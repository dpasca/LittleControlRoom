package tui

import (
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"lcroom/internal/browserctl"
)

var externalBrowserOpener = openExternalBrowserURL
var externalPathOpener = openExternalPath
var managedBrowserStateReader = browserctl.ReadManagedPlaywrightState
var managedBrowserRevealer = browserctl.RevealManagedPlaywrightState
var managedBrowserRevealMarker = browserctl.MarkManagedPlaywrightStateRevealed

func openProjectDirInBrowser(path string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("project path is required")
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve project path: %w", err)
	}
	info, err := os.Stat(absPath)
	if err != nil {
		return fmt.Errorf("inspect project path: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("project path is not a directory")
	}

	return openBrowserURL(directoryFileURL(absPath), "open project in browser")
}

func directoryFileURL(path string) string {
	cleanPath := filepath.ToSlash(filepath.Clean(path))
	if !strings.HasPrefix(cleanPath, "/") {
		cleanPath = "/" + cleanPath
	}
	if !strings.HasSuffix(cleanPath, "/") {
		cleanPath += "/"
	}
	return (&url.URL{Scheme: "file", Path: cleanPath}).String()
}

func openRuntimeURLInBrowser(rawURL string) error {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return fmt.Errorf("runtime URL is required")
	}
	return openBrowserURL(rawURL, "open runtime URL in browser")
}

func openBrowserURL(rawURL, action string) error {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return fmt.Errorf("browser URL is required")
	}
	if err := externalBrowserOpener(rawURL); err != nil {
		return fmt.Errorf("%s: %w", action, err)
	}
	return nil
}

func revealManagedBrowserSession(dataDir, sessionKey string) (browserctl.ManagedPlaywrightState, error) {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return browserctl.ManagedPlaywrightState{}, fmt.Errorf("managed browser session key is required")
	}
	state, err := managedBrowserStateReader(dataDir, sessionKey)
	if err != nil {
		return browserctl.ManagedPlaywrightState{}, fmt.Errorf("read managed browser state: %w", err)
	}
	if err := managedBrowserRevealer(state); err != nil {
		return browserctl.ManagedPlaywrightState{}, fmt.Errorf("reveal managed browser: %w", err)
	}
	if updated, err := managedBrowserRevealMarker(dataDir, sessionKey); err == nil {
		return updated, nil
	}
	state.Hidden = false
	state.UpdatedAt = time.Now().UTC()
	return state.Normalize(), nil
}

func (m *Model) rememberManagedBrowserState(state browserctl.ManagedPlaywrightState) {
	sessionKey := strings.TrimSpace(state.SessionKey)
	if sessionKey == "" {
		return
	}
	if m.managedBrowserStates == nil {
		m.managedBrowserStates = make(map[string]browserctl.ManagedPlaywrightState)
	}
	m.managedBrowserStates[sessionKey] = state.Normalize()
}

func (m Model) cachedManagedBrowserState(sessionKey string) (browserctl.ManagedPlaywrightState, bool) {
	if len(m.managedBrowserStates) == 0 {
		return browserctl.ManagedPlaywrightState{}, false
	}
	state, ok := m.managedBrowserStates[strings.TrimSpace(sessionKey)]
	if !ok {
		return browserctl.ManagedPlaywrightState{}, false
	}
	return state.Normalize(), true
}

func openExternalBrowserURL(rawURL string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", rawURL)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", "", rawURL)
	default:
		cmd = exec.Command("xdg-open", rawURL)
	}
	return cmd.Run()
}

func openExternalPath(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("path is required")
	}
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("inspect path: %w", err)
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", path)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", "", path)
	default:
		cmd = exec.Command("xdg-open", path)
	}
	return cmd.Run()
}
