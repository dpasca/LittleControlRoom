package control

import (
	"encoding/json"
	"testing"
)

func TestEngineerModelSelectionValidation(t *testing.T) {
	for _, tc := range []struct {
		selection EngineerModelSelection
		provider  Provider
	}{
		{EngineerModelSelection{Model: "model"}, ProviderAuto},
		{EngineerModelSelection{ReasoningEffort: "medium"}, ProviderCodex},
		{EngineerModelSelection{Model: "model", SelectModel: true}, ProviderCodex},
		{EngineerModelSelection{SelectModel: true}, ProviderAuto},
		{EngineerModelSelection{Model: "model", ModelProvider: "openai"}, ProviderCodex},
	} {
		if err := tc.selection.Normalize(tc.provider); err == nil {
			t.Fatalf("accepted invalid selection: %+v", tc)
		}
	}
	catalog := EngineerModelCatalog{Provider: ProviderCodex, Models: []EngineerModel{{Model: "exact-id", ReasoningEfforts: []string{"medium"}}}}
	for _, selection := range []EngineerModelSelection{{Model: "alias"}, {Model: "exact-id", ReasoningEffort: "xhigh"}} {
		if err := catalog.Validate(selection); err == nil {
			t.Fatalf("accepted unsupported choice: %+v", selection)
		}
	}
	if err := catalog.Validate(EngineerModelSelection{Model: "exact-id", ReasoningEffort: "medium"}); err != nil {
		t.Fatal(err)
	}
}

func TestEngineerControlChoicesSurviveNormalization(t *testing.T) {
	for _, name := range []CapabilityName{CapabilityEngineerSendPrompt, CapabilityProjectCreateAndStartEngineer, CapabilityTodoCreateWorktreeAndStartEngineer} {
		args := map[string]any{"project_path": "/repo", "project_name": "repo", "provider": "codex", "prompt": "work", "reveal": true, "model": "exact-id", "reasoning_effort": "medium"}
		if name == CapabilityEngineerSendPrompt {
			args["session_mode"] = "new"
		} else {
			args["todo_text"] = "work"
		}
		if name == CapabilityProjectCreateAndStartEngineer {
			args["parent_path"] = "/"
			args["project_path"] = "/repo"
		}
		raw, _ := json.Marshal(args)
		inv, err := ValidateInvocation(Invocation{Capability: name, Args: raw})
		if err != nil {
			t.Fatal(err)
		}
		var selection EngineerModelSelection
		if err := json.Unmarshal(inv.Args, &selection); err != nil {
			t.Fatal(err)
		}
		if selection.Model != "exact-id" || selection.ReasoningEffort != "medium" {
			t.Fatalf("lost selection: %s", inv.Args)
		}
	}
}

func TestAgentTaskModelInputsPreserveChoicesAndRejectAmbiguity(t *testing.T) {
	for _, capability := range []CapabilityName{CapabilityAgentTaskCreate, CapabilityAgentTaskContinue} {
		base := map[string]any{"title": "Worker", "kind": "agent", "task_id": "agt_worker", "provider": "lcagent", "prompt": "work", "session_mode": "resume_or_new", "model": "cheap", "model_provider": "zai", "reasoning_effort": "low"}
		if capability == CapabilityAgentTaskCreate {
			delete(base, "task_id")
			delete(base, "session_mode")
		} else {
			delete(base, "title")
			delete(base, "kind")
		}
		raw, _ := json.Marshal(base)
		inv, err := ValidateInvocation(Invocation{Capability: capability, Args: raw})
		if err != nil {
			t.Fatal(err)
		}
		var selection EngineerModelSelection
		if err := json.Unmarshal(inv.Args, &selection); err != nil {
			t.Fatal(err)
		}
		if selection.Model != "cheap" || selection.ModelProvider != "zai" || selection.ReasoningEffort != "low" {
			t.Fatalf("lost choice: %#v", selection)
		}
		for _, change := range []map[string]any{{"provider": "auto"}, {"provider": "codex"}, {"select_model": true}, {"model": ""}} {
			changed := map[string]any{}
			for k, v := range base {
				changed[k] = v
			}
			for k, v := range change {
				changed[k] = v
			}
			raw, _ = json.Marshal(changed)
			if _, err := ValidateInvocation(Invocation{Capability: capability, Args: raw}); err == nil {
				t.Fatalf("accepted ambiguous selection: %s", raw)
			}
		}
	}
}
