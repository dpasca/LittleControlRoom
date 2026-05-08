package tools

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
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

func (r CommandRunner) Run(ctx context.Context, command string, timeout time.Duration) ToolResult {
	if err := r.Workspace.AllowCommand(command); err != nil {
		return ToolResult{Success: false, Error: err.Error()}
	}
	timeout = policy.ClampTimeout(timeout, defaultCommandTimeout, maxCommandTimeout)
	start := time.Now()
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", command)
	cmd.Dir = r.Workspace.Root
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
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
		CommandLabel: command,
	})
	return ToolResult{
		Success:      err == nil && !timedOut,
		Output:       p.Text,
		Error:        errorString(err, timedOut),
		ExitCode:     exitCode,
		Duration:     duration,
		TimedOut:     timedOut,
		Truncated:    p.Truncated,
		Binary:       p.Binary,
		ArtifactPath: p.ArtifactPath,
	}
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
