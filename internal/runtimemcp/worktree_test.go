package runtimemcp

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lcroom/internal/control"
	"lcroom/internal/projectrun"
	"lcroom/internal/store"
)

func TestEveryEmbeddedProviderCanProposeWorktreeRemoval(t *testing.T) {
	for _, provider := range []string{"codex", "opencode", "claude_code", "lcagent"} {
		t.Run(provider, func(t *testing.T) {
			st, err := store.Open(filepath.Join(t.TempDir(), "control.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			manager := projectrun.NewManager()
			defer manager.CloseAll()
			path := t.TempDir()
			server, err := New(Options{ProjectPath: t.TempDir(), Provider: provider, SessionKey: "remove-test", ControlScope: control.AuthorityScopePortfolio, Store: st, Manager: manager})
			if err != nil {
				t.Fatal(err)
			}
			listed := callRuntimeToolForMap(t, server, "list_control_capabilities", `{"domain":"worktree"}`)
			if !strings.Contains(mustJSON(t, listed), "worktree.remove") {
				t.Fatalf("removal unavailable: %#v", listed)
			}
			described := callRuntimeToolForMap(t, server, "describe_control_capability", `{"name":"worktree.remove"}`)
			if !strings.Contains(mustJSON(t, described), `"risk":"destructive"`) || !strings.Contains(mustJSON(t, described), "worktree_path") {
				t.Fatalf("missing removal contract: %#v", described)
			}
			args := fmt.Sprintf(`{"capability":"worktree.remove","request_id":"cleanup-1","arguments":{"worktree_path":%q}}`, path)
			proposed := callRuntimeToolForMap(t, server, "propose_control_operation", args)
			id := proposed["operation"].(map[string]any)["id"].(string)
			stored, err := st.GetControlOperation(t.Context(), id)
			if err != nil || stored.Status != control.OperationProposed || stored.Confirmed || proposed["requires_new_user_turn"] != true || proposed["automatic_delivery"] == true {
				t.Fatalf("removal bypassed confirmation: %#v, %v", proposed, err)
			}
			if _, err := os.Stat(path); err != nil {
				t.Fatalf("proposal changed target: %v", err)
			}
			replayed := callRuntimeToolForMap(t, server, "propose_control_operation", args)
			if replayed["operation"].(map[string]any)["id"] != id || replayed["idempotent_replay"] != true {
				t.Fatal("retry duplicated deletion")
			}
			queried := callRuntimeToolForMap(t, server, "get_control_operation", fmt.Sprintf(`{"operation_id":%q}`, id))
			if queried["terminal"] != false {
				t.Fatal("proposal reported terminal removal")
			}
		})
	}
}
