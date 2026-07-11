package llm

import "strings"

type OpenAICompatibleChatResponseFormat string

const (
	OpenAICompatibleChatResponseFormatJSONSchema OpenAICompatibleChatResponseFormat = "json_schema"
	OpenAICompatibleChatResponseFormatJSONObject OpenAICompatibleChatResponseFormat = "json_object"
)

type OpenAICompatibleProviderModelProfile struct {
	ProviderID         string
	Model              string
	ChatResponseFormat OpenAICompatibleChatResponseFormat
	AuthHeader         OpenAICompatibleAuthHeader
	ReasoningStyle     string
}

type openAICompatibleProviderModelRule struct {
	providerID         string
	model              string
	modelPrefix        string
	chatResponseFormat OpenAICompatibleChatResponseFormat
	authHeader         OpenAICompatibleAuthHeader
	reasoningStyle     string
}

var openAICompatibleProviderModelRules = []openAICompatibleProviderModelRule{
	{
		providerID:         "deepseek",
		chatResponseFormat: OpenAICompatibleChatResponseFormatJSONObject,
		reasoningStyle:     "deepseek",
	},
	{
		// MiMo's structured-output transport is JSON mode; the schema is
		// supplied in the prompt instead of response_format.json_schema.
		providerID:         "xiaomi",
		chatResponseFormat: OpenAICompatibleChatResponseFormatJSONObject,
		authHeader:         OpenAICompatibleAuthHeaderAPIKey,
		reasoningStyle:     "xiaomi",
	},
	{
		providerID:     "openrouter",
		reasoningStyle: "openai",
	},
}

func OpenAICompatibleProviderModelProfileForProviderModel(providerID, model string) OpenAICompatibleProviderModelProfile {
	providerID = strings.ToLower(strings.TrimSpace(providerID))
	model = strings.TrimSpace(model)
	profile := OpenAICompatibleProviderModelProfile{
		ProviderID:         providerID,
		Model:              model,
		ChatResponseFormat: OpenAICompatibleChatResponseFormatJSONSchema,
		AuthHeader:         OpenAICompatibleAuthHeaderBearer,
	}

	for _, rule := range openAICompatibleProviderModelRules {
		if !rule.matches(providerID, model) {
			continue
		}
		if rule.chatResponseFormat != "" {
			profile.ChatResponseFormat = rule.chatResponseFormat
		}
		if rule.authHeader != "" {
			profile.AuthHeader = rule.authHeader
		}
		if rule.reasoningStyle != "" {
			profile.ReasoningStyle = rule.reasoningStyle
		}
	}

	return profile
}

func OpenAICompatibleResponsesRunnerOptionsForProviderModel(providerID, model string, opts OpenAICompatibleResponsesRunnerOptions) OpenAICompatibleResponsesRunnerOptions {
	profile := OpenAICompatibleProviderModelProfileForProviderModel(providerID, model)
	if profile.ChatResponseFormat != "" {
		opts.ChatResponseFormat = profile.ChatResponseFormat
	}
	if profile.AuthHeader != "" {
		opts.AuthHeader = profile.AuthHeader
	}
	if profile.ReasoningStyle != "" {
		opts.ReasoningStyle = profile.ReasoningStyle
	}
	return opts
}

func (r openAICompatibleProviderModelRule) matches(providerID, model string) bool {
	if strings.TrimSpace(r.providerID) != "" && strings.ToLower(strings.TrimSpace(r.providerID)) != providerID {
		return false
	}
	if strings.TrimSpace(r.model) != "" && strings.TrimSpace(r.model) != model {
		return false
	}
	if strings.TrimSpace(r.modelPrefix) != "" && !strings.HasPrefix(model, strings.TrimSpace(r.modelPrefix)) {
		return false
	}
	return true
}

func normalizeOpenAICompatibleChatResponseFormat(format OpenAICompatibleChatResponseFormat) OpenAICompatibleChatResponseFormat {
	switch format {
	case OpenAICompatibleChatResponseFormatJSONObject:
		return OpenAICompatibleChatResponseFormatJSONObject
	default:
		return OpenAICompatibleChatResponseFormatJSONSchema
	}
}
