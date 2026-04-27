package boss

import (
	"context"
	"os"
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
		"Reference metadata (use only for disambiguation/blockers):",
		"Alpha",
		"/tmp/alpha",
		"Needs a rollout decision",
		"Write boss-mode integration tests",
	} {
		if !strings.Contains(result.Text, want) {
			t.Fatalf("tool result missing %q:\n%s", want, result.Text)
		}
	}
}

func TestQueryExecutorKeepsRoutineRepoStateOutOfOperationalDetail(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_800_000_000, 0)
	store := &fakeBossStore{
		projects: []model.ProjectSummary{{
			Path:           "/tmp/okmain",
			Name:           "okmain",
			Status:         model.StatusActive,
			RepoBranch:     "okmain",
			RepoDirty:      true,
			RepoSyncStatus: model.RepoSyncAhead,
			RepoAheadCount: 3,
		}},
		details: map[string]model.ProjectDetail{
			"/tmp/okmain": {
				Summary: model.ProjectSummary{
					Path:                 "/tmp/okmain",
					Name:                 "okmain",
					Status:               model.StatusActive,
					RepoBranch:           "okmain",
					RepoDirty:            true,
					RepoSyncStatus:       model.RepoSyncAhead,
					RepoAheadCount:       3,
					LatestSessionSummary: "Notification visibility fix around the Other tab is ready for validation.",
				},
			},
		},
	}

	executor := newQueryExecutor(store)
	executor.nowFn = func() time.Time { return now }
	result, err := executor.Execute(context.Background(), bossAction{
		Kind:        bossActionProjectDetail,
		ProjectPath: "/tmp/okmain",
		Limit:       8,
	}, StateSnapshot{}, ViewContext{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	operationalIndex := strings.Index(result.Text, "Operational snapshot:")
	metadataIndex := strings.Index(result.Text, "Reference metadata")
	if operationalIndex < 0 || metadataIndex < 0 || operationalIndex > metadataIndex {
		t.Fatalf("project detail should put operational substance before reference metadata:\n%s", result.Text)
	}
	operational := result.Text[operationalIndex:metadataIndex]
	if !strings.Contains(operational, "Notification visibility fix") {
		t.Fatalf("operational detail missing latest work:\n%s", operational)
	}
	for _, noisy := range []string{"dirty", "ahead +3", "branch=okmain", "name=okmain", "path=/tmp/okmain"} {
		if strings.Contains(operational, noisy) {
			t.Fatalf("operational detail should not include routine repo metadata %q:\n%s", noisy, operational)
		}
	}
	reference := result.Text[metadataIndex:]
	for _, want := range []string{"name=okmain", "path=/tmp/okmain", "branch=okmain", "repo=dirty, ahead +3"} {
		if !strings.Contains(reference, want) {
			t.Fatalf("reference detail missing %q:\n%s", want, reference)
		}
	}
}

func TestQueryExecutorReportsLiveRunningSessionSample(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tempDir := t.TempDir()
	st, err := store.Open(filepath.Join(tempDir, "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	sessionFile := filepath.Join(tempDir, "session.jsonl")
	transcript := strings.Join([]string{
		`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Keep going on FCX profile migration."}]}}`,
		`{"type":"response_item","payload":{"type":"function_call_output","output":"tool-output-should-not-leak"}}`,
		`{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Currently compiling the compressed JSON profile migration path."}]}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(sessionFile, []byte(transcript), 0o644); err != nil {
		t.Fatalf("write session: %v", err)
	}

	now := time.Unix(1_800_000_000, 0)
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:          "/tmp/live",
		Name:          "Live",
		LastActivity:  now,
		Status:        model.StatusActive,
		PresentOnDisk: true,
		InScope:       true,
		UpdatedAt:     now,
		Sessions: []model.SessionEvidence{{
			Source:               model.SessionSourceCodex,
			SessionID:            "codex:ses_live",
			RawSessionID:         "ses_live",
			ProjectPath:          "/tmp/live",
			SessionFile:          sessionFile,
			Format:               "modern",
			LastEventAt:          now,
			LatestTurnStateKnown: true,
			LatestTurnCompleted:  false,
		}},
	}); err != nil {
		t.Fatalf("upsert project: %v", err)
	}

	executor := newQueryExecutor(st)
	executor.nowFn = func() time.Time { return now }
	result, err := executor.Execute(ctx, bossAction{
		Kind:        bossActionProjectDetail,
		ProjectPath: "/tmp/live",
		Limit:       8,
	}, StateSnapshot{}, ViewContext{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	for _, want := range []string{
		"Live assistant session context:",
		"latest turn open",
		"assistant: Currently compiling the compressed JSON profile migration path.",
	} {
		if !strings.Contains(result.Text, want) {
			t.Fatalf("tool result missing %q:\n%s", want, result.Text)
		}
	}
	if strings.Contains(result.Text, "tool-output-should-not-leak") {
		t.Fatalf("tool output should not appear in live session sample:\n%s", result.Text)
	}
	if sampleIndex, metadataIndex := strings.Index(result.Text, "Live assistant session context:"), strings.Index(result.Text, "Reference metadata"); sampleIndex < 0 || metadataIndex < 0 || sampleIndex > metadataIndex {
		t.Fatalf("live session sample should precede reference metadata:\n%s", result.Text)
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
		"Internal routing note:",
		"reference metadata:",
		"Query time:",
		"updated_at=",
		"age_at_query=",
		"/tmp/okmain",
		"FCX",
	} {
		if !strings.Contains(result.Text, want) {
			t.Fatalf("tool result missing %q:\n%s", want, result.Text)
		}
	}
	if strings.Contains(result.Text, "okmain | path:") {
		t.Fatalf("search context should not format alias matches as user-facing project mappings:\n%s", result.Text)
	}
}

func TestQueryExecutorResolvesProjectNameThroughContextSearch(t *testing.T) {
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
		LastActivity:   now.Add(-5 * time.Minute),
		Status:         model.StatusActive,
		AttentionScore: 31,
		PresentOnDisk:  true,
		InScope:        true,
		AttentionReason: []model.AttentionReason{{
			Text:   "FCX flight tuning is active in the latest assistant session",
			Weight: 20,
		}},
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("upsert project: %v", err)
	}

	executor := newQueryExecutor(st)
	executor.nowFn = func() time.Time { return now }
	result, err := executor.Execute(ctx, bossAction{
		Kind:        bossActionProjectDetail,
		ProjectName: "FCX",
		Limit:       8,
	}, StateSnapshot{}, ViewContext{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	for _, want := range []string{
		"Project detail:",
		"okmain",
		"/tmp/okmain",
		"FCX flight tuning",
	} {
		if !strings.Contains(result.Text, want) {
			t.Fatalf("tool result missing %q:\n%s", want, result.Text)
		}
	}
	for _, unwanted := range []string{`resolved "FCX" through context search`, "target note:"} {
		if strings.Contains(result.Text, unwanted) {
			t.Fatalf("tool result should not expose routing note %q:\n%s", unwanted, result.Text)
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
