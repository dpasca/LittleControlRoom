package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"lcroom/internal/procinspect"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const processScanTimeout = 2 * time.Second

type processWarningStats struct {
	Total         int
	Projects      int
	HighCPU       int
	Orphaned      int
	PortListeners int
	MaxCPU        float64
}

type processDialogState struct {
	ProjectPath string
	ProjectName string
	Loading     bool
	Error       string
	Findings    []procinspect.Finding
	Selected    int
	ScannedAt   time.Time
}

func (m *Model) openProcessDialogForSelection() tea.Cmd {
	dialog := processDialogState{
		ProjectName: "All Projects",
		Loading:     true,
	}
	dialog.Findings, dialog.ScannedAt = m.globalProcessFindings()
	m.processDialog = &dialog
	m.showHelp = false
	m.err = nil
	m.status = "Analyzing project processes..."
	return m.requestProcessScanCmd("")
}

func (m Model) updateProcessDialogMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	dialog := m.processDialog
	if dialog == nil {
		return m, nil
	}
	switch msg.String() {
	case "esc", "q":
		m.processDialog = nil
		m.status = "Process inspector closed"
		return m, nil
	case "up", "k":
		if dialog.Selected > 0 {
			dialog.Selected--
		}
		return m, nil
	case "down", "j":
		if dialog.Selected < len(dialog.Findings)-1 {
			dialog.Selected++
		}
		return m, nil
	case "r":
		dialog.Loading = true
		dialog.Error = ""
		m.status = "Refreshing process analysis..."
		return m, m.requestProcessScanCmd(dialog.ProjectPath)
	case "enter":
		if len(dialog.Findings) == 0 {
			return m, nil
		}
		dialog.Selected = clampInt(dialog.Selected, 0, len(dialog.Findings)-1)
		finding := dialog.Findings[dialog.Selected]
		m.status = fmt.Sprintf("PID %d: %s", finding.PID, strings.Join(finding.Reasons, ", "))
		return m, nil
	case "ctrl+c":
		m.processDialog = nil
		return m.updateNormalMode(msg)
	}
	return m, nil
}

func (m Model) loadProcessReportsCmd(dialogProjectPath string) tea.Cmd {
	paths := m.processScanProjectPaths(dialogProjectPath)
	if len(paths) == 0 {
		return nil
	}
	managedPIDs, managedPGIDs := m.managedRuntimeProcessSets()
	now := m.currentTime()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), processScanTimeout)
		defer cancel()
		reports, err := procinspect.ScanProjects(ctx, procinspect.ScanOptions{
			ProjectPaths: paths,
			ManagedPIDs:  managedPIDs,
			ManagedPGIDs: managedPGIDs,
			OwnPID:       os.Getpid(),
			Now:          now,
		})
		return processScanMsg{
			reports:           reports,
			dialogProjectPath: dialogProjectPath,
			err:               err,
		}
	}
}

func (m *Model) requestProcessScanCmd(dialogProjectPath string) tea.Cmd {
	if len(m.processScanProjectPaths(dialogProjectPath)) == 0 {
		return nil
	}
	if m.processScanInFlight {
		m.processScanQueued = true
		if strings.TrimSpace(dialogProjectPath) != "" {
			m.processScanQueuedDialogPath = dialogProjectPath
		}
		return nil
	}
	m.processScanInFlight = true
	return m.loadProcessReportsCmd(dialogProjectPath)
}

func (m *Model) finishProcessScanCmd() tea.Cmd {
	if !m.processScanInFlight {
		return nil
	}
	if m.processScanQueued {
		dialogProjectPath := m.processScanQueuedDialogPath
		m.processScanQueued = false
		m.processScanQueuedDialogPath = ""
		return m.loadProcessReportsCmd(dialogProjectPath)
	}
	m.processScanInFlight = false
	return nil
}

func (m Model) processScanProjectPaths(dialogProjectPath string) []string {
	seen := map[string]struct{}{}
	paths := []string{}
	add := func(path string) {
		path = filepath.Clean(strings.TrimSpace(path))
		if path == "" || path == "." {
			return
		}
		if _, ok := seen[path]; ok {
			return
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
	}
	if strings.TrimSpace(dialogProjectPath) != "" {
		add(dialogProjectPath)
		return paths
	}
	projects := m.allProjects
	if len(projects) == 0 {
		projects = m.projects
	}
	for _, project := range projects {
		add(project.Path)
	}
	return paths
}

func (m Model) managedRuntimeProcessSets() (map[int]struct{}, map[int]struct{}) {
	pids := map[int]struct{}{}
	pgids := map[int]struct{}{}
	for _, snapshot := range m.runtimeSnapshots {
		if snapshot.PID > 0 {
			pids[snapshot.PID] = struct{}{}
		}
		if snapshot.PGID > 0 {
			pgids[snapshot.PGID] = struct{}{}
		}
	}
	return pids, pgids
}

func (m *Model) applyProcessScanMsg(msg processScanMsg) tea.Cmd {
	reloadCmd := m.finishProcessScanCmd()
	if m.processReports == nil {
		m.processReports = make(map[string]procinspect.ProjectReport)
	}
	if msg.err == nil {
		for _, report := range msg.reports {
			path := filepath.Clean(strings.TrimSpace(report.ProjectPath))
			if path == "" || path == "." {
				continue
			}
			m.processReports[path] = report
		}
	}

	if m.processDialog != nil && sameDialogProcessPath(m.processDialog.ProjectPath, msg.dialogProjectPath) {
		m.processDialog.Loading = false
		m.processDialog.Error = ""
		if msg.err != nil {
			m.processDialog.Error = msg.err.Error()
			m.status = "Process analysis failed"
		} else if strings.TrimSpace(m.processDialog.ProjectPath) == "" {
			m.processDialog.Findings, m.processDialog.ScannedAt = m.globalProcessFindings()
			m.processDialog.Selected = clampInt(m.processDialog.Selected, 0, max(0, len(m.processDialog.Findings)-1))
			m.status = processDialogReadyStatus(len(m.processDialog.Findings), "all projects")
		} else if report, ok := m.projectProcessReport(m.processDialog.ProjectPath); ok {
			m.processDialog.Findings = append([]procinspect.Finding(nil), report.Findings...)
			m.processDialog.ScannedAt = report.ScannedAt
			m.processDialog.Selected = clampInt(m.processDialog.Selected, 0, max(0, len(m.processDialog.Findings)-1))
			m.status = processDialogReadyStatus(len(m.processDialog.Findings), "project")
		}
	}
	stats := m.totalProcessWarningStats()
	if strings.TrimSpace(msg.dialogProjectPath) == "" && m.processDialog == nil && stats.Total > 0 && stats.Total != m.processWarningLastCount {
		m.status = processWarningStatus(stats)
	}
	if msg.err == nil && m.sortMode == sortByAttention {
		m.rebuildProjectList(m.currentSelectedProjectPath())
	}
	if m.bossMode {
		m.bossModel = m.bossModel.WithViewContext(m.bossViewContext())
	}
	m.processWarningLastCount = stats.Total
	return reloadCmd
}

func (m Model) renderProcessDialogOverlay(body string, bodyW, bodyH int) string {
	panel := m.renderProcessDialogPanel(bodyW, bodyH)
	panelW := lipgloss.Width(panel)
	panelH := lipgloss.Height(panel)
	left := max(0, (bodyW-panelW)/2)
	top := max(0, (bodyH-panelH)/4)
	return overlayBlock(body, panel, bodyW, bodyH, left, top)
}

func (m Model) renderProcessDialogPanel(bodyW, bodyH int) string {
	panelW := min(bodyW, min(max(62, bodyW-12), 104))
	panelInnerW := max(32, panelW-4)
	maxContentH := max(10, bodyH-6)
	return renderDialogPanel(panelW, panelInnerW, m.renderProcessDialogContent(panelInnerW, maxContentH))
}

func (m Model) renderProcessDialogContent(width, maxHeight int) string {
	dialog := m.processDialog
	if dialog == nil {
		return ""
	}
	lines := []string{
		renderDialogHeader("Process Inspector", dialog.ProjectName, "", width),
	}
	if strings.TrimSpace(dialog.ProjectPath) != "" {
		lines = append(lines, detailField("Path", detailMutedStyle.Render(truncateText(m.displayPathWithHomeTilde(dialog.ProjectPath), max(20, width-6)))))
	} else {
		lines = append(lines, detailField("Scope", detailMutedStyle.Render("all tracked projects")))
	}
	if !dialog.ScannedAt.IsZero() {
		lines = append(lines, detailField("Scanned", detailMutedStyle.Render(dialog.ScannedAt.Format(timeFieldFormat))))
	}
	if dialog.Loading {
		lines = append(lines, "", commandPaletteHintStyle.Render("Scanning project-local processes..."))
	} else if strings.TrimSpace(dialog.Error) != "" {
		lines = append(lines, "", detailDangerStyle.Render("Process scan failed"))
		lines = append(lines, renderWrappedDialogTextLines(detailDangerStyle, width, dialog.Error)...)
	} else if len(dialog.Findings) == 0 {
		lines = append(lines, "", detailValueStyle.Render("No suspicious project-local processes found."))
	} else {
		lines = append(lines, "", detailWarningStyle.Render(processDialogCountLabel(len(dialog.Findings))))
		lines = append(lines, m.renderProcessFindingRows(width, max(2, maxHeight-len(lines)-10))...)
		if dialog.Selected >= 0 && dialog.Selected < len(dialog.Findings) {
			lines = append(lines, "")
			lines = append(lines, m.renderProcessFindingDetail(width, dialog.Findings[dialog.Selected])...)
		}
	}
	lines = append(lines, "",
		renderDialogAction("r", "refresh", navigateActionKeyStyle, navigateActionTextStyle),
		renderDialogAction("Esc", "close", cancelActionKeyStyle, cancelActionTextStyle),
	)
	return strings.Join(limitLines(lines, maxHeight), "\n")
}

func (m Model) renderProcessFindingRows(width, limit int) []string {
	dialog := m.processDialog
	if dialog == nil || len(dialog.Findings) == 0 || limit <= 0 {
		return nil
	}
	if dialog.Selected < 0 {
		dialog.Selected = 0
	}
	if dialog.Selected >= len(dialog.Findings) {
		dialog.Selected = len(dialog.Findings) - 1
	}
	start := 0
	if dialog.Selected >= limit {
		start = dialog.Selected - limit + 1
	}
	end := min(len(dialog.Findings), start+limit)
	rows := []string{}
	if start > 0 {
		rows = append(rows, commandPaletteHintStyle.Render(fmt.Sprintf("↑ %d more", start)))
	}
	for i := start; i < end; i++ {
		finding := dialog.Findings[i]
		marker := " "
		style := commandPaletteRowStyle
		if i == dialog.Selected {
			marker = "›"
			style = commandPaletteSelectStyle
		}
		reason := strings.Join(finding.Reasons, ", ")
		ports := ""
		if len(finding.Ports) > 0 {
			ports = " ports " + joinPorts(finding.Ports)
		}
		project := truncateText(m.runtimeOwnerLabel(finding.ProjectPath), 18)
		row := fmt.Sprintf("%s PID %-6d CPU %5.1f%%  %-18s  %s%s", marker, finding.PID, finding.CPU, project, reason, ports)
		rows = append(rows, style.Width(width).Render(truncateText(row, width)))
	}
	if end < len(dialog.Findings) {
		rows = append(rows, commandPaletteHintStyle.Render(fmt.Sprintf("↓ %d more", len(dialog.Findings)-end)))
	}
	return rows
}

func (m Model) renderProcessFindingDetail(width int, finding procinspect.Finding) []string {
	lines := []string{commandPaletteTitleStyle.Render("Selected PID")}
	fields := []string{
		detailField("PID", detailValueStyle.Render(strconv.Itoa(finding.PID))),
		detailField("Project", detailValueStyle.Render(m.runtimeOwnerLabel(finding.ProjectPath))),
		detailField("Parent", detailValueStyle.Render(strconv.Itoa(finding.PPID))),
		detailField("Group", detailValueStyle.Render(strconv.Itoa(finding.PGID))),
		detailField("State", detailValueStyle.Render(strings.TrimSpace(finding.Stat))),
		detailField("CPU", detailWarningStyle.Render(fmt.Sprintf("%.1f%%", finding.CPU))),
		detailField("Mem", detailValueStyle.Render(fmt.Sprintf("%.1f%%", finding.Mem))),
	}
	if strings.TrimSpace(finding.Elapsed) != "" {
		fields = append(fields, detailField("Age", detailValueStyle.Render(finding.Elapsed)))
	}
	if len(finding.Ports) > 0 {
		fields = append(fields, detailField("Ports", detailValueStyle.Render(joinPorts(finding.Ports))))
	}
	lines = appendDetailFields(lines, width, fields...)
	if strings.TrimSpace(finding.CWD) != "" {
		lines = append(lines, renderWrappedDetailField("CWD", detailMutedStyle, width, m.displayPathWithHomeTilde(finding.CWD)))
	}
	if strings.TrimSpace(finding.Command) != "" {
		lines = append(lines, renderWrappedDetailField("Cmd", detailMutedStyle, width, finding.Command))
	}
	lines = append(lines, renderWrappedDialogTextLines(commandPaletteHintStyle, width, fmt.Sprintf("LCR will not kill this automatically. If it is stale, stop PID %d from your shell.", finding.PID))...)
	return lines
}

func (m Model) globalProcessFindings() ([]procinspect.Finding, time.Time) {
	if len(m.processReports) == 0 {
		return nil, time.Time{}
	}
	paths := m.processScanProjectPaths("")
	findings := []procinspect.Finding{}
	scannedAt := time.Time{}
	for _, path := range paths {
		report, ok := m.projectProcessReport(path)
		if !ok {
			continue
		}
		if report.ScannedAt.After(scannedAt) {
			scannedAt = report.ScannedAt
		}
		findings = append(findings, report.Findings...)
	}
	sortProcessFindings(findings)
	return findings, scannedAt
}

func sortProcessFindings(findings []procinspect.Finding) {
	sort.SliceStable(findings, func(i, j int) bool {
		left, right := findings[i], findings[j]
		if left.CPU != right.CPU {
			return left.CPU > right.CPU
		}
		if left.ProjectPath != right.ProjectPath {
			return strings.ToLower(left.ProjectPath) < strings.ToLower(right.ProjectPath)
		}
		return left.PID < right.PID
	})
}

func (m Model) projectProcessReport(projectPath string) (procinspect.ProjectReport, bool) {
	projectPath = filepath.Clean(strings.TrimSpace(projectPath))
	if projectPath == "" || projectPath == "." || m.processReports == nil {
		return procinspect.ProjectReport{}, false
	}
	report, ok := m.processReports[projectPath]
	return report, ok
}

func (m Model) projectProcessWarningCount(projectPath string) int {
	report, ok := m.projectProcessReport(projectPath)
	if !ok {
		return 0
	}
	return len(report.Findings)
}

func (m Model) projectProcessWarningStats(projectPath string) processWarningStats {
	report, ok := m.projectProcessReport(projectPath)
	if !ok {
		return processWarningStats{}
	}
	stats := processWarningStatsForFindings(report.Findings)
	if stats.Total > 0 {
		stats.Projects = 1
	}
	return stats
}

func (m Model) totalProcessWarningCount() int {
	return m.totalProcessWarningStats().Total
}

func (m Model) totalProcessWarningStats() processWarningStats {
	if len(m.processReports) == 0 {
		return processWarningStats{}
	}
	paths := m.processNoticeProjectPaths()
	return m.processWarningStatsForPaths(paths)
}

func (m Model) processWarningStatsForPaths(paths []string) processWarningStats {
	stats := processWarningStats{}
	for _, path := range paths {
		projectStats := m.projectProcessWarningStats(path)
		stats.Total += projectStats.Total
		stats.HighCPU += projectStats.HighCPU
		stats.Orphaned += projectStats.Orphaned
		stats.PortListeners += projectStats.PortListeners
		if projectStats.Total > 0 {
			stats.Projects++
		}
		if projectStats.MaxCPU > stats.MaxCPU {
			stats.MaxCPU = projectStats.MaxCPU
		}
	}
	return stats
}

func processWarningStatsForFindings(findings []procinspect.Finding) processWarningStats {
	stats := processWarningStats{Total: len(findings)}
	for _, finding := range findings {
		if processFindingIsHighCPU(finding) {
			stats.HighCPU++
		}
		if processFindingIsOrphaned(finding) {
			stats.Orphaned++
		}
		if processFindingHasPorts(finding) {
			stats.PortListeners++
		}
		if finding.CPU > stats.MaxCPU {
			stats.MaxCPU = finding.CPU
		}
	}
	return stats
}

func processFindingIsHighCPU(finding procinspect.Finding) bool {
	return finding.CPU >= procinspect.DefaultHighCPUThreshold
}

func processFindingIsOrphaned(finding procinspect.Finding) bool {
	return finding.PPID == 1
}

func processFindingHasPorts(finding procinspect.Finding) bool {
	return len(finding.Ports) > 0
}

func (m Model) processNoticeProjectPaths() []string {
	projects := m.allProjects
	if len(projects) == 0 {
		projects = m.projects
	}
	if m.privacyMode {
		projects = filterProjectsByPrivacy(projects, m.privacyPatterns)
	}
	seen := map[string]struct{}{}
	paths := make([]string, 0, len(projects))
	for _, project := range projects {
		path := filepath.Clean(strings.TrimSpace(project.Path))
		if path == "" || path == "." {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
	}
	return paths
}

func (m Model) projectProcessWarningSummary(projectPath string) string {
	stats := m.projectProcessWarningStats(projectPath)
	if stats.Total == 0 {
		return ""
	}
	return fmt.Sprintf("%s outside LCR control. Use /cpu to inspect.", processWarningDetailLabel(stats))
}

func processWarningDetailLabel(stats processWarningStats) string {
	subject := "suspicious project-local PID"
	if stats.Total != 1 {
		subject = "suspicious project-local PIDs"
	}
	parts := []string{fmt.Sprintf("%d %s", stats.Total, subject)}
	qualifiers := processWarningQualifiers(stats)
	if len(qualifiers) > 0 {
		parts = append(parts, "("+strings.Join(qualifiers, ", ")+")")
	}
	return strings.Join(parts, " ")
}

func processWarningQualifiers(stats processWarningStats) []string {
	qualifiers := []string{}
	if stats.HighCPU > 0 {
		qualifiers = append(qualifiers, formatCount(stats.HighCPU, "hot CPU"))
	}
	if stats.Orphaned > 0 {
		qualifiers = append(qualifiers, formatCount(stats.Orphaned, "orphaned"))
	}
	if stats.PortListeners > 0 {
		qualifiers = append(qualifiers, formatCount(stats.PortListeners, "port listener"))
	}
	return qualifiers
}

func processWarningFooterLabel(stats processWarningStats) string {
	if stats.Total == 0 {
		return ""
	}
	parts := []string{fmt.Sprintf("PIDs %d", stats.Total)}
	if stats.HighCPU > 0 {
		parts = append(parts, fmt.Sprintf("hot%d", stats.HighCPU))
	}
	if stats.PortListeners > 0 {
		parts = append(parts, fmt.Sprintf("port%d", stats.PortListeners))
	}
	return strings.Join(parts, " ")
}

func processWarningSystemNoticeSummary(stats processWarningStats) string {
	if stats.Total == 0 {
		return ""
	}
	return processWarningDetailLabel(stats) + " outside LCR control; use process_report or /cpu for PID details"
}

func formatCount(count int, label string) string {
	if count == 1 {
		return "1 " + label
	}
	if strings.HasSuffix(label, "CPU") || strings.HasSuffix(label, "ed") {
		return fmt.Sprintf("%d %s", count, label)
	}
	return fmt.Sprintf("%d %ss", count, label)
}

func (m Model) renderFooterProcessWarningSegment() string {
	label := processWarningFooterLabel(m.totalProcessWarningStats())
	if label == "" {
		return ""
	}
	return renderFooterAlert(label)
}

func (m Model) projectProcessRunFlag(projectPath string) string {
	stats := m.projectProcessWarningStats(projectPath)
	if stats.Total == 0 {
		return ""
	}
	prefix := "PID!"
	if stats.HighCPU > 0 {
		prefix = "HOT!"
	}
	if stats.Total == 1 {
		return prefix
	}
	return fmt.Sprintf("%s%d", prefix, stats.Total)
}

func processDialogCountLabel(count int) string {
	if count == 1 {
		return "1 suspicious project-local process"
	}
	return fmt.Sprintf("%d suspicious project-local processes", count)
}

func processDialogReadyStatus(count int, scope string) string {
	scope = strings.TrimSpace(scope)
	if scope == "" {
		scope = "project"
	}
	switch count {
	case 0:
		return "No suspicious project processes found"
	case 1:
		return "Found 1 suspicious process across " + scope
	default:
		return fmt.Sprintf("Found %d suspicious processes across %s", count, scope)
	}
}

func processWarningStatus(stats processWarningStats) string {
	label := processWarningFooterLabel(stats)
	if label == "" {
		return ""
	}
	return label + "; /cpu"
}

func sameDialogProcessPath(left, right string) bool {
	if strings.TrimSpace(left) == "" && strings.TrimSpace(right) == "" {
		return true
	}
	left = filepath.Clean(strings.TrimSpace(left))
	right = filepath.Clean(strings.TrimSpace(right))
	return left != "" && left != "." && left == right
}

func clampInt(value, low, high int) int {
	if high < low {
		return low
	}
	if value < low {
		return low
	}
	if value > high {
		return high
	}
	return value
}

func limitLines(lines []string, limit int) []string {
	if limit <= 0 || len(lines) <= limit {
		return lines
	}
	if limit <= 1 {
		return lines[:1]
	}
	out := append([]string(nil), lines[:limit-1]...)
	out = append(out, commandPaletteHintStyle.Render("..."))
	return out
}
