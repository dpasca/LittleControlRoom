package codexapp

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"lcroom/internal/lcagent/tools"
	"lcroom/internal/todocapture"
)

type recordingLCAgentTodoHandler struct {
	requests []todocapture.Request
	response todocapture.Response
	err      error
}

func (h *recordingLCAgentTodoHandler) HandleTodoCapture(_ context.Context, request todocapture.Request) (todocapture.Response, error) {
	h.requests = append(h.requests, request)
	return h.response, h.err
}

type lcagentTodoWriteCloser struct {
	bytes.Buffer
}

func (*lcagentTodoWriteCloser) Close() error { return nil }

func TestLCAgentTodoBridgeInjectsTrustedSessionScope(t *testing.T) {
	handler := &recordingLCAgentTodoHandler{response: todocapture.Response{Add: &todocapture.AddResult{
		Scope: todocapture.ProjectScope{
			RequestedPath: "/repo/.worktrees/topic",
			ProjectPath:   "/repo",
			ProjectName:   "Little Control Room",
			FromWorktree:  true,
		},
		Disposition:     todocapture.DispositionCreated,
		Todo:            &todocapture.Todo{ID: 42, Text: "Add keyboard navigation"},
		CurrentRevision: "rev-4",
	}}}
	stdin := &lcagentTodoWriteCloser{}
	session := &lcagentSession{
		projectPath:        "/repo/.worktrees/topic",
		runID:              "trusted-run-id",
		threadID:           "trusted-thread-id",
		stdin:              stdin,
		todoCaptureHandler: handler,
		todoCaptureMode:    todocapture.ModeExplicit,
	}

	session.handleEvent([]byte(`{"type":"todo_request","id":"todo-1","action":"add","text":"Add keyboard navigation","capture_kind":"explicit_request","review_revision":"rev-3","project_path":"/attacker/project","provider":"forged","session_key":"forged-session","session_id":"child-session"}`))

	if len(handler.requests) != 1 {
		t.Fatalf("handler requests = %#v", handler.requests)
	}
	request := handler.requests[0]
	if request.Origin.ProjectPath != "/repo/.worktrees/topic" || request.Origin.Provider != string(ProviderLCAgent) || request.Origin.SessionKey != "trusted-run-id" {
		t.Fatalf("trusted origin = %#v", request.Origin)
	}
	if request.Add.Text != "Add keyboard navigation" || request.Add.CaptureKind != todocapture.CaptureExplicitRequest || request.Add.ReviewRevision != "rev-3" {
		t.Fatalf("add request = %#v", request.Add)
	}

	var envelope struct {
		Type   string           `json:"type"`
		ID     string           `json:"id"`
		Result tools.ToolResult `json:"result"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(stdin.Bytes()), &envelope); err != nil {
		t.Fatalf("decode todo_response: %v\n%s", err, stdin.String())
	}
	if envelope.Type != "todo_response" || envelope.ID != "todo-1" || !envelope.Result.Success {
		t.Fatalf("response envelope = %#v", envelope)
	}
	var response todocapture.Response
	if err := json.Unmarshal([]byte(envelope.Result.Output), &response); err != nil {
		t.Fatalf("decode handler response: %v\n%s", err, envelope.Result.Output)
	}
	if response.Add == nil || response.Add.Todo.ID != 42 {
		t.Fatalf("handler response = %#v", response)
	}

	session.mu.Lock()
	var transcript strings.Builder
	for _, entry := range session.entries {
		transcript.WriteString(entry.Text)
		transcript.WriteByte('\n')
	}
	session.mu.Unlock()
	if !strings.Contains(transcript.String(), "Added LCR TODO #42 to Little Control Room") {
		t.Fatalf("transcript missing receipt:\n%s", transcript.String())
	}
}

func TestLCAgentTodoBridgeRejectsDisallowedDeferralBeforeHandler(t *testing.T) {
	handler := &recordingLCAgentTodoHandler{}
	result, _ := (lcagentTodoBridge{
		handler: handler,
		mode:    todocapture.ModeExplicit,
		origin:  todocapture.Origin{ProjectPath: "/repo", Provider: string(ProviderLCAgent)},
	}).run(lcagentTodoRequest{
		Action:         todocapture.ActionAdd,
		Text:           "Deferred work",
		CaptureKind:    todocapture.CaptureClearDeferral,
		ReviewRevision: "rev-1",
	})
	if result.Success || !strings.Contains(result.Error, "not allowed") {
		t.Fatalf("result = %#v", result)
	}
	if len(handler.requests) != 0 {
		t.Fatalf("handler received rejected request: %#v", handler.requests)
	}
}
