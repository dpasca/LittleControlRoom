package tui

import (
	"context"
	"time"

	"lcroom/internal/service"

	tea "github.com/charmbracelet/bubbletea"
)

// Restart is deliberately conservative: old results remain inspectable, but a
// new host cannot establish that an old caller is still waiting for this action.
var controlContinuationHostStarted = time.Now()

type controlContinuationsProcessedMsg struct{ err error }

func (m *Model) requestControlContinuationsCmd() tea.Cmd {
	if m.controlContinuationsInFlight || m.svc == nil || m.codexManager == nil {
		return nil
	}
	m.controlContinuationsInFlight = true
	st, manager, parent := m.svc.Store(), m.codexManager, m.ctx
	if parent == nil {
		parent = context.Background()
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parent, 30*time.Second)
		defer cancel()
		return controlContinuationsProcessedMsg{err: service.ProcessControlContinuations(ctx, st, manager, controlContinuationHostStarted)}
	}
}
