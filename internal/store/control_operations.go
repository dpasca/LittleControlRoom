package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"lcroom/internal/control"
)

func (s *Store) CreateControlOperation(ctx context.Context, operation control.Operation) (control.Operation, error) {
	if s == nil || s.db == nil {
		return control.Operation{}, errors.New("store unavailable")
	}
	operation.ID = strings.TrimSpace(operation.ID)
	operation.ClientRequestID = strings.TrimSpace(operation.ClientRequestID)
	operation.Source = strings.TrimSpace(operation.Source)
	operation.Provider = strings.TrimSpace(operation.Provider)
	operation.SessionKey = strings.TrimSpace(operation.SessionKey)
	operation.ProjectPath = strings.TrimSpace(operation.ProjectPath)
	operation.RequestedBy = strings.TrimSpace(operation.RequestedBy)
	if operation.ID == "" {
		return control.Operation{}, errors.New("control operation id is required")
	}
	if operation.Invocation.RequestID == "" {
		operation.Invocation.RequestID = operation.ID
	}
	if operation.Invocation.RequestID != operation.ID {
		return control.Operation{}, errors.New("control operation invocation request_id must equal operation id")
	}
	normalized, err := control.ValidateInvocation(operation.Invocation)
	if err != nil {
		return control.Operation{}, err
	}
	operation.Invocation = normalized
	operation.Capability = normalized.Capability
	if operation.Status == "" {
		operation.Status = control.OperationProposed
	}
	if operation.Status != control.OperationProposed {
		return control.Operation{}, fmt.Errorf("new control operation status must be %s", control.OperationProposed)
	}
	if operation.Source == "" {
		operation.Source = "unknown"
	}
	if operation.RequestedBy == "" {
		operation.RequestedBy = operation.Provider
	}
	if operation.ClientRequestID != "" {
		existing, found, err := s.FindControlOperationByClientRequest(ctx, operation.Source, operation.SessionKey, operation.ClientRequestID)
		if err != nil {
			return control.Operation{}, err
		}
		if found {
			if !sameControlOperationRequest(existing, operation) {
				return control.Operation{}, errors.New("client request id is already bound to a different control operation")
			}
			return existing, nil
		}
	}
	now := time.Now()
	if operation.CreatedAt.IsZero() {
		operation.CreatedAt = now
	}
	operation.UpdatedAt = now
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO control_operations(
			id, client_request_id, source, provider, session_key, project_path,
			capability, args_json, status, requested_by, confirmed, confirmation_by,
			result_json, error, created_at, updated_at, started_at, completed_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, operation.ID, operation.ClientRequestID, operation.Source, operation.Provider, operation.SessionKey,
		operation.ProjectPath, string(operation.Capability), string(operation.Invocation.Args), string(operation.Status),
		operation.RequestedBy, boolToInt(operation.Confirmed), operation.ConfirmationBy, string(operation.Result),
		operation.Error, operation.CreatedAt.Unix(), operation.UpdatedAt.Unix(),
		nullableTimeUnixValue(operation.StartedAt), nullableTimeUnixValue(operation.CompletedAt)); err != nil {
		if operation.ClientRequestID != "" {
			existing, found, lookupErr := s.FindControlOperationByClientRequest(ctx, operation.Source, operation.SessionKey, operation.ClientRequestID)
			if lookupErr == nil && found {
				if !sameControlOperationRequest(existing, operation) {
					return control.Operation{}, errors.New("client request id is already bound to a different control operation")
				}
				return existing, nil
			}
		}
		return control.Operation{}, fmt.Errorf("create control operation: %w", err)
	}
	return s.GetControlOperation(ctx, operation.ID)
}

func sameControlOperationRequest(existing, proposed control.Operation) bool {
	if existing.Capability != proposed.Capability {
		return false
	}
	return bytes.Equal(
		controlOperationComparableArgs(existing.Invocation.Args),
		controlOperationComparableArgs(proposed.Invocation.Args),
	)
}

func controlOperationComparableArgs(args json.RawMessage) []byte {
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(args, &payload); err != nil {
		return nil
	}
	delete(payload, "request_id")
	normalized, err := json.Marshal(payload)
	if err != nil {
		return nil
	}
	return normalized
}

func (s *Store) GetControlOperation(ctx context.Context, id string) (control.Operation, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return control.Operation{}, errors.New("control operation id is required")
	}
	return scanControlOperation(s.db.QueryRowContext(ctx, controlOperationSelect+` WHERE id = ?`, id))
}

func (s *Store) FindControlOperationByClientRequest(ctx context.Context, source, sessionKey, clientRequestID string) (control.Operation, bool, error) {
	source = strings.TrimSpace(source)
	sessionKey = strings.TrimSpace(sessionKey)
	clientRequestID = strings.TrimSpace(clientRequestID)
	if source == "" || clientRequestID == "" {
		return control.Operation{}, false, nil
	}
	operation, err := scanControlOperation(s.db.QueryRowContext(ctx, controlOperationSelect+`
		WHERE source = ? AND session_key = ? AND client_request_id = ?
	`, source, sessionKey, clientRequestID))
	if errors.Is(err, sql.ErrNoRows) {
		return control.Operation{}, false, nil
	}
	if err != nil {
		return control.Operation{}, false, err
	}
	return operation, true, nil
}

func (s *Store) ClaimNextControlOperation(ctx context.Context) (control.Operation, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return control.Operation{}, false, err
	}
	defer tx.Rollback()
	var id string
	err = tx.QueryRowContext(ctx, `
		SELECT id
		FROM control_operations
		WHERE status = ?
		  AND NOT EXISTS (
			SELECT 1 FROM control_operations WHERE status = ?
		  )
		ORDER BY created_at ASC, id ASC
		LIMIT 1
	`, string(control.OperationProposed), string(control.OperationWaitingForConfirmation)).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return control.Operation{}, false, nil
	}
	if err != nil {
		return control.Operation{}, false, fmt.Errorf("select proposed control operation: %w", err)
	}
	now := time.Now()
	result, err := tx.ExecContext(ctx, `
		UPDATE control_operations
		SET status = ?, updated_at = ?
		WHERE id = ? AND status = ?
	`, string(control.OperationWaitingForConfirmation), now.Unix(), id, string(control.OperationProposed))
	if err != nil {
		return control.Operation{}, false, fmt.Errorf("claim control operation: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return control.Operation{}, false, err
	}
	if changed != 1 {
		return control.Operation{}, false, nil
	}
	if err := tx.Commit(); err != nil {
		return control.Operation{}, false, err
	}
	operation, err := s.GetControlOperation(ctx, id)
	if err != nil {
		return control.Operation{}, false, err
	}
	return operation, true, nil
}

func (s *Store) RequeueWaitingControlOperations(ctx context.Context) error {
	if s == nil || s.db == nil {
		return errors.New("store unavailable")
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE control_operations
		SET status = ?, confirmed = 0, confirmation_by = '', updated_at = ?
		WHERE status = ?
	`, string(control.OperationProposed), time.Now().Unix(), string(control.OperationWaitingForConfirmation))
	if err != nil {
		return fmt.Errorf("requeue waiting control operations: %w", err)
	}
	return nil
}

func (s *Store) UpdateControlOperationStatus(ctx context.Context, id string, status control.OperationStatus, result json.RawMessage, operationErr error) (control.Operation, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return control.Operation{}, errors.New("control operation id is required")
	}
	switch status {
	case control.OperationProposed, control.OperationWaitingForConfirmation, control.OperationRunning,
		control.OperationCompleted, control.OperationFailed, control.OperationCanceled:
	default:
		return control.Operation{}, fmt.Errorf("unsupported control operation status: %s", status)
	}
	current, err := s.GetControlOperation(ctx, id)
	if err != nil {
		return control.Operation{}, err
	}
	if current.Status.Terminal() {
		return current, nil
	}
	now := time.Now()
	confirmed := current.Confirmed
	confirmationBy := current.ConfirmationBy
	startedAt := current.StartedAt
	completedAt := current.CompletedAt
	if status == control.OperationRunning {
		confirmed = true
		confirmationBy = "operator"
		if startedAt.IsZero() {
			startedAt = now
		}
	}
	if status.Terminal() {
		completedAt = now
	}
	errText := ""
	if operationErr != nil {
		errText = strings.TrimSpace(operationErr.Error())
	}
	if result == nil {
		result = current.Result
	}
	if _, err := s.db.ExecContext(ctx, `
		UPDATE control_operations
		SET status = ?, confirmed = ?, confirmation_by = ?, result_json = ?, error = ?,
			updated_at = ?, started_at = ?, completed_at = ?
		WHERE id = ?
	`, string(status), boolToInt(confirmed), confirmationBy, string(result), errText, now.Unix(),
		nullableTimeUnixValue(startedAt), nullableTimeUnixValue(completedAt), id); err != nil {
		return control.Operation{}, fmt.Errorf("update control operation: %w", err)
	}
	return s.GetControlOperation(ctx, id)
}

const controlOperationSelect = `
	SELECT id, client_request_id, source, provider, session_key, project_path,
		capability, args_json, status, requested_by, confirmed, confirmation_by,
		result_json, error, created_at, updated_at, started_at, completed_at
	FROM control_operations
`

type controlOperationScanner interface {
	Scan(dest ...any) error
}

func scanControlOperation(scanner controlOperationScanner) (control.Operation, error) {
	var (
		operation                   control.Operation
		capability, status, argsRaw string
		resultRaw                   string
		confirmed                   int
		createdAt, updatedAt        int64
		startedAt, completedAt      sql.NullInt64
	)
	if err := scanner.Scan(
		&operation.ID,
		&operation.ClientRequestID,
		&operation.Source,
		&operation.Provider,
		&operation.SessionKey,
		&operation.ProjectPath,
		&capability,
		&argsRaw,
		&status,
		&operation.RequestedBy,
		&confirmed,
		&operation.ConfirmationBy,
		&resultRaw,
		&operation.Error,
		&createdAt,
		&updatedAt,
		&startedAt,
		&completedAt,
	); err != nil {
		return control.Operation{}, err
	}
	operation.Capability = control.CapabilityName(capability)
	operation.Status = control.OperationStatus(status)
	operation.Confirmed = confirmed != 0
	operation.Invocation = control.Invocation{
		RequestID:  operation.ID,
		Capability: operation.Capability,
		Args:       json.RawMessage(argsRaw),
	}
	operation.Result = json.RawMessage(resultRaw)
	operation.CreatedAt = time.Unix(createdAt, 0)
	operation.UpdatedAt = time.Unix(updatedAt, 0)
	if startedAt.Valid {
		operation.StartedAt = time.Unix(startedAt.Int64, 0)
	}
	if completedAt.Valid {
		operation.CompletedAt = time.Unix(completedAt.Int64, 0)
	}
	return operation, nil
}
