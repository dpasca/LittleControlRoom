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

func claudeRuntimeMCPConfig(req LaunchRequest) (string, bool, error) {
	executablePath, args, ok := runtimeMCPCommand(req)
	if !ok {
		return "", false, nil
	}
	encoded, err := json.Marshal(claudeMCPConfig{
		Servers: map[string]claudeMCPServer{
			"lcr_runtime": {
				Type:    "stdio",
				Command: executablePath,
				Args:    append([]string(nil), args...),
			},
		},
	})
	if err != nil {
		return "", false, err
	}
	return string(encoded), true, nil
}

func claudeRuntimeMCPLaunchOptions(req LaunchRequest) (config, prompt string, err error) {
	config, ok, err := claudeRuntimeMCPConfig(req)
	if err != nil || !ok {
		return "", "", err
	}
	if req.TodoCaptureMode.Enabled() {
		prompt = strings.TrimSpace(todocapture.AgentInstructions(req.TodoCaptureMode))
	}
	return config, prompt, nil
}

func claudeTurnArgsWithRuntimeMCP(resumeID, model, reasoning, permissionMode, runtimeMCPConfig, runtimeMCPPrompt string) []string {
	args := claudeTurnArgs(resumeID, model, reasoning, permissionMode)
	runtimeMCPConfig = strings.TrimSpace(runtimeMCPConfig)
	if runtimeMCPConfig == "" {
		return args
	}
	args = append(args, "--mcp-config", runtimeMCPConfig)
	runtimeMCPPrompt = strings.TrimSpace(runtimeMCPPrompt)
	if runtimeMCPPrompt == "" {
		return args
	}
	args = append(args, "--append-system-prompt", runtimeMCPPrompt)
	return append(args,
		"--allowedTools",
		claudeRuntimeMCPListTODOsTool+","+claudeRuntimeMCPAddTODOTool,
	)
}
