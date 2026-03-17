package projectrun

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	portRefreshInterval = 2 * time.Second
	maxRecentOutput     = 8
	maxAnnouncedURLs    = 4
	closeAllWaitTimeout = 2 * time.Second
)

var (
	ErrAlreadyRunning = errors.New("project runtime already running")
	ErrNotRunning     = errors.New("project runtime is not running")

	announcedURLPattern = regexp.MustCompile(`https?://[^\s"'<>]+`)
)

type Snapshot struct {
	ProjectPath   string
	Command       string
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
}

type Manager struct {
	mu          sync.Mutex
	runtimes    map[string]*managedRuntime
	procGroups  processGroupReader
	portReaders portReader
	done        chan struct{}
}

type managedRuntime struct {
	projectPath   string
	command       string
	process       *exec.Cmd
	pid           int
	pgid          int
	running       bool
	startedAt     time.Time
	exitedAt      time.Time
	exitCode      int
	exitCodeKnown bool
	lastError     string
	ports         []int
	announcedURLs []string
	recentOutput  []string
}

type processGroupReader func() (map[int]int, error)
type portReader func() (map[int][]int, error)

func NewManager() *Manager {
	manager := &Manager{
		runtimes:    make(map[string]*managedRuntime),
		procGroups:  currentProcessGroups,
		portReaders: currentListeningPorts,
		done:        make(chan struct{}),
	}
	go manager.refreshLoop()
	return manager
}

func (m *Manager) CloseAll() error {
	if m == nil {
		return nil
	}
	select {
	case <-m.done:
	default:
		close(m.done)
	}

	m.mu.Lock()
	runtimes := make([]*managedRuntime, 0, len(m.runtimes))
	for _, runtime := range m.runtimes {
		runtimes = append(runtimes, runtime)
	}
	m.mu.Unlock()

	var firstErr error
	for _, runtime := range runtimes {
		if err := stopManagedRuntime(runtime); err != nil && firstErr == nil {
			firstErr = err
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

	m.mu.Lock()
	existing := m.runtimes[projectPath]
	if existing != nil && existing.running {
		snapshot := m.snapshotLocked(projectPath)
		m.mu.Unlock()
		return snapshot, ErrAlreadyRunning
	}
	if existing == nil {
		existing = &managedRuntime{projectPath: projectPath}
		m.runtimes[projectPath] = existing
	}
	existing.command = command
	existing.exitedAt = time.Time{}
	existing.exitCode = 0
	existing.exitCodeKnown = false
	existing.lastError = ""
	existing.ports = nil
	existing.announcedURLs = nil
	existing.recentOutput = nil
	m.mu.Unlock()

	shellProgram := defaultShellProgram()
	cmd := exec.Command(shellProgram, "-lc", command)
	cmd.Dir = projectPath
	configureManagedCommand(cmd)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		m.markRuntimeStartFailure(projectPath, command, err)
		return Snapshot{}, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		m.markRuntimeStartFailure(projectPath, command, err)
		return Snapshot{}, err
	}
	if err := cmd.Start(); err != nil {
		m.markRuntimeStartFailure(projectPath, command, err)
		return Snapshot{}, err
	}

	startedAt := time.Now()
	pid := 0
	if cmd.Process != nil {
		pid = cmd.Process.Pid
	}
	pgid := managedProcessGroupID(cmd)

	m.mu.Lock()
	runtime := m.runtimes[projectPath]
	if runtime == nil {
		runtime = &managedRuntime{projectPath: projectPath}
		m.runtimes[projectPath] = runtime
	}
	runtime.command = command
	runtime.process = cmd
	runtime.pid = pid
	runtime.pgid = pgid
	runtime.running = true
	runtime.startedAt = startedAt
	runtime.exitedAt = time.Time{}
	runtime.exitCode = 0
	runtime.exitCodeKnown = false
	runtime.lastError = ""
	runtime.ports = nil
	runtime.announcedURLs = nil
	runtime.recentOutput = nil
	m.mu.Unlock()

	go m.captureOutput(projectPath, stdout)
	go m.captureOutput(projectPath, stderr)
	go m.waitForExit(projectPath, cmd)
	m.refreshPorts()
	return m.Snapshot(projectPath)
}

func (m *Manager) Stop(projectPath string) error {
	if m == nil {
		return nil
	}
	projectPath = filepath.Clean(strings.TrimSpace(projectPath))
	if projectPath == "" {
		return errors.New("project path is required")
	}

	m.mu.Lock()
	runtime := m.runtimes[projectPath]
	m.mu.Unlock()
	if runtime == nil || !runtime.running {
		return ErrNotRunning
	}
	return stopManagedRuntime(runtime)
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

func (m *Manager) Snapshots() []Snapshot {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	portOwners := map[int][]string{}
	for _, runtime := range m.runtimes {
		for _, port := range runtime.ports {
			portOwners[port] = append(portOwners[port], runtime.projectPath)
		}
	}

	out := make([]Snapshot, 0, len(m.runtimes))
	for _, runtime := range m.runtimes {
		out = append(out, snapshotFromRuntime(runtime, portOwners))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ProjectPath == out[j].ProjectPath {
			return out[i].StartedAt.After(out[j].StartedAt)
		}
		return out[i].ProjectPath < out[j].ProjectPath
	})
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

func (m *Manager) captureOutput(projectPath string, stream io.Reader) {
	scanner := bufio.NewScanner(stream)
	scanner.Buffer(make([]byte, 0, 1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		m.appendOutput(projectPath, line)
	}
}

func (m *Manager) appendOutput(projectPath, line string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	runtime := m.runtimes[projectPath]
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
		runtime.announcedURLs = append(runtime.announcedURLs, url)
		if len(runtime.announcedURLs) > maxAnnouncedURLs {
			runtime.announcedURLs = append([]string(nil), runtime.announcedURLs[len(runtime.announcedURLs)-maxAnnouncedURLs:]...)
		}
	}
}

func (m *Manager) waitForExit(projectPath string, cmd *exec.Cmd) {
	err := cmd.Wait()

	exitCode, exitCodeKnown := exitCodeFromError(err)
	lastError := ""
	if err != nil {
		lastError = strings.TrimSpace(err.Error())
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	runtime := m.runtimes[projectPath]
	if runtime == nil {
		return
	}
	runtime.running = false
	runtime.exitedAt = time.Now()
	runtime.exitCode = exitCode
	runtime.exitCodeKnown = exitCodeKnown
	runtime.lastError = lastError
	runtime.ports = nil
	runtime.process = nil
}

func (m *Manager) markRuntimeStartFailure(projectPath, command string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	runtime := m.runtimes[projectPath]
	if runtime == nil {
		runtime = &managedRuntime{projectPath: projectPath}
		m.runtimes[projectPath] = runtime
	}
	runtime.command = command
	runtime.running = false
	runtime.exitedAt = time.Now()
	runtime.exitCode = 0
	runtime.exitCodeKnown = false
	runtime.lastError = strings.TrimSpace(err.Error())
	runtime.ports = nil
	runtime.announcedURLs = nil
	runtime.recentOutput = nil
}

func (m *Manager) snapshotLocked(projectPath string) Snapshot {
	runtime := m.runtimes[projectPath]
	if runtime == nil {
		return Snapshot{ProjectPath: projectPath}
	}
	portOwners := map[int][]string{}
	for _, candidate := range m.runtimes {
		for _, port := range candidate.ports {
			portOwners[port] = append(portOwners[port], candidate.projectPath)
		}
	}
	return snapshotFromRuntime(runtime, portOwners)
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
		ProjectPath:   runtime.projectPath,
		Command:       runtime.command,
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

func stopManagedRuntime(runtime *managedRuntime) error {
	if runtime == nil || runtime.process == nil || runtime.process.Process == nil {
		return ErrNotRunning
	}
	if err := terminateManagedCommand(runtime.process); err != nil {
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
