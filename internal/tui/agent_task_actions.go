package tui

import (
	"fmt"
	"strings"

	"lcroom/internal/model"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const (
	agentTaskActionFocusTrash = iota
	agentTaskActionFocusCapture
	agentTaskActionFocusRevoke
	agentTaskActionFocusKeep
)

type agentTaskActionConfirmState struct {
	TaskID      string
	ProjectPath string
	TaskTitle   string
	Selected    int
	Submitting  bool
	// Capture is offered only while a changes_requested review of the current
	// revision leaves the checkout awaiting an authorized correction boundary.
	Capture         bool
	CaptureRevision int64
	// Revoke is offered while an unspent correction grant could still reopen
	// this worker without asking the operator again.
	Revoke          bool
	RevokeRemaining int
}

func agentTaskOffersSupervisionRevoke(task model.AgentTask) bool {
	return task.Workflow.CorrectionsRemaining() > 0
}

// agentTaskOffersCorrectionCapture reads cached task state only.
func agentTaskOffersCorrectionCapture(task model.AgentTask) bool {
	if !task.Repository.Write || task.Repository.State == "held" || !task.Workflow.Enabled {
		return false
	}
	review := task.Workflow.Review
	return review != nil && review.Decision == "changes_requested" && review.Revision == task.Workflow.RunID
}

func (confirm agentTaskActionConfirmState) focusOrder() []int {
	order := []int{agentTaskActionFocusTrash}
	if confirm.Capture {
		order = append(order, agentTaskActionFocusCapture)
	}
	if confirm.Revoke {
		order = append(order, agentTaskActionFocusRevoke)
	}
	return append(order, agentTaskActionFocusKeep)
}

func (m Model) selectedAgentTask() (model.AgentTask, model.ProjectSummary, bool) {
	project, ok := m.selectedProject()
	if !ok {
		return model.AgentTask{}, model.ProjectSummary{}, false
	}
	if model.NormalizeProjectKind(project.Kind) != model.ProjectKindAgentTask {
		return model.AgentTask{}, model.ProjectSummary{}, false
	}
	task, ok := m.agentTaskForProjectPath(project.Path)
	if !ok {
		return model.AgentTask{}, model.ProjectSummary{}, false
	}
	return task, project, true
}

func (m *Model) openAgentTaskActionConfirmForSelection() tea.Cmd {
	task, project, ok := m.selectedAgentTask()
	if !ok {
		if _, ok := m.selectedProject(); ok {
			m.status = "Trash is available for agent tasks"
		} else {
			m.status = "No project selected"
		}
		return nil
	}
	if snapshot, ok := m.liveAgentTaskSnapshot(task); ok && embeddedSessionBlocksProviderSwitch(snapshot) {
		m.showSessionBlockedAttentionDialog(
			project,
			"Trash blocked",
			"Wait for the embedded engineer session before trashing this agent task.",
			"trash this task",
			embeddedProvider(snapshot),
		)
		return nil
	}
	m.agentTaskAction = &agentTaskActionConfirmState{
		TaskID:          task.ID,
		ProjectPath:     task.WorkspacePath,
		TaskTitle:       agentTaskActionTitle(task),
		Selected:        agentTaskActionFocusKeep,
		Capture:         agentTaskOffersCorrectionCapture(task),
		CaptureRevision: task.Workflow.RunID,
		Revoke:          agentTaskOffersSupervisionRevoke(task),
		RevokeRemaining: task.Workflow.CorrectionsRemaining(),
	}
	m.status = "Agent task actions open"
	return nil
}

func (m *Model) closeAgentTaskActionConfirm(status string) {
	m.agentTaskAction = nil
	if status != "" {
		m.status = status
	}
}

func (m *Model) cycleAgentTaskActionSelection(delta int) {
	confirm := m.agentTaskAction
	if confirm == nil || delta == 0 {
		return
	}
	order := confirm.focusOrder()
	index := 0
	for i, focus := range order {
		if focus == confirm.Selected {
			index = i
		}
	}
	index = (index + delta%len(order) + len(order)) % len(order)
	confirm.Selected = order[index]
}

func (m Model) updateAgentTaskActionConfirmMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	confirm := m.agentTaskAction
	if confirm == nil {
		return m, nil
	}
	if confirm.Submitting {
		if msg.String() == "esc" {
			m.status = "Agent task action already in progress"
		}
		return m, nil
	}
	switch msg.String() {
	case "esc":
		m.closeAgentTaskActionConfirm("Agent task actions closed")
		return m, nil
	case "left", "h":
		m.cycleAgentTaskActionSelection(-1)
		return m, nil
	case "right", "l", "tab", "shift+tab":
		m.cycleAgentTaskActionSelection(1)
		return m, nil
	case "enter":
		if confirm.Selected == agentTaskActionFocusKeep {
			m.closeAgentTaskActionConfirm("Agent task actions closed")
			return m, nil
		}
		if confirm.Selected == agentTaskActionFocusCapture {
			if !confirm.Capture {
				return m, nil
			}
			confirm.Submitting = true
			m.status = "Capturing correction baseline..."
			return m, m.captureCorrectionBaselineCmd(confirm.TaskID, confirm.ProjectPath)
		}
		if confirm.Selected == agentTaskActionFocusRevoke {
			if !confirm.Revoke {
				return m, nil
			}
			confirm.Submitting = true
			m.status = "Revoking the correction grant..."
			return m, m.revokeSupervisionCmd(confirm.TaskID, confirm.ProjectPath)
		}
		task := model.AgentTask{ID: confirm.TaskID, Title: confirm.TaskTitle, WorkspacePath: confirm.ProjectPath}
		if snapshot, ok := m.liveAgentTaskSnapshot(task); ok && embeddedSessionBlocksProviderSwitch(snapshot) {
			project := model.ProjectSummary{Name: confirm.TaskTitle, Path: confirm.ProjectPath, Kind: model.ProjectKindAgentTask}
			m.showSessionBlockedAttentionDialog(
				project,
				"Trash blocked",
				"Wait for the embedded engineer session before trashing this agent task.",
				"trash this task",
				embeddedProvider(snapshot),
			)
			return m, nil
		}
		taskID := confirm.TaskID
		projectPath := confirm.ProjectPath
		selectPath := m.nextProjectSelectionPathAfter(projectPath)
		m.status = "Moving agent task to Trash..."
		closeCmd, err := m.closeEmbeddedSessionForProject(projectPath)
		if err != nil {
			m.reportError("Agent task action failed", err, projectPath)
			return m, nil
		}
		m.agentTaskAction = nil
		return m, batchCmds(closeCmd, m.archiveAgentTaskCmd(taskID, projectPath, selectPath))
	}
	return m, nil
}

func (m Model) archiveAgentTaskCmd(taskID, projectPath, selectPath string) tea.Cmd {
	if m.svc == nil {
		return func() tea.Msg {
			return agentTaskActionMsg{projectPath: projectPath, selectPath: selectPath, err: fmt.Errorf("service unavailable")}
		}
	}
	return func() tea.Msg {
		ctx, cancel := m.actionContext(tuiQuickActionTimeout)
		defer cancel()
		task, err := m.svc.ArchiveAgentTask(ctx, taskID)
		err = timeoutActionError(err, tuiQuickActionTimeout, "moving the agent task to Trash")
		return agentTaskActionMsg{
			task:        task,
			projectPath: projectPath,
			selectPath:  selectPath,
			status:      "Agent task moved to Trash",
			err:         err,
		}
	}
}

// revokeSupervisionCmd ends automatic corrections without stopping the worker,
// deleting anything or touching the checkout.
func (m Model) revokeSupervisionCmd(taskID, projectPath string) tea.Cmd {
	svc := m.svc
	return func() tea.Msg {
		if svc == nil || svc.Store() == nil {
			return agentTaskActionMsg{projectPath: projectPath, err: fmt.Errorf("service unavailable")}
		}
		ctx, cancel := m.actionContext(tuiQuickActionTimeout)
		defer cancel()
		task, err := svc.Store().RevokeAgentTaskSupervision(ctx, taskID)
		err = timeoutActionError(err, tuiQuickActionTimeout, "revoking the correction grant")
		return agentTaskActionMsg{
			task:        task,
			projectPath: projectPath,
			selectPath:  projectPath,
			status:      "Correction grant revoked; further corrections ask for confirmation",
			err:         err,
		}
	}
}

// captureCorrectionBaselineCmd runs the checkout inspection off the UI thread.
func (m Model) captureCorrectionBaselineCmd(taskID, projectPath string) tea.Cmd {
	svc := m.svc
	return func() tea.Msg {
		if svc == nil {
			return agentTaskActionMsg{projectPath: projectPath, err: fmt.Errorf("service unavailable")}
		}
		ctx, cancel := m.actionContext(tuiGitActionTimeout)
		defer cancel()
		task, err := svc.CaptureAgentTaskCorrectionBaseline(ctx, taskID)
		err = timeoutActionError(err, tuiGitActionTimeout, "capturing the correction baseline")
		return agentTaskActionMsg{
			task:        task,
			projectPath: projectPath,
			selectPath:  projectPath,
			status:      "Correction baseline captured; a correction run may continue on these edits",
			err:         err,
		}
	}
}

func (m Model) renderAgentTaskActionOverlay(body string, bodyW, bodyH int) string {
	confirm := m.agentTaskAction
	if confirm == nil {
		return body
	}
	panelW := min(max(50, bodyW-24), 76)
	panelInnerW := max(28, panelW-4)
	messageLines := []string{
		detailValueStyle.Render("Move this agent task to Trash and hide it from the dashboard."),
		detailMutedStyle.Render("Its task record and workspace will be deleted automatically after 7 days."),
		detailMutedStyle.Render(m.displayPathWithHomeTilde(confirm.ProjectPath)),
	}
	if confirm.Selected == agentTaskActionFocusRevoke {
		messageLines = []string{
			detailValueStyle.Render(fmt.Sprintf("Stop reopening this worker automatically. %d correction round(s) would otherwise run without asking again.", confirm.RevokeRemaining)),
			detailMutedStyle.Render("The worker, its session and its edits are untouched. Later corrections still work; they ask for confirmation."),
			detailMutedStyle.Render(m.displayPathWithHomeTilde(confirm.ProjectPath)),
		}
	}
	if confirm.Selected == agentTaskActionFocusCapture {
		messageLines = []string{
			detailValueStyle.Render(fmt.Sprintf("Authorize the checkout as it stands now as the starting point for a correction of revision %d.", confirm.CaptureRevision)),
			detailMutedStyle.Render("Nothing is stashed, reset or committed. Use this after making your own fixes on top of the result you rejected."),
			detailMutedStyle.Render(m.displayPathWithHomeTilde(confirm.ProjectPath)),
		}
	}
	buttons := m.renderAgentTaskActionButtons(*confirm)
	lines := []string{
		detailSectionStyle.Render("Agent Task") + "  " + detailValueStyle.Render(confirm.TaskTitle),
		"",
		strings.Join(messageLines, "\n"),
		"",
		buttons,
	}
	panel := renderDialogPanel(panelW, panelInnerW, strings.Join(lines, "\n"))
	left := max(0, (bodyW-panelW)/2)
	top := max(0, (bodyH-lipgloss.Height(panel))/2)
	return overlayBlock(body, panel, bodyW, bodyH, left, top)
}

func (m Model) renderAgentTaskActionButtons(confirm agentTaskActionConfirmState) string {
	if confirm.Submitting {
		return disabledActionTextStyle.Render("[" + todoDialogWaitingLabel(m.spinnerFrame) + "]")
	}
	buttons := []string{renderDialogButton("Trash", confirm.Selected == agentTaskActionFocusTrash)}
	if confirm.Capture {
		buttons = append(buttons, renderDialogButton("Capture baseline", confirm.Selected == agentTaskActionFocusCapture))
	}
	if confirm.Revoke {
		buttons = append(buttons, renderDialogButton("Revoke corrections", confirm.Selected == agentTaskActionFocusRevoke))
	}
	return strings.Join(append(buttons, renderDialogButton("Keep", confirm.Selected == agentTaskActionFocusKeep)), " ")
}

func (m Model) agentTaskFooterActions(width int) []footerAction {
	if width < 60 {
		return nil
	}
	task, _, ok := m.selectedAgentTask()
	if !ok {
		return nil
	}
	if agentTaskOffersCorrectionCapture(task) || agentTaskOffersSupervisionRevoke(task) {
		return []footerAction{footerHideAction("x", "actions")}
	}
	return []footerAction{footerHideAction("x", "trash")}
}

func agentTaskActionTitle(task model.AgentTask) string {
	title := strings.TrimSpace(task.Title)
	if title == "" {
		title = strings.TrimSpace(task.ID)
	}
	if title == "" {
		title = "agent task"
	}
	return title
}
