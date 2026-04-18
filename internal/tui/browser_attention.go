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
	SessionID   string
	Provider    codexapp.Provider
	Activity    browserctl.SessionActivity
	RequestMode codexapp.ElicitationMode
	RequestURL  string
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
		SessionID:   strings.TrimSpace(snapshot.ThreadID),
		Provider:    embeddedProvider(snapshot),
		Activity:    activity,
	}
	if request := snapshot.PendingElicitation; request != nil {
		m.browserAttention.RequestMode = request.Mode
		m.browserAttention.RequestURL = strings.TrimSpace(request.URL)
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
		if notify.canOpenBrowserForLogin() {
			return m.openBrowserAttentionLogin(*notify)
		}
		m.dismissBrowserAttentionNotification()
		return m.showCodexProject(notify.ProjectPath, notify.Provider.Label()+" browser needs your attention")
	case "o":
		if notify.canOpenBrowserForLogin() {
			return m.openBrowserAttentionLogin(*notify)
		}
		return m, nil
	case "s":
		m.dismissBrowserAttentionNotification()
		return m.showCodexProject(notify.ProjectPath, notify.Provider.Label()+" browser needs your attention")
	case "b":
		m.dismissBrowserAttentionNotification()
		return m, m.openBrowserSettingsMode()
	case "ctrl+c":
		m.dismissBrowserAttentionNotification()
		return m.updateNormalMode(msg)
	}
	return m, nil
}

func (n browserAttentionNotification) canOpenBrowserForLogin() bool {
	return n.managedLoginURL() != ""
}

func (n browserAttentionNotification) managedLoginURL() string {
	return managedBrowserLoginURL(n.Provider, n.Activity.Policy, n.RequestMode, n.RequestURL)
}

func managedBrowserLoginURL(provider codexapp.Provider, policy browserctl.Policy, requestMode codexapp.ElicitationMode, requestURL string) string {
	if provider.Normalized() != codexapp.ProviderCodex {
		return ""
	}
	policy = policy.Normalize()
	if policy.ManagementMode != browserctl.ManagementModeManaged || policy.LoginMode != browserctl.LoginModePromote {
		return ""
	}
	if requestMode != codexapp.ElicitationModeURL {
		return ""
	}
	return strings.TrimSpace(requestURL)
}

func (m Model) openBrowserAttentionLogin(notify browserAttentionNotification) (tea.Model, tea.Cmd) {
	loginURL := notify.managedLoginURL()
	if loginURL == "" {
		m.dismissBrowserAttentionNotification()
		return m.showCodexProject(notify.ProjectPath, notify.Provider.Label()+" browser needs your attention")
	}

	m.dismissBrowserAttentionNotification()
	updated, revealCmd := m.showCodexProject(
		notify.ProjectPath,
		"Opening browser for login and switching to the embedded session...",
	)
	model := updated.(Model)
	leaseModel, openCmd := model.openManagedBrowserLogin(
		notify.ProjectPath,
		notify.Provider,
		notify.SessionID,
		notify.Activity,
		loginURL,
		"Opening browser for login and switching to the embedded session...",
		"Opened browser for login. Finish the browser flow, then return to the embedded session if more input is needed.",
	)
	model = leaseModel.(Model)
	if openCmd == nil {
		return model, revealCmd
	}
	return model, batchCmds(openCmd, revealCmd)
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
	if notify.canOpenBrowserForLogin() {
		lines = append(lines, renderWrappedDialogTextLines(
			detailMutedStyle,
			width,
			"Automatic browser mode can open this login flow in your default browser, then bring the embedded session forward so you can keep an eye on it.",
		)...)
		lines = append(lines,
			"",
			renderDialogAction("Enter", "open browser", commitActionKeyStyle, commitActionTextStyle),
			renderDialogAction("S", "open session", pushActionKeyStyle, pushActionTextStyle),
			renderDialogAction("B", "browser settings", pushActionKeyStyle, pushActionTextStyle),
			renderDialogAction("Esc", "dismiss", cancelActionKeyStyle, cancelActionTextStyle),
		)
	} else {
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
	}
	return strings.Join(lines, "\n")
}
