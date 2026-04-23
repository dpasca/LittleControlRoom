package service

import (
	"context"
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
