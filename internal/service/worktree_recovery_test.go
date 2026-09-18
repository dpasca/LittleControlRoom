package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lcroom/internal/events"
	"lcroom/internal/model"
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

func TestRecoveryConsumerScanDeduplicatesRootsAndResolvesAliases(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "target")
	consumer := filepath.Join(base, "consumer")
	outside := t.TempDir()
	for _, path := range []string{target, consumer} {
		if err := os.MkdirAll(path, 0755); err != nil {
			t.Fatal(err)
		}
	}
	writeTestFile(t, filepath.Join(consumer, ".git"), "gitdir: ../target\n", 0600)
	alias := filepath.Join(base, "alias")
	if err := os.Symlink(outside, alias); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(outside, "commondir"), target, 0600)
	// Resolving the explicit symlink root must retain the external tree even
	// though the link itself sits beneath the already-covered parent.
	err := inspectRecoveryConsumers(context.Background(), target, filepath.Join(base, "recoveries"), []string{consumer, base, consumer, alias})
	if err == nil || strings.Count(err.Error(), "external Git metadata consumer") != 2 {
		t.Fatalf("expected two unique consumers, got %v", err)
	}
}

func TestRecoveryConsumerScanCancellationAndDeadline(t *testing.T) {
	base := t.TempDir()
	for _, deadline := range []bool{false, true} {
		var ctx context.Context
		var cancel context.CancelFunc
		if deadline {
			ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
		} else {
			ctx, cancel = context.WithCancel(context.Background())
			cancel()
		}
		defer cancel()
		err := inspectRecoveryConsumers(ctx, filepath.Join(base, "target"), filepath.Join(base, "recoveries"), []string{base, base + "-missing", base + "-also-missing"})
		var canceled *WorktreeConsumerScanCanceledError
		if deadline {
			if !errors.Is(err, context.DeadlineExceeded) || errors.As(err, &canceled) {
				t.Fatalf("deadline was treated as cancellation: %v", err)
			}
		} else if !errors.As(err, &canceled) || !errors.Is(err, context.Canceled) || strings.Contains(err.Error(), "\n") {
			t.Fatalf("cancellation should be one typed result: %v", err)
		}
	}
}

func TestRecoveryConsumerScanSkipsMissingRootsButKeepsLiveConsumers(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "target")
	consumer := filepath.Join(base, "consumer")
	missing := filepath.Join(base, "missing")
	if err := os.Mkdir(target, 0755); err != nil {
		t.Fatal(err)
	}
	if err := inspectRecoveryConsumers(context.Background(), target, filepath.Join(base, "recoveries"), []string{missing}); err != nil {
		t.Fatalf("stale missing project blocked inspection: %v", err)
	}
	if err := os.Symlink(target, consumer); err != nil {
		t.Fatal(err)
	}
	err := inspectRecoveryConsumers(context.Background(), target, filepath.Join(base, "recoveries"), []string{missing, consumer})
	if err == nil || !strings.Contains(err.Error(), "external symbolic-link consumer") || strings.Contains(err.Error(), "inspection incomplete") {
		t.Fatalf("expected only the live consumer blocker: %v", err)
	}
}

func TestRecoveryConsumerScanKeepsPermissionFailures(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read mode-000 directories")
	}
	base := t.TempDir()
	blocked := filepath.Join(base, "blocked")
	if err := os.Mkdir(blocked, 0000); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(blocked, 0755)
	err := inspectRecoveryConsumers(context.Background(), filepath.Join(base, "target"), filepath.Join(base, "recoveries"), []string{blocked})
	if err == nil || !errors.Is(err, os.ErrPermission) || !strings.Contains(err.Error(), "inspection incomplete") {
		t.Fatalf("unreadable existing directory must still block removal: %v", err)
	}
}

func TestRecoveryConsumerScanCancellationDuringProgressStopsRemainingRoots(t *testing.T) {
	base := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reports := 0
	err := inspectRecoveryConsumersWithProgress(ctx, filepath.Join(base, "target"), filepath.Join(base, "recoveries"), []string{base, t.TempDir()}, func(_ string, _ int64) {
		reports++
		cancel()
	})
	var canceled *WorktreeConsumerScanCanceledError
	if !errors.As(err, &canceled) || reports != 1 || strings.Contains(err.Error(), "\n") {
		t.Fatalf("cancellation did not stop the scan: reports=%d err=%v", reports, err)
	}
}

func TestRecoveryRemovalIgnoresStaleMissingProjectAndPublishesProgress(t *testing.T) {
	f := newRemovalFixture(t, nil)
	missing := filepath.Join(t.TempDir(), "no-longer-present")
	if err := f.st.UpsertProjectState(context.Background(), model.ProjectState{Path: missing, Name: "stale", PresentOnDisk: true, InScope: true, UpdatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	stream, unsubscribe := f.svc.bus.Subscribe(64)
	defer unsubscribe()
	if err := f.svc.RemoveWorktree(context.Background(), f.path, false); err != nil {
		t.Fatalf("stale unrelated project blocked removal: %v", err)
	}
	if _, err := os.Lstat(f.path); !os.IsNotExist(err) {
		t.Fatalf("checkout still present: %v", err)
	}
	recovery, err := f.svc.ReviewWorktreeRecovery(context.Background(), f.path)
	if err != nil || recovery == nil || !recovery.Verified {
		t.Fatalf("ignored files not preserved: %#v %v", recovery, err)
	}
	var details []string
	for len(stream) > 0 {
		event := <-stream
		if event.Type == events.WorktreeRemovalProgress && event.ProjectPath == f.path {
			details = append(details, event.Payload["detail"])
		}
	}
	if text := strings.Join(details, "\n"); !strings.Contains(text, "Checking external consumers") || !strings.Contains(text, "Preparing and verifying local recovery") || !strings.Contains(text, "Removing preserved worktree registrations") {
		t.Fatalf("missing removal progress: %v", details)
	}
}

func TestRecoveryConsumerScanRetainsExternalRootSymlink(t *testing.T) {
	target := t.TempDir()
	external := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(target, external); err != nil {
		t.Fatal(err)
	}
	err := inspectRecoveryConsumers(context.Background(), target, filepath.Join(t.TempDir(), "recoveries"), []string{external})
	if err == nil || !strings.Contains(err.Error(), "external symbolic-link consumer") {
		t.Fatalf("resolving a root hid an external reference: %v", err)
	}
}

func TestRecoveryConsumerScopeChecksMetadataWithoutWalkingBuilds(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "target")
	project := filepath.Join(base, "project")
	alias := filepath.Join(base, "alias")
	recoveries := filepath.Join(base, "recoveries")
	writeTestFile(t, filepath.Join(project, "build", "info", "alternates"), target, 0600)
	writeTestFile(t, filepath.Join(project, ".git", "objects", "ab", "commondir"), target, 0600)
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(project, alias); err != nil {
		t.Fatal(err)
	}
	roots, err := recoveryConsumerMetadataRoots(context.Background(), target, recoveries, []string{project, alias, target, filepath.Join(base, "missing")})
	if err != nil {
		t.Fatal(err)
	}
	if err := inspectRecoveryConsumers(context.Background(), target, recoveries, roots); err != nil {
		t.Fatalf("working files or object payloads were scanned: %v", err)
	}
	writeTestFile(t, filepath.Join(project, ".git", "objects", "info", "alternates"), target, 0600)
	if err := inspectRecoveryConsumers(context.Background(), target, recoveries, roots); err == nil || !strings.Contains(err.Error(), "external object consumer") {
		t.Fatalf("real alternate not detected: %v", err)
	}
}

func TestRecoveryConsumerScopeFollowsExternalGitCommonDirectory(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "target")
	project := filepath.Join(base, "project")
	common := filepath.Join(base, "separate-common")
	admin := filepath.Join(common, "worktrees", "project")
	writeTestFile(t, filepath.Join(project, ".git"), "gitdir: "+admin, 0600)
	writeTestFile(t, filepath.Join(admin, "commondir"), "../..", 0600)
	writeTestFile(t, filepath.Join(common, "objects", "info", "alternates"), target, 0600)
	roots, err := recoveryConsumerMetadataRoots(context.Background(), target, filepath.Join(base, "recoveries"), []string{project})
	if err != nil {
		t.Fatal(err)
	}
	if err := inspectRecoveryConsumers(context.Background(), target, filepath.Join(base, "recoveries"), roots); err == nil || !strings.Contains(err.Error(), "external object consumer") {
		t.Fatalf("external shared metadata not inspected: %v", err)
	}
}

func BenchmarkRecoveryConsumerScope(b *testing.B) {
	base := b.TempDir()
	project := filepath.Join(base, "project")
	build := filepath.Join(project, "build")
	if err := os.MkdirAll(build, 0700); err != nil {
		b.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(project, ".git"), 0700); err != nil {
		b.Fatal(err)
	}
	for n := 0; n < 4000; n++ {
		if err := os.WriteFile(filepath.Join(build, fmt.Sprintf("artifact-%d", n)), nil, 0600); err != nil {
			b.Fatal(err)
		}
	}
	target, recoveries := filepath.Join(base, "target"), filepath.Join(base, "recoveries")
	for _, scope := range []string{"parent-tree", "git-metadata"} {
		b.Run(scope, func(b *testing.B) {
			for n := 0; n < b.N; n++ {
				roots := []string{base}
				if scope == "git-metadata" {
					var err error
					roots, err = recoveryConsumerMetadataRoots(context.Background(), target, recoveries, []string{project})
					if err != nil {
						b.Fatal(err)
					}
				}
				if err := inspectRecoveryConsumers(context.Background(), target, recoveries, roots); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
