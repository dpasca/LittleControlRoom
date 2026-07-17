package lcagent

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"lcroom/internal/lcagent/script"
	"lcroom/internal/lcagent/session"
	"lcroom/internal/todocapture"
)

func TestStdioApprovalBrokerProjectTodoRequestHasNoModelControlledScope(t *testing.T) {
	var stream bytes.Buffer
	writer, sessionID, err := session.NewWriter(t.TempDir(), time.Now(), &stream)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	broker := newStdioApprovalBroker(
		writer,
		sessionID,
		"/repo/worktree",
		strings.NewReader(`{"type":"todo_response","id":"lca_todo_1","result":{"success":true,"output":"{\"add\":{\"disposition\":\"created\"}}"}}`+"\n"),
	)
	result, err := broker.RequestProjectTodo(context.Background(), script.ProjectTodoRequest{
		Action:         todocapture.ActionAdd,
		Text:           "Add keyboard navigation",
		CaptureKind:    todocapture.CaptureExplicitRequest,
		ReviewRevision: "rev-3",
	})
	if err != nil {
		t.Fatalf("RequestProjectTodo() error = %v", err)
	}
	if !result.Success || !strings.Contains(result.Output, `"disposition":"created"`) {
		t.Fatalf("result = %#v", result)
	}

	var request map[string]json.RawMessage
	for _, line := range strings.Split(strings.TrimSpace(stream.String()), "\n") {
		var event map[string]json.RawMessage
		if json.Unmarshal([]byte(line), &event) == nil && todoRawJSONString(event["type"]) == "todo_request" {
			request = event
			break
		}
	}
	if request == nil {
		t.Fatalf("stream missing todo_request:\n%s", stream.String())
	}
	for _, forbidden := range []string{"project_path", "provider", "session_key"} {
		if _, ok := request[forbidden]; ok {
			t.Fatalf("todo_request contains model-controlled %s: %s", forbidden, stream.String())
		}
	}
	if got := todoRawJSONString(request["action"]); got != string(todocapture.ActionAdd) {
		t.Fatalf("action = %q", got)
	}
	if got := todoRawJSONString(request["capture_kind"]); got != string(todocapture.CaptureExplicitRequest) {
		t.Fatalf("capture_kind = %q", got)
	}
}

func todoRawJSONString(raw json.RawMessage) string {
	var value string
	_ = json.Unmarshal(raw, &value)
	return value
}
