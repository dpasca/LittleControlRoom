package boss

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lcroom/internal/model"
	"lcroom/internal/store"
)

func TestQueryExecutorReportsProjectDetailFromStore(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	now := time.Unix(1_800_000_000, 0)
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:           "/tmp/alpha",
		Name:           "Alpha",
		LastActivity:   now.Add(-2 * time.Hour),
		Status:         model.StatusPossiblyStuck,
		AttentionScore: 42,
		PresentOnDisk:  true,
		InScope:        true,
		RepoBranch:     "feature/boss",
		RepoDirty:      true,
		AttentionReason: []model.AttentionReason{{
			Text:   "Needs a rollout decision",
			Weight: 20,
		}},
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("upsert project: %v", err)
	}
	if _, err := st.AddTodo(ctx, "/tmp/alpha", "Write boss-mode integration tests"); err != nil {
		t.Fatalf("add todo: %v", err)
	}

	executor := newQueryExecutor(st)
	executor.nowFn = func() time.Time { return now }
	result, err := executor.Execute(ctx, bossAction{
		Kind:   bossActionProjectDetail,
		Target: "selected",
		Limit:  8,
	}, StateSnapshot{}, ViewContext{
		SelectedProject: ProjectViewContext{Path: "/tmp/alpha"},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	for _, want := range []string{
		"Project detail:",
		"Alpha",
		"/tmp/alpha",
		"Needs a rollout decision",
		"Write boss-mode integration tests",
		"using the project selected in the classic TUI",
	} {
		if !strings.Contains(result.Text, want) {
			t.Fatalf("tool result missing %q:\n%s", want, result.Text)
		}
	}
}

func TestBuildViewContextBriefIncludesClassicTUIState(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_800_000_000, 0)
	brief := BuildViewContextBrief(ViewContext{
		Active:              true,
		Embedded:            true,
		AllProjectCount:     4,
		VisibleProjectCount: 2,
		SelectedIndex:       1,
		FocusedPane:         "detail",
		SortMode:            "attention",
		Visibility:          "all_folders",
		Filter:              "boss",
		Status:              "Detail focused",
		SelectedProject: ProjectViewContext{
			Name:           "Alpha",
			Path:           "/tmp/alpha",
			Status:         model.StatusActive,
			AttentionScore: 9,
			LastActivity:   now.Add(-time.Hour),
			OpenTODOCount:  2,
		},
		DetailProjectPath:   "/tmp/alpha",
		DetailReasonCount:   3,
		DetailOpenTODOCount: 2,
		DetailSessionCount:  1,
		DetailRecentEvents:  5,
		DetailLatestSummary: "The assistant needs a richer report surface.",
	}, now)

	for _, want := range []string{
		"embedded over classic TUI",
		"2 visible of 4 known",
		"filter=\"boss\"",
		"selected project #2",
		"reasons=3 open_todos=2 sessions=1 recent_events=5",
		"The assistant needs a richer report surface",
	} {
		if !strings.Contains(brief, want) {
			t.Fatalf("brief missing %q:\n%s", want, brief)
		}
	}
}
