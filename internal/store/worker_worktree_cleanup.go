package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"

	"lcroom/internal/control"
	"lcroom/internal/model"
)

// WorkerWorktreeCleanupEligible reads persisted ownership off the UI thread.
// Neither path naming conventions nor an agent-supplied task ID confer authority.
func (s *Store) WorkerWorktreeCleanupEligible(ctx context.Context, op control.Operation) (bool, error) {
	return workerWorktreeCleanupOwnsTarget(ctx, s.db, op)
}

func workerWorktreeCleanupOwnsTarget(ctx context.Context, q collaborationQuerier, op control.Operation) (bool, error) {
	if _, ok := control.WorkerWorktreeCleanupForOperation(op); !ok {
		return false, nil
	}
	inv, err := control.ValidateInvocation(op.Invocation)
	if err != nil {
		return false, nil
	}
	var input control.WorktreeRemoveInput
	if err := json.Unmarshal(inv.Args, &input); err != nil {
		return false, err
	}
	path := input.WorktreePath
	origin := filepath.Clean(op.ProjectPath)
	// Never allow a grant to delete the caller or an ancestor containing it.
	if origin == path || strings.HasPrefix(origin, path+string(filepath.Separator)) {
		return false, nil
	}
	var linked bool
	err = q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM projects WHERE path=?
		AND worktree_kind=? AND worktree_root_path!='' AND worktree_root_path!=path)`,
		path, model.WorktreeKindLinked).Scan(&linked)
	if err != nil || !linked {
		return false, err
	}
	// Modern workers own an isolated checkout. Legacy workers may own their
	// workspace directly or its exact repo child, without a repository lease.
	// Shared leases and ambiguous ownership fail closed. Missing directories are
	// fine: the same grant can finish removal of retained Git/LCR records.
	legacyWorkspace := path
	if filepath.Base(path) == "repo" {
		legacyWorkspace = filepath.Dir(path)
	}
	var taskOrigin, provider, session string
	var count int
	err = q.QueryRowContext(ctx, `SELECT
		CASE WHEN origin_worktree_path!='' THEN origin_worktree_path ELSE origin_project_path END,
		origin_provider, origin_session_key, COUNT(*) OVER () FROM agent_tasks
		WHERE (COALESCE(json_extract(repository_json,'$.isolated'),0)=1
		       AND json_extract(repository_json,'$.root')=?)
		   OR (COALESCE(json_extract(repository_json,'$.root'),'')=''
		       AND (workspace_path=? OR workspace_path=?))`, path, path, legacyWorkspace).
		Scan(&taskOrigin, &provider, &session, &count)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return count == 1 && filepath.Clean(taskOrigin) == origin && provider == op.Provider && session == op.SessionKey, nil
}

func workerWorktreeCleanupPermissionID(ctx context.Context, q collaborationQuerier, op control.Operation) (int64, error) {
	p, ok := control.WorkerWorktreeCleanupForOperation(op)
	if !ok {
		return 0, nil
	}
	var id int64
	err := q.QueryRowContext(ctx, `SELECT id FROM control_permissions
		WHERE permission_scope=? AND origin=? AND provider=? AND session_key=? AND capability=? AND target=?`,
		p.Scope, p.Origin, p.Provider, p.SessionKey, p.Capability, p.Target).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	allowed, err := workerWorktreeCleanupOwnsTarget(ctx, q, op)
	if !allowed || err != nil {
		return 0, err
	}
	return id, nil
}
