package config

import (
	"fmt"
	"strings"
)

type AIBackend string

const (
	AIBackendUnset      AIBackend = ""
	AIBackendDisabled   AIBackend = "disabled"
	AIBackendOpenAIAPI  AIBackend = "openai_api"
	AIBackendOpenRouter AIBackend = "openrouter"
	AIBackendDeepSeek   AIBackend = "deepseek"
	AIBackendMoonshot   AIBackend = "moonshot"
	AIBackendCodex      AIBackend = "codex"
	AIBackendOpenCode   AIBackend = "opencode"
	AIBackendClaude     AIBackend = "claude_code"
	AIBackendMLX        AIBackend = "mlx"
	AIBackendOllama     AIBackend = "ollama"
	AIBackendXiaomi     AIBackend = "xiaomi"
	AIBackendZai        AIBackend = "zai"
)

var selectableAIBackends = []AIBackend{
	AIBackendCodex,
	AIBackendOpenCode,
	AIBackendClaude,
	AIBackendMLX,
	AIBackendOllama,
	AIBackendOpenAIAPI,
	AIBackendOpenRouter,
	AIBackendDeepSeek,
	AIBackendMoonshot,
	AIBackendXiaomi,
	AIBackendZai,
	AIBackendDisabled,
}

type ModelTier string

const (
	ModelTierFree     ModelTier = "free"
	ModelTierCheap    ModelTier = "cheap"
	ModelTierBalanced ModelTier = "balanced"
)

func ParseModelTier(raw string) (ModelTier, error) {
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case "", "free":
		return ModelTierFree, nil
	case "cheap":
		return ModelTierCheap, nil
	case "balanced":
		return ModelTierBalanced, nil
	default:
		return ModelTierFree, fmt.Errorf("model_tier must be one of free, cheap, or balanced")
	}
}

func ParseAIBackend(raw string) (AIBackend, error) {
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case "":
		return AIBackendUnset, nil
	case string(AIBackendDisabled):
		return AIBackendDisabled, nil
	case string(AIBackendOpenAIAPI):
		return AIBackendOpenAIAPI, nil
	case string(AIBackendOpenRouter):
		return AIBackendOpenRouter, nil
	case string(AIBackendDeepSeek):
		return AIBackendDeepSeek, nil
	case string(AIBackendMoonshot):
		return AIBackendMoonshot, nil
	case string(AIBackendXiaomi):
		return AIBackendXiaomi, nil
	case string(AIBackendZai):
		return AIBackendZai, nil
	case string(AIBackendCodex):
		return AIBackendCodex, nil
	case string(AIBackendOpenCode):
		return AIBackendOpenCode, nil
	case string(AIBackendClaude):
		return AIBackendClaude, nil
	case string(AIBackendMLX):
		return AIBackendMLX, nil
	case string(AIBackendOllama):
		return AIBackendOllama, nil
	default:
		return AIBackendUnset, fmt.Errorf("ai_backend must be one of disabled, openai_api, openrouter, deepseek, moonshot, xiaomi, zai, codex, opencode, claude_code, mlx, or ollama")
	}
}

func ResolveAIBackend(selected AIBackend, openAIAPIKey string) AIBackend {
	if backend, err := ParseAIBackend(string(selected)); err == nil && backend != AIBackendUnset {
		return backend
	}
	if strings.TrimSpace(openAIAPIKey) != "" {
		return AIBackendOpenAIAPI
	}
	return AIBackendUnset
}

func ParseBossChatBackend(raw string) (AIBackend, error) {
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case "":
		return AIBackendUnset, nil
	case string(AIBackendDisabled):
		return AIBackendDisabled, nil
	case string(AIBackendOpenAIAPI):
		return AIBackendOpenAIAPI, nil
	case string(AIBackendOpenRouter):
		return AIBackendOpenRouter, nil
	case string(AIBackendDeepSeek):
		return AIBackendDeepSeek, nil
	case string(AIBackendMoonshot):
		return AIBackendMoonshot, nil
	case string(AIBackendXiaomi):
		return AIBackendXiaomi, nil
	case string(AIBackendZai):
		return AIBackendZai, nil
	case string(AIBackendMLX):
		return AIBackendMLX, nil
	case string(AIBackendOllama):
		return AIBackendOllama, nil
	default:
		return AIBackendUnset, fmt.Errorf("boss_chat_backend must be one of disabled, openai_api, openrouter, deepseek, moonshot, xiaomi, zai, mlx, or ollama")
	}
}

func ResolveBossChatBackend(selected AIBackend, openAIAPIKey string) AIBackend {
	if backend, err := ParseBossChatBackend(string(selected)); err == nil && backend != AIBackendUnset {
		return backend
	}
	if strings.TrimSpace(openAIAPIKey) != "" {
		return AIBackendOpenAIAPI
	}
	return AIBackendUnset
}

func (b AIBackend) Label() string {
	switch b {
	case AIBackendDisabled:
		return "Disabled"
	case AIBackendOpenAIAPI:
		return "OpenAI API"
	case AIBackendOpenRouter:
		return "OpenRouter"
	case AIBackendDeepSeek:
		return "DeepSeek"
	case AIBackendMoonshot:
		return "Moonshot"
	case AIBackendCodex:
		return "Codex"
	case AIBackendOpenCode:
		return "OpenCode"
	case AIBackendClaude:
		return "Claude Code"
	case AIBackendMLX:
		return "MLX"
	case AIBackendOllama:
		return "Ollama"
	case AIBackendXiaomi:
		return "Xiaomi MiMo"
	case AIBackendZai:
		return "Z.ai GLM"
	default:
		return "Not configured"
	}
}

func SelectableAIBackends() []AIBackend {
	out := make([]AIBackend, len(selectableAIBackends))
	copy(out, selectableAIBackends)
	return out
}

func (b AIBackend) UsesLocalProviderPath() bool {
	switch b {
	case AIBackendCodex, AIBackendOpenCode, AIBackendClaude, AIBackendMLX, AIBackendOllama:
		return true
	default:
		return false
	}
}

func (b AIBackend) SupportsModelTier() bool {
	return b == AIBackendOpenCode
}

func (b AIBackend) RequiresCLIInstallHint() bool {
	switch b {
	case AIBackendCodex, AIBackendOpenCode, AIBackendClaude:
		return true
	default:
		return false
	}
}

func (b AIBackend) DefaultOpenAICompatibleBaseURL() string {
	switch b {
	case AIBackendOpenRouter:
		return "https://openrouter.ai/api/v1"
	case AIBackendDeepSeek:
		return "https://api.deepseek.com"
	case AIBackendMoonshot:
		return "https://api.moonshot.ai/v1"
	case AIBackendMLX:
		return "http://127.0.0.1:8080/v1"
	case AIBackendOllama:
		return "http://127.0.0.1:11434/v1"
	case AIBackendXiaomi:
		return "https://api.xiaomimimo.com/v1"
	case AIBackendZai:
		return "https://api.z.ai/api/paas/v4"
	default:
		return ""
	}
}

func (b AIBackend) DefaultOpenAICompatibleAPIKey() string {
	switch b {
	case AIBackendMLX:
		return "mlx"
	case AIBackendOllama:
		return "ollama"
	default:
		return ""
	}
}

func (b AIBackend) DefaultProjectModel() string {
	switch b {
	case AIBackendOpenAIAPI:
		return DefaultOpenAIProjectModel
	case AIBackendOpenRouter:
		return DefaultOpenRouterModel
	case AIBackendDeepSeek:
		return DefaultDeepSeekModel
	case AIBackendMoonshot:
		return DefaultMoonshotModel
	case AIBackendXiaomi:
		return DefaultXiaomiModel
	case AIBackendZai:
		return DefaultZaiModel
	default:
		return ""
	}
}

func (b AIBackend) DefaultBossHelmModel() string {
	switch b {
	case AIBackendOpenRouter:
		return DefaultOpenRouterModel
	case AIBackendDeepSeek:
		return DefaultDeepSeekProModel
	case AIBackendMoonshot:
		return DefaultMoonshotModel
	case AIBackendXiaomi:
		return DefaultXiaomiProModel
	case AIBackendZai:
		return DefaultZaiProModel
	default:
		return DefaultBossHelmModel
	}
}

func (b AIBackend) DefaultBossUtilityModel() string {
	switch b {
	case AIBackendOpenRouter:
		return DefaultOpenRouterModel
	case AIBackendDeepSeek:
		return DefaultDeepSeekModel
	case AIBackendMoonshot:
		return DefaultMoonshotModel
	case AIBackendXiaomi:
		return DefaultXiaomiModel
	case AIBackendZai:
		return DefaultZaiModel
	default:
		return DefaultBossUtilityModel
	}
}

func LooksLikeXiaomiTokenPlanAPIKey(apiKey string) bool {
	key := strings.ToLower(strings.TrimSpace(apiKey))
	return strings.HasPrefix(key, "tc") || strings.HasPrefix(key, "tp-") || strings.HasPrefix(key, "tp_")
}

func LooksLikeXiaomiTokenPlanBaseURL(baseURL string) bool {
	normalized := strings.ToLower(strings.TrimSpace(baseURL))
	return strings.Contains(normalized, "token-plan-") && strings.Contains(normalized, "xiaomimimo.com")
}

func LooksLikeRegularXiaomiBaseURL(baseURL string) bool {
	normalized := strings.TrimRight(strings.ToLower(strings.TrimSpace(baseURL)), "/")
	regular := strings.TrimRight(strings.ToLower(AIBackendXiaomi.DefaultOpenAICompatibleBaseURL()), "/")
	return normalized == "" || normalized == regular
}

func XiaomiTokenPlanBaseURLHint() string {
	return "Use the regional Token Plan base URL from Xiaomi subscription management, for example https://token-plan-sgp.xiaomimimo.com/v1."
}

// ZaiCodingPlanBaseURL is the dedicated GLM Coding Plan endpoint Z.ai documents
// for coding tools. Subscription keys use it instead of the pay-as-you-go
// OpenAI-compatible base URL; the key format is the same for both.
const ZaiCodingPlanBaseURL = "https://api.z.ai/api/coding/paas/v4"

func ZaiCodingPlanBaseURLHint() string {
	return "GLM Coding Plan subscriptions use the dedicated coding endpoint " + ZaiCodingPlanBaseURL + "; pay-as-you-go Z.ai keys use https://api.z.ai/api/paas/v4."
}

func (b AIBackend) UsesOpenAICompatibleAPI() bool {
	switch b {
	case AIBackendOpenRouter, AIBackendDeepSeek, AIBackendMoonshot, AIBackendMLX, AIBackendOllama, AIBackendXiaomi, AIBackendZai:
		return true
	default:
		return false
	}
}

func (b AIBackend) UsesCloudAPIKey() bool {
	switch b {
	case AIBackendOpenAIAPI, AIBackendOpenRouter, AIBackendDeepSeek, AIBackendMoonshot, AIBackendXiaomi, AIBackendZai:
		return true
	default:
		return false
	}
}
