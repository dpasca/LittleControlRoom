package projectowner

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"lcroom/internal/model"
	"lcroom/internal/scanner"
)

// ResolveTodoOwner supports ordinary loaded non-Git projects while requiring
// live repository membership for Git projects and known linked mappings. A
// broken Git checkout must never fall back to becoming its own TODO owner.
func (s *Resolver) ResolveTodoOwner(ctx context.Context, path string) (Scope, error) {
	if s == nil || s.store == nil {
		return Scope{}, fmt.Errorf("TODO owner store is required")
	}
	scope, err := s.ResolveProject(ctx, path)
	if err != nil {
		return Scope{}, err
	}
	project, err := s.store.GetProjectSummary(ctx, scope.RequestedPath, true)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Scope{}, err
	}
	_, gitErr := os.Lstat(filepath.Join(scope.RequestedPath, ".git"))
	if gitErr != nil && !os.IsNotExist(gitErr) {
		return Scope{}, fmt.Errorf("inspect TODO owner's Git metadata: %w", gitErr)
	}
	if scope.FromWorktree || project.WorktreeKind == model.WorktreeKindMain || project.WorktreeKind == model.WorktreeKindLinked || gitErr == nil {
		return s.ResolveRepository(ctx, scope.RequestedPath)
	}
	return scope, nil
}

// ResolveRepository is the mutation boundary. Unlike offline capture reads it
// requires live Git membership and a unique loaded, available primary checkout.
// Stored linked mappings must agree with Git; they cannot authorize a moved or
// missing checkout. No checkout identity is changed by resolving its TODO owner.
func (s *Resolver) ResolveRepository(ctx context.Context, path string) (Scope, error) {
	if s == nil || s.store == nil || strings.TrimSpace(path) == "" {
		return Scope{}, fmt.Errorf("repository path and store are required")
	}
	info, err := scanner.ReadGitWorktreeInfo(ctx, path)
	if err != nil {
		if ctx.Err() != nil {
			return Scope{}, ctx.Err()
		}
		return Scope{}, fmt.Errorf("cannot verify repository membership of %s: %w", path, err)
	}
	if !SamePath(info.TopLevelPath, path) {
		return Scope{}, fmt.Errorf("%s is not an exact checkout root", path)
	}
	scope, err := s.ResolveProject(ctx, path)
	if err != nil {
		if ctx.Err() != nil {
			return Scope{}, ctx.Err()
		}
		return Scope{}, err
	}
	root, err := scanner.ReadGitWorktreeInfo(ctx, scope.ProjectPath)
	if ctx.Err() != nil {
		return Scope{}, ctx.Err()
	}
	if err != nil || root.Kind == scanner.GitWorktreeKindLinked || !SamePath(root.TopLevelPath, root.RootPath) || !SamePath(root.RootPath, info.RootPath) {
		return Scope{}, fmt.Errorf("repository owner %s is missing or no longer the primary checkout for %s", scope.ProjectPath, path)
	}
	loaded, err := s.store.GetProjectSummary(ctx, filepath.Clean(path), true)
	if err == nil && loaded.WorktreeKind == model.WorktreeKindLinked && !SamePath(loaded.WorktreeRootPath, scope.ProjectPath) {
		return Scope{}, fmt.Errorf("stale repository-root mapping for %s; refresh its project state before retrying", path)
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Scope{}, err
	}
	return scope, nil
}

// SamePath compares existing paths only. Missing paths never establish trust.
func SamePath(a, b string) bool {
	if strings.TrimSpace(a) == "" || strings.TrimSpace(b) == "" {
		return false
	}
	x, err := filepath.EvalSymlinks(a)
	if err != nil {
		return false
	}
	y, err := filepath.EvalSymlinks(b)
	return err == nil && filepath.Clean(x) == filepath.Clean(y)
}
