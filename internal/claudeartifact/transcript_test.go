package claudeartifact

import (
	"testing"
	"time"
)

func TestTurnTrackerUsesStructuredTerminalStateAndPendingTasks(t *testing.T) {
	startedAt := time.Date(2026, 8, 14, 1, 0, 0, 0, time.UTC)
	completedAt := startedAt.Add(time.Minute)
	var tracker TurnTracker

	tracker.Observe(TurnObservation{
		Type:               "user",
		At:                 startedAt,
		ConversationalUser: true,
	})
	tracker.Observe(TurnObservation{
		Type:                "assistant",
		At:                  startedAt.Add(30 * time.Second),
		AssistantStopReason: "tool_use",
		AsyncEvents: []AsyncTaskEvent{{
			Kind:   AsyncTaskLaunched,
			TaskID: "task-1",
			At:     startedAt.Add(30 * time.Second),
		}},
	})
	tracker.Observe(TurnObservation{
		Type:                "assistant",
		At:                  completedAt,
		AssistantStopReason: "end_turn",
	})

	state := tracker.State()
	if !state.Known || state.Completed || !state.Verified {
		t.Fatalf("state with pending task = %#v, want known incomplete", state)
	}
	if !state.StartedAt.Equal(startedAt) {
		t.Fatalf("pending task start = %v, want busy span start %v", state.StartedAt, startedAt)
	}

	tracker.Observe(TurnObservation{
		Type: "queue-operation",
		At:   completedAt.Add(time.Second),
		AsyncEvents: []AsyncTaskEvent{{
			Kind:   AsyncTaskUpdated,
			TaskID: "task-1",
			Status: "completed",
			At:     completedAt.Add(time.Second),
		}},
	})
	tracker.Observe(TurnObservation{
		Type:    "system",
		Subtype: "turn_duration",
		At:      completedAt.Add(2 * time.Second),
	})

	state = tracker.State()
	if !state.Known || !state.Completed || !state.Verified {
		t.Fatalf("terminal state = %#v, want known completed", state)
	}
	if !state.StartedAt.IsZero() {
		t.Fatalf("completed state retained start %v", state.StartedAt)
	}
	if !state.UpdatedAt.Equal(completedAt.Add(2 * time.Second)) {
		t.Fatalf("completed state time = %v", state.UpdatedAt)
	}
}

func TestTurnTrackerMarksMissingAssistantStopReasonUnverified(t *testing.T) {
	var tracker TurnTracker
	tracker.Observe(TurnObservation{
		Type: "assistant",
		At:   time.Date(2026, 8, 14, 1, 0, 0, 0, time.UTC),
	})

	state := tracker.State()
	if !state.Known || state.Completed || state.Verified {
		t.Fatalf("state = %#v, want known incomplete but unverified", state)
	}
}

func TestConversationTrackerFiltersGeneratedUserChain(t *testing.T) {
	var tracker ConversationTracker

	entries := []struct {
		entry TranscriptEntry
		want  bool
	}{
		{
			entry: TranscriptEntry{Type: "system", UUID: "completed-turn"},
			want:  false,
		},
		{
			entry: TranscriptEntry{
				Type:       "user",
				UUID:       "command-caveat",
				ParentUUID: "completed-turn",
				IsMeta:     true,
			},
			want: false,
		},
		{
			entry: TranscriptEntry{
				Type:       "user",
				UUID:       "command-name",
				ParentUUID: "command-caveat",
			},
			want: false,
		},
		{
			entry: TranscriptEntry{
				Type:       "user",
				UUID:       "command-output",
				ParentUUID: "command-name",
			},
			want: false,
		},
		{
			entry: TranscriptEntry{Type: "file-history-snapshot"},
			want:  false,
		},
		{
			entry: TranscriptEntry{
				Type:       "user",
				UUID:       "legacy-prompt",
				ParentUUID: "command-output",
			},
			want: true,
		},
	}

	for i, tt := range entries {
		if got := tracker.Observe(tt.entry); got != tt.want {
			t.Fatalf("Observe(entry %d) = %v, want %v", i, got, tt.want)
		}
	}
}

func TestConversationTrackerSubmittedPromptBreaksGeneratedChain(t *testing.T) {
	var tracker ConversationTracker

	if tracker.Observe(TranscriptEntry{
		Type:   "user",
		UUID:   "command-caveat",
		IsMeta: true,
	}) {
		t.Fatal("meta command caveat should not be conversational")
	}
	if tracker.Observe(TranscriptEntry{
		Type:       "user",
		UUID:       "command-output",
		ParentUUID: "command-caveat",
	}) {
		t.Fatal("generated command output should not be conversational")
	}
	if !tracker.Observe(TranscriptEntry{
		Type:         "user",
		UUID:         "real-prompt",
		ParentUUID:   "command-output",
		PromptSource: "typed",
		OriginKind:   "human",
	}) {
		t.Fatal("explicitly submitted prompt should be conversational")
	}
}

func TestConversationTrackerRejectsNonHumanOrigin(t *testing.T) {
	var tracker ConversationTracker

	if tracker.Observe(TranscriptEntry{
		Type:         "user",
		UUID:         "task-notification",
		PromptSource: "sdk",
		OriginKind:   "task-notification",
	}) {
		t.Fatal("task notification should not be conversational")
	}
	tracker.Observe(TranscriptEntry{
		Type:       "assistant",
		UUID:       "assistant-reply",
		ParentUUID: "task-notification",
	})
	if !tracker.Observe(TranscriptEntry{
		Type:         "user",
		UUID:         "sdk-prompt",
		ParentUUID:   "assistant-reply",
		PromptSource: "sdk",
	}) {
		t.Fatal("SDK-submitted prompt should be conversational")
	}
}

func TestConversationTrackerRejectsCompactSummary(t *testing.T) {
	var tracker ConversationTracker

	if tracker.Observe(TranscriptEntry{
		Type:             "user",
		UUID:             "compact-summary",
		IsCompactSummary: true,
	}) {
		t.Fatal("provider-generated compact summary should not be conversational")
	}
	if !tracker.Observe(TranscriptEntry{
		Type:         "user",
		UUID:         "next-prompt",
		ParentUUID:   "compact-summary",
		PromptSource: "typed",
		OriginKind:   "human",
	}) {
		t.Fatal("human prompt after compact summary should be conversational")
	}
}

func TestTurnTrackerKeepsBusySpanAcrossTaskNotificationTurns(t *testing.T) {
	promptAt := time.Date(2026, 10, 3, 20, 29, 0, 0, time.UTC)
	var tracker TurnTracker
	launch := func(id string, at time.Time) {
		tracker.Observe(TurnObservation{
			Type:                "assistant",
			At:                  at,
			AssistantStopReason: "tool_use",
			AsyncEvents:         []AsyncTaskEvent{{Kind: AsyncTaskLaunched, TaskID: id, At: at}},
		})
		tracker.Observe(TurnObservation{Type: "assistant", At: at.Add(time.Second), AssistantStopReason: "end_turn"})
	}
	notify := func(id string, at time.Time) {
		tracker.Observe(TurnObservation{
			Type:        "user",
			At:          at,
			AsyncEvents: []AsyncTaskEvent{{Kind: AsyncTaskUpdated, TaskID: id, Status: "completed", At: at}},
		})
	}

	tracker.Observe(TurnObservation{Type: "user", At: promptAt, ConversationalUser: true})
	launch("build", promptAt.Add(3*time.Minute))
	notify("build", promptAt.Add(4*time.Minute))
	launch("windows", promptAt.Add(21*time.Minute))
	notify("other", promptAt.Add(42*time.Minute))
	launch("smoke", promptAt.Add(43*time.Minute))

	if state := tracker.State(); state.Completed || !state.StartedAt.Equal(promptAt) {
		t.Fatalf("state = %#v, want open busy span from the prompt %v", state, promptAt)
	}

	notify("windows", promptAt.Add(58*time.Minute))
	notify("smoke", promptAt.Add(59*time.Minute))
	tracker.Observe(TurnObservation{Type: "assistant", At: promptAt.Add(60 * time.Minute), AssistantStopReason: "end_turn"})
	if state := tracker.State(); !state.Completed || !state.StartedAt.IsZero() {
		t.Fatalf("settled state = %#v, want completed without a start", state)
	}

	nextAt := promptAt.Add(2 * time.Hour)
	tracker.Observe(TurnObservation{Type: "user", At: nextAt, ConversationalUser: true})
	if state := tracker.State(); !state.StartedAt.Equal(nextAt) {
		t.Fatalf("next span start = %v, want %v", state.StartedAt, nextAt)
	}
}
