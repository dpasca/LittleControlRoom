package tui

import (
	"context"
	"encoding/json"
	"fmt"

	bossui "lcroom/internal/boss"
	"lcroom/internal/control"
	"lcroom/internal/integrations"

	tea "github.com/charmbracelet/bubbletea"
)

type integrationAppliedMsg struct {
	inv    control.Invocation
	result integrations.Result
	err    error
	dialog bool
}

func (m Model) integrationManager() *integrations.Manager {
	options := integrations.Options{CodexHome: m.codexHome()}
	if m.svc != nil {
		options.DataDir = m.svc.Config().DataDir
	}
	return integrations.New(options)
}

func (m Model) executeIntegrationsControl(inv control.Invocation) controlInvocationOutcome {
	var input control.IntegrationsManageInput
	if err := json.Unmarshal(inv.Args, &input); err != nil {
		return controlInvocationOutcome{model: m, err: err}
	}
	if input.Scope == "project" {
		if _, err := m.resolveControlProjectRef(input.ProjectPath, ""); err != nil {
			return controlInvocationOutcome{model: m, err: err}
		}
	}
	m.status = "Applying integration change..."
	return controlInvocationOutcome{model: m, cmd: m.applyIntegrationCmd(inv, false), deferBossResult: true}
}

func (m Model) applyIntegrationCmd(inv control.Invocation, dialog bool) tea.Cmd {
	manager := m.integrationManager()
	ctx := m.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		var input control.IntegrationsManageInput
		if err := json.Unmarshal(inv.Args, &input); err != nil {
			return integrationAppliedMsg{inv: inv, err: err, dialog: dialog}
		}
		result, err := manager.Apply(ctx, input.Change)
		return integrationAppliedMsg{inv: inv, result: result, err: err, dialog: dialog}
	}
}

func (m Model) applyIntegrationResult(msg integrationAppliedMsg) (tea.Model, tea.Cmd) {
	status := msg.result.Status
	if msg.err != nil {
		status = "Integration action failed: " + msg.err.Error()
	}
	m.status = status
	cmds := []tea.Cmd{}
	if msg.dialog {
		m.integrationDialogBusy = false
	}
	if m.skillsDialog != nil && (msg.dialog || !m.skillsDialog.Busy) {
		m.skillsDialog.Busy = false
		m.skillsDialog.Notice = status
		m.skillsDialog.LastResult = &msg.result
		m.skillsDialog.ShowingResult = msg.dialog
		m.skillsDialog.PreviewOffset = 0
		// Invalidate an inventory worker that may have started before this write.
		// Its sequence will no longer match the newly queued refresh.
		m.skillsDialog.Loading = false
		cmds = append(cmds, m.refreshSkillsDialog())
	}
	if !msg.dialog {
		result := bossui.ControlInvocationResultMsg{Invocation: msg.inv, Status: status, Err: msg.err, AnnounceInChat: true, IntegrationResult: &msg.result}
		if msg.err != nil {
			result.IntegrationResult = nil
		}
		cmds = append(cmds, func() tea.Msg { return result })
	}
	return m, tea.Batch(cmds...)
}

func integrationInvocation(change integrations.Change) (control.Invocation, error) {
	raw, err := json.Marshal(control.IntegrationsManageInput{Change: change})
	if err != nil {
		return control.Invocation{}, fmt.Errorf("encode integration change: %w", err)
	}
	return control.ValidateInvocation(control.Invocation{Capability: control.CapabilityIntegrationsManage, Args: raw})
}
