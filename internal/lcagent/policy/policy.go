package policy

import (
	"errors"
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

type DenialError struct {
	Reason string
}

func (e DenialError) Error() string {
	return strings.TrimSpace(e.Reason)
}

func Denied(reason string) error {
	return DenialError{Reason: strings.TrimSpace(reason)}
}

func IsDenied(err error) bool {
	var denial DenialError
	return errors.As(err, &denial)
}

func DenialReason(err error) string {
	var denial DenialError
	if errors.As(err, &denial) {
		return strings.TrimSpace(denial.Reason)
	}
	if err == nil {
		return ""
	}
	return strings.TrimSpace(err.Error())
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
		return "", Denied(fmt.Sprintf("absolute paths are not allowed: %s", rel))
	}
	clean := filepath.Clean(rel)
	if clean == "." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) || clean == ".." {
		return "", Denied(fmt.Sprintf("path escapes workspace: %s", rel))
	}
	target := filepath.Join(w.Root, clean)
	parent := existingParent(target)
	canonParent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return "", err
	}
	if !isUnder(w.Root, canonParent) {
		return "", Denied(fmt.Sprintf("path escapes workspace through symlink: %s", rel))
	}
	if canonTarget, err := filepath.EvalSymlinks(target); err == nil && !isUnder(w.Root, canonTarget) {
		return "", Denied(fmt.Sprintf("path escapes workspace through symlink: %s", rel))
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
		return Denied("apply_patch denied with --auto off")
	}
	return nil
}

func (w Workspace) AllowCommand(command string) error {
	return w.AllowCommandSpec(nil, command, true)
}

func (w Workspace) AllowCommandSpec(argv []string, command string, shell bool) error {
	argv = cleanArgv(argv)
	command = strings.TrimSpace(command)
	if len(argv) == 0 && command == "" {
		return fmt.Errorf("command is required")
	}
	if w.Auto == AutonomyMedium {
		return nil
	}
	if len(argv) > 0 {
		if readOnlyArgvCommand(argv) {
			return nil
		}
		if w.Auto == AutonomyLow && lowAutonomyVerificationArgvCommand(argv) {
			return nil
		}
		return Denied(fmt.Sprintf("command denied with --auto %s: only explicit read-only or approved verification argv forms are allowed below medium autonomy", w.Auto))
	}
	if strings.TrimSpace(command) == "" {
		return fmt.Errorf("command is required")
	}
	if readOnlyShellCommand(command) {
		return nil
	}
	if shell {
		return Denied(fmt.Sprintf("shell command denied with --auto %s: only explicit read-only command forms are allowed below medium autonomy", w.Auto))
	}
	return Denied(fmt.Sprintf("command denied with --auto %s: only explicit read-only or approved verification argv forms are allowed below medium autonomy", w.Auto))
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

func lowAutonomyVerificationArgvCommand(argv []string) bool {
	argv = cleanArgv(argv)
	if len(argv) == 0 {
		return false
	}
	switch filepath.Base(argv[0]) {
	case "go":
		return lowAutonomyGoTestArgs(argv[1:])
	default:
		return false
	}
}

func lowAutonomyGoTestArgs(args []string) bool {
	if len(args) == 0 || args[0] != "test" {
		return false
	}
	expectValue := false
	for _, arg := range args[1:] {
		if arg == "" {
			return false
		}
		if expectValue {
			if strings.HasPrefix(arg, "-") {
				return false
			}
			expectValue = false
			continue
		}
		if strings.HasPrefix(arg, "-") {
			switch {
			case goTestFlagTakesSeparateValue(arg):
				expectValue = true
			case goTestFlagAllowed(arg):
				continue
			default:
				return false
			}
			continue
		}
		if !goTestPackageArgAllowed(arg) {
			return false
		}
	}
	return !expectValue
}

func goTestFlagTakesSeparateValue(arg string) bool {
	switch arg {
	case "-run", "-count", "-timeout", "-parallel", "-vet", "-tags", "-covermode", "-coverpkg", "-list":
		return true
	default:
		return false
	}
}

func goTestFlagAllowed(arg string) bool {
	switch arg {
	case "-v", "-json", "-short", "-race", "-failfast", "-cover":
		return true
	case "-args", "-c", "-bench", "-fuzz", "-exec", "-toolexec", "-coverprofile", "-outputdir":
		return false
	}
	for _, prefix := range []string{
		"-run=",
		"-count=",
		"-timeout=",
		"-parallel=",
		"-vet=",
		"-tags=",
		"-covermode=",
		"-coverpkg=",
		"-list=",
	} {
		if strings.HasPrefix(arg, prefix) && strings.TrimSpace(strings.TrimPrefix(arg, prefix)) != "" {
			return true
		}
	}
	for _, prefix := range []string{
		"-args=",
		"-c=",
		"-bench=",
		"-fuzz=",
		"-exec=",
		"-toolexec=",
		"-coverprofile=",
		"-outputdir=",
	} {
		if strings.HasPrefix(arg, prefix) {
			return false
		}
	}
	return false
}

func goTestPackageArgAllowed(arg string) bool {
	arg = filepath.ToSlash(strings.TrimSpace(arg))
	if arg == "." || arg == "./..." {
		return true
	}
	if !strings.HasPrefix(arg, "./") {
		return false
	}
	for _, part := range strings.Split(strings.TrimPrefix(arg, "./"), "/") {
		switch part {
		case "", "..":
			return false
		case "...":
			continue
		}
	}
	return true
}

func readOnlyShellCommand(command string) bool {
	if strings.ContainsAny(command, ">|;&") || strings.Contains(command, "$(") || strings.Contains(command, "`") {
		return false
	}
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return false
	}
	return readOnlyCommandFields(fields)
}

func readOnlyArgvCommand(argv []string) bool {
	argv = cleanArgv(argv)
	if len(argv) == 0 {
		return false
	}
	return readOnlyCommandFields(argv)
}

func readOnlyCommandFields(fields []string) bool {
	if len(fields) == 0 {
		return false
	}
	name := filepath.Base(fields[0])
	switch name {
	case "pwd", "ls", "rg", "grep", "cat", "head", "tail", "wc", "jq":
		return true
	case "find":
		return readOnlyFindArgs(fields[1:])
	case "sed":
		return readOnlySedArgs(fields[1:])
	case "git":
		return readOnlyGitArgs(fields[1:])
	default:
		return false
	}
}

func readOnlyFindArgs(args []string) bool {
	for _, arg := range args {
		switch {
		case arg == "-delete" || arg == "-exec" || arg == "-execdir" || arg == "-ok" || arg == "-okdir":
			return false
		case arg == "-fls" || arg == "-fprint" || arg == "-fprint0" || arg == "-fprintf":
			return false
		}
	}
	return true
}

func readOnlySedArgs(args []string) bool {
	for _, arg := range args {
		if strings.HasPrefix(arg, "-i") {
			return false
		}
	}
	return true
}

func readOnlyGitArgs(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "status", "show", "log", "rev-parse":
		return true
	case "diff":
		return !hasAnyArgPrefix(args[1:], "--output", "--ext-diff")
	case "branch":
		if len(args) == 1 {
			return true
		}
		for _, arg := range args[1:] {
			switch arg {
			case "--show-current", "--list", "-l", "-a", "-r", "-v", "-vv", "--verbose", "--all", "--remotes", "--merged", "--no-merged", "--contains", "--no-contains", "--points-at", "--format", "--sort", "--color", "--no-color", "--column", "--no-column":
				continue
			default:
				if strings.HasPrefix(arg, "--format=") || strings.HasPrefix(arg, "--sort=") || strings.HasPrefix(arg, "--color=") || strings.HasPrefix(arg, "--column=") {
					continue
				}
				return false
			}
		}
		return true
	default:
		return false
	}
}

func hasAnyArgPrefix(args []string, prefixes ...string) bool {
	for _, arg := range args {
		for _, prefix := range prefixes {
			if arg == prefix || strings.HasPrefix(arg, prefix+"=") {
				return true
			}
		}
	}
	return false
}

func cleanArgv(argv []string) []string {
	out := make([]string, 0, len(argv))
	for _, value := range argv {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}
