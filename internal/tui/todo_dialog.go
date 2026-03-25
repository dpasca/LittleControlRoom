package tui

import (
	"fmt"
	"strings"

	"lcroom/internal/codexapp"
	"lcroom/internal/model"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const (
	todoCopyScopeCodex = iota
	todoCopyScopeOpenCode
	todoCopyScopeCancel
)

const (
	todoDeleteConfirmFocusDelete = iota
	todoDeleteConfirmFocusKeep
)

type todoDialogState struct {
	ProjectPath string
	ProjectName string
	Selected    int
	Offset      int
}

type todoEditorState struct {
	ProjectPath string
	ProjectName string
	TodoID      int64
	Input       textarea.Model
	Submitting  bool
}

type todoDeleteConfirmState struct {
	ProjectPath string
	ProjectName string
	TodoID      int64
	TodoText    string
	Selected    int
}

type todoLaunchDraftState struct {
	projectPath string
	provider    codexapp.Provider
}

type todoCopyDialogState struct {
	ProjectPath string
	ProjectName string
	TodoText    string
	Selected    int
}

func normalizeTodoText(text string) string {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, " ")
}

func newTodoTextInput(value string) textarea.Model {
	input := textarea.New()
	input.Prompt = ""
	input.Placeholder = "Change font color to red when there's an error"
	input.CharLimit = 1024
	input.ShowLineNumbers = false
	styleNoteTextarea(&input)
	input.SetWidth(72)
	input.SetHeight(6)
	input.SetValue(value)
	return input
}

func (m *Model) openTodoDialogForSelection() tea.Cmd {
	project, ok := m.selectedProject()
	if !ok {
		m.status = "No project selected"
		return nil
	}
	return m.openTodoDialog(project)
}

func (m *Model) openTodoDialog(project model.ProjectSummary) tea.Cmd {
	m.todoDialog = &todoDialogState{
		ProjectPath: project.Path,
		ProjectName: noteProjectTitle(project.Path, project.Name),
	}
	m.todoEditor = nil
	m.todoDeleteConfirm = nil
	m.commandMode = false
	m.showHelp = false
	m.err = nil
	m.status = "TODO list open. Enter starts selected item; a adds, e edits, space toggles"
	m.syncTodoDialogSelection()
	return nil
}

func (m Model) todoItemsFor(projectPath string) []model.TodoItem {
	projectPath = strings.TrimSpace(projectPath)
	if projectPath == "" {
		return nil
	}
	if strings.TrimSpace(m.detail.Summary.Path) == projectPath {
		return append([]model.TodoItem(nil), m.detail.Todos...)
	}
	return nil
}

func (m *Model) syncTodoDialogSelection() {
	dialog := m.todoDialog
	if dialog == nil {
		return
	}
	items := m.todoItemsFor(dialog.ProjectPath)
	if len(items) == 0 {
		dialog.Selected = 0
		dialog.Offset = 0
		return
	}
	if dialog.Selected < 0 {
		dialog.Selected = 0
	}
	if dialog.Selected >= len(items) {
		dialog.Selected = len(items) - 1
	}
	listHeight := max(1, m.height-12)
	if dialog.Offset < 0 {
		dialog.Offset = 0
	}
	maxOffset := max(0, len(items)-listHeight)
	if dialog.Offset > maxOffset {
		dialog.Offset = maxOffset
	}
	if dialog.Selected < dialog.Offset {
		dialog.Offset = dialog.Selected
	}
	if dialog.Selected >= dialog.Offset+listHeight {
		dialog.Offset = dialog.Selected - listHeight + 1
	}
}

func (m Model) selectedTodoItem() (model.TodoItem, bool) {
	if m.todoDialog == nil {
		return model.TodoItem{}, false
	}
	items := m.todoItemsFor(m.todoDialog.ProjectPath)
	if len(items) == 0 {
		return model.TodoItem{}, false
	}
	selected := m.todoDialog.Selected
	if selected < 0 || selected >= len(items) {
		return model.TodoItem{}, false
	}
	return items[selected], true
}

func (m *Model) moveTodoSelection(delta int) {
	if m.todoDialog == nil {
		return
	}
	items := m.todoItemsFor(m.todoDialog.ProjectPath)
	if len(items) == 0 {
		m.todoDialog.Selected = 0
		return
	}
	m.todoDialog.Selected = max(0, min(len(items)-1, m.todoDialog.Selected+delta))
	m.syncTodoDialogSelection()
}

func (m *Model) closeTodoDialog(status string) {
	m.todoDialog = nil
	if status != "" {
		m.status = status
	}
}

func (m *Model) openTodoEditor(todoID int64, value string) tea.Cmd {
	if m.todoDialog == nil {
		return nil
	}
	m.todoEditor = &todoEditorState{
		ProjectPath: m.todoDialog.ProjectPath,
		ProjectName: m.todoDialog.ProjectName,
		TodoID:      todoID,
		Input:       newTodoTextInput(value),
	}
	m.err = nil
	if todoID > 0 {
		m.status = "Editing TODO"
	} else {
		m.status = "New TODO"
	}
	return m.todoEditor.Input.Focus()
}

func (m *Model) closeTodoEditor(status string) {
	if m.todoEditor != nil {
		m.todoEditor.Input.Blur()
	}
	m.todoEditor = nil
	if status != "" {
		m.status = status
	}
}

func (m *Model) openTodoDeleteConfirm(todo model.TodoItem) {
	if m.todoDialog == nil {
		return
	}
	m.todoDeleteConfirm = &todoDeleteConfirmState{
		ProjectPath: m.todoDialog.ProjectPath,
		ProjectName: m.todoDialog.ProjectName,
		TodoID:      todo.ID,
		TodoText:    todo.Text,
		Selected:    todoDeleteConfirmFocusKeep,
	}
	m.status = "Confirm TODO delete"
}

func (m *Model) closeTodoDeleteConfirm(status string) {
	m.todoDeleteConfirm = nil
	if status != "" {
		m.status = status
	}
}

func (m *Model) openTodoCopyDialog(todo model.TodoItem) {
	if m.todoDialog == nil {
		return
	}
	defaultSelection := todoCopyScopeCodex
	if project, ok := m.selectedProject(); ok && project.Path == m.todoDialog.ProjectPath {
		if preferredEmbeddedProviderForProject(project) == codexapp.ProviderOpenCode {
			defaultSelection = todoCopyScopeOpenCode
		}
	}
	m.todoCopyDialog = &todoCopyDialogState{
		ProjectPath: m.todoDialog.ProjectPath,
		ProjectName: m.todoDialog.ProjectName,
		TodoText:    todo.Text,
		Selected:    defaultSelection,
	}
	m.status = "Copy TODO to clipboard"
}

func (m *Model) closeTodoCopyDialog(status string) tea.Cmd {
	m.todoCopyDialog = nil
	if status != "" {
		m.status = status
	}
	return nil
}

func (m Model) updateTodoDialogMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.closeTodoDialog("TODO list closed")
		return m, nil
	case "up", "k":
		m.moveTodoSelection(-1)
		return m, nil
	case "down", "j":
		m.moveTodoSelection(1)
		return m, nil
	case "a":
		return m, m.openTodoEditor(0, "")
	case "e":
		item, ok := m.selectedTodoItem()
		if !ok {
			m.status = "No TODO selected"
			return m, nil
		}
		return m, m.openTodoEditor(item.ID, item.Text)
	case "d":
		item, ok := m.selectedTodoItem()
		if !ok {
			m.status = "No TODO selected"
			return m, nil
		}
		m.openTodoDeleteConfirm(item)
		return m, nil
	case " ":
		item, ok := m.selectedTodoItem()
		if !ok {
			m.status = "No TODO selected"
			return m, nil
		}
		m.status = "Updating TODO..."
		return m, m.toggleTodoDoneCmd(item)
	case "enter":
		item, ok := m.selectedTodoItem()
		if !ok {
			m.status = "No TODO selected"
			return m, nil
		}
		m.openTodoCopyDialog(item)
		return m, nil
	case "c":
		item, ok := m.selectedTodoItem()
		if !ok {
			m.status = "No TODO selected"
			return m, nil
		}
		text := strings.TrimSpace(item.Text)
		if text == "" {
			m.status = "Nothing to copy for empty TODO"
			return m, nil
		}
		if err := clipboardTextWriter(text); err != nil {
			m.err = err
			m.status = "TODO copy failed"
			return m, nil
		}
		m.err = nil
		m.status = "TODO copied to clipboard"
		return m, nil
	}
	return m, nil
}

func (m Model) updateTodoCopyDialogMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	copyDialog := m.todoCopyDialog
	if copyDialog == nil {
		return m, nil
	}
	switch msg.String() {
	case "esc":
		return m, m.closeTodoCopyDialog("TODO start canceled")
	case "tab", "shift+tab", "left", "right", "up", "down":
		delta := 1
		if msg.String() == "shift+tab" || msg.String() == "left" || msg.String() == "up" {
			delta = -1
		}
		return m, m.moveTodoCopyDialogSelection(delta)
	case "enter", " ":
		return m.activateTodoCopyDialogSelection()
	}
	return m, nil
}

func (m *Model) moveTodoCopyDialogSelection(delta int) tea.Cmd {
	copyDialog := m.todoCopyDialog
	if copyDialog == nil || delta == 0 {
		return nil
	}
	index := copyDialog.Selected + delta
	if index < todoCopyScopeCodex {
		index = todoCopyScopeCancel
	}
	if index > todoCopyScopeCancel {
		index = todoCopyScopeCodex
	}
	copyDialog.Selected = index
	return nil
}

func (m *Model) activateTodoCopyDialogSelection() (tea.Model, tea.Cmd) {
	copyDialog := m.todoCopyDialog
	if copyDialog == nil {
		return m, nil
	}
	if copyDialog.Selected == todoCopyScopeCancel {
		m.todoCopyDialog = nil
		m.status = "TODO start canceled"
		return m, nil
	}
	var provider codexapp.Provider
	switch copyDialog.Selected {
	case todoCopyScopeCodex:
		provider = codexapp.ProviderCodex
	case todoCopyScopeOpenCode:
		provider = codexapp.ProviderOpenCode
	}
	m.todoCopyDialog = nil
	return m.startSelectedTodoWithProvider(provider)
}

func (m Model) updateTodoEditorMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	dialog := m.todoEditor
	if dialog == nil {
		return m, nil
	}
	if dialog.Submitting {
		return m, nil
	}
	switch msg.String() {
	case "esc":
		m.closeTodoEditor("TODO edit canceled")
		return m, nil
	case "ctrl+s":
		text := normalizeTodoText(dialog.Input.Value())
		if text == "" {
			m.status = "TODO text is required"
			return m, nil
		}
		dialog.Submitting = true
		if dialog.TodoID > 0 {
			m.status = "Saving TODO..."
			return m, m.updateTodoCmd(dialog.ProjectPath, dialog.TodoID, text)
		}
		m.status = "Adding TODO..."
		return m, m.addTodoCmd(dialog.ProjectPath, text)
	}
	var cmd tea.Cmd
	dialog.Input, cmd = dialog.Input.Update(msg)
	return m, cmd
}

func (m Model) updateTodoDeleteConfirmMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	confirm := m.todoDeleteConfirm
	if confirm == nil {
		return m, nil
	}
	switch msg.String() {
	case "esc":
		m.closeTodoDeleteConfirm("TODO delete canceled")
		return m, nil
	case "left", "h", "right", "l", "tab", "shift+tab":
		if confirm.Selected == todoDeleteConfirmFocusDelete {
			confirm.Selected = todoDeleteConfirmFocusKeep
		} else {
			confirm.Selected = todoDeleteConfirmFocusDelete
		}
		return m, nil
	case "enter":
		if confirm.Selected != todoDeleteConfirmFocusDelete {
			m.closeTodoDeleteConfirm("TODO delete canceled")
			return m, nil
		}
		m.status = "Deleting TODO..."
		return m, m.deleteTodoCmd(confirm.ProjectPath, confirm.TodoID)
	}
	return m, nil
}

func (m Model) addTodoCmd(projectPath, text string) tea.Cmd {
	if m.svc == nil {
		return func() tea.Msg {
			return todoActionMsg{projectPath: projectPath, err: fmt.Errorf("service unavailable")}
		}
	}
	return func() tea.Msg {
		_, err := m.svc.AddTodo(m.ctx, projectPath, text)
		return todoActionMsg{
			projectPath: projectPath,
			status:      "TODO added",
			err:         err,
		}
	}
}

func (m Model) updateTodoCmd(projectPath string, todoID int64, text string) tea.Cmd {
	if m.svc == nil {
		return func() tea.Msg {
			return todoActionMsg{projectPath: projectPath, err: fmt.Errorf("service unavailable")}
		}
	}
	return func() tea.Msg {
		err := m.svc.UpdateTodo(m.ctx, projectPath, todoID, text)
		return todoActionMsg{
			projectPath: projectPath,
			status:      "TODO saved",
			err:         err,
		}
	}
}

func (m Model) toggleTodoDoneCmd(item model.TodoItem) tea.Cmd {
	if m.svc == nil {
		return func() tea.Msg {
			return todoActionMsg{projectPath: item.ProjectPath, err: fmt.Errorf("service unavailable")}
		}
	}
	return func() tea.Msg {
		err := m.svc.ToggleTodoDone(m.ctx, item.ProjectPath, item.ID, !item.Done)
		status := "TODO marked done"
		if item.Done {
			status = "TODO reopened"
		}
		return todoActionMsg{
			projectPath: item.ProjectPath,
			status:      status,
			err:         err,
		}
	}
}

func (m Model) deleteTodoCmd(projectPath string, todoID int64) tea.Cmd {
	if m.svc == nil {
		return func() tea.Msg {
			return todoActionMsg{projectPath: projectPath, err: fmt.Errorf("service unavailable")}
		}
	}
	return func() tea.Msg {
		err := m.svc.DeleteTodo(m.ctx, projectPath, todoID)
		return todoActionMsg{
			projectPath: projectPath,
			status:      "TODO deleted",
			err:         err,
		}
	}
}

func (m Model) startSelectedTodoWithProvider(provider codexapp.Provider) (tea.Model, tea.Cmd) {
	item, ok := m.selectedTodoItem()
	if !ok {
		m.status = "No TODO selected"
		return m, nil
	}
	project, ok := m.selectedProject()
	if !ok {
		m.status = "No project selected"
		return m, nil
	}
	if strings.TrimSpace(string(provider)) == "" {
		provider = preferredEmbeddedProviderForProject(project)
	} else {
		provider = provider.Normalized()
	}
	m.restoreCodexDraft(project.Path, codexDraft{Text: strings.TrimSpace(item.Text)})
	m.todoLaunchDraft = &todoLaunchDraftState{projectPath: project.Path, provider: provider}
	m.todoEditor = nil
	m.todoDeleteConfirm = nil
	m.todoDialog = nil
	return m.launchEmbeddedForSelection(provider, true, "")
}

func (m *Model) syncTodoDialogSize() {
	if m.todoDialog != nil {
		m.syncTodoDialogSelection()
	}
}

func (m *Model) syncTodoEditorSize() {
	if m.todoEditor != nil {
		_, panelInnerW, editorHeight := todoEditorPanelLayout(m.width, m.height)
		m.todoEditor.Input.SetWidth(max(24, panelInnerW))
		m.todoEditor.Input.SetHeight(editorHeight)
	}
}

func todoDialogPanelLayout(bodyW, bodyH int) (int, int, int) {
	panelW := min(max(62, bodyW-8), 96)
	panelInnerW := max(24, panelW-4)
	listH := max(10, min(20, bodyH-10))
	return panelW, panelInnerW, listH
}

func todoEditorPanelLayout(bodyW, bodyH int) (int, int, int) {
	panelW := min(max(62, bodyW-6), 96)
	panelInnerW := max(28, panelW-4)
	editorH := max(8, min(16, bodyH-14))
	return panelW, panelInnerW, editorH
}

func (m Model) renderTodoDialogOverlay(body string, bodyW, bodyH int) string {
	panelW, panelInnerW, listH := todoDialogPanelLayout(bodyW, bodyH)
	dialog := m.todoDialog
	if dialog == nil {
		return body
	}
	items := m.todoItemsFor(dialog.ProjectPath)
	openCount := 0
	for _, item := range items {
		if !item.Done {
			openCount++
		}
	}
	title := detailSectionStyle.Render("TODO") + "  " + detailValueStyle.Render(dialog.ProjectName)
	summary := detailMutedStyle.Render(fmt.Sprintf("%d open, %d total", openCount, len(items)))
	lines := []string{title, summary, ""}
	if len(items) == 0 {
		lines = append(lines, detailMutedStyle.Render("No TODOs yet"))
		lines = append(lines, detailMutedStyle.Render("Press a to add one"))
	} else {
		start := min(dialog.Offset, len(items))
		end := min(len(items), start+listH)
		for i := start; i < end; i++ {
			item := items[i]
			prefix := "[ ]"
			style := detailValueStyle
			if item.Done {
				prefix = "[x]"
				style = detailMutedStyle
			}
			line := prefix + " " + truncateText(strings.TrimSpace(item.Text), max(12, panelInnerW-6))
			if i == dialog.Selected {
				line = noteDialogButtonSelectedStyle.UnsetPadding().Width(panelInnerW).Render(line)
			} else {
				line = style.Render(line)
			}
			lines = append(lines, line)
		}
		if end < len(items) {
			lines = append(lines, detailMutedStyle.Render(fmt.Sprintf("... %d more", len(items)-end)))
		}
	}
	lines = append(lines, "")
	lines = append(lines, todoDialogLegendLine())
	panel := renderDialogPanel(panelW, panelInnerW, strings.Join(lines, "\n"))
	left := max(0, (bodyW-panelW)/2)
	top := max(0, (bodyH-lipgloss.Height(panel))/2)
	return overlayBlock(body, panel, bodyW, bodyH, left, top)
}

func (m Model) renderTodoEditorOverlay(body string, bodyW, bodyH int) string {
	dialog := m.todoEditor
	if dialog == nil {
		return body
	}
	panelW, panelInnerW, editorHeight := todoEditorPanelLayout(bodyW, bodyH)
	title := "New TODO"
	if dialog.TodoID > 0 {
		title = "Edit TODO"
	}
	dialog.Input.SetWidth(max(24, panelInnerW))
	dialog.Input.SetHeight(editorHeight)
	lines := []string{
		detailSectionStyle.Render(title) + "  " + detailValueStyle.Render(dialog.ProjectName),
		"",
		dialog.Input.View(),
		"",
		todoEditorLegendLine(),
	}
	panel := renderDialogPanel(panelW, panelInnerW, strings.Join(lines, "\n"))
	left := max(0, (bodyW-panelW)/2)
	top := max(0, (bodyH-lipgloss.Height(panel))/2)
	return overlayBlock(body, panel, bodyW, bodyH, left, top)
}

func todoDialogLegendLine() string {
	return renderHelpPanelActionRow(
		renderDialogAction("a", "add", commitActionKeyStyle, commitActionTextStyle),
		renderDialogAction("e", "edit", navigateActionKeyStyle, navigateActionTextStyle),
		renderDialogAction("space", "done", pushActionKeyStyle, pushActionTextStyle),
		renderDialogAction("d", "delete", cancelActionKeyStyle, cancelActionTextStyle),
		renderDialogAction("c", "copy", navigateActionKeyStyle, navigateActionTextStyle),
		renderDialogAction("Enter", "start", commitActionKeyStyle, commitActionTextStyle),
		renderDialogAction("Esc", "close", cancelActionKeyStyle, cancelActionTextStyle),
	)
}

func todoEditorLegendLine() string {
	return renderHelpPanelActionRow(
		renderDialogAction("enter", "newline", navigateActionKeyStyle, navigateActionTextStyle),
		renderDialogAction("ctrl+s", "save", commitActionKeyStyle, commitActionTextStyle),
		renderDialogAction("Esc", "cancel", cancelActionKeyStyle, cancelActionTextStyle),
	)
}

func (m Model) renderTodoDeleteConfirmOverlay(body string, bodyW, bodyH int) string {
	confirm := m.todoDeleteConfirm
	if confirm == nil {
		return body
	}
	panelW := min(max(46, bodyW-24), 72)
	panelInnerW := max(24, panelW-4)
	buttons := lipgloss.JoinHorizontal(
		lipgloss.Left,
		renderNoteDialogButton("Delete", confirm.Selected == todoDeleteConfirmFocusDelete),
		" ",
		renderNoteDialogButton("Keep", confirm.Selected == todoDeleteConfirmFocusKeep),
	)
	lines := []string{
		detailSectionStyle.Render("Delete TODO") + "  " + detailValueStyle.Render(confirm.ProjectName),
		"",
		detailValueStyle.Render(truncateText(strings.TrimSpace(confirm.TodoText), panelInnerW)),
		"",
		buttons,
	}
	panel := renderDialogPanel(panelW, panelInnerW, strings.Join(lines, "\n"))
	left := max(0, (bodyW-panelW)/2)
	top := max(0, (bodyH-lipgloss.Height(panel))/2)
	return overlayBlock(body, panel, bodyW, bodyH, left, top)
}

func (m Model) renderTodoCopyDialogOverlay(body string, bodyW, bodyH int) string {
	copyDialog := m.todoCopyDialog
	if copyDialog == nil {
		return body
	}
	panelW := min(bodyW, min(max(52, bodyW-16), 82))
	panelInnerW := max(24, panelW-4)
	lines := []string{
		renderDialogHeader("Start TODO", copyDialog.ProjectName, "", panelInnerW),
		detailValueStyle.Render(truncateText(strings.TrimSpace(copyDialog.TodoText), panelInnerW)),
		"",
	}
	options := []int{
		todoCopyScopeCodex,
		todoCopyScopeOpenCode,
		todoCopyScopeCancel,
	}
	for _, option := range options {
		lines = append(lines, renderNoteDialogButton(todoCopyScopeLabel(option), copyDialog.Selected == option))
	}
	lines = append(lines, commandPaletteHintStyle.Render("Tab, arrows, or Shift+Tab switch options. Enter runs the selected action. Esc cancels."))
	panel := renderDialogPanel(panelW, panelInnerW, strings.Join(lines, "\n"))
	left := max(0, (bodyW-panelW)/2)
	top := max(0, (bodyH-lipgloss.Height(panel))/3)
	return overlayBlock(body, panel, bodyW, bodyH, left, top)
}

func todoCopyScopeLabel(scope int) string {
	switch scope {
	case todoCopyScopeCodex:
		return "Start with Codex"
	case todoCopyScopeOpenCode:
		return "Start with OpenCode"
	default:
		return "Cancel"
	}
}
