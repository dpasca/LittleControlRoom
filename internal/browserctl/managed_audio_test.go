package browserctl

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func audioTestPaths(t *testing.T, mode ManagedLaunchMode) ManagedPlaywrightPaths {
	t.Helper()
	paths, err := ManagedPlaywrightPathsFor(t.TempDir(), "codex", "/project", "session", "profile", mode)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteManagedPlaywrightState(paths, ManagedPlaywrightState{SessionKey: paths.SessionKey, LaunchMode: mode}); err != nil {
		t.Fatal(err)
	}
	return paths
}

func TestManagedAudioLaunchModes(t *testing.T) {
	for _, mode := range []ManagedLaunchMode{ManagedLaunchModeHeaded, ManagedLaunchModeHeadless} {
		paths := audioTestPaths(t, mode)
		audio, err := PrepareManagedBrowserAudio(paths, "/Applications/Chromium.app/Contents/MacOS/Chromium")
		if err != nil {
			t.Fatal(err)
		}
		defer audio.Close()
		if len(audio.LaunchOptions) != 0 || audio.ConfigPath != "" || audio.server != nil {
			t.Fatalf("%s should retain Playwright's default audio behavior: %#v", mode, audio)
		}
	}
	for _, executable := range []string{"", "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome", "/custom/browser"} {
		paths := audioTestPaths(t, ManagedLaunchModeBackground)
		audio, err := PrepareManagedBrowserAudio(paths, executable)
		if err != nil {
			t.Fatal(err)
		}
		defer audio.Close()
		if args := audio.LaunchOptions["args"].([]string); len(args) != 1 || args[0] != "--mute-audio" {
			t.Fatalf("unsupported executable must stay muted: %#v", audio)
		}
		state, _ := ReadManagedPlaywrightState(paths.DataDir, paths.SessionKey)
		if state.AudioMode != "muted" || state.AudioAllowed {
			t.Fatalf("fallback state: %#v", state)
		}
	}
}

func TestManagedAudioBridgeFollowsConfirmedRevealAndHide(t *testing.T) {
	paths := audioTestPaths(t, ManagedLaunchModeBackground)
	audio, err := PrepareManagedBrowserAudio(paths, "/Applications/Chromium.app/Contents/MacOS/Chromium")
	if err != nil {
		t.Fatal(err)
	}
	defer audio.Close()
	raw, err := os.ReadFile(filepath.Join(paths.SessionDir, "audio-extension", "background.js"))
	if err != nil {
		t.Fatal(err)
	}
	first, _, _ := strings.Cut(string(raw), "\n")
	var endpoint string
	if err := json.Unmarshal([]byte(strings.TrimSuffix(strings.TrimPrefix(first, "const audioEndpoint = "), ";")), &endpoint); err != nil {
		t.Fatal(err)
	}
	// An ordinary web page cannot connect even if it learns the private URL.
	if conn, _, err := websocket.DefaultDialer.Dial(endpoint, http.Header{"Origin": {"https://example.test"}}); err == nil {
		conn.Close()
		t.Fatal("web page origin accepted")
	}
	conn, _, err := websocket.DefaultDialer.Dial(endpoint, http.Header{"Origin": {"chrome-extension://test"}})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	readMuted := func(want bool) {
		t.Helper()
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		var policy struct{ Muted bool }
		if err := conn.ReadJSON(&policy); err != nil {
			t.Fatal(err)
		}
		if policy.Muted != want {
			t.Fatalf("muted = %v, want %v", policy.Muted, want)
		}
	}
	readMuted(true) // Hidden starts false before process detection; still silent.
	original := managedPlaywrightStateRevealer
	t.Cleanup(func() { managedPlaywrightStateRevealer = original })
	managedPlaywrightStateRevealer = func(ManagedPlaywrightState) error {
		if !managedAudioMuted(paths) {
			t.Fatal("audio enabled before OS reveal succeeded")
		}
		return errors.New("activation failed")
	}
	if _, err := RevealManagedPlaywrightSession(paths.DataDir, paths.SessionKey); err == nil {
		t.Fatal("expected failed reveal")
	}
	if !managedAudioMuted(paths) {
		t.Fatal("failed reveal enabled audio")
	}
	managedPlaywrightStateRevealer = func(ManagedPlaywrightState) error { return nil }
	if _, err := RevealManagedPlaywrightSession(paths.DataDir, paths.SessionKey); err != nil {
		t.Fatal(err)
	}
	readMuted(false)
	if _, _, err := RequestManagedPlaywrightSessionHide(paths.DataDir, paths.SessionKey); err != nil {
		t.Fatal(err)
	}
	readMuted(true)
	if err := os.Remove(paths.StatePath); err != nil {
		t.Fatal(err)
	}
	if !managedAudioMuted(paths) {
		t.Fatal("missing state must fail muted")
	}
	audio.Close()
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, _, err := conn.ReadMessage(); err == nil {
		t.Fatal("bridge connection survived owner shutdown")
	}
}

func TestManagedAudioExtensionBehavior(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required for the extension behavior tests")
	}
	cmd := exec.Command(node, "--test", "managed_audio_extension_test.js")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("extension tests: %v\n%s", err, output)
	}
}
