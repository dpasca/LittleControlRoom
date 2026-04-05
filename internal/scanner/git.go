package scanner

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type GitFingerprint struct {
	HeadHash     string
	RecentHashes []string
}

type GitRepoStatus struct {
	Dirty       bool
	HasRemote   bool
	HasUpstream bool
	Ahead       int
	Behind      int
	Branch      string
	Changes     []GitChange
}

type GitWorktreeKind string

const (
	GitWorktreeKindNone   GitWorktreeKind = ""
	GitWorktreeKindMain   GitWorktreeKind = "main"
	GitWorktreeKindLinked GitWorktreeKind = "linked"
)

type GitWorktreeInfo struct {
	RootPath string
	Kind     GitWorktreeKind
}

type GitWorktree struct {
	Path           string
	Branch         string
	Head           string
	IsMain         bool
	Detached       bool
	LockedReason   string
	PrunableReason string
}

type GitChange struct {
	Path         string
	OriginalPath string
	Code         string
	Kind         GitChangeKind
	Staged       bool
	Unstaged     bool
	Untracked    bool
	IsSubmodule  bool

	SubmoduleCommitChanged bool
	SubmoduleModified      bool
	SubmoduleUntracked     bool
}

type GitChangeKind string

const (
	GitChangeModified  GitChangeKind = "modified"
	GitChangeAdded     GitChangeKind = "added"
	GitChangeDeleted   GitChangeKind = "deleted"
	GitChangeRenamed   GitChangeKind = "renamed"
	GitChangeCopied    GitChangeKind = "copied"
	GitChangeType      GitChangeKind = "type_changed"
	GitChangeUnmerged  GitChangeKind = "unmerged"
	GitChangeUntracked GitChangeKind = "untracked"
	GitChangeUnknown   GitChangeKind = "unknown"
)

func (c GitChange) ParentCommitEligible() bool {
	if !c.IsSubmodule {
		return true
	}
	switch c.Kind {
	case GitChangeAdded, GitChangeDeleted, GitChangeRenamed, GitChangeCopied, GitChangeType, GitChangeUnmerged, GitChangeUntracked:
		return true
	}
	return c.Staged || c.SubmoduleCommitChanged
}

func (c GitChange) SubmoduleWorktreeDirty() bool {
	return c.IsSubmodule && (c.SubmoduleModified || c.SubmoduleUntracked)
}

func (s GitRepoStatus) StagedChanges() []GitChange {
	out := make([]GitChange, 0, len(s.Changes))
	for _, change := range s.Changes {
		if change.Staged {
			out = append(out, change)
		}
	}
	return out
}

func (s GitRepoStatus) UnstagedChanges() []GitChange {
	out := make([]GitChange, 0, len(s.Changes))
	for _, change := range s.Changes {
		if change.Unstaged {
			out = append(out, change)
		}
	}
	return out
}

func (s GitRepoStatus) UntrackedChanges() []GitChange {
	out := make([]GitChange, 0, len(s.Changes))
	for _, change := range s.Changes {
		if change.Untracked {
			out = append(out, change)
		}
	}
	return out
}

func ReadGitFingerprint(ctx context.Context, path string) (GitFingerprint, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", path, "rev-list", "--max-count=3", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return GitFingerprint{}, fmt.Errorf("read git fingerprint for %s: %w", path, err)
	}

	lines := strings.Fields(strings.TrimSpace(string(out)))
	if len(lines) == 0 {
		return GitFingerprint{}, fmt.Errorf("read git fingerprint for %s: no commits", path)
	}

	return GitFingerprint{
		HeadHash:     lines[0],
		RecentHashes: lines,
	}, nil
}

func ReadGitRepoStatus(ctx context.Context, path string) (GitRepoStatus, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", path, "status", "--porcelain=v2", "--branch", "--untracked-files=normal")
	out, err := cmd.Output()
	if err != nil {
		return GitRepoStatus{}, fmt.Errorf("read git repo status for %s: %w", path, err)
	}
	status := parseGitRepoStatusOutput(string(out))
	if status.HasUpstream {
		return status, nil
	}

	remoteCmd := exec.CommandContext(ctx, "git", "-C", path, "remote")
	remoteOut, err := remoteCmd.Output()
	if err != nil {
		return GitRepoStatus{}, fmt.Errorf("read git remotes for %s: %w", path, err)
	}
	status.HasRemote = len(strings.Fields(strings.TrimSpace(string(remoteOut)))) > 0
	return status, nil
}

func ReadGitDirty(ctx context.Context, path string) (bool, error) {
	status, err := ReadGitRepoStatus(ctx, path)
	if err != nil {
		return false, err
	}
	return status.Dirty, nil
}

func ReadGitWorktreeInfo(ctx context.Context, path string) (GitWorktreeInfo, error) {
	topLevel, err := readGitSingleLine(ctx, path, "rev-parse", "--show-toplevel")
	if err != nil {
		if fallback, fallbackErr := readGitWorktreeInfoFromGitFile(path); fallbackErr == nil {
			return fallback, nil
		}
		return GitWorktreeInfo{}, fmt.Errorf("read git top-level for %s: %w", path, err)
	}
	commonDir, err := readGitSingleLine(ctx, path, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		if fallback, fallbackErr := readGitWorktreeInfoFromGitFile(path); fallbackErr == nil {
			return fallback, nil
		}
		return GitWorktreeInfo{}, fmt.Errorf("read git common dir for %s: %w", path, err)
	}
	gitDir, err := readGitSingleLine(ctx, path, "rev-parse", "--absolute-git-dir")
	if err != nil {
		if fallback, fallbackErr := readGitWorktreeInfoFromGitFile(path); fallbackErr == nil {
			return fallback, nil
		}
		return GitWorktreeInfo{}, fmt.Errorf("read git dir for %s: %w", path, err)
	}

	topLevel = filepath.Clean(strings.TrimSpace(topLevel))
	commonDir = filepath.Clean(strings.TrimSpace(commonDir))
	gitDir = filepath.Clean(strings.TrimSpace(gitDir))
	if topLevel == "" || commonDir == "" {
		return GitWorktreeInfo{}, fmt.Errorf("read git worktree info for %s: missing paths", path)
	}

	rootPath := filepath.Dir(commonDir)
	kind := GitWorktreeKindLinked
	if gitDir == commonDir || rootPath == topLevel {
		kind = GitWorktreeKindMain
		rootPath = topLevel
	}
	return GitWorktreeInfo{
		RootPath: rootPath,
		Kind:     kind,
	}, nil
}

func readGitWorktreeInfoFromGitFile(path string) (GitWorktreeInfo, error) {
	gitPath := filepath.Join(path, ".git")
	info, err := os.Stat(gitPath)
	if err != nil {
		return GitWorktreeInfo{}, err
	}
	if info.IsDir() {
		return GitWorktreeInfo{}, fmt.Errorf("%s is a directory", gitPath)
	}

	content, err := os.ReadFile(gitPath)
	if err != nil {
		return GitWorktreeInfo{}, err
	}
	line := strings.TrimSpace(string(content))
	const prefix = "gitdir:"
	if !strings.HasPrefix(line, prefix) {
		return GitWorktreeInfo{}, fmt.Errorf("%s does not contain a gitdir pointer", gitPath)
	}

	gitDir := strings.TrimSpace(strings.TrimPrefix(line, prefix))
	if gitDir == "" {
		return GitWorktreeInfo{}, fmt.Errorf("%s contains an empty gitdir pointer", gitPath)
	}
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(path, gitDir)
	}
	gitDir = filepath.Clean(gitDir)
	worktreesDir := filepath.Dir(gitDir)
	commonDir := filepath.Dir(worktreesDir)
	if filepath.Base(worktreesDir) != "worktrees" || filepath.Base(commonDir) != ".git" {
		return GitWorktreeInfo{}, fmt.Errorf("%s does not point at a linked worktree admin dir", gitPath)
	}

	rootPath := filepath.Clean(filepath.Dir(commonDir))
	if rootPath == "" || rootPath == "." {
		return GitWorktreeInfo{}, fmt.Errorf("%s resolves to an empty worktree root", gitPath)
	}
	return GitWorktreeInfo{
		RootPath: rootPath,
		Kind:     GitWorktreeKindLinked,
	}, nil
}

func ListGitWorktrees(ctx context.Context, path string) ([]GitWorktree, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", path, "worktree", "list", "--porcelain")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("list git worktrees for %s: %w", path, err)
	}
	worktrees := parseGitWorktreeListOutput(string(out))
	if len(worktrees) == 0 {
		if info, infoErr := ReadGitWorktreeInfo(ctx, path); infoErr == nil && strings.TrimSpace(info.RootPath) != "" {
			return []GitWorktree{{
				Path:   info.RootPath,
				IsMain: true,
			}}, nil
		}
	}
	return worktrees, nil
}

func readGitSingleLine(ctx context.Context, path string, args ...string) (string, error) {
	fullArgs := append([]string{"-C", path}, args...)
	cmd := exec.CommandContext(ctx, "git", fullArgs...)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func parseGitWorktreeListOutput(out string) []GitWorktree {
	blocks := strings.Split(strings.TrimSpace(out), "\n\n")
	worktrees := make([]GitWorktree, 0, len(blocks))
	for index, block := range blocks {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}
		entry := GitWorktree{IsMain: index == 0}
		for _, line := range strings.Split(block, "\n") {
			line = strings.TrimSpace(line)
			switch {
			case strings.HasPrefix(line, "worktree "):
				entry.Path = filepath.Clean(strings.TrimSpace(strings.TrimPrefix(line, "worktree ")))
			case strings.HasPrefix(line, "HEAD "):
				entry.Head = strings.TrimSpace(strings.TrimPrefix(line, "HEAD "))
			case strings.HasPrefix(line, "branch "):
				entry.Branch = strings.TrimSpace(strings.TrimPrefix(line, "branch refs/heads/"))
			case line == "detached":
				entry.Detached = true
			case strings.HasPrefix(line, "locked"):
				entry.LockedReason = strings.TrimSpace(strings.TrimPrefix(line, "locked"))
			case strings.HasPrefix(line, "prunable"):
				entry.PrunableReason = strings.TrimSpace(strings.TrimPrefix(line, "prunable"))
			}
		}
		if entry.Path == "" {
			continue
		}
		worktrees = append(worktrees, entry)
	}
	return worktrees
}

func parseGitRepoStatusOutput(out string) GitRepoStatus {
	status := GitRepoStatus{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "# ") {
			switch {
			case strings.HasPrefix(line, "# branch.head "):
				status.Branch = strings.TrimSpace(strings.TrimPrefix(line, "# branch.head "))
			case strings.HasPrefix(line, "# branch.upstream "):
				status.HasUpstream = true
				status.HasRemote = true
			case strings.HasPrefix(line, "# branch.ab "):
				var ahead, behind int
				if _, err := fmt.Sscanf(line, "# branch.ab +%d -%d", &ahead, &behind); err == nil {
					status.Ahead = ahead
					status.Behind = behind
				}
			}
			continue
		}
		change, ok := parseGitChangeLine(line)
		if !ok {
			continue
		}
		status.Dirty = true
		status.Changes = append(status.Changes, change)
	}
	return status
}

func parseGitChangeLine(line string) (GitChange, bool) {
	switch {
	case strings.HasPrefix(line, "? "):
		path := strings.TrimSpace(strings.TrimPrefix(line, "? "))
		if path == "" {
			return GitChange{}, false
		}
		return GitChange{
			Path:      path,
			Code:      "??",
			Kind:      GitChangeUntracked,
			Untracked: true,
			Unstaged:  true,
		}, true
	case strings.HasPrefix(line, "1 "):
		return parseOrdinaryGitChange(line)
	case strings.HasPrefix(line, "2 "):
		return parseRenamedGitChange(line)
	case strings.HasPrefix(line, "u "):
		return parseUnmergedGitChange(line)
	default:
		return GitChange{}, false
	}
}

func parseOrdinaryGitChange(line string) (GitChange, bool) {
	fields := strings.Fields(line)
	if len(fields) < 9 {
		return GitChange{}, false
	}
	return buildGitChange(fields[1], fields[2], fields[8], "", false), true
}

func parseRenamedGitChange(line string) (GitChange, bool) {
	parts := strings.SplitN(line, "\t", 2)
	if len(parts) != 2 {
		return GitChange{}, false
	}
	header := strings.Fields(parts[0])
	if len(header) < 9 {
		return GitChange{}, false
	}
	target := strings.TrimSpace(parts[1])
	if target == "" {
		return GitChange{}, false
	}
	return buildGitChange(header[1], header[2], target, header[len(header)-1], true), true
}

func parseUnmergedGitChange(line string) (GitChange, bool) {
	fields := strings.Fields(line)
	if len(fields) < 11 {
		return GitChange{}, false
	}
	change := buildGitChange(fields[1], fields[2], fields[10], "", false)
	change.Kind = GitChangeUnmerged
	change.Code = strings.ToUpper(fields[1])
	change.Staged = true
	change.Unstaged = true
	return change, true
}

func buildGitChange(xy, submoduleState, path, originalPath string, renamed bool) GitChange {
	indexCode, worktreeCode := splitXY(xy)
	kind := gitChangeKind(indexCode, worktreeCode, renamed)
	isSubmodule, commitChanged, modified, untracked := parseGitSubmoduleState(submoduleState)
	change := GitChange{
		Path:         path,
		OriginalPath: originalPath,
		Code:         gitChangeCode(indexCode, worktreeCode),
		Kind:         kind,
		IsSubmodule:  isSubmodule,

		SubmoduleCommitChanged: commitChanged,
		SubmoduleModified:      modified,
		SubmoduleUntracked:     untracked,
	}
	if kind == GitChangeUntracked {
		change.Untracked = true
		change.Unstaged = true
		return change
	}
	change.Staged = indexCode != '.'
	change.Unstaged = worktreeCode != '.'
	return change
}

func parseGitSubmoduleState(token string) (bool, bool, bool, bool) {
	if len(token) != 4 || token[0] != 'S' {
		return false, false, false, false
	}
	return true, token[1] == 'C', token[2] == 'M', token[3] == 'U'
}

func splitXY(xy string) (byte, byte) {
	if len(xy) < 2 {
		return '.', '.'
	}
	return xy[0], xy[1]
}

func gitChangeKind(indexCode, worktreeCode byte, renamed bool) GitChangeKind {
	switch {
	case renamed || indexCode == 'R' || worktreeCode == 'R':
		return GitChangeRenamed
	case indexCode == 'C' || worktreeCode == 'C':
		return GitChangeCopied
	case indexCode == 'A' || worktreeCode == 'A':
		return GitChangeAdded
	case indexCode == 'D' || worktreeCode == 'D':
		return GitChangeDeleted
	case indexCode == 'T' || worktreeCode == 'T':
		return GitChangeType
	case indexCode == 'U' || worktreeCode == 'U':
		return GitChangeUnmerged
	case indexCode == '?' || worktreeCode == '?':
		return GitChangeUntracked
	case indexCode == 'M' || worktreeCode == 'M':
		return GitChangeModified
	default:
		return GitChangeUnknown
	}
}

func gitChangeCode(indexCode, worktreeCode byte) string {
	if indexCode == '?' && worktreeCode == '?' {
		return "??"
	}
	if indexCode == '.' {
		return string([]byte{worktreeCode})
	}
	if worktreeCode == '.' {
		return string([]byte{indexCode})
	}
	return string([]byte{indexCode, worktreeCode})
}
