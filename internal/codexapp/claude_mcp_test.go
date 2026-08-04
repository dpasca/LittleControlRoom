package codexapp

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"lcroom/internal/browserctl"
	"lcroom/internal/projectrun"
	"lcroom/internal/todocapture"
)

func TestClaudeMCPOptionsDefineRuntimeAndManagedPlaywrightServers(t *testing.T) {
	runtimeManager := projectrun.NewManager()
	defer func() { _ = runtimeManager.CloseAll() }()

	options, err := buildClaudeMCPOptions(LaunchRequest{
		Provider:                 ProviderClaudeCode,
		ProjectPath:              "/tmp/demo-worktree",
		AppDataDir:               "/tmp/lcroom-data",
		RuntimeManager:           runtimeManager,
		CLIExecutablePath:        "/tmp/lcroom-test-bin",
		TodoCaptureMode:          todocapture.ModeExplicit,
		ManagedBrowserSessionKey: "browser-session",
		PlaywrightPolicy:         browserctl.DefaultPolicy(),
	})
	if err != nil {
		t.Fatalf("buildClaudeMCPOptions() error = %v", err)
	}
	if options.Config == "" {
		t.Fatal("buildClaudeMCPOptions() config is empty")
	}
	for _, want := range []string{
		strings.TrimSpace(todocapture.AgentInstructions(todocapture.ModeExplicit)),
		"Little Control Room managed-browser contract",
		"lcr_runtime/request_browser_attention",
	} {
		if !strings.Contains(options.Prompt, want) {
			t.Fatalf("MCP prompt = %q, want %q", options.Prompt, want)
		}
	}
	for _, want := range []string{
		claudePlaywrightMCPAllowedTools,
		claudeRuntimeMCPBrowserAttentionTool,
		claudeRuntimeMCPListTODOsTool,
		claudeRuntimeMCPAddTODOTool,
	} {
		if !slices.Contains(options.AllowedTools, want) {
			t.Fatalf("allowed tools = %#v, want %q", options.AllowedTools, want)
		}
	}

	var config claudeMCPConfig
	if err := json.Unmarshal([]byte(options.Config), &config); err != nil {
		t.Fatalf("unmarshal Claude MCP config: %v", err)
	}
	runtimeServer, exists := config.Servers["lcr_runtime"]
	if !exists {
		t.Fatalf("MCP servers = %#v, want lcr_runtime", config.Servers)
	}
	if runtimeServer.Type != "stdio" {
		t.Fatalf("runtime server type = %q, want stdio", runtimeServer.Type)
	}
	if runtimeServer.Command != "/tmp/lcroom-test-bin" {
		t.Fatalf("runtime server command = %q, want /tmp/lcroom-test-bin", runtimeServer.Command)
	}
	for _, want := range []string{
		"runtime-mcp",
		"--provider", string(ProviderClaudeCode),
		"--project-path", "/tmp/demo-worktree",
		"--data-dir", "/tmp/lcroom-data",
		"--browser-session-key", "browser-session",
	} {
		if !slices.Contains(runtimeServer.Args, want) {
			t.Fatalf("runtime server args = %#v, want %q", runtimeServer.Args, want)
		}
	}

	playwrightServer, exists := config.Servers["playwright"]
	if !exists {
		t.Fatalf("MCP servers = %#v, want playwright", config.Servers)
	}
	if playwrightServer.Type != "stdio" || playwrightServer.Command != "/tmp/lcroom-test-bin" {
		t.Fatalf("playwright server = %#v, want LCR stdio wrapper", playwrightServer)
	}
	for _, want := range []string{
		"playwright-mcp",
		"--provider", string(ProviderClaudeCode),
		"--project-path", "/tmp/demo-worktree",
		"--session-key", "browser-session",
	} {
		if !slices.Contains(playwrightServer.Args, want) {
			t.Fatalf("playwright server args = %#v, want %q", playwrightServer.Args, want)
		}
	}
}

func TestClaudeRuntimeMCPConfigKeepsProcessToolsWhenTODOCaptureIsOff(t *testing.T) {
	runtimeManager := projectrun.NewManager()
	defer func() { _ = runtimeManager.CloseAll() }()

	options, err := buildClaudeMCPOptions(LaunchRequest{
		Provider:          ProviderClaudeCode,
		ProjectPath:       "/tmp/demo",
		RuntimeManager:    runtimeManager,
		CLIExecutablePath: "/tmp/lcroom-test-bin",
		TodoCaptureMode:   todocapture.ModeOff,
		PlaywrightPolicy: browserctl.Policy{
			ManagementMode: browserctl.ManagementModeLegacy,
		},
	})
	if err != nil {
		t.Fatalf("buildClaudeMCPOptions() error = %v", err)
	}
	if options.Config == "" {
		t.Fatal("buildClaudeMCPOptions() config is empty; process tools should remain available")
	}
	if options.Prompt != "" {
		t.Fatalf("buildClaudeMCPOptions() prompt = %q, want no TODO or browser instructions", options.Prompt)
	}
	if slices.Contains(options.AllowedTools, claudePlaywrightMCPAllowedTools) {
		t.Fatalf("classic browser allowed tools = %#v, want no Playwright wildcard", options.AllowedTools)
	}
}

func TestClaudeManagedPlaywrightConfigDoesNotRequireRuntimeMCP(t *testing.T) {
	options, err := buildClaudeMCPOptions(LaunchRequest{
		Provider:                 ProviderClaudeCode,
		ProjectPath:              "/tmp/demo",
		CLIExecutablePath:        "/tmp/lcroom-test-bin",
		ManagedBrowserSessionKey: "browser-session",
		PlaywrightPolicy:         browserctl.DefaultPolicy(),
	})
	if err != nil {
		t.Fatalf("buildClaudeMCPOptions() error = %v", err)
	}
	var config claudeMCPConfig
	if err := json.Unmarshal([]byte(options.Config), &config); err != nil {
		t.Fatalf("unmarshal Claude MCP config: %v", err)
	}
	if _, exists := config.Servers["playwright"]; !exists {
		t.Fatalf("MCP servers = %#v, want standalone managed Playwright", config.Servers)
	}
	if _, exists := config.Servers["lcr_runtime"]; exists {
		t.Fatalf("MCP servers = %#v, want no unavailable runtime server", config.Servers)
	}
	if !slices.Contains(options.AllowedTools, claudePlaywrightMCPAllowedTools) {
		t.Fatalf("allowed tools = %#v, want Playwright wildcard", options.AllowedTools)
	}
	if slices.Contains(options.AllowedTools, claudeRuntimeMCPBrowserAttentionTool) {
		t.Fatalf("allowed tools = %#v, want no unavailable browser-attention tool", options.AllowedTools)
	}
}

func TestClaudeMCPOptionsGenerateOneSharedBrowserSessionKey(t *testing.T) {
	runtimeManager := projectrun.NewManager()
	defer func() { _ = runtimeManager.CloseAll() }()

	options, err := buildClaudeMCPOptions(LaunchRequest{
		Provider:          ProviderClaudeCode,
		ProjectPath:       "/tmp/demo",
		CLIExecutablePath: "/tmp/lcroom-test-bin",
		RuntimeManager:    runtimeManager,
		PlaywrightPolicy:  browserctl.DefaultPolicy(),
	})
	if err != nil {
		t.Fatalf("buildClaudeMCPOptions() error = %v", err)
	}
	var config claudeMCPConfig
	if err := json.Unmarshal([]byte(options.Config), &config); err != nil {
		t.Fatalf("unmarshal Claude MCP config: %v", err)
	}
	playwrightKey := claudeMCPArgValue(config.Servers["playwright"].Args, "--session-key")
	runtimeKey := claudeMCPArgValue(config.Servers["lcr_runtime"].Args, "--browser-session-key")
	if playwrightKey == "" || runtimeKey == "" {
		t.Fatalf("generated browser keys = playwright %q, runtime %q; want both populated", playwrightKey, runtimeKey)
	}
	if playwrightKey != runtimeKey {
		t.Fatalf("generated browser keys = playwright %q, runtime %q; want one shared key", playwrightKey, runtimeKey)
	}
}

func claudeMCPArgValue(args []string, name string) string {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == name {
			return args[i+1]
		}
	}
	return ""
}
