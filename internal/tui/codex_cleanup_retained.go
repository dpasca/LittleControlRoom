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
	lines := []string{
		commandPaletteTitleStyle.Render("Codex storage · retained"),
		detailField("Outside category", dialog.Category.Label()),
		detailField("Storage", summary),
	}
	lines = append(lines, renderWrappedDialogTextLines(detailMutedStyle, width,
		"Session files are grouped by their indexed project / working directory. Other Codex files and unknown ownership are listed separately. Sizes are logical bytes.")...)
	lines = append(lines, "", commandPaletteHintStyle.Render(fmt.Sprintf("%10s  %7s  %s", "SIZE", "FILES", "PROJECT / WORKING DIRECTORY")))
	start, end := cleanupGroupWindow(dialog.RetainedIndex, len(groups), bodyH+9)
	if start > 0 {
		lines = append(lines, detailMutedStyle.Render(fmt.Sprintf("↑ %d more groups", start)))
	}
	for index := start; index < end; index++ {
		group := groups[index]
		style := commandPaletteRowStyle
		if index == dialog.RetainedIndex {
			style = commandPaletteSelectStyle
		}
		size := style.Bold(true).Foreground(lipgloss.Color("42")).Render(fmt.Sprintf("%10s", formatCodexCleanupBytes(group.Bytes)))
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
		renderDialogAction("V / Tab", "eligible cleanup", navigateActionKeyStyle, navigateActionTextStyle)+"   "+
			renderDialogAction("R", "audit again", navigateActionKeyStyle, navigateActionTextStyle)+"   "+
			renderDialogAction("Esc", "close", cancelActionKeyStyle, cancelActionTextStyle))
	return clampDialogContent(strings.Join(lines, "\n"), max(12, bodyH-4), 6, detailMutedStyle.Render("… details clipped to fit terminal …"))
}
