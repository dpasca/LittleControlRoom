package browserctl

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestPlaywrightChromiumExecutableCandidatesPreferNewestRevision(t *testing.T) {
	home := t.TempDir()
	older := fakePlaywrightChromiumExecutable(t, home, "darwin", "1194")
	newer := fakePlaywrightChromiumExecutable(t, home, "darwin", "1201")

	got := playwrightChromiumExecutableCandidates(home, "darwin")
	if len(got) < 2 {
		t.Fatalf("candidates = %#v, want at least two", got)
	}
	if got[0] != newer || got[1] != older {
		t.Fatalf("candidates = %#v, want newest first then older", got)
	}
}

func TestManagedBrowserExecutablePathForLaunchModePrefersPlaywrightChromium(t *testing.T) {
	home := t.TempDir()
	want := fakePlaywrightChromiumExecutable(t, home, runtime.GOOS, "1201")
	if want == "" {
		t.Skipf("no Playwright Chromium candidate path for %s", runtime.GOOS)
	}
	t.Setenv("HOME", home)
	t.Setenv(playwrightBrowserExecutableEnv, "")

	got := ManagedBrowserExecutablePathForLaunchMode(ManagedLaunchModeBackground)
	if got != want {
		t.Fatalf("ManagedBrowserExecutablePathForLaunchMode() = %q, want %q", got, want)
	}
}

func TestManagedBrowserExecutablePathForLaunchModeHonorsEnvOverride(t *testing.T) {
	t.Setenv(playwrightBrowserExecutableEnv, "/tmp/custom-browser")

	got := ManagedBrowserExecutablePathForLaunchMode(ManagedLaunchModeHeadless)
	if got != "/tmp/custom-browser" {
		t.Fatalf("ManagedBrowserExecutablePathForLaunchMode() = %q, want env override", got)
	}
}

func TestPrioritizeNonDefaultBrowserMovesDefaultToEnd(t *testing.T) {
	candidates := []browserExecutableCandidate{
		{Path: "chromium", BundleID: "org.chromium.Chromium"},
		{Path: "chrome", BundleID: "com.google.Chrome"},
		{Path: "brave", BundleID: "com.brave.browser"},
	}

	got := prioritizeNonDefaultBrowser(candidates, "com.google.Chrome")
	if got[0].Path != "chromium" || got[1].Path != "brave" || got[2].Path != "chrome" {
		t.Fatalf("prioritized candidates = %#v, want default Chrome moved to end", got)
	}
}

func TestParseMacDefaultBrowserBundleIDPrefersHTTPS(t *testing.T) {
	output := `
(
    {
        LSHandlerRoleAll = "com.example.http";
        LSHandlerURLScheme = http;
    },
    {
        LSHandlerRoleAll = "com.brave.browser";
        LSHandlerURLScheme = https;
    }
)
`
	got := parseMacDefaultBrowserBundleID(output)
	if got != "com.brave.browser" {
		t.Fatalf("parseMacDefaultBrowserBundleID() = %q, want Brave bundle id", got)
	}
}

func fakePlaywrightChromiumExecutable(t *testing.T, home, goos, revision string) string {
	t.Helper()
	var path string
	switch goos {
	case "darwin":
		path = filepath.Join(home, "Library", "Caches", "ms-playwright", "chromium-"+revision, "chrome-mac", "Chromium.app", "Contents", "MacOS", "Chromium")
	case "linux":
		path = filepath.Join(home, ".cache", "ms-playwright", "chromium-"+revision, "chrome-linux", "chrome")
	case "windows":
		path = filepath.Join(home, "AppData", "Local", "ms-playwright", "chromium-"+revision, "chrome-win", "chrome.exe")
	default:
		return ""
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}
