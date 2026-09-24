package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Alt+L opens a small picker for the embedded transcript detail level instead
// of cycling blindly through four states.

type codexDetailPickerState struct {
	Selected int
}

type codexDetailLevel struct {
	Mode    codexDenseBlockMode
	Label   string
	Summary string
}

func codexDetailLevels() []codexDetailLevel {
	return []codexDetailLevel{
		{
			Mode:    codexDenseBlockNarrative,
			Label:   "Steps",
			Summary: "Engineer messages with one activity line per step: files edited, test and check results, searches, and failures. Commands stay hidden.",
		},
		{
			Mode:    codexDenseBlockSummary,
			Label:   "Tool calls",
			Summary: "One line per tool call and command, with command output hidden.",
		},
		{
			Mode:    codexDenseBlockPreview,
			Label:   "Previews",
			Summary: "Each tool call with the first lines of its output.",
		},
		{
			Mode:    codexDenseBlockFull,
			Label:   "Full output",
			Summary: "Every command, file change, reasoning block, and repeated entry in full.",
		},
	}
}

func codexDetailLevelIndex(mode codexDenseBlockMode) int {
	mode = mode.normalized()
	for i, level := range codexDetailLevels() {
		if level.Mode == mode {
			return i
		}
	}
	return 0
}

func (m *Model) openCodexDetailPicker() {
	m.codexDetailPicker = &codexDetailPickerState{Selected: codexDetailLevelIndex(m.codexDenseBlockMode)}
	m.status = "Choose transcript detail"
}

func (m *Model) closeCodexDetailPicker(status string) {
	m.codexDetailPicker = nil
	if strings.TrimSpace(status) != "" {
		m.status = status
	}
}

func (m Model) updateCodexDetailPickerMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	picker := m.codexDetailPicker
	if picker == nil {
		return m, nil
	}
	levels := codexDetailLevels()
	switch key := msg.String(); key {
	case "esc", "ctrl+c":
		m.closeCodexDetailPicker("Transcript detail unchanged")
		return m, nil
	case "up", "k", "shift+tab":
		picker.Selected = wrapIndex(picker.Selected-1, len(levels))
		return m, nil
	case "down", "j", "tab", "alt+l":
		picker.Selected = wrapIndex(picker.Selected+1, len(levels))
		return m, nil
	case "enter":
		return m.applyCodexDetailLevel(levels[wrapIndex(picker.Selected, len(levels))])
	case "1", "2", "3", "4":
		index := int(key[0] - '1')
		if index < len(levels) {
			return m.applyCodexDetailLevel(levels[index])
		}
	}
	return m, nil
}

func (m Model) applyCodexDetailLevel(level codexDetailLevel) (tea.Model, tea.Cmd) {
	m.codexDetailPicker = nil
	m.codexDenseBlockMode = level.Mode
	m.status = level.Mode.statusText()
	m.syncCodexViewport(false)
	return m, m.requestVisibleCodexTranscriptRenderCmd()
}

func (m Model) renderCodexDetailPickerOverlay(body string, bodyW, bodyH int) string {
	panel := m.renderCodexDetailPicker(bodyW)
	panelW := lipgloss.Width(panel)
	panelH := lipgloss.Height(panel)
	left := max(0, (bodyW-panelW)/2)
	top := max(0, min((bodyH-panelH)/4, bodyH-panelH))
	return overlayBlock(body, panel, bodyW, bodyH, left, top)
}

func (m Model) renderCodexDetailPicker(bodyW int) string {
	panelW := min(bodyW, min(max(52, bodyW-18), 72))
	panelInnerW := max(28, panelW-4)
	return renderDialogPanel(panelW, panelInnerW, m.renderCodexDetailPickerContent(panelInnerW))
}

func (m Model) renderCodexDetailPickerContent(width int) string {
	picker := m.codexDetailPicker
	if picker == nil {
		return ""
	}
	levels := codexDetailLevels()
	selected := wrapIndex(picker.Selected, len(levels))
	current := codexDetailLevelIndex(m.codexDenseBlockMode)
	lines := []string{
		renderDialogHeader("Transcript detail", "", "", width),
		"",
	}
	for i, level := range levels {
		lines = append(lines, renderCodexDetailPickerRow(i, level, i == selected, i == current, width))
	}
	lines = append(lines, "", detailSectionStyle.Render("About"))
	lines = append(lines, renderWrappedDialogTextLines(detailMutedStyle, width, levels[selected].Summary)...)
	lines = append(lines, "", strings.Join([]string{
		renderDialogAction("Enter", "choose", commitActionKeyStyle, commitActionTextStyle),
		renderDialogAction("1-4", "pick", navigateActionKeyStyle, navigateActionTextStyle),
		renderDialogAction("↑↓", "move", navigateActionKeyStyle, navigateActionTextStyle),
		renderDialogAction("Esc", "cancel", cancelActionKeyStyle, cancelActionTextStyle),
	}, "   "))
	return strings.Join(lines, "\n")
}

func renderCodexDetailPickerRow(index int, level codexDetailLevel, selected, current bool, width int) string {
	labelStyle := detailValueStyle
	markerStyle := commandPaletteHintStyle
	marker := " "
	if selected {
		labelStyle = commandPalettePickStyle
		markerStyle = commandPalettePickStyle
		marker = ">"
	}
	row := markerStyle.Render(marker) + " " + commandPaletteHintStyle.Render(fmt.Sprintf("%d", index+1)) + "  " + labelStyle.Render(level.Label)
	if current {
		row += "  " + detailMutedStyle.Render("(current)")
	}
	row = fitFooterWidth(row, width)
	if selected {
		return dialogSelectedRowStyle.Width(width).Render(row)
	}
	return lipgloss.NewStyle().Width(width).Render(row)
}
