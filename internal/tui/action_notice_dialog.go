package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type actionNoticeDialogState struct {
	Title   string
	Subject string
	Message string
}

func (m *Model) openActionNoticeDialog(title, subject, message string) {
	m.actionNoticeDialog = &actionNoticeDialogState{
		Title:   strings.TrimSpace(title),
		Subject: strings.TrimSpace(subject),
		Message: strings.TrimSpace(message),
	}
}

func (m Model) updateActionNoticeDialogMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.actionNoticeDialog == nil {
		return m, nil
	}
	switch msg.String() {
	case "esc", "enter", " ":
		m.actionNoticeDialog = nil
	}
	return m, nil
}

func (m Model) renderActionNoticeDialogOverlay(body string, bodyW, bodyH int) string {
	if m.actionNoticeDialog == nil {
		return body
	}
	panelW := min(82, max(36, bodyW-16))
	panelW = min(panelW, max(14, bodyW-6))
	panelInnerW := max(10, panelW-4)
	panel := renderDialogPanel(panelW, panelInnerW, m.renderActionNoticeDialogContent(panelInnerW))
	left := max(0, (bodyW-lipgloss.Width(panel))/2)
	top := max(0, (bodyH-lipgloss.Height(panel))/2)
	return overlayBlock(body, panel, bodyW, bodyH, left, top)
}

func (m Model) renderActionNoticeDialogContent(width int) string {
	dialog := m.actionNoticeDialog
	if dialog == nil {
		return ""
	}
	title := dialog.Title
	if title == "" {
		title = "Notice"
	}
	lines := []string{
		renderDialogHeader(title, dialog.Subject, "", width),
		"",
	}
	message := dialog.Message
	if message == "" {
		message = "The requested action could not be completed."
	}
	lines = append(lines, renderWrappedDialogTextLines(detailWarningStyle, width, message)...)
	lines = append(lines,
		"",
		renderDialogAction("Enter/Esc", "dismiss", cancelActionKeyStyle, cancelActionTextStyle),
	)
	return strings.Join(lines, "\n")
}
