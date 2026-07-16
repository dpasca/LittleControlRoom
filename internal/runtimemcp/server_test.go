package runtimemcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"lcroom/internal/browserctl"
	"lcroom/internal/projectrun"
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
		!strings.Contains(string(responses[1].Result), `"stop_process"`) ||
		!strings.Contains(string(responses[1].Result), `"request_browser_attention"`) {
		t.Fatalf("tools/list result = %s, want runtime process tools", responses[1].Result)
	}
}

func TestRuntimeMCPRequestBrowserAttentionValidatesAttachedBrowser(t *testing.T) {
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
	}); err != nil {
		t.Fatalf("WriteManagedPlaywrightState() error = %v", err)
	}

	input := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"request_browser_attention","arguments":{"message":"Sign in to Gitea, then return to Little Control Room."}}}`)
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
	if len(responses) != 1 {
		t.Fatalf("responses len = %d, want 1: %s", len(responses), output.String())
	}
	payload := decodeToolJSON(t, responses[0].Result)
	if payload["success"] != true || payload["browser_pid"] != float64(4200) {
		t.Fatalf("browser attention payload = %#v, want attached browser success", payload)
	}
	if got, want := payload["requested_action"], "Sign in to Gitea, then return to Little Control Room."; got != want {
		t.Fatalf("requested action = %#v, want %q", got, want)
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

type testRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   json.RawMessage `json:"error"`
}
