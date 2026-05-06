package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"

	"lcroom/internal/browserctl"
)

func TestRunBrowserStatusPrintsJSON(t *testing.T) {
	origReader := managedBrowserStateReader
	origStdout := browserStdout
	origStderr := browserStderr
	t.Cleanup(func() {
		managedBrowserStateReader = origReader
		browserStdout = origStdout
		browserStderr = origStderr
	})

	managedBrowserStateReader = func(dataDir, sessionKey string) (browserctl.ManagedPlaywrightState, error) {
		return browserctl.ManagedPlaywrightState{
			SessionKey:      sessionKey,
			Provider:        "codex",
			ProjectPath:     "/tmp/demo",
			LaunchMode:      browserctl.ManagedLaunchModeBackground,
			RevealSupported: true,
		}, nil
	}

	var stdout, stderr bytes.Buffer
	browserStdout = &stdout
	browserStderr = &stderr

	exitCode := runBrowser([]string{"status", "--data-dir", "/tmp/lcr", "--session-key", "session-demo"})
	if exitCode != 0 {
		t.Fatalf("runBrowser(status) = %d, want 0; stderr=%q", exitCode, stderr.String())
	}
	var decoded browserctl.ManagedPlaywrightState
	if err := json.Unmarshal(stdout.Bytes(), &decoded); err != nil {
		t.Fatalf("status output not valid JSON: %v\n%s", err, stdout.String())
	}
	if decoded.SessionKey != "session-demo" {
		t.Fatalf("decoded session key = %q, want session-demo", decoded.SessionKey)
	}
	if decoded.ProjectPath != "/tmp/demo" {
		t.Fatalf("decoded project path = %q, want /tmp/demo", decoded.ProjectPath)
	}
}

func TestRunBrowserRevealUsesManagedBrowserSessionRevealer(t *testing.T) {
	origReader := managedBrowserStateReader
	origRevealer := managedBrowserSessionRevealer
	origStdout := browserStdout
	origStderr := browserStderr
	t.Cleanup(func() {
		managedBrowserStateReader = origReader
		managedBrowserSessionRevealer = origRevealer
		browserStdout = origStdout
		browserStderr = origStderr
	})

	managedBrowserStateReader = func(dataDir, sessionKey string) (browserctl.ManagedPlaywrightState, error) {
		return browserctl.ManagedPlaywrightState{
			SessionKey:      sessionKey,
			BrowserPID:      123,
			RevealSupported: true,
		}, nil
	}

	revealedDataDir := ""
	revealedSessionKey := ""
	managedBrowserSessionRevealer = func(dataDir, sessionKey string) (browserctl.ManagedPlaywrightState, error) {
		revealedDataDir = dataDir
		revealedSessionKey = sessionKey
		return browserctl.ManagedPlaywrightState{SessionKey: sessionKey, Hidden: false}, nil
	}

	var stdout, stderr bytes.Buffer
	browserStdout = &stdout
	browserStderr = &stderr

	exitCode := runBrowser([]string{"reveal", "--session-key", "session-demo"})
	if exitCode != 0 {
		t.Fatalf("runBrowser(reveal) = %d, want 0; stderr=%q", exitCode, stderr.String())
	}
	if revealedDataDir != browserctl.DefaultDataDir() || revealedSessionKey != "session-demo" {
		t.Fatalf("revealed session = dataDir %q sessionKey %q, want default data dir and session-demo", revealedDataDir, revealedSessionKey)
	}
	if got := stdout.String(); got != "revealed managed browser session session-demo\n" {
		t.Fatalf("stdout = %q, want reveal confirmation", got)
	}
}

func TestRunBrowserRequiresSessionKey(t *testing.T) {
	origStdout := browserStdout
	origStderr := browserStderr
	t.Cleanup(func() {
		browserStdout = origStdout
		browserStderr = origStderr
	})

	var stdout, stderr bytes.Buffer
	browserStdout = &stdout
	browserStderr = &stderr

	exitCode := runBrowser([]string{"status"})
	if exitCode != 2 {
		t.Fatalf("runBrowser(status without key) = %d, want 2", exitCode)
	}
	if got := stderr.String(); got != "browser command error: --session-key is required\n" {
		t.Fatalf("stderr = %q, want missing-session-key error", got)
	}
}

func TestRunBrowserPropagatesRevealFailure(t *testing.T) {
	origReader := managedBrowserStateReader
	origRevealer := managedBrowserSessionRevealer
	origStdout := browserStdout
	origStderr := browserStderr
	t.Cleanup(func() {
		managedBrowserStateReader = origReader
		managedBrowserSessionRevealer = origRevealer
		browserStdout = origStdout
		browserStderr = origStderr
	})

	managedBrowserStateReader = func(dataDir, sessionKey string) (browserctl.ManagedPlaywrightState, error) {
		return browserctl.ManagedPlaywrightState{SessionKey: sessionKey}, nil
	}
	managedBrowserSessionRevealer = func(dataDir, sessionKey string) (browserctl.ManagedPlaywrightState, error) {
		return browserctl.ManagedPlaywrightState{}, fmt.Errorf("boom")
	}

	var stdout, stderr bytes.Buffer
	browserStdout = &stdout
	browserStderr = &stderr

	exitCode := runBrowser([]string{"reveal", "--session-key", "session-demo"})
	if exitCode != 1 {
		t.Fatalf("runBrowser(reveal failure) = %d, want 1", exitCode)
	}
	if got := stderr.String(); got != "browser reveal failed: boom\n" {
		t.Fatalf("stderr = %q, want reveal failure", got)
	}
}
