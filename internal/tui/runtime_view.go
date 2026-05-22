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
	projectPath = filepath.Clean(strings.TrimSpace(projectPath))
	if projectPath == "." {
		projectPath = ""
	}
	if snapshot, ok := m.runtimeSnapshots[projectPath]; ok {
		return snapshot
	}
	return projectrun.Snapshot{ProjectPath: projectPath}
}

func (m Model) runningRuntimeCount() int {
	count := 0
	for _, snapshot := range m.runtimeSnapshots {
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

func (m Model) runtimeConflictSummary(projectPath string, ports []int) string {
	if len(ports) == 0 {
		return ""
	}
	portSet := map[int]struct{}{}
	for _, port := range ports {
		portSet[port] = struct{}{}
	}
	owners := map[string]struct{}{}
	for _, snapshot := range m.runtimeSnapshots {
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
