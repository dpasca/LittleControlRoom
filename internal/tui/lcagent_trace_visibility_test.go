package tui

import (
	"strings"
	"testing"

	"lcroom/internal/model"

	"github.com/charmbracelet/x/ansi"
)

func TestRenderDetailContentHidesLCAgentTraceRollup(t *testing.T) {
	project := model.ProjectSummary{
		Path:                "/tmp/demo",
		Name:                "demo",
		PresentOnDisk:       true,
		LatestSessionFormat: "lcagent_jsonl",
	}
	m := Model{
		allProjects: []model.ProjectSummary{project},
		projects:    []model.ProjectSummary{project},
		detail: model.ProjectDetail{
			Summary: project,
			Sessions: []model.SessionEvidence{
				{
					SessionID:   "lca_latest",
					Format:      "lcagent_jsonl",
					SessionFile: "/tmp/lca_latest.jsonl",
				},
			},
		},
	}

	rendered := ansi.Strip(m.renderDetailContent(100))
	if strings.Contains(rendered, "LCAgent trace") {
		t.Fatalf("renderDetailContent() should keep LCAgent trace rollups out of project detail: %q", rendered)
	}
}
