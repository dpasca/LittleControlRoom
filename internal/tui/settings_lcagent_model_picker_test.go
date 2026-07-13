package tui

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"lcroom/internal/codexapp"
	"lcroom/internal/config"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
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

func TestSettingsLCAgentModelListConfigUsesOllamaConnection(t *testing.T) {
	settings := config.EditableSettings{
		LCAgentProvider: "ollama",
		OllamaBaseURL:   "http://127.0.0.1:11435/v1",
		OllamaAPIKey:    "ollama-key",
		OllamaModel:     "qwen3:8b",
	}
	cfg, provider, current, ok := settingsLCAgentModelListConfig(settings, settingsFieldLCAgentModel)
	if !ok {
		t.Fatal("settingsLCAgentModelListConfig() ok = false")
	}
	if provider != "ollama" || current != "qwen3:8b" {
		t.Fatalf("provider/current = %q/%q, want ollama/qwen3:8b", provider, current)
	}
	if cfg.Provider != "ollama" || cfg.Model != "qwen3:8b" || cfg.OllamaBaseURL != "http://127.0.0.1:11435/v1" || cfg.OllamaAPIKey != "ollama-key" || cfg.OllamaModel != "qwen3:8b" {
		t.Fatalf("cfg = %#v", cfg)
	}
}

func TestSettingsLCAgentModelPickerStoresOllamaBaseURLBeforeLoading(t *testing.T) {
	settings := config.EditableSettings{LCAgentProvider: "ollama"}
	m := Model{
		settingsFields: newSettingsFields(settings),
		settingsLCAgentModelPicker: &settingsLCAgentModelPickerState{
			FieldIndex:         settingsFieldLCAgentModel,
			Step:               settingsLCAgentModelPickerStepAPIKey,
			Provider:           "ollama",
			APIKeyProvider:     "ollama",
			APIKeyInput:        newSettingsLCAgentModelPickerAPIKeyInput("ollama", "ollama-key"),
			BaseURLInput:       newSettingsLCAgentModelPickerBaseURLInput("ollama", "http://127.0.0.1:11435/v1"),
			ConnectionSelected: 1,
		},
	}

	updated, cmd := m.startSettingsLCAgentModelPickerModelList()
	got := updated.(Model)
	if cmd == nil {
		t.Fatal("startSettingsLCAgentModelPickerModelList() cmd = nil, want model-list command")
	}
	if value := got.settingsFieldValue(settingsFieldOllamaBaseURL); value != "http://127.0.0.1:11435/v1" {
		t.Fatalf("Ollama base URL field = %q", value)
	}
	if value := got.settingsFieldValue(settingsFieldOllamaAPIKey); value != "ollama-key" {
		t.Fatalf("Ollama API key field = %q", value)
	}
}

func TestSettingsLCAgentModelPickerAPIKeyStepHidesEditingSuffix(t *testing.T) {
	m := Model{
		settingsFields: newSettingsFields(config.EditableSettings{LCAgentProvider: "xiaomi"}),
		settingsLCAgentModelPicker: &settingsLCAgentModelPickerState{
			FieldIndex:         settingsFieldLCAgentModel,
			Step:               settingsLCAgentModelPickerStepAPIKey,
			Provider:           "xiaomi",
			APIKeyProvider:     "xiaomi",
			APIKeyInput:        newSettingsLCAgentModelPickerAPIKeyInput("xiaomi", "xm-live-12345"),
			BaseURLInput:       newSettingsLCAgentModelPickerBaseURLInput("xiaomi", config.AIBackendXiaomi.DefaultOpenAICompatibleBaseURL()),
			ConnectionSelected: 0,
		},
	}

	rendered := ansi.Strip(m.renderSettingsLCAgentModelPickerAPIKeyContent(84, 24, "Xiaomi API key"))
	if !strings.Contains(rendered, "It remains hidden while editing.") {
		t.Fatalf("picker should explain edited keys remain hidden: %q", rendered)
	}
	for _, leaked := range []string{"...12345", "xm-live-12345"} {
		if strings.Contains(rendered, leaked) {
			t.Fatalf("picker leaked edited api key fragment %q: %q", leaked, rendered)
		}
	}
}

func TestOpenEmbeddedLCAgentModelPickerUsesUnifiedPicker(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.LCAgentProvider = "ollama"
	m := Model{
		settingsBaseline:    &settings,
		codexVisibleProject: "/tmp/project",
	}

	updated, cmd := m.openEmbeddedLCAgentModelPicker()
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("openEmbeddedLCAgentModelPicker() cmd = %v, want nil", cmd)
	}
	if got.codexModelPicker != nil {
		t.Fatal("legacy codex model picker should not open for embedded LCAgent")
	}
	if got.settingsLCAgentModelPicker == nil {
		t.Fatal("settings LCAgent model picker is nil")
	}
	if !got.settingsLCAgentModelPicker.EmbeddedApply || got.settingsLCAgentModelPicker.EmbeddedProject != "/tmp/project" {
		t.Fatalf("embedded picker state = %#v", got.settingsLCAgentModelPicker)
	}
	if got.settingsLCAgentModelPicker.FieldIndex != settingsFieldLCAgentModel {
		t.Fatalf("field index = %d, want main model", got.settingsLCAgentModelPicker.FieldIndex)
	}
	foundOllama := false
	for _, option := range got.settingsLCAgentModelPicker.ProviderOptions {
		if option.Value == "ollama" {
			foundOllama = true
			break
		}
	}
	if !foundOllama {
		t.Fatalf("provider options = %#v, want ollama", got.settingsLCAgentModelPicker.ProviderOptions)
	}
}

func TestSettingsLCAgentModelPickerWarnsWhenProviderDiscoveryFallsBack(t *testing.T) {
	models := []codexapp.ModelOption{{
		Model:         "deepseek/deepseek-v4-pro",
		ModelProvider: "openrouter",
		DisplayName:   "DeepSeek V4 Pro",
		Description:   "Curated fallback model.",
	}}
	m := Model{
		settingsFields: newSettingsFields(config.EditableSettings{LCAgentProvider: "openrouter"}),
		width:          100,
		height:         24,
		settingsLCAgentModelPicker: &settingsLCAgentModelPickerState{
			FieldIndex:  settingsFieldLCAgentModel,
			Step:        settingsLCAgentModelPickerStepModel,
			Provider:    "openrouter",
			Current:     "",
			FilterInput: newSettingsLCAgentModelPickerFilterInput(),
			Loading:     true,
		},
	}

	updated, cmd := m.applySettingsLCAgentModelListMsg(settingsLCAgentModelListMsg{
		fieldIndex: settingsFieldLCAgentModel,
		provider:   "openrouter",
		models:     models,
		err:        errors.New("OPENROUTER_API_KEY is required for provider=openrouter"),
	})
	if cmd != nil {
		t.Fatalf("applySettingsLCAgentModelListMsg() cmd = %v, want nil", cmd)
	}
	got := updated.(Model)
	state := got.settingsLCAgentModelPicker
	if state == nil || state.Err == "" {
		t.Fatalf("picker state = %#v, want retained provider-list error", state)
	}
	if !strings.Contains(got.status, "curated fallback models only") {
		t.Fatalf("status = %q, want curated fallback warning", got.status)
	}

	rendered := ansi.Strip(got.renderSettingsLCAgentModelPickerContent(84, 24))
	for _, want := range []string{
		"Warning: full provider model list unavailable.",
		"Showing curated fallback models only.",
		"Check the shared API key",
		"OPENROUTER_API_KEY is required",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered picker missing %q:\n%s", want, rendered)
		}
	}
}

func TestSettingsLCAgentVisionAutoUsesVerifiedMainForLaunch(t *testing.T) {
	settings := config.EditableSettings{
		LCAgentProvider:       "openai",
		EmbeddedLCAgentModel:  "gpt-5.4",
		LCAgentVisionProvider: "auto",
		LCAgentVisionModel:    "gpt-5.4-mini",
	}

	if got := settingsLCAgentVisionProviderForLaunch(settings); got != "openai" {
		t.Fatalf("configured auto launch provider = %q, want openai", got)
	}
	if got := settingsLCAgentVisionModelForLaunch(settings); got != "gpt-5.4-mini" {
		t.Fatalf("configured auto launch model = %q, want gpt-5.4-mini", got)
	}

	settings.LCAgentVisionModel = "gpt-5.3-mini"
	if got := settingsLCAgentVisionProviderForLaunch(settings); got != "off" {
		t.Fatalf("unknown explicit auto launch provider = %q, want off", got)
	}
	if got := settingsLCAgentVisionModelForLaunch(settings); got != "" {
		t.Fatalf("unknown explicit auto launch model = %q, want blank", got)
	}

	settings.LCAgentVisionModel = ""
	if got := settingsLCAgentVisionProviderForLaunch(settings); got != "off" {
		t.Fatalf("unverified auto launch provider = %q, want off", got)
	}
	if got := settingsLCAgentVisionModelForLaunch(settings); got != "" {
		t.Fatalf("unverified auto launch model = %q, want blank", got)
	}

	settings.LCAgentMainVisionProvider = "openai"
	settings.LCAgentMainVisionModel = "gpt-5.4"
	if got := settingsLCAgentVisionProviderForLaunch(settings); got != "main" {
		t.Fatalf("verified auto launch provider = %q, want main", got)
	}
	if got := settingsLCAgentVisionModelForLaunch(settings); got != "" {
		t.Fatalf("verified auto launch model = %q, want blank", got)
	}
}

func TestSettingsLCAgentModelListConfigProviderOverrideWorksWhenRoleIsOff(t *testing.T) {
	settings := config.EditableSettings{
		LCAgentProvider:       "deepseek",
		LCAgentVisionProvider: "off",
		OpenRouterAPIKey:      "openrouter-key",
	}

	cfg, provider, current, ok := settingsLCAgentModelListConfigForProvider(settings, settingsFieldLCAgentVisionModel, "openrouter")
	if !ok {
		t.Fatal("settingsLCAgentModelListConfigForProvider() ok = false for Vision override from off")
	}
	if provider != "openrouter" {
		t.Fatalf("provider = %q, want openrouter", provider)
	}
	if current != "" {
		t.Fatalf("current = %q, want empty after provider override", current)
	}
	if cfg.Provider != "openrouter" || cfg.OpenRouterAPIKey != "openrouter-key" {
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

	got := applySettingsLCAgentModelPickerSelectionForTest(t, m, "openrouter", codexapp.ModelOption{Model: "openai/gpt-5.5"})
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
			Provider:   "deepseek",
		},
	}

	got := applySettingsLCAgentModelPickerSelectionForTest(t, m, "deepseek", codexapp.ModelOption{
		Model:         "deepseek-v4-pro",
		ModelProvider: "deepseek",
	})
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
			Step:           settingsLCAgentModelPickerStepModel,
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

func TestSettingsLCAgentModelPickerBackspaceEditsFilter(t *testing.T) {
	models := []codexapp.ModelOption{
		{Model: "openai/gpt-5.5", DisplayName: "GPT 5.5"},
		{Model: "kimi-k2.7-code", DisplayName: "Kimi K2.7 Code"},
	}
	input := newSettingsLCAgentModelPickerFilterInput()
	input.SetValue("gpt")
	m := Model{
		settingsLCAgentModelPicker: &settingsLCAgentModelPickerState{
			FieldIndex:     settingsFieldLCAgentModel,
			Step:           settingsLCAgentModelPickerStepModel,
			Provider:       "openrouter",
			Models:         models,
			FilteredModels: []codexapp.ModelOption{models[0]},
			Rows:           buildSettingsLCAgentPickerRows([]codexapp.ModelOption{models[0]}, "openrouter"),
			FilterInput:    input,
			Selected:       2,
		},
	}

	updated, _ := m.updateSettingsLCAgentModelPickerMode(tea.KeyMsg{Type: tea.KeyBackspace})
	got := updated.(Model)
	state := got.settingsLCAgentModelPicker
	if state == nil {
		t.Fatal("model picker closed unexpectedly")
	}
	if state.Step != settingsLCAgentModelPickerStepModel {
		t.Fatalf("picker step after backspace = %v, want model step", state.Step)
	}
	if state.FilterInput.Value() != "gp" {
		t.Fatalf("filter after backspace = %q, want gp", state.FilterInput.Value())
	}
}

func TestSettingsLCAgentModelPickerCanSelectCustomFilteredModel(t *testing.T) {
	const customModel = "mimo-v2.5-pro-ultraspeed"
	models := []codexapp.ModelOption{{
		Model:         "mimo-v2.5-pro",
		ModelProvider: "xiaomi",
		DisplayName:   "MiMo 2.5 Pro",
	}}
	input := newSettingsLCAgentModelPickerFilterInput()
	input.SetValue(customModel)
	m := Model{
		settingsFields: newSettingsFields(config.EditableSettings{
			LCAgentProvider: "xiaomi",
		}),
		settingsLCAgentModelPicker: &settingsLCAgentModelPickerState{
			FieldIndex:     settingsFieldLCAgentModel,
			Step:           settingsLCAgentModelPickerStepModel,
			Provider:       "xiaomi",
			Models:         models,
			FilteredModels: models,
			Rows:           buildSettingsLCAgentPickerRows(models, "xiaomi"),
			FilterInput:    input,
			Selected:       0,
		},
	}

	m.applySettingsLCAgentModelPickerFilter()
	state := m.settingsLCAgentModelPicker
	if state == nil {
		t.Fatal("model picker closed unexpectedly")
	}
	if len(state.FilteredModels) != 1 || state.FilteredModels[0].Model != customModel {
		t.Fatalf("filtered models = %#v, want custom model only", state.FilteredModels)
	}
	if state.Selected == 0 || state.Rows[state.Selected-1].ModelIndex != 0 {
		t.Fatalf("selected row = %d rows=%#v, want custom model row", state.Selected, state.Rows)
	}
	rendered := ansi.Strip(m.renderSettingsLCAgentModelPickerContent(84, 24))
	if !strings.Contains(rendered, "Custom: "+customModel) {
		t.Fatalf("rendered picker missing custom model row:\n%s", rendered)
	}

	updated, _ := m.updateSettingsLCAgentModelPickerMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	state = got.settingsLCAgentModelPicker
	if state == nil || state.Step != settingsLCAgentModelPickerStepReasoning {
		t.Fatalf("picker state after choosing custom model = %#v, want reasoning step", state)
	}
	if state.PendingModel != customModel || state.PendingModelOption.ModelProvider != "xiaomi" {
		t.Fatalf("pending custom model/provider = %q/%q, want %q/xiaomi", state.PendingModel, state.PendingModelOption.ModelProvider, customModel)
	}

	updated, _ = got.updateSettingsLCAgentModelPickerMode(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if value := got.settingsFieldValue(settingsFieldLCAgentModel); value != customModel {
		t.Fatalf("Main model field = %q, want %q", value, customModel)
	}
	if value := got.settingsFieldValue(settingsFieldLCAgentProvider); value != "xiaomi" {
		t.Fatalf("LCAgent provider = %q, want xiaomi", value)
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
			Step:           settingsLCAgentModelPickerStepModel,
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

func TestSettingsLCAgentModelPickerMainModelAdvancesToReasoning(t *testing.T) {
	option := codexapp.ModelOption{
		Model:         "openai/gpt-5.5",
		ModelProvider: "openrouter",
		SupportedReasoningEfforts: []codexapp.ReasoningEffortOption{
			{ReasoningEffort: "low", Description: "Light"},
			{ReasoningEffort: "high", Description: "Deep"},
		},
		DefaultReasoningEffort: "low",
	}
	m := Model{
		settingsLCAgentModelPicker: &settingsLCAgentModelPickerState{
			FieldIndex:     settingsFieldLCAgentModel,
			Step:           settingsLCAgentModelPickerStepModel,
			Provider:       "openrouter",
			Models:         []codexapp.ModelOption{option},
			FilteredModels: []codexapp.ModelOption{option},
			Rows:           buildSettingsLCAgentPickerRows([]codexapp.ModelOption{option}, "openrouter"),
			FilterInput:    newSettingsLCAgentModelPickerFilterInput(),
			Selected:       2,
		},
	}

	updated, _ := m.updateSettingsLCAgentModelPickerMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	state := got.settingsLCAgentModelPicker
	if state == nil {
		t.Fatal("model picker closed before reasoning step")
	}
	if state.Step != settingsLCAgentModelPickerStepReasoning {
		t.Fatalf("picker step = %v, want reasoning", state.Step)
	}
	if state.PendingModel != "openai/gpt-5.5" {
		t.Fatalf("pending model = %q, want openai/gpt-5.5", state.PendingModel)
	}
	options := settingsLCAgentModelPickerReasoningOptions(state)
	if state.ReasoningSelected < 0 || state.ReasoningSelected >= len(options) || options[state.ReasoningSelected].Value != "low" {
		t.Fatalf("selected reasoning index=%d options=%#v, want low", state.ReasoningSelected, options)
	}
}

func TestSettingsLCAgentModelPickerReasoningMovesWithProviderFallback(t *testing.T) {
	settings := config.EditableSettings{LCAgentProvider: "openai"}
	option := codexapp.ModelOption{
		Model:                  "gpt-5.5",
		ModelProvider:          "openai",
		DisplayName:            "GPT 5.5",
		DefaultReasoningEffort: "low",
	}
	m := Model{
		settingsFields: newSettingsFields(settings),
		settingsLCAgentModelPicker: &settingsLCAgentModelPickerState{
			FieldIndex:       settingsFieldLCAgentModel,
			Step:             settingsLCAgentModelPickerStepModel,
			Provider:         "openai",
			Models:           []codexapp.ModelOption{option},
			FilteredModels:   []codexapp.ModelOption{option},
			Rows:             buildSettingsLCAgentPickerRows([]codexapp.ModelOption{option}, "openai"),
			FilterInput:      newSettingsLCAgentModelPickerFilterInput(),
			Selected:         2,
			CurrentReasoning: "",
		},
	}

	updated, _ := m.updateSettingsLCAgentModelPickerMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	state := got.settingsLCAgentModelPicker
	if state == nil || state.Step != settingsLCAgentModelPickerStepReasoning {
		t.Fatalf("picker state after model enter = %#v, want reasoning step", state)
	}
	options := settingsLCAgentModelPickerReasoningOptions(state)
	if len(options) != 5 {
		t.Fatalf("reasoning options = %#v, want provider default plus low/medium/high/xhigh", options)
	}
	if state.ReasoningSelected >= len(options) || options[state.ReasoningSelected].Value != "low" {
		t.Fatalf("initial selected reasoning index=%d options=%#v, want low", state.ReasoningSelected, options)
	}

	updated, _ = got.updateSettingsLCAgentModelPickerMode(tea.KeyMsg{Type: tea.KeyDown})
	got = updated.(Model)
	if got.settingsLCAgentModelPicker.PendingReasoning != "medium" {
		t.Fatalf("pending reasoning after one down = %q, want medium", got.settingsLCAgentModelPicker.PendingReasoning)
	}
	updated, _ = got.updateSettingsLCAgentModelPickerMode(tea.KeyMsg{Type: tea.KeyDown})
	got = updated.(Model)
	if got.settingsLCAgentModelPicker.PendingReasoning != "high" {
		t.Fatalf("pending reasoning after two downs = %q, want high", got.settingsLCAgentModelPicker.PendingReasoning)
	}
	updated, _ = got.updateSettingsLCAgentModelPickerMode(tea.KeyMsg{Type: tea.KeyDown})
	got = updated.(Model)
	if got.settingsLCAgentModelPicker.PendingReasoning != "xhigh" {
		t.Fatalf("pending reasoning after three downs = %q, want xhigh", got.settingsLCAgentModelPicker.PendingReasoning)
	}

	updated, _ = got.updateSettingsLCAgentModelPickerMode(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if got.settingsLCAgentModelPicker != nil {
		t.Fatal("model picker should close after applying reasoning")
	}
	if value := got.settingsFieldValue(settingsFieldLCAgentReasoning); value != "xhigh" {
		t.Fatalf("LCAgent reasoning field = %q, want xhigh", value)
	}
}

func TestSettingsLCAgentModelPickerUtilityReasoningUsesProviderDefault(t *testing.T) {
	option := codexapp.ModelOption{
		Model:         "deepseek-v4-flash",
		ModelProvider: "deepseek",
		SupportedReasoningEfforts: []codexapp.ReasoningEffortOption{
			{ReasoningEffort: "low", Description: "Light"},
		},
		DefaultReasoningEffort: "low",
	}
	m := Model{
		settingsLCAgentModelPicker: &settingsLCAgentModelPickerState{
			FieldIndex:     settingsFieldLCAgentUtilityModel,
			Step:           settingsLCAgentModelPickerStepModel,
			Provider:       "deepseek",
			Models:         []codexapp.ModelOption{option},
			FilteredModels: []codexapp.ModelOption{option},
			Rows:           buildSettingsLCAgentPickerRows([]codexapp.ModelOption{option}, "deepseek"),
			FilterInput:    newSettingsLCAgentModelPickerFilterInput(),
			Selected:       2,
		},
	}

	updated, _ := m.updateSettingsLCAgentModelPickerMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	state := got.settingsLCAgentModelPicker
	if state == nil {
		t.Fatal("model picker closed before reasoning step")
	}
	if state.Step != settingsLCAgentModelPickerStepReasoning {
		t.Fatalf("picker step = %v, want reasoning", state.Step)
	}
	options := settingsLCAgentModelPickerReasoningOptions(state)
	if len(options) != 1 || options[0].Value != "" {
		t.Fatalf("utility reasoning options = %#v, want provider default only", options)
	}
}

func TestSettingsLCAgentModelValueLabelIncludesReasoning(t *testing.T) {
	settings := config.EditableSettings{
		LCAgentProvider:          "openai",
		EmbeddedLCAgentModel:     "gpt-5.5",
		EmbeddedLCAgentReasoning: "high",
		LCAgentUtilityProvider:   "main",
		LCAgentVisionProvider:    "off",
		LCAgentVisionModel:       "",
		OpenRouterModel:          "deepseek/deepseek-v4-pro",
		BossChatBackend:          config.AIBackendOpenRouter,
		BossHelmModel:            "openai/gpt-5.5",
		BossUtilityModel:         "",
	}

	mainLabel := settingsLCAgentModelValueLabel(settings, settingsFieldLCAgentModel)
	if !strings.Contains(mainLabel, "reasoning: high") {
		t.Fatalf("main label = %q, want reasoning effort", mainLabel)
	}
	utilityLabel := settingsLCAgentModelValueLabel(settings, settingsFieldLCAgentUtilityModel)
	if !strings.Contains(utilityLabel, "Same as Main") || !strings.Contains(utilityLabel, "reasoning: high") {
		t.Fatalf("utility label = %q, want same-as-main reasoning", utilityLabel)
	}
	projectLabel := settingsLCAgentModelValueLabel(settings, settingsFieldOpenRouterModel)
	if !strings.Contains(projectLabel, "OpenRouter / deepseek/deepseek-v4-pro") || !strings.Contains(projectLabel, "reasoning: LCR Default") {
		t.Fatalf("project label = %q, want model and LCR-default reasoning", projectLabel)
	}
	bossLabel := settingsLCAgentModelValueLabel(settings, settingsFieldBossChatModel)
	if !strings.Contains(bossLabel, "OpenRouter / openai/gpt-5.5") || !strings.Contains(bossLabel, "reasoning: Provider Default") {
		t.Fatalf("chat label = %q, want Chat model and provider-default reasoning", bossLabel)
	}
}

func TestSettingsModelPickerAPIKeyStepUpdatesSharedKeyBeforeModelList(t *testing.T) {
	settings := config.EditableSettings{LCAgentProvider: "openrouter"}
	apiKeyInput := newSettingsLCAgentModelPickerAPIKeyInput("moonshot", "mk-test")
	m := Model{
		settingsFields: newSettingsFields(settings),
		settingsLCAgentModelPicker: &settingsLCAgentModelPickerState{
			FieldIndex:     settingsFieldLCAgentModel,
			Step:           settingsLCAgentModelPickerStepAPIKey,
			Provider:       "moonshot",
			APIKeyProvider: "moonshot",
			APIKeyInput:    apiKeyInput,
		},
	}

	updated, cmd := m.startSettingsLCAgentModelPickerModelList()
	got := updated.(Model)
	if cmd == nil {
		t.Fatal("startSettingsLCAgentModelPickerModelList() command = nil, want model-list command")
	}
	if value := got.settingsFieldValue(settingsFieldMoonshotAPIKey); value != "mk-test" {
		t.Fatalf("Moonshot API key field = %q, want mk-test", value)
	}
	state := got.settingsLCAgentModelPicker
	if state == nil || state.Step != settingsLCAgentModelPickerStepModel || !state.Loading {
		t.Fatalf("picker state = %#v, want loading model step", state)
	}
}

func TestSettingsProjectModelPickerSelectionSwitchesBackendAndModelField(t *testing.T) {
	settings := config.EditableSettings{AIBackend: config.AIBackendOpenRouter}
	apiKeyInput := newSettingsLCAgentModelPickerAPIKeyInput("moonshot", "mk-project")
	m := Model{
		settingsFields: newSettingsFields(settings),
		settingsLCAgentModelPicker: &settingsLCAgentModelPickerState{
			FieldIndex:     settingsFieldOpenRouterModel,
			Provider:       "moonshot",
			APIKeyProvider: "moonshot",
			APIKeyInput:    apiKeyInput,
			PendingModel:   "kimi-k2.7-code",
		},
	}

	updated, _ := m.applySettingsLCAgentModelPickerSelection()
	got := updated.(Model)
	if value := got.settingsFieldValue(settingsFieldAIBackend); value != string(config.AIBackendMoonshot) {
		t.Fatalf("AI backend = %q, want moonshot", value)
	}
	if value := got.settingsFieldValue(settingsFieldMoonshotModel); value != "kimi-k2.7-code" {
		t.Fatalf("Moonshot project model = %q, want kimi-k2.7-code", value)
	}
	if value := got.settingsFieldValue(settingsFieldOpenRouterModel); value != "" {
		t.Fatalf("OpenRouter project model = %q, want untouched blank", value)
	}
	if value := got.settingsFieldValue(settingsFieldMoonshotAPIKey); value != "mk-project" {
		t.Fatalf("Moonshot API key field = %q, want mk-project", value)
	}
}

func TestSettingsProjectModelPickerSelectionStoresReasoning(t *testing.T) {
	option := codexapp.ModelOption{
		Model:         "mimo-v2.5-pro",
		ModelProvider: "xiaomi",
		SupportedReasoningEfforts: []codexapp.ReasoningEffortOption{
			{ReasoningEffort: "low", Description: "Light"},
			{ReasoningEffort: "high", Description: "Deep"},
		},
	}
	settings := config.EditableSettings{AIBackend: config.AIBackendXiaomi}
	m := Model{
		settingsFields: newSettingsFields(settings),
		settingsLCAgentModelPicker: &settingsLCAgentModelPickerState{
			FieldIndex:       settingsFieldXiaomiModel,
			Step:             settingsLCAgentModelPickerStepModel,
			Provider:         "xiaomi",
			Models:           []codexapp.ModelOption{option},
			FilteredModels:   []codexapp.ModelOption{option},
			Rows:             buildSettingsLCAgentPickerRows([]codexapp.ModelOption{option}, "xiaomi"),
			FilterInput:      newSettingsLCAgentModelPickerFilterInput(),
			Selected:         2,
			CurrentReasoning: "",
		},
	}

	updated, _ := m.updateSettingsLCAgentModelPickerMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	state := got.settingsLCAgentModelPicker
	if state == nil || state.Step != settingsLCAgentModelPickerStepReasoning {
		t.Fatalf("picker state after model enter = %#v, want reasoning step", state)
	}
	options := settingsLCAgentModelPickerReasoningOptions(state)
	if len(options) != 3 || options[0].Label != "LCR Default" || options[1].Value != "low" || options[2].Value != "high" {
		t.Fatalf("project reasoning options = %#v, want LCR default, low, high", options)
	}

	updated, _ = got.updateSettingsLCAgentModelPickerMode(tea.KeyMsg{Type: tea.KeyDown})
	got = updated.(Model)
	updated, _ = got.updateSettingsLCAgentModelPickerMode(tea.KeyMsg{Type: tea.KeyDown})
	got = updated.(Model)
	updated, _ = got.updateSettingsLCAgentModelPickerMode(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)

	if value := got.settingsFieldValue(settingsFieldXiaomiModel); value != "mimo-v2.5-pro" {
		t.Fatalf("Xiaomi project model = %q, want mimo-v2.5-pro", value)
	}
	if value := got.settingsFieldValue(settingsFieldProjectReasoning); value != "high" {
		t.Fatalf("project reasoning = %q, want high", value)
	}
	if !strings.Contains(got.status, "with high reasoning") {
		t.Fatalf("status = %q, want reasoning summary", got.status)
	}
}

func TestSettingsBossModelPickerOnlyUsesCloudSelectorForCloudBackend(t *testing.T) {
	localSettings := config.EditableSettings{BossChatBackend: config.AIBackendMLX}
	localModel := Model{settingsFields: newSettingsFields(localSettings)}
	if localModel.settingsFieldUsesUnifiedCloudModelPicker(settingsFieldBossChatModel) {
		t.Fatal("Chat main model should not use cloud selector for MLX backend")
	}

	cloudSettings := config.EditableSettings{BossChatBackend: config.AIBackendOpenRouter}
	cloudModel := Model{settingsFields: newSettingsFields(cloudSettings)}
	if !cloudModel.settingsFieldUsesUnifiedCloudModelPicker(settingsFieldBossChatModel) {
		t.Fatal("Chat main model should use cloud selector for OpenRouter backend")
	}
}

func applySettingsLCAgentModelPickerSelectionForTest(t *testing.T, m Model, provider string, option codexapp.ModelOption) Model {
	t.Helper()
	if m.settingsLCAgentModelPicker == nil {
		t.Fatal("settingsLCAgentModelPicker is nil")
	}
	m.settingsLCAgentModelPicker.Provider = provider
	m.settingsLCAgentModelPicker.PendingModel = strings.TrimSpace(option.Model)
	m.settingsLCAgentModelPicker.PendingModelOption = option
	updated, _ := m.applySettingsLCAgentModelPickerSelection()
	got, ok := updated.(Model)
	if !ok {
		t.Fatalf("updated model = %T, want tui.Model", updated)
	}
	return got
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
