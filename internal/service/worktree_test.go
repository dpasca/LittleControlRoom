package service

import (
	"context"
	"errors"
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
	expansion := svc.expandDiscoveredWorktreePaths(
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
				{Path: root, Branch: "master", IsMain: true},
				{Path: worktreePath, Branch: "feature"},
			}, nil
		},
	)

	if !slices.Contains(expansion.paths, worktreePath) {
		t.Fatalf("expanded paths = %#v, want live worktree %q", expansion.paths, worktreePath)
	}
	if _, ok := expansion.liveByRoot[root][worktreePath]; !ok {
		t.Fatalf("live worktrees for root = %#v, want %q", expansion.liveByRoot[root], worktreePath)
	}
	metadata, ok := expansion.reconciled[worktreePath]
	if !ok {
		t.Fatalf("reconciled worktrees = %#v, want %q", expansion.reconciled, worktreePath)
	}
	if metadata.rootPath != root || metadata.kind != model.WorktreeKindLinked || metadata.parentBranch != "master" {
		t.Fatalf("reconciled worktree metadata = %#v, want root=%q kind=%q parent=master", metadata, root, model.WorktreeKindLinked)
	}
}

func TestExpandDiscoveredWorktreePathsReportsListFailureOncePerRoot(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir repo metadata: %v", err)
	}

	wantErr := errors.New("porcelain unavailable")
	svc := &Service{}
	expansion := svc.expandDiscoveredWorktreePaths(
		ctx,
		[]string{root},
		map[string]model.ProjectSummary{
			root: {Path: root, WorktreeKind: model.WorktreeKindMain},
		},
		scanner.NewPathScope(nil, nil),
		func(context.Context, string) (scanner.GitWorktreeInfo, error) {
			return scanner.GitWorktreeInfo{RootPath: root, Kind: scanner.GitWorktreeKindMain}, nil
		},
		func(context.Context, string) ([]scanner.GitWorktree, error) {
			return nil, wantErr
		},
	)

	failures := expansion.failures()
	if len(failures) != 1 {
		t.Fatalf("expansion failures = %#v, want one failure for the root", failures)
	}
	if failures[0].rootPath != root || failures[0].seedPath != root || !errors.Is(failures[0].err, wantErr) {
		t.Fatalf("expansion failure = %#v, want root=%q seed=%q error=%v", failures[0], root, root, wantErr)
	}
}
