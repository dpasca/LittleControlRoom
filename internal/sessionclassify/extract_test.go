package sessionclassify

import (
	"os"
	"path/filepath"
	"testing"
)

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
