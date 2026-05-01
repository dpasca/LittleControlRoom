package tui

import (
	"strings"

	"lcroom/internal/inputcomposer"

	"github.com/charmbracelet/lipgloss"
)

func (m Model) renderCodexInputCopyDialogOverlay(body string, bodyW, bodyH int) string {
	panel := m.renderCodexInputCopyDialog(bodyW)
	panelW := lipgloss.Width(panel)
	panelH := lipgloss.Height(panel)
	left := max(0, (bodyW-panelW)/2)
	top := max(0, min((bodyH-panelH)/4, bodyH-panelH))
	return overlayBlock(body, panel, bodyW, bodyH, left, top)
}

func (m Model) renderCodexInputCopyDialog(bodyW int) string {
	panelW := min(bodyW, min(max(54, bodyW-12), 76))
	panelInnerW := max(28, panelW-4)
	return renderDialogPanel(panelW, panelInnerW, m.renderCodexInputCopyDialogContent(panelInnerW))
}

func (m Model) renderCodexInputCopyDialogContent(width int) string {
	dialog := m.codexInputCopyDialog
	if dialog == nil {
		return ""
	}
	lines := []string{
		renderDialogHeader("Copy", "", "", width),
		commandPaletteHintStyle.Render("Choose what to put on the clipboard."),
		"",
	}
	buttons := make([]string, 0, len(inputcomposer.CopyChoices()))
	for _, choice := range inputcomposer.CopyChoices() {
		buttons = append(buttons, renderDialogButton(inputcomposer.CopyChoiceLabel(choice), dialog.Selected == choice))
	}
	lines = append(lines, lipgloss.JoinHorizontal(lipgloss.Left, buttons...))
	lines = append(lines, "")
	lines = append(lines, renderWrappedDialogTextLines(detailMutedStyle, width, inputcomposer.CopyChoiceSummary(dialog.Selected))...)
	lines = append(lines, "")
	lines = append(lines, strings.Join([]string{
		renderDialogAction("Enter", "choose", commitActionKeyStyle, commitActionTextStyle),
		renderDialogAction("Tab/↑↓", "switch", navigateActionKeyStyle, navigateActionTextStyle),
		renderDialogAction("Esc", "cancel", cancelActionKeyStyle, cancelActionTextStyle),
	}, "   "))
	return strings.Join(lines, "\n")
}
