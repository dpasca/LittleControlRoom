package lcagent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"lcroom/internal/lcagent/modeladapter"
	"lcroom/internal/lcagent/script"
)

const (
	defaultUtilityProvider = "openrouter"
	defaultUtilityModel    = "deepseek/deepseek-v4-flash"
)

type searchRefineProfile struct {
	Enabled     bool
	Provider    string
	Model       string
	MinBytes    int
	Message     string
	Refiner     script.SearchRefiner
	DisabledErr error
}

type utilitySearchRefiner struct {
	provider string
	client   *modeladapter.Client
}

type searchRefinePayload struct {
	Summary               string                        `json:"summary"`
	LikelyRelevant        []searchRefineCandidate       `json:"likely_relevant"`
	SuggestedNextSearches []searchRefineSuggestedSearch `json:"suggested_next_searches"`
	DiscardNotes          []string                      `json:"discard_notes"`
}

type searchRefineCandidate struct {
	Path       string `json:"path"`
	Line       string `json:"line"`
	Reason     string `json:"reason"`
	Confidence string `json:"confidence"`
}

type searchRefineSuggestedSearch struct {
	Query    string `json:"query"`
	Path     string `json:"path"`
	FileGlob string `json:"file_glob"`
	Intent   string `json:"intent"`
}

func newSearchRefineProfile(provider string, cfg modeladapter.OpenRouterConfig, minBytes int) searchRefineProfile {
	if minBytes <= 0 {
		minBytes = script.DefaultSearchRefineMinBytes
	}
	provider, err := normalizeUtilityProvider(provider)
	if err != nil {
		return searchRefineProfile{
			Enabled:     false,
			Provider:    strings.TrimSpace(provider),
			MinBytes:    minBytes,
			Message:     err.Error(),
			DisabledErr: err,
		}
	}
	if provider == "off" {
		return searchRefineProfile{
			Enabled:  false,
			Provider: provider,
			MinBytes: minBytes,
			Message:  "LCAgent search refinement disabled.",
		}
	}
	cfg.Model = firstNonEmptyString(strings.TrimSpace(cfg.Model), defaultUtilityModelForProvider(provider))
	client, err := newChatProviderClient(provider, cfg)
	if err != nil {
		return searchRefineProfile{
			Enabled:     false,
			Provider:    provider,
			Model:       cfg.Model,
			MinBytes:    minBytes,
			Message:     "LCAgent search refinement unavailable: " + err.Error(),
			DisabledErr: err,
		}
	}
	return searchRefineProfile{
		Enabled:  true,
		Provider: provider,
		Model:    client.Model(),
		MinBytes: minBytes,
		Message:  "LCAgent search refinement enabled.",
		Refiner:  utilitySearchRefiner{provider: provider, client: client},
	}
}

func normalizeUtilityProvider(raw string) (string, error) {
	value := strings.ToLower(strings.TrimSpace(raw))
	if value == "" {
		return defaultUtilityProvider, nil
	}
	switch value {
	case "off", "openrouter", "openai", "deepseek", "moonshot":
		return value, nil
	default:
		return "", fmt.Errorf("utility provider must be one of: off, openrouter, openai, deepseek, moonshot")
	}
}

func defaultUtilityModelForProvider(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "deepseek":
		return "deepseek-v4-flash"
	case "openai":
		return "gpt-5.4-mini"
	case "moonshot":
		return modeladapter.DefaultMoonshotModel
	default:
		return defaultUtilityModel
	}
}

func (r utilitySearchRefiner) RefineSearch(ctx context.Context, req script.SearchRefineRequest) (script.SearchRefineResult, error) {
	if r.client == nil {
		return script.SearchRefineResult{}, fmt.Errorf("search refiner client is not configured")
	}
	messages := []modeladapter.Message{
		{Role: "system", Content: searchRefinerSystemPrompt()},
		{Role: "user", Content: searchRefinerUserPrompt(req)},
	}
	options := modeladapter.CompletionOptions{
		MaxCompletionTokens: 1400,
	}
	if !strings.EqualFold(r.provider, "openai") {
		options.DisableThinking = true
	}
	completion, err := r.client.CompleteWithOptions(ctx, messages, nil, options)
	if err != nil {
		return script.SearchRefineResult{}, err
	}
	payload, err := parseSearchRefinePayload(completion.Message.Content)
	if err != nil {
		return script.SearchRefineResult{}, err
	}
	output := formatSearchRefineOutput(req, payload, r.provider, completion.Model)
	return script.SearchRefineResult{
		Output:       output,
		Provider:     r.provider,
		Model:        firstNonEmptyString(strings.TrimSpace(completion.Model), r.client.Model()),
		Usage:        append(json.RawMessage(nil), completion.Usage...),
		UsageSummary: completion.UsageSummary,
	}, nil
}

func searchRefinerSystemPrompt() string {
	return strings.Join([]string{
		"You are a search-result condenser for a coding agent.",
		"Input is a deterministic literal-substring search result plus the user's search intent.",
		"Return only JSON. Do not use markdown.",
		"Rank files and line ranges likely to help the main agent decide what to read next.",
		"Do not invent files, line numbers, APIs, or conclusions that are not present in the search result.",
		"Keep this advisory: the main agent must still read files before making final claims.",
		"The JSON shape is:",
		`{"summary":"short task-focused summary","likely_relevant":[{"path":"relative/or/absolute/path","line":"12 or 12-18","reason":"why this match is relevant","confidence":"high|medium|low"}],"suggested_next_searches":[{"query":"literal substring","path":"optional path","file_glob":"optional glob","intent":"why this narrower search helps"}],"discard_notes":["short notes about large low-value clusters"]}`,
	}, "\n")
}

func searchRefinerUserPrompt(req script.SearchRefineRequest) string {
	var b strings.Builder
	fmt.Fprintf(&b, "query: %s\n", strings.TrimSpace(req.Query))
	if intent := strings.TrimSpace(req.Intent); intent != "" {
		fmt.Fprintf(&b, "intent: %s\n", intent)
	} else {
		fmt.Fprintf(&b, "intent: Rank matches that are most useful for understanding this query in the current coding task.\n")
	}
	if path := strings.TrimSpace(req.Path); path != "" {
		fmt.Fprintf(&b, "path: %s\n", path)
	}
	if fileGlob := strings.TrimSpace(req.FileGlob); fileGlob != "" {
		fmt.Fprintf(&b, "file_glob: %s\n", fileGlob)
	}
	fmt.Fprintf(&b, "max_matches: %d\n", req.MaxMatches)
	fmt.Fprintf(&b, "original_output_bytes: %d\n", req.OriginalOutputBytes)
	fmt.Fprintf(&b, "compact_output_bytes: %d\n", req.CompactOutputBytes)
	fmt.Fprintf(&b, "truncated: %t\n\n", req.Truncated)
	b.WriteString("search_output:\n")
	b.WriteString(strings.TrimSpace(req.SearchOutput))
	b.WriteByte('\n')
	return b.String()
}

func parseSearchRefinePayload(content string) (searchRefinePayload, error) {
	content = strings.TrimSpace(content)
	if strings.HasPrefix(content, "```") {
		content = strings.TrimPrefix(content, "```json")
		content = strings.TrimPrefix(content, "```")
		content = strings.TrimSuffix(content, "```")
		content = strings.TrimSpace(content)
	}
	var payload searchRefinePayload
	if err := json.Unmarshal([]byte(content), &payload); err == nil {
		return payload, nil
	}
	start := strings.Index(content, "{")
	end := strings.LastIndex(content, "}")
	if start >= 0 && end > start {
		if err := json.Unmarshal([]byte(content[start:end+1]), &payload); err == nil {
			return payload, nil
		}
	}
	return searchRefinePayload{}, fmt.Errorf("search refiner returned invalid JSON")
}

func formatSearchRefineOutput(req script.SearchRefineRequest, payload searchRefinePayload, provider, model string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "search_refined: true\n")
	fmt.Fprintf(&b, "provider: %s\n", strings.TrimSpace(provider))
	fmt.Fprintf(&b, "model: %s\n", strings.TrimSpace(model))
	fmt.Fprintf(&b, "query: %s\n", strings.TrimSpace(req.Query))
	if intent := strings.TrimSpace(req.Intent); intent != "" {
		fmt.Fprintf(&b, "intent: %s\n", intent)
	}
	fmt.Fprintf(&b, "original_output_bytes: %d\n", req.OriginalOutputBytes)
	fmt.Fprintf(&b, "compact_output_bytes: %d\n", req.CompactOutputBytes)
	fmt.Fprintf(&b, "evidence_note: This is a routing summary from a utility model; use read_file on relevant ranges before making final claims.\n")
	if summary := strings.TrimSpace(payload.Summary); summary != "" {
		fmt.Fprintf(&b, "\nsummary: %s\n", summary)
	}
	b.WriteString("\nlikely_relevant:\n")
	if len(payload.LikelyRelevant) == 0 {
		b.WriteString("- none\n")
	} else {
		for _, candidate := range payload.LikelyRelevant {
			path := strings.TrimSpace(candidate.Path)
			line := strings.TrimSpace(candidate.Line)
			reason := strings.TrimSpace(candidate.Reason)
			confidence := strings.TrimSpace(candidate.Confidence)
			if path == "" {
				continue
			}
			if confidence == "" {
				confidence = "unknown"
			}
			if line != "" {
				fmt.Fprintf(&b, "- %s:%s confidence=%s", path, line, confidence)
			} else {
				fmt.Fprintf(&b, "- %s confidence=%s", path, confidence)
			}
			if reason != "" {
				fmt.Fprintf(&b, " reason=%s", reason)
			}
			b.WriteByte('\n')
		}
	}
	if len(payload.SuggestedNextSearches) > 0 {
		b.WriteString("\nsuggested_next_searches:\n")
		for _, suggested := range payload.SuggestedNextSearches {
			query := strings.TrimSpace(suggested.Query)
			if query == "" {
				continue
			}
			fmt.Fprintf(&b, "- query=%q", query)
			if path := strings.TrimSpace(suggested.Path); path != "" {
				fmt.Fprintf(&b, " path=%q", path)
			}
			if fileGlob := strings.TrimSpace(suggested.FileGlob); fileGlob != "" {
				fmt.Fprintf(&b, " file_glob=%q", fileGlob)
			}
			if intent := strings.TrimSpace(suggested.Intent); intent != "" {
				fmt.Fprintf(&b, " intent=%q", intent)
			}
			b.WriteByte('\n')
		}
	}
	if len(payload.DiscardNotes) > 0 {
		b.WriteString("\ndiscard_notes:\n")
		for _, note := range payload.DiscardNotes {
			if note = strings.TrimSpace(note); note != "" {
				fmt.Fprintf(&b, "- %s\n", note)
			}
		}
	}
	return b.String()
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
