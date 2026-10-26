package service

import (
	"os"
	"path/filepath"
	"testing"

	"lcroom/internal/codexapp"
	"lcroom/internal/control"
	"lcroom/internal/model"
	"lcroom/internal/projectrun"
)

func submittedTaskCompletionFixture(t *testing.T) (*Service, model.AgentTask, codexapp.Snapshot) {
	t.Helper()
	svc, _, root := repositoryTestFixture(t)
	ctx := t.Context()
	task, err := svc.CreateAgentTask(ctx, model.CreateAgentTaskInput{
		Title: "Claude handoff", Provider: model.SessionSourceClaudeCode,
		OriginProjectPath: root, OriginProvider: model.SessionSourceClaudeCode,
		OriginSessionID: "caller-thread", Repository: model.AgentTaskRepository{Write: true},
		Workflow: model.AgentTaskWorkflow{Enabled: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	startRepositoryTurn(t, svc, task, "worker-key")
	task, err = svc.AttachAgentTaskEngineerSession(ctx, task.ID, task.Provider, "worker-thread")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(task.Repository.Root, "worker.txt"), []byte("review me"), 0600); err != nil {
		t.Fatal(err)
	}
	task, err = svc.store.SubmitAgentTaskResult(ctx, model.AgentTaskActor{
		ProjectPath: task.WorkspacePath, Provider: task.Provider, SessionKey: "worker-key",
	}, control.AgentTaskSubmitResultInput{TaskID: task.ID, RunID: 1, AgentTaskResultPayload: ready("Ready")})
	if err != nil {
		t.Fatal(err)
	}
	return svc, task, codexapp.Snapshot{
		Provider: codexapp.ProviderClaudeCode, ProjectPath: task.WorkspacePath,
		ThreadID: task.SessionID, ControlSessionKey: "worker-key", Started: true,
		LatestTurnStateKnown: true, LatestTurnCompleted: true,
	}
}

func TestStructuredTaskClosedWorkerHandoffRetainsRepositoryEvidence(t *testing.T) {
	svc, task, completion := submittedTaskCompletionFixture(t)
	ctx := t.Context()
	// A result claim without the host's observation must never release a missing
	// worker's lease (including after a host restart).
	if _, err := svc.settleStructuredTask(ctx, task); err == nil {
		t.Fatal("missing worker was implicitly handed off without terminal evidence")
	}
	settled, err := svc.SettleAgentTaskEngineerTurn(ctx, task, completion)
	if err != nil {
		t.Fatal(err)
	}
	if settled.Repository.State != "released" || settled.Repository.HandoffFingerprint == "" ||
		settled.Repository.Changes != "?? worker.txt" || settled.Workflow.Phase != "awaiting_review" {
		t.Fatalf("lost repository evidence: %+v", settled)
	}
	// Duplicate completion notifications preserve the existing handoff and queue
	// exactly one review request to the original caller.
	for range 2 {
		settled, err = svc.SettleAgentTaskEngineerTurn(ctx, task, completion)
		if err != nil {
			t.Fatal(err)
		}
		settled, err = svc.QueueAgentTaskResultCallback(ctx, settled.ID)
		if err != nil {
			t.Fatal(err)
		}
	}
	messages, err := svc.store.ListQueuedEngineerMessages(ctx, 10)
	if err != nil || len(messages) != 1 || messages[0].TargetSessionID != "caller-thread" {
		t.Fatalf("review callbacks: %+v, %v", messages, err)
	}
	history, err := svc.GetAgentTaskResult(ctx, task.ID, 1)
	if err != nil || history["host_repository_evidence"].(*model.AgentTaskRepository).HandoffFingerprint != settled.Repository.HandoffFingerprint {
		t.Fatalf("immutable handoff evidence missing: %+v, %v", history, err)
	}
}

func TestStructuredTaskCompletionRejectsUnverifiedOrSupersededWorker(t *testing.T) {
	for _, condition := range []string{
		"unknown host", "wrong provider", "wrong thread", "wrong workspace", "wrong late control key", "not started",
		"unknown turn", "unfinished turn", "busy completion", "background completion", "elicitation",
		"reconciling", "live busy worker", "live background worker", "replacement worker", "managed process", "new run",
	} {
		t.Run(condition, func(t *testing.T) {
			svc, task, completion := submittedTaskCompletionFixture(t)
			var engineers []codexapp.Snapshot
			var processes []projectrun.Snapshot
			svc.ConfigureAgentTaskRepositoryHost(func() []codexapp.Snapshot { return engineers }, func() []projectrun.Snapshot { return processes })
			switch condition {
			case "unknown host":
				svc.ConfigureAgentTaskRepositoryHost(nil, nil)
			case "wrong provider":
				completion.Provider = codexapp.ProviderCodex
			case "wrong thread":
				completion.ThreadID = "another-thread"
			case "wrong workspace":
				completion.ProjectPath = t.TempDir()
			case "wrong late control key":
				var err error
				task, err = svc.AttachAgentTaskEngineerSession(t.Context(), task.ID, task.Provider, "")
				if err != nil {
					t.Fatal(err)
				}
				completion.ControlSessionKey = "unrelated-worker"
			case "not started":
				completion.Started = false
			case "unknown turn":
				completion.LatestTurnStateKnown = false
			case "unfinished turn":
				completion.LatestTurnCompleted = false
			case "busy completion":
				completion.Busy = true
			case "background completion":
				completion.BackgroundTasks = []codexapp.BackgroundTaskSnapshot{{ID: "pending", Status: "running"}}
			case "elicitation":
				completion.PendingElicitation = &codexapp.ElicitationRequest{}
			case "reconciling":
				completion.Phase = codexapp.SessionPhaseReconciling
			case "live busy worker":
				live := completion
				live.Busy = true
				engineers = []codexapp.Snapshot{live}
			case "live background worker":
				live := completion
				live.BackgroundTasks = []codexapp.BackgroundTaskSnapshot{{ID: "pending", Status: "running"}}
				engineers = []codexapp.Snapshot{live}
			case "replacement worker":
				live := completion
				live.ThreadID = "replacement-thread"
				engineers = []codexapp.Snapshot{live}
			case "managed process":
				processes = []projectrun.Snapshot{{ID: "worker-process", ProjectPath: task.WorkspacePath, CWD: task.Repository.Root, Running: true}}
			case "new run":
				startRepositoryTurn(t, svc, task, "worker-key")
			}
			if _, err := svc.SettleAgentTaskEngineerTurn(t.Context(), task, completion); err == nil {
				t.Fatal("unverified completion released the checkout")
			}
			current, err := svc.GetAgentTask(t.Context(), task.ID)
			if err != nil {
				t.Fatal(err)
			}
			if current.Repository.State != "held" || current.Workflow.Handoff || !current.ResultReadyAt.IsZero() || current.ResultMessageID != "" {
				t.Fatalf("failed handoff mutated ownership or notified the caller: %+v", current)
			}
			if condition == "new run" && (current.Workflow.RunID != 2 || current.Workflow.Phase != "working" || current.Workflow.Result != nil) {
				t.Fatalf("old completion changed the new run: %+v", current.Workflow)
			}
		})
	}
}

func TestStructuredTaskHandoffReloadsLateIdentity(t *testing.T) {
	svc, task, completion := submittedTaskCompletionFixture(t)
	// Model a task-list snapshot captured before the identity write completed.
	stale := task
	stale.SessionID = ""
	completion.ControlSessionKey = "" // Reopened sessions need only their bound provider ID.
	svc.ConfigureAgentTaskRepositoryHost(func() []codexapp.Snapshot { return []codexapp.Snapshot{completion} }, func() []projectrun.Snapshot { return nil })
	settled, err := svc.settleStructuredTask(t.Context(), stale)
	if err != nil || settled.Workflow.Phase != "awaiting_review" {
		t.Fatalf("stale identity caused a spurious unavailable worker: %+v, %v", settled, err)
	}
}

func TestStructuredTaskCompletionPreservesExplicitStop(t *testing.T) {
	for _, caller := range []bool{false, true} {
		t.Run(map[bool]string{false: "worker", true: "caller"}[caller], func(t *testing.T) {
			svc, task, completion := submittedTaskCompletionFixture(t)
			ctx := t.Context()
			if err := svc.store.StopStructuredTask(ctx, task, caller); err != nil {
				t.Fatal(err)
			}
			settled, err := svc.SettleAgentTaskEngineerTurn(ctx, task, completion)
			if err != nil {
				t.Fatal(err)
			}
			settled, err = svc.QueueAgentTaskResultCallback(ctx, settled.ID)
			if err != nil || settled.ResultMessageID != "" || settled.Repository.State != "released" {
				t.Fatalf("stopped session revived: %+v, %v", settled, err)
			}
			if (!caller && settled.Workflow.Phase != "canceled") || (caller && !settled.Workflow.CallerStopped) {
				t.Fatalf("stop was lost: %+v", settled.Workflow)
			}
		})
	}
}

func TestStructuredRepositoryHandoffAndStoppedCaller(t *testing.T) {
	svc, _, root := repositoryTestFixture(t)
	ctx := t.Context()
	task, err := svc.CreateAgentTask(ctx, model.CreateAgentTaskInput{Title: "typed worker", Provider: model.SessionSourceCodex, OriginProjectPath: root, OriginProvider: model.SessionSourceCodex, OriginSessionKey: "caller", OriginSessionID: "caller-thread", Repository: model.AgentTaskRepository{Write: true}, Workflow: model.AgentTaskWorkflow{Enabled: true}})
	if err != nil {
		t.Fatal(err)
	}
	startRepositoryTurn(t, svc, task, "worker")
	task, err = svc.AttachAgentTaskEngineerSession(ctx, task.ID, model.SessionSourceCodex, "worker-thread")
	if err != nil {
		t.Fatal(err)
	}
	snap := codexapp.Snapshot{ProjectPath: task.WorkspacePath, ThreadID: "worker-thread", Busy: true}
	svc.ConfigureAgentTaskRepositoryHost(func() []codexapp.Snapshot { return []codexapp.Snapshot{snap} }, func() []projectrun.Snapshot { return nil })
	if err := os.WriteFile(filepath.Join(task.Repository.Root, "change.txt"), []byte("worker edit"), 0600); err != nil {
		t.Fatal(err)
	}
	task, err = svc.store.SubmitAgentTaskResult(ctx, model.AgentTaskActor{ProjectPath: task.WorkspacePath, Provider: task.Provider, SessionKey: "worker"}, control.AgentTaskSubmitResultInput{TaskID: task.ID, RunID: 1, AgentTaskResultPayload: model.AgentTaskResultPayload{Outcome: "ready_for_review", Summary: "Added change.txt"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.settleStructuredTask(ctx, task); err == nil {
		t.Fatal("active worker handed off")
	}
	snap.Busy = false
	task, err = svc.settleStructuredTask(ctx, task)
	if err != nil {
		t.Fatal(err)
	}
	if task.Repository.State == "held" || task.Repository.Changes == "" || task.Workflow.Phase != "awaiting_review" {
		t.Fatalf("bad handoff: %+v %+v", task.Repository, task.Workflow)
	}
	history, err := svc.GetAgentTaskResult(ctx, task.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if history["host_repository_evidence"].(*model.AgentTaskRepository).State == "held" {
		t.Fatal("captured pre-release evidence")
	}
	task, err = svc.QueueAgentTaskResultCallback(ctx, task.ID)
	if err != nil || task.ResultMessageID == "" {
		t.Fatalf("callback: %+v %v", task, err)
	}
	message, err := svc.store.GetEngineerMessage(ctx, task.ResultMessageID)
	if err != nil {
		t.Fatal(err)
	}
	if message.AgentTaskRevision != 1 {
		t.Fatal("callback has no revision")
	}
	if err := svc.store.ValidateTaskReviewDelivery(ctx, message); err != nil {
		t.Fatal(err)
	}
	if err := svc.StopStructuredTasksForEngineer(ctx, root, model.SessionSourceCodex, "caller", "caller-thread"); err != nil {
		t.Fatal(err)
	}
	if err := svc.store.ValidateTaskReviewDelivery(ctx, message); err == nil {
		t.Fatal("stopped caller revived")
	}
	if _, err := svc.CompleteAgentTask(ctx, task.ID, "bypass"); err == nil {
		t.Fatal("legacy close bypassed review")
	}
}

func TestStructuredTaskFollowUpAfterResultWithBackgroundCommand(t *testing.T) {
	svc, _, _ := repositoryTestFixture(t)
	ctx := t.Context()
	task, err := svc.CreateAgentTask(ctx, model.CreateAgentTaskInput{
		Title: "Background command worker", WorkspacePath: t.TempDir(),
		Provider: model.SessionSourceClaudeCode, Workflow: model.AgentTaskWorkflow{Enabled: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	startRepositoryTurn(t, svc, task, "worker")
	svc.ConfigureAgentTaskRepositoryHost(func() []codexapp.Snapshot {
		return []codexapp.Snapshot{{
			ProjectPath: task.WorkspacePath, Provider: codexapp.ProviderClaudeCode,
			Busy: true, ParentAwaitingBackgroundTasks: true, BackgroundInputSupported: true,
			BackgroundTasks: []codexapp.BackgroundTaskSnapshot{{ID: "ios-exit", Tool: "Bash", Status: "running"}},
		}}
	}, func() []projectrun.Snapshot { return nil })
	// Queueing during an unfinished run retains that run's identity.
	startRepositoryTurn(t, svc, task, "worker")
	claims := control.AgentTaskSubmitResultInput{TaskID: task.ID, RunID: 1,
		AgentTaskResultPayload: model.AgentTaskResultPayload{Outcome: "ready_for_review", Summary: "Game ready to test"}}
	task, err = svc.store.SubmitAgentTaskResult(ctx, model.AgentTaskActor{
		ProjectPath: task.WorkspacePath, Provider: task.Provider, SessionKey: "worker",
	}, claims)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.settleStructuredTask(ctx, task); err == nil {
		t.Fatal("background work must retain ownership before a follow-up")
	}
	if unlock, err := svc.BeginRepositoryTurn(task.WorkspacePath, "other-worker"); err == nil {
		unlock()
		t.Fatal("unrelated session superseded the active worker")
	}
	// The submitted result must not block the original session's follow-up.
	startRepositoryTurn(t, svc, task, "worker")
	current, err := svc.GetAgentTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Workflow.RunID != 2 || current.Workflow.Phase != "working" || current.Workflow.Result != nil || current.Workflow.Handoff {
		t.Fatalf("follow-up did not start a fresh result run: %+v", current.Workflow)
	}
	history, err := svc.GetAgentTaskResult(ctx, task.ID, 1)
	if err != nil || history["worker_claims"].(model.AgentTaskResult).Summary != claims.Summary {
		t.Fatalf("previous claims were lost: %+v %v", history, err)
	}
}
