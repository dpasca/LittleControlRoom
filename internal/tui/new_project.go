package tui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"lcroom/internal/codexapp"
	"lcroom/internal/model"
	"lcroom/internal/service"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const (
	newProjectFieldPath = iota
	newProjectFieldName
	newProjectFieldAssistant
	newProjectFieldGitRepo
)

const (
	newProjectRecentPathLimit     = 3
	newProjectPathSuggestionLimit = 8
)

type newProjectDialogState struct {
	PathInput              textinput.Model
	NameInput              textinput.Model
	Selected               int
	CreateGitRepo          bool
	Provider               codexapp.Provider
	ProviderExplicit       bool
	ProviderDefaultLabel   string
	Submitting             bool
	PathManuallyEdited     bool
	PathSuggestionsPending bool
	PathSuggestionSeq      int64
	PathSuggestionItems    []newProjectPathSuggestion
	PathSuggestionHidden   int
	Preview                newProjectPreview
	PreviewPending         bool
	PreviewSeq             int64
}

type newProjectResultMsg struct {
	result   service.CreateOrAttachProjectResult
	provider codexapp.Provider
	err      error
}

type recentProjectParentsMsg struct {
	paths []string
	err   error
}

type newProjectPreviewMsg struct {
	seq     int64
	preview newProjectPreview
}

type newProjectPathSuggestionsMsg struct {
	seq    int64
	result newProjectPathSuggestionsResult
}

type newProjectPreview struct {
	ParentPath          string
	Name                string
	FullPath            string
	Ready               bool
	Exists              bool
	ExistingDir         bool
	NameDerivedFromPath bool
	Error               string
}

type newProjectPathSuggestionSource string

const (
	newProjectPathSuggestionRecent        newProjectPathSuggestionSource = "recent"
	newProjectPathSuggestionScope         newProjectPathSuggestionSource = "scope"
	newProjectPathSuggestionProjectParent newProjectPathSuggestionSource = "project"
	newProjectPathSuggestionFolder        newProjectPathSuggestionSource = "folder"
)

type newProjectPathSuggestion struct {
	Path   string
	Source newProjectPathSuggestionSource
}

type newProjectPathSuggestionsResult struct {
	Suggestions []newProjectPathSuggestion
	HiddenCount int
}

type newProjectPathSuggestionContext struct {
	RecentParents []string
	IncludePaths  []string
	Projects      []model.ProjectSummary
}

func (m Model) loadRecentProjectParentsCmd() tea.Cmd {
	if m.svc == nil {
		return nil
	}
	return func() tea.Msg {
		paths, err := m.svc.RecentProjectParentPaths(m.ctx, newProjectRecentPathLimit)
		return recentProjectParentsMsg{paths: paths, err: err}
	}
}

func (m *Model) openNewProjectDialog(provider codexapp.Provider) tea.Cmd {
	pathInput := newNewProjectTextInput(m.defaultNewProjectParentPath(), 1024)
	configureNewProjectPathInput(&pathInput)
	provider, defaultLabel := m.initialEmbeddedProviderForNewItem(provider)
	dialog := &newProjectDialogState{
		PathInput:            pathInput,
		NameInput:            newNewProjectTextInput("", 256),
		Selected:             newProjectFieldPath,
		CreateGitRepo:        true,
		Provider:             provider,
		ProviderExplicit:     true,
		ProviderDefaultLabel: defaultLabel,
	}
	dialog.PathInput.Placeholder = "/path/to/projects"
	dialog.NameInput.Placeholder = "project-name (optional for existing folder path)"

	m.newProjectDialog = dialog
	m.showHelp = false
	m.err = nil
	m.status = "New project dialog open. Enter create/add, Esc cancel"
	return batchCmds(m.setNewProjectSelection(newProjectFieldPath), m.refreshNewProjectPreview(), m.refreshNewProjectPathSuggestions())
}

func (m *Model) closeNewProjectDialog(status string) {
	if m.newProjectDialog != nil {
		m.newProjectDialog.PathInput.Blur()
		m.newProjectDialog.NameInput.Blur()
	}
	m.newProjectDialog = nil
	if status != "" {
		m.status = status
	}
}

func (m Model) updateNewProjectMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	dialog := m.newProjectDialog
	if dialog == nil {
		return m, nil
	}
	if dialog.Submitting {
		return m, nil
	}
	if msg.Alt && len(msg.Runes) == 1 {
		index := int(msg.Runes[0] - '1')
		if dialog.Selected == newProjectFieldPath {
			if suggestion, ok := newProjectPathSuggestionAt(dialog.PathInput, index); ok {
				dialog.PathInput.SetValue(suggestion)
				dialog.PathInput.CursorEnd()
				dialog.PathInput.SetSuggestions(nil)
				dialog.PathSuggestionItems = nil
				dialog.PathSuggestionHidden = 0
				dialog.PathManuallyEdited = true
				m.status = fmt.Sprintf("Using path suggestion %d", index+1)
				return m, batchCmds(m.refreshNewProjectPreview(), m.refreshNewProjectPathSuggestions())
			}
		}
		recentParents := m.visibleNewProjectRecentParents()
		if index >= 0 && index < len(recentParents) && index < newProjectRecentPathLimit {
			dialog.PathInput.SetValue(recentParents[index])
			dialog.PathInput.CursorEnd()
			dialog.PathInput.SetSuggestions(nil)
			dialog.PathSuggestionItems = nil
			dialog.PathSuggestionHidden = 0
			dialog.PathManuallyEdited = false
			m.status = fmt.Sprintf("Using recent parent path %d", index+1)
			return m, batchCmds(m.refreshNewProjectPreview(), m.refreshNewProjectPathSuggestions())
		}
	}

	switch msg.String() {
	case "esc":
		m.closeNewProjectDialog("New project canceled")
		return m, nil
	case "tab", "down", "ctrl+n":
		return m, m.moveNewProjectSelection(1)
	case "shift+tab", "up", "ctrl+p":
		return m, m.moveNewProjectSelection(-1)
	case "enter":
		preview := m.currentNewProjectPreview()
		if dialog.PreviewPending && strings.TrimSpace(preview.Name) == "" {
			m.status = "Checking project path..."
			return m, nil
		}
		if preview.Error != "" {
			m.status = preview.Error
			return m, nil
		}
		if !preview.Ready {
			m.status = "Project path and name are required"
			return m, nil
		}
		provider := dialog.explicitProvider()
		req := service.CreateOrAttachProjectRequest{
			ParentPath:             preview.ParentPath,
			Name:                   preview.Name,
			CreateGitRepo:          dialog.CreateGitRepo,
			PreferredSessionSource: modelSessionSourceFromCodexProvider(provider),
		}
		m.closeNewProjectDialog("")
		if preview.Exists && preview.ExistingDir {
			m.status = "Adding existing folder to the list..."
		} else {
			m.status = "Creating project..."
		}
		return m, m.createOrAttachProjectCmd(req, provider)
	case " ", "x":
		if dialog.Selected == newProjectFieldGitRepo {
			dialog.CreateGitRepo = !dialog.CreateGitRepo
			return m, nil
		}
		if dialog.Selected == newProjectFieldAssistant {
			m.cycleNewProjectAssistant(1)
			return m, nil
		}
	case "right", "l":
		if dialog.Selected == newProjectFieldAssistant {
			m.cycleNewProjectAssistant(1)
			return m, nil
		}
	case "left", "h":
		if dialog.Selected == newProjectFieldAssistant {
			m.cycleNewProjectAssistant(-1)
			return m, nil
		}
	}

	switch dialog.Selected {
	case newProjectFieldPath:
		previous := dialog.PathInput.Value()
		input, cmd := dialog.PathInput.Update(msg)
		dialog.PathInput = input
		if dialog.PathInput.Value() != previous {
			dialog.PathManuallyEdited = true
			return m, batchCmds(cmd, m.refreshNewProjectPreview(), m.refreshNewProjectPathSuggestions())
		}
		return m, cmd
	case newProjectFieldName:
		previous := dialog.NameInput.Value()
		input, cmd := dialog.NameInput.Update(msg)
		dialog.NameInput = input
		if dialog.NameInput.Value() != previous {
			return m, batchCmds(cmd, m.refreshNewProjectPreview())
		}
		return m, cmd
	default:
		return m, nil
	}
}

func (m *Model) moveNewProjectSelection(delta int) tea.Cmd {
	dialog := m.newProjectDialog
	if dialog == nil || delta == 0 {
		return nil
	}
	index := dialog.Selected + delta
	if index < newProjectFieldPath {
		index = newProjectFieldGitRepo
	}
	if index > newProjectFieldGitRepo {
		index = newProjectFieldPath
	}
	return m.setNewProjectSelection(index)
}

func (m *Model) setNewProjectSelection(index int) tea.Cmd {
	dialog := m.newProjectDialog
	if dialog == nil {
		return nil
	}
	if index < newProjectFieldPath {
		index = newProjectFieldPath
	}
	if index > newProjectFieldGitRepo {
		index = newProjectFieldGitRepo
	}
	dialog.Selected = index
	dialog.PathInput.Blur()
	dialog.NameInput.Blur()
	switch index {
	case newProjectFieldPath:
		dialog.PathInput.CursorEnd()
		return dialog.PathInput.Focus()
	case newProjectFieldName:
		dialog.NameInput.CursorEnd()
		return dialog.NameInput.Focus()
	default:
		return nil
	}
}

func (m *Model) cycleNewProjectAssistant(delta int) {
	dialog := m.newProjectDialog
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
	dialog.ProviderExplicit = true
	dialog.ProviderDefaultLabel = ""
}

func (m Model) createOrAttachProjectCmd(req service.CreateOrAttachProjectRequest, provider codexapp.Provider) tea.Cmd {
	if m.svc == nil {
		return func() tea.Msg {
			return newProjectResultMsg{err: fmt.Errorf("service unavailable")}
		}
	}
	return func() tea.Msg {
		ctx, cancel := m.actionContext(tuiProjectActionTimeout)
		defer cancel()
		result, err := m.svc.CreateOrAttachProject(ctx, req)
		err = timeoutActionError(err, tuiProjectActionTimeout, "creating or adding the project")
		return newProjectResultMsg{result: result, provider: explicitEmbeddedProvider(provider), err: err}
	}
}

func (s newProjectDialogState) explicitProvider() codexapp.Provider {
	return explicitEmbeddedProvider(s.Provider)
}

func (m Model) defaultNewProjectParentPath() string {
	if recentParents := m.visibleNewProjectRecentParents(); len(recentParents) > 0 {
		return recentParents[0]
	}
	if m.homeDirFn != nil {
		if home, err := m.homeDirFn(); err == nil && strings.TrimSpace(home) != "" {
			return filepath.Clean(home)
		}
	}
	return "."
}

func (m Model) visibleNewProjectRecentParents() []string {
	if len(m.newProjectRecentParents) == 0 {
		return nil
	}
	out := make([]string, 0, len(m.newProjectRecentParents))
	for _, path := range m.newProjectRecentParents {
		if strings.TrimSpace(path) == "" {
			continue
		}
		out = append(out, path)
	}
	return out
}

func newNewProjectTextInput(value string, charLimit int) textinput.Model {
	input := textinput.New()
	input.Prompt = ""
	input.SetValue(value)
	input.CharLimit = charLimit
	return input
}

func configureNewProjectPathInput(input *textinput.Model) {
	if input == nil {
		return
	}
	input.ShowSuggestions = true
	input.KeyMap.AcceptSuggestion = key.NewBinding(key.WithKeys("right"))
	input.KeyMap.NextSuggestion = key.NewBinding(key.WithKeys("alt+down"))
	input.KeyMap.PrevSuggestion = key.NewBinding(key.WithKeys("alt+up"))
}

func (m Model) currentNewProjectPreview() newProjectPreview {
	dialog := m.newProjectDialog
	if dialog == nil {
		return newProjectPreview{}
	}
	return dialog.Preview
}

func newProjectPathSuggestionAt(input textinput.Model, index int) (string, bool) {
	if index < 0 || index >= newProjectPathSuggestionLimit {
		return "", false
	}
	suggestions := input.MatchedSuggestions()
	if index >= len(suggestions) {
		return "", false
	}
	return suggestions[index], true
}

type newProjectPreviewProbe struct {
	parentPath string
	name       string
}

func (m *Model) refreshNewProjectPreview() tea.Cmd {
	dialog := m.newProjectDialog
	if dialog == nil {
		return nil
	}
	dialog.PreviewSeq++
	preview, probe, needsProbe := buildNewProjectPreviewBase(
		m.homeDirFn,
		dialog.PathInput.Value(),
		dialog.NameInput.Value(),
		dialog.PathManuallyEdited,
	)
	dialog.Preview = preview
	dialog.PreviewPending = needsProbe
	if !needsProbe {
		return nil
	}
	return m.inspectNewProjectPreviewCmd(dialog.PreviewSeq, preview, probe)
}

func (m *Model) refreshNewProjectPathSuggestions() tea.Cmd {
	dialog := m.newProjectDialog
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
	dialog.PathSuggestionsPending = true
	return m.loadNewProjectPathSuggestionsCmd(seq, rawPath)
}

func (m Model) loadNewProjectPathSuggestionsCmd(seq int64, rawPath string) tea.Cmd {
	settings := m.currentSettingsBaseline()
	ctx := newProjectPathSuggestionContext{
		RecentParents: append([]string(nil), m.newProjectRecentParents...),
		IncludePaths:  append([]string(nil), settings.IncludePaths...),
		Projects:      append([]model.ProjectSummary(nil), m.allProjects...),
	}
	return func() tea.Msg {
		return newProjectPathSuggestionsMsg{
			seq:    seq,
			result: newProjectPathSuggestionItems(m.homeDirFn, rawPath, newProjectPathSuggestionLimit, ctx),
		}
	}
}

func buildNewProjectPreviewBase(homeDirFn func() (string, error), rawPath, rawName string, pathManuallyEdited bool) (newProjectPreview, newProjectPreviewProbe, bool) {
	parentPath := normalizeNewProjectPathInput(homeDirFn, rawPath)
	name := strings.TrimSpace(rawName)
	fullPath := filepath.Clean(filepath.Join(parentPath, "<name>"))
	if name != "" {
		fullPath = filepath.Clean(filepath.Join(parentPath, name))
	} else if pathManuallyEdited && strings.TrimSpace(parentPath) != "" {
		fullPath = parentPath
	}

	preview := newProjectPreview{
		ParentPath: parentPath,
		Name:       name,
		FullPath:   fullPath,
	}
	if strings.TrimSpace(parentPath) == "" {
		preview.Error = "Project path is required"
		return preview, newProjectPreviewProbe{}, false
	}
	if name == "" {
		if !pathManuallyEdited {
			return preview, newProjectPreviewProbe{}, false
		}
		return preview, newProjectPreviewProbe{parentPath: parentPath}, true
	}
	if !validNewProjectFolderName(name) {
		preview.Error = "Project name must be a single folder name"
		return preview, newProjectPreviewProbe{}, false
	}
	preview.Ready = true
	return preview, newProjectPreviewProbe{
		parentPath: parentPath,
		name:       name,
	}, true
}

func (m Model) inspectNewProjectPreviewCmd(seq int64, base newProjectPreview, probe newProjectPreviewProbe) tea.Cmd {
	return func() tea.Msg {
		preview := base
		if strings.TrimSpace(probe.name) == "" {
			if inferred, ok := deriveNewProjectPreviewFromExistingPath(probe.parentPath); ok {
				preview = inferred
			}
			return newProjectPreviewMsg{seq: seq, preview: preview}
		}
		return newProjectPreviewMsg{
			seq:     seq,
			preview: inspectNewProjectPreviewPath(base),
		}
	}
}

func inspectNewProjectPreviewPath(preview newProjectPreview) newProjectPreview {
	info, err := os.Stat(preview.FullPath)
	switch {
	case err == nil:
		preview.Exists = true
		preview.ExistingDir = info.IsDir()
		if !preview.ExistingDir {
			preview.Error = "Path already exists and is not a directory"
		}
	case errors.Is(err, os.ErrNotExist):
	default:
		preview.Error = fmt.Sprintf("Unable to inspect path: %v", err)
	}
	preview.Ready = preview.Error == ""
	return preview
}

func newProjectExistingPathSuggestions(homeDirFn func() (string, error), raw string, limit int) []string {
	result := newProjectPathSuggestionItems(homeDirFn, raw, limit, newProjectPathSuggestionContext{})
	return newProjectPathSuggestionStrings(result.Suggestions)
}

func newProjectPathSuggestionItems(homeDirFn func() (string, error), raw string, limit int, ctx newProjectPathSuggestionContext) newProjectPathSuggestionsResult {
	displayPath := trimNewProjectWrappingQuotes(strings.TrimSpace(raw))
	if limit <= 0 || displayPath == "" {
		return newProjectPathSuggestionsResult{}
	}

	inspectPath := newProjectInspectPath(homeDirFn, displayPath)
	collector := newNewProjectPathSuggestionCollector(homeDirFn, displayPath, limit, ctx)
	collector.addSourcePaths(ctx.RecentParents, newProjectPathSuggestionRecent)
	collector.addSourcePaths(ctx.IncludePaths, newProjectPathSuggestionScope)
	collector.addProjectParentPaths(ctx.Projects)
	collector.addFilesystemPaths(inspectPath)
	return collector.result()
}

func newProjectPathSuggestionStrings(suggestions []newProjectPathSuggestion) []string {
	if len(suggestions) == 0 {
		return nil
	}
	out := make([]string, 0, len(suggestions))
	for _, suggestion := range suggestions {
		if path := strings.TrimSpace(suggestion.Path); path != "" {
			out = append(out, path)
		}
	}
	return out
}

type newProjectPathSuggestionCollector struct {
	homeDirFn   func() (string, error)
	displayPath string
	limit       int
	ctx         newProjectPathSuggestionContext
	seen        map[string]struct{}
	out         []newProjectPathSuggestion
	hidden      int
}

func newNewProjectPathSuggestionCollector(homeDirFn func() (string, error), displayPath string, limit int, ctx newProjectPathSuggestionContext) *newProjectPathSuggestionCollector {
	return &newProjectPathSuggestionCollector{
		homeDirFn:   homeDirFn,
		displayPath: trimNewProjectWrappingQuotes(strings.TrimSpace(displayPath)),
		limit:       limit,
		ctx:         ctx,
		seen:        make(map[string]struct{}),
	}
}

func (c *newProjectPathSuggestionCollector) result() newProjectPathSuggestionsResult {
	return newProjectPathSuggestionsResult{
		Suggestions: append([]newProjectPathSuggestion(nil), c.out...),
		HiddenCount: c.hidden,
	}
}

func (c *newProjectPathSuggestionCollector) addSourcePaths(paths []string, source newProjectPathSuggestionSource) {
	for _, path := range paths {
		c.addSourcePath(path, source, nil)
	}
}

func (c *newProjectPathSuggestionCollector) addProjectParentPaths(projects []model.ProjectSummary) {
	for _, project := range projects {
		projectPath := strings.TrimSpace(project.Path)
		if projectPath == "" || project.Forgotten || project.Archived || !projectSummaryActive(project) {
			continue
		}
		parentPath := filepath.Dir(filepath.Clean(projectPath))
		c.addSourcePath(parentPath, newProjectPathSuggestionProjectParent, []string{
			project.Name,
			project.Path,
			filepath.Base(project.Path),
		})
	}
}

func (c *newProjectPathSuggestionCollector) addSourcePath(rawPath string, source newProjectPathSuggestionSource, privacyValues []string) {
	inspectPath := newProjectInspectPath(c.homeDirFn, rawPath)
	if strings.TrimSpace(inspectPath) == "" {
		return
	}
	displayPath := newProjectDisplayPathForCandidate(c.homeDirFn, c.displayPath, inspectPath)
	c.addDisplayPath(displayPath, inspectPath, source, privacyValues, true)
}

func (c *newProjectPathSuggestionCollector) addFilesystemPaths(inspectPath string) {
	if strings.TrimSpace(inspectPath) == "" {
		return
	}
	if newProjectPathHasTrailingSeparator(c.displayPath) || newProjectPathIsDir(inspectPath) {
		displayPrefix := ensureNewProjectTrailingSeparator(c.displayPath)
		children := newProjectChildDirectorySuggestions(filepath.Clean(inspectPath), displayPrefix, "")
		before := len(c.out)
		for _, child := range children {
			c.addDisplayPath(child, newProjectInspectPath(c.homeDirFn, child), newProjectPathSuggestionFolder, nil, false)
		}
		if len(c.out) == before && newProjectPathIsDir(inspectPath) {
			c.addDisplayPath(ensureNewProjectTrailingSeparator(c.displayPath), inspectPath, newProjectPathSuggestionFolder, nil, false)
		}
		return
	}

	dirPath := filepath.Dir(filepath.Clean(inspectPath))
	namePrefix := filepath.Base(inspectPath)
	displayPrefix, displayNamePrefix := splitNewProjectDisplayPath(c.displayPath)
	if displayNamePrefix != "" {
		namePrefix = displayNamePrefix
	}
	for _, child := range newProjectChildDirectorySuggestions(dirPath, displayPrefix, namePrefix) {
		c.addDisplayPath(child, newProjectInspectPath(c.homeDirFn, child), newProjectPathSuggestionFolder, nil, false)
	}
}

func (c *newProjectPathSuggestionCollector) addDisplayPath(displayPath, inspectPath string, source newProjectPathSuggestionSource, privacyValues []string, skipExactInput bool) {
	displayPath = ensureNewProjectTrailingSeparator(strings.TrimSpace(displayPath))
	if displayPath == "" || !newProjectSuggestionCompletesInput(c.displayPath, displayPath) {
		return
	}
	inspectPath = filepath.Clean(strings.TrimSpace(inspectPath))
	if inspectPath == "" || inspectPath == "." {
		return
	}
	if skipExactInput && newProjectSamePath(c.homeDirFn, c.displayPath, displayPath) {
		return
	}
	key := filepath.Clean(inspectPath)
	if _, ok := c.seen[key]; ok {
		return
	}
	c.seen[key] = struct{}{}
	if len(c.out) >= c.limit {
		return
	}
	c.out = append(c.out, newProjectPathSuggestion{
		Path:   displayPath,
		Source: source,
	})
}

func newProjectChildDirectorySuggestions(dirPath, displayPrefix, namePrefix string) []string {
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return nil
	}
	namePrefixLower := strings.ToLower(namePrefix)
	showHidden := strings.HasPrefix(namePrefix, ".")
	suggestions := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if name == "." || name == ".." {
			continue
		}
		if !showHidden && namePrefix == "" && strings.HasPrefix(name, ".") {
			continue
		}
		if namePrefixLower != "" && !strings.HasPrefix(strings.ToLower(name), namePrefixLower) {
			continue
		}
		if !newProjectDirEntryIsDir(dirPath, entry) {
			continue
		}
		suggestions = append(suggestions, joinNewProjectDisplayPath(displayPrefix, name))
	}
	return suggestions
}

func newProjectDirEntryIsDir(parent string, entry os.DirEntry) bool {
	if entry.IsDir() {
		return true
	}
	if entry.Type()&os.ModeSymlink == 0 {
		return false
	}
	info, err := os.Stat(filepath.Join(parent, entry.Name()))
	return err == nil && info.IsDir()
}

func newProjectInspectPath(homeDirFn func() (string, error), raw string) string {
	path := trimNewProjectWrappingQuotes(strings.TrimSpace(raw))
	if path == "" {
		return ""
	}
	if expanded := expandNewProjectHomePath(homeDirFn, path); expanded != "" {
		path = expanded
	}
	if absPath, err := filepath.Abs(path); err == nil {
		path = absPath
	}
	return filepath.Clean(path)
}

func newProjectDisplayPathForCandidate(homeDirFn func() (string, error), rawInput, candidatePath string) string {
	candidatePath = filepath.Clean(strings.TrimSpace(candidatePath))
	if candidatePath == "" || candidatePath == "." {
		return ""
	}
	rawInput = trimNewProjectWrappingQuotes(strings.TrimSpace(rawInput))
	if strings.HasPrefix(rawInput, "~") {
		if homePath, ok := newProjectHomePath(homeDirFn); ok {
			if displayPath, ok := newProjectHomeRelativeDisplayPath(homePath, candidatePath); ok {
				return ensureNewProjectTrailingSeparator(displayPath)
			}
		}
	}
	return ensureNewProjectTrailingSeparator(candidatePath)
}

func newProjectHomePath(homeDirFn func() (string, error)) (string, bool) {
	if homeDirFn == nil {
		return "", false
	}
	home, err := homeDirFn()
	if err != nil || strings.TrimSpace(home) == "" {
		return "", false
	}
	return filepath.Clean(home), true
}

func newProjectHomeRelativeDisplayPath(homePath, candidatePath string) (string, bool) {
	homePath = filepath.Clean(strings.TrimSpace(homePath))
	candidatePath = filepath.Clean(strings.TrimSpace(candidatePath))
	if homePath == "" || candidatePath == "" {
		return "", false
	}
	relPath, err := filepath.Rel(homePath, candidatePath)
	if err != nil {
		return "", false
	}
	if relPath == "." {
		return "~", true
	}
	if relPath == ".." || strings.HasPrefix(relPath, ".."+string(os.PathSeparator)) || filepath.IsAbs(relPath) {
		return "", false
	}
	return "~/" + filepath.ToSlash(relPath), true
}

func newProjectSuggestionCompletesInput(input, suggestion string) bool {
	input = strings.ToLower(trimNewProjectWrappingQuotes(strings.TrimSpace(input)))
	suggestion = strings.ToLower(strings.TrimSpace(suggestion))
	return input == "" || strings.HasPrefix(suggestion, input)
}

func newProjectSamePath(homeDirFn func() (string, error), left, right string) bool {
	left = newProjectInspectPath(homeDirFn, left)
	right = newProjectInspectPath(homeDirFn, right)
	return left != "" && right != "" && filepath.Clean(left) == filepath.Clean(right)
}

func newProjectPathComponents(path string) []string {
	path = filepath.Clean(strings.TrimSpace(path))
	if path == "" || path == "." {
		return nil
	}
	volume := filepath.VolumeName(path)
	path = strings.TrimPrefix(path, volume)
	parts := strings.FieldsFunc(path, func(r rune) bool {
		return r == '/' || r == '\\'
	})
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if strings.TrimSpace(part) != "" {
			out = append(out, part)
		}
	}
	return out
}

func newProjectPathIsDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func splitNewProjectDisplayPath(path string) (string, string) {
	index := strings.LastIndexAny(path, `/\`)
	if index < 0 {
		return "", path
	}
	return path[:index+1], path[index+1:]
}

func joinNewProjectDisplayPath(prefix, name string) string {
	return ensureNewProjectTrailingSeparator(prefix) + name + newProjectDisplaySeparator(prefix)
}

func ensureNewProjectTrailingSeparator(path string) string {
	if path == "" || newProjectPathHasTrailingSeparator(path) {
		return path
	}
	return path + newProjectDisplaySeparator(path)
}

func newProjectPathHasTrailingSeparator(path string) bool {
	return strings.HasSuffix(path, "/") || strings.HasSuffix(path, "\\")
}

func newProjectDisplaySeparator(path string) string {
	if strings.Contains(path, "\\") && !strings.Contains(path, "/") {
		return "\\"
	}
	return string(os.PathSeparator)
}

func normalizeNewProjectPathInput(homeDirFn func() (string, error), raw string) string {
	path := strings.TrimSpace(raw)
	if path == "" {
		return ""
	}
	path = trimNewProjectWrappingQuotes(path)
	if expanded := expandNewProjectHomePath(homeDirFn, path); expanded != "" {
		path = expanded
	}
	if absPath, err := filepath.Abs(path); err == nil {
		path = absPath
	}
	return filepath.Clean(path)
}

func trimNewProjectWrappingQuotes(path string) string {
	path = strings.TrimSpace(path)
	if len(path) >= 2 {
		if (path[0] == '\'' && path[len(path)-1] == '\'') || (path[0] == '"' && path[len(path)-1] == '"') {
			return strings.TrimSpace(path[1 : len(path)-1])
		}
	}
	return path
}

func validNewProjectFolderName(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.ContainsRune(name, '/') && !strings.ContainsRune(name, '\\') && filepath.Base(name) == name
}

func deriveNewProjectPreviewFromExistingPath(path string) (newProjectPreview, bool) {
	info, err := os.Stat(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return newProjectPreview{}, false
	case err != nil:
		return newProjectPreview{
			ParentPath: path,
			FullPath:   path,
			Error:      fmt.Sprintf("Unable to inspect path: %v", err),
		}, true
	case !info.IsDir():
		return newProjectPreview{
			ParentPath: path,
			FullPath:   path,
			Exists:     true,
			Error:      "Path already exists and is not a directory",
		}, true
	}

	name := filepath.Base(path)
	if !validNewProjectFolderName(name) || name == string(os.PathSeparator) {
		return newProjectPreview{
			ParentPath:  path,
			FullPath:    path,
			Exists:      true,
			ExistingDir: true,
			Error:       "Project name is required unless the path ends with a folder name",
		}, true
	}

	return newProjectPreview{
		ParentPath:          filepath.Dir(path),
		Name:                name,
		FullPath:            path,
		Ready:               true,
		Exists:              true,
		ExistingDir:         true,
		NameDerivedFromPath: true,
	}, true
}

func expandNewProjectHomePath(homeDirFn func() (string, error), path string) string {
	if strings.TrimSpace(path) == "" || path[0] != '~' || homeDirFn == nil {
		return path
	}
	home, err := homeDirFn()
	if err != nil || strings.TrimSpace(home) == "" {
		return path
	}
	if path == "~" {
		return home
	}
	if strings.HasPrefix(path, "~/") || strings.HasPrefix(path, "~\\") {
		return filepath.Join(home, path[2:])
	}
	return path
}

func (m Model) renderNewProjectOverlay(body string, bodyW, bodyH int) string {
	panel := m.renderNewProjectPanel(bodyW)
	panelWidth := lipgloss.Width(panel)
	panelHeight := lipgloss.Height(panel)
	left := max(0, (bodyW-panelWidth)/2)
	top := max(0, (bodyH-panelHeight)/4)
	return overlayBlock(body, panel, bodyW, bodyH, left, top)
}

func (m Model) renderNewProjectPanel(bodyW int) string {
	panelWidth := min(bodyW, min(max(60, bodyW-10), 98))
	panelInnerWidth := max(28, panelWidth-4)
	return renderDialogPanel(panelWidth, panelInnerWidth, m.renderNewProjectContent(panelInnerWidth))
}

func (m Model) renderNewProjectContent(width int) string {
	dialog := m.newProjectDialog
	if dialog == nil {
		return ""
	}
	preview := m.currentNewProjectPreview()
	labelWidth := max(12, min(18, width/4))
	inputWidth := max(12, width-labelWidth-1)

	lines := []string{
		commandPaletteTitleStyle.Render("New Project"),
		commandPaletteHintStyle.Render("Create a new folder, or add an existing one even before any Codex/OpenCode activity exists there."),
		"",
		m.renderNewProjectInputRow("Path", dialog.Selected == newProjectFieldPath, labelWidth, inputWidth, dialog.PathInput),
		m.renderNewProjectInputRow("Name", dialog.Selected == newProjectFieldName, labelWidth, inputWidth, dialog.NameInput),
		m.renderNewProjectAssistantRow(labelWidth, inputWidth),
	}
	if statusLine := m.newProjectAssistantStatusLine(width); statusLine != "" {
		lines = append(lines, statusLine)
	}
	lines = append(lines,
		m.renderNewProjectGitRow(labelWidth, inputWidth),
		"",
		detailLabelStyle.Render("Full path:"),
		lipgloss.NewStyle().Width(width).Render(detailValueStyle.Render(preview.FullPath)),
	)

	if dialog.Selected == newProjectFieldPath {
		if suggestions := m.renderNewProjectPathSuggestions(width); len(suggestions) > 0 {
			lines = append(lines, "")
			lines = append(lines, suggestions...)
		}
	}

	lines = append(lines, m.renderNewProjectStatus(preview, width)...)

	recentParents := m.visibleNewProjectRecentParents()
	if len(recentParents) > 0 && len(dialog.PathInput.MatchedSuggestions()) == 0 && dialog.PathSuggestionHidden == 0 {
		lines = append(lines, "")
		lines = append(lines, commandPaletteTitleStyle.Render("Recent Paths"))
		lines = append(lines, commandPaletteHintStyle.Render("Alt+1..3 applies a remembered parent path."))
		for i, path := range recentParents {
			label := fmt.Sprintf("Alt+%d ", i+1)
			rowStyle := commandPaletteRowStyle
			if normalizeNewProjectPathInput(m.homeDirFn, dialog.PathInput.Value()) == filepath.Clean(path) {
				rowStyle = commandPaletteSelectStyle
			}
			lines = append(lines, rowStyle.Width(width).Render(label+truncateText(path, max(12, width-len(label)))))
		}
	}

	lines = append(lines, "")
	lines = append(lines, renderNewProjectActions(preview))
	return strings.Join(lines, "\n")
}

func (m Model) renderNewProjectPathSuggestions(width int) []string {
	dialog := m.newProjectDialog
	if dialog == nil {
		return nil
	}
	suggestions := dialog.PathInput.MatchedSuggestions()
	if len(suggestions) == 0 {
		if dialog.PathSuggestionsPending {
			return []string{
				commandPaletteTitleStyle.Render("Path Suggestions"),
				commandPaletteHintStyle.Render("Looking for matching folders..."),
			}
		}
		if dialog.PathSuggestionHidden > 0 {
			return []string{
				commandPaletteTitleStyle.Render("Path Suggestions"),
				commandPaletteHintStyle.Render(newProjectHiddenSuggestionText(dialog.PathSuggestionHidden)),
			}
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
		rowPrefix := label + source + " "
		row := rowPrefix + truncateText(suggestions[i], max(12, width-len(rowPrefix)))
		rowStyle := commandPaletteRowStyle
		if i == selected {
			rowStyle = commandPaletteSelectStyle
		}
		lines = append(lines, rowStyle.Width(width).Render(row))
	}
	return lines
}

func newProjectPathSuggestionForPath(suggestions []newProjectPathSuggestion, path string) (newProjectPathSuggestion, bool) {
	for _, suggestion := range suggestions {
		if suggestion.Path == path {
			return suggestion, true
		}
	}
	return newProjectPathSuggestion{}, false
}

func (s newProjectPathSuggestionSource) Label() string {
	switch s {
	case newProjectPathSuggestionRecent:
		return "recent"
	case newProjectPathSuggestionScope:
		return "scope"
	case newProjectPathSuggestionProjectParent:
		return "project"
	case newProjectPathSuggestionFolder:
		return "folder"
	default:
		return "folder"
	}
}

func newProjectHiddenSuggestionText(count int) string {
	if count <= 1 {
		return "1 private path suggestion hidden while /privacy is on."
	}
	return fmt.Sprintf("%d private path suggestions hidden while /privacy is on.", count)
}

func (m Model) renderNewProjectInputRow(label string, selected bool, labelWidth, inputWidth int, input textinput.Model) string {
	rowLabel := "  " + label
	labelStyle := detailLabelStyle
	if selected {
		rowLabel = "> " + label
		labelStyle = commandPalettePickStyle
	}
	input.Width = inputWidth
	return labelStyle.Width(labelWidth).Render(truncateText(rowLabel, labelWidth)) + " " + input.View()
}

func (m Model) renderNewProjectGitRow(labelWidth, inputWidth int) string {
	dialog := m.newProjectDialog
	if dialog == nil {
		return ""
	}
	label := "  Git repo"
	labelStyle := detailLabelStyle
	if dialog.Selected == newProjectFieldGitRepo {
		label = "> Git repo"
		labelStyle = commandPalettePickStyle
	}
	value := "[ ] initialize when creating a new folder"
	if dialog.CreateGitRepo {
		value = "[x] initialize when creating a new folder"
	}
	return labelStyle.Width(labelWidth).Render(truncateText(label, labelWidth)) + " " + truncateText(value, inputWidth)
}

func (m Model) renderNewProjectAssistantRow(labelWidth, inputWidth int) string {
	dialog := m.newProjectDialog
	if dialog == nil {
		return ""
	}
	label := "  Assistant"
	labelStyle := detailLabelStyle
	if dialog.Selected == newProjectFieldAssistant {
		label = "> Assistant"
		labelStyle = commandPalettePickStyle
	}
	buttons := make([]string, 0, len(embeddedLaunchProviderOptions()))
	for _, provider := range embeddedLaunchProviderOptions() {
		buttons = append(buttons, renderDialogButton(provider.Label(), dialog.Provider.Normalized() == provider))
	}
	value := strings.Join(buttons, " ")
	if label := strings.TrimSpace(dialog.ProviderDefaultLabel); label != "" {
		value += " " + detailMutedStyle.Render(label)
	} else if !dialog.ProviderExplicit {
		value += " " + detailMutedStyle.Render("default")
	}
	return labelStyle.Width(labelWidth).Render(truncateText(label, labelWidth)) + " " + fitStyledWidth(value, inputWidth)
}

func (m Model) newProjectAssistantStatusLine(width int) string {
	dialog := m.newProjectDialog
	if dialog == nil {
		return ""
	}
	statusLine := m.todoCopyProviderStatusLine(dialog.Provider, m.currentSettingsBaseline())
	if strings.TrimSpace(statusLine) == "" {
		return ""
	}
	return lipgloss.NewStyle().Width(width).Render(detailField("Agent status", statusLine))
}

func (m Model) renderNewProjectStatus(preview newProjectPreview, width int) []string {
	var lines []string
	switch {
	case preview.Error != "":
		lines = append(lines, detailWarningStyle.Render(preview.Error))
	case m.newProjectDialog != nil && m.newProjectDialog.PreviewPending:
		lines = append(lines, commandPaletteHintStyle.Render("Checking the project path in the background..."))
	case preview.NameDerivedFromPath:
		lines = append(lines, commandPaletteHintStyle.Render(fmt.Sprintf("Name left blank. Using existing folder name %q from the provided path.", preview.Name)))
		lines = append(lines, commandPaletteHintStyle.Render("Enter will add that folder to the list directly."))
		if m.newProjectDialog != nil && m.newProjectDialog.CreateGitRepo {
			lines = append(lines, commandPaletteHintStyle.Render("Git init only runs when a new folder is created here."))
		}
	case preview.Exists && preview.ExistingDir:
		lines = append(lines, commandPaletteHintStyle.Render("Folder already exists. Enter will add it to the list instead of creating it."))
		if m.newProjectDialog != nil && m.newProjectDialog.CreateGitRepo {
			lines = append(lines, commandPaletteHintStyle.Render("Git init only runs when the folder is created here."))
		}
	case preview.Ready:
		lines = append(lines, commandPaletteHintStyle.Render("Folder does not exist yet. Enter will create it and add it to the list."))
	default:
		lines = append(lines, commandPaletteHintStyle.Render("Enter a parent path and a single-folder project name, or paste an existing folder path and leave Name blank."))
	}
	if width > 0 && len(lines) > 0 {
		for i, line := range lines {
			lines[i] = lipgloss.NewStyle().Width(width).Render(line)
		}
	}
	return lines
}

func renderNewProjectActions(preview newProjectPreview) string {
	primary := "create"
	if preview.Exists && preview.ExistingDir {
		primary = "add"
	}
	actions := []string{
		renderDialogAction("Enter", primary, commitActionKeyStyle, commitActionTextStyle),
		renderDialogAction("Tab", "next", navigateActionKeyStyle, navigateActionTextStyle),
		renderDialogAction("Space", "toggle", pushActionKeyStyle, pushActionTextStyle),
		renderDialogAction("Esc", "cancel", cancelActionKeyStyle, cancelActionTextStyle),
	}
	return strings.Join(actions, "   ")
}
