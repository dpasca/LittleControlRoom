package store

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"lcroom/internal/control"
)

func permissionOperation(t *testing.T, st *Store, id, key, origin, target string, capability control.CapabilityName, fields map[string]any) control.Operation {
	t.Helper()
	if fields == nil {
		fields = map[string]any{"project_path": target, "text": "Track progress"}
	}
	args, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	op, err := st.CreateControlOperation(t.Context(), control.Operation{ID: id, Source: "mcp", Provider: "codex", SessionKey: key, ProjectPath: origin, Invocation: control.Invocation{Capability: capability, Args: args}})
	if err != nil {
		t.Fatal(err)
	}
	return op
}

func TestScopedPermissionSessionRevocationAndQueueIsolation(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := t.Context()
	origin, target := t.TempDir(), t.TempDir()
	first := permissionOperation(t, st, "first", "caller", origin, target, control.CapabilityTodoAdd, nil)
	if _, _, err := st.ClaimNextControlOperation(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ApproveControlPermission(ctx, first.ID, true); err != nil {
		t.Fatal(err)
	}
	unrelated := permissionOperation(t, st, "unrelated", "another", origin, target, control.CapabilityTodoAdd, nil)
	if _, _, err := st.ClaimNextControlOperation(ctx); err != nil {
		t.Fatal(err)
	}
	for _, op := range []control.Operation{
		unrelated,
		permissionOperation(t, st, "other-target", "caller", origin, t.TempDir(), control.CapabilityTodoAdd, nil),
		permissionOperation(t, st, "other-origin", "caller", t.TempDir(), target, control.CapabilityTodoAdd, nil),
	} {
		if yes, err := st.PermissionAllowsOperation(ctx, op); err != nil || yes {
			t.Fatalf("permission escaped scope: %+v %v", op, err)
		}
	}
	next := permissionOperation(t, st, "next", "caller", origin, target, control.CapabilityTodoAdd, nil)
	claimed, found, err := st.ClaimNextControlOperation(ctx)
	if err != nil || !found || claimed.ID != next.ID {
		t.Fatalf("matching request stuck behind manual confirmation: %v %v %v", claimed, found, err)
	}
	permissions, err := st.ListControlPermissions(ctx, origin)
	if err != nil || len(permissions) != 1 {
		t.Fatal(permissions, err)
	}
	if err := st.RevokeControlPermission(ctx, origin, permissions[0].ID); err != nil {
		t.Fatal(err)
	}
	op, automatic, err := st.ConfirmControlPermission(ctx, next.ID)
	if err != nil || automatic || op.Status != control.OperationWaitingForConfirmation {
		t.Fatal("revocation lost race", op, automatic, err)
	}
}

func TestSavedLaunchPermissionHasFixedModelAndAtomicBudget(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.sqlite")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { st.Close() }()
	ctx := t.Context()
	origin, target := t.TempDir(), t.TempDir()
	fields := func(model string) map[string]any {
		return map[string]any{"project_path": target, "todo_text": "Implement the task", "provider": "codex", "model": model}
	}
	first := permissionOperation(t, st, "first", "caller", origin, target, control.CapabilityTodoCreateWorktreeAndStartEngineer, fields("approved-model"))
	if _, _, err := st.ClaimNextControlOperation(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ApproveControlPermission(ctx, first.ID, false); err != nil {
		t.Fatal(err)
	}
	st.Close()
	st, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	changed := permissionOperation(t, st, "changed-model", "later-session", origin, target, control.CapabilityTodoCreateWorktreeAndStartEngineer, fields("expensive-model"))
	if yes, err := st.PermissionAllowsOperation(ctx, changed); err != nil || yes {
		t.Fatal("model change inherited grant", yes, err)
	}
	// Claim the unrelated manual request, then let the saved requests pass it.
	if _, _, err := st.ClaimNextControlOperation(ctx); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"second", "third"} {
		op := permissionOperation(t, st, id, "later-session", origin, target, control.CapabilityTodoCreateWorktreeAndStartEngineer, fields("approved-model"))
		if _, found, err := st.ClaimNextControlOperation(ctx); err != nil || !found {
			t.Fatal(found, err)
		}
		approved, automatic, err := st.ConfirmControlPermission(ctx, op.ID)
		if err != nil || !automatic || approved.ConfirmationBy != control.ConfirmationScopedPermission {
			t.Fatal(approved, automatic, err)
		}
		if _, automatic, err := st.ConfirmControlPermission(ctx, op.ID); err != nil || automatic {
			t.Fatal("replay spent another use", automatic, err)
		}
	}
	last := permissionOperation(t, st, "fourth", "later-session", origin, target, control.CapabilityTodoCreateWorktreeAndStartEngineer, fields("approved-model"))
	if yes, err := st.PermissionAllowsOperation(ctx, last); err != nil || yes {
		t.Fatal("exhausted grant was reused", yes, err)
	}
	permissions, err := st.ListControlPermissions(ctx, origin)
	if err != nil || len(permissions) != 1 || permissions[0].Used != 3 {
		t.Fatal(permissions, err)
	}
}

func TestContinuationIntentIsAtomicAndPartOfIdempotency(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := t.Context()
	args := json.RawMessage(`{"project_path":"/project","text":"Track progress"}`)
	op := control.Operation{ID: "original", ClientRequestID: "retry", Source: "mcp", Provider: "codex", SessionKey: "caller", ProjectPath: "/project", ResumeOnSuccess: true, Invocation: control.Invocation{Capability: control.CapabilityTodoAdd, Args: args}}
	created, err := st.CreateControlOperation(ctx, op)
	if err != nil || created.ContinuationState != "pending" {
		t.Fatal(created, err)
	}
	op.ID = "retry-id"
	again, err := st.CreateControlOperation(ctx, op)
	if err != nil || again.ID != created.ID {
		t.Fatal(again, err)
	}
	op.ResumeOnSuccess = false
	if _, err := st.CreateControlOperation(ctx, op); err == nil {
		t.Fatal("retry silently changed continuation intent")
	}
	list, err := st.ListPendingControlContinuations(ctx)
	if err != nil || len(list) != 1 {
		t.Fatal(list, err)
	}
	c := list[0]
	c.HostID = "host"
	c.SessionID = "thread"
	c.InputRevision = 1
	if yes, err := st.TransitionControlContinuation(ctx, c, "armed", ""); err != nil || !yes {
		t.Fatal(yes, err)
	}
	if yes, err := st.TransitionControlContinuation(ctx, c, "armed", ""); err != nil || yes {
		t.Fatal("duplicate transition", yes, err)
	}
}
