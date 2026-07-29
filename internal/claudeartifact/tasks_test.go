package claudeartifact

import (
	"testing"
	"time"
)

func TestParseAsyncTaskEventsReadsBackgroundShellLaunch(t *testing.T) {
	line := []byte(`{"type":"user","timestamp":"2026-07-29T00:40:21.697Z","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"ignored display text"}]},"toolUseResult":{"backgroundTaskId":"task-123"}}`)

	events := ParseAsyncTaskEvents(line)
	if len(events) != 1 {
		t.Fatalf("events = %#v, want one launch", events)
	}
	event := events[0]
	if event.Kind != AsyncTaskLaunched || event.TaskID != "task-123" || event.ToolUseID != "toolu_1" {
		t.Fatalf("event = %#v, want structured task launch", event)
	}
	if event.Source != AsyncTaskSourceBackgroundShell || event.Status != "running" {
		t.Fatalf("event source/status = %q/%q", event.Source, event.Status)
	}
	wantAt := time.Date(2026, 7, 29, 0, 40, 21, 697000000, time.UTC)
	if !event.At.Equal(wantAt) {
		t.Fatalf("event.At = %v, want %v", event.At, wantAt)
	}
}

func TestParseAsyncTaskEventsAcceptsStreamJSONToolResultField(t *testing.T) {
	line := []byte(`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_stream"}]},"tool_use_result":{"backgroundTaskId":"stream-task"}}`)

	events := ParseAsyncTaskEvents(line)
	if len(events) != 1 || events[0].TaskID != "stream-task" || events[0].ToolUseID != "toolu_stream" {
		t.Fatalf("events = %#v, want snake-case stream task launch", events)
	}
}

func TestParseAsyncTaskEventsReadsTaskNotification(t *testing.T) {
	line := []byte(`{"type":"user","timestamp":"2026-07-29T00:41:00Z","origin":{"kind":"task-notification"},"message":{"content":"<task-notification>\n<task-id>task-123</task-id>\n<output-file>/tmp/task-123.output</output-file>\n<status>completed</status>\n<summary>Telemetry completed (exit code 0)</summary>\n</task-notification>"}}`)

	events := ParseAsyncTaskEvents(line)
	if len(events) != 1 {
		t.Fatalf("events = %#v, want one update", events)
	}
	event := events[0]
	if event.Kind != AsyncTaskUpdated || event.TaskID != "task-123" || event.Status != "completed" {
		t.Fatalf("event = %#v, want terminal task update", event)
	}
	if event.OutputPath != "/tmp/task-123.output" || event.Summary != "Telemetry completed (exit code 0)" {
		t.Fatalf("event output/summary = %q/%q", event.OutputPath, event.Summary)
	}
	if !IsTerminalTaskStatus(event.Status) {
		t.Fatalf("completed task should be terminal")
	}
}

func TestStoppedTaskStatusIsTerminal(t *testing.T) {
	line := []byte(`{"type":"queue-operation","operation":"enqueue","content":"<task-notification>\n<task-id>task-stopped</task-id>\n<status>stopped</status>\n<summary>No completion record was found.</summary>\n</task-notification>"}`)
	events := ParseAsyncTaskEvents(line)
	if len(events) != 1 || events[0].TaskID != "task-stopped" || events[0].Status != "stopped" {
		t.Fatalf("events = %#v, want structured stopped notification", events)
	}
	if !IsTerminalTaskStatus(events[0].Status) {
		t.Fatal("stopped task should be terminal")
	}
	if IsTerminalTaskStatus("running") {
		t.Fatal("running task should not be terminal")
	}
}
