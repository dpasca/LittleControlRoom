package agentquery

import (
	"context"
	"encoding/json"
	"testing"

	"lcroom/internal/model"
)

type resultHistoryReader struct {
	*fakeReader
	calls int
}

func (r *resultHistoryReader) GetAgentTaskResult(_ context.Context, id string, revision int64) (map[string]any, error) {
	r.calls++
	return map[string]any{"task_id": id, "revision": revision}, nil
}
func TestStructuredResultQueryChecksVisibilityBeforeHistory(t *testing.T) {
	reader := &resultHistoryReader{fakeReader: newFakeReader(nil)}
	reader.tasks["agt_private"] = model.AgentTask{ID: "agt_private", CategoryPrivate: true, Workflow: model.AgentTaskWorkflow{Enabled: true, RunID: 2, Phase: "working"}}
	e, err := NewExecutor(Options{Reader: reader, Scope: ScopePortfolio, Disclosure: DisclosureHidePrivate})
	if err != nil {
		t.Fatal(err)
	}
	args := json.RawMessage(`{"task_id":"agt_private","result_revision":1}`)
	if _, err := e.Execute(t.Context(), QueryAgentTaskGet, args); err == nil || reader.calls != 0 {
		t.Fatal("read private result history")
	}
	e.disclosure = DisclosureHost
	result, err := e.Execute(t.Context(), QueryAgentTaskGet, args)
	if err != nil {
		t.Fatal(err)
	}
	if reader.calls != 1 || result["result"] == nil {
		t.Fatalf("missing exact history: %+v", result)
	}
	task := reader.tasks["agt_private"]
	if agentTaskRecord(task)["workflow"].(map[string]any)["run_id"] != int64(2) {
		t.Fatal("list omitted compact run identity")
	}
}
