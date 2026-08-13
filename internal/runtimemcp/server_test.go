package runtimemcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lcroom/internal/browserctl"
	"lcroom/internal/claudeapproval"
	"lcroom/internal/control"
	"lcroom/internal/projectrun"
	"lcroom/internal/store"
	"lcroom/internal/todocapture"
)

func TestRuntimeMCPListsTools(t *testing.T) {
	dir := t.TempDir()
	input := strings.NewReader(strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`,
	}, "\n"))
	var output bytes.Buffer
	manager := projectrun.NewManager()
	defer func() { _ = manager.CloseAll() }()

	err := Run(context.Background(), Options{
		ProjectPath: dir,
		Input:       input,
		Output:      &output,
		Manager:     manager,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	responses := decodeResponses(t, output.String())
	if len(responses) != 2 {
		t.Fatalf("responses len = %d, want 2: %s", len(responses), output.String())
	}
	if !strings.Contains(string(responses[0].Result), serverName) {
		t.Fatalf("initialize result = %s, want server name", responses[0].Result)
	}
	if !strings.Contains(string(responses[1].Result), `"start_process"`) ||
		!strings.Contains(string(responses[1].Result), `"list_processes"`) ||
		!strings.Contains(string(responses[1].Result), `"read_process_output"`) ||
		!strings.Contains(string(responses[1].Result), `"stop_process"`) ||
		!strings.Contains(string(responses[1].Result), `"request_browser_attention"`) ||
		!strings.Contains(string(responses[1].Result), `"list_control_capabilities"`) ||
		!strings.Contains(string(responses[1].Result), `"describe_control_capability"`) ||
		!strings.Contains(string(responses[1].Result), `"propose_control_operation"`) ||
		!strings.Contains(string(responses[1].Result), `"get_control_operation"`) {
		t.Fatalf("tools/list result = %s, want runtime process tools", responses[1].Result)
	}
	if strings.Contains(string(responses[1].Result), string(control.CapabilityProjectCreateAndStartEngineer)) {
		t.Fatalf("tools/list eagerly exposes capability names instead of deferring them: %s", responses[1].Result)
	}
}

func TestRuntimeMCPClaudePermissionToolWaitsForLCRDecision(t *testing.T) {
	bridge, err := claudeapproval.NewServer()
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	t.Cleanup(func() { _ = bridge.Close() })
	manager := projectrun.NewManager()
	t.Cleanup(func() { _ = manager.CloseAll() })
	server, err := New(Options{
		ProjectPath:          t.TempDir(),
		ClaudeApprovalSocket: bridge.SocketPath(),
		Manager:              manager,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	resultCh := make(chan toolCallResult, 1)
	errCh := make(chan error, 1)
	go func() {
		result, callErr := server.handleToolCall(t.Context(), json.RawMessage(`{"name":"request_tool_approval","arguments":{"tool_name":"Write","input":{"file_path":"/tmp/demo.txt","content":"demo"},"tool_use_id":"toolu-runtime"}}`))
		resultCh <- result
		errCh <- callErr
	}()

	request := <-bridge.Requests()
	if request.ToolName != "Write" || request.ToolUseID != "toolu-runtime" {
		t.Fatalf("approval request = %#v", request)
	}
	if err := bridge.Respond(request.ID, claudeapproval.Allow(request.Input)); err != nil {
		t.Fatalf("Respond() error = %v", err)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("handleToolCall() error = %v", err)
	}
	result := <-resultCh
	if result.IsError || len(result.Content) != 1 {
		t.Fatalf("permission tool result = %#v", result)
	}
	var response claudeapproval.Response
	if err := json.Unmarshal([]byte(result.Content[0].Text), &response); err != nil {
		t.Fatalf("decode permission response: %v", err)
	}
	if response.Behavior != "allow" || !strings.Contains(string(response.UpdatedInput), `"file_path"`) {
		t.Fatalf("permission response = %#v", response)
	}
	listed := runtimeTools(todocapture.ModeOff, false, true)
	found := false
	for _, tool := range listed {
		found = found || tool.Name == claudeapproval.PermissionToolName
	}
	if !found {
		t.Fatalf("runtime tools = %#v, want Claude permission callback", listed)
	}
}

func TestRuntimeMCPRejectsClaudePermissionToolWithoutBridge(t *testing.T) {
	manager := projectrun.NewManager()
	t.Cleanup(func() { _ = manager.CloseAll() })
	server, err := New(Options{ProjectPath: t.TempDir(), Manager: manager})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	_, err = server.handleToolCall(t.Context(), json.RawMessage(`{"name":"request_tool_approval","arguments":{"tool_name":"Write","input":{"file_path":"/tmp/demo.txt"},"tool_use_id":"toolu-runtime"}}`))
	if err == nil || !strings.Contains(err.Error(), "approval routing is unavailable") {
		t.Fatalf("unconfigured permission call error = %v", err)
	}
}

func TestRuntimeMCPProgressiveControlProposal(t *testing.T) {
	projectPath := t.TempDir()
	st, err := store.Open(filepath.Join(t.TempDir(), "control.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	server, err := New(Options{
		ProjectPath:  projectPath,
		Provider:     "codex",
		SessionKey:   "session-1",
		ControlScope: control.AuthorityScopePortfolio,
		Store:        st,
		Manager:      projectrun.NewManager(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer server.manager.CloseAll()

	list := callRuntimeToolForMap(t, server, "list_control_capabilities", `{"domain":"project"}`)
	if !strings.Contains(mustJSON(t, list), string(control.CapabilityProjectCreateAndStartEngineer)) {
		t.Fatalf("project capabilities = %#v, want repository creation", list)
	}
	describe := callRuntimeToolForMap(t, server, "describe_control_capability", `{"name":"project.create_and_start_engineer"}`)
	if !strings.Contains(mustJSON(t, describe), `"input_schema"`) {
		t.Fatalf("capability description = %#v, want exact schema", describe)
	}
	parent := t.TempDir()
	proposalArgs := fmt.Sprintf(`{
		"capability":"project.create_and_start_engineer",
		"request_id":"create-demo",
		"arguments":{
			"parent_path":%q,
			"project_name":"demo",
			"todo_text":"Create the initial project",
			"prompt":"Create the initial project",
			"provider":"auto",
			"reveal":false
		}
	}`, parent)
	proposed := callRuntimeToolForMap(t, server, "propose_control_operation", proposalArgs)
	operationMap, ok := proposed["operation"].(map[string]any)
	if !ok {
		t.Fatalf("proposal = %#v, want operation", proposed)
	}
	operationID, _ := operationMap["id"].(string)
	if !control.IsExternalOperationID(operationID) {
		t.Fatalf("operation id = %q", operationID)
	}
	stored, err := st.GetControlOperation(context.Background(), operationID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Capability != control.CapabilityProjectCreateAndStartEngineer || stored.Status != control.OperationProposed {
		t.Fatalf("stored operation = %#v", stored)
	}
	replayed := callRuntimeToolForMap(t, server, "propose_control_operation", proposalArgs)
	replayedMap, _ := replayed["operation"].(map[string]any)
	if replayedMap["id"] != operationID || replayed["idempotent_replay"] != true {
		t.Fatalf("idempotent replay = %#v, want operation %q", replayed, operationID)
	}
	var conflicting proposeControlOperationArgs
	if err := json.Unmarshal([]byte(proposalArgs), &conflicting); err != nil {
		t.Fatal(err)
	}
	conflicting.Arguments = json.RawMessage(strings.Replace(string(conflicting.Arguments), "Create the initial project", "Create a different project", 1))
	if report, isErr := server.proposeControlOperation(context.Background(), conflicting); !isErr || !strings.Contains(fmt.Sprint(report["error"]), "already bound") {
		t.Fatalf("conflicting idempotency retry = %#v, error=%t", report, isErr)
	}
}

func TestControlOperationReportStopsCanceledOrFailedWorkflow(t *testing.T) {
	for _, status := range []control.OperationStatus{
		control.OperationCanceled,
		control.OperationFailed,
	} {
		t.Run(string(status), func(t *testing.T) {
			report := controlOperationReport(control.Operation{Status: status}, false)
			if report["requires_new_user_turn"] != true {
				t.Fatalf("requires_new_user_turn = %#v, want true", report["requires_new_user_turn"])
			}
			message, _ := report["message"].(string)
			if !strings.Contains(message, "Stop this turn") ||
				!strings.Contains(message, "same requested workflow") {
				t.Fatalf("message = %q, want whole-workflow stop guidance", message)
			}
		})
	}
}

func TestRuntimeMCPRequestBrowserAttentionValidatesAttachedBrowser(t *testing.T) {
	for _, tc := range []struct {
		name              string
		sessionKey        string
		browserSessionKey string
	}{
		{
			name:       "legacy shared session key",
			sessionKey: "managed-browser-session",
		},
		{
			name:              "dedicated browser session key",
			sessionKey:        "todo-capture-session",
			browserSessionKey: "managed-browser-session",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			projectPath := t.TempDir()
			dataDir := t.TempDir()
			const managedBrowserSessionKey = "managed-browser-session"
			paths, err := browserctl.ManagedPlaywrightPathsFor(
				dataDir,
				"codex",
				projectPath,
				managedBrowserSessionKey,
				"managed-browser-profile",
				browserctl.ManagedLaunchModeBackground,
			)
			if err != nil {
				t.Fatalf("ManagedPlaywrightPathsFor() error = %v", err)
			}
			if err := browserctl.WriteManagedPlaywrightState(paths, browserctl.ManagedPlaywrightState{
				SessionKey:      managedBrowserSessionKey,
				ProfileKey:      paths.ProfileKey,
				Provider:        "codex",
				ProjectPath:     projectPath,
				LaunchMode:      browserctl.ManagedLaunchModeBackground,
				Policy:          browserctl.DefaultPolicy(),
				MCPPID:          4100,
				BrowserPID:      4200,
				BrowserAppName:  "Chromium",
				RevealSupported: true,
			}); err != nil {
				t.Fatalf("WriteManagedPlaywrightState() error = %v", err)
			}

			input := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"request_browser_attention","arguments":{"message":"Sign in to Gitea, then return to Little Control Room."}}}`)
			var output bytes.Buffer
			err = Run(context.Background(), Options{
				ProjectPath:       projectPath,
				Provider:          "codex",
				DataDir:           dataDir,
				SessionKey:        tc.sessionKey,
				BrowserSessionKey: tc.browserSessionKey,
				Input:             input,
				Output:            &output,
			})
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}

			responses := decodeResponses(t, output.String())
			if len(responses) != 1 {
				t.Fatalf("responses len = %d, want 1: %s", len(responses), output.String())
			}
			payload := decodeToolJSON(t, responses[0].Result)
			if payload["success"] != true || payload["browser_pid"] != float64(4200) {
				t.Fatalf("browser attention payload = %#v, want attached browser success", payload)
			}
			if got, want := payload["session_key"], managedBrowserSessionKey; got != want {
				t.Fatalf("browser session key = %#v, want %q", got, want)
			}
			if got, want := payload["requested_action"], "Sign in to Gitea, then return to Little Control Room."; got != want {
				t.Fatalf("requested action = %#v, want %q", got, want)
			}
		})
	}
}

func TestRuntimeMCPRequestBrowserAttentionFailsBeforeBrowserLaunch(t *testing.T) {
	projectPath := t.TempDir()
	input := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"request_browser_attention","arguments":{"message":"Finish login."}}}`)
	var output bytes.Buffer

	err := Run(context.Background(), Options{
		ProjectPath: projectPath,
		Provider:    "codex",
		DataDir:     t.TempDir(),
		SessionKey:  "managed-browser-session",
		Input:       input,
		Output:      &output,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	responses := decodeResponses(t, output.String())
	if len(responses) != 1 {
		t.Fatalf("responses len = %d, want 1: %s", len(responses), output.String())
	}
	var result struct {
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(responses[0].Result, &result); err != nil {
		t.Fatalf("unmarshal tool result: %v", err)
	}
	if !result.IsError {
		t.Fatalf("request_browser_attention result = %s, want isError=true", responses[0].Result)
	}
	payload := decodeToolJSON(t, responses[0].Result)
	if payload["success"] != false || !strings.Contains(fmt.Sprint(payload["error"]), "until a Playwright browser page has opened") {
		t.Fatalf("browser attention payload = %#v, want pre-launch error", payload)
	}
}

func TestRuntimeMCPRequestBrowserAttentionRequiresMessage(t *testing.T) {
	projectPath := t.TempDir()
	input := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"request_browser_attention","arguments":{}}}`)
	var output bytes.Buffer

	err := Run(context.Background(), Options{
		ProjectPath: projectPath,
		Provider:    "codex",
		DataDir:     t.TempDir(),
		SessionKey:  "managed-browser-session",
		Input:       input,
		Output:      &output,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	responses := decodeResponses(t, output.String())
	if len(responses) != 1 {
		t.Fatalf("responses len = %d, want 1: %s", len(responses), output.String())
	}
	payload := decodeToolJSON(t, responses[0].Result)
	if payload["success"] != false || !strings.Contains(fmt.Sprint(payload["error"]), "message is required") {
		t.Fatalf("browser attention payload = %#v, want required-message error", payload)
	}
}

func TestRuntimeMCPRequestBrowserAttentionRejectsStaleBrowserHeartbeat(t *testing.T) {
	projectPath := t.TempDir()
	dataDir := t.TempDir()
	const sessionKey = "managed-browser-session"
	paths, err := browserctl.ManagedPlaywrightPathsFor(
		dataDir,
		"codex",
		projectPath,
		sessionKey,
		"managed-browser-profile",
		browserctl.ManagedLaunchModeBackground,
	)
	if err != nil {
		t.Fatalf("ManagedPlaywrightPathsFor() error = %v", err)
	}
	if err := browserctl.WriteManagedPlaywrightState(paths, browserctl.ManagedPlaywrightState{
		SessionKey:      sessionKey,
		ProfileKey:      paths.ProfileKey,
		Provider:        "codex",
		ProjectPath:     projectPath,
		LaunchMode:      browserctl.ManagedLaunchModeBackground,
		Policy:          browserctl.DefaultPolicy(),
		MCPPID:          4100,
		BrowserPID:      4200,
		BrowserAppName:  "Chromium",
		RevealSupported: true,
		UpdatedAt:       time.Now().Add(-managedBrowserAttentionHeartbeatMaxAge - time.Second),
	}); err != nil {
		t.Fatalf("WriteManagedPlaywrightState() error = %v", err)
	}

	input := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"request_browser_attention","arguments":{"message":"Finish login."}}}`)
	var output bytes.Buffer
	err = Run(context.Background(), Options{
		ProjectPath: projectPath,
		Provider:    "codex",
		DataDir:     dataDir,
		SessionKey:  sessionKey,
		Input:       input,
		Output:      &output,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	responses := decodeResponses(t, output.String())
	payload := decodeToolJSON(t, responses[0].Result)
	if payload["success"] != false || !strings.Contains(fmt.Sprint(payload["error"]), "no longer attached") {
		t.Fatalf("browser attention payload = %#v, want stale-browser error", payload)
	}
}

func TestRuntimeMCPProtocolVersionGatesStructuredToolFields(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name           string
		requested      string
		wantNegotiated string
		wantStructured bool
	}{
		{name: "legacy", requested: legacyProtocolVersion, wantNegotiated: legacyProtocolVersion},
		{name: "structured", requested: structuredToolsProtocolVersion, wantNegotiated: structuredToolsProtocolVersion, wantStructured: true},
		{name: "unknown newer revision", requested: "2025-11-25", wantNegotiated: legacyProtocolVersion},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := strings.NewReader(strings.Join([]string{
				`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"` + tc.requested + `"}}`,
				`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`,
				`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"list_project_todos","arguments":{}}}`,
			}, "\n"))
			var output bytes.Buffer
			manager := projectrun.NewManager()
			defer manager.CloseAll()
			if err := Run(context.Background(), Options{
				ProjectPath:     t.TempDir(),
				TodoCaptureMode: todocapture.ModeExplicit,
				TodoHandler:     &recordingTodoHandler{},
				Input:           input,
				Output:          &output,
				Manager:         manager,
			}); err != nil {
				t.Fatal(err)
			}

			responses := decodeResponses(t, output.String())
			if len(responses) != 3 {
				t.Fatalf("responses len = %d, want 3: %s", len(responses), output.String())
			}
			if !strings.Contains(string(responses[0].Result), `"protocolVersion":"`+tc.wantNegotiated+`"`) {
				t.Fatalf("initialize result = %s, want negotiated version %q", responses[0].Result, tc.wantNegotiated)
			}
			tools := string(responses[1].Result)
			if got := strings.Contains(tools, `"outputSchema"`) || strings.Contains(tools, `"annotations"`); got != tc.wantStructured {
				t.Fatalf("structured tool metadata visibility = %v, want %v: %s", got, tc.wantStructured, tools)
			}
			callResult := string(responses[2].Result)
			if got := strings.Contains(callResult, `"structuredContent"`); got != tc.wantStructured {
				t.Fatalf("structured result visibility = %v, want %v: %s", got, tc.wantStructured, callResult)
			}
		})
	}
}

func TestRuntimeMCPTodoToolsFollowCaptureModeAndHideScope(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name                  string
		mode                  todocapture.CaptureMode
		wantTools             bool
		wantClearDeferralEnum bool
	}{
		{name: "off", mode: todocapture.ModeOff},
		{name: "explicit", mode: todocapture.ModeExplicit, wantTools: true},
		{name: "clear deferrals", mode: todocapture.ModeExplicitAndClearDeferrals, wantTools: true, wantClearDeferralEnum: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := strings.NewReader(strings.Join([]string{
				`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
				`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`,
			}, "\n"))
			var output bytes.Buffer
			manager := projectrun.NewManager()
			defer manager.CloseAll()
			err := Run(context.Background(), Options{
				ProjectPath:     t.TempDir(),
				TodoCaptureMode: tc.mode,
				TodoHandler:     &recordingTodoHandler{},
				Input:           input,
				Output:          &output,
				Manager:         manager,
			})
			if err != nil {
				t.Fatal(err)
			}
			responses := decodeResponses(t, output.String())
			initialize := string(responses[0].Result)
			tools := string(responses[1].Result)
			if got := strings.Contains(tools, `"list_project_todos"`); got != tc.wantTools {
				t.Fatalf("TODO tool visibility = %v, want %v: %s", got, tc.wantTools, tools)
			}
			if !strings.Contains(initialize, `"instructions"`) || !strings.Contains(initialize, "list_control_capabilities") {
				t.Fatalf("initialize result is missing progressive control instructions: %s", initialize)
			}
			if got := strings.Contains(initialize, "list_project_todos"); got != tc.wantTools {
				t.Fatalf("TODO instructions visibility = %v, want %v: %s", got, tc.wantTools, initialize)
			}
			if strings.Contains(tools, `"project_path"`) {
				t.Fatalf("TODO schemas expose model-controlled project_path: %s", tools)
			}
			clearEnum := `"enum":["explicit_request","clear_deferral"]`
			if got := strings.Contains(tools, clearEnum); got != tc.wantClearDeferralEnum {
				t.Fatalf("clear_deferral enum visibility = %v, want %v: %s", got, tc.wantClearDeferralEnum, tools)
			}
		})
	}
}

func TestRuntimeMCPTodoCallsInjectTrustedOrigin(t *testing.T) {
	t.Parallel()
	handler := &recordingTodoHandler{}
	input := strings.NewReader(strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_project_todos","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"add_project_todo","arguments":{"text":"Add keyboard navigation","capture_kind":"explicit_request","review_revision":"rev-1","project_path":"/attacker"}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"add_project_todo","arguments":{"text":"Add keyboard navigation","capture_kind":"explicit_request","review_revision":"rev-1"}}}`,
	}, "\n"))
	var output bytes.Buffer
	manager := projectrun.NewManager()
	defer manager.CloseAll()
	err := Run(context.Background(), Options{
		ProjectPath:       "/trusted/project",
		Provider:          "claude_code",
		SessionKey:        "trusted-session",
		BrowserSessionKey: "managed-browser-session",
		TodoCaptureMode:   todocapture.ModeExplicit,
		TodoHandler:       handler,
		Input:             input,
		Output:            &output,
		Manager:           manager,
	})
	if err != nil {
		t.Fatal(err)
	}
	responses := decodeResponses(t, output.String())
	if len(responses) != 3 || len(handler.requests) != 2 {
		t.Fatalf("responses=%d requests=%d output=%s", len(responses), len(handler.requests), output.String())
	}
	if len(responses[1].Error) == 0 || string(responses[1].Error) == "null" {
		t.Fatalf("forged extra field did not fail schema/decoding path: %s", responses[1].Result)
	}
	for _, request := range handler.requests {
		if request.Origin.ProjectPath != "/trusted/project" || request.Origin.Provider != "claude_code" || request.Origin.SessionKey != "trusted-session" {
			t.Fatalf("origin = %#v", request.Origin)
		}
	}
	if handler.requests[1].Add.Text != "Add keyboard navigation" {
		t.Fatalf("add request = %#v", handler.requests[1].Add)
	}
}

type recordingTodoHandler struct {
	requests []todocapture.Request
}

func (h *recordingTodoHandler) HandleTodoCapture(_ context.Context, request todocapture.Request) (todocapture.Response, error) {
	h.requests = append(h.requests, request)
	switch request.Action {
	case todocapture.ActionList:
		return todocapture.Response{List: &todocapture.ListResult{ReviewRevision: "rev-1", OpenTodos: []todocapture.Todo{}}}, nil
	case todocapture.ActionAdd:
		todo := todocapture.Todo{ID: 1, Text: request.Add.Text}
		return todocapture.Response{Add: &todocapture.AddResult{Disposition: todocapture.DispositionCreated, Todo: &todo, CurrentRevision: "rev-2"}}, nil
	default:
		return todocapture.Response{}, errors.New("unexpected action")
	}
}

func TestRuntimeMCPStartProcessReusesMatchingProcess(t *testing.T) {
	dir := t.TempDir()
	manager := projectrun.NewManager()
	defer func() { _ = manager.CloseAll() }()

	input := strings.NewReader(strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"start_process","arguments":{"command":"sleep 30","name":"dev-server"}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"start_process","arguments":{"command":"sleep 30","name":"dev-server"}}}`,
	}, "\n"))
	var output bytes.Buffer

	err := Run(context.Background(), Options{
		ProjectPath: dir,
		Input:       input,
		Output:      &output,
		Manager:     manager,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	responses := decodeResponses(t, output.String())
	if len(responses) != 2 {
		t.Fatalf("responses len = %d, want 2: %s", len(responses), output.String())
	}
	first := decodeToolJSON(t, responses[0].Result)
	second := decodeToolJSON(t, responses[1].Result)
	if first["disposition"] != string(projectrun.StartDispositionStarted) {
		t.Fatalf("first disposition = %#v, want started; response=%#v", first["disposition"], first)
	}
	if second["disposition"] != string(projectrun.StartDispositionReused) {
		t.Fatalf("second disposition = %#v, want reused; response=%#v", second["disposition"], second)
	}

	running := 0
	for _, snapshot := range manager.SnapshotsForProject(dir) {
		if snapshot.Running {
			running++
		}
	}
	if running != 1 {
		t.Fatalf("running snapshots = %d, want 1: %+v", running, manager.SnapshotsForProject(dir))
	}
}

func TestRuntimeMCPReadProcessOutput(t *testing.T) {
	dir := t.TempDir()
	manager := projectrun.NewManager()
	defer func() { _ = manager.CloseAll() }()

	server, err := New(Options{ProjectPath: dir, Manager: manager})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	empty := callRuntimeToolForMap(t, server, "read_process_output", `{}`)
	if empty["found"] != false {
		t.Fatalf("empty found = %#v, want false: %#v", empty["found"], empty)
	}

	started := callRuntimeToolForMap(t, server, "start_process", `{"command":"sh -c 'echo alpha; echo beta; echo gamma; exit 3'","name":"crasher"}`)
	if started["disposition"] != string(projectrun.StartDispositionStarted) {
		t.Fatalf("start disposition = %#v: %#v", started["disposition"], started)
	}
	crashed := waitForRuntimeSnapshot(t, manager, dir, func(snapshot projectrun.Snapshot) bool {
		return !snapshot.Running && snapshot.ExitCodeKnown && len(snapshot.RecentOutput) >= 3
	})

	report := callRuntimeToolForMap(t, server, "read_process_output", `{}`)
	if report["found"] != true || report["crashed"] != true {
		t.Fatalf("report found/crashed = %#v/%#v: %#v", report["found"], report["crashed"], report)
	}
	process, _ := report["process"].(map[string]any)
	if process == nil {
		t.Fatalf("report process missing: %#v", report)
	}
	if process["id"] != crashed.ID {
		t.Fatalf("process id = %#v, want %q", process["id"], crashed.ID)
	}
	if code, ok := process["exit_code"].(float64); !ok || int(code) != 3 {
		t.Fatalf("exit_code = %#v, want 3: %#v", process["exit_code"], report)
	}
	output, _ := report["output"].(string)
	for _, want := range []string{"alpha", "beta", "gamma"} {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q: %q", want, output)
		}
	}

	tailed := callRuntimeToolForMap(t, server, "read_process_output", `{"max_lines":2}`)
	if lines, _ := tailed["output_lines"].([]any); len(lines) != 2 {
		t.Fatalf("output_lines = %#v, want 2 lines", tailed["output_lines"])
	}
	if tailed["output_truncated"] != true {
		t.Fatalf("output_truncated = %#v, want true", tailed["output_truncated"])
	}
	if tailOutput, _ := tailed["output"].(string); strings.Contains(tailOutput, "alpha") {
		t.Fatalf("tailed output should drop the earliest line: %q", tailOutput)
	}

	missingParams := fmt.Sprintf(`{"name":%q,"arguments":%s}`, "read_process_output", `{"process_id":"missing-id"}`)
	missingResult, err := server.handleToolCall(context.Background(), json.RawMessage(missingParams))
	if err != nil {
		t.Fatalf("read_process_output missing id call failed: %v", err)
	}
	if !missingResult.IsError {
		t.Fatalf("missing process id should be an error result: %#v", missingResult.Content)
	}

	callRuntimeToolForMap(t, server, "start_process", `{"command":"sleep 30","name":"sleeper"}`)
	latest := callRuntimeToolForMap(t, server, "read_process_output", `{}`)
	latestProcess, _ := latest["process"].(map[string]any)
	if latestProcess["running"] != true || latest["crashed"] != false {
		t.Fatalf("latest report should prefer the most recently started runtime: %#v", latest)
	}

	byID := callRuntimeToolForMap(t, server, "read_process_output", mustJSON(t, map[string]any{"process_id": crashed.ID}))
	if byID["crashed"] != true {
		t.Fatalf("by-id report crashed = %#v, want true: %#v", byID["crashed"], byID)
	}
}

func waitForRuntimeSnapshot(t *testing.T, manager *projectrun.Manager, projectPath string, want func(projectrun.Snapshot) bool) projectrun.Snapshot {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		snapshots := manager.SnapshotsForProject(projectPath)
		for _, snapshot := range snapshots {
			if want(snapshot) {
				return snapshot
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for runtime snapshot: %+v", snapshots)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func decodeResponses(t *testing.T, text string) []testRPCResponse {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(text))
	var out []testRPCResponse
	for {
		var response testRPCResponse
		if err := decoder.Decode(&response); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			t.Fatalf("decode response: %v\n%s", err, text)
		}
		out = append(out, response)
	}
	return out
}

func decodeToolJSON(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal tool result: %v\n%s", err, raw)
	}
	if len(result.Content) != 1 {
		t.Fatalf("content len = %d, want 1: %#v", len(result.Content), result)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(result.Content[0].Text), &payload); err != nil {
		t.Fatalf("unmarshal tool text: %v\n%s", err, result.Content[0].Text)
	}
	return payload
}

func callRuntimeToolForMap(t *testing.T, server *Server, name, arguments string) map[string]any {
	t.Helper()
	params := fmt.Sprintf(`{"name":%q,"arguments":%s}`, name, arguments)
	result, err := server.handleToolCall(context.Background(), json.RawMessage(params))
	if err != nil {
		t.Fatalf("%s call failed: %v", name, err)
	}
	if len(result.Content) != 1 {
		t.Fatalf("%s content = %#v", name, result.Content)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(result.Content[0].Text), &payload); err != nil {
		t.Fatalf("decode %s result: %v\n%s", name, err, result.Content[0].Text)
	}
	if result.IsError {
		t.Fatalf("%s returned error: %#v", name, payload)
	}
	return payload
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

type testRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   json.RawMessage `json:"error"`
}
