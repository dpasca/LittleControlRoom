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
