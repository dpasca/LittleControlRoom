package projectrun

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestStopNormalizesUserRequestedTermination(t *testing.T) {
	dir := t.TempDir()
	manager := NewManager()
	defer func() { _ = manager.CloseAll() }()

	_, err := manager.Start(StartRequest{
		ProjectPath: dir,
		Command:     "sleep 30",
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := WaitUntilRunning(ctx, manager, dir); err != nil {
		t.Fatalf("WaitUntilRunning() error = %v", err)
	}

	if err := manager.Stop(dir); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		snapshot, err := manager.Snapshot(dir)
		if err != nil {
			t.Fatalf("Snapshot() error = %v", err)
		}
		if snapshot.Running {
			time.Sleep(25 * time.Millisecond)
			continue
		}
		if snapshot.ExitCodeKnown {
			t.Fatalf("ExitCodeKnown = true, want false after user stop: %+v", snapshot)
		}
		if strings.TrimSpace(snapshot.LastError) != "" {
			t.Fatalf("LastError = %q, want empty after user stop", snapshot.LastError)
		}
		if snapshot.ExitedAt.IsZero() {
			t.Fatalf("ExitedAt = zero, want stop timestamp")
		}
		return
	}

	snapshot, err := manager.Snapshot(dir)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	t.Fatalf("runtime did not stop in time: %+v", snapshot)
}

func TestStartSerializesConcurrentStartsForSameProject(t *testing.T) {
	dir := t.TempDir()
	manager := NewManager()
	defer func() { _ = manager.CloseAll() }()

	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	manager.prepare = func(projectPath, command string) error {
		once.Do(func() { close(entered) })
		<-release
		return nil
	}

	firstDone := make(chan error, 1)
	go func() {
		_, err := manager.Start(StartRequest{
			ProjectPath: dir,
			Command:     "sleep 30",
		})
		firstDone <- err
	}()

	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("first start never entered runtime preparation")
	}

	secondDone := make(chan error, 1)
	go func() {
		_, err := manager.Start(StartRequest{
			ProjectPath: dir,
			Command:     "sleep 30",
		})
		secondDone <- err
	}()

	select {
	case err := <-secondDone:
		t.Fatalf("second start returned before the first finished preparing: %v", err)
	case <-time.After(150 * time.Millisecond):
	}

	close(release)

	select {
	case err := <-firstDone:
		if err != nil {
			t.Fatalf("first Start() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("first start did not finish")
	}

	select {
	case err := <-secondDone:
		if !errors.Is(err, ErrAlreadyRunning) {
			t.Fatalf("second Start() error = %v, want ErrAlreadyRunning", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second start did not finish")
	}
}

func TestStartCreateNewAllowsMultipleProcessesForSameProject(t *testing.T) {
	dir := t.TempDir()
	manager := NewManager()
	defer func() { _ = manager.CloseAll() }()

	first, err := manager.Start(StartRequest{
		ProjectPath: dir,
		Command:     "sleep 30",
		Name:        "first",
		CreateNew:   true,
	})
	if err != nil {
		t.Fatalf("first Start() error = %v", err)
	}
	second, err := manager.Start(StartRequest{
		ProjectPath: dir,
		Command:     "sleep 30",
		Name:        "second",
		CreateNew:   true,
	})
	if err != nil {
		t.Fatalf("second Start() error = %v", err)
	}
	if first.ID == "" || second.ID == "" || first.ID == second.ID {
		t.Fatalf("process IDs = %q/%q, want distinct generated IDs", first.ID, second.ID)
	}
	if first.Default || second.Default {
		t.Fatalf("generated process snapshots should not be default: first=%+v second=%+v", first, second)
	}

	snapshots := manager.SnapshotsForProject(dir)
	if len(snapshots) != 2 {
		t.Fatalf("SnapshotsForProject() len = %d, want 2: %+v", len(snapshots), snapshots)
	}
	for _, snapshot := range snapshots {
		if !snapshot.Running {
			t.Fatalf("snapshot should be running: %+v", snapshot)
		}
	}

	if err := manager.StopProcess(dir, first.ID); err != nil {
		t.Fatalf("StopProcess(%q) error = %v", first.ID, err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		firstSnapshot, err := manager.SnapshotProcess(dir, first.ID)
		if err != nil {
			t.Fatalf("first SnapshotProcess() error = %v", err)
		}
		secondSnapshot, err := manager.SnapshotProcess(dir, second.ID)
		if err != nil {
			t.Fatalf("second SnapshotProcess() error = %v", err)
		}
		if !firstSnapshot.Running && secondSnapshot.Running {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	firstSnapshot, _ := manager.SnapshotProcess(dir, first.ID)
	secondSnapshot, _ := manager.SnapshotProcess(dir, second.ID)
	t.Fatalf("stopping first process should leave second running: first=%+v second=%+v", firstSnapshot, secondSnapshot)
}

func TestStartManagedReusesMatchingProcessByDefault(t *testing.T) {
	dir := t.TempDir()
	manager := NewManager()
	defer func() { _ = manager.CloseAll() }()

	first, err := manager.StartManaged(StartRequest{
		ProjectPath:   dir,
		Command:       "sleep 30",
		Name:          "dev-server",
		CreateNew:     true,
		ReuseMatching: true,
	})
	if err != nil {
		t.Fatalf("first StartManaged() error = %v", err)
	}
	if first.Disposition != StartDispositionStarted {
		t.Fatalf("first disposition = %q, want started", first.Disposition)
	}

	second, err := manager.StartManaged(StartRequest{
		ProjectPath:   dir,
		Command:       "sleep 30",
		Name:          "dev-server",
		CreateNew:     true,
		ReuseMatching: true,
	})
	if err != nil {
		t.Fatalf("second StartManaged() error = %v", err)
	}
	if second.Disposition != StartDispositionReused {
		t.Fatalf("second disposition = %q, want reused", second.Disposition)
	}
	if second.Snapshot.ID != first.Snapshot.ID {
		t.Fatalf("reused ID = %q, want first ID %q", second.Snapshot.ID, first.Snapshot.ID)
	}

	running := 0
	for _, snapshot := range manager.SnapshotsForProject(dir) {
		if snapshot.Running {
			running++
		}
	}
	if running != 1 {
		t.Fatalf("running snapshots = %d, want 1: %+v", running, manager.SnapshotsForProject(dir))
	}
}

func TestStartManagedCreateNewCanForceDuplicateProcess(t *testing.T) {
	dir := t.TempDir()
	manager := NewManager()
	defer func() { _ = manager.CloseAll() }()

	first, err := manager.StartManaged(StartRequest{
		ProjectPath:   dir,
		Command:       "sleep 30",
		Name:          "dev-server",
		CreateNew:     true,
		ReuseMatching: true,
	})
	if err != nil {
		t.Fatalf("first StartManaged() error = %v", err)
	}

	second, err := manager.StartManaged(StartRequest{
		ProjectPath: dir,
		Command:     "sleep 30",
		Name:        "dev-server",
		CreateNew:   true,
	})
	if err != nil {
		t.Fatalf("second StartManaged() error = %v", err)
	}
	if second.Disposition != StartDispositionStarted {
		t.Fatalf("second disposition = %q, want started", second.Disposition)
	}
	if second.Snapshot.ID == first.Snapshot.ID {
		t.Fatalf("forced duplicate reused ID %q", second.Snapshot.ID)
	}

	running := 0
	for _, snapshot := range manager.SnapshotsForProject(dir) {
		if snapshot.Running {
			running++
		}
	}
	if running != 2 {
		t.Fatalf("running snapshots = %d, want 2: %+v", running, manager.SnapshotsForProject(dir))
	}
}

func TestStartManagedReplaceExistingMatchingProcess(t *testing.T) {
	dir := t.TempDir()
	manager := NewManager()
	defer func() { _ = manager.CloseAll() }()

	first, err := manager.StartManaged(StartRequest{
		ProjectPath:   dir,
		Command:       "sleep 30",
		Name:          "dev-server",
		CreateNew:     true,
		ReuseMatching: true,
	})
	if err != nil {
		t.Fatalf("first StartManaged() error = %v", err)
	}

	replacement, err := manager.StartManaged(StartRequest{
		ProjectPath:     dir,
		Command:         "sleep 30",
		Name:            "dev-server",
		CreateNew:       true,
		ReuseMatching:   true,
		ReplaceExisting: true,
	})
	if err != nil {
		t.Fatalf("replacement StartManaged() error = %v", err)
	}
	if replacement.Disposition != StartDispositionReplaced {
		t.Fatalf("replacement disposition = %q, want replaced", replacement.Disposition)
	}
	if replacement.ReplacedCount != 1 {
		t.Fatalf("ReplacedCount = %d, want 1", replacement.ReplacedCount)
	}
	if replacement.Snapshot.ID == first.Snapshot.ID {
		t.Fatalf("replacement reused old ID %q", replacement.Snapshot.ID)
	}

	firstSnapshot, err := manager.SnapshotProcess(dir, first.Snapshot.ID)
	if err != nil {
		t.Fatalf("first SnapshotProcess() error = %v", err)
	}
	if firstSnapshot.Running {
		t.Fatalf("first process should be stopped after replace: %+v", firstSnapshot)
	}
	replacementSnapshot, err := manager.SnapshotProcess(dir, replacement.Snapshot.ID)
	if err != nil {
		t.Fatalf("replacement SnapshotProcess() error = %v", err)
	}
	if !replacementSnapshot.Running {
		t.Fatalf("replacement should be running: %+v", replacementSnapshot)
	}
}

func TestStartRunsCommandInRequestedCWD(t *testing.T) {
	dir := t.TempDir()
	frontend := filepath.Join(dir, "frontend")
	if err := os.MkdirAll(frontend, 0o755); err != nil {
		t.Fatal(err)
	}
	manager := NewManager()
	defer func() { _ = manager.CloseAll() }()

	snapshot, err := manager.Start(StartRequest{
		ProjectPath: dir,
		Command:     "pwd; sleep 30",
		CWD:         "frontend",
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if snapshot.CWD != frontend {
		t.Fatalf("snapshot.CWD = %q, want %q", snapshot.CWD, frontend)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		snapshot, err := manager.Snapshot(dir)
		if err != nil {
			t.Fatalf("Snapshot() error = %v", err)
		}
		if len(snapshot.RecentOutput) > 0 && snapshot.RecentOutput[0] == frontend {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	snapshot, err = manager.Snapshot(dir)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	t.Fatalf("recent output = %v, want cwd %q", snapshot.RecentOutput, frontend)
}

func TestCloseAllIgnoresAlreadyStoppedRuntime(t *testing.T) {
	dir := t.TempDir()
	manager := NewManager()
	defer func() { _ = manager.CloseAll() }()

	manager.mu.Lock()
	manager.runtimes[dir] = &managedRuntime{
		projectPath: dir,
		command:     "true",
		running:     false,
		exitedAt:    time.Now(),
	}
	manager.mu.Unlock()

	if err := manager.CloseAll(); err != nil {
		t.Fatalf("CloseAll() error = %v, want nil for stopped runtime", err)
	}
}

func TestRefreshPortsSkipsProcessReadersWithoutRunningRuntime(t *testing.T) {
	processGroupReads := 0
	portReads := 0
	manager := &Manager{
		runtimes: map[string]*managedRuntime{
			"stopped": {
				pid:     100,
				pgid:    100,
				running: false,
				ports:   []int{3000},
			},
		},
		procGroups: func() (map[int]int, error) {
			processGroupReads++
			return map[int]int{}, nil
		},
		portReaders: func() (map[int][]int, error) {
			portReads++
			return map[int][]int{}, nil
		},
	}

	manager.refreshPorts()

	if processGroupReads != 0 {
		t.Fatalf("process group reads = %d, want none while all runtimes are stopped", processGroupReads)
	}
	if portReads != 0 {
		t.Fatalf("port reads = %d, want none while all runtimes are stopped", portReads)
	}
}

func TestRefreshPortsReadsProcessesForRunningRuntime(t *testing.T) {
	processGroupReads := 0
	portReads := 0
	runtime := &managedRuntime{
		pid:     100,
		pgid:    100,
		running: true,
	}
	manager := &Manager{
		runtimes: map[string]*managedRuntime{"running": runtime},
		procGroups: func() (map[int]int, error) {
			processGroupReads++
			return map[int]int{100: 100, 101: 100}, nil
		},
		portReaders: func() (map[int][]int, error) {
			portReads++
			return map[int][]int{101: {5173}}, nil
		},
	}

	manager.refreshPorts()

	if processGroupReads != 1 || portReads != 1 {
		t.Fatalf("reader calls = process groups %d ports %d, want one each", processGroupReads, portReads)
	}
	if len(runtime.ports) != 1 || runtime.ports[0] != 5173 {
		t.Fatalf("runtime ports = %v, want [5173]", runtime.ports)
	}
}

func TestStartRefreshesPortsImmediatelyAfterIdle(t *testing.T) {
	dir := t.TempDir()
	manager := NewManager()
	defer func() { _ = manager.CloseAll() }()
	manager.prepare = func(projectPath, command string) error { return nil }

	var readerMu sync.Mutex
	processGroupReads := 0
	portReads := 0
	manager.procGroups = func() (map[int]int, error) {
		readerMu.Lock()
		processGroupReads++
		readerMu.Unlock()
		return map[int]int{}, nil
	}
	manager.portReaders = func() (map[int][]int, error) {
		readerMu.Lock()
		portReads++
		readerMu.Unlock()

		manager.mu.Lock()
		pid := 0
		for _, runtime := range manager.runtimes {
			if runtime != nil && runtime.running {
				pid = runtime.pid
				break
			}
		}
		manager.mu.Unlock()
		if pid <= 0 {
			return nil, errors.New("port reader invoked before runtime became visible")
		}
		return map[int][]int{pid: {5173}}, nil
	}

	snapshot, err := manager.Start(StartRequest{
		ProjectPath: dir,
		Command:     "sleep 30",
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if len(snapshot.Ports) != 1 || snapshot.Ports[0] != 5173 {
		t.Fatalf("Start() snapshot ports = %v, want immediate [5173]", snapshot.Ports)
	}

	readerMu.Lock()
	defer readerMu.Unlock()
	if processGroupReads < 1 || portReads < 1 {
		t.Fatalf("reader calls = process groups %d ports %d, want an immediate refresh", processGroupReads, portReads)
	}
}

func TestAppendOutputPreservesFirstAnnouncedRuntimeURLs(t *testing.T) {
	dir := t.TempDir()
	manager := NewManager()
	defer func() { _ = manager.CloseAll() }()

	manager.mu.Lock()
	manager.runtimes[dir] = &managedRuntime{projectPath: dir}
	manager.mu.Unlock()

	for i := 0; i < maxAnnouncedURLs+2; i++ {
		manager.appendOutput(dir, fmt.Sprintf("ready http://localhost:%d/", 3000+i))
	}

	snapshot, err := manager.Snapshot(dir)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if len(snapshot.AnnouncedURLs) != maxAnnouncedURLs {
		t.Fatalf("announced URL count = %d, want %d: %v", len(snapshot.AnnouncedURLs), maxAnnouncedURLs, snapshot.AnnouncedURLs)
	}
	if snapshot.AnnouncedURLs[0] != "http://localhost:3000/" {
		t.Fatalf("first announced URL = %q, want first discovered local URL", snapshot.AnnouncedURLs[0])
	}
	if snapshot.AnnouncedURLs[len(snapshot.AnnouncedURLs)-1] != fmt.Sprintf("http://localhost:%d/", 3000+maxAnnouncedURLs-1) {
		t.Fatalf("last announced URL = %q, want the last retained URL before cap", snapshot.AnnouncedURLs[len(snapshot.AnnouncedURLs)-1])
	}
}

func TestExpectedPortsCombinesRuntimeSignals(t *testing.T) {
	ports := ExpectedPorts(
		"PORT=3001 pnpm dev -- --host 0.0.0.0 --port 5173 && echo http://localhost:8080/app",
		[]string{"http://127.0.0.1:9229/debug"},
		[]int{3001, 4444},
	)
	want := []int{3001, 4444, 5173, 8080, 9229}
	if len(ports) != len(want) {
		t.Fatalf("ports = %v, want %v", ports, want)
	}
	for i := range want {
		if ports[i] != want[i] {
			t.Fatalf("ports = %v, want %v", ports, want)
		}
	}
}

func TestExpectedPortsIgnoresUnrelatedNumbers(t *testing.T) {
	ports := ExpectedPorts("pnpm test --runInBand --retries 3001", nil, nil)
	if len(ports) != 0 {
		t.Fatalf("ports = %v, want none for unrelated command numbers", ports)
	}
}
