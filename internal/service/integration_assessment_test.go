package service

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"lcroom/internal/config"
	"lcroom/internal/detectors"
	"lcroom/internal/events"
	"lcroom/internal/model"
	"lcroom/internal/scanner"
	"lcroom/internal/sessionclassify"
	"lcroom/internal/store"
)

type unusedIntegrationClassifier struct{}

func (unusedIntegrationClassifier) Classify(context.Context, sessionclassify.SessionSnapshot) (sessionclassify.Result, error) {
	return sessionclassify.Result{}, errors.New("test claims assessments manually")
}

func TestMergeAndParentPushRequeueUnchangedEngineerAssessment(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	initGitRepo(t, repo)
	status, err := scanner.ReadGitRepoStatus(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	target := status.Branch
	remote := filepath.Join(root, "remote.git")
	runGit(t, repo, "git", "init", "--bare", remote)
	runGit(t, repo, "git", "remote", "add", "origin", remote)
	runGit(t, repo, "git", "push", "-u", "origin", target)
	worktree := filepath.Join(root, "worktree")
	runGit(t, repo, "git", "worktree", "add", "-b", "feature", worktree)
	if err := os.WriteFile(filepath.Join(worktree, "README.md"), []byte("implemented feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, worktree, "git", "commit", "-am", "feature")

	st, err := store.Open(filepath.Join(root, "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	bus := events.NewBus()
	now := time.Now().UTC()
	session := model.SessionEvidence{
		SessionID: "integration-assessment", ProjectPath: worktree,
		SessionFile: filepath.Join("..", "..", "testdata", "codex_footprint", "sessions", "2026", "03", "05", "rollout-modern.jsonl"),
		Format:      "modern", LastEventAt: now, StartedAt: now.Add(-time.Minute),
		LatestTurnStateKnown: true, LatestTurnCompleted: true,
	}
	cfg := config.Default()
	cfg.IncludePaths = []string{repo, worktree}
	detector := &fakeDetector{activities: map[string]*model.DetectorProjectActivity{
		worktree: {ProjectPath: worktree, Source: "codex", LastActivity: now, Sessions: []model.SessionEvidence{session}},
	}}
	svc := New(cfg, st, bus, []detectors.Detector{detector})
	svc.classifier = sessionclassify.NewManager(st, bus, sessionclassify.Options{Client: unusedIntegrationClassifier{}})
	for _, state := range []model.ProjectState{
		{Path: repo, Name: "repo", WorktreeRootPath: repo, WorktreeKind: model.WorktreeKindMain, RepoBranch: target, PresentOnDisk: true, InScope: true, UpdatedAt: now},
		{Path: worktree, Name: "worktree", WorktreeRootPath: repo, WorktreeKind: model.WorktreeKindLinked, WorktreeParentBranch: target,
			WorktreeMergeStatus: model.WorktreeMergeStatusNotMerged, RepoBranch: "feature", PresentOnDisk: true, InScope: true, UpdatedAt: now, Sessions: []model.SessionEvidence{session}},
	} {
		if err := st.UpsertProjectState(ctx, state); err != nil {
			t.Fatal(err)
		}
	}
	completeNext := func(wantMerge model.WorktreeMergeStatus, wantRemote model.RepoSyncStatus) string {
		t.Helper()
		claimed, err := st.ClaimNextPendingSessionClassification(ctx, time.Minute)
		if err != nil {
			t.Fatalf("expected reassessment: %v", err)
		}
		detail, err := st.GetProjectDetail(ctx, worktree, 1)
		if err != nil {
			t.Fatal(err)
		}
		gitStatus := sessionclassify.GitStatusForSummary(ctx, detail.Summary)
		if got := gitStatus.Integration; got == nil || got.MergeStatus != wantMerge || got.TargetRemoteStatus != wantRemote {
			t.Fatalf("integration evidence = %+v, want %s / %s", got, wantMerge, wantRemote)
		}
		hash, err := sessionclassify.ComputeSnapshotHash(ctx, worktree, detail.Sessions[0], gitStatus)
		if err != nil || hash != claimed.SnapshotHash {
			t.Fatalf("worker and refresh hashes must agree: %s / %s, %v", hash, claimed.SnapshotHash, err)
		}
		claimed.Category = model.SessionCategoryNeedsFollowUp
		claimed.Summary = "Commit, merge and push the finished work."
		if err := st.CompleteSessionClassification(ctx, claimed); err != nil {
			t.Fatal(err)
		}
		return hash
	}
	if err := svc.RefreshProjectStatus(ctx, worktree); err != nil {
		t.Fatal(err)
	}
	before := completeNext(model.WorktreeMergeStatusNotMerged, "unknown")
	if _, err := svc.MergeWorktreeBack(ctx, worktree); err != nil {
		t.Fatal(err)
	}
	merged := completeNext(model.WorktreeMergeStatusMerged, model.RepoSyncAhead)
	if merged == before {
		t.Fatal("merge did not invalidate the old follow-up")
	}
	runGit(t, repo, "git", "push", "origin", target)
	// Refreshing the parent after a push must refresh its linked worktrees too.
	if err := svc.RefreshProjectStatus(ctx, repo); err != nil {
		t.Fatal(err)
	}
	pushed := completeNext(model.WorktreeMergeStatusMerged, model.RepoSyncSynced)
	if pushed == merged {
		t.Fatal("parent push did not invalidate the old follow-up")
	}
	if err := svc.RefreshProjectStatus(ctx, worktree); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ClaimNextPendingSessionClassification(ctx, time.Minute); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("unchanged evidence must not queue another model call: %v", err)
	}
	if _, err := svc.ScanOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ClaimNextPendingSessionClassification(ctx, time.Minute); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("periodic scan must agree with the targeted refresh hash: %v", err)
	}
	// A later source change must invalidate the merged state on periodic scans,
	// even with the same completed engineer transcript and clean checkout.
	if err := os.WriteFile(filepath.Join(worktree, "README.md"), []byte("second feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, worktree, "git", "commit", "-am", "second feature")
	if _, err := svc.ScanOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if hash := completeNext(model.WorktreeMergeStatusNotMerged, "unknown"); hash == pushed {
		t.Fatal("scan reused stale integration evidence")
	}
}
