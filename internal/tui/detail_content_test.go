package tui

import (
	"strings"
	"testing"

	"lcroom/internal/model"

	"github.com/charmbracelet/x/ansi"
)

func TestRenderDetailContentKeepsTODOsSummarized(t *testing.T) {
	project := model.ProjectSummary{
		Path:                          "/tmp/demo",
		Name:                          "demo",
		PresentOnDisk:                 true,
		AttentionScore:                42,
		OpenTODOCount:                 2,
		TotalTODOCount:                3,
		LatestCompletedSessionSummary: "ready for review",
	}
	m := Model{
		allProjects: []model.ProjectSummary{project},
		projects:    []model.ProjectSummary{project},
		detail: model.ProjectDetail{
			Summary: project,
			Todos: []model.TodoItem{
				{ID: 1, Text: "large TODO text should stay in the TODO dialog"},
				{ID: 2, Text: "another hidden TODO"},
			},
			Reasons: []model.AttentionReason{
				{Code: "repo_dirty", Text: "Git worktree has uncommitted changes", Weight: 15},
			},
		},
	}

	rendered := ansi.Strip(m.renderDetailContent(100))
	for _, want := range []string{
		"Summary:",
		"TODOs: 2 open, 3 total",
		"press t or /todo",
		"Attention reasons",
		"Git worktree has uncommitted changes",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("renderDetailContent() missing %q:\n%s", want, rendered)
		}
	}
	for _, unwanted := range []string{
		"Attention: 42",
		"large TODO text should stay in the TODO dialog",
		"another hidden TODO",
		"Counts:",
	} {
		if strings.Contains(rendered, unwanted) {
			t.Fatalf("renderDetailContent() should not include %q:\n%s", unwanted, rendered)
		}
	}
}
