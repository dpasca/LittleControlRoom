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

	"lcroom/internal/config"
	"lcroom/internal/events"
	"lcroom/internal/model"
	"lcroom/internal/scanner"
	"lcroom/internal/store"
)

type assetResidueFixture struct {
	svc                       *Service
	st                        *store.Store
	root, path, other, commit string
	assets                    []string
}

func newAssetResidueFixture(t *testing.T) assetResidueFixture {
	return newRemovalFixture(t, []string{"TheFractalA", "TheFractalX", "TheRun", "TheRun2"})
}

func newRemovalFixture(t *testing.T, apps []string) assetResidueFixture {
	t.Helper()
	ctx := context.Background()
	parent := t.TempDir()
	parent, _ = filepath.EvalSymlinks(parent)
	f := assetResidueFixture{root: filepath.Join(parent, "repo"), path: filepath.Join(parent, "repo--task"), other: filepath.Join(parent, "repo--live")}
	initGitRepo(t, f.root)
	for _, app := range apps {
		rel := filepath.Join("Apps", app, "Assets")
		origin := filepath.Join(parent, "origin-"+app)
		initGitRepo(t, origin)
		runGit(t, f.root, "git", "-c", "protocol.file.allow=always", "submodule", "add", origin, rel)
		f.assets = append(f.assets, rel)
	}
	writeTestFile(t, filepath.Join(f.root, ".gitignore"), "_artifacts/\nApps/*/android/build/\n", 0600)
	runGit(t, f.root, "git", "add", ".")
	runGit(t, f.root, "git", "commit", "-m", "asset submodules and ignored output")
	f.commit = strings.TrimSpace(gitOutput(t, f.root, "git", "rev-parse", "HEAD"))
	runGit(t, f.root, "git", "worktree", "add", "-b", "task", f.path)
	runGit(t, f.root, "git", "worktree", "add", "-b", "live", f.other)
	for _, rel := range f.assets {
		for _, parent := range []string{f.path, f.other} {
			if err := os.Remove(filepath.Join(parent, rel)); err != nil {
				t.Fatal(err)
			}
			runGit(t, filepath.Join(f.root, rel), "git", "worktree", "add", "--detach", filepath.Join(parent, rel), "HEAD")
		}
	}
	writeTestFile(t, filepath.Join(f.path, "_artifacts", "capture.bin"), strings.Repeat("artifact", 1024), 0600)
	writeTestFile(t, filepath.Join(f.path, "Apps", "TheFractalX", "android", "build", "output.bin"), strings.Repeat("build", 1024), 0600)
	var err error
	f.st, err = store.Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.st.Close() })
	cfg := config.Default()
	cfg.IncludePaths = []string{parent}
	f.svc = New(cfg, f.st, events.NewBus(), nil)
	if _, err := f.svc.CreateOrAttachProject(ctx, CreateOrAttachProjectRequest{ParentPath: parent, Name: "repo"}); err != nil {
		t.Fatal(err)
	}
	if err := f.st.UpsertProjectState(ctx, model.ProjectState{Path: f.path, Name: "repo--task", PresentOnDisk: true, InScope: true, WorktreeRootPath: f.root, WorktreeKind: model.WorktreeKindLinked, WorktreeParentBranch: "master", RepoBranch: "task", WorktreeInitialBranch: "task", UpdatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f assetResidueFixture) orphan(t *testing.T, prune bool) {
	t.Helper()
	if err := os.Remove(filepath.Join(f.path, ".git")); err != nil {
		t.Fatal(err)
	}
	if prune {
		runGit(t, f.root, "git", "worktree", "prune", "--expire", "now")
	}
}

func (f assetResidueFixture) assertChildren(t *testing.T, removed bool) {
	t.Helper()
	for _, rel := range f.assets {
		registrations, err := scanner.ListGitWorktrees(context.Background(), filepath.Join(f.root, rel))
		if err != nil {
			t.Fatal(err)
		}
		foundTarget, foundLive, foundRoot := false, false, false
		for _, wt := range registrations {
			foundTarget = foundTarget || samePath(wt.Path, filepath.Join(f.path, rel))
			foundLive = foundLive || samePath(wt.Path, filepath.Join(f.other, rel))
			foundRoot = foundRoot || wt.IsMain
		}
		if foundTarget == removed || !foundLive || !foundRoot {
			t.Fatalf("unexpected child registrations: %#v", registrations)
		}
		if gitOutput(t, filepath.Join(f.other, rel), "git", "status", "--porcelain", "--untracked-files=all") != "" {
			t.Fatal("live child changed")
		}
	}
	if strings.TrimSpace(gitOutput(t, f.root, "git", "rev-parse", "task")) != f.commit {
		t.Fatal("parent branch changed")
	}
}

func TestAssetWorktreeRemovalModes(t *testing.T) {
	for _, mode := range []string{"normal", "force", "merge", "absent", "prunable"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			f := newAssetResidueFixture(t)
			// Dependency links may point within the checkout, outside it, or
			// nowhere. Neither Git removal nor retained cleanup may follow them.
			for name, target := range map[string]string{
				"internal": "capture.bin",
				"external": f.other,
				"dangling": "missing-package",
			} {
				if err := os.Symlink(target, filepath.Join(f.path, "_artifacts", name)); err != nil {
					t.Fatal(err)
				}
			}
			childRepo := filepath.Join(f.root, f.assets[0])
			exclude, err := gitPath(ctx, childRepo, "info/exclude")
			if err != nil {
				t.Fatal(err)
			}
			writeTestFile(t, exclude, "node_modules/\n", 0600)
			modules := filepath.Join(f.path, f.assets[0], "node_modules")
			if err := os.MkdirAll(modules, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(f.other, filepath.Join(modules, "dependency")); err != nil {
				t.Fatal(err)
			}
			if mode == "absent" || mode == "prunable" {
				// A sparse file exercises multi-GiB reporting without consuming
				// multi-GiB disk space or hashing ignored build output.
				file, err := os.OpenFile(filepath.Join(f.path, "_artifacts", "large.bin"), os.O_CREATE|os.O_WRONLY, 0600)
				if err != nil {
					t.Fatal(err)
				}
				if err := file.Truncate(3 << 30); err != nil {
					t.Fatal(err)
				}
				if err := file.Close(); err != nil {
					t.Fatal(err)
				}
				f.orphan(t, mode == "absent")
				err = f.svc.RemoveWorktree(ctx, f.path, false)
				var retained *RetainedWorktreeError
				if !errors.As(err, &retained) || retained.Bytes < 3<<30 {
					t.Fatalf("want sized retained result: %v", err)
				}
				f.assertChildren(t, false)
				if _, err := f.svc.ScanOnce(ctx); err != nil {
					t.Fatal(err)
				}
				inspection, err := f.svc.InspectOrphanedWorktree(ctx, f.path)
				if err != nil || inspection.Resolution != OrphanedWorktreeResolutionClearResidue || len(inspection.NestedWorktrees) != 4 {
					t.Fatalf("cleanup inspection: %#v, %v", inspection, err)
				}
				if err := f.svc.CleanupRetainedWorktree(ctx, f.path); err != nil {
					t.Fatal(err)
				}
			} else if mode == "merge" {
				result, err := f.svc.FinalizeMergedWorktree(ctx, f.path, FinalizeMergedWorktreeOptions{RemoveWorktree: true})
				if err != nil || !result.WorktreeRemoved {
					t.Fatalf("merge finalization: %#v, %v", result, err)
				}
			} else if err := f.svc.RemoveWorktree(ctx, f.path, mode == "force"); err != nil {
				t.Fatal(err)
			}
			f.assertChildren(t, true)
			if _, err := os.Lstat(f.path); !os.IsNotExist(err) {
				t.Fatalf("parent remains: %v", err)
			}
			detail, err := f.st.GetProjectDetail(ctx, f.path, 100)
			if err != nil || !detail.Summary.Forgotten || detail.Summary.PresentOnDisk {
				t.Fatalf("persisted completion: %#v, %v", detail.Summary, err)
			}
			provenance := false
			for _, event := range detail.RecentEvents {
				if event.Type == "worktree_removal_started" && strings.Contains(event.Payload, "TheFractalX") && strings.Contains(event.Payload, f.commit) {
					provenance = true
				}
			}
			if !provenance {
				t.Fatal("missing durable parent/child receipt")
			}
		})
	}
}

func TestAssetWorktreeRemovalProtectsUncertainData(t *testing.T) {
	for _, kind := range []string{"dirty child", "untracked child", "unrelated repo", "metadata symlink", "untracked parent", "dirty parent", "bare repo"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			f := newAssetResidueFixture(t)
			switch kind {
			case "dirty child":
				writeTestFile(t, filepath.Join(f.path, f.assets[0], "README.md"), "uncommitted", 0600)
			case "untracked child":
				writeTestFile(t, filepath.Join(f.path, f.assets[0], "source.txt"), "untracked", 0600)
			case "unrelated repo":
				initGitRepo(t, filepath.Join(f.path, "_artifacts", "unrelated"))
			case "bare repo":
				runGit(t, f.path, "git", "init", "--bare", filepath.Join(f.path, "_artifacts", "objects.git"))
			case "metadata symlink":
				if err := os.Symlink(filepath.Join(f.other, ".git"), filepath.Join(f.path, "_artifacts", ".git")); err != nil {
					t.Fatal(err)
				}
			case "untracked parent":
				writeTestFile(t, filepath.Join(f.path, "source.txt"), "untracked", 0600)
			case "dirty parent":
				writeTestFile(t, filepath.Join(f.path, "README.md"), "changed", 0600)
			}
			if kind != "untracked parent" && kind != "dirty parent" {
				if err := f.svc.RemoveWorktree(context.Background(), f.path, true); err == nil {
					t.Fatal("force bypassed nested safety")
				}
				f.assertChildren(t, false)
			}
			f.orphan(t, true)
			if err := f.svc.CleanupRetainedWorktree(context.Background(), f.path); err == nil {
				t.Fatal("unsafe cleanup accepted")
			}
			f.assertChildren(t, false)
		})
	}
}

func TestRetainedWorktreeRejectsActiveCWD(t *testing.T) {
	f := newAssetResidueFixture(t)
	f.orphan(t, true)
	cmd := exec.Command("sleep", "60")
	cmd.Dir = f.path
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	err := f.svc.CleanupRetainedWorktree(context.Background(), f.path)
	if err == nil || !strings.Contains(err.Error(), "processes still use") {
		t.Fatalf("active writer not blocked: %v", err)
	}
	f.assertChildren(t, false)
}

func TestOwnedResidualPlanRejectsChangedAncestorAndNewOutput(t *testing.T) {
	for _, mode := range []string{"ancestor", "new output", "changed file"} {
		t.Run(mode, func(t *testing.T) {
			f := newAssetResidueFixture(t)
			f.orphan(t, true)
			ctx := context.Background()
			plan, err := inspectRemovalPlan(ctx, f.root, f.path, f.commit, true)
			if err != nil {
				t.Fatal(err)
			}
			if err := removeOwnedChildren(ctx, plan); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "ancestor":
				if err := os.Rename(filepath.Join(f.path, "_artifacts"), filepath.Join(f.path, "saved")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(f.path, "saved"), filepath.Join(f.path, "_artifacts")); err != nil {
					t.Fatal(err)
				}
			case "new output":
				writeTestFile(t, filepath.Join(f.path, "_artifacts", "new.bin"), "new", 0600)
			case "changed file":
				writeTestFile(t, filepath.Join(f.path, "_artifacts", "capture.bin"), "changed", 0600)
			}
			err = removeInspectedResidualWorktreeDirectory(ctx, residualWorktreeInspection{Kind: ResidualWorktreeCleanupOwned, Safe: true, Plan: plan, Entries: plan.Entries}, f.path)
			if err == nil {
				t.Fatal("concurrent change silently deleted")
			}
			if _, err := os.Lstat(f.path); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestScanShowsOutputRecreatedAfterSuccessfulRemoval(t *testing.T) {
	f := newAssetResidueFixture(t)
	ctx := context.Background()
	if err := f.svc.RemoveWorktree(ctx, f.path, false); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(f.path, "_artifacts", "late.bin"), "late writer", 0600)
	if _, err := f.svc.ScanOnce(ctx); err != nil {
		t.Fatal(err)
	}
	directories, err := f.svc.ListOrphanedWorktreeDirectories(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := directories[f.path]; !ok {
		t.Fatal("recreated output hidden from orphaned list")
	}
}

func TestRetainedCleanupPermissionFailureIsPersistedAndRetryable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("requires normal filesystem permission enforcement")
	}
	f := newAssetResidueFixture(t)
	f.orphan(t, true)
	artifacts := filepath.Join(f.path, "_artifacts")
	if err := os.Chmod(artifacts, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(artifacts, 0700) })
	ctx := context.Background()
	err := f.svc.CleanupRetainedWorktree(ctx, f.path)
	var retained *RetainedWorktreeError
	if !errors.As(err, &retained) {
		t.Fatalf("want partial result: %v", err)
	}
	f.assertChildren(t, true)
	detail, err := f.st.GetProjectDetail(ctx, f.path, 20)
	if err != nil || !detail.Summary.Forgotten || !detail.Summary.PresentOnDisk {
		t.Fatalf("hidden partial failure: %#v %v", detail.Summary, err)
	}
	if _, err := os.Stat(filepath.Join(artifacts, "capture.bin")); err != nil {
		t.Fatal("retained output was lost", err)
	}
	if err := os.Chmod(artifacts, 0700); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.CleanupRetainedWorktree(ctx, f.path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(f.path); !os.IsNotExist(err) {
		t.Fatal("retry retained the parent", err)
	}
}

func TestChildRemovalRechecksLocksAfterPartialProgress(t *testing.T) {
	f := newAssetResidueFixture(t)
	f.orphan(t, true)
	ctx := context.Background()
	plan, err := inspectRemovalPlan(ctx, f.root, f.path, f.commit, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.svc.saveRemovalReceipt(ctx, plan); err != nil {
		t.Fatal(err)
	}
	child := plan.Children[1]
	runGit(t, child.Repository, "git", "worktree", "lock", child.Path)
	if err := removeOwnedChildren(ctx, plan); err == nil {
		t.Fatal("lock change was ignored")
	}
	if _, err := os.Lstat(plan.Children[0].Path); !os.IsNotExist(err) {
		t.Fatal("expected partial child progress", err)
	}
	if _, err := os.Lstat(child.Path); err != nil {
		t.Fatal("locked child lost", err)
	}
	runGit(t, child.Repository, "git", "worktree", "unlock", child.Path)
	if err := f.svc.CleanupRetainedWorktree(ctx, f.path); err != nil {
		t.Fatal(err)
	}
	f.assertChildren(t, true)
}

func TestRemovalRejectsRegisteredChildWithMissingPointer(t *testing.T) {
	f := newAssetResidueFixture(t)
	if err := os.Remove(filepath.Join(f.path, f.assets[0], ".git")); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.RemoveWorktree(context.Background(), f.path, true); err == nil || !strings.Contains(err.Error(), "nested registration") {
		t.Fatalf("missing child pointer must block force: %v", err)
	}
	f.assertChildren(t, false)
}

func TestPrunableOuterCannotErasePrivateModuleStore(t *testing.T) {
	f := newAssetResidueFixture(t)
	admin, err := removalGitOutput(context.Background(), f.path, "rev-parse", "--absolute-git-dir")
	if err != nil {
		t.Fatal(err)
	}
	objects := filepath.Join(admin, "modules", "private", "objects", "keep")
	writeTestFile(t, objects, "private objects must survive", 0600)
	f.orphan(t, false)
	result, err := f.svc.FinalizeMergedWorktree(context.Background(), f.path, FinalizeMergedWorktreeOptions{RemoveWorktree: true})
	if err == nil || result.WorktreeRemoved || !strings.Contains(err.Error(), "private submodule object store") {
		t.Fatalf("unexpected merge removal result: %#v %v", result, err)
	}
	if content, err := os.ReadFile(objects); err != nil || string(content) != "private objects must survive" {
		t.Fatalf("private store changed: %q %v", content, err)
	}
	f.assertChildren(t, false)
}

func TestRemovalProcessSummaries(t *testing.T) {
	out := "p3418\ncFinder\nftxt\nn/tmp/app/AppIcon.icns\np34786\ncpunderclass\nftxt\nn/tmp/app/punderclass\nftxt\nn/tmp/app/AppIcon.icns\n"
	got := (&WorktreeProcessesInUseError{Path: "/tmp/app", Processes: removalProcessSummaries(out)}).Error()
	for _, want := range []string{"Finder (PID 3418)", "punderclass (PID 34786)", "Close these apps/processes, then retry /clean"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %q", want, got)
		}
	}
	if strings.Contains(got, "AppIcon") || strings.Contains(got, "\n") || strings.Count(got, "34786") != 1 {
		t.Fatalf("raw or duplicate file records leaked: %q", got)
	}
	many := strings.Repeat("p12\ncworker\n", 8)
	if got := removalProcessSummaries(many); len(got) != 6 || got[5] != "3 more processes" {
		t.Fatalf("unbounded process summary: %v", got)
	}
}

func TestRemovalProcessProbeCancellation(t *testing.T) {
	bin := t.TempDir()
	writeTestFile(t, filepath.Join(bin, "lsof"), "#!/bin/sh\nexec sleep 60\n", 0700)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := checkRemovalProcesses(ctx, t.TempDir())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("probe lost cancellation: %v", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("stalled probe did not cancel promptly")
	}
}
