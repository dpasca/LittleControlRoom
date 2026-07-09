package tui

import (
	"fmt"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"lcroom/internal/commands"
	"lcroom/internal/model"
	"strings"
	"time"
)

func (m Model) updateNormalMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.pendingG {
		m.pendingG = false
		if msg.String() == "g" {
			if m.focusedPane == focusDetail {
				m.detailViewport.GotoTop()
				return m, nil
			}
			if m.focusedPane == focusRuntime {
				m.runtimeViewport.GotoTop()
				return m, nil
			}
			return m, m.moveSelectionTo(0)
		}
	}
	switch msg.String() {
	case "ctrl+c", "q":
		if m.codexManager != nil {
			_ = m.codexManager.CloseAll()
		}
		if m.runtimeManager != nil {
			_ = m.runtimeManager.CloseAll()
		}
		if m.unsub != nil {
			m.unsub()
		}
		return m, tea.Quit
	case "/":
		m.openCommandMode()
		return m, textinput.Blink
	case "`":
		return m.openHelpChatModeOrSetupPrompt()
	case "tab":
		m.cyclePaneFocus(1)
		return m, nil
	case "shift+tab":
		m.cyclePaneFocus(-1)
		return m, nil
	case "?":
		m.showHelp = !m.showHelp
		if m.showHelp {
			m.status = "Quick help open. Press ? or Esc to close"
		} else {
			m.status = "Quick help closed"
		}
		return m, nil
	case "f":
		return m, m.openProjectFilterDialog()
	case "a":
		return m, m.toggleArchiveMode()
	case "b":
		return m.openBossModeOrSetupPrompt()
	case "f3":
		return m.cycleCodexSession(1)
	case "esc":
		if m.showHelp {
			m.showHelp = false
			m.status = "Quick help closed"
			return m, nil
		}
		if m.focusProjectsPane() {
			return m, nil
		}
	case "enter":
		if m.focusedPane == focusProjects {
			if row, _, ok := m.selectedProjectRow(); ok && row.Kind == projectListRowPendingWorktree {
				return m.openTodoPendingLaunchDialogForSelection(todoPendingLaunchDialogFocusOK)
			}
			project, ok := m.selectedProject()
			if !ok {
				m.status = "No project selected"
				return m, nil
			}
			return m.launchEmbeddedForSelection(m.preferredEmbeddedProviderForProject(project), false, "")
		}
		if m.focusedPane == focusRuntime {
			return m, m.activateRuntimePaneAction()
		}
	case "up", "k":
		if m.focusedPane == focusDetail {
			m.detailViewport.LineUp(1)
			return m, nil
		}
		if m.focusedPane == focusRuntime {
			m.runtimeViewport.LineUp(1)
			return m, nil
		}
		return m, m.moveSelectionBy(-1)
	case "down", "j":
		if m.focusedPane == focusDetail {
			m.detailViewport.LineDown(1)
			return m, nil
		}
		if m.focusedPane == focusRuntime {
			m.runtimeViewport.LineDown(1)
			return m, nil
		}
		return m, m.moveSelectionBy(1)
	case "pgup":
		if m.focusedPane == focusDetail {
			m.detailViewport.PageUp()
			return m, nil
		}
		if m.focusedPane == focusRuntime {
			m.runtimeViewport.PageUp()
			return m, nil
		}
		return m, m.moveSelectionBy(-m.rowsVisible())
	case "pgdown":
		if m.focusedPane == focusDetail {
			m.detailViewport.PageDown()
			return m, nil
		}
		if m.focusedPane == focusRuntime {
			m.runtimeViewport.PageDown()
			return m, nil
		}
		return m, m.moveSelectionBy(m.rowsVisible())
	case "ctrl+u":
		if m.focusedPane == focusDetail {
			m.detailViewport.HalfPageUp()
			return m, nil
		}
		if m.focusedPane == focusRuntime {
			m.runtimeViewport.HalfPageUp()
			return m, nil
		}
		return m, m.moveSelectionBy(-m.rowsVisible())
	case "ctrl+d":
		if m.focusedPane == focusDetail {
			m.detailViewport.HalfPageDown()
			return m, nil
		}
		if m.focusedPane == focusRuntime {
			m.runtimeViewport.HalfPageDown()
			return m, nil
		}
		return m, m.moveSelectionBy(m.rowsVisible())
	case "home":
		if m.focusedPane == focusDetail {
			m.detailViewport.GotoTop()
			return m, nil
		}
		if m.focusedPane == focusRuntime {
			m.runtimeViewport.GotoTop()
			return m, nil
		}
		return m, m.moveSelectionTo(0)
	case "end", "G":
		if m.focusedPane == focusDetail {
			m.detailViewport.GotoBottom()
			return m, nil
		}
		if m.focusedPane == focusRuntime {
			m.runtimeViewport.GotoBottom()
			return m, nil
		}
		return m, m.moveSelectionTo(max(0, len(m.projects)-1))
	case "g":
		m.pendingG = true
		return m, nil
	case "left", "h":
		if m.focusedPane == focusRuntime {
			m.moveRuntimeActionSelection(-1)
			return m, nil
		}
		if m.focusedPane == focusProjects {
			if row, project, ok := m.selectedProjectRow(); ok {
				if row.Kind == projectListRowWorktree || row.Kind == projectListRowPendingWorktree {
					if m.worktreeExpanded == nil {
						m.worktreeExpanded = map[string]bool{}
					}
					m.worktreeExpanded[row.RootPath] = false
					m.rebuildProjectList(projectWorktreeRootPath(project))
					m.status = "Worktrees collapsed"
					return m, m.requestProjectDetailViewCmd(projectWorktreeRootPath(project))
				}
				if row.Kind == projectListRowRepo && row.LinkedCount > 0 && row.Expanded {
					return m, m.toggleSelectedWorktreeGroup()
				}
			}
		}
	case "right", "l":
		if m.focusedPane == focusRuntime {
			m.moveRuntimeActionSelection(1)
			return m, nil
		}
		if m.focusedPane == focusProjects {
			if row, _, ok := m.selectedProjectRow(); ok && row.Kind == projectListRowRepo && row.LinkedCount > 0 && !row.Expanded {
				return m, m.toggleSelectedWorktreeGroup()
			}
		}
	case "[":
		if m.focusedPane == focusRuntime {
			m.selectRuntimeProcess(-1)
			return m, nil
		}
	case "]":
		if m.focusedPane == focusRuntime {
			m.selectRuntimeProcess(1)
			return m, nil
		}
	case "o":
		if m.focusedPane == focusRuntime {
			return m, nil
		}
		if m.sortMode == sortByAttention {
			return m, m.setSortMode(sortByRecent)
		}
		return m, m.setSortMode(sortByAttention)
	case "p":
		if p, ok := m.selectedProject(); ok {
			return m, m.togglePinCmd(p.Path)
		}
	case "d":
		return m, m.openScratchTaskActionConfirmForSelection()
	case "t":
		return m, m.openTodoDialogForSelection()
	case "w":
		return m, m.toggleSelectedWorktreeGroup()
	case "M":
		return m, m.openWorktreeMergeConfirmForSelection()
	case "x":
		if row, _, ok := m.selectedProjectRow(); ok && row.Kind == projectListRowPendingWorktree {
			return m.openTodoPendingLaunchDialogForSelection(todoPendingLaunchDialogFocusAbort)
		}
		return m.openHideActionForSelection()
	}
	return m, nil
}

func (m Model) updateCommandMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.closeCommandMode("Command canceled")
		return m, nil
	case "up", "ctrl+p":
		m.moveCommandSelection(-1)
		return m, nil
	case "down", "ctrl+n":
		m.moveCommandSelection(1)
		return m, nil
	case "tab":
		if m.applySelectedCommandSuggestion() {
			return m, nil
		}
	case "shift+tab":
		m.moveCommandSelection(-1)
		return m, nil
	case "enter":
		raw := m.resolvedCommandInput()
		inv, err := commands.Parse(raw)
		if err != nil {
			m.err = nil
			m.status = err.Error()
			return m, nil
		}
		m.closeCommandMode("")
		m.err = nil
		return m.dispatchCommand(inv)
	}

	var cmd tea.Cmd
	m.commandInput, cmd = m.commandInput.Update(msg)
	m.syncCommandSelection()
	return m, cmd
}

func (m Model) updateCommitPreviewMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.commitPreview == nil {
		return m, nil
	}
	if m.commitApplying {
		return m, nil
	}
	if m.commitPreviewRefreshing && msg.String() != "esc" {
		return m, nil
	}
	switch msg.String() {
	case "esc":
		m.commitPreviewRequestID++
		m.clearPendingGitSummary(m.commitPreview.ProjectPath)
		m.commitPreview = nil
		m.commitTodoCompletions = nil
		m.commitPreviewMessageOverride = ""
		m.commitPreviewRefreshing = false
		m.commitApplying = false
		m.status = "Commit preview canceled"
		return m, nil
	case "d":
		cmd := m.startDiffViewFromCommitPreview(*m.commitPreview, m.commitPreviewMessageOverride)
		m.commitPreview = nil
		m.commitTodoCompletions = nil
		m.commitPreviewMessageOverride = ""
		m.commitPreviewRefreshing = false
		m.commitApplying = false
		return m, cmd
	case "up", "k":
		if len(m.commitTodoCompletions) > 0 && m.commitTodoSelected > 0 {
			m.commitTodoSelected--
		}
		return m, nil
	case "down", "j":
		if len(m.commitTodoCompletions) > 0 && m.commitTodoSelected < len(m.commitTodoCompletions)-1 {
			m.commitTodoSelected++
		}
		return m, nil
	case " ":
		if len(m.commitTodoCompletions) > 0 {
			m.commitTodoCompletions[m.commitTodoSelected].Selected = !m.commitTodoCompletions[m.commitTodoSelected].Selected
		}
		return m, nil
	case "shift+enter", "alt+enter":
		if !m.commitPreview.CanPush {
			m.status = "Commit & push is unavailable for this repo"
			return m, nil
		}
		m.commitApplying = true
		m.setPendingGitSummary(m.commitPreview.ProjectPath, "Committing and pushing...")
		m.status = "Committing and pushing..."
		return m, m.applyCommitPreviewCmd(*m.commitPreview, true)
	case "enter":
		m.commitApplying = true
		m.setPendingGitSummary(m.commitPreview.ProjectPath, "Committing...")
		m.status = "Committing..."
		return m, m.applyCommitPreviewCmd(*m.commitPreview, false)
	}
	return m, nil
}

func (m Model) updateGitStatusDialogMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.gitStatusDialog == nil {
		return m, nil
	}
	if m.gitStatusApplying {
		return m, nil
	}
	switch msg.String() {
	case "esc":
		m.status = gitStatusDialogDismissStatus(*m.gitStatusDialog)
		m.gitStatusDialog = nil
		m.gitStatusApplying = false
		return m, nil
	case "enter":
		if m.gitStatusDialog.ResolveSubmodules {
			m.gitStatusApplying = true
			m.setPendingGitSummary(m.gitStatusDialog.ProjectPath, "Resolving submodule commits...")
			m.status = "Resolving submodule commits..."
			return m, m.resolveSubmodulesAndContinueCmd(m.gitStatusDialog.ProjectPath, m.gitStatusDialog.CommitIntent, m.gitStatusDialog.CommitMessage)
		}
		if !m.gitStatusDialog.CanPush {
			m.status = gitStatusDialogDismissStatus(*m.gitStatusDialog)
			m.gitStatusDialog = nil
			m.gitStatusApplying = false
			return m, nil
		}
		m.gitStatusApplying = true
		m.setPendingGitOperation(m.gitStatusDialog.ProjectPath, pendingGitOperationPush, "Pushing existing commits...")
		m.status = "Pushing existing commits..."
		return m, m.pushCmd(m.gitStatusDialog.ProjectPath)
	}
	return m, nil
}

func (m *Model) moveSelectionBy(delta int) tea.Cmd {
	if len(m.projects) == 0 || delta == 0 {
		return nil
	}
	return m.moveSelectionTo(m.selected + delta)
}

func (m *Model) moveSelectionTo(index int) tea.Cmd {
	if len(m.projects) == 0 {
		m.selected = 0
		m.detail = model.ProjectDetail{}
		m.syncDetailViewport(true)
		return nil
	}
	if index < 0 {
		index = 0
	}
	if index >= len(m.projects) {
		index = len(m.projects) - 1
	}
	if index == m.selected {
		return nil
	}
	m.selected = index
	m.markSelectionFlash(time.Time{})
	m.ensureSelectionVisible()
	m.syncDetailViewport(true)
	return m.requestSelectedProjectDetailViewCmd()
}

func (m *Model) cyclePaneFocus(delta int) {
	order := []paneFocus{focusProjects, focusDetail, focusRuntime}
	current := 0
	for i, pane := range order {
		if pane == m.focusedPane {
			current = i
			break
		}
	}
	if delta == 0 {
		delta = 1
	}
	next := (current + delta) % len(order)
	if next < 0 {
		next += len(order)
	}
	m.focusedPane = order[next]
	m.status = focusedPaneStatus(m.focusedPane)
	m.ensureSelectionVisible()
	m.syncDetailViewport(false)
}

func (m *Model) focusProjectsPane() bool {
	if m.focusedPane == focusProjects {
		return false
	}
	m.focusedPane = focusProjects
	m.status = focusedPaneStatus(m.focusedPane)
	m.ensureSelectionVisible()
	m.syncDetailViewport(false)
	return true
}

func (m *Model) setFocusedPaneFromCommand(target commands.FocusTarget) {
	switch target {
	case commands.FocusDetail:
		m.focusedPane = focusDetail
	case commands.FocusRuntime:
		m.focusedPane = focusRuntime
	default:
		m.focusedPane = focusProjects
	}
	m.status = focusedPaneStatus(m.focusedPane)
	m.ensureSelectionVisible()
	m.syncDetailViewport(false)
}

func focusedPaneStatus(pane paneFocus) string {
	switch pane {
	case focusDetail:
		return "Focus: detail pane"
	case focusRuntime:
		return "Focus: runtime pane"
	default:
		return "Focus: project list"
	}
}

func (m *Model) setSortMode(mode projectSortMode) tea.Cmd {
	selectedPath := ""
	if p, ok := m.selectedProject(); ok {
		selectedPath = p.Path
	}
	m.sortMode = mode
	m.rebuildProjectList(selectedPath)
	m.syncDetailViewport(false)
	m.status = fmt.Sprintf("Sort: %s | View: %s", m.sortMode, visibilityLabel(m.visibility))
	if p, ok := m.selectedProject(); ok {
		return m.requestProjectDetailViewCmd(p.Path)
	}
	m.detail = model.ProjectDetail{}
	m.syncDetailViewport(true)
	return nil
}

func (m *Model) setVisibilityMode(mode projectVisibilityMode) tea.Cmd {
	selectedPath := ""
	if p, ok := m.selectedProject(); ok {
		selectedPath = p.Path
	}
	m.visibility = mode
	m.rebuildProjectList(selectedPath)
	m.syncDetailViewport(false)
	m.status = fmt.Sprintf("Visibility: %s", visibilityLabel(m.visibility))
	if p, ok := m.selectedProject(); ok {
		return m.requestProjectDetailViewCmd(p.Path)
	}
	m.detail = model.ProjectDetail{}
	m.syncDetailViewport(true)
	return nil
}

func (m *Model) toggleArchiveMode() tea.Cmd {
	return m.cycleProjectTab(1)
}

func (m *Model) setArchiveMode(mode projectArchiveMode) tea.Cmd {
	if mode == projectArchiveCategory && strings.TrimSpace(m.selectedCategoryID) == "" {
		mode = projectArchiveMain
	}
	if mode != projectArchiveArchived && mode != projectArchiveCategory {
		mode = projectArchiveMain
	}
	selectedPath := ""
	if p, ok := m.selectedProject(); ok {
		selectedPath = p.Path
	}
	if m.archiveMode != mode {
		selectedPath = ""
	}
	m.archiveMode = mode
	if mode != projectArchiveCategory {
		m.selectedCategoryID = ""
	}
	m.rebuildProjectList(selectedPath)
	m.syncDetailViewport(false)
	m.status = fmt.Sprintf("Project tab: %s", m.currentProjectTabLabel())
	if p, ok := m.selectedProject(); ok {
		return m.requestProjectDetailViewCmd(p.Path)
	}
	m.detail = model.ProjectDetail{}
	m.syncDetailViewport(true)
	return nil
}

func (m *Model) setCategoryMode(categoryID string) tea.Cmd {
	categoryID = strings.TrimSpace(categoryID)
	if categoryID == "" {
		return m.setArchiveMode(projectArchiveMain)
	}
	if _, ok := m.projectCategoryByID(categoryID); !ok {
		return m.setArchiveMode(projectArchiveMain)
	}
	selectedPath := ""
	if p, ok := m.selectedProject(); ok {
		selectedPath = p.Path
	}
	if m.archiveMode != projectArchiveCategory || strings.TrimSpace(m.selectedCategoryID) != categoryID {
		selectedPath = ""
	}
	m.archiveMode = projectArchiveCategory
	m.selectedCategoryID = categoryID
	m.rebuildProjectList(selectedPath)
	m.syncDetailViewport(false)
	m.status = fmt.Sprintf("Project tab: %s", m.currentProjectTabLabel())
	if p, ok := m.selectedProject(); ok {
		return m.requestProjectDetailViewCmd(p.Path)
	}
	m.detail = model.ProjectDetail{}
	m.syncDetailViewport(true)
	return nil
}

func (m *Model) cycleProjectTab(delta int) tea.Cmd {
	tabs := m.projectTabDescriptors()
	if len(tabs) == 0 {
		return nil
	}
	current := m.currentProjectTabIndex(tabs)
	if current < 0 {
		current = 0
	}
	next := (current + delta) % len(tabs)
	if next < 0 {
		next += len(tabs)
	}
	tab := tabs[next]
	if tab.mode == projectArchiveCategory {
		return m.setCategoryMode(tab.categoryID)
	}
	return m.setArchiveMode(tab.mode)
}

type projectTabDescriptor struct {
	mode       projectArchiveMode
	categoryID string
	label      string
	private    bool
}

func (m Model) projectTabDescriptors() []projectTabDescriptor {
	tabs := []projectTabDescriptor{{mode: projectArchiveMain, label: "Main"}}
	for _, category := range m.projectCategories {
		if strings.TrimSpace(category.ID) == "" || strings.TrimSpace(category.Name) == "" {
			continue
		}
		tabs = append(tabs, projectTabDescriptor{mode: projectArchiveCategory, categoryID: strings.TrimSpace(category.ID), label: strings.TrimSpace(category.Name), private: category.Private})
	}
	tabs = append(tabs, projectTabDescriptor{mode: projectArchiveArchived, label: "Archived"})
	return tabs
}

func (m Model) currentProjectTabIndex(tabs []projectTabDescriptor) int {
	for i, tab := range tabs {
		if tab.mode != m.archiveMode {
			continue
		}
		if tab.mode == projectArchiveCategory && strings.TrimSpace(tab.categoryID) != strings.TrimSpace(m.selectedCategoryID) {
			continue
		}
		return i
	}
	return -1
}

func (m *Model) ensureSelectedCategoryTab() {
	if m.archiveMode != projectArchiveCategory {
		return
	}
	if _, ok := m.projectCategoryByID(m.selectedCategoryID); ok {
		return
	}
	m.archiveMode = projectArchiveMain
	m.selectedCategoryID = ""
}

func (m Model) projectCategoryByID(categoryID string) (model.ProjectCategory, bool) {
	categoryID = strings.TrimSpace(categoryID)
	if categoryID == "" {
		return model.ProjectCategory{}, false
	}
	for _, category := range m.projectCategories {
		if strings.TrimSpace(category.ID) == categoryID {
			return category, true
		}
	}
	return model.ProjectCategory{}, false
}

func (m Model) projectCategoryByName(name string) (model.ProjectCategory, bool) {
	name = strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(name)), " "))
	if name == "" {
		return model.ProjectCategory{}, false
	}
	for _, category := range m.projectCategories {
		key := strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(category.Name)), " "))
		if key == name {
			return category, true
		}
	}
	return model.ProjectCategory{}, false
}

func (m *Model) applySectionToggle(label string, mode commands.ToggleMode, target *bool) {
	switch mode {
	case commands.ToggleOn:
		*target = true
	case commands.ToggleOff:
		*target = false
	default:
		*target = !*target
	}
	m.syncDetailViewport(false)
	if *target {
		m.status = label + " section shown"
		return
	}
	m.status = label + " section hidden"
}

func (m *Model) openCommandMode() {
	m.commandMode = true
	m.showHelp = false
	m.commandSelected = 0
	m.commandInput.Focus()
	m.commandInput.SetValue("/")
	m.commandInput.CursorEnd()
	m.syncCommandInputWidth()
	m.syncCommandSelection()
	m.err = nil
	m.status = "Command palette open"
}

func (m *Model) closeCommandMode(status string) {
	m.commandMode = false
	m.commandSelected = 0
	m.commandInput.Blur()
	if status != "" {
		m.status = status
	}
}

func (m *Model) syncCommandInputWidth() {
	width := m.width
	if width <= 0 {
		width = 120
	}
	m.commandInput.Width = max(12, min(72, width-12))
}

func (m Model) commandSuggestions() []commands.Suggestion {
	return commands.SuggestionsWithCategories(m.commandInput.Value(), m.projectCategoryNames())
}

func (m Model) projectCategoryNames() []string {
	names := make([]string, 0, len(m.projectCategories))
	for _, category := range m.projectCategories {
		name := strings.TrimSpace(category.Name)
		if name == "" {
			continue
		}
		names = append(names, name)
	}
	return names
}

func (m *Model) syncCommandSelection() {
	suggestions := m.commandSuggestions()
	if len(suggestions) == 0 {
		m.commandSelected = 0
		return
	}
	if m.commandSelected < 0 {
		m.commandSelected = 0
	}
	if m.commandSelected >= len(suggestions) {
		m.commandSelected = len(suggestions) - 1
	}
}

func (m *Model) moveCommandSelection(delta int) {
	suggestions := m.commandSuggestions()
	if len(suggestions) == 0 || delta == 0 {
		return
	}
	m.commandSelected += delta
	if m.commandSelected < 0 {
		m.commandSelected = len(suggestions) - 1
	}
	if m.commandSelected >= len(suggestions) {
		m.commandSelected = 0
	}
}

func (m Model) selectedCommandSuggestion() (commands.Suggestion, bool) {
	suggestions := m.commandSuggestions()
	if len(suggestions) == 0 {
		return commands.Suggestion{}, false
	}
	index := m.commandSelected
	if index < 0 {
		index = 0
	}
	if index >= len(suggestions) {
		index = len(suggestions) - 1
	}
	return suggestions[index], true
}

func (m *Model) applySelectedCommandSuggestion() bool {
	suggestion, ok := m.selectedCommandSuggestion()
	if !ok {
		return false
	}
	m.commandInput.SetValue(suggestion.Insert)
	m.commandInput.CursorEnd()
	m.syncCommandSelection()
	return true
}

func (m Model) resolvedCommandInput() string {
	raw := strings.TrimSpace(m.commandInput.Value())
	if raw == "" {
		return raw
	}
	suggestion, ok := m.selectedCommandSuggestion()
	if ok {
		insert := strings.TrimSpace(suggestion.Insert)
		if strings.HasPrefix(strings.ToLower(insert), strings.ToLower(raw)) && !strings.EqualFold(insert, raw) {
			return suggestion.Insert
		}
	}
	if _, err := commands.Parse(raw); err == nil {
		return raw
	}
	if !ok {
		return raw
	}
	if strings.HasPrefix(strings.ToLower(suggestion.Insert), strings.ToLower(raw)) {
		return suggestion.Insert
	}
	return raw
}
