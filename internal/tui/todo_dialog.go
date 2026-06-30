package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"lcroom/internal/codexapp"
	"lcroom/internal/config"
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
const todoWorktreePreparingStatus = "Creating dedicated worktree; hydrating submodules if needed..."

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
	Attachments []model.TodoAttachment
	Submitting  bool
}

type todoPendingSaveState struct {
	ProjectPath string
	ProjectName string
	TodoID      int64
	Text        string
	Attachments []model.TodoAttachment
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
	todoID         int64
	provider       codexapp.Provider
	openModelFirst bool
	autoSubmit     bool
	attachments    []codexapp.Attachment
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
	draft.attachments = cloneCodexAttachments(draft.attachments)
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
	ID          int64
	Cancel      context.CancelFunc
	Canceled    bool
	ProjectPath string
	ProjectName string
	TodoID      int64
	TodoText    string
	Provider    codexapp.Provider
	StartedAt   time.Time
}

const (
	todoPendingLaunchDialogFocusOK = iota
	todoPendingLaunchDialogFocusAbort
)

type todoPendingLaunchDialogState struct {
	LaunchID   int64
	Message    string
	AllowAbort bool
	Selected   int
}

type todoCopyDialogState struct {
	ProjectPath            string
	ProjectName            string
	TodoID                 int64
	TodoText               string
	Attachments            []model.TodoAttachment
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
	TodoID         int64
	TodoText       string
	Attachments    []model.TodoAttachment
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

func cloneTodoAttachments(in []model.TodoAttachment) []model.TodoAttachment {
	if len(in) == 0 {
		return nil
	}
	out := make([]model.TodoAttachment, 0, len(in))
	for _, attachment := range in {
		if strings.TrimSpace(attachment.Path) == "" {
			continue
		}
		attachment.Position = len(out)
		out = append(out, attachment)
	}
	return out
}

func todoAttachmentsFromCodex(in []codexapp.Attachment) []model.TodoAttachment {
	if len(in) == 0 {
		return nil
	}
	out := make([]model.TodoAttachment, 0, len(in))
	for _, attachment := range in {
		if strings.TrimSpace(attachment.Path) == "" {
			continue
		}
		kind := model.TodoAttachmentKind(strings.TrimSpace(string(attachment.Kind)))
		if kind == "" {
			kind = model.TodoAttachmentLocalImage
		}
		out = append(out, model.TodoAttachment{
			Kind:     kind,
			Path:     strings.TrimSpace(attachment.Path),
			Position: len(out),
		})
	}
	return out
}

func codexAttachmentsFromTodo(in []model.TodoAttachment) []codexapp.Attachment {
	if len(in) == 0 {
		return nil
	}
	out := make([]codexapp.Attachment, 0, len(in))
	for _, attachment := range in {
		if strings.TrimSpace(attachment.Path) == "" {
			continue
		}
		kind := codexapp.AttachmentKind(strings.TrimSpace(string(attachment.Kind)))
		if kind == "" {
			kind = codexapp.AttachmentLocalImage
		}
		out = append(out, codexapp.Attachment{
			Kind: kind,
			Path: strings.TrimSpace(attachment.Path),
		})
	}
	return out
}

func codexDraftFromTodo(text string, attachments []model.TodoAttachment) codexDraft {
	codexAttachments := codexAttachmentsFromTodo(attachments)
	draftText := text
	if len(codexAttachments) > 0 {
		tokens := make([]string, 0, len(codexAttachments))
		for i, attachment := range codexAttachments {
			tokens = append(tokens, codexAttachmentComposerToken(i, attachment))
		}
		tokenText := strings.Join(tokens, " ") + " "
		if strings.TrimSpace(draftText) != "" {
			draftText = strings.TrimRight(draftText, " \t\r\n") + "\n\n" + tokenText
		} else {
			draftText = tokenText
		}
	}
	return codexDraft{
		Text:        draftText,
		Attachments: codexAttachments,
	}
}

func todoAttachmentLabel(index int, attachment model.TodoAttachment) string {
	kind := codexapp.AttachmentKind(strings.TrimSpace(string(attachment.Kind)))
	if kind == "" {
		kind = codexapp.AttachmentLocalImage
	}
	return codexAttachmentComposerToken(index, codexapp.Attachment{
		Kind: kind,
		Path: attachment.Path,
	})
}

func todoAttachmentSummary(attachments []model.TodoAttachment) string {
	count := len(cloneTodoAttachments(attachments))
	switch count {
	case 0:
		return ""
	case 1:
		return "1 image"
	default:
		return fmt.Sprintf("%d images", count)
	}
}

func providerSupportsTodoAttachments(provider codexapp.Provider) bool {
	switch provider.Normalized() {
	case codexapp.ProviderCodex, codexapp.ProviderOpenCode:
		return true
	default:
		return false
	}
}

func todoAttachmentUnsupportedStatus(provider codexapp.Provider) string {
	return provider.Label() + " does not support TODO image attachments yet. Choose Codex or OpenCode, or remove the images."
}

func (m *Model) openTodoDialogForSelection() tea.Cmd {
	if m.todoPendingSave != nil {
		m.status = "TODO save already in progress"
		return nil
	}
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
	return m.openTodoDialog(project)
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
	return m.requestProjectDetailViewCmd(project.Path)
}

func (m Model) todoItemsFor(projectPath string) []model.TodoItem {
	projectPath = normalizeProjectPath(projectPath)
	if projectPath == "" {
		return nil
	}
	if normalizeProjectPath(m.detail.Summary.Path) == projectPath {
		return append([]model.TodoItem(nil), m.detail.Todos...)
	}
	return nil
}

func (m Model) todoDialogDetailPending(projectPath string) bool {
	projectPath = normalizeProjectPath(projectPath)
	if projectPath == "" {
		return false
	}
	if m.detailReloadInFlight[projectPath] || m.detailReloadQueued[projectPath] {
		return true
	}
	if m.detailReloadError(projectPath) != "" {
		return false
	}
	return normalizeProjectPath(m.detail.Summary.Path) != projectPath
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

func (m *Model) closeTodoDialog(status string) tea.Cmd {
	closedPath := ""
	if m.todoDialog != nil {
		closedPath = normalizeProjectPath(m.todoDialog.ProjectPath)
	}
	m.todoDialog = nil
	if status != "" {
		m.status = status
	}
	selectedPath := m.currentSelectedProjectPath()
	if selectedPath != "" && selectedPath != closedPath {
		return m.requestProjectDetailViewCmd(selectedPath)
	}
	return nil
}

func (m *Model) openTodoEditor(todoID int64, value string, attachments []model.TodoAttachment) tea.Cmd {
	if m.todoDialog == nil {
		return nil
	}
	m.todoEditor = &todoEditorState{
		ProjectPath: m.todoDialog.ProjectPath,
		ProjectName: m.todoDialog.ProjectName,
		TodoID:      todoID,
		Input:       newTodoTextInput(value),
		Attachments: cloneTodoAttachments(attachments),
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

func (m *Model) startTodoEditorSave(text string) tea.Cmd {
	dialog := m.todoEditor
	if dialog == nil {
		return nil
	}
	m.todoPendingSave = &todoPendingSaveState{
		ProjectPath: dialog.ProjectPath,
		ProjectName: dialog.ProjectName,
		TodoID:      dialog.TodoID,
		Text:        text,
		Attachments: cloneTodoAttachments(dialog.Attachments),
	}
	if m.todoDialog != nil && filepath.Clean(strings.TrimSpace(m.todoDialog.ProjectPath)) == filepath.Clean(strings.TrimSpace(dialog.ProjectPath)) {
		m.todoDialog.Busy = true
	}
	m.closeTodoEditor("")
	if dialog.TodoID > 0 {
		m.status = "Saving TODO..."
		return m.updateTodoCmd(dialog.ProjectPath, dialog.TodoID, text, dialog.Attachments)
	}
	m.status = "Adding TODO..."
	return m.addTodoCmd(dialog.ProjectPath, text, dialog.Attachments)
}

func (m *Model) reopenPendingTodoEditor() tea.Cmd {
	pending := m.todoPendingSave
	if pending == nil {
		return nil
	}
	m.todoPendingSave = nil
	m.todoEditor = &todoEditorState{
		ProjectPath: pending.ProjectPath,
		ProjectName: pending.ProjectName,
		TodoID:      pending.TodoID,
		Input:       newTodoTextInput(pending.Text),
		Attachments: cloneTodoAttachments(pending.Attachments),
	}
	return m.todoEditor.Input.Focus()
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
		Attachments: cloneTodoAttachments(todo.Attachments),
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

func (m *Model) beginTodoPendingLaunch(projectPath, projectName string, todoID int64, todoText string, provider codexapp.Provider) (int64, context.Context) {
	m.todoLaunchSeq++
	parent := m.ctx
	if parent == nil {
		parent = context.Background()
	}
	launchCtx, cancel := context.WithCancel(parent)
	m.todoPendingLaunch = &todoPendingLaunchState{
		ID:          m.todoLaunchSeq,
		Cancel:      cancel,
		ProjectPath: strings.TrimSpace(projectPath),
		ProjectName: strings.TrimSpace(projectName),
		TodoID:      todoID,
		TodoText:    strings.TrimSpace(todoText),
		Provider:    provider.Normalized(),
		StartedAt:   m.currentTime(),
	}
	if m.todoCopyDialog != nil {
		m.todoCopyDialog.LaunchID = m.todoLaunchSeq
		m.todoCopyDialog.Submitting = true
	}
	return m.todoLaunchSeq, launchCtx
}

func (m *Model) cancelTodoPendingLaunch(status string) tea.Cmd {
	selectedPath := m.currentSelectedProjectPath()
	selectedPending := false
	if pending := m.todoPendingLaunch; pending != nil {
		_, selectedPending = m.todoPendingLaunchForProjectPath(selectedPath)
		pending.Canceled = true
		if pending.Cancel != nil {
			pending.Cancel()
			pending.Cancel = nil
		}
		if selectedPending {
			selectedPath = pending.ProjectPath
		}
	}
	m.todoPendingLaunchDialog = nil
	m.rebuildProjectList(selectedPath)
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
		TodoID:         m.todoCopyDialog.TodoID,
		TodoText:       m.todoCopyDialog.TodoText,
		Attachments:    cloneTodoAttachments(m.todoCopyDialog.Attachments),
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
		return m, m.closeTodoDialog("TODO list closed")
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
		return m, m.openTodoEditor(0, "", nil)
	case "e":
		item, ok := m.selectedTodoItem()
		if !ok {
			m.status = "No TODO selected"
			return m, nil
		}
		return m, m.openTodoEditor(item.ID, item.Text, item.Attachments)
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
		if updated, cmd, handled := m.openPinnedTodoWorkSession(item); handled {
			return updated, cmd
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
		return m.startTodoInProjectPath(target.Path, dialog.TodoID, dialog.TodoText, dialog.Attachments, dialog.Provider, dialog.OpenModelFirst)
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
		return m, m.startTodoEditorSave(text)
	case "ctrl+v":
		attached, err := m.tryAttachTodoClipboardImage()
		if err != nil {
			m.status = err.Error()
			return m, nil
		}
		if attached {
			return m, nil
		}
		text, err := clipboardTextReader()
		if err != nil {
			m.reportError("Clipboard paste failed", err, dialog.ProjectPath)
			return m, nil
		}
		if text != "" {
			dialog.Input.InsertString(text)
		}
		return m, nil
	case "backspace", "delete":
		if strings.TrimSpace(dialog.Input.Value()) == "" && len(dialog.Attachments) > 0 {
			removed := todoAttachmentLabel(len(dialog.Attachments)-1, dialog.Attachments[len(dialog.Attachments)-1])
			dialog.Attachments = dialog.Attachments[:len(dialog.Attachments)-1]
			m.status = "Removed " + removed + " from TODO"
			return m, nil
		}
	}
	var cmd tea.Cmd
	dialog.Input, cmd = dialog.Input.Update(msg)
	return m, cmd
}

func (m *Model) tryAttachTodoClipboardImage() (bool, error) {
	dialog := m.todoEditor
	if dialog == nil {
		return false, nil
	}
	attachment, err := durableClipboardImageAttachment(m.appDataDir())
	if err != nil {
		if err == errClipboardHasNoImage {
			return false, nil
		}
		return false, err
	}
	todoAttachments := todoAttachmentsFromCodex([]codexapp.Attachment{attachment})
	if len(todoAttachments) == 0 {
		return false, nil
	}
	dialog.Attachments = append(cloneTodoAttachments(dialog.Attachments), todoAttachments[0])
	index := len(dialog.Attachments) - 1
	m.status = "Attached " + todoAttachmentLabel(index, dialog.Attachments[index]) + " to TODO"
	return true, nil
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

func (m Model) addTodoCmd(projectPath, text string, attachments []model.TodoAttachment) tea.Cmd {
	if m.svc == nil {
		return func() tea.Msg {
			return todoActionMsg{projectPath: projectPath, err: fmt.Errorf("service unavailable")}
		}
	}
	attachments = cloneTodoAttachments(attachments)
	return func() tea.Msg {
		ctx, cancel := m.actionContext(tuiQuickActionTimeout)
		defer cancel()
		_, err := m.svc.AddTodoWithAttachments(ctx, projectPath, text, attachments)
		err = timeoutActionError(err, tuiQuickActionTimeout, "adding the TODO")
		return todoActionMsg{
			projectPath: projectPath,
			status:      "TODO added",
			err:         err,
		}
	}
}

func (m Model) updateTodoCmd(projectPath string, todoID int64, text string, attachments []model.TodoAttachment) tea.Cmd {
	if m.svc == nil {
		return func() tea.Msg {
			return todoActionMsg{projectPath: projectPath, err: fmt.Errorf("service unavailable")}
		}
	}
	attachments = cloneTodoAttachments(attachments)
	return func() tea.Msg {
		ctx, cancel := m.actionContext(tuiQuickActionTimeout)
		defer cancel()
		err := m.svc.UpdateTodoWithAttachments(ctx, projectPath, todoID, text, attachments)
		err = timeoutActionError(err, tuiQuickActionTimeout, "saving the TODO")
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
		ctx, cancel := m.actionContext(tuiQuickActionTimeout)
		defer cancel()
		err := m.svc.ToggleTodoDone(ctx, item.ProjectPath, item.ID, !item.Done)
		err = timeoutActionError(err, tuiQuickActionTimeout, "updating the TODO")
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

func (m Model) markTodoWorkStartedCmd(projectPath string, todoID int64, snapshot codexapp.Snapshot) tea.Cmd {
	if m.svc == nil || todoID <= 0 {
		return nil
	}
	projectPath = strings.TrimSpace(projectPath)
	if projectPath == "" {
		projectPath = strings.TrimSpace(snapshot.ProjectPath)
	}
	sessionID := strings.TrimSpace(snapshot.ThreadID)
	if projectPath == "" || sessionID == "" {
		return nil
	}
	provider := modelSessionSourceFromCodexProvider(embeddedProvider(snapshot))
	startedAt := embeddedSnapshotActivityAt(snapshot)
	if startedAt.IsZero() {
		startedAt = m.currentTime()
	}
	return func() tea.Msg {
		ctx, cancel := m.actionContext(tuiQuickActionTimeout)
		defer cancel()
		err := m.svc.MarkTodoWorkStarted(ctx, projectPath, todoID, provider, sessionID, startedAt)
		err = timeoutActionError(err, tuiQuickActionTimeout, "recording TODO work")
		return projectStatusRefreshedMsg{projectPath: projectPath, err: err}
	}
}

func (m Model) deleteTodoCmd(projectPath string, todoID int64) tea.Cmd {
	if m.svc == nil {
		return func() tea.Msg {
			return todoActionMsg{projectPath: projectPath, err: fmt.Errorf("service unavailable")}
		}
	}
	return func() tea.Msg {
		ctx, cancel := m.actionContext(tuiQuickActionTimeout)
		defer cancel()
		err := m.svc.DeleteTodo(ctx, projectPath, todoID)
		err = timeoutActionError(err, tuiQuickActionTimeout, "deleting the TODO")
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
		ctx, cancel := m.actionContext(tuiQuickActionTimeout)
		defer cancel()
		count, err := m.svc.PurgeDoneTodos(ctx, projectPath)
		err = timeoutActionError(err, tuiQuickActionTimeout, "purging completed TODOs")
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

func (m *Model) createTodoWorktreeCmd(launchCtx context.Context, launchID int64, projectPath string, todoID int64, todoText string, attachments []model.TodoAttachment, provider codexapp.Provider, openModelFirst bool, branchOverride, suffixOverride string) tea.Cmd {
	branchOverride = strings.TrimSpace(branchOverride)
	suffixOverride = strings.TrimSpace(suffixOverride)
	attachments = cloneTodoAttachments(attachments)
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
			todoID:         todoID,
			todoText:       todoText,
			attachments:    attachments,
			status:         todoWorktreePreparedStatus(len(result.PreparedPaths)),
			prepProfile:    result.PrepProfile,
			preparedPaths:  append([]string(nil), result.PreparedPaths...),
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
		reason := strings.TrimSpace(m.svc.TodoWorktreeSuggesterUnavailableReason())
		status := "Worktree suggestions are unavailable right now. Press e to enter names manually."
		if reason != "" {
			status = "Worktree suggestions unavailable: " + reason
		}
		return func() tea.Msg {
			return todoActionMsg{
				projectPath: projectPath,
				status:      status,
			}
		}
	}
	return func() tea.Msg {
		ctx, cancel := m.actionContext(tuiQuickActionTimeout)
		defer cancel()
		err := m.svc.RegenerateTodoWorktreeSuggestion(ctx, projectPath, todoID)
		err = timeoutActionError(err, tuiQuickActionTimeout, "refreshing the worktree suggestion")
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
		ctx, cancel := m.actionContext(tuiQuickActionTimeout)
		defer cancel()
		changed, err := m.svc.EnsureTodoWorktreeSuggestion(ctx, projectPath, todoID)
		err = timeoutActionError(err, tuiQuickActionTimeout, "preparing the worktree suggestion")
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

func (m Model) openPinnedTodoWorkSession(item model.TodoItem) (tea.Model, tea.Cmd, bool) {
	if item.Done || strings.TrimSpace(item.WorkSessionID) == "" {
		return m, nil, false
	}
	provider := todoWorkProvider(item)
	if provider == "" {
		return m, nil, false
	}
	if snapshot, projectPath, ok := m.livePinnedTodoWorkSnapshot(item); ok {
		m.closeTodoWorkLaunchDialogs()
		status := fmt.Sprintf("Opened pinned TODO #%d %s session. Alt+Up hides it.", item.ID, provider.Label())
		if state := todoWorkStateFromEmbeddedSnapshot(snapshot, false); state != "" && state != model.TodoWorkStateIdle {
			status = fmt.Sprintf("Opened pinned TODO #%d %s session (%s). Alt+Up hides it.", item.ID, provider.Label(), state)
		}
		updated, cmd := m.showCodexProject(projectPath, status)
		return updated, cmd, true
	}
	project, ok := m.pinnedTodoWorkProject(item)
	if !ok || strings.TrimSpace(project.Path) == "" {
		m.status = fmt.Sprintf("Pinned TODO #%d %s session is no longer available", item.ID, provider.Label())
		return m, nil, true
	}
	if !project.PresentOnDisk {
		return m, nil, false
	}
	if block, blocked := m.embeddedLaunchBlock(project, provider, false); blocked {
		m.status = block.Message
		return m, nil, true
	}
	if snapshot, ok := m.liveEmbeddedSnapshotForProject(project.Path, provider); ok && !todoWorkSessionIDMatches(provider, item.WorkSessionID, snapshot.ThreadID) {
		m.status = fmt.Sprintf("Another embedded %s session is open for this TODO lane. Finish or close it before opening TODO #%d's pinned session.", provider.Label(), item.ID)
		return m, nil, true
	}
	resumeID := todoWorkExternalSessionID(provider, item.WorkSessionID)
	if resumeID == "" {
		m.status = fmt.Sprintf("Pinned TODO #%d %s session is missing a resumable session id", item.ID, provider.Label())
		return m, nil, true
	}
	req := codexapp.LaunchRequest{
		Provider:                   provider,
		ProjectPath:                project.Path,
		ResumeID:                   resumeID,
		ForceNew:                   false,
		Preset:                     m.currentCodexLaunchPreset(),
		PlaywrightPolicy:           m.currentPlaywrightPolicy(),
		AppDataDir:                 m.appDataDir(),
		CodexHome:                  m.codexHome(),
		LCAgentPath:                m.lcagentPath(),
		LCAgentEnvFile:             m.lcagentEnvFile(),
		LCAgentOpenAIAPIKey:        m.openAIAPIKey(),
		LCAgentOpenRouterAPIKey:    m.openRouterAPIKey(),
		LCAgentDeepSeekAPIKey:      m.deepSeekAPIKey(),
		LCAgentMoonshotAPIKey:      m.moonshotAPIKey(),
		LCAgentXiaomiAPIKey:        m.xiaomiAPIKey(),
		LCAgentXiaomiBaseURL:       m.xiaomiBaseURL(),
		LCAgentOllamaAPIKey:        m.ollamaAPIKey(),
		LCAgentOllamaBaseURL:       m.ollamaBaseURL(),
		LCAgentOllamaModel:         m.ollamaModel(),
		LCAgentProviderAccessCheck: true,
		LCAgentRoutePreset:         m.lcagentRoutePreset(),
		LCAgentProvider:            m.lcagentProvider(),
		LCAgentAuto:                m.lcagentAuto(),
		LCAgentAdminWrite:          m.lcagentAdminWrite(),
		LCAgentToolProfile:         m.lcagentToolProfile(),
		LCAgentContextProfile:      m.lcagentContextProfile(),
		LCAgentRequestTimeout:      m.lcagentRequestTimeout(),
		LCAgentUtilityProvider:     m.lcagentUtilityProvider(),
		LCAgentUtilityModel:        m.lcagentUtilityModel(),
		LCAgentVisionProvider:      m.lcagentVisionProvider(),
		LCAgentVisionModel:         m.lcagentVisionModel(),
		LCAgentWebSearchBackend:    m.lcagentWebSearchBackend(),
		LCAgentWebSearchAPIKey:     m.lcagentWebSearchAPIKey(),
		LCAgentWebSearchEngineID:   m.lcagentWebSearchEngineID(),
		LCAgentWebSearchURL:        m.lcagentWebSearchURL(),
	}
	if err := req.Validate(); err != nil {
		m.status = err.Error()
		return m, nil, true
	}
	m.closeTodoWorkLaunchDialogs()
	m.ensureCodexRuntime()
	m.beginCodexPendingOpenWithVisibilityAndReveal(project.Path, provider, true, true)
	m.err = nil
	m.status = fmt.Sprintf("Opening pinned TODO #%d %s session...", item.ID, provider.Label())
	return m, m.openCodexSessionCmdWithVisibility(req, true), true
}

func (m *Model) closeTodoWorkLaunchDialogs() {
	m.todoDialog = nil
	m.todoCopyDialog = nil
	m.todoWorktreeEditor = nil
	m.todoExistingWorktree = nil
	m.todoEditor = nil
	m.todoDeleteConfirm = nil
}

func (m Model) livePinnedTodoWorkSnapshot(item model.TodoItem) (codexapp.Snapshot, string, bool) {
	provider := todoWorkProvider(item)
	if provider == "" || strings.TrimSpace(item.WorkSessionID) == "" {
		return codexapp.Snapshot{}, "", false
	}
	for _, projectPath := range m.pinnedTodoWorkProjectPathCandidates(item) {
		snapshot, ok := m.liveEmbeddedSnapshotForProject(projectPath, provider)
		if !ok {
			continue
		}
		if todoWorkSessionIDMatches(provider, item.WorkSessionID, snapshot.ThreadID) {
			return snapshot, projectPath, true
		}
	}
	return codexapp.Snapshot{}, "", false
}

func (m Model) pinnedTodoWorkProject(item model.TodoItem) (model.ProjectSummary, bool) {
	for _, projectPath := range m.pinnedTodoWorkProjectPathCandidates(item) {
		if project, ok := m.projectSummaryByPathAllProjects(projectPath); ok {
			return project, true
		}
		if project, ok := m.projectSummaryByPath(projectPath); ok {
			return project, true
		}
	}
	if projectPath := strings.TrimSpace(item.WorkProjectPath); projectPath != "" {
		return model.ProjectSummary{Path: projectPath}, true
	}
	return model.ProjectSummary{}, false
}

func (m Model) pinnedTodoWorkProjectPathCandidates(item model.TodoItem) []string {
	var paths []string
	add := func(path string) {
		path = strings.TrimSpace(path)
		if path == "" {
			return
		}
		for _, existing := range paths {
			if filepath.Clean(existing) == filepath.Clean(path) {
				return
			}
		}
		paths = append(paths, path)
	}
	add(item.WorkProjectPath)
	if strings.TrimSpace(item.WorkProjectPath) == "" {
		if linked, ok := m.todoLinkedWorktreeProject(item.ID); ok {
			add(linked.Path)
		}
	}
	add(item.ProjectPath)
	return paths
}

func todoWorkProvider(item model.TodoItem) codexapp.Provider {
	if provider := codexProviderFromSessionSource(item.WorkProvider); provider != "" {
		return provider
	}
	source, _ := model.ParseCanonicalSessionID(item.WorkSessionID)
	return codexProviderFromSessionSource(source)
}

func todoWorkExternalSessionID(provider codexapp.Provider, sessionID string) string {
	provider = provider.Normalized()
	if provider == "" {
		return strings.TrimSpace(sessionID)
	}
	source := modelSessionSourceFromCodexProvider(provider)
	return model.ExternalSessionID(source, embeddedSessionFormat(provider), sessionID, "")
}

func todoWorkSessionIDMatches(provider codexapp.Provider, expected, actual string) bool {
	expected = strings.TrimSpace(expected)
	actual = strings.TrimSpace(actual)
	if expected == "" || actual == "" {
		return false
	}
	if expected == actual {
		return true
	}
	provider = provider.Normalized()
	source := modelSessionSourceFromCodexProvider(provider)
	format := embeddedSessionFormat(provider)
	_, expectedCanonical, expectedRaw := model.NormalizeSessionIdentity(source, format, expected, "")
	_, actualCanonical, actualRaw := model.NormalizeSessionIdentity(source, format, actual, "")
	if expectedCanonical != "" && expectedCanonical == actualCanonical {
		return true
	}
	return expectedRaw != "" && expectedRaw == actualRaw
}

func (m Model) startTodoInProjectPath(projectPath string, todoID int64, todoText string, attachments []model.TodoAttachment, provider codexapp.Provider, openModelFirst bool) (tea.Model, tea.Cmd) {
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
	codexAttachments := codexAttachmentsFromTodo(attachments)
	if len(codexAttachments) > 0 && !providerSupportsTodoAttachments(provider) {
		m.status = todoAttachmentUnsupportedStatus(provider)
		return m, nil
	}
	if message, blocked := m.controlFreshSessionBlockedByActiveEngineerTurn(project, provider, fmt.Sprintf("TODO #%d", todoID)); blocked {
		m.status = message
		return m, nil
	}
	if !project.PresentOnDisk {
		m.status = provider.Label() + " launch requires a folder present on disk"
		return m, nil
	}
	req := codexapp.LaunchRequest{
		Provider:                   provider,
		ProjectPath:                project.Path,
		ResumeID:                   m.selectedProjectSessionID(project, provider),
		ForceNew:                   true,
		Preset:                     m.currentCodexLaunchPreset(),
		PlaywrightPolicy:           m.currentPlaywrightPolicy(),
		AppDataDir:                 m.appDataDir(),
		CodexHome:                  m.codexHome(),
		LCAgentPath:                m.lcagentPath(),
		LCAgentEnvFile:             m.lcagentEnvFile(),
		LCAgentOpenAIAPIKey:        m.openAIAPIKey(),
		LCAgentOpenRouterAPIKey:    m.openRouterAPIKey(),
		LCAgentDeepSeekAPIKey:      m.deepSeekAPIKey(),
		LCAgentMoonshotAPIKey:      m.moonshotAPIKey(),
		LCAgentXiaomiAPIKey:        m.xiaomiAPIKey(),
		LCAgentXiaomiBaseURL:       m.xiaomiBaseURL(),
		LCAgentOllamaAPIKey:        m.ollamaAPIKey(),
		LCAgentOllamaBaseURL:       m.ollamaBaseURL(),
		LCAgentOllamaModel:         m.ollamaModel(),
		LCAgentProviderAccessCheck: true,
		LCAgentRoutePreset:         m.lcagentRoutePreset(),
		LCAgentProvider:            m.lcagentProvider(),
		LCAgentAuto:                m.lcagentAuto(),
		LCAgentAdminWrite:          m.lcagentAdminWrite(),
		LCAgentToolProfile:         m.lcagentToolProfile(),
		LCAgentContextProfile:      m.lcagentContextProfile(),
		LCAgentRequestTimeout:      m.lcagentRequestTimeout(),
		LCAgentUtilityProvider:     m.lcagentUtilityProvider(),
		LCAgentUtilityModel:        m.lcagentUtilityModel(),
		LCAgentVisionProvider:      m.lcagentVisionProvider(),
		LCAgentVisionModel:         m.lcagentVisionModel(),
		LCAgentWebSearchBackend:    m.lcagentWebSearchBackend(),
		LCAgentWebSearchAPIKey:     m.lcagentWebSearchAPIKey(),
		LCAgentWebSearchEngineID:   m.lcagentWebSearchEngineID(),
		LCAgentWebSearchURL:        m.lcagentWebSearchURL(),
	}
	if err := req.Validate(); err != nil {
		m.clearTodoLaunchDraft(project.Path)
		m.status = err.Error()
		return m, nil
	}
	m.rememberEmbeddedProvider(provider)
	m.restoreCodexDraft(project.Path, codexDraftFromTodo(todoText, attachments))
	m.storeTodoLaunchDraft(todoLaunchDraftState{projectPath: project.Path, todoID: todoID, provider: provider, openModelFirst: openModelFirst, attachments: codexAttachments})
	m.todoEditor = nil
	m.todoDeleteConfirm = nil
	m.todoExistingWorktree = nil
	m.todoCopyDialog = nil
	m.todoDialog = nil
	m.ensureCodexRuntime()
	m.beginNewCodexPendingOpen(project.Path, provider)
	m.err = nil
	m.status = "Starting a new embedded " + provider.Label() + " session..."
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
	return m.startTodoInProjectPath(project.Path, item.ID, item.Text, item.Attachments, provider, openModelFirst)
}

func (m Model) startSelectedTodoInNewWorktree(provider codexapp.Provider, openModelFirst bool) (tea.Model, tea.Cmd) {
	if pending := m.activeTodoPendingLaunch(); pending != nil {
		m.openTodoPendingLaunchDialog(*pending, pending.todoWorktreeLaunchAlreadyRunningStatus(), false, todoPendingLaunchDialogFocusOK)
		return m, nil
	}
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
	if len(item.Attachments) > 0 && !providerSupportsTodoAttachments(provider) {
		m.status = todoAttachmentUnsupportedStatus(provider)
		return m, nil
	}
	projectName := ""
	if m.todoDialog != nil {
		projectName = strings.TrimSpace(m.todoDialog.ProjectName)
	}
	branchOverride := ""
	suffixOverride := ""
	if copyDialog := m.todoCopyDialog; copyDialog != nil {
		branchOverride = strings.TrimSpace(copyDialog.BranchOverride)
		suffixOverride = strings.TrimSpace(copyDialog.WorktreeSuffixOverride)
	}
	launchID, launchCtx := m.beginTodoPendingLaunch(projectPath, projectName, item.ID, item.Text, provider)
	m.todoEditor = nil
	m.todoDeleteConfirm = nil
	m.todoWorktreeEditor = nil
	m.todoExistingWorktree = nil
	m.todoCopyDialog = nil
	m.todoDialog = nil
	m.rememberEmbeddedProvider(provider)
	m.status = todoWorktreePreparingStatus
	selectPath := projectPath
	if pendingProject, ok := m.todoPendingLaunchProjectSummary(); ok {
		selectPath = pendingProject.Path
	}
	m.rebuildProjectList(selectPath)
	return m, m.createTodoWorktreeCmd(launchCtx, launchID, projectPath, item.ID, item.Text, item.Attachments, provider, openModelFirst, branchOverride, suffixOverride)
}

func (m Model) activeTodoPendingLaunch() *todoPendingLaunchState {
	if m.todoPendingLaunch == nil || m.todoPendingLaunch.Canceled {
		return nil
	}
	return m.todoPendingLaunch
}

func (m Model) todoPendingLaunchProjectSummary() (model.ProjectSummary, bool) {
	pending := m.activeTodoPendingLaunch()
	if pending == nil {
		return model.ProjectSummary{}, false
	}
	rootPath := normalizeProjectPath(pending.ProjectPath)
	if rootPath == "" {
		return model.ProjectSummary{}, false
	}
	rootProject, _ := m.projectSummaryByPathAllProjects(rootPath)
	if strings.TrimSpace(rootProject.Path) == "" {
		rootProject, _ = m.projectSummaryByPath(rootPath)
	}
	startedAt := pending.StartedAt
	if startedAt.IsZero() {
		startedAt = m.currentTime()
	}
	if startedAt.IsZero() {
		startedAt = time.Now()
	}
	name := "TODO worktree"
	if pending.TodoID > 0 {
		name = fmt.Sprintf("TODO #%d worktree", pending.TodoID)
	}
	project := model.ProjectSummary{
		Path:                            todoPendingLaunchProjectPath(rootPath, pending.ID),
		Name:                            name,
		Kind:                            model.ProjectKindProject,
		CategoryID:                      rootProject.CategoryID,
		CategoryName:                    rootProject.CategoryName,
		LastActivity:                    startedAt,
		Status:                          model.StatusActive,
		AttentionScore:                  rootProject.AttentionScore,
		PresentOnDisk:                   true,
		WorktreeRootPath:                rootPath,
		WorktreeKind:                    model.WorktreeKindLinked,
		WorktreeParentBranch:            strings.TrimSpace(rootProject.RepoBranch),
		WorktreeOriginTodoID:            pending.TodoID,
		LatestSessionClassification:     model.ClassificationRunning,
		LatestSessionClassificationType: model.SessionCategoryInProgress,
		LatestSessionSummary:            todoPendingLaunchListSummary(*pending, m.currentTime()),
	}
	return project, true
}

func todoPendingLaunchProjectPath(rootPath string, launchID int64) string {
	rootPath = normalizeProjectPath(rootPath)
	if rootPath == "" {
		return ""
	}
	if launchID <= 0 {
		launchID = 1
	}
	return filepath.Clean(rootPath + fmt.Sprintf("--pending-todo-worktree-%d", launchID))
}

func (m Model) todoPendingLaunchForProjectPath(projectPath string) (*todoPendingLaunchState, bool) {
	pending := m.activeTodoPendingLaunch()
	if pending == nil {
		return nil, false
	}
	if normalizeProjectPath(projectPath) != todoPendingLaunchProjectPath(pending.ProjectPath, pending.ID) {
		return nil, false
	}
	return pending, true
}

func todoPendingLaunchListSummary(pending todoPendingLaunchState, now time.Time) string {
	parts := []string{"preparing checkout"}
	if !pending.StartedAt.IsZero() {
		if now.IsZero() {
			now = time.Now()
		}
		if elapsed := now.Sub(pending.StartedAt); elapsed > 0 {
			parts = append(parts, "elapsed "+formatRunningDuration(elapsed))
		}
	}
	return strings.Join(parts, "; ")
}

func todoPendingLaunchDetailSummary(pending todoPendingLaunchState, now time.Time) string {
	summary := "Creating the dedicated worktree and hydrating submodules if this repo needs them."
	if !pending.StartedAt.IsZero() {
		if now.IsZero() {
			now = time.Now()
		}
		if elapsed := now.Sub(pending.StartedAt); elapsed > 0 {
			summary += " Elapsed: " + formatRunningDuration(elapsed) + "."
		}
	}
	return summary
}

func (p todoPendingLaunchState) todoWorktreeLaunchAlreadyRunningStatus() string {
	if p.TodoID > 0 {
		return fmt.Sprintf("TODO #%d worktree is already being created; wait for it to finish.", p.TodoID)
	}
	return "A TODO worktree is already being created; wait for it to finish."
}

func todoWorktreePreparedStatus(preparedPathCount int) string {
	if preparedPathCount <= 0 {
		return "Worktree ready"
	}
	if preparedPathCount == 1 {
		return "Worktree ready; hydrated 1 submodule"
	}
	return fmt.Sprintf("Worktree ready; hydrated %d submodules", preparedPathCount)
}

func todoWorktreeSessionStartStatus(provider codexapp.Provider, openModelFirst bool, preparedPathCount int) string {
	prefix := todoWorktreePreparedStatus(preparedPathCount)
	if openModelFirst {
		return prefix + "; starting a new embedded " + provider.Label() + " session..."
	}
	return prefix + "; starting TODO session..."
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
	projectSummary, _ := m.projectSummaryByPath(dialog.ProjectPath)
	displayOpenCount := openCount
	displayTotalCount := len(items)
	if len(items) == 0 && projectSummary.TotalTODOCount > 0 {
		displayOpenCount = projectSummary.OpenTODOCount
		displayTotalCount = projectSummary.TotalTODOCount
	}
	title := detailSectionStyle.Render("TODO") + "  " + detailValueStyle.Render(dialog.ProjectName)
	summary := detailMutedStyle.Render(fmt.Sprintf("%d open, %d total", displayOpenCount, displayTotalCount))
	lines := []string{title, summary, ""}
	if len(items) == 0 {
		if projectSummary.TotalTODOCount > 0 && m.todoDialogDetailPending(dialog.ProjectPath) {
			lines = append(lines, detailMutedStyle.Render("Loading TODOs..."))
		} else if errText := m.detailReloadError(dialog.ProjectPath); projectSummary.TotalTODOCount > 0 && errText != "" {
			lines = append(lines, detailWarningStyle.Render("TODOs could not load"))
			lines = append(lines, detailMutedStyle.Render(truncateText(errText, panelInnerW)))
			lines = append(lines, detailMutedStyle.Render("Close and reopen the dialog to retry"))
		} else if projectSummary.TotalTODOCount > 0 {
			lines = append(lines, detailWarningStyle.Render(fmt.Sprintf("TODO count says %d total, but the list did not load yet", projectSummary.TotalTODOCount)))
			lines = append(lines, detailMutedStyle.Render("Close and reopen the dialog to retry"))
		} else {
			lines = append(lines, detailMutedStyle.Render("No TODOs yet"))
			lines = append(lines, detailMutedStyle.Render("Press a to add one"))
		}
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
	}
	if attachmentLine := renderTodoEditorAttachments(dialog.Attachments, panelInnerW); attachmentLine != "" {
		lines = append(lines, "", attachmentLine)
	}
	lines = append(lines, "", todoEditorLegendLine())
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
	if summary := todoAttachmentSummary(item.Attachments); summary != "" {
		base += " · " + summary
	}
	label, labelStyle := m.todoActivityLabel(item)
	if label == "" {
		label, labelStyle = m.todoWorktreeSuggestionLabel(item)
	}
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

func (m Model) todoActivityLabel(item model.TodoItem) (string, lipgloss.Style) {
	if item.Done {
		return "", lipgloss.Style{}
	}
	if label, style, ok := m.todoPinnedWorkLabel(item); ok {
		return label, style
	}
	state := model.NormalizeTodoWorkState(item.WorkState)
	if state == "" || state == model.TodoWorkStateIdle {
		return "", lipgloss.Style{}
	}
	label := string(state)
	if providerLabel := todoWorkProviderLabel(item.WorkProvider); providerLabel != "" {
		label += " " + providerLabel
	}
	switch state {
	case model.TodoWorkStateWaiting, model.TodoWorkStateBlocked:
		return label, detailWarningStyle
	default:
		return label, statusStyle(model.StatusActive)
	}
}

func (m Model) todoPinnedWorkLabel(item model.TodoItem) (string, lipgloss.Style, bool) {
	if strings.TrimSpace(item.WorkSessionID) == "" {
		return "", lipgloss.Style{}, false
	}
	provider := todoWorkProvider(item)
	if provider == "" {
		return "", lipgloss.Style{}, false
	}
	providerLabel := todoWorkProviderLabel(modelSessionSourceFromCodexProvider(provider))
	if providerLabel == "" {
		providerLabel = provider.Label()
	}
	if snapshot, _, ok := m.livePinnedTodoWorkSnapshot(item); ok {
		state := todoWorkStateFromEmbeddedSnapshot(snapshot, false)
		switch state {
		case model.TodoWorkStateWaiting, model.TodoWorkStateBlocked:
			return string(state) + " " + providerLabel, detailWarningStyle, true
		case model.TodoWorkStateWorking:
			return string(state) + " " + providerLabel, statusStyle(model.StatusActive), true
		default:
			return providerLabel + " session", detailMutedStyle, true
		}
	}
	state := model.NormalizeTodoWorkState(item.WorkState)
	if state == "" || state == model.TodoWorkStateIdle {
		return "resume " + providerLabel, detailMutedStyle, true
	}
	return "stale " + providerLabel, detailWarningStyle, true
}

func todoWorkProviderLabel(provider model.SessionSource) string {
	switch model.NormalizeSessionSource(provider) {
	case model.SessionSourceOpenCode:
		return "OpenCode"
	case model.SessionSourceClaudeCode:
		return "Claude"
	case model.SessionSourceLCAgent:
		return "LCAgent"
	case model.SessionSourceCodex:
		return "Codex"
	default:
		return ""
	}
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
		renderDialogAction("ctrl+v", "image", pushActionKeyStyle, pushActionTextStyle),
		renderDialogAction("ctrl+s", "save", commitActionKeyStyle, commitActionTextStyle),
		renderDialogAction("Esc", "cancel", cancelActionKeyStyle, cancelActionTextStyle),
	)
}

func renderTodoEditorAttachments(attachments []model.TodoAttachment, width int) string {
	attachments = cloneTodoAttachments(attachments)
	if len(attachments) == 0 {
		return ""
	}
	labels := make([]string, 0, len(attachments))
	for i, attachment := range attachments {
		labels = append(labels, todoAttachmentLabel(i, attachment))
	}
	return detailField("Images", truncateText(strings.Join(labels, " "), width))
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
	}
	if summary := todoAttachmentSummary(copyDialog.Attachments); summary != "" {
		lines = append(lines, detailField("Images", summary))
	}
	lines = append(lines, "")
	projectPath := copyDialog.ProjectPath
	settings := m.currentSettingsBaseline()
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
		label := m.todoCopyProviderButtonLabel(projectPath, provider, settings)
		providerButtons = append(providerButtons, renderDialogButton(label, copyDialog.Provider == provider))
	}
	optionButtons := []string{m.renderTodoCopyModelToggle(copyDialog.OpenModelFirst)}
	lines = append(lines, m.renderTodoCopyChooserColumns(panelInnerW, runButtons, providerButtons, optionButtons))
	if statusLine := m.todoCopyProviderStatusLine(copyDialog.Provider, settings); statusLine != "" {
		lines = append(lines, detailField("Agent status", statusLine))
	}
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
			renderDialogAction("ctrl+s", "save", commitActionKeyStyle, commitActionTextStyle),
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
	return embeddedLaunchProviderOptions()
}

type todoCopyProviderReadiness struct {
	State  string
	Detail string
	Style  lipgloss.Style
}

func (m Model) todoCopyProviderButtonLabel(projectPath string, provider codexapp.Provider, settings config.EditableSettings) string {
	label := provider.Label()
	if state := strings.TrimSpace(m.todoCopyProviderReadiness(provider, settings).State); state != "" {
		label += " - " + state
	}
	label += "  (" + m.embeddedModelLabelForProject(projectPath, provider) + ")"
	return label
}

func (m Model) todoCopyProviderStatusLine(provider codexapp.Provider, settings config.EditableSettings) string {
	readiness := m.todoCopyProviderReadiness(provider, settings)
	detail := strings.TrimSpace(readiness.Detail)
	if detail == "" {
		return ""
	}
	return readiness.Style.Render(detail)
}

func (m Model) todoCopyProviderReadiness(provider codexapp.Provider, settings config.EditableSettings) todoCopyProviderReadiness {
	if dialog := m.todoCopyDialog; dialog != nil && len(dialog.Attachments) > 0 && !providerSupportsTodoAttachments(provider) {
		return todoCopyProviderReadiness{
			State:  "no images",
			Detail: todoAttachmentUnsupportedStatus(provider),
			Style:  detailWarningStyle,
		}
	}
	switch provider.Normalized() {
	case codexapp.ProviderCodex:
		return m.todoCopyCLIProviderReadiness(config.AIBackendCodex, settings)
	case codexapp.ProviderOpenCode:
		return m.todoCopyCLIProviderReadiness(config.AIBackendOpenCode, settings)
	case codexapp.ProviderClaudeCode:
		return m.todoCopyCLIProviderReadiness(config.AIBackendClaude, settings)
	case codexapp.ProviderLCAgent:
		return todoCopyLCAgentReadiness(settings)
	default:
		return todoCopyProviderReadiness{
			State:  "unknown",
			Detail: "Unknown embedded agent provider.",
			Style:  detailWarningStyle,
		}
	}
}

func (m Model) todoCopyCLIProviderReadiness(backend config.AIBackend, settings config.EditableSettings) todoCopyProviderReadiness {
	status, known := m.inferenceBackendStatus(backend, settings)
	if !known {
		return todoCopyProviderReadiness{
			State:  "checking",
			Detail: backend.Label() + " availability has not refreshed yet.",
			Style:  detailMutedStyle,
		}
	}
	if status.Ready {
		detail := strings.TrimSpace(status.Detail)
		if detail == "" {
			detail = backend.Label() + " is ready."
		}
		return todoCopyProviderReadiness{
			State:  "ready",
			Detail: detail,
			Style:  footerPrimaryLabelStyle,
		}
	}
	state := "needs setup"
	switch {
	case !status.Installed && backend.RequiresCLIInstallHint():
		state = "not installed"
	case status.Installed && !status.Authenticated:
		state = "needs login"
	}
	detail := firstNonEmptyTrimmed(status.LoginHint, status.Detail, backend.Label()+" needs setup before it can start TODO work.")
	return todoCopyProviderReadiness{
		State:  state,
		Detail: detail,
		Style:  detailWarningStyle,
	}
}

func todoCopyLCAgentReadiness(settings config.EditableSettings) todoCopyProviderReadiness {
	provider := firstNonEmptyTrimmed(lcagentProviderForRoutePreset(settings.LCAgentRoutePreset), settings.LCAgentProvider, "openrouter")
	keyName := lcagentProviderAPIKeyName(provider)
	if keyName == "" {
		return todoCopyProviderReadiness{
			State:  "needs config",
			Detail: "Unknown LCAgent provider " + provider + ". Open /settings and choose openrouter, openai, deepseek, moonshot, xiaomi, or ollama.",
			Style:  detailWarningStyle,
		}
	}
	state, style, detail := lcagentCredentialSmokeCheck(settings)
	readinessState := state
	switch state {
	case "ready", "optional":
		readinessState = "ready"
	case "blocked":
		readinessState = "blocked"
	case "unknown":
		readinessState = "needs config"
	case "needed":
		readinessState = "needs key"
	}
	return todoCopyProviderReadiness{
		State:  readinessState,
		Detail: detail,
		Style:  style,
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
	if len(item.Attachments) > 0 && !providerSupportsTodoAttachments(dialog.Provider) {
		return todoWorktreeLaunchUnavailable, todoAttachmentUnsupportedStatus(dialog.Provider)
	}
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
