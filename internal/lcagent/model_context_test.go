package lcagent

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"

	"lcroom/internal/lcagent/modeladapter"
	"lcroom/internal/lcagent/session"
)

func TestWriteModelContextSnapshotUsesThreadStateRef(t *testing.T) {
	dataDir := t.TempDir()
	workspace := t.TempDir()
	now := time.Date(2026, 6, 8, 9, 0, 0, 0, time.UTC)
	writer, sessionID, err := session.NewWriter(dataDir, now, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()

	store := newThreadStateStore(dataDir, "lct_snapshot_test", workspace, sessionID, now)
	messages := []modeladapter.Message{
		{Role: "system", Content: "system prompt"},
		{Role: "user", Content: strings.Repeat("hello ", 20)},
	}
	if err := writeModelContextSnapshot(writer, store, sessionID, "tool_result", messages, false); err != nil {
		t.Fatalf("writeModelContextSnapshot() error = %v", err)
	}

	artifact, err := os.ReadFile(writer.Path())
	if err != nil {
		t.Fatal(err)
	}
	text := string(artifact)
	for _, want := range []string{`"type":"model_context_snapshot"`, `"snapshot_format":"thread_state_ref"`, `"messages_included":false`, `"content_sha256"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("snapshot artifact missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, `"messages":[`) {
		t.Fatalf("snapshot artifact should not inline exact messages:\n%s", text)
	}

	state, ok, err := loadThreadState(dataDir, store.ThreadID, workspace)
	if err != nil || !ok {
		t.Fatalf("loadThreadState() ok=%v err=%v", ok, err)
	}
	if len(state.Messages) != len(messages) || state.LastStablePoint != "tool_result" {
		t.Fatalf("state checkpoint = count %d stable %q", len(state.Messages), state.LastStablePoint)
	}

	resume, err := loadLegacyResumeContext(dataDir, writer.Path(), workspace)
	if err != nil {
		t.Fatalf("loadLegacyResumeContext() error = %v", err)
	}
	if !resume.hasExactMessages() || resume.ExactSource != "thread_state:tool_result" {
		t.Fatalf("legacy resume exact context = has %v source %q", resume.hasExactMessages(), resume.ExactSource)
	}
}
