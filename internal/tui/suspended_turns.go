package tui

import (
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
	suspendedTurnResumeChoiceLimit = 8
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

type suspendedTurnResumeDialogState struct {
	Choices  []suspendedTurnResumeChoice
	Selected suspendedTurnResumeDialogSelection
}

type suspendedTurnResumeChoice struct {
	ProjectPath  string
	ProjectName  string
	Provider     codexapp.Provider
	SessionID    string
	LastActivity time.Time
	Summary      string
}

func (m Model) loadSuspendedTurnResumeChoicesCmd() tea.Cmd {
	ctx := m.ctx
	svc := m.svc
	return func() tea.Msg {
		if svc == nil || svc.Store() == nil {
			return suspendedTurnResumeChoicesMsg{}
		}
		projects, err := svc.Store().ListProjects(ctx, true)
		if err != nil {
			return suspendedTurnResumeChoicesMsg{err: err}
		}
		return suspendedTurnResumeChoicesMsg{
			choices: buildSuspendedTurnResumeChoices(projects, suspendedTurnResumeChoiceLimit),
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
		return m, nil
	}
	if len(msg.choices) == 0 {
		return m, nil
	}
	m.suspendedTurnDialog = &suspendedTurnResumeDialogState{
		Choices:  append([]suspendedTurnResumeChoice(nil), msg.choices...),
		Selected: suspendedTurnResumeSelectionResume,
	}
	m.status = fmt.Sprintf("Found %d interrupted %s from before reload", len(msg.choices), pluralize("turn", len(msg.choices)))
	return m, nil
}

func buildSuspendedTurnResumeChoices(projects []model.ProjectSummary, limit int) []suspendedTurnResumeChoice {
	choices := make([]suspendedTurnResumeChoice, 0)
	seen := map[string]struct{}{}
	for _, project := range projects {
		if !project.PresentOnDisk || projectSummaryArchived(project) {
			continue
		}
		if !project.LatestTurnStateKnown || project.LatestTurnCompleted {
			continue
		}
		provider := providerForSessionFormat(project.LatestSessionFormat)
		if provider == "" {
			provider = codexProviderFromSessionSource(project.LatestSessionSource)
		}
		provider = provider.Normalized()
		if provider == "" {
			continue
		}
		sessionID := strings.TrimSpace(project.ExternalLatestSessionID())
		if sessionID == "" {
			continue
		}
		key := strings.TrimSpace(project.Path) + "\x00" + string(provider) + "\x00" + sessionID
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		lastActivity := project.LatestSessionLastEventAt
		if lastActivity.IsZero() {
			lastActivity = project.LastActivity
		}
		choices = append(choices, suspendedTurnResumeChoice{
			ProjectPath:  strings.TrimSpace(project.Path),
			ProjectName:  projectNameForPicker(project, project.Path),
			Provider:     provider,
			SessionID:    sessionID,
			LastActivity: lastActivity,
			Summary:      strings.TrimSpace(project.LatestSessionSummary),
		})
	}
	sort.SliceStable(choices, func(i, j int) bool {
		left := choices[i].LastActivity
		right := choices[j].LastActivity
		switch {
		case left.Equal(right):
			return choices[i].ProjectPath < choices[j].ProjectPath
		case left.IsZero():
			return false
		case right.IsZero():
			return true
		default:
			return left.After(right)
		}
	})
	if limit > 0 && len(choices) > limit {
		choices = choices[:limit]
	}
	return choices
}

func (m Model) updateSuspendedTurnResumeDialogMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	dialog := m.suspendedTurnDialog
	if dialog == nil {
		return m, nil
	}
	switch msg.String() {
	case "esc", "q":
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
			reveal:   false,
			resumeID: choice.SessionID,
		})
		m = normalizeUpdateModel(updated)
		if cmd == nil {
			continue
		}
		cmds = append(cmds, cmd)
		resumed++
	}
	if resumed == 0 {
		m.status = "No interrupted turns were resumable"
		return m, nil
	}
	m.status = fmt.Sprintf("Resuming %d interrupted %s in the background...", resumed, pluralize("turn", resumed))
	return m, tea.Batch(cmds...)
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
	intro := fmt.Sprintf("Found %d engineer %s whose latest turn was still open when LCR restarted. Resume them in the background now?", count, pluralize("session", count))
	lines = append(lines, renderWrappedDialogTextLines(detailWarningStyle, width, intro)...)
	lines = append(lines, "")
	note := "Codex can continue or reattach active turns. OpenCode and Claude Code reattach when their CLI is still alive. LCAgent opens saved continuation context."
	lines = append(lines, renderWrappedDialogTextLines(commandPaletteHintStyle, width, note)...)
	lines = append(lines, "")
	for _, choice := range dialog.Choices {
		lines = append(lines, renderSuspendedTurnChoiceLine(choice, width))
		if summary := strings.TrimSpace(choice.Summary); summary != "" {
			lines = append(lines, "  "+detailMutedStyle.Render(truncateText(summary, max(12, width-2))))
		}
	}
	buttons := lipgloss.JoinHorizontal(
		lipgloss.Left,
		renderDialogButton("Resume All", dialog.Selected == suspendedTurnResumeSelectionResume),
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
