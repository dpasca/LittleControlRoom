package uisurface

import (
	"encoding/json"
	"reflect"
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
	if got, want := surface.Projects[0].Name, "Client project"; got != want {
		t.Fatalf("first project = %q, want recent project %q", got, want)
	}
	if got, want := surface.Projects[1].Summary, "Choose the mobile navigation."; got != want {
		t.Fatalf("second project summary = %q, want %q", got, want)
	}
	if got, want := len(surface.Categories), 3; got != want {
		t.Fatalf("category count = %d, want Main, Client, and Archived", got)
	}
	if got, want := surface.Categories[2].ID, "archived"; got != want {
		t.Fatalf("last tab = %q, want %q", got, want)
	}
	for _, category := range surface.Categories {
		if category.ID == "cat_private" {
			t.Fatalf("private category leaked into surface: %#v", category)
		}
	}
}

func TestBuildDashboardMatchesDesktopRecentOrderAndGroupsWorktrees(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.July, 12, 12, 2, 0, 0, time.UTC)
	rootPath := "/tmp/control-room"
	projects := []model.ProjectSummary{
		{
			Path:             rootPath,
			Name:             "Little Control Room",
			CategoryID:       "cat_product",
			CategoryName:     "Product",
			PresentOnDisk:    true,
			WorktreeRootPath: rootPath,
			WorktreeKind:     model.WorktreeKindMain,
			RepoBranch:       "master",
			LastActivity:     now.Add(-2 * time.Hour),
		},
		{
			Path:             "/tmp/control-room--beta",
			Name:             "Beta checkout",
			PresentOnDisk:    true,
			WorktreeRootPath: rootPath,
			WorktreeKind:     model.WorktreeKindLinked,
			RepoBranch:       "todo/beta",
			LastActivity:     now.Add(-65 * time.Second),
		},
		{
			Path:             "/tmp/control-room--alpha",
			Name:             "Alpha checkout",
			PresentOnDisk:    true,
			WorktreeRootPath: rootPath,
			WorktreeKind:     model.WorktreeKindLinked,
			RepoBranch:       "todo/alpha",
			LastActivity:     now.Add(-115 * time.Second),
		},
		{
			Path:          "/tmp/older",
			Name:          "Older standalone",
			PresentOnDisk: true,
			LastActivity:  now.Add(-3 * time.Hour),
		},
		{
			Path:          "/tmp/archived",
			Name:          "Archived project",
			PresentOnDisk: true,
			Archived:      true,
			LastActivity:  now,
		},
	}

	surface := BuildDashboard(projects, []model.ProjectCategory{{ID: "cat_product", Name: "Product"}}, BuildOptions{
		Now:             now,
		IncludeArchived: true,
	})

	if got, want := len(surface.Projects), 5; got != want {
		t.Fatalf("project count = %d, want %d", got, want)
	}
	byPath := make(map[string]ProjectItem, len(surface.Projects))
	for _, project := range surface.Projects {
		byPath[project.Path] = project
	}
	root := byPath[rootPath]
	if root.WorktreeRole != "root" || root.LinkedCount != 2 || root.TabID != "cat_product" {
		t.Fatalf("root hierarchy = %#v", root)
	}
	alpha := byPath["/tmp/control-room--alpha"]
	if alpha.WorktreeRole != "child" || alpha.WorktreeRootPath != rootPath || alpha.ListName != "todo/alpha" || alpha.TabID != root.TabID {
		t.Fatalf("alpha hierarchy = %#v", alpha)
	}

	productItems := []ProjectItem{}
	for _, project := range surface.Projects {
		if project.TabID == "cat_product" {
			productItems = append(productItems, project)
		}
	}
	wantProductPaths := []string{rootPath, "/tmp/control-room--alpha", "/tmp/control-room--beta"}
	for i, want := range wantProductPaths {
		if productItems[i].Path != want {
			t.Fatalf("productItems[%d].Path = %q, want %q; items = %#v", i, productItems[i].Path, want, productItems)
		}
	}
	if got := byPath["/tmp/archived"].TabID; got != "archived" {
		t.Fatalf("archived tab id = %q, want archived", got)
	}
	if surface.Counts.All != 4 {
		t.Fatalf("active dashboard count = %d, want archived project excluded", surface.Counts.All)
	}
}

func TestSortProjectsByRecentUsesCreationAndMinuteAlphabeticalOrder(t *testing.T) {
	t.Parallel()
	minute := time.Date(2026, time.July, 12, 12, 0, 0, 0, time.UTC)
	projects := []model.ProjectSummary{
		{Name: "Zulu", Path: "/tmp/zulu", LastActivity: minute.Add(55 * time.Second)},
		{Name: "Alpha", Path: "/tmp/alpha", LastActivity: minute.Add(5 * time.Second)},
		{Name: "New", Path: "/tmp/new", ManuallyAdded: true, CreatedAt: minute.Add(time.Minute)},
	}

	SortProjectsByRecent(projects)
	want := []string{"New", "Alpha", "Zulu"}
	for i := range want {
		if projects[i].Name != want[i] {
			t.Fatalf("projects[%d].Name = %q, want %q", i, projects[i].Name, want[i])
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

func TestBuildProjectDetailStartsWithSharedOverview(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.July, 16, 9, 30, 0, 0, time.UTC)
	project := model.ProjectSummary{
		Path:                            "/tmp/shared-detail",
		Name:                            "Shared detail",
		PresentOnDisk:                   true,
		Status:                          model.StatusActive,
		LatestSessionClassification:     model.ClassificationCompleted,
		LatestSessionClassificationType: model.SessionCategoryNeedsFollowUp,
		LatestSessionSummary:            "Review the mobile action deck.",
	}
	options := BuildOptions{Now: now}
	overview := BuildProjectDetailOverview(project, options)
	detail := BuildProjectDetail(model.ProjectDetail{Summary: project}, options)

	if len(overview.Blocks) != 3 {
		t.Fatalf("overview block count = %d, want summary, path, and status", len(overview.Blocks))
	}
	if len(detail.Blocks) < len(overview.Blocks) || !reflect.DeepEqual(detail.Blocks[:len(overview.Blocks)], overview.Blocks) {
		t.Fatalf("detail prefix = %#v, want shared overview %#v", detail.Blocks, overview.Blocks)
	}
	if got, want := overview.Blocks[0].Label, "Summary"; got != want {
		t.Fatalf("first overview label = %q, want %q", got, want)
	}
	if got, want := overview.Blocks[0].Text, project.LatestSessionSummary; got != want {
		t.Fatalf("overview summary = %q, want %q", got, want)
	}
	if got, want := overview.Blocks[1].Label, "Path"; got != want {
		t.Fatalf("second overview label = %q, want %q", got, want)
	}
	if got, want := overview.Blocks[2].Fields[0].Label, "Assessment"; got != want {
		t.Fatalf("status label = %q, want %q", got, want)
	}
}

func TestBuildProjectDetailOverviewWarnsWhenLinkedWorktreeBranchChanged(t *testing.T) {
	t.Parallel()
	project := model.ProjectSummary{
		Path:                  "/tmp/repo--hud-radial-widgets",
		Name:                  "repo--hud-radial-widgets",
		PresentOnDisk:         true,
		WorktreeKind:          model.WorktreeKindLinked,
		WorktreeInitialBranch: "hud/radial-widgets",
		RepoBranch:            "hud/aim9-format",
	}

	overview := BuildProjectDetailOverview(project, BuildOptions{})
	if got, want := len(overview.Blocks), 4; got != want {
		t.Fatalf("overview block count = %d, want %d with worktree warning", got, want)
	}
	warning := overview.Blocks[3]
	if got, want := warning.Label, "Worktree warning"; got != want {
		t.Fatalf("warning label = %q, want %q", got, want)
	}
	if warning.Tone != ToneWarning {
		t.Fatalf("warning tone = %q, want %q", warning.Tone, ToneWarning)
	}
	for _, fragment := range []string{"Branch changed", "hud/radial-widgets", "hud/aim9-format", "repurposed"} {
		if !strings.Contains(warning.Text, fragment) {
			t.Fatalf("warning text = %q, want fragment %q", warning.Text, fragment)
		}
	}

	project.RepoBranch = project.WorktreeInitialBranch
	overview = BuildProjectDetailOverview(project, BuildOptions{})
	if got, want := len(overview.Blocks), 3; got != want {
		t.Fatalf("overview block count = %d, want %d when branch identity matches", got, want)
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
