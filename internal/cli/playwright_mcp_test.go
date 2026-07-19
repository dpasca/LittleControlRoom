package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"lcroom/internal/browserctl"
)

func TestWritePlaywrightMCPProfilePreflightNoticesSurfacesWarning(t *testing.T) {
	var stderr bytes.Buffer
	writePlaywrightMCPProfilePreflightNotices(&stderr, browserctl.ManagedPlaywrightProfilePreflight{
		CompatibilityWarning: "  browser profile compatibility check skipped: deadline exceeded  ",
	})

	want := "playwright-mcp warning: browser profile compatibility check skipped: deadline exceeded\n"
	if got := stderr.String(); got != want {
		t.Fatalf("preflight stderr = %q, want %q", got, want)
	}
}

func TestWritePlaywrightMCPProfilePreflightNoticesIsQuietWithoutEvent(t *testing.T) {
	var stderr bytes.Buffer
	writePlaywrightMCPProfilePreflightNotices(&stderr, browserctl.ManagedPlaywrightProfilePreflight{})
	if got := stderr.String(); got != "" {
		t.Fatalf("preflight stderr = %q, want no output", got)
	}
}

func TestPlaywrightMCPChildArgsUsesManagedBrowserExecutableOverride(t *testing.T) {
	t.Setenv("LCR_PLAYWRIGHT_BROWSER_EXECUTABLE", "/tmp/lcr-browser")
	args := playwrightMCPChildArgs(browserctl.ManagedPlaywrightPaths{
		OutputDir:  "/tmp/output",
		ProfileDir: "/tmp/profile",
	}, browserctl.ManagedLaunchModeBackground)

	got := strings.Join(args, "\n")
	for _, want := range []string{
		"--output-dir\n/tmp/output",
		"--user-data-dir\n/tmp/profile",
		"--executable-path\n/tmp/lcr-browser",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("args = %#v, want substring %q", args, want)
		}
	}
	if strings.Contains(got, "--headless") {
		t.Fatalf("args = %#v, did not want --headless for background launch", args)
	}
}

func TestPlaywrightMCPChildArgsKeepsHeadlessMode(t *testing.T) {
	t.Setenv("LCR_PLAYWRIGHT_BROWSER_EXECUTABLE", "")
	args := playwrightMCPChildArgs(browserctl.ManagedPlaywrightPaths{
		OutputDir:  "/tmp/output",
		ProfileDir: "/tmp/profile",
	}, browserctl.ManagedLaunchModeHeadless)

	if len(args) == 0 || args[0] != "--headless" {
		t.Fatalf("args = %#v, want --headless first", args)
	}
}

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
	origHider := hideManagedBrowserSession
	t.Cleanup(func() {
		detectManagedBrowserProcess = origDetector
		hideManagedBrowserSession = origHider
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
	hideManagedBrowserSession = func(dataDir, sessionKey string, browser browserctl.ManagedBrowserProcess) (bool, error) {
		if dataDir != paths.DataDir || sessionKey != paths.SessionKey {
			t.Fatalf("hide session = dataDir %q sessionKey %q, want %q %q", dataDir, sessionKey, paths.DataDir, paths.SessionKey)
		}
		if browser.PID != 123 {
			t.Fatalf("hide pid = %d, want 123", browser.PID)
		}
		hideCount++
		state, err := browserctl.ReadManagedPlaywrightState(dataDir, sessionKey)
		if err != nil {
			return false, err
		}
		state.Hidden = true
		if err := browserctl.WriteManagedPlaywrightState(paths, state); err != nil {
			return false, err
		}
		return true, nil
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

func TestReconcileManagedPlaywrightBrowserDoesNotHideDuringRevealTransition(t *testing.T) {
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
		BrowserPID:  123,
		Hidden:      true,
	}
	if err := browserctl.WriteManagedPlaywrightState(paths, initial); err != nil {
		t.Fatalf("write initial state: %v", err)
	}

	origDetector := detectManagedBrowserProcess
	origHider := hideManagedBrowserSession
	t.Cleanup(func() {
		detectManagedBrowserProcess = origDetector
		hideManagedBrowserSession = origHider
	})

	detectManagedBrowserProcess = func(rootPID int) (browserctl.ManagedBrowserProcess, bool, error) {
		return browserctl.ManagedBrowserProcess{
			PID:            123,
			AppPath:        "/Applications/Google Chrome.app",
			AppName:        "Google Chrome",
			ExecutablePath: "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		}, true, nil
	}
	hideCount := 0
	hideManagedBrowserSession = func(string, string, browserctl.ManagedBrowserProcess) (bool, error) {
		hideCount++
		return true, nil
	}

	lockHeld := make(chan struct{})
	releaseReveal := make(chan struct{})
	revealDone := make(chan error, 1)
	go func() {
		revealDone <- browserctl.WithManagedPlaywrightStateLock(paths.DataDir, paths.SessionKey, func() error {
			stored, err := browserctl.ReadManagedPlaywrightState(paths.DataDir, paths.SessionKey)
			if err != nil {
				return err
			}
			stored.Hidden = false
			if err := browserctl.WriteManagedPlaywrightState(paths, stored); err != nil {
				return err
			}
			close(lockHeld)
			<-releaseReveal
			return nil
		})
	}()
	<-lockHeld

	reconcileDone := make(chan error, 1)
	go func() {
		monitorState := managedBrowserMonitorState{hiddenByLCR: true}
		reconcileDone <- reconcileManagedPlaywrightBrowser(paths, 456, true, &monitorState, time.Unix(20, 0))
	}()

	select {
	case err := <-reconcileDone:
		t.Fatalf("reconcile returned while reveal transition held the state lock: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	if hideCount != 0 {
		t.Fatalf("hideCount while reveal transition is locked = %d, want 0", hideCount)
	}

	close(releaseReveal)
	if err := <-revealDone; err != nil {
		t.Fatalf("reveal transition error = %v", err)
	}
	select {
	case err := <-reconcileDone:
		if err != nil {
			t.Fatalf("reconcile error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatalf("reconcile did not finish after reveal transition released")
	}
	if hideCount != 0 {
		t.Fatalf("hideCount after reveal transition = %d, want 0", hideCount)
	}
	stored, err := browserctl.ReadManagedPlaywrightState(paths.DataDir, paths.SessionKey)
	if err != nil {
		t.Fatalf("read final state: %v", err)
	}
	if stored.Hidden {
		t.Fatalf("stored.Hidden after reveal transition reconcile = true, want false")
	}
}

func TestManagedBrowserForReconcileBacksOffWhenNoBrowserIsRunning(t *testing.T) {
	originalDetector := detectManagedBrowserProcess
	t.Cleanup(func() {
		detectManagedBrowserProcess = originalDetector
	})

	detectCount := 0
	detectManagedBrowserProcess = func(rootPID int) (browserctl.ManagedBrowserProcess, bool, error) {
		detectCount++
		return browserctl.ManagedBrowserProcess{}, false, nil
	}

	base := time.Unix(100, 0)
	state := managedBrowserMonitorState{}
	probes := []struct {
		offset    time.Duration
		wantCount int
		wantDelay time.Duration
	}{
		{offset: 0, wantCount: 1, wantDelay: 250 * time.Millisecond},
		{offset: 100 * time.Millisecond, wantCount: 1, wantDelay: 250 * time.Millisecond},
		{offset: 250 * time.Millisecond, wantCount: 2, wantDelay: 500 * time.Millisecond},
		{offset: 750 * time.Millisecond, wantCount: 3, wantDelay: time.Second},
		{offset: 1750 * time.Millisecond, wantCount: 4, wantDelay: 2 * time.Second},
		{offset: 3750 * time.Millisecond, wantCount: 5, wantDelay: 2 * time.Second},
	}
	for _, probe := range probes {
		if _, ok, err := managedBrowserForReconcile("", 456, &state, base.Add(probe.offset)); err != nil || ok {
			t.Fatalf("probe at %s = ok %v err %v, want no browser without error", probe.offset, ok, err)
		}
		if detectCount != probe.wantCount {
			t.Fatalf("detectCount at %s = %d, want %d", probe.offset, detectCount, probe.wantCount)
		}
		if state.discoveryBackoff != probe.wantDelay {
			t.Fatalf("discoveryBackoff at %s = %s, want %s", probe.offset, state.discoveryBackoff, probe.wantDelay)
		}
	}
}

func TestManagedBrowserForReconcileReusesLivePIDBetweenSafetyRefreshes(t *testing.T) {
	originalDetector := detectManagedBrowserProcess
	originalAlive := managedBrowserProcessAlive
	t.Cleanup(func() {
		detectManagedBrowserProcess = originalDetector
		managedBrowserProcessAlive = originalAlive
	})

	detectCount := 0
	detectManagedBrowserProcess = func(rootPID int) (browserctl.ManagedBrowserProcess, bool, error) {
		detectCount++
		return browserctl.ManagedBrowserProcess{PID: 123, AppName: "Google Chrome"}, true, nil
	}
	aliveCount := 0
	managedBrowserProcessAlive = func(pid int) bool {
		aliveCount++
		if pid != 123 {
			t.Fatalf("liveness pid = %d, want 123", pid)
		}
		return true
	}

	base := time.Unix(200, 0)
	state := managedBrowserMonitorState{}
	for _, offset := range []time.Duration{0, 5 * time.Second, managedBrowserDiscoveryRefreshInterval} {
		detected, ok, err := managedBrowserForReconcile("", 456, &state, base.Add(offset))
		if err != nil || !ok {
			t.Fatalf("probe at %s = ok %v err %v, want browser", offset, ok, err)
		}
		if detected.PID != 123 {
			t.Fatalf("probe at %s pid = %d, want 123", offset, detected.PID)
		}
	}
	if detectCount != 2 {
		t.Fatalf("detectCount = %d, want initial discovery plus one safety refresh", detectCount)
	}
	if aliveCount != 2 {
		t.Fatalf("aliveCount = %d, want cached PID checks after initial discovery", aliveCount)
	}
}

func TestManagedBrowserForReconcileRediscoversDeadPIDImmediately(t *testing.T) {
	originalDetector := detectManagedBrowserProcess
	originalAlive := managedBrowserProcessAlive
	t.Cleanup(func() {
		detectManagedBrowserProcess = originalDetector
		managedBrowserProcessAlive = originalAlive
	})

	detectCount := 0
	detectManagedBrowserProcess = func(rootPID int) (browserctl.ManagedBrowserProcess, bool, error) {
		detectCount++
		return browserctl.ManagedBrowserProcess{PID: 122 + detectCount}, true, nil
	}
	managedBrowserProcessAlive = func(pid int) bool {
		if pid != 123 {
			t.Fatalf("liveness pid = %d, want first detected pid 123", pid)
		}
		return false
	}

	base := time.Unix(300, 0)
	state := managedBrowserMonitorState{}
	first, ok, err := managedBrowserForReconcile("", 456, &state, base)
	if err != nil || !ok || first.PID != 123 {
		t.Fatalf("first discovery = %#v ok %v err %v, want pid 123", first, ok, err)
	}
	second, ok, err := managedBrowserForReconcile("", 456, &state, base.Add(time.Second))
	if err != nil || !ok || second.PID != 124 {
		t.Fatalf("rediscovery = %#v ok %v err %v, want pid 124", second, ok, err)
	}
	if detectCount != 2 {
		t.Fatalf("detectCount = %d, want immediate rediscovery after cached pid died", detectCount)
	}
}

func TestManagedBrowserStateNeedsWritePreservesHeartbeatWithoutChurning(t *testing.T) {
	base := time.Unix(400, 0)
	previous := browserctl.ManagedPlaywrightState{
		SessionKey:      "session-demo",
		ProfileKey:      "profile-demo",
		Provider:        "codex",
		ProjectPath:     "/tmp/demo",
		LaunchMode:      browserctl.ManagedLaunchModeBackground,
		Policy:          browserctl.DefaultPolicy(),
		MCPPID:          456,
		BrowserPID:      123,
		BrowserAppName:  "Google Chrome",
		Hidden:          true,
		RevealSupported: true,
		UpdatedAt:       base,
	}
	unchanged := previous
	unchanged.UpdatedAt = base.Add(time.Second)

	if managedBrowserStateNeedsWrite(previous, unchanged, base.Add(time.Second)) {
		t.Fatalf("unchanged state requested a write before the heartbeat interval")
	}
	if !managedBrowserStateNeedsWrite(previous, unchanged, base.Add(managedBrowserStateHeartbeatInterval)) {
		t.Fatalf("unchanged state did not request a heartbeat write")
	}

	changed := unchanged
	changed.BrowserPID = 124
	if !managedBrowserStateNeedsWrite(previous, changed, base.Add(time.Millisecond)) {
		t.Fatalf("changed browser metadata did not request an immediate write")
	}
}

func TestManagedBrowserForReconcileDetectsProfileLaunchDuringFullScanBackoff(t *testing.T) {
	originalProfileDetector := detectManagedBrowserProcessFromProfileLock
	originalDetector := detectManagedBrowserProcess
	t.Cleanup(func() {
		detectManagedBrowserProcessFromProfileLock = originalProfileDetector
		detectManagedBrowserProcess = originalDetector
	})

	profileDetectCount := 0
	detectManagedBrowserProcessFromProfileLock = func(profileDir, executable string) (browserctl.ManagedBrowserProcess, bool) {
		profileDetectCount++
		if profileDir != "/tmp/managed-profile" {
			t.Fatalf("profile dir = %q", profileDir)
		}
		if executable != "/tmp/browser" {
			t.Fatalf("browser executable = %q", executable)
		}
		if profileDetectCount == 1 {
			return browserctl.ManagedBrowserProcess{}, false
		}
		return browserctl.ManagedBrowserProcess{
			PID:            123,
			ExecutablePath: executable,
		}, true
	}
	fullDetectCount := 0
	detectManagedBrowserProcess = func(rootPID int) (browserctl.ManagedBrowserProcess, bool, error) {
		fullDetectCount++
		return browserctl.ManagedBrowserProcess{}, false, nil
	}

	base := time.Unix(500, 0)
	state := managedBrowserMonitorState{browserExecutable: "/tmp/browser"}
	if _, ok, err := managedBrowserForReconcile("/tmp/managed-profile", 456, &state, base); err != nil || ok {
		t.Fatalf("initial discovery = ok %v err %v, want no browser", ok, err)
	}
	detected, ok, err := managedBrowserForReconcile("/tmp/managed-profile", 456, &state, base.Add(managedBrowserUndetectedProbeInterval))
	if err != nil || !ok {
		t.Fatalf("profile-lock discovery = ok %v err %v, want browser", ok, err)
	}
	if detected.PID != 123 {
		t.Fatalf("profile-lock discovery PID = %d, want 123", detected.PID)
	}
	if fullDetectCount != 1 {
		t.Fatalf("full process scans = %d, want only initial scan during backoff", fullDetectCount)
	}
	if profileDetectCount != 2 {
		t.Fatalf("profile-lock probes = %d, want one per discovery attempt", profileDetectCount)
	}
}

func TestManagedBrowserMonitorDelayPreservesFastLaunchDetection(t *testing.T) {
	state := managedBrowserMonitorState{}
	if got := managedBrowserMonitorDelay(&state); got != managedBrowserUndetectedProbeInterval {
		t.Fatalf("undetected monitor delay = %s, want %s", got, managedBrowserUndetectedProbeInterval)
	}
	state.detectedBrowser = browserctl.ManagedBrowserProcess{PID: 123}
	if got := managedBrowserMonitorDelay(&state); got != managedBrowserDetectedProbeInterval {
		t.Fatalf("detected monitor delay = %s, want %s", got, managedBrowserDetectedProbeInterval)
	}
}
