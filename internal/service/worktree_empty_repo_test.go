package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lcroom/internal/config"
	"lcroom/internal/events"
	"lcroom/internal/scanner"
	"lcroom/internal/store"
)

func TestCreateTodoWorktreeFromNewlyCreatedEmptyGitRepo(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	parent := t.TempDir()
	svc := New(config.Default(), st, events.NewBus(), nil)

	created, err := svc.CreateOrAttachProject(ctx, CreateOrAttachProjectRequest{
		ParentPath:    parent,
		Name:          "social_manager",
		CreateGitRepo: true,
	})
	if err != nil {
		t.Fatalf("CreateOrAttachProject() error = %v", err)
	}
	if !created.GitRepoCreated {
		t.Fatalf("expected git repo to be created")
	}

	item, err := svc.AddTodo(ctx, created.ProjectPath, "Set up social media org docs")
	if err != nil {
		t.Fatalf("AddTodo() error = %v", err)
	}

	result, err := svc.CreateTodoWorktree(ctx, CreateTodoWorktreeRequest{
		ProjectPath:    created.ProjectPath,
		TodoID:         item.ID,
		BranchName:     "docs/social-media-org",
		WorktreeSuffix: "social-media-org",
	})
	if err != nil {
		t.Fatalf("CreateTodoWorktree() error = %v", err)
	}

	expectedPath := filepath.Join(parent, "social_manager--social-media-org")
	if result.WorktreePath != expectedPath {
		t.Fatalf("worktree path = %q, want %q", result.WorktreePath, expectedPath)
	}
	if result.BranchName != "docs/social-media-org" {
		t.Fatalf("branch = %q, want %q", result.BranchName, "docs/social-media-org")
	}

	rootStatus, err := scanner.ReadGitRepoStatus(ctx, created.ProjectPath)
	if err != nil {
		t.Fatalf("read git repo status for root: %v", err)
	}
	if strings.TrimSpace(rootStatus.Branch) == "" || strings.TrimSpace(rootStatus.Branch) == "(detached)" {
		t.Fatalf("root branch = %q, want unborn default branch", rootStatus.Branch)
	}
	if result.ParentBranch != strings.TrimSpace(rootStatus.Branch) {
		t.Fatalf("parent branch = %q, want %q", result.ParentBranch, strings.TrimSpace(rootStatus.Branch))
	}

	worktreeStatus, err := scanner.ReadGitRepoStatus(ctx, result.WorktreePath)
	if err != nil {
		t.Fatalf("read git repo status for worktree: %v", err)
	}
	if worktreeStatus.Branch != "docs/social-media-org" {
		t.Fatalf("worktree branch = %q, want %q", worktreeStatus.Branch, "docs/social-media-org")
	}
}

func TestMergeTodoWorktreeBackFromEmptyGitRepoAfterIndependentRootCommit(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	parent := t.TempDir()
	svc := New(config.Default(), st, events.NewBus(), nil)

	created, err := svc.CreateOrAttachProject(ctx, CreateOrAttachProjectRequest{
		ParentPath:    parent,
		Name:          "social_manager",
		CreateGitRepo: true,
	})
	if err != nil {
		t.Fatalf("CreateOrAttachProject() error = %v", err)
	}

	item, err := svc.AddTodo(ctx, created.ProjectPath, "Set up social media org docs")
	if err != nil {
		t.Fatalf("AddTodo() error = %v", err)
	}

	result, err := svc.CreateTodoWorktree(ctx, CreateTodoWorktreeRequest{
		ProjectPath:    created.ProjectPath,
		TodoID:         item.ID,
		BranchName:     "docs/social-media-org",
		WorktreeSuffix: "social-media-org",
	})
	if err != nil {
		t.Fatalf("CreateTodoWorktree() error = %v", err)
	}

	worktreeFile := filepath.Join(result.WorktreePath, "docs.md")
	if err := os.WriteFile(worktreeFile, []byte("docs from worktree\n"), 0o644); err != nil {
		t.Fatalf("write worktree docs: %v", err)
	}
	runGit(t, result.WorktreePath, "git", "add", "docs.md")
	runGit(t, result.WorktreePath, "git", "commit", "-m", "add worktree docs")

	rootFile := filepath.Join(created.ProjectPath, "README.md")
	if err := os.WriteFile(rootFile, []byte("root bootstrap\n"), 0o644); err != nil {
		t.Fatalf("write root README: %v", err)
	}
	runGit(t, created.ProjectPath, "git", "add", "README.md")
	runGit(t, created.ProjectPath, "git", "commit", "-m", "bootstrap root")

	mergeResult, err := svc.MergeWorktreeBack(ctx, result.WorktreePath)
	if err != nil {
		t.Fatalf("MergeWorktreeBack() error = %v", err)
	}
	if mergeResult.RootProjectPath != created.ProjectPath {
		t.Fatalf("merge root path = %q, want %q", mergeResult.RootProjectPath, created.ProjectPath)
	}
	if mergeResult.SourceBranch != "docs/social-media-org" {
		t.Fatalf("merge source branch = %q, want %q", mergeResult.SourceBranch, "docs/social-media-org")
	}
	if strings.TrimSpace(mergeResult.TargetBranch) == "" {
		t.Fatalf("merge target branch should not be empty: %#v", mergeResult)
	}

	if got, err := os.ReadFile(filepath.Join(created.ProjectPath, "docs.md")); err != nil {
		t.Fatalf("read merged docs from root: %v", err)
	} else if strings.TrimSpace(string(got)) != "docs from worktree" {
		t.Fatalf("merged docs contents = %q, want worktree content", string(got))
	}

	if got, err := os.ReadFile(rootFile); err != nil {
		t.Fatalf("read root README after merge: %v", err)
	} else if strings.TrimSpace(string(got)) != "root bootstrap" {
		t.Fatalf("root README contents = %q, want root bootstrap content", string(got))
	}

	rootStatus, err := scanner.ReadGitRepoStatus(ctx, created.ProjectPath)
	if err != nil {
		t.Fatalf("read git repo status for root after merge: %v", err)
	}
	if rootStatus.Dirty {
		t.Fatalf("root repo should be clean after merge-back, got %#v", rootStatus)
	}
	if strings.TrimSpace(rootStatus.Branch) != strings.TrimSpace(result.ParentBranch) {
		t.Fatalf("root branch after merge-back = %q, want %q", rootStatus.Branch, result.ParentBranch)
	}
}
