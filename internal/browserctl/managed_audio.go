package browserctl

import (
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

//go:embed managed_audio_extension.js
var managedAudioExtensionSource string

// ManagedBrowserAudio owns a session-private audio bridge. Its lifetime must
// follow the browser owner, not an individual tool call or human handoff.
type ManagedBrowserAudio struct {
	LaunchOptions map[string]any
	ConfigPath    string
	server        *http.Server
	cancel        context.CancelFunc
}

func (a *ManagedBrowserAudio) Close() {
	if a != nil && a.cancel != nil {
		a.cancel()
		_ = a.server.Close()
	}
}

// PrepareManagedBrowserAudio leaves headed/headless Playwright defaults alone.
// Background Chromium loads native tab muting; unsupported executables stay
// muted even when revealed rather than silently playing hidden audio.
func PrepareManagedBrowserAudio(paths ManagedPlaywrightPaths, executable string) (*ManagedBrowserAudio, error) {
	a := &ManagedBrowserAudio{LaunchOptions: map[string]any{}}
	if paths.LaunchMode.Normalize() != ManagedLaunchModeBackground {
		return a, nil
	}
	mode := "muted"
	a.LaunchOptions["args"] = []string{"--mute-audio"}
	if managedAudioExtensionSupported(executable) {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nil, fmt.Errorf("start managed browser audio bridge: %w", err)
		}
		var token [32]byte
		if _, err := rand.Read(token[:]); err != nil {
			_ = listener.Close()
			return nil, err
		}
		endpoint := "/" + hex.EncodeToString(token[:])
		ctx, cancel := context.WithCancel(context.Background())
		a.cancel = cancel
		mux := http.NewServeMux()
		mux.HandleFunc(endpoint, managedAudioHandler(ctx, paths))
		a.server = &http.Server{Handler: mux, ReadHeaderTimeout: 3 * time.Second}
		go func() { _ = a.server.Serve(listener) }()
		dir := filepath.Join(paths.SessionDir, "audio-extension")
		manifest := map[string]any{
			"manifest_version": 3, "name": "Little Control Room Audio", "version": "1.0.0",
			"minimum_chrome_version": "116",
			"permissions":            []string{"storage", "alarms"},
			"background":             map[string]any{"service_worker": "background.js"},
		}
		url, _ := json.Marshal("ws://" + listener.Addr().String() + endpoint)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			a.Close()
			return nil, err
		}
		if err := writeManagedAudioJSON(filepath.Join(dir, "manifest.json"), manifest); err != nil {
			a.Close()
			return nil, err
		}
		if err := os.WriteFile(filepath.Join(dir, "background.js"), []byte("const audioEndpoint = "+string(url)+";\n"+managedAudioExtensionSource), 0o600); err != nil {
			a.Close()
			return nil, err
		}
		mode = "visibility"
		a.LaunchOptions = map[string]any{
			"args":              []string{"--disable-extensions-except=" + dir, "--load-extension=" + dir},
			"ignoreDefaultArgs": []string{"--disable-extensions"},
		}
	}
	err := WithManagedPlaywrightStateLock(paths.DataDir, paths.SessionKey, func() error {
		state, err := ReadManagedPlaywrightState(paths.DataDir, paths.SessionKey)
		if err != nil {
			return err
		}
		state.AudioMode = mode
		state.AudioAllowed = false
		return WriteManagedPlaywrightState(paths, state)
	})
	if err == nil {
		a.ConfigPath = filepath.Join(paths.SessionDir, "audio-config.json")
		err = writeManagedAudioJSON(a.ConfigPath, map[string]any{"browser": map[string]any{"launchOptions": a.LaunchOptions}})
	}
	if err != nil {
		a.Close()
		return nil, err
	}
	if mode == "muted" {
		fmt.Fprintln(os.Stderr, "managed browser audio: this executable cannot load LCR's Chromium extension; audio stays muted during handoffs (use Playwright Chromium for automatic unmuting)")
	}
	return a, nil
}

func managedAudioExtensionSupported(executable string) bool {
	// Only opt in known Chromium distributions. Branded Chrome/Edge ignore
	// extension-loading flags; arbitrary configured executables fail closed.
	path := filepath.ToSlash(executable)
	return strings.HasSuffix(path, "/Chromium.app/Contents/MacOS/Chromium") ||
		strings.HasSuffix(path, "/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing") ||
		strings.Contains(path, "/chromium-") && (strings.HasSuffix(path, "/chrome") || strings.HasSuffix(path, "/chrome.exe"))
}

func writeManagedAudioJSON(path string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o600)
}

func managedAudioMuted(paths ManagedPlaywrightPaths) bool {
	state, err := ReadManagedPlaywrightState(paths.DataDir, paths.SessionKey)
	return err != nil || state.LaunchMode != ManagedLaunchModeBackground || state.Hidden || !state.AudioAllowed
}

func managedAudioHandler(ctx context.Context, paths ManagedPlaywrightPaths) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool {
			return strings.HasPrefix(r.Header.Get("Origin"), "chrome-extension://")
		}}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		lastMuted, lastSent := false, time.Time{}
		for {
			muted := managedAudioMuted(paths)
			if muted != lastMuted || time.Since(lastSent) >= 10*time.Second {
				_ = conn.SetWriteDeadline(time.Now().Add(time.Second))
				if err := conn.WriteJSON(map[string]bool{"muted": muted}); err != nil {
					return
				}
				lastMuted, lastSent = muted, time.Now()
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}
}
