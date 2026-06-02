package tui

import (
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"lcroom/internal/browserctl"
	"lcroom/internal/codexapp"
	"lcroom/internal/model"
	"lcroom/internal/procinspect"
	"lcroom/internal/projectrun"
	"lcroom/internal/scanner"
	"lcroom/internal/service"
	"lcroom/internal/uistyle"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

type embeddedCodexPanelFocus string

const (
	embeddedCodexFocusMain    embeddedCodexPanelFocus = ""
	embeddedCodexFocusSidebar embeddedCodexPanelFocus = "sidebar"
)

type embeddedCodexSidebarSection int

const (
	embeddedCodexSidebarProcesses embeddedCodexSidebarSection = iota
	embeddedCodexSidebarDiff
)

const (
	embeddedCodexSidebarSectionCount = 2
	embeddedSidebarRecentLimit       = 3
	embeddedSidebarDiffAutoInterval  = 2 * time.Second
)

type embeddedSidebarDiffState struct {
	ProjectPath string
	Loading     bool
	Seq         int64
	Preview     *service.DiffPreview
	Clean       bool
	Branch      string
	ProjectName string
	Err         string
	UpdatedAt   time.Time
}

type embeddedSidebarDiffPreviewMsg struct {
	projectPath string
	seq         int64
	preview     service.DiffPreview
	clean       bool
	branch      string
	projectName string
	err         error
}

func codexSidebarTargetWidth(width int) int {
	if width < 104 {
		return 0
	}
	sidebarWidth := clampInt(width/3, 34, 46)
	if width-sidebarWidth-1 < 64 {
		return 0
	}
	return sidebarWidth
}

func (m Model) embeddedCodexSidebarAvailable() bool {
	width := m.width
	if width <= 0 {
		width = 120
	}
	return codexSidebarTargetWidth(width) > 0
}

func (m Model) embeddedCodexMainWidth() int {
	width := m.width
	if width <= 0 {
		width = 120
	}
	sidebarWidth := codexSidebarTargetWidth(width)
	if sidebarWidth == 0 {
		return width
	}
	return max(24, width-sidebarWidth-1)
}

func (m Model) codexMainFocused() bool {
	return m.codexPanelFocus != embeddedCodexFocusSidebar
}

func (m *Model) normalizeEmbeddedCodexFocus() {
	if !m.embeddedCodexSidebarAvailable() && m.codexPanelFocus != embeddedCodexFocusMain {
		m.codexPanelFocus = embeddedCodexFocusMain
	}
	if m.codexSidebarSelected < 0 || m.codexSidebarSelected >= embeddedCodexSidebarSectionCount {
		m.codexSidebarSelected = embeddedCodexSidebarProcesses
	}
}

func (m *Model) focusEmbeddedCodexMain(status string) tea.Cmd {
	m.codexPanelFocus = embeddedCodexFocusMain
	m.status = firstNonEmptyTrimmed(status, "Focus: engineer session")
	return m.codexInput.Focus()
}

func (m *Model) focusEmbeddedCodexSidebar() tea.Cmd {
	if !m.embeddedCodexSidebarAvailable() {
		m.status = "Embedded sidebar is hidden at this width"
		return nil
	}
	m.codexPanelFocus = embeddedCodexFocusSidebar
	m.codexInput.Blur()
	m.status = "Focus: embedded sidebar"
	return nil
}

func (m *Model) moveEmbeddedSidebarSelection(delta int) {
	next := int(m.codexSidebarSelected) + delta
	for next < 0 {
		next += embeddedCodexSidebarSectionCount
	}
	next %= embeddedCodexSidebarSectionCount
	m.codexSidebarSelected = embeddedCodexSidebarSection(next)
	switch m.codexSidebarSelected {
	case embeddedCodexSidebarProcesses:
		m.status = "Sidebar: active processes"
	case embeddedCodexSidebarDiff:
		m.status = "Sidebar: diff summary"
	}
}

func (m Model) updateCodexSidebarMode(snapshot codexapp.Snapshot, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	projectPath := m.embeddedSidebarProjectPath(snapshot)
	switch msg.String() {
	case "alt+s":
		cmd := m.focusEmbeddedCodexMain("")
		return m, cmd
	case "esc":
		cmd := m.focusEmbeddedCodexMain("Focus: engineer session")
		return m, cmd
	case "up", "k", "shift+tab":
		m.moveEmbeddedSidebarSelection(-1)
		return m, nil
	case "down", "j", "tab":
		m.moveEmbeddedSidebarSelection(1)
		return m, nil
	case "r":
		m.status = "Refreshing sidebar state..."
		cmd := m.refreshEmbeddedSidebarCmd(projectPath)
		return m, cmd
	case "enter":
		switch m.codexSidebarSelected {
		case embeddedCodexSidebarDiff:
			return m.openEmbeddedSidebarDiff(projectPath)
		default:
			m.status = "Refreshing active process state..."
			cmd := batchCmds(m.requestRuntimeSnapshotsRefreshCmd(), m.requestProcessScanCmd(projectPath))
			return m, cmd
		}
	}
	return m, nil
}

func (m Model) openEmbeddedSidebarDiff(projectPath string) (tea.Model, tea.Cmd) {
	projectPath = strings.TrimSpace(projectPath)
	if projectPath == "" {
		m.status = "Diff unavailable: no project"
		return m, nil
	}
	project, _ := m.projectSummaryByPathAllProjects(projectPath)
	projectName := strings.TrimSpace(project.Name)
	if projectName == "" {
		projectName = filepath.Base(projectPath)
	}
	m.persistVisibleCodexDraft()
	m.codexHiddenProject = projectPath
	m.codexVisibleProject = ""
	m.codexPanelFocus = embeddedCodexFocusMain
	m.codexInput.Blur()
	cmd := m.startDiffView(projectPath, projectName)
	if m.diffView != nil {
		m.diffView.returnToCodexProject = projectPath
	}
	return m, cmd
}

func (m Model) returnFromDiffToEmbeddedCodex(stageAskPrompt bool) (tea.Model, tea.Cmd) {
	if m.diffView == nil || strings.TrimSpace(m.diffView.returnToCodexProject) == "" {
		return m.closeDiffView("Diff view closed")
	}
	projectPath := strings.TrimSpace(m.diffView.returnToCodexProject)
	m.clearPendingGitSummary(m.diffView.ProjectPath)
	m.diffView = nil
	updated, cmd := m.showCodexProject(projectPath, "Back to engineer session")
	m = normalizeUpdateModel(updated)
	if stageAskPrompt && strings.TrimSpace(m.codexInput.Value()) == "" {
		prompt := "Please review the current diff and call out bugs, regressions, and missing tests."
		m.setCodexComposerValue(prompt, len([]rune(prompt)))
		m.persistVisibleCodexDraft()
	}
	return m, cmd
}

func (m *Model) refreshEmbeddedSidebarCmd(projectPath string) tea.Cmd {
	projectPath = normalizeProjectPath(projectPath)
	if projectPath == "" {
		return nil
	}
	return batchCmds(
		m.requestEmbeddedSidebarDiffRefreshCmd(projectPath),
		m.requestRuntimeSnapshotsRefreshCmd(),
		m.requestProcessScanCmd(projectPath),
	)
}

func (m *Model) requestVisibleBusyEmbeddedSidebarDiffRefreshCmd() tea.Cmd {
	projectPath := normalizeProjectPath(m.codexVisibleProject)
	if projectPath == "" {
		return nil
	}
	snapshot, ok := m.codexCachedSnapshot(projectPath)
	if !ok || snapshot.Closed || !snapshot.Busy {
		return nil
	}
	return m.requestVisibleEmbeddedSidebarDiffRefreshCmd(projectPath, false)
}

func (m *Model) requestVisibleEmbeddedSidebarDiffRefreshCmd(projectPath string, force bool) tea.Cmd {
	projectPath = normalizeProjectPath(projectPath)
	if projectPath == "" || projectPath != normalizeProjectPath(m.codexVisibleProject) {
		return nil
	}
	if !m.embeddedCodexSidebarAvailable() {
		return nil
	}
	if state, ok := m.embeddedSidebarDiffState(projectPath); ok && state.Loading {
		return nil
	}
	now := m.currentTime()
	if !force {
		if m.embeddedSidebarDiffAutoAt == nil {
			m.embeddedSidebarDiffAutoAt = make(map[string]time.Time)
		}
		if last := m.embeddedSidebarDiffAutoAt[projectPath]; !last.IsZero() && now.Before(last.Add(embeddedSidebarDiffAutoInterval)) {
			return nil
		}
	}
	cmd := m.requestEmbeddedSidebarDiffRefreshCmd(projectPath)
	if cmd == nil {
		return nil
	}
	if m.embeddedSidebarDiffAutoAt == nil {
		m.embeddedSidebarDiffAutoAt = make(map[string]time.Time)
	}
	m.embeddedSidebarDiffAutoAt[projectPath] = now
	return cmd
}

func (m *Model) requestEmbeddedSidebarDiffRefreshCmd(projectPath string) tea.Cmd {
	projectPath = normalizeProjectPath(projectPath)
	if projectPath == "" || m.svc == nil {
		return nil
	}
	if m.embeddedSidebarDiffs == nil {
		m.embeddedSidebarDiffs = make(map[string]embeddedSidebarDiffState)
	}
	current := m.embeddedSidebarDiffs[projectPath]
	if current.Loading {
		return nil
	}
	m.embeddedSidebarDiffSeq++
	seq := m.embeddedSidebarDiffSeq
	current.ProjectPath = projectPath
	current.Loading = true
	current.Seq = seq
	current.Err = ""
	m.embeddedSidebarDiffs[projectPath] = current
	return func() tea.Msg {
		ctx, cancel := m.actionContext(tuiGitActionTimeout)
		defer cancel()
		preview, err := m.svc.PrepareDiff(ctx, projectPath)
		err = timeoutActionError(err, tuiGitActionTimeout, "preparing the sidebar diff summary")
		if err != nil {
			var noDiffErr service.NoDiffChangesError
			if errors.As(err, &noDiffErr) {
				return embeddedSidebarDiffPreviewMsg{
					projectPath: projectPath,
					seq:         seq,
					clean:       true,
					branch:      noDiffErr.Branch,
					projectName: noDiffErr.ProjectName,
				}
			}
			return embeddedSidebarDiffPreviewMsg{projectPath: projectPath, seq: seq, err: err}
		}
		return embeddedSidebarDiffPreviewMsg{projectPath: projectPath, seq: seq, preview: preview}
	}
}

func (m Model) applyEmbeddedSidebarDiffPreviewMsg(msg embeddedSidebarDiffPreviewMsg) (tea.Model, tea.Cmd) {
	projectPath := normalizeProjectPath(msg.projectPath)
	if projectPath == "" {
		return m, nil
	}
	if m.embeddedSidebarDiffs == nil {
		m.embeddedSidebarDiffs = make(map[string]embeddedSidebarDiffState)
	}
	current := m.embeddedSidebarDiffs[projectPath]
	if current.Seq != 0 && msg.seq != 0 && current.Seq != msg.seq {
		return m, nil
	}
	current.ProjectPath = projectPath
	current.Loading = false
	current.Seq = msg.seq
	current.UpdatedAt = m.currentTime()
	current.Preview = nil
	current.Clean = msg.clean
	current.Branch = strings.TrimSpace(msg.branch)
	current.ProjectName = strings.TrimSpace(msg.projectName)
	current.Err = ""
	if msg.err != nil {
		current.Err = strings.TrimSpace(msg.err.Error())
	} else if !msg.clean {
		preview := msg.preview
		current.Preview = &preview
		current.Branch = strings.TrimSpace(preview.Branch)
		current.ProjectName = strings.TrimSpace(preview.ProjectName)
	}
	m.embeddedSidebarDiffs[projectPath] = current
	return m, nil
}

func (m *Model) rememberEmbeddedSidebarDiffPreview(preview service.DiffPreview) {
	projectPath := normalizeProjectPath(preview.ProjectPath)
	if projectPath == "" {
		return
	}
	if m.embeddedSidebarDiffs == nil {
		m.embeddedSidebarDiffs = make(map[string]embeddedSidebarDiffState)
	}
	current := m.embeddedSidebarDiffs[projectPath]
	preview.ProjectPath = projectPath
	current.ProjectPath = projectPath
	current.Loading = false
	current.Preview = &preview
	current.Clean = false
	current.Branch = strings.TrimSpace(preview.Branch)
	current.ProjectName = strings.TrimSpace(preview.ProjectName)
	current.Err = ""
	current.UpdatedAt = m.currentTime()
	m.embeddedSidebarDiffs[projectPath] = current
}

func (m *Model) rememberEmbeddedSidebarCleanDiff(projectPath, projectName, branch string) {
	projectPath = normalizeProjectPath(projectPath)
	if projectPath == "" {
		return
	}
	if m.embeddedSidebarDiffs == nil {
		m.embeddedSidebarDiffs = make(map[string]embeddedSidebarDiffState)
	}
	current := m.embeddedSidebarDiffs[projectPath]
	current.ProjectPath = projectPath
	current.Loading = false
	current.Preview = nil
	current.Clean = true
	current.Branch = strings.TrimSpace(branch)
	current.ProjectName = strings.TrimSpace(projectName)
	current.Err = ""
	current.UpdatedAt = m.currentTime()
	m.embeddedSidebarDiffs[projectPath] = current
}

func (m Model) renderCodexSplitView(snapshot codexapp.Snapshot, width, height int) string {
	sidebarWidth := codexSidebarTargetWidth(width)
	if sidebarWidth == 0 {
		return m.renderCodexMainView(snapshot, width, height)
	}
	mainWidth := max(24, width-sidebarWidth-1)
	main := fitPaneContent(m.renderCodexMainView(snapshot, mainWidth, height), mainWidth, height)
	sidebar := fitPaneContent(m.renderEmbeddedCodexSidebar(snapshot, sidebarWidth, height), sidebarWidth, height)
	divider := embeddedCodexSidebarDivider(height)
	return lipgloss.JoinHorizontal(lipgloss.Top, main, divider, sidebar)
}

func embeddedCodexSidebarDivider(height int) string {
	if height <= 0 {
		return ""
	}
	style := lipgloss.NewStyle().Foreground(lipgloss.Color("238"))
	lines := make([]string, 0, height)
	for i := 0; i < height; i++ {
		lines = append(lines, style.Render("│"))
	}
	return strings.Join(lines, "\n")
}

func (m Model) renderEmbeddedCodexSidebar(snapshot codexapp.Snapshot, width, height int) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	projectPath := m.embeddedSidebarProjectPath(snapshot)
	contentWidth := max(12, width-2)
	lines := []string{}
	lines = appendEmbeddedSidebarSection(lines, m.renderEmbeddedSidebarSessionSection(snapshot, contentWidth))
	lines = appendEmbeddedSidebarSection(lines, m.renderEmbeddedSidebarBrowserSection(snapshot, contentWidth))
	lines = appendEmbeddedSidebarSection(lines, m.renderEmbeddedSidebarDiffSection(projectPath, contentWidth))
	lines = appendEmbeddedSidebarSection(lines, m.renderEmbeddedSidebarProcessSection(projectPath, contentWidth))
	lines = appendEmbeddedSidebarSection(lines, m.renderEmbeddedSidebarRecentActivitySection(snapshot, contentWidth))
	return lipgloss.NewStyle().PaddingLeft(1).Render(fitPaneContent(strings.Join(lines, "\n"), contentWidth, height))
}

func (m Model) embeddedSidebarProjectPath(snapshot codexapp.Snapshot) string {
	return normalizeProjectPath(firstNonEmptyString(m.codexVisibleProject, snapshot.ProjectPath, m.codexHiddenProject))
}

func appendEmbeddedSidebarSection(lines, section []string) []string {
	if len(section) == 0 {
		return lines
	}
	if len(lines) > 0 {
		lines = append(lines, "")
	}
	return append(lines, section...)
}

func (m Model) renderEmbeddedSidebarSectionHeader(section embeddedCodexSidebarSection, title string, width int) string {
	selected := m.codexSidebarSelected == section && m.codexPanelFocus == embeddedCodexFocusSidebar
	marker := " "
	if selected {
		marker = ">"
	}
	text := marker + " " + title
	if selected {
		return uistyle.SidebarSectionHeaderStyle.Width(width).Render(truncateText(text, max(1, width)))
	}
	return uistyle.SidebarSectionHeaderStyle.Render(fitLine(text, width))
}

func renderEmbeddedSidebarStaticHeader(title string, width int) string {
	return uistyle.SidebarSectionHeaderStyle.Render(fitLine("  "+title, width))
}

func (m Model) renderEmbeddedSidebarSessionSection(snapshot codexapp.Snapshot, width int) []string {
	rows := m.embeddedSidebarSessionRows(snapshot, width)
	if len(rows) == 0 {
		return nil
	}
	return append([]string{renderEmbeddedSidebarStaticHeader("Session", width)}, rows...)
}

func (m Model) embeddedSidebarSessionRows(snapshot codexapp.Snapshot, width int) []string {
	rows := []string{}
	if row := embeddedSidebarContextRow(snapshot, width); row != "" {
		rows = append(rows, row)
	}
	if tokens := codexSnapshotTokenUsageLabel(snapshot); tokens != "" {
		rows = append(rows, embeddedSidebarFieldRow("Tokens", tokens, detailValueStyle, width))
	}
	if goal := snapshot.Goal; goal != nil {
		label := codexSnapshotGoalLabel(snapshot)
		if label == "" {
			label = "active"
		}
		rows = append(rows, embeddedSidebarFieldRow("Goal", label, embeddedSidebarGoalStyle(goal.Status), width))
		if objective := strings.TrimSpace(goal.Objective); objective != "" {
			rows = append(rows, detailMutedStyle.Render(fitLine(objective, width)))
		}
	}
	return rows
}

func embeddedSidebarContextRow(snapshot codexapp.Snapshot, width int) string {
	if snapshot.TokenUsage == nil || snapshot.TokenUsage.ModelContextWindow <= 0 {
		return ""
	}
	used := snapshot.TokenUsage.EstimatedContextTokens()
	if used < 0 {
		used = 0
	}
	if used == 0 {
		return ""
	}
	usedPercent := int(float64(used)*100/float64(snapshot.TokenUsage.ModelContextWindow) + 0.5)
	if usedPercent < 0 {
		usedPercent = 0
	}
	if usedPercent > 100 {
		usedPercent = 100
	}
	style := detailValueStyle
	if usedPercent >= 90 {
		style = detailDangerStyle
	} else if usedPercent >= 70 {
		style = detailWarningStyle
	}
	value := fmt.Sprintf("%d%% of %s", usedPercent, uistyle.FormatTokenCount(snapshot.TokenUsage.ModelContextWindow))
	return embeddedSidebarFieldRow("Context", value, style, width)
}

func embeddedSidebarGoalStyle(status codexapp.ThreadGoalStatus) lipgloss.Style {
	switch status {
	case codexapp.ThreadGoalStatusBlocked, codexapp.ThreadGoalStatusBudgetLimited:
		return detailDangerStyle
	case codexapp.ThreadGoalStatusPaused:
		return detailWarningStyle
	case codexapp.ThreadGoalStatusComplete:
		return detailMutedStyle
	default:
		return detailValueStyle
	}
}

func (m Model) renderEmbeddedSidebarBrowserSection(snapshot codexapp.Snapshot, width int) []string {
	rows := m.embeddedSidebarBrowserRows(snapshot, width)
	if len(rows) == 0 {
		return nil
	}
	return append([]string{renderEmbeddedSidebarStaticHeader("Browser", width)}, rows...)
}

func (m Model) embeddedSidebarBrowserRows(snapshot codexapp.Snapshot, width int) []string {
	if snapshot.Closed {
		return nil
	}
	if !embeddedSidebarBrowserRelevant(snapshot) {
		return nil
	}
	rows := []string{}
	if m.codexBrowserPolicyMismatch(snapshot) {
		rows = append(rows, detailWarningStyle.Render(fitLine("Browser settings changed", width)))
		rows = append(rows, detailMutedStyle.Render(fitLine("Use /reconnect", width)))
	}
	if request := snapshot.PendingElicitation; request != nil && request.Mode == codexapp.ElicitationModeURL {
		rows = append(rows, detailWarningStyle.Render(fitLine("Input requested", width)))
		if requestURL := strings.TrimSpace(request.URL); requestURL != "" {
			rows = append(rows, embeddedSidebarURLRow("URL", requestURL, width))
		}
	}
	activity := snapshot.BrowserActivity.Normalize()
	source := firstNonEmptyString(activity.SourceLabel(), "Playwright")
	switch activity.State {
	case browserctl.SessionActivityStateWaitingForUser:
		rows = append(rows, embeddedSidebarFieldRow("State", "waiting", detailWarningStyle, width))
		rows = append(rows, embeddedSidebarFieldRow("Source", source, detailMutedStyle, width))
	case browserctl.SessionActivityStateActive:
		rows = append(rows, embeddedSidebarFieldRow("State", "active", detailValueStyle, width))
		rows = append(rows, embeddedSidebarFieldRow("Source", source, detailMutedStyle, width))
	}
	if pageURL := managedBrowserCurrentPageURL(snapshot); pageURL != "" &&
		strings.TrimSpace(snapshot.ManagedBrowserSessionKey) != "" &&
		!snapshot.CurrentBrowserPageStale {
		rows = append(rows, embeddedSidebarURLRow("Page", pageURL, width))
		if hint := m.managedBrowserCurrentPageHint(snapshot); hint != "" {
			rows = append(rows, detailMutedStyle.Render(fitLine("ctrl+o reveals browser", width)))
		}
	}
	return dedupeSidebarRows(rows)
}

func embeddedSidebarBrowserRelevant(snapshot codexapp.Snapshot) bool {
	if request := snapshot.PendingElicitation; request != nil && request.Mode == codexapp.ElicitationModeURL {
		return true
	}
	activity := snapshot.BrowserActivity
	if activity.State != "" ||
		strings.TrimSpace(activity.ServerName) != "" ||
		strings.TrimSpace(activity.ToolName) != "" {
		return true
	}
	return strings.TrimSpace(snapshot.ManagedBrowserSessionKey) != "" ||
		strings.TrimSpace(snapshot.CurrentBrowserPageURL) != ""
}

func embeddedSidebarRecentActivitySection(snapshot codexapp.Snapshot, width int) []string {
	rows := embeddedSidebarRecentActivityRows(snapshot, width, embeddedSidebarRecentLimit)
	if len(rows) == 0 {
		return nil
	}
	return append([]string{renderEmbeddedSidebarStaticHeader("Recent Activity", width)}, rows...)
}

func (m Model) renderEmbeddedSidebarRecentActivitySection(snapshot codexapp.Snapshot, width int) []string {
	return embeddedSidebarRecentActivitySection(snapshot, width)
}

func (m Model) renderEmbeddedSidebarProcessSection(projectPath string, width int) []string {
	lines := []string{m.renderEmbeddedSidebarSectionHeader(embeddedCodexSidebarProcesses, "Active Processes", width)}
	rows := m.embeddedSidebarProcessRows(projectPath, width, 5)
	if len(rows) == 0 {
		lines = append(lines, detailMutedStyle.Render(fitLine("No active project processes", width)))
		return lines
	}
	return append(lines, rows...)
}

func (m Model) embeddedSidebarProcessRows(projectPath string, width, limit int) []string {
	projectPath = normalizeProjectPath(projectPath)
	if projectPath == "" || limit <= 0 {
		return nil
	}
	rows := make([]string, 0, limit)
	for _, snapshot := range m.projectRuntimeSnapshots(projectPath) {
		if len(rows) >= limit {
			break
		}
		if !runtimeDetailAvailable("", snapshot) {
			continue
		}
		rows = append(rows, m.embeddedSidebarRuntimeRow(snapshot, width))
	}
	for _, snapshot := range m.projectVisibleLocalInstanceSnapshots(projectPath) {
		if len(rows) >= limit {
			break
		}
		rows = append(rows, embeddedSidebarLocalInstanceRow(snapshot, width))
	}
	if report, ok := m.projectProcessReport(projectPath); ok {
		for _, finding := range report.Findings {
			if len(rows) >= limit {
				break
			}
			rows = append(rows, embeddedSidebarFindingRow(finding, width))
		}
	}
	return rows
}

func (m Model) embeddedSidebarRuntimeRow(snapshot projectrun.Snapshot, width int) string {
	status := "idle"
	style := detailMutedStyle
	if snapshot.Running {
		status = "run"
		style = detailValueStyle
	} else if snapshot.ExitCodeKnown && snapshot.ExitCode != 0 {
		status = fmt.Sprintf("x%d", snapshot.ExitCode)
		style = detailDangerStyle
	}
	label := strings.TrimSpace(snapshot.Command)
	if label == "" {
		label = runtimeProcessLabel(snapshot)
	} else if snapshot.PID > 0 {
		label += fmt.Sprintf(" pid %d", snapshot.PID)
	}
	if url := runtimeURLSummary(snapshot); url != "" {
		label += " " + url
	} else if len(snapshot.Ports) > 0 {
		label += " :" + joinPorts(snapshot.Ports)
	}
	return fitStyledWidth(style.Render(status)+" "+detailMutedStyle.Render(truncateText(label, max(1, width-5))), width)
}

func embeddedSidebarLocalInstanceRow(snapshot projectrun.Snapshot, width int) string {
	label := localInstanceDisplayLabel(snapshot)
	if len(snapshot.AnnouncedURLs) > 0 {
		url := runtimeURLSummary(snapshot)
		label += " " + url
	} else if len(snapshot.Ports) > 0 {
		label += " :" + joinPorts(snapshot.Ports)
	}
	return fitStyledWidth(detailValueStyle.Render("live")+" "+detailMutedStyle.Render(truncateText(label, max(1, width-6))), width)
}

func localInstanceDisplayLabel(snapshot projectrun.Snapshot) string {
	label := projectRunCommandLabel(snapshot.Command)
	if label == "" {
		label = "pid"
	}
	if snapshot.PID > 0 {
		label += fmt.Sprintf(" pid %d", snapshot.PID)
	}
	return strings.TrimSpace(label)
}

func embeddedSidebarFindingRow(finding procinspect.Finding, width int) string {
	label := fmt.Sprintf("pid %d %s", finding.PID, cpuProcessName(finding.Process))
	if finding.CPU > 0 {
		label += " " + formatCPUPercent(finding.CPU)
	}
	if len(finding.Ports) > 0 {
		label += " :" + joinPorts(finding.Ports)
	}
	style := detailWarningStyle
	if finding.PortConflict {
		style = detailDangerStyle
	}
	return fitStyledWidth(style.Render("proc")+" "+detailMutedStyle.Render(truncateText(label, max(1, width-6))), width)
}

func (m Model) renderEmbeddedSidebarDiffSection(projectPath string, width int) []string {
	lines := []string{m.renderEmbeddedSidebarSectionHeader(embeddedCodexSidebarDiff, "Diff Summary", width)}
	state, ok := m.embeddedSidebarDiffState(projectPath)
	project, _ := m.projectSummaryByPathAllProjects(projectPath)
	switch {
	case ok && state.Loading && state.Preview == nil && !state.Clean:
		lines = append(lines, detailMutedStyle.Render(fitLine("Preparing diff summary...", width)))
	case ok && strings.TrimSpace(state.Err) != "":
		lines = append(lines, detailDangerStyle.Render(fitLine("Diff unavailable", width)))
		lines = append(lines, detailMutedStyle.Render(fitLine(state.Err, width)))
	case ok && state.Preview != nil && len(state.Preview.Files) > 0:
		lines = append(lines, embeddedSidebarDiffSummaryRow(*state.Preview, width))
		if branch := strings.TrimSpace(state.Preview.Branch); branch != "" {
			lines = append(lines, detailMutedStyle.Render(fitLine(branch, width)))
		}
		for i, file := range state.Preview.Files {
			if i >= 3 {
				lines = append(lines, detailMutedStyle.Render(fitLine(fmt.Sprintf("+%d more", len(state.Preview.Files)-i), width)))
				break
			}
			lines = append(lines, embeddedSidebarDiffFileRow(file, width))
		}
		lines = append(lines, detailMutedStyle.Render(fitLine("Enter opens full diff", width)))
	case ok && state.Clean:
		label := "Clean worktree"
		if branch := strings.TrimSpace(state.Branch); branch != "" {
			label += " (" + branch + ")"
		}
		lines = append(lines, detailMutedStyle.Render(fitLine(label, width)))
	case project.RepoDirty || project.RepoConflict:
		lines = append(lines, detailWarningStyle.Render(fitLine(repoDirtyPlainLabel(project), width)))
		lines = append(lines, detailMutedStyle.Render(fitLine("Enter opens full diff", width)))
	default:
		lines = append(lines, detailMutedStyle.Render(fitLine("No diff cached yet", width)))
		lines = append(lines, detailMutedStyle.Render(fitLine("r refreshes", width)))
	}
	return lines
}

func embeddedSidebarDiffSummaryRow(preview service.DiffPreview, width int) string {
	summary := strings.TrimSpace(preview.Summary)
	if summary == "" {
		summary = fmt.Sprintf("%d files changed", len(preview.Files))
	}
	counts := embeddedSidebarDiffKindCounts(preview.Files)
	if len(counts) == 0 {
		return detailValueStyle.Render(fitLine(summary, width))
	}
	countText := embeddedSidebarDiffKindCountText(counts)
	summaryWidth := max(1, width-ansi.StringWidth(ansi.Strip(countText))-1)
	return fitStyledWidth(detailValueStyle.Render(truncateText(summary, summaryWidth))+" "+countText, width)
}

func embeddedSidebarDiffKindCounts(files []service.DiffFilePreview) []service.DiffFilePreview {
	order := []scanner.GitChangeKind{
		scanner.GitChangeModified,
		scanner.GitChangeAdded,
		scanner.GitChangeDeleted,
		scanner.GitChangeRenamed,
		scanner.GitChangeCopied,
		scanner.GitChangeType,
		scanner.GitChangeUnmerged,
		scanner.GitChangeUntracked,
		scanner.GitChangeUnknown,
	}
	countByKind := make(map[scanner.GitChangeKind]int)
	for _, file := range files {
		countByKind[file.Kind]++
	}
	out := make([]service.DiffFilePreview, 0, len(countByKind))
	for _, kind := range order {
		count := countByKind[kind]
		if count == 0 {
			continue
		}
		out = append(out, service.DiffFilePreview{Kind: kind, Summary: fmt.Sprintf("%d", count)})
	}
	return out
}

func embeddedSidebarDiffKindCountText(counts []service.DiffFilePreview) string {
	parts := make([]string, 0, len(counts))
	for _, count := range counts {
		code := diffFileKindCode(count)
		parts = append(parts, embeddedSidebarDiffCodeStyle(count).Render(code+count.Summary))
	}
	return strings.Join(parts, " ")
}

func embeddedSidebarDiffFileRow(file service.DiffFilePreview, width int) string {
	code := embeddedSidebarDiffCodeStyle(file).Render(diffFileKindCode(file))
	summary := strings.TrimSpace(file.Summary)
	if summary == "" {
		summary = strings.TrimSpace(file.Path)
	}
	if summary == "" {
		summary = "changed file"
	}
	return fitStyledWidth(code+" "+detailMutedStyle.Render(truncateText(summary, max(1, width-2))), width)
}

func embeddedSidebarDiffCodeStyle(file service.DiffFilePreview) lipgloss.Style {
	switch file.Kind {
	case scanner.GitChangeAdded:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true)
	case scanner.GitChangeModified:
		return detailWarningStyle
	case scanner.GitChangeDeleted, scanner.GitChangeUnmerged:
		return detailDangerStyle
	case scanner.GitChangeRenamed, scanner.GitChangeCopied, scanner.GitChangeType:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("81")).Bold(true)
	case scanner.GitChangeUntracked:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true)
	default:
		return detailMutedStyle
	}
}

func (m Model) embeddedSidebarDiffState(projectPath string) (embeddedSidebarDiffState, bool) {
	projectPath = normalizeProjectPath(projectPath)
	if projectPath == "" || len(m.embeddedSidebarDiffs) == 0 {
		return embeddedSidebarDiffState{}, false
	}
	state, ok := m.embeddedSidebarDiffs[projectPath]
	return state, ok
}

func repoDirtyPlainLabel(project model.ProjectSummary) string {
	if project.RepoConflict {
		return "Unmerged files"
	}
	if project.RepoDirty {
		return "Dirty worktree"
	}
	return "Clean worktree"
}

func embeddedSidebarRecentActivityRows(snapshot codexapp.Snapshot, width, limit int) []string {
	if limit <= 0 {
		return nil
	}
	rows := make([]string, 0, limit)
	seen := map[string]struct{}{}
	for i := len(snapshot.Entries) - 1; i >= 0 && len(rows) < limit; i-- {
		row := embeddedSidebarRecentActivityRow(snapshot.Entries[i], width)
		key := ansi.Strip(row)
		if strings.TrimSpace(key) == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		rows = append(rows, row)
	}
	return rows
}

func embeddedSidebarRecentActivityRow(entry codexapp.TranscriptEntry, width int) string {
	text := embeddedSidebarTranscriptSummaryText(entry)
	if text == "" {
		return ""
	}
	label, style, ok := embeddedSidebarActivityKind(entry.Kind)
	if !ok {
		return ""
	}
	prefix := style.Render(label)
	return fitStyledWidth(prefix+" "+detailMutedStyle.Render(truncateText(text, max(1, width-ansi.StringWidth(label)-1))), width)
}

func embeddedSidebarActivityKind(kind codexapp.TranscriptKind) (string, lipgloss.Style, bool) {
	switch kind {
	case codexapp.TranscriptCommand:
		return "cmd", detailValueStyle, true
	case codexapp.TranscriptFileChange:
		return "file", detailValueStyle, true
	case codexapp.TranscriptTool:
		return "tool", detailValueStyle, true
	case codexapp.TranscriptPlan:
		return "plan", detailWarningStyle, true
	case codexapp.TranscriptStatus:
		return "note", detailMutedStyle, true
	case codexapp.TranscriptError:
		return "err", detailDangerStyle, true
	default:
		return "", lipgloss.Style{}, false
	}
}

func embeddedSidebarTranscriptSummaryText(entry codexapp.TranscriptEntry) string {
	text := strings.TrimSpace(firstNonEmptyString(entry.DisplayText, entry.Text))
	if text == "" {
		return ""
	}
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
}

func embeddedSidebarURLRow(label, rawURL string, width int) string {
	return embeddedSidebarFieldRow(label, embeddedSidebarCompactURL(rawURL), detailMutedStyle, width)
}

func embeddedSidebarCompactURL(rawURL string) string {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return ""
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Host == "" {
		return rawURL
	}
	path := strings.TrimSpace(parsed.EscapedPath())
	if path == "" || path == "/" {
		return parsed.Host
	}
	return parsed.Host + path
}

func embeddedSidebarFieldRow(label, value string, style lipgloss.Style, width int) string {
	label = strings.TrimSpace(label)
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	prefix := label
	if prefix != "" {
		prefix += " "
	}
	valueWidth := max(1, width-ansi.StringWidth(prefix))
	return fitStyledWidth(detailMutedStyle.Render(prefix)+style.Render(truncateText(value, valueWidth)), width)
}

func dedupeSidebarRows(rows []string) []string {
	if len(rows) == 0 {
		return nil
	}
	out := make([]string, 0, len(rows))
	seen := map[string]struct{}{}
	for _, row := range rows {
		key := strings.TrimSpace(ansi.Strip(row))
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, row)
	}
	return out
}

func fitLine(text string, width int) string {
	if width <= 0 {
		return ""
	}
	return ansi.Truncate(strings.TrimRight(text, "\r\n"), width, "")
}
