package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lcroom/internal/config"
	"lcroom/internal/events"
	"lcroom/internal/gitlock"
	"lcroom/internal/scanner"
	"lcroom/internal/store"
)

func TestRootMetadataRepairPreservesIndexLockRecoveryType(t *testing.T) {
	root := t.TempDir()
	initGitRepo(t, root)
	lockPath := filepath.Join(root, ".git", "index.lock")
	if err := os.WriteFile(lockPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	statusErr := errors.New("submodule worktree metadata is stale")
	svc := &Service{gitRepoStatusReader: func(context.Context, string) (scanner.GitRepoStatus, error) {
		return scanner.GitRepoStatus{}, statusErr
	}}
	_, err := svc.readRootRepoStatusWithSubmoduleRepair(context.Background(), root)
	var lockErr gitlock.IndexLockError
	if !errors.Is(err, statusErr) || !errors.As(err, &lockErr) || lockErr.LockPath != lockPath {
		t.Fatalf("metadata repair lost status error or typed lock blocker: %v", err)
	}
}

func TestMergeBackScopesSubmoduleLocksToParticipatingCheckouts(t *testing.T) {
	t.Parallel()
	for _, sourceLocked := range []bool{false, true} {
		name := "unrelated worktree"
		if sourceLocked {
			name = "source worktree"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			root := t.TempDir()
			projectPath := filepath.Join(root, "repo")
			submodulePath := initGitRepoWithSubmodule(t, projectPath, filepath.Join(root, "assets-origin"), "asset-source")
			st, err := store.Open(filepath.Join(root, "state.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			svc := New(config.Default(), st, events.NewBus(), nil)
			if _, err := svc.CreateOrAttachProject(ctx, CreateOrAttachProjectRequest{ParentPath: root, Name: "repo"}); err != nil {
				t.Fatal(err)
			}
			result := createSuggestedTodoWorktreeForTest(t, ctx, svc, st, projectPath, "Scoped merge lock", "feat/scoped-lock", "scoped-lock")
			if err := os.WriteFile(filepath.Join(result.WorktreePath, "FEATURE.txt"), []byte("merge me\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			runGit(t, result.WorktreePath, "git", "add", "FEATURE.txt")
			runGit(t, result.WorktreePath, "git", "commit", "-m", "feature")
			lockedCheckout := filepath.Join(result.WorktreePath, "asset-source")
			if !sourceLocked {
				lockedCheckout = filepath.Join(root, "unrelated assets")
				runGit(t, submodulePath, "git", "worktree", "add", "--detach", lockedCheckout, "HEAD")
			}
			lock, err := gitlock.IndexLockPath(ctx, lockedCheckout)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(lock, []byte("owned by another operation"), 0o600); err != nil {
				t.Fatal(err)
			}
			_, mergeErr := svc.MergeWorktreeBack(ctx, result.WorktreePath)
			if sourceLocked {
				if mergeErr == nil || !strings.Contains(mergeErr.Error(), lock) || !strings.Contains(mergeErr.Error(), "preflight merge-back") {
					t.Fatalf("merge error = %v, want source lock preflight failure", mergeErr)
				}
				if _, err := os.Stat(filepath.Join(projectPath, "FEATURE.txt")); !os.IsNotExist(err) {
					t.Fatalf("root changed despite source lock: %v", err)
				}
			} else {
				if mergeErr != nil {
					t.Fatalf("unrelated lock blocked merge or post-merge sync: %v", mergeErr)
				}
				if _, err := os.Stat(filepath.Join(projectPath, "FEATURE.txt")); err != nil {
					t.Fatal(err)
				}
			}
			contents, err := os.ReadFile(lock)
			if err != nil || string(contents) != "owned by another operation" {
				t.Fatalf("lock was altered: %q, %v", contents, err)
			}
		})
	}
}

func TestMergeBackRechecksRootAfterLockClears(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	projectPath := filepath.Join(root, "repo")
	initGitRepo(t, projectPath)
	st, err := store.Open(filepath.Join(root, "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc := New(config.Default(), st, events.NewBus(), nil)
	if _, err := svc.CreateOrAttachProject(ctx, CreateOrAttachProjectRequest{ParentPath: root, Name: "repo"}); err != nil {
		t.Fatal(err)
	}
	result := createSuggestedTodoWorktreeForTest(t, ctx, svc, st, projectPath, "Concurrent merge lock", "feat/concurrent-lock", "concurrent-lock")
	if err := os.WriteFile(filepath.Join(result.WorktreePath, "FEATURE.txt"), []byte("merge me\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, result.WorktreePath, "git", "add", "FEATURE.txt")
	runGit(t, result.WorktreePath, "git", "commit", "-m", "feature")
	lock := filepath.Join(projectPath, ".git", "index.lock")
	changed := false
	released := make(chan error, 1)
	svc.gitRepoStatusReader = func(ctx context.Context, path string) (scanner.GitRepoStatus, error) {
		status, err := scanner.ReadGitRepoStatus(ctx, path)
		if path == projectPath && err == nil && !changed {
			changed = true
			// Simulate a writer starting just after the first clean snapshot.
			if err := os.WriteFile(lock, nil, 0o600); err != nil {
				return status, err
			}
			if err := os.WriteFile(filepath.Join(projectPath, "OTHER.txt"), []byte("concurrent work\n"), 0o644); err != nil {
				return status, err
			}
			go func() {
				time.Sleep(100 * time.Millisecond)
				released <- os.Remove(lock)
			}()
		}
		return status, err
	}
	_, mergeErr := svc.MergeWorktreeBack(ctx, result.WorktreePath)
	if mergeErr == nil || !strings.Contains(mergeErr.Error(), "root worktree became dirty") {
		t.Fatalf("merge error = %v, want dirty-root revalidation failure", mergeErr)
	}
	if err := <-released; err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(projectPath, "FEATURE.txt")); !os.IsNotExist(err) {
		t.Fatalf("merge proceeded after concurrent root changes: %v", err)
	}
}
