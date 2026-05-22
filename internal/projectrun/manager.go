package projectrun

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"lcroom/internal/keyedmutex"
)

const (
	portRefreshInterval = 2 * time.Second
	maxRecentOutput     = 8
	maxAnnouncedURLs    = 8
	closeAllWaitTimeout = 2 * time.Second
)

var (
	ErrAlreadyRunning = errors.New("project runtime already running")
	ErrNotRunning     = errors.New("project runtime is not running")

	announcedURLPattern = regexp.MustCompile(`https?://[^\s"'<>]+`)
)

type Snapshot struct {
	ID            string
	Name          string
	Default       bool
	ProjectPath   string
	Command       string
	CWD           string
	PID           int
	PGID          int
	Running       bool
	StartedAt     time.Time
	ExitedAt      time.Time
	ExitCode      int
	ExitCodeKnown bool
	LastError     string
	Ports         []int
	ConflictPorts []int
	AnnouncedURLs []string
	RecentOutput  []string
}

type StartRequest struct {
	ProjectPath string
	Command     string
	CWD         string
	ProcessID   string
	Name        string
	CreateNew   bool
}

type Manager struct {
	mu          sync.Mutex
	runtimes    map[string]*managedRuntime
	nextID      int64
	procGroups  processGroupReader
	portReaders portReader
	opLocks     keyedmutex.Locker
	prepare     func(string, string) error
	done        chan struct{}
	doneOnce    sync.Once
}

type managedRuntime struct {
	key            string
	id             string
	name           string
	defaultRuntime bool
	projectPath    string
	command        string
	cwd            string
	process        *exec.Cmd
	pid            int
	pgid           int
	running        bool
	stopRequested  bool
	startedAt      time.Time
	exitedAt       time.Time
	exitCode       int
	exitCodeKnown  bool
	lastError      string
	ports          []int
	announcedURLs  []string
	recentOutput   []string
}

type managedRuntimeStopTarget struct {
	key         string
	projectPath string
	runtime     *managedRuntime
	cmd         *exec.Cmd
}

type processGroupReader func() (map[int]int, error)
type portReader func() (map[int][]int, error)

func NewManager() *Manager {
	manager := &Manager{
		runtimes:    make(map[string]*managedRuntime),
		procGroups:  currentProcessGroups,
		portReaders: currentListeningPorts,
		prepare:     ensureRuntimeDependencies,
		done:        make(chan struct{}),
	}
	go manager.refreshLoop()
	return manager
}

func (m *Manager) CloseAll() error {
	if m == nil {
		return nil
	}
	m.doneOnce.Do(func() { close(m.done) })

	m.mu.Lock()
	targets := make([]managedRuntimeStopTarget, 0, len(m.runtimes))
	for key, runtime := range m.runtimes {
		if runtime == nil || !runtime.running {
			continue
		}
		runtime.stopRequested = true
		targets = append(targets, managedRuntimeStopTarget{
			key:         key,
			projectPath: runtime.projectPath,
			runtime:     runtime,
			cmd:         runtime.process,
		})
	}
	m.mu.Unlock()

	var firstErr error
	for _, target := range targets {
		if err := stopManagedCommand(target.cmd); err != nil {
			m.clearStopRequested(target)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	if err := m.waitForAllStopped(closeAllWaitTimeout); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

func (m *Manager) waitForAllStopped(timeout time.Duration) error {
	if m == nil {
		return nil
	}
	deadline := time.Now().Add(timeout)
	for {
		if !m.anyRunningRuntime() {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("timed out waiting for runtimes to stop")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (m *Manager) anyRunningRuntime() bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, runtime := range m.runtimes {
		if runtime != nil && runtime.running {
			return true
		}
	}
	return false
}

func (m *Manager) Start(req StartRequest) (Snapshot, error) {
	if m == nil {
		return Snapshot{}, errors.New("runtime manager unavailable")
	}
	projectPath := filepath.Clean(strings.TrimSpace(req.ProjectPath))
	command := strings.TrimSpace(req.Command)
	if projectPath == "" {
		return Snapshot{}, errors.New("project path is required")
	}
	if command == "" {
		return Snapshot{}, errors.New("run command is required")
	}
	cwd, err := normalizeRuntimeCWD(projectPath, req.CWD)
	if err != nil {
		return Snapshot{}, err
	}
	runtimeKey := ""
	runtimeID := ""
	defaultRuntime := false
	m.mu.Lock()
	runtimeKey, runtimeID, defaultRuntime = m.runtimeIdentityLocked(projectPath, req)
	m.mu.Unlock()

	lockKey := runtimeKey
	if defaultRuntime {
		lockKey = "project:" + projectPath
	}
	unlock := m.opLocks.Lock(lockKey)
	defer unlock()

	m.mu.Lock()
	existing := m.runtimes[runtimeKey]
	if existing != nil && existing.running {
		snapshot := m.snapshotForRuntimeLocked(existing)
		m.mu.Unlock()
		return snapshot, ErrAlreadyRunning
	}
	m.mu.Unlock()

	prepare := m.prepare
	if prepare == nil {
		prepare = ensureRuntimeDependencies
	}
	if err := prepare(cwd, command); err != nil {
		startErr := fmt.Errorf("prepare runtime dependencies: %w", err)
		m.markRuntimeStartFailure(runtimeKey, runtimeID, req.Name, defaultRuntime, projectPath, command, cwd, startErr)
		return Snapshot{}, startErr
	}

	m.mu.Lock()
	existing = m.runtimes[runtimeKey]
	if existing == nil {
		existing = &managedRuntime{
			key:            runtimeKey,
			id:             runtimeID,
			name:           strings.TrimSpace(req.Name),
			defaultRuntime: defaultRuntime,
			projectPath:    projectPath,
		}
		m.runtimes[runtimeKey] = existing
	}
	existing.key = runtimeKey
	existing.id = runtimeID
	existing.name = strings.TrimSpace(req.Name)
	existing.defaultRuntime = defaultRuntime
	existing.projectPath = projectPath
	existing.command = command
	existing.cwd = cwd
	existing.exitedAt = time.Time{}
	existing.exitCode = 0
	existing.exitCodeKnown = false
	existing.lastError = ""
	existing.stopRequested = false
	existing.ports = nil
	existing.announcedURLs = nil
	existing.recentOutput = nil
	m.mu.Unlock()

	shellProgram := defaultShellProgram()
	cmd := exec.Command(shellProgram, "-lc", command)
	cmd.Dir = cwd
	configureManagedCommand(cmd)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		m.markRuntimeStartFailure(runtimeKey, runtimeID, req.Name, defaultRuntime, projectPath, command, cwd, err)
		return Snapshot{}, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		m.markRuntimeStartFailure(runtimeKey, runtimeID, req.Name, defaultRuntime, projectPath, command, cwd, err)
		return Snapshot{}, err
	}
	if err := cmd.Start(); err != nil {
		m.markRuntimeStartFailure(runtimeKey, runtimeID, req.Name, defaultRuntime, projectPath, command, cwd, err)
		return Snapshot{}, err
	}

	startedAt := time.Now()
	pid := 0
	if cmd.Process != nil {
		pid = cmd.Process.Pid
	}
	pgid := managedProcessGroupID(cmd)

	m.mu.Lock()
	runtime := m.runtimes[runtimeKey]
	if runtime == nil {
		runtime = &managedRuntime{key: runtimeKey}
		m.runtimes[runtimeKey] = runtime
	}
	runtime.key = runtimeKey
	runtime.id = runtimeID
	runtime.name = strings.TrimSpace(req.Name)
	runtime.defaultRuntime = defaultRuntime
	runtime.projectPath = projectPath
	runtime.command = command
	runtime.cwd = cwd
	runtime.process = cmd
	runtime.pid = pid
	runtime.pgid = pgid
	runtime.running = true
	runtime.startedAt = startedAt
	runtime.exitedAt = time.Time{}
	runtime.exitCode = 0
	runtime.exitCodeKnown = false
	runtime.lastError = ""
	runtime.stopRequested = false
	runtime.ports = nil
	runtime.announcedURLs = nil
	runtime.recentOutput = nil
	m.mu.Unlock()

	go m.captureOutput(runtimeKey, stdout)
	go m.captureOutput(runtimeKey, stderr)
	go m.waitForExit(runtimeKey, cmd)
	m.refreshPorts()
	return m.SnapshotProcess(projectPath, runtimeID)
}

func normalizeRuntimeCWD(projectPath, cwd string) (string, error) {
	projectPath = filepath.Clean(strings.TrimSpace(projectPath))
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return projectPath, nil
	}
	if !filepath.IsAbs(cwd) {
		cwd = filepath.Join(projectPath, cwd)
	}
	cwd = filepath.Clean(cwd)
	rel, err := filepath.Rel(projectPath, cwd)
	if err != nil {
		return "", fmt.Errorf("resolve runtime cwd: %w", err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("runtime cwd must stay inside project: %s", cwd)
	}
	return cwd, nil
}

func (m *Manager) runtimeIdentityLocked(projectPath string, req StartRequest) (key, id string, defaultRuntime bool) {
	id = strings.TrimSpace(req.ProcessID)
	if req.CreateNew || id == "" {
		if req.CreateNew {
			m.nextID++
			id = fmt.Sprintf("rt_%d", m.nextID)
			return processRuntimeKey(projectPath, id), id, false
		}
		id = "default"
		return defaultRuntimeKey(projectPath), id, true
	}
	if id == "default" {
		return defaultRuntimeKey(projectPath), id, true
	}
	return processRuntimeKey(projectPath, id), id, false
}

func defaultRuntimeKey(projectPath string) string {
	return "default:" + filepath.Clean(strings.TrimSpace(projectPath))
}

func processRuntimeKey(projectPath, id string) string {
	return "process:" + filepath.Clean(strings.TrimSpace(projectPath)) + ":" + strings.TrimSpace(id)
}

func (m *Manager) Stop(projectPath string) error {
	return m.StopProcess(projectPath, "")
}

func (m *Manager) StopProcess(projectPath, processID string) error {
	if m == nil {
		return nil
	}
	projectPath = filepath.Clean(strings.TrimSpace(projectPath))
	if projectPath == "" {
		return errors.New("project path is required")
	}
	processID = strings.TrimSpace(processID)
	lockKey := "project:" + projectPath
	if processID != "" {
		lockKey = processRuntimeKey(projectPath, processID)
		if processID == "default" {
			lockKey = defaultRuntimeKey(projectPath)
		}
	}
	unlock := m.opLocks.Lock(lockKey)
	defer unlock()

	m.mu.Lock()
	key, runtime := m.stopTargetLocked(projectPath, processID)
	if runtime == nil || !runtime.running {
		m.mu.Unlock()
		return ErrNotRunning
	}
	runtime.stopRequested = true
	target := managedRuntimeStopTarget{
		key:         key,
		projectPath: projectPath,
		runtime:     runtime,
		cmd:         runtime.process,
	}
	m.mu.Unlock()
	if err := stopManagedCommand(target.cmd); err != nil {
		m.clearStopRequested(target)
		return err
	}
	return nil
}

func (m *Manager) StopProject(projectPath string) error {
	if m == nil {
		return nil
	}
	projectPath = filepath.Clean(strings.TrimSpace(projectPath))
	if projectPath == "" {
		return errors.New("project path is required")
	}
	unlock := m.opLocks.Lock("project:" + projectPath)
	defer unlock()

	m.mu.Lock()
	targets := make([]managedRuntimeStopTarget, 0)
	for key, runtime := range m.runtimes {
		if runtime == nil || !runtime.running || runtime.projectPath != projectPath {
			continue
		}
		runtime.stopRequested = true
		targets = append(targets, managedRuntimeStopTarget{
			key:         key,
			projectPath: projectPath,
			runtime:     runtime,
			cmd:         runtime.process,
		})
	}
	m.mu.Unlock()
	if len(targets) == 0 {
		return ErrNotRunning
	}
	var firstErr error
	for _, target := range targets {
		if err := stopManagedCommand(target.cmd); err != nil {
			m.clearStopRequested(target)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

func (m *Manager) stopTargetLocked(projectPath, processID string) (string, *managedRuntime) {
	if processID != "" {
		key := defaultRuntimeKey(projectPath)
		if processID != "default" {
			key = processRuntimeKey(projectPath, processID)
		}
		return key, m.runtimes[key]
	}
	if runtime := m.runtimes[defaultRuntimeKey(projectPath)]; runtime != nil && runtime.running {
		return defaultRuntimeKey(projectPath), runtime
	}
	var selectedKey string
	var selected *managedRuntime
	for key, runtime := range m.runtimes {
		if runtime == nil || !runtime.running || runtime.projectPath != projectPath {
			continue
		}
		if selected == nil || runtime.startedAt.After(selected.startedAt) {
			selectedKey = key
			selected = runtime
		}
	}
	return selectedKey, selected
}

func (m *Manager) clearStopRequested(target managedRuntimeStopTarget) {
	if m == nil || target.runtime == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if current := m.runtimes[target.key]; current == target.runtime {
		current.stopRequested = false
	}
}

func (m *Manager) Snapshot(projectPath string) (Snapshot, error) {
	if m == nil {
		return Snapshot{}, errors.New("runtime manager unavailable")
	}
	projectPath = filepath.Clean(strings.TrimSpace(projectPath))
	if projectPath == "" {
		return Snapshot{}, errors.New("project path is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.snapshotLocked(projectPath), nil
}

func (m *Manager) SnapshotProcess(projectPath, processID string) (Snapshot, error) {
	if m == nil {
		return Snapshot{}, errors.New("runtime manager unavailable")
	}
	projectPath = filepath.Clean(strings.TrimSpace(projectPath))
	processID = strings.TrimSpace(processID)
	if projectPath == "" {
		return Snapshot{}, errors.New("project path is required")
	}
	if processID == "" {
		return m.Snapshot(projectPath)
	}
	key := defaultRuntimeKey(projectPath)
	if processID != "default" {
		key = processRuntimeKey(projectPath, processID)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	runtime := m.runtimes[key]
	if runtime == nil {
		return Snapshot{ID: processID, ProjectPath: projectPath, Default: processID == "default"}, nil
	}
	return m.snapshotForRuntimeLocked(runtime), nil
}

func (m *Manager) SnapshotsForProject(projectPath string) []Snapshot {
	if m == nil {
		return nil
	}
	projectPath = filepath.Clean(strings.TrimSpace(projectPath))
	if projectPath == "" {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	portOwners := m.portOwnersLocked()
	out := make([]Snapshot, 0)
	for _, runtime := range m.runtimes {
		if runtime == nil || runtime.projectPath != projectPath {
			continue
		}
		out = append(out, snapshotFromRuntime(runtime, portOwners))
	}
	sortRuntimeSnapshots(out)
	return out
}

func (m *Manager) Snapshots() []Snapshot {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	portOwners := m.portOwnersLocked()

	out := make([]Snapshot, 0, len(m.runtimes))
	for _, runtime := range m.runtimes {
		out = append(out, snapshotFromRuntime(runtime, portOwners))
	}
	sortRuntimeSnapshots(out)
	return out
}

func (m *Manager) refreshLoop() {
	ticker := time.NewTicker(portRefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			m.refreshPorts()
		case <-m.done:
			return
		}
	}
}

func (m *Manager) refreshPorts() {
	if m == nil {
		return
	}
	processGroups, err := m.procGroups()
	if err != nil {
		return
	}
	listeningPorts, err := m.portReaders()
	if err != nil {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	for _, runtime := range m.runtimes {
		if runtime == nil || !runtime.running {
			continue
		}
		ports := collectPortsForProcessGroup(runtime, processGroups, listeningPorts)
		if equalIntSlices(runtime.ports, ports) {
			continue
		}
		runtime.ports = ports
	}
}

func (m *Manager) captureOutput(runtimeKey string, stream io.Reader) {
	scanner := bufio.NewScanner(stream)
	scanner.Buffer(make([]byte, 0, 1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		m.appendOutput(runtimeKey, line)
	}
}

func (m *Manager) appendOutput(runtimeKey, line string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	runtime := m.runtimes[runtimeKey]
	if runtime == nil {
		return
	}
	runtime.recentOutput = append(runtime.recentOutput, line)
	if len(runtime.recentOutput) > maxRecentOutput {
		runtime.recentOutput = append([]string(nil), runtime.recentOutput[len(runtime.recentOutput)-maxRecentOutput:]...)
	}
	for _, rawURL := range announcedURLPattern.FindAllString(line, -1) {
		url := strings.TrimSpace(strings.TrimRight(rawURL, ".)]}>,;"))
		if url == "" {
			continue
		}
		if slicesContainString(runtime.announcedURLs, url) {
			continue
		}
		if len(runtime.announcedURLs) >= maxAnnouncedURLs {
			continue
		}
		runtime.announcedURLs = append(runtime.announcedURLs, url)
	}
}

func (m *Manager) waitForExit(runtimeKey string, cmd *exec.Cmd) {
	err := cmd.Wait()

	exitCode, exitCodeKnown := exitCodeFromError(err)
	lastError := ""
	if err != nil {
		lastError = strings.TrimSpace(err.Error())
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	runtime := m.runtimes[runtimeKey]
	if runtime == nil {
		return
	}
	stopRequested := runtime.stopRequested
	runtime.running = false
	runtime.stopRequested = false
	runtime.exitedAt = time.Now()
	if stopRequested {
		runtime.exitCode = 0
		runtime.exitCodeKnown = false
		runtime.lastError = ""
	} else {
		runtime.exitCode = exitCode
		runtime.exitCodeKnown = exitCodeKnown
		runtime.lastError = lastError
	}
	runtime.ports = nil
	runtime.process = nil
}

func (m *Manager) markRuntimeStartFailure(runtimeKey, runtimeID, name string, defaultRuntime bool, projectPath, command, cwd string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	runtime := m.runtimes[runtimeKey]
	if runtime == nil {
		runtime = &managedRuntime{key: runtimeKey}
		m.runtimes[runtimeKey] = runtime
	}
	runtime.id = strings.TrimSpace(runtimeID)
	runtime.name = strings.TrimSpace(name)
	runtime.defaultRuntime = defaultRuntime
	runtime.projectPath = projectPath
	runtime.command = command
	runtime.cwd = cwd
	runtime.running = false
	runtime.stopRequested = false
	runtime.exitedAt = time.Now()
	runtime.exitCode = 0
	runtime.exitCodeKnown = false
	runtime.lastError = strings.TrimSpace(err.Error())
	runtime.ports = nil
	runtime.announcedURLs = nil
	runtime.recentOutput = nil
}

func (m *Manager) snapshotLocked(projectPath string) Snapshot {
	if runtime := m.runtimes[defaultRuntimeKey(projectPath)]; runtime != nil {
		return m.snapshotForRuntimeLocked(runtime)
	}
	var selected *managedRuntime
	for _, runtime := range m.runtimes {
		if runtime == nil || runtime.projectPath != projectPath {
			continue
		}
		if selected == nil {
			selected = runtime
			continue
		}
		if runtime.running && !selected.running {
			selected = runtime
			continue
		}
		if runtime.running == selected.running && runtime.startedAt.After(selected.startedAt) {
			selected = runtime
		}
	}
	if selected == nil {
		return Snapshot{ID: "default", ProjectPath: projectPath, Default: true}
	}
	return m.snapshotForRuntimeLocked(selected)
}

func (m *Manager) snapshotForRuntimeLocked(runtime *managedRuntime) Snapshot {
	return snapshotFromRuntime(runtime, m.portOwnersLocked())
}

func (m *Manager) portOwnersLocked() map[int][]string {
	portOwners := map[int][]string{}
	for _, candidate := range m.runtimes {
		if candidate == nil {
			continue
		}
		for _, port := range candidate.ports {
			owner := strings.TrimSpace(candidate.projectPath)
			if candidate.id != "" {
				owner += "\x00" + candidate.id
			}
			portOwners[port] = append(portOwners[port], owner)
		}
	}
	return portOwners
}

func snapshotFromRuntime(runtime *managedRuntime, portOwners map[int][]string) Snapshot {
	if runtime == nil {
		return Snapshot{}
	}
	conflictPorts := []int{}
	for _, port := range runtime.ports {
		owners := uniqueStrings(portOwners[port])
		if len(owners) <= 1 {
			continue
		}
		conflictPorts = append(conflictPorts, port)
	}
	sort.Ints(conflictPorts)
	return Snapshot{
		ID:            firstNonEmptyRuntimeString(runtime.id, "default"),
		Name:          runtime.name,
		Default:       runtime.defaultRuntime,
		ProjectPath:   runtime.projectPath,
		Command:       runtime.command,
		CWD:           runtime.cwd,
		PID:           runtime.pid,
		PGID:          runtime.pgid,
		Running:       runtime.running,
		StartedAt:     runtime.startedAt,
		ExitedAt:      runtime.exitedAt,
		ExitCode:      runtime.exitCode,
		ExitCodeKnown: runtime.exitCodeKnown,
		LastError:     runtime.lastError,
		Ports:         append([]int(nil), runtime.ports...),
		ConflictPorts: conflictPorts,
		AnnouncedURLs: append([]string(nil), runtime.announcedURLs...),
		RecentOutput:  append([]string(nil), runtime.recentOutput...),
	}
}

func stopManagedCommand(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return ErrNotRunning
	}
	if err := terminateManagedCommand(cmd); err != nil {
		return err
	}
	return nil
}

func collectPortsForProcessGroup(runtime *managedRuntime, processGroups map[int]int, listeningPorts map[int][]int) []int {
	if runtime == nil {
		return nil
	}
	seen := map[int]struct{}{}
	for pid, ports := range listeningPorts {
		if !runtimeOwnsPID(runtime, pid, processGroups) {
			continue
		}
		for _, port := range ports {
			seen[port] = struct{}{}
		}
	}
	out := make([]int, 0, len(seen))
	for port := range seen {
		out = append(out, port)
	}
	sort.Ints(out)
	return out
}

func runtimeOwnsPID(runtime *managedRuntime, pid int, processGroups map[int]int) bool {
	if runtime == nil {
		return false
	}
	if pid == runtime.pid {
		return true
	}
	pgid, ok := processGroups[pid]
	if !ok {
		return false
	}
	if runtime.pgid != 0 && pgid == runtime.pgid {
		return true
	}
	return false
}

func equalIntSlices(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func uniqueStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func firstNonEmptyRuntimeString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func sortRuntimeSnapshots(out []Snapshot) {
	sort.Slice(out, func(i, j int) bool {
		if out[i].ProjectPath == out[j].ProjectPath {
			if out[i].Running != out[j].Running {
				return out[i].Running
			}
			if out[i].Default != out[j].Default {
				return out[i].Default
			}
			if out[i].StartedAt.Equal(out[j].StartedAt) {
				return out[i].ID < out[j].ID
			}
			return out[i].StartedAt.After(out[j].StartedAt)
		}
		return out[i].ProjectPath < out[j].ProjectPath
	})
}

func slicesContainString(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

func defaultShellProgram() string {
	shell := strings.TrimSpace(os.Getenv("SHELL"))
	if shell != "" {
		return shell
	}
	if _, err := os.Stat("/bin/zsh"); err == nil {
		return "/bin/zsh"
	}
	return "/bin/sh"
}

func exitCodeFromError(err error) (int, bool) {
	if err == nil {
		return 0, true
	}
	var exitCoder interface{ ExitCode() int }
	if errors.As(err, &exitCoder) {
		return exitCoder.ExitCode(), true
	}
	return 0, false
}

func WaitUntilRunning(ctx context.Context, manager *Manager, projectPath string) error {
	if manager == nil {
		return errors.New("runtime manager unavailable")
	}
	projectPath = filepath.Clean(strings.TrimSpace(projectPath))
	if projectPath == "" {
		return errors.New("project path is required")
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		snapshot, err := manager.Snapshot(projectPath)
		if err != nil {
			return err
		}
		if snapshot.Running {
			return nil
		}
		if snapshot.ExitCodeKnown || snapshot.LastError != "" {
			return errors.New("project runtime failed to stay running")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
