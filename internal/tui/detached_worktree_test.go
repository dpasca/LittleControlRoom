package tui

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"lcroom/internal/model"
)

func TestDetachedWorktreeLabels(t *testing.T) {
	for _, tc := range []struct {
		path, root, branch, want string
	}{
		{"/tmp/repo--cloud-a-0930", "/tmp/repo", "(detached)", "cloud-a-0930"},
		{"/tmp/repo--cloud-b-1003", "/tmp/repo", "HEAD", "cloud-b-1003"},
		{"/tmp/comparison-a", "/tmp/repo", "(detached)", "comparison-a"},
		{"/tmp/repository--comparison-a", "/tmp/repo", "(detached)", "repository--comparison-a"},
		{"/tmp/repo--cloud-a-0930", "/tmp/repo", "feature/cloud", "feature/cloud"},
		{"/tmp/repo", "/tmp/repo", "(detached)", "repo"},
	} {
		p := model.ProjectSummary{Path: tc.path, WorktreeRootPath: tc.root, RepoBranch: tc.branch}
		if got := projectWorktreeLabel(p); got != tc.want {
			t.Errorf("label for %s (%s) = %q, want %q", tc.path, tc.branch, got, tc.want)
		}
	}
}

func TestDetachedWorktreeSummaryPreservesAttention(t *testing.T) {
	base := model.ProjectSummary{WorktreeKind: model.WorktreeKindLinked, RepoBranch: "(detached)"}
	for _, tc := range []struct {
		name   string
		mutate func(*model.ProjectSummary)
		want   string
	}{
		{"sessionless", func(p *model.ProjectSummary) {}, "Detached checkout · no agent session"},
		{"recorded session", func(p *model.ProjectSummary) { p.LatestSessionID = "session" }, "Detached checkout"},
		{"conflict", func(p *model.ProjectSummary) { p.RepoConflict = true }, "unmerged files"},
		{"dirty", func(p *model.ProjectSummary) { p.RepoDirty = true }, "dirty worktree"},
		{"submodule", func(p *model.ProjectSummary) { p.RepoSubmoduleDirtyCount = 1 }, "submodules 1 dirty"},
		{"assessment", func(p *model.ProjectSummary) { p.LatestCompletedSessionSummary = "Comparison finished" }, "Comparison finished"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := base
			tc.mutate(&p)
			if got := projectAssessmentText(p); got != tc.want {
				t.Fatalf("summary = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRenderDetachedWorktreesRemainDistinctWithoutMergeWarnings(t *testing.T) {
	now := time.Now()
	root := model.ProjectSummary{
		Name: "repo", Path: "/tmp/repo", WorktreeRootPath: "/tmp/repo",
		WorktreeKind: model.WorktreeKindMain, RepoBranch: "master", PresentOnDisk: true, InScope: true,
	}
	projects := []model.ProjectSummary{root}
	for _, suffix := range []string{"a-0930", "b-1003"} {
		path := "/tmp/repo--cloud-above-1-comparison-tmp-" + suffix
		projects = append(projects, model.ProjectSummary{
			Name: filepath.Base(path), Path: path, WorktreeRootPath: root.Path,
			WorktreeKind: model.WorktreeKindLinked, RepoBranch: "(detached)",
			PresentOnDisk: true, InScope: true, LastActivity: now, Status: model.StatusIdle,
			RepoSyncStatus: model.RepoSyncNoUpstream,
		})
	}
	m := Model{allProjects: projects, visibility: visibilityAllFolders, sortMode: sortByAttention}
	m.rebuildProjectList(root.Path)
	for _, width := range []int{120, 190, 270} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			rendered := ansi.Strip(m.renderProjectList(width, 8))
			for _, suffix := range []string{"a-0930", "b-1003"} {
				if !strings.Contains(rendered, suffix) {
					t.Fatalf("checkout suffix %q missing from rows: %s", suffix, rendered)
				}
			}
			if strings.Contains(rendered, "(detached)") || strings.Contains(rendered, "to integrate") {
				t.Fatalf("detached rows should have useful labels and no implied integration: %s", rendered)
			}
			if width >= 190 && strings.Count(rendered, "Detached checkout · no agent session") != 2 {
				t.Fatalf("missing detached summaries: %s", rendered)
			}
			for _, row := range strings.Split(rendered, "\n") {
				if ansi.StringWidth(row) > width {
					t.Fatalf("row exceeds %d columns: %s", width, row)
				}
			}
		})
	}
	for _, p := range projects {
		if got := ansi.Strip(m.projectRepoWarningIndicator(p, 0)); got != " " {
			t.Errorf("warning for %s = %q, want none without evidence of pending integration", p.Path, got)
		}
	}
	// The root can be filtered out when it has no agent activity of its own.
	m.allProjects = projects[1:]
	m.rebuildProjectList(projects[1].Path)
	rendered := ansi.Strip(m.renderProjectList(190, 8))
	if strings.Contains(rendered, "repo--") || !strings.Contains(rendered, "b-1003") {
		t.Fatalf("standalone detached rows must also retain useful names: %s", rendered)
	}
}

func TestTruncateMiddleTextPreservesSuffixAndTerminalWidth(t *testing.T) {
	for _, text := range []string{"cloud-above-1-tmp-a-0930", "界界界👩‍💻e\u0301-a-0930"} {
		for width := 0; width <= 30; width++ {
			got := truncateMiddleText(text, width)
			if ansi.StringWidth(got) > width {
				t.Fatalf("truncateMiddleText(%q, %d) = %q exceeds width", text, width, got)
			}
			if width >= 13 && !strings.HasSuffix(got, "a-0930") {
				t.Fatalf("truncateMiddleText(%q, %d) = %q lost identifying suffix", text, width, got)
			}
		}
	}
}
