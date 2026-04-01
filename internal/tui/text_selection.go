package tui

import (
	"strings"
	"unicode"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/x/ansi"
	rw "github.com/mattn/go-runewidth"
	"github.com/rivo/uniseg"
)

const selectionHighlightStart = "\x1b[1;38;5;16;48;5;178m"
const selectionHighlightEnd = "\x1b[22;39;49m"

type textSelection struct {
	anchorRow  int
	anchorCol  int
	currentRow int
	currentCol int
	dragging   bool
}

func (s textSelection) normalized() (startRow, startCol, endRow, endCol int) {
	if s.anchorRow < s.currentRow || (s.anchorRow == s.currentRow && s.anchorCol <= s.currentCol) {
		return s.anchorRow, s.anchorCol, s.currentRow, s.currentCol
	}
	return s.currentRow, s.currentCol, s.anchorRow, s.anchorCol
}

func (s textSelection) hasRange() bool {
	return s.anchorRow != s.currentRow || s.anchorCol != s.currentCol
}

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

func cleanCopiedText(text string) string {
	lines := strings.Split(text, "\n")

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
		if strings.HasSuffix(line, " ") || strings.HasSuffix(line, "\t") {
			joined = append(joined, buf.String())
			buf.Reset()
		}
	}
	if buf.Len() > 0 {
		joined = append(joined, buf.String())
	}

	for i, line := range joined {
		joined[i] = strings.TrimRight(line, " \t")
	}

	return strings.Join(joined, "\n")
}

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

type textSelectionDisplayLine struct {
	RawLine  int
	StartCol int
	EndCol   int
	Text     string
}

func textSelectionNavigationKey(key string) bool {
	switch key {
	case "left", "right", "up", "down", "home", "end",
		"ctrl+a", "ctrl+e", "ctrl+b", "ctrl+f", "ctrl+n", "ctrl+p",
		"alt+left", "alt+right", "alt+b", "alt+f",
		"alt+<", "alt+>", "ctrl+home", "ctrl+end":
		return true
	default:
		return false
	}
}

func textEditorCursor(editor textarea.Model) (int, int) {
	lines := textEditorLines(editor)
	line := editor.Line()
	if len(lines) == 0 {
		return 0, 0
	}
	line = max(0, min(line, len(lines)-1))
	info := editor.LineInfo()
	col := info.StartColumn + info.ColumnOffset
	runes := []rune(lines[line])
	col = max(0, min(col, len(runes)))
	return line, col
}

func textEditorLines(editor textarea.Model) []string {
	lines := strings.Split(strings.ReplaceAll(editor.Value(), "\r\n", "\n"), "\n")
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

func textSelectedContent(editor textarea.Model, startLine, startCol, endLine, endCol int) string {
	lines := textEditorLines(editor)
	startLine = max(0, min(startLine, len(lines)-1))
	endLine = max(0, min(endLine, len(lines)-1))
	startCol = max(0, min(startCol, len([]rune(lines[startLine]))))
	endCol = max(0, min(endCol, len([]rune(lines[endLine]))))
	if textCursorAfter(startLine, startCol, endLine, endCol) {
		startLine, endLine = endLine, startLine
		startCol, endCol = endCol, startCol
	}
	if startLine == endLine {
		return textSliceRunes(lines[startLine], startCol, endCol)
	}
	segments := []string{textSliceRunes(lines[startLine], startCol, len([]rune(lines[startLine])))}
	for line := startLine + 1; line < endLine; line++ {
		segments = append(segments, lines[line])
	}
	segments = append(segments, textSliceRunes(lines[endLine], 0, endCol))
	return strings.Join(segments, "\n")
}

func textSliceRunes(line string, startCol, endCol int) string {
	runes := []rune(line)
	startCol = max(0, min(startCol, len(runes)))
	endCol = max(0, min(endCol, len(runes)))
	if startCol > endCol {
		startCol, endCol = endCol, startCol
	}
	return string(runes[startCol:endCol])
}

func textCursorAfter(lineA, colA, lineB, colB int) bool {
	if lineA != lineB {
		return lineA > lineB
	}
	return colA > colB
}

func textSelectionRangeForDisplayLine(
	line textSelectionDisplayLine,
	startLine int,
	startCol int,
	endLine int,
	endCol int,
	hasSelection bool,
) (int, int, bool) {
	if !hasSelection {
		return 0, 0, false
	}
	if textCursorAfter(startLine, startCol, line.RawLine, line.EndCol) || (startLine == line.RawLine && startCol == line.EndCol) {
		return 0, 0, false
	}
	if textCursorAfter(line.RawLine, line.StartCol, endLine, endCol) || (line.RawLine == endLine && line.StartCol == endCol) {
		return 0, 0, false
	}
	selectionStart := line.StartCol
	if line.RawLine == startLine {
		selectionStart = max(selectionStart, startCol)
	}
	selectionEnd := line.EndCol
	if line.RawLine == endLine {
		selectionEnd = min(selectionEnd, endCol)
	}
	if selectionStart >= selectionEnd {
		return 0, 0, false
	}
	return selectionStart - line.StartCol, selectionEnd - line.StartCol, true
}

func textSelectionRange(editor textarea.Model, anchorSet bool, anchorLine, anchorCol int) (int, int, int, int, bool) {
	if !anchorSet {
		return 0, 0, 0, 0, false
	}
	line, col := textEditorCursor(editor)
	startLine, startCol := anchorLine, anchorCol
	endLine, endCol := line, col
	if textCursorAfter(startLine, startCol, endLine, endCol) {
		startLine, endLine = endLine, startLine
		startCol, endCol = endCol, startCol
	}
	return startLine, startCol, endLine, endCol, true
}

func textSelectionDisplayLines(editor textarea.Model) []textSelectionDisplayLine {
	lines := textEditorLines(editor)
	width := max(1, editor.Width())
	displayLines := make([]textSelectionDisplayLine, 0, len(lines))
	for lineIndex, line := range lines {
		displayLines = append(displayLines, textWrapDisplayLine(line, lineIndex, width)...)
	}
	if len(displayLines) == 0 {
		return []textSelectionDisplayLine{{}}
	}
	return displayLines
}

func textWrapDisplayLine(line string, rawLine int, width int) []textSelectionDisplayLine {
	wrapped := textWrapRunes([]rune(line), width)
	if len(wrapped) == 0 {
		return []textSelectionDisplayLine{{RawLine: rawLine}}
	}
	lineRunes := []rune(line)
	consumed := 0
	displayLines := make([]textSelectionDisplayLine, 0, len(wrapped))
	for _, wrappedLine := range wrapped {
		actualLen := min(len(wrappedLine), len(lineRunes)-consumed)
		if actualLen < 0 {
			actualLen = 0
		}
		displayLines = append(displayLines, textSelectionDisplayLine{
			RawLine:  rawLine,
			StartCol: consumed,
			EndCol:   consumed + actualLen,
			Text:     string(wrappedLine[:actualLen]),
		})
		consumed += actualLen
	}
	return displayLines
}

func textSelectionDisplayCursor(editor textarea.Model) (int, int) {
	displayLines := textSelectionDisplayLines(editor)
	line, col := textEditorCursor(editor)
	for row, displayLine := range displayLines {
		if displayLine.RawLine != line {
			continue
		}
		if col < displayLine.StartCol || col > displayLine.EndCol {
			continue
		}
		if col == displayLine.EndCol && row+1 < len(displayLines) && displayLines[row+1].RawLine == line && displayLines[row+1].StartCol == col {
			continue
		}
		return row, col - displayLine.StartCol
	}
	return 0, 0
}

func textSelectionInitialViewport(editor textarea.Model) int {
	cursorRow, _ := textSelectionDisplayCursor(editor)
	return max(0, cursorRow-editor.Height()+1)
}

func textSelectionViewportForCursor(editor textarea.Model, offset int) int {
	displayLines := textSelectionDisplayLines(editor)
	height := max(1, editor.Height())
	maxOffset := max(0, len(displayLines)-height)
	if offset > maxOffset {
		offset = maxOffset
	}
	cursorRow, _ := textSelectionDisplayCursor(editor)
	if cursorRow < offset {
		offset = cursorRow
	}
	if cursorRow >= offset+height {
		offset = cursorRow - height + 1
	}
	if offset < 0 {
		offset = 0
	}
	if offset > maxOffset {
		offset = maxOffset
	}
	return offset
}

func textWrapRunes(runes []rune, width int) [][]rune {
	if width <= 0 {
		return [][]rune{runes}
	}
	var (
		lines  = [][]rune{{}}
		word   = []rune{}
		row    int
		spaces int
	)

	for _, r := range runes {
		if unicode.IsSpace(r) {
			spaces++
		} else {
			word = append(word, r)
		}

		if spaces > 0 {
			if uniseg.StringWidth(string(lines[row]))+uniseg.StringWidth(string(word))+spaces > width {
				row++
				lines = append(lines, []rune{})
				lines[row] = append(lines[row], word...)
				lines[row] = append(lines[row], repeatSpaces(spaces)...)
				spaces = 0
				word = nil
			} else {
				lines[row] = append(lines[row], word...)
				lines[row] = append(lines[row], repeatSpaces(spaces)...)
				spaces = 0
				word = nil
			}
		} else if len(word) > 0 {
			lastCharLen := rw.RuneWidth(word[len(word)-1])
			if uniseg.StringWidth(string(word))+lastCharLen > width {
				if len(lines[row]) > 0 {
					row++
					lines = append(lines, []rune{})
				}
				lines[row] = append(lines[row], word...)
				word = nil
			}
		}
	}

	if uniseg.StringWidth(string(lines[row]))+uniseg.StringWidth(string(word))+spaces >= width {
		lines = append(lines, []rune{})
		lines[row+1] = append(lines[row+1], word...)
		spaces++
		lines[row+1] = append(lines[row+1], repeatSpaces(spaces)...)
	} else {
		lines[row] = append(lines[row], word...)
		spaces++
		lines[row] = append(lines[row], repeatSpaces(spaces)...)
	}

	return lines
}

func repeatSpaces(n int) []rune {
	return []rune(strings.Repeat(" ", n))
}
