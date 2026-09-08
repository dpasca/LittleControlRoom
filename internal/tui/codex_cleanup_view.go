package tui

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"lcroom/internal/service"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// Render and hit testing share a layout made only from the cached audit.
type codexCleanupHit struct {
	x, y, width int
	focus       codexCleanupFocus
	row         int // -1 for a control, otherwise a table row or dropdown option.
}

type codexCleanupView struct {
	lines []string
	hits  []codexCleanupHit
}

func (v codexCleanupView) text() string         { return strings.Join(v.lines, "\n") }
func (v *codexCleanupView) add(lines ...string) { v.lines = append(v.lines, lines...) }
func (v *codexCleanupView) control(label string, focus codexCleanupFocus, d *codexCleanupDialogState, enabled bool) string {
	text := codexCleanupControl(label, d.Focus == focus && !d.Loading, enabled && !d.Loading, false)
	if enabled && !d.Loading {
		v.hits = append(v.hits, codexCleanupHit{x: lipgloss.Width(v.lines[len(v.lines)-1]), y: len(v.lines) - 1, width: lipgloss.Width(text), focus: focus, row: -1})
	}
	v.lines[len(v.lines)-1] += text
	return text
}
func (v *codexCleanupView) appendText(text string) { v.lines[len(v.lines)-1] += text }

func cleanupStrong(text string) string {
	return lipgloss.NewStyle().Bold(true).Foreground(codexCleanupAccent(nil)).Render(text)
}

func padRight(text string, width int) string {
	return text + strings.Repeat(" ", max(0, width-lipgloss.Width(text)))
}

func cleanupCellText(text string, width int) string {
	text = ansi.Strip(text)
	text = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, text)
	return ansi.Truncate(text, max(0, width), "…")
}

func buildCodexCleanupView(d *codexCleanupDialogState, width, bodyH, spinnerFrame int, now time.Time) codexCleanupView {
	if d.Confirming {
		return buildCodexCleanupReview(d, width, bodyH)
	}
	if d.ShowRetained && !d.Loading {
		return buildCodexCleanupStorage(d, width, bodyH)
	}
	v := codexCleanupView{}
	compact := bodyH < 30
	gap := func() {
		if !compact {
			v.add("")
		}
	}
	v.add(commandPaletteTitleStyle.Render("Clean up Codex storage"))
	gap()
	used := formatCodexCleanupBytes(d.Audit.Storage.TotalBytes) + " used"
	if d.Audit.Storage.Partial {
		used += " · partial inventory"
	}
	if d.Loading {
		used = "Measuring storage…"
	}
	v.add(cleanupStrong(used))
	storageLabel := "Storage breakdown ›"
	space := max(1, width-lipgloss.Width(v.lines[len(v.lines)-1])-len(storageLabel)-6)
	v.appendText(strings.Repeat(" ", space))
	v.control(storageLabel, cleanupFocusStorage, d, d.ErrorMessage == "")
	gap()
	v.add("Clean up: ")
	v.control(d.Category.Label()+" ▾", cleanupFocusCategory, d, d.ErrorMessage == "")
	if d.Category == service.CodexCleanupStale {
		ageText := fmt.Sprintf("%d days ▾", d.days())
		if lipgloss.Width(v.lines[len(v.lines)-1])+len(ageText)+22 > width {
			v.add("Inactive for: ")
		} else {
			v.appendText("   Inactive for: ")
		}
		v.control(ageText, cleanupFocusAge, d, d.ErrorMessage == "")
	}
	if d.Dropdown && d.Focus != cleanupFocusSort {
		v.menu(d, width)
	}
	gap()
	if d.Loading {
		v.add(detailMutedStyle.Render("Read-only audit · checking activity, pins and session trees."), "",
			cleanupStrong(spinnerFrames[spinnerFrame%len(spinnerFrames)]+" Scanning… no sessions are being deleted."), "",
			detailMutedStyle.Render("Esc cancel scan"))
		return v
	}
	if d.ErrorMessage != "" {
		v.add(detailDangerStyle.Render("Could not audit Codex storage"))
		v.add(renderWrappedDialogTextLines(detailMutedStyle, width, d.ErrorMessage)...)
		v.add("")
		v.control("Retry audit", cleanupFocusRefresh, d, true)
		v.appendText("  ")
		v.control("Cancel", cleanupFocusCancel, d, true)
		return v
	}
	description := "Safest cleanup: conversations from LCR-deleted worktrees, missing and inactive for 7+ days."
	protection := "Pinned, loaded and uncertain trees are kept. Project files are never removed."
	if d.Category == service.CodexCleanupStale {
		description = fmt.Sprintf("Existing projects · no session activity in the past %d days.", d.days())
		protection = "Newest per folder, pinned, loaded and uncertain trees are kept. Project files stay untouched."
	}
	if compact {
		description = "LCR-deleted worktrees · missing and inactive for 7+ days."
		protection = "Pinned, loaded and uncertain trees are kept."
		if d.Category == service.CodexCleanupStale {
			description = fmt.Sprintf("Existing projects · inactive for %d+ days.", d.days())
			protection = "Newest per folder, pinned, loaded & uncertain trees are kept."
		}
	}
	v.add(renderWrappedDialogTextLines(detailMutedStyle, width, description)...)
	v.add(renderWrappedDialogTextLines(detailMutedStyle, width, protection)...)
	gap()
	v.add("")
	v.control(codexCleanupSelectAllLabel(d), cleanupFocusAll, d, len(d.Audit.Groups) > 0)
	v.appendText("   Sort: ")
	v.control(codexCleanupSortLabel(d.SortMode)+" ▾", cleanupFocusSort, d, true)
	if d.Dropdown && d.Focus == cleanupFocusSort {
		v.menu(d, width)
	}
	gap()
	nameW := max(6, width-32)
	v.add(detailMutedStyle.Render(fmt.Sprintf("    %-*s  %6s  %6s  %10s", nameW, "PROJECT / FOLDER", "REMOVE", "KEEP", "FREE UP")))
	// Reserve a fixed footer so navigation and the primary action never scroll away.
	available := max(1, bodyH-4-len(v.lines)-8)
	groups := d.Audit.Groups
	start, end := cleanupVisibleRange(d.Selected, len(groups), available)
	for i := start; i < end; i++ {
		g := groups[i]
		mark := "[ ]"
		if d.Chosen[g.WorktreePath] {
			mark = "[x]"
		}
		name := firstNonEmptyString(g.WorktreeName, filepath.Base(g.WorktreePath))
		remove, keep := cleanupSessionCounts(g)
		style := commandPaletteRowStyle
		cursor := " "
		if i == d.Selected && d.Focus == cleanupFocusTable && !d.Dropdown {
			style = lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Background(lipgloss.Color("24"))
			cursor = "›"
		}
		row := style.Render(cursor+mark+" "+padRight(cleanupCellText(name, nameW-1), nameW-1)+fmt.Sprintf("  %6d  %6d  ", remove, keep)) +
			style.Bold(true).Foreground(codexCleanupAccent(d)).Render(fmt.Sprintf("%10s", formatCodexCleanupBytes(g.RecoverableBytes)))
		v.hits = append(v.hits, codexCleanupHit{x: 0, y: len(v.lines), width: width, focus: cleanupFocusTable, row: i})
		v.add(row)
	}
	if len(groups) == 0 {
		v.add(detailMutedStyle.Render("No eligible sessions for this cleanup."))
		v.add(cleanupCellText(cleanupExclusionSummary(d.Audit.Excluded), width))
	} else {
		v.add(detailMutedStyle.Render(fmt.Sprintf("Projects %d–%d of %d · counts include spawned sessions", start+1, end, len(groups))))
		g := groups[min(d.Selected, len(groups)-1)]
		remove, keep := cleanupSessionCounts(g)
		v.add(detailMutedStyle.Render(cleanupCellText(g.WorktreePath, width)))
		v.add(detailMutedStyle.Render(fmt.Sprintf("Remove %d of %d · keep %d · last activity %s", remove, max(g.TotalThreadCount, remove), keep, formatCleanupAge(now, g.LastActivity))))
	}
	selected := selectedCodexCleanupGroups(d)
	bytes, roots, children := codexCleanupGroupTotals(selected)
	summary := "Nothing selected · choose project rows to clean up"
	if len(selected) > 0 {
		summary = fmt.Sprintf("%d project%s selected · %d sessions · %s to free up", len(selected), pluralSuffix(len(selected)), roots+children, formatCodexCleanupBytes(bytes))
	}
	v.add("", cleanupStrong(summary), "")
	v.control("Cancel", cleanupFocusCancel, d, true)
	v.appendText("  ")
	v.control("Rescan", cleanupFocusRefresh, d, true)
	v.appendText(strings.Repeat(" ", max(1, width-lipgloss.Width(v.lines[len(v.lines)-1])-25)))
	v.control("Review cleanup →", cleanupFocusReview, d, len(selected) > 0)
	hint := "Tab focus · ↑↓ move · Space select · Enter activate · Esc back"
	if d.Dropdown {
		hint = "↑↓ choose option · Enter apply · Esc cancel"
	}
	v.add(detailMutedStyle.Render(hint))
	return v
}

func (v *codexCleanupView) menu(d *codexCleanupDialogState, width int) {
	menuW, left := 18, 0
	for _, option := range d.options() {
		menuW = max(menuW, lipgloss.Width(option)+5)
	}
	menuW = min(width, menuW)
	for _, hit := range v.hits {
		if hit.focus == d.Focus && hit.row == -1 {
			left = min(hit.x, width-menuW)
		}
	}
	for i, option := range d.options() {
		label := "   " + option
		style := lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Background(lipgloss.Color("236"))
		if i == d.OptionIndex {
			label = " › " + option
			style = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("232")).Background(codexCleanupAccent(d))
		}
		v.hits = append(v.hits, codexCleanupHit{x: left, y: len(v.lines), width: menuW, focus: d.Focus, row: i})
		v.add(strings.Repeat(" ", left) + style.Render(padRight(label, menuW)))
	}
}

func cleanupVisibleRange(selected, total, visible int) (int, int) {
	visible = max(1, visible)
	start := max(0, min(selected-visible/2, total-visible))
	return start, min(total, start+visible)
}

func cleanupSessionCounts(g service.CodexCleanupWorktreeGroup) (int, int) {
	remove := g.RootThreadCount + g.DescendantCount
	return remove, max(0, g.TotalThreadCount-remove)
}

func buildCodexCleanupReview(d *codexCleanupDialogState, width, bodyH int) codexCleanupView {
	groups := selectedCodexCleanupGroups(d)
	bytes, roots, children := codexCleanupGroupTotals(groups)
	keep := 0
	for _, g := range groups {
		_, n := cleanupSessionCounts(g)
		keep += n
	}
	v := codexCleanupView{}
	v.add(commandPaletteTitleStyle.Render("Review cleanup"), "", detailDangerStyle.Render("Permanent deletion · this cannot be undone"), "")
	policy := d.Category.Label()
	if d.Category == service.CodexCleanupStale {
		policy += fmt.Sprintf(" · inactive for %d+ days", d.days())
	}
	v.add(detailMutedStyle.Render(policy), cleanupStrong(fmt.Sprintf("Remove %d sessions · keep %d · free up %s", roots+children, keep, formatCodexCleanupBytes(bytes))), "")
	nameW := max(8, width-32)
	v.add(detailMutedStyle.Render(fmt.Sprintf("%-*s  %6s  %6s  %10s", nameW, "PROJECT / FOLDER", "REMOVE", "KEEP", "FREE UP")))
	shown := min(len(groups), max(1, min(6, bodyH-20)))
	for _, g := range groups[:shown] {
		remove, kept := cleanupSessionCounts(g)
		v.add(fmt.Sprintf("%s  %6d  %6d  %10s", padRight(cleanupCellText(firstNonEmptyString(g.WorktreeName, filepath.Base(g.WorktreePath)), nameW), nameW), remove, kept, formatCodexCleanupBytes(g.RecoverableBytes)))
	}
	if shown < len(groups) {
		v.add(detailMutedStyle.Render(fmt.Sprintf("… and %d more selected project groups", len(groups)-shown)))
	}
	v.add("")
	v.add(renderWrappedDialogTextLines(detailMutedStyle, width, "Only conversation history is deleted, including spawned sessions. Project files are untouched.")...)
	v.add(renderWrappedDialogTextLines(detailMutedStyle, width, "Codex app-server performs deletion after a fresh safety audit; changed previews are rejected.")...)
	v.add("")
	back := codexCleanupControl("Back", !d.ReviewDelete, true, false)
	deleteButton := codexCleanupControl(fmt.Sprintf("Delete %d sessions permanently", roots+children), d.ReviewDelete, true, true)
	v.hits = append(v.hits, codexCleanupHit{x: 0, y: len(v.lines), width: lipgloss.Width(back), focus: cleanupFocusCancel, row: -1})
	v.add(back)
	if lipgloss.Width(back)+lipgloss.Width(deleteButton)+3 > width {
		v.add("")
	} else {
		v.appendText("   ")
	}
	v.hits = append(v.hits, codexCleanupHit{x: lipgloss.Width(v.lines[len(v.lines)-1]), y: len(v.lines) - 1, width: lipgloss.Width(deleteButton), focus: cleanupFocusReview, row: -1})
	v.appendText(deleteButton)
	v.add(detailMutedStyle.Render("Tab / ←→ choose · Enter activate · Esc back"))
	return v
}
