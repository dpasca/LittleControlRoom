package codexapp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestColdResumeRecoversInterruptedTurnToolCallsFromRollout(t *testing.T) {
	codexHome := t.TempDir()
	projectPath := filepath.Join(t.TempDir(), "project with spaces")
	threadID := "019f49a6-7c84-7b40-841b-f870b907e782"
	rolloutDir := filepath.Join(codexHome, "sessions", "2026", "07", "10")
	if err := os.MkdirAll(rolloutDir, 0o755); err != nil {
		t.Fatalf("mkdir rollout dir: %v", err)
	}
	rolloutPath := filepath.Join(rolloutDir, "rollout-2026-07-10T01-31-17-"+threadID+".jsonl")
	writeCodexRolloutReplayTestFile(t, rolloutPath, []any{
		map[string]any{"type": "session_meta", "payload": map[string]any{"id": threadID, "cwd": projectPath}},
		map[string]any{"type": "event_msg", "payload": map[string]any{"type": "task_started", "turn_id": "prior-turn"}},
		map[string]any{"type": "event_msg", "payload": map[string]any{"type": "user_message", "message": "completed request"}},
		map[string]any{"type": "response_item", "payload": map[string]any{"type": "custom_tool_call", "id": "ctc_prior", "call_id": "call_prior", "name": "exec", "status": "completed"}},
		map[string]any{"type": "event_msg", "payload": map[string]any{"type": "task_complete", "turn_id": "prior-turn"}},
		map[string]any{"type": "event_msg", "payload": map[string]any{"type": "task_started", "turn_id": "interrupted-turn"}},
		// Model-context messages are response items but not user-visible events;
		// they must not leak into the recovered transcript.
		map[string]any{"type": "response_item", "payload": map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "internal injected context"}}}},
		map[string]any{"type": "event_msg", "payload": map[string]any{"type": "user_message", "message": "make this bulletproof"}},
		map[string]any{"type": "event_msg", "payload": map[string]any{"type": "agent_message", "message": "I’ll inspect the files first."}},
		map[string]any{"type": "response_item", "payload": map[string]any{"type": "custom_tool_call", "id": "ctc_exec", "call_id": "call_exec", "name": "exec", "status": "completed", "input": "structured tool input"}},
		map[string]any{"type": "response_item", "payload": map[string]any{"type": "custom_tool_call_output", "call_id": "call_exec", "output": "large output intentionally not replayed"}},
		map[string]any{"type": "event_msg", "payload": map[string]any{"type": "agent_message", "message": "The first check passed; I’m checking the render."}},
		map[string]any{"type": "response_item", "payload": map[string]any{"type": "function_call", "id": "fc_wait", "call_id": "call_wait", "name": "wait", "arguments": `{"cell_id":"17"}`}},
		map[string]any{"type": "response_item", "payload": map[string]any{"type": "function_call_output", "call_id": "call_wait", "output": "done"}},
	})

	recovered, err := loadCodexInterruptedTurnTranscript(LaunchRequest{
		ProjectPath: projectPath,
		ResumeID:    threadID,
		CodexHome:   codexHome,
	})
	if err != nil {
		t.Fatalf("loadCodexInterruptedTurnTranscript() error = %v", err)
	}
	wantRecovered := []TranscriptEntry{
		{Kind: TranscriptUser, Text: "make this bulletproof"},
		{Kind: TranscriptAgent, Text: "I’ll inspect the files first."},
		{ItemID: "ctc_exec", Kind: TranscriptTool, Text: "Tool exec [completed]"},
		{Kind: TranscriptAgent, Text: "The first check passed; I’m checking the render."},
		{ItemID: "fc_wait", Kind: TranscriptTool, Text: "Tool wait [completed]"},
	}
	requireTranscriptEntriesEqual(t, recovered, wantRecovered)

	// Simulate the cold thread/resume response: Codex retained the user and
	// assistant messages but omitted both tool items from the interrupted turn.
	s := &appServerSession{
		projectPath:         projectPath,
		entryIndex:          make(map[string]int),
		notify:              func() {},
		reconnectThreadID:   threadID,
		reconnectTranscript: recovered,
	}
	s.hydrateResumedThread(resumedThread{
		ID:     threadID,
		Status: resumedThreadStatus{Type: "idle"},
		Turns: []resumedTurn{{
			ID:     "interrupted-turn",
			Status: "interrupted",
			Items: []map[string]json.RawMessage{
				{"id": json.RawMessage(`"user"`), "type": json.RawMessage(`"userMessage"`), "content": json.RawMessage(`[{"type":"text","text":"make this bulletproof"}]`)},
				{"id": json.RawMessage(`"agent-one"`), "type": json.RawMessage(`"agentMessage"`), "text": json.RawMessage(`"I’ll inspect the files first."`)},
				{"id": json.RawMessage(`"agent-two"`), "type": json.RawMessage(`"agentMessage"`), "text": json.RawMessage(`"The first check passed; I’m checking the render."`)},
			},
		}},
	})

	snapshot := s.Snapshot()
	wantHydrated := []TranscriptEntry{
		{ItemID: "user", Kind: TranscriptUser, Text: "make this bulletproof"},
		{ItemID: "agent-one", Kind: TranscriptAgent, Text: "I’ll inspect the files first."},
		{ItemID: "ctc_exec", Kind: TranscriptTool, Text: "Tool exec [completed]"},
		{ItemID: "agent-two", Kind: TranscriptAgent, Text: "The first check passed; I’m checking the render."},
		{ItemID: "fc_wait", Kind: TranscriptTool, Text: "Tool wait [completed]"},
	}
	requireTranscriptEntriesEqual(t, snapshot.Entries, wantHydrated)
	if strings.Contains(snapshot.Transcript, "internal injected context") || strings.Contains(snapshot.Transcript, "large output intentionally not replayed") {
		t.Fatalf("recovered transcript leaked hidden context or tool output: %q", snapshot.Transcript)
	}
}

func TestCodexRolloutReplayIgnoresLatestCompletedTurn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout-completed.jsonl")
	writeCodexRolloutReplayTestFile(t, path, []any{
		map[string]any{"type": "session_meta", "payload": map[string]any{"id": "thread-completed", "cwd": "/tmp/demo"}},
		map[string]any{"type": "event_msg", "payload": map[string]any{"type": "task_started", "turn_id": "turn-completed"}},
		map[string]any{"type": "event_msg", "payload": map[string]any{"type": "user_message", "message": "finished request"}},
		map[string]any{"type": "response_item", "payload": map[string]any{"type": "custom_tool_call", "id": "ctc_exec", "call_id": "call_exec", "name": "exec", "status": "completed"}},
		map[string]any{"type": "event_msg", "payload": map[string]any{"type": "task_complete", "turn_id": "turn-completed"}},
	})

	entries, err := readCodexInterruptedTurnTranscript(path, "thread-completed", "/tmp/demo")
	if err != nil {
		t.Fatalf("readCodexInterruptedTurnTranscript() error = %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("completed turn replay = %#v, want empty", entries)
	}
}

func TestCodexRolloutReplayMarksPendingAbortedToolInterrupted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout-aborted.jsonl")
	writeCodexRolloutReplayTestFile(t, path, []any{
		map[string]any{"type": "session_meta", "payload": map[string]any{"id": "thread-aborted", "cwd": "/tmp/demo"}},
		map[string]any{"type": "event_msg", "payload": map[string]any{"type": "task_started", "turn_id": "turn-aborted"}},
		map[string]any{"type": "event_msg", "payload": map[string]any{"type": "user_message", "message": "aborted request"}},
		map[string]any{"type": "response_item", "payload": map[string]any{"type": "function_call", "id": "fc_wait", "call_id": "call_wait", "name": "wait"}},
		map[string]any{"type": "event_msg", "payload": map[string]any{"type": "turn_aborted", "turn_id": "turn-aborted"}},
	})

	entries, err := readCodexInterruptedTurnTranscript(path, "thread-aborted", "/tmp/demo")
	if err != nil {
		t.Fatalf("readCodexInterruptedTurnTranscript() error = %v", err)
	}
	want := []TranscriptEntry{
		{Kind: TranscriptUser, Text: "aborted request"},
		{ItemID: "fc_wait", Kind: TranscriptTool, Text: "Tool wait [interrupted]"},
	}
	requireTranscriptEntriesEqual(t, entries, want)
}

func TestReconnectTranscriptSnapshotIsEnrichedByRolloutReplay(t *testing.T) {
	live := []TranscriptEntry{
		{ItemID: "user", Kind: TranscriptUser, Text: "make this bulletproof"},
		{ItemID: "agent-one", Kind: TranscriptAgent, Text: "I’ll inspect the files first."},
		{ItemID: "agent-two", Kind: TranscriptAgent, Text: "The first check passed."},
	}
	recovered := []TranscriptEntry{
		{Kind: TranscriptUser, Text: "make this bulletproof"},
		{Kind: TranscriptAgent, Text: "I’ll inspect the files first."},
		{ItemID: "ctc_exec", Kind: TranscriptTool, Text: "Tool exec [completed]"},
		{Kind: TranscriptAgent, Text: "The first check passed."},
	}

	got := mergeReconnectTranscriptSnapshots(live, recovered)
	want := []TranscriptEntry{
		{ItemID: "user", Kind: TranscriptUser, Text: "make this bulletproof"},
		{ItemID: "agent-one", Kind: TranscriptAgent, Text: "I’ll inspect the files first."},
		{ItemID: "ctc_exec", Kind: TranscriptTool, Text: "Tool exec [completed]"},
		{ItemID: "agent-two", Kind: TranscriptAgent, Text: "The first check passed."},
	}
	requireTranscriptEntriesEqual(t, got, want)
}

func TestReconnectTranscriptSnapshotKeepsRicherLiveToolWithoutDuplicate(t *testing.T) {
	live := []TranscriptEntry{
		{ItemID: "user", Kind: TranscriptUser, Text: "check it"},
		{ItemID: "ctc_exec", Kind: TranscriptCommand, Text: "$ make test\n[command completed, exit 0]"},
	}
	recovered := []TranscriptEntry{
		{Kind: TranscriptUser, Text: "check it"},
		{ItemID: "ctc_exec", Kind: TranscriptTool, Text: "Tool exec [completed]"},
	}

	got := mergeReconnectTranscriptSnapshots(live, recovered)
	requireTranscriptEntriesEqual(t, got, live)
}

func TestCodexRolloutReplayRejectsDifferentProject(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout-wrong-project.jsonl")
	writeCodexRolloutReplayTestFile(t, path, []any{
		map[string]any{"type": "session_meta", "payload": map[string]any{"id": "thread-demo", "cwd": "/tmp/other"}},
		map[string]any{"type": "event_msg", "payload": map[string]any{"type": "task_started", "turn_id": "turn-demo"}},
	})

	_, err := readCodexInterruptedTurnTranscript(path, "thread-demo", "/tmp/expected")
	if err == nil || !strings.Contains(err.Error(), "does not match resumed project") {
		t.Fatalf("readCodexInterruptedTurnTranscript() error = %v, want project mismatch", err)
	}
}

func writeCodexRolloutReplayTestFile(t *testing.T, path string, events []any) {
	t.Helper()
	var content strings.Builder
	for _, event := range events {
		line, err := json.Marshal(event)
		if err != nil {
			t.Fatalf("marshal rollout event: %v", err)
		}
		content.Write(line)
		content.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(content.String()), 0o600); err != nil {
		t.Fatalf("write rollout: %v", err)
	}
}

func requireTranscriptEntriesEqual(t *testing.T, got, want []TranscriptEntry) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("entries = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i].ItemID != want[i].ItemID || got[i].Kind != want[i].Kind || got[i].Text != want[i].Text {
			t.Fatalf("entry %d = %#v, want %#v", i, got[i], want[i])
		}
	}
}
