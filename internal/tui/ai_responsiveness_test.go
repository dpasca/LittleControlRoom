package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func TestUpdatePerfModeCopiesDetailsToClipboard(t *testing.T) {
	prevWriter := clipboardTextWriter
	var copied string
	clipboardTextWriter = func(text string) error {
		copied = text
		return nil
	}
	t.Cleanup(func() {
		clipboardTextWriter = prevWriter
	})

	now := time.Date(2026, time.April, 5, 17, 57, 34, 0, time.FixedZone("JST", 9*60*60))
	updated, cmd := Model{
		showPerf: true,
		aiLatencyRecent: []aiLatencySample{{
			Name:        "UI stall",
			ProjectPath: "/tmp/quickgames",
			Detail:      "spinner tick gap (event loop blocked)",
			Result:      "captured",
			Duration:    10520 * time.Millisecond,
		}},
		uiDiagnostics: &uiStallDiagnostics{
			started:         true,
			artifactRootDir: "/tmp/stall-captures",
			lastCapture: uiStallCaptureRecord{
				CapturedAt:      now,
				StallDuration:   "2.01s",
				Directory:       "/tmp/stall-captures/20260405-175734.075-stall-2.01s",
				TopActivePhase:  "Update",
				ActiveProject:   "/tmp/quickgames",
				ArtifactRootDir: "/tmp/stall-captures",
			},
			haveLastCapture:  true,
			captureThreshold: 2 * time.Second,
		},
	}.updatePerfMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	got := updated.(Model)

	if cmd != nil {
		t.Fatalf("copy should not return an async command")
	}
	if got.status != "Copied performance details to clipboard" {
		t.Fatalf("status = %q, want clipboard confirmation", got.status)
	}
	for _, want := range []string{
		"Performance",
		"Latency",
		"UI stall",
		"Last capture: 2.01s",
		"Capture dir: /tmp/stall-captures/20260405-175734.075-stall-2.01s",
		"copy",
	} {
		if !strings.Contains(copied, want) {
			t.Fatalf("copied text missing %q: %q", want, copied)
		}
	}
}
