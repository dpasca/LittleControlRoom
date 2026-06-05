package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestOllamaJSONSchemaRunnerUsesNativeGenerateWithThinkDisabled(t *testing.T) {
	t.Parallel()

	var got map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/generate":
			if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"model":"gemma4:12b-mlx",
				"response":"{\"summary\":\"ready\",\"category\":\"completed\"}",
				"done":true,
				"done_reason":"stop",
				"prompt_eval_count":31,
				"prompt_eval_duration":400000000,
				"eval_count":9,
				"eval_duration":900000000
			}`))
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"id":"gemma4:12b-mlx"}]}`))
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer server.Close()

	usage := NewUsageTracker()
	runner := NewOllamaJSONSchemaRunner(server.URL+"/v1", "gemma4:12b-mlx", time.Second, usage)
	resp, err := runner.RunJSONSchema(context.Background(), JSONSchemaRequest{
		Model:      "gemma4:12b-mlx",
		SystemText: "Summarize LCR state.",
		UserText:   "The latest work finished cleanly.",
		SchemaName: "assessment",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"summary":  map[string]any{"type": "string"},
				"category": map[string]any{"type": "string"},
			},
			"required": []string{"summary", "category"},
		},
	})
	if err != nil {
		t.Fatalf("RunJSONSchema() error = %v", err)
	}
	if resp.OutputText != `{"summary":"ready","category":"completed"}` {
		t.Fatalf("OutputText = %q", resp.OutputText)
	}
	if got["think"] != false {
		t.Fatalf("think = %#v, want false", got["think"])
	}
	if got["stream"] != false {
		t.Fatalf("stream = %#v, want false", got["stream"])
	}
	if got["system"] == "" {
		t.Fatalf("system = %#v, want non-empty system prompt", got["system"])
	}
	if got["prompt"] == "" {
		t.Fatalf("prompt = %#v, want non-empty generation prompt", got["prompt"])
	}
	if _, ok := got["format"].(map[string]any); !ok {
		t.Fatalf("format = %#v, want JSON schema object", got["format"])
	}
	snapshot := usage.Snapshot(true)
	if snapshot.Completed != 1 || snapshot.Totals.OutputTokens != 9 {
		t.Fatalf("usage snapshot = %+v, want one completion with output tokens", snapshot)
	}
	if snapshot.LastOutputEvalDuration != 900*time.Millisecond {
		t.Fatalf("LastOutputEvalDuration = %s, want 900ms", snapshot.LastOutputEvalDuration)
	}
	if got := snapshot.LastOutputTokensPerSecond; got < 9.9 || got > 10.1 {
		t.Fatalf("LastOutputTokensPerSecond = %f, want about 10", got)
	}
}

func TestDecodeOllamaModelMetadataReadsContextWindow(t *testing.T) {
	t.Parallel()

	meta, err := DecodeOllamaModelMetadata([]byte(`{
		"details":{"parameter_size":"13.0B","quantization_level":"nvfp4"},
		"model_info":{
			"gemma4_unified.context_length":131072,
			"general.architecture":"gemma4_unified"
		},
		"capabilities":["completion","tools","thinking"]
	}`), "gemma4:12b-mlx")
	if err != nil {
		t.Fatalf("DecodeOllamaModelMetadata() error = %v", err)
	}
	if meta.ContextWindow != 131072 {
		t.Fatalf("ContextWindow = %d, want 131072", meta.ContextWindow)
	}
	if meta.ParameterSize != "13.0B" || meta.Quantization != "nvfp4" || meta.Architecture != "gemma4_unified" {
		t.Fatalf("metadata = %+v, want model details", meta)
	}
	if len(meta.Capabilities) != 3 {
		t.Fatalf("Capabilities = %#v, want three capabilities", meta.Capabilities)
	}
}

func TestOllamaTextRunnerCanEnableThinkingWithoutLeakingIt(t *testing.T) {
	t.Parallel()

	var got map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/generate":
			if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"model":"gemma4:12b-mlx",
				"thinking":"Plan the answer privately.",
				"response":"<think>extra inline thought</think>\nThe final answer.",
				"done":true,
				"done_reason":"stop",
				"prompt_eval_count":7,
				"eval_count":11
			}`))
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"id":"gemma4:12b-mlx"}]}`))
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer server.Close()

	runner := NewOllamaTextRunnerWithOptions(server.URL+"/v1", "gemma4:12b-mlx", time.Second, nil, OllamaChatOptions{Think: true})
	resp, err := runner.RunText(context.Background(), TextRequest{
		Model: "gemma4:12b-mlx",
		Messages: []TextMessage{
			{Role: "user", Content: "Answer directly."},
		},
	})
	if err != nil {
		t.Fatalf("RunText() error = %v", err)
	}
	if got["think"] != true {
		t.Fatalf("think = %#v, want true", got["think"])
	}
	if resp.OutputText != "The final answer." {
		t.Fatalf("OutputText = %q, want final content without thinking", resp.OutputText)
	}
}
