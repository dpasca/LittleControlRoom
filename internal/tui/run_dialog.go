package tui

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"lcroom/internal/model"
	"lcroom/internal/projectrun"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type runCommandDialogState struct {
	ProjectPath       string
	ProjectName       string
	Input             textinput.Model
	Suggestions       []projectrun.Suggestion
	SuggestionReason  string
	SuggestionError   string
	SuggestionPending bool
	SuggestionChecked bool
	SuggestionSeq     int64
	StartAfterSave    bool
	Submitting        bool
}

func (m Model) handleRunCommand(project model.ProjectSummary, command string) (tea.Model, tea.Cmd) {
	command = strings.TrimSpace(command)
	if command != "" {
		m.status = "Saving run command and starting runtime..."
		return m, m.saveRunCommandCmd(project.Path, command, true)
	}

	if snapshot := m.projectRuntimeSnapshot(project.Path); snapshot.Running {
		m.status = "Runtime already running"
		return m, nil
	}

	if strings.TrimSpace(project.RunCommand) == "" {
		return m, m.openRunCommandDialog(project, true)
	}

	m.status = "Starting runtime..."
	return m, m.startProjectRuntimeCmd(project.Path, project.RunCommand)
}

func (m *Model) openRunCommandDialog(project model.ProjectSummary, startAfterSave bool) tea.Cmd {
	command := strings.TrimSpace(project.RunCommand)

	input := textinput.New()
	input.Prompt = ""
	input.Placeholder = "pnpm dev"
	input.CharLimit = 4096
	input.Width = 72
	input.ShowSuggestions = true
	input.SetValue(command)

	m.runCommandDialog = &runCommandDialogState{
		ProjectPath:       project.Path,
		ProjectName:       projectTitle(project.Path, project.Name),
		Input:             input,
		SuggestionPending: true,
		SuggestionSeq:     1,
		StartAfterSave:    startAfterSave,
	}
	m.commandMode = false
	m.err = nil
	if startAfterSave {
		m.status = "Set a run command for this project"
	} else {
		m.status = "Editing saved run command"
	}
	focusCmd := m.runCommandDialog.Input.Focus()
	return batchCmds(focusCmd, m.loadRunCommandSuggestionsCmd(project.Path, m.runCommandDialog.SuggestionSeq))
}

func (m *Model) closeRunCommandDialog(status string) {
	if m.runCommandDialog != nil {
		m.runCommandDialog.Input.Blur()
	}
	m.runCommandDialog = nil
	if status != "" {
		m.status = status
	}
}

func (m *Model) applyRunCommandSavedLocal(projectPath, command string) {
	projectPath = normalizeProjectPath(projectPath)
	if projectPath == "" {
		return
	}
	command = strings.TrimSpace(command)
	updateRunCommandInSummaries(m.allProjects, projectPath, command)
	updateRunCommandInSummaries(m.archivedProjects, projectPath, command)
	updateRunCommandInSummaries(m.projects, projectPath, command)
	if normalizeProjectPath(m.detail.Summary.Path) == projectPath {
		m.detail.Summary.RunCommand = command
	}
	m.rebuildProjectList(m.currentSelectedProjectPath())
}

func updateRunCommandInSummaries(projects []model.ProjectSummary, projectPath, command string) {
	for i := range projects {
		if normalizeProjectPath(projects[i].Path) == projectPath {
			projects[i].RunCommand = command
		}
	}
}

func (m Model) updateRunCommandDialogMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	dialog := m.runCommandDialog
	if dialog == nil {
		return m, nil
	}
	if dialog.Submitting {
		return m, nil
	}

	switch msg.String() {
	case "esc":
		m.closeRunCommandDialog("Run command edit canceled")
		return m, nil
	case "enter":
		command := strings.TrimSpace(dialog.Input.Value())
		if command == "" {
			m.status = "Run command is required"
			return m, nil
		}
		projectPath := dialog.ProjectPath
		startAfterSave := dialog.StartAfterSave
		m.closeRunCommandDialog("")
		if dialog.StartAfterSave {
			m.status = "Saving run command and starting runtime..."
		} else {
			m.status = "Saving run command..."
		}
		return m, m.saveRunCommandCmd(projectPath, command, startAfterSave)
	}

	var cmd tea.Cmd
	dialog.Input, cmd = dialog.Input.Update(msg)
	dialog.SuggestionReason = currentRunCommandSuggestionReason(dialog)
	return m, cmd
}

func (m Model) saveRunCommandCmd(projectPath, command string, startAfter bool) tea.Cmd {
	if m.svc == nil {
		return func() tea.Msg {
			return runCommandSavedMsg{
				projectPath: projectPath,
				command:     command,
				startAfter:  startAfter,
				err:         fmt.Errorf("service unavailable"),
			}
		}
	}
	return func() tea.Msg {
		ctx, cancel := m.actionContext(tuiQuickActionTimeout)
		defer cancel()
		err := m.svc.SetRunCommand(ctx, projectPath, command)
		err = timeoutActionError(err, tuiQuickActionTimeout, "saving the run command")
		return runCommandSavedMsg{
			projectPath: projectPath,
			command:     command,
			startAfter:  startAfter,
			err:         err,
		}
	}
}

func (m Model) loadRunCommandSuggestionsCmd(projectPath string, seq int64) tea.Cmd {
	projectPath = strings.TrimSpace(projectPath)
	if projectPath == "" {
		return nil
	}
	return func() tea.Msg {
		suggestions, err := projectrun.Completions(projectPath)
		return runCommandSuggestionMsg{
			projectPath: projectPath,
			seq:         seq,
			suggestions: suggestions,
			err:         err,
		}
	}
}

func currentRunCommandSuggestionReason(dialog *runCommandDialogState) string {
	if dialog == nil {
		return ""
	}
	current := strings.TrimSpace(dialog.Input.CurrentSuggestion())
	if current == "" {
		return ""
	}
	for _, suggestion := range dialog.Suggestions {
		if strings.TrimSpace(suggestion.Command) == current {
			return strings.TrimSpace(suggestion.Reason)
		}
	}
	return ""
}

func matchingRunCommandSuggestions(dialog *runCommandDialogState) []projectrun.Suggestion {
	if dialog == nil {
		return nil
	}
	matchedCommands := dialog.Input.MatchedSuggestions()
	if len(matchedCommands) == 0 {
		return nil
	}

	byCommand := make(map[string]projectrun.Suggestion, len(dialog.Suggestions))
	for _, suggestion := range dialog.Suggestions {
		command := strings.TrimSpace(suggestion.Command)
		if command != "" {
			byCommand[command] = suggestion
		}
	}

	matched := make([]projectrun.Suggestion, 0, len(matchedCommands))
	for _, command := range matchedCommands {
		command = strings.TrimSpace(command)
		if suggestion, ok := byCommand[command]; ok {
			matched = append(matched, suggestion)
		}
	}
	return matched
}

func (m Model) startProjectRuntimeCmd(projectPath, command string) tea.Cmd {
	return func() tea.Msg {
		if m.runtimeManager == nil {
			return runtimeActionMsg{projectPath: projectPath, err: fmt.Errorf("runtime manager unavailable")}
		}
		result, err := m.runtimeManager.StartManaged(projectrun.StartRequest{
			ProjectPath:   projectPath,
			Command:       command,
			ReuseMatching: true,
		})
		if err != nil {
			return runtimeActionMsg{projectPath: projectPath, err: fmt.Errorf("start runtime: %w", err)}
		}
		return runtimeActionMsg{
			projectPath: projectPath,
			status:      runtimeStartResultStatus(result),
		}
	}
}

func (m Model) stopProjectRuntimeCmd(projectPath string) tea.Cmd {
	return m.stopRuntimeProcessCmd(projectPath, "")
}

func (m Model) stopRuntimeProcessCmd(projectPath, processID string) tea.Cmd {
	return func() tea.Msg {
		if m.runtimeManager == nil {
			return runtimeActionMsg{projectPath: projectPath, err: fmt.Errorf("runtime manager unavailable")}
		}
		err := m.runtimeManager.StopProcess(projectPath, processID)
		if errors.Is(err, projectrun.ErrNotRunning) {
			return runtimeActionMsg{projectPath: projectPath, status: "Runtime is not running"}
		}
		if err != nil {
			return runtimeActionMsg{projectPath: projectPath, err: fmt.Errorf("stop runtime: %w", err)}
		}
		return runtimeActionMsg{projectPath: projectPath, status: "Stopping runtime..."}
	}
}

func (m Model) restartProjectRuntimeCmd(projectPath, processID, command, cwd string) tea.Cmd {
	command = strings.TrimSpace(command)
	cwd = strings.TrimSpace(cwd)
	return func() tea.Msg {
		if m.runtimeManager == nil {
			return runtimeActionMsg{projectPath: projectPath, err: fmt.Errorf("runtime manager unavailable")}
		}
		if command == "" {
			return runtimeActionMsg{projectPath: projectPath, err: fmt.Errorf("runtime command is not set")}
		}
		snapshot, err := restartProjectRuntime(m.runtimeManager, projectrun.StartRequest{
			ProjectPath: projectPath,
			ProcessID:   processID,
			Command:     command,
			CWD:         cwd,
		})
		if err != nil {
			return runtimeActionMsg{projectPath: projectPath, err: fmt.Errorf("restart runtime: %w", err)}
		}
		return runtimeActionMsg{
			projectPath: projectPath,
			status:      runtimeActionStatus("Restarted runtime", snapshot),
		}
	}
}

func runtimeStartStatus(snapshot projectrun.Snapshot) string {
	return runtimeActionStatus("Started runtime", snapshot)
}

func runtimeStartResultStatus(result projectrun.StartResult) string {
	if result.Disposition == projectrun.StartDispositionReused {
		return runtimeActionStatus("Runtime already running", result.Snapshot)
	}
	if result.Disposition == projectrun.StartDispositionReplaced {
		return runtimeActionStatus("Restarted runtime", result.Snapshot)
	}
	return runtimeStartStatus(result.Snapshot)
}

func runtimeActionStatus(label string, snapshot projectrun.Snapshot) string {
	if snapshot.Running {
		if len(snapshot.Ports) == 1 {
			return fmt.Sprintf("%s on port %d", label, snapshot.Ports[0])
		}
		if len(snapshot.Ports) > 1 {
			return fmt.Sprintf("%s on %d ports", label, len(snapshot.Ports))
		}
		return label
	}
	if strings.TrimSpace(snapshot.LastError) != "" {
		return "Runtime exited: " + snapshot.LastError
	}
	return label
}

func restartProjectRuntime(manager *projectrun.Manager, req projectrun.StartRequest) (projectrun.Snapshot, error) {
	if manager == nil {
		return projectrun.Snapshot{}, fmt.Errorf("runtime manager unavailable")
	}
	snapshot, err := manager.SnapshotProcess(req.ProjectPath, req.ProcessID)
	if err != nil {
		return projectrun.Snapshot{}, err
	}
	if snapshot.Running {
		if snapshot, err = stopRuntimeProcessAndWait(manager, req.ProjectPath, req.ProcessID, 3*time.Second); err != nil {
			return snapshot, err
		}
	}
	snapshot, err = manager.Start(req)
	if errors.Is(err, projectrun.ErrAlreadyRunning) {
		return snapshot, nil
	}
	return snapshot, err
}

func stopProjectRuntimeAndWait(manager *projectrun.Manager, projectPath string, timeout time.Duration) (projectrun.Snapshot, error) {
	return stopRuntimeProcessAndWait(manager, projectPath, "", timeout)
}

func stopProjectRuntimesAndWait(manager *projectrun.Manager, projectPath string, timeout time.Duration) (projectrun.Snapshot, error) {
	if manager == nil {
		return projectrun.Snapshot{}, fmt.Errorf("runtime manager unavailable")
	}
	if err := manager.StopProject(projectPath); err != nil && !errors.Is(err, projectrun.ErrNotRunning) {
		return projectrun.Snapshot{}, err
	}
	deadline := time.Now().Add(timeout)
	for {
		snapshot, err := manager.Snapshot(projectPath)
		if err != nil {
			return projectrun.Snapshot{}, err
		}
		running := false
		for _, candidate := range manager.SnapshotsForProject(projectPath) {
			if candidate.Running {
				running = true
				break
			}
		}
		if !running {
			return snapshot, nil
		}
		if time.Now().After(deadline) {
			return snapshot, fmt.Errorf("timed out waiting for runtime to stop")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func stopRuntimeProcessAndWait(manager *projectrun.Manager, projectPath, processID string, timeout time.Duration) (projectrun.Snapshot, error) {
	if manager == nil {
		return projectrun.Snapshot{}, fmt.Errorf("runtime manager unavailable")
	}
	if err := manager.StopProcess(projectPath, processID); err != nil && !errors.Is(err, projectrun.ErrNotRunning) {
		return projectrun.Snapshot{}, err
	}
	deadline := time.Now().Add(timeout)
	for {
		snapshot, err := manager.SnapshotProcess(projectPath, processID)
		if err != nil {
			return projectrun.Snapshot{}, err
		}
		if !snapshot.Running {
			return snapshot, nil
		}
		if time.Now().After(deadline) {
			return snapshot, fmt.Errorf("timed out waiting for runtime to stop")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (m Model) renderRunCommandOverlay(body string, width, height int) string {
	panel := m.renderRunCommandPanel(width)
	panelWidth := lipgloss.Width(panel)
	panelHeight := lipgloss.Height(panel)
	left := max(0, (width-panelWidth)/2)
	top := max(0, (height-panelHeight)/4)
	return overlayBlock(body, panel, width, height, left, top)
}

func (m Model) renderRunCommandPanel(bodyW int) string {
	panelWidth := min(bodyW, min(max(58, bodyW-10), 94))
	panelInnerWidth := max(26, panelWidth-4)
	return renderDialogPanel(panelWidth, panelInnerWidth, m.renderRunCommandContent(panelInnerWidth))
}

func (m Model) renderRunCommandContent(width int) string {
	dialog := m.runCommandDialog
	if dialog == nil {
		return ""
	}

	lines := []string{
		commandPaletteTitleStyle.Render("Run Command"),
		commandPaletteHintStyle.Render("Save the default command Little Control Room should use to start this project's managed runtime."),
		"",
		detailField("Project", detailValueStyle.Render(dialog.ProjectName)),
		detailField("Path", detailMutedStyle.Render(filepath.Clean(dialog.ProjectPath))),
		"",
		detailLabelStyle.Render("Command:"),
		lipgloss.NewStyle().Width(max(16, width)).Render(dialog.Input.View()),
	}
	if strings.TrimSpace(dialog.SuggestionReason) != "" {
		lines = append(lines, "")
		lines = append(lines, detailField("Hint", detailMutedStyle.Render(dialog.SuggestionReason)))
	} else if dialog.SuggestionPending {
		lines = append(lines, "")
		lines = append(lines, detailField("Hint", detailMutedStyle.Render("Checking project files for a suggested command...")))
	} else if strings.TrimSpace(dialog.SuggestionError) != "" {
		lines = append(lines, "")
		lines = append(lines, detailField("Autocomplete", detailMutedStyle.Render("Unavailable: "+dialog.SuggestionError)))
	} else if dialog.SuggestionChecked && len(dialog.Suggestions) == 0 {
		lines = append(lines, "")
		lines = append(lines, detailField("Autocomplete", detailMutedStyle.Render("No project commands detected; enter a command manually.")))
	}
	if len(dialog.Suggestions) > 0 {
		lines = append(lines, "", commandPaletteTitleStyle.Render("Autocomplete"))
		matches := matchingRunCommandSuggestions(dialog)
		visible := matches
		selected := dialog.Input.CurrentSuggestionIndex()
		if len(visible) == 0 {
			visible = dialog.Suggestions
			selected = -1
		}
		start, end := runCommandSuggestionWindow(selected, len(visible))
		if start > 0 {
			lines = append(lines, commandPaletteHintStyle.Render(fmt.Sprintf("↑ %d more", start)))
		}
		for i := start; i < end; i++ {
			lines = append(lines, renderRunCommandSuggestionRow(visible[i], i == selected, width))
		}
		if end < len(visible) {
			lines = append(lines, commandPaletteHintStyle.Render(fmt.Sprintf("↓ %d more", len(visible)-end)))
		}
		if len(matches) == 0 {
			lines = append(lines, commandPaletteHintStyle.Render("Type a prefix to filter detected commands; Tab completes."))
		} else {
			lines = append(lines, commandPaletteHintStyle.Render("Tab completes; Up/Down selects another match."))
		}
	}
	lines = append(lines, "")
	lines = append(lines, renderDialogAction("Enter", saveRunDialogPrimaryLabel(dialog), commitActionKeyStyle, commitActionTextStyle))
	lines = append(lines, renderDialogAction("Esc", "cancel", cancelActionKeyStyle, cancelActionTextStyle))
	return strings.Join(lines, "\n")
}

func runCommandSuggestionWindow(selected, total int) (int, int) {
	if total <= 0 {
		return 0, 0
	}
	limit := min(4, total)
	if selected < 0 {
		return 0, limit
	}
	start := max(0, selected-limit+1)
	start = min(start, total-limit)
	return start, start + limit
}

func renderRunCommandSuggestionRow(suggestion projectrun.Suggestion, selected bool, width int) string {
	commandWidth := max(16, min(38, width/2))
	command := truncateText(strings.TrimSpace(suggestion.Command), commandWidth)
	reason := strings.TrimSpace(suggestion.Reason)
	marker := " "
	if selected {
		marker = ">"
	}
	row := marker + " " + command
	reasonWidth := width - lipgloss.Width(row) - 2
	if reason != "" && reasonWidth >= 8 {
		row += "  " + truncateText(reason, reasonWidth)
	}
	if selected {
		return commandPaletteSelectStyle.Width(width).Render(row)
	}
	styledRow := marker + " " + commandPalettePickStyle.Render(command)
	if reason != "" && reasonWidth >= 8 {
		styledRow += "  " + detailMutedStyle.Render(truncateText(reason, reasonWidth))
	}
	return commandPaletteRowStyle.Width(width).Render(styledRow)
}

func saveRunDialogPrimaryLabel(dialog *runCommandDialogState) string {
	if dialog == nil {
		return "save"
	}
	if dialog.StartAfterSave {
		return "save & run"
	}
	return "save"
}
