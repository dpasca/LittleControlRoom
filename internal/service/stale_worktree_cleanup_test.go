package service

import (
	"strings"
	"testing"
	"time"

	"lcroom/internal/model"
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
