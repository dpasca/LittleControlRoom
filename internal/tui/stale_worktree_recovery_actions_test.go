package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"lcroom/internal/service"
)

func TestCleanupRecoveryActionsRequireSeparatePurgeConfirmation(t *testing.T) {
	d := &staleWorktreeCleanupDialogState{Finished: true, Results: []staleWorktreeCleanupResult{{
		Candidate: service.StaleWorktreeCleanupCandidate{ProjectPath: "/work/task"},
		Finalize:  service.FinalizeMergedWorktreeResult{WorktreeRemoved: true, Recovery: &service.WorktreeRecovery{Location: "/durable/recovery", Phase: "removed", RetainedBytes: 8192, Verified: true}},
	}}}
	m := Model{staleWorktreeCleanup: d}
	_, cmd := m.updateStaleWorktreeCleanupMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	if cmd != nil || !d.PurgeConfirm {
		t.Fatal("permanent deletion did not stop for confirmation")
	}
	rendered := renderStaleWorktreeCleanupResults(d, 100, 50)
	for _, want := range []string{"removed with recovery retained", "8192 bytes at last check", "/durable/recovery", "Review", "Restore", "Permanently delete", "cannot be undone"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("missing %q: %s", want, rendered)
		}
	}
	_, cmd = m.updateStaleWorktreeCleanupMode(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd != nil || d.PurgeConfirm {
		t.Fatal("cancel did not preserve recovery")
	}
	d.RecoveryBusy = true
	_, cmd = m.updateStaleWorktreeCleanupMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	if cmd != nil {
		t.Fatal("duplicate restore started while busy")
	}
}

func TestCleanupReopensDurableRecoveriesFromAudit(t *testing.T) {
	d := &staleWorktreeCleanupDialogState{Audit: service.StaleWorktreeCleanupAudit{Recoveries: []service.WorktreeRecovery{{ProjectPath: "/work/task", Location: "/durable/recovery", Phase: "removed"}}}}
	m := Model{staleWorktreeCleanup: d}
	_, cmd := m.updateStaleWorktreeCleanupMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'v'}})
	if cmd != nil || !d.Finished || len(d.Results) != 1 || d.Results[0].Finalize.Recovery == nil {
		t.Fatal("durable recovery not available after restart")
	}
}
