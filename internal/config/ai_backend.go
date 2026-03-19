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
)

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
	default:
		return AIBackendUnset, fmt.Errorf("ai_backend must be one of disabled, openai_api, codex, or opencode")
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
	default:
		return "Not configured"
	}
}
