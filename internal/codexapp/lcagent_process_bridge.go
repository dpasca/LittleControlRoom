package codexapp

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"

	"lcroom/internal/lcagent/tools"
	"lcroom/internal/projectrun"
)

type lcagentManagedProcessRequest struct {
	ID              string
	Action          string
	ProjectPath     string
	ProcessID       string
	Name            string
	Command         string
	CWD             string
	CreateNew       bool
	ReplaceExisting bool
}

type lcagentProcessBridge struct {
	manager          *projectrun.Manager
	projectPath      string
	stdin            io.Writer
	appendAsync      func(TranscriptKind, string)
	watchProcessExit func(projectrun.Snapshot)
}

func (b lcagentProcessBridge) handle(request lcagentManagedProcessRequest) {
	if b.stdin == nil {
		b.append(TranscriptError, "LCAgent managed process response failed: process channel is not available")
		return
	}
	result := b.run(request)
	payload, err := json.Marshal(map[string]any{
		"type":   "process_response",
		"id":     request.ID,
		"result": result,
	})
	if err != nil {
		b.append(TranscriptError, "LCAgent managed process response failed: "+err.Error())
		return
	}
	if _, err := fmt.Fprintln(b.stdin, string(payload)); err != nil {
		b.append(TranscriptError, "LCAgent managed process response failed: "+err.Error())
		return
	}
	if result.Success {
		b.append(TranscriptStatus, result.Output)
	} else {
		b.append(TranscriptError, firstNonEmpty(result.Error, "LCAgent managed process request failed"))
	}
}

func (b lcagentProcessBridge) run(request lcagentManagedProcessRequest) tools.ToolResult {
	if b.manager == nil {
		return tools.ToolResult{Success: false, Error: "runtime manager unavailable"}
	}
	projectPath, err := lcagentResolveManagedProcessProjectPath(b.projectPath, request.ProjectPath)
	if err != nil {
		return tools.ToolResult{Success: false, Error: err.Error(), CWD: strings.TrimSpace(request.CWD)}
	}
	switch strings.TrimSpace(request.Action) {
	case "start":
		command := strings.TrimSpace(request.Command)
		if command == "" {
			return tools.ToolResult{Success: false, Error: "managed process command is required"}
		}
		if request.CreateNew && request.ReplaceExisting {
			return tools.ToolResult{Success: false, Error: "create_new and replace_existing cannot both be true", Command: command, CWD: strings.TrimSpace(request.CWD)}
		}
		cwd, err := lcagentNormalizeManagedProcessCWD(projectPath, request.CWD)
		if err != nil {
			return tools.ToolResult{Success: false, Error: err.Error(), Command: command, CWD: strings.TrimSpace(request.CWD)}
		}
		result, err := b.manager.StartManaged(projectrun.StartRequest{
			ProjectPath:     projectPath,
			Command:         command,
			CWD:             cwd,
			Name:            strings.TrimSpace(request.Name),
			CreateNew:       true,
			ReuseMatching:   !request.CreateNew,
			ReplaceExisting: request.ReplaceExisting,
		})
		if err != nil {
			return tools.ToolResult{Success: false, Error: err.Error(), Command: command, CWD: cwd}
		}
		prefix := "Started managed process"
		switch result.Disposition {
		case projectrun.StartDispositionReused:
			prefix = "Managed process already running"
		case projectrun.StartDispositionReplaced:
			prefix = fmt.Sprintf("Replaced %d matching managed process", result.ReplacedCount)
			if result.ReplacedCount != 1 {
				prefix += "es"
			}
		}
		if b.watchProcessExit != nil {
			b.watchProcessExit(result.Snapshot)
		}
		return lcagentManagedProcessResult(prefix, result.Snapshot, true)
	case "list":
		return lcagentManagedProcessListResult(b.manager.SnapshotsForProject(projectPath))
	case "stop":
		processID := strings.TrimSpace(request.ProcessID)
		err := b.manager.StopProcess(projectPath, processID)
		if errors.Is(err, projectrun.ErrNotRunning) {
			return tools.ToolResult{Success: true, Output: "No matching managed process is running for this workspace."}
		}
		if err != nil {
			return tools.ToolResult{Success: false, Error: err.Error()}
		}
		if processID != "" {
			return tools.ToolResult{Success: true, Output: "Stopping managed process " + processID + " for this workspace."}
		}
		return tools.ToolResult{Success: true, Output: "Stopping selected managed process for this workspace."}
	default:
		return tools.ToolResult{Success: false, Error: "unsupported managed process action: " + request.Action}
	}
}

func lcagentResolveManagedProcessProjectPath(sessionProjectPath, requestedProjectPath string) (string, error) {
	sessionProjectPath = strings.TrimSpace(sessionProjectPath)
	if sessionProjectPath == "" {
		return "", errors.New("project path is required")
	}
	sessionProjectPath = filepath.Clean(sessionProjectPath)
	requestedProjectPath = strings.TrimSpace(requestedProjectPath)
	if requestedProjectPath == "" {
		return sessionProjectPath, nil
	}
	if !filepath.IsAbs(requestedProjectPath) {
		requestedProjectPath = filepath.Join(sessionProjectPath, requestedProjectPath)
	}
	requestedProjectPath = filepath.Clean(requestedProjectPath)
	sessionCanon, err := filepath.EvalSymlinks(sessionProjectPath)
	if err != nil {
		return "", fmt.Errorf("resolve session project path: %w", err)
	}
	targetCanon, err := filepath.EvalSymlinks(requestedProjectPath)
	if err != nil {
		return "", fmt.Errorf("resolve managed process project_path: %w", err)
	}
	if targetCanon == sessionCanon || filepath.Dir(targetCanon) == filepath.Dir(sessionCanon) {
		return targetCanon, nil
	}
	return "", fmt.Errorf("managed process project_path must be the session workspace or a sibling project: %s", requestedProjectPath)
}

func lcagentNormalizeManagedProcessCWD(projectPath, cwd string) (string, error) {
	projectPath = strings.TrimSpace(projectPath)
	if projectPath == "" {
		return "", errors.New("project path is required")
	}
	projectPath = filepath.Clean(projectPath)
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return projectPath, nil
	}
	if !filepath.IsAbs(cwd) {
		cwd = filepath.Join(projectPath, cwd)
	}
	cwd = filepath.Clean(cwd)
	rel, err := filepath.Rel(projectPath, cwd)
	if err != nil {
		return "", fmt.Errorf("resolve runtime cwd: %w", err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("runtime cwd must stay inside project: %s", cwd)
	}
	return cwd, nil
}

func (b lcagentProcessBridge) append(kind TranscriptKind, text string) {
	if b.appendAsync == nil || strings.TrimSpace(text) == "" {
		return
	}
	b.appendAsync(kind, text)
}

func lcagentProcessRequestStatus(action string) string {
	switch strings.TrimSpace(action) {
	case "start":
		return "Starting managed process"
	case "list":
		return "Listing managed processes"
	case "stop":
		return "Stopping managed process"
	default:
		return "Handling managed process request"
	}
}

func lcagentProcessRequestText(action, command, cwd, projectPath string) string {
	switch strings.TrimSpace(action) {
	case "start":
		message := "LCAgent starting managed process"
		if command = strings.TrimSpace(command); command != "" {
			message += ": " + command
		}
		if projectPath = strings.TrimSpace(projectPath); projectPath != "" {
			message += " for " + projectPath
		}
		if cwd = strings.TrimSpace(cwd); cwd != "" {
			message += " in " + cwd
		}
		return message
	case "list":
		return "LCAgent listing managed processes"
	case "stop":
		return "LCAgent stopping managed process"
	default:
		return ""
	}
}

func lcagentManagedProcessResult(prefix string, snapshot projectrun.Snapshot, success bool) tools.ToolResult {
	output := strings.TrimSpace(prefix)
	detail := lcagentManagedProcessLine(snapshot)
	if detail != "" {
		if output != "" {
			output += ": "
		}
		output += detail
	}
	if output == "" {
		output = "Managed process updated."
	}
	return tools.ToolResult{
		Success:        success,
		Output:         output,
		Command:        snapshot.Command,
		CWD:            firstNonEmpty(snapshot.CWD, snapshot.ProjectPath),
		ManagedProcess: lcagentManagedProcessEvidence("start", snapshot),
	}
}

func lcagentManagedProcessListResult(snapshots []projectrun.Snapshot) tools.ToolResult {
	lines := []string{}
	evidence := []tools.ManagedProcessEvidence{}
	for _, snapshot := range snapshots {
		if !lcagentSnapshotHasProcessDetail(snapshot) {
			continue
		}
		line := lcagentManagedProcessLine(snapshot)
		if line != "" {
			lines = append(lines, line)
		}
		if item := lcagentManagedProcessEvidence("list", snapshot); item != nil {
			evidence = append(evidence, *item)
		}
	}
	if len(lines) == 0 {
		return tools.ToolResult{Success: true, Output: "No managed background processes are known to Little Control Room."}
	}
	return tools.ToolResult{Success: true, Output: strings.Join(lines, "\n"), ManagedProcesses: evidence}
}

func lcagentManagedProcessEvidence(action string, snapshot projectrun.Snapshot) *tools.ManagedProcessEvidence {
	if !lcagentSnapshotHasProcessDetail(snapshot) {
		return nil
	}
	return &tools.ManagedProcessEvidence{
		Action:        strings.TrimSpace(action),
		ProjectPath:   strings.TrimSpace(snapshot.ProjectPath),
		ProcessID:     strings.TrimSpace(snapshot.ID),
		Name:          strings.TrimSpace(snapshot.Name),
		Command:       strings.TrimSpace(snapshot.Command),
		CWD:           firstNonEmpty(strings.TrimSpace(snapshot.CWD), strings.TrimSpace(snapshot.ProjectPath)),
		PID:           snapshot.PID,
		PGID:          snapshot.PGID,
		Running:       snapshot.Running,
		ExitCode:      snapshot.ExitCode,
		ExitCodeKnown: snapshot.ExitCodeKnown,
		Ports:         append([]int(nil), snapshot.Ports...),
		URLs:          append([]string(nil), snapshot.AnnouncedURLs...),
		RecentOutput:  append([]string(nil), snapshot.RecentOutput...),
		Error:         strings.TrimSpace(snapshot.LastError),
	}
}

func lcagentSnapshotHasProcessDetail(snapshot projectrun.Snapshot) bool {
	return snapshot.Running ||
		strings.TrimSpace(snapshot.Command) != "" ||
		!snapshot.ExitedAt.IsZero() ||
		strings.TrimSpace(snapshot.LastError) != "" ||
		len(snapshot.Ports) > 0 ||
		len(snapshot.AnnouncedURLs) > 0 ||
		len(snapshot.RecentOutput) > 0
}

func lcagentManagedProcessLine(snapshot projectrun.Snapshot) string {
	project := strings.TrimSpace(snapshot.ProjectPath)
	command := strings.TrimSpace(snapshot.Command)
	if command == "" {
		command = "managed process"
	}
	status := "stopped"
	if snapshot.Running {
		status = "running"
	} else if snapshot.ExitCodeKnown {
		status = fmt.Sprintf("exited %d", snapshot.ExitCode)
	}
	parts := []string{status, command}
	if id := strings.TrimSpace(snapshot.ID); id != "" {
		parts = append(parts, "id "+id)
	}
	if name := strings.TrimSpace(snapshot.Name); name != "" {
		parts = append(parts, "name "+name)
	}
	if project != "" {
		parts = append(parts, "project "+project)
	}
	if cwd := strings.TrimSpace(snapshot.CWD); cwd != "" && cwd != project {
		parts = append(parts, "cwd "+cwd)
	}
	if snapshot.PID > 0 {
		parts = append(parts, fmt.Sprintf("pid %d", snapshot.PID))
	}
	if snapshot.PGID > 0 {
		parts = append(parts, fmt.Sprintf("pgid %d", snapshot.PGID))
	}
	if url := lcagentRuntimeURL(snapshot); url != "" {
		parts = append(parts, "url "+url)
	}
	if len(snapshot.Ports) > 0 {
		ports := make([]string, 0, len(snapshot.Ports))
		for _, port := range snapshot.Ports {
			ports = append(ports, strconv.Itoa(port))
		}
		parts = append(parts, "ports "+strings.Join(ports, ","))
	}
	if len(snapshot.RecentOutput) > 0 {
		parts = append(parts, "recent "+snapshot.RecentOutput[len(snapshot.RecentOutput)-1])
	}
	if strings.TrimSpace(snapshot.LastError) != "" {
		parts = append(parts, "error "+strings.TrimSpace(snapshot.LastError))
	}
	return strings.Join(parts, "; ")
}

func lcagentRuntimeURL(snapshot projectrun.Snapshot) string {
	if len(snapshot.AnnouncedURLs) > 0 {
		return strings.TrimSpace(snapshot.AnnouncedURLs[0])
	}
	if len(snapshot.Ports) > 0 {
		return fmt.Sprintf("http://127.0.0.1:%d/", snapshot.Ports[0])
	}
	return ""
}
