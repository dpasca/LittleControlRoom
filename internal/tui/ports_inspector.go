package tui

import (
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"lcroom/internal/model"
	"lcroom/internal/procinspect"
	"lcroom/internal/projectrun"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const portsDialogVisibleRows = 9

type portsDialogState struct {
	ProjectPath string
	Loading     bool
	Error       string
	Items       []portsDialogItem
	Selected    int
	ScannedAt   time.Time
}

type portsDialogItem struct {
	ProjectPath     string
	Port            int
	Snapshot        projectrun.Snapshot
	ParentPID       int
	ManagedRuntime  bool
	External        bool
	Orphaned        bool
	PortConflict    bool
	ConflictTargets []string
	Reasons         []string
}

func (m *Model) openPortsDialog() tea.Cmd {
	items, scannedAt := m.globalPortListenerItems()
	dialog := portsDialogState{
		Loading:   scannedAt.IsZero() && len(items) == 0,
		Items:     items,
		ScannedAt: scannedAt,
	}
	portsDialogSyncSelected(&dialog)
	m.portsDialog = &dialog
	m.showHelp = false
	m.err = nil
	m.status = "Inspecting project ports..."
	return m.requestProcessScanCmd("")
}

func (m Model) updatePortsDialogMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	dialog := m.portsDialog
	if dialog == nil {
		return m, nil
	}
	switch msg.String() {
	case "esc", "q":
		m.portsDialog = nil
		m.status = "Ports inspector closed"
		return m, nil
	case "up", "k":
		if dialog.Selected > 0 {
			dialog.Selected--
		}
		return m, nil
	case "down", "j":
		if dialog.Selected < len(dialog.Items)-1 {
			dialog.Selected++
		}
		return m, nil
	case "r":
		dialog.Loading = true
		dialog.Error = ""
		m.status = "Refreshing project ports..."
		return m, m.requestProcessScanCmd(dialog.ProjectPath)
	case "s":
		item, ok := portsDialogSelectedItem(dialog)
		if !ok {
			m.status = "No port listener selected"
			return m, nil
		}
		if !portsDialogItemCanStop(item) {
			m.status = "Only external project-local listeners can be stopped from /ports"
			return m, nil
		}
		project := m.projectSummaryForPortsItem(item)
		m.portsDialog = nil
		return m, m.openExternalProcessStopConfirm(project, item.Snapshot)
	case "enter":
		item, ok := portsDialogSelectedItem(dialog)
		if !ok {
			return m, nil
		}
		m.status = fmt.Sprintf("Port %d: PID %d %s", item.Port, item.Snapshot.PID, strings.Join(portsDialogItemReasons(item), ", "))
		return m, nil
	case "ctrl+c":
		m.portsDialog = nil
		return m.updateNormalMode(msg)
	}
	return m, nil
}

func (m Model) applyPortsProcessScanMsg(msg processScanMsg) {
	if m.portsDialog == nil || !sameDialogProcessPath(m.portsDialog.ProjectPath, msg.dialogProjectPath) {
		return
	}
	m.portsDialog.Loading = false
	m.portsDialog.Error = ""
	if msg.err != nil {
		m.portsDialog.Error = msg.err.Error()
		m.status = "Ports scan failed"
		return
	}
	selectedKey := ""
	if item, ok := portsDialogSelectedItem(m.portsDialog); ok {
		selectedKey = portsDialogItemKey(item)
	}
	m.portsDialog.Items, m.portsDialog.ScannedAt = m.globalPortListenerItems()
	portsDialogSelectKey(m.portsDialog, selectedKey)
	m.status = portsDialogReadyStatus(len(m.portsDialog.Items))
}

func (m Model) globalPortListenerItems() ([]portsDialogItem, time.Time) {
	itemsByKey := map[string]*portsDialogItem{}
	addItem := func(item portsDialogItem) {
		item.ProjectPath = normalizeProjectPath(item.ProjectPath)
		if item.ProjectPath == "" || item.Port <= 0 {
			return
		}
		if len(item.Snapshot.Ports) == 0 {
			item.Snapshot.Ports = []int{item.Port}
		}
		item.Snapshot.ProjectPath = item.ProjectPath
		item.Snapshot.Ports = dedupeSortedPortsForDialog(item.Snapshot.Ports)
		item.Reasons = appendUniquePortDialogStrings(nil, item.Reasons...)
		item.ConflictTargets = appendUniquePortDialogStrings(nil, item.ConflictTargets...)
		key := portsDialogItemKey(item)
		if existing, ok := itemsByKey[key]; ok {
			mergePortsDialogItem(existing, item)
			return
		}
		itemCopy := item
		itemsByKey[key] = &itemCopy
	}

	for _, snapshot := range m.allRuntimeSnapshotsForPortsDialog() {
		if !snapshot.Running || len(snapshot.Ports) == 0 {
			continue
		}
		projectPath := normalizeProjectPath(snapshot.ProjectPath)
		if projectPath == "" {
			continue
		}
		ports := append([]int(nil), snapshot.Ports...)
		sort.Ints(ports)
		for _, port := range ports {
			reasons := []string{"managed runtime"}
			portConflict := intSliceContains(snapshot.ConflictPorts, port)
			if portConflict {
				reasons = append(reasons, "port conflict")
			}
			addItem(portsDialogItem{
				ProjectPath:    projectPath,
				Port:           port,
				Snapshot:       snapshot,
				ManagedRuntime: true,
				PortConflict:   portConflict,
				Reasons:        reasons,
			})
		}
	}

	var scannedAt time.Time
	for _, report := range m.processReports {
		if report.ScannedAt.After(scannedAt) {
			scannedAt = report.ScannedAt
		}
		for _, instance := range report.Instances {
			snapshot := localInstanceRuntimeSnapshot(instance)
			if snapshot.ProjectPath == "" || len(snapshot.Ports) == 0 {
				continue
			}
			reasons := []string{"external listener"}
			orphaned := instance.PPID == 1
			if orphaned {
				reasons = append(reasons, "orphaned under PID 1")
			}
			for _, port := range snapshot.Ports {
				addItem(portsDialogItem{
					ProjectPath:  snapshot.ProjectPath,
					Port:         port,
					Snapshot:     snapshot,
					ParentPID:    instance.PPID,
					External:     true,
					Orphaned:     orphaned,
					PortConflict: intSliceContains(instance.Ports, port) && processInstanceHasPortConflict(report.Findings, instance.PID, port),
					Reasons:      reasons,
				})
			}
		}
		for _, finding := range report.Findings {
			ports := portsForFinding(finding)
			if len(ports) == 0 {
				continue
			}
			projectPath := normalizeProjectPath(finding.OwnerProjectPath)
			if projectPath == "" {
				projectPath = normalizeProjectPath(finding.ProjectPath)
			}
			if projectPath == "" {
				continue
			}
			targets := []string{}
			if target := normalizeProjectPath(finding.ProjectPath); target != "" && target != projectPath {
				targets = append(targets, target)
			}
			snapshot := projectrun.Snapshot{
				ID:          fmt.Sprintf("pid_%d", finding.PID),
				Name:        "local listener",
				External:    !finding.ManagedRuntime && !finding.OwnedByCurrentApp,
				ProjectPath: projectPath,
				Command:     strings.TrimSpace(finding.Command),
				CWD:         strings.TrimSpace(finding.CWD),
				PID:         finding.PID,
				PGID:        finding.PGID,
				Running:     true,
				Ports:       append([]int(nil), finding.Ports...),
			}
			for _, port := range ports {
				if len(snapshot.Ports) == 0 {
					snapshot.Ports = []int{port}
				}
				addItem(portsDialogItem{
					ProjectPath:     projectPath,
					Port:            port,
					Snapshot:        snapshot,
					ParentPID:       finding.PPID,
					ManagedRuntime:  finding.ManagedRuntime,
					External:        snapshot.External,
					Orphaned:        finding.PPID == 1,
					PortConflict:    finding.PortConflict || intSliceContains(finding.ConflictPorts, port),
					ConflictTargets: targets,
					Reasons:         append([]string(nil), finding.Reasons...),
				})
			}
		}
	}

	items := make([]portsDialogItem, 0, len(itemsByKey))
	for _, item := range itemsByKey {
		items = append(items, *item)
	}
	sortPortsDialogItems(items)
	return items, scannedAt
}

func (m Model) allRuntimeSnapshotsForPortsDialog() []projectrun.Snapshot {
	if len(m.runtimeProcessSnapshots) > 0 {
		return append([]projectrun.Snapshot(nil), m.runtimeProcessSnapshots...)
	}
	out := make([]projectrun.Snapshot, 0, len(m.runtimeSnapshots))
	for _, snapshot := range m.runtimeSnapshots {
		out = append(out, snapshot)
	}
	return out
}

func mergePortsDialogItem(existing *portsDialogItem, item portsDialogItem) {
	if existing == nil {
		return
	}
	existing.ManagedRuntime = existing.ManagedRuntime || item.ManagedRuntime
	existing.External = existing.External || item.External
	existing.Orphaned = existing.Orphaned || item.Orphaned
	existing.PortConflict = existing.PortConflict || item.PortConflict
	existing.ConflictTargets = appendUniquePortDialogStrings(existing.ConflictTargets, item.ConflictTargets...)
	existing.Reasons = appendUniquePortDialogStrings(existing.Reasons, item.Reasons...)
	existing.Snapshot.Ports = dedupeSortedPortsForDialog(append(existing.Snapshot.Ports, item.Snapshot.Ports...))
	if strings.TrimSpace(existing.Snapshot.Command) == "" {
		existing.Snapshot.Command = item.Snapshot.Command
	}
	if strings.TrimSpace(existing.Snapshot.CWD) == "" {
		existing.Snapshot.CWD = item.Snapshot.CWD
	}
	if existing.ParentPID == 0 {
		existing.ParentPID = item.ParentPID
	}
	if existing.Snapshot.PGID == 0 {
		existing.Snapshot.PGID = item.Snapshot.PGID
	}
	if existing.Snapshot.PID == 0 {
		existing.Snapshot.PID = item.Snapshot.PID
	}
	if !existing.Snapshot.External {
		existing.Snapshot.External = item.Snapshot.External
	}
}

func sortPortsDialogItems(items []portsDialogItem) {
	sort.SliceStable(items, func(i, j int) bool {
		left, right := items[i], items[j]
		if left.PortConflict != right.PortConflict {
			return left.PortConflict
		}
		if left.Orphaned != right.Orphaned {
			return left.Orphaned
		}
		if left.External != right.External {
			return left.External
		}
		if left.ManagedRuntime != right.ManagedRuntime {
			return !left.ManagedRuntime
		}
		if left.Port != right.Port {
			return left.Port < right.Port
		}
		if left.ProjectPath != right.ProjectPath {
			return strings.ToLower(left.ProjectPath) < strings.ToLower(right.ProjectPath)
		}
		return left.Snapshot.PID < right.Snapshot.PID
	})
}

func (m Model) renderPortsDialogOverlay(body string, bodyW, bodyH int) string {
	panel := m.renderPortsDialogPanel(bodyW, bodyH)
	panelW := lipgloss.Width(panel)
	panelH := lipgloss.Height(panel)
	left := max(0, (bodyW-panelW)/2)
	top := max(0, (bodyH-panelH)/4)
	return overlayBlock(body, panel, bodyW, bodyH, left, top)
}

func (m Model) renderPortsDialogPanel(bodyW, bodyH int) string {
	panelW := min(bodyW, min(max(68, bodyW-12), 112))
	panelInnerW := max(36, panelW-4)
	maxContentH := max(10, bodyH-6)
	return renderDialogPanel(panelW, panelInnerW, m.renderPortsDialogContent(panelInnerW, maxContentH))
}

func (m Model) renderPortsDialogContent(width, maxHeight int) string {
	dialog := m.portsDialog
	if dialog == nil {
		return ""
	}
	lines := []string{
		renderDialogHeader("Ports Inspector", "All Projects", "", width),
		detailField("Scope", detailMutedStyle.Render("all tracked projects")),
	}
	if !dialog.ScannedAt.IsZero() {
		lines = append(lines, detailField("Scanned", detailMutedStyle.Render(dialog.ScannedAt.Format(timeFieldFormat))))
	}
	if dialog.Loading {
		lines = append(lines, "", commandPaletteHintStyle.Render("Scanning project-local TCP listeners..."))
	} else if strings.TrimSpace(dialog.Error) != "" {
		lines = append(lines, "", detailDangerStyle.Render("Ports scan failed"))
		lines = append(lines, renderWrappedDialogTextLines(detailDangerStyle, width, dialog.Error)...)
	} else if len(dialog.Items) == 0 {
		lines = append(lines, "", detailValueStyle.Render("No project-local TCP listeners found."))
	} else {
		lines = append(lines, "", detailWarningStyle.Render(portsDialogCountLabel(dialog.Items)))
		rowLimit := min(portsDialogVisibleRows, max(3, maxHeight-len(lines)-9))
		lines = append(lines, m.renderPortsDialogRows(width, rowLimit)...)
		if item, ok := portsDialogSelectedItem(dialog); ok {
			lines = append(lines, "")
			lines = append(lines, m.renderPortsDialogDetail(width, item)...)
		}
	}
	lines = append(lines, "",
		renderPortsDialogActions(),
	)
	return strings.Join(limitLines(lines, maxHeight), "\n")
}

func renderPortsDialogActions() string {
	return strings.Join([]string{
		renderDialogAction("s", "stop external", commitActionKeyStyle, commitActionTextStyle),
		renderDialogAction("r", "refresh", navigateActionKeyStyle, navigateActionTextStyle),
		renderDialogAction("Esc", "close", cancelActionKeyStyle, cancelActionTextStyle),
	}, "   ")
}

func (m Model) renderPortsDialogRows(width, limit int) []string {
	dialog := m.portsDialog
	if dialog == nil || len(dialog.Items) == 0 || limit <= 0 {
		return nil
	}
	portsDialogSyncSelected(dialog)
	start := 0
	if dialog.Selected >= limit {
		start = dialog.Selected - limit + 1
	}
	end := min(len(dialog.Items), start+limit)
	rows := []string{}
	if start > 0 {
		rows = append(rows, commandPaletteHintStyle.Render(fmt.Sprintf("more above: %d", start)))
	}
	for i := start; i < end; i++ {
		item := dialog.Items[i]
		marker := " "
		style := commandPaletteRowStyle
		if i == dialog.Selected {
			marker = ">"
			style = commandPaletteSelectStyle
		}
		row := fmt.Sprintf("%s PORT %-5d PID %-6d %-18s %-18s %s",
			marker,
			item.Port,
			item.Snapshot.PID,
			truncateText(m.runtimeOwnerLabel(item.ProjectPath), 18),
			truncateText(portsDialogItemStateLabel(item), 18),
			portsDialogCommandLabel(item),
		)
		rows = append(rows, style.Width(width).Render(truncateText(row, width)))
	}
	if end < len(dialog.Items) {
		rows = append(rows, commandPaletteHintStyle.Render(fmt.Sprintf("more below: %d", len(dialog.Items)-end)))
	}
	return rows
}

func (m Model) renderPortsDialogDetail(width int, item portsDialogItem) []string {
	lines := []string{commandPaletteTitleStyle.Render("Selected Listener")}
	fields := []string{
		detailField("Port", detailValueStyle.Render(strconv.Itoa(item.Port))),
		detailField("PID", detailValueStyle.Render(strconv.Itoa(item.Snapshot.PID))),
		detailField("Project", detailValueStyle.Render(m.runtimeOwnerLabel(item.ProjectPath))),
		detailField("State", portsDialogItemStateStyle(item).Render(portsDialogItemStateLabel(item))),
	}
	if item.Snapshot.PGID > 0 {
		fields = append(fields, detailField("Group", detailValueStyle.Render(strconv.Itoa(item.Snapshot.PGID))))
	}
	if item.ParentPID > 0 {
		fields = append(fields, detailField("Parent", detailValueStyle.Render(strconv.Itoa(item.ParentPID))))
	}
	if len(item.Snapshot.Ports) > 1 {
		fields = append(fields, detailField("All ports", detailValueStyle.Render(joinPorts(item.Snapshot.Ports))))
	}
	if len(item.ConflictTargets) > 0 {
		fields = append(fields, detailField("Conflict", detailDangerStyle.Render("wanted by "+m.portsDialogProjectLabels(item.ConflictTargets))))
	}
	lines = appendDetailFields(lines, width, fields...)
	if len(item.Reasons) > 0 {
		lines = append(lines, renderWrappedDetailField("Reasons", detailMutedStyle, width, strings.Join(item.Reasons, ", ")))
	}
	if strings.TrimSpace(item.Snapshot.CWD) != "" {
		lines = append(lines, renderWrappedDetailField("CWD", detailMutedStyle, width, m.displayPathWithHomeTilde(item.Snapshot.CWD)))
	}
	if strings.TrimSpace(item.Snapshot.Command) != "" {
		lines = append(lines, renderWrappedDetailField("Cmd", detailMutedStyle, width, item.Snapshot.Command))
	}
	if portsDialogItemCanStop(item) {
		lines = append(lines, renderWrappedDialogTextLines(commandPaletteHintStyle, width, "Press s to stop this external listener after confirmation.")...)
	} else if item.ManagedRuntime {
		lines = append(lines, renderWrappedDialogTextLines(commandPaletteHintStyle, width, "Managed runtime; use the runtime pane or /stop for shutdown.")...)
	} else {
		lines = append(lines, renderWrappedDialogTextLines(commandPaletteHintStyle, width, "Report-only listener; stop it from the owning shell if needed.")...)
	}
	return lines
}

func portsDialogSyncSelected(dialog *portsDialogState) {
	if dialog == nil {
		return
	}
	if len(dialog.Items) == 0 {
		dialog.Selected = 0
		return
	}
	dialog.Selected = clampInt(dialog.Selected, 0, len(dialog.Items)-1)
}

func portsDialogSelectKey(dialog *portsDialogState, key string) {
	if dialog == nil {
		return
	}
	if strings.TrimSpace(key) != "" {
		for i, item := range dialog.Items {
			if portsDialogItemKey(item) == key {
				dialog.Selected = i
				return
			}
		}
	}
	portsDialogSyncSelected(dialog)
}

func portsDialogSelectedItem(dialog *portsDialogState) (portsDialogItem, bool) {
	if dialog == nil || len(dialog.Items) == 0 {
		return portsDialogItem{}, false
	}
	portsDialogSyncSelected(dialog)
	return dialog.Items[dialog.Selected], true
}

func portsDialogItemKey(item portsDialogItem) string {
	projectPath := normalizeProjectPath(item.ProjectPath)
	pid := item.Snapshot.PID
	if pid > 0 {
		return projectPath + "\x00" + strconv.Itoa(pid) + "\x00" + strconv.Itoa(item.Port)
	}
	return projectPath + "\x00" + strings.TrimSpace(item.Snapshot.Command) + "\x00" + strconv.Itoa(item.Port)
}

func portsDialogItemCanStop(item portsDialogItem) bool {
	return item.External && item.Snapshot.External && item.Snapshot.PID > 0 && !item.ManagedRuntime
}

func portsDialogCommandLabel(item portsDialogItem) string {
	command := strings.TrimSpace(item.Snapshot.Command)
	if command == "" {
		return "(command unknown)"
	}
	return command
}

func portsDialogItemStateLabel(item portsDialogItem) string {
	parts := []string{}
	if item.PortConflict {
		parts = append(parts, "conflict")
	}
	if item.Orphaned {
		parts = append(parts, "orphaned")
	}
	if item.ManagedRuntime {
		parts = append(parts, "managed")
	} else if item.External {
		parts = append(parts, "external")
	}
	if len(parts) == 0 {
		return "listener"
	}
	return strings.Join(parts, " ")
}

func portsDialogItemStateStyle(item portsDialogItem) lipgloss.Style {
	switch {
	case item.PortConflict:
		return detailDangerStyle
	case item.Orphaned:
		return detailWarningStyle
	case item.ManagedRuntime:
		return detailValueStyle
	default:
		return detailMutedStyle
	}
}

func portsDialogItemReasons(item portsDialogItem) []string {
	if len(item.Reasons) > 0 {
		return item.Reasons
	}
	return []string{portsDialogItemStateLabel(item)}
}

func portsDialogCountLabel(items []portsDialogItem) string {
	total := len(items)
	if total == 0 {
		return "No project-local TCP listeners"
	}
	conflicts := 0
	orphaned := 0
	external := 0
	for _, item := range items {
		if item.PortConflict {
			conflicts++
		}
		if item.Orphaned {
			orphaned++
		}
		if item.External && !item.ManagedRuntime {
			external++
		}
	}
	base := formatCount(total, "TCP listener")
	qualifiers := []string{}
	if conflicts > 0 {
		qualifiers = append(qualifiers, formatCount(conflicts, "conflict"))
	}
	if orphaned > 0 {
		qualifiers = append(qualifiers, formatCount(orphaned, "orphaned"))
	}
	if external > 0 {
		qualifiers = append(qualifiers, formatCount(external, "external listener"))
	}
	if len(qualifiers) == 0 {
		return base
	}
	return base + " (" + strings.Join(qualifiers, ", ") + ")"
}

func portsDialogReadyStatus(count int) string {
	if count == 1 {
		return "Ports inspector found 1 TCP listener"
	}
	return fmt.Sprintf("Ports inspector found %d TCP listeners", count)
}

func portsForFinding(finding procinspect.Finding) []int {
	return dedupeSortedPortsForDialog(append(append([]int(nil), finding.Ports...), finding.ConflictPorts...))
}

func processInstanceHasPortConflict(findings []procinspect.Finding, pid, port int) bool {
	if pid <= 0 || port <= 0 {
		return false
	}
	for _, finding := range findings {
		if finding.PID != pid {
			continue
		}
		if finding.PortConflict && (intSliceContains(finding.ConflictPorts, port) || intSliceContains(finding.Ports, port)) {
			return true
		}
	}
	return false
}

func dedupeSortedPortsForDialog(values []int) []int {
	if len(values) == 0 {
		return nil
	}
	sort.Ints(values)
	out := values[:0]
	last := -1
	for _, value := range values {
		if value <= 0 {
			continue
		}
		if len(out) > 0 && value == last {
			continue
		}
		out = append(out, value)
		last = value
	}
	return append([]int(nil), out...)
}

func appendUniquePortDialogStrings(values []string, additions ...string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values)+len(additions))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	for _, value := range additions {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func (m Model) projectSummaryForPortsItem(item portsDialogItem) model.ProjectSummary {
	projectPath := normalizeProjectPath(item.ProjectPath)
	for _, project := range append(append([]model.ProjectSummary(nil), m.allProjects...), m.projects...) {
		if normalizeProjectPath(project.Path) == projectPath {
			return project
		}
	}
	return model.ProjectSummary{
		Name:          filepath.Base(projectPath),
		Path:          projectPath,
		PresentOnDisk: true,
	}
}

func (m Model) portsDialogProjectLabels(paths []string) string {
	labels := make([]string, 0, len(paths))
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
		labels = append(labels, m.runtimeOwnerLabel(path))
	}
	sort.Strings(labels)
	return strings.Join(labels, ", ")
}
