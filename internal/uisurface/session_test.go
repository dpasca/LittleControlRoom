package uisurface

import (
	"strings"
	"testing"
	"time"

	"lcroom/internal/codexapp"
	"lcroom/internal/model"
)

func TestBuildLiveEngineerSessionDetailUsesSemanticSnapshotState(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.July, 11, 12, 0, 0, 0, time.UTC)
	surface := BuildLiveEngineerSessionDetail(codexapp.Snapshot{
		Provider:           codexapp.ProviderCodex,
		ProjectPath:        "/tmp/mobile",
		ThreadID:           "019f-session-demo",
		TranscriptRevision: 7,
		Phase:              codexapp.SessionPhaseRunning,
		Started:            true,
		Busy:               true,
		LastActivityAt:     now.Add(-time.Minute),
		Model:              "gpt-5.4",
		ReasoningEffort:    "high",
		PermissionLevel:    "workspace-write",
		Entries: []codexapp.TranscriptEntry{
			{Kind: codexapp.TranscriptUser, Text: "Show the session on mobile."},
			{Kind: codexapp.TranscriptTool, Text: "apply_patch internal/server"},
			{Kind: codexapp.TranscriptAgent, Text: "The read-only surface is ready."},
		},
	}, now)

	if !surface.Session.Live {
		t.Fatal("live session should be marked live")
	}
	if got, want := surface.Session.ID, "codex:019f-session-demo"; got != want {
		t.Fatalf("session id = %q, want %q", got, want)
	}
	if got, want := surface.Session.Status.Label, "Working"; got != want {
		t.Fatalf("session status = %q, want %q", got, want)
	}
	if got, want := len(surface.Entries), 3; got != want {
		t.Fatalf("entry count = %d, want %d", got, want)
	}
	if surface.Entries[0].Kind != "user" || surface.Entries[1].Kind != "tool" || surface.Entries[2].Kind != "agent" {
		t.Fatalf("entry kinds = %#v", surface.Entries)
	}
	if !strings.Contains(surface.Session.Summary, "read-only surface") {
		t.Fatalf("session summary = %q, want latest agent text", surface.Session.Summary)
	}
}

func TestBuildRecordedEngineerSessionDetailMapsStoredTurns(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.July, 11, 12, 0, 0, 0, time.UTC)
	evidence := model.SessionEvidence{
		Source:               model.SessionSourceClaudeCode,
		SessionID:            "claude_code:session-demo",
		RawSessionID:         "session-demo",
		ProjectPath:          "/tmp/mobile",
		Format:               "claude_code",
		LastEventAt:          now.Add(-10 * time.Minute),
		LatestTurnStateKnown: true,
		LatestTurnCompleted:  true,
	}
	classification := model.SessionClassification{
		Status:   model.ClassificationCompleted,
		Category: model.SessionCategoryNeedsFollowUp,
		Summary:  "Review the mobile transcript.",
	}
	excerpt := model.SessionContextExcerpt{
		Turns: []model.SessionContextTurn{
			{Index: 3, Role: "user", Text: "What comes next?"},
			{Index: 4, Role: "assistant", Text: "Build the session viewer."},
		},
	}

	surface := BuildRecordedEngineerSessionDetail(evidence, classification, excerpt, now)
	if surface.Session.Live {
		t.Fatal("recorded session should not be marked live")
	}
	if got, want := surface.Session.ProviderLabel, "Claude Code"; got != want {
		t.Fatalf("provider label = %q, want %q", got, want)
	}
	if got, want := surface.Session.Status.Label, "Follow-up"; got != want {
		t.Fatalf("status = %q, want %q", got, want)
	}
	if got, want := len(surface.Entries), 2; got != want {
		t.Fatalf("entry count = %d, want %d", got, want)
	}
	if surface.Entries[0].Label != "You" || surface.Entries[1].Label != "Claude Code" {
		t.Fatalf("entry labels = %#v", surface.Entries)
	}
}
