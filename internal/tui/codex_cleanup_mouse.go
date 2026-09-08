package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func (m Model) updateCodexCleanupMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	d := m.codexCleanup
	if d == nil || d.Loading || d.Deleting || d.Finished || msg.Action != tea.MouseActionPress {
		return m, nil
	}
	layout := m.bodyLayout()
	width, height, header := layout.width, layout.height, 1
	if m.codexVisible() {
		width, height, header = max(1, m.width), max(1, m.height), 0
	}
	panelW := min(width, min(max(64, width-8), 112))
	innerW := max(1, panelW-4)
	view := buildCodexCleanupView(d, innerW, height, m.spinnerFrame, m.currentTime())
	panel := renderDialogPanel(panelW-2, innerW, view.text())
	x := msg.X - max(0, (width-panelW)/2) - 2
	y := msg.Y - header - max(0, (height-lipgloss.Height(panel))/2) - 1
	for _, hit := range view.hits {
		if x < hit.x || x >= hit.x+hit.width || y != hit.y {
			continue
		}
		if d.Dropdown && (hit.row < 0 || hit.focus != d.Focus) {
			continue
		}
		if msg.Button == tea.MouseButtonWheelUp || msg.Button == tea.MouseButtonWheelDown {
			if d.Confirming || (!d.Dropdown && hit.focus != cleanupFocusTable) {
				return m, nil
			}
			d.Focus = hit.focus
			key := tea.KeyDown
			if msg.Button == tea.MouseButtonWheelUp {
				key = tea.KeyUp
			}
			return m.updateCodexCleanupMode(tea.KeyMsg{Type: key})
		}
		if msg.Button != tea.MouseButtonLeft {
			return m, nil
		}
		if d.Confirming {
			d.ReviewDelete = hit.focus == cleanupFocusReview
			return m.updateCodexCleanupMode(tea.KeyMsg{Type: tea.KeyEnter})
		}
		if d.Dropdown {
			d.OptionIndex = hit.row
			return m.updateCodexCleanupMode(tea.KeyMsg{Type: tea.KeyEnter})
		}
		d.Focus = hit.focus
		if hit.focus == cleanupFocusTable {
			if d.ShowRetained {
				d.RetainedIndex = hit.row
				return m, nil
			}
			d.Selected = hit.row
			return m.updateCodexCleanupMode(tea.KeyMsg{Type: tea.KeySpace})
		}
		return m.updateCodexCleanupMode(tea.KeyMsg{Type: tea.KeyEnter})
	}
	if msg.Button == tea.MouseButtonLeft {
		d.Dropdown = false
	}
	return m, nil
}
