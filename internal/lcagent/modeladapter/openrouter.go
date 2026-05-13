package modeladapter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"lcroom/internal/model"
)

const (
	DefaultOpenRouterModel    = "deepseek/deepseek-v4-pro"
	DefaultOpenAIModel        = "gpt-5.5"
	DefaultDeepSeekModel      = "deepseek-v4-pro"
	DefaultMoonshotModel      = "kimi-k2.6"
	DefaultOpenRouterMaxTurns = 48
	DefaultChatTemperature    = 0.2
)

type OpenRouterConfig struct {
	APIKey          string
	BaseURL         string
	Model           string
	FinalModel      string
	EnvFile         string
	MaxTurns        int
	RequestTimeout  time.Duration
	ReasoningEffort string
	Temperature     *float64
	OmitTemperature bool
	ProviderOnly    []string
	HTTPClient      *http.Client
}

type Client struct {
	apiKey             string
	baseURL            string
	model              string
	defaultModel       string
	maxTurns           int
	httpClient         *http.Client
	providerName       string
	extraHeaders       map[string]string
	maxTokensField     string
	reasoningStyle     string
	omitTemperature    bool
	temperature        *float64
	providerOnly       []string
	previousResponseID string
}

type ToolDefinition struct {
	Type     string       `json:"type"`
	Function FunctionSpec `json:"function"`
}

type FunctionSpec struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type Message struct {
	Role             string        `json:"role"`
	Content          string        `json:"content,omitempty"`
	ReasoningContent string        `json:"reasoning_content,omitempty"`
	ToolCalls        []ToolCall    `json:"tool_calls,omitempty"`
	ToolCallID       string        `json:"tool_call_id,omitempty"`
	CacheControl     *CacheControl `json:"-"`
}

type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

type FunctionCall struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type CacheControl struct {
	Type string `json:"type"`
	TTL  string `json:"ttl,omitempty"`
}

type messageContentBlock struct {
	Type         string        `json:"type"`
	Text         string        `json:"text"`
	CacheControl *CacheControl `json:"cache_control,omitempty"`
}

func (m Message) MarshalJSON() ([]byte, error) {
	type wireMessage struct {
		Role             string     `json:"role"`
		Content          any        `json:"content,omitempty"`
		ReasoningContent string     `json:"reasoning_content,omitempty"`
		ToolCalls        []ToolCall `json:"tool_calls,omitempty"`
		ToolCallID       string     `json:"tool_call_id,omitempty"`
	}
	var content any
	if m.Content != "" {
		if m.CacheControl != nil {
			content = []messageContentBlock{{
				Type:         "text",
				Text:         m.Content,
				CacheControl: m.CacheControl,
			}}
		} else {
			content = m.Content
		}
	}
	return json.Marshal(wireMessage{
		Role:             m.Role,
		Content:          content,
		ReasoningContent: m.ReasoningContent,
		ToolCalls:        m.ToolCalls,
		ToolCallID:       m.ToolCallID,
	})
}

type ChatResponse struct {
	ID      string `json:"id,omitempty"`
	Model   string `json:"model,omitempty"`
	Choices []struct {
		Message      Message `json:"message"`
		FinishReason string  `json:"finish_reason,omitempty"`
	} `json:"choices"`
	Usage json.RawMessage `json:"usage,omitempty"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    any    `json:"code"`
	} `json:"error,omitempty"`
}

type Completion struct {
	Message      Message
	ID           string
	Model        string
	FinishReason string
	Usage        json.RawMessage
	UsageSummary model.LLMUsage
}

type CompletionOptions struct {
	MaxCompletionTokens int
	ReasoningMaxTokens  int
	ReasoningEffort     string
	DisableThinking     bool
}

func NewOpenRouterClient(cfg OpenRouterConfig) (*Client, error) {
	return newChatCompletionsClient(cfg, chatProviderProfile{
		Name:           "openrouter",
		APIKeyEnv:      "OPENROUTER_API_KEY",
		BaseURLEnv:     "OPENROUTER_BASE_URL",
		DefaultBaseURL: "https://openrouter.ai/api/v1",
		DefaultModel:   DefaultOpenRouterModel,
		MaxTokensField: "max_completion_tokens",
		ReasoningStyle: "openrouter",
		ExtraHeaders: map[string]string{
			"HTTP-Referer": "https://little-control-room.local",
			"X-Title":      "Little Control Room lcagent",
		},
	})
}

func NewOpenAIClient(cfg OpenRouterConfig) (*Client, error) {
	return newChatCompletionsClient(cfg, chatProviderProfile{
		Name:            "openai",
		APIKeyEnv:       "OPENAI_API_KEY",
		BaseURLEnv:      "OPENAI_BASE_URL",
		DefaultBaseURL:  "https://api.openai.com/v1",
		DefaultModel:    DefaultOpenAIModel,
		MaxTokensField:  "max_completion_tokens",
		ReasoningStyle:  "openai",
		OmitTemperature: true,
		ExtraHeaders:    map[string]string{},
	})
}

func NewDeepSeekClient(cfg OpenRouterConfig) (*Client, error) {
	return newChatCompletionsClient(cfg, chatProviderProfile{
		Name:           "deepseek",
		APIKeyEnv:      "DEEPSEEK_API_KEY",
		BaseURLEnv:     "DEEPSEEK_BASE_URL",
		DefaultBaseURL: "https://api.deepseek.com",
		DefaultModel:   DefaultDeepSeekModel,
		MaxTokensField: "max_tokens",
		ReasoningStyle: "deepseek",
		ExtraHeaders:   map[string]string{},
	})
}

func NewMoonshotClient(cfg OpenRouterConfig) (*Client, error) {
	return newChatCompletionsClient(cfg, chatProviderProfile{
		Name:            "moonshot",
		APIKeyEnv:       "MOONSHOT_API_KEY",
		BaseURLEnv:      "MOONSHOT_BASE_URL",
		DefaultBaseURL:  "https://api.moonshot.ai/v1",
		DefaultModel:    DefaultMoonshotModel,
		MaxTokensField:  "max_completion_tokens",
		ReasoningStyle:  "moonshot",
		OmitTemperature: true,
		ExtraHeaders:    map[string]string{},
	})
}

type chatProviderProfile struct {
	Name            string
	APIKeyEnv       string
	BaseURLEnv      string
	DefaultBaseURL  string
	DefaultModel    string
	MaxTokensField  string
	ReasoningStyle  string
	ExtraHeaders    map[string]string
	OmitTemperature bool
}

func newChatCompletionsClient(cfg OpenRouterConfig, profile chatProviderProfile) (*Client, error) {
	if cfg.EnvFile != "" {
		if err := LoadEnvFile(cfg.EnvFile); err != nil {
			return nil, err
		}
	}
	apiKey := strings.TrimSpace(cfg.APIKey)
	if apiKey == "" {
		apiKey = strings.TrimSpace(os.Getenv(profile.APIKeyEnv))
	}
	if apiKey == "" {
		return nil, fmt.Errorf("%s is required for provider=%s", profile.APIKeyEnv, profile.Name)
	}
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if baseURL == "" {
		baseURL = strings.TrimRight(strings.TrimSpace(os.Getenv(profile.BaseURLEnv)), "/")
	}
	if baseURL == "" {
		baseURL = profile.DefaultBaseURL
	}
	model := strings.TrimSpace(cfg.Model)
	if model == "" {
		model = profile.DefaultModel
	}
	maxTurns := cfg.MaxTurns
	if maxTurns <= 0 {
		maxTurns = DefaultOpenRouterMaxTurns
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		timeout := cfg.RequestTimeout
		if timeout <= 0 {
			timeout = 2 * time.Minute
		}
		httpClient = &http.Client{Timeout: timeout}
	}
	return &Client{
		apiKey:          apiKey,
		baseURL:         baseURL,
		model:           model,
		defaultModel:    profile.DefaultModel,
		maxTurns:        maxTurns,
		httpClient:      httpClient,
		providerName:    profile.Name,
		extraHeaders:    profile.ExtraHeaders,
		maxTokensField:  firstNonEmpty(profile.MaxTokensField, "max_completion_tokens"),
		reasoningStyle:  profile.ReasoningStyle,
		omitTemperature: profile.OmitTemperature || cfg.OmitTemperature,
		temperature:     cfg.Temperature,
		providerOnly:    openRouterProviderOnly(profile.Name, cfg.ProviderOnly),
	}, nil
}

func (c *Client) Complete(ctx context.Context, messages []Message, tools []ToolDefinition) (Completion, error) {
	return c.CompleteWithOptions(ctx, messages, tools, CompletionOptions{})
}

func (c *Client) CompleteWithOptions(ctx context.Context, messages []Message, tools []ToolDefinition, opts CompletionOptions) (Completion, error) {
	if c.reasoningStyle == "openai" {
		return c.completeResponses(ctx, messages, tools, opts)
	}
	requestMessages := messages
	if c.shouldEnableAnthropicPromptCache() {
		requestMessages = withAnthropicPromptCache(messages)
	}
	body := map[string]any{
		"model":    c.model,
		"messages": requestMessages,
	}
	if !c.omitTemperature {
		temperature := DefaultChatTemperature
		if c.temperature != nil {
			temperature = *c.temperature
		}
		body["temperature"] = temperature
	}
	if len(c.providerOnly) > 0 {
		body["provider"] = map[string]any{
			"only":               c.providerOnly,
			"allow_fallbacks":    false,
			"require_parameters": true,
		}
	}
	if len(tools) > 0 {
		body["tools"] = tools
		body["tool_choice"] = "auto"
	}
	if opts.MaxCompletionTokens > 0 {
		body[firstNonEmpty(c.maxTokensField, "max_completion_tokens")] = opts.MaxCompletionTokens
	}
	if opts.ReasoningMaxTokens > 0 && strings.TrimSpace(opts.ReasoningEffort) != "" {
		return Completion{}, fmt.Errorf("%s reasoning options are mutually exclusive: max_tokens and effort", c.providerLabel())
	}
	switch c.reasoningStyle {
	case "deepseek":
		if opts.DisableThinking && strings.TrimSpace(opts.ReasoningEffort) != "" {
			return Completion{}, fmt.Errorf("%s thinking options are mutually exclusive: disabled and reasoning effort", c.providerLabel())
		}
		if opts.ReasoningMaxTokens > 0 {
			return Completion{}, fmt.Errorf("%s does not support reasoning max_tokens option", c.providerLabel())
		}
		if opts.DisableThinking {
			body["thinking"] = map[string]any{"type": "disabled"}
		} else if effort := strings.TrimSpace(opts.ReasoningEffort); effort != "" {
			body["thinking"] = map[string]any{"type": "enabled", "reasoning_effort": effort}
		}
	case "moonshot":
		if strings.TrimSpace(opts.ReasoningEffort) != "" || opts.ReasoningMaxTokens > 0 {
			return Completion{}, fmt.Errorf("%s does not support lcagent reasoning effort or max_tokens options", c.providerLabel())
		}
		if opts.DisableThinking {
			body["thinking"] = map[string]any{"type": "disabled"}
		}
	default:
		reasoning := map[string]any{}
		if opts.ReasoningMaxTokens > 0 {
			reasoning["max_tokens"] = opts.ReasoningMaxTokens
		}
		if effort := strings.TrimSpace(opts.ReasoningEffort); effort != "" {
			reasoning["effort"] = effort
		}
		if len(reasoning) > 0 {
			body["reasoning"] = reasoning
		}
	}
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(body); err != nil {
		return Completion{}, err
	}
	endpoint, err := url.JoinPath(c.baseURL, "chat", "completions")
	if err != nil {
		return Completion{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &buf)
	if err != nil {
		return Completion{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	for key, value := range c.extraHeaders {
		req.Header.Set(key, value)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return Completion{}, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8*1024*1024))
	if err != nil {
		return Completion{}, err
	}
	var parsed ChatResponse
	decodeErr := json.Unmarshal(data, &parsed)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if decodeErr != nil {
			if body := responseSnippet(data); body != "" {
				return Completion{}, fmt.Errorf("%s request failed: HTTP %d: %s", c.providerLabel(), resp.StatusCode, body)
			}
			return Completion{}, fmt.Errorf("%s request failed: HTTP %d", c.providerLabel(), resp.StatusCode)
		}
		if parsed.Error != nil && parsed.Error.Message != "" {
			return Completion{}, fmt.Errorf("%s request failed: HTTP %d: %s", c.providerLabel(), resp.StatusCode, parsed.Error.Message)
		}
		return Completion{}, fmt.Errorf("%s request failed: HTTP %d", c.providerLabel(), resp.StatusCode)
	}
	if decodeErr != nil {
		return Completion{}, fmt.Errorf("%s response decode failed: %w", c.providerLabel(), decodeErr)
	}
	if parsed.Error != nil && parsed.Error.Message != "" {
		return Completion{}, fmt.Errorf("%s request failed: %s", c.providerLabel(), parsed.Error.Message)
	}
	if len(parsed.Choices) == 0 {
		return Completion{}, fmt.Errorf("%s response had no choices", c.providerLabel())
	}
	choice := parsed.Choices[0]
	modelName := strings.TrimSpace(firstNonEmpty(parsed.Model, c.model))
	usage := UsageFromRaw(parsed.Usage, modelName)
	return Completion{
		Message:      choice.Message,
		ID:           strings.TrimSpace(parsed.ID),
		Model:        modelName,
		FinishReason: strings.TrimSpace(choice.FinishReason),
		Usage:        append(json.RawMessage(nil), parsed.Usage...),
		UsageSummary: usage,
	}, nil
}

func (c *Client) completeResponses(ctx context.Context, messages []Message, tools []ToolDefinition, opts CompletionOptions) (Completion, error) {
	if opts.DisableThinking {
		return Completion{}, fmt.Errorf("%s does not support disabling reasoning through lcagent options", c.providerLabel())
	}
	if opts.ReasoningMaxTokens > 0 {
		return Completion{}, fmt.Errorf("%s does not support lcagent reasoning max_tokens option", c.providerLabel())
	}
	usePrevious := c.previousResponseID != "" && len(tools) > 0
	instructions, input, usedPrevious := responsesInput(messages, usePrevious)
	body := map[string]any{
		"model": c.model,
		"input": input,
		"store": true,
	}
	if instructions != "" && !usedPrevious {
		body["instructions"] = instructions
	}
	if usedPrevious {
		body["previous_response_id"] = c.previousResponseID
	}
	if len(tools) > 0 {
		body["tools"] = responsesTools(tools)
		body["tool_choice"] = "auto"
	}
	if opts.MaxCompletionTokens > 0 {
		body["max_output_tokens"] = opts.MaxCompletionTokens
	}
	if effort := strings.TrimSpace(opts.ReasoningEffort); effort != "" {
		body["reasoning"] = map[string]any{"effort": effort}
	}
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(body); err != nil {
		return Completion{}, err
	}
	endpoint, err := url.JoinPath(c.baseURL, "responses")
	if err != nil {
		return Completion{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &buf)
	if err != nil {
		return Completion{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	for key, value := range c.extraHeaders {
		req.Header.Set(key, value)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return Completion{}, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8*1024*1024))
	if err != nil {
		return Completion{}, err
	}
	var parsed responsesResponse
	decodeErr := json.Unmarshal(data, &parsed)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if decodeErr != nil {
			if body := responseSnippet(data); body != "" {
				return Completion{}, fmt.Errorf("%s request failed: HTTP %d: %s", c.providerLabel(), resp.StatusCode, body)
			}
			return Completion{}, fmt.Errorf("%s request failed: HTTP %d", c.providerLabel(), resp.StatusCode)
		}
		if parsed.Error != nil && parsed.Error.Message != "" {
			return Completion{}, fmt.Errorf("%s request failed: HTTP %d: %s", c.providerLabel(), resp.StatusCode, parsed.Error.Message)
		}
		return Completion{}, fmt.Errorf("%s request failed: HTTP %d", c.providerLabel(), resp.StatusCode)
	}
	if decodeErr != nil {
		return Completion{}, fmt.Errorf("%s response decode failed: %w", c.providerLabel(), decodeErr)
	}
	if parsed.Error != nil && parsed.Error.Message != "" {
		return Completion{}, fmt.Errorf("%s request failed: %s", c.providerLabel(), parsed.Error.Message)
	}
	if len(parsed.Output) == 0 {
		return Completion{}, fmt.Errorf("%s response had no output", c.providerLabel())
	}
	msg := responsesMessage(parsed.Output)
	if strings.TrimSpace(msg.Content) == "" && len(msg.ToolCalls) == 0 {
		return Completion{}, fmt.Errorf("%s response had no content or function calls", c.providerLabel())
	}
	c.previousResponseID = strings.TrimSpace(parsed.ID)
	modelName := strings.TrimSpace(firstNonEmpty(parsed.Model, c.model))
	return Completion{
		Message:      msg,
		ID:           strings.TrimSpace(parsed.ID),
		Model:        modelName,
		FinishReason: strings.TrimSpace(parsed.Status),
		Usage:        append(json.RawMessage(nil), parsed.Usage...),
		UsageSummary: UsageFromRaw(parsed.Usage, modelName),
	}, nil
}

type responsesResponse struct {
	ID     string            `json:"id,omitempty"`
	Model  string            `json:"model,omitempty"`
	Status string            `json:"status,omitempty"`
	Output []json.RawMessage `json:"output,omitempty"`
	Usage  json.RawMessage   `json:"usage,omitempty"`
	Error  *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    any    `json:"code"`
	} `json:"error,omitempty"`
}

func responsesInput(messages []Message, usePrevious bool) (string, []any, bool) {
	if usePrevious {
		items := responsesContinuationInput(messages)
		if len(items) > 0 {
			return "", items, true
		}
	}
	var instructions []string
	var items []any
	for _, msg := range messages {
		switch msg.Role {
		case "system", "developer":
			if strings.TrimSpace(msg.Content) != "" {
				instructions = append(instructions, strings.TrimSpace(msg.Content))
			}
		case "tool":
			items = append(items, map[string]any{
				"type":    "function_call_output",
				"call_id": msg.ToolCallID,
				"output":  msg.Content,
			})
		case "assistant":
			items = append(items, responsesAssistantItems(msg)...)
		default:
			if strings.TrimSpace(msg.Content) != "" {
				items = append(items, map[string]any{
					"role":    firstNonEmpty(msg.Role, "user"),
					"content": msg.Content,
				})
			}
		}
	}
	return strings.Join(instructions, "\n\n"), items, false
}

func responsesContinuationInput(messages []Message) []any {
	lastAssistant := -1
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "assistant" {
			lastAssistant = i
			break
		}
	}
	if lastAssistant < 0 || lastAssistant+1 >= len(messages) {
		return nil
	}
	var items []any
	for _, msg := range messages[lastAssistant+1:] {
		switch msg.Role {
		case "tool":
			items = append(items, map[string]any{
				"type":    "function_call_output",
				"call_id": msg.ToolCallID,
				"output":  msg.Content,
			})
		case "user":
			if strings.TrimSpace(msg.Content) != "" {
				items = append(items, map[string]any{
					"role":    "user",
					"content": msg.Content,
				})
			}
		}
	}
	return items
}

func responsesAssistantItems(msg Message) []any {
	var items []any
	if strings.TrimSpace(msg.Content) != "" {
		items = append(items, map[string]any{
			"role":    "assistant",
			"content": msg.Content,
		})
	}
	for _, call := range msg.ToolCalls {
		items = append(items, map[string]any{
			"type":      "function_call",
			"call_id":   call.ID,
			"name":      call.Function.Name,
			"arguments": string(call.Function.Arguments),
		})
	}
	return items
}

func responsesTools(tools []ToolDefinition) []map[string]any {
	out := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		out = append(out, map[string]any{
			"type":        "function",
			"name":        tool.Function.Name,
			"description": tool.Function.Description,
			"parameters":  tool.Function.Parameters,
		})
	}
	return out
}

func responsesMessage(output []json.RawMessage) Message {
	msg := Message{Role: "assistant"}
	var textParts []string
	for _, raw := range output {
		var item struct {
			Type      string          `json:"type"`
			Role      string          `json:"role"`
			Content   json.RawMessage `json:"content"`
			CallID    string          `json:"call_id"`
			ID        string          `json:"id"`
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(raw, &item); err != nil {
			continue
		}
		switch item.Type {
		case "message":
			if text := responsesText(item.Content); text != "" {
				textParts = append(textParts, text)
			}
		case "function_call":
			callID := firstNonEmpty(item.CallID, item.ID)
			msg.ToolCalls = append(msg.ToolCalls, ToolCall{
				ID:   callID,
				Type: "function",
				Function: FunctionCall{
					Name:      item.Name,
					Arguments: item.Arguments,
				},
			})
		}
	}
	msg.Content = strings.TrimSpace(strings.Join(textParts, "\n"))
	return msg
}

func responsesText(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return strings.TrimSpace(text)
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &parts); err != nil {
		return ""
	}
	var out []string
	for _, part := range parts {
		if part.Type == "output_text" || part.Type == "text" {
			if strings.TrimSpace(part.Text) != "" {
				out = append(out, strings.TrimSpace(part.Text))
			}
		}
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

func UsageFromRaw(raw json.RawMessage, modelName string) model.LLMUsage {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return model.LLMUsage{}
	}
	var envelope struct {
		PromptTokens        int64 `json:"prompt_tokens"`
		PromptTokensDetails struct {
			CachedTokens int64 `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
		CompletionTokens        int64 `json:"completion_tokens"`
		CompletionTokensDetails struct {
			ReasoningTokens int64 `json:"reasoning_tokens"`
		} `json:"completion_tokens_details"`
		TotalTokens              int64 `json:"total_tokens"`
		PromptCacheHitTokens     int64 `json:"prompt_cache_hit_tokens"`
		PromptCacheMissTokens    int64 `json:"prompt_cache_miss_tokens"`
		CachedTokens             int64 `json:"cached_tokens"`
		CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
		CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`

		InputTokens        int64 `json:"input_tokens"`
		InputTokensDetails struct {
			CachedTokens int64 `json:"cached_tokens"`
		} `json:"input_tokens_details"`
		OutputTokens        int64 `json:"output_tokens"`
		OutputTokensDetails struct {
			ReasoningTokens int64 `json:"reasoning_tokens"`
		} `json:"output_tokens_details"`

		CachedInputTokens int64   `json:"cached_input_tokens"`
		ReasoningTokens   int64   `json:"reasoning_tokens"`
		Cost              float64 `json:"cost"`
		CostUSD           float64 `json:"cost_usd"`
		EstimatedCostUSD  float64 `json:"estimated_cost_usd"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return model.LLMUsage{}
	}
	inputTokens := firstPositiveInt64(envelope.InputTokens, envelope.PromptTokens)
	if envelope.PromptTokens == 0 && (envelope.CacheReadInputTokens > 0 || envelope.CacheCreationInputTokens > 0) {
		inputTokens += envelope.CacheReadInputTokens + envelope.CacheCreationInputTokens
	}
	usage := model.LLMUsage{
		InputTokens:       inputTokens,
		OutputTokens:      firstPositiveInt64(envelope.OutputTokens, envelope.CompletionTokens),
		TotalTokens:       envelope.TotalTokens,
		CachedInputTokens: firstPositiveInt64(envelope.CachedInputTokens, envelope.InputTokensDetails.CachedTokens, envelope.PromptTokensDetails.CachedTokens, envelope.PromptCacheHitTokens, envelope.CachedTokens, envelope.CacheReadInputTokens),
		ReasoningTokens:   firstPositiveInt64(envelope.ReasoningTokens, envelope.OutputTokensDetails.ReasoningTokens, envelope.CompletionTokensDetails.ReasoningTokens),
		EstimatedCostUSD:  firstPositiveFloat64(envelope.EstimatedCostUSD, envelope.CostUSD, envelope.Cost),
	}
	if usage.TotalTokens == 0 && (usage.InputTokens > 0 || usage.OutputTokens > 0) {
		usage.TotalTokens = usage.InputTokens + usage.OutputTokens
	}
	if usage.EstimatedCostUSD == 0 {
		if estimatedCostUSD, ok := model.EstimateLLMCostUSD(strings.TrimSpace(modelName), usage); ok {
			usage.EstimatedCostUSD = estimatedCostUSD
		}
	}
	return usage
}

func (c *Client) MaxTurns() int {
	if c == nil || c.maxTurns <= 0 {
		return DefaultOpenRouterMaxTurns
	}
	return c.maxTurns
}

func (c *Client) shouldEnableAnthropicPromptCache() bool {
	if c == nil || !strings.EqualFold(strings.TrimSpace(c.providerName), "openrouter") {
		return false
	}
	modelName := strings.ToLower(strings.TrimSpace(c.model))
	return strings.HasPrefix(modelName, "anthropic/")
}

func withAnthropicPromptCache(messages []Message) []Message {
	out := append([]Message(nil), messages...)
	for i := range out {
		if out[i].Role == "system" && strings.TrimSpace(out[i].Content) != "" {
			out[i].CacheControl = &CacheControl{Type: "ephemeral"}
			return out
		}
	}
	return out
}

func (c *Client) Model() string {
	if c == nil {
		return DefaultOpenRouterModel
	}
	if model := strings.TrimSpace(c.model); model != "" {
		return model
	}
	return firstNonEmpty(c.defaultModel, DefaultOpenRouterModel)
}

func (c *Client) providerLabel() string {
	if c == nil || strings.TrimSpace(c.providerName) == "" {
		return "chat completions"
	}
	return c.providerName
}

func LoadEnvFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		value = strings.Trim(value, `"'`)
		if key == "" {
			continue
		}
		if os.Getenv(key) == "" {
			if err := os.Setenv(key, value); err != nil {
				return err
			}
		}
	}
	return nil
}

type ToolOptions struct {
	ToolProfile             string
	DefaultReadLineLimit    int
	MaxReadLineLimit        int
	DefaultListEntryLimit   int
	MaxListEntryLimit       int
	DefaultSearchMaxMatch   int
	MaxSearchMaxMatch       int
	MaxSearchContextLines   int
	DefaultOutlineFileLimit int
	MaxOutlineFileLimit     int
	MaxModuleOutlineChars   int
	WebSearchEnabled        bool
}

func Tools() []ToolDefinition {
	return ToolsWithOptions(ToolOptions{})
}

func ToolsWithOptions(opts ToolOptions) []ToolDefinition {
	opts = opts.withDefaults()
	readDescription := "Read a bounded line range from a text file inside the workspace. Prefer targeted ranges after search or file_outline; use broad reads only when the whole range is relevant."
	limitDescription := fmt.Sprintf("Maximum lines to read. For scouting, prefer 40-100 lines; larger ranges are allowed when needed. Defaults to %d.", opts.DefaultReadLineLimit)
	if strings.EqualFold(opts.ToolProfile, "generous") {
		readDescription = "Read a bounded line range from a text file inside the workspace. Larger read limits are available in this run: after search or file_outline identifies a central file, prefer contiguous evidence-complete ranges over tiny samples."
		limitDescription = fmt.Sprintf("Maximum lines to read. For central files, prefer 120-300 line ranges and continue with next_offset when useful. Defaults to %d.", opts.DefaultReadLineLimit)
	}
	defs := []ToolDefinition{
		{
			Type: "function",
			Function: FunctionSpec{
				Name:        "read_file",
				Description: readDescription,
				Parameters: map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"path":   map[string]any{"type": "string", "description": "Workspace-relative path. Absolute paths are denied."},
						"offset": map[string]any{"type": "integer", "minimum": 1, "description": "1-based starting line. Defaults to 1."},
						"limit":  map[string]any{"type": "integer", "minimum": 1, "maximum": opts.MaxReadLineLimit, "description": limitDescription},
					},
					"required": []string{"path"},
				},
			},
		},
		{
			Type: "function",
			Function: FunctionSpec{
				Name:        "file_outline",
				Description: "Summarize structure for a source or Markdown file before reading raw text. Go files return package/import summary and top-level symbols with line ranges; Markdown files return headings with line ranges.",
				Parameters: map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"path": map[string]any{"type": "string", "description": "Workspace-relative path to a .go, .md, or .markdown file. Absolute paths are denied."},
					},
					"required": []string{"path"},
				},
			},
		},
		{
			Type: "function",
			Function: FunctionSpec{
				Name:        "module_outline",
				Description: "Summarize structure for many Go or Markdown files under a workspace directory before broad review. Use this before reading many files in the same module or package.",
				Parameters: map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"path":      map[string]any{"type": "string", "description": "Workspace-relative directory or file. Defaults to workspace root. Absolute paths are denied."},
						"file_glob": map[string]any{"type": "string", "description": "Optional filepath glob matched against relative path or basename, for example *.go."},
						"max_files": map[string]any{"type": "integer", "minimum": 1, "maximum": opts.MaxOutlineFileLimit, "description": fmt.Sprintf("Maximum files to outline. Defaults to %d.", opts.DefaultOutlineFileLimit)},
					},
				},
			},
		},
		{
			Type: "function",
			Function: FunctionSpec{
				Name:        "list_files",
				Description: "List files under a workspace path, optionally filtered by a simple glob.",
				Parameters: map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"path":        map[string]any{"type": "string", "description": "Workspace-relative directory or file to list. Defaults to workspace root. Absolute paths are denied."},
						"glob":        map[string]any{"type": "string", "description": "Optional filepath glob matched against relative path or basename."},
						"max_entries": map[string]any{"type": "integer", "minimum": 1, "maximum": opts.MaxListEntryLimit},
					},
				},
			},
		},
		{
			Type: "function",
			Function: FunctionSpec{
				Name:        "search",
				Description: "Search text files in the workspace with case-insensitive literal substring matching, optionally returning a small context window around each match. The query is not a regex, glob, or alternation pattern.",
				Parameters: map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"query":          map[string]any{"type": "string", "description": "Case-insensitive literal substring to find. Do not use regex syntax; use separate calls for separate identifiers or phrases."},
						"path":           map[string]any{"type": "string", "description": "Workspace-relative directory or file to search. Defaults to workspace root. Absolute paths are denied."},
						"file_glob":      map[string]any{"type": "string", "description": "Optional filepath glob matched against relative path or basename."},
						"max_matches":    map[string]any{"type": "integer", "minimum": 1, "maximum": opts.MaxSearchMaxMatch},
						"context_before": map[string]any{"type": "integer", "minimum": 0, "maximum": opts.MaxSearchContextLines, "description": "Lines of context before each match. Defaults to 1 in the harness."},
						"context_after":  map[string]any{"type": "integer", "minimum": 0, "maximum": opts.MaxSearchContextLines, "description": "Lines of context after each match. Defaults to 2 in the harness."},
					},
					"required": []string{"query"},
				},
			},
		},
	}
	if opts.WebSearchEnabled {
		defs = append(defs, ToolDefinition{
			Type: "function",
			Function: FunctionSpec{
				Name:        "web_search",
				Description: "Search the public web for current external information and return concise source results with URLs. Use this for discovery; use run_command or file tools for workspace-local evidence.",
				Parameters: map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"query":        map[string]any{"type": "string", "description": "Natural-language search query."},
						"max_results":  map[string]any{"type": "integer", "minimum": 1, "maximum": 10, "description": "Maximum results to return. Defaults to 5."},
						"site":         map[string]any{"type": "string", "description": "Optional domain to restrict results, for example docs.example.com."},
						"recency_days": map[string]any{"type": "integer", "minimum": 1, "maximum": 365, "description": "Optional recency window in days."},
					},
					"required": []string{"query"},
				},
			},
		})
	}
	defs = append(defs,
		ToolDefinition{
			Type: "function",
			Function: FunctionSpec{
				Name:        "load_skill",
				Description: "Load the full body of an available skill by name. Use only after checking the skill metadata in the system prompt.",
				Parameters: map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"name": map[string]any{"type": "string"},
					},
					"required": []string{"name"},
				},
			},
		},
		ToolDefinition{
			Type: "function",
			Function: FunctionSpec{
				Name:        "run_command",
				Description: "Run a bounded command in the workspace. Prefer argv. Use shell command strings only when shell behavior is genuinely needed.",
				Parameters: map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"argv":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Command argv, for example [\"go\",\"test\",\"./...\"]"},
						"command":    map[string]any{"type": "string", "description": "Legacy shell command string. Prefer argv unless shell syntax is required."},
						"shell":      map[string]any{"type": "boolean", "description": "Set true when using command as a shell string."},
						"timeout_ms": map[string]any{"type": "integer", "minimum": 1, "maximum": 60000},
					},
				},
			},
		},
		ToolDefinition{
			Type: "function",
			Function: FunctionSpec{
				Name:        "apply_patch",
				Description: "Apply a Codex apply_patch patch. The patch must use the exact envelope: *** Begin Patch, then *** Update File: path or *** Add File: path, hunks with @@ and +/- lines, then *** End Patch. Successful patches return a diff summary; use it when reporting changed files. Example: *** Begin Patch\n*** Update File: README.md\n@@\n-old\n+new\n*** End Patch",
				Parameters: map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"patch": map[string]any{"type": "string"},
					},
					"required": []string{"patch"},
				},
			},
		},
		ToolDefinition{
			Type: "function",
			Function: FunctionSpec{
				Name:        "update_plan",
				Description: "Publish the current short plan with statuses pending, in_progress, or completed.",
				Parameters: map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"items": map[string]any{
							"type": "array",
							"items": map[string]any{
								"type":                 "object",
								"additionalProperties": false,
								"properties": map[string]any{
									"step":   map[string]any{"type": "string"},
									"status": map[string]any{"type": "string", "enum": []string{"pending", "in_progress", "completed"}},
								},
								"required": []string{"step", "status"},
							},
						},
					},
					"required": []string{"items"},
				},
			},
		},
		ToolDefinition{
			Type: "function",
			Function: FunctionSpec{
				Name:        "final_response",
				Description: "Finish the session. The summary must be the complete user-facing answer, including findings, caveats, changed files, verification outcome, and next steps; do not put essential answer content only in verification.",
				Parameters: map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"summary":       map[string]any{"type": "string", "description": "Complete user-facing answer. Include the actual findings or outcome here, not only a preamble."},
						"files_changed": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Workspace files changed by this session, or an empty array."},
						"verification":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Checks run, evidence reviewed, or an explicit not-run reason. This supports the summary; it is not a substitute for the answer."},
					},
					"required": []string{"summary", "files_changed", "verification"},
				},
			},
		},
	)
	return defs
}

func (o ToolOptions) withDefaults() ToolOptions {
	if o.DefaultReadLineLimit <= 0 {
		o.DefaultReadLineLimit = 200
	}
	if o.MaxReadLineLimit <= 0 {
		o.MaxReadLineLimit = 1000
	}
	if o.DefaultListEntryLimit <= 0 {
		o.DefaultListEntryLimit = 200
	}
	if o.MaxListEntryLimit <= 0 {
		o.MaxListEntryLimit = 1000
	}
	if o.DefaultSearchMaxMatch <= 0 {
		o.DefaultSearchMaxMatch = 50
	}
	if o.MaxSearchMaxMatch <= 0 {
		o.MaxSearchMaxMatch = 200
	}
	if o.MaxSearchContextLines <= 0 {
		o.MaxSearchContextLines = 8
	}
	if o.DefaultOutlineFileLimit <= 0 {
		o.DefaultOutlineFileLimit = 30
	}
	if o.MaxOutlineFileLimit <= 0 {
		o.MaxOutlineFileLimit = 80
	}
	if o.MaxModuleOutlineChars <= 0 {
		o.MaxModuleOutlineChars = 24000
	}
	if o.MaxReadLineLimit < o.DefaultReadLineLimit {
		o.MaxReadLineLimit = o.DefaultReadLineLimit
	}
	if o.MaxListEntryLimit < o.DefaultListEntryLimit {
		o.MaxListEntryLimit = o.DefaultListEntryLimit
	}
	if o.MaxSearchMaxMatch < o.DefaultSearchMaxMatch {
		o.MaxSearchMaxMatch = o.DefaultSearchMaxMatch
	}
	if o.MaxOutlineFileLimit < o.DefaultOutlineFileLimit {
		o.MaxOutlineFileLimit = o.DefaultOutlineFileLimit
	}
	return o
}

type SystemPromptOptions struct {
	ToolProfile          string
	DefaultReadLineLimit int
	MaxReadLineLimit     int
	WebSearchEnabled     bool
}

func SystemPrompt(skillIndex, projectInstructions string) string {
	return SystemPromptWithOptions(skillIndex, projectInstructions, SystemPromptOptions{})
}

func SystemPromptWithOptions(skillIndex, projectInstructions string, opts SystemPromptOptions) string {
	if opts.DefaultReadLineLimit <= 0 {
		opts.DefaultReadLineLimit = 200
	}
	if opts.MaxReadLineLimit <= 0 {
		opts.MaxReadLineLimit = 1000
	}
	readScoutingLine := "When scouting with read_file, prefer 40-100 lines; use larger ranges only when the whole range is relevant. If read_file returns next_offset, continue there instead of overlapping the previous range."
	if strings.EqualFold(opts.ToolProfile, "generous") {
		readScoutingLine = "When a file is plausibly central, prefer 120-300 line read_file ranges and continue with next_offset until the relevant contiguous context is covered."
	}
	lines := []string{
		"You are lcagent, a small local coding-agent harness controlled by Little Control Room.",
		"Use the provided tools for all workspace inspection, edits, plan updates, and final responses.",
		"Do not claim to have inspected files or run verification unless a tool result shows that happened.",
		"For unfamiliar source or Markdown files, prefer file_outline before raw reads.",
		"For broad repo, package, or module review, prefer module_outline before reading many files.",
		"For specific behavior, identifiers, errors, commands, or tests, prefer search with context before raw reads.",
		"Search queries are case-insensitive literal substrings, not regexes, globs, or alternation patterns; use separate searches for separate identifiers or phrases.",
		"Use read_file for targeted ranges. Reading from line 1 is useful for imports/package context, but do not default to first-N-line scouting when an outline or search can locate the relevant range.",
		readScoutingLine,
	}
	if opts.WebSearchEnabled {
		lines = append(lines,
			"Use web_search for current public web information or documentation discovery when workspace evidence is not enough; cite URLs from the tool result in final_response when web evidence affects the answer.",
		)
	}
	lines = append(lines,
		"Use workspace-relative paths in file tools; absolute paths are denied.",
		"File tools are workspace-only; use read-only run_command argv for paths outside the workspace.",
		"When using run_command, prefer argv over command strings; shell commands are for shell syntax only.",
		"At low autonomy, use run_command argv for conservative Go verification such as [\"go\",\"test\",\"./...\"]; shell strings and broad write-like commands are denied.",
		"Never write provider tool-call markup such as DSML in assistant text; call tools only through structured tool_calls.",
		"Skill descriptions in this prompt are metadata only; call load_skill before relying on any skill instructions.",
		"Use apply_patch for source edits. Patches must use this exact shape: *** Begin Patch, *** Update File: path, @@, -old line, +new line, *** End Patch.",
		"After edits, use the patch diff summary and run or explain verification before final_response.",
		"When done, call final_response exactly once. Its summary must contain the full answer, changed files, and verification outcome. The verification array must name checks run or say not run with the reason; it is only supporting evidence.",
	)
	if strings.EqualFold(opts.ToolProfile, "generous") {
		lines = append(lines,
			fmt.Sprintf("For this run, read_file defaults to %d lines and permits up to %d lines. Once outline/search identifies a central file, read enough contiguous context to understand it rather than sampling only small first chunks.", opts.DefaultReadLineLimit, opts.MaxReadLineLimit),
		)
	}
	if strings.TrimSpace(skillIndex) != "" {
		lines = append(lines, "", strings.TrimSpace(skillIndex))
	}
	if strings.TrimSpace(projectInstructions) != "" {
		lines = append(lines, "", strings.TrimSpace(projectInstructions))
	}
	return strings.Join(lines, "\n")
}

func SanitizeAssistantContent(content string) (string, bool) {
	first := -1
	for _, marker := range providerToolMarkupMarkers {
		if idx := strings.Index(content, marker); idx >= 0 && (first == -1 || idx < first) {
			first = idx
		}
	}
	if first == -1 {
		return content, false
	}
	return strings.TrimSpace(content[:first]), true
}

func ContainsProviderToolMarkup(content string) bool {
	_, stripped := SanitizeAssistantContent(content)
	return stripped
}

func NormalizeArguments(raw json.RawMessage) (json.RawMessage, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return json.RawMessage(`{}`), nil
	}
	if raw[0] != '"' {
		return raw, nil
	}
	var encoded string
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return nil, err
	}
	encoded = strings.TrimSpace(encoded)
	if encoded == "" {
		return json.RawMessage(`{}`), nil
	}
	return json.RawMessage(encoded), nil
}

var providerToolMarkupMarkers = []string{
	"<\uff5cDSML\uff5c",
	"<|tool_calls|>",
	"<|tool_call|>",
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func openRouterProviderOnly(providerName string, values []string) []string {
	if !strings.EqualFold(strings.TrimSpace(providerName), "openrouter") {
		return nil
	}
	var out []string
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func firstPositiveInt64(values ...int64) int64 {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func firstPositiveFloat64(values ...float64) float64 {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func responseSnippet(data []byte) string {
	text := strings.TrimSpace(string(data))
	if text == "" {
		return ""
	}
	text = strings.Join(strings.Fields(text), " ")
	if len(text) > 512 {
		return text[:512] + "..."
	}
	return text
}
