package tui

import (
	"fmt"

	bossui "lcroom/internal/boss"

	tea "github.com/charmbracelet/bubbletea"
)

func (m Model) openBossMode() (tea.Model, tea.Cmd) {
	m.bossMode = true
	m.bossModel = bossui.NewEmbedded(m.ctx, m.svc)
	m.status = "Boss mode open. Esc returns to the classic TUI."
	if m.width > 0 && m.height > 0 {
		updated, _ := m.bossModel.Update(tea.WindowSizeMsg{Width: m.width, Height: m.height})
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
