package cli

import (
	"testing"
	"time"

	"lcroom/internal/browserctl"
)

func TestReconcileManagedPlaywrightBrowserKeepsBackgroundBrowserHiddenUntilReveal(t *testing.T) {
	paths, err := browserctl.ManagedPlaywrightPathsFor(
		t.TempDir(),
		"codex",
		"/tmp/demo",
		"session-demo",
		"profile-demo",
		browserctl.ManagedLaunchModeBackground,
	)
	if err != nil {
		t.Fatalf("ManagedPlaywrightPathsFor() error = %v", err)
	}
	initial := browserctl.ManagedPlaywrightState{
		SessionKey:  paths.SessionKey,
		ProfileKey:  paths.ProfileKey,
		Provider:    paths.Provider,
		ProjectPath: paths.ProjectPath,
		LaunchMode:  paths.LaunchMode,
		Policy:      browserctl.DefaultPolicy(),
	}
	if err := browserctl.WriteManagedPlaywrightState(paths, initial); err != nil {
		t.Fatalf("write initial state: %v", err)
	}

	origDetector := detectManagedBrowserProcess
	origHider := hideManagedBrowserProcess
	t.Cleanup(func() {
		detectManagedBrowserProcess = origDetector
		hideManagedBrowserProcess = origHider
	})

	detectManagedBrowserProcess = func(rootPID int) (browserctl.ManagedBrowserProcess, bool, error) {
		if rootPID != 456 {
			t.Fatalf("rootPID = %d, want 456", rootPID)
		}
		return browserctl.ManagedBrowserProcess{
			PID:            123,
			AppPath:        "/Applications/Google Chrome.app",
			AppName:        "Google Chrome",
			ExecutablePath: "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		}, true, nil
	}
	hideCount := 0
	hideManagedBrowserProcess = func(pid int) error {
		if pid != 123 {
			t.Fatalf("hide pid = %d, want 123", pid)
		}
		hideCount++
		return nil
	}

	monitorState := managedBrowserMonitorState{}
	if err := reconcileManagedPlaywrightBrowser(paths, 456, true, &monitorState, time.Unix(10, 0)); err != nil {
		t.Fatalf("first reconcile error = %v", err)
	}
	if hideCount != 1 {
		t.Fatalf("hideCount after first reconcile = %d, want 1", hideCount)
	}
	stored, err := browserctl.ReadManagedPlaywrightState(paths.DataDir, paths.SessionKey)
	if err != nil {
		t.Fatalf("read first state: %v", err)
	}
	if !stored.Hidden {
		t.Fatalf("stored.Hidden after first reconcile = false, want true")
	}

	if err := reconcileManagedPlaywrightBrowser(paths, 456, true, &monitorState, time.Unix(11, 0)); err != nil {
		t.Fatalf("second reconcile error = %v", err)
	}
	if hideCount != 2 {
		t.Fatalf("hideCount after second reconcile = %d, want 2 while browser is still marked hidden", hideCount)
	}

	stored.Hidden = false
	if err := browserctl.WriteManagedPlaywrightState(paths, stored); err != nil {
		t.Fatalf("write revealed state: %v", err)
	}
	if err := reconcileManagedPlaywrightBrowser(paths, 456, true, &monitorState, time.Unix(12, 0)); err != nil {
		t.Fatalf("third reconcile error = %v", err)
	}
	if hideCount != 2 {
		t.Fatalf("hideCount after reveal = %d, want no additional hide", hideCount)
	}
	stored, err = browserctl.ReadManagedPlaywrightState(paths.DataDir, paths.SessionKey)
	if err != nil {
		t.Fatalf("read revealed state: %v", err)
	}
	if stored.Hidden {
		t.Fatalf("stored.Hidden after reveal reconcile = true, want false")
	}
}
