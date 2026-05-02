package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
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
			kind TEXT NOT NULL DEFAULT 'project',
			last_activity INTEGER,
			status TEXT NOT NULL,
			attention_score INTEGER NOT NULL,
			present_on_disk INTEGER NOT NULL DEFAULT 1,
			worktree_root_path TEXT NOT NULL DEFAULT '',
			worktree_kind TEXT NOT NULL DEFAULT '',
			worktree_parent_branch TEXT NOT NULL DEFAULT '',
			worktree_merge_status TEXT NOT NULL DEFAULT '',
			worktree_origin_todo_id INTEGER NOT NULL DEFAULT 0,
			repo_branch TEXT NOT NULL DEFAULT '',
			repo_dirty INTEGER NOT NULL DEFAULT 0,
			repo_conflict INTEGER NOT NULL DEFAULT 0,
			repo_sync_status TEXT NOT NULL DEFAULT '',
			repo_ahead_count INTEGER NOT NULL DEFAULT 0,
			repo_behind_count INTEGER NOT NULL DEFAULT 0,
			forgotten INTEGER NOT NULL DEFAULT 0,
			manually_added INTEGER NOT NULL DEFAULT 0,
			in_scope INTEGER NOT NULL DEFAULT 1,
			pinned INTEGER NOT NULL DEFAULT 0,
			snoozed_until INTEGER,
			last_session_seen_at INTEGER,
			run_command TEXT NOT NULL DEFAULT '',
			moved_from_path TEXT NOT NULL DEFAULT '',
			moved_at INTEGER,
			created_at INTEGER,
			updated_at INTEGER NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS project_todos (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			project_path TEXT NOT NULL,
			text TEXT NOT NULL,
			done INTEGER NOT NULL DEFAULT 0,
			position INTEGER NOT NULL,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			completed_at INTEGER,
			FOREIGN KEY(project_path) REFERENCES projects(path) ON DELETE CASCADE
		);`,
		`CREATE INDEX IF NOT EXISTS idx_project_todos_project_path_position ON project_todos(project_path, done, position, id);`,
		`CREATE TABLE IF NOT EXISTS todo_worktree_suggestions (
			todo_id INTEGER PRIMARY KEY,
			status TEXT NOT NULL,
			todo_text_hash TEXT NOT NULL,
			branch_name TEXT NOT NULL DEFAULT '',
			worktree_suffix TEXT NOT NULL DEFAULT '',
			kind TEXT NOT NULL DEFAULT '',
			reason TEXT NOT NULL DEFAULT '',
			confidence REAL NOT NULL DEFAULT 0,
			model TEXT NOT NULL DEFAULT '',
			last_error TEXT NOT NULL DEFAULT '',
			updated_at INTEGER NOT NULL,
			FOREIGN KEY(todo_id) REFERENCES project_todos(id) ON DELETE CASCADE
		);`,
		`CREATE INDEX IF NOT EXISTS idx_todo_worktree_suggestions_status_updated ON todo_worktree_suggestions(status, updated_at);`,
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
			source TEXT NOT NULL DEFAULT '',
			raw_session_id TEXT NOT NULL DEFAULT '',
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
		`CREATE TABLE IF NOT EXISTS context_session_text_cache (
			session_id TEXT PRIMARY KEY,
			source TEXT NOT NULL DEFAULT '',
			raw_session_id TEXT NOT NULL DEFAULT '',
			project_path TEXT NOT NULL,
			project_name TEXT NOT NULL DEFAULT '',
			session_file TEXT NOT NULL,
			session_format TEXT NOT NULL,
			snapshot_hash TEXT NOT NULL DEFAULT '',
			source_updated_at INTEGER NOT NULL,
			latest_turn_state_known INTEGER NOT NULL DEFAULT 0,
			latest_turn_completed INTEGER NOT NULL DEFAULT 0,
			cached_at INTEGER NOT NULL,
			text TEXT NOT NULL DEFAULT ''
		);`,
		`CREATE INDEX IF NOT EXISTS idx_context_session_text_cache_project_path ON context_session_text_cache(project_path);`,
		`CREATE INDEX IF NOT EXISTS idx_context_session_text_cache_source_updated ON context_session_text_cache(source_updated_at DESC);`,
		`CREATE VIRTUAL TABLE IF NOT EXISTS context_search_fts USING fts5(
			source UNINDEXED,
			project_path UNINDEXED,
			project_name UNINDEXED,
			session_id UNINDEXED,
			title,
			body,
			updated_at UNINDEXED,
			tokenize = 'unicode61'
		);`,
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
			source TEXT NOT NULL DEFAULT '',
			raw_session_id TEXT NOT NULL DEFAULT '',
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
		`CREATE TABLE IF NOT EXISTS recent_project_parent_paths (
			parent_path TEXT PRIMARY KEY,
			last_used_at INTEGER NOT NULL
		);`,
		`CREATE INDEX IF NOT EXISTS idx_recent_project_parent_paths_last_used_at ON recent_project_parent_paths(last_used_at DESC);`,
		`CREATE TABLE IF NOT EXISTS ignored_project_names (
			name TEXT PRIMARY KEY,
			created_at INTEGER NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS agent_tasks (
			id TEXT PRIMARY KEY,
			parent_task_id TEXT NOT NULL DEFAULT '',
			title TEXT NOT NULL,
			kind TEXT NOT NULL,
			status TEXT NOT NULL,
			summary TEXT NOT NULL DEFAULT '',
			capabilities TEXT NOT NULL DEFAULT '[]',
			provider TEXT NOT NULL DEFAULT '',
			session_id TEXT NOT NULL DEFAULT '',
			workspace_path TEXT NOT NULL DEFAULT '',
			expires_at INTEGER,
			created_at INTEGER NOT NULL,
			last_touched_at INTEGER NOT NULL,
			completed_at INTEGER,
			archived_at INTEGER,
			updated_at INTEGER NOT NULL
		);`,
		`CREATE INDEX IF NOT EXISTS idx_agent_tasks_status_touched ON agent_tasks(status, last_touched_at DESC);`,
		`CREATE INDEX IF NOT EXISTS idx_agent_tasks_kind_status ON agent_tasks(kind, status);`,
		`CREATE TABLE IF NOT EXISTS agent_task_resources (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			task_id TEXT NOT NULL,
			kind TEXT NOT NULL,
			ref_id TEXT NOT NULL DEFAULT '',
			project_path TEXT NOT NULL DEFAULT '',
			path TEXT NOT NULL DEFAULT '',
			pid INTEGER NOT NULL DEFAULT 0,
			port INTEGER NOT NULL DEFAULT 0,
			provider TEXT NOT NULL DEFAULT '',
			session_id TEXT NOT NULL DEFAULT '',
			label TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL,
			FOREIGN KEY(task_id) REFERENCES agent_tasks(id) ON DELETE CASCADE
		);`,
		`CREATE INDEX IF NOT EXISTS idx_agent_task_resources_task_id ON agent_task_resources(task_id, id);`,
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
	if err := s.ensureProjectsWorktreeColumns(ctx); err != nil {
		return err
	}
	if err := s.ensureProjectsManualAddedColumn(ctx); err != nil {
		return err
	}
	if err := s.ensureProjectsMoveColumns(ctx); err != nil {
		return err
	}
	if err := s.ensureProjectsKindColumn(ctx); err != nil {
		return err
	}
	if err := s.ensureProjectsRunCommandColumn(ctx); err != nil {
		return err
	}
	if err := s.ensureProjectsLastSessionSeenColumn(ctx); err != nil {
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
	if err := s.ensureProjectSessionsIdentityColumns(ctx); err != nil {
		return err
	}
	if err := s.ensureSessionClassificationStageColumns(ctx); err != nil {
		return err
	}
	if err := s.ensureSessionClassificationIdentityColumns(ctx); err != nil {
		return err
	}
	if err := s.ensureAgentTaskMetadataColumns(ctx); err != nil {
		return err
	}
	if err := s.ensureAgentTaskResourceMetadataColumns(ctx); err != nil {
		return err
	}
	if err := s.repairTerminalSessionClassificationStages(ctx); err != nil {
		return err
	}
	if err := s.ensureProjectsCreatedAtColumn(ctx); err != nil {
		return err
	}
	return nil
}

func (s *Store) agentTaskTableColumns(ctx context.Context) (map[string]struct{}, error) {
	rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(agent_tasks)`)
	if err != nil {
		return nil, fmt.Errorf("check agent_tasks schema: %w", err)
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
			return nil, fmt.Errorf("scan agent_tasks schema: %w", err)
		}
		columns[name] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read agent_tasks schema: %w", err)
	}
	return columns, nil
}

func (s *Store) ensureAgentTaskMetadataColumns(ctx context.Context) error {
	columns, err := s.agentTaskTableColumns(ctx)
	if err != nil {
		return err
	}
	if _, ok := columns["parent_task_id"]; !ok {
		if _, err := s.db.ExecContext(ctx, `ALTER TABLE agent_tasks ADD COLUMN parent_task_id TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("add agent_tasks.parent_task_id column: %w", err)
		}
	}
	if _, ok := columns["capabilities"]; !ok {
		if _, err := s.db.ExecContext(ctx, `ALTER TABLE agent_tasks ADD COLUMN capabilities TEXT NOT NULL DEFAULT '[]'`); err != nil {
			return fmt.Errorf("add agent_tasks.capabilities column: %w", err)
		}
	}
	return nil
}

func (s *Store) agentTaskResourceTableColumns(ctx context.Context) (map[string]struct{}, error) {
	rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(agent_task_resources)`)
	if err != nil {
		return nil, fmt.Errorf("check agent_task_resources schema: %w", err)
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
			return nil, fmt.Errorf("scan agent_task_resources schema: %w", err)
		}
		columns[name] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read agent_task_resources schema: %w", err)
	}
	return columns, nil
}

func (s *Store) ensureAgentTaskResourceMetadataColumns(ctx context.Context) error {
	columns, err := s.agentTaskResourceTableColumns(ctx)
	if err != nil {
		return err
	}
	if _, ok := columns["ref_id"]; !ok {
		if _, err := s.db.ExecContext(ctx, `ALTER TABLE agent_task_resources ADD COLUMN ref_id TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("add agent_task_resources.ref_id column: %w", err)
		}
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
	if _, ok := columns["repo_branch"]; !ok {
		if _, err := s.db.ExecContext(ctx, `ALTER TABLE projects ADD COLUMN repo_branch TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("add projects.repo_branch column: %w", err)
		}
	}
	if _, ok := columns["repo_dirty"]; !ok {
		if _, err := s.db.ExecContext(ctx, `ALTER TABLE projects ADD COLUMN repo_dirty INTEGER NOT NULL DEFAULT 0`); err != nil {
			return fmt.Errorf("add projects.repo_dirty column: %w", err)
		}
	}
	if _, ok := columns["repo_conflict"]; !ok {
		if _, err := s.db.ExecContext(ctx, `ALTER TABLE projects ADD COLUMN repo_conflict INTEGER NOT NULL DEFAULT 0`); err != nil {
			return fmt.Errorf("add projects.repo_conflict column: %w", err)
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

func (s *Store) ensureProjectsKindColumn(ctx context.Context) error {
	columns, err := s.projectTableColumns(ctx)
	if err != nil {
		return err
	}
	if _, ok := columns["kind"]; ok {
		return nil
	}
	if _, err := s.db.ExecContext(ctx, `ALTER TABLE projects ADD COLUMN kind TEXT NOT NULL DEFAULT 'project'`); err != nil {
		return fmt.Errorf("add projects.kind column: %w", err)
	}
	return nil
}

func (s *Store) ensureProjectsWorktreeColumns(ctx context.Context) error {
	columns, err := s.projectTableColumns(ctx)
	if err != nil {
		return err
	}
	if _, ok := columns["worktree_root_path"]; !ok {
		if _, err := s.db.ExecContext(ctx, `ALTER TABLE projects ADD COLUMN worktree_root_path TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("add projects.worktree_root_path column: %w", err)
		}
	}
	if _, ok := columns["worktree_kind"]; !ok {
		if _, err := s.db.ExecContext(ctx, `ALTER TABLE projects ADD COLUMN worktree_kind TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("add projects.worktree_kind column: %w", err)
		}
	}
	if _, ok := columns["worktree_parent_branch"]; !ok {
		if _, err := s.db.ExecContext(ctx, `ALTER TABLE projects ADD COLUMN worktree_parent_branch TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("add projects.worktree_parent_branch column: %w", err)
		}
	}
	if _, ok := columns["worktree_merge_status"]; !ok {
		if _, err := s.db.ExecContext(ctx, `ALTER TABLE projects ADD COLUMN worktree_merge_status TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("add projects.worktree_merge_status column: %w", err)
		}
	}
	if _, ok := columns["worktree_origin_todo_id"]; !ok {
		if _, err := s.db.ExecContext(ctx, `ALTER TABLE projects ADD COLUMN worktree_origin_todo_id INTEGER NOT NULL DEFAULT 0`); err != nil {
			return fmt.Errorf("add projects.worktree_origin_todo_id column: %w", err)
		}
	}
	return nil
}

func (s *Store) ensureProjectsManualAddedColumn(ctx context.Context) error {
	columns, err := s.projectTableColumns(ctx)
	if err != nil {
		return err
	}
	if _, ok := columns["manually_added"]; ok {
		return nil
	}
	if _, err := s.db.ExecContext(ctx, `ALTER TABLE projects ADD COLUMN manually_added INTEGER NOT NULL DEFAULT 0`); err != nil {
		return fmt.Errorf("add projects.manually_added column: %w", err)
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

func (s *Store) ensureProjectsRunCommandColumn(ctx context.Context) error {
	columns, err := s.projectTableColumns(ctx)
	if err != nil {
		return err
	}
	if _, ok := columns["run_command"]; ok {
		return nil
	}
	if _, err := s.db.ExecContext(ctx, `ALTER TABLE projects ADD COLUMN run_command TEXT NOT NULL DEFAULT ''`); err != nil {
		return fmt.Errorf("add projects.run_command column: %w", err)
	}
	return nil
}

func (s *Store) ensureProjectsLastSessionSeenColumn(ctx context.Context) error {
	columns, err := s.projectTableColumns(ctx)
	if err != nil {
		return err
	}
	if _, ok := columns["last_session_seen_at"]; ok {
		return nil
	}
	if _, err := s.db.ExecContext(ctx, `ALTER TABLE projects ADD COLUMN last_session_seen_at INTEGER`); err != nil {
		return fmt.Errorf("add projects.last_session_seen_at column: %w", err)
	}
	return nil
}

func (s *Store) ensureProjectsCreatedAtColumn(ctx context.Context) error {
	columns, err := s.projectTableColumns(ctx)
	if err != nil {
		return err
	}
	if _, ok := columns["created_at"]; ok {
		return nil
	}
	if _, err := s.db.ExecContext(ctx, `ALTER TABLE projects ADD COLUMN created_at INTEGER`); err != nil {
		return fmt.Errorf("add projects.created_at column: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE projects SET created_at = COALESCE(updated_at, strftime('%s', 'now')) WHERE created_at IS NULL`); err != nil {
		return fmt.Errorf("backfill projects.created_at: %w", err)
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

func (s *Store) ensureProjectSessionsIdentityColumns(ctx context.Context) error {
	columns, err := s.projectSessionsTableColumns(ctx)
	if err != nil {
		return err
	}
	if _, ok := columns["source"]; !ok {
		if _, err := s.db.ExecContext(ctx, `ALTER TABLE project_sessions ADD COLUMN source TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("add project_sessions.source column: %w", err)
		}
	}
	if _, ok := columns["raw_session_id"]; !ok {
		if _, err := s.db.ExecContext(ctx, `ALTER TABLE project_sessions ADD COLUMN raw_session_id TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("add project_sessions.raw_session_id column: %w", err)
		}
	}
	return s.normalizeProjectSessionIdentityColumns(ctx)
}

func (s *Store) normalizeProjectSessionIdentityColumns(ctx context.Context) error {
	rows, err := s.loadProjectSessionMigrationRows(ctx)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback()
	}()
	for _, row := range rows {
		if !row.needsIdentityUpdate() {
			continue
		}
		if err := s.applyProjectSessionIdentityUpdate(ctx, tx, row); err != nil {
			return fmt.Errorf("normalize project_sessions identity %s: %w", row.originalSessionID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return err
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

func (s *Store) ensureSessionClassificationIdentityColumns(ctx context.Context) error {
	columns, err := s.sessionClassificationTableColumns(ctx)
	if err != nil {
		return err
	}
	if _, ok := columns["source"]; !ok {
		if _, err := s.db.ExecContext(ctx, `ALTER TABLE session_classifications ADD COLUMN source TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("add session_classifications.source column: %w", err)
		}
	}
	if _, ok := columns["raw_session_id"]; !ok {
		if _, err := s.db.ExecContext(ctx, `ALTER TABLE session_classifications ADD COLUMN raw_session_id TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("add session_classifications.raw_session_id column: %w", err)
		}
	}
	return s.normalizeSessionClassificationIdentityColumns(ctx)
}

func (s *Store) normalizeSessionClassificationIdentityColumns(ctx context.Context) error {
	rows, err := s.loadSessionClassificationMigrationRows(ctx)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback()
	}()
	for _, row := range rows {
		if !row.needsIdentityUpdate() {
			continue
		}
		if err := s.applySessionClassificationIdentityUpdate(ctx, tx, row); err != nil {
			return fmt.Errorf("normalize session_classifications identity %s: %w", row.originalSessionID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}

type projectSessionMigrationRow struct {
	originalSessionID    string
	originalSource       string
	originalRawSessionID string
	session              model.SessionEvidence
	updatedAt            time.Time
}

type normalizedSessionIdentity struct {
	sessionID    string
	source       model.SessionSource
	rawSessionID string
}

func normalizedSessionIdentityFrom(originalSessionID, originalSource, originalRawSessionID string) normalizedSessionIdentity {
	return normalizedSessionIdentity{
		sessionID:    strings.TrimSpace(originalSessionID),
		source:       model.NormalizeSessionSource(model.SessionSource(strings.TrimSpace(originalSource))),
		rawSessionID: strings.TrimSpace(originalRawSessionID),
	}
}

func needsNormalizedSessionIdentityUpdate(originalSessionID, originalSource, originalRawSessionID string, normalized normalizedSessionIdentity) bool {
	return strings.TrimSpace(originalSessionID) != normalized.sessionID ||
		strings.TrimSpace(originalSource) != string(normalized.source) ||
		strings.TrimSpace(originalRawSessionID) != normalized.rawSessionID
}

func loadOptionalMigrationRow[T any](
	ctx context.Context,
	tx *sql.Tx,
	query string,
	scan func(interface{ Scan(dest ...any) error }) (T, error),
	args ...any,
) (T, bool, error) {
	row := tx.QueryRowContext(ctx, query, args...)
	result, err := scan(row)
	if err != nil {
		var zero T
		if errors.Is(err, sql.ErrNoRows) {
			return zero, false, nil
		}
		return zero, false, err
	}
	return result, true, nil
}

func (row projectSessionMigrationRow) needsIdentityUpdate() bool {
	return needsNormalizedSessionIdentityUpdate(
		row.originalSessionID,
		row.originalSource,
		row.originalRawSessionID,
		normalizedSessionIdentity{
			sessionID:    row.session.SessionID,
			source:       row.session.Source,
			rawSessionID: row.session.RawSessionID,
		},
	)
}

func (s *Store) loadProjectSessionMigrationRows(ctx context.Context) ([]projectSessionMigrationRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			session_id, source, raw_session_id, project_path, detected_project_path, session_file, format,
			snapshot_hash, started_at, last_event_at, error_count, latest_turn_started_at,
			latest_turn_state_known, latest_turn_completed, updated_at
		FROM project_sessions
	`)
	if err != nil {
		return nil, fmt.Errorf("load project_sessions identities: %w", err)
	}
	defer rows.Close()

	out := []projectSessionMigrationRow{}
	for rows.Next() {
		row, err := scanProjectSessionMigrationRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan project_sessions identity: %w", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read project_sessions identities: %w", err)
	}
	return out, nil
}

func scanProjectSessionMigrationRow(scanner interface {
	Scan(dest ...any) error
}) (projectSessionMigrationRow, error) {
	var (
		row                 projectSessionMigrationRow
		startedAt           sql.NullInt64
		lastEventAt         int64
		latestTurnStartedAt sql.NullInt64
		latestTurnKnown     int
		latestTurnDone      int
		updatedAt           int64
	)
	if err := scanner.Scan(
		&row.originalSessionID,
		&row.originalSource,
		&row.originalRawSessionID,
		&row.session.ProjectPath,
		&row.session.DetectedProjectPath,
		&row.session.SessionFile,
		&row.session.Format,
		&row.session.SnapshotHash,
		&startedAt,
		&lastEventAt,
		&row.session.ErrorCount,
		&latestTurnStartedAt,
		&latestTurnKnown,
		&latestTurnDone,
		&updatedAt,
	); err != nil {
		return projectSessionMigrationRow{}, err
	}
	identity := normalizedSessionIdentityFrom(row.originalSessionID, row.originalSource, row.originalRawSessionID)
	row.session.Source = identity.source
	row.session.SessionID = identity.sessionID
	row.session.RawSessionID = identity.rawSessionID
	if startedAt.Valid {
		row.session.StartedAt = time.Unix(startedAt.Int64, 0)
	}
	row.session.LastEventAt = time.Unix(lastEventAt, 0)
	if latestTurnStartedAt.Valid {
		row.session.LatestTurnStartedAt = time.Unix(latestTurnStartedAt.Int64, 0)
	}
	row.session.LatestTurnStateKnown = latestTurnKnown != 0
	row.session.LatestTurnCompleted = latestTurnDone != 0
	row.updatedAt = time.Unix(updatedAt, 0)
	row.session = model.NormalizeSessionEvidenceIdentity(row.session)
	return row, nil
}

func (s *Store) applyProjectSessionIdentityUpdate(ctx context.Context, tx *sql.Tx, row projectSessionMigrationRow) error {
	targetID := row.session.SessionID
	if targetID == "" {
		return nil
	}
	if row.originalSessionID == targetID {
		return s.updateProjectSessionIdentityMetadata(ctx, tx, row)
	}

	existing, ok, err := s.loadProjectSessionMigrationRowByID(ctx, tx, targetID)
	if err != nil {
		return err
	}
	if !ok {
		_, err := tx.ExecContext(ctx, `
			UPDATE project_sessions
			SET session_id = ?, source = ?, raw_session_id = ?
			WHERE session_id = ?
		`, targetID, string(row.session.Source), row.session.RawSessionID, row.originalSessionID)
		return err
	}

	merged := mergeProjectSessionMigrationRows(existing, row)
	merged.originalSessionID = targetID
	if err := s.writeProjectSessionMigrationRow(ctx, tx, merged); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM project_sessions WHERE session_id = ?`, row.originalSessionID)
	return err
}

func (s *Store) updateProjectSessionIdentityMetadata(ctx context.Context, tx *sql.Tx, row projectSessionMigrationRow) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE project_sessions
		SET source = ?, raw_session_id = ?
		WHERE session_id = ?
	`, string(row.session.Source), row.session.RawSessionID, row.originalSessionID)
	return err
}

func (s *Store) loadProjectSessionMigrationRowByID(ctx context.Context, tx *sql.Tx, sessionID string) (projectSessionMigrationRow, bool, error) {
	return loadOptionalMigrationRow(ctx, tx, `
		SELECT
			session_id, source, raw_session_id, project_path, detected_project_path, session_file, format,
			snapshot_hash, started_at, last_event_at, error_count, latest_turn_started_at,
			latest_turn_state_known, latest_turn_completed, updated_at
		FROM project_sessions
		WHERE session_id = ?
	`, scanProjectSessionMigrationRow, sessionID)
}

func mergeProjectSessionMigrationRows(left, right projectSessionMigrationRow) projectSessionMigrationRow {
	preferred := left
	other := right
	if preferProjectSessionMigrationRow(right, left) {
		preferred = right
		other = left
	}
	merged := preferred
	merged.session.Source = preferred.session.Source
	merged.session.SessionID = preferred.session.SessionID
	merged.session.RawSessionID = preferred.session.RawSessionID
	if merged.session.ProjectPath == "" {
		merged.session.ProjectPath = other.session.ProjectPath
	}
	if merged.session.DetectedProjectPath == "" {
		merged.session.DetectedProjectPath = other.session.DetectedProjectPath
	}
	if merged.session.SessionFile == "" {
		merged.session.SessionFile = other.session.SessionFile
	}
	if merged.session.Format == "" {
		merged.session.Format = other.session.Format
	}
	if merged.session.SnapshotHash == "" {
		merged.session.SnapshotHash = other.session.SnapshotHash
	}
	if merged.session.StartedAt.IsZero() || (!other.session.StartedAt.IsZero() && other.session.StartedAt.Before(merged.session.StartedAt)) {
		merged.session.StartedAt = other.session.StartedAt
	}
	if other.session.LastEventAt.After(merged.session.LastEventAt) {
		merged.session.LastEventAt = other.session.LastEventAt
	}
	if other.session.ErrorCount > merged.session.ErrorCount {
		merged.session.ErrorCount = other.session.ErrorCount
	}
	if !merged.session.LatestTurnStateKnown && other.session.LatestTurnStateKnown {
		merged.session.LatestTurnStateKnown = true
		merged.session.LatestTurnCompleted = other.session.LatestTurnCompleted
		merged.session.LatestTurnStartedAt = other.session.LatestTurnStartedAt
	}
	if merged.session.LatestTurnStartedAt.IsZero() && !other.session.LatestTurnStartedAt.IsZero() {
		merged.session.LatestTurnStartedAt = other.session.LatestTurnStartedAt
	}
	if other.updatedAt.After(merged.updatedAt) {
		merged.updatedAt = other.updatedAt
	}
	return merged
}

func preferProjectSessionMigrationRow(candidate, existing projectSessionMigrationRow) bool {
	if candidate.session.LastEventAt.After(existing.session.LastEventAt) {
		return true
	}
	if existing.session.LastEventAt.After(candidate.session.LastEventAt) {
		return false
	}
	if candidate.updatedAt.After(existing.updatedAt) {
		return true
	}
	if existing.updatedAt.After(candidate.updatedAt) {
		return false
	}
	candidateScore := projectSessionMigrationScore(candidate)
	existingScore := projectSessionMigrationScore(existing)
	if candidateScore != existingScore {
		return candidateScore > existingScore
	}
	return strings.Compare(candidate.originalSessionID, existing.originalSessionID) < 0
}

func projectSessionMigrationScore(row projectSessionMigrationRow) int {
	score := 0
	if row.session.Source != model.SessionSourceUnknown {
		score += 2
	}
	if row.session.RawSessionID != "" {
		score += 2
	}
	if row.session.SnapshotHash != "" {
		score += 2
	}
	if row.session.SessionFile != "" {
		score += 2
	}
	if row.session.DetectedProjectPath != "" {
		score++
	}
	if row.session.LatestTurnStateKnown {
		score++
	}
	return score
}

func (s *Store) writeProjectSessionMigrationRow(ctx context.Context, tx *sql.Tx, row projectSessionMigrationRow) error {
	var startedAt any
	if !row.session.StartedAt.IsZero() {
		startedAt = row.session.StartedAt.Unix()
	}
	var latestTurnStartedAt any
	if !row.session.LatestTurnStartedAt.IsZero() {
		latestTurnStartedAt = row.session.LatestTurnStartedAt.Unix()
	}
	_, err := tx.ExecContext(ctx, `
		UPDATE project_sessions
		SET source = ?,
			raw_session_id = ?,
			project_path = ?,
			detected_project_path = ?,
			session_file = ?,
			format = ?,
			snapshot_hash = ?,
			started_at = ?,
			last_event_at = ?,
			error_count = ?,
			latest_turn_started_at = ?,
			latest_turn_state_known = ?,
			latest_turn_completed = ?,
			updated_at = ?
		WHERE session_id = ?
	`, string(row.session.Source), row.session.RawSessionID, row.session.ProjectPath, row.session.DetectedProjectPath, row.session.SessionFile, row.session.Format, row.session.SnapshotHash, startedAt, row.session.LastEventAt.Unix(), row.session.ErrorCount, latestTurnStartedAt, boolToInt(row.session.LatestTurnStateKnown), boolToInt(row.session.LatestTurnCompleted), row.updatedAt.Unix(), row.session.SessionID)
	return err
}

type sessionClassificationMigrationRow struct {
	originalSessionID    string
	originalSource       string
	originalRawSessionID string
	classification       model.SessionClassification
}

func (row sessionClassificationMigrationRow) needsIdentityUpdate() bool {
	return needsNormalizedSessionIdentityUpdate(
		row.originalSessionID,
		row.originalSource,
		row.originalRawSessionID,
		normalizedSessionIdentity{
			sessionID:    row.classification.SessionID,
			source:       row.classification.Source,
			rawSessionID: row.classification.RawSessionID,
		},
	)
}

func (s *Store) loadSessionClassificationMigrationRows(ctx context.Context) ([]sessionClassificationMigrationRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			session_id, source, raw_session_id, project_path, session_file, session_format, snapshot_hash,
			status, stage, category, summary, confidence, model, classifier_version,
			last_error, source_updated_at, created_at, stage_started_at, updated_at, completed_at
		FROM session_classifications
	`)
	if err != nil {
		return nil, fmt.Errorf("load session_classifications identities: %w", err)
	}
	defer rows.Close()

	out := []sessionClassificationMigrationRow{}
	for rows.Next() {
		row, err := scanSessionClassificationMigrationRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan session_classifications identity: %w", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read session_classifications identities: %w", err)
	}
	return out, nil
}

func scanSessionClassificationMigrationRow(scanner interface {
	Scan(dest ...any) error
}) (sessionClassificationMigrationRow, error) {
	var (
		row             sessionClassificationMigrationRow
		status, stage   string
		category        string
		sourceUpdatedAt int64
		createdAt       int64
		updatedAt       int64
		stageStartedAt  sql.NullInt64
		completedAt     sql.NullInt64
	)
	if err := scanner.Scan(
		&row.originalSessionID,
		&row.originalSource,
		&row.originalRawSessionID,
		&row.classification.ProjectPath,
		&row.classification.SessionFile,
		&row.classification.SessionFormat,
		&row.classification.SnapshotHash,
		&status,
		&stage,
		&category,
		&row.classification.Summary,
		&row.classification.Confidence,
		&row.classification.Model,
		&row.classification.ClassifierVersion,
		&row.classification.LastError,
		&sourceUpdatedAt,
		&createdAt,
		&stageStartedAt,
		&updatedAt,
		&completedAt,
	); err != nil {
		return sessionClassificationMigrationRow{}, err
	}
	identity := normalizedSessionIdentityFrom(row.originalSessionID, row.originalSource, row.originalRawSessionID)
	row.classification.Source = identity.source
	row.classification.SessionID = identity.sessionID
	row.classification.RawSessionID = identity.rawSessionID
	row.classification.Status = model.SessionClassificationStatus(status)
	row.classification.Stage = model.SessionClassificationStage(stage)
	row.classification.Category = model.SessionCategory(category)
	row.classification.SourceUpdatedAt = time.Unix(sourceUpdatedAt, 0)
	row.classification.CreatedAt = time.Unix(createdAt, 0)
	row.classification.UpdatedAt = time.Unix(updatedAt, 0)
	if stageStartedAt.Valid {
		row.classification.StageStartedAt = time.Unix(stageStartedAt.Int64, 0)
	}
	if completedAt.Valid {
		row.classification.CompletedAt = time.Unix(completedAt.Int64, 0)
	}
	row.classification = model.NormalizeSessionClassificationIdentity(row.classification)
	return row, nil
}

func (s *Store) applySessionClassificationIdentityUpdate(ctx context.Context, tx *sql.Tx, row sessionClassificationMigrationRow) error {
	targetID := row.classification.SessionID
	if targetID == "" {
		return nil
	}
	if row.originalSessionID == targetID {
		return s.updateSessionClassificationIdentityMetadata(ctx, tx, row)
	}

	existing, ok, err := s.loadSessionClassificationMigrationRowByID(ctx, tx, targetID)
	if err != nil {
		return err
	}
	if !ok {
		_, err := tx.ExecContext(ctx, `
			UPDATE session_classifications
			SET session_id = ?, source = ?, raw_session_id = ?
			WHERE session_id = ?
		`, targetID, string(row.classification.Source), row.classification.RawSessionID, row.originalSessionID)
		return err
	}

	merged := mergeSessionClassificationMigrationRows(existing, row)
	merged.originalSessionID = targetID
	if err := s.writeSessionClassificationMigrationRow(ctx, tx, merged); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM session_classifications WHERE session_id = ?`, row.originalSessionID)
	return err
}

func (s *Store) updateSessionClassificationIdentityMetadata(ctx context.Context, tx *sql.Tx, row sessionClassificationMigrationRow) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE session_classifications
		SET source = ?, raw_session_id = ?
		WHERE session_id = ?
	`, string(row.classification.Source), row.classification.RawSessionID, row.originalSessionID)
	return err
}

func (s *Store) loadSessionClassificationMigrationRowByID(ctx context.Context, tx *sql.Tx, sessionID string) (sessionClassificationMigrationRow, bool, error) {
	return loadOptionalMigrationRow(ctx, tx, `
		SELECT
			session_id, source, raw_session_id, project_path, session_file, session_format, snapshot_hash,
			status, stage, category, summary, confidence, model, classifier_version,
			last_error, source_updated_at, created_at, stage_started_at, updated_at, completed_at
		FROM session_classifications
		WHERE session_id = ?
	`, scanSessionClassificationMigrationRow, sessionID)
}

func mergeSessionClassificationMigrationRows(left, right sessionClassificationMigrationRow) sessionClassificationMigrationRow {
	preferred := left
	other := right
	if preferSessionClassificationMigrationRow(right, left) {
		preferred = right
		other = left
	}
	merged := preferred
	merged.classification.Source = preferred.classification.Source
	merged.classification.SessionID = preferred.classification.SessionID
	merged.classification.RawSessionID = preferred.classification.RawSessionID
	if merged.classification.ProjectPath == "" {
		merged.classification.ProjectPath = other.classification.ProjectPath
	}
	if merged.classification.SessionFile == "" {
		merged.classification.SessionFile = other.classification.SessionFile
	}
	if merged.classification.SessionFormat == "" {
		merged.classification.SessionFormat = other.classification.SessionFormat
	}
	if merged.classification.SnapshotHash == "" {
		merged.classification.SnapshotHash = other.classification.SnapshotHash
	}
	if merged.classification.Summary == "" {
		merged.classification.Summary = other.classification.Summary
	}
	if merged.classification.Category == "" {
		merged.classification.Category = other.classification.Category
	}
	if merged.classification.Model == "" {
		merged.classification.Model = other.classification.Model
	}
	if merged.classification.ClassifierVersion == "" {
		merged.classification.ClassifierVersion = other.classification.ClassifierVersion
	}
	if merged.classification.LastError == "" {
		merged.classification.LastError = other.classification.LastError
	}
	if merged.classification.Confidence == 0 && other.classification.Confidence > 0 {
		merged.classification.Confidence = other.classification.Confidence
	}
	if merged.classification.SourceUpdatedAt.IsZero() || other.classification.SourceUpdatedAt.After(merged.classification.SourceUpdatedAt) {
		merged.classification.SourceUpdatedAt = other.classification.SourceUpdatedAt
	}
	if merged.classification.CreatedAt.IsZero() || (!other.classification.CreatedAt.IsZero() && other.classification.CreatedAt.Before(merged.classification.CreatedAt)) {
		merged.classification.CreatedAt = other.classification.CreatedAt
	}
	if merged.classification.StageStartedAt.IsZero() && !other.classification.StageStartedAt.IsZero() {
		merged.classification.StageStartedAt = other.classification.StageStartedAt
	}
	if merged.classification.UpdatedAt.IsZero() || other.classification.UpdatedAt.After(merged.classification.UpdatedAt) {
		merged.classification.UpdatedAt = other.classification.UpdatedAt
	}
	if merged.classification.CompletedAt.IsZero() || (!other.classification.CompletedAt.IsZero() && other.classification.CompletedAt.After(merged.classification.CompletedAt)) {
		merged.classification.CompletedAt = other.classification.CompletedAt
	}
	return merged
}

func preferSessionClassificationMigrationRow(candidate, existing sessionClassificationMigrationRow) bool {
	if candidate.classification.UpdatedAt.After(existing.classification.UpdatedAt) {
		return true
	}
	if existing.classification.UpdatedAt.After(candidate.classification.UpdatedAt) {
		return false
	}
	candidateScore := sessionClassificationMigrationScore(candidate)
	existingScore := sessionClassificationMigrationScore(existing)
	if candidateScore != existingScore {
		return candidateScore > existingScore
	}
	return strings.Compare(candidate.originalSessionID, existing.originalSessionID) < 0
}

func sessionClassificationMigrationScore(row sessionClassificationMigrationRow) int {
	score := 0
	if row.classification.Source != model.SessionSourceUnknown {
		score += 2
	}
	if row.classification.RawSessionID != "" {
		score += 2
	}
	if row.classification.SnapshotHash != "" {
		score += 2
	}
	if row.classification.Summary != "" {
		score += 2
	}
	if row.classification.Category != "" {
		score++
	}
	switch row.classification.Status {
	case model.ClassificationCompleted:
		score += 5
	case model.ClassificationRunning:
		score += 4
	case model.ClassificationPending:
		score += 3
	case model.ClassificationFailed:
		score += 2
	}
	if !row.classification.CompletedAt.IsZero() {
		score++
	}
	return score
}

func (s *Store) writeSessionClassificationMigrationRow(ctx context.Context, tx *sql.Tx, row sessionClassificationMigrationRow) error {
	var stageStartedAt any
	if !row.classification.StageStartedAt.IsZero() {
		stageStartedAt = row.classification.StageStartedAt.Unix()
	}
	var completedAt any
	if !row.classification.CompletedAt.IsZero() {
		completedAt = row.classification.CompletedAt.Unix()
	}
	_, err := tx.ExecContext(ctx, `
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
			confidence = ?,
			model = ?,
			classifier_version = ?,
			last_error = ?,
			source_updated_at = ?,
			created_at = ?,
			stage_started_at = ?,
			updated_at = ?,
			completed_at = ?
		WHERE session_id = ?
	`, string(row.classification.Source), row.classification.RawSessionID, row.classification.ProjectPath, row.classification.SessionFile, row.classification.SessionFormat, row.classification.SnapshotHash, string(row.classification.Status), string(row.classification.Stage), string(row.classification.Category), row.classification.Summary, row.classification.Confidence, row.classification.Model, row.classification.ClassifierVersion, row.classification.LastError, row.classification.SourceUpdatedAt.Unix(), row.classification.CreatedAt.Unix(), stageStartedAt, row.classification.UpdatedAt.Unix(), completedAt, row.classification.SessionID)
	return err
}

func projectSummaryBaseQuery() string {
	return fmt.Sprintf(`
		SELECT
			p.path, p.name, p.kind, p.last_activity, p.status, p.attention_score, p.present_on_disk, p.worktree_root_path, p.worktree_kind, p.worktree_parent_branch, p.worktree_merge_status, p.worktree_origin_todo_id, p.repo_branch, p.repo_dirty, p.repo_conflict, p.repo_sync_status, p.repo_ahead_count, p.repo_behind_count, p.forgotten, p.manually_added, p.in_scope, p.pinned, p.snoozed_until, p.last_session_seen_at, p.created_at,
			COALESCE((SELECT COUNT(*) FROM project_todos pt WHERE pt.project_path = p.path AND pt.done = 0), 0),
			COALESCE((SELECT COUNT(*) FROM project_todos pt WHERE pt.project_path = p.path), 0),
			p.run_command,
			p.moved_from_path, p.moved_at,
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
	`, preferredSessionClassificationIDExpr("ps.session_id"), sessionClassificationCanonicalRankExpr("sc2"))
}

func projectSummaryVisibilityConditions(includeHistorical bool) string {
	conditions := `p.forgotten = 0
		AND NOT EXISTS (
			SELECT 1
			FROM ignored_project_names ipn
			WHERE LOWER(ipn.name) = LOWER(p.name)
		)`
	if !includeHistorical {
		conditions += ` AND p.in_scope = 1`
	}
	return conditions
}

func projectSummaryVisibilityFilter(includeHistorical bool) string {
	return ` WHERE ` + projectSummaryVisibilityConditions(includeHistorical)
}

func (s *Store) GetProjectSummaryMap(ctx context.Context) (map[string]model.ProjectSummary, error) {
	query := projectSummaryBaseQuery()
	rows, err := s.db.QueryContext(ctx, query)
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
	query := projectSummaryBaseQuery()
	query += projectSummaryVisibilityFilter(includeHistorical)
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

func (s *Store) GetProjectSummary(ctx context.Context, projectPath string, includeHistorical bool) (model.ProjectSummary, error) {
	query := projectSummaryBaseQuery()
	query += ` WHERE p.path = ?
		AND ` + projectSummaryVisibilityConditions(includeHistorical)

	row := s.db.QueryRowContext(ctx, query, projectPath)
	summary, err := scanSummaryRow(row)
	if err != nil {
		return model.ProjectSummary{}, err
	}
	return summary, nil
}

func scanSummaryRow(scanner interface {
	Scan(dest ...any) error
}) (model.ProjectSummary, error) {
	var (
		path, name, kind, status, runCommand, movedFromPath, repoBranch, worktreeRootPath    string
		worktreeParentBranch, worktreeMergeStatus                                            string
		worktreeKind                                                                         string
		worktreeOriginTodoID                                                                 int64
		lastActivity, snoozedUntil, lastSessionSeenAt, createdAt, movedAt                    sql.NullInt64
		latestSessionLastEventAt, latestTurnStartedAt                                        sql.NullInt64
		latestSessionID, latestSessionSource, latestRawSessionID                             sql.NullString
		latestSessionFormat, latestSessionDetectedPath, latestSessionSnapshotHash            sql.NullString
		latestClassificationStatus                                                           sql.NullString
		latestClassificationStage, latestClassificationCategory, latestClassificationSummary sql.NullString
		latestClassificationStageStartedAt, latestClassificationUpdatedAt                    sql.NullInt64
		latestCompletedClassificationCategory, latestCompletedClassificationSummary          sql.NullString
		latestCompletedClassificationUpdatedAt                                               sql.NullInt64
		repoSyncStatus                                                                       string
		attentionScore, repoAheadCount, repoBehindCount, openTODOCount, totalTODOCount       int
		latestTurnKnown, latestTurnCompleted                                                 int
		presentOnDisk, repoDirty, repoConflict, forgotten, manuallyAdded, inScope, pinned    int
	)
	if err := scanner.Scan(
		&path,
		&name,
		&kind,
		&lastActivity,
		&status,
		&attentionScore,
		&presentOnDisk,
		&worktreeRootPath,
		&worktreeKind,
		&worktreeParentBranch,
		&worktreeMergeStatus,
		&worktreeOriginTodoID,
		&repoBranch,
		&repoDirty,
		&repoConflict,
		&repoSyncStatus,
		&repoAheadCount,
		&repoBehindCount,
		&forgotten,
		&manuallyAdded,
		&inScope,
		&pinned,
		&snoozedUntil,
		&lastSessionSeenAt,
		&createdAt,
		&openTODOCount,
		&totalTODOCount,
		&runCommand,
		&movedFromPath,
		&movedAt,
		&latestSessionID,
		&latestSessionSource,
		&latestRawSessionID,
		&latestSessionFormat,
		&latestSessionDetectedPath,
		&latestSessionSnapshotHash,
		&latestSessionLastEventAt,
		&latestTurnStartedAt,
		&latestTurnKnown,
		&latestTurnCompleted,
		&latestClassificationStatus,
		&latestClassificationStage,
		&latestClassificationCategory,
		&latestClassificationSummary,
		&latestClassificationStageStartedAt,
		&latestClassificationUpdatedAt,
		&latestCompletedClassificationCategory,
		&latestCompletedClassificationSummary,
		&latestCompletedClassificationUpdatedAt,
	); err != nil {
		return model.ProjectSummary{}, err
	}
	normalizedSource, normalizedSessionID, normalizedRawSessionID := model.NormalizeSessionIdentity(
		model.SessionSource(latestSessionSource.String),
		latestSessionFormat.String,
		latestSessionID.String,
		latestRawSessionID.String,
	)
	p := model.ProjectSummary{
		Path:                                     path,
		Name:                                     name,
		Kind:                                     model.NormalizeProjectKind(model.ProjectKind(kind)),
		Status:                                   model.ProjectStatus(status),
		AttentionScore:                           attentionScore,
		PresentOnDisk:                            presentOnDisk == 1,
		WorktreeRootPath:                         strings.TrimSpace(worktreeRootPath),
		WorktreeKind:                             model.WorktreeKind(strings.TrimSpace(worktreeKind)),
		WorktreeParentBranch:                     strings.TrimSpace(worktreeParentBranch),
		WorktreeMergeStatus:                      model.WorktreeMergeStatus(strings.TrimSpace(worktreeMergeStatus)),
		WorktreeOriginTodoID:                     worktreeOriginTodoID,
		RepoBranch:                               strings.TrimSpace(repoBranch),
		RepoDirty:                                repoDirty == 1,
		RepoConflict:                             repoConflict == 1,
		RepoSyncStatus:                           model.RepoSyncStatus(repoSyncStatus),
		RepoAheadCount:                           repoAheadCount,
		RepoBehindCount:                          repoBehindCount,
		Forgotten:                                forgotten == 1,
		ManuallyAdded:                            manuallyAdded == 1,
		InScope:                                  inScope == 1,
		Pinned:                                   pinned == 1,
		OpenTODOCount:                            openTODOCount,
		TotalTODOCount:                           totalTODOCount,
		RunCommand:                               runCommand,
		MovedFromPath:                            movedFromPath,
		LatestSessionSource:                      normalizedSource,
		LatestSessionID:                          normalizedSessionID,
		LatestRawSessionID:                       normalizedRawSessionID,
		LatestSessionFormat:                      latestSessionFormat.String,
		LatestSessionDetectedProjectPath:         latestSessionDetectedPath.String,
		LatestSessionSnapshotHash:                latestSessionSnapshotHash.String,
		LatestTurnStateKnown:                     latestTurnKnown != 0,
		LatestTurnCompleted:                      latestTurnCompleted != 0,
		LatestSessionClassification:              model.SessionClassificationStatus(latestClassificationStatus.String),
		LatestSessionClassificationStage:         model.SessionClassificationStage(latestClassificationStage.String),
		LatestSessionClassificationType:          model.SessionCategory(latestClassificationCategory.String),
		LatestSessionSummary:                     latestClassificationSummary.String,
		LatestCompletedSessionClassificationType: model.SessionCategory(latestCompletedClassificationCategory.String),
		LatestCompletedSessionSummary:            latestCompletedClassificationSummary.String,
	}
	if lastActivity.Valid {
		p.LastActivity = time.Unix(lastActivity.Int64, 0)
	}
	if snoozedUntil.Valid {
		t := time.Unix(snoozedUntil.Int64, 0)
		p.SnoozedUntil = &t
	}
	if lastSessionSeenAt.Valid {
		p.LastSessionSeenAt = time.Unix(lastSessionSeenAt.Int64, 0)
	}
	if createdAt.Valid {
		p.CreatedAt = time.Unix(createdAt.Int64, 0)
	}
	if movedAt.Valid {
		p.MovedAt = time.Unix(movedAt.Int64, 0)
	}
	if latestSessionLastEventAt.Valid {
		p.LatestSessionLastEventAt = time.Unix(latestSessionLastEventAt.Int64, 0)
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
	if latestCompletedClassificationUpdatedAt.Valid {
		p.LatestCompletedSessionClassificationUpdatedAt = time.Unix(latestCompletedClassificationUpdatedAt.Int64, 0)
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
	state.Kind = model.NormalizeProjectKind(state.Kind)
	if state.UpdatedAt.IsZero() {
		state.UpdatedAt = time.Now()
	}

	var (
		lastActivity any
		snoozedUntil any
		movedAt      any
		createdAt    any
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
	if !state.CreatedAt.IsZero() {
		createdAt = state.CreatedAt.Unix()
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO projects(path, name, kind, last_activity, status, attention_score, present_on_disk, worktree_root_path, worktree_kind, worktree_parent_branch, worktree_merge_status, worktree_origin_todo_id, repo_branch, repo_dirty, repo_conflict, repo_sync_status, repo_ahead_count, repo_behind_count, forgotten, manually_added, in_scope, pinned, snoozed_until, moved_from_path, moved_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET
			name=excluded.name,
			kind=excluded.kind,
			last_activity=excluded.last_activity,
			status=excluded.status,
			attention_score=excluded.attention_score,
			present_on_disk=excluded.present_on_disk,
			worktree_root_path=excluded.worktree_root_path,
			worktree_kind=excluded.worktree_kind,
			worktree_parent_branch=CASE
				WHEN excluded.worktree_parent_branch != '' THEN excluded.worktree_parent_branch
				ELSE projects.worktree_parent_branch
			END,
			worktree_merge_status=excluded.worktree_merge_status,
			worktree_origin_todo_id=CASE
				WHEN excluded.worktree_origin_todo_id > 0 THEN excluded.worktree_origin_todo_id
				ELSE projects.worktree_origin_todo_id
			END,
			repo_branch=excluded.repo_branch,
			repo_dirty=excluded.repo_dirty,
			repo_conflict=excluded.repo_conflict,
			repo_sync_status=excluded.repo_sync_status,
			repo_ahead_count=excluded.repo_ahead_count,
			repo_behind_count=excluded.repo_behind_count,
			forgotten=CASE
				WHEN excluded.present_on_disk = 0 AND projects.present_on_disk = 0 AND projects.forgotten = 1 THEN 1
				ELSE excluded.forgotten
			END,
			manually_added=excluded.manually_added,
			in_scope=excluded.in_scope,
			pinned=projects.pinned,
			snoozed_until=projects.snoozed_until,
			moved_from_path=CASE
				WHEN excluded.moved_from_path != '' THEN excluded.moved_from_path
				ELSE projects.moved_from_path
			END,
			moved_at=CASE
				WHEN excluded.moved_at IS NOT NULL THEN excluded.moved_at
				ELSE projects.moved_at
			END,
			created_at=COALESCE(projects.created_at, excluded.created_at),
			updated_at=excluded.updated_at
	`, state.Path, state.Name, string(state.Kind), lastActivity, string(state.Status), state.AttentionScore, boolToInt(state.PresentOnDisk), strings.TrimSpace(state.WorktreeRootPath), string(state.WorktreeKind), strings.TrimSpace(state.WorktreeParentBranch), string(state.WorktreeMergeStatus), state.WorktreeOriginTodoID, strings.TrimSpace(state.RepoBranch), boolToInt(state.RepoDirty), boolToInt(state.RepoConflict), string(state.RepoSyncStatus), state.RepoAheadCount, state.RepoBehindCount, boolToInt(state.Forgotten), boolToInt(state.ManuallyAdded), boolToInt(state.InScope), boolToInt(state.Pinned), snoozedUntil, state.MovedFromPath, movedAt, createdAt, state.UpdatedAt.Unix())
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
		session = model.NormalizeSessionEvidenceIdentity(session)
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
			INSERT INTO project_sessions(session_id, source, raw_session_id, project_path, detected_project_path, session_file, format, snapshot_hash, started_at, last_event_at, error_count, latest_turn_started_at, latest_turn_state_known, latest_turn_completed, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, session.SessionID, string(session.Source), session.RawSessionID, state.Path, detectedPath, session.SessionFile, session.Format, session.SnapshotHash, startedAt, session.LastEventAt.Unix(), session.ErrorCount, latestTurnStartedAt, boolToInt(session.LatestTurnStateKnown), boolToInt(session.LatestTurnCompleted), state.UpdatedAt.Unix())
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
		INSERT INTO projects(path, name, kind, last_activity, status, attention_score, present_on_disk, worktree_root_path, worktree_kind, worktree_parent_branch, worktree_merge_status, worktree_origin_todo_id, repo_branch, repo_dirty, repo_conflict, repo_sync_status, repo_ahead_count, repo_behind_count, forgotten, manually_added, in_scope, pinned, snoozed_until, last_session_seen_at, run_command, moved_from_path, moved_at, created_at, updated_at)
		SELECT ?, name, kind, last_activity, status, attention_score, present_on_disk, worktree_root_path, worktree_kind, worktree_parent_branch, worktree_merge_status, worktree_origin_todo_id, repo_branch, repo_dirty, repo_conflict, repo_sync_status, repo_ahead_count, repo_behind_count, forgotten, manually_added, in_scope, pinned, snoozed_until, last_session_seen_at, run_command, ?, ?, created_at, ?
		FROM projects
		WHERE path = ?
	`, newPath, oldPath, movedAt.Unix(), movedAt.Unix(), oldPath)
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
		`UPDATE project_todos SET project_path = ? WHERE project_path = ?`,
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
		manuallyAdded bool
		pinned        bool
		forgotten     bool
		snoozedUntil  sql.NullInt64
		lastSeenAt    sql.NullInt64
		runCommand    string
		movedFromPath string
		movedAt       sql.NullInt64
	}

	loadProject := func(path string) (projectRow, error) {
		var row projectRow
		var manuallyAdded, pinned, forgotten int
		err := tx.QueryRowContext(ctx, `
			SELECT manually_added, pinned, forgotten, snoozed_until, last_session_seen_at, run_command, moved_from_path, moved_at
			FROM projects
			WHERE path = ?
		`, path).Scan(&manuallyAdded, &pinned, &forgotten, &row.snoozedUntil, &row.lastSeenAt, &row.runCommand, &row.movedFromPath, &row.movedAt)
		if err != nil {
			return projectRow{}, err
		}
		row.manuallyAdded = manuallyAdded != 0
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
	mergedManuallyAdded := oldProject.manuallyAdded || newProject.manuallyAdded
	mergedRunCommand := strings.TrimSpace(newProject.runCommand)
	if mergedRunCommand == "" {
		mergedRunCommand = strings.TrimSpace(oldProject.runCommand)
	}
	mergedSnoozedUntil := pickLaterNullInt64(oldProject.snoozedUntil, newProject.snoozedUntil)
	mergedLastSeenAt := pickLaterNullInt64(oldProject.lastSeenAt, newProject.lastSeenAt)
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
		SET manually_added = ?,
			pinned = ?,
			forgotten = ?,
			snoozed_until = ?,
			last_session_seen_at = ?,
			run_command = ?,
			moved_from_path = ?,
			moved_at = ?,
			updated_at = ?
		WHERE path = ?
	`, boolToInt(mergedManuallyAdded), boolToInt(mergedPinned), boolToInt(mergedForgotten), nullableInt64Value(mergedSnoozedUntil), nullableInt64Value(mergedLastSeenAt), mergedRunCommand, mergedMovedFromPath, nullableInt64Value(mergedMovedAt), movedAt.Unix(), newPath); err != nil {
		return err
	}

	if _, err = tx.ExecContext(ctx, `DELETE FROM project_reasons WHERE project_path = ?`, oldPath); err != nil {
		return err
	}

	updateStatements := []string{
		`UPDATE project_todos SET project_path = ? WHERE project_path = ?`,
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

func (s *Store) SetProjectSessionSeenAt(ctx context.Context, path string, seenAt time.Time) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return errors.New("project path is required")
	}
	if seenAt.IsZero() {
		seenAt = time.Now()
	}
	_, err := s.db.ExecContext(ctx, `UPDATE projects SET last_session_seen_at = ?, updated_at = ? WHERE path = ?`, seenAt.Unix(), time.Now().Unix(), path)
	return err
}

func (s *Store) ClearProjectSessionSeenAt(ctx context.Context, path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return errors.New("project path is required")
	}
	_, err := s.db.ExecContext(ctx, `UPDATE projects SET last_session_seen_at = NULL, updated_at = ? WHERE path = ?`, time.Now().Unix(), path)
	return err
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

func timeUnixOrZero(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

func sessionClassificationLogicalIDExpr(alias string) string {
	alias = strings.TrimSpace(alias)
	return fmt.Sprintf(
		`COALESCE(NULLIF(%[1]s.raw_session_id, ''), CASE WHEN instr(%[1]s.session_id, ':') > 0 THEN substr(%[1]s.session_id, instr(%[1]s.session_id, ':') + 1) ELSE %[1]s.session_id END)`,
		alias,
	)
}

func sessionClassificationCanonicalRankExpr(alias string) string {
	alias = strings.TrimSpace(alias)
	return fmt.Sprintf(
		`CASE WHEN %[1]s.source != '' AND %[1]s.raw_session_id != '' AND instr(%[1]s.session_id, ':') > 0 THEN 1 ELSE 0 END`,
		alias,
	)
}

func preferredSessionClassificationIDExpr(sessionIDExpr string) string {
	sessionIDExpr = strings.TrimSpace(sessionIDExpr)
	return fmt.Sprintf(`
		(
			SELECT sc_lookup.session_id
			FROM session_classifications sc_lookup
			WHERE sc_lookup.session_id = %[1]s OR sc_lookup.raw_session_id = %[1]s
			ORDER BY %[2]s DESC, sc_lookup.updated_at DESC, sc_lookup.session_id ASC
			LIMIT 1
		)
	`, sessionIDExpr, sessionClassificationCanonicalRankExpr("sc_lookup"))
}

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

	sameSnapshotHash := existing.SnapshotHash == classification.SnapshotHash

	if sameSnapshotHash && existing.Status == model.ClassificationCompleted && strings.TrimSpace(existing.Summary) != "" {
		return false, nil
	}

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
		SET source = ?,
			raw_session_id = ?,
			project_path = ?,
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
	`, string(classification.Source), classification.RawSessionID, classification.ProjectPath, classification.SessionFile, classification.SessionFormat, classification.SnapshotHash,
		string(model.ClassificationPending), string(classification.Stage), classification.Model, classification.ClassifierVersion,
		classification.SourceUpdatedAt.Unix(), createdAt.Unix(), nullableTimeUnixValue(classification.StageStartedAt), now.Unix(), classification.SessionID)
	return err == nil, err
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
	sameSnapshotHash := existing.SnapshotHash == classification.SnapshotHash

	if sameSnapshotHash && existing.Status == model.ClassificationCompleted && strings.TrimSpace(existing.Summary) != "" {
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
			category = '',
			summary = '',
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
		string(model.ClassificationFailed), classification.Model, classification.ClassifierVersion, lastError,
		classification.SourceUpdatedAt.Unix(), createdAt.Unix(), now.Unix(), classification.SessionID)
	if err != nil {
		return false, err
	}
	classification.CreatedAt = createdAt
	applyRecordedSessionClassificationFailure(classification, lastError, now)
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
		WHERE sc.status = ? AND p.in_scope = 1
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
			category = '',
			summary = '',
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
			category = '',
			summary = '',
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
	classification.Category = ""
	classification.Summary = ""
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
			p.path, p.name, p.kind, p.last_activity, p.status, p.attention_score, p.present_on_disk, p.worktree_root_path, p.worktree_kind, p.worktree_parent_branch, p.worktree_merge_status, p.worktree_origin_todo_id, p.repo_branch, p.repo_dirty, p.repo_conflict, p.repo_sync_status, p.repo_ahead_count, p.repo_behind_count, p.forgotten, p.manually_added, p.in_scope, p.pinned, p.snoozed_until, p.last_session_seen_at, p.created_at,
			COALESCE((SELECT COUNT(*) FROM project_todos pt WHERE pt.project_path = p.path AND pt.done = 0), 0),
			COALESCE((SELECT COUNT(*) FROM project_todos pt WHERE pt.project_path = p.path), 0),
			p.run_command,
			p.moved_from_path, p.moved_at,
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
			item        model.TodoItem
			done        int
			createdAt   int64
			updatedAt   int64
			completedAt sql.NullInt64
			suggestion  sql.NullInt64
			status      sql.NullString
			textHash    sql.NullString
			branchName  sql.NullString
			suffix      sql.NullString
			kind        sql.NullString
			reason      sql.NullString
			confidence  sql.NullFloat64
			modelName   sql.NullString
			lastError   sql.NullString
			suggestedAt sql.NullInt64
		)
		if err := rows.Scan(
			&item.ID, &item.ProjectPath, &item.Text, &done, &item.Position, &createdAt, &updatedAt, &completedAt,
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
	return out, rows.Err()
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
		SET category = '',
			summary = '',
			confidence = 0
		WHERE status = ?
		  AND (category != '' OR summary != '' OR confidence != 0)
	`, string(model.ClassificationFailed))
	if err != nil {
		return fmt.Errorf("repair failed session classification payloads: %w", err)
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

func (s *Store) SetSnooze(ctx context.Context, path string, until *time.Time) error {
	var v any
	if until != nil {
		v = until.Unix()
	}
	_, err := s.db.ExecContext(ctx, `UPDATE projects SET snoozed_until = ?, updated_at = ? WHERE path = ?`, v, time.Now().Unix(), path)
	return err
}

func (s *Store) AddTodo(ctx context.Context, projectPath, text string) (model.TodoItem, error) {
	if projectPath == "" {
		return model.TodoItem{}, fmt.Errorf("project path is required")
	}
	if strings.TrimSpace(text) == "" {
		return model.TodoItem{}, fmt.Errorf("todo text is required")
	}
	now := time.Now()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return model.TodoItem{}, err
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx, `
		INSERT INTO project_todos(project_path, text, done, position, created_at, updated_at)
		SELECT ?, ?, 0, COALESCE(MAX(position), -1) + 1, ?, ?
		FROM project_todos
		WHERE project_path = ?
	`, projectPath, text, now.Unix(), now.Unix(), projectPath)
	if err != nil {
		return model.TodoItem{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return model.TodoItem{}, err
	}
	var position int
	if err := tx.QueryRowContext(ctx, `SELECT position FROM project_todos WHERE id = ?`, id).Scan(&position); err != nil {
		return model.TodoItem{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE projects SET updated_at = ? WHERE path = ?`, now.Unix(), projectPath); err != nil {
		return model.TodoItem{}, err
	}
	if err := tx.Commit(); err != nil {
		return model.TodoItem{}, err
	}
	return model.TodoItem{
		ID:          id,
		ProjectPath: projectPath,
		Text:        text,
		Position:    position,
		CreatedAt:   now,
		UpdatedAt:   now,
	}, nil
}

func (s *Store) QueueTodoWorktreeSuggestion(ctx context.Context, todoID int64) (bool, error) {
	if todoID <= 0 {
		return false, fmt.Errorf("todo id is required")
	}
	todo, err := s.GetTodo(ctx, todoID)
	if err != nil {
		return false, err
	}
	if todo.Done {
		return false, nil
	}

	now := time.Now()
	textHash := hashTodoText(todo.Text)
	existing, err := s.GetTodoWorktreeSuggestion(ctx, todoID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if err == nil && existing.TodoTextHash == textHash {
		switch existing.Status {
		case model.TodoWorktreeSuggestionQueued,
			model.TodoWorktreeSuggestionRunning,
			model.TodoWorktreeSuggestionReady,
			model.TodoWorktreeSuggestionFailed:
			return false, nil
		}
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO todo_worktree_suggestions(
			todo_id, status, todo_text_hash, branch_name, worktree_suffix, kind, reason,
			confidence, model, last_error, updated_at
		)
		VALUES(?, ?, ?, '', '', '', '', 0, '', '', ?)
		ON CONFLICT(todo_id) DO UPDATE SET
			status = excluded.status,
			todo_text_hash = excluded.todo_text_hash,
			branch_name = '',
			worktree_suffix = '',
			kind = '',
			reason = '',
			confidence = 0,
			model = '',
			last_error = '',
			updated_at = excluded.updated_at
	`, todoID, string(model.TodoWorktreeSuggestionQueued), textHash, now.Unix())
	return err == nil, err
}

func (s *Store) ForceQueueTodoWorktreeSuggestion(ctx context.Context, todoID int64) (bool, error) {
	if todoID <= 0 {
		return false, fmt.Errorf("todo id is required")
	}
	todo, err := s.GetTodo(ctx, todoID)
	if err != nil {
		return false, err
	}
	if todo.Done {
		return false, nil
	}

	// Force-queued suggestions are user-visible requests (open dialog / retry now),
	// so make them immediately claimable regardless of the manager debounce window.
	readyAt := time.Unix(0, 0)
	textHash := hashTodoText(todo.Text)
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO todo_worktree_suggestions(
			todo_id, status, todo_text_hash, branch_name, worktree_suffix, kind, reason,
			confidence, model, last_error, updated_at
		)
		VALUES(?, ?, ?, '', '', '', '', 0, '', '', ?)
		ON CONFLICT(todo_id) DO UPDATE SET
			status = excluded.status,
			todo_text_hash = excluded.todo_text_hash,
			branch_name = '',
			worktree_suffix = '',
			kind = '',
			reason = '',
			confidence = 0,
			model = '',
			last_error = '',
			updated_at = excluded.updated_at
	`, todoID, string(model.TodoWorktreeSuggestionQueued), textHash, readyAt.Unix())
	return err == nil, err
}

func (s *Store) QueueOpenTodoWorktreeSuggestions(ctx context.Context) (int, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id
		FROM project_todos
		WHERE done = 0
		ORDER BY updated_at ASC, id ASC
	`)
	if err != nil {
		return 0, err
	}
	ids := make([]int64, 0, 16)
	for rows.Next() {
		var todoID int64
		if err := rows.Scan(&todoID); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, todoID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}

	queued := 0
	for _, todoID := range ids {
		changed, err := s.QueueTodoWorktreeSuggestion(ctx, todoID)
		if err != nil {
			return queued, err
		}
		if changed {
			queued++
		}
	}
	return queued, nil
}

func scanTodoWorktreeSuggestionRow(scanner interface {
	Scan(dest ...any) error
}) (model.TodoWorktreeSuggestion, error) {
	var (
		suggestion model.TodoWorktreeSuggestion
		status     string
		updatedAt  int64
	)
	if err := scanner.Scan(
		&suggestion.TodoID,
		&suggestion.ProjectPath,
		&suggestion.TodoText,
		&status,
		&suggestion.TodoTextHash,
		&suggestion.BranchName,
		&suggestion.WorktreeSuffix,
		&suggestion.Kind,
		&suggestion.Reason,
		&suggestion.Confidence,
		&suggestion.Model,
		&suggestion.LastError,
		&updatedAt,
	); err != nil {
		return model.TodoWorktreeSuggestion{}, err
	}
	suggestion.Status = model.TodoWorktreeSuggestionStatus(strings.TrimSpace(status))
	suggestion.TodoText = strings.TrimSpace(suggestion.TodoText)
	suggestion.TodoTextHash = strings.TrimSpace(suggestion.TodoTextHash)
	suggestion.BranchName = strings.TrimSpace(suggestion.BranchName)
	suggestion.WorktreeSuffix = strings.TrimSpace(suggestion.WorktreeSuffix)
	suggestion.Kind = strings.TrimSpace(suggestion.Kind)
	suggestion.Reason = strings.TrimSpace(suggestion.Reason)
	suggestion.Model = strings.TrimSpace(suggestion.Model)
	suggestion.LastError = strings.TrimSpace(suggestion.LastError)
	suggestion.UpdatedAt = time.Unix(updatedAt, 0)
	return suggestion, nil
}

func (s *Store) GetTodoWorktreeSuggestion(ctx context.Context, todoID int64) (model.TodoWorktreeSuggestion, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT
			tws.todo_id, pt.project_path, pt.text, tws.status, tws.todo_text_hash, tws.branch_name,
			tws.worktree_suffix, tws.kind, tws.reason, tws.confidence, tws.model, tws.last_error, tws.updated_at
		FROM todo_worktree_suggestions tws
		JOIN project_todos pt ON pt.id = tws.todo_id
		WHERE tws.todo_id = ?
	`, todoID)
	return scanTodoWorktreeSuggestionRow(row)
}

func (s *Store) DeleteTodoWorktreeSuggestion(ctx context.Context, todoID int64) error {
	if todoID <= 0 {
		return fmt.Errorf("todo id is required")
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM todo_worktree_suggestions WHERE todo_id = ?`, todoID)
	return err
}

func (s *Store) ClaimNextQueuedTodoWorktreeSuggestion(ctx context.Context, debounce, staleAfter time.Duration) (model.TodoWorktreeSuggestion, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return model.TodoWorktreeSuggestion{}, err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	now := time.Now()
	if staleAfter > 0 {
		_, err = tx.ExecContext(ctx, `
			UPDATE todo_worktree_suggestions
			SET status = ?, updated_at = ?
			WHERE status = ? AND updated_at < ?
		`, string(model.TodoWorktreeSuggestionQueued), now.Unix(), string(model.TodoWorktreeSuggestionRunning), now.Add(-staleAfter).Unix())
		if err != nil {
			return model.TodoWorktreeSuggestion{}, err
		}
	}

	row := tx.QueryRowContext(ctx, `
		SELECT
			tws.todo_id, pt.project_path, pt.text, tws.status, tws.todo_text_hash, tws.branch_name,
			tws.worktree_suffix, tws.kind, tws.reason, tws.confidence, tws.model, tws.last_error, tws.updated_at
		FROM todo_worktree_suggestions tws
		JOIN project_todos pt ON pt.id = tws.todo_id
		JOIN projects p ON p.path = pt.project_path
		WHERE tws.status = ?
		  AND pt.done = 0
		  AND p.in_scope = 1
		  AND tws.updated_at <= ?
		ORDER BY p.attention_score DESC, pt.updated_at ASC, tws.updated_at ASC
		LIMIT 1
	`, string(model.TodoWorktreeSuggestionQueued), now.Add(-debounce).Unix())

	suggestion, err := scanTodoWorktreeSuggestionRow(row)
	if err != nil {
		return model.TodoWorktreeSuggestion{}, err
	}

	result, err := tx.ExecContext(ctx, `
		UPDATE todo_worktree_suggestions
		SET status = ?, updated_at = ?, last_error = ''
		WHERE todo_id = ? AND status = ? AND updated_at = ?
	`, string(model.TodoWorktreeSuggestionRunning), now.Unix(), suggestion.TodoID, string(model.TodoWorktreeSuggestionQueued), suggestion.UpdatedAt.Unix())
	if err != nil {
		return model.TodoWorktreeSuggestion{}, err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return model.TodoWorktreeSuggestion{}, err
	}
	if rowsAffected != 1 {
		return model.TodoWorktreeSuggestion{}, sql.ErrNoRows
	}
	if err := tx.Commit(); err != nil {
		return model.TodoWorktreeSuggestion{}, err
	}
	suggestion.Status = model.TodoWorktreeSuggestionRunning
	suggestion.UpdatedAt = now
	return suggestion, nil
}

func (s *Store) CompleteTodoWorktreeSuggestion(ctx context.Context, suggestion model.TodoWorktreeSuggestion) (bool, error) {
	if suggestion.TodoID <= 0 {
		return false, errors.New("todo worktree suggestion requires todo_id")
	}
	now := time.Now()
	result, err := s.db.ExecContext(ctx, `
		UPDATE todo_worktree_suggestions
		SET status = ?,
			todo_text_hash = ?,
			branch_name = ?,
			worktree_suffix = ?,
			kind = ?,
			reason = ?,
			confidence = ?,
			model = ?,
			last_error = '',
			updated_at = ?
		WHERE todo_id = ?
		  AND status = ?
		  AND todo_text_hash = ?
		  AND updated_at = ?
	`, string(model.TodoWorktreeSuggestionReady), strings.TrimSpace(suggestion.TodoTextHash), strings.TrimSpace(suggestion.BranchName),
		strings.TrimSpace(suggestion.WorktreeSuffix), strings.TrimSpace(suggestion.Kind), strings.TrimSpace(suggestion.Reason),
		suggestion.Confidence, strings.TrimSpace(suggestion.Model), now.Unix(), suggestion.TodoID,
		string(model.TodoWorktreeSuggestionRunning), strings.TrimSpace(suggestion.TodoTextHash), suggestion.UpdatedAt.Unix())
	if err != nil {
		return false, err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return rowsAffected == 1, nil
}

func (s *Store) FailTodoWorktreeSuggestion(ctx context.Context, suggestion model.TodoWorktreeSuggestion, lastError string) (bool, error) {
	if suggestion.TodoID <= 0 {
		return false, errors.New("todo worktree suggestion requires todo_id")
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE todo_worktree_suggestions
		SET status = ?, last_error = ?, updated_at = ?
		WHERE todo_id = ?
		  AND status = ?
		  AND todo_text_hash = ?
		  AND updated_at = ?
	`, string(model.TodoWorktreeSuggestionFailed), strings.TrimSpace(lastError), time.Now().Unix(), suggestion.TodoID,
		string(model.TodoWorktreeSuggestionRunning), strings.TrimSpace(suggestion.TodoTextHash), suggestion.UpdatedAt.Unix())
	if err != nil {
		return false, err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return rowsAffected == 1, nil
}

func (s *Store) GetTodo(ctx context.Context, todoID int64) (model.TodoItem, error) {
	if todoID <= 0 {
		return model.TodoItem{}, fmt.Errorf("todo id is required")
	}
	row := s.db.QueryRowContext(ctx, `
		SELECT id, project_path, text, done, position, created_at, updated_at, completed_at
		FROM project_todos
		WHERE id = ?
	`, todoID)
	var (
		item        model.TodoItem
		done        int
		createdAt   int64
		updatedAt   int64
		completedAt sql.NullInt64
	)
	if err := row.Scan(&item.ID, &item.ProjectPath, &item.Text, &done, &item.Position, &createdAt, &updatedAt, &completedAt); err != nil {
		return model.TodoItem{}, err
	}
	item.Done = done != 0
	item.CreatedAt = time.Unix(createdAt, 0)
	item.UpdatedAt = time.Unix(updatedAt, 0)
	if completedAt.Valid {
		item.CompletedAt = time.Unix(completedAt.Int64, 0)
	}
	return item, nil
}

func (s *Store) UpdateTodo(ctx context.Context, id int64, text string) error {
	if id <= 0 {
		return fmt.Errorf("todo id is required")
	}
	if strings.TrimSpace(text) == "" {
		return fmt.Errorf("todo text is required")
	}
	now := time.Now()
	result, err := s.db.ExecContext(ctx, `
		UPDATE project_todos
		SET text = ?, updated_at = ?
		WHERE id = ?
	`, text, now.Unix(), id)
	if err != nil {
		return err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) ToggleTodoDone(ctx context.Context, id int64, done bool) error {
	if id <= 0 {
		return fmt.Errorf("todo id is required")
	}
	now := time.Now()
	completedAt := any(nil)
	if done {
		completedAt = now.Unix()
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE project_todos
		SET done = ?, completed_at = ?, updated_at = ?
		WHERE id = ?
	`, boolToInt(done), completedAt, now.Unix(), id)
	if err != nil {
		return err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) DeleteTodo(ctx context.Context, id int64) error {
	if id <= 0 {
		return fmt.Errorf("todo id is required")
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM project_todos WHERE id = ?`, id)
	if err != nil {
		return err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) DeleteDoneTodos(ctx context.Context, projectPath string) (int, error) {
	projectPath = strings.TrimSpace(projectPath)
	if projectPath == "" {
		return 0, fmt.Errorf("project path is required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx, `DELETE FROM project_todos WHERE project_path = ? AND done = 1`, projectPath)
	if err != nil {
		return 0, err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	if rowsAffected > 0 {
		if _, err := tx.ExecContext(ctx, `UPDATE projects SET updated_at = ? WHERE path = ?`, time.Now().Unix(), projectPath); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return int(rowsAffected), nil
}

func (s *Store) SetRunCommand(ctx context.Context, path, command string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE projects SET run_command = ?, updated_at = ? WHERE path = ?`, strings.TrimSpace(command), time.Now().Unix(), path)
	return err
}

func (s *Store) SetWorktreeParentBranch(ctx context.Context, path, branch string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE projects SET worktree_parent_branch = ?, worktree_merge_status = '', updated_at = ? WHERE path = ?`, strings.TrimSpace(branch), time.Now().Unix(), path)
	return err
}

func (s *Store) SetWorktreeOriginTodoID(ctx context.Context, path string, todoID int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE projects SET worktree_origin_todo_id = ?, updated_at = ? WHERE path = ?`, todoID, time.Now().Unix(), path)
	return err
}

func (s *Store) SetForgotten(ctx context.Context, path string, forgotten bool) error {
	_, err := s.db.ExecContext(ctx, `UPDATE projects SET forgotten = ?, updated_at = ? WHERE path = ?`, boolToInt(forgotten), time.Now().Unix(), path)
	return err
}

func (s *Store) SetProjectPresence(ctx context.Context, path string, presentOnDisk bool) error {
	_, err := s.db.ExecContext(ctx, `UPDATE projects SET present_on_disk = ?, updated_at = ? WHERE path = ?`, boolToInt(presentOnDisk), time.Now().Unix(), path)
	return err
}

func (s *Store) SetIgnoredProjectName(ctx context.Context, name string, ignored bool) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("ignored project name is required")
	}
	now := time.Now().Unix()
	if ignored {
		_, err := s.db.ExecContext(ctx, `
			INSERT INTO ignored_project_names(name, created_at)
			VALUES(?, ?)
			ON CONFLICT(name) DO NOTHING
		`, name, now)
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM ignored_project_names WHERE LOWER(name) = LOWER(?)`, name)
	return err
}

func (s *Store) ListIgnoredProjectNames(ctx context.Context) ([]model.IgnoredProjectName, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			ipn.name,
			ipn.created_at,
			COUNT(p.path)
		FROM ignored_project_names ipn
		LEFT JOIN projects p ON LOWER(p.name) = LOWER(ipn.name)
		GROUP BY ipn.name, ipn.created_at
		ORDER BY ipn.created_at DESC, ipn.name ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []model.IgnoredProjectName{}
	for rows.Next() {
		var (
			name          string
			createdAtUnix int64
			matched       int
		)
		if err := rows.Scan(&name, &createdAtUnix, &matched); err != nil {
			return nil, err
		}
		entry := model.IgnoredProjectName{
			Name:            name,
			CreatedAt:       time.Unix(createdAtUnix, 0),
			MatchedProjects: matched,
		}
		out = append(out, entry)
	}
	return out, rows.Err()
}

func (s *Store) SetProjectScope(ctx context.Context, path string, inScope bool) error {
	_, err := s.db.ExecContext(ctx, `UPDATE projects SET in_scope = ?, updated_at = ? WHERE path = ?`, boolToInt(inScope), time.Now().Unix(), path)
	return err
}

func (s *Store) RememberRecentProjectParentPath(ctx context.Context, parentPath string, limit int) error {
	parentPath = filepath.Clean(strings.TrimSpace(parentPath))
	if parentPath == "" {
		return errors.New("recent project parent path is required")
	}
	if limit <= 0 {
		limit = 3
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

	now := time.Now().UnixNano()
	if _, err = tx.ExecContext(ctx, `
		INSERT INTO recent_project_parent_paths(parent_path, last_used_at)
		VALUES (?, ?)
		ON CONFLICT(parent_path) DO UPDATE SET
			last_used_at=excluded.last_used_at
	`, parentPath, now); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `
		DELETE FROM recent_project_parent_paths
		WHERE parent_path NOT IN (
			SELECT parent_path
			FROM recent_project_parent_paths
			ORDER BY last_used_at DESC, parent_path ASC
			LIMIT ?
		)
	`, limit); err != nil {
		return err
	}

	if err = tx.Commit(); err != nil {
		return err
	}
	return nil
}

func (s *Store) ListRecentProjectParentPaths(ctx context.Context, limit int) ([]string, error) {
	if limit <= 0 {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT parent_path
		FROM recent_project_parent_paths
		ORDER BY last_used_at DESC, parent_path ASC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]string, 0, limit)
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			return nil, err
		}
		out = append(out, path)
	}
	return out, rows.Err()
}

func splitRecentHashes(v string) []string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	return strings.FieldsFunc(v, func(r rune) bool {
		return r == '\n' || r == '\r' || r == ' ' || r == '\t'
	})
}
