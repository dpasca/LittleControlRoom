package tui

import (
	"fmt"
	"strings"

	"lcroom/internal/model"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func (m Model) openIgnoredPicker() (tea.Model, tea.Cmd) {
	m.ignoredPickerVisible = true
	m.ignoredPickerLoading = true
	m.ignoredPickerSelected = 0
	m.ignoredPickerItems = nil
	m.commandMode = false
	m.showHelp = false
	m.status = "Loading ignored projects..."
	return m, m.loadIgnoredProjectsCmd()
}

func (m *Model) closeIgnoredPicker(status string) {
	m.ignoredPickerVisible = false
	m.ignoredPickerLoading = false
	m.ignoredPickerSelected = 0
	m.ignoredPickerItems = nil
	if status != "" {
		m.status = status
	}
}

func (m Model) loadIgnoredProjectsCmd() tea.Cmd {
	return func() tea.Msg {
		items, err := m.svc.Store().ListIgnoredProjects(m.ctx)
		return ignoredProjectsMsg{items: items, err: err}
	}
}

func (m Model) unignoreProjectCmd(item model.IgnoredProject) tea.Cmd {
	return func() tea.Msg {
		switch item.Scope {
		case model.ProjectIgnoreScopePath:
			err := m.svc.Store().SetIgnoredProjectPath(m.ctx, item.Path, false)
			return ignoredProjectActionMsg{status: fmt.Sprintf("Restored %q", item.Path), err: err}
		default:
			err := m.svc.Store().SetIgnoredProjectName(m.ctx, item.Name, false)
			return ignoredProjectActionMsg{status: fmt.Sprintf("Restored %q", item.Name), err: err}
		}
	}
}

func (m Model) updateIgnoredPickerMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.ignoredPickerLoading {
		switch msg.String() {
		case "esc":
			m.closeIgnoredPicker("Ignored projects closed")
		}
		return m, nil
	}

	items := m.currentIgnoredPickerItems()
	if len(items) == 0 {
		m.closeIgnoredPicker("No ignored projects")
		return m, nil
	}
	if m.ignoredPickerSelected >= len(items) {
		m.ignoredPickerSelected = len(items) - 1
	}
	if m.ignoredPickerSelected < 0 {
		m.ignoredPickerSelected = 0
	}

	if m.pendingG {
		m.pendingG = false
		if msg.String() == "g" {
			m.ignoredPickerSelected = 0
			return m, nil
		}
	}

	switch msg.String() {
	case "esc":
		m.closeIgnoredPicker("Ignored projects closed")
		return m, nil
	case "up", "k":
		m.moveIgnoredPickerSelection(-1, len(items))
		return m, nil
	case "down", "j":
		m.moveIgnoredPickerSelection(1, len(items))
		return m, nil
	case "pgup", "ctrl+u":
		m.moveIgnoredPickerSelection(-5, len(items))
		return m, nil
	case "pgdown", "ctrl+d":
		m.moveIgnoredPickerSelection(5, len(items))
		return m, nil
	case "home":
		m.ignoredPickerSelected = 0
		return m, nil
	case "end", "G":
		m.ignoredPickerSelected = len(items) - 1
		return m, nil
	case "g":
		m.pendingG = true
		return m, nil
	case "enter":
		item, ok := m.currentIgnoredPickerItem()
		if !ok {
			return m, nil
		}
		m.ignoredPickerLoading = true
		m.status = fmt.Sprintf("Restoring %q...", ignoredProjectLabel(item))
		return m, m.unignoreProjectCmd(item)
	}
	return m, nil
}

func (m *Model) moveIgnoredPickerSelection(delta, total int) {
	if total <= 0 || delta == 0 {
		return
	}
	m.ignoredPickerSelected += delta
	if m.ignoredPickerSelected < 0 {
		m.ignoredPickerSelected = 0
	}
	if m.ignoredPickerSelected >= total {
		m.ignoredPickerSelected = total - 1
	}
}

func (m Model) currentIgnoredPickerItems() []model.IgnoredProject {
	return append([]model.IgnoredProject(nil), m.ignoredPickerItems...)
}

func (m Model) currentIgnoredPickerItem() (model.IgnoredProject, bool) {
	items := m.currentIgnoredPickerItems()
	if len(items) == 0 {
		return model.IgnoredProject{}, false
	}
	index := m.ignoredPickerSelected
	if index < 0 {
		index = 0
	}
	if index >= len(items) {
		index = len(items) - 1
	}
	return items[index], true
}

func (m Model) renderIgnoredPickerOverlay(body string, bodyW, bodyH int) string {
	panel := m.renderIgnoredPickerPanel(bodyW, bodyH)
	panelWidth := lipgloss.Width(panel)
	panelHeight := lipgloss.Height(panel)
	left := max(0, (bodyW-panelWidth)/2)
	top := max(0, (bodyH-panelHeight)/4)
	return overlayBlock(body, panel, bodyW, bodyH, left, top)
}

func (m Model) renderIgnoredPickerPanel(bodyW, bodyH int) string {
	panelWidth := min(bodyW, min(max(58, bodyW-10), 86))
	panelInnerWidth := max(24, panelWidth-4)
	maxContentHeight := max(10, bodyH-2)
	return renderDialogPanel(panelWidth, panelInnerWidth, m.renderIgnoredPickerContent(panelInnerWidth, maxContentHeight))
}

func (m Model) renderIgnoredPickerContent(width, bodyH int) string {
	lines := []string{
		commandPaletteTitleStyle.Render("Ignored Projects"),
		commandPaletteHintStyle.Render("Enter restore  Esc close"),
	}
	if m.ignoredPickerLoading {
		lines = append(lines, "", detailMutedStyle.Render("Loading ignored projects..."))
		return strings.Join(lines, "\n")
	}

	items := m.currentIgnoredPickerItems()
	if len(items) == 0 {
		lines = append(lines, "", detailMutedStyle.Render("No ignored projects."))
		return strings.Join(lines, "\n")
	}

	limit := max(3, min(len(items), bodyH-8))
	start := 0
	if m.ignoredPickerSelected >= limit {
		start = m.ignoredPickerSelected - limit + 1
	}
	if start < 0 {
		start = 0
	}
	end := min(len(items), start+limit)

	if start > 0 {
		lines = append(lines, commandPaletteHintStyle.Render(fmt.Sprintf("↑ %d above", start)))
	}
	for i := start; i < end; i++ {
		lines = append(lines, m.renderIgnoredPickerRow(items[i], i == m.ignoredPickerSelected, width))
	}
	if end < len(items) {
		lines = append(lines, commandPaletteHintStyle.Render(fmt.Sprintf("↓ %d below", len(items)-end)))
	}

	if selected, ok := m.currentIgnoredPickerItem(); ok {
		lines = append(lines, "")
		switch selected.Scope {
		case model.ProjectIgnoreScopePath:
			lines = append(lines, detailField("Type", detailValueStyle.Render("exact path")))
			lines = append(lines, detailField("Path", detailMutedStyle.Render(truncateText(m.displayPathWithHomeTilde(selected.Path), width-8))))
			if strings.TrimSpace(selected.Name) != "" {
				lines = append(lines, detailField("Name", detailValueStyle.Render(selected.Name)))
			}
		default:
			lines = append(lines, detailField("Type", detailValueStyle.Render("exact name")))
			lines = append(lines, detailField("Name", detailValueStyle.Render(selected.Name)))
		}
		lines = append(lines, detailField("Matches", detailMutedStyle.Render(fmt.Sprintf("%d tracked project(s)", selected.MatchedProjects))))
		if !selected.CreatedAt.IsZero() {
			lines = append(lines, detailField("Ignored", detailMutedStyle.Render(selected.CreatedAt.Format("2006-01-02 15:04"))))
		}
	}

	return strings.Join(lines, "\n")
}

func (m Model) renderIgnoredPickerRow(item model.IgnoredProject, selected bool, width int) string {
	left := ignoredProjectLabel(item)
	if left == "" {
		left = "(unnamed)"
	}
	right := fmt.Sprintf("%s %d", ignoredProjectScopeLabel(item.Scope), max(0, item.MatchedProjects))
	leftWidth := max(8, width-lipgloss.Width(right)-2)
	row := lipgloss.JoinHorizontal(
		lipgloss.Top,
		lipgloss.NewStyle().Width(leftWidth).Render(truncateText(left, leftWidth)),
		"  ",
		detailMutedStyle.Width(lipgloss.Width(right)).Align(lipgloss.Right).Render(right),
	)
	if selected {
		return projectListSelectedRowStyle.Render(row)
	}
	return row
}

func ignoredProjectLabel(item model.IgnoredProject) string {
	if item.Scope == model.ProjectIgnoreScopePath {
		return strings.TrimSpace(item.Path)
	}
	return strings.TrimSpace(item.Name)
}

func ignoredProjectScopeLabel(scope model.ProjectIgnoreScope) string {
	if scope == model.ProjectIgnoreScopePath {
		return "path"
	}
	return "name"
}
