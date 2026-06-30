package tui

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"lcroom/internal/model"
)

func (m *Model) openTodoPendingLaunchDialog(pending todoPendingLaunchState, message string, allowAbort bool, selected int) {
	message = strings.TrimSpace(message)
	if message == "" {
		message = todoPendingLaunchDetailSummary(pending, m.currentTime())
	}
	if !allowAbort {
		selected = todoPendingLaunchDialogFocusOK
	} else if selected != todoPendingLaunchDialogFocusAbort {
		selected = todoPendingLaunchDialogFocusOK
	}
	m.todoPendingLaunchDialog = &todoPendingLaunchDialogState{
		LaunchID:   pending.ID,
		Message:    message,
		AllowAbort: allowAbort,
		Selected:   selected,
	}
}

func (m Model) openTodoPendingLaunchDialogForSelection(selected int) (tea.Model, tea.Cmd) {
	row, project, ok := m.selectedProjectRow()
	if !ok || row.Kind != projectListRowPendingWorktree {
		m.status = "No pending worktree selected"
		return m, nil
	}
	pending, ok := m.todoPendingLaunchForProjectPath(project.Path)
	if !ok || pending == nil {
		m.status = "TODO worktree creation is no longer running"
		return m, nil
	}
	m.openTodoPendingLaunchDialog(*pending, "", true, selected)
	return m, nil
}

func (m Model) updateTodoPendingLaunchDialogMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	dialog := m.todoPendingLaunchDialog
	if dialog == nil {
		return m, nil
	}
	pending := m.activeTodoPendingLaunch()
	if pending == nil || pending.ID != dialog.LaunchID {
		m.todoPendingLaunchDialog = nil
		m.status = "TODO worktree creation is no longer running"
		return m, nil
	}
	switch msg.String() {
	case "esc":
		m.todoPendingLaunchDialog = nil
		return m, nil
	case "left", "h", "right", "l", "tab", "shift+tab":
		if dialog.AllowAbort {
			if dialog.Selected == todoPendingLaunchDialogFocusAbort {
				dialog.Selected = todoPendingLaunchDialogFocusOK
			} else {
				dialog.Selected = todoPendingLaunchDialogFocusAbort
			}
		}
		return m, nil
	case "enter", " ":
		if dialog.AllowAbort && dialog.Selected == todoPendingLaunchDialogFocusAbort {
			return m, m.cancelTodoPendingLaunch("Canceling TODO start...")
		}
		m.todoPendingLaunchDialog = nil
		return m, nil
	}
	return m, nil
}

func (m Model) renderTodoPendingLaunchDetailContent(project model.ProjectSummary, width int) string {
	pending, ok := m.todoPendingLaunchForProjectPath(project.Path)
	if !ok || pending == nil {
		return renderWrappedDetailField("Summary", detailMutedStyle, width, "TODO worktree creation is no longer running")
	}
	return m.renderTodoPendingLaunchDetailForPending(*pending, width)
}

func (m Model) renderTodoPendingLaunchDetailForPending(pending todoPendingLaunchState, width int) string {
	lines := []string{
		renderWrappedDetailField("Summary", detailWarningStyle, width, todoPendingLaunchDetailSummary(pending, m.currentTime())),
		detailField("Repo root", detailValueStyle.Render(m.displayPathWithHomeTilde(pending.ProjectPath))),
	}
	if pending.TodoID > 0 {
		lines = append(lines, detailField("TODO", detailValueStyle.Render("#"+formatInt64(pending.TodoID))))
	}
	lines = append(lines, detailField("Provider", detailValueStyle.Render(pending.Provider.Label())))
	if !pending.StartedAt.IsZero() {
		lines = append(lines, detailField("Started", detailValueStyle.Render(pending.StartedAt.Format(time.RFC3339))))
	}
	if text := todoPreviewText(pending.TodoText); text != "" {
		lines = append(lines, renderWrappedDetailField("Prompt", detailMutedStyle, width, truncateText(text, 280)))
	}
	return strings.Join(lines, "\n")
}

func (m Model) renderTodoPendingLaunchDialogOverlay(body string, bodyW, bodyH int) string {
	if m.todoPendingLaunchDialog == nil {
		return body
	}
	panelW := min(max(50, bodyW-24), 82)
	panelInnerW := max(28, panelW-4)
	content := m.renderTodoPendingLaunchDialogContent(panelInnerW)
	panel := renderDialogPanel(panelW, panelInnerW, content)
	left := max(0, (bodyW-panelW)/2)
	top := max(0, (bodyH-lipgloss.Height(panel))/2)
	return overlayBlock(body, panel, bodyW, bodyH, left, top)
}

func (m Model) renderTodoPendingLaunchDialogContent(width int) string {
	dialog := m.todoPendingLaunchDialog
	if dialog == nil {
		return ""
	}
	pending := m.activeTodoPendingLaunch()
	if pending == nil || pending.ID != dialog.LaunchID {
		return strings.Join([]string{
			renderDialogHeader("Preparing Worktree", "", "", width),
			"",
			detailMutedStyle.Render("TODO worktree creation is no longer running."),
			"",
			renderDialogButton("OK", true),
		}, "\n")
	}

	lines := []string{
		renderDialogHeader("Preparing Worktree", pending.ProjectName, "", width),
		"",
	}
	lines = append(lines, renderWrappedDialogTextLines(detailWarningStyle, width, dialog.Message)...)
	lines = append(lines, "")
	if pending.TodoID > 0 {
		lines = append(lines, commitPreviewLine("TODO", "#"+formatInt64(pending.TodoID)))
	}
	lines = append(lines, commitPreviewLine("Provider", pending.Provider.Label()))
	lines = append(lines, commitPreviewLine("Repo root", m.displayPathWithHomeTilde(pending.ProjectPath)))
	if !pending.StartedAt.IsZero() {
		lines = append(lines, commitPreviewLine("Elapsed", formatRunningDuration(m.currentTime().Sub(pending.StartedAt))))
	}
	if text := todoPreviewText(pending.TodoText); text != "" {
		lines = append(lines, "")
		lines = append(lines, renderWrappedDialogTextLines(detailMutedStyle, width, truncateText(text, 240))...)
	}
	lines = append(lines, "")
	lines = append(lines, m.renderTodoPendingLaunchDialogButtons(*dialog))
	return strings.Join(lines, "\n")
}

func (m Model) renderTodoPendingLaunchDialogButtons(dialog todoPendingLaunchDialogState) string {
	if !dialog.AllowAbort {
		return renderDialogButton("OK", true)
	}
	return strings.Join([]string{
		renderDialogButton("Keep waiting", dialog.Selected == todoPendingLaunchDialogFocusOK),
		renderDialogButton("Abort", dialog.Selected == todoPendingLaunchDialogFocusAbort),
	}, " ")
}
