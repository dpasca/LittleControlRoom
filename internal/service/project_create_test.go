package service

import (
	"context"
	"os"
	"path/filepath"
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
	if len(detail.Sessions) != 1 || detail.Sessions[0].SessionID != "ses_tracked" {
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
	if len(detail.Sessions) != 1 || detail.Sessions[0].SessionID != "ses_restored" {
		t.Fatalf("expected sessions to survive restore, got %#v", detail.Sessions)
	}
}
