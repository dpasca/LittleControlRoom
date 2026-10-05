package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"lcroom/internal/codexapp"
	"lcroom/internal/commands"
	"lcroom/internal/service"
)

func TestSessionCleanupProviderCommandsAndSwitch(t *testing.T) {
	for _, tc := range []struct {
		command  string
		provider codexapp.Provider
	}{{"/session-gc", codexapp.ProviderCodex}, {"/codex-gc", codexapp.ProviderCodex}, {"/claude-gc", codexapp.ProviderClaudeCode}} {
		inv, err := commands.Parse(tc.command)
		if err != nil {
			t.Fatal(err)
		}
		updated, cmd := (Model{}).dispatchCommand(inv)
		m := updated.(Model)
		d := m.codexCleanup
		if cmd == nil || d.Provider != tc.provider || !d.Loading || d.Deleting || d.Confirming {
			t.Fatalf("%s: %+v", tc.command, d)
		}
		d.AuditCancel()
		if _, err := commands.Parse(tc.command + " unexpected"); err == nil {
			t.Fatal("accepted extra arguments")
		}
	}
	d := &codexCleanupDialogState{Focus: cleanupFocusProvider, Chosen: map[string]bool{"old": true}}
	m := Model{codexCleanup: d}
	updated, _ := m.updateCodexCleanupMode(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	updated, _ = m.updateCodexCleanupMode(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(Model)
	updated, cmd := m.updateCodexCleanupMode(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if cmd == nil || d.Provider != codexapp.ProviderClaudeCode || !d.Loading || len(d.Chosen) != 0 {
		t.Fatalf("provider switch did not reset audit: %+v", d)
	}
	d.AuditCancel()
	// A late completion from the previous provider cannot replace the new preview.
	m.applyCodexCleanupAudit(codexCleanupAuditMsg{owner: d, generation: d.AuditGeneration - 1})
	if !d.Loading {
		t.Fatal("stale provider audit replaced current preview")
	}
}
func TestClaudeCleanupSharedReviewAndLayout(t *testing.T) {
	for _, size := range []struct{ w, h int }{{68, 22}, {88, 30}, {108, 40}} {
		for _, screen := range []string{"form", "provider", "error", "review", "storage", "progress", "report"} {
			t.Run(fmt.Sprintf("%dx%d/%s", size.w, size.h, screen), func(t *testing.T) {
				g := codexCleanupTestGroup(time.Now())
				g.Provider = codexapp.ProviderClaudeCode
				d := &codexCleanupDialogState{Provider: codexapp.ProviderClaudeCode, Chosen: map[string]bool{g.WorktreePath: true}, Audit: service.CodexCleanupAudit{Groups: []service.CodexCleanupWorktreeGroup{g}}}
				switch screen {
				case "provider":
					d.Dropdown = true
					d.Focus = cleanupFocusProvider
					d.OptionIndex = 1
				case "error":
					d.ErrorMessage = "Audit failed"
				case "review":
					d.Confirming = true
				case "storage":
					d.ShowRetained = true
				case "progress":
					d.Deleting = true
					d.Queue = []service.CodexCleanupWorktreeGroup{g}
				case "report":
					d.Finished = true
				}
				text := renderCodexCleanupContent(d, size.w, size.h, 0, time.Now())
				if screen != "provider" && strings.Contains(ansi.Strip(text), "Codex") {
					t.Fatalf("wrong provider copy: %s", text)
				}
				if lipgloss.Height(text)+2 > size.h {
					t.Fatalf("overflow: %s", text)
				}
				if screen == "review" && (d.ReviewDelete || !strings.Contains(text, "permanently")) {
					t.Fatal("review must default to Back")
				}
			})
		}
	}
}
func TestClaudeCleanupReopensCurrentJobAndProtectsCachedSessions(t *testing.T) {
	d := &codexCleanupDialogState{Provider: codexapp.ProviderClaudeCode, Deleting: true, Backgrounded: true}
	m := Model{codexCleanup: d, codexSnapshots: map[string]codexapp.Snapshot{
		"claude": {Provider: codexapp.ProviderClaudeCode, ThreadID: "parent/agent-worker"},
		"codex":  {Provider: codexapp.ProviderCodex, ThreadID: "codex"},
	}}
	ids := m.cachedLoadedSessionIDs(codexapp.ProviderClaudeCode)
	if len(ids) != 1 || ids[0] != "parent/agent-worker" {
		t.Fatalf("loaded IDs=%v", ids)
	}
	updated, cmd := m.openCodexCleanup()
	m = updated.(Model)
	if cmd != nil || m.codexCleanup != d || d.Backgrounded || !strings.Contains(m.status, "Claude Code") {
		t.Fatal("alias replaced active job")
	}
	d.Backgrounded = true
	if footer := m.renderFooterCodexCleanupSegment(); !strings.Contains(footer, "Claude Code") || !strings.Contains(footer, "/session-gc") {
		t.Fatalf("footer=%s", footer)
	}
}

func TestClaudeCleanupModalFooterUsesProvider(t *testing.T) {
	for _, state := range []codexCleanupDialogState{
		{Loading: true}, {Deleting: true}, {Deleting: true, CancelRequested: true}, {Finished: true}, {},
	} {
		state.Provider = codexapp.ProviderClaudeCode
		footer := ansi.Strip((Model{codexCleanup: &state}).renderFooter(140))
		if !strings.Contains(footer, "Claude Code cleanup") || strings.Contains(footer, "Codex cleanup") {
			t.Fatalf("footer=%s", footer)
		}
	}
}
