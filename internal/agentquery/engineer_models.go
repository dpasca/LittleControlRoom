package agentquery

import (
	"context"
	"encoding/json"
	"fmt"

	"lcroom/internal/control"
)

func engineerModelsCapability() Capability {
	c := collectionCapability(QueryEngineerModels, DomainEngineer, ScopeProject,
		"Discover exact model and reasoning-effort IDs for engineer launch controls. Catalogs are saved when the host opens a session or loads its model picker; source and observed_at identify the evidence. Provider listings do not guarantee current account access. Empty results mean discovery is unavailable: open a provider session or use select_model, never guess an ID.", SensitivityMetadata,
		objectSchema(map[string]any{
			"provider": map[string]any{"type": "string", "enum": control.EngineerProviderStrings(false)},
			"limit":    integerProperty("Maximum models to return.", 1, 50),
			"offset":   integerProperty("Zero-based model offset for the next page.", 0, 100000),
		}, []string{"provider"}))
	c.Freshness = "host_model_catalog_snapshot"
	return c
}

func (e *Executor) engineerModels(ctx context.Context, raw json.RawMessage) (map[string]any, error) {
	var args struct {
		Provider control.Provider `json:"provider"`
		Limit    int              `json:"limit"`
		Offset   int              `json:"offset"`
	}
	if err := decodeStrict(raw, &args); err != nil {
		return nil, err
	}
	valid := false
	for _, provider := range control.EngineerProviderValues() {
		valid = valid || args.Provider == provider
	}
	if !valid {
		return nil, fmt.Errorf("explicit engineer provider is required")
	}
	limit, err := boundedLimit(args.Limit, 50, 50)
	if err != nil {
		return nil, err
	}
	if args.Offset < 0 || args.Offset > 100000 {
		return nil, fmt.Errorf("invalid model offset")
	}
	reader, ok := e.reader.(interface {
		EngineerModelCatalog(context.Context, control.Provider) (control.EngineerModelCatalog, error)
	})
	if !ok {
		return nil, fmt.Errorf("engineer model catalog is not connected")
	}
	catalog, err := reader.EngineerModelCatalog(ctx, args.Provider)
	if err != nil {
		return nil, err
	}
	start := min(args.Offset, len(catalog.Models))
	end := min(start+limit, len(catalog.Models))
	return map[string]any{
		"provider": catalog.Provider, "source": catalog.Source, "observed_at": catalog.ObservedAt,
		"models": catalog.Models[start:end], "total": len(catalog.Models), "truncated": end < len(catalog.Models), "next_offset": end,
		"availability": "Discovery evidence only; provider/account availability is checked at launch. Omit model and reasoning_effort to preserve defaults.",
	}, nil
}
