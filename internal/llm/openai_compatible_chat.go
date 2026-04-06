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
	"sync"
	"syscall"
	"time"

	"lcroom/internal/model"
)

type OpenAICompatibleChatCompletionsClient struct {
	apiKey     string
	endpoint   string
	httpClient *http.Client
	usage      *UsageTracker
}

type OpenAICompatibleStructuredOutputRunner struct {
	mu         sync.Mutex
	preferChat bool
	responses  JSONSchemaRunner
	chat       JSONSchemaRunner
}

type OpenAICompatibleStructuredOutputOptions struct {
	PreferChatCompletions bool
}

func ChatCompletionsEndpointFromBaseURL(baseURL string) string {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	baseURL = strings.TrimRight(baseURL, "/")
	if strings.HasSuffix(baseURL, "/chat/completions") {
		return baseURL
	}
	return baseURL + "/chat/completions"
}

func NewOpenAICompatibleChatCompletionsClientWithBaseURL(apiKey, baseURL string, timeout time.Duration, usage *UsageTracker) *OpenAICompatibleChatCompletionsClient {
	if timeout <= 0 {
		timeout = 45 * time.Second
	}
	return NewOpenAICompatibleChatCompletionsClientWithHTTPClient(
		apiKey,
		ChatCompletionsEndpointFromBaseURL(baseURL),
		&http.Client{Timeout: timeout},
		usage,
	)
}

func NewOpenAICompatibleChatCompletionsClientWithHTTPClient(apiKey, endpoint string, httpClient *http.Client, usage *UsageTracker) *OpenAICompatibleChatCompletionsClient {
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
		httpClientToUse = &http.Client{Timeout: 45 * time.Second}
	}
	return &OpenAICompatibleChatCompletionsClient{
		apiKey:     apiKey,
		endpoint:   endpoint,
		httpClient: httpClientToUse,
		usage:      usage,
	}
}

func (c *OpenAICompatibleChatCompletionsClient) RunJSONSchema(ctx context.Context, req JSONSchemaRequest) (JSONSchemaResponse, error) {
	if c == nil || c.apiKey == "" {
		return JSONSchemaResponse{}, errors.New("openai-compatible chat completions client not configured")
	}

	reqBody := map[string]any{
		"model": req.Model,
		"messages": []any{
			map[string]any{
				"role":    "system",
				"content": req.SystemText,
			},
			map[string]any{
				"role":    "user",
				"content": req.UserText,
			},
		},
		"response_format": map[string]any{
			"type": "json_schema",
			"json_schema": map[string]any{
				"name":   req.SchemaName,
				"strict": true,
				"schema": req.Schema,
			},
		},
	}

	raw, err := json.Marshal(reqBody)
	if err != nil {
		return JSONSchemaResponse{}, fmt.Errorf("marshal openai-compatible chat completion request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(raw))
	if err != nil {
		return JSONSchemaResponse{}, fmt.Errorf("create openai-compatible chat completion request: %w", err)
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
		return JSONSchemaResponse{}, fmt.Errorf("send openai-compatible chat completion request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		if c.usage != nil {
			c.usage.Fail(req.Model)
		}
		return JSONSchemaResponse{}, fmt.Errorf("read openai-compatible chat completion response: %w", err)
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
		Model string `json:"model"`
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
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		if c.usage != nil {
			c.usage.Fail(req.Model)
		}
		return JSONSchemaResponse{}, fmt.Errorf("decode openai-compatible chat completion response: %w", err)
	}

	result := JSONSchemaResponse{
		Status:     "completed",
		Model:      strings.TrimSpace(envelope.Model),
		OutputText: "",
	}
	if len(envelope.Choices) > 0 {
		result.OutputText = strings.TrimSpace(envelope.Choices[0].Message.Content)
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

	if c.usage != nil {
		modelName := result.Model
		if modelName == "" {
			modelName = req.Model
		}
		c.usage.Complete(modelName, result.Usage)
	}

	return result, nil
}

func NewOpenAICompatibleStructuredOutputRunner(responses, chat JSONSchemaRunner) JSONSchemaRunner {
	return NewOpenAICompatibleStructuredOutputRunnerWithOptions(responses, chat, OpenAICompatibleStructuredOutputOptions{})
}

func NewOpenAICompatibleStructuredOutputRunnerWithOptions(responses, chat JSONSchemaRunner, opts OpenAICompatibleStructuredOutputOptions) JSONSchemaRunner {
	if responses == nil && chat == nil {
		return nil
	}
	return &OpenAICompatibleStructuredOutputRunner{
		preferChat: opts.PreferChatCompletions,
		responses:  responses,
		chat:       chat,
	}
}

func (r *OpenAICompatibleStructuredOutputRunner) RunJSONSchema(ctx context.Context, req JSONSchemaRequest) (JSONSchemaResponse, error) {
	if r == nil {
		return JSONSchemaResponse{}, errors.New("openai-compatible structured output runner not configured")
	}

	r.mu.Lock()
	preferChat := r.preferChat
	r.mu.Unlock()
	chatMissingEndpoint := false

	if preferChat && r.chat != nil {
		resp, err := r.chat.RunJSONSchema(ctx, req)
		if err == nil {
			return resp, nil
		}
		if !isMissingEndpointHTTPStatus(err) || r.responses == nil {
			return JSONSchemaResponse{}, err
		}
		chatMissingEndpoint = true
	}

	if r.responses != nil {
		resp, err := r.responses.RunJSONSchema(ctx, req)
		if err == nil {
			if chatMissingEndpoint {
				r.mu.Lock()
				r.preferChat = false
				r.mu.Unlock()
			}
			return resp, nil
		}
		if shouldFallbackResponsesToChat(err) && r.chat != nil {
			chatResp, chatErr := r.chat.RunJSONSchema(ctx, req)
			if chatErr != nil {
				return JSONSchemaResponse{}, errors.Join(err, chatErr)
			}
			r.mu.Lock()
			r.preferChat = true
			r.mu.Unlock()
			return chatResp, nil
		}
		return JSONSchemaResponse{}, err
	}

	if r.chat != nil {
		return r.chat.RunJSONSchema(ctx, req)
	}
	return JSONSchemaResponse{}, errors.New("openai-compatible structured output runner not configured")
}

func isMissingEndpointHTTPStatus(err error) bool {
	var httpErr *HTTPStatusError
	if !errors.As(err, &httpErr) {
		return false
	}
	return httpErr.StatusCode == http.StatusNotFound || httpErr.StatusCode == http.StatusMethodNotAllowed
}

func shouldFallbackResponsesToChat(err error) bool {
	return isMissingEndpointHTTPStatus(err) || isUnsupportedResponsesTransportError(err)
}

func isUnsupportedResponsesTransportError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	return errors.Is(err, io.EOF) ||
		errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, syscall.ECONNABORTED) ||
		errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.EPIPE)
}
