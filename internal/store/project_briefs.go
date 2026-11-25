package store

import (
	"context"
	"encoding/json"
	"time"

	"lcroom/internal/model"
)

type ProjectBriefRow struct {
	CheckoutPath string
	RecordedAt   time.Time
	Payload      json.RawMessage
}

// UpsertProjectBrief compares host timestamps so a slow older report cannot
// overwrite a newer one from the same checkout.
func (s *Store) UpsertProjectBrief(ctx context.Context, row ProjectBriefRow) (bool, error) {
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO project_briefs(checkout_path, recorded_at, payload)
		VALUES (?, ?, ?)
		ON CONFLICT(checkout_path) DO UPDATE SET recorded_at = excluded.recorded_at, payload = excluded.payload
		WHERE excluded.recorded_at > project_briefs.recorded_at`,
		row.CheckoutPath, row.RecordedAt.UnixNano(), string(row.Payload))
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count != 0, err
}

func (s *Store) ListProjectBriefs(ctx context.Context, checkoutPaths []string) ([]ProjectBriefRow, error) {
	pathsJSON, err := json.Marshal(checkoutPaths)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT checkout_path, recorded_at, payload FROM project_briefs
		WHERE checkout_path IN (SELECT value FROM json_each(?))
		ORDER BY recorded_at DESC, checkout_path ASC`, string(pathsJSON))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var briefs []ProjectBriefRow
	for rows.Next() {
		var brief ProjectBriefRow
		var timestamp int64
		var payload string
		if err := rows.Scan(&brief.CheckoutPath, &timestamp, &payload); err != nil {
			return nil, err
		}
		brief.RecordedAt = time.Unix(0, timestamp).UTC()
		brief.Payload = json.RawMessage(payload)
		briefs = append(briefs, brief)
	}
	return briefs, rows.Err()
}

func (s *Store) HasNewerProjectBriefSession(ctx context.Context, checkoutPath string, after time.Time, provider model.SessionSource, sessionID string) (bool, error) {
	var found bool
	err := s.db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM project_sessions
			WHERE project_path = ?
				AND last_event_at > ?
				AND (source != ? OR COALESCE(NULLIF(raw_session_id, ''), session_id) != ?)
		)`, checkoutPath, after.Unix(), provider, sessionID).Scan(&found)
	return found, err
}
