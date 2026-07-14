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

const managedBrowserStateFreshWindow = 5 * time.Second
const managedBrowserStateRefreshInterval = 2 * time.Second
const managedBrowserStateHydrationRetryAttempts = 20

var managedBrowserStateHydrationRetryDelay = 250 * time.Millisecond

type managedBrowserAvailability string

const (
	managedBrowserAvailabilityChecking managedBrowserAvailability = "checking"
	managedBrowserAvailabilityLive     managedBrowserAvailability = "live"
	managedBrowserAvailabilityGone     managedBrowserAvailability = "gone"
)

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

func probeAndRevealManagedBrowserSession(dataDir, sessionKey string) (browserctl.ManagedPlaywrightState, bool, error) {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return browserctl.ManagedPlaywrightState{}, false, fmt.Errorf("managed browser session key is required")
	}
	probe := readManagedBrowserStateMsg(dataDir, sessionKey, 0)
	if probe.err != nil {
		return browserctl.ManagedPlaywrightState{}, false, fmt.Errorf("managed browser is no longer attached; use /reconnect: %w", probe.err)
	}
	state, err := revealManagedBrowserSession(dataDir, sessionKey)
	if err != nil {
		return probe.state.Normalize(), true, err
	}
	return state.Normalize(), true, nil
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
	if m.managedBrowserStateFetchedAt == nil {
		m.managedBrowserStateFetchedAt = make(map[string]time.Time)
	}
	if m.managedBrowserAvailability == nil {
		m.managedBrowserAvailability = make(map[string]managedBrowserAvailability)
	}
	m.managedBrowserStateFetchedAt[sessionKey] = m.currentTime()
	m.managedBrowserAvailability[sessionKey] = managedBrowserAvailabilityLive
}

func (m *Model) markManagedBrowserStateGone(sessionKey string) {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return
	}
	if len(m.managedBrowserStates) > 0 {
		delete(m.managedBrowserStates, sessionKey)
	}
	if m.managedBrowserStateFetchedAt == nil {
		m.managedBrowserStateFetchedAt = make(map[string]time.Time)
	}
	if m.managedBrowserAvailability == nil {
		m.managedBrowserAvailability = make(map[string]managedBrowserAvailability)
	}
	m.managedBrowserStateFetchedAt[sessionKey] = m.currentTime()
	m.managedBrowserAvailability[sessionKey] = managedBrowserAvailabilityGone
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

func (m Model) managedBrowserStateRecentlyFetched(sessionKey string, now time.Time) bool {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return false
	}
	fetchedAt := m.managedBrowserStateFetchedAt[sessionKey]
	if fetchedAt.IsZero() {
		if state, ok := m.cachedManagedBrowserState(sessionKey); ok {
			// Compatibility for models and tests constructed before fetched-at was
			// tracked separately. Production reads always populate fetchedAt.
			fetchedAt = state.UpdatedAt
		}
	}
	if fetchedAt.IsZero() {
		return false
	}
	if now.IsZero() {
		now = m.currentTime()
	}
	age := now.Sub(fetchedAt)
	return age >= 0 && age <= managedBrowserStateRefreshInterval
}

func (m *Model) beginManagedBrowserStateRead(sessionKey string) bool {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return false
	}
	if m.managedBrowserReadsInFlight == nil {
		m.managedBrowserReadsInFlight = make(map[string]bool)
	}
	if m.managedBrowserReadsInFlight[sessionKey] {
		return false
	}
	m.managedBrowserReadsInFlight[sessionKey] = true
	m.markManagedBrowserStateChecking(sessionKey)
	return true
}

func (m *Model) markManagedBrowserStateChecking(sessionKey string) {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return
	}
	if m.managedBrowserAvailability == nil {
		m.managedBrowserAvailability = make(map[string]managedBrowserAvailability)
	}
	m.managedBrowserAvailability[sessionKey] = managedBrowserAvailabilityChecking
}

func (m *Model) finishManagedBrowserStateRead(sessionKey string) {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" || len(m.managedBrowserReadsInFlight) == 0 {
		return
	}
	delete(m.managedBrowserReadsInFlight, sessionKey)
}

func (m Model) readManagedBrowserStateCmd(sessionKey string, retryAttemptsRemaining int) tea.Cmd {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return nil
	}
	dataDir := m.appDataDir()
	return func() tea.Msg {
		return readManagedBrowserStateMsg(dataDir, sessionKey, retryAttemptsRemaining)
	}
}

func (m Model) delayedReadManagedBrowserStateCmd(sessionKey string, retryAttemptsRemaining int) tea.Cmd {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return nil
	}
	dataDir := m.appDataDir()
	delay := managedBrowserStateHydrationRetryDelay
	return func() tea.Msg {
		if delay > 0 {
			time.Sleep(delay)
		}
		return readManagedBrowserStateMsg(dataDir, sessionKey, retryAttemptsRemaining)
	}
}

func readManagedBrowserStateMsg(dataDir, sessionKey string, retryAttemptsRemaining int) managedBrowserStateMsg {
	state, err := managedBrowserStateReader(dataDir, sessionKey)
	retryable := err != nil
	if err == nil && strings.TrimSpace(state.SessionKey) != strings.TrimSpace(sessionKey) {
		err = fmt.Errorf("managed browser state belongs to a different session")
		retryable = false
	}
	if err == nil {
		now := time.Now()
		switch {
		case !managedBrowserStateHeartbeatFresh(state, now):
			err = fmt.Errorf("managed browser is no longer attached")
			retryable = false
		case !state.Normalize().RevealSupported:
			err = fmt.Errorf("managed browser window is not available yet")
			retryable = true
		}
	}
	return managedBrowserStateMsg{
		sessionKey:             sessionKey,
		state:                  state,
		err:                    err,
		retryable:              retryable,
		retryAttemptsRemaining: retryAttemptsRemaining,
	}
}

func managedBrowserStateFreshForUI(state browserctl.ManagedPlaywrightState, now time.Time) bool {
	state = state.Normalize()
	if !state.RevealSupported {
		return false
	}
	return managedBrowserStateHeartbeatFresh(state, now)
}

func managedBrowserStateHeartbeatFresh(state browserctl.ManagedPlaywrightState, now time.Time) bool {
	state = state.Normalize()
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
