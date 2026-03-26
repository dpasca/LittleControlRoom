package tui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// selectionHighlightStart sets bold, black text on a muted gold background.
// Uses raw ANSI escapes rather than lipgloss so the highlight works reliably
// when injected into already-styled content.
const selectionHighlightStart = "\x1b[1;38;5;16;48;5;178m"

// selectionHighlightEnd resets only the attributes we changed (bold off,
// default fg, default bg) without a full reset, so surrounding ANSI state
// in the "after" segment is preserved.
const selectionHighlightEnd = "\x1b[22;39;49m"

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

// cleanCopiedText post-processes extracted selection text for a cleaner
// clipboard result:
//  1. Strip 1 leading space from each line (from viewport PaddingLeft).
//  2. Join soft-wrapped lines: when a line does NOT end with trailing whitespace
//     (meaning it filled the viewport width) and the next line is non-empty,
//     they belong to the same paragraph and are merged.
//  3. Strip trailing whitespace from each line.
func cleanCopiedText(text string) string {
	lines := strings.Split(text, "\n")

	// Step 1: strip 1 leading space per line.
	for i, line := range lines {
		lines[i] = strings.TrimPrefix(line, " ")
	}

	// Step 2: join soft-wrapped lines. A line that does NOT end with
	// whitespace was full-width (soft-wrapped); merge it with the next
	// non-empty line. This must happen before trailing-space stripping.
	joined := make([]string, 0, len(lines))
	var buf strings.Builder
	for _, line := range lines {
		if line == "" {
			if buf.Len() > 0 {
				joined = append(joined, buf.String())
				buf.Reset()
			}
			joined = append(joined, "")
			continue
		}
		if buf.Len() > 0 {
			buf.WriteByte(' ')
		}
		buf.WriteString(line)
		// If the original line ends with whitespace, it was shorter than
		// the viewport width — treat it as a natural line break.
		if strings.HasSuffix(line, " ") || strings.HasSuffix(line, "\t") {
			joined = append(joined, buf.String())
			buf.Reset()
		}
	}
	if buf.Len() > 0 {
		joined = append(joined, buf.String())
	}

	// Step 3: strip trailing whitespace from each line.
	for i, line := range joined {
		joined[i] = strings.TrimRight(line, " \t")
	}

	return strings.Join(joined, "\n")
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
		selected := ansi.Strip(ansi.Cut(line, lStart, lEnd))
		after := ansi.Cut(line, lEnd, lineWidth)
		lines[i] = before + selectionHighlightStart + selected + selectionHighlightEnd + after
	}
	return strings.Join(lines, "\n")
}
