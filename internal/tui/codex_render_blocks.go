package tui

import (
	"fmt"
	"strings"

	"lcroom/internal/terminalmd"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/lipgloss"
)

func renderCodexMessageBlock(label, body string, accent, bodyColor lipgloss.Color, width int) string {
	return renderCodexMessageBlockWithStyle(label, body, accent, bodyColor, width, false)
}

func renderCodexCompactTranscriptLine(body string, accent lipgloss.Color, width int) string {
	return renderCodexMessageBlockWithStyle("", body, accent, accent, width, false)
}

func renderCodexUserMessageBlock(body string, width int) string {
	return renderCodexMessageBlockWithStyle("", body, lipgloss.Color("81"), lipgloss.Color("252"), width, true)
}

func renderCodexMessageBlockWithStyle(label, body string, accent, bodyColor lipgloss.Color, width int, shaded bool) string {
	paddingRight := 0
	style := lipgloss.NewStyle().
		BorderLeft(true).
		BorderForeground(accent).
		PaddingLeft(1)
	if shaded {
		paddingRight = 1
		style = style.PaddingRight(1).Background(codexComposerShellColor)
	}
	contentWidth := max(10, width-2-paddingRight)
	lines := []string{}
	if strings.TrimSpace(label) != "" {
		lines = append(lines, lipgloss.NewStyle().Bold(true).Foreground(accent).Render(label))
	}
	lines = append(lines, renderCodexBody(body, bodyColor, contentWidth))
	return style.Render(strings.Join(lines, "\n"))
}

var reasoningBackgroundColor = lipgloss.Color("235")

func renderReasoningBlock(body string, width int) string {
	contentWidth := max(10, width-4)
	label := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("180")).Faint(true).Render("Reasoning")
	bodyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Width(contentWidth)
	wrappedBody := bodyStyle.Render(renderCodexBody(body, lipgloss.Color("252"), contentWidth))
	return lipgloss.NewStyle().
		BorderLeft(true).
		BorderForeground(lipgloss.Color("180")).
		PaddingLeft(0).
		PaddingRight(1).
		Background(reasoningBackgroundColor).
		Render(label + "\n" + wrappedBody)
}

// renderReasoningIndicator renders a compact single-line indicator for hidden
// reasoning content instead of showing nothing (which causes visible content flashes
// as reasoning entries appear and disappear during streaming).
func renderReasoningIndicator(lineCount int, width int) string {
	accent := lipgloss.Color("180")
	label := lipgloss.NewStyle().Foreground(accent).Faint(true).Render("Thinking…")
	plural := "lines"
	if lineCount == 1 {
		plural = "line"
	}
	detail := lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Render(
		fmt.Sprintf(" (%d %s, Alt+L expands)", lineCount, plural))
	return lipgloss.NewStyle().
		BorderLeft(true).
		BorderForeground(accent).
		PaddingLeft(0).
		Width(width).
		Render(label + detail)
}

func renderCodexComposer(input textarea.Model, width int) string {
	if width <= 0 {
		width = input.Width() + 4
	}
	return lipgloss.NewStyle().
		Width(width).
		Padding(0, 1).
		Background(codexComposerShellColor).
		Foreground(lipgloss.Color("252")).
		Render(input.View())
}

func renderCodexMonospaceBlock(label, body string, accent lipgloss.Color, width int) string {
	contentWidth := max(10, width-2)
	title := lipgloss.NewStyle().Bold(true).Foreground(accent).Render(label)
	renderedLines := make([]string, 0, len(strings.Split(body, "\n")))
	for _, line := range strings.Split(body, "\n") {
		switch {
		case strings.HasPrefix(line, "$ "):
			renderedLines = append(renderedLines, lipgloss.NewStyle().Foreground(lipgloss.Color("81")).Bold(true).Render(line))
		case strings.HasPrefix(line, "diff --git "), strings.HasPrefix(line, "index "):
			renderedLines = append(renderedLines, lipgloss.NewStyle().Foreground(lipgloss.Color("111")).Bold(true).Render(line))
		case strings.HasPrefix(line, "@@"):
			renderedLines = append(renderedLines, lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true).Render(line))
		case strings.HasPrefix(line, "+++"), strings.HasPrefix(line, "+"):
			renderedLines = append(renderedLines, lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Render(line))
		case strings.HasPrefix(line, "---"), strings.HasPrefix(line, "-"):
			renderedLines = append(renderedLines, lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Render(line))
		case strings.HasPrefix(line, "# "):
			renderedLines = append(renderedLines, lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Faint(true).Render(line))
		case strings.HasPrefix(line, "[command ") && !strings.Contains(line, "exit 0]"):
			// Non-zero exit — render as warning
			renderedLines = append(renderedLines, lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Bold(true).Render(line))
		case strings.HasPrefix(line, "[command "):
			renderedLines = append(renderedLines, lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Faint(true).Render(line))
		case strings.HasPrefix(line, "[file changes "):
			renderedLines = append(renderedLines, lipgloss.NewStyle().Foreground(lipgloss.Color("149")).Bold(true).Render(line))
		default:
			renderedLines = append(renderedLines, lipgloss.NewStyle().Foreground(lipgloss.Color("250")).Render(line))
		}
	}
	bodyText := strings.Join(renderedLines, "\n")
	if strings.TrimSpace(bodyText) == "" {
		return lipgloss.NewStyle().
			BorderLeft(true).
			BorderForeground(accent).
			PaddingLeft(0).
			Render(title)
	}
	bodyBlock := lipgloss.NewStyle().Width(contentWidth).Render(bodyText)
	return lipgloss.NewStyle().
		BorderLeft(true).
		BorderForeground(accent).
		PaddingLeft(0).
		Render(title + "\n" + bodyBlock)
}

func renderCodexDenseBlock(label, body string, accent lipgloss.Color, width int, blockMode codexDenseBlockMode) string {
	lines := strings.Split(strings.TrimSpace(body), "\n")
	if len(lines) == 0 {
		return ""
	}
	blockMode = blockMode.normalized()
	if blockMode.full() {
		return renderCodexMonospaceBlock(label, strings.Join(lines, "\n"), accent, width)
	}
	lines, hidden := visibleCodexDenseBlockLines(lines, blockMode)
	if len(lines) == 0 && hidden == 0 {
		return ""
	}
	title := label
	if hidden > 0 {
		title = codexDenseBlockHiddenTitle(label, hidden, blockMode)
	}
	return renderCodexMonospaceBlock(title, strings.Join(lines, "\n"), accent, width)
}

func visibleCodexDenseBlockLines(lines []string, blockMode codexDenseBlockMode) ([]string, int) {
	filtered := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "[command completed, exit 0]" {
			continue
		}
		if strings.HasPrefix(trimmed, "# cwd:") {
			continue
		}
		filtered = append(filtered, line)
	}

	visible := make([]string, 0, len(filtered))
	hidden := 0
	shownPreviewLines := 0
	for _, line := range filtered {
		if isCodexDenseSummaryLine(line) {
			visible = append(visible, line)
			continue
		}
		switch blockMode.normalized() {
		case codexDenseBlockPreview:
			if shownPreviewLines < codexDenseBlockPreviewLines {
				visible = append(visible, line)
				shownPreviewLines++
				continue
			}
			hidden++
		default:
			hidden++
		}
	}
	return visible, hidden
}

func isCodexDenseSummaryLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	if strings.HasPrefix(trimmed, "$ ") {
		return true
	}
	if strings.HasPrefix(trimmed, "[command ") && trimmed != "[command completed, exit 0]" {
		return true
	}
	if strings.HasPrefix(trimmed, "[file changes ") {
		return true
	}
	return false
}

func codexDenseBlockHiddenTitle(label string, hidden int, blockMode codexDenseBlockMode) string {
	plural := "lines"
	if hidden == 1 {
		plural = "line"
	}
	action := "Alt+L previews"
	if blockMode.normalized() == codexDenseBlockPreview {
		action = "Alt+L expands"
	}
	return fmt.Sprintf("%s (%d %s hidden; %s)", label, hidden, plural, action)
}

func renderCodexBody(body string, color lipgloss.Color, width int) string {
	return terminalmd.RenderBody(body, color, width)
}
