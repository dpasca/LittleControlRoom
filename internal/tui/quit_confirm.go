package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const (
	quitConfirmFocusQuit = iota
	quitConfirmFocusStay
)

type quitConfirmState struct {
	Selected int
}

func (m *Model) openQuitConfirm() {
	if m.gracefulQuitInFlight {
		m.status = "Quit already in progress"
		return
	}
	m.quitConfirm = &quitConfirmState{Selected: quitConfirmFocusStay}
	m.status = "Confirm quit"
}

func (m *Model) closeQuitConfirm(status string) {
	m.quitConfirm = nil
	if status != "" {
		m.status = status
	}
}

func (m *Model) cycleQuitConfirmSelection(delta int) {
	if m.quitConfirm == nil || delta == 0 {
		return
	}
	const choiceCount = 2
	m.quitConfirm.Selected = (m.quitConfirm.Selected + delta + choiceCount) % choiceCount
}

func (m Model) updateQuitConfirmMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	confirm := m.quitConfirm
	if confirm == nil {
		return m, nil
	}
	switch msg.String() {
	case "esc", "ctrl+c":
		m.closeQuitConfirm("Quit canceled")
		return m, nil
	case "left", "h", "up", "k", "shift+tab":
		m.cycleQuitConfirmSelection(-1)
		return m, nil
	case "right", "l", "down", "j", "tab":
		m.cycleQuitConfirmSelection(1)
		return m, nil
	case "enter":
		if confirm.Selected == quitConfirmFocusStay {
			m.closeQuitConfirm("Quit canceled")
			return m, nil
		}
		m.quitConfirm = nil
		return m.beginGracefulQuit()
	}
	return m, nil
}

func (m Model) renderQuitConfirmOverlay(body string, bodyW, bodyH int) string {
	confirm := m.quitConfirm
	if confirm == nil {
		return body
	}
	panelW := min(68, max(36, bodyW-16))
	panelW = min(panelW, max(14, bodyW-4))
	panelInnerW := max(10, panelW-4)
	lines := []string{
		detailSectionStyle.Render("Quit Little Control Room?"),
		"",
	}
	lines = append(lines, renderWrappedDialogTextLines(
		detailWarningStyle,
		panelInnerW,
		"In-flight engineer turns will be saved before closing, and managed runtimes will be stopped.",
	)...)
	lines = append(lines, "", renderQuitConfirmButtons(confirm))
	panel := renderDialogPanel(panelW, panelInnerW, strings.Join(lines, "\n"))
	left := max(0, (bodyW-lipgloss.Width(panel))/2)
	top := max(0, (bodyH-lipgloss.Height(panel))/2)
	return overlayBlock(body, panel, bodyW, bodyH, left, top)
}

func renderQuitConfirmButtons(confirm *quitConfirmState) string {
	if confirm == nil {
		return ""
	}
	return strings.Join([]string{
		renderDialogButton("Quit", confirm.Selected == quitConfirmFocusQuit),
		renderDialogButton("Stay", confirm.Selected == quitConfirmFocusStay),
	}, " ")
}
