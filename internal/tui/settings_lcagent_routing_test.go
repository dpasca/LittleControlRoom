package tui

import (
	"slices"
	"strings"
	"testing"

	"lcroom/internal/codexapp"
	"lcroom/internal/config"

	"github.com/charmbracelet/x/ansi"
)

func TestSingleModelSetupExposesMainAndHidesSecondaryRoutes(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.LCAgentRoutePreset = config.LCAgentSingleModel
	settings.LCAgentProvider, settings.EmbeddedLCAgentModel = "deepseek", "deepseek-flash"
	settings.LCAgentUtilityProvider, settings.LCAgentUtilityModel = "zai", "glm-5.3-flash"
	settings.LCAgentVisionProvider, settings.LCAgentVisionModel = "zai", "glm-5.3-flash"
	m := Model{settingsMode: true, settingsFields: newSettingsFields(settings), settingsBaseline: &settings, width: 110, height: 32}
	for _, fields := range [][]int{
		m.visibleSettingsDrilldownFieldOrder(settingsDrilldownLCAgent),
		func() []int { m.setupFocusedRole = setupRoleLCAgent; return m.setupConfigFieldIndexes() }(),
	} {
		if !slices.Contains(fields, settingsFieldLCAgentModel) {
			t.Fatal("single-model setup hid the model picker")
		}
		for _, hidden := range []int{settingsFieldLCAgentUtilityModel, settingsFieldLCAgentVisionModel, settingsFieldZaiAPIKey} {
			if slices.Contains(fields, hidden) {
				t.Fatalf("single-model setup exposed inactive role/credential %d", hidden)
			}
		}
	}
	updated, _ := m.openSettingsDrilldown(settingsDrilldownLCAgent)
	view := ansi.Strip(updated.(Model).renderSettingsContent(100, 30))
	for _, want := range []string{"One Model for Everything", "Main model", "deepseek-flash"} {
		if !strings.Contains(view, want) {
			t.Fatalf("missing %q in setup:\n%s", want, view)
		}
	}
	for _, old := range []string{"glm-5.3-flash", "Vision fallback model", "Utility model"} {
		if strings.Contains(view, old) {
			t.Fatalf("inactive secondary model shown: %s", view)
		}
	}
	if provider := settingsLCAgentVisionProviderForLaunch(settings); provider != "main" {
		t.Fatalf("vision = %s", provider)
	}
}

func TestModelPickerKeepsSingleModelPolicy(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.LCAgentRoutePreset = config.LCAgentSingleModel
	m := Model{
		settingsFields: newSettingsFields(settings), settingsBaseline: &settings,
		settingsLCAgentModelPicker: &settingsLCAgentModelPickerState{
			FieldIndex: settingsFieldLCAgentModel, Provider: "deepseek", PendingModel: "deepseek-flash", PendingReasoning: "max",
		},
	}
	updated, _ := m.applySettingsLCAgentModelPickerSelection()
	got := updated.(Model)
	if got.settingsFieldValue(settingsFieldLCAgentRoutePreset) != config.LCAgentSingleModel {
		t.Fatal("model choice cleared single-model setup")
	}
	if got.settingsFieldValue(settingsFieldLCAgentModel) != "deepseek-flash" {
		t.Fatal("model choice was not applied")
	}
	prefs := map[codexapp.Provider]embeddedModelPreference{codexapp.ProviderLCAgent: {ModelProvider: "deepseek", Model: "deepseek-flash"}}
	applyEmbeddedModelPreferencesToSettings(&settings, prefs)
	if settings.LCAgentRoutePreset != config.LCAgentSingleModel {
		t.Fatal("remembered model cleared single-model setup")
	}
	if remembered := embeddedModelPreferencesFromSettings(settings)[codexapp.ProviderLCAgent]; remembered.Model != "deepseek-flash" {
		t.Fatal("single-model setup did not retain its selected default for fresh launches")
	}
	m.settingsBaseline = &settings
	req := m.enrichEmbeddedLaunchRequestBase(codexapp.LaunchRequest{Provider: codexapp.ProviderLCAgent, PendingModel: "tomorrows-model", LCAgentProvider: "ollama"})
	if req.LCAgentRoutePreset != config.LCAgentSingleModel || req.PendingModel != "tomorrows-model" {
		t.Fatal("explicit handoff model lost the configured single-model policy")
	}
}

func TestNativeVisionPrecedesExplicitFallbackInSettings(t *testing.T) {
	settings := config.EditableSettings{LCAgentProvider: "deepseek", EmbeddedLCAgentModel: "deepseek-flash", LCAgentVisionProvider: "zai", LCAgentVisionModel: "glm-5.3-flash"}
	if p, model := settingsLCAgentVisionRoute(settings); p != "main" || model != "" {
		t.Fatalf("native route = %s/%s", p, model)
	}
	settings.LCAgentVisionProvider = "off"
	if p := settingsLCAgentVisionProviderForLaunch(settings); p != "off" {
		t.Fatalf("explicit off ignored: %s", p)
	}
}
