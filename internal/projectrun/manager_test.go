package projectrun

import (
	"context"
	"errors"
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
