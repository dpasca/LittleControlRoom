package gitops

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestReadDiffStatAllStagedWorksWhenRepoIndexIsMissing(t *testing.T) {
	repoPath := t.TempDir()
	runGitopsTestGit(t, repoPath, "init")
	assertGitIndexMissing(t, repoPath)

	if err := os.WriteFile(filepath.Join(repoPath, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write README.md: %v", err)
	}

	stat, err := ReadDiffStatAllStaged(context.Background(), repoPath)
	if err != nil {
		t.Fatalf("ReadDiffStatAllStaged() error = %v", err)
	}
	if !strings.Contains(stat, "README.md") || !strings.Contains(stat, "1 file changed") {
		t.Fatalf("ReadDiffStatAllStaged() = %q, want README.md stat", stat)
	}
	assertGitIndexMissing(t, repoPath)
}

func TestReadDiffStatWithAddedPathsWorksWhenRepoIndexIsMissing(t *testing.T) {
	repoPath := t.TempDir()
	runGitopsTestGit(t, repoPath, "init")
	assertGitIndexMissing(t, repoPath)

	if err := os.WriteFile(filepath.Join(repoPath, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write README.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoPath, "notes.txt"), []byte("notes\n"), 0o644); err != nil {
		t.Fatalf("write notes.txt: %v", err)
	}

	stat, err := ReadDiffStatWithAddedPaths(context.Background(), repoPath, []string{"notes.txt"})
	if err != nil {
		t.Fatalf("ReadDiffStatWithAddedPaths() error = %v", err)
	}
	if !strings.Contains(stat, "notes.txt") || strings.Contains(stat, "README.md") {
		t.Fatalf("ReadDiffStatWithAddedPaths() = %q, want only notes.txt stat", stat)
	}
	assertGitIndexMissing(t, repoPath)
}

func TestPushTimesOutHungGitProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake git timeout test uses a POSIX shell script")
	}

	oldTimeout := defaultPushTimeout
	defaultPushTimeout = 50 * time.Millisecond
	defer func() {
		defaultPushTimeout = oldTimeout
	}()

	binDir := t.TempDir()
	gitPath := filepath.Join(binDir, "git")
	script := "#!/bin/sh\nsleep 1\n"
	if err := os.WriteFile(gitPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake git: %v", err)
	}

	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	err := Push(context.Background(), t.TempDir())
	if err == nil {
		t.Fatal("Push() error = nil, want timeout")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Push() error = %v, want deadline exceeded", err)
	}
	if !strings.Contains(err.Error(), "timed out after 50ms") {
		t.Fatalf("Push() error = %q, want timeout text", err)
	}
}

func TestPullTimesOutHungGitProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake git timeout test uses a POSIX shell script")
	}

	oldTimeout := defaultPullTimeout
	defaultPullTimeout = 50 * time.Millisecond
	defer func() {
		defaultPullTimeout = oldTimeout
	}()

	binDir := t.TempDir()
	gitPath := filepath.Join(binDir, "git")
	script := "#!/bin/sh\nsleep 1\n"
	if err := os.WriteFile(gitPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake git: %v", err)
	}

	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	err := Pull(context.Background(), t.TempDir())
	if err == nil {
		t.Fatal("Pull() error = nil, want timeout")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Pull() error = %v, want deadline exceeded", err)
	}
	if !strings.Contains(err.Error(), "timed out after 50ms") {
		t.Fatalf("Pull() error = %q, want timeout text", err)
	}
}

func TestIsPushRejectedNeedsPull(t *testing.T) {
	err := errors.New(`push /tmp/repo: exit status 1: Locking support detected on remote "origin".
To https://example.test/repo.git
 ! [rejected]        topic -> topic (fetch first)
error: failed to push some refs to 'https://example.test/repo.git'
hint: Updates were rejected because the remote contains work that you do not
hint: have locally. This is usually caused by another repository pushing to
hint: the same ref. If you want to integrate the remote changes, use
hint: 'git pull' before pushing again.`)

	if !IsPushRejectedNeedsPull(err) {
		t.Fatalf("IsPushRejectedNeedsPull() = false, want true")
	}
}

func TestIsPushRejectedNeedsPullIgnoresOtherPushErrors(t *testing.T) {
	err := errors.New("push /tmp/repo: exit status 128: fatal: could not read Username for 'https://example.test'")

	if IsPushRejectedNeedsPull(err) {
		t.Fatalf("IsPushRejectedNeedsPull() = true, want false")
	}
}

func runGitopsTestGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
	}
}

func assertGitIndexMissing(t *testing.T, repoPath string) {
	t.Helper()
	indexPath := filepath.Join(repoPath, ".git", "index")
	if _, err := os.Stat(indexPath); err == nil {
		t.Fatalf("%s exists, want missing", indexPath)
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat %s: %v", indexPath, err)
	}
}
