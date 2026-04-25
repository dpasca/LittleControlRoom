package tui

import (
	"fmt"
	"strings"

	bossui "lcroom/internal/boss"

	tea "github.com/charmbracelet/bubbletea"
)

func (m Model) openBossMode() (tea.Model, tea.Cmd) {
	m.bossMode = true
	m.bossModel = bossui.NewEmbeddedWithViewContext(m.ctx, m.svc, m.bossViewContext())
	m.status = "Boss mode open. Esc returns to the classic TUI."
	if m.width > 0 && m.height > 0 {
		updated, _ := m.bossModel.Update(tea.WindowSizeMsg{Width: m.width, Height: m.height})
		m.bossModel = normalizeBossModel(updated)
	}
	return m, m.bossModel.Init()
}

func (m *Model) closeBossMode(status string) {
	m.bossMode = false
	m.bossModel = bossui.Model{}
	if status != "" {
		m.status = status
	}
	m.syncDetailViewport(false)
}

func (m Model) updateBossModeMessage(msg tea.Msg) (tea.Model, tea.Cmd) {
	updated, cmd := m.bossModel.Update(msg)
	m.bossModel = normalizeBossModel(updated)
	return m, cmd
}

func (m Model) bossViewContext() bossui.ViewContext {
	view := bossui.ViewContext{
		Active:              true,
		Embedded:            true,
		Loading:             m.loading,
		AllProjectCount:     len(m.allProjects),
		VisibleProjectCount: len(m.projects),
		SelectedIndex:       m.selected,
		FocusedPane:         string(m.focusedPane),
		SortMode:            string(m.sortMode),
		Visibility:          string(m.visibility),
		Filter:              strings.TrimSpace(m.projectFilter),
		Status:              strings.TrimSpace(m.status),
	}
	if project, ok := m.selectedProject(); ok {
		view.SelectedProject = bossui.ProjectViewFromSummary(project)
	}
	if path := strings.TrimSpace(m.detail.Summary.Path); path != "" {
		view.DetailProjectPath = path
		view.DetailReasonCount = len(m.detail.Reasons)
		view.DetailSessionCount = len(m.detail.Sessions)
		view.DetailRecentEvents = len(m.detail.RecentEvents)
		for _, item := range m.detail.Todos {
			if !item.Done {
				view.DetailOpenTODOCount++
			}
		}
		if summary := strings.TrimSpace(m.detail.Summary.LatestSessionSummary); summary != "" {
			view.DetailLatestSummary = summary
		} else {
			view.DetailLatestSummary = strings.TrimSpace(m.detail.Summary.LatestCompletedSessionSummary)
		}
	}
	return view
}

func normalizeBossModel(model tea.Model) bossui.Model {
	switch typed := model.(type) {
	case bossui.Model:
		return typed
	case *bossui.Model:
		if typed == nil {
			panic("boss mode update returned nil *boss.Model")
		}
		return *typed
	default:
		panic(fmt.Sprintf("boss mode update returned unsupported model type %T", model))
	}
}
