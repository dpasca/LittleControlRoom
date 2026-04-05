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
	if status.Detail == "" {
		t.Fatalf("status.Detail should describe the ready server")
	}
}
