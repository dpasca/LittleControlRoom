package codex

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"lcroom/internal/scanner"
)

func TestDetectorParsesModernAndLegacyFixtures(t *testing.T) {
	codexHome := filepath.Join("..", "..", "..", "testdata", "codex_footprint")
	d := New(codexHome)

	scope := scanner.NewPathScope([]string{"/workspaces/repos"}, nil)
	got, err := d.Detect(context.Background(), scope)
	if err != nil {
		t.Fatalf("Detect() error = %v", err)
	}

	if len(got) != 3 {
		t.Fatalf("expected 3 detected projects, got %d", len(got))
	}

	modernPath := "/workspaces/repos/LittleControlRoom"
	modern := got[modernPath]
	if modern == nil {
		t.Fatalf("missing modern project %s", modernPath)
	}
	if modern.ErrorCount != 1 {
		t.Fatalf("expected modern error_count=1, got %d", modern.ErrorCount)
	}
	if len(modern.Sessions) == 0 || modern.Sessions[0].Format != "modern" {
		t.Fatalf("expected modern session evidence, got %#v", modern.Sessions)
	}
	if !modern.Sessions[0].LatestTurnStateKnown || !modern.Sessions[0].LatestTurnCompleted {
		t.Fatalf("expected modern latest turn to be completed, got known=%v completed=%v", modern.Sessions[0].LatestTurnStateKnown, modern.Sessions[0].LatestTurnCompleted)
	}
	if !modern.Sessions[0].LatestTurnStartedAt.IsZero() {
		t.Fatalf("expected completed modern turn to clear turn start time, got %v", modern.Sessions[0].LatestTurnStartedAt)
	}

	legacyPath := "/workspaces/repos/legacy-demo"
	legacy := got[legacyPath]
	if legacy == nil {
		t.Fatalf("missing legacy project %s", legacyPath)
	}
	if len(legacy.Sessions) == 0 || legacy.Sessions[0].Format != "legacy" {
		t.Fatalf("expected legacy session evidence, got %#v", legacy.Sessions)
	}

	archivedPath := "/workspaces/repos/archived-demo"
	if got[archivedPath] == nil {
		t.Fatalf("missing archived project %s", archivedPath)
	}
}

func TestExtractLegacyCWD(t *testing.T) {
	text := `{"type":"message","content":[{"text":"<environment_context>\nCurrent working directory: /tmp/demo\nApproval policy: on-request\n</environment_context>"}]}`
	cwd := extractLegacyCWD(text)
	if cwd != "/tmp/demo" {
		t.Fatalf("extractLegacyCWD() = %q, want %q", cwd, "/tmp/demo")
	}
}

func TestCountNonZeroExitCodes(t *testing.T) {
	in := "Process exited with code 0\nProcess exited with code 1\nProcess exited with code 2"
	if got := countNonZeroExitCodes(in); got != 2 {
		t.Fatalf("countNonZeroExitCodes() = %d, want 2", got)
	}
}

func TestExtractTurnLifecycle(t *testing.T) {
	startLine := `{"timestamp":"2026-03-05T09:04:00.238Z","type":"event_msg","payload":{"type":"task_started"}}`
	event, ok := extractTurnLifecycle(startLine)
	if !ok || event.completed {
		t.Fatalf("task_started parse = (%#v, ok=%v), want completed=false, ok=true", event, ok)
	}
	if event.timestamp.IsZero() {
		t.Fatalf("task_started timestamp = zero, want parsed timestamp")
	}

	completeLine := `{"timestamp":"2026-03-05T09:04:10.657Z","type":"event_msg","payload":{"type":"task_complete"}}`
	event, ok = extractTurnLifecycle(completeLine)
	if !ok || !event.completed {
		t.Fatalf("task_complete parse = (%#v, ok=%v), want completed=true, ok=true", event, ok)
	}
	if event.timestamp.IsZero() {
		t.Fatalf("task_complete timestamp = zero, want parsed timestamp")
	}

	abortedLine := `{"timestamp":"2026-03-05T09:04:12.000Z","type":"event_msg","payload":{"type":"turn_aborted","reason":"interrupted"}}`
	event, ok = extractTurnLifecycle(abortedLine)
	if !ok || !event.completed {
		t.Fatalf("turn_aborted parse = (%#v, ok=%v), want completed=true, ok=true", event, ok)
	}
	if event.timestamp.IsZero() {
		t.Fatalf("turn_aborted timestamp = zero, want parsed timestamp")
	}
}

func TestDetectTurnStateFromTailIgnoresControlOnlyTaskStart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	content := "" +
		"{\"timestamp\":\"2026-03-05T09:04:12.000Z\",\"type\":\"event_msg\",\"payload\":{\"type\":\"task_complete\"}}\n" +
		"{\"timestamp\":\"2026-03-05T09:05:00.000Z\",\"type\":\"event_msg\",\"payload\":{\"type\":\"task_started\"}}\n" +
		"{\"timestamp\":\"2026-03-05T09:05:00.001Z\",\"type\":\"turn_context\",\"payload\":{\"turn_id\":\"turn_control\"}}\n" +
		"{\"timestamp\":\"2026-03-05T09:05:00.002Z\",\"type\":\"response_item\",\"payload\":{\"type\":\"message\",\"role\":\"developer\",\"content\":[{\"type\":\"input_text\",\"text\":\"model switch\"}]}}\n" +
		"{\"timestamp\":\"2026-03-05T09:05:00.003Z\",\"type\":\"event_msg\",\"payload\":{\"type\":\"token_count\"}}\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write rollout: %v", err)
	}

	d := New("")
	state := d.detectTurnStateFromTail(path)
	if !state.known || !state.completed {
		t.Fatalf("detectTurnStateFromTail() = %#v, want latest stable completed state", state)
	}
	if !state.startedAt.IsZero() {
		t.Fatalf("startedAt = %v, want zero after control-only task start is ignored", state.startedAt)
	}
}
