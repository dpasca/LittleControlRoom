package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"lcroom/internal/control"
)

type collaborationQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func collaborationAllowed(ctx context.Context, q collaborationQuerier, op control.Operation) (bool, error) {
	pair, ok := control.CollaborationForOperation(op)
	if !ok {
		return false, nil
	}
	var allowed bool
	err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM project_collaborations WHERE project_a = ? AND project_b = ?)`, pair.ProjectA, pair.ProjectB).Scan(&allowed)
	return allowed, err
}

func (s *Store) CollaborationAllowsOperation(ctx context.Context, op control.Operation) (bool, error) {
	return collaborationAllowed(ctx, s.db, op)
}

// ApproveProjectCollaboration is called only by the operator's host UI. Saving
// trust and approving this first message are atomic and cannot revive a terminal
// operation.
func (s *Store) ApproveProjectCollaboration(ctx context.Context, id string) (control.Operation, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return control.Operation{}, err
	}
	defer tx.Rollback()
	op, err := scanControlOperation(tx.QueryRowContext(ctx, controlOperationSelect+` WHERE id = ?`, id))
	if err != nil {
		return control.Operation{}, err
	}
	pair, ok := control.CollaborationForOperation(op)
	if !ok || op.Status != control.OperationWaitingForConfirmation {
		return control.Operation{}, errors.New("this request is no longer eligible for project collaboration")
	}
	now := time.Now().Unix()
	if _, err = tx.ExecContext(ctx, `INSERT INTO project_collaborations(project_a, project_b, approved_at) VALUES (?, ?, ?) ON CONFLICT(project_a, project_b) DO NOTHING`, pair.ProjectA, pair.ProjectB, now); err != nil {
		return control.Operation{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE control_operations SET status = ?, confirmed = 1, confirmation_by = ?, started_at = ?, updated_at = ? WHERE id = ?`, string(control.OperationRunning), control.ConfirmationProjectCollaboration, now, now, id); err != nil {
		return control.Operation{}, err
	}
	if err = tx.Commit(); err != nil {
		return control.Operation{}, err
	}
	return s.GetControlOperation(ctx, id)
}

// ConfirmProjectCollaboration rechecks current trust immediately before host
// execution. A revoked grant returns the message to ordinary confirmation.
func (s *Store) ConfirmProjectCollaboration(ctx context.Context, id string) (control.Operation, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return control.Operation{}, false, err
	}
	defer tx.Rollback()
	op, err := scanControlOperation(tx.QueryRowContext(ctx, controlOperationSelect+` WHERE id = ?`, id))
	if err != nil {
		return control.Operation{}, false, err
	}
	if op.Status != control.OperationWaitingForConfirmation {
		return op, false, nil
	}
	allowed, err := collaborationAllowed(ctx, tx, op)
	if err != nil {
		return control.Operation{}, false, err
	}
	if !allowed {
		return op, false, nil
	}
	now := time.Now().Unix()
	_, err = tx.ExecContext(ctx, `UPDATE control_operations SET status = ?, confirmed = 1, confirmation_by = ?, started_at = ?, updated_at = ? WHERE id = ?`, string(control.OperationRunning), control.ConfirmationProjectCollaboration, now, now, id)
	if err != nil {
		return control.Operation{}, false, err
	}
	if err = tx.Commit(); err != nil {
		return control.Operation{}, false, err
	}
	op, err = s.GetControlOperation(ctx, id)
	return op, true, err
}

func (s *Store) ListProjectCollaborations(ctx context.Context, project string) ([]control.ProjectCollaboration, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT project_a, project_b FROM project_collaborations WHERE project_a = ? OR project_b = ? ORDER BY project_a, project_b`, project, project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var pairs []control.ProjectCollaboration
	for rows.Next() {
		var pair control.ProjectCollaboration
		if err := rows.Scan(&pair.ProjectA, &pair.ProjectB); err != nil {
			return nil, err
		}
		pairs = append(pairs, pair)
	}
	return pairs, rows.Err()
}

func (s *Store) RevokeProjectCollaboration(ctx context.Context, pair control.ProjectCollaboration) error {
	pair, ok := control.CollaborationPair(pair.ProjectA, pair.ProjectB)
	if !ok {
		return errors.New("invalid project pair")
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM project_collaborations WHERE project_a = ? AND project_b = ?`, pair.ProjectA, pair.ProjectB)
	return err
}
