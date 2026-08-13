package service

import (
	"testing"

	"lcroom/internal/config"
	"lcroom/internal/llm"
	"lcroom/internal/modelcatalog"
)

func deepSeekChatService(t *testing.T, helm, utility string) *Service {
	t.Helper()
	t.Setenv("LCROOM_BOSS_MODEL", "")

	cfg := config.Default()
	cfg.AIBackend = config.AIBackendOpenAIAPI
	cfg.BossChatBackend = config.AIBackendDeepSeek
	cfg.DeepSeekAPIKey = "ds-test-example"
	cfg.BossHelmModel = helm
	cfg.BossUtilityModel = utility
	return &Service{cfg: cfg, bossChatUsageTracker: llm.NewUsageTracker()}
}

// The whole point of the correction: a stale OpenAI model left in the config
// must never be handed to DeepSeek, because the provider answers with a 400
// that surfaces to the user as an unexplained dead chat.
func TestBossRunnersNeverEmitCrossProviderModel(t *testing.T) {
	svc := deepSeekChatService(t, modelcatalog.OpenAIUtilityModel, modelcatalog.OpenAIUtilityModel)

	_, helmModel, backend := svc.NewBossTextRunner()
	if backend != config.AIBackendDeepSeek {
		t.Fatalf("backend = %s, want deepseek", backend)
	}
	if !modelcatalog.IsKnown(modelcatalog.ProviderDeepSeek, helmModel) {
		t.Fatalf("helm model %q is not valid for deepseek", helmModel)
	}
	if helmModel != config.DefaultDeepSeekProModel {
		t.Fatalf("helm model = %q, want the backend default %q", helmModel, config.DefaultDeepSeekProModel)
	}

	_, utilityModel, _ := svc.NewBossUtilityJSONRunner()
	if !modelcatalog.IsKnown(modelcatalog.ProviderDeepSeek, utilityModel) {
		t.Fatalf("utility model %q is not valid for deepseek", utilityModel)
	}
	if utilityModel != config.DefaultDeepSeekModel {
		t.Fatalf("utility model = %q, want the backend default %q", utilityModel, config.DefaultDeepSeekModel)
	}

	_, jsonModel, _ := svc.NewBossJSONRunner()
	if !modelcatalog.IsKnown(modelcatalog.ProviderDeepSeek, jsonModel) {
		t.Fatalf("json model %q is not valid for deepseek", jsonModel)
	}
}

// A deliberately configured, valid model must survive untouched.
func TestBossRunnersPreserveValidConfiguredModels(t *testing.T) {
	svc := deepSeekChatService(t, modelcatalog.DeepSeekFlashModel, modelcatalog.DeepSeekProModel)

	if _, got, _ := svc.NewBossTextRunner(); got != modelcatalog.DeepSeekFlashModel {
		t.Errorf("helm model = %q, want the configured %q", got, modelcatalog.DeepSeekFlashModel)
	}
	if _, got, _ := svc.NewBossUtilityJSONRunner(); got != modelcatalog.DeepSeekProModel {
		t.Errorf("utility model = %q, want the configured %q", got, modelcatalog.DeepSeekProModel)
	}
}

// Open-ended backends serve arbitrary model ids, so nothing may be rewritten.
func TestBossRunnersDoNotRewriteOpenEndedBackendModels(t *testing.T) {
	t.Setenv("LCROOM_BOSS_MODEL", "")

	const custom = "some/self-hosted-model"
	cfg := config.Default()
	cfg.BossChatBackend = config.AIBackendOpenRouter
	cfg.OpenRouterAPIKey = "or-test-example"
	cfg.BossHelmModel = custom
	svc := &Service{cfg: cfg, bossChatUsageTracker: llm.NewUsageTracker()}

	if _, got, _ := svc.NewBossTextRunner(); got != custom {
		t.Fatalf("openrouter model = %q, want the configured %q left untouched", got, custom)
	}
}

func TestBossRunnersPreserveCustomDirectProviderModels(t *testing.T) {
	svc := deepSeekChatService(t, "deepseek/Custom-Helm-V7", "Custom-Utility-V7")

	if _, got, _ := svc.NewBossTextRunner(); got != "Custom-Helm-V7" {
		t.Fatalf("custom helm model = %q, want normalized custom identifier", got)
	}
	if _, got, _ := svc.NewBossUtilityJSONRunner(); got != "Custom-Utility-V7" {
		t.Fatalf("custom utility model = %q, want spelling preserved", got)
	}
}

func TestBossRunnersCanonicalizeKnownQualifiedModel(t *testing.T) {
	svc := deepSeekChatService(t, "DeepSeek/DeepSeek-V4-Pro", modelcatalog.DeepSeekFlashModel)

	if _, got, _ := svc.NewBossTextRunner(); got != modelcatalog.DeepSeekProModel {
		t.Fatalf("qualified helm model = %q, want %q", got, modelcatalog.DeepSeekProModel)
	}
}

func TestConfiguredProjectModelCorrectsAndNormalizes(t *testing.T) {
	cfg := config.Default()
	cfg.AIBackend = config.AIBackendDeepSeek
	cfg.DeepSeekModel = modelcatalog.OpenAIUtilityModel
	if got := configuredProjectModelForBackend(cfg, config.AIBackendDeepSeek); got != config.DefaultDeepSeekModel {
		t.Fatalf("cross-provider project model = %q, want fallback %q", got, config.DefaultDeepSeekModel)
	}

	cfg.DeepSeekModel = "DeepSeek/DeepSeek-V4-Pro"
	if got := configuredProjectModelForBackend(cfg, config.AIBackendDeepSeek); got != modelcatalog.DeepSeekProModel {
		t.Fatalf("qualified project model = %q, want %q", got, modelcatalog.DeepSeekProModel)
	}

	cfg.DeepSeekModel = "deepseek/Custom-Project-V7"
	if got := configuredProjectModelForBackend(cfg, config.AIBackendDeepSeek); got != "Custom-Project-V7" {
		t.Fatalf("custom project model = %q, want normalized custom identifier", got)
	}
}
