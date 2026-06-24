package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lcroom/internal/codexapp"
	"lcroom/internal/commands"
	"lcroom/internal/config"
	"lcroom/internal/events"
	"lcroom/internal/model"
	"lcroom/internal/service"
	"lcroom/internal/store"

	tea "github.com/charmbracelet/bubbletea"
)

func applyNewProjectPreviewRefresh(t *testing.T, m Model) Model {
	t.Helper()

	cmd := m.refreshNewProjectPreview()
	if cmd == nil {
		return m
	}
	updated, nextCmd := m.Update(cmd())
	if nextCmd != nil {
		t.Fatalf("preview refresh should not schedule follow-up work")
	}
	return updated.(Model)
}

func TestDispatchNewProjectCommandOpensDialogWithRecentPathDefault(t *testing.T) {
	m := Model{
		width:                   100,
		height:                  28,
		homeDirFn:               func() (string, error) { return "/Users/tester", nil },
		newProjectRecentParents: []string{"/tmp/work"},
	}

	updated, cmd := m.dispatchCommand(commands.Invocation{Kind: commands.KindNewProject})
	got := updated.(Model)
	if got.newProjectDialog == nil {
		t.Fatalf("expected new project dialog to open")
	}
	if got.newProjectDialog.PathInput.Value() != "/tmp/work" {
		t.Fatalf("default path = %q, want recent path", got.newProjectDialog.PathInput.Value())
	}
	if cmd == nil {
		t.Fatalf("opening the dialog should focus the first field")
	}

	rendered := got.renderNewProjectContent(72)
	if !strings.Contains(rendered, "New Project") || !strings.Contains(rendered, "/tmp/work") {
		t.Fatalf("rendered dialog missing title or default path: %q", rendered)
	}
}

func TestNewProjectDialogCanPreselectAssistant(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	svc := service.New(config.Default(), st, events.NewBus(), nil)
	parent := t.TempDir()

	m := New(ctx, svc)
	m.width = 100
	m.height = 28
	m.homeDirFn = func() (string, error) { return parent, nil }

	updated, _ := m.dispatchCommand(commands.Invocation{Kind: commands.KindNewProject, Assistant: "opencode"})
	got := updated.(Model)
	if got.newProjectDialog == nil {
		t.Fatalf("expected dialog to open")
	}
	if got.newProjectDialog.Provider != codexapp.ProviderOpenCode || !got.newProjectDialog.ProviderExplicit {
		t.Fatalf("dialog provider = %q explicit=%v, want explicit OpenCode", got.newProjectDialog.Provider, got.newProjectDialog.ProviderExplicit)
	}
	got.newProjectDialog.PathInput.SetValue(parent)
	got.newProjectDialog.NameInput.SetValue("demo")
	got.newProjectDialog.CreateGitRepo = false
	got = applyNewProjectPreviewRefresh(t, got)

	updated, cmd := got.updateNewProjectMode(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if cmd == nil {
		t.Fatalf("enter should submit the new project dialog")
	}
	rawMsg := cmd()
	msg, ok := rawMsg.(newProjectResultMsg)
	if !ok {
		t.Fatalf("command returned %T, want newProjectResultMsg", rawMsg)
	}
	if msg.err != nil {
		t.Fatalf("create project error: %v", msg.err)
	}

	updated, _ = got.Update(msg)
	got = updated.(Model)
	provider, ok := got.embeddedLaunchProviderOverride(msg.result.ProjectPath)
	if !ok || provider != codexapp.ProviderOpenCode {
		t.Fatalf("launch provider override = (%q, %v), want OpenCode true", provider, ok)
	}
	summary, err := st.GetProjectSummary(ctx, msg.result.ProjectPath, false)
	if err != nil {
		t.Fatalf("GetProjectSummary() error = %v", err)
	}
	if summary.PreferredSessionSource != model.SessionSourceOpenCode {
		t.Fatalf("persisted preferred source = %q, want %q", summary.PreferredSessionSource, model.SessionSourceOpenCode)
	}
	if fresh := (Model{}).preferredEmbeddedProviderForProject(summary); fresh != codexapp.ProviderOpenCode {
		t.Fatalf("fresh model preferred provider = %q, want OpenCode", fresh)
	}
	if !strings.Contains(got.status, "Enter opens OpenCode") {
		t.Fatalf("status = %q, want OpenCode launch hint", got.status)
	}
}

func TestNewProjectDialogDefaultsToLastUsedAssistant(t *testing.T) {
	m := Model{
		width:     100,
		height:    28,
		homeDirFn: func() (string, error) { return "/Users/tester", nil },
	}
	m.rememberEmbeddedProvider(codexapp.ProviderClaudeCode)

	updated, _ := m.dispatchCommand(commands.Invocation{Kind: commands.KindNewProject})
	got := updated.(Model)
	if got.newProjectDialog == nil {
		t.Fatalf("expected dialog to open")
	}
	if got.newProjectDialog.Provider != codexapp.ProviderClaudeCode {
		t.Fatalf("dialog provider = %q, want last-used Claude Code", got.newProjectDialog.Provider)
	}
	if got.newProjectDialog.ProviderDefaultLabel != "last used" {
		t.Fatalf("default label = %q, want last used", got.newProjectDialog.ProviderDefaultLabel)
	}
}

func TestNewProjectDialogCyclesAssistantSelection(t *testing.T) {
	m := Model{
		width:     100,
		height:    28,
		homeDirFn: func() (string, error) { return "/Users/tester", nil },
	}
	updated, _ := m.dispatchCommand(commands.Invocation{Kind: commands.KindNewProject})
	got := updated.(Model)
	if got.newProjectDialog == nil {
		t.Fatalf("expected dialog to open")
	}
	got.newProjectDialog.Selected = newProjectFieldAssistant

	updated, cmd := got.updateNewProjectMode(tea.KeyMsg{Type: tea.KeyRight})
	got = updated.(Model)
	if cmd != nil {
		t.Fatalf("assistant cycle should not return a command")
	}
	if got.newProjectDialog.Provider != codexapp.ProviderOpenCode || !got.newProjectDialog.ProviderExplicit {
		t.Fatalf("dialog provider = %q explicit=%v, want explicit OpenCode", got.newProjectDialog.Provider, got.newProjectDialog.ProviderExplicit)
	}
}

func TestNewProjectDialogCreatesProjectAndSelectsIt(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	svc := service.New(config.Default(), st, events.NewBus(), nil)
	parent := t.TempDir()

	m := New(ctx, svc)
	m.width = 100
	m.height = 28
	m.homeDirFn = func() (string, error) { return parent, nil }

	updated, _ := m.dispatchCommand(commands.Invocation{Kind: commands.KindNewProject})
	got := updated.(Model)
	if got.newProjectDialog == nil {
		t.Fatalf("expected dialog to open")
	}
	got.newProjectDialog.PathInput.SetValue(parent)
	got.newProjectDialog.NameInput.SetValue("demo")
	got.newProjectDialog.CreateGitRepo = false
	got = applyNewProjectPreviewRefresh(t, got)

	updated, cmd := got.updateNewProjectMode(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if cmd == nil {
		t.Fatalf("enter should submit the new project dialog")
	}
	if got.status != "Creating project..." {
		t.Fatalf("status = %q, want creating notice", got.status)
	}

	msg := cmd()
	updated, loadCmd := got.Update(msg)
	got = updated.(Model)
	if got.newProjectDialog != nil {
		t.Fatalf("dialog should close after a successful create")
	}
	if loadCmd == nil {
		t.Fatalf("successful create should reload the project list")
	}

	msg = loadCmd()
	updated, _ = got.Update(msg)
	got = updated.(Model)
	selected, ok := got.selectedProject()
	if !ok {
		t.Fatalf("expected the created project to be selected")
	}
	wantPath := filepath.Join(parent, "demo")
	if selected.Path != wantPath {
		t.Fatalf("selected path = %q, want %q", selected.Path, wantPath)
	}
}

func TestNewProjectDialogAddsExistingDiscoveredRepoToAIFolders(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	parent := t.TempDir()
	projectPath := filepath.Join(parent, "portfolio")
	if err := os.MkdirAll(filepath.Join(projectPath, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir existing git project: %v", err)
	}
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:          projectPath,
		Name:          "portfolio",
		Status:        model.StatusIdle,
		PresentOnDisk: true,
		InScope:       true,
		UpdatedAt:     time.Now().UTC(),
	}); err != nil {
		t.Fatalf("upsert discovered project: %v", err)
	}

	svc := service.New(config.Default(), st, events.NewBus(), nil)
	m := New(ctx, svc)
	m.width = 100
	m.height = 28
	m.homeDirFn = func() (string, error) { return parent, nil }

	updated, _ := m.dispatchCommand(commands.Invocation{Kind: commands.KindNewProject})
	got := updated.(Model)
	got.newProjectDialog.PathInput.SetValue(parent)
	got.newProjectDialog.NameInput.SetValue("portfolio")
	got.newProjectDialog.CreateGitRepo = false
	got = applyNewProjectPreviewRefresh(t, got)

	updated, cmd := got.updateNewProjectMode(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if cmd == nil {
		t.Fatalf("enter should submit the existing project")
	}
	if got.status != "Adding existing folder to the list..." {
		t.Fatalf("status = %q, want existing-folder add notice", got.status)
	}

	msg := cmd()
	updated, loadCmd := got.Update(msg)
	got = updated.(Model)
	if loadCmd == nil {
		t.Fatalf("successful add should reload the project list")
	}

	updated, _ = got.Update(loadCmd())
	got = updated.(Model)
	selected, ok := got.selectedProject()
	if !ok {
		t.Fatalf("expected the existing repo to be visible in the default AI folders list")
	}
	if selected.Path != projectPath || !selected.ManuallyAdded {
		t.Fatalf("selected project = %#v, want manually added %q", selected, projectPath)
	}
}

func TestNewProjectDialogAltDigitAppliesRecentPath(t *testing.T) {
	m := Model{
		width:                   100,
		height:                  28,
		homeDirFn:               func() (string, error) { return "/Users/tester", nil },
		newProjectRecentParents: []string{"/tmp/one", "/tmp/two", "/tmp/three"},
	}
	updated, _ := m.dispatchCommand(commands.Invocation{Kind: commands.KindNewProject})
	got := updated.(Model)

	updated, cmd := got.updateNewProjectMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}, Alt: true})
	got = updated.(Model)
	got = drainCmdMsgs(got, cmd)
	if got.newProjectDialog.PathInput.Value() != "/tmp/two" {
		t.Fatalf("path input = %q, want /tmp/two", got.newProjectDialog.PathInput.Value())
	}
}

func TestNewProjectPathSuggestionsApplyExistingFolder(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	projectPath := filepath.Join(parent, "alpha")
	if err := os.MkdirAll(projectPath, 0o755); err != nil {
		t.Fatalf("mkdir project path: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(parent, "beta"), 0o755); err != nil {
		t.Fatalf("mkdir beta path: %v", err)
	}

	input := newNewProjectTextInput(filepath.Join(parent, "a"), 1024)
	configureNewProjectPathInput(&input)
	m := Model{
		width:     100,
		height:    28,
		homeDirFn: func() (string, error) { return "/Users/tester", nil },
		newProjectDialog: &newProjectDialogState{
			PathInput:          input,
			NameInput:          newNewProjectTextInput("", 256),
			Selected:           newProjectFieldPath,
			CreateGitRepo:      true,
			PathManuallyEdited: true,
		},
	}

	m = drainCmdMsgs(m, m.refreshNewProjectPathSuggestions())
	wantSuggestion := projectPath + string(os.PathSeparator)
	if suggestion, ok := newProjectPathSuggestionAt(m.newProjectDialog.PathInput, 0); !ok || suggestion != wantSuggestion {
		t.Fatalf("first path suggestion = %q, %t; want %q, true", suggestion, ok, wantSuggestion)
	}

	rendered := m.renderNewProjectContent(90)
	if !strings.Contains(rendered, "Path Suggestions") || !strings.Contains(rendered, "Alt+1") {
		t.Fatalf("rendered dialog missing path suggestions: %q", rendered)
	}
	nameIndex := strings.Index(rendered, "Name")
	fullPathIndex := strings.Index(rendered, "Full path:")
	suggestionIndex := strings.Index(rendered, "Path Suggestions")
	if nameIndex < 0 || fullPathIndex < 0 || suggestionIndex < 0 {
		t.Fatalf("rendered dialog missing expected sections: %q", rendered)
	}
	if suggestionIndex < nameIndex || suggestionIndex < fullPathIndex {
		t.Fatalf("path suggestions should render below the core project fields: %q", rendered)
	}

	updated, cmd := m.updateNewProjectMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}, Alt: true})
	m = updated.(Model)
	m = drainCmdMsgs(m, cmd)

	if got := m.newProjectDialog.PathInput.Value(); got != wantSuggestion {
		t.Fatalf("path input = %q, want %q", got, wantSuggestion)
	}
	preview := m.currentNewProjectPreview()
	if !preview.Ready || !preview.NameDerivedFromPath || preview.FullPath != projectPath {
		t.Fatalf("preview after applying suggestion = %#v, want existing alpha folder", preview)
	}
}

func TestNewProjectPathSuggestionsPreserveHomePrefix(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "dev"), 0o755); err != nil {
		t.Fatalf("mkdir dev path: %v", err)
	}

	suggestions := newProjectExistingPathSuggestions(func() (string, error) { return home, nil }, "~/d", 8)
	if len(suggestions) == 0 {
		t.Fatalf("expected home-relative suggestions")
	}
	if suggestions[0] != "~/dev/" {
		t.Fatalf("first suggestion = %q, want ~/dev/", suggestions[0])
	}
}

func TestNewProjectPathSuggestionsExpandExactExistingDirectory(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	for _, name := range []string{"alpha", "beta", "gamma"} {
		if err := os.MkdirAll(filepath.Join(parent, name), 0o755); err != nil {
			t.Fatalf("mkdir child path: %v", err)
		}
	}

	want := []string{
		filepath.Join(parent, "alpha") + string(os.PathSeparator),
		filepath.Join(parent, "beta") + string(os.PathSeparator),
		filepath.Join(parent, "gamma") + string(os.PathSeparator),
	}
	parentWithSeparator := parent + string(os.PathSeparator)
	for _, raw := range []string{parent, parentWithSeparator} {
		suggestions := newProjectExistingPathSuggestions(func() (string, error) { return "/Users/tester", nil }, raw, 8)
		if len(suggestions) != len(want) {
			t.Fatalf("suggestions for %q = %v, want %v", raw, suggestions, want)
		}
		for i := range want {
			if suggestions[i] != want[i] {
				t.Fatalf("suggestions for %q = %v, want %v", raw, suggestions, want)
			}
		}
	}
}

func TestNewProjectPathSuggestionsExpandExactHomeDirectory(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	repos := filepath.Join(home, "dev", "repos")
	for _, name := range []string{"LittleControlRoom", "OtherProject"} {
		if err := os.MkdirAll(filepath.Join(repos, name), 0o755); err != nil {
			t.Fatalf("mkdir repo path: %v", err)
		}
	}

	suggestions := newProjectExistingPathSuggestions(func() (string, error) { return home, nil }, "~/dev/repos", 8)
	want := []string{"~/dev/repos/LittleControlRoom/", "~/dev/repos/OtherProject/"}
	if len(suggestions) != len(want) {
		t.Fatalf("suggestions = %v, want %v", suggestions, want)
	}
	for i := range want {
		if suggestions[i] != want[i] {
			t.Fatalf("suggestions = %v, want %v", suggestions, want)
		}
	}
}

func TestNewProjectPathSuggestionsUseRecentScopeAndProjectParents(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	recent := filepath.Join(home, "scratch")
	scope := filepath.Join(home, "dev", "repos")
	projectParent := filepath.Join(home, "client-work")
	for _, path := range []string{recent, scope, projectParent} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", path, err)
		}
	}

	result := newProjectPathSuggestionItems(
		func() (string, error) { return home, nil },
		home,
		8,
		newProjectPathSuggestionContext{
			RecentParents: []string{recent},
			IncludePaths:  []string{scope},
			Projects: []model.ProjectSummary{
				{Path: filepath.Join(projectParent, "demo"), Name: "demo", InScope: true},
			},
		},
	)

	for _, want := range []newProjectPathSuggestion{
		{Path: recent + string(os.PathSeparator), Source: newProjectPathSuggestionRecent},
		{Path: scope + string(os.PathSeparator), Source: newProjectPathSuggestionScope},
		{Path: projectParent + string(os.PathSeparator), Source: newProjectPathSuggestionProjectParent},
	} {
		got, ok := newProjectPathSuggestionForPath(result.Suggestions, want.Path)
		if !ok {
			t.Fatalf("suggestions missing %s in %#v", want.Path, result.Suggestions)
		}
		if got.Source != want.Source {
			t.Fatalf("suggestion %s source = %s, want %s", want.Path, got.Source, want.Source)
		}
	}
}

func TestNewProjectPathSuggestionsRespectPrivacyMode(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	visible := filepath.Join(parent, "visible-client")
	private := filepath.Join(parent, "secret-client")
	for _, path := range []string{visible, private} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", path, err)
		}
	}

	settings := config.EditableSettingsFromAppConfig(config.Default())
	m := Model{
		width:                   100,
		height:                  28,
		homeDirFn:               func() (string, error) { return parent, nil },
		settingsBaseline:        &settings,
		privacyMode:             true,
		privacyPatterns:         []string{"*secret*"},
		newProjectRecentParents: []string{private, parent},
		newProjectDialog: &newProjectDialogState{
			PathInput:     newNewProjectTextInput(parent, 1024),
			NameInput:     newNewProjectTextInput("", 256),
			Selected:      newProjectFieldPath,
			CreateGitRepo: true,
		},
	}
	configureNewProjectPathInput(&m.newProjectDialog.PathInput)

	if got := m.defaultNewProjectParentPath(); got != parent {
		t.Fatalf("default parent path = %q, want privacy-visible recent parent %q", got, parent)
	}

	m = drainCmdMsgs(m, m.refreshNewProjectPathSuggestions())
	suggestions := m.newProjectDialog.PathInput.MatchedSuggestions()
	if len(suggestions) != 1 || suggestions[0] != visible+string(os.PathSeparator) {
		t.Fatalf("privacy-filtered suggestions = %v, want only visible client", suggestions)
	}
	if m.newProjectDialog.PathSuggestionHidden == 0 {
		t.Fatalf("expected hidden private suggestion count")
	}
	rendered := m.renderNewProjectContent(100)
	if strings.Contains(rendered, "secret-client") {
		t.Fatalf("rendered suggestions leaked private path: %q", rendered)
	}
	if !strings.Contains(rendered, "private path suggestions hidden") {
		t.Fatalf("rendered suggestions missing privacy hidden note: %q", rendered)
	}
}

func TestNewProjectPreviewDerivesNameFromQuotedExistingPathWhenNameBlank(t *testing.T) {
	t.Parallel()

	projectPath := filepath.Join(t.TempDir(), "Family Room", "Media", "2026_03_mothers_farm")
	if err := os.MkdirAll(projectPath, 0o755); err != nil {
		t.Fatalf("mkdir project path: %v", err)
	}

	m := Model{
		width:     100,
		height:    28,
		homeDirFn: func() (string, error) { return "/Users/tester", nil },
	}
	m.newProjectDialog = &newProjectDialogState{
		PathInput:          newNewProjectTextInput("'"+projectPath+"'", 1024),
		NameInput:          newNewProjectTextInput("", 256),
		CreateGitRepo:      true,
		PathManuallyEdited: true,
	}
	m = applyNewProjectPreviewRefresh(t, m)

	preview := m.currentNewProjectPreview()
	if !preview.Ready {
		t.Fatalf("expected preview to be ready, got %#v", preview)
	}
	if !preview.NameDerivedFromPath {
		t.Fatalf("expected preview to derive the project name from the path")
	}
	if preview.Name != "2026_03_mothers_farm" {
		t.Fatalf("preview name = %q, want %q", preview.Name, "2026_03_mothers_farm")
	}
	if preview.ParentPath != filepath.Dir(projectPath) {
		t.Fatalf("parent path = %q, want %q", preview.ParentPath, filepath.Dir(projectPath))
	}

	rendered := m.renderNewProjectContent(80)
	if !strings.Contains(rendered, "Using existing folder name") {
		t.Fatalf("rendered dialog missing derived-name hint: %q", rendered)
	}
}

func TestNewProjectPreviewDoesNotAutoDeriveNameForDefaultParentPath(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	m := Model{
		width:     100,
		height:    28,
		homeDirFn: func() (string, error) { return parent, nil },
	}
	m.newProjectDialog = &newProjectDialogState{
		PathInput: newNewProjectTextInput(parent, 1024),
		NameInput: newNewProjectTextInput("", 256),
	}
	m = applyNewProjectPreviewRefresh(t, m)

	preview := m.currentNewProjectPreview()
	if preview.Ready {
		t.Fatalf("default parent path should not be ready without a name: %#v", preview)
	}
	if preview.NameDerivedFromPath {
		t.Fatalf("default parent path should not derive a project name: %#v", preview)
	}
}

func TestNewProjectPreviewIgnoresStaleProbeResults(t *testing.T) {
	t.Parallel()

	firstPath := filepath.Join(t.TempDir(), "first-project")
	if err := os.MkdirAll(firstPath, 0o755); err != nil {
		t.Fatalf("mkdir first path: %v", err)
	}
	secondPath := filepath.Join(t.TempDir(), "second-project")
	if err := os.MkdirAll(secondPath, 0o755); err != nil {
		t.Fatalf("mkdir second path: %v", err)
	}

	m := Model{
		width:     100,
		height:    28,
		homeDirFn: func() (string, error) { return "/Users/tester", nil },
	}
	m.newProjectDialog = &newProjectDialogState{
		PathInput:          newNewProjectTextInput(firstPath, 1024),
		NameInput:          newNewProjectTextInput("", 256),
		PathManuallyEdited: true,
	}

	firstCmd := m.refreshNewProjectPreview()
	if firstCmd == nil {
		t.Fatal("first refresh should probe the filesystem")
	}
	m.newProjectDialog.PathInput.SetValue(secondPath)
	secondCmd := m.refreshNewProjectPreview()
	if secondCmd == nil {
		t.Fatal("second refresh should probe the filesystem")
	}

	updated, nextCmd := m.Update(firstCmd())
	if nextCmd != nil {
		t.Fatalf("stale preview result should not schedule follow-up work")
	}
	m = updated.(Model)
	if preview := m.currentNewProjectPreview(); preview.ParentPath != secondPath {
		t.Fatalf("stale preview should be ignored, got %#v", preview)
	}
	if !m.newProjectDialog.PreviewPending {
		t.Fatal("latest probe should still be pending after stale result is ignored")
	}

	updated, nextCmd = m.Update(secondCmd())
	if nextCmd != nil {
		t.Fatalf("latest preview result should not schedule follow-up work")
	}
	m = updated.(Model)
	preview := m.currentNewProjectPreview()
	if preview.Name != "second-project" {
		t.Fatalf("preview name = %q, want %q", preview.Name, "second-project")
	}
	if !preview.NameDerivedFromPath {
		t.Fatalf("expected latest preview to derive the project name from the path: %#v", preview)
	}
	if m.newProjectDialog.PreviewPending {
		t.Fatal("preview pending should clear after the latest probe completes")
	}
}
