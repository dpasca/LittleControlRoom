package tui

import (
	"path/filepath"
	"strings"

	"lcroom/internal/model"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const (
	noteDialogFocusEditor = iota
	noteDialogFocusSave
	noteDialogFocusClear
	noteDialogFocusCancel
)

type noteDialogState struct {
	ProjectPath  string
	ProjectName  string
	OriginalNote string
	Editor       textarea.Model
	Selected     int
}

var (
	noteDialogButtonStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Padding(0, 1)
	noteDialogButtonSelectedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("230")).Background(lipgloss.Color("24")).Bold(true).Padding(0, 1)
	noteListIndicatorStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true)
)

func normalizeProjectNote(note string) string {
	note = strings.ReplaceAll(note, "\r\n", "\n")
	if strings.TrimSpace(note) == "" {
		return ""
	}
	return strings.TrimRight(note, "\n")
}

func projectHasNote(note string) bool {
	return strings.TrimSpace(note) != ""
}

func newNoteTextarea(value string) textarea.Model {
	input := textarea.New()
	input.Prompt = ""
	input.Placeholder = "Add handoff context, reminders, or next steps for this project."
	input.CharLimit = 10000
	input.ShowLineNumbers = false
	input.SetWidth(72)
	input.SetHeight(8)
	styleNoteTextarea(&input)
	input.SetValue(value)
	return input
}

func styleNoteTextarea(input *textarea.Model) {
	focused := input.FocusedStyle
	focused.Base = focused.Base.Background(codexComposerShellColor).Foreground(lipgloss.Color("252"))
	focused.CursorLine = focused.CursorLine.Background(codexComposerCursorLineColor)
	focused.EndOfBuffer = focused.EndOfBuffer.Foreground(lipgloss.Color("238"))
	focused.Placeholder = focused.Placeholder.Foreground(lipgloss.Color("240"))
	focused.Prompt = focused.Prompt.Foreground(lipgloss.Color("81")).Bold(true)
	focused.Text = focused.Text.Foreground(lipgloss.Color("252"))

	blurred := input.BlurredStyle
	blurred.Base = blurred.Base.Background(codexComposerShellColor).Foreground(lipgloss.Color("252"))
	blurred.CursorLine = blurred.CursorLine.Background(codexComposerShellColor)
	blurred.EndOfBuffer = blurred.EndOfBuffer.Foreground(lipgloss.Color("238"))
	blurred.Placeholder = blurred.Placeholder.Foreground(lipgloss.Color("240"))
	blurred.Prompt = blurred.Prompt.Foreground(lipgloss.Color("244")).Bold(true)
	blurred.Text = blurred.Text.Foreground(lipgloss.Color("252"))

	input.FocusedStyle = focused
	input.BlurredStyle = blurred
}

func (m *Model) openNoteDialogForSelection() tea.Cmd {
	project, ok := m.selectedProject()
	if !ok {
		m.status = "No project selected"
		return nil
	}
	return m.openNoteDialog(project)
}

func (m *Model) openNoteDialog(project model.ProjectSummary) tea.Cmd {
	projectName := strings.TrimSpace(project.Name)
	if projectName == "" {
		projectName = filepath.Base(filepath.Clean(project.Path))
	}
	note := normalizeProjectNote(project.Note)
	dialog := &noteDialogState{
		ProjectPath:  project.Path,
		ProjectName:  projectName,
		OriginalNote: note,
		Editor:       newNoteTextarea(note),
		Selected:     noteDialogFocusEditor,
	}
	m.noteDialog = dialog
	m.commandMode = false
	m.showHelp = false
	m.err = nil
	m.status = "Project notes open. Enter adds a newline, Tab picks an action"
	m.syncNoteDialogSize()
	return dialog.Editor.Focus()
}

func (m *Model) closeNoteDialog(status string) {
	if m.noteDialog != nil {
		m.noteDialog.Editor.Blur()
	}
	m.noteDialog = nil
	if status != "" {
		m.status = status
	}
}

func noteDialogPanelLayout(bodyW, bodyH int) (int, int, int) {
	if bodyW <= 0 {
		bodyW = 120
	}
	if bodyH <= 0 {
		bodyH = 24
	}
	panelWidth := min(bodyW, min(max(60, bodyW-10), 100))
	panelInnerWidth := max(28, panelWidth-4)
	editorHeight := max(6, min(14, bodyH/2))
	return panelWidth, panelInnerWidth, editorHeight
}

func (m *Model) syncNoteDialogSize() {
	if m.noteDialog == nil {
		return
	}
	layout := m.bodyLayout()
	_, panelInnerWidth, editorHeight := noteDialogPanelLayout(layout.width, layout.height)
	m.noteDialog.Editor.SetWidth(max(20, panelInnerWidth))
	m.noteDialog.Editor.SetHeight(editorHeight)
}

func (m Model) updateNoteDialogMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	dialog := m.noteDialog
	if dialog == nil {
		return m, nil
	}

	switch msg.String() {
	case "esc":
		m.closeNoteDialog("Note edit canceled")
		return m, nil
	case "tab":
		return m, m.moveNoteDialogSelection(1)
	case "shift+tab":
		return m, m.moveNoteDialogSelection(-1)
	case "ctrl+s":
		return m, m.saveNoteDialog()
	}

	if dialog.Selected != noteDialogFocusEditor {
		switch msg.String() {
		case "left", "up":
			return m, m.moveNoteDialogSelection(-1)
		case "right", "down":
			return m, m.moveNoteDialogSelection(1)
		case "enter", " ":
			return m, m.activateNoteDialogSelection()
		default:
			return m, nil
		}
	}

	var cmd tea.Cmd
	dialog.Editor, cmd = dialog.Editor.Update(msg)
	return m, cmd
}

func (m *Model) moveNoteDialogSelection(delta int) tea.Cmd {
	dialog := m.noteDialog
	if dialog == nil || delta == 0 {
		return nil
	}
	index := dialog.Selected + delta
	if index < noteDialogFocusEditor {
		index = noteDialogFocusCancel
	}
	if index > noteDialogFocusCancel {
		index = noteDialogFocusEditor
	}
	return m.setNoteDialogSelection(index)
}

func (m *Model) setNoteDialogSelection(index int) tea.Cmd {
	dialog := m.noteDialog
	if dialog == nil {
		return nil
	}
	if index < noteDialogFocusEditor {
		index = noteDialogFocusEditor
	}
	if index > noteDialogFocusCancel {
		index = noteDialogFocusCancel
	}
	dialog.Selected = index
	if index == noteDialogFocusEditor {
		return dialog.Editor.Focus()
	}
	dialog.Editor.Blur()
	return nil
}

func (m *Model) activateNoteDialogSelection() tea.Cmd {
	dialog := m.noteDialog
	if dialog == nil {
		return nil
	}
	switch dialog.Selected {
	case noteDialogFocusSave:
		return m.saveNoteDialog()
	case noteDialogFocusClear:
		return m.clearNoteDialog()
	default:
		m.closeNoteDialog("Note edit canceled")
		return nil
	}
}

func (m *Model) saveNoteDialog() tea.Cmd {
	dialog := m.noteDialog
	if dialog == nil {
		return nil
	}
	note := normalizeProjectNote(dialog.Editor.Value())
	if note == dialog.OriginalNote {
		m.closeNoteDialog("Note unchanged")
		return nil
	}
	m.closeNoteDialog("Saving note...")
	return m.setNoteCmd(dialog.ProjectPath, note)
}

func (m *Model) clearNoteDialog() tea.Cmd {
	dialog := m.noteDialog
	if dialog == nil {
		return nil
	}
	if !projectHasNote(dialog.OriginalNote) && !projectHasNote(dialog.Editor.Value()) {
		m.closeNoteDialog("No note to clear")
		return nil
	}
	m.closeNoteDialog("Clearing note...")
	return m.setNoteCmd(dialog.ProjectPath, "")
}

func (m Model) renderNoteDialogOverlay(body string, bodyW, bodyH int) string {
	panel := m.renderNoteDialogPanel(bodyW, bodyH)
	panelWidth := lipgloss.Width(panel)
	panelHeight := lipgloss.Height(panel)
	left := max(0, (bodyW-panelWidth)/2)
	top := max(0, (bodyH-panelHeight)/4)
	return overlayBlock(body, panel, bodyW, bodyH, left, top)
}

func (m Model) renderNoteDialogPanel(bodyW, bodyH int) string {
	panelWidth, panelInnerWidth, editorHeight := noteDialogPanelLayout(bodyW, bodyH)
	return lipgloss.NewStyle().
		Width(panelWidth).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("81")).
		Padding(0, 1).
		Background(lipgloss.Color("235")).
		Foreground(lipgloss.Color("252")).
		Render(m.renderNoteDialogContent(panelInnerWidth, editorHeight))
}

func (m Model) renderNoteDialogContent(width, editorHeight int) string {
	dialog := m.noteDialog
	if dialog == nil {
		return ""
	}
	editor := dialog.Editor
	editor.SetWidth(max(20, width))
	editor.SetHeight(editorHeight)

	lines := []string{
		renderDialogHeader("Project Notes", dialog.ProjectName, "", width),
		commandPaletteHintStyle.Render("Keep a lightweight project scratchpad for handoff context, reminders, or next steps that should stay visible in the control room."),
		"",
	}

	label := "  Notes"
	labelStyle := detailLabelStyle
	if dialog.Selected == noteDialogFocusEditor {
		label = "> Notes"
		labelStyle = commandPalettePickStyle
	}
	lines = append(lines, labelStyle.Render(label))
	lines = append(lines, editor.View())
	lines = append(lines, "")
	lines = append(lines, strings.Join([]string{
		renderNoteDialogButton("Save", dialog.Selected == noteDialogFocusSave),
		renderNoteDialogButton("Clear", dialog.Selected == noteDialogFocusClear),
		renderNoteDialogButton("Cancel", dialog.Selected == noteDialogFocusCancel),
	}, "  "))
	lines = append(lines, commandPaletteHintStyle.Render("Tab or Shift+Tab moves between the editor and actions. Enter adds a newline in the editor or runs the selected action."))
	return strings.Join(lines, "\n")
}

func renderNoteDialogButton(label string, selected bool) string {
	if selected {
		return noteDialogButtonSelectedStyle.Render(" " + label + " ")
	}
	return noteDialogButtonStyle.Render("[" + label + "]")
}
