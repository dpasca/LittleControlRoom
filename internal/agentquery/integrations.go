package agentquery

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"lcroom/internal/integrations"
)

func integrationListCapability() Capability {
	c := collectionCapability(QueryIntegrationsList, DomainIntegrations, ScopeProject,
		"Inspect agent-native skills, plugins and MCP configuration, including source, supported actions, revision, and activation limits. This is a disk inventory, not proof that a running session can call the tools. Credential values and MCP arguments are never returned.", SensitivityMetadata,
		pagedSchema(map[string]any{
			"provider":     map[string]any{"type": "string", "enum": []string{"codex", "claude_code", "opencode", "lcagent"}},
			"scope":        map[string]any{"type": "string", "enum": []string{"user", "project"}},
			"project_path": stringProperty("Required for project scope unless there is an originating project. Omit for user scope.", 1),
			"kind":         map[string]any{"type": "string", "enum": []string{"skill", "mcp", "plugin"}, "description": "Optional inventory filter."},
		}, []string{"provider", "scope"}))
	c.Freshness = "configuration_on_disk"
	return c
}

func integrationCatalogCapability() Capability {
	c := collectionCapability(QueryIntegrationsCatalog, DomainIntegrations, ScopePortfolio,
		"Find curated skill sources or native Codex marketplace plugins. Results describe installation sources, not compatibility guarantees. Use integrations.list before proposing installation.", SensitivityMetadata,
		objectSchema(map[string]any{
			"provider": map[string]any{"type": "string", "enum": []string{"codex", "claude_code", "opencode", "lcagent"}},
			"kind":     map[string]any{"type": "string", "enum": []string{"skill", "plugin"}},
			"query":    stringProperty("Optional literal name or marketplace filter.", 0),
			"limit":    integerProperty("Maximum matching entries; narrow query if truncated.", 1, 50),
		}, []string{"provider", "kind"}))
	c.Freshness = "catalog_fetch"
	return c
}

func (e *Executor) integrationList(ctx context.Context, raw json.RawMessage) (map[string]any, error) {
	if e.integrations == nil {
		return nil, fmt.Errorf("integration inventory is not connected to this LCR host")
	}
	var args struct {
		integrations.Target
		Kind   string `json:"kind"`
		Limit  int    `json:"limit"`
		Cursor string `json:"cursor"`
	}
	if err := decodeStrict(raw, &args); err != nil {
		return nil, err
	}
	if args.Scope == "project" {
		path, _, err := e.resolveProject(ctx, args.ProjectPath)
		if err != nil {
			return nil, err
		}
		args.ProjectPath = path
	} else if e.scope != ScopePortfolio {
		return nil, fmt.Errorf("user integration inventory requires portfolio scope")
	}
	if args.Kind != "" && args.Kind != "skill" && args.Kind != "mcp" && args.Kind != "plugin" {
		return nil, fmt.Errorf("unsupported integration kind")
	}
	limit, err := boundedLimit(args.Limit, 30, 50)
	if err != nil {
		return nil, err
	}
	inv, err := e.integrations.Inventory(ctx, args.Target)
	if err != nil {
		return nil, err
	}
	entries := []integrations.Entry{}
	for _, entry := range inv.Entries {
		if args.Kind == "" || entry.Kind == args.Kind {
			entries = append(entries, entry)
		}
	}
	offset := 0
	cursorRevision := inv.Revision + ":" + args.Kind
	if args.Cursor != "" {
		parts := strings.Split(args.Cursor, ":")
		if len(parts) != 3 {
			return nil, fmt.Errorf("invalid integration cursor")
		}
		offset, err = strconv.Atoi(parts[2])
		if err != nil || parts[0]+":"+parts[1] != cursorRevision || offset < 0 || offset > len(entries) {
			return nil, fmt.Errorf("integration cursor is stale; restart the query")
		}
	}
	end := min(offset+limit, len(entries))
	next := ""
	if end < len(entries) {
		next = fmt.Sprintf("%s:%d", cursorRevision, end)
	}
	return map[string]any{"target": inv.Target, "revision": inv.Revision, "scanned_at": inv.ScannedAt, "entries": entries[offset:end], "warnings": inv.Warnings, "activation": inv.Activation, "total": len(entries), "truncated": end < len(entries), "next_cursor": next}, nil
}

func (e *Executor) integrationCatalog(ctx context.Context, raw json.RawMessage) (map[string]any, error) {
	reader, ok := e.integrations.(integrations.CatalogReader)
	if !ok {
		return nil, fmt.Errorf("integration catalog discovery is not connected")
	}
	var args struct {
		Provider string `json:"provider"`
		Kind     string `json:"kind"`
		Query    string `json:"query"`
		Limit    int    `json:"limit"`
	}
	if err := decodeStrict(raw, &args); err != nil {
		return nil, err
	}
	limit, err := boundedLimit(args.Limit, 20, 50)
	if err != nil {
		return nil, err
	}
	items, truncated, err := reader.Catalog(ctx, args.Provider, args.Kind, args.Query, limit)
	if err != nil {
		return nil, err
	}
	return map[string]any{"entries": items, "truncated": truncated}, nil
}
