package tui

import (
	"context"
	"errors"
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"lcroom/internal/attention"
	"lcroom/internal/codexapp"
	"lcroom/internal/commands"
	"lcroom/internal/model"
	"lcroom/internal/service"
	"lcroom/internal/uistyle"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func projectListCellStyle(style lipgloss.Style, selected bool) lipgloss.Style {
	if !selected {
		return style
	}
	return style.Inherit(projectListSelectedRowStyle)
}

func projectListSelectionFlashStyle(style lipgloss.Style) lipgloss.Style {
	return style.Foreground(lipgloss.Color("16")).Background(lipgloss.Color("178")).Bold(true)
}

func approvalPulseStyle(style lipgloss.Style) lipgloss.Style {
	return style.Foreground(lipgloss.Color("255")).Background(lipgloss.Color("160")).Bold(true)
}

func questionPulseStyle(style lipgloss.Style) lipgloss.Style {
	return style.Foreground(lipgloss.Color("255")).Background(lipgloss.Color("33")).Bold(true)
}

func browserPulseStyle(style lipgloss.Style) lipgloss.Style {
	return style.Foreground(lipgloss.Color("16")).Background(lipgloss.Color("214")).Bold(true)
}

var spinnerFrames = []string{"|", "/", "-", `\`}

const (
	recentMoveWindow                = 24 * time.Hour
	spinnerAnimationFrameWrap       = 4096
	assessmentFlashDuration         = time.Second
	topStatusAttentionPulseDuration = 480 * time.Millisecond
	projectListAgentWidth           = 10
	projectListTODOWidth            = 4
	projectListRunWidth             = 11
	projectListSelectionGutterWidth = 2
	usagePulseDuration              = 900 * time.Millisecond
)

var (
	dialogPanelBackground           = lipgloss.Color("235")
	dialogPanelBorderColor          = lipgloss.Color("81")
	dialogPanelFillReset            = "\x1b[48;5;235m"
	dialogPanelResetReplacer        = strings.NewReplacer("\x1b[0m", "\x1b[0m"+dialogPanelFillReset, "\x1b[m", "\x1b[m"+dialogPanelFillReset)
	dialogPanelFillStyle            = lipgloss.NewStyle().Background(dialogPanelBackground)
	detailLabelStyle                = lipgloss.NewStyle().Foreground(lipgloss.Color("81")).Bold(true)
	detailSectionStyle              = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true)
	detailValueStyle                = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	detailMutedStyle                = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	detailWarningStyle              = lipgloss.NewStyle().Foreground(lipgloss.Color("178")).Bold(true)
	detailDangerStyle               = lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Bold(true)
	detailConflictStyle             = lipgloss.NewStyle().Foreground(lipgloss.Color("141")).Bold(true)
	topStatusWarningBadgeStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("16")).Background(lipgloss.Color("214")).Bold(true).Padding(0, 1)
	topStatusWarningPulseBadgeStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("16")).Background(lipgloss.Color("172")).Bold(true).Padding(0, 1)
	topStatusDangerBadgeStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("255")).Background(lipgloss.Color("160")).Bold(true).Padding(0, 1)
	topStatusDangerPulseBadgeStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("255")).Background(lipgloss.Color("88")).Bold(true).Padding(0, 1)
	topStatusConflictBadgeStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("255")).Background(lipgloss.Color("92")).Bold(true).Padding(0, 1)
	topStatusSetupBadgeStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("16")).Background(lipgloss.Color("214")).Bold(true).Padding(0, 1)
	detailAttentionValueStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("229")).Bold(true)
	projectListActiveTabStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("16")).Background(lipgloss.Color("81")).Bold(true).Padding(0, 1)
	projectListInactiveTabStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Background(lipgloss.Color("238")).Padding(0, 1)
	projectListTabHotkeyStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("81")).Bold(true)
	projectListTabHintStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	projectListSelectedRowStyle     = lipgloss.NewStyle().
					Background(lipgloss.AdaptiveColor{Light: "255", Dark: "236"})
	dialogSelectedRowStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("230")).
				Background(lipgloss.Color("24")).
				Bold(true)
	commandPaletteTitleStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("81")).Bold(true)
	commandPaletteHintStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	commandPaletteRowStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	commandPalettePickStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("153")).Bold(true)
	commandPaletteSelectStyle = dialogSelectedRowStyle
	commitPreviewInfoStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	commitPreviewValueStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("229")).Bold(true)
	dialogProjectTitleStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true)
	commitActionKeyStyle      = uistyle.DialogActionPrimaryKeyStyle
	commitActionTextStyle     = uistyle.DialogActionPrimaryTextStyle
	navigateActionKeyStyle    = uistyle.DialogActionNavigateKeyStyle
	navigateActionTextStyle   = uistyle.DialogActionNavigateTextStyle
	pushActionKeyStyle        = uistyle.DialogActionSecondaryKeyStyle
	pushActionTextStyle       = uistyle.DialogActionSecondaryTextStyle
	cancelActionKeyStyle      = uistyle.DialogActionCancelKeyStyle
	cancelActionTextStyle     = uistyle.DialogActionCancelTextStyle
	disabledActionKeyStyle    = uistyle.DialogActionDisabledKeyStyle
	disabledActionTextStyle   = uistyle.DialogActionDisabledTextStyle
)

const spinnerTickInterval = 120 * time.Millisecond
const projectListSelectionFlashDuration = spinnerTickInterval
const runtimeSnapshotRefreshEveryTicks = 8
const cpuSnapshotRefreshEveryTicks = 25
const processScanRefreshEveryTicks = 500

func spinnerTickCmd() tea.Cmd {
	return tea.Tick(spinnerTickInterval, func(time.Time) tea.Msg {
		return spinnerTickMsg{}
	})
}

func onOffLabel(v bool) string {
	if v {
		return "ON"
	}
	return "OFF"
}

func (m Model) markProjectSessionSeenCmd(path string, seenAt time.Time, refresh projectInvalidationIntent) tea.Cmd {
	if m.svc == nil {
		return nil
	}
	path = filepath.Clean(strings.TrimSpace(path))
	if path == "" {
		return nil
	}
	if m.isAgentTaskProjectPath(path) {
		return nil
	}
	if seenAt.IsZero() {
		seenAt = m.currentTime()
	}
	return func() tea.Msg {
		ctx, cancel := m.actionContext(tuiQuickActionTimeout)
		defer cancel()
		err := m.svc.MarkProjectSessionSeen(ctx, path, seenAt)
		err = timeoutActionError(err, tuiQuickActionTimeout, "marking the session seen")
		return projectSessionSeenMsg{
			path:    path,
			refresh: refresh,
			err:     err,
		}
	}
}

func (m Model) markProjectSessionUnreadCmd(path string) tea.Cmd {
	if m.svc == nil {
		return nil
	}
	path = filepath.Clean(strings.TrimSpace(path))
	if path == "" {
		return nil
	}
	if m.isAgentTaskProjectPath(path) {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := m.actionContext(tuiQuickActionTimeout)
		defer cancel()
		err := m.svc.MarkProjectSessionUnread(ctx, path)
		err = timeoutActionError(err, tuiQuickActionTimeout, "marking the session unread")
		return actionMsg{
			projectPath: path,
			status:      "Marked unread",
			refresh:     invalidateProjectData(path),
			err:         err,
		}
	}
}

func (m *Model) markProjectSessionSeen(projectPath string) tea.Cmd {
	return m.markProjectSessionSeenWithRefresh(projectPath, projectInvalidationIntent{})
}

func (m *Model) markProjectSessionSeenWithRefresh(projectPath string, refresh projectInvalidationIntent) tea.Cmd {
	projectPath = filepath.Clean(strings.TrimSpace(projectPath))
	if projectPath == "" {
		return nil
	}
	seenAt := m.currentTime()
	m.markProjectSessionSeenLocal(projectPath, seenAt)
	return m.markProjectSessionSeenCmd(projectPath, seenAt, refresh)
}

func (m *Model) markProjectSessionUnread(projectPath string) tea.Cmd {
	projectPath = filepath.Clean(strings.TrimSpace(projectPath))
	if projectPath == "" {
		return nil
	}
	m.clearProjectSessionSeenLocal(projectPath)
	return m.markProjectSessionUnreadCmd(projectPath)
}

func (m *Model) upsertProjectSummary(summary model.ProjectSummary) {
	path := filepath.Clean(strings.TrimSpace(summary.Path))
	if path == "" {
		return
	}
	summary.Path = path
	m.allProjects = removeProjectSummaryFromSlice(m.allProjects, path)
	m.archivedProjects = removeProjectSummaryFromSlice(m.archivedProjects, path)
	if projectSummaryArchived(summary) {
		m.archivedProjects = append(m.archivedProjects, summary)
		return
	}
	if !projectSummaryActive(summary) {
		return
	}
	m.allProjects = append(m.allProjects, summary)
}

func (m Model) preserveRefreshingAssessmentDisplays(summaries []model.ProjectSummary) []model.ProjectSummary {
	if len(summaries) == 0 {
		return summaries
	}
	out := make([]model.ProjectSummary, len(summaries))
	for i, summary := range summaries {
		out[i] = m.preserveRefreshingAssessmentDisplay(summary)
	}
	return out
}

func (m Model) preserveRefreshingAssessmentDisplay(summary model.ProjectSummary) model.ProjectSummary {
	previous, ok := m.projectSummaryByPathAllProjects(summary.Path)
	if !ok {
		return summary
	}
	return preserveRefreshingAssessmentDisplay(summary, previous)
}

func preserveRefreshingAssessmentDisplay(summary, previous model.ProjectSummary) model.ProjectSummary {
	if !projectAssessmentRefreshing(summary) || !sameProjectSummaryLatestSession(summary, previous) {
		return summary
	}
	if !previousAssessmentDisplayCanCarryDuringRefresh(previous) {
		return summary
	}
	if strings.TrimSpace(summary.LatestSessionSummary) == "" {
		summary.LatestSessionSummary = previous.LatestSessionSummary
	}
	if !assessmentCategoryHasLabel(summary.LatestSessionClassificationType) {
		summary.LatestSessionClassificationType = previous.LatestSessionClassificationType
	}
	return summary
}

func previousAssessmentDisplayCanCarryDuringRefresh(project model.ProjectSummary) bool {
	if strings.TrimSpace(project.LatestSessionSummary) == "" {
		return false
	}
	return project.LatestSessionClassification == model.ClassificationCompleted || projectAssessmentRefreshing(project)
}

func sameProjectSummaryLatestSession(left, right model.ProjectSummary) bool {
	leftPath := normalizeProjectPath(left.Path)
	rightPath := normalizeProjectPath(right.Path)
	if leftPath == "" || leftPath != rightPath {
		return false
	}
	leftID := canonicalProjectLatestSessionID(left)
	rightID := canonicalProjectLatestSessionID(right)
	return leftID != "" && leftID == rightID
}

func canonicalProjectLatestSessionID(project model.ProjectSummary) string {
	_, sessionID, rawSessionID := model.NormalizeSessionIdentity(project.LatestSessionSource, project.LatestSessionFormat, project.LatestSessionID, project.LatestRawSessionID)
	if strings.TrimSpace(sessionID) != "" {
		return strings.TrimSpace(sessionID)
	}
	return strings.TrimSpace(rawSessionID)
}

func (m *Model) removeProjectSummary(projectPath string) {
	path := filepath.Clean(strings.TrimSpace(projectPath))
	if path == "" {
		return
	}
	m.allProjects = removeProjectSummaryFromSlice(m.allProjects, path)
	m.archivedProjects = removeProjectSummaryFromSlice(m.archivedProjects, path)
	m.projects = removeProjectSummaryFromSlice(m.projects, path)
}

func removeProjectSummaryFromSlice(projects []model.ProjectSummary, cleanPath string) []model.ProjectSummary {
	if len(projects) == 0 {
		return projects
	}
	filtered := projects[:0]
	for _, project := range projects {
		if filepath.Clean(project.Path) == cleanPath {
			continue
		}
		filtered = append(filtered, project)
	}
	return filtered
}

func (m *Model) applyRemovedProjectLocally(projectPath, selectPath string) {
	projectPath = normalizeProjectPath(projectPath)
	selectPath = normalizeProjectPath(selectPath)
	if projectPath == "" {
		return
	}
	currentSelectedPath := m.currentSelectedProjectPath()
	m.removeProjectSummary(projectPath)

	if normalizeProjectPath(m.detail.Summary.Path) == projectPath {
		if selectPath != "" {
			if summary, ok := m.projectSummaryByPathAllProjects(selectPath); ok {
				m.detail = model.ProjectDetail{Summary: summary}
			} else {
				m.detail = model.ProjectDetail{}
			}
		} else {
			m.detail = model.ProjectDetail{}
		}
		m.syncTodoDialogSelection()
	}

	if selectPath == "" && currentSelectedPath != projectPath {
		selectPath = currentSelectedPath
	}
	m.rebuildProjectList(selectPath)
	if len(m.projects) == 0 {
		m.detail = model.ProjectDetail{}
		m.syncDetailViewport(true)
		return
	}
	m.syncDetailViewport(false)
}

func (m Model) orphanedWorktreeFamily(rootPath string) []model.ProjectSummary {
	rootPath = filepath.Clean(strings.TrimSpace(rootPath))
	if rootPath == "" {
		return nil
	}
	family := m.orphanedWorktreesByRoot[rootPath]
	if len(family) == 0 {
		return nil
	}
	return append([]model.ProjectSummary(nil), family...)
}

func (m Model) orphanedWorktreeCount(rootPath string) int {
	return len(m.orphanedWorktreeFamily(rootPath))
}

func (m *Model) markProjectSessionSeenLocal(projectPath string, seenAt time.Time) {
	path := filepath.Clean(strings.TrimSpace(projectPath))
	if path == "" {
		return
	}
	if seenAt.IsZero() {
		seenAt = m.currentTime()
	}
	for i := range m.allProjects {
		if filepath.Clean(m.allProjects[i].Path) == path {
			m.allProjects[i].LastSessionSeenAt = seenAt
		}
	}
	for i := range m.archivedProjects {
		if filepath.Clean(m.archivedProjects[i].Path) == path {
			m.archivedProjects[i].LastSessionSeenAt = seenAt
		}
	}
	for i := range m.projects {
		if filepath.Clean(m.projects[i].Path) == path {
			m.projects[i].LastSessionSeenAt = seenAt
		}
	}
	if filepath.Clean(m.detail.Summary.Path) == path {
		m.detail.Summary.LastSessionSeenAt = seenAt
	}
}

func (m *Model) clearProjectSessionSeenLocal(projectPath string) {
	path := filepath.Clean(strings.TrimSpace(projectPath))
	if path == "" {
		return
	}
	for i := range m.allProjects {
		if filepath.Clean(m.allProjects[i].Path) == path {
			m.allProjects[i].LastSessionSeenAt = time.Time{}
		}
	}
	for i := range m.archivedProjects {
		if filepath.Clean(m.archivedProjects[i].Path) == path {
			m.archivedProjects[i].LastSessionSeenAt = time.Time{}
		}
	}
	for i := range m.projects {
		if filepath.Clean(m.projects[i].Path) == path {
			m.projects[i].LastSessionSeenAt = time.Time{}
		}
	}
	if filepath.Clean(m.detail.Summary.Path) == path {
		m.detail.Summary.LastSessionSeenAt = time.Time{}
	}
}

func (m Model) dispatchCommand(inv commands.Invocation) (tea.Model, tea.Cmd) {
	switch inv.Kind {
	case commands.KindHelp:
		m.showPerf = false
		m.showAIStats = false
		m.showHelp = true
		m.status = "Help open. Press ? or Esc to close"
		return m, nil
	case commands.KindAIStats:
		return m, m.openAIStatsDialog()
	case commands.KindPerf:
		return m, m.openPerfDialog()
	case commands.KindErrors:
		return m.openErrorLog()
	case commands.KindBoss:
		switch inv.Toggle {
		case commands.ToggleOff:
			m.bossSetupPrompt = nil
			m.closeBossMode("Boss mode hidden")
			return m, nil
		case commands.ToggleOn:
			if m.bossMode {
				m.status = "Boss mode already open"
				return m, nil
			}
			return m.openBossModeOrSetupPrompt()
		default:
			if m.bossMode {
				m.closeBossMode("Boss mode hidden")
				return m, nil
			}
			return m.openBossModeOrSetupPrompt()
		}
	case commands.KindRefresh:
		m.loading = true
		m.status = "Scanning and retrying failed assessments..."
		return m, batchCmds(
			m.refreshProjectStatusCmdWithOptions(m.currentSelectedProjectPath(), service.ScanOptions{
				ForceRetryFailedClassifications: true,
			}),
			m.requestScanCmd(true),
		)
	case commands.KindSort:
		return m, m.setSortMode(projectSortMode(inv.Sort))
	case commands.KindView:
		return m, m.setVisibilityMode(commandVisibilityMode(inv.View))
	case commands.KindTab:
		switch inv.Tab {
		case commands.ProjectTabActive:
			return m, m.setArchiveMode(projectArchiveActive)
		case commands.ProjectTabArchived:
			return m, m.setArchiveMode(projectArchiveArchived)
		default:
			return m, m.toggleArchiveMode()
		}
	case commands.KindSetup:
		return m, m.openQuickSetupSettingsMode(true)
	case commands.KindSettings:
		return m, m.openSettingsMode()
	case commands.KindSkills:
		return m, m.openSkillsDialog()
	case commands.KindFilter:
		if inv.Clear {
			return m, m.setProjectFilter("")
		}
		if strings.TrimSpace(inv.Filter) != "" {
			return m, m.setProjectFilter(inv.Filter)
		}
		return m, m.openProjectFilterDialog()
	case commands.KindNewProject:
		return m, m.openNewProjectDialog(commandAssistantProvider(inv.Assistant))
	case commands.KindNewTask:
		return m, m.openNewTaskDialog(inv.Prompt, commandAssistantProvider(inv.Assistant))
	case commands.KindTaskActions:
		return m, m.openScratchTaskActionConfirmForSelection()
	case commands.KindOpen:
		p, ok := m.selectedProject()
		if !ok {
			m.status = "No project selected"
			return m, nil
		}
		if !p.PresentOnDisk {
			m.status = "Open requires a folder present on disk"
			return m, nil
		}
		m.status = "Opening project in browser..."
		return m, m.openProjectDirInBrowserCmd(p.Path)
	case commands.KindTerminal:
		p, ok := m.selectedProject()
		if !ok {
			m.status = "No project selected"
			return m, nil
		}
		if !p.PresentOnDisk {
			m.status = "Terminal requires a folder present on disk"
			return m, nil
		}
		m.status = "Opening project terminal..."
		return m, m.openProjectDirInTerminalCmd(p.Path)
	case commands.KindRun:
		p, ok := m.selectedProject()
		if !ok {
			m.status = "No project selected"
			return m, nil
		}
		if !p.PresentOnDisk {
			m.status = "Run requires a folder present on disk"
			return m, nil
		}
		return m.handleRunCommand(p, inv.Command)
	case commands.KindRestart:
		p, ok := m.selectedProject()
		if !ok {
			m.status = "No project selected"
			return m, nil
		}
		if !p.PresentOnDisk {
			m.status = "Restart requires a folder present on disk"
			return m, nil
		}
		snapshot := m.projectRuntimeSnapshot(p.Path)
		command := effectiveRuntimeCommand(p.RunCommand, snapshot)
		if command == "" {
			m.status = "Runtime command is not set"
			return m, nil
		}
		m.status = "Restarting runtime..."
		return m, m.restartProjectRuntimeCmd(p.Path, snapshot.ID, command, snapshot.CWD)
	case commands.KindRunEdit:
		p, ok := m.selectedProject()
		if !ok {
			m.status = "No project selected"
			return m, nil
		}
		if !p.PresentOnDisk {
			m.status = "Run command editing requires a folder present on disk"
			return m, nil
		}
		return m, m.openRunCommandDialog(p, false)
	case commands.KindRuntime:
		return m, m.openRuntimeInspectorForSelection()
	case commands.KindCPU:
		return m, m.openCPUDialog()
	case commands.KindStop:
		p, ok := m.selectedProject()
		if !ok {
			m.status = "No project selected"
			return m, nil
		}
		return m.handleStopRuntime(p)
	case commands.KindDiff:
		p, ok := m.selectedProject()
		if !ok {
			m.status = "No project selected"
			return m, nil
		}
		if !p.PresentOnDisk {
			m.status = "Diff requires a folder present on disk"
			return m, nil
		}
		return m, m.startDiffView(p.Path, p.Name)
	case commands.KindCommit:
		p, ok := m.selectedProject()
		if !ok {
			m.status = "No project selected"
			return m, nil
		}
		return m, m.startCommitPreview(p, service.GitActionCommit, inv.Message)
	case commands.KindPush:
		p, ok := m.selectedProject()
		if !ok {
			m.status = "No project selected"
			return m, nil
		}
		m.setPendingGitOperation(p.Path, pendingGitOperationPush, "Pushing...")
		m.status = "Pushing..."
		return m, m.pushCmd(p.Path)
	case commands.KindPull:
		p, ok := m.selectedProject()
		if !ok {
			m.status = "No project selected"
			return m, nil
		}
		m.setPendingGitOperation(p.Path, pendingGitOperationPull, "Pulling...")
		m.status = "Pulling..."
		return m, m.pullCmd(p.Path)
	case commands.KindResolve:
		return m.resolveMergeConflictsForSelection()
	case commands.KindCodex:
		return m.launchCodexForSelection(false, inv.Prompt)
	case commands.KindCodexNew:
		return m.launchCodexForSelection(true, inv.Prompt)
	case commands.KindClaude:
		return m.launchClaudeForSelection(false, inv.Prompt)
	case commands.KindClaudeNew:
		return m.launchClaudeForSelection(true, inv.Prompt)
	case commands.KindOpenCode:
		return m.launchOpenCodeForSelection(false, inv.Prompt)
	case commands.KindOpenCodeNew:
		return m.launchOpenCodeForSelection(true, inv.Prompt)
	case commands.KindLCAgent:
		return m.launchLCAgentForSelection(false, inv.Prompt)
	case commands.KindLCAgentNew:
		return m.launchLCAgentForSelection(true, inv.Prompt)
	case commands.KindTodo:
		return m, m.openTodoDialogForSelection()
	case commands.KindWorktreeLanes:
		return m, m.toggleSelectedWorktreeGroup()
	case commands.KindWorktreeMerge:
		return m, m.openWorktreeMergeConfirmForSelection()
	case commands.KindWorktreeRemove:
		return m, m.openWorktreeRemoveConfirmForSelection()
	case commands.KindWorktreePrune:
		row, project, ok := m.selectedProjectRow()
		if !ok {
			m.status = "No project selected"
			return m, nil
		}
		rootPath := row.RootPath
		if rootPath == "" {
			rootPath = projectWorktreeRootPath(project)
		}
		if rootPath == "" {
			m.status = "No project selected"
			return m, nil
		}
		m.setPendingGitSummary(rootPath, "Pruning worktrees...")
		m.status = "Pruning stale git worktrees..."
		return m, m.pruneWorktreesCmd(rootPath, rootPath)
	case commands.KindPin:
		p, ok := m.selectedProject()
		if !ok {
			m.status = "No project selected"
			return m, nil
		}
		return m, m.togglePinCmd(p.Path)
	case commands.KindRead:
		if inv.All {
			paths := make([]string, 0, len(m.projects))
			seenAt := m.currentTime()
			for _, project := range m.projects {
				if attention.AssessmentUnreadAt(project).IsZero() {
					continue
				}
				paths = append(paths, project.Path)
				m.markProjectSessionSeenLocal(project.Path, seenAt)
			}
			if len(paths) == 0 {
				m.status = "No visible completed assessments to mark read"
				return m, nil
			}
			cmds := make([]tea.Cmd, 0, len(paths))
			for _, path := range paths {
				cmds = append(cmds, m.markProjectSessionSeenCmd(path, seenAt, projectInvalidationIntent{}))
			}
			m.status = fmt.Sprintf("Marked %d visible project(s) read", len(paths))
			return m, tea.Batch(cmds...)
		}
		p, ok := m.selectedProject()
		if !ok {
			m.status = "No project selected"
			return m, nil
		}
		if attention.AssessmentUnreadAt(p).IsZero() {
			m.status = "Selected project has no completed assessment to mark read"
			return m, nil
		}
		m.status = "Marked read"
		return m, m.markProjectSessionSeen(p.Path)
	case commands.KindUnread:
		p, ok := m.selectedProject()
		if !ok {
			m.status = "No project selected"
			return m, nil
		}
		if attention.AssessmentUnreadAt(p).IsZero() {
			m.status = "Selected project has no completed assessment to mark unread"
			return m, nil
		}
		m.status = "Marked unread"
		return m, m.markProjectSessionUnread(p.Path)
	case commands.KindSnooze:
		p, ok := m.selectedProject()
		if !ok {
			m.status = "No project selected"
			return m, nil
		}
		return m, m.snoozeCmd(p.Path, inv.Duration)
	case commands.KindClearSnooze:
		p, ok := m.selectedProject()
		if !ok {
			m.status = "No project selected"
			return m, nil
		}
		return m, m.clearSnoozeCmd(p.Path)
	case commands.KindSession:
		return m.openCodexPicker()
	case commands.KindSessions:
		m.applySectionToggle("Sessions", inv.Toggle, &m.showSessions)
		return m, nil
	case commands.KindEvents:
		m.applySectionToggle("Recent events", inv.Toggle, &m.showEvents)
		return m, nil
	case commands.KindIgnore:
		p, ok := m.selectedProject()
		if !ok {
			m.status = "No project selected"
			return m, nil
		}
		return m, m.ignoreProjectCmd(p)
	case commands.KindIgnored:
		return m.openIgnoredPicker()
	case commands.KindArchive:
		return m.setProjectArchivedForSelection(true)
	case commands.KindUnarchive:
		return m.setProjectArchivedForSelection(false)
	case commands.KindRemove:
		return m.openRemoveActionForSelection()
	case commands.KindFocus:
		m.setFocusedPaneFromCommand(inv.Focus)
		return m, nil
	case commands.KindPrivacySettings:
		return m, m.openPrivacySettingsMode()
	case commands.KindPrivacy:
		switch inv.Toggle {
		case commands.ToggleOn:
			m.privacyMode = true
			m.status = "Privacy mode enabled"
		case commands.ToggleOff:
			m.privacyMode = false
			m.status = "Privacy mode disabled"
		case commands.ToggleToggle:
			m.privacyMode = !m.privacyMode
			if m.privacyMode {
				m.status = "Privacy mode enabled"
			} else {
				m.status = "Privacy mode disabled"
			}
		}
		selectedPath := ""
		if p, ok := m.selectedProject(); ok {
			selectedPath = p.Path
		}
		m.rebuildProjectList(selectedPath)
		return m, m.savePrivacyModeCmd(m.privacyMode)
	case commands.KindQuit:
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
	default:
		m.status = "Command not implemented"
		return m, nil
	}
}

func commandAssistantProvider(raw string) codexapp.Provider {
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	return codexapp.Provider(raw).Normalized()
}

func scanCompleteStatus(report service.ScanReport) string {
	if report.QueuedClassifications <= 0 {
		return fmt.Sprintf("Scan complete: %d updated", len(report.UpdatedProjects))
	}
	label := "classifications"
	if report.QueuedClassifications == 1 {
		label = "classification"
	}
	return fmt.Sprintf("Scan complete: %d updated, %d %s queued", len(report.UpdatedProjects), report.QueuedClassifications, label)
}

func loadedProjectsStatus(projectCount int, sortMode projectSortMode, visibility projectVisibilityMode, projectFilter string) string {
	status := fmt.Sprintf("Loaded %d projects (%s, %s)", projectCount, sortMode, visibilityLabel(visibility))
	if label := compactProjectFilterLabel(projectFilter, 24); label != "" {
		return status + " with " + label
	}
	return status
}

func projectLoadFailedStatus(hadProjects bool) string {
	if hadProjects {
		return "Project refresh failed"
	}
	return "Project load failed"
}

func (m Model) togglePinCmd(path string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := m.actionContext(tuiQuickActionTimeout)
		defer cancel()
		err := m.svc.TogglePin(ctx, path)
		err = timeoutActionError(err, tuiQuickActionTimeout, "updating the pin")
		return actionMsg{
			projectPath: path,
			status:      "Pin toggled",
			refresh:     invalidateProjectData(path),
			err:         err,
		}
	}
}

func (m Model) snoozeCmd(path string, d time.Duration) tea.Cmd {
	label := formatSnoozeDuration(d)
	return func() tea.Msg {
		ctx, cancel := m.actionContext(tuiQuickActionTimeout)
		defer cancel()
		err := m.svc.Snooze(ctx, path, d)
		err = timeoutActionError(err, tuiQuickActionTimeout, "snoozing the project")
		return actionMsg{
			projectPath: path,
			status:      "Snoozed for " + label,
			refresh:     invalidateProjectData(path),
			err:         err,
		}
	}
}

func (m Model) clearSnoozeCmd(path string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := m.actionContext(tuiQuickActionTimeout)
		defer cancel()
		err := m.svc.ClearSnooze(ctx, path)
		err = timeoutActionError(err, tuiQuickActionTimeout, "clearing the snooze")
		return actionMsg{
			projectPath: path,
			status:      "Snooze cleared",
			refresh:     invalidateProjectData(path),
			err:         err,
		}
	}
}

func (m Model) openRemoveActionForSelection() (tea.Model, tea.Cmd) {
	project, ok := m.selectedProject()
	if !ok {
		m.status = "No project selected"
		return m, nil
	}
	if model.NormalizeProjectKind(project.Kind) == model.ProjectKindScratchTask {
		return m, m.openScratchTaskActionConfirmForSelection()
	}
	if model.NormalizeProjectKind(project.Kind) == model.ProjectKindAgentTask {
		return m, m.openAgentTaskActionConfirmForSelection()
	}
	if row, project, ok := m.selectedProjectRow(); ok && row.Kind == projectListRowWorktree && project.WorktreeKind == model.WorktreeKindLinked {
		return m, m.openWorktreeRemoveConfirmForSelection()
	}
	if !project.PresentOnDisk {
		return m, m.openProjectRemoveConfirmForSelection()
	}
	return m, m.openProjectRemoveConfirmForSelection()
}

func (m Model) setProjectArchivedForSelection(archived bool) (tea.Model, tea.Cmd) {
	project, ok := m.selectedProject()
	if !ok {
		m.status = "No project selected"
		return m, nil
	}
	switch model.NormalizeProjectKind(project.Kind) {
	case model.ProjectKindAgentTask:
		m.status = "Agent tasks use /remove or /task-actions"
		return m, nil
	case model.ProjectKindScratchTask:
		m.status = "Scratch tasks use /task-actions"
		return m, nil
	}
	if archived && project.Archived {
		m.status = fmt.Sprintf("%q is already archived", projectRemovalName(project))
		return m, nil
	}
	if !archived && !project.Archived {
		if !project.InScope {
			m.status = fmt.Sprintf("%q is outside project scope", projectRemovalName(project))
		} else {
			m.status = fmt.Sprintf("%q is already active", projectRemovalName(project))
		}
		return m, nil
	}
	return m, m.setProjectArchivedCmd(project, archived)
}

func (m Model) setProjectArchivedCmd(project model.ProjectSummary, archived bool) tea.Cmd {
	path := filepath.Clean(strings.TrimSpace(project.Path))
	if path == "" || path == "." {
		return nil
	}
	name := projectRemovalName(project)
	return func() tea.Msg {
		var err error
		status := fmt.Sprintf("Archived %q", name)
		ctx, cancel := m.actionContext(tuiQuickActionTimeout)
		defer cancel()
		if archived {
			err = m.svc.ArchiveProject(ctx, path)
			err = timeoutActionError(err, tuiQuickActionTimeout, "archiving the project")
		} else {
			err = m.svc.UnarchiveProject(ctx, path)
			err = timeoutActionError(err, tuiQuickActionTimeout, "unarchiving the project")
			if project.InScope {
				status = fmt.Sprintf("Unarchived %q", name)
			} else {
				status = fmt.Sprintf("Unarchived %q; still outside project scope", name)
			}
		}
		return actionMsg{
			projectPath: path,
			status:      status,
			refresh:     invalidateProjectStructure(""),
			err:         err,
		}
	}
}

func (m Model) openHideActionForSelection() (tea.Model, tea.Cmd) {
	project, ok := m.selectedProject()
	if !ok {
		m.status = "No project selected"
		return m, nil
	}
	switch model.NormalizeProjectKind(project.Kind) {
	case model.ProjectKindAgentTask:
		return m, m.openAgentTaskActionConfirmForSelection()
	case model.ProjectKindScratchTask:
		return m, m.openScratchTaskActionConfirmForSelection()
	}
	if row, project, ok := m.selectedProjectRow(); ok && row.Kind == projectListRowWorktree && project.WorktreeKind == model.WorktreeKindLinked {
		return m, m.openWorktreeRemoveConfirmForSelection()
	}
	m.status = "Select an agent task or linked worktree to archive or remove it"
	return m, nil
}

func projectRemovalName(project model.ProjectSummary) string {
	name := strings.TrimSpace(project.Name)
	if name == "" {
		name = filepath.Base(filepath.Clean(project.Path))
	}
	return name
}

func (m Model) removeProjectCmd(path string) tea.Cmd {
	return func() tea.Msg {
		removeCtx, cancel := m.actionContext(tuiProjectRemoveTimeout)
		defer cancel()
		err := m.svc.ForgetProject(removeCtx, path)
		err = timeoutActionError(err, tuiProjectRemoveTimeout, "removing the stale project")
		return projectRemoveActionMsg{projectPath: path, status: "Removed from list", err: err}
	}
}

func (m Model) ignoreProjectCmd(project model.ProjectSummary) tea.Cmd {
	name := projectRemovalName(project)
	return func() tea.Msg {
		ctx, cancel := m.actionContext(tuiQuickActionTimeout)
		defer cancel()
		err := m.svc.Store().SetIgnoredProjectName(ctx, name, true)
		err = timeoutActionError(err, tuiQuickActionTimeout, fmt.Sprintf("ignoring %q", name))
		status := fmt.Sprintf("Ignored %q", name)
		return ignoredProjectActionMsg{status: status, err: err}
	}
}

func (m Model) removeProjectFromListCmd(project model.ProjectSummary) tea.Cmd {
	name := projectRemovalName(project)
	return func() tea.Msg {
		removeCtx, cancel := m.actionContext(tuiProjectRemoveTimeout)
		defer cancel()
		err := m.svc.Store().SetIgnoredProjectPath(removeCtx, project.Path, true)
		err = timeoutActionError(err, tuiProjectRemoveTimeout, fmt.Sprintf("removing %q from the list", name))
		status := fmt.Sprintf("Removed %q from list", name)
		return projectRemoveActionMsg{projectPath: project.Path, status: status, err: err}
	}
}

func (m Model) openProjectDirInBrowserCmd(path string) tea.Cmd {
	return func() tea.Msg {
		if err := openProjectDirInBrowser(path); err != nil {
			return browserOpenMsg{projectPath: path, err: err}
		}
		return browserOpenMsg{projectPath: path, status: "Opened project in browser"}
	}
}

func (m Model) openProjectDirInTerminalCmd(path string) tea.Cmd {
	return func() tea.Msg {
		if err := openProjectDirInTerminal(path); err != nil {
			return browserOpenMsg{projectPath: path, err: err}
		}
		return browserOpenMsg{projectPath: path, status: "Opened project terminal"}
	}
}

func (m Model) openArtifactCmd(path string) tea.Cmd {
	projectPath := m.codexVisibleProject
	return func() tea.Msg {
		if err := externalPathOpener(path); err != nil {
			return browserOpenMsg{projectPath: projectPath, err: err}
		}
		return browserOpenMsg{projectPath: projectPath, status: "Opened artifact"}
	}
}

func (m Model) openCodexLinkTargetCmd(target codexArtifactOpenTarget) tea.Cmd {
	if strings.TrimSpace(target.Kind) == "url" {
		return m.openBrowserURLCmd(target.Path, "open link", "Opened link")
	}
	return m.openArtifactCmd(target.Path)
}

func (m Model) openCodexLinkTargetFolderCmd(target codexArtifactOpenTarget) tea.Cmd {
	projectPath := m.codexVisibleProject
	return func() tea.Msg {
		folder, err := codexArtifactContainingFolder(target)
		if err != nil {
			return browserOpenMsg{projectPath: projectPath, err: err}
		}
		if err := externalPathOpener(folder); err != nil {
			return browserOpenMsg{projectPath: projectPath, err: err}
		}
		return browserOpenMsg{projectPath: projectPath, status: "Opened containing folder"}
	}
}

func codexArtifactContainingFolder(target codexArtifactOpenTarget) (string, error) {
	if strings.TrimSpace(target.Kind) == "url" {
		return "", fmt.Errorf("links do not have containing folders")
	}
	path := strings.TrimSpace(target.Path)
	return containingFolderForPath(path)
}

func containingFolderForPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("path is required")
	}
	info, err := os.Stat(path)
	if err == nil && info.IsDir() {
		return path, nil
	}
	folder := filepath.Dir(path)
	if folder == "" || folder == "." {
		if err != nil {
			return "", fmt.Errorf("inspect path: %w", err)
		}
		return "", fmt.Errorf("containing folder is not available")
	}
	info, err = os.Stat(folder)
	if err != nil {
		return "", fmt.Errorf("inspect containing folder: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("containing path is not a directory")
	}
	return folder, nil
}

func (m Model) openBrowserURLCmd(rawURL, action, successStatus string) tea.Cmd {
	return func() tea.Msg {
		if err := openBrowserURL(rawURL, action); err != nil {
			return browserOpenMsg{err: err}
		}
		return browserOpenMsg{status: successStatus}
	}
}

func (m Model) openRuntimeURLInBrowserCmd(rawURL string) tea.Cmd {
	return m.openBrowserURLCmd(rawURL, "open runtime URL in browser", "Opened runtime URL in browser")
}

func (m Model) prepareCommitPreviewCmd(path string, intent service.GitActionIntent, message string) tea.Cmd {
	return func() tea.Msg {
		previewMsg := commitPreviewMsg{
			projectPath: path,
			intent:      intent,
			message:     message,
			requestID:   m.commitPreviewRequestID,
		}
		ctx, cancel := m.actionContext(tuiGitActionTimeout)
		defer cancel()
		preview, err := m.svc.PrepareCommit(ctx, path, intent, message)
		err = timeoutActionError(err, tuiGitActionTimeout, "preparing the commit preview")
		previewMsg.preview = preview
		previewMsg.err = err
		if err != nil {
			var noChangesErr service.NoChangesToCommitError
			if errors.As(err, &noChangesErr) {
				if _, refreshErr := m.refreshProjectStatusAfterGitAction(path); refreshErr == nil {
					previewMsg.refreshedProjectState = true
				}
			}
		}
		return previewMsg
	}
}

func (m *Model) startCommitPreview(project model.ProjectSummary, intent service.GitActionIntent, messageOverride string) tea.Cmd {
	m.commitPreviewRequestID++
	projectName := strings.TrimSpace(project.Name)
	if projectName == "" {
		projectName = filepath.Base(filepath.Clean(project.Path))
	}

	preview := service.CommitPreview{
		Intent:        intent,
		ProjectPath:   project.Path,
		ProjectName:   projectName,
		StageMode:     service.GitStageStagedOnly,
		Message:       commitPreviewLoadingMessage(intent, messageOverride),
		LatestSummary: strings.TrimSpace(project.LatestSessionSummary),
	}

	m.err = nil
	m.showHelp = false
	m.gitStatusDialog = nil
	m.gitStatusApplying = false
	m.diffView = nil
	m.commitApplying = false
	m.commitPreview = &preview
	m.commitTodoCompletions = nil
	m.commitTodoSelected = 0
	m.commitPreviewMessageOverride = strings.TrimSpace(messageOverride)
	m.commitPreviewRefreshing = true
	m.setPendingGitSummary(project.Path, commitPreviewPreparingStatus(intent))
	m.status = commitPreviewPreparingStatus(intent)
	return m.prepareCommitPreviewCmd(project.Path, intent, messageOverride)
}

func commitPreviewPreparingStatus(intent service.GitActionIntent) string {
	if intent == service.GitActionFinish {
		return "Preparing finish preview..."
	}
	return "Preparing commit preview..."
}

func commitPreviewLoadingMessage(intent service.GitActionIntent, messageOverride string) string {
	messageOverride = strings.TrimSpace(messageOverride)
	if messageOverride != "" {
		return messageOverride
	}
	if intent == service.GitActionFinish {
		return "Generating finish message..."
	}
	return "Generating commit message..."
}

func (m Model) prepareDiffPreviewCmd(path string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := m.actionContext(tuiGitActionTimeout)
		defer cancel()
		preview, err := m.svc.PrepareDiff(ctx, path)
		err = timeoutActionError(err, tuiGitActionTimeout, "preparing the diff view")
		return diffPreviewMsg{preview: preview, err: err}
	}
}

func (m *Model) startDiffView(projectPath, projectName string) tea.Cmd {
	m.err = nil
	m.showHelp = false
	m.diffView = newDiffViewState(projectPath, projectName)
	m.syncDiffView(true)
	m.setPendingGitSummary(projectPath, "Preparing diff view...")
	m.status = "Preparing diff view..."
	return m.prepareDiffPreviewCmd(projectPath)
}

func (m *Model) startDiffViewFromCommitPreview(preview service.CommitPreview, messageOverride string) tea.Cmd {
	cmd := m.startDiffView(preview.ProjectPath, preview.ProjectName)
	if m.diffView != nil {
		m.diffView.returnToCommitPreview = &commitPreviewReturnState{
			preview:         preview,
			messageOverride: messageOverride,
		}
	}
	return cmd
}

func (m Model) toggleDiffStageCmd(projectPath string, file service.DiffFilePreview, selectStaged bool) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := m.actionContext(tuiGitActionTimeout)
		defer cancel()
		status, err := m.svc.ToggleDiffFileStage(ctx, projectPath, file)
		err = timeoutActionError(err, tuiGitActionTimeout, "staging the diff file")
		if err != nil {
			return diffStageToggleMsg{status: status, path: file.Path, originalPath: file.OriginalPath, selectStaged: selectStaged, err: err}
		}
		preview, err := m.svc.PrepareDiff(ctx, projectPath)
		err = timeoutActionError(err, tuiGitActionTimeout, "refreshing the diff view")
		return diffStageToggleMsg{
			preview:      preview,
			status:       status,
			path:         file.Path,
			originalPath: file.OriginalPath,
			selectStaged: selectStaged,
			err:          err,
		}
	}
}

func (m Model) resumeCommitPreviewCmd(cached service.CommitPreview, messageOverride string) tea.Cmd {
	return func() tea.Msg {
		previewMsg := commitPreviewMsg{
			projectPath: cached.ProjectPath,
			intent:      cached.Intent,
			message:     messageOverride,
			requestID:   m.commitPreviewRequestID,
		}
		if m.svc == nil {
			previewMsg.err = fmt.Errorf("service unavailable")
			return previewMsg
		}

		ctx, cancel := m.actionContext(tuiGitActionTimeout)
		defer cancel()
		currentHash, err := m.svc.CommitPreviewStateHash(ctx, cached.ProjectPath)
		err = timeoutActionError(err, tuiGitActionTimeout, "checking the commit preview state")
		if err != nil {
			previewMsg.err = err
			return previewMsg
		}
		if currentHash == cached.StateHash && currentHash != "" {
			previewMsg.preview = cached
			return previewMsg
		}

		preview, err := m.svc.PrepareCommit(ctx, cached.ProjectPath, cached.Intent, messageOverride)
		err = timeoutActionError(err, tuiGitActionTimeout, "refreshing the commit preview")
		previewMsg.preview = preview
		previewMsg.err = err
		if err != nil {
			var noChangesErr service.NoChangesToCommitError
			if errors.As(err, &noChangesErr) {
				if _, refreshErr := m.refreshProjectStatusAfterGitAction(cached.ProjectPath); refreshErr == nil {
					previewMsg.refreshedProjectState = true
				}
			}
		}
		return previewMsg
	}
}

func diffPreviewSelectionIndex(files []service.DiffFilePreview, path, originalPath string, fallback int) int {
	for i, file := range files {
		if strings.TrimSpace(file.Path) == strings.TrimSpace(path) && strings.TrimSpace(file.OriginalPath) == strings.TrimSpace(originalPath) {
			return i
		}
		if strings.TrimSpace(file.Path) == strings.TrimSpace(path) {
			return i
		}
	}
	if len(files) == 0 {
		return 0
	}
	if fallback < 0 {
		return 0
	}
	if fallback >= len(files) {
		return len(files) - 1
	}
	return fallback
}

func diffPreviewStagedSelectionIndex(files []service.DiffFilePreview, path, originalPath string, fallback int, selectStaged bool) int {
	currentIdx := diffPreviewSelectionIndex(files, path, originalPath, fallback)
	if len(files) == 0 {
		return 0
	}
	for i := 0; i < len(files); i++ {
		idx := (currentIdx + 1 + i) % len(files)
		if files[idx].Staged == selectStaged {
			return idx
		}
	}
	return currentIdx
}

func (m Model) resolveSubmodulesAndContinueCmd(path string, intent service.GitActionIntent, message string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := m.actionContext(tuiGitActionTimeout)
		defer cancel()
		preview, err := m.svc.ResolveSubmodulesAndPrepareCommit(ctx, path, intent, message)
		err = timeoutActionError(err, tuiGitActionTimeout, "resolving submodules")
		return commitPreviewMsg{preview: preview, projectPath: path, intent: intent, message: message, err: err}
	}
}

func (m Model) applyCommitPreviewCmd(preview service.CommitPreview, pushAfterCommit bool) tea.Cmd {
	completedTodoIDs := selectedCommitTodoIDs(m.commitTodoCompletions)
	return func() tea.Msg {
		ctx, cancel := m.actionContext(tuiGitActionTimeout)
		defer cancel()
		result, err := m.svc.ApplyCommit(ctx, preview, pushAfterCommit, completedTodoIDs)
		err = timeoutActionError(err, tuiGitActionTimeout, "applying the commit")
		if err != nil {
			return actionMsg{projectPath: preview.ProjectPath, status: "Commit failed", clearPendingGitSummary: true, err: err}
		}
		status := "Committed " + result.CommitHash
		if result.Pushed {
			status = "Committed " + result.CommitHash + " and pushed"
		}
		if result.Warning != "" {
			status = result.Warning
		}
		refresh, refreshErr := m.refreshProjectStatusAfterGitAction(preview.ProjectPath)
		if refreshErr != nil {
			status = status + ". Repo status will refresh shortly."
		}
		return actionMsg{
			projectPath:            preview.ProjectPath,
			status:                 status,
			clearPendingGitSummary: true,
			refresh:                refresh,
			err:                    nil,
		}
	}
}

func (m Model) pushCmd(path string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := m.actionContext(tuiGitActionTimeout)
		defer cancel()
		result, err := m.svc.PushProject(ctx, path)
		err = timeoutActionError(err, tuiGitActionTimeout, "pushing the project")
		if err != nil {
			return actionMsg{projectPath: path, status: "Push failed", clearPendingGitSummary: true, err: err}
		}
		status := result.Summary
		if strings.TrimSpace(status) == "" {
			status = "Push complete"
		}
		refresh, refreshErr := m.refreshProjectStatusAfterGitAction(path)
		if refreshErr != nil {
			status = status + ". Repo status will refresh shortly."
		}
		return actionMsg{projectPath: path, status: status, clearPendingGitSummary: true, refresh: refresh, err: nil}
	}
}

func (m Model) pullCmd(path string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := m.actionContext(tuiGitActionTimeout)
		defer cancel()
		result, err := m.svc.PullProject(ctx, path)
		err = timeoutActionError(err, tuiGitActionTimeout, "pulling the project")
		if err != nil {
			return actionMsg{projectPath: path, status: "Pull failed", clearPendingGitSummary: true, err: err}
		}
		status := result.Summary
		if strings.TrimSpace(status) == "" {
			status = "Pull complete"
		}
		refresh, refreshErr := m.refreshProjectStatusAfterGitAction(path)
		if refreshErr != nil {
			status = status + ". Repo status will refresh shortly."
		}
		return actionMsg{projectPath: path, status: status, clearPendingGitSummary: true, refresh: refresh, err: nil}
	}
}

func (m Model) refreshProjectStatusAfterGitAction(path string) (projectInvalidationIntent, error) {
	if m.svc == nil {
		return invalidateProjectScan(m.visibleDetailPathForProject(path), false), fmt.Errorf("service unavailable")
	}
	ctx := m.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	refreshCtx, cancel := context.WithTimeout(ctx, tuiProjectStatusRefreshTimeout)
	defer cancel()
	err := m.svc.RefreshProjectStatusWithOptions(refreshCtx, path, service.ScanOptions{
		SkipLinkedWorktreeStatusRefresh: true,
	})
	if err != nil {
		return invalidateProjectScan(m.visibleDetailPathForProject(path), false), err
	}
	return invalidateProjectData(path), nil
}
