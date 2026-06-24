package codexapp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"lcroom/internal/procinspect"
	"lcroom/internal/projectrun"
)

const (
	runtimePromptContextTimeout = 900 * time.Millisecond
	runtimePromptContextLimit   = 8
)

func augmentSubmissionWithRuntimeContext(input Submission, manager *projectrun.Manager, projectPath string) Submission {
	contextText := buildRuntimePromptContext(manager, projectPath)
	if contextText == "" {
		return input
	}
	originalText := strings.TrimSpace(input.Text)
	if originalText == "" {
		return input
	}
	input.Text = contextText + "\n\nUser request:\n" + originalText
	if strings.TrimSpace(input.DisplayText) == "" {
		input.DisplayText = originalText
	}
	return input
}

func buildRuntimePromptContext(manager *projectrun.Manager, projectPath string) string {
	projectPath = filepath.Clean(strings.TrimSpace(projectPath))
	if manager == nil || projectPath == "" || projectPath == "." {
		return ""
	}

	managed := runningRuntimeSnapshots(manager.SnapshotsForProject(projectPath))
	observed := observedProjectListeners(manager, projectPath)
	if len(managed) == 0 && len(observed) == 0 {
		return ""
	}

	lines := []string{
		"Little Control Room runtime context for this project:",
		"- Reuse an existing matching long-running app/server/watch process by default.",
		"- Start another copy of the same command/cwd only when a parallel instance is truly needed; state that intent before launching it.",
	}
	for i, snapshot := range managed {
		if i >= runtimePromptContextLimit {
			lines = append(lines, fmt.Sprintf("- managed runtimes omitted: %d more", len(managed)-i))
			break
		}
		lines = append(lines, "- managed runtime: "+runtimeSnapshotPromptLine(projectPath, snapshot))
	}
	remaining := runtimePromptContextLimit - minInt(len(managed), runtimePromptContextLimit)
	for i, instance := range observed {
		if remaining <= 0 {
			lines = append(lines, fmt.Sprintf("- observed listeners omitted: %d more", len(observed)-i))
			break
		}
		lines = append(lines, "- observed listener: "+observedProcessPromptLine(projectPath, instance))
		remaining--
	}
	return strings.Join(lines, "\n")
}

func runningRuntimeSnapshots(snapshots []projectrun.Snapshot) []projectrun.Snapshot {
	out := []projectrun.Snapshot{}
	for _, snapshot := range snapshots {
		if snapshot.Running {
			out = append(out, snapshot)
		}
	}
	return out
}

func observedProjectListeners(manager *projectrun.Manager, projectPath string) []procinspect.ProjectInstance {
	managedPIDs, managedPGIDs := managedRuntimeProcessSets(manager.Snapshots())
	ctx, cancel := context.WithTimeout(context.Background(), runtimePromptContextTimeout)
	defer cancel()
	reports, err := procinspect.ScanProjects(ctx, procinspect.ScanOptions{
		ProjectPaths: projectPathsForRuntimePrompt(projectPath),
		ManagedPIDs:  managedPIDs,
		ManagedPGIDs: managedPGIDs,
		OwnPID:       os.Getpid(),
	})
	if err != nil || len(reports) == 0 {
		return nil
	}
	for _, report := range reports {
		if filepath.Clean(report.ProjectPath) == projectPath {
			return report.Instances
		}
	}
	return nil
}

func projectPathsForRuntimePrompt(projectPath string) []string {
	projectPath = filepath.Clean(strings.TrimSpace(projectPath))
	if projectPath == "" || projectPath == "." {
		return nil
	}
	return []string{projectPath}
}

func managedRuntimeProcessSets(snapshots []projectrun.Snapshot) (map[int]struct{}, map[int]struct{}) {
	pids := map[int]struct{}{}
	pgids := map[int]struct{}{}
	for _, snapshot := range snapshots {
		if !snapshot.Running {
			continue
		}
		if snapshot.PID > 0 {
			pids[snapshot.PID] = struct{}{}
		}
		if snapshot.PGID > 0 {
			pgids[snapshot.PGID] = struct{}{}
		}
	}
	return pids, pgids
}

func runtimeSnapshotPromptLine(projectPath string, snapshot projectrun.Snapshot) string {
	parts := []string{}
	if id := strings.TrimSpace(snapshot.ID); id != "" {
		parts = append(parts, "id "+id)
	}
	if name := strings.TrimSpace(snapshot.Name); name != "" {
		parts = append(parts, "name "+name)
	}
	if command := compactRuntimePromptValue(snapshot.Command, 140); command != "" {
		parts = append(parts, "command "+strconv.Quote(command))
	}
	if cwd := relativeRuntimePromptCWD(projectPath, snapshot.CWD); cwd != "" {
		parts = append(parts, "cwd "+cwd)
	}
	if snapshot.PID > 0 {
		parts = append(parts, fmt.Sprintf("pid %d", snapshot.PID))
	}
	if len(snapshot.Ports) > 0 {
		parts = append(parts, "ports "+joinRuntimePromptInts(snapshot.Ports))
	}
	if url := runtimePromptURL(snapshot); url != "" {
		parts = append(parts, "url "+url)
	}
	if len(parts) == 0 {
		return "running"
	}
	return strings.Join(parts, "; ")
}

func observedProcessPromptLine(projectPath string, instance procinspect.ProjectInstance) string {
	process := instance.Process
	parts := []string{}
	if process.PID > 0 {
		parts = append(parts, fmt.Sprintf("pid %d", process.PID))
	}
	if command := compactRuntimePromptValue(process.Command, 140); command != "" {
		parts = append(parts, "command "+strconv.Quote(command))
	}
	if cwd := relativeRuntimePromptCWD(projectPath, process.CWD); cwd != "" {
		parts = append(parts, "cwd "+cwd)
	}
	if len(process.Ports) > 0 {
		parts = append(parts, "ports "+joinRuntimePromptInts(process.Ports))
	}
	if process.PPID == 1 {
		parts = append(parts, "orphaned under PID 1")
	}
	if len(parts) == 0 {
		return "project-local listener"
	}
	return strings.Join(parts, "; ")
}

func runtimePromptURL(snapshot projectrun.Snapshot) string {
	if len(snapshot.AnnouncedURLs) > 0 {
		return strings.TrimSpace(snapshot.AnnouncedURLs[0])
	}
	if len(snapshot.Ports) > 0 {
		return fmt.Sprintf("http://127.0.0.1:%d/", snapshot.Ports[0])
	}
	return ""
}

func relativeRuntimePromptCWD(projectPath, cwd string) string {
	projectPath = filepath.Clean(strings.TrimSpace(projectPath))
	cwd = filepath.Clean(strings.TrimSpace(cwd))
	if cwd == "" || cwd == "." || projectPath == "" || projectPath == "." {
		return ""
	}
	rel, err := filepath.Rel(projectPath, cwd)
	if err != nil || rel == "." {
		return ""
	}
	if strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." {
		return cwd
	}
	return rel
}

func compactRuntimePromptValue(value string, limit int) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if limit <= 0 || len(value) <= limit {
		return value
	}
	if limit <= 3 {
		return value[:limit]
	}
	return value[:limit-3] + "..."
}

func joinRuntimePromptInts(values []int) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		if value > 0 {
			parts = append(parts, strconv.Itoa(value))
		}
	}
	return strings.Join(parts, ",")
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
