package runtimemcp

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"lcroom/internal/control"
	"lcroom/internal/projectrun"
	"lcroom/internal/store"
)

func TestEveryEmbeddedProviderCanProposeIntegrationManagement(t *testing.T) {
	for _, provider := range []string{"codex", "opencode", "claude_code", "lcagent"} {
		t.Run(provider, func(t *testing.T) {
			st, err := store.Open(filepath.Join(t.TempDir(), "control.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			manager := projectrun.NewManager()
			defer manager.CloseAll()
			server, err := New(Options{ProjectPath: t.TempDir(), Provider: provider, SessionKey: "integration-test", ControlScope: control.AuthorityScopePortfolio, Store: st, Manager: manager})
			if err != nil {
				t.Fatal(err)
			}
			listed := callRuntimeToolForMap(t, server, "list_control_capabilities", `{"domain":"integrations"}`)
			if !strings.Contains(mustJSON(t, listed), "integrations.manage") {
				t.Fatalf("integration controls unavailable to %s: %#v", provider, listed)
			}
			described := callRuntimeToolForMap(t, server, "describe_control_capability", `{"name":"integrations.manage"}`)
			if !strings.Contains(mustJSON(t, described), "expected_revision") {
				t.Fatal("missing exact revision schema")
			}
			// The recipient/provider is explicitly independent from the proposer.
			args := fmt.Sprintf(`{"capability":"integrations.manage","request_id":"install-example","arguments":{"provider":"codex","scope":"user","action":"add_mcp","expected_revision":%q,"mcp":{"name":"example","command":["example-server"]}}}`, strings.Repeat("a", 64))
			proposed := callRuntimeToolForMap(t, server, "propose_control_operation", args)
			operation := proposed["operation"].(map[string]any)
			id := operation["id"].(string)
			stored, err := st.GetControlOperation(t.Context(), id)
			if err != nil {
				t.Fatal(err)
			}
			if stored.Status != control.OperationProposed || stored.Capability != control.CapabilityIntegrationsManage {
				t.Fatalf("proposal executed or lost: %#v", stored)
			}
			replayed := callRuntimeToolForMap(t, server, "propose_control_operation", args)
			if replayed["operation"].(map[string]any)["id"] != id || replayed["idempotent_replay"] != true {
				t.Fatal("retry duplicated integration operation")
			}
			queried := callRuntimeToolForMap(t, server, "get_control_operation", fmt.Sprintf(`{"operation_id":%q}`, id))
			if !strings.Contains(mustJSON(t, queried), "proposed") {
				t.Fatalf("status receipt missing: %#v", queried)
			}
		})
	}
}
