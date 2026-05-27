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
		return AIBackendUnset, fmt.Errorf("ai_backend must be one of disabled, openai_api, openrouter, deepseek, moonshot, codex, opencode, claude_code, mlx, or ollama")
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
	case string(AIBackendMLX):
		return AIBackendMLX, nil
	case string(AIBackendOllama):
		return AIBackendOllama, nil
	default:
		return AIBackendUnset, fmt.Errorf("boss_chat_backend must be one of disabled, openai_api, openrouter, deepseek, moonshot, mlx, or ollama")
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

func (b AIBackend) UsesOpenAICompatibleAPI() bool {
	switch b {
	case AIBackendOpenRouter, AIBackendDeepSeek, AIBackendMoonshot, AIBackendMLX, AIBackendOllama:
		return true
	default:
		return false
	}
}

func (b AIBackend) UsesCloudAPIKey() bool {
	switch b {
	case AIBackendOpenAIAPI, AIBackendOpenRouter, AIBackendDeepSeek, AIBackendMoonshot:
		return true
	default:
		return false
	}
}
