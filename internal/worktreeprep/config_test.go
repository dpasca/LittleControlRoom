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

	runGit(t, mainPath, "worktree", "remove", "--force", worktreePath)
	if err := PruneSubmoduleWorktrees(ctx, mainPath); err != nil {
		t.Fatalf("PruneSubmoduleWorktrees() error = %v", err)
	}
	if strings.Contains(gitOutputTest(t, filepath.Join(mainPath, "Assets"), "worktree", "list", "--porcelain"), filepath.Clean(worktreePath)) {
		t.Fatal("asset submodule worktree list still includes removed parent worktree after prune")
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
