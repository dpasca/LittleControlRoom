package tui

import (
	"strings"
	"testing"
	"time"

	"lcroom/internal/commands"
	"lcroom/internal/service"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestDispatchCodexGCOpensReadOnlyAudit(t *testing.T) {
	updated, cmd := (Model{}).dispatchCommand(commands.Invocation{Kind: commands.KindCodexGC})
	got := updated.(Model)
	if cmd == nil || got.codexCleanup == nil || !got.codexCleanup.Loading {
		t.Fatalf("/codex-gc state = %#v, cmd=%v", got.codexCleanup, cmd)
	}
}

func TestCodexCleanupRequiresSelectionAndSeparatePermanentConfirmation(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	group := codexCleanupTestGroup(now)
	m := Model{
		nowFn: func() time.Time { return now },
		codexCleanup: &codexCleanupDialogState{
			Chosen:  make(map[string]bool),
			Loading: true,
		},
	}
	updated, cmd := m.applyCodexCleanupAudit(codexCleanupAuditMsg{audit: service.CodexCleanupAudit{
		AuditedAt:           now,
		ScannedThreads:      8,
		MissingCWDThreads:   5,
		EligibleRootThreads: 1,
		EligibleDescendants: 2,
		RecoverableBytes:    group.RecoverableBytes,
		Groups:              []service.CodexCleanupWorktreeGroup{group},
		Excluded:            service.CodexCleanupExclusions{Pinned: 1, Recent: 1},
	}})
	if cmd != nil {
		t.Fatal("applying audit should not schedule deletion")
	}
	got := updated.(Model)
	rendered := ansi.Strip(got.renderCodexCleanupOverlay("", 120, 38))
	for _, want := range []string{"Clean Codex session storage", "feature/old-cleanup", "parent master", "worktree missing 20d", "2 children", "recoverable", "LCR removed", "thread-r"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("cleanup preview missing %q:\n%s", want, rendered)
		}
	}

	updated, cmd = got.updateCodexCleanupMode(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if cmd != nil || got.codexCleanup.Confirming {
		t.Fatal("Enter without a Space selection must not reach confirmation")
	}
	updated, _ = got.updateCodexCleanupMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	got = updated.(Model)
	if !got.codexCleanup.Chosen[group.WorktreePath] {
		t.Fatal("Space did not explicitly select the worktree group")
	}
	updated, cmd = got.updateCodexCleanupMode(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if cmd != nil || !got.codexCleanup.Confirming || got.codexCleanup.Deleting {
		t.Fatal("Enter should open a warning without deleting")
	}
	confirmation := ansi.Strip(got.renderCodexCleanupOverlay("", 120, 38))
	for _, want := range []string{"WARNING: THIS CANNOT BE UNDONE", "PERMANENTLY DELETE", "2 spawned", "Codex app-server"} {
		if !strings.Contains(confirmation, want) {
			t.Fatalf("permanent warning missing %q:\n%s", want, confirmation)
		}
	}
	updated, cmd = got.updateCodexCleanupMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	got = updated.(Model)
	if cmd == nil || !got.codexCleanup.Deleting || got.codexCleanup.QueueIndex != 0 {
		t.Fatalf("D confirmation did not start one guarded deletion: %#v cmd=%v", got.codexCleanup, cmd)
	}
}

func TestCodexCleanupReportsVerifiedReclaimedSpace(t *testing.T) {
	now := time.Now()
	group := codexCleanupTestGroup(now)
	m := Model{codexCleanup: &codexCleanupDialogState{
		Chosen:     map[string]bool{group.WorktreePath: true},
		Deleting:   true,
		Queue:      []service.CodexCleanupWorktreeGroup{group},
		QueueIndex: 0,
	}}
	updated, cmd := m.applyCodexCleanupDelete(codexCleanupDeleteMsg{
		group: group,
		result: service.DeleteCodexCleanupWorktreeResult{
			WorktreePath:           group.WorktreePath,
			DeletedRootThreads:     1,
			DeletedDescendants:     2,
			VerifiedReclaimedBytes: group.RecoverableBytes,
			ExpectedBytes:          group.RecoverableBytes,
			Verified:               true,
		},
	})
	got := updated.(Model)
	if cmd != nil || got.codexCleanup.Deleting || !got.codexCleanup.Finished {
		t.Fatalf("completed cleanup state = %#v", got.codexCleanup)
	}
	rendered := ansi.Strip(got.renderCodexCleanupOverlay("", 110, 32))
	if !strings.Contains(rendered, "Verified reclaimed space") || !strings.Contains(rendered, "2 descendants") || !strings.Contains(rendered, "2.0 KiB") {
		t.Fatalf("verified cleanup report:\n%s", rendered)
	}
}

func codexCleanupTestGroup(now time.Time) service.CodexCleanupWorktreeGroup {
	return service.CodexCleanupWorktreeGroup{
		WorktreePath:     "/tmp/demo--old-cleanup",
		WorktreeName:     "demo--old-cleanup",
		RootProjectPath:  "/tmp/demo",
		Branch:           "feature/old-cleanup",
		ParentBranch:     "master",
		MissingSince:     now.Add(-20 * 24 * time.Hour),
		LastActivity:     now.Add(-60 * 24 * time.Hour),
		Reason:           "LCR removed this linked worktree and its saved working directory is still missing",
		RootThreadCount:  1,
		DescendantCount:  2,
		RecoverableBytes: 2048,
		Revision:         "preview-revision",
		Threads: []service.CodexCleanupThread{{
			ID:               "thread-root-old",
			GitSHA:           "abcdef1234567890",
			GitBranch:        "feature/old-cleanup",
			LastActivity:     now.Add(-60 * 24 * time.Hour),
			DescendantCount:  2,
			RecoverableBytes: 2048,
		}},
	}
}
