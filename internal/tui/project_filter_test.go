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
			{Name: "helper-tools", Path: "/tmp/helper-tools", LastActivity: time.Date(2026, 3, 19, 9, 0, 0, 0, time.UTC)},
		},
		projects: []model.ProjectSummary{
			{Name: "helper-tools", Path: "/tmp/helper-tools", LastActivity: time.Date(2026, 3, 19, 9, 0, 0, 0, time.UTC)},
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
