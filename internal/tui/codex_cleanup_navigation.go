package tui

import (
	"fmt"

	"lcroom/internal/service"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type codexCleanupFocus int

const (
	cleanupFocusTable codexCleanupFocus = iota
	cleanupFocusStorage
	cleanupFocusCategory
	cleanupFocusAge
	cleanupFocusSort
	cleanupFocusAll
	cleanupFocusCancel
	cleanupFocusRefresh
	cleanupFocusReview
)

var codexCleanupDays = []int{7, 14, 30, 90}

func codexCleanupAccent(_ *codexCleanupDialogState) lipgloss.Color {
	return lipgloss.Color("75")
}

func codexCleanupControl(label string, focused, enabled, danger bool) string {
	style := lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	if !enabled {
		style = detailMutedStyle
	} else if focused {
		color := codexCleanupAccent(nil)
		if danger {
			color = lipgloss.Color("203")
		}
		style = style.Bold(true).Foreground(lipgloss.Color("232")).Background(color)
	}
	marker := " "
	if focused && enabled {
		marker = "›"
	}
	return style.Render(marker + "[ " + label + " ]")
}

func (d *codexCleanupDialogState) days() int {
	return max(7, d.InactiveDays)
}

func (d *codexCleanupDialogState) focusOrder() []codexCleanupFocus {
	if d.ShowRetained {
		return []codexCleanupFocus{cleanupFocusTable, cleanupFocusCancel}
	}
	if d.ErrorMessage != "" {
		return []codexCleanupFocus{cleanupFocusRefresh, cleanupFocusCancel}
	}
	order := []codexCleanupFocus{cleanupFocusStorage, cleanupFocusCategory}
	if d.Category == service.CodexCleanupStale {
		order = append(order, cleanupFocusAge)
	}
	if len(d.Audit.Groups) > 0 {
		order = append(order, cleanupFocusAll)
	}
	order = append(order, cleanupFocusSort)
	if len(d.Audit.Groups) > 0 {
		order = append(order, cleanupFocusTable)
	}
	order = append(order, cleanupFocusCancel, cleanupFocusRefresh)
	if len(selectedCodexCleanupGroups(d)) > 0 {
		order = append(order, cleanupFocusReview)
	}
	return order
}

func (d *codexCleanupDialogState) moveFocus(back bool) {
	order := d.focusOrder()
	index := 0
	for i, focus := range order {
		if focus == d.Focus {
			index = i
			break
		}
	}
	step := 1
	if back {
		step = len(order) - 1
	}
	d.Focus = order[(index+step)%len(order)]
}

func (d *codexCleanupDialogState) options() []string {
	switch d.Focus {
	case cleanupFocusCategory:
		return []string{"Orphaned worktrees", "Stale sessions"}
	case cleanupFocusAge:
		return []string{"7 days", "14 days", "30 days", "90 days"}
	case cleanupFocusSort:
		return []string{"Largest first", "Oldest first", "Name"}
	}
	return nil
}

func (d *codexCleanupDialogState) openDropdown() {
	d.Dropdown = true
	d.OptionIndex = 0
	switch d.Focus {
	case cleanupFocusCategory:
		if d.Category == service.CodexCleanupStale {
			d.OptionIndex = 1
		}
	case cleanupFocusAge:
		for i, days := range codexCleanupDays {
			if days == d.days() {
				d.OptionIndex = i
			}
		}
	case cleanupFocusSort:
		d.OptionIndex = d.SortMode
	}
}

func (m Model) refreshCodexCleanup() (tea.Model, tea.Cmd) {
	d := m.codexCleanup
	d.Loading = true
	d.ErrorMessage = ""
	d.Chosen = make(map[string]bool)
	m.status = "Auditing " + d.Category.Label() + "…"
	return m, m.loadCodexCleanupAuditCmd()
}

func (m Model) updateCodexCleanupForm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	d := m.codexCleanup
	key := msg.String()
	if d.Dropdown {
		switch key {
		case "esc":
			d.Dropdown = false
		case "tab", "shift+tab":
			d.Dropdown = false
			d.moveFocus(key == "shift+tab")
		case "up", "k":
			d.OptionIndex = max(0, d.OptionIndex-1)
		case "down", "j":
			d.OptionIndex = min(len(d.options())-1, d.OptionIndex+1)
		case "enter", " ":
			d.Dropdown = false
			switch d.Focus {
			case cleanupFocusCategory:
				category := service.CodexCleanupOrphaned
				if d.OptionIndex == 1 {
					category = service.CodexCleanupStale
				}
				if category != d.Category {
					d.Category = category
					return m.refreshCodexCleanup()
				}
			case cleanupFocusAge:
				days := codexCleanupDays[d.OptionIndex]
				if days != d.days() {
					d.InactiveDays = days
					return m.refreshCodexCleanup()
				}
			case cleanupFocusSort:
				d.SortMode = d.OptionIndex
				sortCodexCleanupGroups(d)
			}
		}
		return m, nil
	}
	if key == "tab" || key == "shift+tab" {
		d.moveFocus(key == "shift+tab")
		return m, nil
	}
	if key == "esc" {
		if d.ShowRetained {
			d.ShowRetained = false
			d.Focus = cleanupFocusStorage
		} else {
			m.codexCleanup = nil
			m.status = "Codex cleanup closed; no sessions were deleted"
		}
		return m, nil
	}
	if key == "enter" || key == " " || (key == "down" && len(d.options()) > 0) {
		if d.ShowRetained {
			if d.Focus == cleanupFocusCancel {
				d.ShowRetained = false
				d.Focus = cleanupFocusStorage
			}
			return m, nil
		}
		switch d.Focus {
		case cleanupFocusCategory, cleanupFocusAge, cleanupFocusSort:
			d.openDropdown()
		case cleanupFocusStorage:
			d.ShowRetained = true
			d.Focus = cleanupFocusTable
		case cleanupFocusCancel:
			m.codexCleanup = nil
			m.status = "Codex cleanup closed; no sessions were deleted"
		case cleanupFocusRefresh:
			return m.refreshCodexCleanup()
		case cleanupFocusAll:
			m.toggleCodexCleanupAll()
		case cleanupFocusReview:
			m.reviewCodexCleanup()
		case cleanupFocusTable:
			if key == "enter" {
				m.reviewCodexCleanup()
			} else if len(d.Audit.Groups) > 0 {
				if d.Chosen == nil {
					d.Chosen = make(map[string]bool)
				}
				path := d.Audit.Groups[min(d.Selected, len(d.Audit.Groups)-1)].WorktreePath
				d.Chosen[path] = !d.Chosen[path]
			}
		}
		return m, nil
	}
	if d.Focus != cleanupFocusTable {
		return m, nil
	}
	index, total := &d.Selected, len(d.Audit.Groups)
	if d.ShowRetained {
		index, total = &d.RetainedIndex, len(d.Audit.Retained)
	}
	switch key {
	case "up", "k":
		*index = max(0, *index-1)
	case "down", "j":
		*index = min(max(0, total-1), *index+1)
	case "pgup", "ctrl+u":
		*index = max(0, *index-5)
	case "pgdown", "ctrl+d":
		*index = min(max(0, total-1), *index+5)
	case "home", "g":
		*index = 0
	case "end", "G":
		*index = max(0, total-1)
	case "a", "A":
		if !d.ShowRetained {
			m.toggleCodexCleanupAll()
		}
	}
	return m, nil
}

func (m *Model) toggleCodexCleanupAll() {
	d := m.codexCleanup
	if allCodexCleanupGroupsSelected(d) {
		d.Chosen = make(map[string]bool)
		m.status = "Cleared all Codex cleanup selections"
		return
	}
	d.Chosen = make(map[string]bool, len(d.Audit.Groups))
	for _, group := range d.Audit.Groups {
		d.Chosen[group.WorktreePath] = true
	}
}

func (m *Model) reviewCodexCleanup() {
	d := m.codexCleanup
	if len(selectedCodexCleanupGroups(d)) == 0 {
		m.status = "Select one or more project rows with Space; nothing was deleted"
		return
	}
	d.Confirming = true
	d.ReviewDelete = false
	m.status = fmt.Sprintf("Review %d selected project groups before permanent deletion", len(selectedCodexCleanupGroups(d)))
}
