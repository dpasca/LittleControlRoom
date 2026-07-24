package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	bossui "lcroom/internal/boss"
	"lcroom/internal/control"

	tea "github.com/charmbracelet/bubbletea"
)

type externalControlProposalLoadedMsg struct {
	operation control.Operation
	err       error
}

type externalControlConfirmationRecordedMsg struct {
	confirmed bossui.ControlInvocationConfirmedMsg
	err       error
}

type externalControlResultRecordedMsg struct {
	result bossui.ControlInvocationResultMsg
	err    error
}

type externalControlCancellationRecordedMsg struct {
	err error
}

func (m Model) loadExternalControlProposalCmd(operationID string) tea.Cmd {
	svc := m.svc
	parent := m.ctx
	if parent == nil {
		parent = context.Background()
	}
	return func() tea.Msg {
		if svc == nil || svc.Store() == nil {
			return externalControlProposalLoadedMsg{err: errors.New("service store unavailable")}
		}
		operation, err := svc.Store().GetControlOperation(parent, operationID)
		return externalControlProposalLoadedMsg{operation: operation, err: err}
	}
}

func (m Model) applyExternalControlProposalLoaded(msg externalControlProposalLoadedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.appendBackgroundErrorLogEntry("Agent control proposal failed", msg.err, "")
		m.status = "Agent control proposal failed: " + msg.err.Error()
		return m, nil
	}
	if msg.operation.Status != control.OperationWaitingForConfirmation {
		return m, nil
	}
	opened, openCmd := m.openHelpChatMode()
	m = normalizeUpdateModel(opened)
	preview := fmt.Sprintf(
		"%s proposed `%s` through Little Control Room's agent control surface.",
		firstNonEmptyTrimmed(msg.operation.Provider, "An embedded agent"),
		msg.operation.Capability,
	)
	presented, err := m.helpChatModel.PresentExternalControlProposal(msg.operation.Invocation, preview)
	if errors.Is(err, bossui.ErrControlConfirmationPending) {
		m.status = "Agent control proposal queued behind the current confirmation"
		return m, batchCmds(openCmd, m.retryExternalControlProposalCmd(msg.operation))
	}
	if err != nil {
		m.status = "Agent control proposal could not be presented: " + err.Error()
		return m, batchCmds(openCmd, m.failExternalControlOperationCmd(msg.operation.ID, err))
	}
	m.helpChatModel = presented
	m.helpChatModelActive = true
	m.helpChatMode = true
	m.status = "Confirm or cancel the embedded agent's control proposal"
	return m, openCmd
}

func (m Model) retryExternalControlProposalCmd(operation control.Operation) tea.Cmd {
	parent := m.ctx
	if parent == nil {
		parent = context.Background()
	}
	return func() tea.Msg {
		timer := time.NewTimer(500 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-parent.Done():
			return nil
		case <-timer.C:
			return externalControlProposalLoadedMsg{operation: operation}
		}
	}
}

func (m Model) recordExternalControlConfirmationCmd(msg bossui.ControlInvocationConfirmedMsg) tea.Cmd {
	svc := m.svc
	parent := m.ctx
	if parent == nil {
		parent = context.Background()
	}
	return func() tea.Msg {
		var err error
		if svc == nil || svc.Store() == nil {
			err = errors.New("service store unavailable")
		} else {
			_, err = svc.Store().UpdateControlOperationStatus(
				parent,
				msg.Invocation.RequestID,
				control.OperationRunning,
				nil,
				nil,
			)
		}
		msg.OperationRecorded = true
		return externalControlConfirmationRecordedMsg{confirmed: msg, err: err}
	}
}

func (m Model) recordExternalControlResultCmd(msg bossui.ControlInvocationResultMsg) tea.Cmd {
	svc := m.svc
	parent := m.ctx
	if parent == nil {
		parent = context.Background()
	}
	return func() tea.Msg {
		status := control.OperationCompleted
		if msg.Err != nil {
			status = control.OperationFailed
		}
		result, _ := json.Marshal(map[string]any{
			"status":   strings.TrimSpace(msg.Status),
			"activity": msg.Activity,
		})
		var err error
		if svc == nil || svc.Store() == nil {
			err = errors.New("service store unavailable")
		} else {
			_, err = svc.Store().UpdateControlOperationStatus(
				parent,
				msg.Invocation.RequestID,
				status,
				result,
				msg.Err,
			)
		}
		msg.OperationRecorded = true
		return externalControlResultRecordedMsg{result: msg, err: err}
	}
}

func (m Model) recordExternalControlCancellationCmd(msg bossui.ControlInvocationCanceledMsg) tea.Cmd {
	svc := m.svc
	parent := m.ctx
	if parent == nil {
		parent = context.Background()
	}
	return func() tea.Msg {
		var err error
		if svc == nil || svc.Store() == nil {
			err = errors.New("service store unavailable")
		} else {
			_, err = svc.Store().UpdateControlOperationStatus(
				parent,
				msg.Invocation.RequestID,
				control.OperationCanceled,
				nil,
				nil,
			)
		}
		return externalControlCancellationRecordedMsg{err: err}
	}
}

func (m Model) failExternalControlOperationCmd(operationID string, operationErr error) tea.Cmd {
	svc := m.svc
	parent := m.ctx
	if parent == nil {
		parent = context.Background()
	}
	return func() tea.Msg {
		if svc == nil || svc.Store() == nil {
			return externalControlCancellationRecordedMsg{err: errors.New("service store unavailable")}
		}
		_, err := svc.Store().UpdateControlOperationStatus(
			parent,
			operationID,
			control.OperationFailed,
			nil,
			operationErr,
		)
		return externalControlCancellationRecordedMsg{err: err}
	}
}
