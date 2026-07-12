package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

const defaultOpenAICompatibleCacheDuration = 30 * time.Second

type OpenAICompatibleResponsesRunnerOptions struct {
	PreferChatCompletions bool
	ChatResponseFormat    OpenAICompatibleChatResponseFormat
	AuthHeader            OpenAICompatibleAuthHeader
	ReasoningStyle        string
	RequireParameters     bool
}

type OpenAICompatibleModelDiscovery struct {
	baseURL       string
	apiKey        string
	authHeader    OpenAICompatibleAuthHeader
	httpClient    *http.Client
	cacheDuration time.Duration

	mu         sync.Mutex
	models     []string
	lastError  error
	discovered time.Time
}

type AutoModelRunner struct {
	discovery    *OpenAICompatibleModelDiscovery
	baseRunner   JSONSchemaRunner
	defaultModel string
}

func NewOpenAICompatibleModelDiscovery(baseURL, apiKey string, timeout time.Duration) *OpenAICompatibleModelDiscovery {
	return NewOpenAICompatibleModelDiscoveryWithAuthHeader(baseURL, apiKey, timeout, OpenAICompatibleAuthHeaderBearer)
}

func NewOpenAICompatibleModelDiscoveryWithAuthHeader(baseURL, apiKey string, timeout time.Duration, authHeader OpenAICompatibleAuthHeader) *OpenAICompatibleModelDiscovery {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	return &OpenAICompatibleModelDiscovery{
		baseURL:       strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		apiKey:        strings.TrimSpace(apiKey),
		authHeader:    normalizeOpenAICompatibleAuthHeader(authHeader),
		httpClient:    &http.Client{Timeout: timeout},
		cacheDuration: defaultOpenAICompatibleCacheDuration,
	}
}

func (d *OpenAICompatibleModelDiscovery) Models() []string {
	if d == nil {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return slicesClone(d.models)
}

func (d *OpenAICompatibleModelDiscovery) LastError() error {
	if d == nil {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.lastError
}

func (d *OpenAICompatibleModelDiscovery) FirstModel(ctx context.Context) (string, error) {
	if d == nil {
		return "", errors.New("openai-compatible model discovery not configured")
	}
	if err := d.Discover(ctx); err != nil {
		return "", err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.models) == 0 {
		return "", errors.New("openai-compatible endpoint returned no models")
	}
	return d.models[0], nil
}

func (d *OpenAICompatibleModelDiscovery) Discover(ctx context.Context) error {
	if d == nil {
		return errors.New("openai-compatible model discovery not configured")
	}

	d.mu.Lock()
	if d.baseURL == "" {
		d.lastError = errors.New("openai-compatible base URL is required")
		d.mu.Unlock()
		return d.lastError
	}
	if len(d.models) > 0 && time.Since(d.discovered) < d.cacheDuration {
		d.mu.Unlock()
		return nil
	}
	d.mu.Unlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.baseURL+"/models", nil)
	if err != nil {
		return fmt.Errorf("create model list request: %w", err)
	}
	if d.apiKey != "" {
		setOpenAICompatibleAPIKeyHeader(req.Header, d.apiKey, d.authHeader)
	}

	resp, err := d.httpClient.Do(req)
	if err != nil {
		d.storeDiscoverResult(nil, fmt.Errorf("request model list: %w", err))
		return d.LastError()
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		d.storeDiscoverResult(nil, fmt.Errorf("model list request failed: %s", resp.Status))
		return d.LastError()
	}

	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		d.storeDiscoverResult(nil, fmt.Errorf("decode model list: %w", err))
		return d.LastError()
	}

	models := make([]string, 0, len(payload.Data))
	seen := map[string]struct{}{}
	for _, item := range payload.Data {
		model := strings.TrimSpace(item.ID)
		if model == "" {
			continue
		}
		if _, ok := seen[model]; ok {
			continue
		}
		seen[model] = struct{}{}
		models = append(models, model)
	}
	if len(models) == 0 {
		d.storeDiscoverResult(nil, errors.New("openai-compatible endpoint returned no models"))
		return d.LastError()
	}

	d.storeDiscoverResult(models, nil)
	return nil
}

func (d *OpenAICompatibleModelDiscovery) storeDiscoverResult(models []string, err error) {
	if d == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.models = slicesClone(models)
	d.lastError = err
	d.discovered = time.Now()
}

func NewAutoModelRunner(discovery *OpenAICompatibleModelDiscovery, baseRunner JSONSchemaRunner, defaultModel string) *AutoModelRunner {
	return &AutoModelRunner{
		discovery:    discovery,
		baseRunner:   baseRunner,
		defaultModel: strings.TrimSpace(defaultModel),
	}
}

func (r *AutoModelRunner) RunJSONSchema(ctx context.Context, req JSONSchemaRequest) (JSONSchemaResponse, error) {
	if r == nil || r.baseRunner == nil {
		return JSONSchemaResponse{}, errors.New("auto model runner not configured")
	}
	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = r.defaultModel
	}
	if model == "" && r.discovery != nil {
		discovered, err := r.discovery.FirstModel(ctx)
		if err != nil {
			return JSONSchemaResponse{}, err
		}
		model = discovered
	}
	if model == "" {
		return JSONSchemaResponse{}, errors.New("auto model runner could not resolve a model")
	}
	req.Model = model
	response, err := r.baseRunner.RunJSONSchema(ctx, req)
	if err == nil {
		return response, nil
	}
	var httpErr *HTTPStatusError
	if !errors.As(err, &httpErr) || httpErr.StatusCode != http.StatusNotFound || r.discovery == nil {
		return JSONSchemaResponse{}, err
	}
	discovered, discoverErr := r.discovery.FirstModel(ctx)
	if discoverErr != nil || discovered == "" || strings.EqualFold(discovered, req.Model) {
		return JSONSchemaResponse{}, err
	}
	req.Model = discovered
	return r.baseRunner.RunJSONSchema(ctx, req)
}

func NewOpenAICompatibleResponsesRunner(baseURL, apiKey, defaultModel string, timeout time.Duration, usage *UsageTracker) JSONSchemaRunner {
	return NewOpenAICompatibleResponsesRunnerWithOptions(baseURL, apiKey, defaultModel, timeout, usage, OpenAICompatibleResponsesRunnerOptions{})
}

func NewOpenAICompatibleResponsesRunnerWithOptions(baseURL, apiKey, defaultModel string, timeout time.Duration, usage *UsageTracker, opts OpenAICompatibleResponsesRunnerOptions) JSONSchemaRunner {
	authHeader := normalizeOpenAICompatibleAuthHeader(opts.AuthHeader)
	responsesClient := NewResponsesClientWithBaseURLAndAuthHeader(apiKey, baseURL, timeout, usage, authHeader)
	if responsesClient != nil {
		responsesClient.requireParameters = opts.RequireParameters
	}
	chatFormat := normalizeOpenAICompatibleChatResponseFormat(opts.ChatResponseFormat)
	chatClient := NewOpenAICompatibleChatCompletionsClientWithBaseURLAndOptions(apiKey, baseURL, timeout, usage, chatFormat, authHeader, opts.ReasoningStyle)
	if chatClient != nil {
		chatClient.requireParameters = opts.RequireParameters
	}

	var schemaChatClient JSONSchemaRunner
	var jsonModeChatClient JSONSchemaRunner
	var promptOnlyChatClient JSONSchemaRunner
	if chatClient != nil {
		switch chatFormat {
		case OpenAICompatibleChatResponseFormatJSONObject:
			jsonModeChatClient = chatClient
		case OpenAICompatibleChatResponseFormatPromptOnly:
			promptOnlyChatClient = chatClient
		default:
			schemaChatClient = chatClient
		}
	}
	if jsonModeChatClient == nil && chatFormat != OpenAICompatibleChatResponseFormatPromptOnly {
		client := NewOpenAICompatibleChatCompletionsClientWithBaseURLAndOptions(apiKey, baseURL, timeout, usage, OpenAICompatibleChatResponseFormatJSONObject, authHeader, opts.ReasoningStyle)
		if client != nil {
			client.requireParameters = opts.RequireParameters
			jsonModeChatClient = client
		}
	}
	if promptOnlyChatClient == nil {
		client := NewOpenAICompatibleChatCompletionsClientWithBaseURLAndOptions(apiKey, baseURL, timeout, usage, OpenAICompatibleChatResponseFormatPromptOnly, authHeader, opts.ReasoningStyle)
		if client != nil {
			client.requireParameters = opts.RequireParameters
			promptOnlyChatClient = client
		}
	}
	baseRunner := NewOpenAICompatibleStructuredOutputRunnerWithFallbacks(responsesClient, schemaChatClient, jsonModeChatClient, promptOnlyChatClient, OpenAICompatibleStructuredOutputOptions{
		PreferChatCompletions: opts.PreferChatCompletions,
	})
	if baseRunner == nil {
		return nil
	}
	discovery := NewOpenAICompatibleModelDiscoveryWithAuthHeader(baseURL, apiKey, timeout, authHeader)
	return NewAutoModelRunner(discovery, baseRunner, defaultModel)
}
