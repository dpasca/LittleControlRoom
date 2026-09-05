package integrations

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"
)

type rpcReply struct {
	ID     json.RawMessage `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  json.RawMessage `json:"error"`
}

func (s *scan) checkMCP(parent context.Context, entry Entry) (Result, error) {
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	config := s.serverConfig(entry)
	check := Check{State: "failed", CheckedAt: time.Now().UTC()}
	var names []string
	var err error
	if textAt(config, "url") != "" {
		names, err = probeHTTP(ctx, config)
	} else {
		names, err = probeStdio(ctx, config, s.commandCWD())
	}
	if errors.Is(err, errAuthenticationRequired) {
		check.State = "authentication_required"
		check.Detail = "This separate probe requires authentication; it does not reuse the native agent's OAuth credential store. Use native MCP login for the engineer, or configure an environment-variable header for this probe."
	} else if err != nil {
		check.Detail = err.Error()
	} else {
		check.State = "reachable"
		check.Tools = names
		check.Detail = fmt.Sprintf("MCP initialization and tool discovery succeeded (%d tool names, at most 100 from the first page). This was a separate connection check; the running engineer session was not changed.", len(names))
	}
	return Result{Status: entry.Name + ": " + check.Detail, Activation: "Connection check only; no running-session availability claim.", Check: &check}, nil
}

var errAuthenticationRequired = errors.New("authentication required")

func initializeParams() map[string]any {
	return map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "little-control-room-integration-check", "version": "1"}}
}
func rpcRequest(id int, method string, params any) map[string]any {
	request := map[string]any{"jsonrpc": "2.0", "method": method, "params": params}
	if id > 0 {
		request["id"] = id
	}
	return request
}

func probeStdio(ctx context.Context, config map[string]any, cwd string) ([]string, error) {
	command := stringList(config["command"])
	if len(command) == 0 {
		if executable := textAt(config, "command"); executable != "" {
			command = append([]string{executable}, stringList(config["args"])...)
		}
	}
	if len(command) == 0 {
		return nil, fmt.Errorf("no executable is configured")
	}
	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	cmd.Dir = cwd
	cmd.Env = os.Environ()
	cmd.WaitDelay = time.Second
	configureProbeProcess(cmd)
	for _, key := range []string{"env", "environment"} {
		for name, value := range objectAt(config, key) {
			text, _ := value.(string)
			if ref, ok := environmentReference(text); ok {
				var found bool
				text, found = os.LookupEnv(ref)
				if !found {
					return nil, fmt.Errorf("required environment variable %s is not set in the LCR host", ref)
				}
			}
			cmd.Env = append(cmd.Env, name+"="+text)
		}
	}
	for _, name := range stringList(config["env_vars"]) {
		if _, ok := os.LookupEnv(name); !ok {
			return nil, fmt.Errorf("required environment variable %s is not set in the LCR host", name)
		}
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("could not prepare MCP stdin")
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return nil, fmt.Errorf("could not prepare MCP stdout")
	}
	if err := cmd.Start(); err != nil {
		stdin.Close()
		stdout.Close()
		return nil, fmt.Errorf("could not start the configured MCP executable; check installation and PATH")
	}
	defer func() { stdin.Close(); stopProbeProcess(cmd); _ = cmd.Wait() }()
	encoder := json.NewEncoder(stdin)
	decoder := json.NewDecoder(io.LimitReader(stdout, 4<<20))
	call := func(id int, method string, params any) (json.RawMessage, error) {
		if err := encoder.Encode(rpcRequest(id, method, params)); err != nil {
			return nil, fmt.Errorf("MCP transport closed")
		}
		for {
			var reply rpcReply
			if err := decoder.Decode(&reply); err != nil {
				if ctx.Err() != nil {
					return nil, fmt.Errorf("MCP connection check timed out or was canceled")
				}
				return nil, fmt.Errorf("MCP server did not return valid protocol messages")
			}
			if string(reply.ID) != fmt.Sprint(id) {
				continue
			}
			if len(reply.Error) > 0 && string(reply.Error) != "null" {
				return nil, fmt.Errorf("MCP server rejected initialization or tool discovery")
			}
			return reply.Result, nil
		}
	}
	if _, err := call(1, "initialize", initializeParams()); err != nil {
		return nil, err
	}
	if err := encoder.Encode(rpcRequest(0, "notifications/initialized", map[string]any{})); err != nil {
		return nil, fmt.Errorf("MCP transport closed")
	}
	result, err := call(2, "tools/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	return toolNames(result)
}

func probeHTTP(ctx context.Context, config map[string]any) ([]string, error) {
	endpoint := textAt(config, "url")
	if err := validateURL(endpoint, false); err != nil {
		return nil, fmt.Errorf("connection check supports HTTPS and loopback HTTP URLs without embedded credentials")
	}
	client := &http.Client{Timeout: 12 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	headers := http.Header{}
	for _, key := range []string{"headers", "http_headers"} {
		for name, value := range objectAt(config, key) {
			text, _ := value.(string)
			if ref, ok := environmentReference(text); ok {
				text = os.Getenv(ref)
				if text == "" {
					return nil, fmt.Errorf("required environment variable %s is not set in the LCR host", ref)
				}
			}
			headers.Set(name, text)
		}
	}
	for name, value := range objectAt(config, "env_http_headers") {
		ref, _ := value.(string)
		text := os.Getenv(ref)
		if text == "" {
			return nil, fmt.Errorf("required environment variable %s is not set in the LCR host", ref)
		}
		headers.Set(name, text)
	}
	if ref := textAt(config, "bearer_token_env_var"); ref != "" {
		token := os.Getenv(ref)
		if token == "" {
			return nil, fmt.Errorf("required environment variable %s is not set in the LCR host", ref)
		}
		headers.Set("Authorization", "Bearer "+token)
	}
	sessionID := ""
	protocolVersion := ""
	call := func(id int, method string, params any) (json.RawMessage, error) {
		body, _ := json.Marshal(rpcRequest(id, method, params))
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("invalid MCP endpoint")
		}
		request.Header = headers.Clone()
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Accept", "application/json, text/event-stream")
		if sessionID != "" {
			request.Header.Set("Mcp-Session-Id", sessionID)
		}
		if protocolVersion != "" {
			request.Header.Set("MCP-Protocol-Version", protocolVersion)
		}
		response, err := client.Do(request)
		if err != nil {
			return nil, fmt.Errorf("MCP endpoint could not be reached within the timeout")
		}
		defer response.Body.Close()
		if response.StatusCode == 401 || response.StatusCode == 403 {
			return nil, errAuthenticationRequired
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return nil, fmt.Errorf("MCP endpoint returned HTTP %d", response.StatusCode)
		}
		if value := response.Header.Get("Mcp-Session-Id"); value != "" {
			sessionID = value
		}
		if id == 0 {
			return nil, nil
		}
		var reply rpcReply
		if strings.Contains(response.Header.Get("Content-Type"), "text/event-stream") {
			scanner := bufio.NewScanner(io.LimitReader(response.Body, 4<<20))
			scanner.Buffer(make([]byte, 4096), 1<<20)
			for scanner.Scan() {
				line := scanner.Text()
				if !strings.HasPrefix(line, "data:") {
					continue
				}
				if json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), &reply) == nil && string(reply.ID) == fmt.Sprint(id) {
					break
				}
			}
		} else if err := json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(&reply); err != nil {
			return nil, fmt.Errorf("MCP endpoint did not return a protocol response")
		}
		if string(reply.ID) != fmt.Sprint(id) || len(reply.Error) > 0 && string(reply.Error) != "null" {
			return nil, fmt.Errorf("MCP endpoint rejected initialization or tool discovery")
		}
		return reply.Result, nil
	}
	defer func() {
		if sessionID == "" {
			return
		}
		cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		request, _ := http.NewRequestWithContext(cleanup, http.MethodDelete, endpoint, nil)
		request.Header = headers.Clone()
		request.Header.Set("Mcp-Session-Id", sessionID)
		request.Header.Set("MCP-Protocol-Version", protocolVersion)
		if response, err := client.Do(request); err == nil {
			response.Body.Close()
		}
	}()
	initialized, err := call(1, "initialize", initializeParams())
	if err != nil {
		return nil, err
	}
	var info struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if err := json.Unmarshal(initialized, &info); err != nil {
		return nil, fmt.Errorf("invalid MCP initialization response")
	}
	switch info.ProtocolVersion {
	case "2025-06-18", "2025-03-26", "2024-11-05":
		protocolVersion = info.ProtocolVersion
	default:
		return nil, fmt.Errorf("MCP server negotiated an unsupported protocol version")
	}
	if _, err := call(0, "notifications/initialized", map[string]any{}); err != nil {
		return nil, err
	}
	result, err := call(2, "tools/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	return toolNames(result)
}

func toolNames(result json.RawMessage) ([]string, error) {
	var payload struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(result, &payload); err != nil || payload.Tools == nil {
		return nil, fmt.Errorf("MCP server returned an invalid tool list")
	}
	names := []string{}
	for _, tool := range payload.Tools {
		if len(names) >= 100 {
			break
		}
		if len(tool.Name) > 128 {
			continue
		}
		names = append(names, tool.Name)
	}
	sort.Strings(names)
	return names, nil
}
