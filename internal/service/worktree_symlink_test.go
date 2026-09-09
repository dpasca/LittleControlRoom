package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lcroom/internal/model"
)

func createSymlinkWorktree(t *testing.T) (root, path, commit, outside string) {
	t.Helper()
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root, path, outside = filepath.Join(parent, "repo"), filepath.Join(parent, "repo--task"), filepath.Join(parent, "outside")
	initGitRepo(t, root)
	writeTestFile(t, filepath.Join(outside, "keep.txt"), "must survive", 0600)
	writeTestFile(t, filepath.Join(root, ".gitignore"), "node_modules/\n", 0600)
	if err := os.Symlink("../outside", filepath.Join(root, "shortcut")); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "git", "add", ".")
	runGit(t, root, "git", "commit", "-m", "tracked link and ignored dependencies")
	commit = strings.TrimSpace(gitOutput(t, root, "git", "rev-parse", "HEAD"))
	runGit(t, root, "git", "worktree", "add", "-b", "task", path)
	modules := filepath.Join(path, "node_modules", ".pnpm", "@types+chai@5.2.3", "node_modules", "@types")
	writeTestFile(t, filepath.Join(modules, "package", "index.d.ts"), "dependency", 0600)
	for name, target := range map[string]string{
		"deep-eql": "package",
		"external": outside,
		"dangling": "missing-package",
	} {
		if err := os.Symlink(target, filepath.Join(modules, name)); err != nil {
			t.Fatal(err)
		}
	}
	return root, path, commit, outside
}

func TestRetainedWorktreeCleanupUnlinksDependenciesAndTrackedSymlinks(t *testing.T) {
	for _, keepPointer := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing pointer", true: "stale pointer"}[keepPointer], func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			root, path, commit, outside := createSymlinkWorktree(t)
			pointer, err := os.ReadFile(filepath.Join(path, ".git"))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(filepath.Join(path, ".git")); err != nil {
				t.Fatal(err)
			}
			runGit(t, root, "git", "worktree", "prune", "--expire", "now")
			if keepPointer {
				writeTestFile(t, filepath.Join(path, ".git"), string(pointer), 0600)
			}
			svc := &Service{}
			inspection, err := svc.inspectResidualWorktreeDirectory(ctx, root, path, model.ProjectSummary{
				WorktreeKind: model.WorktreeKindLinked, WorktreeRootPath: root, RepoBranch: "task",
			}, commit)
			if err != nil || !inspection.Safe || inspection.Kind != ResidualWorktreeCleanupOwned {
				t.Fatalf("inspection = %#v, %v", inspection, err)
			}
			if err := removeInspectedResidualWorktreeDirectory(ctx, inspection, path); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Lstat(path); !os.IsNotExist(err) {
				t.Fatalf("worktree remains: %v", err)
			}
			if content, err := os.ReadFile(filepath.Join(outside, "keep.txt")); err != nil || string(content) != "must survive" {
				t.Fatalf("symlink target changed: %q, %v", content, err)
			}
			if got := strings.TrimSpace(gitOutput(t, root, "git", "rev-parse", "task")); got != commit {
				t.Fatalf("branch changed: %s", got)
			}
		})
	}
}

func TestRemovalPlanProtectsUnverifiedSymlinks(t *testing.T) {
	for _, kind := range []string{"changed tracked link", "untracked link", "regular file replaced by link", "metadata link", "worktree root link"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			root, path, commit, outside := createSymlinkWorktree(t)
			linkPath := filepath.Join(path, "shortcut")
			target := "../missing"
			want := "differs from preserved commit"
			switch kind {
			case "untracked link":
				linkPath = filepath.Join(path, "untracked")
				want = "untracked or unverifiable"
			case "regular file replaced by link":
				linkPath = filepath.Join(path, "README.md")
				want = "tracked file mode changed"
			case "metadata link":
				linkPath = filepath.Join(path, ".git")
				want = "Git metadata requires review"
			case "worktree root link":
				linkPath, target = path, path+"-saved"
				if err := os.Rename(path, target); err != nil {
					t.Fatal(err)
				}
				want = "removal path is a symbolic link"
			}
			if err := os.Remove(linkPath); err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			if err := os.Symlink(target, linkPath); err != nil {
				t.Fatal(err)
			}
			_, err := inspectRemovalPlan(context.Background(), root, path, commit, true)
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("inspection = %v, want %q", err, want)
			}
			if kind == "metadata link" || kind == "worktree root link" {
				if _, err := inspectRemovalPlan(context.Background(), root, path, commit, false); err == nil {
					t.Fatal("registered removal bypassed path safety")
				}
			}
			if _, err := os.Stat(filepath.Join(outside, "keep.txt")); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRetainedWorktreeCleanupRejectsChangedSymlink(t *testing.T) {
	root, path, commit, outside := createSymlinkWorktree(t)
	ctx := context.Background()
	plan, err := inspectRemovalPlan(ctx, root, path, commit, true)
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(path, "shortcut")
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	err = removeInspectedResidualWorktreeDirectory(ctx, residualWorktreeInspection{
		Kind: ResidualWorktreeCleanupOwned, Safe: true, Plan: plan, Entries: plan.Entries,
	}, path)
	if err == nil || !strings.Contains(err.Error(), "entry changed before deletion") {
		t.Fatalf("changed link was not protected: %v", err)
	}
	if target, err := os.Readlink(link); err != nil || target != outside {
		t.Fatalf("changed link was lost: %q, %v", target, err)
	}
	if content, err := os.ReadFile(filepath.Join(outside, "keep.txt")); err != nil || string(content) != "must survive" {
		t.Fatalf("symlink target changed: %q, %v", content, err)
	}
}
