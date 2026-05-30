package browserctl

import (
	"context"
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
}
