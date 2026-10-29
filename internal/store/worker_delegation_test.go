package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"

	"lcroom/internal/control"
	"lcroom/internal/model"
)

func approveWorkerSession(t *testing.T, st *Store, origin string) control.ControlPermission {
	t.Helper()
	op := permissionOperation(t, st, "first-worker", "organizer", origin, origin, control.CapabilityAgentTaskCreate,
		map[string]any{"title": "First worker", "prompt": "Implement", "provider": "codex", "model": "first-model"})
	if _, found, err := st.ClaimNextControlOperation(t.Context()); err != nil || !found {
		t.Fatal(found, err)
	}
	approved, err := st.ApproveWorkerDelegation(t.Context(), op.ID)
	if err != nil || approved.Status != control.OperationRunning || approved.ConfirmationBy != control.ConfirmationScopedPermission {
		t.Fatal(approved, err)
	}
	permissions, err := st.ListControlPermissions(t.Context(), origin)
	if err != nil || len(permissions) != 1 {
		t.Fatal(permissions, err)
	}
	p := permissions[0]
	if p.Scope != control.PermissionScopeSessionWorkers || p.SessionKey != "organizer" || p.Limit != 0 || p.Used != 1 {
		t.Fatalf("wrong worker session permission: %+v", p)
	}
	return p
}

func TestWorkerDelegationCoversVariedLaunchesAndOwnedTaskLifecycle(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state.sqlite")
	st, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ctx, origin := t.Context(), t.TempDir()
	approveWorkerSession(t, st, origin)
	// Existing grants retain their exact session identity when the store reopens.
	st.Close()
	st, err = Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	// A manual confirmation in another session must not block authorized work.
	permissionOperation(t, st, "unrelated", "other-session", origin, origin, control.CapabilityTodoAdd, nil)
	if _, _, err := st.ClaimNextControlOperation(ctx); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		fields := map[string]any{"title": fmt.Sprintf("Worker %d", i), "prompt": "Implement another task", "structured_results": i%2 == 0, "repository_write": i%2 != 0}
		if i%2 == 0 {
			fields["provider"], fields["model"] = "claude_code", "another-model"
		} else {
			fields["provider"], fields["model"] = "codex", "different-model"
		}
		op := permissionOperation(t, st, fmt.Sprintf("launch-%d", i), "organizer", origin, origin, control.CapabilityAgentTaskCreate, fields)
		if allowed, err := st.PermissionAllowsOperation(ctx, op); err != nil || !allowed {
			t.Fatal("worker session did not cover launch", allowed, err)
		}
		claimed, found, err := st.ClaimNextControlOperation(ctx)
		if err != nil || !found || claimed.ID != op.ID {
			t.Fatalf("authorized launch stuck behind manual review: %v %v %+v", found, err, claimed)
		}
		if _, automatic, err := st.ConfirmControlPermission(ctx, op.ID); err != nil || !automatic {
			t.Fatal(automatic, err)
		}
		if _, automatic, err := st.ConfirmControlPermission(ctx, op.ID); err != nil || automatic {
			t.Fatal("replay reauthorized", automatic, err)
		}
	}
	task, err := st.CreateAgentTask(ctx, model.CreateAgentTaskInput{
		Title: "Owned worker", Kind: model.AgentTaskKindAgent, OriginProjectPath: "/repo-root",
		OriginWorktreePath: origin, OriginProvider: model.SessionSourceCodex, OriginSessionKey: "organizer",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		cap    control.CapabilityName
		fields map[string]any
	}{
		{control.CapabilityAgentTaskContinue, map[string]any{"task_id": task.ID, "prompt": "Continue", "session_mode": "resume_or_new", "provider": "claude_code", "model": "changed-worker-model"}},
		{control.CapabilityAgentTaskClose, map[string]any{"task_id": task.ID, "status": "waiting", "summary": "Ready for review"}},
		{control.CapabilityAgentTaskClose, map[string]any{"task_id": task.ID, "status": "completed", "summary": "Reviewed"}},
	} {
		op := permissionOperation(t, st, fmt.Sprintf("lifecycle-%s-%v", test.cap, test.fields["status"]), "organizer", origin, origin, test.cap, test.fields)
		if _, found, err := st.ClaimNextControlOperation(ctx); err != nil || !found {
			t.Fatal(found, err)
		}
		if _, automatic, err := st.ConfirmControlPermission(ctx, op.ID); err != nil || !automatic {
			t.Fatal("owned lifecycle request was not authorized", automatic, err)
		}
	}
	permissions, err := st.ListControlPermissions(ctx, origin)
	if err != nil || len(permissions) != 1 || permissions[0].Used != 10 {
		t.Fatal("expected ten distinct authorized uses", permissions, err)
	}
}

func TestWorkerDelegationDoesNotEscapeCallerOrRevocation(t *testing.T) {
	st := supervisionStore(t)
	ctx, origin := t.Context(), t.TempDir()
	p := approveWorkerSession(t, st, origin)
	op := control.Operation{ProjectPath: origin, Provider: "codex", SessionKey: "organizer", Invocation: control.Invocation{
		Capability: control.CapabilityAgentTaskCreate, Args: json.RawMessage(`{"title":"Worker","prompt":"Implement"}`),
	}}
	for _, change := range []func(*control.Operation){
		func(op *control.Operation) { op.SessionKey = "worker-session" },
		func(op *control.Operation) { op.ProjectPath = origin + "-sibling" },
		func(op *control.Operation) { op.Provider = "claude_code" },
	} {
		other := op
		change(&other)
		if allowed, err := st.PermissionAllowsOperation(ctx, other); err != nil || allowed {
			t.Fatal("grant escaped caller scope", allowed, err)
		}
	}
	next := permissionOperation(t, st, "next-worker", "organizer", origin, origin, control.CapabilityAgentTaskCreate, map[string]any{"title": "Next worker"})
	if _, found, err := st.ClaimNextControlOperation(ctx); err != nil || !found {
		t.Fatal(found, err)
	}
	if err := st.RevokeControlPermission(ctx, origin, p.ID); err != nil {
		t.Fatal(err)
	}
	if current, automatic, err := st.ConfirmControlPermission(ctx, next.ID); err != nil || automatic || current.Status != control.OperationWaitingForConfirmation {
		t.Fatal("revocation after claim did not take effect", current, automatic, err)
	}
}

func TestWorkerDelegationChecksTaskOwnershipAndExplicitStops(t *testing.T) {
	st := supervisionStore(t)
	ctx, origin := t.Context(), t.TempDir()
	approveWorkerSession(t, st, origin)
	for _, test := range []struct {
		name   string
		mutate func(*model.CreateAgentTaskInput)
	}{
		{"other caller", func(i *model.CreateAgentTaskInput) { i.OriginSessionKey = "another-caller" }},
		{"other checkout", func(i *model.CreateAgentTaskInput) { i.OriginWorktreePath = origin + "-sibling" }},
		{"other provider", func(i *model.CreateAgentTaskInput) { i.OriginProvider = model.SessionSourceClaudeCode }},
		{"stopped", func(i *model.CreateAgentTaskInput) { i.Workflow.CallerStopped = true }},
		{"revoked corrections", func(i *model.CreateAgentTaskInput) { i.Workflow.SupervisionRevoked = true }},
		{"trashed", func(i *model.CreateAgentTaskInput) { i.Status = model.AgentTaskStatusArchived }},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := model.CreateAgentTaskInput{Title: "Worker", Kind: model.AgentTaskKindAgent, OriginWorktreePath: origin,
				OriginProjectPath: origin, OriginProvider: model.SessionSourceCodex, OriginSessionKey: "organizer"}
			test.mutate(&input)
			task, err := st.CreateAgentTask(ctx, input)
			if err != nil {
				t.Fatal(err)
			}
			for _, inv := range []control.Invocation{
				{Capability: control.CapabilityAgentTaskContinue, Args: json.RawMessage(fmt.Sprintf(`{"task_id":%q,"prompt":"Continue","session_mode":"resume_or_new"}`, task.ID))},
				{Capability: control.CapabilityAgentTaskCreate, Args: json.RawMessage(fmt.Sprintf(`{"title":"Nested worker","kind":"subagent","parent_task_id":%q}`, task.ID))},
			} {
				op := control.Operation{ProjectPath: origin, Provider: "codex", SessionKey: "organizer", Invocation: inv}
				if allowed, err := st.PermissionAllowsOperation(ctx, op); err != nil || allowed {
					t.Fatal("ineligible worker inherited grant", allowed, err)
				}
			}
		})
	}
}

func TestExistingActionPermissionsMigrateWithoutBroadening(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE control_permissions (
		id INTEGER PRIMARY KEY AUTOINCREMENT, origin TEXT NOT NULL, provider TEXT NOT NULL,
		session_key TEXT NOT NULL DEFAULT '', capability TEXT NOT NULL, target TEXT NOT NULL,
		constraints_json TEXT NOT NULL, use_limit INTEGER NOT NULL DEFAULT 0, used INTEGER NOT NULL DEFAULT 0,
		UNIQUE(origin,provider,session_key,capability,target,constraints_json));
		INSERT INTO control_permissions(origin,provider,session_key,capability,target,constraints_json,use_limit,used)
		VALUES('/caller','codex','organizer','agent_task.create','/caller','{}',3,2)`)
	db.Close()
	if err != nil {
		t.Fatal(err)
	}
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	permissions, err := st.ListControlPermissions(t.Context(), "/caller")
	if err != nil || len(permissions) != 1 || permissions[0].Scope != "" || permissions[0].Limit != 3 || permissions[0].Used != 2 {
		t.Fatal("existing permission changed on migration", permissions, err)
	}
}
