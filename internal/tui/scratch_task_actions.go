package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	"lcroom/internal/model"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const (
	scratchTaskActionFocusArchive = iota
	scratchTaskActionFocusDelete
	scratchTaskActionFocusKeep
)

type scratchTaskActionConfirmState struct {
	ProjectPath   string
	ProjectName   string
	PresentOnDisk bool
	Selected      int
	Submitting    bool
}

func (m Model) selectedScratchTask() (model.ProjectSummary, bool) {
	project, ok := m.selectedProject()
	if !ok {
		return model.ProjectSummary{}, false
	}
	if model.NormalizeProjectKind(project.Kind) != model.ProjectKindScratchTask {
		return model.ProjectSummary{}, false
	}
	return project, true
}

func (m *Model) openScratchTaskActionConfirmForSelection() tea.Cmd {
	project, ok := m.selectedScratchTask()
	if !ok {
		if _, ok := m.selectedProject(); ok {
			m.status = "Archive/delete is available for scratch tasks"
		} else {
			m.status = "No project selected"
		}
		return nil
	}
	m.scratchTaskAction = &scratchTaskActionConfirmState{
		ProjectPath:   project.Path,
		ProjectName:   projectTitle(project.Path, project.Name),
		PresentOnDisk: project.PresentOnDisk,
		Selected:      scratchTaskActionFocusKeep,
	}
	m.status = "Task actions open"
	return nil
}

func (m *Model) closeScratchTaskActionConfirm(status string) {
	m.scratchTaskAction = nil
	if status != "" {
		m.status = status
	}
}

func (m Model) scratchTaskActionChoices(confirm *scratchTaskActionConfirmState) []int {
	if confirm == nil {
		return nil
	}
	if !confirm.PresentOnDisk {
		return []int{scratchTaskActionFocusDelete, scratchTaskActionFocusKeep}
	}
	return []int{scratchTaskActionFocusArchive, scratchTaskActionFocusDelete, scratchTaskActionFocusKeep}
}

func (m *Model) cycleScratchTaskActionSelection(delta int) {
	confirm := m.scratchTaskAction
	if confirm == nil || delta == 0 {
		return
	}
	choices := m.scratchTaskActionChoices(confirm)
	if len(choices) == 0 {
		return
	}
	index := 0
	for i, choice := range choices {
		if choice == confirm.Selected {
			index = i
			break
		}
	}
	index = (index + delta + len(choices)) % len(choices)
	confirm.Selected = choices[index]
}

func (m Model) nextProjectSelectionPathAfter(projectPath string) string {
	index := m.indexByPath(projectPath)
	if index < 0 {
		return ""
	}
	if index+1 < len(m.projects) {
		return m.projects[index+1].Path
	}
	if index > 0 {
		return m.projects[index-1].Path
	}
	return ""
}

func (m Model) updateScratchTaskActionConfirmMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	confirm := m.scratchTaskAction
	if confirm == nil {
		return m, nil
	}
	if confirm.Submitting {
		if msg.String() == "esc" {
			m.status = "Task action already in progress"
		}
		return m, nil
	}
	switch msg.String() {
	case "esc":
		m.closeScratchTaskActionConfirm("Task actions closed")
		return m, nil
	case "left", "h":
		m.cycleScratchTaskActionSelection(-1)
		return m, nil
	case "right", "l", "tab", "shift+tab":
		m.cycleScratchTaskActionSelection(1)
		return m, nil
	case "enter":
		confirm.Submitting = true
		selectPath := m.nextProjectSelectionPathAfter(confirm.ProjectPath)
		switch confirm.Selected {
		case scratchTaskActionFocusArchive:
			m.status = "Archiving task..."
			return m, m.archiveScratchTaskCmd(confirm.ProjectPath, selectPath)
		case scratchTaskActionFocusDelete:
			if confirm.PresentOnDisk {
				m.status = "Deleting task..."
			} else {
				m.status = "Removing task from list..."
			}
			return m, m.deleteScratchTaskCmd(confirm.ProjectPath, selectPath)
		default:
			m.closeScratchTaskActionConfirm("Task actions closed")
			return m, nil
		}
	}
	return m, nil
}

func (m Model) archiveScratchTaskCmd(projectPath, selectPath string) tea.Cmd {
	if m.svc == nil {
		return func() tea.Msg {
			return scratchTaskActionMsg{projectPath: projectPath, selectPath: selectPath, err: fmt.Errorf("service unavailable")}
		}
	}
	return func() tea.Msg {
		_, err := m.svc.ArchiveScratchTask(m.ctx, projectPath)
		return scratchTaskActionMsg{
			projectPath: projectPath,
			selectPath:  selectPath,
			status:      "Scratch task archived",
			err:         err,
		}
	}
}

func (m Model) deleteScratchTaskCmd(projectPath, selectPath string) tea.Cmd {
	if m.svc == nil {
		return func() tea.Msg {
			return scratchTaskActionMsg{projectPath: projectPath, selectPath: selectPath, err: fmt.Errorf("service unavailable")}
		}
	}
	return func() tea.Msg {
		err := m.svc.DeleteScratchTask(m.ctx, projectPath)
		return scratchTaskActionMsg{
			projectPath: projectPath,
			selectPath:  selectPath,
			status:      "Scratch task deleted",
			err:         err,
		}
	}
}

func (m Model) renderScratchTaskActionOverlay(body string, bodyW, bodyH int) string {
	confirm := m.scratchTaskAction
	if confirm == nil {
		return body
	}
	panelW := min(max(50, bodyW-24), 76)
	panelInnerW := max(28, panelW-4)
	messageLines := []string{
		detailValueStyle.Render("Choose what to do with this scratch task."),
		detailMutedStyle.Render(m.displayPathWithHomeTilde(confirm.ProjectPath)),
	}
	if !confirm.PresentOnDisk {
		messageLines = append(messageLines, detailWarningStyle.Render("The folder is already missing on disk."))
	}
	buttons := m.renderScratchTaskActionButtons(*confirm)
	lines := []string{
		detailSectionStyle.Render("Task Actions") + "  " + detailValueStyle.Render(confirm.ProjectName),
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

func (m Model) renderScratchTaskActionButtons(confirm scratchTaskActionConfirmState) string {
	if confirm.Submitting {
		return disabledActionTextStyle.Render("[" + todoDialogWaitingLabel(m.spinnerFrame) + "]")
	}
	buttons := make([]string, 0, 3)
	if confirm.PresentOnDisk {
		buttons = append(buttons, renderDialogButton("Archive", confirm.Selected == scratchTaskActionFocusArchive))
		buttons = append(buttons, renderDialogButton("Delete", confirm.Selected == scratchTaskActionFocusDelete))
	} else {
		buttons = append(buttons, renderDialogButton("Remove", confirm.Selected == scratchTaskActionFocusDelete))
	}
	buttons = append(buttons, renderDialogButton("Keep", confirm.Selected == scratchTaskActionFocusKeep))
	return strings.Join(buttons, " ")
}

func (m Model) scratchTaskFooterActions(width int) []footerAction {
	if width < 60 {
		return nil
	}
	if _, ok := m.selectedScratchTask(); !ok {
		return nil
	}
	return []footerAction{footerHideAction("d", "task")}
}

func projectPathMatches(left, right string) bool {
	return filepath.Clean(strings.TrimSpace(left)) == filepath.Clean(strings.TrimSpace(right))
}
