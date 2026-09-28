package agentquery

import (
	"encoding/json"
	"testing"
	"time"

	"lcroom/internal/model"
)

func TestHistoricalQueriesFilterBeforePaginationAndPreservePrivacy(t *testing.T) {
	now := time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)
	reader := newFakeReader([]model.ProjectSummary{
		{Path: "/origin", InScope: true, LastActivity: now.Add(-time.Hour), AttentionScore: 100},
		{Path: "/recent", InScope: true, LastActivity: now},
		{Path: "/secret", InScope: true, LastActivity: now, CategoryPrivate: true},
		{Path: "/unknown", InScope: true},
	})
	executor := mustExecutor(t, reader, "/origin", ScopePortfolio)
	first, err := executor.Execute(t.Context(), QueryProjectList, json.RawMessage(`{"since":"2026-09-27T23:00:00Z","until":"2026-09-28T00:00:00Z","order_by":"last_activity","limit":1}`))
	if err != nil {
		t.Fatal(err)
	}
	projects := first["projects"].([]map[string]any)
	if first["total"] != 2 || len(projects) != 1 || projects[0]["path"] != "/recent" {
		t.Fatalf("first=%#v", first)
	}
	args, _ := json.Marshal(map[string]any{"since": "2026-09-27T23:00:00Z", "until": "2026-09-28T00:00:00Z", "order_by": "last_activity", "limit": 1, "cursor": first["next_cursor"]})
	second, err := executor.Execute(t.Context(), QueryProjectList, args)
	if err != nil {
		t.Fatal(err)
	}
	if second["projects"].([]map[string]any)[0]["path"] != "/origin" {
		t.Fatalf("second=%#v", second)
	}
	reader.details["/origin"] = model.ProjectDetail{Summary: reader.projects["/origin"], Todos: []model.TodoItem{
		{ID: 1, Text: "old", UpdatedAt: now.Add(-2 * time.Hour)},
		{ID: 2, Text: "completed", Done: true, UpdatedAt: now},
		{ID: 3, Text: "unknown"},
	}}
	todos, err := executor.Execute(t.Context(), QueryTodoList, json.RawMessage(`{"since":"2026-09-28T09:00:00+09:00","until":"2026-09-28T00:00:00Z","include_completed":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if todos["total"] != 1 {
		t.Fatalf("todos=%#v", todos)
	}
	for _, raw := range []string{`{"since":"yesterday"}`, `{"since":"2026-09-28T00:00:00Z","until":"2026-09-27T00:00:00Z"}`, `{"order_by":"bogus"}`} {
		if _, err := executor.Execute(t.Context(), QueryProjectList, json.RawMessage(raw)); err == nil {
			t.Errorf("accepted invalid args %s", raw)
		}
	}
	unbounded, err := executor.Execute(t.Context(), QueryProjectList, json.RawMessage(`{"order_by":"last_activity"}`))
	if err != nil {
		t.Fatal(err)
	}
	all := unbounded["projects"].([]map[string]any)
	if len(all) != 3 || all[2]["path"] != "/unknown" {
		t.Fatalf("unbounded=%#v", unbounded)
	}
}
