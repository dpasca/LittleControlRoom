package tui

import (
	"strings"
	"testing"

	"lcroom/internal/codexapp"
	"lcroom/internal/config"

	tea "github.com/charmbracelet/bubbletea"
)

func TestSettingsLCAgentModelListConfigUsesUtilityProvider(t *testing.T) {
	settings := config.EditableSettings{
		LCAgentProvider:        "openrouter",
		EmbeddedLCAgentModel:   "deepseek/deepseek-v4-pro",
		LCAgentUtilityProvider: "deepseek",
		LCAgentUtilityModel:    "deepseek-v4-flash",
		DeepSeekAPIKey:         "deepseek-key",
	}
	cfg, provider, current, ok := settingsLCAgentModelListConfig(settings, settingsFieldLCAgentUtilityModel)
	if !ok {
		t.Fatal("settingsLCAgentModelListConfig() ok = false")
	}
	if provider != "deepseek" {
		t.Fatalf("provider = %q, want deepseek", provider)
	}
	if current != "deepseek-v4-flash" {
		t.Fatalf("current = %q, want deepseek-v4-flash", current)
	}
	if cfg.Provider != "deepseek" || cfg.Model != "deepseek-v4-flash" || cfg.DeepSeekAPIKey != "deepseek-key" {
		t.Fatalf("cfg = %#v", cfg)
	}
}

func TestSettingsLCAgentModelPickerSelectionUpdatesField(t *testing.T) {
	settings := config.EditableSettings{LCAgentProvider: "openrouter"}
	m := Model{
		settingsFields: newSettingsFields(settings),
		settingsLCAgentModelPicker: &settingsLCAgentModelPickerState{
			FieldIndex: settingsFieldLCAgentModel,
			Provider:   "openrouter",
		},
	}

	gotModel, _ := m.applySettingsLCAgentModelPickerSelection(codexapp.ModelOption{Model: "openai/gpt-5.5"})
	got := gotModel.(Model)
	if value := got.settingsFieldValue(settingsFieldLCAgentModel); value != "openai/gpt-5.5" {
		t.Fatalf("Main model field = %q, want openai/gpt-5.5", value)
	}
	if got.settingsLCAgentModelPicker != nil {
		t.Fatal("model picker should close after selection")
	}
	if !strings.Contains(got.status, "Press ctrl+s") {
		t.Fatalf("status = %q, want save hint", got.status)
	}
}

func TestSettingsLCAgentModelPickerSelectionUpdatesMainProvider(t *testing.T) {
	settings := config.EditableSettings{
		LCAgentRoutePreset: "mimo-2.5-pro-low",
		LCAgentProvider:    "xiaomi",
	}
	m := Model{
		settingsFields: newSettingsFields(settings),
		settingsLCAgentModelPicker: &settingsLCAgentModelPickerState{
			FieldIndex: settingsFieldLCAgentModel,
			Provider:   "xiaomi",
		},
	}

	gotModel, _ := m.applySettingsLCAgentModelPickerSelection(codexapp.ModelOption{
		Model:         "deepseek-v4-pro",
		ModelProvider: "deepseek",
	})
	got := gotModel.(Model)
	if value := got.settingsFieldValue(settingsFieldLCAgentModel); value != "deepseek-v4-pro" {
		t.Fatalf("Main model field = %q, want deepseek-v4-pro", value)
	}
	if value := got.settingsFieldValue(settingsFieldLCAgentProvider); value != "deepseek" {
		t.Fatalf("LCAgent provider = %q, want deepseek", value)
	}
	if value := got.settingsFieldValue(settingsFieldLCAgentRoutePreset); value != "" {
		t.Fatalf("LCAgent route preset = %q, want cleared", value)
	}
}

func TestSettingsLCAgentModelPickerJKTypeIntoFilter(t *testing.T) {
	models := []codexapp.ModelOption{
		{Model: "joker/model", DisplayName: "Joker Model"},
		{Model: "kimi-k2.6", DisplayName: "Kimi K2.6"},
	}
	m := Model{
		settingsLCAgentModelPicker: &settingsLCAgentModelPickerState{
			FieldIndex:     settingsFieldLCAgentModel,
			Provider:       "openrouter",
			Models:         models,
			FilteredModels: models,
			Selected:       0,
		},
	}

	gotModel, _ := m.updateSettingsLCAgentModelPickerMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	got := gotModel.(Model)
	state := got.settingsLCAgentModelPicker
	if state == nil {
		t.Fatal("model picker closed unexpectedly")
	}
	if state.FilterText != "j" {
		t.Fatalf("filter after j = %q, want j", state.FilterText)
	}
	if state.Selected != 1 {
		t.Fatalf("selected after j = %d, want first matching model row", state.Selected)
	}

	gotModel, _ = got.updateSettingsLCAgentModelPickerMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	got = gotModel.(Model)
	if got.settingsLCAgentModelPicker.FilterText != "jk" {
		t.Fatalf("filter after k = %q, want jk", got.settingsLCAgentModelPicker.FilterText)
	}
}
