package agentquery

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"lcroom/internal/integrations"
	"lcroom/internal/model"
)

type fakeIntegrationReader struct {
	calls  int
	target integrations.Target
}

func (r *fakeIntegrationReader) Inventory(_ context.Context, target integrations.Target) (integrations.Inventory, error) {
	r.calls++
	r.target = target
	if _, err := integrations.ValidateTarget(target); err != nil {
		return integrations.Inventory{}, err
	}
	return integrations.Inventory{Target: target, Revision: strings.Repeat("a", 64), Entries: []integrations.Entry{{ID: "one", Kind: "skill"}, {ID: "two", Kind: "skill"}, {ID: "three", Kind: "mcp"}}}, nil
}

func TestIntegrationQueryPaginationScopeAndPrivacy(t *testing.T) {
	reader := newFakeReader([]model.ProjectSummary{{Path: "/repos/origin", Name: "Origin", InScope: true}, {Path: "/repos/private", Name: "Private", InScope: true, CategoryPrivate: true}})
	manager := &fakeIntegrationReader{}
	e := mustExecutor(t, reader, "/repos/origin", ScopePortfolio)
	e.integrations = manager
	query := func(args string) (map[string]any, error) {
		return e.Execute(t.Context(), QueryIntegrationsList, json.RawMessage(args))
	}
	page, err := query(`{"provider":"codex","scope":"project","kind":"skill","limit":1}`)
	if err != nil {
		t.Fatal(err)
	}
	if manager.target.ProjectPath != "/repos/origin" || page["total"] != 2 || page["truncated"] != true {
		t.Fatalf("wrong page: %#v", page)
	}
	cursor := page["next_cursor"].(string)
	page, err = query(fmt.Sprintf(`{"provider":"codex","scope":"project","kind":"skill","limit":1,"cursor":%q}`, cursor))
	if err != nil {
		t.Fatal(err)
	}
	if page["entries"].([]integrations.Entry)[0].ID != "two" || page["truncated"] != false {
		t.Fatalf("wrong second page: %#v", page)
	}
	if _, err := query(fmt.Sprintf(`{"provider":"codex","scope":"project","kind":"mcp","cursor":%q}`, cursor)); err == nil {
		t.Fatal("cursor accepted for a different filter")
	}
	before := manager.calls
	if _, err := query(`{"provider":"codex","scope":"project","project_path":"/repos/private"}`); err == nil {
		t.Fatal("private project exposed")
	}
	if _, err := query(`{"provider":"codex","scope":"project","project_path":"/unloaded"}`); err == nil {
		t.Fatal("unloaded project exposed")
	}
	if manager.calls != before {
		t.Fatal("private/unloaded project inspected before scope validation")
	}
	e.scope = ScopeProject
	if _, err := query(`{"provider":"codex","scope":"user"}`); err == nil {
		t.Fatal("user inventory exposed to project-only query client")
	}
	if manager.calls != before {
		t.Fatal("user inventory read before authority check")
	}
}
