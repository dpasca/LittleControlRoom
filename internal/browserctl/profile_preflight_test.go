package browserctl

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestPrepareManagedPlaywrightProfileQuarantinesProfileNewerThanBrowser(t *testing.T) {
	paths, err := ManagedPlaywrightPathsFor(t.TempDir(), "codex", "/tmp/demo", "session-demo", "profile-demo", ManagedLaunchModeBackground)
	if err != nil {
		t.Fatalf("ManagedPlaywrightPathsFor() error = %v", err)
	}
	writeProfileVersionFiles(t, paths.ProfileDir, "149.0.7827.200", "148.0.7000.1", "141.0.7390.37")
	browser := fakeBrowserVersionExecutable(t, "Chromium 141.0.7390.37")

	preflight, err := prepareManagedPlaywrightProfileForLaunch(paths, browser, func() time.Time {
		return time.Date(2026, 7, 8, 15, 9, 21, 0, time.UTC)
	})
	if err != nil {
		t.Fatalf("PrepareManagedPlaywrightProfileForLaunch() error = %v", err)
	}
	wantBackup := paths.ProfileDir + ".crash-backup-20260708-150921"
	if preflight.ProfileBackupPath != wantBackup {
		t.Fatalf("backup path = %q, want %q", preflight.ProfileBackupPath, wantBackup)
	}
	if preflight.ProfileMajor != 149 || preflight.BrowserMajor != 141 {
		t.Fatalf("preflight versions = profile %d browser %d, want 149 > 141", preflight.ProfileMajor, preflight.BrowserMajor)
	}
	if !strings.Contains(preflight.RecoveryReason(), "profile 149.0.7827.200") {
		t.Fatalf("recovery reason = %q, want profile version", preflight.RecoveryReason())
	}
	if _, err := os.Stat(filepath.Join(wantBackup, "Default", "Preferences")); err != nil {
		t.Fatalf("backup missing original Preferences: %v", err)
	}
	if _, err := os.Stat(paths.ProfileDir); err != nil {
		t.Fatalf("fresh profile dir missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(paths.ProfileDir, "Default", "Preferences")); !os.IsNotExist(err) {
		t.Fatalf("fresh profile kept old Preferences, stat err = %v", err)
	}
}

func TestPrepareManagedPlaywrightProfileKeepsCompatibleProfile(t *testing.T) {
	paths, err := ManagedPlaywrightPathsFor(t.TempDir(), "codex", "/tmp/demo", "session-demo", "profile-demo", ManagedLaunchModeBackground)
	if err != nil {
		t.Fatalf("ManagedPlaywrightPathsFor() error = %v", err)
	}
	writeProfileVersionFiles(t, paths.ProfileDir, "141.0.7390.37", "", "")
	browser := fakeBrowserVersionExecutable(t, "Chromium 141.0.7390.37")

	preflight, err := prepareManagedPlaywrightProfileForLaunch(paths, browser, time.Now)
	if err != nil {
		t.Fatalf("PrepareManagedPlaywrightProfileForLaunch() error = %v", err)
	}
	if preflight.ProfileBackupPath != "" {
		t.Fatalf("backup path = %q, want no quarantine", preflight.ProfileBackupPath)
	}
	if _, err := os.Stat(filepath.Join(paths.ProfileDir, "Default", "Preferences")); err != nil {
		t.Fatalf("compatible profile was moved: %v", err)
	}
}

func TestPrepareManagedPlaywrightProfileRecordsVersionProbeErrorWithoutBlockingLaunch(t *testing.T) {
	paths, err := ManagedPlaywrightPathsFor(t.TempDir(), "codex", "/tmp/demo", "session-demo", "profile-demo", ManagedLaunchModeBackground)
	if err != nil {
		t.Fatalf("ManagedPlaywrightPathsFor() error = %v", err)
	}
	writeProfileVersionFiles(t, paths.ProfileDir, "149.0.7827.200", "", "")
	missingBrowser := filepath.Join(t.TempDir(), "missing-browser")

	preflight, err := prepareManagedPlaywrightProfileForLaunch(paths, missingBrowser, time.Now)
	if err != nil {
		t.Fatalf("PrepareManagedPlaywrightProfileForLaunch() error = %v, want launch to continue", err)
	}
	if !strings.Contains(preflight.CompatibilityWarning, "compatibility check skipped") ||
		!strings.Contains(preflight.CompatibilityWarning, missingBrowser) {
		t.Fatalf("compatibility warning = %q, want skipped check and executable path", preflight.CompatibilityWarning)
	}
	if preflight.ProfileBackupPath != "" {
		t.Fatalf("backup path = %q, want profile untouched when browser version is unknown", preflight.ProfileBackupPath)
	}
	if _, err := os.Stat(filepath.Join(paths.ProfileDir, "Default", "Preferences")); err != nil {
		t.Fatalf("profile was moved after version probe error: %v", err)
	}
}

func TestPrepareManagedPlaywrightProfileRecordsUnrecognizedBrowserVersion(t *testing.T) {
	paths, err := ManagedPlaywrightPathsFor(t.TempDir(), "codex", "/tmp/demo", "session-demo", "profile-demo", ManagedLaunchModeBackground)
	if err != nil {
		t.Fatalf("ManagedPlaywrightPathsFor() error = %v", err)
	}
	writeProfileVersionFiles(t, paths.ProfileDir, "149.0.7827.200", "", "")
	browser := fakeBrowserVersionExecutable(t, "Chromium development build")

	preflight, err := prepareManagedPlaywrightProfileForLaunch(paths, browser, time.Now)
	if err != nil {
		t.Fatalf("PrepareManagedPlaywrightProfileForLaunch() error = %v, want launch to continue", err)
	}
	if !strings.Contains(preflight.CompatibilityWarning, `unrecognized version "Chromium development build"`) {
		t.Fatalf("compatibility warning = %q, want unrecognized version", preflight.CompatibilityWarning)
	}
}

func TestManagedBrowserExecutableVersionTimeoutIsBounded(t *testing.T) {
	browser := filepath.Join(t.TempDir(), "slow-browser")
	if err := os.WriteFile(browser, []byte("#!/bin/sh\nexec sleep 5\n"), 0o755); err != nil {
		t.Fatalf("write slow browser: %v", err)
	}
	started := time.Now()
	_, _, _, err := managedBrowserExecutableVersionWithTimeout(browser, 20*time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("managedBrowserExecutableVersionWithTimeout() error = %v, want deadline exceeded", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("version timeout took %s, want bounded wait", elapsed)
	}
}

func TestManagedBrowserExecutableVersionBoundsInheritedOutputPipe(t *testing.T) {
	browser := filepath.Join(t.TempDir(), "browser-with-orphan")
	script := "#!/bin/sh\nsleep 5 &\nprintf 'Chromium 149.0.7827.200\\n'\n"
	if err := os.WriteFile(browser, []byte(script), 0o755); err != nil {
		t.Fatalf("write browser wrapper: %v", err)
	}
	started := time.Now()
	version, major, ok, err := managedBrowserExecutableVersionWithTimeout(browser, 2*time.Second)
	if err != nil || !ok || major != 149 || version != "Chromium 149.0.7827.200" {
		t.Fatalf("managedBrowserExecutableVersionWithTimeout() = %q, %d, %v, %v; want valid bounded version", version, major, ok, err)
	}
	if elapsed := time.Since(started); elapsed > 2500*time.Millisecond {
		t.Fatalf("inherited output pipe wait took %s, want bounded wait", elapsed)
	}
}

func TestApplyManagedPlaywrightProfilePreflightTracksLatestWarning(t *testing.T) {
	state := ManagedPlaywrightState{
		ProfileBackupPath:     "/tmp/previous-backup",
		ProfileRecoveryReason: "previous recovery",
	}
	state = applyManagedPlaywrightProfilePreflight(state, ManagedPlaywrightProfilePreflight{
		CompatibilityWarning: "  compatibility check skipped  ",
	})
	if state.ProfilePreflightWarning != "compatibility check skipped" {
		t.Fatalf("profile preflight warning = %q, want trimmed warning", state.ProfilePreflightWarning)
	}
	if state.ProfileBackupPath != "/tmp/previous-backup" || state.ProfileRecoveryReason != "previous recovery" {
		t.Fatalf("warning update erased recovery record: %#v", state)
	}

	state = applyManagedPlaywrightProfilePreflight(state, ManagedPlaywrightProfilePreflight{})
	if state.ProfilePreflightWarning != "" {
		t.Fatalf("profile preflight warning = %q, want successful check to clear it", state.ProfilePreflightWarning)
	}
	if state.ProfileRecoveryReason != "previous recovery" {
		t.Fatalf("successful check erased recovery reason = %q", state.ProfileRecoveryReason)
	}
}

func TestManagedPlaywrightProfilePreflightWarningPersistsInSessionState(t *testing.T) {
	paths, err := ManagedPlaywrightPathsFor(t.TempDir(), "codex", "/tmp/demo", "session-demo", "profile-demo", ManagedLaunchModeBackground)
	if err != nil {
		t.Fatalf("ManagedPlaywrightPathsFor() error = %v", err)
	}
	state := applyManagedPlaywrightProfilePreflight(ManagedPlaywrightState{
		SessionKey:  paths.SessionKey,
		ProfileKey:  paths.ProfileKey,
		Provider:    paths.Provider,
		ProjectPath: paths.ProjectPath,
		LaunchMode:  paths.LaunchMode,
		Policy:      DefaultPolicy(),
	}, ManagedPlaywrightProfilePreflight{CompatibilityWarning: "version probe timed out"})
	if err := WriteManagedPlaywrightState(paths, state); err != nil {
		t.Fatalf("WriteManagedPlaywrightState() error = %v", err)
	}

	stored, err := ReadManagedPlaywrightState(paths.DataDir, paths.SessionKey)
	if err != nil {
		t.Fatalf("ReadManagedPlaywrightState() error = %v", err)
	}
	if stored.ProfilePreflightWarning != "version probe timed out" {
		t.Fatalf("stored profile preflight warning = %q, want persisted warning", stored.ProfilePreflightWarning)
	}
	raw, err := os.ReadFile(paths.StatePath)
	if err != nil {
		t.Fatalf("read managed state JSON: %v", err)
	}
	if !strings.Contains(string(raw), `"profile_preflight_warning": "version probe timed out"`) {
		t.Fatalf("managed state JSON does not expose preflight warning: %s", raw)
	}
}

func TestManagedPlaywrightProfileVersionUsesMetadataWhenLastVersionWasRewritten(t *testing.T) {
	profileDir := t.TempDir()
	writeProfileVersionFiles(t, profileDir, "149.0.7827.200", "", "141.0.7390.37")

	evidence, ok := managedPlaywrightProfileVersion(profileDir)
	if !ok {
		t.Fatalf("managedPlaywrightProfileVersion() = not ok, want evidence")
	}
	if evidence.Major != 149 {
		t.Fatalf("evidence major = %d from %q, want 149 from Preferences metadata", evidence.Major, evidence.Source)
	}
}

func TestPrepareManagedPlaywrightProfileCleansDeadSingletonFiles(t *testing.T) {
	paths, err := ManagedPlaywrightPathsFor(t.TempDir(), "codex", "/tmp/demo", "session-demo", "profile-demo", ManagedLaunchModeBackground)
	if err != nil {
		t.Fatalf("ManagedPlaywrightPathsFor() error = %v", err)
	}
	host, err := os.Hostname()
	if err != nil {
		t.Fatalf("hostname: %v", err)
	}
	for name, body := range map[string]string{
		"SingletonLock":   host + "-99999999",
		"SingletonSocket": "socket",
		"SingletonCookie": "cookie",
	} {
		if err := os.WriteFile(filepath.Join(paths.ProfileDir, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	preflight, err := prepareManagedPlaywrightProfileForLaunch(paths, "", time.Now)
	if err != nil {
		t.Fatalf("PrepareManagedPlaywrightProfileForLaunch() error = %v", err)
	}
	if got := strings.Join(preflight.RemovedSingletonFiles, ","); got != "SingletonLock,SingletonSocket,SingletonCookie" {
		t.Fatalf("removed singleton files = %q, want all singleton files", got)
	}
	for _, name := range []string{"SingletonLock", "SingletonSocket", "SingletonCookie"} {
		if _, err := os.Stat(filepath.Join(paths.ProfileDir, name)); !os.IsNotExist(err) {
			t.Fatalf("%s still exists, stat err = %v", name, err)
		}
	}
}

func TestPrepareManagedPlaywrightProfileKeepsLiveSingletonLock(t *testing.T) {
	paths, err := ManagedPlaywrightPathsFor(t.TempDir(), "codex", "/tmp/demo", "session-demo", "profile-demo", ManagedLaunchModeBackground)
	if err != nil {
		t.Fatalf("ManagedPlaywrightPathsFor() error = %v", err)
	}
	host, err := os.Hostname()
	if err != nil {
		t.Fatalf("hostname: %v", err)
	}
	if err := os.WriteFile(filepath.Join(paths.ProfileDir, "SingletonLock"), []byte(host+"-"+strconv.Itoa(os.Getpid())), 0o644); err != nil {
		t.Fatalf("write SingletonLock: %v", err)
	}

	preflight, err := prepareManagedPlaywrightProfileForLaunch(paths, "", time.Now)
	if err != nil {
		t.Fatalf("PrepareManagedPlaywrightProfileForLaunch() error = %v", err)
	}
	if len(preflight.RemovedSingletonFiles) != 0 {
		t.Fatalf("removed singleton files = %#v, want none for live lock", preflight.RemovedSingletonFiles)
	}
	if _, err := os.Stat(filepath.Join(paths.ProfileDir, "SingletonLock")); err != nil {
		t.Fatalf("live SingletonLock was removed: %v", err)
	}
}

func writeProfileVersionFiles(t *testing.T, profileDir, createdByVersion, lastChromeVersion, lastVersion string) {
	t.Helper()
	defaultDir := filepath.Join(profileDir, "Default")
	if err := os.MkdirAll(defaultDir, 0o755); err != nil {
		t.Fatalf("mkdir Default: %v", err)
	}
	prefs := `{"profile":{"created_by_version":"` + createdByVersion + `"},"extensions":{"last_chrome_version":"` + lastChromeVersion + `"}}`
	if err := os.WriteFile(filepath.Join(defaultDir, "Preferences"), []byte(prefs), 0o644); err != nil {
		t.Fatalf("write Preferences: %v", err)
	}
	localState := `{"optimization_guide":{"on_device":{"last_version":"` + lastChromeVersion + `"}}}`
	if err := os.WriteFile(filepath.Join(profileDir, "Local State"), []byte(localState), 0o644); err != nil {
		t.Fatalf("write Local State: %v", err)
	}
	if lastVersion != "" {
		if err := os.WriteFile(filepath.Join(profileDir, "Last Version"), []byte(lastVersion), 0o644); err != nil {
			t.Fatalf("write Last Version: %v", err)
		}
	}
}

func fakeBrowserVersionExecutable(t *testing.T, version string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "browser")
	body := "#!/bin/sh\nprintf '%s\\n' " + shellSingleQuote(version) + "\n"
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write fake browser: %v", err)
	}
	return path
}

func shellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
