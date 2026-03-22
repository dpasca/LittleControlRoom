package tui

import (
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	"lcroom/internal/model"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	rw "github.com/mattn/go-runewidth"
	"github.com/rivo/uniseg"
)

const (
	noteDialogFocusEditor = iota
	noteDialogFocusCopy
	noteDialogFocusSave
	noteDialogFocusClear
	noteDialogFocusCancel

	noteCopyScopeWhole = iota
	noteCopyScopeSelectedText
	noteCopyScopeCancel

	noteClearConfirmFocusConfirm
	noteClearConfirmFocusCancel
)

type noteDialogState struct {
	ProjectPath  string
	ProjectName  string
	OriginalNote string
	Editor       textarea.Model
	Selected     int
	Selection    *noteTextSelectionState
}

type noteClearConfirmState struct {
	ProjectPath string
	ProjectName string
	Selected    int
}

type noteCopyDialogState struct {
	Selected int
}

type noteTextSelectionState struct {
	AnchorSet  bool
	AnchorLine int
	AnchorCol  int
	ViewportY  int
}

type noteSelectionDisplayLine struct {
	rawLine  int
	startCol int
	endCol   int
	text     string
}

var (
	noteDialogButtonStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Padding(0, 1)
	noteDialogButtonSelectedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("230")).Background(lipgloss.Color("24")).Bold(true).Padding(0, 1)
	noteListIndicatorStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true)
	noteSelectionLineStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Background(codexComposerShellColor).Inline(true)
	noteSelectionCursorLineStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Background(codexComposerCursorLineColor).Inline(true)
	noteSelectionRangeStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Background(lipgloss.Color("60")).Inline(true)
	noteSelectionCursorStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("16")).Background(lipgloss.Color("214")).Bold(true).Inline(true)
	noteSelectionCursorRangeStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("16")).Background(lipgloss.Color("222")).Bold(true).Inline(true)
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

func noteProjectTitle(projectPath, projectName string) string {
	projectName = strings.TrimSpace(projectName)
	if projectName != "" {
		return projectName
	}
	return filepath.Base(filepath.Clean(projectPath))
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
	note := normalizeProjectNote(project.Note)
	dialog := &noteDialogState{
		ProjectPath:  project.Path,
		ProjectName:  noteProjectTitle(project.Path, project.Name),
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
		m.noteDialog.Selection = nil
	}
	m.noteCopyDialog = nil
	m.noteDialog = nil
	if status != "" {
		m.status = status
	}
}

func (m *Model) openNoteCopyDialog() tea.Cmd {
	if m.noteDialog == nil {
		return nil
	}
	m.noteCopyDialog = &noteCopyDialogState{Selected: noteCopyScopeWhole}
	m.noteDialog.Editor.Blur()
	m.status = "Choose whole note or selected text"
	return nil
}

func (m *Model) closeNoteCopyDialog(status string) tea.Cmd {
	m.noteCopyDialog = nil
	if status != "" {
		m.status = status
	}
	return m.restoreNoteDialogFocus()
}

func (m *Model) restoreNoteDialogFocus() tea.Cmd {
	if m.noteDialog == nil {
		return nil
	}
	if m.noteDialog.Selected == noteDialogFocusEditor {
		return m.noteDialog.Editor.Focus()
	}
	m.noteDialog.Editor.Blur()
	return nil
}

func (m *Model) openNoteClearConfirm(projectPath, projectName string) tea.Cmd {
	m.noteCopyDialog = nil
	m.noteClearConfirm = &noteClearConfirmState{
		ProjectPath: projectPath,
		ProjectName: noteProjectTitle(projectPath, projectName),
		Selected:    noteClearConfirmFocusCancel,
	}
	m.commandMode = false
	m.showHelp = false
	m.err = nil
	m.status = "Confirm note clear"
	return nil
}

func (m *Model) closeNoteClearConfirm(status string) {
	m.noteClearConfirm = nil
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
	if dialog.Selection != nil {
		return m.updateNoteTextSelectionMode(msg)
	}

	switch msg.String() {
	case "esc":
		m.closeNoteDialog("Note edit canceled")
		return m, nil
	case "tab":
		return m, m.moveNoteDialogSelection(1)
	case "shift+tab":
		return m, m.moveNoteDialogSelection(-1)
	case "ctrl+y":
		m.copyNoteDialogSelection(noteCopyScopeWhole)
		return m, nil
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
	case noteDialogFocusCopy:
		return m.openNoteCopyDialog()
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
	return m.openNoteClearConfirm(dialog.ProjectPath, dialog.ProjectName)
}

func (m Model) updateNoteClearConfirmMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	confirm := m.noteClearConfirm
	if confirm == nil {
		return m, nil
	}

	switch msg.String() {
	case "esc":
		m.closeNoteClearConfirm("Note clear canceled")
		return m, nil
	case "tab", "shift+tab", "left", "right", "up", "down":
		return m, m.toggleNoteClearConfirmSelection()
	case "enter", " ":
		return m, m.activateNoteClearConfirmSelection()
	default:
		return m, nil
	}
}

func (m *Model) toggleNoteClearConfirmSelection() tea.Cmd {
	confirm := m.noteClearConfirm
	if confirm == nil {
		return nil
	}
	if confirm.Selected == noteClearConfirmFocusConfirm {
		confirm.Selected = noteClearConfirmFocusCancel
	} else {
		confirm.Selected = noteClearConfirmFocusConfirm
	}
	return nil
}

func (m *Model) activateNoteClearConfirmSelection() tea.Cmd {
	confirm := m.noteClearConfirm
	if confirm == nil {
		return nil
	}
	if confirm.Selected == noteClearConfirmFocusConfirm {
		path := confirm.ProjectPath
		m.closeNoteClearConfirm("")
		if m.noteDialog != nil && m.noteDialog.ProjectPath == path {
			m.closeNoteDialog("")
		}
		m.status = "Clearing note..."
		return m.setNoteCmd(path, "")
	}
	m.closeNoteClearConfirm("Note clear canceled")
	return nil
}

func (m Model) renderNoteDialogOverlay(body string, bodyW, bodyH int) string {
	panel := m.renderNoteDialogPanel(bodyW, bodyH)
	panelWidth := lipgloss.Width(panel)
	panelHeight := lipgloss.Height(panel)
	left := max(0, (bodyW-panelWidth)/2)
	top := max(0, (bodyH-panelHeight)/4)
	return overlayBlock(body, panel, bodyW, bodyH, left, top)
}

func (m Model) renderNoteCopyDialogOverlay(body string, bodyW, bodyH int) string {
	panel := m.renderNoteCopyDialogPanel(bodyW)
	panelWidth := lipgloss.Width(panel)
	panelHeight := lipgloss.Height(panel)
	left := max(0, (bodyW-panelWidth)/2)
	top := max(0, (bodyH-panelHeight)/3)
	return overlayBlock(body, panel, bodyW, bodyH, left, top)
}

func (m Model) renderNoteClearConfirmOverlay(body string, bodyW, bodyH int) string {
	panel := m.renderNoteClearConfirmPanel(bodyW)
	panelWidth := lipgloss.Width(panel)
	panelHeight := lipgloss.Height(panel)
	left := max(0, (bodyW-panelWidth)/2)
	top := max(0, (bodyH-panelHeight)/3)
	return overlayBlock(body, panel, bodyW, bodyH, left, top)
}

func (m Model) renderNoteDialogPanel(bodyW, bodyH int) string {
	panelWidth, panelInnerWidth, editorHeight := noteDialogPanelLayout(bodyW, bodyH)
	return renderDialogPanel(panelWidth, panelInnerWidth, m.renderNoteDialogContent(panelInnerWidth, editorHeight))
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
	if dialog.Selection != nil {
		lines = append(lines, detailSectionStyle.Render("Copy Selection"))
		lines = append(lines, commandPaletteHintStyle.Render(noteTextSelectionHint(dialog)))
		lines = append(lines, "")
	}

	label := "  Notes"
	labelStyle := detailLabelStyle
	if dialog.Selected == noteDialogFocusEditor {
		label = "> Notes"
		labelStyle = commandPalettePickStyle
	}
	lines = append(lines, labelStyle.Render(label))
	editorView := editor.View()
	if dialog.Selection != nil {
		editorView = renderNoteSelectionEditor(editor, dialog.Selection)
	}
	lines = append(lines, editorView)
	lines = append(lines, "")
	lines = append(lines, strings.Join([]string{
		renderNoteDialogButton("Copy...", dialog.Selected == noteDialogFocusCopy),
		renderNoteDialogButton("Save", dialog.Selected == noteDialogFocusSave),
		renderNoteDialogButton("Clear", dialog.Selected == noteDialogFocusClear),
		renderNoteDialogButton("Cancel", dialog.Selected == noteDialogFocusCancel),
	}, "  "))
	lines = append(lines, commandPaletteHintStyle.Render("Tab or Shift+Tab moves between the editor and actions. Ctrl+Y copies the full note. Enter adds a newline in the editor or runs the selected action."))
	return strings.Join(lines, "\n")
}

func (m Model) renderNoteCopyDialogPanel(bodyW int) string {
	panelWidth := min(bodyW, min(max(52, bodyW-16), 82))
	panelInnerWidth := max(24, panelWidth-4)
	return renderDialogPanel(panelWidth, panelInnerWidth, m.renderNoteCopyDialogContent(panelInnerWidth))
}

func (m Model) renderNoteCopyDialogContent(width int) string {
	copyDialog := m.noteCopyDialog
	dialog := m.noteDialog
	if copyDialog == nil || dialog == nil {
		return ""
	}

	lines := []string{
		renderDialogHeader("Copy Note Text", dialog.ProjectName, "", width),
		commandPaletteHintStyle.Render("Copy the whole note immediately or enter selection mode. In selection mode, press Space once to mark the start and Space again to copy the end of the range."),
		"",
	}

	options := []int{
		noteCopyScopeWhole,
		noteCopyScopeSelectedText,
		noteCopyScopeCancel,
	}
	for _, option := range options {
		lines = append(lines, renderNoteDialogButton(noteCopyScopeLabel(option), copyDialog.Selected == option))
	}
	lines = append(lines, commandPaletteHintStyle.Render("Tab, arrows, or Shift+Tab switch options. Enter runs the selected action. Esc closes this menu."))
	return strings.Join(lines, "\n")
}

func (m Model) renderNoteClearConfirmPanel(bodyW int) string {
	panelWidth := min(bodyW, min(max(48, bodyW-16), 78))
	panelInnerWidth := max(24, panelWidth-4)
	return renderDialogPanel(panelWidth, panelInnerWidth, m.renderNoteClearConfirmContent(panelInnerWidth))
}

func (m Model) renderNoteClearConfirmContent(width int) string {
	confirm := m.noteClearConfirm
	if confirm == nil {
		return ""
	}

	lines := []string{
		renderDialogHeader("Clear Note", confirm.ProjectName, "", width),
		commandPaletteHintStyle.Render("This will remove the saved note and clear the note badge from the project list."),
		"",
		lipgloss.NewStyle().Width(width).Render(detailValueStyle.Render("Clear the saved note for " + confirm.ProjectName + "?")),
		"",
		strings.Join([]string{
			renderNoteDialogButton("Clear note", confirm.Selected == noteClearConfirmFocusConfirm),
			renderNoteDialogButton("Keep note", confirm.Selected == noteClearConfirmFocusCancel),
		}, "  "),
		commandPaletteHintStyle.Render("Tab, arrows, or Shift+Tab switch actions. Enter confirms the selected action."),
	}
	return strings.Join(lines, "\n")
}

func renderNoteDialogButton(label string, selected bool) string {
	if selected {
		return noteDialogButtonSelectedStyle.Render(" " + label + " ")
	}
	return noteDialogButtonStyle.Render("[" + label + "]")
}

func noteCopyScopeLabel(scope int) string {
	switch scope {
	case noteCopyScopeWhole:
		return "Whole note"
	case noteCopyScopeSelectedText:
		return "Selected text"
	default:
		return "Cancel"
	}
}

func noteCopyScopeStatus(scope int) string {
	switch scope {
	case noteCopyScopeWhole:
		return "Note copied to clipboard"
	case noteCopyScopeSelectedText:
		return "Selected note text copied to clipboard"
	default:
		return "Note copy canceled"
	}
}

func (m Model) updateNoteCopyDialogMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	copyDialog := m.noteCopyDialog
	if copyDialog == nil {
		return m, nil
	}

	switch msg.String() {
	case "esc":
		return m, m.closeNoteCopyDialog("Note copy canceled")
	case "tab", "shift+tab", "left", "right", "up", "down":
		delta := 1
		if msg.String() == "shift+tab" || msg.String() == "left" || msg.String() == "up" {
			delta = -1
		}
		return m, m.moveNoteCopyDialogSelection(delta)
	case "enter", " ":
		return m, m.activateNoteCopyDialogSelection()
	default:
		return m, nil
	}
}

func (m *Model) moveNoteCopyDialogSelection(delta int) tea.Cmd {
	copyDialog := m.noteCopyDialog
	if copyDialog == nil || delta == 0 {
		return nil
	}
	index := copyDialog.Selected + delta
	if index < noteCopyScopeWhole {
		index = noteCopyScopeCancel
	}
	if index > noteCopyScopeCancel {
		index = noteCopyScopeWhole
	}
	copyDialog.Selected = index
	return nil
}

func (m *Model) activateNoteCopyDialogSelection() tea.Cmd {
	copyDialog := m.noteCopyDialog
	if copyDialog == nil {
		return nil
	}
	if copyDialog.Selected == noteCopyScopeCancel {
		return m.closeNoteCopyDialog("Note copy canceled")
	}
	if copyDialog.Selected == noteCopyScopeSelectedText {
		return m.startNoteTextSelection()
	}
	m.copyNoteDialogSelection(copyDialog.Selected)
	return m.closeNoteCopyDialog("")
}

func (m *Model) startNoteTextSelection() tea.Cmd {
	if m.noteDialog == nil {
		return nil
	}
	m.noteCopyDialog = nil
	m.noteDialog.Selection = &noteTextSelectionState{ViewportY: noteSelectionInitialViewport(m.noteDialog.Editor)}
	m.noteDialog.Selected = noteDialogFocusEditor
	m.err = nil
	m.status = "Selection mode: move to the start and press Space"
	return m.noteDialog.Editor.Focus()
}

func (m Model) updateNoteTextSelectionMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	dialog := m.noteDialog
	if dialog == nil || dialog.Selection == nil {
		return m, nil
	}

	switch msg.String() {
	case "esc":
		dialog.Selection = nil
		m.err = nil
		m.status = "Text selection canceled"
		return m, m.restoreNoteDialogFocus()
	case " ":
		return m, m.toggleNoteTextSelectionMark()
	}

	if !noteSelectionNavigationKey(msg.String()) {
		return m, nil
	}

	var cmd tea.Cmd
	dialog.Editor, cmd = dialog.Editor.Update(msg)
	dialog.Selection.ViewportY = noteSelectionViewportForCursor(dialog.Editor, dialog.Selection.ViewportY)
	return m, cmd
}

func noteSelectionNavigationKey(key string) bool {
	switch key {
	case "left", "right", "up", "down", "home", "end",
		"ctrl+a", "ctrl+e", "ctrl+b", "ctrl+f", "ctrl+n", "ctrl+p",
		"alt+left", "alt+right", "alt+b", "alt+f",
		"alt+<", "alt+>", "ctrl+home", "ctrl+end":
		return true
	default:
		return false
	}
}

func (m *Model) toggleNoteTextSelectionMark() tea.Cmd {
	dialog := m.noteDialog
	if dialog == nil || dialog.Selection == nil {
		return nil
	}
	line, col := noteEditorCursor(dialog.Editor)
	selection := dialog.Selection
	if !selection.AnchorSet {
		selection.AnchorSet = true
		selection.AnchorLine = line
		selection.AnchorCol = col
		m.err = nil
		m.status = "Selection start set. Move to the end and press Space again."
		return nil
	}

	text := noteSelectedText(dialog.Editor, selection.AnchorLine, selection.AnchorCol, line, col)
	if text == "" {
		m.err = nil
		m.status = "Selection is empty. Move the cursor and press Space again."
		return nil
	}
	if err := clipboardTextWriter(text); err != nil {
		m.err = err
		m.status = "Note copy failed"
		return nil
	}
	m.err = nil
	dialog.Selection = nil
	m.status = noteCopyScopeStatus(noteCopyScopeSelectedText)
	return m.restoreNoteDialogFocus()
}

func (m *Model) copyNoteDialogSelection(scope int) {
	dialog := m.noteDialog
	if dialog == nil {
		return
	}
	text := noteCopyScopeText(dialog.Editor, scope)
	if text == "" {
		m.err = nil
		m.status = "Nothing to copy for that note selection"
		return
	}
	if err := clipboardTextWriter(text); err != nil {
		m.err = err
		m.status = "Note copy failed"
		return
	}
	m.err = nil
	m.status = noteCopyScopeStatus(scope)
}

func noteCopyScopeText(editor textarea.Model, scope int) string {
	switch scope {
	case noteCopyScopeWhole:
		return strings.ReplaceAll(editor.Value(), "\r\n", "\n")
	default:
		return ""
	}
}

func noteEditorCursor(editor textarea.Model) (int, int) {
	lines := noteEditorLines(editor)
	line := editor.Line()
	if len(lines) == 0 {
		return 0, 0
	}
	line = max(0, min(line, len(lines)-1))
	info := editor.LineInfo()
	col := info.StartColumn + info.ColumnOffset
	runes := []rune(lines[line])
	col = max(0, min(col, len(runes)))
	return line, col
}

func noteEditorLines(editor textarea.Model) []string {
	lines := strings.Split(strings.ReplaceAll(editor.Value(), "\r\n", "\n"), "\n")
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

func noteSelectedText(editor textarea.Model, startLine, startCol, endLine, endCol int) string {
	lines := noteEditorLines(editor)
	startLine = max(0, min(startLine, len(lines)-1))
	endLine = max(0, min(endLine, len(lines)-1))
	startCol = max(0, min(startCol, len([]rune(lines[startLine]))))
	endCol = max(0, min(endCol, len([]rune(lines[endLine]))))
	if noteCursorAfter(startLine, startCol, endLine, endCol) {
		startLine, endLine = endLine, startLine
		startCol, endCol = endCol, startCol
	}
	if startLine == endLine {
		return noteSliceRunes(lines[startLine], startCol, endCol)
	}
	segments := []string{noteSliceRunes(lines[startLine], startCol, len([]rune(lines[startLine])))}
	for line := startLine + 1; line < endLine; line++ {
		segments = append(segments, lines[line])
	}
	segments = append(segments, noteSliceRunes(lines[endLine], 0, endCol))
	return strings.Join(segments, "\n")
}

func noteSliceRunes(line string, startCol, endCol int) string {
	runes := []rune(line)
	startCol = max(0, min(startCol, len(runes)))
	endCol = max(0, min(endCol, len(runes)))
	if startCol > endCol {
		startCol, endCol = endCol, startCol
	}
	return string(runes[startCol:endCol])
}

func noteCursorAfter(lineA, colA, lineB, colB int) bool {
	if lineA != lineB {
		return lineA > lineB
	}
	return colA > colB
}

func noteTextSelectionHint(dialog *noteDialogState) string {
	if dialog == nil || dialog.Selection == nil {
		return ""
	}
	line, col := noteEditorCursor(dialog.Editor)
	if !dialog.Selection.AnchorSet {
		return "Selection mode is on. Move to the start of the text you want and press Space to mark it. Esc cancels."
	}
	return "Start " + noteCursorLabel(dialog.Selection.AnchorLine, dialog.Selection.AnchorCol) + " set. Move to the end and press Space again to copy. Cursor " + noteCursorLabel(line, col) + "."
}

func noteCursorLabel(line, col int) string {
	return "L" + strconv.Itoa(line+1) + ":" + strconv.Itoa(col+1)
}

func renderNoteSelectionEditor(editor textarea.Model, selection *noteTextSelectionState) string {
	displayLines := noteSelectionDisplayLines(editor)
	height := max(1, editor.Height())
	width := max(1, editor.Width())
	offset := noteSelectionViewportForCursor(editor, selection.ViewportY)
	maxOffset := max(0, len(displayLines)-height)
	if offset > maxOffset {
		offset = maxOffset
	}

	cursorRow, cursorCol := noteSelectionDisplayCursor(editor)
	startLine, startCol, endLine, endCol, hasSelection := noteSelectionRange(editor, selection)

	lines := make([]string, 0, height)
	for row := 0; row < height; row++ {
		index := offset + row
		if index >= len(displayLines) {
			lines = append(lines, noteSelectionLineStyle.Render(strings.Repeat(" ", width)))
			continue
		}
		line := displayLines[index]
		lines = append(lines, renderNoteSelectionDisplayLine(line, width, index == cursorRow, cursorCol, startLine, startCol, endLine, endCol, hasSelection))
	}
	return strings.Join(lines, "\n")
}

func renderNoteSelectionDisplayLine(
	line noteSelectionDisplayLine,
	width int,
	cursorVisible bool,
	cursorCol int,
	startLine int,
	startCol int,
	endLine int,
	endCol int,
	hasSelection bool,
) string {
	baseStyle := noteSelectionLineStyle
	if cursorVisible {
		baseStyle = noteSelectionCursorLineStyle
	}
	runes := []rune(line.text)
	selectionStart, selectionEnd, ok := noteSelectionRangeForDisplayLine(line, startLine, startCol, endLine, endCol, hasSelection)

	var out strings.Builder
	lineWidth := 0
	for index, r := range runes {
		style := baseStyle
		if ok && index >= selectionStart && index < selectionEnd {
			style = noteSelectionRangeStyle
		}
		if cursorVisible && index == cursorCol {
			if ok && index >= selectionStart && index < selectionEnd {
				style = noteSelectionCursorRangeStyle
			} else {
				style = noteSelectionCursorStyle
			}
		}
		out.WriteString(style.Render(string(r)))
		lineWidth += rw.RuneWidth(r)
	}
	if cursorVisible && cursorCol == len(runes) {
		style := noteSelectionCursorStyle
		if ok && cursorCol >= selectionStart && cursorCol < selectionEnd {
			style = noteSelectionCursorRangeStyle
		}
		out.WriteString(style.Render(" "))
		lineWidth++
	}
	if lineWidth < width {
		out.WriteString(baseStyle.Render(strings.Repeat(" ", width-lineWidth)))
	}
	return out.String()
}

func noteSelectionRangeForDisplayLine(
	line noteSelectionDisplayLine,
	startLine int,
	startCol int,
	endLine int,
	endCol int,
	hasSelection bool,
) (int, int, bool) {
	if !hasSelection {
		return 0, 0, false
	}
	if noteCursorAfter(startLine, startCol, line.rawLine, line.endCol) || (startLine == line.rawLine && startCol == line.endCol) {
		return 0, 0, false
	}
	if noteCursorAfter(line.rawLine, line.startCol, endLine, endCol) || (line.rawLine == endLine && line.startCol == endCol) {
		return 0, 0, false
	}
	selectionStart := line.startCol
	if line.rawLine == startLine {
		selectionStart = max(selectionStart, startCol)
	}
	selectionEnd := line.endCol
	if line.rawLine == endLine {
		selectionEnd = min(selectionEnd, endCol)
	}
	if selectionStart >= selectionEnd {
		return 0, 0, false
	}
	return selectionStart - line.startCol, selectionEnd - line.startCol, true
}

func noteSelectionRange(editor textarea.Model, selection *noteTextSelectionState) (int, int, int, int, bool) {
	if selection == nil || !selection.AnchorSet {
		return 0, 0, 0, 0, false
	}
	line, col := noteEditorCursor(editor)
	startLine, startCol := selection.AnchorLine, selection.AnchorCol
	endLine, endCol := line, col
	if noteCursorAfter(startLine, startCol, endLine, endCol) {
		startLine, endLine = endLine, startLine
		startCol, endCol = endCol, startCol
	}
	return startLine, startCol, endLine, endCol, true
}

func noteSelectionDisplayLines(editor textarea.Model) []noteSelectionDisplayLine {
	lines := noteEditorLines(editor)
	width := max(1, editor.Width())
	displayLines := make([]noteSelectionDisplayLine, 0, len(lines))
	for lineIndex, line := range lines {
		displayLines = append(displayLines, noteWrapDisplayLine(line, lineIndex, width)...)
	}
	if len(displayLines) == 0 {
		return []noteSelectionDisplayLine{{}}
	}
	return displayLines
}

func noteWrapDisplayLine(line string, rawLine int, width int) []noteSelectionDisplayLine {
	wrapped := noteWrapTextareaRunes([]rune(line), width)
	if len(wrapped) == 0 {
		return []noteSelectionDisplayLine{{rawLine: rawLine}}
	}
	lineRunes := []rune(line)
	consumed := 0
	displayLines := make([]noteSelectionDisplayLine, 0, len(wrapped))
	for _, wrappedLine := range wrapped {
		actualLen := min(len(wrappedLine), len(lineRunes)-consumed)
		if actualLen < 0 {
			actualLen = 0
		}
		displayLines = append(displayLines, noteSelectionDisplayLine{
			rawLine:  rawLine,
			startCol: consumed,
			endCol:   consumed + actualLen,
			text:     string(wrappedLine[:actualLen]),
		})
		consumed += actualLen
	}
	return displayLines
}

func noteSelectionDisplayCursor(editor textarea.Model) (int, int) {
	displayLines := noteSelectionDisplayLines(editor)
	line, col := noteEditorCursor(editor)
	for row, displayLine := range displayLines {
		if displayLine.rawLine != line {
			continue
		}
		if col < displayLine.startCol || col > displayLine.endCol {
			continue
		}
		if col == displayLine.endCol && row+1 < len(displayLines) && displayLines[row+1].rawLine == line && displayLines[row+1].startCol == col {
			continue
		}
		return row, col - displayLine.startCol
	}
	return 0, 0
}

func noteSelectionInitialViewport(editor textarea.Model) int {
	cursorRow, _ := noteSelectionDisplayCursor(editor)
	return max(0, cursorRow-editor.Height()+1)
}

func noteSelectionViewportForCursor(editor textarea.Model, offset int) int {
	displayLines := noteSelectionDisplayLines(editor)
	height := max(1, editor.Height())
	maxOffset := max(0, len(displayLines)-height)
	if offset > maxOffset {
		offset = maxOffset
	}
	cursorRow, _ := noteSelectionDisplayCursor(editor)
	if cursorRow < offset {
		offset = cursorRow
	}
	if cursorRow >= offset+height {
		offset = cursorRow - height + 1
	}
	if offset < 0 {
		offset = 0
	}
	if offset > maxOffset {
		offset = maxOffset
	}
	return offset
}

func noteWrapTextareaRunes(runes []rune, width int) [][]rune {
	if width <= 0 {
		return [][]rune{runes}
	}
	var (
		lines  = [][]rune{{}}
		word   = []rune{}
		row    int
		spaces int
	)

	for _, r := range runes {
		if unicode.IsSpace(r) {
			spaces++
		} else {
			word = append(word, r)
		}

		if spaces > 0 {
			if uniseg.StringWidth(string(lines[row]))+uniseg.StringWidth(string(word))+spaces > width {
				row++
				lines = append(lines, []rune{})
				lines[row] = append(lines[row], word...)
				lines[row] = append(lines[row], noteRepeatSpaces(spaces)...)
				spaces = 0
				word = nil
			} else {
				lines[row] = append(lines[row], word...)
				lines[row] = append(lines[row], noteRepeatSpaces(spaces)...)
				spaces = 0
				word = nil
			}
		} else if len(word) > 0 {
			lastCharLen := rw.RuneWidth(word[len(word)-1])
			if uniseg.StringWidth(string(word))+lastCharLen > width {
				if len(lines[row]) > 0 {
					row++
					lines = append(lines, []rune{})
				}
				lines[row] = append(lines[row], word...)
				word = nil
			}
		}
	}

	if uniseg.StringWidth(string(lines[row]))+uniseg.StringWidth(string(word))+spaces >= width {
		lines = append(lines, []rune{})
		lines[row+1] = append(lines[row+1], word...)
		spaces++
		lines[row+1] = append(lines[row+1], noteRepeatSpaces(spaces)...)
	} else {
		lines[row] = append(lines[row], word...)
		spaces++
		lines[row] = append(lines[row], noteRepeatSpaces(spaces)...)
	}

	return lines
}

func noteRepeatSpaces(n int) []rune {
	return []rune(strings.Repeat(" ", n))
}
