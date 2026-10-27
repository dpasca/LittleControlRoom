package config

import (
	"path/filepath"
	"testing"
)

func TestSingleModelSetupRoundTripsWithoutDiscardingCustomRoutes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	settings := EditableSettingsFromAppConfig(Default())
	settings.LCAgentRoutePreset = LCAgentSingleModel
	settings.LCAgentProvider, settings.EmbeddedLCAgentModel = "deepseek", "deepseek-flash"
	settings.LCAgentUtilityProvider, settings.LCAgentUtilityModel = "zai", "glm-5.3-flash"
	settings.LCAgentVisionProvider, settings.LCAgentVisionModel = "zai", "glm-5.3-flash"
	if err := SaveEditableSettings(path, settings); err != nil {
		t.Fatal(err)
	}
	cfg, err := Parse("scan", []string{"--config", path})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LCAgentRoutePreset != LCAgentSingleModel || cfg.EmbeddedLCAgentModel != "deepseek-flash" || cfg.LCAgentProvider != "deepseek" {
		t.Fatalf("single-model selection did not survive config reload: %s %s/%s", cfg.LCAgentRoutePreset, cfg.LCAgentProvider, cfg.EmbeddedLCAgentModel)
	}
	if cfg.LCAgentVisionModel != "glm-5.3-flash" || cfg.LCAgentUtilityModel != "glm-5.3-flash" {
		t.Fatal("custom routes were discarded instead of retained for Custom Routing")
	}
}
