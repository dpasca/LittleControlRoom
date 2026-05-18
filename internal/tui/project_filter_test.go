package tui

import (
	"strings"
	"testing"
	"time"

	"lcroom/internal/commands"
	"lcroom/internal/model"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
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
		InScope:             false,
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
			InScope:       false,
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
	if !strings.Contains(list, "[Active 1]") || !strings.Contains(list, "Archived 1") {
		t.Fatalf("rendered list = %q, want active and archived tabs", list)
	}

	m.archiveMode = projectArchiveArchived
	m.projects = append([]model.ProjectSummary(nil), m.archivedProjects...)
	list = ansi.Strip(m.renderProjectList(120, 12))
	if !strings.Contains(list, "[Archived 1]") {
		t.Fatalf("rendered archived list = %q, want selected archived tab", list)
	}
}
