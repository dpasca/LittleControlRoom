package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type actionNoticeDialogState struct {
	Title    string
	Subject  string
	Summary  string
	NextStep string
	Details  string
}

func (m *Model) openActionNoticeDialog(title, subject, summary, nextStep, details string) {
	m.actionNoticeDialog = &actionNoticeDialogState{
		Title:    strings.TrimSpace(title),
		Subject:  strings.TrimSpace(subject),
		Summary:  strings.TrimSpace(summary),
		NextStep: strings.TrimSpace(nextStep),
		Details:  strings.TrimSpace(details),
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
	summary := dialog.Summary
	if summary == "" {
		summary = "The requested action could not be completed."
	}
	lines = append(lines, renderWrappedDialogTextLines(detailWarningStyle, width, summary)...)
	if dialog.NextStep != "" {
		lines = append(lines, "", detailSectionStyle.Render("Do this first"))
		lines = append(lines, renderWrappedDialogTextLines(detailValueStyle, width, dialog.NextStep)...)
	}
	if dialog.Details != "" {
		lines = append(lines, "", detailSectionStyle.Render("More detail"))
		lines = append(lines, renderWrappedDialogTextLines(detailMutedStyle, width, dialog.Details)...)
	}
	lines = append(lines,
		"",
		renderDialogAction("Enter/Esc", "dismiss", cancelActionKeyStyle, cancelActionTextStyle),
	)
	return strings.Join(lines, "\n")
}
