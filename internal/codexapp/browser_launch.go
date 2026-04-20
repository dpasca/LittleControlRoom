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

func applyCodexPlaywrightMCPOverrides(cmd *exec.Cmd, policy browserctl.Policy) {
	if cmd == nil {
		return
	}
	for _, override := range codexPlaywrightMCPConfigOverrides(policy) {
		cmd.Args = append(cmd.Args, "-c", override)
	}
}

func codexPlaywrightMCPConfigOverrides(policy browserctl.Policy) []string {
	normalized := policy.Normalize()
	if normalized.ManagementMode != browserctl.ManagementModeManaged {
		return nil
	}

	args := []string{"--isolated"}
	if normalized.DefaultBrowserMode == browserctl.BrowserModeHeadless {
		args = append([]string{"--headless"}, args...)
	}

	return []string{
		`mcp_servers.playwright.command="mcp-server-playwright"`,
		fmt.Sprintf("mcp_servers.playwright.args=%s", formatCodexConfigStringArray(args)),
	}
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
