package worktreeprep

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadConfigAndResolveProfile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writePrepConfig(t, root, `
default_profile = "assets"

[profiles.assets]
description = "Asset checkout"
submodules = [
  { path = "Apps/Demo/Assets", mode = "linked-worktree" },
]
`)

	cfg, configPath, found, err := Load(root)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !found {
		t.Fatal("Load() found = false, want true")
	}
	if !strings.HasSuffix(configPath, ConfigRelPath) {
		t.Fatalf("config path = %q, want suffix %q", configPath, ConfigRelPath)
	}
	name, profile, ok, err := cfg.ResolveProfile("")
	if err != nil {
		t.Fatalf("ResolveProfile() error = %v", err)
	}
	if !ok || name != "assets" {
		t.Fatalf("ResolveProfile() = (%q, ok %v), want assets/true", name, ok)
	}
	if len(profile.Submodules) != 1 || profile.Submodules[0].Path != "Apps/Demo/Assets" || profile.Submodules[0].Mode != "worktree" {
		t.Fatalf("profile submodules = %#v", profile.Submodules)
	}
}

func TestPrepareCheckoutSubmoduleProfile(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	mainPath := filepath.Join(root, "main")
	originPath := filepath.Join(root, "asset-origin")
	initRepoWithSubmodule(t, mainPath, originPath, "Assets")
	writePrepConfig(t, mainPath, `
default_profile = "assets"

[profiles.assets]
submodules = [
  { path = "Assets", mode = "checkout" },
]
`)
	worktreePath := filepath.Join(root, "main--task")
	runGit(t, mainPath, "worktree", "add", "-b", "task", worktreePath, "HEAD")

	result, err := Prepare(ctx, mainPath, worktreePath, "")
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if result.Profile != "assets" || len(result.Prepared) != 1 {
		t.Fatalf("Prepare() result = %#v, want one prepared assets profile", result)
	}
	if got := gitOutputTest(t, filepath.Join(worktreePath, "Assets"), "rev-parse", "--show-toplevel"); !samePath(t, got, filepath.Join(worktreePath, "Assets")) {
		t.Fatalf("submodule top-level = %q", got)
	}
}

func TestPrepareBuiltInAutoSubmodulesWithoutConfigUsesNestedWorktree(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	mainPath := filepath.Join(root, "main")
	originPath := filepath.Join(root, "asset-origin")
	initRepoWithSubmodule(t, mainPath, originPath, "Assets")
	worktreePath := filepath.Join(root, "main--task")
	runGit(t, mainPath, "worktree", "add", "-b", "task", worktreePath, "HEAD")

	result, err := Prepare(ctx, mainPath, worktreePath, "")
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if result.Profile != AutoSubmodulesProfile || len(result.Prepared) != 1 {
		t.Fatalf("Prepare() result = %#v, want auto submodules with one prepared path", result)
	}
	if result.Prepared[0].Path != "Assets" || result.Prepared[0].Mode != "worktree" {
		t.Fatalf("prepared submodule = %#v, want Assets worktree", result.Prepared[0])
	}
	if got := gitOutputTest(t, filepath.Join(worktreePath, "Assets"), "rev-parse", "--show-toplevel"); !samePath(t, got, filepath.Join(worktreePath, "Assets")) {
		t.Fatalf("submodule top-level = %q", got)
	}
	submoduleGitDir := gitOutputTest(t, filepath.Join(worktreePath, "Assets"), "rev-parse", "--absolute-git-dir")
	if !strings.Contains(filepath.ToSlash(submoduleGitDir), "/.git/modules/Assets/worktrees/") {
		t.Fatalf("submodule git dir = %q, want nested worktree git dir", submoduleGitDir)
	}
	parentStatus := gitOutputTest(t, worktreePath, "status", "--porcelain=v2")
	if strings.TrimSpace(parentStatus) != "" {
		t.Fatalf("parent worktree status after prep = %q, want clean", parentStatus)
	}
}

func TestSyncSubmodulesUpdatesNestedWorktreeWithoutRewritingRootMetadata(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	mainPath := filepath.Join(root, "main")
	originPath := filepath.Join(root, "asset-origin")
	initRepoWithSubmodule(t, mainPath, originPath, "Assets")
	worktreePath := filepath.Join(root, "main--task")
	runGit(t, mainPath, "worktree", "add", "-b", "task", worktreePath, "HEAD")

	if _, err := Prepare(ctx, mainPath, worktreePath, ""); err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	rootSubmodulePath := filepath.Join(mainPath, "Assets")
	worktreeSubmodulePath := filepath.Join(worktreePath, "Assets")
	rootCoreWorktree := gitOutputTest(t, rootSubmodulePath, "config", "--local", "--get", "core.worktree")

	newAssetCommit := commitFile(t, originPath, "asset.txt", "updated asset\n", "update asset")
	runGit(t, rootSubmodulePath, "fetch", "origin")
	runGit(t, rootSubmodulePath, "checkout", "--detach", newAssetCommit)
	runGit(t, mainPath, "add", "Assets")
	runGit(t, mainPath, "commit", "-m", "advance asset pointer")
	runGit(t, worktreePath, "merge", "master")

	if got := gitOutputTest(t, worktreeSubmodulePath, "rev-parse", "HEAD"); got == newAssetCommit {
		t.Fatalf("nested submodule worktree already moved to %s before sync", newAssetCommit)
	}
	if err := SyncSubmodules(ctx, mainPath, worktreePath); err != nil {
		t.Fatalf("SyncSubmodules() error = %v", err)
	}
	if got := gitOutputTest(t, worktreeSubmodulePath, "rev-parse", "HEAD"); got != newAssetCommit {
		t.Fatalf("nested submodule HEAD = %q, want %q", got, newAssetCommit)
	}
	if got := gitOutputTest(t, rootSubmodulePath, "config", "--local", "--get", "core.worktree"); got != rootCoreWorktree {
		t.Fatalf("root submodule core.worktree = %q, want unchanged %q", got, rootCoreWorktree)
	}
	for _, repoPath := range []string{mainPath, worktreePath} {
		if got := gitOutputTest(t, repoPath, "status", "--porcelain=v2"); strings.TrimSpace(got) != "" {
			t.Fatalf("repo %s status after submodule sync = %q, want clean", repoPath, got)
		}
	}
}

func TestPrepareBuiltInAutoSubmodulesFallsBackWhenRootSubmoduleCold(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	mainPath := filepath.Join(root, "main")
	originPath := filepath.Join(root, "asset-origin")
	initRepoWithSubmodule(t, mainPath, originPath, "Assets")
	runGit(t, mainPath, "submodule", "deinit", "-f", "Assets")
	worktreePath := filepath.Join(root, "main--task")
	runGit(t, mainPath, "worktree", "add", "-b", "task", worktreePath, "HEAD")

	result, err := Prepare(ctx, mainPath, worktreePath, "")
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if result.Profile != AutoSubmodulesProfile || len(result.Prepared) != 1 {
		t.Fatalf("Prepare() result = %#v, want auto submodules with one prepared path", result)
	}
	if result.Prepared[0].Path != "Assets" || result.Prepared[0].Mode != "checkout" {
		t.Fatalf("prepared submodule = %#v, want Assets checkout fallback", result.Prepared[0])
	}
	if got := gitOutputTest(t, filepath.Join(worktreePath, "Assets"), "rev-parse", "--show-toplevel"); !samePath(t, got, filepath.Join(worktreePath, "Assets")) {
		t.Fatalf("submodule top-level = %q", got)
	}
	submoduleGitDir := gitOutputTest(t, filepath.Join(worktreePath, "Assets"), "rev-parse", "--absolute-git-dir")
	if strings.Contains(filepath.ToSlash(submoduleGitDir), "/.git/modules/Assets/worktrees/") {
		t.Fatalf("submodule git dir = %q, want checkout fallback git dir", submoduleGitDir)
	}
}

func TestPrepareBuiltInAutoSubmodulesFetchesMissingRootCommit(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	mainPath := filepath.Join(root, "main")
	originPath := filepath.Join(root, "asset-origin")
	initRepoWithSubmodule(t, mainPath, originPath, "Assets")
	newAssetCommit := commitFile(t, originPath, "asset.txt", "new asset content\n", "update asset")
	runGit(t, mainPath, "update-index", "--cacheinfo", "160000,"+newAssetCommit+",Assets")
	runGit(t, mainPath, "commit", "-m", "point asset submodule at remote commit")
	if gitCommitExistsTest(filepath.Join(mainPath, "Assets"), newAssetCommit) {
		t.Fatalf("root submodule unexpectedly already has commit %s", newAssetCommit)
	}
	worktreePath := filepath.Join(root, "main--task")
	runGit(t, mainPath, "worktree", "add", "-b", "task", worktreePath, "HEAD")

	result, err := Prepare(ctx, mainPath, worktreePath, "")
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if result.Profile != AutoSubmodulesProfile || len(result.Prepared) != 1 {
		t.Fatalf("Prepare() result = %#v, want auto submodules with one prepared path", result)
	}
	if result.Prepared[0].Path != "Assets" || result.Prepared[0].Mode != "worktree" || result.Prepared[0].Commit != newAssetCommit {
		t.Fatalf("prepared submodule = %#v, want fetched Assets worktree at %s", result.Prepared[0], newAssetCommit)
	}
	if !gitCommitExistsTest(filepath.Join(mainPath, "Assets"), newAssetCommit) {
		t.Fatalf("root submodule still does not have fetched commit %s", newAssetCommit)
	}
	if got := gitOutputTest(t, filepath.Join(worktreePath, "Assets"), "rev-parse", "HEAD"); got != newAssetCommit {
		t.Fatalf("prepared submodule HEAD = %q, want %q", got, newAssetCommit)
	}
}

func TestPrepareBuiltInAutoSubmodulesBlocksRootSubmoduleIndexLock(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	mainPath := filepath.Join(root, "main")
	originPath := filepath.Join(root, "asset-origin")
	initRepoWithSubmodule(t, mainPath, originPath, "Assets")
	worktreePath := filepath.Join(root, "main--task")
	runGit(t, mainPath, "worktree", "add", "-b", "task", worktreePath, "HEAD")
	lockPath := gitPathTest(t, filepath.Join(mainPath, "Assets"), "index.lock")
	if err := os.WriteFile(lockPath, []byte("locked\n"), 0o644); err != nil {
		t.Fatalf("write submodule index.lock: %v", err)
	}

	_, err := Prepare(ctx, mainPath, worktreePath, "")
	if err == nil {
		t.Fatal("Prepare() error = nil, want index.lock error")
	}
	if !strings.Contains(err.Error(), lockPath) || !strings.Contains(err.Error(), "preflight submodule worktree Assets") {
		t.Fatalf("Prepare() error = %q, want root submodule lock guidance", err)
	}
}

func TestPrepareBuiltInAutoSubmodulesBlocksCheckoutIndexLock(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	mainPath := filepath.Join(root, "main")
	originPath := filepath.Join(root, "asset-origin")
	initRepoWithSubmodule(t, mainPath, originPath, "Assets")
	runGit(t, mainPath, "submodule", "deinit", "-f", "Assets")
	worktreePath := filepath.Join(root, "main--task")
	runGit(t, mainPath, "worktree", "add", "-b", "task", worktreePath, "HEAD")
	lockPath := gitPathTest(t, worktreePath, "index.lock")
	if err := os.WriteFile(lockPath, []byte("locked\n"), 0o644); err != nil {
		t.Fatalf("write worktree index.lock: %v", err)
	}

	_, err := Prepare(ctx, mainPath, worktreePath, "")
	if err == nil {
		t.Fatal("Prepare() error = nil, want index.lock error")
	}
	if !strings.Contains(err.Error(), lockPath) || !strings.Contains(err.Error(), "remove the stale lock") {
		t.Fatalf("Prepare() error = %q, want checkout lock guidance", err)
	}
}

func TestPrepareBuiltInRecursiveSubmodulesCanBeRequestedWithoutConfig(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	mainPath := filepath.Join(root, "main")
	originPath := filepath.Join(root, "asset-origin")
	initRepoWithSubmodule(t, mainPath, originPath, "Assets")
	worktreePath := filepath.Join(root, "main--task")
	runGit(t, mainPath, "worktree", "add", "-b", "task", worktreePath, "HEAD")

	result, err := Prepare(ctx, mainPath, worktreePath, "submodules")
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if result.Profile != RecursiveSubmodulesProfile || len(result.Prepared) != 1 {
		t.Fatalf("Prepare() result = %#v, want recursive submodules with one prepared path", result)
	}
	if got := gitOutputTest(t, filepath.Join(worktreePath, "Assets"), "rev-parse", "--show-toplevel"); !samePath(t, got, filepath.Join(worktreePath, "Assets")) {
		t.Fatalf("submodule top-level = %q", got)
	}
}

func TestPrepareBuiltInRecursiveSubmodulesFromDefaultProfile(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	mainPath := filepath.Join(root, "main")
	originPath := filepath.Join(root, "asset-origin")
	initRepoWithSubmodule(t, mainPath, originPath, "Assets")
	writePrepConfig(t, mainPath, `default_profile = "recursive-submodules"`)
	worktreePath := filepath.Join(root, "main--task")
	runGit(t, mainPath, "worktree", "add", "-b", "task", worktreePath, "HEAD")

	result, err := Prepare(ctx, mainPath, worktreePath, "")
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if result.Profile != RecursiveSubmodulesProfile || len(result.Prepared) != 1 || result.Prepared[0].Path != "Assets" {
		t.Fatalf("Prepare() result = %#v, want recursive submodules default profile", result)
	}
}

func TestPrepareCanDisableDefaultRecursiveSubmodules(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	mainPath := filepath.Join(root, "main")
	originPath := filepath.Join(root, "asset-origin")
	initRepoWithSubmodule(t, mainPath, originPath, "Assets")
	writePrepConfig(t, mainPath, `default_profile = "off"`)
	worktreePath := filepath.Join(root, "main--task")
	runGit(t, mainPath, "worktree", "add", "-b", "task", worktreePath, "HEAD")

	result, err := Prepare(ctx, mainPath, worktreePath, "")
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if !result.Skipped || result.SkipReason != "worktree prep disabled" || len(result.Prepared) != 0 {
		t.Fatalf("Prepare() result = %#v, want disabled prep", result)
	}
	if got := gitOutputTest(t, filepath.Join(worktreePath, "Assets"), "rev-parse", "--show-toplevel"); samePath(t, got, filepath.Join(worktreePath, "Assets")) {
		t.Fatal("submodule was initialized despite disabled worktree prep")
	}
}

func TestPrepareWorktreeSubmoduleProfileAndPrune(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	mainPath := filepath.Join(root, "main")
	originPath := filepath.Join(root, "asset-origin")
	initRepoWithSubmodule(t, mainPath, originPath, "Assets")
	writePrepConfig(t, mainPath, `
default_profile = "assets"

[profiles.assets]
submodules = [
  { path = "Assets", mode = "worktree" },
]
`)
	worktreePath := filepath.Join(root, "main--task")
	runGit(t, mainPath, "worktree", "add", "-b", "task", worktreePath, "HEAD")

	result, err := Prepare(ctx, mainPath, worktreePath, "")
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if result.Profile != "assets" || len(result.Prepared) != 1 || result.Prepared[0].Mode != "worktree" {
		t.Fatalf("Prepare() result = %#v, want one worktree-prepared submodule", result)
	}

	submoduleGitDir := gitOutputTest(t, filepath.Join(worktreePath, "Assets"), "rev-parse", "--absolute-git-dir")
	if !strings.Contains(filepath.ToSlash(submoduleGitDir), "/.git/modules/Assets/worktrees/") {
		t.Fatalf("submodule git dir = %q, want nested worktree git dir", submoduleGitDir)
	}
	parentStatus := gitOutputTest(t, worktreePath, "status", "--porcelain=v2")
	if strings.TrimSpace(parentStatus) != "" {
		t.Fatalf("parent worktree status after prep = %q, want clean", parentStatus)
	}
	if branch := gitOutputTest(t, filepath.Join(mainPath, "Assets"), "rev-parse", "--abbrev-ref", "HEAD"); branch != "master" {
		t.Fatalf("root submodule branch after prep = %q, want master", branch)
	}
	if !strings.Contains(gitOutputTest(t, filepath.Join(mainPath, "Assets"), "worktree", "list", "--porcelain"), filepath.Clean(worktreePath)) {
		t.Fatal("asset submodule worktree list does not include prepared worktree")
	}
	if err := PruneSubmoduleWorktrees(ctx, mainPath); err != nil {
		t.Fatalf("prune while child is still present: %v", err)
	}
	if !strings.Contains(gitOutputTest(t, filepath.Join(mainPath, "Assets"), "worktree", "list", "--porcelain"), filepath.Clean(worktreePath)) {
		t.Fatal("prune must preserve a still-present nested worktree registration")
	}

	runGit(t, mainPath, "worktree", "remove", "--force", worktreePath)
	if err := PruneSubmoduleWorktrees(ctx, mainPath); err != nil {
		t.Fatalf("PruneSubmoduleWorktrees() error = %v", err)
	}
	if strings.Contains(gitOutputTest(t, filepath.Join(mainPath, "Assets"), "worktree", "list", "--porcelain"), filepath.Clean(worktreePath)) {
		t.Fatal("asset submodule worktree list still includes removed parent worktree after prune")
	}
}

func TestRepairRootSubmoduleWorktreesRestoresCanonicalCheckout(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	mainPath := filepath.Join(root, "main")
	originPath := filepath.Join(root, "asset-origin")
	initRepoWithSubmodule(t, mainPath, originPath, "Assets")
	worktreePath := filepath.Join(root, "main--removed-task")
	runGit(t, mainPath, "worktree", "add", "-b", "removed-task", worktreePath, "HEAD")
	if _, err := Prepare(ctx, mainPath, worktreePath, ""); err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}

	rootSubmodulePath := filepath.Join(mainPath, "Assets")
	submoduleGitDir := gitOutputTest(t, rootSubmodulePath, "rev-parse", "--absolute-git-dir")
	staleSubmodulePath := filepath.Join(worktreePath, "Assets")
	staleCoreWorktree, err := filepath.Rel(submoduleGitDir, staleSubmodulePath)
	if err != nil {
		t.Fatalf("resolve stale core.worktree: %v", err)
	}
	runGit(t, rootSubmodulePath, "config", "--local", "core.worktree", staleCoreWorktree)
	runGit(t, mainPath, "worktree", "remove", "--force", worktreePath)

	statusCmd := exec.Command("git", "-C", mainPath, "status", "--porcelain=v2")
	if out, err := statusCmd.CombinedOutput(); err == nil {
		t.Fatalf("git status unexpectedly succeeded with stale submodule metadata: %s", strings.TrimSpace(string(out)))
	}

	repaired, err := RepairRootSubmoduleWorktrees(ctx, mainPath)
	if err != nil {
		t.Fatalf("RepairRootSubmoduleWorktrees() error = %v", err)
	}
	if len(repaired) != 1 || repaired[0] != "Assets" {
		t.Fatalf("repaired paths = %#v, want [Assets]", repaired)
	}
	if got := gitOutputTest(t, mainPath, "status", "--porcelain=v2"); strings.TrimSpace(got) != "" {
		t.Fatalf("parent status after repair = %q, want clean", got)
	}
	if got := gitOutputTest(t, rootSubmodulePath, "rev-parse", "--show-toplevel"); !samePath(t, got, rootSubmodulePath) {
		t.Fatalf("root submodule top-level after repair = %q, want %q", got, rootSubmodulePath)
	}

	repaired, err = RepairRootSubmoduleWorktrees(ctx, mainPath)
	if err != nil {
		t.Fatalf("second RepairRootSubmoduleWorktrees() error = %v", err)
	}
	if len(repaired) != 0 {
		t.Fatalf("second repaired paths = %#v, want none", repaired)
	}
}

func TestRepairRootSubmoduleWorktreesRestoresNestedCanonicalCheckout(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	mainPath := filepath.Join(root, "main")
	appOriginPath := filepath.Join(root, "app-origin")
	assetOriginPath := filepath.Join(root, "asset-origin")
	initRepoWithSubmodule(t, appOriginPath, assetOriginPath, "Assets")
	initGitRepo(t, mainPath)
	runGit(t, mainPath, "-c", "protocol.file.allow=always", "submodule", "add", appOriginPath, "Apps/TheRun2")
	runGit(t, mainPath, "commit", "-m", "add app submodule")
	runGit(t, mainPath, "-c", "protocol.file.allow=always", "submodule", "update", "--init", "--recursive")

	nestedSubmodulePath := filepath.Join(mainPath, "Apps", "TheRun2", "Assets")
	nestedSubmoduleGitDir := gitOutputTest(t, nestedSubmodulePath, "rev-parse", "--absolute-git-dir")
	staleSubmodulePath := filepath.Join(root, "main--removed-task", "Apps", "TheRun2", "Assets")
	staleCoreWorktree, err := filepath.Rel(nestedSubmoduleGitDir, staleSubmodulePath)
	if err != nil {
		t.Fatalf("resolve stale nested core.worktree: %v", err)
	}
	runGit(t, nestedSubmodulePath, "config", "--local", "core.worktree", staleCoreWorktree)

	statusCmd := exec.Command("git", "-C", mainPath, "status", "--porcelain=v2")
	if out, err := statusCmd.CombinedOutput(); err == nil {
		t.Fatalf("git status unexpectedly succeeded with stale nested submodule metadata: %s", strings.TrimSpace(string(out)))
	}

	repaired, err := RepairRootSubmoduleWorktrees(ctx, mainPath)
	if err != nil {
		t.Fatalf("RepairRootSubmoduleWorktrees() error = %v", err)
	}
	if len(repaired) != 1 || repaired[0] != "Apps/TheRun2/Assets" {
		t.Fatalf("repaired paths = %#v, want [Apps/TheRun2/Assets]", repaired)
	}
	if got := gitOutputTest(t, mainPath, "status", "--porcelain=v2"); strings.TrimSpace(got) != "" {
		t.Fatalf("parent status after nested repair = %q, want clean", got)
	}
	if got := gitOutputTest(t, nestedSubmodulePath, "rev-parse", "--show-toplevel"); !samePath(t, got, nestedSubmodulePath) {
		t.Fatalf("nested root submodule top-level after repair = %q, want %q", got, nestedSubmodulePath)
	}
}

func TestRepairRootSubmoduleWorktreeConfigMigration(t *testing.T) {
	for _, override := range []string{"missing", "canonical", "stale"} {
		t.Run(override, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			mainPath := filepath.Join(root, "main")
			initRepoWithSubmodule(t, mainPath, filepath.Join(root, "origin"), "Assets")
			worktreePath := filepath.Join(root, "task")
			runGit(t, mainPath, "worktree", "add", "-b", "task", worktreePath, "HEAD")
			if _, err := Prepare(t.Context(), mainPath, worktreePath, ""); err != nil {
				t.Fatal(err)
			}
			canonical := filepath.Join(mainPath, "Assets")
			child := filepath.Join(worktreePath, "Assets")
			gitDir := gitOutputTest(t, canonical, "rev-parse", "--absolute-git-dir")
			head := gitOutputTest(t, child, "rev-parse", "HEAD")
			shared := gitOutputTest(t, canonical, "config", "--local", "--get", "core.worktree")
			sibling := filepath.Join(root, "sibling-assets")
			runGit(t, canonical, "worktree", "add", "--detach", sibling, head)
			siblingGitDir := gitOutputTest(t, sibling, "rev-parse", "--absolute-git-dir")
			runGit(t, canonical, "config", "extensions.worktreeConfig", "true")
			runGit(t, mainPath, "--git-dir="+siblingGitDir, "--work-tree="+sibling, "config", "--worktree", "core.worktree", sibling)
			siblingConfig, err := os.ReadFile(filepath.Join(siblingGitDir, "config.worktree"))
			if err != nil {
				t.Fatal(err)
			}
			if override != "missing" {
				value := canonical
				if override == "stale" {
					value = filepath.Join(root, "removed")
				}
				runGit(t, mainPath, "--git-dir="+gitDir, "--work-tree="+canonical, "config", "--worktree", "core.worktree", value)
			}
			if got := gitOutputTest(t, child, "rev-parse", "--show-toplevel"); samePath(t, got, child) {
				t.Fatal("fixture did not reproduce misdirected linked worktree")
			}
			repaired, err := RepairRootSubmoduleWorktrees(t.Context(), mainPath)
			if err != nil || len(repaired) != 1 {
				t.Fatalf("repair = %v, %v", repaired, err)
			}
			for _, path := range []string{canonical, child, sibling, mainPath, worktreePath} {
				if got := gitOutputTest(t, path, "status", "--porcelain=v2"); got != "" {
					t.Fatalf("%s status = %q", path, got)
				}
				if got := gitOutputTest(t, path, "rev-parse", "--show-toplevel"); !samePath(t, got, path) {
					t.Fatalf("%s top-level = %q", path, got)
				}
			}
			if gitOutputTest(t, child, "rev-parse", "HEAD") != head {
				t.Fatal("repair changed the child commit")
			}
			if got, err := os.ReadFile(filepath.Join(siblingGitDir, "config.worktree")); err != nil || string(got) != string(siblingConfig) {
				t.Fatalf("sibling config changed: %q, %v", got, err)
			}
			if _, found, err := readSubmoduleConfig(t.Context(), gitDir, canonical, "--local", "--get", "core.worktree"); err != nil || found {
				t.Fatalf("shared core.worktree remains: found=%v err=%v", found, err)
			}
			if err := os.WriteFile(filepath.Join(child, "asset.txt"), []byte("local work\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			repaired, err = RepairRootSubmoduleWorktrees(t.Context(), mainPath)
			if err != nil || len(repaired) != 0 {
				t.Fatalf("second repair = %v, %v", repaired, err)
			}
			if gitOutputTest(t, child, "status", "--porcelain") == "" || gitOutputTest(t, canonical, "status", "--porcelain") != "" {
				t.Fatal("repair hid local edits or redirected the canonical checkout")
			}
			// New preparation also repairs incomplete migration before linking a
			// checkout, preserving existing siblings and their local changes.
			runGit(t, canonical, "config", "--local", "core.worktree", shared)
			newPath := filepath.Join(root, "new-task")
			runGit(t, mainPath, "worktree", "add", "-b", "new-task", newPath, "HEAD")
			if _, err := Prepare(t.Context(), mainPath, newPath, ""); err != nil {
				t.Fatal(err)
			}
			if got := gitOutputTest(t, newPath, "status", "--porcelain"); got != "" {
				t.Fatalf("new parent status = %q", got)
			}
			if gitOutputTest(t, child, "status", "--porcelain") == "" {
				t.Fatal("preparation hid sibling changes")
			}
		})
	}
}

func TestSubmoduleHydrationPreservesWorktreeConfig(t *testing.T) {
	for _, mode := range []string{"path", "all"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			parent, origin := filepath.Join(root, "parent"), filepath.Join(root, "origin")
			initRepoWithSubmodule(t, parent, origin, "Assets")
			canonical, child := filepath.Join(parent, "Assets"), filepath.Join(root, "child")
			runGit(t, canonical, "worktree", "add", "--detach", child)
			runGit(t, canonical, "config", "extensions.worktreeConfig", "true")
			if _, err := RepairRootSubmoduleWorktrees(t.Context(), parent); err != nil {
				t.Fatal(err)
			}
			next := commitFile(t, origin, "asset.txt", "updated\n", "advance assets")
			runGit(t, parent, "update-index", "--cacheinfo", "160000", next, "Assets")
			var err error
			if mode == "path" {
				err = gitSubmoduleUpdate(t.Context(), parent, "Assets")
			} else {
				err = gitSubmoduleUpdateAll(t.Context(), parent)
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, path := range []string{canonical, child} {
				if got := gitOutputTest(t, path, "rev-parse", "--show-toplevel"); !samePath(t, got, path) {
					t.Fatalf("%s top-level = %q", path, got)
				}
				if got := gitOutputTest(t, path, "status", "--porcelain"); got != "" {
					t.Fatalf("%s status = %q", path, got)
				}
			}
		})
	}
}

func TestRepairWorktreeConfigKeepsSharedValueWhenCanonicalWriteFails(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	mainPath := filepath.Join(root, "main")
	initRepoWithSubmodule(t, mainPath, filepath.Join(root, "origin"), "Assets")
	canonical := filepath.Join(mainPath, "Assets")
	gitDir := gitOutputTest(t, canonical, "rev-parse", "--absolute-git-dir")
	shared := gitOutputTest(t, canonical, "config", "--local", "--get", "core.worktree")
	runGit(t, canonical, "config", "extensions.worktreeConfig", "true")
	if err := os.WriteFile(filepath.Join(gitDir, "config.worktree.lock"), []byte("another writer"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := RepairRootSubmoduleWorktrees(t.Context(), mainPath); err == nil {
		t.Fatal("repair ignored the canonical config lock")
	}
	if got := gitOutputTest(t, canonical, "config", "--local", "--get", "core.worktree"); got != shared {
		t.Fatalf("shared value changed after failed canonical write: %q", got)
	}
}

func initRepoWithSubmodule(t *testing.T, mainPath, originPath, submoduleName string) {
	t.Helper()
	initGitRepo(t, originPath)
	initGitRepo(t, mainPath)
	runGit(t, mainPath, "-c", "protocol.file.allow=always", "submodule", "add", originPath, submoduleName)
	runGit(t, mainPath, "commit", "-m", "add submodule")
}

func initGitRepo(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	runGit(t, path, "init")
	runGit(t, path, "config", "user.email", "test@example.com")
	runGit(t, path, "config", "user.name", "Little Control Room Test")
	if err := os.WriteFile(filepath.Join(path, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	runGit(t, path, "add", "README.md")
	runGit(t, path, "commit", "-m", "initial")
}

func writePrepConfig(t *testing.T, root, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(ConfigRelPath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir prep config parent: %v", err)
	}
	if err := os.WriteFile(path, []byte(strings.TrimSpace(content)+"\n"), 0o644); err != nil {
		t.Fatalf("write prep config: %v", err)
	}
}

func commitFile(t *testing.T, repoPath, relPath, content, message string) string {
	t.Helper()
	path := filepath.Join(repoPath, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir file parent: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	runGit(t, repoPath, "add", relPath)
	runGit(t, repoPath, "commit", "-m", message)
	return gitOutputTest(t, repoPath, "rev-parse", "HEAD")
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Little Control Room Test",
		"GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Little Control Room Test",
		"GIT_COMMITTER_EMAIL=test@example.com",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git -C %s %v failed: %v\n%s", dir, args, err, string(out))
	}
}

func gitCommitExistsTest(repoPath, commit string) bool {
	cmd := exec.Command("git", "-C", repoPath, "cat-file", "-e", strings.TrimSpace(commit)+"^{commit}")
	return cmd.Run() == nil
}

func gitOutputTest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git -C %s %v failed: %v\n%s", dir, args, err, string(out))
	}
	return strings.TrimSpace(string(out))
}

func gitPathTest(t *testing.T, dir, name string) string {
	t.Helper()
	path := gitOutputTest(t, dir, "rev-parse", "--git-path", name)
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Clean(filepath.Join(dir, path))
}

func samePath(t *testing.T, a, b string) bool {
	t.Helper()
	resolvedA, err := filepath.EvalSymlinks(a)
	if err == nil {
		a = resolvedA
	}
	resolvedB, err := filepath.EvalSymlinks(b)
	if err == nil {
		b = resolvedB
	}
	return filepath.Clean(a) == filepath.Clean(b)
}
