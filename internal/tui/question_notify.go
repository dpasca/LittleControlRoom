package tui

import (
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"lcroom/internal/codexapp"
)

// questionNotification tracks a pending question notification that should be
// displayed as a popup overlay on the dashboard.
type questionNotification struct {
	ProjectPath string
	ProjectName string
	Provider    codexapp.Provider
	Summary     string
}

// detectQuestionNotification checks whether a codex snapshot update introduced
// a new pending question for a project that is not currently visible (i.e. the
// user is looking at the dashboard). If so, it populates the notification.
func (m *Model) detectQuestionNotification(projectPath string, snapshot codexapp.Snapshot) {
	if m.codexVisible() && m.codexVisibleProject == projectPath {
		// User is already looking at this session — no need to notify.
		return
	}
	if snapshot.Closed {
		return
	}

	hasPending := snapshot.PendingToolInput != nil || snapshot.PendingElicitation != nil
	if !hasPending {
		// If notification was for this project and the question was resolved, clear it.
		if m.questionNotify != nil && m.questionNotify.ProjectPath == projectPath {
			m.questionNotify = nil
		}
		return
	}

	// Build the notification.
	var summary string
	if snapshot.PendingToolInput != nil {
		summary = snapshot.PendingToolInput.Summary()
	} else {
		summary = snapshot.PendingElicitation.Summary()
	}
	provider := embeddedProvider(snapshot)
	m.questionNotify = &questionNotification{
		ProjectPath: projectPath,
		ProjectName: strings.TrimSpace(filepath.Base(projectPath)),
		Provider:    provider,
		Summary:     summary,
	}
}

func (m *Model) dismissQuestionNotification() {
	m.questionNotify = nil
}

func (m Model) updateQuestionNotifyMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	notify := m.questionNotify
	if notify == nil {
		return m, nil
	}
	switch msg.String() {
	case "esc", "q":
		m.dismissQuestionNotification()
		return m, nil
	case "enter":
		m.dismissQuestionNotification()
		return m.showCodexProject(notify.ProjectPath, notify.Provider.Label()+" is waiting for your answer")
	case "ctrl+c":
		// Let the normal quit handler deal with ctrl+c.
		m.dismissQuestionNotification()
		return m.updateNormalMode(msg)
	}
	return m, nil
}

// renderQuestionNotifyOverlay renders the question notification popup over the
// dashboard body.
func (m Model) renderQuestionNotifyOverlay(body string, width, height int) string {
	panel := m.renderQuestionNotifyPanel(width)
	panelWidth := lipgloss.Width(panel)
	panelHeight := lipgloss.Height(panel)
	left := max(0, (width-panelWidth)/2)
	top := max(0, (height-panelHeight)/4)
	return overlayBlock(body, panel, width, height, left, top)
}

func (m Model) renderQuestionNotifyPanel(bodyW int) string {
	panelWidth := min(bodyW, min(max(50, bodyW-10), 80))
	panelInnerWidth := max(26, panelWidth-4)
	return renderDialogPanel(panelWidth, panelInnerWidth, m.renderQuestionNotifyContent(panelInnerWidth))
}

func (m Model) renderQuestionNotifyContent(width int) string {
	notify := m.questionNotify
	if notify == nil {
		return ""
	}

	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("33"))
	questionStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Width(width)

	lines := []string{
		titleStyle.Render(notify.Provider.Label() + " needs your input"),
		"",
		detailField("Project", detailValueStyle.Render(notify.ProjectName)),
	}

	summary := notify.Summary
	if len(summary) > width && width > 3 {
		summary = summary[:width-3] + "..."
	}
	lines = append(lines,
		"",
		questionStyle.Render(summary),
		"",
		renderDialogAction("Enter", "open session", commitActionKeyStyle, commitActionTextStyle),
		renderDialogAction("Esc", "dismiss", cancelActionKeyStyle, cancelActionTextStyle),
	)
	return strings.Join(lines, "\n")
}
