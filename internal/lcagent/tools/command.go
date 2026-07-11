package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"lcroom/internal/commandguard"
	"lcroom/internal/lcagent/policy"
	"lcroom/internal/lcagent/present"
)

const (
	defaultCommandTimeout = 10 * time.Second
	maxCommandTimeout     = 60 * time.Second

	CommandWorkspaceWriteDenialReason  = "run_command appears to write workspace files"
	CommandSystemMutationDenialReason  = "run_command appears to mutate user/system configuration"
	CommandArgvShellSyntaxDenialReason = "run_command argv contains shell syntax"
	CommandAdminScopeSystem            = "system"
)

type CommandRunner struct {
	Workspace   policy.Workspace
	ArtifactDir string
}

type CommandSpec struct {
	Command          string
	Argv             []string
	CWD              string
	Shell            bool
	TimeoutMS        int
	Purpose          string
	AdminScope       string
	AllowedExitCodes []int
}

func (r CommandRunner) Run(ctx context.Context, command string, timeout time.Duration) ToolResult {
	return r.RunSpec(ctx, CommandSpec{Command: command, Shell: true, TimeoutMS: int(timeout / time.Millisecond)})
}

func (r CommandRunner) RunSpec(ctx context.Context, spec CommandSpec) ToolResult {
	cwdLabel := commandCWDLabel(r.Workspace.Root, spec.CWD)
	cwd, err := r.Workspace.ResolveCommandCWD(spec.CWD)
	if err != nil {
		result := ToolResult{
			Success:          false,
			Error:            err.Error(),
			Command:          commandLabelFromSpec(spec),
			Argv:             cleanArgv(spec.Argv),
			CWD:              cwdLabel,
			Purpose:          normalizeCommandPurpose(spec.Purpose),
			AdminScope:       normalizeCommandAdminScope(spec.AdminScope),
			AllowedExitCodes: cleanAllowedExitCodes(spec.AllowedExitCodes),
		}
		if policy.IsDenied(err) {
			result.Denied = true
			result.DenialReason = policy.DenialReason(err)
		}
		return result
	}
	if reason := commandArgvShellSyntaxDenialReason(spec); reason != "" {
		return ToolResult{
			Success:          false,
			Error:            reason,
			Denied:           true,
			DenialReason:     reason,
			Command:          commandLabelFromSpec(spec),
			Argv:             cleanArgv(spec.Argv),
			CWD:              cwd,
			Purpose:          normalizeCommandPurpose(spec.Purpose),
			AdminScope:       normalizeCommandAdminScope(spec.AdminScope),
			AllowedExitCodes: cleanAllowedExitCodes(spec.AllowedExitCodes),
		}
	}
	if commandContainsDirectRM(spec) {
		return ToolResult{
			Success:          false,
			Error:            commandguard.DirectRMDenialReason,
			Denied:           true,
			DenialReason:     commandguard.DirectRMDenialReason,
			Command:          commandLabelFromSpec(spec),
			Argv:             cleanArgv(spec.Argv),
			CWD:              cwd,
			Purpose:          normalizeCommandPurpose(spec.Purpose),
			AdminScope:       normalizeCommandAdminScope(spec.AdminScope),
			AllowedExitCodes: cleanAllowedExitCodes(spec.AllowedExitCodes),
		}
	}
	if reason := commandWorkspaceWriteDenialReason(spec); reason != "" {
		return ToolResult{
			Success:          false,
			Error:            reason,
			Denied:           true,
			DenialReason:     reason,
			Command:          commandLabelFromSpec(spec),
			Argv:             cleanArgv(spec.Argv),
			CWD:              cwd,
			Purpose:          normalizeCommandPurpose(spec.Purpose),
			AdminScope:       normalizeCommandAdminScope(spec.AdminScope),
			AllowedExitCodes: cleanAllowedExitCodes(spec.AllowedExitCodes),
		}
	}
	if reason := commandSystemMutationDenialReason(spec, r.Workspace.AdminWrite); reason != "" {
		return ToolResult{
			Success:          false,
			Error:            reason,
			Denied:           true,
			DenialReason:     reason,
			Command:          commandLabelFromSpec(spec),
			Argv:             cleanArgv(spec.Argv),
			CWD:              cwd,
			Purpose:          normalizeCommandPurpose(spec.Purpose),
			AdminScope:       normalizeCommandAdminScope(spec.AdminScope),
			SystemMutation:   true,
			AllowedExitCodes: cleanAllowedExitCodes(spec.AllowedExitCodes),
		}
	}
	if err := r.Workspace.AllowCommandSpec(spec.Argv, spec.Command, spec.Shell); err != nil {
		result := ToolResult{
			Success:          false,
			Error:            err.Error(),
			Command:          commandLabelFromSpec(spec),
			Argv:             cleanArgv(spec.Argv),
			CWD:              cwd,
			Purpose:          normalizeCommandPurpose(spec.Purpose),
			AdminScope:       normalizeCommandAdminScope(spec.AdminScope),
			SystemMutation:   commandSystemMutationDetail(spec) != "",
			AllowedExitCodes: cleanAllowedExitCodes(spec.AllowedExitCodes),
		}
		if policy.IsDenied(err) {
			result.Denied = true
			result.DenialReason = policy.DenialReason(err)
		}
		return result
	}
	timeout := policy.ClampTimeout(time.Duration(spec.TimeoutMS)*time.Millisecond, defaultCommandTimeout, maxCommandTimeout)
	start := time.Now()
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd, label, err := commandFromSpec(spec)
	if err != nil {
		return ToolResult{Success: false, Error: err.Error(), Command: commandLabelFromSpec(spec), Argv: cleanArgv(spec.Argv), CWD: cwd, Purpose: normalizeCommandPurpose(spec.Purpose), AdminScope: normalizeCommandAdminScope(spec.AdminScope), SystemMutation: commandSystemMutationDetail(spec) != "", AllowedExitCodes: cleanAllowedExitCodes(spec.AllowedExitCodes)}
	}
	cmd.Dir = cwd
	prepareCommandProcessGroup(cmd)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = runCommandProcess(ctx, cmd)
	duration := time.Since(start)
	timedOut := ctx.Err() == context.DeadlineExceeded
	exitCode := 0
	if err != nil {
		exitCode = exitCodeFromError(err)
	}
	if timedOut && exitCode == 0 {
		exitCode = -1
	}
	allowedExitCodes := cleanAllowedExitCodes(spec.AllowedExitCodes)
	exitAllowed := commandExitCodeAllowed(exitCode, allowedExitCodes) && !timedOut
	displayErr := err
	if exitAllowed {
		displayErr = nil
	}
	p := present.Command(present.CommandOutput{
		Stdout:       stdout.Bytes(),
		Stderr:       stderr.Bytes(),
		ExitCode:     exitCode,
		Duration:     duration,
		TimedOut:     timedOut,
		ArtifactDir:  r.ArtifactDir,
		CommandLabel: label,
	})
	return ToolResult{
		Success:          (err == nil || exitAllowed) && !timedOut,
		Output:           p.Text,
		Error:            errorString(displayErr, timedOut),
		Command:          label,
		Argv:             cleanArgv(spec.Argv),
		CWD:              cwd,
		Purpose:          normalizeCommandPurpose(spec.Purpose),
		AdminScope:       normalizeCommandAdminScope(spec.AdminScope),
		SystemMutation:   commandSystemMutationDetail(spec) != "",
		AllowedExitCodes: allowedExitCodes,
		ExitCode:         exitCode,
		Duration:         duration,
		TimedOut:         timedOut,
		Truncated:        p.Truncated,
		Binary:           p.Binary,
		ArtifactPath:     p.ArtifactPath,
	}
}

func commandContainsDirectRM(spec CommandSpec) bool {
	argv := cleanArgv(spec.Argv)
	if len(argv) > 0 {
		return commandguard.ArgvContainsDirectRM(argv)
	}
	return commandguard.ContainsDirectRM(spec.Command)
}

func IsWorkspaceWriteCommandDenied(result ToolResult) bool {
	return result.Denied && strings.HasPrefix(result.DenialReason, CommandWorkspaceWriteDenialReason)
}

func IsSystemMutationCommandDenied(result ToolResult) bool {
	return result.Denied && strings.HasPrefix(result.DenialReason, CommandSystemMutationDenialReason)
}

func commandWorkspaceWriteDenialReason(spec CommandSpec) string {
	detail := ""
	if len(cleanArgv(spec.Argv)) > 0 {
		detail = argvWorkspaceWriteDetail(cleanArgv(spec.Argv))
	} else {
		detail = shellWorkspaceWriteDetail(spec.Command)
	}
	if detail == "" {
		return ""
	}
	return CommandWorkspaceWriteDenialReason + " (" + detail + "); use create_file, replace_file, apply_patch, replace_lines, or replace_text for source edits instead"
}

func commandArgvShellSyntaxDenialReason(spec CommandSpec) string {
	token := argvShellSyntaxToken(cleanArgv(spec.Argv))
	if token == "" {
		return ""
	}
	return fmt.Sprintf("%s (%q); use command with shell=true for shell operators, redirects, or multiple commands, or split this into separate argv-only run_command calls", CommandArgvShellSyntaxDenialReason, token)
}

func argvShellSyntaxToken(argv []string) string {
	for _, arg := range argv {
		token := strings.TrimSpace(arg)
		switch token {
		case "&&", "||", ";", "|", "|&", "&", ">", ">>", "<", "<<", "2>", "2>>", "1>", "1>>":
			return token
		}
		if strings.HasPrefix(token, "2>") || strings.HasPrefix(token, "1>") {
			return token
		}
	}
	return ""
}

func commandSystemMutationDenialReason(spec CommandSpec, adminWrite bool) string {
	detail := commandSystemMutationDetail(spec)
	if detail == "" {
		return ""
	}
	if normalizeCommandAdminScope(spec.AdminScope) != CommandAdminScopeSystem {
		return CommandSystemMutationDenialReason + " (" + detail + "); set admin_scope=system only when the user explicitly requested a persistent system/admin change"
	}
	if !adminWrite {
		return CommandSystemMutationDenialReason + " (" + detail + "); enable LCAgent admin write (CLI: --admin-write; LCR /settings: LCAgent admin write) only when the task truly needs system/admin changes"
	}
	return ""
}

func commandSystemMutationDetail(spec CommandSpec) string {
	if argv := cleanArgv(spec.Argv); len(argv) > 0 {
		return argvSystemMutationDetail(argv)
	}
	for _, segment := range shellCommandSegments(spec.Command) {
		if detail := argvSystemMutationDetail(shellSegmentFields(segment)); detail != "" {
			return detail
		}
	}
	return ""
}

func shellSegmentFields(segment string) []string {
	fields := strings.Fields(strings.TrimSpace(segment))
	for len(fields) > 0 && isShellPrefixWord(fields[0]) {
		fields = fields[1:]
	}
	return fields
}

func argvSystemMutationDetail(argv []string) string {
	if len(argv) == 0 {
		return ""
	}
	name := strings.ToLower(filepath.Base(argv[0]))
	switch {
	case name == "sh" || name == "bash" || name == "zsh":
		if script := shellScriptArg(argv); script != "" {
			return commandSystemMutationDetail(CommandSpec{Command: script, Shell: true})
		}
	case name == "defaults":
		if len(argv) >= 2 && macOSDefaultsMutationSubcommand(argv[1]) {
			return "detected macOS defaults mutation"
		}
	case name == "lsregister":
		return "detected Launch Services registration command"
	case name == "duti":
		if len(argv) >= 2 && argv[1] == "-s" {
			return "detected file-association mutation via duti"
		}
	case name == "brew":
		if len(argv) >= 2 && homebrewMutationSubcommand(argv[1]) {
			return "detected Homebrew state mutation"
		}
	case name == "npm" || name == "pnpm":
		if packageManagerGlobalMutation(argv[1:]) {
			return "detected global package-manager state mutation"
		}
	case name == "yarn":
		if yarnGlobalMutation(argv[1:]) {
			return "detected global package-manager state mutation"
		}
	}
	return ""
}

func macOSDefaultsMutationSubcommand(subcommand string) bool {
	switch strings.ToLower(strings.TrimSpace(subcommand)) {
	case "write", "delete", "import", "rename", "rename-domain":
		return true
	default:
		return false
	}
}

func homebrewMutationSubcommand(subcommand string) bool {
	switch strings.ToLower(strings.TrimSpace(subcommand)) {
	case "install", "uninstall", "reinstall", "upgrade", "update", "tap", "untap", "link", "unlink", "pin", "unpin", "services", "bundle", "cleanup", "autoremove":
		return true
	default:
		return false
	}
}

func packageManagerGlobalMutation(args []string) bool {
	if len(args) == 0 || !containsGlobalFlag(args) {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(args[0])) {
	case "install", "i", "add", "remove", "rm", "uninstall", "update", "upgrade", "link", "unlink":
		return true
	default:
		return false
	}
}

func yarnGlobalMutation(args []string) bool {
	if len(args) == 0 {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(args[0]), "global") {
		return true
	}
	return packageManagerGlobalMutation(args)
}

func containsGlobalFlag(args []string) bool {
	for _, arg := range args {
		switch strings.ToLower(strings.TrimSpace(arg)) {
		case "-g", "--global":
			return true
		}
	}
	return false
}

func argvWorkspaceWriteDetail(argv []string) string {
	if len(argv) == 0 {
		return ""
	}
	name := strings.ToLower(filepath.Base(argv[0]))
	switch {
	case name == "sh" || name == "bash" || name == "zsh":
		if script := shellScriptArg(argv); script != "" {
			return shellWorkspaceWriteDetail(script)
		}
	case name == "tee" || name == "rm" || name == "mv" || name == "cp" || name == "install":
		return "detected workspace-mutating command " + name
	case name == "sed":
		for _, arg := range argv[1:] {
			if arg == "-i" || arg == "--in-place" || strings.HasPrefix(arg, "-i") {
				return "detected in-place sed edit"
			}
		}
	case name == "perl":
		for _, arg := range argv[1:] {
			if strings.HasPrefix(arg, "-pi") || (strings.HasPrefix(arg, "-p") && strings.Contains(arg, "i")) {
				return "detected in-place perl edit"
			}
		}
	case strings.HasPrefix(name, "python") || name == "node" || name == "ruby":
		if inline := inlineProgramArg(argv); inline != "" && inlineProgramWritesFiles(name, inline) {
			return "detected inline program file write"
		}
	}
	return ""
}

func shellScriptArg(argv []string) string {
	for i := 1; i < len(argv); i++ {
		if strings.HasPrefix(argv[i], "-") && strings.Contains(argv[i], "c") && i+1 < len(argv) {
			return argv[i+1]
		}
	}
	return ""
}

func inlineProgramArg(argv []string) string {
	for i := 1; i < len(argv); i++ {
		switch argv[i] {
		case "-c", "-e":
			if i+1 < len(argv) {
				return argv[i+1]
			}
		}
	}
	return ""
}

func inlineProgramWritesFiles(name, program string) bool {
	lower := strings.ToLower(program)
	if strings.HasPrefix(name, "python") || name == "ruby" {
		return strings.Contains(lower, "write_text(") ||
			strings.Contains(lower, "write_bytes(") ||
			(strings.Contains(lower, "open(") && inlineOpenUsesWriteMode(lower))
	}
	if name == "node" {
		return strings.Contains(lower, "writefile") ||
			strings.Contains(lower, "appendfile") ||
			strings.Contains(lower, "createwritestream")
	}
	return false
}

func inlineOpenUsesWriteMode(program string) bool {
	for _, marker := range []string{
		`,"w`, `,'w`, `, "w`, `, 'w`,
		`,"a`, `,'a`, `, "a`, `, 'a`,
		`,"x`, `,'x`, `, "x`, `, 'x`,
		`, mode="w`, `, mode='w`,
		`, mode="a`, `, mode='a`,
		`, mode="x`, `, mode='x`,
	} {
		if strings.Contains(program, marker) {
			return true
		}
	}
	return false
}

func shellWorkspaceWriteDetail(command string) string {
	command = strings.TrimSpace(command)
	if command == "" {
		return ""
	}
	if shellContainsHeredoc(command) {
		return "detected shell heredoc"
	}
	if shellContainsFileRedirection(command) {
		return "detected shell file redirection"
	}
	for _, segment := range shellCommandSegments(command) {
		if detail := shellSegmentWorkspaceWriteDetail(segment); detail != "" {
			return detail
		}
	}
	return ""
}

func shellContainsHeredoc(command string) bool {
	inSingle, inDouble, escaped := false, false, false
	for i := 0; i < len(command)-1; i++ {
		ch := command[i]
		if escaped {
			escaped = false
			continue
		}
		if ch == '\\' && !inSingle {
			escaped = true
			continue
		}
		switch ch {
		case '\'':
			if !inDouble {
				inSingle = !inSingle
			}
		case '"':
			if !inSingle {
				inDouble = !inDouble
			}
		case '<':
			if !inSingle && !inDouble && command[i+1] == '<' {
				return true
			}
		}
	}
	return false
}

func shellContainsFileRedirection(command string) bool {
	inSingle, inDouble, escaped := false, false, false
	for i := 0; i < len(command); i++ {
		ch := command[i]
		if escaped {
			escaped = false
			continue
		}
		if ch == '\\' && !inSingle {
			escaped = true
			continue
		}
		switch ch {
		case '\'':
			if !inDouble {
				inSingle = !inSingle
			}
		case '"':
			if !inSingle {
				inDouble = !inDouble
			}
		case '>':
			if inSingle || inDouble {
				continue
			}
			if i+1 < len(command) && (command[i+1] == '&' || command[i+1] == '(') {
				continue
			}
			if shellStderrDiscardRedirection(command, i) {
				continue
			}
			return true
		case '&':
			if inSingle || inDouble {
				continue
			}
			if i+1 < len(command) && command[i+1] == '>' {
				return true
			}
		}
	}
	return false
}

func shellStderrDiscardRedirection(command string, redirectIndex int) bool {
	if redirectIndex <= 0 || command[redirectIndex] != '>' || command[redirectIndex-1] != '2' {
		return false
	}
	if redirectIndex+1 < len(command) && command[redirectIndex+1] == '>' {
		return false
	}
	if redirectIndex >= 2 {
		before := command[redirectIndex-2]
		if before >= '0' && before <= '9' {
			return false
		}
	}
	targetStart := redirectIndex + 1
	for targetStart < len(command) && (command[targetStart] == ' ' || command[targetStart] == '\t') {
		targetStart++
	}
	const devNull = "/dev/null"
	if !strings.HasPrefix(command[targetStart:], devNull) {
		return false
	}
	targetEnd := targetStart + len(devNull)
	if targetEnd >= len(command) {
		return true
	}
	next := command[targetEnd]
	return next == ' ' || next == '\t' || next == '\n' || next == ';' || next == '&' || next == '|'
}

func shellCommandSegments(command string) []string {
	replacer := strings.NewReplacer("\n", ";", "&&", ";", "||", ";", "|", ";")
	raw := strings.Split(replacer.Replace(command), ";")
	segments := make([]string, 0, len(raw))
	for _, segment := range raw {
		segment = strings.TrimSpace(segment)
		if segment != "" {
			segments = append(segments, segment)
		}
	}
	return segments
}

func shellSegmentWorkspaceWriteDetail(segment string) string {
	fields := shellSegmentFields(segment)
	if len(fields) == 0 {
		return ""
	}
	return argvWorkspaceWriteDetail(fields)
}

func isShellPrefixWord(value string) bool {
	value = strings.TrimSpace(value)
	if value == "command" || value == "sudo" {
		return true
	}
	if strings.Contains(value, "=") {
		parts := strings.SplitN(value, "=", 2)
		return parts[0] != "" && !strings.ContainsAny(parts[0], `/\`)
	}
	return false
}

func commandLabelFromSpec(spec CommandSpec) string {
	if len(spec.Argv) > 0 {
		return strings.Join(cleanArgv(spec.Argv), " ")
	}
	return strings.TrimSpace(spec.Command)
}

func commandCWDLabel(root, cwd string) string {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return filepath.Clean(root)
	}
	if filepath.IsAbs(cwd) {
		return filepath.Clean(cwd)
	}
	return filepath.Clean(filepath.Join(root, cwd))
}

func normalizeCommandPurpose(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case CommandPurposeVerify, "verification", "test", "tests", "lint", "typecheck", "type-check", "build":
		return CommandPurposeVerify
	case CommandPurposeInspect, "":
		return ""
	default:
		return strings.ToLower(strings.TrimSpace(raw))
	}
}

func normalizeCommandAdminScope(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case CommandAdminScopeSystem:
		return CommandAdminScopeSystem
	default:
		return ""
	}
}

func commandFromSpec(spec CommandSpec) (*exec.Cmd, string, error) {
	if len(spec.Argv) > 0 {
		argv := cleanArgv(spec.Argv)
		if len(argv) == 0 {
			return nil, "", fmt.Errorf("argv is required")
		}
		return exec.Command(argv[0], argv[1:]...), strings.Join(argv, " "), nil
	}
	command := strings.TrimSpace(spec.Command)
	if command == "" {
		return nil, "", fmt.Errorf("command is required")
	}
	return exec.Command("/bin/sh", "-c", command), command, nil
}

func runCommandProcess(ctx context.Context, cmd *exec.Cmd) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		killCommandProcessGroup(cmd)
		return <-done
	}
}

func prepareCommandProcessGroup(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func killCommandProcessGroup(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	pid := cmd.Process.Pid
	if pid <= 0 {
		return
	}
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil {
		_ = cmd.Process.Kill()
	}
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

func cleanAllowedExitCodes(codes []int) []int {
	if len(codes) == 0 {
		return nil
	}
	seen := map[int]struct{}{}
	out := make([]int, 0, len(codes))
	for _, code := range codes {
		if code < 0 || code > 255 {
			continue
		}
		if _, ok := seen[code]; ok {
			continue
		}
		seen[code] = struct{}{}
		out = append(out, code)
	}
	return out
}

func commandExitCodeAllowed(exitCode int, allowed []int) bool {
	for _, code := range allowed {
		if code == exitCode {
			return true
		}
	}
	return false
}

func exitCodeFromError(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
			return status.ExitStatus()
		}
	}
	return -1
}

func errorString(err error, timedOut bool) string {
	if timedOut {
		return "command timed out; process group terminated"
	}
	if err == nil {
		return ""
	}
	return err.Error()
}
