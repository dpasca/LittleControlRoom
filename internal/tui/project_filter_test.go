package tui

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lcroom/internal/commands"
	"lcroom/internal/config"
	"lcroom/internal/events"
	"lcroom/internal/model"
	"lcroom/internal/service"
	"lcroom/internal/store"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

func TestDispatchFilterCommandOpensDialog(t *testing.T) {
	m := Model{
		width:  100,
		height: 24,
	}

	updated, cmd := m.dispatchCommand(commands.Invocation{Kind: commands.KindFilter})
	got := updated.(Model)
	if got.projectFilterDialog == nil {
		t.Fatalf("expected project filter dialog to open")
	}
	if got.projectFilterDialog.Input.Value() != "" {
		t.Fatalf("filter input = %q, want empty", got.projectFilterDialog.Input.Value())
	}
	if cmd == nil {
		t.Fatalf("opening the project filter dialog should return a focus command")
	}
}

func TestDispatchCategoryCommandOpensDialog(t *testing.T) {
	m := Model{
		width:  100,
		height: 24,
		projectCategories: []model.ProjectCategory{{
			ID:   "cat_client",
			Name: "Client",
		}},
	}

	updated, cmd := m.dispatchCommand(commands.Invocation{Kind: commands.KindCategory, Canonical: "/category"})
	got := updated.(Model)
	if got.categoryDialog == nil {
		t.Fatalf("expected category dialog to open")
	}
	if got.categoryDialog.Mode != categoryDialogModeActions {
		t.Fatalf("category dialog mode = %v, want actions", got.categoryDialog.Mode)
	}
	if cmd != nil {
		t.Fatalf("opening the category dialog should not need an async command")
	}
}

func TestCategoryDialogRendersColoredActionHints(t *testing.T) {
	prevProfile := lipgloss.ColorProfile()
	prevDarkBackground := lipgloss.HasDarkBackground()
	lipgloss.SetColorProfile(termenv.ANSI256)
	lipgloss.SetHasDarkBackground(true)
	t.Cleanup(func() {
		lipgloss.SetColorProfile(prevProfile)
		lipgloss.SetHasDarkBackground(prevDarkBackground)
	})

	m := Model{
		width:  100,
		height: 24,
		categoryDialog: &categoryDialogState{
			Mode:     categoryDialogModeActions,
			Selected: 0,
			Input:    newCategoryNameInput(),
			Marked:   map[string]bool{},
		},
	}

	rendered := m.renderCategoryDialogContent(72, 20)
	if !strings.Contains(rendered, commitActionKeyStyle.Render("Enter")) {
		t.Fatalf("category dialog should color primary Enter key: %q", rendered)
	}
	if !strings.Contains(rendered, cancelActionKeyStyle.Render("Esc")) {
		t.Fatalf("category dialog should color cancel Esc key: %q", rendered)
	}
	stripped := strings.Join(strings.Fields(ansi.Strip(rendered)), " ")
	if !strings.Contains(stripped, "Enter choose") || !strings.Contains(stripped, "Esc close") {
		t.Fatalf("category dialog missing action labels: %q", stripped)
	}

	m.categoryDialog.Mode = categoryDialogModeMoveItems
	m.categoryDialog.Input = newCategoryFilterInput()
	m.categoryDialog.MoveItems = []categoryMoveItem{{
		Key:      "project:/tmp/demo",
		Label:    "demo",
		Resource: model.CategoryResourceRef{Kind: model.CategoryResourceProject, ID: "/tmp/demo"},
	}}
	m.categoryDialog.Marked = map[string]bool{"project:/tmp/demo": true}
	rendered = m.renderCategoryDialogContent(72, 20)
	if !strings.Contains(rendered, pushActionKeyStyle.Render("Space")) {
		t.Fatalf("category move dialog should color Space key: %q", rendered)
	}
	if stripped = strings.Join(strings.Fields(ansi.Strip(rendered)), " "); !strings.Contains(stripped, "Enter destination") {
		t.Fatalf("category move dialog missing destination hint: %q", stripped)
	}

	footer := m.categoryDialogFooterLabel()
	if !strings.Contains(footer, footerPrimaryKeyStyle.Render("Enter")) {
		t.Fatalf("category footer should color primary Enter key: %q", footer)
	}
	if !strings.Contains(footer, footerExitKeyStyle.Render("Esc")) {
		t.Fatalf("category footer should color Esc key: %q", footer)
	}
}

func TestDispatchFilterCommandAppliesTransientFilter(t *testing.T) {
	m := Model{
		allProjects: []model.ProjectSummary{
			{Name: "LittleControlRoom", Path: "/tmp/LittleControlRoom", LastActivity: time.Date(2026, 3, 19, 10, 0, 0, 0, time.UTC)},
			{Name: "helper-tools", Path: "/tmp/helper-tools", LastActivity: time.Date(2026, 3, 19, 9, 0, 0, 0, time.UTC)},
		},
		projects: []model.ProjectSummary{
			{Name: "LittleControlRoom", Path: "/tmp/LittleControlRoom", LastActivity: time.Date(2026, 3, 19, 10, 0, 0, 0, time.UTC)},
			{Name: "helper-tools", Path: "/tmp/helper-tools", LastActivity: time.Date(2026, 3, 19, 9, 0, 0, 0, time.UTC)},
		},
		sortMode:   sortByAttention,
		visibility: visibilityAllFolders,
		width:      100,
		height:     24,
	}

	updated, _ := m.dispatchCommand(commands.Invocation{Kind: commands.KindFilter, Filter: "helper"})
	got := updated.(Model)
	if got.projectFilter != "helper" {
		t.Fatalf("projectFilter = %q, want helper", got.projectFilter)
	}
	if len(got.projects) != 1 || got.projects[0].Name != "helper-tools" {
		t.Fatalf("visible projects = %#v, want only helper-tools", got.projects)
	}
	if !strings.Contains(got.status, `Filter "helper" matched 1 project`) {
		t.Fatalf("status = %q, want filter match status", got.status)
	}
}

func TestPressingFOpensProjectFilterDialog(t *testing.T) {
	m := Model{
		width:         100,
		height:        24,
		projectFilter: "helper",
	}

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	got := updated.(Model)
	if got.projectFilterDialog == nil {
		t.Fatalf("expected pressing f to open the project filter dialog")
	}
	if got.projectFilterDialog.Input.Value() != "helper" {
		t.Fatalf("filter input = %q, want existing filter", got.projectFilterDialog.Input.Value())
	}
	if cmd == nil {
		t.Fatalf("pressing f should return a focus command")
	}
}

func TestProjectFilterDialogUpdatesVisibleProjectsAsYouType(t *testing.T) {
	m := Model{
		allProjects: []model.ProjectSummary{
			{Name: "LittleControlRoom", Path: "/tmp/LittleControlRoom", LastActivity: time.Date(2026, 3, 19, 10, 0, 0, 0, time.UTC)},
			{Name: "helper-tools", Path: "/tmp/helper-tools", LastActivity: time.Date(2026, 3, 19, 9, 0, 0, 0, time.UTC)},
		},
		projects: []model.ProjectSummary{
			{Name: "LittleControlRoom", Path: "/tmp/LittleControlRoom", LastActivity: time.Date(2026, 3, 19, 10, 0, 0, 0, time.UTC)},
			{Name: "helper-tools", Path: "/tmp/helper-tools", LastActivity: time.Date(2026, 3, 19, 9, 0, 0, 0, time.UTC)},
		},
		sortMode:   sortByAttention,
		visibility: visibilityAllFolders,
		width:      100,
		height:     24,
	}
	_ = m.openProjectFilterDialog()

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	got := updated.(Model)
	if got.projectFilter != "h" {
		t.Fatalf("projectFilter = %q, want h", got.projectFilter)
	}
	if len(got.projects) != 1 || got.projects[0].Name != "helper-tools" {
		t.Fatalf("visible projects after typing = %#v, want only helper-tools", got.projects)
	}
}

func TestRenderedListAndFooterShowActiveProjectFilter(t *testing.T) {
	m := Model{
		allProjects: []model.ProjectSummary{
			{Name: "helper-tools", Path: "/tmp/helper-tools", PresentOnDisk: true, LastActivity: time.Date(2026, 3, 19, 9, 0, 0, 0, time.UTC)},
		},
		projects: []model.ProjectSummary{
			{Name: "helper-tools", Path: "/tmp/helper-tools", PresentOnDisk: true, LastActivity: time.Date(2026, 3, 19, 9, 0, 0, 0, time.UTC)},
		},
		projectFilter: "helper",
		sortMode:      sortByAttention,
		visibility:    visibilityAllFolders,
		width:         160,
		height:        28,
	}

	list := ansi.Strip(m.renderProjectList(140, 12))
	if !strings.Contains(list, `filter:"helper"`) {
		t.Fatalf("rendered list = %q, want active filter in header", list)
	}

	footer := ansi.Strip(m.renderFooter(120))
	if !strings.Contains(footer, `Filter "helper"`) {
		t.Fatalf("rendered footer = %q, want active filter segment", footer)
	}

	m.projects = nil
	noMatches := ansi.Strip(m.renderProjectList(100, 12))
	if !strings.Contains(noMatches, `No projects match "helper"`) {
		t.Fatalf("rendered no-match state = %q, want filter-specific empty state", noMatches)
	}
}

func TestProjectArchiveTabSwitchesVisibleProjects(t *testing.T) {
	active := model.ProjectSummary{
		Name:                "active-demo",
		Path:                "/tmp/active-demo",
		InScope:             true,
		PresentOnDisk:       true,
		LatestSessionFormat: "modern",
	}
	archived := model.ProjectSummary{
		Name:                "archived-demo",
		Path:                "/tmp/archived-demo",
		InScope:             true,
		Archived:            true,
		PresentOnDisk:       true,
		LatestSessionFormat: "modern",
	}
	m := Model{
		allProjects:      []model.ProjectSummary{active},
		archivedProjects: []model.ProjectSummary{archived},
		sortMode:         sortByAttention,
		visibility:       visibilityAllFolders,
		width:            100,
		height:           24,
	}
	m.rebuildProjectList("")
	if len(m.projects) != 1 || m.projects[0].Path != active.Path {
		t.Fatalf("active projects = %#v, want active project", m.projects)
	}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	got := updated.(Model)
	if got.archiveMode != projectArchiveArchived {
		t.Fatalf("archiveMode = %q, want archived", got.archiveMode)
	}
	if len(got.projects) != 1 || got.projects[0].Path != archived.Path {
		t.Fatalf("archived projects = %#v, want archived project", got.projects)
	}
	if !strings.Contains(got.status, "Archived") {
		t.Fatalf("status = %q, want archived tab status", got.status)
	}
}

func TestProjectCategoryTabSwitchesVisibleProjects(t *testing.T) {
	main := model.ProjectSummary{
		Name:          "main-demo",
		Path:          "/tmp/main-demo",
		InScope:       true,
		PresentOnDisk: true,
	}
	client := model.ProjectSummary{
		Name:          "client-demo",
		Path:          "/tmp/client-demo",
		CategoryID:    "cat_client",
		CategoryName:  "Client",
		InScope:       true,
		PresentOnDisk: true,
	}
	archived := model.ProjectSummary{
		Name:          "archived-demo",
		Path:          "/tmp/archived-demo",
		InScope:       true,
		Archived:      true,
		PresentOnDisk: true,
	}
	m := Model{
		allProjects:      []model.ProjectSummary{main, client},
		archivedProjects: []model.ProjectSummary{archived},
		projectCategories: []model.ProjectCategory{{
			ID:   "cat_client",
			Name: "Client",
		}},
		sortMode:   sortByAttention,
		visibility: visibilityAllFolders,
		width:      100,
		height:     24,
	}
	m.rebuildProjectList("")
	if len(m.projects) != 1 || m.projects[0].Path != main.Path {
		t.Fatalf("main projects = %#v, want uncategorized project", m.projects)
	}

	updated, _ := m.dispatchCommand(commands.Invocation{Kind: commands.KindTab, Tab: commands.ProjectTabCategory, CategoryName: "client"})
	got := updated.(Model)
	if got.archiveMode != projectArchiveCategory || got.selectedCategoryID != "cat_client" {
		t.Fatalf("tab state = %q/%q, want category/cat_client", got.archiveMode, got.selectedCategoryID)
	}
	if len(got.projects) != 1 || got.projects[0].Path != client.Path {
		t.Fatalf("client projects = %#v, want categorized project", got.projects)
	}

	updated, _ = got.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	got = updated.(Model)
	if got.archiveMode != projectArchiveArchived {
		t.Fatalf("archiveMode after cycling from category = %q, want archived", got.archiveMode)
	}
	if len(got.projects) != 1 || got.projects[0].Path != archived.Path {
		t.Fatalf("archived projects = %#v, want archived project", got.projects)
	}
}

func TestRenderedListShowsProjectArchiveTabs(t *testing.T) {
	m := Model{
		allProjects: []model.ProjectSummary{{
			Name:          "active-demo",
			Path:          "/tmp/active-demo",
			InScope:       true,
			PresentOnDisk: true,
		}},
		archivedProjects: []model.ProjectSummary{{
			Name:          "archived-demo",
			Path:          "/tmp/archived-demo",
			InScope:       true,
			Archived:      true,
			PresentOnDisk: true,
		}},
		projects: []model.ProjectSummary{{
			Name:          "active-demo",
			Path:          "/tmp/active-demo",
			InScope:       true,
			PresentOnDisk: true,
		}},
		sortMode:   sortByAttention,
		visibility: visibilityAllFolders,
		width:      120,
		height:     24,
	}

	list := ansi.Strip(m.renderProjectList(120, 12))
	if !strings.Contains(list, "[Main 1]") || !strings.Contains(list, "Archived 1") || !strings.Contains(list, "/category") {
		t.Fatalf("rendered list = %q, want main/archived tabs and category hint", list)
	}
	lines := strings.Split(list, "\n")
	if len(lines) < 2 {
		t.Fatalf("rendered list = %q, want tabs and header rows", list)
	}
	if !strings.Contains(lines[0], "a cycle") {
		t.Fatalf("tab row = %q, want advertised cycle hotkey", lines[0])
	}
	if strings.Contains(lines[0], "ATTN") || !strings.Contains(lines[1], "ATTN") {
		t.Fatalf("rendered list = %q, want tabs on their own row above the header", list)
	}

	m.archiveMode = projectArchiveArchived
	m.projects = append([]model.ProjectSummary(nil), m.archivedProjects...)
	list = ansi.Strip(m.renderProjectList(120, 12))
	if !strings.Contains(list, "[Archived 1]") {
		t.Fatalf("rendered archived list = %q, want selected archived tab", list)
	}
}

func TestDispatchProjectArchiveTabCommand(t *testing.T) {
	active := model.ProjectSummary{
		Name:          "active-demo",
		Path:          "/tmp/active-demo",
		InScope:       true,
		PresentOnDisk: true,
	}
	archived := model.ProjectSummary{
		Name:          "archived-demo",
		Path:          "/tmp/archived-demo",
		InScope:       true,
		Archived:      true,
		PresentOnDisk: true,
	}
	m := Model{
		allProjects:      []model.ProjectSummary{active},
		archivedProjects: []model.ProjectSummary{archived},
		sortMode:         sortByAttention,
		visibility:       visibilityAllFolders,
		width:            100,
		height:           24,
	}
	m.rebuildProjectList("")

	updated, _ := m.dispatchCommand(commands.Invocation{Kind: commands.KindTab, Tab: commands.ProjectTabArchived})
	got := updated.(Model)
	if got.archiveMode != projectArchiveArchived {
		t.Fatalf("archiveMode = %q, want archived", got.archiveMode)
	}
	if len(got.projects) != 1 || got.projects[0].Path != archived.Path {
		t.Fatalf("archived projects = %#v, want archived project", got.projects)
	}

	updated, _ = got.dispatchCommand(commands.Invocation{Kind: commands.KindTab, Tab: commands.ProjectTabActive})
	got = updated.(Model)
	if got.archiveMode != projectArchiveActive {
		t.Fatalf("archiveMode = %q, want active", got.archiveMode)
	}
	if len(got.projects) != 1 || got.projects[0].Path != active.Path {
		t.Fatalf("active projects = %#v, want active project", got.projects)
	}
}

func TestDispatchArchiveAndUnarchiveProjectCommands(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	projectPath := filepath.Join(t.TempDir(), "archive-demo")
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:          projectPath,
		Name:          "archive-demo",
		Status:        model.StatusIdle,
		PresentOnDisk: true,
		InScope:       true,
		UpdatedAt:     time.Now(),
	}); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	svc := service.New(config.Default(), st, events.NewBus(), nil)

	active, err := st.ListProjects(ctx, false)
	if err != nil {
		t.Fatalf("list active projects: %v", err)
	}
	m := Model{
		ctx:         ctx,
		svc:         svc,
		allProjects: active,
		projects:    active,
		sortMode:    sortByAttention,
		visibility:  visibilityAllFolders,
	}
	_, archiveCmd := m.dispatchCommand(commands.Invocation{Kind: commands.KindArchive, Canonical: "/archive"})
	if archiveCmd == nil {
		t.Fatalf("/archive should return an action command")
	}
	archiveMsg, ok := archiveCmd().(actionMsg)
	if !ok {
		t.Fatalf("/archive command returned unexpected message")
	}
	if archiveMsg.err != nil {
		t.Fatalf("/archive action error = %v", archiveMsg.err)
	}
	if archiveMsg.status != `Archived "archive-demo"` {
		t.Fatalf("archive status = %q, want archive confirmation", archiveMsg.status)
	}
	active, err = st.ListProjects(ctx, false)
	if err != nil {
		t.Fatalf("list active after archive: %v", err)
	}
	if len(active) != 0 {
		t.Fatalf("archived project should leave active list, got %#v", active)
	}
	all, err := st.ListProjects(ctx, true)
	if err != nil {
		t.Fatalf("list historical after archive: %v", err)
	}
	if len(all) != 1 || !all[0].Archived {
		t.Fatalf("historical projects = %#v, want archived project", all)
	}

	m.archiveMode = projectArchiveArchived
	m.archivedProjects = all
	m.projects = all
	_, unarchiveCmd := m.dispatchCommand(commands.Invocation{Kind: commands.KindUnarchive, Canonical: "/unarchive"})
	if unarchiveCmd == nil {
		t.Fatalf("/unarchive should return an action command")
	}
	unarchiveMsg, ok := unarchiveCmd().(actionMsg)
	if !ok {
		t.Fatalf("/unarchive command returned unexpected message")
	}
	if unarchiveMsg.err != nil {
		t.Fatalf("/unarchive action error = %v", unarchiveMsg.err)
	}
	if unarchiveMsg.status != `Unarchived "archive-demo"` {
		t.Fatalf("unarchive status = %q, want unarchive confirmation", unarchiveMsg.status)
	}
	active, err = st.ListProjects(ctx, false)
	if err != nil {
		t.Fatalf("list active after unarchive: %v", err)
	}
	if len(active) != 1 || active[0].Archived {
		t.Fatalf("unarchived project should return to active list, got %#v", active)
	}
}

func TestCategoryMoveSelectsProjectBelowAfterRefresh(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	above := model.ProjectState{Path: "/tmp/category-above", Name: "above", Status: model.StatusIdle, AttentionScore: 30, PresentOnDisk: true, InScope: true, UpdatedAt: now}
	moved := model.ProjectState{Path: "/tmp/category-moved", Name: "moved", Status: model.StatusIdle, AttentionScore: 20, PresentOnDisk: true, InScope: true, UpdatedAt: now}
	below := model.ProjectState{Path: "/tmp/category-below", Name: "below", Status: model.StatusIdle, AttentionScore: 10, PresentOnDisk: true, InScope: true, UpdatedAt: now}
	for _, state := range []model.ProjectState{above, moved, below} {
		if err := st.UpsertProjectState(ctx, state); err != nil {
			t.Fatalf("seed project %s: %v", state.Path, err)
		}
	}
	category, err := st.CreateProjectCategory(ctx, "Client")
	if err != nil {
		t.Fatalf("create category: %v", err)
	}
	svc := service.New(config.Default(), st, events.NewBus(), nil)
	active, err := st.ListProjects(ctx, false)
	if err != nil {
		t.Fatalf("list projects: %v", err)
	}
	m := Model{
		ctx:               ctx,
		svc:               svc,
		allProjects:       active,
		projectCategories: []model.ProjectCategory{category},
		sortMode:          sortByAttention,
		visibility:        visibilityAllFolders,
	}
	m.rebuildProjectList(moved.Path)
	if selected, ok := m.selectedProject(); !ok || selected.Path != moved.Path {
		t.Fatalf("selected project before move = %#v, want moved", selected)
	}

	updated, cmd := m.dispatchCommand(commands.Invocation{
		Kind:           commands.KindCategory,
		CategoryAction: commands.CategoryActionMove,
		CategoryName:   "Client",
		Canonical:      "/category move Client",
	})
	if cmd == nil {
		t.Fatalf("category move should return an action command")
	}
	action, ok := cmd().(actionMsg)
	if !ok {
		t.Fatalf("category move command returned %T, want actionMsg", action)
	}
	if action.err != nil {
		t.Fatalf("category move action error = %v", action.err)
	}
	if action.selectPath != below.Path {
		t.Fatalf("action selectPath = %q, want below project", action.selectPath)
	}

	afterAction, _ := updated.(Model).Update(action)
	got := afterAction.(Model)
	if got.preferredSelectPath != below.Path {
		t.Fatalf("preferredSelectPath = %q, want below project", got.preferredSelectPath)
	}

	active, err = st.ListProjects(ctx, false)
	if err != nil {
		t.Fatalf("list projects after move: %v", err)
	}
	afterReload, _ := got.Update(projectsMsg{
		projects:   active,
		categories: []model.ProjectCategory{category},
	})
	got = afterReload.(Model)
	if selected, ok := got.selectedProject(); !ok || selected.Path != below.Path {
		t.Fatalf("selected project after reload = %#v, want below", selected)
	}
	if len(got.projects) != 2 {
		t.Fatalf("visible main projects = %#v, want moved project filtered out", got.projects)
	}
}

func TestSplitProjectArchiveSummariesHidesOutOfScopeProject(t *testing.T) {
	active := model.ProjectSummary{Path: "/tmp/active", Name: "active", InScope: true}
	manual := model.ProjectSummary{Path: "/tmp/manual", Name: "manual", ManuallyAdded: true}
	archived := model.ProjectSummary{Path: "/tmp/archived", Name: "archived", InScope: true, Archived: true}
	outside := model.ProjectSummary{Path: "/tmp/outside", Name: "outside", InScope: false, Archived: false}

	activeProjects, archivedProjects := splitProjectArchiveSummaries([]model.ProjectSummary{active, manual, archived, outside})

	if len(activeProjects) != 2 || activeProjects[0].Path != active.Path || activeProjects[1].Path != manual.Path {
		t.Fatalf("active projects = %#v, want in-scope plus manually tracked projects", activeProjects)
	}
	if len(archivedProjects) != 1 || archivedProjects[0].Path != archived.Path {
		t.Fatalf("archived projects = %#v, want only explicitly archived project", archivedProjects)
	}
}

func TestDispatchUnarchiveProjectCommandLeavesOutOfScopeProjectHidden(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	projectPath := filepath.Join(t.TempDir(), "outside-scope-project")
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:          projectPath,
		Name:          "outside-scope-project",
		Status:        model.StatusIdle,
		PresentOnDisk: true,
		InScope:       false,
		Archived:      false,
		UpdatedAt:     time.Now(),
	}); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	svc := service.New(config.Default(), st, events.NewBus(), nil)
	all, err := st.ListProjects(ctx, true)
	if err != nil {
		t.Fatalf("list historical projects: %v", err)
	}
	if len(all) != 1 || all[0].Archived || all[0].InScope {
		t.Fatalf("historical projects = %#v, want out-of-scope project", all)
	}

	m := Model{
		ctx:        ctx,
		svc:        svc,
		projects:   all,
		sortMode:   sortByAttention,
		visibility: visibilityAllFolders,
	}
	updated, unarchiveCmd := m.dispatchCommand(commands.Invocation{Kind: commands.KindUnarchive, Canonical: "/unarchive"})
	got := updated.(Model)
	if unarchiveCmd != nil {
		t.Fatalf("/unarchive should not start an action for an out-of-scope project")
	}
	if got.status != `"outside-scope-project" is outside project scope` {
		t.Fatalf("status = %q, want outside-scope explanation", got.status)
	}
	active, err := st.ListProjects(ctx, false)
	if err != nil {
		t.Fatalf("list active after rejected unarchive: %v", err)
	}
	if len(active) != 0 {
		t.Fatalf("out-of-scope project should stay hidden from active list, got %#v", active)
	}
}
