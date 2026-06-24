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

	tea "github.com/charmbracelet/bubbletea"
)

var externalBrowserOpener = openExternalBrowserURL
var externalPathOpener = openExternalPath
var externalPathRevealer = revealExternalPath
var managedBrowserSessionRevealer = browserctl.RevealManagedPlaywrightSession
var managedBrowserStateReader = browserctl.ReadManagedPlaywrightState

const managedBrowserStateFreshWindow = 30 * time.Second

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
	state, err := managedBrowserSessionRevealer(dataDir, sessionKey)
	if err != nil {
		return browserctl.ManagedPlaywrightState{}, fmt.Errorf("reveal managed browser: %w", err)
	}
	return state, nil
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

func (m *Model) forgetManagedBrowserState(sessionKey string) {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" || len(m.managedBrowserStates) == 0 {
		return
	}
	delete(m.managedBrowserStates, sessionKey)
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

func (m Model) readManagedBrowserStateCmd(sessionKey string) tea.Cmd {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return nil
	}
	dataDir := m.appDataDir()
	return func() tea.Msg {
		state, err := managedBrowserStateReader(dataDir, sessionKey)
		if err == nil && !managedBrowserStateFreshForUI(state, time.Now()) {
			return managedBrowserStateMsg{sessionKey: sessionKey, err: fmt.Errorf("managed browser state is stale")}
		}
		return managedBrowserStateMsg{sessionKey: sessionKey, state: state, err: err}
	}
}

func managedBrowserStateFreshForUI(state browserctl.ManagedPlaywrightState, now time.Time) bool {
	state = state.Normalize()
	if !state.RevealSupported {
		return false
	}
	if state.UpdatedAt.IsZero() {
		return false
	}
	if now.IsZero() {
		now = time.Now()
	}
	age := now.Sub(state.UpdatedAt)
	return age >= 0 && age <= managedBrowserStateFreshWindow
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

func revealExternalPath(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("path is required")
	}
	info, err := os.Stat(path)
	if err == nil && info.IsDir() {
		return externalPathOpener(path)
	}
	if err != nil {
		folder, folderErr := containingFolderForPath(path)
		if folderErr != nil {
			return folderErr
		}
		return externalPathOpener(folder)
	}

	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", "-R", path)
	case "windows":
		cmd = exec.Command("explorer", "/select,"+path)
	default:
		folder, folderErr := containingFolderForPath(path)
		if folderErr != nil {
			return folderErr
		}
		return externalPathOpener(folder)
	}
	return cmd.Run()
}
