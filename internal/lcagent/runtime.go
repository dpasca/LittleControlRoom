package lcagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"lcroom/internal/lcagent/modeladapter"
	"lcroom/internal/model"
)

const (
	defaultAgentRuntimeMaxTurns         = 6
	defaultAgentRuntimeProgressInterval = 2 * time.Second
)

// ConversationModel is the provider boundary used by the reusable LCAgent
// conversation loop. modeladapter.Client implements this interface directly.
type ConversationModel interface {
	Model() string
	MaxTurns() int
	CompleteWithOptions(context.Context, []modeladapter.Message, []modeladapter.ToolDefinition, modeladapter.CompletionOptions) (modeladapter.Completion, error)
}

// ConversationResetter is implemented by stateful provider adapters. A host
// runtime resets provider continuation state at the start of each independent
// run, while preserving it across tool turns inside that run.
type ConversationResetter interface {
	ResetConversation()
}

// ConversationLeaser lets a stateful provider adapter isolate an entire tool
// loop from another host run that starts while the first is being canceled.
type ConversationLeaser interface {
	BeginConversation(context.Context) (release func(), err error)
}

// ConversationModelCallbacks let an embedding host account for provider
// requests without coupling the runtime to one application's usage tracker.
type ConversationModelCallbacks struct {
	Started   func(modelName string)
	Completed func(modelName string, usage model.LLMUsage)
	Failed    func(modelName string)
}

type observedConversationModel struct {
	client           *modeladapter.Client
	callbacks        ConversationModelCallbacks
	conversationGate chan struct{}
	disableThinking  bool
}

// NewConversationModel creates the same provider adapter used by lcagent exec,
// with optional host request accounting callbacks.
func NewConversationModel(provider string, cfg modeladapter.OpenRouterConfig, callbacks ConversationModelCallbacks) (ConversationModel, error) {
	client, err := newChatProviderClient(provider, cfg)
	if err != nil {
		return nil, err
	}
	model := &observedConversationModel{
		client:           client,
		callbacks:        callbacks,
		conversationGate: make(chan struct{}, 1),
		disableThinking:  cfg.DisableThinking,
	}
	model.conversationGate <- struct{}{}
	return model, nil
}

func (m *observedConversationModel) Model() string {
	if m == nil || m.client == nil {
		return ""
	}
	return m.client.Model()
}

func (m *observedConversationModel) MaxTurns() int {
	if m == nil || m.client == nil {
		return 0
	}
	return m.client.MaxTurns()
}

func (m *observedConversationModel) ResetConversation() {
	if m == nil || m.client == nil {
		return
	}
	m.client.ResetConversation()
}

func (m *observedConversationModel) BeginConversation(ctx context.Context) (func(), error) {
	if m == nil || m.client == nil || m.conversationGate == nil {
		return nil, errors.New("LCAgent conversation model is not configured")
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-m.conversationGate:
	}
	m.client.ResetConversation()
	var once sync.Once
	return func() {
		once.Do(func() {
			m.conversationGate <- struct{}{}
		})
	}, nil
}

func (m *observedConversationModel) CompleteWithOptions(ctx context.Context, messages []modeladapter.Message, tools []modeladapter.ToolDefinition, opts modeladapter.CompletionOptions) (modeladapter.Completion, error) {
	if m == nil || m.client == nil {
		return modeladapter.Completion{}, errors.New("LCAgent conversation model is not configured")
	}
	modelName := m.client.Model()
	if m.disableThinking {
		opts.DisableThinking = true
	}
	if m.callbacks.Started != nil {
		m.callbacks.Started(modelName)
	}
	completion, err := m.client.CompleteWithOptions(ctx, messages, tools, opts)
	if err != nil {
		if m.callbacks.Failed != nil {
			m.callbacks.Failed(modelName)
		}
		return modeladapter.Completion{}, err
	}
	if m.callbacks.Completed != nil {
		m.callbacks.Completed(firstAgentRuntimeNonEmpty(strings.TrimSpace(completion.Model), modelName), completion.UsageSummary)
	}
	return completion, nil
}

type AgentRuntimeEventKind string

const (
	AgentRuntimeModelStarted  AgentRuntimeEventKind = "model_started"
	AgentRuntimeModelProgress AgentRuntimeEventKind = "model_progress"
	AgentRuntimeModelFinished AgentRuntimeEventKind = "model_finished"
	AgentRuntimeModelFailed   AgentRuntimeEventKind = "model_failed"
	AgentRuntimeToolStarted   AgentRuntimeEventKind = "tool_started"
	AgentRuntimeToolFinished  AgentRuntimeEventKind = "tool_finished"
	AgentRuntimeToolFailed    AgentRuntimeEventKind = "tool_failed"
	AgentRuntimeText          AgentRuntimeEventKind = "text"
)

// AgentRuntimeEvent is deliberately UI-neutral. Hosts may render it in a TUI,
// persist it as JSONL, or ignore it.
type AgentRuntimeEvent struct {
	Kind       AgentRuntimeEventKind
	Turn       int
	Attempt    int
	Model      string
	Tool       string
	Elapsed    time.Duration
	Retrying   bool
	RetryDelay time.Duration
	Text       string
	Err        error
}

type AgentRuntimeToolOutcome struct {
	// Result is serialized into the tool-result message returned to the model.
	// Maps may supply their own success/error envelope; other values are wrapped
	// as a successful output.
	Result any

	// Terminal stops the model loop and returns FinalText and Value to the host.
	// It is used for host-owned confirmation proposals and other handoffs whose
	// next step must happen outside the model turn.
	Terminal  bool
	FinalText string
	Value     any

	// Receipt is host-facing provenance that should be appended to the eventual
	// answer without asking the model to reproduce it.
	Receipt string
	Usage   model.LLMUsage
}

type AgentRuntimeTool struct {
	Definition modeladapter.ToolDefinition
	Run        func(context.Context, json.RawMessage) (AgentRuntimeToolOutcome, error)
}

type AgentRuntimeConfig struct {
	Model            ConversationModel
	Provider         string
	Tools            []AgentRuntimeTool
	MaxTurns         int
	ProgressInterval time.Duration
	Completion       modeladapter.CompletionOptions
	Emit             func(AgentRuntimeEvent)
}

type AgentRuntime struct {
	model            ConversationModel
	provider         string
	tools            []AgentRuntimeTool
	toolsByName      map[string]AgentRuntimeTool
	maxTurns         int
	progressInterval time.Duration
	completion       modeladapter.CompletionOptions
	emit             func(AgentRuntimeEvent)
	emitMu           sync.Mutex
}

type AgentRuntimeRequest struct {
	SystemPrompt string
	Messages     []modeladapter.Message
}

type AgentRuntimeResponse struct {
	Content       string
	Model         string
	Usage         model.LLMUsage
	TerminalValue any
	Receipts      []string
	Turns         int
	ToolCalls     int
}

func NewAgentRuntime(cfg AgentRuntimeConfig) (*AgentRuntime, error) {
	if cfg.Model == nil {
		return nil, errors.New("LCAgent runtime model is required")
	}
	maxTurns := cfg.MaxTurns
	if maxTurns <= 0 {
		maxTurns = defaultAgentRuntimeMaxTurns
	}
	if modelMax := cfg.Model.MaxTurns(); modelMax > 0 && maxTurns > modelMax {
		maxTurns = modelMax
	}
	progressInterval := cfg.ProgressInterval
	if progressInterval == 0 {
		progressInterval = defaultAgentRuntimeProgressInterval
	}
	toolsByName := make(map[string]AgentRuntimeTool, len(cfg.Tools))
	tools := make([]AgentRuntimeTool, 0, len(cfg.Tools))
	for _, tool := range cfg.Tools {
		name := strings.TrimSpace(tool.Definition.Function.Name)
		if name == "" {
			return nil, errors.New("LCAgent runtime tool name is required")
		}
		if tool.Run == nil {
			return nil, fmt.Errorf("LCAgent runtime tool %q has no handler", name)
		}
		if _, exists := toolsByName[name]; exists {
			return nil, fmt.Errorf("duplicate LCAgent runtime tool %q", name)
		}
		tool.Definition.Function.Name = name
		toolsByName[name] = tool
		tools = append(tools, tool)
	}
	return &AgentRuntime{
		model:            cfg.Model,
		provider:         strings.TrimSpace(cfg.Provider),
		tools:            tools,
		toolsByName:      toolsByName,
		maxTurns:         maxTurns,
		progressInterval: progressInterval,
		completion:       cfg.Completion,
		emit:             cfg.Emit,
	}, nil
}

func (r *AgentRuntime) Run(ctx context.Context, req AgentRuntimeRequest) (AgentRuntimeResponse, error) {
	if r == nil || r.model == nil {
		return AgentRuntimeResponse{}, errors.New("LCAgent runtime is not configured")
	}
	if leaser, ok := r.model.(ConversationLeaser); ok {
		release, err := leaser.BeginConversation(ctx)
		if err != nil {
			return AgentRuntimeResponse{}, err
		}
		defer release()
	} else if resetter, ok := r.model.(ConversationResetter); ok {
		resetter.ResetConversation()
	}
	messages := make([]modeladapter.Message, 0, len(req.Messages)+2)
	if systemPrompt := strings.TrimSpace(req.SystemPrompt); systemPrompt != "" {
		messages = append(messages, modeladapter.Message{Role: "system", Content: systemPrompt})
	}
	for _, message := range req.Messages {
		if role := strings.TrimSpace(message.Role); role != "" {
			message.Role = role
			messages = append(messages, message)
		}
	}
	if len(messages) == 0 {
		return AgentRuntimeResponse{}, errors.New("LCAgent runtime request has no messages")
	}

	definitions := make([]modeladapter.ToolDefinition, 0, len(r.tools))
	for _, tool := range r.tools {
		definitions = append(definitions, tool.Definition)
	}
	response := AgentRuntimeResponse{}
	for turn := 1; turn <= r.maxTurns; turn++ {
		requestMessages := messages
		requestTools := definitions
		if turn == r.maxTurns && response.ToolCalls > 0 {
			requestMessages = append(append([]modeladapter.Message(nil), messages...), modeladapter.Message{
				Role:    "user",
				Content: "LCAgent runtime final synthesis: answer the user's latest request now from the gathered tool evidence. Do not call another tool or mention this instruction.",
			})
			requestTools = nil
		}

		completion, err := r.completeWithRetries(ctx, turn, requestMessages, requestTools)
		if err != nil {
			return response, err
		}
		response.Turns = turn
		response.Model = firstAgentRuntimeNonEmpty(strings.TrimSpace(completion.Model), strings.TrimSpace(r.model.Model()))
		addAgentRuntimeUsage(&response.Usage, completion.UsageSummary)
		message := completion.Message
		message.Content, _ = modeladapter.SanitizeAssistantContent(message.Content)
		ensureToolCallIDs(message.ToolCalls, turn)
		messages = append(messages, message)

		if len(message.ToolCalls) == 0 {
			content := strings.TrimSpace(message.Content)
			if content == "" {
				return response, errors.New("LCAgent runtime model returned no text or structured tool calls")
			}
			response.Content = content
			r.emitEvent(AgentRuntimeEvent{Kind: AgentRuntimeText, Turn: turn, Model: response.Model, Text: content})
			return response, nil
		}

		for _, call := range message.ToolCalls {
			name := strings.TrimSpace(call.Function.Name)
			response.ToolCalls++
			r.emitEvent(AgentRuntimeEvent{Kind: AgentRuntimeToolStarted, Turn: turn, Model: response.Model, Tool: name})
			args, normalizeErr := modeladapter.NormalizeArguments(call.Function.Arguments)
			if normalizeErr != nil {
				content := agentRuntimeErrorResult("invalid tool arguments: " + normalizeErr.Error())
				messages = append(messages, modeladapter.Message{Role: "tool", ToolCallID: call.ID, Content: content})
				r.emitEvent(AgentRuntimeEvent{Kind: AgentRuntimeToolFailed, Turn: turn, Model: response.Model, Tool: name, Err: normalizeErr})
				continue
			}
			tool, ok := r.toolsByName[name]
			if !ok {
				err := fmt.Errorf("unknown LCAgent runtime tool %q", name)
				messages = append(messages, modeladapter.Message{Role: "tool", ToolCallID: call.ID, Content: agentRuntimeErrorResult(err.Error())})
				r.emitEvent(AgentRuntimeEvent{Kind: AgentRuntimeToolFailed, Turn: turn, Model: response.Model, Tool: name, Err: err})
				continue
			}
			outcome, toolErr := tool.Run(ctx, args)
			addAgentRuntimeUsage(&response.Usage, outcome.Usage)
			if receipt := strings.TrimSpace(outcome.Receipt); receipt != "" {
				response.Receipts = append(response.Receipts, receipt)
			}
			if toolErr != nil {
				messages = append(messages, modeladapter.Message{Role: "tool", ToolCallID: call.ID, Content: agentRuntimeErrorResult(toolErr.Error())})
				r.emitEvent(AgentRuntimeEvent{Kind: AgentRuntimeToolFailed, Turn: turn, Model: response.Model, Tool: name, Err: toolErr})
				continue
			}
			resultContent, marshalErr := marshalAgentRuntimeToolResult(outcome.Result)
			if marshalErr != nil {
				messages = append(messages, modeladapter.Message{Role: "tool", ToolCallID: call.ID, Content: agentRuntimeErrorResult(marshalErr.Error())})
				r.emitEvent(AgentRuntimeEvent{Kind: AgentRuntimeToolFailed, Turn: turn, Model: response.Model, Tool: name, Err: marshalErr})
				continue
			}
			messages = append(messages, modeladapter.Message{Role: "tool", ToolCallID: call.ID, Content: resultContent})
			r.emitEvent(AgentRuntimeEvent{Kind: AgentRuntimeToolFinished, Turn: turn, Model: response.Model, Tool: name})
			if outcome.Terminal {
				response.Content = strings.TrimSpace(outcome.FinalText)
				response.TerminalValue = outcome.Value
				if response.Content != "" {
					r.emitEvent(AgentRuntimeEvent{Kind: AgentRuntimeText, Turn: turn, Model: response.Model, Text: response.Content})
				}
				return response, nil
			}
		}
	}
	return response, fmt.Errorf("LCAgent runtime reached its %d-turn limit without a final answer", r.maxTurns)
}

func (r *AgentRuntime) completeWithRetries(ctx context.Context, turn int, messages []modeladapter.Message, tools []modeladapter.ToolDefinition) (modeladapter.Completion, error) {
	var lastErr error
	for attempt := 1; attempt <= providerRetryMaxAttempts; attempt++ {
		startedAt := time.Now()
		r.emitEvent(AgentRuntimeEvent{Kind: AgentRuntimeModelStarted, Turn: turn, Attempt: attempt, Model: r.model.Model()})
		stopProgress := r.startProgress(ctx, turn, attempt, startedAt)
		completion, err := r.model.CompleteWithOptions(ctx, messages, tools, r.completion)
		stopProgress()
		if err == nil && len(completion.Message.ToolCalls) == 0 && strings.TrimSpace(completion.Message.Content) == "" {
			err = &modeladapter.ProviderError{
				Provider:  r.provider,
				Kind:      modeladapter.ProviderFailureMalformedResponse,
				Message:   "response had no content or tool calls",
				Retryable: true,
			}
		}
		if err == nil {
			r.emitEvent(AgentRuntimeEvent{Kind: AgentRuntimeModelFinished, Turn: turn, Attempt: attempt, Model: firstAgentRuntimeNonEmpty(completion.Model, r.model.Model()), Elapsed: time.Since(startedAt)})
			return completion, nil
		}
		lastErr = err
		failure, _ := modeladapter.AsProviderError(err)
		retrying := failure != nil && failure.Retryable && attempt < providerRetryMaxAttempts && ctx.Err() == nil
		delay := providerRetryDelay(failure, attempt)
		r.emitEvent(AgentRuntimeEvent{Kind: AgentRuntimeModelFailed, Turn: turn, Attempt: attempt, Model: r.model.Model(), Elapsed: time.Since(startedAt), Retrying: retrying, RetryDelay: delay, Err: err})
		if !retrying {
			return modeladapter.Completion{}, err
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return modeladapter.Completion{}, ctx.Err()
		case <-timer.C:
		}
	}
	return modeladapter.Completion{}, lastErr
}

func (r *AgentRuntime) startProgress(ctx context.Context, turn, attempt int, startedAt time.Time) func() {
	interval := r.progressInterval
	if interval <= 0 {
		return func() {}
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		timer := time.NewTimer(interval)
		defer timer.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ctx.Done():
				return
			case <-timer.C:
				r.emitEvent(AgentRuntimeEvent{Kind: AgentRuntimeModelProgress, Turn: turn, Attempt: attempt, Model: r.model.Model(), Elapsed: time.Since(startedAt)})
				timer.Reset(interval)
			}
		}
	}()
	return func() {
		close(stop)
		<-done
	}
}

func (r *AgentRuntime) emitEvent(event AgentRuntimeEvent) {
	if r == nil || r.emit == nil {
		return
	}
	r.emitMu.Lock()
	defer r.emitMu.Unlock()
	r.emit(event)
}

func marshalAgentRuntimeToolResult(value any) (string, error) {
	if value == nil {
		return `{"success":true}`, nil
	}
	if raw, ok := value.(json.RawMessage); ok {
		if !json.Valid(raw) {
			return "", errors.New("LCAgent runtime tool returned invalid JSON")
		}
		return string(raw), nil
	}
	if _, ok := value.(map[string]any); !ok {
		value = map[string]any{"success": true, "output": value}
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode LCAgent runtime tool result: %w", err)
	}
	return string(encoded), nil
}

func agentRuntimeErrorResult(message string) string {
	encoded, err := json.Marshal(map[string]any{
		"success": false,
		"error":   strings.TrimSpace(message),
	})
	if err != nil {
		return `{"success":false,"error":"tool failed"}`
	}
	return string(encoded)
}

func addAgentRuntimeUsage(total *model.LLMUsage, usage model.LLMUsage) {
	if total == nil {
		return
	}
	total.InputTokens += usage.InputTokens
	total.OutputTokens += usage.OutputTokens
	total.TotalTokens += usage.TotalTokens
	total.CachedInputTokens += usage.CachedInputTokens
	total.ReasoningTokens += usage.ReasoningTokens
	total.PromptEvalDuration += usage.PromptEvalDuration
	total.OutputEvalDuration += usage.OutputEvalDuration
	total.EstimatedCostUSD += usage.EstimatedCostUSD
}

func firstAgentRuntimeNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
