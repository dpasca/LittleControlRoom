package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func addRemovalCodexCache(t *testing.T, f assetResidueFixture) string {
	t.Helper()
	upstream := filepath.Join(filepath.Dir(f.root), "plugin-upstream")
	initGitRepo(t, upstream)
	head := strings.TrimSpace(gitOutput(t, upstream, "git", "rev-parse", "HEAD"))
	cache := filepath.Join(f.path, "dist", "image-review-validation", "internal-workspaces", "lcroom-codex-home-123", ".tmp", "plugins")
	if err := os.MkdirAll(cache, 0700); err != nil {
		t.Fatal(err)
	}
	runGit(t, cache, "git", "init")
	runGit(t, cache, "git", "fetch", "--depth=1", "file://"+upstream, head)
	runGit(t, cache, "git", "checkout", "-B", "master", "FETCH_HEAD")
	runGit(t, cache, "git", "update-ref", "refs/codex/curated-sync", head)
	// Reproduce the recorded production fetch without a network dependency.
	writeTestFile(t, filepath.Join(cache, ".git", "FETCH_HEAD"), head+"\t\t'"+head+"' of https://github.com/openai/plugins\n", 0600)
	exclude, err := gitPath(context.Background(), f.root, "info/exclude")
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, exclude, "dist/\n", 0600)
	runGit(t, cache, "git", "config", "user.name", "Test")
	runGit(t, cache, "git", "config", "user.email", "test@example.com")
	return cache
}

func TestRemovalWithCodexPluginCache(t *testing.T) {
	for _, mode := range []string{"normal", "force", "merge", "retained"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			f := newAssetResidueFixture(t)
			cache := addRemovalCodexCache(t, f)
			ctx := context.Background()
			plan, err := inspectRemovalPlan(ctx, f.root, f.path, f.commit, false)
			if err != nil || len(plan.Clones) != 1 {
				t.Fatalf("inspection: %#v, %v", plan.Clones, err)
			}
			if plan.Clones[0].Upstream != "https://github.com/openai/plugins" {
				t.Fatal("missing cache source in receipt")
			}
			if err := validateRemovalClones(ctx, plan); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "merge":
				_, err = f.svc.FinalizeMergedWorktree(ctx, f.path, FinalizeMergedWorktreeOptions{RemoveWorktree: true})
			case "retained":
				f.orphan(t, true)
				err = f.svc.archiveWorktree(ctx, f.path, false, true)
			default:
				err = f.svc.ArchiveWorktree(ctx, f.path, mode == "force")
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Lstat(f.path); !os.IsNotExist(err) {
				t.Fatalf("checkout retained: %v", err)
			}
			if _, err := os.Lstat(cache); !os.IsNotExist(err) {
				t.Fatalf("cache retained: %v", err)
			}
			f.assertChildren(t, true)
		})
	}
}

func TestRemovalCodexCacheProtectsLocalWork(t *testing.T) {
	for _, kind := range []string{"dirty", "untracked", "local commit", "reflog", "dangling blob", "tag", "stash", "missing sync ref", "missing shallow", "missing fetch", "different source", "not ignored"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			f := newRemovalFixture(t, nil)
			cache := addRemovalCodexCache(t, f)
			readme := filepath.Join(cache, "README.md")
			switch kind {
			case "dirty":
				writeTestFile(t, readme, "local edits", 0600)
			case "untracked":
				writeTestFile(t, filepath.Join(cache, "local.txt"), "local edits", 0600)
			case "local commit", "reflog":
				writeTestFile(t, readme, "local commit", 0600)
				runGit(t, cache, "git", "commit", "-am", "local only")
				if kind == "reflog" {
					runGit(t, cache, "git", "reset", "--hard", "HEAD~1")
				}
			case "dangling blob":
				writeTestFile(t, readme, "staged then discarded", 0600)
				runGit(t, cache, "git", "add", "README.md")
				runGit(t, cache, "git", "reset", "--hard", "HEAD")
			case "tag":
				runGit(t, cache, "git", "tag", "-a", "local", "-m", "local annotation")
			case "stash":
				writeTestFile(t, readme, "stashed edits", 0600)
				runGit(t, cache, "git", "stash")
			case "missing sync ref":
				runGit(t, cache, "git", "update-ref", "-d", "refs/codex/curated-sync")
			case "missing shallow":
				if err := os.Remove(filepath.Join(cache, ".git", "shallow")); err != nil {
					t.Fatal(err)
				}
			case "missing fetch":
				if err := os.Remove(filepath.Join(cache, ".git", "FETCH_HEAD")); err != nil {
					t.Fatal(err)
				}
			case "different source":
				head := strings.TrimSpace(gitOutput(t, cache, "git", "rev-parse", "HEAD"))
				writeTestFile(t, filepath.Join(cache, ".git", "FETCH_HEAD"), head+"\t\t'"+head+"' of file:///local-work\n", 0600)
			case "not ignored":
				exclude, _ := gitPath(context.Background(), f.root, "info/exclude")
				writeTestFile(t, exclude, "", 0600)
			}
			before, err := os.ReadFile(readme)
			if err != nil {
				t.Fatal(err)
			}
			reflog, _ := os.ReadFile(filepath.Join(cache, ".git", "logs", "HEAD"))
			if err := f.svc.ArchiveWorktree(context.Background(), f.path, true); err != nil {
				t.Fatal(err)
			}
			recovery, err := f.svc.ReviewWorktreeRecovery(context.Background(), f.path)
			if err != nil || recovery == nil || !recovery.Verified {
				t.Fatalf("local data was not verifiably preserved: %#v %v", recovery, err)
			}
			rel, _ := filepath.Rel(f.path, cache)
			saved := filepath.Join(recovery.Location, "tree", rel)
			after, err := os.ReadFile(filepath.Join(saved, "README.md"))
			if err != nil || string(before) != string(after) {
				t.Fatalf("working data changed: %v", err)
			}
			savedLog, err := os.ReadFile(filepath.Join(saved, ".git", "logs", "HEAD"))
			if err != nil || string(reflog) != string(savedLog) {
				t.Fatalf("reflog changed: %v", err)
			}
			if _, err := os.Stat(cache); !os.IsNotExist(err) {
				t.Fatalf("selected cache path remains: %v", err)
			}
			f.assertChildren(t, true)
		})
	}
}

func TestArchiveWithEmptyNestedRepository(t *testing.T) {
	for _, kind := range []string{"bare", "checkout", "incomplete object", "untracked file", "staged file", "dangling object"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			f := newRemovalFixture(t, nil)
			cache := filepath.Join(f.path, "dist", "empty-git")
			if err := os.MkdirAll(cache, 0700); err != nil {
				t.Fatal(err)
			}
			metadata := filepath.Join(cache, ".git")
			if kind == "bare" {
				runGit(t, cache, "git", "init", "--bare")
				metadata = cache
			} else {
				runGit(t, cache, "git", "init")
			}
			exclude, err := gitPath(context.Background(), f.root, "info/exclude")
			if err != nil {
				t.Fatal(err)
			}
			writeTestFile(t, exclude, "dist/\n", 0600)
			switch kind {
			case "incomplete object":
				writeTestFile(t, filepath.Join(metadata, "objects", "tmp_pack"), "unfinished data", 0600)
			case "untracked file", "staged file", "dangling object":
				file := filepath.Join(cache, "local.txt")
				writeTestFile(t, file, "keep local data", 0600)
				if kind != "untracked file" {
					runGit(t, cache, "git", "add", "local.txt")
				}
				if kind == "dangling object" {
					runGit(t, cache, "git", "rm", "--cached", "local.txt")
					if err := os.Remove(file); err != nil {
						t.Fatal(err)
					}
				}
			}
			err = f.svc.ArchiveWorktree(context.Background(), f.path, false)
			if err != nil {
				t.Fatal(err)
			}
			recovery, err := f.svc.ReviewWorktreeRecovery(context.Background(), f.path)
			if err != nil || recovery == nil || !recovery.Verified {
				t.Fatalf("unborn repository not preserved: %#v %v", recovery, err)
			}
			rel, _ := filepath.Rel(f.path, cache)
			saved := filepath.Join(recovery.Location, "tree", rel)
			if kind == "incomplete object" {
				data, err := os.ReadFile(filepath.Join(saved, ".git", "objects", "tmp_pack"))
				if err != nil || string(data) != "unfinished data" {
					t.Fatalf("opaque object data lost: %q %v", data, err)
				}
			}
			if _, err := os.Lstat(f.path); !os.IsNotExist(err) {
				t.Fatalf("checkout remains: %v", err)
			}
		})
	}
}
