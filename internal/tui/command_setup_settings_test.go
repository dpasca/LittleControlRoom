package tui

import (
	"context"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	bossui "lcroom/internal/boss"
	"lcroom/internal/brand"
	"lcroom/internal/codexapp"
	"lcroom/internal/commands"
	"lcroom/internal/config"
	"lcroom/internal/events"
	"lcroom/internal/model"
	"lcroom/internal/service"
	"lcroom/internal/store"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCommandTabCompletesSelectedSuggestion(t *testing.T) {
	input := textinput.New()
	input.SetValue("/sort r")

	m := Model{
		commandMode:  true,
		commandInput: input,
		width:        100,
		height:       24,
	}
	m.syncCommandSelection()

	updated, _ := m.updateCommandMode(tea.KeyMsg{Type: tea.KeyTab})
	got := updated.(Model)
	if got.commandInput.Value() != "/sort recent" {
		t.Fatalf("command input = %q, want /sort recent", got.commandInput.Value())
	}
}

func TestCommandEnterUsesAutocompleteSuggestion(t *testing.T) {
	input := textinput.New()
	input.SetValue("/focus d")

	m := Model{
		commandMode:  true,
		commandInput: input,
		focusedPane:  focusProjects,
		width:        100,
		height:       24,
	}
	m.syncCommandSelection()

	updated, _ := m.updateCommandMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if got.commandMode {
		t.Fatalf("command mode should close after executing a valid command")
	}
	if got.focusedPane != focusDetail {
		t.Fatalf("focusedPane = %s, want %s", got.focusedPane, focusDetail)
	}
}

func TestCommandEnterUsesHighlightedSuggestionOverValidPrefix(t *testing.T) {
	input := textinput.New()
	input.SetValue("/open")

	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider: req.Provider.Normalized(),
				Started:  true,
				Status:   req.Provider.Label() + " session ready",
			},
		}, nil
	})

	m := Model{
		commandMode:  true,
		commandInput: input,
		codexManager: manager,
		projects: []model.ProjectSummary{{
			Path:          "/tmp/demo",
			Name:          "demo",
			PresentOnDisk: true,
		}},
		selected:      0,
		codexInput:    newCodexTextarea(),
		codexDrafts:   make(map[string]codexDraft),
		codexViewport: viewport.New(0, 0),
		width:         100,
		height:        24,
	}
	m.syncCommandSelection()
	m.moveCommandSelection(1)

	updated, cmd := m.updateCommandMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("enter should launch the highlighted /opencode suggestion")
	}
	if got.commandMode {
		t.Fatalf("command mode should close after executing the highlighted suggestion")
	}
	if got.codexPendingOpen == nil || got.codexPendingOpen.provider != codexapp.ProviderOpenCode {
		t.Fatalf("codexPendingOpen = %#v, want pending OpenCode launch", got.codexPendingOpen)
	}

	msg := cmd()
	opened, ok := msg.(codexSessionOpenedMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want codexSessionOpenedMsg", msg)
	}
	if opened.err != nil {
		t.Fatalf("highlighted /opencode returned error = %v", opened.err)
	}
	if len(requests) != 1 || requests[0].Provider != codexapp.ProviderOpenCode {
		t.Fatalf("launch requests = %#v, want one OpenCode launch", requests)
	}
}

func TestCommandEnterNonAIFoldersOnChangesVisibility(t *testing.T) {
	input := textinput.New()
	input.SetValue("/non-ai-folders on")

	m := Model{
		allProjects: []model.ProjectSummary{
			{Path: "/tmp/ai", Name: "ai", LastActivity: time.Date(2026, 3, 7, 10, 0, 0, 0, time.UTC)},
			{Path: "/tmp/plain", Name: "plain"},
		},
		projects: []model.ProjectSummary{
			{Path: "/tmp/ai", Name: "ai", LastActivity: time.Date(2026, 3, 7, 10, 0, 0, 0, time.UTC)},
		},
		visibility:   visibilityAIFolders,
		commandMode:  true,
		commandInput: input,
		width:        100,
		height:       24,
	}
	m.syncCommandSelection()

	updated, _ := m.updateCommandMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if got.visibility != visibilityAllFolders {
		t.Fatalf("visibility = %s, want %s", got.visibility, visibilityAllFolders)
	}
	if len(got.projects) != 2 {
		t.Fatalf("visible project count = %d, want 2", len(got.projects))
	}
}

func TestCommandEnterOpensSettingsMode(t *testing.T) {
	input := textinput.New()
	input.SetValue("/settings")

	m := Model{
		commandMode:  true,
		commandInput: input,
		width:        100,
		height:       24,
	}
	m.syncCommandSelection()

	updated, _ := m.updateCommandMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if !got.settingsMode {
		t.Fatalf("settings mode should open after /settings")
	}
	if got.commandMode {
		t.Fatalf("command mode should close after /settings")
	}
	if len(got.settingsFields) != settingsFieldAIBackend+1 {
		t.Fatalf("settings field count = %d, want %d", len(got.settingsFields), settingsFieldAIBackend+1)
	}
}

func TestCommandEnterPrivacySettingsOpensPrivacyField(t *testing.T) {
	input := textinput.New()
	input.SetValue("/privacy settings")

	m := Model{
		commandMode:       true,
		commandInput:      input,
		projectCategories: []model.ProjectCategory{{ID: "cat_private", Name: "Private"}},
		width:             100,
		height:            24,
	}
	m.syncCommandSelection()

	updated, cmd := m.updateCommandMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if got.settingsMode {
		t.Fatalf("settings mode should not open after /privacy settings")
	}
	if got.commandMode {
		t.Fatalf("command mode should close after /privacy settings")
	}
	if got.categoryDialog == nil {
		t.Fatalf("/privacy settings should open the category privacy dialog")
	}
	if got.categoryDialog.Mode != categoryDialogModePrivacy {
		t.Fatalf("category dialog mode = %v, want privacy picker", got.categoryDialog.Mode)
	}
	if got.status != "Choose a category to mark private or public" {
		t.Fatalf("status = %q, want category privacy hint", got.status)
	}
	if cmd != nil {
		t.Fatalf("/privacy settings should not need an async command")
	}
}

func TestCommandEnterOpensSkillsDialog(t *testing.T) {
	input := textinput.New()
	input.SetValue("/skills")

	m := Model{
		commandMode:  true,
		commandInput: input,
		width:        100,
		height:       24,
	}
	m.syncCommandSelection()

	updated, cmd := m.updateCommandMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if got.skillsDialog == nil || !got.skillsDialog.Loading {
		t.Fatalf("skills dialog should open loading after /skills, got %#v", got.skillsDialog)
	}
	if got.commandMode {
		t.Fatalf("command mode should close after /skills")
	}
	if cmd == nil {
		t.Fatalf("/skills should return a skills inventory load command")
	}
}

func TestCommandEnterOpensBossMode(t *testing.T) {
	input := textinput.New()
	input.SetValue("/boss")
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.BossChatBackend = config.AIBackendOpenAIAPI
	settings.OpenAIAPIKey = "sk-test-example"

	m := Model{
		commandMode:      true,
		commandInput:     input,
		settingsBaseline: &settings,
		width:            100,
		height:           24,
	}
	m.syncCommandSelection()

	updated, cmd := m.updateCommandMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if !got.bossMode {
		t.Fatalf("boss mode should open after /boss")
	}
	if got.commandMode {
		t.Fatalf("command mode should close after /boss")
	}
	if cmd == nil {
		t.Fatalf("/boss should return the embedded boss init command")
	}
	rendered := ansi.Strip(got.View())
	if !strings.Contains(rendered, "Boss Chat") || !strings.Contains(rendered, "Boss Desk") {
		t.Fatalf("boss view missing expected panels: %q", rendered)
	}
	if strings.Contains(rendered, "Jump") || strings.Contains(rendered, "Situation") || strings.Contains(rendered, "Notes") {
		t.Fatalf("boss view should not render the old side panels: %q", rendered)
	}
	if strings.Contains(rendered, "Little Room") {
		t.Fatalf("boss view should use the shared TUI panel language, got: %q", rendered)
	}
	lines := strings.Split(rendered, "\n")
	if len(lines) != got.height {
		t.Fatalf("boss view line count = %d, want terminal height %d: %q", len(lines), got.height, rendered)
	}
	if len(lines) < 2 || !strings.Contains(lines[0], "Boss Mode") {
		t.Fatalf("boss view should use a boss-specific top status line: %q", rendered)
	}
	if strings.Contains(lines[0], "high-level project chat") {
		t.Fatalf("boss view should not show redundant high-level chat label: %q", rendered)
	}
	if !strings.HasPrefix(lines[1], "╭") {
		t.Fatalf("boss frames should start below the boss top status line: %q", rendered)
	}
	if strings.Contains(lines[0], brand.Name) {
		t.Fatalf("boss view should not show the classic app title in the top bar: %q", rendered)
	}
	if !strings.Contains(lines[len(lines)-1], "Enter") || !strings.Contains(lines[len(lines)-1], "Alt+Enter") || !strings.Contains(lines[len(lines)-1], "Alt+Up") {
		t.Fatalf("boss footer should show boss chat actions: %q", rendered)
	}
	if strings.Contains(lines[len(lines)-1], "Esc hide") {
		t.Fatalf("boss footer should keep Esc as a silent hide alias: %q", rendered)
	}
	if strings.Contains(lines[len(lines)-1], "ctrl+j") {
		t.Fatalf("boss footer should advertise Alt+Enter newline, not ctrl+j: %q", rendered)
	}
	if strings.Contains(lines[len(lines)-1], "q quit") {
		t.Fatalf("boss footer should not show the classic q quit action: %q", rendered)
	}
	lastBodyLine := ""
	for i := len(lines) - 2; i >= 1; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			lastBodyLine = lines[i]
			break
		}
	}
	if lastBodyLine == "" || !strings.Contains(lastBodyLine, "╰") {
		t.Fatalf("boss footer should consume only one row and leave frame content above it: %q", rendered)
	}
	for _, line := range strings.Split(got.View(), "\n") {
		if gotWidth := ansi.StringWidth(ansi.Strip(line)); gotWidth > got.width {
			t.Fatalf("boss view line width = %d, want <= %d: %q", gotWidth, got.width, ansi.Strip(line))
		}
	}
}

func TestCommandEnterOpensBossModeWithMouseCapture(t *testing.T) {
	input := textinput.New()
	input.SetValue("/boss")
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.BossChatBackend = config.AIBackendOpenAIAPI
	settings.OpenAIAPIKey = "sk-test-example"

	m := Model{
		commandMode:      true,
		commandInput:     input,
		settingsBaseline: &settings,
		width:            100,
		height:           24,
	}
	m.syncCommandSelection()

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if !got.bossMode {
		t.Fatalf("boss mode should open after /boss")
	}
	if !got.mouseEnabled {
		t.Fatalf("boss mode should enable mouse capture for scoped chat selection")
	}

	updated, _ = got.Update(bossui.ExitMsg{})
	got = updated.(Model)
	if got.mouseEnabled {
		t.Fatalf("closing boss mode should release mouse capture")
	}
}

func TestCommandEnterBossUnconfiguredShowsSetupPrompt(t *testing.T) {
	input := textinput.New()
	input.SetValue("/boss")

	m := Model{
		commandMode:  true,
		commandInput: input,
		width:        100,
		height:       24,
	}
	m.syncCommandSelection()

	updated, cmd := m.updateCommandMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("unconfigured /boss should not start async boss work")
	}
	if got.bossMode {
		t.Fatalf("unconfigured /boss should not open boss mode")
	}
	if got.bossSetupPrompt == nil {
		t.Fatalf("unconfigured /boss should open the setup prompt")
	}
	if got.commandMode {
		t.Fatalf("command mode should close after /boss")
	}
	if strings.Contains(got.bossSetupPrompt.Reason, "saved OpenAI API key") {
		t.Fatalf("default boss setup prompt reason should not imply OpenAI-only setup: %q", got.bossSetupPrompt.Reason)
	}
	if strings.Contains(got.bossSetupPrompt.Reason, "OpenAI API") || strings.Contains(got.bossSetupPrompt.Reason, "MLX") || strings.Contains(got.bossSetupPrompt.Reason, "Ollama") {
		t.Fatalf("default boss setup prompt reason should stay provider-agnostic: %q", got.bossSetupPrompt.Reason)
	}
	if !strings.Contains(got.bossSetupPrompt.Reason, "setup") || !strings.Contains(got.bossSetupPrompt.Reason, "boss chat backend") {
		t.Fatalf("boss setup prompt reason = %q, want setup guidance", got.bossSetupPrompt.Reason)
	}
	rendered := ansi.Strip(got.View())
	for _, want := range []string{"Boss Chat Setup", "Open setup", "Cancel"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("boss setup prompt missing %q: %q", want, rendered)
		}
	}
}

func TestBossSetupPromptEnterOpensSettingsFocusedOnBossChat(t *testing.T) {
	m := Model{
		bossSetupPrompt: &bossSetupPromptState{Selected: bossSetupPromptOpenSetup},
		width:           100,
		height:          24,
	}

	updated, cmd := m.updateBossSetupPromptMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("opening settings from boss prompt should return a focus command")
	}
	if got.bossSetupPrompt != nil {
		t.Fatalf("boss setup prompt should close")
	}
	if !got.settingsMode {
		t.Fatalf("settings mode should open")
	}
	if got.settingsSelected != settingsFieldBossChatBackend {
		t.Fatalf("settings selected = %d, want boss chat backend field", got.settingsSelected)
	}
}

func TestBossModeEscReturnsToClassicTUI(t *testing.T) {
	m := Model{
		bossMode:  true,
		bossModel: bossui.NewEmbedded(context.Background(), nil),
		width:     100,
		height:    24,
	}

	updated, cmd := m.updateBossModeMessage(tea.KeyMsg{Type: tea.KeyEsc})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("boss esc should return an exit command")
	}
	msg := cmd()
	if _, ok := msg.(bossui.ExitMsg); !ok {
		t.Fatalf("cmd() returned %T, want boss.ExitMsg", msg)
	}

	updated, _ = got.Update(msg)
	got = updated.(Model)
	if got.bossMode {
		t.Fatalf("boss mode should hide after exit message")
	}
	if got.status != "Boss mode hidden" {
		t.Fatalf("status = %q, want Boss mode hidden", got.status)
	}
}

func TestBossModeAltUpReturnsToClassicTUI(t *testing.T) {
	m := Model{
		bossMode:  true,
		bossModel: bossui.NewEmbedded(context.Background(), nil),
		width:     100,
		height:    24,
	}

	updated, cmd := m.updateBossModeMessage(tea.KeyMsg{Type: tea.KeyUp, Alt: true})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("boss alt+up should return an exit command")
	}
	msg := cmd()
	if _, ok := msg.(bossui.ExitMsg); !ok {
		t.Fatalf("cmd() returned %T, want boss.ExitMsg", msg)
	}

	updated, _ = got.Update(msg)
	got = updated.(Model)
	if got.bossMode {
		t.Fatalf("boss mode should hide after alt+up exit message")
	}
}

func TestBossAttentionEngineerAltUpReturnsToBossMode(t *testing.T) {
	project := model.ProjectSummary{
		Path:          "/tmp/demo",
		Name:          "demo",
		PresentOnDisk: true,
	}
	m := Model{
		ctx:             context.Background(),
		allProjects:     []model.ProjectSummary{project},
		projects:        []model.ProjectSummary{project},
		bossMode:        true,
		bossModelActive: true,
		bossModel:       bossui.NewEmbedded(context.Background(), nil),
		codexInput:      newCodexTextarea(),
		codexViewport:   viewport.New(0, 0),
		codexSnapshots: map[string]codexapp.Snapshot{
			project.Path: {
				ProjectPath: project.Path,
				Provider:    codexapp.ProviderCodex,
				Started:     true,
				Status:      "Codex session ready",
			},
		},
		width:  100,
		height: 24,
	}

	updated, launchCmd := m.openBossAttentionProjectItem(0, project.Path)
	got := updated.(Model)
	if got.bossMode {
		t.Fatalf("boss mode should hide while the engineer session is open")
	}
	if !got.returnToBossModeAfterCodexHide {
		t.Fatalf("engineer session should remember boss mode as its return target")
	}
	if got.codexPendingOpen == nil || !got.codexPendingOpen.showWhilePending {
		t.Fatalf("codexPendingOpen = %#v, want visible pending open", got.codexPendingOpen)
	}
	if launchCmd == nil {
		t.Fatalf("opening engineer session should return launch command")
	}

	got.codexPendingOpen = nil
	got.codexVisibleProject = project.Path
	updated, _ = got.updateCodexMode(tea.KeyMsg{Type: tea.KeyUp, Alt: true})
	got = updated.(Model)
	if !got.bossMode {
		t.Fatalf("alt+up from boss-opened engineer session should return to boss mode")
	}
	if got.returnToBossModeAfterCodexHide {
		t.Fatalf("return target should be consumed after returning to boss mode")
	}
	if got.codexVisibleProject != "" {
		t.Fatalf("codexVisibleProject = %q, want hidden", got.codexVisibleProject)
	}
}

func TestBossAttentionPendingEngineerAltUpReturnsToBossMode(t *testing.T) {
	project := model.ProjectSummary{
		Path:          "/tmp/demo",
		Name:          "demo",
		PresentOnDisk: true,
	}
	m := Model{
		ctx:             context.Background(),
		allProjects:     []model.ProjectSummary{project},
		projects:        []model.ProjectSummary{project},
		bossMode:        true,
		bossModelActive: true,
		bossModel:       bossui.NewEmbedded(context.Background(), nil),
		codexInput:      newCodexTextarea(),
		width:           100,
		height:          24,
	}

	updated, _ := m.openBossAttentionProjectItem(0, project.Path)
	got := updated.(Model)
	updated, _ = got.updateCodexMode(tea.KeyMsg{Type: tea.KeyUp, Alt: true})
	got = updated.(Model)
	if !got.bossMode {
		t.Fatalf("alt+up while the engineer session starts should return to boss mode")
	}
	if got.codexPendingOpen == nil {
		t.Fatalf("pending open should continue in the background")
	}
	if got.codexPendingOpen.showWhilePending {
		t.Fatalf("pending open should be hidden after alt+up")
	}
	if !got.codexPendingOpen.hideOnOpen {
		t.Fatalf("pending open should stay hidden when it finishes")
	}
}

func TestBossModeFooterDoesNotCoverTerminalFrames(t *testing.T) {
	for _, height := range []int{52, 45, 18, 13} {
		m := Model{
			bossMode:  true,
			bossModel: bossui.NewEmbedded(context.Background(), nil),
			width:     180,
			height:    height,
		}

		updated, _ := m.updateBossModeWindowSize()
		got := updated.(Model)
		rendered := ansi.Strip(got.View())
		lines := strings.Split(rendered, "\n")
		if len(lines) != got.height {
			t.Fatalf("boss view line count = %d, want terminal height %d:\n%s", len(lines), got.height, rendered)
		}
		if !strings.Contains(lines[len(lines)-1], "Enter") {
			t.Fatalf("boss footer should be the final row:\n%s", rendered)
		}
		lastBodyLine := ""
		for i := len(lines) - 2; i >= 1; i-- {
			if strings.TrimSpace(lines[i]) != "" {
				lastBodyLine = lines[i]
				break
			}
		}
		if !strings.HasPrefix(lastBodyLine, "╰") {
			t.Fatalf("boss footer should not cover the bottom frame row at height %d:\n%s", height, rendered)
		}
	}
}

func TestBossModeFooterAdvertisesFilePickerInsteadOfBossOffHint(t *testing.T) {
	m := Model{
		bossMode:  true,
		bossModel: bossui.NewEmbedded(context.Background(), nil),
		width:     120,
		height:    24,
	}

	rendered := ansi.Strip(m.renderBossModeFooter(120))
	if strings.Contains(rendered, "/boss off") {
		t.Fatalf("boss footer should not include old /boss off hint: %q", rendered)
	}
	for _, want := range []string{"Alt+O", "files", "Alt+Up", "hide"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("boss footer missing %q: %q", want, rendered)
		}
	}
	if strings.Contains(rendered, "Esc hide") {
		t.Fatalf("boss footer should keep Esc as a silent hide alias: %q", rendered)
	}
}

func TestBossModeForwardsTypingToChatInput(t *testing.T) {
	m := Model{
		bossMode:  true,
		bossModel: bossui.NewEmbedded(context.Background(), nil),
		width:     100,
		height:    24,
	}

	updated, _ := m.updateBossModeMessage(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h', 'i'}})
	got := updated.(Model)
	rendered := ansi.Strip(got.View())
	if !strings.Contains(rendered, "hi") {
		t.Fatalf("boss view should show typed input, got %q", rendered)
	}
}

func TestBossModeRoutesSessionLoadBeforeEnterSubmit(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dataDir := t.TempDir()
	st, err := store.Open(filepath.Join(dataDir, "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	cfg := config.Default()
	cfg.DataDir = dataDir
	cfg.DBPath = filepath.Join(dataDir, "little-control-room.sqlite")
	svc := service.New(cfg, st, events.NewBus(), nil)
	m := New(ctx, svc)
	m.width = 100
	m.height = 24

	updated, initCmd := m.openBossMode()
	got := updated.(Model)
	for _, msg := range collectCmdMsgs(initCmd) {
		if _, ok := msg.(bossui.TickMsg); ok {
			continue
		}
		if !bossui.IsMessage(msg) {
			continue
		}
		updated, _ = got.Update(msg)
		got = updated.(Model)
	}

	updated, _ = got.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("hello boss")})
	got = updated.(Model)
	updated, cmd := got.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if cmd == nil {
		t.Fatalf("enter should submit after session load; boss status = %q", got.bossModel.StatusText())
	}
	if strings.Contains(got.bossModel.StatusText(), "session is still loading") {
		t.Fatalf("boss status = %q, want submitted chat", got.bossModel.StatusText())
	}
}

func TestDispatchBossOffClosesBossMode(t *testing.T) {
	m := Model{
		bossMode:  true,
		bossModel: bossui.NewEmbedded(context.Background(), nil),
		width:     100,
		height:    24,
	}

	updated, cmd := m.dispatchCommand(commands.Invocation{Kind: commands.KindBoss, Toggle: commands.ToggleOff})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("/boss off should not return async work")
	}
	if got.bossMode {
		t.Fatalf("/boss off should hide boss mode")
	}
}

func TestDispatchSetupOpensGettingStartedSettings(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.AIBackend = config.AIBackendOpenCode

	m := Model{
		settingsBaseline: &settings,
		settingsMode:     true,
		width:            100,
		height:           24,
	}

	updated, cmd := m.dispatchCommand(commands.Invocation{Kind: commands.KindSetup})
	got := updated.(Model)
	if got.setupMode {
		t.Fatalf("/setup should not open the retired setup wizard")
	}
	if !got.settingsMode {
		t.Fatalf("/setup should open settings mode")
	}
	if got.settingsSectionMenu {
		t.Fatalf("/setup should open Getting Started directly")
	}
	if got.activeSettingsSection().id != settingsSectionGettingStarted {
		t.Fatalf("active settings section = %q, want Getting Started", got.activeSettingsSection().id)
	}
	if got.settingsSelected != settingsFieldAIBackend {
		t.Fatalf("settingsSelected = %d, want project reports field", got.settingsSelected)
	}
	if got.status != "Setup open in Getting Started. Choose a row, press Enter to configure, or ctrl+s to save." {
		t.Fatalf("status = %q, want Getting Started setup status", got.status)
	}
	if cmd == nil {
		t.Fatalf("/setup should refresh backend availability")
	}
}

func TestDispatchCommandRefreshAlsoRefreshesSelectedProject(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	projectPath := filepath.Join(t.TempDir(), "demo")
	if err := os.MkdirAll(projectPath, 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:           projectPath,
		Name:           "demo",
		Status:         model.StatusIdle,
		AttentionScore: 5,
		PresentOnDisk:  true,
		InScope:        true,
		UpdatedAt:      time.Now(),
	}); err != nil {
		t.Fatalf("seed project: %v", err)
	}

	svc := service.New(config.Default(), st, events.NewBus(), nil)
	svc.SetSessionClassifier(&usageSnapshotClassifier{})

	m := Model{
		ctx:         ctx,
		svc:         svc,
		allProjects: []model.ProjectSummary{{Path: projectPath, Name: "demo", PresentOnDisk: true}},
		visibility:  visibilityAllFolders,
		sortMode:    sortByAttention,
	}
	m.rebuildProjectList(projectPath)

	updated, cmd := m.dispatchCommand(commands.Invocation{Kind: commands.KindRefresh})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("dispatchCommand(/refresh) should queue refresh work")
	}
	if !got.scanInFlight {
		t.Fatalf("dispatchCommand(/refresh) should mark scan in flight")
	}
	if got.status != "Scanning and retrying failed assessments..." {
		t.Fatalf("status = %q, want refresh status", got.status)
	}

	msgs := collectCmdMsgs(cmd)
	foundProjectRefresh := false
	foundScan := false
	for _, msg := range msgs {
		switch typed := msg.(type) {
		case projectStatusRefreshedMsg:
			if typed.projectPath == projectPath && typed.err == nil {
				foundProjectRefresh = true
			}
		case scanMsg:
			if typed.err == nil {
				foundScan = true
			}
		}
	}
	if !foundProjectRefresh {
		t.Fatalf("expected projectStatusRefreshedMsg for selected project, got %#v", msgs)
	}
	if !foundScan {
		t.Fatalf("expected scanMsg from /refresh, got %#v", msgs)
	}
}
