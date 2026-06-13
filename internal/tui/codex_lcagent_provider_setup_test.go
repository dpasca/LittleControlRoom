package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lcroom/internal/codexapp"
	"lcroom/internal/config"

	tea "github.com/charmbracelet/bubbletea"
)

func TestLCAgentModelPickerStagesProviderOverrideWhenConfigured(t *testing.T) {
	projectPath := "/tmp/demo-lcagent-provider"
	models := []codexapp.ModelOption{
		{
			ID:            "deepseek-v4-pro",
			Model:         "deepseek-v4-pro",
			ModelProvider: "deepseek",
			DisplayName:   "DeepSeek V4 Pro",
			IsDefault:     true,
		},
		{
			ID:            "gpt-5.5",
			Model:         "gpt-5.5",
			ModelProvider: "openai",
			DisplayName:   "GPT-5.5",
		},
	}
	session := &fakeCodexSession{
		projectPath: projectPath,
		snapshot: codexapp.Snapshot{
			Provider:      codexapp.ProviderLCAgent,
			ProjectPath:   projectPath,
			ThreadID:      "lca-thread",
			Started:       true,
			Status:        "LCAgent ready",
			Model:         "deepseek-v4-pro",
			ModelProvider: "deepseek",
		},
		models: models,
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		Provider:    codexapp.ProviderLCAgent,
		ProjectPath: projectPath,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.OpenAIAPIKey = "sk-openai-test"
	settings.LCAgentProvider = "deepseek"

	m := Model{
		codexManager:        manager,
		codexVisibleProject: projectPath,
		codexHiddenProject:  projectPath,
		settingsBaseline:    &settings,
		width:               100,
		height:              30,
	}
	m.openLoadedCodexModelPicker(models)
	if m.codexModelPicker == nil {
		t.Fatal("model picker did not open")
	}
	m.codexModelPicker.Focus = codexModelPickerFocusModels
	m.codexModelPicker.ModelIndex = 1
	m.setCodexModelPickerModel(models[1], "")

	updated, cmd := m.updateCodexModelPickerMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("enter should stage configured cross-provider model")
	}
	msg := cmd()
	action, ok := msg.(codexActionMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want codexActionMsg", msg)
	}
	if action.err != nil {
		t.Fatalf("provider staging failed: %v", action.err)
	}
	if got.codexLCAgentProviderSetup != nil {
		t.Fatalf("provider setup should not open when OpenAI key is saved")
	}
	if len(session.modelProviderStages) != 1 {
		t.Fatalf("provider stages = %#v, want one", session.modelProviderStages)
	}
	stage := session.modelProviderStages[0]
	if stage.Provider != "openai" || stage.Model != "gpt-5.5" || stage.Reasoning != "" {
		t.Fatalf("provider stage = %#v, want openai/gpt-5.5", stage)
	}
}

func TestLCAgentModelPickerOpensProviderSetupWhenMissingKey(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	home := t.TempDir()
	configPath := filepath.Join(home, ".little-control-room", "config.toml")
	projectPath := "/tmp/demo-lcagent-provider-setup"
	models := []codexapp.ModelOption{
		{
			ID:            "gpt-5.5",
			Model:         "gpt-5.5",
			ModelProvider: "openai",
			DisplayName:   "GPT-5.5",
		},
	}
	session := &fakeCodexSession{
		projectPath: projectPath,
		snapshot: codexapp.Snapshot{
			Provider:      codexapp.ProviderLCAgent,
			ProjectPath:   projectPath,
			ThreadID:      "lca-thread",
			Started:       true,
			Status:        "LCAgent ready",
			Model:         "deepseek-v4-pro",
			ModelProvider: "deepseek",
		},
		models: models,
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		Provider:    codexapp.ProviderLCAgent,
		ProjectPath: projectPath,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.LCAgentProvider = "deepseek"
	settings.OpenAIAPIKey = ""

	m := Model{
		codexManager:        manager,
		codexVisibleProject: projectPath,
		codexHiddenProject:  projectPath,
		settingsBaseline:    &settings,
		settingsConfigPath:  configPath,
		homeDir:             home,
		width:               100,
		height:              30,
	}
	m.openLoadedCodexModelPicker(models)
	m.codexModelPicker.Focus = codexModelPickerFocusModels
	m.codexModelPicker.ModelIndex = 0
	m.setCodexModelPickerModel(models[0], "")

	updated, cmd := m.updateCodexModelPickerMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("missing key should open setup before staging, got cmd %T", cmd)
	}
	if got.codexLCAgentProviderSetup == nil {
		t.Fatalf("provider setup did not open")
	}
	if got.codexLCAgentProviderSetup.Provider != "openai" {
		t.Fatalf("setup provider = %q, want openai", got.codexLCAgentProviderSetup.Provider)
	}
	if len(session.modelProviderStages) != 0 {
		t.Fatalf("model should not stage before setup: %#v", session.modelProviderStages)
	}

	got.codexLCAgentProviderSetup.Fields[got.codexLCAgentProviderSetup.Selected].input.SetValue("sk-openai-test")
	updated, cmd = got.updateCodexLCAgentProviderSetupMode(tea.KeyMsg{Type: tea.KeyCtrlS})
	got = updated.(Model)
	if cmd == nil {
		t.Fatalf("ctrl+s should save provider setup")
	}
	msg := cmd()
	savedMsg, ok := msg.(codexLCAgentProviderSetupSavedMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want codexLCAgentProviderSetupSavedMsg", msg)
	}
	if savedMsg.err != nil {
		t.Fatalf("save setup error = %v", savedMsg.err)
	}
	if savedMsg.settings.OpenAIAPIKey != "sk-openai-test" ||
		savedMsg.settings.LCAgentProvider != "openai" ||
		savedMsg.settings.EmbeddedLCAgentModel != "gpt-5.5" {
		t.Fatalf("saved settings = %#v, want OpenAI provider/model/key", savedMsg.settings)
	}
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if !strings.Contains(string(raw), `openai_api_key = "sk-openai-test"`) ||
		!strings.Contains(string(raw), `lcagent_provider = "openai"`) ||
		!strings.Contains(string(raw), `embedded_lcagent_model = "gpt-5.5"`) {
		t.Fatalf("saved config missing provider setup:\n%s", string(raw))
	}

	updated, cmd = got.Update(savedMsg)
	got = updated.(Model)
	if got.codexLCAgentProviderSetup != nil || got.codexModelPicker != nil {
		t.Fatalf("setup/model picker should close after save")
	}
	if got.currentSettingsBaseline().OpenAIAPIKey != "sk-openai-test" {
		t.Fatalf("baseline OpenAI key = %q, want saved key", got.currentSettingsBaseline().OpenAIAPIKey)
	}
	if cmd == nil {
		t.Fatalf("setup save should return apply/reload command")
	}
}
