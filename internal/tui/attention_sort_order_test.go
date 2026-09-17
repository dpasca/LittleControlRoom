package tui

import (
	"testing"
	"time"

	"lcroom/internal/model"
)

func attentionSortModel(projects []model.ProjectSummary, tasks []model.AgentTask, now time.Time) *Model {
	return &Model{
		sortMode:       sortByAttention,
		visibility:     visibilityAllFolders,
		archiveMode:    projectArchiveMain,
		allProjects:    projects,
		openAgentTasks: tasks,
		nowFn:          func() time.Time { return now },
	}
}

func projectListOrder(t *testing.T, m *Model) []string {
	t.Helper()
	m.rebuildProjectList("")
	names := make([]string, 0, len(m.projects))
	for _, project := range m.projects {
		names = append(names, project.Name)
	}
	return names
}

func TestAttentionSortKeepsStandaloneAgentTaskAtItsScore(t *testing.T) {
	now := time.Date(2026, 9, 17, 10, 0, 0, 0, time.UTC)
	m := attentionSortModel(
		[]model.ProjectSummary{
			{Path: "/tmp/alpha", Name: "alpha", AttentionScore: 0, LastActivity: now.Add(-2 * time.Hour), PresentOnDisk: true},
			{Path: "/tmp/beta", Name: "beta", AttentionScore: 5, LastActivity: now.Add(-3 * time.Hour), PresentOnDisk: true},
		},
		[]model.AgentTask{{
			ID:            "agt_waiting",
			Title:         "waiting task",
			Status:        model.AgentTaskStatusWaiting,
			WorkspacePath: "/tmp/workspaces/agt_waiting",
		}},
		now,
	)

	got := projectListOrder(t, m)
	want := []string{"waiting task", "beta", "alpha"}
	if !equalStrings(got, want) {
		t.Fatalf("project order = %v, want %v (waiting task scores 100 and must not sink below quiet projects)", got, want)
	}
}

func TestAttentionSortLiftsProjectHostingWaitingAgentTask(t *testing.T) {
	now := time.Date(2026, 9, 17, 10, 0, 0, 0, time.UTC)
	m := attentionSortModel(
		[]model.ProjectSummary{
			{Path: "/tmp/alpha", Name: "alpha", AttentionScore: 0, LastActivity: now.Add(-2 * time.Hour), PresentOnDisk: true},
			{Path: "/tmp/beta", Name: "beta", AttentionScore: 5, LastActivity: now.Add(-3 * time.Hour), PresentOnDisk: true},
		},
		[]model.AgentTask{{
			ID:                "agt_waiting",
			Title:             "waiting task",
			Status:            model.AgentTaskStatusWaiting,
			WorkspacePath:     "/tmp/workspaces/agt_waiting",
			OriginProjectPath: "/tmp/alpha",
		}},
		now,
	)

	got := projectListOrder(t, m)
	want := []string{"alpha", "waiting task", "beta"}
	if !equalStrings(got, want) {
		t.Fatalf("project order = %v, want %v (an affiliated task must lift the project it renders under)", got, want)
	}
	if m.projectRows[1].Kind != projectListRowAgentTask || m.projectRows[1].Indent != 1 {
		t.Fatalf("affiliated task row = %#v, want an indented agent task row under its anchor", m.projectRows[1])
	}
}

func TestAttentionSortLiftsRepoHostingUrgentWorktree(t *testing.T) {
	now := time.Date(2026, 9, 17, 10, 0, 0, 0, time.UTC)
	rootPath := "/tmp/repo"
	m := attentionSortModel(
		[]model.ProjectSummary{
			{
				Path: rootPath, Name: "repo", AttentionScore: 0,
				WorktreeRootPath: rootPath, WorktreeKind: model.WorktreeKindMain,
				LastActivity: now.Add(-4 * time.Hour), PresentOnDisk: true,
			},
			{
				Path: "/tmp/repo--urgent", Name: "repo--urgent", AttentionScore: 90,
				WorktreeRootPath: rootPath, WorktreeKind: model.WorktreeKindLinked,
				LastActivity: now.Add(-5 * time.Hour), PresentOnDisk: true,
			},
			{Path: "/tmp/beta", Name: "beta", AttentionScore: 5, LastActivity: now.Add(-1 * time.Hour), PresentOnDisk: true},
		},
		nil,
		now,
	)

	got := projectListOrder(t, m)
	want := []string{"repo", "repo--urgent", "beta"}
	if !equalStrings(got, want) {
		t.Fatalf("project order = %v, want %v (a repo block must follow its most urgent worktree)", got, want)
	}
	if m.projectRows[0].Kind != projectListRowRepo || m.projectRows[1].Kind != projectListRowWorktree {
		t.Fatalf("row kinds = %v/%v, want repo row followed by its linked worktree", m.projectRows[0].Kind, m.projectRows[1].Kind)
	}
}

func TestAttentionSortKeepsEqualScoresOnRecency(t *testing.T) {
	now := time.Date(2026, 9, 17, 10, 0, 0, 0, time.UTC)
	m := attentionSortModel(
		[]model.ProjectSummary{
			{Path: "/tmp/older", Name: "older", AttentionScore: 20, LastActivity: now.Add(-9 * time.Hour), PresentOnDisk: true},
			{Path: "/tmp/newer", Name: "newer", AttentionScore: 20, LastActivity: now.Add(-1 * time.Hour), PresentOnDisk: true},
		},
		nil,
		now,
	)

	got := projectListOrder(t, m)
	want := []string{"newer", "older"}
	if !equalStrings(got, want) {
		t.Fatalf("project order = %v, want %v (equal scores still break ties on recency)", got, want)
	}
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
