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

	snapshot := usage.Snapshot(true)
	if snapshot.Completed != 1 || snapshot.Running != 0 || snapshot.Totals.TotalTokens != 18 {
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
