package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"lcroom/internal/codexapp"
	"lcroom/internal/control"
	"lcroom/internal/model"
	"lcroom/internal/projectrun"
)

func followupTaskFixture(t *testing.T) (Model, model.AgentTask, string) {
	t.Helper()
	svc := newControlTestService(t)
	root := t.TempDir()
	for _, args := range [][]string{{"init", "-b", "fixture"}, {"-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "baseline"}} {
		if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git: %v %s", err, out)
		}
	}
	task, err := svc.CreateAgentTask(t.Context(), model.CreateAgentTaskInput{Title: "Copy worker", Provider: model.SessionSourceCodex, OriginProjectPath: root, OriginProvider: model.SessionSourceCodex, OriginSessionKey: "caller", OriginSessionID: "caller-thread", Repository: model.AgentTaskRepository{Write: true}, Workflow: model.AgentTaskWorkflow{Enabled: true}})
	if err != nil {
		t.Fatal(err)
	}
	svc.ConfigureAgentTaskRepositoryHost(func() []codexapp.Snapshot { return nil }, func() []projectrun.Snapshot { return nil })
	return Model{ctx: t.Context(), svc: svc, openAgentTasks: []model.AgentTask{task}}, task, root
}

func TestTaskContinueAfterAcceptanceStartsNewRunAndRetainsReview(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(map[bool]string{false: "reopened", true: "existing session"}[existing], func(t *testing.T) {
			m, task, _ := followupTaskFixture(t)
			svc := m.svc
			ctx := t.Context()
			unlock, err := svc.BeginRepositoryTurn(task.WorkspacePath, "original-key")
			if err != nil {
				t.Fatal(err)
			}
			unlock()
			task, err = svc.AttachAgentTaskEngineerSession(ctx, task.ID, task.Provider, "worker-thread")
			if err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(task.WorkspacePath, "copy.txt")
			if err := os.WriteFile(file, []byte("accepted copy"), 0600); err != nil {
				t.Fatal(err)
			}
			snapshot := codexapp.Snapshot{ProjectPath: task.WorkspacePath, Provider: codexapp.ProviderCodex, ThreadID: "worker-thread", ControlSessionKey: "original-key", Started: true, Phase: codexapp.SessionPhaseIdle, LatestTurnStateKnown: true, LatestTurnCompleted: true}
			svc.ConfigureAgentTaskRepositoryHost(func() []codexapp.Snapshot { return []codexapp.Snapshot{snapshot} }, func() []projectrun.Snapshot { return nil })
			task, err = svc.Store().SubmitAgentTaskResult(ctx, model.AgentTaskActor{ProjectPath: task.WorkspacePath, Provider: task.Provider, SessionKey: "original-key"}, control.AgentTaskSubmitResultInput{TaskID: task.ID, RunID: 1, AgentTaskResultPayload: model.AgentTaskResultPayload{Outcome: "ready_for_review", Summary: "copy ready"}})
			if err != nil {
				t.Fatal(err)
			}
			task, err = svc.SettleAgentTaskEngineerTurn(ctx, task, snapshot)
			if err != nil {
				t.Fatal(err)
			}
			task, err = svc.Store().ReviewAgentTaskResult(ctx, model.AgentTaskActor{Operator: true}, control.AgentTaskReviewResultInput{TaskID: task.ID, Revision: 1, Decision: "accept", Summary: "approved"})
			if err != nil {
				t.Fatal(err)
			}
			before, err := svc.GetAgentTaskResult(ctx, task.ID, 1)
			if err != nil {
				t.Fatal(err)
			}
			var worker *repositoryAdmissionTestSession
			manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, _ func()) (codexapp.Session, error) {
				key := req.TodoCaptureSessionKey
				if existing {
					key = "original-key"
				}
				worker = &repositoryAdmissionTestSession{fakeCodexSession: &fakeCodexSession{projectPath: req.ProjectPath, snapshot: codexapp.Snapshot{ProjectPath: req.ProjectPath, Provider: req.Provider, ThreadID: "worker-thread", ControlSessionKey: key, Started: true, Phase: codexapp.SessionPhaseIdle}}, admission: req.TurnAdmission}
				return worker, nil
			})
			defer manager.CloseAll()
			manager.SetTurnAdmission(svc.BeginRepositoryTurn)
			if existing {
				if _, _, err := manager.Open(codexapp.LaunchRequest{ProjectPath: task.WorkspacePath, Provider: codexapp.ProviderCodex, ResumeID: "worker-thread", TodoCaptureSessionKey: "original-key"}); err != nil {
					t.Fatal(err)
				}
			}
			svc.ConfigureAgentTaskRepositoryHost(manager.Snapshots, func() []projectrun.Snapshot { return nil })
			m.codexManager = manager
			m.openAgentTasks = []model.AgentTask{task}
			input := control.AgentTaskContinueInput{TaskID: task.ID, Provider: control.ProviderCodex, SessionMode: control.SessionModeResumeOrNew, Prompt: "Apply the newly approved wording"}
			inv := controlInvocationRawForTest(t, control.CapabilityAgentTaskContinue, input)
			outcome := m.executeAgentTaskContinueControlWithOutcome(inv, input)
			loaded := outcome.cmd().(bossAgentTaskContinueLoadedMsg)
			if loaded.err != nil {
				t.Fatal(loaded.err)
			}
			_, cmd := outcome.model.applyBossAgentTaskContinueLoaded(loaded)
			assertTaskLaunchSucceeded(t, cmd)
			current, err := svc.GetAgentTask(ctx, task.ID)
			if err != nil || current.Workflow.RunID != 2 || current.Repository.State != "held" || !current.Workflow.SupervisionRevoked {
				t.Fatalf("followup did not start: %+v %v", current, err)
			}
			after, err := svc.GetAgentTaskResult(ctx, task.ID, 1)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("accepted review overwritten: %+v %v", after, err)
			}
			if data, err := os.ReadFile(file); err != nil || string(data) != "accepted copy" {
				t.Fatalf("edits changed: %q %v", data, err)
			}
		})
	}
}

func TestExplicitTaskStopWaitsForLaunchingWorkerAndGatesReplacement(t *testing.T) {
	m, stale, root := followupTaskFixture(t)
	entered, release := make(chan struct{}), make(chan struct{})
	source := &fakeCodexSession{projectPath: stale.WorkspacePath, snapshot: codexapp.Snapshot{ProjectPath: stale.WorkspacePath, Provider: codexapp.ProviderCodex, ThreadID: "worker-thread", ControlSessionKey: "launch-key", Started: true, Busy: true}}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, _ func()) (codexapp.Session, error) {
		close(entered)
		<-release
		if err := os.WriteFile(filepath.Join(req.ProjectPath, "copy.txt"), []byte("unfinished copy"), 0600); err != nil {
			return nil, err
		}
		return source, nil
	})
	defer manager.CloseAll()
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	manager.SetTurnAdmission(confirmedTaskTurnAdmission(m.svc, stale, false))
	m.codexManager = manager
	m.svc.ConfigureAgentTaskRepositoryHost(manager.Snapshots, func() []projectrun.Snapshot { return nil })
	replacement := control.AgentTaskCreateInput{Title: "Codex replacement", Kind: control.AgentTaskKindAgent, Provider: control.ProviderCodex, AfterTaskStopped: stale.ID, Resources: []control.ResourceRef{{Kind: control.ResourceProject, ProjectPath: root}}}
	if msg := m.createBossAgentTaskCmd(control.Invocation{}, replacement, codexapp.ProviderCodex)().(bossAgentTaskCreatedMsg); msg.err == nil {
		t.Fatal("replacement created before stop")
	}
	launchDone := make(chan error, 1)
	go func() {
		_, _, err := manager.Open(codexapp.LaunchRequest{ProjectPath: stale.WorkspacePath, Provider: codexapp.ProviderCodex, TodoCaptureSessionKey: "launch-key", Prompt: "Start work"})
		launchDone <- err
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("launch did not acquire ownership")
	}
	stop := control.AgentTaskCloseInput{TaskID: stale.ID, Status: control.AgentTaskCloseWaiting, StopSession: true, CloseSession: true, Summary: "User selected a different worker"}
	stopped := make(chan bossAgentTaskClosedMsg, 1)
	go func() {
		stopped <- m.closeBossAgentTaskCmd(control.Invocation{}, stop, stale)().(bossAgentTaskClosedMsg)
	}()
	select {
	case result := <-stopped:
		t.Fatalf("stop bypassed pending launch: %+v", result)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if err := <-launchDone; err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-stopped:
		if result.err != nil {
			t.Fatal(result.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("stop did not finish")
	}
	if !source.interrupted || !source.snapshot.Closed {
		t.Fatalf("stop was only metadata: interrupted=%t closed=%t", source.interrupted, source.snapshot.Closed)
	}
	current, err := m.svc.GetAgentTask(t.Context(), stale.ID)
	if err != nil || current.Repository.State != "released" || current.Workflow.Phase != "canceled" || !current.Workflow.Handoff || current.Status != model.AgentTaskStatusWaiting {
		t.Fatalf("stop lost launch race: %+v %v", current, err)
	}
	created := m.createBossAgentTaskCmd(control.Invocation{}, replacement, codexapp.ProviderCodex)().(bossAgentTaskCreatedMsg)
	if created.err != nil || created.task.ID == "" {
		t.Fatalf("replacement refused after confirmed stop: %+v", created)
	}
	if data, err := os.ReadFile(filepath.Join(stale.WorkspacePath, "copy.txt")); err != nil || string(data) != "unfinished copy" {
		t.Fatalf("stop lost worker edits: %q %v", data, err)
	}
	unlock, err := m.svc.BeginConfirmedTaskTurn(current, "launch-key", true)
	if err != nil {
		t.Fatalf("stopped worker cannot resume its preserved edits: %v", err)
	}
	unlock()
}

func TestMetadataCloseCannotCancelAnActiveWorker(t *testing.T) {
	m, task, _ := followupTaskFixture(t)
	source := &fakeCodexSession{projectPath: task.WorkspacePath, snapshot: codexapp.Snapshot{Provider: codexapp.ProviderCodex, ThreadID: "worker", Started: true, Busy: true}}
	manager := codexapp.NewManagerWithFactory(func(codexapp.LaunchRequest, func()) (codexapp.Session, error) { return source, nil })
	defer manager.CloseAll()
	if _, _, err := manager.Open(codexapp.LaunchRequest{ProjectPath: task.WorkspacePath, Provider: codexapp.ProviderCodex}); err != nil {
		t.Fatal(err)
	}
	m.codexManager = manager
	result := m.closeBossAgentTaskCmd(control.Invocation{}, control.AgentTaskCloseInput{TaskID: task.ID, Status: control.AgentTaskCloseWaiting}, task)().(bossAgentTaskClosedMsg)
	if result.err == nil {
		t.Fatal("metadata close claimed to stop active worker")
	}
	current, err := m.svc.GetAgentTask(t.Context(), task.ID)
	if err != nil || !reflect.DeepEqual(current.Workflow, task.Workflow) || source.interrupted {
		t.Fatalf("refused close mutated worker: %+v %v", current, err)
	}
}
