package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"lcroom/internal/codexapp"
	"lcroom/internal/service"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const newTaskCreateTimeout = 15 * time.Second

type newTaskResultMsg struct {
	result   service.CreateScratchTaskResult
	provider codexapp.Provider
	err      error
}

type newTaskDialogState struct {
	Request              string
	Provider             codexapp.Provider
	ProviderDefaultLabel string
	Submitting           bool
}

func (m *Model) openNewTaskDialog(request string, provider codexapp.Provider) tea.Cmd {
	provider, defaultLabel := m.initialEmbeddedProviderForNewItem(provider)
	m.newTaskDialog = &newTaskDialogState{
		Request:              strings.TrimSpace(request),
		Provider:             provider,
		ProviderDefaultLabel: defaultLabel,
	}
	m.showHelp = false
	m.err = nil
	m.status = "New task dialog open. Enter create, j/k choose agent, Esc cancel"
	return nil
}

func (m Model) updateNewTaskDialogMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	dialog := m.newTaskDialog
	if dialog == nil {
		return m, nil
	}
	if dialog.Submitting {
		return m, nil
	}
	switch msg.String() {
	case "esc", "ctrl+c":
		m.newTaskDialog = nil
		m.status = "New task canceled"
		return m, nil
	case "down", "j":
		m.cycleNewTaskProvider(1)
		return m, nil
	case "up", "k":
		m.cycleNewTaskProvider(-1)
		return m, nil
	case "enter":
		request := dialog.Request
		provider := dialog.Provider
		m.newTaskDialog = nil
		m.status = "Creating scratch task..."
		return m, m.createScratchTaskCmd(request, provider)
	}
	return m, nil
}

func (m *Model) cycleNewTaskProvider(delta int) {
	dialog := m.newTaskDialog
	if dialog == nil || delta == 0 {
		return
	}
	options := embeddedLaunchProviderOptions()
	index := 0
	current := explicitEmbeddedProvider(dialog.Provider)
	for i, provider := range options {
		if provider == current {
			index = i
			break
		}
	}
	index += delta
	if index < 0 {
		index = len(options) - 1
	}
	if index >= len(options) {
		index = 0
	}
	dialog.Provider = options[index]
	dialog.ProviderDefaultLabel = ""
}

func (m Model) createScratchTaskCmd(request string, provider codexapp.Provider) tea.Cmd {
	if m.svc == nil {
		return func() tea.Msg {
			return newTaskResultMsg{err: fmt.Errorf("service unavailable")}
		}
	}
	request = strings.TrimSpace(request)
	provider = explicitEmbeddedProvider(provider)
	preferredSource := modelSessionSourceFromCodexProvider(provider)
	return func() tea.Msg {
		ctx := m.ctx
		if ctx == nil {
			ctx = context.Background()
		}
		ctx, cancel := context.WithTimeout(ctx, newTaskCreateTimeout)
		defer cancel()
		result, err := m.svc.CreateScratchTask(ctx, service.CreateScratchTaskRequest{
			Request:                request,
			PreferredSessionSource: preferredSource,
		})
		err = timeoutActionError(err, newTaskCreateTimeout, "creating the scratch task")
		return newTaskResultMsg{result: result, provider: provider, err: err}
	}
}

func (m Model) applyNewTaskResultMsg(msg newTaskResultMsg) (tea.Model, tea.Cmd) {
	if m.newTaskDialog != nil {
		m.newTaskDialog.Submitting = false
	}
	if msg.err != nil {
		m.reportError("Scratch task setup failed", msg.err, "")
		return m, nil
	}

	m.err = nil
	m.newTaskDialog = nil
	refreshCmd := m.requestProjectInvalidationCmd(invalidateProjectStructure(""))
	m.focusedPane = focusProjects
	m.preferredSelectPath = msg.result.TaskPath
	m.status = "Scratch task created and added to the list"
	if provider := explicitEmbeddedProvider(msg.provider); provider != "" {
		m.rememberEmbeddedProvider(provider)
		m.setEmbeddedLaunchProviderOverride(msg.result.TaskPath, provider)
		m.status += "; Enter opens " + provider.Label()
	}
	return m, refreshCmd
}

func (m Model) renderNewTaskOverlay(body string, bodyW, bodyH int) string {
	panelW := min(bodyW, min(max(64, bodyW-8), 96))
	panelInnerW := max(24, panelW-4)
	panel := renderDialogPanel(panelW, panelInnerW, m.renderNewTaskContent(panelInnerW))
	left := max(0, (bodyW-panelW)/2)
	top := max(0, (bodyH-lipgloss.Height(panel))/3)
	return overlayBlock(body, panel, bodyW, bodyH, left, top)
}

func (m Model) renderNewTaskContent(width int) string {
	dialog := m.newTaskDialog
	if dialog == nil {
		return ""
	}
	lines := []string{
		commandPaletteTitleStyle.Render("New Task"),
		commandPaletteHintStyle.Render("Create a scratch task and choose which embedded agent Enter should open."),
		"",
	}
	request := strings.TrimSpace(dialog.Request)
	if request == "" {
		request = "Untitled scratch task"
	}
	lines = append(lines,
		detailLabelStyle.Render("Request:"),
		detailValueStyle.Width(width).Render(truncateText(request, max(12, width))),
		"",
		detailSectionStyle.Render("Agent"),
	)
	settings := m.currentSettingsBaseline()
	for _, provider := range embeddedLaunchProviderOptions() {
		label := m.todoCopyProviderButtonLabel("", provider, settings)
		lines = append(lines, fitStyledWidth(renderDialogButton(label, dialog.Provider == provider), width))
	}
	if dialog.ProviderDefaultLabel != "" {
		lines = append(lines, commandPaletteHintStyle.Render("Default: "+dialog.ProviderDefaultLabel+"."))
	}
	if statusLine := m.todoCopyProviderStatusLine(dialog.Provider, settings); statusLine != "" {
		lines = append(lines, detailField("Agent status", statusLine))
	}
	lines = append(lines, "", renderHelpPanelActionRow(
		renderDialogAction("Enter", "create", commitActionKeyStyle, commitActionTextStyle),
		renderDialogAction("↑↓/j/k", "agent", navigateActionKeyStyle, navigateActionTextStyle),
		renderDialogAction("Esc", "cancel", cancelActionKeyStyle, cancelActionTextStyle),
	))
	return strings.Join(lines, "\n")
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
