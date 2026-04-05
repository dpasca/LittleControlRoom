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
