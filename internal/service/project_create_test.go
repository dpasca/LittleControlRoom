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
	"lcroom/internal/model"
	"lcroom/internal/store"
)

func TestCreateOrAttachProjectCreatesDirectoryAndGitRepo(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	svc := New(config.Default(), st, events.NewBus(), nil)
	parent := t.TempDir()

	result, err := svc.CreateOrAttachProject(ctx, CreateOrAttachProjectRequest{
		ParentPath:    parent,
		Name:          "alpha",
		CreateGitRepo: true,
	})
	if err != nil {
		t.Fatalf("CreateOrAttachProject() error = %v", err)
	}
	if result.Action != CreateOrAttachProjectCreated {
		t.Fatalf("action = %q, want %q", result.Action, CreateOrAttachProjectCreated)
	}
	if !result.GitRepoCreated {
		t.Fatalf("expected git repo to be created")
	}
	if result.ProjectPath != filepath.Join(parent, "alpha") {
		t.Fatalf("project path = %q, want %q", result.ProjectPath, filepath.Join(parent, "alpha"))
	}
	if _, err := os.Stat(filepath.Join(result.ProjectPath, ".git")); err != nil {
		t.Fatalf("expected .git directory: %v", err)
	}

	detail, err := st.GetProjectDetail(ctx, result.ProjectPath, 5)
	if err != nil {
		t.Fatalf("GetProjectDetail() error = %v", err)
	}
	if detail.Summary.Path != result.ProjectPath || detail.Summary.Name != "alpha" {
		t.Fatalf("unexpected stored project detail: %#v", detail.Summary)
	}
	if len(result.RecentParentPaths) == 0 || result.RecentParentPaths[0] != parent {
		t.Fatalf("recent parent paths = %v, want %q first", result.RecentParentPaths, parent)
	}
}

func TestCreateOrAttachProjectAddsExistingDirectoryWithoutInitializingGit(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	svc := New(config.Default(), st, events.NewBus(), nil)
	parent := t.TempDir()
	projectPath := filepath.Join(parent, "existing")
	if err := os.MkdirAll(projectPath, 0o755); err != nil {
		t.Fatalf("mkdir existing project: %v", err)
	}

	result, err := svc.CreateOrAttachProject(ctx, CreateOrAttachProjectRequest{
		ParentPath:    parent,
		Name:          "existing",
		CreateGitRepo: true,
	})
	if err != nil {
		t.Fatalf("CreateOrAttachProject() error = %v", err)
	}
	if result.Action != CreateOrAttachProjectAdded {
		t.Fatalf("action = %q, want %q", result.Action, CreateOrAttachProjectAdded)
	}
	if result.GitRepoCreated {
		t.Fatalf("existing directory should not be git-initialized by the add flow")
	}
	if _, err := os.Stat(filepath.Join(projectPath, ".git")); !os.IsNotExist(err) {
		t.Fatalf("expected no .git directory, stat err = %v", err)
	}
}

func TestCreateOrAttachProjectAddsQuotedExistingDirectoryAndDerivesNameFromPath(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	svc := New(config.Default(), st, events.NewBus(), nil)
	parent := filepath.Join(t.TempDir(), "Family Room", "Media")
	projectPath := filepath.Join(parent, "2026_03_mothers_farm")
	if err := os.MkdirAll(projectPath, 0o755); err != nil {
		t.Fatalf("mkdir existing project: %v", err)
	}

	result, err := svc.CreateOrAttachProject(ctx, CreateOrAttachProjectRequest{
		ParentPath:    "'" + projectPath + "'",
		CreateGitRepo: true,
	})
	if err != nil {
		t.Fatalf("CreateOrAttachProject() error = %v", err)
	}
	if result.Action != CreateOrAttachProjectAdded {
		t.Fatalf("action = %q, want %q", result.Action, CreateOrAttachProjectAdded)
	}
	if !result.NameDerivedFromPath {
		t.Fatalf("expected project name to be derived from path")
	}
	if result.ProjectName != "2026_03_mothers_farm" {
		t.Fatalf("project name = %q, want %q", result.ProjectName, "2026_03_mothers_farm")
	}
	if result.ParentPath != parent {
		t.Fatalf("parent path = %q, want %q", result.ParentPath, parent)
	}
	if result.ProjectPath != projectPath {
		t.Fatalf("project path = %q, want %q", result.ProjectPath, projectPath)
	}
}

func TestCreateOrAttachProjectRequiresNameForMissingPath(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	svc := New(config.Default(), st, events.NewBus(), nil)
	projectPath := filepath.Join(t.TempDir(), "missing-project")

	_, err = svc.CreateOrAttachProject(ctx, CreateOrAttachProjectRequest{
		ParentPath: projectPath,
	})
	if err == nil {
		t.Fatalf("expected missing-path request without a name to fail")
	}
	if got := err.Error(); got != "project name is required unless the path already exists" {
		t.Fatalf("error = %q, want %q", got, "project name is required unless the path already exists")
	}
}

func TestCreateOrAttachProjectDoesNotOverwriteTrackedSessions(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	parent := t.TempDir()
	projectPath := filepath.Join(parent, "tracked")
	if err := os.MkdirAll(projectPath, 0o755); err != nil {
		t.Fatalf("mkdir tracked project: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:           projectPath,
		Name:           "tracked",
		LastActivity:   now,
		Status:         model.StatusActive,
		AttentionScore: 17,
		PresentOnDisk:  true,
		InScope:        true,
		UpdatedAt:      now,
		Sessions: []model.SessionEvidence{{
			SessionID:   "ses_tracked",
			ProjectPath: projectPath,
			SessionFile: filepath.Join(projectPath, "session.jsonl"),
			Format:      "modern",
			StartedAt:   now.Add(-time.Minute),
			LastEventAt: now,
		}},
	}); err != nil {
		t.Fatalf("upsert tracked project: %v", err)
	}

	svc := New(config.Default(), st, events.NewBus(), nil)
	result, err := svc.CreateOrAttachProject(ctx, CreateOrAttachProjectRequest{
		ParentPath: parent,
		Name:       "tracked",
	})
	if err != nil {
		t.Fatalf("CreateOrAttachProject() error = %v", err)
	}
	if result.Action != CreateOrAttachProjectAlreadyKnown {
		t.Fatalf("action = %q, want %q", result.Action, CreateOrAttachProjectAlreadyKnown)
	}

	detail, err := st.GetProjectDetail(ctx, projectPath, 5)
	if err != nil {
		t.Fatalf("GetProjectDetail() error = %v", err)
	}
	if len(detail.Sessions) != 1 || detail.Sessions[0].SessionID != "codex:ses_tracked" {
		t.Fatalf("expected tracked sessions to remain intact, got %#v", detail.Sessions)
	}
}

func TestCreateOrAttachProjectRestoresForgottenProjectAsPresentOnDisk(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	parent := t.TempDir()
	projectPath := filepath.Join(parent, "restored")
	now := time.Now().UTC().Truncate(time.Second)
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:           projectPath,
		Name:           "restored",
		Status:         model.StatusIdle,
		AttentionScore: 0,
		Forgotten:      true,
		PresentOnDisk:  false,
		ManuallyAdded:  true,
		InScope:        true,
		UpdatedAt:      now,
		Sessions: []model.SessionEvidence{{
			SessionID:   "ses_restored",
			ProjectPath: projectPath,
			SessionFile: filepath.Join(projectPath, "session.jsonl"),
			Format:      "modern",
			StartedAt:   now.Add(-time.Minute),
			LastEventAt: now,
		}},
	}); err != nil {
		t.Fatalf("upsert forgotten project: %v", err)
	}
	if err := os.MkdirAll(projectPath, 0o755); err != nil {
		t.Fatalf("mkdir restored project: %v", err)
	}

	svc := New(config.Default(), st, events.NewBus(), nil)
	result, err := svc.CreateOrAttachProject(ctx, CreateOrAttachProjectRequest{
		ParentPath: parent,
		Name:       "restored",
	})
	if err != nil {
		t.Fatalf("CreateOrAttachProject() error = %v", err)
	}
	if result.Action != CreateOrAttachProjectAdded {
		t.Fatalf("action = %q, want %q", result.Action, CreateOrAttachProjectAdded)
	}

	detail, err := st.GetProjectDetail(ctx, projectPath, 5)
	if err != nil {
		t.Fatalf("GetProjectDetail() error = %v", err)
	}
	if !detail.Summary.PresentOnDisk || detail.Summary.Forgotten {
		t.Fatalf("expected forgotten project to be restored as present on disk, got %#v", detail.Summary)
	}
	if len(detail.Sessions) != 1 || detail.Sessions[0].SessionID != "codex:ses_restored" {
		t.Fatalf("expected sessions to survive restore, got %#v", detail.Sessions)
	}
}

func TestCreateScratchTaskCreatesMetadataAndPersistsKind(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	cfg := config.Default()
	cfg.ScratchRoot = filepath.Join(t.TempDir(), "tasks")
	svc := New(cfg, st, events.NewBus(), nil)

	result, err := svc.CreateScratchTask(ctx, CreateScratchTaskRequest{
		Title: "Answer Sarah about API docs",
	})
	if err != nil {
		t.Fatalf("CreateScratchTask() error = %v", err)
	}
	if result.TaskName != "Answer Sarah about API docs" {
		t.Fatalf("task name = %q, want title", result.TaskName)
	}
	metadataPath := filepath.Join(result.TaskPath, scratchTaskMetadataFileName)
	content, err := os.ReadFile(metadataPath)
	if err != nil {
		t.Fatalf("read task metadata: %v", err)
	}
	if got := string(content); !strings.Contains(got, "# Answer Sarah about API docs") || !strings.Contains(got, "Kind: scratch_task") {
		t.Fatalf("TASK.md = %q, want heading and scratch kind", got)
	}

	detail, err := st.GetProjectDetail(ctx, result.TaskPath, 5)
	if err != nil {
		t.Fatalf("GetProjectDetail() error = %v", err)
	}
	if detail.Summary.Kind != model.ProjectKindScratchTask {
		t.Fatalf("kind = %q, want %q", detail.Summary.Kind, model.ProjectKindScratchTask)
	}
	if detail.Summary.Name != result.TaskName {
		t.Fatalf("stored name = %q, want %q", detail.Summary.Name, result.TaskName)
	}
	if !detail.Summary.ManuallyAdded || !detail.Summary.InScope || !detail.Summary.PresentOnDisk {
		t.Fatalf("unexpected scratch task summary: %#v", detail.Summary)
	}
}

func TestScanOnceKeepsScratchTaskVisibleOutsideIncludePaths(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	includeRoot := t.TempDir()
	cfg := config.Default()
	cfg.IncludePaths = []string{includeRoot}
	cfg.ScratchRoot = filepath.Join(t.TempDir(), "tasks")
	svc := New(cfg, st, events.NewBus(), nil)

	result, err := svc.CreateScratchTask(ctx, CreateScratchTaskRequest{Title: "Quick note"})
	if err != nil {
		t.Fatalf("CreateScratchTask() error = %v", err)
	}
	if _, err := svc.ScanOnce(ctx); err != nil {
		t.Fatalf("ScanOnce() error = %v", err)
	}

	visible, err := st.ListProjects(ctx, false)
	if err != nil {
		t.Fatalf("ListProjects() error = %v", err)
	}
	if len(visible) != 1 {
		t.Fatalf("visible projects = %d, want 1 scratch task", len(visible))
	}
	if visible[0].Path != result.TaskPath || visible[0].Kind != model.ProjectKindScratchTask {
		t.Fatalf("visible scratch task = %#v, want path=%q kind=%q", visible[0], result.TaskPath, model.ProjectKindScratchTask)
	}
}
