package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lcroom/internal/codexapp"
	"lcroom/internal/commands"
	"lcroom/internal/config"
	"lcroom/internal/events"
	"lcroom/internal/model"
	"lcroom/internal/service"
	"lcroom/internal/store"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestDispatchNewTaskCommandOpensProviderDialogBeforeCreate(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	cfg := config.Default()
	cfg.ScratchRoot = filepath.Join(t.TempDir(), "tasks")
	svc := service.New(cfg, st, events.NewBus(), nil)
	m := New(ctx, svc)

	updated, cmd := m.dispatchCommand(commands.Invocation{
		Kind:   commands.KindNewTask,
		Prompt: "answer Sarah about API docs",
	})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("/new-task should wait for provider confirmation")
	}
	if got.newTaskDialog == nil {
		t.Fatalf("/new-task should open the provider dialog")
	}
	if got.newTaskDialog.Provider != codexapp.ProviderCodex {
		t.Fatalf("default dialog provider = %q, want Codex", got.newTaskDialog.Provider)
	}
	if !strings.Contains(got.status, "New task dialog open") {
		t.Fatalf("status = %q, want dialog status", got.status)
	}

	updated, cmd = got.updateNewTaskDialogMode(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if cmd == nil {
		t.Fatalf("Enter should start scratch task creation")
	}
	if got.status != "Creating scratch task..." {
		t.Fatalf("status = %q, want creation status", got.status)
	}

	rawMsg := cmd()
	msg, ok := rawMsg.(newTaskResultMsg)
	if !ok {
		t.Fatalf("command returned %T, want newTaskResultMsg", rawMsg)
	}
	if msg.err != nil {
		t.Fatalf("create scratch task error: %v", msg.err)
	}
	if msg.result.TaskName != "answer Sarah about API docs" {
		t.Fatalf("task name = %q, want request-derived name", msg.result.TaskName)
	}

	updated, _ = got.applyNewTaskResultMsg(msg)
	got = updated.(Model)
	if got.newTaskDialog != nil {
		t.Fatalf("dialog should close after scratch task creation")
	}
	if got.preferredSelectPath != msg.result.TaskPath {
		t.Fatalf("preferredSelectPath = %q, want created task path %q", got.preferredSelectPath, msg.result.TaskPath)
	}
	if got.status != "Scratch task created and added to the list; Enter opens Codex" {
		t.Fatalf("status = %q, want created status", got.status)
	}
	if provider, ok := got.embeddedLaunchProviderOverride(msg.result.TaskPath); !ok || provider != codexapp.ProviderCodex {
		t.Fatalf("launch provider override = (%q, %v), want Codex true", provider, ok)
	}
}

func TestDispatchNewTaskCommandCanPreselectAssistant(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	cfg := config.Default()
	cfg.ScratchRoot = filepath.Join(t.TempDir(), "tasks")
	svc := service.New(cfg, st, events.NewBus(), nil)
	m := New(ctx, svc)

	updated, cmd := m.dispatchCommand(commands.Invocation{
		Kind:      commands.KindNewTask,
		Prompt:    "answer Sarah about API docs",
		Assistant: "opencode",
	})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("/new-task with assistant should wait for provider confirmation")
	}
	if got.newTaskDialog == nil {
		t.Fatalf("/new-task with assistant should open the provider dialog")
	}
	if got.newTaskDialog.Provider != codexapp.ProviderOpenCode {
		t.Fatalf("dialog provider = %q, want OpenCode", got.newTaskDialog.Provider)
	}

	updated, cmd = got.updateNewTaskDialogMode(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if cmd == nil {
		t.Fatalf("Enter should start scratch task creation")
	}

	rawMsg := cmd()
	msg, ok := rawMsg.(newTaskResultMsg)
	if !ok {
		t.Fatalf("command returned %T, want newTaskResultMsg", rawMsg)
	}
	if msg.err != nil {
		t.Fatalf("create scratch task error: %v", msg.err)
	}

	updated, _ = got.applyNewTaskResultMsg(msg)
	got = updated.(Model)
	provider, ok := got.embeddedLaunchProviderOverride(msg.result.TaskPath)
	if !ok || provider != codexapp.ProviderOpenCode {
		t.Fatalf("launch provider override = (%q, %v), want OpenCode true", provider, ok)
	}
	if got.preferredEmbeddedProviderForProject(model.ProjectSummary{Path: msg.result.TaskPath}) != codexapp.ProviderOpenCode {
		t.Fatalf("fresh scratch task should prefer the explicit assistant")
	}
	if !strings.Contains(got.status, "Enter opens OpenCode") {
		t.Fatalf("status = %q, want OpenCode launch hint", got.status)
	}
}

func TestNewTaskDialogDefaultsToLastUsedProvider(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	cfg := config.Default()
	cfg.ScratchRoot = filepath.Join(t.TempDir(), "tasks")
	svc := service.New(cfg, st, events.NewBus(), nil)
	m := New(ctx, svc)
	m.rememberEmbeddedProvider(codexapp.ProviderClaudeCode)

	updated, _ := m.dispatchCommand(commands.Invocation{
		Kind:   commands.KindNewTask,
		Prompt: "answer Sarah about API docs",
	})
	got := updated.(Model)
	if got.newTaskDialog == nil {
		t.Fatalf("/new-task should open the provider dialog")
	}
	if got.newTaskDialog.Provider != codexapp.ProviderClaudeCode {
		t.Fatalf("dialog provider = %q, want last-used Claude Code", got.newTaskDialog.Provider)
	}
	if got.newTaskDialog.ProviderDefaultLabel != "last used" {
		t.Fatalf("default label = %q, want last used", got.newTaskDialog.ProviderDefaultLabel)
	}
}

func TestNewTaskDialogUsesVerticalProviderSelection(t *testing.T) {
	t.Parallel()

	m := Model{
		newTaskDialog: &newTaskDialogState{
			Request:  "answer Sarah about API docs",
			Provider: codexapp.ProviderCodex,
		},
	}

	updated, cmd := m.updateNewTaskDialogMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("j should only move the agent selection")
	}
	if got.newTaskDialog.Provider != codexapp.ProviderOpenCode {
		t.Fatalf("provider after j = %q, want OpenCode", got.newTaskDialog.Provider)
	}

	updated, _ = got.updateNewTaskDialogMode(tea.KeyMsg{Type: tea.KeyDown})
	got = updated.(Model)
	if got.newTaskDialog.Provider != codexapp.ProviderClaudeCode {
		t.Fatalf("provider after down = %q, want Claude Code", got.newTaskDialog.Provider)
	}

	updated, _ = got.updateNewTaskDialogMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	got = updated.(Model)
	if got.newTaskDialog.Provider != codexapp.ProviderOpenCode {
		t.Fatalf("provider after k = %q, want OpenCode", got.newTaskDialog.Provider)
	}

	updated, _ = got.updateNewTaskDialogMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	got = updated.(Model)
	if got.newTaskDialog.Provider != codexapp.ProviderOpenCode {
		t.Fatalf("provider after a = %q, want unchanged OpenCode", got.newTaskDialog.Provider)
	}

	rendered := ansi.Strip(got.renderNewTaskContent(72))
	if !strings.Contains(rendered, "↑↓/j/k") {
		t.Fatalf("rendered dialog = %q, want vertical selection hint", rendered)
	}
	if strings.Contains(rendered, "a/A") {
		t.Fatalf("rendered dialog = %q, should not advertise a/A agent cycling", rendered)
	}
}

func TestNewTaskDialogLCAgentReadinessUsesSavedXiaomiKey(t *testing.T) {
	t.Parallel()

	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.LCAgentProvider = "xiaomi"
	settings.XiaomiAPIKey = "test-xiaomi-key"
	settings.XiaomiBaseURL = config.AIBackendXiaomi.DefaultOpenAICompatibleBaseURL()
	m := Model{
		settingsBaseline: &settings,
		newTaskDialog: &newTaskDialogState{
			Request:  "check Xiaomi-backed LC agent setup",
			Provider: codexapp.ProviderLCAgent,
		},
	}

	rendered := ansi.Strip(m.renderNewTaskContent(88))
	if !strings.Contains(rendered, "LCAgent - ready") {
		t.Fatalf("rendered dialog = %q, want ready LCAgent state", rendered)
	}
	if !strings.Contains(rendered, "Xiaomi API key saved") {
		t.Fatalf("rendered dialog = %q, want saved Xiaomi key detail", rendered)
	}
	if strings.Contains(rendered, "XIAOMI_API_KEY is not configured") {
		t.Fatalf("rendered dialog = %q, should not claim saved Xiaomi key is missing", rendered)
	}
}

func TestVisibleScratchTaskPromptAutoRenamesTemporaryTask(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	cfg := config.Default()
	cfg.ScratchRoot = filepath.Join(t.TempDir(), "tasks")
	svc := service.New(cfg, st, events.NewBus(), nil)
	created, err := svc.CreateScratchTask(ctx, service.CreateScratchTaskRequest{})
	if err != nil {
		t.Fatalf("CreateScratchTask() error = %v", err)
	}

	session := &fakeCodexSession{
		projectPath: created.TaskPath,
		snapshot: codexapp.Snapshot{
			Provider: codexapp.ProviderCodex,
			Started:  true,
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{ProjectPath: created.TaskPath}); err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	m := New(ctx, svc)
	m.codexManager = manager
	m.codexVisibleProject = created.TaskPath
	cmd := m.submitVisibleCodexCmd(codexDraft{Text: "Fix API docs login"})
	if cmd == nil {
		t.Fatalf("submitVisibleCodexCmd() returned nil")
	}
	rawMsg := cmd()
	msg, ok := rawMsg.(codexActionMsg)
	if !ok {
		t.Fatalf("command returned %T, want codexActionMsg", rawMsg)
	}
	if msg.err != nil {
		t.Fatalf("codex action error = %v", msg.err)
	}
	if !msg.renamedTask {
		t.Fatalf("renamedTask = false, want scratch task auto-rename")
	}

	detail, err := st.GetProjectDetail(ctx, created.TaskPath, 5)
	if err != nil {
		t.Fatalf("GetProjectDetail() error = %v", err)
	}
	if detail.Summary.Name != "Fix API docs login" {
		t.Fatalf("stored name = %q, want prompt title", detail.Summary.Name)
	}
	content, err := os.ReadFile(filepath.Join(created.TaskPath, "TASK.md"))
	if err != nil {
		t.Fatalf("read TASK.md: %v", err)
	}
	if got := string(content); !strings.Contains(got, "# Fix API docs login") {
		t.Fatalf("TASK.md = %q, want renamed heading", got)
	}
}

func TestVisibleScratchTaskPromptAutoRenameUsesTextAroundCollapsedPaste(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	cfg := config.Default()
	cfg.ScratchRoot = filepath.Join(t.TempDir(), "tasks")
	svc := service.New(cfg, st, events.NewBus(), nil)
	created, err := svc.CreateScratchTask(ctx, service.CreateScratchTaskRequest{})
	if err != nil {
		t.Fatalf("CreateScratchTask() error = %v", err)
	}

	session := &fakeCodexSession{
		projectPath: created.TaskPath,
		snapshot: codexapp.Snapshot{
			Provider: codexapp.ProviderCodex,
			Started:  true,
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{ProjectPath: created.TaskPath}); err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	pasted := strings.Repeat("raw log line that should not become the title\n", 9) + "raw log line that should not become the title"
	token := codexPastedTextComposerToken(1, pasted)
	draft := codexDraft{
		Text: token + " summarize the failing config",
		PastedTexts: []codexPastedText{
			{Token: token, Text: pasted},
		},
	}

	m := New(ctx, svc)
	m.codexManager = manager
	m.codexVisibleProject = created.TaskPath
	cmd := m.submitVisibleCodexCmd(draft)
	if cmd == nil {
		t.Fatalf("submitVisibleCodexCmd() returned nil")
	}
	rawMsg := cmd()
	msg, ok := rawMsg.(codexActionMsg)
	if !ok {
		t.Fatalf("command returned %T, want codexActionMsg", rawMsg)
	}
	if msg.err != nil {
		t.Fatalf("codex action error = %v", msg.err)
	}
	if !msg.renamedTask {
		t.Fatalf("renamedTask = false, want scratch task auto-rename")
	}
	if len(session.submissions) != 1 {
		t.Fatalf("submissions = %d, want 1", len(session.submissions))
	}
	if got := session.submissions[0].DisplayText; !strings.Contains(got, "[10 lines pasted]") {
		t.Fatalf("display text = %q, want collapsed paste placeholder", got)
	}

	detail, err := st.GetProjectDetail(ctx, created.TaskPath, 5)
	if err != nil {
		t.Fatalf("GetProjectDetail() error = %v", err)
	}
	if detail.Summary.Name != "summarize the failing config" {
		t.Fatalf("stored name = %q, want prompt text around paste", detail.Summary.Name)
	}
	if strings.Contains(detail.Summary.Name, "pasted") || strings.Contains(detail.Summary.Name, "raw log line") {
		t.Fatalf("stored name = %q, should not use paste placeholder or pasted body", detail.Summary.Name)
	}
}

func TestCodexDraftTitleTextFallsBackToExpandedPasteOnlyPrompt(t *testing.T) {
	t.Parallel()

	pasted := "Fix chart legend on mobile\nUse the attached reproduction notes"
	token := codexPastedTextComposerToken(1, pasted)
	draft := codexDraft{
		Text: token,
		PastedTexts: []codexPastedText{
			{Token: token, Text: pasted},
		},
	}

	got := draft.titleText()
	if got != pasted {
		t.Fatalf("title text = %q, want expanded paste text", got)
	}
	if strings.Contains(got, "pasted]") {
		t.Fatalf("title text = %q, should not use collapsed paste placeholder", got)
	}
}
