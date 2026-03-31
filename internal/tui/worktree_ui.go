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
	Kind              projectListRowKind
	ProjectPath       string
	RootPath          string
	LinkedCount       int
	LinkedActiveCount int
	LinkedDirtyCount  int
	Expanded          bool
}

type worktreeRemoveConfirmState struct {
	ProjectPath  string
	RootPath     string
	ProjectName  string
	BranchName   string
	TargetBranch string
	MergeStatus  model.WorktreeMergeStatus
	Selected     int
}

type worktreeMergeConfirmState struct {
	ProjectPath  string
	RootPath     string
	ProjectName  string
	BranchName   string
	TargetBranch string
	MergeReady   bool
	BlockReason  string
	ErrorMessage string
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

func worktreeMergeStatusSummary(project model.ProjectSummary) string {
	targetBranch := strings.TrimSpace(project.WorktreeParentBranch)
	switch project.WorktreeMergeStatus {
	case model.WorktreeMergeStatusMerged:
		if targetBranch != "" {
			return "merged into " + targetBranch
		}
		return "merged"
	case model.WorktreeMergeStatusNotMerged:
		if targetBranch != "" {
			return "not merged into " + targetBranch
		}
		return "not merged"
	default:
		if targetBranch != "" {
			return "merge status unavailable for " + targetBranch
		}
		return "merge status unavailable"
	}
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
	filtered = expandVisibleWorktreeFamilies(filtered, sorted)
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
		activeCount, dirtyCount := m.worktreeActivityCounts(children)
		expanded := m.isWorktreeGroupExpanded(rootPath, children, selectedPath)

		rows = append(rows, rootProject)
		meta = append(meta, projectListRow{
			Kind:              projectListRowRepo,
			ProjectPath:       rootProject.Path,
			RootPath:          rootPath,
			LinkedCount:       len(children),
			LinkedActiveCount: activeCount,
			LinkedDirtyCount:  dirtyCount,
			Expanded:          expanded,
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

func worktreeLinkedBadgeSummary(linked, active, dirty int) string {
	if linked <= 0 {
		return ""
	}
	parts := []string{fmt.Sprintf("%d linked", linked)}
	if active > 0 {
		parts = append(parts, fmt.Sprintf("%d active", active))
	} else if dirty > 0 {
		parts = append(parts, fmt.Sprintf("%d dirty", dirty))
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func worktreeGroupSummary(projects []model.ProjectSummary, active, dirty int) string {
	rootCount := 0
	for _, project := range projects {
		if projectIsWorktreeRoot(project) {
			rootCount++
		}
	}
	linkedCount := len(projects) - rootCount

	parts := make([]string, 0, 3)
	switch {
	case rootCount > 0 && linkedCount > 0:
		parts = append(parts, fmt.Sprintf("root + %d linked", linkedCount))
	case linkedCount > 0:
		parts = append(parts, fmt.Sprintf("%d linked", linkedCount))
	default:
		parts = append(parts, "root only")
	}
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
	if row.Kind == projectListRowWorktree && m.canRemoveWorktree(project) {
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

func (m Model) canRemoveWorktree(project model.ProjectSummary) bool {
	if project.WorktreeKind != model.WorktreeKindLinked {
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

func (m Model) worktreeActionHints(project model.ProjectSummary, family []model.ProjectSummary) []string {
	hints := make([]string, 0, 4)
	if len(family) > 1 || project.WorktreeKind == model.WorktreeKindLinked {
		hints = append(hints, "w or /wt lanes")
	}
	if m.canMergeWorktreeBack(project) {
		hints = append(hints, "M or /wt merge")
	}
	if m.canRemoveWorktree(project) {
		hints = append(hints, "x or /wt remove")
	}
	if len(family) > 1 {
		hints = append(hints, "P or /wt prune")
	}
	return hints
}

func (m Model) worktreeCommandPaletteHint(project model.ProjectSummary, family []model.ProjectSummary) string {
	hints := m.worktreeActionHints(project, family)
	if len(hints) == 0 {
		return ""
	}
	commands := make([]string, 0, len(hints))
	for _, hint := range hints {
		if slashIndex := strings.Index(hint, "/wt "); slashIndex >= 0 {
			commands = append(commands, hint[slashIndex:])
		}
	}
	if len(commands) == 0 {
		return ""
	}
	return "Worktrees: try " + strings.Join(commands, ", ") + "."
}

func (m Model) mergeBackRulesSummary() string {
	return "Requires a clean source worktree and clean root checkout. Sibling worktrees can stay dirty."
}

func (m Model) worktreeMergeReadiness(project model.ProjectSummary, rootPath string) (bool, string) {
	if project.RepoConflict {
		return false, "This worktree has unresolved conflicts. Resolve or abort the in-progress Git operation before merging back."
	}
	if project.RepoDirty {
		return false, "This worktree is dirty. Commit or discard changes before merging back."
	}
	targetBranch := strings.TrimSpace(project.WorktreeParentBranch)
	if rootPath == "" || targetBranch == "" {
		return true, ""
	}
	rootProject, ok := m.projectSummaryByPath(rootPath)
	if !ok {
		return true, ""
	}
	if rootProject.RepoConflict {
		return false, "The root checkout has unresolved conflicts. Resolve or abort the in-progress Git operation before retrying."
	}
	if rootProject.RepoDirty {
		return false, "The root checkout is dirty. Commit or discard changes before merging back."
	}
	rootBranch := strings.TrimSpace(rootProject.RepoBranch)
	if rootBranch != "" && rootBranch != targetBranch {
		return false, fmt.Sprintf("The root checkout is on %s. Switch it to %s before merging back.", rootBranch, targetBranch)
	}
	return true, ""
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
	mergeReady, blockReason := m.worktreeMergeReadiness(project, row.RootPath)
	m.worktreeMergeConfirm = &worktreeMergeConfirmState{
		ProjectPath:  project.Path,
		RootPath:     row.RootPath,
		ProjectName:  project.Name,
		BranchName:   projectWorktreeLabel(project),
		TargetBranch: strings.TrimSpace(project.WorktreeParentBranch),
		MergeReady:   mergeReady,
		BlockReason:  blockReason,
		Selected:     worktreeMergeConfirmFocusKeep,
	}
	if mergeReady {
		m.status = "Confirm worktree merge-back"
	} else {
		m.status = blockReason
	}
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
		if !confirm.MergeReady {
			confirm.Selected = worktreeMergeConfirmFocusKeep
			return m, nil
		}
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
		if !confirm.MergeReady {
			if strings.TrimSpace(confirm.ErrorMessage) != "" {
				m.status = confirm.ErrorMessage
			} else {
				m.status = confirm.BlockReason
			}
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
		if result.AlreadyMerged {
			status = fmt.Sprintf("%s is already merged into %s", result.SourceBranch, result.TargetBranch)
		}
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
		ProjectPath:  project.Path,
		RootPath:     row.RootPath,
		ProjectName:  project.Name,
		BranchName:   projectWorktreeLabel(project),
		TargetBranch: strings.TrimSpace(project.WorktreeParentBranch),
		MergeStatus:  project.WorktreeMergeStatus,
		Selected:     worktreeRemoveConfirmFocusKeep,
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
	}
	if statusHeader, statusBody, statusStyle := worktreeRemoveSafetyCopy(confirm.MergeStatus, confirm.TargetBranch); statusHeader != "" || statusBody != "" {
		lines = append(lines, "")
		if statusHeader != "" {
			lines = append(lines, statusStyle.Render(statusHeader))
		}
		if statusBody != "" {
			lines = append(lines, renderWrappedDialogTextLines(statusStyle, panelInnerW, statusBody)...)
		}
	}
	lines = append(lines, "")
	lines = append(lines, renderWrappedDialogTextLines(detailMutedStyle, panelInnerW, "Removing a linked worktree deletes the checkout only. The branch ref stays in the repo.")...)
	lines = append(lines, "", buttons)
	panel := renderDialogPanel(panelW, panelInnerW, strings.Join(lines, "\n"))
	left := max(0, (bodyW-panelW)/2)
	top := max(0, (bodyH-lipgloss.Height(panel))/2)
	return overlayBlock(body, panel, bodyW, bodyH, left, top)
}

func worktreeRemoveSafetyCopy(status model.WorktreeMergeStatus, targetBranch string) (string, string, lipgloss.Style) {
	targetBranch = strings.TrimSpace(targetBranch)
	switch status {
	case model.WorktreeMergeStatusMerged:
		if targetBranch != "" {
			return "Merged", "This worktree branch is already merged into " + targetBranch + ".", detailValueStyle
		}
		return "Merged", "This worktree branch is already merged back.", detailValueStyle
	case model.WorktreeMergeStatusNotMerged:
		if targetBranch != "" {
			return "Not merged yet", "This worktree branch is not yet merged into " + targetBranch + ". You can still remove the checkout, but you may lose track of unmerged work.", detailWarningStyle
		}
		return "Not merged yet", "This worktree branch is not yet merged back. You can still remove the checkout, but you may lose track of unmerged work.", detailWarningStyle
	default:
		if targetBranch != "" {
			return "Merge status unavailable", "Little Control Room could not confirm whether this branch is already merged into " + targetBranch + ".", detailMutedStyle
		}
		return "Merge status unavailable", "Little Control Room could not confirm whether this branch is already merged back.", detailMutedStyle
	}
}

func (m Model) renderWorktreeMergeConfirmOverlay(body string, bodyW, bodyH int) string {
	confirm := m.worktreeMergeConfirm
	if confirm == nil {
		return body
	}
	panelW := min(max(54, bodyW-24), 82)
	panelInnerW := max(28, panelW-4)
	mergeButton := renderNoteDialogButton("Merge", confirm.Selected == worktreeMergeConfirmFocusMerge)
	if !confirm.MergeReady {
		mergeButton = disabledActionTextStyle.Render("[Merge blocked]")
	}
	buttons := lipgloss.JoinHorizontal(
		lipgloss.Left,
		mergeButton,
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
	}
	if strings.TrimSpace(confirm.BlockReason) != "" {
		lines = append(lines, "", detailWarningStyle.Render("Merge blocked"))
		lines = append(lines, renderWrappedDialogTextLines(detailWarningStyle, panelInnerW, confirm.BlockReason)...)
	}
	if strings.TrimSpace(confirm.ErrorMessage) != "" {
		headerStyle := detailDangerStyle
		headerText := "Merge error"
		if worktreeMergeConflictMessage(confirm.ErrorMessage) {
			headerStyle = detailConflictStyle
			headerText = "Merge Conflict"
		}
		lines = append(lines, "", headerStyle.Render(headerText))
		lines = append(lines, renderWrappedDialogTextLines(headerStyle, panelInnerW, confirm.ErrorMessage)...)
	}
	lines = append(lines, "", buttons)
	panel := renderDialogPanel(panelW, panelInnerW, strings.Join(lines, "\n"))
	left := max(0, (bodyW-panelW)/2)
	top := max(0, (bodyH-lipgloss.Height(panel))/2)
	return overlayBlock(body, panel, bodyW, bodyH, left, top)
}

func worktreeMergeConflictMessage(text string) bool {
	return strings.HasPrefix(strings.TrimSpace(strings.ToLower(text)), "merge conflict while merging ")
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
