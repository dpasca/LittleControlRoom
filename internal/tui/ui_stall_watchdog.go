package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"strings"
	"sync"
	"time"
)

const (
	uiStallCaptureThreshold     = 2 * time.Second
	uiStallWatchdogPollInterval = 250 * time.Millisecond
	uiStallPhaseHistoryLimit    = 96
	uiStallRecentPhaseLimit     = 24
	uiStallBlockProfileRate     = 1_000_000
	uiStallMutexProfileFraction = 5
)

type uiStallContext struct {
	FocusedPane         string `json:"focused_pane,omitempty"`
	SelectedProjectPath string `json:"selected_project_path,omitempty"`
	DetailPath          string `json:"detail_path,omitempty"`
	CodexVisibleProject string `json:"codex_visible_project,omitempty"`
	Status              string `json:"status,omitempty"`
	Width               int    `json:"width,omitempty"`
	Height              int    `json:"height,omitempty"`
}

type uiPhaseBreadcrumb struct {
	At          time.Time `json:"at"`
	Event       string    `json:"event"`
	Name        string    `json:"name"`
	ProjectPath string    `json:"project_path,omitempty"`
	Detail      string    `json:"detail,omitempty"`
}

type uiActivePhase struct {
	ID          uint64    `json:"-"`
	StartedAt   time.Time `json:"started_at"`
	Name        string    `json:"name"`
	ProjectPath string    `json:"project_path,omitempty"`
	Detail      string    `json:"detail,omitempty"`
}

type uiStallCaptureRecord struct {
	CapturedAt      time.Time `json:"captured_at"`
	StallDuration   string    `json:"stall_duration"`
	Directory       string    `json:"directory,omitempty"`
	SummaryPath     string    `json:"summary_path,omitempty"`
	GoroutinePath   string    `json:"goroutine_path,omitempty"`
	BlockPath       string    `json:"block_path,omitempty"`
	MutexPath       string    `json:"mutex_path,omitempty"`
	LastProgressAt  time.Time `json:"last_progress_at"`
	LastProgress    string    `json:"last_progress,omitempty"`
	Error           string    `json:"error,omitempty"`
	TopActivePhase  string    `json:"top_active_phase,omitempty"`
	ActiveProject   string    `json:"active_project,omitempty"`
	ArtifactRootDir string    `json:"artifact_root_dir,omitempty"`
}

type uiStallArtifactSummary struct {
	PID             int                 `json:"pid"`
	CapturedAt      time.Time           `json:"captured_at"`
	StallDuration   string              `json:"stall_duration"`
	Threshold       string              `json:"threshold"`
	LastProgressAt  time.Time           `json:"last_progress_at"`
	LastProgress    string              `json:"last_progress,omitempty"`
	Context         uiStallContext      `json:"context"`
	ActivePhases    []uiActivePhase     `json:"active_phases,omitempty"`
	RecentPhases    []uiPhaseBreadcrumb `json:"recent_phases,omitempty"`
	ArtifactRootDir string              `json:"artifact_root_dir,omitempty"`
	GoroutinePath   string              `json:"goroutine_path,omitempty"`
	BlockPath       string              `json:"block_path,omitempty"`
	MutexPath       string              `json:"mutex_path,omitempty"`
	ProfileErrors   map[string]string   `json:"profile_errors,omitempty"`
}

type uiStallArtifactIO struct {
	mkdirAll         func(path string, perm os.FileMode) error
	writeFile        func(path string, data []byte, perm os.FileMode) error
	goroutineProfile func() ([]byte, error)
	blockProfile     func() ([]byte, error)
	mutexProfile     func() ([]byte, error)
}

type uiStallDiagnosticsSnapshot struct {
	Enabled         bool
	Threshold       time.Duration
	ArtifactRootDir string
	LastProgressAt  time.Time
	LastProgress    string
	Context         uiStallContext
	ActivePhases    []uiActivePhase
	RecentPhases    []uiPhaseBreadcrumb
	LastCapture     uiStallCaptureRecord
	HaveLastCapture bool
	CaptureInFlight bool
}

type uiStallDiagnostics struct {
	mu sync.Mutex

	pid                 int
	artifactRootDir     string
	captureThreshold    time.Duration
	watchdogPoll        time.Duration
	io                  uiStallArtifactIO
	startOnce           sync.Once
	started             bool
	progressSeq         uint64
	nextPhaseID         uint64
	capturedProgressSeq uint64
	captureInFlight     bool
	lastProgressAt      time.Time
	lastProgress        string
	context             uiStallContext
	activePhases        []uiActivePhase
	phaseHistory        []uiPhaseBreadcrumb
	lastCapture         uiStallCaptureRecord
	haveLastCapture     bool
}

func newUIStallDiagnostics(homeDir string, pid int) *uiStallDiagnostics {
	return &uiStallDiagnostics{
		pid:              pid,
		artifactRootDir:  defaultUIStallArtifactRoot(homeDir),
		captureThreshold: uiStallCaptureThreshold,
		watchdogPoll:     uiStallWatchdogPollInterval,
		io:               defaultUIStallArtifactIO(),
	}
}

func defaultUIStallArtifactRoot(homeDir string) string {
	homeDir = strings.TrimSpace(homeDir)
	if homeDir == "" {
		return filepath.Join(os.TempDir(), "lcroom-stall-captures")
	}
	return filepath.Join(homeDir, ".little-control-room", "stall-captures")
}

func defaultUIStallArtifactIO() uiStallArtifactIO {
	return uiStallArtifactIO{
		mkdirAll:  os.MkdirAll,
		writeFile: os.WriteFile,
		goroutineProfile: func() ([]byte, error) {
			return uiProfileBytes("goroutine", 2)
		},
		blockProfile: func() ([]byte, error) {
			return uiProfileBytes("block", 1)
		},
		mutexProfile: func() ([]byte, error) {
			return uiProfileBytes("mutex", 1)
		},
	}
}

func uiProfileBytes(name string, debug int) ([]byte, error) {
	profile := pprof.Lookup(name)
	if profile == nil {
		return nil, fmt.Errorf("%s profile unavailable", name)
	}
	var buf bytes.Buffer
	if err := profile.WriteTo(&buf, debug); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (d *uiStallDiagnostics) start(ctx context.Context) {
	if d == nil {
		return
	}
	d.startOnce.Do(func() {
		if ctx == nil {
			ctx = context.Background()
		}
		runtime.SetBlockProfileRate(uiStallBlockProfileRate)
		runtime.SetMutexProfileFraction(uiStallMutexProfileFraction)
		d.mu.Lock()
		d.started = true
		d.mu.Unlock()
		go d.watch(ctx)
	})
}

func (d *uiStallDiagnostics) watch(ctx context.Context) {
	ticker := time.NewTicker(d.watchdogPoll)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			d.captureIfStalled(now)
		}
	}
}

func (d *uiStallDiagnostics) noteProgress(now time.Time, label string, ctx uiStallContext) {
	if d == nil {
		return
	}
	if now.IsZero() {
		now = time.Now()
	}
	label = strings.TrimSpace(label)

	d.mu.Lock()
	defer d.mu.Unlock()
	d.progressSeq++
	d.lastProgressAt = now
	d.lastProgress = label
	d.context = ctx
}

func (d *uiStallDiagnostics) enterPhase(now time.Time, name, projectPath, detail string, ctx uiStallContext) func() {
	if d == nil {
		return func() {}
	}
	if now.IsZero() {
		now = time.Now()
	}
	name = strings.TrimSpace(name)
	projectPath = strings.TrimSpace(projectPath)
	detail = strings.TrimSpace(detail)

	d.mu.Lock()
	d.progressSeq++
	d.lastProgressAt = now
	if name != "" {
		d.lastProgress = name
	}
	d.context = ctx
	d.nextPhaseID++
	phase := uiActivePhase{
		ID:          d.nextPhaseID,
		StartedAt:   now,
		Name:        name,
		ProjectPath: projectPath,
		Detail:      detail,
	}
	d.activePhases = append(d.activePhases, phase)
	d.appendPhaseHistoryLocked(uiPhaseBreadcrumb{
		At:          now,
		Event:       "enter",
		Name:        name,
		ProjectPath: projectPath,
		Detail:      detail,
	})
	d.mu.Unlock()

	return func() {
		endAt := time.Now()
		d.mu.Lock()
		defer d.mu.Unlock()
		d.progressSeq++
		d.lastProgressAt = endAt
		if name != "" {
			d.lastProgress = name + " done"
		}
		d.context = ctx
		for i := len(d.activePhases) - 1; i >= 0; i-- {
			if d.activePhases[i].ID != phase.ID {
				continue
			}
			d.activePhases = append(d.activePhases[:i], d.activePhases[i+1:]...)
			break
		}
		d.appendPhaseHistoryLocked(uiPhaseBreadcrumb{
			At:          endAt,
			Event:       "exit",
			Name:        name,
			ProjectPath: projectPath,
			Detail:      detail,
		})
	}
}

func (d *uiStallDiagnostics) appendPhaseHistoryLocked(event uiPhaseBreadcrumb) {
	if strings.TrimSpace(event.Name) == "" {
		return
	}
	d.phaseHistory = append(d.phaseHistory, event)
	if len(d.phaseHistory) > uiStallPhaseHistoryLimit {
		d.phaseHistory = append([]uiPhaseBreadcrumb(nil), d.phaseHistory[len(d.phaseHistory)-uiStallPhaseHistoryLimit:]...)
	}
}

func (d *uiStallDiagnostics) captureIfStalled(now time.Time) {
	if d == nil {
		return
	}
	if now.IsZero() {
		now = time.Now()
	}

	d.mu.Lock()
	if !d.started || d.captureInFlight || d.lastProgressAt.IsZero() {
		d.mu.Unlock()
		return
	}
	stallDuration := now.Sub(d.lastProgressAt)
	if stallDuration < d.captureThreshold || d.capturedProgressSeq == d.progressSeq {
		d.mu.Unlock()
		return
	}
	d.captureInFlight = true
	d.capturedProgressSeq = d.progressSeq
	snapshot := d.snapshotLocked(now)
	d.mu.Unlock()

	record := d.captureStallArtifacts(snapshot)

	d.mu.Lock()
	d.captureInFlight = false
	d.lastCapture = record
	d.haveLastCapture = true
	d.mu.Unlock()
}

func (d *uiStallDiagnostics) snapshotLocked(now time.Time) uiStallDiagnosticsSnapshot {
	active := append([]uiActivePhase(nil), d.activePhases...)
	history := append([]uiPhaseBreadcrumb(nil), d.phaseHistory...)
	if len(history) > uiStallRecentPhaseLimit {
		history = history[len(history)-uiStallRecentPhaseLimit:]
	}
	return uiStallDiagnosticsSnapshot{
		Enabled:         d.started,
		Threshold:       d.captureThreshold,
		ArtifactRootDir: d.artifactRootDir,
		LastProgressAt:  d.lastProgressAt,
		LastProgress:    d.lastProgress,
		Context:         d.context,
		ActivePhases:    active,
		RecentPhases:    history,
		CaptureInFlight: d.captureInFlight,
	}
}

func (d *uiStallDiagnostics) snapshot() uiStallDiagnosticsSnapshot {
	if d == nil {
		return uiStallDiagnosticsSnapshot{}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	snapshot := d.snapshotLocked(time.Now())
	snapshot.LastCapture = d.lastCapture
	snapshot.HaveLastCapture = d.haveLastCapture
	return snapshot
}

func (d *uiStallDiagnostics) captureStallArtifacts(snapshot uiStallDiagnosticsSnapshot) uiStallCaptureRecord {
	record := uiStallCaptureRecord{
		CapturedAt:      time.Now(),
		LastProgressAt:  snapshot.LastProgressAt,
		LastProgress:    snapshot.LastProgress,
		ArtifactRootDir: snapshot.ArtifactRootDir,
	}
	if !snapshot.LastProgressAt.IsZero() {
		record.StallDuration = record.CapturedAt.Sub(snapshot.LastProgressAt).Round(10 * time.Millisecond).String()
	}
	if len(snapshot.ActivePhases) > 0 {
		top := snapshot.ActivePhases[len(snapshot.ActivePhases)-1]
		record.TopActivePhase = strings.TrimSpace(top.Name)
		record.ActiveProject = strings.TrimSpace(top.ProjectPath)
	}

	artifactDir := filepath.Join(snapshot.ArtifactRootDir, uiStallCaptureDirName(record.CapturedAt, record.LastProgressAt))
	record.Directory = artifactDir
	if err := d.io.mkdirAll(artifactDir, 0o755); err != nil {
		record.Error = err.Error()
		return record
	}

	goroutinePath := filepath.Join(artifactDir, "goroutines.txt")
	blockPath := filepath.Join(artifactDir, "block.txt")
	mutexPath := filepath.Join(artifactDir, "mutex.txt")
	summaryPath := filepath.Join(artifactDir, "summary.json")

	record.GoroutinePath = goroutinePath
	record.BlockPath = blockPath
	record.MutexPath = mutexPath
	record.SummaryPath = summaryPath

	profileErrors := map[string]string{}
	writeProfile := func(label, path string, dump func() ([]byte, error)) {
		data, err := dump()
		if err != nil {
			profileErrors[label] = err.Error()
			data = []byte("profile unavailable: " + err.Error() + "\n")
		}
		if writeErr := d.io.writeFile(path, data, 0o644); writeErr != nil {
			profileErrors[label] = writeErr.Error()
		}
	}
	writeProfile("goroutine", goroutinePath, d.io.goroutineProfile)
	writeProfile("block", blockPath, d.io.blockProfile)
	writeProfile("mutex", mutexPath, d.io.mutexProfile)

	summary := uiStallArtifactSummary{
		PID:             d.pid,
		CapturedAt:      record.CapturedAt,
		StallDuration:   record.StallDuration,
		Threshold:       snapshot.Threshold.String(),
		LastProgressAt:  snapshot.LastProgressAt,
		LastProgress:    snapshot.LastProgress,
		Context:         snapshot.Context,
		ActivePhases:    snapshot.ActivePhases,
		RecentPhases:    snapshot.RecentPhases,
		ArtifactRootDir: snapshot.ArtifactRootDir,
		GoroutinePath:   goroutinePath,
		BlockPath:       blockPath,
		MutexPath:       mutexPath,
	}
	if len(profileErrors) > 0 {
		summary.ProfileErrors = profileErrors
		if record.Error == "" {
			record.Error = strings.Join(mapsSorted(profileErrors), "; ")
		}
	}
	data, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		if record.Error == "" {
			record.Error = err.Error()
		}
		_ = d.io.writeFile(summaryPath, []byte("summary marshal failed: "+err.Error()+"\n"), 0o644)
		return record
	}
	if err := d.io.writeFile(summaryPath, append(data, '\n'), 0o644); err != nil && record.Error == "" {
		record.Error = err.Error()
	}
	return record
}

func mapsSorted(values map[string]string) []string {
	if len(values) == 0 {
		return nil
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sortStrings(keys)
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, key+": "+values[key])
	}
	return out
}

func sortStrings(values []string) {
	if len(values) < 2 {
		return
	}
	for i := 1; i < len(values); i++ {
		j := i
		for j > 0 && values[j] < values[j-1] {
			values[j], values[j-1] = values[j-1], values[j]
			j--
		}
	}
}

func uiStallCaptureDirName(capturedAt, lastProgressAt time.Time) string {
	ts := capturedAt.Format("20060102-150405.000")
	if !lastProgressAt.IsZero() {
		return fmt.Sprintf("%s-stall-%s", ts, capturedAt.Sub(lastProgressAt).Round(10*time.Millisecond))
	}
	return ts + "-stall"
}

func uiMessageLabel(msg any) string {
	if msg == nil {
		return "nil"
	}
	label := fmt.Sprintf("%T", msg)
	if idx := strings.LastIndex(label, "."); idx >= 0 && idx+1 < len(label) {
		return label[idx+1:]
	}
	return label
}

func (m *Model) EnableUIStallWatchdog() {
	if m == nil || m.uiDiagnostics == nil {
		return
	}
	m.uiDiagnostics.start(m.ctx)
}

func (m Model) uiStallContext() uiStallContext {
	ctx := uiStallContext{
		FocusedPane: string(m.focusedPane),
		Status:      strings.TrimSpace(m.status),
		Width:       m.width,
		Height:      m.height,
	}
	if project, ok := m.selectedProject(); ok {
		ctx.SelectedProjectPath = strings.TrimSpace(project.Path)
	}
	ctx.DetailPath = strings.TrimSpace(m.detail.Summary.Path)
	ctx.CodexVisibleProject = strings.TrimSpace(m.codexVisibleProject)
	return ctx
}

func (m Model) noteUIProgress(label string) {
	if m.uiDiagnostics == nil {
		return
	}
	m.uiDiagnostics.noteProgress(m.currentTime(), label, m.uiStallContext())
}

func (m Model) beginUIPhase(name, projectPath, detail string) func() {
	if m.uiDiagnostics == nil {
		return func() {}
	}
	return m.uiDiagnostics.enterPhase(m.currentTime(), name, projectPath, detail, m.uiStallContext())
}

func (m Model) uiStallDiagnosticsSnapshot() uiStallDiagnosticsSnapshot {
	if m.uiDiagnostics == nil {
		return uiStallDiagnosticsSnapshot{}
	}
	return m.uiDiagnostics.snapshot()
}
