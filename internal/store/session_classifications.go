package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"lcroom/internal/model"
	"strings"
	"time"
)

type sessionClassificationLookupMatch struct {
	sessionID    string
	source       model.SessionSource
	rawSessionID string
	status       model.SessionClassificationStatus
	summary      string
	updatedAt    time.Time
	completedAt  time.Time
}

func storedSessionClassificationIsCanonical(match sessionClassificationLookupMatch) bool {
	if match.source == model.SessionSourceUnknown || strings.TrimSpace(match.rawSessionID) == "" {
		return false
	}
	return strings.TrimSpace(match.sessionID) == model.BuildCanonicalSessionID(match.source, match.rawSessionID)
}

func sessionClassificationLookupRank(match sessionClassificationLookupMatch, requestedID string) int {
	rank := 0
	if storedSessionClassificationIsCanonical(match) {
		rank += 100
	}
	switch match.status {
	case model.ClassificationCompleted:
		rank += 30
	case model.ClassificationRunning:
		rank += 20
	case model.ClassificationPending:
		rank += 10
	case model.ClassificationFailed:
		rank += 5
	}
	if strings.TrimSpace(match.summary) != "" {
		rank += 3
	}
	if strings.TrimSpace(match.sessionID) == strings.TrimSpace(requestedID) {
		rank++
	}
	return rank
}

func preferSessionClassificationLookupMatch(candidate, existing sessionClassificationLookupMatch, requestedID string) bool {
	candidateRank := sessionClassificationLookupRank(candidate, requestedID)
	existingRank := sessionClassificationLookupRank(existing, requestedID)
	if candidateRank != existingRank {
		return candidateRank > existingRank
	}
	if candidate.completedAt.After(existing.completedAt) {
		return true
	}
	if existing.completedAt.After(candidate.completedAt) {
		return false
	}
	if candidate.updatedAt.After(existing.updatedAt) {
		return true
	}
	if existing.updatedAt.After(candidate.updatedAt) {
		return false
	}
	return strings.Compare(candidate.sessionID, existing.sessionID) < 0
}

func (s *Store) loadSessionClassificationLookupMatches(ctx context.Context, sessionID string) ([]sessionClassificationLookupMatch, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT session_id, source, raw_session_id, status, summary, updated_at, completed_at
		FROM session_classifications
		WHERE session_id = ? OR raw_session_id = ?
	`, sessionID, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []sessionClassificationLookupMatch{}
	for rows.Next() {
		var (
			match                   sessionClassificationLookupMatch
			source, status, summary string
			completedAt             sql.NullInt64
			updatedAt               int64
		)
		if err := rows.Scan(&match.sessionID, &source, &match.rawSessionID, &status, &summary, &updatedAt, &completedAt); err != nil {
			return nil, err
		}
		match.source = model.NormalizeSessionSource(model.SessionSource(source))
		match.status = model.SessionClassificationStatus(status)
		match.summary = strings.TrimSpace(summary)
		match.updatedAt = time.Unix(updatedAt, 0)
		if completedAt.Valid {
			match.completedAt = time.Unix(completedAt.Int64, 0)
		}
		out = append(out, match)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) QueueSessionClassification(ctx context.Context, classification model.SessionClassification, retryAfter time.Duration) (bool, error) {
	classification = model.NormalizeSessionClassificationIdentity(classification)
	if classification.SessionID == "" {
		return false, errors.New("session classification requires session_id")
	}
	if classification.ProjectPath == "" {
		return false, errors.New("session classification requires project_path")
	}
	if classification.SnapshotHash == "" {
		return false, errors.New("session classification requires snapshot_hash")
	}

	now := time.Now()
	if classification.Status == "" {
		classification.Status = model.ClassificationPending
	}
	if classification.CreatedAt.IsZero() {
		classification.CreatedAt = now
	}
	if classification.SourceUpdatedAt.IsZero() {
		classification.SourceUpdatedAt = now
	}
	if classification.Stage == "" && classification.Status == model.ClassificationPending {
		classification.Stage = model.ClassificationStageQueued
	}
	if classification.StageStartedAt.IsZero() && classification.Stage != "" {
		classification.StageStartedAt = now
	}

	existing, err := s.GetSessionClassification(ctx, classification.SessionID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if errors.Is(err, sql.ErrNoRows) {
		_, err = s.db.ExecContext(ctx, `
			INSERT INTO session_classifications(
				session_id, source, raw_session_id, project_path, session_file, session_format, snapshot_hash,
				status, stage, category, summary, confidence, model, classifier_version,
				last_error, source_updated_at, created_at, stage_started_at, updated_at, completed_at
			)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, '', '', 0, ?, ?, '', ?, ?, ?, ?, NULL)
		`, classification.SessionID, string(classification.Source), classification.RawSessionID, classification.ProjectPath, classification.SessionFile, classification.SessionFormat,
			classification.SnapshotHash, string(model.ClassificationPending), string(classification.Stage), classification.Model,
			classification.ClassifierVersion, classification.SourceUpdatedAt.Unix(), classification.CreatedAt.Unix(), nullableTimeUnixValue(classification.StageStartedAt), now.Unix())
		return err == nil, err
	}

	sameSnapshot := existing.SnapshotHash == classification.SnapshotHash &&
		sameClassificationModel(existing.Model, classification.Model) &&
		existing.ClassifierVersion == classification.ClassifierVersion

	if sameSnapshot {
		switch existing.Status {
		case model.ClassificationCompleted:
			if strings.TrimSpace(existing.Summary) != "" {
				return false, nil
			}
		case model.ClassificationPending:
			return false, nil
		case model.ClassificationRunning:
			if retryAfter <= 0 {
				return false, nil
			}
			if !existing.UpdatedAt.IsZero() && now.Sub(existing.UpdatedAt) < retryAfter {
				return false, nil
			}
		case model.ClassificationFailed:
			if retryAfter <= 0 {
				break
			}
			if staleUnsupportedSessionFormatFailure(existing, classification) {
				break
			}
			if !existing.UpdatedAt.IsZero() && now.Sub(existing.UpdatedAt) < retryAfter {
				return false, nil
			}
		}
	}

	createdAt := existing.CreatedAt
	if createdAt.IsZero() {
		createdAt = classification.CreatedAt
	}
	displayCategory, displaySummary := refreshingClassificationDisplayPayload(existing)

	_, err = s.db.ExecContext(ctx, `
		UPDATE session_classifications
		SET source = ?,
			raw_session_id = ?,
			project_path = ?,
			session_file = ?,
			session_format = ?,
			snapshot_hash = ?,
			status = ?,
			stage = ?,
			category = ?,
			summary = ?,
			confidence = 0,
			model = ?,
			classifier_version = ?,
			last_error = '',
			source_updated_at = ?,
			created_at = ?,
			stage_started_at = ?,
			updated_at = ?,
			completed_at = NULL
		WHERE session_id = ?
	`, string(classification.Source), classification.RawSessionID, classification.ProjectPath, classification.SessionFile, classification.SessionFormat, classification.SnapshotHash,
		string(model.ClassificationPending), string(classification.Stage), string(displayCategory), displaySummary, classification.Model, classification.ClassifierVersion,
		classification.SourceUpdatedAt.Unix(), createdAt.Unix(), nullableTimeUnixValue(classification.StageStartedAt), now.Unix(), classification.SessionID)
	return err == nil, err
}

func refreshingClassificationDisplayPayload(existing model.SessionClassification) (model.SessionCategory, string) {
	summary := strings.TrimSpace(existing.Summary)
	if summary == "" {
		return "", ""
	}
	// Keep the last visible assessment on screen while a newer snapshot is
	// queued or running; status/stage still identify it as a refresh.
	switch existing.Status {
	case model.ClassificationCompleted, model.ClassificationPending, model.ClassificationRunning, model.ClassificationFailed:
		return existing.Category, summary
	default:
		return "", ""
	}
}

func staleUnsupportedSessionFormatFailure(existing, next model.SessionClassification) bool {
	format := strings.TrimSpace(next.SessionFormat)
	if format != "lcagent_jsonl" {
		return false
	}
	return strings.TrimSpace(existing.LastError) == "unsupported session format: "+format
}

func (s *Store) RecordSessionClassificationFailure(ctx context.Context, classification *model.SessionClassification, lastError string, retryAfter time.Duration) (bool, error) {
	if classification == nil {
		return false, errors.New("session classification requires session_id")
	}
	normalized := model.NormalizeSessionClassificationIdentity(*classification)
	*classification = normalized
	if classification.SessionID == "" {
		return false, errors.New("session classification requires session_id")
	}
	if classification.ProjectPath == "" {
		return false, errors.New("session classification requires project_path")
	}
	if classification.SnapshotHash == "" {
		return false, errors.New("session classification requires snapshot_hash")
	}
	lastError = strings.TrimSpace(lastError)
	if lastError == "" {
		lastError = "session classification failed"
	}

	now := time.Now()
	if classification.CreatedAt.IsZero() {
		classification.CreatedAt = now
	}
	if classification.SourceUpdatedAt.IsZero() {
		classification.SourceUpdatedAt = now
	}

	existing, err := s.GetSessionClassification(ctx, classification.SessionID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if errors.Is(err, sql.ErrNoRows) {
		_, err = s.db.ExecContext(ctx, `
			INSERT INTO session_classifications(
				session_id, source, raw_session_id, project_path, session_file, session_format, snapshot_hash,
				status, stage, category, summary, confidence, model, classifier_version,
				last_error, source_updated_at, created_at, stage_started_at, updated_at, completed_at
			)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, '', '', '', 0, ?, ?, ?, ?, ?, NULL, ?, NULL)
		`, classification.SessionID, string(classification.Source), classification.RawSessionID, classification.ProjectPath, classification.SessionFile, classification.SessionFormat,
			classification.SnapshotHash, string(model.ClassificationFailed), classification.Model, classification.ClassifierVersion, lastError,
			classification.SourceUpdatedAt.Unix(), classification.CreatedAt.Unix(), now.Unix())
		if err != nil {
			return false, err
		}
		applyRecordedSessionClassificationFailure(classification, lastError, now)
		return true, nil
	}

	sameSnapshot := existing.SnapshotHash == classification.SnapshotHash &&
		sameClassificationModel(existing.Model, classification.Model) &&
		existing.ClassifierVersion == classification.ClassifierVersion

	if sameSnapshot && existing.Status == model.ClassificationCompleted && strings.TrimSpace(existing.Summary) != "" {
		return false, nil
	}
	if sameSnapshot && existing.Status == model.ClassificationFailed && retryAfter > 0 &&
		strings.TrimSpace(existing.LastError) == lastError &&
		!existing.UpdatedAt.IsZero() && now.Sub(existing.UpdatedAt) < retryAfter {
		*classification = existing
		return false, nil
	}

	createdAt := existing.CreatedAt
	if createdAt.IsZero() {
		createdAt = classification.CreatedAt
	}
	displayCategory, displaySummary := refreshingClassificationDisplayPayload(existing)

	_, err = s.db.ExecContext(ctx, `
		UPDATE session_classifications
		SET source = ?,
			raw_session_id = ?,
			project_path = ?,
			session_file = ?,
			session_format = ?,
			snapshot_hash = ?,
			status = ?,
			stage = '',
			category = ?,
			summary = ?,
			confidence = 0,
			model = ?,
			classifier_version = ?,
			last_error = ?,
			source_updated_at = ?,
			created_at = ?,
			stage_started_at = NULL,
			updated_at = ?,
			completed_at = NULL
		WHERE session_id = ?
	`, string(classification.Source), classification.RawSessionID, classification.ProjectPath, classification.SessionFile, classification.SessionFormat, classification.SnapshotHash,
		string(model.ClassificationFailed), string(displayCategory), displaySummary, classification.Model, classification.ClassifierVersion, lastError,
		classification.SourceUpdatedAt.Unix(), createdAt.Unix(), now.Unix(), classification.SessionID)
	if err != nil {
		return false, err
	}
	classification.CreatedAt = createdAt
	applyRecordedSessionClassificationFailure(classification, lastError, now)
	classification.Category = displayCategory
	classification.Summary = displaySummary
	return true, nil
}

func applyRecordedSessionClassificationFailure(classification *model.SessionClassification, lastError string, updatedAt time.Time) {
	classification.Status = model.ClassificationFailed
	classification.Stage = ""
	classification.Category = ""
	classification.Summary = ""
	classification.Confidence = 0
	classification.LastError = lastError
	classification.StageStartedAt = time.Time{}
	classification.UpdatedAt = updatedAt
	classification.CompletedAt = time.Time{}
}

func (s *Store) ClaimNextPendingSessionClassification(ctx context.Context, staleAfter time.Duration) (model.SessionClassification, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return model.SessionClassification{}, err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	now := time.Now()
	if staleAfter > 0 {
		_, err = tx.ExecContext(ctx, `
			UPDATE session_classifications
			SET status = ?, stage = ?, stage_started_at = ?, updated_at = ?
			WHERE status = ? AND updated_at < ?
		`, string(model.ClassificationPending), string(model.ClassificationStageQueued), now.Unix(), now.Unix(), string(model.ClassificationRunning), now.Add(-staleAfter).Unix())
		if err != nil {
			return model.SessionClassification{}, err
		}
	}

	query := fmt.Sprintf(`
		SELECT
			sc.session_id, sc.source, sc.raw_session_id, sc.project_path, sc.session_file, sc.session_format, sc.snapshot_hash,
			sc.status, sc.stage, sc.category, sc.summary, sc.confidence, sc.model, sc.classifier_version,
			sc.last_error, sc.source_updated_at, sc.created_at, sc.stage_started_at, sc.updated_at, sc.completed_at
		FROM session_classifications sc
		JOIN projects p ON p.path = sc.project_path
		WHERE sc.status = ? AND p.in_scope = 1 AND p.archived = 0
		  AND NOT EXISTS (
			SELECT 1
			FROM session_classifications sc_pref
			WHERE %s = %s
			  AND %s > %s
		  )
		ORDER BY p.attention_score DESC, p.last_activity DESC, sc.updated_at ASC
		LIMIT 1
	`, sessionClassificationLogicalIDExpr("sc_pref"), sessionClassificationLogicalIDExpr("sc"), sessionClassificationCanonicalRankExpr("sc_pref"), sessionClassificationCanonicalRankExpr("sc"))

	row := tx.QueryRowContext(ctx, query, string(model.ClassificationPending))

	classification, err := scanSessionClassificationRow(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.SessionClassification{}, err
		}
		return model.SessionClassification{}, err
	}

	result, err := tx.ExecContext(ctx, `
		UPDATE session_classifications
		SET status = ?, stage = ?, stage_started_at = ?, updated_at = ?, last_error = ''
		WHERE session_id = ? AND status = ?
	`, string(model.ClassificationRunning), string(model.ClassificationStagePreparingSnapshot), now.Unix(), now.Unix(), classification.SessionID, string(model.ClassificationPending))
	if err != nil {
		return model.SessionClassification{}, err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return model.SessionClassification{}, err
	}
	if rowsAffected != 1 {
		return model.SessionClassification{}, sql.ErrNoRows
	}
	if err := tx.Commit(); err != nil {
		return model.SessionClassification{}, err
	}

	classification.Status = model.ClassificationRunning
	classification.Stage = model.ClassificationStagePreparingSnapshot
	classification.StageStartedAt = now
	classification.UpdatedAt = now
	classification.LastError = ""
	return classification, nil
}

func (s *Store) CompleteSessionClassification(ctx context.Context, classification model.SessionClassification) error {
	if classification.SessionID == "" {
		return errors.New("session classification requires session_id")
	}
	now := time.Now()
	completedAt := classification.CompletedAt
	if completedAt.IsZero() {
		completedAt = now
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE session_classifications
		SET snapshot_hash = ?,
			source_updated_at = ?,
			status = ?,
			stage = '',
			category = ?,
			summary = ?,
			confidence = ?,
			model = ?,
			last_error = '',
			stage_started_at = NULL,
			updated_at = ?,
			completed_at = ?
		WHERE session_id = ?
	`, classification.SnapshotHash, classification.SourceUpdatedAt.Unix(), string(model.ClassificationCompleted), string(classification.Category), classification.Summary,
		classification.Confidence, classification.Model, now.Unix(), completedAt.Unix(), classification.SessionID)
	return err
}

func (s *Store) AdvanceSessionClassificationStage(ctx context.Context, classification *model.SessionClassification, stage model.SessionClassificationStage) (bool, error) {
	if classification == nil || classification.SessionID == "" {
		return false, errors.New("session classification requires session_id")
	}
	now := time.Now()
	result, err := s.db.ExecContext(ctx, `
		UPDATE session_classifications
		SET stage = ?, stage_started_at = ?, updated_at = ?, last_error = ''
		WHERE session_id = ?
		  AND status = ?
		  AND stage = ?
		  AND COALESCE(stage_started_at, 0) = ?
	`, string(stage), now.Unix(), now.Unix(), classification.SessionID, string(model.ClassificationRunning), string(classification.Stage), timeUnixOrZero(classification.StageStartedAt))
	if err != nil {
		return false, err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if rowsAffected != 1 {
		return false, nil
	}
	classification.Stage = stage
	classification.StageStartedAt = now
	classification.UpdatedAt = now
	classification.LastError = ""
	return true, nil
}

func (s *Store) TouchSessionClassification(ctx context.Context, classification model.SessionClassification) (bool, error) {
	if classification.SessionID == "" {
		return false, errors.New("session classification requires session_id")
	}
	now := time.Now()
	result, err := s.db.ExecContext(ctx, `
		UPDATE session_classifications
		SET updated_at = ?
		WHERE session_id = ?
		  AND status = ?
		  AND stage = ?
		  AND COALESCE(stage_started_at, 0) = ?
	`, now.Unix(), classification.SessionID, string(model.ClassificationRunning), string(classification.Stage), timeUnixOrZero(classification.StageStartedAt))
	if err != nil {
		return false, err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if rowsAffected != 1 {
		return false, nil
	}
	return true, nil
}

func (s *Store) CompleteSessionClassificationAttempt(ctx context.Context, classification *model.SessionClassification) (bool, error) {
	if classification == nil || classification.SessionID == "" {
		return false, errors.New("session classification requires session_id")
	}
	now := time.Now()
	completedAt := classification.CompletedAt
	if completedAt.IsZero() {
		completedAt = now
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE session_classifications
		SET snapshot_hash = ?,
			source_updated_at = ?,
			status = ?,
			stage = '',
			category = ?,
			summary = ?,
			confidence = ?,
			model = ?,
			last_error = '',
			stage_started_at = NULL,
			updated_at = ?,
			completed_at = ?
		WHERE session_id = ?
		  AND status = ?
		  AND stage = ?
		  AND COALESCE(stage_started_at, 0) = ?
	`, classification.SnapshotHash, classification.SourceUpdatedAt.Unix(), string(model.ClassificationCompleted), string(classification.Category), classification.Summary,
		classification.Confidence, classification.Model, now.Unix(), completedAt.Unix(), classification.SessionID, string(model.ClassificationRunning), string(classification.Stage), timeUnixOrZero(classification.StageStartedAt))
	if err != nil {
		return false, err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if rowsAffected != 1 {
		return false, nil
	}
	classification.Status = model.ClassificationCompleted
	classification.Stage = ""
	classification.StageStartedAt = time.Time{}
	classification.LastError = ""
	classification.UpdatedAt = now
	classification.CompletedAt = completedAt
	return true, nil
}

func (s *Store) UpdateSessionEvidenceMetadata(ctx context.Context, session model.SessionEvidence) error {
	session = model.NormalizeSessionEvidenceIdentity(session)
	if session.SessionID == "" {
		return errors.New("session evidence requires session_id")
	}
	var latestTurnStartedAt any
	if !session.LatestTurnStartedAt.IsZero() {
		latestTurnStartedAt = session.LatestTurnStartedAt.Unix()
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE project_sessions
		SET snapshot_hash = ?,
			latest_turn_started_at = ?,
			latest_turn_state_known = ?,
			latest_turn_completed = ?
		WHERE session_id = ?
	`, session.SnapshotHash, latestTurnStartedAt, boolToInt(session.LatestTurnStateKnown), boolToInt(session.LatestTurnCompleted), session.SessionID)
	return err
}

func (s *Store) UpdateSessionClassificationStage(ctx context.Context, sessionID string, stage model.SessionClassificationStage) error {
	resolvedSessionID, err := s.resolveSessionClassificationID(ctx, sessionID)
	if err != nil {
		return err
	}
	now := time.Now()
	_, err = s.db.ExecContext(ctx, `
		UPDATE session_classifications
		SET stage = ?, stage_started_at = ?, updated_at = ?
		WHERE session_id = ?
	`, string(stage), now.Unix(), now.Unix(), resolvedSessionID)
	return err
}

func (s *Store) FailSessionClassification(ctx context.Context, sessionID, lastError string) error {
	resolvedSessionID, err := s.resolveSessionClassificationID(ctx, sessionID)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		UPDATE session_classifications
		SET status = ?,
			stage = '',
			confidence = 0,
			last_error = ?,
			stage_started_at = NULL,
			updated_at = ?,
			completed_at = NULL
		WHERE session_id = ?
	`, string(model.ClassificationFailed), lastError, time.Now().Unix(), resolvedSessionID)
	return err
}

func (s *Store) resolveSessionClassificationID(ctx context.Context, sessionID string) (string, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return "", errors.New("session classification requires session_id")
	}
	matches, err := s.loadSessionClassificationLookupMatches(ctx, sessionID)
	if err != nil {
		return "", err
	}
	switch len(matches) {
	case 0:
		return "", sql.ErrNoRows
	}

	best := matches[0]
	for _, candidate := range matches[1:] {
		if preferSessionClassificationLookupMatch(candidate, best, sessionID) {
			best = candidate
		}
	}
	return best.sessionID, nil
}

func (s *Store) FailSessionClassificationAttempt(ctx context.Context, classification *model.SessionClassification, lastError string) (bool, error) {
	if classification == nil || classification.SessionID == "" {
		return false, errors.New("session classification requires session_id")
	}
	now := time.Now()
	result, err := s.db.ExecContext(ctx, `
		UPDATE session_classifications
		SET status = ?,
			stage = '',
			confidence = 0,
			last_error = ?,
			stage_started_at = NULL,
			updated_at = ?,
			completed_at = NULL
		WHERE session_id = ?
		  AND status = ?
		  AND stage = ?
		  AND COALESCE(stage_started_at, 0) = ?
	`, string(model.ClassificationFailed), lastError, now.Unix(), classification.SessionID, string(model.ClassificationRunning), string(classification.Stage), timeUnixOrZero(classification.StageStartedAt))
	if err != nil {
		return false, err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if rowsAffected != 1 {
		return false, nil
	}
	classification.Status = model.ClassificationFailed
	classification.Stage = ""
	classification.Confidence = 0
	classification.StageStartedAt = time.Time{}
	classification.LastError = lastError
	classification.UpdatedAt = now
	classification.CompletedAt = time.Time{}
	return true, nil
}

func (s *Store) GetSessionClassification(ctx context.Context, sessionID string) (model.SessionClassification, error) {
	resolvedSessionID, err := s.resolveSessionClassificationID(ctx, sessionID)
	if err != nil {
		return model.SessionClassification{}, err
	}
	row := s.db.QueryRowContext(ctx, `
		SELECT
			session_id, source, raw_session_id, project_path, session_file, session_format, snapshot_hash,
			status, stage, category, summary, confidence, model, classifier_version,
			last_error, source_updated_at, created_at, stage_started_at, updated_at, completed_at
		FROM session_classifications
		WHERE session_id = ?
	`, resolvedSessionID)
	return scanSessionClassificationRow(row)
}

func (s *Store) ListSessionClassifications(ctx context.Context, projectPath, sessionID string) ([]model.SessionClassification, error) {
	projectPath = strings.TrimSpace(projectPath)
	sessionID = strings.TrimSpace(sessionID)

	where := []string{"1 = 1"}
	args := []any{}
	if projectPath != "" {
		where = append(where, "project_path = ?")
		args = append(args, projectPath)
	}
	if sessionID != "" {
		where = append(where, "(session_id = ? OR raw_session_id = ?)")
		args = append(args, sessionID, sessionID)
	}

	query := `
		SELECT
			session_id, source, raw_session_id, project_path, session_file, session_format, snapshot_hash,
			status, stage, category, summary, confidence, model, classifier_version,
			last_error, source_updated_at, created_at, stage_started_at, updated_at, completed_at
		FROM session_classifications
		WHERE ` + strings.Join(where, " AND ") + `
		ORDER BY project_path ASC, session_id ASC
	`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []model.SessionClassification{}
	for rows.Next() {
		classification, err := scanSessionClassificationRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, classification)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) UpdateSessionClassificationSummary(ctx context.Context, sessionID, summary string) (bool, error) {
	resolvedSessionID, err := s.resolveSessionClassificationID(ctx, sessionID)
	if err != nil {
		return false, err
	}
	summary = strings.TrimSpace(summary)

	result, err := s.db.ExecContext(ctx, `
		UPDATE session_classifications
		SET summary = ?, updated_at = ?
		WHERE session_id = ? AND summary != ?
	`, summary, time.Now().Unix(), resolvedSessionID, summary)
	if err != nil {
		return false, err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return rowsAffected > 0, nil
}

func (s *Store) GetProjectDetail(ctx context.Context, path string, eventLimit int) (model.ProjectDetail, error) {
	if eventLimit <= 0 {
		eventLimit = 20
	}

	query := fmt.Sprintf(`
		SELECT
			p.path, p.name, p.kind,
			COALESCE(pc.id, ''), COALESCE(pc.name, ''),
			p.last_activity, p.status, p.attention_score, p.present_on_disk, p.worktree_root_path, p.worktree_kind, p.worktree_parent_branch, p.worktree_merge_status, p.worktree_origin_todo_id, p.repo_branch, p.repo_dirty, p.repo_conflict, p.repo_sync_status, p.repo_ahead_count, p.repo_behind_count, p.forgotten, p.manually_added, p.in_scope, p.archived, p.pinned, p.snoozed_until, p.last_session_seen_at, p.created_at,
			COALESCE((SELECT COUNT(*) FROM project_todos pt WHERE pt.project_path = p.path AND pt.done = 0), 0),
			COALESCE((SELECT COUNT(*) FROM project_todos pt WHERE pt.project_path = p.path), 0),
			p.run_command,
			p.moved_from_path, p.moved_at,
			COALESCE(p.preferred_session_source, ''),
			COALESCE(ps.session_id, ''),
			COALESCE(ps.source, ''),
			COALESCE(ps.raw_session_id, ''),
			COALESCE(ps.format, ''),
			COALESCE(ps.detected_project_path, ''),
			COALESCE(ps.snapshot_hash, ''),
			ps.last_event_at,
			ps.latest_turn_started_at,
			COALESCE(ps.latest_turn_state_known, 0),
			COALESCE(ps.latest_turn_completed, 0),
			COALESCE(sc.status, ''),
			COALESCE(sc.stage, ''),
			COALESCE(sc.category, ''),
			COALESCE(sc.summary, ''),
			sc.stage_started_at,
			sc.updated_at,
			COALESCE(sc_completed.category, ''),
			COALESCE(sc_completed.summary, ''),
			sc_completed.updated_at
		FROM projects p
		LEFT JOIN category_assignments ca ON ca.resource_kind = 'project' AND ca.resource_id = p.path
		LEFT JOIN project_categories pc ON pc.id = ca.category_id
		LEFT JOIN project_sessions ps ON ps.session_id = (
			SELECT ps2.session_id
			FROM project_sessions ps2
			WHERE ps2.project_path = p.path
			ORDER BY ps2.last_event_at DESC
			LIMIT 1
		)
		LEFT JOIN session_classifications sc ON sc.session_id = %s
		LEFT JOIN session_classifications sc_completed ON sc_completed.session_id = (
			SELECT sc2.session_id
			FROM session_classifications sc2
			WHERE sc2.project_path = p.path AND sc2.status = 'completed'
			ORDER BY %s DESC, COALESCE(sc2.completed_at, sc2.updated_at) DESC, sc2.updated_at DESC
			LIMIT 1
		)
		WHERE p.path = ?
	`, preferredSessionClassificationIDExpr("ps.session_id"), sessionClassificationCanonicalRankExpr("sc2"))
	row := s.db.QueryRowContext(ctx, query, path)
	summary, err := scanSummaryRow(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.ProjectDetail{}, fmt.Errorf("project not found: %s", path)
		}
		return model.ProjectDetail{}, err
	}

	reasons, err := s.listReasons(ctx, path)
	if err != nil {
		return model.ProjectDetail{}, err
	}
	todos, err := s.listTodos(ctx, path)
	if err != nil {
		return model.ProjectDetail{}, err
	}
	sessions, err := s.listSessions(ctx, path)
	if err != nil {
		return model.ProjectDetail{}, err
	}
	artifacts, err := s.listArtifacts(ctx, path)
	if err != nil {
		return model.ProjectDetail{}, err
	}
	events, err := s.listEvents(ctx, path, eventLimit)
	if err != nil {
		return model.ProjectDetail{}, err
	}

	var latestClassification *model.SessionClassification
	if len(sessions) > 0 {
		classification, err := s.GetSessionClassification(ctx, sessions[0].SessionID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return model.ProjectDetail{}, err
		}
		if err == nil {
			latestClassification = &classification
		}
	}

	return model.ProjectDetail{
		Summary:                     summary,
		Reasons:                     reasons,
		Todos:                       todos,
		Sessions:                    sessions,
		Artifacts:                   artifacts,
		RecentEvents:                events,
		LatestSessionClassification: latestClassification,
	}, nil
}

func (s *Store) listTodos(ctx context.Context, path string) ([]model.TodoItem, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			pt.id, pt.project_path, pt.text, pt.done, pt.position, pt.created_at, pt.updated_at, pt.completed_at,
			pt.work_provider, pt.work_project_path, pt.work_session_id, pt.work_claimed_at, pt.work_state, pt.work_state_at,
			tws.todo_id, tws.status, tws.todo_text_hash, tws.branch_name, tws.worktree_suffix, tws.kind,
			tws.reason, tws.confidence, tws.model, tws.last_error, tws.updated_at
		FROM project_todos pt
		LEFT JOIN todo_worktree_suggestions tws ON tws.todo_id = pt.id
		WHERE project_path = ?
		ORDER BY done ASC, position ASC, id ASC
	`, path)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []model.TodoItem{}
	for rows.Next() {
		var (
			item         model.TodoItem
			done         int
			createdAt    int64
			updatedAt    int64
			completedAt  sql.NullInt64
			workProvider sql.NullString
			workProject  sql.NullString
			workSession  sql.NullString
			workClaimed  sql.NullInt64
			workState    sql.NullString
			workStateAt  sql.NullInt64
			suggestion   sql.NullInt64
			status       sql.NullString
			textHash     sql.NullString
			branchName   sql.NullString
			suffix       sql.NullString
			kind         sql.NullString
			reason       sql.NullString
			confidence   sql.NullFloat64
			modelName    sql.NullString
			lastError    sql.NullString
			suggestedAt  sql.NullInt64
		)
		if err := rows.Scan(
			&item.ID, &item.ProjectPath, &item.Text, &done, &item.Position, &createdAt, &updatedAt, &completedAt,
			&workProvider, &workProject, &workSession, &workClaimed, &workState, &workStateAt,
			&suggestion, &status, &textHash, &branchName, &suffix, &kind, &reason, &confidence, &modelName, &lastError, &suggestedAt,
		); err != nil {
			return nil, err
		}
		item.Done = done != 0
		item.CreatedAt = time.Unix(createdAt, 0)
		item.UpdatedAt = time.Unix(updatedAt, 0)
		if completedAt.Valid {
			item.CompletedAt = time.Unix(completedAt.Int64, 0)
		}
		item.WorkProvider = model.NormalizeSessionSource(model.SessionSource(strings.TrimSpace(workProvider.String)))
		item.WorkProjectPath = strings.TrimSpace(workProject.String)
		item.WorkSessionID = strings.TrimSpace(workSession.String)
		item.WorkState = model.NormalizeTodoWorkState(model.TodoWorkState(strings.TrimSpace(workState.String)))
		if workClaimed.Valid {
			item.WorkClaimedAt = time.Unix(workClaimed.Int64, 0)
		}
		if workStateAt.Valid {
			item.WorkStateAt = time.Unix(workStateAt.Int64, 0)
		}
		if suggestion.Valid {
			worktreeSuggestion := &model.TodoWorktreeSuggestion{
				TodoID:         item.ID,
				ProjectPath:    item.ProjectPath,
				TodoText:       item.Text,
				Status:         model.TodoWorktreeSuggestionStatus(strings.TrimSpace(status.String)),
				TodoTextHash:   strings.TrimSpace(textHash.String),
				BranchName:     strings.TrimSpace(branchName.String),
				WorktreeSuffix: strings.TrimSpace(suffix.String),
				Kind:           strings.TrimSpace(kind.String),
				Reason:         strings.TrimSpace(reason.String),
				Model:          strings.TrimSpace(modelName.String),
				LastError:      strings.TrimSpace(lastError.String),
			}
			if confidence.Valid {
				worktreeSuggestion.Confidence = confidence.Float64
			}
			if suggestedAt.Valid {
				worktreeSuggestion.UpdatedAt = time.Unix(suggestedAt.Int64, 0)
			}
			item.WorktreeSuggestion = worktreeSuggestion
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return out, nil
	}
	attachments, err := s.listTodoAttachmentsForIDs(ctx, todoIDs(out))
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Attachments = attachments[out[i].ID]
	}
	return out, nil
}

func hashTodoText(text string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(text)))
	return hex.EncodeToString(sum[:])
}

func (s *Store) listReasons(ctx context.Context, path string) ([]model.AttentionReason, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT code, text, weight
		FROM project_reasons
		WHERE project_path = ?
		ORDER BY position ASC
	`, path)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []model.AttentionReason{}
	for rows.Next() {
		var r model.AttentionReason
		if err := rows.Scan(&r.Code, &r.Text, &r.Weight); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) listSessions(ctx context.Context, path string) ([]model.SessionEvidence, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT session_id, source, raw_session_id, project_path, detected_project_path, session_file, format, snapshot_hash, started_at, last_event_at, error_count, latest_turn_started_at, latest_turn_state_known, latest_turn_completed
		FROM project_sessions
		WHERE project_path = ?
		ORDER BY last_event_at DESC
		LIMIT 30
	`, path)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []model.SessionEvidence{}
	for rows.Next() {
		var (
			s                   model.SessionEvidence
			source              string
			startedAt           sql.NullInt64
			lastEventAt         int64
			latestTurnStartedAt sql.NullInt64
			turnKnown           int
			turnDone            int
		)
		if err := rows.Scan(&s.SessionID, &source, &s.RawSessionID, &s.ProjectPath, &s.DetectedProjectPath, &s.SessionFile, &s.Format, &s.SnapshotHash, &startedAt, &lastEventAt, &s.ErrorCount, &latestTurnStartedAt, &turnKnown, &turnDone); err != nil {
			return nil, err
		}
		s.Source = model.NormalizeSessionSource(model.SessionSource(source))
		if startedAt.Valid {
			s.StartedAt = time.Unix(startedAt.Int64, 0)
		}
		s.LastEventAt = time.Unix(lastEventAt, 0)
		if latestTurnStartedAt.Valid {
			s.LatestTurnStartedAt = time.Unix(latestTurnStartedAt.Int64, 0)
		}
		s.LatestTurnStateKnown = turnKnown != 0
		s.LatestTurnCompleted = turnDone != 0
		s = model.NormalizeSessionEvidenceIdentity(s)
		out = append(out, s)
	}
	return out, rows.Err()
}

func (s *Store) listArtifacts(ctx context.Context, path string) ([]model.ArtifactEvidence, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT path, kind, updated_at, note
		FROM project_artifacts
		WHERE project_path = ?
		ORDER BY updated_at DESC
		LIMIT 50
	`, path)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []model.ArtifactEvidence{}
	for rows.Next() {
		var a model.ArtifactEvidence
		var updatedAt int64
		if err := rows.Scan(&a.Path, &a.Kind, &updatedAt, &a.Note); err != nil {
			return nil, err
		}
		a.UpdatedAt = time.Unix(updatedAt, 0)
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) listEvents(ctx context.Context, path string, limit int) ([]model.StoredEvent, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, ts, project_path, event_type, payload
		FROM events
		WHERE project_path = ?
		ORDER BY id DESC
		LIMIT ?
	`, path, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []model.StoredEvent{}
	for rows.Next() {
		var e model.StoredEvent
		var ts int64
		if err := rows.Scan(&e.ID, &ts, &e.ProjectPath, &e.Type, &e.Payload); err != nil {
			return nil, err
		}
		e.At = time.Unix(ts, 0)
		out = append(out, e)
	}
	return out, rows.Err()
}

func scanSessionClassificationRow(scanner interface {
	Scan(dest ...any) error
}) (model.SessionClassification, error) {
	var (
		classification                        model.SessionClassification
		source, rawSessionID                  string
		status, stage, category               string
		sourceUpdatedAt, createdAt, updatedAt int64
		stageStartedAt, completedAt           sql.NullInt64
	)
	if err := scanner.Scan(
		&classification.SessionID,
		&source,
		&rawSessionID,
		&classification.ProjectPath,
		&classification.SessionFile,
		&classification.SessionFormat,
		&classification.SnapshotHash,
		&status,
		&stage,
		&category,
		&classification.Summary,
		&classification.Confidence,
		&classification.Model,
		&classification.ClassifierVersion,
		&classification.LastError,
		&sourceUpdatedAt,
		&createdAt,
		&stageStartedAt,
		&updatedAt,
		&completedAt,
	); err != nil {
		return model.SessionClassification{}, err
	}
	classification.Status = model.SessionClassificationStatus(status)
	classification.Stage = model.SessionClassificationStage(stage)
	classification.Category = model.SessionCategory(category)
	classification.Source = model.NormalizeSessionSource(model.SessionSource(source))
	classification.RawSessionID = strings.TrimSpace(rawSessionID)
	classification.SourceUpdatedAt = time.Unix(sourceUpdatedAt, 0)
	classification.CreatedAt = time.Unix(createdAt, 0)
	if stageStartedAt.Valid {
		classification.StageStartedAt = time.Unix(stageStartedAt.Int64, 0)
	}
	classification.UpdatedAt = time.Unix(updatedAt, 0)
	if completedAt.Valid {
		classification.CompletedAt = time.Unix(completedAt.Int64, 0)
	}
	return model.NormalizeSessionClassificationIdentity(classification), nil
}

func (s *Store) SetPinned(ctx context.Context, path string, pinned bool) error {
	_, err := s.db.ExecContext(ctx, `UPDATE projects SET pinned = ?, updated_at = ? WHERE path = ?`, boolToInt(pinned), time.Now().Unix(), path)
	return err
}

func (s *Store) repairTerminalSessionClassificationStages(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE session_classifications
		SET stage = '',
			stage_started_at = NULL
		WHERE status IN (?, ?)
		  AND (stage != '' OR stage_started_at IS NOT NULL)
	`, string(model.ClassificationCompleted), string(model.ClassificationFailed))
	if err != nil {
		return fmt.Errorf("repair terminal session classification stages: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `
		UPDATE session_classifications
		SET confidence = 0
		WHERE status = ?
		  AND confidence != 0
	`, string(model.ClassificationFailed))
	if err != nil {
		return fmt.Errorf("repair failed session classification confidence: %w", err)
	}
	return nil
}

func sameClassificationModel(existing, requested string) bool {
	return normalizeClassificationModel(existing) == normalizeClassificationModel(requested)
}

func normalizeClassificationModel(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	parts := strings.Split(name, "-")
	if len(parts) < 4 {
		return name
	}
	last := parts[len(parts)-3:]
	if !isExactDigits(last[0], 4) || !isExactDigits(last[1], 2) || !isExactDigits(last[2], 2) {
		return name
	}
	return strings.Join(parts[:len(parts)-3], "-")
}

func isExactDigits(s string, length int) bool {
	if len(s) != length {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
