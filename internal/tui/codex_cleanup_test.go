package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"lcroom/internal/commands"
	"lcroom/internal/service"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

func TestDispatchCodexGCOpensReadOnlyAudit(t *testing.T) {
	updated, cmd := (Model{}).dispatchCommand(commands.Invocation{Kind: commands.KindCodexGC})
	got := updated.(Model)
	if cmd == nil || got.codexCleanup == nil || !got.codexCleanup.Loading {
		t.Fatalf("/codex-gc state = %#v, cmd=%v", got.codexCleanup, cmd)
	}
}

func TestCodexCleanupNumberedNavigationAndViewHints(t *testing.T) {
	dialog := &codexCleanupDialogState{Chosen: map[string]bool{"keep-selection": true}}
	m := Model{codexCleanup: dialog}
	updated, cmd := m.updateCodexCleanupMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	m = updated.(Model)
	if cmd != nil || dialog.Loading || !dialog.Chosen["keep-selection"] {
		t.Fatal("choosing the current category must preserve selection without rescanning")
	}
	header := ansi.Strip(strings.Join(renderCodexCleanupNavigation(dialog, 108), "\n"))
	if !strings.Contains(header, "[ 1 Orphaned worktrees ]") || !strings.Contains(header, "Tab: show other storage (read-only)") {
		t.Fatalf("missing active category or explicit view hint: %s", header)
	}
	updated, cmd = m.updateCodexCleanupMode(tea.KeyMsg{Type: tea.KeyTab})
	m = updated.(Model)
	if cmd != nil || !dialog.ShowRetained || !dialog.Chosen["keep-selection"] {
		t.Fatal("view toggle must preserve selection without rescanning")
	}
	header = ansi.Strip(strings.Join(renderCodexCleanupNavigation(dialog, 108), "\n"))
	if !strings.Contains(header, "[ Other storage ]") || !strings.Contains(header, "Tab: return to cleanup candidates") {
		t.Fatalf("missing retained view navigation: %s", header)
	}
	updated, cmd = m.updateCodexCleanupMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	m = updated.(Model)
	if cmd == nil || !dialog.Loading || dialog.ShowRetained || dialog.Category != service.CodexCleanupStale || len(dialog.Chosen) != 0 {
		t.Fatal("changing category must open fresh candidates and clear selection")
	}
	_, cmd = m.updateCodexCleanupMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	if cmd != nil || dialog.Category != service.CodexCleanupStale {
		t.Fatal("loading must ignore repeated navigation")
	}
}

func TestCodexCleanupStaleCategoryUsesGroupedCountsAndConfirmation(t *testing.T) {
	m := Model{codexCleanup: &codexCleanupDialogState{Chosen: map[string]bool{"old-selection": true}}}
	updated, cmd := m.updateCodexCleanupMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	m = updated.(Model)
	if cmd == nil || !m.codexCleanup.Loading || m.codexCleanup.Category != service.CodexCleanupStale || len(m.codexCleanup.Chosen) != 0 {
		t.Fatal("category switch must clear selection and run a fresh audit")
	}
	group := codexCleanupTestGroup(time.Now())
	group.Category = service.CodexCleanupStale
	group.WorktreeName = "okmain"
	group.TotalThreadCount = 240
	group.RootThreadCount = 210
	group.DescendantCount = 0
	updated, _ = m.applyCodexCleanupAudit(codexCleanupAuditMsg{audit: service.CodexCleanupAudit{Category: service.CodexCleanupStale, Groups: []service.CodexCleanupWorktreeGroup{group}}})
	m = updated.(Model)
	view := ansi.Strip(renderCodexCleanupContent(m.codexCleanup, 108, 48, 0, time.Now()))
	for _, want := range []string{"Stale sessions", "REMOVE", "KEEP", "okmain", "Remove 210 of 240", "keep 30"} {
		if !strings.Contains(view, want) {
			t.Fatalf("missing %q in %s", want, view)
		}
	}
	m.codexCleanup.Chosen[group.WorktreePath] = true
	updated, cmd = m.updateCodexCleanupMode(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if cmd != nil || !m.codexCleanup.Confirming || m.codexCleanup.Deleting {
		t.Fatal("stale cleanup must require separate permanent confirmation")
	}
	view = ansi.Strip(renderCodexCleanupConfirmation(m.codexCleanup, 108))
	if !strings.Contains(view, "Remove 210 of 240 sessions; keep 30") {
		t.Fatalf("confirmation lacks per-project counts: %s", view)
	}
}

func TestCodexCleanupRetainedViewCannotDelete(t *testing.T) {
	dialog := &codexCleanupDialogState{
		Chosen: map[string]bool{"/eligible": true},
		Audit: service.CodexCleanupAudit{
			Groups: []service.CodexCleanupWorktreeGroup{{WorktreePath: "/eligible"}},
			Retained: []service.CodexCleanupRetainedGroup{
				{Name: "kept-project", Path: "/kept-project", Bytes: 2 << 30, Files: 20, Reason: "No LCR deleted-worktree record"},
				{Name: "cache", Bytes: 2 << 20},
			},
		},
	}
	m := Model{codexCleanup: dialog}
	for _, key := range []string{"v", " ", "a", "enter", "d"} {
		msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
		if key == "enter" {
			msg = tea.KeyMsg{Type: tea.KeyEnter}
		}
		updated, cmd := m.updateCodexCleanupMode(msg)
		m = updated.(Model)
		if cmd != nil || dialog.Confirming || dialog.Deleting || !dialog.Chosen["/eligible"] {
			t.Fatalf("retained view changed deletion state on %q", key)
		}
	}
	view := ansi.Strip(renderCodexCleanupContent(dialog, 100, 38, 0, time.Now()))
	for _, want := range []string{"retained", "read-only", "kept-project", "2.0 GiB", "2.0 MiB", "No LCR deleted-worktree record"} {
		if !strings.Contains(view, want) {
			t.Fatalf("missing %q in %s", want, view)
		}
	}
	m.updateCodexCleanupMode(tea.KeyMsg{Type: tea.KeyTab})
	if dialog.ShowRetained || !dialog.Chosen["/eligible"] {
		t.Fatal("switching back must preserve selections")
	}
}

func TestCodexCleanupSortingPreservesFocusAndSelection(t *testing.T) {
	now := time.Now()
	groups := []service.CodexCleanupWorktreeGroup{
		{WorktreePath: "/a", WorktreeName: "a", RecoverableBytes: 10, LastActivity: now.Add(-time.Hour)},
		{WorktreePath: "/b", WorktreeName: "b", RecoverableBytes: 100, LastActivity: now},
		{WorktreePath: "/c", WorktreeName: "c", RecoverableBytes: 50, LastActivity: now.Add(-2 * time.Hour)},
	}
	m := Model{codexCleanup: &codexCleanupDialogState{Loading: true}}
	updated, _ := m.applyCodexCleanupAudit(codexCleanupAuditMsg{audit: service.CodexCleanupAudit{Groups: groups}})
	m = updated.(Model)
	dialog := m.codexCleanup
	if dialog.Audit.Groups[0].WorktreePath != "/b" || dialog.Selected != 0 || groups[0].WorktreePath != "/a" {
		t.Fatal("audit must default to largest first without mutating the service snapshot")
	}
	dialog.Chosen["/b"] = true
	for _, first := range []string{"/c", "/a", "/b"} {
		updated, cmd := m.updateCodexCleanupMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
		m = updated.(Model)
		if cmd != nil || dialog.Audit.Groups[0].WorktreePath != first || dialog.Audit.Groups[dialog.Selected].WorktreePath != "/b" || !dialog.Chosen["/b"] {
			t.Fatalf("sort lost ordering, focus or selection: %#v", dialog)
		}
	}
	view := ansi.Strip(renderCodexCleanupContent(dialog, 100, 45, 0, now))
	for _, column := range []string{"SIZE", "AGE", "ROOTS", "CHILD", "WORKTREE", "largest first"} {
		if !strings.Contains(view, column) {
			t.Fatalf("missing %s in %s", column, view)
		}
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
	for _, want := range []string{"Clean Codex session storage", "feature/old-cleanup", "parent master", "worktree missing 20d", "2 children", "recoverable", "LCR removed"} {
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

func TestCodexCleanupReportFitsViewportWithLongWorktreeNames(t *testing.T) {
	const (
		bodyH       = 32
		panelW      = 112
		panelInnerW = panelW - 4
	)
	now := time.Now()
	group := codexCleanupTestGroup(now)
	dialog := &codexCleanupDialogState{Finished: true}
	for index := 0; index < 40; index++ {
		item := group
		item.WorktreePath = fmt.Sprintf("/tmp/%s-%02d", strings.Repeat("long-worktree-name-", 8), index)
		dialog.Results = append(dialog.Results, codexCleanupDeleteResult{
			Group: item,
			Result: service.DeleteCodexCleanupWorktreeResult{
				DeletedRootThreads:     1,
				DeletedDescendants:     2,
				VerifiedReclaimedBytes: item.RecoverableBytes,
				Verified:               true,
			},
		})
	}

	content := renderCodexCleanupContent(dialog, panelInnerW, bodyH, 0, now)
	panel := renderDialogPanel(panelW, panelInnerW, content)
	if got, wantMax := lipgloss.Height(panel), bodyH-2; got > wantMax {
		t.Fatalf("Codex cleanup report panel height = %d, want <= %d:\n%s", got, wantMax, ansi.Strip(panel))
	}
	if rendered := ansi.Strip(panel); !strings.Contains(rendered, "2 descendants · 2.0 KiB verified") {
		t.Fatalf("Codex cleanup report should preserve verification details after truncating long names:\n%s", rendered)
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
