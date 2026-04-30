package service

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"lcroom/internal/model"
	"lcroom/internal/scanner"
)

func TestKnownPathVariantResolverKeepsCurrentScanPathPreferred(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	actual := filepath.Join(root, "repo")
	if err := os.MkdirAll(actual, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	currentAlias := filepath.Join(root, "current")
	if err := os.Symlink(actual, currentAlias); err != nil {
		t.Fatalf("symlink current: %v", err)
	}
	oldAlias := filepath.Join(root, "old")
	if err := os.Symlink(actual, oldAlias); err != nil {
		t.Fatalf("symlink old: %v", err)
	}

	resolver := newKnownPathVariantResolver([]string{currentAlias})
	resolver.addAll([]string{oldAlias}, false)

	if got := resolver.preferred(actual); got != currentAlias {
		t.Fatalf("preferred path = %q, want current scan path %q", got, currentAlias)
	}
}

func TestKnownPathVariantResolverCanPromoteNewScanPath(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	actual := filepath.Join(root, "repo")
	if err := os.MkdirAll(actual, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	oldAlias := filepath.Join(root, "old")
	if err := os.Symlink(actual, oldAlias); err != nil {
		t.Fatalf("symlink old: %v", err)
	}
	currentAlias := filepath.Join(root, "current")
	if err := os.Symlink(actual, currentAlias); err != nil {
		t.Fatalf("symlink current: %v", err)
	}

	resolver := newKnownPathVariantResolver([]string{oldAlias})
	resolver.add(currentAlias, true)

	if got := resolver.preferred(actual); got != currentAlias {
		t.Fatalf("preferred path = %q, want promoted scan path %q", got, currentAlias)
	}
}

func TestKnownPathVariantResolverIsFreshPerScan(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	firstTarget := filepath.Join(root, "repo-first")
	if err := os.MkdirAll(firstTarget, 0o755); err != nil {
		t.Fatalf("mkdir first repo: %v", err)
	}
	secondTarget := filepath.Join(root, "repo-second")
	if err := os.MkdirAll(secondTarget, 0o755); err != nil {
		t.Fatalf("mkdir second repo: %v", err)
	}
	alias := filepath.Join(root, "current")
	if err := os.Symlink(firstTarget, alias); err != nil {
		t.Fatalf("symlink first target: %v", err)
	}

	firstResolver := newKnownPathVariantResolver([]string{alias})
	if got := firstResolver.preferred(firstTarget); got != alias {
		t.Fatalf("first preferred path = %q, want alias %q", got, alias)
	}

	if err := os.Remove(alias); err != nil {
		t.Fatalf("remove alias: %v", err)
	}
	if err := os.Symlink(secondTarget, alias); err != nil {
		t.Fatalf("symlink second target: %v", err)
	}

	secondResolver := newKnownPathVariantResolver([]string{alias})
	if got := secondResolver.preferred(secondTarget); got != alias {
		t.Fatalf("second preferred path = %q, want refreshed alias %q", got, alias)
	}
}

func TestExpandDiscoveredWorktreePathsAddsLiveWorktreeFromCurrentScan(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	worktreePath := filepath.Join(filepath.Dir(root), "repo-feature")
	if err := os.MkdirAll(worktreePath, 0o755); err != nil {
		t.Fatalf("mkdir worktree: %v", err)
	}

	svc := &Service{}
	paths, liveByRoot := svc.expandDiscoveredWorktreePaths(
		ctx,
		[]string{root},
		map[string]model.ProjectSummary{
			root: {Path: root},
		},
		scanner.NewPathScope(nil, nil),
		func(context.Context, string) (scanner.GitWorktreeInfo, error) {
			return scanner.GitWorktreeInfo{RootPath: root, Kind: scanner.GitWorktreeKindMain}, nil
		},
		func(context.Context, string) ([]scanner.GitWorktree, error) {
			return []scanner.GitWorktree{
				{Path: root, IsMain: true},
				{Path: worktreePath, Branch: "feature"},
			}, nil
		},
	)

	if !slices.Contains(paths, worktreePath) {
		t.Fatalf("expanded paths = %#v, want live worktree %q", paths, worktreePath)
	}
	if _, ok := liveByRoot[root][worktreePath]; !ok {
		t.Fatalf("live worktrees for root = %#v, want %q", liveByRoot[root], worktreePath)
	}
}
