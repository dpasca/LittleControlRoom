package tui

import (
	"path/filepath"
	"strings"

	"lcroom/internal/model"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type runtimePaneActionKind string

const (
	runtimePaneActionOpenURL runtimePaneActionKind = "open-url"
	runtimePaneActionRestart runtimePaneActionKind = "restart"
	runtimePaneActionStop    runtimePaneActionKind = "stop"
)

type runtimePaneAction struct {
	Kind           runtimePaneActionKind
	Label          string
	Enabled        bool
	DisabledStatus string
}

func (m *Model) openRuntimeInspectorForSelection() tea.Cmd {
	if _, ok := m.selectedProject(); !ok {
		m.status = "No project selected"
		return nil
	}
	m.err = nil
	m.showHelp = false
	m.focusedPane = focusRuntime
	m.status = focusedPaneStatus(m.focusedPane)
	m.ensureSelectionVisible()
	m.syncDetailViewport(false)
	m.syncRuntimeViewport(true)
	return nil
}

func (m *Model) syncRuntimeViewport(reset bool) {
	layout := m.bodyLayout()
	projectPath := m.runtimePanelProjectPath()
	m.clampRuntimeActionSelection(projectPath)

	m.runtimeViewport.Width = layout.runtimeContentWidth
	innerHeight := max(1, layout.bottomPaneHeight-2)
	summaryLines := m.renderRuntimePanelSummary(layout.runtimeContentWidth, projectPath)
	outputHeight := max(3, innerHeight-len(summaryLines)-4)
	m.runtimeViewport.Height = outputHeight

	offset := m.runtimeViewport.YOffset
	m.runtimeViewport.SetContent(m.renderRuntimePanelOutputContent(layout.runtimeContentWidth, projectPath))
	if reset {
		m.runtimeViewport.GotoBottom()
		return
	}
	maxOffset := max(0, m.runtimeViewport.TotalLineCount()-m.runtimeViewport.Height)
	if offset > maxOffset {
		offset = maxOffset
	}
	m.runtimeViewport.SetYOffset(offset)
}

func (m *Model) moveRuntimeActionSelection(delta int) {
	actions := m.runtimePanelActions(m.runtimePanelProjectPath())
	if len(actions) == 0 || delta == 0 {
		return
	}
	selected := m.runtimeActionSelected % len(actions)
	if selected < 0 {
		selected += len(actions)
	}
	next := (selected + delta) % len(actions)
	if next < 0 {
		next += len(actions)
	}
	m.runtimeActionSelected = next
}

func (m *Model) clampRuntimeActionSelection(projectPath string) {
	actions := m.runtimePanelActions(projectPath)
	if len(actions) == 0 {
		m.runtimeActionSelected = 0
		return
	}
	if m.runtimeActionSelected < 0 {
		m.runtimeActionSelected = 0
	}
	if m.runtimeActionSelected >= len(actions) {
		m.runtimeActionSelected = len(actions) - 1
	}
}

func (m *Model) activateRuntimePaneAction() tea.Cmd {
	project, ok := m.selectedProject()
	if !ok {
		m.status = "No project selected"
		return nil
	}
	actions := m.runtimePanelActions(project.Path)
	if len(actions) == 0 {
		m.status = "No runtime actions available"
		return nil
	}
	m.clampRuntimeActionSelection(project.Path)
	action := actions[m.runtimeActionSelected]
	if !action.Enabled {
		m.status = action.DisabledStatus
		return nil
	}

	snapshot := m.projectRuntimeSnapshot(project.Path)
	switch action.Kind {
	case runtimePaneActionOpenURL:
		rawURL := runtimePrimaryURL(snapshot)
		if rawURL == "" {
			m.status = "No runtime URL or detected port to open"
			return nil
		}
		m.status = "Opening runtime URL in browser..."
		return m.openRuntimeURLInBrowserCmd(rawURL)
	case runtimePaneActionRestart:
		command := effectiveRuntimeCommand(project.RunCommand, snapshot)
		if command == "" {
			m.status = "Runtime command is not set"
			return nil
		}
		m.status = "Restarting runtime..."
		return m.restartProjectRuntimeCmd(project.Path, command)
	case runtimePaneActionStop:
		if !snapshot.Running {
			m.status = "Runtime is not running"
			return nil
		}
		m.status = "Stopping runtime..."
		return m.stopProjectRuntimeCmd(project.Path)
	default:
		m.status = "Runtime action unavailable"
		return nil
	}
}

func (m Model) renderRuntimePanel(width, height int) string {
	if height <= 0 {
		return ""
	}
	projectPath := m.runtimePanelProjectPath()
	summaryLines := m.renderRuntimePanelSummary(width, projectPath)
	contentLines := append([]string(nil), summaryLines...)
	contentLines = append(contentLines, "")
	contentLines = append(contentLines, m.renderRuntimePanelActionRow(width, projectPath))
	contentLines = append(contentLines, "")
	contentLines = append(contentLines, detailSectionStyle.Render("Output"))
	contentLines = append(contentLines, m.runtimeViewport.View())
	return fitPaneContent(strings.Join(contentLines, "\n"), width, height)
}

func (m Model) renderRuntimePanelSummary(width int, projectPath string) []string {
	if strings.TrimSpace(projectPath) == "" {
		return []string{
			detailSectionStyle.Render("Runtime"),
			detailMutedStyle.Render("Select a project to inspect its runtime"),
		}
	}

	project, _ := m.projectSummaryByPath(projectPath)
	snapshot := m.projectRuntimeSnapshot(projectPath)
	projectName := strings.TrimSpace(project.Name)
	if projectName == "" {
		projectName = filepath.Base(strings.TrimSpace(projectPath))
	}

	lines := []string{detailSectionStyle.Render("Runtime - " + projectName)}
	command := effectiveRuntimeCommand(project.RunCommand, snapshot)
	if command == "" && !runtimeDetailAvailable(project.RunCommand, snapshot) {
		lines = append(lines, detailMutedStyle.Render("Use /run or /run-edit to start a managed runtime"))
		return lines
	}

	commandStyle := detailValueStyle
	if command == "" {
		command = "not set"
		commandStyle = detailMutedStyle
	}
	lines = append(lines, renderWrappedRuntimeField("Run cmd", width, commandStyle, command)...)

	fields := []string{detailField("Runtime", renderRuntimeStatusValue(snapshot))}
	if snapshot.Running && !snapshot.StartedAt.IsZero() {
		fields = append(fields, detailField("Up", detailValueStyle.Render(formatRunningDuration(m.currentTime().Sub(snapshot.StartedAt)))))
	} else if !snapshot.ExitedAt.IsZero() {
		fields = append(fields, detailField("Stopped", detailMutedStyle.Render(snapshot.ExitedAt.Format(timeFieldFormat))))
	}
	lines = appendDetailFields(lines, width, fields...)

	infoFields := make([]string, 0, 2)
	if len(snapshot.Ports) > 0 {
		infoFields = append(infoFields, detailField("Ports", detailValueStyle.Render(joinPorts(snapshot.Ports))))
	}
	if urlSummary := runtimeURLSummary(snapshot); urlSummary != "" {
		infoFields = append(infoFields, detailField("URL", detailValueStyle.Render(urlSummary)))
	}
	lines = appendDetailFields(lines, width, infoFields...)

	if len(snapshot.AnnouncedURLs) > 1 {
		lines = append(lines, renderWrappedRuntimeField("More URLs", width, detailMutedStyle, strings.Join(snapshot.AnnouncedURLs[1:], ", "))...)
	}

	statusFields := make([]string, 0, 2)
	if len(snapshot.ConflictPorts) > 0 {
		statusFields = append(statusFields, detailField("Conflict", detailDangerStyle.Render(m.runtimeConflictSummary(projectPath, snapshot.ConflictPorts))))
	}
	if strings.TrimSpace(snapshot.LastError) != "" {
		statusFields = append(statusFields, detailField("Runtime err", detailDangerStyle.Render(snapshot.LastError)))
	}
	lines = appendDetailFields(lines, width, statusFields...)
	return lines
}

func (m Model) renderRuntimePanelOutputContent(width int, projectPath string) string {
	if strings.TrimSpace(projectPath) == "" {
		return detailMutedStyle.Render("Select a project to see runtime output")
	}
	project, _ := m.projectSummaryByPath(projectPath)
	snapshot := m.projectRuntimeSnapshot(projectPath)
	if len(snapshot.RecentOutput) == 0 {
		if !runtimeDetailAvailable(project.RunCommand, snapshot) {
			return detailMutedStyle.Render("No managed runtime yet")
		}
		return detailMutedStyle.Render("No captured runtime output yet")
	}
	lines := make([]string, 0, len(snapshot.RecentOutput))
	for _, line := range snapshot.RecentOutput {
		rendered := lipgloss.NewStyle().Width(max(1, width)).Render(strings.TrimRight(line, "\r\n"))
		parts := strings.Split(strings.ReplaceAll(rendered, "\r\n", "\n"), "\n")
		for _, part := range parts {
			lines = append(lines, detailMutedStyle.Render(part))
		}
	}
	return strings.Join(lines, "\n")
}

func (m Model) renderRuntimePanelActionRow(width int, projectPath string) string {
	actions := m.runtimePanelActions(projectPath)
	if len(actions) == 0 {
		return detailMutedStyle.Render("No runtime actions available")
	}
	parts := make([]string, 0, len(actions))
	for i, action := range actions {
		selected := m.focusedPane == focusRuntime && i == m.runtimeActionSelected
		parts = append(parts, renderRuntimePaneActionChip(action, selected))
	}
	return fitStyledWidth(strings.Join(parts, " "), width)
}

func renderRuntimePaneActionChip(action runtimePaneAction, selected bool) string {
	if !action.Enabled {
		return disabledActionKeyStyle.Render(action.Label)
	}
	if !selected {
		return lipgloss.NewStyle().
			Foreground(lipgloss.Color("252")).
			Background(lipgloss.Color("236")).
			Padding(0, 1).
			Bold(true).
			Render(action.Label)
	}
	switch action.Kind {
	case runtimePaneActionOpenURL:
		return pushActionKeyStyle.Render(action.Label)
	case runtimePaneActionStop:
		return cancelActionKeyStyle.Render(action.Label)
	default:
		return commitActionKeyStyle.Render(action.Label)
	}
}

func (m Model) runtimePanelProjectPath() string {
	project, ok := m.selectedProject()
	if !ok {
		return ""
	}
	return project.Path
}

func (m Model) runtimePanelActions(projectPath string) []runtimePaneAction {
	if strings.TrimSpace(projectPath) == "" {
		return nil
	}
	project, _ := m.projectSummaryByPath(projectPath)
	snapshot := m.projectRuntimeSnapshot(projectPath)
	command := effectiveRuntimeCommand(project.RunCommand, snapshot)
	return []runtimePaneAction{
		{
			Kind:           runtimePaneActionOpenURL,
			Label:          "Open URL",
			Enabled:        runtimePrimaryURL(snapshot) != "",
			DisabledStatus: "No runtime URL or detected port to open",
		},
		{
			Kind:           runtimePaneActionRestart,
			Label:          "Restart",
			Enabled:        command != "",
			DisabledStatus: "Runtime command is not set",
		},
		{
			Kind:           runtimePaneActionStop,
			Label:          "Stop",
			Enabled:        snapshot.Running,
			DisabledStatus: "Runtime is not running",
		},
	}
}

func renderWrappedRuntimeField(label string, width int, valueStyle lipgloss.Style, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	plainLabel := label + ":"
	labelWidth := lipgloss.Width(plainLabel + " ")
	labelRendered := detailLabelStyle.Render(plainLabel)
	if width <= labelWidth+4 {
		return []string{labelRendered + " " + valueStyle.Render(value)}
	}
	wrapped := lipgloss.NewStyle().Width(max(1, width-labelWidth)).Render(value)
	parts := strings.Split(strings.ReplaceAll(wrapped, "\r\n", "\n"), "\n")
	lines := make([]string, 0, len(parts))
	continuation := strings.Repeat(" ", labelWidth)
	for i, part := range parts {
		if i == 0 {
			lines = append(lines, labelRendered+" "+valueStyle.Render(part))
			continue
		}
		lines = append(lines, continuation+valueStyle.Render(part))
	}
	return lines
}

func (m Model) projectSummaryByPath(projectPath string) (model.ProjectSummary, bool) {
	projectPath = filepath.Clean(strings.TrimSpace(projectPath))
	if projectPath == "" {
		return model.ProjectSummary{}, false
	}
	for _, project := range m.projects {
		if filepath.Clean(strings.TrimSpace(project.Path)) == projectPath {
			return project, true
		}
	}
	for _, project := range m.allProjects {
		if filepath.Clean(strings.TrimSpace(project.Path)) == projectPath {
			return project, true
		}
	}
	return model.ProjectSummary{}, false
}
