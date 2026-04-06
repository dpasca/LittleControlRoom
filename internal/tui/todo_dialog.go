package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"lcroom/internal/codexapp"
	"lcroom/internal/model"
	"lcroom/internal/service"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

const (
	todoCopyModeHere = iota
	todoCopyModeNewWorktree
)

const (
	todoDeleteConfirmFocusDelete = iota
	todoDeleteConfirmFocusKeep
)

const todoTextCharLimit = 20000

type todoDialogState struct {
	ProjectPath string
	ProjectName string
	Selected    int
	Offset      int
	Busy        bool
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
	DoneCount   int
	Selected    int
}

type todoLaunchDraftState struct {
	projectPath    string
	provider       codexapp.Provider
	openModelFirst bool
	autoSubmit     bool
}

func normalizeTodoLaunchDraftProjectPath(projectPath string) string {
	return strings.TrimSpace(projectPath)
}

func (m *Model) todoLaunchDraftFor(projectPath string) (todoLaunchDraftState, bool) {
	projectPath = normalizeTodoLaunchDraftProjectPath(projectPath)
	if projectPath == "" || m.todoLaunchDrafts == nil {
		return todoLaunchDraftState{}, false
	}
	draft, ok := m.todoLaunchDrafts[projectPath]
	return draft, ok
}

func (m *Model) storeTodoLaunchDraft(draft todoLaunchDraftState) {
	projectPath := normalizeTodoLaunchDraftProjectPath(draft.projectPath)
	if projectPath == "" {
		return
	}
	if m.todoLaunchDrafts == nil {
		m.todoLaunchDrafts = make(map[string]todoLaunchDraftState)
	}
	draft.projectPath = projectPath
	m.todoLaunchDrafts[projectPath] = draft
}

func (m *Model) clearTodoLaunchDraft(projectPath string) {
	projectPath = normalizeTodoLaunchDraftProjectPath(projectPath)
	if projectPath == "" || m.todoLaunchDrafts == nil {
		return
	}
	delete(m.todoLaunchDrafts, projectPath)
	if len(m.todoLaunchDrafts) == 0 {
		m.todoLaunchDrafts = nil
	}
}

type todoPendingLaunchState struct {
	ID       int64
	Cancel   context.CancelFunc
	Canceled bool
}

type todoCopyDialogState struct {
	ProjectPath            string
	ProjectName            string
	TodoID                 int64
	TodoText               string
	RunMode                int
	Provider               codexapp.Provider
	OpenModelFirst         bool
	BranchOverride         string
	WorktreeSuffixOverride string
	LaunchID               int64
	Submitting             bool
}

type todoModelPickerReturnState struct {
	dialog             todoDialogState
	copyDialog         todoCopyDialogState
	prevVisibleProject string
}

type todoWorktreeEditorState struct {
	ProjectPath string
	ProjectName string
	TodoID      int64
	BranchInput textinput.Model
	FolderInput textinput.Model
	Selected    int
}

type todoExistingWorktreeDialogState struct {
	ProjectPath    string
	ProjectName    string
	TodoText       string
	Provider       codexapp.Provider
	OpenModelFirst bool
	Selected       int
	Candidates     []model.ProjectSummary
	ReturnCopy     *todoCopyDialogState
}

func normalizeTodoText(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	return text
}

func todoPreviewText(text string) string {
	return strings.Join(strings.Fields(normalizeTodoText(text)), " ")
}

func newTodoTextInput(value string) textarea.Model {
	input := textarea.New()
	input.Prompt = ""
	input.Placeholder = "Change font color to red when there's an error"
	input.CharLimit = todoTextCharLimit
	input.ShowLineNumbers = false
	styleDialogTextarea(&input)
	input.SetWidth(72)
	input.SetHeight(6)
	input.SetValue(value)
	return input
}

func newTodoWorktreeTextInput(value string, charLimit int) textinput.Model {
	input := textinput.New()
	input.Prompt = ""
	input.CharLimit = charLimit
	input.SetValue(strings.TrimSpace(value))
	return input
}

func (m *Model) openTodoDialogForSelection() tea.Cmd {
	project, ok := m.selectedProject()
	if !ok {
		m.status = "No project selected"
		return nil
	}
	if rootPath := projectWorktreeRootPath(project); rootPath != "" && filepath.Clean(rootPath) != filepath.Clean(project.Path) {
		if rootProject, ok := m.projectSummaryByPath(rootPath); ok {
			project = rootProject
		}
	}
	cmd := m.openTodoDialog(project)
	if filepath.Clean(strings.TrimSpace(m.detail.Summary.Path)) != filepath.Clean(project.Path) {
		return tea.Batch(cmd, m.requestProjectDetailViewCmd(project.Path))
	}
	return cmd
}

func (m *Model) openTodoDialog(project model.ProjectSummary) tea.Cmd {
	m.todoDialog = &todoDialogState{
		ProjectPath: project.Path,
		ProjectName: projectTitle(project.Path, project.Name),
	}
	m.todoEditor = nil
	m.todoDeleteConfirm = nil
	m.commandMode = false
	m.showHelp = false
	m.err = nil
	m.status = "TODO list open. Enter starts selected item; a adds, e edits, space toggles, p purges done"
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
	listHeight := m.todoDialogListHeight()
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

func (m *Model) pageMoveTodoSelection(delta int) {
	if delta == 0 {
		return
	}
	if m.todoDialog == nil {
		return
	}
	if delta > 0 {
		m.moveTodoSelection(m.todoDialogListHeight())
		return
	}
	m.moveTodoSelection(-m.todoDialogListHeight())
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

func (m *Model) updateTodoDialogMouseScroll(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.todoDialog == nil {
		return m, nil
	}
	if msg.Action != tea.MouseActionPress {
		return m, nil
	}
	if msg.Button != tea.MouseButtonWheelUp && msg.Button != tea.MouseButtonWheelDown {
		return m, nil
	}
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		m.moveTodoSelection(-1)
	case tea.MouseButtonWheelDown:
		m.moveTodoSelection(1)
	}
	return m, nil
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

func (m *Model) openTodoDonePurgeConfirm(doneCount int) {
	if m.todoDialog == nil || doneCount <= 0 {
		return
	}
	m.todoDeleteConfirm = &todoDeleteConfirmState{
		ProjectPath: m.todoDialog.ProjectPath,
		ProjectName: m.todoDialog.ProjectName,
		DoneCount:   doneCount,
		Selected:    todoDeleteConfirmFocusKeep,
	}
	m.status = "Confirm completed TODO purge"
}

func (m *Model) closeTodoDeleteConfirm(status string) {
	m.todoDeleteConfirm = nil
	if status != "" {
		m.status = status
	}
}

func completedTodoCount(items []model.TodoItem) int {
	count := 0
	for _, item := range items {
		if item.Done {
			count++
		}
	}
	return count
}

func (m *Model) openTodoCopyDialog(todo model.TodoItem) tea.Cmd {
	if m.todoDialog == nil {
		return nil
	}
	provider := codexapp.ProviderCodex
	if project, ok := m.selectedProject(); ok && project.Path == m.todoDialog.ProjectPath {
		provider = m.preferredEmbeddedProviderForProject(project)
	}
	m.todoCopyDialog = &todoCopyDialogState{
		ProjectPath: m.todoDialog.ProjectPath,
		ProjectName: m.todoDialog.ProjectName,
		TodoID:      todo.ID,
		TodoText:    todo.Text,
		RunMode:     todoCopyModeNewWorktree,
		Provider:    provider,
	}
	m.status = "Start TODO"
	return m.ensureTodoWorktreeSuggestionCmd(m.todoDialog.ProjectPath, todo.ID)
}

func (m *Model) closeTodoCopyDialog(status string) tea.Cmd {
	m.todoCopyDialog = nil
	if status != "" {
		m.status = status
	}
	return nil
}

func (m *Model) beginTodoPendingLaunch() (int64, context.Context) {
	m.todoLaunchSeq++
	parent := m.ctx
	if parent == nil {
		parent = context.Background()
	}
	launchCtx, cancel := context.WithCancel(parent)
	m.todoPendingLaunch = &todoPendingLaunchState{
		ID:     m.todoLaunchSeq,
		Cancel: cancel,
	}
	if m.todoCopyDialog != nil {
		m.todoCopyDialog.LaunchID = m.todoLaunchSeq
		m.todoCopyDialog.Submitting = true
	}
	return m.todoLaunchSeq, launchCtx
}

func (m *Model) cancelTodoPendingLaunch(status string) tea.Cmd {
	if pending := m.todoPendingLaunch; pending != nil {
		pending.Canceled = true
		if pending.Cancel != nil {
			pending.Cancel()
			pending.Cancel = nil
		}
	}
	return m.closeTodoCopyDialog(status)
}

func (m *Model) openTodoWorktreeEditor(item model.TodoItem) tea.Cmd {
	if m.todoCopyDialog == nil {
		return nil
	}
	branchName := strings.TrimSpace(m.todoCopyDialog.BranchOverride)
	folderName := strings.TrimSpace(m.todoCopyDialog.WorktreeSuffixOverride)
	if branchName == "" && item.WorktreeSuggestion != nil {
		branchName = strings.TrimSpace(item.WorktreeSuggestion.BranchName)
	}
	if folderName == "" && item.WorktreeSuggestion != nil {
		folderName = strings.TrimSpace(item.WorktreeSuggestion.WorktreeSuffix)
	}
	m.todoWorktreeEditor = &todoWorktreeEditorState{
		ProjectPath: m.todoCopyDialog.ProjectPath,
		ProjectName: m.todoCopyDialog.ProjectName,
		TodoID:      item.ID,
		BranchInput: newTodoWorktreeTextInput(branchName, 120),
		FolderInput: newTodoWorktreeTextInput(folderName, 120),
	}
	m.status = "Edit worktree names"
	return m.todoWorktreeEditor.BranchInput.Focus()
}

func (m *Model) closeTodoWorktreeEditor(status string) {
	m.todoWorktreeEditor = nil
	if status != "" {
		m.status = status
	}
}

func (m *Model) openTodoExistingWorktreeDialog(provider codexapp.Provider) {
	if m.todoCopyDialog == nil {
		return
	}
	candidates := m.existingWorktreeCandidates(m.todoCopyDialog.ProjectPath)
	if len(candidates) == 0 {
		m.status = "No existing worktrees for this repo yet"
		return
	}
	returnCopy := *m.todoCopyDialog
	m.todoExistingWorktree = &todoExistingWorktreeDialogState{
		ProjectPath:    m.todoCopyDialog.ProjectPath,
		ProjectName:    m.todoCopyDialog.ProjectName,
		TodoText:       m.todoCopyDialog.TodoText,
		Provider:       provider,
		OpenModelFirst: m.todoCopyDialog.OpenModelFirst,
		Candidates:     candidates,
		ReturnCopy:     &returnCopy,
	}
	m.todoCopyDialog = nil
	m.status = "Pick an existing worktree"
}

func (m *Model) closeTodoExistingWorktreeDialog(status string) {
	if dialog := m.todoExistingWorktree; dialog != nil && dialog.ReturnCopy != nil {
		m.todoCopyDialog = dialog.ReturnCopy
	}
	m.todoExistingWorktree = nil
	if status != "" {
		m.status = status
	}
}

func (m Model) updateTodoDialogMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	switch key {
	case "esc":
		m.closeTodoDialog("TODO list closed")
		return m, nil
	case "up", "k":
		m.moveTodoSelection(-1)
		return m, nil
	case "down", "j":
		m.moveTodoSelection(1)
		return m, nil
	case "pgup", "pageup":
		m.pageMoveTodoSelection(-1)
		return m, nil
	case "pgdown", "pagedown":
		m.pageMoveTodoSelection(1)
		return m, nil
	case "home":
		m.todoDialog.Selected = 0
		m.syncTodoDialogSelection()
		return m, nil
	case "end":
		if m.todoDialog != nil {
			items := m.todoItemsFor(m.todoDialog.ProjectPath)
			m.todoDialog.Selected = len(items) - 1
			m.syncTodoDialogSelection()
		}
		return m, nil
	}
	if m.todoDialog != nil && m.todoDialog.Busy {
		switch key {
		case "a", "e", "d", "p", " ", "enter", "c":
			m.status = "TODO update already in progress"
			return m, nil
		}
	}
	switch key {
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
	case "p":
		doneCount := completedTodoCount(m.todoItemsFor(m.todoDialog.ProjectPath))
		if doneCount == 0 {
			m.status = "No completed TODOs to purge"
			return m, nil
		}
		m.openTodoDonePurgeConfirm(doneCount)
		return m, nil
	case " ":
		item, ok := m.selectedTodoItem()
		if !ok {
			m.status = "No TODO selected"
			return m, nil
		}
		if m.todoDialog != nil {
			m.todoDialog.Busy = true
		}
		m.status = "Updating TODO..."
		return m, m.toggleTodoDoneCmd(item)
	case "enter":
		item, ok := m.selectedTodoItem()
		if !ok {
			m.status = "No TODO selected"
			return m, nil
		}
		return m, m.openTodoCopyDialog(item)
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
			m.reportError("TODO copy failed", err, item.ProjectPath)
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
	if copyDialog.Submitting {
		switch msg.String() {
		case "esc", "ctrl+c":
			return m, m.cancelTodoPendingLaunch("Canceling TODO start...")
		}
		return m, nil
	}
	if copyDialog.RunMode == todoCopyModeNewWorktree {
		if item, ok := m.selectedTodoItem(); ok && item.ID == copyDialog.TodoID {
			readiness, message := m.todoWorktreeLaunchReadiness(*copyDialog, item)
			if readiness == todoWorktreeLaunchWaiting {
				switch msg.String() {
				case "enter", " ":
					m.status = message
				}
				if msg.String() == "esc" || msg.String() == "ctrl+c" {
					return m, m.closeTodoCopyDialog("TODO start canceled")
				}
				return m, nil
			}
		}
	}
	switch msg.String() {
	case "esc", "ctrl+c":
		return m, m.closeTodoCopyDialog("TODO start canceled")
	case "w", "W":
		return m, m.cycleTodoCopyDialogRunMode(msg.String())
	case "a":
		return m, m.cycleTodoCopyDialogProvider(1)
	case "A":
		return m, m.cycleTodoCopyDialogProvider(-1)
	case "m":
		return m, m.toggleTodoCopyDialogOpenModelFirst()
	case "e":
		if copyDialog.RunMode != todoCopyModeNewWorktree {
			return m, nil
		}
		item, ok := m.selectedTodoItem()
		if !ok {
			m.status = "No TODO selected"
			return m, nil
		}
		return m, m.openTodoWorktreeEditor(item)
	case "r":
		if copyDialog.RunMode != todoCopyModeNewWorktree {
			return m, nil
		}
		copyDialog.BranchOverride = ""
		copyDialog.WorktreeSuffixOverride = ""
		m.status = "Refreshing worktree suggestion..."
		return m, m.regenerateTodoWorktreeSuggestionCmd(copyDialog.ProjectPath, copyDialog.TodoID)
	case "x":
		m.openTodoExistingWorktreeDialog(copyDialog.Provider)
		return m, nil
	case "enter", " ":
		return m.activateTodoCopyDialogSelection()
	}
	return m, nil
}

func (m *Model) cycleTodoCopyDialogRunMode(key string) tea.Cmd {
	copyDialog := m.todoCopyDialog
	if copyDialog == nil {
		return nil
	}
	if copyDialog.RunMode == todoCopyModeHere {
		copyDialog.RunMode = todoCopyModeNewWorktree
		if strings.TrimSpace(copyDialog.BranchOverride) != "" || strings.TrimSpace(copyDialog.WorktreeSuffixOverride) != "" {
			return nil
		}
		return m.ensureTodoWorktreeSuggestionCmd(copyDialog.ProjectPath, copyDialog.TodoID)
	}
	copyDialog.RunMode = todoCopyModeHere
	return nil
}

func (m *Model) cycleTodoCopyDialogProvider(delta int) tea.Cmd {
	copyDialog := m.todoCopyDialog
	if copyDialog == nil || delta == 0 {
		return nil
	}
	options := todoCopyDialogProviders()
	index := 0
	for i, provider := range options {
		if provider == copyDialog.Provider {
			index = i
			break
		}
	}
	index += delta
	if index < 0 {
		index = len(options) - 1
	}
	if index >= len(options) {
		index = 0
	}
	copyDialog.Provider = options[index]
	return nil
}

func (m *Model) toggleTodoCopyDialogOpenModelFirst() tea.Cmd {
	copyDialog := m.todoCopyDialog
	if copyDialog == nil {
		return nil
	}
	copyDialog.OpenModelFirst = !copyDialog.OpenModelFirst
	return nil
}

func (m *Model) activateTodoCopyDialogSelection() (tea.Model, tea.Cmd) {
	copyDialog := m.todoCopyDialog
	if copyDialog == nil {
		return m, nil
	}
	if copyDialog.RunMode == todoCopyModeNewWorktree {
		item, ok := m.selectedTodoItem()
		if !ok {
			m.status = "No TODO selected"
			return m, nil
		}
		readiness, message := m.todoWorktreeLaunchReadiness(*copyDialog, item)
		if readiness != todoWorktreeLaunchReady {
			m.status = message
			return m, nil
		}
		return m.startSelectedTodoInNewWorktree(copyDialog.Provider, copyDialog.OpenModelFirst)
	}
	m.todoCopyDialog = nil
	return m.startSelectedTodoWithProvider(copyDialog.Provider, copyDialog.OpenModelFirst)
}

func (m Model) updateTodoWorktreeEditorMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	dialog := m.todoWorktreeEditor
	if dialog == nil {
		return m, nil
	}
	switch msg.String() {
	case "esc":
		m.closeTodoWorktreeEditor("Worktree edit canceled")
		return m, nil
	case "tab", "shift+tab", "up", "down":
		if dialog.Selected == 0 {
			dialog.Selected = 1
			return m, dialog.FolderInput.Focus()
		}
		dialog.Selected = 0
		return m, dialog.BranchInput.Focus()
	case "ctrl+s", "enter":
		branchName := strings.TrimSpace(dialog.BranchInput.Value())
		folderName := strings.TrimSpace(dialog.FolderInput.Value())
		if branchName == "" || folderName == "" {
			m.status = "Branch and folder are required"
			return m, nil
		}
		if m.todoCopyDialog != nil {
			m.todoCopyDialog.BranchOverride = branchName
			m.todoCopyDialog.WorktreeSuffixOverride = folderName
		}
		m.closeTodoWorktreeEditor("Using edited worktree names")
		return m, nil
	}
	var cmd tea.Cmd
	if dialog.Selected == 0 {
		dialog.BranchInput, cmd = dialog.BranchInput.Update(msg)
	} else {
		dialog.FolderInput, cmd = dialog.FolderInput.Update(msg)
	}
	return m, cmd
}

func (m Model) updateTodoExistingWorktreeMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	dialog := m.todoExistingWorktree
	if dialog == nil {
		return m, nil
	}
	switch msg.String() {
	case "esc":
		m.closeTodoExistingWorktreeDialog("Existing worktree picker closed")
		return m, nil
	case "up", "k":
		if dialog.Selected > 0 {
			dialog.Selected--
		}
		return m, nil
	case "down", "j":
		if dialog.Selected < len(dialog.Candidates)-1 {
			dialog.Selected++
		}
		return m, nil
	case "enter":
		if dialog.Selected < 0 || dialog.Selected >= len(dialog.Candidates) {
			m.status = "No worktree selected"
			return m, nil
		}
		target := dialog.Candidates[dialog.Selected]
		return m.startTodoInProjectPath(target.Path, dialog.TodoText, dialog.Provider, dialog.OpenModelFirst)
	}
	return m, nil
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
		if strings.TrimSpace(text) == "" {
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
		if confirm.TodoID == 0 {
			m.status = "Purging completed TODOs..."
			return m, m.purgeDoneTodosCmd(confirm.ProjectPath)
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

func (m Model) purgeDoneTodosCmd(projectPath string) tea.Cmd {
	if m.svc == nil {
		return func() tea.Msg {
			return todoActionMsg{projectPath: projectPath, err: fmt.Errorf("service unavailable")}
		}
	}
	return func() tea.Msg {
		count, err := m.svc.PurgeDoneTodos(m.ctx, projectPath)
		status := "No completed TODOs to purge"
		if count == 1 {
			status = "Purged 1 completed TODO"
		} else if count > 1 {
			status = fmt.Sprintf("Purged %d completed TODOs", count)
		}
		return todoActionMsg{
			projectPath: projectPath,
			status:      status,
			err:         err,
		}
	}
}

func (m *Model) createTodoWorktreeCmd(launchCtx context.Context, launchID int64, projectPath string, todoID int64, todoText string, provider codexapp.Provider, openModelFirst bool, branchOverride, suffixOverride string) tea.Cmd {
	branchOverride = strings.TrimSpace(branchOverride)
	suffixOverride = strings.TrimSpace(suffixOverride)
	if m.svc == nil {
		return func() tea.Msg {
			return todoWorktreeLaunchMsg{
				launchID:    launchID,
				projectPath: projectPath,
				err:         fmt.Errorf("service unavailable"),
			}
		}
	}
	if launchCtx == nil {
		launchCtx = context.Background()
	}
	perfOpID := m.beginAILatencyOp("Worktree create", projectPath, provider.Label())
	return func() tea.Msg {
		startedAt := time.Now()
		result, err := m.svc.CreateTodoWorktree(launchCtx, service.CreateTodoWorktreeRequest{
			ProjectPath:    projectPath,
			TodoID:         todoID,
			BranchName:     branchOverride,
			WorktreeSuffix: suffixOverride,
		})
		if err != nil {
			return todoWorktreeLaunchMsg{
				launchID:     launchID,
				projectPath:  projectPath,
				perfOpID:     perfOpID,
				perfDuration: time.Since(startedAt),
				err:          err,
			}
		}
		return todoWorktreeLaunchMsg{
			launchID:       launchID,
			projectPath:    result.WorktreePath,
			todoText:       todoText,
			status:         "Worktree ready",
			provider:       provider,
			openModelFirst: openModelFirst,
			perfOpID:       perfOpID,
			perfDuration:   time.Since(startedAt),
		}
	}
}

func (m Model) regenerateTodoWorktreeSuggestionCmd(projectPath string, todoID int64) tea.Cmd {
	if m.svc == nil {
		return func() tea.Msg {
			return todoActionMsg{projectPath: projectPath, err: fmt.Errorf("service unavailable")}
		}
	}
	if !m.svc.HasTodoWorktreeSuggester() {
		return func() tea.Msg {
			return todoActionMsg{
				projectPath: projectPath,
				status:      "Worktree suggestions are unavailable right now. Press e to enter names manually.",
			}
		}
	}
	return func() tea.Msg {
		err := m.svc.RegenerateTodoWorktreeSuggestion(m.ctx, projectPath, todoID)
		return todoActionMsg{
			projectPath: projectPath,
			status:      "Refreshing worktree suggestion...",
			err:         err,
		}
	}
}

func (m Model) ensureTodoWorktreeSuggestionCmd(projectPath string, todoID int64) tea.Cmd {
	if m.svc == nil {
		return nil
	}
	if !m.svc.HasTodoWorktreeSuggester() {
		return func() tea.Msg {
			return todoActionMsg{
				projectPath: projectPath,
				status:      "Worktree suggestions are unavailable right now. Press e to enter names manually.",
			}
		}
	}
	return func() tea.Msg {
		changed, err := m.svc.EnsureTodoWorktreeSuggestion(m.ctx, projectPath, todoID)
		status := ""
		if changed {
			status = "Preparing worktree suggestion..."
		}
		return todoActionMsg{
			projectPath: projectPath,
			status:      status,
			err:         err,
		}
	}
}

func (m Model) startTodoInProjectPath(projectPath, todoText string, provider codexapp.Provider, openModelFirst bool) (tea.Model, tea.Cmd) {
	projectPath = strings.TrimSpace(projectPath)
	if projectPath == "" {
		m.status = "No project selected"
		return m, nil
	}
	project, ok := m.projectSummaryByPath(projectPath)
	if !ok {
		project = model.ProjectSummary{Path: projectPath}
	}
	if strings.TrimSpace(string(provider)) == "" {
		provider = m.preferredEmbeddedProviderForProject(project)
	} else {
		provider = provider.Normalized()
	}
	m.restoreCodexDraft(project.Path, codexDraft{Text: todoText})
	m.storeTodoLaunchDraft(todoLaunchDraftState{projectPath: project.Path, provider: provider, openModelFirst: openModelFirst})
	m.todoEditor = nil
	m.todoDeleteConfirm = nil
	m.todoExistingWorktree = nil
	m.todoDialog = nil
	if !project.PresentOnDisk {
		m.status = provider.Label() + " launch requires a folder present on disk"
		return m, nil
	}
	req := codexapp.LaunchRequest{
		Provider:    provider,
		ProjectPath: project.Path,
		ResumeID:    m.selectedProjectSessionID(project, provider),
		ForceNew:    true,
		Preset:      m.currentCodexLaunchPreset(),
	}
	if err := req.Validate(); err != nil {
		m.clearTodoLaunchDraft(project.Path)
		m.status = err.Error()
		return m, nil
	}
	m.ensureCodexRuntime()
	m.beginCodexPendingOpen(project.Path, provider)
	m.err = nil
	m.status = "Opening embedded " + provider.Label() + " session..."
	return m, m.openCodexSessionCmd(req)
}

func (m Model) startSelectedTodoWithProvider(provider codexapp.Provider, openModelFirst bool) (tea.Model, tea.Cmd) {
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
	m.todoCopyDialog = nil
	return m.startTodoInProjectPath(project.Path, item.Text, provider, openModelFirst)
}

func (m Model) startSelectedTodoInNewWorktree(provider codexapp.Provider, openModelFirst bool) (tea.Model, tea.Cmd) {
	item, ok := m.selectedTodoItem()
	if !ok {
		m.status = "No TODO selected"
		return m, nil
	}
	projectPath := ""
	if m.todoDialog != nil {
		projectPath = strings.TrimSpace(m.todoDialog.ProjectPath)
	}
	if projectPath == "" {
		m.status = "No project selected"
		return m, nil
	}
	if strings.TrimSpace(string(provider)) == "" {
		if project, ok := m.selectedProject(); ok {
			provider = m.preferredEmbeddedProviderForProject(project)
		} else {
			provider = codexapp.ProviderCodex
		}
	} else {
		provider = provider.Normalized()
	}
	branchOverride := ""
	suffixOverride := ""
	if copyDialog := m.todoCopyDialog; copyDialog != nil {
		branchOverride = strings.TrimSpace(copyDialog.BranchOverride)
		suffixOverride = strings.TrimSpace(copyDialog.WorktreeSuffixOverride)
	}
	launchID, launchCtx := m.beginTodoPendingLaunch()
	m.todoEditor = nil
	m.todoDeleteConfirm = nil
	m.todoWorktreeEditor = nil
	m.todoExistingWorktree = nil
	m.todoCopyDialog = nil
	m.todoDialog = nil
	m.status = "Starting TODO in dedicated worktree..."
	return m, m.createTodoWorktreeCmd(launchCtx, launchID, projectPath, item.ID, item.Text, provider, openModelFirst, branchOverride, suffixOverride)
}

func (m *Model) returnToTodoFromModelPicker() {
	ret := m.todoModelPickerReturn
	if ret == nil {
		return
	}
	m.todoModelPickerReturn = nil
	m.todoDialog = &ret.dialog
	m.todoCopyDialog = &ret.copyDialog
	m.codexVisibleProject = ret.prevVisibleProject
}

func (m Model) embeddedModelLabelForProject(projectPath string, provider codexapp.Provider) string {
	if pref, ok := m.embeddedModelPreference(provider); ok && pref.Model != "" {
		label := pref.Model
		if pref.Reasoning != "" {
			label += ", " + pref.Reasoning
		}
		return label
	}
	if snapshot, ok := m.liveEmbeddedSnapshotForProject(projectPath, provider); ok {
		model := firstNonEmptyTrimmed(snapshot.PendingModel, snapshot.Model)
		reasoning := firstNonEmptyTrimmed(snapshot.PendingReasoning, snapshot.ReasoningEffort)
		if model != "" {
			label := model
			if reasoning != "" {
				label += ", " + reasoning
			}
			return label
		}
	}
	return "default"
}

func (m *Model) syncTodoDialogSize() {
	if m.todoDialog != nil {
		m.syncTodoDialogSelection()
	}
}

func (m Model) todoDialogListHeight() int {
	_, _, listH := todoDialogPanelLayout(m.width, m.height)
	return max(1, listH)
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
			line := m.todoDialogItemLine(item, prefix, max(12, panelInnerW-2))
			if i == dialog.Selected {
				line = dialogButtonSelectedStyle.UnsetPadding().Width(panelInnerW).Render(line)
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
	return strings.Join([]string{
		renderHelpPanelActionRow(
			renderDialogAction("a", "add", commitActionKeyStyle, commitActionTextStyle),
			renderDialogAction("e", "edit", navigateActionKeyStyle, navigateActionTextStyle),
			renderDialogAction("space", "done", pushActionKeyStyle, pushActionTextStyle),
			renderDialogAction("d", "delete", cancelActionKeyStyle, cancelActionTextStyle),
			renderDialogAction("p", "purge done", cancelActionKeyStyle, cancelActionTextStyle),
			renderDialogAction("c", "copy", navigateActionKeyStyle, navigateActionTextStyle),
		),
		renderHelpPanelActionRow(
			renderDialogAction("Enter", "start", commitActionKeyStyle, commitActionTextStyle),
			renderDialogAction("Esc", "close", cancelActionKeyStyle, cancelActionTextStyle),
		),
	}, "\n")
}

func (m Model) todoDialogItemLine(item model.TodoItem, prefix string, width int) string {
	base := prefix + " " + todoPreviewText(item.Text)
	label, labelStyle := m.todoWorktreeSuggestionLabel(item)
	if label == "" {
		return truncateText(base, width)
	}
	suffixPlain := " · " + label
	suffixStyled := labelStyle.Render(suffixPlain)
	if width <= 0 {
		return base + suffixStyled
	}
	baseWidth := ansi.StringWidth(base)
	suffixWidth := ansi.StringWidth(suffixPlain)
	if baseWidth+suffixWidth <= width {
		return base + suffixStyled
	}
	minBaseWidth := max(12, width/2)
	if baseWidth > minBaseWidth {
		base = truncateText(base, minBaseWidth)
	}
	remaining := width - ansi.StringWidth(base)
	if remaining <= 3 {
		return truncateText(base, width)
	}
	return base + labelStyle.Render(ansi.Truncate(suffixPlain, remaining, ""))
}

func (m Model) todoWorktreeSuggestionLabel(item model.TodoItem) (string, lipgloss.Style) {
	if item.Done {
		return "", lipgloss.Style{}
	}
	if linked, ok := m.todoLinkedWorktreeProject(item.ID); ok {
		label := strings.TrimSpace(projectWorktreeLabel(linked))
		if label == "" {
			label = todoWorktreeSuggestionBranch(item.WorktreeSuggestion)
		}
		if label == "" {
			label = "worktree"
		}
		if linked.PresentOnDisk {
			return label, statusStyle(model.StatusActive)
		}
		return label, detailWarningStyle
	}
	if label := todoWorktreeSuggestionBranch(item.WorktreeSuggestion); label != "" {
		return label, detailMutedStyle
	}
	return "", lipgloss.Style{}
}

func todoWorktreeSuggestionBranch(suggestion *model.TodoWorktreeSuggestion) string {
	if suggestion == nil {
		return ""
	}
	switch suggestion.Status {
	case model.TodoWorktreeSuggestionReady:
		if strings.TrimSpace(suggestion.BranchName) != "" {
			return strings.TrimSpace(suggestion.BranchName)
		}
		return "suggestion ready"
	default:
		return ""
	}
}

func (m Model) todoLinkedWorktreeProject(todoID int64) (model.ProjectSummary, bool) {
	if todoID <= 0 {
		return model.ProjectSummary{}, false
	}
	best, ok := todoLinkedWorktreeProjectIn(m.allProjects, todoID)
	if ok {
		return best, true
	}
	return todoLinkedWorktreeProjectIn(m.projects, todoID)
}

func todoLinkedWorktreeProjectIn(projects []model.ProjectSummary, todoID int64) (model.ProjectSummary, bool) {
	best := model.ProjectSummary{}
	bestRank := -1
	for _, project := range projects {
		if project.WorktreeKind != model.WorktreeKindLinked || project.WorktreeOriginTodoID != todoID {
			continue
		}
		rank := 0
		if project.PresentOnDisk {
			rank += 2
		}
		if strings.TrimSpace(project.RepoBranch) != "" {
			rank++
		}
		if rank > bestRank || (rank == bestRank && strings.TrimSpace(project.Path) < strings.TrimSpace(best.Path)) {
			best = project
			bestRank = rank
		}
	}
	if bestRank < 0 {
		return model.ProjectSummary{}, false
	}
	return best, true
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
	title := "Delete TODO"
	message := detailValueStyle.Render(truncateText(strings.TrimSpace(confirm.TodoText), panelInnerW))
	actionLabel := "Delete"
	if confirm.TodoID == 0 {
		title = "Purge Completed TODOs"
		actionLabel = "Purge"
		noun := "TODOs"
		if confirm.DoneCount == 1 {
			noun = "TODO"
		}
		message = strings.Join([]string{
			detailValueStyle.Render(fmt.Sprintf("Delete %d completed %s from this project?", confirm.DoneCount, noun)),
			detailMutedStyle.Render("Only satisfied TODOs will be removed."),
		}, "\n")
	}
	buttons := lipgloss.JoinHorizontal(
		lipgloss.Left,
		renderDialogButton(actionLabel, confirm.Selected == todoDeleteConfirmFocusDelete),
		" ",
		renderDialogButton("Keep", confirm.Selected == todoDeleteConfirmFocusKeep),
	)
	lines := []string{
		detailSectionStyle.Render(title) + "  " + detailValueStyle.Render(confirm.ProjectName),
		"",
		message,
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
	panelW := min(bodyW, min(max(84, bodyW-6), 116))
	panelInnerW := max(24, panelW-4)
	lines := []string{
		renderDialogHeader("Start TODO", copyDialog.ProjectName, "", panelInnerW),
		detailValueStyle.Render(truncateText(strings.TrimSpace(copyDialog.TodoText), panelInnerW)),
		"",
	}
	projectPath := copyDialog.ProjectPath
	runButtons := make([]string, 0, 3)
	for _, mode := range []int{todoCopyModeHere, todoCopyModeNewWorktree} {
		runButtons = append(runButtons, renderDialogButton(todoCopyRunModeLabel(mode), copyDialog.RunMode == mode))
	}
	candidates := m.existingWorktreeCandidates(copyDialog.ProjectPath)
	if len(candidates) > 0 {
		runButtons = append(runButtons, detailMutedStyle.Render(fmt.Sprintf("x for %d existing worktree(s)", len(candidates))))
	}
	providerButtons := make([]string, 0, 4)
	for _, provider := range todoCopyDialogProviders() {
		label := provider.Label() + "  (" + m.embeddedModelLabelForProject(projectPath, provider) + ")"
		providerButtons = append(providerButtons, renderDialogButton(label, copyDialog.Provider == provider))
	}
	optionButtons := []string{m.renderTodoCopyModelToggle(copyDialog.OpenModelFirst)}
	lines = append(lines, m.renderTodoCopyChooserColumns(panelInnerW, runButtons, providerButtons, optionButtons))
	if copyDialog.RunMode == todoCopyModeNewWorktree {
		lines = append(lines, "")
		if item, ok := m.selectedTodoItem(); ok && item.ID == copyDialog.TodoID {
			lines = append(lines, m.todoWorktreeLaunchDetails(*copyDialog, item, panelInnerW)...)
		}
	}
	enterAction := renderDialogAction("Enter", "start", commitActionKeyStyle, commitActionTextStyle)
	waitingOnly := false
	if copyDialog.Submitting {
		enterAction = renderDialogAction("Enter", todoDialogWaitingLabel(m.spinnerFrame), disabledActionKeyStyle, disabledActionTextStyle)
	} else if copyDialog.RunMode == todoCopyModeNewWorktree {
		if item, ok := m.selectedTodoItem(); ok && item.ID == copyDialog.TodoID {
			readiness, _ := m.todoWorktreeLaunchReadiness(*copyDialog, item)
			switch readiness {
			case todoWorktreeLaunchWaiting:
				enterAction = renderDialogAction("Enter", todoDialogWaitingLabel(m.spinnerFrame), disabledActionKeyStyle, disabledActionTextStyle)
				waitingOnly = true
			case todoWorktreeLaunchUnavailable:
				enterAction = renderDialogAction("Enter", "unavailable", disabledActionKeyStyle, disabledActionTextStyle)
			}
		}
	}
	primaryActions := make([]string, 0, 6)
	if !waitingOnly {
		primaryActions = append(primaryActions,
			renderDialogAction("w", "toggle worktree", navigateActionKeyStyle, navigateActionTextStyle),
			renderDialogAction("a", "cycle agent", navigateActionKeyStyle, navigateActionTextStyle),
			renderDialogAction("m", "change model", pushActionKeyStyle, pushActionTextStyle),
		)
		if len(candidates) > 0 {
			primaryActions = append(primaryActions, renderDialogAction("x", "existing", navigateActionKeyStyle, navigateActionTextStyle))
		}
		if copyDialog.RunMode == todoCopyModeNewWorktree {
			primaryActions = append(primaryActions,
				renderDialogAction("e", "edit", navigateActionKeyStyle, navigateActionTextStyle),
				renderDialogAction("r", "refresh", pushActionKeyStyle, pushActionTextStyle),
			)
		}
	}
	actionLines := []string{}
	if row := renderHelpPanelActionRow(primaryActions...); strings.TrimSpace(row) != "" {
		actionLines = append(actionLines, row)
	}
	actionLines = append(actionLines, renderHelpPanelActionRow(
		enterAction,
		renderDialogAction("Esc", "cancel", cancelActionKeyStyle, cancelActionTextStyle),
	))
	lines = append(lines, strings.Join(actionLines, "\n"))
	panel := renderDialogPanel(panelW, panelInnerW, strings.Join(lines, "\n"))
	left := max(0, (bodyW-panelW)/2)
	top := max(0, (bodyH-lipgloss.Height(panel))/3)
	return overlayBlock(body, panel, bodyW, bodyH, left, top)
}

func (m Model) renderTodoWorktreeEditorOverlay(body string, bodyW, bodyH int) string {
	dialog := m.todoWorktreeEditor
	if dialog == nil {
		return body
	}
	panelW := min(max(64, bodyW-16), 96)
	panelInnerW := max(24, panelW-4)
	dialog.BranchInput.Width = max(20, panelInnerW-10)
	dialog.FolderInput.Width = max(20, panelInnerW-10)
	lines := []string{
		renderDialogHeader("Worktree names", dialog.ProjectName, "", panelInnerW),
		"",
		m.renderTodoWorktreeEditorInput("Branch", dialog.Selected == 0, panelInnerW, dialog.BranchInput),
		m.renderTodoWorktreeEditorInput("Folder", dialog.Selected == 1, panelInnerW, dialog.FolderInput),
		"",
		renderHelpPanelActionRow(
			renderDialogAction("Tab/↑↓", "switch", navigateActionKeyStyle, navigateActionTextStyle),
			renderDialogAction("Ctrl+S", "save", commitActionKeyStyle, commitActionTextStyle),
			renderDialogAction("Esc", "cancel", cancelActionKeyStyle, cancelActionTextStyle),
		),
	}
	panel := renderDialogPanel(panelW, panelInnerW, strings.Join(lines, "\n"))
	left := max(0, (bodyW-panelW)/2)
	top := max(0, (bodyH-lipgloss.Height(panel))/2)
	return overlayBlock(body, panel, bodyW, bodyH, left, top)
}

func (m Model) renderTodoWorktreeEditorInput(label string, selected bool, width int, input textinput.Model) string {
	labelWidth := 8
	valueWidth := max(12, width-labelWidth-1)
	rendered := input.View()
	if lipgloss.Width(rendered) > valueWidth {
		rendered = fitStyledWidth(rendered, valueWidth)
	}
	line := fmt.Sprintf("%-*s %s", labelWidth, label+":", rendered)
	if selected {
		return dialogButtonSelectedStyle.UnsetPadding().Width(width).Render(line)
	}
	return detailValueStyle.Render(line)
}

func (m Model) renderTodoExistingWorktreeOverlay(body string, bodyW, bodyH int) string {
	dialog := m.todoExistingWorktree
	if dialog == nil {
		return body
	}
	panelW := min(max(68, bodyW-18), 100)
	panelInnerW := max(24, panelW-4)
	lines := []string{
		renderDialogHeader("Existing worktree", dialog.ProjectName, "", panelInnerW),
		detailValueStyle.Render(truncateText(strings.TrimSpace(dialog.TodoText), panelInnerW)),
		detailField("Choices", detailValueStyle.Render(fmt.Sprintf("%d sibling worktrees", len(dialog.Candidates)))),
		"",
	}
	for i, candidate := range dialog.Candidates {
		label := projectWorktreeLabel(candidate)
		pathLabel := filepath.Base(strings.TrimSpace(candidate.Path))
		details := []string{}
		if candidate.RepoDirty {
			details = append(details, "dirty")
		} else {
			details = append(details, "clean")
		}
		if snapshot := m.projectRuntimeSnapshot(candidate.Path); snapshot.Running {
			details = append(details, "runtime")
		}
		if m.projectHasLiveCodexSession(candidate.Path) {
			details = append(details, "agent")
		}
		buttonLabel := label
		if pathLabel != "" && pathLabel != "." && pathLabel != label {
			buttonLabel += "  [" + pathLabel + "]"
		}
		if len(details) > 0 {
			buttonLabel += "  (" + strings.Join(details, ", ") + ")"
		}
		button := renderDialogButton(buttonLabel, dialog.Selected == i)
		lines = append(lines, button)
	}
	lines = append(lines, "")
	lines = append(lines, renderHelpPanelActionRow(
		renderDialogAction("↑↓", "switch", navigateActionKeyStyle, navigateActionTextStyle),
		renderDialogAction("Enter", "start", commitActionKeyStyle, commitActionTextStyle),
		renderDialogAction("Esc", "back", cancelActionKeyStyle, cancelActionTextStyle),
	))
	panel := renderDialogPanel(panelW, panelInnerW, strings.Join(lines, "\n"))
	left := max(0, (bodyW-panelW)/2)
	top := max(0, (bodyH-lipgloss.Height(panel))/3)
	return overlayBlock(body, panel, bodyW, bodyH, left, top)
}

func (m Model) renderTodoCopySectionHeader(title, hotkey string) string {
	line := detailSectionStyle.Render(title)
	if hotkey != "" {
		line += "  " + detailMutedStyle.Render("["+hotkey+"]")
	}
	return line
}

func (m Model) renderTodoCopyModelToggle(enabled bool) string {
	value := "[ ] change model"
	if enabled {
		value = "[x] change model"
	}
	return detailValueStyle.Render(value + "  (m)")
}

func (m Model) renderTodoCopyChooserColumns(width int, runButtons, providerButtons, optionButtons []string) string {
	gap := 2
	columnCount := 3
	columnWidth := max(18, (width-gap*(columnCount-1))/columnCount)
	lastWidth := max(18, width-columnWidth*(columnCount-1)-gap*(columnCount-1))

	columns := []struct {
		width int
		lines []string
	}{
		{width: columnWidth, lines: append([]string{m.renderTodoCopySectionHeader("Run in", "w")}, runButtons...)},
		{width: columnWidth, lines: append([]string{m.renderTodoCopySectionHeader("Agent", "a")}, providerButtons...)},
		{width: lastWidth, lines: append([]string{m.renderTodoCopySectionHeader("Options", "")}, optionButtons...)},
	}

	height := 0
	for _, column := range columns {
		height = max(height, len(column.lines))
	}

	parts := make([]string, 0, len(columns)*2-1)
	for idx, column := range columns {
		lines := append([]string(nil), column.lines...)
		for len(lines) < height {
			lines = append(lines, "")
		}
		rendered := make([]string, 0, len(lines))
		for _, line := range lines {
			rendered = append(rendered, fitStyledWidth(line, column.width))
		}
		parts = append(parts, lipgloss.NewStyle().Width(column.width).Render(strings.Join(rendered, "\n")))
		if idx < len(columns)-1 {
			parts = append(parts, strings.Repeat(" ", gap))
		}
	}

	return lipgloss.JoinHorizontal(lipgloss.Top, parts...)
}

func todoCopyDialogProviders() []codexapp.Provider {
	return []codexapp.Provider{
		codexapp.ProviderCodex,
		codexapp.ProviderOpenCode,
		codexapp.ProviderClaudeCode,
	}
}

func todoCopyRunModeLabel(mode int) string {
	switch mode {
	case todoCopyModeNewWorktree:
		return "Dedicated worktree"
	default:
		return "Here"
	}
}

type todoWorktreeLaunchState int

const (
	todoWorktreeLaunchReady todoWorktreeLaunchState = iota
	todoWorktreeLaunchWaiting
	todoWorktreeLaunchUnavailable
)

func (m Model) todoWorktreeLaunchReadiness(dialog todoCopyDialogState, item model.TodoItem) (todoWorktreeLaunchState, string) {
	branchOverride := strings.TrimSpace(dialog.BranchOverride)
	folderOverride := strings.TrimSpace(dialog.WorktreeSuffixOverride)
	switch {
	case branchOverride != "" && folderOverride != "":
		return todoWorktreeLaunchReady, ""
	case branchOverride != "" || folderOverride != "":
		return todoWorktreeLaunchUnavailable, "Branch and folder are required. Press e to finish entering names."
	}

	suggestion := item.WorktreeSuggestion
	if suggestion == nil {
		return todoWorktreeLaunchReady, "Worktree name will be generated automatically."
	}

	switch suggestion.Status {
	case model.TodoWorktreeSuggestionReady:
		if strings.TrimSpace(suggestion.BranchName) != "" && strings.TrimSpace(suggestion.WorktreeSuffix) != "" {
			return todoWorktreeLaunchReady, ""
		}
		return todoWorktreeLaunchReady, "Worktree name will be generated automatically."
	case model.TodoWorktreeSuggestionQueued, model.TodoWorktreeSuggestionRunning:
		return todoWorktreeLaunchReady, "Suggested names are still generating; launch will continue with an automatic name."
	case model.TodoWorktreeSuggestionFailed:
		return todoWorktreeLaunchReady, "Worktree suggestion is unavailable right now; launch will continue with an automatic name."
	default:
		return todoWorktreeLaunchReady, "Worktree name will be generated automatically."
	}
}

func todoDialogWaitingLabel(frame int) string {
	switch frame % 3 {
	case 1:
		return "wait."
	case 2:
		return "wait.."
	default:
		return "wait..."
	}
}

func (m Model) todoWorktreeLaunchDetails(dialog todoCopyDialogState, item model.TodoItem, width int) []string {
	suggestion := item.WorktreeSuggestion
	branchName := strings.TrimSpace(dialog.BranchOverride)
	folderName := strings.TrimSpace(dialog.WorktreeSuffixOverride)
	if branchName != "" || folderName != "" {
		lines := []string{
			detailField("Branch", detailValueStyle.Render(branchName)),
			detailField("Folder", detailValueStyle.Render(truncateText(folderName, width))),
			detailField("Source", detailValueStyle.Render("edited for this launch")),
		}
		if projectPath := todoSuggestedWorktreePath(dialog.ProjectPath, folderName); strings.TrimSpace(projectPath) != "" {
			lines = append(lines, detailField("Path", detailMutedStyle.Render(truncateText(projectPath, width))))
		}
		return lines
	}
	if suggestion == nil {
		readiness, message := m.todoWorktreeLaunchReadiness(dialog, item)
		if readiness == todoWorktreeLaunchUnavailable {
			return []string{
				detailMutedStyle.Render(message),
				detailMutedStyle.Render("Press e to enter names now."),
			}
		}
		return []string{
			detailMutedStyle.Render(message),
			detailMutedStyle.Render("Press Enter to launch now, or e to set names manually."),
		}
	}
	switch suggestion.Status {
	case model.TodoWorktreeSuggestionReady:
		lines := []string{
			detailField("Branch", detailValueStyle.Render(strings.TrimSpace(suggestion.BranchName))),
			detailField("Source", detailValueStyle.Render("cached AI suggestion")),
		}
		folder := filepath.Base(todoSuggestedWorktreePath(dialog.ProjectPath, suggestion.WorktreeSuffix))
		if strings.TrimSpace(folder) != "" {
			lines = append(lines, detailField("Folder", detailValueStyle.Render(truncateText(folder, width))))
		}
		if projectPath := todoSuggestedWorktreePath(dialog.ProjectPath, suggestion.WorktreeSuffix); strings.TrimSpace(projectPath) != "" {
			lines = append(lines, detailField("Path", detailMutedStyle.Render(truncateText(projectPath, width))))
		}
		return lines
	case model.TodoWorktreeSuggestionQueued, model.TodoWorktreeSuggestionRunning:
		return []string{
			detailMutedStyle.Render("Worktree suggestion is still preparing in the background."),
			detailMutedStyle.Render("Press Enter to launch now with an automatic name, or wait for the preview."),
		}
	case model.TodoWorktreeSuggestionFailed:
		return []string{
			detailWarningStyle.Render("Worktree suggestion is unavailable right now."),
			detailMutedStyle.Render("Press Enter to launch with an automatic name, or e to enter names now."),
		}
	default:
		return []string{
			detailMutedStyle.Render("Worktree suggestion is not ready yet."),
			detailMutedStyle.Render("Press Enter to launch with an automatic name, or wait for the preview."),
		}
	}
}

func todoSuggestedWorktreePath(projectPath, worktreeSuffix string) string {
	projectPath = filepath.Clean(strings.TrimSpace(projectPath))
	worktreeSuffix = strings.TrimSpace(worktreeSuffix)
	base := filepath.Base(projectPath)
	if base == "" || base == "." || base == string(filepath.Separator) {
		base = "worktree"
	}
	return filepath.Join(filepath.Dir(projectPath), base+"--"+worktreeSuffix)
}
