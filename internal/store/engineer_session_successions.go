package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"lcroom/internal/model"
)

// engineerSuccessionMaxHops bounds chain resolution. Completing a succession
// reinstates its successor first, so a cycle cannot be stored; the bound is
// defensive.
const engineerSuccessionMaxHops = 16

// EngineerSessionSuccession is an operator-declared replacement of one engineer
// conversation by another, such as a /handoff. Exact-session messages, task
// callbacks and caller authority follow the successor; the predecessor never
// receives forwarded work again. A pending succession has launched its
// successor but has not yet observed the successor's provider session ID.
type EngineerSessionSuccession struct {
	ProjectPath          string
	Provider             model.SessionSource
	SessionID            string
	SessionKey           string
	SuccessorProjectPath string
	SuccessorProvider    model.SessionSource
	SuccessorSessionKey  string
	SuccessorSessionID   string
	Reason               string
	CreatedAt            time.Time
	CompletedAt          time.Time
}

func (s EngineerSessionSuccession) Pending() bool {
	return strings.TrimSpace(s.SuccessorSessionID) == ""
}

// EngineerSessionRef names one provider conversation in one workspace.
type EngineerSessionRef struct {
	ProjectPath string
	Provider    model.SessionSource
	SessionID   string
}

const engineerSuccessionSelect = `
	SELECT project_path, provider, session_id, session_key, successor_project_path, successor_provider,
		successor_session_key, successor_session_id, reason, created_at, completed_at
	FROM engineer_session_successions
`

func normalizeEngineerSuccession(succession EngineerSessionSuccession) (EngineerSessionSuccession, error) {
	succession.ProjectPath = cleanSuccessionPath(succession.ProjectPath)
	succession.SuccessorProjectPath = cleanSuccessionPath(firstNonEmptyString(succession.SuccessorProjectPath, succession.ProjectPath))
	succession.Provider = model.NormalizeSessionSource(succession.Provider)
	succession.SuccessorProvider = model.NormalizeSessionSource(succession.SuccessorProvider)
	succession.SessionID = strings.TrimSpace(succession.SessionID)
	succession.SessionKey = strings.TrimSpace(succession.SessionKey)
	succession.SuccessorSessionKey = strings.TrimSpace(succession.SuccessorSessionKey)
	succession.SuccessorSessionID = strings.TrimSpace(succession.SuccessorSessionID)
	succession.Reason = strings.TrimSpace(succession.Reason)
	if succession.ProjectPath == "" || succession.Provider == model.SessionSourceUnknown || succession.SessionID == "" {
		return succession, errors.New("engineer session succession requires the replaced session's project, provider and session ID")
	}
	if succession.SuccessorProvider == model.SessionSourceUnknown || (succession.SuccessorSessionKey == "" && succession.SuccessorSessionID == "") {
		return succession, errors.New("engineer session succession requires the successor's provider and control key or session ID")
	}
	if succession.SuccessorProjectPath != succession.ProjectPath {
		return succession, errors.New("engineer session succession across workspaces is not supported")
	}
	if succession.SuccessorProvider == succession.Provider && succession.SuccessorSessionID == succession.SessionID {
		return succession, errors.New("an engineer session cannot succeed itself")
	}
	return succession, nil
}

func cleanSuccessionPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	return filepath.Clean(path)
}

// RecordEngineerSessionSuccession stores a replacement before its successor
// launches. Retrying the same launch is idempotent; an unstarted earlier
// handoff is replaced, while a completed one is never silently rewritten.
func (s *Store) RecordEngineerSessionSuccession(ctx context.Context, succession EngineerSessionSuccession) (EngineerSessionSuccession, error) {
	if s == nil || s.db == nil {
		return EngineerSessionSuccession{}, errors.New("store unavailable")
	}
	succession, err := normalizeEngineerSuccession(succession)
	if err != nil {
		return EngineerSessionSuccession{}, err
	}
	now := time.Now()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return EngineerSessionSuccession{}, err
	}
	defer tx.Rollback()
	existing, found, err := getEngineerSuccessionTx(ctx, tx, succession.ProjectPath, succession.Provider, succession.SessionID)
	if err != nil {
		return EngineerSessionSuccession{}, err
	}
	if found {
		sameLaunch := existing.SuccessorProvider == succession.SuccessorProvider &&
			(succession.SuccessorSessionKey != "" && existing.SuccessorSessionKey == succession.SuccessorSessionKey ||
				succession.SuccessorSessionID != "" && existing.SuccessorSessionID == succession.SuccessorSessionID)
		switch {
		case sameLaunch:
			return existing, tx.Commit()
		case !existing.Pending():
			return EngineerSessionSuccession{}, fmt.Errorf("session %s was already replaced by %s", existing.SessionID, existing.SuccessorSessionID)
		}
	}
	succession.CreatedAt = now
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO engineer_session_successions(
			project_path, provider, session_id, session_key, successor_project_path, successor_provider,
			successor_session_key, successor_session_id, reason, created_at, completed_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, '', ?, ?, 0)
		ON CONFLICT(project_path, provider, session_id) DO UPDATE SET
			session_key = excluded.session_key,
			successor_project_path = excluded.successor_project_path,
			successor_provider = excluded.successor_provider,
			successor_session_key = excluded.successor_session_key,
			successor_session_id = '',
			reason = excluded.reason,
			created_at = excluded.created_at,
			completed_at = 0
	`, succession.ProjectPath, string(succession.Provider), succession.SessionID, succession.SessionKey,
		succession.SuccessorProjectPath, string(succession.SuccessorProvider), succession.SuccessorSessionKey,
		succession.Reason, now.Unix()); err != nil {
		return EngineerSessionSuccession{}, fmt.Errorf("record engineer session succession: %w", err)
	}
	if succession.SuccessorSessionID != "" {
		succession, err = completeEngineerSuccessionTx(ctx, tx, succession, succession.SuccessorSessionKey, succession.SuccessorSessionID, now)
		if err != nil {
			return EngineerSessionSuccession{}, err
		}
	} else if _, err := tx.ExecContext(ctx, `
		UPDATE engineer_messages SET last_error = ?, updated_at = ?
		WHERE state = 'queued' AND follow_current_session = 0 AND project_path = ? AND provider = ? AND target_session_id = ?
	`, engineerSuccessionPendingStatus(succession.SessionID), now.Unix(), succession.ProjectPath, string(succession.Provider), succession.SessionID); err != nil {
		return EngineerSessionSuccession{}, fmt.Errorf("mark messages waiting for successor: %w", err)
	}
	return succession, tx.Commit()
}

// CompleteEngineerSessionSuccessions binds pending successions to the provider
// session that announced the successor's exact control key. It is called from
// the host's identity observation, never from a guess about the newest session.
func (s *Store) CompleteEngineerSessionSuccessions(ctx context.Context, projectPath string, provider model.SessionSource, key, sessionID string) ([]EngineerSessionSuccession, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("store unavailable")
	}
	projectPath, key, sessionID = cleanSuccessionPath(projectPath), strings.TrimSpace(key), strings.TrimSpace(sessionID)
	provider = model.NormalizeSessionSource(provider)
	if projectPath == "" || key == "" || sessionID == "" || provider == model.SessionSourceUnknown {
		return nil, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, engineerSuccessionSelect+`
		WHERE successor_project_path = ? AND successor_provider = ? AND successor_session_key = ? AND successor_session_id = ''
	`, projectPath, string(provider), key)
	if err != nil {
		return nil, err
	}
	pending, err := scanEngineerSuccessions(rows)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	completed := make([]EngineerSessionSuccession, 0, len(pending))
	for _, succession := range pending {
		if succession.Provider == provider && succession.SessionID == sessionID {
			continue // The predecessor itself can never be its own successor.
		}
		done, err := completeEngineerSuccessionTx(ctx, tx, succession, key, sessionID, now)
		if err != nil {
			return nil, err
		}
		completed = append(completed, done)
	}
	return completed, tx.Commit()
}

// CompleteEngineerSessionSuccession binds one pending succession when the
// launching host already knows the successor's identity.
func (s *Store) CompleteEngineerSessionSuccession(ctx context.Context, predecessor EngineerSessionRef, launchKey, successorKey, successorSessionID string) (EngineerSessionSuccession, bool, error) {
	if s == nil || s.db == nil {
		return EngineerSessionSuccession{}, false, errors.New("store unavailable")
	}
	successorSessionID = strings.TrimSpace(successorSessionID)
	if successorSessionID == "" {
		return EngineerSessionSuccession{}, false, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return EngineerSessionSuccession{}, false, err
	}
	defer tx.Rollback()
	succession, found, err := getEngineerSuccessionTx(ctx, tx, cleanSuccessionPath(predecessor.ProjectPath), model.NormalizeSessionSource(predecessor.Provider), strings.TrimSpace(predecessor.SessionID))
	if err != nil || !found {
		return EngineerSessionSuccession{}, false, err
	}
	if !succession.Pending() || succession.SuccessorSessionKey != strings.TrimSpace(launchKey) {
		return succession, false, tx.Commit()
	}
	if succession.SuccessorProvider == succession.Provider && successorSessionID == succession.SessionID {
		return succession, false, tx.Commit()
	}
	succession, err = completeEngineerSuccessionTx(ctx, tx, succession, firstNonEmptyString(successorKey, launchKey), successorSessionID, time.Now())
	if err != nil {
		return EngineerSessionSuccession{}, false, err
	}
	return succession, true, tx.Commit()
}

// CancelPendingEngineerSessionSuccession reinstates the predecessor when its
// successor never started. Completed successions are left untouched.
func (s *Store) CancelPendingEngineerSessionSuccession(ctx context.Context, predecessor EngineerSessionRef, launchKey string) error {
	if s == nil || s.db == nil {
		return errors.New("store unavailable")
	}
	path := cleanSuccessionPath(predecessor.ProjectPath)
	provider := string(model.NormalizeSessionSource(predecessor.Provider))
	sessionID := strings.TrimSpace(predecessor.SessionID)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `DELETE FROM engineer_session_successions
		WHERE project_path = ? AND provider = ? AND session_id = ? AND successor_session_key = ? AND successor_session_id = ''`,
		path, provider, sessionID, strings.TrimSpace(launchKey))
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed > 0 {
		if _, err := tx.ExecContext(ctx, `UPDATE engineer_messages SET last_error = '', updated_at = ?
			WHERE state = 'queued' AND project_path = ? AND provider = ? AND target_session_id = ? AND last_error = ?`,
			time.Now().Unix(), path, provider, sessionID, engineerSuccessionPendingStatus(sessionID)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ResolveEngineerSessionSuccessor follows completed successions to the current
// conversation. pending reports that the returned session has been replaced
// but its successor has not announced an identity yet.
func (s *Store) ResolveEngineerSessionSuccessor(ctx context.Context, ref EngineerSessionRef) (EngineerSessionRef, bool, error) {
	if s == nil || s.db == nil {
		return ref, false, errors.New("store unavailable")
	}
	return resolveEngineerSuccessorQuery(ctx, s.db, ref)
}

type successionQueryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func resolveEngineerSuccessorQuery(ctx context.Context, db successionQueryer, ref EngineerSessionRef) (EngineerSessionRef, bool, error) {
	ref.ProjectPath = cleanSuccessionPath(ref.ProjectPath)
	ref.Provider = model.NormalizeSessionSource(ref.Provider)
	ref.SessionID = strings.TrimSpace(ref.SessionID)
	if ref.ProjectPath == "" || ref.SessionID == "" {
		return ref, false, nil
	}
	for hop := 0; hop < engineerSuccessionMaxHops; hop++ {
		succession, err := scanEngineerSuccession(db.QueryRowContext(ctx, engineerSuccessionSelect+`
			WHERE project_path = ? AND provider = ? AND session_id = ?`, ref.ProjectPath, string(ref.Provider), ref.SessionID))
		if errors.Is(err, sql.ErrNoRows) {
			return ref, false, nil
		}
		if err != nil {
			return ref, false, err
		}
		if succession.Pending() {
			return ref, true, nil
		}
		ref = EngineerSessionRef{ProjectPath: succession.SuccessorProjectPath, Provider: succession.SuccessorProvider, SessionID: succession.SuccessorSessionID}
	}
	return ref, false, errors.New("engineer session succession chain is too long")
}

// EngineerSessionSupersededByKey reports whether a control channel belongs to
// a conversation that was replaced. Such sessions keep their transcript but
// lose LCR authority, so forwarded reports and tasks stay with the successor.
func (s *Store) EngineerSessionSupersededByKey(ctx context.Context, projectPath string, provider model.SessionSource, key string) (EngineerSessionSuccession, bool, error) {
	if s == nil || s.db == nil {
		return EngineerSessionSuccession{}, false, errors.New("store unavailable")
	}
	projectPath, key = cleanSuccessionPath(projectPath), strings.TrimSpace(key)
	provider = model.NormalizeSessionSource(provider)
	if projectPath == "" || key == "" || provider == model.SessionSourceUnknown {
		return EngineerSessionSuccession{}, false, nil
	}
	succession, err := scanEngineerSuccession(s.db.QueryRowContext(ctx, engineerSuccessionSelect+`
		WHERE project_path = ? AND provider = ? AND (session_key = ? OR session_id = ? OR session_id = (
			SELECT provider_session_id FROM engineer_session_bindings
			WHERE project_path = ? AND provider = ? AND control_session_key = ?))
		LIMIT 1`, projectPath, string(provider), key, key, projectPath, string(provider), key))
	if errors.Is(err, sql.ErrNoRows) {
		return EngineerSessionSuccession{}, false, nil
	}
	if err != nil {
		return EngineerSessionSuccession{}, false, err
	}
	return succession, true, nil
}

func completeEngineerSuccessionTx(ctx context.Context, tx *sql.Tx, succession EngineerSessionSuccession, successorKey, successorSessionID string, now time.Time) (EngineerSessionSuccession, error) {
	succession.SuccessorSessionKey = strings.TrimSpace(firstNonEmptyString(successorKey, succession.SuccessorSessionKey))
	succession.SuccessorSessionID = strings.TrimSpace(successorSessionID)
	succession.CompletedAt = now
	// Reinstating the successor first makes a stored cycle impossible.
	if _, err := tx.ExecContext(ctx, `DELETE FROM engineer_session_successions
		WHERE project_path = ? AND provider = ? AND session_id = ?`,
		succession.SuccessorProjectPath, string(succession.SuccessorProvider), succession.SuccessorSessionID); err != nil {
		return succession, fmt.Errorf("reinstate successor session: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE engineer_session_successions
		SET successor_session_key = ?, successor_session_id = ?, completed_at = ?
		WHERE project_path = ? AND provider = ? AND session_id = ?`,
		succession.SuccessorSessionKey, succession.SuccessorSessionID, now.Unix(),
		succession.ProjectPath, string(succession.Provider), succession.SessionID); err != nil {
		return succession, fmt.Errorf("complete engineer session succession: %w", err)
	}
	return succession, forwardEngineerSessionWorkTx(ctx, tx, succession, now)
}

// forwardEngineerSessionWorkTx moves work still addressed to the predecessor:
// queued exact messages, task caller bindings and awaiting dispatch reports.
// Possibly-submitted messages stay put; they may already be in the old
// transcript, and resubmitting them elsewhere could duplicate work.
func forwardEngineerSessionWorkTx(ctx context.Context, tx *sql.Tx, from EngineerSessionSuccession, now time.Time) error {
	to := EngineerSessionRef{ProjectPath: from.SuccessorProjectPath, Provider: from.SuccessorProvider, SessionID: from.SuccessorSessionID}
	toKey := from.SuccessorSessionKey
	if to.Provider == model.SessionSourceLCAgent {
		toKey = to.SessionID // LCAgent controls authenticate with the stable thread ID.
	}
	status := fmt.Sprintf("Forwarded from session %s, which was replaced through /handoff.", from.SessionID)
	if _, err := tx.ExecContext(ctx, `
		UPDATE engineer_messages SET project_path = ?, provider = ?, target_session_id = ?, last_error = ?, updated_at = ?
		WHERE state = 'queued' AND submission_uncertain = 0 AND follow_current_session = 0
			AND project_path = ? AND provider = ? AND target_session_id = ?
	`, to.ProjectPath, string(to.Provider), to.SessionID, status, now.Unix(),
		from.ProjectPath, string(from.Provider), from.SessionID); err != nil {
		return fmt.Errorf("forward queued engineer messages: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE agent_tasks SET origin_provider = ?, origin_session_id = ?, origin_session_key = ?, updated_at = ?
		WHERE status != 'archived' AND origin_provider = ?
			AND CASE WHEN origin_worktree_path != '' THEN origin_worktree_path ELSE origin_project_path END = ?
			AND (origin_session_id = ? OR (origin_session_id = '' AND ? != '' AND origin_session_key = ?))
	`, string(to.Provider), to.SessionID, toKey, now.Unix(),
		string(from.Provider), from.ProjectPath, from.SessionID, from.SessionKey, from.SessionKey); err != nil {
		return fmt.Errorf("forward agent task callers: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE engineer_dispatches SET caller_provider = ?, caller_session_id = ?,
			caller_session_key = CASE WHEN ? != '' THEN ? ELSE caller_session_key END, updated_at = ?
		WHERE reply_state IN (?, ?) AND caller_project_path = ? AND caller_provider = ?
			AND (caller_session_id = ? OR (caller_session_id = '' AND ? != '' AND caller_session_key = ?))
	`, string(to.Provider), to.SessionID, toKey, toKey, now.Unix(),
		string(model.EngineerDispatchReplyAwaiting), string(model.EngineerDispatchReplyReady),
		from.ProjectPath, string(from.Provider), from.SessionID, from.SessionKey, from.SessionKey); err != nil {
		return fmt.Errorf("forward engineer dispatch reports: %w", err)
	}
	return nil
}

func engineerSuccessionPendingStatus(sessionID string) string {
	return fmt.Sprintf("Waiting for the session that replaced %s through /handoff to start; it will receive this message.", sessionID)
}

func getEngineerSuccessionTx(ctx context.Context, tx *sql.Tx, projectPath string, provider model.SessionSource, sessionID string) (EngineerSessionSuccession, bool, error) {
	succession, err := scanEngineerSuccession(tx.QueryRowContext(ctx, engineerSuccessionSelect+`
		WHERE project_path = ? AND provider = ? AND session_id = ?`, projectPath, string(provider), sessionID))
	if errors.Is(err, sql.ErrNoRows) {
		return EngineerSessionSuccession{}, false, nil
	}
	if err != nil {
		return EngineerSessionSuccession{}, false, err
	}
	return succession, true, nil
}

type successionScanner interface {
	Scan(dest ...any) error
}

func scanEngineerSuccession(row successionScanner) (EngineerSessionSuccession, error) {
	var succession EngineerSessionSuccession
	var provider, successorProvider string
	var createdAt, completedAt int64
	if err := row.Scan(&succession.ProjectPath, &provider, &succession.SessionID, &succession.SessionKey,
		&succession.SuccessorProjectPath, &successorProvider, &succession.SuccessorSessionKey,
		&succession.SuccessorSessionID, &succession.Reason, &createdAt, &completedAt); err != nil {
		return EngineerSessionSuccession{}, err
	}
	succession.Provider = model.SessionSource(provider)
	succession.SuccessorProvider = model.SessionSource(successorProvider)
	if createdAt > 0 {
		succession.CreatedAt = time.Unix(createdAt, 0)
	}
	if completedAt > 0 {
		succession.CompletedAt = time.Unix(completedAt, 0)
	}
	return succession, nil
}

func scanEngineerSuccessions(rows *sql.Rows) ([]EngineerSessionSuccession, error) {
	defer rows.Close()
	var out []EngineerSessionSuccession
	for rows.Next() {
		succession, err := scanEngineerSuccession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, succession)
	}
	return out, rows.Err()
}
