package store

import (
	"path/filepath"
	"testing"

	"lcroom/internal/control"
	"lcroom/internal/model"
)

func TestStructuredResultIdentityRevisionAndReview(t *testing.T) {
	for _, provider := range []model.SessionSource{model.SessionSourceCodex, model.SessionSourceOpenCode, model.SessionSourceClaudeCode, model.SessionSourceLCAgent} {
		t.Run(string(provider), func(t *testing.T) {
			db := filepath.Join(t.TempDir(), "results.sqlite")
			st, err := Open(db)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { st.Close() }()
			ctx := t.Context()
			task, err := st.CreateAgentTask(ctx, model.CreateAgentTaskInput{ID: "agt_result", Title: "worker", Provider: provider, WorkspacePath: "/worker", OriginProjectPath: "/caller", OriginProvider: provider, OriginSessionKey: "caller", Workflow: model.AgentTaskWorkflow{Enabled: true, Phase: "queued"}})
			if err != nil {
				t.Fatal(err)
			}
			if err := st.BeginStructuredTaskRun(ctx, task, "worker", false); err != nil {
				t.Fatal(err)
			}
			worker := model.AgentTaskActor{ProjectPath: "/worker", Provider: provider, SessionKey: "worker"}
			caller := model.AgentTaskActor{ProjectPath: "/caller", Provider: provider, SessionKey: "caller"}
			input := control.AgentTaskSubmitResultInput{TaskID: task.ID, RunID: 1, AgentTaskResultPayload: model.AgentTaskResultPayload{Outcome: "ready_for_review", Summary: "Changed the bounded feature."}}
			for _, impostor := range []model.AgentTaskActor{caller, {ProjectPath: "/worker", Provider: provider, SessionKey: "other"}, {ProjectPath: "/other", Provider: provider, SessionKey: "worker"}, {Operator: true}} {
				if _, err := st.SubmitAgentTaskResult(ctx, impostor, input); err == nil {
					t.Fatal("accepted spoofed worker")
				}
			}
			task, err = st.SubmitAgentTaskResult(ctx, worker, input)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := st.SubmitAgentTaskResult(ctx, worker, input); err != nil {
				t.Fatal("identical retry:", err)
			}
			changed := input
			changed.Summary = "Rewritten history"
			if _, err := st.SubmitAgentTaskResult(ctx, worker, changed); err == nil {
				t.Fatal("mutable claims")
			}
			review := control.AgentTaskReviewResultInput{TaskID: task.ID, Revision: 1, Decision: "accept", Summary: "Independently checked."}
			if _, err := st.ReviewAgentTaskResult(ctx, caller, review); err == nil {
				t.Fatal("accepted active worker")
			}
			task, err = st.FinalizeStructuredTask(ctx, task)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := st.ReviewAgentTaskResult(ctx, worker, review); err == nil {
				t.Fatal("worker accepted itself")
			}
			wrongCaller := caller
			wrongCaller.SessionKey = "different"
			if _, err := st.ReviewAgentTaskResult(ctx, wrongCaller, review); err == nil {
				t.Fatal("unrelated caller accepted")
			}
			task, err = st.ReviewAgentTaskResult(ctx, caller, review)
			if err != nil {
				t.Fatal(err)
			}
			if task.Status != model.AgentTaskStatusCompleted || task.Workflow.Phase != "completed" {
				t.Fatalf("not accepted: %+v", task.Workflow)
			}
			if _, err := st.ReviewAgentTaskResult(ctx, caller, review); err != nil {
				t.Fatal("review retry:", err)
			}
			if err := st.BeginStructuredTaskRun(ctx, task, "worker", false); err != nil {
				t.Fatal(err)
			}
			if _, err := st.SubmitAgentTaskResult(ctx, worker, input); err == nil {
				t.Fatal("stale worker result accepted")
			}
			if _, err := st.ReviewAgentTaskResult(ctx, caller, review); err == nil {
				t.Fatal("stale caller review accepted")
			}
			if err := st.Close(); err != nil {
				t.Fatal(err)
			}
			st, err = Open(db)
			if err != nil {
				t.Fatal(err)
			}
			history, err := st.GetAgentTaskResult(ctx, task.ID, 1)
			if err != nil {
				t.Fatal(err)
			}
			if history["worker_claims"].(model.AgentTaskResult).Summary != input.Summary || history["caller_review"].(model.AgentTaskReview).Decision != "accept" || history["host_repository_evidence"] == nil {
				t.Fatalf("lost history: %+v", history)
			}
		})
	}
}

func TestStructuredStoppedAndUnclassifiedResults(t *testing.T) {
	for _, stop := range []bool{true, false} {
		st, err := Open(filepath.Join(t.TempDir(), "results.sqlite"))
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		ctx := t.Context()
		task, err := st.CreateAgentTask(ctx, model.CreateAgentTaskInput{ID: "agt_late", Title: "late worker", Provider: model.SessionSourceCodex, WorkspacePath: "/worker", Workflow: model.AgentTaskWorkflow{Enabled: true, RunID: 1, WorkerKey: "worker", Phase: "working"}})
		if err != nil {
			t.Fatal(err)
		}
		if stop {
			if err := st.StopStructuredTask(ctx, task, false); err != nil {
				t.Fatal(err)
			}
			task, _ = st.GetAgentTask(ctx, task.ID)
		}
		task, err = st.FinalizeStructuredTask(ctx, task)
		if err != nil {
			t.Fatal(err)
		}
		expected := "unclassified"
		if stop {
			expected = "canceled"
		}
		if task.Workflow.Phase != expected || !task.ResultReadyAt.IsZero() {
			t.Fatalf("unexpected free-text acceptance: %+v", task.Workflow)
		}
		input := control.AgentTaskSubmitResultInput{TaskID: task.ID, RunID: 1, AgentTaskResultPayload: model.AgentTaskResultPayload{Outcome: "blocked", Summary: "Need input."}}
		task, err = st.SubmitAgentTaskResult(ctx, model.AgentTaskActor{ProjectPath: "/worker", Provider: model.SessionSourceCodex, SessionKey: "worker"}, input)
		if err != nil {
			t.Fatal(err)
		}
		task, err = st.FinalizeStructuredTask(ctx, task)
		if err != nil {
			t.Fatal(err)
		}
		if stop {
			if task.Workflow.Phase != "canceled" || !task.ResultReadyAt.IsZero() {
				t.Fatal("late result revived canceled task")
			}
		} else {
			if task.Workflow.Phase != "blocked" {
				t.Fatal("late result did not classify task")
			}
			review := control.AgentTaskReviewResultInput{TaskID: task.ID, Revision: 1, Decision: "accept", Summary: "accept"}
			if _, err := st.ReviewAgentTaskResult(ctx, model.AgentTaskActor{Operator: true}, review); err == nil {
				t.Fatal("accepted blocked result")
			}
			review.Decision = "changes_requested"
			task, err = st.ReviewAgentTaskResult(ctx, model.AgentTaskActor{Operator: true}, review)
			if err != nil || task.Workflow.Phase != "changes_requested" {
				t.Fatalf("review: %+v %v", task.Workflow, err)
			}
		}
	}
}
