package config

import (
	"fmt"
	"strings"
)

type AIBackend string

const (
	AIBackendUnset     AIBackend = ""
	AIBackendDisabled  AIBackend = "disabled"
	AIBackendOpenAIAPI AIBackend = "openai_api"
	AIBackendCodex     AIBackend = "codex"
	AIBackendOpenCode  AIBackend = "opencode"
	AIBackendClaude    AIBackend = "claude_code"
	AIBackendMLX       AIBackend = "mlx"
	AIBackendOllama    AIBackend = "ollama"
)

var selectableAIBackends = []AIBackend{
	AIBackendCodex,
	AIBackendOpenCode,
	AIBackendClaude,
	AIBackendMLX,
	AIBackendOllama,
	AIBackendOpenAIAPI,
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
		return AIBackendUnset, fmt.Errorf("ai_backend must be one of disabled, openai_api, codex, opencode, claude_code, mlx, or ollama")
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
	default:
		return AIBackendUnset, fmt.Errorf("boss_chat_backend must be one of disabled or openai_api")
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
		return "OpenAI API key"
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
