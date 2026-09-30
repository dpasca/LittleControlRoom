package tui

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	bossui "lcroom/internal/boss"
	"lcroom/internal/codexapp"
	"lcroom/internal/control"
	"lcroom/internal/model"
	"lcroom/internal/service"
)

func TestSessionlessWorktreeOffersStartWithoutOpeningProvider(t *testing.T) {
	for _, provider := range []codexapp.Provider{codexapp.ProviderCodex, codexapp.ProviderOpenCode, codexapp.ProviderClaudeCode, codexapp.ProviderLCAgent} {
		t.Run(string(provider), func(t *testing.T) {
			project := model.ProjectSummary{Path: "/repo--orphan", Name: "orphan", PresentOnDisk: true, WorktreeKind: model.WorktreeKindLinked, WorktreeRootPath: "/repo"}
			m := Model{projects: []model.ProjectSummary{project}, allProjects: []model.ProjectSummary{project}}
			updated, cmd := m.launchEmbeddedForProjectWithOptions(project, provider, embeddedLaunchOptions{reveal: true})
			got := updated.(Model)
			if cmd != nil || got.attentionDialog == nil || got.attentionDialog.StartTodoProject == nil || !strings.Contains(got.attentionDialog.Message, "no engineer session") {
				t.Fatalf("cmd=%v dialog=%+v", cmd, got.attentionDialog)
			}
			if got.codexPendingOpen != nil {
				t.Fatal("opening orphan queued a provider")
			}
		})
	}
}

func TestTrackedLaunchProviderFailureRetryReusesTodoWorktreeAndPrompt(t *testing.T) {
	ctx := context.Background()
	svc := newControlTestService(t)
	root := t.TempDir()
	projectPath := filepath.Join(root, "repo")
	runTUITestGit(t, "", "init", projectPath)
	runTUITestGit(t, projectPath, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "--allow-empty", "-m", "initial")
	if _, err := svc.CreateOrAttachProject(ctx, service.CreateOrAttachProjectRequest{ParentPath: root, Name: "repo"}); err != nil {
		t.Fatal(err)
	}
	input := control.TodoCreateWorktreeAndStartEngineerInput{RequestID: "retry-launch", ProjectPath: projectPath, TodoText: "Implement recoverable launches", Prompt: "Keep this exact task prompt", Provider: control.ProviderCodex}
	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		if len(requests) == 1 {
			return nil, errors.New("provider unavailable")
		}
		return &fakeCodexSession{projectPath: req.ProjectPath, snapshot: codexapp.Snapshot{Provider: req.Provider, ThreadID: "retry-session", Started: true, LastActivityAt: time.Now()}}, nil
	})
	for attempt := 0; attempt < 3; attempt++ {
		projects, err := svc.Store().ListProjects(ctx, false)
		if err != nil {
			t.Fatal(err)
		}
		m := Model{ctx: ctx, svc: svc, projects: projects, allProjects: projects, codexManager: manager}
		outcome := m.executeTodoCreateWorktreeAndStartEngineerControlWithOutcome(input)
		created := outcome.cmd().(bossTodoWorktreeTodoCreatedMsg)
		if created.err != nil {
			t.Fatal(created.err)
		}
		updated, cmd := outcome.model.Update(created)
		m = updated.(Model)
		if attempt == 2 {
			result := cmd().(bossui.ControlInvocationResultMsg)
			if result.Err != nil || !strings.Contains(result.Status, "no duplicate launch") {
				t.Fatalf("replay=%+v", result)
			}
			continue
		}
		var prepared bossTodoWorktreePreparedMsg
		for _, msg := range collectCmdMsgs(cmd) {
			if candidate, ok := msg.(bossTodoWorktreePreparedMsg); ok {
				prepared = candidate
			}
		}
		if prepared.err != nil {
			t.Fatal(prepared.err)
		}
		updated, cmd = m.Update(prepared)
		m = updated.(Model)
		var receipt bossui.ControlInvocationResultMsg
		for _, msg := range collectCmdMsgs(cmd) {
			if result, ok := msg.(bossui.ControlInvocationResultMsg); ok {
				receipt = result
			}
			if opened, ok := msg.(codexSessionOpenedMsg); ok {
				updated, _ = m.Update(opened)
				m = updated.(Model)
			}
		}
		if attempt == 0 && (receipt.Err == nil || !strings.Contains(receipt.Status, "Retry launch")) {
			t.Fatalf("failed receipt=%+v", receipt)
		}
		if attempt == 1 && receipt.Err != nil {
			t.Fatal(receipt.Err)
		}
	}
	if len(requests) != 2 || requests[0].ProjectPath != requests[1].ProjectPath || requests[0].Prompt != requests[1].Prompt {
		t.Fatalf("requests=%+v", requests)
	}
	todos, _, err := svc.Store().ListOpenTodosForReview(ctx, projectPath)
	if err != nil || len(todos) != 1 {
		t.Fatalf("todos=%+v err=%v", todos, err)
	}
	todo, err := svc.Store().GetTodo(ctx, todos[0].ID)
	if err != nil || todo.WorkSessionID != "codex:retry-session" {
		t.Fatalf("todo=%+v err=%v", todo, err)
	}
	waitForControlAsyncRefreshes(t, svc)
}

func TestRetryLaunchDialogUsesSavedRequestAndIgnoresProviderChanges(t *testing.T) {
	item := model.TodoItem{ID: 1, ProjectPath: "/repo", Text: "Task", LaunchRequestID: "request", LaunchWorktreePath: "/repo--task", LaunchWorktreeReady: true}
	m := Model{todoDialog: &todoDialogState{ProjectPath: "/repo"}, detail: model.ProjectDetail{Summary: model.ProjectSummary{Path: "/repo"}, Todos: []model.TodoItem{item}}}
	m.openTodoCopyDialog(item)
	panel := m.renderTodoCopyDialogOverlay("", 100, 40)
	if !strings.Contains(panel, "Worktree ready, engineer not started") || !strings.Contains(panel, "Retry launch") {
		t.Fatal(panel)
	}
	updated, cmd := m.updateTodoCopyDialogMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if cmd != nil || updated.(Model).todoCopyDialog.Provider != m.todoCopyDialog.Provider {
		t.Fatal("retry changed saved provider")
	}
	m.detail.Todos[0].WorktreeSuggestion = &model.TodoWorktreeSuggestion{Status: model.TodoWorktreeSuggestionQueued}
	updated, cmd = m.updateTodoCopyDialogMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil || !got.todoCopyDialog.Submitting {
		t.Fatal("saved retry should bypass queued suggestions and become busy")
	}
	_, duplicate := got.updateTodoCopyDialogMode(tea.KeyMsg{Type: tea.KeyEnter})
	if duplicate != nil {
		t.Fatal("duplicate retry was queued")
	}
	updated, _ = got.Update(cmd()) // unavailable service is an explicit recoverable error
	if updated.(Model).todoCopyDialog.Submitting {
		t.Fatal("failed retry left busy state")
	}

}
