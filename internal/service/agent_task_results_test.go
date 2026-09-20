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
	if err := os.WriteFile(filepath.Join(root, "change.txt"), []byte("worker edit"), 0600); err != nil {
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
