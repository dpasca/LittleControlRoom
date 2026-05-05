package tui

import (
	"fmt"
	"strings"

	"lcroom/internal/model"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const (
	agentTaskActionFocusArchive = iota
	agentTaskActionFocusKeep
)

type agentTaskActionConfirmState struct {
	TaskID      string
	ProjectPath string
	TaskTitle   string
	Selected    int
	Submitting  bool
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
			m.status = "Archive is available for agent tasks"
		} else {
			m.status = "No project selected"
		}
		return nil
	}
	if snapshot, ok := m.liveAgentTaskSnapshot(task); ok && embeddedSessionBlocksProviderSwitch(snapshot) {
		m.showSessionBlockedAttentionDialog(
			project,
			"Archive blocked",
			"Wait for the embedded engineer session before archiving this agent task.",
			"archive this task",
			embeddedProvider(snapshot),
		)
		return nil
	}
	m.agentTaskAction = &agentTaskActionConfirmState{
		TaskID:      task.ID,
		ProjectPath: task.WorkspacePath,
		TaskTitle:   agentTaskActionTitle(task),
		Selected:    agentTaskActionFocusKeep,
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
	if confirm.Selected == agentTaskActionFocusKeep {
		confirm.Selected = agentTaskActionFocusArchive
	} else {
		confirm.Selected = agentTaskActionFocusKeep
	}
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
		task := model.AgentTask{ID: confirm.TaskID, Title: confirm.TaskTitle, WorkspacePath: confirm.ProjectPath}
		if snapshot, ok := m.liveAgentTaskSnapshot(task); ok && embeddedSessionBlocksProviderSwitch(snapshot) {
			project := model.ProjectSummary{Name: confirm.TaskTitle, Path: confirm.ProjectPath, Kind: model.ProjectKindAgentTask}
			m.showSessionBlockedAttentionDialog(
				project,
				"Archive blocked",
				"Wait for the embedded engineer session before archiving this agent task.",
				"archive this task",
				embeddedProvider(snapshot),
			)
			return m, nil
		}
		confirm.Submitting = true
		selectPath := m.nextProjectSelectionPathAfter(confirm.ProjectPath)
		m.status = "Archiving agent task..."
		closeCmd, err := m.closeEmbeddedSessionForProject(confirm.ProjectPath)
		if err != nil {
			confirm.Submitting = false
			m.reportError("Agent task action failed", err, confirm.ProjectPath)
			return m, nil
		}
		return m, batchCmds(closeCmd, m.archiveAgentTaskCmd(confirm.TaskID, confirm.ProjectPath, selectPath))
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
		task, err := m.svc.ArchiveAgentTask(m.ctx, taskID)
		return agentTaskActionMsg{
			task:        task,
			projectPath: projectPath,
			selectPath:  selectPath,
			status:      "Agent task archived",
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
		detailValueStyle.Render("Archive this agent task and hide it from the dashboard."),
		detailMutedStyle.Render(m.displayPathWithHomeTilde(confirm.ProjectPath)),
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
	return strings.Join([]string{
		renderDialogButton("Archive", confirm.Selected == agentTaskActionFocusArchive),
		renderDialogButton("Keep", confirm.Selected == agentTaskActionFocusKeep),
	}, " ")
}

func (m Model) agentTaskFooterActions(width int) []footerAction {
	if width < 60 {
		return nil
	}
	if _, _, ok := m.selectedAgentTask(); !ok {
		return nil
	}
	return []footerAction{footerHideAction("x", "archive")}
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
