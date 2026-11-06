package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	bossui "lcroom/internal/boss"
	"lcroom/internal/codexapp"
	"lcroom/internal/control"
	"lcroom/internal/events"
	"lcroom/internal/model"
	"lcroom/internal/projectrun"
	"lcroom/internal/service"
	"lcroom/internal/store"
)

func TestCloseActiveLCAgentTaskReportsFailureWithoutCancelingRun(t *testing.T) {
	ctx := t.Context()
	svc := newControlTestService(t)
	task, err := svc.CreateAgentTask(ctx, model.CreateAgentTaskInput{
		Title: "Active copy worker", Provider: model.SessionSourceLCAgent,
		Workflow: model.AgentTaskWorkflow{Enabled: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	unlock, err := svc.BeginRepositoryTurn(task.WorkspacePath, "host-key")
	if err != nil {
		t.Fatal(err)
	}
	unlock()
	task, err = svc.AttachAgentTaskEngineerSession(ctx, task.ID, task.Provider, "lct_copy_worker")
	if err != nil {
		t.Fatal(err)
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, _ func()) (codexapp.Session, error) {
		return &fakeCodexSession{projectPath: req.ProjectPath, snapshot: codexapp.Snapshot{
			Provider: codexapp.ProviderLCAgent, ProjectPath: req.ProjectPath, ThreadID: task.SessionID,
			ControlSessionKey: "host-key", Started: true, Busy: true,
		}}, nil
	})
	defer manager.CloseAll()
	if _, _, err := manager.Open(codexapp.LaunchRequest{ProjectPath: task.WorkspacePath, Provider: codexapp.ProviderLCAgent}); err != nil {
		t.Fatal(err)
	}
	m := Model{ctx: ctx, svc: svc, codexManager: manager, openAgentTasks: []model.AgentTask{task}}
	input := control.AgentTaskCloseInput{TaskID: task.ID, Status: control.AgentTaskCloseWaiting, CloseSession: true}
	inv := controlInvocationRawForTest(t, control.CapabilityAgentTaskClose, input)
	outcome := m.executeAgentTaskCloseControlWithOutcome(inv, input)
	loaded := outcome.cmd().(bossAgentTaskCloseLoadedMsg)
	_, cmd := outcome.model.applyBossAgentTaskCloseLoaded(loaded)
	var receipt *bossui.ControlInvocationResultMsg
	for _, msg := range collectCmdMsgs(cmd) {
		if result, ok := msg.(bossui.ControlInvocationResultMsg); ok {
			receipt = &result
		}
	}
	if receipt == nil || receipt.Err == nil || !strings.Contains(receipt.Status, "active embedded LCAgent session") {
		t.Fatalf("missing close refusal: %+v", receipt)
	}
	current, err := svc.GetAgentTask(ctx, task.ID)
	if err != nil || current.Workflow.Phase != "working" || current.Workflow.SupervisionRevoked || current.Workflow.RunID != task.Workflow.RunID {
		t.Fatalf("refused close modified the active run: %+v, %v", current.Workflow, err)
	}
}

// Exercise the same continuation handlers used by agent_task.continue after
// discarding the old service, session registry, and database connection.
func TestLCAgentRepositoryTaskContinueAfterRestartPreservesEdits(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "")
	for _, changeModel := range []bool{false, true} {
		name := "same-model"
		if changeModel {
			name = "changed-model"
		}
		t.Run(name, func(t *testing.T) {
			ctx := t.Context()
			svc := newControlTestService(t)
			svc.ConfigureAgentTaskRepositoryHost(func() []codexapp.Snapshot { return nil }, func() []projectrun.Snapshot { return nil })
			root := t.TempDir()
			git := func(path string, args ...string) string {
				t.Helper()
				out, err := exec.Command("git", append([]string{"-C", path}, args...)...).CombinedOutput()
				if err != nil {
					t.Fatalf("git %v: %v %s", args, err, out)
				}
				return string(out)
			}
			git(root, "init", "-b", "fixture")
			if err := os.WriteFile(filepath.Join(root, "copy.txt"), []byte("approved baseline"), 0600); err != nil {
				t.Fatal(err)
			}
			git(root, "add", "copy.txt")
			git(root, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-m", "baseline")
			selection := model.AgentTaskModelSelection{Provider: model.SessionSourceLCAgent, Model: "deepseek-v4-pro", ModelProvider: "deepseek", ReasoningEffort: "max"}
			task, err := svc.CreateAgentTask(ctx, model.CreateAgentTaskInput{
				Title: "Store copy", Provider: model.SessionSourceLCAgent, OriginProjectPath: root,
				OriginProvider: model.SessionSourceCodex, OriginSessionKey: "caller", OriginSessionID: "caller-thread",
				Repository: model.AgentTaskRepository{Write: true}, Workflow: model.AgentTaskWorkflow{Enabled: true}, ModelSelection: selection,
			})
			if err != nil {
				t.Fatal(err)
			}
			unlock, err := svc.BeginRepositoryTurn(task.WorkspacePath, "host-launch-key")
			if err != nil {
				t.Fatal(err)
			}
			unlock()
			task, err = svc.AttachAgentTaskEngineerSession(ctx, task.ID, model.SessionSourceLCAgent, "lct_copy_worker")
			if err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(task.WorkspacePath, "copy.txt")
			if err := os.WriteFile(file, []byte("staged adaptation"), 0600); err != nil {
				t.Fatal(err)
			}
			git(task.WorkspacePath, "add", "copy.txt")
			if err := os.WriteFile(file, []byte("unstaged adaptation"), 0600); err != nil {
				t.Fatal(err)
			}
			note := filepath.Join(task.WorkspacePath, "review.md")
			if err := os.WriteFile(note, []byte("untracked review"), 0600); err != nil {
				t.Fatal(err)
			}
			snapshot := codexapp.Snapshot{Provider: codexapp.ProviderLCAgent, ProjectPath: task.WorkspacePath,
				ThreadID: task.SessionID, ControlSessionKey: "host-launch-key", Started: true, Phase: codexapp.SessionPhaseIdle,
				LatestTurnStateKnown: true, LatestTurnCompleted: true,
			}
			svc.ConfigureAgentTaskRepositoryHost(func() []codexapp.Snapshot { return []codexapp.Snapshot{snapshot} }, func() []projectrun.Snapshot { return nil })
			if err := svc.Store().StopStructuredTask(ctx, task, false); err != nil {
				t.Fatal(err)
			}
			task, err = svc.MarkAgentTaskReadyForReview(ctx, task.ID, "stopped")
			if err != nil {
				t.Fatal(err)
			}
			if !task.Workflow.Handoff || task.Repository.State != "released" {
				t.Fatalf("no stopped handoff: %+v", task)
			}
			if changeModel {
				selection.Model = "deepseek-flash"
				task, err = svc.Store().UpdateAgentTask(ctx, model.UpdateAgentTaskInput{ID: task.ID, ModelSelection: &selection})
				if err != nil {
					t.Fatal(err)
				}
			}
			statusBefore := git(task.WorkspacePath, "status", "--porcelain")
			indexBefore := git(task.WorkspacePath, "diff", "--cached")
			cfg := svc.Config()
			if err := svc.Store().Close(); err != nil {
				t.Fatal(err)
			}
			st, err := store.Open(cfg.DBPath)
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			svc = service.New(cfg, st, events.NewBus(), nil)
			catalog := control.EngineerModelCatalog{Provider: control.ProviderLCAgent, Source: "test", ObservedAt: time.Now(), Models: []control.EngineerModel{{Model: selection.Model, ModelProvider: "deepseek", ReasoningEfforts: []string{"max"}}}}
			if err := st.SaveEngineerModelCatalog(ctx, catalog); err != nil {
				t.Fatal(err)
			}
			var request codexapp.LaunchRequest
			manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, _ func()) (codexapp.Session, error) {
				request = req
				current, err := svc.GetAgentTask(ctx, task.ID)
				if err != nil || current.Repository.State != "held" {
					t.Fatalf("worker launched before durable ownership: %+v %v", current, err)
				}
				return &repositoryAdmissionTestSession{admission: req.TurnAdmission, fakeCodexSession: &fakeCodexSession{projectPath: req.ProjectPath, snapshot: codexapp.Snapshot{
					Provider: codexapp.ProviderLCAgent, ProjectPath: req.ProjectPath, ThreadID: task.SessionID, ControlSessionKey: req.TodoCaptureSessionKey, Started: true, Phase: codexapp.SessionPhaseIdle,
				}}}, nil
			})
			defer manager.CloseAll()
			svc.ConfigureAgentTaskRepositoryHost(manager.Snapshots, func() []projectrun.Snapshot { return nil })
			manager.SetTurnAdmission(svc.BeginRepositoryTurn)
			m := Model{ctx: ctx, svc: svc, codexManager: manager, openAgentTasks: []model.AgentTask{task}}
			input := control.AgentTaskContinueInput{TaskID: task.ID, Provider: control.ProviderLCAgent, SessionMode: control.SessionModeResumeOrNew, Prompt: "Review all locales and preserve existing edits"}
			inv := controlInvocationRawForTest(t, control.CapabilityAgentTaskContinue, input)
			outcome := m.executeAgentTaskContinueControlWithOutcome(inv, input)
			loaded, ok := outcome.cmd().(bossAgentTaskContinueLoadedMsg)
			if !ok || loaded.err != nil {
				t.Fatalf("load continuation: %+v", loaded)
			}
			_, cmd := outcome.model.applyBossAgentTaskContinueLoaded(loaded)
			assertTaskLaunchSucceeded(t, cmd)
			if request.ResumeID != task.SessionID || request.ForceNew || request.PendingModel != selection.Model || request.PendingReasoning != "max" || request.LCAgentProvider != "deepseek" {
				t.Fatalf("resume lost thread or saved selection: %+v", request)
			}
			current, err := svc.GetAgentTask(ctx, task.ID)
			if err != nil || current.Workflow.RunID != task.Workflow.RunID+1 || current.Workflow.WorkerKey != "host-launch-key" || current.Repository.State != "held" {
				t.Fatalf("wrong resumed run: %+v %v", current, err)
			}
			if git(task.WorkspacePath, "status", "--porcelain") != statusBefore || git(task.WorkspacePath, "diff", "--cached") != indexBefore {
				t.Fatal("resume modified status or staged edits")
			}
			for path, want := range map[string]string{file: "unstaged adaptation", note: "untracked review"} {
				data, err := os.ReadFile(path)
				if err != nil || string(data) != want {
					t.Fatalf("resume changed %s: %q %v", path, data, err)
				}
			}
		})
	}
}
