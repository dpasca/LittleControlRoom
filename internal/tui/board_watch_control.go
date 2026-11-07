package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"lcroom/internal/boardwatch"
	"lcroom/internal/control"

	tea "github.com/charmbracelet/bubbletea"
)

// boardWatchControlMsg reports an operator-confirmed owner-board watch
// registration or an unregistration.
type boardWatchControlMsg struct {
	status string
	err    error
}

// executeBoardWatchControl runs engineer.board_watch_register (after operator
// confirmation) or engineer.board_watch_unregister. File checks and database
// writes run in a tea.Cmd, never on the UI thread.
func (m Model) executeBoardWatchControl(inv control.Invocation) controlInvocationOutcome {
	svc, parent := m.svc, m.ctx
	if parent == nil {
		parent = context.Background()
	}
	switch inv.Capability {
	case control.CapabilityEngineerBoardWatchRegister:
		var input control.EngineerBoardWatchRegisterInput
		if err := json.Unmarshal(inv.Args, &input); err != nil {
			m.status = "Control request invalid: " + err.Error()
			return controlInvocationOutcome{model: m, err: err}
		}
		return controlInvocationOutcome{model: m, cmd: func() tea.Msg {
			if svc == nil || svc.Store() == nil {
				err := errors.New("store unavailable")
				return boardWatchControlMsg{status: "Board watch was not registered: " + err.Error(), err: err}
			}
			ctx, cancel := context.WithTimeout(parent, engineerMessageStoreTimeout)
			defer cancel()
			prepared, err := boardwatch.PrepareRegistration(ctx, svc.Store(), input)
			if err != nil {
				return boardWatchControlMsg{status: "Board watch was not registered: " + err.Error(), err: err}
			}
			watch, created, err := svc.Store().RegisterBoardWatch(ctx, prepared)
			if err != nil {
				return boardWatchControlMsg{status: "Board watch was not registered: " + err.Error(), err: err}
			}
			verb := "already registered"
			if created {
				verb = "registered"
			}
			return boardWatchControlMsg{status: fmt.Sprintf("Owner board %s %s (%s): review clicks will wake the director while Little Control Room runs.", watch.BoardID, verb, watch.ID)}
		}}
	case control.CapabilityEngineerBoardWatchUnregister:
		var input control.EngineerBoardWatchUnregisterInput
		if err := json.Unmarshal(inv.Args, &input); err != nil {
			m.status = "Control request invalid: " + err.Error()
			return controlInvocationOutcome{model: m, err: err}
		}
		return controlInvocationOutcome{model: m, cmd: func() tea.Msg {
			if svc == nil || svc.Store() == nil {
				err := errors.New("store unavailable")
				return boardWatchControlMsg{status: "Board watch was not unregistered: " + err.Error(), err: err}
			}
			ctx, cancel := context.WithTimeout(parent, engineerMessageStoreTimeout)
			defer cancel()
			watch, withdrawn, err := svc.Store().UnregisterBoardWatch(ctx, input.WatchID)
			if err != nil {
				return boardWatchControlMsg{status: "Board watch was not unregistered: " + err.Error(), err: err}
			}
			return boardWatchControlMsg{status: fmt.Sprintf("Owner board %s unregistered; %d undelivered review wake(s) withdrawn.", watch.BoardID, len(withdrawn))}
		}}
	}
	err := fmt.Errorf("unsupported board watch capability %q", inv.Capability)
	return controlInvocationOutcome{model: m, err: err}
}

func (m Model) applyBoardWatchControl(msg boardWatchControlMsg) (tea.Model, tea.Cmd) {
	m.status = msg.status
	if msg.err != nil {
		m.reportError("Owner board watch change failed", msg.err, "")
	}
	return m, nil
}
