package browserctl

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const ManagedBrowserLaunchCommand = "playwright-browser-launch"

// PrepareManagedBrowserExecutable interposes at each Chromium launch, including
// launches retried inside Playwright MCP. A startup-only profile check cannot
// protect a browser that outlives its original automation connection.
func PrepareManagedBrowserExecutable(paths ManagedPlaywrightPaths, executable string) (string, error) {
	if strings.TrimSpace(executable) == "" {
		return "", nil
	}
	helper, err := os.Executable()
	if err != nil {
		return "", err
	}
	return writeManagedBrowserLauncher(paths, executable, helper)
}

func writeManagedBrowserLauncher(paths ManagedPlaywrightPaths, executable, helper string) (string, error) {
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }
	script := "#!/bin/sh\nexec " + quote(helper) + " " + ManagedBrowserLaunchCommand + " " +
		quote(executable) + " " + quote(paths.ProfileDir) + " -- \"$@\"\n"
	path := filepath.Join(paths.SessionDir, "browser-launcher.sh")
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		return "", fmt.Errorf("write managed browser launcher: %w", err)
	}
	return path, nil
}

// RunManagedBrowserLaunch preserves Chromium's PID, descriptors, signals, and
// argument boundaries by replacing this helper rather than spawning a child.
func RunManagedBrowserLaunch(args []string, stderr io.Writer) int {
	if len(args) < 3 || args[2] != "--" || args[0] == "" || args[1] == "" {
		fmt.Fprintln(stderr, "managed browser launcher requires an executable, profile directory, and --")
		return 2
	}
	if err := reserveManagedBrowserLaunch(args[1], args[3:]); err != nil {
		// This marker deliberately differs from Chromium's singleton error.
		// Playwright MCP retries that error with up to five browser launches,
		// each of which can open a tab and activate the live owner.
		fmt.Fprintf(stderr, "LCR_BROWSER_LAUNCH_BLOCKED: %v. Existing browser preserved; stop this browser batch. Close the existing browser explicitly before retrying if its automation connection was lost.\n", err)
		return 1
	}
	if err := syscall.Exec(args[0], append([]string{args[0]}, args[3:]...), os.Environ()); err != nil {
		fmt.Fprintf(stderr, "managed browser launch: %v\n", err)
		return 1
	}
	return 0
}

type managedBrowserLaunchOwner struct {
	Host      string    `json:"host"`
	PID       int       `json:"pid"`
	ExpiresAt time.Time `json:"expires_at"`
}

func reserveManagedBrowserLaunch(profileDir string, args []string) error {
	// Reserve the exec PID before Chromium creates SingletonLock. This closes
	// the gap where two simultaneous first launches would both see no owner.
	lock, err := lockManagedPlaywrightState(filepath.Join(profileDir, ".lcr-launch.lock"))
	if err != nil {
		return err
	}
	defer unlockManagedPlaywrightState(lock)
	if err := checkManagedBrowserLaunchProfile(profileDir, args); err != nil {
		return err
	}
	host, err := os.Hostname()
	if err != nil {
		return err
	}
	owner, err := json.Marshal(managedBrowserLaunchOwner{Host: host, PID: os.Getpid(), ExpiresAt: time.Now().Add(time.Minute)})
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(profileDir, ".lcr-launch-owner.json"), owner, 0o600)
}

func checkManagedBrowserLaunchProfile(profileDir string, args []string) error {
	// Check the actual Chromium profile argument as well as the launcher's
	// pinned profile, so a later configuration change cannot bypass the guard.
	profiles := 0
	for i := 0; i < len(args); i++ {
		value, found := strings.CutPrefix(args[i], "--user-data-dir=")
		if args[i] == "--user-data-dir" && i+1 < len(args) {
			i++
			value, found = args[i], true
		}
		if found {
			profiles++
			if filepath.Clean(value) != filepath.Clean(profileDir) {
				return fmt.Errorf("browser profile does not match the managed profile")
			}
		}
	}
	if profiles != 1 {
		return fmt.Errorf("browser launch requires exactly one managed profile argument")
	}
	return checkManagedBrowserProfileOwner(profileDir)
}

func checkManagedBrowserProfileOwner(profileDir string) error {
	localHost, err := os.Hostname()
	if err != nil {
		return fmt.Errorf("cannot verify browser profile host: %w", err)
	}
	raw, err := os.ReadFile(filepath.Join(profileDir, ".lcr-launch-owner.json"))
	if err == nil {
		var owner managedBrowserLaunchOwner
		if json.Unmarshal(raw, &owner) != nil || owner.PID <= 0 || owner.ExpiresAt.IsZero() {
			return fmt.Errorf("cannot verify the managed browser launch owner")
		}
		// This lease only covers startup. Chromium's own singleton protects
		// the running browser thereafter. Expiry prevents a failed launch's
		// old PID from blocking the profile after the OS reuses that PID.
		if time.Now().Before(owner.ExpiresAt) {
			if owner.Host != localHost {
				return fmt.Errorf("cannot verify the managed browser launch host")
			}
			if processIsAlive(owner.PID) {
				return fmt.Errorf("browser profile is already held by live process %d", owner.PID)
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("cannot inspect managed browser launch ownership: %w", err)
	}
	lockPath := filepath.Join(profileDir, "SingletonLock")
	target, err := os.Readlink(lockPath)
	if err != nil {
		targetBytes, readErr := os.ReadFile(lockPath)
		if errors.Is(readErr, os.ErrNotExist) {
			return nil
		}
		if readErr != nil {
			return fmt.Errorf("cannot inspect browser profile ownership: %w", readErr)
		}
		target = string(targetBytes)
	}
	host, pid, ok := parseSingletonLockTarget(target)
	if !ok {
		return fmt.Errorf("browser profile has an unrecognized ownership lock")
	}
	if host != localHost {
		return fmt.Errorf("browser profile is owned by another host or ownership cannot be verified")
	}
	if processIsAlive(pid) {
		return fmt.Errorf("browser profile is already held by live process %d", pid)
	}
	return nil
}
