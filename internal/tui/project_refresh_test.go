package tui

import (
	"fmt"
	"strings"
	"testing"

	"lcroom/internal/commands"
	"lcroom/internal/events"
	"lcroom/internal/model"

	"github.com/charmbracelet/x/ansi"
)

func TestProjectRefreshRequestsCoalesceWhileInFlight(t *testing.T) {
	m := Model{}

	first := m.requestProjectInvalidationCmd(invalidateProjectScan("", false))
	if first == nil {
		t.Fatal("first refresh request should schedule work")
	}
	if !m.scanInFlight || !m.projectsReloadInFlight {
		t.Fatalf("refresh request should mark scan and project list loads in flight")
	}

	second := m.requestProjectInvalidationCmd(invalidateProjectScan("", false))
	if second != nil {
		t.Fatal("duplicate refresh request should coalesce while work is already in flight")
	}
	if !m.scanQueued || !m.projectsReloadQueued {
		t.Fatalf("duplicate refresh request should queue a rerun")
	}
}

func TestProjectDataInvalidationReloadsVisibleDetailOnly(t *testing.T) {
	m := Model{
		projects: []model.ProjectSummary{{
			Path: "/tmp/current",
			Name: "current",
		}},
		selected: 0,
		detail: model.ProjectDetail{
			Summary: model.ProjectSummary{
				Path: "/tmp/current",
				Name: "current",
			},
		},
	}

	cmd := m.requestProjectInvalidationCmd(invalidateProjectData("/tmp/current"))
	if cmd == nil {
		t.Fatal("visible project data refresh should schedule work")
	}
	if !m.summaryReloadInFlight["/tmp/current"] {
		t.Fatal("visible project data refresh should reload the project summary")
	}
	if !m.detailReloadInFlight["/tmp/current"] {
		t.Fatal("visible project data refresh should reload the visible detail pane")
	}

	hidden := Model{
		projects: []model.ProjectSummary{{
			Path: "/tmp/other",
			Name: "other",
		}},
		selected: 0,
		detail: model.ProjectDetail{
			Summary: model.ProjectSummary{
				Path: "/tmp/other",
				Name: "other",
			},
		},
	}

	cmd = hidden.requestProjectInvalidationCmd(invalidateProjectData("/tmp/current"))
	if cmd == nil {
		t.Fatal("hidden project data refresh should still reload summary data")
	}
	if !hidden.summaryReloadInFlight["/tmp/current"] {
		t.Fatal("hidden project data refresh should reload the project summary")
	}
	if len(hidden.detailReloadInFlight) != 0 {
		t.Fatalf("hidden project data refresh should not reload unrelated detail panes: %#v", hidden.detailReloadInFlight)
	}
}

func TestRunCommandSavedMsgRefreshesOnlyProjectData(t *testing.T) {
	m := Model{
		projects: []model.ProjectSummary{{
			Path: "/tmp/demo",
			Name: "demo",
		}},
		allProjects: []model.ProjectSummary{{
			Path: "/tmp/demo",
			Name: "demo",
		}},
		selected: 0,
		detail: model.ProjectDetail{
			Summary: model.ProjectSummary{
				Path: "/tmp/demo",
				Name: "demo",
			},
		},
		runCommandDialog: &runCommandDialogState{
			ProjectPath: "/tmp/demo",
			Submitting:  true,
		},
	}

	updated, cmd := m.Update(runCommandSavedMsg{
		projectPath: "/tmp/demo",
	})
	got := updated.(Model)

	if cmd == nil {
		t.Fatal("run command save should schedule a refresh")
	}
	if got.projectsReloadInFlight {
		t.Fatal("run command save should not force a full project list reload")
	}
	if !got.summaryReloadInFlight["/tmp/demo"] {
		t.Fatal("run command save should reload the updated project summary")
	}
	if !got.detailReloadInFlight["/tmp/demo"] {
		t.Fatal("run command save should reload the visible project detail")
	}
	if got.runCommandDialog != nil {
		t.Fatal("run command dialog should close after a successful save")
	}
	if got.status != "Saved run command" {
		t.Fatalf("status = %q, want saved message", got.status)
	}
}

func TestBusProjectChangedRefreshesOnlyProjectDataWhenPathKnown(t *testing.T) {
	m := Model{
		projects: []model.ProjectSummary{{
			Path: "/tmp/demo",
			Name: "demo",
		}},
		selected: 0,
		detail: model.ProjectDetail{
			Summary: model.ProjectSummary{
				Path: "/tmp/demo",
				Name: "demo",
			},
		},
	}

	updated, cmd := m.Update(busMsg{
		Type:        events.ProjectChanged,
		ProjectPath: "/tmp/demo",
	})
	got := updated.(Model)

	if cmd == nil {
		t.Fatal("project changed event should schedule a refresh")
	}
	if got.projectsReloadInFlight {
		t.Fatal("project changed event with a path should not reload the whole project list")
	}
	if !got.summaryReloadInFlight["/tmp/demo"] {
		t.Fatal("project changed event should reload the updated project summary")
	}
	if !got.detailReloadInFlight["/tmp/demo"] {
		t.Fatal("project changed event should reload the visible detail pane")
	}
}

func TestBusProjectMovedRefreshesProjectStructureAndSelectedDetail(t *testing.T) {
	m := Model{
		projects: []model.ProjectSummary{{
			Path: "/tmp/demo",
			Name: "demo",
		}},
		selected: 0,
		detail: model.ProjectDetail{
			Summary: model.ProjectSummary{
				Path: "/tmp/demo",
				Name: "demo",
			},
		},
	}

	updated, cmd := m.Update(busMsg{
		Type:        events.ProjectMoved,
		ProjectPath: "/tmp/demo",
	})
	got := updated.(Model)

	if cmd == nil {
		t.Fatal("project moved event should schedule a refresh")
	}
	if !got.projectsReloadInFlight {
		t.Fatal("project moved event should reload the project list")
	}
	if got.scanInFlight {
		t.Fatal("project moved event should not queue a full scan")
	}
	if !got.detailReloadInFlight["/tmp/demo"] {
		t.Fatal("project moved event should reload the selected project detail")
	}
	if len(got.summaryReloadInFlight) != 0 {
		t.Fatalf("project moved event should not queue per-project summary reloads: %#v", got.summaryReloadInFlight)
	}
}

func TestBusEventsDroppedRefreshesProjectStructureAndSelectedDetail(t *testing.T) {
	m := Model{
		projects: []model.ProjectSummary{{
			Path: "/tmp/demo",
			Name: "demo",
		}},
		selected: 0,
		detail: model.ProjectDetail{
			Summary: model.ProjectSummary{
				Path: "/tmp/demo",
				Name: "demo",
			},
		},
	}

	updated, cmd := m.Update(busMsg{
		Type: events.EventsDropped,
	})
	got := updated.(Model)

	if cmd == nil {
		t.Fatal("events dropped event should schedule a conservative refresh")
	}
	if !got.projectsReloadInFlight {
		t.Fatal("events dropped event should reload the project list")
	}
	if got.scanInFlight {
		t.Fatal("events dropped event should not queue a full scan")
	}
	if !got.detailReloadInFlight["/tmp/demo"] {
		t.Fatal("events dropped event should reload the selected project detail")
	}
	if len(got.summaryReloadInFlight) != 0 {
		t.Fatalf("events dropped event should not queue per-project summary reloads: %#v", got.summaryReloadInFlight)
	}
}

func TestDetailMsgIgnoresStaleSelectionResult(t *testing.T) {
	m := Model{
		projects: []model.ProjectSummary{{
			Path: "/tmp/current",
			Name: "current",
		}},
		selected: 0,
		detail: model.ProjectDetail{
			Summary: model.ProjectSummary{
				Path: "/tmp/current",
				Name: "current",
			},
		},
		detailReloadInFlight: map[string]bool{
			"/tmp/old": true,
		},
	}

	updated, cmd := m.Update(detailMsg{
		path: "/tmp/old",
		detail: model.ProjectDetail{
			Summary: model.ProjectSummary{
				Path: "/tmp/old",
				Name: "old",
			},
		},
	})
	got := updated.(Model)

	if cmd != nil {
		t.Fatal("stale detail result should not schedule follow-up work")
	}
	if got.detail.Summary.Path != "/tmp/current" {
		t.Fatalf("detail path = %q, want current selection to remain visible", got.detail.Summary.Path)
	}
	if got.detail.Summary.Name != "current" {
		t.Fatalf("detail name = %q, want stale result ignored", got.detail.Summary.Name)
	}
}

func TestDetailMsgUsesTodoDialogProjectPathWhenLinkedWorktreeSelected(t *testing.T) {
	rootPath := "/tmp/repo"
	worktreePath := "/tmp/repo--todo-fix"
	m := Model{
		projects: []model.ProjectSummary{{
			Path:             worktreePath,
			Name:             "repo--todo-fix",
			WorktreeRootPath: rootPath,
			WorktreeKind:     model.WorktreeKindLinked,
		}},
		selected: 0,
		detail: model.ProjectDetail{
			Summary: model.ProjectSummary{
				Path: worktreePath,
				Name: "repo--todo-fix",
			},
		},
		todoDialog: &todoDialogState{
			ProjectPath: rootPath,
			ProjectName: "repo",
		},
		detailReloadInFlight: map[string]bool{
			rootPath: true,
		},
	}

	updated, cmd := m.Update(detailMsg{
		path: rootPath,
		detail: model.ProjectDetail{
			Summary: model.ProjectSummary{
				Path: rootPath,
				Name: "repo",
			},
			Todos: []model.TodoItem{{
				ID:          7,
				ProjectPath: rootPath,
				Text:        "Root TODO should stay visible from a linked worktree",
			}},
		},
	})
	got := updated.(Model)

	if cmd != nil {
		t.Fatal("detail reload completion should not schedule follow-up work")
	}
	if got.detail.Summary.Path != rootPath {
		t.Fatalf("detail path = %q, want todo dialog project path", got.detail.Summary.Path)
	}
	if len(got.todoItemsFor(rootPath)) != 1 {
		t.Fatalf("todo item count = %d, want 1 for the repo root dialog", len(got.todoItemsFor(rootPath)))
	}
}

func TestOpenTodoDialogRequestsFreshDetailWhenSummaryHasTodosButDetailIsStale(t *testing.T) {
	projectPath := "/tmp/demo"
	project := model.ProjectSummary{
		Path:           projectPath,
		Name:           "demo",
		OpenTODOCount:  1,
		TotalTODOCount: 1,
	}
	m := Model{
		projects:    []model.ProjectSummary{project},
		allProjects: []model.ProjectSummary{project},
		selected:    0,
		detail: model.ProjectDetail{
			Summary: model.ProjectSummary{Path: projectPath},
		},
		width:  100,
		height: 24,
	}

	cmd := m.openTodoDialog(project)

	if cmd == nil {
		t.Fatal("opening the TODO dialog should request a fresh detail snapshot")
	}
	if !m.detailReloadInFlight[projectPath] {
		t.Fatalf("detail reloads = %#v, want %q in flight", m.detailReloadInFlight, projectPath)
	}
	rendered := ansi.Strip(m.renderTodoDialogOverlay("", 100, 24))
	if !strings.Contains(rendered, "Loading TODOs") {
		t.Fatalf("dialog should avoid a false empty state while detail reloads, got %q", rendered)
	}
	if strings.Contains(rendered, "No TODOs yet") {
		t.Fatalf("dialog should not claim no TODOs while summary count is non-zero, got %q", rendered)
	}
}

func TestSlashTodoUsesRepoRootAndRequestsFreshDetailForLinkedWorktree(t *testing.T) {
	rootPath := "/tmp/repo"
	worktreePath := "/tmp/repo--todo-fix"
	root := model.ProjectSummary{
		Path:             rootPath,
		Name:             "repo",
		WorktreeRootPath: rootPath,
		WorktreeKind:     model.WorktreeKindMain,
		OpenTODOCount:    1,
		TotalTODOCount:   1,
	}
	worktree := model.ProjectSummary{
		Path:             worktreePath,
		Name:             "repo--todo-fix",
		WorktreeRootPath: rootPath,
		WorktreeKind:     model.WorktreeKindLinked,
	}
	m := Model{
		projects:    []model.ProjectSummary{worktree},
		allProjects: []model.ProjectSummary{root, worktree},
		selected:    0,
		detail: model.ProjectDetail{
			Summary: worktree,
		},
		width:  100,
		height: 24,
	}

	updated, cmd := m.dispatchCommand(commands.Invocation{Kind: commands.KindTodo})
	got := updated.(Model)

	if cmd == nil {
		t.Fatal("/todo should request a fresh root detail snapshot")
	}
	if got.todoDialog == nil {
		t.Fatal("/todo should open the TODO dialog")
	}
	if got.todoDialog.ProjectPath != rootPath {
		t.Fatalf("todo dialog project path = %q, want root path %q", got.todoDialog.ProjectPath, rootPath)
	}
	if !got.detailReloadInFlight[rootPath] {
		t.Fatalf("detail reloads = %#v, want root path in flight", got.detailReloadInFlight)
	}
	rendered := ansi.Strip(got.renderTodoDialogOverlay("", 100, 24))
	if !strings.Contains(rendered, "Loading TODOs") {
		t.Fatalf("dialog should show a pending root TODO load, got %q", rendered)
	}
}

func TestProjectsMsgRerunsQueuedProjectsReload(t *testing.T) {
	m := Model{
		projectsReloadInFlight: true,
		projectsReloadQueued:   true,
	}

	updated, cmd := m.Update(projectsMsg{})
	got := updated.(Model)

	if cmd == nil {
		t.Fatal("queued project reload should schedule a rerun after completion")
	}
	if !got.projectsReloadInFlight {
		t.Fatal("queued rerun should put project reload back in flight")
	}
	if got.projectsReloadQueued {
		t.Fatal("queued rerun flag should clear once the rerun is scheduled")
	}
}

func TestProjectsMsgClearsInitialLoadingStatusAfterStartupLoad(t *testing.T) {
	m := Model{
		loading:    true,
		status:     initialProjectsStatus,
		sortMode:   sortByAttention,
		visibility: visibilityAIFolders,
	}

	updated, _ := m.Update(projectsMsg{
		projects: []model.ProjectSummary{{
			Name:                "demo",
			Path:                "/tmp/demo",
			PresentOnDisk:       true,
			LatestSessionFormat: "modern",
		}},
	})
	got := updated.(Model)

	want := loadedProjectsStatus(1, sortByAttention, visibilityAIFolders, "")
	if got.status != want {
		t.Fatalf("status = %q, want %q", got.status, want)
	}
	if got.loading {
		t.Fatalf("loading should be false after projects load")
	}
}

func TestProjectsMsgShowsStartupFailureStatusWhenInitialLoadFails(t *testing.T) {
	m := Model{
		loading: true,
		status:  initialProjectsStatus,
	}

	updated, _ := m.Update(projectsMsg{err: fmt.Errorf("open store: database is locked")})
	got := updated.(Model)

	if got.status != "Project load failed" {
		t.Fatalf("status = %q, want project-load failure status", got.status)
	}
	if got.err == nil || got.err.Error() != "open store: database is locked" {
		t.Fatalf("err = %v, want startup load error", got.err)
	}
	if got.loading {
		t.Fatalf("loading should be false after project load failure")
	}
}

func TestProjectsMsgKeepsInitialLoadingStateWhenStartupCacheIsEmpty(t *testing.T) {
	m := Model{
		loading:    true,
		status:     initialProjectsStatus,
		sortMode:   sortByAttention,
		visibility: visibilityAIFolders,
	}

	updated, _ := m.Update(projectsMsg{projects: nil})
	got := updated.(Model)

	if !got.loading {
		t.Fatalf("loading should stay true while the initial background scan is still pending")
	}
	if got.status != initialProjectsStatus {
		t.Fatalf("status = %q, want initial loading status", got.status)
	}
}

func TestProjectSummaryMsgAddsProjectDuringInitialScan(t *testing.T) {
	m := Model{
		loading:    true,
		status:     initialProjectsStatus,
		sortMode:   sortByAttention,
		visibility: visibilityAIFolders,
	}

	updated, _ := m.Update(projectSummaryMsg{
		found: true,
		summary: model.ProjectSummary{
			Name:                "demo",
			Path:                "/tmp/demo",
			PresentOnDisk:       true,
			LatestSessionFormat: "modern",
		},
	})
	got := updated.(Model)

	if got.loading {
		t.Fatalf("loading should stop once the first project summary arrives")
	}
	if len(got.allProjects) != 1 || len(got.projects) != 1 {
		t.Fatalf("expected project summary to be merged into the list, got all=%d visible=%d", len(got.allProjects), len(got.projects))
	}
	if got.projects[0].Path != "/tmp/demo" {
		t.Fatalf("project path = %q, want /tmp/demo", got.projects[0].Path)
	}
}

func TestProjectsMsgKeepsLatestAssessmentDisplayDuringRefresh(t *testing.T) {
	previous := model.ProjectSummary{
		Path:                            "/tmp/demo",
		Name:                            "demo",
		PresentOnDisk:                   true,
		LatestSessionID:                 "codex:ses_current",
		LatestSessionFormat:             "modern",
		LatestSessionClassification:     model.ClassificationCompleted,
		LatestSessionClassificationType: model.SessionCategoryWaitingForUser,
		LatestSessionSummary:            "Current session is waiting on review.",
	}
	refreshing := model.ProjectSummary{
		Path:                                     previous.Path,
		Name:                                     previous.Name,
		PresentOnDisk:                            true,
		LatestSessionID:                          previous.LatestSessionID,
		LatestSessionFormat:                      previous.LatestSessionFormat,
		LatestSessionClassification:              model.ClassificationPending,
		LatestSessionClassificationStage:         model.ClassificationStageQueued,
		LatestCompletedSessionClassificationType: model.SessionCategoryNeedsFollowUp,
		LatestCompletedSessionSummary:            "Older session needs follow-up.",
	}
	m := Model{
		allProjects: []model.ProjectSummary{previous},
		projects:    []model.ProjectSummary{previous},
		sortMode:    sortByAttention,
		visibility:  visibilityAllFolders,
	}

	updated, _ := m.Update(projectsMsg{projects: []model.ProjectSummary{refreshing}})
	got := updated.(Model)

	if len(got.projects) != 1 {
		t.Fatalf("projects len = %d, want 1", len(got.projects))
	}
	project := got.projects[0]
	if project.LatestSessionSummary != previous.LatestSessionSummary {
		t.Fatalf("latest summary = %q, want preserved current summary", project.LatestSessionSummary)
	}
	if gotStatus := projectListStatus(project); gotStatus != "waiting" {
		t.Fatalf("projectListStatus() = %q, want preserved current assessment label", gotStatus)
	}
}

func TestProjectSummaryMsgRemovesProjectWhenItFallsOutOfVisibleSet(t *testing.T) {
	m := Model{
		sortMode:   sortByAttention,
		visibility: visibilityAIFolders,
		allProjects: []model.ProjectSummary{{
			Name:                "demo",
			Path:                "/tmp/demo",
			PresentOnDisk:       true,
			LatestSessionFormat: "modern",
		}},
		projects: []model.ProjectSummary{{
			Name:                "demo",
			Path:                "/tmp/demo",
			PresentOnDisk:       true,
			LatestSessionFormat: "modern",
		}},
	}

	updated, _ := m.Update(projectSummaryMsg{path: "/tmp/demo"})
	got := updated.(Model)

	if len(got.allProjects) != 0 || len(got.projects) != 0 {
		t.Fatalf("expected project summary removal to prune the list, got all=%d visible=%d", len(got.allProjects), len(got.projects))
	}
}

func TestProjectsMsgShowsRefreshFailureStatusWhenReloadFails(t *testing.T) {
	m := Model{
		loading: false,
		status:  "Loaded 3 projects (attention, AI folders)",
		projects: []model.ProjectSummary{{
			Name:          "demo",
			Path:          "/tmp/demo",
			PresentOnDisk: true,
		}},
	}

	updated, _ := m.Update(projectsMsg{err: fmt.Errorf("read config failed")})
	got := updated.(Model)

	if got.status != "Project refresh failed" {
		t.Fatalf("status = %q, want project-refresh failure status", got.status)
	}
	if got.err == nil || got.err.Error() != "read config failed" {
		t.Fatalf("err = %v, want refresh error", got.err)
	}
}
