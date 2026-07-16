package runtimemcp

import (
	"bytes"
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
	"lcroom/internal/store"
	"lcroom/internal/todocapture"
)

const (
	serverName                     = "little-control-room-runtime"
	legacyProtocolVersion          = "2024-11-05"
	structuredToolsProtocolVersion = "2025-06-18"
	defaultProtocolVersion         = legacyProtocolVersion
	processInspectionTimeout       = 900 * time.Millisecond
)

type Options struct {
	ProjectPath     string
	Provider        string
	DataDir         string
	SessionKey      string
	DBPath          string
	TodoCaptureMode todocapture.CaptureMode
	Input           io.Reader
	Output          io.Writer
	Manager         *projectrun.Manager
	TodoHandler     todocapture.Handler
}

type Server struct {
	projectPath     string
	provider        string
	dataDir         string
	sessionKey      string
	input           io.Reader
	output          io.Writer
	manager         *projectrun.Manager
	ownManager      bool
	todoMode        todocapture.CaptureMode
	todoHandler     todocapture.Handler
	todoStore       *store.Store
	protocolVersion string
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
	todoMode := todocapture.NormalizeCaptureMode(opts.TodoCaptureMode)
	todoHandler := opts.TodoHandler
	var todoStore *store.Store
	if todoMode.Enabled() && todoHandler == nil {
		dbPath := strings.TrimSpace(opts.DBPath)
		if dbPath == "" {
			return nil, errors.New("DB path is required when project TODO capture is enabled")
		}
		var err error
		todoStore, err = store.Open(dbPath)
		if err != nil {
			return nil, fmt.Errorf("open TODO capture store: %w", err)
		}
		todoHandler = todocapture.NewExternalService(todoStore, todoMode)
	}
	return &Server{
		projectPath:     projectPath,
		provider:        strings.TrimSpace(opts.Provider),
		dataDir:         strings.TrimSpace(opts.DataDir),
		sessionKey:      strings.TrimSpace(opts.SessionKey),
		input:           input,
		output:          output,
		manager:         manager,
		ownManager:      ownManager,
		todoMode:        todoMode,
		todoHandler:     todoHandler,
		todoStore:       todoStore,
		protocolVersion: defaultProtocolVersion,
	}, nil
}

func (s *Server) Run(ctx context.Context) error {
	if s == nil {
		return errors.New("runtime MCP server unavailable")
	}
	if s.ownManager {
		defer func() { _ = s.manager.CloseAll() }()
	}
	if s.todoStore != nil {
		defer s.todoStore.Close()
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
		protocolVersion := negotiatedProtocolVersion(req.Params)
		s.protocolVersion = protocolVersion
		result := map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities": map[string]any{
				"tools": map[string]any{
					"listChanged": false,
				},
			},
			"serverInfo": map[string]any{
				"name":    serverName,
				"version": "0.1.0",
			},
		}
		if s.todoMode.Enabled() {
			result["instructions"] = todocapture.AgentInstructions(s.todoMode)
		}
		return rpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  result,
		}, true
	case "tools/list":
		return rpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]any{
				"tools": runtimeTools(s.todoMode, s.supportsStructuredTools()),
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
		return s.jsonToolResult(report, false)
	case "start_process":
		var req startProcessArgs
		if err := json.Unmarshal(args, &req); err != nil {
			return toolCallResult{}, fmt.Errorf("decode start_process args: %w", err)
		}
		report, isErr := s.startProcess(ctx, req)
		return s.jsonToolResult(report, isErr)
	case "stop_process":
		var req stopProcessArgs
		if err := json.Unmarshal(args, &req); err != nil {
			return toolCallResult{}, fmt.Errorf("decode stop_process args: %w", err)
		}
		report, isErr := s.stopProcess(ctx, req)
		return s.jsonToolResult(report, isErr)
	case "list_project_todos":
		if !s.todoMode.Enabled() || s.todoHandler == nil {
			return toolCallResult{}, fmt.Errorf("project TODO capture is disabled")
		}
		var listArgs struct{}
		if err := decodeStrictToolArgs(args, &listArgs); err != nil {
			return toolCallResult{}, fmt.Errorf("decode list_project_todos args: %w", err)
		}
		response, err := s.todoHandler.HandleTodoCapture(ctx, todocapture.Request{
			Action: todocapture.ActionList,
			Origin: s.todoOrigin(),
		})
		if err != nil {
			return s.jsonToolResult(map[string]any{"success": false, "error": err.Error()}, true)
		}
		return s.jsonToolResult(response.List, false)
	case "add_project_todo":
		if !s.todoMode.Enabled() || s.todoHandler == nil {
			return toolCallResult{}, fmt.Errorf("project TODO capture is disabled")
		}
		var add addProjectTodoArgs
		if err := decodeStrictToolArgs(args, &add); err != nil {
			return toolCallResult{}, fmt.Errorf("decode add_project_todo args: %w", err)
		}
		response, err := s.todoHandler.HandleTodoCapture(ctx, todocapture.Request{
			Action: todocapture.ActionAdd,
			Origin: s.todoOrigin(),
			Add: todocapture.AddRequest{
				Text:           add.Text,
				CaptureKind:    todocapture.CaptureKind(add.CaptureKind),
				ReviewRevision: add.ReviewRevision,
			},
		})
		if err != nil {
			return s.jsonToolResult(map[string]any{"success": false, "error": err.Error()}, true)
		}
		return s.jsonToolResult(response.Add, false)
	default:
		return toolCallResult{}, fmt.Errorf("unknown runtime tool: %s", name)
	}
}

func decodeStrictToolArgs(data json.RawMessage, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("unexpected trailing JSON value")
		}
		return err
	}
	return nil
}

func (s *Server) todoOrigin() todocapture.Origin {
	return todocapture.Origin{
		ProjectPath: s.projectPath,
		Provider:    s.provider,
		SessionKey:  s.sessionKey,
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

func runtimeTools(todoMode todocapture.CaptureMode, structuredTools bool) []mcpTool {
	tools := []mcpTool{
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
	if !todoMode.Enabled() {
		return tools
	}
	allowedCaptureKinds := []string{string(todocapture.CaptureExplicitRequest)}
	if todoMode.Allows(todocapture.CaptureClearDeferral) {
		allowedCaptureKinds = append(allowedCaptureKinds, string(todocapture.CaptureClearDeferral))
	}
	listTool := mcpTool{
		Name:        "list_project_todos",
		Description: "List the open Little Control Room TODOs for this session's repository. The repository scope is derived from the launch path and cannot be overridden. Always call this before add_project_todo, compare the proposed item with every open TODO for semantic duplicates, and retain review_revision for the add call.",
		InputSchema: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties":           map[string]any{},
		},
	}
	addTool := mcpTool{
		Name:        "add_project_todo",
		Description: "Add one repository-scoped Little Control Room TODO after list_project_todos has been reviewed. Do not call for a semantic duplicate. Pass the exact review_revision from that list result; a todos_changed disposition means the list changed, so list again and reassess. The repository scope is fixed by the host and cannot be supplied by the model.",
		InputSchema: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"text": map[string]any{
					"type":        "string",
					"description": "Concise, actionable TODO text preserving the user's intent.",
					"minLength":   1,
				},
				"capture_kind": map[string]any{
					"type":        "string",
					"enum":        allowedCaptureKinds,
					"description": "Why capture is authorized. The server rejects clear_deferral unless the configured mode permits it.",
				},
				"review_revision": map[string]any{
					"type":        "string",
					"description": "Exact review_revision returned by the immediately preceding list_project_todos call.",
					"minLength":   1,
				},
			},
			"required": []string{"text", "capture_kind", "review_revision"},
		},
	}
	if structuredTools {
		listTool.OutputSchema = todoListOutputSchema()
		listTool.Annotations = &mcpToolAnnotations{
			Title:           "List project TODOs",
			ReadOnlyHint:    true,
			DestructiveHint: false,
			IdempotentHint:  true,
			OpenWorldHint:   false,
		}
		addTool.OutputSchema = todoAddOutputSchema()
		addTool.Annotations = &mcpToolAnnotations{
			Title:           "Add project TODO",
			ReadOnlyHint:    false,
			DestructiveHint: false,
			IdempotentHint:  true,
			OpenWorldHint:   false,
		}
	}
	return append(tools, listTool, addTool)
}

func todoListOutputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"scope":        map[string]any{"type": "object"},
			"capture_mode": map[string]any{"type": "string"},
			"open_todos": map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "object"},
			},
			"review_revision": map[string]any{"type": "string"},
		},
		"required": []string{"scope", "capture_mode", "open_todos", "review_revision"},
	}
}

func todoAddOutputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"scope":                   map[string]any{"type": "object"},
			"disposition":             map[string]any{"type": "string", "enum": []string{todocapture.DispositionCreated, todocapture.DispositionExistingDuplicate, todocapture.DispositionTodosChanged}},
			"todo":                    map[string]any{"type": "object"},
			"current_open_todos":      map[string]any{"type": "array", "items": map[string]any{"type": "object"}},
			"current_review_revision": map[string]any{"type": "string"},
			"warnings":                map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		},
		"required": []string{"scope", "disposition", "current_review_revision"},
	}
}

func (s *Server) jsonToolResult(value any, isError bool) (toolCallResult, error) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return toolCallResult{}, err
	}
	result := toolCallResult{
		Content: []mcpContent{{
			Type: "text",
			Text: string(data),
		}},
		IsError: isError,
	}
	if !isError && s.supportsStructuredTools() {
		result.StructuredContent = value
	}
	return result, nil
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

func negotiatedProtocolVersion(raw json.RawMessage) string {
	var params struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if err := json.Unmarshal(raw, &params); err != nil {
		return defaultProtocolVersion
	}
	switch strings.TrimSpace(params.ProtocolVersion) {
	case structuredToolsProtocolVersion:
		return structuredToolsProtocolVersion
	case legacyProtocolVersion:
		return legacyProtocolVersion
	}
	return defaultProtocolVersion
}

func (s *Server) supportsStructuredTools() bool {
	return s != nil && s.protocolVersion == structuredToolsProtocolVersion
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
	Name         string              `json:"name"`
	Description  string              `json:"description"`
	InputSchema  map[string]any      `json:"inputSchema"`
	OutputSchema map[string]any      `json:"outputSchema,omitempty"`
	Annotations  *mcpToolAnnotations `json:"annotations,omitempty"`
}

type mcpToolAnnotations struct {
	Title           string `json:"title,omitempty"`
	ReadOnlyHint    bool   `json:"readOnlyHint"`
	DestructiveHint bool   `json:"destructiveHint"`
	IdempotentHint  bool   `json:"idempotentHint"`
	OpenWorldHint   bool   `json:"openWorldHint"`
}

type mcpContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type toolCallResult struct {
	Content           []mcpContent `json:"content"`
	StructuredContent any          `json:"structuredContent,omitempty"`
	IsError           bool         `json:"isError,omitempty"`
}

type toolCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type listProcessesArgs struct {
	IncludeObserved    bool `json:"include_observed"`
	IncludeObservedSet bool
}

type addProjectTodoArgs struct {
	Text           string `json:"text"`
	CaptureKind    string `json:"capture_kind"`
	ReviewRevision string `json:"review_revision"`
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
