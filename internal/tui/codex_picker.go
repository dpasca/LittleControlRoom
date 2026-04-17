package tui

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"lcroom/internal/codexapp"
	"lcroom/internal/model"
	"lcroom/internal/sessionclassify"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type codexPickerKind string

const (
	codexPickerKindGlobal codexPickerKind = "global"
	codexPickerKindResume codexPickerKind = "resume"
)

type codexSessionChoice struct {
	ProjectPath  string
	ProjectName  string
	SessionID    string
	Provider     codexapp.Provider
	LastActivity time.Time
	Title        string
	Summary      string
	Live         bool
	Current      bool
	Latest       bool
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
	m.codexPickerChoices = choices
	m.codexPickerLoading = false
	m.codexPickerKind = codexPickerKindGlobal
	m.codexPickerTitle = "Embedded Sessions"
	m.codexPickerHint = "Live sessions first, then each project's latest resumable embedded session."
	m.codexPickerEmpty = "No live or resumable embedded sessions found."
	m.codexPickerProject = ""
	m.codexPickerProvider = ""
	m.status = "Embedded session picker open"
	return m, nil
}

func (m Model) openCodexResumePicker(provider codexapp.Provider, projectPath string) (tea.Model, tea.Cmd) {
	projectPath = strings.TrimSpace(projectPath)
	provider = provider.Normalized()
	if projectPath == "" || provider == "" {
		m.status = "Embedded session unavailable"
		return m, nil
	}
	m.codexPickerVisible = true
	m.codexPickerSelected = 0
	m.codexPickerChoices = nil
	m.codexPickerLoading = true
	m.codexPickerKind = codexPickerKindResume
	m.codexPickerTitle = "Resume " + provider.Label() + " Session"
	m.codexPickerHint = "Saved sessions for this project. CURRENT marks the open embedded session."
	m.codexPickerEmpty = "No saved " + provider.Label() + " sessions found for this project."
	m.codexPickerProject = projectPath
	m.codexPickerProvider = provider
	m.status = "Loading " + provider.Label() + " sessions for this project..."
	return m, m.loadCodexResumeChoicesCmd(projectPath, provider)
}

func (m Model) openVisibleCodexResumePicker() (tea.Model, tea.Cmd) {
	return m.openCodexResumePicker(m.currentEmbeddedProvider(), m.codexVisibleProject)
}

func (m *Model) closeCodexPicker(status string) {
	m.codexPickerVisible = false
	m.codexPickerSelected = 0
	m.codexPickerChoices = nil
	m.codexPickerLoading = false
	m.codexPickerKind = ""
	m.codexPickerTitle = ""
	m.codexPickerHint = ""
	m.codexPickerEmpty = ""
	m.codexPickerProject = ""
	m.codexPickerProvider = ""
	if status != "" {
		m.status = status
	}
}

func (m Model) updateCodexPickerMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.codexPickerLoading {
		switch msg.String() {
		case "esc", "alt+down":
			m.closeCodexPicker("Embedded session picker closed")
		}
		return m, nil
	}

	choices := m.currentCodexPickerChoices()
	if len(choices) == 0 {
		m.closeCodexPicker(m.currentCodexPickerEmpty())
		return m, nil
	}
	if m.codexPickerSelected >= len(choices) {
		m.codexPickerSelected = len(choices) - 1
	}
	if m.codexPickerSelected < 0 {
		m.codexPickerSelected = 0
	}

	if m.pendingG {
		m.pendingG = false
		if msg.String() == "g" {
			m.codexPickerSelected = 0
			return m, nil
		}
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
	case "pgup", "ctrl+u":
		m.moveCodexPickerSelection(-5, len(choices))
		return m, nil
	case "pgdown", "ctrl+d":
		m.moveCodexPickerSelection(5, len(choices))
		return m, nil
	case "home":
		m.codexPickerSelected = 0
		return m, nil
	case "end", "G":
		m.codexPickerSelected = len(choices) - 1
		return m, nil
	case "g":
		m.pendingG = true
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
	choices := m.currentCodexPickerChoices()
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

func (m Model) currentCodexPickerChoices() []codexSessionChoice {
	return append([]codexSessionChoice(nil), m.codexPickerChoices...)
}

func (m Model) currentCodexPickerEmpty() string {
	if text := strings.TrimSpace(m.codexPickerEmpty); text != "" {
		return text
	}
	return "No live or resumable embedded sessions"
}

func (m Model) defaultCodexPickerIndex(choices []codexSessionChoice) int {
	if m.codexPickerKind == codexPickerKindResume {
		for i, choice := range choices {
			if choice.Current {
				return i
			}
		}
		return 0
	}
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

func (m Model) loadCodexResumeChoicesCmd(projectPath string, provider codexapp.Provider) tea.Cmd {
	ctx := m.ctx
	svc := m.svc
	currentDetail := m.detail
	projectPath = strings.TrimSpace(projectPath)
	provider = provider.Normalized()
	return func() tea.Msg {
		// Always fetch fresh session data from the DB so that recently detected
		// sessions (e.g. from a separate shell) are included even if m.detail
		// was loaded before the scan completed.
		var detail model.ProjectDetail
		if svc != nil && svc.Store() != nil {
			loaded, err := svc.Store().GetProjectDetail(ctx, projectPath, 1)
			if err == nil {
				detail = loaded
			}
		}
		if detail.Summary.Path == "" && currentDetail.Summary.Path == projectPath {
			detail = currentDetail
		}
		if detail.Summary.Path == "" {
			return codexResumeChoicesMsg{
				projectPath: projectPath,
				provider:    provider,
				err:         fmt.Errorf("embedded session store unavailable"),
			}
		}
		choices := buildCodexResumeChoices(ctx, detail, provider)
		return codexResumeChoicesMsg{
			projectPath: projectPath,
			provider:    provider,
			choices:     choices,
		}
	}
}

func buildCodexResumeChoices(ctx context.Context, detail model.ProjectDetail, provider codexapp.Provider) []codexSessionChoice {
	project := detail.Summary
	choices := make([]codexSessionChoice, 0, len(detail.Sessions))
	for _, session := range detail.Sessions {
		if providerForSessionFormat(session.Format) != provider.Normalized() {
			continue
		}
		sessionID := session.ExternalID()
		if strings.TrimSpace(sessionID) == "" {
			continue
		}
		if codexResumeSessionHidden(session, provider) {
			continue
		}
		latestSessionID := detail.Summary.ExternalLatestSessionID()
		choice := codexSessionChoice{
			ProjectPath:  project.Path,
			ProjectName:  projectNameForPicker(project, project.Path),
			SessionID:    sessionID,
			Provider:     provider,
			LastActivity: session.LastEventAt,
			Latest:       latestSessionID == sessionID && providerForSessionFormat(detail.Summary.LatestSessionFormat) == provider,
			Missing:      !project.PresentOnDisk,
		}
		if preview, err := sessionclassify.ExtractPreview(ctx, session); err == nil {
			choice.Title = strings.TrimSpace(preview.Title)
			choice.Summary = strings.TrimSpace(preview.Summary)
		}
		if choice.Title == "" {
			choice.Title = "Session " + shortID(session.SessionID)
		}
		if latestSessionID == sessionID && providerForSessionFormat(detail.Summary.LatestSessionFormat) == provider {
			if latest := strings.TrimSpace(detail.Summary.LatestSessionSummary); latest != "" {
				choice.Summary = latest
			}
		}
		if choice.Summary == "" {
			choice.Summary = "Saved " + provider.Label() + " session"
		}
		choices = append(choices, choice)
	}
	return choices
}

func codexResumeSessionHidden(session model.SessionEvidence, provider codexapp.Provider) bool {
	if provider.Normalized() != codexapp.ProviderCodex {
		return false
	}
	if strings.TrimSpace(session.SessionFile) == "" {
		return false
	}
	hidden, err := codexSessionIsForkedSubagent(session.SessionFile)
	if err != nil {
		return false
	}
	return hidden
}

func codexSessionIsForkedSubagent(path string) (bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer file.Close()

	reader := bufio.NewReader(file)
	for i := 0; i < 3; i++ {
		line, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			return false, err
		}
		line = strings.TrimSpace(line)
		if line != "" {
			if hidden, ok := parseCodexForkedSubagentLine(line); ok {
				return hidden, nil
			}
		}
		if err == io.EOF {
			break
		}
	}
	return false, nil
}

func parseCodexForkedSubagentLine(line string) (hidden, ok bool) {
	var payload struct {
		Type    string `json:"type"`
		Payload struct {
			ForkedFromID string `json:"forked_from_id"`
			AgentRole    string `json:"agent_role"`
			Source       struct {
				Subagent struct {
					ThreadSpawn struct {
						ParentThreadID string `json:"parent_thread_id"`
					} `json:"thread_spawn"`
				} `json:"subagent"`
			} `json:"source"`
		} `json:"payload"`
	}
	if err := json.Unmarshal([]byte(line), &payload); err != nil {
		return false, false
	}
	if payload.Type != "session_meta" {
		return false, false
	}
	if strings.TrimSpace(payload.Payload.Source.Subagent.ThreadSpawn.ParentThreadID) != "" {
		return true, true
	}
	if strings.TrimSpace(payload.Payload.ForkedFromID) != "" && strings.TrimSpace(payload.Payload.AgentRole) != "" {
		return true, true
	}
	return false, true
}

func (m Model) applyCodexResumeChoices(msg codexResumeChoicesMsg) (tea.Model, tea.Cmd) {
	if m.codexPickerKind != codexPickerKindResume {
		return m, nil
	}
	if strings.TrimSpace(msg.projectPath) != strings.TrimSpace(m.codexPickerProject) || msg.provider.Normalized() != m.codexPickerProvider.Normalized() {
		return m, nil
	}
	m.codexPickerLoading = false
	if msg.err != nil {
		m.closeCodexPicker("")
		m.err = msg.err
		m.status = "Embedded session picker failed"
		return m, nil
	}
	m.err = nil
	choices := m.mergeCurrentResumeChoice(msg.choices)
	if len(choices) == 0 {
		m.closeCodexPicker(m.currentCodexPickerEmpty())
		return m, nil
	}
	m.codexPickerChoices = choices
	m.codexPickerSelected = m.defaultCodexPickerIndex(choices)
	m.status = m.codexPickerTitle + " open"
	return m, nil
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
		sessionID := project.ExternalLatestSessionID()
		if provider == "" || strings.TrimSpace(sessionID) == "" {
			continue
		}
		if _, ok := seen[project.Path]; ok {
			continue
		}
		choices = append(choices, codexSessionChoice{
			ProjectPath:  project.Path,
			ProjectName:  projectNameForPicker(project, project.Path),
			SessionID:    sessionID,
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
		if codexStatusIsCompacting(snapshot.Status) {
			return "Compacting: waiting for conversation history to settle"
		}
		return "Rechecking whether the turn has gone idle"
	case snapshot.Phase == codexapp.SessionPhaseStalled:
		return "Live now: embedded helper looks stuck; use /reconnect"
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
	summary := strings.TrimSpace(project.LatestSessionSummary)
	if summary != "" {
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
		Provider:         choice.Provider,
		ProjectPath:      choice.ProjectPath,
		ResumeID:         choice.SessionID,
		PlaywrightPolicy: m.currentPlaywrightPolicy(),
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

func (m Model) resumeEmbeddedSession(projectPath string, provider codexapp.Provider, sessionID string) (tea.Model, tea.Cmd) {
	projectPath = strings.TrimSpace(projectPath)
	provider = provider.Normalized()
	sessionID = strings.TrimSpace(sessionID)
	if projectPath == "" || provider == "" || sessionID == "" {
		m.status = "Session ID required"
		return m, nil
	}
	project := m.pickerProjectSummary(projectPath)
	choice := codexSessionChoice{
		ProjectPath: projectPath,
		ProjectName: projectNameForPicker(project, projectPath),
		SessionID:   sessionID,
		Provider:    provider,
	}
	if snapshot, ok := m.nonBlockingCodexSnapshot(projectPath); ok &&
		embeddedProvider(snapshot) == provider &&
		strings.TrimSpace(snapshot.ThreadID) == sessionID &&
		!snapshot.Closed {
		title, summary := liveSessionPreview(snapshot)
		choice.Title = title
		choice.Summary = summary
		choice.LastActivity = snapshot.LastActivityAt
		choice.Live = true
		choice.Current = true
		choice.Busy = snapshot.Busy
		choice.BusyExternal = snapshot.BusyExternal
	}
	return m.openCodexSessionChoice(choice)
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
	if m.browserAttention != nil && m.browserAttention.ProjectPath == projectPath {
		m.browserAttention = nil
	}
	if m.questionNotify != nil && m.questionNotify.ProjectPath == projectPath {
		m.questionNotify = nil
	}
	m.loadCodexDraft(projectPath)
	_, ok, needsAsync := m.refreshCodexSnapshot(projectPath)
	asyncCmd := tea.Cmd(nil)
	if needsAsync {
		asyncCmd = m.deferredCodexSnapshotCmd(projectPath)
	}
	if ok {
		m.syncCodexViewport(true)
	}
	seenCmd := m.markProjectSessionSeen(projectPath)
	if strings.TrimSpace(status) != "" {
		m.status = status
	}
	focusCmd := m.focusProjectPath(projectPath)
	return m, tea.Batch(m.codexInput.Focus(), focusCmd, m.refreshBusyElsewhereCmd(projectPath), seenCmd, asyncCmd)
}

func (m *Model) focusProjectPath(projectPath string) tea.Cmd {
	index := m.indexByPath(projectPath)
	if index < 0 {
		return nil
	}
	if index == m.selected {
		if project, ok := m.selectedProject(); ok && project.Path == projectPath {
			return m.requestProjectDetailViewCmd(projectPath)
		}
		return nil
	}
	m.selected = index
	m.ensureSelectionVisible()
	m.syncDetailViewport(true)
	return m.requestProjectDetailViewCmd(projectPath)
}

func (m Model) renderCodexPickerOverlay(body string, bodyW, bodyH int) string {
	panel := m.renderCodexPicker(bodyW, bodyH)
	panelWidth := lipgloss.Width(panel)
	panelHeight := lipgloss.Height(panel)
	left := max(0, (bodyW-panelWidth)/2)
	top := max(0, min((bodyH-panelHeight)/6, bodyH-panelHeight))
	return overlayBlock(body, panel, bodyW, bodyH, left, top)
}

func (m Model) renderCodexPicker(bodyW, bodyH int) string {
	panelWidth := min(bodyW, min(max(58, bodyW-10), 96))
	panelInnerWidth := max(28, panelWidth-4)
	return renderDialogPanel(panelWidth, panelInnerWidth, m.renderCodexPickerContent(panelInnerWidth, bodyH))
}

func (m Model) renderCodexPickerContent(width, bodyH int) string {
	choices := m.currentCodexPickerChoices()
	title := strings.TrimSpace(m.codexPickerTitle)
	if title == "" {
		title = "Embedded Sessions"
	}
	hint := strings.TrimSpace(m.codexPickerHint)
	if hint == "" {
		hint = "Live sessions first, then each project's latest resumable embedded session."
	}
	action := "open"
	if m.codexPickerKind == codexPickerKindResume {
		action = "resume"
	}
	lines := []string{
		commandPaletteTitleStyle.Render(title),
		commandPaletteHintStyle.Render(hint),
		"",
		renderDialogAction("Enter", action, commitActionKeyStyle, commitActionTextStyle) + "   " +
			renderDialogAction("Esc", "close", cancelActionKeyStyle, cancelActionTextStyle),
		"",
	}

	if m.codexPickerLoading {
		lines = append(lines, commandPaletteHintStyle.Render(spinnerFrames[m.spinnerFrame%len(spinnerFrames)]+" Loading session previews..."))
		return strings.Join(lines, "\n")
	}

	if len(choices) == 0 {
		lines = append(lines, commandPaletteHintStyle.Render(m.currentCodexPickerEmpty()))
		return strings.Join(lines, "\n")
	}

	start, end := m.codexPickerWindow(len(choices), bodyH)
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
		if preview := strings.TrimSpace(m.codexPickerPrimaryLabel(selected)); preview != "" {
			lines = append(lines, detailValueStyle.Render(fitFooterWidth(preview, width)))
		}
		if secondary := strings.TrimSpace(m.codexPickerSecondaryLabel(selected)); secondary != "" {
			lines = append(lines, commandPaletteHintStyle.Render(fitFooterWidth(secondary, width)))
		}
		lines = append(lines, commandPaletteHintStyle.Render(fitFooterWidth(selected.ProjectPath, width)))
		meta := "Source: " + selected.Provider.Label() + "  Session: " + shortID(selected.SessionID) + "  Last activity: " + formatPickerActivity(selected.LastActivity)
		if selected.Current {
			meta += "  Current: yes"
		}
		lines = append(lines, detailValueStyle.Render(fitFooterWidth(meta, width)))
	}

	return strings.Join(lines, "\n")
}

func (m Model) codexPickerWindow(total, bodyH int) (int, int) {
	if total <= 0 {
		return 0, 0
	}
	limit := m.codexPickerListLimit(total, bodyH)
	limit = min(limit, total)
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

func (m Model) codexPickerListLimit(total, bodyH int) int {
	if total <= 0 {
		return 0
	}
	if bodyH <= 0 {
		bodyH = 30
	}

	panelMaxHeight := max(12, bodyH-6)
	headerLines := 5
	rowHeight := 2
	if m.codexPickerKind == codexPickerKindResume {
		rowHeight = 1
	}

	aboutLines := 0
	if selected, ok := m.currentCodexPickerChoice(); ok {
		aboutLines = 4 // blank line, section title, path, and metadata
		if strings.TrimSpace(m.codexPickerPrimaryLabel(selected)) != "" {
			aboutLines++
		}
		if strings.TrimSpace(m.codexPickerSecondaryLabel(selected)) != "" {
			aboutLines++
		}
	}

	markerLines := 2 // reserve space for both "more" markers when scrolling
	borderLines := 2
	available := panelMaxHeight - headerLines - aboutLines - markerLines - borderLines
	if available < rowHeight {
		available = rowHeight
	}
	limit := max(1, available/rowHeight)
	if m.codexPickerKind == codexPickerKindResume {
		limit = min(limit, 12)
	} else {
		limit = min(limit, 8)
	}
	return min(limit, total)
}

func (m Model) renderCodexPickerRow(choice codexSessionChoice, selected bool, width int) string {
	left := m.codexPickerBadgeColumn(choice)
	right := fmt.Sprintf("%s  %s", formatPickerActivity(choice.LastActivity), shortID(choice.SessionID))
	if m.codexPickerKind != codexPickerKindResume {
		right = addPickerProjectHint(right, choice.ProjectName, choice.ProjectPath)
	}
	available := max(16, width-lipgloss.Width(left)-lipgloss.Width(right)-6)
	label := m.codexPickerPrimaryLabel(choice)
	labelCell := fitStyledWidth(fitFooterWidth(label, available), available)
	row := fmt.Sprintf("  %s  %s  %s", left, labelCell, right)
	if m.codexPickerKind != codexPickerKindResume && strings.TrimSpace(choice.Summary) != "" {
		row += "\n  " + truncateText(choice.Summary, max(12, width-4))
	}
	if selected {
		row = "> " + strings.TrimPrefix(row, "  ")
		return commandPaletteSelectStyle.Width(width).Render(row)
	}
	return commandPaletteRowStyle.Width(width).Render(row)
}

func addPickerProjectHint(line, projectName, projectPath string) string {
	project := strings.TrimSpace(projectName)
	if project == "" {
		project = strings.TrimSpace(projectPath)
		if project != "" {
			base := strings.TrimSpace(filepath.Base(filepath.Clean(project)))
			if base != "" && base != string(filepath.Separator) && base != "." {
				project = base
			}
		}
	}
	project = strings.TrimSpace(project)
	if project == "" {
		return line
	}
	return fmt.Sprintf("%s  [%s]", line, truncateText(project, 24))
}

func (m Model) codexPickerBadgeColumn(choice codexSessionChoice) string {
	parts := []string{fixedBadgeSlot(choice.Provider.SourceTag(), 2)}
	if m.codexPickerKind == codexPickerKindResume {
		parts = append(parts, fixedBadgeSlot(boolBadge(choice.Current, "CUR"), 3))
		parts = append(parts, fixedBadgeSlot(m.codexPickerResumeState(choice), 4))
	} else {
		parts = append(parts, fixedBadgeSlot(m.codexPickerGlobalState(choice), 4))
	}
	if choice.Busy {
		parts = append(parts, "BUSY")
	}
	if choice.BusyExternal {
		parts = append(parts, "EXT")
	}
	if choice.Missing {
		parts = append(parts, "MISS")
	}
	return strings.TrimRight(strings.Join(parts, " "), " ")
}

func (m Model) codexPickerResumeState(choice codexSessionChoice) string {
	switch {
	case choice.Live:
		return "LIVE"
	case choice.Latest:
		return "LAST"
	default:
		return "SAVE"
	}
}

func (m Model) codexPickerGlobalState(choice codexSessionChoice) string {
	switch {
	case choice.Live && choice.Hidden:
		return "OPEN"
	case choice.Live:
		return "LIVE"
	default:
		return "LAST"
	}
}

func boolBadge(enabled bool, label string) string {
	if !enabled {
		return ""
	}
	return label
}

func fixedBadgeSlot(label string, width int) string {
	label = strings.TrimSpace(label)
	if width <= 0 {
		return label
	}
	if label == "" {
		return strings.Repeat(" ", width)
	}
	return fmt.Sprintf("%-*s", width, truncateText(label, width))
}

func (m Model) mergeCurrentResumeChoice(choices []codexSessionChoice) []codexSessionChoice {
	if m.codexPickerKind != codexPickerKindResume {
		return append([]codexSessionChoice(nil), choices...)
	}
	currentChoice, ok := m.currentResumeChoice()
	if !ok {
		return append([]codexSessionChoice(nil), choices...)
	}

	mergedCurrent := currentChoice
	others := make([]codexSessionChoice, 0, len(choices))
	for _, choice := range choices {
		if strings.TrimSpace(choice.SessionID) == strings.TrimSpace(currentChoice.SessionID) {
			if strings.TrimSpace(mergedCurrent.Title) == "" {
				mergedCurrent.Title = choice.Title
			}
			if strings.TrimSpace(choice.Summary) != "" {
				mergedCurrent.Summary = choice.Summary
			} else if strings.TrimSpace(mergedCurrent.Summary) == "" {
				mergedCurrent.Summary = choice.Summary
			}
			if choice.LastActivity.After(mergedCurrent.LastActivity) {
				mergedCurrent.LastActivity = choice.LastActivity
			}
			mergedCurrent.Latest = mergedCurrent.Latest || choice.Latest
			mergedCurrent.Missing = choice.Missing
			continue
		}
		others = append(others, choice)
	}
	return append([]codexSessionChoice{mergedCurrent}, others...)
}

func (m Model) codexPickerPrimaryLabel(choice codexSessionChoice) string {
	if m.codexPickerKind == codexPickerKindResume {
		if title := strings.TrimSpace(choice.Title); title != "" {
			return title
		}
		if summary := strings.TrimSpace(choice.Summary); summary != "" {
			return summary
		}
	}
	if title := strings.TrimSpace(choice.Title); title != "" {
		return title
	}
	if summary := strings.TrimSpace(choice.Summary); summary != "" {
		return summary
	}
	return choice.ProjectName
}

func (m Model) codexPickerSecondaryLabel(choice codexSessionChoice) string {
	primary := strings.TrimSpace(m.codexPickerPrimaryLabel(choice))
	if primary == "" {
		return ""
	}
	for _, candidate := range []string{choice.Summary, choice.Title, choice.ProjectName} {
		text := strings.TrimSpace(candidate)
		if text == "" || strings.EqualFold(text, primary) {
			continue
		}
		return text
	}
	return ""
}

func (m Model) currentResumeChoice() (codexSessionChoice, bool) {
	snapshot, ok := m.currentCodexSnapshot()
	if !ok {
		return codexSessionChoice{}, false
	}
	projectPath := strings.TrimSpace(m.codexPickerProject)
	provider := m.codexPickerProvider.Normalized()
	if projectPath == "" || provider == "" {
		return codexSessionChoice{}, false
	}
	if strings.TrimSpace(snapshot.ProjectPath) != projectPath || embeddedProvider(snapshot) != provider {
		return codexSessionChoice{}, false
	}
	sessionID := strings.TrimSpace(snapshot.ThreadID)
	if sessionID == "" {
		return codexSessionChoice{}, false
	}
	project := m.pickerProjectSummary(projectPath)
	title, summary := liveSessionPreview(snapshot)
	if title == "" {
		title = "Session " + shortID(sessionID)
	}
	if summary == "" {
		summary = pickerSummaryForLiveSnapshot(snapshot)
	}
	return codexSessionChoice{
		ProjectPath:  projectPath,
		ProjectName:  projectNameForPicker(project, projectPath),
		SessionID:    sessionID,
		Provider:     provider,
		LastActivity: snapshot.LastActivityAt,
		Title:        title,
		Summary:      summary,
		Live:         true,
		Current:      true,
		Latest:       project.ExternalLatestSessionID() == sessionID && providerForSessionFormat(project.LatestSessionFormat) == provider,
		Busy:         snapshot.Busy,
		BusyExternal: snapshot.BusyExternal,
		Missing:      !project.PresentOnDisk && project.Path != "",
	}, true
}

func (m Model) pickerProjectSummary(projectPath string) model.ProjectSummary {
	if m.detail.Summary.Path == projectPath {
		return m.detail.Summary
	}
	for _, project := range m.allProjects {
		if project.Path == projectPath {
			return project
		}
	}
	return model.ProjectSummary{Path: projectPath}
}

func liveSessionPreview(snapshot codexapp.Snapshot) (string, string) {
	items := make([]sessionclassify.TranscriptItem, 0, len(snapshot.Entries))
	for _, entry := range snapshot.Entries {
		role := ""
		switch entry.Kind {
		case codexapp.TranscriptUser:
			role = "user"
		case codexapp.TranscriptAgent, codexapp.TranscriptStatus, codexapp.TranscriptSystem, codexapp.TranscriptPlan, codexapp.TranscriptReasoning, codexapp.TranscriptCommand, codexapp.TranscriptFileChange, codexapp.TranscriptTool:
			role = "assistant"
		default:
			continue
		}
		text := strings.TrimSpace(entry.Text)
		if text == "" {
			continue
		}
		items = append(items, sessionclassify.TranscriptItem{Role: role, Text: text})
	}
	preview := sessionclassify.PreviewFromTranscript(items)
	title := strings.TrimSpace(preview.Title)
	summary := strings.TrimSpace(preview.Summary)
	if summary == "" && strings.TrimSpace(snapshot.LastSystemNotice) != "" {
		summary = strings.TrimSpace(snapshot.LastSystemNotice)
	}
	return title, summary
}

func formatPickerActivity(at time.Time) string {
	if at.IsZero() {
		return "unknown"
	}
	return at.Local().Format("01-02 15:04")
}
