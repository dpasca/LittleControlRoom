package tui

import (
	"fmt"
	"strings"
	"time"

	"lcroom/internal/brand"
	"lcroom/internal/model"

	"github.com/charmbracelet/lipgloss"
)

func (m *Model) syncDetailViewport(reset bool) {
	done := m.beginUIPhase("syncDetailViewport", m.currentLatencyProjectPath(), fmt.Sprintf("reset=%t", reset))
	defer done()
	layout := m.bodyLayout()
	m.detailViewport.Width = layout.detailContentWidth
	m.detailViewport.Height = max(1, layout.bottomPaneHeight-2)
	if m.codexVisible() {
		if reset {
			m.detailViewport.GotoTop()
		}
		return
	}

	offset := m.detailViewport.YOffset
	m.detailViewport.SetContent(m.renderDetailContent(layout.detailContentWidth))
	if reset {
		m.detailViewport.GotoTop()
		m.syncRuntimeViewport(true)
		return
	}
	maxOffset := max(0, m.detailViewport.TotalLineCount()-m.detailViewport.Height)
	if offset > maxOffset {
		offset = maxOffset
	}
	m.detailViewport.SetYOffset(offset)
	m.syncRuntimeViewport(false)
}

func (m Model) renderDetailViewport(width, height int) string {
	if height <= 0 {
		return ""
	}

	view := m.detailViewport
	view.Width = max(1, width)
	view.Height = max(1, height)

	if m.detailViewport.Width != width || m.detailViewport.Height <= 0 {
		content := strings.ReplaceAll(m.renderDetailContent(width), "\r\n", "\n")
		view.SetContent(content)
	}

	maxOffset := max(0, view.TotalLineCount()-view.Height)
	if view.YOffset > maxOffset {
		view.SetYOffset(maxOffset)
	}
	if view.YOffset < 0 {
		view.SetYOffset(0)
	}
	return fitPaneContent(view.View(), width, height)
}

func (m Model) View() string {
	// Rendering only consumes snapshots already delivered to the model. Looking
	// up live sessions here makes every Bubble Tea redraw contend with session
	// workers, even when the user is only moving the list selection.
	m.renderCachedSessionStateOnly = true
	m.noteUIProgress("View")
	done := m.beginUIPhase("View", m.currentLatencyProjectPath(), "")
	defer done()
	if m.codexVisible() {
		body := m.renderCodexView()
		if m.actionNoticeDialog != nil {
			width := m.width
			if width <= 0 {
				width = 120
			}
			height := m.height
			if height <= 0 {
				height = 30
			}
			return m.renderActionNoticeDialogOverlay(body, width, height)
		}
		if m.attentionDialog != nil {
			width := m.width
			if width <= 0 {
				width = 120
			}
			height := m.height
			if height <= 0 {
				height = 30
			}
			return m.renderAttentionDialogOverlay(body, width, height)
		}
		if m.skillsDialog != nil {
			width := m.width
			if width <= 0 {
				width = 120
			}
			height := m.height
			if height <= 0 {
				height = 30
			}
			return m.renderSkillsDialogOverlay(body, width, height)
		}
		if m.codexArtifactPicker != nil {
			width := m.width
			if width <= 0 {
				width = 120
			}
			height := m.height
			if height <= 0 {
				height = 30
			}
			return m.renderCodexArtifactPickerOverlay(body, width, height)
		}
		if m.codexModelPickerVisible() {
			width := m.width
			if width <= 0 {
				width = 120
			}
			height := m.height
			if height <= 0 {
				height = 30
			}
			body = m.renderCodexModelPickerOverlay(body, width, height)
			if m.codexLCAgentProviderSetup != nil {
				body = m.renderCodexLCAgentProviderSetupOverlay(body, width, height)
			}
			return body
		}
		if m.settingsLCAgentModelPicker != nil {
			width := m.width
			if width <= 0 {
				width = 120
			}
			height := m.height
			if height <= 0 {
				height = 30
			}
			return m.renderSettingsLCAgentModelPickerOverlay(body, width, height)
		}
		if m.codexPickerVisible {
			width := m.width
			if width <= 0 {
				width = 120
			}
			height := m.height
			if height <= 0 {
				height = 30
			}
			return m.renderCodexPickerOverlay(body, width, height)
		}
		if m.codexInputCopyDialog != nil {
			width := m.width
			if width <= 0 {
				width = 120
			}
			height := m.height
			if height <= 0 {
				height = 30
			}
			return m.renderCodexInputCopyDialogOverlay(body, width, height)
		}
		if m.embeddedSidebarDetail != nil {
			width := m.width
			if width <= 0 {
				width = 120
			}
			height := m.height
			if height <= 0 {
				height = 30
			}
			return m.renderEmbeddedSidebarDetailOverlay(body, width, height)
		}
		if m.newProjectDialog != nil {
			width := m.width
			if width <= 0 {
				width = 120
			}
			height := m.height
			if height <= 0 {
				height = 30
			}
			return m.renderNewProjectOverlay(body, width, height)
		}
		if m.newTaskDialog != nil {
			width := m.width
			if width <= 0 {
				width = 120
			}
			height := m.height
			if height <= 0 {
				height = 30
			}
			return m.renderNewTaskOverlay(body, width, height)
		}
		if m.scratchTaskAction != nil {
			width := m.width
			if width <= 0 {
				width = 120
			}
			height := m.height
			if height <= 0 {
				height = 30
			}
			return m.renderScratchTaskActionOverlay(body, width, height)
		}
		if m.agentTaskAction != nil {
			width := m.width
			if width <= 0 {
				width = 120
			}
			height := m.height
			if height <= 0 {
				height = 30
			}
			return m.renderAgentTaskActionOverlay(body, width, height)
		}
		if m.browserAttentionDialogCanTakeFocus() {
			width := m.width
			if width <= 0 {
				width = 120
			}
			height := m.height
			if height <= 0 {
				height = 30
			}
			return m.renderBrowserAttentionOverlay(body, width, height)
		}
		return body
	}

	layout := m.bodyLayout()
	header := m.renderTopStatusLine(layout.width)
	if m.diffView != nil {
		return strings.Join([]string{header, m.renderDiffView(layout.width, layout.height), m.renderFooter(layout.width)}, "\n")
	}
	listHeight := max(1, layout.listPaneHeight-2)
	bottomHeight := max(1, layout.bottomPaneHeight-2)
	list := m.renderProjectList(layout.listContentWidth, listHeight)
	detail := m.renderDetailViewport(layout.detailContentWidth, bottomHeight)
	runtime := m.renderRuntimePanel(layout.runtimeContentWidth, bottomHeight)

	listPane := m.renderFramedPane(list, layout.width, listHeight, m.focusedPane == focusProjects)
	detailPane := m.renderFramedPane(detail, layout.detailPaneWidth, bottomHeight, m.focusedPane == focusDetail)
	runtimePane := m.renderFramedPane(runtime, layout.runtimePaneWidth, bottomHeight, m.focusedPane == focusRuntime)
	bottomRow := lipgloss.JoinHorizontal(lipgloss.Top, detailPane, " ", runtimePane)
	body := lipgloss.JoinVertical(lipgloss.Left, listPane, bottomRow)
	if m.gitStatusDialog != nil {
		body = m.renderGitStatusDialogOverlay(body, layout.width, layout.height)
	} else if m.commitPreview != nil {
		body = m.renderCommitPreviewOverlay(body, layout.width, layout.height)
	} else if m.codexModelPickerVisible() {
		if m.newTaskDialog != nil {
			body = m.renderNewTaskOverlay(body, layout.width, layout.height)
		}
		body = m.renderCodexModelPickerOverlay(body, layout.width, layout.height)
		if m.codexLCAgentProviderSetup != nil {
			body = m.renderCodexLCAgentProviderSetupOverlay(body, layout.width, layout.height)
		}
	} else if m.newProjectDialog != nil {
		body = m.renderNewProjectOverlay(body, layout.width, layout.height)
	} else if m.newTaskDialog != nil {
		body = m.renderNewTaskOverlay(body, layout.width, layout.height)
	} else if m.runCommandDialog != nil {
		body = m.renderRunCommandOverlay(body, layout.width, layout.height)
	} else if m.bossSetupPrompt != nil {
		body = m.renderBossSetupPromptOverlay(body, layout.width, layout.height)
	} else if m.setupMode {
		body = m.renderSetupOverlay(body, layout.width, layout.height)
		if m.localModelPickerVisible {
			body = m.renderLocalBackendModelPickerOverlay(body, layout.width, layout.height)
		}
		if m.settingsLCAgentProviderVisible {
			body = m.renderSettingsLCAgentProviderPickerOverlay(body, layout.width, layout.height)
		}
		if m.settingsLCAgentModelPicker != nil {
			body = m.renderSettingsLCAgentModelPickerOverlay(body, layout.width, layout.height)
		}
		if m.settingsChoicePicker != nil {
			body = m.renderSettingsChoicePickerOverlay(body, layout.width, layout.height)
		}
		if m.settingsLCAgentSearchPickerVisible {
			body = m.renderSettingsLCAgentWebSearchPickerOverlay(body, layout.width, layout.height)
		}
	} else if m.settingsMode {
		body = m.renderSettingsOverlay(body, layout.width, layout.height)
		if m.localModelPickerVisible {
			body = m.renderLocalBackendModelPickerOverlay(body, layout.width, layout.height)
		}
		if m.settingsAIBackendPickerVisible {
			body = m.renderSettingsAIBackendPickerOverlay(body, layout.width, layout.height)
		}
		if m.settingsBossChatPickerVisible {
			body = m.renderSettingsBossChatBackendPickerOverlay(body, layout.width, layout.height)
		}
		if m.settingsLCAgentProviderVisible {
			body = m.renderSettingsLCAgentProviderPickerOverlay(body, layout.width, layout.height)
		}
		if m.settingsBrowserPickerVisible {
			body = m.renderSettingsBrowserAutomationPickerOverlay(body, layout.width, layout.height)
		}
		if m.settingsLCAgentSearchPickerVisible {
			body = m.renderSettingsLCAgentWebSearchPickerOverlay(body, layout.width, layout.height)
		}
		if m.settingsLCAgentModelPicker != nil {
			body = m.renderSettingsLCAgentModelPickerOverlay(body, layout.width, layout.height)
		}
		if m.settingsChoicePicker != nil {
			body = m.renderSettingsChoicePickerOverlay(body, layout.width, layout.height)
		}
		if m.settingsPrivacyEditor != nil {
			body = m.renderSettingsPrivacyEditorOverlay(body, layout.width, layout.height)
		}
	} else if m.showPerf {
		body = m.renderPerfOverlay(body, layout.width, layout.height)
	} else if m.showAIStats {
		body = m.renderAIStatsOverlay(body, layout.width, layout.height)
	} else if m.projectFilterDialog != nil {
		body = m.renderProjectFilterOverlay(body, layout.width, layout.height)
	} else if m.categoryDialog != nil {
		body = m.renderCategoryDialogOverlay(body, layout.width, layout.height)
	} else if m.archiveDialog != nil {
		body = m.renderArchiveDialogOverlay(body, layout.width, layout.height)
	} else if m.commandMode {
		body = m.renderCommandPaletteOverlay(body, layout.width, layout.height)
	} else if m.errorLogVisible {
		body = m.renderErrorLogOverlay(body, layout.width, layout.height)
	} else if m.mobileDialogOpen {
		body = m.renderMobileDialogOverlay(body, layout.width, layout.height)
	} else if m.cpuDialog != nil {
		body = m.renderCPUDialogOverlay(body, layout.width, layout.height)
	} else if m.portsDialog != nil {
		body = m.renderPortsDialogOverlay(body, layout.width, layout.height)
	} else if m.processDialog != nil {
		body = m.renderProcessDialogOverlay(body, layout.width, layout.height)
	} else if m.skillsDialog != nil {
		body = m.renderSkillsDialogOverlay(body, layout.width, layout.height)
	} else if m.codexPickerVisible {
		body = m.renderCodexPickerOverlay(body, layout.width, layout.height)
	} else if m.ignoredPickerVisible {
		body = m.renderIgnoredPickerOverlay(body, layout.width, layout.height)
	} else if m.browserAttention != nil {
		body = m.renderBrowserAttentionOverlay(body, layout.width, layout.height)
	} else if m.questionNotify != nil {
		body = m.renderQuestionNotifyOverlay(body, layout.width, layout.height)
	}
	if m.todoDialog != nil {
		body = m.renderTodoDialogOverlay(body, layout.width, layout.height)
	}
	if m.cpuRemediationEditor != nil {
		body = m.renderCPURemediationEditorOverlay(body, layout.width, layout.height)
	}
	if m.todoEditor != nil {
		body = m.renderTodoEditorOverlay(body, layout.width, layout.height)
	}
	if m.todoDeleteConfirm != nil {
		body = m.renderTodoDeleteConfirmOverlay(body, layout.width, layout.height)
	}
	if m.scratchTaskAction != nil {
		body = m.renderScratchTaskActionOverlay(body, layout.width, layout.height)
	}
	if m.agentTaskAction != nil {
		body = m.renderAgentTaskActionOverlay(body, layout.width, layout.height)
	}
	if m.projectRemoveConfirm != nil {
		body = m.renderProjectRemoveConfirmOverlay(body, layout.width, layout.height)
	}
	if m.externalStopConfirm != nil {
		body = m.renderExternalProcessStopConfirmOverlay(body, layout.width, layout.height)
	}
	if m.todoExistingWorktree != nil {
		body = m.renderTodoExistingWorktreeOverlay(body, layout.width, layout.height)
	}
	if m.todoCopyDialog != nil {
		body = m.renderTodoCopyDialogOverlay(body, layout.width, layout.height)
	}
	if m.todoPendingLaunchDialog != nil {
		body = m.renderTodoPendingLaunchDialogOverlay(body, layout.width, layout.height)
	}
	if m.todoWorktreeEditor != nil {
		body = m.renderTodoWorktreeEditorOverlay(body, layout.width, layout.height)
	}
	if m.worktreeMergeConfirm != nil {
		body = m.renderWorktreeMergeConfirmOverlay(body, layout.width, layout.height)
	}
	if m.worktreePostMerge != nil {
		body = m.renderWorktreePostMergeOverlay(body, layout.width, layout.height)
	}
	if m.worktreeRemoveConfirm != nil {
		body = m.renderWorktreeRemoveConfirmOverlay(body, layout.width, layout.height)
	}
	if m.repositoryIntegrityDialog != nil {
		body = m.renderRepositoryIntegrityDialogOverlay(body, layout.width, layout.height)
	}
	if m.attentionDialog != nil {
		body = m.renderAttentionDialogOverlay(body, layout.width, layout.height)
	}
	if m.suspendedTurnDialog != nil {
		body = m.renderSuspendedTurnResumeDialogOverlay(body, layout.width, layout.height)
	}
	if m.helpChatMode {
		body = m.renderHelpChatOverlay(body, layout.width, layout.height)
	}
	if m.selfUpdateDialog != nil {
		body = m.renderSelfUpdateDialogOverlay(body, layout.width, layout.height)
	}
	if m.actionNoticeDialog != nil {
		body = m.renderActionNoticeDialogOverlay(body, layout.width, layout.height)
	}

	return strings.Join([]string{header, body, m.renderFooter(layout.width)}, "\n")
}

func (m Model) bodyLayout() bodyLayout {
	width := m.width
	if width <= 0 {
		width = 120
	}

	height := m.height
	if height <= 0 {
		height = 30
	}
	bodyHeight := height - 2 // top line + footer
	if bodyHeight < 8 {
		bodyHeight = 8
	}

	listPaneHeight, bottomPaneHeight := splitBodyHeights(bodyHeight, m.focusedPane)
	detailPaneWidth, runtimePaneWidth := splitBottomPaneWidths(width, m.focusedPane)
	return bodyLayout{
		width:               width,
		height:              bodyHeight,
		listPaneHeight:      listPaneHeight,
		bottomPaneHeight:    bottomPaneHeight,
		listContentWidth:    max(24, width-4),
		detailPaneWidth:     detailPaneWidth,
		runtimePaneWidth:    runtimePaneWidth,
		detailContentWidth:  max(20, detailPaneWidth-4),
		runtimeContentWidth: max(18, runtimePaneWidth-4),
	}
}

func splitBodyHeights(bodyHeight int, focused paneFocus) (int, int) {
	if bodyHeight < 8 {
		bodyHeight = 8
	}

	listHeight := (bodyHeight * 3) / 5
	bottomHeight := bodyHeight - listHeight
	if focused == focusDetail || focused == focusRuntime {
		bottomHeight = (bodyHeight * 13) / 20
		listHeight = bodyHeight - bottomHeight
	}

	if listHeight < 6 {
		listHeight = 6
		bottomHeight = bodyHeight - listHeight
	}
	if bottomHeight < 6 {
		bottomHeight = 6
		listHeight = bodyHeight - bottomHeight
	}
	return listHeight, bottomHeight
}

func splitBottomPaneWidths(totalWidth int, focused paneFocus) (int, int) {
	if totalWidth <= 0 {
		totalWidth = 120
	}
	gap := 1
	available := max(2, totalWidth-gap)
	detailWidth := (available * 3) / 5
	switch focused {
	case focusDetail:
		detailWidth = (available * 17) / 25
	case focusRuntime:
		detailWidth = (available * 2) / 5
	}
	runtimeWidth := available - detailWidth

	minDetail := min(available-18, 28)
	if minDetail < 18 {
		minDetail = 18
	}
	minRuntime := min(available-18, 24)
	if minRuntime < 18 {
		minRuntime = 18
	}

	if detailWidth < minDetail {
		detailWidth = minDetail
		runtimeWidth = available - detailWidth
	}
	if runtimeWidth < minRuntime {
		runtimeWidth = minRuntime
		detailWidth = available - runtimeWidth
	}
	if detailWidth < 18 {
		detailWidth = max(18, available/2)
		runtimeWidth = available - detailWidth
	}
	if runtimeWidth < 18 {
		runtimeWidth = max(18, available/2)
		detailWidth = available - runtimeWidth
	}
	return detailWidth, runtimeWidth
}

func (m Model) renderTopStatusLine(width int) string {
	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("81")).Render(brand.Name)
	rawStatus := singleLineStatusText(m.status)
	status := rawStatus
	if m.err != nil {
		errText := singleLineStatusText(m.err.Error())
		if status == "" {
			status = "error: " + errText
		} else {
			status = fmt.Sprintf("%s | error: %s", status, errText)
		}
	}

	mobileNotice := m.renderMobileServerStatusNotice()
	prioritizeMobileStatus := mobileNotice != "" || strings.HasPrefix(rawStatus, "Mobile client ")
	restartWarmupNotice := m.renderRestartWarmupNotice()
	prioritizeTopStatus := prioritizeMobileStatus || restartWarmupNotice != ""
	statusParts := make([]string, 0, 5)
	if mobileNotice != "" {
		statusParts = append(statusParts, mobileNotice)
	}
	if restartWarmupNotice != "" {
		statusParts = append(statusParts, restartWarmupNotice)
	}
	if strings.TrimSpace(status) != "" {
		statusParts = append(statusParts, m.renderTopStatusMessage(rawStatus, status))
	}
	if aiNotice := m.renderAIBackendStatusNotice(); aiNotice != "" {
		statusParts = append(statusParts, aiNotice)
	}
	if project, ok := m.selectedProject(); ok {
		if resolver, found := m.mergeConflictResolverForProject(project.Path); found {
			switch resolver.Phase {
			case mergeConflictResolverStarting, mergeConflictResolverRunning:
				statusParts = append(statusParts, topStatusResolverBadgeStyle.Render("RESOLVING"))
				statusParts = append(statusParts, detailValueStyle.Render("background "+resolver.provider().Label()+" resolver active"))
			case mergeConflictResolverChecking:
				statusParts = append(statusParts, topStatusResolverBadgeStyle.Render("CHECKING GIT"))
			case mergeConflictResolverNeedsAttention:
				statusParts = append(statusParts, topStatusWarningBadgeStyle.Render("RESOLVER WAITING"))
				statusParts = append(statusParts, detailWarningStyle.Render("background resolver needs input; see project detail"))
			case mergeConflictResolverRefreshFailed:
				statusParts = append(statusParts, topStatusWarningBadgeStyle.Render("GIT STATUS UNKNOWN"))
				statusParts = append(statusParts, detailWarningStyle.Render(resolver.summary(m.currentTime())))
			case mergeConflictResolverFailed, mergeConflictResolverConflictsRemain:
				statusParts = append(statusParts, topStatusConflictBadgeStyle.Render("RESOLVER BLOCKED"))
				statusParts = append(statusParts, detailConflictStyle.Render(resolver.summary(m.currentTime())))
			case mergeConflictResolverResolved:
				statusParts = append(statusParts, topStatusResolverBadgeStyle.Render("RESOLVED"))
			}
		} else if project.RepoConflict {
			statusParts = append(statusParts, topStatusConflictBadgeStyle.Render("MERGE CONFLICT"))
			statusParts = append(statusParts, detailConflictStyle.Render("selected repo has unmerged files; use /resolve"))
		}
	}
	if project, ok := m.selectedProject(); ok {
		if state, found := m.repositoryIntegrityStateForProject(project.Path); found && state.Displaced && model.NormalizeRepositoryIntegrityMode(state.Mode) != model.RepositoryIntegrityModeOff {
			statusParts = append(statusParts, topStatusWarningBadgeStyle.Render("ROOT CHECKOUT"))
			message := fmt.Sprintf("root is on %s; expected %s; press I", state.ActualBranch, state.ExpectedBranch)
			if state.Acknowledged {
				message = fmt.Sprintf("root remains on %s; expected %s; acknowledged; press I", state.ActualBranch, state.ExpectedBranch)
			}
			statusParts = append(statusParts, detailWarningStyle.Render(message))
		}
	}

	segments := []string{title}
	if !prioritizeTopStatus {
		if actions := m.renderTopStatusActions(width); actions != "" {
			segments = append(segments, actions)
		}
	}
	if len(statusParts) > 0 {
		segments = append(segments, joinFooterSegments(statusParts...))
	}
	rightSegment := joinFooterSegments(m.renderSelfUpdateTopStatusIndicator(), m.renderMobileTopStatusIndicator(width), m.renderTopCPUUsageSegment())
	if prioritizeTopStatus {
		rightSegment = ""
	}
	return renderLineWithRightSegment(strings.Join(segments, "  "), rightSegment, width)
}

type topStatusSeverity int

const (
	topStatusSeverityNormal topStatusSeverity = iota
	topStatusSeverityWarning
	topStatusSeverityDanger
)

func (m Model) renderTopStatusMessage(rawStatus, displayStatus string) string {
	displayStatus = strings.TrimSpace(displayStatus)
	if displayStatus == "" {
		return ""
	}

	switch topStatusSeverityForMessage(rawStatus, m.err) {
	case topStatusSeverityWarning:
		if !m.topStatusWarningPulseActive(rawStatus) {
			return renderTopStatusWarningStableMessage(displayStatus)
		}
		return renderTopStatusWarningMessage(displayStatus, m.spinnerFrame)
	case topStatusSeverityDanger:
		return renderTopStatusDangerMessage(displayStatus, m.spinnerFrame)
	default:
		return renderFooterStatus(displayStatus)
	}
}

// Until status updates carry structured severity, keep the top-banner alert rules
// focused on explicit action-required and failure messages that should stand out.
func topStatusSeverityForMessage(status string, err error) topStatusSeverity {
	if err != nil {
		return topStatusSeverityDanger
	}

	status = strings.TrimSpace(status)
	if status == "" {
		return topStatusSeverityNormal
	}

	lowerStatus := strings.ToLower(status)
	if topStatusShowsRecoveryProgress(lowerStatus) {
		return topStatusSeverityNormal
	}
	if topStatusIsClipboardConfirmation(lowerStatus) {
		return topStatusSeverityNormal
	}
	switch {
	case strings.Contains(lowerStatus, "failed"),
		strings.Contains(lowerStatus, "merge conflict"),
		strings.Contains(lowerStatus, " error"):
		return topStatusSeverityDanger
	case topStatusNeedsAttention(status):
		return topStatusSeverityWarning
	default:
		return topStatusSeverityNormal
	}
}

func topStatusShowsRecoveryProgress(status string) bool {
	for _, prefix := range []string{
		"scanning and retrying ",
		"retrying ",
	} {
		if strings.HasPrefix(status, prefix) {
			return true
		}
	}
	return false
}

func topStatusIsClipboardConfirmation(status string) bool {
	status = strings.TrimSpace(status)
	if status == "" {
		return false
	}
	return strings.HasSuffix(status, " copied to clipboard") ||
		(strings.HasPrefix(status, "copied ") && strings.HasSuffix(status, " to clipboard"))
}

func topStatusNeedsAttention(status string) bool {
	for _, prefix := range []string{
		"Stop the runtime before ",
		"Close the embedded agent session before ",
		"A commit is still in progress.",
		"Pull first:",
	} {
		if strings.HasPrefix(status, prefix) {
			return true
		}
	}

	for _, snippet := range []string{
		" launch blocked",
		"Resolve or abort the in-progress Git operation before ",
		"Commit or discard changes before ",
		"Finish or close it before ",
		"Close it before ",
		"Switch it to ",
		"Pull first:",
	} {
		if strings.Contains(status, snippet) {
			return true
		}
	}

	return false
}

func (m Model) topStatusWarningPulseActive(status string) bool {
	status = strings.TrimSpace(status)
	if !topStatusWarningSettlesAfterAttentionPulse(status) {
		return true
	}
	return status != "" &&
		status == strings.TrimSpace(m.topStatusAttentionPulseStatus) &&
		m.topStatusAttentionPulseUntil.After(m.currentTime())
}

func topStatusWarningSettlesAfterAttentionPulse(status string) bool {
	return strings.HasPrefix(strings.TrimSpace(status), "Close the embedded agent session before ")
}

func (m *Model) markTopStatusAttentionPulse(status string) {
	status = strings.TrimSpace(status)
	if !topStatusWarningSettlesAfterAttentionPulse(status) {
		return
	}
	m.topStatusAttentionPulseStatus = status
	m.topStatusAttentionPulseUntil = m.currentTime().Add(topStatusAttentionPulseDuration)
}

func renderTopStatusWarningStableMessage(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	return topStatusWarningBadgeStyle.Render(text)
}

func renderTopStatusWarningMessage(text string, spinnerFrame int) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	if spinnerFrame%2 == 0 {
		return topStatusWarningBadgeStyle.Render(text)
	}
	return topStatusWarningPulseBadgeStyle.Render(text)
}

func renderTopStatusDangerMessage(text string, spinnerFrame int) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	if spinnerFrame%2 == 0 {
		return topStatusDangerBadgeStyle.Render(text)
	}
	return topStatusDangerPulseBadgeStyle.Render(text)
}

func (m Model) renderTopStatusActions(width int) string {
	if m.diffView != nil {
		return ""
	}
	if width < 72 {
		return ""
	}
	actions := []footerAction{
		footerNavAction("f", "filter"),
		footerNavAction("/", "command"),
		footerNavAction("`", "chat"),
	}
	if len(m.errorLogEntries) > 0 && width >= 112 {
		actions = append(actions, footerNavAction("/errors", "log"))
	}
	return renderFooterActionList(actions...)
}

func paneBoxStyle(focused bool) lipgloss.Style {
	borderColor := lipgloss.Color("238")
	if focused {
		borderColor = lipgloss.Color("81")
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Padding(0, 1)
}

func (m Model) renderFramedPane(content string, width, innerHeight int, focused bool) string {
	contentWidth := max(0, width-4)
	content = fitPaneContent(content, contentWidth, innerHeight)
	return paneBoxStyle(focused).Render(content)
}

func hFramedPaneStyle(focused bool) lipgloss.Style {
	borderColor := lipgloss.Color("238")
	if focused {
		borderColor = lipgloss.Color("81")
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		BorderLeft(false).
		BorderRight(false)
}

func (m Model) renderHFramedPane(content string, width, innerHeight int, focused bool) string {
	content = fitPaneContent(content, width, innerHeight)
	return hFramedPaneStyle(focused).Width(width).Render(content)
}

func (m Model) selectedProject() (model.ProjectSummary, bool) {
	if m.selected < 0 || m.selected >= len(m.projects) {
		return model.ProjectSummary{}, false
	}
	return m.projects[m.selected], true
}

func (m Model) renderProjectArchiveTabs(width int) string {
	return m.renderProjectArchiveTabsAt(width, m.currentTime(), m.assessmentStallThreshold())
}

func (m Model) renderProjectArchiveTabsAt(width int, now time.Time, stallThreshold time.Duration) string {
	parts := []string{}
	currentMode := m.archiveMode
	if currentMode == "" {
		currentMode = projectArchiveMain
	}
	for _, tab := range m.projectTabDescriptors() {
		label := tab.label
		marker := ""
		if !(m.privacyMode && tab.private) && m.projectTabHasActionableAttentionAt(tab, now, stallThreshold) {
			marker = "*"
		}
		if m.privacyMode && tab.private {
			label = "********"
		} else if width <= 0 || width >= 34 {
			label = fmt.Sprintf("%s%s %d", label, marker, m.projectTabCount(tab))
		} else {
			label += marker
		}
		selected := tab.mode == currentMode
		if tab.mode == projectArchiveCategory {
			selected = selected && strings.TrimSpace(tab.categoryID) == strings.TrimSpace(m.selectedCategoryID)
		}
		parts = append(parts, renderProjectArchiveTab(label, selected))
	}
	line := strings.Join(parts, " ")
	hint := renderProjectArchiveTabHotkey()
	categoryHint := renderProjectCategoryCommandHint()
	if width <= 0 || lipgloss.Width(line)+2+lipgloss.Width(hint)+2+lipgloss.Width(categoryHint) <= width {
		line += "  " + hint + "  " + categoryHint
	} else if width <= 0 || lipgloss.Width(line)+2+lipgloss.Width(hint) <= width {
		line += "  " + hint
	} else if lipgloss.Width(line)+2 <= width {
		line += " " + projectListTabHotkeyStyle.Render("a")
	}
	if width > 0 {
		return fitStyledWidth(line, width)
	}
	return line
}

func (m Model) projectTabCount(tab projectTabDescriptor) int {
	if m.projectTabProjects != nil {
		if tab.mode == projectArchiveArchived {
			return m.archivedProjectTabCount
		}
		categoryID := strings.TrimSpace(tab.categoryID)
		return len(m.projectTabProjects[categoryID]) + len(m.projectTabAgentTasks[categoryID])
	}
	if tab.mode == projectArchiveArchived {
		return len(m.projectsVisibleForPrivacy(m.archivedProjects))
	}
	count := 0
	for _, project := range m.allProjects {
		if strings.TrimSpace(project.CategoryID) == strings.TrimSpace(tab.categoryID) {
			count++
		}
	}
	for _, task := range m.openAgentTasks {
		if !agentTaskIsOpen(task) {
			continue
		}
		if strings.TrimSpace(task.CategoryID) == strings.TrimSpace(tab.categoryID) {
			count++
		}
	}
	return count
}

func (m Model) projectTabHasActionableAttention(tab projectTabDescriptor) bool {
	return m.projectTabHasActionableAttentionAt(tab, m.currentTime(), m.assessmentStallThreshold())
}

func (m Model) projectTabHasActionableAttentionAt(tab projectTabDescriptor, now time.Time, stallThreshold time.Duration) bool {
	switch tab.mode {
	case projectArchiveArchived:
		return false
	case projectArchiveMain, projectArchiveCategory:
		categoryID := strings.TrimSpace(tab.categoryID)
		projects := m.allProjects
		tasks := m.openAgentTasks
		if m.projectTabProjects != nil {
			projects = m.projectTabProjects[categoryID]
			tasks = m.projectTabAgentTasks[categoryID]
		}
		for _, project := range projects {
			if m.projectTabProjects == nil && strings.TrimSpace(project.CategoryID) != categoryID {
				continue
			}
			if m.projectSummaryHasTabAttentionAt(project, now, stallThreshold) {
				return true
			}
		}
		for _, task := range tasks {
			if !agentTaskIsOpen(task) || (m.projectTabAgentTasks == nil && strings.TrimSpace(task.CategoryID) != categoryID) {
				continue
			}
			if agentTaskHasTabAttention(task) {
				return true
			}
		}
	}
	return false
}

func (m Model) projectSummaryHasTabAttention(project model.ProjectSummary) bool {
	return m.projectSummaryHasTabAttentionAt(project, m.currentTime(), m.assessmentStallThreshold())
}

func (m Model) projectSummaryHasTabAttentionAt(project model.ProjectSummary, now time.Time, stallThreshold time.Duration) bool {
	if snapshot, ok := m.cachedLiveCodexSnapshot(project.Path); ok {
		if snapshot.PendingApproval != nil {
			return true
		}
		if _, ok := m.browserAttentionFromSnapshot(snapshot); ok {
			return true
		}
		if snapshot.PendingToolInput != nil || snapshot.PendingElicitation != nil {
			return true
		}
	}
	_, category, ok := visibleAssessmentStatusLabelAt(project, now, stallThreshold)
	return ok && sessionCategoryHasTabAttention(category)
}

func agentTaskHasTabAttention(task model.AgentTask) bool {
	return sessionCategoryHasTabAttention(agentTaskClassificationType(task))
}

func sessionCategoryHasTabAttention(category model.SessionCategory) bool {
	switch category {
	case model.SessionCategoryBlocked,
		model.SessionCategoryWaitingForUser,
		model.SessionCategoryNeedsFollowUp:
		return true
	default:
		return false
	}
}

func renderProjectArchiveTab(label string, selected bool) string {
	label = strings.TrimSpace(label)
	if label == "" {
		label = "Tab"
	}
	if selected {
		return projectListActiveTabStyle.Render("[" + label + "]")
	}
	return projectListInactiveTabStyle.Render(" " + label + " ")
}

func renderProjectArchiveTabHotkey() string {
	return projectListTabHotkeyStyle.Render("a") + projectListTabHintStyle.Render(" cycle")
}

func renderProjectCategoryCommandHint() string {
	return projectListTabHintStyle.Render("/category")
}

func (m Model) renderProjectList(width, height int) string {
	m.renderCachedSessionStateOnly = true
	now := m.currentTime()
	stallThreshold := m.assessmentStallThreshold()
	tabs := m.renderProjectArchiveTabsAt(width, now, stallThreshold)
	if len(m.projects) == 0 {
		message := ""
		if m.loading {
			message = "Loading..."
		} else if filterLabel := m.projectFilterSummaryLabel(24); filterLabel != "" {
			if m.archiveMode == projectArchiveArchived {
				message = fmt.Sprintf("No archived projects match %s\nPress f or /filter to change it", filterLabel)
			} else {
				message = fmt.Sprintf("No projects match %s\nPress f or /filter to change it", filterLabel)
			}
		} else if m.archiveMode == projectArchiveArchived {
			message = "No archived projects\nUse /archive to park a project here"
		} else if m.archiveMode == projectArchiveCategory {
			message = fmt.Sprintf("No projects in %s\nUse /category move to place the selected item here", m.currentProjectTabLabel())
		} else if len(m.projectsVisibleForPrivacy(m.allProjects)) > 0 && m.visibility == visibilityAIFolders {
			message = "No AI-linked folders\nUse /non-ai-folders on to include folders without AI activity"
		} else if len(m.projectsVisibleForPrivacy(m.archivedProjects)) > 0 {
			message = "No Main projects\nPress a to cycle tabs"
		} else {
			message = "No projects detected\nUse /settings to set your project search paths"
		}
		if tabs != "" {
			return tabs + "\n" + message
		}
		return message
	}

	headerRows := 1
	if tabs != "" {
		headerRows++
	}
	if height < headerRows+2 {
		height = headerRows + 2
	}
	visible := height - headerRows
	if visible < 1 {
		visible = 1
	}

	metaParts := []string{
		fmt.Sprintf("sort=%s", m.sortMode),
		fmt.Sprintf("view=%s", visibilityShortLabel(m.visibility)),
	}
	filterLabel := m.projectFilterSummaryLabel(16)
	if filterLabel != "" {
		metaParts = append(metaParts, "filter:"+filterLabel)
	}
	if m.privacyMode {
		metaParts = append(metaParts, "privacy")
	}
	meta := "  (" + strings.Join(metaParts, " ") + ")"
	columnWidth := width
	if filterLabel != "" {
		if reserved := lipgloss.Width(meta); reserved > 0 && width > reserved+53 {
			columnWidth = width - reserved
		}
	}
	if columnWidth > projectListSelectionGutterWidth {
		columnWidth -= projectListSelectionGutterWidth
	}
	projectW, assessmentW := projectListColumnWidths(columnWidth)
	rows := make([]string, 0, visible+3)
	if tabs != "" {
		rows = append(rows, tabs)
	}
	header := renderProjectListHeader(projectW, assessmentW)
	if lipgloss.Width(header)+lipgloss.Width(meta) <= width {
		header += meta
	}
	if width > 0 {
		header = fitStyledWidth(header, width)
	}
	rows = append(rows, header)
	showKindSections := false

	selected := m.selected
	if selected < 0 {
		selected = 0
	}
	if selected >= len(m.projects) {
		selected = len(m.projects) - 1
	}

	start := m.offset
	if start < 0 {
		start = 0
	}
	maxOffset := max(0, len(m.projects)-1)
	if start > maxOffset {
		start = maxOffset
	}
	if selected < start {
		start = selected
	}
	for start < selected && projectListVisibleLineCount(m.projects, start, selected+1, showKindSections) > visible {
		start++
	}
	end := start
	for end < len(m.projects) {
		if next := projectListVisibleLineCount(m.projects, start, end+1, showKindSections); next > visible && end > start {
			break
		}
		end++
		if projectListVisibleLineCount(m.projects, start, end, showKindSections) >= visible {
			break
		}
	}
	if end <= start {
		end = min(len(m.projects), start+1)
	}
	for start > 0 {
		if next := projectListVisibleLineCount(m.projects, start-1, end, showKindSections); next > visible {
			break
		}
		start--
	}
	for i := start; i < end; i++ {
		p := m.projects[i]
		if showKindSections && projectListSectionStartsAt(m.projects, start, i) {
			rows = append(rows, detailSectionStyle.Render(projectListSectionLabel(p)))
		}
		rootPath := projectWorktreeRootPath(p)
		orphanedCount := m.orphanedWorktreeCount(rootPath)
		rowMeta := projectListRow{
			Kind:        projectListRowStandalone,
			ProjectPath: p.Path,
			RootPath:    rootPath,
		}
		if i >= 0 && i < len(m.projectRows) {
			rowMeta = m.projectRows[i]
		}
		selectedRow := i == m.selected
		selectionFlashRow := selectedRow && m.selectionFlashActive()
		// Resolve pulse state once per row. cellStyle is applied to every
		// column, so cache reads inside that closure multiply expensive
		// snapshot copies across the entire visible list.
		approvalPulseRow := false
		browserPulseRow := false
		questionPulseRow := false
		if !selectionFlashRow {
			approvalPulseRow = m.projectApprovalPulseActive(p.Path)
			if !approvalPulseRow {
				browserPulseRow = m.projectBrowserPulseActive(p.Path)
			}
			if !approvalPulseRow && !browserPulseRow {
				questionPulseRow = m.projectQuestionPulseActive(p.Path)
			}
		}
		cellStyle := func(style lipgloss.Style) lipgloss.Style {
			style = projectListCellStyle(style, selectedRow)
			if selectionFlashRow {
				style = projectListSelectionFlashStyle(style)
			} else if approvalPulseRow {
				style = approvalPulseStyle(style)
			} else if browserPulseRow {
				style = browserPulseStyle(style)
			} else if questionPulseRow {
				style = questionPulseStyle(style)
			}
			return style
		}
		last := formatListActivityTime(now, p.LastActivity)
		flagIndicators := m.projectRepoWarningIndicator(p, m.spinnerFrame) + projectUnreadIndicator(p, now, stallThreshold)
		attention := projectAttentionLabelForScore(m.projectAttentionScore(p))
		namePrefix := ""
		nameLabel := p.Name
		statusText := projectListStatusAt(p, now, stallThreshold)
		assessmentText := m.projectAssessmentDisplayTextAt(p, now, stallThreshold)
		statusStyle := m.projectListAssessmentStatusStyleAt(p, now, stallThreshold)
		summaryStyle := m.projectListAssessmentSummaryStyleAt(p, now, stallThreshold)
		nameStyle := lipgloss.NewStyle().Width(projectW).Bold(selectedRow)
		agentTaskRow := false
		if task, ok := m.agentTaskForProjectPath(p.Path); ok {
			agentTaskRow = true
			statusText = agentTaskListStatus(task)
			assessmentText = agentTaskListSummary(task)
			statusStyle = agentTaskStatusStyle(task)
			summaryStyle = detailValueStyle
			if model.NormalizeAgentTaskStatus(task.Status) == model.AgentTaskStatusWaiting {
				summaryStyle = detailWarningStyle
			}
		}
		browserAttentionRow := false
		if browserAttention, ok := m.projectPendingBrowserAttention(p.Path); ok {
			browserAttentionRow = true
			statusText = "browser"
			assessmentText = browserAttentionListSummary(browserAttention)
			statusStyle = detailWarningStyle
			summaryStyle = detailWarningStyle
		}
		pendingLaunch, pendingLaunchRow := m.todoPendingLaunchForProjectPath(p.Path)
		switch rowMeta.Kind {
		case projectListRowRepo:
			if rowMeta.LinkedCount > 0 {
				if rowMeta.LinkedPendingIntegrationCount > 0 {
					nameStyle = nameStyle.Inherit(detailWarningStyle).Bold(true)
					summaryStyle = detailWarningStyle
				}
				if badge := worktreeLinkedBadgeSummary(rowMeta.LinkedCount, rowMeta.LinkedActiveCount, rowMeta.LinkedDirtyCount, rowMeta.LinkedPendingIntegrationCount, orphanedCount); badge != "" {
					if assessmentText == "-" {
						assessmentText = badge
					} else {
						assessmentText += "  " + badge
					}
				}
			}
		case projectListRowWorktree:
			namePrefix = "  ↳ "
			nameLabel = projectWorktreeLabel(p)
			if worktreeHasPendingIntegration(p) {
				nameStyle = nameStyle.Inherit(detailWarningStyle).Bold(true)
				summaryStyle = detailWarningStyle
			}
		case projectListRowPendingWorktree:
			frame := ""
			if len(spinnerFrames) > 0 {
				frame = spinnerFrames[m.spinnerFrame%len(spinnerFrames)] + " "
			}
			namePrefix = "  ↳ " + frame
			nameLabel = p.Name
			statusText = "creating"
			if pendingLaunch != nil {
				assessmentText = todoPendingLaunchListSummary(*pendingLaunch, now)
			} else {
				assessmentText = "preparing checkout"
			}
			statusStyle = detailWarningStyle
			summaryStyle = detailWarningStyle
			nameStyle = nameStyle.Inherit(detailWarningStyle).Bold(true)
		default:
			switch model.NormalizeProjectKind(p.Kind) {
			case model.ProjectKindScratchTask:
				nameLabel = "[T] " + nameLabel
			case model.ProjectKindAgentTask:
				nameLabel = "[A] " + nameLabel
			}
			if projectIsWorktreeRoot(p) {
				if badge := worktreeLinkedBadgeSummary(0, 0, 0, 0, orphanedCount); badge != "" {
					if assessmentText == "-" {
						assessmentText = badge
					} else {
						assessmentText += "  " + badge
					}
				}
			}
		}
		nameRender := projectListNameCellText(namePrefix, nameLabel, projectW, selectedRow, m.marqueeOffset)
		assessment := truncateText(assessmentText, assessmentW)
		runtimeSnapshot := m.projectRuntimeSnapshot(p.Path)
		agentLabel, agentTag, agentLive := m.projectAgentDisplay(p, now)
		if pendingLaunchRow && pendingLaunch != nil {
			agentLabel = pendingLaunch.Provider.SourceTag()
			agentTag = pendingLaunch.Provider.SourceTag()
			agentLive = true
		}
		if liveSummary, ok := m.projectLiveEngineerAssessmentSummary(p, now); ok && !agentTaskRow && !browserAttentionRow && !pendingLaunchRow {
			statusText = "working"
			assessmentText = liveSummary
			statusStyle = classificationCategoryStyle(model.SessionCategoryInProgress)
			summaryStyle = detailValueStyle
		}
		if resolver, ok := m.mergeConflictResolverForProject(p.Path); ok && !agentTaskRow && !browserAttentionRow && !pendingLaunchRow {
			switch resolver.Phase {
			case mergeConflictResolverStarting, mergeConflictResolverRunning:
				statusText = "resolve"
				assessmentText = resolver.summary(now)
				statusStyle = classificationCategoryStyle(model.SessionCategoryInProgress)
				summaryStyle = detailValueStyle
			case mergeConflictResolverChecking:
				statusText = "checking"
				assessmentText = resolver.summary(now)
				statusStyle = classificationCategoryStyle(model.SessionCategoryInProgress)
				summaryStyle = detailValueStyle
			case mergeConflictResolverNeedsAttention:
				statusText = "waiting"
				assessmentText = resolver.summary(now)
				statusStyle = detailWarningStyle
				summaryStyle = detailWarningStyle
			case mergeConflictResolverRefreshFailed:
				statusText = "unknown"
				assessmentText = resolver.summary(now)
				statusStyle = detailWarningStyle
				summaryStyle = detailWarningStyle
			case mergeConflictResolverFailed, mergeConflictResolverConflictsRemain:
				statusText = "blocked"
				assessmentText = resolver.summary(now)
				statusStyle = detailConflictStyle
				summaryStyle = detailConflictStyle
			case mergeConflictResolverResolved:
				statusText = "resolved"
				assessmentText = resolver.summary(now)
				statusStyle = detailValueStyle
				summaryStyle = detailValueStyle
			}
		}
		todoCount := projectTODOCountLabel(p.OpenTODOCount)
		runLabel, runState := projectRunSummary(runtimeSnapshot, p.RunCommand)
		if localRunLabel, ok := m.projectLocalInstanceRunSummary(p.Path); ok && runState == projectRunIdle {
			runLabel = localRunLabel
			runState = projectRunActive
		}
		if processFlag := m.projectProcessRunFlag(p.Path); processFlag != "" {
			if runLabel == "" {
				runLabel = processFlag
			} else {
				runLabel = processFlag + " " + runLabel
			}
			runState = projectRunError
		}
		assessment = truncateText(assessmentText, assessmentW)
		runRender := truncateText(runLabel, projectListRunWidth)
		if selectedRow && len([]rune(runLabel)) > projectListRunWidth {
			runRender = marqueeScrollText(runLabel, projectListRunWidth, m.marqueeOffset)
		}
		assessmentRender := assessment
		if selectedRow && len([]rune(assessmentText)) > assessmentW {
			assessmentRender = marqueeScrollText(assessmentText, assessmentW, m.marqueeOffset)
		}
		selectionMarker := " "
		selectionMarkerStyle := lipgloss.NewStyle().Width(projectListSelectionGutterWidth)
		if selectedRow {
			selectionMarker = ">"
			selectionMarkerStyle = selectionMarkerStyle.Foreground(lipgloss.Color("81")).Bold(true)
		}
		row := lipgloss.JoinHorizontal(
			lipgloss.Top,
			cellStyle(selectionMarkerStyle).Render(selectionMarker),
			flagIndicators+cellStyle(lipgloss.NewStyle().Width(4).Align(lipgloss.Right).Bold(selectedRow)).Render(attention),
			" ",
			cellStyle(statusStyle.Width(8)).Render(statusText),
			" ",
			cellStyle(lipgloss.NewStyle().Width(10)).Render(last),
			" ",
			cellStyle(sourceStyleForTag(agentTag, agentLive).Width(projectListAgentWidth).Align(lipgloss.Left)).Render(truncateText(agentLabel, projectListAgentWidth)),
			" ",
			cellStyle(todoListIndicatorStyle.Width(projectListTODOWidth).Align(lipgloss.Right)).Render(todoCount),
			" ",
			cellStyle(projectRunStyle(runState).Width(projectListRunWidth).Align(lipgloss.Left)).Render(runRender),
			"  ",
			cellStyle(nameStyle).Render(nameRender),
			"  ",
			cellStyle(summaryStyle.Width(assessmentW).Bold(selectedRow)).Render(assessmentRender),
		)
		if width > 0 {
			row = fitStyledWidth(row, width)
		}
		if selectedRow {
			rowStyle := projectListSelectedRowStyle
			if selectionFlashRow {
				rowStyle = projectListSelectionFlashStyle(rowStyle)
			}
			row = rowStyle.Render(row)
		}
		rows = append(rows, row)
	}
	if end < len(m.projects) {
		rows = append(rows, fmt.Sprintf("... %d more rows", len(m.projects)-end))
	}
	return strings.Join(rows, "\n")
}

func projectListHasKindSections(projects []model.ProjectSummary) bool {
	if len(projects) < 2 {
		return false
	}
	first := model.NormalizeProjectKind(projects[0].Kind)
	for _, project := range projects[1:] {
		if model.NormalizeProjectKind(project.Kind) != first {
			return true
		}
	}
	return false
}

func projectListSectionLabel(project model.ProjectSummary) string {
	switch model.NormalizeProjectKind(project.Kind) {
	case model.ProjectKindScratchTask:
		return "Scratch Tasks"
	case model.ProjectKindAgentTask:
		return "Agent Tasks"
	}
	return "Projects"
}

func projectListSectionStartsAt(projects []model.ProjectSummary, start, index int) bool {
	if index < start || index < 0 || index >= len(projects) {
		return false
	}
	if index == start {
		return true
	}
	return model.NormalizeProjectKind(projects[index].Kind) != model.NormalizeProjectKind(projects[index-1].Kind)
}

func projectListVisibleLineCount(projects []model.ProjectSummary, start, end int, showSections bool) int {
	if start < 0 {
		start = 0
	}
	if end > len(projects) {
		end = len(projects)
	}
	if start >= end {
		return 0
	}
	count := 0
	for i := start; i < end; i++ {
		if showSections && projectListSectionStartsAt(projects, start, i) {
			count++
		}
		count++
	}
	return count
}

func (m Model) renderDetailContent(width int) string {
	m.renderCachedSessionStateOnly = true
	done := m.beginUIPhase("renderDetailContent", m.currentLatencyProjectPath(), fmt.Sprintf("width=%d", width))
	defer done()
	p, ok := m.selectedProject()
	if !ok {
		if m.archiveMode == projectArchiveArchived {
			return "No archived project selected\nPress a to cycle tabs"
		}
		if m.archiveMode == projectArchiveCategory {
			return fmt.Sprintf("No project selected in %s\nPress a to cycle tabs", m.currentProjectTabLabel())
		}
		if len(m.projectListSourceProjects()) > 0 && m.visibility == visibilityAIFolders {
			return "No AI-linked folder selected\nUse /non-ai-folders on to include folders without AI activity"
		}
		return "Select a project"
	}
	if task, ok := m.agentTaskForProjectPath(p.Path); ok {
		return m.renderAgentTaskDetailContent(task, width)
	}
	if row, project, ok := m.selectedProjectRow(); ok && row.Kind == projectListRowPendingWorktree {
		return m.renderTodoPendingLaunchDetailContent(project, width)
	}
	d := m.detail
	if d.Summary.Path != "" && d.Summary.Path != p.Path {
		d = model.ProjectDetail{}
	}
	return renderProjectDetailSurface(m.buildProjectDetailSurface(p, d), width)
}
