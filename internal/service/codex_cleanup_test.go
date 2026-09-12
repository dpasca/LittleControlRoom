package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"lcroom/internal/codexapp"
	"lcroom/internal/config"
	"lcroom/internal/events"
	"lcroom/internal/model"
	"lcroom/internal/store"

	_ "modernc.org/sqlite"
)

func TestCodexCleanupStorageInventory(t *testing.T) {
	home := t.TempDir()
	for _, dir := range []string{"sessions", "archived_sessions", "cache"} {
		if err := os.Mkdir(filepath.Join(home, dir), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(home, dir, "data"), []byte("12345"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(home, "sessions"), filepath.Join(home, "alias")); err != nil {
		t.Fatal(err)
	}
	storage := inspectCodexCleanupStorage(context.Background(), home)
	if storage.TotalBytes != 15 || storage.SessionBytes != 10 || storage.Partial {
		t.Fatalf("inventory = %#v", storage)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if !inspectCodexCleanupStorage(ctx, home).Partial {
		t.Fatal("canceled inventory must be partial")
	}
}

func TestAuditCodexSessionStorageGroupsEligibleThreadTrees(t *testing.T) {
	t.Parallel()

	fixture := newCodexCleanupFixture(t)
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	eligiblePath := fixture.addDeletedWorktree(t, "eligible", now.Add(-14*24*time.Hour), false)
	fixture.addThread(t, cleanupThreadFixture{
		ID:           "thread-root",
		CWD:          eligiblePath,
		Title:        "Old root conversation",
		GitSHA:       "1234567890abcdef",
		GitBranch:    "feature/eligible",
		LastActivity: now.Add(-60 * 24 * time.Hour),
	})
	fixture.addThread(t, cleanupThreadFixture{
		ID:           "thread-child",
		CWD:          eligiblePath,
		AgentRole:    "worker",
		ParentID:     "thread-root",
		LastActivity: now.Add(-55 * 24 * time.Hour),
	})

	pinnedPath := fixture.addDeletedWorktree(t, "pinned", now.Add(-14*24*time.Hour), false)
	fixture.addThread(t, cleanupThreadFixture{ID: "thread-pinned", CWD: pinnedPath, Pinned: true, LastActivity: now.Add(-90 * 24 * time.Hour)})
	loadedPath := fixture.addDeletedWorktree(t, "loaded", now.Add(-14*24*time.Hour), false)
	fixture.addThread(t, cleanupThreadFixture{ID: "thread-loaded", CWD: loadedPath, LastActivity: now.Add(-90 * 24 * time.Hour)})
	recentPath := fixture.addDeletedWorktree(t, "recent", now.Add(-14*24*time.Hour), false)
	fixture.addThread(t, cleanupThreadFixture{ID: "thread-recent", CWD: recentPath, LastActivity: now.Add(-2 * 24 * time.Hour)})
	unrelatedRollout := fixture.addThread(t, cleanupThreadFixture{
		ID:           "unrelated-broken-root",
		CWD:          filepath.Join(fixture.worktreeBase, "unrelated-missing-worktree"),
		LastActivity: now.Add(-90 * 24 * time.Hour),
	})
	if err := os.WriteFile(unrelatedRollout, []byte("not-json\n"), 0o600); err != nil {
		t.Fatalf("corrupt unrelated rollout: %v", err)
	}
	unrelatedParentPath := filepath.Join(fixture.worktreeBase, "unrelated-parent-worktree")
	fixture.addThread(t, cleanupThreadFixture{ID: "unrelated-parent", CWD: unrelatedParentPath, LastActivity: now.Add(-90 * 24 * time.Hour)})
	unrelatedChildRollout := fixture.addThread(t, cleanupThreadFixture{
		ID:           "unrelated-broken-child",
		CWD:          unrelatedParentPath,
		AgentRole:    "worker",
		ParentID:     "unrelated-parent",
		LastActivity: now.Add(-90 * 24 * time.Hour),
	})
	if err := os.WriteFile(unrelatedChildRollout, []byte("not-json\n"), 0o600); err != nil {
		t.Fatalf("corrupt unrelated descendant rollout: %v", err)
	}

	audit, err := fixture.service.AuditCodexSessionStorage(context.Background(), CodexCleanupAuditOptions{
		Now:             now,
		LoadedThreadIDs: []string{"thread-loaded"},
	})
	if err != nil {
		t.Fatalf("AuditCodexSessionStorage() error = %v", err)
	}
	if len(audit.Groups) != 1 {
		t.Fatalf("group count = %d, want 1: %#v", len(audit.Groups), audit.Groups)
	}
	group := audit.Groups[0]
	if group.WorktreePath != eligiblePath || group.RootThreadCount != 1 || group.DescendantCount != 1 {
		t.Fatalf("eligible group = %#v", group)
	}
	if group.RecoverableBytes <= 0 || group.Revision == "" || group.Reason != codexCleanupReason {
		t.Fatalf("eligible preview metadata = %#v", group)
	}
	if len(group.Threads) != 1 || group.Threads[0].ID != "thread-root" || !reflect.DeepEqual(group.Threads[0].MemberIDs, []string{"thread-child", "thread-root"}) {
		t.Fatalf("eligible tree = %#v", group.Threads)
	}
	if group.Threads[0].GitBranch != "feature/eligible" || group.Threads[0].GitSHA != "1234567890abcdef" {
		t.Fatalf("Git preview = %#v", group.Threads[0])
	}
	if audit.Excluded.Pinned != 1 || audit.Excluded.Loaded != 1 || audit.Excluded.Recent != 1 {
		t.Fatalf("safety exclusions = %#v", audit.Excluded)
	}
	if audit.Excluded.Uncertain != 0 {
		t.Fatalf("known descendants should not be counted as uncertain roots: %#v", audit.Excluded)
	}
}

func TestAuditCodexSessionStorageUsesSevenDayWindowWithoutUserEventIndexHint(t *testing.T) {
	t.Parallel()

	fixture := newCodexCleanupFixture(t)
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	eligiblePath := fixture.addDeletedWorktree(t, "week-old", now.Add(-14*24*time.Hour), false)
	fixture.addThread(t, cleanupThreadFixture{
		ID:           "thread-week-old",
		CWD:          eligiblePath,
		LastActivity: now.Add(-8 * 24 * time.Hour),
	})
	if _, err := fixture.codexDB.Exec(`UPDATE threads SET has_user_event = 0 WHERE id = 'thread-week-old'`); err != nil {
		t.Fatalf("clear stale user-event index hint: %v", err)
	}

	recentPath := fixture.addDeletedWorktree(t, "under-a-week", now.Add(-14*24*time.Hour), false)
	fixture.addThread(t, cleanupThreadFixture{
		ID:           "thread-under-a-week",
		CWD:          recentPath,
		LastActivity: now.Add(-6 * 24 * time.Hour),
	})

	audit, err := fixture.service.AuditCodexSessionStorage(context.Background(), CodexCleanupAuditOptions{Now: now})
	if err != nil {
		t.Fatalf("AuditCodexSessionStorage() error = %v", err)
	}
	if !audit.RecentCutoff.Equal(now.Add(-7 * 24 * time.Hour)) {
		t.Fatalf("recent cutoff = %v, want seven-day cutoff", audit.RecentCutoff)
	}
	if len(audit.Groups) != 1 || audit.Groups[0].WorktreePath != eligiblePath {
		t.Fatalf("eligible groups = %#v, want week-old tree only", audit.Groups)
	}
	if audit.Excluded.Recent != 1 || audit.Excluded.Uncertain != 0 {
		t.Fatalf("exclusions = %#v, want under-a-week tree recent and no uncertainty", audit.Excluded)
	}
}

func TestAuditCodexSessionStorageExcludesWholeTreeForPinnedDescendant(t *testing.T) {
	t.Parallel()

	fixture := newCodexCleanupFixture(t)
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	worktreePath := fixture.addDeletedWorktree(t, "pinned-child", now.Add(-20*24*time.Hour), false)
	fixture.addThread(t, cleanupThreadFixture{ID: "thread-root", CWD: worktreePath, LastActivity: now.Add(-90 * 24 * time.Hour)})
	fixture.addThread(t, cleanupThreadFixture{
		ID:           "thread-child",
		CWD:          worktreePath,
		ParentID:     "thread-root",
		Pinned:       true,
		LastActivity: now.Add(-80 * 24 * time.Hour),
	})

	audit, err := fixture.service.AuditCodexSessionStorage(context.Background(), CodexCleanupAuditOptions{Now: now})
	if err != nil {
		t.Fatalf("AuditCodexSessionStorage() error = %v", err)
	}
	if len(audit.Groups) != 0 || audit.Excluded.Pinned != 1 {
		t.Fatalf("audit = %#v, want cascading tree excluded as pinned", audit)
	}
}

func TestAuditCodexSessionStorageExcludesExternalAndUncertainThreads(t *testing.T) {
	t.Parallel()

	fixture := newCodexCleanupFixture(t)
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	externalPath := filepath.Join("/Volumes", "offline-backup", "repo--external")
	fixture.addDeletedWorktreeAt(t, externalPath, "external", now.Add(-20*24*time.Hour), false)
	fixture.addThread(t, cleanupThreadFixture{ID: "thread-external", CWD: externalPath, LastActivity: now.Add(-90 * 24 * time.Hour)})

	uncertainPath := fixture.addDeletedWorktree(t, "uncertain", now.Add(-20*24*time.Hour), false)
	uncertainRollout := fixture.addThread(t, cleanupThreadFixture{ID: "thread-uncertain", CWD: uncertainPath, LastActivity: now.Add(-90 * 24 * time.Hour)})
	if err := os.WriteFile(uncertainRollout, []byte("not-json\n"), 0o600); err != nil {
		t.Fatalf("corrupt uncertain rollout: %v", err)
	}

	audit, err := fixture.service.AuditCodexSessionStorage(context.Background(), CodexCleanupAuditOptions{Now: now})
	if err != nil {
		t.Fatalf("AuditCodexSessionStorage() error = %v", err)
	}
	if len(audit.Groups) != 0 || audit.Excluded.ExternalVolume != 1 || audit.Excluded.Uncertain != 1 {
		t.Fatalf("audit exclusions = %#v, groups = %#v", audit.Excluded, audit.Groups)
	}
}

func TestAuditCodexSessionStorageScopesBrokenDescendantToItsRoot(t *testing.T) {
	t.Parallel()

	fixture := newCodexCleanupFixture(t)
	now := time.Now().UTC().Truncate(time.Second)
	worktreePath := fixture.addDeletedWorktree(t, "broken-child", now.Add(-20*24*time.Hour), false)
	fixture.addThread(t, cleanupThreadFixture{ID: "thread-root", CWD: worktreePath, LastActivity: now.Add(-90 * 24 * time.Hour)})
	childRollout := fixture.addThread(t, cleanupThreadFixture{
		ID:           "thread-broken-child",
		CWD:          filepath.Join(fixture.worktreeBase, "missing-child-cwd"),
		AgentRole:    "worker",
		ParentID:     "thread-root",
		LastActivity: now.Add(-90 * 24 * time.Hour),
	})
	if err := os.WriteFile(childRollout, []byte("not-json\n"), 0o600); err != nil {
		t.Fatalf("corrupt candidate descendant rollout: %v", err)
	}

	audit, err := fixture.service.AuditCodexSessionStorage(context.Background(), CodexCleanupAuditOptions{Now: now})
	if err != nil {
		t.Fatalf("AuditCodexSessionStorage() error = %v", err)
	}
	if len(audit.Groups) != 0 || audit.Excluded.Uncertain != 1 {
		t.Fatalf("broken descendant audit = %#v", audit)
	}
}

func TestAuditCodexSessionStorageUsesRolloutMTimeForRecency(t *testing.T) {
	t.Parallel()

	fixture := newCodexCleanupFixture(t)
	now := time.Now().UTC().Truncate(time.Second)
	worktreePath := fixture.addDeletedWorktree(t, "recent-rollout", now.Add(-20*24*time.Hour), false)
	rolloutPath := fixture.addThread(t, cleanupThreadFixture{ID: "thread-root", CWD: worktreePath, LastActivity: now.Add(-90 * 24 * time.Hour)})
	recentRolloutTime := now.Add(-2 * 24 * time.Hour)
	if err := os.Chtimes(rolloutPath, recentRolloutTime, recentRolloutTime); err != nil {
		t.Fatalf("touch recent rollout: %v", err)
	}

	audit, err := fixture.service.AuditCodexSessionStorage(context.Background(), CodexCleanupAuditOptions{Now: now})
	if err != nil {
		t.Fatalf("AuditCodexSessionStorage() error = %v", err)
	}
	if len(audit.Groups) != 0 || audit.Excluded.Recent != 1 {
		t.Fatalf("recent-rollout audit = %#v", audit)
	}
}

func TestAuditCodexSessionStorageKeepsPinnedRecordsAndOpenWork(t *testing.T) {
	t.Parallel()

	fixture := newCodexCleanupFixture(t)
	now := time.Now().UTC().Truncate(time.Second)
	pinnedPath := fixture.addDeletedWorktree(t, "lcr-pinned", now.Add(-20*24*time.Hour), true)
	fixture.addThread(t, cleanupThreadFixture{ID: "thread-pinned", CWD: pinnedPath, LastActivity: now.Add(-90 * 24 * time.Hour)})
	openWorkPath := fixture.addDeletedWorktree(t, "open-work", now.Add(-20*24*time.Hour), false)
	fixture.addThread(t, cleanupThreadFixture{ID: "thread-open-work", CWD: openWorkPath, LastActivity: now.Add(-90 * 24 * time.Hour)})
	if _, err := fixture.store.AddTodo(context.Background(), openWorkPath, "Keep this deleted worktree recoverable"); err != nil {
		t.Fatalf("add open worktree TODO: %v", err)
	}

	audit, err := fixture.service.AuditCodexSessionStorage(context.Background(), CodexCleanupAuditOptions{Now: now})
	if err != nil {
		t.Fatalf("AuditCodexSessionStorage() error = %v", err)
	}
	if len(audit.Groups) != 0 || audit.Excluded.Pinned != 1 || audit.Excluded.Uncertain != 1 {
		t.Fatalf("protected LCR record audit = %#v", audit)
	}
}

func TestDeleteCodexCleanupWorktreeUsesAppServerBoundaryAndVerifiesFiles(t *testing.T) {
	fixture := newCodexCleanupFixture(t)
	now := time.Now().UTC().Truncate(time.Second)
	worktreePath := fixture.addDeletedWorktree(t, "delete", now.Add(-20*24*time.Hour), false)
	rootRollout := fixture.addThread(t, cleanupThreadFixture{ID: "thread-root", CWD: worktreePath, LastActivity: now.Add(-90 * 24 * time.Hour)})
	childRollout := fixture.addThread(t, cleanupThreadFixture{
		ID:           "thread-child",
		CWD:          worktreePath,
		AgentRole:    "worker",
		ParentID:     "thread-root",
		LastActivity: now.Add(-80 * 24 * time.Hour),
	})

	audit, err := fixture.service.AuditCodexSessionStorage(context.Background(), CodexCleanupAuditOptions{Now: now})
	if err != nil || len(audit.Groups) != 1 {
		t.Fatalf("initial audit = %#v, %v", audit, err)
	}
	group := audit.Groups[0]
	var calledWith []string
	fixture.service.codexThreadDeleter = func(_ context.Context, codexHome string, threadIDs []string, _ func(codexapp.ThreadDeleteProgress)) ([]string, error) {
		calledWith = append([]string(nil), threadIDs...)
		if codexHome != fixture.codexHome {
			t.Fatalf("deleter Codex home = %q, want %q", codexHome, fixture.codexHome)
		}
		if _, err := fixture.codexDB.Exec(`DELETE FROM threads WHERE id IN ('thread-root', 'thread-child')`); err != nil {
			t.Fatalf("delete fake Codex rows: %v", err)
		}
		for _, path := range []string{rootRollout, childRollout} {
			if err := os.Remove(path); err != nil {
				t.Fatalf("remove fake rollout %s: %v", path, err)
			}
		}
		return append([]string(nil), threadIDs...), nil
	}

	result, err := fixture.service.DeleteCodexCleanupWorktree(context.Background(), DeleteCodexCleanupWorktreeRequest{
		WorktreePath:    group.WorktreePath,
		RootProjectPath: group.RootProjectPath,
		RootThreadIDs:   []string{"thread-root"},
		Revision:        group.Revision,
	})
	if err != nil {
		t.Fatalf("DeleteCodexCleanupWorktree() error = %v", err)
	}
	if !reflect.DeepEqual(calledWith, []string{"thread-root"}) {
		t.Fatalf("app-server boundary ids = %#v, want root only", calledWith)
	}
	if !result.Verified || result.DeletedRootThreads != 1 || result.DeletedDescendants != 1 {
		t.Fatalf("delete result = %#v", result)
	}
	if result.ExpectedBytes <= 0 || result.VerifiedReclaimedBytes != result.ExpectedBytes {
		t.Fatalf("verified bytes = %#v", result)
	}
}

func TestCodexCleanupProgressVerifiesEachRootBeforeGroupFinishes(t *testing.T) {
	f := newCodexCleanupFixture(t)
	now := time.Now().UTC().Truncate(time.Second)
	path := f.addDeletedWorktree(t, "progress", now.Add(-20*24*time.Hour), false)
	files := make(map[string]string)
	for _, id := range []string{"first", "second"} {
		files[id] = f.addThread(t, cleanupThreadFixture{ID: id, CWD: path, LastActivity: now.Add(-90 * 24 * time.Hour)})
	}
	audit, err := f.service.AuditCodexSessionStorage(context.Background(), CodexCleanupAuditOptions{})
	if err != nil || len(audit.Groups) != 1 {
		t.Fatalf("audit: %#v %v", audit, err)
	}
	g := audit.Groups[0]
	var updates []CodexCleanupProgress
	f.service.codexThreadDeleter = func(ctx context.Context, home string, ids []string, progress func(codexapp.ThreadDeleteProgress)) ([]string, error) {
		for i, id := range ids {
			progress(codexapp.ThreadDeleteProgress{ThreadID: id})
			if _, err := f.codexDB.Exec("DELETE FROM threads WHERE id = ?", id); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(files[id]); err != nil {
				t.Fatal(err)
			}
			progress(codexapp.ThreadDeleteProgress{ThreadID: id, Completed: true})
			latest := updates[len(updates)-1]
			if !latest.HoldsRepositoryLock {
				t.Fatal("cleanup should report its repository lock while deleting")
			}
			if latest.CompletedRoots != i+1 || latest.VerifiedReclaimedBytes <= 0 || (i == 0 && latest.VerifiedReclaimedBytes >= g.RecoverableBytes) {
				t.Fatalf("progress before next root: %#v", latest)
			}
		}
		return ids, nil
	}
	result, err := f.service.DeleteCodexCleanupWorktree(context.Background(), DeleteCodexCleanupWorktreeRequest{
		WorktreePath: g.WorktreePath, RootProjectPath: g.RootProjectPath, RootThreadIDs: []string{"first", "second"}, Revision: g.Revision,
		Progress: func(p CodexCleanupProgress) { updates = append(updates, p) },
	})
	if err != nil || !result.Verified || result.VerifiedReclaimedBytes != g.RecoverableBytes {
		t.Fatalf("result=%#v error=%v", result, err)
	}
	if updates[len(updates)-1].VerifiedReclaimedBytes != result.VerifiedReclaimedBytes {
		t.Fatal("final verification double counted progress")
	}
	if updates[0].HoldsRepositoryLock || updates[len(updates)-1].HoldsRepositoryLock {
		t.Fatal("cleanup should clear its repository lock before and after deletion")
	}
}

func TestDeleteCodexCleanupWorktreeRejectsChangedPreview(t *testing.T) {
	fixture := newCodexCleanupFixture(t)
	now := time.Now().UTC().Truncate(time.Second)
	worktreePath := fixture.addDeletedWorktree(t, "changed", now.Add(-20*24*time.Hour), false)
	fixture.addThread(t, cleanupThreadFixture{ID: "thread-root", CWD: worktreePath, LastActivity: now.Add(-90 * 24 * time.Hour)})
	audit, err := fixture.service.AuditCodexSessionStorage(context.Background(), CodexCleanupAuditOptions{Now: now})
	if err != nil || len(audit.Groups) != 1 {
		t.Fatalf("initial audit = %#v, %v", audit, err)
	}
	group := audit.Groups[0]
	if _, err := fixture.codexDB.Exec(`UPDATE threads SET is_pinned = 1 WHERE id = 'thread-root'`); err != nil {
		t.Fatalf("pin Codex thread: %v", err)
	}
	called := false
	fixture.service.codexThreadDeleter = func(context.Context, string, []string, func(codexapp.ThreadDeleteProgress)) ([]string, error) {
		called = true
		return nil, nil
	}

	_, err = fixture.service.DeleteCodexCleanupWorktree(context.Background(), DeleteCodexCleanupWorktreeRequest{
		WorktreePath:    group.WorktreePath,
		RootProjectPath: group.RootProjectPath,
		RootThreadIDs:   []string{"thread-root"},
		Revision:        group.Revision,
	})
	if err == nil || !strings.Contains(err.Error(), "no longer eligible") {
		t.Fatalf("changed-preview error = %v", err)
	}
	if called {
		t.Fatal("deleter was called after the safety preview changed")
	}
}

func TestDeleteCodexCleanupWorktreeWaitsForRepositoryWorktreeOperations(t *testing.T) {
	fixture := newCodexCleanupFixture(t)
	now := time.Now().UTC().Truncate(time.Second)
	worktreePath := fixture.addDeletedWorktree(t, "worktree-operation", now.Add(-20*24*time.Hour), false)
	fixture.addThread(t, cleanupThreadFixture{ID: "thread-root", CWD: worktreePath, LastActivity: now.Add(-90 * 24 * time.Hour)})
	audit, err := fixture.service.AuditCodexSessionStorage(context.Background(), CodexCleanupAuditOptions{Now: now})
	if err != nil || len(audit.Groups) != 1 {
		t.Fatalf("initial audit = %#v, %v", audit, err)
	}
	group := audit.Groups[0]
	unlock := fixture.service.worktreeCreateLocks.Lock(group.RootProjectPath)
	defer unlock()
	deleteCalled := false
	fixture.service.codexThreadDeleter = func(context.Context, string, []string, func(codexapp.ThreadDeleteProgress)) ([]string, error) {
		deleteCalled = true
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()

	_, err = fixture.service.DeleteCodexCleanupWorktree(ctx, DeleteCodexCleanupWorktreeRequest{
		WorktreePath:    group.WorktreePath,
		RootProjectPath: group.RootProjectPath,
		RootThreadIDs:   []string{"thread-root"},
		Revision:        group.Revision,
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("worktree-operation lock error = %v", err)
	}
	if deleteCalled {
		t.Fatal("deleter ran while a repository worktree operation held the family lock")
	}
}

func TestDeleteCodexCleanupWorktreeVerifiesDeletionAfterLostResponse(t *testing.T) {
	fixture := newCodexCleanupFixture(t)
	now := time.Now().UTC().Truncate(time.Second)
	worktreePath := fixture.addDeletedWorktree(t, "lost-response", now.Add(-20*24*time.Hour), false)
	rolloutPath := fixture.addThread(t, cleanupThreadFixture{ID: "thread-root", CWD: worktreePath, LastActivity: now.Add(-90 * 24 * time.Hour)})
	audit, err := fixture.service.AuditCodexSessionStorage(context.Background(), CodexCleanupAuditOptions{Now: now})
	if err != nil || len(audit.Groups) != 1 {
		t.Fatalf("initial audit = %#v, %v", audit, err)
	}
	group := audit.Groups[0]
	fixture.service.codexThreadDeleter = func(context.Context, string, []string, func(codexapp.ThreadDeleteProgress)) ([]string, error) {
		if _, err := fixture.codexDB.Exec(`DELETE FROM threads WHERE id = 'thread-root'`); err != nil {
			t.Fatalf("delete fake Codex row: %v", err)
		}
		if err := os.Remove(rolloutPath); err != nil {
			t.Fatalf("remove fake rollout: %v", err)
		}
		return nil, errors.New("app-server response lost")
	}

	result, err := fixture.service.DeleteCodexCleanupWorktree(context.Background(), DeleteCodexCleanupWorktreeRequest{
		WorktreePath:    group.WorktreePath,
		RootProjectPath: group.RootProjectPath,
		RootThreadIDs:   []string{"thread-root"},
		Revision:        group.Revision,
	})
	if err == nil || !strings.Contains(err.Error(), "response lost") {
		t.Fatalf("lost-response error = %v", err)
	}
	if !result.Verified || result.DeletedRootThreads != 1 || result.VerifiedReclaimedBytes != group.RecoverableBytes {
		t.Fatalf("verified lost-response result = %#v", result)
	}
}

func TestDeleteCodexCleanupWorktreeVerifiesCompletedDeletionAfterCancellation(t *testing.T) {
	fixture := newCodexCleanupFixture(t)
	now := time.Now().UTC().Truncate(time.Second)
	worktreePath := fixture.addDeletedWorktree(t, "canceled-response", now.Add(-20*24*time.Hour), false)
	rolloutPath := fixture.addThread(t, cleanupThreadFixture{ID: "thread-root", CWD: worktreePath, LastActivity: now.Add(-90 * 24 * time.Hour)})
	audit, err := fixture.service.AuditCodexSessionStorage(context.Background(), CodexCleanupAuditOptions{Now: now})
	if err != nil || len(audit.Groups) != 1 {
		t.Fatalf("initial audit = %#v, %v", audit, err)
	}
	group := audit.Groups[0]
	ctx, cancel := context.WithCancel(context.Background())
	fixture.service.codexThreadDeleter = func(deleteCtx context.Context, _ string, _ []string, _ func(codexapp.ThreadDeleteProgress)) ([]string, error) {
		if _, err := fixture.codexDB.Exec(`DELETE FROM threads WHERE id = 'thread-root'`); err != nil {
			t.Fatalf("delete fake Codex row: %v", err)
		}
		if err := os.Remove(rolloutPath); err != nil {
			t.Fatalf("remove fake rollout: %v", err)
		}
		cancel()
		return nil, deleteCtx.Err()
	}

	result, err := fixture.service.DeleteCodexCleanupWorktree(ctx, DeleteCodexCleanupWorktreeRequest{
		WorktreePath:    group.WorktreePath,
		RootProjectPath: group.RootProjectPath,
		RootThreadIDs:   []string{"thread-root"},
		Revision:        group.Revision,
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled delete error = %v", err)
	}
	if !result.Verified || result.DeletedRootThreads != 1 || result.VerifiedReclaimedBytes != group.RecoverableBytes {
		t.Fatalf("verified canceled-delete result = %#v", result)
	}
}

func TestStartCodexCleanupAuditorIsReadOnly(t *testing.T) {
	fixture := newCodexCleanupFixture(t)
	deleteCalled := make(chan struct{}, 1)
	fixture.service.codexThreadDeleter = func(context.Context, string, []string, func(codexapp.ThreadDeleteProgress)) ([]string, error) {
		deleteCalled <- struct{}{}
		return nil, nil
	}
	fixture.service.codexCleanupAuditEvery = time.Hour

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		fixture.service.StartCodexCleanupAuditor(ctx, nil)
		close(done)
	}()

	deadline := time.NewTimer(time.Second)
	ticker := time.NewTicker(5 * time.Millisecond)
	defer deadline.Stop()
	defer ticker.Stop()
	for fixture.service.LastCodexCleanupAudit().CompletedAt.IsZero() {
		select {
		case <-deadline.C:
			cancel()
			<-done
			t.Fatal("periodic cleanup audit did not publish a snapshot")
		case <-ticker.C:
		}
	}
	cancel()
	<-done
	select {
	case <-deleteCalled:
		t.Fatal("periodic cleanup auditor invoked the deletion boundary")
	default:
	}
}

type codexCleanupFixture struct {
	t            *testing.T
	service      *Service
	store        *store.Store
	codexDB      *sql.DB
	codexHome    string
	rootPath     string
	worktreeBase string
}

type cleanupThreadFixture struct {
	ID           string
	CWD          string
	Title        string
	GitSHA       string
	GitBranch    string
	AgentRole    string
	ParentID     string
	Pinned       bool
	LastActivity time.Time
}

func newCodexCleanupFixture(t *testing.T) *codexCleanupFixture {
	t.Helper()
	parent := t.TempDir()
	rootPath := filepath.Join(parent, "repo")
	if err := os.MkdirAll(rootPath, 0o755); err != nil {
		t.Fatalf("create root project: %v", err)
	}
	codexHome := t.TempDir()
	if err := os.MkdirAll(filepath.Join(codexHome, "sessions"), 0o755); err != nil {
		t.Fatalf("create Codex sessions: %v", err)
	}
	codexDB, err := sql.Open("sqlite", filepath.Join(codexHome, "state_5.sqlite"))
	if err != nil {
		t.Fatalf("open Codex state db: %v", err)
	}
	if _, err := codexDB.Exec(`
		CREATE TABLE threads (
			id TEXT PRIMARY KEY,
			rollout_path TEXT,
			cwd TEXT,
			title TEXT,
			git_sha TEXT,
			git_branch TEXT,
			git_origin_url TEXT,
			source TEXT,
			agent_role TEXT,
			archived INTEGER,
			has_user_event INTEGER,
			created_at INTEGER,
			updated_at INTEGER,
			recency_at_ms INTEGER,
			is_pinned INTEGER
		)
	`); err != nil {
		t.Fatalf("create Codex threads table: %v", err)
	}

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open LCR store: %v", err)
	}
	cfg := config.Default()
	cfg.CodexHome = codexHome
	fixture := &codexCleanupFixture{
		t:            t,
		store:        st,
		codexDB:      codexDB,
		codexHome:    codexHome,
		rootPath:     rootPath,
		worktreeBase: parent,
	}
	fixture.service = New(cfg, st, events.NewBus(), nil)
	t.Cleanup(func() {
		_ = codexDB.Close()
		_ = st.Close()
	})
	return fixture
}

func (f *codexCleanupFixture) addDeletedWorktree(t *testing.T, name string, missingSince time.Time, pinned bool) string {
	t.Helper()
	path := filepath.Join(f.worktreeBase, "repo--"+name)
	f.addDeletedWorktreeAt(t, path, name, missingSince, pinned)
	return path
}

func (f *codexCleanupFixture) addDeletedWorktreeAt(t *testing.T, path, name string, missingSince time.Time, pinned bool) {
	t.Helper()
	if err := f.store.UpsertProjectState(context.Background(), model.ProjectState{
		Path:                  path,
		Name:                  filepath.Base(path),
		Kind:                  model.ProjectKindProject,
		Status:                model.StatusIdle,
		PresentOnDisk:         false,
		WorktreeRootPath:      f.rootPath,
		WorktreeKind:          model.WorktreeKindLinked,
		WorktreeParentBranch:  "master",
		WorktreeInitialBranch: "feature/" + name,
		RepoBranch:            "feature/" + name,
		Forgotten:             true,
		Pinned:                pinned,
		InScope:               true,
		UpdatedAt:             missingSince,
	}); err != nil {
		t.Fatalf("add deleted worktree %s: %v", name, err)
	}
}

func (f *codexCleanupFixture) addThread(t *testing.T, fixture cleanupThreadFixture) string {
	t.Helper()
	rolloutPath := filepath.Join(f.codexHome, "sessions", "rollout-"+fixture.ID+".jsonl")
	payload := map[string]any{"id": fixture.ID}
	indexedSource := "cli"
	if fixture.AgentRole != "" {
		payload["agent_role"] = fixture.AgentRole
	}
	if fixture.ParentID != "" {
		source := map[string]any{
			"subagent": map[string]any{
				"thread_spawn": map[string]any{"parent_thread_id": fixture.ParentID},
			},
		}
		payload["source"] = source
		encodedSource, err := json.Marshal(source)
		if err != nil {
			t.Fatalf("encode indexed thread source: %v", err)
		}
		indexedSource = string(encodedSource)
	}
	line, err := json.Marshal(map[string]any{"type": "session_meta", "payload": payload})
	if err != nil {
		t.Fatalf("encode rollout metadata: %v", err)
	}
	contents := append(line, '\n')
	contents = append(contents, []byte(`{"type":"event_msg","payload":{"type":"user_message"}}`+"\n")...)
	if err := os.WriteFile(rolloutPath, contents, 0o600); err != nil {
		t.Fatalf("write rollout: %v", err)
	}
	if fixture.Title == "" {
		fixture.Title = "Cleanup " + fixture.ID
	}
	if fixture.LastActivity.IsZero() {
		fixture.LastActivity = time.Now().Add(-90 * 24 * time.Hour)
	}
	if err := os.Chtimes(rolloutPath, fixture.LastActivity, fixture.LastActivity); err != nil {
		t.Fatalf("set rollout activity time: %v", err)
	}
	pinned := 0
	if fixture.Pinned {
		pinned = 1
	}
	if _, err := f.codexDB.Exec(`
		INSERT INTO threads(
			id, rollout_path, cwd, title, git_sha, git_branch, git_origin_url,
			source, agent_role, archived, has_user_event, created_at, updated_at,
			recency_at_ms, is_pinned
		) VALUES (?, ?, ?, ?, ?, ?, '', ?, ?, 0, 1, ?, ?, ?, ?)
	`,
		fixture.ID, rolloutPath, fixture.CWD, fixture.Title, fixture.GitSHA, fixture.GitBranch,
		indexedSource, fixture.AgentRole, fixture.LastActivity.Add(-time.Hour).Unix(), fixture.LastActivity.Unix(),
		fixture.LastActivity.UnixMilli(), pinned,
	); err != nil {
		t.Fatalf("insert Codex thread %s: %v", fixture.ID, err)
	}
	return rolloutPath
}

func TestDeleteCodexCleanupWorktreeReportsFilesRemovedButIndexRetained(t *testing.T) {
	fixture := newCodexCleanupFixture(t)
	now := time.Now().UTC().Truncate(time.Second)
	worktreePath := fixture.addDeletedWorktree(t, "locked-index", now.Add(-20*24*time.Hour), false)
	rolloutPath := fixture.addThread(t, cleanupThreadFixture{ID: "thread-root", CWD: worktreePath, LastActivity: now.Add(-90 * 24 * time.Hour)})
	audit, err := fixture.service.AuditCodexSessionStorage(context.Background(), CodexCleanupAuditOptions{Now: now})
	if err != nil || len(audit.Groups) != 1 {
		t.Fatalf("initial audit = %#v, %v", audit, err)
	}
	group := audit.Groups[0]
	fixture.service.codexThreadDeleter = func(context.Context, string, []string, func(codexapp.ThreadDeleteProgress)) ([]string, error) {
		if err := os.Remove(rolloutPath); err != nil {
			t.Fatalf("remove fake rollout: %v", err)
		}
		return nil, errors.New("database is locked")
	}

	result, err := fixture.service.DeleteCodexCleanupWorktree(context.Background(), DeleteCodexCleanupWorktreeRequest{
		WorktreePath:    group.WorktreePath,
		RootProjectPath: group.RootProjectPath,
		RootThreadIDs:   []string{"thread-root"},
		Revision:        group.Revision,
	})
	if err == nil || !strings.Contains(err.Error(), "1 Codex thread records remain") {
		t.Fatalf("locked-index error = %v", err)
	}
	if result.Verified || result.DeletedRootThreads != 0 || result.VerifiedReclaimedBytes != group.RecoverableBytes {
		t.Fatalf("verified locked-index result = %#v", result)
	}
}
