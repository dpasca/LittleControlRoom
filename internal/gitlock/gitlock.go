package gitlock

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

type IndexLockError struct {
	LockPath string
}

func (e IndexLockError) Error() string {
	lockPath := filepath.Clean(strings.TrimSpace(e.LockPath))
	if lockPath == "" || lockPath == "." {
		return "git index.lock already exists; close any other Git process or remove the stale lock if the repo is idle"
	}
	return fmt.Sprintf("git index.lock already exists at %s; close any other Git process or remove the stale lock if the repo is idle", lockPath)
}

func CheckIndexLock(ctx context.Context, repoPath string) error {
	lockPath, exists, err := ExistingIndexLock(ctx, repoPath)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	return IndexLockError{LockPath: lockPath}
}

func CheckIndexAndModuleLocks(ctx context.Context, repoPath string) error {
	if err := CheckIndexLock(ctx, repoPath); err != nil {
		return err
	}
	locks, err := ExistingModuleIndexLocks(ctx, repoPath)
	if err != nil {
		return err
	}
	if len(locks) == 0 {
		return nil
	}
	return IndexLockError{LockPath: locks[0]}
}

func ExistingIndexLock(ctx context.Context, repoPath string) (string, bool, error) {
	lockPath, err := IndexLockPath(ctx, repoPath)
	if err != nil {
		return "", false, err
	}
	info, err := os.Stat(lockPath)
	if err != nil {
		if os.IsNotExist(err) {
			return lockPath, false, nil
		}
		return lockPath, false, fmt.Errorf("stat git index lock %s: %w", lockPath, err)
	}
	if info.IsDir() {
		return lockPath, false, fmt.Errorf("git index lock path is a directory: %s", lockPath)
	}
	return lockPath, true, nil
}

func ExistingModuleIndexLocks(ctx context.Context, repoPath string) ([]string, error) {
	commonDir, err := GitCommonDir(ctx, repoPath)
	if err != nil {
		return nil, err
	}
	modulesDir := filepath.Join(commonDir, "modules")
	if _, err := os.Stat(modulesDir); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("stat git modules dir %s: %w", modulesDir, err)
	}
	var locks []string
	err = filepath.WalkDir(modulesDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Base(path) != "index.lock" {
			return nil
		}
		locks = append(locks, filepath.Clean(path))
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan git module index locks in %s: %w", modulesDir, err)
	}
	sort.Strings(locks)
	return locks, nil
}

func IndexLockPath(ctx context.Context, repoPath string) (string, error) {
	return gitPath(ctx, repoPath, "index.lock")
}

func GitCommonDir(ctx context.Context, repoPath string) (string, error) {
	return gitPath(ctx, repoPath, "--git-common-dir")
}

func LockPathFromOutput(output string) (string, bool) {
	output = strings.ReplaceAll(output, "\r\n", "\n")
	for _, rawLine := range strings.Split(output, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || !strings.Contains(line, "index.lock") {
			continue
		}
		for _, marker := range []string{"Unable to create '", "Unable to create \""} {
			if _, remainder, ok := strings.Cut(line, marker); ok {
				quote := "'"
				if strings.HasSuffix(marker, "\"") {
					quote = "\""
				}
				if lockPath, _, ok := strings.Cut(remainder, quote); ok {
					lockPath = filepath.Clean(strings.TrimSpace(lockPath))
					if lockPath != "" && lockPath != "." {
						return lockPath, true
					}
				}
			}
		}
	}
	return "", false
}

func gitPath(ctx context.Context, repoPath, name string) (string, error) {
	repoPath = filepath.Clean(strings.TrimSpace(repoPath))
	name = strings.TrimSpace(name)
	if repoPath == "" || repoPath == "." || name == "" {
		return "", fmt.Errorf("repo path and git path name are required")
	}
	var args []string
	if strings.HasPrefix(name, "--") {
		args = []string{"-C", repoPath, "rev-parse", name}
	} else {
		args = []string{"-C", repoPath, "rev-parse", "--git-path", name}
	}
	cmd := exec.CommandContext(ctx, "git", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("resolve git path %s in %s: %w: %s", name, repoPath, err, strings.TrimSpace(string(out)))
	}
	resolved := filepath.Clean(strings.TrimSpace(string(out)))
	if resolved == "" || resolved == "." {
		return "", fmt.Errorf("resolve git path %s in %s: empty path", name, repoPath)
	}
	if filepath.IsAbs(resolved) {
		return resolved, nil
	}
	return filepath.Clean(filepath.Join(repoPath, resolved)), nil
}
