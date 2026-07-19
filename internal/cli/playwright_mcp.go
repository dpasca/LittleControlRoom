package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"lcroom/internal/browserctl"
)

var (
	detectManagedBrowserProcessFromProfileLock = browserctl.DetectManagedBrowserProcessFromProfileLock
	detectManagedBrowserProcess                = browserctl.DetectManagedBrowserProcess
	hideManagedBrowserSession                  = browserctl.HideManagedPlaywrightSession
	managedBrowserProcessAlive                 = isManagedBrowserProcessAlive
	readManagedPlaywrightState                 = browserctl.ReadManagedPlaywrightState
	writeManagedPlaywrightState                = browserctl.WriteManagedPlaywrightState
)

const (
	managedBrowserUndetectedProbeInterval   = 100 * time.Millisecond
	managedBrowserDetectedProbeInterval     = 250 * time.Millisecond
	managedBrowserDiscoveryInitialBackoff   = 250 * time.Millisecond
	managedBrowserDiscoveryMaxBackoff       = 2 * time.Second
	managedBrowserDiscoveryRefreshInterval  = 30 * time.Second
	managedBrowserHiddenEnforcementInterval = 250 * time.Millisecond
	managedBrowserStateHeartbeatInterval    = 2 * time.Second
)

type playwrightMCPOptions struct {
	dataDir     string
	projectPath string
	provider    string
	sessionKey  string
	profileKey  string
	launchMode  browserctl.ManagedLaunchMode
}

func runPlaywrightMCP(args []string) int {
	opts, err := parsePlaywrightMCPOptions(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "playwright-mcp config error: %v\n", err)
		return 2
	}

	paths, err := browserctl.ManagedPlaywrightPathsFor(
		opts.dataDir,
		opts.provider,
		opts.projectPath,
		opts.sessionKey,
		opts.profileKey,
		opts.launchMode,
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "playwright-mcp paths error: %v\n", err)
		return 1
	}

	browserExecutable := browserctl.ManagedBrowserExecutablePathForLaunchMode(opts.launchMode)
	preflight, err := browserctl.PrepareManagedPlaywrightProfileForLaunch(
		paths,
		browserctl.ManagedBrowserExecutablePathForCompatibilityCheck(opts.launchMode),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "playwright-mcp profile preflight failed: %v\n", err)
		return 1
	}
	writePlaywrightMCPProfilePreflightNotices(os.Stderr, preflight)

	state := browserctl.ManagedPlaywrightState{
		SessionKey:              paths.SessionKey,
		ProfileKey:              paths.ProfileKey,
		Provider:                paths.Provider,
		ProjectPath:             paths.ProjectPath,
		LaunchMode:              paths.LaunchMode,
		Policy:                  browserctl.PolicyFromEnv(),
		ProfileBackupPath:       preflight.ProfileBackupPath,
		ProfileRecoveryReason:   preflight.RecoveryReason(),
		ProfilePreflightWarning: preflight.CompatibilityWarning,
		UpdatedAt:               time.Now().UTC(),
	}
	if err := writeManagedPlaywrightState(paths, state); err != nil {
		fmt.Fprintf(os.Stderr, "playwright-mcp state init failed: %v\n", err)
		return 1
	}

	childArgs := playwrightMCPChildArgsWithExecutable(paths, opts.launchMode, browserExecutable)

	cmd := exec.Command("mcp-server-playwright", childArgs...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()

	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "playwright-mcp start failed: %v\n", err)
		return 1
	}

	state.MCPPID = cmd.Process.Pid
	state.UpdatedAt = time.Now().UTC()
	if err := writeManagedPlaywrightState(paths, state); err != nil {
		fmt.Fprintf(os.Stderr, "playwright-mcp state update failed: %v\n", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go monitorManagedPlaywrightBrowser(ctx, paths, cmd.Process.Pid, opts.launchMode == browserctl.ManagedLaunchModeBackground, browserExecutable)
	go forwardPlaywrightMCPSignals(ctx, cmd)

	err = cmd.Wait()
	cancel()

	state.UpdatedAt = time.Now().UTC()
	state.MCPPID = 0
	_ = writeManagedPlaywrightState(paths, state)

	if err == nil {
		return 0
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode()
	}
	fmt.Fprintf(os.Stderr, "playwright-mcp wait failed: %v\n", err)
	return 1
}

func writePlaywrightMCPProfilePreflightNotices(w io.Writer, preflight browserctl.ManagedPlaywrightProfilePreflight) {
	if preflight.ProfileBackupPath != "" {
		fmt.Fprintf(w, "playwright-mcp %s; backup=%s\n", preflight.RecoveryReason(), preflight.ProfileBackupPath)
	}
	if warning := strings.TrimSpace(preflight.CompatibilityWarning); warning != "" {
		fmt.Fprintf(w, "playwright-mcp warning: %s\n", warning)
	}
}

func playwrightMCPChildArgs(paths browserctl.ManagedPlaywrightPaths, launchMode browserctl.ManagedLaunchMode) []string {
	return playwrightMCPChildArgsWithExecutable(paths, launchMode, browserctl.ManagedBrowserExecutablePathForLaunchMode(launchMode))
}

func playwrightMCPChildArgsWithExecutable(paths browserctl.ManagedPlaywrightPaths, launchMode browserctl.ManagedLaunchMode, browserPath string) []string {
	args := []string{
		"--output-dir", paths.OutputDir,
		"--user-data-dir", paths.ProfileDir,
	}
	if launchMode.Normalize() == browserctl.ManagedLaunchModeHeadless {
		args = append([]string{"--headless"}, args...)
	}
	if browserPath = strings.TrimSpace(browserPath); browserPath != "" {
		args = append(args, "--executable-path", browserPath)
	}
	return args
}

func parsePlaywrightMCPOptions(args []string) (playwrightMCPOptions, error) {
	fs := flag.NewFlagSet("playwright-mcp", flag.ContinueOnError)
	dataDir := fs.String("data-dir", browserctl.DefaultDataDir(), "LCR data dir")
	projectPath := fs.String("project-path", "", "project path")
	provider := fs.String("provider", "codex", "embedded provider")
	sessionKey := fs.String("session-key", "", "managed Playwright session key")
	profileKey := fs.String("profile-key", "", "managed Playwright profile key")
	launchMode := fs.String("launch-mode", string(browserctl.ManagedLaunchModeHeadless), "managed Playwright launch mode")
	if err := fs.Parse(args); err != nil {
		return playwrightMCPOptions{}, err
	}
	options := playwrightMCPOptions{
		dataDir:     *dataDir,
		projectPath: strings.TrimSpace(*projectPath),
		provider:    strings.TrimSpace(*provider),
		sessionKey:  strings.TrimSpace(*sessionKey),
		profileKey:  strings.TrimSpace(*profileKey),
		launchMode:  browserctl.ManagedLaunchMode(strings.TrimSpace(*launchMode)).Normalize(),
	}
	if options.projectPath == "" {
		return playwrightMCPOptions{}, fmt.Errorf("--project-path is required")
	}
	if options.sessionKey == "" {
		return playwrightMCPOptions{}, fmt.Errorf("--session-key is required")
	}
	if options.profileKey == "" {
		return playwrightMCPOptions{}, fmt.Errorf("--profile-key is required")
	}
	return options, nil
}

func forwardPlaywrightMCPSignals(ctx context.Context, cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	sigCh := make(chan os.Signal, 4)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	for {
		select {
		case <-ctx.Done():
			return
		case sig := <-sigCh:
			_ = cmd.Process.Signal(sig)
		}
	}
}

func monitorManagedPlaywrightBrowser(ctx context.Context, paths browserctl.ManagedPlaywrightPaths, rootPID int, hideOnFirstDetect bool, browserExecutable string) {
	if rootPID <= 0 {
		return
	}
	state := managedBrowserMonitorState{browserExecutable: strings.TrimSpace(browserExecutable)}
	timer := time.NewTimer(0)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			_ = reconcileManagedPlaywrightBrowser(paths, rootPID, hideOnFirstDetect, &state, time.Now().UTC())
			timer.Reset(managedBrowserMonitorDelay(&state))
		}
	}
}

type managedBrowserMonitorState struct {
	hiddenByLCR       bool
	lastHideAttempt   time.Time
	browserExecutable string
	detectedBrowser   browserctl.ManagedBrowserProcess
	discoveryBackoff  time.Duration
	nextDiscoveryAt   time.Time
}

func managedBrowserMonitorDelay(state *managedBrowserMonitorState) time.Duration {
	if state != nil && state.detectedBrowser.PID > 0 {
		return managedBrowserDetectedProbeInterval
	}
	// Chromium can launch lazily after the MCP wrapper has been idle. Keep the
	// original fast hide response using only its cheap profile-lock signal.
	return managedBrowserUndetectedProbeInterval
}

func reconcileManagedPlaywrightBrowser(paths browserctl.ManagedPlaywrightPaths, rootPID int, keepHidden bool, monitorState *managedBrowserMonitorState, now time.Time) error {
	if rootPID <= 0 {
		return nil
	}
	detected, ok, err := managedBrowserForReconcile(paths.ProfileDir, rootPID, monitorState, now)
	if err != nil || !ok {
		return err
	}
	shouldHide := false
	err = browserctl.WithManagedPlaywrightStateLock(paths.DataDir, paths.SessionKey, func() error {
		state, readErr := readManagedPlaywrightState(paths.DataDir, paths.SessionKey)
		previous := state
		if readErr != nil {
			state = browserctl.ManagedPlaywrightState{
				SessionKey:  paths.SessionKey,
				ProfileKey:  paths.ProfileKey,
				Provider:    paths.Provider,
				ProjectPath: paths.ProjectPath,
				LaunchMode:  paths.LaunchMode,
				Policy:      browserctl.PolicyFromEnv(),
			}
			if monitorState != nil && monitorState.hiddenByLCR {
				state.Hidden = true
			}
		}
		state.MCPPID = rootPID
		state.BrowserPID = detected.PID
		state.BrowserAppPath = detected.AppPath
		state.BrowserAppName = detected.AppName
		state.BrowserExecutable = detected.ExecutablePath
		state.RevealSupported = detected.PID > 0 || detected.AppPath != "" || detected.AppName != ""
		shouldHide = shouldHideManagedBrowser(keepHidden, state, monitorState, now)
		if readErr == nil && !managedBrowserStateNeedsWrite(previous, state, now) {
			return nil
		}
		state.UpdatedAt = now
		return writeManagedPlaywrightState(paths, state)
	})
	if err != nil || !shouldHide {
		return err
	}
	if monitorState != nil {
		monitorState.lastHideAttempt = now
	}
	hidden, hideErr := hideManagedBrowserSession(paths.DataDir, paths.SessionKey, detected)
	if hideErr == nil && hidden && monitorState != nil {
		monitorState.hiddenByLCR = true
	}
	// Browser hiding is best-effort. Metadata refresh should continue even if
	// macOS rejects a visibility transition or a foreground handoff suppresses
	// this session's hide attempt.
	return nil
}

func managedBrowserForReconcile(profileDir string, rootPID int, monitorState *managedBrowserMonitorState, now time.Time) (browserctl.ManagedBrowserProcess, bool, error) {
	if monitorState == nil {
		return detectManagedBrowserProcess(rootPID)
	}

	// Discovery shells out to a full process-table snapshot. Cache a live PID
	// and reserve that expensive lookup for startup, restarts, and safety checks.
	cached := monitorState.detectedBrowser
	cachedAlive := cached.PID > 0 && managedBrowserProcessAlive(cached.PID)
	if cached.PID > 0 && !cachedAlive {
		monitorState.detectedBrowser = browserctl.ManagedBrowserProcess{}
		monitorState.nextDiscoveryAt = time.Time{}
	}
	if cachedAlive && (monitorState.nextDiscoveryAt.IsZero() || now.Before(monitorState.nextDiscoveryAt)) {
		return cached, true, nil
	}
	if !cachedAlive {
		// Chromium's SingletonLock appears as the browser claims the managed
		// profile. Check it on every fast probe even while full discovery is in
		// backoff, preserving launch/hide latency without spawning `ps`.
		if strings.TrimSpace(profileDir) != "" {
			if detected, ok := detectManagedBrowserProcessFromProfileLock(profileDir, monitorState.browserExecutable); ok {
				monitorState.detectedBrowser = detected
				monitorState.discoveryBackoff = 0
				monitorState.nextDiscoveryAt = now.Add(managedBrowserDiscoveryRefreshInterval)
				return detected, true, nil
			}
		}
		if !monitorState.nextDiscoveryAt.IsZero() && now.Before(monitorState.nextDiscoveryAt) {
			return browserctl.ManagedBrowserProcess{}, false, nil
		}
	}

	detected, ok, err := detectManagedBrowserProcess(rootPID)
	if err != nil || !ok {
		if cachedAlive {
			monitorState.nextDiscoveryAt = now.Add(managedBrowserDiscoveryRefreshInterval)
			return cached, true, nil
		}
		monitorState.discoveryBackoff = nextManagedBrowserDiscoveryBackoff(monitorState.discoveryBackoff)
		monitorState.nextDiscoveryAt = now.Add(monitorState.discoveryBackoff)
		return detected, ok, err
	}

	monitorState.detectedBrowser = detected
	monitorState.discoveryBackoff = 0
	if detected.PID > 0 {
		monitorState.nextDiscoveryAt = now.Add(managedBrowserDiscoveryRefreshInterval)
	} else {
		monitorState.discoveryBackoff = managedBrowserDiscoveryInitialBackoff
		monitorState.nextDiscoveryAt = now.Add(monitorState.discoveryBackoff)
	}
	return detected, true, nil
}

func nextManagedBrowserDiscoveryBackoff(current time.Duration) time.Duration {
	if current < managedBrowserDiscoveryInitialBackoff {
		return managedBrowserDiscoveryInitialBackoff
	}
	next := current * 2
	if next > managedBrowserDiscoveryMaxBackoff {
		return managedBrowserDiscoveryMaxBackoff
	}
	return next
}

func isManagedBrowserProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

func managedBrowserStateNeedsWrite(previous, next browserctl.ManagedPlaywrightState, now time.Time) bool {
	previous = previous.Normalize()
	next = next.Normalize()
	lastWrite := previous.UpdatedAt
	previous.UpdatedAt = time.Time{}
	next.UpdatedAt = time.Time{}
	if previous != next {
		return true
	}
	if lastWrite.IsZero() {
		return true
	}
	age := now.Sub(lastWrite)
	return age < 0 || age >= managedBrowserStateHeartbeatInterval
}

func shouldHideManagedBrowser(keepHidden bool, state browserctl.ManagedPlaywrightState, monitorState *managedBrowserMonitorState, now time.Time) bool {
	if !keepHidden {
		return false
	}
	if monitorState != nil &&
		!monitorState.lastHideAttempt.IsZero() &&
		now.Sub(monitorState.lastHideAttempt) < managedBrowserHiddenEnforcementInterval {
		return false
	}
	if monitorState == nil || !monitorState.hiddenByLCR {
		return true
	}
	if !state.Normalize().Hidden {
		return false
	}
	return true
}
