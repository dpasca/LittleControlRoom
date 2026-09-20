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

// correctionFixture drives one structured worker run to a released handoff so
// tests can exercise reacquisition over the exact reviewed dirty checkout.
type correctionFixture struct {
	svc      *Service
	task     model.AgentTask
	root     string
	snapshot codexapp.Snapshot
}

func newCorrectionFixture(t *testing.T) *correctionFixture {
	t.Helper()
	svc, _, root := repositoryTestFixture(t)
	ctx := t.Context()
	task, err := svc.CreateAgentTask(ctx, model.CreateAgentTaskInput{Title: "typed worker", Provider: model.SessionSourceCodex, OriginProjectPath: root, OriginProvider: model.SessionSourceCodex, OriginSessionKey: "caller", OriginSessionID: "caller-thread", Repository: model.AgentTaskRepository{Write: true}, Workflow: model.AgentTaskWorkflow{Enabled: true}})
	if err != nil {
		t.Fatal(err)
	}
	fixture := &correctionFixture{svc: svc, task: task, root: root}
	fixture.snapshot = codexapp.Snapshot{ProjectPath: task.WorkspacePath, ThreadID: "worker-thread"}
	svc.ConfigureAgentTaskRepositoryHost(func() []codexapp.Snapshot { return []codexapp.Snapshot{fixture.snapshot} }, func() []projectrun.Snapshot { return nil })
	return fixture
}

// runWorker takes ownership, applies edits and submits one result revision.
func (f *correctionFixture) runWorker(t *testing.T, revision int64, edit func(), payload model.AgentTaskResultPayload) {
	t.Helper()
	ctx := t.Context()
	startRepositoryTurn(t, f.svc, f.task, "worker")
	task, err := f.svc.AttachAgentTaskEngineerSession(ctx, f.task.ID, model.SessionSourceCodex, "worker-thread")
	if err != nil {
		t.Fatal(err)
	}
	edit()
	actor := model.AgentTaskActor{ProjectPath: task.WorkspacePath, Provider: task.Provider, SessionKey: "worker"}
	task, err = f.svc.store.SubmitAgentTaskResult(ctx, actor, control.AgentTaskSubmitResultInput{TaskID: task.ID, RunID: revision, AgentTaskResultPayload: payload})
	if err != nil {
		t.Fatal(err)
	}
	if task, err = f.svc.settleStructuredTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	f.task = task
}

func (f *correctionFixture) review(t *testing.T, decision string) {
	t.Helper()
	actor := model.AgentTaskActor{ProjectPath: f.root, Provider: model.SessionSourceCodex, SessionKey: "caller"}
	task, err := f.svc.store.ReviewAgentTaskResult(t.Context(), actor, control.AgentTaskReviewResultInput{TaskID: f.task.ID, Revision: f.task.Workflow.RunID, Decision: decision, Summary: "reviewed independently"})
	if err != nil {
		t.Fatal(err)
	}
	f.task = task
}

func (f *correctionFixture) reload(t *testing.T) model.AgentTask {
	t.Helper()
	task, err := f.svc.GetAgentTask(t.Context(), f.task.ID)
	if err != nil {
		t.Fatal(err)
	}
	f.task = task
	return task
}

func ready(summary string) model.AgentTaskResultPayload {
	return model.AgentTaskResultPayload{Outcome: "ready_for_review", Summary: summary}
}

// The loop the product needs: a rejected result is corrected in place, on the
// exact edits the caller reviewed, and the next revision is accepted.
func TestCorrectionRunContinuesOnReviewedDirtyCheckout(t *testing.T) {
	f := newCorrectionFixture(t)
	ctx := t.Context()
	worker := filepath.Join(f.root, "worker.txt")
	f.runWorker(t, 1, func() {
		if err := os.WriteFile(worker, []byte("incomplete"), 0600); err != nil {
			t.Fatal(err)
		}
	}, ready("Added worker.txt"))
	if f.task.Workflow.Phase != "awaiting_review" || f.task.Repository.HandoffFingerprint == "" {
		t.Fatalf("bad handoff: %+v %+v", f.task.Workflow, f.task.Repository)
	}
	f.review(t, "changes_requested")
	if f.task.Workflow.Phase != "changes_requested" {
		t.Fatalf("review phase: %+v", f.task.Workflow)
	}

	f.runWorker(t, 2, func() {
		data, err := os.ReadFile(worker)
		if err != nil || string(data) != "incomplete" {
			t.Fatalf("correction lost the reviewed edits: %q %v", data, err)
		}
		if err := os.WriteFile(worker, []byte("incomplete, then corrected"), 0600); err != nil {
			t.Fatal(err)
		}
	}, ready("Corrected worker.txt"))

	if f.task.Workflow.RunID != 2 || f.task.Workflow.Phase != "awaiting_review" {
		t.Fatalf("correction did not open revision 2: %+v", f.task.Workflow)
	}
	if f.task.Repository.State == "held" {
		t.Fatalf("correction did not release ownership: %+v", f.task.Repository)
	}
	f.review(t, "accept")
	if f.task.Workflow.Phase != "completed" || f.task.Status != model.AgentTaskStatusCompleted {
		t.Fatalf("acceptance: %+v %s", f.task.Workflow, f.task.Status)
	}
	history, err := f.svc.GetAgentTaskResult(ctx, f.task.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if history["caller_review"].(model.AgentTaskReview).Decision != "changes_requested" {
		t.Fatal("lost the rejected revision")
	}
	data, _ := os.ReadFile(worker)
	if string(data) != "incomplete, then corrected" {
		t.Fatalf("final content: %q", data)
	}
}

// An edit that arrives between review and correction is not silently adopted.
func TestCorrectionRejectsUnreviewedInterveningChanges(t *testing.T) {
	f := newCorrectionFixture(t)
	f.runWorker(t, 1, func() {
		os.WriteFile(filepath.Join(f.root, "worker.txt"), []byte("incomplete"), 0600)
	}, ready("Added worker.txt"))
	f.review(t, "changes_requested")

	stray := filepath.Join(f.root, "elsewhere.txt")
	if err := os.WriteFile(stray, []byte("unrelated human edit"), 0600); err != nil {
		t.Fatal(err)
	}
	if unlock, err := f.svc.BeginRepositoryTurn(f.task.WorkspacePath, "worker"); err == nil {
		unlock()
		t.Fatal("adopted an unreviewed dirty baseline")
	}
	current := f.reload(t)
	if current.Repository.State != "blocked" || current.Repository.Error == "" {
		t.Fatalf("failure not visible: %+v", current.Repository)
	}
	if data, _ := os.ReadFile(stray); string(data) != "unrelated human edit" {
		t.Fatalf("intervening edit was not preserved: %q", data)
	}
	if data, _ := os.ReadFile(filepath.Join(f.root, "worker.txt")); string(data) != "incomplete" {
		t.Fatalf("worker edit was not preserved: %q", data)
	}
	leases, err := f.svc.Store().AgentTaskRepositoryLeases(t.Context())
	if err != nil || len(leases) != 0 {
		t.Fatalf("unexpected leases: %+v %v", leases, err)
	}
}

// The caller's own fixes become a boundary only through an explicit capture.
func TestCaptureCorrectionBaselineAdmitsCallerFixes(t *testing.T) {
	f := newCorrectionFixture(t)
	ctx := t.Context()
	f.runWorker(t, 1, func() {
		os.WriteFile(filepath.Join(f.root, "worker.txt"), []byte("incomplete"), 0600)
	}, ready("Added worker.txt"))

	if _, err := f.svc.CaptureAgentTaskCorrectionBaseline(ctx, f.task.ID); err == nil {
		t.Fatal("captured a boundary before any changes_requested review")
	}
	f.review(t, "changes_requested")

	fix := filepath.Join(f.root, "caller_fix.txt")
	if err := os.WriteFile(fix, []byte("caller fix"), 0600); err != nil {
		t.Fatal(err)
	}
	if unlock, err := f.svc.BeginRepositoryTurn(f.task.WorkspacePath, "worker"); err == nil {
		unlock()
		t.Fatal("adopted caller fixes without a capture")
	}
	task, err := f.svc.CaptureAgentTaskCorrectionBaseline(ctx, f.task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if task.Repository.CorrectionBaseline == "" || task.Repository.CorrectionRevision != 1 {
		t.Fatalf("baseline not recorded: %+v", task.Repository)
	}
	f.task = task
	if data, _ := os.ReadFile(fix); string(data) != "caller fix" {
		t.Fatal("capture modified the checkout")
	}
	f.runWorker(t, 2, func() {}, ready("Reviewed caller fix"))
	if f.task.Workflow.RunID != 2 {
		t.Fatalf("correction did not run: %+v", f.task.Workflow)
	}
}

// A boundary cannot be replayed once its run has moved on.
func TestCorrectionBaselineDoesNotSurviveItsRevision(t *testing.T) {
	f := newCorrectionFixture(t)
	ctx := t.Context()
	f.runWorker(t, 1, func() {
		os.WriteFile(filepath.Join(f.root, "worker.txt"), []byte("incomplete"), 0600)
	}, ready("Added worker.txt"))
	f.review(t, "changes_requested")
	if _, err := f.svc.CaptureAgentTaskCorrectionBaseline(ctx, f.task.ID); err != nil {
		t.Fatal(err)
	}
	f.reload(t)
	captured := f.task.Repository.CorrectionBaseline

	f.runWorker(t, 2, func() {
		os.WriteFile(filepath.Join(f.root, "worker.txt"), []byte("still incomplete"), 0600)
	}, ready("Second attempt"))
	current := f.reload(t)
	if current.Repository.CorrectionBaseline != captured || current.Repository.CorrectionRevision != 1 {
		t.Fatalf("stale boundary was rewritten: %+v", current.Repository)
	}
	// Revision 2 is awaiting review, so no correction is authorized at all.
	if unlock, err := f.svc.BeginRepositoryTurn(f.task.WorkspacePath, "worker"); err == nil {
		unlock()
		t.Fatal("replayed a stale correction boundary")
	}
}

// Acceptance is terminal for ownership: it never authorizes another dirty run.
func TestAcceptedResultDoesNotAuthorizeCorrection(t *testing.T) {
	f := newCorrectionFixture(t)
	f.runWorker(t, 1, func() {
		os.WriteFile(filepath.Join(f.root, "worker.txt"), []byte("good enough"), 0600)
	}, ready("Added worker.txt"))
	f.review(t, "accept")
	if _, err := f.svc.CaptureAgentTaskCorrectionBaseline(t.Context(), f.task.ID); err == nil {
		t.Fatal("captured a boundary for an accepted result")
	}
	if unlock, err := f.svc.BeginRepositoryTurn(f.task.WorkspacePath, "worker"); err == nil {
		unlock()
		t.Fatal("accepted result authorized a dirty run")
	}
}

// Capture is a read; it must not race a worker that still owns the checkout.
func TestCaptureCorrectionBaselineRefusesHeldOwnershipAndBusyWriters(t *testing.T) {
	f := newCorrectionFixture(t)
	ctx := t.Context()
	f.runWorker(t, 1, func() {
		os.WriteFile(filepath.Join(f.root, "worker.txt"), []byte("incomplete"), 0600)
	}, ready("Added worker.txt"))
	f.review(t, "changes_requested")

	f.snapshot.Busy = true
	if _, err := f.svc.CaptureAgentTaskCorrectionBaseline(ctx, f.task.ID); err == nil {
		t.Fatal("captured a baseline under an active writer")
	}
	f.snapshot.Busy = false

	startRepositoryTurn(t, f.svc, f.task, "worker")
	if _, err := f.svc.CaptureAgentTaskCorrectionBaseline(ctx, f.task.ID); err == nil {
		t.Fatal("captured a baseline while ownership was held")
	}
	if current := f.reload(t); current.Repository.State != "held" {
		t.Fatalf("capture disturbed ownership: %+v", current.Repository)
	}
}
