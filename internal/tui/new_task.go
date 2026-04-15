package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	"lcroom/internal/config"
	"lcroom/internal/service"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type newTaskDialogState struct {
	TitleInput textinput.Model
	Submitting bool
}

type newTaskResultMsg struct {
	result service.CreateScratchTaskResult
	err    error
}

func (m *Model) openNewTaskDialog() tea.Cmd {
	input := textinput.New()
	input.Prompt = ""
	input.Placeholder = "answer Sarah about API docs"
	input.CharLimit = 256

	m.newTaskDialog = &newTaskDialogState{TitleInput: input}
	m.showHelp = false
	m.err = nil
	m.status = "New task dialog open. Enter create, Esc cancel"
	return m.newTaskDialog.TitleInput.Focus()
}

func (m *Model) closeNewTaskDialog(status string) {
	if m.newTaskDialog != nil {
		m.newTaskDialog.TitleInput.Blur()
	}
	m.newTaskDialog = nil
	if status != "" {
		m.status = status
	}
}

func (m Model) updateNewTaskMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	dialog := m.newTaskDialog
	if dialog == nil || dialog.Submitting {
		return m, nil
	}

	switch msg.String() {
	case "esc":
		m.closeNewTaskDialog("New task canceled")
		return m, nil
	case "enter":
		title := strings.TrimSpace(dialog.TitleInput.Value())
		if title == "" {
			m.status = "Task title is required"
			return m, nil
		}
		dialog.Submitting = true
		m.status = "Creating scratch task..."
		return m, m.createScratchTaskCmd(title)
	}

	input, cmd := dialog.TitleInput.Update(msg)
	dialog.TitleInput = input
	return m, cmd
}

func (m Model) createScratchTaskCmd(title string) tea.Cmd {
	if m.svc == nil {
		return func() tea.Msg {
			return newTaskResultMsg{err: fmt.Errorf("service unavailable")}
		}
	}
	return func() tea.Msg {
		result, err := m.svc.CreateScratchTask(m.ctx, service.CreateScratchTaskRequest{Title: title})
		return newTaskResultMsg{result: result, err: err}
	}
}

func (m Model) currentScratchTaskRoot() string {
	if m.svc != nil {
		root := strings.TrimSpace(m.svc.Config().ScratchRoot)
		if root != "" {
			return filepath.Clean(root)
		}
	}
	return filepath.Clean(config.Default().ScratchRoot)
}

func (m Model) scratchTaskPreviewPath(title string) string {
	title = strings.TrimSpace(title)
	if title == "" {
		title = "task"
	}
	folder := service.BuildScratchTaskFolderName(title, m.currentTime())
	return filepath.Join(m.currentScratchTaskRoot(), folder)
}

func (m Model) renderNewTaskOverlay(body string, bodyW, bodyH int) string {
	panel := m.renderNewTaskPanel(bodyW)
	panelWidth := lipgloss.Width(panel)
	panelHeight := lipgloss.Height(panel)
	left := max(0, (bodyW-panelWidth)/2)
	top := max(0, (bodyH-panelHeight)/4)
	return overlayBlock(body, panel, bodyW, bodyH, left, top)
}

func (m Model) renderNewTaskPanel(bodyW int) string {
	panelWidth := min(bodyW, min(max(56, bodyW-10), 92))
	panelInnerWidth := max(28, panelWidth-4)
	return renderDialogPanel(panelWidth, panelInnerWidth, m.renderNewTaskContent(panelInnerWidth))
}

func (m Model) renderNewTaskContent(width int) string {
	dialog := m.newTaskDialog
	if dialog == nil {
		return ""
	}

	labelWidth := max(12, min(18, width/4))
	inputWidth := max(12, width-labelWidth-1)
	input := dialog.TitleInput
	input.Width = inputWidth

	lines := []string{
		commandPaletteTitleStyle.Render("New Task"),
		commandPaletteHintStyle.Render("Create a lightweight scratch workspace for short-lived work that may still matter later."),
		"",
		commandPalettePickStyle.Width(labelWidth).Render("> Title") + " " + input.View(),
		"",
		detailLabelStyle.Render("Root:"),
		lipgloss.NewStyle().Width(width).Render(detailValueStyle.Render(m.displayPathWithHomeTilde(m.currentScratchTaskRoot()))),
		"",
		detailLabelStyle.Render("Folder:"),
		lipgloss.NewStyle().Width(width).Render(detailValueStyle.Render(m.displayPathWithHomeTilde(m.scratchTaskPreviewPath(dialog.TitleInput.Value())))),
		"",
		commandPaletteHintStyle.Render("Enter will create the folder and a TASK.md file. Codex and OpenCode sessions started there will map back to this task by cwd."),
		"",
		renderDialogAction("Enter", "create", commitActionKeyStyle, commitActionTextStyle) + "   " +
			renderDialogAction("Esc", "cancel", cancelActionKeyStyle, cancelActionTextStyle),
	}
	return strings.Join(lines, "\n")
}
