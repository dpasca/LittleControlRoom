package codexapp

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"

	"lcroom/internal/browserctl"
)

func TestCodexPlaywrightMCPConfigOverridesManagedHeadless(t *testing.T) {
	req := LaunchRequest{
		Provider:          ProviderCodex,
		ProjectPath:       "/tmp/demo",
		AppDataDir:        "/tmp/lcr-data",
		CLIExecutablePath: "/tmp/lcroom-test-bin",
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
	if got[0] != `mcp_servers.playwright.command="/tmp/lcroom-test-bin"` {
		t.Fatalf("command override = %q, want configured lcroom executable path", got[0])
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
		Provider:          ProviderCodex,
		ProjectPath:       "/tmp/demo",
		AppDataDir:        "/tmp/lcr-data",
		CLIExecutablePath: "/tmp/lcroom-test-bin",
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

func TestCodexPlaywrightMCPConfigOverridesUsesCurrentExecutableByDefault(t *testing.T) {
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

func TestOpenCodePlaywrightMCPOverrideManagedHeadless(t *testing.T) {
	req := LaunchRequest{
		Provider:          ProviderOpenCode,
		ProjectPath:       "/tmp/demo",
		AppDataDir:        "/tmp/lcr-data",
		CLIExecutablePath: "/tmp/lcroom-test-bin",
		PlaywrightPolicy: browserctl.Policy{
			ManagementMode:     browserctl.ManagementModeManaged,
			DefaultBrowserMode: browserctl.BrowserModeHeadless,
			LoginMode:          browserctl.LoginModePromote,
			IsolationScope:     browserctl.IsolationScopeTask,
		},
		ManagedBrowserSessionKey: "session-demo",
	}

	raw, ok, err := openCodePlaywrightMCPOverride(req)
	if err != nil {
		t.Fatalf("openCodePlaywrightMCPOverride() error = %v", err)
	}
	if !ok {
		t.Fatal("openCodePlaywrightMCPOverride() ok = false, want true")
	}

	var got struct {
		Type    string   `json:"type"`
		Command []string `json:"command"`
		Enabled bool     `json:"enabled"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal override: %v", err)
	}
	if got.Type != "local" {
		t.Fatalf("override type = %q, want local", got.Type)
	}
	if !got.Enabled {
		t.Fatal("override enabled = false, want true")
	}
	if len(got.Command) < 2 {
		t.Fatalf("override command = %#v, want executable plus args", got.Command)
	}
	if got.Command[0] != "/tmp/lcroom-test-bin" {
		t.Fatalf("override executable = %q, want configured lcroom executable", got.Command[0])
	}
	for _, want := range []string{
		"playwright-mcp",
		"--provider", "opencode",
		"--project-path", "/tmp/demo",
		"--data-dir", "/tmp/lcr-data",
		"--session-key", "session-demo",
		"--launch-mode", "background",
	} {
		if !containsString(got.Command, want) {
			t.Fatalf("override command = %#v, want entry %q", got.Command, want)
		}
	}
}

func TestOpenCodePlaywrightMCPOverrideClassicBrowserBehavior(t *testing.T) {
	req := LaunchRequest{
		Provider:    ProviderOpenCode,
		ProjectPath: "/tmp/demo",
		PlaywrightPolicy: browserctl.Policy{
			ManagementMode:     browserctl.ManagementModeLegacy,
			DefaultBrowserMode: browserctl.BrowserModeHeaded,
			LoginMode:          browserctl.LoginModeManual,
			IsolationScope:     browserctl.IsolationScopeProject,
		},
	}

	if raw, ok, err := openCodePlaywrightMCPOverride(req); err != nil {
		t.Fatalf("openCodePlaywrightMCPOverride() error = %v", err)
	} else if ok || raw != nil {
		t.Fatalf("openCodePlaywrightMCPOverride() = (%q, %v), want nil,false", string(raw), ok)
	}
}

func TestApplyCodexPlaywrightMCPOverridesAppendsConfigArgs(t *testing.T) {
	cmd := exec.Command("codex", "app-server")
	applyCodexPlaywrightMCPOverrides(cmd, LaunchRequest{
		Provider:                 ProviderCodex,
		ProjectPath:              "/tmp/demo",
		AppDataDir:               "/tmp/lcr-data",
		ManagedBrowserSessionKey: "session-demo",
		CLIExecutablePath:        "/tmp/lcroom-test-bin",
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

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
