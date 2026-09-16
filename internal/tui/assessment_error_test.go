package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"lcroom/internal/codexapp"
	"lcroom/internal/model"
)

func TestAssessmentShowsStoppedErrorOverFollowup(t *testing.T) {
	project := model.ProjectSummary{
		Name: "demo", Path: "/tmp/demo", PresentOnDisk: true,
		LatestSessionFormat: "modern", LatestSessionClassification: model.ClassificationCompleted,
		LatestSessionClassificationType: model.SessionCategoryNeedsFollowUp,
		LatestSessionSummary:            "Continue converting particles.",
	}
	for _, closed := range []bool{false, true} {
		m := Model{
			projects:                     []model.ProjectSummary{project},
			renderCachedSessionStateOnly: true,
			codexSnapshots: map[string]codexapp.Snapshot{project.Path: {
				Provider: codexapp.ProviderCodex, Started: true, Closed: closed,
				LatestTurnStateKnown: true, LatestTurnCompleted: true,
				LastError: "Bad Request\ncodexErrorInfo: other",
			}},
		}
		rendered := ansi.Strip(m.renderProjectList(160, 4))
		if !strings.Contains(rendered, "blocked") || !strings.Contains(rendered, "Bad Request") || strings.Contains(rendered, "followup") || strings.Contains(rendered, "Continue converting") {
			t.Fatalf("closed=%v: stale assessment masked provider failure: %s", closed, rendered)
		}
		snapshot := m.codexSnapshots[project.Path]
		snapshot.ProjectPath = project.Path
		if summary, _, ok := m.embeddedSidebarSummary(snapshot); !ok || !strings.Contains(summary, "Bad Request") {
			t.Fatalf("sidebar hid failure: %q", summary)
		}
		surface := m.buildProjectDetailSurface(project, model.ProjectDetail{Summary: project})
		foundBlocked := false
		for _, block := range surface.Blocks {
			for _, field := range block.Fields {
				if field.Label == "Assessment" && strings.Contains(strings.ToLower(field.Text), "blocked") {
					foundBlocked = true
				}
			}
		}
		if !foundBlocked {
			t.Fatal("project detail did not show the failed session as blocked")
		}
		if closed {
			newerProject := project
			newerProject.LatestSessionID = "newer-session"
			if failure := m.projectStoppedSessionError(newerProject); failure != "" {
				t.Fatalf("closed historical session masked a replacement session: %q", failure)
			}
		}
		snapshot.LastError = ""
		m.codexSnapshots[project.Path] = snapshot
		if failure := m.projectStoppedSessionError(project); failure != "" {
			t.Fatalf("recovered session still blocked: %s", failure)
		}
	}
}
