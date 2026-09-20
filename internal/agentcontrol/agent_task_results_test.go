package agentcontrol

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"lcroom/internal/control"
	"lcroom/internal/model"
	"lcroom/internal/store"
)

func TestStructuredResultCrossProviderControl(t *testing.T) {
	providers := []model.SessionSource{model.SessionSourceCodex, model.SessionSourceClaudeCode, model.SessionSourceOpenCode, model.SessionSourceLCAgent}
	for _, worker := range providers {
		for _, caller := range providers {
			t.Run(string(worker)+"_to_"+string(caller), func(t *testing.T) {
				st, err := store.Open(filepath.Join(t.TempDir(), "results.sqlite"))
				if err != nil {
					t.Fatal(err)
				}
				defer st.Close()
				ctx := t.Context()
				task, err := st.CreateAgentTask(ctx, model.CreateAgentTaskInput{ID: "agt_cross", Title: "worker", Provider: worker, WorkspacePath: "/worker", OriginProjectPath: "/caller", OriginProvider: caller, OriginSessionKey: "caller", Workflow: model.AgentTaskWorkflow{Enabled: true, RunID: 1, Phase: "working", WorkerKey: "worker"}})
				if err != nil {
					t.Fatal(err)
				}
				workerExec, err := NewExecutor(Options{Store: st, OriginProjectPath: "/worker", Scope: control.AuthorityScopeProject, Source: string(worker), Provider: string(worker), SessionKey: "worker"})
				if err != nil {
					t.Fatal(err)
				}
				result, err := workerExec.Propose(ctx, string(control.CapabilityAgentTaskSubmitResult), json.RawMessage(`{"task_id":"agt_cross","run_id":1,"outcome":"ready_for_review","summary":"Done"}`), "result")
				if err != nil {
					t.Fatal(err)
				}
				if result["operator_confirmation"] != false || result["requires_new_user_turn"] != false || result["operation"] != nil {
					t.Fatalf("unexpected metadata receipt: %+v", result)
				}
				task, err = st.GetAgentTask(ctx, task.ID)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = st.FinalizeStructuredTask(ctx, task); err != nil {
					t.Fatal(err)
				}
				callerExec, err := NewExecutor(Options{Store: st, OriginProjectPath: "/caller", Scope: control.AuthorityScopeProject, Source: string(caller), Provider: string(caller), SessionKey: "caller"})
				if err != nil {
					t.Fatal(err)
				}
				result, err = callerExec.Propose(ctx, string(control.CapabilityAgentTaskReviewResult), json.RawMessage(`{"task_id":"agt_cross","revision":1,"decision":"accept","summary":"Verified independently"}`), "review")
				if err != nil {
					t.Fatal(err)
				}
				if result["workflow"].(model.AgentTaskWorkflow).Phase != "completed" {
					t.Fatalf("not completed: %+v", result)
				}
				if _, err = workerExec.Propose(ctx, string(control.CapabilityAgentTaskSubmitResult), json.RawMessage(`{"task_id":"agt_cross","run_id":1,"outcome":"ready_for_review","summary":"Done","operator":true}`), "spoof"); err == nil {
					t.Fatal("accepted identity in arguments")
				}
			})
		}
	}
}
