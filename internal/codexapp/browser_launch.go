package codexapp

import (
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

func applyCodexPlaywrightMCPOverrides(cmd *exec.Cmd, req LaunchRequest) {
	if cmd == nil {
		return
	}
	for _, override := range codexPlaywrightMCPConfigOverrides(req) {
		cmd.Args = append(cmd.Args, "-c", override)
	}
}

func codexPlaywrightMCPConfigOverrides(req LaunchRequest) []string {
	normalized := req.PlaywrightPolicy.Normalize()
	if normalized.ManagementMode != browserctl.ManagementModeManaged {
		return nil
	}

	executablePath, err := codexPlaywrightMCPExecutablePath(req)
	if err != nil || strings.TrimSpace(executablePath) == "" {
		return nil
	}
	sessionKey := strings.TrimSpace(req.ManagedBrowserSessionKey)
	if sessionKey == "" {
		sessionKey = browserctl.NewManagedSessionKey()
	}
	profileKey := browserctl.ManagedProfileKey(
		normalized,
		string(ProviderCodex.Normalized()),
		req.ProjectPath,
		req.ResumeID,
		sessionKey,
	)
	args := []string{
		"playwright-mcp",
		"--provider", string(ProviderCodex.Normalized()),
		"--project-path", strings.TrimSpace(req.ProjectPath),
		"--data-dir", browserctl.EffectiveDataDir(req.AppDataDir),
		"--session-key", sessionKey,
		"--profile-key", profileKey,
		"--launch-mode", string(browserctl.ManagedLaunchModeForPolicy(normalized)),
	}

	return []string{
		fmt.Sprintf("mcp_servers.playwright.command=%s", strconv.Quote(executablePath)),
		fmt.Sprintf("mcp_servers.playwright.args=%s", formatCodexConfigStringArray(args)),
	}
}

func codexPlaywrightMCPExecutablePath(req LaunchRequest) (string, error) {
	if configured := strings.TrimSpace(req.CLIExecutablePath); configured != "" {
		return configured, nil
	}
	return os.Executable()
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
