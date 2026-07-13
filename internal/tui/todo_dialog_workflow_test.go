package tui

import (
	"context"
	"fmt"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"lcroom/internal/aibackend"
	"lcroom/internal/codexapp"
	"lcroom/internal/codexcli"
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

func TestTodoDialogCopyDialogIncludesClaudeAndDefaultsToClaudeProvider(t *testing.T) {
	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider: req.Provider.Normalized(),
				ThreadID: "ses-claude",
				Started:  true,
				Preset:   req.Preset,
				Status:   req.Provider.Label() + " session ready",
			},
		}, nil
	})

	m := Model{
		codexManager: manager,
		projects: []model.ProjectSummary{{
			Path:                "/tmp/demo",
			Name:                "demo",
			PresentOnDisk:       true,
			LatestSessionFormat: "claude_code",
		}},
		detail: model.ProjectDetail{
			Summary: model.ProjectSummary{Path: "/tmp/demo"},
			Todos: []model.TodoItem{{
				ID:          9,
				ProjectPath: "/tmp/demo",
				Text:        "Investigate the TODO dialog launch provider list",
				WorktreeSuggestion: &model.TodoWorktreeSuggestion{
					Status:         model.TodoWorktreeSuggestionReady,
					BranchName:     "feat/todo-worktree-launch",
					WorktreeSuffix: "feat-todo-worktree-launch",
				},
			}},
		},
		selected:      0,
		todoDialog:    &todoDialogState{ProjectPath: "/tmp/demo", ProjectName: "demo"},
		codexInput:    newCodexTextarea(),
		codexDrafts:   make(map[string]codexDraft),
		codexViewport: viewport.New(0, 0),
		width:         100,
		height:        24,
	}

	updated, _ := m.updateTodoDialogMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if got.todoCopyDialog == nil {
		t.Fatalf("todo copy dialog should open after Enter")
	}
	if got.todoCopyDialog.Provider != codexapp.ProviderClaudeCode {
		t.Fatalf("copy dialog provider = %q, want %q", got.todoCopyDialog.Provider, codexapp.ProviderClaudeCode)
	}
	if got.todoCopyDialog.RunMode != todoCopyModeNewWorktree {
		t.Fatalf("copy dialog run mode = %d, want %d", got.todoCopyDialog.RunMode, todoCopyModeNewWorktree)
	}

	rendered := ansi.Strip(got.renderTodoCopyDialogOverlay("", 100, 24))
	if !strings.Contains(rendered, "Run in") {
		t.Fatalf("rendered copy dialog = %q, want run mode section", rendered)
	}
	if !strings.Contains(rendered, "Dedicated worktree") {
		t.Fatalf("rendered copy dialog = %q, want dedicated worktree option", rendered)
	}
	if !strings.Contains(rendered, "Claude Code") {
		t.Fatalf("rendered copy dialog = %q, want Claude Code provider option", rendered)
	}
	if !strings.Contains(rendered, "LCAgent") {
		t.Fatalf("rendered copy dialog = %q, want LCAgent provider option", rendered)
	}
	if !strings.Contains(rendered, "Run in  [w]") {
		t.Fatalf("rendered copy dialog = %q, want dedicated worktree hotkey hint", rendered)
	}
	if !strings.Contains(rendered, "Branch: feat/todo-worktree-launch") {
		t.Fatalf("rendered copy dialog = %q, want ready worktree branch details by default", rendered)
	}
	if !strings.Contains(rendered, "Agent  [a]") {
		t.Fatalf("rendered copy dialog = %q, want agent hotkey hint", rendered)
	}
	if !strings.Contains(rendered, "Options") {
		t.Fatalf("rendered copy dialog = %q, want options column", rendered)
	}
	if strings.Contains(rendered, "Options  [m]") {
		t.Fatalf("rendered copy dialog = %q, should not show options hotkey badge", rendered)
	}
	if !strings.Contains(rendered, "change model") {
		t.Fatalf("rendered copy dialog = %q, want model toggle row", rendered)
	}
	lines := strings.Split(rendered, "\n")
	foundEnterLine := false
	for _, line := range lines {
		if strings.Contains(line, "change model") && strings.Contains(line, "Enter") && strings.Contains(line, "start") {
			t.Fatalf("rendered copy dialog should keep Enter on its own action row, got %q", line)
		}
		if strings.Contains(line, "Enter") && strings.Contains(line, "start") {
			foundEnterLine = true
			if !strings.Contains(line, "Esc") || !strings.Contains(line, "cancel") {
				t.Fatalf("rendered copy dialog Enter row should also include Esc cancel, got %q", line)
			}
		}
	}
	if !foundEnterLine {
		t.Fatalf("rendered copy dialog should include a dedicated Enter/Esc action row, got %q", rendered)
	}

	updated, _ = got.updateTodoCopyDialogMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}})
	got = updated.(Model)
	if got.todoCopyDialog.RunMode != todoCopyModeHere {
		t.Fatalf("copy dialog run mode = %d, want %d after w", got.todoCopyDialog.RunMode, todoCopyModeHere)
	}

	updated, _ = got.updateTodoCopyDialogMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}})
	got = updated.(Model)
	rendered = ansi.Strip(got.renderTodoCopyDialogOverlay("", 100, 24))
	if !strings.Contains(rendered, "Branch: feat/todo-worktree-launch") {
		t.Fatalf("rendered copy dialog = %q, want ready worktree branch details", rendered)
	}
	if !strings.Contains(rendered, "Source: cached AI suggestion") {
		t.Fatalf("rendered copy dialog = %q, want worktree suggestion source details", rendered)
	}
	if !strings.Contains(rendered, "Path: /tmp/demo--feat-todo-worktree-launch") {
		t.Fatalf("rendered copy dialog = %q, want suggested worktree path details", rendered)
	}

	updated, _ = got.updateTodoCopyDialogMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}})
	got = updated.(Model)
	if got.todoCopyDialog.RunMode != todoCopyModeHere {
		t.Fatalf("copy dialog run mode = %d, want %d before Enter", got.todoCopyDialog.RunMode, todoCopyModeHere)
	}

	updated, cmd := got.updateTodoCopyDialogMode(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if got.codexPendingOpen == nil || got.codexPendingOpen.provider != codexapp.ProviderClaudeCode {
		t.Fatalf("codexPendingOpen = %#v, want pending Claude Code session", got.codexPendingOpen)
	}
	if cmd == nil {
		t.Fatalf("starting the Claude TODO flow should return an open command")
	}

	msg := cmd()
	opened, ok := msg.(codexSessionOpenedMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want codexSessionOpenedMsg", msg)
	}
	if opened.err != nil {
		t.Fatalf("todo launch returned error = %v", opened.err)
	}
	if len(requests) != 1 {
		t.Fatalf("request count = %d, want 1", len(requests))
	}
	if requests[0].Provider != codexapp.ProviderClaudeCode || !requests[0].ForceNew {
		t.Fatalf("launch request = %#v, want fresh Claude Code launch", requests[0])
	}
}

func TestTodoCopyDialogShowsProviderReadinessWarnings(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "")

	settings := config.EditableSettingsFromAppConfig(config.Default())
	m := Model{
		setupChecked: true,
		setupSnapshot: aibackend.Snapshot{
			Codex: aibackend.Status{
				Backend:       config.AIBackendCodex,
				Label:         "Codex",
				Installed:     true,
				Authenticated: false,
				Ready:         false,
				Detail:        "Codex installed, but not logged in.",
				LoginHint:     "Run `codex login`, then press r to refresh.",
			},
			OpenCode: aibackend.Status{
				Backend:   config.AIBackendOpenCode,
				Label:     "OpenCode",
				Installed: false,
				Ready:     false,
				Detail:    "OpenCode CLI is not installed.",
				LoginHint: "Install OpenCode, then refresh.",
			},
			Claude: aibackend.Status{
				Backend:       config.AIBackendClaude,
				Label:         "Claude Code",
				Installed:     true,
				Authenticated: false,
				Ready:         false,
				Detail:        "Claude Code installed, but not logged in.",
				LoginHint:     "Run `claude auth login`, then press r to refresh.",
			},
		},
		settingsBaseline: &settings,
		todoCopyDialog: &todoCopyDialogState{
			ProjectPath: "/tmp/demo",
			ProjectName: "demo",
			TodoID:      9,
			TodoText:    "Investigate unavailable agent providers",
			RunMode:     todoCopyModeHere,
			Provider:    codexapp.ProviderLCAgent,
		},
		width:  132,
		height: 32,
	}

	rendered := ansi.Strip(m.renderTodoCopyDialogOverlay("", 132, 32))
	for _, want := range []string{
		"Codex",
		"needs login",
		"OpenCode",
		"not installed",
		"Claude Code",
		"LCAgent",
		"needs key",
		"OPENROUTER_API_KEY is not configured",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered copy dialog = %q, want %q", rendered, want)
		}
	}
}

func TestTodoDialogEnterReopensPinnedLiveEngineerSession(t *testing.T) {
	rootPath := "/tmp/repo"
	worktreePath := "/tmp/repo--feat-pinned"
	session := &fakeCodexSession{
		projectPath: worktreePath,
		snapshot: codexapp.Snapshot{
			Provider:     codexapp.ProviderOpenCode,
			ThreadID:     "op-session-pinned",
			Started:      true,
			Busy:         true,
			ActiveTurnID: "turn-pinned",
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		Provider:    codexapp.ProviderOpenCode,
		ProjectPath: worktreePath,
		Preset:      codexcli.PresetYolo,
		ResumeID:    "op-session-pinned",
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	m := Model{
		codexManager: manager,
		allProjects: []model.ProjectSummary{
			{Path: rootPath, Name: "repo", PresentOnDisk: true},
			{Path: worktreePath, Name: "repo--feat-pinned", PresentOnDisk: true, WorktreeRootPath: rootPath, WorktreeKind: model.WorktreeKindLinked, WorktreeOriginTodoID: 42},
		},
		projects: []model.ProjectSummary{
			{Path: rootPath, Name: "repo", PresentOnDisk: true},
			{Path: worktreePath, Name: "repo--feat-pinned", PresentOnDisk: true, WorktreeRootPath: rootPath, WorktreeKind: model.WorktreeKindLinked, WorktreeOriginTodoID: 42},
		},
		detail: model.ProjectDetail{
			Summary: model.ProjectSummary{Path: rootPath},
			Todos: []model.TodoItem{{
				ID:              42,
				ProjectPath:     rootPath,
				Text:            "Use the pinned OpenCode lane",
				WorkProvider:    model.SessionSourceOpenCode,
				WorkProjectPath: worktreePath,
				WorkSessionID:   "opencode:op-session-pinned",
				WorkState:       model.TodoWorkStateWorking,
			}},
		},
		todoDialog:    &todoDialogState{ProjectPath: rootPath, ProjectName: "repo"},
		codexInput:    newCodexTextarea(),
		codexDrafts:   make(map[string]codexDraft),
		codexViewport: viewport.New(0, 0),
		width:         100,
		height:        24,
	}

	updated, _ := m.updateTodoDialogMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if got.todoDialog != nil || got.todoCopyDialog != nil {
		t.Fatalf("pinned TODO Enter should reveal the session without opening the launcher, todo=%#v copy=%#v", got.todoDialog, got.todoCopyDialog)
	}
	if got.codexVisibleProject != worktreePath {
		t.Fatalf("codexVisibleProject = %q, want pinned worktree %q", got.codexVisibleProject, worktreePath)
	}
	if !strings.Contains(got.status, "pinned TODO #42 OpenCode session") || !strings.Contains(got.status, "working") {
		t.Fatalf("status = %q, want pinned OpenCode working status", got.status)
	}
}

func TestTodoDialogEnterIgnoresPinnedSessionWhenWorktreeIsMissing(t *testing.T) {
	rootPath := "/tmp/repo"
	worktreePath := "/tmp/repo--feat-gone"
	m := Model{
		allProjects: []model.ProjectSummary{
			{Path: rootPath, Name: "repo", PresentOnDisk: true},
			{Path: worktreePath, Name: "repo--feat-gone", PresentOnDisk: false, WorktreeRootPath: rootPath, WorktreeKind: model.WorktreeKindLinked, WorktreeOriginTodoID: 42},
		},
		projects: []model.ProjectSummary{
			{Path: rootPath, Name: "repo", PresentOnDisk: true},
			{Path: worktreePath, Name: "repo--feat-gone", PresentOnDisk: false, WorktreeRootPath: rootPath, WorktreeKind: model.WorktreeKindLinked, WorktreeOriginTodoID: 42},
		},
		detail: model.ProjectDetail{
			Summary: model.ProjectSummary{Path: rootPath},
			Todos: []model.TodoItem{{
				ID:              42,
				ProjectPath:     rootPath,
				Text:            "Restart after deleting the old worktree",
				WorkProvider:    model.SessionSourceCodex,
				WorkProjectPath: worktreePath,
				WorkSessionID:   "codex:thread-gone",
				WorkState:       model.TodoWorkStateWorking,
			}},
		},
		todoDialog: &todoDialogState{ProjectPath: rootPath, ProjectName: "repo"},
		width:      100,
		height:     24,
	}

	updated, cmd := m.updateTodoDialogMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("missing pinned worktree should fall through to the normal launcher without a command")
	}
	if got.todoDialog == nil || got.todoCopyDialog == nil {
		t.Fatalf("missing pinned worktree should open TODO launcher, todo=%#v copy=%#v", got.todoDialog, got.todoCopyDialog)
	}
	if got.todoCopyDialog.TodoID != 42 || got.todoCopyDialog.RunMode != todoCopyModeNewWorktree {
		t.Fatalf("copy dialog = %#v, want TODO #42 in new-worktree mode", got.todoCopyDialog)
	}
}

func TestTodoDialogEnterResumesPinnedEngineerSession(t *testing.T) {
	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider: req.Provider.Normalized(),
				ThreadID: req.ResumeID,
				Started:  true,
			},
		}, nil
	})
	projectPath := "/tmp/repo"
	m := Model{
		codexManager: manager,
		allProjects: []model.ProjectSummary{{
			Path:          projectPath,
			Name:          "repo",
			PresentOnDisk: true,
		}},
		projects: []model.ProjectSummary{{
			Path:          projectPath,
			Name:          "repo",
			PresentOnDisk: true,
		}},
		detail: model.ProjectDetail{
			Summary: model.ProjectSummary{Path: projectPath},
			Todos: []model.TodoItem{{
				ID:            77,
				ProjectPath:   projectPath,
				Text:          "Resume the pinned Claude session",
				WorkProvider:  model.SessionSourceClaudeCode,
				WorkSessionID: "claude_code:claude-session-77",
				WorkState:     model.TodoWorkStateIdle,
			}},
		},
		todoDialog:    &todoDialogState{ProjectPath: projectPath, ProjectName: "repo"},
		codexInput:    newCodexTextarea(),
		codexDrafts:   make(map[string]codexDraft),
		codexViewport: viewport.New(0, 0),
		width:         100,
		height:        24,
	}

	updated, cmd := m.updateTodoDialogMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("pinned TODO Enter should return a resume command")
	}
	if got.todoDialog != nil || got.todoCopyDialog != nil {
		t.Fatalf("pinned TODO resume should close TODO dialogs, todo=%#v copy=%#v", got.todoDialog, got.todoCopyDialog)
	}
	if got.codexPendingOpen == nil || got.codexPendingOpen.projectPath != projectPath || got.codexPendingOpen.provider != codexapp.ProviderClaudeCode {
		t.Fatalf("codexPendingOpen = %#v, want pending Claude Code resume for %q", got.codexPendingOpen, projectPath)
	}
	msg := cmd()
	opened, ok := msg.(codexSessionOpenedMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want codexSessionOpenedMsg", msg)
	}
	if opened.err != nil {
		t.Fatalf("resume returned error = %v", opened.err)
	}
	if len(requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(requests))
	}
	if requests[0].Provider != codexapp.ProviderClaudeCode || requests[0].ForceNew || requests[0].ResumeID != "claude-session-77" {
		t.Fatalf("resume request = %#v, want Claude Code resume of claude-session-77", requests[0])
	}
}

func TestTodoDialogEnterPinnedSessionBlocksDifferentLiveProvider(t *testing.T) {
	worktreePath := "/tmp/repo--pinned"
	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider:     req.Provider.Normalized(),
				ThreadID:     firstNonEmptyTrimmed(req.ResumeID, "claude-live"),
				Started:      true,
				Busy:         true,
				ActiveTurnID: "turn-live",
			},
		}, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		Provider:    codexapp.ProviderClaudeCode,
		ProjectPath: worktreePath,
		ResumeID:    "claude-live",
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	rootPath := "/tmp/repo"
	m := Model{
		codexManager: manager,
		allProjects: []model.ProjectSummary{
			{Path: rootPath, Name: "repo", PresentOnDisk: true},
			{Path: worktreePath, Name: "repo--pinned", PresentOnDisk: true, WorktreeRootPath: rootPath, WorktreeKind: model.WorktreeKindLinked, WorktreeOriginTodoID: 42},
		},
		projects: []model.ProjectSummary{
			{Path: rootPath, Name: "repo", PresentOnDisk: true},
			{Path: worktreePath, Name: "repo--pinned", PresentOnDisk: true, WorktreeRootPath: rootPath, WorktreeKind: model.WorktreeKindLinked, WorktreeOriginTodoID: 42},
		},
		detail: model.ProjectDetail{
			Summary: model.ProjectSummary{Path: rootPath},
			Todos: []model.TodoItem{{
				ID:              42,
				ProjectPath:     rootPath,
				Text:            "Resume pinned Codex without replacing Claude",
				WorkProvider:    model.SessionSourceCodex,
				WorkProjectPath: worktreePath,
				WorkSessionID:   "codex:codex-pinned",
				WorkState:       model.TodoWorkStateIdle,
			}},
		},
		todoDialog:    &todoDialogState{ProjectPath: rootPath, ProjectName: "repo"},
		codexInput:    newCodexTextarea(),
		codexDrafts:   make(map[string]codexDraft),
		codexViewport: viewport.New(0, 0),
		width:         100,
		height:        24,
	}

	updated, cmd := m.updateTodoDialogMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("different active provider should block pinned TODO reopen")
	}
	if got.todoDialog == nil {
		t.Fatalf("TODO dialog should stay open after the blocked pinned reopen")
	}
	if len(requests) != 1 {
		t.Fatalf("launch requests = %d, want only the seeded Claude session", len(requests))
	}
	if !strings.Contains(got.status, "active embedded Claude Code session") || !strings.Contains(got.status, "Codex") {
		t.Fatalf("status = %q, want cross-provider active-session block", got.status)
	}
}

func TestTodoDialogDedicatedWorktreeEnsuresMissingWorktreeSuggestion(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	projectPath := filepath.Join(root, "repo")
	runTUITestGit(t, "", "init", projectPath)

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})

	cfg := config.Default()
	cfg.AIBackend = config.AIBackendOpenAIAPI
	cfg.OpenAIAPIKey = "test-key"
	svc := service.New(cfg, st, events.NewBus(), nil)
	if _, err := svc.CreateOrAttachProject(ctx, service.CreateOrAttachProjectRequest{
		ParentPath: root,
		Name:       "repo",
	}); err != nil {
		t.Fatalf("track project: %v", err)
	}

	item, err := st.AddTodo(ctx, projectPath, "Launch this TODO in a new worktree")
	if err != nil {
		t.Fatalf("add todo: %v", err)
	}
	detail, err := st.GetProjectDetail(ctx, projectPath, 10)
	if err != nil {
		t.Fatalf("get project detail: %v", err)
	}

	m := Model{
		ctx: ctx,
		svc: svc,
		projects: []model.ProjectSummary{{
			Path:          projectPath,
			Name:          "repo",
			PresentOnDisk: true,
		}},
		detail:     detail,
		selected:   0,
		todoDialog: &todoDialogState{ProjectPath: projectPath, ProjectName: "repo"},
		width:      100,
		height:     24,
	}

	updated, cmd := m.updateTodoDialogMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if got.todoCopyDialog == nil {
		t.Fatalf("todo copy dialog should open after Enter")
	}
	if got.todoCopyDialog.RunMode != todoCopyModeNewWorktree {
		t.Fatalf("copy dialog run mode = %d, want %d by default", got.todoCopyDialog.RunMode, todoCopyModeNewWorktree)
	}
	if cmd == nil {
		t.Fatalf("opening the TODO launcher should ensure a missing worktree suggestion")
	}
	msg, ok := cmd().(todoActionMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want todoActionMsg", msg)
	}
	if msg.err != nil {
		t.Fatalf("todoActionMsg.err = %v, want nil", msg.err)
	}
	if msg.status != "Preparing worktree suggestion..." {
		t.Fatalf("todoActionMsg.status = %q, want preparing status", msg.status)
	}

	suggestion, err := st.GetTodoWorktreeSuggestion(ctx, item.ID)
	if err != nil {
		t.Fatalf("get todo worktree suggestion: %v", err)
	}
	if suggestion.Status != model.TodoWorktreeSuggestionQueued {
		t.Fatalf("suggestion.Status = %q, want %q", suggestion.Status, model.TodoWorktreeSuggestionQueued)
	}
}

func TestTodoDialogDedicatedWorktreeRetriesFailedWorktreeSuggestion(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	projectPath := filepath.Join(root, "repo")
	runTUITestGit(t, "", "init", projectPath)

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})

	cfg := config.Default()
	cfg.AIBackend = config.AIBackendOpenAIAPI
	cfg.OpenAIAPIKey = "test-key"
	svc := service.New(cfg, st, events.NewBus(), nil)
	if _, err := svc.CreateOrAttachProject(ctx, service.CreateOrAttachProjectRequest{
		ParentPath: root,
		Name:       "repo",
	}); err != nil {
		t.Fatalf("track project: %v", err)
	}

	item, err := st.AddTodo(ctx, projectPath, "Launch this TODO in a new worktree")
	if err != nil {
		t.Fatalf("add todo: %v", err)
	}
	queueTodoWorktreeSuggestionForTest(t, ctx, st, item.ID)
	suggestion, err := st.ClaimNextQueuedTodoWorktreeSuggestion(ctx, 0, 0)
	if err != nil {
		t.Fatalf("claim todo worktree suggestion: %v", err)
	}
	if failed, err := st.FailTodoWorktreeSuggestion(ctx, suggestion, "todo worktree suggestion missing branch_name"); err != nil {
		t.Fatalf("fail todo worktree suggestion: %v", err)
	} else if !failed {
		t.Fatalf("expected todo worktree suggestion to fail")
	}
	detail, err := st.GetProjectDetail(ctx, projectPath, 10)
	if err != nil {
		t.Fatalf("get project detail: %v", err)
	}

	m := Model{
		ctx: ctx,
		svc: svc,
		projects: []model.ProjectSummary{{
			Path:          projectPath,
			Name:          "repo",
			PresentOnDisk: true,
		}},
		detail:     detail,
		selected:   0,
		todoDialog: &todoDialogState{ProjectPath: projectPath, ProjectName: "repo"},
		width:      100,
		height:     24,
	}

	updated, cmd := m.updateTodoDialogMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if got.todoCopyDialog == nil {
		t.Fatalf("todo copy dialog should open after Enter")
	}
	if got.todoCopyDialog.RunMode != todoCopyModeNewWorktree {
		t.Fatalf("copy dialog run mode = %d, want %d by default", got.todoCopyDialog.RunMode, todoCopyModeNewWorktree)
	}
	if cmd == nil {
		t.Fatalf("opening the TODO launcher should retry a failed worktree suggestion")
	}
	msg, ok := cmd().(todoActionMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want todoActionMsg", msg)
	}
	if msg.err != nil {
		t.Fatalf("todoActionMsg.err = %v, want nil", msg.err)
	}
	if msg.status != "Preparing worktree suggestion..." {
		t.Fatalf("todoActionMsg.status = %q, want preparing status", msg.status)
	}

	retried, err := st.GetTodoWorktreeSuggestion(ctx, item.ID)
	if err != nil {
		t.Fatalf("get todo worktree suggestion: %v", err)
	}
	if retried.Status != model.TodoWorktreeSuggestionQueued {
		t.Fatalf("retried.Status = %q, want %q", retried.Status, model.TodoWorktreeSuggestionQueued)
	}
}

func TestTodoDialogCanStartSelectedTodoInNewWorktree(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	projectPath := filepath.Join(root, "repo")
	runTUITestGit(t, "", "init", projectPath)
	runTUITestGit(t, projectPath, "config", "user.name", "Little Control Room Tests")
	runTUITestGit(t, projectPath, "config", "user.email", "tests@example.com")
	if err := os.WriteFile(filepath.Join(projectPath, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write README.md: %v", err)
	}
	runTUITestGit(t, projectPath, "add", "README.md")
	runTUITestGit(t, projectPath, "commit", "-m", "initial commit")

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})

	svc := service.New(config.Default(), st, events.NewBus(), nil)
	if _, err := svc.CreateOrAttachProject(ctx, service.CreateOrAttachProjectRequest{
		ParentPath: root,
		Name:       "repo",
	}); err != nil {
		t.Fatalf("track project: %v", err)
	}

	todoText := "Launch this TODO in a new worktree\n\nUse log output:\nline 1\nline 2\n"
	item, err := svc.AddTodo(ctx, projectPath, todoText)
	if err != nil {
		t.Fatalf("add todo: %v", err)
	}
	queueTodoWorktreeSuggestionForTest(t, ctx, st, item.ID)
	suggestion, err := st.ClaimNextQueuedTodoWorktreeSuggestion(ctx, 0, 0)
	if err != nil {
		t.Fatalf("claim todo worktree suggestion: %v", err)
	}
	suggestion.BranchName = "feat/new-worktree-launch"
	suggestion.WorktreeSuffix = "feat-new-worktree-launch"
	suggestion.Kind = "feature"
	suggestion.Reason = "Implements new worktree launch."
	suggestion.Confidence = 0.91
	suggestion.Model = "test"
	if completed, err := st.CompleteTodoWorktreeSuggestion(ctx, suggestion); err != nil {
		t.Fatalf("complete todo worktree suggestion: %v", err)
	} else if !completed {
		t.Fatalf("expected todo worktree suggestion to complete")
	}

	detail, err := st.GetProjectDetail(ctx, projectPath, 10)
	if err != nil {
		t.Fatalf("get project detail: %v", err)
	}

	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider: req.Provider.Normalized(),
				ThreadID: "ses-worktree",
				Started:  true,
				Preset:   req.Preset,
				Status:   req.Provider.Label() + " session ready",
			},
		}, nil
	})

	m := Model{
		ctx:          ctx,
		svc:          svc,
		codexManager: manager,
		projects: []model.ProjectSummary{{
			Path:          projectPath,
			Name:          "repo",
			PresentOnDisk: true,
		}},
		detail:        detail,
		selected:      0,
		todoDialog:    &todoDialogState{ProjectPath: projectPath, ProjectName: "repo"},
		codexInput:    newCodexTextarea(),
		codexDrafts:   make(map[string]codexDraft),
		codexViewport: viewport.New(0, 0),
		width:         100,
		height:        24,
	}

	updated, _ := m.updateTodoDialogMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	updated, cmd := got.updateTodoCopyDialogMode(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if cmd == nil {
		t.Fatalf("starting the worktree TODO flow should return a command")
	}
	if got.todoDialog != nil {
		t.Fatalf("todo dialog should close as soon as the dedicated worktree launch starts")
	}
	if got.todoCopyDialog != nil {
		t.Fatalf("todo copy dialog should dismiss as soon as the dedicated worktree launch starts")
	}
	if got.status != todoWorktreePreparingStatus {
		t.Fatalf("status = %q, want immediate background-start message", got.status)
	}
	msg := cmd()
	launchMsg, ok := msg.(todoWorktreeLaunchMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want todoWorktreeLaunchMsg", msg)
	}
	if launchMsg.err != nil {
		t.Fatalf("worktree launch returned error = %v", launchMsg.err)
	}
	if launchMsg.todoText != todoText {
		t.Fatalf("todoWorktreeLaunchMsg.todoText = %q, want %q", launchMsg.todoText, todoText)
	}
	expectedPath := filepath.Join(root, "repo--feat-new-worktree-launch")
	if launchMsg.projectPath != expectedPath {
		t.Fatalf("worktree launch path = %q, want %q", launchMsg.projectPath, expectedPath)
	}

	updated, cmd = got.Update(launchMsg)
	got = updated.(Model)
	if got.codexPendingOpen == nil || got.codexPendingOpen.projectPath != expectedPath {
		t.Fatalf("codexPendingOpen = %#v, want pending open for %q", got.codexPendingOpen, expectedPath)
	}
	if got.codexPendingOpen.provider != codexapp.ProviderCodex {
		t.Fatalf("pending provider = %q, want %q", got.codexPendingOpen.provider, codexapp.ProviderCodex)
	}
	if !got.codexPendingOpen.newSession {
		t.Fatalf("codexPendingOpen.newSession = false, want true for dedicated worktree launch")
	}
	if got.codexVisible() {
		t.Fatalf("background worktree launch should stay hidden while the session is still opening")
	}
	draft, ok := got.todoLaunchDraftFor(expectedPath)
	if !ok {
		t.Fatalf("todoLaunchDraftFor(%q) missing", expectedPath)
	}
	if !draft.autoSubmit {
		t.Fatalf("todoLaunchDraftFor(%q) = %#v, want auto-submit enabled for background launch", expectedPath, draft)
	}
	if cmd == nil {
		t.Fatalf("handling todoWorktreeLaunchMsg should return an open command")
	}
	msgs := collectCmdMsgs(cmd)
	var opened codexSessionOpenedMsg
	foundOpen := false
	for _, msg := range msgs {
		if candidate, ok := msg.(codexSessionOpenedMsg); ok {
			opened = candidate
			foundOpen = true
			break
		}
	}
	if !foundOpen {
		t.Fatalf("cmd messages did not include codexSessionOpenedMsg: %#v", msgs)
	}
	if opened.err != nil {
		t.Fatalf("embedded session open returned error = %v", opened.err)
	}
	if len(requests) != 1 {
		t.Fatalf("request count = %d, want 1", len(requests))
	}
	if requests[0].ProjectPath != expectedPath {
		t.Fatalf("launch project path = %q, want %q", requests[0].ProjectPath, expectedPath)
	}
	if requests[0].Provider != codexapp.ProviderCodex || !requests[0].ForceNew {
		t.Fatalf("launch request = %#v, want a fresh Codex session", requests[0])
	}
	if requests[0].Prompt != todoText {
		t.Fatalf("launch prompt = %q, want %q", requests[0].Prompt, todoText)
	}

	updated, cmd = got.Update(opened)
	got = updated.(Model)
	if got.codexVisibleProject != "" {
		t.Fatalf("codexVisibleProject = %q, want background launch to stay hidden", got.codexVisibleProject)
	}
	if got.codexHiddenProject != expectedPath {
		t.Fatalf("codexHiddenProject = %q, want %q", got.codexHiddenProject, expectedPath)
	}
	if got.codexInput.Focused() {
		t.Fatalf("composer should not be focused for background worktree launches")
	}
	if got.status != opened.status {
		t.Fatalf("status = %q, want %q", got.status, opened.status)
	}
	if _, ok := got.todoLaunchDraftFor(expectedPath); ok {
		t.Fatalf("todoLaunchDraftFor(%q) should clear after the background session opens", expectedPath)
	}
	_ = cmd
}

func TestBackgroundTodoWorktreeLaunchDoesNotInterruptVisibleEngineerSession(t *testing.T) {
	const (
		visibleProjectPath = "/tmp/current-engineer"
		todoProjectPath    = "/tmp/root--background-todo"
	)

	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		snapshot := codexapp.Snapshot{
			Provider:    req.Provider.Normalized(),
			ProjectPath: req.ProjectPath,
			ThreadID:    "ses-background-todo",
			Started:     true,
			Status:      req.Provider.Label() + " session ready",
		}
		if req.ProjectPath == visibleProjectPath {
			snapshot.ThreadID = "ses-current-engineer"
			snapshot.Entries = []codexapp.TranscriptEntry{{
				Kind: codexapp.TranscriptAgent,
				Text: "Current engineer session stays visible",
			}}
		}
		return &fakeCodexSession{projectPath: req.ProjectPath, snapshot: snapshot}, nil
	})
	visibleSession, _, err := manager.Open(codexapp.LaunchRequest{
		Provider:    codexapp.ProviderOpenCode,
		ProjectPath: visibleProjectPath,
	})
	if err != nil {
		t.Fatalf("open visible engineer session: %v", err)
	}
	visibleSnapshot := visibleSession.Snapshot()

	m := Model{
		codexManager:        manager,
		codexVisibleProject: visibleProjectPath,
		codexHiddenProject:  visibleProjectPath,
		codexInput:          newCodexTextarea(),
		codexDrafts:         make(map[string]codexDraft),
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              24,
	}
	m.storeCodexSnapshot(visibleProjectPath, visibleSnapshot)
	m.syncCodexViewport(true)

	updated, cmd := m.Update(todoWorktreeLaunchMsg{
		projectPath: todoProjectPath,
		todoID:      42,
		todoText:    "Handle this TODO without stealing focus",
		provider:    codexapp.ProviderCodex,
	})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("background TODO launch should return an embedded open command")
	}
	if got.codexPendingOpen == nil || got.codexPendingOpen.showWhilePending {
		t.Fatalf("codexPendingOpen = %#v, want a hidden background open", got.codexPendingOpen)
	}
	if got.codexVisibleProject != visibleProjectPath {
		t.Fatalf("codexVisibleProject = %q, want current session %q", got.codexVisibleProject, visibleProjectPath)
	}
	if got.currentEmbeddedProvider() != codexapp.ProviderOpenCode {
		t.Fatalf("current provider = %q, want visible OpenCode session", got.currentEmbeddedProvider())
	}
	rendered := ansi.Strip(got.renderCodexView())
	if !strings.Contains(rendered, "Current engineer session stays visible") {
		t.Fatalf("hidden TODO launch replaced the visible transcript: %q", rendered)
	}
	if strings.Contains(rendered, "Starting a new embedded Codex session") {
		t.Fatalf("hidden TODO launch flashed its opening screen over the current session: %q", rendered)
	}

	updated, _ = got.updateCodexMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'l'}, Alt: true})
	got = updated.(Model)
	if got.codexDenseBlockMode != codexDenseBlockPreview {
		t.Fatalf("visible session input was captured by hidden TODO launch; block mode = %v", got.codexDenseBlockMode)
	}

	var opened codexSessionOpenedMsg
	foundOpen := false
	for _, msg := range collectCmdMsgs(cmd) {
		if candidate, ok := msg.(codexSessionOpenedMsg); ok {
			opened = candidate
			foundOpen = true
			break
		}
	}
	if !foundOpen {
		t.Fatalf("background TODO command did not return codexSessionOpenedMsg")
	}
	if opened.err != nil {
		t.Fatalf("background TODO open error = %v", opened.err)
	}

	updated, _ = got.Update(opened)
	got = updated.(Model)
	if got.codexVisibleProject != visibleProjectPath {
		t.Fatalf("codexVisibleProject = %q after open, want current session %q", got.codexVisibleProject, visibleProjectPath)
	}
	if got.codexHiddenProject != todoProjectPath {
		t.Fatalf("codexHiddenProject = %q, want completed TODO session %q", got.codexHiddenProject, todoProjectPath)
	}
	if rendered = ansi.Strip(got.renderCodexView()); !strings.Contains(rendered, "Current engineer session stays visible") {
		t.Fatalf("completed background TODO launch replaced the visible transcript: %q", rendered)
	}
}

func TestNormalModeEnterRevealsPendingTodoWorktreeLaunch(t *testing.T) {
	projectPath := "/tmp/root--feat-background-todo"
	m := Model{
		projects: []model.ProjectSummary{{
			Path:          projectPath,
			Name:          "root--feat-background-todo",
			PresentOnDisk: true,
		}},
		selected:    0,
		focusedPane: focusProjects,
		codexPendingOpen: &codexPendingOpenState{
			projectPath:      projectPath,
			provider:         codexapp.ProviderCodex,
			showWhilePending: false,
			newSession:       true,
		},
		width:  100,
		height: 24,
	}

	updated, cmd := m.updateNormalMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("enter should reveal the pending TODO launch, not queue a second open command")
	}
	if !got.codexVisible() {
		t.Fatalf("pending TODO launch should become visible after Enter")
	}
	if got.codexPendingOpen == nil || !got.codexPendingOpen.showWhilePending {
		t.Fatalf("codexPendingOpen = %#v, want visible pending open", got.codexPendingOpen)
	}
	rendered := ansi.Strip(got.renderCodexView())
	if !strings.Contains(rendered, "Starting a new embedded Codex session") || !strings.Contains(rendered, projectPath) {
		t.Fatalf("rendered pending view should show the starting session, got %q", rendered)
	}
	if strings.Contains(rendered, "previous embedded session") {
		t.Fatalf("rendered pending view should not imply a previous embedded session is settling, got %q", rendered)
	}
	got.spinnerFrame++
	animated := ansi.Strip(got.renderCodexView())
	if rendered == animated {
		t.Fatalf("rendered pending view should animate across spinner frames, got %q", rendered)
	}
}

func TestTodoWorktreeLaunchWithModelPickerKeepsPromptUnsentUntilModelChoice(t *testing.T) {
	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider: req.Provider.Normalized(),
				ThreadID: "ses-worktree-model",
				Started:  true,
				Preset:   req.Preset,
				Status:   req.Provider.Label() + " session ready",
			},
		}, nil
	})

	m := Model{
		codexManager:  manager,
		codexInput:    newCodexTextarea(),
		codexDrafts:   make(map[string]codexDraft),
		codexViewport: viewport.New(0, 0),
		projects: []model.ProjectSummary{{
			Path:          "/tmp/root",
			Name:          "root",
			PresentOnDisk: true,
		}},
		selected: 0,
		width:    100,
		height:   24,
	}

	updated, cmd := m.Update(todoWorktreeLaunchMsg{
		projectPath:    "/tmp/root--feat-model-pick",
		todoText:       "Review the TODO before sending it",
		provider:       codexapp.ProviderOpenCode,
		openModelFirst: true,
	})
	got := updated.(Model)
	draft, ok := got.todoLaunchDraftFor("/tmp/root--feat-model-pick")
	if !ok || !draft.openModelFirst {
		t.Fatalf("todoLaunchDraftFor(%q) = %#v, want open-model-first launch state", "/tmp/root--feat-model-pick", draft)
	}
	if !got.codexVisible() {
		t.Fatalf("model-picker launches should stay visible while the session is opening")
	}
	if got.codexDrafts["/tmp/root--feat-model-pick"].Text != "Review the TODO before sending it" {
		t.Fatalf("draft text = %q, want TODO text restored for the picker path", got.codexDrafts["/tmp/root--feat-model-pick"].Text)
	}
	if cmd == nil {
		t.Fatalf("worktree launch should return an embedded open command")
	}

	msgs := collectCmdMsgs(cmd)
	var opened codexSessionOpenedMsg
	foundOpen := false
	for _, msg := range msgs {
		if candidate, ok := msg.(codexSessionOpenedMsg); ok {
			opened = candidate
			foundOpen = true
			break
		}
	}
	if !foundOpen {
		t.Fatalf("cmd messages did not include codexSessionOpenedMsg: %#v", msgs)
	}
	if opened.err != nil {
		t.Fatalf("embedded open returned error = %v", opened.err)
	}
	if len(requests) != 1 {
		t.Fatalf("request count = %d, want 1", len(requests))
	}
	if requests[0].Provider != codexapp.ProviderOpenCode {
		t.Fatalf("provider = %q, want %q", requests[0].Provider, codexapp.ProviderOpenCode)
	}
	if requests[0].Prompt != "" {
		t.Fatalf("prompt = %q, want the TODO to stay unsent until after model selection", requests[0].Prompt)
	}

	updated, cmd = got.Update(opened)
	got = updated.(Model)
	if got.codexVisibleProject != "/tmp/root--feat-model-pick" {
		t.Fatalf("codexVisibleProject = %q, want the worktree session shown for model selection", got.codexVisibleProject)
	}
	if got.codexInput.Focused() {
		t.Fatalf("composer should not be focused while the model picker is opening")
	}
	if got.status != "Pick a model, then send the TODO draft." {
		t.Fatalf("status = %q, want model picker prompt", got.status)
	}
	if cmd == nil {
		t.Fatalf("session open should return the model picker command")
	}
}

func TestTodoWorktreeLaunchHandoffMentionsPreparedSubmodules(t *testing.T) {
	m := Model{
		codexManager: codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
			return &fakeCodexSession{}, nil
		}),
		codexInput:    newCodexTextarea(),
		codexDrafts:   make(map[string]codexDraft),
		codexViewport: viewport.New(0, 0),
		projects: []model.ProjectSummary{{
			Path:          "/tmp/root",
			Name:          "root",
			PresentOnDisk: true,
		}},
		selected: 0,
		width:    100,
		height:   24,
	}

	updated, cmd := m.Update(todoWorktreeLaunchMsg{
		projectPath:   "/tmp/root--feat-assets",
		todoText:      "Use the prepared assets",
		preparedPaths: []string{"Assets", "Shared/Data"},
		provider:      codexapp.ProviderCodex,
	})
	got := updated.(Model)
	if got.status != "Worktree ready; prepared 2 submodules; starting TODO session..." {
		t.Fatalf("status = %q, want prepared-submodule handoff", got.status)
	}
	if cmd == nil {
		t.Fatalf("worktree launch should return an embedded open command")
	}
}

func TestTodoWorktreeAutoSubmitUsesInitialInputForImageAttachments(t *testing.T) {
	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider: req.Provider.Normalized(),
				ThreadID: "ses-worktree-image",
				Started:  true,
				Preset:   req.Preset,
				Status:   req.Provider.Label() + " session ready",
			},
		}, nil
	})

	imagePath := "/tmp/todo-worktree-reference.png"
	m := Model{
		codexManager: manager,
		codexInput:   newCodexTextarea(),
		codexDrafts:  make(map[string]codexDraft),
		projects: []model.ProjectSummary{{
			Path:          "/tmp/root",
			Name:          "root",
			PresentOnDisk: true,
		}},
		selected: 0,
		width:    100,
		height:   24,
	}

	updated, cmd := m.Update(todoWorktreeLaunchMsg{
		projectPath: "/tmp/root--feat-image",
		todoID:      12,
		todoText:    "Use the attached reference screenshot",
		attachments: []model.TodoAttachment{{
			Kind: model.TodoAttachmentLocalImage,
			Path: imagePath,
		}},
		provider: codexapp.ProviderCodex,
	})
	got := updated.(Model)
	draft, ok := got.todoLaunchDraftFor("/tmp/root--feat-image")
	if !ok || !draft.autoSubmit {
		t.Fatalf("todoLaunchDraftFor(%q) = %#v, want auto-submit launch state", "/tmp/root--feat-image", draft)
	}
	if len(draft.attachments) != 1 || draft.attachments[0].Path != imagePath {
		t.Fatalf("launch draft attachments = %#v, want image path", draft.attachments)
	}
	if cmd == nil {
		t.Fatalf("worktree launch should return an embedded open command")
	}

	msgs := collectCmdMsgs(cmd)
	foundOpen := false
	for _, msg := range msgs {
		if opened, ok := msg.(codexSessionOpenedMsg); ok {
			foundOpen = true
			if opened.err != nil {
				t.Fatalf("embedded session open returned error = %v", opened.err)
			}
		}
	}
	if !foundOpen {
		t.Fatalf("cmd messages did not include codexSessionOpenedMsg: %#v", msgs)
	}
	if len(requests) != 1 {
		t.Fatalf("request count = %d, want 1", len(requests))
	}
	if requests[0].Prompt != "" {
		t.Fatalf("launch prompt = %q, want empty prompt when attachments use InitialInput", requests[0].Prompt)
	}
	if requests[0].InitialInput.Text != "Use the attached reference screenshot" {
		t.Fatalf("initial input text = %q, want TODO text", requests[0].InitialInput.Text)
	}
	if len(requests[0].InitialInput.Attachments) != 1 || requests[0].InitialInput.Attachments[0].Path != imagePath {
		t.Fatalf("initial input attachments = %#v, want image path", requests[0].InitialInput.Attachments)
	}
}

func TestTodoWorktreeLaunchWithModelPickerKeepsPerProjectLaunchStateAcrossOverlap(t *testing.T) {
	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		threadID := "ses-" + filepath.Base(req.ProjectPath)
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider: req.Provider.Normalized(),
				ThreadID: threadID,
				Started:  true,
				Preset:   req.Preset,
				Status:   req.Provider.Label() + " session ready",
			},
		}, nil
	})

	m := Model{
		codexManager:  manager,
		codexInput:    newCodexTextarea(),
		codexDrafts:   make(map[string]codexDraft),
		codexViewport: viewport.New(0, 0),
		projects: []model.ProjectSummary{{
			Path:          "/tmp/root",
			Name:          "root",
			PresentOnDisk: true,
		}},
		selected: 0,
		width:    100,
		height:   24,
	}

	updated, cmd := m.Update(todoWorktreeLaunchMsg{
		projectPath:    "/tmp/root--feat-a",
		todoText:       "TODO A",
		provider:       codexapp.ProviderOpenCode,
		openModelFirst: true,
	})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("first worktree launch should return an embedded open command")
	}
	msgs := collectCmdMsgs(cmd)
	if len(msgs) == 0 {
		t.Fatalf("first launch returned no command messages")
	}
	openedA, ok := msgs[0].(codexSessionOpenedMsg)
	if !ok {
		t.Fatalf("first cmd message = %T, want codexSessionOpenedMsg", msgs[0])
	}

	updated, cmd = got.Update(todoWorktreeLaunchMsg{
		projectPath:    "/tmp/root--feat-b",
		todoText:       "TODO B",
		provider:       codexapp.ProviderOpenCode,
		openModelFirst: true,
	})
	got = updated.(Model)
	if cmd == nil {
		t.Fatalf("second worktree launch should return an embedded open command")
	}
	msgs = collectCmdMsgs(cmd)
	if len(msgs) == 0 {
		t.Fatalf("second launch returned no command messages")
	}
	openedB, ok := msgs[0].(codexSessionOpenedMsg)
	if !ok {
		t.Fatalf("second cmd message = %T, want codexSessionOpenedMsg", msgs[0])
	}

	updated, cmd = got.Update(openedA)
	got = updated.(Model)
	if got.codexVisibleProject != "" {
		t.Fatalf("superseded first completion stole the visible pane: codexVisibleProject = %q", got.codexVisibleProject)
	}
	if got.codexPendingOpen == nil || got.codexPendingOpen.projectPath != "/tmp/root--feat-b" {
		t.Fatalf("pending open = %#v, want the newer worktree launch to remain active", got.codexPendingOpen)
	}
	if draft := got.codexDrafts["/tmp/root--feat-a"]; draft.Text != "TODO A" {
		t.Fatalf("background first-session draft = %#v, want TODO A preserved", draft)
	}
	msgs = collectCmdMsgs(cmd)
	for _, msg := range msgs {
		if typed, isList := msg.(codexModelListMsg); isList {
			t.Fatalf("superseded first completion opened a model picker for %q", typed.projectPath)
		}
	}

	updated, cmd = got.Update(openedB)
	got = updated.(Model)
	if got.codexVisibleProject != "/tmp/root--feat-b" {
		t.Fatalf("codexVisibleProject = %q, want %q after the second session opens", got.codexVisibleProject, "/tmp/root--feat-b")
	}
	if got.status != "Pick a model, then send the TODO draft." {
		t.Fatalf("status = %q, want model picker guidance for the second overlapping launch", got.status)
	}
	if cmd == nil {
		t.Fatalf("second overlapping session open should return the model picker command")
	}
	msgs = collectCmdMsgs(cmd)
	var listB codexModelListMsg
	ok = false
	for _, msg := range msgs {
		if typed, isList := msg.(codexModelListMsg); isList {
			listB = typed
			ok = true
			break
		}
	}
	if !ok {
		t.Fatalf("second model-picker cmd messages = %#v, want codexModelListMsg", msgs)
	}
	if listB.projectPath != "/tmp/root--feat-b" {
		t.Fatalf("second model-picker project = %q, want %q", listB.projectPath, "/tmp/root--feat-b")
	}
	if len(requests) != 2 {
		t.Fatalf("request count = %d, want 2", len(requests))
	}
}

func TestTodoCopyDialogEnterStartsImmediatelyWhileWorktreeSuggestionIsQueued(t *testing.T) {
	t.Parallel()

	m := Model{
		detail: model.ProjectDetail{
			Summary: model.ProjectSummary{Path: "/tmp/demo"},
			Todos: []model.TodoItem{{
				ID:          7,
				ProjectPath: "/tmp/demo",
				Text:        "Launch this TODO in a new worktree",
				WorktreeSuggestion: &model.TodoWorktreeSuggestion{
					Status: model.TodoWorktreeSuggestionQueued,
				},
			}},
		},
		todoDialog: &todoDialogState{ProjectPath: "/tmp/demo", ProjectName: "demo"},
		todoCopyDialog: &todoCopyDialogState{
			ProjectPath: "/tmp/demo",
			ProjectName: "demo",
			TodoID:      7,
			TodoText:    "Launch this TODO in a new worktree",
			RunMode:     todoCopyModeNewWorktree,
			Provider:    codexapp.ProviderCodex,
		},
		width:        100,
		height:       24,
		spinnerFrame: 2,
	}

	updated, cmd := m.updateTodoCopyDialogMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("queued worktree suggestion should still start the background launch")
	}
	if got.todoDialog != nil || got.todoCopyDialog != nil {
		t.Fatalf("dialogs should dismiss immediately, got todo=%#v copy=%#v", got.todoDialog, got.todoCopyDialog)
	}
	if got.status != todoWorktreePreparingStatus {
		t.Fatalf("status = %q, want immediate background-start message", got.status)
	}
	msg := cmd()
	launchMsg, ok := msg.(todoWorktreeLaunchMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want todoWorktreeLaunchMsg", msg)
	}
	if launchMsg.err == nil || launchMsg.err.Error() != "service unavailable" {
		t.Fatalf("launch error = %v, want service unavailable from the stubbed model", launchMsg.err)
	}
}

func TestUpdateTodoCopyDialogHandlesPointerModelReturn(t *testing.T) {
	t.Parallel()

	m := Model{
		todoCopyDialog: &todoCopyDialogState{
			ProjectPath: "/tmp/demo",
			ProjectName: "demo",
			TodoID:      7,
			TodoText:    "Launch this TODO in a new worktree",
			RunMode:     todoCopyModeNewWorktree,
			Provider:    codexapp.ProviderCodex,
		},
		width:  100,
		height: 24,
	}

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("missing TODO selection should not start a command")
	}
	if got.status != "No TODO selected" {
		t.Fatalf("status = %q, want %q", got.status, "No TODO selected")
	}
	if got.todoCopyDialog == nil {
		t.Fatalf("todo copy dialog should stay open after a blocked launch")
	}
}

func TestTodoCopyDialogBlocksDedicatedWorktreeWhileLaunchPending(t *testing.T) {
	t.Parallel()

	m := Model{
		detail: model.ProjectDetail{
			Summary: model.ProjectSummary{Path: "/tmp/demo"},
			Todos: []model.TodoItem{{
				ID:          8,
				ProjectPath: "/tmp/demo",
				Text:        "Start another TODO while the first worktree is still creating",
			}},
		},
		todoDialog: &todoDialogState{ProjectPath: "/tmp/demo", ProjectName: "demo"},
		todoCopyDialog: &todoCopyDialogState{
			ProjectPath: "/tmp/demo",
			ProjectName: "demo",
			TodoID:      8,
			TodoText:    "Start another TODO while the first worktree is still creating",
			RunMode:     todoCopyModeNewWorktree,
			Provider:    codexapp.ProviderCodex,
		},
		todoPendingLaunch: &todoPendingLaunchState{
			ID:          21,
			ProjectPath: "/tmp/demo",
			ProjectName: "demo",
			TodoID:      7,
			TodoText:    "Launch this TODO in a new worktree",
			Provider:    codexapp.ProviderCodex,
		},
		width:  100,
		height: 24,
	}

	updated, cmd := m.updateTodoCopyDialogMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("second dedicated worktree launch should not queue another command")
	}
	if got.todoPendingLaunch == nil || got.todoPendingLaunch.ID != 21 {
		t.Fatalf("pending launch = %#v, want original pending launch still tracked", got.todoPendingLaunch)
	}
	if got.todoCopyDialog == nil {
		t.Fatalf("copy dialog should stay open after blocked duplicate launch")
	}
	wantStatus := "TODO #7 worktree is already being created; wait for it to finish."
	if got.todoPendingLaunchDialog == nil {
		t.Fatalf("blocked duplicate launch should open a modal dialog")
	}
	if got.todoPendingLaunchDialog.Message != wantStatus {
		t.Fatalf("dialog message = %q, want %q", got.todoPendingLaunchDialog.Message, wantStatus)
	}
	if got.todoPendingLaunchDialog.AllowAbort {
		t.Fatalf("duplicate-launch modal should require acknowledgement only")
	}
}

func TestStartingTodoWorktreePreservesProjectListSelection(t *testing.T) {
	t.Parallel()

	rootPath := "/tmp/repo"
	otherPath := "/tmp/other"
	root := model.ProjectSummary{
		Path:             rootPath,
		Name:             "repo",
		PresentOnDisk:    true,
		WorktreeRootPath: rootPath,
		WorktreeKind:     model.WorktreeKindMain,
	}
	other := model.ProjectSummary{
		Path:          otherPath,
		Name:          "other",
		PresentOnDisk: true,
	}
	m := Model{
		allProjects: []model.ProjectSummary{root, other},
		detail: model.ProjectDetail{
			Summary: root,
			Todos: []model.TodoItem{{
				ID:          7,
				ProjectPath: rootPath,
				Text:        "Launch without moving the project selection",
			}},
		},
		todoDialog: &todoDialogState{
			ProjectPath: rootPath,
			ProjectName: "repo",
		},
		visibility: visibilityAllFolders,
		width:      100,
		height:     24,
	}
	m.rebuildProjectList(rootPath)

	updated, cmd := m.startSelectedTodoInNewWorktree(codexapp.ProviderCodex, false)
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("starting a TODO worktree should queue its background command")
	}
	if got.currentSelectedProjectPath() != rootPath {
		t.Fatalf("selected path after background worktree start = %q, want %q", got.currentSelectedProjectPath(), rootPath)
	}
	pending, ok := got.todoPendingLaunchProjectSummary()
	if !ok || got.indexByPath(pending.Path) < 0 {
		t.Fatalf("pending worktree row = %#v ok=%v, want a visible background row", pending, ok)
	}
	if got.currentSelectedProjectPath() == pending.Path {
		t.Fatalf("pending worktree row %q should not steal the project selection", pending.Path)
	}
}

func TestTodoWorktreeLaunchAndSessionActivationPreserveLatestProjectListSelection(t *testing.T) {
	t.Parallel()

	rootPath := "/tmp/repo"
	worktreePath := "/tmp/repo--feat-background"
	otherPath := "/tmp/other"
	root := model.ProjectSummary{
		Path:             rootPath,
		Name:             "repo",
		PresentOnDisk:    true,
		WorktreeRootPath: rootPath,
		WorktreeKind:     model.WorktreeKindMain,
	}
	other := model.ProjectSummary{
		Path:          otherPath,
		Name:          "other",
		PresentOnDisk: true,
	}
	m := Model{
		allProjects: []model.ProjectSummary{root, other},
		todoPendingLaunch: &todoPendingLaunchState{
			ID:          12,
			ProjectPath: rootPath,
			ProjectName: "repo",
			TodoID:      7,
			TodoText:    "Launch in the background",
			Provider:    codexapp.ProviderCodex,
		},
		codexManager: codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
			return &fakeCodexSession{projectPath: req.ProjectPath}, nil
		}),
		codexInput:    newCodexTextarea(),
		codexDrafts:   make(map[string]codexDraft),
		codexViewport: viewport.New(0, 0),
		visibility:    visibilityAllFolders,
		width:         100,
		height:        24,
	}
	m.rebuildProjectList(otherPath)

	updated, cmd := m.Update(todoWorktreeLaunchMsg{
		launchID:    12,
		projectPath: worktreePath,
		todoID:      7,
		todoText:    "Launch in the background",
		provider:    codexapp.ProviderCodex,
	})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("completed worktree creation should queue session open and refresh commands")
	}
	if got.currentSelectedProjectPath() != otherPath {
		t.Fatalf("selected path after worktree creation = %q, want latest user selection %q", got.currentSelectedProjectPath(), otherPath)
	}
	if got.preferredSelectPath != "" {
		t.Fatalf("preferred select path = %q, want no background selection override", got.preferredSelectPath)
	}

	worktree := model.ProjectSummary{
		Path:             worktreePath,
		Name:             "repo--feat-background",
		Status:           model.StatusActive,
		PresentOnDisk:    true,
		WorktreeRootPath: rootPath,
		WorktreeKind:     model.WorktreeKindLinked,
	}
	updated, _ = got.Update(projectsMsg{
		projects: []model.ProjectSummary{root, worktree, other},
	})
	got = updated.(Model)
	if got.currentSelectedProjectPath() != otherPath {
		t.Fatalf("selected path after active session refresh = %q, want %q", got.currentSelectedProjectPath(), otherPath)
	}
}

func TestProjectListShowsPendingTodoWorktreeLaunch(t *testing.T) {
	t.Parallel()

	canceled := false
	m := Model{
		allProjects: []model.ProjectSummary{{
			Path:          "/tmp/demo",
			Name:          "demo",
			PresentOnDisk: true,
			LastActivity:  time.Date(2026, 6, 30, 8, 0, 0, 0, time.UTC),
		}},
		todoPendingLaunch: &todoPendingLaunchState{
			ID:          21,
			ProjectPath: "/tmp/demo",
			ProjectName: "demo",
			TodoID:      7,
			TodoText:    "Launch this TODO in a new worktree",
			Provider:    codexapp.ProviderCodex,
			StartedAt:   time.Date(2026, 6, 30, 8, 1, 0, 0, time.UTC),
			Cancel:      func() { canceled = true },
		},
		nowFn:        func() time.Time { return time.Date(2026, 6, 30, 8, 3, 0, 0, time.UTC) },
		width:        100,
		height:       24,
		focusedPane:  focusProjects,
		spinnerFrame: 1,
	}

	pendingProject, ok := m.todoPendingLaunchProjectSummary()
	if !ok {
		t.Fatalf("pending launch should synthesize a project row")
	}
	m.rebuildProjectList(pendingProject.Path)
	if _, pending, ok := m.selectedProjectRow(); !ok || pending.Path != pendingProject.Path {
		t.Fatalf("selected project = %#v ok=%v, want pending row %q", pending, ok, pendingProject.Path)
	}
	row, _, _ := m.selectedProjectRow()
	if row.Kind != projectListRowPendingWorktree {
		t.Fatalf("selected row kind = %q, want pending worktree", row.Kind)
	}
	rendered := ansi.Strip(m.renderProjectList(180, 8))
	if !strings.Contains(rendered, "TODO #7") || !strings.Contains(rendered, "creating") || !strings.Contains(rendered, "preparing checkout") {
		t.Fatalf("project list = %q, want pending worktree placeholder", rendered)
	}
	footer := ansi.Strip(m.renderFooter(120))
	if strings.Contains(footer, "Creating TODO #7 worktree") {
		t.Fatalf("footer = %q, should not duplicate pending worktree launch feedback", footer)
	}

	updated, cmd := m.updateNormalMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("opening pending worktree status should not queue a command")
	}
	if got.todoPendingLaunchDialog == nil || !got.todoPendingLaunchDialog.AllowAbort {
		t.Fatalf("enter on pending row should open an abort-capable dialog, got %#v", got.todoPendingLaunchDialog)
	}

	updated, cmd = got.updateTodoPendingLaunchDialogMode(tea.KeyMsg{Type: tea.KeyTab})
	got = updated.(Model)
	if cmd != nil {
		t.Fatalf("switching pending worktree dialog focus should not queue a command")
	}
	updated, cmd = got.updateTodoPendingLaunchDialogMode(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if cmd != nil {
		t.Fatalf("aborting pending worktree should not queue a command")
	}
	if !canceled {
		t.Fatalf("abort should call the pending launch cancel function")
	}
	if got.todoPendingLaunch == nil || !got.todoPendingLaunch.Canceled {
		t.Fatalf("pending launch = %#v, want canceled state retained until command completion", got.todoPendingLaunch)
	}
	if got.todoPendingLaunchDialog != nil {
		t.Fatalf("pending launch dialog should close after abort")
	}

	m.todoPendingLaunch.Canceled = true
	m.rebuildProjectList("/tmp/demo")
	rendered = ansi.Strip(m.renderProjectList(180, 8))
	if strings.Contains(rendered, "TODO #7") {
		t.Fatalf("project list = %q, should hide canceled pending TODO worktree launch", rendered)
	}
}

func TestTodoCopyDialogSubmittingEscCancelsPendingLaunch(t *testing.T) {
	t.Parallel()

	canceled := false
	m := Model{
		todoCopyDialog: &todoCopyDialogState{
			ProjectPath: "/tmp/demo",
			ProjectName: "demo",
			TodoID:      7,
			TodoText:    "Launch this TODO in a new worktree",
			RunMode:     todoCopyModeNewWorktree,
			Provider:    codexapp.ProviderClaudeCode,
			LaunchID:    11,
			Submitting:  true,
		},
		todoPendingLaunch: &todoPendingLaunchState{
			ID:     11,
			Cancel: func() { canceled = true },
		},
	}

	updated, cmd := m.updateTodoCopyDialogMode(tea.KeyMsg{Type: tea.KeyEsc})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("canceling a pending TODO launch should not queue another command")
	}
	if !canceled {
		t.Fatalf("cancel should be called for the pending TODO launch")
	}
	if got.todoCopyDialog != nil {
		t.Fatalf("todo copy dialog should close after canceling a pending launch")
	}
	if got.todoPendingLaunch == nil || !got.todoPendingLaunch.Canceled {
		t.Fatalf("pending TODO launch should stay tracked as canceled until its completion message arrives")
	}
	if got.status != "Canceling TODO start..." {
		t.Fatalf("status = %q, want canceling status", got.status)
	}
}

func TestTodoCopyDialogSubmittingCtrlCCancelsPendingLaunch(t *testing.T) {
	t.Parallel()

	canceled := false
	m := Model{
		todoCopyDialog: &todoCopyDialogState{
			ProjectPath: "/tmp/demo",
			ProjectName: "demo",
			TodoID:      7,
			TodoText:    "Launch this TODO in a new worktree",
			RunMode:     todoCopyModeNewWorktree,
			Provider:    codexapp.ProviderClaudeCode,
			LaunchID:    12,
			Submitting:  true,
		},
		todoPendingLaunch: &todoPendingLaunchState{
			ID:     12,
			Cancel: func() { canceled = true },
		},
	}

	updated, cmd := m.updateTodoCopyDialogMode(tea.KeyMsg{Type: tea.KeyCtrlC})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("ctrl+c cancel should not queue another command")
	}
	if !canceled {
		t.Fatalf("ctrl+c should cancel the pending TODO launch")
	}
	if got.todoCopyDialog != nil {
		t.Fatalf("todo copy dialog should close after ctrl+c cancel")
	}
	if got.todoPendingLaunch == nil || !got.todoPendingLaunch.Canceled {
		t.Fatalf("pending TODO launch should remain marked canceled after ctrl+c")
	}
	if got.status != "Canceling TODO start..." {
		t.Fatalf("status = %q, want canceling status", got.status)
	}
}

func TestTodoWorktreeLaunchCanceledSkipsErrorReporting(t *testing.T) {
	t.Parallel()

	m := Model{
		todoPendingLaunch: &todoPendingLaunchState{
			ID:       15,
			Canceled: true,
		},
	}

	updated, cmd := m.Update(todoWorktreeLaunchMsg{
		launchID:    15,
		projectPath: "/tmp/demo",
		err:         context.Canceled,
	})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("canceled todo launch should not queue a follow-up command")
	}
	if got.todoPendingLaunch != nil {
		t.Fatalf("pending TODO launch should clear once the canceled result arrives")
	}
	if got.status != "TODO start canceled" {
		t.Fatalf("status = %q, want canceled status", got.status)
	}
	if len(got.errorLogEntries) != 0 {
		t.Fatalf("canceled todo launch should not add error log entries, got %#v", got.errorLogEntries)
	}
}

func TestTodoWorktreeLaunchErrorAfterBackgroundStartLeavesDialogsClosed(t *testing.T) {
	t.Parallel()

	m := Model{
		todoPendingLaunch: &todoPendingLaunchState{ID: 9},
	}

	updated, cmd := m.Update(todoWorktreeLaunchMsg{
		launchID: 9,
		err:      fmt.Errorf("create worktree failed"),
	})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("todo worktree launch error should not return a follow-up command")
	}
	if got.todoCopyDialog != nil {
		t.Fatalf("todo copy dialog should stay dismissed after the background launch fails")
	}
	if got.todoDialog != nil {
		t.Fatalf("todo dialog should stay dismissed after the background launch fails")
	}
	if got.status != "TODO launch failed (use /errors)" {
		t.Fatalf("status = %q, want launch error", got.status)
	}
	if len(got.errorLogEntries) == 0 || got.errorLogEntries[0].Message != "create worktree failed" {
		t.Fatalf("latest error log entry = %#v, want create worktree failed", got.errorLogEntries)
	}
}

func TestTodoDialogCanStartSelectedTodoInExistingWorktree(t *testing.T) {
	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider: req.Provider.Normalized(),
				ThreadID: "ses-existing-worktree",
				Started:  true,
				Preset:   req.Preset,
				Status:   req.Provider.Label() + " session ready",
			},
		}, nil
	})

	rootPath := "/tmp/repo"
	worktreePath := "/tmp/repo--feat-reuse-lane"
	m := Model{
		codexManager: manager,
		allProjects: []model.ProjectSummary{
			{
				Name:             "repo",
				Path:             rootPath,
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindMain,
				RepoBranch:       "master",
			},
			{
				Name:             "repo--feat-reuse-lane",
				Path:             worktreePath,
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindLinked,
				RepoBranch:       "feat/reuse-lane",
				RepoDirty:        true,
			},
		},
		detail: model.ProjectDetail{
			Summary: model.ProjectSummary{Path: rootPath},
			Todos: []model.TodoItem{{
				ID:          41,
				ProjectPath: rootPath,
				Text:        "Reuse an existing worktree for this TODO",
			}},
		},
		selected:      0,
		todoDialog:    &todoDialogState{ProjectPath: rootPath, ProjectName: "repo"},
		codexInput:    newCodexTextarea(),
		codexDrafts:   make(map[string]codexDraft),
		codexViewport: viewport.New(0, 0),
		width:         100,
		height:        24,
		sortMode:      sortByAttention,
		visibility:    visibilityAllFolders,
	}
	m.rebuildProjectList(rootPath)

	updated, _ := m.updateTodoDialogMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if got.todoCopyDialog == nil || got.todoCopyDialog.Provider != codexapp.ProviderCodex {
		t.Fatalf("todoCopyDialog = %#v, want Codex selected", got.todoCopyDialog)
	}
	rendered := ansi.Strip(got.renderTodoCopyDialogOverlay("", 100, 24))
	if !strings.Contains(rendered, "x for 1 existing worktree(s)") {
		t.Fatalf("rendered copy dialog = %q, want existing-worktree hint", rendered)
	}

	updated, cmd := got.updateTodoCopyDialogMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	got = updated.(Model)
	if cmd != nil {
		t.Fatalf("opening the existing worktree picker should not launch immediately")
	}
	if got.todoExistingWorktree == nil {
		t.Fatalf("existing worktree picker should open")
	}
	rendered = ansi.Strip(got.renderTodoExistingWorktreeOverlay("", 100, 24))
	if !strings.Contains(rendered, "feat/reuse-lane") {
		t.Fatalf("rendered existing worktree picker = %q, want branch label", rendered)
	}

	updated, cmd = got.updateTodoExistingWorktreeMode(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if got.codexPendingOpen == nil || got.codexPendingOpen.projectPath != worktreePath {
		t.Fatalf("codexPendingOpen = %#v, want pending open for %q", got.codexPendingOpen, worktreePath)
	}
	if cmd == nil {
		t.Fatalf("starting in an existing worktree should return an open command")
	}
	msg := cmd()
	opened, ok := msg.(codexSessionOpenedMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want codexSessionOpenedMsg", msg)
	}
	if opened.err != nil {
		t.Fatalf("existing worktree launch returned error = %v", opened.err)
	}
	if len(requests) != 1 {
		t.Fatalf("request count = %d, want 1", len(requests))
	}
	if requests[0].ProjectPath != worktreePath || requests[0].Provider != codexapp.ProviderCodex {
		t.Fatalf("launch request = %#v, want Codex launch in %q", requests[0], worktreePath)
	}
}

func TestCloseTodoDialogRequestsSelectedWorktreeDetailAfterRootTodoView(t *testing.T) {
	rootPath := "/tmp/repo"
	worktreePath := "/tmp/repo--todo-fix"
	m := Model{
		projects: []model.ProjectSummary{{
			Path:             worktreePath,
			Name:             "repo--todo-fix",
			WorktreeRootPath: rootPath,
			WorktreeKind:     model.WorktreeKindLinked,
		}},
		selected: 0,
		todoDialog: &todoDialogState{
			ProjectPath: rootPath,
			ProjectName: "repo",
		},
	}

	cmd := m.closeTodoDialog("TODO list closed")

	if m.todoDialog != nil {
		t.Fatalf("todo dialog should close, got %#v", m.todoDialog)
	}
	if cmd == nil {
		t.Fatal("closing a root TODO dialog from a linked worktree should refresh the selected detail view")
	}
}

func TestTodoDialogCopyDialogHotkeysChangeRunModeAndProvider(t *testing.T) {
	m := Model{
		projects: []model.ProjectSummary{{
			Path:                "/tmp/demo",
			Name:                "demo",
			PresentOnDisk:       true,
			LatestSessionFormat: "codex",
		}},
		detail: model.ProjectDetail{
			Summary: model.ProjectSummary{Path: "/tmp/demo"},
			Todos: []model.TodoItem{{
				ID:          9,
				ProjectPath: "/tmp/demo",
				Text:        "Try the TODO launcher hotkeys",
			}},
		},
		selected:      0,
		todoDialog:    &todoDialogState{ProjectPath: "/tmp/demo", ProjectName: "demo"},
		codexInput:    newCodexTextarea(),
		codexDrafts:   make(map[string]codexDraft),
		codexViewport: viewport.New(0, 0),
		width:         100,
		height:        24,
	}

	updated, _ := m.updateTodoDialogMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if got.todoCopyDialog == nil {
		t.Fatalf("todo copy dialog should open after Enter")
	}
	if got.todoCopyDialog.RunMode != todoCopyModeNewWorktree {
		t.Fatalf("copy dialog run mode = %d, want %d", got.todoCopyDialog.RunMode, todoCopyModeNewWorktree)
	}
	if got.todoCopyDialog.Provider != codexapp.ProviderCodex {
		t.Fatalf("copy dialog provider = %q, want %q", got.todoCopyDialog.Provider, codexapp.ProviderCodex)
	}

	updated, _ = got.updateTodoCopyDialogMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}})
	got = updated.(Model)
	if got.todoCopyDialog.RunMode != todoCopyModeHere {
		t.Fatalf("copy dialog run mode = %d, want %d after w", got.todoCopyDialog.RunMode, todoCopyModeHere)
	}

	updated, _ = got.updateTodoCopyDialogMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	got = updated.(Model)
	if got.todoCopyDialog.Provider != codexapp.ProviderOpenCode {
		t.Fatalf("copy dialog provider = %q, want %q after a", got.todoCopyDialog.Provider, codexapp.ProviderOpenCode)
	}

	updated, _ = got.updateTodoCopyDialogMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	got = updated.(Model)
	if !got.todoCopyDialog.OpenModelFirst {
		t.Fatalf("copy dialog should enable model toggle after m")
	}

	updated, _ = got.updateTodoCopyDialogMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'A'}})
	got = updated.(Model)
	if got.todoCopyDialog.Provider != codexapp.ProviderCodex {
		t.Fatalf("copy dialog provider = %q, want %q after A", got.todoCopyDialog.Provider, codexapp.ProviderCodex)
	}
}

func TestTodoDialogPurgeHotkeyOpensConfirmForCompletedItems(t *testing.T) {
	m := Model{
		detail: model.ProjectDetail{
			Summary: model.ProjectSummary{Path: "/tmp/demo"},
			Todos: []model.TodoItem{
				{
					ID:          1,
					ProjectPath: "/tmp/demo",
					Text:        "Keep this open",
				},
				{
					ID:          2,
					ProjectPath: "/tmp/demo",
					Text:        "Purge this done item",
					Done:        true,
				},
			},
		},
		todoDialog: &todoDialogState{ProjectPath: "/tmp/demo", ProjectName: "demo"},
		width:      100,
		height:     24,
	}

	updated, _ := m.updateTodoDialogMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	got := updated.(Model)
	if got.todoDeleteConfirm == nil {
		t.Fatalf("purge hotkey should open a confirmation dialog")
	}
	if got.todoDeleteConfirm.TodoID != 0 {
		t.Fatalf("purge confirmation todo id = %d, want bulk mode", got.todoDeleteConfirm.TodoID)
	}
	if got.todoDeleteConfirm.DoneCount != 1 {
		t.Fatalf("purge confirmation done count = %d, want 1", got.todoDeleteConfirm.DoneCount)
	}
	if got.todoDeleteConfirm.Selected != todoDeleteConfirmFocusKeep {
		t.Fatalf("default purge confirmation selection = %d, want keep", got.todoDeleteConfirm.Selected)
	}
	if got.status != "Confirm completed TODO purge" {
		t.Fatalf("status = %q, want purge confirmation", got.status)
	}

	rendered := ansi.Strip(got.renderTodoDeleteConfirmOverlay("", 100, 24))
	if !strings.Contains(rendered, "Purge Completed TODOs") {
		t.Fatalf("rendered purge confirmation should explain the bulk action, got %q", rendered)
	}
}

func TestTodoDialogPurgeHotkeyReportsWhenNothingIsCompleted(t *testing.T) {
	m := Model{
		detail: model.ProjectDetail{
			Summary: model.ProjectSummary{Path: "/tmp/demo"},
			Todos: []model.TodoItem{{
				ID:          1,
				ProjectPath: "/tmp/demo",
				Text:        "Still in progress",
			}},
		},
		todoDialog: &todoDialogState{ProjectPath: "/tmp/demo", ProjectName: "demo"},
	}

	updated, cmd := m.updateTodoDialogMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("purge hotkey should not start a command when nothing is completed")
	}
	if got.todoDeleteConfirm != nil {
		t.Fatalf("purge confirmation should stay closed when there is nothing to purge")
	}
	if got.status != "No completed TODOs to purge" {
		t.Fatalf("status = %q, want no-completed message", got.status)
	}
}

func TestTodoDeleteConfirmPurgeQueuesBulkRemoval(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	projectPath := filepath.Join(root, "repo")
	runTUITestGit(t, "", "init", projectPath)
	runTUITestGit(t, projectPath, "config", "user.name", "Little Control Room Tests")
	runTUITestGit(t, projectPath, "config", "user.email", "tests@example.com")

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	svc := service.New(config.Default(), st, events.NewBus(), nil)
	if _, err := svc.CreateOrAttachProject(ctx, service.CreateOrAttachProjectRequest{
		ParentPath: root,
		Name:       "repo",
	}); err != nil {
		t.Fatalf("track root project: %v", err)
	}

	if _, err := svc.AddTodo(ctx, projectPath, "Keep this open"); err != nil {
		t.Fatalf("add open todo: %v", err)
	}
	doneItem, err := svc.AddTodo(ctx, projectPath, "Purge this done item")
	if err != nil {
		t.Fatalf("add done todo: %v", err)
	}
	if err := svc.ToggleTodoDone(ctx, projectPath, doneItem.ID, true); err != nil {
		t.Fatalf("mark done todo: %v", err)
	}

	detail, err := st.GetProjectDetail(ctx, projectPath, 0)
	if err != nil {
		t.Fatalf("get project detail: %v", err)
	}

	m := Model{
		ctx:    ctx,
		svc:    svc,
		detail: detail,
		todoDialog: &todoDialogState{
			ProjectPath: projectPath,
			ProjectName: "repo",
		},
	}

	updated, _ := m.updateTodoDialogMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	got := updated.(Model)
	if got.todoDeleteConfirm == nil {
		t.Fatalf("purge hotkey should open the confirmation dialog")
	}

	updated, _ = got.updateTodoDeleteConfirmMode(tea.KeyMsg{Type: tea.KeyTab})
	got = updated.(Model)
	if got.todoDeleteConfirm.Selected != todoDeleteConfirmFocusDelete {
		t.Fatalf("purge confirmation should move focus to purge")
	}

	updated, cmd := got.updateTodoDeleteConfirmMode(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if got.status != "Purging completed TODOs..." {
		t.Fatalf("status = %q, want purge progress", got.status)
	}
	if cmd == nil {
		t.Fatalf("purge confirmation should queue a removal command")
	}
	rawMsg := cmd()
	msg, ok := rawMsg.(todoActionMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want todoActionMsg", rawMsg)
	}
	if msg.err != nil {
		t.Fatalf("todoActionMsg.err = %v, want nil", msg.err)
	}
	if msg.status != "Purged 1 completed TODO" {
		t.Fatalf("todoActionMsg.status = %q, want singular purge status", msg.status)
	}
}

func TestTodoEditorSaveDismissesImmediatelyAndClearsBusyOnSuccess(t *testing.T) {
	t.Parallel()

	const projectPath = "/tmp/demo"
	const todoText = "Codex may say model is at capacity. Switch to a higher model with lower reasoning?"

	m := Model{
		todoDialog: &todoDialogState{
			ProjectPath: projectPath,
			ProjectName: "demo",
		},
		todoEditor: &todoEditorState{
			ProjectPath: projectPath,
			ProjectName: "demo",
			Input:       newTodoTextInput(todoText),
		},
	}

	updated, cmd := m.updateTodoEditorMode(tea.KeyMsg{Type: tea.KeyCtrlS})
	got := updated.(Model)
	if cmd == nil {
		t.Fatal("ctrl+s should queue a save command")
	}
	if got.todoEditor != nil {
		t.Fatalf("todo editor should dismiss immediately after save, got %#v", got.todoEditor)
	}
	if got.todoPendingSave == nil {
		t.Fatal("todo save should remain tracked while the command is in flight")
	}
	if got.todoPendingSave.Text != todoText {
		t.Fatalf("pending todo text = %q, want %q", got.todoPendingSave.Text, todoText)
	}
	if got.todoDialog == nil || !got.todoDialog.Busy {
		t.Fatalf("todo dialog should mark itself busy while the save is in flight, got %#v", got.todoDialog)
	}
	if got.status != "Adding TODO..." {
		t.Fatalf("status = %q, want add progress", got.status)
	}

	updatedModel, followUp := got.Update(todoActionMsg{projectPath: projectPath, status: "TODO added"})
	got = updatedModel.(Model)
	if got.todoPendingSave != nil {
		t.Fatalf("pending todo save should clear after success, got %#v", got.todoPendingSave)
	}
	if got.todoDialog == nil || got.todoDialog.Busy {
		t.Fatalf("todo dialog busy flag should clear after save success, got %#v", got.todoDialog)
	}
	if got.todoEditor != nil {
		t.Fatalf("todo editor should stay dismissed after a successful save, got %#v", got.todoEditor)
	}
	if got.status != "TODO added" {
		t.Fatalf("status = %q, want success status", got.status)
	}
	if followUp == nil {
		t.Fatal("successful save should refresh project state")
	}
}

func TestTodoEditorSaveRestoresDraftOnFailure(t *testing.T) {
	t.Parallel()

	const projectPath = "/tmp/demo"
	const todoText = "Save should restore this draft after an error"

	m := Model{
		todoDialog: &todoDialogState{
			ProjectPath: projectPath,
			ProjectName: "demo",
		},
		todoEditor: &todoEditorState{
			ProjectPath: projectPath,
			ProjectName: "demo",
			Input:       newTodoTextInput(todoText),
		},
	}

	updated, cmd := m.updateTodoEditorMode(tea.KeyMsg{Type: tea.KeyCtrlS})
	got := updated.(Model)
	if cmd == nil {
		t.Fatal("ctrl+s should queue a save command")
	}

	rawMsg := cmd()
	action, ok := rawMsg.(todoActionMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want todoActionMsg", rawMsg)
	}
	if action.err == nil || action.err.Error() != "service unavailable" {
		t.Fatalf("todoActionMsg.err = %v, want service unavailable", action.err)
	}

	updatedModel, focusCmd := got.Update(action)
	got = updatedModel.(Model)
	if got.todoPendingSave != nil {
		t.Fatalf("pending todo save should clear after failure recovery, got %#v", got.todoPendingSave)
	}
	if got.todoDialog == nil || got.todoDialog.Busy {
		t.Fatalf("todo dialog busy flag should clear after save failure, got %#v", got.todoDialog)
	}
	if got.todoEditor == nil {
		t.Fatal("todo editor should reopen after save failure")
	}
	if got.todoEditor.Input.Value() != todoText {
		t.Fatalf("reopened todo text = %q, want %q", got.todoEditor.Input.Value(), todoText)
	}
	if got.todoEditor.Submitting {
		t.Fatalf("reopened todo editor should not stay submitting")
	}
	if focusCmd == nil {
		t.Fatal("reopening the todo editor after failure should refocus the input")
	}
	if !strings.Contains(got.status, "TODO action failed") {
		t.Fatalf("status = %q, want todo action failure hint", got.status)
	}
}

func TestTodoEditorCtrlVAttachesDurableClipboardImageAndBackspaceRemovesIt(t *testing.T) {
	dataDir := t.TempDir()
	sourcePath := filepath.Join(t.TempDir(), "clipboard.png")
	if err := os.WriteFile(sourcePath, []byte("fake png bytes"), 0o600); err != nil {
		t.Fatalf("write fake clipboard image: %v", err)
	}

	previousExporter := clipboardImageExporter
	clipboardImageExporter = func() (string, error) {
		return sourcePath, nil
	}
	t.Cleanup(func() {
		clipboardImageExporter = previousExporter
	})

	m := Model{
		appDataDirPath: dataDir,
		todoEditor: &todoEditorState{
			ProjectPath: "/tmp/demo",
			ProjectName: "demo",
			Input:       newTodoTextInput(""),
		},
	}

	updated, cmd := m.updateTodoEditorMode(tea.KeyMsg{Type: tea.KeyCtrlV})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("ctrl+v image attach should not queue a command")
	}
	if got.todoEditor == nil || len(got.todoEditor.Attachments) != 1 {
		t.Fatalf("attachments = %#v, want one image", got.todoEditor)
	}
	attachment := got.todoEditor.Attachments[0]
	if attachment.Kind != model.TodoAttachmentLocalImage {
		t.Fatalf("attachment kind = %q, want local image", attachment.Kind)
	}
	if !strings.HasPrefix(attachment.Path, filepath.Join(dataDir, "todo-attachments")+string(os.PathSeparator)) {
		t.Fatalf("attachment path = %q, want durable path under app data", attachment.Path)
	}
	if data, err := os.ReadFile(attachment.Path); err != nil || string(data) != "fake png bytes" {
		t.Fatalf("durable image bytes = %q, err=%v", string(data), err)
	}
	if got.status != "Attached [Image #1] to TODO" {
		t.Fatalf("status = %q, want attachment notice", got.status)
	}
	rendered := ansi.Strip(got.renderTodoEditorOverlay("", 100, 30))
	if !strings.Contains(rendered, "Images") || !strings.Contains(rendered, "[Image #1]") {
		t.Fatalf("editor render should show attachment label:\n%s", rendered)
	}

	updated, cmd = got.updateTodoEditorMode(tea.KeyMsg{Type: tea.KeyBackspace})
	got = updated.(Model)
	if cmd != nil {
		t.Fatalf("backspace image removal should not queue a command")
	}
	if got.todoEditor == nil || len(got.todoEditor.Attachments) != 0 {
		t.Fatalf("attachments after backspace = %#v, want none", got.todoEditor)
	}
	if got.status != "Removed [Image #1] from TODO" {
		t.Fatalf("status = %q, want removal notice", got.status)
	}
}

func TestTodoDialogSpaceBlocksRepeatWhileToggleIsInFlight(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	projectPath := filepath.Join(root, "repo")
	runTUITestGit(t, "", "init", projectPath)
	runTUITestGit(t, projectPath, "config", "user.name", "Little Control Room Tests")
	runTUITestGit(t, projectPath, "config", "user.email", "tests@example.com")

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	svc := service.New(config.Default(), st, events.NewBus(), nil)
	if _, err := svc.CreateOrAttachProject(ctx, service.CreateOrAttachProjectRequest{
		ParentPath: root,
		Name:       "repo",
	}); err != nil {
		t.Fatalf("track root project: %v", err)
	}

	if _, err := svc.AddTodo(ctx, projectPath, "Toggle this item"); err != nil {
		t.Fatalf("add todo: %v", err)
	}

	detail, err := st.GetProjectDetail(ctx, projectPath, 0)
	if err != nil {
		t.Fatalf("get project detail: %v", err)
	}

	m := Model{
		ctx:    ctx,
		svc:    svc,
		detail: detail,
		projects: []model.ProjectSummary{{
			Path: projectPath,
			Name: "repo",
		}},
		todoDialog: &todoDialogState{
			ProjectPath: projectPath,
			ProjectName: "repo",
		},
	}

	updated, cmd := m.updateTodoDialogMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	got := updated.(Model)
	if got.todoDialog == nil || !got.todoDialog.Busy {
		t.Fatalf("todo dialog should mark itself busy while the toggle command is in flight, got %#v", got.todoDialog)
	}
	if got.status != "Updating TODO..." {
		t.Fatalf("status = %q, want toggle progress", got.status)
	}
	if cmd == nil {
		t.Fatal("space toggle should queue a command")
	}

	updated, repeatCmd := got.updateTodoDialogMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	got = updated.(Model)
	if repeatCmd != nil {
		t.Fatalf("repeat toggle while busy should not queue another command")
	}
	if got.status != "TODO update already in progress" {
		t.Fatalf("status = %q, want busy warning", got.status)
	}

	rawMsg := cmd()
	action, ok := rawMsg.(todoActionMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want todoActionMsg", rawMsg)
	}
	updatedModel, followUp := got.Update(action)
	got = updatedModel.(Model)
	if got.todoDialog == nil || got.todoDialog.Busy {
		t.Fatalf("todo dialog busy flag should clear after the toggle completes, got %#v", got.todoDialog)
	}
	if followUp == nil {
		t.Fatal("completed toggle should refresh project state")
	}
}

func TestLaunchClaudeForSelectionUsesClaudeProvider(t *testing.T) {
	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider: req.Provider.Normalized(),
				ThreadID: "cc-demo",
				Started:  true,
				Preset:   req.Preset,
				Status:   req.Provider.Label() + " session ready",
			},
		}, nil
	})

	m := Model{
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

	updated, cmd := m.launchClaudeForSelection(true, "continue with the current task")
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("launchClaudeForSelection should return an open command")
	}
	if got.codexPendingOpen == nil || got.codexPendingOpen.provider != codexapp.ProviderClaudeCode {
		t.Fatalf("codexPendingOpen = %#v, want pending Claude launch", got.codexPendingOpen)
	}

	msg := cmd()
	opened, ok := msg.(codexSessionOpenedMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want codexSessionOpenedMsg", msg)
	}
	if opened.err != nil {
		t.Fatalf("launchClaudeForSelection returned error = %v", opened.err)
	}

	if len(requests) != 1 {
		t.Fatalf("request count = %d, want 1", len(requests))
	}
	if requests[0].Provider != codexapp.ProviderClaudeCode {
		t.Fatalf("provider = %q, want %q", requests[0].Provider, codexapp.ProviderClaudeCode)
	}
	if !requests[0].ForceNew {
		t.Fatalf("ForceNew = false, want true")
	}
	if requests[0].Prompt != "continue with the current task" {
		t.Fatalf("prompt = %q, want Claude prompt", requests[0].Prompt)
	}
}

func TestTodoDialogSelectedRowHasNoExtraLeadingSpace(t *testing.T) {
	m := Model{
		detail: model.ProjectDetail{
			Summary: model.ProjectSummary{Path: "/tmp/demo"},
			Todos: []model.TodoItem{{
				ID:          7,
				ProjectPath: "/tmp/demo",
				Text:        "Fix spacing on selected TODO row",
			}},
		},
		todoDialog: &todoDialogState{ProjectPath: "/tmp/demo", ProjectName: "demo", Selected: 0},
		width:      100,
		height:     24,
	}

	rendered := ansi.Strip(m.renderTodoDialogOverlay("", 100, 24))
	selectedRow := ""
	for _, line := range strings.Split(rendered, "\n") {
		if strings.Contains(line, "[ ] Fix spacing on selected TODO row") {
			selectedRow = strings.TrimLeft(line, " \t")
			break
		}
	}
	if selectedRow == "" {
		t.Fatalf("selected TODO row not found, got %q", rendered)
	}
	if !strings.HasPrefix(selectedRow, "│ [") {
		t.Fatalf("expected selected TODO row to start with a single leading space after panel border, got %q", selectedRow)
	}
	if strings.HasPrefix(selectedRow, "│  [") {
		t.Fatalf("expected no extra leading space before selected TODO marker, got %q", selectedRow)
	}
}

func TestTodoDialogShowsWorktreeSuggestionState(t *testing.T) {
	prevProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	defer lipgloss.SetColorProfile(prevProfile)

	m := Model{
		detail: model.ProjectDetail{
			Summary: model.ProjectSummary{Path: "/tmp/demo"},
			Todos: []model.TodoItem{
				{
					ID:          7,
					ProjectPath: "/tmp/demo",
					Text:        "Fix spacing on selected TODO row",
					WorktreeSuggestion: &model.TodoWorktreeSuggestion{
						Status:     model.TodoWorktreeSuggestionReady,
						BranchName: "fix/todo-dialog-spacing",
					},
				},
				{
					ID:          8,
					ProjectPath: "/tmp/demo",
					Text:        "Write launch dialog spec",
					WorktreeSuggestion: &model.TodoWorktreeSuggestion{
						Status: model.TodoWorktreeSuggestionQueued,
					},
				},
			},
		},
		todoDialog: &todoDialogState{ProjectPath: "/tmp/demo", ProjectName: "demo", Selected: 0},
		width:      100,
		height:     24,
	}

	rendered := m.renderTodoDialogOverlay("", 100, 24)
	if !strings.Contains(ansi.Strip(rendered), "fix/todo-dialog-spacing") {
		t.Fatalf("rendered TODO dialog should show ready branch suggestion, got %q", ansi.Strip(rendered))
	}
	if !strings.Contains(rendered, "38;5;244") {
		t.Fatalf("rendered TODO dialog should keep suggestion-only worktree labels muted, got %q", rendered)
	}
	if strings.Contains(ansi.Strip(rendered), "preparing suggestion...") {
		t.Fatalf("rendered TODO dialog should hide queued suggestion state, got %q", ansi.Strip(rendered))
	}
}

func TestTodoDialogPageNavigationRevealsItems(t *testing.T) {
	todos := make([]model.TodoItem, 40)
	for i := 0; i < 40; i++ {
		todos[i] = model.TodoItem{
			ID:          int64(i + 1),
			ProjectPath: "/tmp/demo",
			Text:        fmt.Sprintf("Todo %d", i),
		}
	}
	m := Model{
		detail: model.ProjectDetail{
			Summary: model.ProjectSummary{Path: "/tmp/demo"},
			Todos:   todos,
		},
		todoDialog: &todoDialogState{ProjectPath: "/tmp/demo", ProjectName: "demo"},
		width:      100,
		height:     24,
	}

	updated, _ := m.updateTodoDialogMode(tea.KeyMsg{Type: tea.KeyPgDown})
	got := updated.(Model)
	if got.todoDialog == nil {
		t.Fatalf("todo dialog should stay open after Page Down")
	}
	if got.todoDialog.Selected != 14 {
		t.Fatalf("todo dialog selected = %d, want %d", got.todoDialog.Selected, 14)
	}

	rendered := ansi.Strip(got.renderTodoDialogOverlay("", 100, 24))
	if !strings.Contains(rendered, "Todo 14") {
		t.Fatalf("rendered TODO dialog should include newly selected item, got %q", rendered)
	}
}

func TestTodoDialogMouseWheelScrollRevealsHiddenItems(t *testing.T) {
	todos := make([]model.TodoItem, 40)
	for i := 0; i < 40; i++ {
		todos[i] = model.TodoItem{
			ID:          int64(i + 1),
			ProjectPath: "/tmp/demo",
			Text:        fmt.Sprintf("Todo %d", i),
		}
	}
	m := Model{
		detail: model.ProjectDetail{
			Summary: model.ProjectSummary{Path: "/tmp/demo"},
			Todos:   todos,
		},
		todoDialog: &todoDialogState{ProjectPath: "/tmp/demo", ProjectName: "demo"},
		width:      100,
		height:     24,
	}

	msg := tea.MouseMsg{
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonWheelDown,
	}
	current := m
	for i := 0; i < 25; i++ {
		updated, _ := current.Update(msg)
		next, ok := updated.(Model)
		if !ok {
			t.Fatalf("updated model = %T, want Model", updated)
		}
		current = next
	}

	got := current
	if got.todoDialog.Selected <= 0 {
		t.Fatalf("todo dialog should move selection on wheel scroll, selected = %d", got.todoDialog.Selected)
	}
	if got.todoDialog.Selected != 25 {
		t.Fatalf("todo dialog selected = %d, want %d", got.todoDialog.Selected, 25)
	}

	rendered := ansi.Strip(got.renderTodoDialogOverlay("", 100, 24))
	if !strings.Contains(rendered, "Todo 25") {
		t.Fatalf("rendered TODO dialog should include the scrolled-into selection, got %q", rendered)
	}
}

func TestTodoDialogHomeAndEndJumpToExtremes(t *testing.T) {
	todos := make([]model.TodoItem, 24)
	for i := 0; i < 24; i++ {
		todos[i] = model.TodoItem{
			ID:          int64(i + 1),
			ProjectPath: "/tmp/demo",
			Text:        fmt.Sprintf("Todo %d", i),
		}
	}
	m := Model{
		detail: model.ProjectDetail{
			Summary: model.ProjectSummary{Path: "/tmp/demo"},
			Todos:   todos,
		},
		todoDialog: &todoDialogState{ProjectPath: "/tmp/demo", ProjectName: "demo", Selected: 10},
		width:      100,
		height:     24,
	}

	updated, _ := m.updateTodoDialogMode(tea.KeyMsg{Type: tea.KeyHome})
	got := updated.(Model)
	if got.todoDialog.Selected != 0 {
		t.Fatalf("home should jump to first TODO, selected = %d", got.todoDialog.Selected)
	}

	updated, _ = got.updateTodoDialogMode(tea.KeyMsg{Type: tea.KeyEnd})
	got = updated.(Model)
	if got.todoDialog.Selected != len(todos)-1 {
		t.Fatalf("end should jump to last TODO, selected = %d", got.todoDialog.Selected)
	}

	rendered := ansi.Strip(got.renderTodoDialogOverlay("", 100, 24))
	if !strings.Contains(rendered, "Todo 23") {
		t.Fatalf("rendered TODO dialog should include last item after End, got %q", rendered)
	}
}

func TestTodoDialogHighlightsActiveLinkedWorktreeState(t *testing.T) {
	prevProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	defer lipgloss.SetColorProfile(prevProfile)

	m := Model{
		allProjects: []model.ProjectSummary{
			{
				Path:             "/tmp/demo",
				PresentOnDisk:    true,
				WorktreeRootPath: "/tmp/demo",
				WorktreeKind:     model.WorktreeKindMain,
				RepoBranch:       "master",
			},
			{
				Path:                 "/tmp/demo--fix-todo-dialog-spacing",
				PresentOnDisk:        true,
				WorktreeRootPath:     "/tmp/demo",
				WorktreeKind:         model.WorktreeKindLinked,
				WorktreeOriginTodoID: 7,
				RepoBranch:           "fix/todo-dialog-spacing",
			},
		},
		detail: model.ProjectDetail{
			Summary: model.ProjectSummary{Path: "/tmp/demo"},
			Todos: []model.TodoItem{
				{
					ID:          7,
					ProjectPath: "/tmp/demo",
					Text:        "Fix spacing on selected TODO row",
					WorktreeSuggestion: &model.TodoWorktreeSuggestion{
						Status:     model.TodoWorktreeSuggestionReady,
						BranchName: "fix/todo-dialog-spacing",
					},
				},
			},
		},
		todoDialog: &todoDialogState{ProjectPath: "/tmp/demo", ProjectName: "demo", Selected: 0},
		width:      100,
		height:     24,
	}

	rendered := m.renderTodoDialogOverlay("", 100, 24)
	if !strings.Contains(ansi.Strip(rendered), "fix/todo-dialog-spacing") {
		t.Fatalf("rendered TODO dialog should show linked worktree label, got %q", ansi.Strip(rendered))
	}
	if !strings.Contains(rendered, "38;5;42") {
		t.Fatalf("rendered TODO dialog should highlight active linked worktree labels, got %q", rendered)
	}
}

func TestTodoCopyDialogShowsRetryGuidanceForFailedWorktreeSuggestion(t *testing.T) {
	m := Model{
		detail: model.ProjectDetail{
			Summary: model.ProjectSummary{Path: "/tmp/demo"},
			Todos: []model.TodoItem{{
				ID:          7,
				ProjectPath: "/tmp/demo",
				Text:        "Fix spacing on selected TODO row",
				WorktreeSuggestion: &model.TodoWorktreeSuggestion{
					Status: model.TodoWorktreeSuggestionFailed,
				},
			}},
		},
		todoDialog: &todoDialogState{ProjectPath: "/tmp/demo", ProjectName: "demo"},
		todoCopyDialog: &todoCopyDialogState{
			ProjectPath: "/tmp/demo",
			ProjectName: "demo",
			TodoID:      7,
			TodoText:    "Fix spacing on selected TODO row",
			RunMode:     todoCopyModeNewWorktree,
			Provider:    codexapp.ProviderCodex,
		},
		width:  100,
		height: 24,
	}

	rendered := ansi.Strip(m.renderTodoCopyDialogOverlay("", 100, 24))
	if !strings.Contains(rendered, "Worktree suggestion is unavailable right now.") {
		t.Fatalf("rendered copy dialog should show the failed suggestion status, got %q", rendered)
	}
	if !strings.Contains(rendered, "Press Enter to launch with an automatic name, or e to enter names now.") {
		t.Fatalf("rendered copy dialog should explain the next recovery step, got %q", rendered)
	}
}
