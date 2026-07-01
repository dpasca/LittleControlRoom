package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lcroom/internal/config"
	"lcroom/internal/events"
	"lcroom/internal/scanner"
	"lcroom/internal/store"
	"lcroom/internal/worktreeprep"
)

func TestMergeWorktreeBackAutoResolvesGitlinkFastForwardAfterFetch(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	projectPath := filepath.Join(root, "repo")
	submoduleRootPath := filepath.Join(root, "assets")
	submodulePath := initGitRepoWithPushableSubmodule(t, projectPath, submoduleRootPath, "assets_src")
	base := strings.TrimSpace(gitOutput(t, submodulePath, "git", "rev-parse", "HEAD"))

	svc, st := newGitlinkConflictServiceForTest(t, ctx, root, projectPath)
	result := createGitlinkConflictWorktreeForTest(t, ctx, svc, st, projectPath, "Resolve a fetched fast-forward gitlink", "feat/gitlink-ff", "feat-gitlink-ff")
	worktreeSubmodulePath := filepath.Join(result.WorktreePath, "assets_src")

	runGit(t, submodulePath, "git", "checkout", "master")
	ours := commitAndPushSubmoduleFileForTest(t, submodulePath, "ours.txt", "root submodule side\n", "root submodule bump", "master")
	if ours == base {
		t.Fatalf("expected root submodule commit to advance from base %q", base)
	}
	runGit(t, projectPath, "git", "add", "assets_src")
	runGit(t, projectPath, "git", "commit", "-m", "root bump submodule")

	runGit(t, worktreeSubmodulePath, "git", "fetch", "origin")
	runGit(t, worktreeSubmodulePath, "git", "checkout", "-B", "master", "origin/master")
	theirs := commitAndPushSubmoduleFileForTest(t, worktreeSubmodulePath, "theirs.txt", "worktree submodule side\n", "worktree submodule bump", "master")
	runGit(t, result.WorktreePath, "git", "add", "assets_src")
	runGit(t, result.WorktreePath, "git", "commit", "-m", "worktree bump submodule")

	mergeResult, err := svc.MergeWorktreeBack(ctx, result.WorktreePath)
	if err != nil {
		t.Fatalf("MergeWorktreeBack() error = %v", err)
	}
	if mergeResult.RootProjectPath != projectPath {
		t.Fatalf("merge root path = %q, want %q", mergeResult.RootProjectPath, projectPath)
	}

	resolvedGitlink := strings.TrimSpace(gitOutput(t, projectPath, "git", "rev-parse", "HEAD:assets_src"))
	if resolvedGitlink != theirs {
		t.Fatalf("resolved root gitlink = %q, want fetched fast-forward target %q", resolvedGitlink, theirs)
	}
	rootSubmoduleHead := strings.TrimSpace(gitOutput(t, submodulePath, "git", "rev-parse", "HEAD"))
	if rootSubmoduleHead != theirs {
		t.Fatalf("root submodule head after merge-back = %q, want %q", rootSubmoduleHead, theirs)
	}
	assertGitlinkConflictRootClean(t, ctx, projectPath)
}

func TestResolveGitlinkConflictKeepsOursWhenTheirsAncestor(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	projectPath := filepath.Join(root, "repo")
	submoduleRootPath := filepath.Join(root, "assets")
	submodulePath := initGitRepoWithPushableSubmodule(t, projectPath, submoduleRootPath, "assets_src")
	base := strings.TrimSpace(gitOutput(t, submodulePath, "git", "rev-parse", "HEAD"))

	runGit(t, submodulePath, "git", "checkout", "master")
	ours := commitAndPushSubmoduleFileForTest(t, submodulePath, "ours.txt", "root submodule side\n", "root submodule bump", "master")

	resolution, err := resolveGitlinkConflict(ctx, projectPath, "master", gitlinkConflict{
		Path:   "assets_src",
		Base:   base,
		Ours:   ours,
		Theirs: base,
	})
	if err != nil {
		t.Fatalf("resolveGitlinkConflict() error = %v", err)
	}
	if resolution.Mode != "ours-fast-forward" || resolution.Resolved != ours {
		t.Fatalf("resolution = %#v, want ours-fast-forward to %s", resolution, ours)
	}

	indexGitlink := strings.TrimSpace(gitOutput(t, projectPath, "git", "rev-parse", ":assets_src"))
	if indexGitlink != ours {
		t.Fatalf("staged parent gitlink = %q, want ours %q", indexGitlink, ours)
	}
}

func TestResolveGitlinkConflictReportsMissingCommitAfterFetch(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	projectPath := filepath.Join(root, "repo")
	submoduleRootPath := filepath.Join(root, "assets")
	submodulePath := initGitRepoWithPushableSubmodule(t, projectPath, submoduleRootPath, "assets_src")
	base := strings.TrimSpace(gitOutput(t, submodulePath, "git", "rev-parse", "HEAD"))
	missing := strings.Repeat("0", 40)

	_, err := resolveGitlinkConflict(ctx, projectPath, "master", gitlinkConflict{
		Path:   "assets_src",
		Base:   base,
		Ours:   base,
		Theirs: missing,
	})
	if err == nil {
		t.Fatalf("resolveGitlinkConflict() expected missing commit error")
	}
	if !strings.Contains(err.Error(), "submodule assets_src does not have required commit "+missing+" after fetch") {
		t.Fatalf("resolveGitlinkConflict() error = %q, want missing commit guidance", err)
	}
}

func TestMergeWorktreeBackAutoResolvesDivergentGitlinkWithSubmoduleMerge(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	projectPath := filepath.Join(root, "repo")
	submoduleRootPath := filepath.Join(root, "assets")
	submodulePath := initGitRepoWithPushableSubmodule(t, projectPath, submoduleRootPath, "assets_src")
	base := strings.TrimSpace(gitOutput(t, submodulePath, "git", "rev-parse", "HEAD"))

	svc, st := newGitlinkConflictServiceForTest(t, ctx, root, projectPath)
	result := createGitlinkConflictWorktreeForTest(t, ctx, svc, st, projectPath, "Resolve a divergent gitlink with a submodule merge", "feat/gitlink-divergent", "feat-gitlink-divergent")
	worktreeSubmodulePath := filepath.Join(result.WorktreePath, "assets_src")

	runGit(t, submodulePath, "git", "checkout", "master")
	ours := commitAndPushSubmoduleFileForTest(t, submodulePath, "ours.txt", "root submodule side\n", "root submodule side", "master")
	runGit(t, projectPath, "git", "add", "assets_src")
	runGit(t, projectPath, "git", "commit", "-m", "root bump submodule")

	runGit(t, worktreeSubmodulePath, "git", "checkout", "-B", "theirs", base)
	theirs := commitAndPushSubmoduleFileForTest(t, worktreeSubmodulePath, "theirs.txt", "worktree submodule side\n", "worktree submodule side", "theirs")
	runGit(t, result.WorktreePath, "git", "add", "assets_src")
	runGit(t, result.WorktreePath, "git", "commit", "-m", "worktree bump submodule")

	mergeResult, err := svc.MergeWorktreeBack(ctx, result.WorktreePath)
	if err != nil {
		t.Fatalf("MergeWorktreeBack() error = %v", err)
	}
	if mergeResult.RootProjectPath != projectPath {
		t.Fatalf("merge root path = %q, want %q", mergeResult.RootProjectPath, projectPath)
	}

	resolvedGitlink := strings.TrimSpace(gitOutput(t, projectPath, "git", "rev-parse", "HEAD:assets_src"))
	if resolvedGitlink == ours || resolvedGitlink == theirs {
		t.Fatalf("resolved root gitlink = %q, want a new merge commit distinct from ours %q and theirs %q", resolvedGitlink, ours, theirs)
	}
	rootSubmoduleHead := strings.TrimSpace(gitOutput(t, submodulePath, "git", "rev-parse", "HEAD"))
	if rootSubmoduleHead != resolvedGitlink {
		t.Fatalf("root submodule head after merge-back = %q, want resolved gitlink %q", rootSubmoduleHead, resolvedGitlink)
	}

	parentLine := strings.Fields(gitOutput(t, submodulePath, "git", "rev-list", "--parents", "-n1", "HEAD"))
	if len(parentLine) != 3 || parentLine[0] != resolvedGitlink || !containsString(parentLine[1:], ours) || !containsString(parentLine[1:], theirs) {
		t.Fatalf("submodule merge parents = %#v, want merge commit %s with parents %s and %s", parentLine, resolvedGitlink, ours, theirs)
	}

	originPath := filepath.Join(submoduleRootPath, "origin.git")
	originBranches := gitOutput(t, originPath, "git", "branch", "--contains", resolvedGitlink, "--format=%(refname:short)")
	if !strings.Contains(originBranches, "lcroom/master/assets_src-merge-") {
		t.Fatalf("origin branches containing submodule merge %s = %q, want pushed lcroom merge branch", resolvedGitlink, originBranches)
	}
	assertGitlinkConflictRootClean(t, ctx, projectPath)
}

func TestMergeWorktreeBackSurfacesSubmoduleContentConflictWorktree(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	projectPath := filepath.Join(root, "repo")
	submoduleRootPath := filepath.Join(root, "assets")
	submodulePath := initGitRepoWithPushableSubmodule(t, projectPath, submoduleRootPath, "assets_src")
	base := strings.TrimSpace(gitOutput(t, submodulePath, "git", "rev-parse", "HEAD"))

	svc, st := newGitlinkConflictServiceForTest(t, ctx, root, projectPath)
	result := createGitlinkConflictWorktreeForTest(t, ctx, svc, st, projectPath, "Surface a submodule content conflict worktree", "feat/gitlink-content-conflict", "feat-gitlink-content-conflict")
	worktreeSubmodulePath := filepath.Join(result.WorktreePath, "assets_src")

	runGit(t, submodulePath, "git", "checkout", "master")
	ours := commitAndPushSubmoduleFileForTest(t, submodulePath, "README.md", "root submodule side\n", "root conflicting submodule change", "master")
	runGit(t, projectPath, "git", "add", "assets_src")
	runGit(t, projectPath, "git", "commit", "-m", "root bump submodule")

	runGit(t, worktreeSubmodulePath, "git", "checkout", "-B", "theirs", base)
	theirs := commitAndPushSubmoduleFileForTest(t, worktreeSubmodulePath, "README.md", "worktree submodule side\n", "worktree conflicting submodule change", "theirs")
	runGit(t, result.WorktreePath, "git", "add", "assets_src")
	runGit(t, result.WorktreePath, "git", "commit", "-m", "worktree bump submodule")

	_, err := svc.MergeWorktreeBack(ctx, result.WorktreePath)
	if err == nil {
		t.Fatalf("MergeWorktreeBack() expected submodule content conflict")
	}
	var conflictErr submoduleContentMergeConflictError
	if !errors.As(err, &conflictErr) {
		t.Fatalf("MergeWorktreeBack() error = %v, want submoduleContentMergeConflictError", err)
	}
	if conflictErr.Path != "assets_src" || conflictErr.Ours != ours || conflictErr.Theirs != theirs {
		t.Fatalf("content conflict error = %#v, want assets_src ours/theirs details", conflictErr)
	}
	if !strings.Contains(conflictErr.Branch, "lcroom/master/assets_src-merge-") {
		t.Fatalf("content conflict branch = %q, want lcroom merge branch", conflictErr.Branch)
	}
	if _, statErr := os.Stat(conflictErr.WorktreePath); statErr != nil {
		t.Fatalf("content conflict merge worktree %q should remain for manual resolution: %v", conflictErr.WorktreePath, statErr)
	}
	t.Cleanup(func() {
		_ = removeSubmoduleMergeWorktree(context.Background(), submodulePath, conflictErr.WorktreePath)
		_ = os.RemoveAll(filepath.Dir(conflictErr.WorktreePath))
	})
	if !strings.Contains(err.Error(), "submodule assets_src content merge needs manual resolution") {
		t.Fatalf("MergeWorktreeBack() error = %q, want manual submodule resolution guidance", err)
	}

	target, ok, targetErr := svc.GitlinkConflictResolveTarget(ctx, projectPath)
	if targetErr != nil {
		t.Fatalf("GitlinkConflictResolveTarget() error = %v", targetErr)
	}
	if !ok {
		t.Fatalf("GitlinkConflictResolveTarget() ok = false, want retained submodule merge worktree")
	}
	if !sameTestPath(t, target.WorktreePath, conflictErr.WorktreePath) || target.Branch != conflictErr.Branch || target.SubmodulePath != "assets_src" {
		t.Fatalf("GitlinkConflictResolveTarget() = %#v, want retained conflict worktree %#v", target, conflictErr)
	}
	if target.ParentRepoPath != filepath.Clean(projectPath) || target.SubmoduleRepoPath != filepath.Clean(submodulePath) {
		t.Fatalf("GitlinkConflictResolveTarget() repo paths = %#v, want parent %q submodule %q", target, projectPath, submodulePath)
	}
	if target.Ours != ours || target.Theirs != theirs {
		t.Fatalf("GitlinkConflictResolveTarget() ours/theirs = %s/%s, want %s/%s", target.Ours, target.Theirs, ours, theirs)
	}

	rootStatus, statusErr := scanner.ReadGitRepoStatus(ctx, projectPath)
	if statusErr != nil {
		t.Fatalf("read root git status after submodule content conflict: %v", statusErr)
	}
	if conflicted := conflictedPaths(rootStatus); !containsString(conflicted, "assets_src") {
		t.Fatalf("root conflicted paths = %#v, want assets_src", conflicted)
	}
}

func newGitlinkConflictServiceForTest(t *testing.T, ctx context.Context, root, projectPath string) (*Service, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})

	svc := New(config.Default(), st, events.NewBus(), nil)
	if _, err := svc.CreateOrAttachProject(ctx, CreateOrAttachProjectRequest{
		ParentPath: root,
		Name:       filepath.Base(projectPath),
	}); err != nil {
		t.Fatalf("track root project: %v", err)
	}
	return svc, st
}

func createGitlinkConflictWorktreeForTest(t *testing.T, ctx context.Context, svc *Service, st *store.Store, projectPath, todoText, branchName, worktreeSuffix string) CreateTodoWorktreeResult {
	t.Helper()
	item, err := svc.AddTodo(ctx, projectPath, todoText)
	if err != nil {
		t.Fatalf("add todo: %v", err)
	}
	queueTodoWorktreeSuggestionForTest(t, ctx, st, item.ID)
	suggestion, err := st.ClaimNextQueuedTodoWorktreeSuggestion(ctx, 0, 0)
	if err != nil {
		t.Fatalf("claim todo worktree suggestion: %v", err)
	}
	suggestion.BranchName = branchName
	suggestion.WorktreeSuffix = worktreeSuffix
	suggestion.Kind = "feature"
	suggestion.Reason = "Creates a linked worktree for gitlink conflict coverage."
	suggestion.Confidence = 0.95
	suggestion.Model = "test"
	if completed, err := st.CompleteTodoWorktreeSuggestion(ctx, suggestion); err != nil {
		t.Fatalf("complete todo worktree suggestion: %v", err)
	} else if !completed {
		t.Fatalf("expected todo worktree suggestion to complete")
	}

	result, err := svc.CreateTodoWorktree(ctx, CreateTodoWorktreeRequest{
		ProjectPath: projectPath,
		TodoID:      item.ID,
		PrepProfile: worktreeprep.RecursiveSubmodulesProfile,
	})
	if err != nil {
		t.Fatalf("CreateTodoWorktree() error = %v", err)
	}
	runGit(t, result.WorktreePath, "git", "-c", "protocol.file.allow=always", "submodule", "update", "--init", "--recursive")
	return result
}

func commitAndPushSubmoduleFileForTest(t *testing.T, repoPath, relPath, content, message, pushBranch string) string {
	t.Helper()
	path := filepath.Join(repoPath, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir submodule file parent: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write submodule file: %v", err)
	}
	runGit(t, repoPath, "git", "add", relPath)
	runGit(t, repoPath, "git", "commit", "-m", message)
	if strings.TrimSpace(pushBranch) != "" {
		runGit(t, repoPath, "git", "push", "-u", "origin", "HEAD:"+pushBranch)
	}
	return strings.TrimSpace(gitOutput(t, repoPath, "git", "rev-parse", "HEAD"))
}

func assertGitlinkConflictRootClean(t *testing.T, ctx context.Context, projectPath string) {
	t.Helper()
	rootStatus, err := scanner.ReadGitRepoStatus(ctx, projectPath)
	if err != nil {
		t.Fatalf("read root git status after merge-back: %v", err)
	}
	if rootStatus.Dirty {
		t.Fatalf("root repo should be clean after gitlink conflict merge-back, got %#v", rootStatus)
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
