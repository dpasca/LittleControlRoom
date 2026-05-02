package tui

import (
	"strings"

	"lcroom/internal/model"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const (
	projectRemoveConfirmFocusRemove = iota
	projectRemoveConfirmFocusKeep
)

type projectRemoveConfirmState struct {
	ProjectPath   string
	ProjectName   string
	PresentOnDisk bool
	Selected      int
	Submitting    bool
}

func (m *Model) openProjectRemoveConfirmForSelection() tea.Cmd {
	project, ok := m.selectedProject()
	if !ok {
		m.status = "No project selected"
		return nil
	}
	m.projectRemoveConfirm = &projectRemoveConfirmState{
		ProjectPath:   project.Path,
		ProjectName:   projectTitle(project.Path, project.Name),
		PresentOnDisk: project.PresentOnDisk,
		Selected:      projectRemoveConfirmFocusKeep,
	}
	m.status = "Confirm project removal"
	return nil
}

func (m *Model) closeProjectRemoveConfirm(status string) {
	m.projectRemoveConfirm = nil
	if status != "" {
		m.status = status
	}
}

func (m *Model) cycleProjectRemoveConfirmSelection(delta int) {
	confirm := m.projectRemoveConfirm
	if confirm == nil || delta == 0 {
		return
	}
	count := 2
	confirm.Selected = (confirm.Selected + delta + count) % count
}

func (m Model) updateProjectRemoveConfirmMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	confirm := m.projectRemoveConfirm
	if confirm == nil {
		return m, nil
	}
	if confirm.Submitting {
		if msg.String() == "esc" {
			m.status = "Project removal already in progress"
		}
		return m, nil
	}
	switch msg.String() {
	case "esc":
		m.closeProjectRemoveConfirm("Project removal canceled")
		return m, nil
	case "left", "h", "up", "k", "shift+tab":
		m.cycleProjectRemoveConfirmSelection(-1)
		return m, nil
	case "right", "l", "down", "j", "tab":
		m.cycleProjectRemoveConfirmSelection(1)
		return m, nil
	case "enter":
		if confirm.Selected == projectRemoveConfirmFocusKeep {
			m.closeProjectRemoveConfirm("Project removal canceled")
			return m, nil
		}
		confirm.Submitting = true
		project := model.ProjectSummary{
			Path:          confirm.ProjectPath,
			Name:          confirm.ProjectName,
			PresentOnDisk: confirm.PresentOnDisk,
		}
		if confirm.PresentOnDisk {
			m.status = "Removing project from list..."
			return m, m.removeProjectFromListCmd(project)
		}
		m.status = "Removing stale project..."
		return m, m.removeProjectCmd(confirm.ProjectPath)
	}
	return m, nil
}

func (m Model) renderProjectRemoveConfirmOverlay(body string, bodyW, bodyH int) string {
	confirm := m.projectRemoveConfirm
	if confirm == nil {
		return body
	}
	panelW := min(max(50, bodyW-24), 76)
	panelInnerW := max(28, panelW-4)
	title := "Remove from list"
	description := "This hides only this exact project path. It does not delete files on disk."
	descriptionStyle := detailMutedStyle
	if !confirm.PresentOnDisk {
		title = "Remove missing project"
		description = "This forgets the stale dashboard entry. The folder is already missing on disk."
		descriptionStyle = detailWarningStyle
	}
	lines := []string{
		detailSectionStyle.Render(title),
		"",
		detailValueStyle.Render(truncateText(confirm.ProjectName, panelInnerW)),
		detailMutedStyle.Render(truncateText(m.displayPathWithHomeTilde(confirm.ProjectPath), panelInnerW)),
		"",
	}
	lines = append(lines, renderWrappedDialogTextLines(descriptionStyle, panelInnerW, description)...)
	if confirm.Submitting {
		lines = append(lines, "", detailValueStyle.Render("Removal in progress"))
	}
	lines = append(lines, "", renderProjectRemoveConfirmButtons(confirm, m.spinnerFrame))
	panel := renderDialogPanel(panelW, panelInnerW, strings.Join(lines, "\n"))
	left := max(0, (bodyW-panelW)/2)
	top := max(0, (bodyH-lipgloss.Height(panel))/2)
	return overlayBlock(body, panel, bodyW, bodyH, left, top)
}

func renderProjectRemoveConfirmButtons(confirm *projectRemoveConfirmState, spinnerFrame int) string {
	if confirm == nil {
		return ""
	}
	if confirm.Submitting {
		return disabledActionTextStyle.Render("[" + todoDialogWaitingLabel(spinnerFrame) + "]")
	}
	return strings.Join([]string{
		renderDialogButton("Remove", confirm.Selected == projectRemoveConfirmFocusRemove),
		renderDialogButton("Keep", confirm.Selected == projectRemoveConfirmFocusKeep),
	}, " ")
}
