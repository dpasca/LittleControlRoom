package codexapp

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"lcroom/internal/projectrun"
	"lcroom/internal/todocapture"
)

func TestClaudeRuntimeMCPConfigDefinesInlineStdioServer(t *testing.T) {
	runtimeManager := projectrun.NewManager()
	defer func() { _ = runtimeManager.CloseAll() }()

	raw, prompt, err := claudeRuntimeMCPLaunchOptions(LaunchRequest{
		Provider:          ProviderClaudeCode,
		ProjectPath:       "/tmp/demo-worktree",
		AppDataDir:        "/tmp/lcroom-data",
		RuntimeManager:    runtimeManager,
		CLIExecutablePath: "/tmp/lcroom-test-bin",
		TodoCaptureMode:   todocapture.ModeExplicit,
	})
	if err != nil {
		t.Fatalf("claudeRuntimeMCPConfig() error = %v", err)
	}
	if raw == "" {
		t.Fatal("claudeRuntimeMCPLaunchOptions() config is empty")
	}
	if want := strings.TrimSpace(todocapture.AgentInstructions(todocapture.ModeExplicit)); prompt != want {
		t.Fatalf("runtime MCP prompt = %q, want shared instructions %q", prompt, want)
	}

	var config claudeMCPConfig
	if err := json.Unmarshal([]byte(raw), &config); err != nil {
		t.Fatalf("unmarshal Claude MCP config: %v", err)
	}
	server, exists := config.Servers["lcr_runtime"]
	if !exists {
		t.Fatalf("MCP servers = %#v, want lcr_runtime", config.Servers)
	}
	if server.Type != "stdio" {
		t.Fatalf("server type = %q, want stdio", server.Type)
	}
	if server.Command != "/tmp/lcroom-test-bin" {
		t.Fatalf("server command = %q, want /tmp/lcroom-test-bin", server.Command)
	}
	for _, want := range []string{
		"runtime-mcp",
		"--provider", string(ProviderClaudeCode),
		"--project-path", "/tmp/demo-worktree",
		"--data-dir", "/tmp/lcroom-data",
	} {
		if !slices.Contains(server.Args, want) {
			t.Fatalf("server args = %#v, want %q", server.Args, want)
		}
	}
}

func TestClaudeRuntimeMCPConfigKeepsProcessToolsWhenTODOCaptureIsOff(t *testing.T) {
	runtimeManager := projectrun.NewManager()
	defer func() { _ = runtimeManager.CloseAll() }()

	raw, prompt, err := claudeRuntimeMCPLaunchOptions(LaunchRequest{
		Provider:          ProviderClaudeCode,
		ProjectPath:       "/tmp/demo",
		RuntimeManager:    runtimeManager,
		CLIExecutablePath: "/tmp/lcroom-test-bin",
		TodoCaptureMode:   todocapture.ModeOff,
	})
	if err != nil {
		t.Fatalf("claudeRuntimeMCPConfig() error = %v", err)
	}
	if raw == "" {
		t.Fatal("claudeRuntimeMCPLaunchOptions() config is empty; process tools should remain available")
	}
	if prompt != "" {
		t.Fatalf("claudeRuntimeMCPLaunchOptions() prompt = %q, want no TODO instructions", prompt)
	}
}
