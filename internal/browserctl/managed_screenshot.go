package browserctl

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"runtime"
	"sync"
	"time"
)

const managedScreenshotLeaseDuration = time.Minute

var managedScreenshotShow = func(pid int) error {
	return setMacApplicationProcessVisible(pid, true, false)
}

func managedScreenshotLeaseActive(state ManagedPlaywrightState, now time.Time) bool {
	for _, until := range state.ScreenshotLeases {
		if until.After(now) {
			return true
		}
	}
	return false
}

// BeginManagedScreenshot temporarily unhides background Chromium without
// activating it or granting audio permission. macOS application hiding can stop
// Chromium's compositor even with Playwright's background-throttling flags.
// Hidden remains the user's intent; a real reveal changes it and wins over
// cleanup. The expiring lease prevents the monitor from hiding mid-capture.
// Call the returned function on success, error, cancellation, and transport exit.
func BeginManagedScreenshot(dataDir, sessionKey string) (func() error, error) {
	if runtime.GOOS != "darwin" {
		return func() error { return nil }, nil
	}
	return beginManagedScreenshot(dataDir, sessionKey)
}

func beginManagedScreenshot(dataDir, sessionKey string) (func() error, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return nil, err
	}
	token := hex.EncodeToString(random[:])
	pid := 0
	leased := false
	err := withManagedPlaywrightVisibilityLock(dataDir, func() error {
		return WithManagedPlaywrightStateLock(dataDir, sessionKey, func() error {
			state, err := ReadManagedPlaywrightState(dataDir, sessionKey)
			if err != nil {
				return err
			}
			if state.LaunchMode != ManagedLaunchModeBackground {
				return nil
			}
			pid = state.BrowserPID
			now := time.Now()
			if state.ScreenshotLeases == nil {
				state.ScreenshotLeases = make(map[string]time.Time)
			}
			for key, until := range state.ScreenshotLeases {
				if !until.After(now) {
					delete(state.ScreenshotLeases, key)
				}
			}
			state.ScreenshotLeases[token] = now.Add(managedScreenshotLeaseDuration)
			if err := writeManagedPlaywrightStateFor(dataDir, sessionKey, state); err != nil {
				return err
			}
			leased = true
			if state.Hidden && pid > 0 {
				return managedScreenshotShow(pid)
			}
			// Also guard a lazily launched browser before its first monitor hide.
			return nil
		})
	})
	finish := func() error {
		if !leased {
			return nil
		}
		return withManagedPlaywrightVisibilityLock(dataDir, func() error {
			foreground, hasForeground := activeManagedPlaywrightForegroundStateLocked(dataDir)
			return WithManagedPlaywrightStateLock(dataDir, sessionKey, func() error {
				state, err := ReadManagedPlaywrightState(dataDir, sessionKey)
				if err != nil {
					return err
				}
				delete(state.ScreenshotLeases, token)
				if err := writeManagedPlaywrightStateFor(dataDir, sessionKey, state); err != nil {
					return err
				}
				foregroundOwnsBrowser := hasForeground && managedForegroundBrowserMatches(foreground, ManagedBrowserProcess{PID: pid})
				if state.Hidden && pid > 0 && state.BrowserPID == pid && !foregroundOwnsBrowser && !managedScreenshotLeaseActive(state, time.Now()) {
					return managedPlaywrightProcessHider(pid)
				}
				return nil
			})
		})
	}
	if err != nil {
		if restoreErr := finish(); restoreErr != nil {
			return nil, fmt.Errorf("prepare screenshot: %w; restore browser: %v", err, restoreErr)
		}
		return nil, fmt.Errorf("prepare screenshot: %w", err)
	}
	if !leased {
		return func() error { return nil }, nil
	}
	var once sync.Once
	var finishErr error
	var timer *time.Timer
	release := func() error {
		once.Do(func() { finishErr = finish() })
		return finishErr
	}
	timer = time.AfterFunc(managedScreenshotLeaseDuration, func() { _ = release() })
	return func() error {
		timer.Stop()
		return release()
	}, nil
}
