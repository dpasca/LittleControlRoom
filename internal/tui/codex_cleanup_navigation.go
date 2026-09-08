package tui

import (
	"lcroom/internal/service"

	"github.com/charmbracelet/lipgloss"
)

func codexCleanupAccent(dialog *codexCleanupDialogState) lipgloss.Color {
	if dialog.ShowRetained {
		return lipgloss.Color("75")
	}
	if dialog.Category == service.CodexCleanupStale {
		return lipgloss.Color("214")
	}
	return lipgloss.Color("42")
}

func renderCodexCleanupTab(label string, active bool, color lipgloss.Color) string {
	if active {
		return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("232")).Background(color).Render("[ " + label + " ]")
	}
	return detailMutedStyle.Render("  " + label + "  ")
}

func renderCodexCleanupNavigation(dialog *codexCleanupDialogState, width int) []string {
	stale := dialog.Category == service.CodexCleanupStale
	lines := []string{commandPaletteTitleStyle.Render("Clean Codex session storage"), ""}
	category := detailMutedStyle.Render("Clean up  ") +
		renderCodexCleanupTab("1 Orphaned worktrees", !stale, lipgloss.Color("42")) + " " +
		renderCodexCleanupTab("2 Stale sessions", stale, lipgloss.Color("214"))
	view := detailMutedStyle.Render("View      ") +
		renderCodexCleanupTab("Cleanup candidates", !dialog.ShowRetained, codexCleanupAccent(dialog)) + " " +
		renderCodexCleanupTab("Other storage", dialog.ShowRetained, lipgloss.Color("75"))
	lines = append(lines, renderWrappedDialogTextLines(lipgloss.NewStyle(), width, category)...)
	lines = append(lines, renderWrappedDialogTextLines(lipgloss.NewStyle(), width, view)...)
	hint := "Tab: show other storage (read-only)"
	if dialog.ShowRetained {
		hint = "Tab: return to cleanup candidates"
	}
	if dialog.Loading {
		hint = "Scanning… navigation available when the audit completes"
	}
	lines = append(lines, detailMutedStyle.Render(hint), "")
	return lines
}
