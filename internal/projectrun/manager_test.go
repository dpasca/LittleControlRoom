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
