package uisurface

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"lcroom/internal/model"
)

func TestBuildDashboardGroupsProjectsAndHidesPrivateCategories(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.July, 10, 12, 0, 0, 0, time.UTC)
	projects := []model.ProjectSummary{
		{
			Path:                            "/tmp/main",
			Name:                            "Main project",
			PresentOnDisk:                   true,
			Status:                          model.StatusIdle,
			LatestSessionClassification:     model.ClassificationCompleted,
			LatestSessionClassificationType: model.SessionCategoryWaitingForUser,
			LatestSessionSummary:            "Choose the mobile navigation.",
			LastActivity:                    now.Add(-20 * time.Minute),
		},
		{
			Path:          "/tmp/client",
			Name:          "Client project",
			CategoryID:    "cat_client",
			CategoryName:  "Client",
			PresentOnDisk: true,
			Status:        model.StatusActive,
			LastActivity:  now.Add(-2 * time.Minute),
		},
		{
			Path:            "/tmp/private",
			Name:            "Private project",
			CategoryID:      "cat_private",
			CategoryName:    "Private",
			CategoryPrivate: true,
			PresentOnDisk:   true,
			Status:          model.StatusIdle,
		},
	}
	categories := []model.ProjectCategory{
		{ID: "cat_client", Name: "Client", Position: 1},
		{ID: "cat_private", Name: "Private", Position: 2, Private: true},
	}

	surface := BuildDashboard(projects, categories, BuildOptions{Now: now, HidePrivate: true})
	if got, want := len(surface.Projects), 2; got != want {
		t.Fatalf("project count = %d, want %d", got, want)
	}
	if surface.Counts.Attention != 1 || surface.Counts.Active != 1 || surface.Counts.All != 2 {
		t.Fatalf("counts = %#v, want one attention and one active project", surface.Counts)
	}
	if got, want := surface.Projects[0].Summary, "Choose the mobile navigation."; got != want {
		t.Fatalf("first project summary = %q, want %q", got, want)
	}
	if got, want := surface.Projects[0].Bucket, ProjectBucketAttention; got != want {
		t.Fatalf("first project bucket = %q, want %q", got, want)
	}
	if got, want := len(surface.Categories), 3; got != want {
		t.Fatalf("category count = %d, want Main, Client, and All", got)
	}
	for _, category := range surface.Categories {
		if category.ID == "cat_private" {
			t.Fatalf("private category leaked into surface: %#v", category)
		}
	}
}

func TestBuildProjectItemPromotesStaleInProgressTurnToAttention(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.July, 10, 12, 0, 0, 0, time.UTC)
	item := BuildProjectItem(model.ProjectSummary{
		Path:                            "/tmp/stalled",
		Name:                            "Stalled project",
		PresentOnDisk:                   true,
		Status:                          model.StatusIdle,
		LatestSessionClassification:     model.ClassificationCompleted,
		LatestSessionClassificationType: model.SessionCategoryInProgress,
		LatestSessionSummary:            "Implementing the mobile surface.",
		LatestSessionLastEventAt:        now.Add(-5 * time.Hour),
		LatestTurnStateKnown:            true,
		LatestTurnCompleted:             false,
	}, BuildOptions{Now: now, StuckThreshold: 4 * time.Hour})

	if got, want := item.Assessment.Label, "Blocked"; got != want {
		t.Fatalf("assessment = %q, want %q", got, want)
	}
	if got, want := item.Bucket, ProjectBucketAttention; got != want {
		t.Fatalf("bucket = %q, want %q", got, want)
	}
	if !strings.Contains(item.Summary, "never completed") {
		t.Fatalf("summary = %q, want derived stalled-turn explanation", item.Summary)
	}
}

func TestBuildProjectDetailKeepsTODOContentsOutOfSurface(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.July, 10, 12, 0, 0, 0, time.UTC)
	detail := model.ProjectDetail{
		Summary: model.ProjectSummary{
			Path:           "/tmp/demo",
			Name:           "Demo",
			PresentOnDisk:  true,
			Status:         model.StatusIdle,
			OpenTODOCount:  2,
			TotalTODOCount: 3,
		},
		Todos: []model.TodoItem{{Text: "This body belongs on the TODO screen"}},
		Reasons: []model.AttentionReason{
			{Code: "has_open_todos", Text: "2 open TODO items", Weight: 8},
			{Code: "waiting", Text: "Waiting for a design choice", Weight: 20},
		},
	}

	surface := BuildProjectDetail(detail, BuildOptions{Now: now})
	raw, err := json.Marshal(surface)
	if err != nil {
		t.Fatalf("marshal surface: %v", err)
	}
	encoded := string(raw)
	if strings.Contains(encoded, detail.Todos[0].Text) {
		t.Fatalf("detail surface leaked TODO body: %s", encoded)
	}
	if !strings.Contains(encoded, `"text":"2 open"`) {
		t.Fatalf("detail surface should retain the compact open count: %s", encoded)
	}
	if strings.Contains(encoded, "2 open TODO items") {
		t.Fatalf("detail surface repeated the TODO count in attention reasons: %s", encoded)
	}
	if strings.Contains(encoded, `"attention_score"`) {
		t.Fatalf("detail surface should not expose the attention total: %s", encoded)
	}
}

func TestWorktreeDescriptionReportsPendingIntegrationForDirtyWorktree(t *testing.T) {
	t.Parallel()
	project := model.ProjectSummary{
		WorktreeKind:         model.WorktreeKindLinked,
		WorktreeParentBranch: "master",
		WorktreeMergeStatus:  model.WorktreeMergeStatusMerged,
		RepoDirty:            true,
	}

	if got, want := worktreeDescription(project), "Ready to commit and merge into master"; got != want {
		t.Fatalf("dirty worktree description = %q, want %q", got, want)
	}
	project.RepoDirty = false
	if got, want := worktreeDescription(project), "No changes to integrate into master"; got != want {
		t.Fatalf("clean integrated worktree description = %q, want %q", got, want)
	}
}
