package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lcroom/internal/claudeartifact"
	"lcroom/internal/codexapp"
	"lcroom/internal/model"
)

type claudeCleanupFixture struct {
	*codexCleanupFixture
	home string
	old  time.Time
}

func newClaudeCleanupFixture(t *testing.T) *claudeCleanupFixture {
	f := &claudeCleanupFixture{codexCleanupFixture: newCodexCleanupFixture(t), home: t.TempDir(), old: time.Now().Add(-100 * 24 * time.Hour)}
	f.service.cfg.ClaudeCodeHome = f.home
	return f
}
func (f *claudeCleanupFixture) write(t *testing.T, path string, data []byte, at time.Time) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
	return path
}
func (f *claudeCleanupFixture) transcript(t *testing.T, cwd, id, agent string, at time.Time) string {
	t.Helper()
	dir := filepath.Join(f.home, "projects", claudeartifact.ProjectDirectoryName(cwd))
	path := filepath.Join(dir, id+".jsonl")
	if agent != "" {
		path = filepath.Join(dir, id, "subagents", "agent-"+agent+".jsonl")
	}
	data, _ := json.Marshal(map[string]any{"type": "user", "sessionId": id, "cwd": cwd, "agentId": agent, "isSidechain": agent != "", "timestamp": at.UTC().Format(time.RFC3339Nano), "message": map[string]string{"role": "user", "content": "hello"}})
	return f.write(t, path, append(data, '\n'), at)
}
func (f *claudeCleanupFixture) audit(t *testing.T, category CodexCleanupCategory, loaded ...string) CodexCleanupAudit {
	t.Helper()
	a, err := f.service.AuditSessionStorage(context.Background(), codexapp.ProviderClaudeCode, CodexCleanupAuditOptions{Category: category, InactiveDays: 7, LoadedThreadIDs: loaded})
	if err != nil {
		t.Fatal(err)
	}
	return a
}
func claudeCleanupRequest(g CodexCleanupWorktreeGroup) DeleteCodexCleanupWorktreeRequest {
	r := DeleteCodexCleanupWorktreeRequest{Category: g.Category, InactiveDays: g.InactiveDays, WorktreePath: g.WorktreePath, RootProjectPath: g.RootProjectPath, Revision: g.Revision}
	for _, tree := range g.Threads {
		r.RootThreadIDs = append(r.RootThreadIDs, tree.ID)
	}
	return r
}
func TestClaudeCleanupStaleRetainsNewestAndDeletesOwnedTree(t *testing.T) {
	f := newClaudeCleanupFixture(t)
	old := f.transcript(t, f.rootPath, "old", "", f.old)
	child := f.transcript(t, f.rootPath, "old", "worker", f.old)
	tool := f.write(t, filepath.Join(strings.TrimSuffix(old, ".jsonl"), "tool-results", "result.txt"), []byte("saved result"), f.old)
	newest := f.transcript(t, f.rootPath, "newest", "", f.old.Add(time.Hour))
	unrelated := f.write(t, filepath.Join(f.home, "settings.json"), []byte("{}"), f.old)
	audit := f.audit(t, CodexCleanupStale)
	if len(audit.Groups) != 1 || audit.EligibleRootThreads != 1 || audit.EligibleDescendants != 1 || audit.Groups[0].TotalThreadCount != 3 {
		t.Fatalf("audit=%+v", audit)
	}
	var retained int64
	for _, g := range audit.Retained {
		retained += g.Bytes
	}
	if retained+audit.RecoverableBytes != audit.Storage.TotalBytes {
		t.Fatal("storage totals do not reconcile")
	}
	result, err := f.service.DeleteSessionCleanupWorktree(context.Background(), codexapp.ProviderClaudeCode, claudeCleanupRequest(audit.Groups[0]))
	if err != nil || !result.Verified || result.DeletedRootThreads != 1 || result.DeletedDescendants != 1 || result.VerifiedReclaimedBytes != audit.RecoverableBytes {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	for _, path := range []string{old, child, tool} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("still present: %s", path)
		}
	}
	for _, path := range []string{newest, unrelated} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("retained file lost: %s", path)
		}
	}
}
func TestClaudeCleanupSafeguards(t *testing.T) {
	for _, kind := range []string{"loaded-root", "loaded-child", "recent-child", "recent-tool", "pinned", "malformed", "unknown-associated", "symlink", "different-child-cwd", "live-cli", "invalid-marker", "newest-malformed"} {
		t.Run(kind, func(t *testing.T) {
			f := newClaudeCleanupFixture(t)
			old := f.transcript(t, f.rootPath, "old", "", f.old)
			f.transcript(t, f.rootPath, "newest", "", time.Now())
			child := f.transcript(t, f.rootPath, "old", "worker", f.old)
			loaded := []string{}
			switch kind {
			case "loaded-root":
				loaded = []string{"old"}
			case "loaded-child":
				loaded = []string{"old/agent-worker"}
			case "recent-child":
				f.transcript(t, f.rootPath, "old", "worker", time.Now())
			case "recent-tool":
				f.write(t, filepath.Join(strings.TrimSuffix(old, ".jsonl"), "tool-results", "x.txt"), []byte("new"), time.Now())
			case "pinned":
				if err := f.store.UpsertProjectState(context.Background(), model.ProjectState{Path: f.rootPath, Name: "repo", Pinned: true, PresentOnDisk: true}); err != nil {
					t.Fatal(err)
				}
			case "malformed":
				f.write(t, old, []byte("{broken\n"), f.old)
			case "newest-malformed":
				f.write(t, filepath.Join(filepath.Dir(old), "unknown.jsonl"), []byte("{}\n"), time.Now())
			case "unknown-associated":
				f.write(t, filepath.Join(strings.TrimSuffix(old, ".jsonl"), "unknown.json"), []byte("{}"), f.old)
			case "symlink":
				if err := os.Symlink(f.rootPath, filepath.Join(strings.TrimSuffix(old, ".jsonl"), "linked")); err != nil {
					t.Fatal(err)
				}
			case "different-child-cwd":
				data, _ := os.ReadFile(child)
				data = []byte(strings.ReplaceAll(string(data), f.rootPath, f.rootPath+"--other"))
				f.write(t, child, data, f.old)
			case "live-cli":
				data, _ := json.Marshal(map[string]any{"pid": os.Getpid(), "sessionId": "old", "cwd": f.rootPath})
				f.write(t, filepath.Join(f.home, "sessions", "live.json"), data, f.old)
			case "invalid-marker":
				f.write(t, filepath.Join(f.home, "sessions", "bad.json"), []byte("{}"), f.old)
			}
			audit, err := f.service.AuditClaudeSessionStorage(context.Background(), CodexCleanupAuditOptions{Category: CodexCleanupStale, LoadedThreadIDs: loaded})
			if kind == "invalid-marker" {
				if err == nil {
					t.Fatal("malformed live marker allowed cleanup")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(audit.Groups) != 0 {
				t.Fatalf("unsafe cleanup: %+v", audit.Groups)
			}
		})
	}
}
func TestClaudeCleanupOrphanRequiresDeletionEvidenceAndGrace(t *testing.T) {
	f := newClaudeCleanupFixture(t)
	eligible := f.addDeletedWorktree(t, "eligible", f.old, false)
	recent := f.addDeletedWorktree(t, "recent", time.Now(), false)
	unknown := filepath.Join(f.worktreeBase, "repo--unknown")
	for _, cwd := range []string{eligible, recent, unknown} {
		f.transcript(t, cwd, filepath.Base(cwd), "", f.old)
	}
	a := f.audit(t, CodexCleanupOrphaned)
	if len(a.Groups) != 1 || a.Groups[0].WorktreePath != eligible || a.Excluded.NoLCRRecord != 1 || a.Excluded.Recent != 1 {
		t.Fatalf("audit=%+v", a)
	}
	if err := os.Mkdir(eligible, 0700); err != nil {
		t.Fatal(err)
	}
	result, err := f.service.DeleteClaudeCleanupWorktree(context.Background(), claudeCleanupRequest(a.Groups[0]))
	if err == nil || result.DeletedRootThreads != 0 {
		t.Fatalf("restored worktree deleted: %+v %v", result, err)
	}
}
func TestClaudeCleanupRejectsChangedPreview(t *testing.T) {
	for _, change := range []string{"content", "new-child", "new-root", "loaded", "pinned", "symlink", "cancel"} {
		t.Run(change, func(t *testing.T) {
			f := newClaudeCleanupFixture(t)
			old := f.transcript(t, f.rootPath, "old", "", f.old)
			f.transcript(t, f.rootPath, "newest", "", time.Now())
			a := f.audit(t, CodexCleanupStale)
			r := claudeCleanupRequest(a.Groups[0])
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch change {
			case "content":
				data, _ := os.ReadFile(old)
				f.write(t, old, []byte(strings.ReplaceAll(string(data), "hello", "world")), f.old)
			case "new-child":
				f.transcript(t, f.rootPath, "old", "added", f.old)
			case "new-root":
				f.transcript(t, f.rootPath, "added", "", f.old)
			case "loaded":
				r.CurrentLoadedThreadIDs = func() []string { return []string{"old"} }
			case "pinned":
				if err := f.store.UpsertProjectState(ctx, model.ProjectState{Path: f.rootPath, Name: "repo", Pinned: true, PresentOnDisk: true}); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Rename(old, old+".backup"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(old+".backup", old); err != nil {
					t.Fatal(err)
				}
			case "cancel":
				cancel()
			}
			result, err := f.service.DeleteClaudeCleanupWorktree(ctx, r)
			if err == nil || result.VerifiedReclaimedBytes != 0 {
				t.Fatalf("changed preview deleted: %+v %v", result, err)
			}
			if _, err := os.Lstat(old); err != nil {
				t.Fatal("original was removed")
			}
		})
	}
}
func TestClaudeCleanupCancellationVerifiesCompletedRoots(t *testing.T) {
	f := newClaudeCleanupFixture(t)
	f.transcript(t, f.rootPath, "a", "", f.old)
	b := f.transcript(t, f.rootPath, "b", "", f.old)
	f.transcript(t, f.rootPath, "newest", "", time.Now())
	a := f.audit(t, CodexCleanupStale)
	r := claudeCleanupRequest(a.Groups[0])
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.Progress = func(p CodexCleanupProgress) {
		if p.CompletedRoots == 1 {
			cancel()
		}
	}
	result, err := f.service.DeleteClaudeCleanupWorktree(ctx, r)
	if !errors.Is(err, context.Canceled) || result.Verified || result.DeletedRootThreads != 1 || result.VerifiedReclaimedBytes == 0 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if _, err := os.Stat(b); err != nil {
		t.Fatal("queued root was deleted")
	}
}

func TestClaudeCleanupRejectsActivityAtDeletionBoundary(t *testing.T) {
	for _, change := range []string{"live", "pinned", "new-child", "same-name-tool-result", "changed-file", "cancel"} {
		t.Run(change, func(t *testing.T) {
			f := newClaudeCleanupFixture(t)
			old := f.transcript(t, f.rootPath, "old", "", f.old)
			f.transcript(t, f.rootPath, "newest", "", time.Now())
			if change == "same-name-tool-result" {
				f.write(t, filepath.Join(strings.TrimSuffix(old, ".jsonl"), "tool-results", "old.jsonl"), []byte("tool output"), f.old)
			}
			a := f.audit(t, CodexCleanupStale)
			r := claudeCleanupRequest(a.Groups[0])
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			changed := false
			r.Progress = func(p CodexCleanupProgress) {
				if changed || p.ActiveRoots != 1 {
					return
				}
				changed = true
				switch change {
				case "live":
					data, _ := json.Marshal(map[string]any{"pid": os.Getpid(), "sessionId": "old", "cwd": f.rootPath})
					f.write(t, filepath.Join(f.home, "sessions", "live.json"), data, time.Now())
				case "pinned":
					if err := f.store.UpsertProjectState(ctx, model.ProjectState{Path: f.rootPath, Name: "repo", Pinned: true, PresentOnDisk: true}); err != nil {
						t.Fatal(err)
					}
				case "new-child", "same-name-tool-result":
					f.transcript(t, f.rootPath, "old", "new-child", time.Now())
				case "changed-file":
					f.transcript(t, f.rootPath, "old", "", time.Now())
				case "cancel":
					cancel()
				}
			}
			result, err := f.service.DeleteClaudeCleanupWorktree(ctx, r)
			if !changed || err == nil || result.VerifiedReclaimedBytes != 0 {
				t.Fatalf("boundary change deleted history: %+v %v", result, err)
			}
			if _, err := os.Stat(old); err != nil {
				t.Fatal("parent removed")
			}
		})
	}
}

func TestClaudeCleanupThresholdsAndMetadata(t *testing.T) {
	f := newClaudeCleanupFixture(t)
	for _, days := range []int{8, 15, 31, 91} {
		f.transcript(t, f.rootPath, fmt.Sprintf("old-%d", days), "", time.Now().Add(-time.Duration(days)*24*time.Hour))
	}
	f.transcript(t, f.rootPath, "newest", "", time.Now())
	for _, tc := range []struct{ days, count int }{{7, 4}, {14, 3}, {30, 2}, {90, 1}} {
		a, err := f.service.AuditClaudeSessionStorage(context.Background(), CodexCleanupAuditOptions{Category: CodexCleanupStale, InactiveDays: tc.days})
		if err != nil || a.EligibleRootThreads != tc.count {
			t.Fatalf("%d days: %+v %v", tc.days, a, err)
		}
	}
	parent := f.transcript(t, f.rootPath, "meta-parent", "", f.old)
	f.transcript(t, f.rootPath, "meta-parent", "worker", f.old)
	f.write(t, filepath.Join(strings.TrimSuffix(parent, ".jsonl"), "subagents", "agent-worker.meta.json"), []byte(`{"agentType":"general-purpose"}`), f.old)
	a := f.audit(t, CodexCleanupStale)
	if a.EligibleRootThreads != 5 || a.EligibleDescendants != 1 {
		t.Fatalf("metadata blocked valid tree: %+v", a)
	}
}

func TestClaudeCleanupDetachedChildProtectsParent(t *testing.T) {
	f := newClaudeCleanupFixture(t)
	f.transcript(t, f.rootPath, "parent", "", f.old)
	f.transcript(t, f.rootPath, "newest", "", time.Now())
	// An unexpected project-directory placement must not lose child ownership.
	f.transcript(t, f.rootPath+"-other", "parent", "detached", f.old)
	a := f.audit(t, CodexCleanupStale)
	if len(a.Groups) != 0 {
		t.Fatalf("detached child did not protect parent: %+v", a)
	}
}
