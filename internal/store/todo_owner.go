package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"lcroom/internal/control"
)

type todoOwnerQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

// TodoOwnerRevision includes all retained TODO data and launch provenance. The
// same snapshot is checked under the IMMEDIATE write transaction at repair time.
func (s *Store) TodoOwnerRevision(ctx context.Context, id int64) (string, error) {
	return todoOwnerRevision(ctx, s.db, id)
}

func todoOwnerRevision(ctx context.Context, q todoOwnerQueryer, id int64) (string, error) {
	hash := sha256.New()
	for _, query := range []string{
		`SELECT * FROM project_todos WHERE id = ?`,
		`SELECT * FROM todo_attachments WHERE todo_id = ? ORDER BY id`,
		`SELECT * FROM todo_launch_requests WHERE todo_id = ?`,
		`SELECT * FROM todo_worktree_plans WHERE todo_id = ?`,
	} {
		rows, err := q.QueryContext(ctx, query, id)
		if err != nil {
			return "", err
		}
		columns, err := rows.Columns()
		if err != nil {
			rows.Close()
			return "", err
		}
		for rows.Next() {
			values := make([]any, len(columns))
			pointers := make([]any, len(columns))
			for i := range values {
				pointers[i] = &values[i]
			}
			if err := rows.Scan(pointers...); err != nil {
				rows.Close()
				return "", err
			}
			if err := json.NewEncoder(hash).Encode(values); err != nil {
				rows.Close()
				return "", err
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return "", err
		}
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

func (s *Store) TodoOwnerRepairReplay(ctx context.Context, input control.TodoRepairOwnerInput) (bool, error) {
	payload, _ := json.Marshal(input)
	var saved string
	err := s.db.QueryRowContext(ctx, `SELECT input_json FROM todo_owner_repairs WHERE request_id = ?`, input.RequestID).Scan(&saved)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var prior control.TodoRepairOwnerInput
	if err := json.Unmarshal([]byte(saved), &prior); err != nil {
		return false, err
	}
	if input.ExpectedRevision == "" {
		input.ExpectedRevision = prior.ExpectedRevision
		payload, _ = json.Marshal(input)
	}
	if saved != string(payload) {
		return false, fmt.Errorf("repair request id is already bound to different input")
	}
	return true, nil
}

// RepairTodoOwner is a storage primitive for the trusted service boundary.
// It changes only owner and ordering, retaining timestamps and every work link.
// The audit and change commit together, including exact-retry no-op receipts.
func (s *Store) RepairTodoOwner(ctx context.Context, input control.TodoRepairOwnerInput) (bool, error) {
	if input.RequestID == "" || input.ExpectedRevision == "" {
		return false, fmt.Errorf("repair request id and reviewed revision are required")
	}
	payload, err := json.Marshal(input)
	if err != nil {
		return false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var saved string
	err = tx.QueryRowContext(ctx, `SELECT input_json FROM todo_owner_repairs WHERE request_id = ?`, input.RequestID).Scan(&saved)
	replay := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if replay && saved != string(payload) {
		return false, fmt.Errorf("repair request id is already bound to different input")
	}
	var owner string
	if err := tx.QueryRowContext(ctx, `SELECT project_path FROM project_todos WHERE id = ?`, input.TodoID).Scan(&owner); err != nil {
		return false, err
	}
	if replay {
		if owner != input.RepositoryPath {
			return false, fmt.Errorf("TODO #%d owner changed since repair; refusing stale retry", input.TodoID)
		}
	} else {
		if owner != input.ExpectedOwnerPath {
			return false, fmt.Errorf("TODO #%d expected owner %s, found %s; refresh before retrying", input.TodoID, input.ExpectedOwnerPath, owner)
		}
		revision, err := todoOwnerRevision(ctx, tx, input.TodoID)
		if err != nil {
			return false, err
		}
		if revision != input.ExpectedRevision {
			return false, fmt.Errorf("TODO #%d changed since review; prepare a new repair", input.TodoID)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE project_todos SET project_path = ?, position =
			(SELECT COALESCE(MAX(position), -1) + 1 FROM project_todos WHERE project_path = ? AND done = (SELECT done FROM project_todos WHERE id = ?))
			WHERE id = ?`, input.RepositoryPath, input.RepositoryPath, input.TodoID, input.TodoID); err != nil {
			return false, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO todo_owner_repairs(request_id,todo_id,input_json) VALUES(?,?,?)`, input.RequestID, input.TodoID, string(payload)); err != nil {
			return false, err
		}
	}
	audit, _ := json.Marshal(map[string]any{"request": input, "changed": !replay, "idempotent_replay": replay})
	if _, err := tx.ExecContext(ctx, `INSERT INTO events(ts,project_path,event_type,payload) VALUES(?,?,?,?)`, time.Now().Unix(), input.RepositoryPath, "todo_owner_repair", string(audit)); err != nil {
		return false, err
	}
	return !replay, tx.Commit()
}

func (s *Store) TodoOwnerRepairInput(ctx context.Context, requestID string) (control.TodoRepairOwnerInput, error) {
	var payload string
	err := s.db.QueryRowContext(ctx, `SELECT input_json FROM todo_owner_repairs WHERE request_id = ?`, requestID).Scan(&payload)
	var input control.TodoRepairOwnerInput
	if err == nil {
		err = json.Unmarshal([]byte(payload), &input)
	}
	return input, err
}
