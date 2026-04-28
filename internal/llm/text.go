package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"lcroom/internal/model"
)

type TextRunner interface {
	RunText(ctx context.Context, req TextRequest) (TextResponse, error)
}

type StreamingTextRunner interface {
	RunTextStream(ctx context.Context, req TextRequest, handle TextStreamHandler) (TextResponse, error)
}

type TextStreamHandler func(TextStreamEvent) error

type TextStreamEvent struct {
	Delta string
}

type TextMessage struct {
	Role    string
	Content string
}

type TextRequest struct {
	Model           string
	SystemText      string
	Messages        []TextMessage
	ReasoningEffort string
}

type TextResponse struct {
	Status           string
	Model            string
	IncompleteReason string
	OutputText       string
	Usage            model.LLMUsage
}

type ResponsesTextClient struct {
	apiKey     string
	endpoint   string
	httpClient *http.Client
	usage      *UsageTracker
}

type OpenAICompatibleChatTextClient struct {
	apiKey     string
	endpoint   string
	httpClient *http.Client
	usage      *UsageTracker
}

type AutoTextRunner struct {
	discovery    *OpenAICompatibleModelDiscovery
	baseRunner   TextRunner
	defaultModel string
}

func NewResponsesTextClient(apiKey string, timeout time.Duration, usage *UsageTracker) *ResponsesTextClient {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return nil
	}
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	baseURL := strings.TrimSpace(os.Getenv("OPENAI_BASE_URL"))
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	return NewResponsesTextClientWithBaseURLAndHTTPClient(apiKey, baseURL, &http.Client{Timeout: timeout}, usage)
}

func NewResponsesTextClientWithBaseURL(apiKey, baseURL string, timeout time.Duration, usage *UsageTracker) *ResponsesTextClient {
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	return NewResponsesTextClientWithBaseURLAndHTTPClient(apiKey, baseURL, &http.Client{Timeout: timeout}, usage)
}

func NewResponsesTextClientWithBaseURLAndHTTPClient(apiKey, baseURL string, httpClient *http.Client, usage *UsageTracker) *ResponsesTextClient {
	return NewResponsesTextClientWithHTTPClient(apiKey, ResponsesEndpointFromBaseURL(baseURL), httpClient, usage)
}

func NewResponsesTextClientWithHTTPClient(apiKey, endpoint string, httpClient *http.Client, usage *UsageTracker) *ResponsesTextClient {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return nil
	}
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		endpoint = "https://api.openai.com/v1/responses"
	}
	httpClientToUse := httpClient
	if httpClientToUse == nil {
		httpClientToUse = &http.Client{Timeout: 60 * time.Second}
	}
	return &ResponsesTextClient{
		apiKey:     apiKey,
		endpoint:   endpoint,
		httpClient: httpClientToUse,
		usage:      usage,
	}
}

func NewOpenAICompatibleChatTextClientWithBaseURL(apiKey, baseURL string, timeout time.Duration, usage *UsageTracker) *OpenAICompatibleChatTextClient {
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	return NewOpenAICompatibleChatTextClientWithHTTPClient(
		apiKey,
		ChatCompletionsEndpointFromBaseURL(baseURL),
		&http.Client{Timeout: timeout},
		usage,
	)
}

func NewOpenAICompatibleChatTextClientWithHTTPClient(apiKey, endpoint string, httpClient *http.Client, usage *UsageTracker) *OpenAICompatibleChatTextClient {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return nil
	}
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		endpoint = "https://api.openai.com/v1/chat/completions"
	}
	httpClientToUse := httpClient
	if httpClientToUse == nil {
		httpClientToUse = &http.Client{Timeout: 60 * time.Second}
	}
	return &OpenAICompatibleChatTextClient{
		apiKey:     apiKey,
		endpoint:   endpoint,
		httpClient: httpClientToUse,
		usage:      usage,
	}
}

func NewAutoTextRunner(discovery *OpenAICompatibleModelDiscovery, baseRunner TextRunner, defaultModel string) *AutoTextRunner {
	return &AutoTextRunner{
		discovery:    discovery,
		baseRunner:   baseRunner,
		defaultModel: strings.TrimSpace(defaultModel),
	}
}

func NewOpenAICompatibleTextRunner(baseURL, apiKey, defaultModel string, timeout time.Duration, usage *UsageTracker) TextRunner {
	baseRunner := NewOpenAICompatibleChatTextClientWithBaseURL(apiKey, baseURL, timeout, usage)
	if baseRunner == nil {
		return nil
	}
	discovery := NewOpenAICompatibleModelDiscovery(baseURL, apiKey, timeout)
	return NewAutoTextRunner(discovery, baseRunner, defaultModel)
}

func (c *ResponsesTextClient) RunText(ctx context.Context, req TextRequest) (TextResponse, error) {
	if c == nil || c.apiKey == "" {
		return TextResponse{}, errors.New("openai responses text client not configured")
	}
	modelName := strings.TrimSpace(req.Model)
	if modelName == "" {
		return TextResponse{}, errors.New("openai responses text request requires a model")
	}

	input := make([]any, 0, len(req.Messages)+1)
	if system := strings.TrimSpace(req.SystemText); system != "" {
		input = append(input, responseTextMessage("system", system))
	}
	for _, message := range req.Messages {
		content := strings.TrimSpace(message.Content)
		if content == "" {
			continue
		}
		input = append(input, responseTextMessage(normalizeTextMessageRole(message.Role), content))
	}
	if len(input) == 0 {
		return TextResponse{}, errors.New("openai responses text request requires at least one message")
	}

	reqBody := map[string]any{
		"model": modelName,
		"input": input,
		"store": false,
	}
	if effort := strings.TrimSpace(req.ReasoningEffort); effort != "" {
		reqBody["reasoning"] = map[string]any{"effort": effort}
	}

	raw, err := json.Marshal(reqBody)
	if err != nil {
		return TextResponse{}, fmt.Errorf("marshal openai text request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(raw))
	if err != nil {
		return TextResponse{}, fmt.Errorf("create openai text request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")

	if c.usage != nil {
		c.usage.Start(modelName)
	}
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		if c.usage != nil {
			c.usage.Fail(modelName)
		}
		return TextResponse{}, fmt.Errorf("send openai text request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		if c.usage != nil {
			c.usage.Fail(modelName)
		}
		return TextResponse{}, fmt.Errorf("read openai text response: %w", err)
	}
	if resp.StatusCode >= 300 {
		if c.usage != nil {
			c.usage.Fail(modelName)
		}
		return TextResponse{}, &HTTPStatusError{
			StatusCode: resp.StatusCode,
			Status:     resp.Status,
			Body:       string(body),
			RetryAfter: resp.Header.Get("Retry-After"),
		}
	}

	parsed, err := decodeResponsesTextEnvelope(body)
	if err != nil {
		if c.usage != nil {
			c.usage.Fail(modelName)
		}
		return TextResponse{}, err
	}
	if parsed.Model == "" {
		parsed.Model = modelName
	}
	if c.usage != nil {
		c.usage.Complete(parsed.Model, parsed.Usage)
	}
	return parsed, nil
}

func (c *OpenAICompatibleChatTextClient) RunText(ctx context.Context, req TextRequest) (TextResponse, error) {
	if c == nil || c.apiKey == "" {
		return TextResponse{}, errors.New("openai-compatible chat text client not configured")
	}
	modelName := strings.TrimSpace(req.Model)
	if modelName == "" {
		return TextResponse{}, errors.New("openai-compatible chat text request requires a model")
	}

	messages := make([]any, 0, len(req.Messages)+1)
	if system := strings.TrimSpace(req.SystemText); system != "" {
		messages = append(messages, map[string]any{"role": "system", "content": system})
	}
	for _, message := range req.Messages {
		content := strings.TrimSpace(message.Content)
		if content == "" {
			continue
		}
		messages = append(messages, map[string]any{
			"role":    normalizeTextMessageRole(message.Role),
			"content": content,
		})
	}
	if len(messages) == 0 {
		return TextResponse{}, errors.New("openai-compatible chat text request requires at least one message")
	}

	reqBody := map[string]any{
		"model":    modelName,
		"messages": messages,
		"stream":   false,
	}
	raw, err := json.Marshal(reqBody)
	if err != nil {
		return TextResponse{}, fmt.Errorf("marshal openai-compatible chat text request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(raw))
	if err != nil {
		return TextResponse{}, fmt.Errorf("create openai-compatible chat text request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")

	if c.usage != nil {
		c.usage.Start(modelName)
	}
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		if c.usage != nil {
			c.usage.Fail(modelName)
		}
		return TextResponse{}, fmt.Errorf("send openai-compatible chat text request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		if c.usage != nil {
			c.usage.Fail(modelName)
		}
		return TextResponse{}, fmt.Errorf("read openai-compatible chat text response: %w", err)
	}
	if resp.StatusCode >= 300 {
		if c.usage != nil {
			c.usage.Fail(modelName)
		}
		return TextResponse{}, &HTTPStatusError{
			StatusCode: resp.StatusCode,
			Status:     resp.Status,
			Body:       string(body),
			RetryAfter: resp.Header.Get("Retry-After"),
		}
	}

	parsed, err := decodeChatTextEnvelope(body)
	if err != nil {
		if c.usage != nil {
			c.usage.Fail(modelName)
		}
		return TextResponse{}, err
	}
	if parsed.Model == "" {
		parsed.Model = modelName
	}
	if c.usage != nil {
		c.usage.Complete(parsed.Model, parsed.Usage)
	}
	return parsed, nil
}

func (c *ResponsesTextClient) RunTextStream(ctx context.Context, req TextRequest, handle TextStreamHandler) (TextResponse, error) {
	if c == nil || c.apiKey == "" {
		return TextResponse{}, errors.New("openai responses text client not configured")
	}
	modelName := strings.TrimSpace(req.Model)
	if modelName == "" {
		return TextResponse{}, errors.New("openai responses text request requires a model")
	}
	if handle == nil {
		handle = func(TextStreamEvent) error { return nil }
	}

	input := make([]any, 0, len(req.Messages)+1)
	if system := strings.TrimSpace(req.SystemText); system != "" {
		input = append(input, responseTextMessage("system", system))
	}
	for _, message := range req.Messages {
		content := strings.TrimSpace(message.Content)
		if content == "" {
			continue
		}
		input = append(input, responseTextMessage(normalizeTextMessageRole(message.Role), content))
	}
	if len(input) == 0 {
		return TextResponse{}, errors.New("openai responses text request requires at least one message")
	}

	reqBody := map[string]any{
		"model":  modelName,
		"input":  input,
		"store":  false,
		"stream": true,
	}
	if effort := strings.TrimSpace(req.ReasoningEffort); effort != "" {
		reqBody["reasoning"] = map[string]any{"effort": effort}
	}

	raw, err := json.Marshal(reqBody)
	if err != nil {
		return TextResponse{}, fmt.Errorf("marshal openai text request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(raw))
	if err != nil {
		return TextResponse{}, fmt.Errorf("create openai text request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")

	if c.usage != nil {
		c.usage.Start(modelName)
	}
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		if c.usage != nil {
			c.usage.Fail(modelName)
		}
		return TextResponse{}, fmt.Errorf("send openai text request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		body, readErr := io.ReadAll(resp.Body)
		if c.usage != nil {
			c.usage.Fail(modelName)
		}
		if readErr != nil {
			return TextResponse{}, fmt.Errorf("read openai text response: %w", readErr)
		}
		return TextResponse{}, &HTTPStatusError{
			StatusCode: resp.StatusCode,
			Status:     resp.Status,
			Body:       string(body),
			RetryAfter: resp.Header.Get("Retry-After"),
		}
	}

	var (
		result     TextResponse
		parts      []string
		doneText   string
		streamErr  error
		sawContent bool
	)
	streamErr = readServerSentEventData(resp.Body, func(data []byte) error {
		var event struct {
			Type     string          `json:"type"`
			Delta    string          `json:"delta"`
			Text     string          `json:"text"`
			Response json.RawMessage `json:"response"`
			Error    *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(data, &event); err != nil {
			return fmt.Errorf("decode openai text stream event: %w", err)
		}
		if event.Error != nil && strings.TrimSpace(event.Error.Message) != "" {
			return errors.New(strings.TrimSpace(event.Error.Message))
		}
		switch strings.TrimSpace(event.Type) {
		case "response.output_text.delta":
			if event.Delta == "" {
				return nil
			}
			parts = append(parts, event.Delta)
			sawContent = true
			return handle(TextStreamEvent{Delta: event.Delta})
		case "response.output_text.done":
			doneText = event.Text
			return nil
		case "response.completed", "response.incomplete", "response.failed":
			if len(event.Response) == 0 {
				return nil
			}
			parsed, err := parseResponsesTextEnvelope(event.Response)
			if err != nil {
				return err
			}
			result = parsed
			return nil
		default:
			return nil
		}
	})
	if streamErr != nil {
		if c.usage != nil {
			c.usage.Fail(modelName)
		}
		return TextResponse{}, streamErr
	}
	if result.OutputText == "" {
		result.OutputText = strings.TrimSpace(strings.Join(parts, ""))
	}
	if result.OutputText == "" && strings.TrimSpace(doneText) != "" {
		result.OutputText = strings.TrimSpace(doneText)
		if !sawContent {
			if err := handle(TextStreamEvent{Delta: doneText}); err != nil {
				if c.usage != nil {
					c.usage.Fail(modelName)
				}
				return TextResponse{}, err
			}
		}
	}
	if result.Model == "" {
		result.Model = modelName
	}
	if result.OutputText == "" {
		if c.usage != nil {
			c.usage.Fail(modelName)
		}
		return result, errors.New("openai text response returned no assistant output")
	}
	if c.usage != nil {
		c.usage.Complete(result.Model, result.Usage)
	}
	return result, nil
}

func (c *OpenAICompatibleChatTextClient) RunTextStream(ctx context.Context, req TextRequest, handle TextStreamHandler) (TextResponse, error) {
	if c == nil || c.apiKey == "" {
		return TextResponse{}, errors.New("openai-compatible chat text client not configured")
	}
	modelName := strings.TrimSpace(req.Model)
	if modelName == "" {
		return TextResponse{}, errors.New("openai-compatible chat text request requires a model")
	}
	if handle == nil {
		handle = func(TextStreamEvent) error { return nil }
	}

	messages := make([]any, 0, len(req.Messages)+1)
	if system := strings.TrimSpace(req.SystemText); system != "" {
		messages = append(messages, map[string]any{"role": "system", "content": system})
	}
	for _, message := range req.Messages {
		content := strings.TrimSpace(message.Content)
		if content == "" {
			continue
		}
		messages = append(messages, map[string]any{
			"role":    normalizeTextMessageRole(message.Role),
			"content": content,
		})
	}
	if len(messages) == 0 {
		return TextResponse{}, errors.New("openai-compatible chat text request requires at least one message")
	}

	reqBody := map[string]any{
		"model":    modelName,
		"messages": messages,
		"stream":   true,
	}
	raw, err := json.Marshal(reqBody)
	if err != nil {
		return TextResponse{}, fmt.Errorf("marshal openai-compatible chat text request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(raw))
	if err != nil {
		return TextResponse{}, fmt.Errorf("create openai-compatible chat text request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")

	if c.usage != nil {
		c.usage.Start(modelName)
	}
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		if c.usage != nil {
			c.usage.Fail(modelName)
		}
		return TextResponse{}, fmt.Errorf("send openai-compatible chat text request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		body, readErr := io.ReadAll(resp.Body)
		if c.usage != nil {
			c.usage.Fail(modelName)
		}
		if readErr != nil {
			return TextResponse{}, fmt.Errorf("read openai-compatible chat text response: %w", readErr)
		}
		return TextResponse{}, &HTTPStatusError{
			StatusCode: resp.StatusCode,
			Status:     resp.Status,
			Body:       string(body),
			RetryAfter: resp.Header.Get("Retry-After"),
		}
	}

	result := TextResponse{Status: "completed"}
	var parts []string
	streamErr := readServerSentEventData(resp.Body, func(data []byte) error {
		var chunk struct {
			Model string `json:"model"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
			Usage *struct {
				PromptTokens        int64 `json:"prompt_tokens"`
				PromptTokensDetails struct {
					CachedTokens int64 `json:"cached_tokens"`
				} `json:"prompt_tokens_details"`
				CompletionTokens        int64 `json:"completion_tokens"`
				CompletionTokensDetails struct {
					ReasoningTokens int64 `json:"reasoning_tokens"`
				} `json:"completion_tokens_details"`
				TotalTokens int64 `json:"total_tokens"`
			} `json:"usage"`
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
		}
		if err := json.Unmarshal(data, &chunk); err != nil {
			return fmt.Errorf("decode openai-compatible chat text stream event: %w", err)
		}
		if chunk.Error != nil && strings.TrimSpace(chunk.Error.Message) != "" {
			return errors.New(strings.TrimSpace(chunk.Error.Message))
		}
		if strings.TrimSpace(chunk.Model) != "" {
			result.Model = strings.TrimSpace(chunk.Model)
		}
		if chunk.Usage != nil {
			result.Usage = model.LLMUsage{
				InputTokens:       chunk.Usage.PromptTokens,
				OutputTokens:      chunk.Usage.CompletionTokens,
				TotalTokens:       chunk.Usage.TotalTokens,
				CachedInputTokens: chunk.Usage.PromptTokensDetails.CachedTokens,
				ReasoningTokens:   chunk.Usage.CompletionTokensDetails.ReasoningTokens,
			}
		}
		for _, choice := range chunk.Choices {
			if reason := strings.TrimSpace(choice.FinishReason); reason != "" {
				result.IncompleteReason = reason
			}
			if choice.Delta.Content == "" {
				continue
			}
			parts = append(parts, choice.Delta.Content)
			if err := handle(TextStreamEvent{Delta: choice.Delta.Content}); err != nil {
				return err
			}
		}
		return nil
	})
	if streamErr != nil {
		if c.usage != nil {
			c.usage.Fail(modelName)
		}
		return TextResponse{}, streamErr
	}
	result.OutputText = strings.TrimSpace(strings.Join(parts, ""))
	if result.Model == "" {
		result.Model = modelName
	}
	if estimatedCostUSD, ok := model.EstimateLLMCostUSD(result.Model, result.Usage); ok {
		result.Usage.EstimatedCostUSD = estimatedCostUSD
	}
	if result.OutputText == "" {
		if c.usage != nil {
			c.usage.Fail(modelName)
		}
		return result, errors.New("openai-compatible chat text response returned no assistant output")
	}
	if c.usage != nil {
		c.usage.Complete(result.Model, result.Usage)
	}
	return result, nil
}

func (r *AutoTextRunner) RunText(ctx context.Context, req TextRequest) (TextResponse, error) {
	if r == nil || r.baseRunner == nil {
		return TextResponse{}, errors.New("auto text runner not configured")
	}
	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = r.defaultModel
	}
	if model == "" && r.discovery != nil {
		discovered, err := r.discovery.FirstModel(ctx)
		if err != nil {
			return TextResponse{}, err
		}
		model = discovered
	}
	if model == "" {
		return TextResponse{}, errors.New("auto text runner could not resolve a model")
	}
	req.Model = model
	response, err := r.baseRunner.RunText(ctx, req)
	if err == nil {
		return response, nil
	}
	var httpErr *HTTPStatusError
	if !errors.As(err, &httpErr) || httpErr.StatusCode != http.StatusNotFound || r.discovery == nil {
		return TextResponse{}, err
	}
	discovered, discoverErr := r.discovery.FirstModel(ctx)
	if discoverErr != nil || discovered == "" || strings.EqualFold(discovered, req.Model) {
		return TextResponse{}, err
	}
	req.Model = discovered
	return r.baseRunner.RunText(ctx, req)
}

func (r *AutoTextRunner) RunTextStream(ctx context.Context, req TextRequest, handle TextStreamHandler) (TextResponse, error) {
	if r == nil || r.baseRunner == nil {
		return TextResponse{}, errors.New("auto text runner not configured")
	}
	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = r.defaultModel
	}
	if model == "" && r.discovery != nil {
		discovered, err := r.discovery.FirstModel(ctx)
		if err != nil {
			return TextResponse{}, err
		}
		model = discovered
	}
	if model == "" {
		return TextResponse{}, errors.New("auto text runner could not resolve a model")
	}
	req.Model = model
	response, err := runTextStreamWithFallback(ctx, r.baseRunner, req, handle)
	if err == nil {
		return response, nil
	}
	var httpErr *HTTPStatusError
	if !errors.As(err, &httpErr) || httpErr.StatusCode != http.StatusNotFound || r.discovery == nil {
		return TextResponse{}, err
	}
	discovered, discoverErr := r.discovery.FirstModel(ctx)
	if discoverErr != nil || discovered == "" || strings.EqualFold(discovered, req.Model) {
		return TextResponse{}, err
	}
	req.Model = discovered
	return runTextStreamWithFallback(ctx, r.baseRunner, req, handle)
}

func runTextStreamWithFallback(ctx context.Context, runner TextRunner, req TextRequest, handle TextStreamHandler) (TextResponse, error) {
	if streamer, ok := runner.(StreamingTextRunner); ok {
		return streamer.RunTextStream(ctx, req, handle)
	}
	resp, err := runner.RunText(ctx, req)
	if err != nil {
		return TextResponse{}, err
	}
	if handle != nil && strings.TrimSpace(resp.OutputText) != "" {
		if err := handle(TextStreamEvent{Delta: resp.OutputText}); err != nil {
			return TextResponse{}, err
		}
	}
	return resp, nil
}

func responseTextMessage(role, text string) map[string]any {
	return map[string]any{
		"role": role,
		"content": []any{
			map[string]any{
				"type": "input_text",
				"text": text,
			},
		},
	}
}

func normalizeTextMessageRole(role string) string {
	switch strings.TrimSpace(strings.ToLower(role)) {
	case "assistant":
		return "assistant"
	case "system":
		return "system"
	default:
		return "user"
	}
}

func parseResponsesTextEnvelope(body []byte) (TextResponse, error) {
	var envelope struct {
		Status            string `json:"status"`
		Model             string `json:"model"`
		IncompleteDetails *struct {
			Reason string `json:"reason"`
		} `json:"incomplete_details"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
		Usage *struct {
			InputTokens        int64 `json:"input_tokens"`
			InputTokensDetails struct {
				CachedTokens int64 `json:"cached_tokens"`
			} `json:"input_tokens_details"`
			OutputTokens        int64 `json:"output_tokens"`
			OutputTokensDetails struct {
				ReasoningTokens int64 `json:"reasoning_tokens"`
			} `json:"output_tokens_details"`
			TotalTokens int64 `json:"total_tokens"`
		} `json:"usage"`
		Output []struct {
			Type    string `json:"type"`
			Role    string `json:"role"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return TextResponse{}, fmt.Errorf("decode openai text response: %w", err)
	}
	if envelope.Error != nil && strings.TrimSpace(envelope.Error.Message) != "" {
		return TextResponse{}, errors.New(strings.TrimSpace(envelope.Error.Message))
	}

	result := TextResponse{
		Status:     strings.TrimSpace(envelope.Status),
		Model:      strings.TrimSpace(envelope.Model),
		OutputText: strings.TrimSpace(responseOutputText(envelope.Output)),
	}
	if envelope.IncompleteDetails != nil {
		result.IncompleteReason = strings.TrimSpace(envelope.IncompleteDetails.Reason)
	}
	if envelope.Usage != nil {
		result.Usage = model.LLMUsage{
			InputTokens:       envelope.Usage.InputTokens,
			OutputTokens:      envelope.Usage.OutputTokens,
			TotalTokens:       envelope.Usage.TotalTokens,
			CachedInputTokens: envelope.Usage.InputTokensDetails.CachedTokens,
			ReasoningTokens:   envelope.Usage.OutputTokensDetails.ReasoningTokens,
		}
		if estimatedCostUSD, ok := model.EstimateLLMCostUSD(result.Model, result.Usage); ok {
			result.Usage.EstimatedCostUSD = estimatedCostUSD
		}
	}
	return result, nil
}

func decodeResponsesTextEnvelope(body []byte) (TextResponse, error) {
	result, err := parseResponsesTextEnvelope(body)
	if err != nil {
		return TextResponse{}, err
	}
	if result.OutputText == "" {
		return result, errors.New("openai text response returned no assistant output")
	}
	return result, nil
}

func decodeChatTextEnvelope(body []byte) (TextResponse, error) {
	var envelope struct {
		Model string `json:"model"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
		Usage *struct {
			PromptTokens        int64 `json:"prompt_tokens"`
			PromptTokensDetails struct {
				CachedTokens int64 `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
			CompletionTokens        int64 `json:"completion_tokens"`
			CompletionTokensDetails struct {
				ReasoningTokens int64 `json:"reasoning_tokens"`
			} `json:"completion_tokens_details"`
			TotalTokens int64 `json:"total_tokens"`
		} `json:"usage"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return TextResponse{}, fmt.Errorf("decode openai-compatible chat text response: %w", err)
	}
	if envelope.Error != nil && strings.TrimSpace(envelope.Error.Message) != "" {
		return TextResponse{}, errors.New(strings.TrimSpace(envelope.Error.Message))
	}

	result := TextResponse{
		Status: "completed",
		Model:  strings.TrimSpace(envelope.Model),
	}
	if len(envelope.Choices) > 0 {
		result.OutputText = strings.TrimSpace(envelope.Choices[0].Message.Content)
		result.IncompleteReason = strings.TrimSpace(envelope.Choices[0].FinishReason)
	}
	if envelope.Usage != nil {
		result.Usage = model.LLMUsage{
			InputTokens:       envelope.Usage.PromptTokens,
			OutputTokens:      envelope.Usage.CompletionTokens,
			TotalTokens:       envelope.Usage.TotalTokens,
			CachedInputTokens: envelope.Usage.PromptTokensDetails.CachedTokens,
			ReasoningTokens:   envelope.Usage.CompletionTokensDetails.ReasoningTokens,
		}
		if estimatedCostUSD, ok := model.EstimateLLMCostUSD(result.Model, result.Usage); ok {
			result.Usage.EstimatedCostUSD = estimatedCostUSD
		}
	}
	if result.OutputText == "" {
		return result, errors.New("openai-compatible chat text response returned no assistant output")
	}
	return result, nil
}

func responseOutputText(output []struct {
	Type    string `json:"type"`
	Role    string `json:"role"`
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}) string {
	var parts []string
	for _, item := range output {
		if strings.TrimSpace(item.Type) != "message" && strings.TrimSpace(item.Type) != "" {
			continue
		}
		for _, content := range item.Content {
			if strings.TrimSpace(content.Type) != "output_text" && strings.TrimSpace(content.Type) != "text" {
				continue
			}
			if text := strings.TrimSpace(content.Text); text != "" {
				parts = append(parts, text)
			}
		}
	}
	return strings.Join(parts, "\n\n")
}
