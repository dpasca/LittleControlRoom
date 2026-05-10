package tools

import (
	"context"
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
}
