package browserctl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	managedPlaywrightBrowserVersionTimeout   = 5 * time.Second
	managedPlaywrightBrowserVersionWaitDelay = 250 * time.Millisecond
)

type ManagedPlaywrightProfilePreflight struct {
	BrowserVersion        string
	BrowserMajor          int
	ProfileVersion        string
	ProfileMajor          int
	ProfileVersionSource  string
	ProfileBackupPath     string
	CompatibilityWarning  string
	RemovedSingletonFiles []string
}

func (p ManagedPlaywrightProfilePreflight) RecoveryReason() string {
	if strings.TrimSpace(p.ProfileBackupPath) == "" {
		return ""
	}
	profileVersion := strings.TrimSpace(p.ProfileVersion)
	if profileVersion == "" && p.ProfileMajor > 0 {
		profileVersion = strconv.Itoa(p.ProfileMajor)
	}
	browserVersion := strings.TrimSpace(p.BrowserVersion)
	if browserVersion == "" && p.BrowserMajor > 0 {
		browserVersion = strconv.Itoa(p.BrowserMajor)
	}
	switch {
	case profileVersion != "" && browserVersion != "":
		return fmt.Sprintf("quarantined incompatible browser profile: profile %s from %s is newer than browser %s", profileVersion, p.ProfileVersionSource, browserVersion)
	case profileVersion != "":
		return fmt.Sprintf("quarantined incompatible browser profile: profile %s from %s is newer than browser major %d", profileVersion, p.ProfileVersionSource, p.BrowserMajor)
	default:
		return "quarantined incompatible browser profile"
	}
}

func PrepareManagedPlaywrightProfileForLaunch(paths ManagedPlaywrightPaths, browserExecutable string) (ManagedPlaywrightProfilePreflight, error) {
	return prepareManagedPlaywrightProfileForLaunch(paths, browserExecutable, time.Now)
}

func prepareManagedPlaywrightProfileForLaunch(paths ManagedPlaywrightPaths, browserExecutable string, now func() time.Time) (ManagedPlaywrightProfilePreflight, error) {
	profileDir := strings.TrimSpace(paths.ProfileDir)
	if profileDir == "" {
		return ManagedPlaywrightProfilePreflight{}, fmt.Errorf("managed Playwright profile dir required")
	}
	removed, err := cleanStaleManagedPlaywrightSingletonFiles(profileDir)
	if err != nil {
		return ManagedPlaywrightProfilePreflight{}, err
	}
	preflight := ManagedPlaywrightProfilePreflight{RemovedSingletonFiles: removed}

	evidence, ok := managedPlaywrightProfileVersion(profileDir)
	if !ok {
		return preflight, nil
	}
	preflight.ProfileVersion = evidence.Version
	preflight.ProfileMajor = evidence.Major
	preflight.ProfileVersionSource = evidence.Source

	browserExecutable = strings.TrimSpace(browserExecutable)
	if browserExecutable == "" {
		return preflight, nil
	}
	browserVersion, browserMajor, ok, err := managedBrowserExecutableVersion(browserExecutable)
	if err != nil {
		preflight.CompatibilityWarning = fmt.Sprintf("browser profile compatibility check skipped: %v", err)
		return preflight, nil
	}
	if !ok {
		preflight.CompatibilityWarning = fmt.Sprintf("browser profile compatibility check skipped: unrecognized version %q from %s", browserVersion, browserExecutable)
		return preflight, nil
	}
	preflight.BrowserVersion = browserVersion
	preflight.BrowserMajor = browserMajor
	if evidence.Major <= browserMajor {
		return preflight, nil
	}
	backupPath, err := quarantineManagedPlaywrightProfile(profileDir, now())
	if err != nil {
		return ManagedPlaywrightProfilePreflight{}, err
	}
	preflight.ProfileBackupPath = backupPath
	return preflight, nil
}

type managedProfileVersionEvidence struct {
	Version string
	Major   int
	Source  string
}

func managedPlaywrightProfileVersion(profileDir string) (managedProfileVersionEvidence, bool) {
	var best managedProfileVersionEvidence
	add := func(version, source string) {
		major, ok := chromiumMajorVersion(version)
		if !ok {
			return
		}
		if major > best.Major {
			best = managedProfileVersionEvidence{Version: strings.TrimSpace(version), Major: major, Source: source}
		}
	}

	if prefs, ok := readJSONObject(filepath.Join(profileDir, "Default", "Preferences")); ok {
		add(jsonStringAt(prefs, "profile", "created_by_version"), "Default/Preferences profile.created_by_version")
		add(jsonStringAt(prefs, "extensions", "last_chrome_version"), "Default/Preferences extensions.last_chrome_version")
	}
	if localState, ok := readJSONObject(filepath.Join(profileDir, "Local State")); ok {
		add(jsonStringAt(localState, "optimization_guide", "on_device", "last_version"), "Local State optimization_guide.on_device.last_version")
	}
	if raw, err := os.ReadFile(filepath.Join(profileDir, "Last Version")); err == nil {
		add(string(raw), "Last Version")
	}
	return best, best.Major > 0
}

func readJSONObject(path string) (map[string]any, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, false
	}
	return out, true
}

func jsonStringAt(root map[string]any, path ...string) string {
	var current any = root
	for _, key := range path {
		obj, ok := current.(map[string]any)
		if !ok {
			return ""
		}
		current = obj[key]
	}
	value, _ := current.(string)
	return value
}

func managedBrowserExecutableVersion(executable string) (string, int, bool, error) {
	return managedBrowserExecutableVersionWithTimeout(executable, managedPlaywrightBrowserVersionTimeout)
}

func managedBrowserExecutableVersionWithTimeout(executable string, timeout time.Duration) (string, int, bool, error) {
	executable = strings.TrimSpace(executable)
	if executable == "" {
		return "", 0, false, nil
	}
	if timeout <= 0 {
		return "", 0, false, fmt.Errorf("browser version probe timeout must be positive")
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, "--version")
	// Isolate wrapper descendants so a timed-out version probe can clean up the
	// whole probe tree instead of leaving orphaned helpers behind.
	configureIsolatedProcessGroup(cmd)
	// A browser wrapper may leave a descendant holding the stdout pipe after the
	// wrapper is killed. Bound that wait as well as the process itself.
	cmd.WaitDelay = managedPlaywrightBrowserVersionWaitDelay
	raw, err := cmd.Output()
	version := strings.TrimSpace(string(raw))
	if err != nil {
		bestEffortKillProcessGroup(cmd)
		// WaitDelay may close a pipe inherited by an orphaned wrapper child after
		// the version process itself exited successfully. Its valid output is
		// still authoritative.
		if errors.Is(err, exec.ErrWaitDelay) {
			if major, ok := chromiumMajorVersion(version); ok {
				return version, major, true, nil
			}
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", 0, false, fmt.Errorf("read browser version from %s within %s: %w", executable, timeout, ctxErr)
		}
		return "", 0, false, fmt.Errorf("read browser version from %s: %w", executable, err)
	}
	major, ok := chromiumMajorVersion(version)
	return version, major, ok, nil
}

func applyManagedPlaywrightProfilePreflight(state ManagedPlaywrightState, preflight ManagedPlaywrightProfilePreflight) ManagedPlaywrightState {
	if recoveryReason := preflight.RecoveryReason(); preflight.ProfileBackupPath != "" || recoveryReason != "" {
		state.ProfileBackupPath = preflight.ProfileBackupPath
		state.ProfileRecoveryReason = recoveryReason
	}
	// Unlike a recovery record, a warning describes only the latest launch
	// attempt. Clear it after a subsequent successful compatibility check.
	state.ProfilePreflightWarning = strings.TrimSpace(preflight.CompatibilityWarning)
	return state
}

func chromiumMajorVersion(version string) (int, bool) {
	version = strings.TrimSpace(version)
	if version == "" {
		return 0, false
	}
	for _, field := range strings.Fields(version) {
		field = strings.TrimSpace(field)
		digits := strings.Builder{}
		for _, r := range field {
			if r < '0' || r > '9' {
				break
			}
			digits.WriteRune(r)
		}
		if digits.Len() == 0 {
			continue
		}
		major, err := strconv.Atoi(digits.String())
		if err == nil && major > 0 {
			return major, true
		}
	}
	return 0, false
}

func quarantineManagedPlaywrightProfile(profileDir string, now time.Time) (string, error) {
	profileDir = strings.TrimSpace(profileDir)
	if profileDir == "" {
		return "", fmt.Errorf("managed Playwright profile dir required")
	}
	if now.IsZero() {
		now = time.Now()
	}
	stamp := now.Format("20060102-150405")
	base := profileDir + ".crash-backup-" + stamp
	backupPath := base
	for i := 2; fileOrDirExists(backupPath); i++ {
		backupPath = fmt.Sprintf("%s-%d", base, i)
	}
	if err := os.Rename(profileDir, backupPath); err != nil {
		return "", fmt.Errorf("quarantine incompatible managed Playwright profile: %w", err)
	}
	if err := os.MkdirAll(profileDir, 0o755); err != nil {
		return "", fmt.Errorf("recreate managed Playwright profile after quarantine: %w", err)
	}
	return backupPath, nil
}

func fileOrDirExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func cleanStaleManagedPlaywrightSingletonFiles(profileDir string) ([]string, error) {
	lockPath := filepath.Join(profileDir, "SingletonLock")
	target, ok := singletonLockTarget(lockPath)
	if !ok {
		return nil, nil
	}
	host, pid, ok := parseSingletonLockTarget(target)
	if !ok {
		return nil, nil
	}
	localHost, err := os.Hostname()
	if err != nil || strings.TrimSpace(localHost) == "" {
		return nil, nil
	}
	if strings.TrimSpace(host) != strings.TrimSpace(localHost) || processIsAlive(pid) {
		return nil, nil
	}

	var removed []string
	for _, name := range []string{"SingletonLock", "SingletonSocket", "SingletonCookie"} {
		path := filepath.Join(profileDir, name)
		if err := os.Remove(path); err == nil {
			removed = append(removed, name)
		} else if !errors.Is(err, os.ErrNotExist) {
			return removed, err
		}
	}
	return removed, nil
}

func singletonLockTarget(lockPath string) (string, bool) {
	target, err := os.Readlink(lockPath)
	if err == nil {
		return strings.TrimSpace(target), strings.TrimSpace(target) != ""
	}
	raw, err := os.ReadFile(lockPath)
	if err != nil {
		return "", false
	}
	target = strings.TrimSpace(string(raw))
	return target, target != ""
}

func parseSingletonLockTarget(target string) (string, int, bool) {
	target = strings.TrimSpace(target)
	split := strings.LastIndex(target, "-")
	if split <= 0 || split >= len(target)-1 {
		return "", 0, false
	}
	pid, err := strconv.Atoi(target[split+1:])
	if err != nil || pid <= 0 {
		return "", 0, false
	}
	return target[:split], pid, true
}

// DetectManagedBrowserProcessFromProfileLock uses Chromium's profile ownership
// lock as a cheap launch signal. It deliberately avoids a process-table scan so
// callers can poll it frequently while waiting for a lazy browser launch.
func DetectManagedBrowserProcessFromProfileLock(profileDir, browserExecutable string) (ManagedBrowserProcess, bool) {
	target, ok := singletonLockTarget(filepath.Join(strings.TrimSpace(profileDir), "SingletonLock"))
	if !ok {
		return ManagedBrowserProcess{}, false
	}
	host, pid, ok := parseSingletonLockTarget(target)
	if !ok || !processIsAlive(pid) {
		return ManagedBrowserProcess{}, false
	}
	localHost, err := os.Hostname()
	if err != nil || strings.TrimSpace(localHost) == "" || strings.TrimSpace(host) != strings.TrimSpace(localHost) {
		return ManagedBrowserProcess{}, false
	}

	executable := strings.TrimSpace(browserExecutable)
	appPath := extractMacAppPath(executable)
	processName := ""
	if executable != "" {
		processName = filepath.Base(executable)
	}
	return ManagedBrowserProcess{
		PID:            pid,
		Command:        executable,
		Args:           executable,
		AppPath:        appPath,
		AppName:        macAppName(appPath, processName),
		ExecutablePath: executable,
	}, true
}

func processIsAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
