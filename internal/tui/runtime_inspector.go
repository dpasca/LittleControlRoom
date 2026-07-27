package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	"lcroom/internal/model"
	"lcroom/internal/projectrun"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

type runtimePaneActionKind string

const (
	runtimePaneActionOpenURL    runtimePaneActionKind = "open-url"
	runtimePaneActionCopyOutput runtimePaneActionKind = "copy-output"
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
	m.focusedPane = focusRuntime
	m.status = focusedPaneStatus(m.focusedPane)
	m.ensureSelectionVisible()
	m.syncDetailViewport(false)
	m.syncRuntimeViewport(true)
	return m.requestRuntimeSnapshotsRefreshCmd()
}

func (m *Model) syncRuntimeViewport(reset bool) {
	done := m.beginUIPhase("syncRuntimeViewport", m.currentLatencyProjectPath(), fmt.Sprintf("reset=%t", reset))
	defer done()
	layout := m.bodyLayout()
	projectPath := m.runtimePanelProjectPath()
	m.clampRuntimeActionSelection(projectPath)

	m.runtimeViewport.Width = layout.runtimeContentWidth
	innerHeight := max(1, layout.bottomPaneHeight-2)
	summaryLines := m.renderRuntimePanelSummary(layout.runtimeContentWidth, projectPath)
	actionResult := m.renderRuntimePanelActionRows(layout.runtimeContentWidth, projectPath)
	m.runtimeActionHits = actionResult.hits
	outputHeight := max(3, innerHeight-len(summaryLines)-len(actionResult.lines)-3)
	m.runtimeViewport.Height = outputHeight
	if m.codexVisible() {
		if reset {
			m.runtimeViewport.GotoBottom()
		}
		return
	}

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

// activateRuntimeActionByKind finds the first action matching kind and runs it
// directly, bypassing the arrow-key selection index. Used by keyboard shortcuts.
func (m *Model) activateRuntimeActionByKind(kind runtimePaneActionKind) tea.Cmd {
	project, ok := m.selectedProject()
	if !ok {
		m.status = "No project selected"
		return nil
	}
	actions := m.runtimePanelActions(project.Path)
	for i, action := range actions {
		if action.Kind == kind {
			m.runtimeActionSelected = i
			m.clampRuntimeActionSelection(project.Path)
			return m.activateRuntimePaneAction()
		}
	}
	m.status = "Action not available"
	return nil
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
	if m.focusedPane == focusRuntime {
		snapshot, _, _ = m.selectedRuntimeProcessSnapshot(project.Path)
	}
	switch action.Kind {
	case runtimePaneActionOpenURL:
		rawURL := runtimePrimaryURL(snapshot)
		if rawURL == "" {
			m.status = "No runtime URL or detected port to open"
			return nil
		}
		m.status = "Opening runtime URL in browser..."
		return m.openRuntimeURLInBrowserCmd(rawURL)
	case runtimePaneActionCopyOutput:
		return m.copyRuntimeOutput(project.Path, snapshot)
	default:
		m.status = "Runtime action unavailable"
		return nil
	}
}

func (m Model) renderRuntimePanel(width, height int) string {
	if height <= 0 {
		return ""
	}
	if flair, ok := m.renderRuntimeFlairPanel(width, height); ok {
		return fitPaneContent(flair, width, height)
	}
	projectPath := m.runtimePanelProjectPath()
	summaryLines := m.renderRuntimePanelSummary(width, projectPath)
	contentLines := append([]string(nil), summaryLines...)
	contentLines = append(contentLines, "")
	actionResult := m.renderRuntimePanelActionRows(width, projectPath)
	m.runtimeActionHits = actionResult.hits
	contentLines = append(contentLines, actionResult.lines...)
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
	snapshot, processIndex, processTotal := m.selectedRuntimeProcessSnapshot(projectPath)
	projectName := strings.TrimSpace(project.Name)
	if projectName == "" {
		projectName = filepath.Base(strings.TrimSpace(projectPath))
	}

	lines := []string{detailSectionStyle.Render("Runtime - " + projectName)}
	command := effectiveRuntimeCommand(project.RunCommand, snapshot)
	if command == "" && !runtimeDetailAvailable(project.RunCommand, snapshot) {
		lines = append(lines, detailMutedStyle.Render("Use /run, /start, or /run-edit to start a managed runtime"))
		return lines
	}

	commandStyle := detailValueStyle
	if command == "" {
		command = "not set"
		commandStyle = detailMutedStyle
	}
	lines = append(lines, renderWrappedRuntimeField("Run cmd", width, commandStyle, command)...)
	if cwd := runtimeRelativeCWD(projectPath, snapshot.CWD); cwd != "" {
		lines = append(lines, renderWrappedRuntimeField("CWD", width, detailMutedStyle, cwd)...)
	}
	if processTotal > 1 || snapshot.External {
		processText := fmt.Sprintf("%d/%d  %s", processIndex+1, processTotal, runtimeProcessLabel(snapshot))
		if processTotal <= 1 {
			processText = runtimeProcessLabel(snapshot)
		}
		lines = append(lines, renderWrappedRuntimeField("Process", width, detailMutedStyle, processText)...)
	}
	if listeners := m.projectLocalInstanceDetailSummary(projectPath, snapshot.ID); listeners != "" {
		lines = append(lines, renderWrappedRuntimeField("Local listeners", width, detailValueStyle, listeners)...)
	}

	runtimeStatus := renderRuntimeStatusValue(snapshot)
	if snapshot.Running && !snapshot.StartedAt.IsZero() {
		runtimeStatus += " " + detailMutedStyle.Render("(up "+formatRunningDuration(m.currentTime().Sub(snapshot.StartedAt))+")")
	} else if !snapshot.ExitedAt.IsZero() {
		runtimeStatus += " " + detailMutedStyle.Render("(stopped "+snapshot.ExitedAt.Format(timeFieldFormat)+")")
	}
	lines = append(lines, detailField("Runtime", runtimeStatus))

	if urlSummary := runtimeURLSummary(snapshot); urlSummary != "" {
		if len(snapshot.Ports) > 0 {
			urlSummary += " " + detailMutedStyle.Render("(ports: "+joinPorts(snapshot.Ports)+")")
		}
		lines = append(lines, detailField("URL", detailValueStyle.Render(urlSummary)))
	} else if len(snapshot.Ports) > 0 {
		lines = append(lines, detailField("Ports", detailValueStyle.Render(joinPorts(snapshot.Ports))))
	}

	if len(snapshot.AnnouncedURLs) > 1 {
		lines = append(lines, renderWrappedRuntimeField("More URLs", width, detailMutedStyle, strings.Join(snapshot.AnnouncedURLs[1:], ", "))...)
	}

	statusFields := make([]string, 0, 2)
	if len(snapshot.ConflictPorts) > 0 {
		statusFields = append(statusFields, detailField("Conflict", detailDangerStyle.Render(m.runtimeConflictSummary(projectPath, snapshot.ID, snapshot.ConflictPorts))))
	}
	if strings.TrimSpace(snapshot.LastError) != "" {
		statusFields = append(statusFields, detailField("Runtime err", detailDangerStyle.Render(snapshot.LastError)))
	}
	lines = appendDetailFields(lines, width, statusFields...)
	if summary := m.projectProcessWarningSummary(projectPath); summary != "" {
		lines = append(lines, renderWrappedRuntimeField("Processes", width, detailWarningStyle, summary)...)
	}
	return lines
}

func (m Model) renderRuntimePanelOutputContent(width int, projectPath string) string {
	if strings.TrimSpace(projectPath) == "" {
		return detailMutedStyle.Render("Select a project to see runtime output")
	}
	project, _ := m.projectSummaryByPath(projectPath)
	snapshot, _, _ := m.selectedRuntimeProcessSnapshot(projectPath)
	if len(snapshot.RecentOutput) == 0 {
		if !runtimeDetailAvailable(project.RunCommand, snapshot) {
			return detailMutedStyle.Render("No managed runtime yet")
		}
		if snapshot.External {
			return detailMutedStyle.Render("External listener output is not captured")
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

// runtimeActionHit describes where a single action chip was rendered within a
// row so that mouse clicks can be mapped back to the correct action index.
type runtimeActionHit struct {
	actionIndex int
	row         int
	xStart      int
	xEnd        int
}

type runtimeActionRowResult struct {
	lines []string
	hits  []runtimeActionHit // per-row hit map for the mouse handler
}

func (m Model) renderRuntimePanelActionRows(width int, projectPath string) runtimeActionRowResult {
	actions := m.runtimePanelActions(projectPath)
	if len(actions) == 0 {
		return runtimeActionRowResult{
			lines: []string{detailMutedStyle.Render("No runtime actions available")},
		}
	}
	width = max(1, width)
	var result runtimeActionRowResult
	line := ""
	lineHits := make([]runtimeActionHit, 0, 4)
	cursor := 0
	currentRow := 0
	flushRow := func() {
		if line != "" {
			result.lines = append(result.lines, fitStyledWidth(line, width))
			result.hits = append(result.hits, lineHits...)
			line = ""
			lineHits = lineHits[:0]
			cursor = 0
			currentRow++
		}
	}
	for i, action := range actions {
		selected := m.focusedPane == focusRuntime && i == m.runtimeActionSelected
		chip := renderRuntimePaneActionChip(action, selected)
		chipW := lipgloss.Width(chip)
		if line == "" {
			line = chip
			lineHits = append(lineHits, runtimeActionHit{actionIndex: i, row: currentRow, xStart: 0, xEnd: chipW})
			cursor = chipW
			continue
		}
		if cursor+1+chipW > width {
			flushRow()
			line = chip
			lineHits = append(lineHits, runtimeActionHit{actionIndex: i, row: currentRow, xStart: 0, xEnd: chipW})
			cursor = chipW
			continue
		}
		line += " " + chip
		lineHits = append(lineHits, runtimeActionHit{actionIndex: i, row: currentRow, xStart: cursor + 1, xEnd: cursor + 1 + chipW})
		cursor += 1 + chipW
	}
	flushRow()
	return result
}

// handleRuntimePaneMouse maps mouse events over the runtime pane to pane
// focus, output scrolling, and action chip activation. Without it, clicks on
// the pane fall through to the codex transcript handlers and the action
// chips can never be selected with the mouse.
func (m *Model) handleRuntimePaneMouse(msg tea.MouseMsg) (tea.Cmd, bool) {
	// Mouse tracking is only enabled for the full-screen embedded session,
	// help chat, and diff views, so the dashboard geometry below describes a
	// body that is not on screen. Hit-testing it anyway let clicks in the
	// embedded transcript/composer area silently steal dashboard pane focus
	// for the runtime pane, which then showed up as the Run panel being
	// highlighted after leaving the session with Esc.
	if m.codexVisible() {
		return nil, false
	}
	layout := m.bodyLayout()
	contentWidth := layout.runtimeContentWidth
	innerHeight := max(1, layout.bottomPaneHeight-2)
	if _, flair := m.renderRuntimeFlairPanel(contentWidth, innerHeight); flair {
		return nil, false
	}
	paneX := layout.detailPaneWidth + 1
	paneY := 1 + layout.listPaneHeight
	if msg.X < paneX || msg.X >= paneX+layout.runtimePaneWidth ||
		msg.Y < paneY || msg.Y >= paneY+layout.bottomPaneHeight {
		return nil, false
	}
	if msg.Button == tea.MouseButtonWheelUp || msg.Button == tea.MouseButtonWheelDown {
		var cmd tea.Cmd
		m.runtimeViewport, cmd = m.runtimeViewport.Update(msg)
		return cmd, true
	}
	if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
		return nil, false
	}
	m.focusedPane = focusRuntime
	m.status = focusedPaneStatus(focusRuntime)
	projectPath := m.runtimePanelProjectPath()
	actions := m.runtimePanelActions(projectPath)
	if len(actions) == 0 {
		return nil, true
	}
	contentX := msg.X - (paneX + 2)
	if contentX < 0 || contentX >= contentWidth {
		return nil, true
	}
	// Compute the action-row hit map fresh here. renderRuntimePanel (called
	// by View()) has a value receiver so it stores hits on a discarded copy;
	// we need the pointer receiver *Model to carry the hit state forward.
	actionResult := m.renderRuntimePanelActionRows(contentWidth, projectPath)
	m.runtimeActionHits = actionResult.hits
	summaryRows := len(m.renderRuntimePanelSummary(contentWidth, projectPath))
	chipAreaStartY := paneY + 1 + summaryRows + 1 // blank separator
	clickRow := msg.Y - chipAreaStartY
	for _, hit := range m.runtimeActionHits {
		if hit.row != clickRow {
			continue
		}
		if contentX >= hit.xStart && contentX < hit.xEnd {
			m.runtimeActionSelected = hit.actionIndex
			return m.activateRuntimePaneAction(), true
		}
	}
	return nil, true
}

func renderRuntimePaneActionChip(action runtimePaneAction, selected bool) string {
	chipLabel := action.Label
	if !action.Enabled {
		return disabledActionKeyStyle.Render(chipLabel)
	}
	if !selected {
		return lipgloss.NewStyle().
			Foreground(lipgloss.Color("252")).
			Background(lipgloss.Color("236")).
			Padding(0, 1).
			Bold(true).
			Render(chipLabel)
	}
	return pushActionKeyStyle.Render(chipLabel)
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
	_, _ = m.projectSummaryByPath(projectPath)
	snapshot, _, _ := m.selectedRuntimeProcessSnapshot(projectPath)
	actions := []runtimePaneAction{
		{
			Kind:           runtimePaneActionOpenURL,
			Label:          "Open",
			Enabled:        runtimePrimaryURL(snapshot) != "",
			DisabledStatus: "No runtime URL or detected port to open",
		},
	}
	if runtimeOutputAvailable(snapshot) {
		actions = append(actions,
			runtimePaneAction{
				Kind:    runtimePaneActionCopyOutput,
				Label:   "Copy",
				Enabled: true,
			},
		)
	}
	return actions
}

func (m *Model) copySelectedRuntimeOutput() tea.Cmd {
	project, ok := m.selectedProject()
	if !ok {
		m.status = "No project selected"
		return nil
	}
	snapshot, _, _ := m.selectedRuntimeProcessSnapshot(project.Path)
	return m.copyRuntimeOutput(project.Path, snapshot)
}

func (m *Model) copyRuntimeOutput(projectPath string, snapshot projectrun.Snapshot) tea.Cmd {
	output := runtimeCapturedOutput(snapshot)
	if output == "" {
		m.status = "No captured runtime output to copy"
		return nil
	}
	if m.runtimeOutputCopyBusy {
		m.status = "Runtime output copy already in progress"
		return nil
	}
	m.runtimeOutputCopyBusy = true
	m.status = "Copying runtime output..."
	return func() tea.Msg {
		return runtimeOutputCopyMsg{
			projectPath: projectPath,
			err:         clipboardTextWriter(output),
		}
	}
}

func (m Model) applyRuntimeOutputCopyMsg(msg runtimeOutputCopyMsg) (tea.Model, tea.Cmd) {
	m.runtimeOutputCopyBusy = false
	if msg.err != nil {
		m.reportError("Runtime output copy failed", msg.err, msg.projectPath)
		return m, nil
	}
	m.err = nil
	m.status = "Copied runtime output to clipboard"
	return m, nil
}

func (m *Model) openRuntimeOutputTodo(project model.ProjectSummary, snapshot projectrun.Snapshot) tea.Cmd {
	if m.todoPendingSave != nil {
		m.status = "TODO save already in progress"
		return nil
	}
	if runtimeCapturedOutput(snapshot) == "" {
		m.status = "No captured runtime output to add to a TODO"
		return nil
	}
	todoText := formatRuntimeOutputTodo(project, snapshot)
	project = m.repositoryTodoProject(project)
	loadCmd := m.openTodoDialog(project)
	focusCmd := m.openTodoEditor(0, todoText, nil)
	m.status = "Review the runtime output TODO, then press Ctrl+S to add it"
	return batchCmds(loadCmd, focusCmd)
}

func runtimeOutputAvailable(snapshot projectrun.Snapshot) bool {
	return len(snapshot.RecentOutput) > 0 || strings.TrimSpace(snapshot.LastError) != ""
}

func runtimeCapturedOutput(snapshot projectrun.Snapshot) string {
	output := runtimeRecentOutput(snapshot)
	if output != "" {
		return output
	}
	return cleanRuntimeOutputText(snapshot.LastError)
}

func runtimeRecentOutput(snapshot projectrun.Snapshot) string {
	lines := make([]string, 0, len(snapshot.RecentOutput))
	for _, line := range snapshot.RecentOutput {
		line = cleanRuntimeOutputText(line)
		if line != "" {
			lines = append(lines, line)
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func cleanRuntimeOutputText(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	return strings.TrimSpace(ansi.Strip(text))
}

func formatRuntimeOutputTodo(project model.ProjectSummary, snapshot projectrun.Snapshot) string {
	title := "Investigate this runtime output."
	if (snapshot.ExitCodeKnown && snapshot.ExitCode != 0) || strings.TrimSpace(snapshot.LastError) != "" {
		title = "Investigate and fix this runtime failure."
	}
	lines := []string{title}
	if command := capturedRuntimeCommand(project.RunCommand, snapshot); command != "" {
		lines = append(lines, "", "Run command: "+truncateText(command, 2000))
	}
	if cwd := cleanRuntimeOutputText(runtimeRelativeCWD(snapshot.ProjectPath, snapshot.CWD)); cwd != "" {
		lines = append(lines, "Working directory: "+truncateText(cwd, 1000))
	}
	if process := cleanRuntimeOutputText(runtimeProcessLabel(snapshot)); process != "" {
		lines = append(lines, "Runtime process: "+truncateText(process, 500))
	}
	if snapshot.ExitCodeKnown {
		lines = append(lines, fmt.Sprintf("Exit code: %d", snapshot.ExitCode))
	}
	if lastError := cleanRuntimeOutputText(snapshot.LastError); lastError != "" {
		lines = append(lines, "Error: "+truncateText(lastError, 4000))
	}

	prefix := strings.Join(lines, "\n")
	output := runtimeRecentOutput(snapshot)
	if output == "" {
		return truncateText(prefix, todoTextCharLimit)
	}
	prefix += "\n\nRecent run output:\n\n"
	return prefix + runtimeTodoOutputWithinLimit(prefix, output)
}

func capturedRuntimeCommand(savedCommand string, snapshot projectrun.Snapshot) string {
	if command := cleanRuntimeOutputText(snapshot.Command); command != "" {
		return command
	}
	return cleanRuntimeOutputText(savedCommand)
}

func runtimeTodoOutputWithinLimit(prefix, output string) string {
	remaining := todoTextCharLimit - len([]rune(prefix))
	if remaining <= 0 {
		return ""
	}
	outputRunes := []rune(output)
	if len(outputRunes) <= remaining {
		return output
	}
	const marker = "[Earlier captured runtime output omitted to fit the TODO editor.]\n"
	markerRunes := []rune(marker)
	if remaining <= len(markerRunes) {
		return string(outputRunes[len(outputRunes)-remaining:])
	}
	tailLength := remaining - len(markerRunes)
	return marker + string(outputRunes[len(outputRunes)-tailLength:])
}

func runtimeRestartDisabledStatus(snapshot projectrun.Snapshot, command string) string {
	if snapshot.External {
		return "Stop the external listener before restarting as a managed runtime"
	}
	if strings.TrimSpace(command) == "" {
		return "Runtime command is not set"
	}
	return "Runtime restart unavailable"
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
	for _, project := range m.archivedProjects {
		if filepath.Clean(strings.TrimSpace(project.Path)) == projectPath {
			return project, true
		}
	}
	return model.ProjectSummary{}, false
}
