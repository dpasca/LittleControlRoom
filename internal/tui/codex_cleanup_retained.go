package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

func renderCodexCleanupRetained(dialog *codexCleanupDialogState, width, bodyH int) string {
	groups := dialog.Audit.Retained
	var total int64
	for _, group := range groups {
		total += group.Bytes
	}
	summary := fmt.Sprintf("%s retained · %d groups · largest first · read-only", formatCodexCleanupBytes(total), len(groups))
	if dialog.Audit.Storage.Partial {
		summary += " · partial inventory"
	}
	lines := renderCodexCleanupNavigation(dialog, width)
	lines = append(lines, lipgloss.NewStyle().Bold(true).Foreground(codexCleanupAccent(dialog)).Render("READ-ONLY · Nothing in this view can be selected for deletion"))
	lines = append(lines, renderWrappedDialogTextLines(detailMutedStyle, width,
		"Storage outside "+dialog.Category.Label()+" cleanup: protected sessions, other projects, and non-session files. Sizes are logical bytes.")...)
	lines = append(lines, detailField("Storage", summary), detailMutedStyle.Render("↑/↓ browse projects and files"))
	lines = append(lines, "", commandPaletteHintStyle.Render(fmt.Sprintf("%10s  %7s  %s", "SIZE", "FILES", "PROJECT / WORKING DIRECTORY")))
	start, end := cleanupGroupWindow(dialog.RetainedIndex, len(groups), bodyH+3)
	if start > 0 {
		lines = append(lines, detailMutedStyle.Render(fmt.Sprintf("↑ %d more groups", start)))
	}
	for index := start; index < end; index++ {
		group := groups[index]
		style := commandPaletteRowStyle
		if index == dialog.RetainedIndex {
			style = commandPaletteSelectStyle
		}
		size := style.Bold(true).Foreground(codexCleanupAccent(dialog)).Render(fmt.Sprintf("%10s", formatCodexCleanupBytes(group.Bytes)))
		lines = append(lines, size+style.Render(fmt.Sprintf("  %7d  %s", group.Files, truncateText(group.Name, max(8, width-21)))))
	}
	if end < len(groups) {
		lines = append(lines, detailMutedStyle.Render(fmt.Sprintf("↓ %d more groups", len(groups)-end)))
	}
	if len(groups) > 0 {
		group := groups[max(0, min(dialog.RetainedIndex, len(groups)-1))]
		lines = append(lines, "")
		for _, field := range []struct{ label, value string }{
			{"Path", firstNonEmptyString(group.Path, "Unknown")},
			{"Project", firstNonEmptyString(group.ProjectPath, "Not attributed to a project")},
			{"Retained", group.Reason},
		} {
			lines = append(lines, renderWrappedDialogTextLines(detailMutedStyle, width, field.label+": "+field.value)...)
		}
	} else {
		lines = append(lines, detailMutedStyle.Render("No retained files found in this inventory."))
	}
	lines = append(lines, "",
		renderDialogAction("R", "rescan", navigateActionKeyStyle, navigateActionTextStyle)+"   "+
			renderDialogAction("Esc", "close", cancelActionKeyStyle, cancelActionTextStyle))
	return clampDialogContent(strings.Join(lines, "\n"), max(12, bodyH-4), 6, detailMutedStyle.Render("… details clipped to fit terminal …"))
}
