package browserctl

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

const playwrightBrowserExecutableEnv = "LCR_PLAYWRIGHT_BROWSER_EXECUTABLE"

type browserExecutableCandidate struct {
	Path     string
	BundleID string
}

func ManagedBrowserExecutablePathForLaunchMode(launchMode ManagedLaunchMode) string {
	if path := strings.TrimSpace(os.Getenv(playwrightBrowserExecutableEnv)); path != "" {
		return path
	}
	if launchMode.Normalize() == ManagedLaunchModeHeadless {
		return ""
	}
	if path := installedPlaywrightChromiumExecutable(); path != "" {
		return path
	}
	for _, path := range defaultInteractiveBrowserExecutables() {
		if fileExists(path) {
			return path
		}
	}
	return ""
}

func managedBrowserExecutablePathForConfig(cfg BrowserSessionConfig, launchMode ManagedLaunchMode) string {
	if path := strings.TrimSpace(os.Getenv(playwrightBrowserExecutableEnv)); path != "" {
		return path
	}
	if path := strings.TrimSpace(cfg.BrowserPath); path != "" {
		return path
	}
	if launchMode.Normalize() == ManagedLaunchModeHeadless {
		return ""
	}
	if path := installedPlaywrightChromiumExecutable(); path != "" {
		return path
	}
	for _, path := range defaultInteractiveBrowserExecutables() {
		if fileExists(path) {
			return path
		}
	}
	return ""
}

func installedPlaywrightChromiumExecutable() string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return ""
	}
	for _, path := range playwrightChromiumExecutableCandidates(home, runtime.GOOS) {
		if fileExists(path) {
			return path
		}
	}
	return ""
}

func playwrightChromiumExecutableCandidates(home, goos string) []string {
	home = strings.TrimSpace(home)
	if home == "" {
		return nil
	}
	var pattern string
	switch goos {
	case "darwin":
		pattern = filepath.Join(home, "Library", "Caches", "ms-playwright", "chromium-*", "chrome-mac", "Chromium.app", "Contents", "MacOS", "Chromium")
	case "linux":
		pattern = filepath.Join(home, ".cache", "ms-playwright", "chromium-*", "chrome-linux", "chrome")
	case "windows":
		pattern = filepath.Join(home, "AppData", "Local", "ms-playwright", "chromium-*", "chrome-win", "chrome.exe")
	default:
		return nil
	}
	matches, err := filepath.Glob(pattern)
	if err != nil || len(matches) == 0 {
		return nil
	}
	sort.Slice(matches, func(i, j int) bool {
		left := playwrightChromiumRevision(matches[i])
		right := playwrightChromiumRevision(matches[j])
		if left != right {
			return left > right
		}
		return matches[i] > matches[j]
	})
	return matches
}

func playwrightChromiumRevision(path string) int {
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if !strings.HasPrefix(part, "chromium-") {
			continue
		}
		revision, err := strconv.Atoi(strings.TrimPrefix(part, "chromium-"))
		if err == nil {
			return revision
		}
	}
	return 0
}

func defaultInteractiveBrowserExecutables() []string {
	candidates := defaultInteractiveBrowserCandidates(runtime.GOOS)
	if runtime.GOOS == "darwin" {
		candidates = prioritizeNonDefaultBrowser(candidates, detectMacDefaultBrowserBundleID())
	}
	paths := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		paths = append(paths, candidate.Path)
	}
	return paths
}

func defaultInteractiveBrowserCandidates(goos string) []browserExecutableCandidate {
	if goos == "darwin" {
		return []browserExecutableCandidate{
			{Path: "/Applications/Chromium.app/Contents/MacOS/Chromium", BundleID: "org.chromium.Chromium"},
			{Path: "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome", BundleID: "com.google.Chrome"},
			{Path: "/Applications/Brave Browser.app/Contents/MacOS/Brave Browser", BundleID: "com.brave.browser"},
		}
	}
	return nil
}

func prioritizeNonDefaultBrowser(candidates []browserExecutableCandidate, defaultBundleID string) []browserExecutableCandidate {
	defaultBundleID = strings.TrimSpace(strings.ToLower(defaultBundleID))
	if defaultBundleID == "" || len(candidates) < 2 {
		return candidates
	}
	var nonDefault []browserExecutableCandidate
	var defaults []browserExecutableCandidate
	for _, candidate := range candidates {
		if strings.ToLower(strings.TrimSpace(candidate.BundleID)) == defaultBundleID {
			defaults = append(defaults, candidate)
		} else {
			nonDefault = append(nonDefault, candidate)
		}
	}
	return append(nonDefault, defaults...)
}

var detectMacDefaultBrowserBundleID = func() string {
	output, err := exec.Command("defaults", "read", "com.apple.LaunchServices/com.apple.launchservices.secure", "LSHandlers").Output()
	if err != nil {
		return ""
	}
	return parseMacDefaultBrowserBundleID(string(output))
}

func parseMacDefaultBrowserBundleID(output string) string {
	blocks := strings.Split(output, "},")
	for _, scheme := range []string{"https", "http"} {
		for _, block := range blocks {
			if !strings.Contains(block, "LSHandlerURLScheme = "+scheme+";") {
				continue
			}
			for _, line := range strings.Split(block, "\n") {
				line = strings.TrimSpace(line)
				if !strings.HasPrefix(line, "LSHandlerRoleAll = ") {
					continue
				}
				value := strings.TrimSpace(strings.TrimPrefix(line, "LSHandlerRoleAll = "))
				value = strings.Trim(value, `";`)
				if value != "" {
					return value
				}
			}
		}
	}
	return ""
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
