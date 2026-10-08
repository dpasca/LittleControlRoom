package store

import (
	"context"
	"time"

	"lcroom/internal/control"
)

func (s *Store) ListPendingControlContinuations(ctx context.Context) ([]control.ControlContinuation, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT operation_id,requested_at,state,host_id,session_id,input_revision,reason
		FROM control_continuations WHERE state IN ('pending','armed','dispatching') ORDER BY requested_at LIMIT 50`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []control.ControlContinuation
	for rows.Next() {
		var c control.ControlContinuation
		var requested int64
		if err := rows.Scan(&c.OperationID, &requested, &c.State, &c.HostID, &c.SessionID, &c.InputRevision, &c.Reason); err != nil {
			return nil, err
		}
		c.RequestedAt = time.Unix(0, requested)
		out = append(out, c)
	}
	return out, rows.Err()
}

// TransitionControlContinuation is a compare-and-swap: a callback has one
// durable dispatch attempt. An ambiguous attempt after a crash is never replayed.
func (s *Store) TransitionControlContinuation(ctx context.Context, c control.ControlContinuation, next, reason string) (bool, error) {
	result, err := s.db.ExecContext(ctx, `UPDATE control_continuations SET state=?,host_id=?,session_id=?,input_revision=?,reason=? WHERE operation_id=? AND state=?`,
		next, c.HostID, c.SessionID, c.InputRevision, reason, c.OperationID, c.State)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}
