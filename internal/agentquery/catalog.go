package agentquery

import (
	"sort"
	"strings"
)

type Scope string

const (
	ScopeProject   Scope = "project"
	ScopePortfolio Scope = "portfolio"
)

func NormalizeScope(value string) Scope {
	switch Scope(strings.ToLower(strings.TrimSpace(value))) {
	case ScopeProject:
		return ScopeProject
	case ScopePortfolio:
		return ScopePortfolio
	default:
		return ""
	}
}

func ScopeAllows(available, required Scope) bool {
	rank := func(scope Scope) int {
		switch NormalizeScope(string(scope)) {
		case ScopeProject:
			return 1
		case ScopePortfolio:
			return 2
		default:
			return 0
		}
	}
	return rank(available) >= rank(required) && rank(required) > 0
}

type Domain string

const (
	DomainPortfolio     Domain = "portfolio"
	DomainProject       Domain = "project"
	DomainAssessment    Domain = "assessment"
	DomainWork          Domain = "work"
	DomainDemoRecording Domain = "demo_recording"
)

func NormalizeDomain(value string) Domain {
	switch Domain(strings.ToLower(strings.TrimSpace(value))) {
	case DomainPortfolio:
		return DomainPortfolio
	case DomainProject:
		return DomainProject
	case DomainAssessment:
		return DomainAssessment
	case DomainWork:
		return DomainWork
	case DomainDemoRecording:
		return DomainDemoRecording
	default:
		return ""
	}
}

type Name string

const (
	QueryPortfolioOverview   Name = "portfolio.overview"
	QueryProjectList         Name = "project.list"
	QueryProjectSearch       Name = "project.search"
	QueryProjectDetail       Name = "project.detail"
	QueryTodoList            Name = "project.todo_list"
	QuerySessionList         Name = "project.session_list"
	QueryAssessmentList      Name = "assessment.list"
	QueryAgentTaskList       Name = "work.agent_task_list"
	QueryAgentTaskGet        Name = "work.agent_task_get"
	QueryGoalRunList         Name = "work.goal_run_list"
	QueryGoalRunGet          Name = "work.goal_run_get"
	QueryDemoRecordingLatest Name = "demo_recording.latest"
)

type Sensitivity string

const (
	SensitivityMetadata Sensitivity = "metadata"
	SensitivityContent  Sensitivity = "content"
)

type Capability struct {
	Name         Name           `json:"name"`
	Domain       Domain         `json:"domain"`
	Scope        Scope          `json:"scope"`
	Description  string         `json:"description"`
	Sensitivity  Sensitivity    `json:"sensitivity"`
	Freshness    string         `json:"freshness"`
	InputSchema  map[string]any `json:"input_schema"`
	OutputSchema map[string]any `json:"output_schema"`
}

type CapabilitySummary struct {
	Name        Name        `json:"name"`
	Domain      Domain      `json:"domain"`
	Scope       Scope       `json:"scope"`
	Description string      `json:"description"`
	Sensitivity Sensitivity `json:"sensitivity"`
	Freshness   string      `json:"freshness"`
}

type DomainSummary struct {
	Domain      Domain `json:"domain"`
	Description string `json:"description"`
}

func DomainSummaries() []DomainSummary {
	return []DomainSummary{
		{Domain: DomainPortfolio, Description: "Cross-project inventory, attention, and activity summaries."},
		{Domain: DomainProject, Description: "Bounded project, TODO, and session state."},
		{Domain: DomainAssessment, Description: "Persisted session assessment state and summaries."},
		{Domain: DomainWork, Description: "Delegated agent tasks and durable goal runs."},
		{Domain: DomainDemoRecording, Description: "Active or recently finalized LCR demo recording metadata."},
	}
}

func DomainStrings() []string {
	summaries := DomainSummaries()
	values := make([]string, 0, len(summaries))
	for _, summary := range summaries {
		values = append(values, string(summary.Domain))
	}
	return values
}

func Capabilities() []Capability {
	return []Capability{
		collectionCapability(
			QueryPortfolioOverview,
			DomainPortfolio,
			ScopePortfolio,
			"Summarize the visible LCR portfolio and return the highest-attention projects.",
			SensitivityContent,
			objectSchema(map[string]any{
				"include_historical": booleanProperty("Include archived and out-of-scope projects. Defaults to false."),
				"attention_limit":    integerProperty("Maximum high-attention projects to return.", 1, 20),
			}, nil),
		),
		collectionCapability(
			QueryProjectList,
			DomainProject,
			ScopeProject,
			"List visible LCR projects in attention order. Project-scoped callers receive only their originating project.",
			SensitivityContent,
			pagedSchema(map[string]any{
				"include_historical": booleanProperty("Include archived and out-of-scope projects. Defaults to false."),
			}, nil),
		),
		collectionCapability(
			QueryProjectSearch,
			DomainProject,
			ScopePortfolio,
			"Search visible project names, paths, categories, and persisted summaries without searching transcripts.",
			SensitivityContent,
			pagedSchema(map[string]any{
				"query":              stringProperty("Case-insensitive text to find in project metadata.", 1),
				"include_historical": booleanProperty("Include archived and out-of-scope projects. Defaults to false."),
			}, []string{"query"}),
		),
		collectionCapability(
			QueryProjectDetail,
			DomainProject,
			ScopeProject,
			"Load one structured project snapshot with attention reasons, bounded TODOs, recent sessions, and the latest assessment.",
			SensitivityContent,
			objectSchema(map[string]any{
				"project_path":      stringProperty("Exact project path. Defaults to the originating project.", 1),
				"include_completed": booleanProperty("Include completed TODOs. Defaults to false."),
				"todo_limit":        integerProperty("Maximum TODOs to return.", 1, 50),
				"session_limit":     integerProperty("Maximum recent sessions to return.", 1, 30),
			}, nil),
		),
		collectionCapability(
			QueryTodoList,
			DomainProject,
			ScopeProject,
			"List bounded TODO state for one visible project.",
			SensitivityContent,
			pagedSchema(map[string]any{
				"project_path":      stringProperty("Exact project path. Defaults to the originating project.", 1),
				"include_completed": booleanProperty("Include completed TODOs. Defaults to false."),
			}, nil),
		),
		collectionCapability(
			QuerySessionList,
			DomainProject,
			ScopeProject,
			"List bounded persisted session metadata for one visible project without transcript text or artifact paths.",
			SensitivityMetadata,
			pagedSchema(map[string]any{
				"project_path": stringProperty("Exact project path. Defaults to the originating project.", 1),
			}, nil),
		),
		collectionCapability(
			QueryAssessmentList,
			DomainAssessment,
			ScopeProject,
			"List bounded persisted session assessments. Portfolio callers may omit project_path to inspect visible projects.",
			SensitivityContent,
			pagedSchema(map[string]any{
				"project_path":       stringProperty("Exact project path. Project-scoped callers default to their originating project.", 1),
				"session_id":         stringProperty("Optional exact canonical or provider session id.", 1),
				"include_historical": booleanProperty("Include assessments from archived and out-of-scope projects. Defaults to false."),
			}, nil),
		),
		collectionCapability(
			QueryAgentTaskList,
			DomainWork,
			ScopePortfolio,
			"List bounded delegated agent-task state and resource references.",
			SensitivityContent,
			pagedSchema(map[string]any{
				"include_historical": booleanProperty("Include completed and archived tasks. Defaults to false."),
			}, nil),
		),
		collectionCapability(
			QueryAgentTaskGet,
			DomainWork,
			ScopePortfolio,
			"Load one delegated agent task by exact id.",
			SensitivityContent,
			objectSchema(map[string]any{
				"task_id": stringProperty("Exact delegated agent task id.", 1),
			}, []string{"task_id"}),
		),
		collectionCapability(
			QueryGoalRunList,
			DomainWork,
			ScopePortfolio,
			"List bounded durable LCR goal-run summaries.",
			SensitivityContent,
			pagedSchema(nil, nil),
		),
		collectionCapability(
			QueryGoalRunGet,
			DomainWork,
			ScopePortfolio,
			"Load one durable LCR goal run with a bounded execution trace.",
			SensitivityContent,
			objectSchema(map[string]any{
				"run_id":      stringProperty("Exact goal-run id.", 1),
				"trace_limit": integerProperty("Maximum newest trace entries to return.", 1, 50),
			}, []string{"run_id"}),
		),
		collectionCapability(
			QueryDemoRecordingLatest,
			DomainDemoRecording,
			ScopeProject,
			"Return the active LCR demo recording, or the latest finalized package. The package path is conditional on portfolio authority or a host-provided attachment/confirmation grant; other callers receive sanitized resource metadata.",
			SensitivityMetadata,
			objectSchema(nil, nil),
		),
	}
}

func CapabilityByName(name Name) (Capability, bool) {
	normalized := Name(strings.TrimSpace(string(name)))
	for _, capability := range Capabilities() {
		if capability.Name == normalized {
			return capability, true
		}
	}
	return Capability{}, false
}

func CapabilitySummaries(domain Domain, scope Scope) []CapabilitySummary {
	domain = NormalizeDomain(string(domain))
	scope = NormalizeScope(string(scope))
	summaries := make([]CapabilitySummary, 0, len(Capabilities()))
	for _, capability := range Capabilities() {
		if domain != "" && capability.Domain != domain {
			continue
		}
		if !ScopeAllows(scope, capability.Scope) {
			continue
		}
		summaries = append(summaries, CapabilitySummary{
			Name:        capability.Name,
			Domain:      capability.Domain,
			Scope:       capability.Scope,
			Description: capability.Description,
			Sensitivity: capability.Sensitivity,
			Freshness:   capability.Freshness,
		})
	}
	sort.Slice(summaries, func(i, j int) bool {
		if summaries[i].Domain != summaries[j].Domain {
			return summaries[i].Domain < summaries[j].Domain
		}
		return summaries[i].Name < summaries[j].Name
	})
	return summaries
}

func collectionCapability(name Name, domain Domain, scope Scope, description string, sensitivity Sensitivity, input map[string]any) Capability {
	return Capability{
		Name:         name,
		Domain:       domain,
		Scope:        scope,
		Description:  description,
		Sensitivity:  sensitivity,
		Freshness:    "persisted_snapshot",
		InputSchema:  input,
		OutputSchema: queryOutputSchema(),
	}
}

func pagedSchema(properties map[string]any, required []string) map[string]any {
	if properties == nil {
		properties = map[string]any{}
	}
	properties["limit"] = integerProperty("Maximum records to return. Defaults to 20 and is capped at 50.", 1, 50)
	properties["cursor"] = stringProperty("Opaque continuation cursor from a previous result.", 1)
	return objectSchema(properties, required)
}

func objectSchema(properties map[string]any, required []string) map[string]any {
	if properties == nil {
		properties = map[string]any{}
	}
	schema := map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties":           properties,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func stringProperty(description string, minLength int) map[string]any {
	property := map[string]any{"type": "string", "description": description}
	if minLength > 0 {
		property["minLength"] = minLength
	}
	return property
}

func integerProperty(description string, minimum, maximum int) map[string]any {
	return map[string]any{
		"type":        "integer",
		"description": description,
		"minimum":     minimum,
		"maximum":     maximum,
	}
}

func booleanProperty(description string) map[string]any {
	return map[string]any{"type": "boolean", "description": description}
}

func queryOutputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"description":          "Common result envelope. Query-specific bounded payload fields are additional properties documented by the query description.",
		"additionalProperties": true,
		"properties": map[string]any{
			"success":             map[string]any{"type": "boolean"},
			"query":               map[string]any{"type": "string"},
			"as_of":               map[string]any{"type": "string"},
			"freshness":           map[string]any{"type": "string", "enum": []string{"persisted_snapshot"}},
			"scope":               map[string]any{"type": "string", "enum": []string{"project", "portfolio"}},
			"origin_project_path": map[string]any{"type": "string"},
			"privacy_filter":      map[string]any{"type": "string"},
			"truncated":           map[string]any{"type": "boolean"},
			"next_cursor":         map[string]any{"type": "string"},
		},
		"required": []string{"success", "query", "as_of", "freshness", "scope", "origin_project_path", "privacy_filter", "truncated"},
	}
}
