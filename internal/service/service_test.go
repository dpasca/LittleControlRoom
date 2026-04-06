package service

import (
	"context"
	"database/sql"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"lcroom/internal/aibackend"
	"lcroom/internal/appfs"
	"lcroom/internal/brand"
	"lcroom/internal/config"
	"lcroom/internal/detectors"
	"lcroom/internal/events"
	"lcroom/internal/gitops"
	"lcroom/internal/llm"
	"lcroom/internal/model"
	"lcroom/internal/scanner"
	"lcroom/internal/sessionclassify"
	"lcroom/internal/store"
)

type staticDetector struct {
	name       string
	activities map[string]*model.DetectorProjectActivity
}

func (d staticDetector) Name() string {
	if d.name != "" {
		return d.name
	}
	return "static"
}

func (d staticDetector) Detect(context.Context, scanner.PathScope) (map[string]*model.DetectorProjectActivity, error) {
	out := make(map[string]*model.DetectorProjectActivity, len(d.activities))
	for path, activity := range d.activities {
		out[path] = activity
	}
	return out, nil
}

type recordingClassifier struct {
	normalCalls int
	forcedCalls int
}

func (c *recordingClassifier) QueueProject(context.Context, model.ProjectState) (bool, error) {
	c.normalCalls++
	return true, nil
}

func (c *recordingClassifier) QueueProjectRetry(context.Context, model.ProjectState, time.Duration) (bool, error) {
	c.forcedCalls++
	return true, nil
}

func (c *recordingClassifier) Notify()               {}
func (c *recordingClassifier) Start(context.Context) {}

func TestApplyEditableSettingsSkipsAIClientRefreshForEmbeddedModelPreferences(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.AIBackend = config.AIBackendCodex

	detectCalls := 0
	svc := &Service{
		cfg:               cfg,
		bus:               events.NewBus(),
		llmUsageTracker:   llm.NewUsageTracker(),
		opencodeDiscovery: llm.NewOpenCodeDiscovery(),
		backendDetector: func(context.Context, config.AppConfig, config.AIBackend) aibackend.Status {
			detectCalls++
			return readyBackendStatus(config.AIBackendCodex)
		},
	}

	settings := config.EditableSettingsFromAppConfig(cfg)
	settings.EmbeddedCodexModel = "gpt-5.4"
	settings.EmbeddedCodexReasoning = "high"

	svc.ApplyEditableSettings(settings)

	if detectCalls != 0 {
		t.Fatalf("backend detector calls = %d, want 0 for embedded model-only changes", detectCalls)
	}
	if got := svc.cfg.EmbeddedCodexModel; got != "gpt-5.4" {
		t.Fatalf("embedded codex model = %q, want gpt-5.4", got)
	}
	if got := svc.cfg.EmbeddedCodexReasoning; got != "high" {
		t.Fatalf("embedded codex reasoning = %q, want high", got)
	}
}

func TestApplyEditableSettingsRefreshesAIClientsWhenBackendConfigChanges(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.AIBackend = config.AIBackendCodex

	detectCalls := 0
	svc := &Service{
		cfg:               cfg,
		bus:               events.NewBus(),
		llmUsageTracker:   llm.NewUsageTracker(),
		opencodeDiscovery: llm.NewOpenCodeDiscovery(),
		backendDetector: func(_ context.Context, cfg config.AppConfig, backend config.AIBackend) aibackend.Status {
			detectCalls++
			return readyBackendStatus(firstNonZeroBackend(backend, cfg.EffectiveAIBackend()))
		},
	}

	settings := config.EditableSettingsFromAppConfig(cfg)
	settings.AIBackend = config.AIBackendOpenAIAPI
	settings.OpenAIAPIKey = "sk-test-example"

	svc.ApplyEditableSettings(settings)

	if detectCalls != 1 {
		t.Fatalf("backend detector calls = %d, want 1 when backend config changes", detectCalls)
	}
	if svc.commitMessageSuggester == nil {
		t.Fatalf("commitMessageSuggester = nil, want OpenAI client after reconfigure")
	}
	if svc.classifier == nil {
		t.Fatalf("classifier = nil, want OpenAI client after reconfigure")
	}
	if svc.todoSuggester == nil {
		t.Fatalf("todoSuggester = nil, want OpenAI suggester after reconfigure")
	}
}

func TestApplyEditableSettingsResetsUsageWhenBackendChanges(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.AIBackend = config.AIBackendCodex

	svc := &Service{
		cfg:               cfg,
		bus:               events.NewBus(),
		llmUsageTracker:   llm.NewUsageTracker(),
		opencodeDiscovery: llm.NewOpenCodeDiscovery(),
		backendDetector: func(_ context.Context, cfg config.AppConfig, backend config.AIBackend) aibackend.Status {
			return readyBackendStatus(firstNonZeroBackend(backend, cfg.EffectiveAIBackend()))
		},
	}

	svc.llmUsageTracker.Start("gpt-5-mini")
	svc.llmUsageTracker.Complete("gpt-5-mini", model.LLMUsage{
		InputTokens:  120,
		OutputTokens: 30,
		TotalTokens:  150,
	})

	settings := config.EditableSettingsFromAppConfig(cfg)
	settings.AIBackend = config.AIBackendOpenCode

	svc.ApplyEditableSettings(settings)

	usage := svc.SessionUsage()
	if usage.Started != 0 || usage.Completed != 0 || usage.Failed != 0 || usage.Running != 0 {
		t.Fatalf("usage counters after backend switch = %+v, want all zero", usage)
	}
	if usage.Totals != (model.LLMUsage{}) {
		t.Fatalf("usage totals after backend switch = %+v, want zero totals", usage.Totals)
	}
}

func TestRefreshProjectStatusAsyncCoalescesConcurrentRequests(t *testing.T) {
	t.Parallel()

	started := make(chan int, 4)
	completed := make(chan int, 4)
	release := make(chan struct{})

	callCount := 0
	var callsMu sync.Mutex

	svc := &Service{
		refreshProjectStatusFn: func(context.Context, string) error {
			callsMu.Lock()
			callCount++
			callID := callCount
			callsMu.Unlock()

			started <- callID
			<-release
			completed <- callID
			return nil
		},
	}

	const projectPath = "/tmp/demo"
	svc.refreshProjectStatusAsync(projectPath)

	select {
	case got := <-started:
		if got != 1 {
			t.Fatalf("first refresh call = %d, want 1", got)
		}
	case <-time.After(time.Second):
		t.Fatal("first refresh did not start")
	}

	svc.refreshProjectStatusAsync(projectPath)
	svc.refreshProjectStatusAsync(projectPath)

	select {
	case got := <-started:
		t.Fatalf("unexpected concurrent refresh call %d while first refresh is still running", got)
	case <-time.After(100 * time.Millisecond):
	}

	release <- struct{}{}

	select {
	case got := <-completed:
		if got != 1 {
			t.Fatalf("first completed refresh = %d, want 1", got)
		}
	case <-time.After(time.Second):
		t.Fatal("first refresh did not complete")
	}

	select {
	case got := <-started:
		if got != 2 {
			t.Fatalf("queued refresh call = %d, want 2", got)
		}
	case <-time.After(time.Second):
		t.Fatal("queued refresh did not start")
	}

	select {
	case got := <-started:
		t.Fatalf("unexpected extra refresh call %d after coalesced rerun started", got)
	case <-time.After(100 * time.Millisecond):
	}

	release <- struct{}{}

	select {
	case got := <-completed:
		if got != 2 {
			t.Fatalf("second completed refresh = %d, want 2", got)
		}
	case <-time.After(time.Second):
		t.Fatal("second refresh did not complete")
	}

	select {
	case got := <-started:
		t.Fatalf("unexpected third refresh call %d after queued requests were coalesced", got)
	case <-time.After(150 * time.Millisecond):
	}

	time.Sleep(20 * time.Millisecond)

	svc.refreshMu.Lock()
	defer svc.refreshMu.Unlock()
	if len(svc.refreshState) != 0 {
		t.Fatalf("refreshState = %#v, want empty after coalesced refresh finishes", svc.refreshState)
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

func TestScanOnceCanonicalizesStaticRepoRenameAlias(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	newPath := filepath.Join(root, strings.ReplaceAll(brand.Name, " ", ""))
	oldPath := filepath.Join(root, legacyRepoDirName)
	initGitRepo(t, newPath)

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	activityAt := time.Now().UTC().Truncate(time.Second)
	detector := &fakeDetector{
		activities: map[string]*model.DetectorProjectActivity{
			oldPath: {
				ProjectPath:  oldPath,
				LastActivity: activityAt,
				Source:       "codex",
				Sessions: []model.SessionEvidence{{
					SessionID:           "ses_repo_rename",
					ProjectPath:         oldPath,
					DetectedProjectPath: oldPath,
					SessionFile:         filepath.Join(root, "repo-rename.jsonl"),
					Format:              "modern",
					StartedAt:           activityAt.Add(-2 * time.Minute),
					LastEventAt:         activityAt,
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
	if len(report.States) != 1 || report.States[0].Path != newPath {
		t.Fatalf("unexpected scan states: %#v", report.States)
	}
	if len(report.States[0].Sessions) != 1 || report.States[0].Sessions[0].DetectedProjectPath != oldPath {
		t.Fatalf("expected canonicalized old-path session, got %#v", report.States[0].Sessions)
	}

	aliases, err := st.GetPathAliases(ctx)
	if err != nil {
		t.Fatalf("get path aliases: %v", err)
	}
	alias, ok := aliases[oldPath]
	if !ok || alias.NewPath != newPath {
		t.Fatalf("expected static alias %s -> %s, got %#v", oldPath, newPath, aliases)
	}
}

func TestScanOnceConsolidatesStaticRepoRenameAliasIntoExistingProject(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	newPath := filepath.Join(root, strings.ReplaceAll(brand.Name, " ", ""))
	oldPath := filepath.Join(root, legacyRepoDirName)
	initGitRepo(t, newPath)

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	initialActivityAt := time.Now().Add(-40 * time.Minute).UTC().Truncate(time.Second)
	detector := &fakeDetector{
		activities: map[string]*model.DetectorProjectActivity{
			newPath: fakeActivity(newPath, "ses_repo_rename_existing", initialActivityAt),
		},
	}
	cfg := config.Default()
	cfg.IncludePaths = []string{root}
	svc := New(cfg, st, events.NewBus(), []detectors.Detector{detector})

	firstReport, err := svc.ScanOnce(ctx)
	if err != nil {
		t.Fatalf("first scan: %v", err)
	}
	if len(firstReport.States) != 1 || firstReport.States[0].Path != newPath {
		t.Fatalf("unexpected first scan states: %#v", firstReport.States)
	}

	oldUpdatedAt := time.Now().Add(-30 * time.Minute).UTC().Truncate(time.Second)
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:           oldPath,
		Name:           legacyRepoDirName,
		Status:         model.StatusIdle,
		AttentionScore: 10,
		InScope:        true,
		Pinned:         true,
		UpdatedAt:      oldUpdatedAt,
	}); err != nil {
		t.Fatalf("upsert legacy project state: %v", err)
	}

	activityAt := oldUpdatedAt.Add(20 * time.Minute)
	detector.activities = map[string]*model.DetectorProjectActivity{
		oldPath: {
			ProjectPath:  oldPath,
			LastActivity: activityAt,
			Source:       "codex",
			Sessions: []model.SessionEvidence{{
				SessionID:           "ses_repo_rename_merge",
				ProjectPath:         oldPath,
				DetectedProjectPath: oldPath,
				SessionFile:         filepath.Join(root, "repo-rename-merge.jsonl"),
				Format:              "modern",
				StartedAt:           activityAt.Add(-time.Minute),
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
	if !secondReport.States[0].Pinned {
		t.Fatalf("expected pin to survive consolidation, got %#v", secondReport.States[0])
	}
	if secondReport.States[0].MovedFromPath != oldPath {
		t.Fatalf("moved_from_path = %s, want %s", secondReport.States[0].MovedFromPath, oldPath)
	}
	if len(secondReport.States[0].Sessions) != 1 || secondReport.States[0].Sessions[0].DetectedProjectPath != oldPath {
		t.Fatalf("expected latest session to preserve old detected path, got %#v", secondReport.States[0].Sessions)
	}

	if _, err := st.GetProjectDetail(ctx, oldPath, 5); err == nil {
		t.Fatalf("expected old renamed path to be absent after consolidation")
	}
	detail, err := st.GetProjectDetail(ctx, newPath, 10)
	if err != nil {
		t.Fatalf("get merged detail: %v", err)
	}
	if !detail.Summary.Pinned {
		t.Fatalf("expected merged summary to preserve pin, got %#v", detail.Summary)
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

func TestAddTodoAndUpdateDoNotQueueWorktreeSuggestionsSpeculatively(t *testing.T) {
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

	item, err := svc.AddTodo(ctx, projectPath, "Only create a worktree name when I choose dedicated mode")
	if err != nil {
		t.Fatalf("add todo: %v", err)
	}
	if _, err := st.GetTodoWorktreeSuggestion(ctx, item.ID); err != sql.ErrNoRows {
		t.Fatalf("GetTodoWorktreeSuggestion() after add = %v, want sql.ErrNoRows", err)
	}

	if queued, err := st.QueueTodoWorktreeSuggestion(ctx, item.ID); err != nil {
		t.Fatalf("queue todo worktree suggestion: %v", err)
	} else if !queued {
		t.Fatalf("expected todo worktree suggestion to queue")
	}
	suggestion, err := st.ClaimNextQueuedTodoWorktreeSuggestion(ctx, 0, 0)
	if err != nil {
		t.Fatalf("claim todo worktree suggestion: %v", err)
	}
	suggestion.BranchName = "feat/on-demand-worktree"
	suggestion.WorktreeSuffix = "feat-on-demand-worktree"
	suggestion.Kind = "feature"
	suggestion.Reason = "Creates a worktree on demand."
	suggestion.Confidence = 0.91
	suggestion.Model = "test"
	if completed, err := st.CompleteTodoWorktreeSuggestion(ctx, suggestion); err != nil {
		t.Fatalf("complete todo worktree suggestion: %v", err)
	} else if !completed {
		t.Fatalf("expected todo worktree suggestion to complete")
	}

	if err := svc.UpdateTodo(ctx, projectPath, item.ID, "Still do not speculate on worktree names"); err != nil {
		t.Fatalf("update todo: %v", err)
	}
	if _, err := st.GetTodoWorktreeSuggestion(ctx, item.ID); err != sql.ErrNoRows {
		t.Fatalf("GetTodoWorktreeSuggestion() after update = %v, want sql.ErrNoRows", err)
	}
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
	if queued, err := st.QueueTodoWorktreeSuggestion(ctx, item.ID); err != nil {
		t.Fatalf("queue todo worktree suggestion: %v", err)
	} else if !queued {
		t.Fatalf("expected todo worktree suggestion to queue")
	}
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
	if queued, err := st.QueueTodoWorktreeSuggestion(ctx, item.ID); err != nil {
		t.Fatalf("queue todo worktree suggestion: %v", err)
	} else if !queued {
		t.Fatalf("expected todo worktree suggestion to queue")
	}
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
	if queued, err := st.QueueTodoWorktreeSuggestion(ctx, item.ID); err != nil {
		t.Fatalf("queue todo worktree suggestion: %v", err)
	} else if !queued {
		t.Fatalf("expected todo worktree suggestion to queue")
	}

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
	if queued, err := st.QueueTodoWorktreeSuggestion(ctx, item.ID); err != nil {
		t.Fatalf("queue todo worktree suggestion: %v", err)
	} else if !queued {
		t.Fatalf("expected todo worktree suggestion to queue")
	}
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
	if queued, err := st.QueueTodoWorktreeSuggestion(ctx, item.ID); err != nil {
		t.Fatalf("queue todo worktree suggestion: %v", err)
	} else if !queued {
		t.Fatalf("expected todo worktree suggestion to queue")
	}
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
	if queued, err := st.QueueTodoWorktreeSuggestion(ctx, item.ID); err != nil {
		t.Fatalf("queue todo worktree suggestion: %v", err)
	} else if !queued {
		t.Fatalf("expected todo worktree suggestion to queue")
	}
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
	if _, err := os.Stat(result.WorktreePath); !os.IsNotExist(err) {
		t.Fatalf("worktree path still exists after concurrent removal: stat err = %v", err)
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
	if queued, err := st.QueueTodoWorktreeSuggestion(ctx, item.ID); err != nil {
		t.Fatalf("queue todo worktree suggestion: %v", err)
	} else if !queued {
		t.Fatalf("expected todo worktree suggestion to queue")
	}
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
	if queued, err := st.QueueTodoWorktreeSuggestion(ctx, item.ID); err != nil {
		t.Fatalf("queue todo worktree suggestion: %v", err)
	} else if !queued {
		t.Fatalf("expected todo worktree suggestion to queue")
	}
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
	if queued, err := st.QueueTodoWorktreeSuggestion(ctx, item.ID); err != nil {
		t.Fatalf("queue todo worktree suggestion: %v", err)
	} else if !queued {
		t.Fatalf("expected todo worktree suggestion to queue")
	}
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
	if queued, err := st.QueueTodoWorktreeSuggestion(ctx, item.ID); err != nil {
		t.Fatalf("queue todo worktree suggestion: %v", err)
	} else if !queued {
		t.Fatalf("expected todo worktree suggestion to queue")
	}
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
}

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
	runGit(t, projectPath, "git", "add", "pixel.png", "old.txt")
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
	if len(preview.Files) != 4 {
		t.Fatalf("file count = %d, want 4", len(preview.Files))
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

type fakeDetector struct {
	activities map[string]*model.DetectorProjectActivity
}

type fakeUntrackedFileRecommender struct {
	lastInput  gitops.UntrackedFileRecommendationInput
	suggestion gitops.UntrackedFileRecommendationResult
	err        error
}

type fakeCommitMessageSuggester struct {
	suggestion gitops.CommitMessageSuggestion
	err        error
}

func (f *fakeUntrackedFileRecommender) RecommendUntracked(_ context.Context, input gitops.UntrackedFileRecommendationInput) (gitops.UntrackedFileRecommendationResult, error) {
	f.lastInput = input
	if f.err != nil {
		return gitops.UntrackedFileRecommendationResult{}, f.err
	}
	return f.suggestion, nil
}

func (f *fakeUntrackedFileRecommender) ModelName() string {
	return "fake-untracked-reviewer"
}

func (f fakeCommitMessageSuggester) Suggest(context.Context, gitops.CommitMessageInput) (gitops.CommitMessageSuggestion, error) {
	if f.err != nil {
		return gitops.CommitMessageSuggestion{}, f.err
	}
	return f.suggestion, nil
}

func (f fakeCommitMessageSuggester) ModelName() string {
	return "fake-commit-suggester"
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
