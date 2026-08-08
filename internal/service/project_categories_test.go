package service

import (
	"context"
	"lcroom/internal/config"
	"lcroom/internal/events"
	"lcroom/internal/model"
	"lcroom/internal/store"
	"path/filepath"
	"testing"
	"time"
)

func TestMoveProjectToCategoryMovesRootWorktreeFamily(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st, svc, rootPath, worktreePaths := categoryWorktreeFamilyForTest(t, ctx, true)

	source, err := st.CreateProjectCategory(ctx, "Source")
	if err != nil {
		t.Fatalf("create source category: %v", err)
	}
	destination, err := st.CreateProjectCategory(ctx, "Destination")
	if err != nil {
		t.Fatalf("create destination category: %v", err)
	}
	paths := append([]string{rootPath}, worktreePaths...)
	if err := st.SetProjectsCategory(ctx, paths, source.ID); err != nil {
		t.Fatalf("seed repository family category: %v", err)
	}

	gotCategory, err := svc.MoveProjectToCategory(ctx, rootPath, destination.Name)
	if err != nil {
		t.Fatalf("MoveProjectToCategory() error = %v", err)
	}
	if gotCategory.ID != destination.ID {
		t.Fatalf("MoveProjectToCategory() category = %#v, want %#v", gotCategory, destination)
	}
	assertProjectCategoryAndArchiveForTest(t, ctx, st, paths, destination.ID, false)

	if _, err := svc.MoveProjectToCategory(ctx, rootPath, ""); err != nil {
		t.Fatalf("MoveProjectToCategory(Main) error = %v", err)
	}
	assertProjectCategoryAndArchiveForTest(t, ctx, st, paths, "", false)
}

func TestMoveProjectToCategoryKeepsDirectLinkedWorktreeMoveTargeted(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st, svc, rootPath, worktreePaths := categoryWorktreeFamilyForTest(t, ctx, false)

	source, err := st.CreateProjectCategory(ctx, "Source")
	if err != nil {
		t.Fatalf("create source category: %v", err)
	}
	destination, err := st.CreateProjectCategory(ctx, "Destination")
	if err != nil {
		t.Fatalf("create destination category: %v", err)
	}
	paths := append([]string{rootPath}, worktreePaths...)
	if err := st.SetProjectsCategory(ctx, paths, source.ID); err != nil {
		t.Fatalf("seed repository family category: %v", err)
	}

	if _, err := svc.MoveProjectToCategory(ctx, worktreePaths[0], destination.Name); err != nil {
		t.Fatalf("MoveProjectToCategory(linked worktree) error = %v", err)
	}
	assertProjectCategoryAndArchiveForTest(t, ctx, st, []string{worktreePaths[0]}, destination.ID, false)
	assertProjectCategoryAndArchiveForTest(t, ctx, st, []string{rootPath, worktreePaths[1]}, source.ID, false)
}

func TestMoveResourcesToCategoryMovesRootWorktreeFamily(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st, svc, rootPath, worktreePaths := categoryWorktreeFamilyForTest(t, ctx, false)

	destination, err := st.CreateProjectCategory(ctx, "Destination")
	if err != nil {
		t.Fatalf("create destination category: %v", err)
	}
	category, moved, err := svc.MoveResourcesToCategory(ctx, []model.CategoryResourceRef{
		{Kind: model.CategoryResourceProject, ID: rootPath},
	}, destination.Name)
	if err != nil {
		t.Fatalf("MoveResourcesToCategory() error = %v", err)
	}
	if moved != 1 || category.ID != destination.ID {
		t.Fatalf("MoveResourcesToCategory() = (%#v, %d), want destination and one selected item", category, moved)
	}
	assertProjectCategoryAndArchiveForTest(t, ctx, st, append([]string{rootPath}, worktreePaths...), destination.ID, false)
}

func TestExplicitProjectCategoryAssignmentMovesFamilyWithoutUnarchiving(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st, svc, rootPath, worktreePaths := categoryWorktreeFamilyForTest(t, ctx, true)

	category, err := st.CreateProjectCategory(ctx, "Destination")
	if err != nil {
		t.Fatalf("create destination category: %v", err)
	}
	if err := svc.assignProjectCategoryIfRequested(ctx, rootPath, category.ID, true); err != nil {
		t.Fatalf("assignProjectCategoryIfRequested() error = %v", err)
	}
	assertProjectCategoryAndArchiveForTest(t, ctx, st, append([]string{rootPath}, worktreePaths...), category.ID, true)
}

func categoryWorktreeFamilyForTest(
	t *testing.T,
	ctx context.Context,
	archived bool,
) (*store.Store, *Service, string, []string) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	rootPath := filepath.Join(t.TempDir(), "PermanentUnderclass")
	worktreePaths := []string{
		rootPath + "--feature-one",
		rootPath + "--feature-two",
	}
	now := time.Now()
	states := []model.ProjectState{
		{
			Path:             rootPath,
			Name:             "PermanentUnderclass",
			Status:           model.StatusIdle,
			PresentOnDisk:    true,
			WorktreeRootPath: rootPath,
			WorktreeKind:     model.WorktreeKindMain,
			InScope:          true,
			Archived:         archived,
			UpdatedAt:        now,
		},
	}
	for _, path := range worktreePaths {
		states = append(states, model.ProjectState{
			Path:             path,
			Name:             filepath.Base(path),
			Status:           model.StatusIdle,
			PresentOnDisk:    true,
			WorktreeRootPath: rootPath,
			WorktreeKind:     model.WorktreeKindLinked,
			InScope:          true,
			Archived:         archived,
			UpdatedAt:        now,
		})
	}
	for _, state := range states {
		if err := st.UpsertProjectState(ctx, state); err != nil {
			t.Fatalf("seed project %s: %v", state.Path, err)
		}
	}
	return st, New(config.Default(), st, events.NewBus(), nil), rootPath, worktreePaths
}

func assertProjectCategoryAndArchiveForTest(
	t *testing.T,
	ctx context.Context,
	st *store.Store,
	paths []string,
	categoryID string,
	archived bool,
) {
	t.Helper()
	for _, path := range paths {
		summary, err := st.GetProjectSummary(ctx, path, true)
		if err != nil {
			t.Fatalf("load project %s: %v", path, err)
		}
		if summary.CategoryID != categoryID || summary.Archived != archived {
			t.Errorf("project %s category/archive = %q/%t, want %q/%t", path, summary.CategoryID, summary.Archived, categoryID, archived)
		}
	}
}
