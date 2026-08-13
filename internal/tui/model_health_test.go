package tui

import (
	"strings"
	"testing"

	"lcroom/internal/config"
	"lcroom/internal/modelcatalog"

	"github.com/charmbracelet/x/ansi"
)

func TestModelHealthFooterLabelSilentWhenHealthy(t *testing.T) {
	if got := modelHealthFooterLabel(nil); got != "" {
		t.Fatalf("label = %q, want empty for a healthy config", got)
	}
}

func TestModelHealthFooterLabelNamesTheField(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.BossChatBackend = config.AIBackendDeepSeek
	settings.BossHelmModel = modelcatalog.OpenAIUtilityModel

	label := modelHealthFooterLabel(settings.ModelMismatches())
	if !strings.Contains(label, "boss_helm_model") {
		t.Fatalf("label = %q, want it to name the offending field", label)
	}
}

func TestModelHealthFooterLabelCollapsesMultipleFields(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.BossChatBackend = config.AIBackendDeepSeek
	settings.BossHelmModel = modelcatalog.OpenAIUtilityModel
	settings.BossUtilityModel = modelcatalog.OpenAIUtilityModel

	mismatches := settings.ModelMismatches()
	if len(mismatches) != 2 {
		t.Fatalf("mismatches = %d, want 2", len(mismatches))
	}
	label := modelHealthFooterLabel(mismatches)
	if !strings.Contains(label, "2 fields") {
		t.Fatalf("label = %q, want a collapsed count", label)
	}
}

// The footer chip is a pointer; the detail has to carry the actual fix.
func TestModelHealthDetailCarriesTheFix(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.BossChatBackend = config.AIBackendDeepSeek
	settings.BossHelmModel = modelcatalog.OpenAIUtilityModel

	detail := modelHealthDetail(settings.ModelMismatches())
	for _, want := range []string{"boss_helm_model", modelcatalog.DeepSeekProModel, "fix:"} {
		if !strings.Contains(detail, want) {
			t.Errorf("detail missing %q:\n%s", want, detail)
		}
	}
}

// A repaired config must clear the indicator, otherwise it becomes noise that
// gets ignored and the next real mismatch goes unnoticed.
func TestModelHealthClearsAfterRepair(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.BossChatBackend = config.AIBackendDeepSeek
	settings.BossHelmModel = modelcatalog.DeepSeekProModel
	settings.BossUtilityModel = modelcatalog.DeepSeekFlashModel

	if got := modelHealthFooterLabel(settings.ModelMismatches()); got != "" {
		t.Fatalf("label = %q, want empty after repair", got)
	}
}

func TestSettingsRendersActionableModelHealthDetail(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.BossChatBackend = config.AIBackendDeepSeek
	settings.BossHelmModel = modelcatalog.OpenAIUtilityModel
	m := Model{
		settingsMode:     true,
		settingsFields:   newSettingsFields(settings),
		settingsBaseline: &settings,
	}

	rendered := ansi.Strip(m.renderSettingsContent(90, 24))
	for _, want := range []string{"Model health", "boss_helm_model", "accepted:", "fix:"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("settings model health missing %q:\n%s", want, rendered)
		}
	}
}
