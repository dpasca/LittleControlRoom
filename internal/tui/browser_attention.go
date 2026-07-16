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
	AttentionMessage         string
	Fingerprint              string
	Problem                  string
}

type projectBrowserAttentionState struct {
	Provider                 codexapp.Provider
	Activity                 browserctl.SessionActivity
	ManagedBrowserSessionKey string
	OpenURL                  string
	AttentionMessage         string
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
		AttentionMessage:         strings.TrimSpace(activity.AttentionMessage),
	}, true
}

func (m Model) browserAttentionFromSnapshot(snapshot codexapp.Snapshot) (projectBrowserAttentionState, bool) {
	state, ok := browserAttentionFromSnapshot(snapshot)
	if !ok {
		return projectBrowserAttentionState{}, false
	}
	if strings.TrimSpace(snapshot.ManagedBrowserSessionKey) != "" && managedBrowserAttentionURL(snapshot) != "" && !m.managedBrowserCanReveal(snapshot) {
		return projectBrowserAttentionState{}, false
	}
	return state, true
}

func (m Model) projectPendingBrowserAttention(projectPath string) (projectBrowserAttentionState, bool) {
	projectPath = strings.TrimSpace(projectPath)
	if projectPath == "" {
		return projectBrowserAttentionState{}, false
	}
	snapshot, ok := m.cachedLiveCodexSnapshot(projectPath)
	if !ok {
		return projectBrowserAttentionState{}, false
	}
	return m.browserAttentionFromSnapshot(snapshot)
}

func browserAttentionListSummary(state projectBrowserAttentionState) string {
	source := state.Activity.Normalize().SourceLabel()
	if source == "" {
		source = "browser"
	}
	return "browser: " + source + " waiting for input"
}

func browserAttentionDetailSummary(state projectBrowserAttentionState) string {
	summary := strings.TrimSpace(state.AttentionMessage)
	if strings.TrimSpace(summary) == "" {
		summary = state.Activity.Summary()
		if strings.TrimSpace(summary) == "" {
			summary = "Browser is waiting for user input."
		}
	}
	if strings.TrimSpace(state.ManagedBrowserSessionKey) != "" {
		summary += " Open the embedded session to show or focus the managed browser."
	} else {
		summary += " Open the embedded session to review the request."
	}
	return summary
}

func bossBrowserAttentionHostNoticeForSnapshot(projectPath string, hadPrevious bool, previous, snapshot codexapp.Snapshot) string {
	state, ok := browserAttentionFromSnapshot(snapshot)
	if !ok {
		return ""
	}
	if previousState, previousWaiting := browserAttentionFromSnapshot(previous); hadPrevious && previousWaiting {
		if bossBrowserAttentionFingerprint(previousState) == bossBrowserAttentionFingerprint(state) {
			return ""
		}
	}
	projectName := strings.TrimSpace(filepath.Base(projectPath))
	if projectName == "." || projectName == string(filepath.Separator) {
		projectName = strings.TrimSpace(projectPath)
	}
	provider := embeddedProvider(snapshot).Label()
	if provider == "" {
		provider = "engineer"
	}
	summary := strings.TrimSpace(state.AttentionMessage)
	if summary == "" {
		summary = strings.TrimSpace(state.Activity.Summary())
		if summary == "" {
			summary = "Browser is waiting for user input."
		}
	}
	lines := []string{
		fmt.Sprintf("%s browser update for %s: %s", provider, projectName, summary),
	}
	lines = append(lines,
		"",
		"If the browser window is open, finish that browser step there. Then come back to Chat and ask what the engineer found; I will read the task output before recommending the next move.",
	)
	return strings.Join(lines, "\n")
}

func bossBrowserAttentionFingerprint(state projectBrowserAttentionState) string {
	activity := state.Activity.Normalize()
	return strings.Join([]string{
		string(state.Provider.Normalized()),
		string(activity.State),
		strings.TrimSpace(activity.ServerName),
		strings.TrimSpace(activity.ToolName),
		strings.TrimSpace(state.ManagedBrowserSessionKey),
		strings.TrimSpace(state.OpenURL),
		strings.TrimSpace(state.AttentionMessage),
	}, "\x00")
}

func (m Model) browserAttentionCount() int {
	count := 0
	for _, snapshot := range m.codexSnapshots {
		if _, ok := m.browserAttentionFromSnapshot(snapshot); ok {
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
	_, snapshotWaiting := browserAttentionFromSnapshot(snapshot)
	state, waiting := m.browserAttentionFromSnapshot(snapshot)
	if !waiting {
		projectKey := normalizeProjectPath(projectPath)
		if m.browserAttention != nil && normalizeProjectPath(m.browserAttention.ProjectPath) == projectKey {
			m.browserAttention = nil
		}
		if !snapshotWaiting {
			delete(m.browserAttentionAcknowledged, projectKey)
		}
		return
	}

	notify := newBrowserAttentionNotification(projectPath, snapshot, state)
	projectKey := normalizeProjectPath(projectPath)
	if m.codexVisible() && normalizeProjectPath(m.codexVisibleProject) == projectKey {
		if request := snapshot.PendingElicitation; request != nil && request.Mode == codexapp.ElicitationModeURL {
			// The embedded URL elicitation is already a centered, actionable
			// browser dialog. Treat it as the foreground acknowledgement so a
			// second handoff dialog does not appear when the request settles.
			m.acknowledgeBrowserAttention(notify)
			if m.browserAttention != nil && normalizeProjectPath(m.browserAttention.ProjectPath) == projectKey {
				m.browserAttention = nil
			}
			return
		}
		if snapshot.PendingApproval != nil || snapshot.PendingToolInput != nil || snapshot.PendingElicitation != nil {
			// Let the provider's foreground input win. If the browser remains
			// paused after that input is handled, a later snapshot can surface it.
			if m.browserAttention != nil && normalizeProjectPath(m.browserAttention.ProjectPath) == projectKey {
				m.browserAttention = nil
			}
			return
		}
	}
	if m.browserAttentionAcknowledged[projectKey] == notify.Fingerprint {
		return
	}
	if m.browserAttention != nil && m.browserAttention.fingerprint() == notify.Fingerprint {
		return
	}

	m.browserAttention = &notify
	if m.questionNotify != nil && m.questionNotify.ProjectPath == projectPath {
		m.questionNotify = nil
	}
}

func (m *Model) redetectBrowserAttentionForManagedSession(sessionKey string) {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return
	}
	for projectPath, snapshot := range m.codexSnapshots {
		if strings.TrimSpace(snapshot.ManagedBrowserSessionKey) != sessionKey {
			continue
		}
		m.detectBrowserAttentionNotification(projectPath, snapshot)
	}
}

func (m *Model) dismissBrowserAttentionNotification() {
	if m.browserAttention != nil {
		m.acknowledgeBrowserAttention(*m.browserAttention)
	}
	m.browserAttention = nil
}

func (m Model) browserAttentionDialogCanTakeFocus() bool {
	if m.browserAttention == nil {
		return false
	}
	if !m.codexVisible() {
		return true
	}
	snapshot, ok := m.currentCachedCodexSnapshot()
	if !ok {
		return true
	}
	return snapshot.PendingApproval == nil && snapshot.PendingToolInput == nil && snapshot.PendingElicitation == nil
}

func newBrowserAttentionNotification(projectPath string, snapshot codexapp.Snapshot, state projectBrowserAttentionState) browserAttentionNotification {
	projectPath = strings.TrimSpace(projectPath)
	notify := browserAttentionNotification{
		ProjectPath:              projectPath,
		ProjectName:              strings.TrimSpace(filepath.Base(projectPath)),
		SessionID:                strings.TrimSpace(snapshot.ThreadID),
		Provider:                 state.Provider,
		Activity:                 state.Activity,
		ManagedBrowserSessionKey: state.ManagedBrowserSessionKey,
		OpenURL:                  state.OpenURL,
		AttentionMessage:         state.AttentionMessage,
	}
	notify.Fingerprint = notify.fingerprint()
	return notify
}

func (n browserAttentionNotification) fingerprint() string {
	if fingerprint := strings.TrimSpace(n.Fingerprint); fingerprint != "" {
		return fingerprint
	}
	state := projectBrowserAttentionState{
		Provider:                 n.Provider,
		Activity:                 n.Activity,
		ManagedBrowserSessionKey: n.ManagedBrowserSessionKey,
		OpenURL:                  n.OpenURL,
		AttentionMessage:         n.AttentionMessage,
	}
	return strings.Join([]string{
		normalizeProjectPath(n.ProjectPath),
		strings.TrimSpace(n.SessionID),
		bossBrowserAttentionFingerprint(state),
	}, "\x00")
}

func (m *Model) acknowledgeBrowserAttention(notify browserAttentionNotification) {
	projectKey := normalizeProjectPath(notify.ProjectPath)
	if projectKey == "" {
		return
	}
	if m.browserAttentionAcknowledged == nil {
		m.browserAttentionAcknowledged = make(map[string]string)
	}
	m.browserAttentionAcknowledged[projectKey] = notify.fingerprint()
}

func (m *Model) restoreBrowserAttentionAfterRevealFailure(msg browserOpenMsg) {
	projectPath := normalizeProjectPath(firstNonEmptyString(msg.projectPath, msg.managedBrowserRef.ProjectPath))
	if projectPath == "" {
		return
	}
	state := projectBrowserAttentionState{
		Provider: codexapp.Provider(msg.managedBrowserRef.Provider).Normalized(),
		Activity: browserctl.SessionActivity{
			State:      browserctl.SessionActivityStateWaitingForUser,
			ServerName: "managed browser",
		},
		ManagedBrowserSessionKey: strings.TrimSpace(msg.managedBrowserSessionKey),
	}
	snapshot := codexapp.Snapshot{
		ProjectPath: projectPath,
		ThreadID:    strings.TrimSpace(msg.managedBrowserRef.SessionID),
		Provider:    state.Provider,
	}
	if cached, ok := m.codexCachedSnapshot(projectPath); ok {
		snapshot = cached
		if current, waiting := browserAttentionFromSnapshot(cached); waiting {
			state = current
		}
	}
	if state.ManagedBrowserSessionKey == "" {
		state.ManagedBrowserSessionKey = strings.TrimSpace(msg.managedBrowserSessionKey)
	}
	notify := newBrowserAttentionNotification(projectPath, snapshot, state)
	notify.Problem = "Little Control Room could not show or focus the managed browser window: " + strings.TrimSpace(msg.err.Error())
	delete(m.browserAttentionAcknowledged, projectPath)
	m.browserAttention = &notify
	if m.questionNotify != nil && normalizeProjectPath(m.questionNotify.ProjectPath) == projectPath {
		m.questionNotify = nil
	}
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
		if m.codexVisible() && normalizeProjectPath(m.codexVisibleProject) == normalizeProjectPath(notify.ProjectPath) {
			m.status = notify.Provider.Label() + " browser wait remains available in the Browser sidebar."
			return m, nil
		}
		return m.showCodexProject(notify.ProjectPath, notify.Provider.Label()+" browser needs your attention")
	case "o":
		if notify.canOpenBrowser() {
			return m.openBrowserAttentionLogin(*notify)
		}
		return m, nil
	case "s":
		m.dismissBrowserAttentionNotification()
		if m.codexVisible() && normalizeProjectPath(m.codexVisibleProject) == normalizeProjectPath(notify.ProjectPath) {
			m.status = notify.Provider.Label() + " browser wait remains available in the Browser sidebar."
			return m, nil
		}
		return m.showCodexProject(notify.ProjectPath, notify.Provider.Label()+" browser needs your attention")
	case "b":
		m.dismissBrowserAttentionNotification()
		if m.codexVisible() {
			updated, hideCmd := m.hideCodexSession()
			model := updated.(Model)
			return model, batchCmds(hideCmd, model.openBrowserSettingsMode())
		}
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
	case codexapp.ProviderCodex, codexapp.ProviderOpenCode, codexapp.ProviderLCAgent:
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
		notify.Problem = strings.TrimSpace(model.status)
		model.browserAttention = &notify
		return model, revealCmd
	}
	return model, batchCmds(openCmd, revealCmd)
}

func (m Model) renderBrowserAttentionOverlay(body string, width, height int) string {
	panel := m.renderBrowserAttentionPanel(width)
	panelWidth := lipgloss.Width(panel)
	panelHeight := lipgloss.Height(panel)
	left := max(0, (width-panelWidth)/2)
	top := max(0, (height-panelHeight)/2)
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
	instruction := strings.TrimSpace(notify.AttentionMessage)
	if instruction != "" {
		lines = append(lines, detailSectionStyle.Render("What needs your attention"))
		lines = append(lines, renderWrappedDialogTextLines(detailValueStyle, width, instruction)...)
	} else {
		lines = append(lines, renderWrappedDialogTextLines(
			detailWarningStyle,
			width,
			notify.Activity.Summary(),
		)...)
	}
	if strings.TrimSpace(notify.Problem) != "" {
		lines = append(lines, "")
		lines = append(lines, renderWrappedDialogTextLines(
			detailDangerStyle,
			width,
			notify.Problem,
		)...)
	}
	lines = append(lines, "")
	if notify.canOpenBrowser() {
		handoffCopy := "Little Control Room can reveal the managed browser window or focus it for this same session. The Browser sidebar and ctrl+o remain available after you dismiss this dialog."
		if !m.codexVisible() || normalizeProjectPath(m.codexVisibleProject) != normalizeProjectPath(notify.ProjectPath) {
			handoffCopy = "Little Control Room can reveal the managed browser window or focus it for this same session, then bring the embedded session forward so you can keep an eye on it."
		}
		lines = append(lines, renderWrappedDialogTextLines(
			detailMutedStyle,
			width,
			handoffCopy,
		)...)
		lines = append(lines,
			"",
			renderDialogAction("Enter", m.browserAttentionBrowserActionLabel(*notify), commitActionKeyStyle, commitActionTextStyle),
			renderDialogAction("S", m.browserAttentionSessionActionLabel(*notify), pushActionKeyStyle, pushActionTextStyle),
			renderDialogAction("B", "browser settings", pushActionKeyStyle, pushActionTextStyle),
			renderDialogAction("Esc", "dismiss", cancelActionKeyStyle, cancelActionTextStyle),
		)
	} else {
		sessionCopy := "Open the embedded session to review the request. LCR still does not own the browser window for this flow yet."
		if m.codexVisible() && normalizeProjectPath(m.codexVisibleProject) == normalizeProjectPath(notify.ProjectPath) {
			sessionCopy = "Return to the embedded session to review the request. The Browser sidebar remains available after you dismiss this dialog."
		}
		lines = append(lines, renderWrappedDialogTextLines(
			detailMutedStyle,
			width,
			sessionCopy,
		)...)
		lines = append(lines,
			"",
			renderDialogAction("Enter", m.browserAttentionSessionActionLabel(*notify), commitActionKeyStyle, commitActionTextStyle),
			renderDialogAction("B", "browser settings", pushActionKeyStyle, pushActionTextStyle),
			renderDialogAction("Esc", "dismiss", cancelActionKeyStyle, cancelActionTextStyle),
		)
	}
	return strings.Join(lines, "\n")
}

func (m Model) browserAttentionBrowserActionLabel(notify browserAttentionNotification) string {
	if strings.TrimSpace(notify.Problem) != "" {
		return "retry browser"
	}
	state, ok := m.cachedManagedBrowserState(notify.ManagedBrowserSessionKey)
	if ok && managedBrowserStateFreshForUI(state, m.currentTime()) && !state.Normalize().Hidden {
		return "focus browser"
	}
	return "show browser"
}

func (m Model) browserAttentionSessionActionLabel(notify browserAttentionNotification) string {
	if m.codexVisible() && normalizeProjectPath(m.codexVisibleProject) == normalizeProjectPath(notify.ProjectPath) {
		return "return to session"
	}
	return "open session"
}
