package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"lcroom/internal/model"
)

// BindEngineerSession records host-observed identity. A control channel can never
// be rebound to a replacement conversation, even if it uses the same workspace.
func (s *Store) BindEngineerSession(ctx context.Context, projectPath string, provider model.SessionSource, key, sessionID string) error {
	projectPath = strings.TrimSpace(projectPath)
	key, sessionID = strings.TrimSpace(key), strings.TrimSpace(sessionID)
	provider = model.NormalizeSessionSource(provider)
	if projectPath == "" || key == "" || sessionID == "" || provider == model.SessionSourceUnknown {
		return errors.New("engineer session binding requires project, provider, control key and provider session ID")
	}
	projectPath = filepath.Clean(projectPath)
	if _, err := s.db.ExecContext(ctx, `INSERT INTO engineer_session_bindings(project_path, provider, control_session_key, provider_session_id)
  VALUES (?, ?, ?, ?) ON CONFLICT(project_path, provider, control_session_key) DO NOTHING`, projectPath, string(provider), key, sessionID); err != nil {
		return err
	}
	bound, err := s.ResolveEngineerSession(ctx, projectPath, provider, key)
	if err != nil {
		return err
	}
	if bound != sessionID {
		return fmt.Errorf("control session %q is already bound to a different provider conversation", key)
	}
	return nil
}

// ResolveEngineerSession deliberately has no latest-session or key-as-ID fallback.
// An empty result means the host has not observed this channel's identity yet.
func (s *Store) ResolveEngineerSession(ctx context.Context, projectPath string, provider model.SessionSource, key string) (string, error) {
	if strings.TrimSpace(projectPath) == "" || strings.TrimSpace(key) == "" {
		return "", nil
	}
	var sessionID string
	err := s.db.QueryRowContext(ctx, `SELECT provider_session_id FROM engineer_session_bindings
  WHERE project_path = ? AND provider = ? AND control_session_key = ?`, filepath.Clean(strings.TrimSpace(projectPath)), string(model.NormalizeSessionSource(provider)), strings.TrimSpace(key)).Scan(&sessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return sessionID, err
}

func (s *Store) ResolveAgentTaskCaller(ctx context.Context, task model.AgentTask) (model.AgentTask, error) {
	if task.OriginSessionKey == "" || task.OriginSessionID != "" {
		return task, nil
	}
	path := firstNonEmptyString(task.OriginWorktreePath, task.OriginProjectPath)
	sessionID, err := s.ResolveEngineerSession(ctx, path, task.OriginProvider, task.OriginSessionKey)
	if err != nil || sessionID == "" {
		return task, err
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE agent_tasks SET origin_session_id = ?, result_delivery_error = ''
  WHERE id = ? AND origin_session_key = ? AND origin_session_id = ''`, sessionID, task.ID, task.OriginSessionKey); err != nil {
		return task, err
	}
	return s.GetAgentTask(ctx, task.ID)
}

// Older versions copied the MCP key into origin_session_id. Preserve it as a
// control key; only a matching recorded provider artifact proves it is also a
// resumable ID. Never infer a caller from the newest session in the workspace.
func (s *Store) migrateAgentTaskCallerBindings(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `UPDATE agent_tasks SET origin_session_key = (
  SELECT session_key FROM control_operations WHERE id = origin_operation_id
 ) WHERE origin_session_key = '' AND EXISTS (
  SELECT 1 FROM control_operations WHERE id = origin_operation_id AND session_key != ''
   AND (agent_tasks.origin_session_id = session_key OR agent_tasks.origin_session_id = '')
 )`)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO engineer_session_bindings(project_path, provider, control_session_key, provider_session_id)
  SELECT CASE WHEN at.origin_worktree_path != '' THEN at.origin_worktree_path ELSE at.origin_project_path END,
   at.origin_provider, at.origin_session_key, ps.raw_session_id
  FROM agent_tasks at JOIN project_sessions ps ON
   ps.project_path = CASE WHEN at.origin_worktree_path != '' THEN at.origin_worktree_path ELSE at.origin_project_path END
   AND ps.source = at.origin_provider AND ps.raw_session_id = at.origin_session_key
  WHERE at.origin_session_key != ''
  ON CONFLICT(project_path, provider, control_session_key) DO NOTHING`)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE agent_tasks SET origin_session_id = COALESCE((
  SELECT provider_session_id FROM engineer_session_bindings b
  WHERE b.project_path = CASE WHEN origin_worktree_path != '' THEN origin_worktree_path ELSE origin_project_path END
   AND b.provider = origin_provider AND b.control_session_key = origin_session_key
 ), '') WHERE origin_session_key != '' AND origin_session_id = origin_session_key`)
	if err != nil {
		return err
	}
	// Preserve historical attempts, but never dispatch an old queued callback to
	// an unverified key. Already delivered/consumed results are untouched.
	_, err = s.db.ExecContext(ctx, `UPDATE engineer_messages SET state = 'failed',
  last_error = 'Legacy caller identity could not be verified; explicit redelivery is required.'
  WHERE state IN ('queued', 'delivering') AND EXISTS (
   SELECT 1 FROM agent_tasks at WHERE at.id = engineer_messages.agent_task_id
    AND at.result_message_id = engineer_messages.id AND at.origin_session_key != ''
    AND at.result_consumed_at IS NULL AND at.result_delivered_at IS NULL
    AND (at.origin_session_id = '' OR at.origin_session_id != engineer_messages.target_session_id)
  )`)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE agent_tasks SET result_delivery_error = (
  SELECT last_error FROM engineer_messages WHERE id = result_message_id
 ) WHERE origin_session_key != '' AND result_consumed_at IS NULL AND result_delivered_at IS NULL
  AND EXISTS (SELECT 1 FROM engineer_messages WHERE id = result_message_id AND state = 'failed')`)
	return err
}

// AgentTaskIDsAwaitingCaller lets a late identity announcement wake its waiting
// results without depending on whether those tasks are in the visible page.
func (s *Store) AgentTaskIDsAwaitingCaller(ctx context.Context, projectPath string, provider model.SessionSource, key string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM agent_tasks
  WHERE origin_provider = ? AND origin_session_key = ?
   AND CASE WHEN origin_worktree_path != '' THEN origin_worktree_path ELSE origin_project_path END = ?
   AND result_ready_at IS NOT NULL AND result_consumed_at IS NULL AND result_message_id = '' AND status != 'archived'`,
		string(model.NormalizeSessionSource(provider)), strings.TrimSpace(key), filepath.Clean(strings.TrimSpace(projectPath)))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// RecordAgentTaskCallerPending must not overwrite a binding or delivery that
// raced the caller lookup in another worker.
func (s *Store) RecordAgentTaskCallerPending(ctx context.Context, task model.AgentTask, problem string) (model.AgentTask, error) {
	_, err := s.db.ExecContext(ctx, `UPDATE agent_tasks SET result_delivery_error = ?
  WHERE id = ? AND origin_session_id = '' AND origin_session_key = ?
   AND result_message_id = '' AND result_consumed_at IS NULL AND result_ready_at = ?
   AND result_delivery_error != ?`, problem, task.ID, task.OriginSessionKey, task.ResultReadyAt.Unix(), problem)
	if err != nil {
		return task, err
	}
	return s.GetAgentTask(ctx, task.ID)
}
