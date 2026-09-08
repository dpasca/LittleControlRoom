package tui

import (
	"errors"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var errBusySessionReplacementCanceled = errors.New("busy session replacement canceled")

type busySessionReplacementRequestedMsg struct {
	projectPath   string
	openRequestID uint64
	launchCmd     tea.Cmd
	cancelCmd     tea.Cmd
}

type busySessionReplacementState struct {
	request    busySessionReplacementRequestedMsg
	Replace    bool
	Submitting bool
}

func (m Model) updateBusySessionReplacement(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	dialog := m.busySessionReplacement
	if dialog == nil || dialog.Submitting {
		return m, nil
	}
	switch msg.String() {
	case "tab", "shift+tab", "left", "right", "up", "down":
		dialog.Replace = !dialog.Replace
	case "esc", "ctrl+c":
		dialog.Submitting = true
		return m, dialog.request.cancelCmd
	case "enter":
		dialog.Submitting = true
		if dialog.Replace {
			return m, dialog.request.launchCmd
		}
		return m, dialog.request.cancelCmd
	}
	return m, nil
}

func (m Model) renderBusySessionReplacementOverlay(body string, bodyW, bodyH int) string {
	dialog := m.busySessionReplacement
	if dialog == nil {
		return body
	}
	panelW := min(76, max(14, bodyW-4))
	innerW := max(10, panelW-4)
	lines := []string{detailSectionStyle.Render("Stop the current answer and start a new session?"), ""}
	lines = append(lines, renderWrappedDialogTextLines(detailWarningStyle, innerW,
		projectTitle(dialog.request.projectPath, "")+" has a session still working. Starting a new session will interrupt it. Its conversation history will remain available through /resume.")...)
	lines = append(lines, "")
	lines = append(lines, renderWrappedDialogTextLines(detailLabelStyle, innerW,
		"If you meant to create a separate task, keep the current session and use /new-task.")...)
	buttons := renderDialogButton("Keep current", !dialog.Replace) + " " + renderDialogButton("Stop and start new", dialog.Replace)
	if dialog.Submitting {
		buttons = "Applying choice..."
	}
	lines = append(lines, "", buttons)
	panel := renderDialogPanel(panelW, innerW, strings.Join(lines, "\n"))
	return overlayBlock(body, panel, bodyW, bodyH,
		max(0, (bodyW-lipgloss.Width(panel))/2), max(0, (bodyH-lipgloss.Height(panel))/2))
}
