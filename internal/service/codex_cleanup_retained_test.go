package service

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"lcroom/internal/codexstate"
	"lcroom/internal/store"
)

func TestCodexCleanupRetainedWithoutDeletionRecords(t *testing.T) {
	fixture := newCodexCleanupFixture(t)
	cwd := filepath.Join(fixture.worktreeBase, "kept-project")
	fixture.addThread(t, cleanupThreadFixture{ID: "kept", CWD: cwd, LastActivity: time.Now()})
	audit, err := fixture.service.AuditCodexSessionStorage(context.Background(), CodexCleanupAuditOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(audit.Groups) != 0 || audit.Storage.files != nil {
		t.Fatal("retained-only audit must release inventory and have no deletion candidates")
	}
	for _, group := range audit.Retained {
		if group.Path == cwd && group.Bytes > 0 && group.Files == 1 {
			return
		}
	}
	t.Fatalf("retained project absent without deletion records: %#v", audit.Retained)
}

func TestCodexCleanupRetainedAccountsForFilesOnce(t *testing.T) {
	home := t.TempDir()
	file := func(name string) string { return filepath.Join(home, "sessions", name) }
	audit := CodexCleanupAudit{
		Storage: CodexCleanupStorage{files: map[string]int64{
			file("eligible"): 100, file("kept"): 50, file("unknown"): 20,
			file("ambiguous"): 30, filepath.Join(home, "cache", "data"): 10,
		}},
		Groups: []CodexCleanupWorktreeGroup{{Threads: []CodexCleanupThread{{RolloutFiles: []CodexCleanupRolloutFile{{Path: file("eligible")}}}}}},
	}
	threads := []codexstate.Thread{
		{RolloutPath: file("eligible"), CWD: "/repo-old"},
		{RolloutPath: file("kept"), CWD: "/repo-old"},
		{RolloutPath: file("kept"), CWD: "/repo-old"},
		{RolloutPath: file("ambiguous"), CWD: "/a"},
		{RolloutPath: file("ambiguous"), CWD: "/b"},
	}
	groups := buildCodexCleanupRetained(audit, threads, map[string]store.DeletedWorktreeRecord{
		"/repo-old": {RootPath: "/repo"},
	}, home)
	var total int64
	var files int
	for _, group := range groups {
		total += group.Bytes
		files += group.Files
		if group.Path == "/repo-old" && (group.Bytes != 50 || group.Files != 1 || group.ProjectPath != "/repo") {
			t.Fatalf("bad project attribution: %#v", group)
		}
		if group.Name == "Unattributed session files" && (group.Bytes != 50 || group.Files != 2) {
			t.Fatalf("bad unknown ownership: %#v", group)
		}
	}
	if total != 110 || files != 4 || len(groups) != 3 || groups[len(groups)-1].Name != "cache" {
		t.Fatalf("retained accounting: %#v", groups)
	}
}
