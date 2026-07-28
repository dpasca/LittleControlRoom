package claudeartifact

import "testing"

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
