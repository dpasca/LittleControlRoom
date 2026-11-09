package tui

import (
	"strings"
	"testing"
	"time"

	"lcroom/internal/commands"
	"lcroom/internal/model"
	"lcroom/internal/service"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestWorkspaceWarningsUseObservedStateAndKeepCompletion(t *testing.T) {
	for state, want := range map[model.AgentTaskWorkspaceState]string{
		model.AgentTaskWorkspaceMissing:          "Workspace folder missing",
		model.AgentTaskWorkspaceMetadataMissing:  "Git metadata missing",
		model.AgentTaskWorkspaceInspectionFailed: "Workspace inspection failed",
	} {
		task := model.AgentTask{ID: "completed", Status: model.AgentTaskStatusCompleted, WorkspacePath: "/not-read-by-render",
			WorkspaceGit: model.AgentTaskWorkspaceGit{State: state, Error: "specific failure"}}
		if got := agentTaskListSummary(task); !strings.Contains(got, want) {
			t.Fatalf("%s: summary = %q", state, got)
		}
		if agentTaskListStatus(task) != "done" || agentTaskAttentionScore(task) == 0 {
			t.Fatal("workspace warning erased task completion or lost attention")
		}
	}
	retained := model.AgentTask{Status: model.AgentTaskStatusArchived}
	if agentTaskIsVisible(retained) || agentTaskAttentionScore(retained) != 0 {
		t.Fatal("normal Trash retention raised dashboard attention")
	}
	retained.CleanupError = "Git removal failed"
	if !agentTaskIsVisible(retained) || agentTaskListStatus(retained) != "cleanup" || agentTaskAttentionScore(retained) == 0 {
		t.Fatal("cleanup failure did not surface on its owning task")
	}
}

func TestTrashBrowserRetentionPrivacyInspectionAndFailure(t *testing.T) {
	now := time.Date(2026, 11, 10, 4, 0, 0, 0, time.Local)
	m := Model{privacyMode: true, width: 120, height: 40, nowFn: func() time.Time { return now }}
	cmd := m.openAgentTaskTrash()
	if cmd == nil || !m.agentTaskTrash.busy {
		t.Fatal("Trash did not start a background load")
	}
	updated, _ := m.applyAgentTaskTrashLoaded(agentTaskTrashLoadedMsg{items: []model.AgentTask{
		{ID: "retained", Title: "Music trials", WorkspacePath: "/tmp/retained", Status: model.AgentTaskStatusArchived, ArchivedAt: now, ExpiresAt: now.AddDate(0, 0, 7)},
		{ID: "private", Title: "Private task title", CategoryPrivate: true},
	}})
	m = updated.(Model)
	rendered := ansi.Strip(m.renderAgentTaskTrash("", 120, 40))
	for _, want := range []string{"Music trials", "Archived", "Workspace retained until Nov 17", "Enter", "inspect workspace"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("Trash missing %q: %s", want, rendered)
		}
	}
	if strings.Contains(rendered, "Private task title") || strings.Contains(rendered, "Never") {
		t.Fatal("Trash leaked private state or used a misleading activity time")
	}
	updated, cmd = m.updateAgentTaskTrash(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if cmd == nil || m.orphanedWorktreeInspection == nil || !m.orphanedWorktreeInspection.Busy {
		t.Fatal("Trash Enter did not open read-only inspection")
	}
	if _, duplicate := m.updateOrphanedWorktreeInspectionMode(tea.KeyMsg{Type: tea.KeyEnter}); duplicate != nil {
		t.Fatal("busy inspection allowed repeat activation")
	}
	updated, _ = m.Update(cmd()) // No service: retain a visible failure and retry action.
	m = updated.(Model)
	if m.orphanedWorktreeInspection.Busy || !strings.Contains(ansi.Strip(m.View()), "service unavailable") {
		t.Fatal("inspection failure was hidden behind the Trash browser")
	}
}

func TestWorkspaceCommandAndInspectionSeparateCurrentFromRecordedEvidence(t *testing.T) {
	task := model.AgentTask{ID: "completed", Title: "HUD markers", WorkspacePath: "/tmp/owner/workspace", Status: model.AgentTaskStatusCompleted,
		Repository: model.AgentTaskRepository{Write: true, Changes: "M old-handoff.cpp"}}
	m := Model{openAgentTasks: []model.AgentTask{task}, visibility: visibilityAllFolders, sortMode: sortByAttention}
	m.rebuildProjectList(task.WorkspacePath)
	updated, cmd := m.dispatchCommand(commands.Invocation{Kind: commands.KindWorkspace})
	m = updated.(Model)
	if cmd == nil || m.orphanedWorktreeInspection == nil {
		t.Fatal("workspace command did not inspect selected task")
	}
	m.orphanedWorktreeInspection.Busy = false
	m.orphanedWorktreeInspection.HaveInspection = true
	m.orphanedWorktreeInspection.Inspection = service.OrphanedWorktreeInspection{
		AgentTask: task, Registration: "Not registered in Git", StatusError: "Git metadata missing",
	}
	rendered := ansi.Strip(m.renderOrphanedWorktreeInspectionContent(90))
	for _, want := range []string{"Task workspace", "HUD markers", "completed", "Not registered in Git", "Recorded handoff changes", "Historical host capture", task.WorkspacePath} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("inspection missing %q: %s", want, rendered)
		}
	}
	if strings.Contains(rendered, "Current checkout:") || orphanedWorktreeInspectionCanDelete(m.orphanedWorktreeInspection) {
		t.Fatal("historical handoff was presented as current verification or deletion authority")
	}
}

func TestWorkspaceInspectionScrollKeepsActionsVisibleOnShortTerminal(t *testing.T) {
	m := Model{orphanedWorktreeInspection: &orphanedWorktreeInspectionState{
		ProjectPath: "/tmp/" + strings.Repeat("long-directory/", 12), HaveInspection: true,
		Inspection: service.OrphanedWorktreeInspection{
			Resolution: service.OrphanedWorktreeResolutionDeleteArchivedTaskNow,
			AgentTask: model.AgentTask{ID: "retained", Status: model.AgentTaskStatusArchived, Title: "Retained task",
				Repository: model.AgentTaskRepository{Changes: strings.Repeat("M source.cpp\n", 12)}},
			StatusError: "Git status could not be read",
		},
	}}
	content := m.renderOrphanedWorktreeInspectionContent(65)
	for _, offset := range []int{0, 10000} {
		view := ansi.Strip(workspaceInspectionViewport(content, 18, offset, 6))
		if lines := strings.Count(view, "\n") + 1; lines > 18 {
			t.Fatalf("inspection overflowed short terminal: %d lines", lines)
		}
		for _, want := range []string{"Retained task workspace", "Delete Now", "Keep Until Auto-Delete", "PgUp/PgDn", "Esc"} {
			if !strings.Contains(view, want) {
				t.Fatalf("scroll offset %d hid %q: %s", offset, want, view)
			}
		}
	}
	bottom := ansi.Strip(workspaceInspectionViewport(content, 18, 10000, 6))
	if !strings.Contains(bottom, "Git status could not be read") {
		t.Fatal("scroll did not make final inspection evidence accessible")
	}
}

func TestUnclaimedActivityIsUnknownInListAndDetail(t *testing.T) {
	root, path := "/tmp/demo", "/tmp/unclaimed"
	m := Model{
		allProjects: []model.ProjectSummary{{Path: root, Name: "Demo", WorktreeRootPath: root, WorktreeKind: model.WorktreeKindMain, PresentOnDisk: true}},
		orphanedWorktreesByRoot: map[string][]model.ProjectSummary{root: {{
			Path: path, Name: "Unclaimed", WorktreeRootPath: root, WorktreeKind: model.WorktreeKindLinked, PresentOnDisk: true, Forgotten: true,
		}}},
		visibility: visibilityAllFolders, sortMode: sortByAttention,
	}
	m.rebuildProjectList(path)
	rendered := ansi.Strip(m.renderProjectList(180, 12))
	if !strings.Contains(rendered, "unowned") || !strings.Contains(rendered, "Unknown") {
		t.Fatalf("unclaimed list lacks ownership and activity labels: %s", rendered)
	}
	project := m.orphanedWorktreesByRoot[root][0]
	if m.projectDetailLastActivityText(project) != "unknown" || ansi.Strip(m.projectDetailLastActivityRenderedText(project)) != "unknown" {
		t.Fatal("unclaimed detail claimed activity never occurred")
	}
}
