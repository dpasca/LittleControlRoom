package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lcroom/internal/scanner"
)

func TestExplicitDeletionDiscardsContentsWithoutArchive(t *testing.T) {
	f := newRemovalFixture(t, nil)
	ctx := context.Background()
	outside := filepath.Join(t.TempDir(), "keep")
	writeTestFile(t, outside, "outside data", 0600)
	if err := os.Symlink(filepath.Dir(outside), filepath.Join(f.path, "outside-link")); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(f.path, "untracked"), "discard", 0600)
	writeTestFile(t, filepath.Join(f.path, ".gitignore"), "dirty tracked file", 0600)
	nested := filepath.Join(f.path, "_artifacts", "broken-clone", ".git")
	writeTestFile(t, filepath.Join(nested, "HEAD"), "corrupt Git metadata", 0600)
	writeTestFile(t, filepath.Join(nested, "shallow.lock"), "unfinished clone", 0600)
	// An old, failed archive must neither block deletion nor be modified by it.
	old := filepath.Join(f.svc.recoveryBase(), recoveryPathKey(f.path), "manifest.json")
	writeTestFile(t, old, "invalid old recovery", 0600)
	if _, err := f.svc.AuditStaleWorktreeCleanup(ctx, time.Now()); err != nil {
		t.Fatalf("old archive blocked audit: %v", err)
	}
	for n := 0; n < 600; n++ {
		writeTestFile(t, filepath.Join(f.path, "_artifacts", fmt.Sprint(n)), "discard", 0000)
	}
	held, err := os.OpenFile(filepath.Join(nested, "shallow.lock"), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	branch := gitOutput(t, f.root, "git", "rev-parse", "task")
	if err := f.svc.RemoveWorktree(ctx, f.path, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(f.path); !os.IsNotExist(err) {
		t.Fatalf("directory still present: %v", err)
	}
	if got, err := os.ReadFile(outside); err != nil || string(got) != "outside data" {
		t.Fatalf("outside modified: %q %v", got, err)
	}
	if got, err := os.ReadFile(old); err != nil || string(got) != "invalid old recovery" {
		t.Fatalf("old archive modified: %q %v", got, err)
	}
	if got := gitOutput(t, f.root, "git", "rev-parse", "task"); got != branch {
		t.Fatal("branch deleted")
	}
	entries, err := scanner.ListGitWorktrees(ctx, f.root)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if samePath(e.Path, f.path) {
			t.Fatal("registration remains")
		}
	}
	if err := f.svc.RemoveWorktree(ctx, f.path, false); err != nil {
		t.Fatalf("retry: %v", err)
	}
}

func TestExplicitDeletionPreservesSharedSubmoduleStores(t *testing.T) {
	f := newAssetResidueFixture(t)
	// Deletion must not read/repair configuration or require clean submodules.
	for _, rel := range f.assets {
		writeTestFile(t, filepath.Join(f.path, rel, "untracked"), "discard", 0600)
		admin := strings.TrimSpace(gitOutput(t, filepath.Join(f.path, rel), "git", "rev-parse", "--absolute-git-dir"))
		writeTestFile(t, filepath.Join(admin, "index.lock"), "abandoned", 0600)
	}
	if err := f.svc.RemoveWorktree(context.Background(), f.path, false); err != nil {
		t.Fatal(err)
	}
	f.assertChildren(t, true)
	if _, err := os.Stat(f.svc.recoveryBase()); !os.IsNotExist(err) {
		t.Fatalf("deletion created an archive: %v", err)
	}
}

func TestExplicitDeletionRejectsPrimaryAndSymlink(t *testing.T) {
	f := newRemovalFixture(t, nil)
	if err := f.svc.RemoveWorktree(context.Background(), f.root, true); err == nil {
		t.Fatal("primary deleted")
	}
	if err := os.RemoveAll(f.path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(f.other, f.path); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.RemoveWorktree(context.Background(), f.path, true); err == nil {
		t.Fatal("selected symlink accepted")
	}
	if _, err := os.Stat(filepath.Join(f.other, ".git")); err != nil {
		t.Fatal("symlink target damaged")
	}
}

func TestExplicitDeletionRejectsOutsideNestedWorktreeConsumer(t *testing.T) {
	f := newRemovalFixture(t, nil)
	nested := filepath.Join(f.path, "_artifacts", "nested")
	initGitRepo(t, nested)
	outside := filepath.Join(filepath.Dir(f.path), "outside-consumer")
	runGit(t, nested, "git", "worktree", "add", "--detach", outside, "HEAD")
	if err := f.svc.RemoveWorktree(context.Background(), f.path, true); err == nil || !strings.Contains(err.Error(), "outside worktree") {
		t.Fatalf("outside consumer not blocked: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outside, ".git")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(f.path); err != nil {
		t.Fatal("source removed before boundary check")
	}
}

func TestExplicitDeletionStopsAtFilesystemBoundary(t *testing.T) {
	path := t.TempDir()
	writeTestFile(t, filepath.Join(path, "keep"), "outside", 0600)
	root, err := os.OpenRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	fs, err := deletionFilesystem(root)
	if err != nil {
		t.Fatal(err)
	}
	fs.mount++ // Includes bind mounts with an unchanged device number.
	err = walkDeletionTree(context.Background(), root, fs, "", true, func(string, os.FileInfo) error { return nil })
	if err == nil {
		t.Fatal("filesystem boundary ignored")
	}
	if _, err := os.Stat(filepath.Join(path, "keep")); err != nil {
		t.Fatal("deleted across mount boundary")
	}
}
