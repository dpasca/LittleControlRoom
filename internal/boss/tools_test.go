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
		Kind:        bossActionProjectDetail,
		ProjectPath: "/tmp/alpha",
		Limit:       8,
	}, StateSnapshot{}, ViewContext{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	for _, want := range []string{
		"Project detail:",
		"Alpha",
		"/tmp/alpha",
		"Needs a rollout decision",
		"Write boss-mode integration tests",
		"exact project path supplied by boss chat",
	} {
		if !strings.Contains(result.Text, want) {
			t.Fatalf("tool result missing %q:\n%s", want, result.Text)
		}
	}
}

func TestQueryExecutorSearchesContext(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	now := time.Unix(1_800_000_000, 0)
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:           "/tmp/okmain",
		Name:           "okmain",
		LastActivity:   now,
		Status:         model.StatusIdle,
		AttentionScore: 18,
		PresentOnDisk:  true,
		InScope:        true,
		AttentionReason: []model.AttentionReason{{
			Text:   "FCX is the current game codename under discussion",
			Weight: 12,
		}},
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("upsert project: %v", err)
	}

	executor := newQueryExecutor(st)
	executor.nowFn = func() time.Time { return now }
	result, err := executor.Execute(ctx, bossAction{
		Kind:  bossActionSearchContext,
		Query: "FCX",
		Limit: 4,
	}, StateSnapshot{}, ViewContext{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	for _, want := range []string{
		`Context search for "FCX"`,
		"Query time:",
		"updated_at:",
		"age_at_query:",
		"/tmp/okmain",
		"FCX",
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
		FocusedPane:         "detail",
		SortMode:            "attention",
		Visibility:          "all_folders",
		Filter:              "boss",
		Status:              "Detail focused",
	}, now)

	for _, want := range []string{
		"embedded over classic TUI",
		"2 visible of 4 known",
		"filter=\"boss\"",
	} {
		if !strings.Contains(brief, want) {
			t.Fatalf("brief missing %q:\n%s", want, brief)
		}
	}
	if strings.Contains(brief, "selected project") || strings.Contains(brief, "detail panel") {
		t.Fatalf("brief should not expose hidden project cursor/detail state:\n%s", brief)
	}
}

func TestQueryExecutorDoesNotUseHiddenSelectedProject(t *testing.T) {
	t.Parallel()

	executor := newQueryExecutor(&fakeBossStore{})
	_, _, err := executor.resolveProjectPath(context.Background(), bossAction{Target: "selected"}, ViewContext{})
	if err == nil {
		t.Fatalf("resolveProjectPath() error = nil, want selected-project rejection")
	}
	if !strings.Contains(err.Error(), "hidden classic TUI selection") {
		t.Fatalf("resolveProjectPath() error = %q, want hidden selection guidance", err)
	}
}
