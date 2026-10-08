package agentcontrol

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"lcroom/internal/control"
	"lcroom/internal/store"
)

func TestExplicitContinuationResponseAndSavedPermission(t *testing.T) {
	ctx := t.Context()
	st, err := store.Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	executor, err := NewExecutor(Options{Store: st, OriginProjectPath: "/caller", Scope: control.AuthorityScopePortfolio, Source: "mcp", Provider: "codex", SessionKey: "caller"})
	if err != nil {
		t.Fatal(err)
	}
	args := json.RawMessage(`{"project_path":"/target","text":"Track progress"}`)
	first, err := executor.Propose(ctx, string(control.CapabilityTodoAdd), args, "first")
	if err != nil {
		t.Fatal(err)
	}
	if first["end_turn"] == true || first["requires_new_user_turn"] != true {
		t.Fatal("default proposal opted into continuation", first)
	}
	op := first["operation"].(control.Operation)
	if _, _, err := st.ClaimNextControlOperation(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ApproveControlPermission(ctx, op.ID, false); err != nil {
		t.Fatal(err)
	}
	next, err := executor.Propose(ctx, string(control.CapabilityTodoAdd), args, "next", true)
	if err != nil {
		t.Fatal(err)
	}
	if next["end_turn"] != true || next["requires_new_user_turn"] != false || next["automatic_delivery"] != true {
		t.Fatal("explicit wait did not override automatic delivery", next)
	}
	if _, err := executor.Propose(ctx, string(control.CapabilityTodoAdd), args, "extra", true); err == nil {
		t.Fatal("caller queued a second live continuation")
	}
	op = next["operation"].(control.Operation)
	if _, err := st.UpdateControlOperationStatus(ctx, op.ID, control.OperationCanceled, nil, nil); err != nil {
		t.Fatal(err)
	}
	canceled, err := executor.Get(ctx, op.ID)
	if err != nil || canceled["requires_new_user_turn"] != true || canceled["end_turn"] == true {
		t.Fatal("cancellation promised a callback", canceled, err)
	}
}
