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
	if savedCommand == "" && strings.TrimSpace(snapshot.Command) == "" && !snapshot.Running && len(snapshot.RecentOutput) == 0 && len(snapshot.Ports) == 0 && strings.TrimSpace(snapshot.LastError) == "" {
		return lines
	}

	commandValue := savedCommand
	if commandValue == "" {
		commandValue = strings.TrimSpace(snapshot.Command)
	}
	if commandValue == "" {
		commandValue = detailMutedStyle.Render("not set")
	} else {
		commandValue = detailValueStyle.Render(commandValue)
	}
	lines = append(lines, detailField("Run cmd", commandValue))

	statusValue := detailMutedStyle.Render("idle")
	if snapshot.Running {
		statusValue = detailValueStyle.Render("running")
	} else if snapshot.ExitCodeKnown {
		if snapshot.ExitCode == 0 {
			statusValue = detailMutedStyle.Render("exited")
		} else {
			statusValue = detailDangerStyle.Render(fmt.Sprintf("exit %d", snapshot.ExitCode))
		}
	} else if strings.TrimSpace(snapshot.LastError) != "" {
		statusValue = detailDangerStyle.Render("failed")
	}

	fields := []string{detailField("Runtime", statusValue)}
	if snapshot.Running && !snapshot.StartedAt.IsZero() {
		fields = append(fields, detailField("Up", detailValueStyle.Render(formatRunningDuration(m.currentTime().Sub(snapshot.StartedAt)))))
	} else if !snapshot.ExitedAt.IsZero() {
		fields = append(fields, detailField("Stopped", detailMutedStyle.Render(snapshot.ExitedAt.Format(timeFieldFormat))))
	}
	lines = appendDetailFields(lines, width, fields...)

	if len(snapshot.Ports) > 0 {
		lines = append(lines, detailField("Ports", detailValueStyle.Render(joinPorts(snapshot.Ports))))
	}
	if len(snapshot.AnnouncedURLs) > 0 {
		lines = append(lines, detailField("URLs", detailValueStyle.Render(strings.Join(snapshot.AnnouncedURLs, ", "))))
	}
	if len(snapshot.ConflictPorts) > 0 {
		lines = append(lines, detailField("Conflict", detailDangerStyle.Render(m.runtimeConflictSummary(projectPath, snapshot.ConflictPorts))))
	}
	if strings.TrimSpace(snapshot.LastError) != "" {
		lines = append(lines, detailField("Runtime err", detailDangerStyle.Render(snapshot.LastError)))
	}
	if len(snapshot.RecentOutput) > 0 {
		lines = append(lines, detailSectionStyle.Render("Runtime output"))
		for _, line := range snapshot.RecentOutput {
			lines = append(lines, renderWrappedDetailBullet(detailMutedStyle, width, line))
		}
	}
	return lines
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
