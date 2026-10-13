package tui

import (
	"fmt"
	"strings"
	"testing"

	"lcroom/internal/model"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func mainListInputTestModel() Model {
	projects := make([]model.ProjectSummary, 64)
	for i := range projects {
		projects[i] = model.ProjectSummary{
			Path:          fmt.Sprintf("/tmp/project-%02d", i),
			Name:          fmt.Sprintf("project-%02d", i),
			PresentOnDisk: true,
		}
	}
	m := Model{
		width:       120,
		height:      24,
		projects:    projects,
		allProjects: projects,
		focusedPane: focusProjects,
		selected:    20,
	}
	m.ensureSelectionVisible()
	return m
}

func assertMainListSelectionVisible(t *testing.T, m Model) {
	t.Helper()
	layout := m.bodyLayout()
	height := layout.listPaneHeight - 2
	list := ansi.Strip(fitPaneContent(m.renderProjectList(layout.listContentWidth, height), layout.listContentWidth, height))
	if !strings.Contains(list, m.projects[m.selected].Name) {
		t.Fatalf("selected project %q should be visible:\n%s", m.projects[m.selected].Name, list)
	}
	if m.selected < m.offset || m.selected >= m.offset+m.rowsVisible() {
		t.Fatalf("selected row %d outside offset %d with %d visible rows", m.selected, m.offset, m.rowsVisible())
	}
}

func TestMainListMouseWheelScrollsAndKeepsSelectionVisible(t *testing.T) {
	m := mainListInputTestModel()
	beforeOffset := m.offset
	updated, cmd := m.Update(tea.MouseMsg{X: 20, Y: 5, Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown})
	got := normalizeUpdateModel(updated)
	if got.selected != m.selected+3 || got.offset != beforeOffset+3 {
		t.Fatalf("wheel down: selected=%d offset=%d, want selected=%d offset=%d", got.selected, got.offset, m.selected+3, beforeOffset+3)
	}
	if cmd == nil || got.selectedDetailRequestSeq != m.selectedDetailRequestSeq+1 {
		t.Fatal("wheel navigation should queue the existing debounced detail refresh")
	}
	assertMainListSelectionVisible(t, got)

	updated, _ = got.Update(tea.MouseMsg{X: 20, Y: 5, Action: tea.MouseActionPress, Button: tea.MouseButtonWheelUp})
	got = normalizeUpdateModel(updated)
	if got.selected != m.selected {
		t.Fatalf("wheel up: selected=%d, want %d", got.selected, m.selected)
	}
	assertMainListSelectionVisible(t, got)
}

func TestMainListMouseFocusRoutesPageKeysToList(t *testing.T) {
	for _, button := range []tea.MouseButton{tea.MouseButtonLeft, tea.MouseButtonWheelDown} {
		for _, focus := range []paneFocus{focusDetail, focusRuntime} {
			t.Run(fmt.Sprintf("button=%d/focus=%s", button, focus), func(t *testing.T) {
				m := mainListInputTestModel()
				m.focusedPane = focus
				updated, _ := m.Update(tea.MouseMsg{X: 20, Y: 3, Action: tea.MouseActionPress, Button: button})
				got := normalizeUpdateModel(updated)
				if got.focusedPane != focusProjects {
					t.Fatalf("mouse over list: focus=%q, want projects", got.focusedPane)
				}
				before := got.selected
				step := got.rowsVisible()
				updated, _ = got.Update(tea.KeyMsg{Type: tea.KeyPgDown})
				got = normalizeUpdateModel(updated)
				if got.selected != before+step {
					t.Fatalf("page down after mouse focus: selected=%d, want %d", got.selected, before+step)
				}
				assertMainListSelectionVisible(t, got)
			})
		}
	}
}

func TestMainListPageKeysMatchRenderedPageSize(t *testing.T) {
	for _, height := range []int{14, 24, 40} {
		t.Run(fmt.Sprintf("height=%d", height), func(t *testing.T) {
			m := mainListInputTestModel()
			m.height = height
			m.ensureSelectionVisible()
			layout := m.bodyLayout()
			innerHeight := layout.listPaneHeight - 2
			list := ansi.Strip(fitPaneContent(m.renderProjectList(layout.listContentWidth, innerHeight), layout.listContentWidth, innerHeight))
			visible := strings.Count(list, "\n") + 1 - 2 // tabs and column header
			if m.rowsVisible() != visible {
				t.Fatalf("navigation counts %d visible rows, rendered %d", m.rowsVisible(), visible)
			}
			updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
			got := normalizeUpdateModel(updated)
			if got.selected != m.selected+visible {
				t.Fatalf("page down: selected=%d, want %d", got.selected, m.selected+visible)
			}
			assertMainListSelectionVisible(t, got)
			updated, _ = got.Update(tea.KeyMsg{Type: tea.KeyPgUp})
			got = normalizeUpdateModel(updated)
			if got.selected != m.selected {
				t.Fatalf("page up: selected=%d, want %d", got.selected, m.selected)
			}
			assertMainListSelectionVisible(t, got)
		})
	}
}

func TestMainListScrollClampsAtEndsAndHandlesEmptyList(t *testing.T) {
	for _, count := range []int{0, 1, 64} {
		for _, direction := range []int{-1, 1} {
			m := mainListInputTestModel()
			m.projects = m.projects[:count]
			m.selected = 0
			button, key := tea.MouseButtonWheelUp, tea.KeyPgUp
			if direction > 0 {
				m.selected = max(0, count-1)
				button, key = tea.MouseButtonWheelDown, tea.KeyPgDown
			}
			m.ensureSelectionVisible()
			updated, cmd := m.Update(tea.MouseMsg{X: 20, Y: 5, Action: tea.MouseActionPress, Button: button})
			got := normalizeUpdateModel(updated)
			if got.selected != m.selected || got.offset != m.offset || cmd != nil {
				t.Fatalf("wheel at end with %d projects: selected=%d offset=%d cmd=%t", count, got.selected, got.offset, cmd != nil)
			}
			updated, cmd = got.Update(tea.KeyMsg{Type: key})
			got = normalizeUpdateModel(updated)
			if got.selected != m.selected || got.offset != m.offset || cmd != nil {
				t.Fatalf("page at end with %d projects: selected=%d offset=%d cmd=%t", count, got.selected, got.offset, cmd != nil)
			}
		}
	}
}

func TestMainListIgnoresUnscopedMouseEvents(t *testing.T) {
	for _, msg := range []tea.MouseMsg{
		{X: 20, Y: 0, Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown},
		{X: 20, Y: 18, Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown},
		{X: -1, Y: 5, Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown},
		{X: 120, Y: 5, Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown},
		{X: 20, Y: 5, Action: tea.MouseActionRelease, Button: tea.MouseButtonWheelDown},
		{X: 20, Y: 5, Action: tea.MouseActionMotion, Button: tea.MouseButtonWheelDown},
		{X: 20, Y: 5, Action: tea.MouseActionPress, Button: tea.MouseButtonWheelRight},
	} {
		m := mainListInputTestModel()
		m.focusedPane = focusDetail
		updated, cmd := m.Update(msg)
		got := normalizeUpdateModel(updated)
		if got.selected != m.selected || got.offset != m.offset || got.focusedPane != m.focusedPane || cmd != nil {
			t.Fatalf("mouse %#v changed dashboard state", msg)
		}
	}
}

func TestDashboardMouseDoesNotReachPanesBehindDialogs(t *testing.T) {
	for _, open := range []func(*Model){
		func(m *Model) { m.commandMode = true },
		func(m *Model) { m.settingsMode = true },
		func(m *Model) { m.setupMode = true },
		func(m *Model) { m.showPerf = true },
		func(m *Model) { m.projectFilterDialog = &projectFilterDialogState{} },
		func(m *Model) { m.worktreeRemoveConfirm = &worktreeRemoveConfirmState{} },
		func(m *Model) { m.quitConfirm = &quitConfirmState{} },
	} {
		m := mainListInputTestModel()
		m.focusedPane = focusDetail
		open(&m)
		updated, cmd := m.Update(tea.MouseMsg{X: 20, Y: 5, Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown})
		got := normalizeUpdateModel(updated)
		if got.selected != m.selected || got.offset != m.offset || got.focusedPane != m.focusedPane || cmd != nil {
			t.Fatal("wheel through a dialog changed dashboard state")
		}
		layout := m.bodyLayout()
		updated, cmd = m.Update(tea.MouseMsg{X: layout.detailPaneWidth + 3, Y: layout.listPaneHeight + 2, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
		got = normalizeUpdateModel(updated)
		if got.focusedPane != m.focusedPane || cmd != nil {
			t.Fatal("click through a dialog reached the runtime pane")
		}
	}
}
