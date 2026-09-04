package service

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lcroom/internal/appfs"
	"lcroom/internal/config"
	"lcroom/internal/events"
	"lcroom/internal/model"
	"lcroom/internal/scanner"
	"lcroom/internal/store"
)

func TestInspectOrphanedWorktreeFindsRetainedArchivedAgentTask(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dataDir := t.TempDir()
	repositoryParent := t.TempDir()
	repositoryPath := filepath.Join(repositoryParent, "repo")
	initGitRepo(t, repositoryPath)

	internalRoot, err := appfs.EnsureInternalWorkspaceRoot(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	workspacePath := filepath.Join(internalRoot, "lcroom-agent-task-retained")
	branchName := "asset/retained-workspace"
	runGit(t, repositoryPath, "git", "worktree", "add", "-b", branchName, workspacePath)

	cfg := config.Default()
	cfg.DataDir = dataDir
	cfg.DBPath = filepath.Join(dataDir, "little-control-room.sqlite")
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc := New(cfg, st, events.NewBus(), nil)
	if _, err := svc.CreateOrAttachProject(ctx, CreateOrAttachProjectRequest{
		ParentPath: repositoryParent,
		Name:       filepath.Base(repositoryPath),
	}); err != nil {
		t.Fatalf("track repository root: %v", err)
	}
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:                 workspacePath,
		Name:                 filepath.Base(workspacePath),
		Status:               model.StatusIdle,
		PresentOnDisk:        true,
		Forgotten:            true,
		InScope:              false,
		WorktreeRootPath:     repositoryPath,
		WorktreeKind:         model.WorktreeKindLinked,
		WorktreeParentBranch: "master",
		RepoBranch:           branchName,
		UpdatedAt:            time.Now(),
	}); err != nil {
		t.Fatalf("seed retained worktree project state: %v", err)
	}
	registeredInspection, err := svc.InspectOrphanedWorktree(ctx, workspacePath)
	if err != nil {
		t.Fatalf("InspectOrphanedWorktree(registered worktree) error = %v", err)
	}
	if registeredInspection.Resolution != OrphanedWorktreeResolutionRemoveRegistered || registeredInspection.BranchName != branchName {
		t.Fatalf("registered worktree inspection = %#v", registeredInspection)
	}
	task, err := svc.CreateAgentTask(ctx, model.CreateAgentTaskInput{
		ID:            "agt_retained_workspace",
		Title:         "Retained workspace task",
		Kind:          model.AgentTaskKindAgent,
		WorkspacePath: workspacePath,
	})
	if err != nil {
		t.Fatal(err)
	}

	activeInspection, err := svc.InspectOrphanedWorktree(ctx, workspacePath)
	if err != nil {
		t.Fatalf("InspectOrphanedWorktree(active task) error = %v", err)
	}
	if activeInspection.Resolution != OrphanedWorktreeResolutionNone || activeInspection.AgentTask.ID != task.ID {
		t.Fatalf("active task inspection = %#v, want identified owner without delete action", activeInspection)
	}
	if !strings.Contains(activeInspection.Reason, "still belongs") {
		t.Fatalf("active task inspection reason = %q", activeInspection.Reason)
	}
	if err := svc.DeleteArchivedAgentTaskNow(ctx, task.ID, workspacePath, true); err == nil || !strings.Contains(err.Error(), "no longer in Trash") {
		t.Fatalf("DeleteArchivedAgentTaskNow(active task) error = %v", err)
	}
	if _, err := os.Stat(workspacePath); err != nil {
		t.Fatalf("active task deletion refusal changed workspace: %v", err)
	}

	archived, err := svc.ArchiveAgentTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	inspection, err := svc.InspectOrphanedWorktree(ctx, workspacePath)
	if err != nil {
		t.Fatalf("InspectOrphanedWorktree(archived task) error = %v", err)
	}
	if inspection.Resolution != OrphanedWorktreeResolutionDeleteArchivedTaskNow {
		t.Fatalf("archived task resolution = %q, want %q", inspection.Resolution, OrphanedWorktreeResolutionDeleteArchivedTaskNow)
	}
	if inspection.AgentTask.ID != task.ID || inspection.AgentTask.Status != model.AgentTaskStatusArchived || inspection.AgentTask.ExpiresAt != archived.ExpiresAt {
		t.Fatalf("archived task inspection owner = %#v, want %#v", inspection.AgentTask, archived)
	}
	if inspection.BranchName != branchName || inspection.Dirty {
		t.Fatalf("archived task Git inspection = branch %q dirty=%v", inspection.BranchName, inspection.Dirty)
	}
	if _, err := os.Stat(workspacePath); err != nil {
		t.Fatalf("read-only inspection changed workspace: %v", err)
	}
	if _, err := st.GetAgentTask(ctx, task.ID); err != nil {
		t.Fatalf("read-only inspection changed task record: %v", err)
	}
	if err := svc.DeleteArchivedAgentTaskNow(ctx, task.ID, workspacePath+"-different", true); err == nil || !strings.Contains(err.Error(), "different workspace") {
		t.Fatalf("DeleteArchivedAgentTaskNow(workspace mismatch) error = %v", err)
	}
	if _, err := os.Stat(workspacePath); err != nil {
		t.Fatalf("workspace mismatch refusal changed workspace: %v", err)
	}

	if err := svc.DeleteArchivedAgentTaskNow(ctx, task.ID, workspacePath, false); err != nil {
		t.Fatalf("DeleteArchivedAgentTaskNow() error = %v", err)
	}
	if _, err := os.Stat(workspacePath); !os.IsNotExist(err) {
		t.Fatalf("retained task workspace still exists: %v", err)
	}
	if _, err := st.GetAgentTask(ctx, task.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("archived task lookup error = %v, want sql.ErrNoRows", err)
	}
	worktrees, err := scanner.ListGitWorktrees(ctx, repositoryPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, worktree := range worktrees {
		if samePath(worktree.Path, workspacePath) {
			t.Fatalf("deleted task workspace is still registered: %#v", worktrees)
		}
	}
	if _, err := gitCommitHash(ctx, repositoryPath, "refs/heads/"+branchName); err != nil {
		t.Fatalf("DeleteArchivedAgentTaskNow() deleted the preserved branch: %v", err)
	}
}
