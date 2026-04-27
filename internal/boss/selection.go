package boss

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

const (
	bossSelectionHighlightStart = "\x1b[1;38;5;16;48;5;178m"
	bossSelectionHighlightEnd   = "\x1b[22;39;49m"
)

type bossTextSelection struct {
	anchorRow  int
	anchorCol  int
	currentRow int
	currentCol int
	dragging   bool
}

func (s bossTextSelection) normalized() (startRow, startCol, endRow, endCol int) {
	if s.anchorRow < s.currentRow || (s.anchorRow == s.currentRow && s.anchorCol <= s.currentCol) {
		return s.anchorRow, s.anchorCol, s.currentRow, s.currentCol
	}
	return s.currentRow, s.currentCol, s.anchorRow, s.anchorCol
}

func (s bossTextSelection) hasRange() bool {
	return s.anchorRow != s.currentRow || s.anchorCol != s.currentCol
}

func (s bossTextSelection) extractText(fullContent string) string {
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
			colEnd = minInt(endCol, lineWidth)
		}
		if colStart >= lineWidth {
			result = append(result, "")
			continue
		}
		result = append(result, ansi.Cut(plain, colStart, colEnd))
	}
	return strings.Join(result, "\n")
}

func overlayBossSelectionHighlight(viewportOutput string, sel bossTextSelection, yOffset int) string {
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
			lEnd = minInt(endCol, lineWidth)
		}
		if lStart >= lineWidth || lEnd <= lStart {
			continue
		}

		before := ansi.Cut(line, 0, lStart)
		selected := ansi.Strip(ansi.Cut(line, lStart, lEnd))
		after := ansi.Cut(line, lEnd, lineWidth)
		lines[i] = before + bossSelectionHighlightStart + selected + bossSelectionHighlightEnd + after
	}
	return strings.Join(lines, "\n")
}

func (m Model) updateMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if cmd, handled := m.handleChatMouseSelection(msg); handled {
		return m, cmd
	}
	if m.chatSelection.dragging {
		m.finalizeChatSelection()
	}
	if msg.Action == tea.MouseActionPress &&
		(msg.Button == tea.MouseButtonWheelUp || msg.Button == tea.MouseButtonWheelDown ||
			msg.Button == tea.MouseButtonWheelLeft || msg.Button == tea.MouseButtonWheelRight) {
		var cmd tea.Cmd
		m.chatViewport, cmd = m.chatViewport.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m *Model) handleChatMouseSelection(msg tea.MouseMsg) (tea.Cmd, bool) {
	switch msg.Action {
	case tea.MouseActionPress:
		if m.chatSelection.dragging {
			m.finalizeChatSelection()
		}
		if msg.Button != tea.MouseButtonLeft {
			m.chatSelection = bossTextSelection{}
			return nil, false
		}
		row, col, ok := m.chatMouseToContent(msg.X, msg.Y)
		if !ok {
			m.chatSelection = bossTextSelection{}
			return nil, false
		}
		m.chatSelection = bossTextSelection{
			anchorRow:  row,
			anchorCol:  col,
			currentRow: row,
			currentCol: col,
			dragging:   true,
		}
		return nil, true
	case tea.MouseActionMotion:
		if !m.chatSelection.dragging {
			return nil, false
		}
		row, col, ok := m.chatMouseToContent(msg.X, msg.Y)
		if ok {
			m.chatSelection.currentRow = row
			m.chatSelection.currentCol = col
		}
		return nil, true
	case tea.MouseActionRelease:
		if !m.chatSelection.dragging {
			return nil, false
		}
		row, col, ok := m.chatMouseToContent(msg.X, msg.Y)
		if ok {
			m.chatSelection.currentRow = row
			m.chatSelection.currentCol = col
		}
		m.finalizeChatSelection()
		return nil, true
	default:
		return nil, false
	}
}

func (m *Model) chatMouseToContent(screenX, screenY int) (row, col int, ok bool) {
	layout := m.layout()
	bodyY := screenY
	if !m.embedded {
		bodyY--
	}
	if bodyY < bossChatTranscriptTop || bodyY >= bossChatTranscriptTop+layout.transcriptHeight {
		return 0, 0, false
	}
	if bodyY >= layout.topHeight {
		return 0, 0, false
	}
	if screenX < bossPanelContentLeft || screenX >= bossPanelContentLeft+layout.chatInnerWidth {
		return 0, 0, false
	}
	contentRow := bodyY - bossChatTranscriptTop + m.chatViewport.YOffset
	if contentRow >= m.chatViewport.TotalLineCount() {
		return 0, 0, false
	}
	return contentRow, screenX - bossPanelContentLeft, true
}

func (m *Model) finalizeChatSelection() {
	m.chatSelection.dragging = false
	if !m.chatSelection.hasRange() {
		return
	}
	text := cleanBossCopiedText(m.chatSelection.extractText(m.renderTranscript(m.layout().chatInnerWidth)))
	if text == "" {
		return
	}
	if err := clipboardTextWriter(text); err != nil {
		m.status = "Chat selection copy failed: " + err.Error()
		return
	}
	m.status = "Copied chat selection to clipboard"
}

func cleanBossCopiedText(text string) string {
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

const (
	bossPanelContentLeft  = 2
	bossChatTranscriptTop = 2
)
