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
	embeddedCodexSidebarSession embeddedCodexSidebarSection = iota
	embeddedCodexSidebarQuality
	embeddedCodexSidebarCritic
	embeddedCodexSidebarVision
	embeddedCodexSidebarBrowser
	embeddedCodexSidebarDiff
	embeddedCodexSidebarProcesses
	embeddedCodexSidebarSummary
	embeddedCodexSidebarSectionCount
)

const (
	embeddedSidebarDiffAutoInterval = 2 * time.Second
)

type embeddedSidebarDiffState struct {
	ProjectPath string
	Loading     bool
	Seq         int64
	Preview     *service.DiffPreview
	Clean       bool
	NoGit       bool
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
	noGit       bool
	branch      string
	projectName string
	err         error
}

type embeddedSidebarDetailState struct {
	Section     embeddedCodexSidebarSection
	ProjectPath string
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

func (m *Model) normalizeEmbeddedSidebarSelection(snapshot codexapp.Snapshot) {
	if m.codexSidebarSelected < 0 || m.codexSidebarSelected >= embeddedCodexSidebarSectionCount {
		m.codexSidebarSelected = embeddedCodexSidebarProcesses
	}
	sections := m.embeddedSidebarVisibleSections(snapshot)
	if len(sections) == 0 {
		return
	}
	for _, section := range sections {
		if section == m.codexSidebarSelected {
			return
		}
	}
	for _, section := range sections {
		if section == embeddedCodexSidebarProcesses {
			m.codexSidebarSelected = section
			return
		}
	}
	m.codexSidebarSelected = sections[0]
}

func (m Model) embeddedSidebarVisibleSections(snapshot codexapp.Snapshot) []embeddedCodexSidebarSection {
	projectPath := m.embeddedSidebarProjectPath(snapshot)
	candidates := []embeddedCodexSidebarSection{
		embeddedCodexSidebarSession,
		embeddedCodexSidebarQuality,
		embeddedCodexSidebarCritic,
		embeddedCodexSidebarVision,
		embeddedCodexSidebarBrowser,
		embeddedCodexSidebarDiff,
		embeddedCodexSidebarProcesses,
		embeddedCodexSidebarSummary,
	}
	sections := make([]embeddedCodexSidebarSection, 0, len(candidates))
	for _, section := range candidates {
		if m.embeddedSidebarSectionAvailable(snapshot, projectPath, section) {
			sections = append(sections, section)
		}
	}
	return sections
}

func (m Model) embeddedSidebarSectionAvailable(snapshot codexapp.Snapshot, projectPath string, section embeddedCodexSidebarSection) bool {
	switch section {
	case embeddedCodexSidebarSession:
		return len(m.embeddedSidebarSessionRows(snapshot, 1)) > 0
	case embeddedCodexSidebarQuality:
		return embeddedSidebarQualityRelevant(snapshot)
	case embeddedCodexSidebarCritic:
		return embeddedSidebarCriticRelevant(snapshot)
	case embeddedCodexSidebarVision:
		return embeddedSidebarVisionRelevant(snapshot)
	case embeddedCodexSidebarBrowser:
		return !snapshot.Closed && embeddedSidebarBrowserRelevant(snapshot)
	case embeddedCodexSidebarDiff, embeddedCodexSidebarProcesses:
		return normalizeProjectPath(projectPath) != ""
	case embeddedCodexSidebarSummary:
		_, _, ok := m.embeddedSidebarSummary(snapshot)
		return ok
	default:
		return false
	}
}

func (m *Model) moveEmbeddedSidebarSelection(snapshot codexapp.Snapshot, delta int) {
	sections := m.embeddedSidebarVisibleSections(snapshot)
	if len(sections) == 0 {
		return
	}
	idx := -1
	for i, section := range sections {
		if section == m.codexSidebarSelected {
			idx = i
			break
		}
	}
	if idx < 0 {
		idx = 0
	} else {
		idx += delta
	}
	for idx < 0 {
		idx += len(sections)
	}
	idx %= len(sections)
	m.codexSidebarSelected = sections[idx]
	m.status = "Sidebar: " + strings.ToLower(embeddedSidebarSectionTitle(m.codexSidebarSelected))
	if m.codexSidebarSelected != embeddedCodexSidebarDiff {
		m.status += " (Enter details)"
	}
}

func (m Model) updateCodexSidebarMode(snapshot codexapp.Snapshot, msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	projectPath := m.embeddedSidebarProjectPath(snapshot)
	m.normalizeEmbeddedSidebarSelection(snapshot)
	switch msg.String() {
	case "alt+s":
		cmd := m.focusEmbeddedCodexMain("")
		return m, cmd
	case "esc":
		cmd := m.focusEmbeddedCodexMain("Focus: engineer session")
		return m, cmd
	case "up", "k", "shift+tab":
		m.moveEmbeddedSidebarSelection(snapshot, -1)
		return m, nil
	case "down", "j", "tab":
		m.moveEmbeddedSidebarSelection(snapshot, 1)
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
			return m.openEmbeddedSidebarDetail(snapshot, projectPath, m.codexSidebarSelected)
		}
	}
	return m, nil
}

func embeddedSidebarSectionTitle(section embeddedCodexSidebarSection) string {
	switch section {
	case embeddedCodexSidebarSession:
		return "Session"
	case embeddedCodexSidebarQuality:
		return "Quality"
	case embeddedCodexSidebarCritic:
		return "Critic"
	case embeddedCodexSidebarVision:
		return "Vision"
	case embeddedCodexSidebarBrowser:
		return "Browser"
	case embeddedCodexSidebarDiff:
		return "Diff Summary"
	case embeddedCodexSidebarProcesses:
		return "Active Processes"
	case embeddedCodexSidebarSummary:
		return "Summary"
	default:
		return "Sidebar"
	}
}

func (m Model) openEmbeddedSidebarDetail(snapshot codexapp.Snapshot, projectPath string, section embeddedCodexSidebarSection) (tea.Model, tea.Cmd) {
	projectPath = normalizeProjectPath(projectPath)
	if !m.embeddedSidebarSectionAvailable(snapshot, projectPath, section) {
		m.status = embeddedSidebarSectionTitle(section) + " details unavailable"
		return m, nil
	}
	m.embeddedSidebarDetail = &embeddedSidebarDetailState{
		Section:     section,
		ProjectPath: projectPath,
	}
	m.status = embeddedSidebarSectionTitle(section) + " details open"
	return m, nil
}

func (m Model) updateEmbeddedSidebarDetailMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "enter", "q":
		title := "Sidebar"
		if m.embeddedSidebarDetail != nil {
			title = embeddedSidebarSectionTitle(m.embeddedSidebarDetail.Section)
		}
		m.embeddedSidebarDetail = nil
		m.status = title + " details closed"
		return m, nil
	}
	return m, nil
}

func (m Model) renderEmbeddedSidebarDetailOverlay(body string, bodyW, bodyH int) string {
	panel := m.renderEmbeddedSidebarDetail(bodyW, bodyH)
	if strings.TrimSpace(panel) == "" {
		return body
	}
	panelWidth := lipgloss.Width(panel)
	panelHeight := lipgloss.Height(panel)
	left := max(0, (bodyW-panelWidth)/2)
	top := max(0, (bodyH-panelHeight)/4)
	return overlayBlock(body, panel, bodyW, bodyH, left, top)
}

func (m Model) renderEmbeddedSidebarDetail(bodyW, bodyH int) string {
	if m.embeddedSidebarDetail == nil {
		return ""
	}
	panelWidth := min(bodyW, min(max(58, bodyW-16), 94))
	panelInnerWidth := max(32, panelWidth-4)
	maxContentHeight := max(8, bodyH-4)
	return renderDialogPanel(panelWidth, panelInnerWidth, m.renderEmbeddedSidebarDetailContent(panelInnerWidth, maxContentHeight))
}

func (m Model) renderEmbeddedSidebarDetailContent(width, maxHeight int) string {
	detail := m.embeddedSidebarDetail
	if detail == nil {
		return ""
	}
	snapshot, ok := m.currentCodexSnapshot()
	if !ok {
		return strings.Join([]string{
			renderDialogHeader("Sidebar", "", "", width),
			detailMutedStyle.Render(fitLine("Embedded session is no longer available", width)),
			"",
			renderHelpPanelActionRow(renderDialogAction("Esc", "close", cancelActionKeyStyle, cancelActionTextStyle)),
		}, "\n")
	}
	projectPath := normalizeProjectPath(firstNonEmptyString(detail.ProjectPath, m.embeddedSidebarProjectPath(snapshot)))
	rows := m.embeddedSidebarDetailRows(detail.Section, snapshot, projectPath, width)
	if len(rows) == 0 {
		rows = []string{detailMutedStyle.Render(fitLine("No details available", width))}
	}
	actionRows := []string{
		"",
		renderHelpPanelActionRow(
			renderDialogAction("Enter", "close", commitActionKeyStyle, commitActionTextStyle),
			renderDialogAction("Esc", "close", cancelActionKeyStyle, cancelActionTextStyle),
		),
	}
	headerRows := []string{renderDialogHeader(embeddedSidebarSectionTitle(detail.Section), "", "", width)}
	rowLimit := max(1, maxHeight-len(headerRows)-len(actionRows))
	if len(rows) > rowLimit {
		remaining := len(rows) - rowLimit + 1
		rows = append(rows[:max(0, rowLimit-1)], detailMutedStyle.Render(fitLine(fmt.Sprintf("+%d more rows", remaining), width)))
	}
	lines := append(headerRows, rows...)
	lines = append(lines, actionRows...)
	return strings.Join(lines, "\n")
}

func (m Model) embeddedSidebarDetailRows(section embeddedCodexSidebarSection, snapshot codexapp.Snapshot, projectPath string, width int) []string {
	switch section {
	case embeddedCodexSidebarSession:
		return m.embeddedSidebarSessionRows(snapshot, width)
	case embeddedCodexSidebarQuality:
		return embeddedSidebarQualityDetailRows(snapshot, width)
	case embeddedCodexSidebarCritic:
		return embeddedSidebarCriticDetailRows(snapshot, width)
	case embeddedCodexSidebarVision:
		return embeddedSidebarVisionDetailRows(snapshot, width)
	case embeddedCodexSidebarBrowser:
		return m.embeddedSidebarBrowserRows(snapshot, width)
	case embeddedCodexSidebarProcesses:
		rows := m.embeddedSidebarProcessRows(projectPath, width, 20)
		if len(rows) == 0 {
			return []string{detailMutedStyle.Render(fitLine("No active project processes", width))}
		}
		return rows
	case embeddedCodexSidebarSummary:
		summary, style, ok := m.embeddedSidebarSummary(snapshot)
		if !ok {
			return nil
		}
		return embeddedSidebarWrappedRows(summary, style, width)
	default:
		return nil
	}
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
	if m.embeddedSidebarProjectKnownNonGit(projectPath) {
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
			var noGitErr service.NoGitRepositoryError
			if errors.As(err, &noGitErr) {
				return embeddedSidebarDiffPreviewMsg{
					projectPath: projectPath,
					seq:         seq,
					noGit:       true,
					projectName: noGitErr.ProjectName,
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
	current.NoGit = msg.noGit
	current.Branch = strings.TrimSpace(msg.branch)
	current.ProjectName = strings.TrimSpace(msg.projectName)
	current.Err = ""
	if msg.err != nil {
		current.Err = strings.TrimSpace(msg.err.Error())
	} else if msg.noGit {
		current.Clean = false
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
	current.NoGit = false
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
	current.NoGit = false
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
	lines = appendEmbeddedSidebarSection(lines, m.renderEmbeddedSidebarQualitySection(snapshot, contentWidth))
	lines = appendEmbeddedSidebarSection(lines, m.renderEmbeddedSidebarCriticSection(snapshot, contentWidth))
	lines = appendEmbeddedSidebarSection(lines, m.renderEmbeddedSidebarVisionSection(snapshot, contentWidth))
	lines = appendEmbeddedSidebarSection(lines, m.renderEmbeddedSidebarBrowserSection(snapshot, contentWidth))
	lines = appendEmbeddedSidebarSection(lines, m.renderEmbeddedSidebarDiffSection(projectPath, contentWidth))
	lines = appendEmbeddedSidebarSection(lines, m.renderEmbeddedSidebarProcessSection(projectPath, contentWidth))
	lines = appendEmbeddedSidebarSection(lines, m.renderEmbeddedSidebarSummarySection(snapshot, contentWidth))
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

func (m Model) renderEmbeddedSidebarSessionSection(snapshot codexapp.Snapshot, width int) []string {
	rows := m.embeddedSidebarSessionRows(snapshot, width)
	if len(rows) == 0 {
		return nil
	}
	return append([]string{m.renderEmbeddedSidebarSectionHeader(embeddedCodexSidebarSession, "Session", width)}, rows...)
}

func (m Model) embeddedSidebarSessionRows(snapshot codexapp.Snapshot, width int) []string {
	rows := []string{}
	rows = append(rows, embeddedSidebarModelRows(snapshot, width)...)
	if row := embeddedSidebarContextRow(snapshot, width); row != "" {
		rows = append(rows, row)
	}
	if tokens := codexSnapshotTokenUsageLabel(snapshot); tokens != "" {
		rows = append(rows, embeddedSidebarFieldRow("Tokens", tokens, detailValueStyle, width))
	}
	if commands := embeddedSidebarModelCommands(snapshot); commands != "" {
		rows = append(rows, embeddedSidebarFieldRow("Commands", commands, detailMutedStyle, width))
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

func embeddedSidebarModelCommands(snapshot codexapp.Snapshot) string {
	if strings.TrimSpace(snapshot.Model) == "" &&
		strings.TrimSpace(snapshot.PendingModel) == "" &&
		strings.TrimSpace(snapshot.CriticModel) == "" {
		return ""
	}
	switch snapshot.Provider {
	case codexapp.ProviderLCAgent:
		return "/model /critic"
	case codexapp.ProviderCodex, codexapp.ProviderClaudeCode, codexapp.ProviderOpenCode:
		return "/model"
	default:
		return ""
	}
}

func embeddedSidebarModelRows(snapshot codexapp.Snapshot, width int) []string {
	rows := []string{}
	model := strings.TrimSpace(snapshot.Model)
	reasoning := strings.TrimSpace(snapshot.ReasoningEffort)
	showPendingAsCurrent := codexSnapshotShowsPendingModelAsCurrent(snapshot)
	if showPendingAsCurrent {
		model = strings.TrimSpace(snapshot.PendingModel)
		reasoning = firstNonEmptyCodexLabel(strings.TrimSpace(snapshot.PendingReasoning), reasoning)
	}
	if model != "" {
		value := model
		if reasoning != "" {
			value += " / " + reasoning
		}
		rows = append(rows, embeddedSidebarWrappedFieldRows("Model", value, detailValueStyle, width, 2)...)
	} else if reasoning != "" {
		rows = append(rows, embeddedSidebarWrappedFieldRows("Reasoning", reasoning, detailValueStyle, width, 2)...)
	}
	if nextModel := strings.TrimSpace(snapshot.PendingModel); nextModel != "" && !showPendingAsCurrent {
		nextReasoning := firstNonEmptyCodexLabel(strings.TrimSpace(snapshot.PendingReasoning), strings.TrimSpace(snapshot.ReasoningEffort))
		next := nextModel
		if nextReasoning != "" {
			next += " / " + nextReasoning
		}
		rows = append(rows, embeddedSidebarWrappedFieldRows("Next", next, detailWarningStyle, width, 2)...)
	}
	return rows
}

func (m Model) embeddedSidebarSectionSelected(section embeddedCodexSidebarSection) bool {
	return m.codexPanelFocus == embeddedCodexFocusSidebar && m.codexSidebarSelected == section
}

func embeddedSidebarCriticModelLabel(snapshot codexapp.Snapshot) string {
	model := strings.TrimSpace(snapshot.CriticModel)
	if model == "" {
		return ""
	}
	provider := strings.TrimSpace(snapshot.CriticModelProvider)
	if provider == "" || strings.EqualFold(provider, strings.TrimSpace(snapshot.ModelProvider)) {
		return model
	}
	return provider + "/" + model
}

func (m Model) renderEmbeddedSidebarCriticSection(snapshot codexapp.Snapshot, width int) []string {
	rows := embeddedSidebarCriticSummaryRows(snapshot, width)
	if m.embeddedSidebarSectionSelected(embeddedCodexSidebarCritic) {
		rows = embeddedSidebarCriticDetailRows(snapshot, width)
	}
	if len(rows) == 0 {
		return nil
	}
	return append([]string{m.renderEmbeddedSidebarSectionHeader(embeddedCodexSidebarCritic, "Critic", width)}, rows...)
}

func embeddedSidebarCriticRelevant(snapshot codexapp.Snapshot) bool {
	if snapshot.Provider != codexapp.ProviderLCAgent {
		return false
	}
	model := embeddedSidebarCriticModelLabel(snapshot)
	status := embeddedSidebarCriticStatus(snapshot)
	return model != "" ||
		status != "" ||
		snapshot.CriticReviews != 0 ||
		snapshot.CriticConsultations != 0 ||
		snapshot.CriticConsultConcerns != 0 ||
		snapshot.CriticConcerns != 0 ||
		snapshot.CriticLeadRevisions != 0 ||
		snapshot.CriticFollowupDrafts != 0
}

func embeddedSidebarCriticSummaryRows(snapshot codexapp.Snapshot, width int) []string {
	if !embeddedSidebarCriticRelevant(snapshot) {
		return nil
	}
	rows := []string{}
	if model := embeddedSidebarCriticModelValue(snapshot); model != "" {
		rows = append(rows, embeddedSidebarWrappedFieldRows("Model", model, detailValueStyle, width, 2)...)
	}
	status := embeddedSidebarCriticStatus(snapshot)
	activity := embeddedSidebarCriticActivitySummary(snapshot)
	if status != "" || activity != "" {
		value := strings.TrimSpace(status)
		if activity != "" {
			if value != "" {
				value += " | "
			}
			value += activity
		}
		rows = append(rows, embeddedSidebarWrappedFieldRows("State", value, embeddedSidebarCriticStatusStyle(status), width, 3)...)
	}
	if summary := strings.TrimSpace(snapshot.CriticLastSummary); summary != "" {
		rows = append(rows, detailMutedStyle.Render(fitLine(summary, width)))
	}
	return rows
}

func embeddedSidebarCriticDetailRows(snapshot codexapp.Snapshot, width int) []string {
	if !embeddedSidebarCriticRelevant(snapshot) {
		return nil
	}
	rows := []string{}
	if model := embeddedSidebarCriticModelValue(snapshot); model != "" {
		rows = append(rows, embeddedSidebarWrappedFieldRows("Model", model, detailValueStyle, width, 2)...)
	}
	status := embeddedSidebarCriticStatus(snapshot)
	if status != "" {
		rows = append(rows, embeddedSidebarFieldRow("Status", status, embeddedSidebarCriticStatusStyle(status), width))
	}
	if activity := embeddedSidebarCriticActivitySummary(snapshot); activity != "" {
		rows = append(rows, embeddedSidebarWrappedFieldRows("Activity", activity, detailValueStyle, width, 3)...)
	}
	if concerns := embeddedSidebarCriticConcernSummary(snapshot); concerns != "" {
		rows = append(rows, embeddedSidebarWrappedFieldRows("Concerns", concerns, detailWarningStyle, width, 2)...)
	}
	if snapshot.CriticFollowupDrafts > 0 {
		rows = append(rows, embeddedSidebarFieldRow("Drafts", fmt.Sprintf("%d", snapshot.CriticFollowupDrafts), detailWarningStyle, width))
	}
	if summary := strings.TrimSpace(snapshot.CriticLastSummary); summary != "" {
		rows = append(rows, detailMutedStyle.Render(fitLine(summary, width)))
	}
	return rows
}

func embeddedSidebarCriticModelValue(snapshot codexapp.Snapshot) string {
	model := embeddedSidebarCriticModelLabel(snapshot)
	if model == "" {
		return ""
	}
	if reasoning := strings.TrimSpace(snapshot.CriticReasoningEffort); reasoning != "" {
		model += " / " + reasoning
	}
	return model
}

func embeddedSidebarCriticActivitySummary(snapshot codexapp.Snapshot) string {
	parts := []string{}
	if snapshot.CriticReviews > 0 {
		parts = append(parts, embeddedSidebarCountLabel(snapshot.CriticReviews, "review"))
	}
	if snapshot.CriticConsultations > 0 {
		parts = append(parts, embeddedSidebarCountLabel(snapshot.CriticConsultations, "consult"))
	}
	if concerns := max(0, snapshot.CriticConcerns) + max(0, snapshot.CriticConsultConcerns); concerns > 0 {
		parts = append(parts, embeddedSidebarCountLabel(concerns, "concern"))
	}
	if snapshot.CriticLeadRevisions > 0 {
		parts = append(parts, embeddedSidebarCountLabel(snapshot.CriticLeadRevisions, "correction"))
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " | ")
}

func embeddedSidebarCriticConcernSummary(snapshot codexapp.Snapshot) string {
	parts := []string{}
	if snapshot.CriticConcerns > 0 {
		parts = append(parts, fmt.Sprintf("lead %d", snapshot.CriticConcerns))
	}
	if snapshot.CriticConsultConcerns > 0 {
		parts = append(parts, fmt.Sprintf("consult %d", snapshot.CriticConsultConcerns))
	}
	return strings.Join(parts, " | ")
}

func (m Model) renderEmbeddedSidebarQualitySection(snapshot codexapp.Snapshot, width int) []string {
	rows := embeddedSidebarQualitySummaryRows(snapshot, width)
	if m.embeddedSidebarSectionSelected(embeddedCodexSidebarQuality) {
		rows = embeddedSidebarQualityDetailRows(snapshot, width)
	}
	if len(rows) == 0 {
		return nil
	}
	return append([]string{m.renderEmbeddedSidebarSectionHeader(embeddedCodexSidebarQuality, "Quality", width)}, rows...)
}

func embeddedSidebarQualityRelevant(snapshot codexapp.Snapshot) bool {
	if snapshot.Provider != codexapp.ProviderLCAgent {
		return false
	}
	return snapshot.QualityCheckpointActive ||
		snapshot.QualityCheckpointPasses != 0 ||
		snapshot.QualityCheckpointMaxPasses != 0 ||
		strings.TrimSpace(snapshot.QualityCheckpointLastSummary) != "" ||
		snapshot.QualityRepairActive ||
		snapshot.QualityRepairPasses != 0 ||
		snapshot.QualityRepairMaxPasses != 0 ||
		strings.TrimSpace(snapshot.QualityRepairLastSummary) != "" ||
		snapshot.QualityPlanUpdates != 0 ||
		snapshot.QualityPlanPhases != 0 ||
		strings.TrimSpace(snapshot.QualityPlanLastSummary) != ""
}

func embeddedSidebarQualitySummaryRows(snapshot codexapp.Snapshot, width int) []string {
	if !embeddedSidebarQualityRelevant(snapshot) {
		return nil
	}
	status, style := embeddedSidebarQualityStatus(snapshot)
	parts := []string{status}
	if snapshot.QualityCheckpointActive || snapshot.QualityCheckpointPasses > 0 || snapshot.QualityCheckpointMaxPasses > 0 {
		parts = append(parts, "checks "+embeddedSidebarPassLabel(snapshot.QualityCheckpointPasses, snapshot.QualityCheckpointMaxPasses))
	}
	if snapshot.QualityRepairActive || snapshot.QualityRepairPasses > 0 || snapshot.QualityRepairMaxPasses > 0 {
		parts = append(parts, "repairs "+embeddedSidebarPassLabel(snapshot.QualityRepairPasses, snapshot.QualityRepairMaxPasses))
	}
	if plan := embeddedSidebarQualityPlanSummary(snapshot); plan != "" {
		parts = append(parts, plan)
	}
	if evidence := embeddedSidebarQualityEvidenceSummary(snapshot); evidence != "" {
		parts = append(parts, "needs "+evidence)
	}
	rows := []string{}
	rows = append(rows, embeddedSidebarWrappedFieldRows("State", strings.Join(parts, " | "), style, width, 3)...)
	if summary := embeddedSidebarQualityLatestSummary(snapshot); summary != "" {
		rows = append(rows, detailMutedStyle.Render(fitLine(summary, width)))
	}
	return rows
}

func embeddedSidebarQualityDetailRows(snapshot codexapp.Snapshot, width int) []string {
	if !embeddedSidebarQualityRelevant(snapshot) {
		return nil
	}
	rows := []string{}
	status, style := embeddedSidebarQualityStatus(snapshot)
	rows = append(rows, embeddedSidebarFieldRow("Status", status, style, width))
	passes := embeddedSidebarPassLabel(snapshot.QualityCheckpointPasses, snapshot.QualityCheckpointMaxPasses)
	rows = append(rows, embeddedSidebarFieldRow("Checkpoints", passes, detailValueStyle, width))
	repairPasses := embeddedSidebarPassLabel(snapshot.QualityRepairPasses, snapshot.QualityRepairMaxPasses)
	if snapshot.QualityRepairPasses > 0 || snapshot.QualityRepairMaxPasses > 0 || snapshot.QualityRepairActive {
		rows = append(rows, embeddedSidebarFieldRow("Repairs", repairPasses, detailWarningStyle, width))
	}
	if snapshot.QualityPlanUpdates > 0 || snapshot.QualityPlanPhases > 0 {
		phaseValue := fmt.Sprintf("%d", max(0, snapshot.QualityPlanPhases))
		if snapshot.QualityPlanVerified > 0 || snapshot.QualityPlanSkipped > 0 {
			phaseValue += fmt.Sprintf(" (%d verified", max(0, snapshot.QualityPlanVerified))
			if snapshot.QualityPlanSkipped > 0 {
				phaseValue += fmt.Sprintf(", %d skipped", snapshot.QualityPlanSkipped)
			}
			phaseValue += ")"
		}
		phaseStyle := detailValueStyle
		if snapshot.QualityPlanNeedsRepair > 0 {
			phaseStyle = detailWarningStyle
		}
		rows = append(rows, embeddedSidebarWrappedFieldRows("Plan", phaseValue, phaseStyle, width, 2)...)
		var requirements []string
		if snapshot.QualityPlanRequiresRuntime {
			requirements = append(requirements, "runtime")
		}
		if snapshot.QualityPlanRequiresVisual {
			requirements = append(requirements, "visual")
		}
		if snapshot.QualityPlanRequiresTemporal {
			requirements = append(requirements, "temporal")
		}
		if len(requirements) > 0 {
			rows = append(rows, embeddedSidebarFieldRow("Evidence", strings.Join(requirements, "+"), detailWarningStyle, width))
		}
		rows = append(rows, embeddedSidebarQualityPhaseRows(snapshot.QualityPlanPhaseItems, width)...)
	}
	if summary := strings.TrimSpace(snapshot.QualityCheckpointLastSummary); summary != "" {
		rows = append(rows, detailMutedStyle.Render(fitLine(summary, width)))
	}
	if summary := strings.TrimSpace(snapshot.QualityRepairLastSummary); summary != "" {
		rows = append(rows, detailMutedStyle.Render(fitLine(summary, width)))
	}
	if summary := strings.TrimSpace(snapshot.QualityPlanLastSummary); summary != "" {
		rows = append(rows, detailMutedStyle.Render(fitLine(summary, width)))
	}
	return rows
}

func embeddedSidebarQualityStatus(snapshot codexapp.Snapshot) (string, lipgloss.Style) {
	switch {
	case snapshot.QualityCheckpointActive:
		return "checking", detailValueStyle
	case snapshot.QualityRepairActive:
		return "repairing", detailWarningStyle
	case snapshot.QualityPlanNeedsRepair > 0:
		return "needs repair", detailWarningStyle
	case snapshot.QualityCheckpointPasses > 0:
		return "checked", detailValueStyle
	case snapshot.QualityPlanUpdates > 0 || snapshot.QualityPlanPhases > 0:
		return "planned", detailValueStyle
	default:
		return "idle", detailMutedStyle
	}
}

func embeddedSidebarPassLabel(passes, maxPasses int) string {
	label := fmt.Sprintf("%d", max(0, passes))
	if maxPasses > 0 {
		label += fmt.Sprintf("/%d", maxPasses)
	}
	return label
}

func embeddedSidebarQualityPlanSummary(snapshot codexapp.Snapshot) string {
	if snapshot.QualityPlanUpdates == 0 && snapshot.QualityPlanPhases == 0 {
		return ""
	}
	label := fmt.Sprintf("plan %d", max(0, snapshot.QualityPlanPhases))
	counts := []string{}
	if snapshot.QualityPlanVerified > 0 {
		counts = append(counts, fmt.Sprintf("%d ok", snapshot.QualityPlanVerified))
	}
	if snapshot.QualityPlanSkipped > 0 {
		counts = append(counts, fmt.Sprintf("%d skip", snapshot.QualityPlanSkipped))
	}
	if snapshot.QualityPlanNeedsRepair > 0 {
		counts = append(counts, fmt.Sprintf("%d fix", snapshot.QualityPlanNeedsRepair))
	}
	if len(counts) > 0 {
		label += " (" + strings.Join(counts, ", ") + ")"
	}
	return label
}

func embeddedSidebarQualityEvidenceSummary(snapshot codexapp.Snapshot) string {
	requirements := []string{}
	if snapshot.QualityPlanRequiresRuntime {
		requirements = append(requirements, "runtime")
	}
	if snapshot.QualityPlanRequiresVisual {
		requirements = append(requirements, "visual")
	}
	return strings.Join(requirements, "+")
}

func embeddedSidebarQualityLatestSummary(snapshot codexapp.Snapshot) string {
	for _, summary := range []string{
		snapshot.QualityRepairLastSummary,
		snapshot.QualityCheckpointLastSummary,
		snapshot.QualityPlanLastSummary,
	} {
		if summary = strings.TrimSpace(summary); summary != "" {
			return summary
		}
	}
	return ""
}

func embeddedSidebarQualityPhaseRows(phases []codexapp.QualityPlanPhaseSnapshot, width int) []string {
	if len(phases) == 0 {
		return nil
	}
	const limit = 8
	rows := []string{detailMutedStyle.Render(fitLine("Plan phases", width))}
	count := len(phases)
	if count > limit {
		count = limit
	}
	for _, phase := range phases[:count] {
		status := embeddedSidebarQualityPhaseStatus(phase.Status)
		text := status + " " + strings.TrimSpace(phase.Name)
		if strings.TrimSpace(phase.Name) == "" {
			text = status + " phase"
		}
		if note := strings.TrimSpace(phase.Notes); note != "" && (strings.EqualFold(phase.Status, "needs_repair") || strings.EqualFold(phase.Status, "skipped")) {
			text += ": " + note
		} else if phase.EvidenceCount > 0 {
			evidenceLabel := "evidence"
			text += fmt.Sprintf(" [%d %s]", phase.EvidenceCount, evidenceLabel)
		}
		rows = append(rows, embeddedSidebarQualityPhaseStyle(phase.Status).Render(fitLine(text, width)))
	}
	if len(phases) > limit {
		rows = append(rows, detailMutedStyle.Render(fitLine(fmt.Sprintf("+%d more phases", len(phases)-limit), width)))
	}
	return rows
}

func embeddedSidebarQualityPhaseStatus(status string) string {
	switch strings.ToLower(strings.ReplaceAll(strings.TrimSpace(status), "-", "_")) {
	case "verified":
		return "ok"
	case "implemented":
		return "impl"
	case "in_progress":
		return "doing"
	case "needs_repair":
		return "fix"
	case "skipped":
		return "skip"
	case "planned", "":
		return "todo"
	default:
		return strings.ToLower(strings.TrimSpace(status))
	}
}

func embeddedSidebarQualityPhaseStyle(status string) lipgloss.Style {
	switch strings.ToLower(strings.ReplaceAll(strings.TrimSpace(status), "-", "_")) {
	case "verified", "implemented":
		return detailValueStyle
	case "needs_repair":
		return detailWarningStyle
	default:
		return detailMutedStyle
	}
}

func embeddedSidebarVisionModelLabel(snapshot codexapp.Snapshot) string {
	model := strings.TrimSpace(snapshot.VisionModel)
	if model == "" {
		return ""
	}
	provider := strings.TrimSpace(snapshot.VisionModelProvider)
	if provider == "" || strings.EqualFold(provider, strings.TrimSpace(snapshot.ModelProvider)) {
		return model
	}
	return provider + "/" + model
}

func (m Model) renderEmbeddedSidebarVisionSection(snapshot codexapp.Snapshot, width int) []string {
	rows := embeddedSidebarVisionSummaryRows(snapshot, width)
	if m.embeddedSidebarSectionSelected(embeddedCodexSidebarVision) {
		rows = embeddedSidebarVisionDetailRows(snapshot, width)
	}
	if len(rows) == 0 {
		return nil
	}
	return append([]string{m.renderEmbeddedSidebarSectionHeader(embeddedCodexSidebarVision, "Vision", width)}, rows...)
}

func embeddedSidebarVisionRelevant(snapshot codexapp.Snapshot) bool {
	if snapshot.Provider != codexapp.ProviderLCAgent {
		return false
	}
	model := embeddedSidebarVisionModelLabel(snapshot)
	return model != "" ||
		snapshot.ImageAnalysisActive ||
		snapshot.ImageAnalyses != 0 ||
		snapshot.ImageAnalysisFailures != 0 ||
		strings.TrimSpace(snapshot.ImageAnalysisLastSummary) != ""
}

func embeddedSidebarVisionSummaryRows(snapshot codexapp.Snapshot, width int) []string {
	if !embeddedSidebarVisionRelevant(snapshot) {
		return nil
	}
	rows := []string{}
	if model := embeddedSidebarVisionModelLabel(snapshot); model != "" {
		rows = append(rows, embeddedSidebarWrappedFieldRows("Model", model, detailValueStyle, width, 2)...)
	}
	status := "idle"
	style := detailValueStyle
	if snapshot.ImageAnalysisActive {
		status = "analyzing"
	} else if snapshot.ImageAnalysisFailures > 0 {
		style = detailWarningStyle
	}
	parts := []string{status}
	if snapshot.ImageAnalyses > 0 {
		parts = append(parts, embeddedSidebarCountLabel(snapshot.ImageAnalyses, "analysis"))
	}
	if snapshot.ImageAnalysisFailures > 0 {
		parts = append(parts, embeddedSidebarCountLabel(snapshot.ImageAnalysisFailures, "failure"))
	}
	rows = append(rows, embeddedSidebarWrappedFieldRows("State", strings.Join(parts, " | "), style, width, 2)...)
	if summary := strings.TrimSpace(snapshot.ImageAnalysisLastSummary); summary != "" {
		rows = append(rows, detailMutedStyle.Render(fitLine(summary, width)))
	}
	return rows
}

func embeddedSidebarVisionDetailRows(snapshot codexapp.Snapshot, width int) []string {
	if !embeddedSidebarVisionRelevant(snapshot) {
		return nil
	}
	rows := []string{}
	if model := embeddedSidebarVisionModelLabel(snapshot); model != "" {
		rows = append(rows, embeddedSidebarWrappedFieldRows("Model", model, detailValueStyle, width, 2)...)
	}
	status := "idle"
	style := detailValueStyle
	if snapshot.ImageAnalysisActive {
		status = "analyzing"
	} else if snapshot.ImageAnalysisFailures > 0 {
		style = detailWarningStyle
	}
	rows = append(rows, embeddedSidebarFieldRow("Status", status, style, width))
	rows = append(rows, embeddedSidebarFieldRow("Analyses", fmt.Sprintf("%d", max(0, snapshot.ImageAnalyses)), detailValueStyle, width))
	if snapshot.ImageAnalysisFailures > 0 {
		rows = append(rows, embeddedSidebarFieldRow("Failures", fmt.Sprintf("%d", snapshot.ImageAnalysisFailures), detailWarningStyle, width))
	}
	if summary := strings.TrimSpace(snapshot.ImageAnalysisLastSummary); summary != "" {
		rows = append(rows, detailMutedStyle.Render(fitLine(summary, width)))
	}
	return rows
}

func embeddedSidebarCriticStatus(snapshot codexapp.Snapshot) string {
	if snapshot.CriticActive {
		if strings.EqualFold(strings.TrimSpace(snapshot.CriticLastStatus), "consulting") {
			return "consulting"
		}
		return "reviewing"
	}
	status := strings.ToLower(strings.TrimSpace(snapshot.CriticLastStatus))
	switch status {
	case "":
		if strings.TrimSpace(snapshot.CriticModel) != "" {
			return "idle"
		}
		return ""
	case "clean":
		return "clean"
	case "needs-followup":
		return "needs follow-up"
	case "needs_followup":
		return "needs follow-up"
	default:
		return strings.ReplaceAll(status, "_", " ")
	}
}

func embeddedSidebarCriticStatusStyle(status string) lipgloss.Style {
	status = strings.ToLower(strings.TrimSpace(status))
	switch {
	case status == "reviewing", status == "clean":
		return detailValueStyle
	case strings.Contains(status, "concern"), strings.Contains(status, "revision"):
		return detailWarningStyle
	case strings.Contains(status, "follow"), strings.Contains(status, "failed"):
		return detailDangerStyle
	default:
		return detailMutedStyle
	}
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
	return append([]string{m.renderEmbeddedSidebarSectionHeader(embeddedCodexSidebarBrowser, "Browser", width)}, rows...)
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

func (m Model) renderEmbeddedSidebarSummarySection(snapshot codexapp.Snapshot, width int) []string {
	summary, style, ok := m.embeddedSidebarSummary(snapshot)
	if !ok {
		return nil
	}
	rows := embeddedSidebarWrappedRows(summary, style, width)
	if len(rows) == 0 {
		return nil
	}
	return append([]string{m.renderEmbeddedSidebarSectionHeader(embeddedCodexSidebarSummary, "Summary", width)}, rows...)
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
	project, _ := m.embeddedSidebarProjectSummary(projectPath)
	switch {
	case ok && state.NoGit:
		lines = append(lines, detailMutedStyle.Render(fitLine("No git repository", width)))
		lines = append(lines, detailMutedStyle.Render(fitLine("r checks again", width)))
	case ok && strings.TrimSpace(state.Err) != "":
		lines = append(lines, detailDangerStyle.Render(fitLine("Diff unavailable", width)))
		lines = append(lines, detailMutedStyle.Render(fitLine(state.Err, width)))
	case ok && state.Loading && state.Preview == nil && !state.Clean:
		lines = append(lines, detailMutedStyle.Render(fitLine("Preparing diff summary...", width)))
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
	case m.embeddedSidebarProjectKnownNonGit(projectPath):
		lines = append(lines, detailMutedStyle.Render(fitLine("No git repository", width)))
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

func (m Model) embeddedSidebarProjectSummary(projectPath string) (model.ProjectSummary, bool) {
	projectPath = normalizeProjectPath(projectPath)
	if projectPath == "" {
		return model.ProjectSummary{}, false
	}
	if project, ok := m.projectSummaryByPathAllProjects(projectPath); ok {
		return project, true
	}
	if normalizeProjectPath(m.detail.Summary.Path) == projectPath {
		return m.detail.Summary, true
	}
	return model.ProjectSummary{}, false
}

func (m Model) embeddedSidebarProjectKnownNonGit(projectPath string) bool {
	project, ok := m.embeddedSidebarProjectSummary(projectPath)
	if !ok {
		return false
	}
	return project.PresentOnDisk &&
		project.WorktreeKind == model.WorktreeKindNone &&
		strings.TrimSpace(project.RepoBranch) == "" &&
		!project.RepoDirty &&
		!project.RepoConflict
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

func (m Model) embeddedSidebarSummary(snapshot codexapp.Snapshot) (string, lipgloss.Style, bool) {
	projectPath := m.embeddedSidebarProjectPath(snapshot)
	project, ok := m.embeddedSidebarProjectSummary(projectPath)
	if !ok {
		project = model.ProjectSummary{Path: projectPath}
	}
	now := m.currentTime()
	if task, ok := m.agentTaskForProjectPath(projectPath); ok {
		summary := strings.TrimSpace(agentTaskListSummary(task))
		if summary == "" {
			return "", lipgloss.Style{}, false
		}
		style := detailValueStyle
		if model.NormalizeAgentTaskStatus(task.Status) == model.AgentTaskStatusWaiting {
			style = detailWarningStyle
		}
		return summary, style, true
	}
	if browserAttention, ok := m.projectPendingBrowserAttention(projectPath); ok {
		summary := strings.TrimSpace(browserAttentionListSummary(browserAttention))
		if summary == "" {
			return "", lipgloss.Style{}, false
		}
		return summary, detailWarningStyle, true
	}
	if startedAt, active := embeddedSnapshotActiveStartedAt(snapshot, project); active {
		return formatLiveEngineerSummary(liveEngineerActiveSummaryDetail(snapshot, project), startedAt, now), detailValueStyle, true
	}
	if summary, ok := m.projectLiveEngineerAssessmentSummary(project, now); ok {
		summary = strings.TrimSpace(summary)
		if summary == "" {
			return "", lipgloss.Style{}, false
		}
		return summary, detailValueStyle, true
	}
	if !ok {
		return "", lipgloss.Style{}, false
	}
	summary := strings.TrimSpace(m.projectAssessmentDisplayTextAt(project, now, m.assessmentStallThreshold()))
	if summary == "" || summary == "-" {
		return "", lipgloss.Style{}, false
	}
	return summary, m.projectListAssessmentSummaryStyle(project), true
}

func embeddedSidebarWrappedRows(text string, style lipgloss.Style, width int) []string {
	if width <= 0 {
		return nil
	}
	return renderWrappedDialogTextLines(style, max(1, width), strings.TrimSpace(text))
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

func embeddedSidebarWrappedFieldRows(label, value string, style lipgloss.Style, width, maxLines int) []string {
	label = strings.TrimSpace(label)
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if width <= 0 {
		return nil
	}
	prefix := label
	if prefix != "" {
		prefix += " "
	}
	valueWidth := max(1, width-ansi.StringWidth(prefix))
	if ansi.StringWidth(value) <= valueWidth {
		return []string{embeddedSidebarFieldRow(label, value, style, width)}
	}
	lines := embeddedSidebarWrapFieldValue(value, valueWidth)
	if len(lines) == 0 {
		return nil
	}
	if maxLines > 0 && len(lines) > maxLines {
		lines = lines[:maxLines]
		last := maxLines - 1
		lines[last] = truncateText(strings.TrimSpace(lines[last])+" ...", valueWidth)
	}
	rows := make([]string, 0, len(lines))
	continuation := strings.Repeat(" ", min(width-1, ansi.StringWidth(prefix)))
	for i, line := range lines {
		rendered := style.Render(fitLine(line, valueWidth))
		if i == 0 {
			rows = append(rows, fitStyledWidth(detailMutedStyle.Render(prefix)+rendered, width))
			continue
		}
		rows = append(rows, fitStyledWidth(detailMutedStyle.Render(continuation)+rendered, width))
	}
	return rows
}

func embeddedSidebarWrapFieldValue(value string, width int) []string {
	value = strings.TrimSpace(value)
	if value == "" || width <= 0 {
		return nil
	}
	if strings.Contains(value, " | ") {
		return embeddedSidebarWrapPipedValue(value, width)
	}
	return embeddedSidebarWrappedPlainLines(value, width)
}

func embeddedSidebarWrapPipedValue(value string, width int) []string {
	parts := strings.Split(value, " | ")
	lines := []string{}
	current := ""
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if ansi.StringWidth(part) > width {
			if current != "" {
				lines = append(lines, current)
				current = ""
			}
			lines = append(lines, embeddedSidebarWrappedPlainLines(part, width)...)
			continue
		}
		next := part
		if current != "" {
			next = current + " | " + part
		}
		if ansi.StringWidth(next) <= width {
			current = next
			continue
		}
		if current != "" {
			lines = append(lines, current)
		}
		current = part
	}
	if current != "" {
		lines = append(lines, current)
	}
	return lines
}

func embeddedSidebarWrappedPlainLines(value string, width int) []string {
	wrapped := strings.Split(ansi.Wrap(value, width, "/|-_."), "\n")
	lines := make([]string, 0, len(wrapped))
	for _, line := range wrapped {
		line = strings.TrimSpace(line)
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func embeddedSidebarCountLabel(count int, singular string) string {
	word := strings.TrimSpace(singular)
	if count == 1 {
		return fmt.Sprintf("1 %s", word)
	}
	switch word {
	case "analysis":
		word = "analyses"
	default:
		word = pluralize(word, count)
	}
	return fmt.Sprintf("%d %s", count, word)
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
