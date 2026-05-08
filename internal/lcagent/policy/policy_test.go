package policy

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestWorkspaceResolveAllowsNestedNewPath(t *testing.T) {
	root := t.TempDir()
	w, err := NewWorkspace(root, AutonomyLow)
	if err != nil {
		t.Fatal(err)
	}
	got, err := w.Resolve("new/dir/file.txt")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(w.Root, "new", "dir", "file.txt")
	if got != want {
		t.Fatalf("Resolve = %q, want %q", got, want)
	}
}

func TestWorkspaceResolveDeniesParentEscape(t *testing.T) {
	root := t.TempDir()
	w, err := NewWorkspace(root, AutonomyLow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Resolve("../outside.txt"); err == nil {
		t.Fatal("Resolve ../outside.txt succeeded, want error")
	}
}

func TestWorkspaceResolveDeniesSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink setup requires elevated privileges on some Windows hosts")
	}
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "outside")); err != nil {
		t.Fatal(err)
	}
	w, err := NewWorkspace(root, AutonomyLow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Resolve("outside/file.txt"); err == nil {
		t.Fatal("Resolve through escaping symlink succeeded, want error")
	}
}

func TestAutonomyPatchAndCommandPolicy(t *testing.T) {
	w := Workspace{Root: t.TempDir(), Auto: AutonomyOff}
	if err := w.AllowPatch(); err == nil {
		t.Fatal("AllowPatch with off succeeded, want error")
	}
	if err := w.AllowCommand("git diff"); err != nil {
		t.Fatalf("git diff denied: %v", err)
	}
	if err := w.AllowCommand("rm file.txt"); err == nil {
		t.Fatal("rm allowed with auto off, want error")
	}
	low := Workspace{Root: t.TempDir(), Auto: AutonomyLow}
	if err := low.AllowCommandSpec([]string{"go", "test", "./..."}, "", false); err == nil {
		t.Fatal("go test allowed with auto low, want medium-only denial")
	}
	medium := Workspace{Root: t.TempDir(), Auto: AutonomyMedium}
	if err := medium.AllowCommandSpec([]string{"go", "test", "./..."}, "", false); err != nil {
		t.Fatalf("go test denied with auto medium: %v", err)
	}
	if got := ClampTimeout(5*time.Minute, time.Second, 10*time.Second); got != 10*time.Second {
		t.Fatalf("ClampTimeout = %s, want 10s", got)
	}
}
