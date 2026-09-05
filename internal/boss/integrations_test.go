package boss

import (
	"encoding/json"
	"strings"
	"testing"

	"lcroom/internal/config"
	"lcroom/internal/control"
	"lcroom/internal/integrations"
	"lcroom/internal/lcagent/modeladapter"
)

func TestHelpChatIntegrationProposalIsTerminalAndConfirmable(t *testing.T) {
	args := `{"capability":"integrations.manage","arguments":{"provider":"claude_code","scope":"user","action":"install_plugin","plugin":"example@curated","expected_revision":"` + strings.Repeat("a", 64) + `"}}`
	client := &scriptedHelpChatModel{model: "test-model", completions: []modeladapter.Completion{{Model: "test-model", Message: modeladapter.Message{Role: "assistant", ToolCalls: []modeladapter.ToolCall{{ID: "integration-proposal", Type: "function", Function: modeladapter.FunctionCall{Name: "propose_control_operation", Arguments: json.RawMessage(args)}}}}}}}
	store := &fakeBossStore{}
	assistant := &Assistant{agentModel: client, agentProvider: "openrouter", agentQueryReader: store, query: newQueryExecutor(store), model: "test-model", backend: config.AIBackendOpenRouter}
	response, err := assistant.Reply(t.Context(), AssistantRequest{HelpChat: true, Messages: []ChatMessage{{Role: "user", Content: "Install example@curated for Claude Code."}}})
	if err != nil {
		t.Fatal(err)
	}
	if response.ControlInvocation == nil || response.ControlInvocation.Capability != control.CapabilityIntegrationsManage {
		t.Fatalf("missing typed proposal: %#v", response)
	}
	for _, expected := range []string{"claude_code", "user scope", "example@curated", "Enter confirms; Esc cancels"} {
		if !strings.Contains(response.Content, expected) {
			t.Fatalf("confirmation omitted %q: %s", expected, response.Content)
		}
	}
	if len(client.requests) != 1 {
		t.Fatal("terminal proposal continued into another model turn")
	}
}

func TestHelpChatIntegrationReceiptIncludesRecoveryAndActivation(t *testing.T) {
	result := controlResultContent(ControlInvocationResultMsg{Status: "Saved", IntegrationResult: &integrations.Result{Activation: integrations.ActivationNotice, ChangedPaths: []string{"/example/config.toml"}, BackupPaths: []string{"/example/recovery.bak"}}})
	for _, expected := range []string{"Saved", "Reconnect", "/example/config.toml", "/example/recovery.bak"} {
		if !strings.Contains(result, expected) {
			t.Fatalf("receipt omitted %q: %s", expected, result)
		}
	}
}
