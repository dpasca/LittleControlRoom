package boss

import (
	"fmt"
	"strings"
	"time"

	"lcroom/internal/uistyle"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

func (m Model) SessionPickerActive() bool {
	return m.sessionPickerVisible
}

func (m Model) openBossSessionPicker() (tea.Model, tea.Cmd) {
	m.sessionPickerVisible = true
	m.sessionPickerLoading = true
	m.sessionPickerSessions = nil
	m.sessionPickerSelected = 0
	m.sessionPickerErr = nil
	m.status = "Loading boss chat sessions..."
	m.syncLayout(false)
	return m, m.listBossSessionsCmd()
}

func (m *Model) closeBossSessionPicker(status string) {
	m.sessionPickerVisible = false
	m.sessionPickerLoading = false
	m.sessionPickerSessions = nil
	m.sessionPickerSelected = 0
	m.sessionPickerErr = nil
	if strings.TrimSpace(status) != "" {
		m.status = status
	}
}

func (m Model) applyBossSessionsListed(msg bossSessionsListedMsg) (tea.Model, tea.Cmd) {
	if !m.sessionPickerVisible {
		return m, nil
	}
	m.sessionPickerLoading = false
	m.sessionPickerErr = msg.err
	if msg.err != nil {
		m.status = "Boss chat sessions failed: " + msg.err.Error()
		m.syncLayout(false)
		return m, nil
	}
	m.sessionPickerSessions = append([]bossChatSession(nil), msg.sessions...)
	m.sessionPickerSelected = m.defaultBossSessionPickerIndex()
	if len(m.sessionPickerSessions) == 0 {
		m.status = "No saved boss chat sessions"
	} else {
		m.status = "Boss session picker open"
	}
	m.syncLayout(false)
	return m, nil
}

func (m Model) updateBossSessionPicker(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "alt+up":
		return m, m.exitCmd()
	case "esc", "alt+down":
		m.closeBossSessionPicker("Boss session picker closed")
		return m, nil
	}
	if m.sessionPickerLoading {
		return m, nil
	}
	if m.sessionPickerErr != nil {
		if msg.String() == "enter" {
			m.closeBossSessionPicker("Boss session picker closed")
		}
		return m, nil
	}
	sessions := m.currentBossSessionPickerSessions()
	if len(sessions) == 0 {
		if msg.String() == "enter" {
			m.closeBossSessionPicker("No saved boss chat sessions")
		}
		return m, nil
	}
	m.clampBossSessionPickerSelection(len(sessions))
	switch msg.String() {
	case "up", "k":
		m.moveBossSessionPickerSelection(-1, len(sessions))
	case "down", "j":
		m.moveBossSessionPickerSelection(1, len(sessions))
	case "pgup", "ctrl+u":
		m.moveBossSessionPickerSelection(-5, len(sessions))
	case "pgdown", "ctrl+d":
		m.moveBossSessionPickerSelection(5, len(sessions))
	case "home", "g":
		m.sessionPickerSelected = 0
	case "end", "G":
		m.sessionPickerSelected = len(sessions) - 1
	case "enter":
		session, ok := m.currentBossSessionPickerSession()
		if !ok {
			return m, nil
		}
		m.closeBossSessionPicker("")
		m.sessionLoaded = false
		m.status = "Opening boss chat session " + shortBossSessionID(session.SessionID) + "..."
		return m, m.loadBossSessionCmd(session.SessionID)
	}
	return m, nil
}

func (m *Model) moveBossSessionPickerSelection(delta, total int) {
	if total <= 0 || delta == 0 {
		return
	}
	m.sessionPickerSelected += delta
	m.clampBossSessionPickerSelection(total)
}

func (m *Model) clampBossSessionPickerSelection(total int) {
	if total <= 0 {
		m.sessionPickerSelected = 0
		return
	}
	if m.sessionPickerSelected < 0 {
		m.sessionPickerSelected = 0
	}
	if m.sessionPickerSelected >= total {
		m.sessionPickerSelected = total - 1
	}
}

func (m Model) currentBossSessionPickerSession() (bossChatSession, bool) {
	sessions := m.currentBossSessionPickerSessions()
	if len(sessions) == 0 {
		return bossChatSession{}, false
	}
	index := m.sessionPickerSelected
	if index < 0 {
		index = 0
	}
	if index >= len(sessions) {
		index = len(sessions) - 1
	}
	return sessions[index], true
}

func (m Model) currentBossSessionPickerSessions() []bossChatSession {
	return append([]bossChatSession(nil), m.sessionPickerSessions...)
}

func (m Model) defaultBossSessionPickerIndex() int {
	current := strings.TrimSpace(m.sessionID)
	if current == "" {
		return 0
	}
	for i, session := range m.sessionPickerSessions {
		if strings.TrimSpace(session.SessionID) == current {
			return i
		}
	}
	return 0
}

func (m Model) renderBossSessionPickerOverlay(body string, bodyW, bodyH int) string {
	panel := m.renderBossSessionPicker(bodyW, bodyH)
	panelWidth := lipgloss.Width(panel)
	panelHeight := lipgloss.Height(panel)
	left := maxInt(0, (bodyW-panelWidth)/2)
	top := maxInt(0, minInt((bodyH-panelHeight)/5, bodyH-panelHeight))
	return overlayBossBlock(body, panel, bodyW, bodyH, left, top)
}

func (m Model) renderBossSessionPicker(bodyW, bodyH int) string {
	panelWidth := minInt(bodyW, minInt(maxInt(58, bodyW-10), 92))
	panelInnerWidth := maxInt(28, panelWidth-4)
	content := m.renderBossSessionPickerContent(panelInnerWidth, bodyH)
	panelHeight := minInt(maxInt(8, countBlockLines(content)+4), maxInt(8, bodyH-2))
	return m.renderRawPanel("Boss Sessions", content, panelWidth, panelHeight)
}

func (m Model) renderBossSessionPickerContent(width, bodyH int) string {
	lines := []string{
		bossMutedStyle.Render(fitLine("Enter opens the selected session. Esc closes. /new starts fresh.", width)),
		renderBossSessionPickerAction("Enter", "open", uistyle.DialogActionPrimary) + "   " +
			renderBossSessionPickerAction("Esc", "close", uistyle.DialogActionCancel) + "   " +
			renderBossSessionPickerAction("Up/Down", "select", uistyle.DialogActionNavigate),
		"",
	}
	if m.sessionPickerLoading {
		lines = append(lines, bossMutedStyle.Render(fitLine("Loading saved boss sessions"+spinnerDots(m.spinnerFrame), width)))
		return strings.Join(lines, "\n")
	}
	if m.sessionPickerErr != nil {
		lines = append(lines, bossMutedStyle.Render(fitLine("Could not load saved boss sessions: "+m.sessionPickerErr.Error(), width)))
		return strings.Join(lines, "\n")
	}
	sessions := m.currentBossSessionPickerSessions()
	if len(sessions) == 0 {
		lines = append(lines, bossMutedStyle.Render(fitLine("No saved boss chat sessions yet. Use /new to start one.", width)))
		return strings.Join(lines, "\n")
	}

	start, end := m.bossSessionPickerWindow(len(sessions), bodyH)
	if start > 0 {
		lines = append(lines, bossMutedStyle.Render(fitLine(fmt.Sprintf("up %d more", start), width)))
	}
	for i := start; i < end; i++ {
		lines = append(lines, m.renderBossSessionPickerRow(sessions[i], i == m.sessionPickerSelected, width))
	}
	if end < len(sessions) {
		lines = append(lines, bossMutedStyle.Render(fitLine(fmt.Sprintf("down %d more", len(sessions)-end), width)))
	}
	if selected, ok := m.currentBossSessionPickerSession(); ok {
		lines = append(lines, "")
		lines = append(lines, bossSessionPickerSectionStyle.Render(fitLine("About", width)))
		title := strings.TrimSpace(selected.Title)
		if title == "" {
			title = "untitled boss chat"
		}
		lines = append(lines, bossSessionPickerDetailStyle.Render(fitLine(title, width)))
		lines = append(lines, bossMutedStyle.Render(fitLine(bossSessionPickerMeta(selected, m.now()), width)))
		if path := strings.TrimSpace(selected.Path); path != "" {
			lines = append(lines, bossMutedStyle.Render(fitLine(path, width)))
		}
	}
	return strings.Join(lines, "\n")
}

func (m Model) bossSessionPickerWindow(total, bodyH int) (int, int) {
	if total <= 0 {
		return 0, 0
	}
	limit := m.bossSessionPickerListLimit(total, bodyH)
	limit = minInt(limit, total)
	start := 0
	if m.sessionPickerSelected >= limit {
		start = m.sessionPickerSelected - limit + 1
	}
	maxStart := total - limit
	if start > maxStart {
		start = maxStart
	}
	if start < 0 {
		start = 0
	}
	return start, start + limit
}

func (m Model) bossSessionPickerListLimit(total, bodyH int) int {
	if total <= 0 {
		return 0
	}
	if bodyH <= 0 {
		bodyH = defaultBossHeight
	}
	panelMaxHeight := maxInt(8, bodyH-2)
	fixedLines := 12
	available := panelMaxHeight - fixedLines
	if available < 1 {
		available = 1
	}
	return minInt(total, minInt(12, available))
}

func (m Model) renderBossSessionPickerRow(session bossChatSession, selected bool, width int) string {
	badge := "SAVE"
	if strings.TrimSpace(session.SessionID) == strings.TrimSpace(m.sessionID) {
		badge = "CUR "
	}
	title := strings.TrimSpace(session.Title)
	if title == "" {
		title = "untitled boss chat"
	}
	right := fmt.Sprintf("%s  %s", formatBossSessionPickerActivity(session.UpdatedAt), shortBossSessionID(session.SessionID))
	leftWidth := 6
	rightWidth := len([]rune(right))
	labelWidth := maxInt(12, width-leftWidth-rightWidth-5)
	line := fmt.Sprintf("  %-4s %s %s", badge, clipText(title, labelWidth), right)
	if selected {
		line = "> " + strings.TrimPrefix(line, "  ")
		return bossSessionPickerSelectedRowStyle.Width(width).Render(fitLine(line, width))
	}
	return bossSessionPickerRowStyle.Width(width).Render(fitLine(line, width))
}

func bossSessionPickerMeta(session bossChatSession, now time.Time) string {
	parts := []string{fmt.Sprintf("%d messages", session.MessageCount)}
	if !session.UpdatedAt.IsZero() {
		parts = append(parts, "updated "+relativeAge(now, session.UpdatedAt))
	}
	if !session.CreatedAt.IsZero() {
		parts = append(parts, "created "+formatBossSessionPickerActivity(session.CreatedAt))
	}
	parts = append(parts, "session "+session.SessionID)
	return strings.Join(parts, "  ")
}

func formatBossSessionPickerActivity(at time.Time) string {
	if at.IsZero() {
		return "unknown"
	}
	return at.Local().Format("01-02 15:04")
}

func renderBossSessionPickerAction(key, label string, tone uistyle.DialogActionTone) string {
	return uistyle.RenderDialogActionTone(key, label, tone, bossDialogActionFillStyle)
}

func overlayBossBlock(base, overlay string, width, height, left, top int) string {
	baseLines := bossBlockLines(base, width, height)
	overlayLines := bossBlockLines(overlay, lipgloss.Width(overlay), lipgloss.Height(overlay))
	overlayWidth := lipgloss.Width(overlay)
	for row, overlayLine := range overlayLines {
		target := top + row
		if target < 0 || target >= len(baseLines) {
			continue
		}
		baseLine := baseLines[target]
		prefix := ansi.Cut(baseLine, 0, left)
		suffix := ansi.Cut(baseLine, left+overlayWidth, width)
		baseLines[target] = prefix + overlayLine + suffix
	}
	return strings.Join(baseLines, "\n")
}

func bossBlockLines(block string, width, height int) []string {
	if width < 0 {
		width = 0
	}
	if height < 0 {
		height = 0
	}
	filled := lipgloss.Place(width, height, lipgloss.Left, lipgloss.Top, block)
	lines := strings.Split(filled, "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	for i, line := range lines {
		lines[i] = lipgloss.PlaceHorizontal(width, lipgloss.Left, line)
	}
	return lines
}

var (
	bossSessionPickerRowStyle = lipgloss.NewStyle().
					Foreground(bossPanelText).
					Background(bossPanelBackground)
	bossSessionPickerSelectedRowStyle = lipgloss.NewStyle().
						Foreground(lipgloss.Color("16")).
						Background(bossPanelAccent).
						Bold(true)
	bossDialogActionFillStyle     = lipgloss.NewStyle().Background(bossPanelBackground)
	bossSessionPickerSectionStyle = lipgloss.NewStyle().
					Foreground(bossPanelAccent).
					Background(bossPanelBackground).
					Bold(true)
	bossSessionPickerDetailStyle = lipgloss.NewStyle().
					Foreground(bossPanelText).
					Background(bossPanelBackground).
					Bold(true)
)
