package agentquery

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"lcroom/internal/control"
	"lcroom/internal/store"
)

func TestEngineerModelsQueryDiscoveryAndPagination(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	executor := mustExecutor(t, st, "/repo", ScopeProject)
	if _, err := DescribeReport(string(QueryEngineerModels), ScopeProject, "run_lcr_query"); err != nil {
		t.Fatal(err)
	}
	args := json.RawMessage(`{"provider":"codex","limit":1}`)
	empty, err := executor.Execute(t.Context(), QueryEngineerModels, args)
	if err != nil || empty["source"] != "unavailable" {
		t.Fatalf("missing catalog: %#v %v", empty, err)
	}
	catalog := control.EngineerModelCatalog{Provider: control.ProviderCodex, Source: "provider_listing", ObservedAt: time.Now(), Models: []control.EngineerModel{
		{Model: "exact-a", ReasoningEfforts: []string{"medium"}},
		{Model: "exact-b", ReasoningEfforts: []string{"high"}},
	}}
	if err := st.SaveEngineerModelCatalog(t.Context(), catalog); err != nil {
		t.Fatal(err)
	}
	first, err := executor.Execute(t.Context(), QueryEngineerModels, args)
	if err != nil || first["truncated"] != true || first["models"].([]control.EngineerModel)[0].Model != "exact-a" {
		t.Fatalf("first page: %#v %v", first, err)
	}
	second, err := executor.Execute(t.Context(), QueryEngineerModels, json.RawMessage(`{"provider":"codex","offset":1,"limit":1}`))
	if err != nil || second["truncated"] != false || second["models"].([]control.EngineerModel)[0].Model != "exact-b" {
		t.Fatalf("second page: %#v %v", second, err)
	}
	for _, raw := range []string{`{"provider":"auto"}`, `{"provider":"codex","offset":-1}`, `{"provider":"codex","extra":true}`} {
		if _, err := executor.Execute(t.Context(), QueryEngineerModels, json.RawMessage(raw)); err == nil {
			t.Fatalf("accepted invalid query %s", raw)
		}
	}
}
