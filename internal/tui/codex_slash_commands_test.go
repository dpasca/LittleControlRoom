package tui

import (
	"context"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"lcroom/internal/codexapp"
	"lcroom/internal/codexcli"
	"lcroom/internal/config"
	"lcroom/internal/events"
	"lcroom/internal/model"
	"lcroom/internal/projectrun"
	"lcroom/internal/service"
	"lcroom/internal/todocapture"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestVisibleCodexSlashSuggestionsRender(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Started: true,
			Preset:  codexcli.PresetYolo,
			Status:  "Codex session ready",
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Preset:      codexcli.PresetYolo,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	input := newCodexTextarea()
	input.SetValue("/")

	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          input,
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              24,
	}
	m.syncCodexViewport(true)

	rendered := ansi.Strip(m.renderCodexView())
	if !strings.Contains(rendered, "Embedded Slash Commands") {
		t.Fatalf("rendered view should show embedded slash commands: %q", rendered)
	}
	if !strings.Contains(rendered, "/new [prompt]") || !strings.Contains(rendered, "/resume [session-id]") || !strings.Contains(rendered, "/sessions [session-id]") || !strings.Contains(rendered, "/model") {
		t.Fatalf("rendered view should list embedded slash suggestions: %q", rendered)
	}
	if !strings.Contains(rendered, "Enter run  ctrl+c close  Alt+Up hide") {
		t.Fatalf("rendered view should advertise slash command handling in the footer: %q", rendered)
	}
}

func TestVisibleCodexSlashPermissionsSummaryNotClippedByEllipsis(t *testing.T) {
	input := newCodexTextarea()
	input.SetValue("/permissions ")

	m := Model{
		codexVisibleProject: "/tmp/demo",
		codexInput:          input,
		width:               120,
		height:              24,
	}
	blocks := m.renderCodexSlashBlocks(120)
	rendered := ansi.Strip(strings.Join(blocks, "\n"))
	if !strings.Contains(rendered, "Show what Off, Low, and Medium allow in LCAgent") {
		t.Fatalf("expected full /permissions summary to render: %q", rendered)
	}
}

func TestVisibleCodexSlashSuggestionRowNotClippedByEllipsis(t *testing.T) {
	input := newCodexTextarea()
	input.SetValue("/permissions ")

	m := Model{
		codexVisibleProject: "/tmp/demo",
		codexInput:          input,
		width:               48,
		height:              24,
	}
	blocks := m.renderCodexSlashBlocks(48)
	rendered := ansi.Strip(strings.Join(blocks, "\n"))
	if !strings.Contains(rendered, "Show what Off, Low, and Medium allow in LCAgent") {
		t.Fatalf("expected untruncated /permissions row summary: %q", rendered)
	}
	if strings.Contains(rendered, "...") {
		t.Fatalf("unexpected ellipsis remains in /permissions row: %q", rendered)
	}
}

func TestVisibleCodexSlashSuggestsHostTaskActions(t *testing.T) {
	input := newCodexTextarea()
	input.SetValue("/task")

	m := Model{
		codexVisibleProject: "/tmp/demo",
		codexInput:          input,
	}

	suggestions := m.codexSlashSuggestions()
	if len(suggestions) != 1 {
		t.Fatalf("codexSlashSuggestions(/task) returned %d suggestions, want 1", len(suggestions))
	}
	if suggestions[0].Insert != "/task-actions" {
		t.Fatalf("codexSlashSuggestions(/task)[0].Insert = %q, want /task-actions", suggestions[0].Insert)
	}
}

func TestVisibleCodexSlashTaskActionsFallsBackToHostCommand(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Started: true,
			Preset:  codexcli.PresetYolo,
			Status:  "Codex session ready",
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Preset:      codexcli.PresetYolo,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	input := newCodexTextarea()
	input.SetValue("/task-actions")

	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          input,
		codexViewport:       viewport.New(0, 0),
		projects: []model.ProjectSummary{{
			Name:          "answer Sarah email",
			Path:          "/tmp/tasks/answer-sarah-email",
			Kind:          model.ProjectKindScratchTask,
			PresentOnDisk: true,
		}},
		selected: 0,
		width:    100,
		height:   24,
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("embedded /task-actions should open the host dialog without queuing work")
	}
	if got.scratchTaskAction == nil {
		t.Fatalf("embedded /task-actions should open the scratch task action dialog")
	}
	if got.codexInput.Value() != "" {
		t.Fatalf("codex input should clear after host command fallback, got %q", got.codexInput.Value())
	}
	rendered := ansi.Strip(got.View())
	if !strings.Contains(rendered, "Task Actions") || !strings.Contains(rendered, "Archive") || !strings.Contains(rendered, "Delete") {
		t.Fatalf("host task action dialog should render over visible Codex, got %q", rendered)
	}
}

func TestVisibleLCAgentSlashSettingsOpensLCAgentSettings(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Provider: codexapp.ProviderLCAgent,
			Started:  true,
			Status:   "LCAgent session ready",
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		Provider:    codexapp.ProviderLCAgent,
		ProjectPath: "/tmp/demo",
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	input := newCodexTextarea()
	input.SetValue("/settings")
	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          input,
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              24,
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("embedded /settings should queue settings refresh/focus commands")
	}
	if !got.settingsMode {
		t.Fatalf("embedded /settings should open settings mode")
	}
	if got.settingsSelected != settingsFieldLCAgentWebSearchBackend {
		t.Fatalf("settingsSelected = %d, want LCAgent web search backend", got.settingsSelected)
	}
	if got.activeSettingsSection().id != settingsSectionLCAgent {
		t.Fatalf("active settings section = %q, want LCAgent", got.activeSettingsSection().id)
	}
	if got.settingsEmbeddedProject != "/tmp/demo" || got.settingsEmbeddedProvider != codexapp.ProviderLCAgent {
		t.Fatalf("embedded settings context = (%q, %q), want LCAgent /tmp/demo", got.settingsEmbeddedProject, got.settingsEmbeddedProvider)
	}
	if got.codexInput.Value() != "" {
		t.Fatalf("codex input should clear after embedded /settings, got %q", got.codexInput.Value())
	}
}

func TestScratchTaskActionDialogTakesInputPriorityOverVisibleCodex(t *testing.T) {
	m := Model{
		codexVisibleProject: "/tmp/demo",
		scratchTaskAction: &scratchTaskActionConfirmState{
			ProjectPath:   "/tmp/tasks/answer-sarah-email",
			ProjectName:   "answer Sarah email",
			PresentOnDisk: true,
			Selected:      scratchTaskActionFocusKeep,
		},
	}

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("closing task actions over visible Codex should not queue work")
	}
	if got.scratchTaskAction != nil {
		t.Fatalf("Esc should close the scratch task action dialog before reaching visible Codex")
	}
	if got.codexVisibleProject != "/tmp/demo" {
		t.Fatalf("codexVisibleProject = %q, want visible pane to remain open", got.codexVisibleProject)
	}
}

func TestVisibleCodexSlashTabCyclesSuggestions(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Started: true,
			Preset:  codexcli.PresetYolo,
			Status:  "Codex session ready",
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Preset:      codexcli.PresetYolo,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	input := newCodexTextarea()
	input.SetValue("/")

	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          input,
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              24,
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyTab})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("tab completion should not queue a command")
	}
	if got.codexInput.Value() != "/new" {
		t.Fatalf("codex input = %q, want /new after first tab", got.codexInput.Value())
	}

	updated, cmd = got.updateCodexMode(tea.KeyMsg{Type: tea.KeyTab})
	got = updated.(Model)
	if cmd != nil {
		t.Fatalf("tab cycling should not queue a command")
	}
	if got.codexInput.Value() != "/resume" {
		t.Fatalf("codex input = %q, want /resume after second tab", got.codexInput.Value())
	}

	updated, cmd = got.updateCodexMode(tea.KeyMsg{Type: tea.KeyTab})
	got = updated.(Model)
	if cmd != nil {
		t.Fatalf("tab cycling should not queue a command")
	}
	if got.codexInput.Value() != "/sessions" {
		t.Fatalf("codex input = %q, want /sessions after third tab", got.codexInput.Value())
	}

	updated, cmd = got.updateCodexMode(tea.KeyMsg{Type: tea.KeyTab})
	got = updated.(Model)
	if cmd != nil {
		t.Fatalf("tab cycling should not queue a command")
	}
	if got.codexInput.Value() != "/model" {
		t.Fatalf("codex input = %q, want /model after fourth tab", got.codexInput.Value())
	}

	updated, cmd = got.updateCodexMode(tea.KeyMsg{Type: tea.KeyShiftTab})
	got = updated.(Model)
	if cmd != nil {
		t.Fatalf("shift+tab cycling should not queue a command")
	}
	if got.codexInput.Value() != "/sessions" {
		t.Fatalf("codex input = %q, want /sessions after shift+tab from /model", got.codexInput.Value())
	}

	updated, cmd = got.updateCodexMode(tea.KeyMsg{Type: tea.KeyTab})
	got = updated.(Model)
	if cmd != nil {
		t.Fatalf("tab cycling should not queue a command")
	}
	if got.codexInput.Value() != "/model" {
		t.Fatalf("codex input = %q, want /model after tab from /sessions", got.codexInput.Value())
	}

	updated, cmd = got.updateCodexMode(tea.KeyMsg{Type: tea.KeyTab})
	got = updated.(Model)
	if cmd != nil {
		t.Fatalf("tab cycling should not queue a command")
	}
	if got.codexInput.Value() != "/status" {
		t.Fatalf("codex input = %q, want /status after fifth tab", got.codexInput.Value())
	}

	updated, cmd = got.updateCodexMode(tea.KeyMsg{Type: tea.KeyTab})
	got = updated.(Model)
	if cmd != nil {
		t.Fatalf("tab cycling should not queue a command")
	}
	if got.codexInput.Value() != "/reconnect" {
		t.Fatalf("codex input = %q, want /reconnect after sixth tab", got.codexInput.Value())
	}
}

func TestVisibleCodexSlashArrowsNavigateGoalSuggestions(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Started: true,
			Preset:  codexcli.PresetYolo,
			Status:  "Codex session ready",
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Preset:      codexcli.PresetYolo,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	input := newCodexTextarea()
	input.SetValue("/goal ")

	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          input,
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              24,
	}

	var updated tea.Model = m
	for range 4 {
		var cmd tea.Cmd
		updated, cmd = updated.(Model).updateCodexMode(tea.KeyMsg{Type: tea.KeyDown})
		if cmd != nil {
			t.Fatalf("arrow navigation should not queue a command")
		}
	}
	got := updated.(Model)
	selected, ok := got.selectedCodexSlashSuggestion()
	if !ok {
		t.Fatalf("expected selected slash suggestion")
	}
	if selected.Insert != "/goal clear" {
		t.Fatalf("selected suggestion = %q, want /goal clear", selected.Insert)
	}
	rendered := ansi.Strip(strings.Join(got.renderCodexSlashBlocks(100), "\n"))
	for _, want := range []string{"/goal clear", "↑ 1 more", "↓ 2 more"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered slash blocks missing %q: %q", want, rendered)
		}
	}
}

func TestVisibleCodexSlashBossOpensBossMode(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Started: true,
			Preset:  codexcli.PresetYolo,
			Status:  "Codex session ready",
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Preset:      codexcli.PresetYolo,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.BossChatBackend = config.AIBackendOpenAIAPI
	settings.OpenAIAPIKey = "sk-test-example"

	input := newCodexTextarea()
	input.SetValue("/chat")

	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          input,
		codexViewport:       viewport.New(0, 0),
		settingsBaseline:    &settings,
		width:               100,
		height:              24,
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if !got.helpChatMode {
		t.Fatalf("embedded /chat should open Chat")
	}
	if got.codexVisibleProject != "" || got.codexHiddenProject != "/tmp/demo" {
		t.Fatalf("embedded /chat should return to the dashboard, visible=%q hidden=%q", got.codexVisibleProject, got.codexHiddenProject)
	}
	if got.bossSetupPrompt != nil {
		t.Fatalf("configured embedded /chat should not show setup prompt")
	}
	if got.codexInput.Value() != "" {
		t.Fatalf("codex input should clear after /chat, got %q", got.codexInput.Value())
	}
	if cmd == nil {
		t.Fatalf("embedded /chat should return the Chat init command")
	}
	if rendered := ansi.Strip(got.View()); !strings.Contains(rendered, "Chat") {
		t.Fatalf("embedded /chat should render Chat over the dashboard: %q", rendered)
	}
}

func TestVisibleOpenCodeSlashReconnectReopensSameSession(t *testing.T) {
	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		entries := []codexapp.TranscriptEntry(nil)
		if len(requests) == 1 {
			entries = []codexapp.TranscriptEntry{{
				ItemID: "call_live_tool",
				Kind:   codexapp.TranscriptTool,
				Text:   "Tool exec [completed]",
			}}
		}
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider: codexapp.ProviderOpenCode,
				Started:  true,
				Status:   "OpenCode session ready",
				ThreadID: "ses-old1",
				Entries:  entries,
			},
		}, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Provider:    codexapp.ProviderOpenCode,
		ResumeID:    "ses-old1",
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	input := newCodexTextarea()
	input.SetValue("/reconnect")

	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          input,
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              24,
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("enter should run the embedded /reconnect command")
	}
	if got.codexInput.Value() != "" {
		t.Fatalf("codex input should clear after /reconnect, got %q", got.codexInput.Value())
	}
	if got.status != "Reconnecting embedded OpenCode session..." {
		t.Fatalf("status = %q, want reconnect notice", got.status)
	}
	if got.codexPendingOpen == nil || got.codexPendingOpen.provider != codexapp.ProviderOpenCode {
		t.Fatalf("codexPendingOpen = %#v, want pending OpenCode reconnect", got.codexPendingOpen)
	}

	msg := cmd()
	opened, ok := msg.(codexSessionOpenedMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want codexSessionOpenedMsg", msg)
	}
	if opened.err != nil {
		t.Fatalf("/reconnect returned error = %v", opened.err)
	}
	if opened.status != "Reconnected embedded OpenCode session ses-old1. Alt+Up hides it." {
		t.Fatalf("opened.status = %q, want reconnect confirmation", opened.status)
	}
	if len(requests) != 2 {
		t.Fatalf("launch requests = %d, want 2", len(requests))
	}
	if requests[1].Provider != codexapp.ProviderOpenCode {
		t.Fatalf("second launch provider = %q, want %q", requests[1].Provider, codexapp.ProviderOpenCode)
	}
	if requests[1].ResumeID != "ses-old1" {
		t.Fatalf("second launch resume id = %q, want %q", requests[1].ResumeID, "ses-old1")
	}
	if requests[1].ForceNew {
		t.Fatalf("second launch request should not force a fresh session")
	}
	if len(requests[1].ReconnectTranscript) != 1 || requests[1].ReconnectTranscript[0].ItemID != "call_live_tool" {
		t.Fatalf("second launch reconnect transcript = %#v, want live transcript carried across helper restart", requests[1].ReconnectTranscript)
	}
}

func TestReconnectPreservesTodoCaptureContextForEveryEmbeddedProvider(t *testing.T) {
	for _, provider := range []codexapp.Provider{
		codexapp.ProviderCodex,
		codexapp.ProviderOpenCode,
		codexapp.ProviderClaudeCode,
		codexapp.ProviderLCAgent,
	} {
		t.Run(string(provider), func(t *testing.T) {
			cfg := config.Default()
			cfg.DataDir = t.TempDir()
			cfg.DBPath = filepath.Join(cfg.DataDir, "little-control-room.sqlite")
			cfg.EngineerTodoCaptureMode = todocapture.ModeExplicit
			svc := service.New(cfg, nil, events.NewBus(), nil)
			runtimeManager := projectrun.NewManager()
			t.Cleanup(func() { _ = runtimeManager.CloseAll() })

			var requests []codexapp.LaunchRequest
			manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, _ func()) (codexapp.Session, error) {
				requests = append(requests, req)
				return &fakeCodexSession{
					projectPath: req.ProjectPath,
					snapshot: codexapp.Snapshot{
						Provider: provider,
						Started:  true,
						ThreadID: "existing-thread",
						Preset:   req.Preset,
					},
				}, nil
			})
			if _, _, err := manager.Open(codexapp.LaunchRequest{
				ProjectPath: "/tmp/demo",
				Provider:    provider,
				ResumeID:    "existing-thread",
			}); err != nil {
				t.Fatal(err)
			}

			m := Model{
				svc:                 svc,
				codexManager:        manager,
				runtimeManager:      runtimeManager,
				codexVisibleProject: "/tmp/demo",
				appDataDirPath:      cfg.DataDir,
			}
			cmd := m.reconnectVisibleCodexSessionCmd()
			if cmd == nil {
				t.Fatal("reconnect command is nil")
			}
			opened, ok := cmd().(codexSessionOpenedMsg)
			if !ok {
				t.Fatal("reconnect command did not return codexSessionOpenedMsg")
			}
			if opened.err != nil {
				t.Fatal(opened.err)
			}
			if len(requests) != 2 {
				t.Fatalf("launch requests = %d, want 2", len(requests))
			}
			reconnect := requests[1]
			if reconnect.RuntimeManager != runtimeManager {
				t.Fatal("reconnect lost the shared runtime manager")
			}
			if reconnect.AppDBPath != cfg.DBPath {
				t.Fatalf("reconnect DB path = %q, want %q", reconnect.AppDBPath, cfg.DBPath)
			}
			if reconnect.TodoCaptureMode != todocapture.ModeExplicit {
				t.Fatalf("reconnect TODO capture mode = %q, want %q", reconnect.TodoCaptureMode, todocapture.ModeExplicit)
			}
			if reconnect.TodoCaptureHandler != svc {
				t.Fatal("reconnect lost the in-process TODO capture handler")
			}
			if reconnect.TodoCaptureSessionKey != "existing-thread" {
				t.Fatalf("reconnect TODO session key = %q, want existing thread", reconnect.TodoCaptureSessionKey)
			}
		})
	}
}

func TestVisibleCodexSlashStatusRunsLocally(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Started:  true,
			Preset:   codexcli.PresetYolo,
			Status:   "Codex session ready",
			ThreadID: "thread_demo",
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Preset:      codexcli.PresetYolo,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	input := newCodexTextarea()
	input.SetValue("/st")

	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          input,
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              24,
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("enter should run the embedded /status command")
	}
	if got.codexInput.Value() != "" {
		t.Fatalf("codex input should clear after /status, got %q", got.codexInput.Value())
	}
	if got.status != "Reading embedded Codex status..." {
		t.Fatalf("status = %q, want reading status notice", got.status)
	}

	msg := cmd()
	action, ok := msg.(codexActionMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want codexActionMsg", msg)
	}
	if action.err != nil {
		t.Fatalf("/status returned error = %v", action.err)
	}
	if session.statusCalls != 1 {
		t.Fatalf("status calls = %d, want 1", session.statusCalls)
	}
	if len(session.submissions) != 0 {
		t.Fatalf("/status should not submit a Codex prompt, submissions = %d", len(session.submissions))
	}
	rendered := ansi.Strip(got.renderCodexView())
	if !strings.Contains(rendered, "Status") || !strings.Contains(rendered, "85% left") {
		t.Fatalf("rendered view should include the local /status transcript block: %q", rendered)
	}
}

func TestVisibleLCAgentSlashPermissionsRunsLocally(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Provider: codexapp.ProviderLCAgent,
			Started:  true,
			Status:   "LCAgent ready",
			ThreadID: "lca_demo",
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Provider:    codexapp.ProviderLCAgent,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	input := newCodexTextarea()
	input.SetValue("/permissions")

	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          input,
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              24,
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("enter should run the embedded /permissions command")
	}
	if got.codexInput.Value() != "" {
		t.Fatalf("codex input should clear after /permissions, got %q", got.codexInput.Value())
	}
	msg := cmd()
	action, ok := msg.(codexActionMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want codexActionMsg", msg)
	}
	if action.err != nil {
		t.Fatalf("/permissions returned error = %v", action.err)
	}
	applied, _ := got.Update(action)
	appliedModel := applied.(Model)
	snapshot, ok := appliedModel.codexCachedSnapshot("/tmp/demo")
	if !ok {
		t.Fatalf("expected /permissions action to refresh the cached embedded snapshot")
	}
	if len(snapshot.Entries) == 0 || !strings.Contains(snapshot.Entries[len(snapshot.Entries)-1].Text, "Embedded LCAgent permissions") {
		t.Fatalf("expected /permissions action to echo permissions in the transcript, entries = %#v", snapshot.Entries)
	}
	if session.permissionCalls != 1 {
		t.Fatalf("permission calls = %d, want 1", session.permissionCalls)
	}
	if len(session.submissions) != 0 {
		t.Fatalf("/permissions should not submit a prompt, submissions = %d", len(session.submissions))
	}
}

func TestVisibleLCAgentSlashPermissionsRunsLocallyWhenSuggestionWouldResolveToArg(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Provider: codexapp.ProviderLCAgent,
			Started:  true,
			Status:   "LCAgent ready",
			ThreadID: "lca_demo",
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Provider:    codexapp.ProviderLCAgent,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	input := newCodexTextarea()
	input.SetValue("/permissions")

	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          input,
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              24,
		codexSlashSelected:  2,
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("enter should run the embedded /permissions command")
	}
	if got.codexInput.Value() != "" {
		t.Fatalf("codex input should clear after /permissions, got %q", got.codexInput.Value())
	}
	if got.status != "Reading embedded LCAgent permissions..." {
		t.Fatalf("status = %q, want reading notice", got.status)
	}
	msg := cmd()
	action, ok := msg.(codexActionMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want codexActionMsg", msg)
	}
	if action.err != nil {
		t.Fatalf("/permissions returned error = %v", action.err)
	}
	if session.permissionCalls != 1 {
		t.Fatalf("permission calls = %d, want 1", session.permissionCalls)
	}
	if len(session.permissionLevels) != 0 {
		t.Fatalf("permission level changes = %#v, want none", session.permissionLevels)
	}
	if len(session.submissions) != 0 {
		t.Fatalf("/permissions should not submit a prompt, submissions = %d", len(session.submissions))
	}
}

func TestVisibleLCAgentSlashPermissionsMediumSetsSessionLevel(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Provider: codexapp.ProviderLCAgent,
			Started:  true,
			Status:   "LCAgent ready",
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Provider:    codexapp.ProviderLCAgent,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	input := newCodexTextarea()
	input.SetValue("/permissions medium")

	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          input,
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              24,
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("enter should run the embedded /permissions medium command")
	}
	if got.status != "Setting embedded LCAgent permissions..." {
		t.Fatalf("status = %q, want setting notice", got.status)
	}
	msg := cmd()
	action, ok := msg.(codexActionMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want codexActionMsg", msg)
	}
	if action.err != nil {
		t.Fatalf("/permissions medium returned error = %v", action.err)
	}
	if len(session.permissionLevels) != 1 || session.permissionLevels[0] != "medium" {
		t.Fatalf("permission levels = %#v, want [medium]", session.permissionLevels)
	}
}

func TestVisibleCodexSlashGoalSetRunsLocally(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Started:  true,
			Preset:   codexcli.PresetYolo,
			Status:   "Codex session ready",
			ThreadID: "thread_demo",
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Preset:      codexcli.PresetYolo,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	input := newCodexTextarea()
	input.SetValue("/goal ship the change --budget 5000")

	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          input,
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              24,
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("enter should run the embedded /goal command")
	}
	if got.codexInput.Value() != "" {
		t.Fatalf("codex input should clear after /goal, got %q", got.codexInput.Value())
	}
	if got.status != "Setting embedded Codex goal..." {
		t.Fatalf("status = %q, want goal set notice", got.status)
	}

	msg := cmd()
	action, ok := msg.(codexActionMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want codexActionMsg", msg)
	}
	if action.err != nil {
		t.Fatalf("/goal returned error = %v", action.err)
	}
	if action.status != "Embedded Codex goal set" {
		t.Fatalf("action status = %q, want Embedded Codex goal set", action.status)
	}
	if session.goalSetObjective != "ship the change" {
		t.Fatalf("goal objective = %q, want ship the change", session.goalSetObjective)
	}
	if session.goalSetBudget == nil || *session.goalSetBudget != 5000 {
		t.Fatalf("goal budget = %v, want 5000", session.goalSetBudget)
	}
	if len(session.submissions) != 0 {
		t.Fatalf("/goal should not submit a Codex prompt, submissions = %d", len(session.submissions))
	}
}

func TestVisibleCodexSlashGoalStopRunsLocallyWhileBusy(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Started:      true,
			Preset:       codexcli.PresetYolo,
			Status:       "Codex is working...",
			ThreadID:     "thread_demo",
			Busy:         true,
			ActiveTurnID: "turn_demo",
			Goal: &codexapp.ThreadGoal{
				ThreadID:  "thread_demo",
				Objective: "stay paused",
				Status:    codexapp.ThreadGoalStatusActive,
			},
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Preset:      codexcli.PresetYolo,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	input := newCodexTextarea()
	input.SetValue("/goal stop")

	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          input,
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              24,
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("enter should run the embedded /goal stop command")
	}
	if got.codexInput.Value() != "" {
		t.Fatalf("codex input should clear after /goal stop, got %q", got.codexInput.Value())
	}
	if got.status != "Stopping embedded Codex goal..." {
		t.Fatalf("status = %q, want goal stop notice", got.status)
	}

	msg := cmd()
	action, ok := msg.(codexActionMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want codexActionMsg", msg)
	}
	if action.err != nil {
		t.Fatalf("/goal stop returned error = %v", action.err)
	}
	if action.status != "Embedded Codex goal stopped" {
		t.Fatalf("action status = %q, want Embedded Codex goal stopped", action.status)
	}
	if session.clearGoalCalls != 1 {
		t.Fatalf("clear goal calls = %d, want 1", session.clearGoalCalls)
	}
	if len(session.submissions) != 0 {
		t.Fatalf("/goal stop should not submit a Codex prompt, submissions = %d", len(session.submissions))
	}
}

func TestVisibleCodexSlashGoalResumeRunsLocally(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Started:  true,
			Preset:   codexcli.PresetYolo,
			Status:   "Codex session ready",
			ThreadID: "thread_demo",
			Goal: &codexapp.ThreadGoal{
				ThreadID:  "thread_demo",
				Objective: "stay paused",
				Status:    codexapp.ThreadGoalStatusPaused,
			},
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Preset:      codexcli.PresetYolo,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	input := newCodexTextarea()
	input.SetValue("/goal resume")

	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          input,
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              24,
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("enter should run the embedded /goal resume command")
	}
	if got.status != "Resuming embedded Codex goal..." {
		t.Fatalf("status = %q, want goal resume notice", got.status)
	}

	msg := cmd()
	action, ok := msg.(codexActionMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want codexActionMsg", msg)
	}
	if action.err != nil {
		t.Fatalf("/goal resume returned error = %v", action.err)
	}
	if action.status != "Embedded Codex goal resumed" {
		t.Fatalf("action status = %q, want Embedded Codex goal resumed", action.status)
	}
	if session.snapshot.Goal == nil || session.snapshot.Goal.Status != codexapp.ThreadGoalStatusActive {
		t.Fatalf("goal = %#v, want active goal", session.snapshot.Goal)
	}
}

func TestVisibleCodexSlashReviewRunsLocally(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Started:  true,
			Preset:   codexcli.PresetYolo,
			Status:   "Codex session ready",
			ThreadID: "thread_demo",
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Preset:      codexcli.PresetYolo,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	input := newCodexTextarea()
	input.SetValue("/rev")

	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          input,
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              24,
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("enter should run the embedded /review command")
	}
	if got.codexInput.Value() != "" {
		t.Fatalf("codex input should clear after /review, got %q", got.codexInput.Value())
	}
	if got.status != "Starting embedded Codex review..." {
		t.Fatalf("status = %q, want review start notice", got.status)
	}

	msg := cmd()
	action, ok := msg.(codexActionMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want codexActionMsg", msg)
	}
	if action.err != nil {
		t.Fatalf("/review returned error = %v", action.err)
	}
	if session.reviewCalls != 1 {
		t.Fatalf("review calls = %d, want 1", session.reviewCalls)
	}
	if len(session.submissions) != 0 {
		t.Fatalf("/review should not submit a Codex prompt, submissions = %d", len(session.submissions))
	}
}

func TestVisibleCodexSlashModelOpensPickerAndStagesSelection(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Started:         true,
			Preset:          codexcli.PresetYolo,
			Status:          "Codex session ready",
			ThreadID:        "thread_demo",
			Model:           "gpt-5",
			ReasoningEffort: "medium",
		},
		models: []codexapp.ModelOption{
			{
				ID:          "gpt-5",
				Model:       "gpt-5",
				DisplayName: "GPT-5",
				Description: "Balanced default",
				IsDefault:   true,
				SupportedReasoningEfforts: []codexapp.ReasoningEffortOption{
					{ReasoningEffort: "medium", Description: "Balanced"},
					{ReasoningEffort: "high", Description: "More deliberate"},
				},
				DefaultReasoningEffort: "medium",
			},
			{
				ID:          "gpt-5-codex",
				Model:       "gpt-5-codex",
				DisplayName: "GPT-5 Codex",
				Description: "Specialized coding model",
				SupportedReasoningEfforts: []codexapp.ReasoningEffortOption{
					{ReasoningEffort: "low", Description: "Fast"},
					{ReasoningEffort: "medium", Description: "Balanced"},
					{ReasoningEffort: "high", Description: "Thorough"},
				},
				DefaultReasoningEffort: "medium",
			},
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Preset:      codexcli.PresetYolo,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	input := newCodexTextarea()
	input.SetValue("/model")

	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          input,
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              28,
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("enter should open the embedded /model picker")
	}
	if got.codexInput.Value() != "" {
		t.Fatalf("codex input should clear after /model, got %q", got.codexInput.Value())
	}
	if !got.codexModelPickerVisible() || !got.codexModelPicker.Loading {
		t.Fatalf("model picker should enter loading state")
	}

	msg := cmd()
	listMsg, ok := msg.(codexModelListMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want codexModelListMsg", msg)
	}
	updated, _ = got.Update(listMsg)
	got = updated.(Model)
	if !got.codexModelPickerVisible() || got.codexModelPicker.Loading {
		t.Fatalf("model picker should be visible with loaded models")
	}
	if got.codexModelPicker.Focus != codexModelPickerFocusFilter {
		t.Fatalf("initial picker focus = %q, want filter", got.codexModelPicker.Focus)
	}

	updated, _ = got.updateCodexModelPickerMode(tea.KeyMsg{Type: tea.KeyTab})
	got = updated.(Model)
	if got.codexModelPicker.Focus != codexModelPickerFocusModels {
		t.Fatalf("picker focus after first tab = %q, want models", got.codexModelPicker.Focus)
	}
	updated, _ = got.updateCodexModelPickerMode(tea.KeyMsg{Type: tea.KeyDown})
	got = updated.(Model)
	if got.codexModelPicker.ModelIndex != 1 {
		t.Fatalf("model index after down = %d, want 1", got.codexModelPicker.ModelIndex)
	}
	updated, _ = got.updateCodexModelPickerMode(tea.KeyMsg{Type: tea.KeyTab})
	got = updated.(Model)
	if got.codexModelPicker.Focus != codexModelPickerFocusEfforts {
		t.Fatalf("picker focus after second tab = %q, want efforts", got.codexModelPicker.Focus)
	}
	updated, _ = got.updateCodexModelPickerMode(tea.KeyMsg{Type: tea.KeyDown})
	got = updated.(Model)
	updated, _ = got.updateCodexModelPickerMode(tea.KeyMsg{Type: tea.KeyDown})
	got = updated.(Model)
	if got.codexModelPicker.EffortIndex != 2 {
		t.Fatalf("effort index after two downs = %d, want 2 (high)", got.codexModelPicker.EffortIndex)
	}
	updated, cmd = got.updateCodexModelPickerMode(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if cmd == nil {
		t.Fatalf("enter should apply the selected model choice")
	}

	msg = cmd()
	action, ok := msg.(codexActionMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want codexActionMsg", msg)
	}
	if action.err != nil {
		t.Fatalf("/model returned error = %v", action.err)
	}
	if len(session.modelStages) != 1 {
		t.Fatalf("model stages = %d, want 1", len(session.modelStages))
	}
	if session.modelStages[0].Model != "gpt-5-codex" || session.modelStages[0].Reasoning != "high" {
		t.Fatalf("staged model = %#v, want gpt-5-codex + high", session.modelStages[0])
	}
}

func TestVisibleCodexSlashModelKeepsRecentSelectionWhenChoosingReasoning(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			ThreadID:         "thread-demo",
			Started:          true,
			Preset:           codexcli.PresetYolo,
			Status:           "Codex ready",
			Model:            "gpt-5",
			ReasoningEffort:  "medium",
			PendingModel:     "",
			PendingReasoning: "",
		},
		models: []codexapp.ModelOption{
			{
				ID:          "gpt-5",
				Model:       "gpt-5",
				DisplayName: "GPT-5",
				Description: "Balanced default",
				IsDefault:   true,
				SupportedReasoningEfforts: []codexapp.ReasoningEffortOption{
					{ReasoningEffort: "medium", Description: "Balanced"},
					{ReasoningEffort: "high", Description: "More deliberate"},
				},
				DefaultReasoningEffort: "medium",
			},
			{
				ID:          "gpt-5-codex",
				Model:       "gpt-5-codex",
				DisplayName: "GPT-5 Codex",
				Description: "Specialized coding model",
				SupportedReasoningEfforts: []codexapp.ReasoningEffortOption{
					{ReasoningEffort: "low", Description: "Fast"},
					{ReasoningEffort: "medium", Description: "Balanced"},
					{ReasoningEffort: "high", Description: "Thorough"},
				},
				DefaultReasoningEffort: "medium",
			},
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Preset:      codexcli.PresetYolo,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	input := newCodexTextarea()
	input.SetValue("/model")

	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          input,
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              28,
		recentCodexModels:   []string{"gpt-5-codex"},
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("enter should open the embedded /model picker")
	}

	msg := cmd()
	listMsg, ok := msg.(codexModelListMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want codexModelListMsg", msg)
	}
	updated, _ = got.Update(listMsg)
	got = updated.(Model)
	if !got.codexModelPickerVisible() || got.codexModelPicker.Loading {
		t.Fatalf("model picker should be visible with loaded models")
	}
	if len(got.codexModelPicker.RecentModels) != 1 || got.codexModelPicker.RecentModels[0].Model != "gpt-5-codex" {
		t.Fatalf("recent models = %#v, want only gpt-5-codex", got.codexModelPicker.RecentModels)
	}

	updated, _ = got.updateCodexModelPickerMode(tea.KeyMsg{Type: tea.KeyTab})
	got = updated.(Model)
	if got.codexModelPicker.Focus != codexModelPickerFocusRecent {
		t.Fatalf("picker focus after first tab = %q, want recent", got.codexModelPicker.Focus)
	}
	modelOption, ok := got.currentCodexModelOption()
	if !ok || modelOption.Model != "gpt-5-codex" {
		t.Fatalf("selected model after focusing recent = %#v, want gpt-5-codex", modelOption)
	}

	updated, _ = got.updateCodexModelPickerMode(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if got.codexModelPicker.Focus != codexModelPickerFocusEfforts {
		t.Fatalf("picker focus after enter on recent = %q, want efforts", got.codexModelPicker.Focus)
	}
	modelOption, ok = got.currentCodexModelOption()
	if !ok || modelOption.Model != "gpt-5-codex" {
		t.Fatalf("selected model after moving to efforts = %#v, want gpt-5-codex", modelOption)
	}

	updated, _ = got.updateCodexModelPickerMode(tea.KeyMsg{Type: tea.KeyDown})
	got = updated.(Model)
	updated, _ = got.updateCodexModelPickerMode(tea.KeyMsg{Type: tea.KeyDown})
	got = updated.(Model)
	if got.codexModelPicker.EffortIndex != 2 {
		t.Fatalf("effort index after two downs = %d, want 2 (high)", got.codexModelPicker.EffortIndex)
	}

	updated, cmd = got.updateCodexModelPickerMode(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if cmd == nil {
		t.Fatalf("enter should apply the selected recent model choice")
	}

	msg = cmd()
	action, ok := msg.(codexActionMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want codexActionMsg", msg)
	}
	if action.err != nil {
		t.Fatalf("/model returned error = %v", action.err)
	}
	if len(session.modelStages) != 1 {
		t.Fatalf("model stages = %d, want 1", len(session.modelStages))
	}
	if session.modelStages[0].Model != "gpt-5-codex" || session.modelStages[0].Reasoning != "high" {
		t.Fatalf("staged model = %#v, want gpt-5-codex + high", session.modelStages[0])
	}
}

func TestVisibleCodexSlashModelSkipsReasoningSelectionWhenUnsupported(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			ThreadID:         "thread-demo",
			Started:          true,
			Preset:           codexcli.PresetYolo,
			Status:           "Codex ready",
			Model:            "kimi-k2.7-code",
			ReasoningEffort:  "high",
			PendingModel:     "",
			PendingReasoning: "",
		},
		models: []codexapp.ModelOption{
			{
				ID:            "kimi-k2.7-code",
				Model:         "kimi-k2.7-code",
				ModelProvider: "moonshot",
				DisplayName:   "Balanced: Kimi K2.7 Code",
				Description:   "Direct Moonshot/Kimi coding route.",
				IsDefault:     true,
			},
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Preset:      codexcli.PresetYolo,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	input := newCodexTextarea()
	input.SetValue("/model")

	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          input,
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              28,
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("enter should open the embedded /model picker")
	}
	msg := cmd()
	listMsg, ok := msg.(codexModelListMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want codexModelListMsg", msg)
	}
	updated, _ = got.Update(listMsg)
	got = updated.(Model)
	if !got.codexModelPickerVisible() || got.codexModelPicker.Loading {
		t.Fatalf("model picker should be visible with loaded models")
	}
	if got.codexModelPicker.Focus != codexModelPickerFocusFilter {
		t.Fatalf("initial picker focus = %q, want filter", got.codexModelPicker.Focus)
	}

	updated, _ = got.updateCodexModelPickerMode(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if got.codexModelPicker.Focus != codexModelPickerFocusModels {
		t.Fatalf("picker focus after enter from filter = %q, want models", got.codexModelPicker.Focus)
	}
	updated, cmd = got.updateCodexModelPickerMode(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatalf("enter should apply the selected model without reasoning controls")
	}
	got = updated.(Model)
	msg = cmd()
	action, ok := msg.(codexActionMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want codexActionMsg", msg)
	}
	if action.err != nil {
		t.Fatalf("/model returned error = %v", action.err)
	}
	if len(session.modelStages) != 1 {
		t.Fatalf("model stages = %d, want 1", len(session.modelStages))
	}
	if got.codexModelPicker != nil {
		t.Fatalf("model picker should close after applying")
	}
	if session.modelStages[0].Model != "kimi-k2.7-code" {
		t.Fatalf("staged model = %q, want kimi-k2.7-code", session.modelStages[0].Model)
	}
	if session.modelStages[0].Reasoning != "" {
		t.Fatalf("staged reasoning = %q, want empty", session.modelStages[0].Reasoning)
	}
	if action.status != "Embedded model set to kimi-k2.7-code for the next prompt" {
		t.Fatalf("status = %q, want Embedded model set to kimi-k2.7-code for the next prompt", action.status)
	}
}

func TestVisibleCodexSlashModelArrowDownEntersRecentModels(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			ThreadID:        "thread-demo",
			Started:         true,
			Preset:          codexcli.PresetYolo,
			Status:          "Codex ready",
			Model:           "gpt-5",
			ReasoningEffort: "medium",
		},
		models: []codexapp.ModelOption{
			{
				ID:          "gpt-5",
				Model:       "gpt-5",
				DisplayName: "GPT-5",
				Description: "Balanced default",
				IsDefault:   true,
				SupportedReasoningEfforts: []codexapp.ReasoningEffortOption{
					{ReasoningEffort: "medium", Description: "Balanced"},
					{ReasoningEffort: "high", Description: "More deliberate"},
				},
				DefaultReasoningEffort: "medium",
			},
			{
				ID:          "gpt-5-codex",
				Model:       "gpt-5-codex",
				DisplayName: "GPT-5 Codex",
				Description: "Specialized coding model",
				SupportedReasoningEfforts: []codexapp.ReasoningEffortOption{
					{ReasoningEffort: "low", Description: "Fast"},
					{ReasoningEffort: "medium", Description: "Balanced"},
					{ReasoningEffort: "high", Description: "Thorough"},
				},
				DefaultReasoningEffort: "medium",
			},
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Preset:      codexcli.PresetYolo,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	input := newCodexTextarea()
	input.SetValue("/model")

	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          input,
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              28,
		recentCodexModels:   []string{"gpt-5", "gpt-5-codex"},
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("enter should open the embedded /model picker")
	}

	msg := cmd()
	listMsg, ok := msg.(codexModelListMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want codexModelListMsg", msg)
	}
	updated, _ = got.Update(listMsg)
	got = updated.(Model)
	if !got.codexModelPickerVisible() || got.codexModelPicker.Loading {
		t.Fatalf("model picker should be visible with loaded models")
	}
	if got.codexModelPicker.Focus != codexModelPickerFocusFilter {
		t.Fatalf("initial picker focus = %q, want filter", got.codexModelPicker.Focus)
	}

	updated, _ = got.updateCodexModelPickerMode(tea.KeyMsg{Type: tea.KeyDown})
	got = updated.(Model)
	if got.codexModelPicker.Focus != codexModelPickerFocusRecent {
		t.Fatalf("picker focus after first down = %q, want recent", got.codexModelPicker.Focus)
	}
	modelOption, ok := got.currentCodexModelOption()
	if !ok || modelOption.Model != "gpt-5" {
		t.Fatalf("selected model after entering recent = %#v, want gpt-5", modelOption)
	}

	updated, _ = got.updateCodexModelPickerMode(tea.KeyMsg{Type: tea.KeyDown})
	got = updated.(Model)
	if got.codexModelPicker.RecentIndex != 1 {
		t.Fatalf("recent index after second down = %d, want 1", got.codexModelPicker.RecentIndex)
	}
	modelOption, ok = got.currentCodexModelOption()
	if !ok || modelOption.Model != "gpt-5-codex" {
		t.Fatalf("selected model after moving within recent = %#v, want gpt-5-codex", modelOption)
	}
}

func TestRenderCodexModelPickerMarksRecentFocus(t *testing.T) {
	models := []codexapp.ModelOption{
		{
			ID:                     "gpt-5",
			Model:                  "gpt-5",
			DisplayName:            "GPT-5",
			DefaultReasoningEffort: "medium",
		},
		{
			ID:                     "gpt-5-codex",
			Model:                  "gpt-5-codex",
			DisplayName:            "GPT-5 Codex",
			DefaultReasoningEffort: "medium",
		},
	}
	m := Model{
		codexModelPicker: &codexModelPickerState{
			Models:         append([]codexapp.ModelOption(nil), models...),
			FilteredModels: append([]codexapp.ModelOption(nil), models...),
			RecentModels:   []codexapp.ModelOption{models[1]},
			SelectedModel:  "gpt-5-codex",
			ModelIndex:     1,
			RecentIndex:    0,
			Focus:          codexModelPickerFocusRecent,
		},
	}

	rendered := ansi.Strip(m.renderCodexModelPickerContent(80, 24))
	if !strings.Contains(rendered, "> GPT-5 Codex") {
		t.Fatalf("rendered picker should mark the focused recent row with >: %q", rendered)
	}
	if !strings.Contains(rendered, "* GPT-5 Codex") {
		t.Fatalf("rendered picker should keep the model list row as secondary selection: %q", rendered)
	}
}

func TestVisibleCodexSlashResumeOpensPickerAndLoadsChoices(t *testing.T) {
	modernFixture := filepath.Clean(filepath.Join("..", "..", "testdata", "codex_footprint", "sessions", "2026", "03", "05", "rollout-modern.jsonl"))
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Started:  true,
			Preset:   codexcli.PresetYolo,
			Status:   "Codex session ready",
			ThreadID: "thread-demo",
			Entries: []codexapp.TranscriptEntry{
				{Kind: codexapp.TranscriptUser, Text: "Current task title"},
				{Kind: codexapp.TranscriptAgent, Text: "Current session summary."},
			},
			LastActivityAt: time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC),
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Preset:      codexcli.PresetYolo,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	input := newCodexTextarea()
	input.SetValue("/resume")

	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          input,
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              28,
		detail: model.ProjectDetail{
			Summary: model.ProjectSummary{
				Path:                 "/tmp/demo",
				Name:                 "demo",
				PresentOnDisk:        true,
				LatestSessionID:      "thread-demo",
				LatestSessionFormat:  "modern",
				LatestSessionSummary: "Work appears complete for now.",
			},
			Sessions: []model.SessionEvidence{
				{
					SessionID:   "thread-demo",
					Format:      "modern",
					SessionFile: modernFixture,
					LastEventAt: time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC),
				},
				{
					SessionID:   "thread-old",
					Format:      "modern",
					SessionFile: modernFixture,
					LastEventAt: time.Date(2026, 3, 8, 11, 0, 0, 0, time.UTC),
				},
			},
		},
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("enter should open the embedded /resume picker")
	}
	if !got.codexPickerVisible || !got.codexPickerLoading || got.codexPickerKind != codexPickerKindResume {
		t.Fatalf("resume picker should enter loading state")
	}
	if got.status != "Loading Codex sessions for this project..." {
		t.Fatalf("status = %q, want loading embedded sessions notice", got.status)
	}
	if got.codexInput.Value() != "" {
		t.Fatalf("codex input should clear after /resume, got %q", got.codexInput.Value())
	}

	msg := cmd()
	listMsg, ok := msg.(codexResumeChoicesMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want codexResumeChoicesMsg", msg)
	}
	if listMsg.err != nil {
		t.Fatalf("/resume returned error = %v", listMsg.err)
	}

	updated, _ = got.Update(listMsg)
	got = updated.(Model)
	if !got.codexPickerVisible || got.codexPickerLoading || got.codexPickerKind != codexPickerKindResume {
		t.Fatalf("resume picker should remain visible with loaded choices")
	}
	if len(got.codexPickerChoices) != 2 {
		t.Fatalf("resume picker choices = %d, want 2", len(got.codexPickerChoices))
	}
	if !got.codexPickerChoices[0].Current {
		t.Fatalf("first resume choice should be marked current")
	}
	if got.codexPickerChoices[0].Title != "Current task title" {
		t.Fatalf("current choice title = %q, want live title", got.codexPickerChoices[0].Title)
	}
	if got.codexPickerChoices[0].Summary != "Work appears complete for now." {
		t.Fatalf("current choice summary = %q, want latest summary", got.codexPickerChoices[0].Summary)
	}

	rendered := ansi.Strip(got.View())
	for _, want := range []string{"Resume Codex Session", "CURRENT", "Current task title", "Work appears complete for now."} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("resume picker render missing %q: %q", want, rendered)
		}
	}
}

func TestVisibleCodexSlashSessionsOpensProjectHistoryPicker(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Started:  true,
			Status:   "Codex session ready",
			ThreadID: "thread-demo",
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{ProjectPath: "/tmp/demo"}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	input := newCodexTextarea()
	input.SetValue("/sessions")

	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          input,
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              24,
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("enter should open the embedded /sessions picker")
	}
	if !got.codexPickerVisible || !got.codexPickerLoading || got.codexPickerKind != codexPickerKindResume {
		t.Fatalf("/sessions should open the project-local resume picker, got visible=%v loading=%v kind=%q", got.codexPickerVisible, got.codexPickerLoading, got.codexPickerKind)
	}
	if got.codexPickerProject != "/tmp/demo" || got.codexPickerProvider != codexapp.ProviderCodex {
		t.Fatalf("picker scope = (%q, %q), want current project Codex", got.codexPickerProject, got.codexPickerProvider)
	}
	if got.status != "Loading Codex sessions for this project..." {
		t.Fatalf("status = %q, want loading embedded sessions notice", got.status)
	}
}

func TestBuildCodexResumeChoicesSkipsForkedSubagentSessions(t *testing.T) {
	parentFixture := filepath.Join(t.TempDir(), "parent.jsonl")
	if err := os.WriteFile(parentFixture, []byte(strings.Join([]string{
		`{"timestamp":"2026-03-14T06:27:12Z","type":"session_meta","payload":{"id":"thread-parent","cwd":"/tmp/demo"}}`,
		`{"timestamp":"2026-03-14T06:27:13Z","type":"event_msg","payload":{"type":"user_message","message":"Top-level conversation"}}`,
		`{"timestamp":"2026-03-14T06:27:14Z","type":"event_msg","payload":{"type":"agent_message","message":"Parent summary"}}`,
	}, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("write parent fixture: %v", err)
	}

	childFixture := filepath.Join(t.TempDir(), "child.jsonl")
	if err := os.WriteFile(childFixture, []byte(strings.Join([]string{
		`{"timestamp":"2026-03-14T09:32:01Z","type":"session_meta","payload":{"id":"thread-child","forked_from_id":"thread-parent","cwd":"/tmp/demo","agent_role":"explorer","source":{"subagent":{"thread_spawn":{"parent_thread_id":"thread-parent"}}}}}`,
		`{"timestamp":"2026-03-14T09:32:02Z","type":"event_msg","payload":{"type":"user_message","message":"Top-level conversation"}}`,
		`{"timestamp":"2026-03-14T09:32:03Z","type":"event_msg","payload":{"type":"agent_message","message":"Child summary"}}`,
	}, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("write child fixture: %v", err)
	}

	choices := buildCodexResumeChoices(context.Background(), model.ProjectDetail{
		Summary: model.ProjectSummary{
			Path:          "/tmp/demo",
			Name:          "demo",
			PresentOnDisk: true,
		},
		Sessions: []model.SessionEvidence{
			{
				SessionID:   "thread-parent",
				Format:      "modern",
				SessionFile: parentFixture,
				LastEventAt: time.Date(2026, 3, 14, 6, 27, 14, 0, time.UTC),
			},
			{
				SessionID:   "thread-child",
				Format:      "modern",
				SessionFile: childFixture,
				LastEventAt: time.Date(2026, 3, 14, 9, 32, 3, 0, time.UTC),
			},
		},
	}, codexapp.ProviderCodex)

	if len(choices) != 1 {
		t.Fatalf("resume picker choices = %d, want 1 after hiding forked subagent session", len(choices))
	}
	if choices[0].SessionID != "thread-parent" {
		t.Fatalf("remaining choice session id = %q, want thread-parent", choices[0].SessionID)
	}
}

func TestBuildCodexResumeChoicesAddsLCAgentContinuationHint(t *testing.T) {
	fixture := filepath.Join(t.TempDir(), "lcagent.jsonl")
	body := strings.Join([]string{
		`{"type":"session_meta","id":"lca_child","cwd":"/tmp/demo","started_at":"2026-05-12T10:00:00Z","parent_session_id":"lcaold","root_session_id":"lcaroot","continuation_depth":2,"continuation_reason":"continue_from","handoff_source":"final_handoff"}`,
		`{"type":"continuation","session_id":"lca_child","parent_session_id":"lcaold","root_session_id":"lcaroot","chain_depth":2,"continuation_reason":"continue_from","handoff_source":"final_handoff","pending_status":"missing_after_changes","pending_files":["README.md"],"parent_summary":"source lcaold; summary: previous work"}`,
		`{"type":"turn_complete","session_id":"lca_child","summary":"continued work","verification_status":"not_run"}`,
	}, "\n") + "\n"
	if err := os.WriteFile(fixture, []byte(body), 0o644); err != nil {
		t.Fatalf("write lcagent fixture: %v", err)
	}

	choices := buildCodexResumeChoices(context.Background(), model.ProjectDetail{
		Summary: model.ProjectSummary{
			Path:          "/tmp/demo",
			Name:          "demo",
			PresentOnDisk: true,
		},
		Sessions: []model.SessionEvidence{{
			SessionID:   "lca_child",
			Format:      "lcagent_jsonl",
			SessionFile: fixture,
			LastEventAt: time.Date(2026, 5, 12, 10, 0, 2, 0, time.UTC),
		}},
	}, codexapp.ProviderLCAgent)

	if len(choices) != 1 {
		t.Fatalf("resume picker choices = %d, want 1", len(choices))
	}
	for _, want := range []string{"trace quality:", "continued from lcaold", "depth 2", "pending verification missing_after_changes: README.md"} {
		if !strings.Contains(choices[0].TraceHint, want) {
			t.Fatalf("trace hint missing %q: %#v", want, choices[0])
		}
	}
	if !strings.HasPrefix(choices[0].TraceBadge, "Q") {
		t.Fatalf("trace badge = %q, want quality badge", choices[0].TraceBadge)
	}
	m := Model{codexPickerKind: codexPickerKindResume}
	if secondary := m.codexPickerSecondaryLabel(choices[0]); !strings.Contains(secondary, "pending verification missing_after_changes") {
		t.Fatalf("secondary label = %q, want continuation hint", secondary)
	}
	row := ansi.Strip(m.renderCodexPickerRow(choices[0], false, 96))
	if !strings.Contains(row, "LA     SAVE Q") {
		t.Fatalf("resume picker row should surface LCAgent trace quality badge: %q", row)
	}
}

func TestAddPickerProjectHintFallsBackToPathBase(t *testing.T) {
	m := Model{codexPickerKind: codexPickerKindGlobal}
	row := ansi.Strip(m.renderCodexPickerRow(codexSessionChoice{
		Provider:     codexapp.ProviderOpenCode,
		SessionID:    "thread-old",
		ProjectName:  "Demo App",
		LastActivity: time.Date(2026, 3, 13, 19, 56, 0, 0, time.UTC),
		Summary:      "OpenCode session restored.",
		ProjectPath:  "/tmp/demo",
	}, false, 100))
	if !strings.Contains(row, "[Demo App]") {
		t.Fatalf("project hint should be shown on global picker rows: %q", row)
	}

	row = ansi.Strip(m.renderCodexPickerRow(codexSessionChoice{
		Provider:     codexapp.ProviderOpenCode,
		SessionID:    "thread-old",
		LastActivity: time.Date(2026, 3, 13, 19, 56, 0, 0, time.UTC),
		Summary:      "OpenCode session restored.",
		ProjectPath:  "/tmp/demo",
	}, false, 100))
	if !strings.Contains(row, "[demo]") {
		t.Fatalf("project hint should show path base when name is missing: %q", row)
	}
}

func TestRenderCodexPickerRowUsesCompactSavedBadgeAndTitleInResumeMode(t *testing.T) {
	m := Model{codexPickerKind: codexPickerKindResume}
	row := ansi.Strip(m.renderCodexPickerRow(codexSessionChoice{
		Provider:     codexapp.ProviderCodex,
		SessionID:    "thread-old",
		LastActivity: time.Date(2026, 3, 13, 19, 56, 0, 0, time.UTC),
		Title:        "# AGENTS.md instructions for /Users/davide/dev/repos/FractalMech",
		Summary:      "Feature added: wheel plays a blip on character change, build passes; tuning offers optional next steps.",
	}, false, 96))

	if !strings.Contains(row, "CX     SAVE") {
		t.Fatalf("resume picker row should use the compact saved badge rail: %q", row)
	}
	if strings.Contains(row, "LAST") {
		t.Fatalf("resume picker row should not label every saved session as last: %q", row)
	}
	if !strings.Contains(row, "# AGENTS.md instructions") {
		t.Fatalf("resume picker row should surface the title in the list: %q", row)
	}
	if strings.Contains(row, "Feature added: wheel plays a blip") {
		t.Fatalf("resume picker row should no longer use the summary as the primary list preview: %q", row)
	}
}

func TestRenderCodexPickerRowMarksLatestSavedSessionInResumeMode(t *testing.T) {
	m := Model{codexPickerKind: codexPickerKindResume}
	row := ansi.Strip(m.renderCodexPickerRow(codexSessionChoice{
		Provider:     codexapp.ProviderCodex,
		SessionID:    "thread-latest",
		LastActivity: time.Date(2026, 3, 13, 19, 56, 0, 0, time.UTC),
		Summary:      "Latest saved session summary.",
		Latest:       true,
	}, false, 96))

	if !strings.Contains(row, "CX     LAST") {
		t.Fatalf("resume picker row should use the compact latest badge rail: %q", row)
	}
	if strings.Contains(row, "SAVE") {
		t.Fatalf("latest saved session should use the latest badge instead of saved: %q", row)
	}
}

func TestRenderCodexPickerRowAlignsResumeMetadataColumns(t *testing.T) {
	m := Model{codexPickerKind: codexPickerKindResume}
	at := time.Date(2026, 3, 13, 19, 56, 0, 0, time.UTC)
	ts := formatPickerActivity(at)

	shortRow := ansi.Strip(m.renderCodexPickerRow(codexSessionChoice{
		Provider:     codexapp.ProviderCodex,
		SessionID:    "thread-short",
		LastActivity: at,
		Summary:      "Short label",
	}, false, 96))

	longRow := ansi.Strip(m.renderCodexPickerRow(codexSessionChoice{
		Provider:     codexapp.ProviderCodex,
		SessionID:    "thread-longer",
		LastActivity: at,
		Summary:      "Selection highlight implemented, tests and validation passed after the footer refresh.",
	}, false, 96))

	shortIndex := strings.Index(shortRow, ts)
	longIndex := strings.Index(longRow, ts)
	if shortIndex < 0 || longIndex < 0 {
		t.Fatalf("expected both rows to include the activity timestamp %q: short=%q long=%q", ts, shortRow, longRow)
	}
	if shortIndex != longIndex {
		t.Fatalf("timestamp columns should align: short=%d long=%d shortRow=%q longRow=%q", shortIndex, longIndex, shortRow, longRow)
	}
}

func TestRenderCodexPickerRowKeepsCompactBadgeColumnAligned(t *testing.T) {
	m := Model{codexPickerKind: codexPickerKindResume}
	at := time.Date(2026, 3, 13, 19, 56, 0, 0, time.UTC)
	ts := formatPickerActivity(at)

	savedRow := ansi.Strip(m.renderCodexPickerRow(codexSessionChoice{
		Provider:     codexapp.ProviderCodex,
		SessionID:    "thread-saved",
		LastActivity: at,
		Title:        "Saved session title",
	}, false, 96))

	currentLiveRow := ansi.Strip(m.renderCodexPickerRow(codexSessionChoice{
		Provider:     codexapp.ProviderCodex,
		SessionID:    "thread-live",
		LastActivity: at,
		Title:        "Current live session title",
		Current:      true,
		Live:         true,
	}, false, 96))

	savedIndex := strings.Index(savedRow, ts)
	liveIndex := strings.Index(currentLiveRow, ts)
	if savedIndex < 0 || liveIndex < 0 {
		t.Fatalf("expected both rows to include the activity timestamp %q: saved=%q live=%q", ts, savedRow, currentLiveRow)
	}
	if savedIndex != liveIndex {
		t.Fatalf("compact badge rail should keep timestamp columns aligned: saved=%d live=%d savedRow=%q liveRow=%q", savedIndex, liveIndex, savedRow, currentLiveRow)
	}
	if !strings.Contains(currentLiveRow, "CX CUR LIVE") {
		t.Fatalf("current live row should use the compact current/live badge rail: %q", currentLiveRow)
	}
}

func TestCodexPickerWindowUsesAvailableTerminalHeight(t *testing.T) {
	m := Model{
		codexPickerKind:     codexPickerKindResume,
		codexPickerSelected: 0,
		codexPickerChoices: []codexSessionChoice{
			{Title: "First", Summary: "Summary"},
		},
	}

	start, end := m.codexPickerWindow(20, 30)
	if start != 0 {
		t.Fatalf("start = %d, want 0", start)
	}
	if visible := end - start; visible <= 5 {
		t.Fatalf("visible rows = %d, want more than the old fixed window", visible)
	}
}

func TestVisibleCodexSlashResumeIDOpensRequestedSession(t *testing.T) {
	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Started:  true,
				Preset:   req.Preset,
				Status:   "Codex session ready",
				ThreadID: req.ResumeID,
			},
		}, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Preset:      codexcli.PresetYolo,
		ResumeID:    "thread-demo",
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	input := newCodexTextarea()
	input.SetValue("/resume thread-old")

	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          input,
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              24,
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("enter should resume the requested embedded session")
	}
	if got.codexPendingOpen == nil {
		t.Fatalf("codexPendingOpen should be set while the requested session opens")
	}
	if !strings.Contains(got.status, "Opening embedded Codex session") || !strings.Contains(got.status, "thread-o") {
		t.Fatalf("status = %q, want requested session open notice", got.status)
	}

	msg := cmd()
	opened, ok := msg.(codexSessionOpenedMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want codexSessionOpenedMsg", msg)
	}
	if opened.err != nil {
		t.Fatalf("/resume thread-old returned error = %v", opened.err)
	}
	if len(requests) != 2 {
		t.Fatalf("launch requests = %d, want 2", len(requests))
	}
	if requests[1].ResumeID != "thread-old" {
		t.Fatalf("resume id = %q, want %q", requests[1].ResumeID, "thread-old")
	}
}
