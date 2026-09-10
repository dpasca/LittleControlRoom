package codexapp

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"lcroom/internal/browserctl"
	"lcroom/internal/todocapture"
)

func applyPlaywrightPolicyEnvironment(cmd *exec.Cmd, provider Provider, policy browserctl.Policy) {
	if cmd == nil {
		return
	}
	cmd.Env = browserctl.AppendEnv(os.Environ(), string(provider.Normalized()), policy)
}

func providerSupportsManagedPlaywright(provider Provider) bool {
	switch provider.Normalized() {
	case ProviderCodex, ProviderOpenCode, ProviderClaudeCode, ProviderLCAgent:
		return true
	default:
		return false
	}
}

func providerSupportsRuntimeMCP(provider Provider) bool {
	switch provider.Normalized() {
	case ProviderCodex, ProviderOpenCode, ProviderClaudeCode:
		return true
	default:
		return false
	}
}

func ensureManagedPlaywrightSessionKey(req *LaunchRequest) {
	if req == nil {
		return
	}
	if strings.TrimSpace(req.ManagedBrowserSessionKey) != "" {
		return
	}
	if !providerSupportsManagedPlaywright(req.Provider) {
		return
	}
	if req.PlaywrightPolicy.Normalize().UsesLegacyLaunchBehavior() {
		return
	}
	req.ManagedBrowserSessionKey = browserctl.NewManagedSessionKey()
}

func ensureTodoCaptureSessionKey(req *LaunchRequest) {
	if req == nil || strings.TrimSpace(req.TodoCaptureSessionKey) != "" {
		return
	}
	if resumeID := strings.TrimSpace(req.ResumeID); resumeID != "" && !req.ForceNew {
		req.TodoCaptureSessionKey = resumeID
		return
	}
	// The key also isolates progressive control operations and their idempotency
	// records, so every runtime MCP session needs one even when TODO capture is
	// disabled.
	req.TodoCaptureSessionKey = browserctl.NewManagedSessionKey()
}

func applyCodexPlaywrightMCPOverrides(cmd *exec.Cmd, req LaunchRequest) {
	if cmd == nil {
		return
	}
	for _, override := range codexPlaywrightMCPConfigOverrides(req) {
		cmd.Args = append(cmd.Args, "-c", override)
	}
}

func applyCodexMCPOverrides(cmd *exec.Cmd, req LaunchRequest) {
	if cmd == nil {
		return
	}
	applyCodexPlaywrightMCPOverrides(cmd, req)
	for _, override := range codexRuntimeMCPConfigOverrides(req) {
		cmd.Args = append(cmd.Args, "-c", override)
	}
}

func codexPlaywrightMCPConfigOverrides(req LaunchRequest) []string {
	executablePath, args, ok := managedPlaywrightMCPCommand(req)
	if !ok {
		return nil
	}

	return []string{
		fmt.Sprintf("mcp_servers.playwright.command=%s", strconv.Quote(executablePath)),
		fmt.Sprintf("mcp_servers.playwright.args=%s", formatCodexConfigStringArray(args)),
	}
}

func codexRuntimeMCPConfigOverrides(req LaunchRequest) []string {
	executablePath, args, ok := runtimeMCPCommand(req)
	if !ok {
		return nil
	}
	overrides := []string{
		fmt.Sprintf("mcp_servers.lcr_runtime.command=%s", strconv.Quote(executablePath)),
		fmt.Sprintf("mcp_servers.lcr_runtime.args=%s", formatCodexConfigStringArray(args)),
	}
	if req.ImageReviewEnabled {
		overrides = append(overrides,
			`mcp_servers.lcr_runtime.env_vars=["LCR_IMAGE_REVIEW_API_KEY","OPENAI_API_KEY","OPENAI_BASE_URL"]`,
			"mcp_servers.lcr_runtime.tool_timeout_sec=150",
		)
	}
	return overrides
}

func lcrCLIExecutablePath(req LaunchRequest) (string, error) {
	if configured := strings.TrimSpace(req.CLIExecutablePath); configured != "" {
		return configured, nil
	}
	executablePath, err := os.Executable()
	if err != nil {
		return "", err
	}
	return durableLCRCLIExecutablePath(executablePath, req.AppDataDir)
}

func managedPlaywrightMCPCommand(req LaunchRequest) (string, []string, bool) {
	normalized := req.PlaywrightPolicy.Normalize()
	if normalized.ManagementMode != browserctl.ManagementModeManaged {
		return "", nil, false
	}

	executablePath, err := lcrCLIExecutablePath(req)
	if err != nil || strings.TrimSpace(executablePath) == "" {
		return "", nil, false
	}

	provider := req.Provider.Normalized()
	if provider == "" || !providerSupportsManagedPlaywright(provider) {
		return "", nil, false
	}

	sessionKey := strings.TrimSpace(req.ManagedBrowserSessionKey)
	if sessionKey == "" {
		sessionKey = browserctl.NewManagedSessionKey()
	}
	profileKey := browserctl.ManagedProfileKey(
		normalized,
		string(provider),
		req.ProjectPath,
		req.ResumeID,
		sessionKey,
	)
	args := []string{
		"playwright-mcp",
		"--provider", string(provider),
		"--project-path", strings.TrimSpace(req.ProjectPath),
		"--data-dir", browserctl.EffectiveDataDir(req.AppDataDir),
		"--session-key", sessionKey,
		"--profile-key", profileKey,
		"--launch-mode", string(browserctl.ManagedLaunchModeForPolicy(normalized)),
	}
	return executablePath, args, true
}

func runtimeMCPCommand(req LaunchRequest) (string, []string, bool) {
	if req.RuntimeManager == nil {
		return "", nil, false
	}
	provider := req.Provider.Normalized()
	if provider == "" {
		provider = ProviderCodex
	}
	if !providerSupportsRuntimeMCP(provider) {
		return "", nil, false
	}
	projectPath := strings.TrimSpace(req.ProjectPath)
	if projectPath == "" {
		return "", nil, false
	}
	executablePath, err := lcrCLIExecutablePath(req)
	if err != nil || strings.TrimSpace(executablePath) == "" {
		return "", nil, false
	}
	args := []string{
		"runtime-mcp",
		"--provider", string(provider),
		"--project-path", projectPath,
		"--control-scope", "portfolio",
		"--query-scope", "portfolio",
	}
	if req.ImageReviewEnabled && provider == ProviderCodex {
		args = append(args, "--image-review")
	}
	if dataDir := strings.TrimSpace(req.AppDataDir); dataDir != "" {
		args = append(args, "--data-dir", dataDir)
	}
	if dbPath := strings.TrimSpace(req.AppDBPath); dbPath != "" {
		args = append(args, "--db-path", dbPath)
	}
	args = append(args, "--todo-capture-mode", string(todocapture.NormalizeCaptureMode(req.TodoCaptureMode)))
	if browserSessionKey := strings.TrimSpace(req.ManagedBrowserSessionKey); browserSessionKey != "" {
		args = append(args, "--browser-session-key", browserSessionKey)
	}
	if approvalSocket := strings.TrimSpace(req.ClaudeApprovalSocket); approvalSocket != "" {
		args = append(args, "--claude-approval-socket", approvalSocket)
	}
	sessionKey := strings.TrimSpace(req.TodoCaptureSessionKey)
	if sessionKey == "" && !req.ForceNew {
		sessionKey = strings.TrimSpace(req.ResumeID)
	}
	if sessionKey != "" {
		args = append(args, "--session-key", sessionKey)
	}
	return executablePath, args, true
}

type openCodeMCPServerOverride struct {
	Type    string   `json:"type,omitempty"`
	Command []string `json:"command,omitempty"`
	Enabled *bool    `json:"enabled,omitempty"`
}

func openCodePlaywrightMCPOverride(req LaunchRequest) (json.RawMessage, bool, error) {
	executablePath, args, ok := managedPlaywrightMCPCommand(req)
	if !ok {
		return nil, false, nil
	}
	enabled := true
	raw, err := json.Marshal(openCodeMCPServerOverride{
		Type:    "local",
		Command: append([]string{executablePath}, args...),
		Enabled: &enabled,
	})
	if err != nil {
		return nil, false, err
	}
	return raw, true, nil
}

func openCodeRuntimeMCPOverride(req LaunchRequest) (json.RawMessage, bool, error) {
	executablePath, args, ok := runtimeMCPCommand(req)
	if !ok {
		return nil, false, nil
	}
	enabled := true
	raw, err := json.Marshal(openCodeMCPServerOverride{
		Type:    "local",
		Command: append([]string{executablePath}, args...),
		Enabled: &enabled,
	})
	if err != nil {
		return nil, false, err
	}
	return raw, true, nil
}

func formatCodexConfigStringArray(values []string) string {
	if len(values) == 0 {
		return "[]"
	}
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		quoted = append(quoted, strconv.Quote(strings.TrimSpace(value)))
	}
	return "[" + strings.Join(quoted, ",") + "]"
}
