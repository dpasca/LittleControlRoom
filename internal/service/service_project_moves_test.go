package service

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"lcroom/internal/model"
	"lcroom/internal/scanner"
	"lcroom/internal/store"
)

func TestDetectProjectMovesSkipsSiblingLinkedWorktrees(t *testing.T) {
	t.Parallel()

	svc := &Service{}
	root := "/repos/family"
	oldPath := "/repos/family--old-topic"
	newPath := "/repos/family--new-topic"
	hashes := []string{"aaaa111", "bbbb222", "cccc333"}

	oldMap := map[string]model.ProjectSummary{
		oldPath: {
			Path:             oldPath,
			InScope:          true,
			WorktreeKind:     model.WorktreeKindLinked,
			WorktreeRootPath: root,
		},
	}
	discoveredSet := map[string]struct{}{newPath: {}}
	cached := map[string]model.ProjectGitFingerprint{
		oldPath: {ProjectPath: oldPath, RecentHashes: hashes},
	}
	current := map[string]scanner.GitFingerprint{
		newPath: {RecentHashes: hashes},
	}

	moves := svc.detectProjectMoves(oldMap, discoveredSet, cached, current, map[string]map[string]struct{}{
		root: {newPath: {}},
	})
	if len(moves) != 0 {
		t.Fatalf("expected no moves between sibling linked worktrees, got %#v", moves)
	}
}

func TestDetectProjectMovesStillDetectsPlainRepoRename(t *testing.T) {
	t.Parallel()

	svc := &Service{}
	oldPath := "/repos/old-location/project"
	newPath := "/repos/new-location/project"
	hashes := []string{"dddd444", "eeee555"}

	oldMap := map[string]model.ProjectSummary{
		oldPath: {Path: oldPath, InScope: true},
	}
	discoveredSet := map[string]struct{}{newPath: {}}
	cached := map[string]model.ProjectGitFingerprint{
		oldPath: {ProjectPath: oldPath, RecentHashes: hashes},
	}
	current := map[string]scanner.GitFingerprint{
		newPath: {RecentHashes: hashes},
	}

	moves := svc.detectProjectMoves(oldMap, discoveredSet, cached, current, nil)
	if len(moves) != 1 {
		t.Fatalf("expected exactly one move, got %#v", moves)
	}
	if moves[0].OldPath != oldPath || moves[0].NewPath != newPath {
		t.Fatalf("unexpected move: %#v", moves[0])
	}
	if moves[0].Score != len(hashes) {
		t.Fatalf("score = %d, want %d", moves[0].Score, len(hashes))
	}
}

func TestDetectProjectMovesStillDetectsLinkedRootMove(t *testing.T) {
	t.Parallel()

	svc := &Service{}
	oldRoot := "/repos/old-family"
	oldPath := "/repos/old-family--topic"
	newRoot := "/repos/new-family"
	newPath := "/repos/new-family--topic"
	hashes := []string{"ffff666", "aaaa777"}

	oldMap := map[string]model.ProjectSummary{
		oldPath: {
			Path:             oldPath,
			InScope:          true,
			WorktreeKind:     model.WorktreeKindLinked,
			WorktreeRootPath: oldRoot,
		},
	}
	discoveredSet := map[string]struct{}{newPath: {}}
	cached := map[string]model.ProjectGitFingerprint{
		oldPath: {ProjectPath: oldPath, RecentHashes: hashes},
	}
	current := map[string]scanner.GitFingerprint{
		newPath: {RecentHashes: hashes},
	}

	moves := svc.detectProjectMoves(oldMap, discoveredSet, cached, current, map[string]map[string]struct{}{
		newRoot: {newPath: {}},
	})
	if len(moves) != 1 {
		t.Fatalf("expected the repo root rename to remain a move, got %#v", moves)
	}
	if moves[0].OldPath != oldPath || moves[0].NewPath != newPath {
		t.Fatalf("unexpected move: %#v", moves[0])
	}
}

func TestPersistProjectMoveSkipsWhenTargetExists(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	now := time.Now().UTC().Truncate(time.Second)
	oldPath := "/tmp/move-source"
	targetPath := "/tmp/move-target"

	for _, path := range []string{oldPath, targetPath} {
		if err := st.UpsertProjectState(ctx, model.ProjectState{
			Path:      path,
			Name:      filepath.Base(path),
			Status:    model.StatusIdle,
			InScope:   true,
			UpdatedAt: now,
		}); err != nil {
			t.Fatalf("upsert project %s: %v", path, err)
		}
	}

	svc := &Service{store: st}
	err = svc.persistProjectMove(ctx, detectedProjectMove{OldPath: oldPath, NewPath: targetPath, Score: 3}, now)
	if !errors.Is(err, store.ErrProjectPathExists) {
		t.Fatalf("persistProjectMove() error = %v, want ErrProjectPathExists", err)
	}

	// A refused move must leave both rows untouched.
	if _, err := st.GetProjectSummary(ctx, oldPath, false); err != nil {
		t.Fatalf("old project should remain after refused move: %v", err)
	}
	if _, err := st.GetProjectSummary(ctx, targetPath, false); err != nil {
		t.Fatalf("target project should remain after refused move: %v", err)
	}
	aliases, err := st.GetPathAliases(ctx)
	if err != nil {
		t.Fatalf("get path aliases: %v", err)
	}
	if len(aliases) != 0 {
		t.Fatalf("expected no aliases after refused move, got %#v", aliases)
	}
}

func TestPersistProjectMoveAppliesMoveAndAlias(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	now := time.Now().UTC().Truncate(time.Second)
	oldPath := "/tmp/mv-old"
	newPath := "/tmp/mv-new"

	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:      oldPath,
		Name:      "mv-old",
		Status:    model.StatusIdle,
		InScope:   true,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("upsert project: %v", err)
	}

	svc := &Service{store: st}
	if err := svc.persistProjectMove(ctx, detectedProjectMove{OldPath: oldPath, NewPath: newPath, Score: 2}, now); err != nil {
		t.Fatalf("persistProjectMove() error = %v", err)
	}

	if _, err := st.GetProjectSummary(ctx, oldPath, false); err == nil {
		t.Fatalf("expected old project row to be gone after move")
	}
	if _, err := st.GetProjectSummary(ctx, newPath, false); err != nil {
		t.Fatalf("expected moved project row: %v", err)
	}
	aliases, err := st.GetPathAliases(ctx)
	if err != nil {
		t.Fatalf("get path aliases: %v", err)
	}
	alias, ok := aliases[oldPath]
	if !ok || alias.NewPath != newPath || alias.Reason != "git_recent_hash_match" {
		t.Fatalf("expected alias %s -> %s with git_recent_hash_match, got %#v", oldPath, newPath, aliases)
	}
}
