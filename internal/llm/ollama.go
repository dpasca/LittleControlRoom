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

func NewOllamaJSONSchemaRunner(baseURL, defaultModel string, timeout time.Duration, usage *UsageTracker) JSONSchemaRunner {
	baseRunner := NewOllamaChatClientWithBaseURL(baseURL, timeout, usage)
	if baseRunner == nil {
		return nil
	}
	discovery := NewOpenAICompatibleModelDiscovery(OllamaOpenAIBaseURL(baseURL), "ollama", timeout)
	return NewAutoModelRunner(discovery, baseRunner, defaultModel)
}

func NewOllamaTextRunner(baseURL, defaultModel string, timeout time.Duration, usage *UsageTracker) TextRunner {
	baseRunner := NewOllamaChatClientWithBaseURL(baseURL, timeout, usage)
	if baseRunner == nil {
		return nil
	}
	discovery := NewOpenAICompatibleModelDiscovery(OllamaOpenAIBaseURL(baseURL), "ollama", timeout)
	return NewAutoTextRunner(discovery, baseRunner, defaultModel)
}

func NewOllamaChatClientWithBaseURL(baseURL string, timeout time.Duration, usage *UsageTracker) *OllamaChatClient {
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	return &OllamaChatClient{
		endpoint:   OllamaNativeEndpoint(baseURL, "/api/chat"),
		httpClient: &http.Client{Timeout: timeout},
		usage:      usage,
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
		return TextResponse{}, errors.New("ollama chat client not configured")
	}
	modelName := strings.TrimSpace(req.Model)
	if modelName == "" {
		return TextResponse{}, errors.New("ollama text request requires a model")
	}
	messages := ollamaTextMessages(req.SystemText, req.Messages)
	if len(messages) == 0 {
		return TextResponse{}, errors.New("ollama text request requires at least one message")
	}
	response, err := c.runChat(ctx, modelName, messages, nil)
	if err != nil {
		return TextResponse{}, err
	}
	return TextResponse{
		Status:           ollamaResponseStatus(response.Done),
		Model:            firstNonEmptyString(response.Model, modelName),
		IncompleteReason: strings.TrimSpace(response.DoneReason),
		OutputText:       strings.TrimSpace(response.Message.Content),
		Usage:            response.usage(),
	}, nil
}

func (c *OllamaChatClient) RunJSONSchema(ctx context.Context, req JSONSchemaRequest) (JSONSchemaResponse, error) {
	if c == nil || strings.TrimSpace(c.endpoint) == "" {
		return JSONSchemaResponse{}, errors.New("ollama chat client not configured")
	}
	modelName := strings.TrimSpace(req.Model)
	if modelName == "" {
		return JSONSchemaResponse{}, errors.New("ollama JSON schema request requires a model")
	}
	messages := []ollamaChatMessage{
		{
			Role:    "system",
			Content: strings.TrimSpace(req.SystemText + "\n\nReturn only valid JSON. Do not wrap the JSON in markdown."),
		},
		{
			Role:    "user",
			Content: buildSchemaPrompt(req, true),
		},
	}
	response, err := c.runChat(ctx, modelName, messages, ollamaJSONFormat(req.Schema))
	if err != nil {
		return JSONSchemaResponse{}, err
	}
	return JSONSchemaResponse{
		Status:           ollamaResponseStatus(response.Done),
		Model:            firstNonEmptyString(response.Model, modelName),
		IncompleteReason: strings.TrimSpace(response.DoneReason),
		OutputText:       strings.TrimSpace(response.Message.Content),
		Usage:            response.usage(),
	}, nil
}

type ollamaChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ollamaChatResponse struct {
	Model   string `json:"model"`
	Message struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"message"`
	Done               bool   `json:"done"`
	DoneReason         string `json:"done_reason"`
	PromptEvalCount    int64  `json:"prompt_eval_count"`
	PromptEvalDuration int64  `json:"prompt_eval_duration"`
	EvalCount          int64  `json:"eval_count"`
	EvalDuration       int64  `json:"eval_duration"`
	TotalDuration      int64  `json:"total_duration"`
	Error              string `json:"error"`
}

func (c *OllamaChatClient) runChat(ctx context.Context, modelName string, messages []ollamaChatMessage, format any) (ollamaChatResponse, error) {
	reqBody := map[string]any{
		"model":    modelName,
		"messages": messages,
		"stream":   false,
		"think":    false,
	}
	if format != nil {
		reqBody["format"] = format
	}
	raw, err := json.Marshal(reqBody)
	if err != nil {
		return ollamaChatResponse{}, fmt.Errorf("marshal ollama chat request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(raw))
	if err != nil {
		return ollamaChatResponse{}, fmt.Errorf("create ollama chat request: %w", err)
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
		return ollamaChatResponse{}, fmt.Errorf("send ollama chat request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		if c.usage != nil {
			c.usage.Fail(modelName)
		}
		return ollamaChatResponse{}, fmt.Errorf("read ollama chat response: %w", err)
	}
	if resp.StatusCode >= 300 {
		if c.usage != nil {
			c.usage.Fail(modelName)
		}
		return ollamaChatResponse{}, &HTTPStatusError{
			StatusCode: resp.StatusCode,
			Status:     resp.Status,
			Body:       string(body),
			RetryAfter: resp.Header.Get("Retry-After"),
		}
	}

	var parsed ollamaChatResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		if c.usage != nil {
			c.usage.Fail(modelName)
		}
		return ollamaChatResponse{}, fmt.Errorf("decode ollama chat response: %w", err)
	}
	if strings.TrimSpace(parsed.Error) != "" {
		if c.usage != nil {
			c.usage.Fail(modelName)
		}
		return ollamaChatResponse{}, errors.New(strings.TrimSpace(parsed.Error))
	}
	if c.usage != nil {
		modelForUsage := firstNonEmptyString(parsed.Model, modelName)
		c.usage.Complete(modelForUsage, parsed.usage())
	}
	return parsed, nil
}

func (r ollamaChatResponse) usage() model.LLMUsage {
	usage := model.LLMUsage{
		InputTokens:  r.PromptEvalCount,
		OutputTokens: r.EvalCount,
	}
	usage.TotalTokens = usage.InputTokens + usage.OutputTokens
	return usage
}

func ollamaTextMessages(systemText string, messages []TextMessage) []ollamaChatMessage {
	out := make([]ollamaChatMessage, 0, len(messages)+1)
	if system := strings.TrimSpace(systemText); system != "" {
		out = append(out, ollamaChatMessage{Role: "system", Content: system})
	}
	for _, message := range messages {
		content := strings.TrimSpace(message.Content)
		if content == "" {
			continue
		}
		out = append(out, ollamaChatMessage{
			Role:    normalizeTextMessageRole(message.Role),
			Content: content,
		})
	}
	return out
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
