package store

import (
	"context"
	"encoding/json"
	"fmt"

	"lcroom/internal/control"
	"lcroom/internal/model"
)

// AddTodoForLaunch commits the request identity and TODO together. A retry can
// never allocate a second TODO, even after restart or concurrent delivery.
func (s *Store) AddTodoForLaunch(ctx context.Context, input control.TodoCreateWorktreeAndStartEngineerInput) (model.TodoItem, error) {
	if input.RequestID == "" {
		return model.TodoItem{}, fmt.Errorf("launch request id is required")
	}
	input.TodoID = 0
	input.TodoLabel = ""
	input.WorktreePath = ""
	payload, err := json.Marshal(input)
	if err != nil {
		return model.TodoItem{}, err
	}
	return s.addTodoWithLaunchRequest(ctx, input.ProjectPath, input.TodoText, nil, input.RequestID, string(payload))
}

func (s *Store) TodoLaunchInput(ctx context.Context, todoID int64) (control.TodoCreateWorktreeAndStartEngineerInput, error) {
	var payload string
	err := s.db.QueryRowContext(ctx, `SELECT input_json FROM todo_launch_requests WHERE todo_id = ?`, todoID).Scan(&payload)
	if err != nil {
		return control.TodoCreateWorktreeAndStartEngineerInput{}, err
	}
	var input control.TodoCreateWorktreeAndStartEngineerInput
	err = json.Unmarshal([]byte(payload), &input)
	return input, err
}

type TodoWorktreePlan struct {
	TodoID       int64
	RootPath     string
	WorktreePath string
	Branch       string
	ParentBranch string
	Ready        bool
}

func (s *Store) TodoWorktreePlan(ctx context.Context, todoID int64) (TodoWorktreePlan, error) {
	var plan TodoWorktreePlan
	err := s.db.QueryRowContext(ctx, `SELECT todo_id, root_path, worktree_path, branch, parent_branch, ready FROM todo_worktree_plans WHERE todo_id = ?`, todoID).Scan(&plan.TodoID, &plan.RootPath, &plan.WorktreePath, &plan.Branch, &plan.ParentBranch, &plan.Ready)
	return plan, err
}

// SaveTodoWorktreePlan must succeed BEFORE git creates the directory. Even if
// every subsequent database write fails, the exact destination is recoverable.
func (s *Store) SaveTodoWorktreePlan(ctx context.Context, plan TodoWorktreePlan) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO todo_worktree_plans(todo_id, root_path, worktree_path, branch, parent_branch) VALUES (?, ?, ?, ?, ?)`, plan.TodoID, plan.RootPath, plan.WorktreePath, plan.Branch, plan.ParentBranch)
	return err
}

func (s *Store) MarkTodoWorktreeReady(ctx context.Context, todoID int64) error {
	result, err := s.db.ExecContext(ctx, `UPDATE todo_worktree_plans SET ready = 1 WHERE todo_id = ?`, todoID)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n != 1 {
		return fmt.Errorf("TODO worktree plan %d not found", todoID)
	}
	return err
}

func (s *Store) loadTodoLaunchState(ctx context.Context, item *model.TodoItem) error {
	return s.db.QueryRowContext(ctx, `SELECT
 COALESCE((SELECT request_id FROM todo_launch_requests WHERE todo_id = ?), ''),
 COALESCE((SELECT worktree_path FROM todo_worktree_plans WHERE todo_id = ?), ''),
 COALESCE((SELECT ready FROM todo_worktree_plans WHERE todo_id = ?), 0),
 COALESCE((SELECT engineer_claimed FROM todo_worktree_plans WHERE todo_id = ?), 0)`, item.ID, item.ID, item.ID, item.ID).Scan(&item.LaunchRequestID, &item.LaunchWorktreePath, &item.LaunchWorktreeReady, &item.LaunchEngineerClaimed)
}

// Persist a claim before crossing the provider boundary. A crash or failed
// receipt leaves the claim in place, so retry cannot silently duplicate work.
func (s *Store) ClaimTodoEngineerLaunch(ctx context.Context, todoID int64) error {
	result, err := s.db.ExecContext(ctx, `UPDATE todo_worktree_plans SET engineer_claimed = 1 WHERE todo_id = ? AND ready = 1 AND engineer_claimed = 0 AND NOT EXISTS (SELECT 1 FROM project_todos WHERE id = ? AND (work_session_id <> '' OR done = 1))`, todoID, todoID)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n != 1 {
		return fmt.Errorf("TODO #%d launch is already claimed or not ready; inspect its session before retrying", todoID)
	}
	return err
}

func (s *Store) ReleaseTodoEngineerLaunch(ctx context.Context, todoID int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE todo_worktree_plans SET engineer_claimed = 0 WHERE todo_id = ?`, todoID)
	return err
}

// Exact retries of a failed launch return to confirmation using the same
// operation ID. Running/completed operations and legacy failures without a
// recovery journal are never requeued: their side effects cannot be inferred.
func (s *Store) RetryFailedTodoLaunchOperation(ctx context.Context, id string, confirmed bool) (control.Operation, error) {
	status := control.OperationProposed
	if confirmed {
		status = control.OperationRunning
	}
	_, err := s.db.ExecContext(ctx, `UPDATE control_operations SET status = ?, confirmed = ?, confirmation_by = '', result_json = '', error = '', started_at = NULL, completed_at = NULL
 WHERE id = ? AND status = ? AND capability = ? AND EXISTS (SELECT 1 FROM todo_launch_requests WHERE request_id = control_operations.id)`, string(status), boolToInt(confirmed), id, string(control.OperationFailed), string(control.CapabilityTodoCreateWorktreeAndStartEngineer))
	if err != nil {
		return control.Operation{}, err
	}
	return s.GetControlOperation(ctx, id)
}

func (s *Store) DeleteTodoWorktreePlanForPath(ctx context.Context, path string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM todo_worktree_plans WHERE worktree_path = ?`, path)
	return err
}
