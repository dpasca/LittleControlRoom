package codexapp

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"lcroom/internal/browserctl"
)

func applyPlaywrightPolicyEnvironment(cmd *exec.Cmd, provider Provider, policy browserctl.Policy) {
	if cmd == nil {
		return
	}
	cmd.Env = browserctl.AppendEnv(os.Environ(), string(provider.Normalized()), policy)
}

func providerSupportsManagedPlaywright(provider Provider) bool {
	switch provider.Normalized() {
	case ProviderCodex, ProviderOpenCode:
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

func applyCodexPlaywrightMCPOverrides(cmd *exec.Cmd, req LaunchRequest) {
	if cmd == nil {
		return
	}
	for _, override := range codexPlaywrightMCPConfigOverrides(req) {
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

func managedPlaywrightMCPExecutablePath(req LaunchRequest) (string, error) {
	if configured := strings.TrimSpace(req.CLIExecutablePath); configured != "" {
		return configured, nil
	}
	return os.Executable()
}

func managedPlaywrightMCPCommand(req LaunchRequest) (string, []string, bool) {
	normalized := req.PlaywrightPolicy.Normalize()
	if normalized.ManagementMode != browserctl.ManagementModeManaged {
		return "", nil, false
	}

	executablePath, err := managedPlaywrightMCPExecutablePath(req)
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
