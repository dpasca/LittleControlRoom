package tui

import (
	"fmt"
	"strings"

	"lcroom/internal/model"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

type projectFilterDialogState struct {
	Input textinput.Model
}

func newProjectFilterInput(value string) textinput.Model {
	input := textinput.New()
	input.Prompt = ""
	input.SetValue(strings.TrimSpace(value))
	input.Placeholder = "type part of a project name"
	input.CharLimit = 256
	return input
}

func (m *Model) openProjectFilterDialog() tea.Cmd {
	dialog := &projectFilterDialogState{
		Input: newProjectFilterInput(m.projectFilter),
	}
	m.projectFilterDialog = dialog
	m.commandMode = false
	m.showHelp = false
	m.err = nil
	m.status = "Project filter open. Type to narrow, Enter keep, Esc close"
	return dialog.Input.Focus()
}

func (m *Model) closeProjectFilterDialog(status string) {
	if m.projectFilterDialog != nil {
		m.projectFilterDialog.Input.Blur()
	}
	m.projectFilterDialog = nil
	if status != "" {
		m.status = status
	}
}

func (m Model) updateProjectFilterMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	dialog := m.projectFilterDialog
	if dialog == nil {
		return m, nil
	}

	switch msg.String() {
	case "esc":
		m.closeProjectFilterDialog("Project filter closed")
		return m, nil
	case "enter":
		m.closeProjectFilterDialog("")
		return m, nil
	}

	previous := strings.TrimSpace(dialog.Input.Value())
	input, cmd := dialog.Input.Update(msg)
	dialog.Input = input

	current := strings.TrimSpace(dialog.Input.Value())
	if current == previous {
		return m, cmd
	}

	applyCmd := m.setProjectFilter(current)
	switch {
	case cmd != nil && applyCmd != nil:
		return m, tea.Batch(cmd, applyCmd)
	case applyCmd != nil:
		return m, applyCmd
	default:
		return m, cmd
	}
}

func (m *Model) setProjectFilter(filter string) tea.Cmd {
	filter = strings.TrimSpace(filter)
	selectedPath := ""
	if p, ok := m.selectedProject(); ok {
		selectedPath = p.Path
	}
	currentDetailPath := strings.TrimSpace(m.detail.Summary.Path)

	m.projectFilter = filter
	m.rebuildProjectList(selectedPath)
	if dialog := m.projectFilterDialog; dialog != nil && strings.TrimSpace(dialog.Input.Value()) != filter {
		dialog.Input.SetValue(filter)
		dialog.Input.CursorEnd()
	}

	if filter == "" {
		m.status = "Project filter cleared"
	} else {
		m.status = projectFilterStatus(filter, len(m.projects))
	}

	if p, ok := m.selectedProject(); ok {
		if strings.TrimSpace(p.Path) == currentDetailPath {
			m.syncDetailViewport(false)
			return nil
		}
		return m.loadDetailCmd(p.Path)
	}
	m.detail = model.ProjectDetail{}
	m.syncDetailViewport(true)
	return nil
}

func projectFilterStatus(filter string, visibleCount int) string {
	switch visibleCount {
	case 0:
		return fmt.Sprintf("%s matched no projects", compactProjectFilterLabel(filter, 32))
	case 1:
		return fmt.Sprintf("%s matched 1 project", compactProjectFilterLabel(filter, 32))
	default:
		return fmt.Sprintf("%s matched %d projects", compactProjectFilterLabel(filter, 32), visibleCount)
	}
}

func projectFilterSummaryValue(filter string, maxValueWidth int) string {
	filter = strings.TrimSpace(filter)
	if filter == "" {
		return ""
	}
	if maxValueWidth > 0 {
		filter = truncateText(filter, maxValueWidth)
	}
	filter = strings.ReplaceAll(filter, "\"", "'")
	return `"` + filter + `"`
}

func compactProjectFilterLabel(filter string, maxValueWidth int) string {
	if value := projectFilterSummaryValue(filter, maxValueWidth); value != "" {
		return "Filter " + value
	}
	return ""
}

func (m Model) projectFilterSummaryLabel(maxValueWidth int) string {
	return projectFilterSummaryValue(m.projectFilter, maxValueWidth)
}

func (m Model) renderFooterProjectFilterSegment() string {
	if label := compactProjectFilterLabel(m.projectFilter, 20); label != "" {
		return renderFooterStatus(label)
	}
	return ""
}

func (m Model) renderModalFooter(width int, label string, extraSegments ...string) string {
	parts := []string{label}
	for _, segment := range extraSegments {
		if strings.TrimSpace(ansi.Strip(segment)) == "" {
			continue
		}
		parts = append(parts, segment)
	}
	return fitFooterWidth(strings.Join(parts, " | "), width)
}

func (m Model) renderProjectFilterOverlay(body string, bodyW, bodyH int) string {
	panel := m.renderProjectFilterPanel(bodyW)
	panelWidth := lipgloss.Width(panel)
	panelHeight := lipgloss.Height(panel)
	left := max(0, (bodyW-panelWidth)/2)
	top := max(0, (bodyH-panelHeight)/4)
	return overlayBlock(body, panel, bodyW, bodyH, left, top)
}

func (m Model) renderProjectFilterPanel(bodyW int) string {
	panelWidth := min(bodyW, min(max(56, bodyW-12), 84))
	panelInnerWidth := max(24, panelWidth-4)
	return lipgloss.NewStyle().
		Width(panelWidth).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("81")).
		Padding(0, 1).
		Background(lipgloss.Color("235")).
		Foreground(lipgloss.Color("252")).
		Render(m.renderProjectFilterContent(panelInnerWidth))
}

func (m Model) renderProjectFilterContent(width int) string {
	dialog := m.projectFilterDialog
	if dialog == nil {
		return ""
	}

	input := dialog.Input
	input.Width = max(18, width)

	visibleLabel := fmt.Sprintf("%d visible", len(m.projects))
	if len(m.projects) == 1 {
		visibleLabel = "1 visible"
	}
	activeLabel := detailMutedStyle.Render("none")
	if label := compactProjectFilterLabel(m.projectFilter, max(12, width-18)); label != "" {
		activeLabel = detailValueStyle.Render(label)
	}

	lines := []string{
		commandPaletteTitleStyle.Render("Project Filter"),
		commandPaletteHintStyle.Render("Match any project name or folder name fragment. Delete the text to show everything again."),
		"",
		lipgloss.NewStyle().Width(max(18, width)).Render(input.View()),
		"",
		detailField("Active", activeLabel),
		detailField("Visible", detailMutedStyle.Render(visibleLabel)),
		"",
		renderDialogAction("Enter", "keep", commitActionKeyStyle, commitActionTextStyle) + "   " +
			renderDialogAction("Esc", "close", cancelActionKeyStyle, cancelActionTextStyle),
	}
	return strings.Join(lines, "\n")
}
