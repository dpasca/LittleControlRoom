package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lcroom/internal/worktreerecovery"
)

func TestRecoveryPreservesOwnedSubmodulePointerRepositories(t *testing.T) {
	f := newAssetResidueFixture(t)
	ctx := context.Background()
	for _, rel := range []string{"_artifacts", "Apps/TheFractalX/android"} {
		if err := os.RemoveAll(filepath.Join(f.path, rel)); err != nil {
			t.Fatal(err)
		}
	}
	if ignored := gitOutput(t, f.path, "git", "ls-files", "--others", "--ignored", "--exclude-standard"); strings.TrimSpace(ignored) != "" {
		t.Fatalf("fixture must exercise pointer-only discovery, got ignored files: %s", ignored)
	}
	if err := f.svc.RemoveWorktree(ctx, f.path, false); err != nil {
		t.Fatal(err)
	}
	r, err := f.svc.ReviewWorktreeRecovery(ctx, f.path)
	if err != nil || r == nil || !r.Verified {
		t.Fatalf("nested pointer repositories were not preserved: %#v %v", r, err)
	}
	f.assertChildren(t, true)
}

func TestRecoveryBlocksInvalidSharedSubmoduleMetadata(t *testing.T) {
	f := newAssetResidueFixture(t)
	sharedCheckout := filepath.Join(f.root, f.assets[0])
	sharedConfig := gitOutput(t, sharedCheckout, "git", "rev-parse", "--git-path", "config")
	sharedConfig = strings.TrimSpace(sharedConfig)
	if !filepath.IsAbs(sharedConfig) {
		sharedConfig = filepath.Join(sharedCheckout, sharedConfig)
	}
	runGit(t, sharedCheckout, "git", "config", "--local", "core.worktree", filepath.Join(f.root, "missing-checkout"))
	before, err := os.ReadFile(sharedConfig)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.svc.RemoveWorktree(context.Background(), f.path, false); err == nil {
		t.Fatal("invalid shared metadata was accepted")
	}
	after, err := os.ReadFile(sharedConfig)
	if err != nil || string(before) != string(after) {
		t.Fatalf("shared metadata changed: %v", err)
	}
	if _, err := os.Stat(f.path); err != nil {
		t.Fatal("source relocated despite incomplete ownership inspection")
	}
}

func TestRecoveryRemovalOfflineRetryAndIndependentRestore(t *testing.T) {
	f := newAssetResidueFixture(t)
	ctx := context.Background()
	_, cache, checkout := addRemovalPackageClone(t, f)
	runGit(t, cache, "git", "remote", "set-url", "origin", "https://127.0.0.1:1/offline")
	writeTestFile(t, filepath.Join(checkout, "local"), "preserve ignored dependency edits", 0755)
	branch := gitOutput(t, f.root, "git", "rev-parse", "refs/heads/task")
	if err := f.svc.RemoveWorktree(ctx, f.path, false); err != nil {
		t.Fatal(err)
	}
	r, err := f.svc.ReviewWorktreeRecovery(ctx, f.path)
	if err != nil || r == nil || !r.Verified {
		t.Fatalf("recovery=%#v err=%v", r, err)
	}
	bytes, err := worktreerecovery.Storage(r.Location)
	if err != nil || bytes != r.RetainedBytes {
		t.Fatalf("inaccurate retained bytes: %d / %d, %v", bytes, r.RetainedBytes, err)
	}
	if err := f.svc.RemoveWorktree(ctx, f.path, false); err != nil {
		t.Fatal(err)
	}
	entries, err := f.svc.ListWorktreeRecoveries(ctx)
	if err != nil || len(entries) != 1 {
		t.Fatalf("retry duplicated recovery: %#v %v", entries, err)
	}
	if got := gitOutput(t, f.root, "git", "rev-parse", "refs/heads/task"); got != branch {
		t.Fatal("branch changed")
	}
	destination := f.path + ".restored"
	if err := f.svc.RestoreWorktreeRecovery(ctx, f.path, destination); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(destination, "tree", ".build", "checkouts", "FluidAudio", "local")); err != nil || string(data) != "preserve ignored dependency edits" {
		t.Fatalf("lost nested data %q %v", data, err)
	}
	if err := f.svc.PurgeWorktreeRecovery(ctx, f.path, "Permanently delete "+r.Location); err != nil {
		t.Fatal(err)
	}
	for _, rel := range append([]string{"."}, f.assets...) {
		if out := gitOutput(t, filepath.Join(destination, "tree", rel), "git", "status", "--porcelain", "--untracked-files=no"); out != "" {
			t.Fatalf("restored worktree unusable at %s: %s", rel, out)
		}
	}
	f.assertChildren(t, true)
}

func TestRecoveryStopsForExternalConsumerAndActiveWriter(t *testing.T) {
	for _, kind := range []string{"alternate", "symlink", "writer"} {
		t.Run(kind, func(t *testing.T) {
			f := newRemovalFixture(t, nil)
			_, cache, checkout := addRemovalPackageClone(t, f)
			switch kind {
			case "alternate":
				runGit(t, f.root, "git", "clone", "--shared", cache, filepath.Join(filepath.Dir(f.root), "consumer"))
			case "symlink":
				if err := os.Symlink(filepath.Join(cache, "objects"), filepath.Join(filepath.Dir(f.root), "consumer")); err != nil {
					t.Fatal(err)
				}
			case "writer":
				file, err := os.OpenFile(filepath.Join(checkout, "README.md"), os.O_WRONLY, 0)
				if err != nil {
					t.Fatal(err)
				}
				defer file.Close()
			}
			if err := f.svc.RemoveWorktree(context.Background(), f.path, false); err == nil {
				t.Fatal("unsafe consumer/writer accepted")
			}
			if _, err := os.Stat(checkout); err != nil {
				t.Fatal("source changed on rejection")
			}
		})
	}
}

func TestRecoveryCollectsMultipleBlockers(t *testing.T) {
	f := newRemovalFixture(t, nil)
	_, cache, checkout := addRemovalPackageClone(t, f)
	writeTestFile(t, filepath.Join(cache, "packed-refs.lock"), "", 0600)
	writeTestFile(t, filepath.Join(checkout, ".git", "index.lock"), "", 0600)
	err := f.svc.RemoveWorktree(context.Background(), f.path, false)
	if err == nil || !strings.Contains(err.Error(), "packed-refs.lock") || !strings.Contains(err.Error(), "index.lock") {
		t.Fatalf("not all blockers reported: %v", err)
	}
}

func TestRecoveryServiceResumesAfterRelocation(t *testing.T) {
	f := newRemovalFixture(t, nil)
	addRemovalPackageClone(t, f)
	ctx := context.Background()
	j, err := worktreerecovery.Prepare(ctx, f.svc.recoveryBase(), f.root, f.path)
	if err != nil {
		t.Fatal(err)
	}
	if err := j.Relocate(ctx); err != nil {
		t.Fatal(err)
	}
	candidate, reason, err := f.svc.RevalidateStaleWorktreeCleanupCandidate(ctx, f.path, time.Now())
	if err != nil || reason != "" || !candidate.RecoveryResume {
		t.Fatalf("retry lost operation: %#v %s %v", candidate, reason, err)
	}
	if err := f.svc.RemoveWorktree(ctx, f.path, false); err != nil {
		t.Fatal(err)
	}
	r, err := f.svc.ReviewWorktreeRecovery(ctx, f.path)
	if err != nil || r.Phase != "removed" {
		t.Fatalf("resume incomplete: %#v %v", r, err)
	}
}

func TestRecoveryRefusesAnUnrelatedOuterPointerBeforeRelocation(t *testing.T) {
	f := newRemovalFixture(t, nil)
	pointer, err := os.ReadFile(filepath.Join(f.other, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.path, ".git"), pointer, 0600); err != nil {
		t.Fatal(err)
	}
	err = f.svc.RemoveWorktree(context.Background(), f.path, true)
	if err == nil || !strings.Contains(err.Error(), "another checkout") {
		t.Fatalf("unrelated pointer accepted: %v", err)
	}
	for _, path := range []string{f.path, f.other} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("unrelated checkout relocated: %s %v", path, err)
		}
	}
}

func TestRecoveryMissingManifestCannotFallThroughToDeletion(t *testing.T) {
	f := newRemovalFixture(t, nil)
	directory := worktreerecovery.Location(f.svc.recoveryBase(), f.path)
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	err := f.svc.RemoveWorktree(context.Background(), f.path, false)
	if err == nil || !strings.Contains(err.Error(), "incomplete recovery journal") {
		t.Fatalf("incomplete journal bypassed: %v", err)
	}
	if _, err := os.Stat(f.path); err != nil {
		t.Fatal("source was removed")
	}
}
