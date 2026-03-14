package tui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"lcroom/internal/service"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const (
	newProjectFieldPath = iota
	newProjectFieldName
	newProjectFieldGitRepo
)

const newProjectRecentPathLimit = 3

type newProjectDialogState struct {
	PathInput     textinput.Model
	NameInput     textinput.Model
	Selected      int
	CreateGitRepo bool
	Submitting    bool
}

type newProjectResultMsg struct {
	result service.CreateOrAttachProjectResult
	err    error
}

type recentProjectParentsMsg struct {
	paths []string
	err   error
}

type newProjectPreview struct {
	ParentPath  string
	Name        string
	FullPath    string
	Ready       bool
	Exists      bool
	ExistingDir bool
	Error       string
}

func (m Model) loadRecentProjectParentsCmd() tea.Cmd {
	if m.svc == nil {
		return nil
	}
	return func() tea.Msg {
		paths, err := m.svc.RecentProjectParentPaths(m.ctx, newProjectRecentPathLimit)
		return recentProjectParentsMsg{paths: paths, err: err}
	}
}

func (m *Model) openNewProjectDialog() tea.Cmd {
	dialog := &newProjectDialogState{
		PathInput:     newNewProjectTextInput(m.defaultNewProjectParentPath(), 1024),
		NameInput:     newNewProjectTextInput("", 256),
		Selected:      newProjectFieldPath,
		CreateGitRepo: true,
	}
	dialog.PathInput.Placeholder = "/path/to/projects"
	dialog.NameInput.Placeholder = "project-name"

	m.newProjectDialog = dialog
	m.showHelp = false
	m.closeNoteDialog("")
	m.err = nil
	m.status = "New project dialog open. Enter create/add, Esc cancel"
	return m.setNewProjectSelection(newProjectFieldPath)
}

func (m *Model) closeNewProjectDialog(status string) {
	if m.newProjectDialog != nil {
		m.newProjectDialog.PathInput.Blur()
		m.newProjectDialog.NameInput.Blur()
	}
	m.newProjectDialog = nil
	if status != "" {
		m.status = status
	}
}

func (m Model) updateNewProjectMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	dialog := m.newProjectDialog
	if dialog == nil {
		return m, nil
	}
	if dialog.Submitting {
		return m, nil
	}
	if msg.Alt && len(msg.Runes) == 1 {
		index := int(msg.Runes[0] - '1')
		if index >= 0 && index < len(m.newProjectRecentParents) && index < newProjectRecentPathLimit {
			dialog.PathInput.SetValue(m.newProjectRecentParents[index])
			dialog.PathInput.CursorEnd()
			m.status = fmt.Sprintf("Using recent parent path %d", index+1)
			return m, nil
		}
	}

	switch msg.String() {
	case "esc":
		m.closeNewProjectDialog("New project canceled")
		return m, nil
	case "tab", "down", "ctrl+n":
		return m, m.moveNewProjectSelection(1)
	case "shift+tab", "up", "ctrl+p":
		return m, m.moveNewProjectSelection(-1)
	case "enter":
		preview := m.currentNewProjectPreview()
		if preview.Error != "" {
			m.status = preview.Error
			return m, nil
		}
		if !preview.Ready {
			m.status = "Project path and name are required"
			return m, nil
		}
		dialog.Submitting = true
		if preview.Exists && preview.ExistingDir {
			m.status = "Adding existing folder to the list..."
		} else {
			m.status = "Creating project..."
		}
		return m, m.createOrAttachProjectCmd(service.CreateOrAttachProjectRequest{
			ParentPath:    preview.ParentPath,
			Name:          preview.Name,
			CreateGitRepo: dialog.CreateGitRepo,
		})
	case " ", "x":
		if dialog.Selected == newProjectFieldGitRepo {
			dialog.CreateGitRepo = !dialog.CreateGitRepo
			return m, nil
		}
	}

	switch dialog.Selected {
	case newProjectFieldPath:
		input, cmd := dialog.PathInput.Update(msg)
		dialog.PathInput = input
		return m, cmd
	case newProjectFieldName:
		input, cmd := dialog.NameInput.Update(msg)
		dialog.NameInput = input
		return m, cmd
	default:
		return m, nil
	}
}

func (m *Model) moveNewProjectSelection(delta int) tea.Cmd {
	dialog := m.newProjectDialog
	if dialog == nil || delta == 0 {
		return nil
	}
	index := dialog.Selected + delta
	if index < newProjectFieldPath {
		index = newProjectFieldGitRepo
	}
	if index > newProjectFieldGitRepo {
		index = newProjectFieldPath
	}
	return m.setNewProjectSelection(index)
}

func (m *Model) setNewProjectSelection(index int) tea.Cmd {
	dialog := m.newProjectDialog
	if dialog == nil {
		return nil
	}
	if index < newProjectFieldPath {
		index = newProjectFieldPath
	}
	if index > newProjectFieldGitRepo {
		index = newProjectFieldGitRepo
	}
	dialog.Selected = index
	dialog.PathInput.Blur()
	dialog.NameInput.Blur()
	switch index {
	case newProjectFieldPath:
		dialog.PathInput.CursorEnd()
		return dialog.PathInput.Focus()
	case newProjectFieldName:
		dialog.NameInput.CursorEnd()
		return dialog.NameInput.Focus()
	default:
		return nil
	}
}

func (m Model) createOrAttachProjectCmd(req service.CreateOrAttachProjectRequest) tea.Cmd {
	if m.svc == nil {
		return func() tea.Msg {
			return newProjectResultMsg{err: fmt.Errorf("service unavailable")}
		}
	}
	return func() tea.Msg {
		result, err := m.svc.CreateOrAttachProject(m.ctx, req)
		return newProjectResultMsg{result: result, err: err}
	}
}

func (m Model) defaultNewProjectParentPath() string {
	if len(m.newProjectRecentParents) > 0 {
		return m.newProjectRecentParents[0]
	}
	if m.homeDirFn != nil {
		if home, err := m.homeDirFn(); err == nil && strings.TrimSpace(home) != "" {
			return filepath.Clean(home)
		}
	}
	return "."
}

func newNewProjectTextInput(value string, charLimit int) textinput.Model {
	input := textinput.New()
	input.Prompt = ""
	input.SetValue(value)
	input.CharLimit = charLimit
	return input
}

func (m Model) currentNewProjectPreview() newProjectPreview {
	dialog := m.newProjectDialog
	if dialog == nil {
		return newProjectPreview{}
	}

	parentPath := normalizeNewProjectParentPath(m.homeDirFn, dialog.PathInput.Value())
	name := strings.TrimSpace(dialog.NameInput.Value())
	displayName := name
	if displayName == "" {
		displayName = "<name>"
	}

	preview := newProjectPreview{
		ParentPath: parentPath,
		Name:       name,
		FullPath:   filepath.Clean(filepath.Join(parentPath, displayName)),
		Ready:      strings.TrimSpace(parentPath) != "" && name != "",
	}
	if strings.TrimSpace(parentPath) == "" {
		preview.Error = "Project path is required"
		return preview
	}
	if name == "" {
		return preview
	}
	if name == "." || name == ".." || strings.ContainsRune(name, '/') || strings.ContainsRune(name, '\\') || filepath.Base(name) != name {
		preview.Error = "Project name must be a single folder name"
		return preview
	}

	preview.FullPath = filepath.Clean(filepath.Join(parentPath, name))
	info, err := os.Stat(preview.FullPath)
	switch {
	case err == nil:
		preview.Exists = true
		preview.ExistingDir = info.IsDir()
		if !preview.ExistingDir {
			preview.Error = "Path already exists and is not a directory"
		}
	case errors.Is(err, os.ErrNotExist):
	default:
		preview.Error = fmt.Sprintf("Unable to inspect path: %v", err)
	}
	preview.Ready = preview.Error == ""
	return preview
}

func normalizeNewProjectParentPath(homeDirFn func() (string, error), raw string) string {
	path := strings.TrimSpace(raw)
	if path == "" {
		return ""
	}
	if expanded := expandNewProjectHomePath(homeDirFn, path); expanded != "" {
		path = expanded
	}
	if absPath, err := filepath.Abs(path); err == nil {
		path = absPath
	}
	return filepath.Clean(path)
}

func expandNewProjectHomePath(homeDirFn func() (string, error), path string) string {
	if strings.TrimSpace(path) == "" || path[0] != '~' || homeDirFn == nil {
		return path
	}
	home, err := homeDirFn()
	if err != nil || strings.TrimSpace(home) == "" {
		return path
	}
	if path == "~" {
		return home
	}
	if strings.HasPrefix(path, "~/") || strings.HasPrefix(path, "~\\") {
		return filepath.Join(home, path[2:])
	}
	return path
}

func (m Model) renderNewProjectOverlay(body string, bodyW, bodyH int) string {
	panel := m.renderNewProjectPanel(bodyW)
	panelWidth := lipgloss.Width(panel)
	panelHeight := lipgloss.Height(panel)
	left := max(0, (bodyW-panelWidth)/2)
	top := max(0, (bodyH-panelHeight)/4)
	return overlayBlock(body, panel, bodyW, bodyH, left, top)
}

func (m Model) renderNewProjectPanel(bodyW int) string {
	panelWidth := min(bodyW, min(max(60, bodyW-10), 98))
	panelInnerWidth := max(28, panelWidth-4)
	return lipgloss.NewStyle().
		Width(panelWidth).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("81")).
		Padding(0, 1).
		Background(lipgloss.Color("235")).
		Foreground(lipgloss.Color("252")).
		Render(m.renderNewProjectContent(panelInnerWidth))
}

func (m Model) renderNewProjectContent(width int) string {
	dialog := m.newProjectDialog
	if dialog == nil {
		return ""
	}
	preview := m.currentNewProjectPreview()
	labelWidth := max(12, min(18, width/4))
	inputWidth := max(12, width-labelWidth-1)

	lines := []string{
		commandPaletteTitleStyle.Render("New Project"),
		commandPaletteHintStyle.Render("Create a new folder, or add an existing one even before any Codex/OpenCode activity exists there."),
		"",
		m.renderNewProjectInputRow("Path", dialog.Selected == newProjectFieldPath, labelWidth, inputWidth, dialog.PathInput),
		m.renderNewProjectInputRow("Name", dialog.Selected == newProjectFieldName, labelWidth, inputWidth, dialog.NameInput),
		m.renderNewProjectGitRow(labelWidth, inputWidth),
		"",
		detailLabelStyle.Render("Full path:"),
		lipgloss.NewStyle().Width(width).Render(detailValueStyle.Render(preview.FullPath)),
	}

	lines = append(lines, m.renderNewProjectStatus(preview, width)...)

	if len(m.newProjectRecentParents) > 0 {
		lines = append(lines, "")
		lines = append(lines, commandPaletteTitleStyle.Render("Recent Paths"))
		lines = append(lines, commandPaletteHintStyle.Render("Alt+1..3 applies a remembered parent path."))
		for i, path := range m.newProjectRecentParents {
			label := fmt.Sprintf("Alt+%d ", i+1)
			rowStyle := commandPaletteRowStyle
			if normalizeNewProjectParentPath(m.homeDirFn, dialog.PathInput.Value()) == filepath.Clean(path) {
				rowStyle = commandPaletteSelectStyle
			}
			lines = append(lines, rowStyle.Width(width).Render(label+truncateText(path, max(12, width-len(label)))))
		}
	}

	lines = append(lines, "")
	lines = append(lines, renderNewProjectActions(preview))
	return strings.Join(lines, "\n")
}

func (m Model) renderNewProjectInputRow(label string, selected bool, labelWidth, inputWidth int, input textinput.Model) string {
	rowLabel := "  " + label
	labelStyle := detailLabelStyle
	if selected {
		rowLabel = "> " + label
		labelStyle = commandPalettePickStyle
	}
	input.Width = inputWidth
	return labelStyle.Width(labelWidth).Render(truncateText(rowLabel, labelWidth)) + " " + input.View()
}

func (m Model) renderNewProjectGitRow(labelWidth, inputWidth int) string {
	dialog := m.newProjectDialog
	if dialog == nil {
		return ""
	}
	label := "  Git repo"
	labelStyle := detailLabelStyle
	if dialog.Selected == newProjectFieldGitRepo {
		label = "> Git repo"
		labelStyle = commandPalettePickStyle
	}
	value := "[ ] initialize when creating a new folder"
	if dialog.CreateGitRepo {
		value = "[x] initialize when creating a new folder"
	}
	return labelStyle.Width(labelWidth).Render(truncateText(label, labelWidth)) + " " + truncateText(value, inputWidth)
}

func (m Model) renderNewProjectStatus(preview newProjectPreview, width int) []string {
	var lines []string
	switch {
	case preview.Error != "":
		lines = append(lines, detailWarningStyle.Render(preview.Error))
	case preview.Exists && preview.ExistingDir:
		lines = append(lines, commandPaletteHintStyle.Render("Folder already exists. Enter will add it to the list instead of creating it."))
		if m.newProjectDialog != nil && m.newProjectDialog.CreateGitRepo {
			lines = append(lines, commandPaletteHintStyle.Render("Git init only runs when the folder is created here."))
		}
	case preview.Ready:
		lines = append(lines, commandPaletteHintStyle.Render("Folder does not exist yet. Enter will create it and add it to the list."))
	default:
		lines = append(lines, commandPaletteHintStyle.Render("Enter a parent path and a single-folder project name."))
	}
	if width > 0 && len(lines) > 0 {
		for i, line := range lines {
			lines[i] = lipgloss.NewStyle().Width(width).Render(line)
		}
	}
	return lines
}

func renderNewProjectActions(preview newProjectPreview) string {
	primary := "create"
	if preview.Exists && preview.ExistingDir {
		primary = "add"
	}
	actions := []string{
		renderDialogAction("Enter", primary, commitActionKeyStyle, commitActionTextStyle),
		renderDialogAction("Tab", "next", navigateActionKeyStyle, navigateActionTextStyle),
		renderDialogAction("Space", "toggle git", pushActionKeyStyle, pushActionTextStyle),
		renderDialogAction("Esc", "cancel", cancelActionKeyStyle, cancelActionTextStyle),
	}
	return strings.Join(actions, "   ")
}
