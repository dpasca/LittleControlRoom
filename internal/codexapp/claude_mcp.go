package codexapp

import (
	"encoding/json"
	"strings"

	"lcroom/internal/agentquery"
	"lcroom/internal/claudeapproval"
	"lcroom/internal/todocapture"
)

const claudeRuntimeMCPApprovalTool = "mcp__lcr_runtime__" + claudeapproval.PermissionToolName

const claudeRuntimeProcessInstructions = `Little Control Room tracks this project's long-running app/server/watch processes through the lcr_runtime MCP tools.
- Call lcr_runtime/list_processes before starting a local server or watcher when a matching process may already be active.
- Prefer lcr_runtime/start_process for long-running server/watch commands; it reuses a matching command/cwd process by default. Set create_new=true only for an intentional parallel copy, and replace_existing=true only when a fresh instance is needed.
- Call lcr_runtime/read_process_output to check the tail output or exit state of a managed process.
- Call lcr_runtime/stop_process only when the user asks or to clean up a temporary process you started.`

// claudePathMentionInstructions keeps file mentions openable from the
// transcript: the link picker confirms project-relative paths against the disk
// and cannot guess which folder a bare file name belongs to.
const claudePathMentionInstructions = `When you mention a file or folder the user may want to open, write it in backticks as a path relative to the project root, for example ` + "`build/review-clips/fe-1b/title-backdrop.mp4`" + `, rather than a bare file name. Name the folder once and the files inside it by full relative path.`

type claudeMCPConfig struct {
	Servers map[string]claudeMCPServer `json:"mcpServers"`
}

type claudeMCPServer struct {
	Type    string   `json:"type"`
	Command string   `json:"command"`
	Args    []string `json:"args,omitempty"`
}

type claudeMCPOptions struct {
	Config               string
	Prompt               string
	AllowedTools         []string
	PermissionPromptTool string
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
		promptParts = append(promptParts, agentquery.KnowledgeInstructions, claudeRuntimeProcessInstructions)
		servers["lcr_runtime"] = claudeMCPServer{
			Type:    "stdio",
			Command: executablePath,
			Args:    append([]string(nil), args...),
		}
		allowedTools = append(allowedTools,
			claudeRuntimeMCPListQueriesTool,
			claudeRuntimeMCPDescribeQueryTool,
			claudeRuntimeMCPRunQueryTool,
			claudeRuntimeMCPListControlsTool,
			claudeRuntimeMCPDescribeControlTool,
			claudeRuntimeMCPProposeControlTool,
			claudeRuntimeMCPGetControlTool,
			claudeRuntimeMCPListProcessesTool,
			claudeRuntimeMCPReadProcessOutputTool,
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
	promptParts = append(promptParts, claudePathMentionInstructions)
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
	permissionPromptTool := ""
	if runtimeEnabled && strings.TrimSpace(req.ClaudeApprovalSocket) != "" {
		permissionPromptTool = claudeRuntimeMCPApprovalTool
	}
	return claudeMCPOptions{
		Config:               string(encoded),
		Prompt:               strings.Join(compactPromptParts, "\n\n"),
		AllowedTools:         allowedTools,
		PermissionPromptTool: permissionPromptTool,
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
	mcp.PermissionPromptTool = strings.TrimSpace(mcp.PermissionPromptTool)
	if mcp.PermissionPromptTool != "" {
		args = append(args, "--permission-prompt-tool", mcp.PermissionPromptTool)
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
