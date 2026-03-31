package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	"lcroom/internal/codexapp"
	"lcroom/internal/model"
	"lcroom/internal/service"

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
	worktreeMergeConfirmFocusCommit
	worktreeMergeConfirmFocusKeep
)

const (
	worktreePostMergeFocusTodo = iota
	worktreePostMergeFocusRemove
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
	Busy         bool
	BusyMessage  string
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
	OfferCommit  bool
	ErrorMessage string
	Busy         bool
	BusyMessage  string
	Selected     int
}

type worktreeMergeReadinessState struct {
	MergeReady  bool
	BlockReason string
	SourceDirty bool
}

type worktreePostMergeState struct {
	ProjectPath  string
	RootPath     string
	BranchName   string
	TargetBranch string
	TodoID       int64
	TodoText     string
	TodoPath     string
	MarkTodoDone bool
	RemoveNow    bool
	Status       string
	ErrorMessage string
	Busy         bool
	BusyTitle    string
	BusyMessage  string
	Selected     int
}

func defaultWorktreePostMergeSelection(hasTodo bool) int {
	if hasTodo {
		return worktreePostMergeFocusTodo
	}
	return worktreePostMergeFocusRemove
}

func worktreePostMergeHasTodo(prompt *worktreePostMergeState) bool {
	return prompt != nil && prompt.TodoID > 0
}

func worktreePostMergeOptionCount(prompt *worktreePostMergeState) int {
	if worktreePostMergeHasTodo(prompt) {
		return 2
	}
	return 1
}

func nextWorktreePostMergeSelection(current, delta, count int) int {
	if count <= 0 {
		return 0
	}
	current = ((current % count) + count) % count
	return (current + delta + count) % count
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

func worktreeMergeSnapshotShowsCompletedTurn(snapshot codexapp.Snapshot) bool {
	statuses := []string{
		normalizedCodexStatus(snapshot.Status),
		normalizedCodexStatus(snapshot.LastSystemNotice),
	}
	for _, status := range statuses {
		status = strings.TrimSpace(status)
		if status == "Turn completed" || strings.HasPrefix(status, "Completed in ") {
			return true
		}
	}
	return false
}

func worktreeMergeCanAutoCloseSnapshot(snapshot codexapp.Snapshot) bool {
	if !snapshot.Started || snapshot.Closed || snapshot.Busy || snapshot.BusyExternal {
		return false
	}
	if snapshot.PendingApproval != nil || snapshot.PendingToolInput != nil || snapshot.PendingElicitation != nil {
		return false
	}
	return worktreeMergeSnapshotShowsCompletedTurn(snapshot)
}

func (m *Model) closeEmbeddedSessionForProject(projectPath string) error {
	projectPath = filepath.Clean(strings.TrimSpace(projectPath))
	if projectPath == "" || m.codexManager == nil {
		return nil
	}
	if err := m.codexManager.CloseProject(projectPath); err != nil {
		return err
	}
	delete(m.codexClosedHandled, projectPath)
	if m.codexVisibleProject == projectPath {
		m.codexVisibleProject = ""
		m.codexInput.Blur()
	}
	if m.codexHiddenProject == projectPath {
		m.codexHiddenProject = ""
	}
	return nil
}

func (m Model) worktreeMergeReadiness(project model.ProjectSummary, rootPath string) worktreeMergeReadinessState {
	if project.RepoConflict {
		return worktreeMergeReadinessState{
			BlockReason: "This worktree has unresolved conflicts. Resolve or abort the in-progress Git operation before merging back.",
		}
	}
	if project.RepoDirty {
		return worktreeMergeReadinessState{
			BlockReason: "This worktree is dirty. Commit or discard changes before merging back.",
			SourceDirty: true,
		}
	}
	targetBranch := strings.TrimSpace(project.WorktreeParentBranch)
	if rootPath == "" || targetBranch == "" {
		return worktreeMergeReadinessState{MergeReady: true}
	}
	rootProject, ok := m.projectSummaryByPath(rootPath)
	if !ok {
		return worktreeMergeReadinessState{MergeReady: true}
	}
	if rootProject.RepoConflict {
		return worktreeMergeReadinessState{
			BlockReason: "The root checkout has unresolved conflicts. Resolve or abort the in-progress Git operation before retrying.",
		}
	}
	if rootProject.RepoDirty {
		return worktreeMergeReadinessState{
			BlockReason: "The root checkout is dirty. Commit or discard changes before merging back.",
		}
	}
	rootBranch := strings.TrimSpace(rootProject.RepoBranch)
	if rootBranch != "" && rootBranch != targetBranch {
		return worktreeMergeReadinessState{
			BlockReason: fmt.Sprintf("The root checkout is on %s. Switch it to %s before merging back.", rootBranch, targetBranch),
		}
	}
	return worktreeMergeReadinessState{MergeReady: true}
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
	if snapshot, ok := m.liveCodexSnapshot(project.Path); ok {
		if worktreeMergeCanAutoCloseSnapshot(snapshot) {
			if err := m.closeEmbeddedSessionForProject(project.Path); err != nil {
				m.err = err
				m.status = "Embedded session action failed"
				return nil
			}
		} else {
			m.showSessionBlockedAttentionDialog(
				project,
				"Merge blocked",
				"Close the embedded agent session before merging this worktree back.",
				"retry the merge",
				embeddedProvider(snapshot),
			)
			return nil
		}
	}
	if snapshot := m.projectRuntimeSnapshot(project.Path); snapshot.Running {
		m.status = "Stop the runtime before merging this worktree back"
		return nil
	}
	readiness := m.worktreeMergeReadiness(project, row.RootPath)
	m.worktreeMergeConfirm = &worktreeMergeConfirmState{
		ProjectPath:  project.Path,
		RootPath:     row.RootPath,
		ProjectName:  project.Name,
		BranchName:   projectWorktreeLabel(project),
		TargetBranch: strings.TrimSpace(project.WorktreeParentBranch),
		MergeReady:   readiness.MergeReady,
		BlockReason:  readiness.BlockReason,
		OfferCommit:  readiness.SourceDirty,
		Selected:     worktreeMergeConfirmFocusKeep,
	}
	if readiness.SourceDirty {
		m.worktreeMergeConfirm.Selected = worktreeMergeConfirmFocusCommit
	}
	if readiness.MergeReady {
		m.status = "Confirm worktree merge-back"
	} else {
		m.status = readiness.BlockReason
	}
	return nil
}

func (m Model) updateWorktreeMergeConfirmMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	confirm := m.worktreeMergeConfirm
	if confirm == nil {
		return m, nil
	}
	if confirm.Busy {
		return m, nil
	}
	switch msg.String() {
	case "esc":
		m.worktreeMergeConfirm = nil
		m.status = "Worktree merge-back canceled"
		return m, nil
	case "left", "h", "right", "l", "tab", "shift+tab":
		if confirm.MergeReady {
			if confirm.Selected == worktreeMergeConfirmFocusMerge {
				confirm.Selected = worktreeMergeConfirmFocusKeep
			} else {
				confirm.Selected = worktreeMergeConfirmFocusMerge
			}
			return m, nil
		}
		if !confirm.OfferCommit {
			confirm.Selected = worktreeMergeConfirmFocusKeep
			return m, nil
		}
		if confirm.Selected == worktreeMergeConfirmFocusCommit {
			confirm.Selected = worktreeMergeConfirmFocusKeep
		} else {
			confirm.Selected = worktreeMergeConfirmFocusCommit
		}
		return m, nil
	case "enter":
		if confirm.MergeReady && confirm.Selected == worktreeMergeConfirmFocusMerge {
			confirm.Busy = true
			confirm.BusyMessage = "Please wait while Git merges this worktree back. The dialog is temporarily locked."
			m.status = "Merging worktree back..."
			return m, m.mergeWorktreeBackCmd(confirm.ProjectPath)
		}
		if !confirm.MergeReady && confirm.OfferCommit && confirm.Selected == worktreeMergeConfirmFocusCommit {
			project, ok := m.projectSummaryByPath(confirm.ProjectPath)
			if !ok {
				m.status = "Worktree is unavailable right now"
				return m, nil
			}
			m.worktreeMergeConfirm = nil
			return m, m.startCommitPreview(project, service.GitActionCommit, "")
		}
		if confirm.Selected == worktreeMergeConfirmFocusKeep {
			m.worktreeMergeConfirm = nil
			m.status = "Worktree merge-back canceled"
			return m, nil
		}
		if strings.TrimSpace(confirm.ErrorMessage) != "" {
			m.status = confirm.ErrorMessage
		} else {
			m.status = confirm.BlockReason
		}
		return m, nil
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
			postMergeTodoID:       result.LinkedTodoID,
			postMergeTodoText:     result.LinkedTodoText,
			postMergeTodoPath:     result.LinkedTodoPath,
		}
	}
}

func (m Model) updateWorktreePostMergeMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	prompt := m.worktreePostMerge
	if prompt == nil {
		return m, nil
	}
	if prompt.Busy {
		return m, nil
	}
	switch msg.String() {
	case "esc":
		m.worktreePostMerge = nil
		m.status = worktreePostMergeDismissStatus(prompt)
		return m, nil
	case "left", "h", "right", "l", "up", "k", "down", "j", "tab", "shift+tab":
		delta := 1
		if msg.String() == "left" || msg.String() == "h" || msg.String() == "up" || msg.String() == "k" || msg.String() == "shift+tab" {
			delta = -1
		}
		prompt.Selected = nextWorktreePostMergeSelection(prompt.Selected, delta, worktreePostMergeOptionCount(prompt))
		return m, nil
	case " ":
		if worktreePostMergeHasTodo(prompt) && prompt.Selected == worktreePostMergeFocusTodo {
			prompt.MarkTodoDone = !prompt.MarkTodoDone
			return m, nil
		}
		prompt.RemoveNow = !prompt.RemoveNow
		return m, nil
	case "enter":
		prompt.ErrorMessage = ""
		if !prompt.MarkTodoDone && !prompt.RemoveNow {
			m.worktreePostMerge = nil
			m.status = worktreePostMergeDismissStatus(prompt)
			return m, nil
		}
		if prompt.MarkTodoDone {
			prompt.Busy = true
			if prompt.RemoveNow {
				prompt.BusyTitle = "Updating TODO and worktree"
				prompt.BusyMessage = "Please wait while Little Control Room marks the linked TODO done and removes the merged worktree checkout. The dialog is temporarily locked."
				m.status = "Marking linked TODO done and removing merged worktree..."
				return m, m.completeWorktreePostMergeTodoCmd(prompt.TodoPath, prompt.TodoID, prompt.ProjectPath, prompt.RootPath, true, prompt.Status)
			}
			prompt.BusyTitle = "Updating TODO"
			prompt.BusyMessage = "Please wait while Little Control Room marks the linked TODO done. The dialog is temporarily locked."
			m.status = "Marking linked TODO done..."
			return m, m.completeWorktreePostMergeTodoCmd(prompt.TodoPath, prompt.TodoID, prompt.ProjectPath, prompt.RootPath, false, prompt.Status)
		}
		prompt.Busy = true
		prompt.BusyTitle = "Removal in progress"
		prompt.BusyMessage = "Please wait while Git removes the merged worktree checkout. The dialog is temporarily locked."
		m.status = "Removing merged worktree..."
		return m, m.removeWorktreeCmd(prompt.ProjectPath, prompt.RootPath)
	}
	return m, nil
}

func worktreePostMergeDismissStatus(prompt *worktreePostMergeState) string {
	if prompt == nil {
		return "Merged worktree kept"
	}
	baseStatus := strings.TrimSpace(prompt.Status)
	if worktreePostMergeHasTodo(prompt) {
		if baseStatus != "" {
			return baseStatus + ". TODO kept open."
		}
		return "Merged worktree kept and linked TODO left open"
	}
	if baseStatus != "" {
		return baseStatus + ". Worktree kept."
	}
	return "Merged worktree kept"
}

func worktreePostMergeOptionLine(prompt *worktreePostMergeState, index, width int) string {
	checked := false
	text := "Remove merged worktree now"
	style := detailMutedStyle
	if worktreePostMergeHasTodo(prompt) && index == worktreePostMergeFocusTodo {
		checked = prompt.MarkTodoDone
		text = prompt.TodoText
		if checked {
			style = detailValueStyle
		}
	} else {
		checked = prompt.RemoveNow
		if checked {
			style = detailWarningStyle
		}
	}
	prefix := "[ ] "
	if checked {
		prefix = "[x] "
	}
	line := truncateText(prefix+text, width)
	if prompt.Selected == index {
		return noteDialogButtonSelectedStyle.UnsetPadding().Width(width).Render(line)
	}
	return style.Render(line)
}

func (m Model) completeWorktreePostMergeTodoCmd(todoProjectPath string, todoID int64, worktreePath, rootPath string, removeAfter bool, baseStatus string) tea.Cmd {
	if m.svc == nil {
		return func() tea.Msg {
			return worktreeActionMsg{projectPath: worktreePath, err: fmt.Errorf("service unavailable")}
		}
	}
	return func() tea.Msg {
		if err := m.svc.ToggleTodoDone(m.ctx, todoProjectPath, todoID, true); err != nil {
			return worktreeActionMsg{projectPath: worktreePath, err: err}
		}
		status := strings.TrimSpace(baseStatus)
		if status == "" {
			status = "Linked TODO marked done"
		}
		if removeAfter {
			if err := m.svc.RemoveWorktree(m.ctx, worktreePath); err != nil {
				return worktreeActionMsg{
					projectPath: worktreePath,
					err:         fmt.Errorf("linked TODO was marked done, but removing the worktree failed: %w", err),
				}
			}
			if strings.TrimSpace(baseStatus) != "" {
				status = strings.TrimSpace(baseStatus) + ". Linked TODO marked done. Worktree removed."
			} else {
				status = "Linked TODO marked done. Worktree removed."
			}
		} else {
			if strings.TrimSpace(baseStatus) != "" {
				status = strings.TrimSpace(baseStatus) + ". Linked TODO marked done."
			}
		}
		return worktreeActionMsg{
			projectPath: worktreePath,
			selectPath:  rootPath,
			status:      status,
		}
	}
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
	if snapshot, ok := m.liveCodexSnapshot(project.Path); ok {
		m.showSessionBlockedAttentionDialog(
			project,
			"Remove blocked",
			"Close the embedded agent session before removing this worktree.",
			"retry the removal",
			embeddedProvider(snapshot),
		)
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
	if confirm.Busy {
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
		confirm.Busy = true
		confirm.BusyMessage = "Please wait while Git removes this worktree checkout. The dialog is temporarily locked."
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
	if confirm.Busy {
		buttons = disabledActionTextStyle.Render("[" + todoDialogWaitingLabel(m.spinnerFrame) + "]")
	}
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
	if confirm.Busy {
		lines = append(lines, "")
		lines = append(lines, detailValueStyle.Render("Removal in progress"))
		lines = append(lines, renderWrappedDialogTextLines(detailMutedStyle, panelInnerW, confirm.BusyMessage)...)
	}
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
	if confirm.Busy {
		mergeButton = disabledActionTextStyle.Render("[" + todoDialogWaitingLabel(m.spinnerFrame) + "]")
	}
	buttonParts := []string{mergeButton}
	if !confirm.MergeReady && confirm.OfferCommit {
		buttonParts = append(buttonParts, renderNoteDialogButton("Commit", confirm.Selected == worktreeMergeConfirmFocusCommit))
	}
	buttonParts = append(buttonParts, renderNoteDialogButton("Keep", confirm.Selected == worktreeMergeConfirmFocusKeep))
	buttons := strings.Join(buttonParts, " ")
	if confirm.Busy {
		buttons = mergeButton
	}
	lines := []string{
		detailSectionStyle.Render("Merge worktree back"),
		"",
		detailValueStyle.Render(truncateText(confirm.BranchName+" -> "+confirm.TargetBranch, panelInnerW)),
		detailMutedStyle.Render(truncateText(confirm.ProjectPath, panelInnerW)),
		"",
		detailMutedStyle.Render(truncateText("Root must already be on "+confirm.TargetBranch+".", panelInnerW)),
		detailMutedStyle.Render(truncateText(m.mergeBackRulesSummary(), panelInnerW)),
	}
	if confirm.Busy {
		lines = append(lines, "", detailValueStyle.Render("Merge in progress"))
		lines = append(lines, renderWrappedDialogTextLines(detailMutedStyle, panelInnerW, confirm.BusyMessage)...)
	} else if strings.TrimSpace(confirm.BlockReason) != "" {
		lines = append(lines, "", detailWarningStyle.Render("Merge blocked"))
		lines = append(lines, renderWrappedDialogTextLines(detailWarningStyle, panelInnerW, confirm.BlockReason)...)
		if confirm.OfferCommit {
			lines = append(lines, renderWrappedDialogTextLines(detailMutedStyle, panelInnerW, "Open the commit preview first to finish this worktree, then retry the merge-back.")...)
		}
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
	panelW := min(max(56, bodyW-20), 88)
	panelInnerW := max(28, panelW-4)
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
	}
	if prompt.Busy {
		busyTitle := strings.TrimSpace(prompt.BusyTitle)
		if busyTitle == "" {
			busyTitle = "Update in progress"
		}
		lines = append(lines, detailValueStyle.Render(busyTitle))
		lines = append(lines, renderWrappedDialogTextLines(detailMutedStyle, panelInnerW, prompt.BusyMessage)...)
	} else if strings.TrimSpace(prompt.ErrorMessage) != "" {
		lines = append(lines, detailDangerStyle.Render("Update error"))
		lines = append(lines, renderWrappedDialogTextLines(detailDangerStyle, panelInnerW, prompt.ErrorMessage)...)
		lines = append(lines, "")
	}
	if !prompt.Busy {
		if worktreePostMergeHasTodo(prompt) {
			lines = append(lines, renderWrappedDialogTextLines(detailMutedStyle, panelInnerW, "Choose what to clean up now. The linked TODO and merged worktree are separate actions.")...)
			lines = append(lines, "")
			lines = append(lines, worktreePostMergeOptionLine(prompt, worktreePostMergeFocusTodo, panelInnerW))
			lines = append(lines, worktreePostMergeOptionLine(prompt, worktreePostMergeFocusRemove, panelInnerW))
		} else {
			lines = append(lines, renderWrappedDialogTextLines(detailMutedStyle, panelInnerW, "Choose whether to remove this merged worktree now or keep it for later.")...)
			lines = append(lines, "")
			lines = append(lines, worktreePostMergeOptionLine(prompt, worktreePostMergeFocusRemove, panelInnerW))
		}
		lines = append(lines, "")
		lines = append(lines, commandPaletteHintStyle.Render("Space toggle, ↑↓ navigate, Enter apply, Esc later"))
	} else {
		lines = append(lines, "")
		lines = append(lines, disabledActionTextStyle.Render("["+todoDialogWaitingLabel(m.spinnerFrame)+"]"))
	}
	panel := renderDialogPanel(panelW, panelInnerW, strings.Join(lines, "\n"))
	left := max(0, (bodyW-panelW)/2)
	top := max(0, (bodyH-lipgloss.Height(panel))/2)
	return overlayBlock(body, panel, bodyW, bodyH, left, top)
}
