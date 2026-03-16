package tui

import (
	"path/filepath"
	"strings"

	"lcroom/internal/model"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type runtimeInspectorState struct {
	ProjectPath string
	Viewport    viewport.Model
}

func (m *Model) openRuntimeInspectorForSelection() tea.Cmd {
	project, ok := m.selectedProject()
	if !ok {
		m.status = "No project selected"
		return nil
	}
	snapshot := m.projectRuntimeSnapshot(project.Path)
	if !runtimeDetailAvailable(project.RunCommand, snapshot) {
		m.status = "No runtime details yet. Use /run to start one"
		return nil
	}
	m.err = nil
	m.showHelp = false
	m.runtimeInspector = &runtimeInspectorState{
		ProjectPath: project.Path,
		Viewport:    viewport.New(0, 0),
	}
	m.syncRuntimeInspectorViewport(true)
	m.status = "Runtime panel open"
	return nil
}

func (m *Model) closeRuntimeInspector(status string) {
	m.runtimeInspector = nil
	if status != "" {
		m.status = status
	}
}

func (m Model) updateRuntimeInspectorMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.runtimeInspector == nil {
		return m, nil
	}
	switch msg.String() {
	case "esc":
		m.closeRuntimeInspector("Runtime panel closed")
		return m, nil
	case "up", "k":
		m.runtimeInspector.Viewport.LineUp(1)
		return m, nil
	case "down", "j":
		m.runtimeInspector.Viewport.LineDown(1)
		return m, nil
	case "pgup":
		m.runtimeInspector.Viewport.PageUp()
		return m, nil
	case "pgdown":
		m.runtimeInspector.Viewport.PageDown()
		return m, nil
	case "home":
		m.runtimeInspector.Viewport.GotoTop()
		return m, nil
	case "end":
		m.runtimeInspector.Viewport.GotoBottom()
		return m, nil
	case "o":
		rawURL := m.runtimeInspectorOpenURL()
		if rawURL == "" {
			m.status = "No runtime URL or detected port to open"
			return m, nil
		}
		m.status = "Opening runtime URL in browser..."
		return m, m.openRuntimeURLInBrowserCmd(rawURL)
	case "s":
		m.status = "Stopping runtime..."
		return m, m.stopProjectRuntimeCmd(m.runtimeInspector.ProjectPath)
	case "r":
		command := m.runtimeInspectorCommand()
		if command == "" {
			m.status = "Runtime command is not set"
			return m, nil
		}
		m.status = "Restarting runtime..."
		return m, m.restartProjectRuntimeCmd(m.runtimeInspector.ProjectPath, command)
	}
	return m, nil
}

func (m *Model) syncRuntimeInspectorViewport(reset bool) {
	if m.runtimeInspector == nil {
		return
	}
	_, _, innerWidth, innerHeight := runtimeInspectorPanelDimensions(m.width, m.height)
	summaryLines := m.renderRuntimeInspectorSummary(innerWidth, m.runtimeInspector.ProjectPath)
	outputHeight := max(4, innerHeight-len(summaryLines)-2)

	state := m.runtimeInspector
	state.Viewport.Width = innerWidth
	state.Viewport.Height = outputHeight
	offset := state.Viewport.YOffset
	state.Viewport.SetContent(m.renderRuntimeInspectorOutputContent(innerWidth, state.ProjectPath))
	if reset {
		state.Viewport.GotoBottom()
		return
	}
	maxOffset := max(0, state.Viewport.TotalLineCount()-state.Viewport.Height)
	if offset > maxOffset {
		offset = maxOffset
	}
	state.Viewport.SetYOffset(offset)
}

func runtimeInspectorPanelDimensions(width, height int) (panelWidth, panelHeight, innerWidth, innerHeight int) {
	if width <= 0 {
		width = 120
	}
	if height <= 0 {
		height = 30
	}
	bodyHeight := height - 2
	if bodyHeight < 12 {
		bodyHeight = 12
	}
	panelWidth = min(width, min(max(72, width-8), 112))
	panelHeight = min(bodyHeight, max(16, bodyHeight-2))
	innerWidth = max(24, panelWidth-4)
	innerHeight = max(8, panelHeight-2)
	return panelWidth, panelHeight, innerWidth, innerHeight
}

func (m Model) renderRuntimeInspectorOverlay(body string, bodyW, bodyH int) string {
	if m.runtimeInspector == nil {
		return body
	}
	panelWidth, _, innerWidth, _ := runtimeInspectorPanelDimensions(bodyW, bodyH+2)
	summaryLines := m.renderRuntimeInspectorSummary(innerWidth, m.runtimeInspector.ProjectPath)
	contentLines := append(summaryLines, "")
	contentLines = append(contentLines, detailSectionStyle.Render("Output"))
	contentLines = append(contentLines, m.runtimeInspector.Viewport.View())
	panel := lipgloss.NewStyle().
		Width(panelWidth).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("81")).
		Padding(0, 1).
		Background(lipgloss.Color("235")).
		Foreground(lipgloss.Color("252")).
		Render(strings.Join(contentLines, "\n"))
	panelWidth = lipgloss.Width(panel)
	panelHeight := lipgloss.Height(panel)
	left := max(0, (bodyW-panelWidth)/2)
	top := max(0, (bodyH-panelHeight)/3)
	return overlayBlock(body, panel, bodyW, bodyH, left, top)
}

func (m Model) renderRuntimeInspectorSummary(width int, projectPath string) []string {
	project, _ := m.projectSummaryByPath(projectPath)
	snapshot := m.projectRuntimeSnapshot(projectPath)
	projectName := strings.TrimSpace(project.Name)
	if projectName == "" {
		projectName = filepath.Base(strings.TrimSpace(projectPath))
	}

	lines := []string{dialogProjectTitleStyle.Render(projectName + " Runtime")}
	lines = append(lines, renderWrappedRuntimeField("Path", width, detailValueStyle, projectPath)...)

	command := effectiveRuntimeCommand(project.RunCommand, snapshot)
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

	if len(snapshot.Ports) > 0 {
		lines = append(lines, detailField("Ports", detailValueStyle.Render(joinPorts(snapshot.Ports))))
	}
	if len(snapshot.AnnouncedURLs) > 0 {
		lines = append(lines, renderWrappedRuntimeField("URLs", width, detailValueStyle, strings.Join(snapshot.AnnouncedURLs, ", "))...)
	}
	if len(snapshot.ConflictPorts) > 0 {
		lines = append(lines, renderWrappedRuntimeField("Conflict", width, detailDangerStyle, m.runtimeConflictSummary(projectPath, snapshot.ConflictPorts))...)
	}
	if strings.TrimSpace(snapshot.LastError) != "" {
		lines = append(lines, renderWrappedRuntimeField("Runtime err", width, detailDangerStyle, snapshot.LastError)...)
	}
	return lines
}

func (m Model) renderRuntimeInspectorOutputContent(width int, projectPath string) string {
	snapshot := m.projectRuntimeSnapshot(projectPath)
	if len(snapshot.RecentOutput) == 0 {
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

func (m Model) runtimeInspectorCommand() string {
	if m.runtimeInspector == nil {
		return ""
	}
	project, _ := m.projectSummaryByPath(m.runtimeInspector.ProjectPath)
	return effectiveRuntimeCommand(project.RunCommand, m.projectRuntimeSnapshot(m.runtimeInspector.ProjectPath))
}

func (m Model) runtimeInspectorOpenURL() string {
	if m.runtimeInspector == nil {
		return ""
	}
	return runtimePrimaryURL(m.projectRuntimeSnapshot(m.runtimeInspector.ProjectPath))
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
