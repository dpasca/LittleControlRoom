package boss

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"lcroom/internal/brand"
	"lcroom/internal/config"
	"lcroom/internal/llm"
	"lcroom/internal/model"
	"lcroom/internal/service"
)

const bossAssistantReasoningEffort = "low"

type ChatMessage struct {
	Role    string
	Content string
	At      time.Time
}

type AssistantRequest struct {
	StateBrief string
	Messages   []ChatMessage
}

type AssistantResponse struct {
	Content string
	Model   string
	Usage   model.LLMUsage
}

type Assistant struct {
	runner  llm.TextRunner
	model   string
	backend config.AIBackend
}

func NewAssistant(svc *service.Service) *Assistant {
	if svc == nil {
		return &Assistant{}
	}
	runner, modelName, backend := svc.NewBossTextRunner()
	return &Assistant{
		runner:  runner,
		model:   strings.TrimSpace(modelName),
		backend: backend,
	}
}

func (a *Assistant) Configured() bool {
	return a != nil && a.runner != nil && strings.TrimSpace(a.model) != ""
}

func (a *Assistant) Label() string {
	if a == nil {
		return "Mina offline"
	}
	if !a.Configured() {
		switch a.backend {
		case config.AIBackendOpenAIAPI:
			return "Mina needs an OpenAI API key"
		case config.AIBackendUnset:
			return "Mina needs an AI backend"
		default:
			return "Mina needs OpenAI API chat"
		}
	}
	return fmt.Sprintf("Mina via %s", a.model)
}

func (a *Assistant) Reply(ctx context.Context, req AssistantRequest) (AssistantResponse, error) {
	if a == nil || a.runner == nil {
		backend := config.AIBackendUnset
		if a != nil {
			backend = a.backend
		}
		return AssistantResponse{}, errors.New(unconfiguredAssistantMessage(backend))
	}
	modelName := strings.TrimSpace(a.model)
	if modelName == "" {
		return AssistantResponse{}, errors.New("Mina needs a chat model; set " + brand.BossAssistantModelEnvVar)
	}

	messages := []llm.TextMessage{{
		Role:    "user",
		Content: "Current app state brief:\n" + strings.TrimSpace(req.StateBrief),
	}}
	for _, message := range trimChatHistory(req.Messages, 16) {
		content := strings.TrimSpace(message.Content)
		if content == "" {
			continue
		}
		messages = append(messages, llm.TextMessage{
			Role:    normalizeChatRole(message.Role),
			Content: content,
		})
	}

	resp, err := a.runner.RunText(ctx, llm.TextRequest{
		Model:           modelName,
		SystemText:      bossAssistantSystemPrompt(),
		Messages:        messages,
		ReasoningEffort: bossAssistantReasoningEffort,
	})
	if err != nil {
		return AssistantResponse{}, err
	}
	return AssistantResponse{
		Content: strings.TrimSpace(resp.OutputText),
		Model:   strings.TrimSpace(resp.Model),
		Usage:   resp.Usage,
	}, nil
}

func unconfiguredAssistantMessage(backend config.AIBackend) string {
	switch backend {
	case config.AIBackendOpenAIAPI:
		return "Mina is not connected yet. Configure an OpenAI API key in /settings, then reopen boss mode."
	case config.AIBackendCodex, config.AIBackendOpenCode, config.AIBackendClaude:
		return "Mina chat currently uses direct API inference, not embedded coding-agent sessions. Switch AI backend to openai_api for this first boss-mode prototype."
	default:
		return "Mina is not connected yet. Boss mode currently supports OpenAI API chat; configure ai_backend = \"openai_api\" and openai_api_key."
	}
}

func bossAssistantSystemPrompt() string {
	return strings.Join([]string{
		"You are Mina, the calm project-management assistant inside Little Control Room.",
		"Help the user decide what deserves attention across coding projects.",
		"Use the compact app-state brief, but do not invent facts that are not present there.",
		"Keep replies concise, concrete, and friendly. Prefer clear next steps over dashboards.",
		"You cannot change projects or panels yet. If an action is needed, say what you would inspect or do next.",
		"The classic TUI remains available for detailed micromanagement.",
	}, "\n")
}

func trimChatHistory(messages []ChatMessage, limit int) []ChatMessage {
	if limit <= 0 || len(messages) <= limit {
		return append([]ChatMessage(nil), messages...)
	}
	return append([]ChatMessage(nil), messages[len(messages)-limit:]...)
}

func normalizeChatRole(role string) string {
	switch strings.TrimSpace(strings.ToLower(role)) {
	case "assistant":
		return "assistant"
	default:
		return "user"
	}
}
