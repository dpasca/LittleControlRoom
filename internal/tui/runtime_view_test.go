package tui

import (
	"strings"
	"testing"
	"time"

	"lcroom/internal/projectrun"

	"github.com/charmbracelet/x/ansi"
)

func TestRenderRuntimeStatusValueShowsStoppedForUserStoppedRuntime(t *testing.T) {
	got := ansi.Strip(renderRuntimeStatusValue(projectrun.Snapshot{
		ExitedAt: time.Date(2026, time.March, 18, 8, 9, 54, 0, time.Local),
	}))
	if strings.TrimSpace(got) != "stopped" {
		t.Fatalf("renderRuntimeStatusValue() = %q, want %q", got, "stopped")
	}
}

func TestFooterRuntimeSegmentShowsActiveRuntimeCount(t *testing.T) {
	m := Model{runtimeSnapshots: map[string]projectrun.Snapshot{
		"/tmp/a": {ProjectPath: "/tmp/a", Running: true},
		"/tmp/b": {ProjectPath: "/tmp/b", Running: true},
		"/tmp/c": {ProjectPath: "/tmp/c"},
	}}
	got := ansi.Strip(m.renderFooterRuntimeSegment())
	if !strings.Contains(got, "2 runtimes active") {
		t.Fatalf("renderFooterRuntimeSegment() = %q", got)
	}
}

func TestRuntimeRelativeCWDShowsSubdirectory(t *testing.T) {
	got := runtimeRelativeCWD("/tmp/project", "/tmp/project/frontend")
	if got != "frontend" {
		t.Fatalf("runtimeRelativeCWD() = %q, want frontend", got)
	}
}

func TestEffectiveRuntimeCommandPrefersManagedSnapshotCommand(t *testing.T) {
	got := effectiveRuntimeCommand("pnpm dev", projectrun.Snapshot{Command: "pnpm preview", Running: true})
	if got != "pnpm preview" {
		t.Fatalf("effectiveRuntimeCommand() = %q, want snapshot command", got)
	}
}

func TestEffectiveRuntimeCommandPrefersSavedCommandForStoppedSnapshot(t *testing.T) {
	got := effectiveRuntimeCommand("pnpm dev", projectrun.Snapshot{
		Command:       "npm run dev",
		ExitCodeKnown: true,
		ExitCode:      1,
		LastError:     "exit status 1",
	})
	if got != "pnpm dev" {
		t.Fatalf("effectiveRuntimeCommand() = %q, want saved command", got)
	}
}

func TestEffectiveRuntimeCommandUsesStoppedSnapshotWhenUnset(t *testing.T) {
	got := effectiveRuntimeCommand("", projectrun.Snapshot{
		Command:       "npm run dev",
		ExitCodeKnown: true,
		ExitCode:      1,
	})
	if got != "npm run dev" {
		t.Fatalf("effectiveRuntimeCommand() = %q, want stopped snapshot command", got)
	}
}
