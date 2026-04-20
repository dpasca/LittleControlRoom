package codexapp

import (
	"os/exec"
	"reflect"
	"testing"

	"lcroom/internal/browserctl"
)

func TestCodexPlaywrightMCPConfigOverridesManagedHeadless(t *testing.T) {
	policy := browserctl.Policy{
		ManagementMode:     browserctl.ManagementModeManaged,
		DefaultBrowserMode: browserctl.BrowserModeHeadless,
		LoginMode:          browserctl.LoginModePromote,
		IsolationScope:     browserctl.IsolationScopeTask,
	}

	got := codexPlaywrightMCPConfigOverrides(policy)
	want := []string{
		`mcp_servers.playwright.command="mcp-server-playwright"`,
		`mcp_servers.playwright.args=["--headless","--isolated"]`,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("codexPlaywrightMCPConfigOverrides() = %#v, want %#v", got, want)
	}
}

func TestCodexPlaywrightMCPConfigOverridesManagedHeaded(t *testing.T) {
	policy := browserctl.Policy{
		ManagementMode:     browserctl.ManagementModeManaged,
		DefaultBrowserMode: browserctl.BrowserModeHeaded,
		LoginMode:          browserctl.LoginModePromote,
		IsolationScope:     browserctl.IsolationScopeProject,
	}

	got := codexPlaywrightMCPConfigOverrides(policy)
	want := []string{
		`mcp_servers.playwright.command="mcp-server-playwright"`,
		`mcp_servers.playwright.args=["--isolated"]`,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("codexPlaywrightMCPConfigOverrides() = %#v, want %#v", got, want)
	}
}

func TestCodexPlaywrightMCPConfigOverridesClassicBrowserBehavior(t *testing.T) {
	policy := browserctl.Policy{
		ManagementMode:     browserctl.ManagementModeLegacy,
		DefaultBrowserMode: browserctl.BrowserModeHeaded,
		LoginMode:          browserctl.LoginModeManual,
		IsolationScope:     browserctl.IsolationScopeProject,
	}

	if got := codexPlaywrightMCPConfigOverrides(policy); got != nil {
		t.Fatalf("codexPlaywrightMCPConfigOverrides() = %#v, want nil", got)
	}
}

func TestApplyCodexPlaywrightMCPOverridesAppendsConfigArgs(t *testing.T) {
	cmd := exec.Command("codex", "app-server")
	applyCodexPlaywrightMCPOverrides(cmd, browserctl.Policy{
		ManagementMode:     browserctl.ManagementModeManaged,
		DefaultBrowserMode: browserctl.BrowserModeHeadless,
		LoginMode:          browserctl.LoginModePromote,
		IsolationScope:     browserctl.IsolationScopeTask,
	})

	want := []string{
		"codex",
		"app-server",
		"-c",
		`mcp_servers.playwright.command="mcp-server-playwright"`,
		"-c",
		`mcp_servers.playwright.args=["--headless","--isolated"]`,
	}
	if !reflect.DeepEqual(cmd.Args, want) {
		t.Fatalf("cmd.Args = %#v, want %#v", cmd.Args, want)
	}
}
