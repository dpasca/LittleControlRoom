package agentcontrol

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"lcroom/internal/control"
	"lcroom/internal/model"
	"lcroom/internal/store"
)

func TestTaskCompletionPreflight(t *testing.T) {
	for _, tc := range []struct {
		name    string
		enabled bool
		phase   string
		status  string
		blocked bool
	}{
		{"working", true, "working", "completed", true},
		{"review pending", true, "awaiting_review", "completed", true},
		{"unclassified", true, "unclassified", "completed", true},
		{"changes requested", true, "changes_requested", "completed", true},
		{"accepted", true, "completed", "completed", false},
		{"legacy", false, "", "completed", false},
		{"discard unreviewed", true, "awaiting_review", "archived", false},
		{"release unreviewed", true, "working", "waiting", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st, err := store.Open(filepath.Join(t.TempDir(), "close.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			ctx := t.Context()
			task, err := st.CreateAgentTask(ctx, model.CreateAgentTaskInput{
				ID: "agt_close", Title: "Cleanup", WorkspacePath: "/worker",
				Workflow: model.AgentTaskWorkflow{Enabled: tc.enabled, RunID: 1, Phase: tc.phase},
			})
			if err != nil {
				t.Fatal(err)
			}
			executor, err := NewExecutor(Options{Store: st, Scope: control.AuthorityScopePortfolio, Source: "codex", SessionKey: "caller"})
			if err != nil {
				t.Fatal(err)
			}
			args, _ := json.Marshal(map[string]any{"task_id": task.ID, "status": tc.status, "summary": "Requested cleanup", "close_session": false})
			report, err := executor.Propose(ctx, string(control.CapabilityAgentTaskClose), args, "cleanup")
			if tc.blocked {
				if err == nil || !strings.Contains(err.Error(), "worktree.remove directly") || !strings.Contains(err.Error(), "agent_task.review_result") {
					t.Fatalf("want actionable preflight refusal, got report=%v err=%v", report, err)
				}
				_, found, err := st.FindControlOperationByClientRequest(ctx, "codex", "caller", "cleanup")
				if err != nil || found {
					t.Fatalf("refused completion created a confirmation operation: found=%v err=%v", found, err)
				}
			} else if err != nil {
				t.Fatalf("allowed action refused: %v", err)
			}
			current, err := st.GetAgentTask(ctx, task.ID)
			if err != nil || current.Workflow.Phase != task.Workflow.Phase || current.Status != task.Status {
				t.Fatalf("proposal changed task: %+v err=%v", current, err)
			}
		})
	}
}
