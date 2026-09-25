package tui

import (
	"context"
	"fmt"
	"strings"

	"lcroom/internal/codexapp"
	"lcroom/internal/model"
	"lcroom/internal/service"

	tea "github.com/charmbracelet/bubbletea"
)

type engineerDispatchReplyRoutedMsg struct {
	projectPath string
	dispatch    model.EngineerDispatch
	routed      bool
	err         error
}

// routeEngineerDispatchReplyCmd reports a settled turn to the embedded session
// that dispatched this engineer, if one is waiting. Most completions have no
// dispatcher; the store lookup happens off the UI thread either way.
func (m Model) routeEngineerDispatchReplyCmd(projectPath string, snapshot codexapp.Snapshot, session codexapp.Session) tea.Cmd {
	projectPath = strings.TrimSpace(projectPath)
	if projectPath == "" || m.svc == nil || m.svc.Store() == nil {
		return nil
	}
	svc := m.svc
	parent := m.ctx
	if parent == nil {
		parent = context.Background()
	}
	return func() tea.Msg {
		snapshot := freshEngineerCompletionSnapshot(projectPath, snapshot, session)
		ctx, cancel := context.WithTimeout(parent, engineerMessageStoreTimeout)
		defer cancel()
		dispatch, routed, err := svc.RouteEngineerTurnCompletion(ctx, service.EngineerTurnCompletion{
			ProjectPath: projectPath,
			Provider:    modelSessionSourceFromCodexProvider(embeddedProvider(snapshot)),
			SessionID:   strings.TrimSpace(snapshot.ThreadID),
			Summary:     latestEngineerTranscriptReviewOutput(snapshot),
			Problem:     strings.TrimSpace(snapshot.LastError),
		})
		return engineerDispatchReplyRoutedMsg{projectPath: projectPath, dispatch: dispatch, routed: routed, err: err}
	}
}

func (m Model) applyEngineerDispatchReplyRouted(msg engineerDispatchReplyRoutedMsg) (tea.Model, tea.Cmd) {
	label := "Dispatched engineer"
	if msg.dispatch.TodoID > 0 {
		label = fmt.Sprintf("TODO #%d engineer", msg.dispatch.TodoID)
	}
	if msg.err != nil {
		m.appendBackgroundErrorLogEntry("Engineer completion report failed", msg.err, msg.projectPath)
		m.status = label + " finished, but its report to the requesting session failed: " + msg.err.Error()
		return m, nil
	}
	if !msg.routed {
		return m, nil
	}
	if msg.dispatch.ReplyState != model.EngineerDispatchReplySent {
		m.status = label + " finished; its report is waiting: " + firstNonEmptyTrimmed(msg.dispatch.ReplyError, "the requesting session is not ready")
		return m, nil
	}
	m.status = label + " finished; its report is queued for the session that launched it."
	return m, m.requestEngineerMessagesPollCmd()
}
