package codexapp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestLCAgentDeepSeekDiscoveryUsesLiveAvailability(t *testing.T) {
	var response atomic.Value
	response.Store(`{"data":[{"id":"deepseek-flash"},{"id":"future-model","name":"Future Model"}]}`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" || r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("unexpected model discovery request: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(response.Load().(string)))
	}))
	defer server.Close()
	t.Setenv("DEEPSEEK_BASE_URL", server.URL)
	cfg := LCAgentModelListConfig{Provider: "deepseek", DeepSeekAPIKey: "test-key"}

	models, err := LCAgentModelOptions(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 || models[0].Model != "deepseek-flash" || models[1].Model != "future-model" {
		t.Fatalf("expected only live models in provider order, got %#v", models)
	}
	if models[0].DisplayName != "DeepSeek V4.1 Flash" || models[1].DisplayName != "Future Model" {
		t.Fatalf("missing friendly model names: %#v", models)
	}

	// A subsequent request must discover releases without rebuilding or restarting.
	response.Store(`{"data":[{"id":"next-release"}]}`)
	cfg.Model = "deepseek-v4-flash"
	models, err = LCAgentModelOptions(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 || models[0].Model != cfg.Model || models[1].Model != "next-release" {
		t.Fatalf("expected refreshed list plus configured model, got %#v", models)
	}
	if !strings.Contains(models[0].Description, "did not return") {
		t.Fatalf("unlisted current model needs an explicit warning: %#v", models[0])
	}
}

func TestLCAgentDeepSeekDiscoveryFailureKeepsFallbackAndError(t *testing.T) {
	for _, response := range []struct {
		name   string
		status int
		body   string
	}{
		{"unauthorized", http.StatusUnauthorized, `{"error":{"message":"Invalid API key"}}`},
		{"empty", http.StatusOK, `{"data":[]}`},
		{"malformed", http.StatusOK, `{`},
	} {
		t.Run(response.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(response.status)
				_, _ = w.Write([]byte(response.body))
			}))
			defer server.Close()
			t.Setenv("DEEPSEEK_BASE_URL", server.URL)
			models, err := LCAgentModelOptions(context.Background(), LCAgentModelListConfig{
				Provider: "deepseek", DeepSeekAPIKey: "test-key", Model: "custom-model",
			})
			if err == nil {
				t.Fatal("expected discovery error alongside fallback models")
			}
			if !lcagentModelOptionExists(models, "deepseek-flash") || !lcagentModelOptionExists(models, "custom-model") {
				t.Fatalf("missing current Flash fallback or configured model: %#v", models)
			}
		})
	}
}
