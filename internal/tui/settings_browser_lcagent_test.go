package tui

import (
	"context"
	"errors"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"lcroom/internal/browserctl"
	"lcroom/internal/codexapp"
	"lcroom/internal/codexcli"
	"lcroom/internal/config"
	"lcroom/internal/events"
	"lcroom/internal/model"
	"lcroom/internal/service"
	"lcroom/internal/store"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestSettingsBrowserSectionShowsStatusSummary(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.PlaywrightPolicy = browserctl.Policy{
		ManagementMode:     browserctl.ManagementModeManaged,
		DefaultBrowserMode: browserctl.BrowserModeHeadless,
		LoginMode:          browserctl.LoginModePromote,
		IsolationScope:     browserctl.IsolationScopeTask,
	}

	m := Model{
		settingsMode:     true,
		settingsFields:   newSettingsFields(settings),
		settingsBaseline: &settings,
		width:            100,
		height:           24,
	}
	_ = m.setSettingsSelection(settingsFieldBrowserAutomation)

	rendered := ansi.Strip(m.renderSettingsContent(84, 22))
	for _, want := range []string{
		"Effective:",
		"Ownership:",
		"Live activity:",
		"Provider support:",
		"Codex:",
		"OpenCode:",
		"Claude Code:",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("browser settings status is missing %q: %q", want, rendered)
		}
	}
}

func TestSettingsBrowserAutomationEnterOpensPicker(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())

	m := Model{
		settingsMode:     true,
		settingsFields:   newSettingsFields(settings),
		settingsBaseline: &settings,
		width:            100,
		height:           24,
	}
	_ = m.setSettingsSelection(settingsFieldBrowserAutomation)

	updated, cmd := m.updateSettingsMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("browser automation enter should not save immediately")
	}
	if !got.settingsBrowserPickerVisible {
		t.Fatalf("browser automation enter should open the chooser")
	}
	if got.settingsSaving {
		t.Fatalf("browser automation enter should not start saving")
	}
	if got.status != "Choose when Little Control Room should show browser windows." {
		t.Fatalf("status = %q, want chooser status", got.status)
	}
}

func TestSettingsBrowserAutomationPickerHighlightsSelectionAndCurrentMode(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())

	m := Model{
		settingsMode:     true,
		settingsFields:   newSettingsFields(settings),
		settingsBaseline: &settings,
		width:            100,
		height:           24,
	}
	_ = m.setSettingsSelection(settingsFieldBrowserAutomation)

	updated, _ := m.updateSettingsMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	updated, _ = got.updateSettingsBrowserAutomationPickerMode(tea.KeyMsg{Type: tea.KeyDown})
	got = updated.(Model)

	rendered := ansi.Strip(got.renderSettingsBrowserAutomationPickerContent(56, 18))
	for _, want := range []string{
		"Only when needed  (current)",
		"› Always show",
		"About",
		"Selected: Always show",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("browser automation picker is missing %q: %q", want, rendered)
		}
	}
}

func TestSettingsBrowserAutomationFieldRendersChooserHint(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())

	m := Model{
		settingsMode:     true,
		settingsFields:   newSettingsFields(settings),
		settingsBaseline: &settings,
		width:            100,
		height:           24,
	}
	_ = m.setSettingsSelection(settingsFieldBrowserAutomation)

	rendered := ansi.Strip(m.renderSettingsContent(84, 22))
	for _, want := range []string{
		"Only when needed",
		"Enter to choose",
		"ctrl+s",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("browser settings field is missing %q: %q", want, rendered)
		}
	}
}

func TestSettingsLCAgentMainModelEnterOpensProviderModelPicker(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())

	m := Model{
		settingsMode:     true,
		settingsFields:   newSettingsFields(settings),
		settingsBaseline: &settings,
		width:            100,
		height:           24,
	}
	updated, _ := m.openSettingsDrilldown(settingsDrilldownLCAgent)
	m = updated.(Model)
	_ = m.setSettingsSelection(settingsFieldLCAgentModel)

	updated, cmd := m.updateSettingsMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("LCAgent main model enter should wait for provider selection before loading models")
	}
	if got.settingsLCAgentModelPicker == nil || got.settingsLCAgentModelPicker.Step != settingsLCAgentModelPickerStepProvider {
		t.Fatalf("LCAgent main model enter should open the provider-first model picker")
	}
	if got.status != "Choose the main model provider." {
		t.Fatalf("status = %q, want provider picker status", got.status)
	}
	rendered := ansi.Strip(got.renderSettingsLCAgentModelPickerContent(72, 18))
	for _, want := range []string{"LCAgent Main model", "OpenRouter", "Selected: OpenRouter"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("main model picker is missing %q: %q", want, rendered)
		}
	}
}

func TestSettingsLCAgentVisionCheckShortcutStartsProbe(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.LCAgentVisionProvider = "openrouter"
	settings.LCAgentVisionModel = "openai/gpt-4o-mini"

	m := Model{
		settingsMode:     true,
		settingsFields:   newSettingsFields(settings),
		settingsBaseline: &settings,
		width:            100,
		height:           24,
	}
	updated, _ := m.openSettingsDrilldown(settingsDrilldownLCAgent)
	m = updated.(Model)
	_ = m.setSettingsSelection(settingsFieldLCAgentVisionModel)

	updated, cmd := m.updateSettingsMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'v'}})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("vision check shortcut should start a check command")
	}
	if !got.settingsLCAgentVisionCheckInFlight {
		t.Fatalf("vision check should be marked in flight")
	}
	if got.status != "Checking LCAgent vision image input..." {
		t.Fatalf("status = %q, want vision check status", got.status)
	}
}

func TestSettingsShortcutVStillTypesInTextFields(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())

	m := Model{
		settingsMode:     true,
		settingsFields:   newSettingsFields(settings),
		settingsBaseline: &settings,
		width:            100,
		height:           24,
	}
	_ = m.setSettingsSelection(settingsFieldLCAgentEnvFile)

	updated, _ := m.updateSettingsMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'v'}})
	got := updated.(Model)
	if got.settingsFieldValue(settingsFieldLCAgentEnvFile) != "v" {
		t.Fatalf("env file value = %q, want typed v", got.settingsFieldValue(settingsFieldLCAgentEnvFile))
	}
}

func TestSettingsGettingStartedEnterOpensFocusedSetupPanel(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())

	m := Model{
		settingsMode:     true,
		settingsFields:   newSettingsFields(settings),
		settingsBaseline: &settings,
		width:            100,
		height:           24,
	}
	_ = m.setSettingsSelection(settingsFieldLCAgentRoutePreset)

	updated, _ := m.updateSettingsMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if got.settingsDrilldown != settingsDrilldownLCAgent {
		t.Fatalf("settingsDrilldown = %q, want LCAgent", got.settingsDrilldown)
	}
	if got.settingsLCAgentProviderVisible {
		t.Fatalf("top-level Getting Started Enter should open the setup panel before the picker")
	}
	rendered := ansi.Strip(got.renderSettingsContent(84, 24))
	for _, want := range []string{"LCAgent Setup", "Coding Route", "Main Model", "Utility Model", "Provider Credentials", "OpenRouter API key", "Web Search"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("LCAgent setup panel missing %q: %q", want, rendered)
		}
	}
	for _, hidden := range []string{"Runtime Policy", "LCAgent admin write", "LCAgent timeout", "LCAgent executable", "LCAgent env file"} {
		if strings.Contains(rendered, hidden) {
			t.Fatalf("LCAgent setup panel should keep advanced field %q out of the first setup panel: %q", hidden, rendered)
		}
	}
}

func TestSettingsLCAgentPresetHidesOverriddenMainModelFields(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.LCAgentRoutePreset = "mimo-2.5-pro-low"

	m := Model{
		settingsMode:     true,
		settingsFields:   newSettingsFields(settings),
		settingsBaseline: &settings,
		width:            100,
		height:           24,
	}
	updated, _ := m.openSettingsDrilldown(settingsDrilldownLCAgent)
	got := updated.(Model)

	rendered := ansi.Strip(got.renderSettingsContent(84, 24))
	for _, want := range []string{"LCAgent route preset", "Xiaomi base URL", "Xiaomi API key"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("LCAgent preset setup panel missing %q: %q", want, rendered)
		}
	}
	for _, hidden := range []string{"Main model provider", "Main model", "Main reasoning"} {
		if strings.Contains(rendered, hidden) {
			t.Fatalf("LCAgent preset setup panel should hide overridden field %q: %q", hidden, rendered)
		}
	}
}

func TestLCAgentXiaomiSetupShowsBaseURLWhenProjectReportsUseDifferentBackend(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.AIBackend = config.AIBackendDeepSeek
	settings.LCAgentProvider = "xiaomi"

	setupModel := Model{
		setupMode:        true,
		setupConfigMode:  true,
		setupStep:        setupStepLCAgentConfig,
		setupFocusedRole: setupRoleLCAgent,
		settingsBaseline: &settings,
		settingsFields:   newSettingsFields(settings),
		width:            120,
		height:           30,
	}
	setupFields := setupModel.setupConfigFieldIndexes()
	if !slices.Contains(setupFields, settingsFieldXiaomiBaseURL) || !slices.Contains(setupFields, settingsFieldXiaomiAPIKey) {
		t.Fatalf("LCAgent Xiaomi setup fields should include base URL and API key, got %#v", setupFields)
	}
	setupRendered := ansi.Strip(setupModel.renderSetupConfigContent(110))
	for _, want := range []string{"Xiaomi base URL", "Xiaomi API key"} {
		if !strings.Contains(setupRendered, want) {
			t.Fatalf("LCAgent Xiaomi setup missing %q: %q", want, setupRendered)
		}
	}
	if strings.Contains(setupRendered, "Xiaomi project model") {
		t.Fatalf("LCAgent Xiaomi setup should not expose the project-report Xiaomi model: %q", setupRendered)
	}

	settingsModel := Model{
		settingsMode:      true,
		settingsBaseline:  &settings,
		settingsFields:    newSettingsFields(settings),
		settingsDrilldown: settingsDrilldownLCAgent,
		width:             120,
		height:            30,
	}
	settingsFields := settingsModel.visibleSettingsDrilldownFieldOrder(settingsDrilldownLCAgent)
	if !slices.Contains(settingsFields, settingsFieldXiaomiBaseURL) || !slices.Contains(settingsFields, settingsFieldXiaomiAPIKey) {
		t.Fatalf("LCAgent Xiaomi settings fields should include base URL and API key, got %#v", settingsFields)
	}
	settingsRendered := ansi.Strip(settingsModel.renderSettingsContent(100, 24))
	for _, want := range []string{"LCAgent Setup", "Xiaomi base URL", "Xiaomi API key"} {
		if !strings.Contains(settingsRendered, want) {
			t.Fatalf("LCAgent Xiaomi settings missing %q: %q", want, settingsRendered)
		}
	}
}

func TestSettingsLCAgentMainModelPickerChoosesDeepSeek(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())

	m := Model{
		settingsMode:     true,
		settingsFields:   newSettingsFields(settings),
		settingsBaseline: &settings,
		width:            100,
		height:           24,
		settingsLCAgentModelPicker: &settingsLCAgentModelPickerState{
			FieldIndex: settingsFieldLCAgentModel,
			Provider:   "deepseek",
		},
	}

	got := applySettingsLCAgentModelPickerSelectionForTest(t, m, "deepseek", codexapp.ModelOption{
		Model:         "deepseek-v4-pro",
		ModelProvider: "deepseek",
	})
	if got.settingsLCAgentModelPicker != nil {
		t.Fatalf("LCAgent model picker should close after choosing")
	}
	if got.settingsFields[settingsFieldLCAgentProvider].input.Value() != "deepseek" {
		t.Fatalf("hidden LCAgent provider = %q, want deepseek", got.settingsFields[settingsFieldLCAgentProvider].input.Value())
	}
	updated, _ := got.openSettingsDrilldown(settingsDrilldownLCAgent)
	got = updated.(Model)
	rendered := ansi.Strip(got.renderSettingsContent(84, 24))
	if !strings.Contains(rendered, "DeepSeek API key") {
		t.Fatalf("DeepSeek provider should reveal DeepSeek credentials: %q", rendered)
	}
	if strings.Contains(rendered, "OpenRouter API key") {
		t.Fatalf("Utility Model should default to the Main Model and avoid showing an extra OpenRouter credential: %q", rendered)
	}
}

func TestSettingsLCAgentUtilityProviderPickerChoosesOff(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())

	m := Model{
		settingsMode:     true,
		settingsFields:   newSettingsFields(settings),
		settingsBaseline: &settings,
		width:            100,
		height:           24,
	}
	updated, _ := m.openSettingsDrilldown(settingsDrilldownLCAgent)
	m = updated.(Model)
	_ = m.setSettingsSelection(settingsFieldLCAgentUtilityModel)

	updated, cmd := m.updateSettingsMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("utility model provider step should not load models until a provider is chosen")
	}
	if got.settingsLCAgentModelPicker == nil || got.settingsLCAgentModelPicker.Step != settingsLCAgentModelPickerStepProvider {
		t.Fatalf("utility model enter should open the unified model picker")
	}
	if got.status != "Choose the utility model provider." {
		t.Fatalf("status = %q, want utility chooser status", got.status)
	}
	rendered := ansi.Strip(got.renderSettingsLCAgentModelPickerContent(56, 18))
	for _, want := range []string{"LCAgent Utility model", "> Same as Main  (current)", "Off", "Selected: Same as Main"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("utility model picker is missing %q: %q", want, rendered)
		}
	}

	updated, _ = got.updateSettingsLCAgentModelPickerMode(tea.KeyMsg{Type: tea.KeyDown})
	got = updated.(Model)
	updated, _ = got.updateSettingsLCAgentModelPickerMode(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if got.settingsLCAgentModelPicker != nil {
		t.Fatalf("utility model picker should close after choosing")
	}
	if got.settingsFields[settingsFieldLCAgentUtilityProvider].input.Value() != "off" {
		t.Fatalf("utility provider = %q, want off", got.settingsFields[settingsFieldLCAgentUtilityProvider].input.Value())
	}
	if !got.settingsFieldVisible(settingsFieldLCAgentUtilityModel) {
		t.Fatalf("utility model field should remain visible as the single Utility entry")
	}
}

func TestSettingsLCAgentChoicePickerChoosesRoutePreset(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())

	m := Model{
		settingsMode:     true,
		settingsFields:   newSettingsFields(settings),
		settingsBaseline: &settings,
		width:            100,
		height:           24,
	}
	updated, _ := m.openSettingsDrilldown(settingsDrilldownLCAgent)
	m = updated.(Model)
	_ = m.setSettingsSelection(settingsFieldLCAgentRoutePreset)

	rendered := ansi.Strip(m.renderSettingsContent(84, 24))
	for _, want := range []string{"Individual Fields", "▼"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("route preset row missing %q: %q", want, rendered)
		}
	}
	choiceValue := ansi.Strip(m.renderSettingsChoiceValue(settingsFieldLCAgentRoutePreset, true, 80))
	if !strings.Contains(choiceValue, "Enter to choose") {
		t.Fatalf("route preset choice value missing prompt: %q", choiceValue)
	}

	updated, cmd := m.updateSettingsMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("route preset picker enter should not save immediately")
	}
	if got.settingsChoicePicker == nil {
		t.Fatalf("route preset enter should open the generic choice picker")
	}
	rendered = ansi.Strip(got.renderSettingsChoicePickerContent(56, 18))
	for _, want := range []string{"LCAgent Route Preset", "> Individual Fields  (current)", "Quality Coding", "Selected: Individual Fields"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("route preset picker missing %q: %q", want, rendered)
		}
	}

	updated, _ = got.updateSettingsChoicePickerMode(tea.KeyMsg{Type: tea.KeyDown})
	got = updated.(Model)
	updated, _ = got.updateSettingsChoicePickerMode(tea.KeyMsg{Type: tea.KeyDown})
	got = updated.(Model)
	updated, _ = got.updateSettingsChoicePickerMode(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if got.settingsChoicePicker != nil {
		t.Fatalf("route preset picker should close after choosing")
	}
	if got.settingsFields[settingsFieldLCAgentRoutePreset].input.Value() != "quality" {
		t.Fatalf("route preset = %q, want quality", got.settingsFields[settingsFieldLCAgentRoutePreset].input.Value())
	}
}

func TestSettingsLCAgentChoicePickerChoosesAdminWrite(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())

	m := Model{
		settingsMode:     true,
		settingsFields:   newSettingsFields(settings),
		settingsBaseline: &settings,
		width:            100,
		height:           24,
	}
	_ = m.setSettingsSelection(settingsFieldLCAgentAdminWrite)

	updated, _ := m.updateSettingsMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if got.settingsChoicePicker == nil {
		t.Fatalf("admin write enter should open the generic choice picker")
	}
	rendered := ansi.Strip(got.renderSettingsChoicePickerContent(56, 18))
	for _, want := range []string{"LCAgent Admin Write", "> Off  (current)", "On"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("admin write picker missing %q: %q", want, rendered)
		}
	}
	updated, _ = got.updateSettingsChoicePickerMode(tea.KeyMsg{Type: tea.KeyDown})
	got = updated.(Model)
	updated, _ = got.updateSettingsChoicePickerMode(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if got.settingsFields[settingsFieldLCAgentAdminWrite].input.Value() != "true" {
		t.Fatalf("admin write = %q, want true", got.settingsFields[settingsFieldLCAgentAdminWrite].input.Value())
	}
}

func TestSettingsPanelHeightIsCappedOnTallTerminals(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())

	m := Model{
		settingsMode:     true,
		settingsFields:   newSettingsFields(settings),
		settingsBaseline: &settings,
		width:            140,
		height:           60,
	}
	_ = m.setSettingsSection(settingsSectionIndexByID(settingsSectionLCAgent))

	panel := m.renderSettingsPanel(140, 60)
	if got, wantMax := lipgloss.Height(panel), 34; got > wantMax {
		t.Fatalf("settings panel height = %d, want <= %d:\n%s", got, wantMax, ansi.Strip(panel))
	}
}

func TestSettingsLCAgentWebSearchEnterOpensPicker(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())

	m := Model{
		settingsMode:     true,
		settingsFields:   newSettingsFields(settings),
		settingsBaseline: &settings,
		width:            100,
		height:           24,
	}
	_ = m.setSettingsSelection(settingsFieldLCAgentWebSearchBackend)

	updated, cmd := m.updateSettingsMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("web search picker enter should not save immediately")
	}
	if !got.settingsLCAgentSearchPickerVisible {
		t.Fatalf("web search enter should open the chooser")
	}
	if got.status != "Choose the web search backend for LCAgent." {
		t.Fatalf("status = %q, want chooser status", got.status)
	}
}

func TestSettingsLCAgentWebSearchPickerChoosesExa(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())

	m := Model{
		settingsMode:     true,
		settingsFields:   newSettingsFields(settings),
		settingsBaseline: &settings,
		width:            100,
		height:           24,
	}
	_ = m.setSettingsSelection(settingsFieldLCAgentWebSearchBackend)

	updated, _ := m.openSettingsLCAgentWebSearchPicker()
	got := updated.(Model)
	updated, _ = got.updateSettingsLCAgentWebSearchPickerMode(tea.KeyMsg{Type: tea.KeyDown})
	got = updated.(Model)
	rendered := ansi.Strip(got.renderSettingsLCAgentWebSearchPickerContent(56, 18))
	for _, want := range []string{"Off  (current)", "> Exa", "Selected: Exa"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("web search picker is missing %q: %q", want, rendered)
		}
	}

	updated, _ = got.updateSettingsLCAgentWebSearchPickerMode(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if got.settingsLCAgentSearchPickerVisible {
		t.Fatalf("web search picker should close after choosing")
	}
	if got.settingsFields[settingsFieldLCAgentWebSearchBackend].input.Value() != "exa" {
		t.Fatalf("web search backend = %q, want exa", got.settingsFields[settingsFieldLCAgentWebSearchBackend].input.Value())
	}
}

func TestSetupLCAgentWebSearchEnterRendersPicker(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())

	m := Model{
		setupMode:           true,
		setupConfigMode:     true,
		setupStep:           setupStepLCAgentConfig,
		settingsBaseline:    &settings,
		settingsFields:      newSettingsFields(settings),
		setupFocusedRole:    setupRoleLCAgent,
		setupConfigSelected: 0,
		width:               100,
		height:              28,
	}
	fields := m.setupConfigFieldIndexes()
	webSearchPosition := -1
	for i, field := range fields {
		if field == settingsFieldLCAgentWebSearchBackend {
			webSearchPosition = i
			break
		}
	}
	if webSearchPosition < 0 {
		t.Fatalf("setup LCAgent fields should include web search backend, got %#v", fields)
	}
	m.setupConfigSelected = webSearchPosition

	updated, cmd := m.updateSetupMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("web search picker enter should not advance setup")
	}
	if !got.settingsLCAgentSearchPickerVisible {
		t.Fatalf("web search enter should open the chooser")
	}
	rendered := ansi.Strip(got.View())
	for _, want := range []string{"LCAgent Web Search", "Off", "Exa", "Google", "SearXNG", "Browser"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("setup web search picker view missing %q:\n%s", want, rendered)
		}
	}
}

func TestSettingsLCAgentWebSearchShowsOnlyRelevantFields(t *testing.T) {
	tests := []struct {
		name    string
		backend string
		want    []string
		hide    []string
	}{
		{
			name:    "off",
			backend: "off",
			hide:    []string{"LCAgent search key", "LCAgent search engine", "LCAgent SearXNG URL"},
		},
		{
			name:    "exa",
			backend: "exa",
			want:    []string{"LCAgent search key"},
			hide:    []string{"LCAgent search engine", "LCAgent SearXNG URL"},
		},
		{
			name:    "google",
			backend: "google",
			want:    []string{"LCAgent search key", "LCAgent search engine"},
			hide:    []string{"LCAgent SearXNG URL"},
		},
		{
			name:    "searxng",
			backend: "searxng",
			want:    []string{"LCAgent SearXNG URL"},
			hide:    []string{"LCAgent search key", "LCAgent search engine"},
		},
		{
			name:    "browser",
			backend: "browser",
			hide:    []string{"LCAgent search key", "LCAgent search engine", "LCAgent SearXNG URL"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			settings := config.EditableSettingsFromAppConfig(config.Default())
			settings.LCAgentWebSearchBackend = tt.backend
			settings.LCAgentWebSearchAPIKey = "search-key"
			settings.LCAgentWebSearchEngineID = "engine-id"
			settings.LCAgentWebSearchURL = "http://127.0.0.1:8888"
			m := Model{
				settingsMode:     true,
				settingsFields:   newSettingsFields(settings),
				settingsBaseline: &settings,
				width:            100,
				height:           32,
			}
			_ = m.setSettingsSelection(settingsFieldLCAgentWebSearchBackend)

			rendered := ansi.Strip(m.renderSettingsSectionLayout(96, 40))
			for _, want := range tt.want {
				if !strings.Contains(rendered, want) {
					t.Fatalf("settings for %s missing %q:\n%s", tt.backend, want, rendered)
				}
			}
			for _, hide := range tt.hide {
				if strings.Contains(rendered, hide) {
					t.Fatalf("settings for %s should hide %q:\n%s", tt.backend, hide, rendered)
				}
			}
		})
	}
}

func TestSettingsLCAgentWebSearchNavigationSkipsHiddenFields(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.LCAgentWebSearchBackend = "exa"
	m := Model{
		settingsMode:     true,
		settingsFields:   newSettingsFields(settings),
		settingsBaseline: &settings,
		width:            100,
		height:           24,
	}
	_ = m.setSettingsSelection(settingsFieldLCAgentWebSearchBackend)

	_ = m.moveSettingsSelection(1)
	if m.settingsSelected != settingsFieldLCAgentWebSearchAPIKey {
		t.Fatalf("after backend, selected = %d, want search API key", m.settingsSelected)
	}
	_ = m.moveSettingsSelection(1)
	if m.settingsSelected == settingsFieldLCAgentWebSearchEngineID || m.settingsSelected == settingsFieldLCAgentWebSearchURL {
		t.Fatalf("navigation selected hidden web search field %d", m.settingsSelected)
	}
}

func TestOpenLCAgentSessionFillsWebSearchSettings(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.LCAgentWebSearchBackend = "google"
	settings.LCAgentWebSearchAPIKey = "search-key"
	settings.LCAgentWebSearchEngineID = "engine-id"
	settings.LCAgentWebSearchURL = "http://127.0.0.1:8888"
	settings.LCAgentProvider = "openai"
	settings.LCAgentAdminWrite = true
	settings.XiaomiBaseURL = "https://token-plan-sgp.xiaomimimo.com/v1"
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Provider: codexapp.ProviderLCAgent,
			Started:  true,
			Status:   "LCAgent session ready",
		},
	}
	var captured codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		captured = req
		return session, nil
	})
	m := Model{
		codexManager:     manager,
		settingsBaseline: &settings,
		codexInput:       newCodexTextarea(),
		codexViewport:    viewport.New(0, 0),
		width:            100,
		height:           24,
	}

	cmd := m.openCodexSessionCmd(codexapp.LaunchRequest{
		Provider:    codexapp.ProviderLCAgent,
		ProjectPath: "/tmp/demo",
	})
	if cmd == nil {
		t.Fatalf("openCodexSessionCmd() returned nil")
	}
	msg := cmd()
	opened, ok := msg.(codexSessionOpenedMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want codexSessionOpenedMsg", msg)
	}
	if opened.err != nil {
		t.Fatalf("opened.err = %v, want nil", opened.err)
	}
	if captured.LCAgentWebSearchBackend != "google" {
		t.Fatalf("web search backend = %q, want google", captured.LCAgentWebSearchBackend)
	}
	if captured.LCAgentWebSearchAPIKey != "search-key" {
		t.Fatalf("web search API key = %q, want search-key", captured.LCAgentWebSearchAPIKey)
	}
	if captured.LCAgentWebSearchEngineID != "engine-id" {
		t.Fatalf("web search engine ID = %q, want engine-id", captured.LCAgentWebSearchEngineID)
	}
	if captured.LCAgentProvider != "openai" {
		t.Fatalf("lcagent provider = %q, want openai", captured.LCAgentProvider)
	}
	if captured.LCAgentXiaomiBaseURL != "https://token-plan-sgp.xiaomimimo.com/v1" {
		t.Fatalf("xiaomi base URL = %q, want token plan URL", captured.LCAgentXiaomiBaseURL)
	}
	if !captured.LCAgentAdminWrite {
		t.Fatalf("lcagent admin write = false, want true")
	}
}

func TestReloadEmbeddedLCAgentAfterSettingsUsesSavedWebSearch(t *testing.T) {
	previous := config.EditableSettingsFromAppConfig(config.Default())
	saved := previous
	saved.LCAgentWebSearchBackend = "exa"
	saved.LCAgentWebSearchAPIKey = "exa-key"
	saved.LCAgentAdminWrite = true
	saved.EmbeddedLCAgentModel = "custom-lcagent-model"
	initial := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Provider: codexapp.ProviderLCAgent,
			Started:  true,
			ThreadID: "lca_existing",
			Status:   "LCAgent run complete",
		},
	}
	reopened := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Provider: codexapp.ProviderLCAgent,
			Started:  true,
			ThreadID: "lca_existing",
			Status:   "Loaded LCAgent session lca_existing from disk",
		},
	}
	var openCount int
	var captured codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		openCount++
		captured = req
		if openCount == 1 {
			return initial, nil
		}
		return reopened, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		Provider:    codexapp.ProviderLCAgent,
		ProjectPath: "/tmp/demo",
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}
	m := Model{
		codexManager:             manager,
		settingsBaseline:         &previous,
		settingsEmbeddedProject:  "/tmp/demo",
		settingsEmbeddedProvider: codexapp.ProviderLCAgent,
	}
	projectPath, ok := m.shouldReloadEmbeddedLCAgentAfterSettingsSave(previous, saved)
	if !ok {
		t.Fatalf("shouldReloadEmbeddedLCAgentAfterSettingsSave() = false, want true")
	}
	if projectPath != "/tmp/demo" {
		t.Fatalf("reload project = %q, want /tmp/demo", projectPath)
	}

	cmd := m.reloadEmbeddedLCAgentAfterSettingsCmd(projectPath, saved)
	msg := cmd()
	opened, ok := msg.(codexSessionOpenedMsg)
	if !ok {
		t.Fatalf("reload cmd returned %T, want codexSessionOpenedMsg", msg)
	}
	if opened.err != nil {
		t.Fatalf("reload err = %v, want nil", opened.err)
	}
	if openCount != 2 {
		t.Fatalf("open count = %d, want 2", openCount)
	}
	if !initial.snapshot.Closed {
		t.Fatalf("initial session should be closed before reload")
	}
	if captured.ResumeID != "lca_existing" {
		t.Fatalf("resume ID = %q, want lca_existing", captured.ResumeID)
	}
	if captured.LCAgentWebSearchBackend != "exa" || captured.LCAgentWebSearchAPIKey != "exa-key" {
		t.Fatalf("captured web search = (%q, %q), want exa/exa-key", captured.LCAgentWebSearchBackend, captured.LCAgentWebSearchAPIKey)
	}
	if !captured.LCAgentAdminWrite {
		t.Fatalf("captured admin write = false, want true")
	}
	if captured.PendingModel != "custom-lcagent-model" {
		t.Fatalf("pending model = %q, want custom-lcagent-model", captured.PendingModel)
	}
	if !strings.Contains(opened.status, "Restarted LCAgent") {
		t.Fatalf("opened status = %q, want restart notice", opened.status)
	}
}

func TestSettingsBrowserSectionShowsLiveBrowserActivity(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.PlaywrightPolicy = browserctl.Policy{
		ManagementMode:     browserctl.ManagementModeManaged,
		DefaultBrowserMode: browserctl.BrowserModeHeadless,
		LoginMode:          browserctl.LoginModePromote,
		IsolationScope:     browserctl.IsolationScopeTask,
	}

	m := Model{
		settingsMode:     true,
		settingsFields:   newSettingsFields(settings),
		settingsBaseline: &settings,
		codexSnapshots: map[string]codexapp.Snapshot{
			"/tmp/demo": {
				Provider: codexapp.ProviderCodex,
				BrowserActivity: browserctl.SessionActivity{
					Policy:     settings.PlaywrightPolicy,
					State:      browserctl.SessionActivityStateWaitingForUser,
					ServerName: "playwright",
					ToolName:   "browser_navigate",
				},
			},
		},
		width:  120,
		height: 24,
	}
	_ = m.setSettingsSelection(settingsFieldBrowserAutomation)

	rendered := ansi.Strip(m.renderSettingsContent(120, 22))
	for _, want := range []string{
		"Codex / demo:",
		"playwright/browser_navigate is waiting for user",
		"input.",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("browser settings live activity is missing %q: %q", want, rendered)
		}
	}
}

func TestSettingsBrowserSectionShowsInteractiveLeaseOwner(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	controller := browserctl.NewController()
	ownerObservation := browserctl.Observation{
		Ref: browserctl.SessionRef{
			Provider:    "codex",
			ProjectPath: "/tmp/owner-demo",
			SessionID:   "thread-owner",
		},
		Policy:   settingsAutomaticPlaywrightPolicy,
		Activity: browserctl.SessionActivity{Policy: settingsAutomaticPlaywrightPolicy, State: browserctl.SessionActivityStateWaitingForUser, ServerName: "playwright", ToolName: "browser_navigate"},
		LoginURL: "https://example.test/owner",
	}
	waitingObservation := browserctl.Observation{
		Ref: browserctl.SessionRef{
			Provider:    "codex",
			ProjectPath: "/tmp/waiting-demo",
			SessionID:   "thread-waiting",
		},
		Policy:   settingsAutomaticPlaywrightPolicy,
		Activity: browserctl.SessionActivity{Policy: settingsAutomaticPlaywrightPolicy, State: browserctl.SessionActivityStateWaitingForUser, ServerName: "playwright", ToolName: "browser_navigate"},
		LoginURL: "https://example.test/waiting",
	}
	controller.Observe(ownerObservation)
	controller.Observe(waitingObservation)
	snapshot := controller.AcquireInteractive(ownerObservation.Ref).Snapshot

	m := Model{
		settingsMode:         true,
		settingsFields:       newSettingsFields(settings),
		settingsBaseline:     &settings,
		browserController:    controller,
		browserLeaseSnapshot: snapshot,
		width:                120,
		height:               24,
	}
	_ = m.setSettingsSelection(settingsFieldBrowserAutomation)

	rendered := ansi.Strip(m.renderSettingsContent(120, 22))
	for _, want := range []string{
		"Interactive browser reserved by Codex / owner-demo",
		"1 managed login flow(s) waiting",
		"Codex / waiting-demo is waiting to open a browser login flow.",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("browser settings lease status is missing %q: %q", want, rendered)
		}
	}
}

func TestBrowserAttentionOverlayRendersAndSkipsQuestionNotify(t *testing.T) {
	m := Model{
		browserAttention: &browserAttentionNotification{
			ProjectPath: "/tmp/demo",
			ProjectName: "demo",
			SessionID:   "thread-demo",
			Provider:    codexapp.ProviderCodex,
			Activity: browserctl.SessionActivity{
				Policy:     settingsAutomaticPlaywrightPolicy,
				State:      browserctl.SessionActivityStateWaitingForUser,
				ServerName: "playwright",
				ToolName:   "browser_navigate",
			},
		},
	}

	rendered := ansi.Strip(m.renderBrowserAttentionContent(72))
	for _, want := range []string{
		"Browser needs attention",
		"demo",
		"playwright/browser_navigate",
		"open session",
		"browser settings",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("browser attention overlay is missing %q: %q", want, rendered)
		}
	}

	waitingSnapshot := codexapp.Snapshot{
		Provider: codexapp.ProviderCodex,
		BrowserActivity: browserctl.SessionActivity{
			Policy:     settingsAutomaticPlaywrightPolicy,
			State:      browserctl.SessionActivityStateWaitingForUser,
			ServerName: "playwright",
		},
		PendingElicitation: &codexapp.ElicitationRequest{ID: "elicitation_1"},
	}
	m.detectQuestionNotification("/tmp/demo", waitingSnapshot)
	if m.questionNotify != nil {
		t.Fatalf("question notification should stay nil for browser attention waits")
	}
}

func TestBrowserAttentionOverlayShowsOpenBrowserForManagedLoginURL(t *testing.T) {
	m := Model{
		browserAttention: &browserAttentionNotification{
			ProjectPath: "/tmp/demo",
			ProjectName: "demo",
			SessionID:   "thread-demo",
			Provider:    codexapp.ProviderCodex,
			Activity: browserctl.SessionActivity{
				Policy:     settingsAutomaticPlaywrightPolicy,
				State:      browserctl.SessionActivityStateWaitingForUser,
				ServerName: "playwright",
				ToolName:   "browser_navigate",
			},
			ManagedBrowserSessionKey: "managed-demo",
			OpenURL:                  "https://example.test/login",
		},
	}

	rendered := ansi.Strip(m.renderBrowserAttentionContent(72))
	for _, want := range []string{
		"show browser",
		"open session",
		"Little Control Room can reveal the managed browser window",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("browser attention overlay is missing %q: %q", want, rendered)
		}
	}
}

func TestBrowserAttentionEnterOpensSession(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	m := Model{
		settingsBaseline: &settings,
		browserAttention: &browserAttentionNotification{
			ProjectPath: "/tmp/demo",
			ProjectName: "demo",
			SessionID:   "thread-demo",
			Provider:    codexapp.ProviderCodex,
			Activity: browserctl.SessionActivity{
				Policy:     settingsAutomaticPlaywrightPolicy,
				State:      browserctl.SessionActivityStateWaitingForUser,
				ServerName: "playwright",
			},
		},
	}

	updated, cmd := m.updateBrowserAttentionMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("browser attention Enter should queue follow-up commands")
	}
	if got.browserAttention != nil {
		t.Fatalf("browser attention should clear after opening the session")
	}
	if got.codexVisibleProject != "/tmp/demo" {
		t.Fatalf("codexVisibleProject = %q, want /tmp/demo", got.codexVisibleProject)
	}
	if got.status != "Codex browser needs your attention" {
		t.Fatalf("status = %q, want browser attention status", got.status)
	}
}

func TestBrowserAttentionEnterOpensBrowserAndSessionForManagedLoginURL(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())

	m := Model{
		settingsBaseline: &settings,
		browserAttention: &browserAttentionNotification{
			ProjectPath: "/tmp/demo",
			ProjectName: "demo",
			SessionID:   "thread-demo",
			Provider:    codexapp.ProviderCodex,
			Activity: browserctl.SessionActivity{
				Policy:     settingsAutomaticPlaywrightPolicy,
				State:      browserctl.SessionActivityStateWaitingForUser,
				ServerName: "playwright",
			},
			ManagedBrowserSessionKey: "managed-demo",
			OpenURL:                  "https://example.test/login",
		},
	}

	previousSessionRevealer := managedBrowserSessionRevealer
	defer func() {
		managedBrowserSessionRevealer = previousSessionRevealer
	}()

	revealedSessionKey := ""
	managedBrowserSessionRevealer = func(_ string, sessionKey string) (browserctl.ManagedPlaywrightState, error) {
		revealedSessionKey = sessionKey
		return browserctl.ManagedPlaywrightState{SessionKey: sessionKey, BrowserPID: 123, RevealSupported: true}, nil
	}

	updated, cmd := m.updateBrowserAttentionMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("browser attention Enter should queue login follow-up commands")
	}
	if got.browserAttention != nil {
		t.Fatalf("browser attention should clear after opening the browser flow")
	}
	if got.codexVisibleProject != "/tmp/demo" {
		t.Fatalf("codexVisibleProject = %q, want /tmp/demo", got.codexVisibleProject)
	}
	if got.status != "Showing the managed browser window and switching to the embedded session..." {
		t.Fatalf("status = %q, want browser-reveal status", got.status)
	}

	msgs := collectCmdMsgs(cmd)
	var openMsg browserOpenMsg
	foundOpenMsg := false
	for _, msg := range msgs {
		if candidate, ok := msg.(browserOpenMsg); ok {
			openMsg = candidate
			foundOpenMsg = true
			break
		}
	}
	if !foundOpenMsg {
		t.Fatalf("expected browserOpenMsg in batched login commands, got %#v", msgs)
	}
	if openMsg.err != nil {
		t.Fatalf("browserOpenMsg.err = %v, want nil", openMsg.err)
	}
	if openMsg.status != "Managed browser window is ready. Finish the browser flow there, then return to the embedded session if more input is needed." {
		t.Fatalf("browserOpenMsg.status = %q, want browser reveal success status", openMsg.status)
	}
	if revealedSessionKey != "managed-demo" {
		t.Fatalf("revealed session key = %q, want managed-demo", revealedSessionKey)
	}
}

func TestBrowserAttentionEnterShowsBlockedStatusWhenLeaseOwnedElsewhere(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	controller := browserctl.NewController()
	ownerObservation := browserctl.Observation{
		Ref: browserctl.SessionRef{
			Provider:    "codex",
			ProjectPath: "/tmp/owner-demo",
			SessionID:   "thread-owner",
		},
		Policy:   settingsAutomaticPlaywrightPolicy,
		Activity: browserctl.SessionActivity{Policy: settingsAutomaticPlaywrightPolicy, State: browserctl.SessionActivityStateWaitingForUser, ServerName: "playwright", ToolName: "browser_navigate"},
		LoginURL: "https://example.test/owner",
	}
	controller.Observe(ownerObservation)
	controller.AcquireInteractive(ownerObservation.Ref)

	m := Model{
		settingsBaseline:     &settings,
		browserController:    controller,
		browserLeaseSnapshot: controller.Snapshot(),
		browserAttention: &browserAttentionNotification{
			ProjectPath: "/tmp/demo",
			ProjectName: "demo",
			SessionID:   "thread-demo",
			Provider:    codexapp.ProviderCodex,
			Activity: browserctl.SessionActivity{
				Policy:     settingsAutomaticPlaywrightPolicy,
				State:      browserctl.SessionActivityStateWaitingForUser,
				ServerName: "playwright",
			},
			ManagedBrowserSessionKey: "managed-demo",
			OpenURL:                  "https://example.test/login",
		},
	}

	updated, cmd := m.updateBrowserAttentionMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("blocked browser attention should still reveal the embedded session")
	}
	if got.codexVisibleProject != "/tmp/demo" {
		t.Fatalf("codexVisibleProject = %q, want /tmp/demo", got.codexVisibleProject)
	}
	if !strings.Contains(got.status, "Interactive browser is already reserved by Codex / owner-demo") {
		t.Fatalf("status = %q, want blocked browser ownership status", got.status)
	}
}

func TestOpenManagedBrowserLoginReleasesLeaseOnBrowserOpenFailure(t *testing.T) {
	controller := browserctl.NewController()
	observation := browserctl.Observation{
		Ref: browserctl.SessionRef{
			Provider:    "codex",
			ProjectPath: "/tmp/demo",
			SessionID:   "thread-demo",
		},
		Policy:   settingsAutomaticPlaywrightPolicy,
		Activity: browserctl.SessionActivity{Policy: settingsAutomaticPlaywrightPolicy, State: browserctl.SessionActivityStateWaitingForUser, ServerName: "playwright", ToolName: "browser_navigate"},
		LoginURL: "https://example.test/login",
	}
	controller.Observe(observation)

	previousSessionRevealer := managedBrowserSessionRevealer
	defer func() {
		managedBrowserSessionRevealer = previousSessionRevealer
	}()
	managedBrowserSessionRevealer = func(_ string, sessionKey string) (browserctl.ManagedPlaywrightState, error) {
		return browserctl.ManagedPlaywrightState{}, errors.New("boom")
	}

	m := Model{
		browserController:    controller,
		browserLeaseSnapshot: controller.Snapshot(),
	}

	updated, cmd := m.openManagedBrowserLogin(
		"/tmp/demo",
		codexapp.ProviderCodex,
		"thread-demo",
		"managed-demo",
		browserctl.SessionActivity{
			Policy:     settingsAutomaticPlaywrightPolicy,
			State:      browserctl.SessionActivityStateWaitingForUser,
			ServerName: "playwright",
			ToolName:   "browser_navigate",
		},
		"https://example.test/login",
		"Showing the managed browser window...",
		"Managed browser window is ready.",
	)
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("openManagedBrowserLogin should queue a browser-open command")
	}
	if got.browserLeaseSnapshot.Interactive == nil {
		t.Fatalf("interactive lease should be held while browser open is pending")
	}

	msg := cmd()
	openMsg, ok := msg.(browserOpenMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want browserOpenMsg", msg)
	}
	if openMsg.err == nil {
		t.Fatalf("browserOpenMsg.err = nil, want open failure")
	}
	if !openMsg.browserLeaseSnapshotSet {
		t.Fatalf("browser open failure should return an updated lease snapshot")
	}
	if openMsg.browserLeaseSnapshot.Interactive != nil {
		t.Fatalf("interactive lease should be released after open failure, got %#v", openMsg.browserLeaseSnapshot.Interactive)
	}
	if len(openMsg.browserLeaseSnapshot.Waiting) != 1 {
		t.Fatalf("waiting leases = %d, want 1 after open failure", len(openMsg.browserLeaseSnapshot.Waiting))
	}
}

func TestBrowserAttentionBrowserSettingsShortcutOpensBrowserSection(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	m := Model{
		settingsBaseline: &settings,
		browserAttention: &browserAttentionNotification{
			ProjectPath: "/tmp/demo",
			ProjectName: "demo",
			Provider:    codexapp.ProviderCodex,
			Activity: browserctl.SessionActivity{
				Policy:     settingsAutomaticPlaywrightPolicy,
				State:      browserctl.SessionActivityStateWaitingForUser,
				ServerName: "playwright",
			},
		},
	}

	updated, cmd := m.updateBrowserAttentionMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b")})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("browser attention b should open browser settings")
	}
	if got.browserAttention != nil {
		t.Fatalf("browser attention should clear after opening browser settings")
	}
	if !got.settingsMode {
		t.Fatalf("settings mode should open from browser attention")
	}
	if got.settingsSelected != settingsFieldBrowserAutomation {
		t.Fatalf("settingsSelected = %d, want browser automation field", got.settingsSelected)
	}
	if got.activeSettingsSection().id != settingsSectionBrowser {
		t.Fatalf("active settings section = %q, want browser", got.activeSettingsSection().id)
	}
}

func TestSettingsCtrlSSavesConfigAndClosesModal(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	m := Model{
		settingsMode:       true,
		settingsConfigPath: filepath.Join(home, ".little-control-room", "config.toml"),
		settingsFields:     newSettingsFields(config.EditableSettingsFromAppConfig(config.Default())),
		width:              100,
		height:             24,
	}
	_ = m.setSettingsSelection(0)

	m.settingsFields[settingsFieldOpenAIAPIKey].input.SetValue("sk-test-example")
	m.settingsFields[settingsFieldIncludePaths].input.SetValue("/tmp/a,/tmp/b")
	m.settingsFields[settingsFieldExcludePaths].input.SetValue("/tmp/skip")
	m.settingsFields[settingsFieldExcludeProjectPatterns].input.SetValue("quickgame_*,secret-demo")
	m.settingsFields[settingsFieldCodexLaunchPreset].input.SetValue("full-auto")
	m.settingsFields[settingsFieldActiveThreshold].input.SetValue("15m")
	m.settingsFields[settingsFieldStuckThreshold].input.SetValue("3h")
	m.settingsFields[settingsFieldInterval].input.SetValue("45s")
	xiaomiBaseURL := "https://token-plan-sgp.xiaomimimo.com/v1"
	m.settingsFields[settingsFieldLCAgentProvider].input.SetValue("xiaomi")
	m.settingsFields[settingsFieldXiaomiBaseURL].input.SetValue(xiaomiBaseURL)

	updated, cmd := m.updateSettingsMode(tea.KeyMsg{Type: tea.KeyCtrlS})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("expected save command")
	}
	if !got.settingsSaving {
		t.Fatalf("settings ctrl+s should mark saving in progress")
	}
	if got.status != "Saving settings..." {
		t.Fatalf("status = %q, want saving message", got.status)
	}

	msg := cmd()
	finalModel, _ := got.Update(msg)
	saved := finalModel.(Model)
	if saved.settingsMode {
		t.Fatalf("settings mode should close after a successful save")
	}
	if !strings.Contains(saved.status, "Project scope changed; rescanning projects in the background now") {
		t.Fatalf("status = %q, want scope rescan notice", saved.status)
	}

	configPath := filepath.Join(home, ".little-control-room", "config.toml")
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read saved config: %v", err)
	}
	text := string(raw)
	if !strings.Contains(text, "openai_api_key = \"sk-test-example\"") || !strings.Contains(text, "include_paths = [") || !strings.Contains(text, "exclude_paths = [") || !strings.Contains(text, "exclude_project_patterns = [") || !strings.Contains(text, "codex_launch_preset = \"full-auto\"") || !strings.Contains(text, "interval = \"45s\"") {
		t.Fatalf("saved config missing edited values: %q", text)
	}
	if !strings.Contains(text, `xiaomi_base_url = "`+xiaomiBaseURL+`"`) {
		t.Fatalf("saved config missing Xiaomi base URL: %q", text)
	}
	if saved.xiaomiBaseURL() != xiaomiBaseURL {
		t.Fatalf("model Xiaomi base URL after settings save = %q, want %q", saved.xiaomiBaseURL(), xiaomiBaseURL)
	}
}

func TestSettingsEnterOnTextFieldDoesNotSave(t *testing.T) {
	m := Model{
		settingsMode:   true,
		settingsFields: newSettingsFields(config.EditableSettingsFromAppConfig(config.Default())),
		width:          100,
		height:         24,
	}
	updated, _ := m.openSettingsDrilldown(settingsDrilldownProjectScope)
	m = updated.(Model)

	updated, cmd := m.updateSettingsMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("settings enter on text field should not save")
	}
	if got.settingsSaving {
		t.Fatalf("settings enter on text field should not mark saving")
	}
	if got.status != "Press ctrl+s to save settings." {
		t.Fatalf("status = %q, want ctrl+s hint", got.status)
	}
}

func TestSettingsOpenWarnsAboutMissingLCAgentEnvFile(t *testing.T) {
	missingPath := filepath.Join(t.TempDir(), "missing.env")
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.LCAgentEnvFile = missingPath

	m := Model{width: 100, height: 24}
	cmd := m.openSettingsModeWithBaseline(settings)
	if cmd == nil {
		t.Fatalf("opening settings should focus the first field")
	}
	if m.status != "LCAgent env file warning (use /errors)" {
		t.Fatalf("status = %q, want missing env file error-log hint", m.status)
	}
	if len(m.errorLogEntries) != 1 {
		t.Fatalf("error log count = %d, want 1", len(m.errorLogEntries))
	}
	if m.errorLogEntries[0].Status != "LCAgent env file warning" {
		t.Fatalf("error log status = %q", m.errorLogEntries[0].Status)
	}
	if m.errorLogEntries[0].Message != "LCAgent env file not found: "+missingPath {
		t.Fatalf("error log message = %q, want missing env file detail", m.errorLogEntries[0].Message)
	}
	if !m.settingsMode {
		t.Fatalf("settings mode should be open")
	}
}

func TestSettingsWarnsWhenXiaomiTokenPlanKeyUsesRegularURL(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.AIBackend = config.AIBackendXiaomi
	settings.LCAgentProvider = "xiaomi"
	settings.XiaomiAPIKey = "TC_example"
	settings.XiaomiBaseURL = ""

	warning := settingsXiaomiTokenPlanBaseURLWarning(settings)
	if !strings.Contains(warning, "Token Plan key") || !strings.Contains(warning, "regular API URL") {
		t.Fatalf("warning = %q, want obvious Token Plan URL warning", warning)
	}
	if got := settingsCloudConnectionState(settings, config.AIBackendXiaomi); got != "blocked" {
		t.Fatalf("settingsCloudConnectionState() = %q, want blocked", got)
	}
	if users := strings.Join(settingsProviderUsers(settings, config.AIBackendXiaomi), ", "); !strings.Contains(users, "LCAgent") {
		t.Fatalf("Xiaomi provider users = %q, want LCAgent included", users)
	}
	state, _, detail := lcagentCredentialSmokeCheck(settings)
	if state != "blocked" || !strings.Contains(detail, "token-plan base URL") {
		t.Fatalf("lcagentCredentialSmokeCheck() = (%q, %q), want blocked token-plan URL detail", state, detail)
	}
}

func TestLCAgentCredentialSmokeBlockedStateRendersRed(t *testing.T) {
	prevProfile := lipgloss.ColorProfile()
	prevDarkBackground := lipgloss.HasDarkBackground()
	lipgloss.SetColorProfile(termenv.ANSI256)
	lipgloss.SetHasDarkBackground(true)
	t.Cleanup(func() {
		lipgloss.SetColorProfile(prevProfile)
		lipgloss.SetHasDarkBackground(prevDarkBackground)
	})

	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.LCAgentProvider = "xiaomi"
	settings.XiaomiAPIKey = "TC_example"
	settings.XiaomiBaseURL = ""
	settingsFields := newSettingsFields(settings)
	setupModel := Model{
		setupMode:        true,
		setupConfigMode:  true,
		setupStep:        setupStepLCAgentConfig,
		setupFocusedRole: setupRoleLCAgent,
		settingsBaseline: &settings,
		settingsFields:   settingsFields,
		width:            120,
		height:           30,
	}

	setupRendered := setupModel.renderSetupConfigContent(110)
	if !strings.Contains(ansi.Strip(setupRendered), "Credential smoke: blocked - Token Plan key needs the regional Xiaomi token-plan base URL.") {
		t.Fatalf("setup credential smoke missing blocked detail: %q", ansi.Strip(setupRendered))
	}
	if !strings.Contains(setupRendered, "38;5;203") {
		t.Fatalf("setup credential smoke should render blocked in danger red, got %q", setupRendered)
	}

	settingsModel := Model{
		settingsBaseline:  &settings,
		settingsFields:    newSettingsFields(settings),
		settingsDrilldown: settingsDrilldownLCAgent,
	}
	settingsRendered := strings.Join(settingsModel.renderSettingsDrilldownStatus(110), "\n")
	if !strings.Contains(ansi.Strip(settingsRendered), "Xiaomi connection: blocked - Token Plan key needs the regional Xiaomi token-plan base URL.") {
		t.Fatalf("settings credential smoke missing blocked detail: %q", ansi.Strip(settingsRendered))
	}
	if !strings.Contains(settingsRendered, "38;5;203") {
		t.Fatalf("settings credential smoke should render blocked in danger red, got %q", settingsRendered)
	}
}

func TestSettingsTreatsMimoRoutePresetAsXiaomiProvider(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.LCAgentRoutePreset = "mimo-2.5-pro-low"
	settings.XiaomiAPIKey = "xm-test-example"

	if got := settingsLCAgentMainProvider(settings); got != "xiaomi" {
		t.Fatalf("settingsLCAgentMainProvider() = %q, want xiaomi", got)
	}
	if settingsLCAgentCredentialField(settings) != settingsFieldXiaomiAPIKey {
		t.Fatalf("settingsLCAgentCredentialField() = %d, want %d", settingsLCAgentCredentialField(settings), settingsFieldXiaomiAPIKey)
	}
	if got := settingsCloudConnectionState(settings, config.AIBackendXiaomi); got != "ready" {
		t.Fatalf("settingsCloudConnectionState() = %q, want ready", got)
	}
	if users := strings.Join(settingsProviderUsers(settings, config.AIBackendXiaomi), ", "); !strings.Contains(users, "LCAgent") {
		t.Fatalf("Xiaomi provider users = %q, want LCAgent included", users)
	}
}

func TestSettingsTreatsDeepSeekRoutePresetAsDeepSeekProvider(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.LCAgentRoutePreset = "balanced"
	settings.DeepSeekAPIKey = "ds-test-example"

	if got := settingsLCAgentMainProvider(settings); got != "deepseek" {
		t.Fatalf("settingsLCAgentMainProvider() = %q, want deepseek", got)
	}
	if settingsLCAgentCredentialField(settings) != settingsFieldDeepSeekAPIKey {
		t.Fatalf("settingsLCAgentCredentialField() = %d, want %d", settingsLCAgentCredentialField(settings), settingsFieldDeepSeekAPIKey)
	}
	if got := settingsCloudConnectionState(settings, config.AIBackendDeepSeek); got != "ready" {
		t.Fatalf("settingsCloudConnectionState() = %q, want ready", got)
	}
	if users := strings.Join(settingsProviderUsers(settings, config.AIBackendDeepSeek), ", "); !strings.Contains(users, "LCAgent") {
		t.Fatalf("DeepSeek provider users = %q, want LCAgent included", users)
	}
}

func TestSettingsXiaomiUtilityDefaultUsesNonProModel(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.LCAgentRoutePreset = "mimo-2.5-pro-low"
	settings.LCAgentUtilityProvider = "main"

	got := settingsLCAgentUtilityDefaultLabel(settings)
	if !strings.Contains(got, "mimo-v2.5") || strings.Contains(got, "mimo-v2.5-pro") {
		t.Fatalf("settingsLCAgentUtilityDefaultLabel() = %q, want non-Pro Xiaomi 2.5", got)
	}
}

func TestNewWarnsAboutMissingLCAgentEnvFile(t *testing.T) {
	missingPath := filepath.Join(t.TempDir(), "missing.env")
	cfg := config.Default()
	cfg.LCAgentEnvFile = missingPath
	svc := service.New(cfg, nil, events.NewBus(), nil)

	m := New(context.Background(), svc)
	if m.status != "LCAgent env file warning (use /errors)" {
		t.Fatalf("status = %q, want startup error-log hint", m.status)
	}
	if len(m.errorLogEntries) != 1 {
		t.Fatalf("error log count = %d, want 1", len(m.errorLogEntries))
	}
	if m.errorLogEntries[0].Message != "LCAgent env file not found: "+missingPath {
		t.Fatalf("error log message = %q, want missing env file detail", m.errorLogEntries[0].Message)
	}
}

func TestSettingsBrowserAutomationMapsToManagedPolicy(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	m := Model{
		settingsMode:       true,
		settingsConfigPath: filepath.Join(home, ".little-control-room", "config.toml"),
		settingsFields:     newSettingsFields(config.EditableSettingsFromAppConfig(config.Default())),
		width:              100,
		height:             24,
	}
	_ = m.setSettingsSelection(settingsFieldBrowserAutomation)
	updated, cmd := m.updateSettingsMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("browser automation enter should not queue a save command")
	}
	if !got.settingsBrowserPickerVisible {
		t.Fatalf("browser automation enter should open the chooser")
	}

	updated, cmd = got.updateSettingsBrowserAutomationPickerMode(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if cmd != nil {
		t.Fatalf("browser automation choice apply should not save immediately")
	}
	if got.settingsBrowserPickerVisible {
		t.Fatalf("browser automation chooser should close after choosing")
	}
	if got.settingsFields[settingsFieldBrowserAutomation].input.Value() != "only-when-needed" {
		t.Fatalf("browser automation value = %q, want only-when-needed", got.settingsFields[settingsFieldBrowserAutomation].input.Value())
	}

	updated, cmd = got.updateSettingsMode(tea.KeyMsg{Type: tea.KeyCtrlS})
	got = updated.(Model)
	if cmd == nil {
		t.Fatalf("expected save command after choosing browser automation")
	}
	if !got.settingsSaving {
		t.Fatalf("settings ctrl+s should mark saving in progress")
	}

	msg := cmd()
	finalModel, _ := got.Update(msg)
	saved := finalModel.(Model)
	if saved.settingsMode {
		t.Fatalf("settings mode should close after a successful save")
	}

	configPath := filepath.Join(home, ".little-control-room", "config.toml")
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read saved config: %v", err)
	}
	text := string(raw)
	for _, want := range []string{
		"playwright_management_mode = \"managed\"",
		"playwright_default_browser_mode = \"headless\"",
		"playwright_login_mode = \"promote\"",
		"playwright_isolation_scope = \"task\"",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("saved config missing %q: %q", want, text)
		}
	}
}

func TestSettingsSavingBlocksRepeatCtrlS(t *testing.T) {
	m := Model{
		settingsMode:   true,
		settingsSaving: true,
		settingsFields: newSettingsFields(config.EditableSettingsFromAppConfig(config.Default())),
		status:         "Saving settings...",
		width:          100,
		height:         24,
	}
	_ = m.setSettingsSelection(0)

	updated, cmd := m.updateSettingsMode(tea.KeyMsg{Type: tea.KeyCtrlS})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("settings ctrl+s should not queue another save while saving")
	}
	if !got.settingsSaving {
		t.Fatalf("settings saving flag should stay true until the save completes")
	}
	if got.status != "Saving settings..." {
		t.Fatalf("status = %q, want existing saving message", got.status)
	}
}

func TestSettingsCtrlSShowsValidationError(t *testing.T) {
	m := Model{
		settingsMode:   true,
		settingsFields: newSettingsFields(config.EditableSettingsFromAppConfig(config.Default())),
		width:          100,
		height:         24,
	}
	_ = m.setSettingsSelection(0)
	m.settingsFields[settingsFieldOpenAIAPIKey].input.SetValue("sk-test-example")
	m.settingsFields[settingsFieldActiveThreshold].input.SetValue("20m")
	m.settingsFields[settingsFieldStuckThreshold].input.SetValue("10m")

	updated, cmd := m.updateSettingsMode(tea.KeyMsg{Type: tea.KeyCtrlS})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("expected no save command when validation fails")
	}
	if got.status != "stuck-threshold must be greater than active-threshold" {
		t.Fatalf("status = %q, want validation message", got.status)
	}
	if !got.settingsMode {
		t.Fatalf("settings mode should stay open after validation failure")
	}
}

func TestSettingsCtrlSWarnsAboutMissingLCAgentEnvFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	missingPath := filepath.Join(home, "missing.env")

	m := Model{
		settingsMode:       true,
		settingsConfigPath: filepath.Join(home, ".little-control-room", "config.toml"),
		settingsFields:     newSettingsFields(config.EditableSettingsFromAppConfig(config.Default())),
		width:              100,
		height:             24,
	}
	_ = m.setSettingsSelection(settingsFieldLCAgentEnvFile)
	m.settingsFields[settingsFieldLCAgentEnvFile].input.SetValue(missingPath)

	updated, cmd := m.updateSettingsMode(tea.KeyMsg{Type: tea.KeyCtrlS})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("expected save command even with missing optional env file")
	}
	if !got.settingsSaving {
		t.Fatalf("settings should still start saving for missing optional env file")
	}
	msg := cmd()
	finalModel, _ := got.Update(msg)
	saved := finalModel.(Model)
	if saved.settingsMode {
		t.Fatalf("settings mode should close after saving with a missing optional env file")
	}
	if !strings.Contains(saved.status, "LCAgent env file warning (use /errors)") {
		t.Fatalf("status = %q, want missing env file error-log hint", saved.status)
	}
	if len(saved.errorLogEntries) != 1 {
		t.Fatalf("error log count = %d, want 1", len(saved.errorLogEntries))
	}
	if saved.errorLogEntries[0].Message != "LCAgent env file not found: "+missingPath {
		t.Fatalf("error log message = %q, want missing env file detail", saved.errorLogEntries[0].Message)
	}
}

func TestSettingsSavePreservesEmbeddedModelPreferences(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfg := config.Default()
	cfg.ConfigPath = filepath.Join(home, ".little-control-room", "config.toml")
	cfg.EmbeddedCodexModel = "gpt-5.4"
	cfg.EmbeddedCodexReasoning = "high"
	cfg.EmbeddedClaudeModel = "sonnet"
	cfg.EmbeddedClaudeReasoning = "max"
	cfg.EmbeddedOpenCodeModel = "openai/gpt-5.4"
	cfg.EmbeddedOpenCodeReasoning = "medium"

	svc := service.New(cfg, nil, events.NewBus(), nil)
	m := New(context.Background(), svc)
	m.settingsMode = true
	m.settingsFields = newSettingsFields(config.EditableSettingsFromAppConfig(cfg))
	m.width = 100
	m.height = 24
	_ = m.setSettingsSelection(settingsFieldOpenAIAPIKey)
	m.settingsFields[settingsFieldOpenAIAPIKey].input.SetValue("sk-test-example")

	updated, cmd := m.updateSettingsMode(tea.KeyMsg{Type: tea.KeyCtrlS})
	if cmd == nil {
		t.Fatalf("expected save command from settings ctrl+s")
	}
	msg := cmd()
	savedMsg, ok := msg.(settingsSavedMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want settingsSavedMsg", msg)
	}
	if savedMsg.err != nil {
		t.Fatalf("settings save returned error = %v", savedMsg.err)
	}
	got := updated.(Model)
	updated, _ = got.Update(savedMsg)
	got = updated.(Model)

	raw, err := os.ReadFile(cfg.ConfigPath)
	if err != nil {
		t.Fatalf("read saved config: %v", err)
	}
	text := string(raw)
	for _, want := range []string{
		"embedded_codex_model = \"gpt-5.4\"",
		"embedded_codex_reasoning_effort = \"high\"",
		"embedded_claude_model = \"sonnet\"",
		"embedded_claude_reasoning_effort = \"max\"",
		"embedded_opencode_model = \"openai/gpt-5.4\"",
		"embedded_opencode_reasoning_effort = \"medium\"",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("saved config missing %q: %q", want, text)
		}
	}
	if got.currentSettingsBaseline().EmbeddedCodexModel != "gpt-5.4" {
		t.Fatalf("baseline embedded codex model = %q, want gpt-5.4", got.currentSettingsBaseline().EmbeddedCodexModel)
	}
	if got.currentSettingsBaseline().EmbeddedClaudeModel != "sonnet" {
		t.Fatalf("baseline embedded claude model = %q, want sonnet", got.currentSettingsBaseline().EmbeddedClaudeModel)
	}
}

func TestCodexActionMsgPersistsEmbeddedModelPreferencesToConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfg := config.Default()
	cfg.ConfigPath = filepath.Join(home, ".little-control-room", "config.toml")
	svc := service.New(cfg, nil, events.NewBus(), nil)
	m := New(context.Background(), svc)

	updated, cmd := m.Update(codexActionMsg{
		projectPath: "/tmp/demo",
		status:      "Embedded model set to gpt-5.4 with high reasoning for the next prompt",
		provider:    codexapp.ProviderCodex,
		model:       "gpt-5.4",
		reasoning:   "high",
	})
	if cmd == nil {
		t.Fatalf("codexActionMsg should trigger a config save command")
	}
	got := updated.(Model)
	if got.status != "Embedded model set to gpt-5.4 with high reasoning for the next prompt" {
		t.Fatalf("status = %q, want model update message", got.status)
	}

	msg := cmd()
	savedMsg, ok := msg.(embeddedModelPreferencesSavedMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want embeddedModelPreferencesSavedMsg", msg)
	}
	if savedMsg.err != nil {
		t.Fatalf("embedded model preference save returned error = %v", savedMsg.err)
	}

	updated, _ = got.Update(savedMsg)
	got = updated.(Model)
	raw, err := os.ReadFile(cfg.ConfigPath)
	if err != nil {
		t.Fatalf("read saved config: %v", err)
	}
	text := string(raw)
	if !strings.Contains(text, "embedded_codex_model = \"gpt-5.4\"") || !strings.Contains(text, "embedded_codex_reasoning_effort = \"high\"") {
		t.Fatalf("saved config missing codex model preference: %q", text)
	}
	if got.currentSettingsBaseline().EmbeddedCodexModel != "gpt-5.4" || got.currentSettingsBaseline().EmbeddedCodexReasoning != "high" {
		t.Fatalf("settings baseline = %#v, want saved codex model preference", got.currentSettingsBaseline())
	}
}

func TestSettingsEscCancelsWithoutQuitting(t *testing.T) {
	m := Model{
		settingsMode:        true,
		settingsSectionMenu: true,
		settingsFields:      newSettingsFields(config.EditableSettingsFromAppConfig(config.Default())),
		width:               100,
		height:              24,
	}
	_ = m.setSettingsSelection(0)

	updated, cmd := m.updateSettingsMode(tea.KeyMsg{Type: tea.KeyEsc})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("esc should not return a command")
	}
	if got.settingsMode {
		t.Fatalf("settings mode should close after escape")
	}
	if got.status != "Settings edit canceled" {
		t.Fatalf("status = %q, want canceled message", got.status)
	}
}

func TestSettingsAPIKeyHintShowsMaskedSuffix(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.AIBackend = config.AIBackendOpenAIAPI
	settings.OpenAIAPIKey = "sk-live-12345"

	m := Model{
		settingsMode:     true,
		settingsFields:   newSettingsFields(settings),
		settingsBaseline: &settings,
		width:            100,
		height:           24,
	}
	updated, _ := m.openSettingsDrilldown(settingsDrilldownProjectReports)
	m = updated.(Model)
	_ = m.setSettingsSelection(settingsFieldOpenAIAPIKey)

	rendered := ansi.Strip(m.renderSettingsContent(72, 18))
	if !strings.Contains(rendered, "Stored key ends with") || !strings.Contains(rendered, "...12345.") {
		t.Fatalf("settings modal should show a masked api key suffix hint: %q", rendered)
	}
	if strings.Contains(rendered, "sk-live-12345") {
		t.Fatalf("settings modal should not show the full api key: %q", rendered)
	}
}

func TestSettingsAPIKeyHintHidesDraftSuffixWhileEditing(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.LCAgentProvider = "xiaomi"
	settings.XiaomiAPIKey = "xm-live-12345"

	m := Model{
		settingsMode:     true,
		settingsFields:   newSettingsFields(settings),
		settingsBaseline: &settings,
		width:            100,
		height:           24,
	}
	updated, _ := m.openSettingsDrilldown(settingsDrilldownLCAgent)
	m = updated.(Model)
	_ = m.setSettingsSelection(settingsFieldXiaomiAPIKey)
	m.settingsFields[settingsFieldXiaomiAPIKey].input.SetValue("xm-live-1234")

	rendered := ansi.Strip(m.renderSettingsContent(72, 18))
	if !strings.Contains(rendered, "Key is being edited") || !strings.Contains(rendered, "hidden until you save.") {
		t.Fatalf("settings modal should say edited keys remain hidden: %q", rendered)
	}
	for _, leaked := range []string{"...1234", "...12345", "xm-live-1234", "xm-live-12345"} {
		if strings.Contains(rendered, leaked) {
			t.Fatalf("settings modal leaked edited api key fragment %q: %q", leaked, rendered)
		}
	}
}

func TestSettingsClearedSelectedAPIKeyRemainsVisible(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.XiaomiAPIKey = "xm-live-12345"

	m := Model{
		settingsMode:            true,
		settingsSectionSelected: settingsSectionIndexByID(settingsSectionLCAgent),
		settingsFields:          newSettingsFields(settings),
		settingsBaseline:        &settings,
		width:                   100,
		height:                  24,
	}
	_ = m.setSettingsSelection(settingsFieldXiaomiAPIKey)
	m.settingsFields[settingsFieldXiaomiAPIKey].input.SetValue("")

	if !m.settingsFieldVisible(settingsFieldXiaomiAPIKey) {
		t.Fatal("cleared selected Xiaomi API key field should remain visible")
	}
	rendered := ansi.Strip(m.renderSettingsContent(72, 18))
	if !strings.Contains(rendered, "Xiaomi API key") || !strings.Contains(rendered, "Key cleared in this draft.") {
		t.Fatalf("settings modal should keep cleared Xiaomi key field visible with replacement hint: %q", rendered)
	}
	if strings.Contains(rendered, "...12345") || strings.Contains(rendered, "xm-live-12345") {
		t.Fatalf("settings modal leaked cleared api key suffix: %q", rendered)
	}
}

func TestSettingsSavedMsgAppliesProjectNameFilterImmediately(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	next := settings
	next.ExcludeProjectPatterns = []string{"client-*"}
	next.CodexLaunchPreset = codexcli.PresetSafe

	m := Model{
		settingsMode:     true,
		settingsBaseline: &settings,
		allProjects: []model.ProjectSummary{
			{Path: "/tmp/client-demo-03", Name: "client-demo-03", LastActivity: time.Date(2026, 3, 6, 9, 0, 0, 0, time.UTC)},
			{Path: "/tmp/visible-demo", Name: "visible-demo", LastActivity: time.Date(2026, 3, 6, 10, 0, 0, 0, time.UTC)},
		},
		sortMode:   sortByAttention,
		visibility: visibilityAllFolders,
	}
	m.rebuildProjectList("")

	updated, cmd := m.Update(settingsSavedMsg{
		path:     "/tmp/config.toml",
		settings: next,
	})
	got := updated.(Model)
	if got.settingsMode {
		t.Fatalf("settings mode should close after settingsSavedMsg")
	}
	if cmd == nil {
		t.Fatalf("settingsSavedMsg should refresh detail for the remaining visible project")
	}
	if len(got.projects) != 1 || got.projects[0].Name != "visible-demo" {
		t.Fatalf("visible projects after settingsSavedMsg = %#v, want only visible-demo", got.projects)
	}
	if !strings.Contains(got.status, "Filters, API keys, local endpoint/model overrides, Codex launch mode, and browser automation policy are applying in the background now") {
		t.Fatalf("status = %q, want immediate-apply notice", got.status)
	}
}

func TestSettingsSavedMsgExplainsMobileRestart(t *testing.T) {
	baseline := config.EditableSettingsFromAppConfig(config.Default())
	next := baseline
	next.MobileListenAddress = "0.0.0.0:8787"
	m := Model{
		settingsMode:     true,
		settingsBaseline: &baseline,
	}

	updated, _ := m.Update(settingsSavedMsg{path: "/tmp/config.toml", settings: next})
	got := updated.(Model)
	if !strings.Contains(got.status, "Restart lcroom to apply the mobile interface change") {
		t.Fatalf("status = %q, want mobile restart notice", got.status)
	}
}

func TestApplyEditableSettingsCmdReturnsCompletionMsg(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	svc := service.New(config.Default(), st, events.NewBus(), nil)
	m := Model{svc: svc}

	cmd := m.applyEditableSettingsCmd(config.EditableSettingsFromAppConfig(config.Default()))
	if cmd == nil {
		t.Fatal("applyEditableSettingsCmd() should return a command when the service is available")
	}
	msg := cmd()
	if _, ok := msg.(editableSettingsAppliedMsg); !ok {
		t.Fatalf("cmd() returned %T, want editableSettingsAppliedMsg", msg)
	}
}

func TestSettingsSavedMsgQueuesScanAfterScopePathChange(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	cfg := config.Default()
	cfg.IncludePaths = []string{"/tmp/old-root"}
	svc := service.New(cfg, st, events.NewBus(), nil)
	baseline := config.EditableSettingsFromAppConfig(cfg)
	next := baseline
	next.IncludePaths = []string{"/tmp/new-root"}

	m := Model{
		svc:              svc,
		settingsMode:     true,
		settingsBaseline: &baseline,
	}

	updated, cmd := m.Update(settingsSavedMsg{
		path:     "/tmp/config.toml",
		settings: next,
	})
	got := updated.(Model)
	if got.scanInFlight {
		t.Fatal("settings save should wait until settings are applied before starting the scan")
	}
	var applied editableSettingsAppliedMsg
	found := false
	for _, msg := range collectCmdMsgs(cmd) {
		if appliedMsg, ok := msg.(editableSettingsAppliedMsg); ok {
			applied = appliedMsg
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("settings save command did not apply settings before scan")
	}
	if !applied.scanAfter {
		t.Fatalf("editableSettingsAppliedMsg.scanAfter = false, want true for include path change")
	}

	updated, scanCmd := got.Update(applied)
	got = updated.(Model)
	if !got.scanInFlight {
		t.Fatal("scope-change settings apply should mark scan in flight")
	}
	msgs := collectCmdMsgs(scanCmd)
	if len(msgs) != 1 {
		t.Fatalf("scan command messages = %#v, want one scanMsg", msgs)
	}
	if _, ok := msgs[0].(scanMsg); !ok {
		t.Fatalf("scan command returned %T, want scanMsg", msgs[0])
	}
}
