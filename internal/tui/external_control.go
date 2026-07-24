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
	"github.com/charmbracelet/lipgloss"
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
	operation control.Operation
	err       error
}

type externalControlConfirmationState struct {
	operation control.Operation
	preview   string
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
		switch msg.operation.Status {
		case control.OperationCanceled:
			m.status = "Agent control proposal was already canceled; no confirmation is pending"
		case control.OperationCompleted:
			m.status = "Agent control proposal already completed"
		case control.OperationFailed:
			m.status = "Agent control proposal already failed; no confirmation is pending"
		}
		return m, nil
	}
	if m.externalControlConfirmation != nil {
		m.status = "Agent control proposal queued behind the current confirmation"
		return m, m.retryExternalControlProposalCmd(msg.operation)
	}
	invocation, err := control.ValidateInvocation(msg.operation.Invocation)
	if err != nil {
		m.status = "Agent control proposal could not be presented: " + err.Error()
		return m, m.failExternalControlOperationCmd(msg.operation.ID, err)
	}
	msg.operation.Invocation = invocation
	preview := fmt.Sprintf(
		"%s proposed `%s` through Little Control Room's agent control surface.",
		firstNonEmptyTrimmed(msg.operation.Provider, "An embedded agent"),
		msg.operation.Capability,
	)
	m.externalControlConfirmation = &externalControlConfirmationState{
		operation: msg.operation,
		preview:   preview,
	}
	m.status = "Confirm or cancel the embedded agent's control proposal"
	return m, nil
}

func (m Model) updateExternalControlConfirmationMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.externalControlConfirmation == nil {
		return m, nil
	}
	invocation := m.externalControlConfirmation.operation.Invocation
	switch msg.String() {
	case "enter":
		m.externalControlConfirmation = nil
		m.status = bossui.ControlProposalSubmittingStatus(invocation)
		return m, func() tea.Msg {
			return bossui.ControlInvocationConfirmedMsg{Invocation: invocation}
		}
	case "esc", "ctrl+c":
		m.externalControlConfirmation = nil
		m.status = "Control action canceled"
		return m, func() tea.Msg {
			return bossui.ControlInvocationCanceledMsg{Invocation: invocation}
		}
	case "q":
		if invocation.Capability == control.CapabilityTodoCreateWorktreeAndStartEngineer {
			m.status = "Use Enter or Esc for an externally proposed tracked-work operation"
		}
	}
	return m, nil
}

func (m Model) renderExternalControlConfirmationOverlay(body string, bodyW, bodyH int) string {
	if m.externalControlConfirmation == nil {
		return body
	}
	confirmation := m.externalControlConfirmation
	panel, err := bossui.RenderControlConfirmationDialog(
		confirmation.operation.Invocation,
		confirmation.preview,
		bodyW,
		bodyH,
	)
	if err != nil {
		return body
	}
	left := max(0, (bodyW-lipgloss.Width(panel))/2)
	top := max(0, min((bodyH-lipgloss.Height(panel))/3, bodyH-lipgloss.Height(panel)))
	return overlayBlock(body, panel, bodyW, bodyH, left, top)
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
		var (
			operation control.Operation
			err       error
		)
		if svc == nil || svc.Store() == nil {
			err = errors.New("service store unavailable")
		} else {
			operation, err = svc.Store().UpdateControlOperationStatus(
				parent,
				msg.Invocation.RequestID,
				control.OperationCanceled,
				nil,
				nil,
			)
		}
		return externalControlCancellationRecordedMsg{operation: operation, err: err}
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
