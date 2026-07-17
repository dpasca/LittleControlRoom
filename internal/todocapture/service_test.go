package todocapture

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lcroom/internal/model"
	"lcroom/internal/store"
)

func TestServiceResolvesLinkedWorktreeAndNestedRowsToRepositoryRoot(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root, linked := createTodoCaptureWorktree(t)
	st, err := store.Open(filepath.Join(t.TempDir(), "lcr.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	upsertCaptureProject(t, st, model.ProjectState{Path: root, Name: "root", WorktreeKind: model.WorktreeKindMain, WorktreeRootPath: root})
	upsertCaptureProject(t, st, model.ProjectState{Path: linked, Name: "linked", WorktreeKind: model.WorktreeKindLinked, WorktreeRootPath: root})
	nested := filepath.Join(linked, "packages", "ui")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	// Even if a stale/manual project row exists for a nested directory, live Git
	// scope must win and redirect capture to the repository root.
	upsertCaptureProject(t, st, model.ProjectState{Path: nested, Name: "ui"})

	svc := NewService(st, ModeExplicit)
	listed, err := svc.List(ctx, Origin{ProjectPath: nested, Provider: "codex"})
	if err != nil {
		t.Fatalf("list from nested worktree: %v", err)
	}
	if listed.Scope.ProjectPath != root || !listed.Scope.FromWorktree {
		t.Fatalf("scope = %#v, want repository root %s", listed.Scope, root)
	}
	added, err := svc.Add(ctx, Origin{ProjectPath: nested, Provider: "codex"}, AddRequest{
		Text:           "Add keyboard navigation",
		CaptureKind:    CaptureExplicitRequest,
		ReviewRevision: listed.ReviewRevision,
	})
	if err != nil {
		t.Fatalf("add from nested worktree: %v", err)
	}
	if added.Disposition != DispositionCreated || added.Todo == nil || added.Todo.Text != "Add keyboard navigation" {
		t.Fatalf("add result = %#v", added)
	}
	rootTodos, _, err := st.ListOpenTodosForReview(ctx, root)
	if err != nil || len(rootTodos) != 1 {
		t.Fatalf("root TODOs = %#v, err = %v", rootTodos, err)
	}
	nestedTodos, _, err := st.ListOpenTodosForReview(ctx, nested)
	if err != nil || len(nestedTodos) != 0 {
		t.Fatalf("nested TODOs = %#v, err = %v", nestedTodos, err)
	}
}

func TestServiceResolvesUniqueSymlinkSpellingAndFailsClosedOnAmbiguity(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root, _ := createTodoCaptureWorktree(t)
	alias := filepath.Join(t.TempDir(), "repo-alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "lcr.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	upsertCaptureProject(t, st, model.ProjectState{Path: alias, Name: "alias", WorktreeKind: model.WorktreeKindMain, WorktreeRootPath: alias})
	svc := NewService(st, ModeExplicit)
	listed, err := svc.List(ctx, Origin{ProjectPath: root})
	if err != nil {
		t.Fatalf("resolve symlink spelling: %v", err)
	}
	if listed.Scope.ProjectPath != alias {
		t.Fatalf("resolved project = %s, want stored alias %s", listed.Scope.ProjectPath, alias)
	}

	upsertCaptureProject(t, st, model.ProjectState{Path: root, Name: "physical", WorktreeKind: model.WorktreeKindMain, WorktreeRootPath: root})
	_, err = svc.List(ctx, Origin{ProjectPath: root})
	if err == nil || !strings.Contains(err.Error(), "multiple loaded") {
		t.Fatalf("ambiguous exact-path error = %v", err)
	}

	secondAlias := filepath.Join(t.TempDir(), "second-alias")
	if err := os.Symlink(root, secondAlias); err != nil {
		t.Fatal(err)
	}
	_, err = svc.List(ctx, Origin{ProjectPath: secondAlias})
	if err == nil || !strings.Contains(err.Error(), "multiple loaded") {
		t.Fatalf("ambiguous symlink error = %v", err)
	}

	upsertCaptureProject(t, st, model.ProjectState{Path: alias, Name: "hidden-alias", WorktreeKind: model.WorktreeKindMain, WorktreeRootPath: alias, Forgotten: true})
	listed, err = svc.List(ctx, Origin{ProjectPath: secondAlias})
	if err != nil {
		t.Fatalf("resolve with forgotten alias filtered: %v", err)
	}
	if listed.Scope.ProjectPath != root {
		t.Fatalf("resolved project with forgotten alias = %s, want %s", listed.Scope.ProjectPath, root)
	}
}

func TestServiceEnforcesCurrentRuntimeModeAfterLaunch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root, _ := createTodoCaptureWorktree(t)
	st, err := store.Open(filepath.Join(t.TempDir(), "lcr.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	upsertCaptureProject(t, st, model.ProjectState{Path: root, Name: "root", WorktreeKind: model.WorktreeKindMain, WorktreeRootPath: root})
	if err := st.SetRuntimeSetting(ctx, RuntimeModeSettingKey, string(ModeExplicitAndClearDeferrals)); err != nil {
		t.Fatal(err)
	}
	svc := NewExternalService(st, ModeExplicitAndClearDeferrals)
	listed, err := svc.List(ctx, Origin{ProjectPath: root})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetRuntimeSetting(ctx, RuntimeModeSettingKey, string(ModeExplicit)); err != nil {
		t.Fatal(err)
	}
	_, err = svc.Add(ctx, Origin{ProjectPath: root}, AddRequest{Text: "Later work", CaptureKind: CaptureClearDeferral, ReviewRevision: listed.ReviewRevision})
	if err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("clear deferral after downgrade error = %v", err)
	}
	if err := st.SetRuntimeSetting(ctx, RuntimeModeSettingKey, string(ModeExplicitAndClearDeferrals)); err != nil {
		t.Fatal(err)
	}
	narrowSvc := NewExternalService(st, ModeExplicit)
	narrowList, err := narrowSvc.List(ctx, Origin{ProjectPath: root})
	if err != nil {
		t.Fatal(err)
	}
	if narrowList.CaptureMode != ModeExplicit {
		t.Fatalf("launch-time explicit mode expanded live to %q", narrowList.CaptureMode)
	}
	_, err = narrowSvc.Add(ctx, Origin{ProjectPath: root}, AddRequest{Text: "Still later work", CaptureKind: CaptureClearDeferral, ReviewRevision: narrowList.ReviewRevision})
	if err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("clear deferral without reconnect error = %v", err)
	}
	if err := st.SetRuntimeSetting(ctx, RuntimeModeSettingKey, string(ModeOff)); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.List(ctx, Origin{ProjectPath: root}); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("list after revocation error = %v", err)
	}
}

func createTodoCaptureWorktree(t *testing.T) (string, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "init")
	runGit(t, root, "config", "user.email", "test@example.com")
	runGit(t, root, "config", "user.name", "LCR Test")
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "add", "README.md")
	runGit(t, root, "commit", "-m", "initial")
	linked := filepath.Join(t.TempDir(), "linked")
	runGit(t, root, "worktree", "add", "-b", "capture-test", linked)
	root, _ = filepath.Abs(root)
	linked, _ = filepath.Abs(linked)
	return filepath.Clean(root), filepath.Clean(linked)
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
}

func upsertCaptureProject(t *testing.T, st *store.Store, state model.ProjectState) {
	t.Helper()
	state.Status = model.StatusIdle
	state.InScope = true
	state.PresentOnDisk = true
	state.UpdatedAt = time.Now()
	if err := st.UpsertProjectState(context.Background(), state); err != nil {
		t.Fatalf("upsert project %s: %v", state.Path, err)
	}
}
