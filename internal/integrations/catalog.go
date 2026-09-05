package integrations

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type CatalogItem struct {
	Name          string `json:"name"`
	Kind          string `json:"kind"`
	Source        string `json:"source"`
	Plugin        string `json:"plugin,omitempty"`
	GitURL        string `json:"git_url,omitempty"`
	GitRef        string `json:"git_ref,omitempty"`
	Subdirectory  string `json:"subdirectory,omitempty"`
	Installed     bool   `json:"installed,omitempty"`
	Enabled       bool   `json:"enabled,omitempty"`
	Compatibility string `json:"compatibility"`
}

type CatalogReader interface {
	Catalog(context.Context, string, string, string, int) ([]CatalogItem, bool, error)
}

func (m *Manager) Catalog(ctx context.Context, provider, kind, query string, limit int) ([]CatalogItem, bool, error) {
	if _, err := ValidateTarget(Target{Provider: provider, Scope: "user"}); err != nil {
		return nil, false, err
	}
	if limit < 1 || limit > 50 {
		return nil, false, fmt.Errorf("limit must be 1 through 50")
	}
	items := []CatalogItem{}
	switch kind {
	case "skill":
		ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		request, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/repos/openai/skills/contents/skills/.curated", nil)
		request.Header.Set("Accept", "application/vnd.github+json")
		request.Header.Set("User-Agent", "Little-Control-Room")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			return nil, false, fmt.Errorf("curated skill catalog could not be reached")
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return nil, false, fmt.Errorf("curated skill catalog returned HTTP %d", response.StatusCode)
		}
		raw, err := readBounded(response.Body, 2<<20)
		if err != nil {
			return nil, false, err
		}
		var entries []struct {
			Name string `json:"name"`
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &entries); err != nil {
			return nil, false, fmt.Errorf("invalid curated skill catalog response")
		}
		for _, entry := range entries {
			if entry.Type != "dir" || !validName(entry.Name) {
				continue
			}
			items = append(items, CatalogItem{Name: entry.Name, Kind: "skill", Source: "openai/skills curated", GitURL: "https://github.com/openai/skills.git", GitRef: "main", Subdirectory: "skills/.curated/" + entry.Name, Compatibility: "Review the skill's tool and runtime dependencies for the chosen agent before installation."})
		}
	case "plugin":
		if provider != "codex" {
			return nil, false, fmt.Errorf("native plugin catalog discovery is currently available for Codex; Claude Code accepts an exact name@marketplace selector")
		}
		roots, err := m.roots()
		if err != nil {
			return nil, false, err
		}
		s := &scan{manager: m, roots: roots, inventory: Inventory{Target: Target{Provider: provider, Scope: "user"}}}
		raw, err := s.run(ctx, "codex", []string{"plugin", "list", "--available", "--json"}, roots.HomeDir)
		if err != nil {
			return nil, false, err
		}
		type plugin struct {
			Name        string `json:"name"`
			Marketplace string `json:"marketplaceName"`
			Installed   bool   `json:"installed"`
			Enabled     bool   `json:"enabled"`
		}
		var catalog struct {
			Installed []plugin `json:"installed"`
			Available []plugin `json:"available"`
		}
		if err := json.Unmarshal(raw, &catalog); err != nil {
			return nil, false, fmt.Errorf("native Codex plugin catalog returned an unsupported response")
		}
		seen := map[string]bool{}
		for _, entry := range append(catalog.Installed, catalog.Available...) {
			selector := entry.Name + "@" + entry.Marketplace
			if seen[selector] || !validPluginSelector(selector) {
				continue
			}
			seen[selector] = true
			items = append(items, CatalogItem{Name: entry.Name, Kind: "plugin", Source: entry.Marketplace, Plugin: selector, Installed: entry.Installed, Enabled: entry.Enabled, Compatibility: "Native Codex plugin; bundled connectors may need authentication."})
		}
	default:
		return nil, false, fmt.Errorf("kind must be skill or plugin; MCP servers are added from a documented command or URL")
	}
	filtered := []CatalogItem{}
	query = strings.ToLower(strings.TrimSpace(query))
	truncated := false
	for _, item := range items {
		if query != "" && !strings.Contains(strings.ToLower(item.Name+" "+item.Source), query) {
			continue
		}
		if len(filtered) >= limit {
			truncated = true
			break
		}
		filtered = append(filtered, item)
	}
	return filtered, truncated, nil
}
