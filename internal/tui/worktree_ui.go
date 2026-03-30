package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	"lcroom/internal/model"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type projectListRowKind string

const (
	projectListRowStandalone projectListRowKind = "standalone"
	projectListRowRepo       projectListRowKind = "repo"
	projectListRowWorktree   projectListRowKind = "worktree"
)

const (
	worktreeMergeConfirmFocusMerge = iota
	worktreeMergeConfirmFocusKeep
)

const (
	worktreePostMergeFocusRemove = iota
	worktreePostMergeFocusKeep
)

const (
	worktreeRemoveConfirmFocusRemove = iota
	worktreeRemoveConfirmFocusKeep
)

type projectListRow struct {
	Kind        projectListRowKind
	ProjectPath string
	RootPath    string
	LinkedCount int
	ActiveCount int
	DirtyCount  int
	Expanded    bool
}

type worktreeRemoveConfirmState struct {
	ProjectPath string
	RootPath    string
	ProjectName string
	BranchName  string
	Selected    int
}

type worktreeMergeConfirmState struct {
	ProjectPath  string
	RootPath     string
	ProjectName  string
	BranchName   string
	TargetBranch string
	Selected     int
}

type worktreePostMergeState struct {
	ProjectPath  string
	RootPath     string
	BranchName   string
	TargetBranch string
	Status       string
	Selected     int
}

func projectWorktreeRootPath(project model.ProjectSummary) string {
	rootPath := filepath.Clean(strings.TrimSpace(project.WorktreeRootPath))
	if rootPath != "" && rootPath != "." {
		return rootPath
	}
	return filepath.Clean(strings.TrimSpace(project.Path))
}

func projectIsWorktreeRoot(project model.ProjectSummary) bool {
	path := filepath.Clean(strings.TrimSpace(project.Path))
	rootPath := projectWorktreeRootPath(project)
	return path != "" && path == rootPath
}

func projectWorktreeLabel(project model.ProjectSummary) string {
	if branch := strings.TrimSpace(project.RepoBranch); branch != "" {
		return branch
	}
	name := filepath.Base(strings.TrimSpace(project.Path))
	if name == "" || name == "." || name == string(filepath.Separator) {
		return "worktree"
	}
	return name
}

func (m Model) selectedProjectRow() (projectListRow, model.ProjectSummary, bool) {
	project, ok := m.selectedProject()
	if !ok {
		return projectListRow{}, model.ProjectSummary{}, false
	}
	if m.selected >= 0 && m.selected < len(m.projectRows) {
		return m.projectRows[m.selected], project, true
	}
	return projectListRow{
		Kind:        projectListRowStandalone,
		ProjectPath: project.Path,
		RootPath:    projectWorktreeRootPath(project),
	}, project, true
}

func (m Model) worktreeFamily(rootPath string) []model.ProjectSummary {
	rootPath = filepath.Clean(strings.TrimSpace(rootPath))
	if rootPath == "" {
		return nil
	}
	out := make([]model.ProjectSummary, 0, 4)
	for _, project := range m.allProjects {
		if projectWorktreeRootPath(project) == rootPath {
			out = append(out, project)
		}
	}
	return out
}

func (m Model) existingWorktreeCandidates(projectPath string) []model.ProjectSummary {
	project, ok := m.projectSummaryByPath(projectPath)
	if !ok {
		return nil
	}
	rootPath := projectWorktreeRootPath(project)
	family := m.worktreeFamily(rootPath)
	out := make([]model.ProjectSummary, 0, len(family))
	for _, candidate := range family {
		if filepath.Clean(candidate.Path) == filepath.Clean(projectPath) {
			continue
		}
		out = append(out, candidate)
	}
	return out
}

func (m *Model) rebuildProjectList(selectedPath string) {
	sorted := append([]model.ProjectSummary(nil), m.allProjects...)
	m.sortProjects(sorted)
	filtered := filterProjects(sorted, m.visibility, m.excludeProjectPatterns, m.projectFilter)
	if m.privacyMode {
		filtered = filterProjectsByPrivacy(filtered, m.privacyPatterns)
	}
	m.projects, m.projectRows = m.buildProjectRows(filtered, selectedPath)
	if len(m.projects) == 0 {
		m.selected = 0
		m.offset = 0
		return
	}
	preservedSelection := false
	if selectedPath != "" {
		if idx := m.indexByPath(selectedPath); idx >= 0 {
			m.selected = idx
			preservedSelection = true
		}
	}
	if selectedPath != "" && !preservedSelection {
		m.selected = 0
	}
	if m.selected >= len(m.projects) {
		m.selected = max(0, len(m.projects)-1)
	}
	m.ensureSelectionVisible()
}

func (m Model) buildProjectRows(projects []model.ProjectSummary, selectedPath string) ([]model.ProjectSummary, []projectListRow) {
	type group struct {
		rootPath string
		members  []model.ProjectSummary
	}
	order := []string{}
	groups := map[string]*group{}
	for _, project := range projects {
		rootPath := projectWorktreeRootPath(project)
		if _, ok := groups[rootPath]; !ok {
			order = append(order, rootPath)
			groups[rootPath] = &group{rootPath: rootPath}
		}
		groups[rootPath].members = append(groups[rootPath].members, project)
	}

	rows := make([]model.ProjectSummary, 0, len(projects))
	meta := make([]projectListRow, 0, len(projects))
	for _, rootPath := range order {
		group := groups[rootPath]
		if group == nil || len(group.members) == 0 {
			continue
		}
		rootIndex := -1
		for i, project := range group.members {
			if projectIsWorktreeRoot(project) {
				rootIndex = i
				break
			}
		}
		if rootIndex < 0 || len(group.members) == 1 {
			for _, project := range group.members {
				rows = append(rows, project)
				meta = append(meta, projectListRow{
					Kind:        projectListRowStandalone,
					ProjectPath: project.Path,
					RootPath:    rootPath,
				})
			}
			continue
		}

		rootProject := group.members[rootIndex]
		children := make([]model.ProjectSummary, 0, len(group.members)-1)
		for i, project := range group.members {
			if i == rootIndex {
				continue
			}
			children = append(children, project)
		}
		allMembers := append([]model.ProjectSummary{rootProject}, children...)
		activeCount, dirtyCount := m.worktreeActivityCounts(allMembers)
		expanded := m.isWorktreeGroupExpanded(rootPath, children, selectedPath)

		rows = append(rows, rootProject)
		meta = append(meta, projectListRow{
			Kind:        projectListRowRepo,
			ProjectPath: rootProject.Path,
			RootPath:    rootPath,
			LinkedCount: len(children),
			ActiveCount: activeCount,
			DirtyCount:  dirtyCount,
			Expanded:    expanded,
		})
		if !expanded {
			continue
		}
		for _, child := range children {
			rows = append(rows, child)
			meta = append(meta, projectListRow{
				Kind:        projectListRowWorktree,
				ProjectPath: child.Path,
				RootPath:    rootPath,
			})
		}
	}
	return rows, meta
}

func (m Model) worktreeActivityCounts(projects []model.ProjectSummary) (int, int) {
	active := 0
	dirty := 0
	for _, project := range projects {
		if project.RepoDirty {
			dirty++
		}
		if project.Status != model.StatusIdle || m.projectHasLiveCodexSession(project.Path) || m.projectRuntimeSnapshot(project.Path).Running {
			active++
		}
	}
	return active, dirty
}

func (m Model) isWorktreeGroupExpanded(rootPath string, children []model.ProjectSummary, selectedPath string) bool {
	if m.worktreeExpanded != nil {
		if expanded, ok := m.worktreeExpanded[rootPath]; ok {
			return expanded
		}
	}
	selectedPath = filepath.Clean(strings.TrimSpace(selectedPath))
	for _, child := range children {
		if filepath.Clean(child.Path) == selectedPath {
			return true
		}
		if child.RepoDirty || child.Status != model.StatusIdle || m.projectHasLiveCodexSession(child.Path) || m.projectRuntimeSnapshot(child.Path).Running {
			return true
		}
	}
	return false
}

func (m *Model) toggleSelectedWorktreeGroup() tea.Cmd {
	row, project, ok := m.selectedProjectRow()
	if !ok {
		m.status = "No project selected"
		return nil
	}
	if row.RootPath == "" || row.LinkedCount == 0 && row.Kind != projectListRowWorktree {
		m.status = "No sibling worktrees to show"
		return nil
	}
	rootPath := row.RootPath
	if rootPath == "" {
		rootPath = projectWorktreeRootPath(project)
	}
	children := make([]model.ProjectSummary, 0, 4)
	for _, member := range m.worktreeFamily(rootPath) {
		if filepath.Clean(member.Path) == filepath.Clean(rootPath) {
			continue
		}
		children = append(children, member)
	}
	if m.worktreeExpanded == nil {
		m.worktreeExpanded = map[string]bool{}
	}
	next := !m.isWorktreeGroupExpanded(rootPath, children, project.Path)
	if row.Kind == projectListRowWorktree && !next {
		m.worktreeExpanded[rootPath] = false
		m.rebuildProjectList(rootPath)
		m.status = "Worktrees collapsed"
		return m.loadDetailCmd(rootPath)
	}
	m.worktreeExpanded[rootPath] = next
	m.rebuildProjectList(project.Path)
	if next {
		m.status = "Worktrees expanded"
	} else {
		m.status = "Worktrees collapsed"
	}
	if selected, ok := m.selectedProject(); ok {
		return m.loadDetailCmd(selected.Path)
	}
	return nil
}

func worktreeGroupSummary(total, active, dirty int) string {
	parts := []string{fmt.Sprintf("%d worktrees", total)}
	if active > 0 {
		parts = append(parts, fmt.Sprintf("%d active", active))
	}
	if dirty > 0 {
		parts = append(parts, fmt.Sprintf("%d dirty", dirty))
	}
	return strings.Join(parts, ", ")
}

func (m Model) worktreeFooterActions(width int) []footerAction {
	if width < 60 {
		return nil
	}
	row, project, ok := m.selectedProjectRow()
	if !ok {
		return nil
	}
	rootPath := row.RootPath
	if rootPath == "" {
		rootPath = projectWorktreeRootPath(project)
	}
	family := m.worktreeFamily(rootPath)
	if len(family) <= 1 && row.Kind != projectListRowWorktree && row.LinkedCount == 0 {
		return nil
	}

	actions := make([]footerAction, 0, 3)
	if len(family) > 1 || row.LinkedCount > 0 || row.Kind == projectListRowWorktree {
		actions = append(actions, footerNavAction("w", "lanes"))
	}
	if row.Kind == projectListRowWorktree && m.canMergeWorktreeBack(project) && width >= 80 {
		actions = append(actions, footerPrimaryAction("M", "merge"))
	}
	if row.Kind == projectListRowWorktree &&
		project.WorktreeKind == model.WorktreeKindLinked &&
		!m.projectHasLiveCodexSession(project.Path) &&
		!m.projectRuntimeSnapshot(project.Path).Running {
		actions = append(actions, footerHideAction("x", "remove"))
	}
	if width >= 90 && len(family) > 1 {
		actions = append(actions, footerLowAction("P", "prune"))
	}
	return actions
}

func (m Model) canMergeWorktreeBack(project model.ProjectSummary) bool {
	if project.WorktreeKind != model.WorktreeKindLinked {
		return false
	}
	targetBranch := strings.TrimSpace(project.WorktreeParentBranch)
	sourceBranch := strings.TrimSpace(project.RepoBranch)
	if targetBranch == "" || sourceBranch == "" || sourceBranch == targetBranch {
		return false
	}
	if m.projectHasLiveCodexSession(project.Path) {
		return false
	}
	if snapshot := m.projectRuntimeSnapshot(project.Path); snapshot.Running {
		return false
	}
	return true
}

func (m Model) mergeBackRulesSummary() string {
	return "Requires a clean source worktree and clean root checkout. Sibling worktrees can stay dirty."
}

func (m *Model) openWorktreeMergeConfirmForSelection() tea.Cmd {
	row, project, ok := m.selectedProjectRow()
	if !ok {
		m.status = "No project selected"
		return nil
	}
	if row.Kind != projectListRowWorktree || project.WorktreeKind != model.WorktreeKindLinked {
		m.status = "Select a linked worktree to merge it back"
		return nil
	}
	if strings.TrimSpace(project.WorktreeParentBranch) == "" {
		m.status = "This worktree has no recorded parent branch yet"
		return nil
	}
	if strings.TrimSpace(project.RepoBranch) == "" {
		m.status = "This worktree branch is unavailable right now"
		return nil
	}
	if strings.TrimSpace(project.RepoBranch) == strings.TrimSpace(project.WorktreeParentBranch) {
		m.status = "This worktree is already on its parent branch"
		return nil
	}
	if m.projectHasLiveCodexSession(project.Path) {
		m.status = "Close the embedded agent session before merging this worktree back"
		return nil
	}
	if snapshot := m.projectRuntimeSnapshot(project.Path); snapshot.Running {
		m.status = "Stop the runtime before merging this worktree back"
		return nil
	}
	m.worktreeMergeConfirm = &worktreeMergeConfirmState{
		ProjectPath:  project.Path,
		RootPath:     row.RootPath,
		ProjectName:  project.Name,
		BranchName:   projectWorktreeLabel(project),
		TargetBranch: strings.TrimSpace(project.WorktreeParentBranch),
		Selected:     worktreeMergeConfirmFocusKeep,
	}
	m.status = "Confirm worktree merge-back"
	return nil
}

func (m Model) updateWorktreeMergeConfirmMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	confirm := m.worktreeMergeConfirm
	if confirm == nil {
		return m, nil
	}
	switch msg.String() {
	case "esc":
		m.worktreeMergeConfirm = nil
		m.status = "Worktree merge-back canceled"
		return m, nil
	case "left", "h", "right", "l", "tab", "shift+tab":
		if confirm.Selected == worktreeMergeConfirmFocusMerge {
			confirm.Selected = worktreeMergeConfirmFocusKeep
		} else {
			confirm.Selected = worktreeMergeConfirmFocusMerge
		}
		return m, nil
	case "enter":
		if confirm.Selected != worktreeMergeConfirmFocusMerge {
			m.worktreeMergeConfirm = nil
			m.status = "Worktree merge-back canceled"
			return m, nil
		}
		m.status = "Merging worktree back..."
		return m, m.mergeWorktreeBackCmd(confirm.ProjectPath)
	}
	return m, nil
}

func (m Model) mergeWorktreeBackCmd(projectPath string) tea.Cmd {
	if m.svc == nil {
		return func() tea.Msg {
			return worktreeActionMsg{projectPath: projectPath, err: fmt.Errorf("service unavailable")}
		}
	}
	return func() tea.Msg {
		result, err := m.svc.MergeWorktreeBack(m.ctx, projectPath)
		if err != nil {
			return worktreeActionMsg{
				projectPath: projectPath,
				err:         err,
			}
		}
		status := fmt.Sprintf("Merged %s into %s", result.SourceBranch, result.TargetBranch)
		return worktreeActionMsg{
			projectPath:           projectPath,
			selectPath:            result.RootProjectPath,
			status:                status,
			offerPostMergeCleanup: true,
			postMergeRootPath:     result.RootProjectPath,
			postMergeSourceBranch: result.SourceBranch,
			postMergeTargetBranch: result.TargetBranch,
		}
	}
}

func (m Model) updateWorktreePostMergeMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	prompt := m.worktreePostMerge
	if prompt == nil {
		return m, nil
	}
	switch msg.String() {
	case "esc":
		m.worktreePostMerge = nil
		if strings.TrimSpace(prompt.Status) != "" {
			m.status = prompt.Status + ". Worktree kept."
		} else {
			m.status = "Merged worktree kept"
		}
		return m, nil
	case "left", "h", "right", "l", "tab", "shift+tab":
		if prompt.Selected == worktreePostMergeFocusRemove {
			prompt.Selected = worktreePostMergeFocusKeep
		} else {
			prompt.Selected = worktreePostMergeFocusRemove
		}
		return m, nil
	case "enter":
		if prompt.Selected != worktreePostMergeFocusRemove {
			m.worktreePostMerge = nil
			if strings.TrimSpace(prompt.Status) != "" {
				m.status = prompt.Status + ". Worktree kept."
			} else {
				m.status = "Merged worktree kept"
			}
			return m, nil
		}
		m.status = "Removing merged worktree..."
		return m, m.removeWorktreeCmd(prompt.ProjectPath, prompt.RootPath)
	}
	return m, nil
}

func (m *Model) openWorktreeRemoveConfirmForSelection() tea.Cmd {
	row, project, ok := m.selectedProjectRow()
	if !ok {
		m.status = "No project selected"
		return nil
	}
	if row.Kind != projectListRowWorktree || project.WorktreeKind != model.WorktreeKindLinked {
		m.status = "Select a linked worktree to remove it"
		return nil
	}
	if m.projectHasLiveCodexSession(project.Path) {
		m.status = "Close the embedded agent session before removing this worktree"
		return nil
	}
	if snapshot := m.projectRuntimeSnapshot(project.Path); snapshot.Running {
		m.status = "Stop the runtime before removing this worktree"
		return nil
	}
	m.worktreeRemoveConfirm = &worktreeRemoveConfirmState{
		ProjectPath: project.Path,
		RootPath:    row.RootPath,
		ProjectName: project.Name,
		BranchName:  projectWorktreeLabel(project),
		Selected:    worktreeRemoveConfirmFocusKeep,
	}
	m.status = "Confirm worktree removal"
	return nil
}

func (m Model) updateWorktreeRemoveConfirmMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	confirm := m.worktreeRemoveConfirm
	if confirm == nil {
		return m, nil
	}
	switch msg.String() {
	case "esc":
		m.worktreeRemoveConfirm = nil
		m.status = "Worktree removal canceled"
		return m, nil
	case "left", "h", "right", "l", "tab", "shift+tab":
		if confirm.Selected == worktreeRemoveConfirmFocusRemove {
			confirm.Selected = worktreeRemoveConfirmFocusKeep
		} else {
			confirm.Selected = worktreeRemoveConfirmFocusRemove
		}
		return m, nil
	case "enter":
		if confirm.Selected != worktreeRemoveConfirmFocusRemove {
			m.worktreeRemoveConfirm = nil
			m.status = "Worktree removal canceled"
			return m, nil
		}
		m.status = "Removing worktree..."
		return m, m.removeWorktreeCmd(confirm.ProjectPath, confirm.RootPath)
	}
	return m, nil
}

func (m Model) removeWorktreeCmd(projectPath, rootPath string) tea.Cmd {
	if m.svc == nil {
		return func() tea.Msg {
			return worktreeActionMsg{projectPath: projectPath, err: fmt.Errorf("service unavailable")}
		}
	}
	return func() tea.Msg {
		err := m.svc.RemoveWorktree(m.ctx, projectPath)
		return worktreeActionMsg{
			projectPath: projectPath,
			selectPath:  rootPath,
			status:      "Worktree removed",
			err:         err,
		}
	}
}

func (m Model) pruneWorktreesCmd(projectPath, selectPath string) tea.Cmd {
	if m.svc == nil {
		return func() tea.Msg {
			return worktreeActionMsg{projectPath: projectPath, err: fmt.Errorf("service unavailable")}
		}
	}
	return func() tea.Msg {
		err := m.svc.PruneWorktrees(m.ctx, projectPath)
		return worktreeActionMsg{
			projectPath: projectPath,
			selectPath:  selectPath,
			status:      "Pruned stale git worktrees",
			err:         err,
		}
	}
}

func (m Model) renderWorktreeRemoveConfirmOverlay(body string, bodyW, bodyH int) string {
	confirm := m.worktreeRemoveConfirm
	if confirm == nil {
		return body
	}
	panelW := min(max(48, bodyW-24), 78)
	panelInnerW := max(24, panelW-4)
	buttons := lipgloss.JoinHorizontal(
		lipgloss.Left,
		renderNoteDialogButton("Remove", confirm.Selected == worktreeRemoveConfirmFocusRemove),
		" ",
		renderNoteDialogButton("Keep", confirm.Selected == worktreeRemoveConfirmFocusKeep),
	)
	lines := []string{
		detailSectionStyle.Render("Remove worktree"),
		"",
		detailValueStyle.Render(truncateText(confirm.BranchName, panelInnerW)),
		detailMutedStyle.Render(truncateText(confirm.ProjectPath, panelInnerW)),
		"",
		buttons,
	}
	panel := renderDialogPanel(panelW, panelInnerW, strings.Join(lines, "\n"))
	left := max(0, (bodyW-panelW)/2)
	top := max(0, (bodyH-lipgloss.Height(panel))/2)
	return overlayBlock(body, panel, bodyW, bodyH, left, top)
}

func (m Model) renderWorktreeMergeConfirmOverlay(body string, bodyW, bodyH int) string {
	confirm := m.worktreeMergeConfirm
	if confirm == nil {
		return body
	}
	panelW := min(max(54, bodyW-24), 82)
	panelInnerW := max(28, panelW-4)
	buttons := lipgloss.JoinHorizontal(
		lipgloss.Left,
		renderNoteDialogButton("Merge", confirm.Selected == worktreeMergeConfirmFocusMerge),
		" ",
		renderNoteDialogButton("Keep", confirm.Selected == worktreeMergeConfirmFocusKeep),
	)
	lines := []string{
		detailSectionStyle.Render("Merge worktree back"),
		"",
		detailValueStyle.Render(truncateText(confirm.BranchName+" -> "+confirm.TargetBranch, panelInnerW)),
		detailMutedStyle.Render(truncateText(confirm.ProjectPath, panelInnerW)),
		"",
		detailMutedStyle.Render(truncateText("Root must already be on "+confirm.TargetBranch+".", panelInnerW)),
		detailMutedStyle.Render(truncateText(m.mergeBackRulesSummary(), panelInnerW)),
		"",
		buttons,
	}
	panel := renderDialogPanel(panelW, panelInnerW, strings.Join(lines, "\n"))
	left := max(0, (bodyW-panelW)/2)
	top := max(0, (bodyH-lipgloss.Height(panel))/2)
	return overlayBlock(body, panel, bodyW, bodyH, left, top)
}

func (m Model) renderWorktreePostMergeOverlay(body string, bodyW, bodyH int) string {
	prompt := m.worktreePostMerge
	if prompt == nil {
		return body
	}
	panelW := min(max(54, bodyW-24), 82)
	panelInnerW := max(28, panelW-4)
	buttons := lipgloss.JoinHorizontal(
		lipgloss.Left,
		renderNoteDialogButton("Remove", prompt.Selected == worktreePostMergeFocusRemove),
		" ",
		renderNoteDialogButton("Keep", prompt.Selected == worktreePostMergeFocusKeep),
	)
	statusLine := strings.TrimSpace(prompt.Status)
	if statusLine == "" {
		statusLine = "Worktree merged back"
	}
	lines := []string{
		detailSectionStyle.Render("Merge complete"),
		"",
		detailValueStyle.Render(truncateText(statusLine, panelInnerW)),
		detailMutedStyle.Render(truncateText(prompt.ProjectPath, panelInnerW)),
		"",
		detailMutedStyle.Render(truncateText("Remove this linked worktree now? You can still keep it and remove it later with x.", panelInnerW)),
		"",
		buttons,
	}
	panel := renderDialogPanel(panelW, panelInnerW, strings.Join(lines, "\n"))
	left := max(0, (bodyW-panelW)/2)
	top := max(0, (bodyH-lipgloss.Height(panel))/2)
	return overlayBlock(body, panel, bodyW, bodyH, left, top)
}
