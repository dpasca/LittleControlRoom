package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

func TestUIStallDiagnosticsCaptureWritesArtifacts(t *testing.T) {
	tempDir := t.TempDir()
	d := newUIStallDiagnostics(tempDir, 4242)
	d.artifactRootDir = filepath.Join(tempDir, "stall-captures")
	d.io = uiStallArtifactIO{
		mkdirAll:  os.MkdirAll,
		writeFile: os.WriteFile,
		goroutineProfile: func() ([]byte, error) {
			return []byte("goroutine dump\n"), nil
		},
		blockProfile: func() ([]byte, error) {
			return []byte("block profile\n"), nil
		},
		mutexProfile: func() ([]byte, error) {
			return []byte("mutex profile\n"), nil
		},
	}

	now := time.Date(2026, time.April, 5, 15, 4, 5, 0, time.UTC)
	snapshot := uiStallDiagnosticsSnapshot{
		Enabled:         true,
		Threshold:       2 * time.Second,
		ArtifactRootDir: d.artifactRootDir,
		LastProgressAt:  now.Add(-58 * time.Second),
		LastProgress:    "Update spinnerTickMsg",
		Context: uiStallContext{
			FocusedPane:         string(focusDetail),
			SelectedProjectPath: "/tmp/demo",
			DetailPath:          "/tmp/demo",
			Status:              "busy",
			Width:               120,
			Height:              30,
		},
		ActivePhases: []uiActivePhase{{
			StartedAt:   now.Add(-58 * time.Second),
			Name:        "renderCodexView",
			ProjectPath: "/tmp/demo",
			Detail:      "full-screen",
		}},
		RecentPhases: []uiPhaseBreadcrumb{{
			At:          now.Add(-59 * time.Second),
			Event:       "enter",
			Name:        "Update",
			ProjectPath: "/tmp/demo",
			Detail:      "spinnerTickMsg",
		}},
	}

	record := d.captureStallArtifacts(snapshot)
	if record.Directory == "" {
		t.Fatalf("captureStallArtifacts() missing output directory")
	}
	for _, path := range []string{record.Directory, record.SummaryPath, record.GoroutinePath, record.BlockPath, record.MutexPath} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected artifact %q: %v", path, err)
		}
	}

	rawSummary, err := os.ReadFile(record.SummaryPath)
	if err != nil {
		t.Fatalf("read summary: %v", err)
	}
	var summary uiStallArtifactSummary
	if err := json.Unmarshal(rawSummary, &summary); err != nil {
		t.Fatalf("unmarshal summary: %v", err)
	}
	if summary.PID != 4242 {
		t.Fatalf("summary pid = %d, want 4242", summary.PID)
	}
	if summary.LastProgress != "Update spinnerTickMsg" {
		t.Fatalf("summary last progress = %q, want Update spinnerTickMsg", summary.LastProgress)
	}
	if len(summary.ActivePhases) != 1 || summary.ActivePhases[0].Name != "renderCodexView" {
		t.Fatalf("summary active phases = %#v, want renderCodexView", summary.ActivePhases)
	}
	if got := strings.TrimSpace(string(mustReadFile(t, record.GoroutinePath))); got != "goroutine dump" {
		t.Fatalf("goroutine artifact = %q, want goroutine dump", got)
	}
}

func TestRenderPerfContentShowsLastStallCapture(t *testing.T) {
	d := newUIStallDiagnostics("/Users/tester", 4242)
	d.started = true
	d.artifactRootDir = "/Users/tester/.little-control-room/stall-captures"
	d.lastCapture = uiStallCaptureRecord{
		CapturedAt:     time.Date(2026, time.April, 5, 15, 4, 5, 0, time.UTC),
		StallDuration:  "58s",
		Directory:      "/Users/tester/.little-control-room/stall-captures/20260405-150405.000-stall-58s",
		TopActivePhase: "renderCodexView",
		ActiveProject:  "/tmp/demo",
	}
	d.haveLastCapture = true

	m := Model{
		uiDiagnostics: d,
		homeDir:       "/Users/tester",
	}

	rendered := ansi.Strip(m.renderPerfContent(76))
	for _, want := range []string{
		"Stall Capture",
		"Watchdog: armed at 2s",
		"Last capture: 58s",
		"Phase: renderCodexView",
		"Capture dir: ~/.little-control-room/stall-captures/",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("renderPerfContent() missing %q: %q", want, rendered)
		}
	}
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}
