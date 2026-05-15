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
		return Denied(commandDenialReason(w.Auto, argv, command, shell))
	}
	if strings.TrimSpace(command) == "" {
		return fmt.Errorf("command is required")
	}
	if readOnlyShellCommand(command) {
		return nil
	}
	if shell {
		return Denied(commandDenialReason(w.Auto, argv, command, shell))
	}
	return Denied(commandDenialReason(w.Auto, argv, command, shell))
}

func commandDenialReason(auto Autonomy, argv []string, command string, shell bool) string {
	base := fmt.Sprintf("command denied with --auto %s: only explicit read-only or approved verification argv forms are allowed below medium autonomy", auto)
	if shell && strings.TrimSpace(command) != "" {
		base = fmt.Sprintf("shell command denied with --auto %s: only explicit read-only shell forms are allowed below medium autonomy", auto)
	}
	if hint := commandDenialHint(argv, command, shell); hint != "" {
		base += ". " + hint
	}
	return base
}

func commandDenialHint(argv []string, command string, shell bool) string {
	argv = cleanArgv(argv)
	if shell && strings.TrimSpace(command) != "" {
		return "Use argv-only run_command for verification, for example argv=[\"go\",\"test\",\"./...\"] with shell=false and purpose=verify."
	}
	if len(argv) == 0 {
		return ""
	}
	if argvEscapesWorkspaceLexically(argv[1:]) {
		return "Use workspace-relative paths inside the project; parent-directory and absolute paths require a higher-autonomy review."
	}
	name := filepath.Base(argv[0])
	args := argv[1:]
	switch name {
	case "go":
		return goCommandDenialHint(args)
	case "gofmt", "goimports":
		return "Format-writing modes are denied at low autonomy; use " + name + " -l or " + name + " -d to inspect formatting differences."
	case "make", "gmake", "just":
		return "Use verification targets such as test, check, lint, typecheck, build, ci, or verify; install/deploy/custom mutation targets require medium autonomy."
	case "npm", "pnpm", "yarn", "bun":
		return packageManagerDenialHint(args)
	case "cargo":
		return cargoCommandDenialHint(args)
	case "python", "python3":
		return "Use python -m pytest or python -m mypy with safe workspace-relative arguments; other Python modules may mutate files or environment state."
	case "pytest":
		return verificationArgsDenialHint(args, "Use one-shot pytest checks with workspace-relative paths and without cache-clear, junit/output, watch, or update flags.")
	case "mypy", "pyright":
		return verificationArgsDenialHint(args, "Use typecheck commands without install-types, createstub, output, watch, or update flags.")
	case "ruff":
		return ruffCommandDenialHint(args)
	case "prettier":
		return prettierCommandDenialHint(args)
	case "eslint":
		return verificationArgsDenialHint(args, "Use eslint check mode without --fix, --output-file, cache/output mutation, or watch flags.")
	case "tsc", "vue-tsc":
		return "Add --noEmit for low-autonomy typechecking; emitting files requires medium autonomy."
	case "biome":
		return biomeCommandDenialHint(args)
	case "git":
		return "Only read-only git commands are allowed below medium autonomy; branch creation, checkout, reset, commit, and mutation commands require explicit user control."
	default:
		return "Use read_file/search/file_outline for inspection, or an approved argv-only test/lint/typecheck/build command with purpose=verify."
	}
}

func goCommandDenialHint(args []string) string {
	if len(args) == 0 {
		return "Use go test/list/vet, or go build ./... without output flags."
	}
	switch args[0] {
	case "fmt":
		return "go fmt mutates files; use gofmt -l or gofmt -d for low-autonomy formatting inspection."
	case "test":
		for _, arg := range args[1:] {
			lower := strings.ToLower(strings.TrimSpace(arg))
			switch {
			case lower == "-exec" || strings.HasPrefix(lower, "-exec=") ||
				lower == "-toolexec" || strings.HasPrefix(lower, "-toolexec="):
				return "go test exec/toolexec hooks are denied; rerun without custom execution hooks."
			case lower == "-coverprofile" || strings.HasPrefix(lower, "-coverprofile=") ||
				lower == "-outputdir" || strings.HasPrefix(lower, "-outputdir="):
				return "go test output-file flags are denied; use -cover without writing a profile, or run the output-producing command at medium autonomy."
			}
		}
		return "Use workspace-relative package patterns such as ./... and safe flags like -run, -count, -timeout, -short, -race, -json, or -cover."
	case "build":
		return "Low autonomy only allows whole-repo verification builds such as go build ./... without -o or custom exec flags."
	case "list", "vet":
		return "Use workspace-relative package patterns and safe flags only; custom tools, output files, and parent paths require higher autonomy."
	default:
		return "Use go test/list/vet, or go build ./... for low-autonomy verification."
	}
}

func packageManagerDenialHint(args []string) string {
	if len(args) == 0 {
		return "Use test/check/lint/typecheck/build scripts, not dependency or script mutation commands."
	}
	command := strings.ToLower(strings.TrimSpace(args[0]))
	switch command {
	case "install", "add", "remove", "update", "upgrade", "publish", "exec", "dlx":
		return "Dependency, publish, and package-execution commands require medium autonomy; use an existing test/check/lint/typecheck/build script instead."
	case "run", "run-script":
		if len(args) < 2 {
			return "Name a verification script such as test, check, lint, typecheck, build, ci, or verify."
		}
		if !verificationTargetAllowed(args[1]) {
			return "Only verification-like scripts are allowed at low autonomy: test, check, lint, typecheck, build, ci, or verify."
		}
	}
	return verificationArgsDenialHint(args, "Use one-shot package-manager verification scripts without --watch, --write, --fix, output, install, or update flags.")
}

func cargoCommandDenialHint(args []string) string {
	if len(args) == 0 {
		return "Use cargo test/check/clippy/build, or cargo fmt --check."
	}
	if args[0] == "fmt" && !hasArg(args[1:], "--check") {
		return "cargo fmt mutates files unless --check is present; retry as cargo fmt --check."
	}
	return verificationArgsDenialHint(args, "Use cargo test/check/clippy/build with safe flags, or cargo fmt --check; publish/install/output/watch modes require medium autonomy.")
}

func ruffCommandDenialHint(args []string) string {
	if len(args) > 0 && args[0] == "format" && !hasArg(args[1:], "--check") {
		return "ruff format mutates files unless --check is present; retry as ruff format --check."
	}
	return verificationArgsDenialHint(args, "Use ruff check or ruff format --check without --fix, --write, output, or watch flags.")
}

func prettierCommandDenialHint(args []string) string {
	if !hasArg(args, "--check") && !hasArg(args, "--list-different") {
		return "Prettier mutates files unless --check or --list-different is used; retry with prettier --check."
	}
	return verificationArgsDenialHint(args, "Use prettier --check or --list-different without --write, output, or watch flags.")
}

func biomeCommandDenialHint(args []string) string {
	if len(args) > 0 && args[0] == "format" && !hasArg(args[1:], "--check") {
		return "biome format mutates files unless --check is present; retry as biome format --check."
	}
	return verificationArgsDenialHint(args, "Use biome check/ci/lint or biome format --check without --write, --fix, output, or watch flags.")
}

func verificationArgsDenialHint(args []string, fallback string) string {
	for _, arg := range args {
		if verificationArgDenied(arg) {
			return fmt.Sprintf("Flag %s is denied at low autonomy because it may write files, update state, install dependencies, watch, or emit reports. %s", arg, fallback)
		}
		if argEscapesWorkspaceLexically(arg) {
			return "Use workspace-relative paths inside the project; parent-directory and absolute paths require a higher-autonomy review."
		}
	}
	return fallback
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
	case "biome":
		return lowAutonomyBiomeArgs(argv[1:])
	case "bun":
		return lowAutonomyBunArgs(argv[1:])
	case "cargo":
		return lowAutonomyCargoArgs(argv[1:])
	case "eslint":
		return lowAutonomyESLintArgs(argv[1:])
	case "go":
		return lowAutonomyGoArgs(argv[1:])
	case "gofmt", "goimports":
		return lowAutonomyGoFormatArgs(argv[1:])
	case "just":
		return lowAutonomyTaskRunnerArgs(argv[1:])
	case "make", "gmake":
		return lowAutonomyMakeArgs(argv[1:])
	case "mypy", "pyright":
		return safeVerificationArgs(argv[1:])
	case "npm":
		return lowAutonomyNPMArgs(argv[1:])
	case "pnpm":
		return lowAutonomyPNPMArgs(argv[1:])
	case "prettier":
		return lowAutonomyPrettierArgs(argv[1:])
	case "pytest":
		return safeVerificationArgs(argv[1:])
	case "python", "python3":
		return lowAutonomyPythonArgs(argv[1:])
	case "ruff":
		return lowAutonomyRuffArgs(argv[1:])
	case "tsc", "vue-tsc":
		return lowAutonomyTypeScriptArgs(argv[1:])
	case "yarn":
		return lowAutonomyYarnArgs(argv[1:])
	default:
		return false
	}
}

func lowAutonomyGoArgs(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "test":
		return lowAutonomyGoTestArgs(args)
	case "list":
		return lowAutonomyGoListArgs(args[1:])
	case "vet":
		return lowAutonomyGoVetArgs(args[1:])
	case "build":
		return lowAutonomyGoBuildArgs(args[1:])
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

func lowAutonomyGoListArgs(args []string) bool {
	expectValue := false
	for _, arg := range args {
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
			case goListFlagTakesSeparateValue(arg):
				expectValue = true
			case goListFlagAllowed(arg):
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

func goListFlagTakesSeparateValue(arg string) bool {
	switch arg {
	case "-f", "-tags":
		return true
	default:
		return false
	}
}

func goListFlagAllowed(arg string) bool {
	switch arg {
	case "-json", "-deps", "-test", "-e":
		return true
	case "-m", "-u":
		return false
	}
	for _, prefix := range []string{"-f=", "-tags="} {
		if strings.HasPrefix(arg, prefix) && strings.TrimSpace(strings.TrimPrefix(arg, prefix)) != "" {
			return true
		}
	}
	return false
}

func lowAutonomyGoVetArgs(args []string) bool {
	expectValue := false
	for _, arg := range args {
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
			case arg == "-tags":
				expectValue = true
			case arg == "-json" || strings.HasPrefix(arg, "-tags="):
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

func lowAutonomyGoBuildArgs(args []string) bool {
	expectValue := false
	packages := make([]string, 0, len(args))
	for _, arg := range args {
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
			case goBuildFlagTakesSeparateValue(arg):
				expectValue = true
			case goBuildFlagAllowed(arg):
				continue
			default:
				return false
			}
			continue
		}
		if !goTestPackageArgAllowed(arg) {
			return false
		}
		packages = append(packages, filepath.ToSlash(arg))
	}
	return !expectValue && len(packages) == 1 && packages[0] == "./..."
}

func goBuildFlagTakesSeparateValue(arg string) bool {
	switch arg {
	case "-tags", "-mod":
		return true
	default:
		return false
	}
}

func goBuildFlagAllowed(arg string) bool {
	switch arg {
	case "-v", "-x", "-race", "-trimpath":
		return true
	case "-o", "-exec", "-toolexec", "-buildmode":
		return false
	}
	for _, prefix := range []string{"-tags=", "-mod="} {
		if strings.HasPrefix(arg, prefix) && strings.TrimSpace(strings.TrimPrefix(arg, prefix)) != "" {
			return true
		}
	}
	for _, prefix := range []string{"-o=", "-exec=", "-toolexec=", "-buildmode="} {
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

func lowAutonomyMakeArgs(args []string) bool {
	if len(args) == 0 {
		return false
	}
	sawTarget := false
	expectJobsValue := false
	for _, arg := range args {
		if expectJobsValue {
			if !unsignedInteger(arg) {
				return false
			}
			expectJobsValue = false
			continue
		}
		switch {
		case arg == "-j" || arg == "--jobs":
			expectJobsValue = true
		case strings.HasPrefix(arg, "-j") && len(arg) > 2:
			if !unsignedInteger(strings.TrimPrefix(arg, "-j")) {
				return false
			}
		case strings.HasPrefix(arg, "--jobs="):
			if !unsignedInteger(strings.TrimPrefix(arg, "--jobs=")) {
				return false
			}
		case arg == "-k" || arg == "--keep-going" || arg == "-s" || arg == "--silent":
			continue
		case strings.HasPrefix(arg, "-") || strings.Contains(arg, "="):
			return false
		default:
			if !verificationTargetAllowed(arg) {
				return false
			}
			sawTarget = true
		}
	}
	return sawTarget && !expectJobsValue
}

func lowAutonomyTaskRunnerArgs(args []string) bool {
	if len(args) == 0 {
		return false
	}
	sawTarget := false
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") || strings.Contains(arg, "=") {
			return false
		}
		if !verificationTargetAllowed(arg) {
			return false
		}
		sawTarget = true
	}
	return sawTarget
}

func lowAutonomyNPMArgs(args []string) bool {
	args = stripPackageManagerOptions(args)
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "test", "t":
		return safeVerificationArgs(args[1:])
	case "run", "run-script":
		return lowAutonomyScriptArgs(args[1:])
	default:
		return false
	}
}

func lowAutonomyPNPMArgs(args []string) bool {
	args = stripPackageManagerOptions(args)
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "test":
		return safeVerificationArgs(args[1:])
	case "run":
		return lowAutonomyScriptArgs(args[1:])
	default:
		return false
	}
}

func lowAutonomyYarnArgs(args []string) bool {
	args = stripPackageManagerOptions(args)
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "test":
		return safeVerificationArgs(args[1:])
	case "run":
		return lowAutonomyScriptArgs(args[1:])
	default:
		return lowAutonomyScriptArgs(args)
	}
}

func lowAutonomyBunArgs(args []string) bool {
	args = stripPackageManagerOptions(args)
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "test":
		return safeVerificationArgs(args[1:])
	case "run":
		return lowAutonomyScriptArgs(args[1:])
	default:
		return false
	}
}

func stripPackageManagerOptions(args []string) []string {
	for len(args) > 0 {
		switch args[0] {
		case "--silent", "--if-present", "--no-audit", "--no-fund", "--ignore-scripts", "--offline":
			args = args[1:]
		default:
			return args
		}
	}
	return args
}

func lowAutonomyScriptArgs(args []string) bool {
	if len(args) == 0 || !verificationTargetAllowed(args[0]) {
		return false
	}
	return safeVerificationArgs(args[1:])
}

func lowAutonomyCargoArgs(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "test", "check", "clippy", "build":
		return safeVerificationArgs(args[1:])
	case "fmt":
		return safeVerificationArgs(args[1:]) && hasArg(args[1:], "--check")
	default:
		return false
	}
}

func lowAutonomyPythonArgs(args []string) bool {
	if len(args) < 2 || args[0] != "-m" {
		return false
	}
	switch args[1] {
	case "pytest", "mypy":
		return safeVerificationArgs(args[2:])
	default:
		return false
	}
}

func lowAutonomyRuffArgs(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "check":
		return safeVerificationArgs(args[1:])
	case "format":
		return hasArg(args[1:], "--check") && safeVerificationArgs(args[1:])
	default:
		return false
	}
}

func lowAutonomyPrettierArgs(args []string) bool {
	return len(args) > 0 && (hasArg(args, "--check") || hasArg(args, "--list-different")) && safeVerificationArgs(args)
}

func lowAutonomyESLintArgs(args []string) bool {
	return safeVerificationArgs(args)
}

func lowAutonomyTypeScriptArgs(args []string) bool {
	return hasArg(args, "--noEmit") && safeVerificationArgs(args)
}

func lowAutonomyBiomeArgs(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "check", "ci", "lint":
		return safeVerificationArgs(args[1:])
	case "format":
		return hasArg(args[1:], "--check") && safeVerificationArgs(args[1:])
	default:
		return false
	}
}

func lowAutonomyGoFormatArgs(args []string) bool {
	if len(args) == 0 {
		return false
	}
	sawPath := false
	for _, arg := range args {
		switch arg {
		case "-l", "-d", "-e", "-s":
			continue
		case "-w":
			return false
		default:
			if strings.HasPrefix(arg, "-") || argEscapesWorkspaceLexically(arg) {
				return false
			}
			sawPath = true
		}
	}
	return sawPath
}

func verificationTargetAllowed(target string) bool {
	target = strings.TrimSpace(strings.ToLower(target))
	if target == "" || !safeScriptOrTargetName(target) {
		return false
	}
	if base, _, ok := strings.Cut(target, ":"); ok {
		target = base
	}
	switch target {
	case "build", "check", "checks", "ci", "lint", "test", "tests", "type-check", "typecheck", "unit", "verify":
		return true
	default:
		return false
	}
}

func safeScriptOrTargetName(value string) bool {
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			continue
		case r >= 'A' && r <= 'Z':
			continue
		case r >= '0' && r <= '9':
			continue
		case r == '-' || r == '_' || r == ':' || r == '.':
			continue
		default:
			return false
		}
	}
	return true
}

func safeVerificationArgs(args []string) bool {
	for _, arg := range args {
		if arg == "" {
			return false
		}
		if arg == "--" {
			continue
		}
		if verificationArgDenied(arg) || argEscapesWorkspaceLexically(arg) {
			return false
		}
	}
	return true
}

func verificationArgDenied(arg string) bool {
	lower := strings.ToLower(strings.TrimSpace(arg))
	switch lower {
	case "-g", "-i", "-u", "-w",
		"--clean", "--delete", "--fix", "--force", "--global", "--install",
		"--interactive", "--open", "--publish", "--remove", "--update",
		"--update-snapshot", "--updatesnapshot", "--watch", "--watchall", "--write":
		return true
	}
	if strings.HasPrefix(lower, "--reporter=json:") {
		return true
	}
	for _, prefix := range []string{
		"--cache-location",
		"--cache-clear",
		"--clean",
		"--coverage",
		"--cov",
		"--cov-report",
		"--createstub",
		"--delete",
		"--fix",
		"--html",
		"--install-types",
		"--junitxml",
		"--out",
		"--out-dir",
		"--out-file",
		"--outdir",
		"--outfile",
		"--output",
		"--output-file",
		"--outputfile",
		"--update",
		"--update-snapshot",
		"--updatesnapshot",
		"--watch",
		"--watchall",
		"--write",
	} {
		if lower == prefix || strings.HasPrefix(lower, prefix+"=") {
			return true
		}
	}
	return false
}

func argEscapesWorkspaceLexically(arg string) bool {
	if strings.TrimSpace(arg) == "" {
		return true
	}
	values := []string{arg}
	if key, value, ok := strings.Cut(arg, "="); ok && strings.HasPrefix(key, "-") {
		values = append(values, value)
	}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		slash := filepath.ToSlash(value)
		if filepath.IsAbs(value) || slash == ".." || strings.HasPrefix(slash, "../") || strings.Contains(slash, "/../") || strings.HasSuffix(slash, "/..") {
			return true
		}
	}
	return false
}

func argvEscapesWorkspaceLexically(args []string) bool {
	for _, arg := range args {
		if argEscapesWorkspaceLexically(arg) {
			return true
		}
	}
	return false
}

func hasArg(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}

func unsignedInteger(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
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
