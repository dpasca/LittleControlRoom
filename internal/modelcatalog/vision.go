package modelcatalog

import "strings"

// SupportsImageInput records documented capabilities, not guesses from a model
// name. Unknown/new models can instead be enabled by the live image-input check.
func SupportsImageInput(provider, model string) bool {
	model = strings.TrimSpace(model)
	if Canonical(provider) == ProviderOpenRouter {
		prefix, _, found := strings.Cut(model, "/")
		if !found || Canonical(prefix) != ProviderDeepSeek {
			return false
		}
		provider = ProviderDeepSeek
	}
	return Canonical(provider) == ProviderDeepSeek && Canonical(Normalize(provider, model)) == DeepSeekLatestFlashModel
}

// ResolveVisionRoute prefers native pixels over a secondary vision route.
// Explicit off remains authoritative. mainVerified is scoped to the exact
// provider/model pair by the caller, never a property of an entire provider.
func ResolveVisionRoute(mainProvider, mainModel, fallbackProvider, fallbackModel string, mainVerified bool) (string, string) {
	provider := Canonical(fallbackProvider)
	if provider == "off" {
		return "off", ""
	}
	if SupportsImageInput(mainProvider, mainModel) || mainVerified {
		return "main", ""
	}
	if provider == "main" {
		return "main", fallbackModel
	}
	if provider == "" || provider == "auto" {
		provider = ProviderForModel(fallbackModel)
		if provider == "" {
			return "off", ""
		}
	}
	return provider, fallbackModel
}
