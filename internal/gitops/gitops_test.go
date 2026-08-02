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

	oldTimeout := defaultPullStallTimeout
	defaultPullStallTimeout = 50 * time.Millisecond
	defer func() {
		defaultPullStallTimeout = oldTimeout
	}()

	binDir := t.TempDir()
	writePullTestGit(t, binDir)
	t.Setenv("LCROOM_PULL_TEST_MODE", "stalled-fetch")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	err := Pull(context.Background(), t.TempDir())
	if err == nil {
		t.Fatal("Pull() error = nil, want timeout")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Pull() error = %v, want deadline exceeded", err)
	}
	var stallErr *PullStallError
	if !errors.As(err, &stallErr) || stallErr.Phase != PullPhaseFetch {
		t.Fatalf("Pull() error = %v, want fetch stall error", err)
	}
	if !strings.Contains(err.Error(), "stalled after 50ms without progress") {
		t.Fatalf("Pull() error = %q, want stall text", err)
	}
}

func TestPullContinuesPastStallWindowWhileFetchReportsProgress(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake git progress test uses a POSIX shell script")
	}

	binDir := t.TempDir()
	writePullTestGit(t, binDir)
	t.Setenv("LCROOM_PULL_TEST_MODE", "active-fetch")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	var progress []PullProgress
	startedAt := time.Now()
	result, err := PullWithOptions(context.Background(), t.TempDir(), PullOptions{
		StallTimeout: 50 * time.Millisecond,
		Progress: func(update PullProgress) {
			progress = append(progress, update)
		},
	})
	if err != nil {
		t.Fatalf("PullWithOptions() error = %v", err)
	}
	if elapsed := time.Since(startedAt); elapsed < 100*time.Millisecond {
		t.Fatalf("pull elapsed = %s, want total runtime beyond stall window", elapsed)
	}
	if !result.FetchCompleted || !result.FastForwarded || result.PendingFastForward {
		t.Fatalf("PullWithOptions() result = %#v, want completed fast-forward", result)
	}
	if !pullTestProgressContains(progress, "Receiving objects") {
		t.Fatalf("progress = %#v, want receiving-objects detail", progress)
	}
}

func TestPullTreatsGitLFSFileUpdatesAsProgress(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake Git LFS progress test uses a POSIX shell script")
	}

	binDir := t.TempDir()
	writePullTestGit(t, binDir)
	t.Setenv("LCROOM_PULL_TEST_MODE", "lfs-fast-forward")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	var progress []PullProgress
	result, err := PullWithOptions(context.Background(), t.TempDir(), PullOptions{
		StallTimeout: 250 * time.Millisecond,
		Progress: func(update PullProgress) {
			progress = append(progress, update)
		},
	})
	if err != nil {
		t.Fatalf("PullWithOptions() error = %v", err)
	}
	if !result.FastForwarded {
		t.Fatalf("PullWithOptions() result = %#v, want completed fast-forward", result)
	}
	if !pullTestProgressContains(progress, "media.pack") {
		t.Fatalf("progress = %#v, want Git LFS media progress", progress)
	}
}

func TestPullReportsPendingFastForwardWhenUpdateStallsAfterFetch(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake git stall test uses a POSIX shell script")
	}

	binDir := t.TempDir()
	writePullTestGit(t, binDir)
	t.Setenv("LCROOM_PULL_TEST_MODE", "stalled-fast-forward")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	result, err := PullWithOptions(context.Background(), t.TempDir(), PullOptions{StallTimeout: 50 * time.Millisecond})
	if err == nil {
		t.Fatal("PullWithOptions() error = nil, want fast-forward stall")
	}
	var stallErr *PullStallError
	if !errors.As(err, &stallErr) || stallErr.Phase != PullPhaseFastForward {
		t.Fatalf("PullWithOptions() error = %v, want fast-forward stall", err)
	}
	if !result.FetchCompleted || !result.PendingFastForward || result.FastForwarded {
		t.Fatalf("PullWithOptions() result = %#v, want fetched pending fast-forward", result)
	}
}

func TestPullPreservesFetchWhenCallerDeadlineEndsBeforeFastForward(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake git deadline test uses a POSIX shell script")
	}

	binDir := t.TempDir()
	writePullTestGit(t, binDir)
	t.Setenv("LCROOM_PULL_TEST_MODE", "fetched")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	ctx, cancel := context.WithCancel(context.Background())
	result, err := PullWithOptions(ctx, t.TempDir(), PullOptions{
		StallTimeout: time.Second,
		Progress: func(update PullProgress) {
			if update.Phase == PullPhaseFetch && update.Detail == "Fetch complete" {
				cancel()
			}
		},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("PullWithOptions() error = %v, want caller cancellation after fetch", err)
	}
	if !result.FetchCompleted {
		t.Fatalf("PullWithOptions() result = %#v, want completed fetch preserved", result)
	}
	if !result.PendingFastForward {
		t.Fatalf("PullWithOptions() result = %#v, want pending fast-forward identified", result)
	}
	if result.FastForwarded {
		t.Fatalf("PullWithOptions() result = %#v, fast-forward should not run after cancellation", result)
	}
}

func TestPullHonorsExplicitCancellation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake git cancellation test uses a POSIX shell script")
	}

	binDir := t.TempDir()
	writePullTestGit(t, binDir)
	t.Setenv("LCROOM_PULL_TEST_MODE", "cancel-fetch")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	ctx, cancel := context.WithCancel(context.Background())
	repoPath := t.TempDir()
	done := make(chan error, 1)
	go func() {
		_, err := PullWithOptions(ctx, repoPath, PullOptions{StallTimeout: time.Second})
		done <- err
	}()
	time.Sleep(80 * time.Millisecond)
	cancel()

	err := <-done
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("PullWithOptions() error = %v, want context canceled", err)
	}
	var stallErr *PullStallError
	if errors.As(err, &stallErr) {
		t.Fatalf("PullWithOptions() error = %v, should not report user cancellation as a stall", err)
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

func writePullTestGit(t *testing.T, binDir string) {
	t.Helper()
	script := `#!/bin/sh
args="$*"
case "$args" in
  *"rev-parse --abbrev-ref --symbolic-full-name @{upstream}"*)
    echo origin/master
    ;;
  *"rev-parse HEAD"*)
    echo aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
    ;;
  *"rev-parse origin/master"*)
    echo bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
    ;;
  *"merge-base --is-ancestor aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"*)
    exit 0
    ;;
  *"fetch --progress"*)
    case "$LCROOM_PULL_TEST_MODE" in
      stalled-fetch)
        while :; do :; done
        ;;
      cancel-fetch)
        while :; do
          echo "Receiving objects: still active" >&2
          sleep 0.03
        done
        ;;
      active-fetch)
        i=1
        while [ "$i" -le 5 ]; do
          echo "Receiving objects: $i/5" >&2
          sleep 0.03
          i=$((i + 1))
        done
        ;;
      *)
        echo "Already up to date" >&2
        ;;
    esac
    ;;
  *"merge --ff-only --progress bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"*)
    case "$LCROOM_PULL_TEST_MODE" in
      stalled-fast-forward)
        while :; do :; done
        ;;
      lfs-fast-forward)
        i=1
        while [ "$i" -le 4 ]; do
          echo "download 1/1 $i/4 media.pack" >> "$GIT_LFS_PROGRESS"
          sleep 0.15
          i=$((i + 1))
        done
        ;;
    esac
    ;;
  *)
    echo "unexpected fake git invocation: $args" >&2
    exit 2
    ;;
esac
`
	gitPath := filepath.Join(binDir, "git")
	if err := os.WriteFile(gitPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake git: %v", err)
	}
}

func pullTestProgressContains(progress []PullProgress, needle string) bool {
	for _, update := range progress {
		if strings.Contains(update.Detail, needle) {
			return true
		}
	}
	return false
}
