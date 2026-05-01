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

func (m Model) updateBossModeKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if !m.bossModel.SessionPickerActive() {
		if index, ok := bossModeAttentionJumpIndex(msg); ok {
			return m.openBossAttentionProject(index)
		}
	}
	return m.updateBossModeMessage(msg)
}

func (m Model) openBossAttentionProject(index int) (tea.Model, tea.Cmd) {
	projectPath := m.bossModel.HotProjectPath(index)
	if projectPath == "" {
		m.status = fmt.Sprintf("No attention project is mapped to Alt+%d", index+1)
		return m, nil
	}
	project, ok := m.projectSummaryByPathAllProjects(projectPath)
	if !ok {
		m.status = fmt.Sprintf("Project for Alt+%d is no longer in the list", index+1)
		return m, nil
	}
	m.closeBossMode(fmt.Sprintf("Opening engineer session for %s", projectNameForPicker(project, project.Path)))
	focusCmd := m.focusProjectPath(project.Path)
	updated, launchCmd := m.launchEmbeddedForProject(project, m.preferredEmbeddedProviderForProject(project), false, "")
	m = normalizeUpdateModel(updated)
	return m, tea.Batch(focusCmd, launchCmd)
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
	actions := []footerAction{
		footerPrimaryAction("Enter", "send"),
		footerNavAction("Alt+Enter", "newline"),
		footerNavAction("Alt+1..8", "jump"),
		footerLowAction("Alt+C", "copy menu"),
		footerNavAction("Ctrl+R", "refresh"),
		footerHideAction("Alt+Up", "hide"),
	}
	if m.bossModel.SlashActive() {
		actions = []footerAction{
			footerPrimaryAction("Enter", "run"),
			footerNavAction("Tab", "complete"),
			footerNavAction("Shift+Tab", "previous"),
			footerNavAction("Alt+Enter", "newline"),
			footerLowAction("Alt+C", "copy menu"),
			footerHideAction("Alt+Up", "hide"),
		}
	}
	if m.bossModel.ControlConfirmationActive() {
		actions = []footerAction{
			footerPrimaryAction("Enter", "send"),
			footerExitAction("Esc", "cancel"),
			footerHideAction("Alt+Up", "hide"),
		}
	}
	if m.bossModel.SessionPickerActive() {
		actions = []footerAction{
			footerPrimaryAction("Enter", "open"),
			footerNavAction("Up/Down", "select"),
			footerExitAction("Esc", "close"),
			footerHideAction("Alt+Up", "hide"),
		}
	}
	return fitStyledWidth(renderFooterLine(
		width,
		renderFooterActionList(actions...),
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
		PrivacyMode:         m.privacyMode,
		PrivacyPatterns:     append([]string(nil), m.privacyPatterns...),
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

func bossModeAttentionJumpIndex(msg tea.KeyMsg) (int, bool) {
	if !msg.Alt {
		return 0, false
	}
	key := strings.TrimPrefix(msg.String(), "alt+")
	if len(key) == 1 && key[0] >= '1' && key[0] <= '8' {
		return int(key[0] - '1'), true
	}
	if len(msg.Runes) == 1 && msg.Runes[0] >= '1' && msg.Runes[0] <= '8' {
		return int(msg.Runes[0] - '1'), true
	}
	return 0, false
}
