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
}

type openAICompatibleProviderModelRule struct {
	providerID         string
	model              string
	modelPrefix        string
	chatResponseFormat OpenAICompatibleChatResponseFormat
}

var openAICompatibleProviderModelRules = []openAICompatibleProviderModelRule{
	{
		providerID:         "deepseek",
		chatResponseFormat: OpenAICompatibleChatResponseFormatJSONObject,
	},
}

func OpenAICompatibleProviderModelProfileForProviderModel(providerID, model string) OpenAICompatibleProviderModelProfile {
	providerID = strings.ToLower(strings.TrimSpace(providerID))
	model = strings.TrimSpace(model)
	profile := OpenAICompatibleProviderModelProfile{
		ProviderID:         providerID,
		Model:              model,
		ChatResponseFormat: OpenAICompatibleChatResponseFormatJSONSchema,
	}

	for _, rule := range openAICompatibleProviderModelRules {
		if !rule.matches(providerID, model) {
			continue
		}
		if rule.chatResponseFormat != "" {
			profile.ChatResponseFormat = rule.chatResponseFormat
		}
	}

	return profile
}

func OpenAICompatibleResponsesRunnerOptionsForProviderModel(providerID, model string, opts OpenAICompatibleResponsesRunnerOptions) OpenAICompatibleResponsesRunnerOptions {
	profile := OpenAICompatibleProviderModelProfileForProviderModel(providerID, model)
	if profile.ChatResponseFormat != "" {
		opts.ChatResponseFormat = profile.ChatResponseFormat
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
