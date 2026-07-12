package tui

import (
	"context"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"lcroom/internal/codexapp"
	"lcroom/internal/commands"
	"lcroom/internal/config"
	"lcroom/internal/events"
	"lcroom/internal/model"
	"lcroom/internal/service"
	"lcroom/internal/store"
	"os"
	"path/filepath"
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
