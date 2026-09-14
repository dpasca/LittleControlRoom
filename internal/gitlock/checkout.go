package gitlock

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// WaitForCheckoutIndexLocks waits briefly for concurrent Git work to finish in
// the selected checkouts and their populated submodules. Unlike the repository
// family scan, this does not include sibling submodule worktrees' private indexes.
// It only inspects locks: Git lockfiles have no reliable owner metadata, so an
// old lock or a lock without an open descriptor is not safe to delete here.
// Call from a background action, never the UI update/render path.
func WaitForCheckoutIndexLocks(ctx context.Context, repoPaths ...string) error {
	var lockPaths []string
	seen := make(map[string]bool)
	for _, repoPath := range repoPaths {
		paths, err := checkoutIndexLockPaths(ctx, repoPath)
		if err != nil {
			return err
		}
		for _, path := range paths {
			if !seen[path] {
				seen[path] = true
				lockPaths = append(lockPaths, path)
			}
		}
	}
	return waitForIndexLocks(ctx, lockPaths, 3*time.Second, 100*time.Millisecond)
}

func checkoutIndexLockPaths(ctx context.Context, repoPath string) ([]string, error) {
	rootLock, err := IndexLockPath(ctx, repoPath)
	if err != nil {
		return nil, err
	}
	// Let Git enumerate actual populated checkouts, including nested submodules
	// and linked worktrees. NUL delimiters preserve spaces and newlines in paths.
	// Unpopulated submodules have no checkout index to preflight; Git still
	// enforces its own locks when the later update initializes them.
	cmd := exec.CommandContext(ctx, "git", "-C", repoPath, "submodule", "foreach", "--quiet", "--recursive",
		`printf '%s\0' "$toplevel/$sm_path"`)
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("enumerate submodule checkouts in %s: %w", repoPath, err)
	}
	paths := []string{rootLock}
	for _, path := range strings.Split(string(out), "\x00") {
		if path == "" {
			continue
		}
		lockPath, err := IndexLockPath(ctx, path)
		if err != nil {
			return nil, err
		}
		paths = append(paths, lockPath)
	}
	return paths, nil
}

func waitForIndexLocks(ctx context.Context, lockPaths []string, timeout, interval time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		var blocked string
		for _, path := range lockPaths {
			info, err := os.Stat(path)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return fmt.Errorf("stat git index lock %s: %w", path, err)
			}
			if info.IsDir() {
				return fmt.Errorf("git index lock path is a directory: %s", filepath.Clean(path))
			}
			blocked = path
			break
		}
		if blocked == "" {
			return nil
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return fmt.Errorf("git index is still locked after waiting %s; no lock was removed: %w", timeout, IndexLockError{LockPath: blocked})
		}
		timer := time.NewTimer(min(interval, remaining))
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
