package browserctl

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPlaywrightBrowserSessionUsesWorkerProtocolAndManagedState(t *testing.T) {
	dir := t.TempDir()
	fakeWorker := filepath.Join(dir, "fake-worker.sh")
	script := `#!/bin/sh
while IFS= read -r line; do
  id=$(printf '%s' "$line" | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')
  if printf '%s' "$line" | grep -q '"method":"navigate"'; then
    printf '{"id":"%s","ok":true,"result":{"URL":"https://example.test/","Title":"Example","Status":"navigated","Fresh":true}}\n' "$id"
  elif printf '%s' "$line" | grep -q '"method":"search_google"'; then
    printf '{"id":"%s","ok":true,"result":{"URL":"https://www.google.com/search?q=lcagent","Title":"lcagent - Google Search","Status":"searched","Snapshot":"backend: browser\\nquery: lcagent\\nresults: 1\\n\\n1. LCAgent Browser\\n   url: https://example.test/browser\\n","Fresh":true}}\n' "$id"
  elif printf '%s' "$line" | grep -q '"method":"current_page"'; then
    printf '{"id":"%s","ok":true,"result":{"URL":"https://example.test/","Title":"Example","Status":"current_page","Fresh":true}}\n' "$id"
  else
    printf '{"id":"%s","ok":false,"error":"unexpected method"}\n' "$id"
  fi
done
`
	if err := os.WriteFile(fakeWorker, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	origCommand := playwrightWorkerCommand
	origArgs := playwrightWorkerArgs
	playwrightWorkerCommand = fakeWorker
	playwrightWorkerArgs = func(string) []string { return nil }
	t.Cleanup(func() {
		playwrightWorkerCommand = origCommand
		playwrightWorkerArgs = origArgs
	})

	session, err := NewPlaywrightBrowserSession(BrowserSessionConfig{
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
	if result.URL != "https://example.test/" || result.Title != "Example" || result.Status != "navigated" {
		t.Fatalf("navigate result = %#v", result)
	}
	state, err := ReadManagedPlaywrightState(dir, "session-demo")
	if err != nil {
		t.Fatal(err)
	}
	if state.MCPPID == 0 || state.ProfileKey != "profile-demo" || state.Provider != "lcagent" {
		t.Fatalf("state after lazy start = %#v", state)
	}

	current, err := session.CurrentPage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(current.URL, "example.test") {
		t.Fatalf("current page = %#v", current)
	}
	search, err := session.SearchGoogle(context.Background(), "lcagent", 5, "example.test", 7)
	if err != nil {
		t.Fatal(err)
	}
	if search.Status != "searched" || !strings.Contains(search.Snapshot, "backend: browser") || !strings.Contains(search.Snapshot, "LCAgent Browser") {
		t.Fatalf("search result = %#v", search)
	}
	state.BrowserPID = 321
	state.BrowserAppName = "Chromium"
	state.RevealSupported = true
	if err := WriteManagedPlaywrightState(session.paths, state); err != nil {
		t.Fatal(err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	state, err = ReadManagedPlaywrightState(dir, "session-demo")
	if err != nil {
		t.Fatal(err)
	}
	if state.MCPPID != 0 {
		t.Fatalf("MCPPID after close = %d, want 0", state.MCPPID)
	}
	if state.BrowserPID != 0 || state.RevealSupported || state.BrowserAppName != "" {
		t.Fatalf("browser attachment after close = %#v, want cleared", state)
	}
}

func TestPlaywrightBrowserSessionPrefersChromeForVisibleHandoffs(t *testing.T) {
	t.Setenv("LCR_PLAYWRIGHT_BROWSER_CHANNEL", "")
	dir := t.TempDir()
	fakeWorker := filepath.Join(dir, "fake-worker.sh")
	seenConfig := filepath.Join(dir, "seen-config.json")
	script := `#!/bin/sh
printf '%s' "$LCR_BROWSER_WORKER_CONFIG" > "$SEEN_CONFIG"
while IFS= read -r line; do
  id=$(printf '%s' "$line" | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')
  printf '{"id":"%s","ok":true,"result":{"URL":"about:blank","Title":"","Status":"current_page","Fresh":true}}\n' "$id"
done
`
	if err := os.WriteFile(fakeWorker, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	origCommand := playwrightWorkerCommand
	origArgs := playwrightWorkerArgs
	playwrightWorkerCommand = fakeWorker
	playwrightWorkerArgs = func(string) []string { return nil }
	t.Cleanup(func() {
		playwrightWorkerCommand = origCommand
		playwrightWorkerArgs = origArgs
	})
	t.Setenv("SEEN_CONFIG", seenConfig)

	session, err := NewPlaywrightBrowserSession(BrowserSessionConfig{
		DataDir:     dir,
		Provider:    "lcagent",
		ProjectPath: "/tmp/demo",
		SessionKey:  "session-demo",
		ProfileKey:  "profile-demo",
		LaunchMode:  ManagedLaunchModeBackground,
		Policy:      DefaultPolicy(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	if _, err := session.CurrentPage(context.Background()); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(seenConfig)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("worker config %q is not JSON: %v", raw, err)
	}
	if got := cfg["browserChannel"]; got != "chrome" {
		t.Fatalf("browserChannel = %#v, want chrome for background handoff", got)
	}
}
