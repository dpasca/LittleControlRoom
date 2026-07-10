package browserctl

import (
	"errors"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestManagedLaunchModeForPolicy(t *testing.T) {
	policy := Policy{
		ManagementMode:     ManagementModeManaged,
		DefaultBrowserMode: BrowserModeHeadless,
		LoginMode:          LoginModePromote,
		IsolationScope:     IsolationScopeTask,
	}
	got := ManagedLaunchModeForPolicy(policy)
	want := ManagedLaunchModeHeadless
	if runtime.GOOS == "darwin" {
		want = ManagedLaunchModeBackground
	}
	if got != want {
		t.Fatalf("ManagedLaunchModeForPolicy() = %q, want %q", got, want)
	}
}

func TestManagedProfileKeyProjectScopeStable(t *testing.T) {
	policy := Policy{
		ManagementMode:     ManagementModeManaged,
		DefaultBrowserMode: BrowserModeHeadless,
		LoginMode:          LoginModePromote,
		IsolationScope:     IsolationScopeProject,
	}
	first := ManagedProfileKey(policy, "codex", "/tmp/demo", "", "session-a")
	second := ManagedProfileKey(policy, "codex", "/tmp/demo", "", "session-b")
	if first != second {
		t.Fatalf("ManagedProfileKey() = %q and %q, want stable project-scoped key", first, second)
	}
}

func TestExtractMacAppPath(t *testing.T) {
	args := "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome --user-data-dir=/tmp/demo"
	got := extractMacAppPath(args)
	if got != "/Applications/Google Chrome.app" {
		t.Fatalf("extractMacAppPath() = %q, want /Applications/Google Chrome.app", got)
	}
}

func TestManagedBrowserCandidateRecognizesChromeAppProcess(t *testing.T) {
	process := osProcessSnapshot{
		PID:     123,
		PPID:    45,
		Command: "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		Args:    "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome --remote-debugging-port=9222",
	}
	candidate, ok := managedBrowserCandidate(process)
	if !ok {
		t.Fatalf("managedBrowserCandidate() = not ok, want ok")
	}
	if candidate.PID != 123 {
		t.Fatalf("candidate PID = %d, want 123", candidate.PID)
	}
	if !strings.Contains(candidate.AppPath, "Google Chrome.app") {
		t.Fatalf("candidate AppPath = %q, want Google Chrome.app", candidate.AppPath)
	}
	if candidate.ExecutablePath != "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" {
		t.Fatalf("candidate ExecutablePath = %q, want full Chrome executable", candidate.ExecutablePath)
	}
}

func TestManagedBrowserCandidateUsesFullExecutableFromArgsWhenCommandIsTruncated(t *testing.T) {
	process := osProcessSnapshot{
		PID:     123,
		PPID:    45,
		Command: "/Users/davide/Li",
		Args:    "/Users/davide/Library/Caches/ms-playwright/chromium-1194/chrome-mac/Chromium.app/Contents/MacOS/Chromium --remote-debugging-port=52942 about:blank",
	}
	candidate, ok := managedBrowserCandidate(process)
	if !ok {
		t.Fatalf("managedBrowserCandidate() = not ok, want ok")
	}
	want := "/Users/davide/Library/Caches/ms-playwright/chromium-1194/chrome-mac/Chromium.app/Contents/MacOS/Chromium"
	if candidate.ExecutablePath != want {
		t.Fatalf("candidate ExecutablePath = %q, want %q", candidate.ExecutablePath, want)
	}
}

func TestRevealManagedPlaywrightSessionMarksRevealedBeforeOSReveal(t *testing.T) {
	paths, err := ManagedPlaywrightPathsFor(
		t.TempDir(),
		"codex",
		"/tmp/demo",
		"session-demo",
		"profile-demo",
		ManagedLaunchModeBackground,
	)
	if err != nil {
		t.Fatalf("ManagedPlaywrightPathsFor() error = %v", err)
	}
	initial := ManagedPlaywrightState{
		SessionKey:  paths.SessionKey,
		ProfileKey:  paths.ProfileKey,
		Provider:    paths.Provider,
		ProjectPath: paths.ProjectPath,
		LaunchMode:  paths.LaunchMode,
		Policy:      DefaultPolicy(),
		BrowserPID:  123,
		Hidden:      true,
	}
	if err := WriteManagedPlaywrightState(paths, initial); err != nil {
		t.Fatalf("write initial state: %v", err)
	}

	previousRevealer := managedPlaywrightStateRevealer
	t.Cleanup(func() {
		managedPlaywrightStateRevealer = previousRevealer
	})
	managedPlaywrightStateRevealer = func(state ManagedPlaywrightState) error {
		if state.Hidden {
			t.Fatalf("revealer saw Hidden=true, want reveal intent persisted first")
		}
		stored, err := ReadManagedPlaywrightState(paths.DataDir, paths.SessionKey)
		if err != nil {
			t.Fatalf("read state during reveal: %v", err)
		}
		if stored.Hidden {
			t.Fatalf("stored.Hidden during reveal = true, want false")
		}
		foreground, ok, err := readManagedPlaywrightForegroundState(paths.DataDir)
		if err != nil {
			t.Fatalf("read foreground state during reveal: %v", err)
		}
		if !ok || foreground.SessionKey != paths.SessionKey || foreground.Hidden {
			t.Fatalf("foreground state during reveal = %#v, ok=%v", foreground, ok)
		}
		return nil
	}

	updated, err := RevealManagedPlaywrightSession(paths.DataDir, paths.SessionKey)
	if err != nil {
		t.Fatalf("RevealManagedPlaywrightSession() error = %v", err)
	}
	if updated.Hidden {
		t.Fatalf("updated.Hidden = true, want false")
	}
}

func TestRevealManagedPlaywrightSessionRestoresHiddenStateOnRevealFailure(t *testing.T) {
	paths, err := ManagedPlaywrightPathsFor(
		t.TempDir(),
		"codex",
		"/tmp/demo",
		"session-demo",
		"profile-demo",
		ManagedLaunchModeBackground,
	)
	if err != nil {
		t.Fatalf("ManagedPlaywrightPathsFor() error = %v", err)
	}
	initial := ManagedPlaywrightState{
		SessionKey:  paths.SessionKey,
		ProfileKey:  paths.ProfileKey,
		Provider:    paths.Provider,
		ProjectPath: paths.ProjectPath,
		LaunchMode:  paths.LaunchMode,
		Policy:      DefaultPolicy(),
		BrowserPID:  123,
		Hidden:      true,
	}
	if err := WriteManagedPlaywrightState(paths, initial); err != nil {
		t.Fatalf("write initial state: %v", err)
	}

	previousRevealer := managedPlaywrightStateRevealer
	t.Cleanup(func() {
		managedPlaywrightStateRevealer = previousRevealer
	})
	revealErr := errors.New("reveal failed")
	managedPlaywrightStateRevealer = func(state ManagedPlaywrightState) error {
		return revealErr
	}

	updated, err := RevealManagedPlaywrightSession(paths.DataDir, paths.SessionKey)
	if !errors.Is(err, revealErr) {
		t.Fatalf("RevealManagedPlaywrightSession() error = %v, want %v", err, revealErr)
	}
	if !updated.Hidden {
		t.Fatalf("updated.Hidden = false, want restored true")
	}
	stored, err := ReadManagedPlaywrightState(paths.DataDir, paths.SessionKey)
	if err != nil {
		t.Fatalf("read state after failed reveal: %v", err)
	}
	if !stored.Hidden {
		t.Fatalf("stored.Hidden after failed reveal = false, want true")
	}
	if foreground, ok, foregroundErr := readManagedPlaywrightForegroundState(paths.DataDir); foregroundErr != nil || ok {
		t.Fatalf("foreground state after failed reveal = %#v, ok=%v, err=%v; want absent", foreground, ok, foregroundErr)
	}
}

func TestHideManagedPlaywrightSessionDoesNotCollapseForegroundSibling(t *testing.T) {
	dataDir := t.TempDir()
	foregroundPaths, err := ManagedPlaywrightPathsFor(
		dataDir,
		"codex",
		"/tmp/foreground",
		"session-foreground",
		"profile-foreground",
		ManagedLaunchModeBackground,
	)
	if err != nil {
		t.Fatal(err)
	}
	targetPaths, err := ManagedPlaywrightPathsFor(
		dataDir,
		"codex",
		"/tmp/background",
		"session-background",
		"profile-background",
		ManagedLaunchModeBackground,
	)
	if err != nil {
		t.Fatal(err)
	}

	foreground := ManagedPlaywrightState{
		SessionKey:        foregroundPaths.SessionKey,
		ProfileKey:        foregroundPaths.ProfileKey,
		Provider:          foregroundPaths.Provider,
		ProjectPath:       foregroundPaths.ProjectPath,
		LaunchMode:        foregroundPaths.LaunchMode,
		Policy:            DefaultPolicy(),
		MCPPID:            os.Getpid(),
		BrowserPID:        os.Getpid(),
		BrowserAppPath:    "/Applications/Chromium.app",
		BrowserAppName:    "Chromium",
		BrowserExecutable: "/Applications/Chromium.app/Contents/MacOS/Chromium",
		Hidden:            false,
		UpdatedAt:         time.Now().UTC(),
	}
	if err := WriteManagedPlaywrightState(foregroundPaths, foreground); err != nil {
		t.Fatal(err)
	}
	if err := writeManagedPlaywrightForegroundState(dataDir, foreground); err != nil {
		t.Fatal(err)
	}
	target := ManagedPlaywrightState{
		SessionKey:        targetPaths.SessionKey,
		ProfileKey:        targetPaths.ProfileKey,
		Provider:          targetPaths.Provider,
		ProjectPath:       targetPaths.ProjectPath,
		LaunchMode:        targetPaths.LaunchMode,
		Policy:            DefaultPolicy(),
		BrowserPID:        222,
		BrowserAppPath:    foreground.BrowserAppPath,
		BrowserAppName:    foreground.BrowserAppName,
		BrowserExecutable: foreground.BrowserExecutable,
		Hidden:            true,
		UpdatedAt:         time.Now().UTC(),
	}
	if err := WriteManagedPlaywrightState(targetPaths, target); err != nil {
		t.Fatal(err)
	}

	previousHider := managedPlaywrightProcessHider
	t.Cleanup(func() { managedPlaywrightProcessHider = previousHider })
	hideCount := 0
	managedPlaywrightProcessHider = func(pid int) error {
		hideCount++
		return nil
	}

	hidden, err := HideManagedPlaywrightSession(dataDir, target.SessionKey, ManagedBrowserProcess{
		PID:            target.BrowserPID,
		AppPath:        target.BrowserAppPath,
		AppName:        target.BrowserAppName,
		ExecutablePath: target.BrowserExecutable,
	})
	if err != nil {
		t.Fatalf("HideManagedPlaywrightSession() error = %v", err)
	}
	if hidden || hideCount != 0 {
		t.Fatalf("HideManagedPlaywrightSession() hidden=%v hideCount=%d, want suppressed", hidden, hideCount)
	}
}

func TestHideManagedPlaywrightSessionAllowsDifferentBrowserApplication(t *testing.T) {
	dataDir := t.TempDir()
	foregroundPaths, err := ManagedPlaywrightPathsFor(dataDir, "codex", "/tmp/foreground", "session-foreground", "profile-foreground", ManagedLaunchModeBackground)
	if err != nil {
		t.Fatal(err)
	}
	targetPaths, err := ManagedPlaywrightPathsFor(dataDir, "codex", "/tmp/background", "session-background", "profile-background", ManagedLaunchModeBackground)
	if err != nil {
		t.Fatal(err)
	}
	foreground := ManagedPlaywrightState{
		SessionKey:        foregroundPaths.SessionKey,
		ProfileKey:        foregroundPaths.ProfileKey,
		Provider:          foregroundPaths.Provider,
		ProjectPath:       foregroundPaths.ProjectPath,
		LaunchMode:        foregroundPaths.LaunchMode,
		Policy:            DefaultPolicy(),
		MCPPID:            os.Getpid(),
		BrowserPID:        os.Getpid(),
		BrowserAppPath:    "/Applications/Chromium.app",
		BrowserAppName:    "Chromium",
		BrowserExecutable: "/Applications/Chromium.app/Contents/MacOS/Chromium",
		UpdatedAt:         time.Now().UTC(),
	}
	if err := WriteManagedPlaywrightState(foregroundPaths, foreground); err != nil {
		t.Fatal(err)
	}
	if err := writeManagedPlaywrightForegroundState(dataDir, foreground); err != nil {
		t.Fatal(err)
	}
	target := ManagedPlaywrightState{
		SessionKey:        targetPaths.SessionKey,
		ProfileKey:        targetPaths.ProfileKey,
		Provider:          targetPaths.Provider,
		ProjectPath:       targetPaths.ProjectPath,
		LaunchMode:        targetPaths.LaunchMode,
		Policy:            DefaultPolicy(),
		BrowserPID:        333,
		BrowserAppPath:    "/Applications/Firefox.app",
		BrowserAppName:    "Firefox",
		BrowserExecutable: "/Applications/Firefox.app/Contents/MacOS/firefox",
		UpdatedAt:         time.Now().UTC(),
	}
	if err := WriteManagedPlaywrightState(targetPaths, target); err != nil {
		t.Fatal(err)
	}

	previousHider := managedPlaywrightProcessHider
	t.Cleanup(func() { managedPlaywrightProcessHider = previousHider })
	hideCount := 0
	managedPlaywrightProcessHider = func(pid int) error {
		if pid != target.BrowserPID {
			t.Fatalf("hide pid = %d, want %d", pid, target.BrowserPID)
		}
		hideCount++
		return nil
	}

	hidden, err := HideManagedPlaywrightSession(dataDir, target.SessionKey, ManagedBrowserProcess{
		PID:            target.BrowserPID,
		AppPath:        target.BrowserAppPath,
		AppName:        target.BrowserAppName,
		ExecutablePath: target.BrowserExecutable,
	})
	if err != nil {
		t.Fatalf("HideManagedPlaywrightSession() error = %v", err)
	}
	if !hidden || hideCount != 1 {
		t.Fatalf("HideManagedPlaywrightSession() hidden=%v hideCount=%d, want one hide", hidden, hideCount)
	}
}

func TestMacApplicationProcessVisibilityScriptRaisesTargetWindowWhenFrontmost(t *testing.T) {
	args, err := macApplicationProcessVisibilityScript(49916, true, true)
	if err != nil {
		t.Fatalf("macApplicationProcessVisibilityScript() error = %v", err)
	}
	script := strings.Join(args, "\n")
	for _, want := range []string{
		"unix id is 49916",
		"set visible of targetProcess to true",
		`perform action "AXRaise" of window 1 of targetProcess`,
		"set frontmost of targetProcess to true",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("script missing %q:\n%s", want, script)
		}
	}
}

func TestMacApplicationProcessDelayedRaiseScriptRepeatsTargetWindowRaise(t *testing.T) {
	args, err := macApplicationProcessDelayedRaiseScript(49916, 300*time.Millisecond)
	if err != nil {
		t.Fatalf("macApplicationProcessDelayedRaiseScript() error = %v", err)
	}
	script := strings.Join(args, "\n")
	for _, want := range []string{
		"delay 0.300",
		"unix id is 49916",
		"set visible of targetProcess to true",
		`perform action "AXRaise" of window 1 of targetProcess`,
		"set frontmost of targetProcess to true",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("script missing %q:\n%s", want, script)
		}
	}
	if got := strings.Count(script, `perform action "AXRaise" of window 1 of targetProcess`); got != 2 {
		t.Fatalf("raise count = %d, want 2:\n%s", got, script)
	}
}

func TestMacApplicationNamedProcessVisibilityScriptRaisesExistingApp(t *testing.T) {
	args, err := macApplicationNamedProcessVisibilityScript("Google Chrome", true, true)
	if err != nil {
		t.Fatalf("macApplicationNamedProcessVisibilityScript() error = %v", err)
	}
	script := strings.Join(args, "\n")
	for _, want := range []string{
		`whose name is "Google Chrome"`,
		"set visible of targetProcess to true",
		`perform action "AXRaise" of window 1 of targetProcess`,
		"set frontmost of targetProcess to true",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("script missing %q:\n%s", want, script)
		}
	}
	if strings.Contains(script, "open -a") {
		t.Fatalf("named process reveal should not launch a new app/tab:\n%s", script)
	}
}

func TestMacApplicationNamedProcessDelayedRaiseScriptRepeatsTargetWindowRaise(t *testing.T) {
	args, err := macApplicationNamedProcessDelayedRaiseScript("Google Chrome", 300*time.Millisecond)
	if err != nil {
		t.Fatalf("macApplicationNamedProcessDelayedRaiseScript() error = %v", err)
	}
	script := strings.Join(args, "\n")
	for _, want := range []string{
		"delay 0.300",
		`whose name is "Google Chrome"`,
		"set visible of targetProcess to true",
		`perform action "AXRaise" of window 1 of targetProcess`,
		"set frontmost of targetProcess to true",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("script missing %q:\n%s", want, script)
		}
	}
	if got := strings.Count(script, `perform action "AXRaise" of window 1 of targetProcess`); got != 2 {
		t.Fatalf("raise count = %d, want 2:\n%s", got, script)
	}
}

func TestMacApplicationProcessVisibilityScriptDoesNotRaiseWindowWhenHiding(t *testing.T) {
	args, err := macApplicationProcessVisibilityScript(49916, false, false)
	if err != nil {
		t.Fatalf("macApplicationProcessVisibilityScript() error = %v", err)
	}
	script := strings.Join(args, "\n")
	if strings.Contains(script, "AXRaise") {
		t.Fatalf("hide script should not raise windows:\n%s", script)
	}
	if !strings.Contains(script, "set visible of targetProcess to false") {
		t.Fatalf("script should hide the target process:\n%s", script)
	}
}
