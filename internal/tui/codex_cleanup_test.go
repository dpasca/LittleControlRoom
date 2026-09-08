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
	"github.com/muesli/termenv"
)

func TestDispatchCodexGCOpensReadOnlyAudit(t *testing.T) {
	updated, cmd := (Model{}).dispatchCommand(commands.Invocation{Kind: commands.KindCodexGC})
	got := updated.(Model)
	if cmd == nil || got.codexCleanup == nil || !got.codexCleanup.Loading {
		t.Fatalf("/codex-gc state = %#v, cmd=%v", got.codexCleanup, cmd)
	}
	got.codexCleanup.AuditCancel()
}

func TestCodexCleanupAuditIgnoresClosedAndSupersededScans(t *testing.T) {
	old := &codexCleanupDialogState{Loading: true, AuditGeneration: 1}
	current := &codexCleanupDialogState{Loading: true, AuditGeneration: 2}
	m := Model{codexCleanup: current}
	for _, msg := range []codexCleanupAuditMsg{{owner: old, generation: 1}, {owner: current, generation: 1}} {
		m.applyCodexCleanupAudit(msg)
		if !current.Loading {
			t.Fatal("stale completion must not replace the current preview")
		}
	}
	canceled := false
	current.AuditCancel = func() { canceled = true }
	updated, _ := m.updateCodexCleanupMode(tea.KeyMsg{Type: tea.KeyEsc})
	if !canceled || updated.(Model).codexCleanup != nil {
		t.Fatal("closing scan must cancel its worker")
	}
}

func TestCodexCleanupLayoutsKeepActionsAndColumnsVisible(t *testing.T) {
	for _, size := range []struct{ width, height int }{{68, 22}, {88, 30}, {108, 40}} {
		for _, screen := range []string{"orphaned", "stale", "category", "age", "sort", "storage", "review"} {
			t.Run(fmt.Sprintf("%dx%d/%s", size.width, size.height, screen), func(t *testing.T) {
				d := &codexCleanupDialogState{Category: service.CodexCleanupStale, InactiveDays: 30, Chosen: map[string]bool{}}
				for i := 0; i < 40; i++ {
					g := codexCleanupTestGroup(time.Now())
					g.WorktreeName = strings.Repeat("日本語-project-", 10)
					g.WorktreePath = fmt.Sprintf("/tmp/%d", i)
					g.TotalThreadCount = 240
					g.RootThreadCount = 228
					g.DescendantCount = 2
					g.RecoverableBytes = 2 << 30
					d.Audit.Groups = append(d.Audit.Groups, g)
					d.Chosen[g.WorktreePath] = true
					d.Audit.Retained = append(d.Audit.Retained, service.CodexCleanupRetainedGroup{Name: g.WorktreeName, Path: g.WorktreePath, Reason: "Protected sessions", Bytes: 2 << 30})
				}
				switch screen {
				case "orphaned":
					d.Category = service.CodexCleanupOrphaned
				case "category":
					d.Dropdown = true
					d.Focus = cleanupFocusCategory
				case "age":
					d.Dropdown = true
					d.Focus = cleanupFocusAge
				case "sort":
					d.Dropdown = true
					d.Focus = cleanupFocusSort
				case "storage":
					d.ShowRetained = true
				case "review":
					d.Confirming = true
				}
				view := buildCodexCleanupView(d, size.width, size.height, 0, time.Now())
				panel := renderDialogPanel(size.width+2, size.width, view.text())
				if lipgloss.Height(panel) > size.height || lipgloss.Width(panel) > size.width+4 {
					t.Fatalf("overflow %dx%d:\n%s", lipgloss.Width(panel), lipgloss.Height(panel), ansi.Strip(panel))
				}
				for _, hit := range view.hits {
					if hit.y < 0 || hit.y >= len(view.lines) || hit.x < 0 || hit.x+hit.width > size.width {
						t.Fatalf("invalid hit target: %#v", hit)
					}
				}
				want := "Review cleanup →"
				if screen == "review" {
					want = "Delete 9200 sessions permanently"
				}
				if screen == "storage" {
					want = "Back to cleanup"
				}
				if !strings.Contains(ansi.Strip(view.text()), want) {
					t.Fatalf("missing %s", want)
				}
			})
		}
	}
}

func TestCodexCleanupMouseUsesVisibleControls(t *testing.T) {
	g := codexCleanupTestGroup(time.Now())
	d := &codexCleanupDialogState{Chosen: map[string]bool{}, Audit: service.CodexCleanupAudit{Groups: []service.CodexCleanupWorktreeGroup{g}}}
	m := Model{width: 120, height: 40, codexCleanup: d}
	click := func(focus codexCleanupFocus, row int) tea.Cmd {
		t.Helper()
		layout := m.bodyLayout()
		panelW := 112
		view := buildCodexCleanupView(d, 108, layout.height, 0, time.Now())
		panel := renderDialogPanel(panelW-2, 108, view.text())
		for _, hit := range view.hits {
			if hit.focus == focus && hit.row == row {
				updated, cmd := m.Update(tea.MouseMsg{X: (120-panelW)/2 + 2 + hit.x + 1, Y: 1 + (layout.height-lipgloss.Height(panel))/2 + 1 + hit.y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
				m = updated.(Model)
				return cmd
			}
		}
		t.Fatalf("missing control %d/%d", focus, row)
		return nil
	}
	if click(cleanupFocusTable, 0) != nil || !d.Chosen[g.WorktreePath] {
		t.Fatal("mouse row must select, not delete")
	}
	click(cleanupFocusStorage, -1)
	if !d.ShowRetained {
		t.Fatal("storage action did not open breakdown")
	}
	click(cleanupFocusCancel, -1)
	if d.ShowRetained || !d.Chosen[g.WorktreePath] {
		t.Fatal("Back must preserve selection")
	}
	click(cleanupFocusCategory, -1)
	if !d.Dropdown {
		t.Fatal("mouse must open dropdown")
	}
	click(cleanupFocusCategory, 0)
	if d.Dropdown || d.Loading {
		t.Fatal("choosing same option must close without rescanning")
	}
	if click(cleanupFocusReview, -1) != nil || !d.Confirming || d.ReviewDelete {
		t.Fatal("review must default to Back")
	}
	click(cleanupFocusCancel, -1)
	if d.Confirming || d.Deleting {
		t.Fatal("Back must leave review without deleting")
	}
}

func TestCodexCleanupFocusIsVisibleWithoutColor(t *testing.T) {
	previous := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	t.Cleanup(func() { lipgloss.SetColorProfile(previous) })
	text := codexCleanupControl("Stale sessions ▾", true, true, false)
	if !strings.Contains(text, "48;5;75") || !strings.Contains(ansi.Strip(text), "›[ Stale sessions ▾ ]") {
		t.Fatalf("focus must have both color and a visible cursor: %q", text)
	}
}

func TestCodexCleanupDropdownAndFocusNavigation(t *testing.T) {
	d := &codexCleanupDialogState{Focus: cleanupFocusCategory, Chosen: map[string]bool{"keep": true}}
	m := Model{codexCleanup: d}
	for _, key := range []string{"1", "2", "c", "v"} {
		_, cmd := m.updateCodexCleanupMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
		if cmd != nil || d.Loading || d.ShowRetained {
			t.Fatal("old ambiguous shortcuts must not change views")
		}
	}
	m.updateCodexCleanupMode(tea.KeyMsg{Type: tea.KeyEnter})
	if !d.Dropdown {
		t.Fatal("Enter should open cleanup dropdown")
	}
	m.updateCodexCleanupMode(tea.KeyMsg{Type: tea.KeyEsc})
	if d.Dropdown || m.codexCleanup == nil {
		t.Fatal("Esc should dismiss only the dropdown")
	}
	m.updateCodexCleanupMode(tea.KeyMsg{Type: tea.KeyEnter})
	_, cmd := m.updateCodexCleanupMode(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil || d.Loading || !d.Chosen["keep"] {
		t.Fatal("same policy must preserve selection")
	}
	m.updateCodexCleanupMode(tea.KeyMsg{Type: tea.KeyEnter})
	m.updateCodexCleanupMode(tea.KeyMsg{Type: tea.KeyDown})
	_, cmd = m.updateCodexCleanupMode(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil || !d.Loading || d.Category != service.CodexCleanupStale || len(d.Chosen) != 0 {
		t.Fatal("new policy must rescan and clear selection")
	}
	if d.AuditCancel != nil {
		d.AuditCancel()
	}
	d.Loading = false
	m.updateCodexCleanupMode(tea.KeyMsg{Type: tea.KeyTab})
	if d.Focus != cleanupFocusAge || d.ShowRetained {
		t.Fatal("Tab must move focus to age, not switch views")
	}
	m.updateCodexCleanupMode(tea.KeyMsg{Type: tea.KeyEnter})
	m.updateCodexCleanupMode(tea.KeyMsg{Type: tea.KeyDown})
	m.updateCodexCleanupMode(tea.KeyMsg{Type: tea.KeyDown})
	_, cmd = m.updateCodexCleanupMode(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil || !d.Loading || d.InactiveDays != 30 {
		t.Fatal("age dropdown must change the audit policy")
	}
	if d.AuditCancel != nil {
		d.AuditCancel()
	}
}

func TestCodexCleanupStaleCategoryUsesGroupedCountsAndConfirmation(t *testing.T) {
	m := Model{codexCleanup: &codexCleanupDialogState{Focus: cleanupFocusCategory, Dropdown: true, OptionIndex: 1, Chosen: map[string]bool{"old-selection": true}}}
	updated, cmd := m.updateCodexCleanupMode(tea.KeyMsg{Type: tea.KeyEnter})
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
	m.codexCleanup.Focus = cleanupFocusReview
	updated, cmd = m.updateCodexCleanupMode(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if cmd != nil || !m.codexCleanup.Confirming || m.codexCleanup.Deleting {
		t.Fatal("stale cleanup must require separate permanent confirmation")
	}
	view = ansi.Strip(renderCodexCleanupConfirmation(m.codexCleanup, 108))
	if !strings.Contains(view, "Remove 210 sessions · keep 30") || !strings.Contains(view, "Delete 210 sessions permanently") {
		t.Fatalf("confirmation lacks per-project counts: %s", view)
	}
}

func TestCodexCleanupRetainedViewCannotDelete(t *testing.T) {
	dialog := &codexCleanupDialogState{
		ShowRetained: true,
		Chosen:       map[string]bool{"/eligible": true},
		Audit: service.CodexCleanupAudit{
			Groups: []service.CodexCleanupWorktreeGroup{{WorktreePath: "/eligible"}},
			Retained: []service.CodexCleanupRetainedGroup{
				{Name: "kept-project", Path: "/kept-project", Bytes: 2 << 30, Files: 20, Reason: "No LCR deleted-worktree record"},
				{Name: "cache", Bytes: 2 << 20},
			},
		},
	}
	m := Model{codexCleanup: dialog}
	for _, key := range []string{"v", " ", "a", "enter", "d", "1", "2"} {
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
	for _, want := range []string{"Retained", "read-only", "kept-project", "2.0 GiB", "2.0 MiB", "No LCR deleted-worktree record"} {
		if !strings.Contains(view, want) {
			t.Fatalf("missing %q in %s", want, view)
		}
	}
	m.updateCodexCleanupMode(tea.KeyMsg{Type: tea.KeyEsc})
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
		dialog.Focus = cleanupFocusSort
		dialog.Dropdown = true
		dialog.OptionIndex = (dialog.SortMode + 1) % 3
		updated, cmd := m.updateCodexCleanupMode(tea.KeyMsg{Type: tea.KeyEnter})
		m = updated.(Model)
		if cmd != nil || dialog.Audit.Groups[0].WorktreePath != first || dialog.Audit.Groups[dialog.Selected].WorktreePath != "/b" || !dialog.Chosen["/b"] {
			t.Fatalf("sort lost ordering, focus or selection: %#v", dialog)
		}
	}
	view := ansi.Strip(renderCodexCleanupContent(dialog, 100, 45, 0, now))
	for _, column := range []string{"FREE UP", "REMOVE", "KEEP", "PROJECT / FOLDER", "largest first"} {
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
	for _, want := range []string{"Clean up Codex storage", "Orphaned worktrees", "demo--old-cleanup", "REMOVE", "KEEP", "FREE UP", "LCR-deleted"} {
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
	for _, want := range []string{"this cannot be undone", "Delete 3 sessions permanently", "spawned sessions", "Codex app-server"} {
		if !strings.Contains(confirmation, want) {
			t.Fatalf("permanent warning missing %q:\n%s", want, confirmation)
		}
	}
	updated, cmd = got.updateCodexCleanupMode(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if cmd != nil || got.codexCleanup.Deleting || got.codexCleanup.Confirming {
		t.Fatal("review must default to Back")
	}
	got.updateCodexCleanupMode(tea.KeyMsg{Type: tea.KeyEnter})
	got.updateCodexCleanupMode(tea.KeyMsg{Type: tea.KeyTab})
	updated, cmd = got.updateCodexCleanupMode(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if cmd == nil || !got.codexCleanup.Deleting || got.codexCleanup.QueueIndex != 0 {
		t.Fatalf("explicit delete button did not start one guarded deletion: %#v cmd=%v", got.codexCleanup, cmd)
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
	if !strings.Contains(progress, "Aborting Codex session cleanup") || !strings.Contains(progress, "no queued project group will start") {
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
	for _, want := range []string{"Cleanup aborted", "1 queued project group did not start", "2.0 KiB", "2 descendants"} {
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
