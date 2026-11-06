package runtimemcp

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lcroom/internal/control"
	"lcroom/internal/model"
	"lcroom/internal/store"
)

func TestAnyToolResponseNotifiesBusyCallerOnceWithoutDeliveringCallback(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "tasks.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	path := t.TempDir()
	if err := st.BindEngineerSession(t.Context(), path, model.SessionSourceClaudeCode, "caller-key", "caller"); err != nil {
		t.Fatal(err)
	}
	task, err := st.CreateAgentTask(t.Context(), model.CreateAgentTaskInput{Title: "Review worker", OriginProjectPath: path, OriginProvider: model.SessionSourceClaudeCode, OriginSessionID: "caller", OriginSessionKey: "caller-key"})
	if err != nil {
		t.Fatal(err)
	}
	message, err := st.CreateEngineerMessage(t.Context(), control.EngineerMessage{AgentTaskID: task.ID, ProjectPath: path, Provider: control.ProviderClaudeCode, TargetSessionID: "caller", Prompt: "Review the result", State: control.EngineerMessageQueued})
	if err != nil {
		t.Fatal(err)
	}
	ready := time.Now().Add(-time.Hour)
	_, err = st.UpdateAgentTask(t.Context(), model.UpdateAgentTaskInput{ID: task.ID, ResultMessageID: &message.ID, ResultReadyAt: &ready})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.RecordEngineerMessageWaiting(t.Context(), message.ID, "recipient-busy", "Recipient busy"); err != nil {
		t.Fatal(err)
	}
	server := &Server{stateStore: st, projectPath: path, provider: "claude_code", sessionKey: "other-key"}
	call := json.RawMessage(`{"name":"list_lcr_queries","arguments":{"domain":"work"}}`)
	response, err := server.handleToolCall(t.Context(), call)
	data, _ := json.Marshal(response)
	if err != nil || strings.Contains(string(data), `"pending_results"`) {
		t.Fatalf("wrong caller received notice: %s %v", data, err)
	}
	server.sessionKey = "caller-key"
	response, err = server.handleToolCall(t.Context(), call)
	data, _ = json.Marshal(response)
	if err != nil || !strings.Contains(string(data), "pending_results") || !strings.Contains(string(data), task.ID) || !strings.Contains(string(data), "review_instruction") {
		t.Fatalf("missing notice: %s %v", data, err)
	}
	// A new MCP process for the same caller must not repeat the compact notice.
	server = &Server{stateStore: st, projectPath: path, provider: "claude_code", sessionKey: "caller-key"}
	response, err = server.handleToolCall(t.Context(), call)
	data, _ = json.Marshal(response)
	if err != nil || strings.Contains(string(data), "pending_results") {
		t.Fatalf("duplicate notice: %s %v", data, err)
	}
	current, err := st.GetAgentTask(t.Context(), task.ID)
	if err != nil || current.ResultDeliveryState() != "waiting" || !current.ResultDeliveredAt.IsZero() {
		t.Fatalf("compact notice consumed callback: %#v %v", current, err)
	}
	queued, err := st.ListQueuedEngineerMessages(t.Context(), 10)
	if err != nil || len(queued) != 1 || queued[0].ID != message.ID {
		t.Fatalf("full callback lost: %#v %v", queued, err)
	}
}
