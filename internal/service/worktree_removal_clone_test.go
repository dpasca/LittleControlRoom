package service

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Reproduce SwiftPM's layout without needing Swift or network access: a bare
// cache plus a detached --shared checkout whose origin is that in-tree cache.
func addRemovalPackageClone(t *testing.T, f assetResidueFixture) (string, string, string) {
	t.Helper()
	origin := filepath.Join(filepath.Dir(f.root), "package-upstream")
	initGitRepo(t, origin)
	runGit(t, origin, "git", "tag", "-a", "v1", "-m", "published package")
	cache := filepath.Join(f.path, ".build", "repositories", "FluidAudio-cache")
	checkout := filepath.Join(f.path, ".build", "checkouts", "FluidAudio")
	runGit(t, f.root, "git", "clone", "--bare", origin, cache)
	runGit(t, f.root, "git", "clone", "--shared", cache, checkout)
	runGit(t, checkout, "git", "checkout", "--detach", "HEAD")
	// Ignore evidence must be supplied by the parent, not the nested clone.
	exclude, err := gitPath(context.Background(), f.root, "info/exclude")
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, exclude, ".build/\n", 0600)
	return origin, cache, checkout
}

func TestRemovalWithIgnoredPackageClones(t *testing.T) {
	for _, mode := range []string{"normal", "force", "merge", "retained"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			f := newAssetResidueFixture(t)
			origin, _, _ := addRemovalPackageClone(t, f)
			ctx := context.Background()
			plan, err := inspectRemovalPlan(ctx, f.root, f.path, f.commit, false)
			if err != nil || len(plan.Clones) != 2 {
				t.Fatalf("package inventory = %#v, %v", plan.Clones, err)
			}
			if err := validateRemovalClones(ctx, plan); err != nil {
				t.Fatal(err)
			}
			if mode == "retained" {
				f.orphan(t, true)
				err = f.svc.CleanupRetainedWorktree(ctx, f.path)
			} else if mode == "merge" {
				_, err = f.svc.FinalizeMergedWorktree(ctx, f.path, FinalizeMergedWorktreeOptions{RemoveWorktree: true})
			} else {
				err = f.svc.RemoveWorktree(ctx, f.path, mode == "force")
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Lstat(f.path); !os.IsNotExist(err) {
				t.Fatalf("worktree still present: %v", err)
			}
			f.assertChildren(t, true)
			if gitOutput(t, origin, "git", "status", "--porcelain") != "" {
				t.Fatal("upstream was changed")
			}
			detail, err := f.st.GetProjectDetail(ctx, f.path, 100)
			if err != nil {
				t.Fatal(err)
			}
			receipt := false
			for _, event := range detail.RecentEvents {
				if event.Type == "worktree_removal_started" && strings.Contains(event.Payload, "FluidAudio") && strings.Contains(event.Payload, "refs/tags/v1") {
					receipt = true
				}
			}
			if !receipt {
				t.Fatal("clone refs missing from durable removal receipt")
			}
		})
	}
}

func TestRemovalPackageCloneProtection(t *testing.T) {
	for _, kind := range []string{"dirty", "untracked", "ignored", "local commit", "stash", "reflog", "dangling blob", "local tag", "cache commit", "no remote", "unavailable remote", "origin cycle", "linked worktree", "metadata symlink", "lock", "assume unchanged", "not ignored", "local hook"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			f := newRemovalFixture(t, nil)
			origin, cache, checkout := addRemovalPackageClone(t, f)
			runGit(t, checkout, "git", "config", "user.email", "test@example.com")
			runGit(t, checkout, "git", "config", "user.name", "Test")
			readme := filepath.Join(checkout, "README.md")
			switch kind {
			case "dirty":
				writeTestFile(t, readme, "local changes", 0600)
			case "untracked":
				writeTestFile(t, filepath.Join(checkout, "local.txt"), "keep", 0600)
			case "ignored":
				writeTestFile(t, filepath.Join(checkout, ".git", "info", "exclude"), "local.txt\n", 0600)
				writeTestFile(t, filepath.Join(checkout, "local.txt"), "keep", 0600)
			case "local commit", "reflog":
				writeTestFile(t, readme, "local commit", 0600)
				runGit(t, checkout, "git", "add", "README.md")
				runGit(t, checkout, "git", "commit", "-m", "local only")
				if kind == "reflog" {
					runGit(t, checkout, "git", "reset", "--hard", "HEAD~1")
				}
			case "stash":
				writeTestFile(t, readme, "stashed changes", 0600)
				runGit(t, checkout, "git", "stash")
			case "dangling blob":
				writeTestFile(t, readme, "staged then discarded", 0600)
				runGit(t, checkout, "git", "add", "README.md")
				runGit(t, checkout, "git", "reset", "--hard", "HEAD")
			case "local tag":
				runGit(t, checkout, "git", "tag", "-a", "local", "-m", "unpublished annotation")
			case "cache commit":
				writeTestFile(t, filepath.Join(origin, "README.md"), "cache only", 0600)
				runGit(t, origin, "git", "commit", "-am", "later deleted upstream")
				runGit(t, cache, "git", "fetch", "origin", "master:refs/heads/master")
				runGit(t, origin, "git", "reset", "--hard", "HEAD~1")
			case "no remote":
				runGit(t, checkout, "git", "remote", "remove", "origin")
			case "unavailable remote":
				runGit(t, cache, "git", "remote", "set-url", "origin", filepath.Join(f.root, "missing"))
			case "origin cycle":
				runGit(t, cache, "git", "remote", "set-url", "origin", checkout)
			case "linked worktree":
				runGit(t, cache, "git", "worktree", "add", "--detach", filepath.Join(filepath.Dir(f.root), "external-package"), "HEAD")
			case "metadata symlink":
				if err := os.Symlink(origin, filepath.Join(checkout, ".git", "outside")); err != nil {
					t.Fatal(err)
				}
			case "lock":
				writeTestFile(t, filepath.Join(checkout, ".git", "index.lock"), "", 0600)
			case "assume unchanged":
				runGit(t, checkout, "git", "update-index", "--assume-unchanged", "README.md")
				writeTestFile(t, readme, "hidden edits", 0600)
			case "not ignored":
				exclude, _ := gitPath(context.Background(), f.root, "info/exclude")
				writeTestFile(t, exclude, "", 0600)
			case "local hook":
				writeTestFile(t, filepath.Join(checkout, ".git", "hooks", "pre-commit"), "#!/bin/sh\nexit 0\n", 0700)
			}
			ctx := context.Background()
			if err := f.svc.RemoveWorktree(ctx, f.path, true); err == nil {
				t.Fatal("force discarded an unverified package repository")
			}
			f.assertChildren(t, false)
			f.orphan(t, true)
			if err := f.svc.CleanupRetainedWorktree(ctx, f.path); err == nil {
				t.Fatal("retained cleanup discarded an unverified package repository")
			}
			if _, err := os.Stat(filepath.Join(checkout, ".git")); err != nil {
				t.Fatalf("package metadata was lost: %v", err)
			}
		})
	}
}

func TestRemovalCloneRevalidatesSnapshot(t *testing.T) {
	for _, change := range []string{"new file", "new object", "changed file", "missing file", "metadata replaced"} {
		t.Run(change, func(t *testing.T) {
			t.Parallel()
			f := newRemovalFixture(t, nil)
			_, _, checkout := addRemovalPackageClone(t, f)
			ctx := context.Background()
			plan, err := inspectRemovalPlan(ctx, f.root, f.path, f.commit, true)
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "new file":
				writeTestFile(t, filepath.Join(checkout, "new.txt"), "keep", 0600)
			case "new object":
				writeTestFile(t, filepath.Join(checkout, ".git", "objects", "new"), "keep", 0600)
			case "changed file":
				writeTestFile(t, filepath.Join(checkout, "README.md"), "changed", 0600)
			case "missing file":
				if err := os.Remove(filepath.Join(checkout, "README.md")); err != nil {
					t.Fatal(err)
				}
			case "metadata replaced":
				metadata := filepath.Join(checkout, ".git")
				if err := os.Rename(metadata, metadata+"-saved"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(metadata+"-saved", metadata); err != nil {
					t.Fatal(err)
				}
			}
			if err := removeOwnedChildren(ctx, plan); err == nil {
				t.Fatal("mutation proceeded after clone contents changed")
			}
			f.assertChildren(t, false)
		})
	}
}

func TestRemovalCloneRejectsUpstreamBorrowingRemovedObjects(t *testing.T) {
	f := newRemovalFixture(t, nil)
	_, cache, checkout := addRemovalPackageClone(t, f)
	external := filepath.Join(filepath.Dir(f.root), "borrowed-upstream")
	runGit(t, f.root, "git", "clone", "--shared", cache, external)
	runGit(t, checkout, "git", "remote", "set-url", "origin", external)
	_, err := inspectRemovalPlan(context.Background(), f.root, f.path, f.commit, false)
	if err == nil || !strings.Contains(err.Error(), "borrows objects") {
		t.Fatalf("dependent upstream was accepted: %v", err)
	}
}

func TestRemovalCloneProtectsTrackedParentSourceDespiteIgnore(t *testing.T) {
	f := newRemovalFixture(t, nil)
	_, _, checkout := addRemovalPackageClone(t, f)
	// Parent tree evidence takes precedence even when ignore rules cover it.
	plan := worktreeRemovalPlan{RootPath: f.root, Path: f.path, ResolvedPath: f.path}
	tree := map[string]residualGitTreeEntry{
		".build/checkouts/FluidAudio/README.md": {Mode: "100644", Type: "blob"},
	}
	_, err := inspectRemovalClone(context.Background(), plan, checkout, tree)
	if err == nil || !strings.Contains(err.Error(), "preserved parent source") {
		t.Fatalf("tracked parent source accepted: %v", err)
	}
}

func TestRemovalCloneUpstreamProbeCancellation(t *testing.T) {
	f := newRemovalFixture(t, nil)
	addRemovalPackageClone(t, f)
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	marker := filepath.Join(bin, "remote-started")
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }
	writeTestFile(t, filepath.Join(bin, "git"), "#!/bin/sh\nfor arg do\nif [ \"$arg\" = ls-remote ]; then\n: > "+quote(marker)+"\nexec sleep 60\nfi\ndone\nexec "+quote(git)+" \"$@\"\n", 0700)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := inspectRemovalPlan(ctx, f.root, f.path, f.commit, false)
		done <- err
	}()
	deadline := time.After(15 * time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case err := <-done:
			t.Fatalf("inspection returned before remote probe: %v", err)
		case <-deadline:
			t.Fatal("remote probe did not start")
		case <-ticker.C:
			if _, err := os.Stat(marker); err != nil {
				continue
			}
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("probe lost cancellation: %v", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("cancelled remote probe did not return promptly")
			}
			if _, err := os.Stat(f.path); err != nil {
				t.Fatal("cancelled inspection mutated the worktree")
			}
			return
		}
	}
}

func TestRemovalCloneRejectsUpstreamInOuterAdministration(t *testing.T) {
	f := newRemovalFixture(t, nil)
	origin, _, checkout := addRemovalPackageClone(t, f)
	admin, err := removalGitOutput(context.Background(), f.path, "rev-parse", "--absolute-git-dir")
	if err != nil {
		t.Fatal(err)
	}
	transient := filepath.Join(admin, "package-cache")
	runGit(t, f.root, "git", "clone", "--bare", origin, transient)
	runGit(t, checkout, "git", "remote", "set-url", "origin", transient)
	_, err = inspectRemovalPlan(context.Background(), f.root, f.path, f.commit, false)
	if err == nil || !strings.Contains(err.Error(), "selected worktree's object store") {
		t.Fatalf("upstream erased with outer registration was accepted: %v", err)
	}
}
