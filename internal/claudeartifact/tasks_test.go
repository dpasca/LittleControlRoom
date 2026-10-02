package claudeartifact

import (
	"strings"
	"testing"
	"time"
)

func TestStreamTaskLifecycle(t *testing.T) {
	for _, tt := range []struct {
		line           string
		count          int
		kind           AsyncTaskEventKind
		status, source string
	}{
		{`{"type":"system","subtype":"task_started","task_id":"agent","task_type":"local_agent","is_backgrounded":false}`, 1, AsyncTaskLaunched, "running", AsyncTaskSourceAgent},
		{`{"type":"system","subtype":"task_progress","task_id":"agent","summary":"Testing"}`, 1, AsyncTaskUpdated, "running", ""},
		{`{"type":"system","subtype":"task_updated","task_id":"agent","patch":{"status":"killed"}}`, 1, AsyncTaskUpdated, "killed", ""},
		{`{"type":"system","subtype":"task_notification","task_id":"agent","status":"completed"}`, 1, AsyncTaskUpdated, "completed", ""},
		{`{"type":"system","subtype":"task_started","task_id":"housekeeping","ambient":true}`, 0, "", "", ""},
		{`{"type":"system","subtype":"background_tasks_changed","tasks":[{"task_id":"shell","task_type":"local_bash"},{"task_id":"housekeeping","ambient":true}]}`, 1, AsyncTaskLaunched, "running", AsyncTaskSourceBackgroundShell},
		{`{"type":"system","subtype":"task_updated","task_id":"agent","patch":{"description":"new title"}}`, 0, "", "", ""},
		{`{"type":"assistant","subtype":"task_started","task_id":"not-a-system-event"}`, 0, "", "", ""},
	} {
		events := ParseAsyncTaskEvents([]byte(tt.line))
		if len(events) != tt.count {
			t.Fatalf("%s: %#v", tt.line, events)
		}
		if tt.count > 0 && (events[0].Kind != tt.kind || events[0].Status != tt.status || events[0].Source != tt.source) {
			t.Fatalf("%s: %#v", tt.line, events)
		}
	}
	if !IsTerminalTaskStatus("killed") {
		t.Fatal("native killed status must release ownership")
	}
}

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

func TestOrphanNotificationStopsListedAndSummarizedTasks(t *testing.T) {
	launch := func(id, ts string) []byte {
		return []byte(`{"type":"user","timestamp":"` + ts + `","toolUseResult":{"backgroundTaskId":"` + id + `"}}`)
	}
	// Claude Code 2.1.284 reports the previous process's unfinished shells in
	// one notification: at most 20 task ids, a per-kind summary marker for the
	// whole set, and live markers for tasks that must stay running.
	orphans := []byte(`{"type":"queue-operation","operation":"enqueue","timestamp":"2026-10-02T06:39:57Z","content":"<task-notification>\n<task-id>task-a</task-id>\n<task-id>task-b</task-id>\n<task-id>__orphan_summary__:shell</task-id>\n<task-id>__orphan_summary_live__:task-live</task-id>\n<status>stopped</status>\n<summary>3 background shell command tasks didn't finish before the previous session ended. First 2 task ids: task-a, task-b.</summary>\n</task-notification>"}`)

	events := ParseAsyncTaskEvents(orphans)
	var ids []string
	var orphaned *AsyncTaskEvent
	for i, event := range events {
		switch event.Kind {
		case AsyncTaskUpdated:
			ids = append(ids, event.TaskID)
		case AsyncTasksOrphaned:
			orphaned = &events[i]
		default:
			t.Fatalf("event = %#v, want stopped updates and one orphan summary", event)
		}
		if event.Status != "stopped" {
			t.Fatalf("event = %#v, want stopped status", event)
		}
	}
	if strings.Join(ids, ",") != "task-a,task-b" {
		t.Fatalf("task ids = %v, want every listed task and no scan markers", ids)
	}
	if orphaned == nil || orphaned.Source != AsyncTaskSourceBackgroundShell {
		t.Fatalf("orphan summary = %#v, want background shell summary", orphaned)
	}
	if !orphaned.FinishesOrphan("task-unlisted", AsyncTaskSourceBackgroundShell) ||
		orphaned.FinishesOrphan("task-live", AsyncTaskSourceBackgroundShell) ||
		orphaned.FinishesOrphan("agent-1", AsyncTaskSourceAgent) {
		t.Fatalf("orphan summary should cover unlisted shells only, excluding live tasks and other kinds")
	}

	var tracker TurnTracker
	for _, line := range [][]byte{
		launch("task-a", "2026-10-01T18:05:21Z"),
		launch("task-b", "2026-10-01T19:00:00Z"),
		launch("task-unlisted", "2026-10-01T20:00:00Z"),
	} {
		tracker.Observe(TurnObservation{Type: "user", AsyncEvents: ParseAsyncTaskEvents(line)})
	}
	tracker.Observe(TurnObservation{Type: "queue-operation", AsyncEvents: events})
	tracker.Observe(TurnObservation{Type: "assistant", At: time.Date(2026, 10, 2, 6, 40, 0, 0, time.UTC), AssistantStopReason: "end_turn"})
	if state := tracker.State(); !state.Completed {
		t.Fatalf("state = %#v, want completed once every orphan is stopped", state)
	}
	promptAt := time.Date(2026, 10, 2, 7, 27, 0, 0, time.UTC)
	tracker.Observe(TurnObservation{Type: "user", At: promptAt, ConversationalUser: true})
	if state := tracker.State(); !state.StartedAt.Equal(promptAt) {
		t.Fatalf("turn started = %s, want latest prompt %s rather than an orphaned launch", state.StartedAt, promptAt)
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
