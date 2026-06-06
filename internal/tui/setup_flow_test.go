package tui

import (
	"context"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"lcroom/internal/aibackend"
	"lcroom/internal/brand"
	"lcroom/internal/config"
	"lcroom/internal/model"
	"lcroom/internal/store"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStartupUnconfiguredAIBackendOpensGettingStartedSettings(t *testing.T) {
	m := Model{
		width:  100,
		height: 24,
	}

	updated, cmd := m.Update(setupSnapshotMsg{
		openOnStartup: true,
		snapshot: aibackend.Snapshot{
			Selected: config.AIBackendUnset,
		},
	})
	got := updated.(Model)
	if got.setupMode {
		t.Fatalf("startup setup should not open the retired setup wizard")
	}
	if !got.settingsMode {
		t.Fatalf("startup setup should open settings mode")
	}
	if got.settingsSectionMenu {
		t.Fatalf("startup setup should open the Getting Started guide directly")
	}
	if got.activeSettingsSection().id != settingsSectionGettingStarted {
		t.Fatalf("active settings section = %q, want Getting Started", got.activeSettingsSection().id)
	}
	if got.settingsSelected != settingsFieldAIBackend {
		t.Fatalf("settingsSelected = %d, want project reports field", got.settingsSelected)
	}
	if got.status != "Setup open in Getting Started. Choose a row, press Enter to configure, or ctrl+s to save." {
		t.Fatalf("status = %q, want Getting Started setup explanation", got.status)
	}
	if cmd == nil {
		t.Fatalf("opening setup should focus the Getting Started selection")
	}
}

func TestStartupSetupSnapshotCmdSkippedWhenBackendConfigured(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.AIBackend = config.AIBackendCodex

	m := Model{settingsBaseline: &settings}
	if cmd := m.startupSetupSnapshotCmd(); cmd != nil {
		t.Fatalf("startupSetupSnapshotCmd() should skip configured backends")
	}
}

func TestStartupSetupSnapshotCmdRunsWhenBackendUnset(t *testing.T) {
	m := Model{}
	if cmd := m.startupSetupSnapshotCmd(); cmd == nil {
		t.Fatalf("startupSetupSnapshotCmd() should run when no backend is configured")
	}
}

func TestSetupDetectionConfigIncludesCloudProviderSettings(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.AIBackend = config.AIBackendDeepSeek
	settings.BossChatBackend = config.AIBackendDeepSeek
	settings.BossHelmModel = "deepseek-v4-pro"
	settings.BossUtilityModel = "deepseek-v4-flash"
	settings.OpenAIAPIKey = "openai-key"
	settings.OpenRouterAPIKey = "openrouter-key"
	settings.DeepSeekAPIKey = "deepseek-key"
	settings.MoonshotAPIKey = "moonshot-key"

	cfg := setupDetectionConfig(settings)
	if cfg.AIBackend != config.AIBackendDeepSeek {
		t.Fatalf("AIBackend = %s, want %s", cfg.AIBackend, config.AIBackendDeepSeek)
	}
	if cfg.BossChatBackend != config.AIBackendDeepSeek {
		t.Fatalf("BossChatBackend = %s, want %s", cfg.BossChatBackend, config.AIBackendDeepSeek)
	}
	if cfg.BossHelmModel != "deepseek-v4-pro" || cfg.BossUtilityModel != "deepseek-v4-flash" {
		t.Fatalf("boss models = %q/%q, want DeepSeek models", cfg.BossHelmModel, cfg.BossUtilityModel)
	}
	if cfg.OpenAIAPIKey != "openai-key" || cfg.OpenRouterAPIKey != "openrouter-key" || cfg.DeepSeekAPIKey != "deepseek-key" || cfg.MoonshotAPIKey != "moonshot-key" {
		t.Fatalf("cloud provider keys were not copied into setup detection config")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := aibackend.DetectStatus(ctx, cfg, config.AIBackendDeepSeek); !got.Ready {
		t.Fatalf("DeepSeek status should be ready with saved key: %#v", got)
	}
}

func TestOpenSetupModePrefersReadyBackendOverUnavailableCurrentBackend(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.AIBackend = config.AIBackendOpenAIAPI

	m := Model{
		settingsBaseline: &settings,
		setupSnapshot: aibackend.Snapshot{
			Selected: config.AIBackendOpenAIAPI,
			OpenAIAPI: aibackend.Status{
				Backend: config.AIBackendOpenAIAPI,
				Label:   "OpenAI API key",
				Detail:  "No saved OpenAI API key.",
			},
			Codex: aibackend.Status{
				Backend:       config.AIBackendCodex,
				Label:         "Codex",
				Installed:     true,
				Authenticated: true,
				Ready:         true,
				Detail:        "Logged in with ChatGPT.",
			},
		},
	}

	_ = m.openSetupMode()
	if got := m.setupSelectedBackend(); got != config.AIBackendCodex {
		t.Fatalf("setupSelectedBackend() = %s, want %s", got, config.AIBackendCodex)
	}
}

func TestRenderSetupProviderRowsUseSharedProviderChoiceRows(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.AIBackend = config.AIBackendOpenAIAPI

	m := Model{
		settingsBaseline: &settings,
		setupSnapshot: aibackend.Snapshot{
			Selected: config.AIBackendOpenAIAPI,
			OpenAIAPI: aibackend.Status{
				Backend: config.AIBackendOpenAIAPI,
				Label:   "OpenAI API key",
				Detail:  "No saved OpenAI API key.",
			},
			Codex: aibackend.Status{
				Backend:       config.AIBackendCodex,
				Label:         "Codex",
				Installed:     true,
				Authenticated: true,
				Ready:         true,
				Detail:        "Logged in with ChatGPT.",
			},
		},
	}

	rows := ansi.Strip(strings.Join(m.renderSetupProviderRows(88), "\n"))
	if !strings.Contains(rows, "OpenAI API") || !strings.Contains(rows, "(current)") {
		t.Fatalf("current backend row should come from shared provider choice rows, got %q", rows)
	}
	if !strings.Contains(rows, "Codex") || !strings.Contains(rows, "ready") {
		t.Fatalf("ready backend row should say ready, got %q", rows)
	}
	if strings.Contains(rows, "active") {
		t.Fatalf("provider rows should use shared current marker instead of setup-only active state, got %q", rows)
	}
}

func TestProviderChoiceStatusShowsLocalModelSelection(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.AIBackend = config.AIBackendMLX
	settings.MLXModel = "mlx-community/Qwen3.5-9B-MLX-4bit"

	m := Model{
		settingsBaseline: &settings,
		setupSnapshot: aibackend.Snapshot{
			Selected: config.AIBackendMLX,
			MLX: aibackend.Status{
				Backend:     config.AIBackendMLX,
				Label:       "MLX",
				Ready:       true,
				Endpoint:    "http://127.0.0.1:8080/v1",
				Models:      []string{"mlx-community/Qwen3.5-9B-MLX-4bit", "mlx-community/Qwen3.5-4B-MLX-4bit"},
				ActiveModel: "mlx-community/Qwen3.5-9B-MLX-4bit",
			},
		},
	}

	choices := m.providerChoices(providerChoiceRoleProjectReports, settings)
	choice := choices[providerChoiceSelection(choices, config.AIBackendMLX)]
	status := ansi.Strip(renderProviderChoiceStatus(choice))
	if !strings.Contains(status, "using mlx-community/Qwen3.5-9B-MLX-4bit") {
		t.Fatalf("provider choice status = %q, want configured local model detail", status)
	}
}

func TestOpenSetupModeCanPreferReadyClaudeBackend(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.AIBackend = config.AIBackendOpenAIAPI

	m := Model{
		settingsBaseline: &settings,
		setupSnapshot: aibackend.Snapshot{
			Selected: config.AIBackendOpenAIAPI,
			OpenAIAPI: aibackend.Status{
				Backend: config.AIBackendOpenAIAPI,
				Label:   "OpenAI API key",
				Detail:  "No saved OpenAI API key.",
			},
			Claude: aibackend.Status{
				Backend:       config.AIBackendClaude,
				Label:         "Claude Code",
				Installed:     true,
				Authenticated: true,
				Ready:         true,
				Detail:        "Claude Code ready via claude.ai (max)",
			},
		},
	}

	_ = m.openSetupMode()
	if got := m.setupSelectedBackend(); got != config.AIBackendClaude {
		t.Fatalf("setupSelectedBackend() = %s, want %s", got, config.AIBackendClaude)
	}
}

func TestSetupLoadingBlocksRepeatRefresh(t *testing.T) {
	m := Model{
		setupMode:    true,
		setupLoading: true,
		status:       "Refreshing AI backend checks...",
	}

	updated, cmd := m.updateSetupMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("setup refresh should not queue another command while loading")
	}
	if !got.setupLoading {
		t.Fatalf("setup loading should remain true while the existing refresh is in flight")
	}
	if got.status != "Refreshing AI backend checks..." {
		t.Fatalf("status = %q, want existing refresh status", got.status)
	}
}

func TestSetupEnterSelectsSaveStepThenEnterSaves(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	m := Model{
		setupMode:         true,
		setupStep:         setupStepProjectProvider,
		setupSelected:     mSetupSelectionForTest(config.AIBackendDisabled),
		setupBossSelected: mSetupBossSelectionForTest(config.AIBackendDisabled),
		setupSnapshot: aibackend.Snapshot{
			Selected: config.AIBackendDisabled,
		},
	}

	updated, cmd := m.updateSetupMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("project provider enter should not save")
	}
	if got.setupStep != setupStepBossProvider || got.setupFocusedRole != setupRoleBossChat {
		t.Fatalf("project provider enter should advance to boss provider, got step=%v role=%v", got.setupStep, got.setupFocusedRole)
	}
	if got.setupSaving {
		t.Fatalf("setup enter should not mark saving before save confirmation")
	}

	updated, cmd = got.updateSetupMode(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if cmd != nil {
		t.Fatalf("boss provider enter should not save before the save step")
	}
	if got.setupReviewMode || got.setupStep != setupStepLCAgentConfig || !got.setupConfigMode {
		t.Fatalf("boss provider enter should open the LCAgent setup step, got step=%v review=%v config=%v", got.setupStep, got.setupReviewMode, got.setupConfigMode)
	}

	updated, cmd = got.updateSetupMode(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if cmd != nil {
		t.Fatalf("LCAgent setup enter should not save before the save step")
	}
	if !got.setupReviewMode || got.setupStep != setupStepSave {
		t.Fatalf("LCAgent setup enter should open the save step, got step=%v review=%v", got.setupStep, got.setupReviewMode)
	}

	updated, cmd = got.updateSetupMode(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if cmd == nil {
		t.Fatalf("save step enter should queue a save command")
	}
	if !got.setupSaving {
		t.Fatalf("save step enter should mark saving in progress")
	}
	if got.setupReviewMode {
		t.Fatalf("save step should close once saving starts")
	}
	if got.status != "Saving AI setup..." {
		t.Fatalf("status = %q, want saving message", got.status)
	}
}

func TestRenderSetupSaveStepShowsFinalChoices(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.AIBackend = config.AIBackendOpenCode
	settings.BossChatBackend = config.AIBackendOpenAIAPI
	settings.OpenAIAPIKey = "sk-review-test"
	settings.OpenCodeModelTier = string(config.ModelTierCheap)

	m := Model{
		setupMode:         true,
		setupReviewMode:   true,
		setupStep:         setupStepSave,
		settingsBaseline:  &settings,
		setupFocusedRole:  setupRoleProjectReports,
		setupSelected:     mSetupSelectionForTest(config.AIBackendOpenCode),
		setupBossSelected: mSetupBossSelectionForTest(config.AIBackendOpenAIAPI),
		setupModelTier:    config.ModelTierCheap,
		setupSnapshot: aibackend.Snapshot{
			OpenCode: aibackend.Status{
				Backend:       config.AIBackendOpenCode,
				Label:         "OpenCode",
				Installed:     true,
				Authenticated: true,
				Ready:         true,
			},
			OpenAIAPI: aibackend.Status{
				Backend:       config.AIBackendOpenAIAPI,
				Label:         "OpenAI API key",
				Authenticated: true,
				Ready:         true,
			},
		},
	}

	rendered := ansi.Strip(m.renderSetupContent(96, 24))
	for _, want := range []string{
		"Save Setup",
		"Project reports",
		"OpenCode",
		"Boss chat",
		"OpenAI API",
		"OpenAI key",
		"OpenCode tier",
		"cheap",
		"Enter saves setup. Esc goes back to the previous page.",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("setup save step missing %q:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, "Ready to Save") {
		t.Fatalf("setup save step should avoid ambiguous ready-to-save wording:\n%s", rendered)
	}
}

func TestRenderSetupContentKeepsActionLegendWhileLoading(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.AIBackend = config.AIBackendOpenCode
	settings.BossChatBackend = config.AIBackendOpenAIAPI
	settings.OpenAIAPIKey = "sk-loading-legend"

	m := Model{
		setupMode:         true,
		setupLoading:      true,
		settingsBaseline:  &settings,
		setupFocusedRole:  setupRoleProjectReports,
		setupSelected:     mSetupSelectionForTest(config.AIBackendOpenCode),
		setupBossSelected: mSetupBossSelectionForTest(config.AIBackendOpenAIAPI),
		setupModelTier:    config.ModelTierCheap,
	}

	rendered := ansi.Strip(m.renderSetupContent(72, 20))
	for _, want := range []string{
		"Setup Wizard",
		"Checking local backend availability",
		"Enter",
		"next",
		"Up/Down",
		"provider",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("setup loading screen missing %q:\n%s", want, rendered)
		}
	}
}

func TestRenderSetupSelectedRoleCardPulsesSlowly(t *testing.T) {
	prevProfile := lipgloss.ColorProfile()
	prevDarkBackground := lipgloss.HasDarkBackground()
	lipgloss.SetColorProfile(termenv.TrueColor)
	lipgloss.SetHasDarkBackground(true)
	t.Cleanup(func() {
		lipgloss.SetColorProfile(prevProfile)
		lipgloss.SetHasDarkBackground(prevDarkBackground)
	})

	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.AIBackend = config.AIBackendOpenCode
	settings.BossChatBackend = config.AIBackendOpenAIAPI
	settings.OpenAIAPIKey = "sk-pulse-test"

	m := Model{
		setupMode:         true,
		settingsBaseline:  &settings,
		setupFocusedRole:  setupRoleProjectReports,
		setupSelected:     mSetupSelectionForTest(config.AIBackendOpenCode),
		setupBossSelected: mSetupBossSelectionForTest(config.AIBackendOpenAIAPI),
	}

	m.spinnerFrame = 0
	first := m.renderSetupRoleCards(88)
	m.spinnerFrame = 16
	second := m.renderSetupRoleCards(88)

	if ansi.Strip(first) != ansi.Strip(second) {
		t.Fatalf("selected setup card pulse should preserve visible text:\n%s\n---\n%s", ansi.Strip(first), ansi.Strip(second))
	}
	if first == second {
		t.Fatalf("selected setup card should animate border styling across slow pulse frames")
	}
	if !strings.Contains(first, "38;2;") || !strings.Contains(second, "38;2;") {
		t.Fatalf("selected setup card pulse should use 24-bit RGB color escapes")
	}
}

func TestSetupFieldsStayOnDetailsPageUntilEnter(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.AIBackend = config.AIBackendCodex
	settings.MLXBaseURL = "http://127.0.0.1:8080/v1"
	settings.MLXModel = "local-test-model"

	m := Model{
		setupMode:        true,
		setupStep:        setupStepProjectProvider,
		settingsBaseline: &settings,
		settingsFields:   newSettingsFields(settings),
		setupFocusedRole: setupRoleProjectReports,
		setupSelected:    mSetupSelectionForTest(config.AIBackendMLX),
	}

	rendered := ansi.Strip(m.renderSetupPanel(96, 28))
	for _, unwanted := range []string{
		"Configure Project Reports",
		"MLX base URL",
		"local-test-model",
		"Press e",
	} {
		if strings.Contains(rendered, unwanted) {
			t.Fatalf("setup wizard should keep field editor hidden before Enter; found %q:\n%s", unwanted, rendered)
		}
	}

	updated, cmd := m.updateSetupMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("entering a configurable setup choice should focus the details field")
	}
	if !got.setupConfigMode {
		t.Fatalf("entering a configurable setup choice should open setup details mode")
	}
	if got.setupStep != setupStepProjectConfig {
		t.Fatalf("setup step = %v, want project config", got.setupStep)
	}
	rendered = ansi.Strip(got.renderSetupPanel(104, 34))
	for _, want := range []string{
		"Configure Project Reports",
		"MLX base URL",
		"local-test-model",
		"Enter",
		"continue",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("setup details page missing %q:\n%s", want, rendered)
		}
	}
}

func TestSetupEnterAdvancesToBossChatRole(t *testing.T) {
	m := Model{
		setupMode:         true,
		setupStep:         setupStepProjectProvider,
		setupFocusedRole:  setupRoleProjectReports,
		setupSelected:     mSetupSelectionForTest(config.AIBackendDisabled),
		setupBossSelected: 0,
	}

	updated, cmd := m.updateSetupMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("advancing setup roles should not queue a command")
	}
	if got.setupFocusedRole != setupRoleBossChat || got.setupStep != setupStepBossProvider {
		t.Fatalf("setup step=%v role=%v, want boss provider", got.setupStep, got.setupFocusedRole)
	}

	updated, _ = got.updateSetupMode(tea.KeyMsg{Type: tea.KeyDown})
	got = updated.(Model)
	if got.setupSelectedBossBackend() != config.AIBackendOpenAIAPI {
		t.Fatalf("boss chat selected backend = %s, want openai_api", got.setupSelectedBossBackend())
	}
}

func TestSetupBossChatDisabledSavesSeparately(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.AIBackend = config.AIBackendCodex

	m := Model{
		setupMode:         true,
		setupStep:         setupStepBossProvider,
		settingsBaseline:  &settings,
		setupFocusedRole:  setupRoleBossChat,
		setupBossSelected: mSetupBossSelectionForTest(config.AIBackendDisabled),
	}

	updated, cmd := m.updateSetupMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("selecting disabled boss chat should not save before the save step")
	}
	if got.setupReviewMode || got.setupStep != setupStepLCAgentConfig {
		t.Fatalf("selecting disabled boss chat should open the LCAgent setup step, got step=%v review=%v", got.setupStep, got.setupReviewMode)
	}
	if got.currentSettingsBaseline().AIBackend != config.AIBackendCodex {
		t.Fatalf("boss chat selection should not change project reports backend")
	}
}

func TestSetupOpenAIKeyOpensDetailsPageInsteadOfSettings(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.AIBackend = config.AIBackendCodex
	settings.OpenAIAPIKey = ""

	m := Model{
		setupMode:        true,
		settingsBaseline: &settings,
		settingsFields:   newSettingsFields(settings),
		setupFocusedRole: setupRoleProjectReports,
		setupSelected:    mSetupSelectionForTest(config.AIBackendOpenAIAPI),
	}

	updated, cmd := m.updateSetupMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("entering OpenAI setup should focus the config field")
	}
	if !got.setupConfigMode {
		t.Fatalf("OpenAI setup should enter setup config mode")
	}
	if got.settingsMode {
		t.Fatalf("OpenAI setup should not open /settings")
	}
	if got.setupSelectedConfigFieldIndex() != settingsFieldOpenAIAPIKey {
		t.Fatalf("focused setup field = %d, want OpenAI API key", got.setupSelectedConfigFieldIndex())
	}
}

func TestSetupDetailsPageSaveStepUsesEditedFields(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.AIBackend = config.AIBackendCodex
	settings.LCAgentProvider = "xiaomi"
	xiaomiBaseURL := "https://token-plan-sgp.xiaomimimo.com/v1"

	m := Model{
		setupMode:           true,
		setupConfigMode:     true,
		setupStep:           setupStepBossConfig,
		settingsBaseline:    &settings,
		settingsConfigPath:  filepath.Join(home, ".little-control-room", "config.toml"),
		settingsFields:      newSettingsFields(settings),
		setupFocusedRole:    setupRoleBossChat,
		setupBossSelected:   mSetupBossSelectionForTest(config.AIBackendOpenAIAPI),
		setupConfigSelected: 0,
	}
	m.settingsFields[settingsFieldOpenAIAPIKey].input.SetValue("sk-inline-test")

	updated, cmd := m.updateSetupMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("continuing from setup config fields should focus the LCAgent setup field")
	}
	if got.setupReviewMode || got.setupStep != setupStepLCAgentConfig {
		t.Fatalf("continuing from boss config fields should open LCAgent setup first, got step=%v review=%v", got.setupStep, got.setupReviewMode)
	}
	if got.setupSaving {
		t.Fatalf("setup config fields should not save until save confirmation")
	}
	got.settingsFields[settingsFieldXiaomiBaseURL].input.SetValue(xiaomiBaseURL)

	updated, cmd = got.updateSetupMode(tea.KeyMsg{Type: tea.KeyCtrlS})
	got = updated.(Model)
	if cmd != nil {
		t.Fatalf("continuing from LCAgent setup should open the save step before save")
	}
	if !got.setupReviewMode {
		t.Fatalf("continuing from LCAgent setup should open the save step")
	}

	updated, cmd = got.updateSetupMode(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if cmd == nil {
		t.Fatalf("confirming setup config fields should queue a save command")
	}
	if !got.setupSaving {
		t.Fatalf("confirming setup config fields should mark setup saving")
	}
	msg := cmd()
	saved, ok := msg.(setupSavedMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want setupSavedMsg", msg)
	}
	if saved.err != nil {
		t.Fatalf("setup save returned error: %v", saved.err)
	}
	if saved.settings.OpenAIAPIKey != "sk-inline-test" {
		t.Fatalf("saved OpenAI key = %q, want edited field", saved.settings.OpenAIAPIKey)
	}
	if saved.settings.BossChatBackend != config.AIBackendOpenAIAPI {
		t.Fatalf("saved boss backend = %s, want openai_api", saved.settings.BossChatBackend)
	}
	if saved.settings.XiaomiBaseURL != xiaomiBaseURL {
		t.Fatalf("saved Xiaomi base URL = %q, want %q", saved.settings.XiaomiBaseURL, xiaomiBaseURL)
	}
	rawConfig, err := os.ReadFile(saved.path)
	if err != nil {
		t.Fatalf("read saved config: %v", err)
	}
	if !strings.Contains(string(rawConfig), `xiaomi_base_url = "`+xiaomiBaseURL+`"`) {
		t.Fatalf("saved config missing Xiaomi base URL: %s", rawConfig)
	}

	updated, _ = got.Update(saved)
	got = updated.(Model)
	if got.xiaomiBaseURL() != xiaomiBaseURL {
		t.Fatalf("model Xiaomi base URL after setupSavedMsg = %q, want %q", got.xiaomiBaseURL(), xiaomiBaseURL)
	}
}

func TestSetupSectionCtrlSSavesLCAgentXiaomiBaseURL(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.AIBackend = config.AIBackendDeepSeek
	settings.BossChatBackend = config.AIBackendDeepSeek
	settings.LCAgentProvider = "xiaomi"
	xiaomiBaseURL := "https://token-plan-sgp.xiaomimimo.com/v1"

	m := Model{
		setupMode:              true,
		setupSectionNavigation: true,
		setupConfigMode:        true,
		setupStep:              setupStepLCAgentConfig,
		setupFocusedRole:       setupRoleLCAgent,
		setupSelected:          mSetupSelectionForTest(config.AIBackendDeepSeek),
		setupBossSelected:      mSetupBossSelectionForTest(config.AIBackendDeepSeek),
		settingsBaseline:       &settings,
		settingsConfigPath:     filepath.Join(home, ".little-control-room", "config.toml"),
		settingsFields:         newSettingsFields(settings),
		width:                  120,
		height:                 30,
	}
	m.settingsFields[settingsFieldXiaomiBaseURL].input.SetValue(xiaomiBaseURL)

	rendered := ansi.Strip(m.renderSetupConfigContent(110))
	if !strings.Contains(rendered, "ctrl+s") || !strings.Contains(rendered, "save") {
		t.Fatalf("setup section should advertise ctrl+s save action: %q", rendered)
	}

	updated, cmd := m.updateSetupMode(tea.KeyMsg{Type: tea.KeyCtrlS})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("setup section ctrl+s should queue a save command")
	}
	if !got.setupSaving {
		t.Fatalf("setup section ctrl+s should mark setup saving")
	}
	if got.status != "Saving AI setup..." {
		t.Fatalf("status = %q, want saving message", got.status)
	}

	msg := cmd()
	saved, ok := msg.(setupSavedMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want setupSavedMsg", msg)
	}
	if saved.err != nil {
		t.Fatalf("setup section save returned error: %v", saved.err)
	}
	if saved.settings.AIBackend != config.AIBackendDeepSeek {
		t.Fatalf("saved project reports backend = %s, want deepseek", saved.settings.AIBackend)
	}
	if saved.settings.BossChatBackend != config.AIBackendDeepSeek {
		t.Fatalf("saved boss chat backend = %s, want deepseek", saved.settings.BossChatBackend)
	}
	if saved.settings.XiaomiBaseURL != xiaomiBaseURL {
		t.Fatalf("saved Xiaomi base URL = %q, want %q", saved.settings.XiaomiBaseURL, xiaomiBaseURL)
	}
	rawConfig, err := os.ReadFile(saved.path)
	if err != nil {
		t.Fatalf("read saved config: %v", err)
	}
	if !strings.Contains(string(rawConfig), `xiaomi_base_url = "`+xiaomiBaseURL+`"`) {
		t.Fatalf("saved config missing Xiaomi base URL: %s", rawConfig)
	}

	updated, _ = got.Update(saved)
	got = updated.(Model)
	if got.xiaomiBaseURL() != xiaomiBaseURL {
		t.Fatalf("model Xiaomi base URL after setup section save = %q, want %q", got.xiaomiBaseURL(), xiaomiBaseURL)
	}
}

func TestSetupBossDeepSeekModelDefaultsUseSelectedBackend(t *testing.T) {
	t.Setenv(brand.BossAssistantModelEnvVar, "")
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.BossChatBackend = config.AIBackendUnset

	m := Model{
		setupMode:         true,
		setupConfigMode:   true,
		setupStep:         setupStepBossConfig,
		setupFocusedRole:  setupRoleBossChat,
		setupBossSelected: mSetupBossSelectionForTest(config.AIBackendDeepSeek),
		settingsBaseline:  &settings,
		settingsFields:    newSettingsFields(settings),
		width:             120,
		height:            30,
	}

	rendered := ansi.Strip(m.renderSetupConfigContent(110))
	for _, want := range []string{
		"Default: " + config.DefaultDeepSeekProModel + " from DeepSeek",
		"Default: " + config.DefaultDeepSeekModel + " from DeepSeek",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("DeepSeek boss setup missing %q: %q", want, rendered)
		}
	}
	for _, unwanted := range []string{
		"Default: " + config.DefaultBossHelmModel,
		"Default: " + config.DefaultBossUtilityModel,
	} {
		if strings.Contains(rendered, unwanted) {
			t.Fatalf("DeepSeek boss setup should not show %q: %q", unwanted, rendered)
		}
	}
}

func TestRenderSetupHintExplainsClaudeHaikuDefault(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.AIBackend = config.AIBackendClaude

	m := Model{
		settingsBaseline: &settings,
		setupSelected:    mSetupSelectionForTest(config.AIBackendClaude),
		setupSnapshot: aibackend.Snapshot{
			Selected: config.AIBackendClaude,
			Claude: aibackend.Status{
				Backend:       config.AIBackendClaude,
				Label:         "Claude Code",
				Installed:     true,
				Authenticated: true,
				Ready:         true,
				Detail:        "Claude Code ready via claude.ai (max)",
			},
		},
	}

	hint := ansi.Strip(m.renderSetupHint(96))
	if !strings.Contains(hint, "Haiku") {
		t.Fatalf("renderSetupHint() = %q, want Haiku guidance", hint)
	}
}

func TestRenderSetupHintExplainsLocalModelPicker(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.AIBackend = config.AIBackendMLX

	m := Model{
		settingsBaseline: &settings,
		setupSelected:    mSetupSelectionForTest(config.AIBackendMLX),
		setupSnapshot: aibackend.Snapshot{
			Selected: config.AIBackendMLX,
			MLX: aibackend.Status{
				Backend:     config.AIBackendMLX,
				Label:       "MLX",
				Ready:       true,
				Endpoint:    "http://127.0.0.1:8080/v1",
				Models:      []string{"mlx-community/Qwen3.5-9B-MLX-4bit"},
				ActiveModel: "mlx-community/Qwen3.5-9B-MLX-4bit",
			},
		},
	}

	hint := ansi.Strip(m.renderSetupHint(120))
	if !strings.Contains(hint, "Press m to pin a discovered") || !strings.Contains(hint, "Enter continues to endpoint") {
		t.Fatalf("renderSetupHint() = %q, want local model picker guidance", hint)
	}
	if !strings.Contains(hint, "Qwen3.5-9B-MLX-4bit") {
		t.Fatalf("renderSetupHint() = %q, want current discovered model", hint)
	}
}

func TestSetupLocalModelPickerUpdatesBaseline(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.AIBackend = config.AIBackendMLX

	m := Model{
		setupMode:        true,
		settingsBaseline: &settings,
		setupSelected:    mSetupSelectionForTest(config.AIBackendMLX),
		setupSnapshot: aibackend.Snapshot{
			Selected: config.AIBackendMLX,
			MLX: aibackend.Status{
				Backend:     config.AIBackendMLX,
				Label:       "MLX",
				Ready:       true,
				Endpoint:    "http://127.0.0.1:8080/v1",
				Models:      []string{"mlx-community/Qwen3.5-9B-MLX-4bit", "mlx-community/Qwen3.5-4B-MLX-4bit"},
				ActiveModel: "mlx-community/Qwen3.5-9B-MLX-4bit",
			},
		},
	}

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("m")})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("opening the local model picker should not return a command")
	}
	if !got.localModelPickerVisible {
		t.Fatalf("local model picker should be visible after pressing m in setup")
	}

	updated, _ = got.Update(tea.KeyMsg{Type: tea.KeyDown})
	got = updated.(Model)
	updated, _ = got.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if got.localModelPickerVisible {
		t.Fatalf("local model picker should close after choosing a model")
	}
	if got.currentSettingsBaseline().MLXModel != "mlx-community/Qwen3.5-9B-MLX-4bit" {
		t.Fatalf("saved local model = %q, want first discovered model after selecting row 1", got.currentSettingsBaseline().MLXModel)
	}
}

func TestSetupOllamaModelFieldOpensPicker(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.AIBackend = config.AIBackendOllama

	m := Model{
		setupMode:        true,
		setupConfigMode:  true,
		setupStep:        setupStepProjectConfig,
		setupFocusedRole: setupRoleProjectReports,
		settingsBaseline: &settings,
		settingsFields:   newSettingsFields(settings),
		setupSelected:    mSetupSelectionForTest(config.AIBackendOllama),
		setupSnapshot: aibackend.Snapshot{
			Selected: config.AIBackendOllama,
			Ollama: aibackend.Status{
				Backend:     config.AIBackendOllama,
				Label:       "Ollama",
				Ready:       true,
				Endpoint:    "http://127.0.0.1:11434/v1",
				Models:      []string{"qwen3:8b", "llama3.2:3b"},
				ActiveModel: "qwen3:8b",
			},
		},
	}
	fields := m.setupConfigFieldIndexes()
	for i, field := range fields {
		if field == settingsFieldOllamaModel {
			m.setupConfigSelected = i
			break
		}
	}

	updated, cmd := m.updateSetupMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("opening Ollama picker should not queue a command")
	}
	if !got.localModelPickerVisible || got.localModelPickerBackend != config.AIBackendOllama {
		t.Fatalf("Ollama model field should open Ollama picker, visible=%v backend=%s", got.localModelPickerVisible, got.localModelPickerBackend)
	}

	updated, _ = got.Update(tea.KeyMsg{Type: tea.KeyDown})
	got = updated.(Model)
	updated, _ = got.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if got.localModelPickerVisible {
		t.Fatalf("local model picker should close after choosing a model")
	}
	if got.currentSettingsBaseline().OllamaModel != "qwen3:8b" {
		t.Fatalf("saved Ollama model = %q, want qwen3:8b", got.currentSettingsBaseline().OllamaModel)
	}
}

func mSetupSelectionForTest(backend config.AIBackend) int {
	return providerChoiceSelection(Model{}.providerChoices(providerChoiceRoleProjectReports, config.EditableSettings{}), backend)
}

func mSetupBossSelectionForTest(backend config.AIBackend) int {
	return providerChoiceSelection(Model{}.providerChoices(providerChoiceRoleBossChat, config.EditableSettings{}), backend)
}

func queueTodoWorktreeSuggestionForTest(t *testing.T, ctx context.Context, st *store.Store, todoID int64) {
	t.Helper()
	queued, err := st.QueueTodoWorktreeSuggestion(ctx, todoID)
	if err != nil {
		t.Fatalf("queue todo worktree suggestion: %v", err)
	}
	if queued {
		return
	}
	suggestion, err := st.GetTodoWorktreeSuggestion(ctx, todoID)
	if err != nil {
		t.Fatalf("get existing todo worktree suggestion: %v", err)
	}
	if suggestion.Status != model.TodoWorktreeSuggestionQueued {
		t.Fatalf("existing todo worktree suggestion status = %q, want %q", suggestion.Status, model.TodoWorktreeSuggestionQueued)
	}
}
