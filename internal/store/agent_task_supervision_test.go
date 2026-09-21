package store

import (
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"lcroom/internal/control"
	"lcroom/internal/model"
)

const supervisionCaller = "/tmp/supervision-caller"

func supervisionTask(t *testing.T, st *Store, maxCorrections int) model.AgentTask {
	t.Helper()
	task, err := st.CreateAgentTask(t.Context(), model.CreateAgentTaskInput{
		Title:             "supervised worker",
		Kind:              model.AgentTaskKindAgent,
		Provider:          model.SessionSourceCodex,
		OriginProjectPath: supervisionCaller,
		OriginProvider:    model.SessionSourceCodex,
		OriginSessionKey:  "caller-key",
		Workflow:          model.AgentTaskWorkflow{Enabled: true, MaxCorrections: maxCorrections},
	})
	if err != nil {
		t.Fatal(err)
	}
	return task
}

// rejectRevision drives the task to a recorded changes_requested review, the
// only state a correction grant covers.
func rejectRevision(t *testing.T, st *Store, task model.AgentTask, revision int64) model.AgentTask {
	t.Helper()
	ctx := t.Context()
	workflow := task.Workflow
	workflow.RunID, workflow.Phase, workflow.Handoff, workflow.WorkerKey = revision, "changes_requested", true, "worker-key"
	workflow.Result = &model.AgentTaskResult{TaskID: task.ID, Revision: revision, AgentTaskResultPayload: model.AgentTaskResultPayload{Outcome: "ready_for_review", Summary: "incomplete"}}
	workflow.Review = &model.AgentTaskReview{Revision: revision, Decision: "changes_requested", Summary: "needs work", Reviewer: "caller-key"}
	if err := st.workflowTransaction(ctx, task, workflow, model.AgentTaskStatusWaiting, nil); err != nil {
		t.Fatal(err)
	}
	updated, err := st.GetAgentTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	return updated
}

func continueOperation(t *testing.T, st *Store, taskID string, mutate func(*control.AgentTaskContinueInput), op func(*control.Operation)) control.Operation {
	t.Helper()
	input := control.AgentTaskContinueInput{TaskID: taskID, Prompt: "address the review", Provider: control.ProviderAuto, SessionMode: control.SessionModeResumeOrNew}
	if mutate != nil {
		mutate(&input)
	}
	args, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	id := "lcrop_" + strings.ReplaceAll(taskID, "-", "") + supervisionOperationSuffix(t)
	operation := control.Operation{
		ID:          id,
		Capability:  control.CapabilityAgentTaskContinue,
		Invocation:  control.Invocation{Capability: control.CapabilityAgentTaskContinue, Args: args},
		Source:      "mcp",
		Provider:    string(model.SessionSourceCodex),
		SessionKey:  "caller-key",
		ProjectPath: supervisionCaller,
		RequestedBy: "codex",
	}
	if op != nil {
		op(&operation)
	}
	created, err := st.CreateControlOperation(t.Context(), operation)
	if err != nil {
		t.Fatal(err)
	}
	claimed, found, err := st.ClaimNextControlOperation(t.Context())
	if err != nil || !found || claimed.ID != created.ID {
		t.Fatalf("claim operation: %v %v %s", found, err, claimed.ID)
	}
	if claimed.Status != control.OperationWaitingForConfirmation {
		t.Fatalf("claimed status = %s", claimed.Status)
	}
	return claimed
}

var supervisionOperationCounter int

func supervisionOperationSuffix(t *testing.T) string {
	t.Helper()
	supervisionOperationCounter++
	return strconv.Itoa(supervisionOperationCounter)
}

func supervisionStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestSupervisionGrantAuthorizesBoundedCorrections(t *testing.T) {
	st := supervisionStore(t)
	task := rejectRevision(t, st, supervisionTask(t, st, 2), 1)

	first := continueOperation(t, st, task.ID, nil, nil)
	allowed, err := st.SupervisionAllowsOperation(t.Context(), first)
	if err != nil || !allowed {
		t.Fatalf("grant did not cover the first correction: %v %v", allowed, err)
	}
	confirmed, automatic, err := st.ConfirmDelegationSupervision(t.Context(), first.ID)
	if err != nil || !automatic {
		t.Fatalf("first correction not authorized: %v %v", automatic, err)
	}
	if confirmed.ConfirmationBy != control.ConfirmationDelegationSupervision || confirmed.Status != control.OperationRunning {
		t.Fatalf("unexpected confirmation: %+v", confirmed)
	}
	current, err := st.GetAgentTask(t.Context(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Workflow.CorrectionsUsed != 1 || current.Workflow.CorrectionsRemaining() != 1 {
		t.Fatalf("round not consumed exactly once: %+v", current.Workflow)
	}
	// Replaying the same authorized operation must not spend a second round.
	if _, automatic, err := st.ConfirmDelegationSupervision(t.Context(), first.ID); err != nil || automatic {
		t.Fatalf("replay re-authorized: %v %v", automatic, err)
	}
	if current, _ = st.GetAgentTask(t.Context(), task.ID); current.Workflow.CorrectionsUsed != 1 {
		t.Fatalf("replay consumed a round: %+v", current.Workflow)
	}

	// The second round is covered; the third is not, and falls back to asking.
	task = rejectRevision(t, st, current, 2)
	second := continueOperation(t, st, task.ID, nil, nil)
	if _, automatic, err := st.ConfirmDelegationSupervision(t.Context(), second.ID); err != nil || !automatic {
		t.Fatalf("second correction not authorized: %v %v", automatic, err)
	}
	current, _ = st.GetAgentTask(t.Context(), task.ID)
	task = rejectRevision(t, st, current, 3)
	third := continueOperation(t, st, task.ID, nil, nil)
	if _, automatic, err := st.ConfirmDelegationSupervision(t.Context(), third.ID); err != nil || automatic {
		t.Fatalf("exhausted grant still authorized: %v %v", automatic, err)
	}
	if reloaded, _ := st.GetControlOperation(t.Context(), third.ID); reloaded.Status != control.OperationWaitingForConfirmation {
		t.Fatalf("exhausted grant did not fall back to confirmation: %s", reloaded.Status)
	}
}

func TestSupervisionGrantRefusesAnythingItDoesNotCover(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*control.AgentTaskContinueInput)
		op     func(*control.Operation)
		setup  func(*testing.T, *Store, model.AgentTask) model.AgentTask
	}{
		{name: "fresh session", mutate: func(i *control.AgentTaskContinueInput) { i.SessionMode = control.SessionModeNew }},
		{name: "explicit provider", mutate: func(i *control.AgentTaskContinueInput) { i.Provider = control.ProviderClaudeCode }},
		{name: "model change", mutate: func(i *control.AgentTaskContinueInput) {
			// LCR requires an explicit provider alongside a model choice, so a
			// model change is always also a provider-naming proposal.
			i.Provider = control.ProviderCodex
			i.EngineerModelSelection = control.EngineerModelSelection{Model: "gpt-5-codex", ReasoningEffort: "medium"}
		}},
		{name: "unrelated caller session", op: func(o *control.Operation) { o.SessionKey = "someone-else" }},
		{name: "unrelated caller project", op: func(o *control.Operation) { o.ProjectPath = "/tmp/other-project" }},
		{name: "caller provider mismatch", op: func(o *control.Operation) { o.Provider = string(model.SessionSourceClaudeCode) }},
		{
			name: "no rejected review",
			setup: func(t *testing.T, st *Store, task model.AgentTask) model.AgentTask {
				workflow := task.Workflow
				workflow.Review = &model.AgentTaskReview{Revision: 1, Decision: "accept", Summary: "verified"}
				workflow.Phase = "completed"
				if err := st.workflowTransaction(t.Context(), task, workflow, model.AgentTaskStatusWaiting, nil); err != nil {
					t.Fatal(err)
				}
				updated, _ := st.GetAgentTask(t.Context(), task.ID)
				return updated
			},
		},
		{
			name: "superseded revision",
			setup: func(t *testing.T, st *Store, task model.AgentTask) model.AgentTask {
				workflow := task.Workflow
				workflow.RunID = 2
				if err := st.workflowTransaction(t.Context(), task, workflow, model.AgentTaskStatusWaiting, nil); err != nil {
					t.Fatal(err)
				}
				updated, _ := st.GetAgentTask(t.Context(), task.ID)
				return updated
			},
		},
		{
			name: "explicit stop",
			setup: func(t *testing.T, st *Store, task model.AgentTask) model.AgentTask {
				if err := st.StopStructuredTask(t.Context(), task, true); err != nil {
					t.Fatal(err)
				}
				updated, _ := st.GetAgentTask(t.Context(), task.ID)
				return updated
			},
		},
		{
			name: "revoked grant",
			setup: func(t *testing.T, st *Store, task model.AgentTask) model.AgentTask {
				updated, err := st.RevokeAgentTaskSupervision(t.Context(), task.ID)
				if err != nil {
					t.Fatal(err)
				}
				return updated
			},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			st := supervisionStore(t)
			task := rejectRevision(t, st, supervisionTask(t, st, 2), 1)
			if testCase.setup != nil {
				task = testCase.setup(t, st, task)
			}
			operation := continueOperation(t, st, task.ID, testCase.mutate, testCase.op)
			allowed, err := st.SupervisionAllowsOperation(t.Context(), operation)
			if err != nil {
				t.Fatal(err)
			}
			if allowed {
				t.Fatal("grant covered an operation outside its scope")
			}
			if _, automatic, err := st.ConfirmDelegationSupervision(t.Context(), operation.ID); err != nil || automatic {
				t.Fatalf("operation was authorized anyway: %v %v", automatic, err)
			}
			current, _ := st.GetAgentTask(t.Context(), task.ID)
			if current.Workflow.CorrectionsUsed != 0 {
				t.Fatalf("a refused operation consumed a round: %+v", current.Workflow)
			}
		})
	}
}

// A worker must never be able to reopen itself for another round.
func TestSupervisionGrantRejectsWorkerSelfContinuation(t *testing.T) {
	st := supervisionStore(t)
	task := rejectRevision(t, st, supervisionTask(t, st, 2), 1)
	operation := continueOperation(t, st, task.ID, nil, func(o *control.Operation) {
		o.ProjectPath = task.WorkspacePath
		o.SessionKey = "worker-key"
	})
	if allowed, err := st.SupervisionAllowsOperation(t.Context(), operation); err != nil || allowed {
		t.Fatalf("worker authorized its own correction: %v %v", allowed, err)
	}
}

// A new run resets result metadata; it must not reset or refill the grant.
func TestSupervisionGrantSurvivesNewRunsWithoutRefilling(t *testing.T) {
	st := supervisionStore(t)
	task := rejectRevision(t, st, supervisionTask(t, st, 2), 1)
	operation := continueOperation(t, st, task.ID, nil, nil)
	if _, automatic, err := st.ConfirmDelegationSupervision(t.Context(), operation.ID); err != nil || !automatic {
		t.Fatalf("correction not authorized: %v %v", automatic, err)
	}
	current, _ := st.GetAgentTask(t.Context(), task.ID)
	if err := st.BeginStructuredTaskRun(t.Context(), current, "worker-key", false); err != nil {
		t.Fatal(err)
	}
	next, _ := st.GetAgentTask(t.Context(), task.ID)
	if next.Workflow.RunID != 2 || next.Workflow.Result != nil || next.Workflow.Review != nil {
		t.Fatalf("new run did not reset result metadata: %+v", next.Workflow)
	}
	if next.Workflow.MaxCorrections != 2 || next.Workflow.CorrectionsUsed != 1 {
		t.Fatalf("new run disturbed the grant: %+v", next.Workflow)
	}
}
