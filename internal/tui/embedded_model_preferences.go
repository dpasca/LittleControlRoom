package tui

import (
	"strings"

	"lcroom/internal/codexapp"
	"lcroom/internal/config"
)

type embeddedModelPreference struct {
	Model     string
	Reasoning string
}

func embeddedModelPreferencesFromSettings(settings config.EditableSettings) map[codexapp.Provider]embeddedModelPreference {
	prefs := map[codexapp.Provider]embeddedModelPreference{}
	if model := strings.TrimSpace(settings.EmbeddedCodexModel); model != "" || strings.TrimSpace(settings.EmbeddedCodexReasoning) != "" {
		prefs[codexapp.ProviderCodex] = embeddedModelPreference{
			Model:     model,
			Reasoning: strings.TrimSpace(settings.EmbeddedCodexReasoning),
		}
	}
	if model := strings.TrimSpace(settings.EmbeddedOpenCodeModel); model != "" || strings.TrimSpace(settings.EmbeddedOpenCodeReasoning) != "" {
		prefs[codexapp.ProviderOpenCode] = embeddedModelPreference{
			Model:     model,
			Reasoning: strings.TrimSpace(settings.EmbeddedOpenCodeReasoning),
		}
	}
	if len(prefs) == 0 {
		return nil
	}
	return prefs
}

func applyEmbeddedModelPreferencesToSettings(settings *config.EditableSettings, prefs map[codexapp.Provider]embeddedModelPreference) {
	if settings == nil {
		return
	}
	settings.EmbeddedCodexModel = ""
	settings.EmbeddedCodexReasoning = ""
	settings.EmbeddedOpenCodeModel = ""
	settings.EmbeddedOpenCodeReasoning = ""
	if pref, ok := prefs[codexapp.ProviderCodex]; ok {
		settings.EmbeddedCodexModel = strings.TrimSpace(pref.Model)
		settings.EmbeddedCodexReasoning = strings.TrimSpace(pref.Reasoning)
	}
	if pref, ok := prefs[codexapp.ProviderOpenCode]; ok {
		settings.EmbeddedOpenCodeModel = strings.TrimSpace(pref.Model)
		settings.EmbeddedOpenCodeReasoning = strings.TrimSpace(pref.Reasoning)
	}
}

func (m Model) embeddedModelPreference(provider codexapp.Provider) (embeddedModelPreference, bool) {
	provider = provider.Normalized()
	if provider == "" || m.embeddedModelPrefs == nil {
		return embeddedModelPreference{}, false
	}
	pref, ok := m.embeddedModelPrefs[provider]
	if !ok {
		return embeddedModelPreference{}, false
	}
	pref.Model = strings.TrimSpace(pref.Model)
	pref.Reasoning = strings.TrimSpace(pref.Reasoning)
	if pref.Model == "" && pref.Reasoning == "" {
		return embeddedModelPreference{}, false
	}
	return pref, true
}

func (m *Model) rememberEmbeddedModelPreference(provider codexapp.Provider, model, reasoning string) {
	provider = provider.Normalized()
	model = strings.TrimSpace(model)
	reasoning = strings.TrimSpace(reasoning)
	if provider == "" || (model == "" && reasoning == "") {
		return
	}
	if m.embeddedModelPrefs == nil {
		m.embeddedModelPrefs = make(map[codexapp.Provider]embeddedModelPreference)
	}
	m.embeddedModelPrefs[provider] = embeddedModelPreference{
		Model:     model,
		Reasoning: reasoning,
	}
}

func (m Model) applyEmbeddedModelPreference(req codexapp.LaunchRequest) codexapp.LaunchRequest {
	if pref, ok := m.embeddedModelPreference(req.Provider); ok {
		req.PendingModel = pref.Model
		req.PendingReasoning = pref.Reasoning
	}
	return req
}
