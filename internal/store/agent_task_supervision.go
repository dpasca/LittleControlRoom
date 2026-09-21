package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"time"

	"lcroom/internal/control"
	"lcroom/internal/model"
)

// supervisedCorrection reports whether an operation is a correction the task's
// own grant already covers. Every condition is read from persisted task state,
// never from the proposal, and any doubt returns the operation to ordinary
// operator confirmation rather than authorizing it.
func (s *Store) supervisedCorrection(ctx context.Context, op control.Operation) (model.AgentTask, bool, error) {
	correction, ok := control.DelegationCorrectionForOperation(op)
	if !ok {
		return model.AgentTask{}, false, nil
	}
	task, err := s.GetAgentTask(ctx, correction.TaskID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.AgentTask{}, false, nil
		}
		return model.AgentTask{}, false, err
	}
	workflow := task.Workflow
	if workflow.CorrectionsRemaining() < 1 || workflow.CallerStopped {
		return task, false, nil
	}
	switch model.NormalizeAgentTaskStatus(task.Status) {
	case model.AgentTaskStatusArchived, model.AgentTaskStatusCompleted:
		return task, false, nil
	}
	// The grant answers a review the caller already recorded for this exact
	// revision. Nothing else is a correction.
	review := workflow.Review
	if workflow.Phase != "changes_requested" || review == nil || review.Decision != "changes_requested" || review.Revision != workflow.RunID {
		return task, false, nil
	}
	actor := model.AgentTaskActor{ProjectPath: correction.ProjectPath, Provider: model.NormalizeSessionSource(model.SessionSource(correction.Provider)), SessionKey: correction.SessionKey}
	if !s.taskCallerAuthorized(ctx, task, actor) {
		return task, false, nil
	}
	// A worker that could reopen itself would be approving its own next round.
	if filepath.Clean(correction.ProjectPath) == filepath.Clean(task.WorkspacePath) {
		return task, false, nil
	}
	return task, true, nil
}

// SupervisionAllowsOperation is the read-only probe used when reporting a
// proposal back to the proposing agent. It consumes nothing.
func (s *Store) SupervisionAllowsOperation(ctx context.Context, op control.Operation) (bool, error) {
	_, allowed, err := s.supervisedCorrection(ctx, op)
	return allowed, err
}

// ConfirmDelegationSupervision rechecks the grant immediately before host
// execution and consumes one correction round in the same transaction, so a
// duplicate or replayed proposal cannot spend the grant twice. A revoked,
// exhausted or no-longer-matching grant returns the operation to ordinary
// confirmation instead.
func (s *Store) ConfirmDelegationSupervision(ctx context.Context, id string) (control.Operation, bool, error) {
	op, err := s.GetControlOperation(ctx, id)
	if err != nil {
		return control.Operation{}, false, err
	}
	if op.Status != control.OperationWaitingForConfirmation {
		return op, false, nil
	}
	task, allowed, err := s.supervisedCorrection(ctx, op)
	if err != nil || !allowed {
		return op, false, err
	}
	next := task.Workflow
	next.CorrectionsUsed++
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return control.Operation{}, false, err
	}
	defer tx.Rollback()
	// Compare-and-swap on the exact workflow this decision was made against and
	// on the still-pending operation, so a concurrent run, review or ordinary
	// confirmation cannot be overwritten by a stale grant read.
	result, err := tx.ExecContext(ctx, `UPDATE agent_tasks SET workflow_json = ?, updated_at = ? WHERE id = ? AND workflow_json = ?`,
		encodeTaskWorkflow(next), time.Now().Unix(), task.ID, encodeTaskWorkflow(task.Workflow))
	if err != nil {
		return control.Operation{}, false, err
	}
	if count, err := result.RowsAffected(); err != nil {
		return control.Operation{}, false, err
	} else if count != 1 {
		return op, false, nil
	}
	now := time.Now().Unix()
	result, err = tx.ExecContext(ctx, `UPDATE control_operations SET status = ?, confirmed = 1, confirmation_by = ?, started_at = ?, updated_at = ? WHERE id = ? AND status = ?`,
		string(control.OperationRunning), control.ConfirmationDelegationSupervision, now, now, id, string(control.OperationWaitingForConfirmation))
	if err != nil {
		return control.Operation{}, false, err
	}
	if count, err := result.RowsAffected(); err != nil {
		return control.Operation{}, false, err
	} else if count != 1 {
		return op, false, nil
	}
	if err = tx.Commit(); err != nil {
		return control.Operation{}, false, err
	}
	op, err = s.GetControlOperation(ctx, id)
	return op, true, err
}

// RevokeAgentTaskSupervision ends automatic corrections for one task without
// touching its result history, its edits or any running work.
func (s *Store) RevokeAgentTaskSupervision(ctx context.Context, taskID string) (model.AgentTask, error) {
	task, err := s.GetAgentTask(ctx, taskID)
	if err != nil {
		return task, err
	}
	if task.Workflow.SupervisionRevoked {
		return task, nil
	}
	next := task.Workflow
	next.SupervisionRevoked = true
	raw, err := s.db.ExecContext(ctx, `UPDATE agent_tasks SET workflow_json = ?, updated_at = ? WHERE id = ? AND workflow_json = ?`,
		encodeTaskWorkflow(next), time.Now().Unix(), task.ID, encodeTaskWorkflow(task.Workflow))
	if err != nil {
		return task, err
	}
	if count, err := raw.RowsAffected(); err != nil {
		return task, err
	} else if count != 1 {
		return task, errors.New("task changed; reload before revoking its correction grant")
	}
	return s.GetAgentTask(ctx, task.ID)
}
