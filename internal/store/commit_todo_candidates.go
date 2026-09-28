package store

import (
	"context"
	"time"

	"lcroom/internal/model"
)

// CompleteCommitTodoCandidate uses a compare-and-set write so edits or manual
// completion during inference cannot be overwritten by an old model decision.
// The check claim also fences workers superseded by a later retry.
func (s *Store) CompleteCommitTodoCandidate(ctx context.Context, check model.CommitTodoCheck, candidate model.CommitTodoCandidate) (bool, error) {
	now := time.Now().Unix()
	result, err := s.db.ExecContext(ctx, `
		UPDATE project_todos
		SET done = 1, completed_at = ?, updated_at = ?,
			work_provider = '', work_project_path = '', work_session_id = '',
			work_claimed_at = NULL, work_state = '', work_state_at = NULL
		WHERE id = ? AND project_path = ? AND text = ? AND updated_at = ? AND done = 0
			AND EXISTS (
				SELECT 1 FROM commit_todo_checks
				WHERE project_path = ? AND head_hash = ? AND status = ? AND attempt_count = ?
			)
	`, now, now, candidate.ID, candidate.ProjectPath, candidate.Text, candidate.UpdatedAt,
		check.ProjectPath, check.HeadHash, string(model.CommitTodoCheckRunning), check.AttemptCount)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count == 1, err
}
