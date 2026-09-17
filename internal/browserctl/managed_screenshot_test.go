package browserctl

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func screenshotTestSession(t *testing.T) (ManagedPlaywrightPaths, *int, *int) {
	t.Helper()
	paths := audioTestPaths(t, ManagedLaunchModeBackground)
	state, _ := ReadManagedPlaywrightState(paths.DataDir, paths.SessionKey)
	state.Hidden = true
	state.BrowserPID = 123
	if err := WriteManagedPlaywrightState(paths, state); err != nil {
		t.Fatal(err)
	}
	show, hide := 0, 0
	oldShow, oldHide := managedScreenshotShow, managedPlaywrightProcessHider
	managedScreenshotShow = func(pid int) error {
		if pid != 123 {
			t.Fatalf("show PID = %d", pid)
		}
		show++
		return nil
	}
	managedPlaywrightProcessHider = func(pid int) error {
		if pid != 123 {
			t.Fatalf("hide PID = %d", pid)
		}
		hide++
		return nil
	}
	t.Cleanup(func() { managedScreenshotShow, managedPlaywrightProcessHider = oldShow, oldHide })
	return paths, &show, &hide
}

func TestManagedScreenshotOverlapsAndMonitor(t *testing.T) {
	paths, show, hide := screenshotTestSession(t)
	first, err := beginManagedScreenshot(paths.DataDir, paths.SessionKey)
	if err != nil {
		t.Fatal(err)
	}
	defer first()
	second, err := beginManagedScreenshot(paths.DataDir, paths.SessionKey)
	if err != nil {
		t.Fatal(err)
	}
	defer second()
	state, _ := ReadManagedPlaywrightState(paths.DataDir, paths.SessionKey)
	if !state.Hidden || state.AudioAllowed || len(state.ScreenshotLeases) != 2 {
		t.Fatalf("capture changed user visibility/audio intent: %#v", state)
	}
	if hidden, err := HideManagedPlaywrightSession(paths.DataDir, paths.SessionKey, ManagedBrowserProcess{PID: 123}); err != nil || hidden {
		t.Fatalf("monitor hid during capture: %v, %v", hidden, err)
	}
	if err := first(); err != nil {
		t.Fatal(err)
	}
	if *hide != 0 {
		t.Fatal("first completion hid overlapping capture")
	}
	if err := second(); err != nil {
		t.Fatal(err)
	}
	if err := second(); err != nil {
		t.Fatal(err)
	}
	if *show != 2 || *hide != 1 {
		t.Fatalf("show=%d hide=%d", *show, *hide)
	}
	state, _ = ReadManagedPlaywrightState(paths.DataDir, paths.SessionKey)
	if len(state.ScreenshotLeases) != 0 {
		t.Fatal("capture lease leaked")
	}
}

func TestManagedScreenshotExplicitRevealWins(t *testing.T) {
	paths, _, hide := screenshotTestSession(t)
	finish, err := beginManagedScreenshot(paths.DataDir, paths.SessionKey)
	if err != nil {
		t.Fatal(err)
	}
	defer finish()
	if _, err := MarkManagedPlaywrightStateRevealed(paths.DataDir, paths.SessionKey); err != nil {
		t.Fatal(err)
	}
	if err := finish(); err != nil {
		t.Fatal(err)
	}
	if *hide != 0 {
		t.Fatal("capture cleanup overrode user reveal")
	}
}

func TestManagedScreenshotGuardsLazyBrowserBeforeFirstHide(t *testing.T) {
	paths, show, hide := screenshotTestSession(t)
	state, _ := ReadManagedPlaywrightState(paths.DataDir, paths.SessionKey)
	state.Hidden, state.BrowserPID = false, 0
	if err := WriteManagedPlaywrightState(paths, state); err != nil {
		t.Fatal(err)
	}
	finish, err := beginManagedScreenshot(paths.DataDir, paths.SessionKey)
	if err != nil {
		t.Fatal(err)
	}
	defer finish()
	if hidden, err := HideManagedPlaywrightSession(paths.DataDir, paths.SessionKey, ManagedBrowserProcess{PID: 123}); err != nil || hidden {
		t.Fatalf("first monitor hide interrupted screenshot: %v %v", hidden, err)
	}
	if err := finish(); err != nil {
		t.Fatal(err)
	}
	if *show != 0 || *hide != 0 {
		t.Fatal("lazy capture should not call OS visibility before process discovery")
	}
	if hidden, err := HideManagedPlaywrightSession(paths.DataDir, paths.SessionKey, ManagedBrowserProcess{PID: 123}); err != nil || !hidden {
		t.Fatalf("first monitor hide did not resume: %v %v", hidden, err)
	}
}

func TestManagedScreenshotDoesNotHideReplacementBrowser(t *testing.T) {
	paths, _, hide := screenshotTestSession(t)
	finish, err := beginManagedScreenshot(paths.DataDir, paths.SessionKey)
	if err != nil {
		t.Fatal(err)
	}
	defer finish()
	state, _ := ReadManagedPlaywrightState(paths.DataDir, paths.SessionKey)
	state.BrowserPID = 456
	if err := WriteManagedPlaywrightState(paths, state); err != nil {
		t.Fatal(err)
	}
	if err := finish(); err != nil {
		t.Fatal(err)
	}
	if *hide != 0 {
		t.Fatal("cleanup hid a replacement process")
	}
}

func TestManagedScreenshotShowFailureRestores(t *testing.T) {
	paths, _, hide := screenshotTestSession(t)
	managedScreenshotShow = func(int) error { return errors.New("show failed") }
	if _, err := beginManagedScreenshot(paths.DataDir, paths.SessionKey); err == nil {
		t.Fatal("expected error")
	}
	state, _ := ReadManagedPlaywrightState(paths.DataDir, paths.SessionKey)
	if len(state.ScreenshotLeases) != 0 || !state.Hidden || *hide != 1 {
		t.Fatalf("failed capture leaked: %#v, hides=%d", state, *hide)
	}
}

func TestManagedScreenshotExpiredLeaseDoesNotBlockMonitor(t *testing.T) {
	paths, _, hide := screenshotTestSession(t)
	state, _ := ReadManagedPlaywrightState(paths.DataDir, paths.SessionKey)
	state.ScreenshotLeases = map[string]time.Time{"crashed-owner": time.Now().Add(-time.Second)}
	if err := WriteManagedPlaywrightState(paths, state); err != nil {
		t.Fatal(err)
	}
	if hidden, err := HideManagedPlaywrightSession(paths.DataDir, paths.SessionKey, ManagedBrowserProcess{PID: 123}); err != nil || !hidden || *hide != 1 {
		t.Fatalf("expired capture blocked hiding: hidden=%v err=%v", hidden, err)
	}
}

func TestManagedScreenshotLeavesVisibleAndHeadlessAlone(t *testing.T) {
	for _, mode := range []ManagedLaunchMode{ManagedLaunchModeHeadless, ManagedLaunchModeHeaded, ManagedLaunchModeBackground} {
		t.Run(string(mode), func(t *testing.T) {
			paths, show, hide := screenshotTestSession(t)
			state, _ := ReadManagedPlaywrightState(paths.DataDir, paths.SessionKey)
			state.LaunchMode = mode
			state.Hidden = mode != ManagedLaunchModeBackground
			if err := WriteManagedPlaywrightState(paths, state); err != nil {
				t.Fatal(err)
			}
			finish, err := beginManagedScreenshot(paths.DataDir, paths.SessionKey)
			if err != nil {
				t.Fatal(err)
			}
			if err := finish(); err != nil {
				t.Fatal(err)
			}
			if *show != 0 || *hide != 0 {
				t.Fatal("non-hidden-background session changed visibility")
			}
		})
	}
}

// Opt-in integration check against an already-running managed browser. It does
// not launch or navigate a browser: take the screenshot through its registered
// MCP tool after "ready" appears, then create "release" in the signal directory.
func TestManagedScreenshotExistingSession(t *testing.T) {
	dataDir, session := os.Getenv("LCR_SCREENSHOT_TEST_DATA_DIR"), os.Getenv("LCR_SCREENSHOT_TEST_SESSION")
	signals := os.Getenv("LCR_SCREENSHOT_TEST_SIGNALS")
	if runtime.GOOS != "darwin" || dataDir == "" || session == "" || signals == "" {
		t.Skip("requires explicit existing-session screenshot test configuration")
	}
	state, err := ReadManagedPlaywrightState(dataDir, session)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Hidden || state.BrowserPID <= 0 {
		t.Fatal("test requires a hidden, running browser")
	}
	finish, err := BeginManagedScreenshot(dataDir, session)
	if err != nil {
		t.Fatal(err)
	}
	defer finish()
	waitForCapture := func() error {
		if err := os.WriteFile(filepath.Join(signals, "ready"), []byte("ready"), 0600); err != nil {
			return err
		}
		deadline := time.Now().Add(45 * time.Second)
		for {
			if _, err := os.Stat(filepath.Join(signals, "release")); err == nil {
				return nil
			}
			if time.Now().After(deadline) {
				return errors.New("timed out waiting for MCP screenshot")
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	if os.Getenv("LCR_SCREENSHOT_TEST_LEGACY_MONITOR") == "1" {
		// A pinned pre-fix wrapper does not recognize leases. Hold its existing
		// visibility lock only for this smoke check; production uses the lease
		// so handoffs and other sessions are never blocked across capture.
		err = withManagedPlaywrightVisibilityLock(dataDir, func() error {
			if err := managedScreenshotShow(state.BrowserPID); err != nil {
				return err
			}
			return waitForCapture()
		})
	} else {
		err = waitForCapture()
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := finish(); err != nil {
		t.Fatal(err)
	}
	state, err = ReadManagedPlaywrightState(dataDir, session)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Hidden || state.AudioAllowed || len(state.ScreenshotLeases) != 0 {
		t.Fatalf("capture did not restore state: %#v", state)
	}
}
