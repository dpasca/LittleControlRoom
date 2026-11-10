package tui

import (
	"context"
	"fmt"
	"time"

	"lcroom/internal/codexapp"

	tea "github.com/charmbracelet/bubbletea"
)

func (m Model) requestVisibleClaudeCheckpoint(snapshot codexapp.Snapshot, note string) (tea.Model, tea.Cmd) {
	path := m.codexVisibleProject
	switch {
	case embeddedProvider(snapshot) != codexapp.ProviderClaudeCode:
		m.status = "/checkpoint currently supports Claude Code sessions."
	case snapshot.BusyExternal:
		m.status = "This Claude Code session is owned by another process and cannot be checkpointed here."
	case m.codexCheckpointPending[path] || snapshot.CheckpointPending:
		m.status = "Claude Code checkpoint is already in progress; ctrl+c cancels it."
	case !snapshot.Busy || snapshot.Closed || snapshot.Compacting:
		m.status = "Claude Code needs an active, non-compacting turn to checkpoint."
	default:
		if m.codexCheckpointPending == nil {
			m.codexCheckpointPending = make(map[string]bool)
		}
		m.codexCheckpointPending[path] = true
		m.status = "Interrupting Claude Code's active tools, then requesting a checkpoint report..."
		return m, m.codexSessionCmd(path, func() tea.Msg {
			return codexActionMsg{projectPath: path, checkpointDone: true, err: fmt.Errorf("checkpoint target is no longer open")}
		}, func(session codexapp.Session) tea.Msg {
			result := codexActionMsg{projectPath: path, checkpointDone: true, refreshView: true}
			current := session.Snapshot()
			if current.ThreadID != snapshot.ThreadID || current.ControlSessionKey != snapshot.ControlSessionKey {
				result.err = fmt.Errorf("%w: checkpoint target was replaced", codexapp.ErrSessionChanged)
				return result
			}
			requester, ok := session.(codexapp.CheckpointRequester)
			if !ok {
				result.err = fmt.Errorf("this session cannot interrupt and checkpoint")
				return result
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			result.err = requester.RequestCheckpoint(ctx, note)
			if result.err == nil {
				result.status = "Checkpoint report requested from Claude Code; report completion is not yet confirmed"
			}
			return result
		})
	}
	return m, nil
}
