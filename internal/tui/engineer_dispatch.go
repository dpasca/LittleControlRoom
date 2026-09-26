package tui

import (
	"context"
	"fmt"
	"strings"

	"lcroom/internal/codexapp"
	"lcroom/internal/control"
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
		if bossEngineerSnapshotActive(snapshot) {
			return engineerDispatchReplyRoutedMsg{projectPath: projectPath}
		}
		ctx, cancel := context.WithTimeout(parent, engineerMessageStoreTimeout)
		defer cancel()
		completion := service.EngineerTurnCompletion{
			ProjectPath: projectPath,
			Provider:    modelSessionSourceFromCodexProvider(embeddedProvider(snapshot)),
			SessionID:   strings.TrimSpace(snapshot.ThreadID),
			Summary:     latestEngineerTranscriptReviewOutput(snapshot),
			Problem:     strings.TrimSpace(snapshot.LastError),
		}
		dispatches, err := svc.RouteEngineerTurnCompletion(ctx, completion)
		var replies []tea.Cmd
		for _, dispatch := range dispatches {
			msg := engineerDispatchReplyRoutedMsg{projectPath: projectPath, dispatch: dispatch, routed: true}
			replies = append(replies, func() tea.Msg { return msg })
		}
		if err != nil {
			replies = append(replies, func() tea.Msg { return engineerDispatchReplyRoutedMsg{projectPath: projectPath, err: err} })
		}
		return tea.BatchMsg(replies)
	}
}

// A short turn can settle before its delivery receipt arms the return address.
// Recheck after the receipt is saved; all live session reads stay off the UI
// thread, and a busy or replaced worker is left to its normal completion path.
func (m Model) reconcileEngineerMessageReplyCmd(message control.EngineerMessage) tea.Cmd {
	manager := m.codexManager
	if manager == nil || message.AgentTaskID != "" || !control.IsExternalOperationID(message.OperationID) {
		return nil
	}
	return func() tea.Msg {
		session, ok := manager.Session(message.ProjectPath)
		if !ok || session == nil {
			return nil
		}
		snapshot := session.Snapshot()
		if embeddedProvider(snapshot) != codexProviderFromControlProvider(message.Provider) ||
			strings.TrimSpace(snapshot.ThreadID) != strings.TrimSpace(message.TargetSessionID) ||
			!snapshot.Started || snapshot.Closed || bossEngineerSnapshotActive(snapshot) {
			return nil
		}
		if cmd := m.routeEngineerDispatchReplyCmd(message.ProjectPath, snapshot, session); cmd != nil {
			return cmd()
		}
		return nil
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
	m.status = label + " finished; its report is queued for the requesting session."
	return m, m.requestEngineerMessagesPollCmd()
}
