package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lcroom/internal/codexapp"
	"lcroom/internal/model"
	"lcroom/internal/projectrun"
)

func TestAgentTaskWorkspacePresenceRefreshClearsStaleGitState(t *testing.T) {
	svc, _, root := repositoryTestFixture(t)
	task, err := svc.CreateAgentTask(t.Context(), model.CreateAgentTaskInput{Title: "workspace presence", OriginProjectPath: root, OriginSessionKey: "caller"})
	if err != nil {
		t.Fatal(err)
	}
	if task.WorkspaceGit.Missing || task.WorkspaceGit.Branch == "" {
		t.Fatalf("existing workspace unavailable: %+v", task.WorkspaceGit)
	}
	if err := os.RemoveAll(task.WorkspacePath); err != nil {
		t.Fatal(err)
	}
	task = svc.refreshAgentTaskWorkspace(t.Context(), task)
	if !task.WorkspaceGit.Missing || task.WorkspaceGit.Error == "" || task.WorkspaceGit.Branch != "" {
		t.Fatalf("missing workspace retained stale state: %+v", task.WorkspaceGit)
	}
	if err := os.Mkdir(task.WorkspacePath, 0700); err != nil {
		t.Fatal(err)
	}
	task = svc.refreshAgentTaskWorkspace(t.Context(), task)
	if task.WorkspaceGit.Missing || task.WorkspaceGit.Error != "Isolated workspace is missing its Git metadata" {
		t.Fatalf("restored directory retained missing state: %+v", task.WorkspaceGit)
	}
}

func TestSessionAgentTasksIsolateNestedWorkAndPreserveCaller(t *testing.T) {
	svc, _, root := repositoryTestFixture(t)
	if err := os.WriteFile(filepath.Join(root, "catalog.txt"), []byte("parent edits"), 0600); err != nil {
		t.Fatal(err)
	}
	svc.ConfigureAgentTaskRepositoryHost(func() []codexapp.Snapshot {
		return []codexapp.Snapshot{{ProjectPath: root, Busy: true}}
	}, func() []projectrun.Snapshot { return nil })
	task, err := svc.CreateAgentTask(t.Context(), model.CreateAgentTaskInput{Title: "Isolated worker", OriginProjectPath: root, OriginSessionKey: "parent", Repository: model.AgentTaskRepository{Write: true}})
	if err != nil {
		t.Fatal(err)
	}
	if !task.Repository.Isolated || canonicalRepositoryPath(task.Repository.Root) != canonicalRepositoryPath(task.WorkspacePath) || task.WorkspaceGit.Branch == "" {
		t.Fatalf("worker has no separate checkout: %+v", task)
	}
	startRepositoryTurn(t, svc, task, "worker") // Busy, dirty caller is independent.
	if _, err := os.Stat(filepath.Join(task.WorkspacePath, "catalog.txt")); !os.IsNotExist(err) {
		t.Fatal("uncommitted caller files were copied")
	}
	repositoryTestGit(t, task.WorkspacePath, "switch", "-c", "worker/changed")
	if err := os.WriteFile(filepath.Join(task.WorkspacePath, "worker.txt"), []byte("worker edits"), 0600); err != nil {
		t.Fatal(err)
	}
	if unlock, err := svc.BeginRepositoryProcess(task.WorkspacePath, root); err == nil {
		unlock()
		t.Fatal("worker process admitted into caller checkout")
	}
	child, err := svc.CreateAgentTask(t.Context(), model.CreateAgentTaskInput{Title: "Nested worker", ParentTaskID: task.ID, OriginProjectPath: root, OriginWorktreePath: task.WorkspacePath, OriginSessionKey: "worker"})
	if err != nil {
		t.Fatal(err)
	}
	if !child.Repository.Isolated || child.WorkspacePath == task.WorkspacePath || child.WorkspaceGit.Branch == task.WorkspaceGit.Branch {
		t.Fatal("nested worker shared a checkout or branch")
	}
	branch, err := repositoryGit(t.Context(), root, "branch", "--show-current")
	if err != nil || strings.TrimSpace(string(branch)) != "fixture" {
		t.Fatalf("caller branch changed: %s %v", branch, err)
	}
	data, err := os.ReadFile(filepath.Join(root, "catalog.txt"))
	if err != nil || string(data) != "parent edits" {
		t.Fatal("caller files changed")
	}
	refreshed, err := svc.GetAgentTask(t.Context(), task.ID)
	if err != nil || refreshed.WorkspaceGit.Branch != "worker/changed" || !refreshed.WorkspaceGit.Dirty || refreshed.Repository.InitialBranch == refreshed.WorkspaceGit.Branch {
		t.Fatalf("branch/dirty state not visible: %+v %v", refreshed, err)
	}
	if _, err := svc.CreateAgentTask(t.Context(), model.CreateAgentTaskInput{Title: "Unsafe worker", OriginProjectPath: root, WorkspacePath: root}); err == nil {
		t.Fatal("accepted shared caller checkout")
	}
	// Retention removes both files and Git's worktree registration.
	if _, err := svc.ArchiveAgentTask(t.Context(), child.ID); err != nil {
		t.Fatal(err)
	}
	if purged, err := svc.PurgeExpiredAgentTasks(t.Context(), time.Now().Add(8*24*time.Hour)); err != nil || purged != 1 {
		t.Fatalf("purge isolated worktree: %d %v", purged, err)
	}
	registered, err := repositoryGit(t.Context(), root, "worktree", "list", "--porcelain")
	if err != nil || strings.Contains(string(registered), child.WorkspacePath) {
		t.Fatalf("task worktree registration leaked: %s %v", registered, err)
	}
	svc.cfg.DataDir = filepath.Join(root, ".lcr-data")
	if _, err := svc.CreateAgentTask(t.Context(), model.CreateAgentTaskInput{Title: "Unsafe data location", OriginProjectPath: root, OriginSessionKey: "caller"}); err == nil {
		t.Fatal("allocated a task workspace inside the caller's checkout")
	}
	if _, err := os.Stat(svc.cfg.DataDir); !os.IsNotExist(err) {
		t.Fatal("rejected task created files in the caller's checkout")
	}
}
