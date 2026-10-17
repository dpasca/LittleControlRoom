package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"lcroom/internal/config"
	"lcroom/internal/control"
	"lcroom/internal/events"
	"lcroom/internal/model"
	"lcroom/internal/store"
	"lcroom/internal/todocontrol"
	"lcroom/internal/todocontrol/ownertest"
)

func TestLinkedCheckoutLaunchOwnsOneRootTodoAndReplaysAfterRestart(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(map[bool]string{false: "new-todo", true: "existing-1618"}[existing], func(t *testing.T) {
			f := ownertest.New(t)
			svc := newOwnerTestService(t, f.Store)
			caller, err := f.Store.AddTodo(t.Context(), f.Root, "release work")
			if err != nil {
				t.Fatal(err)
			}
			if err := f.Store.AttachTodoWorkSession(t.Context(), caller.ID, f.Caller, model.SessionSourceCodex, "caller", model.TodoWorkStateIdle, time.Now()); err != nil {
				t.Fatal(err)
			}
			before, _ := f.Store.GetTodo(t.Context(), caller.ID)
			input := control.TodoCreateWorktreeAndStartEngineerInput{RequestID: "launch", ProjectPath: f.Caller, TodoText: "worker task", Prompt: "original prompt", Provider: control.ProviderCodex}
			if existing {
				item, err := f.Store.AddTodo(t.Context(), f.Root, "existing worker task")
				if err != nil {
					t.Fatal(err)
				}
				input.TodoID = item.ID
			}
			before, _ = f.Store.GetTodo(t.Context(), caller.ID)
			todo, err := svc.AddTodoForLaunch(t.Context(), input)
			if err != nil || todo.ProjectPath != f.Root || (existing && todo.ID != input.TodoID) {
				t.Fatalf("todo=%+v err=%v", todo, err)
			}
			result, err := svc.CreateTodoWorktree(t.Context(), CreateTodoWorktreeRequest{ProjectPath: f.Caller, TodoID: todo.ID})
			if err != nil || result.RootProjectPath != f.Root || result.WorktreePath == f.Caller {
				t.Fatalf("worktree=%+v err=%v", result, err)
			}
			if err := f.Store.ClaimTodoEngineerLaunch(t.Context(), todo.ID); err != nil {
				t.Fatal(err)
			}
			if err := svc.MarkTodoWorkStarted(t.Context(), result.WorktreePath, todo.ID, model.SessionSourceCodex, "worker-session", time.Now()); err != nil {
				t.Fatal(err)
			}
			svc = newOwnerTestService(t, f.Store)
			replayed, err := svc.AddTodoForLaunch(t.Context(), input)
			if err != nil || replayed.ID != todo.ID || !replayed.LaunchEngineerClaimed || replayed.WorkSessionID != "codex:worker-session" {
				t.Fatalf("replay=%+v err=%v", replayed, err)
			}
			if err := f.Store.ClaimTodoEngineerLaunch(t.Context(), todo.ID); err == nil {
				t.Fatal("duplicate engineer claim accepted")
			}
			after, _ := f.Store.GetTodo(t.Context(), caller.ID)
			if !reflect.DeepEqual(before, after) {
				t.Fatalf("caller changed: before=%+v after=%+v", before, after)
			}
			root, _, _ := f.Store.ListOpenTodosForReview(t.Context(), f.Root)
			linked, _, _ := f.Store.ListOpenTodosForReview(t.Context(), f.Caller)
			if len(root) != 2 || len(linked) != 0 {
				t.Fatalf("root=%+v linked=%+v", root, linked)
			}
			journal, err := f.Store.TodoLaunchInput(t.Context(), todo.ID)
			if err != nil || journal.ProjectPath != f.Caller || journal.Prompt != input.Prompt {
				t.Fatalf("provenance=%+v err=%v", journal, err)
			}
		})
	}
}

// The IDs and links reproduce the 2026-10-16 incident without touching live FCX.
func TestOwnerRepair1792Preserves1791AndLaunchProvenance(t *testing.T) {
	f := ownertest.New(t)
	svc := newOwnerTestService(t, f.Store)
	raw, err := sql.Open("sqlite", f.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	for _, row := range []struct {
		id          int64
		owner, text string
	}{{1791, f.Root, "Release readiness"}, {1792, f.Caller, "Improve tracer visibility"}} {
		if _, err := raw.Exec(`INSERT INTO project_todos(id,project_path,text,done,position,created_at,updated_at) VALUES(?,?,?,0,0,123,456)`, row.id, row.owner, row.text); err != nil {
			t.Fatal(err)
		}
	}
	input := control.TodoCreateWorktreeAndStartEngineerInput{RequestID: "incident-launch", ProjectPath: f.Caller, TodoText: "Improve tracer visibility", Prompt: "original FCX task", Provider: control.ProviderCodex}
	historical, _ := json.Marshal(input)
	if _, err := raw.Exec(`INSERT INTO todo_launch_requests(request_id,todo_id,input_json) VALUES(?,1792,?)`, input.RequestID, string(historical)); err != nil {
		t.Fatal(err)
	}
	if err := f.Store.SaveTodoWorktreePlan(t.Context(), store.TodoWorktreePlan{TodoID: 1792, RootPath: f.Root, WorktreePath: f.Worker, Branch: "worker", ParentBranch: "master"}); err != nil {
		t.Fatal(err)
	}
	if err := f.Store.MarkTodoWorktreeReady(t.Context(), 1792); err != nil {
		t.Fatal(err)
	}
	if err := f.Store.ClaimTodoEngineerLaunch(t.Context(), 1792); err != nil {
		t.Fatal(err)
	}
	for _, link := range []struct {
		id            int64
		path, session string
	}{{1791, f.Caller, "release-session"}, {1792, f.Worker, "tracer-session"}} {
		if err := f.Store.AttachTodoWorkSession(t.Context(), link.id, link.path, model.SessionSourceCodex, link.session, model.TodoWorkStateIdle, time.Unix(789, 0)); err != nil {
			t.Fatal(err)
		}
		if _, err := raw.Exec(`UPDATE projects SET worktree_origin_todo_id=? WHERE path=?`, link.id, link.path); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := raw.Exec(`INSERT INTO todo_attachments(todo_id,kind,path,position,created_at) VALUES(1792,'file','evidence.json',0,234)`); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(f.Worker, "uncommitted.txt")
	if err := os.WriteFile(sentinel, []byte("preserve worker changes\n"), 0600); err != nil {
		t.Fatal(err)
	}
	before1791, _ := f.Store.GetTodo(t.Context(), 1791)
	before1792, _ := f.Store.GetTodo(t.Context(), 1792)
	beforePlan, _ := f.Store.TodoWorktreePlan(t.Context(), 1792)
	beforeJournal, _ := f.Store.TodoLaunchInput(t.Context(), 1792)
	repair := control.TodoRepairOwnerInput{RequestID: "repair-1792", TodoID: 1792, ExpectedOwnerPath: f.Caller, RepositoryPath: f.Root}
	bound, _, err := todocontrol.InspectRepair(t.Context(), f.Store, repair)
	if err != nil {
		t.Fatal(err)
	}
	// Failure after the owner update must roll back owner, ordering and audit.
	if _, err := raw.Exec(`CREATE TRIGGER fail_repair BEFORE INSERT ON todo_owner_repairs BEGIN SELECT RAISE(ABORT,'injected audit failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RepairTodoOwner(t.Context(), bound); err == nil {
		t.Fatal("injected failure accepted")
	}
	rolledBack, _ := f.Store.GetTodo(t.Context(), 1792)
	if !reflect.DeepEqual(before1792, rolledBack) {
		t.Fatalf("partial repair: %+v", rolledBack)
	}
	if _, err := raw.Exec(`DROP TRIGGER fail_repair`); err != nil {
		t.Fatal(err)
	}
	if changed, err := svc.RepairTodoOwner(t.Context(), bound); err != nil || !changed {
		t.Fatalf("changed=%t err=%v", changed, err)
	}
	after1791, _ := f.Store.GetTodo(t.Context(), 1791)
	after1792, _ := f.Store.GetTodo(t.Context(), 1792)
	if !reflect.DeepEqual(before1791, after1791) {
		t.Fatal("root TODO #1791 changed")
	}
	if after1792.ProjectPath != f.Root || after1792.Position <= after1791.Position {
		t.Fatalf("owner/order=%+v", after1792)
	}
	after1792.ProjectPath, after1792.Position = before1792.ProjectPath, before1792.Position
	if !reflect.DeepEqual(before1792, after1792) {
		t.Fatalf("metadata changed: before=%+v after=%+v", before1792, after1792)
	}
	plan, _ := f.Store.TodoWorktreePlan(t.Context(), 1792)
	journal, _ := f.Store.TodoLaunchInput(t.Context(), 1792)
	if !reflect.DeepEqual(beforePlan, plan) || !reflect.DeepEqual(beforeJournal, journal) {
		t.Fatal("historical launch provenance changed")
	}
	if replayed, err := svc.AddTodoForLaunch(t.Context(), input); err != nil || replayed.ID != 1792 || replayed.WorkSessionID != "tracer-session" {
		t.Fatalf("historical replay=%+v err=%v", replayed, err)
	}
	if changed, err := svc.RepairTodoOwner(t.Context(), repair); err != nil || changed {
		t.Fatalf("exact retry changed=%t err=%v", changed, err)
	}
	stale := bound
	stale.RequestID = "stale-repair"
	if _, err := svc.RepairTodoOwner(t.Context(), stale); err == nil {
		t.Fatal("stale source owner accepted")
	}
	for path, id := range map[string]int64{f.Caller: 1791, f.Worker: 1792} {
		p, err := f.Store.GetProjectSummary(t.Context(), path, true)
		if err != nil || p.WorktreeOriginTodoID != id {
			t.Fatalf("origin=%+v err=%v", p, err)
		}
	}
	bytes, _ := os.ReadFile(sentinel)
	if string(bytes) != "preserve worker changes\n" {
		t.Fatal("worker files changed")
	}
	var auditCount int
	if err := raw.QueryRow(`SELECT COUNT(*) FROM events WHERE event_type='todo_owner_repair'`).Scan(&auditCount); err != nil || auditCount != 2 {
		t.Fatalf("audit=%d err=%v", auditCount, err)
	}
	root, _, _ := f.Store.ListOpenTodosForReview(t.Context(), f.Root)
	linked, _, _ := f.Store.ListOpenTodosForReview(t.Context(), f.Caller)
	if len(root) != 2 || len(linked) != 0 {
		t.Fatalf("owner views root=%+v linked=%+v", root, linked)
	}
	// The original journal must retain the historical wrong owner spelling.
	encoded, _ := json.Marshal(journal)
	if journal.ProjectPath != f.Caller {
		t.Fatalf("rewritten provenance: %s", encoded)
	}
}

func TestOwnerRepairRefusesChangedReviewAndCrossRepositoryWorker(t *testing.T) {
	f := ownertest.New(t)
	svc := newOwnerTestService(t, f.Store)
	todo, err := f.Store.AddTodo(t.Context(), f.Caller, "wrong owner")
	if err != nil {
		t.Fatal(err)
	}
	input, _, err := todocontrol.InspectRepair(t.Context(), f.Store, control.TodoRepairOwnerInput{RequestID: "repair", TodoID: todo.ID, ExpectedOwnerPath: f.Caller, RepositoryPath: f.Root})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Store.UpdateTodo(t.Context(), todo.ID, "changed after review"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RepairTodoOwner(t.Context(), input); err == nil {
		t.Fatal("stale revision accepted")
	}
	other := ownertest.New(t)
	if err := f.Store.AttachTodoWorkSession(t.Context(), todo.ID, other.Worker, model.SessionSourceCodex, "other", model.TodoWorkStateIdle, time.Now()); err != nil {
		t.Fatal(err)
	}
	input.ExpectedRevision = ""
	if _, err := svc.RepairTodoOwner(t.Context(), input); err == nil {
		t.Fatal("cross-repository worker accepted")
	}
}

func TestLinkedCheckoutLaunchResumesPartialPreparationWithOriginalDestination(t *testing.T) {
	f := ownertest.New(t)
	svc := newOwnerTestService(t, f.Store)
	input := control.TodoCreateWorktreeAndStartEngineerInput{RequestID: "linked-partial", ProjectPath: f.Caller, TodoText: "Recover this task", Prompt: "Original prompt", Provider: control.ProviderCodex}
	todo, err := svc.AddTodoForLaunch(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", f.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, err := raw.Exec(`CREATE TRIGGER fail_ready BEFORE UPDATE ON todo_worktree_plans WHEN NEW.ready=1 BEGIN SELECT RAISE(ABORT,'pause preparation'); END`); err != nil {
		t.Fatal(err)
	}
	result, err := svc.CreateTodoWorktree(t.Context(), CreateTodoWorktreeRequest{ProjectPath: f.Caller, TodoID: todo.ID})
	if err == nil || result.WorktreePath == "" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	marker := filepath.Join(result.WorktreePath, "keep-during-retry.txt")
	if err := os.WriteFile(marker, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`DROP TRIGGER fail_ready`); err != nil {
		t.Fatal(err)
	}
	svc = newOwnerTestService(t, f.Store)
	retry, err := svc.AddTodoForLaunch(t.Context(), input)
	if err != nil || retry.ID != todo.ID {
		t.Fatalf("retry=%+v err=%v", retry, err)
	}
	prepared, err := svc.CreateTodoWorktree(t.Context(), CreateTodoWorktreeRequest{ProjectPath: f.Caller, TodoID: todo.ID})
	if err != nil || prepared.WorktreePath != result.WorktreePath {
		t.Fatalf("prepared=%+v err=%v", prepared, err)
	}
	bytes, err := os.ReadFile(marker)
	if err != nil || string(bytes) != "keep" {
		t.Fatalf("worker file=%q err=%v", bytes, err)
	}
	root, _, _ := f.Store.ListOpenTodosForReview(t.Context(), f.Root)
	caller, _, _ := f.Store.ListOpenTodosForReview(t.Context(), f.Caller)
	if len(root) != 1 || len(caller) != 0 {
		t.Fatalf("root=%+v caller=%+v", root, caller)
	}
}

func newOwnerTestService(t *testing.T, st *store.Store) *Service {
	t.Helper()
	svc := New(config.Default(), st, events.NewBus(), nil)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if err := svc.WaitForAsyncProjectRefreshes(ctx); err != nil {
			t.Errorf("drain owner test refreshes: %v", err)
		}
	})
	return svc
}
