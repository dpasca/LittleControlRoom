package boss

import (
	"context"
	"strings"
	"time"

	"lcroom/internal/codexskills"

	tea "github.com/charmbracelet/bubbletea"
)

type bossSkillsInventoryMsg struct {
	inventory codexskills.Inventory
	err       error
}

func (m Model) loadSkillsInventoryCmd() tea.Cmd {
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
		inv, err := codexskills.LoadInventory(ctx, codexHome, m.now())
		return bossSkillsInventoryMsg{inventory: inv, err: err}
	}
}

func (m Model) applyBossSkillsInventoryMsg(msg bossSkillsInventoryMsg) (tea.Model, tea.Cmd) {
	content := ""
	if msg.err != nil {
		content = "Codex skills scan failed: " + msg.err.Error()
		m.status = "Codex skills scan failed"
	} else {
		content = codexskills.FormatInventoryReport(msg.inventory, 40)
		m.status = "Codex skills inventory"
	}
	content = strings.TrimSpace(content)
	if content == "" {
		content = "No Codex skills report is available."
	}
	m.messages = append(m.messages, ChatMessage{
		Role:    "assistant",
		Content: content,
		At:      m.now(),
	})
	m.syncLayout(true)
	return m, nil
}
