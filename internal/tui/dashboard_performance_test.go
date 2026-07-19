package tui

import (
	"fmt"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/viewport"

	"lcroom/internal/codexapp"
	"lcroom/internal/config"
	"lcroom/internal/model"
)

func benchmarkDashboardModel() Model {
	now := time.Date(2026, time.July, 14, 12, 0, 0, 0, time.UTC)
	projects := make([]model.ProjectSummary, 190)
	for i := range projects {
		path := fmt.Sprintf("/tmp/lcroom-bench/project-%03d", i)
		projects[i] = model.ProjectSummary{
			Path:                            path,
			Name:                            fmt.Sprintf("project-%03d", i),
			PresentOnDisk:                   true,
			LastActivity:                    now.Add(-time.Duration(i) * time.Minute),
			LatestSessionClassification:     model.ClassificationCompleted,
			LatestSessionClassificationType: model.SessionCategoryCompleted,
			LatestSessionSummary:            "Finished the current implementation and verification work.",
		}
	}
	snapshots := make(map[string]codexapp.Snapshot, 9)
	for i := 0; i < 9; i++ {
		path := projects[i].Path
		snapshots[path] = codexapp.Snapshot{
			Provider:       codexapp.ProviderCodex,
			ProjectPath:    path,
			Started:        true,
			Busy:           true,
			ActiveTurnID:   fmt.Sprintf("turn-%d", i),
			BusySince:      now.Add(-time.Duration(i+1) * time.Minute),
			LastActivityAt: now,
		}
	}
	settings := config.EditableSettings{
		ActiveThreshold: 20 * time.Minute,
		StuckThreshold:  2 * time.Hour,
	}
	m := Model{
		allProjects:       projects,
		projectCategories: []model.ProjectCategory{{ID: "one", Name: "One"}, {ID: "two", Name: "Two"}, {ID: "three", Name: "Three"}, {ID: "four", Name: "Four"}},
		codexSnapshots:    snapshots,
		width:             150,
		height:            38,
		nowFn:             func() time.Time { return now },
		sortMode:          sortByRecent,
		visibility:        visibilityAllFolders,
		archiveMode:       projectArchiveMain,
		focusedPane:       focusProjects,
		detailViewport:    viewport.New(0, 0),
		runtimeViewport:   viewport.New(0, 0),
		settingsBaseline:  &settings,
	}
	m.rebuildProjectList("")
	m.detail = model.ProjectDetail{Summary: projects[0]}
	m.syncDetailViewport(true)
	return m
}

func BenchmarkDashboardView(b *testing.B) {
	m := benchmarkDashboardModel()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.View()
	}
}

func BenchmarkDashboardNavigationAndView(b *testing.B) {
	m := benchmarkDashboardModel()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.selected = (m.selected + 1) % len(m.projects)
		m.ensureSelectionVisible()
		m.syncDetailViewport(true)
		_ = m.View()
	}
}
