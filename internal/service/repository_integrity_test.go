package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lcroom/internal/config"
	"lcroom/internal/events"
	"lcroom/internal/model"
	"lcroom/internal/scanner"
	"lcroom/internal/store"
)

func TestRepositoryIntegrityDetectsAndRepairsDisplacedRoot(t *testing.T) {
	ctx := context.Background()
	parent := t.TempDir()
	rootPath := filepath.Join(parent, "repo")
	initGitRepo(t, rootPath)
	rootStatus, err := scanner.ReadGitRepoStatus(ctx, rootPath)
	if err != nil {
		t.Fatalf("read initial root status: %v", err)
	}
	expectedBranch := strings.TrimSpace(rootStatus.Branch)
	if expectedBranch == "" {
		t.Fatal("initial branch is empty")
	}

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	svc := New(config.Default(), st, events.NewBus(), nil)
	if _, err := svc.CreateOrAttachProject(ctx, CreateOrAttachProjectRequest{ParentPath: parent, Name: "repo"}); err != nil {
		t.Fatalf("track root: %v", err)
	}

	todo, err := svc.AddTodo(ctx, rootPath, "Create a helper worktree that records the root branch")
	if err != nil {
		t.Fatalf("add todo: %v", err)
	}
	if _, err := svc.CreateTodoWorktree(ctx, CreateTodoWorktreeRequest{
		ProjectPath:    rootPath,
		TodoID:         todo.ID,
		BranchName:     "test/helper-worktree",
		WorktreeSuffix: "helper-worktree",
	}); err != nil {
		t.Fatalf("create helper worktree: %v", err)
	}

	displacedBranch := "hud/energy-declutter"
	runGit(t, rootPath, "git", "switch", "-c", displacedBranch)
	if err := svc.RefreshProjectStatus(ctx, rootPath); err != nil {
		t.Fatalf("refresh displaced root: %v", err)
	}

	states, err := svc.RepositoryIntegrityStates(ctx)
	if err != nil {
		t.Fatalf("RepositoryIntegrityStates() error = %v", err)
	}
	state, ok := states[rootPath]
	if !ok {
		t.Fatalf("states = %#v, want root %q", states, rootPath)
	}
	if !state.Displaced || !state.NeedsAttention() {
		t.Fatalf("state = %#v, want unacknowledged displaced root", state)
	}
	if state.ExpectedBranch != expectedBranch || state.ActualBranch != displacedBranch {
		t.Fatalf("branches = expected %q actual %q, want %q and %q", state.ExpectedBranch, state.ActualBranch, expectedBranch, displacedBranch)
	}
	if !state.CanRepair {
		t.Fatalf("CanRepair = false: %s", state.RepairBlockReason)
	}

	result, err := svc.RepairRepositoryRoot(ctx, RepositoryIntegrityRepairRequest{RootPath: rootPath})
	if err != nil {
		t.Fatalf("RepairRepositoryRoot() error = %v", err)
	}
	if result.MovedBranch != displacedBranch || result.RestoredBranch != expectedBranch {
		t.Fatalf("repair result = %#v", result)
	}
	rootStatus, err = scanner.ReadGitRepoStatus(ctx, rootPath)
	if err != nil {
		t.Fatalf("read repaired root: %v", err)
	}
	if rootStatus.Branch != expectedBranch {
		t.Fatalf("repaired root branch = %q, want %q", rootStatus.Branch, expectedBranch)
	}
	worktreeStatus, err := scanner.ReadGitRepoStatus(ctx, result.WorktreePath)
	if err != nil {
		t.Fatalf("read repair worktree: %v", err)
	}
	if worktreeStatus.Branch != displacedBranch {
		t.Fatalf("repair worktree branch = %q, want %q", worktreeStatus.Branch, displacedBranch)
	}
	tracked, err := st.GetProjectSummary(ctx, result.WorktreePath, true)
	if err != nil {
		t.Fatalf("load tracked repair worktree: %v", err)
	}
	if tracked.WorktreeKind != model.WorktreeKindLinked || tracked.WorktreeParentBranch != expectedBranch {
		t.Fatalf("tracked repair worktree = %#v, want linked with parent %q", tracked, expectedBranch)
	}
}

func TestRepositoryIntegrityRepairBlocksDirtyRoot(t *testing.T) {
	ctx := context.Background()
	parent := t.TempDir()
	rootPath := filepath.Join(parent, "repo")
	initGitRepo(t, rootPath)
	status, err := scanner.ReadGitRepoStatus(ctx, rootPath)
	if err != nil {
		t.Fatalf("read root status: %v", err)
	}
	expectedBranch := status.Branch

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	svc := New(config.Default(), st, events.NewBus(), nil)
	if _, err := svc.CreateOrAttachProject(ctx, CreateOrAttachProjectRequest{ParentPath: parent, Name: "repo"}); err != nil {
		t.Fatalf("track root: %v", err)
	}
	if err := svc.SetRepositoryRootExpectedBranch(ctx, rootPath, expectedBranch); err != nil {
		t.Fatalf("set policy: %v", err)
	}
	runGit(t, rootPath, "git", "switch", "-c", "feature/dirty-root")
	if err := os.WriteFile(filepath.Join(rootPath, "DIRTY.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatalf("write dirty file: %v", err)
	}
	if err := svc.RefreshProjectStatus(ctx, rootPath); err != nil {
		t.Fatalf("refresh dirty root: %v", err)
	}

	state, err := svc.RepositoryIntegrityStateForProject(ctx, rootPath)
	if err != nil {
		t.Fatalf("RepositoryIntegrityStateForProject() error = %v", err)
	}
	if state.CanRepair || !strings.Contains(state.RepairBlockReason, "uncommitted") {
		t.Fatalf("repair state = %#v, want uncommitted-change blocker", state)
	}
}
