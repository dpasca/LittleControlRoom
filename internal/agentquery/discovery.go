package agentquery

import (
	"fmt"
	"strings"
)

const (
	PrivacyContract   = "Private-category projects and tasks are hidden except when they are the originating project. Help Chat transcripts, raw event payloads, and arbitrary repository files are not available through this catalog."
	FreshnessContract = "Query results are bounded persisted snapshots and include an as_of timestamp; they do not claim to mirror transient in-memory TUI state."
)

func ListReport(domainRaw string, scope Scope, available bool) (map[string]any, error) {
	domain := NormalizeDomain(domainRaw)
	if strings.TrimSpace(domainRaw) != "" && domain == "" {
		return nil, fmt.Errorf("unsupported LCR query domain")
	}
	queries := []CapabilitySummary{}
	nextStep := "Call list_lcr_queries again with one exact domain to load its compact query summaries."
	if domain != "" {
		queries = CapabilitySummaries(domain, scope)
		nextStep = "Call describe_lcr_query with one exact query name before running it."
	}
	return map[string]any{
		"success":            true,
		"query_scope":        NormalizeScope(string(scope)),
		"queries_available":  available,
		"domains":            DomainSummaries(),
		"queries":            queries,
		"next_step":          nextStep,
		"privacy_contract":   PrivacyContract,
		"freshness_contract": FreshnessContract,
	}, nil
}

func DescribeReport(nameRaw string, scope Scope, executionTool string) (map[string]any, error) {
	capability, ok := CapabilityByName(Name(strings.TrimSpace(nameRaw)))
	if !ok {
		return nil, fmt.Errorf("unknown LCR query")
	}
	if !ScopeAllows(scope, capability.Scope) {
		return nil, fmt.Errorf("LCR query %q requires %s scope; this embedded session has %s scope", capability.Name, capability.Scope, NormalizeScope(string(scope)))
	}
	return map[string]any{
		"success": true,
		"query":   capability,
		"execution": map[string]any{
			"tool": strings.TrimSpace(executionTool),
			"arguments": map[string]any{
				"query":     capability.Name,
				"arguments": "Use an object matching query.input_schema.",
			},
		},
	}, nil
}
