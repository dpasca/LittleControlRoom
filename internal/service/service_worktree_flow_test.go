package service

import (
	"context"
	"database/sql"
	"errors"
	"lcroom/internal/config"
	"lcroom/internal/events"
	"lcroom/internal/model"
	"lcroom/internal/scanner"
	"lcroom/internal/store"
	"lcroom/internal/worktreeprep"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAddTodoAndUpdateQueueWorktreeSuggestionsImmediately(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	projectPath := filepath.Join(root, "repo")
	initGitRepo(t, projectPath)

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	cfg := config.Default()
	cfg.AIBackend = config.AIBackendOpenAIAPI
	cfg.OpenAIAPIKey = "test-key"
	svc := New(cfg, st, events.NewBus(), nil)
	if _, err := svc.CreateOrAttachProject(ctx, CreateOrAttachProjectRequest{
		ParentPath: root,
		Name:       "repo",
	}); err != nil {
		t.Fatalf("track root project: %v", err)
	}

	item, err := svc.AddTodo(ctx, projectPath, "Create a cached worktree name when I add the TODO")
	if err != nil {
		t.Fatalf("add todo: %v", err)
	}
	suggestion, err := st.GetTodoWorktreeSuggestion(ctx, item.ID)
	if err != nil {
		t.Fatalf("GetTodoWorktreeSuggestion() after add = %v", err)
	}
	if suggestion.Status != model.TodoWorktreeSuggestionQueued {
		t.Fatalf("suggestion.Status after add = %q, want %q", suggestion.Status, model.TodoWorktreeSuggestionQueued)
	}
	suggestion, err = st.ClaimNextQueuedTodoWorktreeSuggestion(ctx, time.Hour, 0)
	if err != nil {
		t.Fatalf("claim immediately queued todo worktree suggestion: %v", err)
	}
	suggestion.BranchName = "feat/cached-worktree-name"
	suggestion.WorktreeSuffix = "feat-cached-worktree-name"
	suggestion.Kind = "feature"
	suggestion.Reason = "Creates the worktree name before the launch dialog opens."
	suggestion.Confidence = 0.91
	suggestion.Model = "test"
	if completed, err := st.CompleteTodoWorktreeSuggestion(ctx, suggestion); err != nil {
		t.Fatalf("complete todo worktree suggestion: %v", err)
	} else if !completed {
		t.Fatalf("expected todo worktree suggestion to complete")
	}

	if err := svc.UpdateTodo(ctx, projectPath, item.ID, "Refresh the cached worktree name when the TODO changes"); err != nil {
		t.Fatalf("update todo: %v", err)
	}
	suggestion, err = st.GetTodoWorktreeSuggestion(ctx, item.ID)
	if err != nil {
		t.Fatalf("GetTodoWorktreeSuggestion() after update = %v", err)
	}
	if suggestion.Status != model.TodoWorktreeSuggestionQueued {
		t.Fatalf("suggestion.Status after update = %q, want %q", suggestion.Status, model.TodoWorktreeSuggestionQueued)
	}
	if suggestion.BranchName != "" || suggestion.WorktreeSuffix != "" {
		t.Fatalf("suggestion names after update = (%q, %q), want cleared queued suggestion", suggestion.BranchName, suggestion.WorktreeSuffix)
	}
	if _, err := st.ClaimNextQueuedTodoWorktreeSuggestion(ctx, time.Hour, 0); err != nil {
		t.Fatalf("claim immediately requeued todo worktree suggestion: %v", err)
	}
}

func queueTodoWorktreeSuggestionForTest(t *testing.T, ctx context.Context, st *store.Store, todoID int64) {
	t.Helper()
	queued, err := st.QueueTodoWorktreeSuggestion(ctx, todoID)
	if err != nil {
		t.Fatalf("queue todo worktree suggestion: %v", err)
	}
	if queued {
		return
	}
	suggestion, err := st.GetTodoWorktreeSuggestion(ctx, todoID)
	if err != nil {
		t.Fatalf("get existing todo worktree suggestion: %v", err)
	}
	if suggestion.Status != model.TodoWorktreeSuggestionQueued {
		t.Fatalf("existing todo worktree suggestion status = %q, want %q", suggestion.Status, model.TodoWorktreeSuggestionQueued)
	}
}

func writeWorktreePrepConfig(t *testing.T, root, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(worktreeprep.ConfigRelPath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir worktree prep config parent: %v", err)
	}
	if err := os.WriteFile(path, []byte(strings.TrimSpace(content)+"\n"), 0o644); err != nil {
		t.Fatalf("write worktree prep config: %v", err)
	}
}

func sameTestPath(t *testing.T, a, b string) bool {
	t.Helper()
	resolvedA, err := filepath.EvalSymlinks(a)
	if err == nil {
		a = resolvedA
	}
	resolvedB, err := filepath.EvalSymlinks(b)
	if err == nil {
		b = resolvedB
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

func TestPurgeDoneTodosDeletesOnlyCompletedItems(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	projectPath := filepath.Join(root, "repo")
	initGitRepo(t, projectPath)

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	cfg := config.Default()
	svc := New(cfg, st, events.NewBus(), nil)
	if _, err := svc.CreateOrAttachProject(ctx, CreateOrAttachProjectRequest{
		ParentPath: root,
		Name:       "repo",
	}); err != nil {
		t.Fatalf("track root project: %v", err)
	}

	openItem, err := svc.AddTodo(ctx, projectPath, "Keep this task")
	if err != nil {
		t.Fatalf("add open todo: %v", err)
	}
	doneItem, err := svc.AddTodo(ctx, projectPath, "Remove this completed task")
	if err != nil {
		t.Fatalf("add done todo: %v", err)
	}
	if err := svc.ToggleTodoDone(ctx, projectPath, doneItem.ID, true); err != nil {
		t.Fatalf("mark done: %v", err)
	}

	count, err := svc.PurgeDoneTodos(ctx, projectPath)
	if err != nil {
		t.Fatalf("PurgeDoneTodos() error = %v", err)
	}
	if count != 1 {
		t.Fatalf("PurgeDoneTodos() count = %d, want 1", count)
	}

	detail, err := st.GetProjectDetail(ctx, projectPath, 0)
	if err != nil {
		t.Fatalf("get project detail: %v", err)
	}
	if len(detail.Todos) != 1 {
		t.Fatalf("remaining todo count = %d, want 1", len(detail.Todos))
	}
	if detail.Todos[0].ID != openItem.ID {
		t.Fatalf("remaining todo = %#v, want open item %#v", detail.Todos[0], openItem)
	}
}

func TestAddTodoRefreshesProjectStatusAsync(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	projectPath := filepath.Join(root, "repo")
	initGitRepo(t, projectPath)

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	cfg := config.Default()
	svc := New(cfg, st, events.NewBus(), nil)
	if _, err := svc.CreateOrAttachProject(ctx, CreateOrAttachProjectRequest{
		ParentPath: root,
		Name:       "repo",
	}); err != nil {
		t.Fatalf("track root project: %v", err)
	}

	const refreshedBranch = "async-refresh-branch"
	refreshStarted := make(chan struct{}, 1)
	releaseRefresh := make(chan struct{})
	svc.gitRepoStatusReader = func(context.Context, string) (scanner.GitRepoStatus, error) {
		select {
		case refreshStarted <- struct{}{}:
		default:
		}
		<-releaseRefresh
		return scanner.GitRepoStatus{Branch: refreshedBranch}, nil
	}

	addDone := make(chan error, 1)
	go func() {
		_, err := svc.AddTodo(ctx, projectPath, "Save should stay responsive")
		addDone <- err
	}()

	select {
	case <-refreshStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for background refresh to reach git status")
	}

	select {
	case err := <-addDone:
		if err != nil {
			t.Fatalf("AddTodo() error = %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("AddTodo() blocked on project refresh")
	}

	close(releaseRefresh)

	deadline := time.Now().Add(2 * time.Second)
	for {
		summary, err := st.GetProjectSummary(ctx, projectPath, false)
		if err == nil && summary.RepoBranch == refreshedBranch {
			break
		}
		if time.Now().After(deadline) {
			if err != nil {
				t.Fatalf("timed out waiting for async refresh summary update: %v", err)
			}
			t.Fatalf("repo branch after async refresh = %q, want %q", summary.RepoBranch, refreshedBranch)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestCreateTodoWorktreeCreatesTrackedSiblingProject(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	projectPath := filepath.Join(root, "repo")
	initGitRepo(t, projectPath)

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	cfg := config.Default()
	svc := New(cfg, st, events.NewBus(), nil)
	if _, err := svc.CreateOrAttachProject(ctx, CreateOrAttachProjectRequest{
		ParentPath: root,
		Name:       "repo",
	}); err != nil {
		t.Fatalf("track root project: %v", err)
	}

	item, err := svc.AddTodo(ctx, projectPath, "Build the first worktree launch flow")
	if err != nil {
		t.Fatalf("add todo: %v", err)
	}
	queueTodoWorktreeSuggestionForTest(t, ctx, st, item.ID)
	suggestion, err := st.ClaimNextQueuedTodoWorktreeSuggestion(ctx, 0, 0)
	if err != nil {
		t.Fatalf("claim todo worktree suggestion: %v", err)
	}
	suggestion.BranchName = "feat/worktree-launch"
	suggestion.WorktreeSuffix = "feat-worktree-launch"
	suggestion.Kind = "feature"
	suggestion.Reason = "Implements the first worktree launch flow."
	suggestion.Confidence = 0.93
	suggestion.Model = "test"
	if completed, err := st.CompleteTodoWorktreeSuggestion(ctx, suggestion); err != nil {
		t.Fatalf("complete todo worktree suggestion: %v", err)
	} else if !completed {
		t.Fatalf("expected todo worktree suggestion to complete")
	}

	result, err := svc.CreateTodoWorktree(ctx, CreateTodoWorktreeRequest{
		ProjectPath: projectPath,
		TodoID:      item.ID,
	})
	if err != nil {
		t.Fatalf("CreateTodoWorktree() error = %v", err)
	}
	expectedPath := filepath.Join(root, "repo--feat-worktree-launch")
	if result.WorktreePath != expectedPath {
		t.Fatalf("worktree path = %q, want %q", result.WorktreePath, expectedPath)
	}
	if result.BranchName != "feat/worktree-launch" {
		t.Fatalf("branch = %q, want %q", result.BranchName, "feat/worktree-launch")
	}
	rootStatus, err := scanner.ReadGitRepoStatus(ctx, projectPath)
	if err != nil {
		t.Fatalf("read git repo status for root: %v", err)
	}
	if result.ParentBranch != strings.TrimSpace(rootStatus.Branch) {
		t.Fatalf("parent branch = %q, want %q", result.ParentBranch, strings.TrimSpace(rootStatus.Branch))
	}
	status, err := scanner.ReadGitRepoStatus(ctx, result.WorktreePath)
	if err != nil {
		t.Fatalf("read git repo status for worktree: %v", err)
	}
	if status.Branch != "feat/worktree-launch" {
		t.Fatalf("worktree branch = %q, want %q", status.Branch, "feat/worktree-launch")
	}

	detail, err := st.GetProjectDetail(ctx, result.WorktreePath, 5)
	if err != nil {
		t.Fatalf("GetProjectDetail() for worktree error = %v", err)
	}
	if detail.Summary.Path != result.WorktreePath {
		t.Fatalf("tracked worktree path = %q, want %q", detail.Summary.Path, result.WorktreePath)
	}
	if strings.TrimSpace(detail.Summary.RepoBranch) != "feat/worktree-launch" {
		t.Fatalf("tracked worktree branch = %q, want %q", detail.Summary.RepoBranch, "feat/worktree-launch")
	}
	if strings.TrimSpace(detail.Summary.WorktreeParentBranch) != strings.TrimSpace(rootStatus.Branch) {
		t.Fatalf("tracked worktree parent branch = %q, want %q", detail.Summary.WorktreeParentBranch, strings.TrimSpace(rootStatus.Branch))
	}
}

func TestCreateTodoWorktreeInheritsProjectCategory(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	projectPath := filepath.Join(root, "repo")
	initGitRepo(t, projectPath)

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	category, err := st.CreateProjectCategory(ctx, "Client")
	if err != nil {
		t.Fatalf("CreateProjectCategory() error = %v", err)
	}
	cfg := config.Default()
	svc := New(cfg, st, events.NewBus(), nil)
	if _, err := svc.CreateOrAttachProject(ctx, CreateOrAttachProjectRequest{
		ParentPath:       root,
		Name:             "repo",
		CategoryID:       category.ID,
		CategoryExplicit: true,
	}); err != nil {
		t.Fatalf("track root project: %v", err)
	}

	item, err := svc.AddTodo(ctx, projectPath, "Keep worktrees in the same category")
	if err != nil {
		t.Fatalf("add todo: %v", err)
	}
	result, err := svc.CreateTodoWorktree(ctx, CreateTodoWorktreeRequest{
		ProjectPath:    projectPath,
		TodoID:         item.ID,
		BranchName:     "fix/worktree-category",
		WorktreeSuffix: "fix-worktree-category",
	})
	if err != nil {
		t.Fatalf("CreateTodoWorktree() error = %v", err)
	}

	detail, err := st.GetProjectDetail(ctx, result.WorktreePath, 5)
	if err != nil {
		t.Fatalf("GetProjectDetail() for worktree error = %v", err)
	}
	if detail.Summary.CategoryID != category.ID || detail.Summary.CategoryName != "Client" {
		t.Fatalf("category = %q/%q, want %q/Client", detail.Summary.CategoryID, detail.Summary.CategoryName, category.ID)
	}
}

func TestCreateTodoWorktreeWaitsForRootCreationLock(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	projectPath := filepath.Join(root, "repo")
	initGitRepo(t, projectPath)

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	cfg := config.Default()
	svc := New(cfg, st, events.NewBus(), nil)
	if _, err := svc.CreateOrAttachProject(ctx, CreateOrAttachProjectRequest{
		ParentPath: root,
		Name:       "repo",
	}); err != nil {
		t.Fatalf("track root project: %v", err)
	}

	item, err := svc.AddTodo(ctx, projectPath, "Wait for another worktree creation")
	if err != nil {
		t.Fatalf("add todo: %v", err)
	}

	unlock := svc.worktreeCreateLocks.Lock(filepath.Clean(projectPath))
	defer unlock()

	waitCtx, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	_, err = svc.CreateTodoWorktree(waitCtx, CreateTodoWorktreeRequest{
		ProjectPath: projectPath,
		TodoID:      item.ID,
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("CreateTodoWorktree() error = %v, want context deadline while waiting for create lock", err)
	}
}

func TestCreateTodoWorktreeAppliesConfiguredPrepProfile(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	projectPath := filepath.Join(root, "repo")
	submoduleOriginPath := filepath.Join(root, "asset-origin")
	initGitRepoWithSubmodule(t, projectPath, submoduleOriginPath, "Assets")
	writeWorktreePrepConfig(t, projectPath, `
default_profile = "assets"

[profiles.assets]
submodules = [
  { path = "Assets", mode = "checkout" },
]
`)

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	cfg := config.Default()
	svc := New(cfg, st, events.NewBus(), nil)
	if _, err := svc.CreateOrAttachProject(ctx, CreateOrAttachProjectRequest{
		ParentPath: root,
		Name:       "repo",
	}); err != nil {
		t.Fatalf("track root project: %v", err)
	}

	item, err := svc.AddTodo(ctx, projectPath, "Create a worktree with prepared assets")
	if err != nil {
		t.Fatalf("add todo: %v", err)
	}
	queueTodoWorktreeSuggestionForTest(t, ctx, st, item.ID)
	suggestion, err := st.ClaimNextQueuedTodoWorktreeSuggestion(ctx, 0, 0)
	if err != nil {
		t.Fatalf("claim todo worktree suggestion: %v", err)
	}
	suggestion.BranchName = "feat/prepared-assets"
	suggestion.WorktreeSuffix = "feat-prepared-assets"
	suggestion.Kind = "feature"
	suggestion.Reason = "Creates a prepared worktree."
	suggestion.Confidence = 0.92
	suggestion.Model = "test"
	if completed, err := st.CompleteTodoWorktreeSuggestion(ctx, suggestion); err != nil {
		t.Fatalf("complete todo worktree suggestion: %v", err)
	} else if !completed {
		t.Fatalf("expected todo worktree suggestion to complete")
	}

	result, err := svc.CreateTodoWorktree(ctx, CreateTodoWorktreeRequest{
		ProjectPath: projectPath,
		TodoID:      item.ID,
	})
	if err != nil {
		t.Fatalf("CreateTodoWorktree() error = %v", err)
	}
	if result.PrepProfile != "assets" {
		t.Fatalf("PrepProfile = %q, want assets", result.PrepProfile)
	}
	if len(result.PreparedPaths) != 1 || result.PreparedPaths[0] != "Assets" {
		t.Fatalf("PreparedPaths = %#v, want [Assets]", result.PreparedPaths)
	}
	if got := strings.TrimSpace(gitOutput(t, filepath.Join(result.WorktreePath, "Assets"), "git", "rev-parse", "--show-toplevel")); !sameTestPath(t, got, filepath.Join(result.WorktreePath, "Assets")) {
		t.Fatalf("prepared submodule top-level = %q", got)
	}
}

func TestCreateTodoWorktreePreparesSubmodulesByDefault(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	projectPath := filepath.Join(root, "repo")
	submoduleOriginPath := filepath.Join(root, "asset-origin")
	initGitRepoWithSubmodule(t, projectPath, submoduleOriginPath, "Assets")

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	cfg := config.Default()
	svc := New(cfg, st, events.NewBus(), nil)
	if _, err := svc.CreateOrAttachProject(ctx, CreateOrAttachProjectRequest{
		ParentPath: root,
		Name:       "repo",
	}); err != nil {
		t.Fatalf("track root project: %v", err)
	}

	item, err := svc.AddTodo(ctx, projectPath, "Create a worktree with default prepared assets")
	if err != nil {
		t.Fatalf("add todo: %v", err)
	}
	queueTodoWorktreeSuggestionForTest(t, ctx, st, item.ID)
	suggestion, err := st.ClaimNextQueuedTodoWorktreeSuggestion(ctx, 0, 0)
	if err != nil {
		t.Fatalf("claim todo worktree suggestion: %v", err)
	}
	suggestion.BranchName = "feat/default-prepared-assets"
	suggestion.WorktreeSuffix = "feat-default-prepared-assets"
	suggestion.Kind = "feature"
	suggestion.Reason = "Creates a prepared worktree with the default submodule preparation."
	suggestion.Confidence = 0.92
	suggestion.Model = "test"
	if completed, err := st.CompleteTodoWorktreeSuggestion(ctx, suggestion); err != nil {
		t.Fatalf("complete todo worktree suggestion: %v", err)
	} else if !completed {
		t.Fatalf("expected todo worktree suggestion to complete")
	}

	result, err := svc.CreateTodoWorktree(ctx, CreateTodoWorktreeRequest{
		ProjectPath: projectPath,
		TodoID:      item.ID,
	})
	if err != nil {
		t.Fatalf("CreateTodoWorktree() error = %v", err)
	}
	if result.PrepProfile != worktreeprep.AutoSubmodulesProfile {
		t.Fatalf("PrepProfile = %q, want %s", result.PrepProfile, worktreeprep.AutoSubmodulesProfile)
	}
	if len(result.PreparedPaths) != 1 || result.PreparedPaths[0] != "Assets" {
		t.Fatalf("PreparedPaths = %#v, want [Assets]", result.PreparedPaths)
	}
	if got := strings.TrimSpace(gitOutput(t, filepath.Join(result.WorktreePath, "Assets"), "git", "rev-parse", "--show-toplevel")); !sameTestPath(t, got, filepath.Join(result.WorktreePath, "Assets")) {
		t.Fatalf("prepared submodule top-level = %q", got)
	}
}

func TestCreateTodoWorktreeAutoSuffixOnConflict(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	projectPath := filepath.Join(root, "repo")
	initGitRepo(t, projectPath)

	// Pre-create a directory that collides with the default worktree path.
	conflictingPath := filepath.Join(root, "repo--feat-worktree-launch")
	if err := os.MkdirAll(conflictingPath, 0o755); err != nil {
		t.Fatalf("create conflicting directory: %v", err)
	}

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	cfg := config.Default()
	svc := New(cfg, st, events.NewBus(), nil)
	if _, err := svc.CreateOrAttachProject(ctx, CreateOrAttachProjectRequest{
		ParentPath: root,
		Name:       "repo",
	}); err != nil {
		t.Fatalf("track root project: %v", err)
	}

	item, err := svc.AddTodo(ctx, projectPath, "Auto-suffix on conflict")
	if err != nil {
		t.Fatalf("add todo: %v", err)
	}
	queueTodoWorktreeSuggestionForTest(t, ctx, st, item.ID)
	suggestion, err := st.ClaimNextQueuedTodoWorktreeSuggestion(ctx, 0, 0)
	if err != nil {
		t.Fatalf("claim todo worktree suggestion: %v", err)
	}
	suggestion.BranchName = "feat/worktree-launch"
	suggestion.WorktreeSuffix = "feat-worktree-launch"
	suggestion.Kind = "feature"
	suggestion.Reason = "Auto-suffix test."
	suggestion.Confidence = 0.93
	suggestion.Model = "test"
	if completed, err := st.CompleteTodoWorktreeSuggestion(ctx, suggestion); err != nil {
		t.Fatalf("complete todo worktree suggestion: %v", err)
	} else if !completed {
		t.Fatalf("expected todo worktree suggestion to complete")
	}

	result, err := svc.CreateTodoWorktree(ctx, CreateTodoWorktreeRequest{
		ProjectPath: projectPath,
		TodoID:      item.ID,
	})
	if err != nil {
		t.Fatalf("CreateTodoWorktree() error = %v", err)
	}
	expectedPath := filepath.Join(root, "repo--feat-worktree-launch-2")
	if result.WorktreePath != expectedPath {
		t.Fatalf("worktree path = %q, want %q", result.WorktreePath, expectedPath)
	}
	if result.WorktreeSuffix != "feat-worktree-launch-2" {
		t.Fatalf("worktree suffix = %q, want %q", result.WorktreeSuffix, "feat-worktree-launch-2")
	}
	if result.BranchName != "feat/worktree-launch-2" {
		t.Fatalf("branch = %q, want %q", result.BranchName, "feat/worktree-launch-2")
	}
}

func TestCreateTodoWorktreeFallsBackToGeneratedNamesWhileSuggestionQueued(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	projectPath := filepath.Join(root, "repo")
	initGitRepo(t, projectPath)

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	svc := New(config.Default(), st, events.NewBus(), nil)
	if _, err := svc.CreateOrAttachProject(ctx, CreateOrAttachProjectRequest{
		ParentPath: root,
		Name:       "repo",
	}); err != nil {
		t.Fatalf("track root project: %v", err)
	}

	item, err := svc.AddTodo(ctx, projectPath, "Async todo launch")
	if err != nil {
		t.Fatalf("add todo: %v", err)
	}
	queueTodoWorktreeSuggestionForTest(t, ctx, st, item.ID)

	result, err := svc.CreateTodoWorktree(ctx, CreateTodoWorktreeRequest{
		ProjectPath: projectPath,
		TodoID:      item.ID,
	})
	if err != nil {
		t.Fatalf("CreateTodoWorktree() error = %v", err)
	}
	expectedPath := filepath.Join(root, "repo--todo-async-todo-launch")
	if result.WorktreePath != expectedPath {
		t.Fatalf("worktree path = %q, want %q", result.WorktreePath, expectedPath)
	}
	if result.WorktreeSuffix != "todo-async-todo-launch" {
		t.Fatalf("worktree suffix = %q, want %q", result.WorktreeSuffix, "todo-async-todo-launch")
	}
	if result.BranchName != "todo/async-todo-launch" {
		t.Fatalf("branch = %q, want %q", result.BranchName, "todo/async-todo-launch")
	}
}

func TestRemoveWorktreeRemovesTrackedLinkedWorktree(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	projectPath := filepath.Join(root, "repo")
	initGitRepo(t, projectPath)

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	cfg := config.Default()
	svc := New(cfg, st, events.NewBus(), nil)
	if _, err := svc.CreateOrAttachProject(ctx, CreateOrAttachProjectRequest{
		ParentPath: root,
		Name:       "repo",
	}); err != nil {
		t.Fatalf("track root project: %v", err)
	}

	item, err := svc.AddTodo(ctx, projectPath, "Create a removable linked worktree")
	if err != nil {
		t.Fatalf("add todo: %v", err)
	}
	queueTodoWorktreeSuggestionForTest(t, ctx, st, item.ID)
	suggestion, err := st.ClaimNextQueuedTodoWorktreeSuggestion(ctx, 0, 0)
	if err != nil {
		t.Fatalf("claim todo worktree suggestion: %v", err)
	}
	suggestion.BranchName = "feat/remove-worktree"
	suggestion.WorktreeSuffix = "feat-remove-worktree"
	suggestion.Kind = "feature"
	suggestion.Reason = "Creates a linked worktree for removal coverage."
	suggestion.Confidence = 0.92
	suggestion.Model = "test"
	if completed, err := st.CompleteTodoWorktreeSuggestion(ctx, suggestion); err != nil {
		t.Fatalf("complete todo worktree suggestion: %v", err)
	} else if !completed {
		t.Fatalf("expected todo worktree suggestion to complete")
	}

	result, err := svc.CreateTodoWorktree(ctx, CreateTodoWorktreeRequest{
		ProjectPath: projectPath,
		TodoID:      item.ID,
	})
	if err != nil {
		t.Fatalf("CreateTodoWorktree() error = %v", err)
	}

	if err := svc.RemoveWorktree(ctx, result.WorktreePath, false); err != nil {
		t.Fatalf("RemoveWorktree() error = %v", err)
	}
	if _, err := os.Stat(result.WorktreePath); !os.IsNotExist(err) {
		t.Fatalf("worktree path still exists after removal: stat err = %v", err)
	}
	worktrees, err := scanner.ListGitWorktrees(ctx, projectPath)
	if err != nil {
		t.Fatalf("ListGitWorktrees() error = %v", err)
	}
	for _, worktree := range worktrees {
		if filepath.Clean(strings.TrimSpace(worktree.Path)) == filepath.Clean(result.WorktreePath) {
			t.Fatalf("removed worktree %q still present in git worktree list", result.WorktreePath)
		}
	}
	detail, err := st.GetProjectDetail(ctx, result.WorktreePath, 5)
	if err != nil {
		t.Fatalf("GetProjectDetail() after removal error = %v", err)
	}
	if !detail.Summary.Forgotten {
		t.Fatalf("removed worktree should be marked forgotten: %#v", detail.Summary)
	}
	if detail.Summary.PresentOnDisk {
		t.Fatalf("removed worktree should be marked missing on disk immediately: %#v", detail.Summary)
	}
}

func TestRemoveWorktreeRemovesMissingTrackedLinkedWorktree(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	projectPath := filepath.Join(root, "repo")

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	cfg := config.Default()
	svc := New(cfg, st, events.NewBus(), nil)
	initGitRepo(t, projectPath)
	if _, err := svc.CreateOrAttachProject(ctx, CreateOrAttachProjectRequest{
		ParentPath: root,
		Name:       "repo",
	}); err != nil {
		t.Fatalf("track root project: %v", err)
	}

	item, err := svc.AddTodo(ctx, projectPath, "Create a removable linked worktree that has already disappeared on disk")
	if err != nil {
		t.Fatalf("add todo: %v", err)
	}
	queueTodoWorktreeSuggestionForTest(t, ctx, st, item.ID)
	suggestion, err := st.ClaimNextQueuedTodoWorktreeSuggestion(ctx, 0, 0)
	if err != nil {
		t.Fatalf("claim todo worktree suggestion: %v", err)
	}
	suggestion.BranchName = "feat/remove-missing-worktree"
	suggestion.WorktreeSuffix = "feat-remove-missing-worktree"
	suggestion.Kind = "feature"
	suggestion.Reason = "Creates a linked worktree that will be removed after its checkout disappears."
	suggestion.Confidence = 0.92
	suggestion.Model = "test"
	if completed, err := st.CompleteTodoWorktreeSuggestion(ctx, suggestion); err != nil {
		t.Fatalf("complete todo worktree suggestion: %v", err)
	} else if !completed {
		t.Fatalf("expected todo worktree suggestion to complete")
	}

	result, err := svc.CreateTodoWorktree(ctx, CreateTodoWorktreeRequest{
		ProjectPath: projectPath,
		TodoID:      item.ID,
	})
	if err != nil {
		t.Fatalf("CreateTodoWorktree() error = %v", err)
	}

	if err := os.RemoveAll(result.WorktreePath); err != nil {
		t.Fatalf("remove worktree path: %v", err)
	}
	if _, err := os.Stat(result.WorktreePath); !os.IsNotExist(err) {
		t.Fatalf("worktree path should be gone before removal: stat err = %v", err)
	}

	if err := svc.RemoveWorktree(ctx, result.WorktreePath, false); err != nil {
		t.Fatalf("RemoveWorktree() with missing checkout error = %v", err)
	}

	worktrees, err := scanner.ListGitWorktrees(ctx, projectPath)
	if err != nil {
		t.Fatalf("ListGitWorktrees() error = %v", err)
	}
	for _, worktree := range worktrees {
		if filepath.Clean(strings.TrimSpace(worktree.Path)) == filepath.Clean(result.WorktreePath) {
			t.Fatalf("removed missing worktree %q still present in git worktree list", result.WorktreePath)
		}
	}
	detail, err := st.GetProjectDetail(ctx, result.WorktreePath, 5)
	if err != nil {
		t.Fatalf("GetProjectDetail() after removal error = %v", err)
	}
	if !detail.Summary.Forgotten {
		t.Fatalf("removed missing worktree should be marked forgotten: %#v", detail.Summary)
	}
	if detail.Summary.PresentOnDisk {
		t.Fatalf("removed missing worktree should stay marked missing on disk: %#v", detail.Summary)
	}
}

func TestRemoveWorktreeRetriesWithForceForInitializedSubmodules(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	projectPath := filepath.Join(root, "repo")
	submoduleOriginPath := filepath.Join(root, "assets-origin")
	initGitRepoWithSubmodule(t, projectPath, submoduleOriginPath, "assets_src")

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	cfg := config.Default()
	svc := New(cfg, st, events.NewBus(), nil)
	if _, err := svc.CreateOrAttachProject(ctx, CreateOrAttachProjectRequest{
		ParentPath: root,
		Name:       "repo",
	}); err != nil {
		t.Fatalf("track root project: %v", err)
	}

	item, err := svc.AddTodo(ctx, projectPath, "Create a removable linked worktree with initialized submodules")
	if err != nil {
		t.Fatalf("add todo: %v", err)
	}
	queueTodoWorktreeSuggestionForTest(t, ctx, st, item.ID)
	suggestion, err := st.ClaimNextQueuedTodoWorktreeSuggestion(ctx, 0, 0)
	if err != nil {
		t.Fatalf("claim todo worktree suggestion: %v", err)
	}
	suggestion.BranchName = "feat/remove-worktree-submodule"
	suggestion.WorktreeSuffix = "feat-remove-worktree-submodule"
	suggestion.Kind = "feature"
	suggestion.Reason = "Creates a linked worktree with initialized submodules for removal coverage."
	suggestion.Confidence = 0.92
	suggestion.Model = "test"
	if completed, err := st.CompleteTodoWorktreeSuggestion(ctx, suggestion); err != nil {
		t.Fatalf("complete todo worktree suggestion: %v", err)
	} else if !completed {
		t.Fatalf("expected todo worktree suggestion to complete")
	}

	result, err := svc.CreateTodoWorktree(ctx, CreateTodoWorktreeRequest{
		ProjectPath: projectPath,
		TodoID:      item.ID,
	})
	if err != nil {
		t.Fatalf("CreateTodoWorktree() error = %v", err)
	}

	runGit(t, result.WorktreePath, "git", "-c", "protocol.file.allow=always", "submodule", "update", "--init", "--recursive")

	if err := gitWorktreeRemove(ctx, projectPath, result.WorktreePath, false); err == nil {
		t.Fatalf("plain gitWorktreeRemove() unexpectedly succeeded for initialized submodule worktree")
	} else if !isGitWorktreeSubmoduleRemoveError(err) {
		t.Fatalf("gitWorktreeRemove() error = %v, want submodule removal error", err)
	}

	if err := svc.RemoveWorktree(ctx, result.WorktreePath, false); err != nil {
		t.Fatalf("RemoveWorktree() error = %v", err)
	}
	if _, err := os.Stat(result.WorktreePath); !os.IsNotExist(err) {
		t.Fatalf("worktree path still exists after removal: stat err = %v", err)
	}
}

func TestRemoveWorktreeWaitsForScanAndStaysForgotten(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	projectPath := filepath.Join(root, "repo")
	initGitRepo(t, projectPath)

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	cfg := config.Default()
	cfg.IncludePaths = []string{root}
	svc := New(cfg, st, events.NewBus(), nil)
	if _, err := svc.CreateOrAttachProject(ctx, CreateOrAttachProjectRequest{
		ParentPath: root,
		Name:       "repo",
	}); err != nil {
		t.Fatalf("track root project: %v", err)
	}

	item, err := svc.AddTodo(ctx, projectPath, "Create a removable linked worktree")
	if err != nil {
		t.Fatalf("add todo: %v", err)
	}
	queueTodoWorktreeSuggestionForTest(t, ctx, st, item.ID)
	suggestion, err := st.ClaimNextQueuedTodoWorktreeSuggestion(ctx, 0, 0)
	if err != nil {
		t.Fatalf("claim todo worktree suggestion: %v", err)
	}
	suggestion.BranchName = "feat/remove-during-scan"
	suggestion.WorktreeSuffix = "feat-remove-during-scan"
	suggestion.Kind = "feature"
	suggestion.Reason = "Creates a linked worktree for concurrent removal coverage."
	suggestion.Confidence = 0.92
	suggestion.Model = "test"
	if completed, err := st.CompleteTodoWorktreeSuggestion(ctx, suggestion); err != nil {
		t.Fatalf("complete todo worktree suggestion: %v", err)
	} else if !completed {
		t.Fatalf("expected todo worktree suggestion to complete")
	}

	result, err := svc.CreateTodoWorktree(ctx, CreateTodoWorktreeRequest{
		ProjectPath: projectPath,
		TodoID:      item.ID,
	})
	if err != nil {
		t.Fatalf("CreateTodoWorktree() error = %v", err)
	}

	originalFingerprintReader := svc.gitFingerprintReader
	if originalFingerprintReader == nil {
		t.Fatalf("gitFingerprintReader = nil")
	}

	scanBlocked := make(chan struct{})
	releaseScan := make(chan struct{})
	var blockOnce sync.Once
	svc.gitFingerprintReader = func(ctx context.Context, path string) (scanner.GitFingerprint, error) {
		if filepath.Clean(path) == filepath.Clean(result.WorktreePath) {
			blockOnce.Do(func() {
				close(scanBlocked)
				<-releaseScan
			})
		}
		return originalFingerprintReader(ctx, path)
	}

	scanDone := make(chan error, 1)
	go func() {
		_, err := svc.ScanOnce(ctx)
		scanDone <- err
	}()

	select {
	case <-scanBlocked:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for scan to block on worktree fingerprint read")
	}

	removeDone := make(chan error, 1)
	go func() {
		removeDone <- svc.RemoveWorktree(ctx, result.WorktreePath, false)
	}()

	close(releaseScan)

	if err := <-scanDone; err != nil {
		t.Fatalf("ScanOnce() error = %v", err)
	}
	if err := <-removeDone; err != nil {
		t.Fatalf("RemoveWorktree() error = %v", err)
	}

	detail, err := st.GetProjectDetail(ctx, result.WorktreePath, 5)
	if err != nil {
		t.Fatalf("GetProjectDetail() after concurrent removal error = %v", err)
	}
	if !detail.Summary.Forgotten {
		t.Fatalf("removed worktree should stay forgotten after a concurrent scan: %#v", detail.Summary)
	}
	if detail.Summary.PresentOnDisk {
		t.Fatalf("removed worktree should stay marked missing after a concurrent scan: %#v", detail.Summary)
	}
	if _, err := os.Stat(result.WorktreePath); !os.IsNotExist(err) {
		t.Fatalf("worktree path still exists after concurrent removal: stat err = %v", err)
	}
}

func TestRemoveWorktreeHonorsContextWhileWaitingForMutationLock(t *testing.T) {
	t.Parallel()

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	svc := New(config.Default(), st, events.NewBus(), nil)
	svc.mu.Lock()
	defer svc.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()

	err = svc.RemoveWorktree(ctx, filepath.Join(t.TempDir(), "repo--blocked"), false)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("RemoveWorktree() error = %v, want context deadline exceeded", err)
	}
}

func TestMergeWorktreeBackMergesIntoRecordedParentBranch(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	projectPath := filepath.Join(root, "repo")
	initGitRepo(t, projectPath)

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	cfg := config.Default()
	svc := New(cfg, st, events.NewBus(), nil)
	if _, err := svc.CreateOrAttachProject(ctx, CreateOrAttachProjectRequest{
		ParentPath: root,
		Name:       "repo",
	}); err != nil {
		t.Fatalf("track root project: %v", err)
	}

	item, err := svc.AddTodo(ctx, projectPath, "Implement merge back for linked worktrees")
	if err != nil {
		t.Fatalf("add todo: %v", err)
	}
	queueTodoWorktreeSuggestionForTest(t, ctx, st, item.ID)
	suggestion, err := st.ClaimNextQueuedTodoWorktreeSuggestion(ctx, 0, 0)
	if err != nil {
		t.Fatalf("claim todo worktree suggestion: %v", err)
	}
	suggestion.BranchName = "feat/merge-worktree-back"
	suggestion.WorktreeSuffix = "feat-merge-worktree-back"
	suggestion.Kind = "feature"
	suggestion.Reason = "Creates a linked worktree so the merge-back flow can be tested."
	suggestion.Confidence = 0.95
	suggestion.Model = "test"
	if completed, err := st.CompleteTodoWorktreeSuggestion(ctx, suggestion); err != nil {
		t.Fatalf("complete todo worktree suggestion: %v", err)
	} else if !completed {
		t.Fatalf("expected todo worktree suggestion to complete")
	}

	result, err := svc.CreateTodoWorktree(ctx, CreateTodoWorktreeRequest{
		ProjectPath: projectPath,
		TodoID:      item.ID,
	})
	if err != nil {
		t.Fatalf("CreateTodoWorktree() error = %v", err)
	}

	initialWorktreeDetail, err := st.GetProjectDetail(ctx, result.WorktreePath, 5)
	if err != nil {
		t.Fatalf("GetProjectDetail() for worktree after create error = %v", err)
	}
	if initialWorktreeDetail.Summary.WorktreeMergeStatus != model.WorktreeMergeStatusMerged {
		t.Fatalf("new linked worktree should start merged with its parent branch, got %#v", initialWorktreeDetail.Summary)
	}
	if initialWorktreeDetail.Summary.WorktreeOriginTodoID != item.ID {
		t.Fatalf("new linked worktree should remember its origin todo id, got %#v", initialWorktreeDetail.Summary)
	}

	if err := os.WriteFile(filepath.Join(result.WorktreePath, "FEATURE.txt"), []byte("merged from linked worktree\n"), 0o644); err != nil {
		t.Fatalf("write FEATURE.txt in worktree: %v", err)
	}
	runGit(t, result.WorktreePath, "git", "add", "FEATURE.txt")
	runGit(t, result.WorktreePath, "git", "commit", "-m", "add worktree feature")
	if err := svc.RefreshProjectStatus(ctx, result.WorktreePath); err != nil {
		t.Fatalf("RefreshProjectStatus() for worktree after commit error = %v", err)
	}

	divergedWorktreeDetail, err := st.GetProjectDetail(ctx, result.WorktreePath, 5)
	if err != nil {
		t.Fatalf("GetProjectDetail() for worktree after diverging error = %v", err)
	}
	if divergedWorktreeDetail.Summary.WorktreeMergeStatus != model.WorktreeMergeStatusNotMerged {
		t.Fatalf("diverged linked worktree should be marked not merged, got %#v", divergedWorktreeDetail.Summary)
	}

	mergeResult, err := svc.MergeWorktreeBack(ctx, result.WorktreePath)
	if err != nil {
		t.Fatalf("MergeWorktreeBack() error = %v", err)
	}
	if mergeResult.RootProjectPath != projectPath {
		t.Fatalf("merge root path = %q, want %q", mergeResult.RootProjectPath, projectPath)
	}
	if mergeResult.SourceBranch != "feat/merge-worktree-back" {
		t.Fatalf("merge source branch = %q, want %q", mergeResult.SourceBranch, "feat/merge-worktree-back")
	}
	if strings.TrimSpace(mergeResult.TargetBranch) != strings.TrimSpace(result.ParentBranch) {
		t.Fatalf("merge target branch = %q, want %q", mergeResult.TargetBranch, result.ParentBranch)
	}
	if mergeResult.LinkedTodoID != item.ID || strings.TrimSpace(mergeResult.LinkedTodoText) != strings.TrimSpace(item.Text) || strings.TrimSpace(mergeResult.LinkedTodoPath) != strings.TrimSpace(item.ProjectPath) {
		t.Fatalf("merge linked todo = %#v, want todo id/text/path for %#v", mergeResult, item)
	}

	featurePath := filepath.Join(projectPath, "FEATURE.txt")
	if got, err := os.ReadFile(featurePath); err != nil {
		t.Fatalf("read merged file from root: %v", err)
	} else if strings.TrimSpace(string(got)) != "merged from linked worktree" {
		t.Fatalf("merged file contents = %q, want merged worktree content", string(got))
	}

	rootStatus, err := scanner.ReadGitRepoStatus(ctx, projectPath)
	if err != nil {
		t.Fatalf("read root git status after merge-back: %v", err)
	}
	if rootStatus.Dirty {
		t.Fatalf("root repo should be clean after merge-back, got %#v", rootStatus)
	}
	if strings.TrimSpace(rootStatus.Branch) != strings.TrimSpace(result.ParentBranch) {
		t.Fatalf("root branch after merge-back = %q, want %q", rootStatus.Branch, result.ParentBranch)
	}

	rootDetail, err := st.GetProjectDetail(ctx, projectPath, 5)
	if err != nil {
		t.Fatalf("GetProjectDetail() for root after merge-back error = %v", err)
	}
	if strings.TrimSpace(rootDetail.Summary.RepoBranch) != strings.TrimSpace(result.ParentBranch) {
		t.Fatalf("stored root branch after merge-back = %q, want %q", rootDetail.Summary.RepoBranch, result.ParentBranch)
	}

	worktreeDetail, err := st.GetProjectDetail(ctx, result.WorktreePath, 5)
	if err != nil {
		t.Fatalf("GetProjectDetail() for worktree after merge-back error = %v", err)
	}
	if strings.TrimSpace(worktreeDetail.Summary.WorktreeParentBranch) != strings.TrimSpace(result.ParentBranch) {
		t.Fatalf("stored worktree parent branch after merge-back = %q, want %q", worktreeDetail.Summary.WorktreeParentBranch, result.ParentBranch)
	}
	if worktreeDetail.Summary.WorktreeMergeStatus != model.WorktreeMergeStatusMerged {
		t.Fatalf("stored worktree merge status after merge-back = %q, want %q", worktreeDetail.Summary.WorktreeMergeStatus, model.WorktreeMergeStatusMerged)
	}
	if worktreeDetail.Summary.WorktreeOriginTodoID != item.ID {
		t.Fatalf("stored worktree origin todo after merge-back = %d, want %d", worktreeDetail.Summary.WorktreeOriginTodoID, item.ID)
	}

	alreadyMergedResult, err := svc.MergeWorktreeBack(ctx, result.WorktreePath)
	if err != nil {
		t.Fatalf("second MergeWorktreeBack() error = %v", err)
	}
	if !alreadyMergedResult.AlreadyMerged {
		t.Fatalf("second MergeWorktreeBack() should report already merged, got %#v", alreadyMergedResult)
	}
}

func TestWorktreeMergeStatusTreatsCherryPickedBranchAsMergedOnNonMasterParent(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	projectPath := filepath.Join(root, "repo")
	initGitRepo(t, projectPath)
	runGit(t, projectPath, "git", "checkout", "-b", "master_mobnext")

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	svc := New(config.Default(), st, events.NewBus(), nil)
	if _, err := svc.CreateOrAttachProject(ctx, CreateOrAttachProjectRequest{
		ParentPath: root,
		Name:       "repo",
	}); err != nil {
		t.Fatalf("track root project: %v", err)
	}

	result := createSuggestedTodoWorktreeForTest(t, ctx, svc, st, projectPath, "Cherry-pick a linked worktree back", "feat/cherry-equivalent", "feat-cherry-equivalent")
	if result.ParentBranch != "master_mobnext" {
		t.Fatalf("parent branch = %q, want master_mobnext", result.ParentBranch)
	}

	if err := os.WriteFile(filepath.Join(result.WorktreePath, "FEATURE.txt"), []byte("cherry-picked from linked worktree\n"), 0o644); err != nil {
		t.Fatalf("write FEATURE.txt in worktree: %v", err)
	}
	runGit(t, result.WorktreePath, "git", "add", "FEATURE.txt")
	runGit(t, result.WorktreePath, "git", "commit", "-m", "add cherry-equivalent worktree feature")
	if err := os.WriteFile(filepath.Join(projectPath, "ROOT.txt"), []byte("root moved before cherry-pick\n"), 0o644); err != nil {
		t.Fatalf("write ROOT.txt in root: %v", err)
	}
	runGit(t, projectPath, "git", "add", "ROOT.txt")
	runGit(t, projectPath, "git", "commit", "-m", "advance root before cherry-pick")
	runGit(t, projectPath, "git", "cherry-pick", "feat/cherry-equivalent")

	if ancestorMerged, err := gitBranchMergedIntoBranch(ctx, projectPath, "feat/cherry-equivalent", "master_mobnext"); err != nil {
		t.Fatalf("check ancestry merge status: %v", err)
	} else if ancestorMerged {
		t.Fatalf("test setup should use a patch-equivalent non-ancestor branch")
	}

	if err := svc.RefreshProjectStatus(ctx, result.WorktreePath); err != nil {
		t.Fatalf("RefreshProjectStatus() for cherry-picked worktree error = %v", err)
	}
	worktreeDetail, err := st.GetProjectDetail(ctx, result.WorktreePath, 5)
	if err != nil {
		t.Fatalf("GetProjectDetail() for cherry-picked worktree error = %v", err)
	}
	if worktreeDetail.Summary.WorktreeMergeStatus != model.WorktreeMergeStatusMerged {
		t.Fatalf("cherry-picked worktree should be marked merged, got %#v", worktreeDetail.Summary)
	}

	rootHeadBefore := strings.TrimSpace(gitOutput(t, projectPath, "git", "rev-parse", "HEAD"))
	mergeResult, err := svc.MergeWorktreeBack(ctx, result.WorktreePath)
	if err != nil {
		t.Fatalf("MergeWorktreeBack() after cherry-pick error = %v", err)
	}
	if !mergeResult.AlreadyMerged {
		t.Fatalf("MergeWorktreeBack() should report already merged for patch-equivalent worktree, got %#v", mergeResult)
	}
	rootHeadAfter := strings.TrimSpace(gitOutput(t, projectPath, "git", "rev-parse", "HEAD"))
	if rootHeadAfter != rootHeadBefore {
		t.Fatalf("already-merged patch-equivalent worktree changed root HEAD from %s to %s", rootHeadBefore, rootHeadAfter)
	}
}

func TestWorktreeMergeStatusRecognizesConflictResolvedMergeOnNonMasterParent(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	projectPath := filepath.Join(root, "repo")
	initGitRepo(t, projectPath)
	runGit(t, projectPath, "git", "checkout", "-b", "master_mobnext")

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	svc := New(config.Default(), st, events.NewBus(), nil)
	if _, err := svc.CreateOrAttachProject(ctx, CreateOrAttachProjectRequest{
		ParentPath: root,
		Name:       "repo",
	}); err != nil {
		t.Fatalf("track root project: %v", err)
	}

	result := createSuggestedTodoWorktreeForTest(t, ctx, svc, st, projectPath, "Resolve a linked worktree merge conflict", "feat/conflict-resolved", "feat-conflict-resolved")
	if result.ParentBranch != "master_mobnext" {
		t.Fatalf("parent branch = %q, want master_mobnext", result.ParentBranch)
	}

	if err := os.WriteFile(filepath.Join(projectPath, "README.md"), []byte("hello from root\n"), 0o644); err != nil {
		t.Fatalf("write README in root: %v", err)
	}
	runGit(t, projectPath, "git", "add", "README.md")
	runGit(t, projectPath, "git", "commit", "-m", "root edit before conflict")

	if err := os.WriteFile(filepath.Join(result.WorktreePath, "README.md"), []byte("hello from worktree\n"), 0o644); err != nil {
		t.Fatalf("write README in worktree: %v", err)
	}
	runGit(t, result.WorktreePath, "git", "add", "README.md")
	runGit(t, result.WorktreePath, "git", "commit", "-m", "worktree edit before conflict")

	cmd := exec.Command("git", "-C", projectPath, "merge", "--no-ff", "feat/conflict-resolved")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("merge unexpectedly succeeded; output:\n%s", string(out))
	}
	if !strings.Contains(string(out), "CONFLICT") {
		t.Fatalf("merge output = %q, want conflict", string(out))
	}

	if err := os.WriteFile(filepath.Join(projectPath, "README.md"), []byte("hello from root\nhello from worktree\n"), 0o644); err != nil {
		t.Fatalf("write resolved README in root: %v", err)
	}
	runGit(t, projectPath, "git", "add", "README.md")

	if err := svc.RefreshProjectStatus(ctx, result.WorktreePath); err != nil {
		t.Fatalf("RefreshProjectStatus() for conflict-resolved worktree before merge commit error = %v", err)
	}
	inProgressDetail, err := st.GetProjectDetail(ctx, result.WorktreePath, 5)
	if err != nil {
		t.Fatalf("GetProjectDetail() for conflict-resolved worktree before merge commit error = %v", err)
	}
	if inProgressDetail.Summary.WorktreeMergeStatus != model.WorktreeMergeStatusMergeInProgress {
		t.Fatalf("conflict-resolved-but-uncommitted worktree merge status = %q, want %q", inProgressDetail.Summary.WorktreeMergeStatus, model.WorktreeMergeStatusMergeInProgress)
	}

	runGit(t, projectPath, "git", "commit", "-m", "merge conflict-resolved worktree")

	if ancestorMerged, err := gitBranchMergedIntoBranch(ctx, projectPath, "feat/conflict-resolved", "master_mobnext"); err != nil {
		t.Fatalf("check ancestry merge status: %v", err)
	} else if !ancestorMerged {
		t.Fatalf("conflict-resolved merge should make the source branch an ancestor")
	}

	if err := svc.RefreshProjectStatus(ctx, result.WorktreePath); err != nil {
		t.Fatalf("RefreshProjectStatus() for conflict-resolved worktree error = %v", err)
	}
	worktreeDetail, err := st.GetProjectDetail(ctx, result.WorktreePath, 5)
	if err != nil {
		t.Fatalf("GetProjectDetail() for conflict-resolved worktree error = %v", err)
	}
	if worktreeDetail.Summary.WorktreeMergeStatus != model.WorktreeMergeStatusMerged {
		t.Fatalf("conflict-resolved worktree should be marked merged, got %#v", worktreeDetail.Summary)
	}
}

func TestRefreshProjectStatusForRootUpdatesLinkedWorktreeMergeStatus(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	projectPath := filepath.Join(root, "repo")
	initGitRepo(t, projectPath)

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	svc := New(config.Default(), st, events.NewBus(), nil)
	if _, err := svc.CreateOrAttachProject(ctx, CreateOrAttachProjectRequest{
		ParentPath: root,
		Name:       "repo",
	}); err != nil {
		t.Fatalf("track root project: %v", err)
	}

	result := createSuggestedTodoWorktreeForTest(t, ctx, svc, st, projectPath, "Refresh linked merge status from root", "feat/root-refresh-status", "feat-root-refresh-status")
	if err := os.WriteFile(filepath.Join(result.WorktreePath, "FEATURE.txt"), []byte("merged by hand\n"), 0o644); err != nil {
		t.Fatalf("write FEATURE.txt in worktree: %v", err)
	}
	runGit(t, result.WorktreePath, "git", "add", "FEATURE.txt")
	runGit(t, result.WorktreePath, "git", "commit", "-m", "add root-refresh feature")
	if err := svc.RefreshProjectStatus(ctx, result.WorktreePath); err != nil {
		t.Fatalf("RefreshProjectStatus() for diverged worktree error = %v", err)
	}
	divergedWorktreeDetail, err := st.GetProjectDetail(ctx, result.WorktreePath, 5)
	if err != nil {
		t.Fatalf("GetProjectDetail() for diverged worktree error = %v", err)
	}
	if divergedWorktreeDetail.Summary.WorktreeMergeStatus != model.WorktreeMergeStatusNotMerged {
		t.Fatalf("diverged worktree merge status = %q, want %q", divergedWorktreeDetail.Summary.WorktreeMergeStatus, model.WorktreeMergeStatusNotMerged)
	}

	runGit(t, projectPath, "git", "merge", "--no-ff", "feat/root-refresh-status", "-m", "merge root-refresh worktree")
	if err := svc.RefreshProjectStatus(ctx, projectPath); err != nil {
		t.Fatalf("RefreshProjectStatus() for root after manual merge error = %v", err)
	}
	worktreeDetail, err := st.GetProjectDetail(ctx, result.WorktreePath, 5)
	if err != nil {
		t.Fatalf("GetProjectDetail() for worktree after root refresh error = %v", err)
	}
	if worktreeDetail.Summary.WorktreeMergeStatus != model.WorktreeMergeStatusMerged {
		t.Fatalf("root refresh should update linked worktree merge status = %q, want %q", worktreeDetail.Summary.WorktreeMergeStatus, model.WorktreeMergeStatusMerged)
	}
}

func TestRefreshProjectStatusForRootSkipsForgottenMissingLinkedWorktrees(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	projectPath := filepath.Join(root, "repo")
	missingWorktreePath := filepath.Join(root, "repo--missing")
	initGitRepo(t, projectPath)

	dbPath := filepath.Join(t.TempDir(), "little-control-room.sqlite")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	oldUpdatedAt := time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Second)
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:          projectPath,
		Name:          "repo",
		Status:        model.StatusIdle,
		PresentOnDisk: true,
		InScope:       true,
		UpdatedAt:     oldUpdatedAt,
	}); err != nil {
		t.Fatalf("seed root project: %v", err)
	}
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:             missingWorktreePath,
		Name:             "repo--missing",
		Status:           model.StatusIdle,
		PresentOnDisk:    false,
		WorktreeRootPath: projectPath,
		WorktreeKind:     model.WorktreeKindLinked,
		Forgotten:        true,
		InScope:          true,
		UpdatedAt:        oldUpdatedAt,
	}); err != nil {
		t.Fatalf("seed missing worktree project: %v", err)
	}

	cfg := config.Default()
	cfg.IncludePaths = nil
	svc := New(cfg, st, events.NewBus(), nil)
	if err := svc.RefreshProjectStatus(ctx, projectPath); err != nil {
		t.Fatalf("RefreshProjectStatus(root) error = %v", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open verification db: %v", err)
	}
	defer db.Close()

	var updatedAt int64
	if err := db.QueryRowContext(ctx, `SELECT updated_at FROM projects WHERE path = ?`, missingWorktreePath).Scan(&updatedAt); err != nil {
		t.Fatalf("read missing worktree updated_at: %v", err)
	}
	if updatedAt != oldUpdatedAt.Unix() {
		t.Fatalf("missing linked worktree updated_at = %d, want unchanged %d", updatedAt, oldUpdatedAt.Unix())
	}
}

func TestRefreshProjectStatusWithOptionsSkipsLinkedWorktreeCascade(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	projectPath := filepath.Join(root, "repo")
	linkedPath := filepath.Join(root, "repo--linked")
	initGitRepo(t, projectPath)
	initGitRepo(t, linkedPath)

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	now := time.Now().UTC().Truncate(time.Second)
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:             projectPath,
		Name:             "repo",
		Status:           model.StatusIdle,
		PresentOnDisk:    true,
		WorktreeRootPath: projectPath,
		WorktreeKind:     model.WorktreeKindMain,
		InScope:          true,
		UpdatedAt:        now,
	}); err != nil {
		t.Fatalf("seed root project: %v", err)
	}
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:             linkedPath,
		Name:             "repo--linked",
		Status:           model.StatusIdle,
		PresentOnDisk:    true,
		WorktreeRootPath: projectPath,
		WorktreeKind:     model.WorktreeKindLinked,
		InScope:          true,
		UpdatedAt:        now,
	}); err != nil {
		t.Fatalf("seed linked project: %v", err)
	}

	svc := New(config.Default(), st, events.NewBus(), nil)
	svc.gitRepoStatusReader = func(_ context.Context, path string) (scanner.GitRepoStatus, error) {
		if filepath.Clean(path) == linkedPath {
			t.Fatalf("linked worktree git status should not be read when cascade is skipped")
		}
		return scanner.GitRepoStatus{Branch: "master"}, nil
	}
	svc.gitWorktreeInfoReader = func(_ context.Context, path string) (scanner.GitWorktreeInfo, error) {
		if filepath.Clean(path) == linkedPath {
			t.Fatalf("linked worktree info should not be read when cascade is skipped")
		}
		return scanner.GitWorktreeInfo{
			RootPath:     projectPath,
			TopLevelPath: projectPath,
			Kind:         scanner.GitWorktreeKindMain,
		}, nil
	}
	svc.gitWorktreeListReader = func(context.Context, string) ([]scanner.GitWorktree, error) {
		t.Fatalf("linked worktree list should not be read when cascade is skipped")
		return nil, nil
	}

	if err := svc.RefreshProjectStatusWithOptions(ctx, projectPath, ScanOptions{SkipLinkedWorktreeStatusRefresh: true}); err != nil {
		t.Fatalf("RefreshProjectStatusWithOptions() error = %v", err)
	}
}

func TestScanPurgesExpiredMissingLinkedWorktrees(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	expiredPath := filepath.Join(t.TempDir(), "repo--expired")
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:             expiredPath,
		Name:             "repo--expired",
		Status:           model.StatusIdle,
		PresentOnDisk:    false,
		WorktreeRootPath: filepath.Dir(expiredPath),
		WorktreeKind:     model.WorktreeKindLinked,
		Forgotten:        true,
		InScope:          true,
		UpdatedAt:        time.Now().Add(-8 * 24 * time.Hour),
	}); err != nil {
		t.Fatalf("seed expired missing worktree: %v", err)
	}

	cfg := config.Default()
	cfg.IncludePaths = nil
	svc := New(cfg, st, events.NewBus(), nil)
	if _, err := svc.ScanOnce(ctx); err != nil {
		t.Fatalf("ScanOnce() error = %v", err)
	}
	if _, err := st.GetProjectDetail(ctx, expiredPath, 5); err == nil || !strings.Contains(err.Error(), "project not found") {
		t.Fatalf("expired missing worktree lookup error = %v, want project not found", err)
	}
}

func TestMergeWorktreeBackSyncsRootSubmoduleAfterMerge(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	projectPath := filepath.Join(root, "repo")
	submoduleRootPath := filepath.Join(root, "assets")
	submodulePath := initGitRepoWithPushableSubmodule(t, projectPath, submoduleRootPath, "assets_src")
	initialSubmoduleHead := strings.TrimSpace(gitOutput(t, submodulePath, "git", "rev-parse", "HEAD"))

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	svc := New(config.Default(), st, events.NewBus(), nil)
	if _, err := svc.CreateOrAttachProject(ctx, CreateOrAttachProjectRequest{
		ParentPath: root,
		Name:       "repo",
	}); err != nil {
		t.Fatalf("track root project: %v", err)
	}

	item, err := svc.AddTodo(ctx, projectPath, "Merge back a worktree that bumps a submodule")
	if err != nil {
		t.Fatalf("add todo: %v", err)
	}
	queueTodoWorktreeSuggestionForTest(t, ctx, st, item.ID)
	suggestion, err := st.ClaimNextQueuedTodoWorktreeSuggestion(ctx, 0, 0)
	if err != nil {
		t.Fatalf("claim todo worktree suggestion: %v", err)
	}
	suggestion.BranchName = "feat/merge-worktree-submodule"
	suggestion.WorktreeSuffix = "feat-merge-worktree-submodule"
	suggestion.Kind = "feature"
	suggestion.Reason = "Creates a linked worktree so merge-back can sync submodules in the root checkout."
	suggestion.Confidence = 0.95
	suggestion.Model = "test"
	if completed, err := st.CompleteTodoWorktreeSuggestion(ctx, suggestion); err != nil {
		t.Fatalf("complete todo worktree suggestion: %v", err)
	} else if !completed {
		t.Fatalf("expected todo worktree suggestion to complete")
	}

	result, err := svc.CreateTodoWorktree(ctx, CreateTodoWorktreeRequest{
		ProjectPath: projectPath,
		TodoID:      item.ID,
		PrepProfile: worktreeprep.RecursiveSubmodulesProfile,
	})
	if err != nil {
		t.Fatalf("CreateTodoWorktree() error = %v", err)
	}

	runGit(t, result.WorktreePath, "git", "-c", "protocol.file.allow=always", "submodule", "update", "--init", "--recursive")

	worktreeSubmodulePath := filepath.Join(result.WorktreePath, "assets_src")
	runGit(t, worktreeSubmodulePath, "git", "checkout", "master")
	if err := os.WriteFile(filepath.Join(worktreeSubmodulePath, "README.md"), []byte("hello\nsubmodule edit\n"), 0o644); err != nil {
		t.Fatalf("write submodule README in worktree: %v", err)
	}
	runGit(t, worktreeSubmodulePath, "git", "add", "README.md")
	runGit(t, worktreeSubmodulePath, "git", "commit", "-m", "update submodule from worktree")
	runGit(t, worktreeSubmodulePath, "git", "push")
	updatedSubmoduleHead := strings.TrimSpace(gitOutput(t, worktreeSubmodulePath, "git", "rev-parse", "HEAD"))
	if updatedSubmoduleHead == initialSubmoduleHead {
		t.Fatalf("expected worktree submodule head to advance, still at %q", updatedSubmoduleHead)
	}

	runGit(t, result.WorktreePath, "git", "add", "assets_src")
	runGit(t, result.WorktreePath, "git", "commit", "-m", "bump submodule pointer")

	mergeResult, err := svc.MergeWorktreeBack(ctx, result.WorktreePath)
	if err != nil {
		t.Fatalf("MergeWorktreeBack() error = %v", err)
	}
	if mergeResult.RootProjectPath != projectPath {
		t.Fatalf("merge root path = %q, want %q", mergeResult.RootProjectPath, projectPath)
	}

	rootSubmoduleHead := strings.TrimSpace(gitOutput(t, submodulePath, "git", "rev-parse", "HEAD"))
	if rootSubmoduleHead != updatedSubmoduleHead {
		t.Fatalf("root submodule head after merge-back = %q, want %q", rootSubmoduleHead, updatedSubmoduleHead)
	}

	rootStatus, err := scanner.ReadGitRepoStatus(ctx, projectPath)
	if err != nil {
		t.Fatalf("read root git status after merge-back: %v", err)
	}
	if rootStatus.Dirty {
		t.Fatalf("root repo should be clean after merge-back with submodule sync, got %#v", rootStatus)
	}
}

func TestCommitAndMergeWorktreeBackCommitsDirtyWorktreeBeforeMerge(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	projectPath := filepath.Join(root, "repo")
	initGitRepo(t, projectPath)

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	svc := New(config.Default(), st, events.NewBus(), nil)
	if _, err := svc.CreateOrAttachProject(ctx, CreateOrAttachProjectRequest{
		ParentPath: root,
		Name:       "repo",
	}); err != nil {
		t.Fatalf("track root project: %v", err)
	}

	item, err := svc.AddTodo(ctx, projectPath, "Commit dirty worktree and merge it back")
	if err != nil {
		t.Fatalf("add todo: %v", err)
	}
	queueTodoWorktreeSuggestionForTest(t, ctx, st, item.ID)
	suggestion, err := st.ClaimNextQueuedTodoWorktreeSuggestion(ctx, 0, 0)
	if err != nil {
		t.Fatalf("claim todo worktree suggestion: %v", err)
	}
	suggestion.BranchName = "feat/commit-and-merge-worktree"
	suggestion.WorktreeSuffix = "feat-commit-and-merge-worktree"
	suggestion.Kind = "feature"
	suggestion.Reason = "Creates a linked worktree so auto commit-and-merge can be tested."
	suggestion.Confidence = 0.95
	suggestion.Model = "test"
	if completed, err := st.CompleteTodoWorktreeSuggestion(ctx, suggestion); err != nil {
		t.Fatalf("complete todo worktree suggestion: %v", err)
	} else if !completed {
		t.Fatalf("expected todo worktree suggestion to complete")
	}

	result, err := svc.CreateTodoWorktree(ctx, CreateTodoWorktreeRequest{
		ProjectPath: projectPath,
		TodoID:      item.ID,
	})
	if err != nil {
		t.Fatalf("CreateTodoWorktree() error = %v", err)
	}

	worktreeFile := filepath.Join(result.WorktreePath, "FEATURE.txt")
	if err := os.WriteFile(worktreeFile, []byte("committed and merged from dirty worktree\n"), 0o644); err != nil {
		t.Fatalf("write FEATURE.txt in worktree: %v", err)
	}

	mergeResult, err := svc.CommitAndMergeWorktreeBack(ctx, result.WorktreePath)
	if err != nil {
		t.Fatalf("CommitAndMergeWorktreeBack() error = %v", err)
	}
	if strings.TrimSpace(mergeResult.CommitHash) == "" {
		t.Fatalf("CommitAndMergeWorktreeBack() should report the created commit hash, got %#v", mergeResult)
	}
	if mergeResult.RootProjectPath != projectPath {
		t.Fatalf("merge root path = %q, want %q", mergeResult.RootProjectPath, projectPath)
	}
	if mergeResult.SourceBranch != "feat/commit-and-merge-worktree" {
		t.Fatalf("merge source branch = %q, want feat/commit-and-merge-worktree", mergeResult.SourceBranch)
	}
	if strings.TrimSpace(mergeResult.TargetBranch) != strings.TrimSpace(result.ParentBranch) {
		t.Fatalf("merge target branch = %q, want %q", mergeResult.TargetBranch, result.ParentBranch)
	}

	featurePath := filepath.Join(projectPath, "FEATURE.txt")
	if got, err := os.ReadFile(featurePath); err != nil {
		t.Fatalf("read merged file from root: %v", err)
	} else if strings.TrimSpace(string(got)) != "committed and merged from dirty worktree" {
		t.Fatalf("merged file contents = %q, want committed dirty-worktree content", string(got))
	}

	rootStatus, err := scanner.ReadGitRepoStatus(ctx, projectPath)
	if err != nil {
		t.Fatalf("read root git status after commit-and-merge: %v", err)
	}
	if rootStatus.Dirty {
		t.Fatalf("root repo should be clean after commit-and-merge, got %#v", rootStatus)
	}

	worktreeStatus, err := scanner.ReadGitRepoStatus(ctx, result.WorktreePath)
	if err != nil {
		t.Fatalf("read worktree git status after commit-and-merge: %v", err)
	}
	if worktreeStatus.Dirty {
		t.Fatalf("worktree should be clean after commit-and-merge, got %#v", worktreeStatus)
	}

	worktreeDetail, err := st.GetProjectDetail(ctx, result.WorktreePath, 5)
	if err != nil {
		t.Fatalf("GetProjectDetail() for worktree after commit-and-merge error = %v", err)
	}
	if worktreeDetail.Summary.WorktreeMergeStatus != model.WorktreeMergeStatusMerged {
		t.Fatalf("stored worktree merge status after commit-and-merge = %q, want %q", worktreeDetail.Summary.WorktreeMergeStatus, model.WorktreeMergeStatusMerged)
	}
}

func TestMergeWorktreeBackReportsConflictAndRefreshesStatus(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	projectPath := filepath.Join(root, "repo")
	initGitRepo(t, projectPath)

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	svc := New(config.Default(), st, events.NewBus(), nil)
	if _, err := svc.CreateOrAttachProject(ctx, CreateOrAttachProjectRequest{
		ParentPath: root,
		Name:       "repo",
	}); err != nil {
		t.Fatalf("track root project: %v", err)
	}

	item, err := svc.AddTodo(ctx, projectPath, "Create a worktree conflict")
	if err != nil {
		t.Fatalf("add todo: %v", err)
	}
	queueTodoWorktreeSuggestionForTest(t, ctx, st, item.ID)
	suggestion, err := st.ClaimNextQueuedTodoWorktreeSuggestion(ctx, 0, 0)
	if err != nil {
		t.Fatalf("claim todo worktree suggestion: %v", err)
	}
	suggestion.BranchName = "feat/merge-worktree-conflict"
	suggestion.WorktreeSuffix = "feat-merge-worktree-conflict"
	suggestion.Kind = "feature"
	suggestion.Reason = "Creates a linked worktree so merge conflict handling can be tested."
	suggestion.Confidence = 0.95
	suggestion.Model = "test"
	if completed, err := st.CompleteTodoWorktreeSuggestion(ctx, suggestion); err != nil {
		t.Fatalf("complete todo worktree suggestion: %v", err)
	} else if !completed {
		t.Fatalf("expected todo worktree suggestion to complete")
	}

	result, err := svc.CreateTodoWorktree(ctx, CreateTodoWorktreeRequest{
		ProjectPath: projectPath,
		TodoID:      item.ID,
	})
	if err != nil {
		t.Fatalf("CreateTodoWorktree() error = %v", err)
	}

	if err := os.WriteFile(filepath.Join(projectPath, "README.md"), []byte("hello from root\n"), 0o644); err != nil {
		t.Fatalf("write README in root: %v", err)
	}
	runGit(t, projectPath, "git", "add", "README.md")
	runGit(t, projectPath, "git", "commit", "-m", "root change")

	if err := os.WriteFile(filepath.Join(result.WorktreePath, "README.md"), []byte("hello from worktree\n"), 0o644); err != nil {
		t.Fatalf("write README in worktree: %v", err)
	}
	runGit(t, result.WorktreePath, "git", "add", "README.md")
	runGit(t, result.WorktreePath, "git", "commit", "-m", "worktree change")

	_, err = svc.MergeWorktreeBack(ctx, result.WorktreePath)
	if err == nil {
		t.Fatalf("MergeWorktreeBack() expected conflict error")
	}
	if !strings.Contains(err.Error(), "merge conflict while merging feat/merge-worktree-conflict") {
		t.Fatalf("merge conflict error = %q, want actionable conflict message", err)
	}
	if !strings.Contains(err.Error(), "Conflicted files:") {
		t.Fatalf("merge conflict error = %q, want conflicted-files section", err)
	}
	if !strings.Contains(err.Error(), "README.md") {
		t.Fatalf("merge conflict error = %q, want conflicted file name", err)
	}

	rootStatus, err := scanner.ReadGitRepoStatus(ctx, projectPath)
	if err != nil {
		t.Fatalf("read root git status after merge conflict: %v", err)
	}
	if !rootStatus.Dirty {
		t.Fatalf("root repo should be dirty after merge conflict, got %#v", rootStatus)
	}
	if conflicted := conflictedPaths(rootStatus); len(conflicted) == 0 || conflicted[0] != "README.md" {
		t.Fatalf("conflicted paths = %#v, want README.md", conflicted)
	}

	rootDetail, err := st.GetProjectDetail(ctx, projectPath, 5)
	if err != nil {
		t.Fatalf("GetProjectDetail() for root after merge conflict error = %v", err)
	}
	if !rootDetail.Summary.RepoDirty {
		t.Fatalf("stored root detail should refresh to dirty after merge conflict: %#v", rootDetail.Summary)
	}
	if !rootDetail.Summary.RepoConflict {
		t.Fatalf("stored root detail should refresh to conflict after merge conflict: %#v", rootDetail.Summary)
	}

	worktreeDetail, err := st.GetProjectDetail(ctx, result.WorktreePath, 5)
	if err != nil {
		t.Fatalf("GetProjectDetail() for worktree after merge conflict error = %v", err)
	}
	if worktreeDetail.Summary.WorktreeMergeStatus != model.WorktreeMergeStatusMergeInProgress {
		t.Fatalf("stored worktree merge status after merge conflict = %q, want %q", worktreeDetail.Summary.WorktreeMergeStatus, model.WorktreeMergeStatusMergeInProgress)
	}
}

func TestMergeWorktreeBackReportsGitIndexLockActionably(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	projectPath := filepath.Join(root, "repo")
	initGitRepo(t, projectPath)

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	svc := New(config.Default(), st, events.NewBus(), nil)
	if _, err := svc.CreateOrAttachProject(ctx, CreateOrAttachProjectRequest{
		ParentPath: root,
		Name:       "repo",
	}); err != nil {
		t.Fatalf("track root project: %v", err)
	}

	item, err := svc.AddTodo(ctx, projectPath, "Create a merge blocked by a stale git index lock")
	if err != nil {
		t.Fatalf("add todo: %v", err)
	}
	queueTodoWorktreeSuggestionForTest(t, ctx, st, item.ID)
	suggestion, err := st.ClaimNextQueuedTodoWorktreeSuggestion(ctx, 0, 0)
	if err != nil {
		t.Fatalf("claim todo worktree suggestion: %v", err)
	}
	suggestion.BranchName = "feat/merge-worktree-lock"
	suggestion.WorktreeSuffix = "feat-merge-worktree-lock"
	suggestion.Kind = "feature"
	suggestion.Reason = "Creates a linked worktree so git index.lock handling can be tested."
	suggestion.Confidence = 0.95
	suggestion.Model = "test"
	if completed, err := st.CompleteTodoWorktreeSuggestion(ctx, suggestion); err != nil {
		t.Fatalf("complete todo worktree suggestion: %v", err)
	} else if !completed {
		t.Fatalf("expected todo worktree suggestion to complete")
	}

	result, err := svc.CreateTodoWorktree(ctx, CreateTodoWorktreeRequest{
		ProjectPath: projectPath,
		TodoID:      item.ID,
	})
	if err != nil {
		t.Fatalf("CreateTodoWorktree() error = %v", err)
	}

	if err := os.WriteFile(filepath.Join(result.WorktreePath, "FEATURE.txt"), []byte("blocked by git lock\n"), 0o644); err != nil {
		t.Fatalf("write FEATURE.txt in worktree: %v", err)
	}
	runGit(t, result.WorktreePath, "git", "add", "FEATURE.txt")
	runGit(t, result.WorktreePath, "git", "commit", "-m", "prepare merge blocked by lock")

	lockPath := filepath.Join(projectPath, ".git", "index.lock")
	if err := os.WriteFile(lockPath, []byte("locked\n"), 0o644); err != nil {
		t.Fatalf("write index.lock: %v", err)
	}

	_, err = svc.MergeWorktreeBack(ctx, result.WorktreePath)
	if err == nil {
		t.Fatalf("MergeWorktreeBack() expected index.lock error")
	}
	if !strings.Contains(err.Error(), "git index.lock already exists at ") || !strings.Contains(err.Error(), filepath.ToSlash(filepath.Join("repo", ".git", "index.lock"))) {
		t.Fatalf("merge-back error = %q, want lock-path guidance", err)
	}
	if !strings.Contains(err.Error(), "remove the stale lock") {
		t.Fatalf("merge-back error = %q, want stale-lock recovery guidance", err)
	}
}
