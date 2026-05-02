package appfs

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"lcroom/internal/brand"
)

const internalWorkspaceDirName = "internal-workspaces"

var persistentWorkspacePrefixes = []string{
	"lcroom-agent-task-",
}

var reservedWorkspacePrefixes = []string{
	"lcroom-agent-task-",
	"lcroom-codex-helper-",
	"lcroom-ai-",
	"lcroom-index-",
	"lcroom-screenshots-",
}

func DefaultDataDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Clean(brand.DataDirName)
	}
	return filepath.Join(home, brand.DataDirName)
}

func InternalWorkspaceRoot(dataDir string) string {
	dataDir = strings.TrimSpace(dataDir)
	if dataDir == "" {
		dataDir = DefaultDataDir()
	}
	return filepath.Join(filepath.Clean(dataDir), internalWorkspaceDirName)
}

func EnsureInternalWorkspaceRoot(dataDir string) (string, error) {
	root := InternalWorkspaceRoot(dataDir)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", err
	}
	return root, nil
}

func CreateInternalWorkspace(dataDir, prefix string) (string, error) {
	root, err := EnsureInternalWorkspaceRoot(dataDir)
	if err != nil {
		return "", err
	}
	return os.MkdirTemp(root, strings.TrimSpace(prefix))
}

func IsManagedInternalPath(path string, managedRoots []string) bool {
	cleanPath := filepath.Clean(strings.TrimSpace(path))
	if cleanPath == "" || cleanPath == "." {
		return false
	}
	for _, root := range managedRoots {
		if isWithinRoot(cleanPath, root) {
			return true
		}
	}
	base := filepath.Base(cleanPath)
	for _, prefix := range reservedWorkspacePrefixes {
		if strings.HasPrefix(base, prefix) {
			return true
		}
	}
	return false
}

func CleanupStaleInternalWorkspaces(dataDir string, maxAge time.Duration) error {
	if maxAge <= 0 {
		return nil
	}
	root := InternalWorkspaceRoot(dataDir)
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	cutoff := time.Now().Add(-maxAge)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if hasPersistentWorkspacePrefix(entry.Name()) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(cutoff) {
			continue
		}
		_ = os.RemoveAll(filepath.Join(root, entry.Name()))
	}
	return nil
}

func ReservedWorkspacePrefixes() []string {
	out := make([]string, len(reservedWorkspacePrefixes))
	copy(out, reservedWorkspacePrefixes)
	return out
}

func isWithinRoot(path string, root string) bool {
	root = filepath.Clean(strings.TrimSpace(root))
	if root == "" || root == "." {
		return false
	}
	if path == root {
		return true
	}
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func hasPersistentWorkspacePrefix(name string) bool {
	for _, prefix := range persistentWorkspacePrefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}
