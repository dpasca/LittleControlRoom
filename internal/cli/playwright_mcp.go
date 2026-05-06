package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"lcroom/internal/browserctl"
)

var (
	detectManagedBrowserProcess = browserctl.DetectManagedBrowserProcess
	hideManagedBrowserProcess   = browserctl.HideManagedBrowserProcess
	readManagedPlaywrightState  = browserctl.ReadManagedPlaywrightState
	writeManagedPlaywrightState = browserctl.WriteManagedPlaywrightState
)

const (
	managedBrowserMonitorInterval           = 100 * time.Millisecond
	managedBrowserHiddenEnforcementInterval = 250 * time.Millisecond
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

	state := browserctl.ManagedPlaywrightState{
		SessionKey:  opts.sessionKey,
		ProfileKey:  opts.profileKey,
		Provider:    opts.provider,
		ProjectPath: opts.projectPath,
		LaunchMode:  opts.launchMode,
		Policy:      browserctl.PolicyFromEnv(),
		UpdatedAt:   time.Now().UTC(),
	}
	if err := writeManagedPlaywrightState(paths, state); err != nil {
		fmt.Fprintf(os.Stderr, "playwright-mcp state init failed: %v\n", err)
		return 1
	}

	childArgs := []string{
		"--output-dir", paths.OutputDir,
		"--user-data-dir", paths.ProfileDir,
	}
	if opts.launchMode == browserctl.ManagedLaunchModeHeadless {
		childArgs = append([]string{"--headless"}, childArgs...)
	}

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
	go monitorManagedPlaywrightBrowser(ctx, paths, cmd.Process.Pid, opts.launchMode == browserctl.ManagedLaunchModeBackground)
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

func monitorManagedPlaywrightBrowser(ctx context.Context, paths browserctl.ManagedPlaywrightPaths, rootPID int, hideOnFirstDetect bool) {
	if rootPID <= 0 {
		return
	}
	ticker := time.NewTicker(managedBrowserMonitorInterval)
	defer ticker.Stop()

	state := managedBrowserMonitorState{}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = reconcileManagedPlaywrightBrowser(paths, rootPID, hideOnFirstDetect, &state, time.Now().UTC())
		}
	}
}

type managedBrowserMonitorState struct {
	hiddenByLCR     bool
	lastHideAttempt time.Time
}

func reconcileManagedPlaywrightBrowser(paths browserctl.ManagedPlaywrightPaths, rootPID int, keepHidden bool, monitorState *managedBrowserMonitorState, now time.Time) error {
	if rootPID <= 0 {
		return nil
	}
	detected, ok, err := detectManagedBrowserProcess(rootPID)
	if err != nil || !ok {
		return err
	}
	return browserctl.WithManagedPlaywrightStateLock(paths.DataDir, paths.SessionKey, func() error {
		state, readErr := readManagedPlaywrightState(paths.DataDir, paths.SessionKey)
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
		state.BrowserPID = detected.PID
		state.BrowserAppPath = detected.AppPath
		state.BrowserAppName = detected.AppName
		state.BrowserExecutable = detected.ExecutablePath
		state.RevealSupported = detected.PID > 0 || detected.AppPath != "" || detected.AppName != ""
		state.UpdatedAt = now
		if shouldHideManagedBrowser(keepHidden, state, monitorState, now) {
			if monitorState != nil {
				monitorState.lastHideAttempt = now
			}
			if err := hideManagedBrowserProcess(detected.PID); err == nil {
				state.Hidden = true
				if monitorState != nil {
					monitorState.hiddenByLCR = true
				}
			}
		}
		return writeManagedPlaywrightState(paths, state)
	})
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
