package codexapp

import (
	"testing"

	"lcroom/internal/browserctl"
)

func TestRequestManagedBrowserHideAfterSubmissionUsesOnlyWhenNeededPolicy(t *testing.T) {
	previous := managedBrowserHideRequester
	t.Cleanup(func() { managedBrowserHideRequester = previous })

	var gotDataDir string
	var gotSessionKey string
	managedBrowserHideRequester = func(dataDir, sessionKey string) (browserctl.ManagedPlaywrightState, bool, error) {
		gotDataDir = dataDir
		gotSessionKey = sessionKey
		return browserctl.ManagedPlaywrightState{}, true, nil
	}

	requestManagedBrowserHideAfterSubmission(
		"/tmp/lcroom-data",
		"browser-session",
		browserctl.Policy{
			ManagementMode:     browserctl.ManagementModeManaged,
			DefaultBrowserMode: browserctl.BrowserModeHeadless,
			LoginMode:          browserctl.LoginModePromote,
			IsolationScope:     browserctl.IsolationScopeTask,
		},
	)
	if gotDataDir != "/tmp/lcroom-data" || gotSessionKey != "browser-session" {
		t.Fatalf("hide request = %q %q, want configured data dir and session", gotDataDir, gotSessionKey)
	}
}

func TestRequestManagedBrowserHideAfterSubmissionLeavesAlwaysShowVisible(t *testing.T) {
	previous := managedBrowserHideRequester
	t.Cleanup(func() { managedBrowserHideRequester = previous })

	called := false
	managedBrowserHideRequester = func(string, string) (browserctl.ManagedPlaywrightState, bool, error) {
		called = true
		return browserctl.ManagedPlaywrightState{}, true, nil
	}

	requestManagedBrowserHideAfterSubmission(
		"/tmp/lcroom-data",
		"browser-session",
		browserctl.Policy{
			ManagementMode:     browserctl.ManagementModeManaged,
			DefaultBrowserMode: browserctl.BrowserModeHeaded,
			LoginMode:          browserctl.LoginModePromote,
			IsolationScope:     browserctl.IsolationScopeTask,
		},
	)
	if called {
		t.Fatal("Always show policy should not request a background hide")
	}
}
