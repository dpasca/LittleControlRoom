package appfs

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestIsManagedInternalPath(t *testing.T) {
	root := InternalWorkspaceRoot(filepath.Join(t.TempDir(), ".little-control-room"))

	if !IsManagedInternalPath(filepath.Join(root, "lcroom-codex-helper-123"), []string{root}) {
		t.Fatalf("expected managed root child to be detected")
	}
	if !IsManagedInternalPath(filepath.Join(os.TempDir(), "lcroom-codex-helper-legacy"), nil) {
		t.Fatalf("expected legacy helper prefix path to be detected")
	}
	if IsManagedInternalPath(filepath.Join(t.TempDir(), "demo"), []string{root}) {
		t.Fatalf("expected unrelated path to be ignored")
	}
}

func TestCleanupStaleInternalWorkspaces(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), ".little-control-room")
	root, err := EnsureInternalWorkspaceRoot(dataDir)
	if err != nil {
		t.Fatalf("EnsureInternalWorkspaceRoot() error = %v", err)
	}

	oldPath := filepath.Join(root, "lcroom-codex-helper-old")
	newPath := filepath.Join(root, "lcroom-codex-helper-new")
	taskPath := filepath.Join(root, "lcroom-agent-task-old")
	for _, path := range []string{oldPath, newPath, taskPath} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatalf("mkdir %s: %v", path, err)
		}
	}
	oldTime := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(oldPath, oldTime, oldTime); err != nil {
		t.Fatalf("chtimes old workspace: %v", err)
	}
	if err := os.Chtimes(taskPath, oldTime, oldTime); err != nil {
		t.Fatalf("chtimes old agent task workspace: %v", err)
	}

	if err := CleanupStaleInternalWorkspaces(dataDir, 24*time.Hour); err != nil {
		t.Fatalf("CleanupStaleInternalWorkspaces() error = %v", err)
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("expected old workspace to be removed, stat err = %v", err)
	}
	if _, err := os.Stat(newPath); err != nil {
		t.Fatalf("expected fresh workspace to remain, stat err = %v", err)
	}
	if _, err := os.Stat(taskPath); err != nil {
		t.Fatalf("expected agent task workspace to remain for lifecycle GC, stat err = %v", err)
	}
}
