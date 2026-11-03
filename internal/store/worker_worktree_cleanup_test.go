package store

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"lcroom/internal/control"
	"lcroom/internal/model"
)

func cleanupWorker(t *testing.T, st *Store, origin, path string) model.AgentTask {
	t.Helper()
	if err := st.UpsertProjectState(t.Context(), model.ProjectState{
		Path: path, Name: "worker", WorktreeKind: model.WorktreeKindLinked, WorktreeRootPath: "/repo",
	}); err != nil {
		t.Fatal(err)
	}
	task, err := st.CreateAgentTask(t.Context(), model.CreateAgentTaskInput{
		Title: "Worker", WorkspacePath: path, OriginProjectPath: "/repo", OriginWorktreePath: origin,
		OriginProvider: model.SessionSourceCodex, OriginSessionKey: "organizer",
		Repository: model.AgentTaskRepository{Isolated: true, Root: path},
	})
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func cleanupOperation(t *testing.T, st *Store, id, origin, path string) control.Operation {
	t.Helper()
	return permissionOperation(t, st, id, "organizer", origin, path, control.CapabilityWorktreeRemove,
		map[string]any{"worktree_path": path})
}

func TestWorkerCleanupSessionApprovalQueueAndRevocation(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state.sqlite")
	st, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ctx, origin := t.Context(), t.TempDir()
	firstPath, secondPath := t.TempDir(), t.TempDir()
	cleanupWorker(t, st, origin, firstPath)
	cleanupWorker(t, st, origin, secondPath)
	first := cleanupOperation(t, st, "first-delete", origin, firstPath)
	if yes, err := st.WorkerWorktreeCleanupEligible(ctx, first); err != nil || !yes {
		t.Fatal("owned worktree not eligible", yes, err)
	}
	if yes, err := st.PermissionAllowsOperation(ctx, first); err != nil || yes {
		t.Fatal("permission exists before approval", yes, err)
	}
	if _, _, err := st.ClaimNextControlOperation(ctx); err != nil {
		t.Fatal(err)
	}
	// Generic session and saved action approvals must not widen deletion scope.
	for _, sessionOnly := range []bool{false, true} {
		if _, err := st.ApproveControlPermission(ctx, first.ID, sessionOnly); err == nil {
			t.Fatal("generic permission authorized deletion")
		}
	}
	approved, err := st.ApproveWorkerWorktreeCleanup(ctx, first.ID)
	if err != nil || approved.Status != control.OperationRunning || approved.ConfirmationBy != control.ConfirmationScopedPermission {
		t.Fatal(approved, err)
	}
	if _, err := st.ApproveWorkerWorktreeCleanup(ctx, first.ID); err == nil {
		t.Fatal("duplicate approval succeeded")
	}
	st.Close()
	st, err = Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	permissions, err := st.ListControlPermissions(ctx, origin)
	if err != nil || len(permissions) != 1 || permissions[0].Scope != control.PermissionScopeSessionWorkerCleanup || permissions[0].SessionKey != "organizer" || permissions[0].Limit != 0 {
		t.Fatal(permissions, err)
	}
	// A manual review in another session cannot block authorized cleanup.
	permissionOperation(t, st, "unrelated", "other-session", origin, origin, control.CapabilityTodoAdd, nil)
	if _, _, err := st.ClaimNextControlOperation(ctx); err != nil {
		t.Fatal(err)
	}
	second := cleanupOperation(t, st, "second-delete", origin, secondPath)
	for _, change := range []func(*control.Operation){
		func(op *control.Operation) { op.SessionKey = "worker-session" },
		func(op *control.Operation) { op.Provider = "claude_code" },
		func(op *control.Operation) { op.ProjectPath = origin + "-sibling" },
	} {
		other := second
		change(&other)
		if yes, err := st.PermissionAllowsOperation(ctx, other); err != nil || yes {
			t.Fatal("cleanup grant escaped its caller", yes, err)
		}
	}
	claimed, found, err := st.ClaimNextControlOperation(ctx)
	if err != nil || !found || claimed.ID != second.ID {
		t.Fatal("cleanup stuck behind manual review", claimed, found, err)
	}
	if _, automatic, err := st.ConfirmControlPermission(ctx, second.ID); err != nil || !automatic {
		t.Fatal("second worker asks again", automatic, err)
	}
	if _, automatic, err := st.ConfirmControlPermission(ctx, second.ID); err != nil || automatic {
		t.Fatal("replay consumed another use", automatic, err)
	}
	third := cleanupOperation(t, st, "third-delete", origin, secondPath)
	if _, found, err := st.ClaimNextControlOperation(ctx); err != nil || !found {
		t.Fatal(found, err)
	}
	if err := st.RevokeControlPermission(ctx, origin, permissions[0].ID); err != nil {
		t.Fatal(err)
	}
	if current, automatic, err := st.ConfirmControlPermission(ctx, third.ID); err != nil || automatic || current.Status != control.OperationWaitingForConfirmation {
		t.Fatal("revocation after claim lost", current, automatic, err)
	}
	permissions, err = st.ListControlPermissions(ctx, origin)
	if err != nil || len(permissions) != 0 {
		t.Fatal(permissions, err)
	}
}

func TestWorkerDelegationDoesNotAuthorizeWorktreeCleanup(t *testing.T) {
	st := supervisionStore(t)
	origin, path := t.TempDir(), t.TempDir()
	approveWorkerSession(t, st, origin)
	cleanupWorker(t, st, origin, path)
	op := cleanupOperation(t, st, "delete-worker", origin, path)
	if yes, err := st.PermissionAllowsOperation(t.Context(), op); err != nil || yes {
		t.Fatal("worker delegation widened to permanent deletion", yes, err)
	}
}

func TestWorkerCleanupOwnershipAndCallerBoundaries(t *testing.T) {
	for _, provider := range []string{"codex", "claude_code", "opencode", "lcagent"} {
		t.Run(provider, func(t *testing.T) {
			for _, name := range []string{"owned", "legacy", "other session", "other provider", "other origin", "missing caller", "ordinary worktree", "shared lease", "arbitrary child", "primary", "caller", "ancestor", "ambiguous", "changed ownership"} {
				t.Run(name, func(t *testing.T) {
					st := supervisionStore(t)
					ctx, origin, path := t.Context(), t.TempDir(), t.TempDir()
					task := cleanupWorker(t, st, origin, path)
					if _, err := st.db.ExecContext(ctx, `UPDATE agent_tasks SET origin_provider=? WHERE id=?`, provider, task.ID); err != nil {
						t.Fatal(err)
					}
					op := cleanupOperation(t, st, "delete", origin, path)
					op.Provider = provider
					if _, err := st.db.ExecContext(ctx, `UPDATE control_operations SET provider=? WHERE id=?`, provider, op.ID); err != nil {
						t.Fatal(err)
					}
					allowed := name == "owned" || name == "legacy"
					var err error
					switch name {
					case "legacy":
						path = filepath.Join(path, "repo")
						if err := st.UpsertProjectState(ctx, model.ProjectState{Path: path, Name: "repo", WorktreeKind: model.WorktreeKindLinked, WorktreeRootPath: "/repo"}); err != nil {
							t.Fatal(err)
						}
						op.Invocation.Args, _ = json.Marshal(control.WorktreeRemoveInput{WorktreePath: path})
						_, err = st.db.ExecContext(ctx, `UPDATE agent_tasks SET repository_json='{}' WHERE id=?`, task.ID)
					case "missing caller":
						op.SessionKey = ""
					case "other session":
						op.SessionKey = "replacement"
					case "other provider":
						op.Provider = "another-provider"
					case "other origin":
						op.ProjectPath = origin + "-sibling"
					case "ordinary worktree":
						_, err = st.db.ExecContext(ctx, `DELETE FROM agent_tasks WHERE id=?`, task.ID)
					case "shared lease":
						_, err = st.db.ExecContext(ctx, `UPDATE agent_tasks SET repository_json=? WHERE id=?`, encodeAgentTaskRepository(model.AgentTaskRepository{Root: path, Write: true}), task.ID)
					case "arbitrary child":
						_, err = st.db.ExecContext(ctx, `UPDATE agent_tasks SET workspace_path=?, repository_json='{}' WHERE id=?`, filepath.Dir(path), task.ID)
					case "primary":
						_, err = st.db.ExecContext(ctx, `UPDATE projects SET worktree_kind='root' WHERE path=?`, path)
					case "caller":
						op.ProjectPath = path
					case "ancestor":
						op.ProjectPath = filepath.Join(path, "child")
					case "ambiguous":
						cleanupWorker(t, st, origin, path)
					case "changed ownership":
						if _, _, err := st.ClaimNextControlOperation(ctx); err != nil {
							t.Fatal(err)
						}
						if _, err := st.ApproveWorkerWorktreeCleanup(ctx, op.ID); err != nil {
							t.Fatal(err)
						}
						_, err = st.db.ExecContext(ctx, `UPDATE agent_tasks SET origin_session_key='replacement' WHERE id=?`, task.ID)
					}
					if err != nil {
						t.Fatal(err)
					}
					if _, err := st.db.ExecContext(ctx, `UPDATE control_operations SET project_path=?,provider=?,session_key=?,args_json=? WHERE id=?`,
						op.ProjectPath, op.Provider, op.SessionKey, string(op.Invocation.Args), op.ID); err != nil {
						t.Fatal(err)
					}
					if yes, err := st.WorkerWorktreeCleanupEligible(ctx, op); err != nil || yes != allowed {
						t.Fatal("ownership eligibility", yes, err)
					}
					if !allowed {
						if yes, err := st.PermissionAllowsOperation(ctx, op); err != nil || yes {
							t.Fatal("unauthorized target covered", yes, err)
						}
						if name != "changed ownership" {
							if _, _, err := st.ClaimNextControlOperation(ctx); err != nil {
								t.Fatal(err)
							}
							if _, err := st.ApproveWorkerWorktreeCleanup(ctx, op.ID); err == nil {
								t.Fatal("ineligible target saved a permission")
							}
						}
					}
				})
			}
		})
	}
}
