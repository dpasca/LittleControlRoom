package sessionclassify

import (
	"context"
	"os"
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
	git("checkout", "-b", "feature")
	if err := os.WriteFile(filepath.Join(repo, "feature.txt"), []byte("feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "feature.txt")
	git("commit", "-m", "feature")
	feature := git("rev-parse", "HEAD")
	git("checkout", "master")
	git("merge", "--ff-only", "feature")
	// Current branch is deliberately not the merge target, and has no upstream.
	git("checkout", "feature")
	snapshot := func() GitStatusSnapshot {
		return NewGitStatusSnapshot(false, model.RepoSyncNoUpstream, 0, 0).
			WithWorktree(ctx, repo, model.WorktreeKindLinked, "master", model.WorktreeMergeStatusMerged)
	}
	check := func(want WorktreePublicationStatus) string {
		t.Helper()
		got := snapshot()
		if got.Integration.PublicationStatus != want {
			t.Fatalf("publication = %+v, want %s", got.Integration, want)
		}
		return SnapshotHashForSnapshot(SessionSnapshot{GitStatus: got})
	}
	unpushed := check(WorktreePublicationPending)
	git("push", "origin", "master")
	pushed := check(WorktreePublicationPublished)
	if unpushed == pushed {
		t.Fatal("publishing this work must invalidate its assessment")
	}
	git("checkout", "master")
	git("commit", "--allow-empty", "-m", "unrelated work")
	git("checkout", "feature")
	if check(WorktreePublicationPublished) != pushed {
		t.Fatal("unrelated target commits must not invalidate published work")
	}
	git("push", "origin", "master")
	if check(WorktreePublicationPublished) != pushed {
		t.Fatal("unrelated target pushes must not invalidate published work")
	}
	git("update-ref", "refs/heads/master", feature)
	if check(WorktreePublicationPublished) != pushed {
		t.Fatal("a target behind upstream must not invalidate published work")
	}
	git("checkout", "master")
	git("commit", "--allow-empty", "-m", "different local work")
	git("checkout", "feature")
	if check(WorktreePublicationPublished) != pushed {
		t.Fatal("a diverged target must not invalidate work already upstream")
	}
	git("update-ref", "refs/remotes/origin/master", base)
	if check(WorktreePublicationPending) != unpushed {
		t.Fatal("removing work from upstream must invalidate publication")
	}
	// Publication follows equivalent patches too, matching merge detection.
	git("checkout", "-b", "rebased", base)
	git("commit", "--allow-empty", "-m", "new base")
	git("cherry-pick", "feature")
	git("update-ref", "refs/remotes/origin/master", "HEAD")
	git("checkout", "feature")
	if check(WorktreePublicationPublished) != pushed {
		t.Fatal("equivalent published patches must resolve publication")
	}
	git("branch", "--unset-upstream", "master")
	check(WorktreePublicationUnknown)
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
	if merged.Integration.PublicationStatus != WorktreePublicationUnknown {
		t.Fatal("failed Git reads must leave publication unknown")
	}
	if SnapshotHashForSnapshot(SessionSnapshot{GitStatus: unmerged}) == SnapshotHashForSnapshot(SessionSnapshot{GitStatus: merged}) {
		t.Fatal("merge-only changes must invalidate the assessment")
	}
}
