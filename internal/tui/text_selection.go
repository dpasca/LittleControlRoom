package tui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// textSelection tracks a mouse-driven text selection in the codex viewport.
type textSelection struct {
	anchorRow  int // content row where the press started
	anchorCol  int // visual column where the press started
	currentRow int // content row of the current drag position
	currentCol int // visual column of the current drag position
	dragging   bool
}

// normalized returns the selection bounds with start <= end.
func (s textSelection) normalized() (startRow, startCol, endRow, endCol int) {
	if s.anchorRow < s.currentRow || (s.anchorRow == s.currentRow && s.anchorCol <= s.currentCol) {
		return s.anchorRow, s.anchorCol, s.currentRow, s.currentCol
	}
	return s.currentRow, s.currentCol, s.anchorRow, s.anchorCol
}

// hasRange reports whether the selection covers at least one character.
func (s textSelection) hasRange() bool {
	return s.anchorRow != s.currentRow || s.anchorCol != s.currentCol
}

// extractText returns the plain text covered by the selection.
// fullContent is the full viewport content (with ANSI styling).
func (s textSelection) extractText(fullContent string) string {
	if !s.hasRange() {
		return ""
	}
	lines := strings.Split(fullContent, "\n")
	startRow, startCol, endRow, endCol := s.normalized()

	var result []string
	for row := startRow; row <= endRow && row < len(lines); row++ {
		if row < 0 {
			continue
		}
		plain := ansi.Strip(lines[row])
		lineWidth := ansi.StringWidth(plain)

		colStart := 0
		colEnd := lineWidth
		if row == startRow {
			colStart = startCol
		}
		if row == endRow {
			colEnd = min(endCol, lineWidth)
		}
		if colStart >= lineWidth {
			result = append(result, "")
			continue
		}
		result = append(result, ansi.Cut(plain, colStart, colEnd))
	}
	return strings.Join(result, "\n")
}

// overlaySelectionHighlight applies reverse-video styling to the selected
// region within the viewport's visible output.
func overlaySelectionHighlight(viewportOutput string, sel textSelection, yOffset int) string {
	if !sel.hasRange() {
		return viewportOutput
	}
	startRow, startCol, endRow, endCol := sel.normalized()
	lines := strings.Split(viewportOutput, "\n")

	for i, line := range lines {
		contentRow := yOffset + i
		if contentRow < startRow || contentRow > endRow {
			continue
		}
		lineWidth := ansi.StringWidth(line)
		if lineWidth == 0 {
			continue
		}

		lStart := 0
		lEnd := lineWidth
		if contentRow == startRow {
			lStart = startCol
		}
		if contentRow == endRow {
			lEnd = min(endCol, lineWidth)
		}
		if lStart >= lineWidth || lEnd <= lStart {
			continue
		}

		before := ansi.Cut(line, 0, lStart)
		selected := ansi.Cut(line, lStart, lEnd)
		after := ansi.Cut(line, lEnd, lineWidth)
		lines[i] = before + "\x1b[7m" + selected + "\x1b[27m" + after
	}
	return strings.Join(lines, "\n")
}
