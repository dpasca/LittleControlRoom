package tui

import (
	"strings"

	bossui "lcroom/internal/boss"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

type helpChatOverlayGeometry struct {
	left        int
	top         int
	panelWidth  int
	panelHeight int
	chatWidth   int
	chatHeight  int
}

func (m Model) openHelpChatModeOrSetupPrompt() (tea.Model, tea.Cmd) {
	m, surfaceCmd := m.prepareHelpChatHostSurface()
	if m.bossChatConfigured() {
		updated, openCmd := m.openHelpChatMode()
		return updated, batchCmds(surfaceCmd, openCmd)
	}
	m.openBossSetupPrompt()
	m.status = "Help chat needs setup before it can open."
	return m, surfaceCmd
}

func (m Model) prepareHelpChatHostSurface() (Model, tea.Cmd) {
	var cmds []tea.Cmd
	if projectPath := m.codexPendingOpenProject(); m.codexPendingOpenVisible() && projectPath != "" {
		updated, cmd := m.hidePendingCodexOpen(projectPath)
		m = normalizeUpdateModel(updated)
		cmds = append(cmds, cmd)
	} else if m.codexVisible() {
		updated, cmd := m.hideCodexSession()
		m = normalizeUpdateModel(updated)
		cmds = append(cmds, cmd)
	}
	if m.diffView != nil {
		m.clearPendingGitSummary(m.diffView.ProjectPath)
		m.diffView = nil
		m.syncDetailViewport(false)
	}
	return m, batchCmds(cmds...)
}

func (m Model) openHelpChatMode() (tea.Model, tea.Cmd) {
	m.helpChatMode = true
	m.showHelp = false
	m.showPerf = false
	m.showAIStats = false
	var initCmd tea.Cmd
	if !m.helpChatModelActive {
		m.helpChatModel = bossui.NewEmbeddedHelpWithViewContext(m.ctx, m.svc, m.bossViewContext())
		m.helpChatModelActive = true
		initCmd = m.helpChatModel.Init()
	} else {
		m.helpChatModel = m.helpChatModel.WithViewContext(m.bossViewContext())
		initCmd = m.helpChatModel.ActivateCmd()
	}
	m.status = "Help chat open. Ask a question, or press Esc/backtick to hide."
	if m.width > 0 && m.height > 0 {
		updated, _ := m.helpChatModel.Update(m.helpChatWindowSizeMsg())
		m.helpChatModel = normalizeBossModel(updated)
	}
	m, noticeCmd := m.drainPendingBossHostNotices()
	return m, tea.Batch(initCmd, noticeCmd)
}

func (m *Model) closeHelpChatMode(status string) {
	m.helpChatMode = false
	if status != "" {
		m.status = status
	}
	m.syncDetailViewport(false)
}

func (m Model) updateHelpChatModeWindowSize() (tea.Model, tea.Cmd) {
	m.helpChatModel = m.helpChatModel.WithViewContext(m.bossViewContext())
	updated, cmd := m.helpChatModel.Update(m.helpChatWindowSizeMsg())
	m.helpChatModel = normalizeBossModel(updated)
	return m, cmd
}

func (m Model) updateHelpChatModeMessage(msg tea.Msg) (tea.Model, tea.Cmd) {
	m.helpChatModel = m.helpChatModel.WithViewContext(m.bossViewContext())
	updated, cmd := m.helpChatModel.Update(msg)
	m.helpChatModel = normalizeBossModel(updated)
	m, noticeCmd := m.drainPendingBossHostNotices()
	return m, tea.Batch(cmd, noticeCmd)
}

func (m Model) updateHelpChatModeKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "`" {
		m.closeHelpChatMode("Help chat hidden")
		return m, nil
	}
	return m.updateHelpChatModeMessage(msg)
}

func (m Model) updateHelpChatModeMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	geom := m.helpChatOverlayGeometry()
	chatLeft := geom.left + 2
	chatTop := geom.top + 2
	if msg.X < chatLeft || msg.X >= chatLeft+geom.chatWidth || msg.Y < chatTop || msg.Y >= chatTop+geom.chatHeight {
		return m, nil
	}
	msg.X -= chatLeft
	msg.Y -= chatTop
	return m.updateHelpChatModeMessage(msg)
}

func (m Model) helpChatWindowSizeMsg() tea.WindowSizeMsg {
	geom := m.helpChatOverlayGeometry()
	return tea.WindowSizeMsg{Width: geom.chatWidth, Height: geom.chatHeight}
}

func (m Model) helpChatOverlayGeometry() helpChatOverlayGeometry {
	layout := m.bodyLayout()
	return helpChatOverlayGeometryForSize(layout.width, layout.height)
}

func helpChatOverlayGeometryForSize(bodyW, bodyH int) helpChatOverlayGeometry {
	bodyW = max(1, bodyW)
	bodyH = max(1, bodyH)

	panelWidth := bodyW
	switch {
	case bodyW >= 126:
		panelWidth = 118
	case bodyW >= 76:
		panelWidth = bodyW - 8
	case bodyW >= 52:
		panelWidth = bodyW - 4
	}
	panelWidth = clampInt(panelWidth, 1, bodyW)
	chatWidth := max(1, panelWidth-4)

	panelHeight := bodyH
	switch {
	case bodyH >= 40:
		panelHeight = 36
	case bodyH >= 22:
		panelHeight = bodyH - 4
	case bodyH >= 14:
		panelHeight = bodyH - 2
	}
	panelHeight = clampInt(panelHeight, 1, bodyH)
	chatHeight := max(1, panelHeight-4)
	panelHeight = min(bodyH, chatHeight+4)

	return helpChatOverlayGeometry{
		left:        max(0, (bodyW-panelWidth)/2),
		top:         max(0, (bodyH-panelHeight)/3),
		panelWidth:  panelWidth,
		panelHeight: panelHeight,
		chatWidth:   chatWidth,
		chatHeight:  chatHeight,
	}
}

func (m Model) renderHelpChatOverlay(body string, bodyW, bodyH int) string {
	geom := helpChatOverlayGeometryForSize(bodyW, bodyH)
	header := m.renderHelpChatHeader(geom.chatWidth)
	chat := fitPaneContent(m.helpChatModel.View(), geom.chatWidth, geom.chatHeight)
	footer := m.renderHelpChatFooter(geom.chatWidth)
	content := strings.Join([]string{header, chat, footer}, "\n")
	// Lip Gloss Width includes padding in its wrapping budget but excludes the
	// border. Give the shell room for its padding so chatWidth remains the one
	// and only content-wrapping width owned by the embedded chat viewport.
	panelBoxWidth := geom.chatWidth + helpChatPanelStyle.GetHorizontalPadding()
	panel := helpChatPanelStyle.
		Width(panelBoxWidth).
		Render(fitPaneContent(content, geom.chatWidth, geom.chatHeight+2))
	return overlayBlock(body, panel, bodyW, bodyH, geom.left, geom.top)
}

func (m Model) renderHelpChatHeader(width int) string {
	parts := []string{helpChatTitleStyle.Render("Help Chat")}
	if statusText := strings.TrimSpace(m.helpChatModel.StatusText()); statusText != "" {
		parts = append(parts, helpChatStatusStyle.Render(statusText))
	}
	if usageText := strings.TrimSpace(m.helpChatModel.UsageText()); usageText != "" {
		parts = append(parts, helpChatUsageStyle.Render(usageText))
	}
	return fitHelpChatSurfaceLine(strings.Join(parts, helpChatSurfaceFillStyle.Render("  ")), width)
}

func (m Model) renderHelpChatFooter(width int) string {
	actions := []footerAction{
		footerPrimaryAction("Enter", "send"),
		footerHideAction("Esc", "hide"),
		footerHideAction("`", "hide"),
		footerLowAction("/new", "clear"),
		footerLowAction("Ctrl+L", "clear"),
		footerNavAction("Alt+Enter", "newline"),
	}
	if m.helpChatModel.ControlConfirmationActive() {
		actions = []footerAction{
			footerPrimaryAction("Enter", "confirm"),
			footerExitAction("Esc", "cancel"),
		}
	}
	if m.helpChatModel.OpenTargetPickerActive() {
		actions = []footerAction{
			footerPrimaryAction("Enter/Alt+O", "open"),
			footerNavAction("f", "folder"),
			footerNavAction("Up/Down", "select"),
			footerExitAction("Esc", "close"),
		}
	}
	segments := []string{renderFooterActionListWithBackground(helpChatSurfaceBackground, actions...)}
	if profileText := strings.TrimSpace(m.helpChatModel.ProfileText()); profileText != "" {
		segments = append(segments, helpChatUsageStyle.Render(profileText))
	}
	return fitHelpChatSurfaceLine(strings.Join(segments, helpChatSurfaceFillStyle.Render("  ")), width)
}

func fitHelpChatSurfaceLine(text string, width int) string {
	if width <= 0 {
		return text
	}
	text = ansi.Truncate(text, width, "")
	if padding := width - ansi.StringWidth(text); padding > 0 {
		text += helpChatSurfaceFillStyle.Render(strings.Repeat(" ", padding))
	}
	return text
}

var (
	helpChatSurfaceBackground = lipgloss.Color("234")
	helpChatSurfaceFillStyle  = lipgloss.NewStyle().Background(helpChatSurfaceBackground)
	helpChatTitleStyle        = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("81")).Background(helpChatSurfaceBackground)
	helpChatStatusStyle       = footerStatusStyle.Background(helpChatSurfaceBackground)
	helpChatUsageStyle        = footerUsageStyle.Background(helpChatSurfaceBackground)
	helpChatPanelStyle        = lipgloss.NewStyle().
					Border(lipgloss.RoundedBorder()).
					BorderForeground(lipgloss.Color("81")).
					Padding(0, 1).
					Background(helpChatSurfaceBackground).
					Foreground(lipgloss.Color("252"))
)
