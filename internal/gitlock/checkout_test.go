package gitlock

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestWaitForIndexLocksReleased(t *testing.T) {
	t.Parallel()
	lock := filepath.Join(t.TempDir(), "index.lock")
	if err := os.WriteFile(lock, []byte("in use"), 0o600); err != nil {
		t.Fatal(err)
	}
	removed := make(chan error, 1)
	go func() {
		time.Sleep(30 * time.Millisecond)
		removed <- os.Remove(lock)
	}()
	if err := waitForIndexLocks(context.Background(), []string{lock}, time.Second, 5*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if err := <-removed; err != nil {
		t.Fatal(err)
	}
}

func TestWaitForIndexLocksPreservesPersistentLock(t *testing.T) {
	t.Parallel()
	lock := filepath.Join(t.TempDir(), "index.lock")
	if err := os.WriteFile(lock, []byte("unfinished index"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := waitForIndexLocks(context.Background(), []string{lock}, 20*time.Millisecond, 5*time.Millisecond)
	var lockErr IndexLockError
	if !errors.As(err, &lockErr) || lockErr.LockPath != lock {
		t.Fatalf("error = %v, want typed lock error for %s", err, lock)
	}
	contents, err := os.ReadFile(lock)
	if err != nil || string(contents) != "unfinished index" {
		t.Fatalf("lock changed: %q, %v", contents, err)
	}
}

func TestWaitForIndexLocksCancellation(t *testing.T) {
	t.Parallel()
	lock := filepath.Join(t.TempDir(), "index.lock")
	if err := os.WriteFile(lock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := waitForIndexLocks(ctx, []string{lock}, time.Minute, time.Minute)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want deadline exceeded", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("cancellation did not interrupt the wait")
	}
}

func TestWaitForIndexLocksRejectsDirectory(t *testing.T) {
	t.Parallel()
	if err := waitForIndexLocks(context.Background(), []string{t.TempDir()}, time.Second, time.Millisecond); err == nil {
		t.Fatal("accepted a directory as an index lock")
	}
}

func TestCheckoutIndexLockPathsIncludesNestedSubmodules(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	leaf := filepath.Join(root, "leaf origin")
	parent := filepath.Join(root, "parent origin")
	repo := filepath.Join(root, "root checkout")
	git := func(path string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "protocol.file.allow=always", "-C", path}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com", "GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	for _, path := range []string{leaf, parent, repo} {
		initRepo(t, path)
		git(path, "commit", "--allow-empty", "-m", "initial")
	}
	git(parent, "submodule", "add", leaf, "nested assets")
	git(parent, "commit", "-am", "nested submodule")
	git(repo, "submodule", "add", parent, "assets folder")
	git(repo, "commit", "-am", "parent submodule")
	git(repo, "submodule", "update", "--init", "--recursive")
	paths, err := checkoutIndexLockPaths(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 3 {
		t.Fatalf("lock paths = %v, want root, parent, and nested", paths)
	}
	for _, path := range []string{repo, filepath.Join(repo, "assets folder"), filepath.Join(repo, "assets folder", "nested assets")} {
		lock, err := IndexLockPath(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(paths, lock) {
			t.Fatalf("missing checkout lock %s in %v", lock, paths)
		}
	}
}
