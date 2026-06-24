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
	embeddedCodexSidebarVision
	embeddedCodexSidebarBrowser
	embeddedCodexSidebarMCP
	embeddedCodexSidebarDiff
	embeddedCodexSidebarProcesses
	embeddedCodexSidebarSummary
	embeddedCodexSidebarSectionCount
)

const (
	embeddedSidebarDiffAutoInterval = 2 * time.Second
	embeddedSidebarPreviewTextLimit = 52
)

var embeddedSidebarMutedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("248"))

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
	Offset      int
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
		embeddedCodexSidebarVision,
		embeddedCodexSidebarBrowser,
		embeddedCodexSidebarMCP,
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
		return len(embeddedSidebarQualitySummaryRows(snapshot, 1)) > 0
	case embeddedCodexSidebarVision:
		return len(embeddedSidebarVisionSummaryRows(snapshot, 1)) > 0
	case embeddedCodexSidebarBrowser:
		return len(m.embeddedSidebarBrowserRows(snapshot, 1)) > 0
	case embeddedCodexSidebarMCP:
		return len(embeddedSidebarMCPRows(snapshot, 1, 1)) > 0
	case embeddedCodexSidebarDiff, embeddedCodexSidebarProcesses:
		return normalizeProjectPath(projectPath) != ""
	case embeddedCodexSidebarSummary:
		summary, style, ok := m.embeddedSidebarSummary(snapshot)
		return ok && len(embeddedSidebarPreviewRows(summary, style, 1)) > 0
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
	case embeddedCodexSidebarVision:
		return "Vision"
	case embeddedCodexSidebarBrowser:
		return "Browser"
	case embeddedCodexSidebarMCP:
		return "Used MCPs"
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
	case "up", "k":
		return m.moveEmbeddedSidebarDetailOffset(-1), nil
	case "down", "j":
		return m.moveEmbeddedSidebarDetailOffset(1), nil
	case "pgup", "ctrl+u":
		return m.moveEmbeddedSidebarDetailOffset(-m.embeddedSidebarDetailPageSize()), nil
	case "pgdown", "ctrl+d":
		return m.moveEmbeddedSidebarDetailOffset(m.embeddedSidebarDetailPageSize()), nil
	case "home":
		if m.embeddedSidebarDetail != nil {
			m.embeddedSidebarDetail.Offset = 0
			m.status = embeddedSidebarSectionTitle(m.embeddedSidebarDetail.Section) + " details top"
		}
		return m, nil
	case "end":
		if m.embeddedSidebarDetail != nil {
			_, maxOffset, _ := m.embeddedSidebarDetailScrollBounds()
			m.embeddedSidebarDetail.Offset = maxOffset
			m.status = embeddedSidebarSectionTitle(m.embeddedSidebarDetail.Section) + " details bottom"
		}
		return m, nil
	}
	return m, nil
}

func (m Model) moveEmbeddedSidebarDetailOffset(delta int) Model {
	if m.embeddedSidebarDetail == nil || delta == 0 {
		return m
	}
	offset, maxOffset, _ := m.embeddedSidebarDetailScrollBounds()
	next := clampInt(offset+delta, 0, maxOffset)
	m.embeddedSidebarDetail.Offset = next
	title := embeddedSidebarSectionTitle(m.embeddedSidebarDetail.Section)
	if maxOffset == 0 {
		m.status = title + " details fit"
	} else {
		m.status = fmt.Sprintf("%s details %d/%d", title, next+1, maxOffset+1)
	}
	return m
}

func (m Model) embeddedSidebarDetailPageSize() int {
	_, _, pageSize := m.embeddedSidebarDetailScrollBounds()
	return max(1, pageSize)
}

func (m Model) embeddedSidebarDetailScrollBounds() (offset, maxOffset, pageSize int) {
	detail := m.embeddedSidebarDetail
	if detail == nil {
		return 0, 0, 1
	}
	width, maxHeight := m.embeddedSidebarDetailContentGeometry()
	rows := m.embeddedSidebarDetailRowsForCurrentSnapshot(width)
	rowLimit := embeddedSidebarDetailRowLimit(maxHeight)
	maxOffset = max(0, len(rows)-rowLimit)
	offset = clampInt(detail.Offset, 0, maxOffset)
	pageSize = rowLimit
	return offset, maxOffset, pageSize
}

func (m Model) embeddedSidebarDetailContentGeometry() (width, maxHeight int) {
	bodyW := m.width
	if bodyW <= 0 {
		bodyW = 120
	}
	bodyH := m.height
	if bodyH <= 0 {
		bodyH = 30
	}
	panelWidth := min(bodyW, min(max(58, bodyW-16), 94))
	return max(32, panelWidth-4), max(8, bodyH-4)
}

func embeddedSidebarDetailRowLimit(maxHeight int) int {
	const headerRows = 1
	const actionRows = 2
	return max(1, maxHeight-headerRows-actionRows)
}

func (m Model) embeddedSidebarDetailRowsForCurrentSnapshot(width int) []string {
	detail := m.embeddedSidebarDetail
	if detail == nil {
		return nil
	}
	snapshot, ok := m.currentCodexSnapshot()
	if !ok {
		return nil
	}
	projectPath := normalizeProjectPath(firstNonEmptyString(detail.ProjectPath, m.embeddedSidebarProjectPath(snapshot)))
	rows := m.embeddedSidebarDetailRows(detail.Section, snapshot, projectPath, width)
	if len(rows) == 0 {
		return []string{embeddedSidebarMutedStyle.Render(fitLine("No details available", width))}
	}
	return rows
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
			embeddedSidebarMutedStyle.Render(fitLine("Embedded session is no longer available", width)),
			"",
			renderHelpPanelActionRow(renderDialogAction("Esc", "close", cancelActionKeyStyle, cancelActionTextStyle)),
		}, "\n")
	}
	projectPath := normalizeProjectPath(firstNonEmptyString(detail.ProjectPath, m.embeddedSidebarProjectPath(snapshot)))
	rows := m.embeddedSidebarDetailRows(detail.Section, snapshot, projectPath, width)
	if len(rows) == 0 {
		rows = []string{embeddedSidebarMutedStyle.Render(fitLine("No details available", width))}
	}
	rowLimit := embeddedSidebarDetailRowLimit(maxHeight)
	maxOffset := max(0, len(rows)-rowLimit)
	offset := clampInt(detail.Offset, 0, maxOffset)
	if len(rows) > rowLimit {
		rows = append([]string{}, rows[offset:min(len(rows), offset+rowLimit)]...)
	}
	actions := []string{
		renderDialogAction("Up/Dn", "scroll", navigateActionKeyStyle, navigateActionTextStyle),
	}
	if maxOffset == 0 {
		actions = nil
	}
	actions = append(actions,
		renderDialogAction("Enter", "close", commitActionKeyStyle, commitActionTextStyle),
		renderDialogAction("Esc", "close", cancelActionKeyStyle, cancelActionTextStyle),
	)
	actionRows := []string{
		"",
		renderHelpPanelActionRow(actions...),
	}
	headerRows := []string{renderDialogHeader(embeddedSidebarSectionTitle(detail.Section), "", "", width)}
	lines := append(headerRows, rows...)
	lines = append(lines, actionRows...)
	return strings.Join(lines, "\n")
}

func (m Model) embeddedSidebarDetailRows(section embeddedCodexSidebarSection, snapshot codexapp.Snapshot, projectPath string, width int) []string {
	switch section {
	case embeddedCodexSidebarSession:
		return embeddedSidebarSessionDetailRows(snapshot, width)
	case embeddedCodexSidebarQuality:
		return embeddedSidebarQualityDetailRows(snapshot, width)
	case embeddedCodexSidebarVision:
		return embeddedSidebarVisionDetailRows(snapshot, width)
	case embeddedCodexSidebarBrowser:
		return m.embeddedSidebarBrowserDetailRows(snapshot, width)
	case embeddedCodexSidebarMCP:
		return embeddedSidebarMCPDetailRows(snapshot, width)
	case embeddedCodexSidebarProcesses:
		rows := m.embeddedSidebarProcessDetailRows(projectPath, width, 0)
		if len(rows) == 0 {
			return []string{embeddedSidebarMutedStyle.Render(fitLine("No active project processes", width))}
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
	lines = appendEmbeddedSidebarSection(lines, m.renderEmbeddedSidebarVisionSection(snapshot, contentWidth))
	lines = appendEmbeddedSidebarSection(lines, m.renderEmbeddedSidebarBrowserSection(snapshot, contentWidth))
	lines = appendEmbeddedSidebarSection(lines, m.renderEmbeddedSidebarMCPSection(snapshot, contentWidth))
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

func embeddedSidebarActionRow(key, label string, width int) string {
	rendered := footerNavAction(key, label).render()
	if rendered == "" {
		return ""
	}
	return fitStyledWidth(rendered, width)
}

func (m Model) renderEmbeddedSidebarSectionHeader(section embeddedCodexSidebarSection, title string, width int) string {
	selected := m.codexSidebarSelected == section && m.codexPanelFocus == embeddedCodexFocusSidebar
	marker := " "
	if selected {
		marker = ">"
	}
	text := marker + " " + title
	if selected {
		return commandPaletteSelectStyle.Width(width).Render(fitFooterWidth(text, max(1, width)))
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
	return embeddedSidebarSessionRows(snapshot, width, false)
}

func embeddedSidebarSessionDetailRows(snapshot codexapp.Snapshot, width int) []string {
	return embeddedSidebarSessionRows(snapshot, width, true)
}

func embeddedSidebarSessionRows(snapshot codexapp.Snapshot, width int, detail bool) []string {
	rows := []string{}
	modelLineLimit := 2
	if detail {
		modelLineLimit = 0
	}
	rows = append(rows, embeddedSidebarModelRowsWithLimit(snapshot, width, modelLineLimit)...)
	if row := embeddedSidebarContextRow(snapshot, width); row != "" {
		rows = append(rows, row)
	}
	if tokens := codexSnapshotTokenUsageLabel(snapshot); tokens != "" {
		rows = append(rows, embeddedSidebarFieldRow("Tokens", tokens, detailValueStyle, width))
	}
	if commands := embeddedSidebarModelCommands(snapshot); commands != "" {
		rows = append(rows, embeddedSidebarFieldRow("Commands", commands, embeddedSidebarMutedStyle, width))
	}
	if goal := snapshot.Goal; goal != nil {
		label := codexSnapshotGoalLabel(snapshot)
		if label == "" {
			label = "active"
		}
		rows = append(rows, embeddedSidebarFieldRow("Goal", label, embeddedSidebarGoalStyle(goal.Status), width))
		if objective := strings.TrimSpace(goal.Objective); objective != "" {
			if detail {
				rows = append(rows, embeddedSidebarWrappedRows(objective, embeddedSidebarMutedStyle, width)...)
			} else {
				rows = append(rows, embeddedSidebarPreviewRows(objective, embeddedSidebarMutedStyle, width)...)
			}
		}
	}
	return rows
}

func embeddedSidebarModelCommands(snapshot codexapp.Snapshot) string {
	if strings.TrimSpace(snapshot.Model) == "" &&
		strings.TrimSpace(snapshot.PendingModel) == "" {
		return ""
	}
	switch snapshot.Provider {
	case codexapp.ProviderLCAgent:
		return "/model"
	case codexapp.ProviderCodex, codexapp.ProviderClaudeCode, codexapp.ProviderOpenCode:
		return "/model"
	default:
		return ""
	}
}

func embeddedSidebarModelRows(snapshot codexapp.Snapshot, width int) []string {
	return embeddedSidebarModelRowsWithLimit(snapshot, width, 2)
}

func embeddedSidebarModelRowsWithLimit(snapshot codexapp.Snapshot, width, maxLines int) []string {
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
		rows = append(rows, embeddedSidebarWrappedFieldRows("Model", value, detailValueStyle, width, maxLines)...)
	} else if reasoning != "" {
		rows = append(rows, embeddedSidebarWrappedFieldRows("Reasoning", reasoning, detailValueStyle, width, maxLines)...)
	}
	if nextModel := strings.TrimSpace(snapshot.PendingModel); nextModel != "" && !showPendingAsCurrent {
		nextReasoning := firstNonEmptyCodexLabel(strings.TrimSpace(snapshot.PendingReasoning), strings.TrimSpace(snapshot.ReasoningEffort))
		next := nextModel
		if nextReasoning != "" {
			next += " / " + nextReasoning
		}
		rows = append(rows, embeddedSidebarWrappedFieldRows("Next", next, detailWarningStyle, width, maxLines)...)
	}
	return rows
}

func (m Model) renderEmbeddedSidebarQualitySection(snapshot codexapp.Snapshot, width int) []string {
	rows := embeddedSidebarQualitySummaryRows(snapshot, width)
	if len(rows) == 0 {
		return nil
	}
	return append([]string{m.renderEmbeddedSidebarSectionHeader(embeddedCodexSidebarQuality, "Quality", width)}, rows...)
}

func embeddedSidebarQualityRelevant(snapshot codexapp.Snapshot) bool {
	if snapshot.Provider != codexapp.ProviderLCAgent {
		return false
	}
	return snapshot.QualityPlanUpdates != 0 ||
		snapshot.QualityPlanPhases != 0 ||
		strings.TrimSpace(snapshot.QualityPlanLastSummary) != ""
}

func embeddedSidebarQualitySummaryRows(snapshot codexapp.Snapshot, width int) []string {
	if !embeddedSidebarQualityRelevant(snapshot) {
		return nil
	}
	status, style := embeddedSidebarQualityStatus(snapshot)
	parts := []string{status}
	if plan := embeddedSidebarQualityPlanSummary(snapshot); plan != "" {
		parts = append(parts, plan)
	}
	if evidence := embeddedSidebarQualityEvidenceSummary(snapshot); evidence != "" {
		parts = append(parts, "needs "+evidence)
	}
	rows := []string{}
	rows = append(rows, embeddedSidebarWrappedFieldRows("State", strings.Join(parts, " | "), style, width, 3)...)
	if summary := embeddedSidebarQualityLatestSummary(snapshot); summary != "" {
		rows = append(rows, embeddedSidebarPreviewRows(summary, embeddedSidebarMutedStyle, width)...)
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
	if summary := strings.TrimSpace(snapshot.QualityPlanLastSummary); summary != "" {
		rows = append(rows, embeddedSidebarWrappedRows(summary, embeddedSidebarMutedStyle, width)...)
	}
	return rows
}

func embeddedSidebarQualityStatus(snapshot codexapp.Snapshot) (string, lipgloss.Style) {
	switch {
	case snapshot.QualityPlanNeedsRepair > 0:
		return "needs repair", detailWarningStyle
	case snapshot.QualityPlanUpdates > 0 || snapshot.QualityPlanPhases > 0:
		return "planned", detailValueStyle
	default:
		return "idle", embeddedSidebarMutedStyle
	}
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
	if summary := strings.TrimSpace(snapshot.QualityPlanLastSummary); summary != "" {
		return summary
	}
	return ""
}

func embeddedSidebarQualityPhaseRows(phases []codexapp.QualityPlanPhaseSnapshot, width int) []string {
	if len(phases) == 0 {
		return nil
	}
	rows := []string{embeddedSidebarMutedStyle.Render(fitLine("Plan phases", width))}
	for _, phase := range phases {
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
		rows = append(rows, embeddedSidebarWrappedRows(text, embeddedSidebarQualityPhaseStyle(phase.Status), width)...)
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
		return embeddedSidebarMutedStyle
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
	status, style := embeddedSidebarVisionStatusSummary(snapshot)
	rows = append(rows, embeddedSidebarWrappedFieldRows("State", status, style, width, 2)...)
	if summary := strings.TrimSpace(snapshot.ImageAnalysisLastSummary); summary != "" {
		rows = append(rows, embeddedSidebarPreviewRows(summary, embeddedSidebarMutedStyle, width)...)
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
	status, style := embeddedSidebarVisionStatusSummary(snapshot)
	rows = append(rows, embeddedSidebarWrappedFieldRows("Status", status, style, width, 0)...)
	if summary := strings.TrimSpace(snapshot.ImageAnalysisLastSummary); summary != "" {
		rows = append(rows, embeddedSidebarWrappedRows(summary, embeddedSidebarMutedStyle, width)...)
	}
	return rows
}

func embeddedSidebarVisionStatusSummary(snapshot codexapp.Snapshot) (string, lipgloss.Style) {
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
	return strings.Join(parts, " | "), style
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
		return embeddedSidebarMutedStyle
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
	return m.embeddedSidebarBrowserRowsWithMode(snapshot, width, false)
}

func (m Model) embeddedSidebarBrowserDetailRows(snapshot codexapp.Snapshot, width int) []string {
	return m.embeddedSidebarBrowserRowsWithMode(snapshot, width, true)
}

func (m Model) embeddedSidebarBrowserRowsWithMode(snapshot codexapp.Snapshot, width int, detail bool) []string {
	if snapshot.Closed {
		return nil
	}
	if !embeddedSidebarBrowserRelevant(snapshot) {
		return nil
	}
	rows := []string{}
	if m.codexBrowserPolicyMismatch(snapshot) {
		rows = append(rows, detailWarningStyle.Render(fitLine("Browser settings changed", width)))
		rows = append(rows, embeddedSidebarMutedStyle.Render(fitLine("Use /reconnect", width)))
	}
	if request := snapshot.PendingElicitation; request != nil && request.Mode == codexapp.ElicitationModeURL {
		rows = append(rows, detailWarningStyle.Render(fitLine("Input requested", width)))
		if requestURL := strings.TrimSpace(request.URL); requestURL != "" {
			if detail {
				rows = append(rows, embeddedSidebarURLDetailRows("URL", requestURL, width)...)
			} else {
				rows = append(rows, embeddedSidebarURLRow("URL", requestURL, width))
			}
		}
	}
	activity := snapshot.BrowserActivity.Normalize()
	source := firstNonEmptyString(activity.SourceLabel(), "Playwright")
	switch activity.State {
	case browserctl.SessionActivityStateWaitingForUser:
		rows = append(rows, embeddedSidebarFieldRow("State", "waiting", detailWarningStyle, width))
		if detail {
			rows = append(rows, embeddedSidebarWrappedFieldRows("Source", source, embeddedSidebarMutedStyle, width, 0)...)
		} else {
			rows = append(rows, embeddedSidebarFieldRow("Source", source, embeddedSidebarMutedStyle, width))
		}
	case browserctl.SessionActivityStateActive:
		rows = append(rows, embeddedSidebarFieldRow("State", "active", detailValueStyle, width))
		if detail {
			rows = append(rows, embeddedSidebarWrappedFieldRows("Source", source, embeddedSidebarMutedStyle, width, 0)...)
		} else {
			rows = append(rows, embeddedSidebarFieldRow("Source", source, embeddedSidebarMutedStyle, width))
		}
	}
	if pageURL := managedBrowserCurrentPageURL(snapshot); pageURL != "" &&
		strings.TrimSpace(snapshot.ManagedBrowserSessionKey) != "" &&
		!snapshot.CurrentBrowserPageStale {
		if detail {
			rows = append(rows, embeddedSidebarURLDetailRows("Page", pageURL, width)...)
		} else {
			rows = append(rows, embeddedSidebarURLRow("Page", pageURL, width))
		}
		if m.managedBrowserCurrentPageHint(snapshot) != "" {
			rows = append(rows, embeddedSidebarActionRow("ctrl+o", "reveals browser", width))
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

func (m Model) renderEmbeddedSidebarMCPSection(snapshot codexapp.Snapshot, width int) []string {
	rows := embeddedSidebarMCPRows(snapshot, width, 3)
	if len(rows) == 0 {
		return nil
	}
	return append([]string{m.renderEmbeddedSidebarSectionHeader(embeddedCodexSidebarMCP, "Used MCPs", width)}, rows...)
}

func embeddedSidebarMCPRows(snapshot codexapp.Snapshot, width, limit int) []string {
	usage := filteredMCPUsage(snapshot.MCPUsage)
	if len(usage) == 0 || width <= 0 {
		return nil
	}
	if limit <= 0 || limit > len(usage) {
		limit = len(usage)
	}
	rows := make([]string, 0, limit+1)
	for _, item := range usage[:limit] {
		row := embeddedSidebarFieldRow(item.ServerName, embeddedSidebarMCPUsageSummary(item, true), detailValueStyle, width)
		if row != "" {
			rows = append(rows, row)
		}
	}
	if remaining := len(usage) - limit; remaining > 0 {
		rows = append(rows, embeddedSidebarMutedStyle.Render(fitLine(fmt.Sprintf("+%d more", remaining), width)))
	}
	return rows
}

func embeddedSidebarMCPDetailRows(snapshot codexapp.Snapshot, width int) []string {
	usage := filteredMCPUsage(snapshot.MCPUsage)
	if len(usage) == 0 || width <= 0 {
		return nil
	}
	rows := []string{}
	for _, item := range usage {
		rows = append(rows, embeddedSidebarWrappedFieldRows(item.ServerName, embeddedSidebarMCPUsageSummary(item, true), detailValueStyle, width, 0)...)
		for _, tool := range item.Tools {
			if strings.TrimSpace(tool.Name) == "" || tool.Calls <= 0 {
				continue
			}
			rows = append(rows, embeddedSidebarMCPToolRow(tool, width))
		}
	}
	return rows
}

func filteredMCPUsage(usage []codexapp.MCPUsageSnapshot) []codexapp.MCPUsageSnapshot {
	if len(usage) == 0 {
		return nil
	}
	filtered := make([]codexapp.MCPUsageSnapshot, 0, len(usage))
	for _, item := range usage {
		item.ServerName = strings.TrimSpace(item.ServerName)
		item.LastTool = strings.TrimSpace(item.LastTool)
		if item.ServerName == "" || item.ToolCalls <= 0 {
			continue
		}
		filtered = append(filtered, item)
	}
	return filtered
}

func embeddedSidebarMCPUsageSummary(usage codexapp.MCPUsageSnapshot, includeLast bool) string {
	parts := []string{embeddedSidebarCountLabel(usage.ToolCalls, "call")}
	if includeLast {
		if lastTool := strings.TrimSpace(usage.LastTool); lastTool != "" {
			parts = append(parts, "last "+lastTool)
		}
	}
	return strings.Join(parts, " | ")
}

func embeddedSidebarMCPToolRow(tool codexapp.MCPToolUsageSnapshot, width int) string {
	name := strings.TrimSpace(tool.Name)
	if name == "" || tool.Calls <= 0 || width <= 0 {
		return ""
	}
	text := "- " + name + " " + embeddedSidebarCountLabel(tool.Calls, "call")
	return embeddedSidebarMutedStyle.Render(fitLine(text, width))
}

func (m Model) renderEmbeddedSidebarSummarySection(snapshot codexapp.Snapshot, width int) []string {
	summary, style, ok := m.embeddedSidebarSummary(snapshot)
	if !ok {
		return nil
	}
	rows := embeddedSidebarPreviewRows(summary, style, width)
	if len(rows) == 0 {
		return nil
	}
	return append([]string{m.renderEmbeddedSidebarSectionHeader(embeddedCodexSidebarSummary, "Summary", width)}, rows...)
}

func (m Model) renderEmbeddedSidebarProcessSection(projectPath string, width int) []string {
	lines := []string{m.renderEmbeddedSidebarSectionHeader(embeddedCodexSidebarProcesses, "Active Processes", width)}
	rows := m.embeddedSidebarProcessRows(projectPath, width, 5)
	if len(rows) == 0 {
		lines = append(lines, embeddedSidebarMutedStyle.Render(fitLine("No active project processes", width)))
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
	for _, snapshot := range m.projectManagedRuntimeSnapshots(projectPath) {
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

func (m Model) embeddedSidebarProcessDetailRows(projectPath string, width, limit int) []string {
	projectPath = normalizeProjectPath(projectPath)
	if projectPath == "" {
		return nil
	}
	capacity := limit
	if capacity < 0 {
		capacity = 0
	}
	rows := make([]string, 0, capacity)
	for _, snapshot := range m.projectManagedRuntimeSnapshots(projectPath) {
		if limit > 0 && len(rows) >= limit {
			break
		}
		if !runtimeDetailAvailable("", snapshot) {
			continue
		}
		rows = append(rows, embeddedSidebarRuntimeDetailRows(snapshot, width)...)
	}
	for _, snapshot := range m.projectVisibleLocalInstanceSnapshots(projectPath) {
		if limit > 0 && len(rows) >= limit {
			break
		}
		rows = append(rows, embeddedSidebarLocalInstanceDetailRows(snapshot, width)...)
	}
	if report, ok := m.projectProcessReport(projectPath); ok {
		for _, finding := range report.Findings {
			if limit > 0 && len(rows) >= limit {
				break
			}
			rows = append(rows, embeddedSidebarFindingDetailRows(finding, width)...)
		}
	}
	if limit > 0 && len(rows) > limit {
		return rows[:limit]
	}
	return rows
}

func (m Model) embeddedSidebarRuntimeRow(snapshot projectrun.Snapshot, width int) string {
	status := "idle"
	style := embeddedSidebarMutedStyle
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
	return fitStyledWidth(style.Render(status)+" "+embeddedSidebarMutedStyle.Render(truncateText(label, max(1, width-5))), width)
}

func embeddedSidebarRuntimeDetailRows(snapshot projectrun.Snapshot, width int) []string {
	status := "idle"
	style := embeddedSidebarMutedStyle
	if snapshot.Running {
		status = "running"
		style = detailValueStyle
	} else if snapshot.ExitCodeKnown && snapshot.ExitCode != 0 {
		status = fmt.Sprintf("exited %d", snapshot.ExitCode)
		style = detailDangerStyle
	}
	rows := []string{style.Render(fitLine("Runtime "+status, width))}
	if command := strings.TrimSpace(snapshot.Command); command != "" {
		rows = append(rows, embeddedSidebarWrappedFieldRows("Command", command, embeddedSidebarMutedStyle, width, 0)...)
	} else if label := runtimeProcessLabel(snapshot); label != "" {
		rows = append(rows, embeddedSidebarWrappedFieldRows("Process", label, embeddedSidebarMutedStyle, width, 0)...)
	}
	if snapshot.PID > 0 {
		rows = append(rows, embeddedSidebarFieldRow("PID", fmt.Sprintf("%d", snapshot.PID), embeddedSidebarMutedStyle, width))
	}
	if url := runtimeURLSummary(snapshot); url != "" {
		rows = append(rows, embeddedSidebarWrappedFieldRows("URL", url, detailValueStyle, width, 0)...)
	} else if len(snapshot.Ports) > 0 {
		rows = append(rows, embeddedSidebarFieldRow("Ports", joinPorts(snapshot.Ports), detailValueStyle, width))
	}
	return rows
}

func embeddedSidebarLocalInstanceRow(snapshot projectrun.Snapshot, width int) string {
	label := localInstanceDisplayLabel(snapshot)
	if len(snapshot.AnnouncedURLs) > 0 {
		url := runtimeURLSummary(snapshot)
		label += " " + url
	} else if len(snapshot.Ports) > 0 {
		label += " :" + joinPorts(snapshot.Ports)
	}
	badge := "port"
	return fitStyledWidth(detailValueStyle.Render(badge)+" "+embeddedSidebarMutedStyle.Render(truncateText(label, max(1, width-len(badge)-1))), width)
}

func embeddedSidebarLocalInstanceDetailRows(snapshot projectrun.Snapshot, width int) []string {
	rows := []string{detailValueStyle.Render(fitLine("Local port", width))}
	if command := strings.TrimSpace(snapshot.Command); command != "" {
		rows = append(rows, embeddedSidebarWrappedFieldRows("Command", command, embeddedSidebarMutedStyle, width, 0)...)
	}
	if snapshot.PID > 0 {
		rows = append(rows, embeddedSidebarFieldRow("PID", fmt.Sprintf("%d", snapshot.PID), embeddedSidebarMutedStyle, width))
	}
	if url := runtimeURLSummary(snapshot); url != "" {
		rows = append(rows, embeddedSidebarWrappedFieldRows("URL", url, detailValueStyle, width, 0)...)
	} else if len(snapshot.Ports) > 0 {
		rows = append(rows, embeddedSidebarFieldRow("Ports", joinPorts(snapshot.Ports), detailValueStyle, width))
	}
	return rows
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
	badge := "proc"
	style := detailWarningStyle
	if finding.PortConflict {
		style = detailDangerStyle
		badge = "port!"
	} else if len(finding.Ports) > 0 {
		badge = "port!"
	}
	return fitStyledWidth(style.Render(badge)+" "+embeddedSidebarMutedStyle.Render(truncateText(label, max(1, width-len(badge)-1))), width)
}

func embeddedSidebarFindingDetailRows(finding procinspect.Finding, width int) []string {
	style := detailWarningStyle
	title := "Process"
	if finding.PortConflict {
		style = detailDangerStyle
		title = "Port conflict"
	}
	rows := []string{style.Render(fitLine(title, width))}
	if finding.PID > 0 {
		rows = append(rows, embeddedSidebarFieldRow("PID", fmt.Sprintf("%d", finding.PID), embeddedSidebarMutedStyle, width))
	}
	if name := cpuProcessName(finding.Process); name != "" {
		rows = append(rows, embeddedSidebarWrappedFieldRows("Name", name, embeddedSidebarMutedStyle, width, 0)...)
	}
	if command := strings.TrimSpace(finding.Process.Command); command != "" {
		rows = append(rows, embeddedSidebarWrappedFieldRows("Command", command, embeddedSidebarMutedStyle, width, 0)...)
	}
	if finding.CPU > 0 {
		rows = append(rows, embeddedSidebarFieldRow("CPU", formatCPUPercent(finding.CPU), detailWarningStyle, width))
	}
	if len(finding.Ports) > 0 {
		rows = append(rows, embeddedSidebarFieldRow("Ports", joinPorts(finding.Ports), detailWarningStyle, width))
	}
	for _, reason := range finding.Reasons {
		if reason = strings.TrimSpace(reason); reason != "" {
			rows = append(rows, embeddedSidebarWrappedFieldRows("Reason", reason, embeddedSidebarMutedStyle, width, 0)...)
		}
	}
	return rows
}

func (m Model) renderEmbeddedSidebarDiffSection(projectPath string, width int) []string {
	lines := []string{m.renderEmbeddedSidebarSectionHeader(embeddedCodexSidebarDiff, "Diff Summary", width)}
	state, ok := m.embeddedSidebarDiffState(projectPath)
	project, _ := m.embeddedSidebarProjectSummary(projectPath)
	switch {
	case ok && state.NoGit:
		lines = append(lines, embeddedSidebarMutedStyle.Render(fitLine("No git repository", width)))
		lines = append(lines, embeddedSidebarMutedStyle.Render(fitLine("r checks again", width)))
	case ok && strings.TrimSpace(state.Err) != "":
		lines = append(lines, detailDangerStyle.Render(fitLine("Diff unavailable", width)))
		lines = append(lines, embeddedSidebarMutedStyle.Render(fitLine(state.Err, width)))
	case ok && state.Loading && state.Preview == nil && !state.Clean:
		lines = append(lines, embeddedSidebarMutedStyle.Render(fitLine("Preparing diff summary...", width)))
	case ok && state.Preview != nil && len(state.Preview.Files) > 0:
		lines = append(lines, embeddedSidebarDiffSummaryRow(*state.Preview, width))
		if branch := strings.TrimSpace(state.Preview.Branch); branch != "" {
			lines = append(lines, embeddedSidebarMutedStyle.Render(fitLine(branch, width)))
		}
		for i, file := range state.Preview.Files {
			if i >= 3 {
				lines = append(lines, embeddedSidebarMutedStyle.Render(fitLine(fmt.Sprintf("+%d more", len(state.Preview.Files)-i), width)))
				break
			}
			lines = append(lines, embeddedSidebarDiffFileRow(file, width))
		}
		lines = append(lines, embeddedSidebarMutedStyle.Render(fitLine("Enter opens full diff", width)))
	case ok && state.Clean:
		label := "Clean worktree"
		if branch := strings.TrimSpace(state.Branch); branch != "" {
			label += " (" + branch + ")"
		}
		lines = append(lines, embeddedSidebarMutedStyle.Render(fitLine(label, width)))
	case project.RepoDirty || project.RepoConflict:
		lines = append(lines, detailWarningStyle.Render(fitLine(repoDirtyPlainLabel(project), width)))
		lines = append(lines, embeddedSidebarMutedStyle.Render(fitLine("Enter opens full diff", width)))
	case m.embeddedSidebarProjectKnownNonGit(projectPath):
		lines = append(lines, embeddedSidebarMutedStyle.Render(fitLine("No git repository", width)))
	default:
		lines = append(lines, embeddedSidebarMutedStyle.Render(fitLine("No diff cached yet", width)))
		lines = append(lines, embeddedSidebarMutedStyle.Render(fitLine("r refreshes", width)))
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
	return fitStyledWidth(code+" "+embeddedSidebarMutedStyle.Render(truncateText(summary, max(1, width-2))), width)
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
		return embeddedSidebarMutedStyle
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

func embeddedSidebarPreviewRows(text string, style lipgloss.Style, width int) []string {
	text = embeddedSidebarPreviewText(text)
	if text == "" {
		return nil
	}
	return embeddedSidebarWrappedRows(text, style, width)
}

func embeddedSidebarPreviewText(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	if text == "" {
		return ""
	}
	return fitFooterWidth(text, embeddedSidebarPreviewTextLimit)
}

func embeddedSidebarURLRow(label, rawURL string, width int) string {
	return embeddedSidebarFieldRow(label, embeddedSidebarCompactURL(rawURL), embeddedSidebarMutedStyle, width)
}

func embeddedSidebarURLDetailRows(label, rawURL string, width int) []string {
	return embeddedSidebarWrappedFieldRows(label, rawURL, embeddedSidebarMutedStyle, width, 0)
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
	return fitStyledWidth(embeddedSidebarMutedStyle.Render(prefix)+style.Render(truncateText(value, valueWidth)), width)
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
			rows = append(rows, fitStyledWidth(embeddedSidebarMutedStyle.Render(prefix)+rendered, width))
			continue
		}
		rows = append(rows, fitStyledWidth(embeddedSidebarMutedStyle.Render(continuation)+rendered, width))
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
