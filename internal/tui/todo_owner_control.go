package tui

import (
	"encoding/json"
	"fmt"

	"lcroom/internal/control"

	tea "github.com/charmbracelet/bubbletea"
)

// Called only in background preflight commands, never Update or rendering.
func (m Model) preflightTodoTargetSession(inv control.Invocation) error {
	if inv.Capability != control.CapabilityEngineerSendPrompt || m.codexManager == nil {
		return nil
	}
	var input control.EngineerSendPromptInput
	if err := json.Unmarshal(inv.Args, &input); err != nil {
		return err
	}
	if input.TodoID <= 0 || input.TargetSessionID == "" {
		return nil
	}
	session, live := m.codexManager.Session(input.DestinationPath)
	if !live {
		return nil
	}
	snapshot := session.Snapshot()
	if controlProviderFromCodexProvider(embeddedProvider(snapshot)) != input.Provider {
		return fmt.Errorf("exact target provider changed before confirmation")
	}
	if snapshot.ThreadID != "" && snapshot.ThreadID != input.TargetSessionID && !m.embeddedSessionMatchesResumeID(input.DestinationPath, input.TargetSessionID) {
		return fmt.Errorf("exact target session changed before confirmation: expected %s, found %s", input.TargetSessionID, snapshot.ThreadID)
	}
	return nil
}

type todoOwnerRepairedMsg struct {
	input  control.TodoRepairOwnerInput
	status string
	err    error
}

func (m Model) executeTodoOwnerRepairControl(inv control.Invocation) controlInvocationOutcome {
	var input control.TodoRepairOwnerInput
	if err := json.Unmarshal(inv.Args, &input); err != nil {
		return controlInvocationOutcome{model: m, err: err}
	}
	m.status = fmt.Sprintf("Repairing TODO #%d repository owner...", input.TodoID)
	return controlInvocationOutcome{model: m, cmd: func() tea.Msg {
		ctx, cancel := m.actionContext(tuiQuickActionTimeout)
		defer cancel()
		msg := todoOwnerRepairedMsg{input: input}
		if m.svc == nil {
			msg.err = fmt.Errorf("service unavailable")
		} else {
			var changed bool
			changed, msg.err = m.svc.RepairTodoOwner(ctx, input)
			if changed {
				msg.status = fmt.Sprintf("Repaired TODO #%d owner to %s; worktree, exact session and launch provenance retained.", input.TodoID, input.RepositoryPath)
			} else {
				msg.status = fmt.Sprintf("TODO #%d repair already applied; audited no-op.", input.TodoID)
			}
		}
		if msg.err != nil {
			msg.status = "TODO owner repair refused: " + msg.err.Error()
		}
		return msg
	}}
}

func (m Model) applyTodoOwnerRepaired(msg todoOwnerRepairedMsg) (tea.Model, tea.Cmd) {
	m.status = msg.status
	if msg.err != nil {
		m.appendErrorLogEntry("TODO owner repair refused", msg.err, msg.input.RepositoryPath)
		return m, nil
	}
	return m, batchCmds(m.requestProjectInvalidationCmd(invalidateProjectData(msg.input.ExpectedOwnerPath)), m.requestProjectInvalidationCmd(invalidateProjectData(msg.input.RepositoryPath)))
}
