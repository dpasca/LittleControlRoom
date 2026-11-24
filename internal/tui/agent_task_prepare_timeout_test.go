package tui

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"lcroom/internal/codexapp"
	"lcroom/internal/control"
	"lcroom/internal/model"
	"lcroom/internal/submodulealign/aligntest"
)

func TestCreateAgentTaskReportsInterruptedSubmoduleCheckout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Git wrapper requires a POSIX shell")
	}
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Skipf("git unavailable: %v", err)
	}
	aligntest.IsolateEnv(t)
	fixture := aligntest.New(t)

	for _, tt := range []struct {
		name     string
		git      string
		deadline bool
	}{
		{name: "deadline", git: "exec sleep 30", deadline: true},
		{name: "external kill", git: "kill -KILL $$"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			svc := newControlTestService(t)
			origin := fixture.Canonical
			input := control.AgentTaskCreateInput{
				RequestID: "lcrop_prepare_timeout",
				Title:     "Prepare task",
				Kind:      control.AgentTaskKindAgent,
				Resources: []control.ResourceRef{{Kind: control.ResourceProject, ProjectPath: origin}},
			}
			inv := controlInvocationRawForTest(t, control.CapabilityAgentTaskCreate, input)
			inv.RequestID = input.RequestID
			if _, err := svc.Store().CreateControlOperation(t.Context(), control.Operation{
				ID: inv.RequestID, Capability: inv.Capability, Invocation: inv,
				Status: control.OperationProposed, Confirmed: true,
				Source: "little-control-room-runtime", Provider: "codex", SessionKey: "caller-session",
				ProjectPath: origin, RequestedBy: "codex",
			}); err != nil {
				t.Fatal(err)
			}
			if err := svc.Store().BindEngineerSession(t.Context(), origin, model.SessionSourceCodex, "caller-session", "provider-thread"); err != nil {
				t.Fatal(err)
			}

			// Let the parent checkout succeed and interrupt only the nested add.
			bin := t.TempDir()
			script := `#!/bin/sh
case " $* " in
*" worktree add "*" --detach "*) ` + tt.git + `;;
esac
exec "$LCR_TEST_REAL_GIT" "$@"
`
			if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("LCR_TEST_REAL_GIT", realGit)
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

			ctx := t.Context()
			if tt.deadline {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 3*time.Second)
				defer cancel()
			}
			msg := (Model{ctx: ctx, svc: svc}).createBossAgentTaskCmd(inv, input, codexapp.ProviderCodex)().(bossAgentTaskCreatedMsg)
			if msg.err == nil || !strings.Contains(msg.err.Error(), "create submodule worktree asset") {
				t.Fatalf("expected a nested checkout failure, got %v", msg.err)
			}
			var exitErr *exec.ExitError
			if !errors.As(msg.err, &exitErr) || exitErr.ExitCode() != -1 {
				t.Fatalf("expected the underlying signal error, got %v", msg.err)
			}
			if errors.Is(msg.err, context.DeadlineExceeded) != tt.deadline {
				t.Fatalf("deadline classification for %s: %v", tt.name, msg.err)
			}
			if tt.deadline && !strings.HasPrefix(msg.err.Error(), "deadline exceeded while creating the agent task workspace") {
				t.Fatalf("expected the inherited deadline to be reported accurately, got %v", msg.err)
			}
			tasks, err := svc.Store().ListAgentTasks(t.Context(), model.AgentTaskFilter{Limit: 10})
			if err != nil || len(tasks) != 0 {
				t.Fatalf("failed creation left tasks = %#v, err = %v", tasks, err)
			}
		})
	}
}
