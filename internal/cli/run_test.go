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
