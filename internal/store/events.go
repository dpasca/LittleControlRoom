package store

import (
	"context"
	"fmt"
	"time"

	"lcroom/internal/model"
)

func (s *Store) LatestEventIDByType(ctx context.Context, eventType string) (int64, error) {
	var id int64
	if err := s.db.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(id), 0)
		FROM events
		WHERE event_type = ?
	`, eventType).Scan(&id); err != nil {
		return 0, err
	}
	return id, nil
}

func (s *Store) ListEventsAfterIDByType(ctx context.Context, eventType string, afterID int64, limit int) ([]model.StoredEvent, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, ts, project_path, event_type, payload
		FROM events
		WHERE event_type = ? AND id > ?
		ORDER BY id ASC
		LIMIT ?
	`, eventType, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := make([]model.StoredEvent, 0)
	for rows.Next() {
		var event model.StoredEvent
		var at int64
		if err := rows.Scan(&event.ID, &at, &event.ProjectPath, &event.Type, &event.Payload); err != nil {
			return nil, err
		}
		event.At = time.Unix(at, 0)
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list events after id: %w", err)
	}
	return events, nil
}

func (s *Store) ListRecentEventsByTypeForProject(ctx context.Context, eventType, projectPath string, limit int) ([]model.StoredEvent, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, ts, project_path, event_type, payload
		FROM events
		WHERE event_type = ? AND project_path = ?
		ORDER BY id DESC
		LIMIT ?
	`, eventType, projectPath, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := make([]model.StoredEvent, 0)
	for rows.Next() {
		var event model.StoredEvent
		var at int64
		if err := rows.Scan(&event.ID, &at, &event.ProjectPath, &event.Type, &event.Payload); err != nil {
			return nil, err
		}
		event.At = time.Unix(at, 0)
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list recent project events by type: %w", err)
	}
	return events, nil
}
