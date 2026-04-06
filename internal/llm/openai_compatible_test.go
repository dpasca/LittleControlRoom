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
