package tui

import (
	"fmt"

	"lcroom/internal/service"

	"github.com/charmbracelet/lipgloss"
)

func buildCodexCleanupStorage(d *codexCleanupDialogState, width, bodyH int) codexCleanupView {
	v := codexCleanupView{}
	storage := d.Audit.Storage
	groups := d.Audit.Retained
	var retained int64
	for _, g := range groups {
		retained += g.Bytes
	}
	v.add(commandPaletteTitleStyle.Render("Storage breakdown"), "",
		cleanupStrong(formatCodexCleanupBytes(storage.TotalBytes)+" used in Codex storage"),
		detailMutedStyle.Render(fmt.Sprintf("%s sessions · %s other files · logical sizes", formatCodexCleanupBytes(storage.SessionBytes), formatCodexCleanupBytes(storage.TotalBytes-storage.SessionBytes))), "")
	v.add(cleanupStrong(fmt.Sprintf("%s eligible for this cleanup · %s outside it", formatCodexCleanupBytes(d.Audit.RecoverableBytes), formatCodexCleanupBytes(retained))))
	policy := d.Category.Label()
	if d.Category == service.CodexCleanupStale {
		policy += fmt.Sprintf(" · %d+ days", d.days())
	}
	v.add(detailMutedStyle.Render("Current policy: " + policy))
	v.add(renderWrappedDialogTextLines(detailMutedStyle, width, "Outside this cleanup: protected or ineligible sessions, other projects, and non-session files. This view is read-only; nothing here is selected for deletion.")...)
	if storage.Partial {
		v.add(detailWarningStyle.Render("Partial inventory · some files could not be measured."))
	}
	v.add("")
	nameW := max(8, width-23)
	v.add(detailMutedStyle.Render(fmt.Sprintf("  %-*s  %7s  %10s", nameW, "PROJECT / STORAGE", "FILES", "SIZE")))
	start, end := cleanupVisibleRange(d.RetainedIndex, len(groups), max(1, bodyH-4-len(v.lines)-8))
	for i := start; i < end; i++ {
		g := groups[i]
		style := commandPaletteRowStyle
		mark := "  "
		if i == d.RetainedIndex && d.Focus == cleanupFocusTable {
			style = lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Background(lipgloss.Color("24"))
			mark = "› "
		}
		v.hits = append(v.hits, codexCleanupHit{x: 0, y: len(v.lines), width: width, focus: cleanupFocusTable, row: i})
		v.add(style.Render(mark+padRight(cleanupCellText(g.Name, nameW), nameW)+fmt.Sprintf("  %7d  ", g.Files)) + style.Bold(true).Foreground(codexCleanupAccent(d)).Render(fmt.Sprintf("%10s", formatCodexCleanupBytes(g.Bytes))))
	}
	if len(groups) > 0 {
		g := groups[min(d.RetainedIndex, len(groups)-1)]
		v.add(detailMutedStyle.Render(fmt.Sprintf("Groups %d–%d of %d · largest first", start+1, end, len(groups))), "")
		for _, text := range []string{g.Path, "Project: " + firstNonEmptyString(g.ProjectPath, "Not attributed to a project"), "Retained: " + g.Reason} {
			v.add(detailMutedStyle.Render(cleanupCellText(text, width)))
		}
	} else {
		v.add(detailMutedStyle.Render("No retained files in this inventory."))
	}
	v.add("")
	v.control("Back to cleanup", cleanupFocusCancel, d, true)
	v.add(detailMutedStyle.Render("↑↓ browse · Tab focus · Enter activate · Esc back"))
	return v
}
