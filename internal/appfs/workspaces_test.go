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
	codexHomePath := filepath.Join(root, "lcroom-codex-home-old")
	for _, path := range []string{oldPath, newPath, taskPath, codexHomePath} {
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
	if err := os.Chtimes(codexHomePath, oldTime, oldTime); err != nil {
		t.Fatalf("chtimes old codex home overlay: %v", err)
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
	if _, err := os.Stat(codexHomePath); err != nil {
		t.Fatalf("expected codex home overlay to remain for resumable skill paths, stat err = %v", err)
	}
}

func TestCleanupStaleCodexHomeOverlaysUsesSeparateRetention(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), ".little-control-room")
	root, err := EnsureInternalWorkspaceRoot(dataDir)
	if err != nil {
		t.Fatalf("EnsureInternalWorkspaceRoot() error = %v", err)
	}

	oldOverlay := filepath.Join(root, "lcroom-codex-home-old")
	freshOverlay := filepath.Join(root, "lcroom-codex-home-fresh")
	taskPath := filepath.Join(root, "lcroom-agent-task-old")
	for _, path := range []string{oldOverlay, freshOverlay, taskPath} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatalf("mkdir %s: %v", path, err)
		}
	}
	oldTime := time.Now().Add(-8 * 24 * time.Hour)
	for _, path := range []string{oldOverlay, taskPath} {
		if err := os.Chtimes(path, oldTime, oldTime); err != nil {
			t.Fatalf("chtimes %s: %v", path, err)
		}
	}

	removed, err := CleanupStaleCodexHomeOverlays(dataDir, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("CleanupStaleCodexHomeOverlays() error = %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	if _, err := os.Stat(oldOverlay); !os.IsNotExist(err) {
		t.Fatalf("old Codex overlay should be removed, stat err = %v", err)
	}
	for _, path := range []string{freshOverlay, taskPath} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("retained path %s missing: %v", path, err)
		}
	}
}
