package tui

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"lcroom/internal/codexapp"
	"lcroom/internal/model"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const (
	suspendedTurnDisplayLimit       = 8
	suspendedTurnContinuationPrompt = "Continue the work from the turn interrupted by the Little Control Room restart. First re-check the repository and any external tool state, then finish the user's most recent request. Do not repeat side effects that may already have completed; verify their current state before acting."
)

type suspendedTurnResumeDialogSelection int

const (
	suspendedTurnResumeSelectionResume suspendedTurnResumeDialogSelection = iota
	suspendedTurnResumeSelectionSkip
)

type suspendedTurnResumeChoicesMsg struct {
	choices []suspendedTurnResumeChoice
	err     error
}

type restartIntentsAcknowledgedMsg struct {
	err error
}

type suspendedTurnResumeDialogState struct {
	Choices  []suspendedTurnResumeChoice
	Selected suspendedTurnResumeDialogSelection
}

type suspendedTurnResumeChoice struct {
	ProjectPath    string
	ProjectName    string
	Provider       codexapp.Provider
	SessionID      string
	ActiveTurnID   string
	LastActivity   time.Time
	Summary        string
	CapturedOnQuit bool
}

type restartWarmupEntry struct {
	ProjectPath    string
	ProjectName    string
	Provider       codexapp.Provider
	CapturedOnQuit bool
}

type restartWarmupState struct {
	Total         int
	Succeeded     int
	Failed        int
	FailedSaved   int
	PendingByPath map[string]restartWarmupEntry
}

func (m *Model) beginRestartWarmup(entries []restartWarmupEntry) {
	pending := make(map[string]restartWarmupEntry, len(entries))
	for _, entry := range entries {
		path := normalizeProjectPath(entry.ProjectPath)
		provider := entry.Provider.Normalized()
		if path == "" || provider == "" {
			continue
		}
		entry.ProjectPath = strings.TrimSpace(entry.ProjectPath)
		entry.Provider = provider
		pending[path] = entry
	}
	if len(pending) == 0 {
		m.restartWarmup = nil
		return
	}
	m.restartWarmup = &restartWarmupState{
		Total:         len(pending),
		PendingByPath: pending,
	}
}

func (m Model) restartWarmupForProject(projectPath string) (restartWarmupEntry, bool) {
	if m.restartWarmup == nil {
		return restartWarmupEntry{}, false
	}
	entry, ok := m.restartWarmup.PendingByPath[normalizeProjectPath(projectPath)]
	return entry, ok
}

func (m *Model) settleRestartWarmup(projectPath string, succeeded bool) {
	if m.restartWarmup == nil {
		return
	}
	path := normalizeProjectPath(projectPath)
	entry, ok := m.restartWarmup.PendingByPath[path]
	if !ok {
		return
	}
	delete(m.restartWarmup.PendingByPath, path)
	if succeeded {
		m.restartWarmup.Succeeded++
	} else {
		m.restartWarmup.Failed++
		if entry.CapturedOnQuit {
			m.restartWarmup.FailedSaved++
		}
	}
	if len(m.restartWarmup.PendingByPath) > 0 {
		return
	}

	succeededCount := m.restartWarmup.Succeeded
	failedCount := m.restartWarmup.Failed
	failedSaved := m.restartWarmup.FailedSaved
	m.restartWarmup = nil
	if failedCount == 0 {
		m.status = fmt.Sprintf("Restart recovery complete: restored %d engineer %s.", succeededCount, pluralize("session", succeededCount))
		return
	}
	m.status = fmt.Sprintf("Restart recovery finished: %d restored; attention needed for %d. Review the error log before opening them manually.", succeededCount, failedCount)
	if failedSaved > 0 {
		m.status += " Saved continuations remain available for retry."
	}
}

func (m Model) renderRestartWarmupNotice() string {
	if m.restartWarmup == nil || m.restartWarmup.Total <= 0 {
		return ""
	}
	settled := m.restartWarmup.Total - len(m.restartWarmup.PendingByPath)
	badge := topStatusWarningBadgeStyle.Render(fmt.Sprintf("RESTART %d/%d", settled, m.restartWarmup.Total))
	detail := detailWarningStyle.Render("warming up engineer sessions one at a time; wait before opening manually")
	return joinFooterSegments(badge, detail)
}

func (m Model) loadSuspendedTurnResumeChoicesCmd() tea.Cmd {
	ctx := m.ctx
	svc := m.svc
	dataDir := m.appDataDir()
	return func() tea.Msg {
		intents, intentErr := codexapp.ReadRestartIntents(dataDir)
		if len(intents) == 0 || svc == nil || svc.Store() == nil {
			return suspendedTurnResumeChoicesMsg{
				choices: buildRestartIntentResumeChoices(nil, intents),
				err:     intentErr,
			}
		}
		projects, err := svc.Store().ListProjects(ctx, true)
		if err != nil {
			return suspendedTurnResumeChoicesMsg{
				choices: buildRestartIntentResumeChoices(nil, intents),
				err:     errors.Join(intentErr, err),
			}
		}
		return suspendedTurnResumeChoicesMsg{
			choices: buildRestartIntentResumeChoices(projects, intents),
			err:     intentErr,
		}
	}
}

func (m Model) applySuspendedTurnResumeChoicesMsg(msg suspendedTurnResumeChoicesMsg) (tea.Model, tea.Cmd) {
	if m.suspendedTurnChecked {
		return m, nil
	}
	m.suspendedTurnChecked = true
	if msg.err != nil {
		m.appendBackgroundErrorLogEntry("Interrupted turn check failed", msg.err, "")
	}
	m.openSuspendedTurnResumeDialog(msg.choices)
	return m, nil
}

func (m *Model) openSuspendedTurnResumeDialog(choices []suspendedTurnResumeChoice) bool {
	if len(choices) == 0 {
		return false
	}
	m.suspendedTurnChecked = true
	m.suspendedTurnDialog = &suspendedTurnResumeDialogState{
		Choices:  append([]suspendedTurnResumeChoice(nil), choices...),
		Selected: suspendedTurnResumeSelectionResume,
	}
	m.status = fmt.Sprintf("LCR saved %d interrupted %s for restart recovery", len(choices), pluralize("turn", len(choices)))
	return true
}

func buildRestartIntentResumeChoices(projects []model.ProjectSummary, intents []codexapp.RestartIntent) []suspendedTurnResumeChoice {
	projectByPath := make(map[string]model.ProjectSummary, len(projects))
	for _, project := range projects {
		projectByPath[normalizeProjectPath(project.Path)] = project
	}

	captured := make([]suspendedTurnResumeChoice, 0, len(intents))
	seen := make(map[string]struct{}, len(intents))
	for _, intent := range intents {
		key := intent.Key()
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		project := projectByPath[normalizeProjectPath(intent.ProjectPath)]
		name := strings.TrimSpace(project.Name)
		if name == "" {
			name = projectNameForPicker(project, intent.ProjectPath)
		}
		activity := intent.CapturedAt
		if !project.LatestSessionLastEventAt.IsZero() {
			activity = project.LatestSessionLastEventAt
		}
		captured = append(captured, suspendedTurnResumeChoice{
			ProjectPath:    strings.TrimSpace(intent.ProjectPath),
			ProjectName:    name,
			Provider:       intent.Provider.Normalized(),
			SessionID:      strings.TrimSpace(intent.SessionID),
			ActiveTurnID:   strings.TrimSpace(intent.ActiveTurnID),
			LastActivity:   activity,
			Summary:        strings.TrimSpace(project.LatestSessionSummary),
			CapturedOnQuit: true,
		})
		seen[key] = struct{}{}
	}
	sort.SliceStable(captured, func(i, j int) bool {
		return captured[i].LastActivity.After(captured[j].LastActivity)
	})
	return captured
}

func (m Model) updateSuspendedTurnResumeDialogMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	dialog := m.suspendedTurnDialog
	if dialog == nil {
		return m, nil
	}
	switch msg.String() {
	case "esc", "q":
		m.dismissSuspendedTurnChoices(dialog.Choices)
		m.suspendedTurnDialog = nil
		m.status = "Skipped interrupted turn resume"
		return m, nil
	case "left", "h", "right", "l", "tab", "shift+tab":
		if dialog.Selected == suspendedTurnResumeSelectionResume {
			dialog.Selected = suspendedTurnResumeSelectionSkip
		} else {
			dialog.Selected = suspendedTurnResumeSelectionResume
		}
		return m, nil
	case "enter":
		choices := append([]suspendedTurnResumeChoice(nil), dialog.Choices...)
		selected := dialog.Selected
		m.suspendedTurnDialog = nil
		if selected == suspendedTurnResumeSelectionSkip {
			m.dismissSuspendedTurnChoices(choices)
			m.status = "Skipped interrupted turn resume"
			return m, nil
		}
		return m.resumeSuspendedTurnChoices(choices)
	case "ctrl+c":
		m.suspendedTurnDialog = nil
		return m.updateNormalMode(msg)
	}
	return m, nil
}

func (m Model) resumeSuspendedTurnChoices(choices []suspendedTurnResumeChoice) (tea.Model, tea.Cmd) {
	cmds := make([]tea.Cmd, 0, len(choices))
	warmupEntries := make([]restartWarmupEntry, 0, len(choices))
	resumed := 0
	for _, choice := range choices {
		if strings.TrimSpace(choice.ProjectPath) == "" || strings.TrimSpace(choice.SessionID) == "" || choice.Provider.Normalized() == "" {
			continue
		}
		project := model.ProjectSummary{
			Path:          choice.ProjectPath,
			Name:          choice.ProjectName,
			PresentOnDisk: true,
		}
		updated, cmd := m.launchEmbeddedForProjectWithOptions(project, choice.Provider, embeddedLaunchOptions{
			reveal:                  false,
			resumeID:                choice.SessionID,
			prompt:                  suspendedTurnContinuationPromptForChoice(choice),
			continueInterruptedTurn: choice.CapturedOnQuit,
			interruptedTurnID:       choice.ActiveTurnID,
			restartWarmup:           true,
		})
		m = normalizeUpdateModel(updated)
		if cmd == nil {
			continue
		}
		cmds = append(cmds, cmd)
		warmupEntries = append(warmupEntries, restartWarmupEntry{
			ProjectPath:    choice.ProjectPath,
			ProjectName:    choice.ProjectName,
			Provider:       choice.Provider,
			CapturedOnQuit: choice.CapturedOnQuit,
		})
		resumed++
	}
	if resumed == 0 {
		m.status = "No interrupted turns were resumable"
		return m, nil
	}
	m.beginRestartWarmup(warmupEntries)
	m.status = fmt.Sprintf("Restart recovery is warming up %d engineer %s one at a time. Please wait before opening them manually.", resumed, pluralize("session", resumed))
	// Each provider helper can initialize shared credentials, state databases,
	// and MCP services while reopening a session. Starting every saved helper at
	// once creates a thundering herd and can exhaust the app-server startup RPC
	// window. Keep the work off the UI thread, but open the sessions in order.
	return m, tea.Sequence(cmds...)
}

func suspendedTurnContinuationPromptForChoice(choice suspendedTurnResumeChoice) string {
	if !choice.CapturedOnQuit {
		return ""
	}
	return suspendedTurnContinuationPrompt
}

func (m Model) acknowledgeRestartIntentsCmd(keys []string) tea.Cmd {
	if len(keys) == 0 {
		return nil
	}
	dataDir := m.appDataDir()
	keys = append([]string(nil), keys...)
	return func() tea.Msg {
		return restartIntentsAcknowledgedMsg{
			err: codexapp.AcknowledgeRestartIntents(dataDir, keys),
		}
	}
}

func (m *Model) dismissSuspendedTurnChoices(choices []suspendedTurnResumeChoice) {
	if len(choices) == 0 {
		return
	}
	if m.dismissedSuspendedTurns == nil {
		m.dismissedSuspendedTurns = make(map[string]struct{})
	}
	for _, choice := range choices {
		key := suspendedTurnDismissalKey(choice.ProjectPath, choice.Provider, choice.SessionID)
		if key == "" {
			continue
		}
		m.dismissedSuspendedTurns[key] = struct{}{}
	}
}

func (m *Model) clearDismissedSuspendedTurn(projectPath string, provider codexapp.Provider, sessionID string) {
	key := suspendedTurnDismissalKey(projectPath, provider, sessionID)
	if key == "" || m.dismissedSuspendedTurns == nil {
		return
	}
	delete(m.dismissedSuspendedTurns, key)
}

func (m Model) suspendedTurnDismissed(project model.ProjectSummary) bool {
	key := suspendedTurnDismissalKeyForProject(project)
	if key == "" || m.dismissedSuspendedTurns == nil {
		return false
	}
	_, ok := m.dismissedSuspendedTurns[key]
	return ok
}

func suspendedTurnDismissalKeyForProject(project model.ProjectSummary) string {
	provider := providerForSessionFormat(project.LatestSessionFormat)
	if provider == "" {
		provider = codexProviderFromSessionSource(project.LatestSessionSource)
	}
	return suspendedTurnDismissalKey(project.Path, provider, project.ExternalLatestSessionID())
}

func suspendedTurnDismissalKey(projectPath string, provider codexapp.Provider, sessionID string) string {
	projectPath = strings.TrimSpace(projectPath)
	sessionID = strings.TrimSpace(sessionID)
	provider = provider.Normalized()
	if projectPath == "" || provider == "" || sessionID == "" {
		return ""
	}
	return projectPath + "\x00" + string(provider) + "\x00" + sessionID
}

func (m Model) renderSuspendedTurnResumeDialogOverlay(body string, bodyW, bodyH int) string {
	panel := m.renderSuspendedTurnResumeDialogPanel(bodyW)
	panelW := lipgloss.Width(panel)
	panelH := lipgloss.Height(panel)
	left := max(0, (bodyW-panelW)/2)
	top := max(0, (bodyH-panelH)/4)
	return overlayBlock(body, panel, bodyW, bodyH, left, top)
}

func (m Model) renderSuspendedTurnResumeDialogPanel(bodyW int) string {
	panelW := min(bodyW, min(max(62, bodyW-18), 92))
	panelInnerW := max(32, panelW-4)
	return lipgloss.NewStyle().
		Width(panelW).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("178")).
		Padding(0, 1).
		Background(dialogPanelBackground).
		Foreground(lipgloss.Color("252")).
		Render(fillDialogBlock(m.renderSuspendedTurnResumeDialogContent(panelInnerW), panelInnerW))
}

func (m Model) renderSuspendedTurnResumeDialogContent(width int) string {
	dialog := m.suspendedTurnDialog
	if dialog == nil {
		return ""
	}
	count := len(dialog.Choices)
	lines := []string{
		renderDialogHeader("Interrupted Turns", "", "", width),
		"",
	}
	intro := fmt.Sprintf("LCR saved %d in-flight engineer %s before quitting. Continue them in the background now?", count, pluralize("session", count))
	lines = append(lines, renderWrappedDialogTextLines(detailWarningStyle, width, intro)...)
	lines = append(lines, "")
	note := "Each saved turn reopens its exact conversation and starts a new continuation turn. LCR only shows sessions recorded in its graceful-shutdown journal; a generic unfinished provider artifact is not enough. After confirmation, the top bar and Agent column show warmup progress. Helpers open one at a time; wait for recovery to finish before opening a saved session manually."
	lines = append(lines, renderWrappedDialogTextLines(commandPaletteHintStyle, width, note)...)
	lines = append(lines, "")
	visibleChoices := dialog.Choices
	if len(visibleChoices) > suspendedTurnDisplayLimit {
		visibleChoices = visibleChoices[:suspendedTurnDisplayLimit]
	}
	for _, choice := range visibleChoices {
		lines = append(lines, renderSuspendedTurnChoiceLine(choice, width))
		if summary := strings.TrimSpace(choice.Summary); summary != "" {
			lines = append(lines, "  "+detailMutedStyle.Render(truncateText(summary, max(12, width-2))))
		}
	}
	if hidden := len(dialog.Choices) - len(visibleChoices); hidden > 0 {
		lines = append(lines, detailMutedStyle.Render(fmt.Sprintf("  + %d more saved %s", hidden, pluralize("session", hidden))))
	}
	buttons := lipgloss.JoinHorizontal(
		lipgloss.Left,
		renderDialogButton("Continue All", dialog.Selected == suspendedTurnResumeSelectionResume),
		" ",
		renderDialogButton("Skip", dialog.Selected == suspendedTurnResumeSelectionSkip),
	)
	lines = append(lines, "", buttons)
	lines = append(lines,
		renderDialogAction("Tab", "switch", navigateActionKeyStyle, navigateActionTextStyle),
		renderDialogAction("Enter", "use highlighted", commitActionKeyStyle, commitActionTextStyle),
		renderDialogAction("Esc", "skip", cancelActionKeyStyle, cancelActionTextStyle),
	)
	return strings.Join(lines, "\n")
}

func renderSuspendedTurnChoiceLine(choice suspendedTurnResumeChoice, width int) string {
	left := fmt.Sprintf("- %s  %s", choice.Provider.Label(), firstNonEmptyTrimmed(choice.ProjectName, choice.ProjectPath))
	if choice.CapturedOnQuit {
		left += "  saved"
	}
	right := fmt.Sprintf("%s  %s", formatPickerActivity(choice.LastActivity), shortID(choice.SessionID))
	if width <= 0 {
		return left + "  " + right
	}
	rightW := lipgloss.Width(right)
	leftW := max(12, width-rightW-2)
	return detailValueStyle.Render(truncateText(left, leftW)) + "  " + detailMutedStyle.Render(truncateText(right, max(8, rightW)))
}

func pluralize(word string, count int) string {
	if count == 1 {
		return word
	}
	return word + "s"
}
