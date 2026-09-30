package tui

import (
	"context"
	"database/sql"
	"errors"

	"lcroom/internal/model"

	tea "github.com/charmbracelet/bubbletea"
)

func (m Model) retrySavedTodoLaunchCmd(item model.TodoItem) tea.Cmd {
	return func() tea.Msg {
		ctx := m.ctx
		if ctx == nil {
			ctx = context.Background()
		}
		if m.svc == nil {
			return todoActionMsg{projectPath: item.ProjectPath, err: errors.New("service unavailable")}
		}
		input, err := m.svc.Store().TodoLaunchInput(ctx, item.ID)
		if err != nil {
			return todoActionMsg{projectPath: item.ProjectPath, err: err}
		}
		project, err := m.svc.Store().GetTrackedProjectSummary(ctx, item.ProjectPath)
		if err != nil {
			return todoActionMsg{projectPath: item.ProjectPath, err: err}
		}
		provider, err := m.resolveControlEngineerProvider(input.Provider, project)
		if err != nil {
			return todoActionMsg{projectPath: item.ProjectPath, err: err}
		}
		// This button is an explicit retry; reopen an external operation receipt
		// when present. Chat-owned launches have no control_operations row.
		if _, err := m.svc.Store().GetControlOperation(ctx, input.RequestID); err == nil {
			if _, err := m.svc.Store().RetryFailedTodoLaunchOperation(ctx, input.RequestID, true); err != nil {
				return todoActionMsg{projectPath: item.ProjectPath, err: err}
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return todoActionMsg{projectPath: item.ProjectPath, err: err}
		}
		fresh, err := m.svc.Store().GetTodo(ctx, item.ID)
		if err != nil {
			return todoActionMsg{projectPath: item.ProjectPath, err: err}
		}
		return bossTodoWorktreeTodoCreatedMsg{inv: todoCreateWorktreeAndStartEngineerInvocationFromInput(input), input: input, project: project, provider: provider, todo: fresh, err: err}
	}
}
