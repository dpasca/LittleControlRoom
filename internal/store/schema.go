package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"lcroom/internal/model"
	"strings"
	"time"
)

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
			missing_since INTEGER,
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
			archived INTEGER NOT NULL DEFAULT 0,
			pinned INTEGER NOT NULL DEFAULT 0,
			snoozed_until INTEGER,
			last_session_seen_at INTEGER,
			run_command TEXT NOT NULL DEFAULT '',
			moved_from_path TEXT NOT NULL DEFAULT '',
			moved_at INTEGER,
			preferred_session_source TEXT NOT NULL DEFAULT '',
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
			work_provider TEXT NOT NULL DEFAULT '',
			work_project_path TEXT NOT NULL DEFAULT '',
			work_session_id TEXT NOT NULL DEFAULT '',
			work_claimed_at INTEGER,
			work_state TEXT NOT NULL DEFAULT '',
			work_state_at INTEGER,
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
		`CREATE TABLE IF NOT EXISTS commit_todo_checks (
			project_path TEXT NOT NULL,
			head_hash TEXT NOT NULL,
			base_hash TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL,
			model TEXT NOT NULL DEFAULT '',
			completed_todo_ids TEXT NOT NULL DEFAULT '',
			last_error TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			PRIMARY KEY(project_path, head_hash)
		);`,
		`CREATE INDEX IF NOT EXISTS idx_commit_todo_checks_status_updated ON commit_todo_checks(status, updated_at);`,
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
		`CREATE TABLE IF NOT EXISTS ignored_project_paths (
			path TEXT PRIMARY KEY,
			created_at INTEGER NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS project_categories (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			name_key TEXT NOT NULL UNIQUE,
			position INTEGER NOT NULL,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		);`,
		`CREATE INDEX IF NOT EXISTS idx_project_categories_position ON project_categories(position, name COLLATE NOCASE);`,
		`CREATE TABLE IF NOT EXISTS category_assignments (
			resource_kind TEXT NOT NULL,
			resource_id TEXT NOT NULL,
			category_id TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			PRIMARY KEY(resource_kind, resource_id),
			FOREIGN KEY(category_id) REFERENCES project_categories(id) ON DELETE CASCADE
		);`,
		`CREATE INDEX IF NOT EXISTS idx_category_assignments_category ON category_assignments(category_id, resource_kind, resource_id);`,
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
		`CREATE TABLE IF NOT EXISTS boss_goal_runs (
			id TEXT PRIMARY KEY,
			kind TEXT NOT NULL,
			title TEXT NOT NULL,
			objective TEXT NOT NULL DEFAULT '',
			success_criteria TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL,
			proposal_json TEXT NOT NULL DEFAULT '{}',
			result_json TEXT NOT NULL DEFAULT '{}',
			error TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			completed_at INTEGER
		);`,
		`CREATE INDEX IF NOT EXISTS idx_boss_goal_runs_status_updated ON boss_goal_runs(status, updated_at DESC);`,
		`CREATE TABLE IF NOT EXISTS boss_goal_trace_entries (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			run_id TEXT NOT NULL,
			position INTEGER NOT NULL,
			step_id TEXT NOT NULL DEFAULT '',
			capability TEXT NOT NULL DEFAULT '',
			resource_id TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT '',
			summary TEXT NOT NULL DEFAULT '',
			at INTEGER NOT NULL,
			payload_json TEXT NOT NULL DEFAULT '{}',
			FOREIGN KEY(run_id) REFERENCES boss_goal_runs(id) ON DELETE CASCADE,
			UNIQUE(run_id, position)
		);`,
		`CREATE INDEX IF NOT EXISTS idx_boss_goal_trace_entries_run_position ON boss_goal_trace_entries(run_id, position);`,
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
	if err := s.ensureProjectsArchivedColumn(ctx); err != nil {
		return err
	}
	if err := s.ensureProjectsVisibilityColumns(ctx); err != nil {
		return err
	}
	if err := s.ensureProjectsMissingSinceColumn(ctx); err != nil {
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
	if err := s.ensureProjectsPreferredSessionSourceColumn(ctx); err != nil {
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
	if err := s.ensureProjectTodosWorkColumns(ctx); err != nil {
		return err
	}
	if err := s.ensureProjectsMissingLinkedWorktreeIndex(ctx); err != nil {
		return err
	}
	return nil
}

func (s *Store) projectTodoTableColumns(ctx context.Context) (map[string]struct{}, error) {
	rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(project_todos)`)
	if err != nil {
		return nil, fmt.Errorf("check project_todos schema: %w", err)
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
			return nil, fmt.Errorf("scan project_todos schema: %w", err)
		}
		columns[name] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read project_todos schema: %w", err)
	}
	return columns, nil
}

func (s *Store) ensureProjectTodosWorkColumns(ctx context.Context) error {
	columns, err := s.projectTodoTableColumns(ctx)
	if err != nil {
		return err
	}
	if _, ok := columns["work_provider"]; !ok {
		if _, err := s.db.ExecContext(ctx, `ALTER TABLE project_todos ADD COLUMN work_provider TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("add project_todos.work_provider column: %w", err)
		}
	}
	if _, ok := columns["work_project_path"]; !ok {
		if _, err := s.db.ExecContext(ctx, `ALTER TABLE project_todos ADD COLUMN work_project_path TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("add project_todos.work_project_path column: %w", err)
		}
	}
	if _, ok := columns["work_session_id"]; !ok {
		if _, err := s.db.ExecContext(ctx, `ALTER TABLE project_todos ADD COLUMN work_session_id TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("add project_todos.work_session_id column: %w", err)
		}
	}
	if _, ok := columns["work_claimed_at"]; !ok {
		if _, err := s.db.ExecContext(ctx, `ALTER TABLE project_todos ADD COLUMN work_claimed_at INTEGER`); err != nil {
			return fmt.Errorf("add project_todos.work_claimed_at column: %w", err)
		}
	}
	if _, ok := columns["work_state"]; !ok {
		if _, err := s.db.ExecContext(ctx, `ALTER TABLE project_todos ADD COLUMN work_state TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("add project_todos.work_state column: %w", err)
		}
	}
	if _, ok := columns["work_state_at"]; !ok {
		if _, err := s.db.ExecContext(ctx, `ALTER TABLE project_todos ADD COLUMN work_state_at INTEGER`); err != nil {
			return fmt.Errorf("add project_todos.work_state_at column: %w", err)
		}
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

func (s *Store) ensureProjectsArchivedColumn(ctx context.Context) error {
	columns, err := s.projectTableColumns(ctx)
	if err != nil {
		return err
	}
	if _, ok := columns["archived"]; ok {
		return nil
	}

	if _, err := s.db.ExecContext(ctx, `ALTER TABLE projects ADD COLUMN archived INTEGER NOT NULL DEFAULT 0`); err != nil {
		return fmt.Errorf("add projects.archived column: %w", err)
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

func (s *Store) ensureProjectsMissingSinceColumn(ctx context.Context) error {
	columns, err := s.projectTableColumns(ctx)
	if err != nil {
		return err
	}
	if _, ok := columns["missing_since"]; ok {
		return nil
	}
	if _, err := s.db.ExecContext(ctx, `ALTER TABLE projects ADD COLUMN missing_since INTEGER`); err != nil {
		return fmt.Errorf("add projects.missing_since column: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `
		UPDATE projects
		SET missing_since = updated_at
		WHERE present_on_disk = 0 AND missing_since IS NULL
	`); err != nil {
		return fmt.Errorf("backfill projects.missing_since column: %w", err)
	}
	return nil
}

func (s *Store) ensureProjectsMissingLinkedWorktreeIndex(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
		CREATE INDEX IF NOT EXISTS idx_projects_missing_linked_worktree
		ON projects(worktree_kind, present_on_disk, forgotten, missing_since)
	`)
	if err != nil {
		return fmt.Errorf("create missing linked worktree index: %w", err)
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

func (s *Store) ensureProjectsPreferredSessionSourceColumn(ctx context.Context) error {
	columns, err := s.projectTableColumns(ctx)
	if err != nil {
		return err
	}
	if _, ok := columns["preferred_session_source"]; ok {
		return nil
	}
	if _, err := s.db.ExecContext(ctx, `ALTER TABLE projects ADD COLUMN preferred_session_source TEXT NOT NULL DEFAULT ''`); err != nil {
		return fmt.Errorf("add projects.preferred_session_source column: %w", err)
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
