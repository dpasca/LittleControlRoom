package service

import (
	"context"
	"errors"
	"lcroom/internal/aibackend"
	"lcroom/internal/appfs"
	"lcroom/internal/config"
	"lcroom/internal/detectors"
	"lcroom/internal/events"
	"lcroom/internal/model"
	"lcroom/internal/scanner"
	"lcroom/internal/sessionclassify"
	"lcroom/internal/store"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestScanWithOptionsTimesOutHungWorktreeReadersAndRepairsCodexSessionFile(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	oldTimeout := scanGitMetadataTimeout
	scanGitMetadataTimeout = 50 * time.Millisecond
	defer func() {
		scanGitMetadataTimeout = oldTimeout
	}()

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	root := t.TempDir()
	hungPath := filepath.Join(root, "aaa-hung")
	projectPath := filepath.Join(root, "bbb-target")
	for _, path := range []string{hungPath, projectPath} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", path, err)
		}
	}

	now := time.Date(2026, 4, 17, 15, 12, 53, 0, time.UTC)
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:           hungPath,
		Name:           "hung",
		LastActivity:   now.Add(-30 * time.Minute),
		Status:         model.StatusIdle,
		AttentionScore: 5,
		PresentOnDisk:  true,
		InScope:        true,
		CreatedAt:      now.Add(-2 * time.Hour),
		UpdatedAt:      now.Add(-30 * time.Minute),
	}); err != nil {
		t.Fatalf("seed hung project: %v", err)
	}

	sessionID := "019d9c00-851d-7033-8291-b0c6c7525753"
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:           projectPath,
		Name:           "target",
		LastActivity:   now,
		Status:         model.StatusIdle,
		AttentionScore: 5,
		PresentOnDisk:  true,
		InScope:        true,
		CreatedAt:      now.Add(-2 * time.Hour),
		UpdatedAt:      now,
		Sessions: []model.SessionEvidence{{
			Source:               model.SessionSourceCodex,
			SessionID:            sessionID,
			RawSessionID:         sessionID,
			ProjectPath:          projectPath,
			DetectedProjectPath:  projectPath,
			Format:               "modern",
			LastEventAt:          now,
			LatestTurnStateKnown: true,
			LatestTurnCompleted:  true,
		}},
	}); err != nil {
		t.Fatalf("seed target project: %v", err)
	}

	fixture := filepath.Clean(filepath.Join("..", "..", "testdata", "codex_footprint", "sessions", "2026", "03", "05", "rollout-modern.jsonl"))
	fixtureData, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	sessionFile := filepath.Join(root, "rollout-modern.jsonl")
	if err := os.WriteFile(sessionFile, fixtureData, 0o644); err != nil {
		t.Fatalf("write session file: %v", err)
	}

	detector := staticDetector{
		name: "codex",
		activities: map[string]*model.DetectorProjectActivity{
			projectPath: {
				ProjectPath:  projectPath,
				LastActivity: now,
				Sessions: []model.SessionEvidence{{
					Source:               model.SessionSourceCodex,
					SessionID:            sessionID,
					RawSessionID:         sessionID,
					ProjectPath:          projectPath,
					DetectedProjectPath:  projectPath,
					SessionFile:          sessionFile,
					Format:               "modern",
					StartedAt:            now.Add(-10 * time.Minute),
					LastEventAt:          now,
					LatestTurnStateKnown: true,
					LatestTurnCompleted:  true,
				}},
			},
		},
	}

	classifier := &recordingClassifier{}
	cfg := config.Default()
	cfg.IncludePaths = nil
	svc := New(cfg, st, events.NewBus(), []detectors.Detector{detector})
	svc.SetSessionClassifier(classifier)
	svc.gitFingerprintReader = nil
	svc.gitRepoStatusReader = nil
	svc.gitWorktreeInfoReader = func(ctx context.Context, path string) (scanner.GitWorktreeInfo, error) {
		if path != hungPath {
			return scanner.GitWorktreeInfo{}, errors.New("no worktree metadata")
		}
		<-ctx.Done()
		return scanner.GitWorktreeInfo{}, ctx.Err()
	}
	svc.gitWorktreeListReader = func(ctx context.Context, path string) ([]scanner.GitWorktree, error) {
		if path != hungPath {
			return nil, errors.New("no worktree list")
		}
		<-ctx.Done()
		return nil, ctx.Err()
	}

	report, err := svc.ScanWithOptions(ctx, ScanOptions{})
	if err != nil {
		t.Fatalf("ScanWithOptions() error = %v", err)
	}
	if report.QueuedClassifications != 1 {
		t.Fatalf("QueuedClassifications = %d, want 1", report.QueuedClassifications)
	}
	if classifier.normalCalls != 1 {
		t.Fatalf("QueueProject() calls = %d, want 1", classifier.normalCalls)
	}
	if len(classifier.lastState.Sessions) != 1 {
		t.Fatalf("queued state sessions = %#v, want one session", classifier.lastState.Sessions)
	}
	if got := classifier.lastState.Sessions[0].SessionFile; got != sessionFile {
		t.Fatalf("queued session file = %q, want %q", got, sessionFile)
	}
	if strings.TrimSpace(classifier.lastState.Sessions[0].SnapshotHash) == "" {
		t.Fatalf("expected queued session snapshot hash after scan")
	}

	detail, err := st.GetProjectDetail(ctx, projectPath, 20)
	if err != nil {
		t.Fatalf("get detail: %v", err)
	}
	if len(detail.Sessions) != 1 {
		t.Fatalf("stored sessions = %#v, want one session", detail.Sessions)
	}
	if got := detail.Sessions[0].SessionFile; got != sessionFile {
		t.Fatalf("stored session file = %q, want %q", got, sessionFile)
	}
	if strings.TrimSpace(detail.Sessions[0].SnapshotHash) == "" {
		t.Fatalf("expected stored session snapshot hash after scan")
	}
}

func TestReadScanGitMetadataUsesBoundedConcurrency(t *testing.T) {
	oldConcurrency := scanGitMetadataConcurrency
	scanGitMetadataConcurrency = 4
	defer func() {
		scanGitMetadataConcurrency = oldConcurrency
	}()

	root := t.TempDir()
	paths := []string{
		filepath.Join(root, "one"),
		filepath.Join(root, "two"),
		filepath.Join(root, "three"),
		filepath.Join(root, "four"),
	}
	for _, path := range paths {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", path, err)
		}
	}

	started := make(chan string, len(paths))
	release := make(chan struct{})
	done := make(chan []scanGitMetadataResult, 1)
	go func() {
		done <- readScanGitMetadata(context.Background(), paths, func(_ context.Context, path string) (scanner.GitFingerprint, error) {
			started <- path
			<-release
			return scanner.GitFingerprint{HeadHash: filepath.Base(path)}, nil
		}, nil, nil)
	}()

	for i := 0; i < len(paths); i++ {
		select {
		case <-started:
		case <-time.After(500 * time.Millisecond):
			close(release)
			t.Fatalf("metadata reader started %d of %d paths before blocking; want concurrent starts", i, len(paths))
		}
	}
	close(release)

	select {
	case results := <-done:
		if len(results) != len(paths) {
			t.Fatalf("results = %d, want %d", len(results), len(paths))
		}
		for _, result := range results {
			if !result.haveFingerprint {
				t.Fatalf("result for %s missing fingerprint: %#v", result.path, result)
			}
		}
	case <-time.After(time.Second):
		t.Fatal("metadata reads did not finish after release")
	}
}

func readyBackendStatus(backend config.AIBackend) aibackend.Status {
	return aibackend.Status{
		Backend: backend,
		Label:   backend.Label(),
		Ready:   true,
	}
}

func firstNonZeroBackend(values ...config.AIBackend) config.AIBackend {
	for _, value := range values {
		if value != config.AIBackendUnset {
			return value
		}
	}
	return config.AIBackendUnset
}

func TestScanWithOptionsForceRetriesFailedClassifications(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	projectPath := t.TempDir()
	now := time.Date(2026, 3, 6, 9, 0, 0, 0, time.UTC)
	detector := staticDetector{
		activities: map[string]*model.DetectorProjectActivity{
			projectPath: {
				ProjectPath:  projectPath,
				LastActivity: now,
				Sessions: []model.SessionEvidence{{
					SessionID:           "ses_force_scan",
					ProjectPath:         projectPath,
					DetectedProjectPath: projectPath,
					SessionFile:         filepath.Join(projectPath, "session.jsonl"),
					Format:              "modern",
					StartedAt:           now.Add(-time.Hour),
					LastEventAt:         now,
				}},
				Source: "test",
			},
		},
	}
	classifier := &recordingClassifier{}

	cfg := config.Default()
	cfg.IncludePaths = nil
	svc := New(cfg, st, events.NewBus(), []detectors.Detector{detector})
	svc.SetSessionClassifier(classifier)
	svc.gitFingerprintReader = nil
	svc.gitRepoStatusReader = nil

	if _, err := svc.ScanWithOptions(ctx, ScanOptions{ForceRetryFailedClassifications: true}); err != nil {
		t.Fatalf("scan with forced retry: %v", err)
	}
	if classifier.forcedCalls != 1 {
		t.Fatalf("forcedCalls = %d, want 1", classifier.forcedCalls)
	}
	if classifier.normalCalls != 0 {
		t.Fatalf("normalCalls = %d, want 0", classifier.normalCalls)
	}
}

func TestScanHidesManagedInternalProjects(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	now := time.Date(2026, 3, 19, 18, 0, 0, 0, time.UTC)
	dataDir := filepath.Join(t.TempDir(), ".little-control-room")
	helperPath := filepath.Join(appfs.InternalWorkspaceRoot(dataDir), "lcroom-codex-helper-live")
	if err := os.MkdirAll(helperPath, 0o700); err != nil {
		t.Fatalf("mkdir helper path: %v", err)
	}
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:           helperPath,
		Name:           filepath.Base(helperPath),
		LastActivity:   now.Add(-time.Minute),
		Status:         model.StatusActive,
		AttentionScore: 60,
		PresentOnDisk:  true,
		InScope:        true,
		UpdatedAt:      now,
	}); err != nil {
		t.Fatalf("seed leaked helper project: %v", err)
	}

	projectPath := t.TempDir()
	detector := staticDetector{
		activities: map[string]*model.DetectorProjectActivity{
			projectPath: {
				ProjectPath:  projectPath,
				LastActivity: now,
				Sessions: []model.SessionEvidence{{
					SessionID:           "ses_visible",
					ProjectPath:         projectPath,
					DetectedProjectPath: projectPath,
					SessionFile:         filepath.Join(projectPath, "session.jsonl"),
					Format:              "modern",
					StartedAt:           now.Add(-time.Hour),
					LastEventAt:         now,
				}},
				Source: "test",
			},
			helperPath: {
				ProjectPath:  helperPath,
				LastActivity: now,
				Sessions: []model.SessionEvidence{{
					SessionID:           "ses_hidden",
					ProjectPath:         helperPath,
					DetectedProjectPath: helperPath,
					SessionFile:         filepath.Join(helperPath, "session.jsonl"),
					Format:              "modern",
					StartedAt:           now.Add(-5 * time.Minute),
					LastEventAt:         now,
				}},
				Source: "test",
			},
		},
	}

	cfg := config.Default()
	cfg.IncludePaths = nil
	cfg.DataDir = dataDir
	svc := New(cfg, st, events.NewBus(), []detectors.Detector{detector})
	svc.gitFingerprintReader = nil
	svc.gitRepoStatusReader = nil

	if _, err := svc.ScanOnce(ctx); err != nil {
		t.Fatalf("scan once: %v", err)
	}

	projects, err := st.ListProjects(ctx, false)
	if err != nil {
		t.Fatalf("list current projects: %v", err)
	}
	if len(projects) != 1 || projects[0].Path != projectPath {
		t.Fatalf("visible projects = %#v, want only %q", projects, projectPath)
	}

	summaries, err := st.GetProjectSummaryMap(ctx)
	if err != nil {
		t.Fatalf("summary map: %v", err)
	}
	helper, ok := summaries[helperPath]
	if !ok {
		t.Fatalf("expected helper project row to remain queryable for cleanup assertions")
	}
	if helper.InScope {
		t.Fatalf("helper project should be out of scope after cleanup")
	}
	if !helper.Forgotten {
		t.Fatalf("helper project should be forgotten after cleanup")
	}
}

func TestRefreshProjectStatusUsesCompletedClassification(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	now := time.Now().UTC().Truncate(time.Second)
	state := model.ProjectState{
		Path:           "/tmp/archived-demo",
		Name:           "archived-demo",
		LastActivity:   now.Add(-72 * time.Hour),
		Status:         model.StatusPossiblyStuck,
		AttentionScore: 65,
		InScope:        true,
		UpdatedAt:      now,
		Sessions: []model.SessionEvidence{{
			SessionID:    "ses_1",
			ProjectPath:  "/tmp/archived-demo",
			SessionFile:  "/tmp/archived-demo/session.jsonl",
			Format:       "modern",
			SnapshotHash: "stable-session-hash",
			StartedAt:    now.Add(-73 * time.Hour),
			LastEventAt:  now.Add(-72 * time.Hour),
		}},
	}
	if err := st.UpsertProjectState(ctx, state); err != nil {
		t.Fatalf("upsert project: %v", err)
	}

	classification, ok := sessionclassify.BuildClassificationRequest(state)
	if !ok {
		t.Fatalf("expected build classification request to succeed")
	}
	if queued, err := st.QueueSessionClassification(ctx, classification, time.Minute); err != nil || !queued {
		t.Fatalf("queue classification: queued=%v err=%v", queued, err)
	}
	claimed, err := st.ClaimNextPendingSessionClassification(ctx, time.Minute)
	if err != nil {
		t.Fatalf("claim classification: %v", err)
	}
	claimed.Category = model.SessionCategoryCompleted
	claimed.Summary = "Work appears complete for now."
	claimed.Confidence = 0.93
	if err := st.CompleteSessionClassification(ctx, claimed); err != nil {
		t.Fatalf("complete classification: %v", err)
	}

	cfg := config.Default()
	cfg.ActiveThreshold = 20 * time.Minute
	cfg.StuckThreshold = 4 * time.Hour
	svc := New(cfg, st, events.NewBus(), nil)

	if err := svc.RefreshProjectStatus(ctx, state.Path); err != nil {
		t.Fatalf("refresh project status: %v", err)
	}

	detail, err := st.GetProjectDetail(ctx, state.Path, 10)
	if err != nil {
		t.Fatalf("get detail: %v", err)
	}
	if detail.Summary.Status != model.StatusIdle {
		t.Fatalf("status = %s, want idle", detail.Summary.Status)
	}
	if detail.Summary.AttentionScore != 12 {
		t.Fatalf("attention score = %d, want 12", detail.Summary.AttentionScore)
	}
	if len(detail.Reasons) != 1 || detail.Reasons[0].Code != "unread" {
		t.Fatalf("expected unread reason for stale unread completed work, got %#v", detail.Reasons)
	}
}

func TestRefreshProjectStatusKeepsRecentCompletedWorkFresh(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	now := time.Now().UTC().Truncate(time.Second)
	state := model.ProjectState{
		Path:           "/tmp/fresh-demo",
		Name:           "fresh-demo",
		LastActivity:   now.Add(-27 * time.Minute),
		Status:         model.StatusIdle,
		AttentionScore: 20,
		InScope:        true,
		UpdatedAt:      now,
		Sessions: []model.SessionEvidence{{
			SessionID:    "ses_recent",
			ProjectPath:  "/tmp/fresh-demo",
			SessionFile:  "/tmp/fresh-demo/session.jsonl",
			Format:       "modern",
			SnapshotHash: "recent-session-hash",
			StartedAt:    now.Add(-42 * time.Minute),
			LastEventAt:  now.Add(-27 * time.Minute),
		}},
	}
	if err := st.UpsertProjectState(ctx, state); err != nil {
		t.Fatalf("upsert project: %v", err)
	}

	classification, ok := sessionclassify.BuildClassificationRequest(state)
	if !ok {
		t.Fatalf("expected build classification request to succeed")
	}
	if queued, err := st.QueueSessionClassification(ctx, classification, time.Minute); err != nil || !queued {
		t.Fatalf("queue classification: queued=%v err=%v", queued, err)
	}
	claimed, err := st.ClaimNextPendingSessionClassification(ctx, time.Minute)
	if err != nil {
		t.Fatalf("claim classification: %v", err)
	}
	claimed.Category = model.SessionCategoryCompleted
	claimed.Summary = "Work appears complete for now."
	claimed.Confidence = 0.93
	if err := st.CompleteSessionClassification(ctx, claimed); err != nil {
		t.Fatalf("complete classification: %v", err)
	}

	cfg := config.Default()
	cfg.ActiveThreshold = 20 * time.Minute
	cfg.StuckThreshold = 4 * time.Hour
	svc := New(cfg, st, events.NewBus(), nil)

	if err := svc.RefreshProjectStatus(ctx, state.Path); err != nil {
		t.Fatalf("refresh project status: %v", err)
	}

	detail, err := st.GetProjectDetail(ctx, state.Path, 10)
	if err != nil {
		t.Fatalf("get detail: %v", err)
	}
	if detail.Summary.Status != model.StatusIdle {
		t.Fatalf("status = %s, want idle", detail.Summary.Status)
	}
	if detail.Summary.AttentionScore != 41 {
		t.Fatalf("attention score = %d, want 41", detail.Summary.AttentionScore)
	}
	if len(detail.Reasons) != 3 || detail.Reasons[0].Code != "session_completed" || detail.Reasons[1].Code != "recent_activity" || detail.Reasons[2].Code != "unread" {
		t.Fatalf("expected session_completed + recent_activity + unread reasons, got %#v", detail.Reasons)
	}
}

func TestTogglePinRefreshesAttentionScoreImmediately(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	now := time.Date(2026, 4, 4, 9, 0, 0, 0, time.UTC)
	projectPath := "/tmp/pin-demo"
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:           projectPath,
		Name:           "pin-demo",
		Status:         model.StatusIdle,
		AttentionScore: 10,
		InScope:        true,
		UpdatedAt:      now,
	}); err != nil {
		t.Fatalf("upsert project: %v", err)
	}

	svc := New(config.Default(), st, events.NewBus(), nil)
	if err := svc.TogglePin(ctx, projectPath); err != nil {
		t.Fatalf("TogglePin() error = %v", err)
	}

	summary, err := st.GetProjectSummary(ctx, projectPath, false)
	if err != nil {
		t.Fatalf("GetProjectSummary() error = %v", err)
	}
	if !summary.Pinned {
		t.Fatalf("Pinned = false, want true")
	}
	if summary.AttentionScore != 40 {
		t.Fatalf("AttentionScore = %d, want 40", summary.AttentionScore)
	}

	detail, err := st.GetProjectDetail(ctx, projectPath, 10)
	if err != nil {
		t.Fatalf("GetProjectDetail() error = %v", err)
	}
	found := false
	for _, reason := range detail.Reasons {
		if reason.Code == "pinned" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected pinned reason after TogglePin, got %#v", detail.Reasons)
	}
}

func TestSnoozeAndClearSnoozeRefreshAttentionImmediately(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	now := time.Date(2026, 4, 4, 9, 0, 0, 0, time.UTC)
	projectPath := "/tmp/snooze-demo"
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:           projectPath,
		Name:           "snooze-demo",
		Status:         model.StatusIdle,
		AttentionScore: 40,
		InScope:        true,
		Pinned:         true,
		UpdatedAt:      now,
	}); err != nil {
		t.Fatalf("upsert project: %v", err)
	}

	svc := New(config.Default(), st, events.NewBus(), nil)
	if err := svc.Snooze(ctx, projectPath, time.Hour); err != nil {
		t.Fatalf("Snooze() error = %v", err)
	}

	summary, err := st.GetProjectSummary(ctx, projectPath, false)
	if err != nil {
		t.Fatalf("GetProjectSummary() after snooze error = %v", err)
	}
	if summary.SnoozedUntil == nil {
		t.Fatalf("SnoozedUntil = nil, want active snooze")
	}
	if summary.AttentionScore != 0 {
		t.Fatalf("AttentionScore after snooze = %d, want 0", summary.AttentionScore)
	}

	detail, err := st.GetProjectDetail(ctx, projectPath, 10)
	if err != nil {
		t.Fatalf("GetProjectDetail() after snooze error = %v", err)
	}
	foundSnoozed := false
	for _, reason := range detail.Reasons {
		if reason.Code == "snoozed" {
			foundSnoozed = true
			break
		}
	}
	if !foundSnoozed {
		t.Fatalf("expected snoozed reason after Snooze, got %#v", detail.Reasons)
	}

	if err := svc.ClearSnooze(ctx, projectPath); err != nil {
		t.Fatalf("ClearSnooze() error = %v", err)
	}

	summary, err = st.GetProjectSummary(ctx, projectPath, false)
	if err != nil {
		t.Fatalf("GetProjectSummary() after clear error = %v", err)
	}
	if summary.SnoozedUntil != nil {
		t.Fatalf("SnoozedUntil after clear = %v, want nil", summary.SnoozedUntil)
	}
	if summary.AttentionScore != 40 {
		t.Fatalf("AttentionScore after clear = %d, want 40", summary.AttentionScore)
	}
}

func TestMarkProjectSessionReadUnreadRefreshesAttentionScoreImmediately(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	now := time.Now().UTC().Truncate(time.Second)
	projectPath := "/tmp/unread-demo"
	state := model.ProjectState{
		Path:           projectPath,
		Name:           "unread-demo",
		LastActivity:   now.Add(-27 * time.Minute),
		Status:         model.StatusIdle,
		AttentionScore: 20,
		InScope:        true,
		UpdatedAt:      now,
		Sessions: []model.SessionEvidence{{
			SessionID:            "ses_unread",
			ProjectPath:          projectPath,
			DetectedProjectPath:  projectPath,
			SessionFile:          filepath.Join(projectPath, "session.jsonl"),
			Format:               "modern",
			SnapshotHash:         "unread-session-hash",
			StartedAt:            now.Add(-42 * time.Minute),
			LastEventAt:          now.Add(-27 * time.Minute),
			LatestTurnStateKnown: true,
			LatestTurnCompleted:  true,
		}},
	}
	if err := st.UpsertProjectState(ctx, state); err != nil {
		t.Fatalf("upsert project: %v", err)
	}

	classification, ok := sessionclassify.BuildClassificationRequest(state)
	if !ok {
		t.Fatalf("expected build classification request to succeed")
	}
	if queued, err := st.QueueSessionClassification(ctx, classification, time.Minute); err != nil || !queued {
		t.Fatalf("queue classification: queued=%v err=%v", queued, err)
	}
	claimed, err := st.ClaimNextPendingSessionClassification(ctx, time.Minute)
	if err != nil {
		t.Fatalf("claim classification: %v", err)
	}
	claimed.Category = model.SessionCategoryCompleted
	claimed.Summary = "Work appears complete for now."
	claimed.Confidence = 0.93
	if err := st.CompleteSessionClassification(ctx, claimed); err != nil {
		t.Fatalf("complete classification: %v", err)
	}

	cfg := config.Default()
	cfg.ActiveThreshold = 20 * time.Minute
	cfg.StuckThreshold = 4 * time.Hour
	svc := New(cfg, st, events.NewBus(), nil)

	if err := svc.RefreshProjectStatus(ctx, state.Path); err != nil {
		t.Fatalf("refresh project status: %v", err)
	}

	summary, err := st.GetProjectSummary(ctx, state.Path, false)
	if err != nil {
		t.Fatalf("GetProjectSummary() error = %v", err)
	}
	if summary.AttentionScore != 41 {
		t.Fatalf("AttentionScore = %d, want 41 for unread completed work", summary.AttentionScore)
	}

	if err := svc.MarkProjectSessionSeen(ctx, state.Path, time.Now().UTC()); err != nil {
		t.Fatalf("MarkProjectSessionSeen() error = %v", err)
	}
	summary, err = st.GetProjectSummary(ctx, state.Path, false)
	if err != nil {
		t.Fatalf("GetProjectSummary() after read error = %v", err)
	}
	if summary.AttentionScore != 29 {
		t.Fatalf("AttentionScore after read = %d, want 29", summary.AttentionScore)
	}
	if summary.LastSessionSeenAt.IsZero() {
		t.Fatalf("LastSessionSeenAt = zero, want populated after read")
	}

	if err := svc.MarkProjectSessionUnread(ctx, state.Path); err != nil {
		t.Fatalf("MarkProjectSessionUnread() error = %v", err)
	}
	summary, err = st.GetProjectSummary(ctx, state.Path, false)
	if err != nil {
		t.Fatalf("GetProjectSummary() after unread error = %v", err)
	}
	if summary.AttentionScore != 41 {
		t.Fatalf("AttentionScore after unread = %d, want 41", summary.AttentionScore)
	}
	if !summary.LastSessionSeenAt.IsZero() {
		t.Fatalf("LastSessionSeenAt = %v, want zero after unread", summary.LastSessionSeenAt)
	}

	detail, err := st.GetProjectDetail(ctx, state.Path, 10)
	if err != nil {
		t.Fatalf("GetProjectDetail() error = %v", err)
	}
	found := false
	for _, reason := range detail.Reasons {
		if reason.Code == "unread" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected unread reason after MarkProjectSessionUnread, got %#v", detail.Reasons)
	}
}

func TestScanOnceReusesLatestOpenCodeSnapshotHashWhenSessionIsUnchanged(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	projectPath := filepath.Join(t.TempDir(), "demo")
	if err := os.MkdirAll(projectPath, 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}
	sessionID := "ses_reuse_hash"
	lastEventAt := time.Date(2026, 3, 13, 2, 0, 0, 0, time.UTC)

	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:           projectPath,
		Name:           "demo",
		Status:         model.StatusIdle,
		AttentionScore: 0,
		PresentOnDisk:  true,
		InScope:        true,
		UpdatedAt:      lastEventAt,
		Sessions: []model.SessionEvidence{{
			SessionID:    sessionID,
			ProjectPath:  projectPath,
			SessionFile:  filepath.Join(t.TempDir(), "missing-opencode.db") + "#session:" + sessionID,
			Format:       "opencode_db",
			SnapshotHash: "stable-opencode-hash",
			StartedAt:    lastEventAt.Add(-5 * time.Minute),
			LastEventAt:  lastEventAt,
		}},
	}); err != nil {
		t.Fatalf("seed prior project state: %v", err)
	}

	detector := staticDetector{
		activities: map[string]*model.DetectorProjectActivity{
			projectPath: {
				ProjectPath:  projectPath,
				LastActivity: lastEventAt,
				Source:       "opencode",
				Sessions: []model.SessionEvidence{{
					SessionID:   sessionID,
					ProjectPath: projectPath,
					SessionFile: filepath.Join(t.TempDir(), "missing-opencode.db") + "#session:" + sessionID,
					Format:      "opencode_db",
					StartedAt:   lastEventAt.Add(-5 * time.Minute),
					LastEventAt: lastEventAt,
				}},
			},
		},
	}

	cfg := config.Default()
	cfg.IncludePaths = []string{projectPath}
	svc := New(cfg, st, events.NewBus(), []detectors.Detector{detector})
	svc.gitFingerprintReader = nil
	svc.gitRepoStatusReader = nil

	report, err := svc.ScanOnce(ctx)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(report.States) != 1 {
		t.Fatalf("expected one scanned project, got %#v", report.States)
	}
	if got := report.States[0].Sessions[0].SnapshotHash; got != "stable-opencode-hash" {
		t.Fatalf("snapshot hash = %q, want reused stable hash", got)
	}
}

func TestLatestSessionClassificationUsesPersistedSessionSnapshotHash(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	now := time.Date(2026, 3, 6, 10, 0, 0, 0, time.UTC)
	state := model.ProjectState{
		Path:           "/tmp/hash-stable",
		Name:           "hash-stable",
		Status:         model.StatusIdle,
		AttentionScore: 15,
		InScope:        true,
		UpdatedAt:      now,
		Sessions: []model.SessionEvidence{{
			SessionID:    "ses_hashy",
			ProjectPath:  "/tmp/hash-stable",
			SessionFile:  "/tmp/hash-stable/opencode.db#session:ses_hashy",
			Format:       "opencode_db",
			SnapshotHash: "stable-transcript-hash",
			LastEventAt:  now,
			ErrorCount:   0,
			StartedAt:    now.Add(-5 * time.Minute),
		}},
	}
	if err := st.UpsertProjectState(ctx, state); err != nil {
		t.Fatalf("upsert project: %v", err)
	}

	if queued, err := st.QueueSessionClassification(ctx, model.SessionClassification{
		SessionID:         "ses_hashy",
		ProjectPath:       state.Path,
		SessionFile:       state.Sessions[0].SessionFile,
		SessionFormat:     state.Sessions[0].Format,
		SnapshotHash:      "stable-transcript-hash",
		Model:             "gpt-5-mini",
		ClassifierVersion: "v1",
		SourceUpdatedAt:   now,
	}, time.Minute); err != nil || !queued {
		t.Fatalf("queue classification: queued=%v err=%v", queued, err)
	}
	claimed, err := st.ClaimNextPendingSessionClassification(ctx, time.Minute)
	if err != nil {
		t.Fatalf("claim classification: %v", err)
	}
	claimed.Category = model.SessionCategoryCompleted
	claimed.Summary = "Nothing new was added."
	claimed.Confidence = 0.88
	if err := st.CompleteSessionClassification(ctx, claimed); err != nil {
		t.Fatalf("complete classification: %v", err)
	}

	svc := New(config.Default(), st, events.NewBus(), nil)
	known, category := svc.latestSessionClassification(ctx, state.Path, []model.SessionEvidence{{
		SessionID:    "ses_hashy",
		ProjectPath:  state.Path,
		SessionFile:  state.Sessions[0].SessionFile,
		Format:       state.Sessions[0].Format,
		SnapshotHash: "stable-transcript-hash",
		LastEventAt:  now.Add(20 * time.Minute),
	}}, now.Add(20*time.Minute))
	if !known {
		t.Fatalf("expected classification to remain current for timestamp-only updates")
	}
	if category != model.SessionCategoryCompleted {
		t.Fatalf("category = %s, want completed", category)
	}
}

func TestLatestSessionClassificationTreatsStaleInProgressTurnAsBlocked(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	now := time.Date(2026, 3, 29, 8, 0, 0, 0, time.UTC)
	state := model.ProjectState{
		Path:           "/tmp/stalled-session",
		Name:           "stalled-session",
		Status:         model.StatusPossiblyStuck,
		AttentionScore: 32,
		InScope:        true,
		UpdatedAt:      now,
		Sessions: []model.SessionEvidence{{
			SessionID:            "ses_stalled",
			ProjectPath:          "/tmp/stalled-session",
			SessionFile:          "/tmp/stalled-session/rollout.jsonl",
			Format:               "modern",
			SnapshotHash:         "stable-stalled-hash",
			LastEventAt:          now.Add(-65 * time.Minute),
			StartedAt:            now.Add(-2 * time.Hour),
			LatestTurnStateKnown: true,
			LatestTurnCompleted:  false,
		}},
	}
	if err := st.UpsertProjectState(ctx, state); err != nil {
		t.Fatalf("upsert project: %v", err)
	}

	if queued, err := st.QueueSessionClassification(ctx, model.SessionClassification{
		SessionID:         "ses_stalled",
		ProjectPath:       state.Path,
		SessionFile:       state.Sessions[0].SessionFile,
		SessionFormat:     state.Sessions[0].Format,
		SnapshotHash:      "stable-stalled-hash",
		Model:             "gpt-5-mini",
		ClassifierVersion: "v1",
		SourceUpdatedAt:   state.Sessions[0].LastEventAt,
	}, time.Minute); err != nil || !queued {
		t.Fatalf("queue classification: queued=%v err=%v", queued, err)
	}
	claimed, err := st.ClaimNextPendingSessionClassification(ctx, time.Minute)
	if err != nil {
		t.Fatalf("claim classification: %v", err)
	}
	claimed.Category = model.SessionCategoryInProgress
	claimed.Summary = "Checking the latest tool outputs."
	claimed.Confidence = 0.76
	if err := st.CompleteSessionClassification(ctx, claimed); err != nil {
		t.Fatalf("complete classification: %v", err)
	}

	cfg := config.Default()
	cfg.StuckThreshold = 30 * time.Minute
	svc := New(cfg, st, events.NewBus(), nil)
	known, category := svc.latestSessionClassification(ctx, state.Path, state.Sessions, now)
	if !known {
		t.Fatalf("expected classification to remain known")
	}
	if category != model.SessionCategoryBlocked {
		t.Fatalf("category = %s, want blocked", category)
	}
}

func TestScanOncePersistsLatestSessionSnapshotHash(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	projectPath := filepath.Join(root, "hash-scan")
	initGitRepo(t, projectPath)

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	fixture := filepath.Clean(filepath.Join("..", "..", "testdata", "codex_footprint", "sessions", "2026", "03", "05", "rollout-modern.jsonl"))
	detector := &fakeDetector{
		activities: map[string]*model.DetectorProjectActivity{
			projectPath: {
				ProjectPath:  projectPath,
				LastActivity: time.Date(2026, 3, 6, 1, 0, 0, 0, time.UTC),
				Source:       "codex",
				Sessions: []model.SessionEvidence{{
					SessionID:   "ses_hash_scan",
					ProjectPath: projectPath,
					SessionFile: fixture,
					Format:      "modern",
					StartedAt:   time.Date(2026, 3, 6, 0, 45, 0, 0, time.UTC),
					LastEventAt: time.Date(2026, 3, 6, 1, 0, 0, 0, time.UTC),
				}},
			},
		},
	}

	cfg := config.Default()
	cfg.IncludePaths = []string{root}
	svc := New(cfg, st, events.NewBus(), []detectors.Detector{detector})

	report, err := svc.ScanOnce(ctx)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(report.States) != 1 {
		t.Fatalf("expected one scanned project, got %#v", report.States)
	}
	if report.States[0].Sessions[0].SnapshotHash == "" {
		t.Fatalf("expected in-memory latest session snapshot hash")
	}

	detail, err := st.GetProjectDetail(ctx, projectPath, 10)
	if err != nil {
		t.Fatalf("get detail: %v", err)
	}
	if len(detail.Sessions) != 1 || detail.Sessions[0].SnapshotHash == "" {
		t.Fatalf("expected stored latest session snapshot hash, got %#v", detail.Sessions)
	}
}

func TestScanOnceRecoversLatestSessionTurnStateFromTranscript(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	projectPath := filepath.Join(root, "turn-scan")
	initGitRepo(t, projectPath)

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	fixture := filepath.Clean(filepath.Join("..", "..", "testdata", "codex_footprint", "sessions", "2026", "03", "05", "rollout-modern.jsonl"))
	detector := &fakeDetector{
		activities: map[string]*model.DetectorProjectActivity{
			projectPath: {
				ProjectPath:  projectPath,
				LastActivity: time.Date(2026, 3, 6, 1, 0, 0, 0, time.UTC),
				Source:       "codex",
				Sessions: []model.SessionEvidence{{
					SessionID:   "ses_turn_scan",
					ProjectPath: projectPath,
					SessionFile: fixture,
					Format:      "modern",
					StartedAt:   time.Date(2026, 3, 6, 0, 45, 0, 0, time.UTC),
					LastEventAt: time.Date(2026, 3, 6, 1, 0, 0, 0, time.UTC),
				}},
			},
		},
	}

	cfg := config.Default()
	cfg.IncludePaths = []string{root}
	svc := New(cfg, st, events.NewBus(), []detectors.Detector{detector})

	report, err := svc.ScanOnce(ctx)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(report.States) != 1 || len(report.States[0].Sessions) != 1 {
		t.Fatalf("unexpected scan report: %#v", report.States)
	}
	session := report.States[0].Sessions[0]
	if !session.LatestTurnStateKnown || !session.LatestTurnCompleted {
		t.Fatalf("expected recovered in-memory turn state, got known=%v completed=%v", session.LatestTurnStateKnown, session.LatestTurnCompleted)
	}

	detail, err := st.GetProjectDetail(ctx, projectPath, 10)
	if err != nil {
		t.Fatalf("get detail: %v", err)
	}
	if len(detail.Sessions) != 1 {
		t.Fatalf("expected stored session, got %#v", detail.Sessions)
	}
	if !detail.Sessions[0].LatestTurnStateKnown || !detail.Sessions[0].LatestTurnCompleted {
		t.Fatalf("expected stored recovered turn state, got known=%v completed=%v", detail.Sessions[0].LatestTurnStateKnown, detail.Sessions[0].LatestTurnCompleted)
	}
}

func TestScanOnceRecomputesLatestSessionSnapshotHashWhenTurnStateChanges(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	projectPath := filepath.Join(root, "turn-hash-refresh")
	initGitRepo(t, projectPath)

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	fixture := filepath.Clean(filepath.Join("..", "..", "testdata", "codex_footprint", "sessions", "2026", "03", "05", "rollout-modern.jsonl"))
	lastEventAt := time.Date(2026, 3, 6, 1, 0, 0, 0, time.UTC)
	staleHash := "stale-no-turn-state-hash"
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:           projectPath,
		Name:           filepath.Base(projectPath),
		LastActivity:   lastEventAt,
		Status:         model.StatusIdle,
		AttentionScore: 20,
		PresentOnDisk:  true,
		InScope:        true,
		UpdatedAt:      lastEventAt,
		Sessions: []model.SessionEvidence{{
			SessionID:    "ses_turn_hash_refresh",
			ProjectPath:  projectPath,
			SessionFile:  fixture,
			Format:       "modern",
			SnapshotHash: staleHash,
			StartedAt:    lastEventAt.Add(-5 * time.Minute),
			LastEventAt:  lastEventAt,
		}},
	}); err != nil {
		t.Fatalf("seed project state: %v", err)
	}

	detector := &fakeDetector{
		activities: map[string]*model.DetectorProjectActivity{
			projectPath: {
				ProjectPath:  projectPath,
				LastActivity: lastEventAt,
				Source:       "codex",
				Sessions: []model.SessionEvidence{{
					SessionID:   "ses_turn_hash_refresh",
					ProjectPath: projectPath,
					SessionFile: fixture,
					Format:      "modern",
					StartedAt:   lastEventAt.Add(-5 * time.Minute),
					LastEventAt: lastEventAt,
				}},
			},
		},
	}

	cfg := config.Default()
	cfg.IncludePaths = []string{root}
	svc := New(cfg, st, events.NewBus(), []detectors.Detector{detector})

	report, err := svc.ScanOnce(ctx)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(report.States) != 1 || len(report.States[0].Sessions) != 1 {
		t.Fatalf("unexpected scan report: %#v", report.States)
	}
	session := report.States[0].Sessions[0]
	if session.SnapshotHash == "" {
		t.Fatalf("expected recomputed snapshot hash")
	}
	if session.SnapshotHash == staleHash {
		t.Fatalf("snapshot hash = %q, want a refreshed hash after turn-state recovery", session.SnapshotHash)
	}
}

func TestScanOnceRecomputesLatestSessionSnapshotHashWhenGitStatusChanges(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	projectPath := filepath.Join(root, "git-hash-refresh")
	initGitRepo(t, projectPath)

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	fixture := filepath.Clean(filepath.Join("..", "..", "testdata", "codex_footprint", "sessions", "2026", "03", "05", "rollout-modern.jsonl"))
	lastEventAt := time.Date(2026, 3, 6, 1, 0, 0, 0, time.UTC)
	staleHash := "clean-repo-session-hash"
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:           projectPath,
		Name:           filepath.Base(projectPath),
		LastActivity:   lastEventAt,
		Status:         model.StatusIdle,
		AttentionScore: 20,
		PresentOnDisk:  true,
		InScope:        true,
		UpdatedAt:      lastEventAt,
		Sessions: []model.SessionEvidence{{
			SessionID:            "ses_git_hash_refresh",
			ProjectPath:          projectPath,
			SessionFile:          fixture,
			Format:               "modern",
			SnapshotHash:         staleHash,
			StartedAt:            lastEventAt.Add(-5 * time.Minute),
			LastEventAt:          lastEventAt,
			LatestTurnStateKnown: true,
			LatestTurnCompleted:  true,
		}},
	}); err != nil {
		t.Fatalf("seed project state: %v", err)
	}

	detector := &fakeDetector{
		activities: map[string]*model.DetectorProjectActivity{
			projectPath: {
				ProjectPath:  projectPath,
				LastActivity: lastEventAt,
				Source:       "codex",
				Sessions: []model.SessionEvidence{{
					SessionID:            "ses_git_hash_refresh",
					ProjectPath:          projectPath,
					SessionFile:          fixture,
					Format:               "modern",
					StartedAt:            lastEventAt.Add(-5 * time.Minute),
					LastEventAt:          lastEventAt,
					LatestTurnStateKnown: true,
					LatestTurnCompleted:  true,
				}},
			},
		},
	}

	if err := os.WriteFile(filepath.Join(projectPath, "scratch.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatalf("write dirty file: %v", err)
	}

	cfg := config.Default()
	cfg.IncludePaths = []string{root}
	svc := New(cfg, st, events.NewBus(), []detectors.Detector{detector})

	report, err := svc.ScanOnce(ctx)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(report.States) != 1 || len(report.States[0].Sessions) != 1 {
		t.Fatalf("unexpected scan report: %#v", report.States)
	}
	if !report.States[0].RepoDirty {
		t.Fatalf("expected repo to be marked dirty")
	}
	session := report.States[0].Sessions[0]
	if session.SnapshotHash == "" {
		t.Fatalf("expected recomputed snapshot hash")
	}
	if session.SnapshotHash == staleHash {
		t.Fatalf("snapshot hash = %q, want a refreshed hash after git status changed", session.SnapshotHash)
	}
}

func TestScanOnceReusesStoredLatestTurnStateWhenSessionIsUnchanged(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	projectPath := filepath.Join(root, "turn-reuse")
	initGitRepo(t, projectPath)

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	lastEventAt := time.Date(2026, 3, 6, 1, 0, 0, 0, time.UTC)
	startedAt := lastEventAt.Add(-5 * time.Minute)
	latestTurnStartedAt := lastEventAt.Add(-90 * time.Second)
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:           projectPath,
		Name:           filepath.Base(projectPath),
		LastActivity:   lastEventAt,
		Status:         model.StatusIdle,
		AttentionScore: 20,
		PresentOnDisk:  true,
		InScope:        true,
		UpdatedAt:      lastEventAt,
		Sessions: []model.SessionEvidence{{
			SessionID:            "ses_turn_reuse",
			ProjectPath:          projectPath,
			Format:               "modern",
			StartedAt:            startedAt,
			LastEventAt:          lastEventAt,
			LatestTurnStartedAt:  latestTurnStartedAt,
			LatestTurnStateKnown: true,
			LatestTurnCompleted:  false,
		}},
	}); err != nil {
		t.Fatalf("seed project state: %v", err)
	}

	detector := &fakeDetector{
		activities: map[string]*model.DetectorProjectActivity{
			projectPath: {
				ProjectPath:  projectPath,
				LastActivity: lastEventAt,
				Source:       "codex",
				Sessions: []model.SessionEvidence{{
					SessionID:   "ses_turn_reuse",
					ProjectPath: projectPath,
					Format:      "modern",
					StartedAt:   startedAt,
					LastEventAt: lastEventAt,
				}},
			},
		},
	}

	cfg := config.Default()
	cfg.IncludePaths = []string{root}
	svc := New(cfg, st, events.NewBus(), []detectors.Detector{detector})

	report, err := svc.ScanOnce(ctx)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(report.States) != 1 || len(report.States[0].Sessions) != 1 {
		t.Fatalf("unexpected scan report: %#v", report.States)
	}
	session := report.States[0].Sessions[0]
	if !session.LatestTurnStateKnown || session.LatestTurnCompleted {
		t.Fatalf("expected reused latest turn state, got known=%v completed=%v", session.LatestTurnStateKnown, session.LatestTurnCompleted)
	}
	if !session.LatestTurnStartedAt.Equal(latestTurnStartedAt) {
		t.Fatalf("latest turn started at = %s, want %s", session.LatestTurnStartedAt, latestTurnStartedAt)
	}
}

func TestScanOnceAutoMovesProjectAndCanonicalizesOldPathActivity(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	oldPath := filepath.Join(root, "old-project")
	newPath := filepath.Join(root, "new-project")
	initGitRepo(t, oldPath)

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	firstActivityAt := time.Now().Add(-20 * time.Minute).UTC().Truncate(time.Second)
	detector := &fakeDetector{
		activities: map[string]*model.DetectorProjectActivity{
			oldPath: fakeActivity(oldPath, "ses_move_initial", firstActivityAt),
		},
	}
	cfg := config.Default()
	cfg.IncludePaths = []string{root}

	svc := New(cfg, st, events.NewBus(), []detectors.Detector{detector})
	firstReport, err := svc.ScanOnce(ctx)
	if err != nil {
		t.Fatalf("first scan: %v", err)
	}
	if len(firstReport.States) != 1 || firstReport.States[0].Path != oldPath {
		t.Fatalf("unexpected first scan states: %#v", firstReport.States)
	}
	if err := st.SetPinned(ctx, oldPath, true); err != nil {
		t.Fatalf("set pinned: %v", err)
	}
	if err := os.Rename(oldPath, newPath); err != nil {
		t.Fatalf("rename repo: %v", err)
	}

	activityAt := time.Now().Add(-10 * time.Minute).UTC().Truncate(time.Second)
	detector.activities = map[string]*model.DetectorProjectActivity{
		oldPath: {
			ProjectPath:  oldPath,
			LastActivity: activityAt,
			Source:       "codex",
			Sessions: []model.SessionEvidence{{
				SessionID:           "ses_move_auto",
				ProjectPath:         oldPath,
				DetectedProjectPath: oldPath,
				SessionFile:         filepath.Join(root, "session.jsonl"),
				Format:              "modern",
				StartedAt:           activityAt.Add(-5 * time.Minute),
				LastEventAt:         activityAt,
			}},
		},
	}

	secondReport, err := svc.ScanOnce(ctx)
	if err != nil {
		t.Fatalf("second scan: %v", err)
	}
	if len(secondReport.States) != 1 || secondReport.States[0].Path != newPath {
		t.Fatalf("unexpected second scan states: %#v", secondReport.States)
	}
	if secondReport.States[0].MovedFromPath != oldPath {
		t.Fatalf("moved_from_path = %s, want %s", secondReport.States[0].MovedFromPath, oldPath)
	}
	if secondReport.States[0].LastActivity.Unix() != activityAt.Unix() {
		t.Fatalf("last activity = %s, want %s", secondReport.States[0].LastActivity, activityAt)
	}
	if !secondReport.States[0].Pinned {
		t.Fatalf("expected pin to survive move, got %#v", secondReport.States[0])
	}

	aliases, err := st.GetPathAliases(ctx)
	if err != nil {
		t.Fatalf("get path aliases: %v", err)
	}
	alias, ok := aliases[oldPath]
	if !ok || alias.NewPath != newPath {
		t.Fatalf("expected alias %s -> %s, got %#v", oldPath, newPath, aliases)
	}

	if _, err := st.GetProjectDetail(ctx, oldPath, 5); err == nil {
		t.Fatalf("expected old project path to be absent after auto-move")
	}
	detail, err := st.GetProjectDetail(ctx, newPath, 10)
	if err != nil {
		t.Fatalf("get moved detail: %v", err)
	}
	if detail.Summary.MovedFromPath != oldPath {
		t.Fatalf("detail moved_from_path = %s, want %s", detail.Summary.MovedFromPath, oldPath)
	}
	if len(detail.Sessions) != 1 || detail.Sessions[0].ProjectPath != newPath {
		t.Fatalf("expected canonicalized session path, got %#v", detail.Sessions)
	}
	if detail.Sessions[0].DetectedProjectPath != oldPath {
		t.Fatalf("expected detected path to preserve old location, got %#v", detail.Sessions)
	}

	foundMoveEvent := false
	for _, event := range detail.RecentEvents {
		if event.Type == string(events.ProjectMoved) {
			foundMoveEvent = true
			break
		}
	}
	if !foundMoveEvent {
		t.Fatalf("expected project_moved event in detail, got %#v", detail.RecentEvents)
	}

	newActivityAt := activityAt.Add(5 * time.Minute)
	detector.activities = map[string]*model.DetectorProjectActivity{
		newPath: {
			ProjectPath:  newPath,
			LastActivity: newActivityAt,
			Source:       "codex",
			Sessions: []model.SessionEvidence{{
				SessionID:           "ses_move_auto_new",
				ProjectPath:         newPath,
				DetectedProjectPath: newPath,
				SessionFile:         filepath.Join(root, "session-new.jsonl"),
				Format:              "modern",
				StartedAt:           newActivityAt.Add(-2 * time.Minute),
				LastEventAt:         newActivityAt,
			}},
		},
	}

	thirdReport, err := svc.ScanOnce(ctx)
	if err != nil {
		t.Fatalf("third scan: %v", err)
	}
	if len(thirdReport.States) != 1 || thirdReport.States[0].Path != newPath {
		t.Fatalf("unexpected third scan states: %#v", thirdReport.States)
	}
	if len(thirdReport.States[0].Sessions) != 1 || thirdReport.States[0].Sessions[0].DetectedProjectPath != newPath {
		t.Fatalf("expected latest session to come from new path, got %#v", thirdReport.States[0].Sessions)
	}

	projects, err := st.ListProjects(ctx, false)
	if err != nil {
		t.Fatalf("list projects after new-path activity: %v", err)
	}
	if len(projects) != 1 {
		t.Fatalf("expected 1 project after new-path activity, got %d", len(projects))
	}
	if projects[0].LatestSessionDetectedProjectPath != newPath {
		t.Fatalf("latest session detected path = %s, want %s", projects[0].LatestSessionDetectedProjectPath, newPath)
	}
}

func TestScanOnceConsolidatesStaleMovedFromDuplicate(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	oldPath := filepath.Join(root, "LittleControlRoom--codex-session-compaction-stuck")
	newPath := filepath.Join(root, "lcroom-codex-permissions-compare")
	initGitRepo(t, newPath)

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	now := time.Now().UTC().Truncate(time.Second)
	oldSessionAt := now.Add(-20 * time.Minute)
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:           oldPath,
		Name:           filepath.Base(oldPath),
		Kind:           model.ProjectKindProject,
		LastActivity:   oldSessionAt,
		Status:         model.StatusPossiblyStuck,
		AttentionScore: 60,
		PresentOnDisk:  false,
		InScope:        true,
		Sessions: []model.SessionEvidence{{
			SessionID:           "ses_stale_moved_from",
			ProjectPath:         oldPath,
			DetectedProjectPath: oldPath,
			SessionFile:         filepath.Join(root, "stale-session.jsonl"),
			Format:              "modern",
			StartedAt:           oldSessionAt.Add(-5 * time.Minute),
			LastEventAt:         oldSessionAt,
		}},
		CreatedAt: oldSessionAt,
		UpdatedAt: oldSessionAt,
	}); err != nil {
		t.Fatalf("upsert old stale project: %v", err)
	}
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:             newPath,
		Name:             filepath.Base(newPath),
		Kind:             model.ProjectKindProject,
		LastActivity:     now.Add(-10 * time.Minute),
		Status:           model.StatusIdle,
		PresentOnDisk:    true,
		WorktreeRootPath: root,
		WorktreeKind:     model.WorktreeKindLinked,
		RepoBranch:       "(detached)",
		RepoDirty:        true,
		InScope:          true,
		MovedFromPath:    oldPath,
		MovedAt:          now.Add(-15 * time.Minute),
		CreatedAt:        now.Add(-15 * time.Minute),
		UpdatedAt:        now.Add(-15 * time.Minute),
	}); err != nil {
		t.Fatalf("upsert moved target project: %v", err)
	}

	activityAt := now.Add(-5 * time.Minute)
	detector := &fakeDetector{
		activities: map[string]*model.DetectorProjectActivity{
			oldPath: fakeActivity(oldPath, "ses_detected_at_old_path", activityAt),
		},
	}
	cfg := config.Default()
	cfg.IncludePaths = []string{root}
	svc := New(cfg, st, events.NewBus(), []detectors.Detector{detector})

	report, err := svc.ScanOnce(ctx)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	var targetState *model.ProjectState
	for i := range report.States {
		if report.States[i].Path == oldPath {
			t.Fatalf("old duplicate path should not be scanned after consolidation: %#v", report.States)
		}
		if report.States[i].Path == newPath {
			targetState = &report.States[i]
		}
	}
	if targetState == nil {
		t.Fatalf("target project missing after consolidation: %#v", report.States)
	}
	if targetState.MovedFromPath != oldPath {
		t.Fatalf("moved_from_path = %s, want %s", targetState.MovedFromPath, oldPath)
	}

	if _, err := st.GetProjectDetail(ctx, oldPath, 5); err == nil {
		t.Fatalf("expected old duplicate project to be absent after consolidation")
	}
	detail, err := st.GetProjectDetail(ctx, newPath, 10)
	if err != nil {
		t.Fatalf("get consolidated detail: %v", err)
	}
	if len(detail.Sessions) == 0 {
		t.Fatalf("expected sessions to move to target project")
	}
	for _, session := range detail.Sessions {
		if session.ProjectPath != newPath {
			t.Fatalf("session project path = %s, want %s: %#v", session.ProjectPath, newPath, detail.Sessions)
		}
	}
	aliases, err := st.GetPathAliases(ctx)
	if err != nil {
		t.Fatalf("get aliases: %v", err)
	}
	if alias, ok := aliases[oldPath]; !ok || alias.NewPath != newPath {
		t.Fatalf("expected alias %s -> %s, got %#v", oldPath, newPath, aliases)
	}
}

func TestScanOnceForgottenMissingProjectStaysHiddenUntilRediscovered(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	projectPath := filepath.Join(root, "orphaned_repo")
	initGitRepo(t, projectPath)

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	initialActivityAt := time.Now().Add(-20 * time.Minute).UTC().Truncate(time.Second)
	detector := &fakeDetector{
		activities: map[string]*model.DetectorProjectActivity{
			projectPath: fakeActivity(projectPath, "ses_orphan_initial", initialActivityAt),
		},
	}
	cfg := config.Default()
	cfg.IncludePaths = []string{root}

	svc := New(cfg, st, events.NewBus(), []detectors.Detector{detector})
	firstReport, err := svc.ScanOnce(ctx)
	if err != nil {
		t.Fatalf("first scan: %v", err)
	}
	if len(firstReport.States) != 1 || firstReport.States[0].Path != projectPath {
		t.Fatalf("unexpected first scan states: %#v", firstReport.States)
	}

	if err := os.RemoveAll(projectPath); err != nil {
		t.Fatalf("remove project path: %v", err)
	}
	if err := st.SetForgotten(ctx, projectPath, true); err != nil {
		t.Fatalf("set forgotten: %v", err)
	}

	activityAt := time.Now().Add(-15 * time.Minute).UTC().Truncate(time.Second)
	detector.activities = map[string]*model.DetectorProjectActivity{
		projectPath: {
			ProjectPath:  projectPath,
			LastActivity: activityAt,
			Source:       "codex",
			Sessions: []model.SessionEvidence{{
				SessionID:           "ses_orphan",
				ProjectPath:         projectPath,
				DetectedProjectPath: projectPath,
				SessionFile:         filepath.Join(root, "orphan-session.jsonl"),
				Format:              "modern",
				StartedAt:           activityAt.Add(-2 * time.Minute),
				LastEventAt:         activityAt,
			}},
		},
	}

	secondReport, err := svc.ScanOnce(ctx)
	if err != nil {
		t.Fatalf("second scan: %v", err)
	}
	if len(secondReport.States) != 0 {
		t.Fatalf("expected forgotten missing project to stay hidden, got %#v", secondReport.States)
	}

	visibleProjects, err := st.ListProjects(ctx, false)
	if err != nil {
		t.Fatalf("list visible projects: %v", err)
	}
	if len(visibleProjects) != 0 {
		t.Fatalf("expected forgotten project to be hidden from list, got %#v", visibleProjects)
	}

	initGitRepo(t, projectPath)
	detector.activities = nil

	thirdReport, err := svc.ScanOnce(ctx)
	if err != nil {
		t.Fatalf("third scan: %v", err)
	}
	if len(thirdReport.States) != 1 || thirdReport.States[0].Path != projectPath {
		t.Fatalf("expected rediscovered project to return, got %#v", thirdReport.States)
	}
	if thirdReport.States[0].Forgotten {
		t.Fatalf("expected rediscovered project to auto-clear forgotten flag")
	}
	if !thirdReport.States[0].PresentOnDisk {
		t.Fatalf("expected rediscovered project to be marked present on disk")
	}

	visibleProjects, err = st.ListProjects(ctx, false)
	if err != nil {
		t.Fatalf("list visible projects after rediscovery: %v", err)
	}
	if len(visibleProjects) != 1 || visibleProjects[0].Path != projectPath {
		t.Fatalf("expected rediscovered project in visible list, got %#v", visibleProjects)
	}
	if visibleProjects[0].Forgotten {
		t.Fatalf("expected visible project forgotten flag to be cleared")
	}
}

func TestArchiveProjectMovesProjectOutOfCurrentList(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	projectPath := filepath.Join(t.TempDir(), "active-project")
	now := time.Now().UTC()
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:          projectPath,
		Name:          "active-project",
		Status:        model.StatusIdle,
		PresentOnDisk: true,
		InScope:       true,
		UpdatedAt:     now,
	}); err != nil {
		t.Fatalf("seed project: %v", err)
	}

	svc := New(config.Default(), st, events.NewBus(), nil)
	if err := svc.ArchiveProject(ctx, projectPath); err != nil {
		t.Fatalf("ArchiveProject() error = %v", err)
	}

	current, err := st.ListProjects(ctx, false)
	if err != nil {
		t.Fatalf("ListProjects(current) error = %v", err)
	}
	if len(current) != 0 {
		t.Fatalf("archived project should be hidden from current list, got %#v", current)
	}

	all, err := st.ListProjects(ctx, true)
	if err != nil {
		t.Fatalf("ListProjects(all) error = %v", err)
	}
	if len(all) != 1 || !all[0].Archived {
		t.Fatalf("historical list should include archived project, got %#v", all)
	}

	if err := svc.UnarchiveProject(ctx, projectPath); err != nil {
		t.Fatalf("UnarchiveProject() error = %v", err)
	}
	current, err = st.ListProjects(ctx, false)
	if err != nil {
		t.Fatalf("ListProjects(current after unarchive) error = %v", err)
	}
	if len(current) != 1 || current[0].Archived {
		t.Fatalf("unarchived project should return to current list, got %#v", current)
	}
}

func TestArchiveProjectsMovesBatchOutOfCurrentList(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	root := t.TempDir()
	projectOne := filepath.Join(root, "quickgame_01")
	projectTwo := filepath.Join(root, "quickgame_02")
	keep := filepath.Join(root, "keep")
	now := time.Now().UTC()
	for _, state := range []model.ProjectState{
		{Path: projectOne, Name: "quickgame_01", Status: model.StatusIdle, PresentOnDisk: true, InScope: true, UpdatedAt: now},
		{Path: projectTwo, Name: "quickgame_02", Status: model.StatusIdle, PresentOnDisk: true, InScope: true, UpdatedAt: now},
		{Path: keep, Name: "keep", Status: model.StatusIdle, PresentOnDisk: true, InScope: true, UpdatedAt: now},
	} {
		if err := st.UpsertProjectState(ctx, state); err != nil {
			t.Fatalf("seed project %s: %v", state.Path, err)
		}
	}

	svc := New(config.Default(), st, events.NewBus(), nil)
	if err := svc.ArchiveProjects(ctx, []string{projectTwo, projectOne, projectTwo}); err != nil {
		t.Fatalf("ArchiveProjects() error = %v", err)
	}

	current, err := st.ListProjects(ctx, false)
	if err != nil {
		t.Fatalf("ListProjects(current) error = %v", err)
	}
	if len(current) != 1 || current[0].Path != keep {
		t.Fatalf("active projects = %#v, want only keep project", current)
	}
	all, err := st.ListProjects(ctx, true)
	if err != nil {
		t.Fatalf("ListProjects(all) error = %v", err)
	}
	archived := map[string]bool{}
	for _, project := range all {
		archived[project.Path] = project.Archived
	}
	if !archived[projectOne] || !archived[projectTwo] || archived[keep] {
		t.Fatalf("archived flags = %#v", archived)
	}

	if err := svc.UnarchiveProjects(ctx, []string{projectOne, projectTwo}); err != nil {
		t.Fatalf("UnarchiveProjects() error = %v", err)
	}
	current, err = st.ListProjects(ctx, false)
	if err != nil {
		t.Fatalf("ListProjects(current after unarchive) error = %v", err)
	}
	if len(current) != 3 {
		t.Fatalf("current projects after unarchive = %#v, want 3 projects", current)
	}
}

func TestUnarchiveProjectLeavesOutOfScopeProjectHidden(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	projectPath := filepath.Join(t.TempDir(), "outside-scope-project")
	if err := os.MkdirAll(projectPath, 0o755); err != nil {
		t.Fatalf("create project dir: %v", err)
	}
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:          projectPath,
		Name:          "outside-scope-project",
		Status:        model.StatusIdle,
		PresentOnDisk: true,
		InScope:       false,
		Archived:      false,
		UpdatedAt:     time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed project: %v", err)
	}

	svc := New(config.Default(), st, events.NewBus(), nil)
	if err := svc.UnarchiveProject(ctx, projectPath); err != nil {
		t.Fatalf("UnarchiveProject() error = %v", err)
	}

	current, err := st.ListProjects(ctx, false)
	if err != nil {
		t.Fatalf("ListProjects(current after unarchive) error = %v", err)
	}
	if len(current) != 0 {
		t.Fatalf("out-of-scope project should stay out of current list, got %#v", current)
	}
	all, err := st.ListProjects(ctx, true)
	if err != nil {
		t.Fatalf("ListProjects(all after unarchive) error = %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("historical project should remain queryable, got %#v", all)
	}
	if got := all[0]; got.Archived || got.InScope || got.ManuallyAdded {
		t.Fatalf("out-of-scope project flags = %#v, want not archived, out of scope, not manual", got)
	}
}

func TestScanOnceKeepsConcurrentForgottenMissingProjectHidden(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	visiblePath := filepath.Join(root, "alpha-repo")
	missingPath := filepath.Join(root, "zeta-missing-repo")
	initGitRepo(t, visiblePath)
	initGitRepo(t, missingPath)
	if err := os.RemoveAll(missingPath); err != nil {
		t.Fatalf("remove missing project path: %v", err)
	}

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	now := time.Now().UTC()
	for _, state := range []model.ProjectState{
		{
			Path:           visiblePath,
			Name:           filepath.Base(visiblePath),
			Status:         model.StatusIdle,
			AttentionScore: 10,
			PresentOnDisk:  true,
			InScope:        true,
			UpdatedAt:      now,
		},
		{
			Path:           missingPath,
			Name:           filepath.Base(missingPath),
			Status:         model.StatusIdle,
			AttentionScore: 10,
			PresentOnDisk:  false,
			InScope:        true,
			UpdatedAt:      now,
		},
	} {
		if err := st.UpsertProjectState(ctx, state); err != nil {
			t.Fatalf("seed project state %s: %v", state.Path, err)
		}
	}

	cfg := config.Default()
	cfg.IncludePaths = []string{root}
	svc := New(cfg, st, events.NewBus(), nil)

	originalFingerprintReader := svc.gitFingerprintReader
	if originalFingerprintReader == nil {
		t.Fatalf("gitFingerprintReader = nil")
	}

	scanBlocked := make(chan struct{})
	releaseScan := make(chan struct{})
	var blockOnce sync.Once
	svc.gitFingerprintReader = func(ctx context.Context, path string) (scanner.GitFingerprint, error) {
		if filepath.Clean(path) == filepath.Clean(visiblePath) {
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
		t.Fatalf("timed out waiting for scan to block on visible project fingerprint read")
	}

	if err := svc.ForgetProject(ctx, missingPath); err != nil {
		t.Fatalf("ForgetProject() error = %v", err)
	}
	close(releaseScan)

	if err := <-scanDone; err != nil {
		t.Fatalf("ScanOnce() error = %v", err)
	}

	detail, err := st.GetProjectDetail(ctx, missingPath, 5)
	if err != nil {
		t.Fatalf("GetProjectDetail() after concurrent forget error = %v", err)
	}
	if !detail.Summary.Forgotten {
		t.Fatalf("missing project should stay forgotten after a concurrent scan: %#v", detail.Summary)
	}
	if detail.Summary.PresentOnDisk {
		t.Fatalf("missing project should stay marked missing after a concurrent scan: %#v", detail.Summary)
	}

	visibleProjects, err := st.ListProjects(ctx, false)
	if err != nil {
		t.Fatalf("list visible projects after concurrent forget: %v", err)
	}
	if len(visibleProjects) != 1 || visibleProjects[0].Path != visiblePath {
		t.Fatalf("visible projects after concurrent forget = %#v, want only visible repo", visibleProjects)
	}
}

func TestScanOnceMarksPrunedLinkedWorktreeAsForgotten(t *testing.T) {
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

	cfg := config.Default()
	cfg.IncludePaths = nil
	svc := New(cfg, st, events.NewBus(), nil)
	if _, err := svc.CreateOrAttachProject(ctx, CreateOrAttachProjectRequest{
		ParentPath: root,
		Name:       "repo--feature",
	}); err != nil {
		t.Fatalf("track worktree: %v", err)
	}

	if err := os.RemoveAll(worktreePath); err != nil {
		t.Fatalf("remove worktree path: %v", err)
	}
	if err := svc.PruneWorktrees(ctx, projectPath); err != nil {
		t.Fatalf("prune worktrees: %v", err)
	}

	report, err := svc.ScanOnce(ctx)
	if err != nil {
		t.Fatalf("scan after prune: %v", err)
	}
	if len(report.States) != 0 {
		t.Fatalf("expected pruned worktree to be hidden after scan, got %#v", report.States)
	}

	visibleProjects, err := st.ListProjects(ctx, false)
	if err != nil {
		t.Fatalf("list visible projects: %v", err)
	}
	if len(visibleProjects) != 0 {
		t.Fatalf("expected pruned worktree to be hidden from visible list, got %#v", visibleProjects)
	}

	detail, err := st.GetProjectDetail(ctx, worktreePath, 5)
	if err != nil {
		t.Fatalf("get pruned worktree detail: %v", err)
	}
	if !detail.Summary.Forgotten {
		t.Fatalf("expected pruned worktree to be marked forgotten: %#v", detail.Summary)
	}
	if detail.Summary.PresentOnDisk {
		t.Fatalf("expected pruned worktree to be marked missing on disk: %#v", detail.Summary)
	}
}

func TestScanOnceForgetsMissingLinkedWorktreeWithLostMetadata(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	projectPath := filepath.Join(root, "repo")
	worktreePath := filepath.Join(root, "repo--old-task")
	initGitRepo(t, projectPath)

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	now := time.Now().UTC().Truncate(time.Second)
	for _, state := range []model.ProjectState{
		{
			Path:          projectPath,
			Name:          "repo",
			Status:        model.StatusIdle,
			PresentOnDisk: true,
			InScope:       true,
			UpdatedAt:     now,
		},
		{
			Path:          worktreePath,
			Name:          "repo--old-task",
			Status:        model.StatusIdle,
			PresentOnDisk: false,
			Forgotten:     false,
			InScope:       true,
			UpdatedAt:     now,
		},
	} {
		if err := st.UpsertProjectState(ctx, state); err != nil {
			t.Fatalf("seed project state %s: %v", state.Path, err)
		}
	}

	cfg := config.Default()
	cfg.IncludePaths = nil
	svc := New(cfg, st, events.NewBus(), nil)
	report, err := svc.ScanOnce(ctx)
	if err != nil {
		t.Fatalf("ScanOnce() error = %v", err)
	}
	for _, state := range report.States {
		if state.Path == worktreePath {
			t.Fatalf("missing inferred worktree should be hidden from scan states, got %#v", report.States)
		}
	}

	detail, err := st.GetProjectDetail(ctx, worktreePath, 5)
	if err != nil {
		t.Fatalf("get inferred missing worktree detail: %v", err)
	}
	if !detail.Summary.Forgotten {
		t.Fatalf("expected inferred missing worktree to be marked forgotten: %#v", detail.Summary)
	}
	if detail.Summary.PresentOnDisk {
		t.Fatalf("expected inferred missing worktree to remain missing on disk: %#v", detail.Summary)
	}
	if detail.Summary.WorktreeKind != model.WorktreeKindLinked || detail.Summary.WorktreeRootPath != projectPath {
		t.Fatalf("expected inferred worktree metadata root=%s kind=%s, got root=%s kind=%s", projectPath, model.WorktreeKindLinked, detail.Summary.WorktreeRootPath, detail.Summary.WorktreeKind)
	}

	visibleProjects, err := st.ListProjects(ctx, false)
	if err != nil {
		t.Fatalf("list visible projects: %v", err)
	}
	if len(visibleProjects) != 1 || visibleProjects[0].Path != projectPath {
		t.Fatalf("visible projects after inferred missing worktree cleanup = %#v, want only root", visibleProjects)
	}
}

func TestScanOnceClearsTodoWorkSessionForPrunedLinkedWorktree(t *testing.T) {
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

	cfg := config.Default()
	cfg.IncludePaths = nil
	svc := New(cfg, st, events.NewBus(), nil)
	if _, err := svc.CreateOrAttachProject(ctx, CreateOrAttachProjectRequest{
		ParentPath: root,
		Name:       "repo",
	}); err != nil {
		t.Fatalf("track root project: %v", err)
	}
	if _, err := svc.CreateOrAttachProject(ctx, CreateOrAttachProjectRequest{
		ParentPath: root,
		Name:       "repo--feature",
	}); err != nil {
		t.Fatalf("track worktree: %v", err)
	}
	todo, err := st.AddTodo(ctx, projectPath, "Restart this after the old lane is gone")
	if err != nil {
		t.Fatalf("add todo: %v", err)
	}
	if err := st.AttachTodoWorkSession(ctx, todo.ID, worktreePath, model.SessionSourceCodex, "codex:thread-gone", model.TodoWorkStateWorking, time.Now()); err != nil {
		t.Fatalf("attach todo work session: %v", err)
	}

	if err := os.RemoveAll(worktreePath); err != nil {
		t.Fatalf("remove worktree path: %v", err)
	}
	if err := svc.PruneWorktrees(ctx, projectPath); err != nil {
		t.Fatalf("prune worktrees: %v", err)
	}
	if _, err := svc.ScanOnce(ctx); err != nil {
		t.Fatalf("scan after prune: %v", err)
	}

	got, err := st.GetTodo(ctx, todo.ID)
	if err != nil {
		t.Fatalf("get todo after scan: %v", err)
	}
	if got.WorkProvider != "" || got.WorkProjectPath != "" || got.WorkSessionID != "" || got.WorkState != "" || !got.WorkClaimedAt.IsZero() || !got.WorkStateAt.IsZero() {
		t.Fatalf("pruned worktree TODO kept work metadata: %#v", got)
	}
}

func TestScanOnceMarksMissingProjectWithoutFreshActivity(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	projectPath := filepath.Join(root, "missing_repo")
	initGitRepo(t, projectPath)

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	initialActivityAt := time.Now().Add(-20 * time.Minute).UTC().Truncate(time.Second)
	detector := &fakeDetector{
		activities: map[string]*model.DetectorProjectActivity{
			projectPath: fakeActivity(projectPath, "ses_missing_initial", initialActivityAt),
		},
	}
	cfg := config.Default()
	cfg.IncludePaths = []string{root}

	svc := New(cfg, st, events.NewBus(), []detectors.Detector{detector})
	firstReport, err := svc.ScanOnce(ctx)
	if err != nil {
		t.Fatalf("first scan: %v", err)
	}
	if len(firstReport.States) != 1 || firstReport.States[0].Path != projectPath {
		t.Fatalf("unexpected first scan states: %#v", firstReport.States)
	}

	if err := os.RemoveAll(projectPath); err != nil {
		t.Fatalf("remove project path: %v", err)
	}
	detector.activities = nil

	secondReport, err := svc.ScanOnce(ctx)
	if err != nil {
		t.Fatalf("second scan: %v", err)
	}
	if len(secondReport.States) != 1 || secondReport.States[0].Path != projectPath {
		t.Fatalf("expected missing project to remain tracked, got %#v", secondReport.States)
	}
	if secondReport.States[0].PresentOnDisk {
		t.Fatalf("expected missing project to be marked absent on disk")
	}

	detail, err := st.GetProjectDetail(ctx, projectPath, 10)
	if err != nil {
		t.Fatalf("get missing project detail: %v", err)
	}
	if detail.Summary.PresentOnDisk {
		t.Fatalf("expected stored summary to mark project missing")
	}
}

func TestScanOnceForgetsStaleLinkedWorktreeDirectoryStillOnDisk(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	projectPath := filepath.Join(root, "repo")
	initGitRepo(t, projectPath)

	staleWorktreePath := filepath.Join(root, "repo--stale-worktree")
	if err := os.MkdirAll(staleWorktreePath, 0o755); err != nil {
		t.Fatalf("mkdir stale worktree path: %v", err)
	}
	staleGitDir := filepath.Join(projectPath, ".git", "worktrees", "repo--stale-worktree")
	if err := os.WriteFile(filepath.Join(staleWorktreePath, ".git"), []byte("gitdir: "+staleGitDir+"\n"), 0o644); err != nil {
		t.Fatalf("write stale gitfile: %v", err)
	}

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	activityAt := time.Now().Add(-10 * time.Minute).UTC().Truncate(time.Second)
	detector := staticDetector{
		activities: map[string]*model.DetectorProjectActivity{
			staleWorktreePath: fakeActivity(staleWorktreePath, "ses_stale_worktree", activityAt),
		},
	}
	cfg := config.Default()
	cfg.IncludePaths = []string{root}

	svc := New(cfg, st, events.NewBus(), []detectors.Detector{detector})
	if _, err := svc.CreateOrAttachProject(ctx, CreateOrAttachProjectRequest{
		ParentPath: root,
		Name:       "repo",
	}); err != nil {
		t.Fatalf("track root project: %v", err)
	}
	report, err := svc.ScanOnce(ctx)
	if err != nil {
		t.Fatalf("ScanOnce() error = %v", err)
	}
	if len(report.States) == 0 {
		t.Fatalf("scan should persist at least one project state")
	}

	detail, err := st.GetProjectDetail(ctx, staleWorktreePath, 5)
	if err != nil {
		t.Fatalf("GetProjectDetail(stale worktree) error = %v", err)
	}
	if !detail.Summary.Forgotten {
		t.Fatalf("stale linked worktree should be forgotten: %#v", detail.Summary)
	}
	if !detail.Summary.PresentOnDisk {
		t.Fatalf("stale linked worktree directory should still be marked present on disk: %#v", detail.Summary)
	}
	if detail.Summary.WorktreeRootPath != projectPath {
		t.Fatalf("WorktreeRootPath = %q, want %q", detail.Summary.WorktreeRootPath, projectPath)
	}
	if detail.Summary.WorktreeKind != model.WorktreeKindLinked {
		t.Fatalf("WorktreeKind = %q, want %q", detail.Summary.WorktreeKind, model.WorktreeKindLinked)
	}

	visible, err := st.ListProjects(ctx, false)
	if err != nil {
		t.Fatalf("ListProjects() error = %v", err)
	}
	if len(visible) != 1 || visible[0].Path != projectPath {
		t.Fatalf("visible projects = %#v, want only the repo root", visible)
	}
}

func TestRefreshProjectStatusForgetsStaleLinkedWorktreeDirectoryStillOnDisk(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	projectPath := filepath.Join(root, "repo")
	initGitRepo(t, projectPath)

	staleWorktreePath := filepath.Join(root, "repo--stale-worktree")
	if err := os.MkdirAll(staleWorktreePath, 0o755); err != nil {
		t.Fatalf("mkdir stale worktree path: %v", err)
	}
	staleGitDir := filepath.Join(projectPath, ".git", "worktrees", "repo--stale-worktree")
	if err := os.WriteFile(filepath.Join(staleWorktreePath, ".git"), []byte("gitdir: "+staleGitDir+"\n"), 0o644); err != nil {
		t.Fatalf("write stale gitfile: %v", err)
	}

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

	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:          staleWorktreePath,
		Name:          filepath.Base(staleWorktreePath),
		Status:        model.StatusIdle,
		PresentOnDisk: true,
		ManuallyAdded: true,
		InScope:       true,
		UpdatedAt:     time.Now(),
	}); err != nil {
		t.Fatalf("seed stale worktree state: %v", err)
	}

	if err := svc.RefreshProjectStatus(ctx, staleWorktreePath); err != nil {
		t.Fatalf("RefreshProjectStatus() error = %v", err)
	}

	detail, err := st.GetProjectDetail(ctx, staleWorktreePath, 5)
	if err != nil {
		t.Fatalf("GetProjectDetail(stale worktree) error = %v", err)
	}
	if !detail.Summary.Forgotten {
		t.Fatalf("stale linked worktree should be forgotten after refresh: %#v", detail.Summary)
	}
	if detail.Summary.WorktreeRootPath != projectPath {
		t.Fatalf("WorktreeRootPath = %q, want %q", detail.Summary.WorktreeRootPath, projectPath)
	}
	if detail.Summary.WorktreeKind != model.WorktreeKindLinked {
		t.Fatalf("WorktreeKind = %q, want %q", detail.Summary.WorktreeKind, model.WorktreeKindLinked)
	}
}

func TestRefreshProjectStatusForgetsRemovedPrunedLinkedWorktree(t *testing.T) {
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

	cfg := config.Default()
	cfg.IncludePaths = nil
	svc := New(cfg, st, events.NewBus(), nil)
	if _, err := svc.CreateOrAttachProject(ctx, CreateOrAttachProjectRequest{
		ParentPath: root,
		Name:       "repo--feature",
	}); err != nil {
		t.Fatalf("track worktree: %v", err)
	}

	if err := os.RemoveAll(worktreePath); err != nil {
		t.Fatalf("remove worktree path: %v", err)
	}
	if err := svc.PruneWorktrees(ctx, projectPath); err != nil {
		t.Fatalf("prune worktrees: %v", err)
	}

	if err := svc.RefreshProjectStatus(ctx, worktreePath); err != nil {
		t.Fatalf("RefreshProjectStatus() error = %v", err)
	}

	detail, err := st.GetProjectDetail(ctx, worktreePath, 5)
	if err != nil {
		t.Fatalf("GetProjectDetail() after refresh error = %v", err)
	}
	if !detail.Summary.Forgotten {
		t.Fatalf("removed linked worktree should be forgotten after refresh: %#v", detail.Summary)
	}
	if detail.Summary.PresentOnDisk {
		t.Fatalf("removed linked worktree should stay marked missing on disk: %#v", detail.Summary)
	}

	visibleProjects, err := st.ListProjects(ctx, false)
	if err != nil {
		t.Fatalf("list visible projects: %v", err)
	}
	if len(visibleProjects) != 0 {
		t.Fatalf("expected removed linked worktree to stay hidden from visible list, got %#v", visibleProjects)
	}
}

func TestScanOnceDetectsDirtyRepoAndClearsWhenClean(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	projectPath := filepath.Join(root, "dirty_repo")
	initGitRepo(t, projectPath)

	if err := os.WriteFile(filepath.Join(projectPath, "scratch.txt"), []byte("draft\n"), 0o644); err != nil {
		t.Fatalf("write dirty file: %v", err)
	}

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	activityAt := time.Now().Add(-10 * time.Minute).UTC().Truncate(time.Second)
	detector := &fakeDetector{
		activities: map[string]*model.DetectorProjectActivity{
			projectPath: fakeActivity(projectPath, "ses_dirty", activityAt),
		},
	}
	cfg := config.Default()
	cfg.IncludePaths = []string{root}

	svc := New(cfg, st, events.NewBus(), []detectors.Detector{detector})
	firstReport, err := svc.ScanOnce(ctx)
	if err != nil {
		t.Fatalf("first scan: %v", err)
	}
	if len(firstReport.States) != 1 {
		t.Fatalf("expected 1 project, got %#v", firstReport.States)
	}
	if !firstReport.States[0].RepoDirty {
		t.Fatalf("expected repo to be marked dirty")
	}
	foundReason := false
	for _, reason := range firstReport.States[0].AttentionReason {
		if reason.Code == "repo_dirty" {
			foundReason = true
			break
		}
	}
	if !foundReason {
		t.Fatalf("expected repo_dirty reason, got %#v", firstReport.States[0].AttentionReason)
	}

	if err := os.Remove(filepath.Join(projectPath, "scratch.txt")); err != nil {
		t.Fatalf("remove dirty file: %v", err)
	}

	secondReport, err := svc.ScanOnce(ctx)
	if err != nil {
		t.Fatalf("second scan: %v", err)
	}
	if len(secondReport.States) != 1 {
		t.Fatalf("expected 1 project on second scan, got %#v", secondReport.States)
	}
	if secondReport.States[0].RepoDirty {
		t.Fatalf("expected repo dirty flag to clear")
	}

	detail, err := st.GetProjectDetail(ctx, projectPath, 10)
	if err != nil {
		t.Fatalf("get detail: %v", err)
	}
	if detail.Summary.RepoDirty {
		t.Fatalf("expected stored summary to be clean after second scan")
	}
}

func TestScanOnceClearsDirtyWhenGitDirRemoved(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	projectPath := filepath.Join(root, "was_git")
	initGitRepo(t, projectPath)

	// Make the repo dirty so that the first scan records RepoDirty=true.
	if err := os.WriteFile(filepath.Join(projectPath, "scratch.txt"), []byte("wip\n"), 0o644); err != nil {
		t.Fatalf("write dirty file: %v", err)
	}

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	activityAt := time.Now().Add(-10 * time.Minute).UTC().Truncate(time.Second)
	detector := &fakeDetector{
		activities: map[string]*model.DetectorProjectActivity{
			projectPath: fakeActivity(projectPath, "ses_gitgone", activityAt),
		},
	}
	cfg := config.Default()
	cfg.IncludePaths = []string{root}

	svc := New(cfg, st, events.NewBus(), []detectors.Detector{detector})
	firstReport, err := svc.ScanOnce(ctx)
	if err != nil {
		t.Fatalf("first scan: %v", err)
	}
	if len(firstReport.States) != 1 {
		t.Fatalf("expected 1 project, got %d", len(firstReport.States))
	}
	if !firstReport.States[0].RepoDirty {
		t.Fatalf("expected repo to be dirty on first scan")
	}

	// Remove the .git directory — the project is no longer a git repository.
	if err := os.RemoveAll(filepath.Join(projectPath, ".git")); err != nil {
		t.Fatalf("remove .git: %v", err)
	}

	secondReport, err := svc.ScanOnce(ctx)
	if err != nil {
		t.Fatalf("second scan: %v", err)
	}
	if len(secondReport.States) != 1 {
		t.Fatalf("expected 1 project on second scan, got %d", len(secondReport.States))
	}
	if secondReport.States[0].RepoDirty {
		t.Fatalf("expected repo dirty flag to clear after .git removal")
	}
	if secondReport.States[0].RepoBranch != "" {
		t.Fatalf("expected empty branch after .git removal, got %q", secondReport.States[0].RepoBranch)
	}
	if secondReport.States[0].WorktreeKind != model.WorktreeKindNone {
		t.Fatalf("expected worktree kind none after .git removal, got %q", secondReport.States[0].WorktreeKind)
	}
}

func TestScanOnceDetectsRepoAheadOfRemote(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	remotePath := filepath.Join(root, "remote.git")
	initBareGitRepo(t, remotePath)

	projectPath := filepath.Join(root, "ahead_repo")
	initGitRepo(t, projectPath)
	runGit(t, projectPath, "git", "remote", "add", "origin", remotePath)
	runGit(t, projectPath, "git", "push", "-u", "origin", "master")

	if err := os.WriteFile(filepath.Join(projectPath, "README.md"), []byte("hello\nahead\n"), 0o644); err != nil {
		t.Fatalf("update README: %v", err)
	}
	runGit(t, projectPath, "git", "add", "README.md")
	runGit(t, projectPath, "git", "commit", "-m", "ahead")

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	activityAt := time.Now().Add(-10 * time.Minute).UTC().Truncate(time.Second)
	detector := &fakeDetector{
		activities: map[string]*model.DetectorProjectActivity{
			projectPath: fakeActivity(projectPath, "ses_ahead", activityAt),
		},
	}
	cfg := config.Default()
	cfg.IncludePaths = []string{root}

	svc := New(cfg, st, events.NewBus(), []detectors.Detector{detector})
	report, err := svc.ScanOnce(ctx)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(report.States) < 1 {
		t.Fatalf("expected at least one scanned project")
	}

	var found *model.ProjectState
	for i := range report.States {
		if report.States[i].Path == projectPath {
			found = &report.States[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("expected project %s in scan report", projectPath)
	}
	if found.RepoSyncStatus != model.RepoSyncAhead {
		t.Fatalf("repo sync status = %s, want %s", found.RepoSyncStatus, model.RepoSyncAhead)
	}
	if strings.TrimSpace(found.RepoBranch) == "" {
		t.Fatalf("expected scan report to include repo branch: %+v", *found)
	}
	if found.RepoAheadCount < 1 || found.RepoBehindCount != 0 {
		t.Fatalf("unexpected ahead/behind counts: %+v", *found)
	}

	detail, err := st.GetProjectDetail(ctx, projectPath, 10)
	if err != nil {
		t.Fatalf("get detail: %v", err)
	}
	if detail.Summary.RepoSyncStatus != model.RepoSyncAhead || detail.Summary.RepoAheadCount < 1 {
		t.Fatalf("expected stored summary to preserve ahead status, got %#v", detail.Summary)
	}
	if strings.TrimSpace(detail.Summary.RepoBranch) == "" {
		t.Fatalf("expected stored summary to preserve repo branch, got %#v", detail.Summary)
	}
}
