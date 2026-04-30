package boss

import (
	"strings"

	"lcroom/internal/inputcomposer"
	"lcroom/internal/uistyle"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var (
	bossInputSelectionLineStyle        = lipgloss.NewStyle().Foreground(bossPanelText).Background(bossInputBackground).Inline(true)
	bossInputSelectionCursorLineStyle  = lipgloss.NewStyle().Foreground(bossPanelText).Background(bossInputCursorLineBackground).Inline(true)
	bossInputSelectionRangeStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("16")).Background(lipgloss.Color("178")).Bold(true).Inline(true)
	bossInputSelectionCursorStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("16")).Background(lipgloss.Color("214")).Bold(true).Inline(true)
	bossInputSelectionCursorRangeStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("16")).Background(lipgloss.Color("222")).Bold(true).Inline(true)
)

func (m *Model) openInputCopyDialog() {
	m.inputCopyDialog = inputcomposer.NewCopyDialogState()
	m.status = "Choose how to copy the boss chat input"
}

func (m *Model) closeInputCopyDialog(status string) {
	m.inputCopyDialog = nil
	if strings.TrimSpace(status) != "" {
		m.status = status
	}
}

func (m *Model) startInputSelection() {
	m.inputSelection = inputcomposer.NewSelectionState(m.input)
	m.status = "Selection mode: move to the start and press Space"
}

func (m *Model) stopInputSelection() {
	m.inputSelection = nil
}

func (m Model) updateInputCopyDialog(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.inputCopyDialog == nil {
		return m, nil
	}
	switch msg.String() {
	case "esc", "ctrl+c":
		m.closeInputCopyDialog("Input copy canceled")
		return m, nil
	case "tab", "down", "right", "j":
		m.inputCopyDialog.Move(1)
		return m, nil
	case "shift+tab", "up", "left", "k":
		m.inputCopyDialog.Move(-1)
		return m, nil
	case "a":
		m.inputCopyDialog.Selected = inputcomposer.CopyChoiceAll
		return m.applyInputCopyChoice()
	case "s":
		m.inputCopyDialog.Selected = inputcomposer.CopyChoiceSelection
		return m.applyInputCopyChoice()
	case "enter":
		return m.applyInputCopyChoice()
	default:
		return m, nil
	}
}

func (m Model) applyInputCopyChoice() (tea.Model, tea.Cmd) {
	if m.inputCopyDialog == nil {
		return m, nil
	}
	choice := m.inputCopyDialog.Selected
	m.inputCopyDialog = nil
	switch choice {
	case inputcomposer.CopyChoiceSelection:
		m.startInputSelection()
		m.syncLayout(false)
		return m, nil
	default:
		return m.copyInputToClipboard()
	}
}

func (m Model) updateInputSelection(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	sel := m.inputSelection
	if sel == nil {
		return m, nil
	}
	switch msg.String() {
	case "esc":
		m.stopInputSelection()
		m.status = "Text selection canceled"
		m.syncLayout(false)
		return m, nil
	case "alt+c":
		m.stopInputSelection()
		m.openInputCopyDialog()
		m.syncLayout(false)
		return m, nil
	case " ":
		return m.toggleInputSelectionMark()
	}
	if !inputcomposer.NavigationKey(msg.String()) {
		return m, nil
	}
	if inputcomposer.ShouldIgnoreWordBackward(&m.input, msg) {
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	sel.ViewportY = inputcomposer.SelectionViewportForCursor(m.input, sel.ViewportY)
	m.syncLayout(false)
	return m, cmd
}

func (m Model) toggleInputSelectionMark() (tea.Model, tea.Cmd) {
	sel := m.inputSelection
	if sel == nil {
		return m, nil
	}
	line, col := inputcomposer.EditorCursor(m.input)
	if !sel.AnchorSet {
		sel.AnchorSet = true
		sel.AnchorLine = line
		sel.AnchorCol = col
		m.status = "Selection start set. Move to the end and press Space again to copy."
		m.syncLayout(false)
		return m, nil
	}
	text := inputcomposer.CleanCopiedText(inputcomposer.SelectedContent(m.input, sel.AnchorLine, sel.AnchorCol, line, col))
	if text == "" {
		m.status = "Selection is empty. Move the cursor and press Space again."
		m.syncLayout(false)
		return m, nil
	}
	if err := clipboardTextWriter(text); err != nil {
		m.status = "Boss chat selection copy failed: " + err.Error()
		return m, nil
	}
	m.stopInputSelection()
	m.status = "Copied selected text to clipboard"
	m.syncLayout(false)
	return m, nil
}

func renderBossInputWithSelection(input textarea.Model, sel *inputcomposer.SelectionState, width int) string {
	if sel == nil {
		return renderBossInput(input, width)
	}
	input.SetWidth(maxInt(20, width))
	editorView := inputcomposer.RenderSelectionEditor(input, sel, inputcomposer.SelectionStyles{
		Line:        bossInputSelectionLineStyle,
		CursorLine:  bossInputSelectionCursorLineStyle,
		Range:       bossInputSelectionRangeStyle,
		Cursor:      bossInputSelectionCursorStyle,
		CursorRange: bossInputSelectionCursorRangeStyle,
	})
	return bossInputShellStyle.Width(width).Render(editorView)
}

func (m Model) renderInputCopyDialogOverlay(body string, bodyW, bodyH int) string {
	panel := m.renderInputCopyDialog(bodyW)
	panelW := lipgloss.Width(panel)
	panelH := lipgloss.Height(panel)
	left := maxInt(0, (bodyW-panelW)/2)
	top := maxInt(0, minInt((bodyH-panelH)/4, bodyH-panelH))
	return overlayBossBlock(body, panel, bodyW, bodyH, left, top)
}

func (m Model) renderInputCopyDialog(bodyW int) string {
	panelW := minInt(bodyW, minInt(maxInt(54, bodyW-12), 76))
	panelInnerW := maxInt(28, panelW-4)
	content := m.renderInputCopyDialogContent(panelInnerW)
	panelH := maxInt(8, countBlockLines(content)+4)
	return m.renderRawPanel("Copy Input", content, panelW, panelH)
}

func (m Model) renderInputCopyDialogContent(width int) string {
	dialog := m.inputCopyDialog
	if dialog == nil {
		return ""
	}
	lines := []string{
		bossMutedStyle.Render(fitLine("Choose what to put on the clipboard.", width)),
	}
	buttons := make([]string, 0, len(inputcomposer.CopyChoices()))
	for _, choice := range inputcomposer.CopyChoices() {
		buttons = append(buttons, renderBossInputCopyButton(inputcomposer.CopyChoiceLabel(choice), dialog.Selected == choice))
	}
	lines = append(lines, lipgloss.JoinHorizontal(lipgloss.Left, buttons...))
	lines = append(lines, "")
	lines = append(lines, bossMutedStyle.Render(fitLine(inputcomposer.CopyChoiceSummary(dialog.Selected), width)))
	lines = append(lines, "")
	lines = append(lines, strings.Join([]string{
		renderBossInputCopyAction("Enter", "choose", uistyle.DialogActionPrimary),
		renderBossInputCopyAction("Tab/Up/Down", "switch", uistyle.DialogActionNavigate),
		renderBossInputCopyAction("Esc", "cancel", uistyle.DialogActionCancel),
	}, "   "))
	return strings.Join(lines, "\n")
}

func renderBossInputCopyButton(label string, selected bool) string {
	if selected {
		return lipgloss.NewStyle().Foreground(lipgloss.Color("230")).Background(lipgloss.Color("24")).Bold(true).Padding(0, 1).Render(" " + label + " ")
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Background(bossPanelBackground).Padding(0, 1).Render("[" + label + "]")
}

func renderBossInputCopyAction(key, label string, tone uistyle.DialogActionTone) string {
	return uistyle.RenderDialogActionTone(key, label, tone, bossDialogActionFillStyle)
}
