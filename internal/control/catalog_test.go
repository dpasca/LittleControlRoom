package control

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCapabilityCatalogScopesAndDomains(t *testing.T) {
	project, ok := CapabilityByName(CapabilityTodoAdd)
	if !ok {
		t.Fatal("todo.add capability missing")
	}
	if project.Domain != CapabilityDomainTodo || project.Scope != AuthorityScopeProject {
		t.Fatalf("todo.add metadata = domain %q scope %q", project.Domain, project.Scope)
	}
	portfolio, ok := CapabilityByName(CapabilityProjectCreateAndStartEngineer)
	if !ok {
		t.Fatal("project.create_and_start_engineer capability missing")
	}
	if portfolio.Domain != CapabilityDomainProject || portfolio.Scope != AuthorityScopePortfolio || !portfolio.Async {
		t.Fatalf("project creation metadata = %#v", portfolio)
	}
	host, ok := CapabilityByName(CapabilitySettingsUpdate)
	if !ok {
		t.Fatal("settings.update capability missing")
	}
	if host.Scope != AuthorityScopeHost {
		t.Fatalf("settings.update scope = %q, want host", host.Scope)
	}

	summaries := CapabilitySummaries("", AuthorityScopePortfolio)
	if !summaryContainsCapability(summaries, CapabilityTodoAdd) ||
		!summaryContainsCapability(summaries, CapabilityProjectCreateAndStartEngineer) {
		t.Fatalf("portfolio summaries = %#v, want project and portfolio capabilities", summaries)
	}
	if summaryContainsCapability(summaries, CapabilitySettingsUpdate) {
		t.Fatalf("portfolio summaries expose host capability: %#v", summaries)
	}
}

func TestAuthorityAllowsUsesExplicitHierarchy(t *testing.T) {
	if !AuthorityAllows(AuthorityScopePortfolio, AuthorityScopeProject) {
		t.Fatal("portfolio authority should include project capabilities")
	}
	if AuthorityAllows(AuthorityScopeProject, AuthorityScopePortfolio) {
		t.Fatal("project authority must not include portfolio capabilities")
	}
	if !AuthorityAllows(AuthorityScopeHost, AuthorityScopePortfolio) {
		t.Fatal("host authority should include portfolio capabilities")
	}
	if AuthorityAllows("", AuthorityScopeProject) {
		t.Fatal("empty authority must not allow capabilities")
	}
}

func TestValidateInvocationRejectsArgumentsOutsideCapabilitySchema(t *testing.T) {
	_, err := ValidateInvocation(Invocation{
		RequestID:  "request-1",
		Capability: CapabilityTodoAdd,
		Args: json.RawMessage(`{
			"request_id":"request-1",
			"project_path":"/tmp/demo",
			"text":"Document the control surface",
			"unexpected":"must not be silently ignored"
		}`),
	})
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("ValidateInvocation() error = %v, want unknown-field rejection", err)
	}
}

func summaryContainsCapability(summaries []CapabilitySummary, name CapabilityName) bool {
	for _, summary := range summaries {
		if summary.Name == name {
			return true
		}
	}
	return false
}
