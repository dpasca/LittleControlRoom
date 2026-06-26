package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeBrowserWebSearcher struct {
	query       string
	maxResults  int
	site        string
	recencyDays int
	result      ToolResult
}

func (f *fakeBrowserWebSearcher) SearchBrowser(ctx context.Context, query string, maxResults int, site string, recencyDays int) ToolResult {
	f.query = query
	f.maxResults = maxResults
	f.site = site
	f.recencyDays = recencyDays
	return f.result
}

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

func TestWebSearchRunnerBrowser(t *testing.T) {
	browser := &fakeBrowserWebSearcher{
		result: ToolResult{Success: true, Output: "backend: browser\nquery: lcagent\nresults: 1\n\n1. LCAgent Browser\n   url: https://example.com/browser\n"},
	}
	runner, status := NewWebSearchRunner(WebSearchConfig{
		Backend: "browser",
		Browser: browser,
	})
	if !status.Enabled {
		t.Fatalf("status = %#v, want enabled", status)
	}

	result := runner.Search(context.Background(), "lcagent", 42, "https://example.com/docs", 14)
	if !result.Success {
		t.Fatalf("Search() failed: %#v", result)
	}
	if browser.query != "lcagent" || browser.maxResults != maxWebSearchMaxResults || browser.site != "https://example.com/docs" || browser.recencyDays != 14 {
		t.Fatalf("browser call = query %q max %d site %q recency %d", browser.query, browser.maxResults, browser.site, browser.recencyDays)
	}
	if !strings.Contains(result.Output, "backend: browser") || !strings.Contains(result.Output, "https://example.com/browser") {
		t.Fatalf("output = %q", result.Output)
	}
}

func TestWebSearchRunnerBrowserRequiresManagedBrowser(t *testing.T) {
	_, status := NewWebSearchRunner(WebSearchConfig{Backend: "browser"})
	if status.Enabled {
		t.Fatalf("status = %#v, want disabled", status)
	}
	if !strings.Contains(status.Message, "managed browser control") {
		t.Fatalf("message = %q", status.Message)
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
