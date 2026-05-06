package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"lcroom/internal/bossrun"
	"lcroom/internal/control"
)

func (s *Store) CreateGoalRun(ctx context.Context, proposal bossrun.GoalProposal) (bossrun.GoalRecord, error) {
	normalized, err := bossrun.NormalizeGoalProposal(proposal)
	if err != nil {
		return bossrun.GoalRecord{}, err
	}
	if strings.TrimSpace(normalized.Run.ID) == "" {
		normalized.Run.ID, err = newGoalRunID()
		if err != nil {
			return bossrun.GoalRecord{}, err
		}
	}
	now := time.Now()
	if normalized.Run.CreatedAt.IsZero() {
		normalized.Run.CreatedAt = now
	}
	normalized.Run.UpdatedAt = now
	if strings.TrimSpace(normalized.Run.Status) == "" {
		normalized.Run.Status = bossrun.GoalStatusWaitingForApproval
	}
	proposalJSON, err := encodeGoalJSON(normalized)
	if err != nil {
		return bossrun.GoalRecord{}, err
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO boss_goal_runs(
			id, kind, title, objective, success_criteria, status, proposal_json, result_json, error, created_at, updated_at, completed_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, '{}', '', ?, ?, ?)
	`, normalized.Run.ID, normalized.Run.Kind, normalized.Run.Title, normalized.Run.Objective, normalized.Run.SuccessCriteria,
		normalized.Run.Status, proposalJSON, normalized.Run.CreatedAt.Unix(), normalized.Run.UpdatedAt.Unix(), nullableTimeUnixValue(normalized.Run.CompletedAt)); err != nil {
		return bossrun.GoalRecord{}, fmt.Errorf("create goal run: %w", err)
	}
	return s.GetGoalRun(ctx, normalized.Run.ID)
}

func (s *Store) GetGoalRun(ctx context.Context, id string) (bossrun.GoalRecord, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return bossrun.GoalRecord{}, errors.New("goal run id is required")
	}
	record, err := scanGoalRunRecord(s.db.QueryRowContext(ctx, `
		SELECT proposal_json, result_json, error, created_at, updated_at, completed_at
		FROM boss_goal_runs
		WHERE id = ?
	`, id))
	if err != nil {
		return bossrun.GoalRecord{}, err
	}
	trace, err := s.ListGoalRunTrace(ctx, id)
	if err != nil {
		return bossrun.GoalRecord{}, err
	}
	record.Trace = trace
	record.Result.Trace = append([]bossrun.TraceEntry(nil), trace...)
	return record, nil
}

func (s *Store) ListGoalRuns(ctx context.Context, limit int) ([]bossrun.GoalRecord, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT proposal_json, result_json, error, created_at, updated_at, completed_at
		FROM boss_goal_runs
		ORDER BY updated_at DESC, created_at DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("list goal runs: %w", err)
	}
	defer rows.Close()

	records := []bossrun.GoalRecord{}
	for rows.Next() {
		record, err := scanGoalRunRecord(rows)
		if err != nil {
			return nil, fmt.Errorf("scan goal run: %w", err)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read goal runs: %w", err)
	}
	for i := range records {
		trace, err := s.ListGoalRunTrace(ctx, records[i].Proposal.Run.ID)
		if err != nil {
			return nil, err
		}
		records[i].Trace = trace
		records[i].Result.Trace = append([]bossrun.TraceEntry(nil), trace...)
	}
	return records, nil
}

func (s *Store) UpdateGoalRunStatus(ctx context.Context, runID, status string) (bossrun.GoalRecord, error) {
	runID = strings.TrimSpace(runID)
	status = strings.TrimSpace(status)
	if runID == "" {
		return bossrun.GoalRecord{}, errors.New("goal run id is required")
	}
	if status == "" {
		return bossrun.GoalRecord{}, errors.New("goal run status is required")
	}
	record, err := s.GetGoalRun(ctx, runID)
	if err != nil {
		return bossrun.GoalRecord{}, err
	}
	now := time.Now()
	record.Proposal.Run.Status = status
	record.Proposal.Run.UpdatedAt = now
	if status == bossrun.GoalStatusCompleted || status == bossrun.GoalStatusFailed {
		record.Proposal.Run.CompletedAt = now
	}
	proposalJSON, err := encodeGoalJSON(record.Proposal)
	if err != nil {
		return bossrun.GoalRecord{}, err
	}
	_, err = s.db.ExecContext(ctx, `
		UPDATE boss_goal_runs
		SET status = ?, proposal_json = ?, updated_at = ?, completed_at = ?
		WHERE id = ?
	`, status, proposalJSON, now.Unix(), nullableTimeUnixValue(record.Proposal.Run.CompletedAt), runID)
	if err != nil {
		return bossrun.GoalRecord{}, fmt.Errorf("update goal run status: %w", err)
	}
	return s.GetGoalRun(ctx, runID)
}

func (s *Store) AppendGoalRunTrace(ctx context.Context, runID string, entry bossrun.TraceEntry) error {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return errors.New("goal run id is required")
	}
	entry.StepID = strings.TrimSpace(entry.StepID)
	entry.Capability = control.CapabilityName(strings.TrimSpace(string(entry.Capability)))
	entry.ResourceID = strings.TrimSpace(entry.ResourceID)
	entry.Status = strings.TrimSpace(entry.Status)
	entry.Summary = strings.TrimSpace(entry.Summary)
	if entry.At.IsZero() {
		entry.At = time.Now()
	}
	payloadJSON, err := encodeGoalJSON(entry)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var current sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT MAX(position) FROM boss_goal_trace_entries WHERE run_id = ?`, runID).Scan(&current); err != nil {
		return fmt.Errorf("read goal trace position: %w", err)
	}
	position := int64(1)
	if current.Valid {
		position = current.Int64 + 1
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO boss_goal_trace_entries(run_id, position, step_id, capability, resource_id, status, summary, at, payload_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, runID, position, entry.StepID, string(entry.Capability), entry.ResourceID, entry.Status, entry.Summary, entry.At.Unix(), payloadJSON); err != nil {
		return fmt.Errorf("append goal trace: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE boss_goal_runs SET updated_at = ? WHERE id = ?`, time.Now().Unix(), runID); err != nil {
		return fmt.Errorf("touch goal run: %w", err)
	}
	return tx.Commit()
}

func (s *Store) CompleteGoalRun(ctx context.Context, result bossrun.GoalResult, runErr error) (bossrun.GoalRecord, error) {
	runID := strings.TrimSpace(result.RunID)
	if runID == "" {
		return bossrun.GoalRecord{}, errors.New("goal run id is required")
	}
	record, err := s.GetGoalRun(ctx, runID)
	if err != nil {
		return bossrun.GoalRecord{}, err
	}
	trace, err := s.ListGoalRunTrace(ctx, runID)
	if err != nil {
		return bossrun.GoalRecord{}, err
	}
	now := time.Now()
	if strings.TrimSpace(result.Kind) == "" {
		result.Kind = record.Proposal.Run.Kind
	}
	result.Trace = append([]bossrun.TraceEntry(nil), trace...)
	if strings.TrimSpace(result.Summary) == "" {
		result.Summary = bossrun.FormatGoalResult(result)
	}
	status := bossrun.GoalStatusCompleted
	errText := ""
	if runErr != nil {
		status = bossrun.GoalStatusFailed
		errText = strings.TrimSpace(runErr.Error())
	}
	record.Proposal.Run.Status = status
	record.Proposal.Run.UpdatedAt = now
	record.Proposal.Run.CompletedAt = now
	proposalJSON, err := encodeGoalJSON(record.Proposal)
	if err != nil {
		return bossrun.GoalRecord{}, err
	}
	resultJSON, err := encodeGoalJSON(result)
	if err != nil {
		return bossrun.GoalRecord{}, err
	}
	if _, err := s.db.ExecContext(ctx, `
		UPDATE boss_goal_runs
		SET status = ?, proposal_json = ?, result_json = ?, error = ?, updated_at = ?, completed_at = ?
		WHERE id = ?
	`, status, proposalJSON, resultJSON, errText, now.Unix(), now.Unix(), runID); err != nil {
		return bossrun.GoalRecord{}, fmt.Errorf("complete goal run: %w", err)
	}
	return s.GetGoalRun(ctx, runID)
}

func (s *Store) ListGoalRunTrace(ctx context.Context, runID string) ([]bossrun.TraceEntry, error) {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return nil, errors.New("goal run id is required")
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT payload_json, step_id, capability, resource_id, status, summary, at
		FROM boss_goal_trace_entries
		WHERE run_id = ?
		ORDER BY position ASC
	`, runID)
	if err != nil {
		return nil, fmt.Errorf("list goal trace: %w", err)
	}
	defer rows.Close()
	trace := []bossrun.TraceEntry{}
	for rows.Next() {
		entry, err := scanGoalTraceEntry(rows)
		if err != nil {
			return nil, fmt.Errorf("scan goal trace: %w", err)
		}
		trace = append(trace, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read goal trace: %w", err)
	}
	return trace, nil
}

func scanGoalRunRecord(scanner interface {
	Scan(dest ...any) error
}) (bossrun.GoalRecord, error) {
	var (
		proposalJSON string
		resultJSON   string
		completedAt  sql.NullInt64
		createdAt    int64
		updatedAt    int64
		record       bossrun.GoalRecord
	)
	if err := scanner.Scan(&proposalJSON, &resultJSON, &record.Error, &createdAt, &updatedAt, &completedAt); err != nil {
		return bossrun.GoalRecord{}, err
	}
	if err := json.Unmarshal([]byte(proposalJSON), &record.Proposal); err != nil {
		return bossrun.GoalRecord{}, fmt.Errorf("decode goal proposal: %w", err)
	}
	if strings.TrimSpace(resultJSON) != "" && strings.TrimSpace(resultJSON) != "{}" {
		if err := json.Unmarshal([]byte(resultJSON), &record.Result); err != nil {
			return bossrun.GoalRecord{}, fmt.Errorf("decode goal result: %w", err)
		}
	}
	record.CreatedAt = time.Unix(createdAt, 0)
	record.UpdatedAt = time.Unix(updatedAt, 0)
	if completedAt.Valid {
		record.CompletedAt = time.Unix(completedAt.Int64, 0)
	}
	return record, nil
}

func scanGoalTraceEntry(scanner interface {
	Scan(dest ...any) error
}) (bossrun.TraceEntry, error) {
	var (
		payloadJSON string
		stepID      string
		capability  string
		resourceID  string
		status      string
		summary     string
		at          int64
		entry       bossrun.TraceEntry
	)
	if err := scanner.Scan(&payloadJSON, &stepID, &capability, &resourceID, &status, &summary, &at); err != nil {
		return bossrun.TraceEntry{}, err
	}
	if strings.TrimSpace(payloadJSON) != "" && strings.TrimSpace(payloadJSON) != "{}" {
		if err := json.Unmarshal([]byte(payloadJSON), &entry); err != nil {
			return bossrun.TraceEntry{}, fmt.Errorf("decode goal trace entry: %w", err)
		}
	}
	if strings.TrimSpace(entry.StepID) == "" {
		entry.StepID = strings.TrimSpace(stepID)
	}
	if strings.TrimSpace(entry.ResourceID) == "" {
		entry.ResourceID = strings.TrimSpace(resourceID)
	}
	if strings.TrimSpace(entry.Status) == "" {
		entry.Status = strings.TrimSpace(status)
	}
	if strings.TrimSpace(entry.Summary) == "" {
		entry.Summary = strings.TrimSpace(summary)
	}
	if strings.TrimSpace(string(entry.Capability)) == "" {
		entry.Capability = control.CapabilityName(strings.TrimSpace(capability))
	}
	if entry.At.IsZero() {
		entry.At = time.Unix(at, 0)
	}
	return entry, nil
}

func encodeGoalJSON(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func newGoalRunID() (string, error) {
	random := make([]byte, 5)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("generate goal run id: %w", err)
	}
	return "goal_" + time.Now().UTC().Format("20060102T150405") + "_" + hex.EncodeToString(random), nil
}
