package service

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"lcroom/internal/model"
)

type ResidualWorktreeCleanupKind string

const (
	ResidualWorktreeCleanupUnknown       ResidualWorktreeCleanupKind = ""
	ResidualWorktreeCleanupDSStoreOnly   ResidualWorktreeCleanupKind = "ds_store_only"
	ResidualWorktreeCleanupPartialGitDir ResidualWorktreeCleanupKind = "partial_git_removal"
)

const (
	maxResidualGitFileSize  = 16 * 1024
	maxResidualTrackedFiles = 100_000
	maxResidualTrackedBytes = int64(1 << 30)
)

var errStopResidualInspection = errors.New("stop residual worktree inspection")

type residualWorktreeEntry struct {
	Path string
	Info os.FileInfo
}

type residualWorktreeInspection struct {
	Kind                ResidualWorktreeCleanupKind
	Safe                bool
	Reason              string
	Commit              string
	TrackedFileCount    int
	DSStoreCount        int
	EmptyDirectoryCount int
	Entries             []residualWorktreeEntry
}

type staleWorktreeGitFile struct {
	Path   string
	Target string
	Info   os.FileInfo
}

type residualGitTreeEntry struct {
	Mode   string
	Type   string
	Object string
}

func (s *Service) residualWorktreeCleanupKind(ctx context.Context, rootPath, projectPath string) (ResidualWorktreeCleanupKind, error) {
	onlyDSStore, err := directoryContainsOnlyRegularDSStore(projectPath)
	if err != nil {
		return ResidualWorktreeCleanupUnknown, err
	}
	if onlyDSStore {
		return ResidualWorktreeCleanupDSStoreOnly, nil
	}
	_, ok, err := inspectStaleWorktreeGitFile(ctx, rootPath, projectPath)
	if err != nil {
		return ResidualWorktreeCleanupUnknown, err
	}
	if ok {
		return ResidualWorktreeCleanupPartialGitDir, nil
	}
	return ResidualWorktreeCleanupUnknown, nil
}

func (s *Service) inspectResidualWorktreeDirectory(
	ctx context.Context,
	rootPath string,
	projectPath string,
	summary model.ProjectSummary,
	expectedCommit string,
) (residualWorktreeInspection, error) {
	onlyDSStore, err := directoryContainsOnlyRegularDSStore(projectPath)
	if err != nil {
		return residualWorktreeInspection{}, err
	}
	if onlyDSStore {
		return residualWorktreeInspection{
			Kind:         ResidualWorktreeCleanupDSStoreOnly,
			Safe:         true,
			DSStoreCount: 1,
		}, nil
	}

	gitFile, ok, err := inspectStaleWorktreeGitFile(ctx, rootPath, projectPath)
	if err != nil {
		return residualWorktreeInspection{}, err
	}
	if !ok {
		return residualWorktreeInspection{
			Reason: "the folder does not contain a stale worktree .git pointer for the expected repository",
		}, nil
	}

	commits, reason, err := residualWorktreeCommitCandidates(ctx, rootPath, summary, expectedCommit)
	if err != nil {
		return residualWorktreeInspection{}, err
	}
	if len(commits) == 0 {
		return residualWorktreeInspection{
			Kind:   ResidualWorktreeCleanupPartialGitDir,
			Reason: reason,
		}, nil
	}

	var firstUnsafe residualWorktreeInspection
	for _, commit := range commits {
		inspection, inspectErr := inspectResidualWorktreeAgainstCommit(ctx, rootPath, projectPath, gitFile, commit)
		if inspectErr != nil {
			return residualWorktreeInspection{}, inspectErr
		}
		if inspection.Safe {
			return inspection, nil
		}
		if firstUnsafe.Reason == "" {
			firstUnsafe = inspection
		}
	}
	if firstUnsafe.Reason == "" {
		firstUnsafe = residualWorktreeInspection{
			Kind:   ResidualWorktreeCleanupPartialGitDir,
			Reason: "the remaining checkout could not be verified against its preserved branch",
		}
	}
	return firstUnsafe, nil
}

func inspectStaleWorktreeGitFile(ctx context.Context, rootPath, projectPath string) (staleWorktreeGitFile, bool, error) {
	rootPath = filepath.Clean(strings.TrimSpace(rootPath))
	projectPath = filepath.Clean(strings.TrimSpace(projectPath))
	if rootPath == "" || rootPath == "." || projectPath == "" || projectPath == "." {
		return staleWorktreeGitFile{}, false, nil
	}

	gitFilePath := filepath.Join(projectPath, ".git")
	info, err := os.Lstat(gitFilePath)
	if err != nil {
		if os.IsNotExist(err) {
			return staleWorktreeGitFile{}, false, nil
		}
		return staleWorktreeGitFile{}, false, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > maxResidualGitFileSize {
		return staleWorktreeGitFile{}, false, nil
	}
	file, err := os.Open(gitFilePath)
	if err != nil {
		return staleWorktreeGitFile{}, false, err
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil {
		return staleWorktreeGitFile{}, false, err
	}
	if !openedInfo.Mode().IsRegular() || !os.SameFile(info, openedInfo) || openedInfo.Size() > maxResidualGitFileSize {
		return staleWorktreeGitFile{}, false, nil
	}
	content, err := io.ReadAll(io.LimitReader(contextReader{Context: ctx, Reader: file}, maxResidualGitFileSize+1))
	if err != nil {
		return staleWorktreeGitFile{}, false, err
	}
	if len(content) > maxResidualGitFileSize {
		return staleWorktreeGitFile{}, false, nil
	}
	afterInfo, err := file.Stat()
	if err != nil {
		return staleWorktreeGitFile{}, false, err
	}
	if !sameResidualFileSnapshot(openedInfo, afterInfo) {
		return staleWorktreeGitFile{}, false, nil
	}
	line := strings.TrimSpace(string(content))
	const prefix = "gitdir:"
	if !strings.HasPrefix(line, prefix) {
		return staleWorktreeGitFile{}, false, nil
	}
	target := strings.TrimSpace(strings.TrimPrefix(line, prefix))
	if target == "" || strings.ContainsRune(target, '\x00') {
		return staleWorktreeGitFile{}, false, nil
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(projectPath, target)
	}
	target = filepath.Clean(target)

	worktreesPath, err := gitPath(ctx, rootPath, "worktrees")
	if err != nil {
		return staleWorktreeGitFile{}, false, err
	}
	if !samePath(filepath.Dir(target), worktreesPath) {
		return staleWorktreeGitFile{}, false, nil
	}
	if _, err := os.Lstat(target); err == nil {
		return staleWorktreeGitFile{}, false, nil
	} else if !os.IsNotExist(err) {
		return staleWorktreeGitFile{}, false, err
	}
	return staleWorktreeGitFile{
		Path:   gitFilePath,
		Target: target,
		Info:   info,
	}, true, nil
}

func residualWorktreeCommitCandidates(
	ctx context.Context,
	rootPath string,
	summary model.ProjectSummary,
	expectedCommit string,
) ([]string, string, error) {
	refs := make([]string, 0, 2)
	if expectedCommit = strings.TrimSpace(expectedCommit); expectedCommit != "" {
		refs = append(refs, expectedCommit)
	} else {
		for _, branch := range []string{summary.RepoBranch, summary.WorktreeInitialBranch} {
			branch = strings.TrimSpace(branch)
			if branch == "" || branch == "HEAD" || strings.EqualFold(branch, "(detached)") {
				continue
			}
			if !strings.HasPrefix(branch, "refs/heads/") {
				branch = "refs/heads/" + branch
			}
			if len(refs) == 0 || refs[len(refs)-1] != branch {
				refs = append(refs, branch)
			}
		}
	}
	if len(refs) == 0 {
		return nil, "no preserved worktree branch is available for verification", nil
	}

	commits := make([]string, 0, len(refs))
	var resolutionFailures []string
	for _, ref := range refs {
		if err := ctx.Err(); err != nil {
			return nil, "", err
		}
		commit, err := gitCommitHash(ctx, rootPath, ref)
		if err != nil {
			resolutionFailures = append(resolutionFailures, ref)
			continue
		}
		duplicate := false
		for _, existing := range commits {
			if existing == commit {
				duplicate = true
				break
			}
		}
		if !duplicate {
			commits = append(commits, commit)
		}
	}
	if len(commits) == 0 {
		return nil, fmt.Sprintf("the preserved worktree ref could not be resolved (%s)", strings.Join(resolutionFailures, ", ")), nil
	}
	return commits, "", nil
}

func inspectResidualWorktreeAgainstCommit(
	ctx context.Context,
	rootPath string,
	projectPath string,
	gitFile staleWorktreeGitFile,
	commit string,
) (residualWorktreeInspection, error) {
	inspection := residualWorktreeInspection{
		Kind:   ResidualWorktreeCleanupPartialGitDir,
		Commit: commit,
	}
	tree, err := readResidualGitTree(ctx, rootPath, commit)
	if err != nil {
		return residualWorktreeInspection{}, err
	}
	objectHash, err := residualObjectHash(commit)
	if err != nil {
		return residualWorktreeInspection{}, err
	}

	trackedBytes := int64(0)
	walkErr := filepath.WalkDir(projectPath, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		inspection.Entries = append(inspection.Entries, residualWorktreeEntry{Path: path, Info: info})
		if samePath(path, projectPath) {
			if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				inspection.Reason = "the residual worktree path is not a regular directory"
				return errStopResidualInspection
			}
			return nil
		}

		rel, err := filepath.Rel(projectPath, path)
		if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			inspection.Reason = fmt.Sprintf("the residual path %s is outside the expected worktree", path)
			return errStopResidualInspection
		}
		gitRel := filepath.ToSlash(rel)
		if gitRel == ".git" {
			if !info.Mode().IsRegular() || !os.SameFile(info, gitFile.Info) {
				inspection.Reason = "the stale worktree .git pointer changed during inspection"
				return errStopResidualInspection
			}
			return nil
		}
		if filepath.Base(path) == ".git" {
			inspection.Reason = fmt.Sprintf("nested Git metadata remains at %s", rel)
			return errStopResidualInspection
		}
		if filepath.Base(path) == ".DS_Store" {
			if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
				inspection.Reason = fmt.Sprintf("%s is not a regular .DS_Store file", rel)
				return errStopResidualInspection
			}
			inspection.DSStoreCount++
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			inspection.Reason = fmt.Sprintf("symbolic link %s requires manual inspection", rel)
			return errStopResidualInspection
		}
		if info.IsDir() {
			entries, err := os.ReadDir(path)
			if err != nil {
				return err
			}
			if len(entries) == 0 {
				inspection.EmptyDirectoryCount++
			}
			return nil
		}
		if !info.Mode().IsRegular() {
			inspection.Reason = fmt.Sprintf("special file %s requires manual inspection", rel)
			return errStopResidualInspection
		}
		treeEntry, ok := tree[gitRel]
		if !ok || treeEntry.Type != "blob" {
			inspection.Reason = fmt.Sprintf("untracked file %s remains in the orphaned worktree", rel)
			return errStopResidualInspection
		}
		actualMode := "100644"
		if info.Mode().Perm()&0o111 != 0 {
			actualMode = "100755"
		}
		if treeEntry.Mode != actualMode {
			inspection.Reason = fmt.Sprintf("file mode for %s differs from commit %s", rel, shortResidualCommit(commit))
			return errStopResidualInspection
		}
		inspection.TrackedFileCount++
		if inspection.TrackedFileCount > maxResidualTrackedFiles {
			inspection.Reason = fmt.Sprintf("more than %d tracked files remain; manual inspection is required", maxResidualTrackedFiles)
			return errStopResidualInspection
		}
		trackedBytes += info.Size()
		if trackedBytes > maxResidualTrackedBytes {
			inspection.Reason = "more than 1 GiB of tracked files remain; manual inspection is required"
			return errStopResidualInspection
		}
		actualObject, err := hashResidualGitBlob(ctx, path, info, objectHash)
		if err != nil {
			return err
		}
		if actualObject != treeEntry.Object {
			inspection.Reason = fmt.Sprintf("file %s differs from commit %s", rel, shortResidualCommit(commit))
			return errStopResidualInspection
		}
		return nil
	})
	if errors.Is(walkErr, errStopResidualInspection) {
		return inspection, nil
	}
	if walkErr != nil {
		return residualWorktreeInspection{}, walkErr
	}
	inspection.Safe = true
	return inspection, nil
}

func readResidualGitTree(ctx context.Context, rootPath, commit string) (map[string]residualGitTreeEntry, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", rootPath, "ls-tree", "-r", "-z", "--full-tree", commit)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("read Git tree %s in %s: %w", shortResidualCommit(commit), rootPath, err)
	}
	entries := make(map[string]residualGitTreeEntry)
	for _, record := range strings.Split(string(out), "\x00") {
		if record == "" {
			continue
		}
		header, path, ok := strings.Cut(record, "\t")
		if !ok || path == "" {
			return nil, fmt.Errorf("parse Git tree %s: malformed entry", shortResidualCommit(commit))
		}
		fields := strings.Fields(header)
		if len(fields) != 3 {
			return nil, fmt.Errorf("parse Git tree %s: malformed metadata for %s", shortResidualCommit(commit), path)
		}
		entries[path] = residualGitTreeEntry{
			Mode:   fields[0],
			Type:   fields[1],
			Object: fields[2],
		}
	}
	return entries, nil
}

func residualObjectHash(commit string) (func() hash.Hash, error) {
	switch len(strings.TrimSpace(commit)) {
	case sha1.Size * 2:
		return sha1.New, nil
	case sha256.Size * 2:
		return sha256.New, nil
	default:
		return nil, fmt.Errorf("unsupported Git object hash %q", commit)
	}
}

func hashResidualGitBlob(ctx context.Context, path string, expectedInfo os.FileInfo, newHash func() hash.Hash) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil {
		return "", err
	}
	if !openedInfo.Mode().IsRegular() || !os.SameFile(expectedInfo, openedInfo) {
		return "", fmt.Errorf("%s changed during residual worktree inspection", path)
	}

	h := newHash()
	if _, err := io.WriteString(h, "blob "+strconv.FormatInt(openedInfo.Size(), 10)+"\x00"); err != nil {
		return "", err
	}
	if _, err := io.Copy(h, contextReader{Context: ctx, Reader: file}); err != nil {
		return "", err
	}
	afterInfo, err := file.Stat()
	if err != nil {
		return "", err
	}
	if !sameResidualFileSnapshot(openedInfo, afterInfo) {
		return "", fmt.Errorf("%s changed during residual worktree inspection", path)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func removeInspectedResidualWorktreeDirectory(ctx context.Context, inspection residualWorktreeInspection, projectPath string) error {
	if !inspection.Safe {
		return fmt.Errorf("residual worktree cleanup is not safe: %s", strings.TrimSpace(inspection.Reason))
	}
	if inspection.Kind == ResidualWorktreeCleanupDSStoreOnly {
		return removeDSStoreOnlyDirectory(projectPath)
	}
	if inspection.Kind != ResidualWorktreeCleanupPartialGitDir {
		return fmt.Errorf("residual worktree cleanup kind is unavailable")
	}

	entries := append([]residualWorktreeEntry(nil), inspection.Entries...)
	sort.Slice(entries, func(i, j int) bool {
		depthI := residualPathDepth(entries[i].Path)
		depthJ := residualPathDepth(entries[j].Path)
		if depthI != depthJ {
			return depthI > depthJ
		}
		if entries[i].Info.IsDir() != entries[j].Info.IsDir() {
			return !entries[i].Info.IsDir()
		}
		return entries[i].Path > entries[j].Path
	})
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		currentInfo, err := os.Lstat(entry.Path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		if !sameResidualFileSnapshot(entry.Info, currentInfo) {
			return fmt.Errorf("residual worktree entry changed before deletion: %s", entry.Path)
		}
		if err := os.Remove(entry.Path); err != nil {
			return fmt.Errorf("remove verified residual worktree entry %s: %w", entry.Path, err)
		}
	}
	if _, err := os.Lstat(projectPath); err == nil {
		return fmt.Errorf("verified residual worktree path still exists after cleanup: %s", projectPath)
	} else if !os.IsNotExist(err) {
		return err
	}
	return nil
}

func sameResidualFileSnapshot(expected, actual os.FileInfo) bool {
	if expected == nil || actual == nil || !os.SameFile(expected, actual) {
		return false
	}
	if expected.Mode().Type() != actual.Mode().Type() || expected.Mode().Perm() != actual.Mode().Perm() {
		return false
	}
	if expected.IsDir() {
		return true
	}
	return expected.Size() == actual.Size() && expected.ModTime() == actual.ModTime()
}

func residualPathDepth(path string) int {
	return strings.Count(filepath.Clean(path), string(filepath.Separator))
}

func shortResidualCommit(commit string) string {
	commit = strings.TrimSpace(commit)
	if len(commit) > 12 {
		return commit[:12]
	}
	return commit
}

type contextReader struct {
	Context context.Context
	Reader  io.Reader
}

func (r contextReader) Read(buffer []byte) (int, error) {
	if err := r.Context.Err(); err != nil {
		return 0, err
	}
	return r.Reader.Read(buffer)
}
