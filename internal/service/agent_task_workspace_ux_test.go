package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lcroom/internal/model"
)

func TestTaskOwnershipSurvivesBrokenGitAndTrashRetention(t *testing.T) {
	svc, task, root := isolatedWorkspaceUXFixture(t)
	if err := os.Remove(filepath.Join(task.WorkspacePath, ".git")); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(task.WorkspacePath, "keep.txt"), "retained work", 0600)
	if err := svc.Store().UpsertProjectState(t.Context(), model.ProjectState{
		Path: task.WorkspacePath, Name: "broken checkout", PresentOnDisk: true,
		Forgotten: true, WorktreeRootPath: root, WorktreeKind: model.WorktreeKindLinked,
	}); err != nil {
		t.Fatal(err)
	}
	for _, status := range []model.AgentTaskStatus{model.AgentTaskStatusCompleted, model.AgentTaskStatusArchived} {
		if _, err := svc.Store().UpdateAgentTask(t.Context(), model.UpdateAgentTaskInput{ID: task.ID, Status: &status}); err != nil {
			t.Fatal(err)
		}
		unclaimed, err := svc.ListOrphanedWorktreeDirectories(t.Context())
		if err != nil || len(unclaimed) != 0 {
			t.Fatalf("%s task duplicated as unclaimed: %+v, %v", status, unclaimed, err)
		}
	}
	current, err := svc.GetAgentTask(t.Context(), task.ID)
	if err != nil || current.WorkspaceGit.State != model.AgentTaskWorkspaceMetadataMissing || current.WorkspaceGit.CheckedAt.IsZero() {
		t.Fatalf("missing Git metadata not identified: %+v, %v", current.WorkspaceGit, err)
	}
	if data, err := os.ReadFile(filepath.Join(task.WorkspacePath, "keep.txt")); err != nil || string(data) != "retained work" {
		t.Fatalf("read-only reconciliation changed files: %q, %v", data, err)
	}
}

func TestInspectTaskWithoutProjectRecordAndDeleteTrashRegistration(t *testing.T) {
	svc, task, root := isolatedWorkspaceUXFixture(t)
	inspection, err := svc.InspectOrphanedWorktree(t.Context(), task.WorkspacePath)
	if err != nil || inspection.AgentTask.ID != task.ID || !inspection.HaveCurrentStatus || inspection.Resolution != OrphanedWorktreeResolutionNone {
		t.Fatalf("task without project record could not be inspected: %+v, %v", inspection, err)
	}
	if _, err := svc.ArchiveAgentTask(t.Context(), task.ID); err != nil {
		t.Fatal(err)
	}
	inspection, err = svc.InspectOrphanedWorktree(t.Context(), task.WorkspacePath)
	if err != nil || inspection.Resolution != OrphanedWorktreeResolutionDeleteArchivedTaskNow || inspection.Registration != "Registered Git worktree" {
		t.Fatalf("Trash inspection = %+v, %v", inspection, err)
	}
	if err := svc.DeleteArchivedAgentTaskNow(t.Context(), task.ID, task.WorkspacePath, false); err != nil {
		t.Fatal(err)
	}
	registered, err := repositoryGit(t.Context(), root, "worktree", "list", "--porcelain")
	if err != nil || strings.Contains(string(registered), task.WorkspacePath) {
		t.Fatalf("deleted task left Git registration: %s, %v", registered, err)
	}
}

func TestFailedTrashCleanupKeepsTaskListsAvailable(t *testing.T) {
	svc, task, root := isolatedWorkspaceUXFixture(t)
	// A stale pointer makes Git removal fail without depending on OS permissions.
	writeTestFile(t, filepath.Join(task.WorkspacePath, ".git"), "gitdir: /no-such-git-directory\n", 0600)
	status, expired := model.AgentTaskStatusArchived, time.Now().Add(-time.Hour)
	if _, err := svc.Store().UpdateAgentTask(t.Context(), model.UpdateAgentTaskInput{ID: task.ID, Status: &status, ExpiresAt: &expired}); err != nil {
		t.Fatal(err)
	}
	live, err := svc.CreateAgentTask(t.Context(), model.CreateAgentTaskInput{Title: "Still active", OriginProjectPath: root})
	if err != nil {
		t.Fatal(err)
	}
	tasks, err := svc.ListOpenAgentTasks(t.Context(), 50)
	if err != nil {
		t.Fatal(err)
	}
	foundLive, foundFailed := false, false
	for _, current := range tasks {
		foundLive = foundLive || current.ID == live.ID
		foundFailed = foundFailed || (current.ID == task.ID && current.CleanupError != "")
	}
	if !foundLive || !foundFailed {
		t.Fatalf("cleanup failure hid live work or its owning task: %+v", tasks)
	}
	trash, err := svc.ListAgentTaskTrash(t.Context())
	if err != nil || len(trash) != 1 || trash[0].CleanupError == "" {
		t.Fatalf("Trash lost cleanup failure: %+v, %v", trash, err)
	}
}

func isolatedWorkspaceUXFixture(t *testing.T) (*Service, model.AgentTask, string) {
	t.Helper()
	svc, _, root := repositoryTestFixture(t)
	task, err := svc.CreateAgentTask(t.Context(), model.CreateAgentTaskInput{
		Title: "Isolated task", OriginProjectPath: root, OriginSessionKey: "ux-caller",
		Repository: model.AgentTaskRepository{Write: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	return svc, task, root
}
