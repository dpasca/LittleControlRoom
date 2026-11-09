package tui

import (
	"fmt"
	"strings"
	"time"

	"lcroom/internal/model"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type agentTaskTrashState struct {
	items    []model.AgentTask
	selected int
	busy     bool
	err      error
}

type agentTaskTrashLoadedMsg struct {
	items []model.AgentTask
	err   error
}

func (m *Model) openAgentTaskTrash() tea.Cmd {
	m.agentTaskTrash = &agentTaskTrashState{busy: true}
	m.status = "Trash open; Enter inspects a retained workspace"
	return m.loadAgentTaskTrashCmd()
}

func (m Model) loadAgentTaskTrashCmd() tea.Cmd {
	return func() tea.Msg {
		if m.svc == nil {
			return agentTaskTrashLoadedMsg{err: fmt.Errorf("service unavailable")}
		}
		ctx, cancel := m.actionContext(tuiProjectsReloadTimeout)
		defer cancel()
		items, err := m.svc.ListAgentTaskTrash(ctx)
		return agentTaskTrashLoadedMsg{items: items, err: err}
	}
}

func (m Model) applyAgentTaskTrashLoaded(msg agentTaskTrashLoadedMsg) (tea.Model, tea.Cmd) {
	if state := m.agentTaskTrash; state != nil {
		selectedID := ""
		if state.selected < len(state.items) {
			selectedID = state.items[state.selected].ID
		}
		state.busy, state.err = false, msg.err
		state.items = nil
		for _, task := range msg.items {
			if m.privacyMode && task.CategoryPrivate {
				continue
			}
			state.items = append(state.items, task)
			if task.ID == selectedID {
				state.selected = len(state.items) - 1
			}
		}
		state.selected = min(state.selected, max(0, len(state.items)-1))
	}
	return m, nil
}

func (m Model) updateAgentTaskTrash(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	state := m.agentTaskTrash
	if key.String() == "esc" {
		m.agentTaskTrash = nil
		m.status = "Trash closed"
		return m, nil
	}
	if state.busy {
		return m, nil
	}
	switch key.String() {
	case "up", "k":
		state.selected = max(0, state.selected-1)
	case "down", "j":
		state.selected = min(max(0, len(state.items)-1), state.selected+1)
	case "r":
		state.busy, state.err = true, nil
		return m, m.loadAgentTaskTrashCmd()
	case "enter":
		if state.selected < len(state.items) {
			task := state.items[state.selected]
			project := model.ProjectSummary{Path: task.WorkspacePath, Name: agentTaskActionTitle(task)}
			return m, m.openOrphanedWorktreeInspection(project, task.OriginProjectPath)
		}
	}
	return m, nil
}

func agentTaskTrashRetention(task model.AgentTask, now time.Time) string {
	if task.CleanupError != "" {
		return "Cleanup failed: " + task.CleanupError
	}
	if task.ExpiresAt.IsZero() {
		return "Workspace retained; cleanup date unavailable"
	}
	if !task.ExpiresAt.After(now) {
		return "Retention expired; cleanup pending"
	}
	return "Workspace retained until " + task.ExpiresAt.Local().Format("Jan 2 at 15:04")
}

func (m Model) renderAgentTaskTrash(body string, width, height int) string {
	state := m.agentTaskTrash
	panelW := min(100, max(36, width-8))
	innerW := max(20, panelW-4)
	lines := []string{renderDialogHeader("Trash", "retained task workspaces", "", innerW), ""}
	switch {
	case state.busy:
		lines = append(lines, detailMutedStyle.Render("Loading retained tasks..."))
	case state.err != nil:
		lines = append(lines, renderWrappedDialogTextLines(detailDangerStyle, innerW, "Trash could not be loaded: "+state.err.Error())...)
	case len(state.items) == 0:
		lines = append(lines, detailMutedStyle.Render("No retained tasks in Trash."))
	default:
		visible := max(1, (height-14)/2)
		start := max(0, state.selected-visible+1)
		end := min(len(state.items), start+visible)
		for i := start; i < end; i++ {
			task := state.items[i]
			prefix, style := "  ", detailValueStyle
			if i == state.selected {
				prefix, style = "> ", dialogButtonSelectedStyle.UnsetPadding()
			}
			lines = append(lines, style.Render(truncateText(prefix+agentTaskActionTitle(task), innerW)))
			archived := "Archived " + formatListActivityTime(m.currentTime(), task.ArchivedAt)
			if task.ArchivedAt.Local().Format("2006-01-02") == m.currentTime().Local().Format("2006-01-02") {
				archived = "Archived today"
			}
			if task.ArchivedAt.IsZero() {
				archived = "Archive date unknown"
			}
			lines = append(lines, detailMutedStyle.Render(truncateText("  "+archived+" · "+agentTaskTrashRetention(task, m.currentTime()), innerW)))
		}
		lines = append(lines, detailMutedStyle.Render(fmt.Sprintf("%d of %d", state.selected+1, len(state.items))))
	}
	lines = append(lines, "",
		renderDialogAction("Enter", "inspect workspace", navigateActionKeyStyle, navigateActionTextStyle),
		renderDialogAction("↑↓", "select", navigateActionKeyStyle, navigateActionTextStyle),
		renderDialogAction("r", "refresh", navigateActionKeyStyle, navigateActionTextStyle),
		renderDialogAction("Esc", "close", cancelActionKeyStyle, cancelActionTextStyle),
	)
	panel := renderDialogPanel(panelW, innerW, strings.Join(lines, "\n"))
	return overlayBlock(body, panel, width, height, max(0, (width-lipgloss.Width(panel))/2), max(0, (height-lipgloss.Height(panel))/2))
}
