package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"lcroom/internal/gitops"
)

type GitlinkConflictResolution struct {
	Path     string
	Base     string
	Ours     string
	Theirs   string
	Resolved string
	Mode     string
	Branch   string
}

type gitlinkConflict struct {
	Path   string
	Base   string
	Ours   string
	Theirs string
}

type submoduleContentMergeConflictError struct {
	Path         string
	WorktreePath string
	Branch       string
	Ours         string
	Theirs       string
	Err          error
	Output       string
}

func (e submoduleContentMergeConflictError) Error() string {
	parts := []string{
		fmt.Sprintf("submodule %s content merge needs manual resolution", e.Path),
		fmt.Sprintf("merge worktree: %s", e.WorktreePath),
		fmt.Sprintf("branch: %s", e.Branch),
		fmt.Sprintf("ours: %s", e.Ours),
		fmt.Sprintf("theirs: %s", e.Theirs),
	}
	if output := strings.TrimSpace(e.Output); output != "" {
		parts = append(parts, "git output: "+output)
	}
	return strings.Join(parts, "; ")
}

func (e submoduleContentMergeConflictError) Unwrap() error {
	return e.Err
}

func (s *Service) ResolveGitlinkConflicts(ctx context.Context, parentRepoPath string) ([]GitlinkConflictResolution, error) {
	parentRepoPath = filepath.Clean(strings.TrimSpace(parentRepoPath))
	if parentRepoPath == "" || parentRepoPath == "." {
		return nil, fmt.Errorf("parent repo path is required")
	}
	conflicts, err := readUnmergedGitlinkConflicts(ctx, parentRepoPath)
	if err != nil {
		return nil, err
	}
	if len(conflicts) == 0 {
		return nil, nil
	}

	parentBranch := ""
	if s != nil && s.gitRepoStatusReader != nil {
		if status, statusErr := s.gitRepoStatusReader(ctx, parentRepoPath); statusErr == nil {
			parentBranch = status.Branch
		}
	}
	if strings.TrimSpace(parentBranch) == "" {
		parentBranch, _ = gitCurrentBranch(ctx, parentRepoPath)
	}

	resolved := make([]GitlinkConflictResolution, 0, len(conflicts))
	for _, conflict := range conflicts {
		item, err := resolveGitlinkConflict(ctx, parentRepoPath, parentBranch, conflict)
		if err != nil {
			return resolved, err
		}
		resolved = append(resolved, item)
	}
	return resolved, nil
}

func readUnmergedGitlinkConflicts(ctx context.Context, parentRepoPath string) ([]gitlinkConflict, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", parentRepoPath, "ls-files", "-u", "-z")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("list unmerged files in %s: %w", parentRepoPath, err)
	}
	byPath := map[string]*gitlinkConflict{}
	for _, record := range strings.Split(string(out), "\x00") {
		if record == "" {
			continue
		}
		header, path, ok := strings.Cut(record, "\t")
		if !ok {
			continue
		}
		fields := strings.Fields(header)
		if len(fields) != 3 || fields[0] != "160000" {
			continue
		}
		stage, err := strconv.Atoi(fields[2])
		if err != nil {
			continue
		}
		path = filepath.ToSlash(filepath.Clean(filepath.FromSlash(path)))
		if path == "" || path == "." || strings.HasPrefix(path, "../") || strings.HasPrefix(path, "/") {
			continue
		}
		conflict := byPath[path]
		if conflict == nil {
			conflict = &gitlinkConflict{Path: path}
			byPath[path] = conflict
		}
		switch stage {
		case 1:
			conflict.Base = fields[1]
		case 2:
			conflict.Ours = fields[1]
		case 3:
			conflict.Theirs = fields[1]
		}
	}
	paths := make([]string, 0, len(byPath))
	for path := range byPath {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	outConflicts := make([]gitlinkConflict, 0, len(paths))
	for _, path := range paths {
		conflict := *byPath[path]
		if strings.TrimSpace(conflict.Ours) == "" || strings.TrimSpace(conflict.Theirs) == "" {
			return nil, fmt.Errorf("gitlink conflict %s is missing ours or theirs stage", conflict.Path)
		}
		outConflicts = append(outConflicts, conflict)
	}
	return outConflicts, nil
}

func resolveGitlinkConflict(ctx context.Context, parentRepoPath, parentBranch string, conflict gitlinkConflict) (GitlinkConflictResolution, error) {
	submoduleRepoPath, err := initializedSubmoduleRepoPath(ctx, parentRepoPath, conflict.Path)
	if err != nil {
		return GitlinkConflictResolution{}, err
	}
	for _, commit := range []string{conflict.Base, conflict.Ours, conflict.Theirs} {
		if strings.TrimSpace(commit) == "" {
			continue
		}
		if err := ensureSubmoduleRepoHasCommit(ctx, submoduleRepoPath, conflict.Path, commit); err != nil {
			return GitlinkConflictResolution{}, err
		}
	}

	oursAncestor, err := gitIsAncestor(ctx, submoduleRepoPath, conflict.Ours, conflict.Theirs)
	if err != nil {
		return GitlinkConflictResolution{}, err
	}
	if oursAncestor {
		if err := stageParentGitlink(ctx, parentRepoPath, conflict.Path, conflict.Theirs); err != nil {
			return GitlinkConflictResolution{}, err
		}
		return GitlinkConflictResolution{
			Path:     conflict.Path,
			Base:     conflict.Base,
			Ours:     conflict.Ours,
			Theirs:   conflict.Theirs,
			Resolved: conflict.Theirs,
			Mode:     "theirs-fast-forward",
		}, nil
	}

	theirsAncestor, err := gitIsAncestor(ctx, submoduleRepoPath, conflict.Theirs, conflict.Ours)
	if err != nil {
		return GitlinkConflictResolution{}, err
	}
	if theirsAncestor {
		if err := stageParentGitlink(ctx, parentRepoPath, conflict.Path, conflict.Ours); err != nil {
			return GitlinkConflictResolution{}, err
		}
		return GitlinkConflictResolution{
			Path:     conflict.Path,
			Base:     conflict.Base,
			Ours:     conflict.Ours,
			Theirs:   conflict.Theirs,
			Resolved: conflict.Ours,
			Mode:     "ours-fast-forward",
		}, nil
	}

	resolved, branch, err := mergeDivergentGitlinkConflict(ctx, submoduleRepoPath, parentBranch, conflict)
	if err != nil {
		return GitlinkConflictResolution{}, err
	}
	if err := stageParentGitlink(ctx, parentRepoPath, conflict.Path, resolved); err != nil {
		return GitlinkConflictResolution{}, err
	}
	return GitlinkConflictResolution{
		Path:     conflict.Path,
		Base:     conflict.Base,
		Ours:     conflict.Ours,
		Theirs:   conflict.Theirs,
		Resolved: resolved,
		Mode:     "submodule-merge",
		Branch:   branch,
	}, nil
}

func initializedSubmoduleRepoPath(ctx context.Context, parentRepoPath, submodulePath string) (string, error) {
	path := filepath.Join(parentRepoPath, filepath.FromSlash(submodulePath))
	cmd := exec.CommandContext(ctx, "git", "-C", path, "rev-parse", "--show-toplevel")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("submodule %s is not initialized at %s; initialize it before resolving the gitlink conflict", submodulePath, path)
	}
	topLevel := filepath.Clean(strings.TrimSpace(string(out)))
	if !samePath(topLevel, path) {
		return "", fmt.Errorf("submodule %s is not an initialized Git repo at %s", submodulePath, path)
	}
	return path, nil
}

func samePath(a, b string) bool {
	a = filepath.Clean(strings.TrimSpace(a))
	b = filepath.Clean(strings.TrimSpace(b))
	if resolved, err := filepath.EvalSymlinks(a); err == nil {
		a = filepath.Clean(resolved)
	}
	if resolved, err := filepath.EvalSymlinks(b); err == nil {
		b = filepath.Clean(resolved)
	}
	return a == b
}

func ensureSubmoduleRepoHasCommit(ctx context.Context, repoPath, displayPath, commit string) error {
	if gitRepoCommitExists(ctx, repoPath, commit) {
		return nil
	}
	cmd := exec.CommandContext(ctx, "git", "-C", repoPath, "fetch", "--all", "--tags")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("fetch submodule %s commits: %w: %s", displayPath, err, strings.TrimSpace(string(out)))
	}
	if !gitRepoCommitExists(ctx, repoPath, commit) {
		return fmt.Errorf("submodule %s does not have required commit %s after fetch", displayPath, commit)
	}
	return nil
}

func gitRepoCommitExists(ctx context.Context, repoPath, commit string) bool {
	commit = strings.TrimSpace(commit)
	if commit == "" {
		return false
	}
	cmd := exec.CommandContext(ctx, "git", "-C", repoPath, "cat-file", "-e", commit+"^{commit}")
	return cmd.Run() == nil
}

func gitIsAncestor(ctx context.Context, repoPath, ancestor, descendant string) (bool, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", repoPath, "merge-base", "--is-ancestor", ancestor, descendant)
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return false, nil
		}
		return false, fmt.Errorf("check submodule ancestry %s..%s in %s: %w", ancestor, descendant, repoPath, err)
	}
	return true, nil
}

func stageParentGitlink(ctx context.Context, parentRepoPath, submodulePath, commit string) error {
	cmd := exec.CommandContext(ctx, "git", "-C", parentRepoPath, "update-index", "--add", "--cacheinfo", "160000", commit, submodulePath)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("stage resolved gitlink %s at %s: %w: %s", submodulePath, commit, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func mergeDivergentGitlinkConflict(ctx context.Context, submoduleRepoPath, parentBranch string, conflict gitlinkConflict) (string, string, error) {
	tempRoot, err := os.MkdirTemp("", "lcroom-submodule-merge-*")
	if err != nil {
		return "", "", fmt.Errorf("create submodule merge temp dir: %w", err)
	}
	cleanupTempRoot := true
	defer func() {
		if cleanupTempRoot {
			_ = os.RemoveAll(tempRoot)
		}
	}()

	worktreePath := filepath.Join(tempRoot, filepath.Base(filepath.FromSlash(conflict.Path)))
	branchBase := gitlinkMergeBranchName(parentBranch, conflict.Path, conflict.Ours, conflict.Theirs)
	branch, err := addSubmoduleMergeWorktree(ctx, submoduleRepoPath, worktreePath, branchBase, conflict.Ours)
	if err != nil {
		return "", "", err
	}
	cleanupWorktree := true
	defer func() {
		if cleanupWorktree {
			_ = removeSubmoduleMergeWorktree(context.Background(), submoduleRepoPath, worktreePath)
		}
	}()

	cmd := exec.CommandContext(ctx, "git", "-C", worktreePath, "merge", "--no-edit", conflict.Theirs)
	if out, err := cmd.CombinedOutput(); err != nil {
		cleanupWorktree = false
		cleanupTempRoot = false
		return "", "", submoduleContentMergeConflictError{
			Path:         conflict.Path,
			WorktreePath: worktreePath,
			Branch:       branch,
			Ours:         conflict.Ours,
			Theirs:       conflict.Theirs,
			Err:          err,
			Output:       strings.TrimSpace(string(out)),
		}
	}
	if err := gitops.PushSetUpstream(ctx, worktreePath, "origin"); err != nil {
		return "", "", fmt.Errorf("push submodule merge branch %s: %w", branch, err)
	}
	resolved, err := gitCommitHash(ctx, worktreePath, "HEAD")
	if err != nil {
		return "", "", err
	}
	return resolved, branch, nil
}

func gitlinkMergeBranchName(parentBranch, submodulePath, ours, theirs string) string {
	oursShort := shortCommitForBranch(ours)
	theirsShort := shortCommitForBranch(theirs)
	suffix := "merge"
	if oursShort != "" && theirsShort != "" {
		suffix += "-" + oursShort + "-" + theirsShort
	}
	return "lcroom/" + defaultBranchComponent(parentBranch, "merge-parent") + "/" + defaultBranchComponent(submodulePath, "submodule") + "-" + suffix
}

func defaultBranchComponent(raw, fallback string) string {
	out := sanitizeBranchComponent(raw)
	if out == "" {
		return fallback
	}
	return out
}

func shortCommitForBranch(commit string) string {
	commit = sanitizeBranchComponent(commit)
	if len(commit) > 10 {
		return commit[:10]
	}
	return commit
}

func addSubmoduleMergeWorktree(ctx context.Context, submoduleRepoPath, worktreePath, baseBranch, startCommit string) (string, error) {
	for i := 0; i < 20; i++ {
		branch := baseBranch
		if i > 0 {
			branch = fmt.Sprintf("%s-%d", baseBranch, i+1)
		}
		exists, err := gitLocalBranchExists(ctx, submoduleRepoPath, branch)
		if err != nil {
			return "", err
		}
		if exists {
			continue
		}
		cmd := exec.CommandContext(ctx, "git", "-C", submoduleRepoPath, "worktree", "add", "-b", branch, worktreePath, startCommit)
		if out, err := cmd.CombinedOutput(); err != nil {
			return "", fmt.Errorf("create submodule merge worktree %s on %s: %w: %s", worktreePath, branch, err, strings.TrimSpace(string(out)))
		}
		return branch, nil
	}
	return "", fmt.Errorf("could not find available submodule merge branch for %s", baseBranch)
}

func removeSubmoduleMergeWorktree(ctx context.Context, submoduleRepoPath, worktreePath string) error {
	cmd := exec.CommandContext(ctx, "git", "-C", submoduleRepoPath, "worktree", "remove", "--force", worktreePath)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("remove submodule merge worktree %s: %w: %s", worktreePath, err, strings.TrimSpace(string(out)))
	}
	prune := exec.CommandContext(ctx, "git", "-C", submoduleRepoPath, "worktree", "prune")
	if out, err := prune.CombinedOutput(); err != nil {
		return fmt.Errorf("prune submodule merge worktrees: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func gitCurrentBranch(ctx context.Context, repoPath string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", repoPath, "branch", "--show-current")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func gitCommitMergeNoEdit(ctx context.Context, repoPath string) error {
	cmd := exec.CommandContext(ctx, "git", "-C", repoPath, "commit", "--no-edit")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("commit resolved merge in %s: %w: %s", repoPath, err, strings.TrimSpace(string(out)))
	}
	return nil
}
