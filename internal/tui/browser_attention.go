package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"lcroom/internal/browserctl"
	"lcroom/internal/codexapp"
)

type browserAttentionNotification struct {
	ProjectPath              string
	ProjectName              string
	SessionID                string
	Provider                 codexapp.Provider
	Activity                 browserctl.SessionActivity
	ManagedBrowserSessionKey string
	OpenURL                  string
}

type projectBrowserAttentionState struct {
	Provider                 codexapp.Provider
	Activity                 browserctl.SessionActivity
	ManagedBrowserSessionKey string
	OpenURL                  string
}

func browserAttentionFromSnapshot(snapshot codexapp.Snapshot) (projectBrowserAttentionState, bool) {
	activity := snapshot.BrowserActivity.Normalize()
	if snapshot.Closed || activity.State != browserctl.SessionActivityStateWaitingForUser {
		return projectBrowserAttentionState{}, false
	}
	return projectBrowserAttentionState{
		Provider:                 embeddedProvider(snapshot),
		Activity:                 activity,
		ManagedBrowserSessionKey: strings.TrimSpace(snapshot.ManagedBrowserSessionKey),
		OpenURL:                  managedBrowserAttentionURL(snapshot),
	}, true
}

func (m Model) projectPendingBrowserAttention(projectPath string) (projectBrowserAttentionState, bool) {
	projectPath = strings.TrimSpace(projectPath)
	if projectPath == "" {
		return projectBrowserAttentionState{}, false
	}
	snapshot, ok := m.nonBlockingCodexSnapshot(projectPath)
	if !ok {
		return projectBrowserAttentionState{}, false
	}
	return browserAttentionFromSnapshot(snapshot)
}

func browserAttentionListSummary(state projectBrowserAttentionState) string {
	source := state.Activity.Normalize().SourceLabel()
	if source == "" {
		source = "browser"
	}
	return "browser: " + source + " waiting for input"
}

func browserAttentionDetailSummary(state projectBrowserAttentionState) string {
	summary := state.Activity.Summary()
	if strings.TrimSpace(summary) == "" {
		summary = "Browser is waiting for user input."
	}
	if strings.TrimSpace(state.ManagedBrowserSessionKey) != "" {
		return summary + " Open the embedded session to show or focus the managed browser."
	}
	return summary + " Open the embedded session to review the request."
}

func (m Model) browserAttentionCount() int {
	count := 0
	for _, snapshot := range m.codexSnapshots {
		if _, ok := browserAttentionFromSnapshot(snapshot); ok {
			count++
		}
	}
	return count
}

func (m Model) footerBrowserAttentionLabel() string {
	switch count := m.browserAttentionCount(); count {
	case 0:
		return ""
	case 1:
		return "1 browser wait"
	default:
		return fmt.Sprintf("%d browser waits", count)
	}
}

func (m Model) renderFooterBrowserAttentionSegment() string {
	text := m.footerBrowserAttentionLabel()
	if text == "" {
		return ""
	}
	return renderFooterAlert(text)
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
		ProjectPath:              projectPath,
		ProjectName:              strings.TrimSpace(filepath.Base(projectPath)),
		SessionID:                strings.TrimSpace(snapshot.ThreadID),
		Provider:                 embeddedProvider(snapshot),
		Activity:                 activity,
		ManagedBrowserSessionKey: strings.TrimSpace(snapshot.ManagedBrowserSessionKey),
		OpenURL:                  managedBrowserAttentionURL(snapshot),
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
		if notify.canOpenBrowser() {
			return m.openBrowserAttentionLogin(*notify)
		}
		m.dismissBrowserAttentionNotification()
		return m.showCodexProject(notify.ProjectPath, notify.Provider.Label()+" browser needs your attention")
	case "o":
		if notify.canOpenBrowser() {
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

func (n browserAttentionNotification) canOpenBrowser() bool {
	return strings.TrimSpace(n.ManagedBrowserSessionKey) != ""
}

func managedBrowserFlowSupported(provider codexapp.Provider) bool {
	switch provider.Normalized() {
	case codexapp.ProviderCodex, codexapp.ProviderOpenCode:
		return true
	default:
		return false
	}
}

func managedBrowserLoginURL(provider codexapp.Provider, policy browserctl.Policy, requestMode codexapp.ElicitationMode, requestURL string) string {
	if !managedBrowserFlowSupported(provider) {
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

func managedBrowserCurrentPageURL(snapshot codexapp.Snapshot) string {
	return managedBrowserSessionURL(embeddedProvider(snapshot), snapshot.BrowserActivity.Policy, snapshot.CurrentBrowserPageURL)
}

func managedBrowserAttentionURL(snapshot codexapp.Snapshot) string {
	if request := snapshot.PendingElicitation; request != nil {
		if loginURL := managedBrowserLoginURL(embeddedProvider(snapshot), snapshot.BrowserActivity.Policy, request.Mode, request.URL); loginURL != "" {
			return loginURL
		}
	}
	if snapshot.BrowserActivity.Normalize().State != browserctl.SessionActivityStateWaitingForUser {
		return ""
	}
	return managedBrowserCurrentPageURL(snapshot)
}

func managedBrowserSessionURL(provider codexapp.Provider, policy browserctl.Policy, rawURL string) string {
	if !managedBrowserFlowSupported(provider) {
		return ""
	}
	policy = policy.Normalize()
	if policy.ManagementMode != browserctl.ManagementModeManaged || policy.LoginMode != browserctl.LoginModePromote {
		return ""
	}
	return strings.TrimSpace(rawURL)
}

func (m Model) openBrowserAttentionLogin(notify browserAttentionNotification) (tea.Model, tea.Cmd) {
	if !notify.canOpenBrowser() {
		m.dismissBrowserAttentionNotification()
		return m.showCodexProject(notify.ProjectPath, notify.Provider.Label()+" browser needs your attention")
	}

	m.dismissBrowserAttentionNotification()
	updated, revealCmd := m.showCodexProject(
		notify.ProjectPath,
		"Showing the managed browser window and switching to the embedded session...",
	)
	model := updated.(Model)
	leaseModel, openCmd := model.openManagedBrowserLogin(
		notify.ProjectPath,
		notify.Provider,
		notify.SessionID,
		notify.ManagedBrowserSessionKey,
		notify.Activity,
		notify.OpenURL,
		"Showing the managed browser window and switching to the embedded session...",
		"Managed browser window is ready. Finish the browser flow there, then return to the embedded session if more input is needed.",
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
	if notify.canOpenBrowser() {
		lines = append(lines, renderWrappedDialogTextLines(
			detailMutedStyle,
			width,
			"Little Control Room can reveal the managed browser window for this same session, then bring the embedded session forward so you can keep an eye on it.",
		)...)
		lines = append(lines,
			"",
			renderDialogAction("Enter", "show browser", commitActionKeyStyle, commitActionTextStyle),
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
