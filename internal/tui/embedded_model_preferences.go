package tui

import (
	"strings"

	"lcroom/internal/codexapp"
)

type embeddedModelPreference struct {
	Model     string
	Reasoning string
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
