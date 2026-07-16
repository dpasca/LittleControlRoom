package tui

import (
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"lcroom/internal/brand"
	"lcroom/internal/browserctl"
	"lcroom/internal/codexapp"
	"lcroom/internal/codexcli"
	"lcroom/internal/uistyle"
	"math"
	"path/filepath"
	"strings"
	"time"
)

func (m Model) renderCodexView() string {
	done := m.beginUIPhase("renderCodexView", strings.TrimSpace(m.codexVisibleProject), "")
	defer done()
	if projectPath := m.codexPendingOpenProject(); m.codexPendingOpenVisible() && projectPath != "" {
		return m.renderCodexOpeningView(projectPath)
	}
	snapshot, ok := m.currentCodexSnapshot()
	if !ok {
		return lipgloss.NewStyle().Bold(true).Render(brand.FullTitle + " | Embedded session unavailable")
	}

	width := m.width
	if width <= 0 {
		width = 120
	}
	height := m.height
	if height <= 0 {
		height = 30
	}
	body := m.renderCodexSplitView(snapshot, width, height)
	if snapshot.PendingElicitation != nil {
		body = m.renderCodexElicitationDialogOverlay(body, width, height, snapshot)
	}
	return body
}

func (m Model) renderCodexMainView(snapshot codexapp.Snapshot, width, height int) string {
	lowerBlocks := m.codexLowerBlocks(snapshot, width)
	lowerHeight := countRenderedBlockLines(lowerBlocks)
	transcriptHeight := codexTranscriptContentHeight(height, lowerHeight)

	transcript := m.codexViewport
	projectPath := strings.TrimSpace(m.codexVisibleProject)
	transcript.Width = max(24, width)
	transcript.Height = max(1, transcriptHeight)
	switch {
	case m.codexViewportContentMatches(projectPath, transcript.Width):
		maxOffset := max(0, transcript.TotalLineCount()-transcript.Height)
		if transcript.YOffset > maxOffset {
			transcript.SetYOffset(maxOffset)
		}
	case m.codexViewportContentCanStayStale(projectPath, transcript.Width, snapshot):
	case func() bool {
		rendered, ok := m.cachedCodexTranscriptContent(projectPath, transcript.Width)
		if !ok {
			return false
		}
		transcript.SetContent(rendered)
		return true
	}():
	case codexTranscriptCacheMissCanRender(snapshot):
		transcript.SetContent(m.renderCodexTranscriptContentFromSnapshotForProject(projectPath, snapshot, transcript.Width))
	default:
		transcript.SetContent(renderCodexTranscriptCacheMissContent(snapshot))
	}
	viewOutput := transcript.View()
	if m.codexSelection.dragging && m.codexSelection.hasRange() {
		viewOutput = overlaySelectionHighlight(viewOutput, m.codexSelection, transcript.YOffset)
	}
	body := m.renderHFramedPane(viewOutput, width, transcriptHeight, m.codexMainFocused())

	lines := []string{m.renderCodexBanner(snapshot, width), body}
	lines = append(lines, lowerBlocks...)
	return strings.Join(lines, "\n")
}

func codexTranscriptContentHeight(totalHeight, lowerHeight int) int {
	return max(3, totalHeight-lowerHeight-3)
}

func (m Model) renderCodexOpeningView(projectPath string) string {
	width := m.width
	if width <= 0 {
		width = 120
	}
	height := m.height
	if height <= 0 {
		height = 30
	}

	projectName := strings.TrimSpace(filepath.Base(projectPath))
	if projectName == "" || projectName == "." {
		projectName = projectPath
	}
	label := m.codexPendingOpenProvider().Label()
	headline := "Opening embedded " + label + " session..."
	detail := "Waiting for the requested embedded session to come online."
	footerStatus := "Opening embedded " + label + " session"
	if m.codexPendingOpen != nil && m.codexPendingOpen.newSession {
		headline = "Starting a new embedded " + label + " session..."
		detail = "Preparing the new embedded session."
		footerStatus = "Starting a new embedded " + label + " session"
	}
	spinner := spinnerFrames[m.spinnerFrame%len(spinnerFrames)]
	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("81")).Render(label + " | " + projectName)
	bodyHeight := max(3, height-6)
	input := m.codexInput
	input.SetWidth(max(20, width-4))
	input.SetHeight(max(3, min(10, m.codexInput.LineCount()+1)))
	composer := renderCodexComposer(input, width)
	bodyHeight = max(3, bodyHeight-lipgloss.Height(composer))
	body := m.renderHFramedPane(strings.Join([]string{
		lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("81")).Render(spinner + " " + headline),
		"",
		fitFooterWidth("Project: "+projectPath, max(24, width-4)),
		fitFooterWidth(detail, max(24, width-4)),
		fitFooterWidth("Type your draft now; press Enter after the session is ready to send it.", max(24, width-4)),
	}, "\n"), width, bodyHeight, true)
	footer := renderFooterLine(width, renderFooterStatus(spinner+" "+footerStatus))
	return strings.Join([]string{renderFooterLine(width, title), body, composer, footer}, "\n")
}

func (m Model) codexLowerBlocks(snapshot codexapp.Snapshot, width int) []string {
	label := embeddedProvider(snapshot).Label()
	switch {
	case snapshot.PendingApproval != nil:
		approvalActions := []footerAction{
			footerPrimaryAction("a", "accept"),
			footerExitAction("d", "decline"),
			footerLowAction("c", "cancel"),
			footerHideAction("Alt+Up", "hide"),
		}
		if snapshot.PendingApproval.AllowsDecision(codexapp.DecisionAcceptForSession) {
			sessionAction := "session"
			if snapshot.Provider == codexapp.ProviderLCAgent {
				sessionAction = "medium"
			}
			approvalActions = []footerAction{
				footerPrimaryAction("a", "accept"),
				footerNavAction("A", sessionAction),
				footerExitAction("d", "decline"),
				footerLowAction("c", "cancel"),
				footerHideAction("Alt+Up", "hide"),
			}
		}
		return []string{
			fitFooterWidth("Approval: "+snapshot.PendingApproval.Summary(), width),
			renderFooterLine(width, renderFooterActionList(approvalActions...)),
		}
	case snapshot.Closed:
		return []string{
			fitFooterWidth(label+" session closed. Alt+Up hides it; Enter on the project opens a new one.", width),
			"",
		}
	default:
		lines := []string{}
		if snapshot.BusyExternal {
			lines = append(lines, m.renderCodexBusyElsewhereNotice(snapshot, width))
		}
		lines = append(lines, m.renderCodexFooter(snapshot, width))
		lines = append(lines, m.renderCodexRequestBlocks(snapshot, width)...)
		if browser := m.renderCodexBrowserPanel(snapshot, width); browser != "" {
			lines = append(lines, browser)
		}
		lines = append(lines, m.renderCodexSlashBlocks(width)...)
		showComposer := snapshot.PendingElicitation == nil || codexElicitationNeedsComposer(*snapshot.PendingElicitation)
		if showComposer {
			input := m.codexInput
			input.SetWidth(max(20, width-4))
			input.SetHeight(max(3, min(10, m.codexInput.LineCount()+1)))
			if m.codexInputSelectionActive() {
				lines = append(lines, renderCodexComposerWithSelection(input, m.codexInputSelection, width))
			} else if m.codexComposerSelection.dragging && m.codexComposerSelection.hasRange() {
				lines = append(lines, renderCodexComposerWithMouseSelection(input, m.codexComposerSelection, width))
			} else {
				lines = append(lines, renderCodexComposer(input, width))
			}
		}
		return lines
	}
}

func (m Model) renderCodexRequestBlocks(snapshot codexapp.Snapshot, width int) []string {
	switch {
	case snapshot.PendingToolInput != nil:
		return m.renderCodexToolInputBlocks(*snapshot.PendingToolInput, width)
	default:
		return nil
	}
}

func (m Model) renderCodexBrowserPanel(snapshot codexapp.Snapshot, width int) string {
	lines := []string{}
	lines = append(lines, m.codexBrowserReconnectLines(snapshot)...)
	if snapshot.PendingElicitation == nil {
		lines = append(lines, m.renderCodexCurrentBrowserPageBlocks(snapshot, width)...)
	}
	lines = compactNonEmptyStrings(lines)
	if len(lines) == 0 {
		return ""
	}
	accent := lipgloss.Color("81")
	if m.codexBrowserPolicyMismatch(snapshot) {
		accent = lipgloss.Color("221")
	}
	return renderCodexMessageBlock("Browser", strings.Join(lines, "\n"), accent, lipgloss.Color("252"), max(24, width-4))
}

func (m Model) renderCodexToolInputBlocks(request codexapp.ToolInputRequest, width int) []string {
	lines := []string{fitFooterWidth("Structured input: "+request.Summary(), width)}
	if len(request.Questions) == 0 {
		return lines
	}
	state := m.toolAnswerStateFor(m.codexVisibleProject, &request)
	if state.QuestionIndex >= len(request.Questions) {
		state.QuestionIndex = max(0, len(request.Questions)-1)
	}
	question := request.Questions[state.QuestionIndex]
	if len(request.Questions) > 1 {
		lines = append(lines, fitFooterWidth(fmt.Sprintf("Question %d/%d", state.QuestionIndex+1, len(request.Questions)), width))
	}
	if header := strings.TrimSpace(question.Header); header != "" {
		lines = append(lines, fitFooterWidth(header, width))
	}
	prompt := question.Question
	if question.IsSecret {
		prompt += " [secret]"
	}
	lines = append(lines, fitFooterWidth(prompt, width))
	for i, option := range question.Options {
		line := fmt.Sprintf("%d %s", i+1, strings.TrimSpace(option.Label))
		if desc := strings.TrimSpace(option.Description); desc != "" {
			line += " - " + desc
		}
		lines = append(lines, fitFooterWidth(line, width))
	}
	return lines
}

func (m Model) renderCodexCurrentBrowserPageBlocks(snapshot codexapp.Snapshot, width int) []string {
	if snapshot.Closed || snapshot.BusyExternal {
		return nil
	}
	if snapshot.Busy && !codexSnapshotBrowserWaitingForUser(snapshot) {
		return nil
	}
	pageURL := managedBrowserCurrentPageURL(snapshot)
	if pageURL == "" || strings.TrimSpace(snapshot.ManagedBrowserSessionKey) == "" {
		return nil
	}
	if snapshot.CurrentBrowserPageStale {
		// URLs recovered from resumed transcripts are historical context, not live controls.
		return nil
	}
	lines := []string{}
	if codexSnapshotBrowserWaitingForUser(snapshot) {
		activity := snapshot.BrowserActivity.Normalize()
		summary := strings.TrimSpace(activity.AttentionMessage)
		if summary == "" {
			summary = strings.TrimSpace(activity.Summary())
		}
		if summary == "" {
			summary = "Browser is waiting for you."
		}
		lines = append(lines, fitFooterWidth(summary, width))
	}
	lines = append(lines, fitFooterWidth(m.managedBrowserCurrentPageLabel(snapshot)+pageURL, width))
	if hint := m.managedBrowserCurrentPageHint(snapshot); hint != "" {
		lines = append(lines, fitFooterWidth(hint, width))
	}
	return lines
}

func (m Model) codexBrowserPolicyMismatch(snapshot codexapp.Snapshot) bool {
	currentPolicy := m.currentPlaywrightPolicy()
	sessionPolicy := snapshot.BrowserActivity.Policy.Normalize()
	if currentPolicy != sessionPolicy {
		return true
	}
	return managedBrowserFlowSupported(embeddedProvider(snapshot)) &&
		!currentPolicy.UsesLegacyLaunchBehavior() &&
		strings.TrimSpace(snapshot.ManagedBrowserSessionKey) == ""
}

func (m Model) codexBrowserReconnectLines(snapshot codexapp.Snapshot) []string {
	if !m.codexBrowserPolicyMismatch(snapshot) {
		return nil
	}
	currentPolicy := m.currentPlaywrightPolicy()
	currentLabel := settingsBrowserAutomationOptionLabel(settingsBrowserAutomationValue(currentPolicy), currentPolicy)
	newCommand := embeddedNewCommand(embeddedProvider(snapshot))
	if managedBrowserFlowSupported(embeddedProvider(snapshot)) &&
		!currentPolicy.UsesLegacyLaunchBehavior() &&
		strings.TrimSpace(snapshot.ManagedBrowserSessionKey) == "" {
		lines := []string{
			"Managed browser controls are not attached to this session yet.",
			"Current browser setting: " + currentLabel + ".",
		}
		if newCommand != "" {
			lines = append(lines, "Use /reconnect to reopen this thread with the current browser behavior, or "+newCommand+" for a fresh session.")
		} else {
			lines = append(lines, "Use /reconnect to reopen this thread with the current browser behavior.")
		}
		return lines
	}
	sessionPolicy := snapshot.BrowserActivity.Policy.Normalize()
	sessionLabel := settingsBrowserAutomationOptionLabel(settingsBrowserAutomationValue(sessionPolicy), sessionPolicy)
	lines := []string{
		"Session browser setting: " + sessionLabel + ".",
		"Current browser setting: " + currentLabel + ".",
	}
	if newCommand != "" {
		lines = append(lines, "Use /reconnect to reopen this thread with the current browser behavior, or "+newCommand+" for a fresh session.")
	} else {
		lines = append(lines, "Use /reconnect to reopen this thread with the current browser behavior.")
	}
	return lines
}

func (m Model) codexBrowserReconnectStatus(snapshot codexapp.Snapshot) string {
	lines := m.codexBrowserReconnectLines(snapshot)
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, " ")
}

func (m Model) renderCodexBanner(snapshot codexapp.Snapshot, width int) string {
	provider := embeddedProvider(snapshot)
	parts := []string{
		sourceStyle(embeddedSessionFormat(provider), snapshot.Started && !snapshot.Closed).Render(provider.Label()),
	}
	if projectName := strings.TrimSpace(filepath.Base(snapshot.ProjectPath)); projectName != "" && projectName != "." {
		parts = append(parts, codexBannerProjectStyle.Render(projectName))
	}
	if snapshot.BusyExternal {
		parts = append(parts, detailWarningStyle.Render("Read-only"))
	}
	if blockMode := m.codexDenseBlockMode.bannerText(); blockMode != "" {
		parts = append(parts, codexBannerMetaStyle.Render(blockMode))
	}
	title := strings.Join(parts, codexBannerSeparatorStyle.Render(" | "))
	actions := []footerAction{}
	if len(m.cachedCodexOpenTargetsForPicker(snapshot)) > 0 && codexArtifactPickerAllowed(snapshot) {
		actions = append(actions, footerNavAction("Alt+O", "links"))
	}
	actions = append(actions, footerLowAction("Alt+L", "blocks"))
	if m.embeddedCodexSidebarAvailable() {
		label := "sidebar"
		if m.codexPanelFocus == embeddedCodexFocusSidebar {
			label = "session"
		}
		actions = append(actions, footerNavAction("Alt+S", label))
	}
	overlay := codexBannerRightStatus(snapshot)
	contentWidth := width
	if overlay != "" {
		overlay = "  " + overlay
		if overlayWidth := lipgloss.Width(overlay); overlayWidth >= width {
			return ansi.Cut(overlay, max(0, overlayWidth-width), overlayWidth)
		} else if width > 0 {
			contentWidth = width - overlayWidth
		}
	}
	line := renderFooterLine(contentWidth, title, renderFooterActionList(actions...))
	banner := lipgloss.PlaceHorizontal(max(0, contentWidth), lipgloss.Left, line)
	if overlay != "" {
		return banner + overlay
	}
	return banner
}

func codexBannerRightStatus(snapshot codexapp.Snapshot) string {
	if snapshot.Closed {
		return ""
	}
	if embeddedProvider(snapshot) == codexapp.ProviderLCAgent {
		if label := codexSnapshotPermissionLabel(snapshot); label != "" {
			return codexPermissionBadgeStyle(label).Render("PERM " + strings.ToUpper(label))
		}
		return ""
	}
	if snapshot.Preset == codexcli.PresetYolo {
		return detailDangerStyle.Render("YOLO MODE")
	}
	return ""
}

func codexPermissionBadgeStyle(label string) lipgloss.Style {
	switch strings.ToLower(strings.TrimSpace(label)) {
	case "off":
		return detailDangerStyle
	case "medium":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true)
	case "low":
		return detailWarningStyle
	default:
		return detailMutedStyle
	}
}

func overlayCodexBannerRight(base, overlay string, width int) string {
	if overlay == "" {
		return base
	}
	if width <= 0 {
		width = max(lipgloss.Width(base), lipgloss.Width(overlay))
	}
	if lipgloss.Width(base) < width {
		base = lipgloss.PlaceHorizontal(width, lipgloss.Left, base)
	}
	overlayWidth := lipgloss.Width(overlay)
	if overlayWidth >= width {
		return ansi.Cut(overlay, max(0, overlayWidth-width), overlayWidth)
	}
	left := width - overlayWidth
	prefix := ansi.Cut(base, 0, left)
	suffix := ansi.Cut(base, left+overlayWidth, width)
	return prefix + overlay + suffix
}

func (m Model) renderCodexBusyElsewhereNotice(snapshot codexapp.Snapshot, width int) string {
	label := embeddedProvider(snapshot).Label()
	message := strings.TrimSpace(snapshot.LastSystemNotice)
	if message == "" {
		sessionID := shortID(snapshot.ThreadID)
		if sessionID == "" {
			sessionID = "this session"
		}
		message = fmt.Sprintf("Embedded %s session %s is already active in another process, so embedded controls are read-only until it finishes.", label, sessionID)
	}
	return renderCodexMessageBlock("Read-only", message, lipgloss.Color("221"), lipgloss.Color("252"), max(24, width-4))
}

func codexSnapshotPermissionLabel(snapshot codexapp.Snapshot) string {
	if embeddedProvider(snapshot) != codexapp.ProviderLCAgent {
		return ""
	}
	switch strings.ToLower(strings.TrimSpace(snapshot.PermissionLevel)) {
	case "off":
		return "Off"
	case "low":
		return "Low"
	case "medium":
		return "Medium"
	default:
		return ""
	}
}

func codexSnapshotShowsPendingModelAsCurrent(snapshot codexapp.Snapshot) bool {
	if strings.TrimSpace(snapshot.PendingModel) == "" || snapshot.Busy || snapshot.BusyExternal || snapshot.Closed {
		return false
	}
	for _, entry := range snapshot.Entries {
		switch entry.Kind {
		case codexapp.TranscriptSystem, codexapp.TranscriptStatus:
			continue
		default:
			return false
		}
	}
	return true
}

func codexSnapshotTokenUsageLabel(snapshot codexapp.Snapshot) string {
	if snapshot.TokenUsage == nil {
		return ""
	}
	usage := snapshot.TokenUsage.Total
	if !codexTokenUsageHasBreakdown(usage) {
		usage = snapshot.TokenUsage.Last
	}
	if !codexTokenUsageHasBreakdown(usage) {
		return ""
	}
	return uistyle.FormatCompactTokenBreakdown(
		usage.InputTokens,
		usage.OutputTokens,
		usage.CachedInputTokens,
		usage.ReasoningOutputTokens,
	)
}

func codexTokenUsageHasBreakdown(usage codexapp.TokenUsageBreakdown) bool {
	return usage.InputTokens != 0 ||
		usage.OutputTokens != 0 ||
		usage.CachedInputTokens != 0 ||
		usage.ReasoningOutputTokens != 0
}

func codexSnapshotGoalLabel(snapshot codexapp.Snapshot) string {
	if snapshot.Goal == nil {
		return ""
	}
	status := codexGoalStatusLabel(snapshot.Goal.Status)
	if status == "" {
		status = "active"
	}
	if snapshot.Goal.TokenBudget != nil && *snapshot.Goal.TokenBudget > 0 {
		usage := fmt.Sprintf("%s/%s tok", formatInt64(snapshot.Goal.TokensUsed), formatInt64(*snapshot.Goal.TokenBudget))
		return status + " " + usage
	}
	return status
}

func codexGoalStatusLabel(status codexapp.ThreadGoalStatus) string {
	switch status {
	case codexapp.ThreadGoalStatusActive:
		return "active"
	case codexapp.ThreadGoalStatusPaused:
		return "paused"
	case codexapp.ThreadGoalStatusBlocked:
		return "blocked"
	case codexapp.ThreadGoalStatusBudgetLimited:
		return "budget-limited"
	case codexapp.ThreadGoalStatusComplete:
		return "complete"
	default:
		return strings.TrimSpace(string(status))
	}
}

func codexSnapshotGoalCanBeStopped(snapshot codexapp.Snapshot) bool {
	if snapshot.Goal == nil || snapshot.Closed || snapshot.BusyExternal {
		return false
	}
	switch snapshot.Goal.Status {
	case codexapp.ThreadGoalStatusBudgetLimited, codexapp.ThreadGoalStatusComplete:
		return false
	default:
		return true
	}
}

func codexSnapshotGoalPausesOnPrompt(snapshot codexapp.Snapshot) bool {
	if snapshot.Goal == nil || snapshot.Closed || snapshot.BusyExternal || !snapshot.Busy {
		return false
	}
	switch snapshot.Goal.Status {
	case "", codexapp.ThreadGoalStatusActive:
		return true
	default:
		return false
	}
}

func cloneCodexThreadGoal(goal *codexapp.ThreadGoal) *codexapp.ThreadGoal {
	if goal == nil {
		return nil
	}
	cloned := *goal
	if goal.TokenBudget != nil {
		budget := *goal.TokenBudget
		cloned.TokenBudget = &budget
	}
	return &cloned
}

func firstNonEmptyCodexLabel(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func (m Model) renderCodexFooter(snapshot codexapp.Snapshot, width int) string {
	status := renderCodexFooterStatus(snapshot, m.currentTime(), m.spinnerFrame)

	var actions []footerAction
	switch {
	case snapshot.PendingToolInput != nil:
		actions = append(actions,
			footerPrimaryAction("Enter", "answer"),
			footerExitAction("ctrl+c", "close"),
			footerHideAction("Alt+Up", "hide"),
			footerLowAction("Alt+C", "copy menu"),
		)
		state := m.toolAnswerStateFor(m.codexVisibleProject, snapshot.PendingToolInput)
		if state.QuestionIndex >= 0 && state.QuestionIndex < len(snapshot.PendingToolInput.Questions) {
			if len(snapshot.PendingToolInput.Questions[state.QuestionIndex].Options) > 0 {
				actions = append(actions, footerNavAction("1-9", "choose"))
			}
		}
		if len(snapshot.PendingToolInput.Questions) > 1 {
			actions = append(actions, footerNavAction("Tab", "next"))
		}
		if m.managedBrowserCanReveal(snapshot) {
			actions = append(actions, footerNavAction("ctrl+o", m.managedBrowserCurrentPageFooterLabel(snapshot)))
		}
	case snapshot.PendingElicitation != nil && snapshot.PendingElicitation.Mode == codexapp.ElicitationModeForm:
		actions = []footerAction{
			footerPrimaryAction("Enter", "accept"),
			footerExitAction("ctrl+c", "close"),
			footerHideAction("Alt+Up", "hide"),
			footerExitAction("d", "decline"),
			footerLowAction("c", "cancel"),
			footerNavAction("Alt+Enter", "newline"),
			footerLowAction("Alt+C", "copy menu"),
		}
	case snapshot.PendingElicitation != nil &&
		strings.TrimSpace(snapshot.ManagedBrowserSessionKey) != "" &&
		managedBrowserLoginURL(embeddedProvider(snapshot), snapshot.BrowserActivity.Policy, snapshot.PendingElicitation.Mode, snapshot.PendingElicitation.URL) != "":
		actions = []footerAction{
			footerPrimaryAction("O", "show browser"),
			footerPrimaryAction("Enter", "done/accept"),
			footerExitAction("ctrl+c", "close"),
			footerHideAction("Alt+Up", "hide"),
			footerExitAction("d", "decline"),
			footerLowAction("c", "cancel"),
		}
	case snapshot.PendingElicitation != nil:
		actions = []footerAction{
			footerPrimaryAction("Enter", "accept"),
			footerExitAction("ctrl+c", "close"),
			footerHideAction("Alt+Up", "hide"),
			footerExitAction("d", "decline"),
			footerLowAction("c", "cancel"),
		}
	case m.codexSlashActive():
		actions = []footerAction{
			footerPrimaryAction("Enter", "run"),
			footerExitAction("ctrl+c", "close"),
			footerHideAction("Alt+Up", "hide"),
			footerNavAction("Tab", "complete"),
			footerNavAction("Shift+Tab", "previous"),
			footerNavAction("Alt+Enter", "newline"),
			footerLowAction("Alt+C", "copy menu"),
		}
	case snapshot.BusyExternal:
		actions = []footerAction{
			footerHideAction("Alt+Up", "hide"),
		}
		if cmd := embeddedNewCommand(embeddedProvider(snapshot)); cmd != "" {
			actions = append(actions, footerNavAction(cmd, "session"))
		}
	case snapshot.Phase == codexapp.SessionPhaseReconciling:
		actions = []footerAction{
			footerHideAction("Alt+Up", "hide"),
			footerNavAction("/reconnect", "recover"),
			footerNavAction("/sessions", "inspect"),
		}
	case snapshot.Phase == codexapp.SessionPhaseStalled:
		actions = []footerAction{
			footerHideAction("Alt+Up", "hide"),
			footerNavAction("/reconnect", "recover"),
		}
	case snapshot.Phase == codexapp.SessionPhaseFinishing:
		actions = []footerAction{
			footerHideAction("Alt+Up", "hide"),
		}
	case m.codexInputSelectionActive():
		actions = []footerAction{
			footerPrimaryAction("Space", "mark"),
			footerExitAction("Esc", "cancel"),
			footerNavAction("arrows", "move"),
		}
	case codexSnapshotBrowserWaitingForUser(snapshot):
		actions = []footerAction{
			footerPrimaryAction("Enter", "continue"),
		}
		if m.managedBrowserCanReveal(snapshot) {
			actions = append(actions, footerNavAction("ctrl+o", m.managedBrowserCurrentPageFooterLabel(snapshot)))
		}
		actions = append(actions,
			footerExitAction("ctrl+c", "stop"),
			footerHideAction("Alt+Up", "hide"),
			footerHideAction("Esc", "hide"),
			footerNavAction("Alt+Enter", "newline"),
			footerLowAction("Alt+C", "copy menu"),
		)
	case snapshot.Busy:
		enterAction := footerPrimaryAction("Enter", "steer")
		if codexSnapshotGoalPausesOnPrompt(snapshot) {
			enterAction = footerPrimaryAction("Enter", "pause goal")
		} else if codexSnapshotQueuesBusyInput(snapshot) {
			enterAction = footerPrimaryAction("Enter", "queue")
		}
		ctrlCAction := footerExitAction("ctrl+c", "close")
		if codexSnapshotCanInterruptActiveTurn(snapshot) {
			ctrlCAction = footerExitAction("ctrl+c", "interrupt")
		}
		actions = []footerAction{
			enterAction,
			ctrlCAction,
			footerHideAction("Alt+Up", "hide"),
			footerNavAction("Alt+Enter", "newline"),
			footerNavAction("ctrl+v", "image"),
			footerLowAction("Alt+C", "copy menu"),
		}
	case snapshot.Closed:
		actions = []footerAction{
			footerHideAction("Alt+Up", "hide"),
			footerLowAction("PgUp/PgDn", "scroll"),
		}
	default:
		actions = []footerAction{
			footerPrimaryAction("Enter", "send"),
			footerExitAction("ctrl+c", "close"),
			footerHideAction("Alt+Up", "hide"),
			footerNavAction("Alt+Enter", "newline"),
			footerNavAction("ctrl+v", "image"),
			footerLowAction("Alt+C", "copy menu"),
		}
		if m.managedBrowserCanReveal(snapshot) {
			actions = append(actions, footerNavAction("ctrl+o", m.managedBrowserCurrentPageFooterLabel(snapshot)))
		}
	}
	if mismatchStatus := m.codexBrowserReconnectStatus(snapshot); mismatchStatus != "" && !snapshot.Closed && !snapshot.BusyExternal {
		actions = append(actions, footerNavAction("/reconnect", "apply browser"))
		if cmd := embeddedNewCommand(embeddedProvider(snapshot)); cmd != "" {
			actions = append(actions, footerLowAction(cmd, "fresh"))
		}
	}
	if codexSnapshotGoalCanBeStopped(snapshot) && !m.codexSlashActive() {
		actions = append([]footerAction{footerExitAction("/goal clear", "stop goal")}, actions...)
	}
	segments := []string{}
	if composerStatus := m.renderCodexComposerFocusStatus(); composerStatus != "" {
		segments = append(segments, composerStatus)
	}
	if status != "" {
		segments = append(segments, status)
	}
	segments = append(segments, renderFooterActionList(actions...))
	return renderFooterLine(width, segments...)
}

func (m Model) renderCodexComposerFocusStatus() string {
	if strings.TrimSpace(m.codexVisibleProject) == "" {
		return ""
	}
	if m.codexPanelFocus != embeddedCodexFocusMain || !m.codexInput.Focused() {
		return renderFooterMeta("Input off")
	}
	return ""
}

func (m Model) managedBrowserCurrentPageLabel(snapshot codexapp.Snapshot) string {
	if m.managedBrowserCachedVisible(snapshot) {
		return "Managed browser page: "
	}
	return "Background browser page: "
}

func (m Model) managedBrowserCurrentPageHint(snapshot codexapp.Snapshot) string {
	if !m.managedBrowserCanReveal(snapshot) {
		return ""
	}
	if m.managedBrowserCachedVisible(snapshot) {
		return ""
	}
	return "Press ctrl+o to reveal the managed browser window for this same session."
}

func (m Model) managedBrowserCurrentPageFooterLabel(snapshot codexapp.Snapshot) string {
	if m.managedBrowserCachedVisible(snapshot) {
		return "focus browser"
	}
	return "show browser"
}

func (m Model) managedBrowserCanReveal(snapshot codexapp.Snapshot) bool {
	if !managedBrowserRevealTargetAttached(snapshot) {
		return false
	}
	if _, ok := m.freshManagedBrowserState(snapshot); ok {
		return true
	}
	if managedBrowserActivityCanReveal(embeddedProvider(snapshot), snapshot.BrowserActivity) {
		return true
	}
	return m.liveManagedBrowserActivityCanReveal(snapshot)
}

func managedBrowserRevealTargetAttached(snapshot codexapp.Snapshot) bool {
	return strings.TrimSpace(snapshot.ManagedBrowserSessionKey) != "" &&
		!snapshot.BusyExternal &&
		!snapshot.Closed &&
		!snapshot.CurrentBrowserPageStale
}

func managedBrowserActivityCanReveal(provider codexapp.Provider, activity browserctl.SessionActivity) bool {
	if provider.Normalized() != codexapp.ProviderCodex {
		return false
	}
	switch activity.Normalize().State {
	case browserctl.SessionActivityStateActive, browserctl.SessionActivityStateWaitingForUser:
		return true
	default:
		return false
	}
}

func (m Model) liveManagedBrowserActivityCanReveal(snapshot codexapp.Snapshot) bool {
	state, ok := m.attachedManagedBrowserSessionState(snapshot)
	return ok && state.BrowserActivity.Normalize().Live()
}

func (m Model) attachedManagedBrowserSessionState(snapshot codexapp.Snapshot) (codexapp.Snapshot, bool) {
	projectPath := strings.TrimSpace(firstNonEmptyString(snapshot.ProjectPath, m.codexVisibleProject))
	if projectPath == "" {
		return codexapp.Snapshot{}, false
	}
	if normalizeProjectPath(projectPath) != normalizeProjectPath(m.codexVisibleProject) {
		return codexapp.Snapshot{}, false
	}
	session, ok := m.codexSession(projectPath)
	if !ok {
		return codexapp.Snapshot{}, false
	}
	state, ok := stateSnapshotForCodexSession(session)
	if !ok || !managedBrowserRevealTargetAttached(state) {
		return codexapp.Snapshot{}, false
	}
	if strings.TrimSpace(state.ManagedBrowserSessionKey) != strings.TrimSpace(snapshot.ManagedBrowserSessionKey) {
		return codexapp.Snapshot{}, false
	}
	snapshotProvider := embeddedProvider(snapshot)
	stateProvider := embeddedProvider(state)
	if snapshotProvider.Normalized() != "" && stateProvider.Normalized() != "" && snapshotProvider != stateProvider {
		return codexapp.Snapshot{}, false
	}
	return state, true
}

func (m Model) managedBrowserCachedVisible(snapshot codexapp.Snapshot) bool {
	state, ok := m.freshManagedBrowserState(snapshot)
	return ok && !state.Normalize().Hidden
}

func (m Model) freshManagedBrowserState(snapshot codexapp.Snapshot) (browserctl.ManagedPlaywrightState, bool) {
	state, ok := m.cachedManagedBrowserState(snapshot.ManagedBrowserSessionKey)
	if !ok || !managedBrowserStateFreshForUI(state, m.currentTime()) {
		return browserctl.ManagedPlaywrightState{}, false
	}
	return state.Normalize(), true
}

func (m *Model) maybeReadManagedBrowserStateCmd(snapshot codexapp.Snapshot) tea.Cmd {
	sessionKey := strings.TrimSpace(snapshot.ManagedBrowserSessionKey)
	if sessionKey == "" || snapshot.BusyExternal || snapshot.Closed {
		return nil
	}
	now := m.currentTime()
	if state, ok := m.cachedManagedBrowserState(sessionKey); ok &&
		managedBrowserStateFreshForUI(state, now) &&
		m.managedBrowserStateRecentlyFetched(sessionKey, now) {
		return nil
	}
	if m.managedBrowserAvailability[sessionKey] == managedBrowserAvailabilityGone &&
		m.managedBrowserStateRecentlyFetched(sessionKey, now) {
		return nil
	}
	if !m.beginManagedBrowserStateRead(sessionKey) {
		return nil
	}
	retryAttempts := 0
	if managedBrowserStateHydrationShouldRetry(snapshot) {
		retryAttempts = managedBrowserStateHydrationRetryAttempts
	}
	return m.readManagedBrowserStateCmd(sessionKey, retryAttempts)
}

func (m *Model) maybeRefreshVisibleManagedBrowserStateCmd() tea.Cmd {
	snapshot, ok := m.currentCachedCodexSnapshot()
	if !ok || !managedBrowserRevealTargetAttached(snapshot) || !managedBrowserFlowSupported(embeddedProvider(snapshot)) {
		return nil
	}
	return m.maybeReadManagedBrowserStateCmd(snapshot)
}

func managedBrowserStateHydrationShouldRetry(snapshot codexapp.Snapshot) bool {
	if !managedBrowserRevealTargetAttached(snapshot) {
		return false
	}
	provider := embeddedProvider(snapshot)
	if !managedBrowserFlowSupported(provider) {
		return false
	}
	if snapshot.BrowserActivity.Normalize().Live() {
		return true
	}
	if request := snapshot.PendingElicitation; request != nil &&
		managedBrowserLoginURL(provider, snapshot.BrowserActivity.Policy, request.Mode, request.URL) != "" {
		return true
	}
	if provider == codexapp.ProviderLCAgent {
		return false
	}
	return managedBrowserCurrentPageURL(snapshot) != ""
}

func compactNonEmptyStrings(lines []string) []string {
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		out = append(out, line)
	}
	return out
}

var (
	codexFinishingFooterPalette   = []lipgloss.Color{lipgloss.Color("214"), lipgloss.Color("220"), lipgloss.Color("229"), lipgloss.Color("221")}
	codexReconcilingFooterPalette = []lipgloss.Color{lipgloss.Color("220"), lipgloss.Color("229"), lipgloss.Color("214")}
)

const codexBusyGradientLoopFrames = 25.0

func renderCodexFooterStatus(snapshot codexapp.Snapshot, now time.Time, spinnerFrame int) string {
	status := codexFooterStatus(snapshot, now)
	switch {
	case status == "Working", status == "Working elsewhere", strings.HasPrefix(status, "Working "):
		return renderCodexAnimatedBusyFooterStatus(status, spinnerFrame)
	case strings.HasPrefix(status, "Finishing "):
		timer := strings.TrimPrefix(status, "Finishing ")
		return renderCodexAnimatedFooterLabel("Finishing", spinnerFrame, codexFinishingFooterPalette) + " " +
			renderCodexAnimatedFooterTimer(timer, spinnerFrame, lipgloss.Color("221"))
	case status == "Finishing":
		return renderCodexAnimatedFooterLabel("Finishing", spinnerFrame, codexFinishingFooterPalette)
	case strings.HasPrefix(status, "Rechecking turn"):
		return renderCodexAnimatedFooterText(status, spinnerFrame, codexReconcilingFooterPalette)
	default:
		return renderFooterStatus(status)
	}
}

// Render a wrapped grayscale wave across the full busy label so the gradient
// stays continuous from the end of the phrase back to the beginning.
func renderCodexAnimatedBusyFooterStatus(text string, spinnerFrame int) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	runes := []rune(text)
	if len(runes) == 1 {
		return lipgloss.NewStyle().Bold(true).Foreground(renderCodexBusyGrayColor(228)).Render(text)
	}

	phase := codexBusyGradientPhase(spinnerFrame)
	count := float64(len(runes))
	var out strings.Builder
	for i, r := range runes {
		position := (float64(i) + 0.5) / count
		gray := codexBusyGradientGrayLevel(position, phase)
		out.WriteString(lipgloss.NewStyle().Bold(true).Foreground(renderCodexBusyGrayColor(gray)).Render(string(r)))
	}
	return out.String()
}

func codexBusyGradientPhase(spinnerFrame int) float64 {
	phase := math.Mod(float64(spinnerFrame)/codexBusyGradientLoopFrames, 1.0)
	if phase < 0 {
		phase += 1
	}
	return phase
}

func codexBusyGradientGrayLevel(position, phase float64) int {
	position = math.Mod(position, 1)
	if position < 0 {
		position += 1
	}
	theta := 2 * math.Pi * (position - phase)
	wave := 0.5 + 0.5*math.Cos(theta)
	contrast := math.Pow(wave, 0.92)
	gray := 124 + contrast*(244-124)
	return int(math.Round(gray))
}

func renderCodexBusyGrayColor(level int) lipgloss.Color {
	if level < 0 {
		level = 0
	}
	if level > 255 {
		level = 255
	}
	return lipgloss.Color(fmt.Sprintf("#%02x%02x%02x", level, level, level))
}

func renderCodexAnimatedFooterLabel(label string, spinnerFrame int, palette []lipgloss.Color) string {
	label = strings.TrimSpace(label)
	if label == "" {
		return ""
	}
	if len(palette) == 0 {
		return renderFooterStatus(label)
	}
	shift := (spinnerFrame / 3) % len(palette)
	var out strings.Builder
	for i, r := range label {
		style := lipgloss.NewStyle().Bold(true).Foreground(palette[(i+shift)%len(palette)])
		out.WriteString(style.Render(string(r)))
	}
	return out.String()
}

func renderCodexAnimatedFooterText(text string, spinnerFrame int, palette []lipgloss.Color) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	if len(palette) == 0 {
		return renderFooterStatus(text)
	}
	color := palette[(spinnerFrame/4)%len(palette)]
	return lipgloss.NewStyle().Bold(true).Foreground(color).Render(text)
}

func renderCodexAnimatedFooterTimer(text string, spinnerFrame int, accent lipgloss.Color) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	palette := []lipgloss.Color{accent, lipgloss.Color("252"), accent, lipgloss.Color("153")}
	return lipgloss.NewStyle().Bold(true).Foreground(palette[(spinnerFrame/4)%len(palette)]).Render(text)
}

func (m Model) renderCodexTranscriptContent(width int) string {
	snapshot, ok := m.currentCodexSnapshot()
	if !ok {
		return "Embedded session unavailable"
	}
	return m.renderCodexTranscriptContentFromSnapshotForProject(m.codexVisibleProject, snapshot, width)
}

func (m Model) renderCodexTranscriptContentFromSnapshot(snapshot codexapp.Snapshot, width int) string {
	return m.renderCodexTranscriptContentFromSnapshotForProject("", snapshot, width)
}

func (m Model) renderCodexTranscriptContentFromSnapshotForProject(projectPath string, snapshot codexapp.Snapshot, width int) string {
	rendered, _ := m.renderCodexTranscriptContentFromSnapshotWithLinksForProject(projectPath, snapshot, width)
	return rendered
}

func (m Model) renderCodexTranscriptContentFromSnapshotWithLinks(snapshot codexapp.Snapshot, width int) (string, []codexTranscriptLinkSpan) {
	return m.renderCodexTranscriptContentFromSnapshotWithLinksForProject("", snapshot, width)
}

func (m Model) renderCodexTranscriptContentFromSnapshotWithLinksForProject(projectPath string, snapshot codexapp.Snapshot, width int) (string, []codexTranscriptLinkSpan) {
	projectPath = strings.TrimSpace(firstNonEmptyString(projectPath, snapshot.ProjectPath))
	return renderCodexTranscriptContentFromSnapshotWithLinksForProjectOptions(snapshot, width, m.codexTranscriptRenderOptionsFor(projectPath))
}

func renderCodexTranscriptContentFromSnapshotWithLinksForProjectOptions(snapshot codexapp.Snapshot, width int, options codexTranscriptRenderOptions) (string, []codexTranscriptLinkSpan) {
	if strings.TrimSpace(options.projectPath) == "" {
		options.projectPath = strings.TrimSpace(snapshot.ProjectPath)
	}
	if !options.blockModeSet {
		options.blockMode = codexDenseBlockSummary
		options.blockModeSet = true
	}
	if rendered, links := renderCodexTranscriptEntriesWithLinksConfigured(snapshot, width, options); strings.TrimSpace(rendered) != "" {
		return rendered, links
	}
	if snapshot.Closed {
		return embeddedProvider(snapshot).Label() + " session closed.", nil
	}
	if notice := strings.TrimSpace(snapshot.LastSystemNotice); notice != "" {
		return "[system] " + sanitizeCodexRenderedText(notice), nil
	}
	return "Type a prompt and press Enter.", nil
}

func renderCodexTranscriptCacheMissContent(snapshot codexapp.Snapshot) string {
	if snapshot.Closed {
		return embeddedProvider(snapshot).Label() + " session closed."
	}
	if len(snapshot.Entries) == 0 && strings.TrimSpace(snapshot.Transcript) == "" {
		if notice := strings.TrimSpace(snapshot.LastSystemNotice); notice != "" {
			return "[system] " + sanitizeCodexRenderedText(notice)
		}
		return "Type a prompt and press Enter."
	}
	return "Transcript is updating..."
}

func codexTranscriptCacheMissCanRender(snapshot codexapp.Snapshot) bool {
	if len(snapshot.Entries) > 0 {
		if len(snapshot.Entries) > codexCacheMissEntryLimit {
			return false
		}
		lines := 0
		bytes := 0
		for _, entry := range snapshot.Entries {
			bytes += codexTranscriptEntryApproxByteCount(entry)
			if bytes > codexCacheMissByteLimit {
				return false
			}
			lines += codexTranscriptEntryApproxLineCount(entry)
			if lines > codexCacheMissLineLimit {
				return false
			}
		}
		return true
	}
	transcript := strings.TrimSpace(snapshot.Transcript)
	if transcript == "" {
		return true
	}
	if len(transcript) > codexCacheMissByteLimit {
		return false
	}
	return transcriptApproxLineCount(transcript) <= codexCacheMissLineLimit
}
