package service

import (
	"fmt"
	"strings"

	"lcroom/internal/config"
	"lcroom/internal/lcagent"
	"lcroom/internal/lcagent/modeladapter"
	"lcroom/internal/model"
)

// NewBossConversationModel builds the LCAgent provider adapter for the Chat
// surface while retaining Chat's independent backend, model, credentials, and
// usage accounting.
func (s *Service) NewBossConversationModel() (lcagent.ConversationModel, string, string, config.AIBackend, error) {
	if s == nil {
		return nil, "", "", config.AIBackendUnset, nil
	}
	s.mu.Lock()
	cfg := cloneAppConfig(s.cfg)
	usageTracker := s.bossChatUsageTracker
	s.mu.Unlock()

	backend := cfg.EffectiveBossChatBackend()
	provider := config.ProviderForBackend(backend)
	modelName := configuredBossHelmModelForBackend(cfg, backend)
	if provider == "" {
		return nil, modelName, provider, backend, nil
	}
	apiKey := cfg.OpenAICompatibleAPIKey(backend)
	if backend == config.AIBackendOpenAIAPI {
		apiKey = strings.TrimSpace(cfg.OpenAIAPIKey)
	}
	callbacks := lcagent.ConversationModelCallbacks{}
	if usageTracker != nil {
		callbacks = lcagent.ConversationModelCallbacks{
			Started: func(modelName string) {
				usageTracker.Start(modelName)
			},
			Completed: func(modelName string, usage model.LLMUsage) {
				usageTracker.Complete(modelName, usage)
			},
			Failed: func(modelName string) {
				usageTracker.Fail(modelName)
			},
		}
	}
	conversationModel, err := lcagent.NewConversationModel(provider, modeladapter.OpenRouterConfig{
		APIKey:          apiKey,
		BaseURL:         cfg.OpenAICompatibleBaseURL(backend),
		Model:           modelName,
		MaxTurns:        12,
		RequestTimeout:  bossAssistantHTTPTimeout,
		DisableThinking: backend == config.AIBackendOllama && !cfg.BossChatOllamaThinking,
	}, callbacks)
	if err != nil {
		return nil, modelName, provider, backend, fmt.Errorf("initialize Chat LCAgent model: %w", err)
	}
	return conversationModel, modelName, provider, backend, nil
}
