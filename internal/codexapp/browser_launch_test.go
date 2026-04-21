package codexapp

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"lcroom/internal/browserctl"
)

func TestCodexPlaywrightMCPConfigOverridesManagedHeadless(t *testing.T) {
	req := LaunchRequest{
		Provider:    ProviderCodex,
		ProjectPath: "/tmp/demo",
		AppDataDir:  "/tmp/lcr-data",
		PlaywrightPolicy: browserctl.Policy{
			ManagementMode:     browserctl.ManagementModeManaged,
			DefaultBrowserMode: browserctl.BrowserModeHeadless,
			LoginMode:          browserctl.LoginModePromote,
			IsolationScope:     browserctl.IsolationScopeTask,
		},
		ManagedBrowserSessionKey: "session-demo",
	}

	got := codexPlaywrightMCPConfigOverrides(req)
	if len(got) != 2 {
		t.Fatalf("codexPlaywrightMCPConfigOverrides() len = %d, want 2", len(got))
	}
	executablePath, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable() error = %v", err)
	}
	if got[0] != `mcp_servers.playwright.command="`+executablePath+`"` {
		t.Fatalf("command override = %q, want lcroom executable %q", got[0], executablePath)
	}
	for _, want := range []string{
		`"playwright-mcp"`,
		`"--project-path","/tmp/demo"`,
		`"--data-dir","/tmp/lcr-data"`,
		`"--session-key","session-demo"`,
		`"--launch-mode","background"`,
		`"--profile-key","`,
	} {
		if !strings.Contains(got[1], want) {
			t.Fatalf("args override = %q, want substring %q", got[1], want)
		}
	}
}

func TestCodexPlaywrightMCPConfigOverridesManagedHeaded(t *testing.T) {
	req := LaunchRequest{
		Provider:    ProviderCodex,
		ProjectPath: "/tmp/demo",
		AppDataDir:  "/tmp/lcr-data",
		PlaywrightPolicy: browserctl.Policy{
			ManagementMode:     browserctl.ManagementModeManaged,
			DefaultBrowserMode: browserctl.BrowserModeHeaded,
			LoginMode:          browserctl.LoginModePromote,
			IsolationScope:     browserctl.IsolationScopeProject,
		},
		ManagedBrowserSessionKey: "session-demo",
	}

	got := codexPlaywrightMCPConfigOverrides(req)
	if len(got) != 2 {
		t.Fatalf("codexPlaywrightMCPConfigOverrides() len = %d, want 2", len(got))
	}
	if !strings.Contains(got[1], `--launch-mode","headed"`) {
		t.Fatalf("args override = %q, want headed launch mode", got[1])
	}
}

func TestCodexPlaywrightMCPConfigOverridesClassicBrowserBehavior(t *testing.T) {
	req := LaunchRequest{
		Provider:    ProviderCodex,
		ProjectPath: "/tmp/demo",
		PlaywrightPolicy: browserctl.Policy{
			ManagementMode:     browserctl.ManagementModeLegacy,
			DefaultBrowserMode: browserctl.BrowserModeHeaded,
			LoginMode:          browserctl.LoginModeManual,
			IsolationScope:     browserctl.IsolationScopeProject,
		},
	}

	if got := codexPlaywrightMCPConfigOverrides(req); got != nil {
		t.Fatalf("codexPlaywrightMCPConfigOverrides() = %#v, want nil", got)
	}
}

func TestApplyCodexPlaywrightMCPOverridesAppendsConfigArgs(t *testing.T) {
	cmd := exec.Command("codex", "app-server")
	applyCodexPlaywrightMCPOverrides(cmd, LaunchRequest{
		Provider:                 ProviderCodex,
		ProjectPath:              "/tmp/demo",
		AppDataDir:               "/tmp/lcr-data",
		ManagedBrowserSessionKey: "session-demo",
		PlaywrightPolicy: browserctl.Policy{
			ManagementMode:     browserctl.ManagementModeManaged,
			DefaultBrowserMode: browserctl.BrowserModeHeadless,
			LoginMode:          browserctl.LoginModePromote,
			IsolationScope:     browserctl.IsolationScopeTask,
		},
	})

	if len(cmd.Args) != 6 {
		t.Fatalf("cmd.Args len = %d, want 6", len(cmd.Args))
	}
	if cmd.Args[0] != "codex" || cmd.Args[1] != "app-server" || cmd.Args[2] != "-c" || cmd.Args[4] != "-c" {
		t.Fatalf("cmd.Args prefix = %#v, want codex app-server -c ... -c ...", cmd.Args)
	}
	if !strings.Contains(cmd.Args[3], `mcp_servers.playwright.command=`) {
		t.Fatalf("command override = %q, want playwright command override", cmd.Args[3])
	}
	if !strings.Contains(cmd.Args[5], `"playwright-mcp"`) {
		t.Fatalf("args override = %q, want lcroom wrapper args", cmd.Args[5])
	}
}
