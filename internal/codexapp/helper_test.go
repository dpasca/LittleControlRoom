package codexapp

import (
	"testing"

	"lcroom/internal/browserctl"
	"lcroom/internal/codexcli"
)

func TestPromptHelperLaunchRequestDisablesManagedPlaywright(t *testing.T) {
	req := promptHelperLaunchRequest("/tmp/lcroom-codex-helper")

	if req.Provider != ProviderCodex {
		t.Fatalf("provider = %q, want codex", req.Provider)
	}
	if req.ProjectPath != "/tmp/lcroom-codex-helper" {
		t.Fatalf("project path = %q", req.ProjectPath)
	}
	if !req.ForceNew {
		t.Fatal("ForceNew = false, want true")
	}
	if req.Preset != codexcli.PresetSafe {
		t.Fatalf("preset = %q, want safe", req.Preset)
	}
	if got := req.PlaywrightPolicy.Normalize().ManagementMode; got != browserctl.ManagementModeLegacy {
		t.Fatalf("playwright management mode = %s, want legacy", got)
	}
	if got := codexPlaywrightMCPConfigOverrides(req); got != nil {
		t.Fatalf("prompt helper should not configure Playwright MCP, got %#v", got)
	}
}
