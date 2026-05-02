package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"lcroom/internal/codexskills"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

type skillsDialogState struct {
	Loading   bool
	Inventory codexskills.Inventory
	Err       error
	Selected  int
	Offset    int
	RequestID int64
}

type skillsInventoryMsg struct {
	requestID int64
	inventory codexskills.Inventory
	err       error
}

func (m *Model) openSkillsDialog() tea.Cmd {
	m.skillsInventorySeq++
	requestID := m.skillsInventorySeq
	m.skillsDialog = &skillsDialogState{
		Loading:   true,
		RequestID: requestID,
	}
	m.commandMode = false
	m.showHelp = false
	m.err = nil
	m.status = "Loading Codex skills..."
	return m.loadSkillsInventoryCmd(requestID)
}

func (m Model) loadSkillsInventoryCmd(requestID int64) tea.Cmd {
	codexHome := ""
	if m.svc != nil {
		codexHome = m.svc.Config().CodexHome
	}
	parent := m.ctx
	if parent == nil {
		parent = context.Background()
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parent, 5*time.Second)
		defer cancel()
		inv, err := codexskills.LoadInventory(ctx, codexHome, time.Now())
		return skillsInventoryMsg{requestID: requestID, inventory: inv, err: err}
	}
}

func (m Model) applySkillsInventoryMsg(msg skillsInventoryMsg) (tea.Model, tea.Cmd) {
	if m.skillsDialog == nil || m.skillsDialog.RequestID != msg.requestID {
		return m, nil
	}
	m.skillsDialog.Loading = false
	m.skillsDialog.Inventory = msg.inventory
	m.skillsDialog.Err = msg.err
	if msg.err != nil {
		m.status = "Codex skills scan failed: " + msg.err.Error()
		return m, nil
	}
	attentionCount := len(codexskills.AttentionSkills(msg.inventory))
	if attentionCount > 0 {
		m.status = fmt.Sprintf("Codex skills loaded. %d skill(s) need review.", attentionCount)
	} else {
		m.status = fmt.Sprintf("Codex skills loaded. %d skill(s) installed.", len(msg.inventory.Skills))
	}
	m.syncSkillsDialogSelection()
	return m, nil
}

func (m Model) updateSkillsDialogMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.skillsDialog == nil {
		return m, nil
	}
	switch msg.String() {
	case "esc", "q":
		m.skillsDialog = nil
		m.status = "Codex skills closed"
		return m, nil
	case "r":
		return m, m.refreshSkillsDialog()
	case "up", "k":
		m.moveSkillsDialogSelection(-1)
		return m, nil
	case "down", "j":
		m.moveSkillsDialogSelection(1)
		return m, nil
	case "pgup", "ctrl+u":
		m.moveSkillsDialogSelection(-m.skillsDialogListHeight())
		return m, nil
	case "pgdown", "ctrl+d":
		m.moveSkillsDialogSelection(m.skillsDialogListHeight())
		return m, nil
	case "home":
		m.skillsDialog.Selected = 0
		m.syncSkillsDialogSelection()
		return m, nil
	case "end":
		m.skillsDialog.Selected = len(m.skillsDialog.Inventory.Skills) - 1
		m.syncSkillsDialogSelection()
		return m, nil
	case "c", "enter":
		return m.copySelectedSkillPath()
	}
	return m, nil
}

func (m *Model) refreshSkillsDialog() tea.Cmd {
	if m.skillsDialog == nil || m.skillsDialog.Loading {
		return nil
	}
	m.skillsInventorySeq++
	m.skillsDialog.RequestID = m.skillsInventorySeq
	m.skillsDialog.Loading = true
	m.skillsDialog.Err = nil
	m.status = "Refreshing Codex skills..."
	return m.loadSkillsInventoryCmd(m.skillsDialog.RequestID)
}

func (m *Model) moveSkillsDialogSelection(delta int) {
	if m.skillsDialog == nil || delta == 0 {
		return
	}
	m.skillsDialog.Selected += delta
	m.syncSkillsDialogSelection()
}

func (m *Model) syncSkillsDialogSelection() {
	dialog := m.skillsDialog
	if dialog == nil {
		return
	}
	items := dialog.Inventory.Skills
	if len(items) == 0 {
		dialog.Selected = 0
		dialog.Offset = 0
		return
	}
	if dialog.Selected < 0 {
		dialog.Selected = 0
	}
	if dialog.Selected >= len(items) {
		dialog.Selected = len(items) - 1
	}
	listHeight := m.skillsDialogListHeight()
	if dialog.Offset < 0 {
		dialog.Offset = 0
	}
	maxOffset := max(0, len(items)-listHeight)
	if dialog.Offset > maxOffset {
		dialog.Offset = maxOffset
	}
	if dialog.Selected < dialog.Offset {
		dialog.Offset = dialog.Selected
	}
	if dialog.Selected >= dialog.Offset+listHeight {
		dialog.Offset = dialog.Selected - listHeight + 1
	}
}

func (m Model) selectedSkill() (codexskills.Skill, bool) {
	if m.skillsDialog == nil {
		return codexskills.Skill{}, false
	}
	items := m.skillsDialog.Inventory.Skills
	if len(items) == 0 {
		return codexskills.Skill{}, false
	}
	index := m.skillsDialog.Selected
	if index < 0 || index >= len(items) {
		return codexskills.Skill{}, false
	}
	return items[index], true
}

func (m Model) copySelectedSkillPath() (tea.Model, tea.Cmd) {
	skill, ok := m.selectedSkill()
	if !ok || strings.TrimSpace(skill.Path) == "" {
		m.status = "No Codex skill selected"
		return m, nil
	}
	if err := clipboardTextWriter(skill.Path); err != nil {
		m.status = "Copy failed: " + err.Error()
		return m, nil
	}
	m.status = "Copied Codex skill path to clipboard"
	return m, nil
}

func (m Model) renderSkillsDialogOverlay(body string, bodyW, bodyH int) string {
	panel := m.renderSkillsDialog(bodyW, bodyH)
	panelWidth := lipgloss.Width(panel)
	panelHeight := lipgloss.Height(panel)
	left := max(0, (bodyW-panelWidth)/2)
	top := max(0, (bodyH-panelHeight)/4)
	return overlayBlock(body, panel, bodyW, bodyH, left, top)
}

func (m Model) renderSkillsDialog(bodyW, bodyH int) string {
	panelWidth := min(bodyW, min(max(64, bodyW-8), 108))
	panelInnerWidth := max(32, panelWidth-4)
	content := m.renderSkillsDialogContent(panelInnerWidth)
	return renderDialogPanel(panelWidth, panelInnerWidth, content)
}

func (m Model) renderSkillsDialogContent(width int) string {
	dialog := m.skillsDialog
	if dialog == nil {
		return ""
	}
	lines := []string{
		commandPaletteTitleStyle.Render("Codex Skills"),
		commandPaletteHintStyle.Render(skillsFitLine(skillsDialogSummary(*dialog), width)),
	}
	if dialog.Loading {
		lines = append(lines, "", detailMutedStyle.Render("Loading "+skillsSpinnerDots(m.spinnerFrame)))
		return strings.Join(lines, "\n")
	}
	if dialog.Err != nil {
		lines = append(lines, "", detailDangerStyle.Render(skillsFitLine(dialog.Err.Error(), width)))
		return strings.Join(lines, "\n")
	}
	items := dialog.Inventory.Skills
	if len(items) == 0 {
		lines = append(lines, "", detailMutedStyle.Render("No Codex skills found."))
		return strings.Join(lines, "\n")
	}
	lines = append(lines, "")
	lines = append(lines, m.renderSkillsDialogRows(width)...)
	if skill, ok := m.selectedSkill(); ok {
		lines = append(lines, "")
		lines = append(lines, m.renderSkillDetail(skill, width)...)
	}
	lines = append(lines, "")
	lines = append(lines, renderDialogAction("r", "refresh", navigateActionKeyStyle, navigateActionTextStyle)+"   "+
		renderDialogAction("c/Enter", "copy path", commitActionKeyStyle, commitActionTextStyle)+"   "+
		renderDialogAction("Esc", "close", cancelActionKeyStyle, cancelActionTextStyle))
	return strings.Join(lines, "\n")
}

func skillsDialogSummary(dialog skillsDialogState) string {
	if dialog.Loading {
		return "Scanning Codex home..."
	}
	inv := dialog.Inventory
	attentionCount := len(codexskills.AttentionSkills(inv))
	return fmt.Sprintf("%d installed | %d user | %d system | %d plugin | %d review | %s",
		len(inv.Skills),
		codexskills.CountBySource(inv, codexskills.SourceUser),
		codexskills.CountBySource(inv, codexskills.SourceSystem),
		codexskills.CountBySource(inv, codexskills.SourcePlugin),
		attentionCount,
		inv.CodexHome,
	)
}

func (m Model) renderSkillsDialogRows(width int) []string {
	dialog := m.skillsDialog
	if dialog == nil {
		return nil
	}
	items := dialog.Inventory.Skills
	listHeight := m.skillsDialogListHeight()
	start := dialog.Offset
	if start < 0 {
		start = 0
	}
	end := min(len(items), start+listHeight)
	lines := make([]string, 0, listHeight+2)
	if start > 0 {
		lines = append(lines, commandPaletteHintStyle.Render(skillsFitLine("up more", width)))
	}
	nameWidth := min(28, max(14, width/4))
	sourceWidth := min(16, max(8, width/6))
	statusWidth := 8
	descWidth := max(10, width-nameWidth-sourceWidth-statusWidth-8)
	for i := start; i < end; i++ {
		skill := items[i]
		selected := i == dialog.Selected
		marker := " "
		if selected {
			marker = ">"
		}
		status := "ok"
		if len(skill.Attention) > 0 {
			status = "review"
		}
		line := fmt.Sprintf("%s %-*s %-*s %-*s %s",
			marker,
			nameWidth,
			truncateText(skill.InvocationName, nameWidth),
			sourceWidth,
			truncateText(skill.SourceLabel, sourceWidth),
			statusWidth,
			status,
			truncateText(skill.Description, descWidth),
		)
		style := commandPaletteRowStyle
		if selected {
			style = commandPaletteSelectStyle
		} else if len(skill.Attention) > 0 {
			style = detailWarningStyle
		}
		lines = append(lines, style.Render(skillsFitLine(line, width)))
	}
	if end < len(items) {
		lines = append(lines, commandPaletteHintStyle.Render(skillsFitLine("down more", width)))
	}
	return lines
}

func (m Model) renderSkillDetail(skill codexskills.Skill, width int) []string {
	lines := []string{
		detailSectionStyle.Render("Selected"),
		skillsDialogField("Name", skill.InvocationName, width),
		skillsDialogField("Source", skill.SourceLabel, width),
		skillsDialogField("Updated", formatSkillModifiedAt(skill.ModifiedAt), width),
		skillsDialogField("Path", skill.Path, width),
	}
	if strings.TrimSpace(skill.SymlinkTarget) != "" {
		lines = append(lines, skillsDialogField("Link", skill.SymlinkTarget, width))
	}
	if strings.TrimSpace(skill.Description) != "" {
		lines = append(lines, skillsDialogWrappedField("About", skill.Description, width)...)
	}
	if len(skill.Attention) > 0 {
		lines = append(lines, detailWarningStyle.Render("Review"))
		for _, item := range skill.Attention {
			lines = append(lines, detailWarningStyle.Render(skillsFitLine("- "+item, width)))
		}
	}
	return lines
}

func skillsDialogField(label, value string, width int) string {
	if strings.TrimSpace(value) == "" {
		value = "-"
	}
	prefix := detailLabelStyle.Render(label + ":")
	prefixWidth := ansi.StringWidth(label + ": ")
	return prefix + " " + detailValueStyle.Render(truncateText(value, max(1, width-prefixWidth)))
}

func skillsDialogWrappedField(label, value string, width int) []string {
	prefix := detailLabelStyle.Render(label + ":")
	prefixPlainWidth := ansi.StringWidth(label + ": ")
	textWidth := max(1, width-prefixPlainWidth)
	wrapped := lipgloss.NewStyle().Width(textWidth).Render(strings.TrimSpace(value))
	parts := strings.Split(strings.ReplaceAll(wrapped, "\r\n", "\n"), "\n")
	lines := make([]string, 0, len(parts))
	for i, part := range parts {
		if i == 0 {
			lines = append(lines, prefix+" "+detailValueStyle.Render(part))
			continue
		}
		lines = append(lines, strings.Repeat(" ", prefixPlainWidth)+detailValueStyle.Render(part))
	}
	return lines
}

func (m Model) skillsDialogListHeight() int {
	if m.height <= 0 {
		return 8
	}
	return min(10, max(4, m.height-18))
}

func formatSkillModifiedAt(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Format("2006-01-02 15:04")
}

func skillsFitLine(text string, width int) string {
	if width <= 0 {
		return ""
	}
	if ansi.StringWidth(text) <= width {
		return text
	}
	return ansi.Truncate(text, width, "...")
}

func skillsSpinnerDots(frame int) string {
	switch frame % 4 {
	case 0:
		return "."
	case 1:
		return ".."
	case 2:
		return "..."
	default:
		return ""
	}
}
