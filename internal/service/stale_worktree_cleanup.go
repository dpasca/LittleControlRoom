package service

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"lcroom/internal/model"
)

const StaleWorktreeCleanupWindow = 24 * time.Hour

type StaleWorktreeCleanupCandidate struct {
	ProjectPath       string
	ProjectName       string
	RootProjectPath   string
	Branch            string
	ParentBranch      string
	LastActivity      time.Time
	LinkedTodoID      int64
	AssessmentSummary string
}

type StaleWorktreeCleanupAudit struct {
	AuditedAt              time.Time
	ScannedLinkedWorktrees int
	Candidates             []StaleWorktreeCleanupCandidate
}

// EvaluateStaleWorktreeCleanupCandidate applies the durable, UI-independent
// cleanup policy. Live runtimes, in-flight Git actions, and managed engineer
// sessions are intentionally checked by the host immediately before removal.
func EvaluateStaleWorktreeCleanupCandidate(summary model.ProjectSummary, now time.Time) (StaleWorktreeCleanupCandidate, string, bool) {
	if now.IsZero() {
		now = time.Now()
	}
	if summary.WorktreeKind != model.WorktreeKindLinked {
		return StaleWorktreeCleanupCandidate{}, "not a linked worktree", false
	}
	if summary.Forgotten || !summary.PresentOnDisk {
		return StaleWorktreeCleanupCandidate{}, "checkout is no longer present", false
	}
	if summary.Pinned {
		return StaleWorktreeCleanupCandidate{}, "worktree is pinned", false
	}
	if strings.TrimSpace(summary.WorktreeRootPath) == "" {
		return StaleWorktreeCleanupCandidate{}, "repository root is unavailable", false
	}
	if strings.TrimSpace(summary.WorktreeParentBranch) == "" {
		return StaleWorktreeCleanupCandidate{}, "parent branch is unavailable", false
	}
	if summary.WorktreeMergeStatus != model.WorktreeMergeStatusMerged {
		return StaleWorktreeCleanupCandidate{}, "worktree is not merged into its parent branch", false
	}
	if summary.RepoConflict {
		return StaleWorktreeCleanupCandidate{}, "worktree has unresolved conflicts", false
	}
	if summary.RepoDirty {
		return StaleWorktreeCleanupCandidate{}, "worktree has uncommitted changes", false
	}
	if summary.LatestSessionClassification != model.ClassificationCompleted {
		return StaleWorktreeCleanupCandidate{}, "latest assessment is not complete", false
	}
	if summary.LatestSessionClassificationType != model.SessionCategoryCompleted {
		return StaleWorktreeCleanupCandidate{}, "latest assessment is not done", false
	}
	if !summary.LatestTurnStateKnown {
		return StaleWorktreeCleanupCandidate{}, "latest engineer turn state is unknown", false
	}
	if !summary.LatestTurnCompleted {
		return StaleWorktreeCleanupCandidate{}, "latest engineer turn is unfinished", false
	}

	lastActivity := summary.LastActivity
	if summary.LatestSessionLastEventAt.After(lastActivity) {
		lastActivity = summary.LatestSessionLastEventAt
	}
	if lastActivity.IsZero() {
		return StaleWorktreeCleanupCandidate{}, "last activity is unknown", false
	}
	if !lastActivity.Before(now.Add(-StaleWorktreeCleanupWindow)) {
		return StaleWorktreeCleanupCandidate{}, "worktree was used within the last 24 hours", false
	}

	return StaleWorktreeCleanupCandidate{
		ProjectPath:       summary.Path,
		ProjectName:       summary.Name,
		RootProjectPath:   summary.WorktreeRootPath,
		Branch:            summary.RepoBranch,
		ParentBranch:      summary.WorktreeParentBranch,
		LastActivity:      lastActivity,
		LinkedTodoID:      summary.WorktreeOriginTodoID,
		AssessmentSummary: summary.LatestSessionSummary,
	}, "", true
}

func (s *Service) AuditStaleWorktreeCleanup(ctx context.Context, now time.Time) (StaleWorktreeCleanupAudit, error) {
	if s == nil || s.store == nil {
		return StaleWorktreeCleanupAudit{}, fmt.Errorf("service unavailable")
	}
	if now.IsZero() {
		now = time.Now()
	}
	projects, err := s.store.ListProjects(ctx, true)
	if err != nil {
		return StaleWorktreeCleanupAudit{}, fmt.Errorf("list projects for stale worktree cleanup: %w", err)
	}
	audit := StaleWorktreeCleanupAudit{AuditedAt: now}
	for _, project := range projects {
		if project.WorktreeKind != model.WorktreeKindLinked {
			continue
		}
		audit.ScannedLinkedWorktrees++
		candidate, _, ok := EvaluateStaleWorktreeCleanupCandidate(project, now)
		if ok {
			audit.Candidates = append(audit.Candidates, candidate)
		}
	}
	sort.SliceStable(audit.Candidates, func(i, j int) bool {
		if !audit.Candidates[i].LastActivity.Equal(audit.Candidates[j].LastActivity) {
			return audit.Candidates[i].LastActivity.Before(audit.Candidates[j].LastActivity)
		}
		if audit.Candidates[i].RootProjectPath != audit.Candidates[j].RootProjectPath {
			return audit.Candidates[i].RootProjectPath < audit.Candidates[j].RootProjectPath
		}
		return audit.Candidates[i].ProjectPath < audit.Candidates[j].ProjectPath
	})
	return audit, nil
}

// RevalidateStaleWorktreeCleanupCandidate refreshes Git-derived state before
// repeating the durable eligibility checks. Callers must still re-check their
// live runtime and engineer-session state before invoking removal.
func (s *Service) RevalidateStaleWorktreeCleanupCandidate(ctx context.Context, projectPath string, now time.Time) (StaleWorktreeCleanupCandidate, string, error) {
	if s == nil || s.store == nil {
		return StaleWorktreeCleanupCandidate{}, "", fmt.Errorf("service unavailable")
	}
	projectPath = strings.TrimSpace(projectPath)
	if projectPath == "" {
		return StaleWorktreeCleanupCandidate{}, "", fmt.Errorf("worktree path is required")
	}
	if err := s.RefreshProjectStatusWithOptions(ctx, projectPath, ScanOptions{SkipLinkedWorktreeStatusRefresh: true}); err != nil {
		return StaleWorktreeCleanupCandidate{}, "", fmt.Errorf("refresh stale worktree status: %w", err)
	}
	summary, err := s.store.GetProjectSummary(ctx, projectPath, true)
	if err != nil {
		return StaleWorktreeCleanupCandidate{}, "", fmt.Errorf("reload stale worktree status: %w", err)
	}
	candidate, reason, ok := EvaluateStaleWorktreeCleanupCandidate(summary, now)
	if !ok {
		return StaleWorktreeCleanupCandidate{}, reason, nil
	}
	return candidate, "", nil
}
