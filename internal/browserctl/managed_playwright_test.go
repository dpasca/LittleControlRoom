package browserctl

import (
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
