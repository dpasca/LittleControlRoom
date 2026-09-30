package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"lcroom/internal/model"
)

func TestTruncateTextUsesTerminalColumns(t *testing.T) {
	for _, tc := range []struct {
		text  string
		width int
		want  string
	}{
		{"abcdef", 5, "ab..."},
		{"界界界", 5, "界..."},
		{"界界", 3, "界"},
		{"界", 1, ""},
		{"e\u0301e\u0301e\u0301", 3, "e\u0301e\u0301e\u0301"},
		{"👩‍💻abc", 4, "..."},
	} {
		if got := truncateText(tc.text, tc.width); got != tc.want {
			t.Errorf("truncateText(%q, %d) = %q, want %q", tc.text, tc.width, got, tc.want)
		}
	}
}

func TestMarqueeScrollTextStaysOneLine(t *testing.T) {
	for _, text := range []string{
		"first line\r\nsecond\tline\rthird\n",
		"界界界界界界",
		"👩‍💻e\u0301界 hello 👩‍💻",
	} {
		for width := 1; width <= 16; width++ {
			for offset := -30; offset <= 30; offset++ {
				got := marqueeScrollText(text, width, offset)
				if strings.ContainsAny(got, "\r\n\t") || ansi.StringWidth(got) != width {
					t.Fatalf("marqueeScrollText(%q, %d, %d) = %q (width %d)", text, width, offset, got, ansi.StringWidth(got))
				}
			}
		}
	}
}

func TestRenderProjectListKeepsCellsOnOneLine(t *testing.T) {
	for _, text := range []string{
		"first line\nsecond line\r\nthird\tline\rfinal line\n",
		strings.Repeat("界👩‍💻e\u0301", 30),
	} {
		for _, width := range []int{80, 190} {
			t.Run(fmt.Sprintf("%q/width=%d", text, width), func(t *testing.T) {
				m := Model{
					projects: []model.ProjectSummary{
						{Name: text, Path: "/tmp/row-a", Status: model.StatusIdle, PresentOnDisk: true, RunCommand: text, LatestCompletedSessionSummary: text},
						{Name: "next-project", Path: "/tmp/row-b", Status: model.StatusIdle, PresentOnDisk: true},
					},
					sortMode:   sortByAttention,
					visibility: visibilityAIFolders,
				}
				for selected := 0; selected < 2; selected++ {
					m.selected = selected
					for offset := 0; offset < 100; offset++ {
						m.marqueeOffset = offset
						rendered := ansi.Strip(m.renderProjectList(width, 6))
						lines := strings.Split(rendered, "\n")
						if len(lines) != 4 {
							t.Fatalf("selected=%d offset=%d: got %d lines, want tabs + header + 2 rows: %q", selected, offset, len(lines), rendered)
						}
						for _, row := range lines[2:] {
							if ansi.StringWidth(row) > width || strings.ContainsAny(row, "\r\t") {
								t.Fatalf("invalid row: %q", row)
							}
						}
					}
				}
			})
		}
	}
}

func TestRenderProjectListKeepsAgentWorkflowStatusOnOneLine(t *testing.T) {
	for _, phase := range []string{"completed", "awaiting_review", "changes_requested", "unclassified"} {
		t.Run(phase, func(t *testing.T) {
			task := model.AgentTask{
				ID:            "completed-startup-task",
				Title:         "Prepare signal-only automatic startup",
				WorkspacePath: "/tmp/startup-agent-task",
				Status:        model.AgentTaskStatusCompleted,
				Summary:       "Addressed review revision 1 P1: both install preflights now run in isolated service-identity child processes. Root snapshot stays 0700/0600.",
				Workflow:      model.AgentTaskWorkflow{Enabled: true, Phase: phase},
			}
			project, err := projectSummaryForAgentTask(task)
			if err != nil {
				t.Fatal(err)
			}
			m := Model{
				projects: []model.ProjectSummary{
					project,
					{Name: "next-project", Path: "/tmp/next-project", Status: model.StatusIdle, PresentOnDisk: true},
				},
				projectRows: []projectListRow{
					{Kind: projectListRowAgentTask, Indent: 1},
					{Kind: projectListRowStandalone},
				},
				openAgentTasks: []model.AgentTask{task},
				sortMode:       sortByAttention,
				visibility:     visibilityAIFolders,
			}
			for _, width := range []int{80, 145, 190} {
				for selected := 0; selected < 2; selected++ {
					m.selected = selected
					for offset := 0; offset < 100; offset++ {
						m.marqueeOffset = offset
						rendered := ansi.Strip(m.renderProjectList(width, 6))
						lines := strings.Split(rendered, "\n")
						if len(lines) != 4 {
							t.Fatalf("width=%d selected=%d offset=%d: got %d lines, want tabs + header + 2 rows: %q", width, selected, offset, len(lines), rendered)
						}
						statusPrefix := ansi.Truncate(strings.Split(strings.ReplaceAll(phase, "_", " "), " ")[0], 8, "")
						if !strings.Contains(lines[2], statusPrefix) {
							t.Fatalf("workflow status missing from task row: %q", lines[2])
						}
					}
				}
			}
		})
	}
}
