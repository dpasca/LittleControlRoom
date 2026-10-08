package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"lcroom/internal/control"
)

func scopedPermissionID(ctx context.Context, q collaborationQuerier, op control.Operation) (int64, error) {
	p, ok := control.PermissionForOperation(op)
	if !ok {
		return 0, nil
	}
	var id int64
	err := q.QueryRowContext(ctx, `SELECT id FROM control_permissions
		WHERE origin = ? AND provider = ? AND capability = ? AND target = ? AND constraints_json = ?
		AND (session_key = '' OR session_key = ?) AND (use_limit = 0 OR used < use_limit)
		ORDER BY CASE WHEN session_key = '' THEN 1 ELSE 0 END, id LIMIT 1`,
		p.Origin, p.Provider, p.Capability, p.Target, p.Constraints, op.SessionKey).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return id, err
}

func (s *Store) PermissionAllowsOperation(ctx context.Context, op control.Operation) (bool, error) {
	id, err := scopedPermissionID(ctx, s.db, op)
	return id != 0, err
}

// ApproveControlPermission is host-only; grant creation and the first use are
// atomic. A session grant is bound to the actual control channel, not model input.
func (s *Store) ApproveControlPermission(ctx context.Context, operationID string, sessionOnly bool) (control.Operation, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return control.Operation{}, err
	}
	defer tx.Rollback()
	op, err := scanControlOperation(tx.QueryRowContext(ctx, controlOperationSelect+` WHERE id = ?`, operationID))
	if err != nil {
		return control.Operation{}, err
	}
	p, ok := control.PermissionForOperation(op)
	if !ok || op.Status != control.OperationWaitingForConfirmation {
		return control.Operation{}, errors.New("this request is no longer eligible for a saved permission")
	}
	if sessionOnly {
		p.SessionKey = op.SessionKey
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO control_permissions
		(origin,provider,session_key,capability,target,constraints_json,use_limit,used)
		VALUES(?,?,?,?,?,?,?,1)
		ON CONFLICT(origin,provider,session_key,capability,target,constraints_json)
		DO UPDATE SET use_limit=excluded.use_limit, used=1`, p.Origin, p.Provider, p.SessionKey, p.Capability, p.Target, p.Constraints, p.Limit)
	if err != nil {
		return control.Operation{}, err
	}
	if err = confirmScopedOperation(ctx, tx, op.ID); err != nil {
		return control.Operation{}, err
	}
	if err = tx.Commit(); err != nil {
		return control.Operation{}, err
	}
	return s.GetControlOperation(ctx, op.ID)
}

func confirmScopedOperation(ctx context.Context, tx *sql.Tx, id string) error {
	now := time.Now().Unix()
	_, err := tx.ExecContext(ctx, `UPDATE control_operations SET status='running', confirmed=1,
		confirmation_by=?, started_at=?, updated_at=? WHERE id=?`, control.ConfirmationScopedPermission, now, now, id)
	return err
}

// Recheck and consume immediately before execution. Replays cannot consume a
// second use, and a revocation between queue claim and execution takes effect.
func (s *Store) ConfirmControlPermission(ctx context.Context, operationID string) (control.Operation, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return control.Operation{}, false, err
	}
	defer tx.Rollback()
	op, err := scanControlOperation(tx.QueryRowContext(ctx, controlOperationSelect+` WHERE id=?`, operationID))
	if err != nil || op.Status != control.OperationWaitingForConfirmation {
		return op, false, err
	}
	id, err := scopedPermissionID(ctx, tx, op)
	if err != nil || id == 0 {
		return op, false, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE control_permissions SET used=used+1 WHERE id=?`, id); err != nil {
		return op, false, err
	}
	if err = confirmScopedOperation(ctx, tx, op.ID); err != nil {
		return op, false, err
	}
	if err = tx.Commit(); err != nil {
		return op, false, err
	}
	op, err = s.GetControlOperation(ctx, op.ID)
	return op, true, err
}

func (s *Store) ListControlPermissions(ctx context.Context, origin string) ([]control.ControlPermission, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,origin,provider,session_key,capability,target,constraints_json,use_limit,used FROM control_permissions WHERE origin=? ORDER BY id`, origin)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []control.ControlPermission
	for rows.Next() {
		var p control.ControlPermission
		if err := rows.Scan(&p.ID, &p.Origin, &p.Provider, &p.SessionKey, &p.Capability, &p.Target, &p.Constraints, &p.Limit, &p.Used); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) RevokeControlPermission(ctx context.Context, origin string, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM control_permissions WHERE origin=? AND id=?`, origin, id)
	return err
}
