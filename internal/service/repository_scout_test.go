package service

import (
	"testing"

	"lcroom/internal/config"
)

func TestRepositoryScoutRoutesInheritChatWithoutLCAgentSetup(t *testing.T) {
	cfg := config.Default()
	cfg.BossChatBackend = config.AIBackendDeepSeek
	cfg.DeepSeekAPIKey = "shared-key"
	cfg.BossUtilityModel = "deepseek-v4-flash"
	cfg.BossHelmModel = "deepseek-v4-pro"
	cfg.AIBackend = config.AIBackendCodex

	routes := repositoryScoutRoutes(cfg)
	if len(routes) != 2 {
		t.Fatalf("routes = %+v, want Chat utility and main", routes)
	}
	if routes[0].Source != "chat_utility" || routes[0].Provider != "deepseek" || routes[0].Model != "deepseek-v4-flash" {
		t.Fatalf("utility route = %+v", routes[0])
	}
	if routes[1].Source != "chat_main" || routes[1].Model != "deepseek-v4-pro" {
		t.Fatalf("main route = %+v", routes[1])
	}
	if routes[0].APIKey != "shared-key" {
		t.Fatal("Scout did not inherit the shared Chat credential")
	}
}

func TestRepositoryScoutRoutesPutExplicitLCAgentOverrideFirst(t *testing.T) {
	cfg := config.Default()
	cfg.LCAgentRoutePreset = "quality"
	cfg.BossChatBackend = config.AIBackendDeepSeek
	cfg.DeepSeekAPIKey = "shared-key"
	cfg.BossUtilityModel = "deepseek-v4-flash"
	cfg.BossHelmModel = "deepseek-v4-pro"

	routes := repositoryScoutRoutes(cfg)
	if len(routes) != 3 {
		t.Fatalf("routes = %+v, want override plus Chat utility/main", routes)
	}
	if routes[0].Source != "lcagent_override" || routes[0].Provider != "openai" {
		t.Fatalf("first route = %+v, want explicit quality override", routes[0])
	}
	if routes[1].Source != "chat_utility" || routes[2].Source != "chat_main" {
		t.Fatalf("fallback order = %+v", routes)
	}
}

func TestRepositoryScoutRoutesUseProjectInferenceAsLastFallback(t *testing.T) {
	cfg := config.Default()
	cfg.BossChatBackend = config.AIBackendDisabled
	cfg.AIBackend = config.AIBackendOllama
	cfg.OllamaModel = "qwen-local"

	routes := repositoryScoutRoutes(cfg)
	if len(routes) != 1 {
		t.Fatalf("routes = %+v, want project inference only", routes)
	}
	if routes[0].Source != "project_inference" || routes[0].Provider != "ollama" || routes[0].Model != "qwen-local" {
		t.Fatalf("project route = %+v", routes[0])
	}
}
