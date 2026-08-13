package config

import (
	"fmt"
	"strings"

	"lcroom/internal/modelcatalog"
)

// ModelMismatch describes a configured model that the configured provider does
// not serve. It carries enough context to tell the user which field is wrong,
// what the provider would have accepted, and what is being used instead.
type ModelMismatch struct {
	// Field is the config.toml key holding the bad model.
	Field string
	// ProviderField is the config.toml key that selected the provider.
	ProviderField string
	// Provider is the canonical provider the model was going to be sent to.
	Provider string
	// Model is the configured value that the provider will reject.
	Model string
	// LikelyProvider names the provider the model actually belongs to, when it
	// can be resolved. This is what identifies a stale value left behind by a
	// provider switch rather than a typo.
	LikelyProvider string
	// Accepted lists what the provider does serve.
	Accepted []string
	// Fallback is the model used instead for this run. Empty means the value was
	// left in place and the call will fail at request time.
	Fallback string
}

func (m ModelMismatch) Error() string {
	return fmt.Sprintf("%s=%q is not a valid model for %s=%q", m.Field, m.Model, m.ProviderField, m.Provider)
}

// Detail renders the multi-line operator-facing form: what broke, why, what is
// happening about it, and how to fix it permanently.
func (m ModelMismatch) Detail() string {
	var b strings.Builder
	fmt.Fprintf(&b, "config: %s=%q is not valid for %s=%q", m.Field, m.Model, m.ProviderField, m.Provider)
	if m.LikelyProvider != "" {
		fmt.Fprintf(&b, "\n  %q belongs to provider %q; it was probably left behind when the provider changed", m.Model, m.LikelyProvider)
	}
	if len(m.Accepted) > 0 {
		fmt.Fprintf(&b, "\n  accepted: %s", strings.Join(m.Accepted, ", "))
	}
	if m.Fallback != "" {
		fmt.Fprintf(&b, "\n  using %q instead whenever this route runs", m.Fallback)
	}
	fmt.Fprintf(&b, "\n  fix: set %s to an accepted value, or clear it to accept the provider default", m.Field)
	return b.String()
}

// ProviderForBackend maps an AIBackend onto a modelcatalog provider.
//
// It returns "" for backends that are not direct model APIs — the agent CLIs
// (codex, opencode, claude_code) carry their own model selection and auth, and
// disabled/unset select nothing. Those must not be validated here; reporting a
// mismatch for them would be a false alarm.
func ProviderForBackend(backend AIBackend) string {
	switch backend {
	case AIBackendOpenAIAPI:
		return modelcatalog.ProviderOpenAI
	case AIBackendDeepSeek:
		return modelcatalog.ProviderDeepSeek
	case AIBackendMoonshot:
		return modelcatalog.ProviderMoonshot
	case AIBackendXiaomi:
		return modelcatalog.ProviderXiaomi
	case AIBackendOpenRouter:
		return modelcatalog.ProviderOpenRouter
	case AIBackendOllama:
		return modelcatalog.ProviderOllama
	case AIBackendMLX:
		return modelcatalog.ProviderMLX
	default:
		return ""
	}
}

// NormalizeModelForProvider returns the identifier a provider's API expects.
// Known catalog entries are canonicalized, while custom model identifiers are
// preserved apart from a matching direct-provider prefix.
func NormalizeModelForProvider(provider, model string) string {
	return modelcatalog.NormalizeForRequest(provider, model)
}

// NormalizeModelForBackend is the AIBackend form of
// NormalizeModelForProvider. Agent CLIs and disabled backends have no direct
// provider contract, so their model strings are only trimmed.
func NormalizeModelForBackend(backend AIBackend, model string) string {
	provider := ProviderForBackend(backend)
	if provider == "" {
		return strings.TrimSpace(model)
	}
	return NormalizeModelForProvider(provider, model)
}

// CheckModel reports a mismatch when provider is known to not serve model.
//
// It returns nil — meaning "do not complain" — whenever validation cannot be
// meaningful: no provider selected, no model configured (the default applies),
// or an open-ended provider that serves arbitrary ids. Being permissive in
// those cases is deliberate; a false mismatch that blocks a valid custom model
// is worse than the silence this whole check exists to remove.
func CheckModel(field, providerField, provider, model, fallback string) *ModelMismatch {
	provider = modelcatalog.Canonical(provider)
	model = strings.TrimSpace(model)
	if provider == "" || model == "" {
		return nil
	}
	if modelcatalog.IsKnown(provider, model) {
		return nil
	}
	likelyProvider := modelcatalog.ProviderForModel(modelcatalog.NormalizeForRequest(provider, model))
	if likelyProvider == "" || likelyProvider == provider {
		// A static catalog can prove that a known model belongs to another
		// provider, but it cannot prove that an unknown identifier is invalid.
		// Direct providers can expose custom and newly released model ids.
		return nil
	}
	accepted := modelcatalog.Accepted(provider)
	if len(accepted) == 0 {
		// Provider has no enumerable model set; nothing to validate against.
		return nil
	}
	return &ModelMismatch{
		Field:          field,
		ProviderField:  providerField,
		Provider:       provider,
		Model:          model,
		LikelyProvider: likelyProvider,
		Accepted:       accepted,
		Fallback:       strings.TrimSpace(fallback),
	}
}

// ModelMismatches returns every provider/model divergence in the config.
//
// This is the check that makes a stale model impossible to hold silently: it
// covers all configured pairs, not just the one a settings widget happens to
// touch, and it runs off the loaded config so any write path is caught.
func (c AppConfig) ModelMismatches() []ModelMismatch {
	var out []ModelMismatch
	add := func(m *ModelMismatch) {
		if m != nil {
			out = append(out, *m)
		}
	}

	chatBackend := c.EffectiveBossChatBackend()
	chatProvider := ProviderForBackend(chatBackend)
	add(CheckModel("boss_helm_model", "boss_chat_backend", chatProvider,
		firstNonEmptyTrimmed(c.BossHelmModel, c.BossChatModel), chatBackend.DefaultBossHelmModel()))
	add(CheckModel("boss_utility_model", "boss_chat_backend", chatProvider,
		c.BossUtilityModel, chatBackend.DefaultBossUtilityModel()))

	// The per-provider project model fields are scoped by their own name, not by
	// whichever ai_backend happens to be selected: deepseek_model is always a
	// DeepSeek model even while ai_backend is openai_api. Validating these
	// against the active backend would flag a correct config as broken.
	add(CheckModel("deepseek_model", "ai_backend", modelcatalog.ProviderDeepSeek,
		c.DeepSeekModel, AIBackendDeepSeek.DefaultProjectModel()))
	add(CheckModel("moonshot_model", "ai_backend", modelcatalog.ProviderMoonshot,
		c.MoonshotModel, AIBackendMoonshot.DefaultProjectModel()))
	add(CheckModel("xiaomi_model", "ai_backend", modelcatalog.ProviderXiaomi,
		c.XiaomiModel, AIBackendXiaomi.DefaultProjectModel()))

	// A route preset owns the main model, so an older explicit main-model value
	// is inactive until that preset is cleared. Utility and vision overrides do
	// remain active and are checked against their resolved providers, including
	// the "main" sentinel.
	if strings.TrimSpace(c.LCAgentRoutePreset) == "" {
		provider := lcagentEffectiveMainProvider(c.LCAgentRoutePreset, c.LCAgentProvider)
		add(CheckModel("embedded_lcagent_model", "lcagent_provider", provider, c.EmbeddedLCAgentModel, ""))
	}
	utilityProvider := lcagentEffectiveUtilityProvider(c.LCAgentRoutePreset, c.LCAgentProvider, c.LCAgentUtilityProvider)
	add(CheckModel("lcagent_utility_model", "lcagent_utility_provider", utilityProvider, c.LCAgentUtilityModel, ""))
	visionProvider := lcagentEffectiveVisionProvider(c.LCAgentRoutePreset, c.LCAgentProvider, c.LCAgentVisionProvider)
	add(CheckModel("lcagent_vision_model", "lcagent_vision_provider", visionProvider, c.LCAgentVisionModel, ""))

	return out
}
