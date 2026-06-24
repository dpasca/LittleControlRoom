package runtimemcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"lcroom/internal/procinspect"
	"lcroom/internal/projectrun"
)

const (
	serverName               = "little-control-room-runtime"
	defaultProtocolVersion   = "2024-11-05"
	processInspectionTimeout = 900 * time.Millisecond
)

type Options struct {
	ProjectPath string
	Provider    string
	DataDir     string
	SessionKey  string
	Input       io.Reader
	Output      io.Writer
	Manager     *projectrun.Manager
}

type Server struct {
	projectPath string
	provider    string
	dataDir     string
	sessionKey  string
	input       io.Reader
	output      io.Writer
	manager     *projectrun.Manager
	ownManager  bool
}

func Run(ctx context.Context, opts Options) error {
	server, err := New(opts)
	if err != nil {
		return err
	}
	return server.Run(ctx)
}

func New(opts Options) (*Server, error) {
	projectPath := filepath.Clean(strings.TrimSpace(opts.ProjectPath))
	if projectPath == "" || projectPath == "." {
		return nil, errors.New("project path is required")
	}
	input := opts.Input
	if input == nil {
		input = os.Stdin
	}
	output := opts.Output
	if output == nil {
		output = os.Stdout
	}
	manager := opts.Manager
	ownManager := false
	if manager == nil {
		manager = projectrun.NewManager()
		ownManager = true
	}
	return &Server{
		projectPath: projectPath,
		provider:    strings.TrimSpace(opts.Provider),
		dataDir:     strings.TrimSpace(opts.DataDir),
		sessionKey:  strings.TrimSpace(opts.SessionKey),
		input:       input,
		output:      output,
		manager:     manager,
		ownManager:  ownManager,
	}, nil
}

func (s *Server) Run(ctx context.Context) error {
	if s == nil {
		return errors.New("runtime MCP server unavailable")
	}
	if s.ownManager {
		defer func() { _ = s.manager.CloseAll() }()
	}

	decoder := json.NewDecoder(s.input)
	encoder := json.NewEncoder(s.output)
	for {
		var req rpcRequest
		if err := decoder.Decode(&req); err != nil {
			if errors.Is(err, io.EOF) || ctx.Err() != nil {
				return nil
			}
			return err
		}
		response, ok := s.handle(ctx, req)
		if !ok {
			continue
		}
		if err := encoder.Encode(response); err != nil {
			return err
		}
	}
}

func (s *Server) handle(ctx context.Context, req rpcRequest) (rpcResponse, bool) {
	if !req.hasID() {
		return rpcResponse{}, false
	}
	switch strings.TrimSpace(req.Method) {
	case "initialize":
		return rpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]any{
				"protocolVersion": requestedProtocolVersion(req.Params),
				"capabilities": map[string]any{
					"tools": map[string]any{
						"listChanged": false,
					},
				},
				"serverInfo": map[string]any{
					"name":    serverName,
					"version": "0.1.0",
				},
			},
		}, true
	case "tools/list":
		return rpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]any{
				"tools": runtimeTools(),
			},
		}, true
	case "tools/call":
		result, err := s.handleToolCall(ctx, req.Params)
		if err != nil {
			return rpcErrorResponse(req.ID, -32602, err.Error()), true
		}
		return rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: result}, true
	case "ping":
		return rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{}}, true
	case "shutdown":
		return rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{}}, true
	default:
		return rpcErrorResponse(req.ID, -32601, "method not found: "+req.Method), true
	}
}

func (s *Server) handleToolCall(ctx context.Context, raw json.RawMessage) (toolCallResult, error) {
	var params toolCallParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return toolCallResult{}, fmt.Errorf("decode tool call: %w", err)
	}
	name := strings.TrimSpace(params.Name)
	args := params.Arguments
	if len(strings.TrimSpace(string(args))) == 0 {
		args = json.RawMessage(`{}`)
	}
	switch name {
	case "list_processes":
		var req listProcessesArgs
		if err := json.Unmarshal(args, &req); err != nil {
			return toolCallResult{}, fmt.Errorf("decode list_processes args: %w", err)
		}
		report := s.processReport(ctx, !req.IncludeObservedSet || req.IncludeObserved)
		return jsonToolResult(report, false)
	case "start_process":
		var req startProcessArgs
		if err := json.Unmarshal(args, &req); err != nil {
			return toolCallResult{}, fmt.Errorf("decode start_process args: %w", err)
		}
		report, isErr := s.startProcess(ctx, req)
		return jsonToolResult(report, isErr)
	case "stop_process":
		var req stopProcessArgs
		if err := json.Unmarshal(args, &req); err != nil {
			return toolCallResult{}, fmt.Errorf("decode stop_process args: %w", err)
		}
		report, isErr := s.stopProcess(ctx, req)
		return jsonToolResult(report, isErr)
	default:
		return toolCallResult{}, fmt.Errorf("unknown runtime tool: %s", name)
	}
}

func (s *Server) startProcess(ctx context.Context, req startProcessArgs) (map[string]any, bool) {
	command := strings.TrimSpace(req.Command)
	if command == "" {
		return map[string]any{
			"success": false,
			"error":   "command is required",
		}, true
	}
	if req.CreateNew && req.ReplaceExisting {
		return map[string]any{
			"success": false,
			"error":   "create_new and replace_existing cannot both be true",
			"command": command,
		}, true
	}
	cwd, err := normalizeRuntimeCWD(s.projectPath, req.CWD)
	if err != nil {
		return map[string]any{
			"success": false,
			"error":   err.Error(),
			"command": command,
			"cwd":     strings.TrimSpace(req.CWD),
		}, true
	}
	if !req.CreateNew && !req.ReplaceExisting {
		if observed := s.observedMatchingListener(ctx, command, cwd); observed != nil {
			return map[string]any{
				"success":          true,
				"disposition":      "observed_existing",
				"message":          "A matching project-local listener is already running; reuse it instead of launching a duplicate. Set create_new=true only for an intentional parallel copy.",
				"observed_process": observed,
				"project_path":     s.projectPath,
				"command":          command,
				"cwd":              cwd,
			}, false
		}
	}
	result, err := s.manager.StartManaged(projectrun.StartRequest{
		ProjectPath:     s.projectPath,
		Command:         command,
		CWD:             cwd,
		Name:            strings.TrimSpace(req.Name),
		CreateNew:       true,
		ReuseMatching:   !req.CreateNew,
		ReplaceExisting: req.ReplaceExisting,
	})
	if err != nil {
		return map[string]any{
			"success": false,
			"error":   err.Error(),
			"command": command,
			"cwd":     cwd,
		}, true
	}
	return map[string]any{
		"success":        true,
		"disposition":    string(result.Disposition),
		"replaced_count": result.ReplacedCount,
		"message":        startMessage(result),
		"process":        snapshotSummary(s.projectPath, result.Snapshot),
	}, false
}

func (s *Server) stopProcess(ctx context.Context, req stopProcessArgs) (map[string]any, bool) {
	processID := strings.TrimSpace(req.ProcessID)
	err := s.manager.StopProcess(s.projectPath, processID)
	if errors.Is(err, projectrun.ErrNotRunning) {
		return map[string]any{
			"success": true,
			"message": "No matching managed process is running for this workspace.",
		}, false
	}
	if err != nil {
		return map[string]any{
			"success":    false,
			"error":      err.Error(),
			"process_id": processID,
		}, true
	}
	report := s.processReport(ctx, true)
	return map[string]any{
		"success":    true,
		"message":    "Stopping managed process.",
		"process_id": processID,
		"state":      report,
	}, false
}

func (s *Server) processReport(ctx context.Context, includeObserved bool) map[string]any {
	managed := s.manager.SnapshotsForProject(s.projectPath)
	report := map[string]any{
		"project_path":       s.projectPath,
		"provider":           s.provider,
		"managed_processes":  snapshotSummaries(s.projectPath, managed),
		"observed_listeners": []map[string]any{},
	}
	if includeObserved {
		report["observed_listeners"] = observedListenerSummaries(s.projectPath, scanObservedListeners(ctx, s.manager, s.projectPath))
	}
	return report
}

func (s *Server) observedMatchingListener(ctx context.Context, command, cwd string) map[string]any {
	command = compact(command)
	cwd = filepath.Clean(strings.TrimSpace(cwd))
	for _, instance := range scanObservedListeners(ctx, s.manager, s.projectPath) {
		process := instance.Process
		if filepath.Clean(strings.TrimSpace(process.CWD)) != cwd {
			continue
		}
		if processCommandMatches(compact(process.Command), command) {
			return observedListenerSummary(s.projectPath, instance)
		}
	}
	return nil
}

func scanObservedListeners(parent context.Context, manager *projectrun.Manager, projectPath string) []procinspect.ProjectInstance {
	ctx, cancel := context.WithTimeout(parent, processInspectionTimeout)
	defer cancel()
	managedPIDs, managedPGIDs := managedRuntimeProcessSets(manager.Snapshots())
	reports, err := procinspect.ScanProjects(ctx, procinspect.ScanOptions{
		ProjectPaths: projectPathsForScan(projectPath),
		ManagedPIDs:  managedPIDs,
		ManagedPGIDs: managedPGIDs,
		OwnPID:       os.Getpid(),
	})
	if err != nil {
		return nil
	}
	for _, report := range reports {
		if filepath.Clean(report.ProjectPath) == filepath.Clean(projectPath) {
			return report.Instances
		}
	}
	return nil
}

func snapshotSummaries(projectPath string, snapshots []projectrun.Snapshot) []map[string]any {
	out := make([]map[string]any, 0, len(snapshots))
	for _, snapshot := range snapshots {
		out = append(out, snapshotSummary(projectPath, snapshot))
	}
	return out
}

func snapshotSummary(projectPath string, snapshot projectrun.Snapshot) map[string]any {
	item := map[string]any{
		"id":                 snapshot.ID,
		"name":               snapshot.Name,
		"default":            snapshot.Default,
		"project_path":       snapshot.ProjectPath,
		"command":            snapshot.Command,
		"cwd":                snapshot.CWD,
		"relative_cwd":       relativeCWD(projectPath, snapshot.CWD),
		"pid":                snapshot.PID,
		"pgid":               snapshot.PGID,
		"running":            snapshot.Running,
		"ports":              snapshot.Ports,
		"conflict_ports":     snapshot.ConflictPorts,
		"announced_urls":     snapshot.AnnouncedURLs,
		"recent_output":      snapshot.RecentOutput,
		"exit_code":          snapshot.ExitCode,
		"exit_code_known":    snapshot.ExitCodeKnown,
		"last_error":         snapshot.LastError,
		"started_at":         formatTime(snapshot.StartedAt),
		"exited_at":          formatTime(snapshot.ExitedAt),
		"preferred_url":      preferredURL(snapshot.AnnouncedURLs, snapshot.Ports),
		"managed_by_lcr_mcp": true,
	}
	return item
}

func observedListenerSummaries(projectPath string, instances []procinspect.ProjectInstance) []map[string]any {
	out := make([]map[string]any, 0, len(instances))
	for _, instance := range instances {
		out = append(out, observedListenerSummary(projectPath, instance))
	}
	return out
}

func observedListenerSummary(projectPath string, instance procinspect.ProjectInstance) map[string]any {
	process := instance.Process
	return map[string]any{
		"pid":                  process.PID,
		"ppid":                 process.PPID,
		"pgid":                 process.PGID,
		"command":              process.Command,
		"cwd":                  process.CWD,
		"relative_cwd":         relativeCWD(projectPath, process.CWD),
		"ports":                process.Ports,
		"preferred_url":        preferredURL(nil, process.Ports),
		"orphaned_under_pid_1": process.PPID == 1,
		"owned_by_current_app": instance.OwnedByCurrentApp,
		"managed_runtime":      instance.ManagedRuntime,
	}
}

func runtimeTools() []mcpTool {
	return []mcpTool{
		{
			Name:        "list_processes",
			Description: "List Little Control Room managed runtime processes for this project and observed project-local TCP listeners. Call this before starting a local server/watch process when ports may already be active.",
			InputSchema: map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"include_observed": map[string]any{"type": "boolean", "description": "Include project-local TCP listeners discovered from the OS. Defaults to true."},
				},
			},
		},
		{
			Name:        "start_process",
			Description: "Start a long-running project runtime through Little Control Room. By default this reuses an existing matching command/cwd process instead of launching a duplicate. Set create_new=true only for an intentional parallel copy; set replace_existing=true only when a fresh instance is needed.",
			InputSchema: map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"command":          map[string]any{"type": "string", "description": "Foreground command to run, for example \"npm run dev\" or \"pnpm dev\"."},
					"cwd":              map[string]any{"type": "string", "description": "Optional project-relative working directory. Absolute paths must stay inside the project."},
					"name":             map[string]any{"type": "string", "description": "Optional short label, such as \"frontend\" or \"sprite tuner\"."},
					"create_new":       map[string]any{"type": "boolean", "description": "Set true only when another concurrent copy of the same command/cwd is truly needed."},
					"replace_existing": map[string]any{"type": "boolean", "description": "Stop matching managed processes before starting a fresh one. Do not combine with create_new."},
				},
				"required": []string{"command"},
			},
		},
		{
			Name:        "stop_process",
			Description: "Stop a Little Control Room managed runtime process for this project. Use process_id from list_processes when more than one managed process is known.",
			InputSchema: map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"process_id": map[string]any{"type": "string", "description": "Optional managed process id. If omitted, stops the selected/default managed runtime for this project."},
				},
			},
		},
	}
}

func jsonToolResult(value any, isError bool) (toolCallResult, error) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return toolCallResult{}, err
	}
	return toolCallResult{
		Content: []mcpContent{{
			Type: "text",
			Text: string(data),
		}},
		IsError: isError,
	}, nil
}

func startMessage(result projectrun.StartResult) string {
	switch result.Disposition {
	case projectrun.StartDispositionReused:
		return "Managed process already running; reuse this process."
	case projectrun.StartDispositionReplaced:
		return fmt.Sprintf("Replaced %d matching managed process(es).", result.ReplacedCount)
	default:
		return "Started managed process."
	}
}

func normalizeRuntimeCWD(projectPath, cwd string) (string, error) {
	projectPath = filepath.Clean(strings.TrimSpace(projectPath))
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

func projectPathsForScan(projectPath string) []string {
	projectPath = filepath.Clean(strings.TrimSpace(projectPath))
	if projectPath == "" || projectPath == "." {
		return nil
	}
	return []string{projectPath}
}

func processCommandMatches(processCommand, requestedCommand string) bool {
	processCommand = compact(processCommand)
	requestedCommand = compact(requestedCommand)
	if processCommand == "" || requestedCommand == "" {
		return false
	}
	return processCommand == requestedCommand || strings.Contains(processCommand, requestedCommand)
}

func compact(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
}

func preferredURL(urls []string, ports []int) string {
	for _, raw := range urls {
		if trimmed := strings.TrimSpace(raw); trimmed != "" {
			return trimmed
		}
	}
	for _, port := range ports {
		if port > 0 {
			return "http://127.0.0.1:" + strconv.Itoa(port) + "/"
		}
	}
	return ""
}

func relativeCWD(projectPath, cwd string) string {
	projectPath = filepath.Clean(strings.TrimSpace(projectPath))
	cwd = filepath.Clean(strings.TrimSpace(cwd))
	if projectPath == "" || projectPath == "." || cwd == "" || cwd == "." {
		return ""
	}
	rel, err := filepath.Rel(projectPath, cwd)
	if err != nil || rel == "." {
		return ""
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return cwd
	}
	return rel
}

func formatTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.Format(time.RFC3339)
}

func requestedProtocolVersion(raw json.RawMessage) string {
	var params struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if err := json.Unmarshal(raw, &params); err != nil {
		return defaultProtocolVersion
	}
	if trimmed := strings.TrimSpace(params.ProtocolVersion); trimmed != "" {
		return trimmed
	}
	return defaultProtocolVersion
}

func rpcErrorResponse(id json.RawMessage, code int, message string) rpcResponse {
	return rpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error: &rpcError{
			Code:    code,
			Message: message,
		},
	}
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

func (r rpcRequest) hasID() bool {
	trimmed := strings.TrimSpace(string(r.ID))
	return trimmed != "" && trimmed != "null"
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type mcpTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

type mcpContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type toolCallResult struct {
	Content []mcpContent `json:"content"`
	IsError bool         `json:"isError,omitempty"`
}

type toolCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type listProcessesArgs struct {
	IncludeObserved    bool `json:"include_observed"`
	IncludeObservedSet bool
}

func (a *listProcessesArgs) UnmarshalJSON(data []byte) error {
	type rawArgs listProcessesArgs
	var raw rawArgs
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	var present map[string]json.RawMessage
	if err := json.Unmarshal(data, &present); err == nil {
		_, raw.IncludeObservedSet = present["include_observed"]
	}
	*a = listProcessesArgs(raw)
	return nil
}

type startProcessArgs struct {
	Command         string `json:"command"`
	CWD             string `json:"cwd"`
	Name            string `json:"name"`
	CreateNew       bool   `json:"create_new"`
	ReplaceExisting bool   `json:"replace_existing"`
}

type stopProcessArgs struct {
	ProcessID string `json:"process_id"`
}
