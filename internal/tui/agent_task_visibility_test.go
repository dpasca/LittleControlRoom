package tui

import (
	"strings"
	"testing"
	"time"

	"lcroom/internal/model"

	"github.com/charmbracelet/x/ansi"
)

// A reload issued before a task was created must not drop that task when its
// stale snapshot lands after the creation.
func TestStaleProjectsReloadKeepsTaskCreatedWhileInFlight(t *testing.T) {
	m := Model{sortMode: sortByAttention, visibility: visibilityAIFolders}
	staleReload := m.loadProjectsMsgRevisionForTest()

	task := model.AgentTask{
		ID:            "agt_created_during_reload",
		Title:         "Director review",
		Status:        model.AgentTaskStatusActive,
		WorkspacePath: "/tmp/lcroom-agent-task-during-reload",
		CreatedAt:     time.Date(2026, 11, 20, 20, 40, 33, 0, time.UTC),
	}
	m.upsertOpenAgentTask(task)

	updated, _ := m.Update(projectsMsg{agentTaskRevision: staleReload})
	got := updated.(Model)
	if _, ok := got.agentTaskForProjectPath(task.WorkspacePath); !ok {
		t.Fatalf("stale reload dropped a task created while it was in flight: %#v", got.openAgentTasks)
	}
	if len(got.projects) != 1 || got.projects[0].Path != task.WorkspacePath {
		t.Fatalf("project rows = %#v, want the created task", got.projects)
	}

	// A reload issued after the creation is authoritative again.
	fresh := got.loadProjectsMsgRevisionForTest()
	updated, _ = got.Update(projectsMsg{agentTaskRevision: fresh})
	got = updated.(Model)
	if len(got.openAgentTasks) != 0 || len(got.agentTaskLocalChanges) != 0 {
		t.Fatalf("fresh reload should replace local edits, tasks=%#v changes=%#v", got.openAgentTasks, got.agentTaskLocalChanges)
	}
}

func TestStaleProjectsReloadDoesNotResurrectRemovedTask(t *testing.T) {
	task := model.AgentTask{
		ID:            "agt_closed_during_reload",
		Status:        model.AgentTaskStatusActive,
		WorkspacePath: "/tmp/lcroom-agent-task-closed",
	}
	m := Model{sortMode: sortByAttention, visibility: visibilityAIFolders, openAgentTasks: []model.AgentTask{task}}
	staleReload := m.loadProjectsMsgRevisionForTest()

	archived := task
	archived.Status = model.AgentTaskStatusArchived
	m.upsertOpenAgentTask(archived)

	updated, _ := m.Update(projectsMsg{agentTaskRevision: staleReload, openAgentTasks: []model.AgentTask{task}})
	got := updated.(Model)
	if _, ok := got.agentTaskForProjectPath(task.WorkspacePath); ok {
		t.Fatalf("stale reload resurrected a task archived while it was in flight")
	}
}

// loadProjectsMsgRevisionForTest returns the task revision a reload command
// issued now would carry.
func (m Model) loadProjectsMsgRevisionForTest() uint64 {
	return m.loadProjects(time.Second).agentTaskRevision
}

func TestEmbeddedSidebarListsAgentTasksDelegatedFromSession(t *testing.T) {
	projectPath := "/tmp/lcr-sidebar-demo"
	m := testEmbeddedSidebarModel(projectPath)
	snapshot := testEmbeddedSidebarSnapshot(projectPath)
	m.openAgentTasks = []model.AgentTask{
		{
			ID:                 "agt_from_worktree",
			Title:              "Facing a new reality — director review",
			Status:             model.AgentTaskStatusActive,
			WorkspacePath:      "/tmp/lcroom-agent-task-1",
			OriginProjectPath:  "/tmp/lcr-sidebar",
			OriginWorktreePath: projectPath,
		},
		{
			ID:              "agt_from_session",
			Title:           "Session follow-up",
			Status:          model.AgentTaskStatusWaiting,
			WorkspacePath:   "/tmp/lcroom-agent-task-2",
			OriginSessionID: snapshot.ThreadID,
		},
		{
			ID:                "agt_elsewhere",
			Title:             "Unrelated work",
			Status:            model.AgentTaskStatusActive,
			WorkspacePath:     "/tmp/lcroom-agent-task-3",
			OriginProjectPath: "/tmp/other-project",
		},
		{
			ID:                 "agt_finished",
			Title:              "Finished review",
			Status:             model.AgentTaskStatusCompleted,
			WorkspacePath:      "/tmp/lcroom-agent-task-4",
			OriginWorktreePath: projectPath,
		},
	}

	rendered := ansi.Strip(strings.Join(m.renderEmbeddedSidebarProcessSection(snapshot, projectPath, 46), "\n"))
	for _, want := range []string{"Facing a new reality", "review Session follow-up"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("process section missing delegated task %q:\n%s", want, rendered)
		}
	}
	for _, unwanted := range []string{"Unrelated work", "Finished review", "No active project processes"} {
		if strings.Contains(rendered, unwanted) {
			t.Fatalf("process section should not show %q:\n%s", unwanted, rendered)
		}
	}

	detail := ansi.Strip(strings.Join(m.embeddedSidebarProcessDetailRows(snapshot, projectPath, 46, 0), "\n"))
	for _, want := range []string{"agt_from_worktree", "agt_from_session", "Open it from the project list"} {
		if !strings.Contains(detail, want) {
			t.Fatalf("process detail missing %q:\n%s", want, detail)
		}
	}
	for _, row := range m.embeddedSidebarProcessDetailRows(snapshot, projectPath, 32, 0) {
		if ansi.StringWidth(row) > 32 {
			t.Fatalf("delegated task detail overflows narrow sidebar: %q", row)
		}
	}
}
