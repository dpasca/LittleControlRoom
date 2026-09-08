package service

import (
	"context"
	"os"
	"testing"
	"time"
)

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
			f.service.codexThreadDeleter = func(ctx context.Context, home string, ids []string) ([]string, error) {
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
