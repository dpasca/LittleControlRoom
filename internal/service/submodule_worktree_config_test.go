package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lcroom/internal/worktreeprep"
)

func TestRootSubmoduleSyncPreservesWorktreeConfig(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	parent := filepath.Join(root, "parent")
	origin := filepath.Join(root, "origin")
	canonical := initGitRepoWithSubmodule(t, parent, origin, "Assets")
	child := filepath.Join(root, "linked-assets")
	runGit(t, canonical, "git", "worktree", "add", "--detach", child, "HEAD")
	childHead := strings.TrimSpace(gitOutput(t, child, "git", "rev-parse", "HEAD"))
	runGit(t, canonical, "git", "config", "extensions.worktreeConfig", "true")
	if _, err := worktreeprep.RepairRootSubmoduleWorktrees(t.Context(), parent); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(origin, "next.txt"), []byte("next asset\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, origin, "git", "add", "next.txt")
	runGit(t, origin, "git", "commit", "-m", "advance assets")
	next := strings.TrimSpace(gitOutput(t, origin, "git", "rev-parse", "HEAD"))
	runGit(t, parent, "git", "update-index", "--cacheinfo", "160000", next, "Assets")
	if err := gitSubmoduleUpdateInitRecursive(t.Context(), parent); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(gitOutput(t, canonical, "git", "rev-parse", "HEAD")); got != next {
		t.Fatalf("canonical HEAD = %s, want %s", got, next)
	}
	if got := strings.TrimSpace(gitOutput(t, child, "git", "rev-parse", "HEAD")); got != childHead {
		t.Fatalf("sibling HEAD moved from %s to %s", childHead, got)
	}
	for _, path := range []string{canonical, child} {
		if got := strings.TrimSpace(gitOutput(t, path, "git", "status", "--porcelain")); got != "" {
			t.Fatalf("%s status after sync = %q", path, got)
		}
		got := strings.TrimSpace(gitOutput(t, path, "git", "rev-parse", "--show-toplevel"))
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil || got != resolved {
			t.Fatalf("%s top-level after sync = %q; resolved=%q, err=%v", path, got, resolved, err)
		}
	}
}
