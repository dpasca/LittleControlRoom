package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"lcroom/internal/control"
)

const engineerMessageSelect = `
	SELECT id, operation_id, agent_task_id, project_path, provider, session_mode, requested_target_session_id,
		target_session_id, prompt,
		reveal, todo_id, todo_label, todo_text, state, attempt_count,
		last_error, created_at, updated_at, delivered_at, model_selection_json
	FROM engineer_messages
`

func (s *Store) CreateEngineerMessage(ctx context.Context, message control.EngineerMessage) (control.EngineerMessage, error) {
	if s == nil || s.db == nil {
		return control.EngineerMessage{}, errors.New("store unavailable")
	}
	message.ID = strings.TrimSpace(message.ID)
	message.OperationID = strings.TrimSpace(message.OperationID)
	message.AgentTaskID = strings.TrimSpace(message.AgentTaskID)
	message.ProjectPath = strings.TrimSpace(message.ProjectPath)
	message.Provider = message.Provider.Normalized()
	message.SessionMode = message.SessionMode.Normalized()
	message.RequestedTargetSessionID = strings.TrimSpace(message.RequestedTargetSessionID)
	message.TargetSessionID = strings.TrimSpace(message.TargetSessionID)
	if message.RequestedTargetSessionID == "" {
		message.RequestedTargetSessionID = message.TargetSessionID
	}
	if err := message.EngineerModelSelection.Normalize(message.Provider); err != nil {
		return control.EngineerMessage{}, err
	}
	if message.SelectModel {
		return control.EngineerMessage{}, errors.New("model picker must complete before queueing")
	}
	message.Prompt = strings.TrimSpace(message.Prompt)
	message.TodoLabel = strings.TrimSpace(message.TodoLabel)
	message.TodoText = strings.TrimSpace(message.TodoText)
	if message.ProjectPath == "" {
		return control.EngineerMessage{}, errors.New("engineer message project path is required")
	}
	if message.Provider == "" || message.Provider == control.ProviderAuto {
		return control.EngineerMessage{}, errors.New("engineer message requires an explicit provider")
	}
	if message.SessionMode == "" {
		return control.EngineerMessage{}, errors.New("engineer message session mode is required")
	}
	if (message.RequestedTargetSessionID != "" || message.TargetSessionID != "") && message.SessionMode != control.SessionModeResumeOrNew {
		return control.EngineerMessage{}, fmt.Errorf("target session requires mode %s", control.SessionModeResumeOrNew)
	}
	if message.Prompt == "" {
		return control.EngineerMessage{}, errors.New("engineer message prompt is required")
	}
	if message.State == "" {
		message.State = control.EngineerMessageQueued
	}
	if message.State != control.EngineerMessageQueued {
		return control.EngineerMessage{}, fmt.Errorf("new engineer message state must be %s", control.EngineerMessageQueued)
	}
	if message.OperationID != "" {
		existing, found, err := s.FindEngineerMessageByOperation(ctx, message.OperationID)
		if err != nil {
			return control.EngineerMessage{}, err
		}
		if found {
			if !sameEngineerMessageRequest(existing, message) {
				return control.EngineerMessage{}, errors.New("control operation is already bound to a different engineer message")
			}
			return existing, s.linkEngineerMessageToAgentTask(ctx, existing)
		}
	}
	if message.ID == "" {
		id, err := control.NewEngineerMessageID()
		if err != nil {
			return control.EngineerMessage{}, err
		}
		message.ID = id
	}
	now := time.Now()
	if message.CreatedAt.IsZero() {
		message.CreatedAt = now
	}
	message.UpdatedAt = now
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO engineer_messages(
			id, operation_id, agent_task_id, project_path, provider, session_mode, requested_target_session_id,
			target_session_id, prompt,
			reveal, todo_id, todo_label, todo_text, state, attempt_count,
			last_error, created_at, updated_at, delivered_at, model_selection_json
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, message.ID, message.OperationID, message.AgentTaskID, message.ProjectPath, string(message.Provider),
		string(message.SessionMode), message.RequestedTargetSessionID, message.TargetSessionID, message.Prompt,
		boolToInt(message.Reveal), message.TodoID,
		message.TodoLabel, message.TodoText, string(message.State), message.AttemptCount,
		message.LastError, message.CreatedAt.Unix(), message.UpdatedAt.Unix(), nil, modelSelectionJSON(message.EngineerModelSelection))
	if err != nil {
		if message.OperationID != "" {
			existing, found, lookupErr := s.FindEngineerMessageByOperation(ctx, message.OperationID)
			if lookupErr == nil && found {
				if !sameEngineerMessageRequest(existing, message) {
					return control.EngineerMessage{}, errors.New("control operation is already bound to a different engineer message")
				}
				return existing, s.linkEngineerMessageToAgentTask(ctx, existing)
			}
		}
		return control.EngineerMessage{}, fmt.Errorf("create engineer message: %w", err)
	}
	created, err := s.GetEngineerMessage(ctx, message.ID)
	if err != nil {
		return control.EngineerMessage{}, err
	}
	return created, s.linkEngineerMessageToAgentTask(ctx, created)
}

func (s *Store) linkEngineerMessageToAgentTask(ctx context.Context, message control.EngineerMessage) error {
	if strings.TrimSpace(message.AgentTaskID) == "" {
		return nil
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE agent_tasks
		SET result_message_id = ?, updated_at = ?
		WHERE id = ?
	`, message.ID, time.Now().Unix(), message.AgentTaskID)
	if err != nil {
		return fmt.Errorf("link engineer message to agent task: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return fmt.Errorf("link engineer message to missing agent task: %s", message.AgentTaskID)
	}
	return nil
}

func sameEngineerMessageRequest(existing, proposed control.EngineerMessage) bool {
	return existing.EngineerModelSelection == proposed.EngineerModelSelection &&
		existing.AgentTaskID == proposed.AgentTaskID &&
		existing.ProjectPath == proposed.ProjectPath &&
		existing.Provider == proposed.Provider &&
		existing.SessionMode == proposed.SessionMode &&
		existing.RequestedTargetSessionID == proposed.RequestedTargetSessionID &&
		existing.Prompt == proposed.Prompt &&
		existing.Reveal == proposed.Reveal &&
		existing.TodoID == proposed.TodoID &&
		existing.TodoLabel == proposed.TodoLabel &&
		existing.TodoText == proposed.TodoText
}

func (s *Store) GetEngineerMessage(ctx context.Context, id string) (control.EngineerMessage, error) {
	if s == nil || s.db == nil {
		return control.EngineerMessage{}, errors.New("store unavailable")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return control.EngineerMessage{}, errors.New("engineer message id is required")
	}
	return scanEngineerMessage(s.db.QueryRowContext(ctx, engineerMessageSelect+` WHERE id = ?`, id))
}

func (s *Store) FindEngineerMessageByOperation(ctx context.Context, operationID string) (control.EngineerMessage, bool, error) {
	if s == nil || s.db == nil {
		return control.EngineerMessage{}, false, errors.New("store unavailable")
	}
	operationID = strings.TrimSpace(operationID)
	if operationID == "" {
		return control.EngineerMessage{}, false, nil
	}
	message, err := scanEngineerMessage(s.db.QueryRowContext(ctx, engineerMessageSelect+` WHERE operation_id = ?`, operationID))
	if errors.Is(err, sql.ErrNoRows) {
		return control.EngineerMessage{}, false, nil
	}
	if err != nil {
		return control.EngineerMessage{}, false, err
	}
	return message, true, nil
}

func (s *Store) ListQueuedEngineerMessages(ctx context.Context, limit int) ([]control.EngineerMessage, error) {
	return s.ListQueuedEngineerMessagesAfter(ctx, time.Time{}, "", limit)
}

func (s *Store) ListQueuedEngineerMessagesAfter(ctx context.Context, afterCreatedAt time.Time, afterID string, limit int) ([]control.EngineerMessage, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("store unavailable")
	}
	if limit <= 0 {
		limit = 32
	}
	afterID = strings.TrimSpace(afterID)
	query := engineerMessageSelect + `
		WHERE state = ?
	`
	args := []any{string(control.EngineerMessageQueued)}
	if !afterCreatedAt.IsZero() && afterID != "" {
		query += ` AND (created_at > ? OR (created_at = ? AND id > ?))`
		afterUnix := afterCreatedAt.Unix()
		args = append(args, afterUnix, afterUnix, afterID)
	}
	query += ` ORDER BY created_at ASC, id ASC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list queued engineer messages: %w", err)
	}
	defer rows.Close()
	messages := make([]control.EngineerMessage, 0)
	for rows.Next() {
		message, err := scanEngineerMessage(rows)
		if err != nil {
			return nil, err
		}
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read queued engineer messages: %w", err)
	}
	return messages, nil
}

func (s *Store) RequeueDeliveringEngineerMessages(ctx context.Context) error {
	if s == nil || s.db == nil {
		return errors.New("store unavailable")
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE engineer_messages
		SET state = ?, last_error = ?, updated_at = ?
		WHERE state = ?
	`, string(control.EngineerMessageQueued), "Delivery was interrupted by an LCR restart; retrying.", time.Now().Unix(), string(control.EngineerMessageDelivering))
	if err != nil {
		return fmt.Errorf("requeue delivering engineer messages: %w", err)
	}
	return nil
}

func (s *Store) ClaimEngineerMessage(ctx context.Context, id, targetSessionID string) (control.EngineerMessage, bool, error) {
	if s == nil || s.db == nil {
		return control.EngineerMessage{}, false, errors.New("store unavailable")
	}
	id = strings.TrimSpace(id)
	targetSessionID = strings.TrimSpace(targetSessionID)
	if id == "" {
		return control.EngineerMessage{}, false, errors.New("engineer message id is required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return control.EngineerMessage{}, false, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
		UPDATE engineer_messages
		SET state = ?,
			target_session_id = CASE WHEN target_session_id = '' THEN ? ELSE target_session_id END,
			attempt_count = attempt_count + 1,
			last_error = '',
			updated_at = ?
		WHERE id = ? AND state = ?
	`, string(control.EngineerMessageDelivering), targetSessionID, time.Now().Unix(), id, string(control.EngineerMessageQueued))
	if err != nil {
		return control.EngineerMessage{}, false, fmt.Errorf("claim engineer message: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return control.EngineerMessage{}, false, err
	}
	if changed != 1 {
		return control.EngineerMessage{}, false, nil
	}
	message, err := scanEngineerMessage(tx.QueryRowContext(ctx, engineerMessageSelect+` WHERE id = ?`, id))
	if err != nil {
		return control.EngineerMessage{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return control.EngineerMessage{}, false, err
	}
	return message, true, nil
}

func (s *Store) RecordEngineerMessageState(
	ctx context.Context,
	id string,
	state control.EngineerMessageState,
	targetSessionID string,
	statusText string,
	stateErr error,
) (control.EngineerMessage, error) {
	if s == nil || s.db == nil {
		return control.EngineerMessage{}, errors.New("store unavailable")
	}
	id = strings.TrimSpace(id)
	targetSessionID = strings.TrimSpace(targetSessionID)
	statusText = strings.TrimSpace(statusText)
	if id == "" {
		return control.EngineerMessage{}, errors.New("engineer message id is required")
	}
	switch state {
	case control.EngineerMessageQueued, control.EngineerMessageDelivered, control.EngineerMessageFailed:
	default:
		return control.EngineerMessage{}, fmt.Errorf("unsupported engineer message state: %s", state)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return control.EngineerMessage{}, err
	}
	defer tx.Rollback()
	current, err := scanEngineerMessage(tx.QueryRowContext(ctx, engineerMessageSelect+` WHERE id = ?`, id))
	if err != nil {
		return control.EngineerMessage{}, err
	}
	if current.State.Terminal() {
		return current, nil
	}
	now := time.Now()
	errText := ""
	if stateErr != nil {
		errText = strings.TrimSpace(stateErr.Error())
	}
	var deliveredAt any
	if state == control.EngineerMessageDelivered {
		deliveredAt = now.Unix()
	}
	_, err = tx.ExecContext(ctx, `
		UPDATE engineer_messages
		SET state = ?,
			target_session_id = CASE WHEN ? <> '' THEN ? ELSE target_session_id END,
			last_error = ?, updated_at = ?, delivered_at = ?
		WHERE id = ?
	`, string(state), targetSessionID, targetSessionID, errText, now.Unix(), deliveredAt, id)
	if err != nil {
		return control.EngineerMessage{}, fmt.Errorf("record engineer message state: %w", err)
	}
	current.State = state
	if targetSessionID != "" {
		current.TargetSessionID = targetSessionID
	}
	receipt := control.EngineerMessageReceipt{
		MessageID:       current.ID,
		State:           state,
		Provider:        current.Provider,
		ProjectPath:     current.ProjectPath,
		TargetSessionID: current.TargetSessionID,
		Status:          statusText,
	}
	resultJSON, _ := json.Marshal(map[string]any{
		"status":   statusText,
		"delivery": receipt,
	})
	if current.OperationID != "" {
		operationStatus := control.OperationRunning
		var completedAt any
		switch state {
		case control.EngineerMessageDelivered:
			operationStatus = control.OperationCompleted
			completedAt = now.Unix()
		case control.EngineerMessageFailed:
			operationStatus = control.OperationFailed
			completedAt = now.Unix()
		}
		_, err = tx.ExecContext(ctx, `
			UPDATE control_operations
			SET status = ?, confirmed = 1,
				confirmation_by = CASE WHEN confirmation_by = '' THEN 'operator' ELSE confirmation_by END,
				result_json = ?, error = ?, updated_at = ?,
				started_at = COALESCE(started_at, ?), completed_at = ?
			WHERE id = ? AND status NOT IN (?, ?, ?)
		`, string(operationStatus), string(resultJSON), errText, now.Unix(), now.Unix(), completedAt,
			current.OperationID, string(control.OperationCompleted), string(control.OperationFailed), string(control.OperationCanceled))
		if err != nil {
			return control.EngineerMessage{}, fmt.Errorf("record engineer message operation state: %w", err)
		}
	}
	if strings.TrimSpace(current.AgentTaskID) != "" {
		deliveryError := ""
		if state == control.EngineerMessageFailed {
			deliveryError = firstNonEmptyString(errText, statusText, "result callback delivery failed")
		}
		var deliveredAt any
		if state == control.EngineerMessageDelivered {
			deliveredAt = now.Unix()
		}
		_, err = tx.ExecContext(ctx, `
			UPDATE agent_tasks
			SET result_delivered_at = CASE
					WHEN ? IS NOT NULL THEN COALESCE(result_delivered_at, ?)
					ELSE result_delivered_at
				END,
				result_delivery_error = ?, updated_at = ?
			WHERE id = ? AND result_message_id = ?
		`, deliveredAt, deliveredAt, deliveryError, now.Unix(), current.AgentTaskID, current.ID)
		if err != nil {
			return control.EngineerMessage{}, fmt.Errorf("record agent task result delivery state: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return control.EngineerMessage{}, err
	}
	return s.GetEngineerMessage(ctx, id)
}

type engineerMessageScanner interface {
	Scan(dest ...any) error
}

func scanEngineerMessage(scanner engineerMessageScanner) (control.EngineerMessage, error) {
	var (
		selectionJSON                string
		message                      control.EngineerMessage
		provider, sessionMode, state string
		reveal                       int
		createdAt, updatedAt         int64
		deliveredAt                  sql.NullInt64
	)
	if err := scanner.Scan(
		&message.ID,
		&message.OperationID,
		&message.AgentTaskID,
		&message.ProjectPath,
		&provider,
		&sessionMode,
		&message.RequestedTargetSessionID,
		&message.TargetSessionID,
		&message.Prompt,
		&reveal,
		&message.TodoID,
		&message.TodoLabel,
		&message.TodoText,
		&state,
		&message.AttemptCount,
		&message.LastError,
		&createdAt,
		&updatedAt,
		&deliveredAt,
		&selectionJSON,
	); err != nil {
		return control.EngineerMessage{}, err
	}
	if err := json.Unmarshal([]byte(selectionJSON), &message.EngineerModelSelection); err != nil {
		return control.EngineerMessage{}, err
	}
	message.Provider = control.NormalizeProvider(provider)
	message.SessionMode = control.NormalizeSessionMode(sessionMode)
	message.State = control.EngineerMessageState(state)
	message.Reveal = reveal != 0
	message.CreatedAt = time.Unix(createdAt, 0)
	message.UpdatedAt = time.Unix(updatedAt, 0)
	if deliveredAt.Valid {
		message.DeliveredAt = time.Unix(deliveredAt.Int64, 0)
	}
	return message, nil
}
