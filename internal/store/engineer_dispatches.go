package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"lcroom/internal/control"
	"lcroom/internal/model"
)

const engineerDispatchSelect = `
	SELECT id, origin_operation_id, todo_id, todo_text, worker_project_path, worker_provider, worker_session_id,
		caller_project_path, caller_provider, caller_session_key, caller_session_id,
		reply_state, reply_seq, reply_prompt, reply_message_id, reply_error, created_at, updated_at
	FROM engineer_dispatches
`

// CreateEngineerDispatch records a return address for an engineer launched by
// another embedded session. It is idempotent per originating control
// operation, so a replayed launch receipt cannot arm a second address.
func (s *Store) CreateEngineerDispatch(ctx context.Context, dispatch model.EngineerDispatch) (model.EngineerDispatch, error) {
	if s == nil || s.db == nil {
		return model.EngineerDispatch{}, errors.New("store unavailable")
	}
	dispatch.OriginOperationID = strings.TrimSpace(dispatch.OriginOperationID)
	dispatch.TodoText = strings.TrimSpace(dispatch.TodoText)
	dispatch.WorkerProjectPath = cleanDispatchPath(dispatch.WorkerProjectPath)
	dispatch.WorkerProvider = model.NormalizeSessionSource(dispatch.WorkerProvider)
	dispatch.WorkerSessionID = strings.TrimSpace(dispatch.WorkerSessionID)
	dispatch.CallerProjectPath = cleanDispatchPath(dispatch.CallerProjectPath)
	dispatch.CallerProvider = model.NormalizeSessionSource(dispatch.CallerProvider)
	dispatch.CallerSessionKey = strings.TrimSpace(dispatch.CallerSessionKey)
	dispatch.CallerSessionID = strings.TrimSpace(dispatch.CallerSessionID)
	if dispatch.OriginOperationID == "" || dispatch.WorkerProjectPath == "" || dispatch.CallerProjectPath == "" || dispatch.CallerSessionKey == "" ||
		dispatch.WorkerProvider == model.SessionSourceUnknown || dispatch.CallerProvider == model.SessionSourceUnknown {
		return model.EngineerDispatch{}, errors.New("engineer dispatch requires an operation, worker project and provider, and caller project, provider and control key")
	}
	now := time.Now().Unix()
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO engineer_dispatches(
			origin_operation_id, todo_id, todo_text, worker_project_path, worker_provider, worker_session_id,
			caller_project_path, caller_provider, caller_session_key, caller_session_id,
			reply_state, reply_seq, created_at, updated_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?)
		ON CONFLICT(origin_operation_id) DO NOTHING
	`, dispatch.OriginOperationID, dispatch.TodoID, dispatch.TodoText, dispatch.WorkerProjectPath, string(dispatch.WorkerProvider),
		dispatch.WorkerSessionID, dispatch.CallerProjectPath, string(dispatch.CallerProvider), dispatch.CallerSessionKey,
		dispatch.CallerSessionID, string(model.EngineerDispatchReplyAwaiting), now, now); err != nil {
		return model.EngineerDispatch{}, fmt.Errorf("create engineer dispatch: %w", err)
	}
	return scanEngineerDispatch(s.db.QueryRowContext(ctx, engineerDispatchSelect+` WHERE origin_operation_id = ?`, dispatch.OriginOperationID))
}

func (s *Store) GetEngineerDispatch(ctx context.Context, id int64) (model.EngineerDispatch, error) {
	if s == nil || s.db == nil {
		return model.EngineerDispatch{}, errors.New("store unavailable")
	}
	return scanEngineerDispatch(s.db.QueryRowContext(ctx, engineerDispatchSelect+` WHERE id = ?`, id))
}

// ListAwaitingEngineerDispatches snapshots the requests waiting for this worker's
// turn to end. A different session in the same worktree does not match once
// the worker's identity is known.
func (s *Store) ListAwaitingEngineerDispatches(ctx context.Context, workerProjectPath string, provider model.SessionSource, sessionID string) ([]model.EngineerDispatch, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("store unavailable")
	}
	workerProjectPath = cleanDispatchPath(workerProjectPath)
	sessionID = strings.TrimSpace(sessionID)
	if workerProjectPath == "" {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, engineerDispatchSelect+`
		WHERE worker_project_path = ? AND worker_provider = ? AND reply_state = ?
		  AND (worker_session_id = '' OR worker_session_id = ?)
		ORDER BY id
	`, workerProjectPath, string(model.NormalizeSessionSource(provider)), string(model.EngineerDispatchReplyAwaiting), sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var dispatches []model.EngineerDispatch
	for rows.Next() {
		dispatch, err := scanEngineerDispatch(rows)
		if err != nil {
			return nil, err
		}
		dispatches = append(dispatches, dispatch)
	}
	return dispatches, rows.Err()
}

// MarkEngineerDispatchReplyReady stores the reply for the exact request
// sequence. It reports false when a newer request or another completion
// already claimed the reply.
func (s *Store) MarkEngineerDispatchReplyReady(ctx context.Context, id, seq int64, workerSessionID, prompt string) (model.EngineerDispatch, bool, error) {
	if s == nil || s.db == nil {
		return model.EngineerDispatch{}, false, errors.New("store unavailable")
	}
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return model.EngineerDispatch{}, false, errors.New("engineer dispatch reply prompt is required")
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE engineer_dispatches
		SET reply_state = ?, reply_prompt = ?, reply_message_id = '', reply_error = '',
			worker_session_id = CASE WHEN worker_session_id = '' THEN ? ELSE worker_session_id END,
			updated_at = ?
		WHERE id = ? AND reply_seq = ? AND reply_state = ?
	`, string(model.EngineerDispatchReplyReady), prompt, strings.TrimSpace(workerSessionID), time.Now().Unix(), id, seq, string(model.EngineerDispatchReplyAwaiting))
	if err != nil {
		return model.EngineerDispatch{}, false, fmt.Errorf("mark engineer dispatch reply ready: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return model.EngineerDispatch{}, false, err
	}
	dispatch, err := s.GetEngineerDispatch(ctx, id)
	return dispatch, changed == 1, err
}

// ResolveEngineerDispatchCaller fills the caller's provider session from the
// exact host binding. It never falls back to the newest session in a worktree.
func (s *Store) ResolveEngineerDispatchCaller(ctx context.Context, dispatch model.EngineerDispatch) (model.EngineerDispatch, error) {
	if dispatch.CallerSessionID != "" {
		return dispatch, nil
	}
	sessionID, err := s.ResolveEngineerSession(ctx, dispatch.CallerProjectPath, dispatch.CallerProvider, dispatch.CallerSessionKey)
	if err != nil || sessionID == "" {
		return dispatch, err
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE engineer_dispatches SET caller_session_id = ?, updated_at = ?
		WHERE id = ? AND caller_session_id = ''`, sessionID, time.Now().Unix(), dispatch.ID); err != nil {
		return dispatch, err
	}
	return s.GetEngineerDispatch(ctx, dispatch.ID)
}

func (s *Store) RecordEngineerDispatchReplySent(ctx context.Context, id, seq int64, messageID string) (model.EngineerDispatch, error) {
	if s == nil || s.db == nil {
		return model.EngineerDispatch{}, errors.New("store unavailable")
	}
	if _, err := s.db.ExecContext(ctx, `
		UPDATE engineer_dispatches
		SET reply_state = ?, reply_message_id = ?, reply_error = '', updated_at = ?
		WHERE id = ? AND reply_seq = ? AND reply_state = ?
	`, string(model.EngineerDispatchReplySent), strings.TrimSpace(messageID), time.Now().Unix(), id, seq, string(model.EngineerDispatchReplyReady)); err != nil {
		return model.EngineerDispatch{}, fmt.Errorf("record engineer dispatch reply: %w", err)
	}
	return s.GetEngineerDispatch(ctx, id)
}

func (s *Store) RecordEngineerDispatchReplyError(ctx context.Context, id, seq int64, problem string) (model.EngineerDispatch, error) {
	if s == nil || s.db == nil {
		return model.EngineerDispatch{}, errors.New("store unavailable")
	}
	if _, err := s.db.ExecContext(ctx, `
		UPDATE engineer_dispatches SET reply_error = ?, updated_at = ?
		WHERE id = ? AND reply_seq = ? AND reply_state = ?
	`, strings.TrimSpace(problem), time.Now().Unix(), id, seq, string(model.EngineerDispatchReplyReady)); err != nil {
		return model.EngineerDispatch{}, fmt.Errorf("record engineer dispatch reply failure: %w", err)
	}
	return s.GetEngineerDispatch(ctx, id)
}

// EngineerDispatchIDsAwaitingCaller lists stored replies for one caller
// channel that have not reached the mailbox yet.
func (s *Store) EngineerDispatchIDsAwaitingCaller(ctx context.Context, callerProjectPath string, provider model.SessionSource, key string) ([]int64, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("store unavailable")
	}
	callerProjectPath = cleanDispatchPath(callerProjectPath)
	key = strings.TrimSpace(key)
	if callerProjectPath == "" || key == "" {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM engineer_dispatches
		WHERE caller_project_path = ? AND caller_provider = ? AND caller_session_key = ? AND reply_state = ?
		ORDER BY id`, callerProjectPath, string(model.NormalizeSessionSource(provider)), key, string(model.EngineerDispatchReplyReady))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// armEngineerDispatchForDelivery records the return address of a delivered
// embedded-session request, even when the caller did not launch the worker.
// Receipt retries are idempotent in the surrounding message transaction.
// Operator messages and LCR callbacks do not create return addresses.
func armEngineerDispatchForDelivery(ctx context.Context, tx *sql.Tx, message control.EngineerMessage, now time.Time) error {
	if strings.TrimSpace(message.AgentTaskID) != "" || !control.IsExternalOperationID(message.OperationID) {
		return nil
	}
	var provider, key, projectPath, capability string
	err := tx.QueryRowContext(ctx, `SELECT provider, session_key, project_path, capability FROM control_operations WHERE id = ?`, message.OperationID).
		Scan(&provider, &key, &projectPath, &capability)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load engineer message caller: %w", err)
	}
	key = strings.TrimSpace(key)
	projectPath = cleanDispatchPath(projectPath)
	callerProvider := sessionSourceFromControlProvider(control.NormalizeProvider(provider))
	if key == "" || projectPath == "" || callerProvider == model.SessionSourceUnknown || control.CapabilityName(capability) != control.CapabilityEngineerSendPrompt {
		return nil
	}
	workerPath := cleanDispatchPath(message.ProjectPath)
	workerProvider := sessionSourceFromControlProvider(message.Provider)
	targetSessionID := strings.TrimSpace(message.TargetSessionID)
	var callerSessionID string
	err = tx.QueryRowContext(ctx, `SELECT provider_session_id FROM engineer_session_bindings
		WHERE project_path = ? AND provider = ? AND control_session_key = ?`, projectPath, string(callerProvider), key).Scan(&callerSessionID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("resolve engineer message caller: %w", err)
	}
	if projectPath == workerPath && callerProvider == workerProvider && callerSessionID != "" && callerSessionID == targetSessionID {
		return nil
	}
	// Reuse only this caller's exact worker exchange. A stored report waiting
	// for caller identity must survive a later request, as must other sessions'
	// return addresses in the same worktree.
	result, err := tx.ExecContext(ctx, `
		UPDATE engineer_dispatches
		SET reply_state = ?, reply_seq = reply_seq + 1, reply_prompt = '', reply_message_id = '', reply_error = '',
			worker_session_id = CASE WHEN ? <> '' THEN ? ELSE worker_session_id END,
			updated_at = ?
		WHERE id = (
			SELECT id FROM engineer_dispatches
			WHERE worker_project_path = ? AND worker_provider = ?
			  AND (worker_session_id = '' OR worker_session_id = ?)
			  AND caller_project_path = ? AND caller_provider = ? AND caller_session_key = ?
			  AND reply_state IN (?, ?)
			ORDER BY updated_at DESC, id DESC LIMIT 1
		)
	`, string(model.EngineerDispatchReplyAwaiting), targetSessionID, targetSessionID, now.Unix(),
		workerPath, string(workerProvider), targetSessionID, projectPath, string(callerProvider), key,
		string(model.EngineerDispatchReplyAwaiting), string(model.EngineerDispatchReplySent))
	if err != nil {
		return fmt.Errorf("arm engineer dispatch reply: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed > 0 {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO engineer_dispatches(
			origin_operation_id, todo_id, todo_text, worker_project_path, worker_provider, worker_session_id,
			caller_project_path, caller_provider, caller_session_key, caller_session_id,
			reply_state, reply_seq, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?)
		ON CONFLICT(origin_operation_id) DO NOTHING
	`, message.OperationID, message.TodoID, message.TodoText, workerPath, string(workerProvider), targetSessionID,
		projectPath, string(callerProvider), key, callerSessionID, string(model.EngineerDispatchReplyAwaiting), now.Unix(), now.Unix()); err != nil {
		return fmt.Errorf("record engineer message return address: %w", err)
	}
	return nil
}

func sessionSourceFromControlProvider(provider control.Provider) model.SessionSource {
	switch provider.Normalized() {
	case control.ProviderCodex:
		return model.SessionSourceCodex
	case control.ProviderOpenCode:
		return model.SessionSourceOpenCode
	case control.ProviderClaudeCode:
		return model.SessionSourceClaudeCode
	case control.ProviderLCAgent:
		return model.SessionSourceLCAgent
	default:
		return model.SessionSourceUnknown
	}
}

func cleanDispatchPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	return filepath.Clean(path)
}

func scanEngineerDispatch(scanner engineerMessageScanner) (model.EngineerDispatch, error) {
	var (
		dispatch                              model.EngineerDispatch
		workerProvider, callerProvider, state string
		createdAt, updatedAt                  int64
	)
	if err := scanner.Scan(
		&dispatch.ID,
		&dispatch.OriginOperationID,
		&dispatch.TodoID,
		&dispatch.TodoText,
		&dispatch.WorkerProjectPath,
		&workerProvider,
		&dispatch.WorkerSessionID,
		&dispatch.CallerProjectPath,
		&callerProvider,
		&dispatch.CallerSessionKey,
		&dispatch.CallerSessionID,
		&state,
		&dispatch.ReplySeq,
		&dispatch.ReplyPrompt,
		&dispatch.ReplyMessageID,
		&dispatch.ReplyError,
		&createdAt,
		&updatedAt,
	); err != nil {
		return model.EngineerDispatch{}, err
	}
	dispatch.WorkerProvider = model.NormalizeSessionSource(model.SessionSource(workerProvider))
	dispatch.CallerProvider = model.NormalizeSessionSource(model.SessionSource(callerProvider))
	dispatch.ReplyState = model.EngineerDispatchReplyState(state)
	dispatch.CreatedAt = time.Unix(createdAt, 0)
	dispatch.UpdatedAt = time.Unix(updatedAt, 0)
	return dispatch, nil
}
