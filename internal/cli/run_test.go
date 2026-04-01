package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lcroom/internal/config"
	"lcroom/internal/model"
	"lcroom/internal/store"
)

func TestLoadStoredProjectStates(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "little-control-room.sqlite")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	lastActivity := time.Unix(1_700_000_000, 0)
	snoozedUntil := lastActivity.Add(time.Hour)
	updatedAt := lastActivity.Add(2 * time.Hour)
	state := model.ProjectState{
		Path:            "/tmp/demo",
		Name:            "demo",
		LastActivity:    lastActivity,
		Status:          model.StatusIdle,
		AttentionScore:  37,
		PresentOnDisk:   true,
		RepoDirty:       true,
		RepoSyncStatus:  model.RepoSyncAhead,
		RepoAheadCount:  2,
		RepoBehindCount: 0,
		InScope:         true,
		Pinned:          true,
		SnoozedUntil:    &snoozedUntil,
		MovedFromPath:   "/tmp/old-demo",
		MovedAt:         lastActivity.Add(-time.Hour),
		AttentionReason: []model.AttentionReason{{
			Code:   "repo_dirty",
			Text:   "Git worktree has uncommitted changes",
			Weight: 15,
		}},
		Sessions: []model.SessionEvidence{{
			SessionID:           "ses_1",
			ProjectPath:         "/tmp/demo",
			DetectedProjectPath: "/tmp/demo",
			SessionFile:         "/tmp/demo/session.jsonl",
			Format:              "modern",
			SnapshotHash:        "abc",
			StartedAt:           lastActivity.Add(-30 * time.Minute),
			LastEventAt:         lastActivity,
		}},
		Artifacts: []model.ArtifactEvidence{{
			Path:      "/tmp/demo/session.jsonl",
			Kind:      "codex_session_jsonl",
			UpdatedAt: lastActivity,
			Note:      "rollout path from state_5.sqlite threads",
		}},
		UpdatedAt: updatedAt,
	}
	if err := st.UpsertProjectState(ctx, state); err != nil {
		t.Fatalf("upsert project state: %v", err)
	}

	states, err := loadStoredProjectStates(ctx, st)
	if err != nil {
		t.Fatalf("load stored project states: %v", err)
	}
	if len(states) != 1 {
		t.Fatalf("expected 1 state, got %d", len(states))
	}

	got := states[0]
	if got.Path != state.Path || got.Name != state.Name {
		t.Fatalf("unexpected project identity: %+v", got)
	}
	if !got.RepoDirty || got.RepoSyncStatus != model.RepoSyncAhead || got.RepoAheadCount != 2 {
		t.Fatalf("unexpected repo state: %+v", got)
	}
	if got.SnoozedUntil == nil || got.SnoozedUntil.Unix() != snoozedUntil.Unix() {
		t.Fatalf("unexpected snooze time: %+v", got.SnoozedUntil)
	}
	if len(got.AttentionReason) != 1 || got.AttentionReason[0].Code != "repo_dirty" {
		t.Fatalf("unexpected reasons: %+v", got.AttentionReason)
	}
	if len(got.Sessions) != 1 || got.Sessions[0].SessionID != "ses_1" {
		t.Fatalf("unexpected sessions: %+v", got.Sessions)
	}
	if len(got.Artifacts) != 1 || got.Artifacts[0].Kind != "codex_session_jsonl" {
		t.Fatalf("unexpected artifacts: %+v", got.Artifacts)
	}
}

func TestSelectOpenCodeSnapshotSessionsSortsAndFilters(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_700_000_000, 0)
	states := []model.ProjectState{
		{
			Path: "/tmp/beta",
			Sessions: []model.SessionEvidence{
				{
					SessionID:   "ses_beta_new",
					SessionFile: "/tmp/beta/opencode.db#session:ses_beta_new",
					Format:      "opencode_db",
					LastEventAt: now.Add(10 * time.Minute),
				},
				{
					SessionID:   "ses_beta_codex",
					SessionFile: "/tmp/beta/session.jsonl",
					Format:      "modern",
					LastEventAt: now.Add(9 * time.Minute),
				},
			},
		},
		{
			Path: "/tmp/alpha",
			Sessions: []model.SessionEvidence{
				{
					SessionID:   "ses_alpha_old",
					SessionFile: "/tmp/alpha/opencode.db#session:ses_alpha_old",
					Format:      "opencode_db",
					LastEventAt: now.Add(5 * time.Minute),
				},
			},
		},
	}

	selected := selectSnapshotSessions(states, "", "", 2)
	if len(selected) != 2 {
		t.Fatalf("selected len = %d, want 2", len(selected))
	}
	if selected[0].Session.SessionID != "ses_beta_new" {
		t.Fatalf("first session = %q, want ses_beta_new", selected[0].Session.SessionID)
	}
	if selected[1].Session.SessionID != "ses_beta_codex" {
		t.Fatalf("second session = %q, want ses_beta_codex", selected[1].Session.SessionID)
	}
}

func TestSelectOpenCodeSnapshotSessionsSupportsProjectAndSessionFilters(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_700_000_000, 0)
	states := []model.ProjectState{
		{
			Path: "/tmp/demo",
			Sessions: []model.SessionEvidence{
				{
					SessionID:   "ses_keep",
					SessionFile: "/tmp/demo/opencode.db#session:ses_keep",
					Format:      "opencode_db",
					LastEventAt: now,
				},
				{
					SessionID:   "ses_skip",
					SessionFile: "/tmp/demo/opencode.db#session:ses_skip",
					Format:      "opencode_db",
					LastEventAt: now.Add(-time.Minute),
				},
			},
		},
		{
			Path: "/tmp/other",
			Sessions: []model.SessionEvidence{
				{
					SessionID:   "ses_other",
					SessionFile: "/tmp/other/opencode.db#session:ses_other",
					Format:      "opencode_db",
					LastEventAt: now.Add(time.Minute),
				},
			},
		},
	}

	selected := selectSnapshotSessions(states, "/tmp/demo", "ses_keep", 3)
	if len(selected) != 1 {
		t.Fatalf("selected len = %d, want 1", len(selected))
	}
	if selected[0].State.Path != "/tmp/demo" || selected[0].Session.SessionID != "ses_keep" {
		t.Fatalf("unexpected selection: %+v", selected[0])
	}
}

func TestRunSanitizeSummariesDryRunAndApply(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tempDir := t.TempDir()
	st, err := store.Open(filepath.Join(tempDir, "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	projectPath := "/tmp/demo"
	sessionID := "ses_open_status"
	sessionFile := filepath.Join(tempDir, "session-summary.jsonl")
	if err := os.WriteFile(sessionFile, []byte(strings.Join([]string{
		`{"timestamp":"2026-03-14T06:27:12Z","type":"message","role":"user","content":[{"type":"text","text":"Please confirm the session summary behavior."}]}`,
		`{"timestamp":"2026-03-14T06:27:13Z","type":"message","role":"assistant","content":[{"type":"text","text":"I confirmed the behavior and updated the retry guard."}]}`,
	}, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:           projectPath,
		Name:           "demo",
		Status:         model.StatusIdle,
		AttentionScore: 10,
		InScope:        true,
		UpdatedAt:      now,
		Sessions: []model.SessionEvidence{{
			SessionID:            sessionID,
			ProjectPath:          projectPath,
			DetectedProjectPath:  projectPath,
			SessionFile:          sessionFile,
			Format:               "modern",
			LastEventAt:          now,
			LatestTurnStateKnown: true,
		}},
	}); err != nil {
		t.Fatalf("upsert project state: %v", err)
	}
	if _, err := st.QueueSessionClassification(ctx, model.SessionClassification{
		SessionID:         sessionID,
		ProjectPath:       projectPath,
		SessionFile:       sessionFile,
		SessionFormat:     "modern",
		SnapshotHash:      "hash-1",
		Model:             "gpt-test-mini",
		ClassifierVersion: "v1",
		SourceUpdatedAt:   now,
	}, 15*time.Minute); err != nil {
		t.Fatalf("queue classification: %v", err)
	}
	updated, err := st.UpdateSessionClassificationSummary(ctx, sessionID, "Turn completed")
	if err != nil || !updated {
		t.Fatalf("pre-sanitize summary update: updated=%v err=%v", updated, err)
	}

	if code := runSanitizeSummaries(ctx, st, config.AppConfig{SanitizeDryRun: true}); code != 0 {
		t.Fatalf("runSanitizeSummaries dry-run: code=%d", code)
	}
	stored, err := st.GetSessionClassification(ctx, sessionID)
	if err != nil {
		t.Fatalf("read classification: %v", err)
	}
	if got := strings.TrimSpace(stored.Summary); got != "Turn completed" {
		t.Fatalf("dry-run should not modify summary, got %q", got)
	}

	if code := runSanitizeSummaries(ctx, st, config.AppConfig{SanitizeApply: true}); code != 0 {
		t.Fatalf("runSanitizeSummaries apply: code=%d", code)
	}
	stored, err = st.GetSessionClassification(ctx, sessionID)
	if err != nil {
		t.Fatalf("read classification after apply: %v", err)
	}
	want := "I confirmed the behavior and updated the retry guard."
	if got := strings.TrimSpace(stored.Summary); got != want {
		t.Fatalf("sanitized summary = %q, want %q", got, want)
	}
}

func TestRunSanitizeSummariesProjectAndSessionFilter(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tempDir := t.TempDir()
	st, err := store.Open(filepath.Join(tempDir, "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	now := time.Now().UTC().Truncate(time.Second)
	makeFixture := func(project, file string) {
		_ = os.WriteFile(file, []byte(strings.Join([]string{
			`{"timestamp":"2026-03-14T06:27:12Z","type":"message","role":"user","content":[{"type":"text","text":"` + project + `"}]}`,
			`{"timestamp":"2026-03-14T06:27:13Z","type":"message","role":"assistant","content":[{"type":"text","text":"Summary for ` + project + `"}]}`,
		}, "\n")+"\n"), 0o644)
	}

	fixtureAlpha := filepath.Join(tempDir, "alpha.jsonl")
	fixtureBeta := filepath.Join(tempDir, "beta.jsonl")
	makeFixture("alpha", fixtureAlpha)
	makeFixture("beta", fixtureBeta)

	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:           "/tmp/alpha",
		Name:           "alpha",
		Status:         model.StatusIdle,
		AttentionScore: 10,
		InScope:        true,
		UpdatedAt:      now,
		Sessions: []model.SessionEvidence{{
			SessionID:            "ses_alpha",
			ProjectPath:          "/tmp/alpha",
			DetectedProjectPath:  "/tmp/alpha",
			SessionFile:          fixtureAlpha,
			Format:               "modern",
			LastEventAt:          now,
			LatestTurnStateKnown: true,
		}},
	}); err != nil {
		t.Fatalf("upsert alpha project: %v", err)
	}
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:           "/tmp/beta",
		Name:           "beta",
		Status:         model.StatusIdle,
		AttentionScore: 10,
		InScope:        true,
		UpdatedAt:      now,
		Sessions: []model.SessionEvidence{{
			SessionID:            "ses_beta",
			ProjectPath:          "/tmp/beta",
			DetectedProjectPath:  "/tmp/beta",
			SessionFile:          fixtureBeta,
			Format:               "modern",
			LastEventAt:          now,
			LatestTurnStateKnown: true,
		}},
	}); err != nil {
		t.Fatalf("upsert beta project: %v", err)
	}

	for _, classification := range []model.SessionClassification{
		{
			SessionID:         "ses_alpha",
			ProjectPath:       "/tmp/alpha",
			SessionFile:       fixtureAlpha,
			SessionFormat:     "modern",
			SnapshotHash:      "hash-alpha",
			Model:             "gpt-5-mini",
			ClassifierVersion: "v1",
			SourceUpdatedAt:   now,
		},
		{
			SessionID:         "ses_beta",
			ProjectPath:       "/tmp/beta",
			SessionFile:       fixtureBeta,
			SessionFormat:     "modern",
			SnapshotHash:      "hash-beta",
			Model:             "gpt-5-mini",
			ClassifierVersion: "v1",
			SourceUpdatedAt:   now,
		},
	} {
		if _, err := st.QueueSessionClassification(ctx, classification, 15*time.Minute); err != nil {
			t.Fatalf("queue classification %s: %v", classification.SessionID, err)
		}
		if _, err := st.UpdateSessionClassificationSummary(ctx, classification.SessionID, "Turn completed"); err != nil {
			t.Fatalf("seed bad summary %s: %v", classification.SessionID, err)
		}
	}

	if code := runSanitizeSummaries(ctx, st, config.AppConfig{
		SanitizeProject: "/tmp/alpha",
		SanitizeApply:   true,
	}); code != 0 {
		t.Fatalf("runSanitizeSummaries with project filter: code=%d", code)
	}

	alpha, err := st.GetSessionClassification(ctx, "ses_alpha")
	if err != nil {
		t.Fatalf("read alpha classification: %v", err)
	}
	if strings.TrimSpace(alpha.Summary) == "Turn completed" {
		t.Fatalf("alpha summary should be sanitized")
	}
	beta, err := st.GetSessionClassification(ctx, "ses_beta")
	if err != nil {
		t.Fatalf("read beta classification: %v", err)
	}
	if strings.TrimSpace(beta.Summary) != "Turn completed" {
		t.Fatalf("beta summary should remain unsanitized by project filter")
	}

	if code := runSanitizeSummaries(ctx, st, config.AppConfig{
		SanitizeSessionID: "ses_beta",
		SanitizeApply:     true,
	}); code != 0 {
		t.Fatalf("runSanitizeSummaries with session filter: code=%d", code)
	}
	beta, err = st.GetSessionClassification(ctx, "ses_beta")
	if err != nil {
		t.Fatalf("read beta classification after session filter: %v", err)
	}
	if strings.TrimSpace(beta.Summary) == "Turn completed" {
		t.Fatalf("beta summary should be sanitized after session-id filter")
	}
}
