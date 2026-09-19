package codexapp

import (
	"testing"

	"lcroom/internal/lcagent/modeladapter"
)

// The embedded LCAgent model picker must offer GLM models for provider zai and
// wire the Z.ai credential/env plumbing exactly like the other direct
// providers.
func TestLCAgentCuratedModelOptionsOfferZaiGLM(t *testing.T) {
	options := lcagentModelOptionsForProvider("zai")
	want := map[string]bool{
		modeladapter.DefaultZaiModel:        false,
		modeladapter.DefaultZaiProModel:     false,
		modeladapter.DefaultZaiUtilityModel: false,
	}
	if len(options) == 0 {
		t.Fatal("curated Z.ai model options are empty")
	}
	for _, option := range options {
		if option.ModelProvider != "zai" {
			t.Fatalf("Z.ai option %#v has provider %q", option, option.ModelProvider)
		}
		if _, ok := want[option.Model]; ok {
			want[option.Model] = true
		}
	}
	for model, seen := range want {
		if !seen {
			t.Fatalf("curated Z.ai options are missing %q: %#v", model, options)
		}
	}

	// GLM-5.3 supports the reasoning-effort ladder; GLM-5.1 does not.
	if got := lcagentReasoningEffortOptionsForProvider("zai", modeladapter.DefaultZaiProModel); len(got) == 0 {
		t.Fatalf("GLM-5.3 should offer reasoning efforts, got %#v", got)
	}
	if got := lcagentReasoningEffortOptionsForProvider("zai", modeladapter.DefaultZaiModel); len(got) != 0 {
		t.Fatalf("GLM-5.1 should not offer reasoning efforts, got %#v", got)
	}
}

func TestLCAgentZaiProviderHelpers(t *testing.T) {
	for _, raw := range []string{"zai", "z-ai", "z.ai", "ZAI"} {
		provider, err := lcagentProviderValue(raw)
		if err != nil {
			t.Fatalf("lcagentProviderValue(%q) error = %v", raw, err)
		}
		if provider != "zai" {
			t.Fatalf("lcagentProviderValue(%q) = %q, want zai", raw, provider)
		}
	}
	if got := lcagentDefaultModel("zai"); got != modeladapter.DefaultZaiModel {
		t.Fatalf("lcagentDefaultModel(zai) = %q, want %q", got, modeladapter.DefaultZaiModel)
	}
	name, value := lcagentLaunchCredential(LaunchRequest{LCAgentZaiAPIKey: "zai-test-key"}, "zai")
	if name != "ZAI_API_KEY" || value != "zai-test-key" {
		t.Fatalf("lcagentLaunchCredential(zai) = %q/%q", name, value)
	}
	if got := lcagentProviderDisplayName("zai"); got != "Z.ai" {
		t.Fatalf("lcagentProviderDisplayName(zai) = %q, want Z.ai", got)
	}
}
