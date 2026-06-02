package tui

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

const (
	codexMarkdownLinkLabelScanLimit  = 512
	codexMarkdownLinkTargetScanLimit = 8192
)

func renderCodexMessageBlock(label, body string, accent, bodyColor lipgloss.Color, width int) string {
	return renderCodexMessageBlockWithStyle(label, body, accent, bodyColor, width, false)
}

func renderCodexMessageBlockForProject(label, body string, accent, bodyColor lipgloss.Color, width int, projectPath string) string {
	return renderCodexMessageBlockWithStyleForProject(label, body, accent, bodyColor, width, false, projectPath)
}

func renderCodexCompactTranscriptLine(body string, accent lipgloss.Color, width int) string {
	return renderCodexMessageBlockWithStyle("", body, accent, accent, width, false)
}

func renderCodexUserMessageBlock(body string, width int) string {
	return renderCodexMessageBlockWithStyle("", body, lipgloss.Color("81"), lipgloss.Color("252"), width, true)
}

func renderCodexUserMessageBlockForProject(body string, width int, projectPath string) string {
	return renderCodexMessageBlockWithStyleForProject("", body, lipgloss.Color("81"), lipgloss.Color("252"), width, true, projectPath)
}

func renderCodexPlanBlock(body string, width int) string {
	return renderCodexPlanBlockForProject(body, width, "")
}

func renderCodexPlanBlockForProject(body string, width int, projectPath string) string {
	accent := lipgloss.Color("214")
	contentWidth := max(10, width-2)
	label := lipgloss.NewStyle().Bold(true).Foreground(accent).Render("Plan")
	lines := []string{label}
	if renderedBody := strings.TrimSpace(renderCodexPlanBodyForProject(body, contentWidth, projectPath)); renderedBody != "" {
		lines = append(lines, strings.Split(renderedBody, "\n")...)
	}
	return lipgloss.NewStyle().
		BorderLeft(true).
		BorderForeground(accent).
		PaddingLeft(1).
		Render(strings.Join(lines, "\n"))
}

func renderCodexPlanBody(body string, width int) string {
	return renderCodexPlanBodyForProject(body, width, "")
}

func renderCodexPlanBodyForProject(body string, width int, projectPath string) string {
	body = strings.TrimSpace(body)
	if body == "" {
		return ""
	}
	lines := strings.Split(body, "\n")
	rendered := make([]string, 0, len(lines))
	for _, line := range lines {
		if marker, text, ok := parseCodexPlanMarker(line); ok {
			rendered = append(rendered, renderCodexPlanStatusLineForProject(marker, text, width, projectPath)...)
			continue
		}
		rendered = append(rendered, strings.Split(renderCodexBodyForProject(line, lipgloss.Color("252"), width, projectPath), "\n")...)
	}
	return strings.Join(rendered, "\n")
}

func parseCodexPlanMarker(line string) (marker, text string, ok bool) {
	trimmed := strings.TrimSpace(line)
	for _, candidate := range []string{"[x]", "[>]", "[*]", "[ ]"} {
		if strings.HasPrefix(trimmed, candidate) {
			return candidate, strings.TrimSpace(strings.TrimPrefix(trimmed, candidate)), true
		}
	}
	return "", "", false
}

func renderCodexPlanStatusLine(marker, text string, width int) []string {
	return renderCodexPlanStatusLineForProject(marker, text, width, "")
}

func renderCodexPlanStatusLineForProject(marker, text string, width int, projectPath string) []string {
	markerStyle, textStyle := codexPlanStatusStyles(marker)
	markerWidth := ansi.StringWidth(marker)
	indent := markerWidth + 1
	textWidth := max(4, width-indent)
	if strings.TrimSpace(text) == "" {
		return []string{markerStyle.Render(marker)}
	}
	renderedText := renderCodexInlineMarkdownForProject(text, textStyle, projectPath)
	wrapped := strings.Split(lipgloss.NewStyle().Width(textWidth).Render(renderedText), "\n")
	out := make([]string, 0, len(wrapped))
	for index, line := range wrapped {
		if index == 0 {
			out = append(out, markerStyle.Render(marker)+" "+line)
			continue
		}
		out = append(out, strings.Repeat(" ", indent)+line)
	}
	return out
}

func codexPlanStatusStyles(marker string) (lipgloss.Style, lipgloss.Style) {
	switch marker {
	case "[x]":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("120")).Bold(true),
			lipgloss.NewStyle().Foreground(lipgloss.Color("246"))
	case "[>]", "[*]":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true),
			lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Bold(true)
	case "[ ]":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("244")),
			lipgloss.NewStyle().Foreground(lipgloss.Color("248"))
	default:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("141")).Bold(true),
			lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	}
}

func renderCodexMessageBlockWithStyle(label, body string, accent, bodyColor lipgloss.Color, width int, shaded bool) string {
	return renderCodexMessageBlockWithStyleForProject(label, body, accent, bodyColor, width, shaded, "")
}

func renderCodexMessageBlockWithStyleForProject(label, body string, accent, bodyColor lipgloss.Color, width int, shaded bool, projectPath string) string {
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
	lines := renderCodexMessageBlockLinesForProject(label, body, accent, bodyColor, contentWidth, projectPath)
	return style.Render(strings.Join(lines, "\n"))
}

var reasoningBackgroundColor = lipgloss.Color("235")

func renderCodexMessageBlockLines(label, body string, accent, bodyColor lipgloss.Color, contentWidth int) []string {
	return renderCodexMessageBlockLinesForProject(label, body, accent, bodyColor, contentWidth, "")
}

func renderCodexMessageBlockLinesForProject(label, body string, accent, bodyColor lipgloss.Color, contentWidth int, projectPath string) []string {
	label = strings.TrimSpace(label)
	if label == "" {
		return []string{renderCodexBodyForProject(body, bodyColor, contentWidth, projectPath)}
	}
	labelStyle := lipgloss.NewStyle().Bold(true).Foreground(accent)
	labelWidth := ansi.StringWidth(label) + 1
	if labelWidth >= contentWidth-4 {
		return []string{
			labelStyle.Render(label),
			renderCodexBodyForProject(body, bodyColor, contentWidth, projectPath),
		}
	}

	firstLine, rest, _ := strings.Cut(body, "\n")
	firstWidth := max(4, contentWidth-labelWidth)
	firstRendered := strings.Split(renderCodexBodyForProject(firstLine, bodyColor, firstWidth, projectPath), "\n")
	lines := make([]string, 0, len(firstRendered)+1)
	for index, line := range firstRendered {
		if index == 0 {
			lines = append(lines, labelStyle.Render(label)+" "+line)
			continue
		}
		lines = append(lines, strings.Repeat(" ", labelWidth)+line)
	}
	if strings.TrimSpace(rest) != "" {
		lines = append(lines, renderCodexBodyForProject(rest, bodyColor, contentWidth, projectPath))
	}
	return lines
}

func renderReasoningBlock(body string, width int) string {
	return renderReasoningBlockForProject(body, width, "")
}

func renderReasoningBlockForProject(body string, width int, projectPath string) string {
	contentWidth := max(10, width-4)
	label := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("180")).Faint(true).Render("Reasoning")
	bodyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Width(contentWidth)
	wrappedBody := bodyStyle.Render(renderCodexBodyForProject(body, lipgloss.Color("252"), contentWidth, projectPath))
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
		renderedLines = append(renderedLines, renderCodexMonospaceLine(line))
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
	if hidden > 0 && len(lines) > 0 && isCodexDenseSummaryLine(lines[0]) {
		return renderCodexDenseBlockWithInlineSummary(title, lines, accent, width)
	}
	return renderCodexMonospaceBlock(title, strings.Join(lines, "\n"), accent, width)
}

func renderCodexDenseBlockWithInlineSummary(label string, lines []string, accent lipgloss.Color, width int) string {
	contentWidth := max(10, width-2)
	title := lipgloss.NewStyle().Bold(true).Foreground(accent).Render(label)
	firstLine := renderCodexMonospaceLine(lines[0])
	inline := lipgloss.NewStyle().Width(contentWidth).Render(title + " -> " + firstLine)
	if len(lines) == 1 {
		return lipgloss.NewStyle().
			BorderLeft(true).
			BorderForeground(accent).
			PaddingLeft(0).
			Render(inline)
	}
	bodyLines := make([]string, 0, len(lines)-1)
	for _, line := range lines[1:] {
		bodyLines = append(bodyLines, renderCodexMonospaceLine(line))
	}
	bodyBlock := lipgloss.NewStyle().Width(contentWidth).Render(strings.Join(bodyLines, "\n"))
	return lipgloss.NewStyle().
		BorderLeft(true).
		BorderForeground(accent).
		PaddingLeft(0).
		Render(inline + "\n" + bodyBlock)
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

func renderCodexMonospaceLine(line string) string {
	switch {
	case strings.HasPrefix(line, "$ "):
		return lipgloss.NewStyle().Foreground(lipgloss.Color("81")).Bold(true).Render(line)
	case strings.HasPrefix(line, "diff --git "), strings.HasPrefix(line, "index "):
		return lipgloss.NewStyle().Foreground(lipgloss.Color("111")).Bold(true).Render(line)
	case strings.HasPrefix(line, "@@"):
		return lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true).Render(line)
	case strings.HasPrefix(line, "+++"), strings.HasPrefix(line, "+"):
		return lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Render(line)
	case strings.HasPrefix(line, "---"), strings.HasPrefix(line, "-"):
		return lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Render(line)
	case strings.HasPrefix(line, "# "):
		return lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Faint(true).Render(line)
	case strings.HasPrefix(line, "[command ") && !strings.Contains(line, "exit 0]"):
		return lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Bold(true).Render(line)
	case strings.HasPrefix(line, "[command "):
		return lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Faint(true).Render(line)
	case strings.HasPrefix(line, "[file changes "):
		return lipgloss.NewStyle().Foreground(lipgloss.Color("149")).Bold(true).Render(line)
	default:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("250")).Render(line)
	}
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
	return renderCodexBodyForProject(body, color, width, "")
}

func renderCodexBodyForProject(body string, color lipgloss.Color, width int, projectPath string) string {
	lines := strings.Split(body, "\n")
	out := make([]string, 0, len(lines))
	inFence := false
	fenceLanguage := ""
	fenceLines := []string{}
	tableRows := []string{}

	flushFence := func() {
		if len(fenceLines) == 0 {
			return
		}
		highlighted := syntaxHighlightBlock(strings.Join(fenceLines, "\n"), fenceLanguage, "", syntaxHighlightOptions{
			DefaultColor: lipgloss.Color("180"),
		})
		out = append(out, strings.Split(highlighted, "\n")...)
		fenceLines = nil
	}
	flushTable := func() {
		if len(tableRows) == 0 {
			return
		}
		out = append(out, renderCodexMarkdownTable(tableRows, color, width)...)
		tableRows = nil
	}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "```"):
			flushTable()
			if inFence {
				flushFence()
				inFence = false
				fenceLanguage = ""
			} else {
				inFence = true
				fenceLanguage = strings.TrimSpace(strings.TrimPrefix(trimmed, "```"))
			}
			out = append(out, lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Faint(true).Render(line))
		case inFence:
			fenceLines = append(fenceLines, line)
		case isMarkdownTableRow(trimmed):
			tableRows = append(tableRows, trimmed)
		default:
			flushTable()
			switch {
			case strings.HasPrefix(trimmed, "[attached image]"):
				out = append(out, renderCodexInlineMarkdownForProject(line, lipgloss.NewStyle().Foreground(lipgloss.Color("179")).Bold(true), projectPath))
			case strings.HasPrefix(trimmed, "### "):
				out = append(out, renderCodexInlineMarkdownForProject(strings.TrimPrefix(trimmed, "### "), lipgloss.NewStyle().Foreground(lipgloss.Color("117")).Bold(true), projectPath))
			case strings.HasPrefix(trimmed, "## "):
				out = append(out, renderCodexInlineMarkdownForProject(strings.TrimPrefix(trimmed, "## "), lipgloss.NewStyle().Foreground(lipgloss.Color("117")).Bold(true), projectPath))
			case strings.HasPrefix(trimmed, "# "):
				out = append(out, renderCodexInlineMarkdownForProject(strings.TrimPrefix(trimmed, "# "), lipgloss.NewStyle().Foreground(lipgloss.Color("117")).Bold(true), projectPath))
			case strings.HasPrefix(trimmed, "> "):
				out = append(out, renderCodexInlineMarkdownForProject(line, lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Italic(true), projectPath))
			case isMarkdownHorizontalRule(trimmed):
				rule := lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Faint(true).Render(strings.Repeat("─", min(width, 40)))
				out = append(out, rule)
			case strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* "):
				out = append(out, renderCodexInlineMarkdownForProject("• "+strings.TrimSpace(trimmed[2:]), lipgloss.NewStyle().Foreground(lipgloss.Color("151")), projectPath))
			case isMarkdownNumberedListItem(trimmed):
				num, content := parseMarkdownNumberedListItem(trimmed)
				numStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
				out = append(out, numStyle.Render(num+".")+renderCodexInlineMarkdownForProject(" "+content, lipgloss.NewStyle().Foreground(lipgloss.Color("151")), projectPath))
			default:
				out = append(out, renderCodexInlineMarkdownForProject(line, lipgloss.NewStyle().Foreground(color), projectPath))
			}
		}
	}
	if inFence {
		flushFence()
	}
	flushTable()
	return lipgloss.NewStyle().Width(width).Render(strings.Join(out, "\n"))
}

func isMarkdownTableRow(line string) bool {
	return strings.HasPrefix(line, "|") && strings.HasSuffix(line, "|") && strings.Count(line, "|") >= 3
}

func isMarkdownTableSeparator(line string) bool {
	if !isMarkdownTableRow(line) {
		return false
	}
	inner := strings.Trim(line, "|")
	for _, cell := range strings.Split(inner, "|") {
		cell = strings.TrimSpace(cell)
		cleaned := strings.Trim(cell, ":-")
		if cleaned != "" {
			return false
		}
	}
	return true
}

func renderCodexMarkdownTable(rows []string, color lipgloss.Color, maxWidth int) []string {
	if len(rows) == 0 {
		return nil
	}
	// Parse all rows into cells
	parsed := make([][]string, 0, len(rows))
	separatorIdxs := map[int]bool{}
	for i, row := range rows {
		if isMarkdownTableSeparator(row) {
			separatorIdxs[i] = true
			parsed = append(parsed, nil)
			continue
		}
		inner := strings.Trim(strings.TrimSpace(row), "|")
		cells := strings.Split(inner, "|")
		for j := range cells {
			cells[j] = strings.TrimSpace(cells[j])
		}
		parsed = append(parsed, cells)
	}

	numCols := 0
	for _, cells := range parsed {
		if len(cells) > numCols {
			numCols = len(cells)
		}
	}
	if numCols == 0 {
		return nil
	}
	colWidths := make([]int, numCols)
	for _, cells := range parsed {
		for j, cell := range cells {
			if w := ansi.StringWidth(cell); w > colWidths[j] {
				colWidths[j] = w
			}
		}
	}

	tableOverhead := 2 + numCols*3 - 1
	totalWidth := tableOverhead
	for _, w := range colWidths {
		totalWidth += w
	}
	if totalWidth > maxWidth {
		colWidths = fitCodexMarkdownTableColumnWidths(colWidths, maxWidth, tableOverhead)
	}
	for i, w := range colWidths {
		if w < 1 {
			colWidths[i] = 1
		}
	}

	headerStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("117")).Bold(true)
	cellStyle := lipgloss.NewStyle().Foreground(color)
	borderStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))

	out := make([]string, 0, len(rows))
	for i, cells := range parsed {
		if separatorIdxs[i] {
			// Render separator line
			parts := make([]string, numCols)
			for j := range parts {
				parts[j] = strings.Repeat("─", colWidths[j])
			}
			out = append(out, borderStyle.Render("├─"+strings.Join(parts, "─┼─")+"─┤"))
			continue
		}
		isHeader := i == 0 && len(parsed) > 1 && separatorIdxs[1]
		wrappedCells := make([][]string, numCols)
		rowHeight := 1
		for j := 0; j < numCols; j++ {
			cell := ""
			if j < len(cells) {
				cell = cells[j]
			}
			wrappedCells[j] = wrapCodexMarkdownTableCell(cell, colWidths[j])
			if len(wrappedCells[j]) > rowHeight {
				rowHeight = len(wrappedCells[j])
			}
		}
		for lineIdx := 0; lineIdx < rowHeight; lineIdx++ {
			parts := make([]string, numCols)
			for j := 0; j < numCols; j++ {
				cellLine := ""
				if lineIdx < len(wrappedCells[j]) {
					cellLine = wrappedCells[j][lineIdx]
				}
				w := colWidths[j]
				pad := w - ansi.StringWidth(cellLine)
				if pad < 0 {
					pad = 0
				}
				padded := cellLine + strings.Repeat(" ", pad)
				if isHeader {
					parts[j] = headerStyle.Render(padded)
				} else {
					parts[j] = cellStyle.Render(padded)
				}
			}
			sep := borderStyle.Render(" │ ")
			out = append(out, borderStyle.Render("│ ")+strings.Join(parts, sep)+borderStyle.Render(" │"))
		}
	}
	return out
}

func fitCodexMarkdownTableColumnWidths(maxWidths []int, maxWidth int, tableOverhead int) []int {
	widths := append([]int(nil), maxWidths...)
	target := maxWidth - tableOverhead
	if target <= 0 {
		target = len(widths)
	}
	if target < len(widths) {
		for i := range widths {
			widths[i] = 1
		}
		return widths
	}

	available := target
	unresolved := make([]bool, len(widths))
	remaining := 0
	for i := range widths {
		unresolved[i] = true
		remaining++
	}
	for remaining > 0 {
		share := available / remaining
		if share < 1 {
			share = 1
		}
		changed := false
		for i, maxWidth := range maxWidths {
			if !unresolved[i] || maxWidth > share {
				continue
			}
			widths[i] = max(1, maxWidth)
			available -= widths[i]
			unresolved[i] = false
			remaining--
			changed = true
		}
		if changed {
			continue
		}
		remainder := available - share*remaining
		for i := range widths {
			if !unresolved[i] {
				continue
			}
			widths[i] = share
			if remainder > 0 {
				widths[i]++
				remainder--
			}
		}
		break
	}
	return widths
}

func wrapCodexMarkdownTableCell(cell string, width int) []string {
	if width <= 0 || cell == "" {
		return []string{cell}
	}
	wrapped := lipgloss.NewStyle().Width(width).Render(cell)
	rawLines := strings.Split(strings.ReplaceAll(wrapped, "\r\n", "\n"), "\n")
	lines := make([]string, 0, len(rawLines))
	for _, raw := range rawLines {
		lines = append(lines, hardWrapCodexMarkdownTableLine(raw, width)...)
	}
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

func hardWrapCodexMarkdownTableLine(line string, width int) []string {
	if width <= 0 || ansi.StringWidth(line) <= width {
		return []string{line}
	}
	lines := []string{}
	remaining := line
	for remaining != "" {
		part := ansi.Truncate(remaining, width, "")
		if part == "" {
			runes := []rune(remaining)
			part = string(runes[:1])
		}
		lines = append(lines, part)
		remaining = strings.TrimPrefix(remaining, part)
	}
	return lines
}

func isMarkdownHorizontalRule(line string) bool {
	if len(line) < 3 {
		return false
	}
	// Must be only dashes, asterisks, or underscores (with optional spaces)
	cleaned := strings.ReplaceAll(line, " ", "")
	if len(cleaned) < 3 {
		return false
	}
	ch := cleaned[0]
	if ch != '-' && ch != '*' && ch != '_' {
		return false
	}
	for _, r := range cleaned {
		if byte(r) != ch {
			return false
		}
	}
	return true
}

func isMarkdownNumberedListItem(line string) bool {
	for i, r := range line {
		if r >= '0' && r <= '9' {
			continue
		}
		if r == '.' && i > 0 && i < len(line)-1 && line[i+1] == ' ' {
			return true
		}
		return false
	}
	return false
}

func parseMarkdownNumberedListItem(line string) (num, content string) {
	dotIdx := strings.IndexByte(line, '.')
	if dotIdx < 0 {
		return "", line
	}
	return line[:dotIdx], strings.TrimSpace(line[dotIdx+1:])
}

func renderCodexInlineMarkdown(text string, style lipgloss.Style) string {
	return renderCodexInlineMarkdownForProject(text, style, "")
}

func renderCodexInlineMarkdownForProject(text string, style lipgloss.Style, projectPath string) string {
	if text == "" {
		return style.Render(text)
	}
	codeStyle := style.Copy().
		Foreground(lipgloss.Color("223")).
		Background(lipgloss.Color("236"))
	var out strings.Builder
	remaining := text
	for len(remaining) > 0 {
		// Find the earliest markdown marker: **, *, [, or `
		boldIdx := strings.Index(remaining, "**")
		italicIdx := -1
		linkIdx := strings.IndexByte(remaining, '[')
		codeIdx := strings.IndexByte(remaining, '`')

		// Find standalone * (italic) that is not part of **
		for i := 0; i < len(remaining); i++ {
			if remaining[i] == '*' {
				if i+1 < len(remaining) && remaining[i+1] == '*' {
					i++ // skip **
					continue
				}
				italicIdx = i
				break
			}
		}

		// Find earliest marker
		earliest := -1
		for _, idx := range []int{boldIdx, italicIdx, linkIdx, codeIdx} {
			if idx >= 0 && (earliest < 0 || idx < earliest) {
				earliest = idx
			}
		}
		if earliest < 0 {
			out.WriteString(style.Render(remaining))
			break
		}

		// Render text before the marker
		if earliest > 0 {
			out.WriteString(style.Render(remaining[:earliest]))
		}

		// Process the marker
		switch {
		case boldIdx == earliest:
			// Look for closing **
			close := strings.Index(remaining[earliest+2:], "**")
			if close < 0 || close == 0 {
				out.WriteString(style.Render("**"))
				remaining = remaining[earliest+2:]
				continue
			}
			inner := remaining[earliest+2 : earliest+2+close]
			out.WriteString(style.Copy().Bold(true).Render(inner))
			remaining = remaining[earliest+2+close+2:]

		case italicIdx == earliest:
			// Look for closing * (not **)
			rest := remaining[earliest+1:]
			close := -1
			for i := 0; i < len(rest); i++ {
				if rest[i] == '*' {
					if i+1 < len(rest) && rest[i+1] == '*' {
						i++ // skip **
						continue
					}
					close = i
					break
				}
			}
			if close <= 0 {
				out.WriteString(style.Render("*"))
				remaining = remaining[earliest+1:]
				continue
			}
			inner := rest[:close]
			out.WriteString(style.Copy().Italic(true).Render(inner))
			remaining = rest[close+1:]

		case linkIdx == earliest:
			label, target, consumed, ok := parseCodexMarkdownLink(remaining[earliest:])
			if !ok {
				out.WriteString(style.Render("["))
				remaining = remaining[earliest+1:]
				continue
			}
			out.WriteString(renderCodexHyperlinkForProject(label, target, style, projectPath))
			remaining = remaining[earliest+consumed:]

		case codeIdx == earliest:
			rest := remaining[earliest+1:]
			close := strings.IndexByte(rest, '`')
			if close <= 0 {
				out.WriteString(style.Render("`"))
				remaining = rest
				continue
			}
			inner := rest[:close]
			out.WriteString(codeStyle.Render(inner))
			remaining = rest[close+1:]
		}
	}
	return out.String()
}

func parseCodexMarkdownLink(text string) (label, target string, consumed int, ok bool) {
	if !strings.HasPrefix(text, "[") {
		return "", "", 0, false
	}
	closeLabelOffset := boundedIndexByte(text[1:], ']', codexMarkdownLinkLabelScanLimit)
	if closeLabelOffset < 0 {
		return "", "", 0, false
	}
	closeLabel := 1 + closeLabelOffset
	if closeLabel <= 1 {
		return "", "", 0, false
	}
	if closeLabel+1 >= len(text) || text[closeLabel+1] != '(' {
		return "", "", 0, false
	}
	targetText := text[closeLabel+2:]
	target, targetConsumed, ok := parseCodexMarkdownLinkTarget(targetText)
	if !ok {
		return "", "", 0, false
	}
	label = text[1:closeLabel]
	if strings.TrimSpace(label) == "" || strings.TrimSpace(target) == "" {
		return "", "", 0, false
	}
	return label, target, closeLabel + 2 + targetConsumed, true
}

func parseCodexMarkdownLinkTarget(text string) (target string, consumed int, ok bool) {
	leading := len(text) - len(strings.TrimLeftFunc(text, unicode.IsSpace))
	if leading >= len(text) || leading > codexMarkdownLinkTargetScanLimit {
		return "", 0, false
	}
	trimmed := text[leading:]
	if strings.HasPrefix(trimmed, "<") {
		closeAngle := boundedIndexByte(trimmed[1:], '>', codexMarkdownLinkTargetScanLimit)
		if closeAngle < 0 {
			return "", 0, false
		}
		target = trimmed[1 : 1+closeAngle]
		afterTarget := trimmed[1+closeAngle+1:]
		trailing := len(afterTarget) - len(strings.TrimLeftFunc(afterTarget, unicode.IsSpace))
		afterTarget = afterTarget[trailing:]
		if !strings.HasPrefix(afterTarget, ")") {
			return "", 0, false
		}
		return strings.TrimSpace(target), leading + 1 + closeAngle + 1 + trailing + 1, true
	}
	closeTarget := boundedIndexByte(trimmed, ')', codexMarkdownLinkTargetScanLimit)
	if closeTarget < 0 {
		return "", 0, false
	}
	return strings.TrimSpace(trimmed[:closeTarget]), leading + closeTarget + 1, true
}

func boundedIndexByte(text string, c byte, limit int) int {
	if limit > 0 && len(text) > limit {
		text = text[:limit]
	}
	return strings.IndexByte(text, c)
}

func renderCodexHyperlink(label, target string, style lipgloss.Style) string {
	return renderCodexHyperlinkForProject(label, target, style, "")
}

func renderCodexHyperlinkForProject(label, target string, style lipgloss.Style, projectPath string) string {
	linkStyle := style.Copy().Foreground(lipgloss.Color("111")).Underline(true)
	if localPath, ok := codexLocalLinkTextForProject(target, projectPath); ok {
		if artifactPath, _, ok := codexLocalArtifactOpenTarget(label, localPath); ok {
			return renderCodexLocalArtifactLink(label, artifactPath, linkStyle)
		}
		return renderCodexLocalLink(label, localPath, linkStyle)
	}
	target = codexHyperlinkTarget(target)
	renderedLabel := linkStyle.Render(label)
	if target == "" {
		return renderedLabel
	}
	return ansi.SetHyperlink(target) + renderedLabel + ansi.ResetHyperlink()
}

func renderCodexLocalLink(label, target string, linkStyle lipgloss.Style) string {
	label = codexLocalLinkLabel(label, target)
	target, _ = codexLocalOpenPath(target)
	renderedLabel := linkStyle.Render(label)
	return ansi.SetHyperlink(target) + renderedLabel + ansi.ResetHyperlink()
}

func renderCodexLocalArtifactLink(label, target string, linkStyle lipgloss.Style) string {
	label = codexLocalLinkLabel(label, target)
	target = strings.TrimSpace(target)
	artifactStyle := linkStyle.Copy().Underline(false)
	rendered := artifactStyle.Render(label)
	base := filepath.Base(target)
	if target == "" || label == target || label == base {
		return rendered + renderCodexInlineArtifactOpenHint()
	}
	pathStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	return rendered + pathStyle.Render(" ("+filepath.Base(target)+")") + renderCodexInlineArtifactOpenHint()
}

func renderCodexInlineArtifactOpenHint() string {
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color("111")).
		Render(" Alt+O")
}

func codexLocalLinkLabel(label, target string) string {
	label = strings.TrimSpace(label)
	target = strings.TrimSpace(target)
	if label == "" || label == target {
		label = filepath.Base(target)
		if label == "." || label == string(filepath.Separator) {
			label = target
		}
	}
	return label
}

func codexHyperlinkTarget(target string) string {
	target = strings.TrimSpace(target)
	if target == "" {
		return ""
	}
	parsed, err := url.Parse(target)
	if err != nil || parsed.Scheme == "" {
		return target
	}
	return parsed.String()
}

func codexLocalLinkText(target string) (string, bool) {
	target = strings.TrimSpace(target)
	if target == "" {
		return "", false
	}
	if strings.HasPrefix(target, "/") {
		return codexLocalPathText(target, ""), true
	}
	parsed, err := url.Parse(target)
	if err != nil || parsed.Scheme != "file" {
		return "", false
	}
	path := parsed.EscapedPath()
	if path == "" {
		path = parsed.Path
	}
	pathText := codexLocalPathText(path, parsed.Fragment)
	if parsed.Host != "" && parsed.Host != "localhost" {
		pathText = parsed.Host + ":" + pathText
	}
	return pathText, path != ""
}

func codexLocalLinkTextForProject(target, projectPath string) (string, bool) {
	if localPath, ok := codexLocalLinkText(target); ok {
		return localPath, true
	}
	return codexRelativeLocalLinkText(target, projectPath)
}

func codexRelativeLocalLinkText(target, projectPath string) (string, bool) {
	target = strings.TrimSpace(target)
	projectPath = strings.TrimSpace(projectPath)
	if target == "" || projectPath == "" {
		return "", false
	}
	if strings.HasPrefix(target, "#") || strings.HasPrefix(target, "?") || strings.HasPrefix(target, "//") {
		return "", false
	}
	parsed, err := url.Parse(target)
	if err != nil || parsed.Scheme != "" || parsed.Host != "" {
		return "", false
	}
	pathPart, location := codexLocalOpenPath(target)
	pathPart = strings.TrimSpace(pathPart)
	if !codexRelativeLocalLinkPath(pathPart) {
		return "", false
	}
	resolved := filepath.Clean(filepath.Join(projectPath, filepath.FromSlash(pathPart)))
	if location != "" {
		resolved += location
	}
	return resolved, true
}

func codexRelativeLocalLinkPath(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" || filepath.IsAbs(path) || strings.ContainsAny(path, "\r\n") {
		return false
	}
	slashPath := filepath.ToSlash(path)
	if slashPath == "." || slashPath == ".." {
		return false
	}
	if strings.HasPrefix(slashPath, "./") || strings.HasPrefix(slashPath, "../") {
		return true
	}
	firstSegment := slashPath
	if idx := strings.IndexByte(slashPath, '/'); idx >= 0 {
		firstSegment = slashPath[:idx]
	}
	if strings.Contains(slashPath, "/") && firstSegment != "." && firstSegment != ".." && strings.Contains(firstSegment, ".") {
		return false
	}
	base := slashPath
	if idx := strings.LastIndexByte(slashPath, '/'); idx >= 0 {
		base = slashPath[idx+1:]
	}
	return filepath.Ext(base) != ""
}

func codexLocalPathText(path, fragment string) string {
	if fragment == "" {
		if before, after, found := strings.Cut(path, "#"); found {
			path = before
			fragment = after
		}
	}
	if fragment != "" {
		if isCodexLineFragment(fragment) {
			path += ":" + fragment
		} else {
			path += "#" + fragment
		}
	}
	return path
}

func isCodexLineFragment(text string) bool {
	if text == "" {
		return false
	}
	for i, part := range strings.Split(text, ":") {
		if part == "" || (i > 1) {
			return false
		}
		for _, r := range part {
			if r < '0' || r > '9' {
				return false
			}
		}
	}
	return true
}

func isCodexLocalImagePath(path string) bool {
	return codexArtifactKindForPath(path) == "image"
}

func codexLocalArtifactOpenTarget(label, target string) (path, kind string, ok bool) {
	target = strings.TrimSpace(target)
	if target == "" {
		return "", "", false
	}
	openPath, _ := codexLocalOpenPath(target)
	if dir, ok := codexReadmeDirectoryLinkTarget(label, openPath); ok {
		return dir, "dir", true
	}
	kind = codexArtifactKindForPath(openPath)
	if kind == "" {
		return "", "", false
	}
	return openPath, kind, true
}

func codexLocalOpenPath(target string) (path, location string) {
	path = strings.TrimSpace(target)
	if path == "" {
		return "", ""
	}
	if before, after, found := strings.Cut(path, "#"); found {
		path = before
		if after != "" {
			location = "#" + after
		}
	}
	path, lineLocation := codexSplitLocalLineSuffix(path)
	if lineLocation != "" {
		location = lineLocation
	}
	path = unescapeCodexLocalOpenPath(path)
	return path, location
}

func unescapeCodexLocalOpenPath(path string) string {
	if !strings.Contains(path, "%") {
		return path
	}
	unescaped, err := url.PathUnescape(path)
	if err != nil {
		return path
	}
	return unescaped
}

func codexSplitLocalLineSuffix(path string) (string, string) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", ""
	}
	base, suffix, ok := codexCutTrailingNumberSuffix(path)
	if !ok {
		return path, ""
	}
	if nextBase, nextSuffix, ok := codexCutTrailingNumberSuffix(base); ok {
		return nextBase, nextSuffix + suffix
	}
	return base, suffix
}

func codexCutTrailingNumberSuffix(path string) (base, suffix string, ok bool) {
	idx := strings.LastIndexByte(path, ':')
	if idx <= 0 || idx == len(path)-1 {
		return path, "", false
	}
	tail := path[idx+1:]
	for _, r := range tail {
		if r < '0' || r > '9' {
			return path, "", false
		}
	}
	return path[:idx], path[idx:], true
}

func codexReadmeDirectoryLinkTarget(label, target string) (string, bool) {
	base := strings.ToLower(filepath.Base(strings.TrimSpace(target)))
	if base != "readme.md" && base != "readme.markdown" {
		return "", false
	}
	dir := filepath.Dir(strings.TrimSpace(target))
	if dir == "" || dir == "." {
		return "", false
	}
	label = strings.TrimSpace(label)
	if label == "" {
		return "", false
	}
	label = strings.TrimSuffix(label, "/")
	label = strings.TrimSuffix(label, string(filepath.Separator))
	if label != filepath.Base(dir) {
		return "", false
	}
	return dir, true
}

func codexArtifactKindForPath(path string) string {
	switch strings.ToLower(filepath.Ext(strings.TrimSpace(path))) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp", ".tif", ".tiff", ".heic", ".heif", ".svg":
		return "image"
	case ".pdf":
		return "pdf"
	case ".csv", ".tsv", ".xlsx", ".xls", ".ods", ".numbers":
		return "sheet"
	case ".doc", ".docx", ".pages", ".rtf", ".md", ".markdown":
		return "doc"
	case ".ppt", ".pptx", ".key":
		return "deck"
	case ".zip", ".tar", ".gz", ".tgz", ".bz2", ".xz", ".7z", ".rar":
		return "archive"
	case ".mp4", ".mov", ".m4v", ".avi", ".mkv", ".webm":
		return "video"
	case ".mp3", ".wav", ".m4a", ".aac", ".flac":
		return "audio"
	case ".html", ".htm":
		return "html"
	default:
		return ""
	}
}
