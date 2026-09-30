package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"lcroom/internal/claudeartifact"
	"lcroom/internal/codexapp"
)

func TestClaudeParentPaneShowsChildProgressWithoutSidebar(t *testing.T) {
	now := time.Now()
	snapshot := codexapp.Snapshot{Provider: codexapp.ProviderClaudeCode, Busy: true, Phase: codexapp.SessionPhaseRunning,
		Subagents: []claudeartifact.SubagentProgress{
			{ID: "perf", Description: "Phone profiling", LatestAction: "Bash: Compare graphics tiers", UpdatedAt: now.Add(-time.Minute)},
			{ID: "icon", Description: "Icon review", Completed: true, UpdatedAt: now.Add(-4 * time.Hour)},
		},
	}
	m := Model{codexInput: newCodexTextarea()}
	for _, width := range []int{60, 120} {
		text := ansi.Strip(strings.Join(m.codexLowerBlocks(snapshot, width), "\n"))
		for _, want := range []string{"1 active", "1 completed", "Phone profiling", "Icon review", "Compare graphics tiers", "ago"} {
			if !strings.Contains(text, want) {
				t.Fatalf("width %d missing %q:\n%s", width, want, text)
			}
		}
		for _, line := range m.renderCodexSubagentProgress(snapshot, width) {
			if ansi.StringWidth(line) > width {
				t.Fatalf("panel overflows at width %d: %q", width, line)
			}
		}
	}
	if footer := codexFooterStatus(snapshot, now); !strings.Contains(footer, "Subagents: 1 active") {
		t.Fatalf("footer still hides children: %q", footer)
	}
	snapshot.Subagents[0].UpdatedAt = now.Add(-time.Hour)
	text := ansi.Strip(strings.Join(m.renderCodexSubagentProgress(snapshot, 100), "\n"))
	if !strings.Contains(text, "no recent activity") || strings.Contains(text, "1 active") {
		t.Fatalf("silent child still claims active: %s", text)
	}
	snapshot.PendingApproval = &codexapp.ApprovalRequest{}
	if codexFooterStatus(snapshot, now) != "Waiting for approval" {
		t.Fatal("child progress hid approval")
	}
	snapshot.SubagentProgressError = "Subagent activity unavailable: cannot read logs"
	text = ansi.Strip(strings.Join(m.renderCodexSubagentProgress(snapshot, 100), "\n"))
	if !strings.Contains(text, "unavailable") {
		t.Fatalf("hidden failure: %s", text)
	}
}

func TestOverlaySnapshotCopiesChildProgress(t *testing.T) {
	state := codexapp.Snapshot{Subagents: []claudeartifact.SubagentProgress{{ID: "child", Description: "Task"}}, SubagentProgressError: "read failed"}
	cached := overlayCodexSnapshotState(codexapp.Snapshot{}, state)
	state.Subagents[0].Description = "changed"
	if cached.Subagents[0].Description != "Task" || cached.SubagentProgressError != "read failed" {
		t.Fatalf("overlay lost or aliased activity: %#v", cached)
	}
}
