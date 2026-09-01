package boss

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"lcroom/internal/config"
	"lcroom/internal/control"
	"lcroom/internal/lcagent/modeladapter"
	"lcroom/internal/model"
)

type scriptedHelpChatModel struct {
	mu          sync.Mutex
	model       string
	completions []modeladapter.Completion
	requests    [][]modeladapter.Message
	tools       [][]modeladapter.ToolDefinition
	options     []modeladapter.CompletionOptions
}

func (m *scriptedHelpChatModel) Model() string {
	return m.model
}

func (m *scriptedHelpChatModel) MaxTurns() int {
	return 12
}

func (m *scriptedHelpChatModel) CompleteWithOptions(_ context.Context, messages []modeladapter.Message, tools []modeladapter.ToolDefinition, opts modeladapter.CompletionOptions) (modeladapter.Completion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.requests = append(m.requests, append([]modeladapter.Message(nil), messages...))
	m.tools = append(m.tools, append([]modeladapter.ToolDefinition(nil), tools...))
	m.options = append(m.options, opts)
	if len(m.completions) == 0 {
		return modeladapter.Completion{}, context.Canceled
	}
	completion := m.completions[0]
	m.completions = m.completions[1:]
	return completion, nil
}

func TestHelpChatLCAgentUsesGeneratedHelpForRecording(t *testing.T) {
	modelClient := &scriptedHelpChatModel{
		model: "test-model",
		completions: []modeladapter.Completion{{
			Model: "test-model",
			Message: modeladapter.Message{Role: "assistant", ToolCalls: []modeladapter.ToolCall{{
				ID:   "help-call",
				Type: "function",
				Function: modeladapter.FunctionCall{
					Name:      "lookup_lcr_help",
					Arguments: json.RawMessage(`{"query":"recording","limit":5}`),
				},
			}}},
			UsageSummary: model.LLMUsage{InputTokens: 10, OutputTokens: 2, TotalTokens: 12},
		}},
	}
	store := &fakeBossStore{}
	assistant := &Assistant{
		agentModel:       modelClient,
		agentProvider:    "openrouter",
		agentQueryReader: store,
		query:            newQueryExecutor(store),
		model:            "test-model",
		reasoningEffort:  "xhigh",
		reasoningSet:     true,
		backend:          config.AIBackendOpenRouter,
	}
	var events []AssistantStreamEvent
	response, err := assistant.ReplyStream(context.Background(), AssistantRequest{
		HelpChat: true,
		Messages: []ChatMessage{{Role: "user", Content: "How do I launch LCR with recording again?"}},
	}, func(event AssistantStreamEvent) {
		events = append(events, event)
	})
	if err != nil {
		t.Fatalf("ReplyStream() error = %v", err)
	}
	for _, want := range []string{"/record", "lcroom tui --demo-record", "make tui-record"} {
		if !strings.Contains(response.Content, want) {
			t.Fatalf("response missing %q:\n%s", want, response.Content)
		}
	}
	if len(modelClient.requests) != 1 {
		t.Fatalf("model requests = %d, want one model turn plus local help lookup", len(modelClient.requests))
	}
	if len(modelClient.options) != 1 || modelClient.options[0].ReasoningEffort != "xhigh" {
		t.Fatalf("completion options = %#v, want xhigh reasoning", modelClient.options)
	}
	toolNames := helpChatToolNames(modelClient.tools[0])
	for _, want := range []string{"lookup_lcr_help", "list_lcr_queries", "run_lcr_query", "list_control_capabilities", "propose_control_operation", "scout_repository"} {
		if !toolNames[want] {
			t.Errorf("Help Chat tool profile missing %q: %#v", want, toolNames)
		}
	}
	for _, forbidden := range []string{"run_command", "apply_patch", "read_file", "web_search", "load_skill"} {
		if toolNames[forbidden] {
			t.Errorf("lean Help Chat unexpectedly exposes %q", forbidden)
		}
	}
	var sawThinking, sawHelp, sawText bool
	for _, event := range events {
		switch event.Kind {
		case AssistantStreamProgress:
			if strings.Contains(event.Progress, "LCAgent thinking") && event.ProgressState == "running" {
				sawThinking = true
			}
		case AssistantStreamToolCall:
			if strings.Contains(event.ToolCall, "checking LCR help") {
				sawHelp = true
			}
		case AssistantStreamTextDelta:
			sawText = true
		}
	}
	if !sawThinking || !sawHelp || !sawText {
		t.Fatalf("stream events missing visible progress/help/text: %#v", events)
	}
}

func TestHelpChatLCAgentReturnsTypedControlProposalToHost(t *testing.T) {
	modelClient := &scriptedHelpChatModel{
		model: "test-model",
		completions: []modeladapter.Completion{{
			Model: "test-model",
			Message: modeladapter.Message{Role: "assistant", ToolCalls: []modeladapter.ToolCall{{
				ID:   "proposal-call",
				Type: "function",
				Function: modeladapter.FunctionCall{
					Name: "propose_control_operation",
					Arguments: json.RawMessage(`{
						"capability":"todo.add",
						"arguments":{"project_path":"/tmp/alpha","project_name":"Alpha","text":"Document the launch recording workflow"},
						"scope_note":"Add this to Alpha's backlog."
					}`),
				},
			}}},
		}},
	}
	store := &fakeBossStore{}
	assistant := &Assistant{
		agentModel:       modelClient,
		agentProvider:    "openrouter",
		agentQueryReader: store,
		query:            newQueryExecutor(store),
		model:            "test-model",
		backend:          config.AIBackendOpenRouter,
	}
	response, err := assistant.Reply(context.Background(), AssistantRequest{
		HelpChat: true,
		Messages: []ChatMessage{{Role: "user", Content: "Add documenting recording to Alpha's backlog."}},
	})
	if err != nil {
		t.Fatalf("Reply() error = %v", err)
	}
	if response.ControlInvocation == nil {
		t.Fatal("ControlInvocation = nil, want host-owned confirmation proposal")
	}
	if response.ControlInvocation.Capability != control.CapabilityTodoAdd {
		t.Fatalf("capability = %q", response.ControlInvocation.Capability)
	}
	var input control.TodoAddInput
	if err := json.Unmarshal(response.ControlInvocation.Args, &input); err != nil {
		t.Fatalf("decode proposal: %v", err)
	}
	if input.ProjectPath != "/tmp/alpha" || input.Text != "Document the launch recording workflow" {
		t.Fatalf("proposal input = %#v", input)
	}
	if !strings.Contains(response.Content, "Enter confirms; Esc cancels") {
		t.Fatalf("proposal did not use existing host confirmation preview:\n%s", response.Content)
	}
	if len(modelClient.requests) != 1 {
		t.Fatalf("model requests = %d, terminal proposal must not take a synthesis turn", len(modelClient.requests))
	}
}

func TestHelpChatLCAgentSharedQueryHonorsHostPrivacyMode(t *testing.T) {
	modelClient := &scriptedHelpChatModel{
		model: "test-model",
		completions: []modeladapter.Completion{
			{
				Model: "test-model",
				Message: modeladapter.Message{Role: "assistant", ToolCalls: []modeladapter.ToolCall{{
					ID:   "query-call",
					Type: "function",
					Function: modeladapter.FunctionCall{
						Name:      "run_lcr_query",
						Arguments: json.RawMessage(`{"query":"project.list","arguments":{"include_historical":false,"limit":20,"cursor":""}}`),
					},
				}}},
			},
			{Model: "test-model", Message: modeladapter.Message{Role: "assistant", Content: "Only Public Alpha is visible."}},
		},
	}
	store := &fakeBossStore{projects: []model.ProjectSummary{
		{Path: "/tmp/public", Name: "Public Alpha", InScope: true},
		{Path: "/tmp/private", Name: "Secret Alpha", InScope: true, CategoryPrivate: true},
	}}
	assistant := &Assistant{
		agentModel:       modelClient,
		agentProvider:    "openrouter",
		agentQueryReader: store,
		query:            newQueryExecutor(store),
		model:            "test-model",
		backend:          config.AIBackendOpenRouter,
	}
	response, err := assistant.Reply(context.Background(), AssistantRequest{
		HelpChat: true,
		View:     ViewContext{PrivacyMode: true},
		Messages: []ChatMessage{{Role: "user", Content: "List the visible projects."}},
	})
	if err != nil {
		t.Fatalf("Reply() error = %v", err)
	}
	if response.Content != "Only Public Alpha is visible." || len(modelClient.requests) != 2 {
		t.Fatalf("response = %#v; requests = %d", response, len(modelClient.requests))
	}
	last := modelClient.requests[1][len(modelClient.requests[1])-1]
	if last.Role != "tool" || !strings.Contains(last.Content, "Public Alpha") || strings.Contains(last.Content, "Secret Alpha") {
		t.Fatalf("privacy-filtered tool result = %s", last.Content)
	}
	if !strings.Contains(last.Content, "private_categories_hidden") {
		t.Fatalf("tool result omitted privacy contract: %s", last.Content)
	}
}

func TestHelpChatLCAgentPreflightSkipsLegacyRouter(t *testing.T) {
	router := &fakeJSONSchemaRunner{err: context.Canceled}
	assistant := &Assistant{
		agentModel:  &scriptedHelpChatModel{model: "test-model"},
		queryRouter: router,
	}
	_, handled, _, err := assistant.preflightHelpChat(context.Background(), AssistantRequest{HelpChat: true}, nil)
	if err != nil || handled {
		t.Fatalf("preflight = handled %t, err %v", handled, err)
	}
	if len(router.reqs) != 0 {
		t.Fatalf("legacy router requests = %d, want none", len(router.reqs))
	}
}

func helpChatToolNames(definitions []modeladapter.ToolDefinition) map[string]bool {
	names := make(map[string]bool, len(definitions))
	for _, definition := range definitions {
		names[definition.Function.Name] = true
	}
	return names
}
