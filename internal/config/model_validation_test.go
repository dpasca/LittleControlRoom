package config

import (
	"os"
	"strings"
	"testing"

	"lcroom/internal/modelcatalog"
)

// The live failure this validation exists to prevent: the chat provider was
// switched to DeepSeek while the model fields kept their OpenAI values, and
// every chat call returned a provider 400 with nothing in LCR reporting why.
func TestModelMismatchesCatchesChatProviderSwitchLeftovers(t *testing.T) {
	cfg := Default()
	cfg.BossChatBackend = AIBackendDeepSeek
	cfg.BossHelmModel = modelcatalog.OpenAIUtilityModel
	cfg.BossUtilityModel = modelcatalog.OpenAIUtilityModel

	mismatches := cfg.ModelMismatches()
	if len(mismatches) != 2 {
		t.Fatalf("mismatches = %d, want 2: %+v", len(mismatches), mismatches)
	}

	byField := map[string]ModelMismatch{}
	for _, m := range mismatches {
		byField[m.Field] = m
	}
	for _, field := range []string{"boss_helm_model", "boss_utility_model"} {
		m, ok := byField[field]
		if !ok {
			t.Fatalf("no mismatch reported for %s", field)
		}
		if m.Provider != modelcatalog.ProviderDeepSeek {
			t.Errorf("%s provider = %q, want deepseek", field, m.Provider)
		}
		if m.LikelyProvider != modelcatalog.ProviderOpenAI {
			t.Errorf("%s likely provider = %q, want openai", field, m.LikelyProvider)
		}
		if m.Fallback == "" {
			t.Errorf("%s has no fallback; the run would still send the bad model", field)
		}
		if !modelcatalog.IsKnown(modelcatalog.ProviderDeepSeek, m.Fallback) {
			t.Errorf("%s fallback %q is itself not valid for deepseek", field, m.Fallback)
		}
	}
}

func TestCheckModelCatchesForeignModelBehindTargetProviderPrefix(t *testing.T) {
	mismatch := CheckModel("boss_helm_model", "boss_chat_backend", modelcatalog.ProviderDeepSeek,
		"deepseek/"+modelcatalog.OpenAIDefaultModel, modelcatalog.DeepSeekProModel)
	if mismatch == nil || mismatch.LikelyProvider != modelcatalog.ProviderOpenAI {
		t.Fatalf("mismatch = %+v, want OpenAI model detected after DeepSeek prefix removal", mismatch)
	}
}

// The repaired config must be silent. A validator that cries wolf on a correct
// config is worse than none, because the warning stops being read.
func TestModelMismatchesSilentOnRepairedConfig(t *testing.T) {
	cfg := Default()
	cfg.BossChatBackend = AIBackendDeepSeek
	cfg.BossHelmModel = modelcatalog.DeepSeekProModel
	cfg.BossUtilityModel = modelcatalog.DeepSeekFlashModel

	if got := cfg.ModelMismatches(); len(got) != 0 {
		t.Fatalf("repaired config reported %d mismatches: %+v", len(got), got)
	}
}

// Regression for a bug in the first cut of this validator: the per-provider
// project model fields are scoped by their own name, so deepseek_model holds a
// DeepSeek model even while ai_backend is openai_api. Checking them against the
// active backend flagged a correct config as broken.
func TestModelMismatchesScopesProviderModelFieldsByName(t *testing.T) {
	cfg := Default()
	cfg.AIBackend = AIBackendOpenAIAPI
	cfg.DeepSeekModel = modelcatalog.DeepSeekFlashModel
	cfg.XiaomiModel = modelcatalog.XiaomiUtilityModel
	cfg.MoonshotModel = modelcatalog.MoonshotModel

	if got := cfg.ModelMismatches(); len(got) != 0 {
		t.Fatalf("provider-scoped fields reported %d mismatches: %+v", len(got), got)
	}

	cfg.DeepSeekModel = modelcatalog.OpenAIUtilityModel
	got := cfg.ModelMismatches()
	if len(got) != 1 || got[0].Field != "deepseek_model" {
		t.Fatalf("expected one deepseek_model mismatch, got %+v", got)
	}
}

// Blank means "use the provider default", which is always valid.
func TestModelMismatchesIgnoresBlankModels(t *testing.T) {
	cfg := Default()
	cfg.BossChatBackend = AIBackendDeepSeek
	cfg.BossHelmModel = ""
	cfg.BossUtilityModel = ""
	cfg.BossChatModel = ""

	if got := cfg.ModelMismatches(); len(got) != 0 {
		t.Fatalf("blank models reported %d mismatches: %+v", len(got), got)
	}
}

// Routers and self-hosted runtimes serve arbitrary ids.
func TestModelMismatchesPermissiveForOpenEndedBackends(t *testing.T) {
	for _, backend := range []AIBackend{AIBackendOpenRouter, AIBackendOllama, AIBackendMLX} {
		cfg := Default()
		cfg.BossChatBackend = backend
		cfg.BossHelmModel = "some/locally-served-model"
		if got := cfg.ModelMismatches(); len(got) != 0 {
			t.Errorf("backend %q reported %d mismatches: %+v", backend, len(got), got)
		}
	}
}

// Agent CLIs carry their own model selection; they are not model APIs.
func TestProviderForBackendSkipsAgentCLIs(t *testing.T) {
	for _, backend := range []AIBackend{AIBackendCodex, AIBackendClaude, AIBackendOpenCode, AIBackendDisabled, AIBackendUnset} {
		if got := ProviderForBackend(backend); got != "" {
			t.Errorf("ProviderForBackend(%q) = %q, want empty", backend, got)
		}
	}
}

// The message has to carry the fix, not just the symptom.
func TestModelMismatchDetailNamesFieldAcceptedAndFix(t *testing.T) {
	m := ModelMismatch{
		Field:          "boss_helm_model",
		ProviderField:  "boss_chat_backend",
		Provider:       modelcatalog.ProviderDeepSeek,
		Model:          modelcatalog.OpenAIUtilityModel,
		LikelyProvider: modelcatalog.ProviderOpenAI,
		Accepted:       modelcatalog.Accepted(modelcatalog.ProviderDeepSeek),
		Fallback:       modelcatalog.DeepSeekProModel,
	}
	detail := m.Detail()
	for _, want := range []string{
		"boss_helm_model", "boss_chat_backend", modelcatalog.OpenAIUtilityModel,
		modelcatalog.DeepSeekProModel, "accepted:", "fix:", "openai",
	} {
		if !strings.Contains(detail, want) {
			t.Errorf("Detail() missing %q:\n%s", want, detail)
		}
	}
}

// The write boundary is the backstop for every path that does not go through
// the settings dialog: a mismatch must never reach config.toml.
func TestSaveEditableSettingsRefusesProviderModelDivergence(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/config.toml"

	settings := EditableSettingsFromAppConfig(Default())
	settings.BossChatBackend = AIBackendDeepSeek
	settings.BossHelmModel = modelcatalog.OpenAIUtilityModel

	err := SaveEditableSettings(path, settings)
	if err == nil {
		t.Fatal("SaveEditableSettings() = nil, want refusal for a cross-provider model")
	}
	for _, want := range []string{"boss_helm_model", "deepseek"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q: %v", want, err)
		}
	}
	if _, statErr := os.Stat(path); statErr == nil {
		t.Fatal("config was written despite the refusal")
	}
}

func TestSaveEditableSettingsAcceptsMatchingProviderModel(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/config.toml"

	settings := EditableSettingsFromAppConfig(Default())
	settings.BossChatBackend = AIBackendDeepSeek
	settings.BossHelmModel = modelcatalog.DeepSeekProModel
	settings.BossUtilityModel = modelcatalog.DeepSeekFlashModel

	if err := SaveEditableSettings(path, settings); err != nil {
		t.Fatalf("SaveEditableSettings() error = %v, want success", err)
	}
	if _, statErr := os.Stat(path); statErr != nil {
		t.Fatalf("config was not written: %v", statErr)
	}
}

func TestSaveEditableSettingsAcceptsCustomDirectProviderModels(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/config.toml"

	settings := EditableSettingsFromAppConfig(Default())
	settings.BossChatBackend = AIBackendDeepSeek
	settings.BossHelmModel = "deepseek/Custom-Helm-V7"
	settings.BossUtilityModel = "Custom-Utility-V7"
	settings.DeepSeekModel = "deepseek/Custom-Project-V7"
	settings.LCAgentRoutePreset = ""
	settings.LCAgentProvider = "deepseek"
	settings.EmbeddedLCAgentModel = "deepseek/Custom-Agent-V7"

	if err := SaveEditableSettings(path, settings); err != nil {
		t.Fatalf("SaveEditableSettings() rejected valid custom models: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read saved config: %v", err)
	}
	for _, want := range []string{"Custom-Helm-V7", "Custom-Project-V7", "Custom-Agent-V7"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("saved config missing custom model %q:\n%s", want, raw)
		}
	}
	if strings.Contains(string(raw), "deepseek/Custom") {
		t.Fatalf("saved direct-provider models kept their redundant prefix:\n%s", raw)
	}
}

func TestModelMismatchesIgnoresMainModelWhileRoutePresetIsActive(t *testing.T) {
	cfg := Default()
	cfg.LCAgentRoutePreset = "balanced"
	cfg.LCAgentProvider = "deepseek"
	cfg.EmbeddedLCAgentModel = modelcatalog.OpenAIDefaultModel

	if got := cfg.ModelMismatches(); len(got) != 0 {
		t.Fatalf("inactive explicit main model reported mismatches: %+v", got)
	}
}

func TestNormalizeEditableSettingsCanonicalizesDirectProviderModels(t *testing.T) {
	settings := EditableSettingsFromAppConfig(Default())
	settings.BossChatBackend = AIBackendDeepSeek
	settings.BossHelmModel = "DeepSeek/DeepSeek-V4-Pro"
	settings.BossUtilityModel = "DEEPSEEK/DEEPSEEK-V4-FLASH"
	settings.DeepSeekModel = "DeepSeek/DeepSeek-V4-Pro"
	settings.MoonshotModel = "MoonshotAI/KIMI-K3"
	settings.XiaomiModel = "XIAOMI/MIMO-V2.5-PRO"

	got := NormalizeEditableSettings(settings)
	if got.BossHelmModel != modelcatalog.DeepSeekProModel || got.BossUtilityModel != modelcatalog.DeepSeekFlashModel {
		t.Errorf("normalized boss models = %q/%q", got.BossHelmModel, got.BossUtilityModel)
	}
	if got.DeepSeekModel != modelcatalog.DeepSeekProModel || got.MoonshotModel != "kimi-k3" || got.XiaomiModel != modelcatalog.XiaomiProModel {
		t.Errorf("normalized project models = %q/%q/%q", got.DeepSeekModel, got.MoonshotModel, got.XiaomiModel)
	}
}
