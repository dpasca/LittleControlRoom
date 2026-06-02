package tui

import (
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"lcroom/internal/procinspect"
	"lcroom/internal/projectrun"
)

func (m Model) projectRuntimeSnapshot(projectPath string) projectrun.Snapshot {
	projectPath = filepath.Clean(strings.TrimSpace(projectPath))
	if projectPath == "." {
		projectPath = ""
	}
	if snapshot, ok := m.runtimeSnapshots[projectPath]; ok {
		return snapshot
	}
	return projectrun.Snapshot{ProjectPath: projectPath}
}

func (m Model) projectRuntimeSnapshots(projectPath string) []projectrun.Snapshot {
	projectPath = filepath.Clean(strings.TrimSpace(projectPath))
	if projectPath == "." || projectPath == "" {
		return nil
	}
	if len(m.runtimeProcessSnapshots) == 0 {
		if snapshot, ok := m.runtimeSnapshots[projectPath]; ok && runtimeDetailAvailable("", snapshot) {
			return []projectrun.Snapshot{snapshot}
		}
		return nil
	}
	out := make([]projectrun.Snapshot, 0)
	for _, snapshot := range m.runtimeProcessSnapshots {
		if filepath.Clean(strings.TrimSpace(snapshot.ProjectPath)) != projectPath {
			continue
		}
		out = append(out, snapshot)
	}
	return out
}

func (m Model) projectLocalInstanceSnapshots(projectPath string) []projectrun.Snapshot {
	report, ok := m.projectProcessReport(projectPath)
	if !ok || len(report.Instances) == 0 {
		return nil
	}
	out := make([]projectrun.Snapshot, 0, len(report.Instances))
	for _, instance := range report.Instances {
		snapshot := localInstanceRuntimeSnapshot(instance)
		if snapshot.ProjectPath == "" || len(snapshot.Ports) == 0 {
			continue
		}
		out = append(out, snapshot)
	}
	sort.SliceStable(out, func(i, j int) bool {
		leftPort, rightPort := firstRuntimeSnapshotPort(out[i]), firstRuntimeSnapshotPort(out[j])
		if leftPort != rightPort {
			return leftPort < rightPort
		}
		return out[i].PID < out[j].PID
	})
	return out
}

func (m Model) projectVisibleLocalInstanceSnapshots(projectPath string) []projectrun.Snapshot {
	snapshots := m.projectLocalInstanceSnapshots(projectPath)
	if len(snapshots) == 0 {
		return nil
	}
	report, ok := m.projectProcessReport(projectPath)
	if !ok || len(report.Findings) == 0 {
		return snapshots
	}
	findingPIDs := make(map[int]struct{}, len(report.Findings))
	for _, finding := range report.Findings {
		if finding.PID > 0 {
			findingPIDs[finding.PID] = struct{}{}
		}
	}
	out := snapshots[:0]
	for _, snapshot := range snapshots {
		if snapshot.PID > 0 {
			if _, ok := findingPIDs[snapshot.PID]; ok {
				continue
			}
		}
		out = append(out, snapshot)
	}
	return out
}

func (m Model) projectPrimaryLocalInstanceSnapshot(projectPath string) (projectrun.Snapshot, bool) {
	snapshots := m.projectLocalInstanceSnapshots(projectPath)
	if len(snapshots) == 0 {
		return projectrun.Snapshot{}, false
	}
	return snapshots[0], true
}

func (m Model) projectRuntimeContextSnapshot(projectPath string) projectrun.Snapshot {
	snapshot := m.projectRuntimeSnapshot(projectPath)
	if snapshot.Running {
		return snapshot
	}
	if localSnapshot, ok := m.projectPrimaryLocalInstanceSnapshot(projectPath); ok {
		return localSnapshot
	}
	return snapshot
}

func (m Model) projectLocalInstanceSummary(projectPath string) string {
	snapshots := m.projectVisibleLocalInstanceSnapshots(projectPath)
	if len(snapshots) == 0 {
		return ""
	}
	snapshot := snapshots[0]
	label := localInstanceDisplayLabel(snapshot)
	if url := runtimePrimaryURL(snapshot); url != "" {
		label += " at " + url
	} else if len(snapshot.Ports) > 0 {
		label += " on " + joinPorts(snapshot.Ports)
	}
	if len(snapshots) > 1 {
		label += fmt.Sprintf(" (+%d more)", len(snapshots)-1)
	}
	return strings.TrimSpace(label)
}

func localInstanceRuntimeSnapshot(instance procinspect.ProjectInstance) projectrun.Snapshot {
	projectPath := normalizeProjectPath(instance.ProjectPath)
	if projectPath == "" {
		return projectrun.Snapshot{}
	}
	ports := append([]int(nil), instance.Ports...)
	sort.Ints(ports)
	return projectrun.Snapshot{
		ID:          fmt.Sprintf("pid_%d", instance.PID),
		Name:        "local listener",
		External:    true,
		ProjectPath: projectPath,
		Command:     strings.TrimSpace(instance.Command),
		CWD:         strings.TrimSpace(instance.CWD),
		PID:         instance.PID,
		PGID:        instance.PGID,
		Running:     true,
		Ports:       ports,
	}
}

func firstRuntimeSnapshotPort(snapshot projectrun.Snapshot) int {
	if len(snapshot.Ports) == 0 {
		return 0
	}
	return snapshot.Ports[0]
}

func (m Model) selectedRuntimeProcessSnapshot(projectPath string) (projectrun.Snapshot, int, int) {
	snapshots := m.projectRuntimeSnapshots(projectPath)
	if len(snapshots) == 0 {
		return m.projectRuntimeSnapshot(projectPath), 0, 0
	}
	selectedID := ""
	if m.runtimeProcessSelected != nil {
		selectedID = strings.TrimSpace(m.runtimeProcessSelected[filepath.Clean(strings.TrimSpace(projectPath))])
	}
	for i, snapshot := range snapshots {
		if selectedID != "" && strings.TrimSpace(snapshot.ID) == selectedID {
			return snapshot, i, len(snapshots)
		}
	}
	primary := m.projectRuntimeSnapshot(projectPath)
	for i, snapshot := range snapshots {
		if strings.TrimSpace(snapshot.ID) != "" && strings.TrimSpace(snapshot.ID) == strings.TrimSpace(primary.ID) {
			return snapshot, i, len(snapshots)
		}
	}
	return snapshots[0], 0, len(snapshots)
}

func (m *Model) selectRuntimeProcess(delta int) {
	projectPath := filepath.Clean(strings.TrimSpace(m.runtimePanelProjectPath()))
	if projectPath == "" || delta == 0 {
		return
	}
	snapshots := m.projectRuntimeSnapshots(projectPath)
	if len(snapshots) <= 1 {
		m.status = "Only one managed process for this project"
		return
	}
	_, index, _ := m.selectedRuntimeProcessSnapshot(projectPath)
	next := (index + delta) % len(snapshots)
	if next < 0 {
		next += len(snapshots)
	}
	if m.runtimeProcessSelected == nil {
		m.runtimeProcessSelected = make(map[string]string)
	}
	m.runtimeProcessSelected[projectPath] = strings.TrimSpace(snapshots[next].ID)
	m.status = "Selected runtime process " + runtimeProcessLabel(snapshots[next])
	m.syncRuntimeViewport(true)
}

func (m Model) runningRuntimeCount() int {
	count := 0
	snapshots := m.runtimeProcessSnapshots
	if len(snapshots) == 0 {
		for _, snapshot := range m.runtimeSnapshots {
			snapshots = append(snapshots, snapshot)
		}
	}
	for _, snapshot := range snapshots {
		if snapshot.Running {
			count++
		}
	}
	return count
}

func (m Model) renderFooterRuntimeSegment() string {
	count := m.runningRuntimeCount()
	if count == 0 {
		return ""
	}
	label := "1 runtime active"
	if count != 1 {
		label = fmt.Sprintf("%d runtimes active", count)
	}
	return renderFooterStatus(label)
}

func runtimeDetailAvailable(savedCommand string, snapshot projectrun.Snapshot) bool {
	return strings.TrimSpace(savedCommand) != "" ||
		snapshot.External ||
		strings.TrimSpace(snapshot.Command) != "" ||
		strings.TrimSpace(snapshot.CWD) != "" ||
		snapshot.Running ||
		snapshot.ExitCodeKnown ||
		!snapshot.ExitedAt.IsZero() ||
		len(snapshot.Ports) > 0 ||
		len(snapshot.ConflictPorts) > 0 ||
		len(snapshot.AnnouncedURLs) > 0 ||
		len(snapshot.RecentOutput) > 0 ||
		strings.TrimSpace(snapshot.LastError) != ""
}

func effectiveRuntimeCommand(savedCommand string, snapshot projectrun.Snapshot) string {
	if command := strings.TrimSpace(snapshot.Command); command != "" {
		return command
	}
	return strings.TrimSpace(savedCommand)
}

func runtimeRelativeCWD(projectPath, cwd string) string {
	projectPath = filepath.Clean(strings.TrimSpace(projectPath))
	cwd = filepath.Clean(strings.TrimSpace(cwd))
	if cwd == "" || cwd == "." || cwd == projectPath {
		return ""
	}
	rel, err := filepath.Rel(projectPath, cwd)
	if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." || filepath.IsAbs(rel) {
		return cwd
	}
	return rel
}

func runtimePrimaryURL(snapshot projectrun.Snapshot) string {
	if len(snapshot.AnnouncedURLs) > 0 {
		return strings.TrimSpace(snapshot.AnnouncedURLs[0])
	}
	if len(snapshot.Ports) > 0 {
		return fmt.Sprintf("http://127.0.0.1:%d/", snapshot.Ports[0])
	}
	return ""
}

func runtimeURLSummary(snapshot projectrun.Snapshot) string {
	primary := runtimePrimaryURL(snapshot)
	if primary == "" {
		return ""
	}
	if len(snapshot.AnnouncedURLs) <= 1 {
		return primary
	}
	return fmt.Sprintf("%s (+%d more)", primary, len(snapshot.AnnouncedURLs)-1)
}

func runtimeProcessLabel(snapshot projectrun.Snapshot) string {
	id := strings.TrimSpace(snapshot.ID)
	if id == "" {
		id = "default"
	}
	label := id
	if name := strings.TrimSpace(snapshot.Name); name != "" {
		label += " " + name
	} else if snapshot.Default {
		label += " default"
	}
	if snapshot.PID > 0 {
		label += fmt.Sprintf(" pid %d", snapshot.PID)
	}
	return strings.TrimSpace(label)
}

func renderRuntimeStatusValue(snapshot projectrun.Snapshot) string {
	statusStyle := detailMutedStyle
	statusText := "idle"
	switch {
	case snapshot.Running:
		statusStyle = detailValueStyle
		statusText = "running"
	case !snapshot.ExitedAt.IsZero() && !snapshot.ExitCodeKnown && strings.TrimSpace(snapshot.LastError) == "":
		statusText = "stopped"
	case snapshot.ExitCodeKnown:
		if snapshot.ExitCode == 0 {
			statusText = "exited"
		} else {
			statusStyle = detailDangerStyle
			statusText = fmt.Sprintf("exit %d", snapshot.ExitCode)
		}
	case strings.TrimSpace(snapshot.LastError) != "":
		statusStyle = detailDangerStyle
		statusText = "failed"
	}
	return statusStyle.Render(statusText)
}

func joinPorts(ports []int) string {
	if len(ports) == 0 {
		return ""
	}
	values := make([]string, 0, len(ports))
	for _, port := range ports {
		values = append(values, strconv.Itoa(port))
	}
	return strings.Join(values, ", ")
}

func (m Model) runtimeConflictSummary(projectPath, processID string, ports []int) string {
	if len(ports) == 0 {
		return ""
	}
	portSet := map[int]struct{}{}
	for _, port := range ports {
		portSet[port] = struct{}{}
	}
	owners := map[string]struct{}{}
	snapshots := m.runtimeProcessSnapshots
	if len(snapshots) == 0 {
		for _, snapshot := range m.runtimeSnapshots {
			snapshots = append(snapshots, snapshot)
		}
	}
	for _, snapshot := range snapshots {
		if snapshot.ProjectPath == projectPath && strings.TrimSpace(snapshot.ID) == strings.TrimSpace(processID) {
			continue
		}
		for _, port := range snapshot.Ports {
			if _, ok := portSet[port]; ok {
				owners[m.runtimeOwnerLabel(snapshot.ProjectPath)] = struct{}{}
				break
			}
		}
	}
	names := make([]string, 0, len(owners))
	for name := range owners {
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) == 0 {
		return "ports already claimed"
	}
	return fmt.Sprintf("%s with %s", joinPorts(ports), strings.Join(names, ", "))
}

func (m Model) runtimeOwnerLabel(projectPath string) string {
	projectPath = filepath.Clean(strings.TrimSpace(projectPath))
	for _, project := range m.allProjects {
		if filepath.Clean(project.Path) == projectPath && strings.TrimSpace(project.Name) != "" {
			return project.Name
		}
	}
	return filepath.Base(projectPath)
}

const timeFieldFormat = "2006-01-02 15:04:05"
