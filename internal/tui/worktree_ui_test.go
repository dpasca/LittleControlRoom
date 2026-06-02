package tui

import (
	"context"
	"errors"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"lcroom/internal/codexapp"
	"lcroom/internal/commands"
	"lcroom/internal/config"
	"lcroom/internal/events"
	"lcroom/internal/model"
	"lcroom/internal/projectrun"
	"lcroom/internal/service"
	"lcroom/internal/store"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRenderFooterShowsWorktreeHintsForRepoFamily(t *testing.T) {
	rootPath := "/tmp/repo"
	m := Model{
		focusedPane: focusProjects,
		allProjects: []model.ProjectSummary{
			{
				Name:             "repo",
				Path:             rootPath,
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindMain,
			},
			{
				Name:             "repo--feat-parallel-lane",
				Path:             "/tmp/repo--feat-parallel-lane",
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindLinked,
				RepoBranch:       "feat/parallel-lane",
			},
		},
		visibility: visibilityAllFolders,
		sortMode:   sortByAttention,
	}
	m.rebuildProjectList(rootPath)

	rendered := ansi.Strip(m.renderFooter(160))
	if !strings.Contains(rendered, "w lanes") {
		t.Fatalf("renderFooter() should advertise worktree lane toggling, got %q", rendered)
	}
	if strings.Contains(rendered, "P prune") {
		t.Fatalf("renderFooter() should not advertise a Prune hotkey, got %q", rendered)
	}
}

func TestRenderFooterShowsRemoveHintForLinkedWorktree(t *testing.T) {
	rootPath := "/tmp/repo"
	childPath := "/tmp/repo--feat-parallel-lane"
	m := Model{
		focusedPane: focusProjects,
		allProjects: []model.ProjectSummary{
			{
				Name:             "repo",
				Path:             rootPath,
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindMain,
			},
			{
				Name:             "repo--feat-parallel-lane",
				Path:             childPath,
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindLinked,
				RepoBranch:       "feat/parallel-lane",
			},
		},
		visibility: visibilityAllFolders,
		sortMode:   sortByAttention,
	}
	m.rebuildProjectList(childPath)
	if len(m.projects) > 1 {
		m.selected = 1
	}

	rendered := ansi.Strip(m.renderFooter(160))
	if !strings.Contains(rendered, "w lanes") {
		t.Fatalf("renderFooter() should keep lane toggling available on a linked worktree row, got %q", rendered)
	}
	if !strings.Contains(rendered, "x remove") {
		t.Fatalf("renderFooter() should advertise linked worktree removal when it is allowed, got %q", rendered)
	}
}

func TestRenderFooterShowsRemoveHintForLinkedWorktreeWithActiveSession(t *testing.T) {
	rootPath := "/tmp/repo"
	childPath := "/tmp/repo--feat-parallel-lane"
	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider: req.Provider.Normalized(),
				Started:  true,
				Status:   req.Provider.Label() + " session ready",
			},
		}, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: childPath,
		Provider:    codexapp.ProviderClaudeCode,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	m := Model{
		codexManager: manager,
		focusedPane:  focusProjects,
		allProjects: []model.ProjectSummary{
			{
				Name:             "repo",
				Path:             rootPath,
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindMain,
			},
			{
				Name:             "repo--feat-parallel-lane",
				Path:             childPath,
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindLinked,
				RepoBranch:       "feat/parallel-lane",
			},
		},
		visibility: visibilityAllFolders,
		sortMode:   sortByAttention,
	}
	m.rebuildProjectList(childPath)
	if len(m.projects) > 1 {
		m.selected = 1
	}

	rendered := ansi.Strip(m.renderFooter(160))
	if !strings.Contains(rendered, "x remove") {
		t.Fatalf("renderFooter() should still advertise linked worktree removal with an active session, got %q", rendered)
	}
}

func TestRenderFooterShowsMergeHintForLinkedWorktreeWithParentBranch(t *testing.T) {
	rootPath := "/tmp/repo"
	childPath := "/tmp/repo--feat-parallel-lane"
	m := Model{
		focusedPane: focusProjects,
		allProjects: []model.ProjectSummary{
			{
				Name:             "repo",
				Path:             rootPath,
				Status:           model.StatusIdle,
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindMain,
				RepoBranch:       "master",
			},
			{
				Name:                 "repo--feat-parallel-lane",
				Path:                 childPath,
				PresentOnDisk:        true,
				WorktreeRootPath:     rootPath,
				WorktreeKind:         model.WorktreeKindLinked,
				WorktreeParentBranch: "master",
				RepoBranch:           "feat/parallel-lane",
			},
		},
		visibility: visibilityAllFolders,
		sortMode:   sortByAttention,
	}
	m.rebuildProjectList(childPath)

	rendered := ansi.Strip(m.renderFooter(160))
	if !strings.Contains(rendered, "M merge") {
		t.Fatalf("renderFooter() should advertise linked worktree merge-back when a parent branch is known, got %q", rendered)
	}
}

func TestRenderFooterShowsCommitMergeHintForDirtyLinkedWorktree(t *testing.T) {
	rootPath := "/tmp/repo"
	childPath := "/tmp/repo--feat-parallel-lane"
	m := Model{
		focusedPane: focusProjects,
		allProjects: []model.ProjectSummary{
			{
				Name:             "repo",
				Path:             rootPath,
				Status:           model.StatusIdle,
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindMain,
				RepoBranch:       "master",
			},
			{
				Name:                 "repo--feat-parallel-lane",
				Path:                 childPath,
				PresentOnDisk:        true,
				WorktreeRootPath:     rootPath,
				WorktreeKind:         model.WorktreeKindLinked,
				WorktreeParentBranch: "master",
				RepoBranch:           "feat/parallel-lane",
				RepoDirty:            true,
			},
		},
		visibility: visibilityAllFolders,
		sortMode:   sortByAttention,
	}
	m.rebuildProjectList(childPath)

	rendered := ansi.Strip(m.renderFooter(160))
	if !strings.Contains(rendered, "M commit+merge") {
		t.Fatalf("renderFooter() should advertise commit+merge for dirty linked worktrees, got %q", rendered)
	}
}

func TestRenderFooterHidesMergeHintDuringCommitInFlight(t *testing.T) {
	rootPath := "/tmp/repo"
	childPath := "/tmp/repo--feat-parallel-lane"
	m := Model{
		focusedPane: focusProjects,
		allProjects: []model.ProjectSummary{
			{
				Name:             "repo",
				Path:             rootPath,
				Status:           model.StatusIdle,
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindMain,
				RepoBranch:       "master",
			},
			{
				Name:                 "repo--feat-parallel-lane",
				Path:                 childPath,
				PresentOnDisk:        true,
				WorktreeRootPath:     rootPath,
				WorktreeKind:         model.WorktreeKindLinked,
				WorktreeParentBranch: "master",
				RepoBranch:           "feat/parallel-lane",
			},
		},
		visibility: visibilityAllFolders,
		sortMode:   sortByAttention,
		pendingGitSummaries: map[string]string{
			childPath: "Committing...",
		},
	}
	m.rebuildProjectList(childPath)

	rendered := ansi.Strip(m.renderFooter(160))
	if strings.Contains(rendered, "M merge") {
		t.Fatalf("renderFooter() should hide merge action while commit is in flight")
	}
}

func TestRenderDetailContentShowsWorktreeActions(t *testing.T) {
	rootPath := "/tmp/repo"
	childPath := "/tmp/repo--feat-parallel-lane"
	m := Model{
		allProjects: []model.ProjectSummary{
			{
				Name:             "repo",
				Path:             rootPath,
				Status:           model.StatusIdle,
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindMain,
				RepoBranch:       "master",
			},
			{
				Name:                 "repo--feat-parallel-lane",
				Path:                 childPath,
				PresentOnDisk:        true,
				WorktreeRootPath:     rootPath,
				WorktreeKind:         model.WorktreeKindLinked,
				WorktreeParentBranch: "master",
				RepoBranch:           "feat/parallel-lane",
			},
		},
		visibility: visibilityAllFolders,
		sortMode:   sortByAttention,
	}
	m.rebuildProjectList(childPath)

	rendered := ansi.Strip(m.renderDetailContent(100))
	if !strings.Contains(rendered, "Worktree actions") {
		t.Fatalf("renderDetailContent() should include a worktree actions section, got %q", rendered)
	}
	if !strings.Contains(rendered, "M or /wt merge") || !strings.Contains(rendered, "x or /wt remove") {
		t.Fatalf("renderDetailContent() should list worktree hotkeys and slash commands, got %q", rendered)
	}
	if strings.Contains(rendered, "/wt prune") {
		t.Fatalf("renderDetailContent() should skip prune hint for linked worktree selection, got %q", rendered)
	}
}

func TestRenderDetailContentForLinkedWorktreeSkipsPruneHint(t *testing.T) {
	rootPath := "/tmp/repo"
	childPath := "/tmp/repo--feat-parallel-lane"
	m := Model{
		allProjects: []model.ProjectSummary{
			{
				Name:             "repo",
				Path:             rootPath,
				Status:           model.StatusIdle,
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindMain,
				RepoBranch:       "master",
			},
			{
				Name:                 "repo--feat-parallel-lane",
				Path:                 childPath,
				PresentOnDisk:        true,
				WorktreeRootPath:     rootPath,
				WorktreeKind:         model.WorktreeKindLinked,
				WorktreeParentBranch: "master",
				RepoBranch:           "feat/parallel-lane",
			},
		},
		visibility: visibilityAllFolders,
		sortMode:   sortByAttention,
	}
	m.rebuildProjectList(childPath)

	rendered := ansi.Strip(m.renderDetailContent(100))
	if strings.Contains(rendered, "/wt prune") {
		t.Fatalf("renderDetailContent() should not include prune for linked worktree selection, got %q", rendered)
	}
}

func TestRenderDetailContentForRootProjectShowsPruneSlashCommand(t *testing.T) {
	rootPath := "/tmp/repo"
	m := Model{
		allProjects: []model.ProjectSummary{
			{
				Name:             "repo",
				Path:             rootPath,
				Status:           model.StatusIdle,
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindMain,
			},
			{
				Name:             "repo--feat-parallel-lane",
				Path:             "/tmp/repo--feat-parallel-lane",
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindLinked,
				RepoBranch:       "feat/parallel-lane",
			},
		},
		visibility: visibilityAllFolders,
		sortMode:   sortByAttention,
	}
	m.rebuildProjectList(rootPath)

	rendered := ansi.Strip(m.renderDetailContent(100))
	if !strings.Contains(rendered, "Worktree actions") {
		t.Fatalf("renderDetailContent() should include a worktree actions section, got %q", rendered)
	}
	if strings.Contains(rendered, "M or /wt merge") || strings.Contains(rendered, "x or /wt remove") {
		t.Fatalf("renderDetailContent() should only show root-level worktree actions, got %q", rendered)
	}
	if !strings.Contains(rendered, "/wt prune") {
		t.Fatalf("renderDetailContent() should include /wt prune for the worktree root, got %q", rendered)
	}
}

func TestRenderDetailContentShowsWorktreeMergeStatus(t *testing.T) {
	rootPath := "/tmp/repo"
	childPath := "/tmp/repo--feat-parallel-lane"
	m := Model{
		allProjects: []model.ProjectSummary{
			{
				Name:             "repo",
				Path:             rootPath,
				Status:           model.StatusIdle,
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindMain,
				RepoBranch:       "master",
			},
			{
				Name:                 "repo--feat-parallel-lane",
				Path:                 childPath,
				PresentOnDisk:        true,
				WorktreeRootPath:     rootPath,
				WorktreeKind:         model.WorktreeKindLinked,
				WorktreeParentBranch: "master",
				WorktreeMergeStatus:  model.WorktreeMergeStatusNotMerged,
				RepoBranch:           "feat/parallel-lane",
			},
		},
		visibility: visibilityAllFolders,
		sortMode:   sortByAttention,
	}
	m.rebuildProjectList(childPath)

	rendered := ansi.Strip(m.renderDetailContent(100))
	if !strings.Contains(rendered, "Merge status:") {
		t.Fatalf("renderDetailContent() should show a merge status field for linked worktrees, got %q", rendered)
	}
	if !strings.Contains(rendered, "ready to merge into master") {
		t.Fatalf("renderDetailContent() should show the linked worktree merge status, got %q", rendered)
	}
	if !strings.Contains(rendered, "needs merge") {
		t.Fatalf("renderDetailContent() should include worktree lane merge status in the family list, got %q", rendered)
	}
}

func TestRenderDetailContentShowsWorktreeMergeInProgress(t *testing.T) {
	rootPath := "/tmp/repo"
	childPath := "/tmp/repo--feat-parallel-lane"
	m := Model{
		allProjects: []model.ProjectSummary{
			{
				Name:             "repo",
				Path:             rootPath,
				Status:           model.StatusIdle,
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindMain,
				RepoBranch:       "master",
			},
			{
				Name:                 "repo--feat-parallel-lane",
				Path:                 childPath,
				PresentOnDisk:        true,
				WorktreeRootPath:     rootPath,
				WorktreeKind:         model.WorktreeKindLinked,
				WorktreeParentBranch: "master",
				WorktreeMergeStatus:  model.WorktreeMergeStatusMergeInProgress,
				RepoBranch:           "feat/parallel-lane",
			},
		},
		visibility: visibilityAllFolders,
		sortMode:   sortByAttention,
	}
	m.rebuildProjectList(childPath)

	rendered := ansi.Strip(m.renderDetailContent(100))
	if !strings.Contains(rendered, "merging into master") {
		t.Fatalf("renderDetailContent() should show in-progress merge status, got %q", rendered)
	}
	if strings.Contains(rendered, "ready to merge into master") {
		t.Fatalf("renderDetailContent() should not show in-progress merges as unmerged, got %q", rendered)
	}
	if worktreeNeedsMergeBack(m.allProjects[1]) {
		t.Fatalf("merge-in-progress worktree should not be counted as still needing merge-back")
	}
}

func TestRenderDetailContentPrioritizesDirtyWorktreeMergeReadiness(t *testing.T) {
	rootPath := "/tmp/repo"
	childPath := "/tmp/repo--feat-parallel-lane"
	m := Model{
		allProjects: []model.ProjectSummary{
			{
				Name:             "repo",
				Path:             rootPath,
				Status:           model.StatusIdle,
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindMain,
				RepoBranch:       "master",
			},
			{
				Name:                 "repo--feat-parallel-lane",
				Path:                 childPath,
				PresentOnDisk:        true,
				WorktreeRootPath:     rootPath,
				WorktreeKind:         model.WorktreeKindLinked,
				WorktreeParentBranch: "master",
				WorktreeMergeStatus:  model.WorktreeMergeStatusNotMerged,
				RepoBranch:           "feat/parallel-lane",
				RepoDirty:            true,
			},
		},
		visibility: visibilityAllFolders,
		sortMode:   sortByAttention,
	}
	m.rebuildProjectList(childPath)

	rendered := ansi.Strip(m.renderDetailContent(100))
	if !strings.Contains(rendered, "commit changes before merging into master") {
		t.Fatalf("renderDetailContent() should put dirty merge readiness first, got %q", rendered)
	}
	if strings.Contains(rendered, "Merge status: ready to merge into master") {
		t.Fatalf("renderDetailContent() should not present dirty worktrees as merge-ready, got %q", rendered)
	}
	if !strings.Contains(rendered, "M or /wt merge (commit dirty changes first)") {
		t.Fatalf("renderDetailContent() should describe the commit+merge action for dirty worktrees, got %q", rendered)
	}
}

func TestRenderDetailContentDoesNotImplyDirtyIntegratedWorktreeWasMerged(t *testing.T) {
	rootPath := "/tmp/repo"
	childPath := "/tmp/repo--todo-reconnect-exhaustion"
	m := Model{
		allProjects: []model.ProjectSummary{
			{
				Name:             "repo",
				Path:             rootPath,
				Status:           model.StatusIdle,
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindMain,
				RepoBranch:       "master",
			},
			{
				Name:                 "repo--todo-reconnect-exhaustion",
				Path:                 childPath,
				PresentOnDisk:        true,
				WorktreeRootPath:     rootPath,
				WorktreeKind:         model.WorktreeKindLinked,
				WorktreeParentBranch: "master",
				WorktreeMergeStatus:  model.WorktreeMergeStatusMerged,
				RepoBranch:           "todo/reconnect-exhaustion",
				RepoDirty:            true,
			},
		},
		visibility: visibilityAllFolders,
		sortMode:   sortByAttention,
	}
	m.rebuildProjectList(childPath)

	rendered := ansi.Strip(m.renderDetailContent(100))
	if !strings.Contains(rendered, "nothing to merge; local changes") {
		t.Fatalf("renderDetailContent() should describe branch ancestry without implying a merge happened, got %q", rendered)
	}
	if strings.Contains(rendered, "merged into master") {
		t.Fatalf("renderDetailContent() should not imply the dirty worktree was merged into master, got %q", rendered)
	}
}

func TestRenderDetailContentShowsRepoCentricWorktreeSummary(t *testing.T) {
	rootPath := "/tmp/repo"
	childPath := "/tmp/repo--feat-parallel-lane"
	m := Model{
		allProjects: []model.ProjectSummary{
			{
				Name:             "repo",
				Path:             rootPath,
				Status:           model.StatusIdle,
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindMain,
				RepoBranch:       "master",
			},
			{
				Name:             "repo--feat-parallel-lane",
				Path:             childPath,
				Status:           model.StatusActive,
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindLinked,
				RepoBranch:       "feat/parallel-lane",
				RepoDirty:        true,
			},
		},
		visibility: visibilityAllFolders,
		sortMode:   sortByAttention,
	}
	m.rebuildProjectList(rootPath)

	rendered := ansi.Strip(m.renderDetailContent(100))
	if !strings.Contains(rendered, "Worktrees:") {
		t.Fatalf("renderDetailContent() should show worktree family details for the repo root, got %q", rendered)
	}
	if !strings.Contains(rendered, "root + 1 linked, 1 active, 1 dirty") {
		t.Fatalf("renderDetailContent() should describe the family without counting the root as a generic worktree, got %q", rendered)
	}
}

func TestRenderDetailContentShowsOrphanedWorktreeWarning(t *testing.T) {
	rootPath := "/tmp/repo"
	orphanPath := "/tmp/repo--stale-lane"
	m := Model{
		allProjects: []model.ProjectSummary{
			{
				Name:             "repo",
				Path:             rootPath,
				Status:           model.StatusIdle,
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindMain,
				RepoBranch:       "master",
			},
		},
		orphanedWorktreesByRoot: map[string][]model.ProjectSummary{
			rootPath: {{
				Name:                "repo--stale-lane",
				Path:                orphanPath,
				Status:              model.StatusIdle,
				PresentOnDisk:       true,
				Forgotten:           true,
				WorktreeRootPath:    rootPath,
				WorktreeKind:        model.WorktreeKindLinked,
				WorktreeMergeStatus: model.WorktreeMergeStatusMerged,
				RepoBranch:          "todo/stale-lane",
			}},
		},
		visibility: visibilityAllFolders,
		sortMode:   sortByAttention,
	}
	m.rebuildProjectList(rootPath)

	rendered := ansi.Strip(m.renderDetailContent(120))
	if !strings.Contains(rendered, "Worktree warnings") {
		t.Fatalf("renderDetailContent() should add an orphaned-worktree warning section, got %q", rendered)
	}
	if !strings.Contains(rendered, "1 orphaned checkout(s) still exist on disk") {
		t.Fatalf("renderDetailContent() should explain the orphaned checkout state, got %q", rendered)
	}
	if !strings.Contains(rendered, "todo/stale-lane · orphaned, nothing to merge") {
		t.Fatalf("renderDetailContent() should list the orphaned checkout branch and status, got %q", rendered)
	}
	if !strings.Contains(rendered, orphanPath) {
		t.Fatalf("renderDetailContent() should show the orphaned checkout path, got %q", rendered)
	}
}

func TestRenderDetailContentShowsSessionSummaryBeforeWorktreeInfo(t *testing.T) {
	rootPath := "/tmp/repo"
	childPath := "/tmp/repo--feat-parallel-lane"
	m := Model{
		allProjects: []model.ProjectSummary{
			{
				Name:                             "repo",
				Path:                             rootPath,
				Status:                           model.StatusIdle,
				PresentOnDisk:                    true,
				WorktreeRootPath:                 rootPath,
				WorktreeKind:                     model.WorktreeKindMain,
				RepoBranch:                       "master",
				LatestSessionClassification:      model.ClassificationCompleted,
				LatestSessionClassificationType:  model.SessionCategoryNeedsFollowUp,
				LatestSessionSummary:             "Follow the root plan before merging any lane.",
				LatestSessionFormat:              "modern",
				LatestSessionDetectedProjectPath: rootPath,
			},
			{
				Name:             "repo--feat-parallel-lane",
				Path:             childPath,
				Status:           model.StatusActive,
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindLinked,
				RepoBranch:       "feat/parallel-lane",
				RepoDirty:        true,
			},
		},
		visibility: visibilityAllFolders,
		sortMode:   sortByAttention,
		detail: model.ProjectDetail{
			LatestSessionClassification: &model.SessionClassification{
				Status:   model.ClassificationCompleted,
				Category: model.SessionCategoryNeedsFollowUp,
				Summary:  "Follow the root plan before merging any lane.",
			},
		},
	}
	m.rebuildProjectList(rootPath)

	rendered := ansi.Strip(m.renderDetailContent(100))
	summaryIndex := strings.Index(rendered, "Summary:")
	pathIndex := strings.Index(rendered, "Path:")
	worktreesIndex := strings.Index(rendered, "Worktrees:")
	if summaryIndex < 0 || pathIndex < 0 || worktreesIndex < 0 {
		t.Fatalf("renderDetailContent() missing summary or worktree sections: %q", rendered)
	}
	if summaryIndex > pathIndex || summaryIndex > worktreesIndex {
		t.Fatalf("renderDetailContent() should show the summary at the top before path and worktree info: %q", rendered)
	}
}

func TestRenderDetailContentSkipsWorktreeLaneSectionForLinkedSelection(t *testing.T) {
	rootPath := "/tmp/repo"
	childPath := "/tmp/repo--feat-parallel-lane"
	m := Model{
		allProjects: []model.ProjectSummary{
			{
				Name:             "repo",
				Path:             rootPath,
				Status:           model.StatusIdle,
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindMain,
				RepoBranch:       "master",
			},
			{
				Name:                 "repo--feat-parallel-lane",
				Path:                 childPath,
				Status:               model.StatusActive,
				PresentOnDisk:        true,
				WorktreeRootPath:     rootPath,
				WorktreeKind:         model.WorktreeKindLinked,
				WorktreeParentBranch: "master",
				WorktreeMergeStatus:  model.WorktreeMergeStatusNotMerged,
				RepoBranch:           "feat/parallel-lane",
				RepoDirty:            true,
			},
		},
		visibility: visibilityAllFolders,
		sortMode:   sortByAttention,
	}
	m.rebuildProjectList(childPath)

	rendered := ansi.Strip(m.renderDetailContent(100))
	if strings.Contains(rendered, "Worktree lanes") {
		t.Fatalf("renderDetailContent() should keep the family lane list on the root project only, got %q", rendered)
	}
	if !strings.Contains(rendered, "Worktree actions") {
		t.Fatalf("renderDetailContent() should keep linked-worktree actions available, got %q", rendered)
	}
}

func TestUpdateNormalModeMOpensWorktreeMergeConfirm(t *testing.T) {
	rootPath := "/tmp/repo"
	childPath := "/tmp/repo--feat-parallel-lane"
	m := Model{
		focusedPane: focusProjects,
		allProjects: []model.ProjectSummary{
			{
				Name:             "repo",
				Path:             rootPath,
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindMain,
				RepoBranch:       "master",
			},
			{
				Name:                 "repo--feat-parallel-lane",
				Path:                 childPath,
				PresentOnDisk:        true,
				WorktreeRootPath:     rootPath,
				WorktreeKind:         model.WorktreeKindLinked,
				WorktreeParentBranch: "master",
				RepoBranch:           "feat/parallel-lane",
			},
		},
		visibility: visibilityAllFolders,
		sortMode:   sortByAttention,
		width:      120,
		height:     28,
	}
	m.rebuildProjectList(childPath)

	updated, cmd := m.updateNormalMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'M'}})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("merge confirm should open without scheduling a command")
	}
	if got.worktreeMergeConfirm == nil {
		t.Fatalf("M should open the worktree merge-back confirmation dialog")
	}
	if got.worktreeMergeConfirm.TargetBranch != "master" {
		t.Fatalf("merge confirm target branch = %q, want master", got.worktreeMergeConfirm.TargetBranch)
	}
}

func TestUpdateNormalModeMBlockedWhenCommitIsInFlight(t *testing.T) {
	rootPath := "/tmp/repo"
	childPath := "/tmp/repo--feat-parallel-lane"
	workingPath := "/tmp/repo--feat-parallel-lane-wip"
	m := Model{
		focusedPane: focusProjects,
		allProjects: []model.ProjectSummary{
			{
				Name:             "repo",
				Path:             rootPath,
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindMain,
				RepoBranch:       "master",
			},
			{
				Name:                 "repo--feat-parallel-lane",
				Path:                 childPath,
				PresentOnDisk:        true,
				WorktreeRootPath:     rootPath,
				WorktreeKind:         model.WorktreeKindLinked,
				WorktreeParentBranch: "master",
				RepoBranch:           "feat/parallel-lane",
			},
			{
				Name:                 "repo--feat-parallel-lane-wip",
				Path:                 workingPath,
				PresentOnDisk:        true,
				WorktreeRootPath:     rootPath,
				WorktreeKind:         model.WorktreeKindLinked,
				WorktreeParentBranch: "master",
				RepoBranch:           "feat/parallel-lane-wip",
			},
		},
		visibility: visibilityAllFolders,
		sortMode:   sortByAttention,
		width:      120,
		height:     28,
		pendingGitSummaries: map[string]string{
			workingPath: "Committing...",
		},
	}
	m.rebuildProjectList(childPath)

	updated, cmd := m.updateNormalMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'M'}})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("commit-in-flight should block merge confirm without scheduling work")
	}
	if got.worktreeMergeConfirm != nil {
		t.Fatalf("merge confirm should remain closed while a commit is running")
	}
	if got.status != "A commit is still in progress. Finish it before merging this worktree back." {
		t.Fatalf("status = %q, want commit in-flight gate message", got.status)
	}
}

func TestUpdateNormalModeMShowsBlockedMergeWhenWorktreesDirty(t *testing.T) {
	rootPath := "/tmp/repo"
	childPath := "/tmp/repo--feat-parallel-lane"
	m := Model{
		focusedPane: focusProjects,
		allProjects: []model.ProjectSummary{
			{
				Name:             "repo",
				Path:             rootPath,
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindMain,
				RepoBranch:       "master",
				RepoDirty:        true,
			},
			{
				Name:                 "repo--feat-parallel-lane",
				Path:                 childPath,
				PresentOnDisk:        true,
				WorktreeRootPath:     rootPath,
				WorktreeKind:         model.WorktreeKindLinked,
				WorktreeParentBranch: "master",
				RepoBranch:           "feat/parallel-lane",
				RepoDirty:            true,
			},
		},
		visibility: visibilityAllFolders,
		sortMode:   sortByAttention,
		width:      120,
		height:     28,
	}
	m.rebuildProjectList(childPath)

	updated, cmd := m.updateNormalMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'M'}})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("blocked merge confirm should open without scheduling work")
	}
	if got.worktreeMergeConfirm == nil {
		t.Fatalf("M should still open the merge-back dialog in blocked state")
	}
	if worktreeMergeConfirmReady(got.worktreeMergeConfirm) {
		t.Fatalf("merge confirm should start blocked when source/root are dirty")
	}
	if !got.worktreeMergeConfirm.SourceDirty {
		t.Fatalf("blocked merge should offer a commit when the source worktree is dirty")
	}
	if !strings.Contains(worktreeMergeConfirmBlockReason(got.worktreeMergeConfirm), "The root checkout is dirty") {
		t.Fatalf("blocked reason = %q, want root dirty reason", worktreeMergeConfirmBlockReason(got.worktreeMergeConfirm))
	}
	rendered := ansi.Strip(got.renderWorktreeMergeConfirmOverlay("", 100, 24))
	if !strings.Contains(rendered, "Merge blocked") || !strings.Contains(rendered, "The root checkout is dirty") || !strings.Contains(rendered, "Commit worktree changes first") {
		t.Fatalf("blocked merge overlay should explain why merge is unavailable, got %q", rendered)
	}
}

func TestOpenWorktreeMergeConfirmRefreshesLiveRootDirtyStatus(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	rootDir := t.TempDir()
	rootPath := filepath.Join(rootDir, "repo")
	worktreePath := filepath.Join(rootDir, "repo--feat-live-dirty")

	runTUITestGit(t, "", "init", rootPath)
	runTUITestGit(t, rootPath, "config", "user.name", "Little Control Room Tests")
	runTUITestGit(t, rootPath, "config", "user.email", "tests@example.com")
	if err := os.WriteFile(filepath.Join(rootPath, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write README.md: %v", err)
	}
	runTUITestGit(t, rootPath, "add", "README.md")
	runTUITestGit(t, rootPath, "commit", "-m", "initial commit")
	runTUITestGit(t, rootPath, "worktree", "add", "-b", "feat/live-dirty", worktreePath)
	if err := os.WriteFile(filepath.Join(rootPath, "DIRTY.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatalf("write DIRTY.txt: %v", err)
	}

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	now := time.Now().UTC()
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:             rootPath,
		Name:             "repo",
		PresentOnDisk:    true,
		InScope:          true,
		WorktreeRootPath: rootPath,
		WorktreeKind:     model.WorktreeKindMain,
		RepoBranch:       "master",
		UpdatedAt:        now,
	}); err != nil {
		t.Fatalf("seed root project state: %v", err)
	}
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:                 worktreePath,
		Name:                 "repo--feat-live-dirty",
		PresentOnDisk:        true,
		InScope:              true,
		WorktreeRootPath:     rootPath,
		WorktreeKind:         model.WorktreeKindLinked,
		WorktreeParentBranch: "master",
		RepoBranch:           "feat/live-dirty",
		UpdatedAt:            now,
	}); err != nil {
		t.Fatalf("seed linked worktree state: %v", err)
	}

	svc := service.New(config.Default(), st, events.NewBus(), nil)
	m := Model{
		ctx: ctx,
		svc: svc,
		allProjects: []model.ProjectSummary{
			{
				Name:             "repo",
				Path:             rootPath,
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindMain,
				RepoBranch:       "master",
			},
			{
				Name:                 "repo--feat-live-dirty",
				Path:                 worktreePath,
				PresentOnDisk:        true,
				WorktreeRootPath:     rootPath,
				WorktreeKind:         model.WorktreeKindLinked,
				WorktreeParentBranch: "master",
				RepoBranch:           "feat/live-dirty",
			},
		},
		visibility: visibilityAllFolders,
		sortMode:   sortByAttention,
		width:      120,
		height:     28,
	}
	m.rebuildProjectList(worktreePath)

	cmd := m.openWorktreeMergeConfirmForSelection()
	if cmd == nil {
		t.Fatal("opening the merge confirmation should refresh live git status when the service is available")
	}
	if m.worktreeMergeConfirm == nil {
		t.Fatal("merge confirmation should open before the async refresh completes")
	}
	if !m.worktreeMergeConfirm.Busy {
		t.Fatal("merge confirmation should stay busy while live git status is loading")
	}
	if got := m.status; got != "Checking live worktree status..." {
		t.Fatalf("status = %q, want live-status refresh message", got)
	}

	m = drainCmdMsgs(m, cmd)

	if m.worktreeMergeConfirm == nil {
		t.Fatal("merge confirmation should stay open after the live status refresh")
	}
	if m.worktreeMergeConfirm.Busy {
		t.Fatal("merge confirmation should stop waiting once both worktree statuses refresh")
	}
	if worktreeMergeConfirmReady(m.worktreeMergeConfirm) {
		t.Fatalf("merge confirmation should block once the live root checkout refresh reports dirtiness: %#v", m.worktreeMergeConfirm)
	}
	if !strings.Contains(worktreeMergeConfirmBlockReason(m.worktreeMergeConfirm), "The root checkout is dirty") {
		t.Fatalf("blocked reason = %q, want refreshed root-dirty warning", worktreeMergeConfirmBlockReason(m.worktreeMergeConfirm))
	}
	if got := m.status; got != "The root checkout is dirty. Commit or discard changes before merging back." {
		t.Fatalf("status = %q, want refreshed root-dirty status", got)
	}
	rootSummary, ok := m.projectSummaryByPath(rootPath)
	if !ok {
		t.Fatalf("root project summary for %q missing after refresh", rootPath)
	}
	if !rootSummary.RepoDirty {
		t.Fatalf("root summary should refresh to dirty after the live status check: %#v", rootSummary)
	}

	rendered := ansi.Strip(m.renderWorktreeMergeConfirmOverlay("", 100, 24))
	if !strings.Contains(rendered, "Merge blocked") || !strings.Contains(rendered, "The root checkout is dirty") {
		t.Fatalf("rendered merge dialog should show the refreshed root-dirty warning, got %q", rendered)
	}
}

func TestBusyWorktreeMergeRefreshEscCancelsDialog(t *testing.T) {
	t.Parallel()

	m := Model{
		worktreeMergeConfirm: &worktreeMergeConfirmState{
			ProjectPath:    "/tmp/repo--feat-live-dirty",
			RootPath:       "/tmp/repo",
			BranchName:     "feat/live-dirty",
			TargetBranch:   "master",
			PendingRefresh: worktreeMergeConfirmPendingRefreshSet("/tmp/repo--feat-live-dirty", "/tmp/repo"),
			Busy:           true,
			BusyMessage:    "Checking live git status for this worktree and its root checkout.",
		},
	}

	updated, cmd := m.updateWorktreeMergeConfirmMode(tea.KeyMsg{Type: tea.KeyEsc})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("esc during merge readiness refresh should not schedule work")
	}
	if got.worktreeMergeConfirm != nil {
		t.Fatalf("esc during merge readiness refresh should close the dialog")
	}
	if got.status != "Worktree merge-back canceled" {
		t.Fatalf("status = %q, want cancel status", got.status)
	}
}

func TestRenderBusyWorktreeMergeRefreshShowsEscHint(t *testing.T) {
	t.Parallel()

	m := Model{
		worktreeMergeConfirm: &worktreeMergeConfirmState{
			ProjectPath:    "/tmp/repo--feat-live-dirty",
			RootPath:       "/tmp/repo",
			BranchName:     "feat/live-dirty",
			TargetBranch:   "master",
			PendingRefresh: worktreeMergeConfirmPendingRefreshSet("/tmp/repo--feat-live-dirty", "/tmp/repo"),
			Busy:           true,
			BusyMessage:    "Checking live git status for this worktree and its root checkout.",
		},
	}

	rendered := ansi.Strip(m.renderWorktreeMergeConfirmOverlay("", 100, 24))
	if !strings.Contains(rendered, "Esc") || !strings.Contains(rendered, "close") {
		t.Fatalf("rendered busy merge dialog should advertise esc close while refreshing, got %q", rendered)
	}
}

func TestRenderWorktreeMergeConfirmClampsLongErrorMessage(t *testing.T) {
	t.Parallel()

	longLine := "This is a deliberately long merge error line that should wrap inside the dialog instead of falling off the bottom of the screen."
	m := Model{
		worktreeMergeConfirm: &worktreeMergeConfirmState{
			ProjectPath:  "/tmp/repo--feat-live-dirty",
			RootPath:     "/tmp/repo",
			BranchName:   "feat/live-dirty",
			TargetBranch: "master",
			Selected:     worktreeMergeConfirmKeepIndex(&worktreeMergeConfirmState{}),
			ErrorMessage: strings.TrimSpace(strings.Repeat(longLine+"\n", 16)),
		},
	}
	m.worktreeMergeConfirm.Selected = worktreeMergeConfirmKeepIndex(m.worktreeMergeConfirm)

	rendered := ansi.Strip(m.renderWorktreeMergeConfirmOverlay("", 72, 18))
	if !strings.Contains(rendered, "... more error details in /errors.") {
		t.Fatalf("long merge error should show an overflow hint instead of clipping silently, got %q", rendered)
	}
	if !strings.Contains(rendered, "Keep") {
		t.Fatalf("long merge error should keep the dialog actions visible, got %q", rendered)
	}
}

func TestOpenWorktreeMergeConfirmAutoClosesCompletedSession(t *testing.T) {
	rootPath := "/tmp/repo"
	childPath := "/tmp/repo--feat-parallel-lane"
	seenAt := time.Date(2026, 4, 4, 10, 0, 0, 0, time.UTC)
	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider: req.Provider.Normalized(),
				Started:  true,
				ThreadID: "thread-live",
				Status:   "Completed in 12s",
			},
		}, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: childPath,
		Provider:    codexapp.ProviderCodex,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	m := Model{
		codexManager:        manager,
		codexVisibleProject: childPath,
		nowFn:               func() time.Time { return seenAt },
		allProjects: []model.ProjectSummary{
			{
				Name:             "repo",
				Path:             rootPath,
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindMain,
				RepoBranch:       "master",
			},
			{
				Name:                            "repo--feat-parallel-lane",
				Path:                            childPath,
				PresentOnDisk:                   true,
				WorktreeRootPath:                rootPath,
				WorktreeKind:                    model.WorktreeKindLinked,
				WorktreeParentBranch:            "master",
				RepoBranch:                      "feat/parallel-lane",
				LatestSessionClassification:     model.ClassificationCompleted,
				LatestSessionClassificationType: model.SessionCategoryCompleted,
				LatestSessionFormat:             "modern",
				LatestSessionLastEventAt:        seenAt.Add(-2 * time.Minute),
				LatestTurnStateKnown:            true,
				LatestTurnCompleted:             true,
			},
		},
		visibility: visibilityAllFolders,
		sortMode:   sortByAttention,
	}
	m.rebuildProjectList(childPath)

	cmd := m.openWorktreeMergeConfirmForSelection()
	if cmd != nil {
		t.Fatalf("auto-closing a completed session should not require background work without a service")
	}
	if m.attentionDialog != nil {
		t.Fatalf("auto-close path should not show the attention dialog")
	}
	if m.worktreeMergeConfirm == nil {
		t.Fatalf("merge confirmation should open after auto-closing the completed session")
	}
	if m.status != "Confirm worktree merge-back" {
		t.Fatalf("status = %q, want merge confirmation", m.status)
	}
	if m.codexVisibleProject != "" {
		t.Fatalf("visible project should be cleared after auto-close, got %q", m.codexVisibleProject)
	}
	if _, ok := manager.Session(childPath); ok {
		t.Fatalf("completed session should be closed before opening merge confirm")
	}
	if !m.allProjects[1].LastSessionSeenAt.Equal(seenAt) {
		t.Fatalf("allProjects seen_at = %v, want %v", m.allProjects[1].LastSessionSeenAt, seenAt)
	}
	if !m.projects[1].LastSessionSeenAt.Equal(seenAt) {
		t.Fatalf("projects seen_at = %v, want %v", m.projects[1].LastSessionSeenAt, seenAt)
	}
}

func TestOpenWorktreeMergeConfirmAutoClosesSettledIdleSession(t *testing.T) {
	rootPath := "/tmp/repo"
	childPath := "/tmp/repo--feat-parallel-lane"
	seenAt := time.Date(2026, 4, 4, 11, 0, 0, 0, time.UTC)
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider: req.Provider.Normalized(),
				Started:  true,
				ThreadID: "thread-live",
				Phase:    codexapp.SessionPhaseIdle,
				Status:   "Recovered idle after status check",
			},
		}, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: childPath,
		Provider:    codexapp.ProviderCodex,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	m := Model{
		codexManager:        manager,
		codexVisibleProject: childPath,
		nowFn:               func() time.Time { return seenAt },
		allProjects: []model.ProjectSummary{
			{
				Name:             "repo",
				Path:             rootPath,
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindMain,
				RepoBranch:       "master",
			},
			{
				Name:                            "repo--feat-parallel-lane",
				Path:                            childPath,
				PresentOnDisk:                   true,
				WorktreeRootPath:                rootPath,
				WorktreeKind:                    model.WorktreeKindLinked,
				WorktreeParentBranch:            "master",
				RepoBranch:                      "feat/parallel-lane",
				LatestSessionClassification:     model.ClassificationCompleted,
				LatestSessionClassificationType: model.SessionCategoryCompleted,
				LatestSessionFormat:             "modern",
				LatestSessionLastEventAt:        seenAt.Add(-2 * time.Minute),
				LatestTurnStateKnown:            true,
				LatestTurnCompleted:             true,
			},
		},
		visibility: visibilityAllFolders,
		sortMode:   sortByAttention,
	}
	m.rebuildProjectList(childPath)

	cmd := m.openWorktreeMergeConfirmForSelection()
	if cmd != nil {
		t.Fatalf("auto-closing a settled idle session should not require background work without a service")
	}
	if m.attentionDialog != nil {
		t.Fatalf("settled idle auto-close path should not show the attention dialog")
	}
	if m.worktreeMergeConfirm == nil {
		t.Fatalf("merge confirmation should open after auto-closing the settled idle session")
	}
	if m.status != "Confirm worktree merge-back" {
		t.Fatalf("status = %q, want merge confirmation", m.status)
	}
	if m.codexVisibleProject != "" {
		t.Fatalf("visible project should be cleared after auto-close, got %q", m.codexVisibleProject)
	}
	if _, ok := manager.Session(childPath); ok {
		t.Fatalf("settled idle session should be closed before opening merge confirm")
	}
	if !m.allProjects[1].LastSessionSeenAt.Equal(seenAt) {
		t.Fatalf("allProjects seen_at = %v, want %v", m.allProjects[1].LastSessionSeenAt, seenAt)
	}
	if !m.projects[1].LastSessionSeenAt.Equal(seenAt) {
		t.Fatalf("projects seen_at = %v, want %v", m.projects[1].LastSessionSeenAt, seenAt)
	}
}

func TestOpenWorktreeMergeConfirmIncludesActiveRuntimeShutdown(t *testing.T) {
	rootPath := "/tmp/repo"
	childPath := "/tmp/repo--feat-runtime"
	m := Model{
		runtimeSnapshots: map[string]projectrun.Snapshot{
			childPath: {ProjectPath: childPath, Running: true},
		},
		allProjects: []model.ProjectSummary{
			{
				Name:             "repo",
				Path:             rootPath,
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindMain,
				RepoBranch:       "master",
			},
			{
				Name:                 "repo--feat-runtime",
				Path:                 childPath,
				PresentOnDisk:        true,
				WorktreeRootPath:     rootPath,
				WorktreeKind:         model.WorktreeKindLinked,
				WorktreeParentBranch: "master",
				WorktreeOriginTodoID: 42,
				RepoBranch:           "feat/runtime",
			},
		},
		visibility: visibilityAllFolders,
		sortMode:   sortByAttention,
	}
	m.rebuildProjectList(childPath)

	cmd := m.openWorktreeMergeConfirmForSelection()
	if cmd != nil {
		t.Fatalf("merge confirmation with a running runtime should not need background work")
	}
	if m.worktreeMergeConfirm == nil {
		t.Fatalf("merge confirmation should open even when a runtime is active")
	}
	if !m.worktreeMergeConfirm.RuntimeRunning || !m.worktreeMergeConfirm.StopRuntime {
		t.Fatalf("merge confirmation should default to stopping the active runtime, got %#v", m.worktreeMergeConfirm)
	}
	if !m.worktreeMergeConfirm.MarkTodoDone || !m.worktreeMergeConfirm.RemoveNow {
		t.Fatalf("merge confirmation should default to follow-up cleanup actions, got %#v", m.worktreeMergeConfirm)
	}
	if !worktreeMergeConfirmReady(m.worktreeMergeConfirm) {
		t.Fatalf("merge confirmation should stay runnable when runtime shutdown is selected")
	}
	if m.status != "Confirm worktree merge-back" {
		t.Fatalf("status = %q, want merge confirmation", m.status)
	}
}

func TestWorktreeMergePlanStopsRuntimeBeforeRunningGitActions(t *testing.T) {
	projectPath := t.TempDir()
	runtimeManager := projectrun.NewManager()
	defer runtimeManager.CloseAll()

	snapshot, err := runtimeManager.Start(projectrun.StartRequest{
		ProjectPath: projectPath,
		Command:     "sleep 30",
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if !snapshot.Running {
		t.Fatalf("runtime should be running after start, got %+v", snapshot)
	}
	extraSnapshot, err := runtimeManager.Start(projectrun.StartRequest{
		ProjectPath: projectPath,
		Command:     "sleep 30",
		Name:        "extra",
		CreateNew:   true,
	})
	if err != nil {
		t.Fatalf("second Start() error = %v", err)
	}
	if !extraSnapshot.Running {
		t.Fatalf("second runtime should be running after start, got %+v", extraSnapshot)
	}

	m := Model{
		runtimeManager: runtimeManager,
		allProjects: []model.ProjectSummary{{
			Name:                 "repo--feat-runtime",
			Path:                 projectPath,
			PresentOnDisk:        true,
			WorktreeRootPath:     "/tmp/repo",
			WorktreeKind:         model.WorktreeKindLinked,
			WorktreeParentBranch: "master",
			RepoBranch:           "feat/runtime",
		}},
		worktreeMergeConfirm: &worktreeMergeConfirmState{
			ProjectPath:    projectPath,
			RootPath:       "/tmp/repo",
			BranchName:     "feat/runtime",
			TargetBranch:   "master",
			RuntimeRunning: true,
			StopRuntime:    true,
			RemoveNow:      true,
		},
	}
	m.worktreeMergeConfirm.Selected = worktreeMergeConfirmApplyIndex(m.worktreeMergeConfirm)

	updated, cmd := m.updateWorktreeMergeConfirmMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("applying the merge plan should queue background work")
	}
	if got.worktreeMergeConfirm != nil {
		t.Fatalf("merge plan should dismiss the dialog while it runs")
	}
	if got.pendingGitSummary(projectPath) != worktreeMergePendingSummary {
		t.Fatalf("pending git summary = %q, want merge summary", got.pendingGitSummary(projectPath))
	}

	msg := cmd()
	action, ok := msg.(worktreeActionMsg)
	if !ok {
		t.Fatalf("merge plan command returned %T, want worktreeActionMsg", msg)
	}
	if action.err == nil || !strings.Contains(action.err.Error(), "service unavailable") {
		t.Fatalf("merge plan should reach the service boundary after stopping the runtime, got %#v", action)
	}
	for _, stopped := range runtimeManager.SnapshotsForProject(projectPath) {
		if stopped.Running {
			t.Fatalf("all runtimes should be stopped before the merge action returns, got %+v", stopped)
		}
	}
}

func TestBlockedWorktreeMergeEnterOnApplyKeepsDialogOpen(t *testing.T) {
	m := Model{
		allProjects: []model.ProjectSummary{{
			Name:                 "repo--feat-parallel-lane",
			Path:                 "/tmp/repo--feat-parallel-lane",
			PresentOnDisk:        true,
			WorktreeRootPath:     "/tmp/repo",
			WorktreeKind:         model.WorktreeKindLinked,
			WorktreeParentBranch: "master",
			RepoBranch:           "feat/parallel-lane",
			RepoDirty:            true,
		}},
		worktreeMergeConfirm: &worktreeMergeConfirmState{
			ProjectPath:       "/tmp/repo--feat-parallel-lane",
			RootPath:          "/tmp/repo",
			BranchName:        "feat/parallel-lane",
			TargetBranch:      "master",
			SourceDirty:       true,
			CommitBeforeMerge: false,
			RemoveNow:         true,
		},
	}
	m.worktreeMergeConfirm.Selected = worktreeMergeConfirmApplyIndex(m.worktreeMergeConfirm)

	updated, cmd := m.updateWorktreeMergeConfirmMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("blocked merge should not schedule a merge command")
	}
	if got.worktreeMergeConfirm == nil {
		t.Fatalf("blocked merge should keep the dialog open")
	}
	if got.status != "This worktree is dirty. Leave commit checked or clean it manually before merging back." {
		t.Fatalf("status = %q, want blocked merge reason", got.status)
	}
}

func TestBlockedWorktreeMergeEnterOnKeepCancels(t *testing.T) {
	m := Model{
		allProjects: []model.ProjectSummary{{
			Name:                 "repo--feat-parallel-lane",
			Path:                 "/tmp/repo--feat-parallel-lane",
			PresentOnDisk:        true,
			WorktreeRootPath:     "/tmp/repo",
			WorktreeKind:         model.WorktreeKindLinked,
			WorktreeParentBranch: "master",
			RepoBranch:           "feat/parallel-lane",
			RepoDirty:            true,
		}},
		worktreeMergeConfirm: &worktreeMergeConfirmState{
			ProjectPath:  "/tmp/repo--feat-parallel-lane",
			RootPath:     "/tmp/repo",
			BranchName:   "feat/parallel-lane",
			TargetBranch: "master",
			SourceDirty:  true,
			RemoveNow:    true,
		},
	}
	m.worktreeMergeConfirm.Selected = worktreeMergeConfirmKeepIndex(m.worktreeMergeConfirm)

	updated, cmd := m.updateWorktreeMergeConfirmMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("blocked keep should not schedule a merge command")
	}
	if got.worktreeMergeConfirm != nil {
		t.Fatalf("keep should close the blocked merge dialog")
	}
	if got.status != "Worktree merge-back canceled" {
		t.Fatalf("status = %q, want cancel status", got.status)
	}
}

func TestDirtyWorktreeMergeEnterOnApplyQueuesCommitAndMergePlan(t *testing.T) {
	projectPath := "/tmp/repo--feat-parallel-lane"
	m := Model{
		allProjects: []model.ProjectSummary{
			{
				Name:                 "repo--feat-parallel-lane",
				Path:                 projectPath,
				PresentOnDisk:        true,
				WorktreeRootPath:     "/tmp/repo",
				WorktreeKind:         model.WorktreeKindLinked,
				WorktreeParentBranch: "master",
				RepoBranch:           "feat/parallel-lane",
				RepoDirty:            true,
				LatestSessionSummary: "Updated the merge-back flow.",
			},
		},
		worktreeMergeConfirm: &worktreeMergeConfirmState{
			ProjectPath:       projectPath,
			RootPath:          "/tmp/repo",
			BranchName:        "feat/parallel-lane",
			TargetBranch:      "master",
			SourceDirty:       true,
			CommitBeforeMerge: true,
			HasLinkedTodo:     true,
			MarkTodoDone:      true,
			RemoveNow:         true,
		},
	}
	m.worktreeMergeConfirm.Selected = worktreeMergeConfirmApplyIndex(m.worktreeMergeConfirm)

	updated, cmd := m.updateWorktreeMergeConfirmMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("apply should queue the merge plan in the background")
	}
	if got.worktreeMergeConfirm != nil {
		t.Fatalf("commit & merge should dismiss the merge dialog immediately")
	}
	if got.commitPreview != nil {
		t.Fatalf("commit & merge should not open the commit preview")
	}
	if got.pendingGitSummary(projectPath) != worktreeCommitMergePendingSummary {
		t.Fatalf("pending git summary = %q, want commit-and-merge summary", got.pendingGitSummary(projectPath))
	}
	if got.status != worktreeCommitMergePendingSummary {
		t.Fatalf("status = %q, want async merge status", got.status)
	}
}

func TestWorktreeMergeEnterDismissesDialogWhileRunning(t *testing.T) {
	m := Model{
		allProjects: []model.ProjectSummary{{
			Name:                 "repo--feat-parallel-lane",
			Path:                 "/tmp/repo--feat-parallel-lane",
			PresentOnDisk:        true,
			WorktreeRootPath:     "/tmp/repo",
			WorktreeKind:         model.WorktreeKindLinked,
			WorktreeParentBranch: "master",
			RepoBranch:           "feat/parallel-lane",
		}},
		worktreeMergeConfirm: &worktreeMergeConfirmState{
			ProjectPath:  "/tmp/repo--feat-parallel-lane",
			RootPath:     "/tmp/repo",
			BranchName:   "feat/parallel-lane",
			TargetBranch: "master",
			RemoveNow:    true,
		},
		spinnerFrame: 2,
	}
	m.worktreeMergeConfirm.Selected = worktreeMergeConfirmApplyIndex(m.worktreeMergeConfirm)

	updated, cmd := m.updateWorktreeMergeConfirmMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("ready merge should queue a merge command")
	}
	if got.worktreeMergeConfirm != nil {
		t.Fatalf("merge dialog should close once the background work starts")
	}
	if got.pendingGitSummary("/tmp/repo--feat-parallel-lane") != worktreeMergePendingSummary {
		t.Fatalf("pending git summary = %q, want merge summary", got.pendingGitSummary("/tmp/repo--feat-parallel-lane"))
	}
	if got.status != worktreeMergePendingSummary {
		t.Fatalf("status = %q, want merge progress message", got.status)
	}
}

func TestDispatchCommandWorktreeMergeOpensConfirm(t *testing.T) {
	rootPath := "/tmp/repo"
	childPath := "/tmp/repo--feat-parallel-lane"
	m := Model{
		allProjects: []model.ProjectSummary{
			{
				Name:             "repo",
				Path:             rootPath,
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindMain,
				RepoBranch:       "master",
			},
			{
				Name:                 "repo--feat-parallel-lane",
				Path:                 childPath,
				PresentOnDisk:        true,
				WorktreeRootPath:     rootPath,
				WorktreeKind:         model.WorktreeKindLinked,
				WorktreeParentBranch: "master",
				RepoBranch:           "feat/parallel-lane",
			},
		},
		visibility: visibilityAllFolders,
		sortMode:   sortByAttention,
	}
	m.rebuildProjectList(childPath)

	updated, cmd := m.dispatchCommand(commands.Invocation{Kind: commands.KindWorktreeMerge})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("dispatchCommand(/wt merge) should open the confirmation dialog without scheduling work")
	}
	if got.worktreeMergeConfirm == nil {
		t.Fatalf("dispatchCommand(/wt merge) should open the merge confirmation dialog")
	}
}

func TestDispatchCommandWorktreeMergeBlockedWhenCommitIsInFlight(t *testing.T) {
	rootPath := "/tmp/repo"
	childPath := "/tmp/repo--feat-parallel-lane"
	m := Model{
		allProjects: []model.ProjectSummary{
			{
				Name:             "repo",
				Path:             rootPath,
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindMain,
				RepoBranch:       "master",
			},
			{
				Name:                 "repo--feat-parallel-lane",
				Path:                 childPath,
				PresentOnDisk:        true,
				WorktreeRootPath:     rootPath,
				WorktreeKind:         model.WorktreeKindLinked,
				WorktreeParentBranch: "master",
				RepoBranch:           "feat/parallel-lane",
			},
		},
		visibility: visibilityAllFolders,
		sortMode:   sortByAttention,
		pendingGitSummaries: map[string]string{
			childPath: "Committing...",
		},
	}
	m.rebuildProjectList(childPath)

	updated, cmd := m.dispatchCommand(commands.Invocation{Kind: commands.KindWorktreeMerge})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("commit-in-flight should block /wt merge without scheduling work")
	}
	if got.worktreeMergeConfirm != nil {
		t.Fatalf("merge confirmation should remain closed while a commit is running")
	}
	if got.status != "A commit is still in progress. Finish it before merging this worktree back." {
		t.Fatalf("status = %q, want commit in-flight gate message", got.status)
	}
}

func TestDispatchCommandWorktreeRemoveOpensConfirm(t *testing.T) {
	rootPath := "/tmp/repo"
	childPath := "/tmp/repo--feat-parallel-lane"
	m := Model{
		allProjects: []model.ProjectSummary{
			{
				Name:             "repo",
				Path:             rootPath,
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindMain,
			},
			{
				Name:             "repo--feat-parallel-lane",
				Path:             childPath,
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindLinked,
				RepoBranch:       "feat/parallel-lane",
			},
		},
		visibility: visibilityAllFolders,
		sortMode:   sortByAttention,
	}
	m.rebuildProjectList(childPath)

	updated, cmd := m.dispatchCommand(commands.Invocation{Kind: commands.KindWorktreeRemove})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("dispatchCommand(/wt remove) should open the confirmation dialog without scheduling work")
	}
	if got.worktreeRemoveConfirm == nil {
		t.Fatalf("dispatchCommand(/wt remove) should open the remove confirmation dialog")
	}
}

func TestLinkedWorktreeRemoveHotkeyStillOpensConfirm(t *testing.T) {
	rootPath := "/tmp/repo"
	childPath := "/tmp/repo--feat-parallel-lane"
	m := Model{
		allProjects: []model.ProjectSummary{
			{
				Name:             "repo",
				Path:             rootPath,
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindMain,
			},
			{
				Name:             "repo--feat-parallel-lane",
				Path:             childPath,
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindLinked,
				RepoBranch:       "feat/parallel-lane",
			},
		},
		visibility: visibilityAllFolders,
		sortMode:   sortByAttention,
	}
	m.rebuildProjectList(childPath)

	updated, cmd := m.updateNormalMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("x on linked worktree should open the confirmation dialog without scheduling work")
	}
	if got.worktreeRemoveConfirm == nil {
		t.Fatalf("x on linked worktree should open the remove confirmation dialog")
	}
}

func TestDispatchCommandRemoveOpensWorktreeRemoveConfirm(t *testing.T) {
	rootPath := "/tmp/repo"
	childPath := "/tmp/repo--feat-parallel-lane"
	m := Model{
		allProjects: []model.ProjectSummary{
			{
				Name:             "repo",
				Path:             rootPath,
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindMain,
			},
			{
				Name:             "repo--feat-parallel-lane",
				Path:             childPath,
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindLinked,
				RepoBranch:       "feat/parallel-lane",
			},
		},
		visibility: visibilityAllFolders,
		sortMode:   sortByAttention,
	}
	m.rebuildProjectList(childPath)

	updated, cmd := m.dispatchCommand(commands.Invocation{Kind: commands.KindRemove, Canonical: "/remove"})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("dispatchCommand(/remove) should open the confirmation dialog without scheduling work")
	}
	if got.worktreeRemoveConfirm == nil {
		t.Fatalf("dispatchCommand(/remove) should open the remove confirmation dialog for linked worktrees")
	}
}

func TestOpenWorktreeMergeConfirmWithLiveSessionShowsAttentionDialog(t *testing.T) {
	rootPath := "/tmp/repo"
	childPath := "/tmp/repo--feat-parallel-lane"
	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider: req.Provider.Normalized(),
				Started:  true,
				ThreadID: "thread-live",
				Status:   req.Provider.Label() + " session ready",
			},
		}, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: childPath,
		Provider:    codexapp.ProviderClaudeCode,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	m := Model{
		codexManager: manager,
		allProjects: []model.ProjectSummary{
			{
				Name:             "repo",
				Path:             rootPath,
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindMain,
				RepoBranch:       "master",
			},
			{
				Name:                 "repo--feat-parallel-lane",
				Path:                 childPath,
				PresentOnDisk:        true,
				WorktreeRootPath:     rootPath,
				WorktreeKind:         model.WorktreeKindLinked,
				WorktreeParentBranch: "master",
				RepoBranch:           "feat/parallel-lane",
			},
		},
		visibility: visibilityAllFolders,
		sortMode:   sortByAttention,
	}
	m.rebuildProjectList(childPath)

	cmd := m.openWorktreeMergeConfirmForSelection()
	if cmd != nil {
		t.Fatalf("blocked merge should not schedule a command")
	}
	if m.worktreeMergeConfirm != nil {
		t.Fatalf("merge confirmation dialog should stay closed when the session warning modal is shown")
	}
	if m.attentionDialog == nil {
		t.Fatalf("blocked merge should show the attention dialog")
	}
	if m.attentionDialog.Title != "Merge blocked" {
		t.Fatalf("attention dialog title = %q, want merge blocked", m.attentionDialog.Title)
	}
	if m.attentionDialog.PrimaryLabel != "Open Claude Code" {
		t.Fatalf("attention dialog primary label = %q, want open action", m.attentionDialog.PrimaryLabel)
	}
	if m.status != "Close the embedded agent session before merging this worktree back." {
		t.Fatalf("status = %q, want merge block warning", m.status)
	}
}

func TestOpenWorktreeRemoveConfirmWithLiveSessionShowsAttentionDialog(t *testing.T) {
	rootPath := "/tmp/repo"
	childPath := "/tmp/repo--feat-parallel-lane"
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider: req.Provider.Normalized(),
				Started:  true,
				ThreadID: "thread-live",
				Status:   req.Provider.Label() + " session ready",
			},
		}, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: childPath,
		Provider:    codexapp.ProviderClaudeCode,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	m := Model{
		codexManager: manager,
		allProjects: []model.ProjectSummary{
			{
				Name:             "repo",
				Path:             rootPath,
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindMain,
			},
			{
				Name:             "repo--feat-parallel-lane",
				Path:             childPath,
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindLinked,
				RepoBranch:       "feat/parallel-lane",
			},
		},
		visibility: visibilityAllFolders,
		sortMode:   sortByAttention,
	}
	m.rebuildProjectList(childPath)

	cmd := m.openWorktreeRemoveConfirmForSelection()
	if cmd != nil {
		t.Fatalf("blocked removal should not schedule a command")
	}
	if m.worktreeRemoveConfirm != nil {
		t.Fatalf("remove confirmation dialog should stay closed when the session warning modal is shown")
	}
	if m.attentionDialog == nil {
		t.Fatalf("blocked removal should show the attention dialog")
	}
	if m.attentionDialog.Title != "Remove blocked" {
		t.Fatalf("attention dialog title = %q, want remove blocked", m.attentionDialog.Title)
	}
	if m.attentionDialog.PrimaryLabel != "Open Claude Code" {
		t.Fatalf("attention dialog primary label = %q, want open action", m.attentionDialog.PrimaryLabel)
	}
	if m.status != "Close the embedded agent session before removing this worktree." {
		t.Fatalf("status = %q, want removal block warning", m.status)
	}
}

func TestRenderWorktreeRemoveConfirmShowsMergeSafetyCopy(t *testing.T) {
	m := Model{
		worktreeRemoveConfirm: &worktreeRemoveConfirmState{
			ProjectPath:  "/tmp/repo--feat-parallel-lane",
			RootPath:     "/tmp/repo",
			ProjectName:  "repo--feat-parallel-lane",
			BranchName:   "feat/parallel-lane",
			TargetBranch: "master",
			MergeStatus:  model.WorktreeMergeStatusNotMerged,
			Selected:     worktreeRemoveConfirmKeepIndex(nil),
		},
	}

	rendered := ansi.Strip(m.renderWorktreeRemoveConfirmOverlay("body", 90, 24))
	if !strings.Contains(rendered, "Pending merge") {
		t.Fatalf("remove confirm should call out unmerged worktrees, got %q", rendered)
	}
	if !strings.Contains(rendered, "still has commits to merge into master") {
		t.Fatalf("remove confirm should explain the merge target, got %q", rendered)
	}
	if !strings.Contains(rendered, "branch ref stays in the repo") {
		t.Fatalf("remove confirm should explain that only the checkout is removed, got %q", rendered)
	}
}

func TestWorktreeRemoveEnterDismissesDialogWhileRunning(t *testing.T) {
	m := Model{
		worktreeRemoveConfirm: &worktreeRemoveConfirmState{
			ProjectPath: "/tmp/repo--feat-parallel-lane",
			RootPath:    "/tmp/repo",
			BranchName:  "feat/parallel-lane",
			Selected:    worktreeRemoveConfirmRemoveIndex(nil),
		},
		spinnerFrame: 2,
	}

	updated, cmd := m.updateWorktreeRemoveConfirmMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("remove confirm should queue a removal command")
	}
	if got.worktreeRemoveConfirm != nil {
		t.Fatalf("remove dialog should close once the background work starts")
	}
	if got.pendingGitSummary("/tmp/repo--feat-parallel-lane") != worktreeRemovePendingSummary {
		t.Fatalf("pending git summary = %q, want remove summary", got.pendingGitSummary("/tmp/repo--feat-parallel-lane"))
	}
	if got.status != worktreeRemovePendingSummary {
		t.Fatalf("status = %q, want removal progress message", got.status)
	}
}

func TestDispatchCommandWorktreePruneQueuesCommand(t *testing.T) {
	rootPath := "/tmp/repo"
	m := Model{
		allProjects: []model.ProjectSummary{
			{
				Name:             "repo",
				Path:             rootPath,
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindMain,
			},
			{
				Name:             "repo--feat-parallel-lane",
				Path:             "/tmp/repo--feat-parallel-lane",
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindLinked,
				RepoBranch:       "feat/parallel-lane",
			},
		},
		visibility: visibilityAllFolders,
		sortMode:   sortByAttention,
	}
	m.rebuildProjectList(rootPath)

	updated, cmd := m.dispatchCommand(commands.Invocation{Kind: commands.KindWorktreePrune})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("dispatchCommand(/wt prune) should queue a prune command")
	}
	if got.status != "Pruning stale git worktrees..." {
		t.Fatalf("status = %q, want pruning status", got.status)
	}
}

func TestWorktreeActionMsgMergeCompletionDoesNotOpenFollowUpPrompt(t *testing.T) {
	rootPath := "/tmp/repo"
	childPath := "/tmp/repo--feat-parallel-lane"
	status := "Merged feat/parallel-lane into master"

	updated, cmd := Model{}.Update(worktreeActionMsg{
		projectPath: childPath,
		selectPath:  rootPath,
		status:      status,
	})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("merge completion should queue a refresh command batch")
	}
	if got.worktreePostMerge != nil {
		t.Fatalf("merge completion should stay in the single merge dialog flow")
	}
	if got.preferredSelectPath != "" {
		t.Fatalf("preferred select path = %q, want no forced selection", got.preferredSelectPath)
	}
	if got.status != status {
		t.Fatalf("status = %q, want %q", got.status, status)
	}
}

func TestWorktreeActionMsgMergeCompletionPreservesCurrentWorktreeSelectionThroughReload(t *testing.T) {
	rootPath := "/tmp/repo"
	mergingPath := "/tmp/repo--feat-parallel-lane"
	selectedPath := "/tmp/repo--feat-toolbar"

	root := model.ProjectSummary{
		Name:             "repo",
		Path:             rootPath,
		Status:           model.StatusIdle,
		PresentOnDisk:    true,
		WorktreeRootPath: rootPath,
		WorktreeKind:     model.WorktreeKindMain,
		RepoBranch:       "master",
	}
	merging := model.ProjectSummary{
		Name:                "repo--feat-parallel-lane",
		Path:                mergingPath,
		Status:              model.StatusIdle,
		PresentOnDisk:       true,
		WorktreeRootPath:    rootPath,
		WorktreeKind:        model.WorktreeKindLinked,
		WorktreeMergeStatus: model.WorktreeMergeStatusNotMerged,
		RepoBranch:          "feat/parallel-lane",
	}
	selected := model.ProjectSummary{
		Name:                "repo--feat-toolbar",
		Path:                selectedPath,
		Status:              model.StatusIdle,
		PresentOnDisk:       true,
		WorktreeRootPath:    rootPath,
		WorktreeKind:        model.WorktreeKindLinked,
		WorktreeMergeStatus: model.WorktreeMergeStatusMerged,
		RepoBranch:          "feat/toolbar",
	}
	m := Model{
		allProjects: []model.ProjectSummary{root, merging, selected},
		detail: model.ProjectDetail{
			Summary: selected,
		},
		sortMode:   sortByAttention,
		visibility: visibilityAllFolders,
	}
	m.rebuildProjectList(selectedPath)

	updated, cmd := m.Update(worktreeActionMsg{
		projectPath: mergingPath,
		selectPath:  rootPath,
		status:      "Merged feat/parallel-lane into master",
	})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("merge completion should queue a refresh command batch")
	}
	if got.currentSelectedProjectPath() != selectedPath {
		t.Fatalf("selected path after merge completion = %q, want %q", got.currentSelectedProjectPath(), selectedPath)
	}
	if got.preferredSelectPath != "" {
		t.Fatalf("preferred select path = %q, want no forced selection", got.preferredSelectPath)
	}

	merging.WorktreeMergeStatus = model.WorktreeMergeStatusMerged
	reloaded, _ := got.Update(projectsMsg{
		projects: []model.ProjectSummary{root, merging, selected},
	})
	got = reloaded.(Model)
	if got.currentSelectedProjectPath() != selectedPath {
		t.Fatalf("selected path after project reload = %q, want %q", got.currentSelectedProjectPath(), selectedPath)
	}
}

func TestWorktreeActionMsgRemoveHidesWorktreeImmediately(t *testing.T) {
	rootPath := "/tmp/repo"
	childPath := "/tmp/repo--feat-parallel-lane"

	root := model.ProjectSummary{
		Name:             "repo",
		Path:             rootPath,
		Status:           model.StatusIdle,
		PresentOnDisk:    true,
		WorktreeRootPath: rootPath,
		WorktreeKind:     model.WorktreeKindMain,
		RepoBranch:       "master",
	}
	child := model.ProjectSummary{
		Name:             "repo--feat-parallel-lane",
		Path:             childPath,
		Status:           model.StatusIdle,
		PresentOnDisk:    true,
		WorktreeRootPath: rootPath,
		WorktreeKind:     model.WorktreeKindLinked,
		RepoBranch:       "feat/parallel-lane",
	}
	m := Model{
		allProjects: []model.ProjectSummary{root, child},
		detail: model.ProjectDetail{
			Summary: child,
		},
		sortMode:   sortByAttention,
		visibility: visibilityAllFolders,
	}
	m.rebuildProjectList(childPath)

	updated, cmd := m.Update(worktreeActionMsg{
		projectPath:        childPath,
		removedProjectPath: childPath,
		selectPath:         rootPath,
		status:             "Worktree removed",
	})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("worktree removal completion should queue a refresh command batch")
	}
	if got.preferredSelectPath != "" {
		t.Fatalf("preferred select path = %q, want no forced selection", got.preferredSelectPath)
	}
	if len(got.projects) != 1 || got.projects[0].Path != rootPath {
		t.Fatalf("visible projects after local removal = %#v, want only the repo root", got.projects)
	}
	if _, ok := got.projectSummaryByPath(childPath); ok {
		t.Fatalf("removed worktree %q should be hidden from local project state", childPath)
	}
	if got.detail.Summary.Path != rootPath {
		t.Fatalf("detail path = %q, want immediate fallback to root %q", got.detail.Summary.Path, rootPath)
	}
}

func TestWorktreeActionMsgErrorLogsAsyncMergeFailure(t *testing.T) {
	errText := "merge conflict while merging feat/parallel-lane into master at /tmp/repo\nResolve or abort the merge in the root checkout before retrying.\nConflicted files:\n- README.md\n- STATUS.md"
	m := Model{
		pendingGitSummaries: map[string]string{
			"/tmp/repo--feat-parallel-lane": worktreeMergePendingSummary,
		},
	}

	updated, cmd := m.Update(worktreeActionMsg{
		projectPath:            "/tmp/repo--feat-parallel-lane",
		clearPendingGitSummary: true,
		err:                    errors.New(errText),
	})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("merge error should not queue follow-up work")
	}
	if got.worktreeMergeConfirm != nil {
		t.Fatalf("merge error should not reopen the merge dialog")
	}
	if got.pendingGitSummary("/tmp/repo--feat-parallel-lane") != "" {
		t.Fatalf("pending git summary = %q, want cleared", got.pendingGitSummary("/tmp/repo--feat-parallel-lane"))
	}
	if got.status != "Worktree action failed (use /errors)" {
		t.Fatalf("status = %q, want error hint", got.status)
	}
	if len(got.errorLogEntries) != 1 {
		t.Fatalf("error log count = %d, want 1", len(got.errorLogEntries))
	}
	if got.errorLogEntries[0].Message != errText {
		t.Fatalf("error log message = %q, want %q", got.errorLogEntries[0].Message, errText)
	}
}

func TestWorktreePostMergeEnterRemoveQueuesRemoval(t *testing.T) {
	rootPath := "/tmp/repo"
	childPath := "/tmp/repo--feat-parallel-lane"

	m := Model{
		worktreePostMerge: &worktreePostMergeState{
			ProjectPath:  childPath,
			RootPath:     rootPath,
			BranchName:   "feat/parallel-lane",
			TargetBranch: "master",
			Status:       "Merged feat/parallel-lane into master",
			RemoveNow:    true,
			Selected:     worktreePostMergeFocusRemove,
		},
	}

	updated, cmd := m.updateWorktreePostMergeMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("removing the merged worktree should queue a removal command")
	}
	if got.worktreePostMerge != nil {
		t.Fatalf("post-merge remove should dismiss the prompt immediately")
	}
	if got.pendingGitSummary(childPath) != worktreePostMergeRemoveSummary {
		t.Fatalf("pending git summary = %q, want merged-worktree remove summary", got.pendingGitSummary(childPath))
	}
	if got.status != worktreePostMergeRemoveSummary {
		t.Fatalf("status = %q, want removal progress", got.status)
	}
}

func TestWorktreePostMergeEnterRemoveDismissesDialogWhileRunning(t *testing.T) {
	rootPath := "/tmp/repo"
	childPath := "/tmp/repo--feat-parallel-lane"

	m := Model{
		worktreePostMerge: &worktreePostMergeState{
			ProjectPath:  childPath,
			RootPath:     rootPath,
			BranchName:   "feat/parallel-lane",
			TargetBranch: "master",
			Status:       "Merged feat/parallel-lane into master",
			RemoveNow:    true,
			Selected:     worktreePostMergeFocusRemove,
		},
		spinnerFrame: 2,
	}

	updated, cmd := m.updateWorktreePostMergeMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("post-merge remove should queue a removal command")
	}
	if got.worktreePostMerge != nil {
		t.Fatalf("post-merge dialog should close while removal runs")
	}
	if got.pendingGitSummary(childPath) != worktreePostMergeRemoveSummary {
		t.Fatalf("pending git summary = %q, want merged-worktree remove summary", got.pendingGitSummary(childPath))
	}
}

func TestWorktreePostMergeEnterDoneKeepsWorktreeQueuesTodoUpdate(t *testing.T) {
	rootPath := "/tmp/repo"
	childPath := "/tmp/repo--feat-parallel-lane"

	m := Model{
		worktreePostMerge: &worktreePostMergeState{
			ProjectPath:  childPath,
			RootPath:     rootPath,
			BranchName:   "feat/parallel-lane",
			TargetBranch: "master",
			TodoID:       7,
			TodoText:     "Finish the parallel lane work",
			TodoPath:     rootPath,
			MarkTodoDone: true,
			Status:       "Merged feat/parallel-lane into master",
			Selected:     worktreePostMergeFocusTodo,
		},
	}

	updated, cmd := m.updateWorktreePostMergeMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("marking the linked todo done should queue an update command")
	}
	if got.status != "Marking linked TODO done..." {
		t.Fatalf("status = %q, want todo progress", got.status)
	}
	if got.worktreePostMerge != nil {
		t.Fatalf("post-merge dialog should dismiss while updating the todo")
	}
}

func TestWorktreePostMergeSpaceTogglesSelectedOption(t *testing.T) {
	m := Model{
		worktreePostMerge: &worktreePostMergeState{
			ProjectPath:  "/tmp/repo--feat-parallel-lane",
			RootPath:     "/tmp/repo",
			BranchName:   "feat/parallel-lane",
			TargetBranch: "master",
			TodoID:       7,
			TodoText:     "Finish the parallel lane work",
			TodoPath:     "/tmp/repo",
			Status:       "Merged feat/parallel-lane into master",
			Selected:     worktreePostMergeFocusTodo,
		},
	}

	updated, cmd := m.updateWorktreePostMergeMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("toggling a post-merge option should not queue background work")
	}
	if got.worktreePostMerge == nil || !got.worktreePostMerge.MarkTodoDone {
		t.Fatalf("space should toggle the selected linked todo checkbox")
	}

	got.worktreePostMerge.Selected = worktreePostMergeFocusRemove
	updated, cmd = got.updateWorktreePostMergeMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	got = updated.(Model)
	if cmd != nil {
		t.Fatalf("toggling the remove option should not queue background work")
	}
	if got.worktreePostMerge == nil || !got.worktreePostMerge.RemoveNow {
		t.Fatalf("space should toggle the remove-worktree checkbox")
	}
}

func TestRenderWorktreePostMergeOverlayShowsSeparateCleanupChoices(t *testing.T) {
	rendered := ansi.Strip(Model{
		worktreePostMerge: &worktreePostMergeState{
			ProjectPath:  "/tmp/repo--feat-parallel-lane",
			RootPath:     "/tmp/repo",
			BranchName:   "feat/parallel-lane",
			TargetBranch: "master",
			TodoID:       7,
			TodoText:     "Finish the parallel lane work",
			TodoPath:     "/tmp/repo",
			Status:       "Merged feat/parallel-lane into master",
			Selected:     worktreePostMergeFocusTodo,
		},
	}.renderWorktreePostMergeOverlay("", 72, 24))

	for _, want := range []string{
		"Choose what to clean up now.",
		"The linked TODO and",
		"merged worktree are separate actions.",
		"Linked TODO",
		"Mark the originating TODO complete",
		"[ ] Finish the parallel lane work",
		"Worktree cleanup",
		"Remove this merged checkout now or keep it around",
		"for later. Removing it only deletes the checkout.",
		"[ ] Remove merged worktree now",
		"Enter  apply",
		"Esc",
		"later",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("post-merge overlay should render the full wrapped prompt copy, missing %q in %q", want, rendered)
		}
	}
}

func TestRenderWorktreePostMergeOverlayWithoutTodoShowsCleanupSection(t *testing.T) {
	rendered := ansi.Strip(Model{
		worktreePostMerge: &worktreePostMergeState{
			ProjectPath:  "/tmp/repo--feat-parallel-lane",
			RootPath:     "/tmp/repo",
			BranchName:   "feat/parallel-lane",
			TargetBranch: "master",
			Status:       "Merged feat/parallel-lane into master",
			Selected:     worktreePostMergeFocusRemove,
		},
	}.renderWorktreePostMergeOverlay("", 72, 24))

	for _, want := range []string{
		"Choose whether to remove this merged worktree now or",
		"keep it for later.",
		"Worktree cleanup",
		"Removing it only deletes the checkout.",
		"[ ] Remove merged worktree now",
		"Enter  apply",
		"Esc",
		"later",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("post-merge overlay without todo should show the cleanup section, missing %q in %q", want, rendered)
		}
	}
}

func TestWorktreeActionMsgErrorLogsAsyncPostMergeFailure(t *testing.T) {
	childPath := "/tmp/repo--feat-parallel-lane"
	errText := "linked TODO was marked done, but removing the worktree failed: remove git worktree"

	m := Model{
		pendingGitSummaries: map[string]string{
			childPath: worktreePostMergeRemoveSummary,
		},
	}

	updated, cmd := m.Update(worktreeActionMsg{
		projectPath:            childPath,
		clearPendingGitSummary: true,
		err:                    errors.New(errText),
	})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("post-merge error should not queue follow-up work")
	}
	if got.worktreePostMerge != nil {
		t.Fatalf("post-merge error should not reopen the prompt")
	}
	if got.pendingGitSummary(childPath) != "" {
		t.Fatalf("pending git summary = %q, want cleared", got.pendingGitSummary(childPath))
	}
	if got.status != "Worktree action failed (use /errors)" {
		t.Fatalf("status = %q, want error hint", got.status)
	}
	if len(got.errorLogEntries) != 1 {
		t.Fatalf("error log count = %d, want 1", len(got.errorLogEntries))
	}
	if got.errorLogEntries[0].Message != errText {
		t.Fatalf("error log message = %q, want %q", got.errorLogEntries[0].Message, errText)
	}
}
