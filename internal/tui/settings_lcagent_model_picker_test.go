package tui

import (
	"path/filepath"
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
		{Model: "kimi-k2.7-code", DisplayName: "Kimi K2.7 Code"},
	}
	m := Model{
		settingsLCAgentModelPicker: &settingsLCAgentModelPickerState{
			FieldIndex:     settingsFieldLCAgentModel,
			Provider:       "openrouter",
			Models:         models,
			FilteredModels: models,
			Rows:           buildSettingsLCAgentPickerRows(models, "openrouter"),
			FilterInput:    newSettingsLCAgentModelPickerFilterInput(),
			Selected:       0,
		},
	}

	gotModel, _ := m.updateSettingsLCAgentModelPickerMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	got := gotModel.(Model)
	state := got.settingsLCAgentModelPicker
	if state == nil {
		t.Fatal("model picker closed unexpectedly")
	}
	if state.FilterInput.Value() != "j" {
		t.Fatalf("filter after j = %q, want j", state.FilterInput.Value())
	}
	// After filtering to "j", only "joker/model" remains. With provider grouping
	// the display is: [0]=Auto, [1]=provider header, [2]=first model row.
	if state.Selected != 2 {
		t.Fatalf("selected after j = %d, want 2 (first model row after provider header)", state.Selected)
	}

	gotModel, _ = got.updateSettingsLCAgentModelPickerMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	got = gotModel.(Model)
	if got.settingsLCAgentModelPicker.FilterInput.Value() != "jk" {
		t.Fatalf("filter after k = %q, want jk", got.settingsLCAgentModelPicker.FilterInput.Value())
	}
}

func TestSettingsLCAgentModelPickerPageKeysSkipHeaders(t *testing.T) {
	models := make([]codexapp.ModelOption, 0, 8)
	for i := 0; i < 8; i++ {
		models = append(models, codexapp.ModelOption{
			Model:         "model-" + string(rune('a'+i)),
			ModelProvider: "openai",
			DisplayName:   "Model " + string(rune('A'+i)),
		})
	}
	m := Model{
		settingsLCAgentModelPicker: &settingsLCAgentModelPickerState{
			FieldIndex:     settingsFieldLCAgentModel,
			Provider:       "openai",
			Models:         models,
			FilteredModels: models,
			Rows:           buildSettingsLCAgentPickerRows(models, "openai"),
			FilterInput:    newSettingsLCAgentModelPickerFilterInput(),
			Selected:       0,
		},
	}

	updated, _ := m.updateSettingsLCAgentModelPickerMode(tea.KeyMsg{Type: tea.KeyPgDown})
	got := updated.(Model)
	state := got.settingsLCAgentModelPicker
	if state == nil {
		t.Fatal("model picker closed unexpectedly")
	}
	if state.Selected == 0 || state.Rows[state.Selected-1].IsHeader {
		t.Fatalf("selected after pgdown = %d, want a model row", state.Selected)
	}
	if state.Rows[state.Selected-1].ModelIndex != 4 {
		t.Fatalf("model index after pgdown = %d, want 4", state.Rows[state.Selected-1].ModelIndex)
	}

	updated, _ = got.updateSettingsLCAgentModelPickerMode(tea.KeyMsg{Type: tea.KeyPgUp})
	got = updated.(Model)
	if got.settingsLCAgentModelPicker.Selected != 0 {
		t.Fatalf("selected after pgup = %d, want Auto row", got.settingsLCAgentModelPicker.Selected)
	}
}

func TestSettingsLCAgentKnownModelProviderMismatchBlocksSave(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.LCAgentRoutePreset = ""
	settings.LCAgentProvider = "deepseek"
	settings.EmbeddedLCAgentModel = "mimo-v2.5-pro"

	m := Model{
		settingsMode:     true,
		settingsFields:   newSettingsFields(settings),
		settingsBaseline: &settings,
		width:            100,
		height:           24,
	}

	updated, cmd := m.updateSettingsMode(tea.KeyMsg{Type: tea.KeyCtrlS})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("mismatched LCAgent provider/model should block save")
	}
	if got.settingsSaving {
		t.Fatalf("settingsSaving = true, want false after blocked save")
	}
	for _, want := range []string{"Main model mismatch", "mimo-v2.5-pro belongs to Xiaomi", "current provider is DeepSeek", "Settings were not saved"} {
		if !strings.Contains(got.status, want) {
			t.Fatalf("status missing %q: %q", want, got.status)
		}
	}
	rendered := got.renderSettingsContent(90, 24)
	if !strings.Contains(rendered, "mimo-v2.5-pro belongs to Xiaomi") {
		t.Fatalf("settings warning missing provider/model mismatch: %q", rendered)
	}
}

func TestSettingsLCAgentOpenRouterAllowsCrossProviderModel(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.LCAgentRoutePreset = ""
	settings.LCAgentProvider = "openrouter"
	settings.EmbeddedLCAgentModel = "xiaomi/mimo-v2.5-pro"

	m := Model{
		settingsMode:     true,
		settingsFields:   newSettingsFields(settings),
		settingsBaseline: &settings,
		width:            100,
		height:           24,
	}

	updated, cmd := m.updateSettingsMode(tea.KeyMsg{Type: tea.KeyCtrlS})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("OpenRouter cross-provider model should still be saveable")
	}
	if !got.settingsSaving {
		t.Fatalf("settingsSaving = false, want true while save command is pending")
	}
}

func TestSettingsLCAgentEditedProviderWinsOverBaselineEmbeddedPreference(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.LCAgentRoutePreset = ""
	settings.LCAgentProvider = "deepseek"
	settings.EmbeddedLCAgentModel = "mimo-v2.5-pro"

	m := Model{
		settingsMode:       true,
		settingsConfigPath: filepath.Join(t.TempDir(), "config.toml"),
		settingsFields:     newSettingsFields(settings),
		settingsBaseline:   &settings,
		width:              100,
		height:             24,
	}
	m.settingsFields[settingsFieldLCAgentProvider].input.SetValue("xiaomi")

	updated, cmd := m.updateSettingsMode(tea.KeyMsg{Type: tea.KeyCtrlS})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("corrected LCAgent provider/model should save")
	}
	if !got.settingsSaving {
		t.Fatalf("settingsSaving = false, want true while save command is pending")
	}
	msg, ok := cmd().(settingsSavedMsg)
	if !ok {
		t.Fatalf("save command returned %T, want settingsSavedMsg", msg)
	}
	if msg.err != nil {
		t.Fatalf("save error = %v", msg.err)
	}
	if msg.settings.LCAgentProvider != "xiaomi" {
		t.Fatalf("saved LCAgent provider = %q, want xiaomi", msg.settings.LCAgentProvider)
	}
	if msg.settings.EmbeddedLCAgentModel != "mimo-v2.5-pro" {
		t.Fatalf("saved LCAgent model = %q, want mimo-v2.5-pro", msg.settings.EmbeddedLCAgentModel)
	}
}

func TestSetupLCAgentKnownModelProviderMismatchBlocksSave(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.LCAgentRoutePreset = ""
	settings.LCAgentProvider = "deepseek"
	settings.EmbeddedLCAgentModel = "mimo-v2.5-pro"

	m := Model{
		setupMode:        true,
		setupConfigMode:  true,
		setupStep:        setupStepLCAgentConfig,
		setupFocusedRole: setupRoleLCAgent,
		settingsFields:   newSettingsFields(settings),
		settingsBaseline: &settings,
		width:            100,
		height:           24,
	}

	updated, cmd := m.saveSetupFromCurrentChoices()
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("mismatched LCAgent provider/model should block setup save")
	}
	if got.setupSaving {
		t.Fatalf("setupSaving = true, want false after blocked setup save")
	}
	if !strings.Contains(got.status, "mimo-v2.5-pro belongs to Xiaomi") {
		t.Fatalf("status = %q, want provider/model mismatch", got.status)
	}
	rendered := got.renderSetupConfigContent(90)
	if !strings.Contains(rendered, "mimo-v2.5-pro belongs to Xiaomi") {
		t.Fatalf("setup warning missing provider/model mismatch: %q", rendered)
	}
}
