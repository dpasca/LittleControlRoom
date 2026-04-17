package tui

import (
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"lcroom/internal/browserctl"
	"lcroom/internal/codexapp"
)

type browserAttentionNotification struct {
	ProjectPath string
	ProjectName string
	Provider    codexapp.Provider
	Activity    browserctl.SessionActivity
}

func (m *Model) detectBrowserAttentionNotification(projectPath string, snapshot codexapp.Snapshot) {
	activity := snapshot.BrowserActivity.Normalize()
	waiting := !snapshot.Closed && activity.State == browserctl.SessionActivityStateWaitingForUser

	if !waiting {
		if m.browserAttention != nil && m.browserAttention.ProjectPath == projectPath {
			m.browserAttention = nil
		}
		return
	}
	if m.codexVisible() && m.codexVisibleProject == projectPath {
		return
	}

	m.browserAttention = &browserAttentionNotification{
		ProjectPath: projectPath,
		ProjectName: strings.TrimSpace(filepath.Base(projectPath)),
		Provider:    embeddedProvider(snapshot),
		Activity:    activity,
	}
	if m.questionNotify != nil && m.questionNotify.ProjectPath == projectPath {
		m.questionNotify = nil
	}
}

func (m *Model) dismissBrowserAttentionNotification() {
	m.browserAttention = nil
}

func (m Model) updateBrowserAttentionMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	notify := m.browserAttention
	if notify == nil {
		return m, nil
	}
	switch msg.String() {
	case "esc", "q":
		m.dismissBrowserAttentionNotification()
		return m, nil
	case "enter":
		m.dismissBrowserAttentionNotification()
		return m.showCodexProject(notify.ProjectPath, notify.Provider.Label()+" browser needs your attention")
	case "b", "s":
		m.dismissBrowserAttentionNotification()
		return m, m.openBrowserSettingsMode()
	case "ctrl+c":
		m.dismissBrowserAttentionNotification()
		return m.updateNormalMode(msg)
	}
	return m, nil
}

func (m Model) renderBrowserAttentionOverlay(body string, width, height int) string {
	panel := m.renderBrowserAttentionPanel(width)
	panelWidth := lipgloss.Width(panel)
	panelHeight := lipgloss.Height(panel)
	left := max(0, (width-panelWidth)/2)
	top := max(0, (height-panelHeight)/4)
	return overlayBlock(body, panel, width, height, left, top)
}

func (m Model) renderBrowserAttentionPanel(bodyW int) string {
	panelWidth := min(bodyW, min(max(52, bodyW-10), 82))
	panelInnerWidth := max(28, panelWidth-4)
	return renderDialogPanel(panelWidth, panelInnerWidth, m.renderBrowserAttentionContent(panelInnerWidth))
}

func (m Model) renderBrowserAttentionContent(width int) string {
	notify := m.browserAttention
	if notify == nil {
		return ""
	}

	projectName := notify.ProjectName
	if projectName == "" {
		projectName = strings.TrimSpace(notify.ProjectPath)
	}
	source := notify.Activity.SourceLabel()
	if source == "" {
		source = "Playwright"
	}

	lines := []string{
		lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("214")).Render("Browser needs attention"),
		"",
		detailField("Project", detailValueStyle.Render(projectName)),
		detailField("Provider", detailValueStyle.Render(notify.Provider.Label())),
		detailField("Source", detailWarningStyle.Render(source)),
		"",
	}
	lines = append(lines, renderWrappedDialogTextLines(
		detailWarningStyle,
		width,
		notify.Activity.Summary(),
	)...)
	lines = append(lines, "")
	lines = append(lines, renderWrappedDialogTextLines(
		detailMutedStyle,
		width,
		"Open the embedded session to review the request. LCR still does not own the browser window for this flow yet.",
	)...)
	lines = append(lines,
		"",
		renderDialogAction("Enter", "open session", commitActionKeyStyle, commitActionTextStyle),
		renderDialogAction("B", "browser settings", pushActionKeyStyle, pushActionTextStyle),
		renderDialogAction("Esc", "dismiss", cancelActionKeyStyle, cancelActionTextStyle),
	)
	return strings.Join(lines, "\n")
}
