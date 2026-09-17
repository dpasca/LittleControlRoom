package tui

import (
	"context"
	"errors"
	"fmt"
	"lcroom/internal/service"

	tea "github.com/charmbracelet/bubbletea"
)

type staleRecoveryActionMsg struct {
	dialog   *staleWorktreeCleanupDialogState
	index    int
	recovery *service.WorktreeRecovery
	message  string
	err      error
}

func (m Model) staleRecoveryActionCmd(action string) tea.Cmd {
	d := m.staleWorktreeCleanup
	if d == nil || d.RecoveryBusy || len(d.Results) == 0 || m.svc == nil {
		return nil
	}
	index := d.Selected
	result := d.Results[index]
	r := result.Finalize.Recovery
	if r == nil || r.Phase == "purged" {
		return nil
	}
	d.RecoveryBusy = true
	d.RecoveryMessage = ""
	svc := m.svc
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), staleWorktreeCleanupTimeout)
		defer cancel()
		msg := staleRecoveryActionMsg{dialog: d, index: index}
		switch action {
		case "review":
			msg.recovery, msg.err = svc.ReviewWorktreeRecovery(ctx, result.Candidate.ProjectPath)
			// Review must also let the user inspect incomplete/corrupt evidence.
			msg.err = errors.Join(msg.err, openExternalPath(r.Location))
			msg.message = "Recovery opened. manifest.json records original paths, repairs, objects, and verification."
		case "restore":
			destination := result.Candidate.ProjectPath + ".restored"
			msg.err = svc.RestoreWorktreeRecovery(ctx, result.Candidate.ProjectPath, destination)
			msg.message = "Restored independent workspace: " + destination + "/tree. The original recovery is retained."
		case "purge":
			msg.err = svc.PurgeWorktreeRecovery(ctx, result.Candidate.ProjectPath, "Permanently delete "+r.Location)
			msg.message = "Recovery data permanently deleted; operation receipt retained."
		}
		if msg.err == nil {
			msg.recovery, msg.err = svc.ReviewWorktreeRecovery(ctx, result.Candidate.ProjectPath)
		}
		return msg
	}
}

func (m Model) applyStaleRecoveryAction(msg staleRecoveryActionMsg) (tea.Model, tea.Cmd) {
	d := m.staleWorktreeCleanup
	if d == nil || d != msg.dialog {
		return m, nil
	}
	d.RecoveryBusy = false
	if msg.recovery != nil && msg.index < len(d.Results) {
		d.Results[msg.index].Finalize.Recovery = msg.recovery
	}
	if msg.err != nil {
		d.RecoveryMessage = fmt.Sprintf("Recovery action blocked: %v", msg.err)
	} else {
		d.RecoveryMessage = msg.message
	}
	return m, nil
}
