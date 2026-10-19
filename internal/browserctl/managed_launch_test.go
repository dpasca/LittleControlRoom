package browserctl

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestManagedBrowserLauncherProcess(t *testing.T) {
	if os.Getenv("LCR_TEST_BROWSER_LAUNCHER") != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == ManagedBrowserLaunchCommand {
			os.Exit(RunManagedBrowserLaunch(os.Args[i+1:], os.Stderr))
		}
	}
	os.Exit(2)
}

func managedBrowserLauncherFixture(t *testing.T) (ManagedPlaywrightPaths, string, string) {
	t.Helper()
	dir := t.TempDir()
	// Exercise shell argument boundaries without allowing shell expansion.
	paths := ManagedPlaywrightPaths{
		SessionDir: filepath.Join(dir, "session with ' quotes"),
		ProfileDir: filepath.Join(dir, "profile with ' quotes $(ignored) `ignored`"),
	}
	for _, path := range []string{paths.SessionDir, paths.ProfileDir} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	testBinary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }
	helper := filepath.Join(dir, "test helper ' executable")
	if err := os.WriteFile(helper, []byte("#!/bin/sh\nexec "+quote(testBinary)+" -test.run=^TestManagedBrowserLauncherProcess$ -- \"$@\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	called := filepath.Join(dir, "browser-calls")
	t.Setenv("LCR_TEST_BROWSER_LAUNCHER", "1")
	t.Setenv("LCR_TEST_BROWSER_CALLS", called)
	browser := filepath.Join(dir, "fake browser ' executable")
	if err := os.WriteFile(browser, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" >> \"$LCR_TEST_BROWSER_CALLS\"\nif [ \"$LCR_TEST_BROWSER_FDS\" = 1 ]; then cat <&4 >&3; fi\nexit 23\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	launcher, err := writeManagedBrowserLauncher(paths, browser, helper)
	if err != nil {
		t.Fatal(err)
	}
	return paths, launcher, called
}

func TestManagedBrowserLauncherBlocksRepeatedLaunchesIntoLiveProfile(t *testing.T) {
	paths, launcher, called := managedBrowserLauncherFixture(t)
	host, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(paths.ProfileDir, "SingletonLock")
	owner := fmt.Sprintf("%s-%d", host, os.Getpid())
	if err := os.Symlink(owner, lockPath); err != nil {
		t.Fatal(err)
	}
	// Reproduce the evaluate/screenshot batch that previously made six calls,
	// each internally launching Chromium five times. No call may reach it now.
	for i := 0; i < 6; i++ {
		output, err := exec.Command(launcher, "--user-data-dir="+paths.ProfileDir, "about:blank").CombinedOutput()
		if err == nil || !strings.Contains(string(output), "LCR_BROWSER_LAUNCH_BLOCKED") {
			t.Fatalf("attempt %d: err=%v, output=%s", i, err, output)
		}
		// These are the exact strings the installed MCP treats as retriable.
		for _, retryTrigger := range []string{"ProcessSingleton", "Invalid URL"} {
			if strings.Contains(string(output), retryTrigger) {
				t.Fatalf("attempt %d would trigger MCP's launch retries: %s", i, output)
			}
		}
	}
	if _, err := os.Stat(called); !os.IsNotExist(err) {
		t.Fatalf("occupied profile reached the browser: %v", err)
	}
	if target, err := os.Readlink(lockPath); err != nil || target != owner {
		t.Fatalf("existing profile lock changed: %q, %v", target, err)
	}
	// Explicitly releasing ownership allows the next launch; no permanent
	// circuit state or browser/profile replacement is needed for recovery.
	if err := os.Remove(lockPath); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		output, err := exec.Command(launcher, "--user-data-dir="+paths.ProfileDir).CombinedOutput()
		if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 23 {
			t.Fatalf("recovery did not reach browser: %v, %s", err, output)
		}
	}
}

func TestManagedBrowserLauncherPreservesArgumentsOutputAndExitStatus(t *testing.T) {
	paths, launcher, called := managedBrowserLauncherFixture(t)
	want := []string{"--user-data-dir", paths.ProfileDir, "--remote-debugging-pipe", "value with ' quotes $(ignored) `ignored`"}
	output, err := exec.Command(launcher, want...).CombinedOutput()
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 23 {
		t.Fatalf("browser exit status lost: %v, %s", err, output)
	}
	if len(output) != 0 {
		t.Fatalf("launcher added output: %s", output)
	}
	raw, err := os.ReadFile(called)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n"); !reflect.DeepEqual(got, want) {
		t.Fatalf("browser arguments = %#v, want %#v", got, want)
	}
}

func TestManagedBrowserLauncherPreservesDebuggingDescriptors(t *testing.T) {
	paths, launcher, _ := managedBrowserLauncherFixture(t)
	t.Setenv("LCR_TEST_BROWSER_FDS", "1")
	input, err := os.CreateTemp(t.TempDir(), "debug-input")
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	output, err := os.CreateTemp(t.TempDir(), "debug-output")
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	const payload = "{\"id\":1,\"method\":\"Browser.getVersion\"}\x00"
	if _, err := input.WriteString(payload); err != nil {
		t.Fatal(err)
	}
	if _, err := input.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(launcher, "--user-data-dir="+paths.ProfileDir, "--remote-debugging-pipe")
	cmd.ExtraFiles = []*os.File{output, input}
	if raw, err := cmd.CombinedOutput(); err == nil {
		t.Fatal("expected browser's exit code 23")
	} else if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 23 {
		t.Fatalf("browser failed: %v, %s", err, raw)
	}
	raw, err := os.ReadFile(output.Name())
	if err != nil || string(raw) != payload {
		t.Fatalf("debugging pipe changed: %q, %v", raw, err)
	}
}

func TestManagedBrowserLaunchReservationProtectsStartupBeforeSingletonExists(t *testing.T) {
	profile := t.TempDir()
	args := []string{"--user-data-dir=" + profile}
	if err := reserveManagedBrowserLaunch(profile, args); err != nil {
		t.Fatal(err)
	}
	if err := reserveManagedBrowserLaunch(profile, args); err == nil {
		t.Fatal("second launch admitted before Chromium acquired its singleton")
	}
}

func TestManagedBrowserLaunchReservationExpiresBeforePIDReuseCanBlockProfile(t *testing.T) {
	profile := t.TempDir()
	host, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(managedBrowserLaunchOwner{Host: host, PID: os.Getpid(), ExpiresAt: time.Now().Add(-time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profile, ".lcr-launch-owner.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := reserveManagedBrowserLaunch(profile, []string{"--user-data-dir=" + profile}); err != nil {
		t.Fatalf("expired reservation with reused live PID blocked launch: %v", err)
	}
}

func TestManagedBrowserLauncherRejectsUncertainOwnershipAndProfileOverrides(t *testing.T) {
	host, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		owner  string
		args   []string
		denied bool
	}{
		{name: "foreign host", owner: "other-" + host + "-123", denied: true},
		{name: "unknown lock", owner: "unknown", denied: true},
		{name: "empty lock", owner: "", denied: true},
		{name: "stale owner", owner: fmt.Sprintf("%s-%d", host, 2147483647)},
		{name: "profile override", args: []string{"--user-data-dir=/other"}, denied: true},
		{name: "missing profile", args: []string{}, denied: true},
		{name: "duplicate profile", args: []string{"--user-data-dir=/other", "--user-data-dir=/other"}, denied: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			profile := t.TempDir()
			if tc.args == nil {
				if err := os.WriteFile(filepath.Join(profile, "SingletonLock"), []byte(tc.owner), 0o600); err != nil {
					t.Fatal(err)
				}
				tc.args = []string{"--user-data-dir=" + profile}
			}
			err := checkManagedBrowserLaunchProfile(profile, tc.args)
			if (err != nil) != tc.denied {
				t.Fatalf("profile check = %v, want denied=%v", err, tc.denied)
			}
		})
	}
}

func TestManagedBrowserLauncherReportsMalformedInvocation(t *testing.T) {
	var stderr bytes.Buffer
	if code := RunManagedBrowserLaunch(nil, &stderr); code != 2 || stderr.Len() == 0 {
		t.Fatalf("malformed invocation: code=%d, stderr=%s", code, &stderr)
	}
}
