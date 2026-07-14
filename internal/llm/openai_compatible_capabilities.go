package llm

import "strings"

const xiaomiStructuredOutputMaxCompletionTokens int64 = 16_384

type OpenAICompatibleChatResponseFormat string

const (
	OpenAICompatibleChatResponseFormatJSONSchema OpenAICompatibleChatResponseFormat = "json_schema"
	OpenAICompatibleChatResponseFormatJSONObject OpenAICompatibleChatResponseFormat = "json_object"
	OpenAICompatibleChatResponseFormatPromptOnly OpenAICompatibleChatResponseFormat = "prompt_only"
)

type OpenAICompatibleProviderModelProfile struct {
	ProviderID            string
	Model                 string
	ChatResponseFormat    OpenAICompatibleChatResponseFormat
	AuthHeader            OpenAICompatibleAuthHeader
	ReasoningStyle        string
	RequireParameters     bool
	PreferChatCompletions bool
	ChatMaxOutputTokens   int64
	ChatMaxTokensField    string
}

type openAICompatibleProviderModelRule struct {
	providerID          string
	model               string
	modelPrefix         string
	chatResponseFormat  OpenAICompatibleChatResponseFormat
	authHeader          OpenAICompatibleAuthHeader
	reasoningStyle      string
	requireParameters   bool
	preferChat          bool
	chatMaxOutputTokens int64
	chatMaxTokensField  string
}

var openAICompatibleProviderModelRules = []openAICompatibleProviderModelRule{
	{
		providerID:         "deepseek",
		chatResponseFormat: OpenAICompatibleChatResponseFormatJSONObject,
		reasoningStyle:     "deepseek",
	},
	{
		// Kimi's stable API guarantees parseable JSON objects, but does not
		// document response_format.json_schema support.
		providerID:         "moonshot",
		chatResponseFormat: OpenAICompatibleChatResponseFormatJSONObject,
	},
	{
		// MiMo's structured-output transport is JSON mode; the schema is
		// supplied in the prompt instead of response_format.json_schema.
		providerID:          "xiaomi",
		chatResponseFormat:  OpenAICompatibleChatResponseFormatJSONObject,
		authHeader:          OpenAICompatibleAuthHeaderAPIKey,
		reasoningStyle:      "xiaomi",
		chatMaxOutputTokens: xiaomiStructuredOutputMaxCompletionTokens,
		chatMaxTokensField:  "max_completion_tokens",
	},
	{
		providerID:        "openrouter",
		reasoningStyle:    "openai",
		requireParameters: true,
	},
	{
		// mlx-lm's documented server accepts prompt/sampling fields but does
		// not expose response_format. Keep the schema in the prompt and omit
		// an unsupported transport hint.
		providerID:         "mlx",
		chatResponseFormat: OpenAICompatibleChatResponseFormatPromptOnly,
		preferChat:         true,
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
		if rule.requireParameters {
			profile.RequireParameters = true
		}
		if rule.preferChat {
			profile.PreferChatCompletions = true
		}
		if rule.chatMaxOutputTokens > 0 {
			profile.ChatMaxOutputTokens = rule.chatMaxOutputTokens
		}
		if strings.TrimSpace(rule.chatMaxTokensField) != "" {
			profile.ChatMaxTokensField = strings.TrimSpace(rule.chatMaxTokensField)
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
	if profile.RequireParameters {
		opts.RequireParameters = true
	}
	if profile.PreferChatCompletions {
		opts.PreferChatCompletions = true
	}
	if profile.ChatMaxOutputTokens > 0 {
		opts.ChatMaxOutputTokens = profile.ChatMaxOutputTokens
	}
	if strings.TrimSpace(profile.ChatMaxTokensField) != "" {
		opts.ChatMaxTokensField = strings.TrimSpace(profile.ChatMaxTokensField)
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
	case OpenAICompatibleChatResponseFormatPromptOnly:
		return OpenAICompatibleChatResponseFormatPromptOnly
	default:
		return OpenAICompatibleChatResponseFormatJSONSchema
	}
}
