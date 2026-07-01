package gitops

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Push may run user-defined hooks, including release builds.
var defaultPushTimeout = 5 * time.Minute
var defaultPullTimeout = 90 * time.Second

func ReadDiffStat(ctx context.Context, path string, cached bool) (string, error) {
	args := []string{"-C", path, "diff"}
	if cached {
		args = append(args, "--cached")
	}
	args = append(args, "--stat", "--find-renames", "--", ".")
	out, err := exec.CommandContext(ctx, "git", args...).Output()
	if err != nil {
		return "", fmt.Errorf("read git diff stat for %s: %w", path, err)
	}
	return strings.TrimSpace(string(out)), nil
}

func ReadDiffPatch(ctx context.Context, path string, cached bool, maxBytes int) (string, error) {
	args := []string{"-C", path, "diff"}
	if cached {
		args = append(args, "--cached")
	}
	args = append(args, "--unified=0", "--no-color", "--find-renames", "--", ".")
	out, err := exec.CommandContext(ctx, "git", args...).Output()
	if err != nil {
		return "", fmt.Errorf("read git diff patch for %s: %w", path, err)
	}
	trimmed := strings.TrimSpace(string(out))
	if maxBytes > 0 && len(trimmed) > maxBytes {
		return "", nil
	}
	return trimmed, nil
}

func ReadDiffStatAllStaged(ctx context.Context, path string) (string, error) {
	out, err := readCachedDiffWithTempIndex(ctx, path, []string{"add", "--all", "--", "."}, "--stat", "--find-renames", "--", ".")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func ReadDiffPatchAllStaged(ctx context.Context, path string, maxBytes int) (string, error) {
	out, err := readCachedDiffWithTempIndex(ctx, path, []string{"add", "--all", "--", "."}, "--unified=0", "--no-color", "--find-renames", "--", ".")
	if err != nil {
		return "", err
	}
	trimmed := strings.TrimSpace(string(out))
	if maxBytes > 0 && len(trimmed) > maxBytes {
		return "", nil
	}
	return trimmed, nil
}

func ReadDiffStatWithAddedPaths(ctx context.Context, path string, extraPaths []string) (string, error) {
	out, err := readCachedDiffWithAddedPaths(ctx, path, extraPaths, "--stat", "--find-renames", "--", ".")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func ReadDiffPatchWithAddedPaths(ctx context.Context, path string, extraPaths []string, maxBytes int) (string, error) {
	out, err := readCachedDiffWithAddedPaths(ctx, path, extraPaths, "--unified=0", "--no-color", "--find-renames", "--", ".")
	if err != nil {
		return "", err
	}
	trimmed := strings.TrimSpace(string(out))
	if maxBytes > 0 && len(trimmed) > maxBytes {
		return "", nil
	}
	return trimmed, nil
}

func ReadDiffPatchPerFile(ctx context.Context, path string, cached bool, paths []string, maxBytes int) (map[string]string, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	if maxBytes <= 0 {
		return nil, nil
	}
	perFileBudget := maxBytes / len(paths)
	results := make(map[string]string, len(paths))
	for _, relPath := range paths {
		patch, err := readDiffPatchForSinglePath(ctx, path, cached, relPath, perFileBudget)
		if err != nil {
			return nil, err
		}
		results[relPath] = patch
	}
	return results, nil
}

func readDiffPatchForSinglePath(ctx context.Context, repoPath string, cached bool, relPath string, maxBytes int) (string, error) {
	args := []string{"-C", repoPath, "diff"}
	if cached {
		args = append(args, "--cached")
	}
	args = append(args, "--unified=0", "--no-color", "--find-renames", "--", relPath)
	out, err := exec.CommandContext(ctx, "git", args...).Output()
	if err != nil {
		return "", fmt.Errorf("read git diff patch for %s: %w", relPath, err)
	}
	trimmed := strings.TrimSpace(string(out))
	if maxBytes > 0 && len(trimmed) > maxBytes {
		trimmed = trimmed[:maxBytes]
	}
	return trimmed, nil
}

func MergeDiffPatches(patches map[string]string) string {
	if len(patches) == 0 {
		return ""
	}
	var builder strings.Builder
	for _, patch := range patches {
		if patch != "" {
			builder.WriteString(patch)
			builder.WriteString("\n")
		}
	}
	return strings.TrimSpace(builder.String())
}

func StageAll(ctx context.Context, path string) error {
	cmd := exec.CommandContext(ctx, "git", "-C", path, "add", "--all", "--", ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("stage changes for %s: %w: %s", path, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func StagePaths(ctx context.Context, path string, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	args := append([]string{"-C", path, "add", "-A", "--"}, paths...)
	cmd := exec.CommandContext(ctx, "git", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("stage selected paths for %s: %w: %s", path, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func UnstagePaths(ctx context.Context, path string, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	args := append([]string{"-C", path, "restore", "--staged", "--"}, paths...)
	cmd := exec.CommandContext(ctx, "git", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("unstage selected paths for %s: %w: %s", path, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func Commit(ctx context.Context, path, message string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", path, "commit", "-m", message)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("commit changes for %s: %w: %s", path, err, strings.TrimSpace(string(out)))
	}
	head, err := exec.CommandContext(ctx, "git", "-C", path, "rev-parse", "--short", "HEAD").Output()
	if err != nil {
		return "", fmt.Errorf("read commit hash for %s: %w", path, err)
	}
	return strings.TrimSpace(string(head)), nil
}

func Push(ctx context.Context, path string) error {
	pushCtx, cancel, appliedTimeout := withDefaultTimeout(ctx, defaultPushTimeout)
	defer cancel()

	cmd := exec.CommandContext(pushCtx, "git", "-C", path, "push")
	if out, err := cmd.CombinedOutput(); err != nil {
		if timeoutErr := commandTimeoutError("push", path, pushCtx, appliedTimeout, out); timeoutErr != nil {
			return timeoutErr
		}
		return fmt.Errorf("push %s: %w: %s", path, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func PushSetUpstream(ctx context.Context, path, remote string) error {
	remote = strings.TrimSpace(remote)
	if remote == "" {
		remote = "origin"
	}
	pushCtx, cancel, appliedTimeout := withDefaultTimeout(ctx, defaultPushTimeout)
	defer cancel()

	cmd := exec.CommandContext(pushCtx, "git", "-C", path, "push", "-u", remote, "HEAD")
	if out, err := cmd.CombinedOutput(); err != nil {
		if timeoutErr := commandTimeoutError("push", path, pushCtx, appliedTimeout, out); timeoutErr != nil {
			return timeoutErr
		}
		return fmt.Errorf("push %s: %w: %s", path, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func IsPushRejectedNeedsPull(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	if !strings.Contains(text, "push ") || !strings.Contains(text, "rejected") {
		return false
	}
	hasRemoteWorkHint := strings.Contains(text, "failed to push some refs") &&
		strings.Contains(text, "remote contains work that you do not")
	return strings.Contains(text, "fetch first") ||
		hasRemoteWorkHint ||
		strings.Contains(text, "non-fast-forward")
}

func Pull(ctx context.Context, path string) error {
	pullCtx, cancel, appliedTimeout := withDefaultTimeout(ctx, defaultPullTimeout)
	defer cancel()

	cmd := exec.CommandContext(pullCtx, "git", "-C", path, "pull")
	if out, err := cmd.CombinedOutput(); err != nil {
		if timeoutErr := commandTimeoutError("pull", path, pullCtx, appliedTimeout, out); timeoutErr != nil {
			return timeoutErr
		}
		return fmt.Errorf("pull %s: %w: %s", path, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func commandTimeoutError(operation, path string, ctx context.Context, appliedTimeout time.Duration, out []byte) error {
	if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return nil
	}

	trimmed := strings.TrimSpace(string(out))
	timeoutText := ""
	if appliedTimeout > 0 {
		timeoutText = " after " + appliedTimeout.Round(time.Millisecond).String()
	}
	if trimmed != "" {
		return fmt.Errorf("%s %s timed out%s: %w: %s", operation, path, timeoutText, context.DeadlineExceeded, trimmed)
	}
	return fmt.Errorf("%s %s timed out%s: %w", operation, path, timeoutText, context.DeadlineExceeded)
}

func withDefaultTimeout(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc, time.Duration) {
	if parent == nil {
		parent = context.Background()
	}
	if timeout <= 0 {
		ctx, cancel := context.WithCancel(parent)
		return ctx, cancel, 0
	}
	if _, ok := parent.Deadline(); ok {
		ctx, cancel := context.WithCancel(parent)
		return ctx, cancel, 0
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	return ctx, cancel, timeout
}

func readCachedDiffWithAddedPaths(ctx context.Context, path string, extraPaths []string, diffArgs ...string) ([]byte, error) {
	if len(extraPaths) == 0 {
		args := []string{"-C", path, "diff", "--cached"}
		args = append(args, diffArgs...)
		out, err := exec.CommandContext(ctx, "git", args...).Output()
		if err != nil {
			return nil, fmt.Errorf("read git cached diff for %s: %w", path, err)
		}
		return out, nil
	}

	indexPath, err := gitIndexPath(ctx, path)
	if err != nil {
		return nil, err
	}
	tempDir, err := os.MkdirTemp("", "lcroom-index-*")
	if err != nil {
		return nil, fmt.Errorf("create temp index dir for %s: %w", path, err)
	}
	defer os.RemoveAll(tempDir)

	tempIndex := filepath.Join(tempDir, "index")
	if err := seedTempIndex(path, indexPath, tempIndex); err != nil {
		return nil, err
	}

	addArgs := append([]string{"add", "--"}, extraPaths...)
	return readCachedDiffWithTempIndexFromSeed(ctx, path, tempIndex, addArgs, diffArgs...)
}

func gitIndexPath(ctx context.Context, path string) (string, error) {
	out, err := exec.CommandContext(ctx, "git", "-C", path, "rev-parse", "--git-path", "index").Output()
	if err != nil {
		return "", fmt.Errorf("resolve git index path for %s: %w", path, err)
	}
	indexPath := strings.TrimSpace(string(out))
	if indexPath == "" {
		return "", fmt.Errorf("resolve git index path for %s: empty path", path)
	}
	if filepath.IsAbs(indexPath) {
		return indexPath, nil
	}
	return filepath.Join(path, indexPath), nil
}

func readCachedDiffWithTempIndex(ctx context.Context, path string, addArgs []string, diffArgs ...string) ([]byte, error) {
	indexPath, err := gitIndexPath(ctx, path)
	if err != nil {
		return nil, err
	}
	tempDir, err := os.MkdirTemp("", "lcroom-index-*")
	if err != nil {
		return nil, fmt.Errorf("create temp index dir for %s: %w", path, err)
	}
	defer os.RemoveAll(tempDir)

	tempIndex := filepath.Join(tempDir, "index")
	if err := seedTempIndex(path, indexPath, tempIndex); err != nil {
		return nil, err
	}
	return readCachedDiffWithTempIndexFromSeed(ctx, path, tempIndex, addArgs, diffArgs...)
}

func seedTempIndex(repoPath, indexPath, tempIndex string) error {
	data, err := os.ReadFile(indexPath)
	if err == nil {
		if len(data) == 0 {
			return nil
		}
		if writeErr := os.WriteFile(tempIndex, data, 0o600); writeErr != nil {
			return fmt.Errorf("seed temp index for %s: %w", repoPath, writeErr)
		}
		return nil
	}
	if os.IsNotExist(err) {
		return nil
	}
	return fmt.Errorf("read git index for %s: %w", repoPath, err)
}

func readCachedDiffWithTempIndexFromSeed(ctx context.Context, path, tempIndex string, addArgs []string, diffArgs ...string) ([]byte, error) {
	env := append(os.Environ(), "GIT_INDEX_FILE="+tempIndex)
	addCmd := exec.CommandContext(ctx, "git", append([]string{"-C", path}, addArgs...)...)
	addCmd.Env = env
	if out, err := addCmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("stage temp index changes for %s: %w: %s", path, err, strings.TrimSpace(string(out)))
	}

	args := []string{"-C", path, "diff", "--cached"}
	args = append(args, diffArgs...)
	diffCmd := exec.CommandContext(ctx, "git", args...)
	diffCmd.Env = env
	out, err := diffCmd.Output()
	if err != nil {
		return nil, fmt.Errorf("read git cached diff with temp index for %s: %w", path, err)
	}
	return out, nil
}
