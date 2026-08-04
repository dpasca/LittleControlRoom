package codexapp

import (
	"encoding/json"
	"strings"

	"lcroom/internal/todocapture"
)

type claudeMCPConfig struct {
	Servers map[string]claudeMCPServer `json:"mcpServers"`
}

type claudeMCPServer struct {
	Type    string   `json:"type"`
	Command string   `json:"command"`
	Args    []string `json:"args,omitempty"`
}

type claudeMCPOptions struct {
	Config       string
	Prompt       string
	AllowedTools []string
}

func buildClaudeMCPOptions(req LaunchRequest) (claudeMCPOptions, error) {
	// Build both inline servers from one stable key even when this helper is
	// called outside Manager.Open or the Claude session constructor.
	ensureManagedPlaywrightSessionKey(&req)

	servers := make(map[string]claudeMCPServer)
	allowedTools := make([]string, 0, 8)
	promptParts := make([]string, 0, 2)

	playwrightEnabled := false
	if executablePath, args, ok := managedPlaywrightMCPCommand(req); ok {
		playwrightEnabled = true
		servers["playwright"] = claudeMCPServer{
			Type:    "stdio",
			Command: executablePath,
			Args:    append([]string(nil), args...),
		}
		allowedTools = append(allowedTools, claudePlaywrightMCPAllowedTools)
	}

	runtimeEnabled := false
	if executablePath, args, ok := runtimeMCPCommand(req); ok {
		runtimeEnabled = true
		servers["lcr_runtime"] = claudeMCPServer{
			Type:    "stdio",
			Command: executablePath,
			Args:    append([]string(nil), args...),
		}
		allowedTools = append(allowedTools,
			claudeRuntimeMCPListControlsTool,
			claudeRuntimeMCPDescribeControlTool,
			claudeRuntimeMCPProposeControlTool,
			claudeRuntimeMCPGetControlTool,
		)
		if req.TodoCaptureMode.Enabled() {
			allowedTools = append(allowedTools, claudeRuntimeMCPListTODOsTool, claudeRuntimeMCPAddTODOTool)
			promptParts = append(promptParts, strings.TrimSpace(todocapture.AgentInstructions(req.TodoCaptureMode)))
		}
	}

	if playwrightEnabled && runtimeEnabled {
		allowedTools = append(allowedTools, claudeRuntimeMCPBrowserAttentionTool)
		promptParts = append(promptParts, strings.TrimSpace(managedBrowserTurnContextText))
	}

	if len(servers) == 0 {
		return claudeMCPOptions{}, nil
	}
	encoded, err := json.Marshal(claudeMCPConfig{Servers: servers})
	if err != nil {
		return claudeMCPOptions{}, err
	}
	compactPromptParts := make([]string, 0, len(promptParts))
	for _, part := range promptParts {
		if part = strings.TrimSpace(part); part != "" {
			compactPromptParts = append(compactPromptParts, part)
		}
	}
	return claudeMCPOptions{
		Config:       string(encoded),
		Prompt:       strings.Join(compactPromptParts, "\n\n"),
		AllowedTools: allowedTools,
	}, nil
}

func claudeTurnArgsWithMCP(resumeID, model, reasoning, permissionMode string, mcp claudeMCPOptions, safetySettings string) []string {
	args := claudeTurnArgs(resumeID, model, reasoning, permissionMode)
	safetySettings = strings.TrimSpace(safetySettings)
	if safetySettings != "" {
		args = append(args, "--settings", safetySettings)
	}
	mcp.Config = strings.TrimSpace(mcp.Config)
	if mcp.Config == "" {
		return args
	}
	args = append(args, "--mcp-config", mcp.Config)
	mcp.Prompt = strings.TrimSpace(mcp.Prompt)
	if mcp.Prompt != "" {
		args = append(args, "--append-system-prompt", mcp.Prompt)
	}
	allowedTools := make([]string, 0, len(mcp.AllowedTools))
	for _, tool := range mcp.AllowedTools {
		if tool = strings.TrimSpace(tool); tool != "" {
			allowedTools = append(allowedTools, tool)
		}
	}
	if len(allowedTools) == 0 {
		return args
	}
	return append(args,
		"--allowedTools",
		strings.Join(allowedTools, ","),
	)
}
