package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lcroom/internal/config"
	"lcroom/internal/events"
	"lcroom/internal/gitops"
	"lcroom/internal/model"
	"lcroom/internal/scanner"
	"lcroom/internal/store"
)

func TestSplitAutoReviewableUntrackedSkipsCommonGeneratedDirectories(t *testing.T) {
	t.Parallel()

	reviewable, skipped := splitAutoReviewableUntracked([]scanner.GitChange{
		{Path: "notes.txt", Untracked: true},
		{Path: "output/", Untracked: true},
		{Path: "nested/dist/", Untracked: true},
		{Path: "src/output.txt", Untracked: true},
	})

	if len(reviewable) != 2 || reviewable[0].Path != "notes.txt" || reviewable[1].Path != "src/output.txt" {
		t.Fatalf("reviewable = %#v, want notes.txt and src/output.txt", reviewable)
	}
	if len(skipped) != 2 || skipped[0].Path != "output/" || skipped[1].Path != "nested/dist/" {
		t.Fatalf("skipped = %#v, want output/ and nested/dist/", skipped)
	}
}

func TestPrepareCommitSkipsCommonGeneratedDirectoriesFromAutoUntrackedReview(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	projectPath := filepath.Join(t.TempDir(), "repo")
	initGitRepo(t, projectPath)

	if err := os.WriteFile(filepath.Join(projectPath, "README.md"), []byte("hello\npreview\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectPath, "notes.txt"), []byte("release note for the staged change\n"), 0o644); err != nil {
		t.Fatalf("write notes: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(projectPath, "output"), 0o755); err != nil {
		t.Fatalf("mkdir output: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectPath, "output", "report.txt"), []byte("generated export\n"), 0o644); err != nil {
		t.Fatalf("write output report: %v", err)
	}
	runGit(t, projectPath, "git", "add", "README.md")

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:          projectPath,
		Name:          "repo",
		PresentOnDisk: true,
		InScope:       true,
		UpdatedAt:     time.Now().UTC(),
	}); err != nil {
		t.Fatalf("upsert project: %v", err)
	}

	recommender := &fakeUntrackedFileRecommender{
		suggestion: gitops.UntrackedFileRecommendationResult{
			Files: []gitops.UntrackedFileDecision{
				{Path: "notes.txt", Include: true, Confidence: 0.95, Reason: "notes.txt matches the staged README update."},
			},
		},
	}
	svc := New(config.Default(), st, events.NewBus(), nil)
	svc.commitMessageSuggester = nil
	svc.untrackedFileRecommender = recommender

	preview, err := svc.PrepareCommit(ctx, projectPath, GitActionCommit, "Update repo")
	if err != nil {
		t.Fatalf("prepare commit: %v", err)
	}
	if len(recommender.lastInput.Candidates) != 1 || recommender.lastInput.Candidates[0].Path != "notes.txt" {
		t.Fatalf("candidate files = %#v, want notes.txt only", recommender.lastInput.Candidates)
	}
	if len(preview.SelectedUntracked) != 1 || preview.SelectedUntracked[0].Path != "notes.txt" {
		t.Fatalf("selected untracked = %#v, want notes.txt", preview.SelectedUntracked)
	}
	if len(preview.Excluded) != 1 || preview.Excluded[0].Path != "output/" {
		t.Fatalf("excluded files = %#v, want output/ left out", preview.Excluded)
	}
	warnings := strings.Join(preview.Warnings, "\n")
	if !strings.Contains(warnings, "Skipped automatic review for 1 untracked directory with a common generated/export name: output/") {
		t.Fatalf("warnings = %#v, want skipped output/ warning", preview.Warnings)
	}
	if !strings.Contains(warnings, "Will also stage 1 AI-recommended untracked file before commit.") {
		t.Fatalf("warnings = %#v, want selected untracked note", preview.Warnings)
	}
}
