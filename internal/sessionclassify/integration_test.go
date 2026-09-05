package sessionclassify

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"lcroom/internal/model"
)

func TestWorktreeIntegrationTracksTargetPublication(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repo := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-b", "master")
	git("config", "user.name", "Test")
	git("config", "user.email", "test@example.com")
	git("commit", "--allow-empty", "-m", "initial")
	remote := filepath.Join(t.TempDir(), "remote.git")
	git("init", "--bare", remote)
	git("remote", "add", "origin", remote)
	git("push", "-u", "origin", "master")
	base := git("rev-parse", "HEAD")
	git("branch", "feature")
	git("commit", "--allow-empty", "-m", "integrated work")
	// Current branch is deliberately not the merge target, and has no upstream.
	git("checkout", "feature")
	snapshot := func() GitStatusSnapshot {
		return NewGitStatusSnapshot(false, model.RepoSyncNoUpstream, 0, 0).
			WithWorktree(ctx, repo, model.WorktreeKindLinked, "master", model.WorktreeMergeStatusMerged)
	}
	unpushed := snapshot()
	if got := unpushed.Integration; got.TargetRemoteStatus != model.RepoSyncAhead || got.TargetAheadCount != 1 {
		t.Fatalf("unpublished target = %+v", got)
	}
	git("push", "origin", "master")
	pushed := snapshot()
	if got := pushed.Integration; got.TargetRemoteStatus != model.RepoSyncSynced || got.TargetAheadCount != 0 {
		t.Fatalf("published target = %+v", got)
	}
	if SnapshotHashForSnapshot(SessionSnapshot{GitStatus: unpushed}) == SnapshotHashForSnapshot(SessionSnapshot{GitStatus: pushed}) {
		t.Fatal("target push must invalidate the assessment without a source-branch change")
	}
	git("update-ref", "refs/heads/master", base)
	if got := snapshot().Integration; got.TargetRemoteStatus != model.RepoSyncBehind || got.TargetBehindCount != 1 {
		t.Fatalf("target behind its upstream = %+v", got)
	}
	git("checkout", "master")
	git("commit", "--allow-empty", "-m", "different local work")
	if got := snapshot().Integration; got.TargetRemoteStatus != model.RepoSyncDiverged || got.TargetAheadCount != 1 || got.TargetBehindCount != 1 {
		t.Fatalf("diverged target = %+v", got)
	}
	git("branch", "--unset-upstream", "master")
	if got := snapshot().Integration; got.TargetRemoteStatus != "unknown" {
		t.Fatalf("missing upstream must not imply publication: %+v", got)
	}
}

func TestWorktreeIntegrationUnknownAndMergeHash(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	base := NewGitStatusSnapshot(false, model.RepoSyncNoUpstream, 0, 0)
	if got := base.WithWorktree(ctx, t.TempDir(), model.WorktreeKindMain, "master", model.WorktreeMergeStatusMerged); got.Integration != nil {
		t.Fatal("root checkouts must not claim linked-worktree integration")
	}
	unmerged := base.WithWorktree(ctx, t.TempDir(), model.WorktreeKindLinked, "master", model.WorktreeMergeStatusNotMerged)
	merged := base.WithWorktree(ctx, t.TempDir(), model.WorktreeKindLinked, "master", model.WorktreeMergeStatusMerged)
	if merged.Integration.TargetRemoteStatus != "unknown" {
		t.Fatal("failed Git reads must leave publication unknown")
	}
	if SnapshotHashForSnapshot(SessionSnapshot{GitStatus: unmerged}) == SnapshotHashForSnapshot(SessionSnapshot{GitStatus: merged}) {
		t.Fatal("merge-only changes must invalidate the assessment")
	}
}
