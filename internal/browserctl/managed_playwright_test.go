package browserctl

import (
	"errors"
	"runtime"
	"strings"
	"testing"
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
