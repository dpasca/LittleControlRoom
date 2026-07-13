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

func TestBuildLiveEngineerSessionDetailKeepsConversationWhenActivityIsDense(t *testing.T) {
	t.Parallel()
	entries := []codexapp.TranscriptEntry{
		{ItemID: "user-old", Kind: codexapp.TranscriptUser, Text: "Keep the earlier question visible."},
		{ItemID: "agent-old", Kind: codexapp.TranscriptAgent, Text: "The earlier answer is still useful."},
	}
	for i := 0; i < 30; i++ {
		entries = append(entries, codexapp.TranscriptEntry{
			ItemID: "command-output",
			Kind:   codexapp.TranscriptCommand,
			Text:   strings.Repeat("large command output ", 500),
		})
	}
	entries = append(entries, codexapp.TranscriptEntry{
		ItemID: "agent-latest",
		Kind:   codexapp.TranscriptAgent,
		Text:   "| Slice | State |\n| --- | --- |\n| Mobile | Ready |",
	})

	surface := BuildLiveEngineerSessionDetail(codexapp.Snapshot{
		Provider:    codexapp.ProviderCodex,
		ProjectPath: "/tmp/mobile-dense-activity",
		ThreadID:    "dense-activity",
		Started:     true,
		Busy:        true,
		Entries:     entries,
	}, time.Now())

	conversationTexts := []string{}
	activityCount := 0
	totalRunes := 0
	for _, entry := range surface.Entries {
		totalRunes += len([]rune(entry.Text))
		switch entry.Kind {
		case "user", "agent", "plan", "error":
			conversationTexts = append(conversationTexts, entry.Text)
		case "command":
			activityCount++
			if got := len([]rune(entry.Text)); got > engineerSessionActivityEntryRuneLimit {
				t.Fatalf("activity entry runes = %d, want at most %d", got, engineerSessionActivityEntryRuneLimit)
			}
		}
	}
	if got, want := conversationTexts, []string{
		"Keep the earlier question visible.",
		"The earlier answer is still useful.",
		"| Slice | State |\n| --- | --- |\n| Mobile | Ready |",
	}; strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("conversation entries = %#v, want %#v", got, want)
	}
	if activityCount == 0 {
		t.Fatal("dense transcript should retain a bounded recent activity slice")
	}
	if totalRunes > engineerSessionTextRuneLimit {
		t.Fatalf("transcript runes = %d, want at most %d", totalRunes, engineerSessionTextRuneLimit)
	}
	if !surface.Truncated {
		t.Fatal("dense transcript should report that recent activity was bounded")
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
