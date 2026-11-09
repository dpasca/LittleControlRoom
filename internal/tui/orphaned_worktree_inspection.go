package tui

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"lcroom/internal/codexapp"
	"lcroom/internal/model"
	"lcroom/internal/service"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type orphanedWorktreeInspectionState struct {
	ProjectPath         string
	RootPath            string
	ProjectName         string
	BranchName          string
	Inspection          service.OrphanedWorktreeInspection
	HaveInspection      bool
	Busy                bool
	BusyMessage         string
	ErrorMessage        string
	ForceRemove         bool
	HasActiveSession    bool
	HasIdleSession      bool
	IdleSessionProvider codexapp.Provider
	RuntimeRunning      bool
	Selected            int
	Scroll              int
}

type orphanedWorktreeInspectionMsg struct {
	ProjectPath string
	Inspection  service.OrphanedWorktreeInspection
	Err         error
}

type orphanedWorktreeResolutionMsg struct {
	ProjectPath           string
	RootPath              string
	TaskID                string
	Status                string
	ClosedEmbeddedSession bool
	Err                   error
}

func orphanedWorktreeInspectionNeedsForce(state *orphanedWorktreeInspectionState) bool {
	if state == nil || !orphanedWorktreeInspectionCanDelete(state) {
		return false
	}
	return state.Inspection.Dirty || state.HasIdleSession
}

func orphanedWorktreeInspectionOptionCount(state *orphanedWorktreeInspectionState) int {
	if orphanedWorktreeInspectionNeedsForce(state) {
		return 1
	}
	return 0
}

func orphanedWorktreeInspectionActionIndex(state *orphanedWorktreeInspectionState) int {
	return orphanedWorktreeInspectionOptionCount(state)
}

func orphanedWorktreeInspectionKeepIndex(state *orphanedWorktreeInspectionState) int {
	return orphanedWorktreeInspectionOptionCount(state) + 1
}

func orphanedWorktreeInspectionFocusCount(state *orphanedWorktreeInspectionState) int {
	return orphanedWorktreeInspectionOptionCount(state) + 2
}

func orphanedWorktreeInspectionCanDelete(state *orphanedWorktreeInspectionState) bool {
	if state == nil || !state.HaveInspection {
		return false
	}
	switch state.Inspection.Resolution {
	case service.OrphanedWorktreeResolutionClearResidue,
		service.OrphanedWorktreeResolutionRemoveRegistered,
		service.OrphanedWorktreeResolutionDeleteArchivedTaskNow:
		return true
	default:
		return false
	}
}

func orphanedWorktreeInspectionReady(state *orphanedWorktreeInspectionState) bool {
	if state == nil || state.HasActiveSession || state.RuntimeRunning {
		return false
	}
	return !orphanedWorktreeInspectionNeedsForce(state) || state.ForceRemove
}

func (m *Model) openOrphanedWorktreeInspection(project model.ProjectSummary, rootPath string) tea.Cmd {
	state := &orphanedWorktreeInspectionState{
		ProjectPath: project.Path,
		RootPath:    rootPath,
		ProjectName: project.Name,
		BranchName:  projectWorktreeLabel(project),
		Busy:        true,
		BusyMessage: "Checking Git registration, retained task ownership, and the remaining files. This inspection is read-only.",
	}
	m.orphanedWorktreeInspection = state
	m.status = "Inspecting workspace ownership and files..."
	return m.inspectOrphanedWorktreeCmd(project.Path)
}

func (m Model) inspectOrphanedWorktreeCmd(projectPath string) tea.Cmd {
	if m.svc == nil {
		return func() tea.Msg {
			return orphanedWorktreeInspectionMsg{ProjectPath: projectPath, Err: fmt.Errorf("service unavailable")}
		}
	}
	return func() tea.Msg {
		ctx, cancel := m.actionContext(tuiWorktreeInspectTimeout)
		defer cancel()
		inspection, err := m.svc.InspectOrphanedWorktree(ctx, projectPath)
		err = timeoutActionError(err, tuiWorktreeInspectTimeout, "inspecting the orphaned worktree")
		return orphanedWorktreeInspectionMsg{
			ProjectPath: projectPath,
			Inspection:  inspection,
			Err:         err,
		}
	}
}

func (m Model) applyOrphanedWorktreeInspection(msg orphanedWorktreeInspectionMsg) (tea.Model, tea.Cmd) {
	state := m.orphanedWorktreeInspection
	if state == nil || normalizeProjectPath(state.ProjectPath) != normalizeProjectPath(msg.ProjectPath) {
		return m, nil
	}
	state.Busy = false
	state.Scroll = 0
	state.BusyMessage = ""
	state.ErrorMessage = ""
	state.ForceRemove = false
	state.HasActiveSession = false
	state.HasIdleSession = false
	state.IdleSessionProvider = ""
	state.RuntimeRunning = false
	if msg.Err != nil {
		state.HaveInspection = false
		state.ErrorMessage = msg.Err.Error()
		state.Selected = orphanedWorktreeInspectionKeepIndex(state)
		m.reportError("Worktree inspection failed", msg.Err, msg.ProjectPath)
		return m, nil
	}

	state.HaveInspection = true
	state.Inspection = msg.Inspection
	if rootPath := strings.TrimSpace(msg.Inspection.RootPath); rootPath != "" {
		state.RootPath = rootPath
	}
	if branch := strings.TrimSpace(msg.Inspection.BranchName); branch != "" {
		state.BranchName = branch
	}
	if snapshot, ok := m.liveCodexSnapshot(state.ProjectPath); ok {
		state.IdleSessionProvider = embeddedProvider(snapshot)
		if embeddedSessionBlocksProviderSwitch(snapshot) {
			state.HasActiveSession = true
		} else if state.IdleSessionProvider != "" {
			state.HasIdleSession = true
		}
	}
	state.RuntimeRunning = m.projectRuntimeSnapshot(state.ProjectPath).Running
	state.Selected = orphanedWorktreeInspectionKeepIndex(state)
	m.err = nil
	m.status = orphanedWorktreeInspectionStatus(state)
	return m, nil
}

func orphanedWorktreeInspectionStatus(state *orphanedWorktreeInspectionState) string {
	if state == nil || !state.HaveInspection {
		return "Orphaned worktree inspection needs attention"
	}
	switch state.Inspection.Resolution {
	case service.OrphanedWorktreeResolutionDeleteArchivedTaskNow:
		return "This workspace belongs to an archived task in Trash"
	case service.OrphanedWorktreeResolutionRemoveRegistered:
		return "This path is still a registered Git worktree"
	case service.OrphanedWorktreeResolutionClearResidue:
		return "This worktree residue passed safety verification"
	default:
		if state.Inspection.AgentTask.ID != "" {
			return "Workspace inspection complete; owned by an existing task"
		}
		return "This worktree needs manual review; no files were changed"
	}
}

func (m Model) updateOrphanedWorktreeInspectionMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	state := m.orphanedWorktreeInspection
	if state == nil {
		return m, nil
	}
	if state.Busy {
		return m, nil
	}
	focusCount := orphanedWorktreeInspectionFocusCount(state)
	switch msg.String() {
	case "pgup":
		state.Scroll = max(0, state.Scroll-8)
		return m, nil
	case "pgdown":
		lineCount := strings.Count(m.renderOrphanedWorktreeInspectionContent(max(28, min(84, m.width-22)-4)), "\n") + 1
		state.Scroll = min(state.Scroll+8, max(0, lineCount-max(10, m.height-4)))
		return m, nil
	case "esc":
		m.orphanedWorktreeInspection = nil
		m.status = orphanedWorktreeInspectionKeptStatus(state)
		return m, nil
	case "left", "h", "up", "k", "shift+tab":
		state.Selected = ((state.Selected-1)%focusCount + focusCount) % focusCount
		return m, nil
	case "right", "l", "down", "j", "tab":
		state.Selected = (state.Selected + 1) % focusCount
		return m, nil
	case " ":
		if orphanedWorktreeInspectionNeedsForce(state) && state.Selected == 0 {
			state.ForceRemove = !state.ForceRemove
		}
		return m, nil
	case "enter":
		if orphanedWorktreeInspectionNeedsForce(state) && state.Selected == 0 {
			state.ForceRemove = !state.ForceRemove
			return m, nil
		}
		if state.Selected == orphanedWorktreeInspectionKeepIndex(state) {
			m.orphanedWorktreeInspection = nil
			m.status = orphanedWorktreeInspectionKeptStatus(state)
			return m, nil
		}
		if !orphanedWorktreeInspectionCanDelete(state) {
			state.Busy = true
			state.BusyMessage = "Re-checking Git registration, retained task ownership, and the remaining files. This inspection is read-only."
			state.ErrorMessage = ""
			m.status = "Inspecting orphaned worktree..."
			return m, m.inspectOrphanedWorktreeCmd(state.ProjectPath)
		}
		if state.HasActiveSession {
			state.ErrorMessage = "Finish or close the active embedded engineer session, then reopen this inspection to resolve the workspace."
			m.status = state.ErrorMessage
			return m, nil
		}
		if state.RuntimeRunning {
			state.ErrorMessage = "Stop the project runtime, then reopen this inspection to resolve the workspace."
			m.status = state.ErrorMessage
			return m, nil
		}
		if !orphanedWorktreeInspectionReady(state) {
			state.ErrorMessage = orphanedWorktreeInspectionForcePrompt(state)
			m.status = state.ErrorMessage
			return m, nil
		}
		state.Busy = true
		state.BusyMessage = orphanedWorktreeResolutionBusyMessage(state)
		state.ErrorMessage = ""
		m.setPendingGitSummary(state.ProjectPath, worktreeOrphanCleanupSummary)
		m.status = state.BusyMessage
		return m, m.resolveInspectedOrphanedWorktreeCmd(*state)
	}
	return m, nil
}

func orphanedWorktreeInspectionForcePrompt(state *orphanedWorktreeInspectionState) string {
	switch {
	case state != nil && state.Inspection.Dirty && state.HasIdleSession:
		return "Check the confirmation box to close the idle session and discard uncommitted changes."
	case state != nil && state.HasIdleSession:
		return "Check the confirmation box to close the idle embedded session."
	default:
		return "Check the confirmation box to discard uncommitted changes."
	}
}

func orphanedWorktreeResolutionBusyMessage(state *orphanedWorktreeInspectionState) string {
	if state == nil {
		return "Resolving inspected worktree..."
	}
	switch state.Inspection.Resolution {
	case service.OrphanedWorktreeResolutionDeleteArchivedTaskNow:
		return "Permanently deleting the archived task and its retained workspace..."
	case service.OrphanedWorktreeResolutionRemoveRegistered:
		return "Removing the registered checkout while preserving its branch..."
	default:
		return "Re-verifying and clearing the safe worktree residue..."
	}
}

func (m Model) resolveInspectedOrphanedWorktreeCmd(state orphanedWorktreeInspectionState) tea.Cmd {
	if m.svc == nil {
		return func() tea.Msg {
			return orphanedWorktreeResolutionMsg{ProjectPath: state.ProjectPath, RootPath: state.RootPath, Err: fmt.Errorf("service unavailable")}
		}
	}
	return func() tea.Msg {
		closedSession, err := closeIdleEmbeddedSessionForWorktree(m.codexManager, state.ProjectPath, state.HasIdleSession)
		if err != nil {
			return orphanedWorktreeResolutionMsg{
				ProjectPath:           state.ProjectPath,
				RootPath:              state.RootPath,
				ClosedEmbeddedSession: closedSession,
				Err:                   err,
			}
		}
		ctx, cancel := m.actionContext(tuiWorktreeRemoveTimeout)
		defer cancel()
		forceGitRemoval := state.Inspection.Dirty && state.ForceRemove
		status := "Verified orphaned worktree residue cleared"
		taskID := ""
		switch state.Inspection.Resolution {
		case service.OrphanedWorktreeResolutionDeleteArchivedTaskNow:
			taskID = state.Inspection.AgentTask.ID
			err = m.svc.DeleteArchivedAgentTaskNow(ctx, taskID, state.ProjectPath, forceGitRemoval)
			status = "Archived task and retained workspace permanently deleted"
		case service.OrphanedWorktreeResolutionRemoveRegistered:
			err = m.svc.RemoveWorktree(ctx, state.ProjectPath, forceGitRemoval)
			status = "Registered worktree removed; branch preserved"
		case service.OrphanedWorktreeResolutionClearResidue:
			err = m.svc.CleanupRetainedWorktree(ctx, state.ProjectPath)
		default:
			err = fmt.Errorf("the inspection did not provide an automatic resolution")
		}
		err = timeoutActionError(err, tuiWorktreeRemoveTimeout, "resolving the inspected orphaned worktree")
		return orphanedWorktreeResolutionMsg{
			ProjectPath:           state.ProjectPath,
			RootPath:              state.RootPath,
			TaskID:                taskID,
			Status:                status,
			ClosedEmbeddedSession: closedSession,
			Err:                   err,
		}
	}
}

func (m Model) applyOrphanedWorktreeResolution(msg orphanedWorktreeResolutionMsg) (tea.Model, tea.Cmd) {
	state := m.orphanedWorktreeInspection
	if state == nil || normalizeProjectPath(state.ProjectPath) != normalizeProjectPath(msg.ProjectPath) {
		return m, nil
	}
	if msg.ClosedEmbeddedSession {
		m.dropCodexSnapshot(msg.ProjectPath)
		state.HasIdleSession = false
		state.IdleSessionProvider = ""
	}
	m.clearPendingGitSummary(msg.ProjectPath)
	if msg.Err != nil {
		state.Busy = false
		state.BusyMessage = ""
		state.HaveInspection = false
		state.ForceRemove = false
		state.ErrorMessage = msg.Err.Error()
		state.Selected = orphanedWorktreeInspectionKeepIndex(state)
		m.reportError("Orphaned worktree resolution failed", msg.Err, msg.ProjectPath)
		return m, m.refreshProjectStatusPathsCmd(msg.ProjectPath, msg.RootPath)
	}

	m.orphanedWorktreeInspection = nil
	m.err = nil
	m.applyRemovedProjectLocally(msg.ProjectPath, msg.RootPath)
	m.status = strings.TrimSpace(msg.Status)
	var trashCmd tea.Cmd
	if m.agentTaskTrash != nil {
		m.agentTaskTrash.busy = true
		trashCmd = m.loadAgentTaskTrashCmd()
	}
	return m, batchCmds(trashCmd, m.requestProjectInvalidationCmd(invalidateProjectStructure(msg.RootPath)))
}

func (m Model) renderOrphanedWorktreeInspectionOverlay(body string, bodyW, bodyH int) string {
	state := m.orphanedWorktreeInspection
	if state == nil {
		return body
	}
	panelW := min(max(54, bodyW-22), 84)
	panelInnerW := max(28, panelW-4)
	content := m.renderOrphanedWorktreeInspectionContent(panelInnerW)
	content = workspaceInspectionViewport(content, max(10, bodyH-4), state.Scroll, 6+orphanedWorktreeInspectionOptionCount(state))
	panel := renderDialogPanel(panelW, panelInnerW, content)
	left := max(0, (bodyW-lipgloss.Width(panel))/2)
	top := max(0, (bodyH-lipgloss.Height(panel))/2)
	return overlayBlock(body, panel, bodyW, bodyH, left, top)
}

// Keep the action buttons visible while long paths and inspection evidence
// scroll independently on short terminals. This consumes rendered text only.
func workspaceInspectionViewport(content string, height, offset, footerLines int) string {
	lines := strings.Split(content, "\n")
	if len(lines) <= height {
		return content
	}
	header, footer := lines[:2], lines[len(lines)-footerLines:]
	details := lines[2 : len(lines)-footerLines]
	visible := max(1, height-len(header)-len(footer))
	offset = min(max(0, offset), max(0, len(details)-visible))
	out := append([]string{}, header...)
	out = append(out, details[offset:min(len(details), offset+visible)]...)
	out = append(out, footer...)
	return strings.Join(out, "\n")
}

func (m Model) renderOrphanedWorktreeInspectionContent(width int) string {
	state := m.orphanedWorktreeInspection
	if state == nil {
		return ""
	}
	title, summary := orphanedWorktreeInspectionCopy(state)
	subject := state.BranchName
	if strings.TrimSpace(subject) == "" {
		subject = state.ProjectName
	}
	lines := []string{
		renderDialogHeader(title, subject, "", width),
		"",
		renderWrappedDetailField("Path", detailMutedStyle, width, state.ProjectPath),
	}
	if strings.TrimSpace(state.RootPath) != "" {
		lines = append(lines, renderWrappedDetailField("Repository", detailMutedStyle, width, state.RootPath))
	}
	lines = append(lines, "")
	if summary != "" {
		lines = append(lines, renderWrappedDialogTextLines(detailWarningStyle, width, summary)...)
	}
	if state.Busy {
		lines = append(lines, "", detailValueStyle.Render("Inspection in progress"))
		lines = append(lines, renderWrappedDialogTextLines(detailMutedStyle, width, state.BusyMessage)...)
		lines = append(lines, "", disabledActionTextStyle.Render("["+todoDialogWaitingLabel(m.spinnerFrame)+"]"))
		return strings.Join(lines, "\n")
	}
	if state.HaveInspection {
		lines = append(lines, orphanedWorktreeInspectionDetailLines(m, state, width)...)
	}
	if strings.TrimSpace(state.ErrorMessage) != "" {
		lines = append(lines, "", detailDangerStyle.Render("Action needed"))
		lines = append(lines, renderWrappedDialogTextLines(detailDangerStyle, width, state.ErrorMessage)...)
	}
	if orphanedWorktreeInspectionNeedsForce(state) {
		lines = append(lines, "")
		lines = append(lines, orphanedWorktreeForceLines(state, width)...)
	}

	actionLabel := orphanedWorktreeInspectionActionLabel(state)
	actionButton := renderDialogButton(actionLabel, state.Selected == orphanedWorktreeInspectionActionIndex(state))
	if orphanedWorktreeInspectionCanDelete(state) && !orphanedWorktreeInspectionReady(state) {
		actionButton = disabledActionTextStyle.Render("[Action blocked]")
	}
	buttons := lipgloss.JoinHorizontal(
		lipgloss.Left,
		actionButton,
		" ",
		renderDialogButton(orphanedWorktreeInspectionKeepLabel(state), state.Selected == orphanedWorktreeInspectionKeepIndex(state)),
	)
	lines = append(lines, "", buttons)
	if orphanedWorktreeInspectionOptionCount(state) > 0 {
		lines = append(lines, renderDialogAction("Space", "toggle", pushActionKeyStyle, pushActionTextStyle))
	}
	lines = append(lines,
		renderDialogAction("PgUp/PgDn", "details", navigateActionKeyStyle, navigateActionTextStyle),
		renderDialogAction("Tab/↑↓", "switch", navigateActionKeyStyle, navigateActionTextStyle),
		renderDialogAction("Enter", "choose", commitActionKeyStyle, commitActionTextStyle),
		renderDialogAction("Esc", "keep and close", cancelActionKeyStyle, cancelActionTextStyle),
	)
	return strings.Join(lines, "\n")
}

func orphanedWorktreeInspectionCopy(state *orphanedWorktreeInspectionState) (string, string) {
	if state == nil || state.Busy {
		return "Inspecting workspace", "Little Control Room is determining what owns this folder before offering any destructive action."
	}
	if !state.HaveInspection {
		return "Inspection needs attention", "The folder was left untouched. You can retry the read-only inspection or keep it."
	}
	if state.Inspection.AgentTask.ID != "" && state.Inspection.Resolution == service.OrphanedWorktreeResolutionNone {
		return "Task workspace", "This folder belongs to an existing task. Inspect its checkout health and recorded handoff below."
	}
	switch state.Inspection.Resolution {
	case service.OrphanedWorktreeResolutionDeleteArchivedTaskNow:
		return "Retained task workspace", "This is not abandoned residue. It belongs to a task in Trash and is being retained intentionally."
	case service.OrphanedWorktreeResolutionRemoveRegistered:
		return "Registered Git worktree", "Git still owns this checkout. Little Control Room can remove the checkout while preserving its branch."
	case service.OrphanedWorktreeResolutionClearResidue:
		return "Verified worktree residue", "The remaining folder passed the strict cleanup inspection and can be cleared safely."
	default:
		return "Manual review required", "Little Control Room could not prove that automatic removal is safe, so the folder remains untouched."
	}
}

func orphanedWorktreeInspectionDetailLines(m Model, state *orphanedWorktreeInspectionState, width int) []string {
	inspection := state.Inspection
	lines := []string{"", detailSectionStyle.Render("Inspection result")}
	if !inspection.InspectedAt.IsZero() {
		lines = append(lines, detailField("Checked", inspection.InspectedAt.Local().Format(time.RFC3339)))
	}
	registration := inspection.Registration
	if registration == "" {
		registration = "Unknown"
	}
	lines = append(lines, detailField("Git registration", registration))
	if inspection.RegistrationError != "" {
		lines = append(lines, renderWrappedDetailField("Registration inspection failed", detailWarningStyle, width, inspection.RegistrationError))
	}
	if inspection.HaveCurrentStatus {
		status := "clean"
		if inspection.Dirty {
			status = "local changes"
		}
		lines = append(lines, renderWrappedDetailField("Current checkout", detailValueStyle, width, inspection.BranchName+" · "+status))
	}
	lines = append(lines, detailField("Retained", fmt.Sprintf("%d bytes (logical size)", inspection.RetainedBytes)))
	if inspection.SizeError != "" {
		lines = append(lines, "Size incomplete: "+inspection.SizeError)
	}
	for _, child := range inspection.NestedWorktrees {
		path := child.Path
		if rel, err := filepath.Rel(inspection.ProjectPath, path); err == nil {
			path = rel
		}
		commit := child.Commit
		if len(commit) > 12 {
			commit = commit[:12]
		}
		lines = append(lines, renderWrappedDialogTextLines(detailMutedStyle, width, path+" @ "+commit+" "+child.Branch)...)
	}
	if reason := strings.TrimSpace(inspection.Reason); reason != "" {
		lines = append(lines, renderWrappedDialogTextLines(detailValueStyle, width, reason)...)
	}
	if task := inspection.AgentTask; task.ID != "" {
		lines = append(lines,
			renderWrappedDetailField("Task", detailValueStyle, width, agentTaskActionTitle(task)),
			detailField("Task status", string(task.Status)),
		)
		if issue := agentTaskWorkspaceIssue(task); issue != "" {
			lines = append(lines, detailField("Checkout health", issue))
		}
		if task.CleanupError != "" {
			lines = append(lines, renderWrappedDetailField("Cleanup failed", detailWarningStyle, width, task.CleanupError))
		}
		if task.Repository.Changes != "" {
			lines = append(lines, renderWrappedDetailFieldLimited("Recorded handoff changes", detailMutedStyle, width, task.Repository.Changes, 3))
			lines = append(lines, renderWrappedDialogTextLines(detailMutedStyle, width, "Historical host capture; this is not a verification of the files currently on disk.")...)
		}
	}
	if inspection.Resolution == service.OrphanedWorktreeResolutionDeleteArchivedTaskNow {
		task := inspection.AgentTask
		if !task.ArchivedAt.IsZero() {
			lines = append(lines, detailField("Archived", task.ArchivedAt.Local().Format(time.RFC3339)))
		}
		if retention := orphanedWorktreeRetentionText(m.currentTime(), task.ExpiresAt); retention != "" {
			lines = append(lines, renderWrappedDialogTextLines(detailMutedStyle, width, retention)...)
		}
		lines = append(lines, renderWrappedDialogTextLines(
			detailWarningStyle,
			width,
			"Delete Now permanently deletes the task record and this retained checkout. The Git branch remains available in the repository.",
		)...)
	}
	if inspection.Resolution == service.OrphanedWorktreeResolutionRemoveRegistered {
		if header, body, style := worktreeRemoveSafetyCopy(inspection.MergeStatus, inspection.TargetBranch, inspection.Dirty); header != "" || body != "" {
			lines = append(lines, "")
			if header != "" {
				lines = append(lines, style.Render(header))
			}
			if body != "" {
				lines = append(lines, renderWrappedDialogTextLines(style, width, body)...)
			}
		}
		lines = append(lines, renderWrappedDialogTextLines(detailMutedStyle, width, "Removing the worktree deletes only the checkout. Its branch ref stays in the repository.")...)
	}
	if inspection.Resolution == service.OrphanedWorktreeResolutionClearResidue {
		lines = append(lines, renderWrappedDialogTextLines(detailMutedStyle, width, "Cleanup re-runs the strict verification immediately before deleting anything. A changed or unverified entry stops the action.")...)
	}
	if statusError := strings.TrimSpace(inspection.StatusError); statusError != "" {
		lines = append(lines, "", detailWarningStyle.Render("Git status detail unavailable"))
		lines = append(lines, renderWrappedDialogTextLines(detailMutedStyle, width, statusError)...)
		if orphanedWorktreeInspectionCanDelete(state) {
			lines = append(lines, renderWrappedDialogTextLines(detailMutedStyle, width, "The removal action will re-check Git and stop on uncertainty.")...)
		}
	}
	if state.HasActiveSession {
		lines = append(lines, "", detailDangerStyle.Render("Active embedded session"))
		lines = append(lines, renderWrappedDialogTextLines(detailDangerStyle, width, "Finish or close the running engineer session before removing this workspace.")...)
	}
	if state.RuntimeRunning {
		lines = append(lines, "", detailDangerStyle.Render("Project runtime is running"))
		lines = append(lines, renderWrappedDialogTextLines(detailDangerStyle, width, "Stop the runtime before removing this workspace.")...)
	}
	return lines
}

func orphanedWorktreeRetentionText(now, expiresAt time.Time) string {
	if expiresAt.IsZero() {
		return "Trash retention is active; the workspace will be deleted by the normal cleanup cycle."
	}
	if !now.IsZero() && !expiresAt.After(now) {
		return "Its Trash retention has expired; the next cleanup cycle can delete it automatically."
	}
	return "It is scheduled for automatic deletion on " + expiresAt.Local().Format("Jan 2 at 15:04") + "."
}

func orphanedWorktreeForceLines(state *orphanedWorktreeInspectionState, width int) []string {
	title := "Uncommitted changes"
	copy := "This checkout has uncommitted changes. Confirming force removal will discard them."
	label := "Force remove (discard uncommitted changes)"
	if state.HasIdleSession {
		providerLabel := state.IdleSessionProvider.Label()
		title = "Open embedded session"
		copy = "An idle " + providerLabel + " session is still attached. Confirming force removal closes it before deleting the checkout."
		label = "Force remove (close idle " + providerLabel + " session)"
		if state.Inspection.Dirty {
			title = "Open session and uncommitted changes"
			copy += " It also discards uncommitted changes."
			label = "Force remove (close session and discard changes)"
		}
	}
	lines := []string{detailWarningStyle.Render(title)}
	lines = append(lines, renderWrappedDialogTextLines(detailWarningStyle, width, copy)...)
	prefix := "[ ] "
	style := detailMutedStyle
	if state.ForceRemove {
		prefix = "[x] "
		style = detailWarningStyle
	}
	line := truncateText(prefix+label, width)
	if state.Selected == 0 {
		lines = append(lines, "", dialogButtonSelectedStyle.UnsetPadding().Width(width).Render(line))
	} else {
		lines = append(lines, "", style.Render(line))
	}
	return lines
}

func orphanedWorktreeInspectionActionLabel(state *orphanedWorktreeInspectionState) string {
	if state == nil || !state.HaveInspection || state.Inspection.Resolution == service.OrphanedWorktreeResolutionNone {
		return "Inspect Again"
	}
	switch state.Inspection.Resolution {
	case service.OrphanedWorktreeResolutionDeleteArchivedTaskNow:
		return "Delete Now"
	case service.OrphanedWorktreeResolutionRemoveRegistered:
		return "Remove Worktree"
	case service.OrphanedWorktreeResolutionClearResidue:
		return "Clear Residue"
	default:
		return "Inspect Again"
	}
}

func orphanedWorktreeInspectionKeepLabel(state *orphanedWorktreeInspectionState) string {
	if state != nil && state.HaveInspection && state.Inspection.Resolution == service.OrphanedWorktreeResolutionDeleteArchivedTaskNow {
		return "Keep Until Auto-Delete"
	}
	return "Keep"
}

func orphanedWorktreeInspectionKeptStatus(state *orphanedWorktreeInspectionState) string {
	if state != nil && state.HaveInspection && state.Inspection.Resolution == service.OrphanedWorktreeResolutionDeleteArchivedTaskNow {
		return "Retained task workspace kept until automatic Trash cleanup"
	}
	return "Worktree kept; inspection closed"
}
