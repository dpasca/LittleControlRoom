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
	automatic bool
	preflight externalControlPreflight
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
	// standingUnavailable is set when the arguments could be saved as a standing
	// permission but the live repository state does not qualify.
	standingUnavailable string
	reviewing           bool
	submitting          bool
	errorText           string
	scrollOffset        int
	showDetails         bool
}

const externalControlReviewKey = "ctrl+g"

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
		operation, automatic, err := svc.Store().ConfirmProjectCollaboration(parent, operationID)
		var preflight externalControlPreflight
		if err == nil && !automatic && operation.Status == control.OperationWaitingForConfirmation {
			// Saved permissions match typed arguments only. Some capabilities also
			// depend on live repository state, so inspect it before consulting them.
			preflight = inspectSubmoduleAlignProposal(parent, operation)
		}
		if err == nil && !automatic && preflight.allowsStandingPermission() {
			operation, automatic, err = svc.Store().ConfirmControlPermission(parent, operationID)
		}
		if err == nil && !automatic {
			// A task's own correction grant is the other standing authorization.
			// It consumes one round only when it actually authorizes this run.
			operation, automatic, err = svc.Store().ConfirmDelegationSupervision(parent, operationID)
		}
		return externalControlProposalLoadedMsg{operation: operation, automatic: automatic, preflight: preflight, err: err}
	}
}

func (m Model) applyExternalControlProposalLoaded(msg externalControlProposalLoadedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.appendBackgroundErrorLogEntry("Agent control proposal failed", msg.err, "")
		m.status = "Agent control proposal failed: " + msg.err.Error()
		return m, nil
	}
	if msg.automatic {
		m.status = "Executing an operation covered by an LCR permission"
		if msg.operation.ConfirmationBy == control.ConfirmationDelegationSupervision {
			m.status = "Running an authorized correction round for this delegated task"
		}
		return m, func() tea.Msg {
			return bossui.ControlInvocationConfirmedMsg{Invocation: msg.operation.Invocation, OperationRecorded: true}
		}
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
	if msg.preflight.refusal != nil {
		// Never ask the operator to confirm something that would refuse. The
		// agent learns the precise reason from the failed operation.
		m.status = "Agent request refused: " + msg.preflight.refusal.Error()
		return m, m.failExternalControlOperationCmd(msg.operation.ID, msg.preflight.refusal)
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
	if msg.preflight.applies {
		preview = msg.preflight.preview
	}
	m.externalControlConfirmation = &externalControlConfirmationState{
		operation:           msg.operation,
		preview:             preview,
		standingUnavailable: msg.preflight.standingNote,
	}
	m.status = "Agent request waiting; current input remains active until Ctrl+G opens review"
	return m, nil
}

func (m Model) externalControlReviewActive() bool {
	return m.externalControlConfirmation != nil && m.externalControlConfirmation.reviewing
}

func (m Model) externalControlReviewWaiting() bool {
	return m.externalControlConfirmation != nil && !m.externalControlConfirmation.reviewing
}

func (m Model) openExternalControlConfirmationReview() (tea.Model, tea.Cmd) {
	if m.externalControlConfirmation == nil {
		return m, nil
	}
	confirmation := *m.externalControlConfirmation
	confirmation.reviewing = true
	m.externalControlConfirmation = &confirmation
	m.status = "Review the embedded agent's control proposal"
	return m, nil
}

func (m Model) renderExternalControlPendingNotice() string {
	if !m.externalControlReviewWaiting() {
		return ""
	}
	return joinFooterSegments(
		renderFooterAlert("Agent request waiting"),
		renderFooterActionList(footerPrimaryAction("ctrl+g", "review")),
	)
}

func (m Model) updateExternalControlConfirmationMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if !m.externalControlReviewActive() {
		return m, nil
	}
	if m.externalControlConfirmation.submitting {
		return m, nil
	}
	invocation := m.externalControlConfirmation.operation.Invocation
	switch msg.String() {
	case "d":
		state := *m.externalControlConfirmation
		state.showDetails = !state.showDetails
		state.scrollOffset = 0
		m.externalControlConfirmation = &state
		return m, nil
	case "up", "down", "pgup", "pgdown", "home", "end":
		layout := m.bodyLayout()
		width, height := layout.width, layout.height
		if m.codexVisible() && m.diffView == nil {
			width, height = m.width, m.height
		}
		_, maxScroll, err := m.externalControlConfirmationPanel(width, height)
		if err != nil {
			return m, nil
		}
		state := *m.externalControlConfirmation
		state.scrollOffset = min(state.scrollOffset, maxScroll)
		switch msg.String() {
		case "up":
			state.scrollOffset--
		case "down":
			state.scrollOffset++
		case "pgup":
			state.scrollOffset -= max(1, height/2)
		case "pgdown":
			state.scrollOffset += max(1, height/2)
		case "home":
			state.scrollOffset = 0
		case "end":
			state.scrollOffset = maxScroll
		}
		state.scrollOffset = max(0, min(state.scrollOffset, maxScroll))
		m.externalControlConfirmation = &state
		return m, nil
	case "s", "p":
		if _, ok := control.PermissionForOperation(m.externalControlConfirmation.operation); !ok || m.externalControlConfirmation.standingUnavailable != "" {
			return m, nil
		}
		state := *m.externalControlConfirmation
		state.submitting = true
		state.errorText = ""
		m.externalControlConfirmation = &state
		return m, m.approveControlPermissionCmd(state.operation.ID, msg.String() == "s")
	case "a":
		if _, ok := control.CollaborationForOperation(m.externalControlConfirmation.operation); !ok {
			return m, nil
		}
		state := *m.externalControlConfirmation
		state.submitting = true
		state.errorText = ""
		m.externalControlConfirmation = &state
		return m, m.approveProjectCollaborationCmd(state.operation.ID)
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

func (m Model) externalControlConfirmationPanel(bodyW, bodyH int) (string, int, error) {
	confirmation := m.externalControlConfirmation
	return bossui.RenderPermissionConfirmationDialog(confirmation.operation, bossui.PermissionConfirmationOptions{
		Preview: confirmation.preview, Busy: confirmation.submitting,
		ErrorText: confirmation.errorText, ScrollOffset: confirmation.scrollOffset,
		ShowDetails: confirmation.showDetails, StandingUnavailable: confirmation.standingUnavailable,
	}, bodyW, bodyH)
}

func (m Model) renderExternalControlConfirmationOverlay(body string, bodyW, bodyH int) string {
	if !m.externalControlReviewActive() {
		return body
	}
	panel, _, err := m.externalControlConfirmationPanel(bodyW, bodyH)
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
			return m.loadExternalControlProposalCmd(operation.ID)()
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
		if msg.OperationPending && msg.Err == nil {
			status = control.OperationRunning
		} else if msg.Err != nil {
			status = control.OperationFailed
		}
		resultPayload := map[string]any{"status": strings.TrimSpace(msg.Status)}
		if msg.WorktreeResult != nil {
			resultPayload["worktree"] = msg.WorktreeResult
		}
		if msg.SubmoduleAlign != nil {
			resultPayload["submodule_align"] = msg.SubmoduleAlign
		}
		if msg.IntegrationResult != nil {
			resultPayload["integration_result"] = msg.IntegrationResult
		}
		if msg.Activity != nil {
			resultPayload["activity"] = msg.Activity
		}
		if msg.Delivery != nil {
			resultPayload["delivery"] = msg.Delivery
		}
		result, _ := json.Marshal(resultPayload)
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
