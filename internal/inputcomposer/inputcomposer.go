package inputcomposer

import (
	"reflect"
	"strings"
	"unicode"
	"unsafe"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	rw "github.com/mattn/go-runewidth"
	"github.com/rivo/uniseg"
)

type CopyChoice int

const (
	CopyChoiceAll CopyChoice = iota
	CopyChoiceSelection
)

type CopyDialogState struct {
	Selected CopyChoice
}

func NewCopyDialogState() *CopyDialogState {
	return &CopyDialogState{Selected: CopyChoiceAll}
}

func (s *CopyDialogState) Move(delta int) {
	if s == nil || delta == 0 {
		return
	}
	choices := CopyChoices()
	current := 0
	for i, choice := range choices {
		if choice == s.Selected {
			current = i
			break
		}
	}
	current = (current + delta + len(choices)) % len(choices)
	s.Selected = choices[current]
}

func CopyChoices() []CopyChoice {
	return []CopyChoice{CopyChoiceAll, CopyChoiceSelection}
}

func CopyChoiceLabel(choice CopyChoice) string {
	switch choice {
	case CopyChoiceSelection:
		return "Select to copy"
	default:
		return "Copy all"
	}
}

func CopyChoiceSummary(choice CopyChoice) string {
	switch choice {
	case CopyChoiceSelection:
		return "Move through the input, mark the start and end with Space, then copy only that range."
	default:
		return "Copy the full current input text to the clipboard."
	}
}

type SelectionState struct {
	AnchorSet  bool
	AnchorLine int
	AnchorCol  int
	ViewportY  int
}

func NewSelectionState(input textarea.Model) *SelectionState {
	return &SelectionState{ViewportY: SelectionInitialViewport(input)}
}

func NavigationKey(key string) bool {
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

func EditorCursor(editor textarea.Model) (int, int) {
	lines := editorLines(editor)
	line := editor.Line()
	if len(lines) == 0 {
		return 0, 0
	}
	line = maxInt(0, minInt(line, len(lines)-1))
	info := editor.LineInfo()
	col := info.StartColumn + info.ColumnOffset
	runes := []rune(lines[line])
	col = maxInt(0, minInt(col, len(runes)))
	return line, col
}

func SelectedContent(editor textarea.Model, startLine, startCol, endLine, endCol int) string {
	lines := editorLines(editor)
	startLine = maxInt(0, minInt(startLine, len(lines)-1))
	endLine = maxInt(0, minInt(endLine, len(lines)-1))
	startCol = maxInt(0, minInt(startCol, len([]rune(lines[startLine]))))
	endCol = maxInt(0, minInt(endCol, len([]rune(lines[endLine]))))
	if cursorAfter(startLine, startCol, endLine, endCol) {
		startLine, endLine = endLine, startLine
		startCol, endCol = endCol, startCol
	}
	if startLine == endLine {
		return sliceRunes(lines[startLine], startCol, endCol)
	}
	segments := []string{sliceRunes(lines[startLine], startCol, len([]rune(lines[startLine])))}
	for line := startLine + 1; line < endLine; line++ {
		segments = append(segments, lines[line])
	}
	segments = append(segments, sliceRunes(lines[endLine], 0, endCol))
	return strings.Join(segments, "\n")
}

func SelectionInitialViewport(editor textarea.Model) int {
	cursorRow, _ := displayCursor(editor)
	return maxInt(0, cursorRow-editor.Height()+1)
}

func SelectionViewportForCursor(editor textarea.Model, offset int) int {
	displayLines := displayLines(editor)
	height := maxInt(1, editor.Height())
	maxOffset := maxInt(0, len(displayLines)-height)
	if offset > maxOffset {
		offset = maxOffset
	}
	cursorRow, _ := displayCursor(editor)
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

type SelectionStyles struct {
	Line        lipgloss.Style
	CursorLine  lipgloss.Style
	Range       lipgloss.Style
	Cursor      lipgloss.Style
	CursorRange lipgloss.Style
}

func RenderSelectionEditor(input textarea.Model, sel *SelectionState, styles SelectionStyles) string {
	if sel == nil {
		return input.View()
	}
	displayLines := displayLines(input)
	height := maxInt(1, input.Height())
	width := maxInt(1, input.Width())
	offset := SelectionViewportForCursor(input, sel.ViewportY)
	maxOffset := maxInt(0, len(displayLines)-height)
	if offset > maxOffset {
		offset = maxOffset
	}

	cursorRow, cursorCol := displayCursor(input)
	startLine, startCol, endLine, endCol, hasSelection := selectionRange(input, sel.AnchorSet, sel.AnchorLine, sel.AnchorCol)

	lines := make([]string, 0, height)
	for row := 0; row < height; row++ {
		index := offset + row
		if index >= len(displayLines) {
			lines = append(lines, styles.Line.Render(strings.Repeat(" ", width)))
			continue
		}
		line := displayLines[index]
		lines = append(lines, renderSelectionLine(line, width, index == cursorRow, cursorCol, startLine, startCol, endLine, endCol, hasSelection, styles))
	}
	return strings.Join(lines, "\n")
}

func CleanCopiedText(text string) string {
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

func ShouldIgnoreWordBackward(input *textarea.Model, msg tea.KeyMsg) bool {
	if !wordBackwardKey(msg) {
		return false
	}
	rows, row, col, ok := textareaState(input)
	if !ok || len(rows) == 0 {
		return false
	}
	row = maxInt(0, minInt(row, len(rows)-1))
	col = maxInt(0, minInt(col, len(rows[row])))

	for {
		prevRow, prevCol := row, col
		row, col = textareaCharacterLeft(rows, row, col, true)
		if col < len(rows[row]) && !unicode.IsSpace(rows[row][col]) {
			return false
		}
		if row == prevRow && col == prevCol {
			return true
		}
	}
}

type displayLine struct {
	RawLine  int
	StartCol int
	EndCol   int
	Text     string
}

func editorLines(editor textarea.Model) []string {
	lines := strings.Split(strings.ReplaceAll(editor.Value(), "\r\n", "\n"), "\n")
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

func sliceRunes(line string, startCol, endCol int) string {
	runes := []rune(line)
	startCol = maxInt(0, minInt(startCol, len(runes)))
	endCol = maxInt(0, minInt(endCol, len(runes)))
	if startCol > endCol {
		startCol, endCol = endCol, startCol
	}
	return string(runes[startCol:endCol])
}

func cursorAfter(lineA, colA, lineB, colB int) bool {
	if lineA != lineB {
		return lineA > lineB
	}
	return colA > colB
}

func selectionRangeForDisplayLine(line displayLine, startLine, startCol, endLine, endCol int, hasSelection bool) (int, int, bool) {
	if !hasSelection {
		return 0, 0, false
	}
	if cursorAfter(startLine, startCol, line.RawLine, line.EndCol) || (startLine == line.RawLine && startCol == line.EndCol) {
		return 0, 0, false
	}
	if cursorAfter(line.RawLine, line.StartCol, endLine, endCol) || (line.RawLine == endLine && line.StartCol == endCol) {
		return 0, 0, false
	}
	selectionStart := line.StartCol
	if line.RawLine == startLine {
		selectionStart = maxInt(selectionStart, startCol)
	}
	selectionEnd := line.EndCol
	if line.RawLine == endLine {
		selectionEnd = minInt(selectionEnd, endCol)
	}
	if selectionStart >= selectionEnd {
		return 0, 0, false
	}
	return selectionStart - line.StartCol, selectionEnd - line.StartCol, true
}

func selectionRange(editor textarea.Model, anchorSet bool, anchorLine, anchorCol int) (int, int, int, int, bool) {
	if !anchorSet {
		return 0, 0, 0, 0, false
	}
	line, col := EditorCursor(editor)
	startLine, startCol := anchorLine, anchorCol
	endLine, endCol := line, col
	if cursorAfter(startLine, startCol, endLine, endCol) {
		startLine, endLine = endLine, startLine
		startCol, endCol = endCol, startCol
	}
	return startLine, startCol, endLine, endCol, true
}

func displayLines(editor textarea.Model) []displayLine {
	lines := editorLines(editor)
	width := maxInt(1, editor.Width())
	displayLines := make([]displayLine, 0, len(lines))
	for lineIndex, line := range lines {
		displayLines = append(displayLines, wrapDisplayLine(line, lineIndex, width)...)
	}
	if len(displayLines) == 0 {
		return []displayLine{{}}
	}
	return displayLines
}

func wrapDisplayLine(line string, rawLine int, width int) []displayLine {
	wrapped := wrapRunes([]rune(line), width)
	if len(wrapped) == 0 {
		return []displayLine{{RawLine: rawLine}}
	}
	lineRunes := []rune(line)
	consumed := 0
	displayLines := make([]displayLine, 0, len(wrapped))
	for _, wrappedLine := range wrapped {
		actualLen := minInt(len(wrappedLine), len(lineRunes)-consumed)
		if actualLen < 0 {
			actualLen = 0
		}
		displayLines = append(displayLines, displayLine{
			RawLine:  rawLine,
			StartCol: consumed,
			EndCol:   consumed + actualLen,
			Text:     string(wrappedLine[:actualLen]),
		})
		consumed += actualLen
	}
	return displayLines
}

func displayCursor(editor textarea.Model) (int, int) {
	displayLines := displayLines(editor)
	line, col := EditorCursor(editor)
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

func renderSelectionLine(
	line displayLine,
	width int,
	cursorVisible bool,
	cursorCol int,
	startLine int,
	startCol int,
	endLine int,
	endCol int,
	hasSelection bool,
	styles SelectionStyles,
) string {
	baseStyle := styles.Line
	if cursorVisible {
		baseStyle = styles.CursorLine
	}
	runes := []rune(line.Text)
	selStart, selEnd, ok := selectionRangeForDisplayLine(line, startLine, startCol, endLine, endCol, hasSelection)

	var out strings.Builder
	lineWidth := 0
	for index, r := range runes {
		style := baseStyle
		if ok && index >= selStart && index < selEnd {
			style = styles.Range
		}
		if cursorVisible && index == cursorCol {
			if ok && index >= selStart && index < selEnd {
				style = styles.CursorRange
			} else {
				style = styles.Cursor
			}
		}
		out.WriteString(style.Render(string(r)))
		lineWidth += rw.RuneWidth(r)
	}
	if cursorVisible && cursorCol == len(runes) {
		style := styles.Cursor
		if ok && cursorCol >= selStart && cursorCol < selEnd {
			style = styles.CursorRange
		}
		out.WriteString(style.Render(" "))
		lineWidth++
	}
	if lineWidth < width {
		out.WriteString(baseStyle.Render(strings.Repeat(" ", width-lineWidth)))
	}
	return out.String()
}

func wrapRunes(runes []rune, width int) [][]rune {
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

func wordBackwardKey(msg tea.KeyMsg) bool {
	switch msg.String() {
	case "alt+b", "alt+left":
		return true
	default:
		return false
	}
}

func textareaCharacterLeft(rows [][]rune, row, col int, insideLine bool) (int, int) {
	if col == 0 && row != 0 {
		row--
		col = len(rows[row])
		if !insideLine {
			return row, col
		}
	}
	if col > 0 {
		col--
	}
	return row, col
}

func textareaState(input *textarea.Model) ([][]rune, int, int, bool) {
	if input == nil {
		return nil, 0, 0, false
	}

	valueField, ok := textareaField(input, "value")
	if !ok {
		return nil, 0, 0, false
	}
	rowField, ok := textareaField(input, "row")
	if !ok {
		return nil, 0, 0, false
	}
	colField, ok := textareaField(input, "col")
	if !ok {
		return nil, 0, 0, false
	}

	rows, ok := valueField.Interface().([][]rune)
	if !ok {
		return nil, 0, 0, false
	}
	return rows, int(rowField.Int()), int(colField.Int()), true
}

func textareaField(input *textarea.Model, name string) (reflect.Value, bool) {
	value := reflect.ValueOf(input)
	if value.Kind() != reflect.Ptr || value.IsNil() {
		return reflect.Value{}, false
	}
	field := value.Elem().FieldByName(name)
	if !field.IsValid() || !field.CanAddr() {
		return reflect.Value{}, false
	}
	return reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem(), true
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
