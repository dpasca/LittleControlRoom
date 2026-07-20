package service

import (
	"context"
	"database/sql"
	"errors"
	"image"
	"image/color"
	"image/png"
	"lcroom/internal/config"
	"lcroom/internal/events"
	"lcroom/internal/gitops"
	"lcroom/internal/model"
	"lcroom/internal/scanner"
	"lcroom/internal/store"
	"lcroom/internal/worktreeprep"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestPrepareCommitUsesStagedScopeAndFinishPushState(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	remotePath := filepath.Join(root, "origin.git")
	projectPath := filepath.Join(root, "repo")
	initBareGitRepo(t, remotePath)
	initGitRepo(t, projectPath)
	runGit(t, projectPath, "git", "remote", "add", "origin", remotePath)
	runGit(t, projectPath, "git", "push", "-u", "origin", "master")

	if err := os.WriteFile(filepath.Join(projectPath, "README.md"), []byte("hello\npreview\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectPath, "notes.txt"), []byte("keep local for later\n"), 0o644); err != nil {
		t.Fatalf("write notes: %v", err)
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

	svc := New(config.Default(), st, events.NewBus(), nil)
	svc.commitMessageSuggester = nil

	preview, err := svc.PrepareCommit(ctx, projectPath, GitActionFinish, "")
	if err != nil {
		t.Fatalf("prepare commit: %v", err)
	}
	if preview.StageMode != GitStageStagedOnly {
		t.Fatalf("stage mode = %s, want %s", preview.StageMode, GitStageStagedOnly)
	}
	if len(preview.Included) != 1 || preview.Included[0].Path != "README.md" {
		t.Fatalf("included files = %#v, want staged README.md only", preview.Included)
	}
	if len(preview.Excluded) != 1 || preview.Excluded[0].Path != "notes.txt" {
		t.Fatalf("excluded files = %#v, want unstaged notes.txt", preview.Excluded)
	}
	if !preview.CanPush {
		t.Fatalf("expected preview to allow push: %#v", preview)
	}
	if preview.Message != "Update README.md" {
		t.Fatalf("message = %q, want fallback subject", preview.Message)
	}
	if preview.DiffSummary == "" {
		t.Fatalf("diff summary should be populated: %#v", preview)
	}
}

func TestCommitPreviewStateHashTracksCurrentGitState(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	projectPath := filepath.Join(t.TempDir(), "repo")
	initGitRepo(t, projectPath)

	if err := os.WriteFile(filepath.Join(projectPath, "README.md"), []byte("hello\npreview\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
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

	svc := New(config.Default(), st, events.NewBus(), nil)
	preview, err := svc.PrepareCommit(ctx, projectPath, GitActionCommit, "")
	if err != nil {
		t.Fatalf("prepare commit: %v", err)
	}
	if strings.TrimSpace(preview.StateHash) == "" {
		t.Fatalf("prepare commit should populate a state hash: %#v", preview)
	}

	currentHash, err := svc.CommitPreviewStateHash(ctx, projectPath)
	if err != nil {
		t.Fatalf("current commit preview state hash: %v", err)
	}
	if currentHash != preview.StateHash {
		t.Fatalf("current hash = %q, want preview hash %q", currentHash, preview.StateHash)
	}

	if err := os.WriteFile(filepath.Join(projectPath, "notes.txt"), []byte("keep this too\n"), 0o644); err != nil {
		t.Fatalf("write notes: %v", err)
	}
	runGit(t, projectPath, "git", "add", "notes.txt")

	updatedHash, err := svc.CommitPreviewStateHash(ctx, projectPath)
	if err != nil {
		t.Fatalf("updated commit preview state hash: %v", err)
	}
	if updatedHash == preview.StateHash {
		t.Fatalf("state hash should change after staged files change; still got %q", updatedHash)
	}
}

func TestPrepareDiffIncludesTextUntrackedDeletedAndImagePreviews(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	projectPath := filepath.Join(t.TempDir(), "repo")
	initGitRepo(t, projectPath)

	if err := writeTestPNG(filepath.Join(projectPath, "pixel.png"), color.RGBA{R: 220, G: 32, B: 32, A: 255}); err != nil {
		t.Fatalf("write initial image: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectPath, "old.txt"), []byte("old line\n"), 0o644); err != nil {
		t.Fatalf("write old.txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectPath, ".gitignore"), []byte("notes_dir/ignored.log\n"), 0o644); err != nil {
		t.Fatalf("write .gitignore: %v", err)
	}
	runGit(t, projectPath, "git", "add", "pixel.png", "old.txt", ".gitignore")
	runGit(t, projectPath, "git", "commit", "-m", "add fixtures")

	if err := os.WriteFile(filepath.Join(projectPath, "README.md"), []byte("hello\ndiff screen\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	if err := writeTestPNG(filepath.Join(projectPath, "pixel.png"), color.RGBA{R: 32, G: 120, B: 220, A: 255}); err != nil {
		t.Fatalf("write updated image: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectPath, "notes.txt"), []byte("release note\n"), 0o644); err != nil {
		t.Fatalf("write notes: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(projectPath, "notes_dir", "include"), 0o755); err != nil {
		t.Fatalf("mkdir notes_dir/include: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectPath, "notes_dir", "main.cpp"), []byte("int main() {\n    return 0;\n}\n"), 0o644); err != nil {
		t.Fatalf("write notes_dir/main.cpp: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectPath, "notes_dir", "include", "main.h"), []byte("#pragma once\n"), 0o644); err != nil {
		t.Fatalf("write notes_dir/include/main.h: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectPath, "notes_dir", "ignored.log"), []byte("ignore me\n"), 0o644); err != nil {
		t.Fatalf("write notes_dir/ignored.log: %v", err)
	}
	if err := os.Remove(filepath.Join(projectPath, "old.txt")); err != nil {
		t.Fatalf("remove old.txt: %v", err)
	}

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

	svc := New(config.Default(), st, events.NewBus(), nil)
	preview, err := svc.PrepareDiff(ctx, projectPath)
	if err != nil {
		t.Fatalf("prepare diff: %v", err)
	}
	if preview.ProjectName != "repo" {
		t.Fatalf("project name = %q, want repo", preview.ProjectName)
	}
	if len(preview.Files) != 6 {
		t.Fatalf("file count = %d, want 6", len(preview.Files))
	}

	byPath := map[string]DiffFilePreview{}
	for _, file := range preview.Files {
		byPath[file.Path] = file
	}

	readme := byPath["README.md"]
	if !strings.Contains(readme.Body, "diff --git") || !strings.Contains(readme.Body, "README.md") {
		t.Fatalf("README preview = %q, want git diff content", readme.Body)
	}

	notes := byPath["notes.txt"]
	if !notes.Untracked || !strings.Contains(notes.Body, "# Untracked") || !strings.Contains(notes.Body, "+release note") {
		t.Fatalf("notes preview = %#v, want untracked added-line preview", notes)
	}

	cpp := byPath["notes_dir/main.cpp"]
	if !cpp.Untracked || !strings.Contains(cpp.Body, "+int main()") {
		t.Fatalf("expanded untracked cpp preview = %#v, want notes_dir/main.cpp", cpp)
	}
	header := byPath["notes_dir/include/main.h"]
	if !header.Untracked || !strings.Contains(header.Body, "+#pragma once") {
		t.Fatalf("expanded untracked header preview = %#v, want notes_dir/include/main.h", header)
	}
	if _, ok := byPath["notes_dir/"]; ok {
		t.Fatalf("preview should expand untracked directories into files, got directory entry %#v", byPath["notes_dir/"])
	}
	if _, ok := byPath["notes_dir/ignored.log"]; ok {
		t.Fatalf("preview should not include ignored files, got %#v", byPath["notes_dir/ignored.log"])
	}

	deleted := byPath["old.txt"]
	if deleted.Kind != scanner.GitChangeDeleted || !strings.Contains(deleted.Body, "old.txt") {
		t.Fatalf("deleted preview = %#v, want deleted file diff", deleted)
	}

	imageFile := byPath["pixel.png"]
	if !imageFile.IsImage {
		t.Fatalf("pixel.png should be marked as image: %#v", imageFile)
	}
	if len(imageFile.OldImage) == 0 || len(imageFile.NewImage) == 0 {
		t.Fatalf("image previews should include HEAD and worktree bytes: %#v", imageFile)
	}
	if !strings.Contains(imageFile.Body, "Binary image change rendered as ANSI preview.") {
		t.Fatalf("image body = %q, want image-preview note", imageFile.Body)
	}
}

func TestPrepareDiffReturnsNoChangesError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	projectPath := filepath.Join(t.TempDir(), "repo")
	initGitRepo(t, projectPath)

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

	svc := New(config.Default(), st, events.NewBus(), nil)
	_, err = svc.PrepareDiff(ctx, projectPath)
	if err == nil {
		t.Fatalf("prepare diff should fail on a clean repo")
	}

	var noDiffErr NoDiffChangesError
	if !errors.As(err, &noDiffErr) {
		t.Fatalf("prepare diff error = %v, want NoDiffChangesError", err)
	}
	if noDiffErr.ProjectName != "repo" {
		t.Fatalf("project name = %q, want repo", noDiffErr.ProjectName)
	}
}

func TestPrepareDiffReturnsNoGitRepositoryError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	projectPath := filepath.Join(t.TempDir(), "scratch")
	if err := os.MkdirAll(projectPath, 0o755); err != nil {
		t.Fatalf("create scratch dir: %v", err)
	}

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:          projectPath,
		Name:          "scratch",
		PresentOnDisk: true,
		InScope:       true,
		UpdatedAt:     time.Now().UTC(),
	}); err != nil {
		t.Fatalf("upsert project: %v", err)
	}

	svc := New(config.Default(), st, events.NewBus(), nil)
	_, err = svc.PrepareDiff(ctx, projectPath)
	if err == nil {
		t.Fatalf("prepare diff should fail outside a git repository")
	}

	var noGitErr NoGitRepositoryError
	if !errors.As(err, &noGitErr) {
		t.Fatalf("prepare diff error = %v, want NoGitRepositoryError", err)
	}
	if noGitErr.ProjectName != "scratch" {
		t.Fatalf("project name = %q, want scratch", noGitErr.ProjectName)
	}
}

func TestToggleDiffFileStageStagesAndUnstagesFile(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	projectPath := filepath.Join(t.TempDir(), "repo")
	initGitRepo(t, projectPath)

	if err := os.WriteFile(filepath.Join(projectPath, "README.md"), []byte("hello\ndiff toggle\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}

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

	svc := New(config.Default(), st, events.NewBus(), nil)
	preview, err := svc.PrepareDiff(ctx, projectPath)
	if err != nil {
		t.Fatalf("prepare diff: %v", err)
	}
	if len(preview.Files) != 1 {
		t.Fatalf("file count = %d, want 1", len(preview.Files))
	}

	status, err := svc.ToggleDiffFileStage(ctx, projectPath, preview.Files[0])
	if err != nil {
		t.Fatalf("stage file: %v", err)
	}
	if !strings.Contains(status, "Staged README.md") {
		t.Fatalf("status = %q, want staged status", status)
	}
	if got := gitOutput(t, projectPath, "git", "status", "--short"); !strings.Contains(got, "M  README.md") {
		t.Fatalf("git status after stage = %q, want staged README", got)
	}

	preview, err = svc.PrepareDiff(ctx, projectPath)
	if err != nil {
		t.Fatalf("prepare diff after stage: %v", err)
	}
	status, err = svc.ToggleDiffFileStage(ctx, projectPath, preview.Files[0])
	if err != nil {
		t.Fatalf("unstage file: %v", err)
	}
	if !strings.Contains(status, "Unstaged README.md") {
		t.Fatalf("status = %q, want unstaged status", status)
	}
	if got := gitOutput(t, projectPath, "git", "status", "--short"); !strings.Contains(got, " M README.md") {
		t.Fatalf("git status after unstage = %q, want unstaged README", got)
	}
}

func TestPrepareCommitIncludesRecommendedUntrackedFiles(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(projectPath, "scratch.txt"), []byte("personal reminder\n"), 0o644); err != nil {
		t.Fatalf("write scratch: %v", err)
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
				{Path: "notes.txt", Include: true, Confidence: 0.93, Reason: "notes.txt matches the staged README update and scratch.txt looks unrelated."},
				{Path: "scratch.txt", Include: false, Confidence: 0.18, Reason: "scratch.txt looks like a personal note."},
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
	if len(recommender.lastInput.Candidates) != 2 {
		t.Fatalf("candidate files = %d, want 2", len(recommender.lastInput.Candidates))
	}
	if len(preview.SelectedUntracked) != 1 || preview.SelectedUntracked[0].Path != "notes.txt" {
		t.Fatalf("selected untracked = %#v, want notes.txt", preview.SelectedUntracked)
	}
	if len(preview.Included) != 2 || preview.Included[0].Path != "README.md" || preview.Included[1].Path != "notes.txt" {
		t.Fatalf("included files = %#v, want README.md + notes.txt", preview.Included)
	}
	if len(preview.Excluded) != 1 || preview.Excluded[0].Path != "scratch.txt" {
		t.Fatalf("excluded files = %#v, want scratch.txt left out", preview.Excluded)
	}
	if !strings.Contains(preview.DiffStat, "notes.txt") || !strings.Contains(preview.DiffSummary, "2 files changed") {
		t.Fatalf("diff preview should reflect staged plus selected untracked files: stat=%q summary=%q", preview.DiffStat, preview.DiffSummary)
	}
	if !strings.Contains(strings.Join(preview.Warnings, "\n"), "Will also stage 1 AI-recommended untracked file before commit.") {
		t.Fatalf("warnings = %#v, want AI untracked staging note", preview.Warnings)
	}

	statusOut := gitOutput(t, projectPath, "git", "status", "--short")
	if !strings.Contains(statusOut, "M  README.md") || !strings.Contains(statusOut, "?? notes.txt") || !strings.Contains(statusOut, "?? scratch.txt") {
		t.Fatalf("prepare commit should not touch the real index, got status %q", statusOut)
	}
}

func TestPrepareCommitRecordsCommitMessageErrorWhileUsingFallback(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	projectPath := filepath.Join(t.TempDir(), "repo")
	initGitRepo(t, projectPath)

	if err := os.WriteFile(filepath.Join(projectPath, "README.md"), []byte("hello\npreview\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}

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

	svc := New(config.Default(), st, events.NewBus(), nil)
	svc.commitMessageSuggester = fakeCommitMessageSuggester{
		err: errors.New("model mlx-community/Qwen3.5-35B-A3B-4bit: EOF"),
	}

	preview, err := svc.PrepareCommit(ctx, projectPath, GitActionCommit, "")
	if err != nil {
		t.Fatalf("prepare commit: %v", err)
	}
	if preview.Message != "Update README.md" {
		t.Fatalf("commit message = %q, want fallback subject", preview.Message)
	}
	if preview.CommitMessageError != "model mlx-community/Qwen3.5-35B-A3B-4bit: EOF" {
		t.Fatalf("commit message error = %q", preview.CommitMessageError)
	}
	if warnings := strings.Join(preview.Warnings, "\n"); !strings.Contains(warnings, "AI commit message unavailable: model mlx-community/Qwen3.5-35B-A3B-4bit: EOF") {
		t.Fatalf("warnings = %#v, want AI fallback warning", preview.Warnings)
	}
}

func TestPrepareCommitTimesOutCommitMessageSuggestionAndUsesFallback(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	projectPath := filepath.Join(t.TempDir(), "repo")
	initGitRepo(t, projectPath)

	if err := os.WriteFile(filepath.Join(projectPath, "README.md"), []byte("hello\npreview\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}

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

	svc := New(config.Default(), st, events.NewBus(), nil)
	svc.commitAssistantTimeout = 10 * time.Millisecond
	svc.commitMessageSuggester = fakeCommitMessageSuggester{waitForContext: true}

	preview, err := svc.PrepareCommit(ctx, projectPath, GitActionCommit, "")
	if err != nil {
		t.Fatalf("prepare commit: %v", err)
	}
	if preview.Message != "Update README.md" {
		t.Fatalf("commit message = %q, want fallback subject", preview.Message)
	}
	if preview.CommitMessageError != "timed out after 10ms" {
		t.Fatalf("commit message error = %q", preview.CommitMessageError)
	}
	if warnings := strings.Join(preview.Warnings, "\n"); !strings.Contains(warnings, "AI commit message unavailable: timed out after 10ms") {
		t.Fatalf("warnings = %#v, want timeout warning", preview.Warnings)
	}
}

func TestPrepareCommitTimesOutUntrackedReviewAndContinues(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	projectPath := filepath.Join(t.TempDir(), "repo")
	initGitRepo(t, projectPath)

	if err := os.WriteFile(filepath.Join(projectPath, "README.md"), []byte("hello\npreview\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectPath, "notes.txt"), []byte("release note\n"), 0o644); err != nil {
		t.Fatalf("write notes: %v", err)
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

	svc := New(config.Default(), st, events.NewBus(), nil)
	svc.commitAssistantTimeout = 10 * time.Millisecond
	svc.commitMessageSuggester = nil
	svc.untrackedFileRecommender = &fakeUntrackedFileRecommender{waitForContext: true}

	preview, err := svc.PrepareCommit(ctx, projectPath, GitActionCommit, "Update repo")
	if err != nil {
		t.Fatalf("prepare commit: %v", err)
	}
	if len(preview.SelectedUntracked) != 0 {
		t.Fatalf("selected untracked = %#v, want none after timeout", preview.SelectedUntracked)
	}
	if warnings := strings.Join(preview.Warnings, "\n"); !strings.Contains(warnings, "AI untracked review unavailable: timed out after 10ms") {
		t.Fatalf("warnings = %#v, want timeout warning", preview.Warnings)
	}
}

func TestPrepareCommitReturnsNoChangesErrorWithPushContext(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	remotePath := filepath.Join(root, "origin.git")
	projectPath := filepath.Join(root, "repo")
	initBareGitRepo(t, remotePath)
	initGitRepo(t, projectPath)
	runGit(t, projectPath, "git", "remote", "add", "origin", remotePath)
	runGit(t, projectPath, "git", "push", "-u", "origin", "master")

	if err := os.WriteFile(filepath.Join(projectPath, "README.md"), []byte("hello\nahead\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	runGit(t, projectPath, "git", "add", "README.md")
	runGit(t, projectPath, "git", "commit", "-m", "ahead")

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

	svc := New(config.Default(), st, events.NewBus(), nil)
	_, err = svc.PrepareCommit(ctx, projectPath, GitActionCommit, "")
	if err == nil {
		t.Fatalf("prepare commit should fail for a clean repo")
	}

	var noChangesErr NoChangesToCommitError
	if !errors.As(err, &noChangesErr) {
		t.Fatalf("prepare commit error = %v, want NoChangesToCommitError", err)
	}
	if !noChangesErr.CanPush {
		t.Fatalf("expected no-changes error to allow push, got %#v", noChangesErr)
	}
	if noChangesErr.Ahead != 1 {
		t.Fatalf("ahead = %d, want 1", noChangesErr.Ahead)
	}
	if noChangesErr.ProjectName != "repo" {
		t.Fatalf("project name = %q, want repo", noChangesErr.ProjectName)
	}
}

func TestPrepareCommitReturnsSubmoduleAttentionErrorForDirtySubmoduleOnly(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	projectPath := filepath.Join(root, "repo")
	submoduleOriginPath := filepath.Join(root, "assets-origin")
	submodulePath := initGitRepoWithSubmodule(t, projectPath, submoduleOriginPath, "assets_src")

	if err := os.WriteFile(filepath.Join(submodulePath, "README.md"), []byte("hello\nsubmodule edit\n"), 0o644); err != nil {
		t.Fatalf("write submodule README: %v", err)
	}

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

	svc := New(config.Default(), st, events.NewBus(), nil)
	_, err = svc.PrepareCommit(ctx, projectPath, GitActionCommit, "")
	if err == nil {
		t.Fatalf("prepare commit should fail when only submodule-local changes are dirty")
	}

	var submoduleErr SubmoduleAttentionError
	if !errors.As(err, &submoduleErr) {
		t.Fatalf("prepare commit error = %v, want SubmoduleAttentionError", err)
	}
	if len(submoduleErr.Submodules) != 1 || submoduleErr.Submodules[0] != "assets_src" {
		t.Fatalf("submodules = %#v, want assets_src", submoduleErr.Submodules)
	}
}

func TestPrepareCommitReturnsSubmoduleAttentionErrorForUnpushedSubmoduleOnly(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	projectPath := filepath.Join(root, "repo")
	submoduleRootPath := filepath.Join(root, "assets")
	submodulePath := initGitRepoWithPushableSubmodule(t, projectPath, submoduleRootPath, "assets_src")

	if err := os.WriteFile(filepath.Join(submodulePath, "README.md"), []byte("hello\nlocal commit\n"), 0o644); err != nil {
		t.Fatalf("write submodule README: %v", err)
	}
	runGit(t, submodulePath, "git", "add", "README.md")
	runGit(t, submodulePath, "git", "commit", "-m", "local submodule commit")
	runGit(t, projectPath, "git", "add", "assets_src")
	runGit(t, projectPath, "git", "commit", "-m", "record local submodule commit")

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

	svc := New(config.Default(), st, events.NewBus(), nil)
	_, err = svc.PrepareCommit(ctx, projectPath, GitActionCommit, "")
	if err == nil {
		t.Fatalf("prepare commit should fail when a submodule has unpushed commits")
	}

	var submoduleErr SubmoduleAttentionError
	if !errors.As(err, &submoduleErr) {
		t.Fatalf("prepare commit error = %v, want SubmoduleAttentionError", err)
	}
	if len(submoduleErr.UnpushedSubmodules) != 1 || submoduleErr.UnpushedSubmodules[0] != "assets_src" {
		t.Fatalf("unpushed submodules = %#v, want assets_src", submoduleErr.UnpushedSubmodules)
	}
	if len(submoduleErr.DirtySubmodules) != 0 {
		t.Fatalf("dirty submodules = %#v, want none", submoduleErr.DirtySubmodules)
	}
}

func TestResolveSubmodulesAndPrepareCommitPushesUnpushedSubmoduleWithoutParentCommit(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	projectPath := filepath.Join(root, "repo")
	submoduleRootPath := filepath.Join(root, "assets")
	submodulePath := initGitRepoWithPushableSubmodule(t, projectPath, submoduleRootPath, "assets_src")

	if err := os.WriteFile(filepath.Join(submodulePath, "README.md"), []byte("hello\nlocal commit\n"), 0o644); err != nil {
		t.Fatalf("write submodule README: %v", err)
	}
	runGit(t, submodulePath, "git", "add", "README.md")
	runGit(t, submodulePath, "git", "commit", "-m", "local submodule commit")
	runGit(t, projectPath, "git", "add", "assets_src")
	runGit(t, projectPath, "git", "commit", "-m", "record local submodule commit")

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

	svc := New(config.Default(), st, events.NewBus(), nil)
	_, err = svc.ResolveSubmodulesAndPrepareCommit(ctx, projectPath, GitActionCommit, "")
	if err == nil {
		t.Fatalf("resolve submodules should report no parent commit after pushing")
	}
	var resolvedErr SubmoduleResolvedNoParentChangesError
	if !errors.As(err, &resolvedErr) {
		t.Fatalf("resolve submodules error = %v, want SubmoduleResolvedNoParentChangesError", err)
	}
	if !strings.Contains(resolvedErr.Summary, "Pushed existing commits from submodule assets_src") {
		t.Fatalf("resolved summary = %q, want pushed submodule note", resolvedErr.Summary)
	}

	currentSubmoduleHead := strings.TrimSpace(gitOutput(t, submodulePath, "git", "rev-parse", "HEAD"))
	remoteHead := strings.TrimSpace(gitOutput(t, filepath.Join(submoduleRootPath, "origin.git"), "git", "rev-parse", "master"))
	if remoteHead != currentSubmoduleHead {
		t.Fatalf("expected pushed submodule HEAD %q to match remote %q", currentSubmoduleHead, remoteHead)
	}
}

func TestSetRunCommandPublishesActionAndPersistsEvent(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	projectPath := "/tmp/runtime-project"
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:           projectPath,
		Name:           "runtime-project",
		Status:         model.StatusIdle,
		AttentionScore: 10,
		InScope:        true,
		UpdatedAt:      time.Now().UTC(),
	}); err != nil {
		t.Fatalf("upsert project: %v", err)
	}

	bus := events.NewBus()
	sub, unsub := bus.Subscribe(1)
	defer unsub()

	svc := New(config.Default(), st, bus, nil)
	if err := svc.SetRunCommand(ctx, projectPath, "pnpm dev"); err != nil {
		t.Fatalf("SetRunCommand() error = %v", err)
	}

	select {
	case evt := <-sub:
		if evt.Type != events.ActionApplied {
			t.Fatalf("event type = %s, want %s", evt.Type, events.ActionApplied)
		}
		if evt.ProjectPath != projectPath {
			t.Fatalf("event project path = %q, want %q", evt.ProjectPath, projectPath)
		}
		if evt.Payload["action"] != "set_run_command" {
			t.Fatalf("event action = %q, want set_run_command", evt.Payload["action"])
		}
	default:
		t.Fatalf("expected ActionApplied event")
	}

	detail, err := st.GetProjectDetail(ctx, projectPath, 5)
	if err != nil {
		t.Fatalf("GetProjectDetail() error = %v", err)
	}
	if detail.Summary.RunCommand != "pnpm dev" {
		t.Fatalf("run command = %q, want pnpm dev", detail.Summary.RunCommand)
	}
	if len(detail.RecentEvents) == 0 || detail.RecentEvents[0].Payload != "set_run_command" {
		t.Fatalf("expected stored set_run_command event, got %#v", detail.RecentEvents)
	}
}

func TestPrepareCommitAndApplyCommitLeaveDirtySubmoduleOutOfParentCommit(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	projectPath := filepath.Join(root, "repo")
	submoduleOriginPath := filepath.Join(root, "assets-origin")
	submodulePath := initGitRepoWithSubmodule(t, projectPath, submoduleOriginPath, "assets_src")

	if err := os.WriteFile(filepath.Join(projectPath, "README.md"), []byte("hello\nship parent update\n"), 0o644); err != nil {
		t.Fatalf("write parent README: %v", err)
	}
	if err := os.WriteFile(filepath.Join(submodulePath, "README.md"), []byte("hello\nsubmodule edit\n"), 0o644); err != nil {
		t.Fatalf("write submodule README: %v", err)
	}

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

	svc := New(config.Default(), st, events.NewBus(), nil)
	svc.commitMessageSuggester = nil

	preview, err := svc.PrepareCommit(ctx, projectPath, GitActionFinish, "Ship parent update")
	if err != nil {
		t.Fatalf("prepare commit: %v", err)
	}
	if preview.StageMode != GitStageAllChanges {
		t.Fatalf("stage mode = %s, want %s", preview.StageMode, GitStageAllChanges)
	}
	if len(preview.Included) != 1 || preview.Included[0].Path != "README.md" {
		t.Fatalf("included files = %#v, want parent README only", preview.Included)
	}
	if len(preview.Excluded) != 1 || preview.Excluded[0].Path != "assets_src" {
		t.Fatalf("excluded files = %#v, want dirty assets_src submodule", preview.Excluded)
	}
	if strings.Contains(preview.DiffStat, "assets_src") {
		t.Fatalf("diff stat should exclude dirty-only submodule changes, got %q", preview.DiffStat)
	}
	if warnings := strings.Join(preview.Warnings, "\n"); !strings.Contains(warnings, "Submodule assets_src has local changes inside it.") {
		t.Fatalf("warnings = %#v, want submodule guidance", preview.Warnings)
	}

	result, err := svc.ApplyCommit(ctx, preview, false, nil)
	if err != nil {
		t.Fatalf("apply commit: %v", err)
	}
	if result.Pushed {
		t.Fatalf("commit-only flow should not push, got %#v", result)
	}

	headFiles := gitOutput(t, projectPath, "git", "show", "--name-only", "--format=", "HEAD")
	if !strings.Contains(headFiles, "README.md") || strings.Contains(headFiles, "assets_src") {
		t.Fatalf("HEAD files = %q, want parent README only", headFiles)
	}

	statusOut := gitOutput(t, projectPath, "git", "status", "--short")
	if !strings.Contains(statusOut, "assets_src") || strings.Contains(statusOut, "README.md") {
		t.Fatalf("post-commit status = %q, want only dirty submodule left", statusOut)
	}
}

func TestResolveSubmodulesAndPrepareCommitCommitsPushesSubmoduleAndReturnsParentPreview(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	projectPath := filepath.Join(root, "repo")
	submoduleRootPath := filepath.Join(root, "assets")
	submodulePath := initGitRepoWithPushableSubmodule(t, projectPath, submoduleRootPath, "assets_src")
	initialSubmoduleHead := strings.TrimSpace(gitOutput(t, submodulePath, "git", "rev-parse", "HEAD"))

	if err := os.WriteFile(filepath.Join(submodulePath, "README.md"), []byte("hello\nsubmodule edit\n"), 0o644); err != nil {
		t.Fatalf("write submodule README: %v", err)
	}

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

	svc := New(config.Default(), st, events.NewBus(), nil)
	svc.commitMessageSuggester = nil

	preview, err := svc.ResolveSubmodulesAndPrepareCommit(ctx, projectPath, GitActionCommit, "Update parent after assets refresh")
	if err != nil {
		t.Fatalf("resolve submodules and prepare commit: %v", err)
	}
	if len(preview.Included) != 1 || preview.Included[0].Path != "assets_src" {
		t.Fatalf("included files = %#v, want staged submodule hash only", preview.Included)
	}
	if !strings.Contains(strings.Join(preview.Warnings, "\n"), "Resolved submodule assets_src") {
		t.Fatalf("warnings = %#v, want resolved submodule note", preview.Warnings)
	}

	currentSubmoduleHead := strings.TrimSpace(gitOutput(t, submodulePath, "git", "rev-parse", "HEAD"))
	if currentSubmoduleHead == "" || currentSubmoduleHead == initialSubmoduleHead {
		t.Fatalf("expected submodule HEAD to advance, got %q -> %q", initialSubmoduleHead, currentSubmoduleHead)
	}
	remoteHead := strings.TrimSpace(gitOutput(t, filepath.Join(submoduleRootPath, "origin.git"), "git", "rev-parse", "master"))
	if remoteHead != currentSubmoduleHead {
		t.Fatalf("expected pushed submodule HEAD %q to match remote %q", currentSubmoduleHead, remoteHead)
	}

	submoduleStatus := strings.TrimSpace(gitOutput(t, submodulePath, "git", "status", "--short"))
	if submoduleStatus != "" {
		t.Fatalf("expected clean submodule after assisted commit/push, got %q", submoduleStatus)
	}
}

func TestResolveSubmodulesAndPrepareCommitBranchesDetachedNestedSubmoduleWorktree(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	projectPath := filepath.Join(root, "repo")
	submoduleRootPath := filepath.Join(root, "assets")
	initGitRepoWithPushableSubmodule(t, projectPath, submoduleRootPath, "assets_src")
	worktreePath := filepath.Join(root, "repo--feat-detached-assets")
	runGit(t, projectPath, "git", "worktree", "add", "-b", "feat/detached-assets", worktreePath, "HEAD")

	prep, err := worktreeprep.Prepare(ctx, projectPath, worktreePath, "")
	if err != nil {
		t.Fatalf("prepare worktree: %v", err)
	}
	if len(prep.Prepared) != 1 || prep.Prepared[0].Mode != "worktree" {
		t.Fatalf("prepared submodules = %#v, want nested worktree", prep.Prepared)
	}

	submodulePath := filepath.Join(worktreePath, "assets_src")
	if branch := strings.TrimSpace(gitOutput(t, submodulePath, "git", "rev-parse", "--abbrev-ref", "HEAD")); branch != "HEAD" {
		t.Fatalf("prepared submodule branch = %q, want detached HEAD", branch)
	}
	if err := os.WriteFile(filepath.Join(submodulePath, "README.md"), []byte("hello\nnested submodule edit\n"), 0o644); err != nil {
		t.Fatalf("write submodule README: %v", err)
	}

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:          worktreePath,
		Name:          filepath.Base(worktreePath),
		PresentOnDisk: true,
		InScope:       true,
		UpdatedAt:     time.Now().UTC(),
	}); err != nil {
		t.Fatalf("upsert project: %v", err)
	}

	svc := New(config.Default(), st, events.NewBus(), nil)
	svc.commitMessageSuggester = nil

	preview, err := svc.ResolveSubmodulesAndPrepareCommit(ctx, worktreePath, GitActionCommit, "Record nested asset update")
	if err != nil {
		t.Fatalf("resolve submodules and prepare commit: %v", err)
	}
	if len(preview.Included) != 1 || preview.Included[0].Path != "assets_src" {
		t.Fatalf("included files = %#v, want staged submodule hash only", preview.Included)
	}
	warnings := strings.Join(preview.Warnings, "\n")
	if !strings.Contains(warnings, "Resolved submodule assets_src") || !strings.Contains(warnings, "on branch lcroom/feat-detached-assets/assets_src-") {
		t.Fatalf("warnings = %#v, want resolved submodule branch note", preview.Warnings)
	}

	resolutionBranch := strings.TrimSpace(gitOutput(t, submodulePath, "git", "rev-parse", "--abbrev-ref", "HEAD"))
	if !strings.HasPrefix(resolutionBranch, "lcroom/feat-detached-assets/assets_src-") {
		t.Fatalf("resolution branch = %q, want LCR submodule branch", resolutionBranch)
	}
	submoduleHead := strings.TrimSpace(gitOutput(t, submodulePath, "git", "rev-parse", "HEAD"))
	remoteHead := strings.TrimSpace(gitOutput(t, filepath.Join(submoduleRootPath, "origin.git"), "git", "rev-parse", resolutionBranch))
	if remoteHead != submoduleHead {
		t.Fatalf("expected pushed submodule HEAD %q to match remote %q", submoduleHead, remoteHead)
	}

	result, err := svc.ApplyCommit(ctx, preview, false, nil)
	if err != nil {
		t.Fatalf("apply parent commit: %v", err)
	}
	if result.Pushed {
		t.Fatalf("parent commit should not push in commit-only flow, got %#v", result)
	}
	recordedSubmoduleHead := strings.TrimSpace(gitOutput(t, worktreePath, "git", "rev-parse", "HEAD:assets_src"))
	if recordedSubmoduleHead != submoduleHead {
		t.Fatalf("parent gitlink = %q, want resolved submodule HEAD %q", recordedSubmoduleHead, submoduleHead)
	}
}

func TestApplyCommitStagesRecommendedUntrackedFiles(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(projectPath, "scratch.txt"), []byte("personal reminder\n"), 0o644); err != nil {
		t.Fatalf("write scratch: %v", err)
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

	svc := New(config.Default(), st, events.NewBus(), nil)
	svc.commitMessageSuggester = nil
	svc.untrackedFileRecommender = &fakeUntrackedFileRecommender{
		suggestion: gitops.UntrackedFileRecommendationResult{
			Files: []gitops.UntrackedFileDecision{
				{Path: "notes.txt", Include: true, Confidence: 0.95, Reason: "notes.txt matches the staged README update."},
				{Path: "scratch.txt", Include: false, Confidence: 0.11, Reason: "scratch.txt looks unrelated."},
			},
		},
	}

	preview, err := svc.PrepareCommit(ctx, projectPath, GitActionCommit, "Update repo")
	if err != nil {
		t.Fatalf("prepare commit: %v", err)
	}

	result, err := svc.ApplyCommit(ctx, preview, false, nil)
	if err != nil {
		t.Fatalf("apply commit: %v", err)
	}
	if result.Pushed {
		t.Fatalf("commit-only flow should not push, got %#v", result)
	}

	headFiles := gitOutput(t, projectPath, "git", "show", "--name-only", "--format=", "HEAD")
	if !strings.Contains(headFiles, "README.md") || !strings.Contains(headFiles, "notes.txt") || strings.Contains(headFiles, "scratch.txt") {
		t.Fatalf("HEAD files = %q, want README.md and notes.txt only", headFiles)
	}

	statusOut := gitOutput(t, projectPath, "git", "status", "--short")
	if strings.Contains(statusOut, "notes.txt") || !strings.Contains(statusOut, "?? scratch.txt") {
		t.Fatalf("post-commit status = %q, want scratch.txt only", statusOut)
	}
}

func TestApplyCommitStagesAllAndPushes(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	remotePath := filepath.Join(root, "origin.git")
	projectPath := filepath.Join(root, "repo")
	initBareGitRepo(t, remotePath)
	initGitRepo(t, projectPath)
	runGit(t, projectPath, "git", "remote", "add", "origin", remotePath)
	runGit(t, projectPath, "git", "push", "-u", "origin", "master")

	if err := os.WriteFile(filepath.Join(projectPath, "README.md"), []byte("hello\nrelease notes\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectPath, "notes.txt"), []byte("ship it\n"), 0o644); err != nil {
		t.Fatalf("write notes: %v", err)
	}

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

	svc := New(config.Default(), st, events.NewBus(), nil)
	svc.commitMessageSuggester = nil

	preview, err := svc.PrepareCommit(ctx, projectPath, GitActionFinish, "Ship current repo changes")
	if err != nil {
		t.Fatalf("prepare commit: %v", err)
	}
	if preview.StageMode != GitStageAllChanges {
		t.Fatalf("stage mode = %s, want %s", preview.StageMode, GitStageAllChanges)
	}

	result, err := svc.ApplyCommit(ctx, preview, true, nil)
	if err != nil {
		t.Fatalf("apply commit: %v", err)
	}
	if !result.Pushed {
		t.Fatalf("expected commit result to include push, got %#v", result)
	}

	statusOut := gitOutput(t, projectPath, "git", "status", "--short")
	if strings.TrimSpace(statusOut) != "" {
		t.Fatalf("expected clean worktree after apply, got %q", statusOut)
	}

	head := strings.TrimSpace(gitOutput(t, projectPath, "git", "rev-parse", "HEAD"))
	upstream := strings.TrimSpace(gitOutput(t, projectPath, "git", "rev-parse", "@{u}"))
	if head == "" || upstream == "" || head != upstream {
		t.Fatalf("expected local HEAD %q to match upstream %q", head, upstream)
	}
}

func TestScanOnceQueuesAndProcessesCommitTodoCheckForExternalCommit(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	projectPath := filepath.Join(t.TempDir(), "repo")
	initGitRepo(t, projectPath)
	if realPath, err := filepath.EvalSymlinks(projectPath); err == nil {
		projectPath = realPath
	}

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

	cfg := config.Default()
	cfg.IncludePaths = nil
	svc := New(cfg, st, events.NewBus(), nil)
	svc.refreshProjectStatusFn = func(context.Context, string) error { return nil }
	svc.commitTodoChecker = &fakeCommitTodoChecker{model: "fake-todo-checker"}
	if _, err := svc.ScanOnce(ctx); err != nil {
		t.Fatalf("baseline scan: %v", err)
	}

	item, err := svc.AddTodo(ctx, projectPath, "Add release notes for the shipped workflow")
	if err != nil {
		t.Fatalf("add todo: %v", err)
	}
	checker := &fakeCommitTodoChecker{
		model: "fake-todo-checker",
		suggestion: gitops.CommitTodoCompletionSuggestion{
			Model: "fake-todo-checker-v2",
			CompletedTodos: []gitops.CommitTodoCompletionDecision{{
				ID:         item.ID,
				Reason:     "The commit adds release notes.",
				Confidence: 0.92,
			}},
		},
	}
	svc.commitTodoChecker = checker

	if err := os.WriteFile(filepath.Join(projectPath, "RELEASE.md"), []byte("workflow shipped\n"), 0o644); err != nil {
		t.Fatalf("write release notes: %v", err)
	}
	runGit(t, projectPath, "git", "add", "RELEASE.md")
	runGit(t, projectPath, "git", "commit", "-m", "add release notes")
	head := strings.TrimSpace(gitOutput(t, projectPath, "git", "rev-parse", "HEAD"))

	if _, err := svc.ScanOnce(ctx); err != nil {
		t.Fatalf("scan after external commit: %v", err)
	}
	if err := svc.processOneCommitTodoCheck(ctx); err != nil {
		t.Fatalf("process commit TODO check: %v", err)
	}

	detail, err := st.GetProjectDetail(ctx, projectPath, 5)
	if err != nil {
		t.Fatalf("get detail: %v", err)
	}
	if len(detail.Todos) != 1 || !detail.Todos[0].Done {
		t.Fatalf("todo after commit check = %#v, want marked done", detail.Todos)
	}
	if checker.lastInput.HeadHash != head {
		t.Fatalf("checker head = %q, want %q", checker.lastInput.HeadHash, head)
	}
	if len(checker.lastInput.OpenTodos) != 1 || checker.lastInput.OpenTodos[0].ID != item.ID {
		t.Fatalf("checker open todos = %#v, want TODO %d", checker.lastInput.OpenTodos, item.ID)
	}
	if !strings.Contains(strings.Join(checker.lastInput.ChangedFiles, "\n"), "RELEASE.md") {
		t.Fatalf("checker changed files = %#v, want RELEASE.md", checker.lastInput.ChangedFiles)
	}
	if !strings.Contains(checker.lastInput.Patch, "workflow shipped") {
		t.Fatalf("checker patch = %q, want release notes content", checker.lastInput.Patch)
	}
	storedCheck, err := st.GetCommitTodoCheck(ctx, projectPath, head)
	if err != nil {
		t.Fatalf("get completed commit TODO check: %v", err)
	}
	if storedCheck.AttemptCount != 1 || storedCheck.Status != model.CommitTodoCheckCompleted {
		t.Fatalf("stored check = %#v, want completed first attempt", storedCheck)
	}
	if !strings.Contains(storedCheck.DecisionJSON, `"id":`+strconv.FormatInt(item.ID, 10)) {
		t.Fatalf("stored decision JSON = %q, want TODO %d", storedCheck.DecisionJSON, item.ID)
	}
	if !strings.Contains(storedCheck.EvidenceJSON, `"strategy":"complete_patch"`) {
		t.Fatalf("stored evidence JSON = %q, want complete-patch strategy", storedCheck.EvidenceJSON)
	}
}

func TestBuildCommitTodoCompletionInputSelectsFocusedEvidenceWhenPatchTruncates(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	projectPath := filepath.Join(t.TempDir(), "repo")
	initGitRepo(t, projectPath)
	if realPath, err := filepath.EvalSymlinks(projectPath); err == nil {
		projectPath = realPath
	}
	baseHash := strings.TrimSpace(gitOutput(t, projectPath, "git", "rev-parse", "HEAD"))

	noise := strings.Repeat("unrelated generated payload line\n", 1400)
	if err := os.WriteFile(filepath.Join(projectPath, "a-noise.txt"), []byte(noise), 0o644); err != nil {
		t.Fatalf("write noise file: %v", err)
	}
	relevantText := "compile Vulkan shaders before presenting the startup frame\n"
	if err := os.WriteFile(filepath.Join(projectPath, "z-startup-renderer.go"), []byte(relevantText), 0o644); err != nil {
		t.Fatalf("write relevant file: %v", err)
	}
	runGit(t, projectPath, "git", "add", "a-noise.txt", "z-startup-renderer.go")
	runGit(t, projectPath, "git", "commit", "-m", "compile Vulkan startup shaders")
	headHash := strings.TrimSpace(gitOutput(t, projectPath, "git", "rev-parse", "HEAD"))

	selector := &fakeCommitTodoEvidenceSelector{
		model: "utility-selector",
		selection: gitops.CommitTodoEvidenceSelection{
			Model: "utility-selector-actual",
			Items: []gitops.CommitTodoEvidenceSelectionItem{{
				TodoIDs:    []int64{73},
				CommitHash: headHash,
				Files:      []string{"z-startup-renderer.go"},
				Reason:     "The startup renderer is the plausible implementation evidence.",
			}},
		},
	}
	svc := &Service{commitTodoEvidenceSelector: selector}
	input, err := svc.buildCommitTodoCompletionInput(
		ctx,
		model.CommitTodoCheck{ProjectPath: projectPath, BaseHash: baseHash, HeadHash: headHash},
		model.ProjectDetail{Summary: model.ProjectSummary{Path: projectPath, Name: "renderer", RepoBranch: "master"}},
		[]gitops.CommitTodoRef{{ID: 73, Text: "Fix the startup black screen"}},
	)
	if err != nil {
		t.Fatalf("buildCommitTodoCompletionInput() error = %v", err)
	}
	if selector.calls != 1 {
		t.Fatalf("evidence selector calls = %d, want 1", selector.calls)
	}
	if len(selector.lastInput.Commits) != 1 || selector.lastInput.Commits[0].Hash != headHash {
		t.Fatalf("selector commits = %#v, want new commit", selector.lastInput.Commits)
	}
	if !slices.Contains(selector.lastInput.Commits[0].ChangedFiles, "z-startup-renderer.go") {
		t.Fatalf("selector changed files = %#v, want startup renderer", selector.lastInput.Commits[0].ChangedFiles)
	}
	if input.EvidenceStrategy != "focused_model_selection" || input.EvidenceModel != "utility-selector-actual" {
		t.Fatalf("focused evidence metadata = %q/%q", input.EvidenceStrategy, input.EvidenceModel)
	}
	if !strings.Contains(input.Patch, strings.TrimSpace(relevantText)) {
		t.Fatalf("focused patch omitted relevant evidence: %q", input.Patch)
	}
	if strings.Contains(input.Patch, "unrelated generated payload") {
		t.Fatalf("focused patch retained unrelated prefix: %q", input.Patch)
	}
	if len(input.Patch) > commitTodoFocusedPatchMaxBytes {
		t.Fatalf("focused patch bytes = %d, want <= %d", len(input.Patch), commitTodoFocusedPatchMaxBytes)
	}
	if !reflect.DeepEqual(input.ChangedFiles, []string{"z-startup-renderer.go"}) {
		t.Fatalf("focused changed files = %#v", input.ChangedFiles)
	}
}

func TestProcessCommitTodoCheckSchedulesModelFailureRetry(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	projectPath := filepath.Join(t.TempDir(), "repo")
	initGitRepo(t, projectPath)
	if realPath, err := filepath.EvalSymlinks(projectPath); err == nil {
		projectPath = realPath
	}
	baseHash := strings.TrimSpace(gitOutput(t, projectPath, "git", "rev-parse", "HEAD"))
	if err := os.WriteFile(filepath.Join(projectPath, "startup.go"), []byte("prepare startup renderer\n"), 0o644); err != nil {
		t.Fatalf("write startup change: %v", err)
	}
	runGit(t, projectPath, "git", "add", "startup.go")
	runGit(t, projectPath, "git", "commit", "-m", "prepare startup renderer")
	headHash := strings.TrimSpace(gitOutput(t, projectPath, "git", "rev-parse", "HEAD"))

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
		UpdatedAt:     time.Now(),
	}); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	if _, err := st.AddTodo(ctx, projectPath, "Fix the startup black screen"); err != nil {
		t.Fatalf("seed TODO: %v", err)
	}
	if queued, err := st.QueueCommitTodoCheck(ctx, model.CommitTodoCheck{
		ProjectPath: projectPath,
		BaseHash:    baseHash,
		HeadHash:    headHash,
		UpdatedAt:   time.Now(),
	}); err != nil || !queued {
		t.Fatalf("queue check = %v, %v", queued, err)
	}

	svc := New(config.Default(), st, events.NewBus(), nil)
	svc.commitTodoChecker = &fakeCommitTodoChecker{model: "failing-checker", err: errors.New("temporary upstream failure")}
	svc.commitTodoEvidenceSelector = nil
	if err := svc.processOneCommitTodoCheck(ctx); err != nil {
		t.Fatalf("process failed check: %v", err)
	}

	failed, err := st.GetCommitTodoCheck(ctx, projectPath, headHash)
	if err != nil {
		t.Fatalf("get failed check: %v", err)
	}
	if failed.Status != model.CommitTodoCheckFailed || failed.AttemptCount != 1 || !failed.AutoRetry {
		t.Fatalf("failed retry state = %#v", failed)
	}
	if !strings.Contains(failed.LastError, "temporary upstream failure") {
		t.Fatalf("last error = %q", failed.LastError)
	}
	if !failed.NextAttemptAt.After(time.Now().Add(45 * time.Second)) {
		t.Fatalf("next attempt = %s, want first backoff in the future", failed.NextAttemptAt)
	}
	if !strings.Contains(failed.EvidenceJSON, `"strategy":"complete_patch"`) {
		t.Fatalf("evidence JSON = %q, want persisted diagnostic", failed.EvidenceJSON)
	}
	if _, err := st.ClaimNextQueuedCommitTodoCheck(ctx, time.Minute); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("claim before retry deadline error = %v, want sql.ErrNoRows", err)
	}
}

func TestCommitTodoCheckRetryDelayAndTodoSnapshotBoundary(t *testing.T) {
	t.Parallel()

	wantDelays := map[int]time.Duration{
		0:  time.Minute,
		1:  time.Minute,
		2:  5 * time.Minute,
		5:  6 * time.Hour,
		20: 6 * time.Hour,
	}
	for attempt, want := range wantDelays {
		if got := commitTodoCheckRetryDelay(attempt); got != want {
			t.Fatalf("commitTodoCheckRetryDelay(%d) = %s, want %s", attempt, got, want)
		}
	}

	queuedAt := time.Now().Add(-time.Minute)
	svc := &Service{}
	refs, todos := svc.commitTodoRefsForDetail(context.Background(), model.ProjectDetail{
		Todos: []model.TodoItem{
			{ID: 1, ProjectPath: "/tmp/repo", Text: "Existing TODO", CreatedAt: queuedAt.Add(-time.Minute)},
			{ID: 2, ProjectPath: "/tmp/repo", Text: "Later TODO", CreatedAt: queuedAt.Add(time.Minute)},
		},
	}, queuedAt)
	if len(refs) != 1 || refs[0].ID != 1 || len(todos) != 1 || todos[0].ID != 1 {
		t.Fatalf("retry TODO snapshot = refs %#v todos %#v, want only pre-existing TODO", refs, todos)
	}
}

func TestApplyCommitRefreshesFingerprintToAvoidPostCommitTodoCheck(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	projectPath := filepath.Join(t.TempDir(), "repo")
	initGitRepo(t, projectPath)
	if realPath, err := filepath.EvalSymlinks(projectPath); err == nil {
		projectPath = realPath
	}

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

	cfg := config.Default()
	cfg.IncludePaths = nil
	svc := New(cfg, st, events.NewBus(), nil)
	svc.refreshProjectStatusFn = func(context.Context, string) error { return nil }
	svc.commitMessageSuggester = nil
	svc.commitTodoChecker = &fakeCommitTodoChecker{model: "fake-todo-checker"}
	if _, err := svc.ScanOnce(ctx); err != nil {
		t.Fatalf("baseline scan: %v", err)
	}
	if _, err := svc.AddTodo(ctx, projectPath, "Ship README update"); err != nil {
		t.Fatalf("add todo: %v", err)
	}

	if err := os.WriteFile(filepath.Join(projectPath, "README.md"), []byte("hello\nship via lcr\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	preview, err := svc.PrepareCommit(ctx, projectPath, GitActionCommit, "Ship README update")
	if err != nil {
		t.Fatalf("prepare commit: %v", err)
	}
	if _, err := svc.ApplyCommit(ctx, preview, false, nil); err != nil {
		t.Fatalf("apply commit: %v", err)
	}
	if _, err := svc.ScanOnce(ctx); err != nil {
		t.Fatalf("scan after LCR commit: %v", err)
	}
	if _, err := st.ClaimNextQueuedCommitTodoCheck(ctx, time.Minute); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("queued commit TODO check after LCR commit err = %v, want sql.ErrNoRows", err)
	}
}

func TestCommitTodoRefsForDetailIncludesLinkedOriginTodo(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	rootPath := filepath.Join(t.TempDir(), "repo")
	worktreePath := filepath.Join(t.TempDir(), "repo--todo")
	if err := st.UpsertProjectState(ctx, model.ProjectState{Path: rootPath, Name: "repo", PresentOnDisk: true, InScope: true, UpdatedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("upsert root: %v", err)
	}
	item, err := st.AddTodo(ctx, rootPath, "Finish linked worktree task")
	if err != nil {
		t.Fatalf("add todo: %v", err)
	}

	svc := New(config.Default(), st, events.NewBus(), nil)
	refs, todos := svc.commitTodoRefsForDetail(ctx, model.ProjectDetail{
		Summary: model.ProjectSummary{
			Path:                 worktreePath,
			WorktreeOriginTodoID: item.ID,
		},
	}, time.Time{})
	if len(refs) != 1 || refs[0].ID != item.ID {
		t.Fatalf("refs = %#v, want origin TODO", refs)
	}
	if len(todos) != 1 || todos[0].ProjectPath != rootPath {
		t.Fatalf("todos = %#v, want root TODO item", todos)
	}
}

func TestPushProjectReportsNothingToPushWhenBranchAlreadySynced(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	remotePath := filepath.Join(root, "origin.git")
	projectPath := filepath.Join(root, "repo")
	initBareGitRepo(t, remotePath)
	initGitRepo(t, projectPath)
	runGit(t, projectPath, "git", "remote", "add", "origin", remotePath)
	runGit(t, projectPath, "git", "push", "-u", "origin", "master")

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	svc := New(config.Default(), st, events.NewBus(), nil)

	result, err := svc.PushProject(ctx, projectPath)
	if err != nil {
		t.Fatalf("push project: %v", err)
	}
	if result.Pushed {
		t.Fatalf("expected no-op push to report Pushed=false, got %#v", result)
	}
	if result.Summary != "Nothing to push; branch already synced" {
		t.Fatalf("summary = %q, want no-op push message", result.Summary)
	}
}

func TestPushProjectWarnsPullFirstWhenRemoteRejectsStalePush(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	remotePath := filepath.Join(root, "origin.git")
	seedPath := filepath.Join(root, "seed")
	projectPath := filepath.Join(root, "repo")
	initBareGitRepo(t, remotePath)
	initGitRepo(t, seedPath)
	branch := strings.TrimSpace(gitOutput(t, seedPath, "git", "branch", "--show-current"))
	runGit(t, seedPath, "git", "remote", "add", "origin", remotePath)
	runGit(t, seedPath, "git", "push", "-u", "origin", branch)
	runGit(t, root, "git", "clone", remotePath, projectPath)
	runGit(t, projectPath, "git", "config", "user.email", "test@example.com")
	runGit(t, projectPath, "git", "config", "user.name", "Little Control Room Test")

	if err := os.WriteFile(filepath.Join(projectPath, "README.md"), []byte("hello\nlocal\n"), 0o644); err != nil {
		t.Fatalf("update local README: %v", err)
	}
	runGit(t, projectPath, "git", "add", "README.md")
	runGit(t, projectPath, "git", "commit", "-m", "local update")

	if err := os.WriteFile(filepath.Join(seedPath, "README.md"), []byte("hello\nremote\n"), 0o644); err != nil {
		t.Fatalf("update remote README: %v", err)
	}
	runGit(t, seedPath, "git", "add", "README.md")
	runGit(t, seedPath, "git", "commit", "-m", "remote update")
	runGit(t, seedPath, "git", "push")

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	svc := New(config.Default(), st, events.NewBus(), nil)

	result, err := svc.PushProject(ctx, projectPath)
	if err != nil {
		t.Fatalf("push project: %v", err)
	}
	if result.Pushed {
		t.Fatalf("stale push should not report Pushed=true, got %#v", result)
	}
	if result.Summary != pushRejectedNeedsPullWarning {
		t.Fatalf("summary = %q, want pull-first warning", result.Summary)
	}
}

func TestPullProjectPullsFreshRemoteChanges(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	remotePath := filepath.Join(root, "origin.git")
	seedPath := filepath.Join(root, "seed")
	projectPath := filepath.Join(root, "repo")
	initBareGitRepo(t, remotePath)
	initGitRepo(t, seedPath)
	branch := strings.TrimSpace(gitOutput(t, seedPath, "git", "branch", "--show-current"))
	runGit(t, seedPath, "git", "remote", "add", "origin", remotePath)
	runGit(t, seedPath, "git", "push", "-u", "origin", branch)
	runGit(t, root, "git", "clone", remotePath, projectPath)

	if err := os.WriteFile(filepath.Join(seedPath, "README.md"), []byte("hello\nremote\n"), 0o644); err != nil {
		t.Fatalf("update seed README: %v", err)
	}
	runGit(t, seedPath, "git", "add", "README.md")
	runGit(t, seedPath, "git", "commit", "-m", "remote update")
	runGit(t, seedPath, "git", "push")

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	svc := New(config.Default(), st, events.NewBus(), nil)

	result, err := svc.PullProject(ctx, projectPath)
	if err != nil {
		t.Fatalf("pull project: %v", err)
	}
	if !result.Pulled {
		t.Fatalf("expected pull command to run, got %#v", result)
	}
	if result.Summary != "Pull complete" {
		t.Fatalf("summary = %q, want pull completion message", result.Summary)
	}
	content, err := os.ReadFile(filepath.Join(projectPath, "README.md"))
	if err != nil {
		t.Fatalf("read project README: %v", err)
	}
	if !strings.Contains(string(content), "remote") {
		t.Fatalf("README content = %q, want pulled remote update", string(content))
	}
}

type fakeDetector struct {
	activities map[string]*model.DetectorProjectActivity
}

type fakeUntrackedFileRecommender struct {
	lastInput      gitops.UntrackedFileRecommendationInput
	suggestion     gitops.UntrackedFileRecommendationResult
	err            error
	waitForContext bool
}

type fakeCommitMessageSuggester struct {
	suggestion     gitops.CommitMessageSuggestion
	err            error
	waitForContext bool
}

type fakeCommitTodoChecker struct {
	lastInput  gitops.CommitTodoCompletionInput
	suggestion gitops.CommitTodoCompletionSuggestion
	err        error
	model      string
}

type fakeCommitTodoEvidenceSelector struct {
	lastInput gitops.CommitTodoEvidenceSelectionInput
	selection gitops.CommitTodoEvidenceSelection
	err       error
	model     string
	calls     int
}

func (f *fakeUntrackedFileRecommender) RecommendUntracked(ctx context.Context, input gitops.UntrackedFileRecommendationInput) (gitops.UntrackedFileRecommendationResult, error) {
	f.lastInput = input
	if f.waitForContext {
		<-ctx.Done()
		return gitops.UntrackedFileRecommendationResult{}, ctx.Err()
	}
	if f.err != nil {
		return gitops.UntrackedFileRecommendationResult{}, f.err
	}
	return f.suggestion, nil
}

func (f *fakeUntrackedFileRecommender) ModelName() string {
	return "fake-untracked-reviewer"
}

func (f fakeCommitMessageSuggester) Suggest(ctx context.Context, _ gitops.CommitMessageInput) (gitops.CommitMessageSuggestion, error) {
	if f.waitForContext {
		<-ctx.Done()
		return gitops.CommitMessageSuggestion{}, ctx.Err()
	}
	if f.err != nil {
		return gitops.CommitMessageSuggestion{}, f.err
	}
	return f.suggestion, nil
}

func (f fakeCommitMessageSuggester) ModelName() string {
	return "fake-commit-suggester"
}

func (f *fakeCommitTodoChecker) CheckCompletedTodos(_ context.Context, input gitops.CommitTodoCompletionInput) (gitops.CommitTodoCompletionSuggestion, error) {
	f.lastInput = input
	if f.err != nil {
		return gitops.CommitTodoCompletionSuggestion{}, f.err
	}
	return f.suggestion, nil
}

func (f *fakeCommitTodoChecker) ModelName() string {
	if strings.TrimSpace(f.model) != "" {
		return strings.TrimSpace(f.model)
	}
	return "fake-commit-todo-checker"
}

func (f *fakeCommitTodoEvidenceSelector) SelectCommitTodoEvidence(_ context.Context, input gitops.CommitTodoEvidenceSelectionInput) (gitops.CommitTodoEvidenceSelection, error) {
	f.calls++
	f.lastInput = input
	return f.selection, f.err
}

func (f *fakeCommitTodoEvidenceSelector) ModelName() string {
	if strings.TrimSpace(f.model) != "" {
		return strings.TrimSpace(f.model)
	}
	return "fake-commit-todo-evidence-selector"
}

func (d *fakeDetector) Name() string {
	return "fake"
}

func (d *fakeDetector) Detect(context.Context, scanner.PathScope) (map[string]*model.DetectorProjectActivity, error) {
	if d.activities == nil {
		return map[string]*model.DetectorProjectActivity{}, nil
	}
	return d.activities, nil
}

func fakeActivity(projectPath, sessionID string, at time.Time) *model.DetectorProjectActivity {
	return &model.DetectorProjectActivity{
		ProjectPath:  projectPath,
		LastActivity: at,
		Source:       "codex",
		Sessions: []model.SessionEvidence{{
			SessionID:           sessionID,
			ProjectPath:         projectPath,
			DetectedProjectPath: projectPath,
			SessionFile:         filepath.Join(filepath.Dir(projectPath), sessionID+".jsonl"),
			Format:              "modern",
			StartedAt:           at.Add(-2 * time.Minute),
			LastEventAt:         at,
		}},
	}
}

func writeTestPNG(path string, fill color.RGBA) error {
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			img.SetRGBA(x, y, fill)
		}
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}

func initGitRepo(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	runGit(t, path, "git", "init")
	runGit(t, path, "git", "config", "user.email", "test@example.com")
	runGit(t, path, "git", "config", "user.name", "Little Control Room Test")
	if err := os.WriteFile(filepath.Join(path, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	runGit(t, path, "git", "add", "README.md")
	runGit(t, path, "git", "commit", "-m", "initial")
}

func initGitRepoWithSubmodule(t *testing.T, projectPath, submoduleOriginPath, submoduleName string) string {
	t.Helper()
	initGitRepo(t, submoduleOriginPath)
	initGitRepo(t, projectPath)
	runGit(t, projectPath, "git", "-c", "protocol.file.allow=always", "submodule", "add", submoduleOriginPath, submoduleName)
	runGit(t, projectPath, "git", "commit", "-m", "add submodule")
	return filepath.Join(projectPath, submoduleName)
}

func initGitRepoWithPushableSubmodule(t *testing.T, projectPath, submoduleRootPath, submoduleName string) string {
	t.Helper()
	seedPath := filepath.Join(submoduleRootPath, "seed")
	originPath := filepath.Join(submoduleRootPath, "origin.git")
	initBareGitRepo(t, originPath)
	initGitRepo(t, seedPath)
	runGit(t, seedPath, "git", "remote", "add", "origin", originPath)
	runGit(t, seedPath, "git", "push", "-u", "origin", "master")

	initGitRepo(t, projectPath)
	runGit(t, projectPath, "git", "-c", "protocol.file.allow=always", "submodule", "add", originPath, submoduleName)
	runGit(t, projectPath, "git", "commit", "-m", "add submodule")
	return filepath.Join(projectPath, submoduleName)
}
