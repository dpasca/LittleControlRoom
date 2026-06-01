package tui

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"lcroom/internal/codexapp"
	"lcroom/internal/model"
	"lcroom/internal/procinspect"
	"lcroom/internal/projectrun"
	"lcroom/internal/service"

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

const embeddedCodexSidebarSectionCount = 2

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
	projectPath := strings.TrimSpace(firstNonEmptyString(snapshot.ProjectPath, m.codexVisibleProject))
	if projectPath == "" {
		projectPath = strings.TrimSpace(m.codexHiddenProject)
	}
	switch msg.String() {
	case "f6":
		cmd := m.focusEmbeddedCodexMain("")
		return m, cmd
	case "shift+f6":
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

func (m Model) returnFromDiffToEmbeddedCodex() (tea.Model, tea.Cmd) {
	if m.diffView == nil || strings.TrimSpace(m.diffView.returnToCodexProject) == "" {
		return m.closeDiffView("Diff view closed")
	}
	projectPath := strings.TrimSpace(m.diffView.returnToCodexProject)
	m.clearPendingGitSummary(m.diffView.ProjectPath)
	m.diffView = nil
	updated, cmd := m.showCodexProject(projectPath, "Back to engineer session")
	m = normalizeUpdateModel(updated)
	if strings.TrimSpace(m.codexInput.Value()) == "" {
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
		preview, err := m.svc.PrepareDiff(m.ctx, projectPath)
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
	projectPath := strings.TrimSpace(firstNonEmptyString(snapshot.ProjectPath, m.codexVisibleProject, m.codexHiddenProject))
	contentWidth := max(12, width-2)
	lines := []string{
		m.renderEmbeddedSidebarTitle(contentWidth),
	}
	lines = append(lines, m.renderEmbeddedSidebarProcessSection(projectPath, contentWidth)...)
	lines = append(lines, "")
	lines = append(lines, m.renderEmbeddedSidebarDiffSection(projectPath, contentWidth)...)
	return lipgloss.NewStyle().PaddingLeft(1).Render(fitPaneContent(strings.Join(lines, "\n"), contentWidth, height))
}

func (m Model) renderEmbeddedSidebarTitle(width int) string {
	title := detailSectionStyle.Render(fitLine("Session Sidebar", width))
	if m.codexPanelFocus == embeddedCodexFocusSidebar {
		return title
	}
	return detailMutedStyle.Render(fitLine("Session Sidebar", width))
}

func (m Model) renderEmbeddedSidebarSectionHeader(section embeddedCodexSidebarSection, title string, width int) string {
	selected := m.codexSidebarSelected == section && m.codexPanelFocus == embeddedCodexFocusSidebar
	marker := " "
	if selected {
		marker = ">"
	}
	text := marker + " " + title
	if selected {
		return commandPaletteSelectStyle.Width(width).Render(truncateText(text, max(1, width)))
	}
	return detailSectionStyle.Render(fitLine(text, width))
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
	case ok && state.Loading:
		lines = append(lines, detailMutedStyle.Render(fitLine("Preparing diff summary...", width)))
	case ok && strings.TrimSpace(state.Err) != "":
		lines = append(lines, detailDangerStyle.Render(fitLine("Diff unavailable", width)))
		lines = append(lines, detailMutedStyle.Render(fitLine(state.Err, width)))
	case ok && state.Preview != nil && len(state.Preview.Files) > 0:
		lines = append(lines, detailValueStyle.Render(fitLine(state.Preview.Summary, width)))
		if branch := strings.TrimSpace(state.Preview.Branch); branch != "" {
			lines = append(lines, detailMutedStyle.Render(fitLine(branch, width)))
		}
		for i, file := range state.Preview.Files {
			if i >= 3 {
				lines = append(lines, detailMutedStyle.Render(fitLine(fmt.Sprintf("+%d more", len(state.Preview.Files)-i), width)))
				break
			}
			lines = append(lines, detailMutedStyle.Render(fitLine(diffFileKindCode(file)+" "+file.Summary, width)))
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

func fitLine(text string, width int) string {
	if width <= 0 {
		return ""
	}
	return ansi.Truncate(strings.TrimRight(text, "\r\n"), width, "")
}
