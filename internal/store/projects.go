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

func projectSummaryBaseQuery() string {
	return fmt.Sprintf(`
		SELECT
			p.path, p.name, p.kind,
			COALESCE(pc.id, ''), COALESCE(pc.name, ''),
			p.last_activity, p.status, p.attention_score, p.present_on_disk, p.worktree_root_path, p.worktree_kind, p.worktree_parent_branch, p.worktree_merge_status, p.worktree_origin_todo_id, p.repo_branch, p.repo_dirty, p.repo_conflict, p.repo_sync_status, p.repo_ahead_count, p.repo_behind_count, p.repo_submodule_dirty_count, p.repo_submodule_unpushed_count, p.forgotten, p.manually_added, p.in_scope, p.archived, p.pinned, p.snoozed_until, p.last_session_seen_at, p.created_at,
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
	`, preferredSessionClassificationIDExpr("ps.session_id"), sessionClassificationCanonicalRankExpr("sc2"))
}

func projectSummaryVisibilityConditions(includeHistorical bool) string {
	conditions := `p.forgotten = 0
		AND NOT EXISTS (
			SELECT 1
			FROM ignored_project_names ipn
			WHERE LOWER(ipn.name) = LOWER(p.name)
		)
		AND NOT EXISTS (
			SELECT 1
			FROM ignored_project_paths ipp
			WHERE ipp.path = p.path
		)`
	if !includeHistorical {
		conditions += ` AND p.in_scope = 1 AND p.archived = 0`
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
		path, name, kind, categoryID, categoryName, status, runCommand, movedFromPath, repoBranch, worktreeRootPath string
		worktreeParentBranch, worktreeMergeStatus                                                                   string
		worktreeKind                                                                                                string
		worktreeOriginTodoID                                                                                        int64
		lastActivity, snoozedUntil, lastSessionSeenAt, createdAt, movedAt                                           sql.NullInt64
		preferredSessionSource                                                                                      string
		latestSessionLastEventAt, latestTurnStartedAt                                                               sql.NullInt64
		latestSessionID, latestSessionSource, latestRawSessionID                                                    sql.NullString
		latestSessionFormat, latestSessionDetectedPath, latestSessionSnapshotHash                                   sql.NullString
		latestClassificationStatus                                                                                  sql.NullString
		latestClassificationStage, latestClassificationCategory, latestClassificationSummary                        sql.NullString
		latestClassificationStageStartedAt, latestClassificationUpdatedAt                                           sql.NullInt64
		latestCompletedClassificationCategory, latestCompletedClassificationSummary                                 sql.NullString
		latestCompletedClassificationUpdatedAt                                                                      sql.NullInt64
		repoSyncStatus                                                                                              string
		attentionScore, repoAheadCount, repoBehindCount, repoSubmoduleDirtyCount, repoSubmoduleUnpushedCount        int
		openTODOCount, totalTODOCount                                                                               int
		latestTurnKnown, latestTurnCompleted                                                                        int
		presentOnDisk, repoDirty, repoConflict, forgotten, manuallyAdded, inScope, archived                         int
		pinned                                                                                                      int
	)
	if err := scanner.Scan(
		&path,
		&name,
		&kind,
		&categoryID,
		&categoryName,
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
		&repoSubmoduleDirtyCount,
		&repoSubmoduleUnpushedCount,
		&forgotten,
		&manuallyAdded,
		&inScope,
		&archived,
		&pinned,
		&snoozedUntil,
		&lastSessionSeenAt,
		&createdAt,
		&openTODOCount,
		&totalTODOCount,
		&runCommand,
		&movedFromPath,
		&movedAt,
		&preferredSessionSource,
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
		CategoryID:                               strings.TrimSpace(categoryID),
		CategoryName:                             strings.TrimSpace(categoryName),
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
		RepoSubmoduleDirtyCount:                  repoSubmoduleDirtyCount,
		RepoSubmoduleUnpushedCount:               repoSubmoduleUnpushedCount,
		Forgotten:                                forgotten == 1,
		ManuallyAdded:                            manuallyAdded == 1,
		InScope:                                  inScope == 1,
		Archived:                                 archived == 1,
		Pinned:                                   pinned == 1,
		OpenTODOCount:                            openTODOCount,
		TotalTODOCount:                           totalTODOCount,
		RunCommand:                               runCommand,
		MovedFromPath:                            movedFromPath,
		PreferredSessionSource:                   model.NormalizeSessionSource(model.SessionSource(preferredSessionSource)),
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
		query += ` JOIN projects p ON p.path = sc.project_path WHERE p.in_scope = 1 AND p.archived = 0`
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
	state.PreferredSessionSource = model.NormalizeSessionSource(state.PreferredSessionSource)
	if state.UpdatedAt.IsZero() {
		state.UpdatedAt = time.Now()
	}

	var (
		lastActivity any
		snoozedUntil any
		movedAt      any
		createdAt    any
		missingSince any
	)
	if !state.LastActivity.IsZero() {
		lastActivity = state.LastActivity.Unix()
	}
	if !state.PresentOnDisk {
		missingAt := state.UpdatedAt
		if missingAt.IsZero() {
			missingAt = time.Now()
		}
		missingSince = missingAt.Unix()
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
		INSERT INTO projects(path, name, kind, last_activity, status, attention_score, present_on_disk, missing_since, worktree_root_path, worktree_kind, worktree_parent_branch, worktree_merge_status, worktree_origin_todo_id, repo_branch, repo_dirty, repo_conflict, repo_sync_status, repo_ahead_count, repo_behind_count, repo_submodule_dirty_count, repo_submodule_unpushed_count, forgotten, manually_added, in_scope, archived, pinned, snoozed_until, moved_from_path, moved_at, preferred_session_source, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET
			name=excluded.name,
			kind=excluded.kind,
			last_activity=excluded.last_activity,
			status=excluded.status,
			attention_score=excluded.attention_score,
			present_on_disk=excluded.present_on_disk,
			missing_since=CASE
				WHEN excluded.present_on_disk != 0 THEN NULL
				ELSE COALESCE(projects.missing_since, excluded.missing_since, excluded.updated_at)
			END,
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
			repo_submodule_dirty_count=excluded.repo_submodule_dirty_count,
			repo_submodule_unpushed_count=excluded.repo_submodule_unpushed_count,
			forgotten=CASE
				WHEN excluded.present_on_disk = 0 AND projects.present_on_disk = 0 AND projects.forgotten = 1 THEN 1
				ELSE excluded.forgotten
			END,
			manually_added=CASE
				WHEN projects.manually_added != 0 THEN 1
				ELSE excluded.manually_added
			END,
			in_scope=CASE
				WHEN projects.manually_added != 0 OR excluded.manually_added != 0 THEN 1
				ELSE excluded.in_scope
			END,
			archived=CASE
				WHEN projects.manually_added != 0 THEN projects.archived
				ELSE excluded.archived
			END,
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
			preferred_session_source=CASE
				WHEN excluded.preferred_session_source != '' THEN excluded.preferred_session_source
				ELSE projects.preferred_session_source
			END,
			created_at=COALESCE(projects.created_at, excluded.created_at),
			updated_at=excluded.updated_at
	`, state.Path, state.Name, string(state.Kind), lastActivity, string(state.Status), state.AttentionScore, boolToInt(state.PresentOnDisk), missingSince, strings.TrimSpace(state.WorktreeRootPath), string(state.WorktreeKind), strings.TrimSpace(state.WorktreeParentBranch), string(state.WorktreeMergeStatus), state.WorktreeOriginTodoID, strings.TrimSpace(state.RepoBranch), boolToInt(state.RepoDirty), boolToInt(state.RepoConflict), string(state.RepoSyncStatus), state.RepoAheadCount, state.RepoBehindCount, state.RepoSubmoduleDirtyCount, state.RepoSubmoduleUnpushedCount, boolToInt(state.Forgotten), boolToInt(state.ManuallyAdded), boolToInt(state.InScope), boolToInt(state.Archived), boolToInt(state.Pinned), snoozedUntil, state.MovedFromPath, movedAt, string(state.PreferredSessionSource), createdAt, state.UpdatedAt.Unix())
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
		if _, err = tx.ExecContext(ctx, `
			DELETE FROM project_sessions
			WHERE session_id = ? AND project_path != ?
		`, session.SessionID, state.Path); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO project_sessions(session_id, source, raw_session_id, project_path, detected_project_path, session_file, format, snapshot_hash, started_at, last_event_at, error_count, latest_turn_started_at, latest_turn_state_known, latest_turn_completed, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, session.SessionID, string(session.Source), session.RawSessionID, state.Path, detectedPath, session.SessionFile, session.Format, session.SnapshotHash, startedAt, session.LastEventAt.Unix(), session.ErrorCount, latestTurnStartedAt, boolToInt(session.LatestTurnStateKnown), boolToInt(session.LatestTurnCompleted), state.UpdatedAt.Unix())
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `
			UPDATE session_classifications
			SET project_path = ?,
				source = ?,
				raw_session_id = ?,
				session_file = ?,
				session_format = ?
			WHERE session_id = ?
		`, state.Path, string(session.Source), session.RawSessionID, session.SessionFile, session.Format, session.SessionID); err != nil {
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

func (s *Store) SetProjectPreferredSessionSource(ctx context.Context, projectPath string, source model.SessionSource) error {
	projectPath = filepath.Clean(strings.TrimSpace(projectPath))
	if projectPath == "" || projectPath == "." {
		return errors.New("project path is required")
	}
	source = model.NormalizeSessionSource(source)
	if source == model.SessionSourceUnknown {
		return nil
	}
	updatedAt := time.Now().Unix()
	result, err := s.db.ExecContext(ctx, `
		UPDATE projects
		SET preferred_session_source = ?,
			updated_at = ?
		WHERE path = ?
	`, string(source), updatedAt, projectPath)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return sql.ErrNoRows
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

func (s *Store) QueueCommitTodoCheck(ctx context.Context, check model.CommitTodoCheck) (bool, error) {
	projectPath := filepath.Clean(strings.TrimSpace(check.ProjectPath))
	headHash := strings.TrimSpace(check.HeadHash)
	if projectPath == "" || projectPath == "." {
		return false, errors.New("commit TODO check requires project_path")
	}
	if headHash == "" {
		return false, errors.New("commit TODO check requires head_hash")
	}
	now := check.UpdatedAt
	if now.IsZero() {
		now = time.Now()
	}
	baseHash := strings.TrimSpace(check.BaseHash)

	var status string
	err := s.db.QueryRowContext(ctx, `
		SELECT status
		FROM commit_todo_checks
		WHERE project_path = ? AND head_hash = ?
	`, projectPath, headHash).Scan(&status)
	if err == nil {
		switch model.CommitTodoCheckStatus(strings.TrimSpace(status)) {
		case model.CommitTodoCheckQueued, model.CommitTodoCheckRunning, model.CommitTodoCheckCompleted:
			return false, nil
		}
		_, err = s.db.ExecContext(ctx, `
			UPDATE commit_todo_checks
			SET base_hash = ?, status = ?, model = '', completed_todo_ids = '', last_error = '', updated_at = ?
			WHERE project_path = ? AND head_hash = ?
		`, baseHash, string(model.CommitTodoCheckQueued), now.Unix(), projectPath, headHash)
		return err == nil, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO commit_todo_checks(project_path, head_hash, base_hash, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, projectPath, headHash, baseHash, string(model.CommitTodoCheckQueued), now.Unix(), now.Unix())
	return err == nil, err
}

func (s *Store) ClaimNextQueuedCommitTodoCheck(ctx context.Context, staleAfter time.Duration) (model.CommitTodoCheck, error) {
	now := time.Now()
	staleBefore := now.Add(-staleAfter)
	if staleAfter <= 0 {
		staleBefore = now
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return model.CommitTodoCheck{}, err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	check, err := scanCommitTodoCheck(tx.QueryRowContext(ctx, `
		SELECT project_path, base_hash, head_hash, status, model, completed_todo_ids, last_error, created_at, updated_at
		FROM commit_todo_checks
		WHERE status = ? OR (status = ? AND updated_at <= ?)
		ORDER BY updated_at ASC, created_at ASC
		LIMIT 1
	`, string(model.CommitTodoCheckQueued), string(model.CommitTodoCheckRunning), staleBefore.Unix()))
	if err != nil {
		return model.CommitTodoCheck{}, err
	}

	result, err := tx.ExecContext(ctx, `
		UPDATE commit_todo_checks
		SET status = ?, updated_at = ?
		WHERE project_path = ? AND head_hash = ? AND (status = ? OR status = ?)
	`, string(model.CommitTodoCheckRunning), now.Unix(), check.ProjectPath, check.HeadHash, string(model.CommitTodoCheckQueued), string(model.CommitTodoCheckRunning))
	if err != nil {
		return model.CommitTodoCheck{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return model.CommitTodoCheck{}, err
	}
	if affected == 0 {
		err = sql.ErrNoRows
		return model.CommitTodoCheck{}, err
	}
	check.Status = model.CommitTodoCheckRunning
	check.UpdatedAt = now

	if err = tx.Commit(); err != nil {
		return model.CommitTodoCheck{}, err
	}
	return check, nil
}

func (s *Store) CompleteCommitTodoCheck(ctx context.Context, check model.CommitTodoCheck) (bool, error) {
	projectPath := filepath.Clean(strings.TrimSpace(check.ProjectPath))
	headHash := strings.TrimSpace(check.HeadHash)
	if projectPath == "" || projectPath == "." || headHash == "" {
		return false, errors.New("commit TODO check requires project_path and head_hash")
	}
	now := check.UpdatedAt
	if now.IsZero() {
		now = time.Now()
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE commit_todo_checks
		SET status = ?, model = ?, completed_todo_ids = ?, last_error = '', updated_at = ?
		WHERE project_path = ? AND head_hash = ?
	`, string(model.CommitTodoCheckCompleted), strings.TrimSpace(check.Model), formatInt64Lines(check.CompletedTodoIDs), now.Unix(), projectPath, headHash)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected > 0, err
}

func (s *Store) FailCommitTodoCheck(ctx context.Context, check model.CommitTodoCheck, lastError string) (bool, error) {
	projectPath := filepath.Clean(strings.TrimSpace(check.ProjectPath))
	headHash := strings.TrimSpace(check.HeadHash)
	if projectPath == "" || projectPath == "." || headHash == "" {
		return false, errors.New("commit TODO check requires project_path and head_hash")
	}
	now := time.Now()
	result, err := s.db.ExecContext(ctx, `
		UPDATE commit_todo_checks
		SET status = ?, last_error = ?, updated_at = ?
		WHERE project_path = ? AND head_hash = ?
	`, string(model.CommitTodoCheckFailed), strings.TrimSpace(lastError), now.Unix(), projectPath, headHash)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected > 0, err
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
		INSERT INTO projects(path, name, kind, last_activity, status, attention_score, present_on_disk, missing_since, worktree_root_path, worktree_kind, worktree_parent_branch, worktree_merge_status, worktree_origin_todo_id, repo_branch, repo_dirty, repo_conflict, repo_sync_status, repo_ahead_count, repo_behind_count, repo_submodule_dirty_count, repo_submodule_unpushed_count, forgotten, manually_added, in_scope, archived, pinned, snoozed_until, last_session_seen_at, run_command, moved_from_path, moved_at, preferred_session_source, created_at, updated_at)
		SELECT ?, name, kind, last_activity, status, attention_score, present_on_disk, missing_since, worktree_root_path, worktree_kind, worktree_parent_branch, worktree_merge_status, worktree_origin_todo_id, repo_branch, repo_dirty, repo_conflict, repo_sync_status, repo_ahead_count, repo_behind_count, repo_submodule_dirty_count, repo_submodule_unpushed_count, forgotten, manually_added, in_scope, archived, pinned, snoozed_until, last_session_seen_at, run_command, ?, ?, preferred_session_source, created_at, ?
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
		`UPDATE commit_todo_checks SET project_path = ? WHERE project_path = ?`,
		`UPDATE events SET project_path = ? WHERE project_path = ?`,
	}
	for _, stmt := range updateStatements {
		if _, err = tx.ExecContext(ctx, stmt, newPath, oldPath); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `
		UPDATE category_assignments
		SET resource_id = ?, updated_at = ?
		WHERE resource_kind = ? AND resource_id = ?
	`, newPath, movedAt.Unix(), string(model.CategoryResourceProject), oldPath); err != nil {
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
		manuallyAdded   bool
		pinned          bool
		forgotten       bool
		archived        bool
		snoozedUntil    sql.NullInt64
		lastSeenAt      sql.NullInt64
		runCommand      string
		movedFromPath   string
		movedAt         sql.NullInt64
		preferredSource string
	}

	loadProject := func(path string) (projectRow, error) {
		var row projectRow
		var manuallyAdded, pinned, forgotten, archived int
		err := tx.QueryRowContext(ctx, `
			SELECT manually_added, pinned, forgotten, archived, snoozed_until, last_session_seen_at, run_command, moved_from_path, moved_at, preferred_session_source
			FROM projects
			WHERE path = ?
		`, path).Scan(&manuallyAdded, &pinned, &forgotten, &archived, &row.snoozedUntil, &row.lastSeenAt, &row.runCommand, &row.movedFromPath, &row.movedAt, &row.preferredSource)
		if err != nil {
			return projectRow{}, err
		}
		row.manuallyAdded = manuallyAdded != 0
		row.pinned = pinned != 0
		row.forgotten = forgotten != 0
		row.archived = archived != 0
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
	mergedArchived := oldProject.archived || newProject.archived
	mergedManuallyAdded := oldProject.manuallyAdded || newProject.manuallyAdded
	mergedRunCommand := strings.TrimSpace(newProject.runCommand)
	if mergedRunCommand == "" {
		mergedRunCommand = strings.TrimSpace(oldProject.runCommand)
	}
	mergedPreferredSource := strings.TrimSpace(newProject.preferredSource)
	if mergedPreferredSource == "" {
		mergedPreferredSource = strings.TrimSpace(oldProject.preferredSource)
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
			archived = ?,
			snoozed_until = ?,
			last_session_seen_at = ?,
			run_command = ?,
			preferred_session_source = ?,
			moved_from_path = ?,
			moved_at = ?,
			updated_at = ?
		WHERE path = ?
	`, boolToInt(mergedManuallyAdded), boolToInt(mergedPinned), boolToInt(mergedForgotten), boolToInt(mergedArchived), nullableInt64Value(mergedSnoozedUntil), nullableInt64Value(mergedLastSeenAt), mergedRunCommand, mergedPreferredSource, mergedMovedFromPath, nullableInt64Value(mergedMovedAt), movedAt.Unix(), newPath); err != nil {
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
	if _, err = tx.ExecContext(ctx, `
		INSERT INTO category_assignments(resource_kind, resource_id, category_id, created_at, updated_at)
		SELECT resource_kind, ?, category_id, created_at, ?
		FROM category_assignments
		WHERE resource_kind = ? AND resource_id = ?
			AND NOT EXISTS (
				SELECT 1
				FROM category_assignments existing
				WHERE existing.resource_kind = ? AND existing.resource_id = ?
			)
	`, newPath, movedAt.Unix(), string(model.CategoryResourceProject), oldPath, string(model.CategoryResourceProject), newPath); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `
		DELETE FROM category_assignments
		WHERE resource_kind = ? AND resource_id = ?
	`, string(model.CategoryResourceProject), oldPath); err != nil {
		return err
	}

	if _, err = tx.ExecContext(ctx, `DELETE FROM project_git_fingerprints WHERE project_path = ?`, oldPath); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM commit_todo_checks WHERE project_path = ?`, oldPath); err != nil {
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
