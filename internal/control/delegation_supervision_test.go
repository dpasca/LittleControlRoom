package control

import (
	"encoding/json"
	"testing"
)

func TestMaxCorrectionsIsBoundedAndRequiresStructuredResults(t *testing.T) {
	base := AgentTaskCreateInput{Title: "worker", Kind: AgentTaskKindAgent, Provider: ProviderCodex}
	cases := []struct {
		name       string
		structured bool
		max        int
		wantErr    bool
	}{
		{name: "no grant by default", structured: false, max: 0},
		{name: "grant with structured results", structured: true, max: MaxSupervisedCorrections},
		{name: "grant without structured results", structured: false, max: 1, wantErr: true},
		{name: "negative grant", structured: true, max: -1, wantErr: true},
		{name: "grant beyond the product bound", structured: true, max: MaxSupervisedCorrections + 1, wantErr: true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			input := base
			input.StructuredResults, input.MaxCorrections = testCase.structured, testCase.max
			normalized, err := NormalizeAgentTaskCreateInput(input)
			if testCase.wantErr {
				if err == nil {
					t.Fatal("accepted an out-of-contract correction grant")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if normalized.MaxCorrections != testCase.max {
				t.Fatalf("max_corrections = %d, want %d", normalized.MaxCorrections, testCase.max)
			}
		})
	}
}

// The eligibility rule runs before any database lookup, so it must reject
// anything that widens scope on the proposal alone.
func TestDelegationCorrectionForOperationRejectsWideningProposals(t *testing.T) {
	build := func(mutate func(*AgentTaskContinueInput), op func(*Operation)) Operation {
		input := AgentTaskContinueInput{TaskID: "task-1", Prompt: "fix it", Provider: ProviderAuto, SessionMode: SessionModeResumeOrNew}
		if mutate != nil {
			mutate(&input)
		}
		normalized, err := NormalizeAgentTaskContinueInput(input)
		if err != nil {
			t.Fatal(err)
		}
		args, err := json.Marshal(normalized)
		if err != nil {
			t.Fatal(err)
		}
		operation := Operation{
			Capability:  CapabilityAgentTaskContinue,
			Invocation:  Invocation{Capability: CapabilityAgentTaskContinue, Args: args},
			Provider:    "codex",
			SessionKey:  "caller-key",
			ProjectPath: "/tmp/caller",
		}
		if op != nil {
			op(&operation)
		}
		return operation
	}

	if correction, ok := DelegationCorrectionForOperation(build(nil, nil)); !ok || correction.TaskID != "task-1" {
		t.Fatalf("inherited continuation was not recognized: %+v %v", correction, ok)
	}
	refusals := map[string]Operation{
		"fresh session":     build(func(i *AgentTaskContinueInput) { i.SessionMode = SessionModeNew }, nil),
		"explicit provider": build(func(i *AgentTaskContinueInput) { i.Provider = ProviderClaudeCode }, nil),
		"select model": build(func(i *AgentTaskContinueInput) {
			i.Provider = ProviderCodex
			i.SelectModel = true
		}, nil),
		"other capability":   build(nil, func(o *Operation) { o.Capability = CapabilityAgentTaskClose }),
		"no caller session":  build(nil, func(o *Operation) { o.SessionKey = "" }),
		"no caller provider": build(nil, func(o *Operation) { o.Provider = "" }),
		"relative caller":    build(nil, func(o *Operation) { o.ProjectPath = "relative/path" }),
	}
	for name, operation := range refusals {
		if _, ok := DelegationCorrectionForOperation(operation); ok {
			t.Fatalf("%s was treated as a covered correction", name)
		}
	}
}
