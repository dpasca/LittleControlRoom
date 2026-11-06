package service

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lcroom/internal/codexapp"
	"lcroom/internal/model"
)

// The launch-scoped host key recorded when a fresh LCAgent task started. It is
// minted by the launch path and cannot be re-derived after a restart.
const lcagentLaunchKey = "dmns6g8yo8s0-fe7c4aa1facd1568"

// The stable LCAgent logical thread announced after that launch. A resumed
// session presents this id as its control key.
const lcagentThreadID = "lct_a6987025baf38a0f59f9a7f4"

// stoppedLCAgentHandoff mirrors the reported lifecycle: a fresh launch records
// the launch-scoped key, LCAgent announces its thread, the worker edits the
// checkout, the turn is stopped without a structured result, and the host
// releases the lease with a handoff fingerprint.
func stoppedLCAgentHandoff(t *testing.T) (*correctionFixture, model.AgentTask) {
	t.Helper()
	ctx := t.Context()
	f := newCorrectionFixture(t)
	startRepositoryTurn(t, f.svc, f.task, lcagentLaunchKey)
	task, err := f.svc.AttachAgentTaskEngineerSession(ctx, f.task.ID, model.SessionSourceLCAgent, lcagentThreadID)
	if err != nil {
		t.Fatal(err)
	}
	// The host must be able to verify the stopped LCAgent worker before it can
	// settle the handoff; register the idle thread that announced itself.
	f.snapshot.Provider = codexapp.ProviderLCAgent
	f.snapshot.ThreadID = lcagentThreadID
	f.snapshot.ControlSessionKey = lcagentLaunchKey
	f.snapshot.Started = true
	f.snapshot.Phase = codexapp.SessionPhaseIdle
	if err := os.WriteFile(filepath.Join(task.Repository.Root, "store-copy.txt"), []byte("approved copy"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.store.StopStructuredTask(ctx, task, false); err != nil {
		t.Fatal(err)
	}
	if task, err = f.svc.GetAgentTask(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	if task, err = f.svc.settleStructuredTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	f.task = task
	return f, task
}

func requireReleasedLCAgentHandoff(t *testing.T, task model.AgentTask) {
	t.Helper()
	if task.Repository.State != "released" || !task.Workflow.Handoff || task.Repository.HandoffFingerprint == "" {
		t.Fatalf("fixture did not produce a released handoff: repository=%+v workflow=%+v", task.Repository, task.Workflow)
	}
	if task.Workflow.WorkerKey != lcagentLaunchKey {
		t.Fatalf("worker key = %q, want the launch-scoped key %q", task.Workflow.WorkerKey, lcagentLaunchKey)
	}
}

// A stopped LCAgent task whose checkout is byte-identical to its recorded
// handoff must resume on its own thread, including after a model override.
func TestStoppedLCAgentTaskResumesItsRecordedThreadAfterRestart(t *testing.T) {
	ctx := t.Context()
	f, task := stoppedLCAgentHandoff(t)
	requireReleasedLCAgentHandoff(t, task)
	selection := model.AgentTaskModelSelection{
		Provider:        model.SessionSourceLCAgent,
		Model:           "deepseek-flash",
		ModelProvider:   "deepseek",
		ReasoningEffort: "max",
	}
	updated, err := f.svc.store.UpdateAgentTask(ctx, model.UpdateAgentTaskInput{ID: task.ID, ModelSelection: &selection})
	if err != nil {
		t.Fatal(err)
	}
	if updated.ModelSelection.Model != "deepseek-flash" {
		t.Fatalf("model override not recorded: %+v", updated.ModelSelection)
	}
	unlock, err := f.svc.BeginRepositoryTurn(updated.WorkspacePath, lcagentThreadID)
	if err != nil {
		t.Fatalf("resuming the task's own unchanged handoff failed: %v", err)
	}
	unlock()
	current, err := f.svc.GetAgentTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Repository.State != "held" || current.Workflow.WorkerKey != lcagentThreadID {
		t.Fatalf("resume did not rebind the worker to its thread: repository=%+v workflow=%+v", current.Repository, current.Workflow)
	}
	if current.Workflow.RunID != task.Workflow.RunID+1 || current.Workflow.Phase != "working" {
		t.Fatalf("resume did not start the next run: %+v", current.Workflow)
	}
	if current.ModelSelection.Model != "deepseek-flash" || current.ModelSelection.ModelProvider != "deepseek" || current.ModelSelection.ReasoningEffort != "max" {
		t.Fatalf("resume changed the model override: %+v", current.ModelSelection)
	}
	data, err := os.ReadFile(filepath.Join(current.Repository.Root, "store-copy.txt"))
	if err != nil || string(data) != "approved copy" {
		t.Fatalf("resume changed the worker edits: %q, %v", data, err)
	}
}

// Any intervening edit still fails closed with the specific changed-checkout
// message instead of being adopted.
func TestStoppedLCAgentTaskRejectsChangedCheckout(t *testing.T) {
	f, task := stoppedLCAgentHandoff(t)
	requireReleasedLCAgentHandoff(t, task)
	file := filepath.Join(task.Repository.Root, "store-copy.txt")
	if err := os.WriteFile(file, []byte("intervening edit"), 0600); err != nil {
		t.Fatal(err)
	}
	unlock, err := f.svc.BeginRepositoryTurn(task.WorkspacePath, lcagentThreadID)
	if err == nil {
		unlock()
		t.Fatal("accepted a checkout changed after the handoff")
	}
	if !strings.Contains(err.Error(), "checkout changed since the unfinished task stopped") {
		t.Fatalf("unexpected error for an altered handoff: %v", err)
	}
	if data, readErr := os.ReadFile(file); readErr != nil || string(data) != "intervening edit" {
		t.Fatalf("rejection changed the intervening edit: %q, %v", data, readErr)
	}
}

// Only the task's own LCAgent thread is a second identity for its launch key.
// Another thread, or the same id on a non-LCAgent task, still fails closed.
func TestStoppedLCAgentTaskRejectsAnotherWorker(t *testing.T) {
	ctx := t.Context()
	f, task := stoppedLCAgentHandoff(t)
	requireReleasedLCAgentHandoff(t, task)
	if unlock, err := f.svc.BeginRepositoryTurn(task.WorkspacePath, "lct_other_worker"); err == nil {
		unlock()
		t.Fatal("accepted another LCAgent thread as the task worker")
	} else if !strings.Contains(err.Error(), "clean checkout") {
		t.Fatalf("unexpected error for another thread: %v", err)
	}
	provider := model.SessionSourceCodex
	sessionID := lcagentThreadID
	if _, err := f.svc.store.UpdateAgentTask(ctx, model.UpdateAgentTaskInput{ID: task.ID, Provider: &provider, SessionID: &sessionID}); err != nil {
		t.Fatal(err)
	}
	if unlock, err := f.svc.BeginRepositoryTurn(task.WorkspacePath, lcagentThreadID); err == nil {
		unlock()
		t.Fatal("accepted a provider mismatch as the same worker")
	}
}

// A live lease remains owned by the key that took it: the provider thread
// cannot take over an active task's checkout.
func TestActiveLCAgentTaskLeaseStillBlocksItsThread(t *testing.T) {
	ctx := t.Context()
	f := newCorrectionFixture(t)
	startRepositoryTurn(t, f.svc, f.task, lcagentLaunchKey)
	task, err := f.svc.AttachAgentTaskEngineerSession(ctx, f.task.ID, model.SessionSourceLCAgent, lcagentThreadID)
	if err != nil {
		t.Fatal(err)
	}
	unlock, err := f.svc.BeginRepositoryTurn(task.WorkspacePath, lcagentThreadID)
	if err == nil {
		unlock()
		t.Fatal("a live lease was taken over by the provider thread")
	}
	current, err := f.svc.GetAgentTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Repository.State != "held" || current.Repository.SessionKey != lcagentLaunchKey {
		t.Fatalf("active lease changed: %+v", current.Repository)
	}
}

// The supported recovery without a host patch: an operator follow-up submitted
// from the task's own pane admits the isolated task's existing edits, clears the
// blocked preflight state, and rebinds the worker to the LCAgent thread so later
// resumes match.
func TestOperatorPaneFollowUpRecoversBlockedLCAgentHandoff(t *testing.T) {
	ctx := t.Context()
	f, task := stoppedLCAgentHandoff(t)
	requireReleasedLCAgentHandoff(t, task)
	f.svc.recordRepositoryProblem(ctx, task, errors.New("repository write preflight requires a clean checkout; preserve and resolve existing edits before continuing"))
	blocked, err := f.svc.GetAgentTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if blocked.Repository.State != "blocked" || blocked.Repository.Error == "" {
		t.Fatalf("fixture did not reproduce the blocked checkout: %+v", blocked.Repository)
	}
	unlock, err := f.svc.BeginOperatorRepositoryTurn(blocked.WorkspacePath, lcagentThreadID)
	if err != nil {
		t.Fatalf("operator follow-up could not recover the blocked checkout: %v", err)
	}
	unlock()
	recovered, err := f.svc.GetAgentTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Repository.State != "held" || recovered.Repository.SessionKey != lcagentThreadID || recovered.Repository.Error != "" {
		t.Fatalf("operator follow-up did not reacquire the checkout: %+v", recovered.Repository)
	}
	if recovered.Workflow.WorkerKey != lcagentThreadID {
		t.Fatalf("operator follow-up did not rebind the worker: %+v", recovered.Workflow)
	}
	if data, readErr := os.ReadFile(filepath.Join(recovered.Repository.Root, "store-copy.txt")); readErr != nil || string(data) != "approved copy" {
		t.Fatalf("operator follow-up changed the worker edits: %q, %v", data, readErr)
	}
	// After the next stop the same thread resumes without a launch-scoped key.
	if err := f.svc.store.StopStructuredTask(ctx, recovered, false); err != nil {
		t.Fatal(err)
	}
	if recovered, err = f.svc.GetAgentTask(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	if recovered, err = f.svc.settleStructuredTask(ctx, recovered); err != nil {
		t.Fatal(err)
	}
	unlock, err = f.svc.BeginRepositoryTurn(recovered.WorkspacePath, lcagentThreadID)
	if err != nil {
		t.Fatalf("resume after the operator recovery failed: %v", err)
	}
	unlock()
}

// Closing the idle task as waiting is safe for the edits but does not restore
// the worker identity or clear the blocked checkout, so it cannot be the step
// that makes a resume eligible.
func TestClosingLCAgentTaskAsWaitingDoesNotRestoreResume(t *testing.T) {
	ctx := t.Context()
	f, task := stoppedLCAgentHandoff(t)
	requireReleasedLCAgentHandoff(t, task)
	f.svc.recordRepositoryProblem(ctx, task, errors.New("repository write preflight requires a clean checkout; preserve and resolve existing edits before continuing"))
	blocked, err := f.svc.GetAgentTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.svc.store.StopStructuredTask(ctx, blocked, false); err != nil {
		t.Fatal(err)
	}
	status := model.AgentTaskStatusWaiting
	summary := "waiting for the operator"
	closed, err := f.svc.store.UpdateAgentTask(ctx, model.UpdateAgentTaskInput{ID: task.ID, Status: &status, Summary: &summary})
	if err != nil {
		t.Fatal(err)
	}
	if closed.Repository.State != "blocked" || closed.Repository.Error == "" {
		t.Fatalf("closing as waiting silently cleared the blocked checkout: %+v", closed.Repository)
	}
	if closed.Workflow.WorkerKey != lcagentLaunchKey {
		t.Fatalf("closing as waiting rewrote the recorded worker: %+v", closed.Workflow)
	}
	if closed.Workflow.Phase != "canceled" || !closed.Workflow.SupervisionRevoked {
		t.Fatalf("closing as waiting did not cancel the run: %+v", closed.Workflow)
	}
	if data, readErr := os.ReadFile(filepath.Join(closed.Repository.Root, "store-copy.txt")); readErr != nil || string(data) != "approved copy" {
		t.Fatalf("closing as waiting changed the worker edits: %q, %v", data, readErr)
	}
}
