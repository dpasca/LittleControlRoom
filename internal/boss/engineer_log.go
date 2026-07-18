package boss

import (
	"fmt"
	"strings"
	"time"

	"lcroom/internal/viewportnav"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type engineerLogOverlayGeometry struct {
	panelWidth     int
	panelHeight    int
	contentWidth   int
	viewportHeight int
	showFooter     bool
}

func (m Model) EngineerLogVisible() bool {
	return m.engineerLogVisible
}

func (m Model) CloseEngineerLog() Model {
	if m.engineerLogVisible {
		m.closeEngineerLog()
	}
	return m
}

func (m Model) openEngineerLog() (tea.Model, tea.Cmd) {
	m.input.Reset()
	m.bossSlashSelected = 0
	m.engineerLogVisible = true
	count := m.engineerLogEventCount()
	if count == 0 {
		m.status = "Engineer events open; no events yet"
	} else if count == 1 {
		m.status = "Engineer events open; 1 event"
	} else {
		m.status = fmt.Sprintf("Engineer events open; %d events", count)
	}
	m.syncLayout(false)
	m.engineerLogViewport.GotoBottom()
	return m, nil
}

func (m *Model) closeEngineerLog() {
	m.engineerLogVisible = false
	m.status = "Engineer events closed"
}

func (m Model) updateEngineerLog(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "ctrl+c", "q":
		m.closeEngineerLog()
		return m, nil
	case "pgup":
		viewportnav.PageUp(&m.engineerLogViewport)
		return m, nil
	case "pgdown":
		viewportnav.PageDown(&m.engineerLogViewport)
		return m, nil
	case "home":
		m.engineerLogViewport.GotoTop()
		return m, nil
	case "end":
		m.engineerLogViewport.GotoBottom()
		return m, nil
	case "up", "down":
		var cmd tea.Cmd
		m.engineerLogViewport, cmd = m.engineerLogViewport.Update(msg)
		return m, cmd
	default:
		return m, nil
	}
}

func (m Model) updateEngineerLogMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if msg.Action != tea.MouseActionPress {
		return m, nil
	}
	switch msg.Button {
	case tea.MouseButtonWheelUp, tea.MouseButtonWheelDown:
		var cmd tea.Cmd
		m.engineerLogViewport, cmd = m.engineerLogViewport.Update(msg)
		return m, cmd
	default:
		return m, nil
	}
}

func (m *Model) syncEngineerLogViewport(gotoBottom bool) {
	if m == nil {
		return
	}
	geometry := engineerLogOverlayGeometryForSize(m.layout().width, m.layout().height)
	wasAtBottom := m.engineerLogViewport.AtBottom()
	m.engineerLogViewport.Width = geometry.contentWidth
	m.engineerLogViewport.Height = geometry.viewportHeight
	m.engineerLogViewport.SetContent(m.renderEngineerLogEntries(geometry.contentWidth))
	if gotoBottom || wasAtBottom {
		m.engineerLogViewport.GotoBottom()
	}
}

func (m Model) renderEngineerLogOverlay(body string, bodyW, bodyH int) string {
	panel := m.renderEngineerLogPanel(bodyW, bodyH)
	panelW := lipgloss.Width(panel)
	panelH := lipgloss.Height(panel)
	left := maxInt(0, (bodyW-panelW)/2)
	top := maxInt(0, minInt((bodyH-panelH)/3, bodyH-panelH))
	return overlayBossBlock(body, panel, bodyW, bodyH, left, top)
}

func (m Model) renderEngineerLogPanel(bodyW, bodyH int) string {
	geometry := engineerLogOverlayGeometryForSize(bodyW, bodyH)
	content := m.engineerLogViewport.View()
	if geometry.showFooter {
		footer := fmt.Sprintf(
			"%s  PgUp/PgDn scroll  Home/End jump  Esc close",
			engineerLogEventCountLabel(m.engineerLogEventCount()),
		)
		content = strings.Join([]string{
			fitRenderedBlock(content, geometry.contentWidth, geometry.viewportHeight),
			bossMutedStyle.Render(fitLine(footer, geometry.contentWidth)),
		}, "\n")
	}
	return renderBossControlPanel("Engineer Events", content, geometry.panelWidth, geometry.panelHeight)
}

func engineerLogOverlayGeometryForSize(bodyW, bodyH int) engineerLogOverlayGeometry {
	if bodyW <= 0 {
		bodyW = defaultBossWidth
	}
	if bodyH <= 0 {
		bodyH = defaultBossHeight
	}
	panelWidth := minInt(bodyW, maxInt(36, minInt(96, bodyW-6)))
	panelHeight := minInt(bodyH, maxInt(8, minInt(26, bodyH-2)))
	contentWidth := bossPanelInnerWidth(panelWidth)
	contentHeight := maxInt(1, panelHeight-4)
	showFooter := contentHeight >= 2
	viewportHeight := contentHeight
	if showFooter {
		viewportHeight--
	}
	return engineerLogOverlayGeometry{
		panelWidth:     panelWidth,
		panelHeight:    panelHeight,
		contentWidth:   contentWidth,
		viewportHeight: maxInt(1, viewportHeight),
		showFooter:     showFooter,
	}
}

func (m Model) renderEngineerLogEntries(width int) string {
	width = maxInt(1, width)
	messages := m.engineerLogMessages()
	if len(messages) == 0 {
		return bossMutedStyle.Render(fitLine("No engineer events yet.", width))
	}
	blocks := make([]string, 0, len(messages)*2)
	var lastDate time.Time
	for _, message := range messages {
		if !message.At.IsZero() {
			messageDate := time.Date(message.At.Year(), message.At.Month(), message.At.Day(), 0, 0, 0, 0, message.At.Location())
			if lastDate.IsZero() || !messageDate.Equal(lastDate) {
				blocks = append(blocks, bossMutedStyle.Render(fitLine(messageDate.Format("Monday, January 2, 2006"), width)))
			}
			lastDate = messageDate
		}
		blocks = append(blocks, renderEngineerLogMessage(message, width))
	}
	return strings.Join(blocks, "\n\n")
}

func renderEngineerLogMessage(message ChatMessage, width int) string {
	prefix := "event "
	if !message.At.IsZero() {
		prefix = message.At.Format("15:04:05") + " "
	}
	return renderPrefixedMessageWithProjectHighlights(
		message.Content,
		prefix,
		bossPanelText,
		bossToolCallStyle,
		bossAssistantContinuationStyle,
		bossAssistantMessageStyle,
		width,
		false,
		handoffMessageHighlights(message.Content, message.Handoff),
		nil,
	)
}

func (m Model) engineerLogMessages() []ChatMessage {
	messages := make([]ChatMessage, 0, len(m.messages))
	for _, message := range m.messages {
		if chatMessageIsLog(message) {
			messages = append(messages, message)
		}
	}
	return messages
}

func (m Model) engineerLogEventCount() int {
	count := 0
	for _, message := range m.messages {
		if chatMessageIsLog(message) {
			count++
		}
	}
	return count
}

func engineerLogEventCountLabel(count int) string {
	if count == 1 {
		return "1 event"
	}
	return fmt.Sprintf("%d events", count)
}
