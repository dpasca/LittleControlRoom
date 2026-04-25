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

func decodeResponsesTextEnvelope(body []byte) (TextResponse, error) {
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
	if result.OutputText == "" {
		return result, errors.New("openai text response returned no assistant output")
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
