package tui

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"lcroom/internal/codexapp"
	"lcroom/internal/projectrun"

	tea "github.com/charmbracelet/bubbletea"
)

type gracefulQuitFinishedMsg struct {
	captured int
	err      error
}

const gracefulQuitRuntimeTimeout = 30 * time.Second

func (m Model) beginGracefulQuit() (tea.Model, tea.Cmd) {
	if m.gracefulQuitInFlight {
		return m, nil
	}
	m.gracefulQuitInFlight = true
	if m.relaunchAfterUpdate {
		m.status = "Update installed; saving in-flight engineer turns before restarting..."
	} else {
		m.status = "Saving in-flight engineer turns before quitting..."
	}
	manager := m.codexManager
	runtimeManager := m.runtimeManager
	dataDir := m.appDataDir()
	return m, gracefulQuitCmd(manager, runtimeManager, dataDir)
}

func gracefulQuitCmd(manager *codexapp.Manager, runtimeManager *projectrun.Manager, dataDir string) tea.Cmd {
	return func() tea.Msg {
		captured := 0
		if manager != nil {
			intents, err := manager.CloseAllForRestart(dataDir)
			if err != nil {
				return gracefulQuitFinishedMsg{captured: len(intents), err: fmt.Errorf("close embedded sessions: %w", err)}
			}
			captured = len(intents)
		}
		if runtimeManager != nil {
			ctx, cancel := context.WithTimeout(context.Background(), gracefulQuitRuntimeTimeout)
			defer cancel()
			if err := runtimeManager.CloseAllWithContext(ctx); err != nil {
				return gracefulQuitFinishedMsg{captured: captured, err: fmt.Errorf("stop managed runtimes: %w", err)}
			}
		}
		return gracefulQuitFinishedMsg{captured: captured}
	}
}

func (m Model) applyGracefulQuitFinishedMsg(msg gracefulQuitFinishedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		slog.Error("Graceful quit paused", "captured_turns", msg.captured, "error", msg.err)
		m.gracefulQuitInFlight = false
		m.reportError("Quit paused before shutdown completed", msg.err, "")
		m.status = "Quit paused: " + strings.TrimSpace(msg.err.Error()) + ". Press q to retry."
		return m, nil
	}
	if m.unsub != nil {
		m.unsub()
	}
	if msg.captured > 0 {
		m.status = fmt.Sprintf("Saved %d in-flight %s", msg.captured, pluralize("turn", msg.captured))
	}
	return m, tea.Quit
}
