package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	rw "github.com/mattn/go-runewidth"
)

// codexInputSelectionState tracks keyboard-driven text selection state for the
// codex composer (Alt+S mode).
type codexInputSelectionState struct {
	AnchorSet  bool
	AnchorLine int
	AnchorCol  int
	ViewportY  int
}

// Prompt width used by the codex textarea ("> " or "  ").
const codexComposerPromptWidth = 2

// Padding(0,1) adds 1 column on each side.
const codexComposerLeftPadding = 1

var (
	codexSelLineStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Background(codexComposerShellColor).Inline(true)
	codexSelCursorLineStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Background(codexComposerCursorLineColor).Inline(true)
	codexSelRangeStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("16")).Background(lipgloss.Color("178")).Bold(true).Inline(true)
	codexSelCursorStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("16")).Background(lipgloss.Color("214")).Bold(true).Inline(true)
	codexSelCursorRangeStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("16")).Background(lipgloss.Color("222")).Bold(true).Inline(true)
)

// --- Keyboard selection (Alt+S) ---

func (m *Model) startCodexInputSelection() {
	m.codexInputSelection = &codexInputSelectionState{
		ViewportY: textSelectionInitialViewport(m.codexInput),
	}
	m.status = "Selection mode: move to the start and press Space"
}

func (m *Model) stopCodexInputSelection() {
	m.codexInputSelection = nil
}

func (m Model) codexInputSelectionActive() bool {
	return m.codexInputSelection != nil
}

func (m Model) updateCodexInputSelectionMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	sel := m.codexInputSelection
	if sel == nil {
		return m, nil
	}

	switch msg.String() {
	case "esc":
		m.stopCodexInputSelection()
		m.status = "Text selection canceled"
		return m, nil
	case " ":
		return m, m.toggleCodexInputSelectionMark()
	}

	if !textSelectionNavigationKey(msg.String()) {
		return m, nil
	}

	if codexShouldIgnoreTextareaWordBackward(&m.codexInput, msg) {
		return m, nil
	}

	var cmd tea.Cmd
	m.codexInput, cmd = m.codexInput.Update(msg)
	sel.ViewportY = textSelectionViewportForCursor(m.codexInput, sel.ViewportY)
	return m, cmd
}

func (m *Model) toggleCodexInputSelectionMark() tea.Cmd {
	sel := m.codexInputSelection
	if sel == nil {
		return nil
	}

	line, col := textEditorCursor(m.codexInput)

	if !sel.AnchorSet {
		sel.AnchorSet = true
		sel.AnchorLine = line
		sel.AnchorCol = col
		m.status = "Selection start set. Move to the end and press Space again to copy."
		return nil
	}

	text := cleanCopiedText(textSelectedContent(m.codexInput, sel.AnchorLine, sel.AnchorCol, line, col))
	if text == "" {
		m.status = "Selection is empty. Move the cursor and press Space again."
		return nil
	}

	if err := clipboardTextWriter(text); err != nil {
		m.reportError("Copy failed", err, m.codexVisibleProject)
		return nil
	}

	m.stopCodexInputSelection()
	m.status = "Copied selected text to clipboard"
	return nil
}

// --- Mouse selection ---

// codexComposerScreenTop returns the screen Y where the composer starts.
func (m Model) codexComposerScreenTop() int {
	composerH := max(3, min(10, m.codexInput.LineCount()+1))
	return m.height - composerH
}

// handleCodexComposerMouseSelection processes mouse events for text selection
// in the codex composer input area.
func (m *Model) handleCodexComposerMouseSelection(msg tea.MouseMsg) (tea.Cmd, bool) {
	composerTop := m.codexComposerScreenTop()
	composerH := max(3, min(10, m.codexInput.LineCount()+1))

	switch msg.Action {
	case tea.MouseActionPress:
		if m.codexComposerSelection.dragging {
			m.finalizeCodexComposerSelection()
		}
		if msg.Button != tea.MouseButtonLeft {
			m.codexComposerSelection = textSelection{}
			return nil, false
		}
		row, col, ok := m.codexComposerMouseToContent(msg.X, msg.Y, composerTop, composerH)
		if !ok {
			return nil, false
		}
		m.codexComposerSelection = textSelection{
			anchorRow:  row,
			anchorCol:  col,
			currentRow: row,
			currentCol: col,
			dragging:   true,
		}
		return nil, true

	case tea.MouseActionMotion:
		if !m.codexComposerSelection.dragging {
			return nil, false
		}
		row, col, ok := m.codexComposerMouseToContent(msg.X, msg.Y, composerTop, composerH)
		if ok {
			m.codexComposerSelection.currentRow = row
			m.codexComposerSelection.currentCol = col
		}
		return nil, true

	case tea.MouseActionRelease:
		if !m.codexComposerSelection.dragging {
			return nil, false
		}
		row, col, ok := m.codexComposerMouseToContent(msg.X, msg.Y, composerTop, composerH)
		if ok {
			m.codexComposerSelection.currentRow = row
			m.codexComposerSelection.currentCol = col
		}
		m.finalizeCodexComposerSelection()
		return nil, true
	}
	return nil, false
}

// codexComposerMouseToContent maps screen coordinates to content row/col
// within the composer textarea. Accounts for left padding and prompt width.
func (m *Model) codexComposerMouseToContent(screenX, screenY, composerTop, composerH int) (row, col int, ok bool) {
	visLine := screenY - composerTop
	if visLine < 0 || visLine >= composerH {
		return 0, 0, false
	}
	contentRow := visLine
	lines := strings.Split(m.codexInput.Value(), "\n")
	if contentRow >= len(lines) {
		return 0, 0, false
	}
	// Subtract left padding + prompt width to get the content column.
	col = max(0, screenX-codexComposerLeftPadding-codexComposerPromptWidth)
	lineRunes := []rune(lines[contentRow])
	col = min(col, len(lineRunes))
	return contentRow, col, true
}

func (m *Model) finalizeCodexComposerSelection() {
	m.codexComposerSelection.dragging = false
	if m.codexComposerSelection.hasRange() {
		text := cleanCopiedText(m.codexComposerSelection.extractText(m.codexInput.Value()))
		if text != "" {
			if err := clipboardTextWriter(text); err == nil {
				m.status = "Copied composer selection to clipboard"
			} else {
				m.reportError("Selection copy failed", err, m.codexVisibleProject)
			}
		}
	}
}

// --- Rendering ---

// renderCodexComposerWithMouseSelection renders the composer with a
// per-character highlight for the mouse-driven selection. Unlike the ANSI
// overlay approach, this builds each line character-by-character so the
// highlight is guaranteed to be visible over the composer background.
func renderCodexComposerWithMouseSelection(input textarea.Model, sel textSelection, width int) string {
	if width <= 0 {
		width = input.Width() + 4
	}
	innerWidth := max(20, width-4)

	lines := strings.Split(input.Value(), "\n")
	height := max(1, input.Height())
	startRow, startCol, endRow, endCol := sel.normalized()

	promptStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("81")).Bold(true).Background(codexComposerShellColor).Inline(true)
	contPromptStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("81")).Background(codexComposerShellColor).Inline(true)
	baseStyle := codexSelLineStyle

	rendered := make([]string, 0, height)
	for row := 0; row < height; row++ {
		var out strings.Builder
		// Prompt
		if row == 0 {
			out.WriteString(promptStyle.Render("> "))
		} else {
			out.WriteString(contPromptStyle.Render("  "))
		}

		lineWidth := codexComposerPromptWidth
		if row < len(lines) {
			runes := []rune(lines[row])
			for ci, r := range runes {
				style := baseStyle
				if isInMouseSelection(row, ci, startRow, startCol, endRow, endCol) {
					style = codexSelRangeStyle
				}
				out.WriteString(style.Render(string(r)))
				lineWidth += rw.RuneWidth(r)
			}
		}
		// Fill remaining width
		if lineWidth < innerWidth {
			out.WriteString(baseStyle.Render(strings.Repeat(" ", innerWidth-lineWidth)))
		}
		rendered = append(rendered, out.String())
	}

	content := strings.Join(rendered, "\n")
	return lipgloss.NewStyle().
		Width(width).
		Padding(0, 1).
		Background(codexComposerShellColor).
		Render(content)
}

func isInMouseSelection(row, col, startRow, startCol, endRow, endCol int) bool {
	if row < startRow || row > endRow {
		return false
	}
	if row == startRow && col < startCol {
		return false
	}
	if row == endRow && col >= endCol {
		return false
	}
	return true
}

// renderCodexComposerWithSelection renders the composer with a per-character
// highlight for the keyboard-driven selection (Alt+S mode).
func renderCodexComposerWithSelection(input textarea.Model, sel *codexInputSelectionState, width int) string {
	if width <= 0 {
		width = input.Width() + 4
	}
	innerWidth := max(20, width-4)
	input.SetWidth(innerWidth)

	editorView := renderCodexInputSelectionEditor(input, sel)
	return lipgloss.NewStyle().
		Width(width).
		Padding(0, 1).
		Background(codexComposerShellColor).
		Foreground(lipgloss.Color("252")).
		Render(editorView)
}

func renderCodexInputSelectionEditor(input textarea.Model, sel *codexInputSelectionState) string {
	displayLines := textSelectionDisplayLines(input)
	height := max(1, input.Height())
	width := max(1, input.Width())
	offset := textSelectionViewportForCursor(input, sel.ViewportY)
	maxOffset := max(0, len(displayLines)-height)
	if offset > maxOffset {
		offset = maxOffset
	}

	cursorRow, cursorCol := textSelectionDisplayCursor(input)
	startLine, startCol, endLine, endCol, hasSelection := textSelectionRange(input, sel.AnchorSet, sel.AnchorLine, sel.AnchorCol)

	lines := make([]string, 0, height)
	for row := 0; row < height; row++ {
		index := offset + row
		if index >= len(displayLines) {
			lines = append(lines, codexSelLineStyle.Render(strings.Repeat(" ", width)))
			continue
		}
		line := displayLines[index]
		lines = append(lines, renderCodexInputSelectionLine(line, width, index == cursorRow, cursorCol, startLine, startCol, endLine, endCol, hasSelection))
	}
	return strings.Join(lines, "\n")
}

func renderCodexInputSelectionLine(
	line textSelectionDisplayLine,
	width int,
	cursorVisible bool,
	cursorCol int,
	startLine int,
	startCol int,
	endLine int,
	endCol int,
	hasSelection bool,
) string {
	baseStyle := codexSelLineStyle
	if cursorVisible {
		baseStyle = codexSelCursorLineStyle
	}
	runes := []rune(line.Text)
	selStart, selEnd, ok := textSelectionRangeForDisplayLine(line, startLine, startCol, endLine, endCol, hasSelection)

	var out strings.Builder
	lineWidth := 0
	for index, r := range runes {
		style := baseStyle
		if ok && index >= selStart && index < selEnd {
			style = codexSelRangeStyle
		}
		if cursorVisible && index == cursorCol {
			if ok && index >= selStart && index < selEnd {
				style = codexSelCursorRangeStyle
			} else {
				style = codexSelCursorStyle
			}
		}
		out.WriteString(style.Render(string(r)))
		lineWidth += rw.RuneWidth(r)
	}
	if cursorVisible && cursorCol == len(runes) {
		style := codexSelCursorStyle
		if ok && cursorCol >= selStart && cursorCol < selEnd {
			style = codexSelCursorRangeStyle
		}
		out.WriteString(style.Render(" "))
		lineWidth++
	}
	if lineWidth < width {
		out.WriteString(baseStyle.Render(strings.Repeat(" ", width-lineWidth)))
	}
	return out.String()
}
