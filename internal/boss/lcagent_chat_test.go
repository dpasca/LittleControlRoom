package boss

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"lcroom/internal/config"
	"lcroom/internal/control"
	"lcroom/internal/lcagent/modeladapter"
	"lcroom/internal/model"
	"lcroom/internal/store"
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
		completions: []modeladapter.Completion{
			{
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
			},
			{
				Model: "test-model",
				Message: modeladapter.Message{
					Role:    "assistant",
					Content: "Use `lcroom tui --demo-record` to launch with recording enabled. Once inside, `/record` controls capture; `make tui-record` is the development shortcut.",
				},
			},
		},
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
	if len(modelClient.requests) != 2 {
		t.Fatalf("model requests = %d, want lookup turn plus a synthesis turn", len(modelClient.requests))
	}
	if len(modelClient.options) != 2 || modelClient.options[0].ReasoningEffort != "xhigh" || modelClient.options[1].ReasoningEffort != "xhigh" {
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

func TestHelpChatLCAgentCanRecoverFromIrrelevantHelpLookup(t *testing.T) {
	modelClient := &scriptedHelpChatModel{
		model: "test-model",
		completions: []modeladapter.Completion{
			{
				Model: "test-model",
				Message: modeladapter.Message{Role: "assistant", ToolCalls: []modeladapter.ToolCall{{
					ID:   "mistaken-help-call",
					Type: "function",
					Function: modeladapter.FunctionCall{
						Name:      "lookup_lcr_help",
						Arguments: json.RawMessage(`{"query":"what's up with the orphaned f14 teture wroktree","limit":5}`),
					},
				}}},
			},
			{
				Model: "test-model",
				Message: modeladapter.Message{Role: "assistant", ToolCalls: []modeladapter.ToolCall{{
					ID:   "current-state-call",
					Type: "function",
					Function: modeladapter.FunctionCall{
						Name:      "inspect_current_tui",
						Arguments: json.RawMessage(`{}`),
					},
				}}},
			},
			{
				Model: "test-model",
				Message: modeladapter.Message{
					Role:    "assistant",
					Content: "The F-14 texture worktree is clean and ready to merge; its checkout remains only because the task record no longer owns it.",
				},
			},
		},
	}
	assistant := &Assistant{
		agentModel:    modelClient,
		agentProvider: "openrouter",
		query:         newQueryExecutor(&fakeBossStore{}),
		model:         "test-model",
		backend:       config.AIBackendOpenRouter,
	}
	response, err := assistant.Reply(context.Background(), AssistantRequest{
		HelpChat: true,
		Messages: []ChatMessage{{Role: "user", Content: "what's up with the orphaned f14 teture wroktree ?"}},
		Snapshot: StateSnapshot{HotProjects: []ProjectBrief{{
			Name:          "F-14 texture worktree",
			RepoBranch:    "asset/f14-textured-packed-pbr",
			LatestSummary: "Orphaned worktree is clean and ready to merge.",
		}}},
	})
	if err != nil {
		t.Fatalf("Reply() error = %v", err)
	}
	if len(modelClient.requests) != 3 {
		t.Fatalf("model requests = %d, want state inspection and synthesis after the help lookup", len(modelClient.requests))
	}
	if strings.Contains(response.Content, "Show current context use") || !strings.Contains(response.Content, "ready to merge") {
		t.Fatalf("response should recover instead of returning the top help hit: %q", response.Content)
	}
	helpResult := modelClient.requests[1][len(modelClient.requests[1])-1]
	if helpResult.Role != "tool" || !strings.Contains(helpResult.Content, "help reference") {
		t.Fatalf("recovery turn did not receive the help lookup evidence: %#v", helpResult)
	}
	stateResult := modelClient.requests[2][len(modelClient.requests[2])-1]
	if stateResult.Role != "tool" || !strings.Contains(stateResult.Content, "Orphaned worktree is clean and ready to merge") {
		t.Fatalf("synthesis turn did not receive current state evidence: %#v", stateResult)
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

func TestHelpChatResolvesCompletedTaskBeforeContinuingWithoutSelection(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "chat.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	task, err := st.CreateAgentTask(t.Context(), model.CreateAgentTaskInput{
		ID: "agt_gamepix", Title: "Review GamePix Side Letter for Fractal Strike",
		Status: model.AgentTaskStatusCompleted, Provider: model.SessionSourceCodex,
		SessionID: "gamepix-thread", WorkspacePath: "/tasks/gamepix",
	})
	if err != nil {
		t.Fatal(err)
	}
	steps := []struct{ name, args string }{
		{"list_lcr_queries", `{}`},
		{"list_lcr_queries", `{"domain":"work"}`},
		{"describe_lcr_query", `{"name":"work.agent_task_list"}`},
		{"run_lcr_query", `{"query":"work.agent_task_list","arguments":{"query":"GamePix","include_historical":true}}`},
		{"describe_lcr_query", `{"name":"work.agent_task_get"}`},
		{"run_lcr_query", `{"query":"work.agent_task_get","arguments":{"task_id":"agt_gamepix"}}`},
		{"list_control_capabilities", `{}`},
		{"list_control_capabilities", `{"domain":"agent_task"}`},
		{"describe_control_capability", `{"name":"agent_task.continue"}`},
		{"propose_control_operation", `{"capability":"agent_task.continue","arguments":{"task_id":"agt_gamepix","prompt":"Check LinkedIn for the revised GamePix side letter and compare it with the prior agreement.","provider":"codex","session_mode":"resume_or_new","reveal":false}}`},
	}
	client := &scriptedHelpChatModel{model: "test-model"}
	for _, step := range steps {
		client.completions = append(client.completions, modeladapter.Completion{
			Model: "test-model",
			Message: modeladapter.Message{Role: "assistant", ToolCalls: []modeladapter.ToolCall{{
				ID: step.name, Type: "function",
				Function: modeladapter.FunctionCall{Name: step.name, Arguments: json.RawMessage(step.args)},
			}}},
		})
	}
	assistant := &Assistant{
		agentModel: client, agentProvider: "openrouter", agentQueryReader: st,
		query: newQueryExecutor(st), model: "test-model", backend: config.AIBackendOpenRouter,
	}
	response, err := assistant.Reply(t.Context(), AssistantRequest{
		HelpChat: true,
		Messages: []ChatMessage{
			{Role: "user", Content: "Please tell the engineer on the previous GamePix task to check LinkedIn for the fixed side letter."},
			{Role: "assistant", Content: "I can continue Review GamePix Side Letter for Fractal Strike with that request."},
			{Role: "user", Content: "let's do that"},
		},
		Snapshot: StateSnapshot{OpenAgentTasks: []AgentTaskBrief{{ID: "agt_unrelated", Title: "Download videos"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.ControlInvocation == nil || response.ControlInvocation.Capability != control.CapabilityAgentTaskContinue {
		t.Fatalf("expected continuation confirmation, got %#v", response)
	}
	var input control.AgentTaskContinueInput
	if err := json.Unmarshal(response.ControlInvocation.Args, &input); err != nil {
		t.Fatal(err)
	}
	if input.TaskID != task.ID || !strings.Contains(input.Prompt, "LinkedIn") {
		t.Fatalf("handoff lost target or request: %#v", input)
	}
	if len(client.requests) != len(steps) {
		t.Fatalf("model rounds = %d, want %d", len(client.requests), len(steps))
	}
	for i, definitions := range client.tools {
		if !helpChatToolNames(definitions)[steps[i].name] {
			t.Fatalf("round %d lost tool access before handoff", i+1)
		}
	}
	for _, index := range []int{4, 6} {
		messages := client.requests[index]
		evidence := messages[len(messages)-1]
		if evidence.Role != "tool" || !strings.Contains(evidence.Content, task.ID) || !strings.Contains(evidence.Content, "gamepix-thread") {
			t.Fatalf("lookup did not return actionable identity: %#v", evidence)
		}
	}
	persisted, err := st.GetAgentTask(t.Context(), task.ID)
	if err != nil || persisted.Status != model.AgentTaskStatusCompleted {
		t.Fatalf("proposal must await confirmation without mutating task: %#v, %v", persisted, err)
	}
}

func helpChatToolNames(definitions []modeladapter.ToolDefinition) map[string]bool {
	names := make(map[string]bool, len(definitions))
	for _, definition := range definitions {
		names[definition.Function.Name] = true
	}
	return names
}
