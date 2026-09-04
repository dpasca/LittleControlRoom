package service

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lcroom/internal/config"
	"lcroom/internal/events"
	"lcroom/internal/model"
	"lcroom/internal/scanner"
	"lcroom/internal/store"
)

func TestEvaluateStaleWorktreeCleanupCandidateRequiresEverySafetySignal(t *testing.T) {
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	base := staleWorktreeCleanupTestSummary(now)
	candidate, reason, ok := EvaluateStaleWorktreeCleanupCandidate(base, now)
	if !ok || reason != "" {
		t.Fatalf("eligible candidate = (%#v, %q, %t)", candidate, reason, ok)
	}
	if candidate.ProjectPath != base.Path || candidate.RootProjectPath != base.WorktreeRootPath || candidate.LinkedTodoID != base.WorktreeOriginTodoID {
		t.Fatalf("candidate = %#v, want paths and TODO from summary", candidate)
	}

	tests := []struct {
		name   string
		mutate func(*model.ProjectSummary)
		reason string
	}{
		{name: "exactly 24 hours", mutate: func(p *model.ProjectSummary) {
			p.LastActivity = now.Add(-24 * time.Hour)
			p.LatestSessionLastEventAt = p.LastActivity
		}, reason: "within the last 24 hours"},
		{name: "newer session event", mutate: func(p *model.ProjectSummary) { p.LatestSessionLastEventAt = now.Add(-time.Hour) }, reason: "within the last 24 hours"},
		{name: "pinned", mutate: func(p *model.ProjectSummary) { p.Pinned = true }, reason: "pinned"},
		{name: "dirty", mutate: func(p *model.ProjectSummary) { p.RepoDirty = true }, reason: "uncommitted"},
		{name: "conflict", mutate: func(p *model.ProjectSummary) { p.RepoConflict = true }, reason: "conflicts"},
		{name: "not merged", mutate: func(p *model.ProjectSummary) { p.WorktreeMergeStatus = model.WorktreeMergeStatusNotMerged }, reason: "not merged"},
		{name: "merge status unknown", mutate: func(p *model.ProjectSummary) { p.WorktreeMergeStatus = model.WorktreeMergeStatusUnknown }, reason: "merge status is unavailable"},
		{name: "merge in progress", mutate: func(p *model.ProjectSummary) { p.WorktreeMergeStatus = model.WorktreeMergeStatusMergeInProgress }, reason: "still in progress"},
		{name: "waiting assessment", mutate: func(p *model.ProjectSummary) { p.LatestSessionClassificationType = model.SessionCategoryWaitingForUser }, reason: "not done"},
		{name: "assessment running", mutate: func(p *model.ProjectSummary) { p.LatestSessionClassification = model.ClassificationRunning }, reason: "not complete"},
		{name: "unfinished turn", mutate: func(p *model.ProjectSummary) { p.LatestTurnCompleted = false }, reason: "unfinished"},
		{name: "unknown turn", mutate: func(p *model.ProjectSummary) { p.LatestTurnStateKnown = false }, reason: "unknown"},
		{name: "missing", mutate: func(p *model.ProjectSummary) { p.PresentOnDisk = false }, reason: "no longer present"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			project := base
			test.mutate(&project)
			_, gotReason, gotOK := EvaluateStaleWorktreeCleanupCandidate(project, now)
			if gotOK || !strings.Contains(gotReason, test.reason) {
				t.Fatalf("eligibility = (%q, %t), want reason containing %q", gotReason, gotOK, test.reason)
			}
		})
	}
}

func TestEvaluateStaleWorktreeCleanupCandidateWithoutRecordedSession(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	base := model.ProjectSummary{
		Path:                 "/tmp/demo--external",
		PresentOnDisk:        true,
		WorktreeKind:         model.WorktreeKindLinked,
		WorktreeRootPath:     "/tmp/demo",
		WorktreeParentBranch: "master",
		WorktreeMergeStatus:  model.WorktreeMergeStatusMerged,
		RepoBranch:           "feature/external",
		LastActivity:         now.Add(-48 * time.Hour),
	}
	candidate, reason, ok := EvaluateStaleWorktreeCleanupCandidate(base, now)
	if !ok || reason != "" || !candidate.NoRecordedSession || !candidate.LastActivity.Equal(base.LastActivity) {
		t.Fatalf("sessionless candidate = %#v, %q, %t", candidate, reason, ok)
	}
	for _, test := range []struct {
		name   string
		mutate func(*model.ProjectSummary)
		reason string
	}{
		{"unknown age", func(p *model.ProjectSummary) { p.LastActivity = time.Time{} }, "unknown"},
		{"recent", func(p *model.ProjectSummary) { p.LastActivity = now.Add(-time.Hour) }, "within the last 24 hours"},
		{"boundary", func(p *model.ProjectSummary) { p.LastActivity = now.Add(-24 * time.Hour) }, "within the last 24 hours"},
		{"pinned", func(p *model.ProjectSummary) { p.Pinned = true }, "pinned"},
		{"dirty", func(p *model.ProjectSummary) { p.RepoDirty = true }, "uncommitted"},
		{"unmerged", func(p *model.ProjectSummary) { p.WorktreeMergeStatus = model.WorktreeMergeStatusNotMerged }, "not merged"},
		{"unclassified session", func(p *model.ProjectSummary) { p.LatestSessionID = "unclassified" }, "not complete"},
		{"partial session", func(p *model.ProjectSummary) { p.LatestSessionLastEventAt = p.LastActivity }, "not complete"},
	} {
		t.Run(test.name, func(t *testing.T) {
			project := base
			test.mutate(&project)
			if _, reason, ok := EvaluateStaleWorktreeCleanupCandidate(project, now); ok || !strings.Contains(reason, test.reason) {
				t.Fatalf("eligibility = %q, %t; want %q", reason, ok, test.reason)
			}
		})
	}
}

func TestStaleWorktreeCleanupScansAndRevalidatesExternalActivity(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	rootPath := filepath.Join(parent, "repo")
	worktreePath := filepath.Join(parent, "repo--external")
	initGitRepo(t, rootPath)
	runGit(t, rootPath, "git", "worktree", "add", "-b", "feature/external", worktreePath)
	gitDirOutput, err := exec.Command("git", "-C", worktreePath, "rev-parse", "--absolute-git-dir").Output()
	if err != nil {
		t.Fatal(err)
	}
	gitDir := strings.TrimSpace(string(gitDirOutput))
	now := time.Now().UTC().Truncate(time.Second)
	old := now.Add(-72 * time.Hour)
	for _, path := range []string{filepath.Join(worktreePath, ".git"), filepath.Join(gitDir, "HEAD"), filepath.Join(gitDir, "index"), filepath.Join(gitDir, "logs", "HEAD")} {
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}
	st, err := store.Open(filepath.Join(parent, "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path: rootPath, Name: "repo", PresentOnDisk: true, InScope: true, UpdatedAt: old,
	}); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.IncludePaths = []string{parent}
	svc := New(cfg, st, events.NewBus(), nil)
	svc.SetSessionClassifier(nil)
	for range 2 {
		if _, err := svc.ScanOnce(ctx); err != nil {
			t.Fatal(err)
		}
		summary, err := st.GetProjectSummary(ctx, worktreePath, true)
		if err != nil || !summary.LastActivity.Equal(old) || summary.HasRecordedSession() || summary.Status != model.StatusIdle {
			t.Fatalf("external worktree summary = %#v, err = %v", summary, err)
		}
		audit, err := svc.AuditStaleWorktreeCleanup(ctx, now)
		if err != nil || len(audit.Candidates) != 1 || audit.Candidates[0].ProjectPath != worktreePath || !audit.Candidates[0].NoRecordedSession {
			t.Fatalf("external worktree audit = %#v, err = %v", audit, err)
		}
	}
	if err := svc.refreshProjectAttention(ctx, worktreePath); err != nil {
		t.Fatal(err)
	}
	if summary, err := st.GetProjectSummary(ctx, worktreePath, true); err != nil || !summary.LastActivity.Equal(old) || summary.Status != model.StatusIdle {
		t.Fatalf("attention refresh lost Git age: %#v, %v", summary, err)
	}
	if candidate, reason, err := svc.RevalidateStaleWorktreeCleanupCandidate(ctx, worktreePath, now); err != nil || reason != "" || !candidate.NoRecordedSession || !candidate.LastActivity.Equal(old) {
		t.Fatalf("revalidation = %#v, %q, %v", candidate, reason, err)
	}
	reader := svc.gitWorktreeInfoReader
	svc.gitWorktreeInfoReader = func(context.Context, string) (scanner.GitWorktreeInfo, error) {
		return scanner.GitWorktreeInfo{}, fmt.Errorf("metadata unavailable")
	}
	if _, reason, err := svc.RevalidateStaleWorktreeCleanupCandidate(ctx, worktreePath, now); err != nil || reason != "last activity is unknown" {
		t.Fatalf("unavailable metadata revalidation = %q, %v", reason, err)
	}
	svc.gitWorktreeInfoReader = reader
	// Even a clean reset to the same commit is new activity after the audit.
	runGit(t, worktreePath, "git", "reset", "--hard", "HEAD")
	if _, reason, err := svc.RevalidateStaleWorktreeCleanupCandidate(ctx, worktreePath, now); err != nil || !strings.Contains(reason, "within the last 24 hours") {
		t.Fatalf("recent Git activity revalidation = %q, %v", reason, err)
	}
}

func staleWorktreeCleanupTestSummary(now time.Time) model.ProjectSummary {
	lastActivity := now.Add(-25 * time.Hour)
	return model.ProjectSummary{
		Path:                            "/tmp/demo--feature-cleanup",
		Name:                            "demo--feature-cleanup",
		PresentOnDisk:                   true,
		WorktreeRootPath:                "/tmp/demo",
		WorktreeKind:                    model.WorktreeKindLinked,
		WorktreeParentBranch:            "master",
		WorktreeMergeStatus:             model.WorktreeMergeStatusMerged,
		WorktreeOriginTodoID:            42,
		RepoBranch:                      "feature/cleanup",
		LastActivity:                    lastActivity,
		LatestSessionLastEventAt:        lastActivity,
		LatestTurnStateKnown:            true,
		LatestTurnCompleted:             true,
		LatestSessionClassification:     model.ClassificationCompleted,
		LatestSessionClassificationType: model.SessionCategoryCompleted,
		LatestSessionSummary:            "The requested work is complete.",
	}
}
