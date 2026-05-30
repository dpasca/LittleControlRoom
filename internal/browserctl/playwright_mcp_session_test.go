package browserctl

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPlaywrightMCPBrowserSessionUsesMCPProtocolAndManagedState(t *testing.T) {
	dir := t.TempDir()
	fakeMCP := filepath.Join(dir, "fake-mcp.sh")
	script := `#!/bin/sh
while IFS= read -r line; do
  id=$(printf '%s' "$line" | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')
  if printf '%s' "$line" | grep -q '"method":"initialize"'; then
    printf '{"id":"%s","jsonrpc":"2.0","result":{"protocolVersion":"2024-11-05","capabilities":{"tools":{}},"serverInfo":{"name":"Playwright","version":"test"}}}\n' "$id"
  elif printf '%s' "$line" | grep -q '"method":"tools/call"'; then
    printf '{"id":"%s","jsonrpc":"2.0","result":{"content":[{"type":"text","text":"### Page state\\n- Page URL: https://example.test/\\n- Page Title: Example"}]}}\n' "$id"
  fi
done
`
	if err := os.WriteFile(fakeMCP, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	origCommand := playwrightMCPCommand
	playwrightMCPCommand = fakeMCP
	t.Cleanup(func() { playwrightMCPCommand = origCommand })

	session, err := NewPlaywrightMCPBrowserSession(BrowserSessionConfig{
		DataDir:     dir,
		Provider:    "lcagent",
		ProjectPath: "/tmp/demo",
		SessionKey:  "session-demo",
		ProfileKey:  "profile-demo",
		LaunchMode:  ManagedLaunchModeHeadless,
		Policy:      DefaultPolicy(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	result, err := session.Navigate(context.Background(), "https://example.test")
	if err != nil {
		t.Fatal(err)
	}
	if result.URL != "https://example.test/" || result.Title != "Example" {
		t.Fatalf("navigate result = %#v", result)
	}
	state, err := ReadManagedPlaywrightState(dir, "session-demo")
	if err != nil {
		t.Fatal(err)
	}
	if state.MCPPID == 0 || state.ProfileKey != "profile-demo" || state.Provider != "lcagent" {
		t.Fatalf("state after lazy start = %#v", state)
	}
}

func TestPlaywrightMCPBrowserSessionPrefersConfiguredBrowserPath(t *testing.T) {
	dir := t.TempDir()
	fakeMCP := filepath.Join(dir, "fake-mcp.sh")
	argsPath := filepath.Join(dir, "args.json")
	script := `#!/bin/sh
printf '%s\n' "$@" > "$ARGS_PATH"
while IFS= read -r line; do
  id=$(printf '%s' "$line" | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')
  if printf '%s' "$line" | grep -q '"method":"initialize"'; then
    printf '{"id":"%s","jsonrpc":"2.0","result":{"protocolVersion":"2024-11-05","capabilities":{"tools":{}},"serverInfo":{"name":"Playwright","version":"test"}}}\n' "$id"
  elif printf '%s' "$line" | grep -q '"method":"tools/call"'; then
    printf '{"id":"%s","jsonrpc":"2.0","result":{"content":[{"type":"text","text":"### Page state\\n- Page URL: about:blank"}]}}\n' "$id"
  fi
done
`
	if err := os.WriteFile(fakeMCP, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	origCommand := playwrightMCPCommand
	playwrightMCPCommand = fakeMCP
	t.Cleanup(func() { playwrightMCPCommand = origCommand })
	t.Setenv("ARGS_PATH", argsPath)

	session, err := NewPlaywrightMCPBrowserSession(BrowserSessionConfig{
		DataDir:     dir,
		Provider:    "lcagent",
		ProjectPath: "/tmp/demo",
		SessionKey:  "session-demo",
		ProfileKey:  "profile-demo",
		LaunchMode:  ManagedLaunchModeBackground,
		Policy:      DefaultPolicy(),
		BrowserPath: "/Applications/Brave Browser.app/Contents/MacOS/Brave Browser",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	if _, err := session.CurrentPage(context.Background()); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "--executable-path") || !strings.Contains(string(raw), "Brave Browser") {
		t.Fatalf("mcp args = %q, want configured Brave executable", raw)
	}
	if strings.Contains(string(raw), "--headless") {
		t.Fatalf("background handoff should be headed for reveal, args = %q", raw)
	}
}

func TestBrowserActionResultFromMCPTextParsesPageState(t *testing.T) {
	result := browserActionResultFromMCPText("snapshot", "### Page state\n- Page URL: https://example.test/\n- Page Title: Example", "")
	raw, _ := json.Marshal(result)
	if result.URL != "https://example.test/" || result.Title != "Example" || !result.Fresh {
		t.Fatalf("result = %s", raw)
	}
}
