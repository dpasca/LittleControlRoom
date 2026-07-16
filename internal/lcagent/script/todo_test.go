package script

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"lcroom/internal/lcagent/session"
	"lcroom/internal/lcagent/tools"
	"lcroom/internal/todocapture"
)

type recordingProjectTodoBroker struct {
	requests []ProjectTodoRequest
	result   tools.ToolResult
	err      error
}

func (b *recordingProjectTodoBroker) RequestProjectTodo(_ context.Context, request ProjectTodoRequest) (tools.ToolResult, error) {
	b.requests = append(b.requests, request)
	if b.err != nil {
		return tools.ToolResult{}, b.err
	}
	return b.result, nil
}

func TestRunnerProjectTodoToolsForwardPathlessRequests(t *testing.T) {
	var stream bytes.Buffer
	writer, sessionID, err := session.NewWriter(t.TempDir(), time.Now(), &stream)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	broker := &recordingProjectTodoBroker{result: tools.ToolResult{Success: true, Output: `{"ok":true}`}}
	runner := Runner{
		Session:         writer,
		SessionID:       sessionID,
		ProjectTodos:    broker,
		TodoCaptureMode: todocapture.ModeExplicitAndClearDeferrals,
	}

	if result, err := runner.RunTool(context.Background(), Action{Type: "tool_call", Tool: "list_project_todos", Args: raw(`{}`)}); err != nil || !result.Success {
		t.Fatalf("list_project_todos result = %#v, error = %v", result, err)
	}
	if result, err := runner.RunTool(context.Background(), Action{
		Type: "tool_call",
		Tool: "add_project_todo",
		Args: raw(`{"text":" Add accessibility smoke test ","capture_kind":"clear_deferral","review_revision":" rev-7 "}`),
	}); err != nil || !result.Success {
		t.Fatalf("add_project_todo result = %#v, error = %v", result, err)
	}

	if len(broker.requests) != 2 {
		t.Fatalf("broker requests = %#v, want two", broker.requests)
	}
	if got := broker.requests[0]; got.Action != todocapture.ActionList || got.SessionID != sessionID {
		t.Fatalf("list request = %#v", got)
	}
	got := broker.requests[1]
	if got.Action != todocapture.ActionAdd || got.SessionID != sessionID || got.Text != "Add accessibility smoke test" || got.CaptureKind != todocapture.CaptureClearDeferral || got.ReviewRevision != "rev-7" {
		t.Fatalf("add request = %#v", got)
	}
}

func TestRunnerProjectTodoToolsEnforceCaptureMode(t *testing.T) {
	var stream bytes.Buffer
	writer, sessionID, err := session.NewWriter(t.TempDir(), time.Now(), &stream)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	broker := &recordingProjectTodoBroker{result: tools.ToolResult{Success: true}}
	runner := Runner{
		Session:         writer,
		SessionID:       sessionID,
		ProjectTodos:    broker,
		TodoCaptureMode: todocapture.ModeExplicit,
	}

	result, err := runner.RunTool(context.Background(), Action{
		Type: "tool_call",
		Tool: "add_project_todo",
		Args: raw(`{"text":"Deferred work","capture_kind":"clear_deferral","review_revision":"rev-1"}`),
	})
	if err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("RunTool() error = %v, want capture-mode rejection", err)
	}
	if result.Success || !strings.Contains(result.Error, "not allowed") {
		t.Fatalf("result = %#v, want capture-mode rejection", result)
	}
	if len(broker.requests) != 0 {
		t.Fatalf("broker received disallowed request: %#v", broker.requests)
	}
}
