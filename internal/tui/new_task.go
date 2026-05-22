package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"lcroom/internal/service"

	tea "github.com/charmbracelet/bubbletea"
)

const newTaskCreateTimeout = 15 * time.Second

type newTaskResultMsg struct {
	result service.CreateScratchTaskResult
	err    error
}

func (m *Model) startNewTaskCreation(request string) tea.Cmd {
	m.showHelp = false
	m.err = nil
	m.status = "Creating scratch task..."
	return m.createScratchTaskCmd(request)
}

func (m Model) createScratchTaskCmd(request string) tea.Cmd {
	if m.svc == nil {
		return func() tea.Msg {
			return newTaskResultMsg{err: fmt.Errorf("service unavailable")}
		}
	}
	request = strings.TrimSpace(request)
	return func() tea.Msg {
		ctx := m.ctx
		if ctx == nil {
			ctx = context.Background()
		}
		ctx, cancel := context.WithTimeout(ctx, newTaskCreateTimeout)
		defer cancel()
		result, err := m.svc.CreateScratchTask(ctx, service.CreateScratchTaskRequest{Request: request})
		return newTaskResultMsg{result: result, err: err}
	}
}

func (m Model) applyNewTaskResultMsg(msg newTaskResultMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.reportError("Scratch task setup failed", msg.err, "")
		return m, nil
	}

	m.err = nil
	refreshCmd := m.requestProjectInvalidationCmd(invalidateProjectStructure(""))
	m.focusedPane = focusProjects
	m.preferredSelectPath = msg.result.TaskPath
	m.status = "Scratch task created and added to the list"
	return m, refreshCmd
}

func (m Model) maybeAutoRenameScratchTaskFromPrompt(projectPath, prompt string) (bool, error) {
	if m.svc == nil {
		return false, nil
	}
	ctx := m.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	result, err := m.svc.MaybeRenameScratchTaskFromPrompt(ctx, projectPath, prompt)
	return result.Renamed, err
}
