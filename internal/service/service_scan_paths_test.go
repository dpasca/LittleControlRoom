package service

import (
	"context"
	"lcroom/internal/config"
	"lcroom/internal/detectors"
	"lcroom/internal/events"
	"lcroom/internal/model"
	"lcroom/internal/store"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestScanOnceClearsExpiredSnooze(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	projectPath := t.TempDir()
	now := time.Now().UTC().Truncate(time.Second)
	snoozedUntil := now.Add(-1 * time.Hour)

	state := model.ProjectState{
		Path:           projectPath,
		Name:           filepath.Base(projectPath),
		LastActivity:   now.Add(-2 * time.Hour),
		Status:         model.StatusIdle,
		AttentionScore: 0,
		InScope:        true,
		SnoozedUntil:   &snoozedUntil,
		UpdatedAt:      now,
	}
	if err := st.UpsertProjectState(ctx, state); err != nil {
		t.Fatalf("upsert project: %v", err)
	}

	detector := staticDetector{
		activities: map[string]*model.DetectorProjectActivity{
			projectPath: {
				ProjectPath:  projectPath,
				LastActivity: now.Add(-2 * time.Hour),
				Source:       "test",
			},
		},
	}

	cfg := config.Default()
	cfg.IncludePaths = nil
	svc := New(cfg, st, events.NewBus(), []detectors.Detector{detector})

	report, err := svc.ScanOnce(ctx)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(report.States) != 1 {
		t.Fatalf("expected 1 project, got %d", len(report.States))
	}

	detail, err := st.GetProjectDetail(ctx, projectPath, 10)
	if err != nil {
		t.Fatalf("get detail: %v", err)
	}
	if detail.Summary.SnoozedUntil != nil {
		t.Fatalf("expected snooze to be cleared, got snoozed_until=%v", detail.Summary.SnoozedUntil)
	}
}

func TestScanOnceReconcilesDuplicateSessionOwnershipAcrossProjects(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	root := t.TempDir()
	alphaPath := filepath.Join(root, "alpha")
	betaPath := filepath.Join(root, "beta")
	for _, path := range []string{alphaPath, betaPath} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", path, err)
		}
	}

	older := time.Now().UTC().Add(-10 * time.Minute).Truncate(time.Second)
	newer := older.Add(5 * time.Minute)
	dupID := "dup-session"

	detector := staticDetector{
		name: "test",
		activities: map[string]*model.DetectorProjectActivity{
			alphaPath: fakeActivity(alphaPath, dupID, older),
			betaPath:  fakeActivity(betaPath, dupID, newer),
		},
	}

	cfg := config.Default()
	cfg.IncludePaths = nil
	svc := New(cfg, st, events.NewBus(), []detectors.Detector{detector})

	if _, err := svc.ScanOnce(ctx); err != nil {
		t.Fatalf("scan: %v", err)
	}

	if _, err := st.GetProjectDetail(ctx, alphaPath, 10); err == nil {
		t.Fatalf("expected losing project %s to be dropped once its only session moved away", alphaPath)
	}

	betaDetail, err := st.GetProjectDetail(ctx, betaPath, 10)
	if err != nil {
		t.Fatalf("beta detail: %v", err)
	}
	if len(betaDetail.Sessions) != 1 {
		t.Fatalf("beta session count = %d, want 1", len(betaDetail.Sessions))
	}
	if betaDetail.Sessions[0].SessionID != "codex:"+dupID {
		t.Fatalf("beta session id = %q, want %q", betaDetail.Sessions[0].SessionID, "codex:"+dupID)
	}
	if betaDetail.Sessions[0].ProjectPath != betaPath {
		t.Fatalf("beta session project path = %q, want %q", betaDetail.Sessions[0].ProjectPath, betaPath)
	}
}

func TestScanOnceWithIncludePathsRunsDetectorsOnceAndKeepsRecentOutOfScopeActivity(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	includeRoot := t.TempDir()
	includedPath := filepath.Join(includeRoot, "included")
	outsideRoot := t.TempDir()
	recentOutsidePath := filepath.Join(outsideRoot, "recent-outside")
	staleOutsidePath := filepath.Join(outsideRoot, "stale-outside")
	for _, path := range []string{includedPath, recentOutsidePath, staleOutsidePath} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", path, err)
		}
	}

	now := time.Now().UTC().Truncate(time.Second)
	detector := &countingDetector{
		name: "test",
		activities: map[string]*model.DetectorProjectActivity{
			includedPath:      fakeActivity(includedPath, "included-session", now.Add(-72*time.Hour)),
			recentOutsidePath: fakeActivity(recentOutsidePath, "recent-outside-session", now.Add(-time.Hour)),
			staleOutsidePath:  fakeActivity(staleOutsidePath, "stale-outside-session", now.Add(-72*time.Hour)),
		},
	}

	cfg := config.Default()
	cfg.IncludePaths = []string{includeRoot}
	svc := New(cfg, st, events.NewBus(), []detectors.Detector{detector})
	svc.SetSessionClassifier(nil)
	svc.gitFingerprintReader = nil
	svc.gitRepoStatusReader = nil
	svc.gitWorktreeInfoReader = nil
	svc.gitWorktreeListReader = nil

	report, err := svc.ScanOnce(ctx)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if detector.calls != 1 {
		t.Fatalf("detector calls = %d, want 1", detector.calls)
	}
	if len(detector.scopes) != 1 || len(detector.scopes[0].IncludePaths) != 0 {
		t.Fatalf("detector scope = %#v, want one broad activity pass", detector.scopes)
	}

	statesByPath := map[string]model.ProjectState{}
	for _, state := range report.States {
		statesByPath[state.Path] = state
	}
	if _, ok := statesByPath[includedPath]; !ok {
		t.Fatalf("included activity missing from scan states: %#v", report.States)
	}
	if _, ok := statesByPath[recentOutsidePath]; !ok {
		t.Fatalf("recent out-of-scope activity missing from scan states: %#v", report.States)
	}
	if _, ok := statesByPath[staleOutsidePath]; ok {
		t.Fatalf("stale out-of-scope activity should be filtered from scan states: %#v", report.States)
	}
}

func TestScanOnceKeepsActiveSnooze(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	projectPath := t.TempDir()
	now := time.Now().UTC().Truncate(time.Second)
	snoozedUntil := now.Add(1 * time.Hour)

	state := model.ProjectState{
		Path:           projectPath,
		Name:           filepath.Base(projectPath),
		LastActivity:   now.Add(-2 * time.Hour),
		Status:         model.StatusIdle,
		AttentionScore: 0,
		InScope:        true,
		SnoozedUntil:   &snoozedUntil,
		UpdatedAt:      now,
	}
	if err := st.UpsertProjectState(ctx, state); err != nil {
		t.Fatalf("upsert project: %v", err)
	}

	detector := staticDetector{
		activities: map[string]*model.DetectorProjectActivity{
			projectPath: {
				ProjectPath:  projectPath,
				LastActivity: now.Add(-2 * time.Hour),
				Source:       "test",
			},
		},
	}

	cfg := config.Default()
	cfg.IncludePaths = nil
	svc := New(cfg, st, events.NewBus(), []detectors.Detector{detector})

	report, err := svc.ScanOnce(ctx)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(report.States) != 1 {
		t.Fatalf("expected 1 project, got %d", len(report.States))
	}

	detail, err := st.GetProjectDetail(ctx, projectPath, 10)
	if err != nil {
		t.Fatalf("get detail: %v", err)
	}
	if detail.Summary.SnoozedUntil == nil {
		t.Fatalf("expected snooze to remain active")
	}
	if !detail.Summary.SnoozedUntil.Equal(snoozedUntil) {
		t.Fatalf("snoozed_until = %v, want %v", detail.Summary.SnoozedUntil, snoozedUntil)
	}
}

func TestScanOncePropagatesToRootProjectFromLinkedWorktreeActivity(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	projectPath := filepath.Join(root, "repo")
	worktreePath := filepath.Join(root, "repo--feature")

	initGitRepo(t, projectPath)
	runGit(t, projectPath, "git", "worktree", "add", "-b", "feature", worktreePath)

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	today := time.Now().UTC().Add(-5 * time.Minute).Truncate(time.Second)

	// Only the linked worktree has recent activity; the root has none.
	detector := staticDetector{
		name: "test",
		activities: map[string]*model.DetectorProjectActivity{
			worktreePath: fakeActivity(worktreePath, "wt-session", today),
		},
	}

	cfg := config.Default()
	cfg.IncludePaths = nil
	svc := New(cfg, st, events.NewBus(), []detectors.Detector{detector})

	// First scan: discover both root and worktree.
	if _, err := svc.CreateOrAttachProject(ctx, CreateOrAttachProjectRequest{
		ParentPath: root, Name: "repo",
	}); err != nil {
		t.Fatalf("track root: %v", err)
	}
	if _, err := svc.CreateOrAttachProject(ctx, CreateOrAttachProjectRequest{
		ParentPath: root, Name: "repo--feature",
	}); err != nil {
		t.Fatalf("track worktree: %v", err)
	}

	report, err := svc.ScanOnce(ctx)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}

	// Root project should inherit the worktree's more-recent LastActivity.
	var rootState *model.ProjectState
	for i := range report.States {
		if report.States[i].Path == projectPath {
			rootState = &report.States[i]
			break
		}
	}
	if rootState == nil {
		t.Fatalf("root project not in scan report; states = %v", report.States)
	}
	if rootState.LastActivity.Before(today) {
		t.Fatalf("root LastActivity = %v, want >= %v (worktree activity should propagate)", rootState.LastActivity, today)
	}
}

func TestScanOnceCanonicalizesRepoSubdirectorySessionToGitTopLevel(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	projectPath := filepath.Join(root, "repo")
	runPath := filepath.Join(projectPath, "runs", "001-demo")
	initGitRepo(t, projectPath)
	if err := os.MkdirAll(runPath, 0o755); err != nil {
		t.Fatalf("mkdir run path: %v", err)
	}

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	activityAt := time.Now().UTC().Add(-5 * time.Minute).Truncate(time.Second)
	detector := staticDetector{
		name: "codex",
		activities: map[string]*model.DetectorProjectActivity{
			runPath: fakeActivity(runPath, "nested-session", activityAt),
		},
	}

	cfg := config.Default()
	cfg.IncludePaths = []string{root}
	svc := New(cfg, st, events.NewBus(), []detectors.Detector{detector})

	report, err := svc.ScanOnce(ctx)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(report.States) != 1 || report.States[0].Path != projectPath {
		t.Fatalf("scan states = %#v, want only root project %s", report.States, projectPath)
	}

	detail, err := st.GetProjectDetail(ctx, projectPath, 10)
	if err != nil {
		t.Fatalf("get root detail: %v", err)
	}
	if len(detail.Sessions) != 1 {
		t.Fatalf("root session count = %d, want 1", len(detail.Sessions))
	}
	if detail.Sessions[0].ProjectPath != projectPath {
		t.Fatalf("session project path = %q, want %q", detail.Sessions[0].ProjectPath, projectPath)
	}
	if detail.Sessions[0].DetectedProjectPath != runPath {
		t.Fatalf("session detected path = %q, want original cwd %q", detail.Sessions[0].DetectedProjectPath, runPath)
	}

	projects, err := st.ListProjects(ctx, false)
	if err != nil {
		t.Fatalf("list projects: %v", err)
	}
	if len(projects) != 1 || projects[0].Path != projectPath {
		t.Fatalf("visible projects = %#v, want only root project", projects)
	}
}

func TestScanOnceKeepsLinkedWorktreeSubdirectorySessionOnLinkedWorktree(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	projectPath := filepath.Join(root, "repo")
	worktreePath := filepath.Join(root, "repo--feature")
	runPath := filepath.Join(worktreePath, "runs", "001-demo")
	initGitRepo(t, projectPath)
	runGit(t, projectPath, "git", "worktree", "add", "-b", "feature", worktreePath)
	if err := os.MkdirAll(runPath, 0o755); err != nil {
		t.Fatalf("mkdir linked run path: %v", err)
	}

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	activityAt := time.Now().UTC().Add(-5 * time.Minute).Truncate(time.Second)
	detector := staticDetector{
		name: "codex",
		activities: map[string]*model.DetectorProjectActivity{
			runPath: fakeActivity(runPath, "linked-nested-session", activityAt),
		},
	}

	cfg := config.Default()
	cfg.IncludePaths = []string{root}
	svc := New(cfg, st, events.NewBus(), []detectors.Detector{detector})

	if _, err := svc.ScanOnce(ctx); err != nil {
		t.Fatalf("scan: %v", err)
	}

	detail, err := st.GetProjectDetail(ctx, worktreePath, 10)
	if err != nil {
		t.Fatalf("get linked worktree detail: %v", err)
	}
	if detail.Summary.WorktreeKind != model.WorktreeKindLinked {
		t.Fatalf("worktree kind = %q, want linked", detail.Summary.WorktreeKind)
	}
	if len(detail.Sessions) != 1 {
		t.Fatalf("linked session count = %d, want 1", len(detail.Sessions))
	}
	if detail.Sessions[0].ProjectPath != worktreePath {
		t.Fatalf("session project path = %q, want linked top-level %q", detail.Sessions[0].ProjectPath, worktreePath)
	}
	if detail.Sessions[0].DetectedProjectPath != runPath {
		t.Fatalf("session detected path = %q, want original cwd %q", detail.Sessions[0].DetectedProjectPath, runPath)
	}
}

func TestScanOnceForgetsExistingDerivedRepoSubdirectoryProject(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	projectPath := filepath.Join(root, "repo")
	runPath := filepath.Join(projectPath, "runs", "001-demo")
	initGitRepo(t, projectPath)
	if err := os.MkdirAll(runPath, 0o755); err != nil {
		t.Fatalf("mkdir run path: %v", err)
	}

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	activityAt := time.Now().UTC().Add(-10 * time.Minute).Truncate(time.Second)
	sessionID := "codex:existing-derived-session"
	sessionFile := filepath.Join(runPath, "session.jsonl")
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:             runPath,
		Name:             filepath.Base(runPath),
		Kind:             model.ProjectKindProject,
		LastActivity:     activityAt,
		Status:           model.StatusActive,
		AttentionScore:   77,
		PresentOnDisk:    true,
		WorktreeRootPath: projectPath,
		WorktreeKind:     model.WorktreeKindMain,
		Forgotten:        false,
		InScope:          true,
		Sessions: []model.SessionEvidence{{
			Source:              model.SessionSourceCodex,
			SessionID:           sessionID,
			RawSessionID:        "existing-derived-session",
			ProjectPath:         runPath,
			DetectedProjectPath: runPath,
			SessionFile:         sessionFile,
			Format:              "modern",
			SnapshotHash:        "old-hash",
			StartedAt:           activityAt.Add(-2 * time.Minute),
			LastEventAt:         activityAt,
		}},
		UpdatedAt: activityAt,
	}); err != nil {
		t.Fatalf("seed derived project: %v", err)
	}
	if _, err := st.QueueSessionClassification(ctx, model.SessionClassification{
		Source:            model.SessionSourceCodex,
		SessionID:         sessionID,
		RawSessionID:      "existing-derived-session",
		ProjectPath:       runPath,
		SessionFile:       sessionFile,
		SessionFormat:     "modern",
		SnapshotHash:      "old-hash",
		Status:            model.ClassificationPending,
		ClassifierVersion: "test",
		SourceUpdatedAt:   activityAt,
	}, 0); err != nil {
		t.Fatalf("seed classification: %v", err)
	}

	newActivityAt := time.Now().UTC().Add(-5 * time.Minute).Truncate(time.Second)
	detector := staticDetector{
		name: "codex",
		activities: map[string]*model.DetectorProjectActivity{
			runPath: {
				ProjectPath:  runPath,
				LastActivity: newActivityAt,
				Source:       "codex",
				Sessions: []model.SessionEvidence{{
					Source:              model.SessionSourceCodex,
					SessionID:           sessionID,
					RawSessionID:        "existing-derived-session",
					ProjectPath:         runPath,
					DetectedProjectPath: runPath,
					SessionFile:         sessionFile,
					Format:              "modern",
					SnapshotHash:        "new-hash",
					StartedAt:           newActivityAt.Add(-2 * time.Minute),
					LastEventAt:         newActivityAt,
				}},
			},
		},
	}

	cfg := config.Default()
	cfg.IncludePaths = []string{root}
	svc := New(cfg, st, events.NewBus(), []detectors.Detector{detector})

	if _, err := svc.ScanOnce(ctx); err != nil {
		t.Fatalf("scan: %v", err)
	}

	rootDetail, err := st.GetProjectDetail(ctx, projectPath, 10)
	if err != nil {
		t.Fatalf("get root detail: %v", err)
	}
	if len(rootDetail.Sessions) != 1 || rootDetail.Sessions[0].ProjectPath != projectPath || rootDetail.Sessions[0].DetectedProjectPath != runPath {
		t.Fatalf("root sessions were not canonicalized as expected: %#v", rootDetail.Sessions)
	}

	derivedDetail, err := st.GetProjectDetail(ctx, runPath, 10)
	if err != nil {
		t.Fatalf("get derived detail: %v", err)
	}
	if !derivedDetail.Summary.Forgotten || derivedDetail.Summary.InScope {
		t.Fatalf("derived project should be forgotten and out of scope, got %#v", derivedDetail.Summary)
	}
	if len(derivedDetail.Sessions) != 0 {
		t.Fatalf("derived project sessions = %#v, want none after repair", derivedDetail.Sessions)
	}

	classification, err := st.GetSessionClassification(ctx, sessionID)
	if err != nil {
		t.Fatalf("get classification: %v", err)
	}
	if classification.ProjectPath != projectPath {
		t.Fatalf("classification project path = %q, want canonical root %q", classification.ProjectPath, projectPath)
	}

	projects, err := st.ListProjects(ctx, false)
	if err != nil {
		t.Fatalf("list projects: %v", err)
	}
	if len(projects) != 1 || projects[0].Path != projectPath {
		t.Fatalf("visible projects = %#v, want only root project", projects)
	}
}

func createSuggestedTodoWorktreeForTest(t *testing.T, ctx context.Context, svc *Service, st *store.Store, projectPath, todoText, branchName, worktreeSuffix string) CreateTodoWorktreeResult {
	t.Helper()
	item, err := svc.AddTodo(ctx, projectPath, todoText)
	if err != nil {
		t.Fatalf("add todo: %v", err)
	}
	queueTodoWorktreeSuggestionForTest(t, ctx, st, item.ID)
	suggestion, err := st.ClaimNextQueuedTodoWorktreeSuggestion(ctx, 0, 0)
	if err != nil {
		t.Fatalf("claim todo worktree suggestion: %v", err)
	}
	suggestion.BranchName = branchName
	suggestion.WorktreeSuffix = worktreeSuffix
	suggestion.Kind = "feature"
	suggestion.Reason = "Creates a linked worktree for test coverage."
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
	return result
}

func initBareGitRepo(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir bare repo parent: %v", err)
	}
	runGit(t, filepath.Dir(path), "git", "init", "--bare", path)
}

func runGit(t *testing.T, dir string, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Little Control Room Test",
		"GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Little Control Room Test",
		"GIT_COMMITTER_EMAIL=test@example.com",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v failed: %v\n%s", name, args, err, string(out))
	}
}

func gitOutput(t *testing.T, dir string, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Little Control Room Test",
		"GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Little Control Room Test",
		"GIT_COMMITTER_EMAIL=test@example.com",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v failed: %v\n%s", name, args, err, string(out))
	}
	return string(out)
}
