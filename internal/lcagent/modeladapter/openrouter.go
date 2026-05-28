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
	DefaultXiaomiModel        = "mimo-v2.5-pro"
	DefaultXiaomiUtilityModel = "mimo-v2-flash"
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
	authHeader         string
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

type ListedModel struct {
	ID          string
	Name        string
	Description string
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

func NewXiaomiClient(cfg OpenRouterConfig) (*Client, error) {
	return newChatCompletionsClient(cfg, chatProviderProfile{
		Name:           "xiaomi",
		APIKeyEnv:      "XIAOMI_API_KEY",
		BaseURLEnv:     "XIAOMI_BASE_URL",
		DefaultBaseURL: "https://api.xiaomimimo.com/v1",
		DefaultModel:   DefaultXiaomiModel,
		AuthHeader:     "api-key",
		MaxTokensField: "max_tokens",
		ReasoningStyle: "deepseek",
		ExtraHeaders:   map[string]string{},
	})
}

type chatProviderProfile struct {
	Name            string
	APIKeyEnv       string
	BaseURLEnv      string
	DefaultBaseURL  string
	DefaultModel    string
	AuthHeader      string
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
	model = NormalizeModelForProvider(profile.Name, model)
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
		authHeader:      normalizeAuthHeader(profile.AuthHeader),
		maxTokensField:  firstNonEmpty(profile.MaxTokensField, "max_completion_tokens"),
		reasoningStyle:  profile.ReasoningStyle,
		omitTemperature: profile.OmitTemperature || cfg.OmitTemperature,
		temperature:     cfg.Temperature,
		providerOnly:    openRouterProviderOnly(profile.Name, cfg.ProviderOnly),
	}, nil
}

func NormalizeModelForProvider(provider, model string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		return ""
	}
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "openai":
		return trimProviderModelPrefix(model, "openai/")
	case "deepseek":
		return trimProviderModelPrefix(model, "deepseek/")
	case "moonshot":
		model = trimProviderModelPrefix(model, "moonshot/")
		return trimProviderModelPrefix(model, "moonshotai/")
	case "xiaomi":
		return trimProviderModelPrefix(model, "xiaomi/")
	default:
		return model
	}
}

func trimProviderModelPrefix(model, prefix string) string {
	if strings.HasPrefix(strings.ToLower(model), prefix) {
		return strings.TrimSpace(model[len(prefix):])
	}
	return model
}

func normalizeAuthHeader(header string) string {
	switch strings.ToLower(strings.TrimSpace(header)) {
	case "api-key":
		return "api-key"
	default:
		return "bearer"
	}
}

func (c *Client) setAPIKeyHeader(header http.Header) {
	if c == nil {
		return
	}
	apiKey := strings.TrimSpace(c.apiKey)
	if apiKey == "" {
		return
	}
	switch normalizeAuthHeader(c.authHeader) {
	case "api-key":
		header.Set("api-key", apiKey)
	default:
		header.Set("Authorization", "Bearer "+apiKey)
	}
}

func (c *Client) Complete(ctx context.Context, messages []Message, tools []ToolDefinition) (Completion, error) {
	return c.CompleteWithOptions(ctx, messages, tools, CompletionOptions{})
}

func (c *Client) ListModels(ctx context.Context) ([]ListedModel, error) {
	if c == nil {
		return nil, fmt.Errorf("provider client is not configured")
	}
	endpoint, err := url.JoinPath(c.baseURL, "models")
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(c.apiKey) != "" {
		c.setAPIKeyHeader(req.Header)
	}
	for key, value := range c.extraHeaders {
		req.Header.Set(key, value)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, newProviderRequestError(c.providerLabel(), err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8*1024*1024))
	if err != nil {
		return nil, newProviderRequestError(c.providerLabel(), err)
	}
	var parsed modelListResponse
	decodeErr := json.Unmarshal(data, &parsed)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if decodeErr != nil {
			if body := responseSnippet(data); body != "" {
				return nil, newProviderHTTPError(c.providerLabel(), resp.StatusCode, body, resp.Header.Get("Retry-After"))
			}
			return nil, newProviderHTTPError(c.providerLabel(), resp.StatusCode, "", resp.Header.Get("Retry-After"))
		}
		if parsed.Error != nil && parsed.Error.Message != "" {
			return nil, newProviderHTTPError(c.providerLabel(), resp.StatusCode, parsed.Error.Message, resp.Header.Get("Retry-After"))
		}
		return nil, newProviderHTTPError(c.providerLabel(), resp.StatusCode, "", resp.Header.Get("Retry-After"))
	}
	if decodeErr != nil {
		return nil, newProviderDecodeError(c.providerLabel(), decodeErr)
	}
	if parsed.Error != nil && parsed.Error.Message != "" {
		return nil, newProviderBodyError(c.providerLabel(), parsed.Error.Message, parsed.Error.Type)
	}
	models := make([]ListedModel, 0, len(parsed.Data))
	seen := map[string]struct{}{}
	for _, item := range parsed.Data {
		id := strings.TrimSpace(item.ID)
		if id == "" {
			continue
		}
		key := strings.ToLower(id)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		models = append(models, ListedModel{
			ID:          id,
			Name:        strings.TrimSpace(item.Name),
			Description: strings.TrimSpace(item.Description),
		})
	}
	if len(models) == 0 {
		return nil, newProviderSchemaError(c.providerLabel(), "model list response contained no models")
	}
	return models, nil
}

type modelListResponse struct {
	Data []struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Description string `json:"description"`
	} `json:"data"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    any    `json:"code"`
	} `json:"error,omitempty"`
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
	c.setAPIKeyHeader(req.Header)
	req.Header.Set("Content-Type", "application/json")
	for key, value := range c.extraHeaders {
		req.Header.Set(key, value)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return Completion{}, newProviderRequestError(c.providerLabel(), err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8*1024*1024))
	if err != nil {
		return Completion{}, newProviderRequestError(c.providerLabel(), err)
	}
	var parsed ChatResponse
	decodeErr := json.Unmarshal(data, &parsed)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if decodeErr != nil {
			if body := responseSnippet(data); body != "" {
				return Completion{}, newProviderHTTPError(c.providerLabel(), resp.StatusCode, body, resp.Header.Get("Retry-After"))
			}
			return Completion{}, newProviderHTTPError(c.providerLabel(), resp.StatusCode, "", resp.Header.Get("Retry-After"))
		}
		if parsed.Error != nil && parsed.Error.Message != "" {
			return Completion{}, newProviderHTTPError(c.providerLabel(), resp.StatusCode, parsed.Error.Message, resp.Header.Get("Retry-After"))
		}
		return Completion{}, newProviderHTTPError(c.providerLabel(), resp.StatusCode, "", resp.Header.Get("Retry-After"))
	}
	if decodeErr != nil {
		return Completion{}, newProviderDecodeError(c.providerLabel(), decodeErr)
	}
	if parsed.Error != nil && parsed.Error.Message != "" {
		return Completion{}, newProviderBodyError(c.providerLabel(), parsed.Error.Message, parsed.Error.Type)
	}
	if len(parsed.Choices) == 0 {
		return Completion{}, newProviderSchemaError(c.providerLabel(), "response had no choices")
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
	c.setAPIKeyHeader(req.Header)
	req.Header.Set("Content-Type", "application/json")
	for key, value := range c.extraHeaders {
		req.Header.Set(key, value)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return Completion{}, newProviderRequestError(c.providerLabel(), err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8*1024*1024))
	if err != nil {
		return Completion{}, newProviderRequestError(c.providerLabel(), err)
	}
	var parsed responsesResponse
	decodeErr := json.Unmarshal(data, &parsed)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if decodeErr != nil {
			if body := responseSnippet(data); body != "" {
				return Completion{}, newProviderHTTPError(c.providerLabel(), resp.StatusCode, body, resp.Header.Get("Retry-After"))
			}
			return Completion{}, newProviderHTTPError(c.providerLabel(), resp.StatusCode, "", resp.Header.Get("Retry-After"))
		}
		if parsed.Error != nil && parsed.Error.Message != "" {
			return Completion{}, newProviderHTTPError(c.providerLabel(), resp.StatusCode, parsed.Error.Message, resp.Header.Get("Retry-After"))
		}
		return Completion{}, newProviderHTTPError(c.providerLabel(), resp.StatusCode, "", resp.Header.Get("Retry-After"))
	}
	if decodeErr != nil {
		return Completion{}, newProviderDecodeError(c.providerLabel(), decodeErr)
	}
	if parsed.Error != nil && parsed.Error.Message != "" {
		return Completion{}, newProviderBodyError(c.providerLabel(), parsed.Error.Message, parsed.Error.Type)
	}
	if len(parsed.Output) == 0 {
		return Completion{}, newProviderSchemaError(c.providerLabel(), "response had no output")
	}
	msg := responsesMessage(parsed.Output)
	if strings.TrimSpace(msg.Content) == "" && len(msg.ToolCalls) == 0 {
		return Completion{}, newProviderSchemaError(c.providerLabel(), "response had no content or function calls")
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

func MaxTurnsForRequestTimeout(timeout time.Duration) int {
	switch {
	case timeout >= 45*time.Minute:
		return 128
	case timeout >= 20*time.Minute:
		return 96
	default:
		return DefaultOpenRouterMaxTurns
	}
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
