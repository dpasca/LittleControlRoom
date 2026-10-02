package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"lcroom/internal/codexapp"
	"lcroom/internal/codexslash"
)

type claudePermissionsResultMsg struct{ codexActionMsg }

type claudePermissionRulesSession interface {
	ShowPermissions() error
	EditPermissionRule(action, rule string, remove bool) error
}

func (m Model) runClaudePermissions(inv codexslash.Invocation) (tea.Model, tea.Cmd) {
	if m.claudePermissionsBusy {
		return m, nil
	}
	if m.codexManager == nil {
		m.status = "Claude Code session unavailable"
		return m, nil
	}
	m.claudePermissionsBusy = true
	m.status = "Reading Claude Code permissions..."
	if inv.PermissionAction != "" {
		m.status = "Saving Claude Code permission rule..."
	}
	project := m.codexVisibleProject
	cmd := m.codexSessionCmd(project, nil, func(session codexapp.Session) tea.Msg {
		result := codexActionMsg{projectPath: project, refreshView: true}
		permissions, ok := session.(claudePermissionRulesSession)
		if !ok {
			result.err = fmt.Errorf("session does not support Claude Code permission rules")
			return result
		}
		if inv.PermissionAction == "" {
			result.err = permissions.ShowPermissions()
			result.status = "Claude Code permissions added to the transcript"
		} else {
			result.err = permissions.EditPermissionRule(inv.PermissionAction, inv.PermissionRule, inv.PermissionRemove)
			result.status = "Claude Code rule saved; use /reconnect before retrying"
		}
		return result
	})
	if cmd == nil {
		m.claudePermissionsBusy = false
		m.status = "Claude Code session unavailable"
		return m, nil
	}
	return m, func() tea.Msg { return claudePermissionsResultMsg{cmd().(codexActionMsg)} }
}
