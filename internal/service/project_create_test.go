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

type scratchTaskTitleAssessorFunc func(context.Context, ScratchTaskTitleAssessmentInput) (ScratchTaskTitleAssessment, error)

func (f scratchTaskTitleAssessorFunc) AssessScratchTaskTitle(ctx context.Context, input ScratchTaskTitleAssessmentInput) (ScratchTaskTitleAssessment, error) {
	return f(ctx, input)
}

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
	if detail.Summary.AttentionScore != 80 {
		t.Fatalf("attention score = %d, want 80 for a newly created project", detail.Summary.AttentionScore)
	}
	if len(detail.Reasons) == 0 || detail.Reasons[0].Code != "new_project" || !strings.Contains(detail.Reasons[0].Text, "Recently added project") {
		t.Fatalf("attention reasons = %#v, want an explicit recently-added project reason", detail.Reasons)
	}
	if len(result.RecentParentPaths) == 0 || result.RecentParentPaths[0] != parent {
		t.Fatalf("recent parent paths = %v, want %q first", result.RecentParentPaths, parent)
	}
}

func TestCreateOrAttachProjectAssignsExplicitCategory(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	category, err := st.CreateProjectCategory(ctx, "Client")
	if err != nil {
		t.Fatalf("CreateProjectCategory() error = %v", err)
	}
	svc := New(config.Default(), st, events.NewBus(), nil)
	parent := t.TempDir()

	result, err := svc.CreateOrAttachProject(ctx, CreateOrAttachProjectRequest{
		ParentPath:       parent,
		Name:             "alpha",
		CategoryID:       category.ID,
		CategoryExplicit: true,
	})
	if err != nil {
		t.Fatalf("CreateOrAttachProject() error = %v", err)
	}

	summary, err := st.GetProjectSummary(ctx, result.ProjectPath, false)
	if err != nil {
		t.Fatalf("GetProjectSummary() error = %v", err)
	}
	if summary.CategoryID != category.ID || summary.CategoryName != "Client" {
		t.Fatalf("category = %q/%q, want %q/Client", summary.CategoryID, summary.CategoryName, category.ID)
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

func TestCreateOrAttachProjectExplicitMainClearsExistingCategory(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	category, err := st.CreateProjectCategory(ctx, "Client")
	if err != nil {
		t.Fatalf("CreateProjectCategory() error = %v", err)
	}
	parent := t.TempDir()
	projectPath := filepath.Join(parent, "existing")
	if err := os.MkdirAll(projectPath, 0o755); err != nil {
		t.Fatalf("mkdir existing project: %v", err)
	}
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:          projectPath,
		Name:          "existing",
		Status:        model.StatusIdle,
		PresentOnDisk: true,
		ManuallyAdded: true,
		InScope:       true,
		UpdatedAt:     time.Now().UTC(),
	}); err != nil {
		t.Fatalf("upsert project: %v", err)
	}
	if err := st.SetResourceCategory(ctx, model.CategoryResourceProject, projectPath, category.ID); err != nil {
		t.Fatalf("SetResourceCategory() error = %v", err)
	}

	svc := New(config.Default(), st, events.NewBus(), nil)
	if _, err := svc.CreateOrAttachProject(ctx, CreateOrAttachProjectRequest{
		ParentPath:       parent,
		Name:             "existing",
		CategoryExplicit: true,
	}); err != nil {
		t.Fatalf("CreateOrAttachProject() error = %v", err)
	}

	summary, err := st.GetProjectSummary(ctx, projectPath, false)
	if err != nil {
		t.Fatalf("GetProjectSummary() error = %v", err)
	}
	if summary.CategoryID != "" || summary.CategoryName != "" {
		t.Fatalf("category = %q/%q, want Main", summary.CategoryID, summary.CategoryName)
	}
}

func TestCreateOrAttachProjectPromotesExistingDiscoveredProjectWithoutSessions(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	parent := t.TempDir()
	projectPath := filepath.Join(parent, "portfolio")
	discoveredAt := time.Now().UTC().Add(-7 * 24 * time.Hour)
	if err := os.MkdirAll(filepath.Join(projectPath, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir existing git project: %v", err)
	}
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:           projectPath,
		Name:           "portfolio",
		Status:         model.StatusIdle,
		AttentionScore: 10,
		PresentOnDisk:  true,
		InScope:        true,
		CreatedAt:      discoveredAt,
		UpdatedAt:      discoveredAt,
	}); err != nil {
		t.Fatalf("upsert discovered project: %v", err)
	}

	svc := New(config.Default(), st, events.NewBus(), nil)
	result, err := svc.CreateOrAttachProject(ctx, CreateOrAttachProjectRequest{
		ParentPath: parent,
		Name:       "portfolio",
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
	if !detail.Summary.ManuallyAdded || !detail.Summary.InScope || detail.Summary.Archived || detail.Summary.Forgotten {
		t.Fatalf("expected existing discovered project to be promoted into the active manual list, got %#v", detail.Summary)
	}
	if !detail.Summary.CreatedAt.After(discoveredAt) {
		t.Fatalf("created at = %v, want promotion time after discovery time %v", detail.Summary.CreatedAt, discoveredAt)
	}
	if detail.Summary.AttentionScore <= 50 {
		t.Fatalf("attention score = %d, want promoted project above ordinary active work", detail.Summary.AttentionScore)
	}
	if len(detail.Reasons) == 0 || detail.Reasons[0].Code != "new_project" {
		t.Fatalf("attention reasons = %#v, want new_project after promotion", detail.Reasons)
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

func TestCreateScratchTaskAssignsExplicitCategory(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
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
	cfg.ScratchRoot = filepath.Join(t.TempDir(), "tasks")
	svc := New(cfg, st, events.NewBus(), nil)

	result, err := svc.CreateScratchTask(ctx, CreateScratchTaskRequest{
		Title:            "Answer Sarah about API docs",
		CategoryID:       category.ID,
		CategoryExplicit: true,
	})
	if err != nil {
		t.Fatalf("CreateScratchTask() error = %v", err)
	}

	summary, err := st.GetProjectSummary(ctx, result.TaskPath, false)
	if err != nil {
		t.Fatalf("GetProjectSummary() error = %v", err)
	}
	if summary.CategoryID != category.ID || summary.CategoryName != "Client" {
		t.Fatalf("category = %q/%q, want %q/Client", summary.CategoryID, summary.CategoryName, category.ID)
	}
}

func TestCreateScratchTaskUsesRequestAsInitialName(t *testing.T) {
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
		Request: "answer Sarah about API docs",
	})
	if err != nil {
		t.Fatalf("CreateScratchTask() error = %v", err)
	}
	if result.TaskName != "answer Sarah about API docs" {
		t.Fatalf("task name = %q, want request-derived name", result.TaskName)
	}
	if got := filepath.Base(result.TaskPath); !strings.Contains(got, "answer-sarah-about-api-docs") {
		t.Fatalf("task folder = %q, want request slug", got)
	}
	content, err := os.ReadFile(filepath.Join(result.TaskPath, scratchTaskMetadataFileName))
	if err != nil {
		t.Fatalf("read task metadata: %v", err)
	}
	if got := string(content); !strings.Contains(got, "Title-State: provisional") {
		t.Fatalf("TASK.md = %q, want provisional request-derived title state", got)
	}
}

func TestCreateScratchTaskIgnoresCollapsedPasteOnlyRequest(t *testing.T) {
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
		Request: "[2 lines pasted]",
	})
	if err != nil {
		t.Fatalf("CreateScratchTask() error = %v", err)
	}
	if !strings.HasPrefix(result.TaskName, defaultScratchTaskTitlePrefix+" ") {
		t.Fatalf("task name = %q, want temporary name", result.TaskName)
	}
	if strings.Contains(filepath.Base(result.TaskPath), "lines-pasted") {
		t.Fatalf("task folder = %q, should not use paste placeholder", filepath.Base(result.TaskPath))
	}
}

func TestCreateScratchTaskUsesTextAroundCollapsedPasteRequest(t *testing.T) {
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
		Request: "[2 lines pasted] summarize this config",
	})
	if err != nil {
		t.Fatalf("CreateScratchTask() error = %v", err)
	}
	if result.TaskName != "summarize this config" {
		t.Fatalf("task name = %q, want prompt text around paste placeholder", result.TaskName)
	}
	if got := filepath.Base(result.TaskPath); !strings.Contains(got, "summarize-this-config") || strings.Contains(got, "lines-pasted") {
		t.Fatalf("task folder = %q, want prompt slug without paste placeholder", got)
	}
}

func TestCreateScratchTaskUsesTemporaryNameWhenTitleAndRequestAreBlank(t *testing.T) {
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

	result, err := svc.CreateScratchTask(ctx, CreateScratchTaskRequest{})
	if err != nil {
		t.Fatalf("CreateScratchTask() error = %v", err)
	}
	if !strings.HasPrefix(result.TaskName, defaultScratchTaskTitlePrefix+" ") {
		t.Fatalf("task name = %q, want temporary name", result.TaskName)
	}
	metadataPath := filepath.Join(result.TaskPath, scratchTaskMetadataFileName)
	content, err := os.ReadFile(metadataPath)
	if err != nil {
		t.Fatalf("read task metadata: %v", err)
	}
	if got := string(content); !strings.Contains(got, "# "+result.TaskName) || !strings.Contains(got, "Kind: scratch_task") {
		t.Fatalf("TASK.md = %q, want temporary heading and scratch kind", got)
	}
}

func TestMaybeRenameScratchTaskFromPromptRenamesTemporaryName(t *testing.T) {
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
	svc.SetScratchTaskTitleAssessor(scratchTaskTitleAssessorFunc(func(_ context.Context, input ScratchTaskTitleAssessmentInput) (ScratchTaskTitleAssessment, error) {
		if input.LatestUserPrompt != "Fix API docs login" {
			t.Fatalf("latest prompt = %q, want submitted prompt", input.LatestUserPrompt)
		}
		return ScratchTaskTitleAssessment{
			CandidateTitle: "Fix API docs login",
			Quality:        scratchTaskTitleQualityHigh,
			Confidence:     0.92,
			Adopt:          true,
			KeepWatching:   false,
			Reason:         "specific implementation task",
			Model:          "unit-title-model",
		}, nil
	}))

	result, err := svc.CreateScratchTask(ctx, CreateScratchTaskRequest{})
	if err != nil {
		t.Fatalf("CreateScratchTask() error = %v", err)
	}
	renamed, err := svc.MaybeRenameScratchTaskFromPrompt(ctx, result.TaskPath, "Fix API docs login")
	if err != nil {
		t.Fatalf("MaybeRenameScratchTaskFromPrompt() error = %v", err)
	}
	if !renamed.Renamed || renamed.OldName != result.TaskName || renamed.TaskName != "Fix API docs login" {
		t.Fatalf("rename result = %#v, want temporary name replaced", renamed)
	}
	detail, err := st.GetProjectDetail(ctx, result.TaskPath, 5)
	if err != nil {
		t.Fatalf("GetProjectDetail() error = %v", err)
	}
	if detail.Summary.Name != "Fix API docs login" {
		t.Fatalf("stored name = %q, want prompt title", detail.Summary.Name)
	}
	content, err := os.ReadFile(filepath.Join(result.TaskPath, scratchTaskMetadataFileName))
	if err != nil {
		t.Fatalf("read task metadata: %v", err)
	}
	if got := string(content); !strings.Contains(got, "# Fix API docs login") {
		t.Fatalf("TASK.md = %q, want renamed heading", got)
	}
	if got := string(content); !strings.Contains(got, "Title-State: accepted") || !strings.Contains(got, "Title-Quality: high") {
		t.Fatalf("TASK.md = %q, want accepted high-quality title metadata", got)
	}
}

func TestMaybeRenameScratchTaskFromPromptKeepsWatchingAfterLowQualityPrompt(t *testing.T) {
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

	assessments := []ScratchTaskTitleAssessment{
		{
			Quality:      scratchTaskTitleQualityLow,
			Confidence:   0.24,
			Adopt:        false,
			KeepWatching: true,
			Reason:       "social opener without task intent",
			Model:        "unit-title-model",
		},
		{
			CandidateTitle: "Three.js Armored Core Prototype",
			Quality:        scratchTaskTitleQualityHigh,
			Confidence:     0.93,
			Adopt:          true,
			KeepWatching:   false,
			Reason:         "specific requested game prototype",
			Model:          "unit-title-model",
		},
	}
	call := 0
	svc.SetScratchTaskTitleAssessor(scratchTaskTitleAssessorFunc(func(_ context.Context, input ScratchTaskTitleAssessmentInput) (ScratchTaskTitleAssessment, error) {
		if call >= len(assessments) {
			t.Fatalf("unexpected title assessment call %d for prompt %q", call+1, input.LatestUserPrompt)
		}
		result := assessments[call]
		call++
		return result, nil
	}))

	result, err := svc.CreateScratchTask(ctx, CreateScratchTaskRequest{})
	if err != nil {
		t.Fatalf("CreateScratchTask() error = %v", err)
	}
	first, err := svc.MaybeRenameScratchTaskFromPrompt(ctx, result.TaskPath, "hi there !")
	if err != nil {
		t.Fatalf("MaybeRenameScratchTaskFromPrompt() greeting error = %v", err)
	}
	if first.Renamed {
		t.Fatalf("greeting rename result = %#v, want keep temporary title", first)
	}
	detail, err := st.GetProjectDetail(ctx, result.TaskPath, 5)
	if err != nil {
		t.Fatalf("GetProjectDetail() after greeting error = %v", err)
	}
	if detail.Summary.Name != result.TaskName {
		t.Fatalf("stored name after greeting = %q, want temporary %q", detail.Summary.Name, result.TaskName)
	}
	content, err := os.ReadFile(filepath.Join(result.TaskPath, scratchTaskMetadataFileName))
	if err != nil {
		t.Fatalf("read task metadata after greeting: %v", err)
	}
	if got := string(content); !strings.Contains(got, "Title-State: temporary") || !strings.Contains(got, "Title-Quality: low") {
		t.Fatalf("TASK.md after greeting = %q, want low-quality temporary title metadata", got)
	}

	second, err := svc.MaybeRenameScratchTaskFromPrompt(ctx, result.TaskPath, "can you make a very simple threejs game with the mechanics of the first armored core")
	if err != nil {
		t.Fatalf("MaybeRenameScratchTaskFromPrompt() task error = %v", err)
	}
	if !second.Renamed || second.TaskName != "Three.js Armored Core Prototype" {
		t.Fatalf("task rename result = %#v, want high-quality title", second)
	}
	detail, err = st.GetProjectDetail(ctx, result.TaskPath, 5)
	if err != nil {
		t.Fatalf("GetProjectDetail() after task prompt error = %v", err)
	}
	if detail.Summary.Name != "Three.js Armored Core Prototype" {
		t.Fatalf("stored name after task prompt = %q, want generated title", detail.Summary.Name)
	}
	if call != 2 {
		t.Fatalf("assessment calls = %d, want 2", call)
	}
}

func TestMaybeRenameScratchTaskFromPromptKeepsExplicitName(t *testing.T) {
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

	result, err := svc.CreateScratchTask(ctx, CreateScratchTaskRequest{Title: "Quick note"})
	if err != nil {
		t.Fatalf("CreateScratchTask() error = %v", err)
	}
	renamed, err := svc.MaybeRenameScratchTaskFromPrompt(ctx, result.TaskPath, "Fix API docs login")
	if err != nil {
		t.Fatalf("MaybeRenameScratchTaskFromPrompt() error = %v", err)
	}
	if renamed.Renamed {
		t.Fatalf("rename result = %#v, want explicit name preserved", renamed)
	}
	detail, err := st.GetProjectDetail(ctx, result.TaskPath, 5)
	if err != nil {
		t.Fatalf("GetProjectDetail() error = %v", err)
	}
	if detail.Summary.Name != "Quick note" {
		t.Fatalf("stored name = %q, want explicit title", detail.Summary.Name)
	}
}

func TestCreateScratchTaskCleansFolderWhenPersistenceFails(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	cfg := config.Default()
	cfg.ScratchRoot = filepath.Join(t.TempDir(), "tasks")
	svc := New(cfg, st, events.NewBus(), nil)

	if _, err := svc.CreateScratchTask(ctx, CreateScratchTaskRequest{Title: "Canceled before persistence"}); err == nil {
		t.Fatalf("CreateScratchTask() error = nil, want canceled persistence error")
	}
	entries, err := os.ReadDir(cfg.ScratchRoot)
	if err != nil {
		t.Fatalf("read scratch root: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("scratch root entries = %d, want cleanup after failed persistence: %#v", len(entries), entries)
	}
}

func TestScanOnceDiscoversScratchTaskFolderCreatedBeforePersistence(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	cfg := config.Default()
	cfg.IncludePaths = []string{t.TempDir()}
	cfg.ScratchRoot = filepath.Join(t.TempDir(), "tasks")
	svc := New(cfg, st, events.NewBus(), nil)

	taskPath := filepath.Join(cfg.ScratchRoot, "2026-05-18-pdf-of-alien-card")
	if err := os.MkdirAll(taskPath, 0o755); err != nil {
		t.Fatalf("mkdir scratch task: %v", err)
	}
	createdAt := time.Date(2026, 5, 18, 0, 0, 0, 0, time.UTC)
	if err := os.WriteFile(filepath.Join(taskPath, scratchTaskMetadataFileName), []byte(renderScratchTaskMetadata("pdf of alien card", createdAt)), 0o644); err != nil {
		t.Fatalf("write scratch metadata: %v", err)
	}

	if _, err := svc.ScanOnce(ctx); err != nil {
		t.Fatalf("ScanOnce() error = %v", err)
	}
	detail, err := st.GetProjectDetail(ctx, taskPath, 5)
	if err != nil {
		t.Fatalf("GetProjectDetail() error = %v", err)
	}
	if detail.Summary.Kind != model.ProjectKindScratchTask || detail.Summary.Name != "pdf of alien card" {
		t.Fatalf("discovered scratch task summary = %#v", detail.Summary)
	}
	if !detail.Summary.ManuallyAdded || !detail.Summary.InScope || !detail.Summary.PresentOnDisk {
		t.Fatalf("unexpected discovered scratch task flags: %#v", detail.Summary)
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

func TestArchiveScratchTaskMovesFolderAndHidesOriginalTask(t *testing.T) {
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

	result, err := svc.CreateScratchTask(ctx, CreateScratchTaskRequest{Title: "Archive me later"})
	if err != nil {
		t.Fatalf("CreateScratchTask() error = %v", err)
	}

	archivedPath, err := svc.ArchiveScratchTask(ctx, result.TaskPath)
	if err != nil {
		t.Fatalf("ArchiveScratchTask() error = %v", err)
	}
	if archivedPath == result.TaskPath {
		t.Fatalf("archived path = %q, want a new location", archivedPath)
	}
	if _, err := os.Stat(result.TaskPath); !os.IsNotExist(err) {
		t.Fatalf("original task path should be gone after archive, stat err = %v", err)
	}
	if _, err := os.Stat(archivedPath); err != nil {
		t.Fatalf("archived path should exist: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(archivedPath, scratchTaskMetadataFileName))
	if err != nil {
		t.Fatalf("read archived task metadata: %v", err)
	}
	if got := string(content); !strings.Contains(got, "Status: archived") {
		t.Fatalf("archived TASK.md = %q, want archived status", got)
	}

	visible, err := st.ListProjects(ctx, false)
	if err != nil {
		t.Fatalf("ListProjects() error = %v", err)
	}
	if len(visible) != 0 {
		t.Fatalf("expected archived task to disappear from visible projects, got %#v", visible)
	}

	detail, err := st.GetProjectDetail(ctx, result.TaskPath, 5)
	if err != nil {
		t.Fatalf("GetProjectDetail() error = %v", err)
	}
	if !detail.Summary.Forgotten || detail.Summary.PresentOnDisk {
		t.Fatalf("archived scratch task summary = %#v, want forgotten and missing", detail.Summary)
	}
}

func TestDeleteScratchTaskRemovesFolderAndHidesTask(t *testing.T) {
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

	result, err := svc.CreateScratchTask(ctx, CreateScratchTaskRequest{Title: "Delete me later"})
	if err != nil {
		t.Fatalf("CreateScratchTask() error = %v", err)
	}

	if err := svc.DeleteScratchTask(ctx, result.TaskPath); err != nil {
		t.Fatalf("DeleteScratchTask() error = %v", err)
	}
	if _, err := os.Stat(result.TaskPath); !os.IsNotExist(err) {
		t.Fatalf("deleted task path should be gone, stat err = %v", err)
	}

	visible, err := st.ListProjects(ctx, false)
	if err != nil {
		t.Fatalf("ListProjects() error = %v", err)
	}
	if len(visible) != 0 {
		t.Fatalf("expected deleted task to disappear from visible projects, got %#v", visible)
	}

	detail, err := st.GetProjectDetail(ctx, result.TaskPath, 5)
	if err != nil {
		t.Fatalf("GetProjectDetail() error = %v", err)
	}
	if !detail.Summary.Forgotten || detail.Summary.PresentOnDisk {
		t.Fatalf("deleted scratch task summary = %#v, want forgotten and missing", detail.Summary)
	}
}
