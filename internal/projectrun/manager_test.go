package projectrun

import (
	"context"
	"strings"
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
