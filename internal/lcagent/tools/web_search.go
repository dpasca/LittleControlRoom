package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultWebSearchTimeout    = 15 * time.Second
	defaultWebSearchMaxResults = 5
	maxWebSearchMaxResults     = 10
	maxWebSearchOutputBytes    = 20000
)

type WebSearchConfig struct {
	Backend        string
	APIKey         string
	SearchEngineID string
	URL            string
	ExaURL         string
	EnvFile        string
	Browser        BrowserWebSearcher
	HTTPClient     *http.Client
}

type WebSearchRunner struct {
	Backend        WebSearchBackend
	APIKey         string
	SearchEngineID string
	URL            string
	ExaURL         string
	Browser        BrowserWebSearcher
	HTTPClient     *http.Client
}

type BrowserWebSearcher interface {
	SearchBrowser(context.Context, string, int, string, int) ToolResult
}

type WebSearchStatus struct {
	Enabled bool   `json:"enabled"`
	Backend string `json:"backend"`
	Message string `json:"message,omitempty"`
}

func NewWebSearchRunner(cfg WebSearchConfig) (WebSearchRunner, WebSearchStatus) {
	_ = loadEnvFileIfPresent(cfg.EnvFile)
	backend := parseWebSearchBackend(firstNonEmpty(cfg.Backend, os.Getenv("LCAGENT_WEB_SEARCH_BACKEND")))
	runner := WebSearchRunner{
		Backend:        backend,
		APIKey:         webSearchAPIKey(backend, cfg.APIKey),
		SearchEngineID: firstNonEmpty(cfg.SearchEngineID, os.Getenv("GOOGLE_SEARCH_ENGINE_ID"), os.Getenv("GOOGLE_CSE_ID")),
		URL:            firstNonEmpty(cfg.URL, os.Getenv("LCAGENT_WEB_SEARCH_URL"), os.Getenv("LCAGENT_SEARXNG_URL")),
		ExaURL:         firstNonEmpty(cfg.ExaURL, os.Getenv("LCAGENT_EXA_URL")),
		Browser:        cfg.Browser,
		HTTPClient:     cfg.HTTPClient,
	}
	if runner.HTTPClient == nil {
		runner.HTTPClient = &http.Client{Timeout: defaultWebSearchTimeout}
	}
	status := runner.Status()
	if !status.Enabled {
		return WebSearchRunner{}, status
	}
	return runner, status
}

func (r WebSearchRunner) Status() WebSearchStatus {
	switch r.Backend {
	case WebSearchBackendExa:
		if strings.TrimSpace(r.APIKey) == "" {
			return WebSearchStatus{
				Backend: string(r.Backend),
				Message: "Web search is disabled: Exa search needs an API key. Configure LCAgent web search in /settings.",
			}
		}
		return WebSearchStatus{Enabled: true, Backend: string(r.Backend)}
	case WebSearchBackendGoogle:
		if strings.TrimSpace(r.APIKey) == "" || strings.TrimSpace(r.SearchEngineID) == "" {
			return WebSearchStatus{
				Backend: string(r.Backend),
				Message: "Web search is disabled: Google search needs an API key and search engine ID. Configure LCAgent web search in /settings.",
			}
		}
		return WebSearchStatus{Enabled: true, Backend: string(r.Backend)}
	case WebSearchBackendSearXNG:
		if strings.TrimSpace(r.URL) == "" {
			return WebSearchStatus{
				Backend: string(r.Backend),
				Message: "Web search is disabled: SearXNG search needs a base URL. Configure LCAgent web search in /settings.",
			}
		}
		return WebSearchStatus{Enabled: true, Backend: string(r.Backend)}
	case WebSearchBackendBrowser:
		if r.Browser == nil {
			return WebSearchStatus{
				Backend: string(r.Backend),
				Message: "Web search is disabled: browser search needs managed browser control. Enable managed browser automation in /settings.",
			}
		}
		return WebSearchStatus{Enabled: true, Backend: string(r.Backend)}
	default:
		return WebSearchStatus{
			Backend: string(WebSearchBackendOff),
			Message: "Web search is disabled for LCAgent. Configure a web search backend in /settings to enable web_search.",
		}
	}
}

func (r WebSearchRunner) Search(ctx context.Context, query string, maxResults int, site string, recencyDays int) ToolResult {
	query = strings.TrimSpace(query)
	if query == "" {
		return ToolResult{Success: false, Error: "query is required"}
	}
	maxResults = clampInt(maxResults, defaultWebSearchMaxResults, maxWebSearchMaxResults)
	switch r.Backend {
	case WebSearchBackendExa:
		return r.searchExa(ctx, query, maxResults, site, recencyDays)
	case WebSearchBackendGoogle:
		return r.searchGoogle(ctx, query, maxResults, site, recencyDays)
	case WebSearchBackendSearXNG:
		return r.searchSearXNG(ctx, query, maxResults, site, recencyDays)
	case WebSearchBackendBrowser:
		if r.Browser == nil {
			return ToolResult{Success: false, Error: "browser web search is not configured"}
		}
		return r.Browser.SearchBrowser(ctx, query, maxResults, site, recencyDays)
	default:
		status := r.Status()
		return ToolResult{Success: false, Error: firstNonEmpty(status.Message, "web search is not configured")}
	}
}

func (r WebSearchRunner) searchExa(ctx context.Context, query string, maxResults int, site string, recencyDays int) ToolResult {
	body := map[string]any{
		"query":      query,
		"numResults": maxResults,
		"type":       "auto",
		"contents": map[string]any{
			"highlights": true,
			"summary":    true,
		},
	}
	if domain := cleanSearchDomain(site); domain != "" {
		body["includeDomains"] = []string{domain}
	}
	if recencyDays > 0 {
		body["startPublishedDate"] = time.Now().UTC().Add(-time.Duration(recencyDays) * 24 * time.Hour).Format(time.RFC3339)
	}
	endpoint := firstNonEmpty(r.ExaURL, "https://api.exa.ai/search")
	data, duration, err := r.postJSON(ctx, endpoint, body, map[string]string{"x-api-key": r.APIKey})
	if err != nil {
		return ToolResult{Success: false, Error: err.Error(), Duration: duration}
	}
	var parsed struct {
		Results []struct {
			Title         string   `json:"title"`
			URL           string   `json:"url"`
			PublishedDate string   `json:"publishedDate"`
			Author        string   `json:"author"`
			Text          string   `json:"text"`
			Highlights    []string `json:"highlights"`
			Summary       string   `json:"summary"`
		} `json:"results"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return ToolResult{Success: false, Error: "decode Exa search response: " + err.Error(), Duration: duration}
	}
	if strings.TrimSpace(parsed.Error) != "" {
		return ToolResult{Success: false, Error: parsed.Error, Duration: duration}
	}
	results := make([]webSearchResult, 0, minInt(len(parsed.Results), maxResults))
	for _, item := range parsed.Results {
		if len(results) >= maxResults {
			break
		}
		snippet := firstNonEmpty(item.Summary, firstString(item.Highlights), item.Text)
		source := firstNonEmpty(item.PublishedDate, item.Author)
		results = append(results, webSearchResult{Title: item.Title, URL: item.URL, Snippet: snippet, Source: source})
	}
	return formatWebSearchResults("exa", query, results, duration)
}

func (r WebSearchRunner) searchGoogle(ctx context.Context, query string, maxResults int, site string, recencyDays int) ToolResult {
	endpoint, err := url.Parse("https://www.googleapis.com/customsearch/v1")
	if err != nil {
		return ToolResult{Success: false, Error: err.Error()}
	}
	q := endpoint.Query()
	q.Set("key", r.APIKey)
	q.Set("cx", r.SearchEngineID)
	q.Set("q", webSearchQuery(query, site))
	q.Set("num", strconv.Itoa(maxResults))
	if recencyDays > 0 {
		q.Set("dateRestrict", "d"+strconv.Itoa(recencyDays))
	}
	endpoint.RawQuery = q.Encode()
	data, duration, err := r.getJSON(ctx, endpoint.String())
	if err != nil {
		return ToolResult{Success: false, Error: err.Error(), Duration: duration}
	}
	var parsed struct {
		Items []struct {
			Title   string `json:"title"`
			Link    string `json:"link"`
			Snippet string `json:"snippet"`
		} `json:"items"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return ToolResult{Success: false, Error: "decode Google search response: " + err.Error(), Duration: duration}
	}
	if parsed.Error != nil && strings.TrimSpace(parsed.Error.Message) != "" {
		return ToolResult{Success: false, Error: parsed.Error.Message, Duration: duration}
	}
	results := make([]webSearchResult, 0, len(parsed.Items))
	for _, item := range parsed.Items {
		results = append(results, webSearchResult{Title: item.Title, URL: item.Link, Snippet: item.Snippet})
	}
	return formatWebSearchResults("google", query, results, duration)
}

func (r WebSearchRunner) searchSearXNG(ctx context.Context, query string, maxResults int, site string, recencyDays int) ToolResult {
	base, err := url.Parse(strings.TrimRight(strings.TrimSpace(r.URL), "/") + "/search")
	if err != nil {
		return ToolResult{Success: false, Error: "invalid SearXNG URL: " + err.Error()}
	}
	q := base.Query()
	q.Set("q", webSearchQuery(query, site))
	q.Set("format", "json")
	q.Set("language", "en")
	if recencyDays > 0 {
		q.Set("time_range", searxngTimeRange(recencyDays))
	}
	base.RawQuery = q.Encode()
	data, duration, err := r.getJSON(ctx, base.String())
	if err != nil {
		return ToolResult{Success: false, Error: err.Error(), Duration: duration}
	}
	var parsed struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
			Engine  string `json:"engine"`
		} `json:"results"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return ToolResult{Success: false, Error: "decode SearXNG search response: " + err.Error(), Duration: duration}
	}
	results := make([]webSearchResult, 0, minInt(len(parsed.Results), maxResults))
	for _, item := range parsed.Results {
		if len(results) >= maxResults {
			break
		}
		results = append(results, webSearchResult{Title: item.Title, URL: item.URL, Snippet: item.Content, Source: item.Engine})
	}
	return formatWebSearchResults("searxng", query, results, duration)
}

func (r WebSearchRunner) postJSON(ctx context.Context, rawURL string, body map[string]any, headers map[string]string) ([]byte, time.Duration, error) {
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(body); err != nil {
		return nil, 0, err
	}
	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, &buf)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "LittleControlRoom-lcagent/1")
	for key, value := range headers {
		if strings.TrimSpace(value) != "" {
			req.Header.Set(key, value)
		}
	}
	resp, err := r.HTTPClient.Do(req)
	duration := time.Since(start)
	if err != nil {
		return nil, duration, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
	if err != nil {
		return nil, duration, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, duration, fmt.Errorf("search request failed: HTTP %d: %s", resp.StatusCode, truncateString(strings.TrimSpace(string(data)), 500))
	}
	return data, duration, nil
}

func (r WebSearchRunner) getJSON(ctx context.Context, rawURL string) ([]byte, time.Duration, error) {
	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "LittleControlRoom-lcagent/1")
	resp, err := r.HTTPClient.Do(req)
	duration := time.Since(start)
	if err != nil {
		return nil, duration, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
	if err != nil {
		return nil, duration, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, duration, fmt.Errorf("search request failed: HTTP %d: %s", resp.StatusCode, truncateString(strings.TrimSpace(string(data)), 500))
	}
	return data, duration, nil
}

type webSearchResult struct {
	Title   string
	URL     string
	Snippet string
	Source  string
}

func formatWebSearchResults(backend, query string, results []webSearchResult, duration time.Duration) ToolResult {
	var b strings.Builder
	fmt.Fprintf(&b, "backend: %s\n", backend)
	fmt.Fprintf(&b, "query: %s\n", query)
	fmt.Fprintf(&b, "results: %d\n\n", len(results))
	if len(results) == 0 {
		b.WriteString("No results.\n")
	}
	for i, result := range results {
		fmt.Fprintf(&b, "%d. %s\n", i+1, oneLine(result.Title))
		if strings.TrimSpace(result.URL) != "" {
			fmt.Fprintf(&b, "   url: %s\n", strings.TrimSpace(result.URL))
		}
		if strings.TrimSpace(result.Source) != "" {
			fmt.Fprintf(&b, "   source: %s\n", oneLine(result.Source))
		}
		if strings.TrimSpace(result.Snippet) != "" {
			fmt.Fprintf(&b, "   snippet: %s\n", oneLine(result.Snippet))
		}
	}
	output := b.String()
	truncated := false
	if len(output) > maxWebSearchOutputBytes {
		output = truncateString(output, maxWebSearchOutputBytes)
		truncated = true
	}
	return ToolResult{Success: true, Output: output, Duration: duration, Truncated: truncated}
}

func parseWebSearchBackend(raw string) WebSearchBackend {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "exa", "exa.ai":
		return WebSearchBackendExa
	case "google":
		return WebSearchBackendGoogle
	case "searxng", "searx":
		return WebSearchBackendSearXNG
	case "browser", "google-browser", "chrome", "chrome-browser":
		return WebSearchBackendBrowser
	default:
		return WebSearchBackendOff
	}
}

func webSearchAPIKey(backend WebSearchBackend, configured string) string {
	switch backend {
	case WebSearchBackendExa:
		return firstNonEmpty(configured, os.Getenv("EXA_API_KEY"))
	case WebSearchBackendGoogle:
		return firstNonEmpty(configured, os.Getenv("GOOGLE_SEARCH_API_KEY"), os.Getenv("GOOGLE_API_KEY"))
	default:
		return strings.TrimSpace(configured)
	}
}

func webSearchQuery(query, site string) string {
	site = strings.TrimSpace(site)
	if site == "" {
		return query
	}
	return query + " site:" + site
}

func cleanSearchDomain(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if parsed, err := url.Parse(raw); err == nil && parsed.Host != "" {
		raw = parsed.Host
	}
	raw = strings.TrimPrefix(raw, "www.")
	if slash := strings.Index(raw, "/"); slash >= 0 {
		raw = raw[:slash]
	}
	return strings.TrimSpace(raw)
}

func firstString(values []string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func searxngTimeRange(days int) string {
	switch {
	case days <= 1:
		return "day"
	case days <= 7:
		return "week"
	case days <= 31:
		return "month"
	default:
		return "year"
	}
}

func oneLine(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func truncateString(value string, limit int) string {
	if limit <= 0 || len(value) <= limit {
		return value
	}
	if limit <= 20 {
		return value[:limit]
	}
	return value[:limit-14] + "\n--- truncated ---"
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func loadEnvFileIfPresent(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		if key != "" && os.Getenv(key) == "" {
			_ = os.Setenv(key, value)
		}
	}
	return nil
}
