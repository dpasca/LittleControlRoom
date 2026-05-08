package policy

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type Autonomy string

const (
	AutonomyOff    Autonomy = "off"
	AutonomyLow    Autonomy = "low"
	AutonomyMedium Autonomy = "medium"
)

func ParseAutonomy(value string) (Autonomy, error) {
	switch Autonomy(strings.ToLower(strings.TrimSpace(value))) {
	case "", AutonomyOff:
		return AutonomyOff, nil
	case AutonomyLow:
		return AutonomyLow, nil
	case AutonomyMedium:
		return AutonomyMedium, nil
	default:
		return "", fmt.Errorf("unsupported autonomy level: %s", value)
	}
}

type Workspace struct {
	Root string
	Auto Autonomy
}

func NewWorkspace(root string, auto Autonomy) (Workspace, error) {
	if strings.TrimSpace(root) == "" {
		return Workspace{}, fmt.Errorf("--cwd is required")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return Workspace{}, err
	}
	canon, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return Workspace{}, err
	}
	info, err := os.Stat(canon)
	if err != nil {
		return Workspace{}, err
	}
	if !info.IsDir() {
		return Workspace{}, fmt.Errorf("workspace is not a directory: %s", root)
	}
	return Workspace{Root: filepath.Clean(canon), Auto: auto}, nil
}

func (w Workspace) Resolve(rel string) (string, error) {
	rel = strings.TrimSpace(rel)
	if rel == "" {
		return "", fmt.Errorf("path is required")
	}
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("absolute paths are not allowed: %s", rel)
	}
	clean := filepath.Clean(rel)
	if clean == "." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) || clean == ".." {
		return "", fmt.Errorf("path escapes workspace: %s", rel)
	}
	target := filepath.Join(w.Root, clean)
	parent := existingParent(target)
	canonParent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return "", err
	}
	if !isUnder(w.Root, canonParent) {
		return "", fmt.Errorf("path escapes workspace through symlink: %s", rel)
	}
	if canonTarget, err := filepath.EvalSymlinks(target); err == nil && !isUnder(w.Root, canonTarget) {
		return "", fmt.Errorf("path escapes workspace through symlink: %s", rel)
	}
	return target, nil
}

func existingParent(path string) string {
	current := path
	for {
		info, err := os.Lstat(current)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				return current
			}
			if info.IsDir() {
				return current
			}
			return filepath.Dir(current)
		}
		next := filepath.Dir(current)
		if next == current {
			return current
		}
		current = next
	}
}

func (w Workspace) AllowPatch() error {
	if w.Auto == AutonomyOff {
		return fmt.Errorf("apply_patch denied with --auto off")
	}
	return nil
}

func (w Workspace) AllowCommand(command string) error {
	if strings.TrimSpace(command) == "" {
		return fmt.Errorf("command is required")
	}
	if w.Auto != AutonomyOff {
		return nil
	}
	if readOnlyShellCommand(command) {
		return nil
	}
	return fmt.Errorf("command denied with --auto off: only explicit read-only command forms are allowed")
}

func ClampTimeout(requested time.Duration, fallback time.Duration, max time.Duration) time.Duration {
	if requested <= 0 {
		return fallback
	}
	if requested > max {
		return max
	}
	return requested
}

func isUnder(root, path string) bool {
	root = filepath.Clean(root)
	path = filepath.Clean(path)
	if runtime.GOOS == "windows" {
		root = strings.ToLower(root)
		path = strings.ToLower(path)
	}
	if path == root {
		return true
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

func readOnlyShellCommand(command string) bool {
	if strings.ContainsAny(command, ">|;&") || strings.Contains(command, "$(") || strings.Contains(command, "`") {
		return false
	}
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return false
	}
	switch fields[0] {
	case "pwd", "ls", "find", "rg", "grep", "sed", "cat", "head", "tail", "wc", "jq":
		return true
	case "git":
		if len(fields) < 2 {
			return false
		}
		switch fields[1] {
		case "status", "diff", "show", "log", "branch", "rev-parse":
			return true
		default:
			return false
		}
	default:
		return false
	}
}
