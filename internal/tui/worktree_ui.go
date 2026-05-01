package tui

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

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
	worktreePostMergeFocusTodo = iota
	worktreePostMergeFocusRemove
)

// worktreeRemoveConfirm focus indices are computed dynamically via helper
// functions because the checkbox row is only present when the worktree is
// dirty.  See worktreeRemoveConfirmRemoveIndex / KeepIndex / FocusCount.

const (
	worktreeMergePendingSummary       = "Merging worktree back..."
	worktreeCommitMergePendingSummary = "Committing and merging worktree back..."
	worktreeRemovePendingSummary      = "Removing worktree..."
	worktreePostMergeRemoveSummary    = "Removing merged worktree..."
)

type projectListRow struct {
	Kind                projectListRowKind
	ProjectPath         string
	RootPath            string
	LinkedCount         int
	LinkedActiveCount   int
	LinkedDirtyCount    int
	LinkedUnmergedCount int
	Expanded            bool
}

type worktreeRemoveConfirmState struct {
	ProjectPath  string
	RootPath     string
	ProjectName  string
	BranchName   string
	TargetBranch string
	MergeStatus  model.WorktreeMergeStatus
	Dirty        bool
	ForceRemove  bool
	Busy         bool
	BusyMessage  string
	Selected     int
}

func worktreeRemoveConfirmOptionCount(confirm *worktreeRemoveConfirmState) int {
	if confirm != nil && confirm.Dirty {
		return 1 // "Force remove" checkbox
	}
	return 0
}

func worktreeRemoveConfirmRemoveIndex(confirm *worktreeRemoveConfirmState) int {
	return worktreeRemoveConfirmOptionCount(confirm)
}

func worktreeRemoveConfirmKeepIndex(confirm *worktreeRemoveConfirmState) int {
	return worktreeRemoveConfirmOptionCount(confirm) + 1
}

func worktreeRemoveConfirmFocusCount(confirm *worktreeRemoveConfirmState) int {
	return worktreeRemoveConfirmOptionCount(confirm) + 2
}

func worktreeRemoveConfirmReady(confirm *worktreeRemoveConfirmState) bool {
	if confirm == nil {
		return false
	}
	return !confirm.Dirty || confirm.ForceRemove
}

type worktreeMergeConfirmState struct {
	ProjectPath       string
	RootPath          string
	ProjectName       string
	BranchName        string
	TargetBranch      string
	PendingRefresh    map[string]struct{}
	HardBlockReason   string
	SourceDirty       bool
	RuntimeRunning    bool
	StopRuntime       bool
	CommitBeforeMerge bool
	HasLinkedTodo     bool
	MarkTodoDone      bool
	RemoveNow         bool
	ErrorMessage      string
	Busy              bool
	BusyMessage       string
	Selected          int
}

type worktreeMergeReadinessState struct {
	HardBlockReason string
	SourceDirty     bool
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

type worktreeMergeConfirmOption struct {
	label   string
	checked bool
}

func worktreeMergeConfirmOptions(confirm *worktreeMergeConfirmState) []worktreeMergeConfirmOption {
	if confirm == nil {
		return nil
	}
	options := make([]worktreeMergeConfirmOption, 0, 4)
	if confirm.RuntimeRunning {
		options = append(options, worktreeMergeConfirmOption{
			label:   "Stop active runtime first",
			checked: confirm.StopRuntime,
		})
	}
	if confirm.SourceDirty {
		options = append(options, worktreeMergeConfirmOption{
			label:   "Commit worktree changes first",
			checked: confirm.CommitBeforeMerge,
		})
	}
	if confirm.HasLinkedTodo {
		options = append(options, worktreeMergeConfirmOption{
			label:   "Mark linked TODO done after merge",
			checked: confirm.MarkTodoDone,
		})
	}
	options = append(options, worktreeMergeConfirmOption{
		label:   "Remove merged worktree after merge",
		checked: confirm.RemoveNow,
	})
	return options
}

func worktreeMergeConfirmOptionCount(confirm *worktreeMergeConfirmState) int {
	return len(worktreeMergeConfirmOptions(confirm))
}

func worktreeMergeConfirmApplyIndex(confirm *worktreeMergeConfirmState) int {
	return worktreeMergeConfirmOptionCount(confirm)
}

func worktreeMergeConfirmKeepIndex(confirm *worktreeMergeConfirmState) int {
	return worktreeMergeConfirmOptionCount(confirm) + 1
}

func worktreeMergeConfirmFocusCount(confirm *worktreeMergeConfirmState) int {
	return worktreeMergeConfirmOptionCount(confirm) + 2
}

func nextWorktreeMergeConfirmSelection(current, delta int, confirm *worktreeMergeConfirmState) int {
	count := worktreeMergeConfirmFocusCount(confirm)
	if count <= 0 {
		return 0
	}
	current = ((current % count) + count) % count
	return (current + delta + count) % count
}

func toggleWorktreeMergeConfirmSelection(confirm *worktreeMergeConfirmState) bool {
	if confirm == nil {
		return false
	}
	index := confirm.Selected
	if index < 0 {
		return false
	}
	if confirm.RuntimeRunning {
		if index == 0 {
			confirm.StopRuntime = !confirm.StopRuntime
			return true
		}
		index--
	}
	if confirm.SourceDirty {
		if index == 0 {
			confirm.CommitBeforeMerge = !confirm.CommitBeforeMerge
			return true
		}
		index--
	}
	if confirm.HasLinkedTodo {
		if index == 0 {
			confirm.MarkTodoDone = !confirm.MarkTodoDone
			return true
		}
		index--
	}
	if index == 0 {
		confirm.RemoveNow = !confirm.RemoveNow
		return true
	}
	return false
}

func worktreeMergeConfirmBlockReason(confirm *worktreeMergeConfirmState) string {
	if confirm == nil {
		return ""
	}
	if reason := strings.TrimSpace(confirm.HardBlockReason); reason != "" {
		return reason
	}
	if confirm.RuntimeRunning && !confirm.StopRuntime {
		return "This worktree still has an active runtime. Leave runtime shutdown checked or stop it manually before merging back."
	}
	if confirm.SourceDirty && !confirm.CommitBeforeMerge {
		return "This worktree is dirty. Leave commit checked or clean it manually before merging back."
	}
	return ""
}

func worktreeMergeConfirmRefreshing(confirm *worktreeMergeConfirmState) bool {
	return confirm != nil && len(confirm.PendingRefresh) > 0
}

func worktreeMergeConfirmTracksPath(confirm *worktreeMergeConfirmState, path string) bool {
	if confirm == nil {
		return false
	}
	path = normalizeProjectPath(path)
	if path == "" {
		return false
	}
	return path == normalizeProjectPath(confirm.ProjectPath) || path == normalizeProjectPath(confirm.RootPath)
}

func worktreeMergeConfirmPendingRefreshSet(paths ...string) map[string]struct{} {
	seen := map[string]struct{}{}
	for _, path := range paths {
		path = normalizeProjectPath(path)
		if path == "" {
			continue
		}
		seen[path] = struct{}{}
	}
	if len(seen) == 0 {
		return nil
	}
	return seen
}

func worktreeMergeConfirmMarkRefreshDone(confirm *worktreeMergeConfirmState, path string) bool {
	if confirm == nil || len(confirm.PendingRefresh) == 0 {
		return true
	}
	delete(confirm.PendingRefresh, normalizeProjectPath(path))
	return len(confirm.PendingRefresh) == 0
}

func worktreeMergeConfirmReady(confirm *worktreeMergeConfirmState) bool {
	return strings.TrimSpace(worktreeMergeConfirmBlockReason(confirm)) == ""
}

func worktreeMergeConfirmStatus(confirm *worktreeMergeConfirmState) string {
	if worktreeMergeConfirmRefreshing(confirm) {
		return "Checking live worktree status..."
	}
	if reason := strings.TrimSpace(worktreeMergeConfirmBlockReason(confirm)); reason != "" {
		return reason
	}
	return "Confirm worktree merge-back"
}

func worktreeMergeConfirmBusyMessage(confirm *worktreeMergeConfirmState) string {
	if confirm == nil {
		return "Please wait while Little Control Room merges this worktree back. The dialog is temporarily locked."
	}
	steps := make([]string, 0, 5)
	if confirm.StopRuntime && confirm.RuntimeRunning {
		steps = append(steps, "shuts down the runtime")
	}
	if confirm.CommitBeforeMerge && confirm.SourceDirty {
		steps = append(steps, "commits this worktree")
	}
	steps = append(steps, "merges it back")
	if confirm.MarkTodoDone && confirm.HasLinkedTodo {
		steps = append(steps, "marks the linked TODO done")
	}
	if confirm.RemoveNow {
		steps = append(steps, "removes the merged checkout")
	}
	if len(steps) == 1 {
		return "Please wait while Little Control Room " + steps[0] + ". The dialog is temporarily locked."
	}
	if len(steps) == 2 {
		return "Please wait while Little Control Room " + steps[0] + " and " + steps[1] + ". The dialog is temporarily locked."
	}
	return "Please wait while Little Control Room " + strings.Join(steps[:len(steps)-1], ", ") + ", and " + steps[len(steps)-1] + ". The dialog is temporarily locked."
}

func (m *Model) beginAsyncWorktreeAction(projectPath, pendingSummary, status string) {
	m.worktreeMergeConfirm = nil
	m.worktreePostMerge = nil
	m.worktreeRemoveConfirm = nil
	if strings.TrimSpace(pendingSummary) != "" {
		m.setPendingGitSummary(projectPath, pendingSummary)
	}
	m.status = strings.TrimSpace(status)
}

func (m Model) refreshProjectStatusPathsCmd(paths ...string) tea.Cmd {
	cmds := make([]tea.Cmd, 0, len(paths))
	seen := map[string]struct{}{}
	for _, path := range paths {
		path = normalizeProjectPath(path)
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		cmds = append(cmds, m.refreshProjectStatusCmd(path))
	}
	return batchCmds(cmds...)
}

func (m *Model) syncWorktreeMergeConfirmFromProjects(path string) {
	confirm := m.worktreeMergeConfirm
	if !worktreeMergeConfirmTracksPath(confirm, path) {
		return
	}

	project, ok := m.projectSummaryByPathAllProjects(confirm.ProjectPath)
	if ok {
		confirm.ProjectName = project.Name
		if rootPath := projectWorktreeRootPath(project); rootPath != "" {
			confirm.RootPath = rootPath
		}
		confirm.BranchName = projectWorktreeLabel(project)
		confirm.TargetBranch = strings.TrimSpace(project.WorktreeParentBranch)
		confirm.HasLinkedTodo = project.WorktreeOriginTodoID > 0
		confirm.RuntimeRunning = m.projectRuntimeSnapshot(project.Path).Running
	} else {
		project = model.ProjectSummary{
			Path:                 confirm.ProjectPath,
			Name:                 confirm.ProjectName,
			WorktreeRootPath:     confirm.RootPath,
			WorktreeKind:         model.WorktreeKindLinked,
			WorktreeParentBranch: confirm.TargetBranch,
			RepoDirty:            confirm.SourceDirty,
		}
	}
	rootProject, hasRoot := m.projectSummaryByPathAllProjects(confirm.RootPath)
	readiness := worktreeMergeReadinessWithRoot(project, strings.TrimSpace(project.WorktreeParentBranch), rootProject, hasRoot)
	if !confirm.SourceDirty && readiness.SourceDirty {
		confirm.CommitBeforeMerge = true
	}
	confirm.SourceDirty = readiness.SourceDirty
	confirm.HardBlockReason = readiness.HardBlockReason
	if worktreeMergeConfirmMarkRefreshDone(confirm, path) {
		confirm.Busy = false
		confirm.BusyMessage = ""
	}
	m.status = worktreeMergeConfirmStatus(confirm)
}

func shouldRefreshWorktreeMergeFamilyAfterError(err error) bool {
	if err == nil {
		return false
	}
	text := strings.TrimSpace(err.Error())
	return strings.Contains(text, "worktree is dirty; commit or discard changes before merging back") ||
		strings.Contains(text, "root worktree is dirty; commit or discard changes before merging back")
}

func worktreeMergeStatusText(result service.MergeWorktreeBackResult) string {
	status := fmt.Sprintf("Merged %s into %s", result.SourceBranch, result.TargetBranch)
	if strings.TrimSpace(result.CommitHash) != "" {
		status = fmt.Sprintf("Committed %s and merged %s into %s", result.CommitHash, result.SourceBranch, result.TargetBranch)
	}
	if result.AlreadyMerged {
		status = fmt.Sprintf("%s is already merged into %s", result.SourceBranch, result.TargetBranch)
		if strings.TrimSpace(result.CommitHash) != "" {
			status = fmt.Sprintf("Committed %s; %s is already merged into %s", result.CommitHash, result.SourceBranch, result.TargetBranch)
		}
	}
	return status
}

func appendWorktreeStatusClause(base, clause string) string {
	base = strings.TrimSpace(base)
	clause = strings.TrimSpace(clause)
	if clause == "" {
		return base
	}
	if base == "" {
		return clause
	}
	if strings.HasSuffix(base, ".") {
		return base + " " + clause
	}
	return base + ". " + clause
}

func projectWorktreeRootPath(project model.ProjectSummary) string {
	rootPath := filepath.Clean(strings.TrimSpace(project.WorktreeRootPath))
	if rootPath != "" && rootPath != "." {
		return rootPath
	}
	return filepath.Clean(strings.TrimSpace(project.Path))
}

func projectUsesRepoUI(project model.ProjectSummary) bool {
	return model.NormalizeProjectKind(project.Kind) != model.ProjectKindScratchTask
}

func projectIsOrphanedWorktree(project model.ProjectSummary) bool {
	if !projectUsesRepoUI(project) {
		return false
	}
	return project.WorktreeKind == model.WorktreeKindLinked && project.Forgotten && project.PresentOnDisk
}

func buildOrphanedWorktreeMap(summaries map[string]model.ProjectSummary) map[string][]model.ProjectSummary {
	if len(summaries) == 0 {
		return nil
	}
	byRoot := map[string][]model.ProjectSummary{}
	for _, summary := range summaries {
		if !projectIsOrphanedWorktree(summary) {
			continue
		}
		rootPath := projectWorktreeRootPath(summary)
		if rootPath == "" || rootPath == "." {
			continue
		}
		byRoot[rootPath] = append(byRoot[rootPath], summary)
	}
	for rootPath := range byRoot {
		sort.SliceStable(byRoot[rootPath], func(i, j int) bool {
			left := byRoot[rootPath][i]
			right := byRoot[rootPath][j]
			if !left.LastActivity.Equal(right.LastActivity) {
				return left.LastActivity.After(right.LastActivity)
			}
			return strings.ToLower(left.Path) < strings.ToLower(right.Path)
		})
	}
	return byRoot
}

func projectIsWorktreeRoot(project model.ProjectSummary) bool {
	if !projectUsesRepoUI(project) {
		return false
	}
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
	if project.RepoDirty {
		switch project.WorktreeMergeStatus {
		case model.WorktreeMergeStatusMerged:
			if targetBranch != "" {
				return "merged into " + targetBranch + ", but uncommitted changes remain"
			}
			return "merged, but uncommitted changes remain"
		case model.WorktreeMergeStatusMergeInProgress:
			if targetBranch != "" {
				return "merge in progress into " + targetBranch + ", but uncommitted changes remain"
			}
			return "merge in progress, but uncommitted changes remain"
		default:
			if targetBranch != "" {
				return "dirty; commit changes before merging into " + targetBranch
			}
			return "dirty; commit changes before merging back"
		}
	}
	switch project.WorktreeMergeStatus {
	case model.WorktreeMergeStatusMerged:
		if targetBranch != "" {
			return "merged into " + targetBranch
		}
		return "merged"
	case model.WorktreeMergeStatusMergeInProgress:
		if targetBranch != "" {
			return "merge in progress into " + targetBranch
		}
		return "merge in progress"
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
		if !projectUsesRepoUI(project) {
			continue
		}
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
	projects = partitionProjectsByKind(projects)

	type group struct {
		rootPath string
		members  []model.ProjectSummary
	}
	order := []string{}
	groups := map[string]*group{}
	for _, project := range projects {
		if !projectUsesRepoUI(project) {
			key := filepath.Clean(strings.TrimSpace(project.Path))
			if key == "" || key == "." {
				key = project.Path
			}
			order = append(order, key)
			groups[key] = &group{rootPath: key, members: []model.ProjectSummary{project}}
			continue
		}
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
		if !projectUsesRepoUI(group.members[0]) {
			project := group.members[0]
			rows = append(rows, project)
			meta = append(meta, projectListRow{
				Kind:        projectListRowStandalone,
				ProjectPath: project.Path,
				RootPath:    project.Path,
			})
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

		rootProject := aggregateWorktreeRootRow(group.members[rootIndex], group.members)
		children := make([]model.ProjectSummary, 0, len(group.members)-1)
		for i, project := range group.members {
			if i == rootIndex {
				continue
			}
			children = append(children, project)
		}
		activeCount, dirtyCount := m.worktreeActivityCounts(children)
		unmergedCount := worktreeUnmergedCount(children)
		expanded := m.isWorktreeGroupExpanded(rootPath, children, selectedPath)

		rows = append(rows, rootProject)
		meta = append(meta, projectListRow{
			Kind:                projectListRowRepo,
			ProjectPath:         rootProject.Path,
			RootPath:            rootPath,
			LinkedCount:         len(children),
			LinkedActiveCount:   activeCount,
			LinkedDirtyCount:    dirtyCount,
			LinkedUnmergedCount: unmergedCount,
			Expanded:            expanded,
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

func partitionProjectsByKind(projects []model.ProjectSummary) []model.ProjectSummary {
	return append([]model.ProjectSummary(nil), projects...)
}

func aggregateWorktreeRootRow(root model.ProjectSummary, family []model.ProjectSummary) model.ProjectSummary {
	aggregated := root
	for _, member := range family {
		if member.LastActivity.After(aggregated.LastActivity) {
			aggregated.LastActivity = member.LastActivity
		}
	}
	return aggregated
}

func (m Model) worktreeActivityCounts(projects []model.ProjectSummary) (int, int) {
	active := 0
	dirty := 0
	for _, project := range projects {
		if _, ok := m.pendingGitOperation(project.Path); ok {
			active++
			continue
		}
		if project.RepoDirty {
			dirty++
		}
		if project.Status != model.StatusIdle || m.projectHasLiveCodexSession(project.Path) || m.projectRuntimeSnapshot(project.Path).Running {
			active++
		}
	}
	return active, dirty
}

func worktreeNeedsMergeBack(project model.ProjectSummary) bool {
	return projectUsesRepoUI(project) &&
		project.WorktreeKind == model.WorktreeKindLinked &&
		project.WorktreeMergeStatus == model.WorktreeMergeStatusNotMerged
}

func worktreeUnmergedCount(projects []model.ProjectSummary) int {
	count := 0
	for _, project := range projects {
		if worktreeNeedsMergeBack(project) {
			count++
		}
	}
	return count
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
		if _, ok := m.pendingGitOperation(child.Path); ok {
			return true
		}
		if worktreeNeedsMergeBack(child) {
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
		return m.requestProjectDetailViewCmd(rootPath)
	}
	m.worktreeExpanded[rootPath] = next
	m.rebuildProjectList(project.Path)
	if next {
		m.status = "Worktrees expanded"
	} else {
		m.status = "Worktrees collapsed"
	}
	if selected, ok := m.selectedProject(); ok {
		return m.requestProjectDetailViewCmd(selected.Path)
	}
	return nil
}

func worktreeLinkedBadgeSummary(linked, active, dirty, unmerged, orphaned int) string {
	if linked <= 0 && orphaned <= 0 {
		return ""
	}
	parts := make([]string, 0, 4)
	if linked > 0 {
		parts = append(parts, fmt.Sprintf("%d linked", linked))
	}
	if orphaned > 0 {
		parts = append(parts, fmt.Sprintf("%d orphaned", orphaned))
	}
	if unmerged > 0 {
		parts = append(parts, fmt.Sprintf("%d needs merge", unmerged))
	}
	if active > 0 {
		parts = append(parts, fmt.Sprintf("%d active", active))
	} else if dirty > 0 {
		parts = append(parts, fmt.Sprintf("%d dirty", dirty))
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func worktreeGroupSummary(projects []model.ProjectSummary, active, dirty, unmerged, orphaned int) string {
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
	if orphaned > 0 {
		parts = append(parts, fmt.Sprintf("%d orphaned", orphaned))
	}
	if unmerged > 0 {
		parts = append(parts, fmt.Sprintf("%d needs merge", unmerged))
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
	actions := append([]footerAction(nil), m.scratchTaskFooterActions(width)...)
	row, project, ok := m.selectedProjectRow()
	if !ok {
		return actions
	}
	if projectMissing(project) {
		actions = append(actions, footerNavAction("/remove", "remove"))
	}
	rootPath := row.RootPath
	if rootPath == "" {
		rootPath = projectWorktreeRootPath(project)
	}
	family := m.worktreeFamily(rootPath)
	if len(family) <= 1 && row.Kind != projectListRowWorktree && row.LinkedCount == 0 {
		return actions
	}

	if len(family) > 1 || row.LinkedCount > 0 || row.Kind == projectListRowWorktree {
		actions = append(actions, footerNavAction("w", "lanes"))
	}
	if row.Kind == projectListRowWorktree && m.canMergeWorktreeBack(project) && width >= 80 {
		label := "merge"
		if project.RepoDirty {
			label = "commit+merge"
		}
		actions = append(actions, footerPrimaryAction("M", label))
	}
	if row.Kind == projectListRowWorktree && m.canRemoveWorktree(project) {
		actions = append(actions, footerHideAction("x", "remove"))
	}
	return actions
}

func (m Model) canMergeWorktreeBack(project model.ProjectSummary) bool {
	if !projectUsesRepoUI(project) {
		return false
	}
	if project.WorktreeKind != model.WorktreeKindLinked {
		return false
	}
	if project.WorktreeMergeStatus == model.WorktreeMergeStatusMergeInProgress {
		return false
	}
	if m.commitInFlightForWorktree(project, projectWorktreeRootPath(project)) {
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
	return true
}

func (m Model) commitInFlightForWorktree(project model.ProjectSummary, rootPath string) bool {
	projectPath := filepath.Clean(strings.TrimSpace(project.Path))
	if projectPath == "." {
		projectPath = ""
	}
	if projectPath != "" && m.pendingGitSummary(projectPath) != "" {
		return true
	}
	rootPath = filepath.Clean(strings.TrimSpace(rootPath))
	if rootPath == "" || rootPath == "." {
		rootPath = projectWorktreeRootPath(project)
	}
	if rootPath == "" || rootPath == "." {
		return false
	}
	for _, member := range m.worktreeFamily(rootPath) {
		memberPath := filepath.Clean(strings.TrimSpace(member.Path))
		if memberPath == "" || memberPath == "." {
			continue
		}
		if m.pendingGitSummary(memberPath) != "" {
			return true
		}
	}
	return false
}

func (m Model) canRemoveWorktree(project model.ProjectSummary) bool {
	if !projectUsesRepoUI(project) {
		return false
	}
	if project.WorktreeKind != model.WorktreeKindLinked {
		return false
	}
	if _, ok := m.pendingGitOperation(project.Path); ok {
		return false
	}
	return true
}

func (m Model) worktreeActionHints(project model.ProjectSummary, family []model.ProjectSummary) []string {
	if !projectUsesRepoUI(project) {
		return nil
	}
	hints := make([]string, 0, 4)
	if len(family) > 1 || project.WorktreeKind == model.WorktreeKindLinked {
		hints = append(hints, "w or /wt lanes")
	}
	if m.canMergeWorktreeBack(project) {
		hint := "M or /wt merge"
		if project.RepoDirty {
			hint += " (commit dirty changes first)"
		}
		hints = append(hints, hint)
	}
	if m.canRemoveWorktree(project) {
		hints = append(hints, "x or /wt remove")
	}
	if project.WorktreeKind != model.WorktreeKindLinked && len(family) > 1 {
		hints = append(hints, "/wt prune")
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
		if command := worktreeSlashCommandFromHint(hint); command != "" {
			commands = append(commands, command)
		}
	}
	if len(commands) == 0 {
		return ""
	}
	return "Worktrees: try " + strings.Join(commands, ", ") + "."
}

func worktreeSlashCommandFromHint(hint string) string {
	fields := strings.Fields(hint)
	for i := 0; i < len(fields)-1; i++ {
		if fields[i] != "/wt" {
			continue
		}
		command := strings.Trim(fields[i+1], ".,;:)")
		if command == "" {
			return ""
		}
		return "/wt " + command
	}
	return ""
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

func worktreeMergeSnapshotShowsSettledIdle(snapshot codexapp.Snapshot) bool {
	if snapshot.Phase != codexapp.SessionPhaseIdle {
		return false
	}
	statuses := []string{
		normalizedCodexStatus(snapshot.Status),
		normalizedCodexStatus(snapshot.LastSystemNotice),
	}
	for _, status := range statuses {
		switch strings.TrimSpace(status) {
		case "Turn finished", "Recovered idle after status check", "Conversation history compacted":
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
	return worktreeMergeSnapshotShowsCompletedTurn(snapshot) || worktreeMergeSnapshotShowsSettledIdle(snapshot)
}

func (m *Model) closeEmbeddedSessionForProject(projectPath string) (tea.Cmd, error) {
	projectPath = filepath.Clean(strings.TrimSpace(projectPath))
	if projectPath == "" || m.codexManager == nil {
		return nil, nil
	}
	if err := m.codexManager.CloseProject(projectPath); err != nil {
		return nil, err
	}
	delete(m.codexClosedHandled, projectPath)
	if m.codexVisibleProject == projectPath {
		m.codexVisibleProject = ""
		m.codexInput.Blur()
	}
	if m.codexHiddenProject == projectPath {
		m.codexHiddenProject = ""
	}
	return m.markProjectSessionSeen(projectPath), nil
}

func (m Model) worktreeMergeReadiness(project model.ProjectSummary, rootPath string) worktreeMergeReadinessState {
	if project.RepoConflict {
		return worktreeMergeReadinessState{
			HardBlockReason: "This worktree has unresolved conflicts. Resolve or abort the in-progress Git operation before merging back.",
		}
	}
	state := worktreeMergeReadinessState{}
	if project.RepoDirty {
		state.SourceDirty = true
	}
	targetBranch := strings.TrimSpace(project.WorktreeParentBranch)
	if rootPath == "" || targetBranch == "" {
		return state
	}
	rootProject, ok := m.projectSummaryByPath(rootPath)
	return worktreeMergeReadinessWithRoot(project, targetBranch, rootProject, ok)
}

func worktreeMergeReadinessWithRoot(project model.ProjectSummary, targetBranch string, rootProject model.ProjectSummary, ok bool) worktreeMergeReadinessState {
	if project.RepoConflict {
		return worktreeMergeReadinessState{
			HardBlockReason: "This worktree has unresolved conflicts. Resolve or abort the in-progress Git operation before merging back.",
		}
	}
	state := worktreeMergeReadinessState{}
	if project.RepoDirty {
		state.SourceDirty = true
	}
	targetBranch = strings.TrimSpace(targetBranch)
	if targetBranch == "" || !ok {
		return state
	}
	if rootProject.RepoConflict {
		state.HardBlockReason = "The root checkout has unresolved conflicts. Resolve or abort the in-progress Git operation before retrying."
		return state
	}
	if rootProject.RepoDirty {
		state.HardBlockReason = "The root checkout is dirty. Commit or discard changes before merging back."
		return state
	}
	rootBranch := strings.TrimSpace(rootProject.RepoBranch)
	if rootBranch != "" && rootBranch != targetBranch {
		state.HardBlockReason = fmt.Sprintf("The root checkout is on %s. Switch it to %s before merging back.", rootBranch, targetBranch)
		return state
	}
	return state
}

func (m Model) projectSummaryByPathAllProjects(projectPath string) (model.ProjectSummary, bool) {
	projectPath = filepath.Clean(strings.TrimSpace(projectPath))
	if projectPath == "" {
		return model.ProjectSummary{}, false
	}
	for _, project := range m.allProjects {
		if filepath.Clean(strings.TrimSpace(project.Path)) == projectPath {
			return project, true
		}
	}
	for _, project := range m.projects {
		if filepath.Clean(strings.TrimSpace(project.Path)) == projectPath {
			return project, true
		}
	}
	return model.ProjectSummary{}, false
}

func (m *Model) openWorktreeMergeConfirmForSelection() tea.Cmd {
	row, project, ok := m.selectedProjectRow()
	if !ok {
		m.status = "No project selected"
		return nil
	}
	autoCloseCmd := tea.Cmd(nil)
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
	if m.commitInFlightForWorktree(project, row.RootPath) {
		m.status = "A commit is still in progress. Finish it before merging this worktree back."
		return nil
	}
	if snapshot, ok := m.liveCodexSnapshot(project.Path); ok {
		if worktreeMergeCanAutoCloseSnapshot(snapshot) {
			seenCmd, err := m.closeEmbeddedSessionForProject(project.Path)
			if err != nil {
				m.reportError("Embedded session action failed", err, project.Path)
				return nil
			}
			autoCloseCmd = batchCmds(autoCloseCmd, seenCmd)
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
	readiness := m.worktreeMergeReadiness(project, row.RootPath)
	pendingRefresh := map[string]struct{}(nil)
	if m.svc != nil {
		pendingRefresh = worktreeMergeConfirmPendingRefreshSet(project.Path, row.RootPath)
	}
	m.worktreeMergeConfirm = &worktreeMergeConfirmState{
		ProjectPath:     project.Path,
		RootPath:        row.RootPath,
		ProjectName:     project.Name,
		BranchName:      projectWorktreeLabel(project),
		TargetBranch:    strings.TrimSpace(project.WorktreeParentBranch),
		PendingRefresh:  pendingRefresh,
		HardBlockReason: readiness.HardBlockReason,
		SourceDirty:     readiness.SourceDirty,
		RuntimeRunning:  m.projectRuntimeSnapshot(project.Path).Running,
		HasLinkedTodo:   project.WorktreeOriginTodoID > 0,
		RemoveNow:       true,
		Selected:        0,
	}
	if len(pendingRefresh) > 0 {
		m.worktreeMergeConfirm.Busy = true
		m.worktreeMergeConfirm.BusyMessage = "Checking live git status for this worktree and its root checkout."
	}
	m.worktreeMergeConfirm.StopRuntime = m.worktreeMergeConfirm.RuntimeRunning
	m.worktreeMergeConfirm.CommitBeforeMerge = m.worktreeMergeConfirm.SourceDirty
	m.worktreeMergeConfirm.MarkTodoDone = m.worktreeMergeConfirm.HasLinkedTodo
	m.worktreeMergeConfirm.Selected = worktreeMergeConfirmApplyIndex(m.worktreeMergeConfirm)
	m.status = worktreeMergeConfirmStatus(m.worktreeMergeConfirm)
	return batchCmds(autoCloseCmd, m.refreshProjectStatusPathsCmd(project.Path, row.RootPath))
}

func (m Model) updateWorktreeMergeConfirmMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	confirm := m.worktreeMergeConfirm
	if confirm == nil {
		return m, nil
	}
	if confirm.Busy {
		if worktreeMergeConfirmRefreshing(confirm) && msg.String() == "esc" {
			m.worktreeMergeConfirm = nil
			m.status = "Worktree merge-back canceled"
			return m, nil
		}
		return m, nil
	}
	switch msg.String() {
	case "esc":
		m.worktreeMergeConfirm = nil
		m.status = "Worktree merge-back canceled"
		return m, nil
	case "left", "h", "up", "k", "shift+tab":
		confirm.Selected = nextWorktreeMergeConfirmSelection(confirm.Selected, -1, confirm)
		return m, nil
	case "right", "l", "down", "j", "tab":
		confirm.Selected = nextWorktreeMergeConfirmSelection(confirm.Selected, 1, confirm)
		return m, nil
	case " ":
		if toggleWorktreeMergeConfirmSelection(confirm) {
			m.status = worktreeMergeConfirmStatus(confirm)
		}
		return m, nil
	case "enter":
		project, ok := m.projectSummaryByPath(confirm.ProjectPath)
		if !ok {
			m.status = "Worktree is unavailable right now"
			return m, nil
		}
		if m.commitInFlightForWorktree(project, confirm.RootPath) {
			m.status = "A commit is still in progress. Finish it before merging this worktree back."
			confirm.Busy = false
			return m, nil
		}
		if confirm.Selected < worktreeMergeConfirmOptionCount(confirm) {
			if toggleWorktreeMergeConfirmSelection(confirm) {
				m.status = worktreeMergeConfirmStatus(confirm)
			}
			return m, nil
		}
		if confirm.Selected == worktreeMergeConfirmKeepIndex(confirm) {
			m.worktreeMergeConfirm = nil
			m.status = "Worktree merge-back canceled"
			return m, nil
		}
		if !worktreeMergeConfirmReady(confirm) {
			m.status = worktreeMergeConfirmStatus(confirm)
			return m, nil
		}
		pendingSummary := worktreeMergePendingSummary
		if confirm.CommitBeforeMerge && confirm.SourceDirty {
			pendingSummary = worktreeCommitMergePendingSummary
		}
		m.beginAsyncWorktreeAction(confirm.ProjectPath, pendingSummary, pendingSummary)
		return m, m.applyWorktreeMergePlanCmd(*confirm)
	}
	return m, nil
}

func (m Model) applyWorktreeMergePlanCmd(confirm worktreeMergeConfirmState) tea.Cmd {
	return func() tea.Msg {
		projectPath := strings.TrimSpace(confirm.ProjectPath)
		msg := worktreeActionMsg{
			projectPath:            projectPath,
			selectPath:             strings.TrimSpace(confirm.RootPath),
			clearPendingGitSummary: true,
		}
		if confirm.StopRuntime && confirm.RuntimeRunning {
			if m.runtimeManager == nil {
				msg.err = fmt.Errorf("runtime manager unavailable")
				return msg
			}
			snapshot, err := stopProjectRuntimeAndWait(m.runtimeManager, projectPath, 3*time.Second)
			if err != nil {
				msg.err = fmt.Errorf("stop runtime: %w", err)
				return msg
			}
			confirm.RuntimeRunning = snapshot.Running
		}
		if m.svc == nil {
			msg.err = fmt.Errorf("service unavailable")
			return msg
		}

		var (
			result service.MergeWorktreeBackResult
			err    error
		)
		if confirm.CommitBeforeMerge && confirm.SourceDirty {
			result, err = m.svc.CommitAndMergeWorktreeBack(m.ctx, projectPath)
		} else {
			result, err = m.svc.MergeWorktreeBack(m.ctx, projectPath)
		}
		if err != nil {
			msg.err = err
			return msg
		}
		if rootPath := strings.TrimSpace(result.RootProjectPath); rootPath != "" {
			msg.selectPath = rootPath
		}
		status := worktreeMergeStatusText(result)
		if confirm.MarkTodoDone && confirm.HasLinkedTodo {
			switch {
			case result.LinkedTodoID <= 0:
				status = appendWorktreeStatusClause(status, "Linked TODO was already closed.")
			case strings.TrimSpace(result.LinkedTodoPath) == "":
				status = appendWorktreeStatusClause(status, "Could not mark the linked TODO done because its project path is unavailable.")
			default:
				if err := m.svc.ToggleTodoDone(m.ctx, result.LinkedTodoPath, result.LinkedTodoID, true); err != nil {
					status = appendWorktreeStatusClause(status, "Could not mark the linked TODO done: "+err.Error())
				} else {
					status = appendWorktreeStatusClause(status, "Linked TODO marked done.")
				}
			}
		}
		if confirm.RemoveNow {
			if err := m.svc.RemoveWorktree(m.ctx, projectPath, false); err != nil {
				status = appendWorktreeStatusClause(status, "Could not remove the merged worktree: "+err.Error())
			} else {
				status = appendWorktreeStatusClause(status, "Worktree removed.")
				msg.removedProjectPath = projectPath
			}
		}
		msg.status = status
		return msg
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
			if prompt.RemoveNow {
				m.beginAsyncWorktreeAction(prompt.ProjectPath, worktreePostMergeRemoveSummary, "Marking linked TODO done and removing merged worktree...")
				return m, m.completeWorktreePostMergeTodoCmd(prompt.TodoPath, prompt.TodoID, prompt.ProjectPath, prompt.RootPath, true, prompt.Status)
			}
			m.beginAsyncWorktreeAction(prompt.ProjectPath, "", "Marking linked TODO done...")
			return m, m.completeWorktreePostMergeTodoCmd(prompt.TodoPath, prompt.TodoID, prompt.ProjectPath, prompt.RootPath, false, prompt.Status)
		}
		m.beginAsyncWorktreeAction(prompt.ProjectPath, worktreePostMergeRemoveSummary, worktreePostMergeRemoveSummary)
		return m, m.removeWorktreeCmd(prompt.ProjectPath, prompt.RootPath, false)
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
		return dialogButtonSelectedStyle.UnsetPadding().Width(width).Render(line)
	}
	return style.Render(line)
}

func worktreePostMergeSectionLines(prompt *worktreePostMergeState, index, width int) []string {
	title := "Worktree cleanup"
	description := "Remove this merged checkout now or keep it around for later. Removing it only deletes the checkout."
	if worktreePostMergeHasTodo(prompt) && index == worktreePostMergeFocusTodo {
		title = "Linked TODO"
		description = "Mark the originating TODO complete if this merge finishes the work tracked by that item."
	}

	lines := []string{commandPaletteTitleStyle.Render(title)}
	lines = append(lines, renderWrappedDialogTextLines(detailMutedStyle, width, description)...)
	lines = append(lines, worktreePostMergeOptionLine(prompt, index, width))
	return lines
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
			if err := m.svc.RemoveWorktree(m.ctx, worktreePath, false); err != nil {
				return worktreeActionMsg{
					projectPath:            worktreePath,
					clearPendingGitSummary: true,
					err:                    fmt.Errorf("linked TODO was marked done, but removing the worktree failed: %w", err),
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
			projectPath:            worktreePath,
			removedProjectPath:     removedWorktreePath(removeAfter, worktreePath),
			selectPath:             rootPath,
			status:                 status,
			clearPendingGitSummary: removeAfter,
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
	if _, ok := m.pendingGitOperation(project.Path); ok {
		m.status = "A git action is still in progress for this worktree"
		return nil
	}
	state := &worktreeRemoveConfirmState{
		ProjectPath:  project.Path,
		RootPath:     row.RootPath,
		ProjectName:  project.Name,
		BranchName:   projectWorktreeLabel(project),
		TargetBranch: strings.TrimSpace(project.WorktreeParentBranch),
		MergeStatus:  project.WorktreeMergeStatus,
		Dirty:        project.RepoDirty,
	}
	state.Selected = worktreeRemoveConfirmKeepIndex(state)
	m.worktreeRemoveConfirm = state
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
	focusCount := worktreeRemoveConfirmFocusCount(confirm)
	switch msg.String() {
	case "esc":
		m.worktreeRemoveConfirm = nil
		m.status = "Worktree removal canceled"
		return m, nil
	case "left", "h", "up", "k", "shift+tab":
		confirm.Selected = ((confirm.Selected-1)%focusCount + focusCount) % focusCount
		return m, nil
	case "right", "l", "down", "j", "tab":
		confirm.Selected = (confirm.Selected + 1) % focusCount
		return m, nil
	case " ":
		if confirm.Dirty && confirm.Selected == 0 {
			confirm.ForceRemove = !confirm.ForceRemove
		}
		return m, nil
	case "enter":
		// Toggle checkbox on enter when focused on it.
		if confirm.Dirty && confirm.Selected < worktreeRemoveConfirmOptionCount(confirm) {
			confirm.ForceRemove = !confirm.ForceRemove
			return m, nil
		}
		if confirm.Selected == worktreeRemoveConfirmKeepIndex(confirm) {
			m.worktreeRemoveConfirm = nil
			m.status = "Worktree removal canceled"
			return m, nil
		}
		if !worktreeRemoveConfirmReady(confirm) {
			m.status = "Check \"Force remove\" to discard uncommitted changes"
			return m, nil
		}
		m.beginAsyncWorktreeAction(confirm.ProjectPath, worktreeRemovePendingSummary, worktreeRemovePendingSummary)
		return m, m.removeWorktreeCmd(confirm.ProjectPath, confirm.RootPath, confirm.ForceRemove)
	}
	return m, nil
}

func (m Model) removeWorktreeCmd(projectPath, rootPath string, force bool) tea.Cmd {
	if m.svc == nil {
		return func() tea.Msg {
			return worktreeActionMsg{projectPath: projectPath, err: fmt.Errorf("service unavailable")}
		}
	}
	return func() tea.Msg {
		err := m.svc.RemoveWorktree(m.ctx, projectPath, force)
		return worktreeActionMsg{
			projectPath:            projectPath,
			removedProjectPath:     removedWorktreePath(err == nil, projectPath),
			selectPath:             rootPath,
			status:                 "Worktree removed",
			clearPendingGitSummary: true,
			err:                    err,
		}
	}
}

func removedWorktreePath(removed bool, projectPath string) string {
	if !removed {
		return ""
	}
	return strings.TrimSpace(projectPath)
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
			projectPath:            projectPath,
			selectPath:             selectPath,
			status:                 "Pruned stale git worktrees",
			clearPendingGitSummary: true,
			err:                    err,
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
	removeLabel := "Remove"
	if confirm.Dirty && confirm.ForceRemove {
		removeLabel = "Force Remove"
	}
	removeButton := renderDialogButton(removeLabel, confirm.Selected == worktreeRemoveConfirmRemoveIndex(confirm))
	if confirm.Dirty && !confirm.ForceRemove {
		removeButton = disabledActionTextStyle.Render("[Remove blocked]")
	}
	if confirm.Busy {
		removeButton = disabledActionTextStyle.Render("[" + todoDialogWaitingLabel(m.spinnerFrame) + "]")
	}
	buttons := lipgloss.JoinHorizontal(
		lipgloss.Left,
		removeButton,
		" ",
		renderDialogButton("Keep", confirm.Selected == worktreeRemoveConfirmKeepIndex(confirm)),
	)
	if confirm.Busy {
		buttons = removeButton
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
	if confirm.Dirty && !confirm.Busy {
		lines = append(lines, "")
		lines = append(lines, detailWarningStyle.Render("Uncommitted changes"))
		lines = append(lines, renderWrappedDialogTextLines(detailWarningStyle, panelInnerW, "This worktree has uncommitted changes that will be discarded if you force-remove it.")...)
		lines = append(lines, "")
		prefix := "[ ] "
		style := detailMutedStyle
		if confirm.ForceRemove {
			prefix = "[x] "
			style = detailWarningStyle
		}
		line := truncateText(prefix+"Force remove (discard uncommitted changes)", panelInnerW)
		if confirm.Selected == 0 {
			lines = append(lines, dialogButtonSelectedStyle.UnsetPadding().Width(panelInnerW).Render(line))
		} else {
			lines = append(lines, style.Render(line))
		}
	}
	lines = append(lines, "")
	lines = append(lines, renderWrappedDialogTextLines(detailMutedStyle, panelInnerW, "Removing a linked worktree deletes the checkout only. The branch ref stays in the repo.")...)
	if confirm.Busy {
		lines = append(lines, "")
		lines = append(lines, detailValueStyle.Render("Removal in progress"))
		lines = append(lines, renderWrappedDialogTextLines(detailMutedStyle, panelInnerW, confirm.BusyMessage)...)
	}
	if confirm.Dirty && !confirm.Busy {
		lines = append(lines, "")
		lines = append(lines, renderHelpPanelActionRow(
			renderDialogAction("Space", "toggle", pushActionKeyStyle, pushActionTextStyle),
			renderDialogAction("↑↓", "navigate", navigateActionKeyStyle, navigateActionTextStyle),
			renderDialogAction("Enter", "choose", commitActionKeyStyle, commitActionTextStyle),
			renderDialogAction("Esc", "cancel", cancelActionKeyStyle, cancelActionTextStyle),
		))
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
	case model.WorktreeMergeStatusMergeInProgress:
		if targetBranch != "" {
			return "Merge in progress", "This worktree branch is already being merged into " + targetBranch + ". Finish or abort the root merge before removing the checkout.", detailWarningStyle
		}
		return "Merge in progress", "This worktree branch is already being merged back. Finish or abort the root merge before removing the checkout.", detailWarningStyle
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
	mergeButton := renderDialogButton("Merge", confirm.Selected == worktreeMergeConfirmApplyIndex(confirm))
	if !worktreeMergeConfirmReady(confirm) {
		mergeButton = disabledActionTextStyle.Render("[Merge blocked]")
	}
	if confirm.Busy {
		mergeButton = disabledActionTextStyle.Render("[" + todoDialogWaitingLabel(m.spinnerFrame) + "]")
	}
	buttons := strings.Join([]string{
		mergeButton,
		renderDialogButton("Keep", confirm.Selected == worktreeMergeConfirmKeepIndex(confirm)),
	}, " ")
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
		busyTitle := "Merge in progress"
		if worktreeMergeConfirmRefreshing(confirm) {
			busyTitle = "Checking merge readiness"
		}
		lines = append(lines, "", detailValueStyle.Render(busyTitle))
		lines = append(lines, renderWrappedDialogTextLines(detailMutedStyle, panelInnerW, confirm.BusyMessage)...)
		if worktreeMergeConfirmRefreshing(confirm) {
			lines = append(lines, "")
			lines = append(lines, renderHelpPanelActionRow(
				renderDialogAction("Esc", "close", cancelActionKeyStyle, cancelActionTextStyle),
			))
		}
	} else {
		lines = append(lines, "")
		lines = append(lines, renderWrappedDialogTextLines(detailMutedStyle, panelInnerW, "Choose the merge actions Little Control Room should handle now. Applicable actions start checked so you can press Enter to run the full flow.")...)
		lines = append(lines, "")
		for index, option := range worktreeMergeConfirmOptions(confirm) {
			prefix := "[ ] "
			style := detailMutedStyle
			if option.checked {
				prefix = "[x] "
				style = detailValueStyle
			}
			line := truncateText(prefix+option.label, panelInnerW)
			if confirm.Selected == index {
				lines = append(lines, dialogButtonSelectedStyle.UnsetPadding().Width(panelInnerW).Render(line))
			} else {
				lines = append(lines, style.Render(line))
			}
		}
	}
	if reason := strings.TrimSpace(worktreeMergeConfirmBlockReason(confirm)); !confirm.Busy && reason != "" {
		lines = append(lines, "", detailWarningStyle.Render("Merge blocked"))
		lines = append(lines, renderWrappedDialogTextLines(detailWarningStyle, panelInnerW, reason)...)
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
	if !confirm.Busy {
		lines = append(lines, "")
		lines = append(lines, renderHelpPanelActionRow(
			renderDialogAction("Space", "toggle", pushActionKeyStyle, pushActionTextStyle),
			renderDialogAction("↑↓", "navigate", navigateActionKeyStyle, navigateActionTextStyle),
			renderDialogAction("Enter", "choose", commitActionKeyStyle, commitActionTextStyle),
			renderDialogAction("Esc", "cancel", cancelActionKeyStyle, cancelActionTextStyle),
		))
	}
	lines = append(lines, "", buttons)
	content := clampDialogContent(
		strings.Join(lines, "\n"),
		max(12, bodyH-2),
		4,
		dialogOverflowHintLine(panelInnerW, "... more error details in /errors."),
	)
	panel := renderDialogPanel(panelW, panelInnerW, content)
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
			lines = append(lines, worktreePostMergeSectionLines(prompt, worktreePostMergeFocusTodo, panelInnerW)...)
			lines = append(lines, "")
			lines = append(lines, worktreePostMergeSectionLines(prompt, worktreePostMergeFocusRemove, panelInnerW)...)
		} else {
			lines = append(lines, renderWrappedDialogTextLines(detailMutedStyle, panelInnerW, "Choose whether to remove this merged worktree now or keep it for later.")...)
			lines = append(lines, "")
			lines = append(lines, worktreePostMergeSectionLines(prompt, worktreePostMergeFocusRemove, panelInnerW)...)
		}
		lines = append(lines, "")
		lines = append(lines, renderHelpPanelActionRow(
			renderDialogAction("Space", "toggle", pushActionKeyStyle, pushActionTextStyle),
			renderDialogAction("↑↓", "navigate", navigateActionKeyStyle, navigateActionTextStyle),
			renderDialogAction("Enter", "apply", commitActionKeyStyle, commitActionTextStyle),
			renderDialogAction("Esc", "later", cancelActionKeyStyle, cancelActionTextStyle),
		))
	} else {
		lines = append(lines, "")
		lines = append(lines, disabledActionTextStyle.Render("["+todoDialogWaitingLabel(m.spinnerFrame)+"]"))
	}
	content := clampDialogContent(
		strings.Join(lines, "\n"),
		max(12, bodyH-2),
		4,
		dialogOverflowHintLine(panelInnerW, "... more error details in /errors."),
	)
	panel := renderDialogPanel(panelW, panelInnerW, content)
	left := max(0, (bodyW-panelW)/2)
	top := max(0, (bodyH-lipgloss.Height(panel))/2)
	return overlayBlock(body, panel, bodyW, bodyH, left, top)
}
