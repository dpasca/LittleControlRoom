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
	ProjectPath      string
	ProjectName      string
	Input            textinput.Model
	SuggestionReason string
	StartAfterSave   bool
	Submitting       bool
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
	suggestionReason := ""
	if command == "" {
		if suggestion, err := projectrun.Suggest(project.Path); err == nil {
			command = suggestion.Command
			suggestionReason = suggestion.Reason
		}
	}

	input := textinput.New()
	input.Prompt = ""
	input.Placeholder = "pnpm dev"
	input.CharLimit = 4096
	input.Width = 72
	input.SetValue(command)

	m.runCommandDialog = &runCommandDialogState{
		ProjectPath:      project.Path,
		ProjectName:      noteProjectTitle(project.Path, project.Name),
		Input:            input,
		SuggestionReason: suggestionReason,
		StartAfterSave:   startAfterSave,
	}
	m.commandMode = false
	m.showHelp = false
	m.err = nil
	if startAfterSave {
		m.status = "Set a run command for this project"
	} else {
		m.status = "Editing saved run command"
	}
	return m.runCommandDialog.Input.Focus()
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
		dialog.Submitting = true
		if dialog.StartAfterSave {
			m.status = "Saving run command and starting runtime..."
		} else {
			m.status = "Saving run command..."
		}
		return m, m.saveRunCommandCmd(dialog.ProjectPath, command, dialog.StartAfterSave)
	}

	var cmd tea.Cmd
	dialog.Input, cmd = dialog.Input.Update(msg)
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
		err := m.svc.SetRunCommand(m.ctx, projectPath, command)
		return runCommandSavedMsg{
			projectPath: projectPath,
			command:     command,
			startAfter:  startAfter,
			err:         err,
		}
	}
}

func (m Model) startProjectRuntimeCmd(projectPath, command string) tea.Cmd {
	return func() tea.Msg {
		if m.runtimeManager == nil {
			return runtimeActionMsg{projectPath: projectPath, err: fmt.Errorf("runtime manager unavailable")}
		}
		snapshot, err := m.runtimeManager.Start(projectrun.StartRequest{
			ProjectPath: projectPath,
			Command:     command,
		})
		if errors.Is(err, projectrun.ErrAlreadyRunning) {
			return runtimeActionMsg{
				projectPath: projectPath,
				status:      runtimeStartStatus(snapshot),
			}
		}
		if err != nil {
			return runtimeActionMsg{projectPath: projectPath, err: fmt.Errorf("start runtime: %w", err)}
		}
		return runtimeActionMsg{
			projectPath: projectPath,
			status:      runtimeStartStatus(snapshot),
		}
	}
}

func (m Model) stopProjectRuntimeCmd(projectPath string) tea.Cmd {
	return func() tea.Msg {
		if m.runtimeManager == nil {
			return runtimeActionMsg{projectPath: projectPath, err: fmt.Errorf("runtime manager unavailable")}
		}
		err := m.runtimeManager.Stop(projectPath)
		if errors.Is(err, projectrun.ErrNotRunning) {
			return runtimeActionMsg{projectPath: projectPath, status: "Runtime is not running"}
		}
		if err != nil {
			return runtimeActionMsg{projectPath: projectPath, err: fmt.Errorf("stop runtime: %w", err)}
		}
		return runtimeActionMsg{projectPath: projectPath, status: "Stopping runtime..."}
	}
}

func (m Model) restartProjectRuntimeCmd(projectPath, command string) tea.Cmd {
	command = strings.TrimSpace(command)
	return func() tea.Msg {
		if m.runtimeManager == nil {
			return runtimeActionMsg{projectPath: projectPath, err: fmt.Errorf("runtime manager unavailable")}
		}
		if command == "" {
			return runtimeActionMsg{projectPath: projectPath, err: fmt.Errorf("runtime command is not set")}
		}
		snapshot, err := restartProjectRuntime(m.runtimeManager, projectrun.StartRequest{
			ProjectPath: projectPath,
			Command:     command,
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
	snapshot, err := manager.Snapshot(req.ProjectPath)
	if err != nil {
		return projectrun.Snapshot{}, err
	}
	if snapshot.Running {
		if err := manager.Stop(req.ProjectPath); err != nil && !errors.Is(err, projectrun.ErrNotRunning) {
			return projectrun.Snapshot{}, err
		}
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			time.Sleep(100 * time.Millisecond)
			snapshot, err = manager.Snapshot(req.ProjectPath)
			if err != nil {
				return projectrun.Snapshot{}, err
			}
			if !snapshot.Running {
				break
			}
		}
		if snapshot.Running {
			return snapshot, fmt.Errorf("timed out waiting for runtime to stop")
		}
	}
	snapshot, err = manager.Start(req)
	if errors.Is(err, projectrun.ErrAlreadyRunning) {
		return snapshot, nil
	}
	return snapshot, err
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
	return lipgloss.NewStyle().
		Width(panelWidth).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("81")).
		Padding(0, 1).
		Background(lipgloss.Color("235")).
		Foreground(lipgloss.Color("252")).
		Render(m.renderRunCommandContent(panelInnerWidth))
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
	}
	lines = append(lines, "")
	lines = append(lines, renderDialogAction("Enter", saveRunDialogPrimaryLabel(dialog), commitActionKeyStyle, commitActionTextStyle))
	lines = append(lines, renderDialogAction("Esc", "cancel", cancelActionKeyStyle, cancelActionTextStyle))
	return strings.Join(lines, "\n")
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
