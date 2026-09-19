package tui

import (
	"context"
	"lcroom/internal/codexapp"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"lcroom/internal/config"
)

// The Z.ai provider must be visible in /settings, not only in the config file:
// these assertions cover the field rows the user edits, the provider pickers
// that make Z.ai selectable, and the model-picker registration that offers GLM
// models for the Z.ai model field.
func TestSettingsFieldListOffersZaiRows(t *testing.T) {
	settings := config.EditableSettings{
		ZaiBaseURL: "https://api.z.ai/api/coding/paas/v4",
		ZaiAPIKey:  "zai-test-key",
		ZaiModel:   "glm-5.3",
	}
	fields := newSettingsFields(settings)
	byLabel := make(map[string]settingsField, len(fields))
	for _, field := range fields {
		byLabel[field.label] = field
	}
	for _, label := range []string{"Z.ai base URL", "Z.ai API key", "Z.ai project model"} {
		if _, ok := byLabel[label]; !ok {
			t.Fatalf("settings field list is missing %q", label)
		}
	}
	if got := byLabel["Z.ai base URL"].input.Value(); got != "https://api.z.ai/api/coding/paas/v4" {
		t.Fatalf("Z.ai base URL field value = %q", got)
	}
	if got := byLabel["Z.ai API key"].input.Value(); got != "zai-test-key" {
		t.Fatalf("Z.ai API key field value = %q", got)
	}
	if got := byLabel["Z.ai project model"].input.Value(); got != "glm-5.3" {
		t.Fatalf("Z.ai project model field value = %q", got)
	}
	if !byLabel["Z.ai API key"].sensitive {
		t.Fatal("Z.ai API key field should render as a sensitive input")
	}
}

func TestLCAgentProviderPickersOfferZai(t *testing.T) {
	pickers := map[string][]settingsLCAgentProviderOption{
		"main":    settingsLCAgentProviderOptions(),
		"utility": settingsLCAgentUtilityProviderOptions(),
		"vision":  settingsLCAgentVisionProviderOptions(),
	}
	for name, options := range pickers {
		found := false
		for _, option := range options {
			if option.Value != "zai" {
				continue
			}
			found = true
			if option.Label != "Z.ai" {
				t.Fatalf("%s picker Z.ai label = %q, want Z.ai", name, option.Label)
			}
			if option.Summary == "" || option.Description == "" {
				t.Fatalf("%s picker Z.ai option is missing summary/description: %#v", name, option)
			}
		}
		if !found {
			t.Fatalf("%s provider picker does not offer Z.ai", name)
		}
	}
	if got := settingsLCAgentProviderOptionLabel("zai"); got != "Z.ai" {
		t.Fatalf("provider label for zai = %q, want Z.ai", got)
	}
}

func TestZaiModelPickerAndCredentialWiring(t *testing.T) {
	if !settingsFieldUsesProjectCloudModelPicker(settingsFieldZaiModel) {
		t.Fatal("Z.ai project model field should use the cloud model picker")
	}
	if got := settingsProjectCloudModelFieldBackend(settingsFieldZaiModel); got != config.AIBackendZai {
		t.Fatalf("Z.ai model field backend = %q, want zai", got)
	}
	if got := settingsCloudModelProviderForBackend(config.AIBackendZai); got != "zai" {
		t.Fatalf("cloud provider for Z.ai backend = %q, want zai", got)
	}
	if got := settingsCloudModelBackendForProvider("zai"); got != config.AIBackendZai {
		t.Fatalf("cloud backend for zai provider = %q, want zai", got)
	}
	if got := settingsLCAgentModelPickerProviderLabel("zai"); got != "Z.ai" {
		t.Fatalf("model picker provider label for zai = %q, want Z.ai", got)
	}
	if got := lcagentProviderSavedKeyLabel("zai"); got != "Z.ai API key" {
		t.Fatalf("saved key label for zai = %q, want Z.ai API key", got)
	}
	if got := lcagentProviderAPIKeyName("zai"); got != "ZAI_API_KEY" {
		t.Fatalf("env key name for zai = %q, want ZAI_API_KEY", got)
	}
	if got := lcagentDefaultModelForProvider("zai"); got != config.DefaultZaiModel {
		t.Fatalf("default model for zai = %q, want %q", got, config.DefaultZaiModel)
	}
	if got := settingsLCAgentCredentialFieldForProvider("zai"); got != settingsFieldZaiAPIKey {
		t.Fatalf("credential field for zai = %d, want %d", got, settingsFieldZaiAPIKey)
	}
}

func TestZaiSettingsEditsReachDraftAndSave(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.ZaiAPIKey = "old-key"
	settings.ZaiModel = "glm-5.1"
	m := Model{settingsBaseline: &settings, settingsFields: newSettingsFields(settings), settingsConfigPath: filepath.Join(t.TempDir(), "config.toml")}
	for _, key := range []string{"new-key", ""} {
		m.settingsFields[settingsFieldZaiAPIKey].input.SetValue(key)
		m.settingsFields[settingsFieldZaiBaseURL].input.SetValue(config.ZaiCodingPlanBaseURL)
		m.settingsFields[settingsFieldZaiModel].input.SetValue("glm-5.3")
		check := func(got config.EditableSettings) {
			t.Helper()
			if got.ZaiAPIKey != key || got.ZaiBaseURL != config.ZaiCodingPlanBaseURL || got.ZaiModel != "glm-5.3" {
				t.Fatal("Z.ai edits were not retained")
			}
		}
		check(m.settingsDraftForInferenceStatus())
		check(m.setupDraftSettingsForProviderChoices())
		next, cmd := m.saveSettingsFromFields()
		if cmd == nil {
			t.Fatalf("save not scheduled: %s", next.(Model).status)
		}
		msg := cmd().(settingsSavedMsg)
		if msg.err != nil {
			t.Fatal(msg.err)
		}
		check(msg.settings)
	}
}

func TestZaiProjectModelSelectionUpdatesField(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	m := Model{settingsBaseline: &settings, settingsFields: newSettingsFields(settings), settingsLCAgentModelPicker: &settingsLCAgentModelPickerState{FieldIndex: settingsFieldZaiModel}}
	for _, model := range []string{"glm-5.3", ""} {
		next, _ := m.applySettingsProjectCloudModelPickerSelection("zai", model)
		m = next.(Model)
		if got := m.settingsFieldValue(settingsFieldZaiModel); got != model {
			t.Fatalf("model = %q, want %q", got, model)
		}
		if got := m.settingsFieldValue(settingsFieldAIBackend); got != "zai" {
			t.Fatalf("backend = %q", got)
		}
	}
	options := settingsLCAgentModelPickerProviderOptions(settingsFieldZaiModel)
	found := false
	for _, option := range options {
		if option.Value == "zai" {
			found = true
		}
		if option.Value == "openai" {
			t.Fatal("project picker includes unsupported direct OpenAI model field")
		}
	}
	if !found {
		t.Fatal("project picker omits Z.ai")
	}
}

func TestZaiSettingsModelDiscoveryUsesEditedConnection(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Header.Get("Authorization") != "Bearer edited-key" {
			t.Error("model discovery lost the edited key")
		}
		if r.URL.Path != "/models" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"glm-future-release"}]}`))
	}))
	defer server.Close()
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.LCAgentRoutePreset = ""
	settings.LCAgentProvider = "zai"
	m := Model{settingsBaseline: &settings, settingsFields: newSettingsFields(settings)}
	m.setSettingsModelPickerAPIKey("zai", "edited-key")
	m.setSettingsModelPickerBaseURL("zai", server.URL)
	for _, field := range []int{settingsFieldLCAgentModel, settingsFieldZaiModel, settingsFieldBossChatModel} {
		cfg, _, _, ok := settingsLCAgentModelListConfigForProvider(m.settingsDraftForInferenceStatus(), field, "zai")
		if !ok {
			t.Fatal("model discovery unavailable")
		}
		models, err := codexapp.LCAgentModelOptions(context.Background(), cfg)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, model := range models {
			if model.Model == "glm-future-release" {
				found = true
			}
		}
		if !found {
			t.Fatal("live model missing")
		}
	}
	if requests != 3 {
		t.Fatalf("requests = %d, want 3", requests)
	}
}
