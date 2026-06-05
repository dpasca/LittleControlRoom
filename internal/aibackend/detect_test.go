package aibackend

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"lcroom/internal/config"
)

func TestParseClaudeAuthStatusJSON(t *testing.T) {
	t.Parallel()

	raw := `{"loggedIn":true,"authMethod":"claude.ai","subscriptionType":"max"}`
	got, ok := parseClaudeAuthStatus(raw)
	if !ok {
		t.Fatalf("parseClaudeAuthStatus() reported !ok")
	}
	if !got.LoggedIn {
		t.Fatalf("LoggedIn = false, want true")
	}
	if got.AuthMethod != "claude.ai" {
		t.Fatalf("AuthMethod = %q, want claude.ai", got.AuthMethod)
	}
	if got.SubscriptionType != "max" {
		t.Fatalf("SubscriptionType = %q, want max", got.SubscriptionType)
	}
}

func TestParseClaudeAuthStatusExtractsEmbeddedJSON(t *testing.T) {
	t.Parallel()

	raw := "warning: something\n{\"loggedIn\":false,\"authMethod\":\"api_key\"}\n"
	got, ok := parseClaudeAuthStatus(raw)
	if !ok {
		t.Fatalf("parseClaudeAuthStatus() reported !ok")
	}
	if got.LoggedIn {
		t.Fatalf("LoggedIn = true, want false")
	}
	if got.AuthMethod != "api_key" {
		t.Fatalf("AuthMethod = %q, want api_key", got.AuthMethod)
	}
}

func TestClaudeAuthDetailIncludesMethodAndSubscription(t *testing.T) {
	t.Parallel()

	got := claudeAuthDetail(claudeAuthStatus{
		LoggedIn:         true,
		AuthMethod:       "claude.ai",
		SubscriptionType: "max",
	})
	want := "Claude Code ready via claude.ai (max)"
	if got != want {
		t.Fatalf("claudeAuthDetail() = %q, want %q", got, want)
	}
}

func TestDetectOpenAICompatibleLocalReady(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("path = %q, want /v1/models", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"qwen-local"}]}`))
	}))
	defer server.Close()

	cfg := config.Default()
	cfg.MLXBaseURL = server.URL + "/v1"

	status := detectOpenAICompatibleLocal(context.Background(), cfg, config.AIBackendMLX)
	if !status.Ready {
		t.Fatalf("status.Ready = false, want true")
	}
	if got := status.ActiveModel; got != "qwen-local" {
		t.Fatalf("status.ActiveModel = %q, want %q", got, "qwen-local")
	}
	if len(status.Models) != 1 || status.Models[0] != "qwen-local" {
		t.Fatalf("status.Models = %#v, want qwen-local", status.Models)
	}
	if status.Detail == "" {
		t.Fatalf("status.Detail should describe the ready server")
	}
}

func TestDetectOllamaLocalAddsContextMetadata(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"gemma4:12b-mlx"}]}`))
		case "/api/show":
			_, _ = w.Write([]byte(`{
				"details":{"parameter_size":"13.0B","quantization_level":"nvfp4"},
				"model_info":{"gemma4_unified.context_length":131072,"general.architecture":"gemma4_unified"},
				"capabilities":["completion","tools","thinking"]
			}`))
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer server.Close()

	cfg := config.Default()
	cfg.OllamaBaseURL = server.URL + "/v1"
	cfg.OllamaModel = "gemma4:12b-mlx"

	status := detectOpenAICompatibleLocal(context.Background(), cfg, config.AIBackendOllama)
	if !status.Ready {
		t.Fatalf("status.Ready = false, want true")
	}
	if status.ContextWindow != 131072 {
		t.Fatalf("ContextWindow = %d, want 131072", status.ContextWindow)
	}
	if status.ContextWarning != "" {
		t.Fatalf("ContextWarning = %q, want none for large context", status.ContextWarning)
	}
	if status.ContextDetail == "" {
		t.Fatalf("ContextDetail should describe model context")
	}
}

func TestDetectOpenAICompatibleLocalConfiguredModelMustExist(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"qwen-local"},{"id":"qwen-other"}]}`))
	}))
	defer server.Close()

	cfg := config.Default()
	cfg.MLXBaseURL = server.URL + "/v1"
	cfg.MLXModel = "missing-model"

	status := detectOpenAICompatibleLocal(context.Background(), cfg, config.AIBackendMLX)
	if status.Ready {
		t.Fatalf("status.Ready = true, want false when configured model is missing")
	}
	if status.LoginHint == "" {
		t.Fatalf("status.LoginHint should explain how to fix the missing configured model")
	}
}

func TestDetectXiaomiTokenPlanKeyNeedsTokenPlanBaseURL(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.XiaomiAPIKey = "TC_example"

	status := detectOpenAICompatibleCloud(context.Background(), cfg, config.AIBackendXiaomi)
	if status.Ready {
		t.Fatalf("status.Ready = true, want false for Token Plan key on regular API URL")
	}
	if status.Detail == "" || status.LoginHint == "" {
		t.Fatalf("status should explain Token Plan URL mismatch: %+v", status)
	}
}

func TestOpenCodeProviderStatusRecognizesNonOpenAIProviders(t *testing.T) {
	t.Parallel()

	raw := "\x1b[0m\n┌  Credentials\n│\n●  MiniMax (minimax.io) \x1b[90mapi\n│\n●  OpenCode Zen \x1b[90mapi\n│\n└  2 credentials\n"

	ready, detail := openCodeProviderStatus(raw)
	if !ready {
		t.Fatalf("openCodeProviderStatus() ready = false, want true")
	}
	if got, want := detail, "OpenCode providers ready: MiniMax API, OpenCode Zen API."; got != want {
		t.Fatalf("openCodeProviderStatus() detail = %q, want %q", got, want)
	}
}
