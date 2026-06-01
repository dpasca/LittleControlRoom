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
