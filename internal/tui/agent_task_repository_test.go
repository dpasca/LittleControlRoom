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

type repositoryAdmissionTestSession struct {
	*fakeCodexSession
	admission codexapp.TurnAdmission
}

func (s *repositoryAdmissionTestSession) SubmitInput(input codexapp.Submission) error {
	admit := input.TurnAdmission
	if admit == nil {
		admit = func() (func(), error) {
			return s.admission(s.projectPath, s.snapshot.ControlSessionKey)
		}
	}
	unlock, err := admit()
	if err != nil {
		return err
	}
	defer unlock()
	return s.fakeCodexSession.SubmitInput(input)
}

func TestVisibleOperatorMessageContinuesDirtyIsolatedTask(t *testing.T) {
	svc := newControlTestService(t)
	root := t.TempDir()
	for _, args := range [][]string{{"init", "-b", "fixture"}, {"-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "baseline"}} {
		if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git: %v %s", err, out)
		}
	}
	task, err := svc.CreateAgentTask(t.Context(), model.CreateAgentTaskInput{
		Title: "Operator test", OriginProjectPath: root, OriginSessionKey: "caller",
		Repository: model.AgentTaskRepository{Write: true}, Workflow: model.AgentTaskWorkflow{Enabled: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(task.WorkspacePath, "worker.txt")
	if err := os.WriteFile(file, []byte("keep these edits"), 0600); err != nil {
		t.Fatal(err)
	}
	var session *repositoryAdmissionTestSession
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, _ func()) (codexapp.Session, error) {
		session = &repositoryAdmissionTestSession{admission: req.TurnAdmission, fakeCodexSession: &fakeCodexSession{
			projectPath: req.ProjectPath, snapshot: codexapp.Snapshot{ProjectPath: req.ProjectPath,
				Provider: codexapp.ProviderCodex, ThreadID: "worker", ControlSessionKey: req.TodoCaptureSessionKey, Started: true},
		}}
		return session, nil
	})
	t.Cleanup(func() { manager.CloseProject(task.WorkspacePath) })
	svc.ConfigureAgentTaskRepositoryHost(manager.Snapshots, func() []projectrun.Snapshot { return nil })
	manager.SetTurnAdmission(svc.BeginRepositoryTurn)
	if _, _, err := manager.Open(codexapp.LaunchRequest{ProjectPath: task.WorkspacePath}); err != nil {
		t.Fatal(err)
	}
	if err := session.SubmitInput(codexapp.Submission{Text: "automated follow-up"}); err == nil {
		t.Fatal("ordinary submission adopted dirty checkout")
	}
	m := Model{ctx: t.Context(), svc: svc, codexManager: manager, codexVisibleProject: task.WorkspacePath}
	action := m.submitVisibleCodexCmd(codexDraft{Text: "let me test the game"})().(codexActionMsg)
	if action.err != nil || len(session.submitted) != 1 || session.submitted[0] != "let me test the game" {
		t.Fatalf("operator message failed: %+v, submissions %v", action, session.submitted)
	}
	current, err := svc.GetAgentTask(t.Context(), task.ID)
	if err != nil || current.Repository.State != "held" || current.Repository.Error != "" || current.Workflow.Phase != "working" {
		t.Fatalf("operator message did not acquire ownership: %+v, %v", current, err)
	}
	if data, err := os.ReadFile(file); err != nil || string(data) != "keep these edits" {
		t.Fatalf("operator message modified edits: %q, %v", data, err)
	}
}

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
