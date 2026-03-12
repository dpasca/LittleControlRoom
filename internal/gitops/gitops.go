package gitops

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

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
	cmd := exec.CommandContext(ctx, "git", "-C", path, "push")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("push %s: %w: %s", path, err, strings.TrimSpace(string(out)))
	}
	return nil
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
	if data, readErr := os.ReadFile(indexPath); readErr == nil {
		if writeErr := os.WriteFile(tempIndex, data, 0o600); writeErr != nil {
			return nil, fmt.Errorf("seed temp index for %s: %w", path, writeErr)
		}
	} else if !os.IsNotExist(readErr) {
		return nil, fmt.Errorf("read git index for %s: %w", path, readErr)
	} else if writeErr := os.WriteFile(tempIndex, nil, 0o600); writeErr != nil {
		return nil, fmt.Errorf("create empty temp index for %s: %w", path, writeErr)
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
	if data, readErr := os.ReadFile(indexPath); readErr == nil {
		if writeErr := os.WriteFile(tempIndex, data, 0o600); writeErr != nil {
			return nil, fmt.Errorf("seed temp index for %s: %w", path, writeErr)
		}
	} else if !os.IsNotExist(readErr) {
		return nil, fmt.Errorf("read git index for %s: %w", path, readErr)
	} else if writeErr := os.WriteFile(tempIndex, nil, 0o600); writeErr != nil {
		return nil, fmt.Errorf("create empty temp index for %s: %w", path, writeErr)
	}
	return readCachedDiffWithTempIndexFromSeed(ctx, path, tempIndex, addArgs, diffArgs...)
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
