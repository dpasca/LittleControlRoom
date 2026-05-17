package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"lcroom/internal/config"
	"lcroom/internal/service"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const newTaskCreateTimeout = 15 * time.Second

type newTaskDialogState struct {
	TitleInput textinput.Model
	Submitting bool
	RequestID  int64
}

type newTaskResultMsg struct {
	requestID int64
	result    service.CreateScratchTaskResult
	err       error
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
	if dialog == nil {
		return m, nil
	}
	if dialog.Submitting {
		if msg.String() == "esc" {
			m.closeNewTaskDialog("Scratch task creation is still running in the background")
		}
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
		m.newTaskRequestSeq++
		dialog.Submitting = true
		dialog.RequestID = m.newTaskRequestSeq
		m.status = "Creating scratch task..."
		return m, m.createScratchTaskCmd(dialog.RequestID, title)
	}

	input, cmd := dialog.TitleInput.Update(msg)
	dialog.TitleInput = input
	return m, cmd
}

func (m Model) createScratchTaskCmd(requestID int64, title string) tea.Cmd {
	if m.svc == nil {
		return func() tea.Msg {
			return newTaskResultMsg{requestID: requestID, err: fmt.Errorf("service unavailable")}
		}
	}
	return func() tea.Msg {
		ctx := m.ctx
		if ctx == nil {
			ctx = context.Background()
		}
		ctx, cancel := context.WithTimeout(ctx, newTaskCreateTimeout)
		defer cancel()
		result, err := m.svc.CreateScratchTask(ctx, service.CreateScratchTaskRequest{Title: title})
		return newTaskResultMsg{requestID: requestID, result: result, err: err}
	}
}

func (m Model) applyNewTaskResultMsg(msg newTaskResultMsg) (tea.Model, tea.Cmd) {
	dialog := m.newTaskDialog
	matchesCurrentDialog := newTaskResultMatchesDialog(dialog, msg.requestID)
	if matchesCurrentDialog {
		dialog.Submitting = false
	}

	if msg.err != nil {
		if dialog != nil && !matchesCurrentDialog {
			m.appendErrorLogEntry("Scratch task setup failed", msg.err, "")
			m.status = errorStatusWithHint("Background scratch task setup failed")
			return m, nil
		}
		m.reportError("Scratch task setup failed", msg.err, "")
		return m, nil
	}

	m.err = nil
	refreshCmd := m.requestProjectInvalidationCmd(invalidateProjectStructure(""))
	if dialog != nil && !matchesCurrentDialog {
		m.status = "Background scratch task created and added to the list"
		return m, refreshCmd
	}

	m.newTaskDialog = nil
	m.focusedPane = focusProjects
	m.preferredSelectPath = msg.result.TaskPath
	m.status = "Scratch task created and added to the list"
	return m, refreshCmd
}

func newTaskResultMatchesDialog(dialog *newTaskDialogState, requestID int64) bool {
	if dialog == nil {
		return false
	}
	if requestID == 0 || dialog.RequestID == 0 {
		return true
	}
	return dialog.RequestID == requestID
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

	actions := renderDialogAction("Enter", "create", commitActionKeyStyle, commitActionTextStyle) + "   " +
		renderDialogAction("Esc", "cancel", cancelActionKeyStyle, cancelActionTextStyle)
	if dialog.Submitting {
		actions = renderDialogAction("Esc", "close", cancelActionKeyStyle, cancelActionTextStyle)
	}

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
		actions,
	}
	return strings.Join(lines, "\n")
}
