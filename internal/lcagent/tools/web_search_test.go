package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWebSearchRunnerSearXNG(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("q"); got != "lcagent site:example.com" {
			t.Fatalf("query = %q", got)
		}
		if got := r.URL.Query().Get("format"); got != "json" {
			t.Fatalf("format = %q", got)
		}
		_, _ = w.Write([]byte(`{"results":[{"title":"LCAgent docs","url":"https://example.com/docs","content":"Search result snippet","engine":"unit"}]}`))
	}))
	defer server.Close()

	runner, status := NewWebSearchRunner(WebSearchConfig{
		Backend: "searxng",
		URL:     server.URL,
	})
	if !status.Enabled {
		t.Fatalf("status = %#v, want enabled", status)
	}

	result := runner.Search(context.Background(), "lcagent", 5, "example.com", 7)
	if !result.Success {
		t.Fatalf("Search() failed: %#v", result)
	}
	for _, want := range []string{"backend: searxng", "query: lcagent", "LCAgent docs", "https://example.com/docs", "Search result snippet"} {
		if !strings.Contains(result.Output, want) {
			t.Fatalf("output missing %q:\n%s", want, result.Output)
		}
	}
}

func TestWebSearchRunnerExa(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("x-api-key"); got != "exa-key" {
			t.Fatalf("x-api-key = %q", got)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if got := body["query"]; got != "lcagent" {
			t.Fatalf("query = %#v", got)
		}
		domains, _ := body["includeDomains"].([]any)
		if len(domains) != 1 || domains[0] != "example.com" {
			t.Fatalf("includeDomains = %#v", body["includeDomains"])
		}
		_, _ = w.Write([]byte(`{"results":[{"title":"LCAgent Exa","url":"https://example.com/exa","summary":"Exa result summary","publishedDate":"2026-05-13T00:00:00Z"}]}`))
	}))
	defer server.Close()

	runner, status := NewWebSearchRunner(WebSearchConfig{
		Backend: "exa",
		APIKey:  "exa-key",
		ExaURL:  server.URL,
	})
	if !status.Enabled {
		t.Fatalf("status = %#v, want enabled", status)
	}

	result := runner.Search(context.Background(), "lcagent", 5, "https://example.com/docs", 30)
	if !result.Success {
		t.Fatalf("Search() failed: %#v", result)
	}
	for _, want := range []string{"backend: exa", "LCAgent Exa", "https://example.com/exa", "Exa result summary"} {
		if !strings.Contains(result.Output, want) {
			t.Fatalf("output missing %q:\n%s", want, result.Output)
		}
	}
}

func TestWebSearchRunnerGoogleRequiresCredentials(t *testing.T) {
	t.Setenv("GOOGLE_SEARCH_API_KEY", "")
	t.Setenv("GOOGLE_API_KEY", "")
	t.Setenv("GOOGLE_SEARCH_ENGINE_ID", "")
	t.Setenv("GOOGLE_CSE_ID", "")
	_, status := NewWebSearchRunner(WebSearchConfig{Backend: "google"})
	if status.Enabled {
		t.Fatalf("status = %#v, want disabled", status)
	}
	if !strings.Contains(status.Message, "Google search needs an API key and search engine ID") {
		t.Fatalf("message = %q", status.Message)
	}
}
