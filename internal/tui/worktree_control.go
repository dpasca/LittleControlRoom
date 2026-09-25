package tui

import (
	"fmt"

	"lcroom/internal/control"
	"lcroom/internal/model"

	tea "github.com/charmbracelet/bubbletea"
)

func (m Model) executeWorktreeRemoveControl(input control.WorktreeRemoveInput) controlInvocationOutcome {
	if m.pendingGitSummary(input.WorktreePath) != "" {
		err := fmt.Errorf("a worktree action is already running for %s", input.WorktreePath)
		m.status = err.Error()
		return controlInvocationOutcome{model: m, err: err}
	}
	m.setPendingGitSummary(input.WorktreePath, worktreeRemovePendingSummary)
	m.status = "Removing linked worktree..."
	return controlInvocationOutcome{model: m, cmd: m.removeWorktreeControlCmd(input)}
}

func (m Model) removeWorktreeControlCmd(input control.WorktreeRemoveInput) tea.Cmd {
	return func() tea.Msg {
		result := &control.WorktreeRemoveResult{WorktreePath: input.WorktreePath}
		msg := worktreeActionMsg{
			projectPath:            input.WorktreePath,
			controlRemovalResult:   result,
			clearPendingGitSummary: true,
			status:                 "Worktree removal failed",
		}
		if m.svc == nil || m.svc.Store() == nil {
			msg.err = fmt.Errorf("service unavailable")
			return msg
		}
		ctx, cancel := m.actionContext(tuiWorktreeRemoveTimeout)
		defer cancel()
		// Read authoritative records off the UI thread. Missing/forgotten linked
		// checkouts still need their Git and LCR state cleaned up.
		project, err := m.svc.Store().GetTrackedProjectSummary(ctx, input.WorktreePath)
		if err != nil {
			msg.err = fmt.Errorf("load tracked worktree: %w", err)
			return msg
		}
		if project.WorktreeKind != model.WorktreeKindLinked || project.WorktreeRootPath == "" || normalizeProjectPath(project.WorktreeRootPath) == normalizeProjectPath(input.WorktreePath) {
			msg.err = fmt.Errorf("only tracked linked worktrees can be removed")
			return msg
		}
		result.RootPath = project.WorktreeRootPath
		msg.selectPath = project.WorktreeRootPath
		result.IdleSessionClosed, _ = closeIdleEmbeddedSessionForWorktree(m.codexManager, input.WorktreePath, true)
		msg.closedEmbeddedSession = result.IdleSessionClosed
		err = m.svc.RemoveWorktree(ctx, input.WorktreePath, false)
		msg.err = timeoutActionError(err, tuiWorktreeRemoveTimeout, "removing the worktree")
		if msg.err == nil {
			result.WorktreeRemoved = true
			msg.removedProjectPath = input.WorktreePath
			msg.status = "Worktree removed; branch and conversation history preserved"
			msg.refresh = invalidateProjectStructure(project.WorktreeRootPath)
		}
		return msg
	}
}
