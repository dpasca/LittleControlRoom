package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lcroom/internal/config"
	"lcroom/internal/detectors"
	"lcroom/internal/events"
	"lcroom/internal/model"
	"lcroom/internal/scanner"
	"lcroom/internal/store"
)

func TestInspectResidualWorktreeDirectoryVerifiesPartialGitRemoval(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	rootPath, residualPath, commit := createVerifiedPartialWorktreeResidue(t)
	svc := &Service{}

	kind, err := svc.residualWorktreeCleanupKind(ctx, rootPath, residualPath)
	if err != nil {
		t.Fatalf("residualWorktreeCleanupKind() error = %v", err)
	}
	if kind != ResidualWorktreeCleanupPartialGitDir {
		t.Fatalf("residualWorktreeCleanupKind() = %q, want %q", kind, ResidualWorktreeCleanupPartialGitDir)
	}
	inspection, err := svc.inspectResidualWorktreeDirectory(ctx, rootPath, residualPath, model.ProjectSummary{RepoBranch: "master"}, "")
	if err != nil {
		t.Fatalf("inspectResidualWorktreeDirectory() error = %v", err)
	}
	if !inspection.Safe || inspection.Kind != ResidualWorktreeCleanupPartialGitDir {
		t.Fatalf("inspection = %#v, want verified partial Git removal", inspection)
	}
	if inspection.Commit != commit {
		t.Fatalf("inspection.Commit = %q, want %q", inspection.Commit, commit)
	}
	if inspection.TrackedFileCount != 3 || inspection.DSStoreCount != 2 || inspection.EmptyDirectoryCount == 0 {
		t.Fatalf("inspection counts = tracked:%d ds_store:%d empty:%d", inspection.TrackedFileCount, inspection.DSStoreCount, inspection.EmptyDirectoryCount)
	}
	if err := removeInspectedResidualWorktreeDirectory(ctx, inspection, residualPath); err != nil {
		t.Fatalf("removeInspectedResidualWorktreeDirectory() error = %v", err)
	}
	if _, err := os.Lstat(residualPath); !os.IsNotExist(err) {
		t.Fatalf("verified residual path still exists: %v", err)
	}
}

func TestInspectResidualWorktreeDirectoryBlocksUnverifiedEntries(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		mutate     func(*testing.T, string)
		wantReason string
	}{
		{
			name: "changed tracked file",
			mutate: func(t *testing.T, residualPath string) {
				writeTestFile(t, filepath.Join(residualPath, "README.md"), "changed\n", 0o644)
			},
			wantReason: "differs from commit",
		},
		{
			name: "untracked file",
			mutate: func(t *testing.T, residualPath string) {
				writeTestFile(t, filepath.Join(residualPath, "important.txt"), "keep me\n", 0o644)
			},
			wantReason: "untracked file important.txt",
		},
		{
			name: "symbolic link",
			mutate: func(t *testing.T, residualPath string) {
				if err := os.Symlink("README.md", filepath.Join(residualPath, "shortcut")); err != nil {
					t.Fatalf("create residual symlink: %v", err)
				}
			},
			wantReason: "symbolic link shortcut",
		},
		{
			name: "changed executable mode",
			mutate: func(t *testing.T, residualPath string) {
				if err := os.Chmod(filepath.Join(residualPath, "script.sh"), 0o644); err != nil {
					t.Fatalf("change residual executable mode: %v", err)
				}
			},
			wantReason: "file mode for script.sh differs",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			rootPath, residualPath, _ := createVerifiedPartialWorktreeResidue(t)
			test.mutate(t, residualPath)
			svc := &Service{}

			inspection, err := svc.inspectResidualWorktreeDirectory(ctx, rootPath, residualPath, model.ProjectSummary{RepoBranch: "master"}, "")
			if err != nil {
				t.Fatalf("inspectResidualWorktreeDirectory() error = %v", err)
			}
			if inspection.Safe || !strings.Contains(inspection.Reason, test.wantReason) {
				t.Fatalf("inspection = %#v, want blocked reason containing %q", inspection, test.wantReason)
			}
			if _, err := os.Lstat(residualPath); err != nil {
				t.Fatalf("blocked residual path was changed: %v", err)
			}
		})
	}
}

func TestRemoveInspectedResidualWorktreeDirectoryRevalidatesEntries(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	rootPath, residualPath, _ := createVerifiedPartialWorktreeResidue(t)
	svc := &Service{}
	inspection, err := svc.inspectResidualWorktreeDirectory(ctx, rootPath, residualPath, model.ProjectSummary{RepoBranch: "master"}, "")
	if err != nil || !inspection.Safe {
		t.Fatalf("initial inspection = %#v, %v", inspection, err)
	}
	changedPath := filepath.Join(residualPath, "nested", "kept.txt")
	writeTestFile(t, changedPath, "changed after inspection\n", 0o644)

	err = removeInspectedResidualWorktreeDirectory(ctx, inspection, residualPath)
	if err == nil || !strings.Contains(err.Error(), "changed before deletion") {
		t.Fatalf("removeInspectedResidualWorktreeDirectory() error = %v, want changed-entry rejection", err)
	}
	if content, err := os.ReadFile(changedPath); err != nil || string(content) != "changed after inspection\n" {
		t.Fatalf("changed file = %q, %v; want preserved", content, err)
	}
}

func TestRemoveInspectedResidualWorktreeDirectoryDoesNotTraverseNewEntries(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	rootPath, residualPath, _ := createVerifiedPartialWorktreeResidue(t)
	svc := &Service{}
	inspection, err := svc.inspectResidualWorktreeDirectory(ctx, rootPath, residualPath, model.ProjectSummary{RepoBranch: "master"}, "")
	if err != nil || !inspection.Safe {
		t.Fatalf("initial inspection = %#v, %v", inspection, err)
	}
	newPath := filepath.Join(residualPath, "output", "appeared-after-inspection.txt")
	writeTestFile(t, newPath, "keep me\n", 0o644)

	err = removeInspectedResidualWorktreeDirectory(ctx, inspection, residualPath)
	if err == nil || !strings.Contains(err.Error(), "directory not empty") {
		t.Fatalf("removeInspectedResidualWorktreeDirectory() error = %v, want non-empty-directory rejection", err)
	}
	if content, err := os.ReadFile(newPath); err != nil || string(content) != "keep me\n" {
		t.Fatalf("new file = %q, %v; want preserved", content, err)
	}
}

func TestResidualWorktreeCleanupRejectsGitPointerOutsideExpectedRepository(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	rootPath, residualPath, _ := createVerifiedPartialWorktreeResidue(t)
	outsideTarget := filepath.Join(t.TempDir(), "worktrees", "other")
	writeTestFile(t, filepath.Join(residualPath, ".git"), "gitdir: "+outsideTarget+"\n", 0o644)
	svc := &Service{}

	kind, err := svc.residualWorktreeCleanupKind(ctx, rootPath, residualPath)
	if err != nil {
		t.Fatalf("residualWorktreeCleanupKind() error = %v", err)
	}
	if kind != ResidualWorktreeCleanupUnknown {
		t.Fatalf("residualWorktreeCleanupKind() = %q, want unknown", kind)
	}
	inspection, err := svc.inspectResidualWorktreeDirectory(ctx, rootPath, residualPath, model.ProjectSummary{RepoBranch: "master"}, "")
	if err != nil {
		t.Fatalf("inspectResidualWorktreeDirectory() error = %v", err)
	}
	if inspection.Safe || !strings.Contains(inspection.Reason, "stale worktree .git pointer") {
		t.Fatalf("inspection = %#v, want stale .git pointer rejection", inspection)
	}
}

func TestFinishSafeWorktreeRemovalAfterGitErrorClearsVerifiedPartialResidue(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	rootPath, residualPath, commit := createVerifiedPartialWorktreeResidue(t)
	removeErr := errors.New("git worktree remove failed")
	svc := &Service{
		gitWorktreeListReader: func(context.Context, string) ([]scanner.GitWorktree, error) {
			return []scanner.GitWorktree{{Path: rootPath}}, nil
		},
	}

	if err := svc.finishSafeWorktreeRemovalAfterGitError(ctx, rootPath, model.WorktreeKindLinked, residualPath, commit, removeErr); err != nil {
		t.Fatalf("finishSafeWorktreeRemovalAfterGitError() error = %v", err)
	}
	if _, err := os.Lstat(residualPath); !os.IsNotExist(err) {
		t.Fatalf("verified partial residue still exists: %v", err)
	}
}

func TestCleanupResidualWorktreeDirectoriesRemovesVerifiedPartialResidue(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	rootPath, residualPath, _ := createVerifiedPartialWorktreeResidue(t)
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	svc := New(config.Default(), st, events.NewBus(), nil)
	if _, err := svc.CreateOrAttachProject(ctx, CreateOrAttachProjectRequest{
		ParentPath: filepath.Dir(rootPath),
		Name:       filepath.Base(rootPath),
	}); err != nil {
		t.Fatalf("track root project: %v", err)
	}
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:                  residualPath,
		Name:                  filepath.Base(residualPath),
		Status:                model.StatusIdle,
		PresentOnDisk:         true,
		Forgotten:             true,
		InScope:               true,
		WorktreeRootPath:      rootPath,
		WorktreeKind:          model.WorktreeKindLinked,
		WorktreeInitialBranch: "master",
		RepoBranch:            "master",
		UpdatedAt:             time.Now(),
	}); err != nil {
		t.Fatalf("seed orphaned worktree: %v", err)
	}

	directories, err := svc.ListOrphanedWorktreeDirectories(ctx)
	if err != nil {
		t.Fatalf("ListOrphanedWorktreeDirectories() error = %v", err)
	}
	if directory := directories[residualPath]; directory.CleanupKind != ResidualWorktreeCleanupPartialGitDir || directory.InspectionError != "" {
		t.Fatalf("partial residual directory = %#v", directory)
	}
	result, err := svc.CleanupResidualWorktreeDirectories(ctx, rootPath)
	if err != nil {
		t.Fatalf("CleanupResidualWorktreeDirectories() error = %v", err)
	}
	if len(result.RemovedPaths) != 1 || result.RemovedPaths[0] != residualPath || len(result.KeptPaths) != 0 {
		t.Fatalf("cleanup result = %#v", result)
	}
	if _, err := os.Lstat(residualPath); !os.IsNotExist(err) {
		t.Fatalf("verified partial residue still exists: %v", err)
	}
}

func TestScanOncePreservesPartialRemovalAsOrphanedWorktree(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	rootPath, residualPath, _ := createVerifiedPartialWorktreeResidue(t)
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	activityAt := time.Now().Add(-10 * time.Minute).UTC().Truncate(time.Second)
	detector := staticDetector{
		activities: map[string]*model.DetectorProjectActivity{
			residualPath: fakeActivity(residualPath, "ses_partial_residual", activityAt),
		},
	}
	cfg := config.Default()
	cfg.IncludePaths = []string{filepath.Dir(rootPath)}
	svc := New(cfg, st, events.NewBus(), []detectors.Detector{detector})
	if _, err := svc.CreateOrAttachProject(ctx, CreateOrAttachProjectRequest{
		ParentPath: filepath.Dir(rootPath),
		Name:       filepath.Base(rootPath),
	}); err != nil {
		t.Fatalf("track root project: %v", err)
	}
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:                  residualPath,
		Name:                  filepath.Base(residualPath),
		Status:                model.StatusIdle,
		PresentOnDisk:         true,
		InScope:               true,
		WorktreeRootPath:      rootPath,
		WorktreeKind:          model.WorktreeKindLinked,
		WorktreeInitialBranch: "master",
		RepoBranch:            "master",
		UpdatedAt:             time.Now(),
	}); err != nil {
		t.Fatalf("seed partial worktree state: %v", err)
	}

	if _, err := svc.ScanOnce(ctx); err != nil {
		t.Fatalf("ScanOnce() error = %v", err)
	}
	detail, err := st.GetProjectDetail(ctx, residualPath, 5)
	if err != nil {
		t.Fatalf("GetProjectDetail(partial residual) error = %v", err)
	}
	if !detail.Summary.Forgotten || !detail.Summary.PresentOnDisk ||
		detail.Summary.WorktreeKind != model.WorktreeKindLinked ||
		detail.Summary.WorktreeRootPath != rootPath ||
		detail.Summary.RepoBranch != "master" {
		t.Fatalf("scanned partial residual = %#v", detail.Summary)
	}
	directories, err := svc.ListOrphanedWorktreeDirectories(ctx)
	if err != nil {
		t.Fatalf("ListOrphanedWorktreeDirectories() error = %v", err)
	}
	if directory := directories[residualPath]; directory.CleanupKind != ResidualWorktreeCleanupPartialGitDir {
		t.Fatalf("scanned partial residual directory = %#v", directory)
	}
}

func createVerifiedPartialWorktreeResidue(t *testing.T) (string, string, string) {
	t.Helper()
	parent := t.TempDir()
	rootPath := filepath.Join(parent, "repo")
	initGitRepo(t, rootPath)
	writeTestFile(t, filepath.Join(rootPath, "nested", "kept.txt"), "committed nested file\n", 0o644)
	writeTestFile(t, filepath.Join(rootPath, "script.sh"), "#!/bin/sh\nexit 0\n", 0o755)
	runGit(t, rootPath, "git", "add", "nested/kept.txt", "script.sh")
	runGit(t, rootPath, "git", "commit", "-m", "add partial cleanup fixtures")
	commit := strings.TrimSpace(gitOutput(t, rootPath, "git", "rev-parse", "HEAD"))

	residualPath := filepath.Join(parent, "repo--partial-removal")
	worktreesPath := strings.TrimSpace(gitOutput(t, rootPath, "git", "rev-parse", "--git-path", "worktrees"))
	if !filepath.IsAbs(worktreesPath) {
		worktreesPath = filepath.Join(rootPath, worktreesPath)
	}
	staleTarget := filepath.Join(filepath.Clean(worktreesPath), filepath.Base(residualPath))
	writeTestFile(t, filepath.Join(residualPath, ".git"), "gitdir: "+staleTarget+"\n", 0o644)
	writeTestFile(t, filepath.Join(residualPath, "README.md"), "hello\n", 0o644)
	writeTestFile(t, filepath.Join(residualPath, "nested", "kept.txt"), "committed nested file\n", 0o644)
	writeTestFile(t, filepath.Join(residualPath, "script.sh"), "#!/bin/sh\nexit 0\n", 0o755)
	writeTestFile(t, filepath.Join(residualPath, "nested", ".DS_Store"), "finder metadata", 0o644)
	writeTestFile(t, filepath.Join(residualPath, "nested", "deeper", ".DS_Store"), "finder metadata", 0o644)
	if err := os.MkdirAll(filepath.Join(residualPath, "output"), 0o755); err != nil {
		t.Fatalf("create empty residual directory: %v", err)
	}
	return rootPath, residualPath, commit
}

func writeTestFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create parent for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatalf("chmod %s: %v", path, err)
	}
}
