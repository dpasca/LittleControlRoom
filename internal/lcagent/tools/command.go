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

	"lcroom/internal/lcagent/policy"
	"lcroom/internal/lcagent/present"
)

const (
	defaultCommandTimeout = 10 * time.Second
	maxCommandTimeout     = 60 * time.Second
)

type CommandRunner struct {
	Workspace   policy.Workspace
	ArtifactDir string
}

type CommandSpec struct {
	Command   string
	Argv      []string
	CWD       string
	Shell     bool
	TimeoutMS int
	Purpose   string
}

func (r CommandRunner) Run(ctx context.Context, command string, timeout time.Duration) ToolResult {
	return r.RunSpec(ctx, CommandSpec{Command: command, Shell: true, TimeoutMS: int(timeout / time.Millisecond)})
}

func (r CommandRunner) RunSpec(ctx context.Context, spec CommandSpec) ToolResult {
	cwdLabel := commandCWDLabel(r.Workspace.Root, spec.CWD)
	cwd, err := r.Workspace.ResolveCommandCWD(spec.CWD)
	if err != nil {
		result := ToolResult{
			Success: false,
			Error:   err.Error(),
			Command: commandLabelFromSpec(spec),
			Argv:    cleanArgv(spec.Argv),
			CWD:     cwdLabel,
			Purpose: normalizeCommandPurpose(spec.Purpose),
		}
		if policy.IsDenied(err) {
			result.Denied = true
			result.DenialReason = policy.DenialReason(err)
		}
		return result
	}
	if err := r.Workspace.AllowCommandSpec(spec.Argv, spec.Command, spec.Shell); err != nil {
		result := ToolResult{
			Success: false,
			Error:   err.Error(),
			Command: commandLabelFromSpec(spec),
			Argv:    cleanArgv(spec.Argv),
			CWD:     cwd,
			Purpose: normalizeCommandPurpose(spec.Purpose),
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

	cmd, label, err := commandFromSpec(ctx, spec)
	if err != nil {
		return ToolResult{Success: false, Error: err.Error(), Command: commandLabelFromSpec(spec), Argv: cleanArgv(spec.Argv), CWD: cwd, Purpose: normalizeCommandPurpose(spec.Purpose)}
	}
	cmd.Dir = cwd
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	duration := time.Since(start)
	timedOut := ctx.Err() == context.DeadlineExceeded
	exitCode := 0
	if err != nil {
		exitCode = exitCodeFromError(err)
	}
	if timedOut && exitCode == 0 {
		exitCode = -1
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
		Success:      err == nil && !timedOut,
		Output:       p.Text,
		Error:        errorString(err, timedOut),
		Command:      label,
		Argv:         cleanArgv(spec.Argv),
		CWD:          cwd,
		Purpose:      normalizeCommandPurpose(spec.Purpose),
		ExitCode:     exitCode,
		Duration:     duration,
		TimedOut:     timedOut,
		Truncated:    p.Truncated,
		Binary:       p.Binary,
		ArtifactPath: p.ArtifactPath,
	}
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

func commandFromSpec(ctx context.Context, spec CommandSpec) (*exec.Cmd, string, error) {
	if len(spec.Argv) > 0 {
		argv := cleanArgv(spec.Argv)
		if len(argv) == 0 {
			return nil, "", fmt.Errorf("argv is required")
		}
		return exec.CommandContext(ctx, argv[0], argv[1:]...), strings.Join(argv, " "), nil
	}
	command := strings.TrimSpace(spec.Command)
	if command == "" {
		return nil, "", fmt.Errorf("command is required")
	}
	return exec.CommandContext(ctx, "/bin/sh", "-c", command), command, nil
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
		return "command timed out"
	}
	if err == nil {
		return ""
	}
	return err.Error()
}
