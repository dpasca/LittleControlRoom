package tui

import (
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"lcroom/internal/projectrun"
)

func (m Model) projectRuntimeSnapshot(projectPath string) projectrun.Snapshot {
	if m.runtimeManager == nil {
		return projectrun.Snapshot{ProjectPath: strings.TrimSpace(projectPath)}
	}
	snapshot, err := m.runtimeManager.Snapshot(projectPath)
	if err != nil {
		return projectrun.Snapshot{ProjectPath: strings.TrimSpace(projectPath)}
	}
	return snapshot
}

func (m Model) renderRuntimeDetail(lines []string, width int, projectPath, savedCommand string) []string {
	savedCommand = strings.TrimSpace(savedCommand)
	snapshot := m.projectRuntimeSnapshot(projectPath)
	if !runtimeDetailAvailable(savedCommand, snapshot) {
		return lines
	}

	commandValue := effectiveRuntimeCommand(savedCommand, snapshot)
	if commandValue == "" {
		commandValue = detailMutedStyle.Render("not set")
	} else {
		commandValue = detailValueStyle.Render(commandValue)
	}
	lines = append(lines, detailField("Run cmd", commandValue))

	fields := []string{detailField("Runtime", renderRuntimeStatusValue(snapshot))}
	if snapshot.Running && !snapshot.StartedAt.IsZero() {
		fields = append(fields, detailField("Up", detailValueStyle.Render(formatRunningDuration(m.currentTime().Sub(snapshot.StartedAt)))))
	} else if !snapshot.ExitedAt.IsZero() {
		fields = append(fields, detailField("Stopped", detailMutedStyle.Render(snapshot.ExitedAt.Format(timeFieldFormat))))
	}
	lines = appendDetailFields(lines, width, fields...)

	if len(snapshot.Ports) > 0 {
		lines = append(lines, detailField("Ports", detailValueStyle.Render(joinPorts(snapshot.Ports))))
	}
	if urlSummary := runtimeURLSummary(snapshot); urlSummary != "" {
		lines = append(lines, detailField("URL", detailValueStyle.Render(urlSummary)))
	}
	if len(snapshot.ConflictPorts) > 0 {
		lines = append(lines, detailField("Conflict", detailDangerStyle.Render(m.runtimeConflictSummary(projectPath, snapshot.ConflictPorts))))
	}
	if strings.TrimSpace(snapshot.LastError) != "" {
		lines = append(lines, detailField("Runtime err", detailDangerStyle.Render(snapshot.LastError)))
	}
	if outputSummary := runtimeOutputSummary(snapshot, width); outputSummary != "" {
		lines = append(lines, detailField("Output", detailMutedStyle.Render(outputSummary)))
	}
	return lines
}

func runtimeDetailAvailable(savedCommand string, snapshot projectrun.Snapshot) bool {
	return strings.TrimSpace(savedCommand) != "" ||
		strings.TrimSpace(snapshot.Command) != "" ||
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
	savedCommand = strings.TrimSpace(savedCommand)
	if savedCommand != "" {
		return savedCommand
	}
	return strings.TrimSpace(snapshot.Command)
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

func runtimeOutputSummary(snapshot projectrun.Snapshot, width int) string {
	if len(snapshot.RecentOutput) == 0 {
		return ""
	}
	count := len(snapshot.RecentOutput)
	label := "lines"
	if count == 1 {
		label = "line"
	}
	last := strings.TrimSpace(snapshot.RecentOutput[count-1])
	if last == "" {
		last = "(blank line)"
	}
	last = truncateText(last, max(18, width-36))
	return fmt.Sprintf("%d %s captured; last: %s  (r or /runtime)", count, label, last)
}

func renderRuntimeStatusValue(snapshot projectrun.Snapshot) string {
	statusStyle := detailMutedStyle
	statusText := "idle"
	switch {
	case snapshot.Running:
		statusStyle = detailValueStyle
		statusText = "running"
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

func (m Model) runtimeConflictSummary(projectPath string, ports []int) string {
	if len(ports) == 0 || m.runtimeManager == nil {
		return ""
	}
	snapshots := m.runtimeManager.Snapshots()
	portSet := map[int]struct{}{}
	for _, port := range ports {
		portSet[port] = struct{}{}
	}
	owners := map[string]struct{}{}
	for _, snapshot := range snapshots {
		if snapshot.ProjectPath == projectPath {
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
