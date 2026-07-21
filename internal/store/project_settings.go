package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"lcroom/internal/model"
	"path/filepath"
	"strings"
	"time"
)

func (s *Store) DeleteExpiredMissingLinkedWorktrees(ctx context.Context, now time.Time, retention time.Duration) (int, error) {
	if retention <= 0 {
		return 0, nil
	}
	if now.IsZero() {
		now = time.Now()
	}
	cutoff := now.Add(-retention).Unix()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	rows, err := tx.QueryContext(ctx, `
		SELECT p.path
		FROM projects p
		WHERE p.worktree_kind = ?
			AND p.present_on_disk = 0
			AND p.forgotten = 1
			AND p.archived = 0
			AND p.pinned = 0
			AND p.missing_since IS NOT NULL
			AND p.missing_since <= ?
			AND NOT EXISTS (
				SELECT 1
				FROM project_todos pt
				WHERE pt.project_path = p.path AND pt.done = 0
			)
	`, string(model.WorktreeKindLinked), cutoff)
	if err != nil {
		return 0, err
	}
	var paths []string
	for rows.Next() {
		var path string
		if err = rows.Scan(&path); err != nil {
			rows.Close()
			return 0, err
		}
		paths = append(paths, path)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	if err = rows.Close(); err != nil {
		return 0, err
	}
	for _, path := range paths {
		if err = deleteProjectOwnedRows(ctx, tx, path); err != nil {
			return 0, err
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM projects WHERE path = ?`, path); err != nil {
			return 0, err
		}
	}
	if err = tx.Commit(); err != nil {
		return 0, err
	}
	return len(paths), nil
}

func deleteProjectOwnedRows(ctx context.Context, tx *sql.Tx, path string) error {
	type deleteStatement struct {
		query string
		args  []any
	}
	deleteStatements := []deleteStatement{
		{query: `DELETE FROM category_assignments WHERE resource_kind = ? AND resource_id = ?`, args: []any{string(model.CategoryResourceProject), path}},
		{query: `DELETE FROM session_classifications WHERE project_path = ?`, args: []any{path}},
		{query: `DELETE FROM context_session_text_cache WHERE project_path = ?`, args: []any{path}},
		{query: `DELETE FROM context_search_fts WHERE project_path = ?`, args: []any{path}},
		{query: `DELETE FROM project_git_fingerprints WHERE project_path = ?`, args: []any{path}},
		{query: `DELETE FROM commit_todo_checks WHERE project_path = ?`, args: []any{path}},
		{query: `DELETE FROM events WHERE project_path = ?`, args: []any{path}},
		{query: `DELETE FROM path_aliases WHERE old_path = ? OR new_path = ?`, args: []any{path, path}},
	}
	for _, stmt := range deleteStatements {
		if _, err := tx.ExecContext(ctx, stmt.query, stmt.args...); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) SetRunCommand(ctx context.Context, path, command string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE projects SET run_command = ?, updated_at = ? WHERE path = ?`, strings.TrimSpace(command), time.Now().Unix(), path)
	return err
}

func (s *Store) SetWorktreeParentBranch(ctx context.Context, path, branch string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE projects SET worktree_parent_branch = ?, worktree_merge_status = '', updated_at = ? WHERE path = ?`, strings.TrimSpace(branch), time.Now().Unix(), path)
	return err
}

// SetWorktreeInitialBranch records the branch used for the current checkout's creation.
func (s *Store) SetWorktreeInitialBranch(ctx context.Context, path, branch string) error {
	branch = strings.TrimSpace(branch)
	if branch == "" {
		return errors.New("worktree initial branch is required")
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE projects
		SET worktree_initial_branch = ?, updated_at = ?
		WHERE path = ?
	`, branch, time.Now().Unix(), path)
	return err
}

func (s *Store) SetWorktreeOriginTodoID(ctx context.Context, path string, todoID int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE projects SET worktree_origin_todo_id = ?, updated_at = ? WHERE path = ?`, todoID, time.Now().Unix(), path)
	return err
}

func (s *Store) SetProjectWorktreeInfo(ctx context.Context, path, rootPath string, kind model.WorktreeKind) error {
	_, err := s.db.ExecContext(ctx, `UPDATE projects SET worktree_root_path = ?, worktree_kind = ?, worktree_merge_status = '', updated_at = ? WHERE path = ?`, strings.TrimSpace(rootPath), string(kind), time.Now().Unix(), path)
	return err
}

func (s *Store) SetForgotten(ctx context.Context, path string, forgotten bool) error {
	_, err := s.db.ExecContext(ctx, `UPDATE projects SET forgotten = ?, updated_at = ? WHERE path = ?`, boolToInt(forgotten), time.Now().Unix(), path)
	return err
}

func (s *Store) SetProjectPresence(ctx context.Context, path string, presentOnDisk bool) error {
	now := time.Now().Unix()
	if presentOnDisk {
		_, err := s.db.ExecContext(ctx, `UPDATE projects SET present_on_disk = 1, missing_since = NULL, updated_at = ? WHERE path = ?`, now, path)
		return err
	}
	_, err := s.db.ExecContext(ctx, `UPDATE projects SET present_on_disk = 0, missing_since = COALESCE(missing_since, ?), updated_at = ? WHERE path = ?`, now, now, path)
	return err
}

func (s *Store) SetProjectName(ctx context.Context, path, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("project name is required")
	}
	_, err := s.db.ExecContext(ctx, `UPDATE projects SET name = ?, updated_at = ? WHERE path = ?`, name, time.Now().Unix(), path)
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

func (s *Store) SetIgnoredProjectPath(ctx context.Context, path string, ignored bool) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("ignored project path is required")
	}
	path = filepath.Clean(path)
	if path == "." {
		return fmt.Errorf("ignored project path is required")
	}
	now := time.Now().Unix()
	if ignored {
		_, err := s.db.ExecContext(ctx, `
			INSERT INTO ignored_project_paths(path, created_at)
			VALUES(?, ?)
			ON CONFLICT(path) DO NOTHING
		`, path, now)
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM ignored_project_paths WHERE path = ?`, path)
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

func (s *Store) ListIgnoredProjects(ctx context.Context) ([]model.IgnoredProject, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			'name' AS scope,
			ipn.name AS name,
			'' AS path,
			ipn.created_at AS created_at,
			COUNT(p.path) AS matched_projects
		FROM ignored_project_names ipn
		LEFT JOIN projects p ON LOWER(p.name) = LOWER(ipn.name)
		GROUP BY ipn.name, ipn.created_at
		UNION ALL
		SELECT
			'path' AS scope,
			COALESCE(MAX(p.name), '') AS name,
			ipp.path AS path,
			ipp.created_at AS created_at,
			COUNT(p.path) AS matched_projects
		FROM ignored_project_paths ipp
		LEFT JOIN projects p ON p.path = ipp.path
		GROUP BY ipp.path, ipp.created_at
		ORDER BY created_at DESC, scope ASC, name ASC, path ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.IgnoredProject{}
	for rows.Next() {
		var (
			scope     string
			createdAt int64
			entry     model.IgnoredProject
		)
		if err := rows.Scan(&scope, &entry.Name, &entry.Path, &createdAt, &entry.MatchedProjects); err != nil {
			return nil, err
		}
		switch scope {
		case string(model.ProjectIgnoreScopePath):
			entry.Scope = model.ProjectIgnoreScopePath
		default:
			entry.Scope = model.ProjectIgnoreScopeName
		}
		if createdAt > 0 {
			entry.CreatedAt = time.Unix(createdAt, 0)
		}
		out = append(out, entry)
	}
	return out, rows.Err()
}

func (s *Store) SetProjectScope(ctx context.Context, path string, inScope bool) error {
	_, err := s.db.ExecContext(ctx, `UPDATE projects SET in_scope = ?, updated_at = ? WHERE path = ?`, boolToInt(inScope), time.Now().Unix(), path)
	return err
}

func (s *Store) SetProjectArchived(ctx context.Context, path string, archived bool) error {
	_, err := s.db.ExecContext(ctx, `UPDATE projects SET archived = ?, updated_at = ? WHERE path = ?`, boolToInt(archived), time.Now().Unix(), path)
	return err
}

func (s *Store) SetProjectsArchived(ctx context.Context, paths []string, archived bool) error {
	if len(paths) == 0 {
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

	now := time.Now().Unix()
	for _, path := range paths {
		if _, err = tx.ExecContext(ctx, `UPDATE projects SET archived = ?, updated_at = ? WHERE path = ?`, boolToInt(archived), now, path); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	return nil
}

// ReconcileLinkedWorktreeArchiveState repairs older project rows created before
// archive state followed repository families. A linked worktree can remain
// explicitly archived while its root is active, but it may not remain active
// while its root is archived.
func (s *Store) ReconcileLinkedWorktreeArchiveState(ctx context.Context) (int64, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT path FROM projects WHERE archived = 1`)
	if err != nil {
		return 0, fmt.Errorf("load archived project roots: %w", err)
	}
	archivedRoots := map[string]struct{}{}
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			_ = rows.Close()
			return 0, fmt.Errorf("scan archived project root: %w", err)
		}
		path = filepath.Clean(strings.TrimSpace(path))
		if path != "" && path != "." {
			archivedRoots[path] = struct{}{}
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, fmt.Errorf("list archived project roots: %w", err)
	}
	if err := rows.Close(); err != nil {
		return 0, fmt.Errorf("close archived project roots: %w", err)
	}
	if len(archivedRoots) == 0 {
		return 0, nil
	}

	rows, err = s.db.QueryContext(ctx, `
		SELECT path, worktree_root_path
		FROM projects
		WHERE worktree_kind = ? AND archived = 0
	`, string(model.WorktreeKindLinked))
	if err != nil {
		return 0, fmt.Errorf("load active linked worktrees: %w", err)
	}
	paths := []string{}
	for rows.Next() {
		var path, rootPath string
		if err := rows.Scan(&path, &rootPath); err != nil {
			_ = rows.Close()
			return 0, fmt.Errorf("scan active linked worktree: %w", err)
		}
		rootPath = filepath.Clean(strings.TrimSpace(rootPath))
		if _, ok := archivedRoots[rootPath]; ok {
			paths = append(paths, path)
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, fmt.Errorf("list active linked worktrees: %w", err)
	}
	if err := rows.Close(); err != nil {
		return 0, fmt.Errorf("close active linked worktrees: %w", err)
	}
	if err := s.SetProjectsArchived(ctx, paths, true); err != nil {
		return 0, fmt.Errorf("inherit archived root state for linked worktrees: %w", err)
	}
	return int64(len(paths)), nil
}

func (s *Store) MarkProjectManuallyAdded(ctx context.Context, path string, presentOnDisk bool) error {
	path = filepath.Clean(strings.TrimSpace(path))
	if path == "" || path == "." {
		return fmt.Errorf("project path is required")
	}
	now := time.Now().Unix()
	if presentOnDisk {
		_, err := s.db.ExecContext(ctx, `
			UPDATE projects
			SET manually_added = 1,
				in_scope = 1,
				archived = 0,
				forgotten = 0,
				present_on_disk = 1,
				missing_since = NULL,
				created_at = ?,
				updated_at = ?
			WHERE path = ?
		`, now, now, path)
		return err
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE projects
		SET manually_added = 1,
			in_scope = 1,
			archived = 0,
			forgotten = 0,
			created_at = ?,
			updated_at = ?
		WHERE path = ?
	`, now, now, path)
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

type rowScanner interface {
	Scan(dest ...any) error
}

func scanCommitTodoCheck(row rowScanner) (model.CommitTodoCheck, error) {
	var (
		check            model.CommitTodoCheck
		status           string
		completedTodoIDs string
		nextAttemptAt    int64
		autoRetry        int
		createdAt        int64
		updatedAt        int64
	)
	if err := row.Scan(
		&check.ProjectPath,
		&check.BaseHash,
		&check.HeadHash,
		&status,
		&check.Model,
		&completedTodoIDs,
		&check.DecisionJSON,
		&check.EvidenceJSON,
		&check.LastError,
		&check.AttemptCount,
		&nextAttemptAt,
		&autoRetry,
		&createdAt,
		&updatedAt,
	); err != nil {
		return model.CommitTodoCheck{}, err
	}
	check.Status = model.CommitTodoCheckStatus(strings.TrimSpace(status))
	check.CompletedTodoIDs = parseInt64Lines(completedTodoIDs)
	if nextAttemptAt > 0 {
		check.NextAttemptAt = time.Unix(nextAttemptAt, 0)
	}
	check.AutoRetry = autoRetry != 0
	check.CreatedAt = time.Unix(createdAt, 0)
	check.UpdatedAt = time.Unix(updatedAt, 0)
	return check, nil
}

func formatInt64Lines(values []int64) string {
	var out []string
	for _, value := range values {
		out = append(out, fmt.Sprintf("%d", value))
	}
	return strings.Join(out, "\n")
}

func parseInt64Lines(value string) []int64 {
	var out []int64
	for _, line := range strings.Split(value, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var parsed int64
		if _, err := fmt.Sscanf(line, "%d", &parsed); err == nil {
			out = append(out, parsed)
		}
	}
	return out
}
