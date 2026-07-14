package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"syscall"
	"testing"
	"time"
)

type recordingJSONSchemaRunner struct {
	lastRequest JSONSchemaRequest
	response    JSONSchemaResponse
	errs        []error
	requests    []JSONSchemaRequest
}

func (r *recordingJSONSchemaRunner) RunJSONSchema(_ context.Context, req JSONSchemaRequest) (JSONSchemaResponse, error) {
	r.lastRequest = req
	r.requests = append(r.requests, req)
	if len(r.errs) > 0 {
		err := r.errs[0]
		r.errs = r.errs[1:]
		if err != nil {
			return JSONSchemaResponse{}, err
		}
	}
	return r.response, nil
}

func TestOpenAICompatibleModelDiscoveryFirstModel(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("path = %q, want /v1/models", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"qwen-local"},{"id":"qwen-small"}]}`))
	}))
	defer server.Close()

	discovery := NewOpenAICompatibleModelDiscovery(server.URL+"/v1", "local-key", time.Second)
	got, err := discovery.FirstModel(context.Background())
	if err != nil {
		t.Fatalf("FirstModel() error = %v", err)
	}
	if got != "qwen-local" {
		t.Fatalf("FirstModel() = %q, want qwen-local", got)
	}
}

func TestAutoModelRunnerUsesDiscoveredModelWhenRequestModelMissing(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"qwen-local"}]}`))
	}))
	defer server.Close()

	baseRunner := &recordingJSONSchemaRunner{
		response: JSONSchemaResponse{Model: "qwen-local", OutputText: `{"ok":true}`},
	}
	runner := NewAutoModelRunner(
		NewOpenAICompatibleModelDiscovery(server.URL+"/v1", "local-key", time.Second),
		baseRunner,
		"",
	)

	_, err := runner.RunJSONSchema(context.Background(), JSONSchemaRequest{
		SystemText: "system",
		UserText:   "user",
	})
	if err != nil {
		t.Fatalf("RunJSONSchema() error = %v", err)
	}
	if baseRunner.lastRequest.Model != "qwen-local" {
		t.Fatalf("discovered model = %q, want qwen-local", baseRunner.lastRequest.Model)
	}
}

func TestAutoModelRunnerRetriesWithDiscoveredModelAfterNotFound(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"qwen-local"}]}`))
	}))
	defer server.Close()

	baseRunner := &recordingJSONSchemaRunner{
		errs: []error{
			&HTTPStatusError{StatusCode: http.StatusNotFound, Status: "404 Not Found", Body: "Not Found"},
			nil,
		},
		response: JSONSchemaResponse{Model: "qwen-local", OutputText: `{"ok":true}`},
	}
	runner := NewAutoModelRunner(
		NewOpenAICompatibleModelDiscovery(server.URL+"/v1", "local-key", time.Second),
		baseRunner,
		"gpt-5.4-mini",
	)

	response, err := runner.RunJSONSchema(context.Background(), JSONSchemaRequest{
		SystemText: "system",
		UserText:   "user",
	})
	if err != nil {
		t.Fatalf("RunJSONSchema() error = %v", err)
	}
	if response.Model != "qwen-local" {
		t.Fatalf("response model = %q, want qwen-local", response.Model)
	}
	if len(baseRunner.requests) != 2 {
		t.Fatalf("request count = %d, want 2", len(baseRunner.requests))
	}
	if baseRunner.requests[0].Model != "gpt-5.4-mini" {
		t.Fatalf("first request model = %q, want gpt-5.4-mini", baseRunner.requests[0].Model)
	}
	if baseRunner.requests[1].Model != "qwen-local" {
		t.Fatalf("retry model = %q, want qwen-local", baseRunner.requests[1].Model)
	}
}

func TestResponsesEndpointFromBaseURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		baseURL string
		want    string
	}{
		{name: "base path", baseURL: "http://127.0.0.1:11434/v1", want: "http://127.0.0.1:11434/v1/responses"},
		{name: "already responses", baseURL: "http://127.0.0.1:11434/v1/responses", want: "http://127.0.0.1:11434/v1/responses"},
	}
	for _, tt := range tests {
		if got := ResponsesEndpointFromBaseURL(tt.baseURL); got != tt.want {
			t.Fatalf("%s: ResponsesEndpointFromBaseURL() = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestChatCompletionsEndpointFromBaseURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		baseURL string
		want    string
	}{
		{name: "base path", baseURL: "http://127.0.0.1:8080/v1", want: "http://127.0.0.1:8080/v1/chat/completions"},
		{name: "already chat completions", baseURL: "http://127.0.0.1:8080/v1/chat/completions", want: "http://127.0.0.1:8080/v1/chat/completions"},
	}
	for _, tt := range tests {
		if got := ChatCompletionsEndpointFromBaseURL(tt.baseURL); got != tt.want {
			t.Fatalf("%s: ChatCompletionsEndpointFromBaseURL() = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestShouldFallbackResponsesToChat(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "missing endpoint 404",
			err:  &HTTPStatusError{StatusCode: http.StatusNotFound, Status: "404 Not Found"},
			want: true,
		},
		{
			name: "missing endpoint 405",
			err:  &HTTPStatusError{StatusCode: http.StatusMethodNotAllowed, Status: "405 Method Not Allowed"},
			want: true,
		},
		{
			name: "connection reset while reading",
			err:  errors.New("read openai response: " + syscall.ECONNRESET.Error()),
			want: false,
		},
		{
			name: "wrapped connection reset",
			err:  wrapErr("read openai response", syscall.ECONNRESET),
			want: true,
		},
		{
			name: "wrapped broken pipe",
			err:  wrapErr("send openai request", syscall.EPIPE),
			want: true,
		},
		{
			name: "unexpected eof",
			err:  wrapErr("read openai response", io.ErrUnexpectedEOF),
			want: true,
		},
		{
			name: "context canceled",
			err:  context.Canceled,
			want: false,
		},
		{
			name: "deadline exceeded",
			err:  context.DeadlineExceeded,
			want: false,
		},
		{
			name: "server unavailable",
			err:  &HTTPStatusError{StatusCode: http.StatusServiceUnavailable, Status: "503 Service Unavailable"},
			want: false,
		},
		{
			name: "connection refused",
			err:  wrapErr("send openai request", syscall.ECONNREFUSED),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldFallbackResponsesToChat(tt.err); got != tt.want {
				t.Fatalf("shouldFallbackResponsesToChat(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestOpenAICompatibleResponsesRunnerFallsBackToChatCompletions(t *testing.T) {
	t.Parallel()

	var responsesCalls int
	var chatCalls int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/responses":
			responsesCalls++
			http.NotFound(w, r)
		case "/v1/chat/completions":
			chatCalls++
			if got := r.Header.Get("Authorization"); got != "Bearer local-key" {
				t.Fatalf("authorization = %q, want bearer token", got)
			}
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("read request body: %v", err)
			}
			var req struct {
				Model          string `json:"model"`
				ResponseFormat struct {
					Type       string `json:"type"`
					JSONSchema struct {
						Name   string `json:"name"`
						Strict bool   `json:"strict"`
					} `json:"json_schema"`
				} `json:"response_format"`
			}
			if err := json.Unmarshal(body, &req); err != nil {
				t.Fatalf("decode request body: %v", err)
			}
			if req.Model != "qwen-local" {
				t.Fatalf("model = %q, want qwen-local", req.Model)
			}
			if req.ResponseFormat.Type != "json_schema" {
				t.Fatalf("response_format.type = %q, want json_schema", req.ResponseFormat.Type)
			}
			if req.ResponseFormat.JSONSchema.Name != "commit_message" {
				t.Fatalf("response_format.json_schema.name = %q, want commit_message", req.ResponseFormat.JSONSchema.Name)
			}
			if !req.ResponseFormat.JSONSchema.Strict {
				t.Fatalf("response_format.json_schema.strict = false, want true")
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"model":"qwen-local",
				"choices":[
					{
						"message":{
							"role":"assistant",
							"content":"{\"message\":\"Use chat fallback\"}"
						}
					}
				],
				"usage":{
					"prompt_tokens":12,
					"completion_tokens":7,
					"total_tokens":19
				}
			}`))
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"id":"qwen-local"}]}`))
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer server.Close()

	runner := NewOpenAICompatibleResponsesRunner(server.URL+"/v1", "local-key", "qwen-local", time.Second, nil)
	if runner == nil {
		t.Fatalf("expected runner")
	}

	req := JSONSchemaRequest{
		Model:      "qwen-local",
		SystemText: "system",
		UserText:   "user",
		SchemaName: "commit_message",
		Schema: map[string]any{
			"type": "object",
		},
	}

	first, err := runner.RunJSONSchema(context.Background(), req)
	if err != nil {
		t.Fatalf("first RunJSONSchema() error = %v", err)
	}
	if first.OutputText != "{\"message\":\"Use chat fallback\"}" {
		t.Fatalf("first OutputText = %q, want chat fallback payload", first.OutputText)
	}

	second, err := runner.RunJSONSchema(context.Background(), req)
	if err != nil {
		t.Fatalf("second RunJSONSchema() error = %v", err)
	}
	if second.OutputText != "{\"message\":\"Use chat fallback\"}" {
		t.Fatalf("second OutputText = %q, want chat fallback payload", second.OutputText)
	}
	if responsesCalls != 1 {
		t.Fatalf("responses endpoint calls = %d, want 1 after caching chat fallback", responsesCalls)
	}
	if chatCalls != 2 {
		t.Fatalf("chat completions endpoint calls = %d, want 2", chatCalls)
	}
}

func TestOpenAICompatibleResponsesRunnerFallsBackToChatAfterResponsesTransportReset(t *testing.T) {
	t.Parallel()

	var responsesCalls int
	var chatCalls int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/responses":
			responsesCalls++
			hijacker, ok := w.(http.Hijacker)
			if !ok {
				t.Fatalf("response writer does not support hijacking")
			}
			conn, rw, err := hijacker.Hijack()
			if err != nil {
				t.Fatalf("hijack responses connection: %v", err)
			}
			if _, err := rw.WriteString("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 512\r\n\r\n{\"status\":\"completed\",\"output\":["); err != nil {
				t.Fatalf("write partial responses payload: %v", err)
			}
			if err := rw.Flush(); err != nil {
				t.Fatalf("flush partial responses payload: %v", err)
			}
			if tcpConn, ok := conn.(*net.TCPConn); ok {
				_ = tcpConn.SetLinger(0)
			}
			_ = conn.Close()
		case "/v1/chat/completions":
			chatCalls++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"model":"qwen-local",
				"choices":[
					{
						"message":{
							"role":"assistant",
							"content":"{\"message\":\"Use chat fallback after transport reset\"}"
						}
					}
				]
			}`))
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"id":"qwen-local"}]}`))
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer server.Close()

	runner := NewOpenAICompatibleResponsesRunner(server.URL+"/v1", "local-key", "qwen-local", time.Second, nil)
	if runner == nil {
		t.Fatalf("expected runner")
	}

	req := JSONSchemaRequest{
		Model:      "qwen-local",
		SystemText: "system",
		UserText:   "user",
		SchemaName: "commit_message",
		Schema: map[string]any{
			"type": "object",
		},
	}

	first, err := runner.RunJSONSchema(context.Background(), req)
	if err != nil {
		t.Fatalf("first RunJSONSchema() error = %v", err)
	}
	if first.OutputText != "{\"message\":\"Use chat fallback after transport reset\"}" {
		t.Fatalf("first OutputText = %q, want chat fallback payload", first.OutputText)
	}

	second, err := runner.RunJSONSchema(context.Background(), req)
	if err != nil {
		t.Fatalf("second RunJSONSchema() error = %v", err)
	}
	if second.OutputText != "{\"message\":\"Use chat fallback after transport reset\"}" {
		t.Fatalf("second OutputText = %q, want chat fallback payload", second.OutputText)
	}
	if responsesCalls != 1 {
		t.Fatalf("responses endpoint calls = %d, want 1 after caching chat fallback", responsesCalls)
	}
	if chatCalls != 2 {
		t.Fatalf("chat completions endpoint calls = %d, want 2", chatCalls)
	}
}

func TestOpenAICompatibleResponsesRunnerCanPreferChatFromStart(t *testing.T) {
	t.Parallel()

	var responsesCalls int
	var chatCalls int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/responses":
			responsesCalls++
			t.Fatalf("responses endpoint should not be called when chat preference succeeds")
		case "/v1/chat/completions":
			chatCalls++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"model":"qwen-local",
				"choices":[
					{
						"message":{
							"role":"assistant",
							"content":"{\"message\":\"Use preferred chat path\"}"
						}
					}
				]
			}`))
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"id":"qwen-local"}]}`))
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer server.Close()

	runner := NewOpenAICompatibleResponsesRunnerWithOptions(server.URL+"/v1", "local-key", "qwen-local", time.Second, nil, OpenAICompatibleResponsesRunnerOptions{
		PreferChatCompletions: true,
	})
	if runner == nil {
		t.Fatalf("expected runner")
	}

	req := JSONSchemaRequest{
		Model:      "qwen-local",
		SystemText: "system",
		UserText:   "user",
		SchemaName: "commit_message",
		Schema: map[string]any{
			"type": "object",
		},
	}

	first, err := runner.RunJSONSchema(context.Background(), req)
	if err != nil {
		t.Fatalf("first RunJSONSchema() error = %v", err)
	}
	if first.OutputText != "{\"message\":\"Use preferred chat path\"}" {
		t.Fatalf("first OutputText = %q, want preferred chat payload", first.OutputText)
	}

	second, err := runner.RunJSONSchema(context.Background(), req)
	if err != nil {
		t.Fatalf("second RunJSONSchema() error = %v", err)
	}
	if second.OutputText != "{\"message\":\"Use preferred chat path\"}" {
		t.Fatalf("second OutputText = %q, want preferred chat payload", second.OutputText)
	}
	if responsesCalls != 0 {
		t.Fatalf("responses endpoint calls = %d, want 0", responsesCalls)
	}
	if chatCalls != 2 {
		t.Fatalf("chat completions endpoint calls = %d, want 2", chatCalls)
	}
}

func TestOpenAICompatibleProviderModelProfileMapsDeepSeekToJSONMode(t *testing.T) {
	t.Parallel()

	opts := OpenAICompatibleResponsesRunnerOptionsForProviderModel("deepseek", "deepseek-v4-pro", OpenAICompatibleResponsesRunnerOptions{
		PreferChatCompletions: true,
	})
	if opts.ChatResponseFormat != OpenAICompatibleChatResponseFormatJSONObject {
		t.Fatalf("DeepSeek chat response format = %q, want %q", opts.ChatResponseFormat, OpenAICompatibleChatResponseFormatJSONObject)
	}

	openRouter := OpenAICompatibleResponsesRunnerOptionsForProviderModel("openrouter", "deepseek/deepseek-v4-pro", OpenAICompatibleResponsesRunnerOptions{
		PreferChatCompletions: true,
	})
	if openRouter.ChatResponseFormat != OpenAICompatibleChatResponseFormatJSONSchema {
		t.Fatalf("OpenRouter chat response format = %q, want %q", openRouter.ChatResponseFormat, OpenAICompatibleChatResponseFormatJSONSchema)
	}
	if !openRouter.RequireParameters {
		t.Fatal("OpenRouter should require routed providers to support all structured-output parameters")
	}

	moonshot := OpenAICompatibleResponsesRunnerOptionsForProviderModel("moonshot", "kimi-k2.7-code", OpenAICompatibleResponsesRunnerOptions{})
	if moonshot.ChatResponseFormat != OpenAICompatibleChatResponseFormatJSONObject {
		t.Fatalf("Moonshot chat response format = %q, want %q", moonshot.ChatResponseFormat, OpenAICompatibleChatResponseFormatJSONObject)
	}

	mlx := OpenAICompatibleResponsesRunnerOptionsForProviderModel("mlx", "mlx-community/Qwen3.5-9B-MLX-4bit", OpenAICompatibleResponsesRunnerOptions{})
	if mlx.ChatResponseFormat != OpenAICompatibleChatResponseFormatPromptOnly {
		t.Fatalf("MLX chat response format = %q, want %q", mlx.ChatResponseFormat, OpenAICompatibleChatResponseFormatPromptOnly)
	}
	if !mlx.PreferChatCompletions {
		t.Fatal("MLX should use its documented chat completions endpoint directly")
	}
}

func TestOpenAICompatibleResponsesRunnerRequiresOpenRouterStructuredOutputParameters(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode OpenRouter request: %v", err)
		}
		responseFormat, _ := body["response_format"].(map[string]any)
		if responseFormat["type"] != "json_schema" {
			t.Fatalf("response_format = %#v, want json_schema", responseFormat)
		}
		jsonSchema, _ := responseFormat["json_schema"].(map[string]any)
		if jsonSchema["strict"] != true {
			t.Fatalf("json_schema.strict = %#v, want true", jsonSchema["strict"])
		}
		provider, _ := body["provider"].(map[string]any)
		if provider["require_parameters"] != true {
			t.Fatalf("provider = %#v, want require_parameters=true", provider)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"model":"deepseek/deepseek-v4-pro",
			"choices":[{"message":{"role":"assistant","content":"{\"message\":\"strict route\"}"}}]
		}`))
	}))
	defer server.Close()

	opts := OpenAICompatibleResponsesRunnerOptionsForProviderModel("openrouter", "deepseek/deepseek-v4-pro", OpenAICompatibleResponsesRunnerOptions{
		PreferChatCompletions: true,
	})
	runner := NewOpenAICompatibleResponsesRunnerWithOptions(server.URL+"/v1", "local-key", "deepseek/deepseek-v4-pro", time.Second, nil, opts)
	response, err := runner.RunJSONSchema(context.Background(), JSONSchemaRequest{
		Model:      "deepseek/deepseek-v4-pro",
		SystemText: "system",
		UserText:   "user",
		SchemaName: "commit_message",
		Schema:     map[string]any{"type": "object"},
	})
	if err != nil {
		t.Fatalf("RunJSONSchema() error = %v", err)
	}
	if response.OutputText != `{"message":"strict route"}` {
		t.Fatalf("OutputText = %q, want strict route payload", response.OutputText)
	}
}

func TestOpenAICompatibleResponsesRunnerUsesPromptSchemaForMLX(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/responses" {
			t.Fatal("MLX profile should not probe the undocumented Responses endpoint")
		}
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode MLX request: %v", err)
		}
		if _, ok := body["response_format"]; ok {
			t.Fatalf("MLX request should omit undocumented response_format: %#v", body["response_format"])
		}
		messages, _ := body["messages"].([]any)
		if len(messages) != 2 {
			t.Fatalf("messages = %#v, want system and user", messages)
		}
		user, _ := messages[1].(map[string]any)
		prompt := fmt.Sprint(user["content"])
		if !strings.Contains(prompt, `"message"`) || !strings.Contains(prompt, `"type": "object"`) {
			t.Fatalf("MLX prompt should include the requested schema: %q", prompt)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"model":"mlx-community/Qwen3.5-9B-MLX-4bit",
			"choices":[{"message":{"role":"assistant","content":"{\"message\":\"prompt constrained\"}"}}]
		}`))
	}))
	defer server.Close()

	opts := OpenAICompatibleResponsesRunnerOptionsForProviderModel("mlx", "mlx-community/Qwen3.5-9B-MLX-4bit", OpenAICompatibleResponsesRunnerOptions{})
	runner := NewOpenAICompatibleResponsesRunnerWithOptions(server.URL+"/v1", "mlx", "mlx-community/Qwen3.5-9B-MLX-4bit", time.Second, nil, opts)
	response, err := runner.RunJSONSchema(context.Background(), JSONSchemaRequest{
		Model:      "mlx-community/Qwen3.5-9B-MLX-4bit",
		SystemText: "system",
		UserText:   "user",
		SchemaName: "commit_message",
		Schema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"message": map[string]any{"type": "string"}},
			"required":   []string{"message"},
		},
	})
	if err != nil {
		t.Fatalf("RunJSONSchema() error = %v", err)
	}
	if response.OutputText != `{"message":"prompt constrained"}` {
		t.Fatalf("OutputText = %q, want prompt-constrained payload", response.OutputText)
	}
}

func TestOpenAICompatibleProviderModelProfileMapsXiaomiToJSONModeAndAPIKeyHeader(t *testing.T) {
	t.Parallel()

	opts := OpenAICompatibleResponsesRunnerOptionsForProviderModel("xiaomi", "mimo-v2.5-pro", OpenAICompatibleResponsesRunnerOptions{
		PreferChatCompletions: true,
	})
	if opts.AuthHeader != OpenAICompatibleAuthHeaderAPIKey {
		t.Fatalf("Xiaomi auth header = %q, want %q", opts.AuthHeader, OpenAICompatibleAuthHeaderAPIKey)
	}
	if opts.ChatResponseFormat != OpenAICompatibleChatResponseFormatJSONObject {
		t.Fatalf("Xiaomi chat response format = %q, want %q", opts.ChatResponseFormat, OpenAICompatibleChatResponseFormatJSONObject)
	}
	if opts.ReasoningStyle != "xiaomi" {
		t.Fatalf("Xiaomi reasoning style = %q, want xiaomi", opts.ReasoningStyle)
	}
	if opts.ChatMaxOutputTokens != xiaomiStructuredOutputMaxCompletionTokens || opts.ChatMaxTokensField != "max_completion_tokens" {
		t.Fatalf("Xiaomi structured output budget = %d via %q", opts.ChatMaxOutputTokens, opts.ChatMaxTokensField)
	}
}

func TestOpenAICompatibleChatCompletionsSendsXiaomiReasoning(t *testing.T) {
	t.Parallel()

	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"model":"mimo-v2.5-pro",
			"choices":[{"message":{"role":"assistant","content":"{\"ok\":true}"}}]
		}`))
	}))
	defer server.Close()

	client := NewOpenAICompatibleChatCompletionsClientWithBaseURLAndOptions("test-key", server.URL, time.Second, nil, OpenAICompatibleChatResponseFormatJSONSchema, OpenAICompatibleAuthHeaderAPIKey, "xiaomi")
	_, err := client.RunJSONSchema(context.Background(), JSONSchemaRequest{
		Model:           "mimo-v2.5-pro",
		SystemText:      "system",
		UserText:        "user",
		SchemaName:      "xiaomi_reasoning",
		Schema:          map[string]any{"type": "object"},
		ReasoningEffort: "high",
	})
	if err != nil {
		t.Fatalf("RunJSONSchema() error = %v", err)
	}
	thinking, ok := gotBody["thinking"].(map[string]any)
	if !ok || thinking["type"] != "enabled" || thinking["reasoning_effort"] != "high" {
		t.Fatalf("thinking = %#v, want enabled high", gotBody["thinking"])
	}
	if _, ok := gotBody["reasoning"]; ok {
		t.Fatalf("xiaomi request should not use OpenAI reasoning field: %#v", gotBody["reasoning"])
	}
}

func TestOpenAICompatibleChatCompletionsCanDisableXiaomiReasoning(t *testing.T) {
	t.Parallel()

	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"finish_reason":"stop","message":{"content":"{\"ok\":true}"}}]}`))
	}))
	defer server.Close()

	client := NewOpenAICompatibleChatCompletionsClientWithBaseURLAndOptions("test-key", server.URL, time.Second, nil, OpenAICompatibleChatResponseFormatJSONObject, OpenAICompatibleAuthHeaderAPIKey, "xiaomi")
	_, err := client.RunJSONSchema(context.Background(), JSONSchemaRequest{
		Model:           "mimo-v2.5-pro",
		SystemText:      "system",
		UserText:        "user",
		SchemaName:      "xiaomi_repair",
		Schema:          map[string]any{"type": "object"},
		ReasoningEffort: "none",
	})
	if err != nil {
		t.Fatalf("RunJSONSchema() error = %v", err)
	}
	thinking, ok := gotBody["thinking"].(map[string]any)
	if !ok || thinking["type"] != "disabled" {
		t.Fatalf("thinking = %#v, want disabled", gotBody["thinking"])
	}
	if _, ok := thinking["reasoning_effort"]; ok {
		t.Fatalf("disabled thinking should omit reasoning_effort: %#v", thinking)
	}
}

func TestOpenAICompatibleChatCompletionsSurfacesIncompleteFinishReason(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model": "mimo-v2.5-pro",
			"choices": []any{map[string]any{
				"finish_reason": "length",
				"message":       map[string]any{"content": "```json\n{\n\""},
			}},
		})
	}))
	defer server.Close()

	client := NewOpenAICompatibleChatCompletionsClientWithBaseURLAndOptions("test-key", server.URL, time.Second, nil, OpenAICompatibleChatResponseFormatJSONObject, OpenAICompatibleAuthHeaderAPIKey, "xiaomi")
	response, err := client.RunJSONSchema(context.Background(), JSONSchemaRequest{
		Model: "mimo-v2.5-pro", Schema: map[string]any{"type": "object"},
	})
	if err != nil {
		t.Fatalf("RunJSONSchema() error = %v", err)
	}
	if response.Status != "incomplete" || response.IncompleteReason != "length" || response.FinishReason != "length" {
		t.Fatalf("response completion state = %+v", response)
	}
	if response.OutputText != "```json\n{\n\"" {
		t.Fatalf("OutputText = %q", response.OutputText)
	}
}

func TestOpenAICompatibleResponsesRunnerUsesXiaomiJSONModeAndAPIKeyHeader(t *testing.T) {
	t.Parallel()

	var chatCalls int
	var modelCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("api-key"); got != "local-key" {
			t.Fatalf("api-key = %q, want local-key", got)
		}
		if got := r.Header.Get("Authorization"); got != "" {
			t.Fatalf("Authorization = %q, want empty", got)
		}
		switch r.URL.Path {
		case "/v1/chat/completions":
			chatCalls++
			var body struct {
				Messages []struct {
					Content string `json:"content"`
				} `json:"messages"`
				ResponseFormat struct {
					Type string `json:"type"`
				} `json:"response_format"`
				MaxCompletionTokens int64 `json:"max_completion_tokens"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode Xiaomi request: %v", err)
			}
			if body.ResponseFormat.Type != "json_object" {
				t.Fatalf("Xiaomi response_format.type = %q, want json_object", body.ResponseFormat.Type)
			}
			if len(body.Messages) != 2 || !strings.Contains(body.Messages[1].Content, `"type": "object"`) {
				t.Fatalf("Xiaomi JSON-mode prompt should contain the requested schema: %#v", body.Messages)
			}
			if body.MaxCompletionTokens != xiaomiStructuredOutputMaxCompletionTokens {
				t.Fatalf("Xiaomi max_completion_tokens = %d, want %d", body.MaxCompletionTokens, xiaomiStructuredOutputMaxCompletionTokens)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"model":"mimo-v2.5-pro",
				"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"{\"message\":\"xiaomi auth ok\"}"}}]
			}`))
		case "/v1/models":
			modelCalls++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"id":"mimo-v2.5-pro"}]}`))
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer server.Close()

	opts := OpenAICompatibleResponsesRunnerOptionsForProviderModel("xiaomi", "mimo-v2.5-pro", OpenAICompatibleResponsesRunnerOptions{
		PreferChatCompletions: true,
	})
	runner := NewOpenAICompatibleResponsesRunnerWithOptions(server.URL+"/v1", "local-key", "", time.Second, nil, opts)
	if runner == nil {
		t.Fatalf("expected runner")
	}
	response, err := runner.RunJSONSchema(context.Background(), JSONSchemaRequest{
		SystemText: "system",
		UserText:   "user",
		SchemaName: "commit_message",
		Schema: map[string]any{
			"type": "object",
		},
	})
	if err != nil {
		t.Fatalf("RunJSONSchema() error = %v", err)
	}
	if response.OutputText != "{\"message\":\"xiaomi auth ok\"}" {
		t.Fatalf("OutputText = %q, want Xiaomi payload", response.OutputText)
	}
	if response.FinishReason != "stop" || response.IncompleteReason != "" {
		t.Fatalf("Xiaomi completion state = %+v", response)
	}
	if chatCalls != 1 {
		t.Fatalf("chat calls = %d, want 1", chatCalls)
	}
	if modelCalls != 1 {
		t.Fatalf("model calls = %d, want 1", modelCalls)
	}
}

func TestOpenAICompatibleResponsesRunnerUsesConfiguredJSONMode(t *testing.T) {
	t.Parallel()

	var jsonModeCalls int
	var schemaChatCalls int
	var responsesCalls int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/responses":
			responsesCalls++
			t.Fatalf("responses endpoint should not be called when chat is preferred")
		case "/v1/chat/completions":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode chat request: %v", err)
			}
			responseFormat, _ := body["response_format"].(map[string]any)
			switch responseFormat["type"] {
			case "json_schema":
				schemaChatCalls++
				t.Fatalf("json_schema should not be used for configured JSON mode")
			case "json_object":
				jsonModeCalls++
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{
					"model":"deepseek-v4-pro",
					"choices":[
						{
							"message":{
								"role":"assistant",
								"content":"{\"message\":\"Use configured json object\"}"
							}
						}
					]
				}`))
			default:
				t.Fatalf("unexpected response_format %#v", responseFormat)
			}
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"id":"deepseek-v4-pro"}]}`))
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer server.Close()

	opts := OpenAICompatibleResponsesRunnerOptionsForProviderModel("deepseek", "deepseek-v4-pro", OpenAICompatibleResponsesRunnerOptions{
		PreferChatCompletions: true,
	})
	runner := NewOpenAICompatibleResponsesRunnerWithOptions(server.URL+"/v1", "local-key", "deepseek-v4-pro", time.Second, nil, opts)
	if runner == nil {
		t.Fatalf("expected runner")
	}

	req := JSONSchemaRequest{
		Model:      "deepseek-v4-pro",
		SystemText: "system",
		UserText:   "user",
		SchemaName: "commit_message",
		Schema: map[string]any{
			"type": "object",
		},
	}

	response, err := runner.RunJSONSchema(context.Background(), req)
	if err != nil {
		t.Fatalf("RunJSONSchema() error = %v", err)
	}
	if response.OutputText != "{\"message\":\"Use configured json object\"}" {
		t.Fatalf("OutputText = %q, want JSON mode payload", response.OutputText)
	}
	if jsonModeCalls != 1 {
		t.Fatalf("json_object chat calls = %d, want 1", jsonModeCalls)
	}
	if schemaChatCalls != 0 {
		t.Fatalf("json_schema chat calls = %d, want 0", schemaChatCalls)
	}
	if responsesCalls != 0 {
		t.Fatalf("responses endpoint calls = %d, want 0", responsesCalls)
	}
}

func TestOpenAICompatibleResponsesRunnerFallsBackToJSONModeWhenJSONSchemaUnsupported(t *testing.T) {
	t.Parallel()

	var schemaChatCalls int
	var jsonModeCalls int
	var responsesCalls int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/responses":
			responsesCalls++
			t.Fatalf("responses endpoint should not be called when chat is preferred")
		case "/v1/chat/completions":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode chat request: %v", err)
			}
			responseFormat, _ := body["response_format"].(map[string]any)
			switch responseFormat["type"] {
			case "json_schema":
				schemaChatCalls++
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":{"message":"This response_format type is unavailable now","type":"invalid_request_error","code":"invalid_request_error"}}`))
			case "json_object":
				jsonModeCalls++
				messages, _ := body["messages"].([]any)
				if len(messages) < 2 {
					t.Fatalf("json mode request missing messages: %#v", body)
				}
				user, _ := messages[1].(map[string]any)
				if !strings.Contains(strings.ToLower(fmt.Sprint(user["content"])), "json") {
					t.Fatalf("json mode prompt should ask for JSON: %#v", user["content"])
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{
					"model":"qwen-local",
					"choices":[
						{
							"message":{
								"role":"assistant",
								"content":"{\"message\":\"Use json object fallback\"}"
							}
						}
					]
				}`))
			default:
				t.Fatalf("unexpected response_format %#v", responseFormat)
			}
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"id":"qwen-local"}]}`))
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer server.Close()

	runner := NewOpenAICompatibleResponsesRunnerWithOptions(server.URL+"/v1", "local-key", "qwen-local", time.Second, nil, OpenAICompatibleResponsesRunnerOptions{
		PreferChatCompletions: true,
	})
	if runner == nil {
		t.Fatalf("expected runner")
	}

	req := JSONSchemaRequest{
		Model:      "qwen-local",
		SystemText: "system",
		UserText:   "user",
		SchemaName: "commit_message",
		Schema: map[string]any{
			"type": "object",
		},
	}

	first, err := runner.RunJSONSchema(context.Background(), req)
	if err != nil {
		t.Fatalf("first RunJSONSchema() error = %v", err)
	}
	if first.OutputText != "{\"message\":\"Use json object fallback\"}" {
		t.Fatalf("first OutputText = %q, want JSON mode payload", first.OutputText)
	}

	second, err := runner.RunJSONSchema(context.Background(), req)
	if err != nil {
		t.Fatalf("second RunJSONSchema() error = %v", err)
	}
	if second.OutputText != "{\"message\":\"Use json object fallback\"}" {
		t.Fatalf("second OutputText = %q, want JSON mode payload", second.OutputText)
	}
	if schemaChatCalls != 1 {
		t.Fatalf("json_schema chat calls = %d, want 1 after caching JSON mode fallback", schemaChatCalls)
	}
	if jsonModeCalls != 2 {
		t.Fatalf("json_object chat calls = %d, want 2", jsonModeCalls)
	}
	if responsesCalls != 0 {
		t.Fatalf("responses endpoint calls = %d, want 0", responsesCalls)
	}
}

func TestOpenAICompatibleResponsesRunnerFallsBackToPromptWhenResponseFormatsUnsupported(t *testing.T) {
	t.Parallel()

	var schemaCalls int
	var jsonModeCalls int
	var promptCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		responseFormat, hasResponseFormat := body["response_format"].(map[string]any)
		if !hasResponseFormat {
			promptCalls++
			messages, _ := body["messages"].([]any)
			user, _ := messages[1].(map[string]any)
			if !strings.Contains(fmt.Sprint(user["content"]), `"message"`) {
				t.Fatalf("prompt fallback should carry schema: %#v", user["content"])
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"model":"basic-local",
				"choices":[{"message":{"role":"assistant","content":"{\"message\":\"prompt fallback\"}"}}]
			}`))
			return
		}
		switch responseFormat["type"] {
		case "json_schema":
			schemaCalls++
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"response_format json_schema is unsupported"}}`))
		case "json_object":
			jsonModeCalls++
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(`{"error":{"message":"response_format json_object is unsupported"}}`))
		default:
			t.Fatalf("unexpected response_format %#v", responseFormat)
		}
	}))
	defer server.Close()

	runner := NewOpenAICompatibleResponsesRunnerWithOptions(server.URL+"/v1", "local-key", "basic-local", time.Second, nil, OpenAICompatibleResponsesRunnerOptions{
		PreferChatCompletions: true,
	})
	req := JSONSchemaRequest{
		Model:      "basic-local",
		SystemText: "system",
		UserText:   "user",
		SchemaName: "commit_message",
		Schema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"message": map[string]any{"type": "string"}},
		},
	}
	for i := 0; i < 2; i++ {
		response, err := runner.RunJSONSchema(context.Background(), req)
		if err != nil {
			t.Fatalf("RunJSONSchema() attempt %d error = %v", i+1, err)
		}
		if response.OutputText != `{"message":"prompt fallback"}` {
			t.Fatalf("OutputText = %q, want prompt fallback payload", response.OutputText)
		}
	}
	if schemaCalls != 1 || jsonModeCalls != 1 || promptCalls != 2 {
		t.Fatalf("calls schema/json/prompt = %d/%d/%d, want 1/1/2", schemaCalls, jsonModeCalls, promptCalls)
	}
}

func TestOpenAICompatibleResponsesRunnerCachesResponsesFallbackWhenPreferredChatIsMissing(t *testing.T) {
	t.Parallel()

	var responsesCalls int
	var chatCalls int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/chat/completions":
			chatCalls++
			http.NotFound(w, r)
		case "/v1/responses":
			responsesCalls++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"status":"completed",
				"model":"qwen-local",
				"output":[
					{
						"type":"message",
						"role":"assistant",
						"content":[
							{
								"type":"output_text",
								"text":"{\"message\":\"Use responses fallback\"}"
							}
						]
					}
				]
			}`))
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"id":"qwen-local"}]}`))
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer server.Close()

	runner := NewOpenAICompatibleResponsesRunnerWithOptions(server.URL+"/v1", "local-key", "qwen-local", time.Second, nil, OpenAICompatibleResponsesRunnerOptions{
		PreferChatCompletions: true,
	})
	if runner == nil {
		t.Fatalf("expected runner")
	}

	req := JSONSchemaRequest{
		Model:      "qwen-local",
		SystemText: "system",
		UserText:   "user",
		SchemaName: "commit_message",
		Schema: map[string]any{
			"type": "object",
		},
	}

	first, err := runner.RunJSONSchema(context.Background(), req)
	if err != nil {
		t.Fatalf("first RunJSONSchema() error = %v", err)
	}
	if first.OutputText != "{\"message\":\"Use responses fallback\"}" {
		t.Fatalf("first OutputText = %q, want responses fallback payload", first.OutputText)
	}

	second, err := runner.RunJSONSchema(context.Background(), req)
	if err != nil {
		t.Fatalf("second RunJSONSchema() error = %v", err)
	}
	if second.OutputText != "{\"message\":\"Use responses fallback\"}" {
		t.Fatalf("second OutputText = %q, want responses fallback payload", second.OutputText)
	}
	if chatCalls != 1 {
		t.Fatalf("chat completions endpoint calls = %d, want 1 after caching responses fallback", chatCalls)
	}
	if responsesCalls != 2 {
		t.Fatalf("responses endpoint calls = %d, want 2", responsesCalls)
	}
}

func wrapErr(prefix string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", prefix, err)
}
