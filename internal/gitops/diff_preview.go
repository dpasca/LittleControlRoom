package gitops

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func ReadDiffPatchForPaths(ctx context.Context, path string, cached bool, paths []string, maxBytes int) (string, bool, error) {
	if len(paths) == 0 {
		return "", false, nil
	}
	args := []string{"-C", path, "diff"}
	if cached {
		args = append(args, "--cached")
	}
	args = append(args, "--unified=3", "--no-color", "--find-renames", "--")
	args = append(args, paths...)
	out, err := exec.CommandContext(ctx, "git", args...).Output()
	if err != nil {
		return "", false, fmt.Errorf("read git diff patch for %s: %w", path, err)
	}
	return trimPreviewOutput(out, maxBytes), maxBytes > 0 && len(out) > maxBytes, nil
}

func ReadGitBlob(ctx context.Context, repoPath, blobPath string, maxBytes int) ([]byte, bool, error) {
	spec := "HEAD:" + strings.TrimSpace(blobPath)
	out, err := exec.CommandContext(ctx, "git", "-C", repoPath, "show", spec).Output()
	if err != nil {
		return nil, false, fmt.Errorf("read git blob %s for %s: %w", blobPath, repoPath, err)
	}
	if maxBytes > 0 && len(out) > maxBytes {
		return out[:maxBytes], true, nil
	}
	return out, false, nil
}

func ReadWorkingTreeFile(repoPath, relPath string, maxBytes int) ([]byte, bool, error) {
	fullPath := filepath.Join(repoPath, filepath.FromSlash(relPath))
	data, err := os.ReadFile(fullPath)
	if err != nil {
		return nil, false, fmt.Errorf("read worktree file %s for %s: %w", relPath, repoPath, err)
	}
	if maxBytes > 0 && len(data) > maxBytes {
		return data[:maxBytes], true, nil
	}
	return data, false, nil
}

func trimPreviewOutput(out []byte, maxBytes int) string {
	if maxBytes > 0 && len(out) > maxBytes {
		out = out[:maxBytes]
	}
	return strings.TrimSpace(string(out))
}
