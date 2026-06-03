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

type cpuRemediationScope int

const (
	cpuRemediationScopeScoped cpuRemediationScope = iota
	cpuRemediationScopeSnapshot
)

type cpuDialogView int

const (
	cpuDialogViewTopCPU cpuDialogView = iota
	cpuDialogViewProjectPIDs
)

type cpuDialogState struct {
	Loading          bool
	Error            string
	View             cpuDialogView
	ViewPinned       bool
	Processes        []procinspect.CPUProcess
	FlaggedProcesses []procinspect.CPUProcess
	FlagProjectPath  string
	FlagProjectName  string
	Selected         int
	SelectedPID      int
	MarkedPIDs       map[int]struct{}
	ScannedAt        time.Time
	TotalCPU         float64
	ProcessCount     int
	LogicalCPUs      int
}

type cpuRemediationEditorState struct {
	Input      textarea.Model
	Processes  []procinspect.CPUProcess
	Scope      cpuRemediationScope
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
		View:    cpuDialogViewTopCPU,
	}
	dialog.FlagProjectPath, dialog.FlagProjectName = m.cpuDialogInitialFlagScope()
	dialog.FlaggedProcesses = m.cpuDialogFlaggedProcesses(dialog.FlagProjectPath)
	if len(dialog.FlaggedProcesses) > 0 {
		dialog.View = cpuDialogViewProjectPIDs
	}
	if !m.cpuSnapshot.ScannedAt.IsZero() {
		dialog.Loading = false
		dialog.Processes = m.cpuDialogProcesses(m.cpuSnapshot)
		dialog.ScannedAt = m.cpuSnapshot.ScannedAt
		dialog.TotalCPU = m.cpuSnapshot.TotalCPU
		dialog.ProcessCount = m.cpuSnapshot.ProcessCount
		dialog.LogicalCPUs = m.cpuSnapshot.LogicalCPUs
	}
	cpuDialogSyncSelectedPID(&dialog)
	m.cpuDialog = &dialog
	m.showHelp = false
	m.err = nil
	m.status = "Inspecting CPU usage..."
	return batchCmds(m.requestCPUSnapshotRefreshCmd(), m.requestProcessScanCmd(dialog.FlagProjectPath))
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
	case "tab":
		if len(dialog.FlaggedProcesses) == 0 {
			m.status = "No project PID flags in scope"
			return m, nil
		}
		selectedPID := cpuDialogSelectedPID(dialog)
		if cpuDialogNormalizedView(dialog) == cpuDialogViewProjectPIDs {
			dialog.View = cpuDialogViewTopCPU
			m.status = "CPU inspector showing top CPU"
		} else {
			dialog.View = cpuDialogViewProjectPIDs
			m.status = "CPU inspector showing project PID flags"
		}
		dialog.ViewPinned = true
		cpuDialogSelectPID(dialog, selectedPID)
		return m, nil
	case "up", "k":
		if dialog.Selected > 0 {
			dialog.Selected--
			cpuDialogSyncSelectedPID(dialog)
		}
		return m, nil
	case "down", "j":
		if dialog.Selected < len(cpuDialogActiveProcesses(dialog))-1 {
			dialog.Selected++
			cpuDialogSyncSelectedPID(dialog)
		}
		return m, nil
	case " ":
		process, marked, ok := cpuDialogToggleMarkedProcess(dialog)
		if !ok {
			m.status = "No CPU process selected"
			return m, nil
		}
		action := "Removed"
		preposition := "from"
		if marked {
			action = "Added"
			preposition = "to"
		}
		m.status = fmt.Sprintf("%s PID %d %s %s CPU ask scope", action, process.PID, cpuProcessName(process.Process), preposition)
		return m, nil
	case "r":
		dialog.Loading = true
		dialog.Error = ""
		m.status = "Refreshing CPU usage..."
		return m, m.requestCPUSnapshotRefreshCmd()
	case "a":
		return m.openCPURemediationEditor(cpuRemediationScopeScoped)
	case "A", "shift+a":
		return m.openCPURemediationEditor(cpuRemediationScopeSnapshot)
	case "enter":
		if len(cpuDialogActiveProcesses(dialog)) == 0 {
			return m, nil
		}
		process, ok := cpuDialogSelectedProcess(dialog)
		if !ok {
			return m, nil
		}
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
			selectedPID := cpuDialogSelectedPID(m.cpuDialog)
			processes := m.cpuDialogProcesses(msg.snapshot)
			processes = stableCPUDialogProcesses(m.cpuDialog.Processes, processes)
			flagged := m.cpuDialogFlaggedProcesses(m.cpuDialog.FlagProjectPath)
			flagged = stableCPUDialogProcesses(m.cpuDialog.FlaggedProcesses, flagged)
			m.cpuDialog.Loading = false
			m.cpuDialog.Error = ""
			m.cpuDialog.Processes = processes
			m.cpuDialog.FlaggedProcesses = flagged
			if len(flagged) > 0 && !m.cpuDialog.ViewPinned {
				m.cpuDialog.View = cpuDialogViewProjectPIDs
			} else if len(flagged) == 0 && cpuDialogNormalizedView(m.cpuDialog) == cpuDialogViewProjectPIDs {
				m.cpuDialog.View = cpuDialogViewTopCPU
			}
			m.cpuDialog.ScannedAt = msg.snapshot.ScannedAt
			m.cpuDialog.TotalCPU = msg.snapshot.TotalCPU
			m.cpuDialog.ProcessCount = msg.snapshot.ProcessCount
			m.cpuDialog.LogicalCPUs = msg.snapshot.LogicalCPUs
			cpuDialogPruneMarkedPIDs(m.cpuDialog)
			cpuDialogSelectPID(m.cpuDialog, selectedPID)
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
	return append([]procinspect.CPUProcess(nil), snapshot.Processes...)
}

func (m Model) cpuDialogInitialFlagScope() (string, string) {
	if project, ok := m.selectedProject(); ok {
		path := filepath.Clean(strings.TrimSpace(project.Path))
		if path != "" && path != "." {
			name := strings.TrimSpace(project.Name)
			if name == "" {
				name = filepath.Base(path)
			}
			return path, name
		}
	}
	return "", "All Projects"
}

func (m Model) cpuDialogFlaggedProcesses(projectPath string) []procinspect.CPUProcess {
	projectPath = filepath.Clean(strings.TrimSpace(projectPath))
	var findings []procinspect.Finding
	if projectPath != "" && projectPath != "." {
		report, ok := m.projectProcessReport(projectPath)
		if !ok {
			return nil
		}
		findings = report.Findings
	} else {
		findings, _ = m.globalProcessFindings()
	}
	return cpuProcessesFromFindings(findings)
}

func cpuProcessesFromFindings(findings []procinspect.Finding) []procinspect.CPUProcess {
	if len(findings) == 0 {
		return nil
	}
	processes := make([]procinspect.CPUProcess, 0, len(findings))
	seen := map[int]struct{}{}
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
			PortConflict:      finding.PortConflict,
			ConflictPorts:     append([]int(nil), finding.ConflictPorts...),
			OwnerProjectPath:  finding.OwnerProjectPath,
		})
	}
	return processes
}

func (m *Model) refreshCPUDialogFlaggedProcesses() {
	if m.cpuDialog == nil {
		return
	}
	selectedPID := cpuDialogSelectedPID(m.cpuDialog)
	flagged := m.cpuDialogFlaggedProcesses(m.cpuDialog.FlagProjectPath)
	m.cpuDialog.FlaggedProcesses = stableCPUDialogProcesses(m.cpuDialog.FlaggedProcesses, flagged)
	if len(m.cpuDialog.FlaggedProcesses) > 0 && !m.cpuDialog.ViewPinned {
		m.cpuDialog.View = cpuDialogViewProjectPIDs
	} else if len(m.cpuDialog.FlaggedProcesses) == 0 && cpuDialogNormalizedView(m.cpuDialog) == cpuDialogViewProjectPIDs {
		m.cpuDialog.View = cpuDialogViewTopCPU
	}
	cpuDialogPruneMarkedPIDs(m.cpuDialog)
	cpuDialogSelectPID(m.cpuDialog, selectedPID)
}

func stableCPUDialogProcesses(previous, current []procinspect.CPUProcess) []procinspect.CPUProcess {
	current = append([]procinspect.CPUProcess(nil), current...)
	if len(previous) == 0 || len(current) <= 1 {
		return current
	}
	byPID := make(map[int]procinspect.CPUProcess, len(current))
	for _, process := range current {
		if process.PID > 0 {
			byPID[process.PID] = process
		}
	}
	out := make([]procinspect.CPUProcess, 0, len(current))
	used := map[int]struct{}{}
	for _, previousProcess := range previous {
		if previousProcess.PID <= 0 {
			continue
		}
		process, ok := byPID[previousProcess.PID]
		if !ok {
			continue
		}
		if _, ok := used[process.PID]; ok {
			continue
		}
		used[process.PID] = struct{}{}
		out = append(out, process)
	}
	for _, process := range current {
		if process.PID > 0 {
			if _, ok := used[process.PID]; ok {
				continue
			}
			used[process.PID] = struct{}{}
		}
		out = append(out, process)
	}
	return out
}

func cpuDialogNormalizedView(dialog *cpuDialogState) cpuDialogView {
	if dialog == nil {
		return cpuDialogViewTopCPU
	}
	if dialog.View == cpuDialogViewProjectPIDs {
		return cpuDialogViewProjectPIDs
	}
	return cpuDialogViewTopCPU
}

func cpuDialogActiveProcesses(dialog *cpuDialogState) []procinspect.CPUProcess {
	if dialog == nil {
		return nil
	}
	if cpuDialogNormalizedView(dialog) == cpuDialogViewProjectPIDs {
		return dialog.FlaggedProcesses
	}
	return dialog.Processes
}

func cpuDialogKnownProcesses(dialog *cpuDialogState) []procinspect.CPUProcess {
	if dialog == nil {
		return nil
	}
	out := make([]procinspect.CPUProcess, 0, len(dialog.Processes)+len(dialog.FlaggedProcesses))
	seen := map[int]struct{}{}
	add := func(process procinspect.CPUProcess) {
		if process.PID <= 0 {
			return
		}
		if _, ok := seen[process.PID]; ok {
			return
		}
		seen[process.PID] = struct{}{}
		out = append(out, process)
	}
	for _, process := range cpuDialogActiveProcesses(dialog) {
		add(process)
	}
	for _, process := range dialog.Processes {
		add(process)
	}
	for _, process := range dialog.FlaggedProcesses {
		add(process)
	}
	return out
}

func cpuDialogSelectedPID(dialog *cpuDialogState) int {
	if dialog == nil {
		return 0
	}
	if dialog.SelectedPID > 0 {
		return dialog.SelectedPID
	}
	if process, ok := cpuDialogSelectedProcess(dialog); ok {
		return process.PID
	}
	return 0
}

func cpuDialogSelectPID(dialog *cpuDialogState, pid int) {
	if dialog == nil {
		return
	}
	processes := cpuDialogActiveProcesses(dialog)
	if len(processes) == 0 {
		dialog.Selected = 0
		dialog.SelectedPID = 0
		return
	}
	if pid > 0 {
		for i, process := range processes {
			if process.PID == pid {
				dialog.Selected = i
				dialog.SelectedPID = pid
				return
			}
		}
	}
	dialog.Selected = clampInt(dialog.Selected, 0, len(processes)-1)
	dialog.SelectedPID = processes[dialog.Selected].PID
}

func cpuDialogSyncSelectedPID(dialog *cpuDialogState) {
	if dialog == nil {
		return
	}
	processes := cpuDialogActiveProcesses(dialog)
	if len(processes) == 0 {
		dialog.Selected = 0
		dialog.SelectedPID = 0
		return
	}
	dialog.Selected = clampInt(dialog.Selected, 0, len(processes)-1)
	dialog.SelectedPID = processes[dialog.Selected].PID
}

func cpuDialogSelectedProcess(dialog *cpuDialogState) (procinspect.CPUProcess, bool) {
	processes := cpuDialogActiveProcesses(dialog)
	if dialog == nil || len(processes) == 0 {
		return procinspect.CPUProcess{}, false
	}
	dialog.Selected = clampInt(dialog.Selected, 0, len(processes)-1)
	process := processes[dialog.Selected]
	dialog.SelectedPID = process.PID
	return process, process.PID > 0
}

func cpuDialogToggleMarkedProcess(dialog *cpuDialogState) (procinspect.CPUProcess, bool, bool) {
	process, ok := cpuDialogSelectedProcess(dialog)
	if !ok {
		return procinspect.CPUProcess{}, false, false
	}
	if dialog.MarkedPIDs == nil {
		dialog.MarkedPIDs = map[int]struct{}{}
	}
	if _, ok := dialog.MarkedPIDs[process.PID]; ok {
		delete(dialog.MarkedPIDs, process.PID)
		return process, false, true
	}
	dialog.MarkedPIDs[process.PID] = struct{}{}
	return process, true, true
}

func cpuDialogMarkedProcesses(dialog *cpuDialogState) []procinspect.CPUProcess {
	if dialog == nil || len(dialog.MarkedPIDs) == 0 {
		return nil
	}
	out := make([]procinspect.CPUProcess, 0, min(cpuRemediationProcessLimit, len(dialog.MarkedPIDs)))
	seen := map[int]struct{}{}
	for _, process := range cpuDialogKnownProcesses(dialog) {
		if len(out) >= cpuRemediationProcessLimit || process.PID <= 0 {
			continue
		}
		if _, ok := dialog.MarkedPIDs[process.PID]; !ok {
			continue
		}
		if _, ok := seen[process.PID]; ok {
			continue
		}
		seen[process.PID] = struct{}{}
		out = append(out, process)
	}
	return out
}

func cpuDialogPruneMarkedPIDs(dialog *cpuDialogState) {
	if dialog == nil || len(dialog.MarkedPIDs) == 0 {
		return
	}
	present := map[int]struct{}{}
	for _, process := range cpuDialogKnownProcesses(dialog) {
		if process.PID > 0 {
			present[process.PID] = struct{}{}
		}
	}
	for pid := range dialog.MarkedPIDs {
		if _, ok := present[pid]; !ok {
			delete(dialog.MarkedPIDs, pid)
		}
	}
	if len(dialog.MarkedPIDs) == 0 {
		dialog.MarkedPIDs = nil
	}
}

func cpuDialogMarkedCount(dialog *cpuDialogState) int {
	return len(cpuDialogMarkedProcesses(dialog))
}

func (m Model) openCPURemediationEditor(scope cpuRemediationScope) (tea.Model, tea.Cmd) {
	processes := m.cpuRemediationProcessesForScope(scope)
	if len(processes) == 0 {
		m.status = "No CPU snapshot to hand to an engineer yet"
		return m, nil
	}
	prompt := m.cpuRemediationPrompt(processes, scope)
	m.cpuRemediationEditor = &cpuRemediationEditorState{
		Input:     newCPURemediationPromptInput(prompt),
		Processes: append([]procinspect.CPUProcess(nil), processes...),
		Scope:     scope,
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
	return m.startCPURemediationTaskWithPrompt(processes, m.cpuRemediationPrompt(processes, cpuRemediationScopeScoped))
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
		ctx, cancel := m.actionContext(newTaskCreateTimeout)
		defer cancel()
		result, err := m.svc.CreateScratchTask(ctx, service.CreateScratchTaskRequest{Title: cpuRemediationTaskTitle})
		err = timeoutActionError(err, newTaskCreateTimeout, "creating the CPU remediation task")
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
		InScope:                         true,
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
	return m.cpuRemediationProcessesForScope(cpuRemediationScopeScoped)
}

func (m Model) cpuRemediationProcessesForScope(scope cpuRemediationScope) []procinspect.CPUProcess {
	dialog := m.cpuDialog
	if dialog == nil || len(cpuDialogActiveProcesses(dialog)) == 0 {
		return nil
	}
	if scope == cpuRemediationScopeSnapshot {
		processes := cpuDialogActiveProcesses(dialog)
		out := make([]procinspect.CPUProcess, 0, len(processes))
		seen := map[int]struct{}{}
		for _, process := range processes {
			if process.PID <= 0 {
				continue
			}
			if _, ok := seen[process.PID]; ok {
				continue
			}
			seen[process.PID] = struct{}{}
			out = append(out, process)
		}
		return out
	}
	if marked := cpuDialogMarkedProcesses(dialog); len(marked) > 0 {
		return marked
	}
	if process, ok := cpuDialogSelectedProcess(dialog); ok {
		return []procinspect.CPUProcess{process}
	}
	return nil
}

func (m Model) cpuRemediationPrompt(processes []procinspect.CPUProcess, scope cpuRemediationScope) string {
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

	lines := []string{cpuRemediationOpeningInstruction(processes, scope)}
	if scannedAt != "" {
		lines = append(lines, "Snapshot scanned at: "+scannedAt)
	}
	cpuScopeLabel := "Scoped CPU: "
	processSectionLabel := "Scoped CPU processes:"
	if scope == cpuRemediationScopeSnapshot {
		cpuScopeLabel = "Listed CPU: "
		processSectionLabel = "CPU processes to inspect:"
	}
	lines = append(lines,
		"Total CPU: "+formatSystemCPUPercent(totalCPU, logicalCPUs),
		cpuScopeLabel+formatSystemCPUPercent(sumCPUProcesses(processes), logicalCPUs),
		"Note: per-process CPU values are raw ps percentages, so totals can exceed 100% on multicore machines.",
		"",
		processSectionLabel,
	)
	for _, process := range processes {
		lines = append(lines, "- "+m.cpuRemediationProcessLine(process))
	}
	lines = append(lines,
		"",
		"Goal:",
	)
	if cpuProcessesIncludePortConflict(processes) {
		lines = append(lines,
			"- Determine which listener owns the expected runtime port and whether it explains browser traffic going to the wrong app.",
			"- Inspect the listener's command, cwd, parent process, age, and recent activity before deciding whether it is stale or intended.",
		)
	}
	if scope == cpuRemediationScopeSnapshot {
		lines = append(lines, "- Use the whole listed snapshot as context; choose the likely culprit yourself rather than assuming the selected row is relevant.")
	}
	lines = append(lines,
		"- Identify which processes are expected system or foreground user processes and which look stale, orphaned, runaway, or unintended.",
		"- Gracefully stop only processes that are clearly stale or unintended; prefer application/runtime-specific shutdown or SIGTERM before SIGKILL.",
		"- Do not terminate macOS system services, Finder, WindowServer, fileproviderd, browsers, chat apps, editors, or other foreground user apps unless the evidence is unambiguous.",
		"- If uncertain, do not kill the process; report the evidence and ask for confirmation.",
		"- After any action, resample CPU and report what changed, what was left alone, and any follow-up needed.",
	)
	return strings.Join(lines, "\n")
}

func cpuRemediationOpeningInstruction(processes []procinspect.CPUProcess, scope cpuRemediationScope) string {
	if cpuProcessesIncludePortConflict(processes) && scope == cpuRemediationScopeSnapshot {
		return "Investigate the current CPU and process situation and identify what, if anything, is unexpectedly consuming CPU or runtime ports."
	}
	if cpuProcessesIncludePortConflict(processes) {
		return "Investigate the selected runtime-port process issue and resolve it where it is safe to do so."
	}
	if scope == cpuRemediationScopeSnapshot {
		return "Investigate the current CPU situation and identify what, if anything, is taking CPU time unexpectedly."
	}
	return "Investigate the current high CPU usage and reduce it where it is safe to do so."
}

func cpuProcessesIncludePortConflict(processes []procinspect.CPUProcess) bool {
	for _, process := range processes {
		if process.PortConflict || len(process.ConflictPorts) > 0 {
			return true
		}
	}
	return false
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
	if len(process.ConflictPorts) > 0 {
		parts = append(parts, "conflict ports "+joinPorts(process.ConflictPorts))
	}
	if process.PortConflict && strings.TrimSpace(process.ProjectPath) != "" {
		parts = append(parts, "expected by "+m.runtimeOwnerLabel(process.ProjectPath))
	}
	if process.PortConflict && strings.TrimSpace(process.OwnerProjectPath) != "" {
		parts = append(parts, "port owner project "+m.runtimeOwnerLabel(process.OwnerProjectPath))
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
		detailSectionStyle.Render("CPU Engineer Prompt") + "  " + detailValueStyle.Render(cpuRemediationScopeLabel(editor.Scope, len(editor.Processes))),
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
	activeProcesses := cpuDialogActiveProcesses(dialog)
	lines := []string{
		renderDialogHeader("CPU Inspector", cpuDialogHeaderScope(dialog), "", width),
		detailField("View", detailValueStyle.Render(cpuDialogViewLabel(dialog))),
		detailField("Scope", detailMutedStyle.Render(cpuDialogScopeLabel(dialog, len(activeProcesses)))),
	}
	if len(activeProcesses) > 0 {
		shownCPU := sumCPUProcesses(activeProcesses)
		lines = append(lines, detailField("Shown", cpuSystemTotalStyle(shownCPU, dialog.LogicalCPUs).Render(formatSystemCPUPercent(shownCPU, dialog.LogicalCPUs))))
	}
	lines = append(lines, detailField("Total", cpuSystemTotalStyle(dialog.TotalCPU, dialog.LogicalCPUs).Render(formatSystemCPUPercent(dialog.TotalCPU, dialog.LogicalCPUs))))
	if !dialog.ScannedAt.IsZero() {
		lines = append(lines, detailField("Scanned", detailMutedStyle.Render(dialog.ScannedAt.Format(timeFieldFormat))))
	}
	if dialog.Loading && len(activeProcesses) == 0 {
		lines = append(lines, "", commandPaletteHintStyle.Render("Sampling CPU usage..."))
	} else if strings.TrimSpace(dialog.Error) != "" {
		lines = append(lines, "", detailDangerStyle.Render("CPU scan failed"))
		lines = append(lines, renderWrappedDialogTextLines(detailDangerStyle, width, dialog.Error)...)
	} else if len(activeProcesses) == 0 {
		lines = append(lines, "", detailValueStyle.Render(cpuDialogEmptyLabel(dialog)))
	} else {
		lines = append(lines, "", detailWarningStyle.Render(cpuDialogCountLabel(len(activeProcesses), cpuDialogMarkedCount(dialog), dialog)))
		processRowLimit := min(cpuDialogVisibleProcessRows, max(3, maxHeight-len(lines)-7))
		lines = append(lines, m.renderCPUProcessRows(width, processRowLimit)...)
		if dialog.Selected >= 0 && dialog.Selected < len(activeProcesses) {
			lines = append(lines, "")
			lines = append(lines, m.renderCPUProcessDetail(width, activeProcesses[dialog.Selected])...)
		}
	}
	lines = append(lines, "",
		renderCPUDialogActions(),
	)
	return strings.Join(limitLines(lines, maxHeight), "\n")
}

func renderCPUDialogActions() string {
	return renderDialogAction("space", "mark", pushActionKeyStyle, pushActionTextStyle) + "   " +
		renderDialogAction("tab", "view", navigateActionKeyStyle, navigateActionTextStyle) + "   " +
		renderDialogAction("a", "ask scoped", pushActionKeyStyle, pushActionTextStyle) + "   " +
		renderDialogAction("A", "ask all", pushActionKeyStyle, pushActionTextStyle) + "   " +
		renderDialogAction("r", "refresh", navigateActionKeyStyle, navigateActionTextStyle) + "   " +
		renderDialogAction("Esc", "close", cancelActionKeyStyle, cancelActionTextStyle)
}

func (m Model) renderCPUProcessRows(width, maxLines int) []string {
	dialog := m.cpuDialog
	processes := cpuDialogActiveProcesses(dialog)
	if dialog == nil || len(processes) == 0 || maxLines <= 0 {
		return nil
	}
	maxLines = min(cpuDialogVisibleProcessRows, max(1, maxLines))
	cpuDialogSyncSelectedPID(dialog)
	start, end := cpuProcessRowWindow(len(processes), dialog.Selected, maxLines)
	rows := []string{}
	if start > 0 {
		rows = append(rows, commandPaletteHintStyle.Render(fmt.Sprintf("↑ %d more", start)))
	}
	for i := start; i < end; i++ {
		process := processes[i]
		cursor := " "
		style := commandPaletteRowStyle
		if i == dialog.Selected {
			cursor = "›"
			style = commandPaletteSelectStyle
		}
		scope := " "
		if dialog.MarkedPIDs != nil {
			if _, ok := dialog.MarkedPIDs[process.PID]; ok {
				scope = "*"
			}
		}
		row := m.cpuProcessRow(dialog, process, cursor, scope)
		rows = append(rows, style.Width(width).Render(truncateText(row, width)))
	}
	if end < len(processes) {
		rows = append(rows, commandPaletteHintStyle.Render(fmt.Sprintf("↓ %d more", len(processes)-end)))
	}
	return rows
}

func (m Model) cpuProcessRow(dialog *cpuDialogState, process procinspect.CPUProcess, cursor, scope string) string {
	owner := truncateText(m.cpuProcessOwnerLabel(process), 24)
	name := truncateText(cpuProcessName(process.Process), 18)
	if cpuDialogNormalizedView(dialog) == cpuDialogViewProjectPIDs {
		reason := truncateText(strings.Join(process.Reasons, ", "), 30)
		return fmt.Sprintf("%s%s PID %-6d CPU %6s MEM %5s  %-18s  %-24s  %s", cursor, scope, process.PID, formatCPUPercent(process.CPU), formatCPUPercent(process.Mem), name, owner, reason)
	}
	return fmt.Sprintf("%s%s PID %-6d CPU %6s MEM %5s  %-18s  %-24s", cursor, scope, process.PID, formatCPUPercent(process.CPU), formatCPUPercent(process.Mem), name, owner)
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
	if len(process.ConflictPorts) > 0 {
		fields = append(fields, detailField("Conflict", detailDangerStyle.Render("port "+joinPorts(process.ConflictPorts)+" busy")))
	}
	lines = appendCPUCompactFields(lines, width, fields...)
	if len(process.Reasons) > 0 {
		lines = append(lines, renderCompactCPUTextField("Why", detailWarningStyle, width, strings.Join(process.Reasons, ", ")))
	}
	if strings.TrimSpace(process.ProjectPath) != "" {
		lines = append(lines, renderCompactCPUTextField("Project", detailValueStyle, width, m.runtimeOwnerLabel(process.ProjectPath)))
	}
	if process.PortConflict && strings.TrimSpace(process.OwnerProjectPath) != "" {
		lines = append(lines, renderCompactCPUTextField("Port owner", detailWarningStyle, width, m.runtimeOwnerLabel(process.OwnerProjectPath)))
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
	if process.PortConflict && strings.TrimSpace(process.OwnerProjectPath) != "" {
		parts = append(parts, m.runtimeOwnerLabel(process.OwnerProjectPath))
	} else if strings.TrimSpace(process.ProjectPath) != "" {
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

func cpuDialogHeaderScope(dialog *cpuDialogState) string {
	if cpuDialogNormalizedView(dialog) == cpuDialogViewProjectPIDs {
		name := strings.TrimSpace(dialog.FlagProjectName)
		if name != "" {
			return name
		}
		return "Project PIDs"
	}
	return "System"
}

func cpuDialogViewLabel(dialog *cpuDialogState) string {
	if cpuDialogNormalizedView(dialog) == cpuDialogViewProjectPIDs {
		return "Project PIDs"
	}
	return "Top CPU"
}

func cpuDialogEmptyLabel(dialog *cpuDialogState) string {
	if cpuDialogNormalizedView(dialog) == cpuDialogViewProjectPIDs {
		return "No project PID flags found."
	}
	return "No CPU-using processes found."
}

func cpuDialogCountLabel(count, marked int, dialog *cpuDialogState) string {
	scope := ""
	if marked > 0 {
		scope = fmt.Sprintf(" (%d marked)", marked)
	}
	if cpuDialogNormalizedView(dialog) == cpuDialogViewProjectPIDs {
		if count == 1 {
			return "1 project PID flag" + scope
		}
		return fmt.Sprintf("%d project PID flags%s", count, scope)
	}
	if count == 1 {
		return "Top CPU process" + scope
	}
	return fmt.Sprintf("Top %d CPU processes%s", count, scope)
}

func cpuDialogScopeLabel(dialog *cpuDialogState, shown int) string {
	if cpuDialogNormalizedView(dialog) == cpuDialogViewProjectPIDs {
		name := strings.TrimSpace(dialog.FlagProjectName)
		if name == "" || name == "All Projects" {
			if shown == 1 {
				return "1 flagged project-local process"
			}
			return fmt.Sprintf("%d flagged project-local processes", shown)
		}
		if shown == 1 {
			return "1 flagged process for " + name
		}
		return fmt.Sprintf("%d flagged processes for %s", shown, name)
	}
	if shown > 0 && dialog.ProcessCount > shown {
		return fmt.Sprintf("top %d of %d system processes", shown, dialog.ProcessCount)
	}
	return "top system processes"
}

func cpuRemediationScopeLabel(scope cpuRemediationScope, count int) string {
	if scope == cpuRemediationScopeSnapshot {
		if count == 1 {
			return "whole snapshot: 1 process"
		}
		return fmt.Sprintf("whole snapshot: %d processes", count)
	}
	if count == 1 {
		return "1 scoped process"
	}
	return fmt.Sprintf("%d scoped processes", count)
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
