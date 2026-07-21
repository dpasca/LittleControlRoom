package tui

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"lcroom/internal/codexapp"
	"lcroom/internal/model"
	"lcroom/internal/service"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const (
	cloneProjectFieldRepository = iota
	cloneProjectFieldPath
	cloneProjectFieldAssistant
)

const cloneProjectActionTimeout = 5 * time.Minute

type cloneProjectDialogState struct {
	RepositoryInput        textinput.Model
	PathInput              textinput.Model
	Selected               int
	Provider               codexapp.Provider
	ProviderDefaultLabel   string
	Submitting             bool
	Cancel                 context.CancelFunc
	CancelRequested        bool
	ReturnToNewProject     *newProjectDialogState
	Preview                service.CloneProjectPreview
	PreviewError           string
	PreviewPending         bool
	PreviewSeq             int64
	PathSuggestionsPending bool
	PathSuggestionSeq      int64
	PathSuggestionItems    []newProjectPathSuggestion
	PathSuggestionHidden   int
	Error                  string
}

type cloneProjectPreviewMsg struct {
	seq     int64
	preview service.CloneProjectPreview
	err     error
}

type cloneProjectPathSuggestionsMsg struct {
	seq    int64
	result newProjectPathSuggestionsResult
}

type cloneProjectResultMsg struct {
	result   service.CloneProjectResult
	provider codexapp.Provider
	err      error
}

func (m *Model) openCloneProjectDialog(parentPath string, provider codexapp.Provider, defaultLabel string) tea.Cmd {
	if strings.TrimSpace(parentPath) == "" {
		parentPath = m.defaultNewProjectParentPath()
	}
	provider, initialDefaultLabel := m.initialEmbeddedProviderForNewItem(provider)
	if strings.TrimSpace(defaultLabel) == "" {
		defaultLabel = initialDefaultLabel
	}
	repositoryInput := newNewProjectTextInput("", 2048)
	repositoryInput.Placeholder = "https://github.com/owner/repository.git"
	pathInput := newNewProjectTextInput(parentPath, 1024)
	pathInput.Placeholder = "/path/to/projects"
	configureNewProjectPathInput(&pathInput)

	m.newProjectDialog = nil
	m.cloneProjectDialog = &cloneProjectDialogState{
		RepositoryInput:      repositoryInput,
		PathInput:            pathInput,
		Selected:             cloneProjectFieldRepository,
		Provider:             provider,
		ProviderDefaultLabel: defaultLabel,
	}
	m.err = nil
	m.status = "Clone project dialog open. Enter clone, Esc cancel"
	return batchCmds(m.setCloneProjectSelection(cloneProjectFieldRepository), m.refreshCloneProjectPreview(), m.refreshCloneProjectPathSuggestions())
}

func (m *Model) openCloneProjectDialogFromNewProject() tea.Cmd {
	previous := m.newProjectDialog
	if previous == nil {
		return m.openCloneProjectDialog("", "", "")
	}
	previous.PathInput.Blur()
	previous.NameInput.Blur()
	parentPath := previous.PathInput.Value()
	provider := previous.Provider
	defaultLabel := previous.ProviderDefaultLabel
	cmd := m.openCloneProjectDialog(parentPath, provider, defaultLabel)
	if m.cloneProjectDialog != nil {
		m.cloneProjectDialog.ReturnToNewProject = previous
	}
	return cmd
}

func (m *Model) closeCloneProjectDialog(status string) tea.Cmd {
	dialog := m.cloneProjectDialog
	if dialog == nil {
		return nil
	}
	dialog.RepositoryInput.Blur()
	dialog.PathInput.Blur()
	if dialog.Cancel != nil {
		dialog.Cancel()
		dialog.Cancel = nil
	}
	returnDialog := dialog.ReturnToNewProject
	m.cloneProjectDialog = nil
	if returnDialog != nil {
		m.newProjectDialog = returnDialog
		if status == "" {
			status = "Back to New Project"
		}
		m.status = status
		return m.setNewProjectSelection(returnDialog.Selected)
	}
	if status != "" {
		m.status = status
	}
	return nil
}

func (m Model) updateCloneProjectMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	dialog := m.cloneProjectDialog
	if dialog == nil {
		return m, nil
	}
	if dialog.Submitting {
		if msg.String() == "esc" && !dialog.CancelRequested {
			dialog.CancelRequested = true
			if dialog.Cancel != nil {
				dialog.Cancel()
			}
			m.status = "Canceling repository clone..."
		}
		return m, nil
	}
	if msg.Alt && len(msg.Runes) == 1 && dialog.Selected == cloneProjectFieldPath {
		index := int(msg.Runes[0] - '1')
		if suggestion, ok := newProjectPathSuggestionAt(dialog.PathInput, index); ok {
			dialog.PathInput.SetValue(suggestion)
			dialog.PathInput.CursorEnd()
			dialog.PathInput.SetSuggestions(nil)
			dialog.PathSuggestionItems = nil
			dialog.PathSuggestionHidden = 0
			m.status = fmt.Sprintf("Using path suggestion %d", index+1)
			return m, batchCmds(m.refreshCloneProjectPreview(), m.refreshCloneProjectPathSuggestions())
		}
		recentParents := m.visibleNewProjectRecentParents()
		if index >= 0 && index < len(recentParents) && index < newProjectRecentPathLimit {
			dialog.PathInput.SetValue(recentParents[index])
			dialog.PathInput.CursorEnd()
			dialog.PathInput.SetSuggestions(nil)
			dialog.PathSuggestionItems = nil
			dialog.PathSuggestionHidden = 0
			m.status = fmt.Sprintf("Using recent parent path %d", index+1)
			return m, batchCmds(m.refreshCloneProjectPreview(), m.refreshCloneProjectPathSuggestions())
		}
	}

	switch msg.String() {
	case "esc":
		return m, m.closeCloneProjectDialog("Clone project canceled")
	case "tab", "down", "ctrl+n":
		return m, m.moveCloneProjectSelection(1)
	case "shift+tab", "up", "ctrl+p":
		return m, m.moveCloneProjectSelection(-1)
	case "enter":
		if dialog.PreviewPending {
			m.status = "Checking clone destination..."
			return m, nil
		}
		if dialog.PreviewError != "" {
			m.status = dialog.PreviewError
			return m, nil
		}
		if strings.TrimSpace(dialog.Preview.ProjectPath) == "" {
			m.status = "Repository and clone destination are required"
			return m, nil
		}
		dialog.Submitting = true
		dialog.CancelRequested = false
		dialog.Error = ""
		m.status = "Cloning repository into " + dialog.Preview.ProjectPath + "..."
		return m, m.cloneProjectCmd(service.CloneProjectRequest{
			Repository:             dialog.RepositoryInput.Value(),
			ParentPath:             dialog.PathInput.Value(),
			PreferredSessionSource: modelSessionSourceFromCodexProvider(dialog.Provider),
			CategoryID:             m.categoryIDForNewItem(),
			CategoryExplicit:       true,
		}, dialog.Provider)
	case " ", "x", "right", "l":
		if dialog.Selected == cloneProjectFieldAssistant {
			m.cycleCloneProjectAssistant(1)
			return m, nil
		}
	case "left", "h":
		if dialog.Selected == cloneProjectFieldAssistant {
			m.cycleCloneProjectAssistant(-1)
			return m, nil
		}
	}

	switch dialog.Selected {
	case cloneProjectFieldRepository:
		previous := dialog.RepositoryInput.Value()
		input, cmd := dialog.RepositoryInput.Update(msg)
		dialog.RepositoryInput = input
		if dialog.RepositoryInput.Value() != previous {
			dialog.Error = ""
			return m, batchCmds(cmd, m.refreshCloneProjectPreview())
		}
		return m, cmd
	case cloneProjectFieldPath:
		previous := dialog.PathInput.Value()
		input, cmd := dialog.PathInput.Update(msg)
		dialog.PathInput = input
		if dialog.PathInput.Value() != previous {
			dialog.Error = ""
			return m, batchCmds(cmd, m.refreshCloneProjectPreview(), m.refreshCloneProjectPathSuggestions())
		}
		return m, cmd
	default:
		return m, nil
	}
}

func (m *Model) moveCloneProjectSelection(delta int) tea.Cmd {
	dialog := m.cloneProjectDialog
	if dialog == nil || delta == 0 {
		return nil
	}
	index := dialog.Selected + delta
	if index < cloneProjectFieldRepository {
		index = cloneProjectFieldAssistant
	}
	if index > cloneProjectFieldAssistant {
		index = cloneProjectFieldRepository
	}
	return m.setCloneProjectSelection(index)
}

func (m *Model) setCloneProjectSelection(index int) tea.Cmd {
	dialog := m.cloneProjectDialog
	if dialog == nil {
		return nil
	}
	index = max(cloneProjectFieldRepository, min(cloneProjectFieldAssistant, index))
	dialog.Selected = index
	dialog.RepositoryInput.Blur()
	dialog.PathInput.Blur()
	switch index {
	case cloneProjectFieldRepository:
		dialog.RepositoryInput.CursorEnd()
		return dialog.RepositoryInput.Focus()
	case cloneProjectFieldPath:
		dialog.PathInput.CursorEnd()
		return dialog.PathInput.Focus()
	default:
		return nil
	}
}

func (m *Model) cycleCloneProjectAssistant(delta int) {
	dialog := m.cloneProjectDialog
	if dialog == nil || delta == 0 {
		return
	}
	options := embeddedLaunchProviderOptions()
	index := 0
	current := dialog.Provider.Normalized()
	for i, provider := range options {
		if provider == current {
			index = i
			break
		}
	}
	index += delta
	if index < 0 {
		index = len(options) - 1
	}
	if index >= len(options) {
		index = 0
	}
	dialog.Provider = options[index]
	dialog.ProviderDefaultLabel = ""
}

func (m *Model) refreshCloneProjectPreview() tea.Cmd {
	dialog := m.cloneProjectDialog
	if dialog == nil {
		return nil
	}
	dialog.PreviewSeq++
	seq := dialog.PreviewSeq
	repository := strings.TrimSpace(dialog.RepositoryInput.Value())
	parentPath := strings.TrimSpace(dialog.PathInput.Value())
	dialog.Preview = service.CloneProjectPreview{}
	dialog.PreviewError = ""
	if repository == "" || parentPath == "" {
		dialog.PreviewPending = false
		return nil
	}
	dialog.PreviewPending = true
	return func() tea.Msg {
		preview, err := service.PreviewCloneProject(repository, parentPath)
		return cloneProjectPreviewMsg{seq: seq, preview: preview, err: err}
	}
}

func (m *Model) refreshCloneProjectPathSuggestions() tea.Cmd {
	dialog := m.cloneProjectDialog
	if dialog == nil {
		return nil
	}
	dialog.PathSuggestionSeq++
	seq := dialog.PathSuggestionSeq
	rawPath := dialog.PathInput.Value()
	if strings.TrimSpace(trimNewProjectWrappingQuotes(rawPath)) == "" {
		dialog.PathInput.SetSuggestions(nil)
		dialog.PathSuggestionItems = nil
		dialog.PathSuggestionHidden = 0
		dialog.PathSuggestionsPending = false
		return nil
	}
	settings := m.currentSettingsBaseline()
	suggestionCtx := newProjectPathSuggestionContext{
		RecentParents: append([]string(nil), m.newProjectRecentParents...),
		IncludePaths:  append([]string(nil), settings.IncludePaths...),
		Projects:      append([]model.ProjectSummary(nil), m.allProjects...),
	}
	dialog.PathSuggestionsPending = true
	return func() tea.Msg {
		return cloneProjectPathSuggestionsMsg{
			seq:    seq,
			result: newProjectPathSuggestionItems(m.homeDirFn, rawPath, newProjectPathSuggestionLimit, suggestionCtx),
		}
	}
}

func (m *Model) cloneProjectCmd(req service.CloneProjectRequest, provider codexapp.Provider) tea.Cmd {
	dialog := m.cloneProjectDialog
	if dialog == nil {
		return nil
	}
	if m.svc == nil {
		return func() tea.Msg {
			return cloneProjectResultMsg{provider: provider, err: fmt.Errorf("service unavailable")}
		}
	}
	parent := m.ctx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, cloneProjectActionTimeout)
	dialog.Cancel = cancel
	return func() tea.Msg {
		defer cancel()
		result, err := m.svc.CloneProject(ctx, req)
		err = timeoutActionError(err, cloneProjectActionTimeout, "cloning the repository")
		return cloneProjectResultMsg{result: result, provider: explicitEmbeddedProvider(provider), err: err}
	}
}

func (m Model) applyCloneProjectResultMsg(msg cloneProjectResultMsg) (tea.Model, tea.Cmd) {
	dialog := m.cloneProjectDialog
	if dialog != nil {
		dialog.Submitting = false
		if dialog.Cancel != nil {
			dialog.Cancel()
			dialog.Cancel = nil
		}
	}
	if msg.err != nil && !msg.result.Cloned {
		if dialog != nil {
			dialog.Error = msg.err.Error()
			dialog.CancelRequested = false
		}
		if errors.Is(msg.err, context.Canceled) {
			m.status = "Repository clone canceled"
			return m, nil
		}
		m.reportError("Repository clone failed", msg.err, "")
		return m, nil
	}

	m.cloneProjectDialog = nil
	m.newProjectDialog = nil
	m.focusedPane = focusProjects
	m.preferredSelectPath = msg.result.ProjectPath
	provider := explicitEmbeddedProvider(msg.provider)
	if provider != "" {
		m.rememberEmbeddedProvider(provider)
		m.setEmbeddedLaunchProviderOverride(msg.result.ProjectPath, provider)
	}
	refreshCmd := m.requestProjectInvalidationCmd(invalidateProjectStructure(""))
	if msg.err != nil {
		m.reportError("Repository cloned but project setup was incomplete", msg.err, msg.result.ProjectPath)
		return m, refreshCmd
	}

	m.err = nil
	m.newProjectRecentParents = append([]string(nil), msg.result.RecentParentPaths...)
	m.status = fmt.Sprintf("Repository cloned as %q and added to the list", msg.result.ProjectName)
	if msg.result.CollisionResolved {
		m.status = fmt.Sprintf("Repository cloned as %q to avoid a folder collision and added to the list", msg.result.ProjectName)
	}
	if provider != "" {
		m.status += "; Enter opens " + provider.Label()
	}
	return m, refreshCmd
}

func (m Model) renderCloneProjectOverlay(body string, bodyW, bodyH int) string {
	panelWidth := min(bodyW, min(max(64, bodyW-10), 98))
	panelInnerWidth := max(28, panelWidth-4)
	panel := renderDialogPanel(panelWidth, panelInnerWidth, m.renderCloneProjectContent(panelInnerWidth))
	left := max(0, (bodyW-lipgloss.Width(panel))/2)
	top := max(0, (bodyH-lipgloss.Height(panel))/4)
	return overlayBlock(body, panel, bodyW, bodyH, left, top)
}

func (m Model) renderCloneProjectContent(width int) string {
	dialog := m.cloneProjectDialog
	if dialog == nil {
		return ""
	}
	labelWidth := max(12, min(18, width/4))
	inputWidth := max(12, width-labelWidth-1)
	lines := []string{
		commandPaletteTitleStyle.Render("Clone Git Repository"),
		commandPaletteHintStyle.Render("Clone an existing Git repository into a new project folder."),
		"",
		m.renderNewProjectInputRow("Repository", dialog.Selected == cloneProjectFieldRepository, labelWidth, inputWidth, dialog.RepositoryInput),
		m.renderNewProjectInputRow("Clone into", dialog.Selected == cloneProjectFieldPath, labelWidth, inputWidth, dialog.PathInput),
		m.renderCloneProjectAssistantRow(labelWidth, inputWidth),
	}
	if statusLine := m.todoCopyProviderStatusLine(dialog.Provider, m.currentSettingsBaseline()); statusLine != "" {
		lines = append(lines, lipgloss.NewStyle().Width(width).Render(detailField("Agent status", statusLine)))
	}
	destination := dialog.Preview.ProjectPath
	if destination == "" {
		parentPath := normalizeNewProjectPathInput(m.homeDirFn, dialog.PathInput.Value())
		destination = filepath.Join(parentPath, "<repository>")
	}
	lines = append(lines,
		"",
		detailLabelStyle.Render("Destination:"),
		lipgloss.NewStyle().Width(width).Render(detailValueStyle.Render(destination)),
	)
	if dialog.Selected == cloneProjectFieldPath {
		if suggestions := m.renderCloneProjectPathSuggestions(width); len(suggestions) > 0 {
			lines = append(lines, "")
			lines = append(lines, suggestions...)
		}
	}
	lines = append(lines, m.renderCloneProjectStatus(width)...)
	if dialog.Selected == cloneProjectFieldPath && len(dialog.PathInput.MatchedSuggestions()) == 0 && dialog.PathSuggestionHidden == 0 {
		if recentParents := m.visibleNewProjectRecentParents(); len(recentParents) > 0 {
			lines = append(lines, "", commandPaletteTitleStyle.Render("Recent Paths"), commandPaletteHintStyle.Render("Alt+1..3 applies a remembered destination."))
			for i, path := range recentParents {
				label := fmt.Sprintf("Alt+%d ", i+1)
				rowStyle := commandPaletteRowStyle
				if normalizeNewProjectPathInput(m.homeDirFn, dialog.PathInput.Value()) == filepath.Clean(path) {
					rowStyle = commandPaletteSelectStyle
				}
				lines = append(lines, rowStyle.Width(width).Render(label+truncateText(path, max(12, width-len(label)))))
			}
		}
	}
	lines = append(lines, "", m.renderCloneProjectActions())
	return strings.Join(lines, "\n")
}

func (m Model) renderCloneProjectAssistantRow(labelWidth, inputWidth int) string {
	dialog := m.cloneProjectDialog
	if dialog == nil {
		return ""
	}
	label := "  Assistant"
	labelStyle := detailLabelStyle
	if dialog.Selected == cloneProjectFieldAssistant {
		label = "> Assistant"
		labelStyle = commandPalettePickStyle
	}
	buttons := make([]string, 0, len(embeddedLaunchProviderOptions()))
	for _, provider := range embeddedLaunchProviderOptions() {
		buttons = append(buttons, renderDialogButton(provider.Label(), dialog.Provider.Normalized() == provider))
	}
	value := strings.Join(buttons, " ")
	if defaultLabel := strings.TrimSpace(dialog.ProviderDefaultLabel); defaultLabel != "" {
		value += " " + detailMutedStyle.Render(defaultLabel)
	}
	return labelStyle.Width(labelWidth).Render(truncateText(label, labelWidth)) + " " + fitStyledWidth(value, inputWidth)
}

func (m Model) renderCloneProjectStatus(width int) []string {
	dialog := m.cloneProjectDialog
	if dialog == nil {
		return nil
	}
	var lines []string
	switch {
	case dialog.Submitting && dialog.CancelRequested:
		lines = append(lines, commandPaletteHintStyle.Render(spinnerFrames[m.spinnerFrame%len(spinnerFrames)]+" Canceling clone and cleaning up..."))
	case dialog.Submitting:
		lines = append(lines, commandPaletteHintStyle.Render(spinnerFrames[m.spinnerFrame%len(spinnerFrames)]+" Cloning repository..."))
	case dialog.Error != "":
		message := truncateText(dialog.Error, max(80, width*4))
		lines = append(lines, renderWrappedDialogTextLines(detailWarningStyle, width, message)...)
	case dialog.PreviewPending:
		lines = append(lines, commandPaletteHintStyle.Render("Checking the clone destination in the background..."))
	case dialog.PreviewError != "":
		lines = append(lines, detailWarningStyle.Render(dialog.PreviewError))
	case dialog.Preview.Collision:
		lines = append(lines, commandPaletteHintStyle.Render(fmt.Sprintf("%q already exists here. The clone will use %q.", dialog.Preview.BaseName, dialog.Preview.ProjectName)))
	case dialog.Preview.ProjectPath != "":
		lines = append(lines, commandPaletteHintStyle.Render(fmt.Sprintf("The repository will be cloned as %q.", dialog.Preview.ProjectName)))
	default:
		lines = append(lines, commandPaletteHintStyle.Render("Enter a Git repository and choose the existing folder that should contain its clone."))
	}
	for i, line := range lines {
		lines[i] = lipgloss.NewStyle().Width(width).Render(line)
	}
	return lines
}

func (m Model) renderCloneProjectPathSuggestions(width int) []string {
	dialog := m.cloneProjectDialog
	if dialog == nil {
		return nil
	}
	suggestions := dialog.PathInput.MatchedSuggestions()
	if len(suggestions) == 0 {
		if dialog.PathSuggestionsPending {
			return []string{commandPaletteTitleStyle.Render("Path Suggestions"), commandPaletteHintStyle.Render("Looking for matching folders...")}
		}
		if dialog.PathSuggestionHidden > 0 {
			return []string{commandPaletteTitleStyle.Render("Path Suggestions"), commandPaletteHintStyle.Render(newProjectHiddenSuggestionText(dialog.PathSuggestionHidden))}
		}
		return nil
	}
	limit := min(newProjectPathSuggestionLimit, len(suggestions))
	lines := []string{
		commandPaletteTitleStyle.Render("Path Suggestions"),
		commandPaletteHintStyle.Render("Right completes the highlighted path; Alt+1..8 picks; Alt+Up/Down cycles."),
	}
	if dialog.PathSuggestionHidden > 0 {
		lines = append(lines, commandPaletteHintStyle.Render(newProjectHiddenSuggestionText(dialog.PathSuggestionHidden)))
	}
	selected := dialog.PathInput.CurrentSuggestionIndex()
	for i := 0; i < limit; i++ {
		label := fmt.Sprintf("Alt+%d ", i+1)
		source := "folder"
		if suggestion, ok := newProjectPathSuggestionForPath(dialog.PathSuggestionItems, suggestions[i]); ok {
			source = suggestion.Source.Label()
		}
		source = truncateText(fmt.Sprintf("%-7s", source), 7)
		prefix := label + source + " "
		row := prefix + truncateText(suggestions[i], max(12, width-len(prefix)))
		rowStyle := commandPaletteRowStyle
		if i == selected {
			rowStyle = commandPaletteSelectStyle
		}
		lines = append(lines, rowStyle.Width(width).Render(row))
	}
	return lines
}

func (m Model) renderCloneProjectActions() string {
	dialog := m.cloneProjectDialog
	if dialog == nil {
		return ""
	}
	if dialog.Submitting {
		return strings.Join([]string{
			renderDialogAction("Enter", "cloning", disabledActionKeyStyle, disabledActionTextStyle),
			renderDialogAction("Esc", "cancel clone", cancelActionKeyStyle, cancelActionTextStyle),
		}, "   ")
	}
	return strings.Join([]string{
		renderDialogAction("Enter", "clone", commitActionKeyStyle, commitActionTextStyle),
		renderDialogAction("Tab", "next", navigateActionKeyStyle, navigateActionTextStyle),
		renderDialogAction("Esc", "cancel", cancelActionKeyStyle, cancelActionTextStyle),
	}, "   ")
}
