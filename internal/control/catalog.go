package control

import (
	"sort"
	"strings"
)

type CapabilitySummary struct {
	Name         CapabilityName     `json:"name"`
	Domain       CapabilityDomain   `json:"domain"`
	Scope        AuthorityScope     `json:"scope"`
	Description  string             `json:"description"`
	Risk         RiskLevel          `json:"risk"`
	Confirmation ConfirmationPolicy `json:"confirmation"`
	RequiresHost bool               `json:"requires_host"`
	Async        bool               `json:"async"`
}

type DomainSummary struct {
	Domain      CapabilityDomain `json:"domain"`
	Description string           `json:"description"`
}

func DomainSummaries() []DomainSummary {
	return []DomainSummary{
		{Domain: CapabilityDomainEngineer, Description: "Embedded engineer session handoffs and continuation."},
		{Domain: CapabilityDomainTask, Description: "Temporary delegated agent tasks and their lifecycle."},
		{Domain: CapabilityDomainProject, Description: "Repository creation, registration, organization, and archive state."},
		{Domain: CapabilityDomainTodo, Description: "Project TODO capture, completion, worktrees, and tracked engineer work."},
		{Domain: CapabilityDomainSettings, Description: "Little Control Room application settings."},
		{Domain: CapabilityDomainGit, Description: "Git review and operator-confirmed commit preparation."},
	}
}

func CapabilitySummaries(domain CapabilityDomain, authority AuthorityScope) []CapabilitySummary {
	domain = NormalizeCapabilityDomain(string(domain))
	authority = NormalizeAuthorityScope(string(authority))
	out := make([]CapabilitySummary, 0, len(CapabilityNameValues()))
	for _, capability := range Capabilities() {
		if domain != "" && capability.Domain != domain {
			continue
		}
		if !AuthorityAllows(authority, capability.Scope) {
			continue
		}
		out = append(out, CapabilitySummary{
			Name:         capability.Name,
			Domain:       capability.Domain,
			Scope:        capability.Scope,
			Description:  strings.TrimSpace(capability.Description),
			Risk:         capability.Risk,
			Confirmation: capability.Confirmation,
			RequiresHost: capability.RequiresHost,
			Async:        capability.Async,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Domain != out[j].Domain {
			return out[i].Domain < out[j].Domain
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func capabilityWithCatalogMetadata(capability Capability) Capability {
	switch capability.Name {
	case CapabilityEngineerSendPrompt:
		capability.Domain = CapabilityDomainEngineer
		capability.Scope = AuthorityScopeProject
		capability.Async = true
	case CapabilityAgentTaskCreate, CapabilityAgentTaskContinue:
		capability.Domain = CapabilityDomainTask
		capability.Scope = AuthorityScopePortfolio
		capability.Async = true
	case CapabilityAgentTaskClose:
		capability.Domain = CapabilityDomainTask
		capability.Scope = AuthorityScopePortfolio
	case CapabilityProjectCreateAndStartEngineer:
		capability.Domain = CapabilityDomainProject
		capability.Scope = AuthorityScopePortfolio
		capability.Async = true
	case CapabilityProjectSetCategory, CapabilityProjectArchive, CapabilityScratchTaskArchive:
		capability.Domain = CapabilityDomainProject
		capability.Scope = AuthorityScopePortfolio
	case CapabilityTodoCreateWorktreeAndStartEngineer:
		capability.Domain = CapabilityDomainTodo
		capability.Scope = AuthorityScopeProject
		capability.Async = true
	case CapabilityTodoAdd, CapabilityTodoComplete:
		capability.Domain = CapabilityDomainTodo
		capability.Scope = AuthorityScopeProject
	case CapabilitySettingsUpdate:
		capability.Domain = CapabilityDomainSettings
		capability.Scope = AuthorityScopeHost
	case CapabilityGitPrepareCommit:
		capability.Domain = CapabilityDomainGit
		capability.Scope = AuthorityScopeProject
	}
	return capability
}
