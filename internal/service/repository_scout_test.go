package service

import (
	"strings"
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

func TestRepositoryScoutRoutesKeepConfiguredLCAgentWorkerBehindChat(t *testing.T) {
	cfg := config.Default()
	cfg.LCAgentRoutePreset = "quality"
	cfg.BossChatBackend = config.AIBackendDeepSeek
	cfg.DeepSeekAPIKey = "shared-key"
	cfg.BossUtilityModel = "deepseek-v4-flash"
	cfg.BossHelmModel = "deepseek-v4-pro"

	routes := repositoryScoutRoutes(cfg)
	if len(routes) != 3 {
		t.Fatalf("routes = %+v, want Chat utility/main plus worker fallback", routes)
	}
	if routes[0].Source != "chat_utility" || routes[1].Source != "chat_main" {
		t.Fatalf("Chat route order = %+v", routes)
	}
	if routes[2].Source != "lcagent_override" || routes[2].Provider != "openai" || !strings.Contains(routes[2].Description, "worker fallback") {
		t.Fatalf("worker fallback = %+v", routes[2])
	}
}

func TestRepositoryScoutRoutesPreferLunaOverConfiguredKimiWorker(t *testing.T) {
	cfg := config.Default()
	cfg.BossChatBackend = config.AIBackendOpenAIAPI
	cfg.OpenAIAPIKey = "openai-key"
	cfg.BossUtilityModel = "gpt-5.6-luna"
	cfg.BossHelmModel = "gpt-5.6-luna"
	cfg.LCAgentProvider = "moonshot"
	cfg.EmbeddedLCAgentModel = "kimi-k3"
	cfg.MoonshotAPIKey = "moonshot-key"

	routes := repositoryScoutRoutes(cfg)
	if len(routes) < 3 {
		t.Fatalf("routes = %+v, want Chat utility/main plus Kimi fallback", routes)
	}
	if routes[0].Source != "chat_utility" || routes[0].Provider != "openai" || routes[0].Model != "gpt-5.6-luna" {
		t.Fatalf("first route = %+v, want inherited Chat Luna", routes[0])
	}
	if routes[2].Provider != "moonshot" || routes[2].Model != "kimi-k3" || !strings.Contains(routes[2].Description, "worker fallback") {
		t.Fatalf("Kimi fallback = %+v", routes[2])
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
