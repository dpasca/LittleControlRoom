package cli

import (
	"context"
	"path/filepath"
	"testing"
	"time"

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
		Note:            "hello",
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

	selected := selectOpenCodeSnapshotSessions(states, "", "", 2)
	if len(selected) != 2 {
		t.Fatalf("selected len = %d, want 2", len(selected))
	}
	if selected[0].Session.SessionID != "ses_beta_new" {
		t.Fatalf("first session = %q, want ses_beta_new", selected[0].Session.SessionID)
	}
	if selected[1].Session.SessionID != "ses_alpha_old" {
		t.Fatalf("second session = %q, want ses_alpha_old", selected[1].Session.SessionID)
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

	selected := selectOpenCodeSnapshotSessions(states, "/tmp/demo", "ses_keep", 3)
	if len(selected) != 1 {
		t.Fatalf("selected len = %d, want 1", len(selected))
	}
	if selected[0].State.Path != "/tmp/demo" || selected[0].Session.SessionID != "ses_keep" {
		t.Fatalf("unexpected selection: %+v", selected[0])
	}
}
