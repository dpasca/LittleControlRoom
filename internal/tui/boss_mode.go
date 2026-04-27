package tui

import (
	"fmt"
	"strings"

	bossui "lcroom/internal/boss"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func (m Model) openBossMode() (tea.Model, tea.Cmd) {
	m.bossMode = true
	m.bossModel = bossui.NewEmbeddedWithViewContext(m.ctx, m.svc, m.bossViewContext())
	m.status = "Boss mode open. Alt+Up returns to the classic TUI."
	if m.width > 0 && m.height > 0 {
		updated, _ := m.bossModel.Update(m.bossModeWindowSizeMsg())
		m.bossModel = normalizeBossModel(updated)
	}
	return m, m.bossModel.Init()
}

func (m *Model) closeBossMode(status string) {
	m.bossMode = false
	m.bossModel = bossui.Model{}
	if status != "" {
		m.status = status
	}
	m.syncDetailViewport(false)
}

func (m Model) updateBossModeMessage(msg tea.Msg) (tea.Model, tea.Cmd) {
	updated, cmd := m.bossModel.Update(msg)
	m.bossModel = normalizeBossModel(updated)
	return m, cmd
}

func (m Model) updateBossModeWindowSize() (tea.Model, tea.Cmd) {
	updated, cmd := m.bossModel.Update(m.bossModeWindowSizeMsg())
	m.bossModel = normalizeBossModel(updated)
	return m, cmd
}

func (m Model) bossModeWindowSizeMsg() tea.WindowSizeMsg {
	layout := m.bodyLayout()
	return tea.WindowSizeMsg{Width: layout.width, Height: bossModeBodyHeight(layout.height)}
}

func (m Model) renderBossModeView() string {
	layout := m.bodyLayout()
	header := m.renderBossModeHeader(layout.width)
	bodyHeight := bossModeBodyHeight(layout.height)
	body := fitPaneContent(m.bossModel.View(), layout.width, bodyHeight)
	return strings.Join([]string{header, body, m.renderBossModeFooter(layout.width)}, "\n")
}

func (m Model) renderBossModeHeader(width int) string {
	line := strings.Join([]string{
		bossModeTitleStyle.Render("Boss Mode"),
		renderFooterStatus(m.bossModel.StatusText()),
		renderFooterMeta("high-level project chat"),
	}, "  ")
	return fitStyledWidth(line, width)
}

func (m Model) renderBossModeFooter(width int) string {
	return fitStyledWidth(renderFooterLine(
		width,
		renderFooterActionList(
			footerPrimaryAction("Enter", "send"),
			footerNavAction("Alt+Enter", "newline"),
			footerNavAction("Ctrl+R", "refresh"),
			footerHideAction("Alt+Up", "hide"),
		),
		renderFooterMeta("/boss off also closes"),
	), width)
}

var bossModeTitleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("81"))

func bossModeBodyHeight(shellBodyHeight int) int {
	return max(1, shellBodyHeight)
}

func (m Model) bossViewContext() bossui.ViewContext {
	view := bossui.ViewContext{
		Active:              true,
		Embedded:            true,
		Loading:             m.loading,
		AllProjectCount:     len(m.allProjects),
		VisibleProjectCount: len(m.projects),
		FocusedPane:         string(m.focusedPane),
		SortMode:            string(m.sortMode),
		Visibility:          string(m.visibility),
		Filter:              strings.TrimSpace(m.projectFilter),
		Status:              strings.TrimSpace(m.status),
	}
	return view
}

func normalizeBossModel(model tea.Model) bossui.Model {
	switch typed := model.(type) {
	case bossui.Model:
		return typed
	case *bossui.Model:
		if typed == nil {
			panic("boss mode update returned nil *boss.Model")
		}
		return *typed
	default:
		panic(fmt.Sprintf("boss mode update returned unsupported model type %T", model))
	}
}
