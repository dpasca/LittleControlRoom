package tui

import (
	"context"
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
	if got.codexCleanup.Cancel == nil {
		t.Fatal("D confirmation did not retain a cancellation handle for the background deletion")
	}
	got.codexCleanup.Cancel()
}

func TestCodexCleanupSelectsAndClearsAllGroups(t *testing.T) {
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	first := codexCleanupTestGroup(now)
	second := first
	second.WorktreePath = "/tmp/demo--another-cleanup"
	second.WorktreeName = "demo--another-cleanup"
	second.Revision = "another-preview-revision"
	second.Threads = append([]service.CodexCleanupThread(nil), first.Threads...)
	second.Threads[0].ID = "thread-root-another"

	m := Model{codexCleanup: &codexCleanupDialogState{
		Audit:  service.CodexCleanupAudit{Groups: []service.CodexCleanupWorktreeGroup{first, second}},
		Chosen: make(map[string]bool),
	}}
	updated, cmd := m.updateCodexCleanupMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	got := updated.(Model)
	if cmd != nil || len(selectedCodexCleanupGroups(got.codexCleanup)) != 2 || !allCodexCleanupGroupsSelected(got.codexCleanup) {
		t.Fatalf("select-all state = %#v, cmd=%v", got.codexCleanup.Chosen, cmd)
	}
	rendered := ansi.Strip(got.renderCodexCleanupOverlay("", 120, 38))
	if !strings.Contains(rendered, "clear all") {
		t.Fatalf("selected cleanup actions do not offer clear all:\n%s", rendered)
	}

	updated, cmd = got.updateCodexCleanupMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'A'}})
	got = updated.(Model)
	if cmd != nil || len(selectedCodexCleanupGroups(got.codexCleanup)) != 0 || allCodexCleanupGroupsSelected(got.codexCleanup) {
		t.Fatalf("clear-all state = %#v, cmd=%v", got.codexCleanup.Chosen, cmd)
	}
	if got.status != "Cleared all Codex cleanup selections" {
		t.Fatalf("clear-all status = %q", got.status)
	}
}

func TestCodexCleanupCanRunInBackgroundReopenAndAbort(t *testing.T) {
	group := codexCleanupTestGroup(time.Now())
	canceled := false
	m := Model{codexCleanup: &codexCleanupDialogState{
		Deleting: true,
		Queue:    []service.CodexCleanupWorktreeGroup{group},
		Cancel:   func() { canceled = true },
	}}

	updated, cmd := m.updateCodexCleanupMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}})
	got := updated.(Model)
	if cmd != nil || !got.codexCleanup.Backgrounded || !got.codexCleanup.Deleting || got.codexCleanupVisible() {
		t.Fatalf("background cleanup state = %#v, cmd=%v", got.codexCleanup, cmd)
	}
	footer := ansi.Strip(got.renderFooterCodexCleanupSegment())
	if !strings.Contains(footer, "Codex GC 1/1") {
		t.Fatalf("background cleanup footer = %q", footer)
	}

	updated, cmd = got.dispatchCommand(commands.Invocation{Kind: commands.KindCodexGC})
	got = updated.(Model)
	if cmd != nil || got.codexCleanup.Backgrounded || !got.codexCleanupVisible() || !got.codexCleanup.Deleting {
		t.Fatalf("reopened cleanup state = %#v, cmd=%v", got.codexCleanup, cmd)
	}

	updated, cmd = got.updateCodexCleanupMode(tea.KeyMsg{Type: tea.KeyEsc})
	got = updated.(Model)
	if cmd != nil || !canceled || !got.codexCleanup.CancelRequested || !got.codexCleanup.Deleting {
		t.Fatalf("abort cleanup state = canceled:%v dialog:%#v cmd=%v", canceled, got.codexCleanup, cmd)
	}
	progress := ansi.Strip(got.renderCodexCleanupOverlay("", 110, 32))
	if !strings.Contains(progress, "Aborting Codex session cleanup") || !strings.Contains(progress, "no queued worktree will start") {
		t.Fatalf("abort progress does not explain stop semantics:\n%s", progress)
	}
}

func TestCodexCleanupAbortStopsQueueAndKeepsVerifiedReport(t *testing.T) {
	now := time.Now()
	first := codexCleanupTestGroup(now)
	second := first
	second.WorktreePath = "/tmp/demo--queued-cleanup"
	second.WorktreeName = "demo--queued-cleanup"
	m := Model{codexCleanup: &codexCleanupDialogState{
		Backgrounded:    true,
		Deleting:        true,
		CancelRequested: true,
		Queue:           []service.CodexCleanupWorktreeGroup{first, second},
		QueueIndex:      0,
	}}

	updated, cmd := m.applyCodexCleanupDelete(codexCleanupDeleteMsg{
		group: first,
		result: service.DeleteCodexCleanupWorktreeResult{
			WorktreePath:           first.WorktreePath,
			DeletedRootThreads:     1,
			DeletedDescendants:     2,
			VerifiedReclaimedBytes: first.RecoverableBytes,
			ExpectedBytes:          first.RecoverableBytes,
			Verified:               true,
		},
		err: context.Canceled,
	})
	got := updated.(Model)
	if cmd != nil || got.codexCleanup.Deleting || !got.codexCleanup.Finished || !got.codexCleanup.Aborted {
		t.Fatalf("aborted cleanup state = %#v, cmd=%v", got.codexCleanup, cmd)
	}
	if got.codexCleanup.QueueIndex != 1 || len(got.codexCleanup.Results) != 1 || !got.codexCleanup.Results[0].Canceled {
		t.Fatalf("aborted cleanup results = %#v", got.codexCleanup)
	}
	if len(got.errorLogEntries) != 0 || !strings.Contains(got.status, "/codex-gc opens the report") {
		t.Fatalf("aborted cleanup status/log = %q / %#v", got.status, got.errorLogEntries)
	}
	report := ansi.Strip(renderCodexCleanupContent(got.codexCleanup, 100, 32, 0, now))
	for _, want := range []string{"Cleanup aborted", "1 queued worktree group did not start", "2.0 KiB", "2 descendants"} {
		if !strings.Contains(report, want) {
			t.Fatalf("aborted cleanup report missing %q:\n%s", want, report)
		}
	}
	footer := ansi.Strip(got.renderFooterCodexCleanupSegment())
	if !strings.Contains(footer, "Codex GC stopped") {
		t.Fatalf("aborted cleanup footer = %q", footer)
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
