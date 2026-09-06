package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"lcroom/internal/config"
	"lcroom/internal/detectors"
	"lcroom/internal/events"
	"lcroom/internal/model"
	"lcroom/internal/scanner"
	"lcroom/internal/store"
)

func TestScanOnceHidesAutomaticSubmodulesAndSkipsTheirWorktrees(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	projectPath := filepath.Join(root, "repo")
	assetsPath := initGitRepoWithSubmodule(t, projectPath, filepath.Join(root, "source"), "Apps/Game/Assets")
	parentWorktree := filepath.Join(root, "repo--feature")
	runGit(t, projectPath, "git", "worktree", "add", "-b", "feature", parentWorktree)
	runGit(t, parentWorktree, "git", "-c", "protocol.file.allow=always", "submodule", "update", "--init")
	linkedAssets := filepath.Join(parentWorktree, "Apps/Game/Assets")
	mergePath := filepath.Join(root, "retained-merge", "Assets")
	runGit(t, linkedAssets, "git", "worktree", "add", "-b", "merge-assets", mergePath)
	ordinaryAssets := filepath.Join(root, "Assets")
	initGitRepo(t, ordinaryAssets)

	st, err := store.Open(filepath.Join(root, "test.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for _, path := range []string{projectPath, assetsPath, linkedAssets, mergePath, ordinaryAssets} {
		info, err := scanner.ReadGitWorktreeInfo(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
		if err := st.UpsertProjectState(ctx, model.ProjectState{
			Path: path, Name: filepath.Base(path), PresentOnDisk: true,
			InScope: true, Status: model.StatusIdle, UpdatedAt: time.Now().Add(-time.Hour),
			WorktreeRootPath: info.RootPath, WorktreeKind: modelWorktreeKindFromGit(info.Kind),
		}); err != nil {
			t.Fatal(err)
		}
	}

	cfg := config.Default()
	cfg.IncludePaths = []string{root}
	svc := New(cfg, st, events.NewBus(), nil)
	svc.SetSessionClassifier(nil)
	originalListReader := svc.gitWorktreeListReader
	svc.gitWorktreeListReader = func(ctx context.Context, path string) ([]scanner.GitWorktree, error) {
		for _, submodule := range []string{assetsPath, linkedAssets, mergePath} {
			if path == submodule {
				t.Errorf("scan expanded submodule worktrees at %s", path)
			}
		}
		return originalListReader(ctx, path)
	}
	for scan := 0; scan < 2; scan++ {
		if _, err := svc.ScanOnce(ctx); err != nil {
			t.Fatal(err)
		}
		projects, err := st.ListProjects(ctx, false)
		if err != nil {
			t.Fatal(err)
		}
		want := map[string]bool{projectPath: true, parentWorktree: true, ordinaryAssets: true}
		for _, project := range projects {
			if !want[project.Path] {
				t.Errorf("scan %d: unexpected project %s", scan, project.Path)
			}
			delete(want, project.Path)
		}
		if len(want) != 0 {
			t.Errorf("scan %d: missing projects %v", scan, want)
		}
		for _, path := range []string{assetsPath, linkedAssets, mergePath} {
			detail, err := st.GetProjectDetail(ctx, path, 10)
			if err != nil {
				t.Fatal(err)
			}
			if !detail.Summary.Forgotten || detail.Summary.InScope {
				t.Errorf("submodule remains visible: %#v", detail.Summary)
			}
			if _, err := os.Stat(filepath.Join(path, ".git")); err != nil {
				t.Fatalf("submodule checkout was changed: %v", err)
			}
		}
	}

	// A real session against a previously hidden submodule restores that project.
	svc = New(cfg, st, events.NewBus(), []detectors.Detector{staticDetector{
		name: "codex", activities: map[string]*model.DetectorProjectActivity{
			assetsPath: fakeActivity(assetsPath, "submodule-work", time.Now()),
		},
	}})
	svc.SetSessionClassifier(nil)
	if _, err := svc.ScanOnce(ctx); err != nil {
		t.Fatal(err)
	}
	detail, err := st.GetProjectDetail(ctx, assetsPath, 10)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Summary.Forgotten || !detail.Summary.InScope || len(detail.Sessions) != 1 {
		t.Fatalf("recorded submodule work was hidden: %#v", detail)
	}
}

func TestAutomaticSubmodulePreservesIndependentTracking(t *testing.T) {
	info := scanner.GitWorktreeInfo{IsSubmodule: true}
	for name, summary := range map[string]model.ProjectSummary{
		"manual":       {ManuallyAdded: true},
		"pinned":       {Pinned: true},
		"session":      {LatestSessionID: "codex:session"},
		"session seen": {LastSessionSeenAt: time.Now()},
		"open TODO":    {OpenTODOCount: 1},
		"TODO history": {TotalTODOCount: 1},
		"origin TODO":  {WorktreeOriginTodoID: 1},
		"run command":  {RunCommand: "make run"},
		"scratch task": {Kind: model.ProjectKindScratchTask},
	} {
		t.Run(name, func(t *testing.T) {
			if shouldForgetAutomaticSubmoduleProject(summary, nil, info, true) {
				t.Fatal("independently tracked submodule would be hidden")
			}
		})
	}
}
