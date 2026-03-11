package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"lcroom/internal/model"

	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

const sqliteBusyTimeout = 5 * time.Second

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create db directory: %w", err)
	}
	db, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	s := &Store{db: db}
	if err := s.initSchema(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func sqliteDSN(path string) string {
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}

	return fmt.Sprintf(
		"%s%s_pragma=foreign_keys(ON)&_pragma=busy_timeout(%d)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)",
		path,
		sep,
		sqliteBusyTimeout/time.Millisecond,
	)
}

func (s *Store) initSchema(ctx context.Context) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS projects (
			path TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			last_activity INTEGER,
			status TEXT NOT NULL,
			attention_score INTEGER NOT NULL,
			present_on_disk INTEGER NOT NULL DEFAULT 1,
			repo_dirty INTEGER NOT NULL DEFAULT 0,
			repo_sync_status TEXT NOT NULL DEFAULT '',
			repo_ahead_count INTEGER NOT NULL DEFAULT 0,
			repo_behind_count INTEGER NOT NULL DEFAULT 0,
			forgotten INTEGER NOT NULL DEFAULT 0,
			in_scope INTEGER NOT NULL DEFAULT 1,
			pinned INTEGER NOT NULL DEFAULT 0,
			snoozed_until INTEGER,
			note TEXT NOT NULL DEFAULT '',
			moved_from_path TEXT NOT NULL DEFAULT '',
			moved_at INTEGER,
			updated_at INTEGER NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS project_reasons (
			project_path TEXT NOT NULL,
			position INTEGER NOT NULL,
			code TEXT NOT NULL,
			text TEXT NOT NULL,
			weight INTEGER NOT NULL,
			PRIMARY KEY (project_path, position),
			FOREIGN KEY(project_path) REFERENCES projects(path) ON DELETE CASCADE
		);`,
		`CREATE TABLE IF NOT EXISTS project_sessions (
			session_id TEXT PRIMARY KEY,
			project_path TEXT NOT NULL,
			detected_project_path TEXT NOT NULL DEFAULT '',
			session_file TEXT NOT NULL,
			format TEXT NOT NULL,
			snapshot_hash TEXT NOT NULL DEFAULT '',
			started_at INTEGER,
			last_event_at INTEGER NOT NULL,
			error_count INTEGER NOT NULL,
			latest_turn_started_at INTEGER,
			latest_turn_state_known INTEGER NOT NULL DEFAULT 0,
			latest_turn_completed INTEGER NOT NULL DEFAULT 0,
			updated_at INTEGER NOT NULL,
			FOREIGN KEY(project_path) REFERENCES projects(path) ON DELETE CASCADE
		);`,
		`CREATE INDEX IF NOT EXISTS idx_project_sessions_project_path ON project_sessions(project_path);`,
		`CREATE TABLE IF NOT EXISTS project_artifacts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			project_path TEXT NOT NULL,
			path TEXT NOT NULL,
			kind TEXT NOT NULL,
			updated_at INTEGER NOT NULL,
			note TEXT NOT NULL,
			FOREIGN KEY(project_path) REFERENCES projects(path) ON DELETE CASCADE
			);`,
		`CREATE INDEX IF NOT EXISTS idx_project_artifacts_project_path ON project_artifacts(project_path);`,
		`CREATE TABLE IF NOT EXISTS session_classifications (
			session_id TEXT PRIMARY KEY,
			project_path TEXT NOT NULL,
			session_file TEXT NOT NULL,
			session_format TEXT NOT NULL,
			snapshot_hash TEXT NOT NULL,
			status TEXT NOT NULL,
			stage TEXT NOT NULL DEFAULT '',
			category TEXT NOT NULL DEFAULT '',
			summary TEXT NOT NULL DEFAULT '',
			confidence REAL NOT NULL DEFAULT 0,
			model TEXT NOT NULL DEFAULT '',
			classifier_version TEXT NOT NULL DEFAULT '',
			last_error TEXT NOT NULL DEFAULT '',
			source_updated_at INTEGER NOT NULL,
			created_at INTEGER NOT NULL,
			stage_started_at INTEGER,
			updated_at INTEGER NOT NULL,
			completed_at INTEGER
		);`,
		`CREATE INDEX IF NOT EXISTS idx_session_classifications_project_path ON session_classifications(project_path);`,
		`CREATE INDEX IF NOT EXISTS idx_session_classifications_status_updated ON session_classifications(status, updated_at);`,
		`CREATE TABLE IF NOT EXISTS path_aliases (
			old_path TEXT PRIMARY KEY,
			new_path TEXT NOT NULL,
			reason TEXT NOT NULL DEFAULT '',
			updated_at INTEGER NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS project_git_fingerprints (
			project_path TEXT PRIMARY KEY,
			head_hash TEXT NOT NULL DEFAULT '',
			recent_hashes TEXT NOT NULL DEFAULT '',
			updated_at INTEGER NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			ts INTEGER NOT NULL,
			project_path TEXT NOT NULL,
			event_type TEXT NOT NULL,
			payload TEXT NOT NULL
		);`,
	}

	for _, stmt := range stmts {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("init schema: %w", err)
		}
	}
	if err := s.ensureProjectsInScopeColumn(ctx); err != nil {
		return err
	}
	if err := s.ensureProjectsVisibilityColumns(ctx); err != nil {
		return err
	}
	if err := s.ensureProjectsMoveColumns(ctx); err != nil {
		return err
	}
	if err := s.ensureProjectSessionsDetectedPathColumn(ctx); err != nil {
		return err
	}
	if err := s.ensureProjectSessionsSnapshotHashColumn(ctx); err != nil {
		return err
	}
	if err := s.ensureProjectSessionsTurnLifecycleColumns(ctx); err != nil {
		return err
	}
	if err := s.ensureSessionClassificationStageColumns(ctx); err != nil {
		return err
	}
	return nil
}

func (s *Store) projectTableColumns(ctx context.Context) (map[string]struct{}, error) {
	rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(projects)`)
	if err != nil {
		return nil, fmt.Errorf("check projects schema: %w", err)
	}
	defer rows.Close()

	columns := map[string]struct{}{}
	for rows.Next() {
		var (
			cid       int
			name      string
			typeName  string
			notNull   int
			defaultV  sql.NullString
			isPrimary int
		)
		if err := rows.Scan(&cid, &name, &typeName, &notNull, &defaultV, &isPrimary); err != nil {
			return nil, fmt.Errorf("scan projects schema: %w", err)
		}
		columns[name] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read projects schema: %w", err)
	}
	return columns, nil
}

func (s *Store) ensureProjectsInScopeColumn(ctx context.Context) error {
	columns, err := s.projectTableColumns(ctx)
	if err != nil {
		return err
	}
	if _, ok := columns["in_scope"]; ok {
		return nil
	}

	if _, err := s.db.ExecContext(ctx, `ALTER TABLE projects ADD COLUMN in_scope INTEGER NOT NULL DEFAULT 1`); err != nil {
		return fmt.Errorf("add projects.in_scope column: %w", err)
	}
	return nil
}

func (s *Store) ensureProjectsVisibilityColumns(ctx context.Context) error {
	columns, err := s.projectTableColumns(ctx)
	if err != nil {
		return err
	}
	if _, ok := columns["present_on_disk"]; !ok {
		if _, err := s.db.ExecContext(ctx, `ALTER TABLE projects ADD COLUMN present_on_disk INTEGER NOT NULL DEFAULT 1`); err != nil {
			return fmt.Errorf("add projects.present_on_disk column: %w", err)
		}
	}
	if _, ok := columns["repo_dirty"]; !ok {
		if _, err := s.db.ExecContext(ctx, `ALTER TABLE projects ADD COLUMN repo_dirty INTEGER NOT NULL DEFAULT 0`); err != nil {
			return fmt.Errorf("add projects.repo_dirty column: %w", err)
		}
	}
	if _, ok := columns["repo_sync_status"]; !ok {
		if _, err := s.db.ExecContext(ctx, `ALTER TABLE projects ADD COLUMN repo_sync_status TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("add projects.repo_sync_status column: %w", err)
		}
	}
	if _, ok := columns["repo_ahead_count"]; !ok {
		if _, err := s.db.ExecContext(ctx, `ALTER TABLE projects ADD COLUMN repo_ahead_count INTEGER NOT NULL DEFAULT 0`); err != nil {
			return fmt.Errorf("add projects.repo_ahead_count column: %w", err)
		}
	}
	if _, ok := columns["repo_behind_count"]; !ok {
		if _, err := s.db.ExecContext(ctx, `ALTER TABLE projects ADD COLUMN repo_behind_count INTEGER NOT NULL DEFAULT 0`); err != nil {
			return fmt.Errorf("add projects.repo_behind_count column: %w", err)
		}
	}
	if _, ok := columns["forgotten"]; ok {
		return nil
	}
	if _, err := s.db.ExecContext(ctx, `ALTER TABLE projects ADD COLUMN forgotten INTEGER NOT NULL DEFAULT 0`); err != nil {
		return fmt.Errorf("add projects.forgotten column: %w", err)
	}
	return nil
}

func (s *Store) ensureProjectsMoveColumns(ctx context.Context) error {
	columns, err := s.projectTableColumns(ctx)
	if err != nil {
		return err
	}
	if _, ok := columns["moved_from_path"]; !ok {
		if _, err := s.db.ExecContext(ctx, `ALTER TABLE projects ADD COLUMN moved_from_path TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("add projects.moved_from_path column: %w", err)
		}
	}
	if _, ok := columns["moved_at"]; ok {
		return nil
	}
	if _, err := s.db.ExecContext(ctx, `ALTER TABLE projects ADD COLUMN moved_at INTEGER`); err != nil {
		return fmt.Errorf("add projects.moved_at column: %w", err)
	}
	return nil
}

func (s *Store) projectSessionsTableColumns(ctx context.Context) (map[string]struct{}, error) {
	rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(project_sessions)`)
	if err != nil {
		return nil, fmt.Errorf("check project_sessions schema: %w", err)
	}
	defer rows.Close()

	columns := map[string]struct{}{}
	for rows.Next() {
		var (
			cid       int
			name      string
			typeName  string
			notNull   int
			defaultV  sql.NullString
			isPrimary int
		)
		if err := rows.Scan(&cid, &name, &typeName, &notNull, &defaultV, &isPrimary); err != nil {
			return nil, fmt.Errorf("scan project_sessions schema: %w", err)
		}
		columns[name] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read project_sessions schema: %w", err)
	}
	return columns, nil
}

func (s *Store) ensureProjectSessionsDetectedPathColumn(ctx context.Context) error {
	columns, err := s.projectSessionsTableColumns(ctx)
	if err != nil {
		return err
	}
	if _, ok := columns["detected_project_path"]; ok {
		return nil
	}
	if _, err := s.db.ExecContext(ctx, `ALTER TABLE project_sessions ADD COLUMN detected_project_path TEXT NOT NULL DEFAULT ''`); err != nil {
		return fmt.Errorf("add project_sessions.detected_project_path column: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE project_sessions SET detected_project_path = project_path WHERE detected_project_path = ''`); err != nil {
		return fmt.Errorf("backfill project_sessions.detected_project_path: %w", err)
	}
	return nil
}

func (s *Store) ensureProjectSessionsSnapshotHashColumn(ctx context.Context) error {
	columns, err := s.projectSessionsTableColumns(ctx)
	if err != nil {
		return err
	}
	if _, ok := columns["snapshot_hash"]; ok {
		return nil
	}
	if _, err := s.db.ExecContext(ctx, `ALTER TABLE project_sessions ADD COLUMN snapshot_hash TEXT NOT NULL DEFAULT ''`); err != nil {
		return fmt.Errorf("add project_sessions.snapshot_hash column: %w", err)
	}
	return nil
}

func (s *Store) ensureProjectSessionsTurnLifecycleColumns(ctx context.Context) error {
	columns, err := s.projectSessionsTableColumns(ctx)
	if err != nil {
		return err
	}
	if _, ok := columns["latest_turn_started_at"]; !ok {
		if _, err := s.db.ExecContext(ctx, `ALTER TABLE project_sessions ADD COLUMN latest_turn_started_at INTEGER`); err != nil {
			return fmt.Errorf("add project_sessions.latest_turn_started_at column: %w", err)
		}
	}
	if _, ok := columns["latest_turn_state_known"]; !ok {
		if _, err := s.db.ExecContext(ctx, `ALTER TABLE project_sessions ADD COLUMN latest_turn_state_known INTEGER NOT NULL DEFAULT 0`); err != nil {
			return fmt.Errorf("add project_sessions.latest_turn_state_known column: %w", err)
		}
	}
	if _, ok := columns["latest_turn_completed"]; ok {
		return nil
	}
	if _, err := s.db.ExecContext(ctx, `ALTER TABLE project_sessions ADD COLUMN latest_turn_completed INTEGER NOT NULL DEFAULT 0`); err != nil {
		return fmt.Errorf("add project_sessions.latest_turn_completed column: %w", err)
	}
	return nil
}

func (s *Store) sessionClassificationTableColumns(ctx context.Context) (map[string]struct{}, error) {
	rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(session_classifications)`)
	if err != nil {
		return nil, fmt.Errorf("check session classifications schema: %w", err)
	}
	defer rows.Close()

	columns := map[string]struct{}{}
	for rows.Next() {
		var (
			cid       int
			name      string
			typeName  string
			notNull   int
			defaultV  sql.NullString
			isPrimary int
		)
		if err := rows.Scan(&cid, &name, &typeName, &notNull, &defaultV, &isPrimary); err != nil {
			return nil, fmt.Errorf("scan session classifications schema: %w", err)
		}
		columns[name] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read session classifications schema: %w", err)
	}
	return columns, nil
}

func (s *Store) ensureSessionClassificationStageColumns(ctx context.Context) error {
	columns, err := s.sessionClassificationTableColumns(ctx)
	if err != nil {
		return err
	}
	if _, ok := columns["stage"]; !ok {
		if _, err := s.db.ExecContext(ctx, `ALTER TABLE session_classifications ADD COLUMN stage TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("add session_classifications.stage column: %w", err)
		}
	}
	if _, ok := columns["stage_started_at"]; ok {
		return nil
	}
	if _, err := s.db.ExecContext(ctx, `ALTER TABLE session_classifications ADD COLUMN stage_started_at INTEGER`); err != nil {
		return fmt.Errorf("add session_classifications.stage_started_at column: %w", err)
	}
	return nil
}

func (s *Store) GetProjectSummaryMap(ctx context.Context) (map[string]model.ProjectSummary, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			p.path, p.name, p.last_activity, p.status, p.attention_score, p.present_on_disk, p.repo_dirty, p.repo_sync_status, p.repo_ahead_count, p.repo_behind_count, p.forgotten, p.in_scope, p.pinned, p.snoozed_until, p.note,
			p.moved_from_path, p.moved_at,
			COALESCE(ps.session_id, ''),
			COALESCE(ps.format, ''),
			COALESCE(ps.detected_project_path, ''),
			ps.latest_turn_started_at,
			COALESCE(ps.latest_turn_state_known, 0),
			COALESCE(ps.latest_turn_completed, 0),
			COALESCE(sc.status, ''),
			COALESCE(sc.stage, ''),
			COALESCE(sc.category, ''),
			COALESCE(sc.summary, ''),
			sc.stage_started_at,
			sc.updated_at
		FROM projects p
		LEFT JOIN project_sessions ps ON ps.session_id = (
			SELECT ps2.session_id
			FROM project_sessions ps2
			WHERE ps2.project_path = p.path
			ORDER BY ps2.last_event_at DESC
			LIMIT 1
		)
		LEFT JOIN session_classifications sc ON sc.session_id = ps.session_id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]model.ProjectSummary{}
	for rows.Next() {
		p, err := scanSummaryRow(rows)
		if err != nil {
			return nil, err
		}
		out[p.Path] = p
	}
	return out, rows.Err()
}

func (s *Store) ListProjects(ctx context.Context, includeHistorical bool) ([]model.ProjectSummary, error) {
	query := `
		SELECT
			p.path, p.name, p.last_activity, p.status, p.attention_score, p.present_on_disk, p.repo_dirty, p.repo_sync_status, p.repo_ahead_count, p.repo_behind_count, p.forgotten, p.in_scope, p.pinned, p.snoozed_until, p.note,
			p.moved_from_path, p.moved_at,
			COALESCE(ps.session_id, ''),
			COALESCE(ps.format, ''),
			COALESCE(ps.detected_project_path, ''),
			ps.latest_turn_started_at,
			COALESCE(ps.latest_turn_state_known, 0),
			COALESCE(ps.latest_turn_completed, 0),
			COALESCE(sc.status, ''),
			COALESCE(sc.stage, ''),
			COALESCE(sc.category, ''),
			COALESCE(sc.summary, ''),
			sc.stage_started_at,
			sc.updated_at
		FROM projects p
		LEFT JOIN project_sessions ps ON ps.session_id = (
			SELECT ps2.session_id
			FROM project_sessions ps2
			WHERE ps2.project_path = p.path
			ORDER BY ps2.last_event_at DESC
			LIMIT 1
		)
		LEFT JOIN session_classifications sc ON sc.session_id = ps.session_id
	`
	query += ` WHERE p.forgotten = 0`
	if !includeHistorical {
		query += ` AND p.in_scope = 1`
	}
	query += ` ORDER BY p.attention_score DESC, p.last_activity DESC, p.name ASC`

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []model.ProjectSummary{}
	for rows.Next() {
		p, err := scanSummaryRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func scanSummaryRow(scanner interface {
	Scan(dest ...any) error
}) (model.ProjectSummary, error) {
	var (
		path, name, status, note, movedFromPath                                                     string
		lastActivity, snoozedUntil, movedAt, latestTurnStartedAt                                    sql.NullInt64
		latestSessionID, latestSessionFormat, latestSessionDetectedPath, latestClassificationStatus sql.NullString
		latestClassificationStage, latestClassificationCategory, latestClassificationSummary        sql.NullString
		latestClassificationStageStartedAt, latestClassificationUpdatedAt                           sql.NullInt64
		repoSyncStatus                                                                              string
		attentionScore, repoAheadCount, repoBehindCount, latestTurnKnown, latestTurnCompleted       int
		presentOnDisk, repoDirty, forgotten, inScope, pinned                                        int
	)
	if err := scanner.Scan(
		&path,
		&name,
		&lastActivity,
		&status,
		&attentionScore,
		&presentOnDisk,
		&repoDirty,
		&repoSyncStatus,
		&repoAheadCount,
		&repoBehindCount,
		&forgotten,
		&inScope,
		&pinned,
		&snoozedUntil,
		&note,
		&movedFromPath,
		&movedAt,
		&latestSessionID,
		&latestSessionFormat,
		&latestSessionDetectedPath,
		&latestTurnStartedAt,
		&latestTurnKnown,
		&latestTurnCompleted,
		&latestClassificationStatus,
		&latestClassificationStage,
		&latestClassificationCategory,
		&latestClassificationSummary,
		&latestClassificationStageStartedAt,
		&latestClassificationUpdatedAt,
	); err != nil {
		return model.ProjectSummary{}, err
	}
	p := model.ProjectSummary{
		Path:                             path,
		Name:                             name,
		Status:                           model.ProjectStatus(status),
		AttentionScore:                   attentionScore,
		PresentOnDisk:                    presentOnDisk == 1,
		RepoDirty:                        repoDirty == 1,
		RepoSyncStatus:                   model.RepoSyncStatus(repoSyncStatus),
		RepoAheadCount:                   repoAheadCount,
		RepoBehindCount:                  repoBehindCount,
		Forgotten:                        forgotten == 1,
		InScope:                          inScope == 1,
		Pinned:                           pinned == 1,
		Note:                             note,
		MovedFromPath:                    movedFromPath,
		LatestSessionID:                  latestSessionID.String,
		LatestSessionFormat:              latestSessionFormat.String,
		LatestSessionDetectedProjectPath: latestSessionDetectedPath.String,
		LatestTurnStateKnown:             latestTurnKnown != 0,
		LatestTurnCompleted:              latestTurnCompleted != 0,
		LatestSessionClassification:      model.SessionClassificationStatus(latestClassificationStatus.String),
		LatestSessionClassificationStage: model.SessionClassificationStage(latestClassificationStage.String),
		LatestSessionClassificationType:  model.SessionCategory(latestClassificationCategory.String),
		LatestSessionSummary:             latestClassificationSummary.String,
	}
	if lastActivity.Valid {
		p.LastActivity = time.Unix(lastActivity.Int64, 0)
	}
	if snoozedUntil.Valid {
		t := time.Unix(snoozedUntil.Int64, 0)
		p.SnoozedUntil = &t
	}
	if movedAt.Valid {
		p.MovedAt = time.Unix(movedAt.Int64, 0)
	}
	if latestTurnStartedAt.Valid {
		p.LatestTurnStartedAt = time.Unix(latestTurnStartedAt.Int64, 0)
	}
	if latestClassificationStageStartedAt.Valid {
		p.LatestSessionClassificationStageStartedAt = time.Unix(latestClassificationStageStartedAt.Int64, 0)
	}
	if latestClassificationUpdatedAt.Valid {
		p.LatestSessionClassificationUpdatedAt = time.Unix(latestClassificationUpdatedAt.Int64, 0)
	}
	return p, nil
}

func (s *Store) GetSessionClassificationCounts(ctx context.Context, inScopeOnly bool) (map[model.SessionClassificationStatus]int, error) {
	query := `
		SELECT sc.status, COUNT(*)
		FROM session_classifications sc
	`
	if inScopeOnly {
		query += ` JOIN projects p ON p.path = sc.project_path WHERE p.in_scope = 1`
	}
	query += ` GROUP BY sc.status`

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	counts := map[model.SessionClassificationStatus]int{}
	for rows.Next() {
		var (
			status string
			count  int
		)
		if err := rows.Scan(&status, &count); err != nil {
			return nil, err
		}
		counts[model.SessionClassificationStatus(status)] = count
	}
	return counts, rows.Err()
}

func (s *Store) UpsertProjectState(ctx context.Context, state model.ProjectState) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	if state.Name == "" {
		state.Name = filepath.Base(state.Path)
	}
	if state.UpdatedAt.IsZero() {
		state.UpdatedAt = time.Now()
	}

	var (
		lastActivity any
		snoozedUntil any
		movedAt      any
	)
	if !state.LastActivity.IsZero() {
		lastActivity = state.LastActivity.Unix()
	}
	if state.SnoozedUntil != nil {
		snoozedUntil = state.SnoozedUntil.Unix()
	}
	if !state.MovedAt.IsZero() {
		movedAt = state.MovedAt.Unix()
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO projects(path, name, last_activity, status, attention_score, present_on_disk, repo_dirty, repo_sync_status, repo_ahead_count, repo_behind_count, forgotten, in_scope, pinned, snoozed_until, note, moved_from_path, moved_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET
			name=excluded.name,
			last_activity=excluded.last_activity,
			status=excluded.status,
			attention_score=excluded.attention_score,
			present_on_disk=excluded.present_on_disk,
			repo_dirty=excluded.repo_dirty,
			repo_sync_status=excluded.repo_sync_status,
			repo_ahead_count=excluded.repo_ahead_count,
			repo_behind_count=excluded.repo_behind_count,
			forgotten=excluded.forgotten,
			in_scope=excluded.in_scope,
			pinned=excluded.pinned,
			snoozed_until=excluded.snoozed_until,
			note=excluded.note,
			moved_from_path=CASE
				WHEN excluded.moved_from_path != '' THEN excluded.moved_from_path
				ELSE projects.moved_from_path
			END,
			moved_at=CASE
				WHEN excluded.moved_at IS NOT NULL THEN excluded.moved_at
				ELSE projects.moved_at
			END,
			updated_at=excluded.updated_at
	`, state.Path, state.Name, lastActivity, string(state.Status), state.AttentionScore, boolToInt(state.PresentOnDisk), boolToInt(state.RepoDirty), string(state.RepoSyncStatus), state.RepoAheadCount, state.RepoBehindCount, boolToInt(state.Forgotten), boolToInt(state.InScope), boolToInt(state.Pinned), snoozedUntil, state.Note, state.MovedFromPath, movedAt, state.UpdatedAt.Unix())
	if err != nil {
		return err
	}

	if _, err = tx.ExecContext(ctx, `DELETE FROM project_reasons WHERE project_path = ?`, state.Path); err != nil {
		return err
	}
	for i, reason := range state.AttentionReason {
		_, err = tx.ExecContext(ctx, `
			INSERT INTO project_reasons(project_path, position, code, text, weight)
			VALUES (?, ?, ?, ?, ?)
		`, state.Path, i, reason.Code, reason.Text, reason.Weight)
		if err != nil {
			return err
		}
	}

	if _, err = tx.ExecContext(ctx, `DELETE FROM project_sessions WHERE project_path = ?`, state.Path); err != nil {
		return err
	}
	for _, session := range state.Sessions {
		var startedAt any
		if !session.StartedAt.IsZero() {
			startedAt = session.StartedAt.Unix()
		}
		var latestTurnStartedAt any
		if !session.LatestTurnStartedAt.IsZero() {
			latestTurnStartedAt = session.LatestTurnStartedAt.Unix()
		}
		detectedPath := session.DetectedProjectPath
		if detectedPath == "" {
			detectedPath = session.ProjectPath
		}
		if detectedPath == "" {
			detectedPath = state.Path
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO project_sessions(session_id, project_path, detected_project_path, session_file, format, snapshot_hash, started_at, last_event_at, error_count, latest_turn_started_at, latest_turn_state_known, latest_turn_completed, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, session.SessionID, state.Path, detectedPath, session.SessionFile, session.Format, session.SnapshotHash, startedAt, session.LastEventAt.Unix(), session.ErrorCount, latestTurnStartedAt, boolToInt(session.LatestTurnStateKnown), boolToInt(session.LatestTurnCompleted), state.UpdatedAt.Unix())
		if err != nil {
			return err
		}
	}

	if _, err = tx.ExecContext(ctx, `DELETE FROM project_artifacts WHERE project_path = ?`, state.Path); err != nil {
		return err
	}
	for _, artifact := range state.Artifacts {
		_, err = tx.ExecContext(ctx, `
			INSERT INTO project_artifacts(project_path, path, kind, updated_at, note)
			VALUES (?, ?, ?, ?, ?)
		`, state.Path, artifact.Path, artifact.Kind, artifact.UpdatedAt.Unix(), artifact.Note)
		if err != nil {
			return err
		}
	}

	if err = tx.Commit(); err != nil {
		return err
	}
	return nil
}

func (s *Store) AddEvent(ctx context.Context, event model.StoredEvent) error {
	if event.At.IsZero() {
		event.At = time.Now()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO events(ts, project_path, event_type, payload)
		VALUES (?, ?, ?, ?)
	`, event.At.Unix(), event.ProjectPath, event.Type, event.Payload)
	return err
}

func (s *Store) GetPathAliases(ctx context.Context) (map[string]model.PathAlias, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT old_path, new_path, reason, updated_at
		FROM path_aliases
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]model.PathAlias{}
	for rows.Next() {
		var (
			alias     model.PathAlias
			updatedAt int64
		)
		if err := rows.Scan(&alias.OldPath, &alias.NewPath, &alias.Reason, &updatedAt); err != nil {
			return nil, err
		}
		alias.UpdatedAt = time.Unix(updatedAt, 0)
		out[alias.OldPath] = alias
	}
	return out, rows.Err()
}

func (s *Store) UpsertPathAlias(ctx context.Context, alias model.PathAlias) error {
	if alias.OldPath == "" || alias.NewPath == "" {
		return errors.New("path alias requires old_path and new_path")
	}
	if alias.UpdatedAt.IsZero() {
		alias.UpdatedAt = time.Now()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO path_aliases(old_path, new_path, reason, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(old_path) DO UPDATE SET
			new_path=excluded.new_path,
			reason=excluded.reason,
			updated_at=excluded.updated_at
	`, alias.OldPath, alias.NewPath, alias.Reason, alias.UpdatedAt.Unix())
	return err
}

func (s *Store) GetProjectGitFingerprints(ctx context.Context) (map[string]model.ProjectGitFingerprint, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT project_path, head_hash, recent_hashes, updated_at
		FROM project_git_fingerprints
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]model.ProjectGitFingerprint{}
	for rows.Next() {
		var (
			projectPath  string
			headHash     string
			recentHashes string
			updatedAt    int64
		)
		if err := rows.Scan(&projectPath, &headHash, &recentHashes, &updatedAt); err != nil {
			return nil, err
		}
		out[projectPath] = model.ProjectGitFingerprint{
			ProjectPath:  projectPath,
			HeadHash:     headHash,
			RecentHashes: splitRecentHashes(recentHashes),
			UpdatedAt:    time.Unix(updatedAt, 0),
		}
	}
	return out, rows.Err()
}

func (s *Store) UpsertProjectGitFingerprint(ctx context.Context, fingerprint model.ProjectGitFingerprint) error {
	if fingerprint.ProjectPath == "" {
		return errors.New("project git fingerprint requires project_path")
	}
	if fingerprint.UpdatedAt.IsZero() {
		fingerprint.UpdatedAt = time.Now()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO project_git_fingerprints(project_path, head_hash, recent_hashes, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(project_path) DO UPDATE SET
			head_hash=excluded.head_hash,
			recent_hashes=excluded.recent_hashes,
			updated_at=excluded.updated_at
	`, fingerprint.ProjectPath, fingerprint.HeadHash, strings.Join(fingerprint.RecentHashes, "\n"), fingerprint.UpdatedAt.Unix())
	return err
}

func (s *Store) MoveProjectPath(ctx context.Context, oldPath, newPath string, movedAt time.Time) error {
	if oldPath == "" || newPath == "" {
		return errors.New("move project path requires old and new paths")
	}
	if oldPath == newPath {
		return nil
	}
	if movedAt.IsZero() {
		movedAt = time.Now()
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	var existingCount int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM projects WHERE path = ?`, newPath).Scan(&existingCount); err != nil {
		return err
	}
	if existingCount > 0 {
		return fmt.Errorf("target project path already exists: %s", newPath)
	}

	result, err := tx.ExecContext(ctx, `
		INSERT INTO projects(path, name, last_activity, status, attention_score, present_on_disk, repo_dirty, repo_sync_status, repo_ahead_count, repo_behind_count, forgotten, in_scope, pinned, snoozed_until, note, moved_from_path, moved_at, updated_at)
		SELECT ?, ?, last_activity, status, attention_score, present_on_disk, repo_dirty, repo_sync_status, repo_ahead_count, repo_behind_count, forgotten, in_scope, pinned, snoozed_until, note, ?, ?, ?
		FROM projects
		WHERE path = ?
	`, newPath, filepath.Base(newPath), oldPath, movedAt.Unix(), movedAt.Unix(), oldPath)
	if err != nil {
		return err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected != 1 {
		return fmt.Errorf("project not found: %s", oldPath)
	}

	updateStatements := []string{
		`UPDATE project_reasons SET project_path = ? WHERE project_path = ?`,
		`UPDATE project_sessions SET project_path = ? WHERE project_path = ?`,
		`UPDATE project_artifacts SET project_path = ? WHERE project_path = ?`,
		`UPDATE session_classifications SET project_path = ? WHERE project_path = ?`,
		`UPDATE events SET project_path = ? WHERE project_path = ?`,
	}
	for _, stmt := range updateStatements {
		if _, err = tx.ExecContext(ctx, stmt, newPath, oldPath); err != nil {
			return err
		}
	}

	if _, err = tx.ExecContext(ctx, `DELETE FROM projects WHERE path = ?`, oldPath); err != nil {
		return err
	}

	if err = tx.Commit(); err != nil {
		return err
	}
	return nil
}

func (s *Store) ConsolidateProjectPath(ctx context.Context, oldPath, newPath string, movedAt time.Time) error {
	if oldPath == "" || newPath == "" {
		return errors.New("consolidate project path requires old_path and new_path")
	}
	if oldPath == newPath {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	type projectRow struct {
		pinned        bool
		forgotten     bool
		snoozedUntil  sql.NullInt64
		note          string
		movedFromPath string
		movedAt       sql.NullInt64
	}

	loadProject := func(path string) (projectRow, error) {
		var row projectRow
		var pinned, forgotten int
		err := tx.QueryRowContext(ctx, `
			SELECT pinned, forgotten, snoozed_until, note, moved_from_path, moved_at
			FROM projects
			WHERE path = ?
		`, path).Scan(&pinned, &forgotten, &row.snoozedUntil, &row.note, &row.movedFromPath, &row.movedAt)
		if err != nil {
			return projectRow{}, err
		}
		row.pinned = pinned != 0
		row.forgotten = forgotten != 0
		return row, nil
	}

	oldProject, err := loadProject(oldPath)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("project not found: %s", oldPath)
		}
		return err
	}
	newProject, err := loadProject(newPath)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("target project path not found: %s", newPath)
		}
		return err
	}

	mergedPinned := oldProject.pinned || newProject.pinned
	mergedForgotten := oldProject.forgotten || newProject.forgotten
	mergedNote := newProject.note
	if strings.TrimSpace(mergedNote) == "" {
		mergedNote = oldProject.note
	}
	mergedSnoozedUntil := pickLaterNullInt64(oldProject.snoozedUntil, newProject.snoozedUntil)
	mergedMovedFromPath := strings.TrimSpace(newProject.movedFromPath)
	if mergedMovedFromPath == "" {
		mergedMovedFromPath = strings.TrimSpace(oldProject.movedFromPath)
	}
	if mergedMovedFromPath == "" {
		mergedMovedFromPath = oldPath
	}
	mergedMovedAt := pickLaterNullInt64(oldProject.movedAt, newProject.movedAt)
	if !mergedMovedAt.Valid {
		mergedMovedAt = sql.NullInt64{Int64: movedAt.Unix(), Valid: true}
	}

	if _, err = tx.ExecContext(ctx, `
		UPDATE projects
		SET pinned = ?,
			forgotten = ?,
			snoozed_until = ?,
			note = ?,
			moved_from_path = ?,
			moved_at = ?,
			updated_at = ?
		WHERE path = ?
	`, boolToInt(mergedPinned), boolToInt(mergedForgotten), nullableInt64Value(mergedSnoozedUntil), mergedNote, mergedMovedFromPath, nullableInt64Value(mergedMovedAt), movedAt.Unix(), newPath); err != nil {
		return err
	}

	if _, err = tx.ExecContext(ctx, `DELETE FROM project_reasons WHERE project_path = ?`, oldPath); err != nil {
		return err
	}

	updateStatements := []string{
		`UPDATE project_sessions SET project_path = ? WHERE project_path = ?`,
		`UPDATE project_artifacts SET project_path = ? WHERE project_path = ?`,
		`UPDATE session_classifications SET project_path = ? WHERE project_path = ?`,
		`UPDATE events SET project_path = ? WHERE project_path = ?`,
	}
	for _, stmt := range updateStatements {
		if _, err = tx.ExecContext(ctx, stmt, newPath, oldPath); err != nil {
			return err
		}
	}

	if _, err = tx.ExecContext(ctx, `DELETE FROM project_git_fingerprints WHERE project_path = ?`, oldPath); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM projects WHERE path = ?`, oldPath); err != nil {
		return err
	}

	if err = tx.Commit(); err != nil {
		return err
	}
	return nil
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func pickLaterNullInt64(a, b sql.NullInt64) sql.NullInt64 {
	switch {
	case !a.Valid:
		return b
	case !b.Valid:
		return a
	case a.Int64 >= b.Int64:
		return a
	default:
		return b
	}
}

func nullableInt64Value(v sql.NullInt64) any {
	if !v.Valid {
		return nil
	}
	return v.Int64
}

func nullableTimeUnixValue(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.Unix()
}

func (s *Store) QueueSessionClassification(ctx context.Context, classification model.SessionClassification, retryAfter time.Duration) (bool, error) {
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
				session_id, project_path, session_file, session_format, snapshot_hash,
				status, stage, category, summary, confidence, model, classifier_version,
				last_error, source_updated_at, created_at, stage_started_at, updated_at, completed_at
			)
			VALUES (?, ?, ?, ?, ?, ?, ?, '', '', 0, ?, ?, '', ?, ?, ?, ?, NULL)
		`, classification.SessionID, classification.ProjectPath, classification.SessionFile, classification.SessionFormat,
			classification.SnapshotHash, string(model.ClassificationPending), string(classification.Stage), classification.Model,
			classification.ClassifierVersion, classification.SourceUpdatedAt.Unix(), classification.CreatedAt.Unix(), nullableTimeUnixValue(classification.StageStartedAt), now.Unix())
		return err == nil, err
	}

	sameSnapshot := existing.SnapshotHash == classification.SnapshotHash &&
		sameClassificationModel(existing.Model, classification.Model) &&
		existing.ClassifierVersion == classification.ClassifierVersion

	if sameSnapshot {
		switch existing.Status {
		case model.ClassificationCompleted, model.ClassificationPending:
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
			if !existing.UpdatedAt.IsZero() && now.Sub(existing.UpdatedAt) < retryAfter {
				return false, nil
			}
		}
	}

	createdAt := existing.CreatedAt
	if createdAt.IsZero() {
		createdAt = classification.CreatedAt
	}

	_, err = s.db.ExecContext(ctx, `
		UPDATE session_classifications
		SET project_path = ?,
			session_file = ?,
			session_format = ?,
			snapshot_hash = ?,
			status = ?,
			stage = ?,
			category = '',
			summary = '',
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
	`, classification.ProjectPath, classification.SessionFile, classification.SessionFormat, classification.SnapshotHash,
		string(model.ClassificationPending), string(classification.Stage), classification.Model, classification.ClassifierVersion,
		classification.SourceUpdatedAt.Unix(), createdAt.Unix(), nullableTimeUnixValue(classification.StageStartedAt), now.Unix(), classification.SessionID)
	return err == nil, err
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

	row := tx.QueryRowContext(ctx, `
		SELECT
			sc.session_id, sc.project_path, sc.session_file, sc.session_format, sc.snapshot_hash,
			sc.status, sc.stage, sc.category, sc.summary, sc.confidence, sc.model, sc.classifier_version,
			sc.last_error, sc.source_updated_at, sc.created_at, sc.stage_started_at, sc.updated_at, sc.completed_at
		FROM session_classifications sc
		JOIN projects p ON p.path = sc.project_path
		WHERE sc.status = ? AND p.in_scope = 1
		ORDER BY p.attention_score DESC, p.last_activity DESC, sc.updated_at ASC
		LIMIT 1
	`, string(model.ClassificationPending))

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

func (s *Store) UpdateSessionClassificationStage(ctx context.Context, sessionID string, stage model.SessionClassificationStage) error {
	if sessionID == "" {
		return errors.New("session classification requires session_id")
	}
	now := time.Now()
	_, err := s.db.ExecContext(ctx, `
		UPDATE session_classifications
		SET stage = ?, stage_started_at = ?, updated_at = ?
		WHERE session_id = ?
	`, string(stage), now.Unix(), now.Unix(), sessionID)
	return err
}

func (s *Store) FailSessionClassification(ctx context.Context, sessionID, lastError string) error {
	if sessionID == "" {
		return errors.New("session classification requires session_id")
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE session_classifications
		SET status = ?, last_error = ?, updated_at = ?
		WHERE session_id = ?
	`, string(model.ClassificationFailed), lastError, time.Now().Unix(), sessionID)
	return err
}

func (s *Store) GetSessionClassification(ctx context.Context, sessionID string) (model.SessionClassification, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT
			session_id, project_path, session_file, session_format, snapshot_hash,
			status, stage, category, summary, confidence, model, classifier_version,
			last_error, source_updated_at, created_at, stage_started_at, updated_at, completed_at
		FROM session_classifications
		WHERE session_id = ?
	`, sessionID)
	return scanSessionClassificationRow(row)
}

func (s *Store) GetProjectDetail(ctx context.Context, path string, eventLimit int) (model.ProjectDetail, error) {
	if eventLimit <= 0 {
		eventLimit = 20
	}

	row := s.db.QueryRowContext(ctx, `
		SELECT
			p.path, p.name, p.last_activity, p.status, p.attention_score, p.present_on_disk, p.repo_dirty, p.repo_sync_status, p.repo_ahead_count, p.repo_behind_count, p.forgotten, p.in_scope, p.pinned, p.snoozed_until, p.note,
			p.moved_from_path, p.moved_at,
			COALESCE(ps.session_id, ''),
			COALESCE(ps.format, ''),
			COALESCE(ps.detected_project_path, ''),
			ps.latest_turn_started_at,
			COALESCE(ps.latest_turn_state_known, 0),
			COALESCE(ps.latest_turn_completed, 0),
			COALESCE(sc.status, ''),
			COALESCE(sc.stage, ''),
			COALESCE(sc.category, ''),
			COALESCE(sc.summary, ''),
			sc.stage_started_at,
			sc.updated_at
		FROM projects p
		LEFT JOIN project_sessions ps ON ps.session_id = (
			SELECT ps2.session_id
			FROM project_sessions ps2
			WHERE ps2.project_path = p.path
			ORDER BY ps2.last_event_at DESC
			LIMIT 1
		)
		LEFT JOIN session_classifications sc ON sc.session_id = ps.session_id
		WHERE p.path = ?
	`, path)
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
		Sessions:                    sessions,
		Artifacts:                   artifacts,
		RecentEvents:                events,
		LatestSessionClassification: latestClassification,
	}, nil
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
		SELECT session_id, project_path, detected_project_path, session_file, format, snapshot_hash, started_at, last_event_at, error_count, latest_turn_started_at, latest_turn_state_known, latest_turn_completed
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
			startedAt           sql.NullInt64
			lastEventAt         int64
			latestTurnStartedAt sql.NullInt64
			turnKnown           int
			turnDone            int
		)
		if err := rows.Scan(&s.SessionID, &s.ProjectPath, &s.DetectedProjectPath, &s.SessionFile, &s.Format, &s.SnapshotHash, &startedAt, &lastEventAt, &s.ErrorCount, &latestTurnStartedAt, &turnKnown, &turnDone); err != nil {
			return nil, err
		}
		if startedAt.Valid {
			s.StartedAt = time.Unix(startedAt.Int64, 0)
		}
		s.LastEventAt = time.Unix(lastEventAt, 0)
		if latestTurnStartedAt.Valid {
			s.LatestTurnStartedAt = time.Unix(latestTurnStartedAt.Int64, 0)
		}
		s.LatestTurnStateKnown = turnKnown != 0
		s.LatestTurnCompleted = turnDone != 0
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
		status, stage, category               string
		sourceUpdatedAt, createdAt, updatedAt int64
		stageStartedAt, completedAt           sql.NullInt64
	)
	if err := scanner.Scan(
		&classification.SessionID,
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
	classification.SourceUpdatedAt = time.Unix(sourceUpdatedAt, 0)
	classification.CreatedAt = time.Unix(createdAt, 0)
	if stageStartedAt.Valid {
		classification.StageStartedAt = time.Unix(stageStartedAt.Int64, 0)
	}
	classification.UpdatedAt = time.Unix(updatedAt, 0)
	if completedAt.Valid {
		classification.CompletedAt = time.Unix(completedAt.Int64, 0)
	}
	return classification, nil
}

func (s *Store) SetPinned(ctx context.Context, path string, pinned bool) error {
	_, err := s.db.ExecContext(ctx, `UPDATE projects SET pinned = ?, updated_at = ? WHERE path = ?`, boolToInt(pinned), time.Now().Unix(), path)
	return err
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

func (s *Store) SetSnooze(ctx context.Context, path string, until *time.Time) error {
	var v any
	if until != nil {
		v = until.Unix()
	}
	_, err := s.db.ExecContext(ctx, `UPDATE projects SET snoozed_until = ?, updated_at = ? WHERE path = ?`, v, time.Now().Unix(), path)
	return err
}

func (s *Store) SetNote(ctx context.Context, path, note string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE projects SET note = ?, updated_at = ? WHERE path = ?`, note, time.Now().Unix(), path)
	return err
}

func (s *Store) SetForgotten(ctx context.Context, path string, forgotten bool) error {
	_, err := s.db.ExecContext(ctx, `UPDATE projects SET forgotten = ?, updated_at = ? WHERE path = ?`, boolToInt(forgotten), time.Now().Unix(), path)
	return err
}

func (s *Store) SetProjectScope(ctx context.Context, path string, inScope bool) error {
	_, err := s.db.ExecContext(ctx, `UPDATE projects SET in_scope = ?, updated_at = ? WHERE path = ?`, boolToInt(inScope), time.Now().Unix(), path)
	return err
}

func splitRecentHashes(v string) []string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	return strings.FieldsFunc(v, func(r rune) bool {
		return r == '\n' || r == '\r' || r == ' ' || r == '\t'
	})
}
