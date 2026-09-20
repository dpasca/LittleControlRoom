package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"lcroom/internal/model"
)

type AgentTaskRepositoryLease struct {
	Root       string
	TaskID     string
	SessionKey string
}

func encodeAgentTaskRepository(value model.AgentTaskRepository) string {
	data, _ := json.Marshal(value)
	return string(data)
}

func (s *Store) AgentTaskRepositoryLeases(ctx context.Context) ([]AgentTaskRepositoryLease, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT root, task_id, session_key FROM agent_task_repository_leases`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var leases []AgentTaskRepositoryLease
	for rows.Next() {
		var lease AgentTaskRepositoryLease
		if err := rows.Scan(&lease.Root, &lease.TaskID, &lease.SessionKey); err != nil {
			return nil, err
		}
		leases = append(leases, lease)
	}
	return leases, rows.Err()
}

// AcquireAgentTaskRepository atomically persists ownership and its baseline.
// The unique checkout key also protects against a second host using this DB.
func (s *Store) AcquireAgentTaskRepository(ctx context.Context, taskID string, repository model.AgentTaskRepository) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO agent_task_repository_leases(root, task_id, session_key) VALUES (?, ?, ?)`, repository.Root, taskID, repository.SessionKey); err != nil {
		return fmt.Errorf("repository already has a write owner: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE agent_tasks SET repository_json = ? WHERE id = ?`, encodeAgentTaskRepository(repository), taskID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ReleaseAgentTaskRepository(ctx context.Context, taskID string, repository model.AgentTaskRepository) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM agent_task_repository_leases WHERE task_id = ?`, taskID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE agent_tasks SET repository_json = ? WHERE id = ?`, encodeAgentTaskRepository(repository), taskID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) AgentTaskForWorkspace(ctx context.Context, path string) (model.AgentTask, bool, error) {
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT id FROM agent_tasks WHERE workspace_path = ? LIMIT 1`, path).Scan(&id)
	if err == sql.ErrNoRows {
		return model.AgentTask{}, false, nil
	}
	if err != nil {
		return model.AgentTask{}, false, err
	}
	task, err := s.GetAgentTask(ctx, id)
	return task, true, err
}
