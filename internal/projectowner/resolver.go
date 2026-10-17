// Package projectowner defines the shared repository ownership boundary for TODOs.
package projectowner

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"lcroom/internal/model"
	"lcroom/internal/scanner"
	"lcroom/internal/store"
)

type Scope struct {
	RequestedPath string `json:"requested_path"`
	ProjectPath   string `json:"project_path"`
	ProjectName   string `json:"project_name"`
	FromWorktree  bool   `json:"from_worktree"`
}

type Resolver struct{ store *store.Store }

func New(st *store.Store) *Resolver { return &Resolver{store: st} }

// ResolveProject deliberately has no caller-supplied override. The launch cwd
// is resolved to an LCR project, and linked worktrees are redirected to their
// loaded repository root. If that mapping is unavailable or ambiguous, capture
// fails closed instead of writing to a guessed project.
func (s *Resolver) ResolveProject(ctx context.Context, requestedPath string) (Scope, error) {
	requestedPath = strings.TrimSpace(requestedPath)
	if requestedPath == "" {
		return Scope{}, fmt.Errorf("launch project path is required")
	}
	abs, err := filepath.Abs(requestedPath)
	if err != nil {
		return Scope{}, fmt.Errorf("resolve launch project path: %w", err)
	}
	requestedPath = filepath.Clean(abs)

	info, err := scanner.ReadGitWorktreeInfo(ctx, requestedPath)
	if err == nil {
		repositoryRoot := filepath.Clean(info.RootPath)
		summary, found, err := s.uniqueSymlinkEquivalentProject(ctx, repositoryRoot)
		if err != nil {
			return Scope{}, err
		}
		if !found {
			// EvalSymlinks can fail for a temporarily unavailable path even
			// when Git supplied a durable root spelling. Preserve exact-path
			// resolution in that case, but only after the uniqueness check.
			summary, err = s.store.GetProjectSummary(ctx, repositoryRoot, true)
			if err == nil {
				found = true
			} else if !errors.Is(err, sql.ErrNoRows) {
				return Scope{}, fmt.Errorf("resolve loaded repository root %s: %w", repositoryRoot, err)
			}
		}
		if found {
			return Scope{
				RequestedPath: requestedPath,
				ProjectPath:   summary.Path,
				ProjectName:   summary.Name,
				FromWorktree:  info.Kind == scanner.GitWorktreeKindLinked || requestedPath != summary.Path,
			}, nil
		}
		return Scope{}, fmt.Errorf("repository root %s is not an unambiguous loaded LCR project", repositoryRoot)
	}

	// Non-Git projects and temporarily unavailable repositories use LCR's
	// durable mapping. Live Git metadata always wins when it is available.
	if summary, lookupErr := s.store.GetProjectSummary(ctx, requestedPath, true); lookupErr == nil {
		return s.scopeFromSummary(ctx, requestedPath, summary)
	} else if !errors.Is(lookupErr, sql.ErrNoRows) {
		return Scope{}, fmt.Errorf("resolve loaded project %s: %w", requestedPath, lookupErr)
	}
	return Scope{}, fmt.Errorf("launch path %s does not map to a loaded LCR project (Git inspection failed: %v)", requestedPath, err)
}

func (s *Resolver) scopeFromSummary(ctx context.Context, requestedPath string, summary model.ProjectSummary) (Scope, error) {
	project := summary
	fromWorktree := summary.WorktreeKind == model.WorktreeKindLinked
	if fromWorktree {
		rootPath := filepath.Clean(strings.TrimSpace(summary.WorktreeRootPath))
		if rootPath == "" || rootPath == "." {
			return Scope{}, fmt.Errorf("linked worktree %s has no repository-root mapping", summary.Path)
		}
		root, err := s.store.GetProjectSummary(ctx, rootPath, true)
		if errors.Is(err, sql.ErrNoRows) {
			var found bool
			root, found, err = s.uniqueSymlinkEquivalentProject(ctx, rootPath)
			if err == nil && !found {
				err = sql.ErrNoRows
			}
		}
		if err != nil {
			return Scope{}, fmt.Errorf("linked worktree repository root %s is not an unambiguous loaded LCR project: %w", rootPath, err)
		}
		project = root
	}
	return Scope{
		RequestedPath: requestedPath,
		ProjectPath:   project.Path,
		ProjectName:   project.Name,
		FromWorktree:  fromWorktree || requestedPath != project.Path,
	}, nil
}

func (s *Resolver) uniqueSymlinkEquivalentProject(ctx context.Context, candidate string) (model.ProjectSummary, bool, error) {
	resolvedCandidate, err := filepath.EvalSymlinks(filepath.Clean(candidate))
	if err != nil {
		return model.ProjectSummary{}, false, nil
	}
	resolvedCandidate = filepath.Clean(resolvedCandidate)
	projects, err := s.store.GetProjectSummaryMap(ctx)
	if err != nil {
		return model.ProjectSummary{}, false, fmt.Errorf("list loaded projects while resolving %s: %w", candidate, err)
	}
	matches := make([]model.ProjectSummary, 0, 1)
	for path, summary := range projects {
		resolved, err := filepath.EvalSymlinks(filepath.Clean(path))
		if err == nil && filepath.Clean(resolved) == resolvedCandidate {
			visible, err := s.store.GetProjectSummary(ctx, summary.Path, true)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return model.ProjectSummary{}, false, fmt.Errorf("validate loaded project %s while resolving %s: %w", summary.Path, candidate, err)
			}
			matches = append(matches, visible)
		}
	}
	switch len(matches) {
	case 0:
		return model.ProjectSummary{}, false, nil
	case 1:
		return matches[0], true, nil
	default:
		return model.ProjectSummary{}, false, fmt.Errorf("launch path %s matches multiple loaded LCR project spellings", candidate)
	}
}
