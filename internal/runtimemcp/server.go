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

	"lcroom/internal/agentcontrol"
	"lcroom/internal/agentquery"
	"lcroom/internal/browserctl"
	"lcroom/internal/claudeapproval"
	"lcroom/internal/control"
	"lcroom/internal/procinspect"
	"lcroom/internal/projectrun"
	"lcroom/internal/store"
	"lcroom/internal/todocapture"

	"github.com/charmbracelet/x/ansi"
)

const (
	serverName                                 = "little-control-room-runtime"
	legacyProtocolVersion                      = "2024-11-05"
	structuredToolsProtocolVersion             = "2025-06-18"
	defaultProtocolVersion                     = legacyProtocolVersion
	processInspectionTimeout                   = 900 * time.Millisecond
	managedBrowserAttentionHeartbeatMaxAge     = 5 * time.Second
	managedBrowserAttentionMessageMaxRuneCount = 800
)

type Options struct {
	ProjectPath          string
	Provider             string
	DataDir              string
	SessionKey           string
	BrowserSessionKey    string
	ClaudeApprovalSocket string
	DBPath               string
	TodoCaptureMode      todocapture.CaptureMode
	ControlScope         control.AuthorityScope
	QueryScope           agentquery.Scope
	Input                io.Reader
	Output               io.Writer
	Manager              *projectrun.Manager
	TodoHandler          todocapture.Handler
	Store                *store.Store
}

type Server struct {
	projectPath          string
	provider             string
	dataDir              string
	sessionKey           string
	browserSessionKey    string
	claudeApprovalSocket string
	input                io.Reader
	output               io.Writer
	manager              *projectrun.Manager
	ownManager           bool
	todoMode             todocapture.CaptureMode
	todoHandler          todocapture.Handler
	controlScope         control.AuthorityScope
	controlExecutor      *agentcontrol.Executor
	queryScope           agentquery.Scope
	queryExecutor        *agentquery.Executor
	stateStore           *store.Store
	ownStore             bool
	protocolVersion      string
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
	stateStore := opts.Store
	ownStore := false
	dbPath := strings.TrimSpace(opts.DBPath)
	if stateStore == nil && dbPath != "" {
		var err error
		stateStore, err = store.Open(dbPath)
		if err != nil {
			return nil, fmt.Errorf("open runtime MCP store: %w", err)
		}
		ownStore = true
	}
	if todoMode.Enabled() && todoHandler == nil {
		if stateStore == nil {
			return nil, errors.New("DB path is required when project TODO capture is enabled")
		}
		todoHandler = todocapture.NewExternalService(stateStore, todoMode)
	}
	controlScope := control.NormalizeAuthorityScope(string(opts.ControlScope))
	if controlScope == "" {
		controlScope = control.AuthorityScopeProject
	}
	queryScope := agentquery.NormalizeScope(string(opts.QueryScope))
	if queryScope == "" {
		queryScope = agentquery.ScopeProject
	}
	var queryExecutor *agentquery.Executor
	var controlExecutor *agentcontrol.Executor
	if stateStore != nil {
		var queryErr error
		queryExecutor, queryErr = agentquery.NewExecutor(agentquery.Options{
			Reader:            stateStore,
			OriginProjectPath: projectPath,
			Scope:             queryScope,
		})
		if queryErr != nil {
			return nil, fmt.Errorf("initialize runtime MCP query service: %w", queryErr)
		}
		controlExecutor, queryErr = agentcontrol.NewExecutor(agentcontrol.Options{
			Store:             stateStore,
			OriginProjectPath: projectPath,
			Scope:             controlScope,
			Source:            serverName,
			Provider:          strings.TrimSpace(opts.Provider),
			SessionKey:        strings.TrimSpace(opts.SessionKey),
		})
		if queryErr != nil {
			return nil, fmt.Errorf("initialize runtime MCP control service: %w", queryErr)
		}
	}
	return &Server{
		projectPath:          projectPath,
		provider:             strings.TrimSpace(opts.Provider),
		dataDir:              strings.TrimSpace(opts.DataDir),
		sessionKey:           strings.TrimSpace(opts.SessionKey),
		browserSessionKey:    strings.TrimSpace(opts.BrowserSessionKey),
		claudeApprovalSocket: strings.TrimSpace(opts.ClaudeApprovalSocket),
		input:                input,
		output:               output,
		manager:              manager,
		ownManager:           ownManager,
		todoMode:             todoMode,
		todoHandler:          todoHandler,
		controlScope:         controlScope,
		controlExecutor:      controlExecutor,
		queryScope:           queryScope,
		queryExecutor:        queryExecutor,
		stateStore:           stateStore,
		ownStore:             ownStore,
		protocolVersion:      defaultProtocolVersion,
	}, nil
}

func (s *Server) Run(ctx context.Context) error {
	if s == nil {
		return errors.New("runtime MCP server unavailable")
	}
	if s.ownManager {
		defer func() { _ = s.manager.CloseAll() }()
	}
	if s.ownStore && s.stateStore != nil {
		defer s.stateStore.Close()
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
		instructions := "Little Control Room exposes project runtime tools plus progressively discoverable read and control catalogs. For current LCR state, call list_lcr_queries with one exact domain, then describe_lcr_query and run_lcr_query. Query results are bounded persisted snapshots and never include private-category projects outside the originating project. For actions, use list_control_capabilities, then describe_control_capability before propose_control_operation. When the user asks you to tell, hand off to, continue, trigger, or steer another embedded engineer, inspect the target session and propose engineer.send_prompt instead of asking the operator to relay the message. Supply the matching explicit provider and exact target_session_id for a known Codex, OpenCode, Claude Code, or LCAgent recipient. After confirmation LCR persists the message, steers an eligible active Codex turn, or waits to resume the exact recipient when idle. Every proposed write or external action is validated by LCR and waits for explicit operator confirmation. Use get_control_operation on a later turn to inspect its result."
		if s.todoMode.Enabled() {
			instructions += "\n\n" + todocapture.AgentInstructions(s.todoMode)
		}
		result["instructions"] = instructions
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
				"tools": runtimeTools(s.todoMode, s.supportsStructuredTools(), s.claudeApprovalSocket != ""),
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
	case "list_lcr_queries":
		var req listLCRQueriesArgs
		if err := decodeStrictToolArgs(args, &req); err != nil {
			return toolCallResult{}, fmt.Errorf("decode list_lcr_queries args: %w", err)
		}
		report, isErr := s.listLCRQueries(req)
		return s.jsonToolResult(report, isErr)
	case "describe_lcr_query":
		var req describeLCRQueryArgs
		if err := decodeStrictToolArgs(args, &req); err != nil {
			return toolCallResult{}, fmt.Errorf("decode describe_lcr_query args: %w", err)
		}
		report, isErr := s.describeLCRQuery(req)
		return s.jsonToolResult(report, isErr)
	case "run_lcr_query":
		var req runLCRQueryArgs
		if err := decodeStrictToolArgs(args, &req); err != nil {
			return toolCallResult{}, fmt.Errorf("decode run_lcr_query args: %w", err)
		}
		report, isErr := s.runLCRQuery(ctx, req)
		return s.jsonToolResult(report, isErr)
	case "list_control_capabilities":
		var req listControlCapabilitiesArgs
		if err := decodeStrictToolArgs(args, &req); err != nil {
			return toolCallResult{}, fmt.Errorf("decode list_control_capabilities args: %w", err)
		}
		report, isErr := s.listControlCapabilities(req)
		return s.jsonToolResult(report, isErr)
	case "describe_control_capability":
		var req describeControlCapabilityArgs
		if err := decodeStrictToolArgs(args, &req); err != nil {
			return toolCallResult{}, fmt.Errorf("decode describe_control_capability args: %w", err)
		}
		report, isErr := s.describeControlCapability(req)
		return s.jsonToolResult(report, isErr)
	case "propose_control_operation":
		var req proposeControlOperationArgs
		if err := decodeStrictToolArgs(args, &req); err != nil {
			return toolCallResult{}, fmt.Errorf("decode propose_control_operation args: %w", err)
		}
		report, isErr := s.proposeControlOperation(ctx, req)
		return s.jsonToolResult(report, isErr)
	case "get_control_operation":
		var req getControlOperationArgs
		if err := decodeStrictToolArgs(args, &req); err != nil {
			return toolCallResult{}, fmt.Errorf("decode get_control_operation args: %w", err)
		}
		report, isErr := s.getControlOperation(ctx, req)
		return s.jsonToolResult(report, isErr)
	case "list_processes":
		var req listProcessesArgs
		if err := json.Unmarshal(args, &req); err != nil {
			return toolCallResult{}, fmt.Errorf("decode list_processes args: %w", err)
		}
		report := s.processReport(ctx, !req.IncludeObservedSet || req.IncludeObserved)
		return s.jsonToolResult(report, false)
	case "read_process_output":
		var req readProcessOutputArgs
		if err := json.Unmarshal(args, &req); err != nil {
			return toolCallResult{}, fmt.Errorf("decode read_process_output args: %w", err)
		}
		report, isErr := s.readProcessOutput(req)
		return s.jsonToolResult(report, isErr)
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
	case "request_browser_attention":
		var req requestBrowserAttentionArgs
		if err := json.Unmarshal(args, &req); err != nil {
			return toolCallResult{}, fmt.Errorf("decode request_browser_attention args: %w", err)
		}
		report, isErr := s.requestBrowserAttention(req)
		return s.jsonToolResult(report, isErr)
	case claudeapproval.PermissionToolName:
		if s.claudeApprovalSocket == "" {
			return toolCallResult{}, fmt.Errorf("Claude Code approval routing is unavailable")
		}
		var req claudePermissionPromptArgs
		if err := decodeStrictToolArgs(args, &req); err != nil {
			return toolCallResult{}, fmt.Errorf("decode %s args: %w", claudeapproval.PermissionToolName, err)
		}
		response := s.requestClaudeToolApproval(ctx, req)
		return s.jsonToolResult(response, false)
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

func (s *Server) listLCRQueries(req listLCRQueriesArgs) (map[string]any, bool) {
	report, err := agentquery.ListReport(req.Domain, s.queryScope, s.queryExecutor != nil)
	if err != nil {
		return map[string]any{
			"success": false,
			"error":   err.Error(),
			"domains": agentquery.DomainSummaries(),
		}, true
	}
	return report, false
}

func (s *Server) describeLCRQuery(req describeLCRQueryArgs) (map[string]any, bool) {
	report, err := agentquery.DescribeReport(req.Name, s.queryScope, "run_lcr_query")
	if err != nil {
		return map[string]any{"success": false, "error": err.Error()}, true
	}
	return report, false
}

func (s *Server) runLCRQuery(ctx context.Context, req runLCRQueryArgs) (map[string]any, bool) {
	if s.queryExecutor == nil {
		return map[string]any{
			"success": false,
			"error":   "LCR queries are unavailable because this MCP server has no LCR state store",
		}, true
	}
	name := agentquery.Name(strings.TrimSpace(req.Query))
	if _, ok := agentquery.CapabilityByName(name); !ok {
		return map[string]any{
			"success": false,
			"error":   "unknown LCR query; call list_lcr_queries first",
		}, true
	}
	report, err := s.queryExecutor.Execute(ctx, name, req.Arguments)
	if err != nil {
		return map[string]any{
			"success": false,
			"query":   name,
			"error":   err.Error(),
			"hint":    "Call describe_lcr_query and match its input_schema exactly.",
		}, true
	}
	return report, false
}

func (s *Server) listControlCapabilities(req listControlCapabilitiesArgs) (map[string]any, bool) {
	report, err := control.ListReport(req.Domain, s.controlScope, s.stateStore != nil)
	if err != nil {
		return map[string]any{
			"success": false,
			"error":   err.Error(),
			"domains": control.DomainSummaries(),
		}, true
	}
	return report, false
}

func (s *Server) describeControlCapability(req describeControlCapabilityArgs) (map[string]any, bool) {
	report, err := control.DescribeReport(req.Name, s.controlScope, "propose_control_operation")
	if err != nil {
		return map[string]any{
			"success": false,
			"error":   err.Error(),
		}, true
	}
	return report, false
}

func (s *Server) proposeControlOperation(ctx context.Context, req proposeControlOperationArgs) (map[string]any, bool) {
	if s.controlExecutor == nil {
		return map[string]any{
			"success": false,
			"error":   "control proposals are unavailable because this MCP server has no LCR state store",
		}, true
	}
	report, err := s.controlExecutor.Propose(ctx, req.Capability, req.Arguments, req.RequestID)
	if err != nil {
		return map[string]any{
			"success": false,
			"error":   err.Error(),
			"hint":    "Call describe_control_capability and match its input_schema exactly.",
		}, true
	}
	return report, false
}

func (s *Server) getControlOperation(ctx context.Context, req getControlOperationArgs) (map[string]any, bool) {
	if s.controlExecutor == nil {
		return map[string]any{
			"success": false,
			"error":   "control operations are unavailable because this MCP server has no LCR state store",
		}, true
	}
	report, err := s.controlExecutor.Get(ctx, req.OperationID)
	if err != nil {
		return map[string]any{"success": false, "error": err.Error()}, true
	}
	return report, false
}

func controlOperationReport(operation control.Operation, idempotentReplay bool) map[string]any {
	return agentcontrol.OperationReport(operation, idempotentReplay)
}

func (s *Server) requestBrowserAttention(req requestBrowserAttentionArgs) (map[string]any, bool) {
	attentionMessage := strings.TrimSpace(req.Message)
	if attentionMessage == "" {
		return map[string]any{
			"success": false,
			"error":   "message is required so Little Control Room can explain the browser step to the user",
		}, true
	}
	if len([]rune(attentionMessage)) > managedBrowserAttentionMessageMaxRuneCount {
		return map[string]any{
			"success": false,
			"error":   fmt.Sprintf("message must be at most %d characters", managedBrowserAttentionMessageMaxRuneCount),
		}, true
	}
	sessionKey := strings.TrimSpace(s.browserSessionKey)
	if sessionKey == "" {
		// Backward compatibility for runtime MCP launchers from before browser
		// and TODO capture identities were carried separately.
		sessionKey = strings.TrimSpace(s.sessionKey)
	}
	if sessionKey == "" {
		return map[string]any{
			"success": false,
			"error":   "managed browser attention is unavailable because this embedded session has no browser session key",
		}, true
	}

	var state browserctl.ManagedPlaywrightState
	err := browserctl.WithManagedPlaywrightStateLock(s.dataDir, sessionKey, func() error {
		var readErr error
		state, readErr = browserctl.ReadManagedPlaywrightState(s.dataDir, sessionKey)
		return readErr
	})
	if err != nil {
		return map[string]any{
			"success": false,
			"error":   "managed browser attention is unavailable until a Playwright browser page has opened for this session",
		}, true
	}
	state = state.Normalize()
	if state.SessionKey != sessionKey {
		return map[string]any{
			"success": false,
			"error":   "managed browser state belongs to a different embedded session",
		}, true
	}
	if filepath.Clean(state.ProjectPath) != s.projectPath {
		return map[string]any{
			"success": false,
			"error":   "managed browser state belongs to a different project",
		}, true
	}
	if state.Provider != "" && !strings.EqualFold(state.Provider, s.provider) {
		return map[string]any{
			"success": false,
			"error":   "managed browser state belongs to a different provider",
		}, true
	}
	if !state.RevealSupported || state.BrowserPID <= 0 {
		return map[string]any{
			"success": false,
			"error":   "managed browser window is not available for this session yet; navigate with Playwright first, then retry",
		}, true
	}
	now := time.Now()
	heartbeatAge := now.Sub(state.UpdatedAt)
	if state.UpdatedAt.IsZero() || heartbeatAge < 0 || heartbeatAge > managedBrowserAttentionHeartbeatMaxAge {
		return map[string]any{
			"success": false,
			"error":   "managed browser is no longer attached to this session; reconnect before requesting browser attention",
		}, true
	}

	return map[string]any{
		"success":          true,
		"message":          "Little Control Room recorded the managed-browser handoff. Stop this turn now and do not call Playwright again until the user sends a new message.",
		"requested_action": attentionMessage,
		"project_path":     s.projectPath,
		"provider":         s.provider,
		"session_key":      sessionKey,
		"browser_pid":      state.BrowserPID,
		"reveal_supported": state.RevealSupported,
	}, false
}

func (s *Server) requestClaudeToolApproval(ctx context.Context, req claudePermissionPromptArgs) claudeapproval.Response {
	toolName := strings.TrimSpace(req.ToolName)
	toolUseID := strings.TrimSpace(req.ToolUseID)
	if toolName == "" || toolUseID == "" {
		return claudeapproval.Deny("Claude Code sent a malformed approval request", false)
	}
	input := req.Input
	if len(strings.TrimSpace(string(input))) == 0 {
		input = json.RawMessage(`{}`)
	}
	response, err := claudeapproval.RequestApproval(ctx, s.claudeApprovalSocket, claudeapproval.Request{
		ID:        toolUseID,
		ToolName:  toolName,
		Input:     input,
		ToolUseID: toolUseID,
	})
	if err != nil {
		return claudeapproval.Deny("Little Control Room could not collect this approval; deny the tool request", false)
	}
	if response.Behavior == "allow" && len(strings.TrimSpace(string(response.UpdatedInput))) == 0 {
		response.UpdatedInput = append(json.RawMessage(nil), input...)
	}
	return response
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

func (s *Server) readProcessOutput(req readProcessOutputArgs) (map[string]any, bool) {
	snapshots := s.manager.SnapshotsForProject(s.projectPath)
	if len(snapshots) == 0 {
		return map[string]any{
			"success":      true,
			"found":        false,
			"project_path": s.projectPath,
			"message":      "No managed runtime processes for this project. Start one with start_process first.",
		}, false
	}
	processID := strings.TrimSpace(req.ProcessID)
	snapshot, ok := selectProcessOutputSnapshot(snapshots, processID)
	if !ok {
		return map[string]any{
			"success":      false,
			"found":        false,
			"project_path": s.projectPath,
			"process_id":   processID,
			"error":        fmt.Sprintf("No managed runtime process with id %q for this project. Call list_processes for the known ids.", processID),
		}, true
	}
	lines := cleanProcessOutputLines(snapshot.RecentOutput)
	captured := len(lines)
	truncated := false
	if req.MaxLines > 0 && len(lines) > req.MaxLines {
		lines = lines[len(lines)-req.MaxLines:]
		truncated = true
	}
	if lines == nil {
		lines = []string{}
	}
	lastError := cleanProcessOutputText(snapshot.LastError)
	crashed := !snapshot.Running && ((snapshot.ExitCodeKnown && snapshot.ExitCode != 0) || lastError != "")
	return map[string]any{
		"success":             true,
		"found":               true,
		"project_path":        s.projectPath,
		"process":             snapshotSummary(s.projectPath, snapshot),
		"crashed":             crashed,
		"output":              strings.Join(lines, "\n"),
		"output_lines":        lines,
		"captured_line_count": captured,
		"output_truncated":    truncated,
	}, false
}

func selectProcessOutputSnapshot(snapshots []projectrun.Snapshot, processID string) (projectrun.Snapshot, bool) {
	if processID != "" {
		for _, snapshot := range snapshots {
			if snapshot.ID == processID {
				return snapshot, true
			}
		}
		return projectrun.Snapshot{}, false
	}
	latest := -1
	for i := range snapshots {
		if latest == -1 || snapshots[i].StartedAt.After(snapshots[latest].StartedAt) {
			latest = i
		}
	}
	if latest == -1 {
		return projectrun.Snapshot{}, false
	}
	return snapshots[latest], true
}

func cleanProcessOutputLines(raw []string) []string {
	lines := make([]string, 0, len(raw))
	for _, line := range raw {
		line = cleanProcessOutputText(line)
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func cleanProcessOutputText(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	return strings.TrimSpace(ansi.Strip(text))
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

func runtimeTools(todoMode todocapture.CaptureMode, structuredTools, claudeApprovalEnabled bool) []mcpTool {
	tools := queryCatalogTools(structuredTools)
	tools = append(tools, controlCatalogTools(structuredTools)...)
	tools = append(tools,
		mcpTool{
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
		mcpTool{
			Name:        "read_process_output",
			Description: "Read the captured tail output and exit state of a Little Control Room managed runtime process for this project, for example to check whether the last /run or start_process command crashed. Without process_id, reads the most recently started managed runtime for this project.",
			InputSchema: map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"process_id": map[string]any{"type": "string", "description": "Optional managed process id from list_processes. Defaults to the most recently started managed runtime for this project."},
					"max_lines":  map[string]any{"type": "integer", "description": "Optional maximum number of trailing output lines to return. Defaults to every captured line; the manager keeps a bounded tail."},
				},
			},
		},
		mcpTool{
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
		mcpTool{
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
		mcpTool{
			Name:        "request_browser_attention",
			Description: "Notify Little Control Room that the already-open managed Playwright page for this same embedded session needs human interaction, such as login, MFA, consent, or CAPTCHA. Call this only after navigating to the exact page with Playwright. Provide a short user-facing instruction. On success, stop the current turn and do not call Playwright again until the user sends a new message. Do not open a separate browser context.",
			InputSchema: map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"message": map[string]any{
						"type":        "string",
						"description": "Short user-facing instruction describing the exact browser action needed, for example: Sign in to Gitea in the managed browser, then return to Little Control Room.",
						"maxLength":   managedBrowserAttentionMessageMaxRuneCount,
					},
				},
				"required": []string{"message"},
			},
		},
	)
	if claudeApprovalEnabled {
		tools = append(tools, mcpTool{
			Name:        claudeapproval.PermissionToolName,
			Description: "Reserved callback used by embedded Claude Code to ask Little Control Room for a tool approval or structured user answer. The model must not call this tool directly.",
			InputSchema: map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"tool_name":   map[string]any{"type": "string"},
					"input":       map[string]any{"type": "object"},
					"tool_use_id": map[string]any{"type": "string"},
				},
				"required": []string{"tool_name", "input", "tool_use_id"},
			},
		})
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

func queryCatalogTools(structuredTools bool) []mcpTool {
	tools := []mcpTool{
		{
			Name:        "list_lcr_queries",
			Description: "Discover Little Control Room read-only query domains without loading their schemas. With no domain, returns only domain summaries; with one exact domain, returns compact query summaries allowed by this session's query scope.",
			InputSchema: map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"domain": map[string]any{
						"type":        "string",
						"enum":        []string{string(agentquery.DomainPortfolio), string(agentquery.DomainProject), string(agentquery.DomainAssessment), string(agentquery.DomainWork)},
						"description": "Optional exact query domain. Omit to receive only domain summaries, then call again with the relevant domain.",
					},
				},
			},
		},
		{
			Name:        "describe_lcr_query",
			Description: "Load one LCR query's strict input schema, output envelope, scope, sensitivity, and freshness contract. Call after list_lcr_queries and before run_lcr_query.",
			InputSchema: map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"name": map[string]any{
						"type":        "string",
						"description": "Exact query name returned by list_lcr_queries.",
					},
				},
				"required": []string{"name"},
			},
		},
		{
			Name:        "run_lcr_query",
			Description: "Run one previously described read-only LCR query. Results are bounded structured persisted snapshots with freshness and privacy metadata; this tool never performs a control action.",
			InputSchema: map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"query": map[string]any{
						"type":        "string",
						"description": "Exact query name previously loaded with describe_lcr_query.",
					},
					"arguments": map[string]any{
						"type":        "object",
						"description": "Arguments matching the selected query's exact input_schema.",
					},
				},
				"required": []string{"query", "arguments"},
			},
		},
	}
	if !structuredTools {
		return tools
	}
	for i := range tools {
		tools[i].OutputSchema = genericObjectOutputSchema()
		tools[i].Annotations = &mcpToolAnnotations{
			Title:           queryToolTitle(tools[i].Name),
			ReadOnlyHint:    true,
			DestructiveHint: false,
			IdempotentHint:  true,
			OpenWorldHint:   false,
		}
	}
	return tools
}

func queryToolTitle(name string) string {
	switch name {
	case "list_lcr_queries":
		return "List LCR queries"
	case "describe_lcr_query":
		return "Describe LCR query"
	case "run_lcr_query":
		return "Run LCR query"
	default:
		return name
	}
}

func controlCatalogTools(structuredTools bool) []mcpTool {
	tools := []mcpTool{
		{
			Name:        "list_control_capabilities",
			Description: "List the Little Control Room control domains and compact capability summaries available to this embedded session. Optionally filter by one exact domain. This intentionally omits detailed input schemas; call describe_control_capability for one selected capability.",
			InputSchema: map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"domain": map[string]any{
						"type":        "string",
						"enum":        control.CapabilityDomainStrings(false),
						"description": "Optional exact domain filter. Omit to receive every compact summary allowed by this session's authority.",
					},
				},
			},
		},
		{
			Name:        "describe_control_capability",
			Description: "Load the exact input/output schemas, risk, scope, confirmation policy, and host effects for one Little Control Room capability. Call this after list_control_capabilities and before propose_control_operation.",
			InputSchema: map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"name": map[string]any{
						"type":        "string",
						"description": "Exact capability name returned by list_control_capabilities.",
					},
				},
				"required": []string{"name"},
			},
		},
		{
			Name:        "propose_control_operation",
			Description: "Propose one previously described Little Control Room capability. This never directly executes the action: LCR validates the exact arguments, records an idempotent operation, and asks the operator for confirmation in the TUI. After a successful proposal, stop the turn and wait for a new user message.",
			InputSchema: map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"capability": map[string]any{
						"type":        "string",
						"description": "Exact capability name previously loaded with describe_control_capability.",
					},
					"arguments": map[string]any{
						"type":        "object",
						"description": "Arguments matching the selected capability's exact input_schema. LCR supplies the internal operation request_id.",
					},
					"request_id": map[string]any{
						"type":        "string",
						"minLength":   1,
						"description": "Optional caller-stable idempotency key. Reusing it in this embedded session returns the original operation.",
					},
				},
				"required": []string{"capability", "arguments"},
			},
		},
		{
			Name:        "get_control_operation",
			Description: "Read the current state and result of a control operation proposed by this same embedded session. Call on a later user turn after the operator had an opportunity to confirm or cancel it.",
			InputSchema: map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"operation_id": map[string]any{
						"type":        "string",
						"minLength":   1,
						"description": "Operation id returned by propose_control_operation.",
					},
				},
				"required": []string{"operation_id"},
			},
		},
	}
	if !structuredTools {
		return tools
	}
	for i := range tools {
		tools[i].OutputSchema = genericObjectOutputSchema()
		tools[i].Annotations = &mcpToolAnnotations{
			Title:           controlToolTitle(tools[i].Name),
			ReadOnlyHint:    tools[i].Name != "propose_control_operation",
			DestructiveHint: false,
			IdempotentHint:  tools[i].Name != "propose_control_operation",
			OpenWorldHint:   false,
		}
	}
	return tools
}

func controlToolTitle(name string) string {
	switch name {
	case "list_control_capabilities":
		return "List LCR control capabilities"
	case "describe_control_capability":
		return "Describe LCR control capability"
	case "propose_control_operation":
		return "Propose LCR control operation"
	case "get_control_operation":
		return "Get LCR control operation"
	default:
		return name
	}
}

func genericObjectOutputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": true,
	}
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

type claudePermissionPromptArgs struct {
	ToolName  string          `json:"tool_name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
}

type listProcessesArgs struct {
	IncludeObserved    bool `json:"include_observed"`
	IncludeObservedSet bool
}

type readProcessOutputArgs struct {
	ProcessID string `json:"process_id"`
	MaxLines  int    `json:"max_lines"`
}

type listControlCapabilitiesArgs struct {
	Domain string `json:"domain"`
}

type listLCRQueriesArgs struct {
	Domain string `json:"domain"`
}

type describeLCRQueryArgs struct {
	Name string `json:"name"`
}

type runLCRQueryArgs struct {
	Query     string          `json:"query"`
	Arguments json.RawMessage `json:"arguments"`
}

type describeControlCapabilityArgs struct {
	Name string `json:"name"`
}

type proposeControlOperationArgs struct {
	Capability string          `json:"capability"`
	Arguments  json.RawMessage `json:"arguments"`
	RequestID  string          `json:"request_id"`
}

type getControlOperationArgs struct {
	OperationID string `json:"operation_id"`
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

type requestBrowserAttentionArgs struct {
	Message string `json:"message"`
}
