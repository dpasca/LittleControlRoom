package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestResponsesTextClientSendsPlainChatRequest(t *testing.T) {
	t.Parallel()

	var got map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			t.Fatalf("path = %q, want /v1/responses", r.URL.Path)
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer test-key" {
			t.Fatalf("authorization = %q", auth)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"status": "completed",
			"model": "gpt-test",
			"usage": {
				"input_tokens": 11,
				"input_tokens_details": {"cached_tokens": 3},
				"output_tokens": 7,
				"output_tokens_details": {"reasoning_tokens": 2},
				"total_tokens": 18
			},
			"output": [{
				"type": "message",
				"role": "assistant",
				"content": [{"type": "output_text", "text": "We should inspect the hot project first."}]
			}]
		}`))
	}))
	defer server.Close()

	usage := NewUsageTracker()
	client := NewResponsesTextClientWithBaseURL("test-key", server.URL+"/v1", time.Second, usage)
	resp, err := client.RunText(context.Background(), TextRequest{
		Model:           "gpt-test",
		SystemText:      "You are a project assistant.",
		ReasoningEffort: "low",
		Messages: []TextMessage{
			{Role: "user", Content: "Current state"},
			{Role: "assistant", Content: "I am watching."},
			{Role: "user", Content: "What now?"},
		},
	})
	if err != nil {
		t.Fatalf("RunText() error = %v", err)
	}
	if resp.OutputText != "We should inspect the hot project first." {
		t.Fatalf("OutputText = %q", resp.OutputText)
	}
	if resp.Usage.TotalTokens != 18 || resp.Usage.CachedInputTokens != 3 || resp.Usage.ReasoningTokens != 2 {
		t.Fatalf("unexpected usage: %+v", resp.Usage)
	}

	if got["model"] != "gpt-test" || got["store"] != false {
		t.Fatalf("unexpected request top-level fields: %#v", got)
	}
	reasoning, ok := got["reasoning"].(map[string]any)
	if !ok || reasoning["effort"] != "low" {
		t.Fatalf("unexpected reasoning field: %#v", got["reasoning"])
	}
	input, ok := got["input"].([]any)
	if !ok || len(input) != 4 {
		t.Fatalf("input len = %#v, want 4", got["input"])
	}
	first, _ := input[0].(map[string]any)
	if first["role"] != "system" {
		t.Fatalf("first role = %#v", first["role"])
	}
	last, _ := input[3].(map[string]any)
	if last["role"] != "user" {
		t.Fatalf("last role = %#v", last["role"])
	}
	for index, want := range []string{"input_text", "input_text", "output_text", "input_text"} {
		if gotType := responseTextRequestContentType(t, input, index); gotType != want {
			t.Fatalf("input[%d] content type = %q, want %q", index, gotType, want)
		}
	}

	snapshot := usage.Snapshot(true)
	if snapshot.Completed != 1 || snapshot.Running != 0 || snapshot.Totals.TotalTokens != 18 {
		t.Fatalf("unexpected usage snapshot: %+v", snapshot)
	}
}

func TestResponsesTextClientStreamsDeltas(t *testing.T) {
	t.Parallel()

	var got map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			t.Fatalf("path = %q, want /v1/responses", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"Look\"}\n\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\" alive\"}\n\n"))
		_, _ = w.Write([]byte(`data: {"type":"response.completed","response":{"status":"completed","model":"gpt-test","usage":{"input_tokens":9,"input_tokens_details":{"cached_tokens":2},"output_tokens":4,"output_tokens_details":{"reasoning_tokens":1},"total_tokens":13},"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Look alive"}]}]}}` + "\n\n"))
	}))
	defer server.Close()

	usage := NewUsageTracker()
	client := NewResponsesTextClientWithBaseURL("test-key", server.URL+"/v1", time.Second, usage)
	var deltas []string
	resp, err := client.RunTextStream(context.Background(), TextRequest{
		Model: "gpt-test",
		Messages: []TextMessage{
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: "hi"},
			{Role: "user", Content: "next"},
		},
	}, func(event TextStreamEvent) error {
		deltas = append(deltas, event.Delta)
		return nil
	})
	if err != nil {
		t.Fatalf("RunTextStream() error = %v", err)
	}
	if got["stream"] != true {
		t.Fatalf("stream request field = %#v, want true", got["stream"])
	}
	input, ok := got["input"].([]any)
	if !ok || len(input) != 3 {
		t.Fatalf("input len = %#v, want 3", got["input"])
	}
	for index, want := range []string{"input_text", "output_text", "input_text"} {
		if gotType := responseTextRequestContentType(t, input, index); gotType != want {
			t.Fatalf("input[%d] content type = %q, want %q", index, gotType, want)
		}
	}
	if strings.Join(deltas, "") != "Look alive" || resp.OutputText != "Look alive" {
		t.Fatalf("streamed deltas = %q response = %+v", strings.Join(deltas, ""), resp)
	}
	if resp.Usage.TotalTokens != 13 || resp.Usage.CachedInputTokens != 2 || resp.Usage.ReasoningTokens != 1 {
		t.Fatalf("unexpected usage: %+v", resp.Usage)
	}
	snapshot := usage.Snapshot(true)
	if snapshot.Completed != 1 || snapshot.Running != 0 || snapshot.Totals.TotalTokens != 13 {
		t.Fatalf("unexpected usage snapshot: %+v", snapshot)
	}
}

func TestResponsesTextClientReportsHTTPStatusError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"slow down"}}`))
	}))
	defer server.Close()

	usage := NewUsageTracker()
	client := NewResponsesTextClientWithBaseURL("test-key", server.URL+"/v1", time.Second, usage)
	_, err := client.RunText(context.Background(), TextRequest{
		Model:    "gpt-test",
		Messages: []TextMessage{{Role: "user", Content: "hello"}},
	})
	if err == nil {
		t.Fatal("RunText() error = nil, want HTTP error")
	}
	if !strings.Contains(err.Error(), "429") || !strings.Contains(err.Error(), "slow down") {
		t.Fatalf("unexpected error: %v", err)
	}
	snapshot := usage.Snapshot(true)
	if snapshot.Failed != 1 || snapshot.Running != 0 {
		t.Fatalf("unexpected usage snapshot: %+v", snapshot)
	}
}

func TestOpenAICompatibleChatTextClientStreamsDeltas(t *testing.T) {
	t.Parallel()

	var got map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"model\":\"qwen-local\",\"choices\":[{\"delta\":{\"content\":\"Local\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"model\":\"qwen-local\",\"choices\":[{\"delta\":{\"content\":\" stream\"},\"finish_reason\":\"stop\"}]}\n\n"))
		_, _ = w.Write([]byte(`data: {"model":"qwen-local","choices":[],"usage":{"prompt_tokens":5,"prompt_tokens_details":{"cached_tokens":1},"completion_tokens":4,"completion_tokens_details":{"reasoning_tokens":0},"total_tokens":9}}` + "\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	usage := NewUsageTracker()
	client := NewOpenAICompatibleChatTextClientWithHTTPClient("local-key", server.URL, server.Client(), usage)
	var deltas []string
	resp, err := client.RunTextStream(context.Background(), TextRequest{
		Model:    "qwen-local",
		Messages: []TextMessage{{Role: "user", Content: "hello"}},
	}, func(event TextStreamEvent) error {
		deltas = append(deltas, event.Delta)
		return nil
	})
	if err != nil {
		t.Fatalf("RunTextStream() error = %v", err)
	}
	if got["stream"] != true {
		t.Fatalf("stream request field = %#v, want true", got["stream"])
	}
	if strings.Join(deltas, "") != "Local stream" || resp.OutputText != "Local stream" || resp.Model != "qwen-local" {
		t.Fatalf("unexpected streamed response: deltas=%q resp=%+v", strings.Join(deltas, ""), resp)
	}
	if resp.Usage.TotalTokens != 9 || resp.Usage.CachedInputTokens != 1 {
		t.Fatalf("unexpected usage: %+v", resp.Usage)
	}
	snapshot := usage.Snapshot(true)
	if snapshot.Completed != 1 || snapshot.Totals.TotalTokens != 9 {
		t.Fatalf("unexpected usage snapshot: %+v", snapshot)
	}
}

func responseTextRequestContentType(t *testing.T, input []any, index int) string {
	t.Helper()
	item, ok := input[index].(map[string]any)
	if !ok {
		t.Fatalf("input[%d] = %#v, want object", index, input[index])
	}
	content, ok := item["content"].([]any)
	if !ok || len(content) != 1 {
		t.Fatalf("input[%d].content = %#v, want one content item", index, item["content"])
	}
	part, ok := content[0].(map[string]any)
	if !ok {
		t.Fatalf("input[%d].content[0] = %#v, want object", index, content[0])
	}
	contentType, ok := part["type"].(string)
	if !ok {
		t.Fatalf("input[%d].content[0].type = %#v, want string", index, part["type"])
	}
	return contentType
}

func TestOpenAICompatibleTextRunnerUsesChatCompletionsAndAutoModel(t *testing.T) {
	t.Parallel()

	var got map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"qwen-local"}]}`))
		case "/v1/chat/completions":
			if auth := r.Header.Get("Authorization"); auth != "Bearer local-key" {
				t.Fatalf("authorization = %q", auth)
			}
			if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			_, _ = w.Write([]byte(`{
				"model": "qwen-local",
				"usage": {
					"prompt_tokens": 5,
					"prompt_tokens_details": {"cached_tokens": 1},
					"completion_tokens": 4,
					"completion_tokens_details": {"reasoning_tokens": 0},
					"total_tokens": 9
				},
				"choices": [{"message": {"content": "Local boss chat ready."}, "finish_reason": "stop"}]
			}`))
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer server.Close()

	usage := NewUsageTracker()
	runner := NewOpenAICompatibleTextRunner(server.URL+"/v1", "local-key", "", time.Second, usage)
	resp, err := runner.RunText(context.Background(), TextRequest{
		SystemText: "You are a local assistant.",
		Messages:   []TextMessage{{Role: "user", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("RunText() error = %v", err)
	}
	if resp.OutputText != "Local boss chat ready." || resp.Model != "qwen-local" {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if got["model"] != "qwen-local" {
		t.Fatalf("request model = %#v, want qwen-local", got["model"])
	}
	if _, ok := got["reasoning"]; ok {
		t.Fatalf("openai-compatible chat text request should not include reasoning: %#v", got)
	}
	messages, ok := got["messages"].([]any)
	if !ok || len(messages) != 2 {
		t.Fatalf("messages = %#v, want system and user", got["messages"])
	}
	snapshot := usage.Snapshot(true)
	if snapshot.Completed != 1 || snapshot.Totals.TotalTokens != 9 {
		t.Fatalf("unexpected usage snapshot: %+v", snapshot)
	}
}
