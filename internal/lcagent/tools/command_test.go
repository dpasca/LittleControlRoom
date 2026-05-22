package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lcroom/internal/lcagent/policy"
)

func TestCommandRunnerIncludesStderrOnFailure(t *testing.T) {
	w, err := policy.NewWorkspace(t.TempDir(), policy.AutonomyMedium)
	if err != nil {
		t.Fatal(err)
	}
	result := CommandRunner{Workspace: w, ArtifactDir: t.TempDir()}.Run(context.Background(), `printf "bad\n" >&2; exit 7`, time.Second)
	if result.Success {
		t.Fatal("command succeeded, want failure")
	}
	if result.ExitCode != 7 {
		t.Fatalf("ExitCode = %d, want 7", result.ExitCode)
	}
	if !strings.Contains(result.Output, "bad") {
		t.Fatalf("Output did not include stderr: %q", result.Output)
	}
}

func TestCommandRunnerTimesOut(t *testing.T) {
	w, err := policy.NewWorkspace(t.TempDir(), policy.AutonomyMedium)
	if err != nil {
		t.Fatal(err)
	}
	result := CommandRunner{Workspace: w, ArtifactDir: t.TempDir()}.Run(context.Background(), `sleep 2`, 10*time.Millisecond)
	if result.Success {
		t.Fatal("sleep succeeded, want timeout")
	}
	if !result.TimedOut {
		t.Fatalf("TimedOut = false, output %q", result.Output)
	}
	if !strings.Contains(result.Error, "process group terminated") {
		t.Fatalf("Error = %q, want process-group termination note", result.Error)
	}
	if !strings.Contains(result.Output, "assume long-running servers or watchers from this command are stopped") {
		t.Fatalf("Output missing timeout liveness warning: %q", result.Output)
	}
}

func TestCommandRunnerTimeoutKillsChildProcessHoldingOutputPipe(t *testing.T) {
	w, err := policy.NewWorkspace(t.TempDir(), policy.AutonomyMedium)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	result := CommandRunner{Workspace: w, ArtifactDir: t.TempDir()}.Run(context.Background(), `sleep 5 &`, 50*time.Millisecond)
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("command returned after %s, want prompt timeout despite child process holding pipe; result=%#v", elapsed, result)
	}
	if result.Success || !result.TimedOut {
		t.Fatalf("result = %#v, want timeout", result)
	}
}

func TestCommandRunnerSupportsArgv(t *testing.T) {
	w, err := policy.NewWorkspace(t.TempDir(), policy.AutonomyOff)
	if err != nil {
		t.Fatal(err)
	}
	result := CommandRunner{Workspace: w, ArtifactDir: t.TempDir()}.RunSpec(context.Background(), CommandSpec{
		Argv:      []string{"pwd"},
		TimeoutMS: 1000,
	})
	if !result.Success {
		t.Fatalf("argv command failed: %#v", result)
	}
	if !strings.Contains(result.Output, w.Root) {
		t.Fatalf("output = %q, want workspace path", result.Output)
	}
}

func TestCommandRunnerRunsInWorkspaceRelativeCWD(t *testing.T) {
	root := t.TempDir()
	frontend := filepath.Join(root, "frontend")
	if err := os.Mkdir(frontend, 0o755); err != nil {
		t.Fatal(err)
	}
	w, err := policy.NewWorkspace(root, policy.AutonomyLow)
	if err != nil {
		t.Fatal(err)
	}
	result := CommandRunner{Workspace: w, ArtifactDir: t.TempDir()}.RunSpec(context.Background(), CommandSpec{
		Argv:      []string{"pwd"},
		CWD:       "frontend",
		TimeoutMS: 1000,
	})
	if !result.Success {
		t.Fatalf("argv command failed: %#v", result)
	}
	wantCWD := filepath.Join(w.Root, "frontend")
	if result.CWD != wantCWD {
		t.Fatalf("CWD = %q, want %q", result.CWD, wantCWD)
	}
	if !strings.Contains(result.Output, wantCWD) {
		t.Fatalf("output = %q, want cwd %q", result.Output, wantCWD)
	}
}

func TestCommandRunnerDeniesCWDOutsideWorkspaceBelowMedium(t *testing.T) {
	w, err := policy.NewWorkspace(t.TempDir(), policy.AutonomyLow)
	if err != nil {
		t.Fatal(err)
	}
	result := CommandRunner{Workspace: w, ArtifactDir: t.TempDir()}.RunSpec(context.Background(), CommandSpec{
		Argv:      []string{"pwd"},
		CWD:       "..",
		TimeoutMS: 1000,
	})
	if result.Success {
		t.Fatalf("command with escaping cwd succeeded below medium: %#v", result)
	}
	if !result.Denied || !strings.Contains(result.DenialReason, "cwd is outside the workspace") {
		t.Fatalf("denial metadata = denied %v reason %q", result.Denied, result.DenialReason)
	}
}

func TestCommandRunnerPreservesVerificationPurpose(t *testing.T) {
	w, err := policy.NewWorkspace(t.TempDir(), policy.AutonomyOff)
	if err != nil {
		t.Fatal(err)
	}
	result := CommandRunner{Workspace: w, ArtifactDir: t.TempDir()}.RunSpec(context.Background(), CommandSpec{
		Argv:      []string{"pwd"},
		TimeoutMS: 1000,
		Purpose:   "test",
	})
	if !result.Success {
		t.Fatalf("argv command failed: %#v", result)
	}
	if result.Purpose != CommandPurposeVerify || result.Command != "pwd" {
		t.Fatalf("purpose/command = %q/%q, want verify/pwd", result.Purpose, result.Command)
	}
}

func TestCommandRunnerDeniesBroadCommandBelowMedium(t *testing.T) {
	w, err := policy.NewWorkspace(t.TempDir(), policy.AutonomyLow)
	if err != nil {
		t.Fatal(err)
	}
	result := CommandRunner{Workspace: w, ArtifactDir: t.TempDir()}.RunSpec(context.Background(), CommandSpec{
		Argv:      []string{"sh", "-c", "printf hi > out.txt"},
		TimeoutMS: 1000,
	})
	if result.Success {
		t.Fatalf("broad command succeeded below medium: %#v", result)
	}
	if !strings.Contains(result.Error, "below medium autonomy") {
		t.Fatalf("error = %q", result.Error)
	}
	if !result.Denied || !strings.Contains(result.DenialReason, "below medium autonomy") {
		t.Fatalf("denial metadata = denied %v reason %q", result.Denied, result.DenialReason)
	}
}
