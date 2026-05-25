package agentcontext

import (
	"path/filepath"
	"testing"
	"time"
)

type testMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type testToolCall struct {
	ID string `json:"id"`
}

func TestStoreCheckpointsAndLatestState(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	projectPath := filepath.Join(dataDir, "project")
	store := &Store[testMessage, testToolCall]{
		DataDir:     dataDir,
		Namespace:   "demo",
		ThreadID:    "thread_one",
		ProjectPath: projectPath,
		RunID:       "run_one",
		CreatedAt:   time.Date(2026, 5, 25, 8, 0, 0, 0, time.UTC),
		ApproxChars: func(messages []testMessage) int {
			return ApproxMessages(messages, func(message testMessage) MessageParts {
				return MessageParts{Role: message.Role, Content: message.Content}
			})
		},
	}

	stableMessages := []testMessage{
		{Role: "system", Content: "system"},
		{Role: "user", Content: "hello"},
	}
	if err := store.SaveCheckpoint("first_stable", stableMessages, false); err != nil {
		t.Fatalf("SaveCheckpoint() error = %v", err)
	}
	loaded, ok, err := LoadState[testMessage, testToolCall](dataDir, "demo", "thread_one", projectPath, nil)
	if err != nil || !ok {
		t.Fatalf("LoadState() ok=%v err=%v", ok, err)
	}
	if loaded.Status != StatusStable || loaded.ContextMode != ContextModeExact || loaded.LastStablePoint != "first_stable" {
		t.Fatalf("loaded stable state = %#v", loaded)
	}
	if loaded.MessageCount != 2 || loaded.ApproxChars == 0 {
		t.Fatalf("loaded message accounting = count %d approx %d", loaded.MessageCount, loaded.ApproxChars)
	}

	if err := store.MarkPendingTools("pending_turn", append(stableMessages, testMessage{Role: "assistant", Content: "calling"}), true, []testToolCall{{ID: "call_one"}}); err != nil {
		t.Fatalf("MarkPendingTools() error = %v", err)
	}
	pending, ok, err := LatestState[testMessage, testToolCall](dataDir, "demo", projectPath, nil)
	if err != nil || !ok {
		t.Fatalf("LatestState() ok=%v err=%v", ok, err)
	}
	if pending.Status != StatusPendingTools || pending.ContextMode != ContextModeCompacted {
		t.Fatalf("pending status/mode = %q/%q", pending.Status, pending.ContextMode)
	}
	if pending.LastStablePoint != "first_stable" {
		t.Fatalf("pending LastStablePoint = %q, want previous stable checkpoint", pending.LastStablePoint)
	}
	if len(pending.PendingToolCalls) != 1 || pending.PendingToolCalls[0].ID != "call_one" {
		t.Fatalf("pending tool calls = %#v", pending.PendingToolCalls)
	}
}
