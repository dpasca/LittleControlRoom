package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"lcroom/internal/config"
	"lcroom/internal/events"
	"lcroom/internal/model"
	"lcroom/internal/store"
)

func TestDiscoveredDetachedWorktreeAttentionAcrossScansAndRefresh(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(parent, "repo")
	path := filepath.Join(parent, "repo--comparison-a")
	initGitRepo(t, root)
	runGit(t, root, "git", "worktree", "add", "--detach", path)
	st, err := store.Open(filepath.Join(parent, "test.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path: root, Name: "repo", PresentOnDisk: true, InScope: true,
		WorktreeKind: model.WorktreeKindMain,
	}); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.IncludePaths = []string{parent}
	svc := New(cfg, st, events.NewBus(), nil)
	svc.SetSessionClassifier(nil)
	check := func(wantScore int, wantReason string) {
		t.Helper()
		detail, err := st.GetProjectDetail(ctx, path, 1)
		if err != nil {
			t.Fatal(err)
		}
		p := detail.Summary
		if p.WorktreeKind != model.WorktreeKindLinked || p.RepoBranch != "(detached)" || !p.PresentOnDisk {
			t.Fatalf("expected a visible detached linked checkout, got %#v", p)
		}
		scoreMatches := p.AttentionScore == wantScore
		if wantReason == "new_project" {
			// The boost tapers as real time elapses during the scan.
			scoreMatches = p.AttentionScore > 50 && p.AttentionScore <= wantScore
		}
		if !scoreMatches || p.Status != model.StatusIdle || len(detail.Sessions) != 0 {
			t.Fatalf("score/status = %d/%s with %d sessions, want %d/idle with no sessions", p.AttentionScore, p.Status, len(detail.Sessions), wantScore)
		}
		if len(detail.Reasons) == 0 || detail.Reasons[0].Code != wantReason {
			t.Fatalf("attention reasons = %#v, want %s", detail.Reasons, wantReason)
		}
	}
	// Both first discovery and subsequent scans must preserve a quiet checkout.
	for i := 0; i < 2; i++ {
		if _, err := svc.ScanOnce(ctx); err != nil {
			t.Fatal(err)
		}
		check(10, "no_activity")
	}
	if err := svc.RefreshProjectStatus(ctx, path); err != nil {
		t.Fatal(err)
	}
	check(10, "no_activity")

	// Actual local edits remain actionable even without an engineer session.
	if err := os.WriteFile(filepath.Join(path, "comparison.txt"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := svc.RefreshProjectStatus(ctx, path); err != nil {
		t.Fatal(err)
	}
	check(25, "no_activity")
	if err := os.Remove(filepath.Join(path, "comparison.txt")); err != nil {
		t.Fatal(err)
	}

	// Explicit TODO affiliation is enough to request a first engineer session.
	todo, err := st.AddTodo(ctx, root, "Review this comparison")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetWorktreeOriginTodoID(ctx, path, todo.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.RefreshProjectStatus(ctx, path); err != nil {
		t.Fatal(err)
	}
	check(80, "new_project")
	if _, err := svc.ScanOnce(ctx); err != nil {
		t.Fatal(err)
	}
	check(80, "new_project")
}
