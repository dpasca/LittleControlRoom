package tui

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"lcroom/internal/codexapp"
	"lcroom/internal/model"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type codexSessionChoice struct {
	ProjectPath  string
	ProjectName  string
	SessionID    string
	Provider     codexapp.Provider
	LastActivity time.Time
	Summary      string
	Live         bool
	Busy         bool
	BusyExternal bool
	Hidden       bool
	Missing      bool
}

func (m Model) openCodexPicker() (tea.Model, tea.Cmd) {
	choices := m.codexSessionChoices()
	if len(choices) == 0 {
		m.status = "No live or resumable embedded sessions"
		return m, nil
	}
	m.codexPickerVisible = true
	m.codexPickerSelected = m.defaultCodexPickerIndex(choices)
	m.status = "Embedded session picker open"
	return m, nil
}

func (m *Model) closeCodexPicker(status string) {
	m.codexPickerVisible = false
	m.codexPickerSelected = 0
	if status != "" {
		m.status = status
	}
}

func (m Model) updateCodexPickerMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	choices := m.codexSessionChoices()
	if len(choices) == 0 {
		m.closeCodexPicker("No live or resumable embedded sessions")
		return m, nil
	}
	if m.codexPickerSelected >= len(choices) {
		m.codexPickerSelected = len(choices) - 1
	}
	if m.codexPickerSelected < 0 {
		m.codexPickerSelected = 0
	}

	switch msg.String() {
	case "esc", "alt+down":
		m.closeCodexPicker("Embedded session picker closed")
		return m, nil
	case "up", "k":
		m.moveCodexPickerSelection(-1, len(choices))
		return m, nil
	case "down", "j":
		m.moveCodexPickerSelection(1, len(choices))
		return m, nil
	case "pgup":
		m.moveCodexPickerSelection(-5, len(choices))
		return m, nil
	case "pgdown":
		m.moveCodexPickerSelection(5, len(choices))
		return m, nil
	case "home":
		m.codexPickerSelected = 0
		return m, nil
	case "end":
		m.codexPickerSelected = len(choices) - 1
		return m, nil
	case "enter":
		choice, ok := m.currentCodexPickerChoice()
		if !ok {
			return m, nil
		}
		m.closeCodexPicker("")
		return m.openCodexSessionChoice(choice)
	}

	return m, nil
}

func (m *Model) moveCodexPickerSelection(delta, total int) {
	if total <= 0 || delta == 0 {
		return
	}
	m.codexPickerSelected += delta
	if m.codexPickerSelected < 0 {
		m.codexPickerSelected = 0
	}
	if m.codexPickerSelected >= total {
		m.codexPickerSelected = total - 1
	}
}

func (m Model) currentCodexPickerChoice() (codexSessionChoice, bool) {
	choices := m.codexSessionChoices()
	if len(choices) == 0 {
		return codexSessionChoice{}, false
	}
	index := m.codexPickerSelected
	if index < 0 {
		index = 0
	}
	if index >= len(choices) {
		index = len(choices) - 1
	}
	return choices[index], true
}

func (m Model) defaultCodexPickerIndex(choices []codexSessionChoice) int {
	current := strings.TrimSpace(m.codexVisibleProject)
	for i, choice := range choices {
		if choice.ProjectPath == current {
			return i
		}
	}
	hidden := strings.TrimSpace(m.codexHiddenProject)
	for i, choice := range choices {
		if choice.ProjectPath == hidden {
			return i
		}
	}
	if project, ok := m.selectedProject(); ok {
		for i, choice := range choices {
			if choice.ProjectPath == project.Path {
				return i
			}
		}
	}
	return 0
}

func (m Model) codexSessionChoices() []codexSessionChoice {
	nameByPath := make(map[string]model.ProjectSummary, len(m.allProjects))
	for _, project := range m.allProjects {
		nameByPath[project.Path] = project
	}

	choices := make([]codexSessionChoice, 0, len(m.allProjects))
	seen := make(map[string]struct{}, len(m.allProjects))
	for _, snapshot := range m.liveCodexSnapshots() {
		project := nameByPath[snapshot.ProjectPath]
		choices = append(choices, codexSessionChoice{
			ProjectPath:  snapshot.ProjectPath,
			ProjectName:  projectNameForPicker(project, snapshot.ProjectPath),
			SessionID:    snapshot.ThreadID,
			Provider:     embeddedProvider(snapshot),
			LastActivity: snapshot.LastActivityAt,
			Summary:      pickerSummaryForLiveSnapshot(snapshot),
			Live:         true,
			Busy:         snapshot.Busy,
			BusyExternal: snapshot.BusyExternal,
			Hidden:       snapshot.ProjectPath != m.codexVisibleProject,
			Missing:      !project.PresentOnDisk && project.Path != "",
		})
		seen[snapshot.ProjectPath] = struct{}{}
	}

	recent := append([]model.ProjectSummary(nil), m.allProjects...)
	sort.SliceStable(recent, func(i, j int) bool {
		left := recent[i].LastActivity
		right := recent[j].LastActivity
		switch {
		case left.Equal(right):
			return recent[i].Path < recent[j].Path
		case left.IsZero():
			return false
		case right.IsZero():
			return true
		default:
			return left.After(right)
		}
	})
	for _, project := range recent {
		provider := providerForSessionFormat(project.LatestSessionFormat)
		if provider == "" || strings.TrimSpace(project.LatestSessionID) == "" {
			continue
		}
		if _, ok := seen[project.Path]; ok {
			continue
		}
		choices = append(choices, codexSessionChoice{
			ProjectPath:  project.Path,
			ProjectName:  projectNameForPicker(project, project.Path),
			SessionID:    project.LatestSessionID,
			Provider:     provider,
			LastActivity: project.LastActivity,
			Summary:      pickerSummaryForProject(project, provider),
			Missing:      !project.PresentOnDisk,
		})
	}

	return choices
}

func projectNameForPicker(project model.ProjectSummary, path string) string {
	name := strings.TrimSpace(project.Name)
	if name != "" {
		return name
	}
	base := strings.TrimSpace(filepath.Base(path))
	if base != "" && base != "." && base != string(filepath.Separator) {
		return base
	}
	return path
}

func pickerSummaryForLiveSnapshot(snapshot codexapp.Snapshot) string {
	label := embeddedProvider(snapshot).Label()
	status := normalizedCodexStatus(snapshot.Status)
	switch {
	case snapshot.BusyExternal:
		return "Live elsewhere: embedded view is read-only"
	case snapshot.Phase == codexapp.SessionPhaseReconciling:
		return "Rechecking whether the turn has gone idle"
	case snapshot.Phase == codexapp.SessionPhaseFinishing:
		return "Finishing: waiting for trailing output"
	case codexSnapshotCanSteer(snapshot):
		return "Live now: Enter steers the active turn"
	case snapshot.Busy:
		return "Live now: waiting for turn state to settle"
	case status != "":
		return status
	default:
		return "Live embedded " + label + " session"
	}
}

func pickerSummaryForProject(project model.ProjectSummary, provider codexapp.Provider) string {
	if summary := strings.TrimSpace(project.LatestSessionSummary); summary != "" {
		return summary
	}
	label := provider.Label()
	if !project.LastActivity.IsZero() {
		return "Latest resumable " + label + " session"
	}
	return "Resumable " + label + " session"
}

func (m Model) openCodexSessionChoice(choice codexSessionChoice) (tea.Model, tea.Cmd) {
	label := choice.Provider.Label()
	if choice.Live {
		if strings.TrimSpace(choice.ProjectPath) == strings.TrimSpace(m.codexVisibleProject) {
			m.status = "Already showing that live embedded " + label + " session"
			return m, nil
		}
		return m.showCodexProject(choice.ProjectPath, "Switched to the selected embedded "+label+" session")
	}
	if choice.Missing {
		m.status = "Resuming " + label + " requires a folder present on disk"
		return m, nil
	}
	req := codexapp.LaunchRequest{
		Provider:    choice.Provider,
		ProjectPath: choice.ProjectPath,
		ResumeID:    choice.SessionID,
	}
	if choice.Provider.Normalized() == codexapp.ProviderCodex {
		req.Preset = m.currentCodexLaunchPreset()
	}
	if err := req.Validate(); err != nil {
		m.status = err.Error()
		return m, nil
	}
	m.beginCodexPendingOpen(choice.ProjectPath, choice.Provider)
	m.status = fmt.Sprintf("Opening embedded %s session %s...", label, shortID(choice.SessionID))
	focusCmd := m.focusProjectPath(choice.ProjectPath)
	return m, tea.Batch(m.openCodexSessionCmd(req), focusCmd)
}

func (m Model) showCodexProject(projectPath, status string) (tea.Model, tea.Cmd) {
	m.ensureCodexRuntime()
	projectPath = strings.TrimSpace(projectPath)
	if projectPath == "" {
		m.status = "Embedded session unavailable"
		return m, nil
	}
	if current := strings.TrimSpace(m.codexVisibleProject); current != "" && current != projectPath {
		m.persistVisibleCodexDraft()
		m.codexHiddenProject = current
	}
	m.codexVisibleProject = projectPath
	m.codexHiddenProject = projectPath
	m.loadCodexDraft(projectPath)
	m.syncCodexViewport(true)
	if strings.TrimSpace(status) != "" {
		m.status = status
	}
	focusCmd := m.focusProjectPath(projectPath)
	return m, tea.Batch(m.codexInput.Focus(), focusCmd, m.refreshBusyElsewhereCmd(projectPath))
}

func (m *Model) focusProjectPath(projectPath string) tea.Cmd {
	index := m.indexByPath(projectPath)
	if index < 0 {
		return nil
	}
	if index == m.selected {
		if project, ok := m.selectedProject(); ok && project.Path == projectPath {
			return m.loadDetailCmd(projectPath)
		}
		return nil
	}
	m.selected = index
	m.ensureSelectionVisible()
	m.syncDetailViewport(true)
	return m.loadDetailCmd(projectPath)
}

func (m Model) renderCodexPickerOverlay(body string, bodyW, bodyH int) string {
	panel := m.renderCodexPicker(bodyW)
	panelWidth := lipgloss.Width(panel)
	panelHeight := lipgloss.Height(panel)
	left := max(0, (bodyW-panelWidth)/2)
	top := max(0, min((bodyH-panelHeight)/6, bodyH-panelHeight))
	return overlayBlock(body, panel, bodyW, bodyH, left, top)
}

func (m Model) renderCodexPicker(bodyW int) string {
	panelWidth := min(bodyW, min(max(58, bodyW-10), 96))
	panelInnerWidth := max(28, panelWidth-4)
	return lipgloss.NewStyle().
		Width(panelWidth).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("81")).
		Padding(0, 1).
		Background(lipgloss.Color("235")).
		Foreground(lipgloss.Color("252")).
		Render(m.renderCodexPickerContent(panelInnerWidth))
}

func (m Model) renderCodexPickerContent(width int) string {
	choices := m.codexSessionChoices()
	lines := []string{
		commandPaletteTitleStyle.Render("Embedded Sessions"),
		commandPaletteHintStyle.Render("Live sessions first, then each project's latest resumable embedded session."),
		"",
		renderDialogAction("Enter", "open", commitActionKeyStyle, commitActionTextStyle) + "   " +
			renderDialogAction("Esc", "close", cancelActionKeyStyle, cancelActionTextStyle),
		"",
	}

	if len(choices) == 0 {
		lines = append(lines, commandPaletteHintStyle.Render("No live or resumable embedded sessions found."))
		return strings.Join(lines, "\n")
	}

	start, end := m.codexPickerWindow(len(choices))
	if start > 0 {
		lines = append(lines, commandPaletteHintStyle.Render(fmt.Sprintf("↑ %d more", start)))
	}
	for i := start; i < end; i++ {
		lines = append(lines, m.renderCodexPickerRow(choices[i], i == m.codexPickerSelected, width))
	}
	if end < len(choices) {
		lines = append(lines, commandPaletteHintStyle.Render(fmt.Sprintf("↓ %d more", len(choices)-end)))
	}

	if selected, ok := m.currentCodexPickerChoice(); ok {
		lines = append(lines, "")
		lines = append(lines, commandPaletteTitleStyle.Render("About"))
		lines = append(lines, commandPaletteHintStyle.Render(fitFooterWidth(selected.ProjectPath, width)))
		lines = append(lines, detailValueStyle.Render(fitFooterWidth("Source: "+selected.Provider.Label()+"  Session: "+shortID(selected.SessionID)+"  Last activity: "+formatPickerActivity(selected.LastActivity), width)))
		if summary := strings.TrimSpace(selected.Summary); summary != "" {
			lines = append(lines, commandPaletteHintStyle.Render(fitFooterWidth(summary, width)))
		}
	}

	return strings.Join(lines, "\n")
}

func (m Model) codexPickerWindow(total int) (int, int) {
	if total <= 0 {
		return 0, 0
	}
	limit := min(7, total)
	start := 0
	if m.codexPickerSelected >= limit {
		start = m.codexPickerSelected - limit + 1
	}
	maxStart := total - limit
	if start > maxStart {
		start = maxStart
	}
	if start < 0 {
		start = 0
	}
	return start, start + limit
}

func (m Model) renderCodexPickerRow(choice codexSessionChoice, selected bool, width int) string {
	badges := make([]string, 0, 4)
	if tag := choice.Provider.SourceTag(); tag != "" {
		badges = append(badges, tag)
	}
	switch {
	case choice.Live && choice.Hidden:
		badges = append(badges, "OPEN")
	case choice.Live:
		badges = append(badges, "LIVE")
	default:
		badges = append(badges, "LAST")
	}
	if choice.Busy {
		badges = append(badges, "BUSY")
	}
	if choice.BusyExternal {
		badges = append(badges, "EXT")
	}
	if choice.Missing {
		badges = append(badges, "MISSING")
	}

	left := strings.Join(badges, " ")
	right := fmt.Sprintf("%s  %s", formatPickerActivity(choice.LastActivity), shortID(choice.SessionID))
	available := max(16, width-len(left)-len(right)-6)
	label := truncateText(choice.ProjectName, available)
	row := fmt.Sprintf("  %s  %s  %s", left, label, right)
	if selected {
		row = "> " + strings.TrimPrefix(row, "  ")
		return commandPaletteSelectStyle.Width(width).Render(row)
	}
	return commandPaletteRowStyle.Width(width).Render(row)
}

func formatPickerActivity(at time.Time) string {
	if at.IsZero() {
		return "unknown"
	}
	return at.Local().Format("01-02 15:04")
}
