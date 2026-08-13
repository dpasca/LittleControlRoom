package modelcatalog

import "testing"

func TestIsKnownAcceptsProviderOwnModels(t *testing.T) {
	cases := []struct {
		provider string
		model    string
	}{
		{ProviderOpenAI, OpenAIDefaultModel},
		{ProviderOpenAI, OpenAIUtilityModel},
		{ProviderOpenAI, "gpt-5.6-sol"},
		{ProviderOpenAI, "gpt-5.6-2026-05-01"},
		{ProviderDeepSeek, DeepSeekProModel},
		{ProviderDeepSeek, DeepSeekFlashModel},
		{ProviderMoonshot, MoonshotModel},
		{ProviderMoonshot, "kimi-k3-turbo"},
		{ProviderXiaomi, XiaomiProModel},
		{ProviderXiaomi, XiaomiUtilityModel},
	}
	for _, tc := range cases {
		if !IsKnown(tc.provider, tc.model) {
			t.Errorf("IsKnown(%q, %q) = false, want true", tc.provider, tc.model)
		}
	}
}

// The exact pair that took the chat backend down: an OpenAI model name left
// behind in the config after the chat provider was switched to DeepSeek.
func TestIsKnownRejectsCrossProviderModel(t *testing.T) {
	if IsKnown(ProviderDeepSeek, OpenAIUtilityModel) {
		t.Fatalf("IsKnown(deepseek, %q) = true, want false", OpenAIUtilityModel)
	}
	if got := ProviderForModel(OpenAIUtilityModel); got != ProviderOpenAI {
		t.Fatalf("ProviderForModel(%q) = %q, want %q", OpenAIUtilityModel, got, ProviderOpenAI)
	}
}

// Regression: the DeepSeek arm used to compare the raw string while every other
// provider normalized and lowercased first, so a qualified or capitalized
// DeepSeek model was reported as foreign.
func TestIsKnownNormalizesDeepSeekLikeOtherProviders(t *testing.T) {
	for _, model := range []string{"DeepSeek-V4-Pro", "deepseek/deepseek-v4-pro", "  deepseek-v4-flash  "} {
		if !IsKnown(ProviderDeepSeek, model) {
			t.Errorf("IsKnown(deepseek, %q) = false, want true", model)
		}
	}
}

// Routers and self-hosted runtimes serve arbitrary ids; validating them would
// produce false alarms, so they must stay permissive.
func TestOpenEndedProvidersAcceptAnything(t *testing.T) {
	for _, provider := range []string{ProviderOpenRouter, ProviderOllama, ProviderMLX, ""} {
		if !IsOpenEnded(provider) {
			t.Errorf("IsOpenEnded(%q) = false, want true", provider)
		}
		if !IsKnown(provider, "some-locally-pulled-model") {
			t.Errorf("IsKnown(%q, custom) = false, want true", provider)
		}
		if Accepted(provider) != nil {
			t.Errorf("Accepted(%q) should be nil so no mismatch is reported", provider)
		}
	}
}

func TestIsKnownRejectsEmptyModel(t *testing.T) {
	for _, provider := range []string{ProviderOpenAI, ProviderDeepSeek, ProviderOpenRouter, ""} {
		if IsKnown(provider, "  ") {
			t.Errorf("IsKnown(%q, empty) = true, want false", provider)
		}
	}
}

func TestProviderForModelReturnsEmptyForUnclaimedModel(t *testing.T) {
	if got := ProviderForModel("something-nobody-serves"); got != "" {
		t.Fatalf("ProviderForModel(unknown) = %q, want empty", got)
	}
}

func TestNormalizeStripsQualifiedPrefixes(t *testing.T) {
	cases := map[[2]string]string{
		{ProviderDeepSeek, "deepseek/deepseek-v4-pro"}:  DeepSeekProModel,
		{ProviderDeepSeek, "deepseek/ deepseek-v4-pro"}: DeepSeekProModel,
		{ProviderOpenAI, "openai/gpt-5.6"}:              OpenAIDefaultModel,
		{ProviderMoonshot, "moonshotai/kimi-k3"}:        "kimi-k3",
		{ProviderXiaomi, "xiaomi/mimo-v2.5-pro"}:        XiaomiProModel,
	}
	for input, want := range cases {
		if got := Normalize(input[0], input[1]); got != want {
			t.Errorf("Normalize(%q, %q) = %q, want %q", input[0], input[1], got, want)
		}
	}
}

func TestNormalizeForRequestCanonicalizesKnownModels(t *testing.T) {
	cases := map[[2]string]string{
		{ProviderDeepSeek, "DeepSeek/DeepSeek-V4-Pro"}: DeepSeekProModel,
		{ProviderOpenAI, "OPENAI/GPT-5.6"}:             OpenAIDefaultModel,
		{ProviderMoonshot, "MoonshotAI/KIMI-K3"}:       "kimi-k3",
		{ProviderXiaomi, "XIAOMI/MIMO-V2.5-PRO"}:       XiaomiProModel,
	}
	for input, want := range cases {
		if got := NormalizeForRequest(input[0], input[1]); got != want {
			t.Errorf("NormalizeForRequest(%q, %q) = %q, want %q", input[0], input[1], got, want)
		}
	}
}

func TestNormalizeForRequestPreservesCustomDirectModelSpelling(t *testing.T) {
	if got, want := NormalizeForRequest(ProviderDeepSeek, "DeepSeek/Custom-Experiment-V7"), "Custom-Experiment-V7"; got != want {
		t.Fatalf("NormalizeForRequest(custom) = %q, want %q", got, want)
	}
	if got, want := NormalizeForRequest("future-provider", "Owner/Custom-Model-V7"), "Owner/Custom-Model-V7"; got != want {
		t.Fatalf("NormalizeForRequest(unknown provider) = %q, want %q", got, want)
	}
}
