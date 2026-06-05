package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"lcroom/internal/model"
)

const defaultOllamaBaseURL = "http://127.0.0.1:11434"

type OllamaChatClient struct {
	endpoint   string
	httpClient *http.Client
	usage      *UsageTracker
	think      bool
}

type OllamaModelMetadata struct {
	Model          string
	ContextWindow  int64
	ParameterSize  string
	Quantization   string
	Architecture   string
	Capabilities   []string
	DetailsPresent bool
}

type OllamaChatOptions struct {
	Think bool
}

func NewOllamaJSONSchemaRunner(baseURL, defaultModel string, timeout time.Duration, usage *UsageTracker) JSONSchemaRunner {
	return NewOllamaJSONSchemaRunnerWithOptions(baseURL, defaultModel, timeout, usage, OllamaChatOptions{})
}

func NewOllamaJSONSchemaRunnerWithOptions(baseURL, defaultModel string, timeout time.Duration, usage *UsageTracker, opts OllamaChatOptions) JSONSchemaRunner {
	baseRunner := NewOllamaChatClientWithBaseURLAndOptions(baseURL, timeout, usage, opts)
	if baseRunner == nil {
		return nil
	}
	discovery := NewOpenAICompatibleModelDiscovery(OllamaOpenAIBaseURL(baseURL), "ollama", timeout)
	return NewAutoModelRunner(discovery, baseRunner, defaultModel)
}

func NewOllamaTextRunner(baseURL, defaultModel string, timeout time.Duration, usage *UsageTracker) TextRunner {
	return NewOllamaTextRunnerWithOptions(baseURL, defaultModel, timeout, usage, OllamaChatOptions{})
}

func NewOllamaTextRunnerWithOptions(baseURL, defaultModel string, timeout time.Duration, usage *UsageTracker, opts OllamaChatOptions) TextRunner {
	baseRunner := NewOllamaChatClientWithBaseURLAndOptions(baseURL, timeout, usage, opts)
	if baseRunner == nil {
		return nil
	}
	discovery := NewOpenAICompatibleModelDiscovery(OllamaOpenAIBaseURL(baseURL), "ollama", timeout)
	return NewAutoTextRunner(discovery, baseRunner, defaultModel)
}

func NewOllamaChatClientWithBaseURL(baseURL string, timeout time.Duration, usage *UsageTracker) *OllamaChatClient {
	return NewOllamaChatClientWithBaseURLAndOptions(baseURL, timeout, usage, OllamaChatOptions{})
}

func NewOllamaChatClientWithBaseURLAndOptions(baseURL string, timeout time.Duration, usage *UsageTracker, opts OllamaChatOptions) *OllamaChatClient {
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	return &OllamaChatClient{
		endpoint:   OllamaNativeEndpoint(baseURL, "/api/generate"),
		httpClient: &http.Client{Timeout: timeout},
		usage:      usage,
		think:      opts.Think,
	}
}

func OllamaOpenAIBaseURL(baseURL string) string {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		return defaultOllamaBaseURL + "/v1"
	}
	if strings.HasSuffix(base, "/v1") {
		return base
	}
	return base + "/v1"
}

func OllamaNativeBaseURL(baseURL string) string {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		return defaultOllamaBaseURL
	}
	if strings.HasSuffix(base, "/v1") {
		base = strings.TrimRight(strings.TrimSuffix(base, "/v1"), "/")
	}
	if strings.HasSuffix(base, "/api/chat") {
		base = strings.TrimRight(strings.TrimSuffix(base, "/api/chat"), "/")
	}
	if strings.HasSuffix(base, "/api/generate") {
		base = strings.TrimRight(strings.TrimSuffix(base, "/api/generate"), "/")
	}
	if strings.HasSuffix(base, "/api/show") {
		base = strings.TrimRight(strings.TrimSuffix(base, "/api/show"), "/")
	}
	if base == "" {
		return defaultOllamaBaseURL
	}
	return base
}

func OllamaNativeEndpoint(baseURL, path string) string {
	path = "/" + strings.TrimLeft(strings.TrimSpace(path), "/")
	return OllamaNativeBaseURL(baseURL) + path
}

func FetchOllamaModelMetadata(ctx context.Context, baseURL, modelName string, timeout time.Duration) (OllamaModelMetadata, error) {
	modelName = strings.TrimSpace(modelName)
	if modelName == "" {
		return OllamaModelMetadata{}, errors.New("ollama model metadata requires a model")
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	raw, err := json.Marshal(map[string]any{"model": modelName})
	if err != nil {
		return OllamaModelMetadata{}, fmt.Errorf("marshal ollama model metadata request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, OllamaNativeEndpoint(baseURL, "/api/show"), bytes.NewReader(raw))
	if err != nil {
		return OllamaModelMetadata{}, fmt.Errorf("create ollama model metadata request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := (&http.Client{Timeout: timeout}).Do(req)
	if err != nil {
		return OllamaModelMetadata{}, fmt.Errorf("send ollama model metadata request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return OllamaModelMetadata{}, fmt.Errorf("read ollama model metadata response: %w", err)
	}
	if resp.StatusCode >= 300 {
		return OllamaModelMetadata{}, &HTTPStatusError{
			StatusCode: resp.StatusCode,
			Status:     resp.Status,
			Body:       string(body),
		}
	}
	return DecodeOllamaModelMetadata(body, modelName)
}

func DecodeOllamaModelMetadata(body []byte, fallbackModel string) (OllamaModelMetadata, error) {
	var envelope struct {
		Details struct {
			ParameterSize     string `json:"parameter_size"`
			QuantizationLevel string `json:"quantization_level"`
			ContextLength     int64  `json:"context_length"`
		} `json:"details"`
		ModelInfo    map[string]any `json:"model_info"`
		Capabilities []string       `json:"capabilities"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return OllamaModelMetadata{}, fmt.Errorf("decode ollama model metadata response: %w", err)
	}
	meta := OllamaModelMetadata{
		Model:          strings.TrimSpace(fallbackModel),
		ContextWindow:  envelope.Details.ContextLength,
		ParameterSize:  strings.TrimSpace(envelope.Details.ParameterSize),
		Quantization:   strings.TrimSpace(envelope.Details.QuantizationLevel),
		Capabilities:   trimNonEmptyStrings(envelope.Capabilities),
		DetailsPresent: len(envelope.ModelInfo) > 0 || envelope.Details.ContextLength > 0 || envelope.Details.ParameterSize != "" || envelope.Details.QuantizationLevel != "",
	}
	for key, value := range envelope.ModelInfo {
		normalized := strings.ToLower(strings.TrimSpace(key))
		switch {
		case strings.HasSuffix(normalized, ".context_length") || normalized == "context_length":
			if parsed, ok := numericMetadataValue(value); ok && parsed > meta.ContextWindow {
				meta.ContextWindow = parsed
			}
		case normalized == "general.architecture":
			if text, ok := value.(string); ok {
				meta.Architecture = strings.TrimSpace(text)
			}
		}
	}
	return meta, nil
}

func (c *OllamaChatClient) RunText(ctx context.Context, req TextRequest) (TextResponse, error) {
	if c == nil || strings.TrimSpace(c.endpoint) == "" {
		return TextResponse{}, errors.New("ollama generate client not configured")
	}
	modelName := strings.TrimSpace(req.Model)
	if modelName == "" {
		return TextResponse{}, errors.New("ollama text request requires a model")
	}
	systemText := strings.TrimSpace(req.SystemText)
	prompt := ollamaGeneratePrompt(req.Messages)
	if systemText == "" && prompt == "" {
		return TextResponse{}, errors.New("ollama text request requires at least one message")
	}
	if prompt == "" {
		prompt = systemText
		systemText = ""
	}
	response, err := c.runGenerate(ctx, modelName, systemText, prompt, nil)
	if err != nil {
		return TextResponse{}, err
	}
	return TextResponse{
		Status:           ollamaResponseStatus(response.Done),
		Model:            firstNonEmptyString(response.Model, modelName),
		IncompleteReason: strings.TrimSpace(response.DoneReason),
		OutputText:       strings.TrimSpace(StripThinkingBlocks(response.Response)),
		Usage:            response.usage(),
	}, nil
}

func (c *OllamaChatClient) RunJSONSchema(ctx context.Context, req JSONSchemaRequest) (JSONSchemaResponse, error) {
	if c == nil || strings.TrimSpace(c.endpoint) == "" {
		return JSONSchemaResponse{}, errors.New("ollama generate client not configured")
	}
	modelName := strings.TrimSpace(req.Model)
	if modelName == "" {
		return JSONSchemaResponse{}, errors.New("ollama JSON schema request requires a model")
	}
	systemText := strings.TrimSpace(req.SystemText + "\n\nReturn only valid JSON. Do not wrap the JSON in markdown.")
	response, err := c.runGenerate(ctx, modelName, systemText, buildSchemaPrompt(req, true), ollamaJSONFormat(req.Schema))
	if err != nil {
		return JSONSchemaResponse{}, err
	}
	return JSONSchemaResponse{
		Status:           ollamaResponseStatus(response.Done),
		Model:            firstNonEmptyString(response.Model, modelName),
		IncompleteReason: strings.TrimSpace(response.DoneReason),
		OutputText:       strings.TrimSpace(StripThinkingBlocks(response.Response)),
		Usage:            response.usage(),
	}, nil
}

type ollamaGenerateResponse struct {
	Model              string `json:"model"`
	Response           string `json:"response"`
	Thinking           string `json:"thinking"`
	Done               bool   `json:"done"`
	DoneReason         string `json:"done_reason"`
	PromptEvalCount    int64  `json:"prompt_eval_count"`
	PromptEvalDuration int64  `json:"prompt_eval_duration"`
	EvalCount          int64  `json:"eval_count"`
	EvalDuration       int64  `json:"eval_duration"`
	TotalDuration      int64  `json:"total_duration"`
	Error              string `json:"error"`
}

func (c *OllamaChatClient) runGenerate(ctx context.Context, modelName, systemText, prompt string, format any) (ollamaGenerateResponse, error) {
	reqBody := map[string]any{
		"model":  modelName,
		"prompt": strings.TrimSpace(prompt),
		"stream": false,
		"think":  c.think,
	}
	if system := strings.TrimSpace(systemText); system != "" {
		reqBody["system"] = system
	}
	if format != nil {
		reqBody["format"] = format
	}
	raw, err := json.Marshal(reqBody)
	if err != nil {
		return ollamaGenerateResponse{}, fmt.Errorf("marshal ollama generate request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(raw))
	if err != nil {
		return ollamaGenerateResponse{}, fmt.Errorf("create ollama generate request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	if c.usage != nil {
		c.usage.Start(modelName)
	}
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		if c.usage != nil {
			c.usage.Fail(modelName)
		}
		return ollamaGenerateResponse{}, fmt.Errorf("send ollama generate request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		if c.usage != nil {
			c.usage.Fail(modelName)
		}
		return ollamaGenerateResponse{}, fmt.Errorf("read ollama generate response: %w", err)
	}
	if resp.StatusCode >= 300 {
		if c.usage != nil {
			c.usage.Fail(modelName)
		}
		return ollamaGenerateResponse{}, &HTTPStatusError{
			StatusCode: resp.StatusCode,
			Status:     resp.Status,
			Body:       string(body),
			RetryAfter: resp.Header.Get("Retry-After"),
		}
	}

	var parsed ollamaGenerateResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		if c.usage != nil {
			c.usage.Fail(modelName)
		}
		return ollamaGenerateResponse{}, fmt.Errorf("decode ollama generate response: %w", err)
	}
	if strings.TrimSpace(parsed.Error) != "" {
		if c.usage != nil {
			c.usage.Fail(modelName)
		}
		return ollamaGenerateResponse{}, errors.New(strings.TrimSpace(parsed.Error))
	}
	if c.usage != nil {
		modelForUsage := firstNonEmptyString(parsed.Model, modelName)
		c.usage.Complete(modelForUsage, parsed.usage())
	}
	return parsed, nil
}

func (r ollamaGenerateResponse) usage() model.LLMUsage {
	usage := model.LLMUsage{
		InputTokens:        r.PromptEvalCount,
		OutputTokens:       r.EvalCount,
		PromptEvalDuration: time.Duration(r.PromptEvalDuration),
		OutputEvalDuration: time.Duration(r.EvalDuration),
	}
	usage.TotalTokens = usage.InputTokens + usage.OutputTokens
	return usage
}

func ollamaGeneratePrompt(messages []TextMessage) string {
	parts := make([]string, 0, len(messages)+1)
	for _, message := range messages {
		content := strings.TrimSpace(message.Content)
		if content == "" {
			continue
		}
		role := normalizeTextMessageRole(message.Role)
		if role == "" || role == "user" && len(parts) == 0 {
			parts = append(parts, content)
			continue
		}
		parts = append(parts, strings.ToUpper(role[:1])+role[1:]+": "+content)
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "\n\n")
}

func ollamaJSONFormat(schema map[string]any) any {
	if len(schema) == 0 {
		return "json"
	}
	return schema
}

func ollamaResponseStatus(done bool) string {
	if done {
		return "completed"
	}
	return "incomplete"
}

func numericMetadataValue(value any) (int64, bool) {
	switch typed := value.(type) {
	case float64:
		return int64(typed), true
	case int64:
		return typed, true
	case int:
		return int64(typed), true
	case json.Number:
		parsed, err := typed.Int64()
		return parsed, err == nil
	default:
		return 0, false
	}
}

func trimNonEmptyStrings(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	return out
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
