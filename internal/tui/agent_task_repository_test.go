package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	bossui "lcroom/internal/boss"
	"lcroom/internal/codexapp"
	"lcroom/internal/control"
	"lcroom/internal/model"
	"lcroom/internal/projectrun"
)

func TestAgentTaskRepositoryControlPersistsBeforeAnyWorkerTurn(t *testing.T) {
	for _, dirty := range []bool{false, true} {
		name := "clean"
		if dirty {
			name = "dirty"
		}
		t.Run(name, func(t *testing.T) {
			svc := newControlTestService(t)
			root := t.TempDir()
			for _, args := range [][]string{{"init", "-b", "fixture"}, {"-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "baseline"}} {
				cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("git: %v %s", err, out)
				}
			}
			if dirty {
				if err := os.WriteFile(filepath.Join(root, "user.txt"), []byte("preserve"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			started := false
			manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
				if req.TurnAdmission == nil {
					t.Fatal("missing submission guard")
				}
				unlock, err := req.TurnAdmission(req.ProjectPath, req.TodoCaptureSessionKey)
				if err != nil {
					return nil, err
				}
				defer unlock()
				tasks, err := svc.Store().ListAgentTasks(t.Context(), model.AgentTaskFilter{})
				if err != nil || len(tasks) != 1 || tasks[0].Repository.State != "held" {
					t.Fatalf("not visible/durable before turn: %+v %v", tasks, err)
				}
				if !strings.Contains(req.Prompt, "Do not commit") || !strings.Contains(req.Prompt, tasks[0].Repository.Root) {
					t.Fatal("worker missing repository contract")
				}
				started = true
				return &fakeCodexSession{projectPath: req.ProjectPath, snapshot: codexapp.Snapshot{Provider: req.Provider, ThreadID: "worker", Started: true}}, nil
			})
			svc.ConfigureAgentTaskRepositoryHost(manager.Snapshots, func() []projectrun.Snapshot { return nil })
			m := Model{ctx: t.Context(), svc: svc, codexManager: manager}
			inv := controlInvocationRawForTest(t, control.CapabilityAgentTaskCreate, control.AgentTaskCreateInput{Title: "Visible writer", Kind: control.AgentTaskKindAgent, Provider: control.ProviderCodex, RepositoryWrite: true, Prompt: "Implement", Resources: []control.ResourceRef{{Kind: control.ResourceProject, ProjectPath: root}}})
			updated, cmd := m.executeBossControlInvocation(bossui.ControlInvocationConfirmedMsg{Invocation: inv})
			if cmd == nil {
				t.Fatal("missing create command")
			}
			created, ok := cmd().(bossAgentTaskCreatedMsg)
			if !ok || created.err != nil {
				t.Fatalf("create: %+v", created)
			}
			_, cmd = updated.(Model).Update(created)
			if dirty {
				found := false
				for _, msg := range collectCmdMsgs(cmd) {
					if result, ok := msg.(bossui.ControlInvocationResultMsg); ok {
						found = true
						if result.Err == nil {
							t.Fatal("expected failed receipt")
						}
					}
				}
				if !found || started {
					t.Fatal("blocked task ran or lost failure receipt")
				}
			} else {
				assertTaskLaunchSucceeded(t, cmd)
				if !started {
					t.Fatal("worker not started")
				}
			}
			task, err := svc.GetAgentTask(t.Context(), created.task.ID)
			if err != nil {
				t.Fatal(err)
			}
			if dirty && task.Repository.State != "blocked" {
				t.Fatalf("missing visible blocker: %+v", task.Repository)
			}
			manager.CloseProject(task.WorkspacePath)
		})
	}
}
