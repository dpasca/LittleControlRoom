package sessionclassify

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lcroom/internal/model"
)

func TestExtractClaudeCodeTranscriptHidesGeneratedLocalCommandRecords(t *testing.T) {
	sessionFile := filepath.Join(t.TempDir(), "session.jsonl")
	lines := []string{
		`{"type":"assistant","uuid":"previous-answer","message":{"role":"assistant","content":[{"type":"text","text":"Choose one of the open items."}]}}`,
		`{"type":"user","isMeta":true,"uuid":"command-caveat","parentUuid":"previous-answer","message":{"role":"user","content":"<local-command-caveat>generated records follow</local-command-caveat>"}}`,
		`{"type":"user","uuid":"command-name","parentUuid":"command-caveat","message":{"role":"user","content":"<command-name>/model</command-name>"}}`,
		`{"type":"user","uuid":"command-output","parentUuid":"command-name","message":{"role":"user","content":"<local-command-stdout>Set model to Opus 5</local-command-stdout>"}}`,
		`{"type":"file-history-snapshot"}`,
		`{"type":"user","uuid":"escaped-prompt","parentUuid":"command-output","promptSource":"typed","origin":{"kind":"human"},"message":{"role":"user","content":"which one you think is more improtant at this point"}}`,
		`{"type":"file-history-snapshot"}`,
		`{"type":"user","uuid":"continued-prompt","parentUuid":"command-output","promptSource":"typed","origin":{"kind":"human"},"message":{"role":"user","content":"which one you think is more improtant at this point"}}`,
		`{"type":"assistant","uuid":"answer","parentUuid":"continued-prompt","message":{"role":"assistant","content":[{"type":"text","text":"Config plumbing is the most important."}]}}`,
	}
	if err := os.WriteFile(sessionFile, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("write session file: %v", err)
	}

	items, err := extractClaudeCodeTranscript(sessionFile)
	if err != nil {
		t.Fatalf("extractClaudeCodeTranscript() error = %v", err)
	}
	if got, want := len(items), 3; got != want {
		t.Fatalf("transcript item count = %d, want %d: %#v", got, want, items)
	}
	for _, item := range items {
		if strings.Contains(item.Text, "<command-") || strings.Contains(item.Text, "<local-command-") {
			t.Fatalf("generated local command XML leaked into classifier transcript: %#v", item)
		}
	}
	if items[1].Role != "user" || items[1].Text != "which one you think is more improtant at this point" {
		t.Fatalf("user prompt item = %#v", items[1])
	}
}

func TestExtractCodexTurnLifecycleEventTreatsTurnAbortedAsCompleted(t *testing.T) {
	line := `{"timestamp":"2026-03-05T09:04:12.000Z","type":"event_msg","payload":{"type":"turn_aborted","reason":"interrupted"}}`
	event, ok := extractCodexTurnLifecycleEvent(line)
	if !ok || !event.completed {
		t.Fatalf("extractCodexTurnLifecycleEvent(turn_aborted) = (%#v, ok=%v), want completed=true, ok=true", event, ok)
	}
	if event.timestamp.IsZero() {
		t.Fatalf("turn_aborted timestamp = zero, want parsed timestamp")
	}
}

func TestDetectCodexTurnLifecycleTreatsTurnAbortedAsCompleted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	content := "" +
		"{\"timestamp\":\"2026-03-05T09:04:00.238Z\",\"type\":\"event_msg\",\"payload\":{\"type\":\"task_started\"}}\n" +
		"{\"timestamp\":\"2026-03-05T09:04:12.000Z\",\"type\":\"event_msg\",\"payload\":{\"type\":\"turn_aborted\",\"reason\":\"interrupted\"}}\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write session file: %v", err)
	}

	state, err := detectCodexTurnLifecycle(path)
	if err != nil {
		t.Fatalf("detectCodexTurnLifecycle() error = %v", err)
	}
	if !state.known || !state.completed {
		t.Fatalf("detectCodexTurnLifecycle() = %#v, want known=true completed=true", state)
	}
	if !state.startedAt.IsZero() {
		t.Fatalf("startedAt = %v, want zero for completed/aborted turn", state.startedAt)
	}
}

func TestDetectCodexTurnLifecycleIgnoresControlOnlyTaskStart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	content := "" +
		"{\"timestamp\":\"2026-03-05T09:04:12.000Z\",\"type\":\"event_msg\",\"payload\":{\"type\":\"task_complete\"}}\n" +
		"{\"timestamp\":\"2026-03-05T09:05:00.000Z\",\"type\":\"event_msg\",\"payload\":{\"type\":\"task_started\"}}\n" +
		"{\"timestamp\":\"2026-03-05T09:05:00.001Z\",\"type\":\"turn_context\",\"payload\":{\"turn_id\":\"turn_control\"}}\n" +
		"{\"timestamp\":\"2026-03-05T09:05:00.002Z\",\"type\":\"response_item\",\"payload\":{\"type\":\"message\",\"role\":\"developer\",\"content\":[{\"type\":\"input_text\",\"text\":\"model switch\"}]}}\n" +
		"{\"timestamp\":\"2026-03-05T09:05:00.003Z\",\"type\":\"event_msg\",\"payload\":{\"type\":\"token_count\"}}\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write session file: %v", err)
	}

	state, err := detectCodexTurnLifecycle(path)
	if err != nil {
		t.Fatalf("detectCodexTurnLifecycle() error = %v", err)
	}
	if !state.known || !state.completed {
		t.Fatalf("detectCodexTurnLifecycle() = %#v, want latest stable completed state", state)
	}
	if !state.startedAt.IsZero() {
		t.Fatalf("startedAt = %v, want zero after control-only task start is ignored", state.startedAt)
	}
}

func TestDetectLCAgentTurnLifecycleTreatsTurnAbortedAsCompleted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lcagent.jsonl")
	content := "" +
		"{\"type\":\"user_message\",\"timestamp\":\"2026-06-13T06:20:00Z\",\"message\":\"build the game\"}\n" +
		"{\"type\":\"turn_aborted\",\"timestamp\":\"2026-06-13T06:21:00Z\",\"reason\":\"interrupted\"}\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write session file: %v", err)
	}

	state, err := detectLCAgentTurnLifecycle(path)
	if err != nil {
		t.Fatalf("detectLCAgentTurnLifecycle() error = %v", err)
	}
	if !state.known || !state.completed {
		t.Fatalf("detectLCAgentTurnLifecycle() = %#v, want known=true completed=true", state)
	}
	if !state.startedAt.IsZero() {
		t.Fatalf("startedAt = %v, want zero for aborted turn", state.startedAt)
	}
}

func TestRecoverSessionTurnStateRefreshesStaleLCAgentEvidence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lcagent.jsonl")
	userAt := time.Date(2026, 6, 13, 6, 20, 0, 0, time.UTC)
	content := "" +
		"{\"type\":\"user_message\",\"timestamp\":\"" + userAt.Format(time.RFC3339Nano) + "\",\"message\":\"build the game\"}\n" +
		"{\"type\":\"turn_aborted\",\"timestamp\":\"2026-06-13T06:21:00Z\",\"reason\":\"interrupted\"}\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write session file: %v", err)
	}

	session := model.SessionEvidence{
		SessionFile:          path,
		Format:               "lcagent_jsonl",
		LatestTurnStateKnown: true,
		LatestTurnCompleted:  false,
		LatestTurnStartedAt:  userAt,
	}
	if err := RecoverSessionTurnState(&session); err != nil {
		t.Fatalf("RecoverSessionTurnState() error = %v", err)
	}
	if !session.LatestTurnStateKnown || !session.LatestTurnCompleted {
		t.Fatalf("recovered state known=%v completed=%v, want settled aborted turn", session.LatestTurnStateKnown, session.LatestTurnCompleted)
	}
	if !session.LatestTurnStartedAt.IsZero() {
		t.Fatalf("LatestTurnStartedAt = %v, want zero after abort", session.LatestTurnStartedAt)
	}
}
