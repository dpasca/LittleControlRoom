package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"time"

	"lcroom/internal/model"
)

// RecordAgentTaskActivity stores observed provider progress separately from workflow bookkeeping.
func (s *Store) RecordAgentTaskActivity(ctx context.Context, taskID, sessionID string, at time.Time, exited bool) error {
	stamp := int64(0)
	if !at.IsZero() {
		stamp = at.UnixNano()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO agent_task_activity(task_id, session_id, last_activity_at, exited, observed_at)
 VALUES (?, ?, ?, ?, ?) ON CONFLICT(task_id) DO UPDATE SET
 last_activity_at = CASE WHEN session_id = excluded.session_id THEN MAX(last_activity_at, excluded.last_activity_at) ELSE excluded.last_activity_at END,
 session_id = excluded.session_id, exited = excluded.exited, observed_at = excluded.observed_at`, taskID, sessionID, stamp, exited, time.Now().UnixNano())
	return err
}

// AgentTaskActivity uses exact-session evidence, never the newest session in a workspace.
// Disk reads happen only in query execution, outside the TUI update/render path.
func (s *Store) AgentTaskActivity(ctx context.Context, task model.AgentTask, now time.Time) (map[string]any, error) {
	var stamp, observed int64
	var exited bool
	source := "unknown"
	err := s.db.QueryRowContext(ctx, `SELECT last_activity_at, exited, observed_at FROM agent_task_activity WHERE task_id = ? AND session_id = ?`, task.ID, task.SessionID).Scan(&stamp, &exited, &observed)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	var at time.Time
	if stamp > 0 {
		at = time.Unix(0, stamp)
		source = "worker_session"
	}
	var file string
	var event int64
	err = s.db.QueryRowContext(ctx, `SELECT session_file, last_event_at FROM project_sessions WHERE source = ? AND (session_id = ? OR raw_session_id = ?) ORDER BY last_event_at DESC LIMIT 1`, string(task.Provider), task.SessionID, task.SessionID).Scan(&file, &event)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if task.SessionID != "" && err == nil {
		if event > 0 && time.Unix(event, 0).After(at) {
			at = time.Unix(event, 0)
			source = "session_event"
		}
		if info, err := os.Stat(file); err == nil && info.ModTime().After(at) {
			at = info.ModTime()
			source = "provider_log_write"
		}
	}
	state, hint := "unknown", "No worker activity observed"
	var age any
	if !at.IsZero() {
		seconds := max(int64(0), int64(now.Sub(at).Seconds()))
		age = seconds
		state, hint = "active", "Activity within the last 2 minutes"
		if seconds >= 120 {
			state, hint = "quiet", fmt.Sprintf("Quiet for %d minutes", seconds/60)
		}
	}
	if exited {
		state, hint = "exited", "Worker turn exited (host observed)"
	}
	formatted := ""
	if !at.IsZero() {
		formatted = at.UTC().Format(time.RFC3339Nano)
	}
	observedAt := ""
	if observed > 0 {
		observedAt = time.Unix(0, observed).UTC().Format(time.RFC3339Nano)
	}
	return map[string]any{"last_activity_at": formatted, "source": source, "state": state, "hint": hint, "age_seconds": age, "host_observed_at": observedAt}, nil
}
