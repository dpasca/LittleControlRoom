package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"lcroom/internal/codexapp"
	"lcroom/internal/service"

	tea "github.com/charmbracelet/bubbletea"
)

const newTaskCreateTimeout = 15 * time.Second

type newTaskResultMsg struct {
	result   service.CreateScratchTaskResult
	provider codexapp.Provider
	err      error
}

func (m *Model) startNewTaskCreation(request string, provider codexapp.Provider) tea.Cmd {
	m.showHelp = false
	m.err = nil
	m.status = "Creating scratch task..."
	return m.createScratchTaskCmd(request, provider)
}

func (m Model) createScratchTaskCmd(request string, provider codexapp.Provider) tea.Cmd {
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
		return newTaskResultMsg{result: result, provider: explicitEmbeddedProvider(provider), err: err}
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
	if provider := explicitEmbeddedProvider(msg.provider); provider != "" {
		m.setEmbeddedLaunchProviderOverride(msg.result.TaskPath, provider)
		m.status += "; Enter opens " + provider.Label()
	}
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
