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

type ResponsesClient struct {
	apiKey     string
	endpoint   string
	httpClient *http.Client
	usage      *UsageTracker
}

type JSONSchemaRequest struct {
	Model           string
	SystemText      string
	UserText        string
	SchemaName      string
	Schema          map[string]any
	ReasoningEffort string
}

type JSONSchemaResponse struct {
	Status           string
	Model            string
	MaxOutputTokens  *int64
	IncompleteReason string
	OutputText       string
	Usage            model.LLMUsage
}

type HTTPStatusError struct {
	StatusCode int
	Status     string
	Body       string
	RetryAfter string
}

func (e *HTTPStatusError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("openai responses api %s: %s", e.Status, strings.TrimSpace(e.Body))
}

func NewResponsesClient(apiKey string, timeout time.Duration, usage *UsageTracker) *ResponsesClient {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return nil
	}
	if timeout <= 0 {
		timeout = 45 * time.Second
	}

	baseURL := strings.TrimSpace(os.Getenv("OPENAI_BASE_URL"))
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	return NewResponsesClientWithBaseURLAndHTTPClient(apiKey, baseURL, &http.Client{Timeout: timeout}, usage)
}

func ResponsesEndpointFromBaseURL(baseURL string) string {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	baseURL = strings.TrimRight(baseURL, "/")
	if strings.HasSuffix(baseURL, "/responses") {
		return baseURL
	}
	return baseURL + "/responses"
}

func NewResponsesClientWithBaseURL(apiKey, baseURL string, timeout time.Duration, usage *UsageTracker) *ResponsesClient {
	if timeout <= 0 {
		timeout = 45 * time.Second
	}
	return NewResponsesClientWithBaseURLAndHTTPClient(apiKey, baseURL, &http.Client{Timeout: timeout}, usage)
}

func NewResponsesClientWithBaseURLAndHTTPClient(apiKey, baseURL string, httpClient *http.Client, usage *UsageTracker) *ResponsesClient {
	return NewResponsesClientWithHTTPClient(apiKey, ResponsesEndpointFromBaseURL(baseURL), httpClient, usage)
}

func NewResponsesClientWithHTTPClient(apiKey, endpoint string, httpClient *http.Client, usage *UsageTracker) *ResponsesClient {
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
		httpClientToUse = &http.Client{Timeout: 45 * time.Second}
	}
	return &ResponsesClient{
		apiKey:     apiKey,
		endpoint:   endpoint,
		httpClient: httpClientToUse,
		usage:      usage,
	}
}

func (c *ResponsesClient) RunJSONSchema(ctx context.Context, req JSONSchemaRequest) (JSONSchemaResponse, error) {
	if c == nil || c.apiKey == "" {
		return JSONSchemaResponse{}, errors.New("openai responses client not configured")
	}

	reqBody := map[string]any{
		"model": req.Model,
		"input": []any{
			map[string]any{
				"role": "system",
				"content": []any{
					map[string]any{
						"type": "input_text",
						"text": req.SystemText,
					},
				},
			},
			map[string]any{
				"role": "user",
				"content": []any{
					map[string]any{
						"type": "input_text",
						"text": req.UserText,
					},
				},
			},
		},
		"store": false,
		"text": map[string]any{
			"format": map[string]any{
				"type":   "json_schema",
				"name":   req.SchemaName,
				"strict": true,
				"schema": req.Schema,
			},
		},
	}
	if effort := strings.TrimSpace(req.ReasoningEffort); effort != "" {
		reqBody["reasoning"] = map[string]any{
			"effort": effort,
		}
	}

	raw, err := json.Marshal(reqBody)
	if err != nil {
		return JSONSchemaResponse{}, fmt.Errorf("marshal openai request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(raw))
	if err != nil {
		return JSONSchemaResponse{}, fmt.Errorf("create openai request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")

	if c.usage != nil {
		c.usage.Start(req.Model)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		if c.usage != nil {
			c.usage.Fail(req.Model)
		}
		return JSONSchemaResponse{}, fmt.Errorf("send openai request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		if c.usage != nil {
			c.usage.Fail(req.Model)
		}
		return JSONSchemaResponse{}, fmt.Errorf("read openai response: %w", err)
	}
	if resp.StatusCode >= 300 {
		if c.usage != nil {
			c.usage.Fail(req.Model)
		}
		return JSONSchemaResponse{}, &HTTPStatusError{
			StatusCode: resp.StatusCode,
			Status:     resp.Status,
			Body:       string(body),
			RetryAfter: resp.Header.Get("Retry-After"),
		}
	}

	var envelope struct {
		Status            string `json:"status"`
		Model             string `json:"model"`
		MaxOutputTokens   *int64 `json:"max_output_tokens"`
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
		if c.usage != nil {
			c.usage.Fail(req.Model)
		}
		return JSONSchemaResponse{}, fmt.Errorf("decode openai response: %w", err)
	}
	if envelope.Error != nil && envelope.Error.Message != "" {
		if c.usage != nil {
			c.usage.Fail(req.Model)
		}
		return JSONSchemaResponse{}, errors.New(envelope.Error.Message)
	}

	result := JSONSchemaResponse{
		Status:          strings.TrimSpace(envelope.Status),
		Model:           strings.TrimSpace(envelope.Model),
		MaxOutputTokens: envelope.MaxOutputTokens,
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
	result.OutputText = responseAssistantOutputText(envelope.Output)

	if c.usage != nil {
		modelName := result.Model
		if modelName == "" {
			modelName = req.Model
		}
		c.usage.Complete(modelName, result.Usage)
	}

	return result, nil
}

func responseAssistantOutputText(output []struct {
	Type    string `json:"type"`
	Role    string `json:"role"`
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}) string {
	for _, item := range output {
		if item.Type != "message" || item.Role != "assistant" {
			continue
		}
		for _, content := range item.Content {
			if content.Type == "output_text" && strings.TrimSpace(content.Text) != "" {
				return content.Text
			}
		}
	}
	return ""
}
