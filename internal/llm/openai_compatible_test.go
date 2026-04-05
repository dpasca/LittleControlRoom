package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type recordingJSONSchemaRunner struct {
	lastRequest JSONSchemaRequest
	response    JSONSchemaResponse
}

func (r *recordingJSONSchemaRunner) RunJSONSchema(_ context.Context, req JSONSchemaRequest) (JSONSchemaResponse, error) {
	r.lastRequest = req
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
