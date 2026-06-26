package browserctl

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
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

func TestPlaywrightMCPBrowserSessionUploadsFiles(t *testing.T) {
	dir := t.TempDir()
	fakeMCP := filepath.Join(dir, "fake-mcp.sh")
	callsPath := filepath.Join(dir, "calls.log")
	script := `#!/bin/sh
while IFS= read -r line; do
  printf '%s\n' "$line" >> "$CALLS_PATH"
  id=$(printf '%s' "$line" | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')
  if printf '%s' "$line" | grep -q '"method":"initialize"'; then
    printf '{"id":"%s","jsonrpc":"2.0","result":{"protocolVersion":"2024-11-05","capabilities":{"tools":{}},"serverInfo":{"name":"Playwright","version":"test"}}}\n' "$id"
  elif printf '%s' "$line" | grep -q '"method":"tools/call"'; then
    printf '{"id":"%s","jsonrpc":"2.0","result":{"content":[{"type":"text","text":"### Page state\\n- Page URL: https://studio.example/upload\\n- Page Title: Upload"}]}}\n' "$id"
  fi
done
`
	if err := os.WriteFile(fakeMCP, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	origCommand := playwrightMCPCommand
	playwrightMCPCommand = fakeMCP
	t.Cleanup(func() { playwrightMCPCommand = origCommand })
	t.Setenv("CALLS_PATH", callsPath)

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

	result, err := session.FileUpload(context.Background(), []string{"/tmp/video.mp4"})
	if err != nil {
		t.Fatal(err)
	}
	if result.URL != "https://studio.example/upload" || result.Status != "file_uploaded" {
		t.Fatalf("upload result = %#v", result)
	}
	raw, err := os.ReadFile(callsPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"name":"browser_file_upload"`, `"/tmp/video.mp4"`} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("MCP calls missing %q:\n%s", want, raw)
		}
	}
}

func TestDefaultInteractiveBrowserExecutablesPrefersChromeBeforeBrave(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS browser application preference only applies on darwin")
	}
	got := strings.Join(defaultInteractiveBrowserExecutables(), "\n")
	chrome := strings.Index(got, "Google Chrome.app")
	chromium := strings.Index(got, "Chromium.app")
	brave := strings.Index(got, "Brave Browser.app")
	if chrome < 0 || chromium < 0 || brave < 0 {
		t.Fatalf("browser candidates = %q, want Chrome, Chromium, and Brave", got)
	}
	if !(chrome < chromium && chromium < brave) {
		t.Fatalf("browser candidates = %q, want Chrome then Chromium then Brave", got)
	}
}

func TestPlaywrightMCPTabSelectionPrefersNonBlankWhenCurrentIsBlank(t *testing.T) {
	text := strings.Join([]string{
		"### Open tabs",
		"- 0: (current) [] (about:blank)",
		"- 1: [Example] (https://example.test/)",
		"",
	}, "\n")
	index, ok := playwrightMCPNonBlankTabToSelect(parsePlaywrightMCPTabs(text))
	if !ok || index != 1 {
		t.Fatalf("tab selection = %d, %v; want index 1", index, ok)
	}
}

func TestPlaywrightMCPTabSelectionKeepsCurrentNonBlank(t *testing.T) {
	text := strings.Join([]string{
		"### Open tabs",
		"- 0: (current) [Example] (https://example.test/)",
		"- 1: [] (about:blank)",
		"",
	}, "\n")
	index, ok := playwrightMCPNonBlankTabToSelect(parsePlaywrightMCPTabs(text))
	if ok {
		t.Fatalf("tab selection = %d, %v; want no selection", index, ok)
	}
}

func TestPlaywrightMCPBrowserSessionSelectsNonBlankTabBeforeCurrentPage(t *testing.T) {
	dir := t.TempDir()
	fakeMCP := filepath.Join(dir, "fake-mcp.sh")
	callsPath := filepath.Join(dir, "calls.log")
	script := `#!/bin/sh
while IFS= read -r line; do
  printf '%s\n' "$line" >> "$CALLS_PATH"
  id=$(printf '%s' "$line" | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')
  if printf '%s' "$line" | grep -q '"method":"initialize"'; then
    printf '{"id":"%s","jsonrpc":"2.0","result":{"protocolVersion":"2024-11-05","capabilities":{"tools":{}},"serverInfo":{"name":"Playwright","version":"test"}}}\n' "$id"
  elif printf '%s' "$line" | grep -q '"name":"browser_tabs"' && printf '%s' "$line" | grep -q '"action":"list"'; then
    printf '{"id":"%s","jsonrpc":"2.0","result":{"content":[{"type":"text","text":"### Open tabs\\n- 0: (current) [] (about:blank)\\n- 1: [Example] (https://example.test/)\\n"}]}}\n' "$id"
  elif printf '%s' "$line" | grep -q '"name":"browser_tabs"' && printf '%s' "$line" | grep -q '"action":"select"'; then
    printf '{"id":"%s","jsonrpc":"2.0","result":{"content":[{"type":"text","text":"### Open tabs\\n- 0: [] (about:blank)\\n- 1: (current) [Example] (https://example.test/)\\n"}]}}\n' "$id"
  elif printf '%s' "$line" | grep -q '"name":"browser_snapshot"'; then
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
	t.Setenv("CALLS_PATH", callsPath)

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

	result, err := session.CurrentPage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.URL != "https://example.test/" || result.Title != "Example" {
		t.Fatalf("current page = %#v", result)
	}
	raw, err := os.ReadFile(callsPath)
	if err != nil {
		t.Fatal(err)
	}
	calls := string(raw)
	if !strings.Contains(calls, `"name":"browser_tabs"`) || !strings.Contains(calls, `"action":"select"`) || !strings.Contains(calls, `"index":1`) {
		t.Fatalf("calls did not select the non-blank tab:\n%s", calls)
	}
}

func TestBrowserActionResultFromMCPTextParsesPageState(t *testing.T) {
	result := browserActionResultFromMCPText("snapshot", "### Page state\n- Page URL: https://example.test/\n- Page Title: Example", "")
	raw, _ := json.Marshal(result)
	if result.URL != "https://example.test/" || result.Title != "Example" || !result.Fresh {
		t.Fatalf("result = %s", raw)
	}
}
