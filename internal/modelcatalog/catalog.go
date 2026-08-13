// Package modelcatalog is the single source of truth for which model names
// belong to which provider.
//
// It deliberately imports nothing from the rest of the tree. Both config and
// lcagent/modeladapter depend on it, which is what lets a provider/model pair
// be validated at config-load time. Before this package existed the registry
// lived in modeladapter, which imports config; that cycle meant config could
// not check its own model fields, so the only mismatch check ran inside one
// settings widget and any other write path silently produced a config that
// posts an OpenAI model name to DeepSeek.
package modelcatalog

import "strings"

// Canonical model identifiers. Names here say which tier they are so that a
// constant cannot be mistaken for its sibling: config and modeladapter both
// used to export DefaultDeepSeekModel with *different* values (flash vs pro).
const (
	OpenAIDefaultModel    = "gpt-5.6"
	OpenAIUtilityModel    = "gpt-5.6-luna"
	DeepSeekProModel      = "deepseek-v4-pro"
	DeepSeekFlashModel    = "deepseek-v4-flash"
	MoonshotModel         = "kimi-k2.7-code"
	XiaomiProModel        = "mimo-v2.5-pro"
	XiaomiUtilityModel    = "mimo-v2.5"
	OpenRouterViaDeepSeek = "deepseek/deepseek-v4-pro"
)

// Provider identifiers, canonical lowercase form.
const (
	ProviderOpenAI     = "openai"
	ProviderDeepSeek   = "deepseek"
	ProviderMoonshot   = "moonshot"
	ProviderXiaomi     = "xiaomi"
	ProviderOpenRouter = "openrouter"
	ProviderOllama     = "ollama"
	ProviderMLX        = "mlx"
)

// Canonical normalizes a provider identifier for lookup.
func Canonical(provider string) string {
	return strings.ToLower(strings.TrimSpace(provider))
}

// IsOpenEnded reports whether a provider accepts arbitrary model identifiers.
// Routers and self-hosted runtimes serve whatever the user has pulled or
// whatever the router proxies, so there is no set to validate against and a
// strict check would produce false alarms. Validation must stay permissive here.
func IsOpenEnded(provider string) bool {
	switch Canonical(provider) {
	case ProviderOpenRouter, ProviderOllama, ProviderMLX, "":
		return true
	default:
		return false
	}
}

// Normalize strips a provider-qualified prefix ("deepseek/deepseek-v4-pro")
// down to the bare model id the provider's own API expects.
func Normalize(provider, model string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		return ""
	}
	switch Canonical(provider) {
	case ProviderOpenAI:
		return trimPrefix(model, "openai/")
	case ProviderDeepSeek:
		return trimPrefix(model, "deepseek/")
	case ProviderMoonshot:
		return trimPrefix(trimPrefix(model, "moonshot/"), "moonshotai/")
	case ProviderXiaomi:
		return trimPrefix(model, "xiaomi/")
	default:
		return model
	}
}

// NormalizeForRequest returns the model identifier that should be sent to a
// provider. Matching direct-provider prefixes are removed, and identifiers
// from the known catalog are returned in their canonical lowercase form.
// Unknown identifiers keep their spelling because direct providers may expose
// valid custom or newly released models that are not in this static catalog.
func NormalizeForRequest(provider, model string) string {
	normalized := Normalize(provider, model)
	switch Canonical(provider) {
	case ProviderOpenAI, ProviderDeepSeek, ProviderMoonshot, ProviderXiaomi:
		if IsKnown(provider, normalized) {
			return strings.ToLower(normalized)
		}
	}
	return normalized
}

// IsKnown reports whether model is a recognized identifier for provider.
// Open-ended providers always report true; an empty model never does.
func IsKnown(provider, model string) bool {
	provider = Canonical(provider)
	if strings.TrimSpace(model) == "" {
		return false
	}
	if IsOpenEnded(provider) {
		return true
	}
	normalized := strings.ToLower(Normalize(provider, model))
	switch provider {
	case ProviderOpenAI:
		switch normalized {
		case OpenAIDefaultModel, "gpt-5.6-sol", "gpt-5.6-terra", OpenAIUtilityModel,
			"gpt-5.5", "gpt-5.4", "gpt-5.4-mini", "gpt-5.4-nano":
			return true
		}
		for _, prefix := range []string{
			"gpt-5.6-", "gpt-5.6-sol-", "gpt-5.6-terra-", "gpt-5.6-luna-",
			"gpt-5.5-", "gpt-5.4-", "gpt-5.4-mini-", "gpt-5.4-nano-",
		} {
			if hasDatedSuffix(normalized, prefix) {
				return true
			}
		}
		return false
	case ProviderDeepSeek:
		return normalized == DeepSeekProModel || normalized == DeepSeekFlashModel
	case ProviderMoonshot:
		return normalized == MoonshotModel || normalized == "kimi-k2.6" ||
			normalized == "kimi-k3" || strings.HasPrefix(normalized, "kimi-k3-")
	case ProviderXiaomi:
		return normalized == XiaomiUtilityModel || normalized == XiaomiProModel ||
			strings.HasPrefix(normalized, "mimo-v2.5-")
	default:
		// Unknown provider: nothing to validate against, stay permissive.
		return true
	}
}

// Accepted lists the model identifiers a provider is known to serve, for use in
// error messages. It returns nil for open-ended or unrecognized providers,
// which is the signal that the pair must not be reported as a mismatch.
func Accepted(provider string) []string {
	switch Canonical(provider) {
	case ProviderOpenAI:
		return []string{OpenAIDefaultModel, "gpt-5.6-sol", "gpt-5.6-terra", OpenAIUtilityModel,
			"gpt-5.5", "gpt-5.4", "gpt-5.4-mini", "gpt-5.4-nano"}
	case ProviderDeepSeek:
		return []string{DeepSeekProModel, DeepSeekFlashModel}
	case ProviderMoonshot:
		return []string{MoonshotModel, "kimi-k2.6", "kimi-k3"}
	case ProviderXiaomi:
		return []string{XiaomiProModel, XiaomiUtilityModel}
	default:
		return nil
	}
}

// ProviderForModel resolves which provider a model identifier belongs to, or ""
// when no enumerable provider claims it. This is what turns "this is wrong"
// into "this looks like an OpenAI model", so the error can name the likely cause.
func ProviderForModel(model string) string {
	if strings.TrimSpace(model) == "" {
		return ""
	}
	for _, provider := range []string{ProviderOpenAI, ProviderDeepSeek, ProviderMoonshot, ProviderXiaomi} {
		if IsKnown(provider, model) {
			return provider
		}
	}
	return ""
}

func trimPrefix(model, prefix string) string {
	if len(model) > len(prefix) && strings.EqualFold(model[:len(prefix)], prefix) {
		return strings.TrimSpace(model[len(prefix):])
	}
	return model
}

// hasDatedSuffix matches a pinned release such as "gpt-5.6-2026-05-01": the
// prefix followed by something that starts with a digit.
func hasDatedSuffix(model, prefix string) bool {
	if !strings.HasPrefix(model, prefix) {
		return false
	}
	suffix := strings.TrimPrefix(model, prefix)
	return suffix != "" && suffix[0] >= '0' && suffix[0] <= '9'
}
