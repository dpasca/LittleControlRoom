package tui

import (
	"testing"
	"time"

	"lcroom/internal/model"
)

func TestBossViewContextCapturesSelectedClassicTUIState(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_800_000_000, 0)
	project := model.ProjectSummary{
		Path:                 "/tmp/alpha",
		Name:                 "Alpha",
		Status:               model.StatusPossiblyStuck,
		AttentionScore:       31,
		LastActivity:         now.Add(-time.Hour),
		RepoBranch:           "feature/boss",
		RepoDirty:            true,
		OpenTODOCount:        2,
		LatestSessionSummary: "Waiting for product direction.",
	}
	m := Model{
		allProjects: []model.ProjectSummary{
			project,
			{Path: "/tmp/beta", Name: "Beta"},
		},
		projects:    []model.ProjectSummary{project},
		selected:    0,
		sortMode:    sortByAttention,
		visibility:  visibilityAllFolders,
		focusedPane: focusDetail,
		status:      "Detail focused",
		detail: model.ProjectDetail{
			Summary:      project,
			Reasons:      []model.AttentionReason{{Text: "High attention"}},
			Todos:        []model.TodoItem{{Text: "Open"}, {Text: "Done", Done: true}},
			Sessions:     []model.SessionEvidence{{SessionID: "s1"}},
			RecentEvents: []model.StoredEvent{{Type: "scan"}},
		},
	}

	view := m.bossViewContext()
	if !view.Active || !view.Embedded {
		t.Fatalf("view should be active embedded context: %#v", view)
	}
	if view.SelectedProject.Path != "/tmp/alpha" || view.SelectedProject.Name != "Alpha" {
		t.Fatalf("selected project context = %#v", view.SelectedProject)
	}
	if view.VisibleProjectCount != 1 || view.AllProjectCount != 2 {
		t.Fatalf("project counts = visible %d all %d", view.VisibleProjectCount, view.AllProjectCount)
	}
	if view.FocusedPane != "detail" || view.SortMode != "attention" || view.Visibility != "all_folders" {
		t.Fatalf("view controls = %#v", view)
	}
	if view.DetailOpenTODOCount != 1 || view.DetailReasonCount != 1 || view.DetailSessionCount != 1 || view.DetailRecentEvents != 1 {
		t.Fatalf("detail context = %#v", view)
	}
	if view.DetailLatestSummary != "Waiting for product direction." {
		t.Fatalf("detail latest summary = %q", view.DetailLatestSummary)
	}
}
