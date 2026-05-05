package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"lcroom/internal/codexapp"
	"lcroom/internal/model"
	"lcroom/internal/procinspect"
	"lcroom/internal/service"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const cpuSnapshotTimeout = 1500 * time.Millisecond
const cpuRemediationProcessLimit = 12
const cpuDialogVisibleProcessRows = 7
const hotSystemCPUCapacityPercent = 75.0
const cpuRemediationPromptCharLimit = 40000
const cpuRemediationTaskTitle = "Investigate and reduce CPU usage"

type cpuDialogState struct {
	Loading      bool
	Error        string
	Processes    []procinspect.CPUProcess
	Selected     int
	ScannedAt    time.Time
	TotalCPU     float64
	ProcessCount int
	LogicalCPUs  int
}

type cpuRemediationEditorState struct {
	Input      textarea.Model
	Processes  []procinspect.CPUProcess
	Submitting bool
}

type cpuRemediationTaskCreatedMsg struct {
	result service.CreateScratchTaskResult
	prompt string
	err    error
}

func (m *Model) openCPUDialog() tea.Cmd {
	dialog := cpuDialogState{
		Loading: true,
	}
	if !m.cpuSnapshot.ScannedAt.IsZero() {
		dialog.Loading = false
		dialog.Processes = m.cpuDialogProcesses(m.cpuSnapshot)
		dialog.ScannedAt = m.cpuSnapshot.ScannedAt
		dialog.TotalCPU = m.cpuSnapshot.TotalCPU
		dialog.ProcessCount = m.cpuSnapshot.ProcessCount
		dialog.LogicalCPUs = m.cpuSnapshot.LogicalCPUs
	}
	m.cpuDialog = &dialog
	m.showHelp = false
	m.err = nil
	m.status = "Inspecting CPU usage..."
	return m.requestCPUSnapshotRefreshCmd()
}

func (m *Model) syncCPURemediationEditorSize() {
	if m.cpuRemediationEditor != nil {
		_, panelInnerW, editorHeight := cpuRemediationEditorPanelLayout(m.width, m.height)
		m.cpuRemediationEditor.Input.SetWidth(max(24, panelInnerW))
		m.cpuRemediationEditor.Input.SetHeight(editorHeight)
	}
}

func newCPURemediationPromptInput(value string) textarea.Model {
	input := textarea.New()
	input.Prompt = ""
	input.Placeholder = "Ask the engineer to inspect the hot CPU processes"
	input.CharLimit = cpuRemediationPromptCharLimit
	input.ShowLineNumbers = false
	styleDialogTextarea(&input)
	input.SetWidth(84)
	input.SetHeight(16)
	input.SetValue(value)
	return input
}

func (m Model) updateCPUDialogMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	dialog := m.cpuDialog
	if dialog == nil {
		return m, nil
	}
	switch msg.String() {
	case "esc", "q":
		m.cpuDialog = nil
		m.status = "CPU inspector closed"
		return m, nil
	case "up", "k":
		if dialog.Selected > 0 {
			dialog.Selected--
		}
		return m, nil
	case "down", "j":
		if dialog.Selected < len(dialog.Processes)-1 {
			dialog.Selected++
		}
		return m, nil
	case "r":
		dialog.Loading = true
		dialog.Error = ""
		m.status = "Refreshing CPU usage..."
		return m, m.requestCPUSnapshotRefreshCmd()
	case "a":
		return m.openCPURemediationEditor()
	case "enter":
		if len(dialog.Processes) == 0 {
			return m, nil
		}
		dialog.Selected = clampInt(dialog.Selected, 0, len(dialog.Processes)-1)
		process := dialog.Processes[dialog.Selected]
		m.status = fmt.Sprintf("PID %d: CPU %s %s", process.PID, formatCPUPercent(process.CPU), cpuProcessName(process.Process))
		return m, nil
	case "ctrl+c":
		m.cpuDialog = nil
		return m.updateNormalMode(msg)
	}
	return m, nil
}

func (m Model) updateCPURemediationEditorMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	editor := m.cpuRemediationEditor
	if editor == nil {
		return m, nil
	}
	if editor.Submitting {
		return m, nil
	}
	switch msg.String() {
	case "esc":
		m.closeCPURemediationEditor("CPU engineer prompt canceled")
		return m, nil
	case "ctrl+s":
		prompt := normalizeCPURemediationPrompt(editor.Input.Value())
		if strings.TrimSpace(prompt) == "" {
			m.status = "CPU engineer prompt is required"
			return m, nil
		}
		processes := append([]procinspect.CPUProcess(nil), editor.Processes...)
		editor.Submitting = true
		m.closeCPURemediationEditor("")
		return m.startCPURemediationTaskWithPrompt(processes, prompt)
	}
	var cmd tea.Cmd
	editor.Input, cmd = editor.Input.Update(msg)
	return m, cmd
}

func normalizeCPURemediationPrompt(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	return text
}

func (m Model) loadCPUSnapshotCmd() tea.Cmd {
	managedPIDs, managedPGIDs := m.managedRuntimeProcessSets()
	paths := m.cpuMonitorProjectPaths()
	now := m.currentTime()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), cpuSnapshotTimeout)
		defer cancel()
		snapshot, err := procinspect.ScanCPU(ctx, procinspect.CPUScanOptions{
			ProjectPaths:       paths,
			ManagedPIDs:        managedPIDs,
			ManagedPGIDs:       managedPGIDs,
			OwnPID:             os.Getpid(),
			Limit:              18,
			CWDLimit:           30,
			HighCPUThreshold:   procinspect.DefaultHighCPUThreshold,
			OrphanCPUThreshold: procinspect.DefaultOrphanCPUThreshold,
			Now:                now,
		})
		return cpuSnapshotMsg{snapshot: snapshot, err: err}
	}
}

func (m *Model) requestCPUSnapshotRefreshCmd() tea.Cmd {
	if m.cpuMonitorInFlight {
		m.cpuMonitorQueued = true
		return nil
	}
	m.cpuMonitorInFlight = true
	return m.loadCPUSnapshotCmd()
}

func (m *Model) finishCPUSnapshotRefreshCmd() tea.Cmd {
	if !m.cpuMonitorInFlight {
		return nil
	}
	if m.cpuMonitorQueued {
		m.cpuMonitorQueued = false
		return m.loadCPUSnapshotCmd()
	}
	m.cpuMonitorInFlight = false
	return nil
}

func (m *Model) applyCPUSnapshotMsg(msg cpuSnapshotMsg) tea.Cmd {
	reloadCmd := m.finishCPUSnapshotRefreshCmd()
	if msg.err == nil {
		m.cpuSnapshot = msg.snapshot
		if m.cpuDialog != nil {
			m.cpuDialog.Loading = false
			m.cpuDialog.Error = ""
			m.cpuDialog.Processes = m.cpuDialogProcesses(msg.snapshot)
			m.cpuDialog.ScannedAt = msg.snapshot.ScannedAt
			m.cpuDialog.TotalCPU = msg.snapshot.TotalCPU
			m.cpuDialog.ProcessCount = msg.snapshot.ProcessCount
			m.cpuDialog.LogicalCPUs = msg.snapshot.LogicalCPUs
			m.cpuDialog.Selected = clampInt(m.cpuDialog.Selected, 0, max(0, len(m.cpuDialog.Processes)-1))
			m.status = cpuDialogReadyStatus(msg.snapshot)
		}
		if m.bossMode {
			m.bossModel = m.bossModel.WithViewContext(m.bossViewContext())
		}
		return reloadCmd
	}
	if m.cpuDialog != nil {
		m.cpuDialog.Loading = false
		m.cpuDialog.Error = msg.err.Error()
		m.status = "CPU usage scan failed"
	}
	return reloadCmd
}

func (m Model) cpuMonitorProjectPaths() []string {
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

func (m Model) cpuDialogProcesses(snapshot procinspect.CPUSnapshot) []procinspect.CPUProcess {
	processes := append([]procinspect.CPUProcess(nil), snapshot.Processes...)
	seen := map[int]struct{}{}
	for _, process := range processes {
		if process.PID > 0 {
			seen[process.PID] = struct{}{}
		}
	}
	findings, _ := m.globalProcessFindings()
	for _, finding := range findings {
		if finding.PID <= 0 {
			continue
		}
		if _, ok := seen[finding.PID]; ok {
			continue
		}
		seen[finding.PID] = struct{}{}
		processes = append(processes, procinspect.CPUProcess{
			Process:           finding.Process,
			ProjectPath:       finding.ProjectPath,
			Reasons:           append([]string(nil), finding.Reasons...),
			ManagedRuntime:    finding.ManagedRuntime,
			OwnedByCurrentApp: finding.OwnedByCurrentApp,
		})
	}
	return processes
}

func (m Model) openCPURemediationEditor() (tea.Model, tea.Cmd) {
	processes := m.cpuRemediationProcesses()
	if len(processes) == 0 {
		m.status = "No CPU snapshot to hand to an engineer yet"
		return m, nil
	}
	prompt := m.cpuRemediationPrompt(processes)
	m.cpuRemediationEditor = &cpuRemediationEditorState{
		Input:     newCPURemediationPromptInput(prompt),
		Processes: append([]procinspect.CPUProcess(nil), processes...),
	}
	m.status = "Review CPU engineer prompt"
	return m, m.cpuRemediationEditor.Input.Focus()
}

func (m *Model) closeCPURemediationEditor(status string) {
	if m.cpuRemediationEditor != nil {
		m.cpuRemediationEditor.Input.Blur()
	}
	m.cpuRemediationEditor = nil
	if status != "" {
		m.status = status
	}
}

func (m Model) startCPURemediationTask() (tea.Model, tea.Cmd) {
	processes := m.cpuRemediationProcesses()
	if len(processes) == 0 {
		m.status = "No CPU processes to hand to an engineer"
		return m, nil
	}
	return m.startCPURemediationTaskWithPrompt(processes, m.cpuRemediationPrompt(processes))
}

func (m Model) startCPURemediationTaskWithPrompt(processes []procinspect.CPUProcess, prompt string) (tea.Model, tea.Cmd) {
	if len(processes) == 0 {
		m.status = "No CPU processes to hand to an engineer"
		return m, nil
	}
	if strings.TrimSpace(prompt) == "" {
		m.status = "CPU engineer prompt is required"
		return m, nil
	}
	m.status = "Creating CPU task..."
	return m, m.createCPURemediationTaskCmd(prompt)
}

func (m Model) createCPURemediationTaskCmd(prompt string) tea.Cmd {
	if m.svc == nil {
		return func() tea.Msg {
			return cpuRemediationTaskCreatedMsg{prompt: prompt, err: fmt.Errorf("service unavailable")}
		}
	}
	return func() tea.Msg {
		result, err := m.svc.CreateScratchTask(m.ctx, service.CreateScratchTaskRequest{Title: cpuRemediationTaskTitle})
		return cpuRemediationTaskCreatedMsg{result: result, prompt: prompt, err: err}
	}
}

func (m Model) applyCPURemediationTaskCreatedMsg(msg cpuRemediationTaskCreatedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.reportError("CPU task setup failed", msg.err, "")
		return m, nil
	}
	if strings.TrimSpace(msg.result.TaskPath) == "" {
		m.reportError("CPU task setup failed", fmt.Errorf("task path is empty"), "")
		return m, nil
	}
	project := cpuRemediationTaskProjectSummary(msg.result, m.currentTime())
	if strings.TrimSpace(project.Path) == "" {
		m.reportError("CPU task setup failed", fmt.Errorf("task path is empty"), "")
		return m, nil
	}
	m.err = nil
	m.preferredSelectPath = project.Path
	m.upsertProjectSummary(project)
	m.rebuildProjectList(project.Path)

	updated, launchCmd := m.launchEmbeddedForProjectWithOptions(project, codexapp.ProviderCodex, embeddedLaunchOptions{
		forceNew: true,
		prompt:   cpuRemediationTaskLaunchPrompt(project.Name, msg.prompt),
		reveal:   false,
	})
	m = normalizeUpdateModel(updated)
	refreshCmd := m.requestProjectInvalidationCmd(invalidateProjectStructure(project.Path))
	if launchCmd == nil {
		return m, refreshCmd
	}
	return m, batchCmds(refreshCmd, launchCmd)
}

func cpuRemediationTaskProjectSummary(result service.CreateScratchTaskResult, now time.Time) model.ProjectSummary {
	path := strings.TrimSpace(result.TaskPath)
	if path != "" {
		path = filepath.Clean(path)
	}
	name := strings.TrimSpace(result.TaskName)
	if name == "" {
		name = strings.TrimSpace(filepath.Base(path))
	}
	if name == "" || name == "." {
		name = cpuRemediationTaskTitle
	}
	return model.ProjectSummary{
		Path:                            path,
		Name:                            name,
		Kind:                            model.ProjectKindScratchTask,
		LastActivity:                    now,
		Status:                          model.StatusActive,
		AttentionScore:                  75,
		PresentOnDisk:                   true,
		ManuallyAdded:                   true,
		LatestSessionClassification:     model.ClassificationPending,
		LatestSessionClassificationType: model.SessionCategoryInProgress,
		LatestSessionSummary:            "CPU investigation task starting",
		CreatedAt:                       now,
	}
}

func cpuRemediationTaskLaunchPrompt(title, prompt string) string {
	lines := []string{
		"Little Control Room task:",
		"Title: " + strings.TrimSpace(title),
		"Allowed capabilities: process.inspect, process.terminate",
		"",
		"User request:",
		strings.TrimSpace(prompt),
	}
	return strings.Join(lines, "\n")
}

func (m Model) cpuRemediationProcesses() []procinspect.CPUProcess {
	dialog := m.cpuDialog
	if dialog == nil || len(dialog.Processes) == 0 {
		return nil
	}
	out := make([]procinspect.CPUProcess, 0, min(cpuRemediationProcessLimit, len(dialog.Processes)))
	seen := map[int]struct{}{}
	add := func(process procinspect.CPUProcess) {
		if len(out) >= cpuRemediationProcessLimit || process.PID <= 0 {
			return
		}
		if _, ok := seen[process.PID]; ok {
			return
		}
		seen[process.PID] = struct{}{}
		out = append(out, process)
	}
	if dialog.Selected >= 0 && dialog.Selected < len(dialog.Processes) {
		add(dialog.Processes[dialog.Selected])
	}
	for _, process := range dialog.Processes {
		add(process)
	}
	return out
}

func (m Model) cpuRemediationPrompt(processes []procinspect.CPUProcess) string {
	dialog := m.cpuDialog
	totalCPU := 0.0
	logicalCPUs := 0
	scannedAt := ""
	if dialog != nil {
		totalCPU = dialog.TotalCPU
		logicalCPUs = dialog.LogicalCPUs
		if !dialog.ScannedAt.IsZero() {
			scannedAt = dialog.ScannedAt.Format(timeFieldFormat)
		}
	}

	lines := []string{
		"Investigate the current high CPU usage and reduce it where it is safe to do so.",
	}
	if scannedAt != "" {
		lines = append(lines, "Snapshot scanned at: "+scannedAt)
	}
	lines = append(lines,
		"Total CPU: "+formatSystemCPUPercent(totalCPU, logicalCPUs),
		"Listed CPU: "+formatSystemCPUPercent(sumCPUProcesses(processes), logicalCPUs),
		"Note: per-process CPU values are raw ps percentages, so totals can exceed 100% on multicore machines.",
		"",
		"Current CPU processes:",
	)
	for _, process := range processes {
		lines = append(lines, "- "+m.cpuRemediationProcessLine(process))
	}
	lines = append(lines,
		"",
		"Goal:",
		"- Identify which processes are expected system or foreground user processes and which look stale, orphaned, runaway, or unintended.",
		"- Gracefully stop only processes that are clearly stale or unintended; prefer application/runtime-specific shutdown or SIGTERM before SIGKILL.",
		"- Do not terminate macOS system services, Finder, WindowServer, fileproviderd, browsers, chat apps, editors, or other foreground user apps unless the evidence is unambiguous.",
		"- If uncertain, do not kill the process; report the evidence and ask for confirmation.",
		"- After any action, resample CPU and report what changed, what was left alone, and any follow-up needed.",
	)
	return strings.Join(lines, "\n")
}

func (m Model) cpuRemediationProcessLine(process procinspect.CPUProcess) string {
	parts := []string{
		fmt.Sprintf("PID %d", process.PID),
		fmt.Sprintf("PPID %d", process.PPID),
		fmt.Sprintf("PGID %d", process.PGID),
		"CPU " + formatCPUPercent(process.CPU),
		"MEM " + formatCPUPercent(process.Mem),
		"name " + cpuProcessName(process.Process),
		"owner " + m.cpuProcessOwnerLabel(process),
	}
	if stat := strings.TrimSpace(process.Stat); stat != "" {
		parts = append(parts, "state "+stat)
	}
	if elapsed := strings.TrimSpace(process.Elapsed); elapsed != "" {
		parts = append(parts, "age "+elapsed)
	}
	if len(process.Reasons) > 0 {
		parts = append(parts, "reasons "+strings.Join(process.Reasons, ", "))
	}
	if cwd := strings.TrimSpace(process.CWD); cwd != "" {
		parts = append(parts, "cwd "+singleLineCPUField(cwd))
	}
	if command := strings.TrimSpace(process.Command); command != "" {
		parts = append(parts, "cmd "+singleLineCPUField(command))
	}
	return strings.Join(parts, "; ")
}

func singleLineCPUField(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
}

func (m Model) renderCPUDialogOverlay(body string, bodyW, bodyH int) string {
	panel := m.renderCPUDialogPanel(bodyW, bodyH)
	panelW := lipgloss.Width(panel)
	panelH := lipgloss.Height(panel)
	left := max(0, (bodyW-panelW)/2)
	top := max(0, (bodyH-panelH)/4)
	return overlayBlock(body, panel, bodyW, bodyH, left, top)
}

func (m Model) renderCPURemediationEditorOverlay(body string, bodyW, bodyH int) string {
	editor := m.cpuRemediationEditor
	if editor == nil {
		return body
	}
	panelW, panelInnerW, editorHeight := cpuRemediationEditorPanelLayout(bodyW, bodyH)
	editor.Input.SetWidth(max(24, panelInnerW))
	editor.Input.SetHeight(editorHeight)
	lines := []string{
		detailSectionStyle.Render("CPU Engineer Prompt") + "  " + detailValueStyle.Render(cpuDialogScopeLabel(len(editor.Processes), len(editor.Processes))),
		"",
		editor.Input.View(),
		"",
		cpuRemediationEditorLegendLine(),
	}
	panel := renderDialogPanel(panelW, panelInnerW, strings.Join(lines, "\n"))
	left := max(0, (bodyW-panelW)/2)
	top := max(0, (bodyH-lipgloss.Height(panel))/2)
	return overlayBlock(body, panel, bodyW, bodyH, left, top)
}

func cpuRemediationEditorPanelLayout(bodyW, bodyH int) (int, int, int) {
	panelW := min(max(68, bodyW-8), 112)
	panelInnerW := max(34, panelW-4)
	editorHeight := max(8, min(18, bodyH-10))
	return panelW, panelInnerW, editorHeight
}

func cpuRemediationEditorLegendLine() string {
	return renderHelpPanelActionRow(
		renderDialogAction("enter", "newline", navigateActionKeyStyle, navigateActionTextStyle),
		renderDialogAction("ctrl+s", "send", commitActionKeyStyle, commitActionTextStyle),
		renderDialogAction("Esc", "cancel", cancelActionKeyStyle, cancelActionTextStyle),
	)
}

func (m Model) renderCPUDialogPanel(bodyW, bodyH int) string {
	panelW := min(bodyW, min(max(66, bodyW-12), 112))
	panelInnerW := max(34, panelW-4)
	maxContentH := max(10, bodyH-6)
	return renderDialogPanel(panelW, panelInnerW, m.renderCPUDialogContent(panelInnerW, maxContentH))
}

func (m Model) renderCPUDialogContent(width, maxHeight int) string {
	dialog := m.cpuDialog
	if dialog == nil {
		return ""
	}
	lines := []string{
		renderDialogHeader("CPU Inspector", "System", "", width),
		detailField("Scope", detailMutedStyle.Render(cpuDialogScopeLabel(len(dialog.Processes), dialog.ProcessCount))),
	}
	if len(dialog.Processes) > 0 {
		shownCPU := sumCPUProcesses(dialog.Processes)
		lines = append(lines, detailField("Shown", cpuSystemTotalStyle(shownCPU, dialog.LogicalCPUs).Render(formatSystemCPUPercent(shownCPU, dialog.LogicalCPUs))))
	}
	lines = append(lines, detailField("Total", cpuSystemTotalStyle(dialog.TotalCPU, dialog.LogicalCPUs).Render(formatSystemCPUPercent(dialog.TotalCPU, dialog.LogicalCPUs))))
	if !dialog.ScannedAt.IsZero() {
		lines = append(lines, detailField("Scanned", detailMutedStyle.Render(dialog.ScannedAt.Format(timeFieldFormat))))
	}
	if dialog.Loading {
		lines = append(lines, "", commandPaletteHintStyle.Render("Sampling CPU usage..."))
	} else if strings.TrimSpace(dialog.Error) != "" {
		lines = append(lines, "", detailDangerStyle.Render("CPU scan failed"))
		lines = append(lines, renderWrappedDialogTextLines(detailDangerStyle, width, dialog.Error)...)
	} else if len(dialog.Processes) == 0 {
		lines = append(lines, "", detailValueStyle.Render("No CPU-using processes found."))
	} else {
		lines = append(lines, "", detailWarningStyle.Render(cpuDialogCountLabel(len(dialog.Processes))))
		processRowLimit := min(cpuDialogVisibleProcessRows, max(3, maxHeight-len(lines)-7))
		lines = append(lines, m.renderCPUProcessRows(width, processRowLimit)...)
		if dialog.Selected >= 0 && dialog.Selected < len(dialog.Processes) {
			lines = append(lines, "")
			lines = append(lines, m.renderCPUProcessDetail(width, dialog.Processes[dialog.Selected])...)
		}
	}
	lines = append(lines, "",
		renderCPUDialogActions(),
	)
	return strings.Join(limitLines(lines, maxHeight), "\n")
}

func renderCPUDialogActions() string {
	return renderDialogAction("a", "ask engineer", pushActionKeyStyle, pushActionTextStyle) + "   " +
		renderDialogAction("r", "refresh", navigateActionKeyStyle, navigateActionTextStyle) + "   " +
		renderDialogAction("Esc", "close", cancelActionKeyStyle, cancelActionTextStyle)
}

func (m Model) renderCPUProcessRows(width, maxLines int) []string {
	dialog := m.cpuDialog
	if dialog == nil || len(dialog.Processes) == 0 || maxLines <= 0 {
		return nil
	}
	maxLines = min(cpuDialogVisibleProcessRows, max(1, maxLines))
	if dialog.Selected < 0 {
		dialog.Selected = 0
	}
	if dialog.Selected >= len(dialog.Processes) {
		dialog.Selected = len(dialog.Processes) - 1
	}
	start, end := cpuProcessRowWindow(len(dialog.Processes), dialog.Selected, maxLines)
	rows := []string{}
	if start > 0 {
		rows = append(rows, commandPaletteHintStyle.Render(fmt.Sprintf("↑ %d more", start)))
	}
	for i := start; i < end; i++ {
		process := dialog.Processes[i]
		marker := " "
		style := commandPaletteRowStyle
		if i == dialog.Selected {
			marker = "›"
			style = commandPaletteSelectStyle
		}
		owner := truncateText(m.cpuProcessOwnerLabel(process), 24)
		name := truncateText(cpuProcessName(process.Process), 18)
		row := fmt.Sprintf("%s PID %-6d CPU %6s MEM %5s  %-18s  %-24s", marker, process.PID, formatCPUPercent(process.CPU), formatCPUPercent(process.Mem), name, owner)
		rows = append(rows, style.Width(width).Render(truncateText(row, width)))
	}
	if end < len(dialog.Processes) {
		rows = append(rows, commandPaletteHintStyle.Render(fmt.Sprintf("↓ %d more", len(dialog.Processes)-end)))
	}
	return rows
}

func cpuProcessRowWindow(total, selected, maxLines int) (int, int) {
	if total <= 0 || maxLines <= 0 {
		return 0, 0
	}
	if total <= maxLines {
		return 0, total
	}
	selected = clampInt(selected, 0, total-1)
	processSlots := maxLines
	start, end := 0, 0
	for {
		start = clampInt(selected-processSlots/2, 0, max(0, total-processSlots))
		end = min(total, start+processSlots)
		hintCount := 0
		if start > 0 {
			hintCount++
		}
		if end < total {
			hintCount++
		}
		nextSlots := max(1, maxLines-hintCount)
		if nextSlots == processSlots {
			return start, end
		}
		processSlots = nextSlots
	}
}

func (m Model) renderCPUProcessDetail(width int, process procinspect.CPUProcess) []string {
	lines := []string{commandPaletteTitleStyle.Render(fmt.Sprintf("Selected PID %d", process.PID))}
	fields := []string{
		detailField("Parent", detailValueStyle.Render(fmt.Sprintf("%d", process.PPID))),
		detailField("Group", detailValueStyle.Render(fmt.Sprintf("%d", process.PGID))),
		detailField("State", detailValueStyle.Render(strings.TrimSpace(process.Stat))),
		detailField("CPU", cpuProcessStyle(process.CPU).Render(formatCPUPercent(process.CPU))),
		detailField("Mem", detailValueStyle.Render(formatCPUPercent(process.Mem))),
		detailField("Owner", detailValueStyle.Render(truncateText(m.cpuProcessOwnerLabel(process), max(14, width/4)))),
	}
	if strings.TrimSpace(process.Elapsed) != "" {
		fields = append(fields, detailField("Age", detailValueStyle.Render(process.Elapsed)))
	}
	if len(process.Ports) > 0 {
		fields = append(fields, detailField("Ports", detailValueStyle.Render(joinPorts(process.Ports))))
	}
	lines = appendCPUCompactFields(lines, width, fields...)
	if len(process.Reasons) > 0 {
		lines = append(lines, renderCompactCPUTextField("Why", detailWarningStyle, width, strings.Join(process.Reasons, ", ")))
	}
	if strings.TrimSpace(process.ProjectPath) != "" {
		lines = append(lines, renderCompactCPUTextField("Project", detailValueStyle, width, m.runtimeOwnerLabel(process.ProjectPath)))
	}
	if strings.TrimSpace(process.CWD) != "" {
		lines = append(lines, renderCompactCPUTextField("CWD", detailMutedStyle, width, m.displayPathWithHomeTilde(process.CWD)))
	}
	if strings.TrimSpace(process.Command) != "" {
		lines = append(lines, renderCompactCPUTextField("Cmd", detailMutedStyle, width, process.Command))
	}
	return lines
}

func appendCPUCompactFields(lines []string, width int, fields ...string) []string {
	current := ""
	for _, field := range fields {
		if strings.TrimSpace(field) == "" {
			continue
		}
		if current == "" {
			current = field
			continue
		}
		next := current + "  " + field
		if lipgloss.Width(next) > width {
			lines = append(lines, current)
			current = field
			continue
		}
		current = next
	}
	if current != "" {
		lines = append(lines, current)
	}
	return lines
}

func renderCompactCPUTextField(label string, style lipgloss.Style, width int, text string) string {
	text = singleLineCPUField(text)
	valueWidth := max(8, width-len(label)-2)
	return detailField(label, style.Render(truncateText(text, valueWidth)))
}

func (m Model) cpuProcessOwnerLabel(process procinspect.CPUProcess) string {
	parts := []string{}
	if process.OwnedByCurrentApp {
		parts = append(parts, "LCR child")
	}
	if process.ManagedRuntime {
		parts = append(parts, "managed runtime")
	}
	if strings.TrimSpace(process.ProjectPath) != "" {
		parts = append(parts, m.runtimeOwnerLabel(process.ProjectPath))
	}
	if process.PPID == 1 {
		parts = append(parts, "orphaned")
	}
	if len(parts) == 0 {
		return "system"
	}
	return strings.Join(parts, ", ")
}

func (m Model) renderTopCPUUsageSegment() string {
	label := cpuSnapshotHeaderLabel(m.cpuSnapshot, !m.privacyMode)
	if label == "" {
		return ""
	}
	if cpuSnapshotIsHot(m.cpuSnapshot) {
		return renderFooterAlert(label)
	}
	return renderFooterUsage(label)
}

func cpuSnapshotHeaderLabel(snapshot procinspect.CPUSnapshot, includeProcess bool) string {
	if snapshot.ScannedAt.IsZero() {
		return ""
	}
	parts := []string{"CPU " + formatSystemCPUPercent(snapshot.TotalCPU, snapshot.LogicalCPUs)}
	if includeProcess && len(snapshot.Processes) > 0 && snapshot.Processes[0].CPU >= 1 {
		parts = append(parts, truncateText(cpuProcessName(snapshot.Processes[0].Process), 14), formatCPUPercent(snapshot.Processes[0].CPU))
	}
	return strings.Join(parts, " ")
}

func cpuSnapshotSystemNoticeSummary(snapshot procinspect.CPUSnapshot, includeProcess bool) string {
	if !cpuSnapshotIsHot(snapshot) {
		return ""
	}
	label := "CPU monitor: total " + formatSystemCPUPercent(snapshot.TotalCPU, snapshot.LogicalCPUs)
	if includeProcess && len(snapshot.Processes) > 0 {
		top := snapshot.Processes[0]
		label += fmt.Sprintf("; top PID %d %s at %s", top.PID, cpuProcessName(top.Process), formatCPUPercent(top.CPU))
	} else if len(snapshot.Processes) > 0 {
		top := snapshot.Processes[0]
		label += fmt.Sprintf("; top PID %d at %s", top.PID, formatCPUPercent(top.CPU))
	}
	return label + "; use /cpu for top CPU details"
}

func cpuSnapshotIsHot(snapshot procinspect.CPUSnapshot) bool {
	if snapshot.ScannedAt.IsZero() {
		return false
	}
	if cpuSnapshotTotalIsHot(snapshot) {
		return true
	}
	return len(snapshot.Processes) > 0 && snapshot.Processes[0].CPU >= procinspect.DefaultHighCPUThreshold
}

func cpuSnapshotTotalIsHot(snapshot procinspect.CPUSnapshot) bool {
	if snapshot.LogicalCPUs > 1 {
		return cpuCapacityPercent(snapshot.TotalCPU, snapshot.LogicalCPUs) >= hotSystemCPUCapacityPercent
	}
	return snapshot.TotalCPU >= procinspect.DefaultHotTotalCPU
}

func cpuSnapshotHotProcessCount(snapshot procinspect.CPUSnapshot) int {
	count := 0
	for _, process := range snapshot.Processes {
		if process.CPU >= procinspect.DefaultHighCPUThreshold {
			count++
		}
	}
	if count == 0 && cpuSnapshotTotalIsHot(snapshot) {
		return 1
	}
	return count
}

func cpuDialogReadyStatus(snapshot procinspect.CPUSnapshot) string {
	if len(snapshot.Processes) == 0 {
		return "CPU snapshot ready"
	}
	top := snapshot.Processes[0]
	return fmt.Sprintf("CPU %s total; top PID %d %s", formatSystemCPUPercent(snapshot.TotalCPU, snapshot.LogicalCPUs), top.PID, formatCPUPercent(top.CPU))
}

func cpuDialogCountLabel(count int) string {
	if count == 1 {
		return "Top CPU process"
	}
	return fmt.Sprintf("Top %d CPU processes", count)
}

func cpuDialogScopeLabel(shown, total int) string {
	if shown > 0 && total > shown {
		return fmt.Sprintf("top %d of %d system processes", shown, total)
	}
	return "top system processes"
}

func cpuProcessName(process procinspect.Process) string {
	command := strings.TrimSpace(process.Command)
	if command == "" {
		return fmt.Sprintf("pid-%d", process.PID)
	}
	first := strings.Fields(command)[0]
	if base := filepath.Base(first); base != "." && base != string(filepath.Separator) && base != "" {
		return base
	}
	return first
}

func formatCPUPercent(value float64) string {
	if value >= 10 {
		return fmt.Sprintf("%.0f%%", value)
	}
	return fmt.Sprintf("%.1f%%", value)
}

func formatSystemCPUPercent(value float64, logicalCPUs int) string {
	raw := formatCPUPercent(value)
	if logicalCPUs <= 1 {
		return raw
	}
	return fmt.Sprintf("%s (%s raw)", formatCPUPercent(cpuCapacityPercent(value, logicalCPUs)), raw)
}

func cpuCapacityPercent(value float64, logicalCPUs int) float64 {
	if logicalCPUs <= 1 {
		return value
	}
	return value / float64(logicalCPUs)
}

func sumCPUProcesses(processes []procinspect.CPUProcess) float64 {
	total := 0.0
	for _, process := range processes {
		if process.CPU > 0 {
			total += process.CPU
		}
	}
	return total
}

func cpuProcessStyle(cpu float64) lipgloss.Style {
	if cpu >= procinspect.DefaultHighCPUThreshold {
		return detailDangerStyle
	}
	if cpu >= 25 {
		return detailWarningStyle
	}
	return detailValueStyle
}

func cpuTotalStyle(total float64) lipgloss.Style {
	if total >= procinspect.DefaultHotTotalCPU {
		return detailDangerStyle
	}
	if total >= 50 {
		return detailWarningStyle
	}
	return detailValueStyle
}

func cpuSystemTotalStyle(total float64, logicalCPUs int) lipgloss.Style {
	if logicalCPUs <= 1 {
		return cpuTotalStyle(total)
	}
	capacityPercent := cpuCapacityPercent(total, logicalCPUs)
	if capacityPercent >= hotSystemCPUCapacityPercent {
		return detailDangerStyle
	}
	if capacityPercent >= 50 {
		return detailWarningStyle
	}
	return detailValueStyle
}
