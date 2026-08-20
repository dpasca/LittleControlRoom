package lcagent

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"lcroom/internal/lcagent/modeladapter"
	"lcroom/internal/model"
)

type scriptedConversationModel struct {
	mu          sync.Mutex
	model       string
	completions []modeladapter.Completion
	requests    [][]modeladapter.Message
	tools       [][]modeladapter.ToolDefinition
	delay       time.Duration
	resetCount  int
}

func (m *scriptedConversationModel) Model() string {
	return m.model
}

func (m *scriptedConversationModel) MaxTurns() int {
	return 20
}

func (m *scriptedConversationModel) ResetConversation() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.resetCount++
}

func (m *scriptedConversationModel) CompleteWithOptions(ctx context.Context, messages []modeladapter.Message, tools []modeladapter.ToolDefinition, _ modeladapter.CompletionOptions) (modeladapter.Completion, error) {
	if m.delay > 0 {
		timer := time.NewTimer(m.delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return modeladapter.Completion{}, ctx.Err()
		case <-timer.C:
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.requests = append(m.requests, append([]modeladapter.Message(nil), messages...))
	m.tools = append(m.tools, append([]modeladapter.ToolDefinition(nil), tools...))
	if len(m.completions) == 0 {
		return modeladapter.Completion{}, context.Canceled
	}
	completion := m.completions[0]
	m.completions = m.completions[1:]
	return completion, nil
}

func TestAgentRuntimeRunsExactRegisteredToolThenSynthesizes(t *testing.T) {
	modelClient := &scriptedConversationModel{
		model: "test-model",
		completions: []modeladapter.Completion{
			{
				Model: "test-model",
				Message: modeladapter.Message{Role: "assistant", ToolCalls: []modeladapter.ToolCall{{
					ID:   "call_state",
					Type: "function",
					Function: modeladapter.FunctionCall{
						Name:      "read_state",
						Arguments: json.RawMessage(`{"project":"alpha"}`),
					},
				}}},
				UsageSummary: model.LLMUsage{InputTokens: 10, OutputTokens: 2, TotalTokens: 12},
			},
			{
				Model:        "test-model",
				Message:      modeladapter.Message{Role: "assistant", Content: "Alpha is ready."},
				UsageSummary: model.LLMUsage{InputTokens: 15, OutputTokens: 4, TotalTokens: 19},
			},
		},
	}
	var gotArgs string
	var events []AgentRuntimeEventKind
	runtime, err := NewAgentRuntime(AgentRuntimeConfig{
		Model:            modelClient,
		MaxTurns:         4,
		ProgressInterval: -1,
		Tools: []AgentRuntimeTool{{
			Definition: testAgentRuntimeToolDefinition("read_state"),
			Run: func(_ context.Context, args json.RawMessage) (AgentRuntimeToolOutcome, error) {
				gotArgs = string(args)
				return AgentRuntimeToolOutcome{Result: map[string]any{"success": true, "status": "ready"}}, nil
			},
		}},
		Emit: func(event AgentRuntimeEvent) {
			events = append(events, event.Kind)
		},
	})
	if err != nil {
		t.Fatalf("NewAgentRuntime() error = %v", err)
	}
	response, err := runtime.Run(context.Background(), AgentRuntimeRequest{
		SystemPrompt: "You are concise.",
		Messages:     []modeladapter.Message{{Role: "user", Content: "Status?"}},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if response.Content != "Alpha is ready." || response.ToolCalls != 1 || response.Turns != 2 {
		t.Fatalf("response = %#v", response)
	}
	if modelClient.resetCount != 1 {
		t.Fatalf("conversation resets = %d, want one at run boundary", modelClient.resetCount)
	}
	if response.Usage.InputTokens != 25 || response.Usage.OutputTokens != 6 || response.Usage.TotalTokens != 31 {
		t.Fatalf("usage = %#v", response.Usage)
	}
	if gotArgs != `{"project":"alpha"}` {
		t.Fatalf("tool args = %s", gotArgs)
	}
	if len(modelClient.requests) != 2 || len(modelClient.requests[1]) != 4 {
		t.Fatalf("request messages = %#v", modelClient.requests)
	}
	last := modelClient.requests[1][3]
	if last.Role != "tool" || last.ToolCallID != "call_state" || !strings.Contains(last.Content, `"status":"ready"`) {
		t.Fatalf("tool result message = %#v", last)
	}
	wantEvents := []AgentRuntimeEventKind{
		AgentRuntimeModelStarted,
		AgentRuntimeModelFinished,
		AgentRuntimeToolStarted,
		AgentRuntimeToolFinished,
		AgentRuntimeModelStarted,
		AgentRuntimeModelFinished,
		AgentRuntimeText,
	}
	if len(events) != len(wantEvents) {
		t.Fatalf("events = %#v, want %#v", events, wantEvents)
	}
	for i := range wantEvents {
		if events[i] != wantEvents[i] {
			t.Fatalf("events = %#v, want %#v", events, wantEvents)
		}
	}
}

func TestAgentRuntimeReturnsTerminalHostOutcomeWithoutAnotherModelTurn(t *testing.T) {
	modelClient := &scriptedConversationModel{
		model: "test-model",
		completions: []modeladapter.Completion{{
			Model: "test-model",
			Message: modeladapter.Message{Role: "assistant", ToolCalls: []modeladapter.ToolCall{{
				ID:   "call_propose",
				Type: "function",
				Function: modeladapter.FunctionCall{
					Name:      "propose",
					Arguments: json.RawMessage(`{"target":"alpha"}`),
				},
			}}},
		}},
	}
	token := struct{ ID string }{ID: "proposal-1"}
	runtime, err := NewAgentRuntime(AgentRuntimeConfig{
		Model:            modelClient,
		ProgressInterval: -1,
		Tools: []AgentRuntimeTool{{
			Definition: testAgentRuntimeToolDefinition("propose"),
			Run: func(context.Context, json.RawMessage) (AgentRuntimeToolOutcome, error) {
				return AgentRuntimeToolOutcome{
					Result:    map[string]any{"success": true, "waiting_for_confirmation": true},
					Terminal:  true,
					FinalText: "Confirm this proposal.",
					Value:     token,
					Receipt:   "proposal receipt",
				}, nil
			},
		}},
	})
	if err != nil {
		t.Fatalf("NewAgentRuntime() error = %v", err)
	}
	response, err := runtime.Run(context.Background(), AgentRuntimeRequest{Messages: []modeladapter.Message{{Role: "user", Content: "Do it"}}})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if response.Content != "Confirm this proposal." || response.TerminalValue != token {
		t.Fatalf("response = %#v", response)
	}
	if len(response.Receipts) != 1 || response.Receipts[0] != "proposal receipt" {
		t.Fatalf("receipts = %#v", response.Receipts)
	}
	if len(modelClient.requests) != 1 {
		t.Fatalf("model requests = %d, want 1", len(modelClient.requests))
	}
}

func TestAgentRuntimeEmitsProgressDuringSlowModelRequest(t *testing.T) {
	modelClient := &scriptedConversationModel{
		model: "test-model",
		delay: 25 * time.Millisecond,
		completions: []modeladapter.Completion{{
			Model:   "test-model",
			Message: modeladapter.Message{Role: "assistant", Content: "done"},
		}},
	}
	var events []AgentRuntimeEvent
	runtime, err := NewAgentRuntime(AgentRuntimeConfig{
		Model:            modelClient,
		ProgressInterval: 5 * time.Millisecond,
		Emit: func(event AgentRuntimeEvent) {
			events = append(events, event)
		},
	})
	if err != nil {
		t.Fatalf("NewAgentRuntime() error = %v", err)
	}
	if _, err := runtime.Run(context.Background(), AgentRuntimeRequest{Messages: []modeladapter.Message{{Role: "user", Content: "wait"}}}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	progress := 0
	for _, event := range events {
		if event.Kind == AgentRuntimeModelProgress && event.Elapsed > 0 {
			progress++
		}
	}
	if progress == 0 {
		t.Fatalf("events = %#v, want model progress", events)
	}
}

func TestNewAgentRuntimeRejectsDuplicateTools(t *testing.T) {
	modelClient := &scriptedConversationModel{model: "test-model"}
	tool := AgentRuntimeTool{
		Definition: testAgentRuntimeToolDefinition("same"),
		Run: func(context.Context, json.RawMessage) (AgentRuntimeToolOutcome, error) {
			return AgentRuntimeToolOutcome{}, nil
		},
	}
	if _, err := NewAgentRuntime(AgentRuntimeConfig{Model: modelClient, Tools: []AgentRuntimeTool{tool, tool}}); err == nil {
		t.Fatal("NewAgentRuntime() error = nil, want duplicate rejection")
	}
}

func testAgentRuntimeToolDefinition(name string) modeladapter.ToolDefinition {
	return modeladapter.ToolDefinition{
		Type: "function",
		Function: modeladapter.FunctionSpec{
			Name:        name,
			Description: "test tool",
			Parameters: map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties":           map[string]any{},
			},
		},
	}
}
