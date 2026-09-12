package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"lcroom/internal/codexapp"
)

func TestCodexFastModeIndicators(t *testing.T) {
	for _, tt := range []struct {
		name, tier, next, err, want string
		busy, pending               bool
	}{
		{name: "enabled", tier: "fast", next: "fast", want: "FAST · higher usage"},
		{name: "priority", tier: "priority", next: "priority", want: "FAST · higher usage"},
		{name: "disabled", tier: "default", next: "default", want: "Standard · fast OFF"},
		{name: "running after off", tier: "fast", next: "default", busy: true, want: "FAST finishing · next OFF"},
		{name: "on next turn", tier: "default", next: "fast", busy: true, want: "FAST next · current OFF"},
		{name: "pending disable", tier: "fast", next: "default", pending: true, want: "FAST · change pending"},
		{name: "pending enable", tier: "default", next: "fast", pending: true, want: "FAST pending"},
		{name: "apply failed", tier: "fast", next: "default", err: "RPC failed", want: "FAST · status uncertain"},
		{name: "unknown", tier: "default", err: "read failed", want: "FAST status unknown"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			snapshot := codexapp.Snapshot{Provider: codexapp.ProviderCodex, Started: true, ProjectPath: "/demo/project", Model: "gpt-test", ReasoningEffort: "high", ServiceTier: tt.tier, Busy: tt.busy, FastMode: codexapp.FastModeSnapshot{Managed: true, Tier: tt.next, Error: tt.err, Pending: tt.pending}}
			got, warning := codexFastModeLabel(snapshot)
			if got != tt.want {
				t.Fatalf("label=%q want %q", got, tt.want)
			}
			for _, width := range []int{22, 35, 60} {
				rows := embeddedSidebarModelRows(snapshot, width)
				rendered := ansi.Strip(strings.Join(rows, "\n"))
				if !strings.Contains(rendered, "FAST") && !(strings.Contains(rendered, "fast") && strings.Contains(rendered, "OFF")) {
					t.Fatalf("lost indicator at width %d: %s", width, rendered)
				}
				for _, row := range rows {
					if ansi.StringWidth(row) > width {
						t.Fatalf("row overflows width %d: %q", width, row)
					}
				}
			}
			if warning {
				// Header remains visible even when sidebar is collapsed or unavailable.
				banner := ansi.Strip((Model{}).renderCodexBanner(snapshot, 85))
				if !strings.Contains(banner, "FAST") {
					t.Fatalf("header omitted warning: %s", banner)
				}
			}
		})
	}
}

func TestCodexFastModeUnsupportedBusyAndMissingSession(t *testing.T) {
	m := Model{}
	updated, cmd := m.setVisibleCodexFastMode(codexapp.Snapshot{Provider: codexapp.ProviderClaudeCode}, "on")
	if cmd != nil || !strings.Contains(normalizeUpdateModel(updated).status, "only for Codex") {
		t.Fatal("unsupported provider accepted /fast")
	}
	m.codexFastModeBusy = true
	updated, cmd = m.setVisibleCodexFastMode(codexapp.Snapshot{Provider: codexapp.ProviderCodex}, "off")
	if cmd != nil || !strings.Contains(normalizeUpdateModel(updated).status, "already in progress") {
		t.Fatal("repeat activation was not ignored")
	}
	m.codexFastModeBusy = false
	updated, cmd = m.setVisibleCodexFastMode(codexapp.Snapshot{Provider: codexapp.ProviderCodex}, "on")
	if cmd != nil || normalizeUpdateModel(updated).codexFastModeBusy {
		t.Fatal("missing session left action busy")
	}
}
