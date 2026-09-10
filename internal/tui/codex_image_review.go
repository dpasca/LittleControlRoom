package tui

import (
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
	"lcroom/internal/codexapp"
	"lcroom/internal/imagereview"
)

func (m Model) setVisibleImageReview(snapshot codexapp.Snapshot, mode string) (tea.Model, tea.Cmd) {
	if embeddedProvider(snapshot) != codexapp.ProviderCodex {
		m.status = "External image recovery is available for embedded Codex sessions"
		return m, nil
	}
	state := "off"
	if snapshot.ImageReviewEnabled {
		state = "on"
	}
	if mode == "" || mode == state {
		m.status = fmt.Sprintf("External image review: %s for this session. /image-review on enables %s / %s via the OpenAI API (separate API billing); /image-review off disables it. New sessions default to off.", state, imagereview.Model, imagereview.Reasoning)
		return m, nil
	}
	if snapshot.Busy || snapshot.BusyExternal || snapshot.ThreadID == "" || m.codexPendingOpen != nil {
		m.status = "Wait for this session to become idle before changing image review"
		return m, nil
	}
	enabled := mode == "on"
	m.status = "Reconnecting this Codex session with external image review " + mode + "..."
	m.beginCodexPendingOpen(m.codexVisibleProject, codexapp.ProviderCodex)
	return m, m.reconnectVisibleCodexSessionWithImageReviewCmd(&enabled)
}

func imageReviewReconnectStatus(req codexapp.LaunchRequest, snapshot codexapp.Snapshot, changed *bool) string {
	status := embeddedSessionReconnectStatus(req, snapshot)
	if req.ImageReviewEnabled {
		return status + " External image recovery is ON for this session only (" + imagereview.Model + "; separate API billing). /image-review off disables it."
	}
	if changed != nil {
		return status + " External image recovery is OFF."
	}
	return status
}
