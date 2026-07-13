package service

import (
	"strings"
	"time"

	"lcroom/internal/config"
	"lcroom/internal/lcagent"
	"lcroom/internal/model"
	"lcroom/internal/sessionclassify"
)

// NewRepositoryScout builds a read-only LCAgent Scout whose inference routes
// inherit already-configured LCR providers. Dedicated LCAgent settings are an
// optional first-choice override, not a prerequisite for Chat file access.
func (s *Service) NewRepositoryScout() *lcagent.ScoutService {
	if s == nil {
		return &lcagent.ScoutService{}
	}
	s.mu.Lock()
	cfg := cloneAppConfig(s.cfg)
	usageTracker := s.bossChatUsageTracker
	s.mu.Unlock()
	scout := &lcagent.ScoutService{Routes: repositoryScoutRoutes(cfg)}
	if usageTracker != nil {
		scout.OnAttemptStart = func(route lcagent.ScoutRoute) {
			usageTracker.Start(strings.TrimSpace(route.Model))
		}
		scout.OnAttemptFinish = func(route lcagent.ScoutRoute, usage model.LLMUsage, err error) {
			if err == nil || hasMeaningfulScoutUsage(usage) {
				usageTracker.Complete(strings.TrimSpace(route.Model), usage)
				return
			}
			usageTracker.Fail(strings.TrimSpace(route.Model))
		}
	}
	return scout
}

func hasMeaningfulScoutUsage(usage model.LLMUsage) bool {
	return usage.InputTokens != 0 || usage.OutputTokens != 0 || usage.TotalTokens != 0 || usage.CachedInputTokens != 0 || usage.ReasoningTokens != 0 || usage.EstimatedCostUSD != 0
}

func repositoryScoutRoutes(cfg config.AppConfig) []lcagent.ScoutRoute {
	var routes []lcagent.ScoutRoute
	if route, ok := explicitLCAgentScoutRoute(cfg); ok {
		routes = append(routes, route)
	}

	chatBackend := cfg.EffectiveBossChatBackend()
	if route, ok := inheritedScoutRoute(cfg, chatBackend, configuredBossUtilityModelForBackend(cfg, chatBackend), "chat_utility", "inherited Chat utility model"); ok {
		routes = append(routes, route)
	}
	if route, ok := inheritedScoutRoute(cfg, chatBackend, configuredBossHelmModelForBackend(cfg, chatBackend), "chat_main", "inherited Chat main model fallback"); ok {
		routes = append(routes, route)
	}

	projectBackend := cfg.EffectiveAIBackend()
	projectModel := strings.TrimSpace(cfg.OpenAICompatibleModel(projectBackend))
	if projectBackend == config.AIBackendOpenAIAPI {
		projectModel = sessionclassify.DefaultModel
	}
	if route, ok := inheritedScoutRoute(cfg, projectBackend, projectModel, "project_inference", "inherited project-analysis inference fallback"); ok {
		routes = append(routes, route)
	}
	return routes
}

func explicitLCAgentScoutRoute(cfg config.AppConfig) (lcagent.ScoutRoute, bool) {
	if presetName := strings.TrimSpace(cfg.LCAgentRoutePreset); presetName != "" {
		route, ok := lcagent.ScoutRouteFromPreset(presetName)
		if !ok {
			return lcagent.ScoutRoute{}, false
		}
		return hydrateScoutRoute(cfg, route, true), true
	}
	provider := strings.ToLower(strings.TrimSpace(cfg.LCAgentProvider))
	explicit := strings.TrimSpace(cfg.EmbeddedLCAgentModel) != "" ||
		strings.TrimSpace(cfg.LCAgentEnvFile) != "" ||
		(provider != "" && provider != "openrouter")
	if !explicit {
		return lcagent.ScoutRoute{}, false
	}
	if provider == "" {
		provider = "openrouter"
	}
	route := lcagent.ScoutRoute{
		Source:          "lcagent_override",
		Description:     "explicit LCAgent provider/model route",
		Provider:        provider,
		Model:           strings.TrimSpace(cfg.EmbeddedLCAgentModel),
		ReasoningEffort: strings.TrimSpace(cfg.EmbeddedLCAgentReasoning),
		RequestTimeout:  cfg.LCAgentRequestTimeout,
	}
	return hydrateScoutRoute(cfg, route, true), true
}

func inheritedScoutRoute(cfg config.AppConfig, backend config.AIBackend, modelName, source, description string) (lcagent.ScoutRoute, bool) {
	provider := scoutProviderForBackend(backend)
	if provider == "" {
		return lcagent.ScoutRoute{}, false
	}
	route := lcagent.ScoutRoute{
		Source:         strings.TrimSpace(source),
		Description:    strings.TrimSpace(description),
		Provider:       provider,
		Model:          strings.TrimSpace(modelName),
		RequestTimeout: bossAssistantHTTPTimeout,
	}
	route = hydrateScoutRoute(cfg, route, false)
	if routeNeedsAPIKey(route.Provider) && strings.TrimSpace(route.APIKey) == "" {
		return lcagent.ScoutRoute{}, false
	}
	if route.Provider == "mlx" && route.Model == "" {
		return lcagent.ScoutRoute{}, false
	}
	return route, true
}

func hydrateScoutRoute(cfg config.AppConfig, route lcagent.ScoutRoute, explicit bool) lcagent.ScoutRoute {
	provider := strings.ToLower(strings.TrimSpace(route.Provider))
	route.Provider = provider
	if explicit {
		route.EnvFile = strings.TrimSpace(cfg.LCAgentEnvFile)
		if route.RequestTimeout <= 0 {
			route.RequestTimeout = cfg.LCAgentRequestTimeout
		}
	}
	if route.RequestTimeout <= 0 {
		route.RequestTimeout = 90 * time.Second
	}
	switch provider {
	case "openai", "openai_api":
		route.Provider = "openai"
		route.APIKey = strings.TrimSpace(cfg.OpenAIAPIKey)
	case "openrouter":
		route.APIKey = strings.TrimSpace(cfg.OpenRouterAPIKey)
		route.BaseURL = config.AIBackendOpenRouter.DefaultOpenAICompatibleBaseURL()
	case "deepseek":
		route.APIKey = strings.TrimSpace(cfg.DeepSeekAPIKey)
		route.BaseURL = config.AIBackendDeepSeek.DefaultOpenAICompatibleBaseURL()
	case "moonshot":
		route.APIKey = strings.TrimSpace(cfg.MoonshotAPIKey)
		route.BaseURL = config.AIBackendMoonshot.DefaultOpenAICompatibleBaseURL()
	case "xiaomi":
		route.APIKey = strings.TrimSpace(cfg.XiaomiAPIKey)
		route.BaseURL = cfg.OpenAICompatibleBaseURL(config.AIBackendXiaomi)
	case "mlx":
		route.APIKey = cfg.OpenAICompatibleAPIKey(config.AIBackendMLX)
		route.BaseURL = cfg.OpenAICompatibleBaseURL(config.AIBackendMLX)
	case "ollama":
		route.APIKey = cfg.OpenAICompatibleAPIKey(config.AIBackendOllama)
		route.BaseURL = cfg.OpenAICompatibleBaseURL(config.AIBackendOllama)
	}
	return route
}

func scoutProviderForBackend(backend config.AIBackend) string {
	switch backend {
	case config.AIBackendOpenAIAPI:
		return "openai"
	case config.AIBackendOpenRouter, config.AIBackendDeepSeek, config.AIBackendMoonshot, config.AIBackendXiaomi, config.AIBackendMLX, config.AIBackendOllama:
		return string(backend)
	default:
		return ""
	}
}

func routeNeedsAPIKey(provider string) bool {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "openai", "openrouter", "deepseek", "moonshot", "xiaomi":
		return true
	default:
		return false
	}
}
