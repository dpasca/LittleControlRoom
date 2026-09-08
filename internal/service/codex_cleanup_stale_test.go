package service

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"lcroom/internal/codexapp"
)

func TestCodexCleanupStaleInactivityThresholds(t *testing.T) {
	f := newCodexCleanupFixture(t)
	now := time.Now().Truncate(time.Second)
	for _, days := range []int{8, 15, 31, 91} {
		f.addThread(t, cleanupThreadFixture{ID: fmt.Sprintf("old-%d", days), CWD: f.rootPath, LastActivity: now.Add(-time.Duration(days) * 24 * time.Hour)})
	}
	f.addThread(t, cleanupThreadFixture{ID: "newest", CWD: f.rootPath, LastActivity: now})
	for _, tc := range []struct{ days, count int }{{0, 4}, {7, 4}, {14, 3}, {30, 2}, {90, 1}} {
		audit, err := f.service.AuditCodexSessionStorage(context.Background(), CodexCleanupAuditOptions{Category: CodexCleanupStale, InactiveDays: tc.days, Now: now})
		if err != nil || len(audit.Groups) != 1 {
			t.Fatalf("%d days: %#v, %v", tc.days, audit.Groups, err)
		}
		g := audit.Groups[0]
		days := max(7, tc.days)
		if g.RootThreadCount != tc.count || g.InactiveDays != days || !audit.RecentCutoff.Equal(now.Add(-time.Duration(days)*24*time.Hour)) {
			t.Fatalf("wrong %d-day policy: %#v", tc.days, g)
		}
	}
	for _, days := range []int{-1, 1, 6, 8, 1000} {
		if _, err := f.service.AuditCodexSessionStorage(context.Background(), CodexCleanupAuditOptions{Category: CodexCleanupStale, InactiveDays: days}); err == nil {
			t.Fatalf("accepted invalid days %d", days)
		}
	}
	if _, err := f.service.AuditCodexSessionStorage(context.Background(), CodexCleanupAuditOptions{InactiveDays: 30}); err == nil {
		t.Fatal("orphaned policy must retain fixed seven-day rules")
	}
}

func TestCodexCleanupRevisionIncludesDisplayedKeepCount(t *testing.T) {
	for _, category := range []CodexCleanupCategory{CodexCleanupOrphaned, CodexCleanupStale} {
		group := CodexCleanupWorktreeGroup{Category: category, InactiveDays: 7, RootThreadCount: 1, TotalThreadCount: 3}
		revision := cleanupGroupRevision(group)
		group.TotalThreadCount++
		if revision == cleanupGroupRevision(group) {
			t.Fatalf("%s preview must reject changed keep counts", category.Label())
		}
	}
}

func TestCodexCleanupStaleThresholdChecksIndexAndRolloutBoundary(t *testing.T) {
	f := newCodexCleanupFixture(t)
	now := time.Now().Truncate(time.Second)
	cutoff := now.Add(-30 * 24 * time.Hour)
	f.addThread(t, cleanupThreadFixture{ID: "boundary", CWD: f.rootPath, LastActivity: cutoff})
	file := f.addThread(t, cleanupThreadFixture{ID: "file-recent", CWD: f.rootPath, LastActivity: cutoff})
	if err := os.Chtimes(file, cutoff.Add(time.Second), cutoff.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	file = f.addThread(t, cleanupThreadFixture{ID: "index-recent", CWD: f.rootPath, LastActivity: cutoff.Add(time.Second)})
	if err := os.Chtimes(file, cutoff, cutoff); err != nil {
		t.Fatal(err)
	}
	f.addThread(t, cleanupThreadFixture{ID: "newest", CWD: f.rootPath, LastActivity: now})
	audit, err := f.service.AuditCodexSessionStorage(context.Background(), CodexCleanupAuditOptions{Category: CodexCleanupStale, InactiveDays: 30, Now: now})
	if err != nil || len(audit.Groups) != 1 || len(audit.Groups[0].Threads) != 1 || audit.Groups[0].Threads[0].ID != "boundary" {
		t.Fatalf("threshold boundary: %#v %v", audit.Groups, err)
	}
}

func TestCodexCleanupStaleDeleteBindsInactivityPolicy(t *testing.T) {
	for _, days := range []int{7, 14, 30, 90} {
		t.Run(fmt.Sprint(days), func(t *testing.T) {
			f := newCodexCleanupFixture(t)
			path := f.addThread(t, cleanupThreadFixture{ID: "old", CWD: f.rootPath, LastActivity: time.Now().Add(-100 * 24 * time.Hour)})
			f.addThread(t, cleanupThreadFixture{ID: "newest", CWD: f.rootPath, LastActivity: time.Now()})
			audit, err := f.service.AuditCodexSessionStorage(context.Background(), CodexCleanupAuditOptions{Category: CodexCleanupStale, InactiveDays: days})
			if err != nil || len(audit.Groups) != 1 {
				t.Fatalf("preview: %#v %v", audit.Groups, err)
			}
			g := audit.Groups[0]
			request := DeleteCodexCleanupWorktreeRequest{Category: g.Category, InactiveDays: days, WorktreePath: g.WorktreePath, RootProjectPath: g.RootProjectPath, RootThreadIDs: []string{"old"}, Revision: g.Revision}
			called := false
			f.service.codexThreadDeleter = func(ctx context.Context, home string, ids []string, _ func(codexapp.ThreadDeleteProgress)) ([]string, error) {
				called = true
				if _, err := f.codexDB.Exec("DELETE FROM threads WHERE id='old'"); err != nil {
					return nil, err
				}
				return ids, os.Remove(path)
			}
			request.InactiveDays = 7
			if days == 7 {
				request.InactiveDays = 30
			}
			if _, err := f.service.DeleteCodexCleanupWorktree(context.Background(), request); err == nil || called {
				t.Fatal("changed threshold must reject even when eligible IDs are unchanged")
			}
			request.InactiveDays = days
			result, err := f.service.DeleteCodexCleanupWorktree(context.Background(), request)
			if err != nil || !called || !result.Verified {
				t.Fatalf("threshold lost in delete revalidation: %#v %v", result, err)
			}
		})
	}
}

func TestCodexCleanupStaleGroupsExistingProjectAndProtectsTrees(t *testing.T) {
	f := newCodexCleanupFixture(t)
	now := time.Now()
	old := now.Add(-30 * 24 * time.Hour)
	for _, thread := range []cleanupThreadFixture{
		{ID: "old-root", LastActivity: old},
		{ID: "old-child", ParentID: "old-root", AgentRole: "worker", LastActivity: old},
		{ID: "pinned", Pinned: true, LastActivity: old},
		{ID: "loaded", LastActivity: old},
		{ID: "recent", LastActivity: now.Add(-time.Hour)},
		{ID: "active-tree", LastActivity: old},
		{ID: "active-child", ParentID: "active-tree", AgentRole: "worker", LastActivity: now.Add(-time.Hour)},
	} {
		thread.CWD = f.rootPath
		f.addThread(t, thread)
	}
	orphaned, err := f.service.AuditCodexSessionStorage(context.Background(), CodexCleanupAuditOptions{Now: now})
	if err != nil || len(orphaned.Groups) != 0 {
		t.Fatalf("existing project in orphaned audit: %#v, %v", orphaned.Groups, err)
	}
	audit, err := f.service.AuditCodexSessionStorage(context.Background(), CodexCleanupAuditOptions{
		Category: CodexCleanupStale, Now: now, LoadedThreadIDs: []string{"loaded"},
	})
	if err != nil || len(audit.Groups) != 1 {
		t.Fatalf("stale audit: %#v, %v", audit.Groups, err)
	}
	group := audit.Groups[0]
	if group.Category != CodexCleanupStale || group.TotalThreadCount != 7 || group.RootThreadCount != 1 || group.DescendantCount != 1 || group.Threads[0].ID != "old-root" {
		t.Fatalf("stale selection: %#v", group)
	}
}

func TestCodexCleanupStaleKeepsNewestEvenWhenOld(t *testing.T) {
	f := newCodexCleanupFixture(t)
	f.addThread(t, cleanupThreadFixture{ID: "old", CWD: f.rootPath, LastActivity: time.Now().Add(-30 * 24 * time.Hour)})
	f.addThread(t, cleanupThreadFixture{ID: "newest", CWD: f.rootPath, LastActivity: time.Now().Add(-14 * 24 * time.Hour)})
	audit, err := f.service.AuditCodexSessionStorage(context.Background(), CodexCleanupAuditOptions{Category: CodexCleanupStale})
	if err != nil || len(audit.Groups) != 1 || audit.Groups[0].RootThreadCount != 1 || audit.Groups[0].Threads[0].ID != "old" {
		t.Fatalf("newest session not kept: %#v, %v", audit.Groups, err)
	}
}

func TestCodexCleanupStaleDeleteRevalidatesCategoryAndActivity(t *testing.T) {
	for _, change := range []string{"category", "recent", "pinned", "loaded", "loaded-during-audit", "pinned-during-audit", "modified-during-audit", "missing", "unchanged"} {
		t.Run(change, func(t *testing.T) {
			f := newCodexCleanupFixture(t)
			path := f.addThread(t, cleanupThreadFixture{ID: "old", CWD: f.rootPath, LastActivity: time.Now().Add(-30 * 24 * time.Hour)})
			f.addThread(t, cleanupThreadFixture{ID: "newest", CWD: f.rootPath, LastActivity: time.Now()})
			audit, err := f.service.AuditCodexSessionStorage(context.Background(), CodexCleanupAuditOptions{Category: CodexCleanupStale})
			if err != nil || len(audit.Groups) != 1 {
				t.Fatalf("preview: %#v, %v", audit.Groups, err)
			}
			group := audit.Groups[0]
			request := DeleteCodexCleanupWorktreeRequest{Category: group.Category, WorktreePath: group.WorktreePath, RootProjectPath: group.RootProjectPath, RootThreadIDs: []string{"old"}, Revision: group.Revision}
			switch change {
			case "category":
				request.Category = CodexCleanupOrphaned
			case "recent":
				err = os.Chtimes(path, time.Now(), time.Now())
			case "pinned":
				_, err = f.codexDB.Exec("UPDATE threads SET is_pinned=1 WHERE id='old'")
			case "loaded":
				request.LoadedThreadIDs = []string{"old"}
			case "loaded-during-audit":
				request.CurrentLoadedThreadIDs = func() []string { return []string{"old"} }
			case "pinned-during-audit":
				request.CurrentLoadedThreadIDs = func() []string {
					if _, err := f.codexDB.Exec("UPDATE threads SET is_pinned=1 WHERE id='old'"); err != nil {
						t.Fatal(err)
					}
					return nil
				}
			case "modified-during-audit":
				request.CurrentLoadedThreadIDs = func() []string {
					if err := os.Chtimes(path, time.Now(), time.Now()); err != nil {
						t.Fatal(err)
					}
					return nil
				}
			case "missing":
				err = os.Remove(f.rootPath)
			}
			if err != nil {
				t.Fatal(err)
			}
			called := false
			f.service.codexThreadDeleter = func(ctx context.Context, home string, ids []string, _ func(codexapp.ThreadDeleteProgress)) ([]string, error) {
				called = true
				if len(ids) != 1 || ids[0] != "old" {
					t.Fatalf("unexpected deletion: %v", ids)
				}
				if _, err := f.codexDB.Exec("DELETE FROM threads WHERE id='old'"); err != nil {
					return nil, err
				}
				return ids, os.Remove(path)
			}
			result, err := f.service.DeleteCodexCleanupWorktree(context.Background(), request)
			if change == "unchanged" {
				if err != nil || !called || !result.Verified || result.DeletedRootThreads != 1 {
					t.Fatalf("delete failed: %#v, %v", result, err)
				}
			} else if err == nil || called {
				t.Fatalf("changed preview reached deletion: %#v, %v", result, err)
			}
		})
	}
}
