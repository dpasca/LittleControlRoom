package gitlock

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckIndexLockReportsRootLock(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repoPath := filepath.Join(t.TempDir(), "repo")
	initRepo(t, repoPath)
	lockPath := filepath.Join(repoPath, ".git", "index.lock")
	if err := os.WriteFile(lockPath, []byte("locked\n"), 0o644); err != nil {
		t.Fatalf("write lock: %v", err)
	}

	err := CheckIndexLock(ctx, repoPath)
	if err == nil {
		t.Fatal("CheckIndexLock() error = nil, want lock error")
	}
	if !strings.Contains(err.Error(), lockPath) || !strings.Contains(err.Error(), "remove the stale lock") {
		t.Fatalf("CheckIndexLock() error = %q, want lock path guidance", err)
	}
}

func TestExistingModuleIndexLocksFindsNestedLocks(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repoPath := filepath.Join(t.TempDir(), "repo")
	initRepo(t, repoPath)
	lockPath := filepath.Join(repoPath, ".git", "modules", "externals", "zlib", "index.lock")
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		t.Fatalf("mkdir lock parent: %v", err)
	}
	if err := os.WriteFile(lockPath, []byte("locked\n"), 0o644); err != nil {
		t.Fatalf("write lock: %v", err)
	}

	locks, err := ExistingModuleIndexLocks(ctx, repoPath)
	if err != nil {
		t.Fatalf("ExistingModuleIndexLocks() error = %v", err)
	}
	if len(locks) != 1 || locks[0] != lockPath {
		t.Fatalf("ExistingModuleIndexLocks() = %#v, want %q", locks, lockPath)
	}
}

func TestLockPathFromOutputParsesGitError(t *testing.T) {
	t.Parallel()

	const output = "fatal: Unable to create '/tmp/repo/.git/index.lock': File exists.\n\nAnother git process seems to be running in this repository."
	lockPath, ok := LockPathFromOutput(output)
	if !ok {
		t.Fatal("LockPathFromOutput() ok = false, want true")
	}
	if lockPath != "/tmp/repo/.git/index.lock" {
		t.Fatalf("LockPathFromOutput() path = %q", lockPath)
	}
}

func initRepo(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	cmd := exec.Command("git", "-C", path, "init", "--quiet")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, strings.TrimSpace(string(out)))
	}
}
