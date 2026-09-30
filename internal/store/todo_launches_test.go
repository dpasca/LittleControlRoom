package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"lcroom/internal/control"
	"lcroom/internal/model"
	"modernc.org/sqlite"
)

type injectedBusy struct{}

func (injectedBusy) Error() string { return "database is locked (5) (SQLITE_BUSY)" }
func (injectedBusy) Code() int     { return 5 }

type faultSQLiteDriver struct {
	mu      sync.Mutex
	pending map[string]bool
}

func (d *faultSQLiteDriver) Open(name string) (driver.Conn, error) {
	conn, err := (&sqlite.Driver{}).Open(name)
	if err != nil {
		return nil, err
	}
	return &retrySQLiteConn{Conn: &faultSQLiteConn{Conn: conn, faults: d}}, nil
}

type faultSQLiteConn struct {
	driver.Conn
	faults *faultSQLiteDriver
}

func (c *faultSQLiteConn) fail(query string) bool {
	c.faults.mu.Lock()
	defer c.faults.mu.Unlock()
	for needle, pending := range c.faults.pending {
		if pending && strings.Contains(query, needle) {
			c.faults.pending[needle] = false
			return true
		}
	}
	return false
}
func (c *faultSQLiteConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	if c.fail("BEGIN IMMEDIATE") {
		return nil, injectedBusy{}
	}
	return c.Conn.(driver.ConnBeginTx).BeginTx(ctx, opts)
}
func (c *faultSQLiteConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if c.fail(query) {
		return nil, injectedBusy{}
	}
	return c.Conn.(driver.ExecerContext).ExecContext(ctx, query, args)
}
func (c *faultSQLiteConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if c.fail(query) {
		return nil, injectedBusy{}
	}
	return c.Conn.(driver.QueryerContext).QueryContext(ctx, query, args)
}

func TestConcurrentControlLaunchWritesRetryBusyAtEveryPersistenceStage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "launch.sqlite")
	st := openReviewTestStore(t, path)
	project := "/test/project"
	insertReviewTestProject(t, st, project)
	category, err := st.CreateProjectCategory(t.Context(), "Launches")
	if err != nil {
		t.Fatal(err)
	}
	faults := &faultSQLiteDriver{pending: map[string]bool{}}
	for _, statement := range []string{"BEGIN IMMEDIATE", "COMMIT", "INSERT INTO control_operations", "INSERT INTO project_todos", "INSERT INTO todo_launch_requests", "INSERT INTO todo_worktree_plans", "INSERT INTO projects", "INSERT INTO category_assignments", "SET worktree_initial_branch =", "SET ready = 1", "SET engineer_claimed = 1", "SET work_provider =", "SET status ="} {
		faults.pending[statement] = true
	}
	driverName := "fault-launch-" + fmt.Sprint(time.Now().UnixNano())
	sql.Register(driverName, faults)
	st.Close()
	db, err := sql.Open(driverName, sqliteDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(4)
	st = &Store{db: db}
	defer st.Close()
	const count = 6
	errs := make(chan error, count)
	for i := 0; i < count; i++ {
		go func() { errs <- runLaunchPersistence(t.Context(), st, project, category.ID, i) }()
	}
	for i := 0; i < count; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	faults.mu.Lock()
	for statement, pending := range faults.pending {
		if pending {
			t.Errorf("fault not exercised: %s", statement)
		}
	}
	faults.mu.Unlock()
	todos, _, err := st.ListOpenTodosForReview(t.Context(), project)
	if err != nil || len(todos) != count {
		t.Fatalf("todos=%d err=%v", len(todos), err)
	}
}

func runLaunchPersistence(ctx context.Context, st *Store, project, category string, i int) error {
	input := control.TodoCreateWorktreeAndStartEngineerInput{RequestID: fmt.Sprintf("launch-%d", i), ProjectPath: project, TodoText: fmt.Sprintf("Task %d", i), Prompt: "Implement this task", Provider: control.ProviderCodex}
	stage := "control operation"
	args, _ := json.Marshal(input)
	_, err := st.CreateControlOperation(ctx, control.Operation{ID: input.RequestID, ClientRequestID: input.RequestID, Source: "test", SessionKey: "caller", Invocation: control.Invocation{RequestID: input.RequestID, Capability: control.CapabilityTodoCreateWorktreeAndStartEngineer, Args: args}})
	if err != nil {
		return fmt.Errorf("stage %s: %w", stage, err)
	}
	stage = "TODO"
	todo, err := st.AddTodoForLaunch(ctx, input)
	if err != nil {
		return fmt.Errorf("stage %s: %w", stage, err)
	}
	stage = "TODO retry"
	retry, err := st.AddTodoForLaunch(ctx, input)
	if err != nil || retry.ID != todo.ID {
		return fmt.Errorf("retry duplicated TODO: %v", err)
	}
	stage = "worktree plan"
	path := fmt.Sprintf("%s--task-%d", project, i)
	if err := st.SaveTodoWorktreePlan(ctx, TodoWorktreePlan{TodoID: todo.ID, RootPath: project, WorktreePath: path, Branch: "task", ParentBranch: "master"}); err != nil {
		return fmt.Errorf("stage %s: %w", stage, err)
	}
	stage = "project"
	if err := st.UpsertProjectState(ctx, model.ProjectState{Path: path, Name: "task", PresentOnDisk: true, InScope: true, WorktreeKind: model.WorktreeKindLinked, WorktreeRootPath: project}); err != nil {
		return fmt.Errorf("stage %s: %w", stage, err)
	}
	stage = "category"
	if err := st.SetProjectsCategory(ctx, []string{path}, category); err != nil {
		return fmt.Errorf("stage %s: %w", stage, err)
	}
	stage = "metadata"
	if err := st.SetTodoWorktreeMetadata(ctx, path, "task", "master", "", todo.ID); err != nil {
		return fmt.Errorf("stage %s: %w", stage, err)
	}
	stage = "ready"
	if err := st.MarkTodoWorktreeReady(ctx, todo.ID); err != nil {
		return fmt.Errorf("stage %s: %w", stage, err)
	}
	stage = "claim"
	if err := st.ClaimTodoEngineerLaunch(ctx, todo.ID); err != nil {
		return fmt.Errorf("stage %s: %w", stage, err)
	}
	stage = "claim"
	if err := st.ClaimTodoEngineerLaunch(ctx, todo.ID); err == nil {
		return errors.New("second engineer claim succeeded")
	}
	stage = "session"
	if err := st.AttachTodoWorkSession(ctx, todo.ID, path, model.SessionSourceCodex, "session", model.TodoWorkStateWorking, time.Now()); err != nil {
		return fmt.Errorf("stage %s: %w", stage, err)
	}
	stage = "completion"
	_, err = st.UpdateControlOperationStatus(ctx, input.RequestID, control.OperationCompleted, nil, nil)
	return err
}

func TestSQLiteImmediateWriterWaitsWhileWALReadersContinue(t *testing.T) {
	st := openReviewTestStore(t, filepath.Join(t.TempDir(), "busy.sqlite"))
	defer st.Close()
	insertReviewTestProject(t, st, "/repo")
	ctx := t.Context()
	tx, err := st.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	// BEGIN itself reserves the writer, even before the first write.
	done := make(chan error, 1)
	go func() { done <- st.SetProjectsCategory(ctx, []string{"/repo"}, "") }()
	select {
	case err := <-done:
		t.Fatalf("writer did not wait: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	if _, err := st.GetTrackedProjectSummary(ctx, "/repo"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestSQLiteBusyBackoffBoundedAndCanceled(t *testing.T) {
	calls := 0
	_, err := retrySQLite(t.Context(), func() (int, error) { calls++; return 0, injectedBusy{} })
	if err == nil || calls != 4 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	calls = 0
	_, err = retrySQLite(ctx, func() (int, error) { calls++; cancel(); return 0, injectedBusy{} })
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
}

func TestLaunchRequestIdentitySurvivesReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "launch.sqlite")
	st := openReviewTestStore(t, path)
	insertReviewTestProject(t, st, "/repo")
	input := control.TodoCreateWorktreeAndStartEngineerInput{RequestID: "launch", ProjectPath: "/repo", TodoText: "Task", Provider: control.ProviderClaudeCode, Prompt: "Original prompt"}
	todo, err := st.AddTodoForLaunch(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	st.Close()
	st = openReviewTestStore(t, path)
	defer st.Close()
	retry, err := st.AddTodoForLaunch(t.Context(), input)
	if err != nil || retry.ID != todo.ID {
		t.Fatalf("retry=%+v err=%v", retry, err)
	}
	input.Prompt = "Different prompt"
	if _, err := st.AddTodoForLaunch(t.Context(), input); err == nil {
		t.Fatal("rebound request accepted")
	}
}

func TestDeferredSnapshotUpgradeReproducesBusy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "snapshot.sqlite")
	st := openReviewTestStore(t, path)
	defer st.Close()
	insertReviewTestProject(t, st, "/repo")
	raw, err := sql.Open("sqlite", strings.ReplaceAll(sqliteDSN(path), "_txlock=immediate&", ""))
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	tx, err := raw.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRow(`SELECT 1 FROM projects WHERE path='/repo'`).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	writer, err := st.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Rollback()
	if _, err := writer.Exec(`UPDATE projects SET name = 'other writer' WHERE path='/repo'`); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	_, err = tx.Exec(`UPDATE projects SET updated_at=updated_at+1 WHERE path='/repo'`)
	var busy interface{ Code() int }
	if !errors.As(err, &busy) || busy.Code() != 5 {
		t.Fatalf("read upgrade error=%v, want SQLITE_BUSY", err)
	}
	if time.Since(started) >= sqliteBusyTimeout {
		t.Fatal("expected an immediate upgrade failure, bypassing busy_timeout")
	}
	if err := writer.Commit(); err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(`UPDATE projects SET updated_at=updated_at+1 WHERE path='/repo'`)
	var coded interface{ Code() int }
	if !errors.As(err, &coded) || coded.Code() != 517 {
		t.Fatalf("snapshot upgrade error=%v, want SQLITE_BUSY_SNAPSHOT", err)
	}
}

func TestLaunchRequestAndTodoRollbackTogether(t *testing.T) {
	st := openReviewTestStore(t, filepath.Join(t.TempDir(), "rollback.sqlite"))
	defer st.Close()
	insertReviewTestProject(t, st, "/repo")
	if _, err := st.db.Exec(`CREATE TRIGGER reject_request BEFORE INSERT ON todo_launch_requests BEGIN SELECT RAISE(ABORT, 'request failure'); END`); err != nil {
		t.Fatal(err)
	}
	input := control.TodoCreateWorktreeAndStartEngineerInput{RequestID: "request", ProjectPath: "/repo", TodoText: "Task"}
	if _, err := st.AddTodoForLaunch(t.Context(), input); err == nil {
		t.Fatal("expected failure")
	}
	todos, _, err := st.ListOpenTodosForReview(t.Context(), "/repo")
	if err != nil || len(todos) != 0 {
		t.Fatalf("leaked todos=%+v err=%v", todos, err)
	}
	if _, err := st.db.Exec(`DROP TRIGGER reject_request`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddTodoForLaunch(t.Context(), input); err != nil {
		t.Fatal(err)
	}
}

func TestFailedLaunchProposalRetryRetainsOperationIdentity(t *testing.T) {
	st := openReviewTestStore(t, filepath.Join(t.TempDir(), "retry.sqlite"))
	defer st.Close()
	input := control.TodoCreateWorktreeAndStartEngineerInput{ProjectPath: "/repo", TodoText: "Task", Prompt: "Task", Provider: control.ProviderCodex}
	args, _ := json.Marshal(input)
	operation := control.Operation{ID: "original", ClientRequestID: "client-key", Source: "mcp", SessionKey: "caller", Invocation: control.Invocation{RequestID: "original", Capability: control.CapabilityTodoCreateWorktreeAndStartEngineer, Args: args}}
	if _, err := st.CreateControlOperation(t.Context(), operation); err != nil {
		t.Fatal(err)
	}
	insertReviewTestProject(t, st, "/repo")
	input.RequestID = "original"
	if _, err := st.AddTodoForLaunch(t.Context(), input); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpdateControlOperationStatus(t.Context(), "original", control.OperationFailed, nil, injectedBusy{}); err != nil {
		t.Fatal(err)
	}
	operation.ID, operation.Invocation.RequestID = "retry", "retry"
	retried, err := st.CreateControlOperation(t.Context(), operation)
	if err != nil || retried.ID != "original" || retried.Status != control.OperationProposed || retried.Confirmed {
		t.Fatalf("retry=%+v err=%v", retried, err)
	}
	if _, err := st.UpdateControlOperationStatus(t.Context(), "original", control.OperationCompleted, nil, nil); err != nil {
		t.Fatal(err)
	}
	completed, err := st.CreateControlOperation(t.Context(), operation)
	if err != nil || completed.Status != control.OperationCompleted {
		t.Fatalf("completed retry=%+v err=%v", completed, err)
	}
}

func TestLegacyFailedLaunchIsNotRequeuedWithoutRecoveryJournal(t *testing.T) {
	st := openReviewTestStore(t, filepath.Join(t.TempDir(), "legacy.sqlite"))
	defer st.Close()
	args, _ := json.Marshal(control.TodoCreateWorktreeAndStartEngineerInput{ProjectPath: "/repo", TodoText: "Legacy task", Prompt: "Task", Provider: control.ProviderCodex})
	op := control.Operation{ID: "legacy", ClientRequestID: "client", Source: "mcp", SessionKey: "caller", Invocation: control.Invocation{RequestID: "legacy", Capability: control.CapabilityTodoCreateWorktreeAndStartEngineer, Args: args}}
	if _, err := st.CreateControlOperation(t.Context(), op); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpdateControlOperationStatus(t.Context(), "legacy", control.OperationFailed, nil, injectedBusy{}); err != nil {
		t.Fatal(err)
	}
	replay, err := st.CreateControlOperation(t.Context(), op)
	if err != nil || replay.Status != control.OperationFailed {
		t.Fatalf("legacy replay=%+v err=%v", replay, err)
	}
}
