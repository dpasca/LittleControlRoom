package tui

import (
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"lcroom/internal/codexapp"
	"strings"
	"time"
)

var codexFastModeStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF5555")).Bold(true)

type codexFastModeMsg struct{ action codexActionMsg }

func (m Model) setVisibleCodexFastMode(snapshot codexapp.Snapshot, mode string) (tea.Model, tea.Cmd) {
	if embeddedProvider(snapshot) != codexapp.ProviderCodex {
		m.status = "/fast is available only for Codex engineers"
		return m, nil
	}
	if m.codexFastModeBusy {
		m.status = "Codex fast mode update already in progress"
		return m, nil
	}
	if snapshot.BusyExternal {
		m.status = "This Codex turn is owned by another process; its fast mode cannot be changed here"
		return m, nil
	}
	m.codexFastModeBusy = true
	m.status = "Updating shared Codex fast mode..."
	projectPath := m.codexVisibleProject
	command := m.codexSessionCmd(projectPath, nil, func(session codexapp.Session) tea.Msg {
		if current := session.Snapshot(); snapshot.ThreadID != "" && current.ThreadID != snapshot.ThreadID {
			return codexActionMsg{projectPath: projectPath, err: codexapp.ErrSessionChanged}
		}
		controller, ok := session.(interface{ FastMode(string) error })
		if !ok {
			return codexActionMsg{projectPath: projectPath, err: fmt.Errorf("this Codex session does not support fast mode; reconnect it")}
		}
		if err := controller.FastMode(mode); err != nil {
			return codexActionMsg{projectPath: projectPath, err: err, refreshView: true}
		}
		return codexActionMsg{projectPath: projectPath, status: "Shared Codex fast mode updated; /fast status shows details", refreshView: true}
	})
	if command == nil {
		m.codexFastModeBusy = false
		m.status = "Embedded Codex session unavailable"
		return m, nil
	}
	return m, func() tea.Msg { return codexFastModeMsg{action: command().(codexActionMsg)} }
}

func codexFastModeLabel(snapshot codexapp.Snapshot) (string, bool) {
	return codexFastModeLabelAt(snapshot, time.Now())
}

func codexFastModeLabelAt(snapshot codexapp.Snapshot, now time.Time) (string, bool) {
	label, warning := codexFastModeBaseLabel(snapshot)
	if label == "" || snapshot.FastMode.ExpiresAt.IsZero() {
		return label, warning
	}
	if snapshot.FastMode.Error != "" {
		if snapshot.FastMode.Expired {
			return "FAST expired · OFF failed", true
		}
		return label, warning
	}
	if remaining := snapshot.FastMode.ExpiresAt.Sub(now); remaining > 0 && !snapshot.FastMode.Expired {
		seconds := int64((remaining + time.Second - 1) / time.Second)
		timer := fmt.Sprintf("%d:%02d:%02d", seconds/3600, (seconds/60)%60, seconds%60)
		return strings.Replace(label, "FAST", "FAST "+timer, 1), warning
	}
	return "FAST expired · switching off", true
}

func codexFastModeBaseLabel(snapshot codexapp.Snapshot) (string, bool) {
	if snapshot.Provider != codexapp.ProviderCodex {
		return "", false
	}
	activeFast := codexapp.IsFastServiceTier(snapshot.ServiceTier)
	state := snapshot.FastMode
	if state.Error != "" {
		if activeFast {
			return "FAST · status uncertain", true
		}
		return "FAST status unknown", true
	}
	if state.Pending {
		if activeFast {
			return "FAST · change pending", true
		}
		if codexapp.IsFastServiceTier(state.Tier) {
			return "FAST pending", true
		}
		return "Speed syncing", true
	}
	if snapshot.Busy && activeFast && state.Managed && !codexapp.IsFastServiceTier(state.Tier) {
		return "FAST finishing · next OFF", true
	}
	if state.Managed && codexapp.IsFastServiceTier(state.Tier) {
		if snapshot.Busy && !activeFast {
			return "FAST next · current OFF", true
		}
		return "FAST · higher usage", true
	}
	if activeFast {
		return "FAST · higher usage", true
	}
	if state.Managed && state.Tier == "flex" {
		return "Flex · fast OFF", false
	}
	if state.Managed {
		return "Standard · fast OFF", false
	}
	return "", false
}
