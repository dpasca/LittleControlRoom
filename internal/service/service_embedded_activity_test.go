package service

import (
	"context"
	"errors"
	"lcroom/internal/config"
	"lcroom/internal/detectors"
	"lcroom/internal/events"
	"lcroom/internal/model"
	"lcroom/internal/scanner"
	"lcroom/internal/store"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRefreshProjectAttentionAsyncCoalescesConcurrentRequests(t *testing.T) {
	t.Parallel()

	started := make(chan int, 4)
	completed := make(chan int, 4)
	release := make(chan struct{})

	callCount := 0
	var callsMu sync.Mutex

	svc := &Service{
		refreshProjectAttentionFn: func(context.Context, string) error {
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
	svc.refreshProjectAttentionAsync(projectPath)

	select {
	case got := <-started:
		if got != 1 {
			t.Fatalf("first refresh call = %d, want 1", got)
		}
	case <-time.After(time.Second):
		t.Fatal("first refresh did not start")
	}

	svc.refreshProjectAttentionAsync(projectPath)
	svc.refreshProjectAttentionAsync(projectPath)

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

	svc.backgroundRefreshMu.Lock()
	defer svc.backgroundRefreshMu.Unlock()
	if len(svc.backgroundRefreshState) != 0 {
		t.Fatalf("backgroundRefreshState = %#v, want empty after coalesced refresh finishes", svc.backgroundRefreshState)
	}
}

func TestProjectRefreshAsyncSerializesDifferentProjects(t *testing.T) {
	t.Parallel()

	started := make(chan string, 3)
	completed := make(chan string, 3)
	release := make(chan struct{})
	runner := func(_ context.Context, projectPath string) error {
		started <- projectPath
		<-release
		completed <- projectPath
		return nil
	}
	svc := &Service{
		refreshProjectAttentionFn: runner,
		refreshProjectStatusFn:    runner,
	}

	paths := []string{"/tmp/one", "/tmp/two", "/tmp/three"}
	svc.refreshProjectAttentionAsync(paths[0])
	svc.refreshProjectStatusAsync(paths[1])
	svc.refreshProjectAttentionAsync(paths[2])

	first := waitForProjectRefreshTestEvent(t, started, "first refresh to start")
	select {
	case path := <-started:
		t.Fatalf("refresh %q started while %q was still running", path, first)
	case <-time.After(100 * time.Millisecond):
	}

	release <- struct{}{}
	if got := waitForProjectRefreshTestEvent(t, completed, "first refresh to complete"); got != first {
		t.Fatalf("first completed refresh = %q, want %q", got, first)
	}
	second := waitForProjectRefreshTestEvent(t, started, "second refresh to start")
	if second == first {
		t.Fatalf("second refresh = %q, want a different project", second)
	}
	select {
	case path := <-started:
		t.Fatalf("refresh %q started while %q was still running", path, second)
	case <-time.After(100 * time.Millisecond):
	}

	release <- struct{}{}
	if got := waitForProjectRefreshTestEvent(t, completed, "second refresh to complete"); got != second {
		t.Fatalf("second completed refresh = %q, want %q", got, second)
	}
	third := waitForProjectRefreshTestEvent(t, started, "third refresh to start")
	if third == first || third == second {
		t.Fatalf("third refresh = %q, want the remaining project", third)
	}
	release <- struct{}{}
	if got := waitForProjectRefreshTestEvent(t, completed, "third refresh to complete"); got != third {
		t.Fatalf("third completed refresh = %q, want %q", got, third)
	}
}

func TestProjectRefreshAsyncPromotesQueuedStatusRefresh(t *testing.T) {
	t.Parallel()

	attentionStarted := make(chan struct{}, 1)
	releaseAttention := make(chan struct{})
	statusStarted := make(chan struct{}, 1)
	svc := &Service{
		refreshProjectAttentionFn: func(context.Context, string) error {
			attentionStarted <- struct{}{}
			<-releaseAttention
			return nil
		},
		refreshProjectStatusFn: func(context.Context, string) error {
			statusStarted <- struct{}{}
			return nil
		},
	}

	const projectPath = "/tmp/demo"
	svc.refreshProjectAttentionAsync(projectPath)
	select {
	case <-attentionStarted:
	case <-time.After(time.Second):
		t.Fatal("attention refresh did not start")
	}
	svc.refreshProjectStatusAsync(projectPath)
	select {
	case <-statusStarted:
		t.Fatal("status refresh started before the in-flight attention refresh completed")
	case <-time.After(100 * time.Millisecond):
	}

	close(releaseAttention)
	select {
	case <-statusStarted:
	case <-time.After(time.Second):
		t.Fatal("queued status refresh did not run after attention refresh")
	}
}

func TestProjectRefreshAsyncCoalescesRequestsWhileWaitingForSlot(t *testing.T) {
	t.Parallel()

	blockerStarted := make(chan struct{}, 1)
	releaseBlocker := make(chan struct{})
	targetRuns := make(chan string, 2)
	svc := &Service{
		refreshProjectAttentionFn: func(_ context.Context, projectPath string) error {
			if projectPath == "/tmp/blocker" {
				blockerStarted <- struct{}{}
				<-releaseBlocker
				return nil
			}
			targetRuns <- "attention"
			return nil
		},
		refreshProjectStatusFn: func(context.Context, string) error {
			targetRuns <- "status"
			return nil
		},
	}

	svc.refreshProjectAttentionAsync("/tmp/blocker")
	select {
	case <-blockerStarted:
	case <-time.After(time.Second):
		t.Fatal("blocking refresh did not start")
	}

	const targetPath = "/tmp/target"
	svc.refreshProjectAttentionAsync(targetPath)
	svc.refreshProjectStatusAsync(targetPath)
	svc.refreshProjectAttentionAsync(targetPath)
	close(releaseBlocker)

	select {
	case got := <-targetRuns:
		if got != "status" {
			t.Fatalf("coalesced target refresh = %q, want status", got)
		}
	case <-time.After(time.Second):
		t.Fatal("coalesced target refresh did not start")
	}
	select {
	case got := <-targetRuns:
		t.Fatalf("unexpected redundant target refresh %q", got)
	case <-time.After(150 * time.Millisecond):
	}
}

func waitForProjectRefreshTestEvent(t *testing.T, ch <-chan string, description string) string {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", description)
		return ""
	}
}

func TestRecordEmbeddedSessionActivityRespectsContextWhileProjectLockHeld(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	projectPath := filepath.Join(root, "project")
	if err := os.MkdirAll(projectPath, 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}
	st, err := store.Open(filepath.Join(root, "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	now := time.Now()
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:          projectPath,
		Name:          "project",
		PresentOnDisk: true,
		InScope:       true,
		CreatedAt:     now.Add(-time.Hour),
		UpdatedAt:     now.Add(-time.Hour),
	}); err != nil {
		t.Fatalf("seed project: %v", err)
	}

	svc := New(config.Default(), st, events.NewBus(), nil)
	unlock := svc.lockProjectStateMutation(projectPath)
	released := false
	release := func() {
		if released {
			return
		}
		released = true
		unlock()
	}
	defer release()

	waitCtx, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- svc.RecordEmbeddedSessionActivity(waitCtx, EmbeddedSessionActivity{
			ProjectPath:    projectPath,
			Source:         model.SessionSourceCodex,
			SessionID:      "blocked-session",
			Format:         "modern",
			LastActivityAt: now,
		})
	}()

	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("RecordEmbeddedSessionActivity() error = %v, want deadline exceeded", err)
		}
	case <-time.After(500 * time.Millisecond):
		release()
		t.Fatal("RecordEmbeddedSessionActivity() did not respect context while waiting for project lock")
	}
}

func TestRefreshProjectStatusDoesNotWaitForLongScan(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	projectPath := filepath.Join(t.TempDir(), "demo-project")
	if err := os.MkdirAll(projectPath, 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:           projectPath,
		Name:           filepath.Base(projectPath),
		Status:         model.StatusIdle,
		AttentionScore: 1,
		PresentOnDisk:  true,
		InScope:        true,
		UpdatedAt:      time.Now(),
	}); err != nil {
		t.Fatalf("upsert project: %v", err)
	}

	started := make(chan struct{}, 1)
	release := make(chan struct{})
	cfg := config.Default()
	cfg.IncludePaths = nil
	svc := &Service{
		cfg:       cfg,
		store:     st,
		bus:       events.NewBus(),
		detectors: []detectors.Detector{blockingDetector{started: started, release: release}},
	}

	scanDone := make(chan error, 1)
	go func() {
		_, err := svc.ScanWithOptions(ctx, ScanOptions{})
		scanDone <- err
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("scan did not reach blocking detector")
	}

	refreshDone := make(chan error, 1)
	go func() {
		refreshDone <- svc.RefreshProjectStatus(ctx, projectPath)
	}()

	select {
	case err := <-refreshDone:
		if err != nil {
			t.Fatalf("RefreshProjectStatus() error = %v", err)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("RefreshProjectStatus blocked behind ScanWithOptions")
	}

	close(release)

	select {
	case err := <-scanDone:
		if err != nil {
			t.Fatalf("ScanWithOptions() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("scan did not finish after detector release")
	}
}

func TestRecordEmbeddedSessionActivityClearsStaleStuckState(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	now := time.Now().UTC().Truncate(time.Minute)
	staleAt := now.Add(-time.Hour)
	projectPath := filepath.Join(t.TempDir(), "demo")
	if err := os.MkdirAll(projectPath, 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}

	session := model.NormalizeSessionEvidenceIdentity(model.SessionEvidence{
		Source:               model.SessionSourceCodex,
		SessionID:            "codex:thread-demo",
		RawSessionID:         "thread-demo",
		ProjectPath:          projectPath,
		DetectedProjectPath:  projectPath,
		Format:               "modern",
		SnapshotHash:         "stale-snapshot",
		StartedAt:            staleAt.Add(-10 * time.Minute),
		LastEventAt:          staleAt,
		LatestTurnStartedAt:  staleAt.Add(-5 * time.Minute),
		LatestTurnStateKnown: true,
		LatestTurnCompleted:  false,
	})
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:           projectPath,
		Name:           "demo",
		LastActivity:   staleAt,
		Status:         model.StatusPossiblyStuck,
		AttentionScore: 40,
		PresentOnDisk:  true,
		InScope:        true,
		Sessions:       []model.SessionEvidence{session},
		AttentionReason: []model.AttentionReason{{
			Code:   "blocked",
			Text:   "Latest session is blocked; idle for 1h",
			Weight: 40,
		}},
		CreatedAt: now.Add(-2 * time.Hour),
		UpdatedAt: staleAt,
	}); err != nil {
		t.Fatalf("seed project: %v", err)
	}

	svc := New(config.Default(), st, events.NewBus(), nil)
	if err := svc.RecordEmbeddedSessionActivity(ctx, EmbeddedSessionActivity{
		ProjectPath:          projectPath,
		Source:               model.SessionSourceCodex,
		SessionID:            "thread-demo",
		Format:               "modern",
		LastActivityAt:       now,
		LatestTurnStartedAt:  staleAt.Add(-5 * time.Minute),
		LatestTurnStateKnown: true,
		LatestTurnCompleted:  false,
	}); err != nil {
		t.Fatalf("RecordEmbeddedSessionActivity() error = %v", err)
	}

	detail, err := st.GetProjectDetail(ctx, projectPath, 20)
	if err != nil {
		t.Fatalf("get detail: %v", err)
	}
	if !detail.Summary.LastActivity.Equal(now) {
		t.Fatalf("last activity = %v, want %v", detail.Summary.LastActivity, now)
	}
	if detail.Summary.Status != model.StatusActive {
		t.Fatalf("status = %q, want active", detail.Summary.Status)
	}
	if len(detail.Sessions) == 0 || !detail.Sessions[0].LastEventAt.Equal(now) {
		t.Fatalf("latest session = %#v, want last event %v", detail.Sessions, now)
	}
	if detail.Sessions[0].SnapshotHash != "" {
		t.Fatalf("snapshot hash = %q, want cleared after live activity", detail.Sessions[0].SnapshotHash)
	}
	for _, reason := range detail.Reasons {
		if reason.Code == "blocked" {
			t.Fatalf("stale blocked reason should be cleared, got %#v", detail.Reasons)
		}
	}
}

func TestEmbeddedSessionActivityUpdatesLinkedTodoWorkState(t *testing.T) {
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
	now := time.Now().UTC().Truncate(time.Second)
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:          projectPath,
		Name:          "demo",
		Status:        model.StatusIdle,
		PresentOnDisk: true,
		InScope:       true,
		UpdatedAt:     now,
	}); err != nil {
		t.Fatalf("seed project: %v", err)
	}

	svc := New(config.Default(), st, events.NewBus(), nil)
	todo, err := st.AddTodo(ctx, projectPath, "Let the coordinator track this")
	if err != nil {
		t.Fatalf("add todo: %v", err)
	}
	if err := svc.MarkTodoWorkStarted(ctx, projectPath, todo.ID, model.SessionSourceCodex, "thread-demo", now); err != nil {
		t.Fatalf("MarkTodoWorkStarted() error = %v", err)
	}

	started, err := st.GetTodo(ctx, todo.ID)
	if err != nil {
		t.Fatalf("get started todo: %v", err)
	}
	if started.WorkSessionID != "codex:thread-demo" || started.WorkState != model.TodoWorkStateWorking {
		t.Fatalf("started todo work = session:%q state:%q, want codex:thread-demo/working", started.WorkSessionID, started.WorkState)
	}

	waitingAt := now.Add(3 * time.Minute)
	if err := svc.RecordEmbeddedSessionActivity(ctx, EmbeddedSessionActivity{
		ProjectPath:          projectPath,
		Source:               model.SessionSourceCodex,
		SessionID:            "thread-demo",
		Format:               "modern",
		LastActivityAt:       waitingAt,
		LatestTurnStartedAt:  now,
		LatestTurnStateKnown: true,
		LatestTurnCompleted:  false,
		WorkState:            model.TodoWorkStateWaiting,
	}); err != nil {
		t.Fatalf("RecordEmbeddedSessionActivity() error = %v", err)
	}

	updated, err := st.GetTodo(ctx, todo.ID)
	if err != nil {
		t.Fatalf("get updated todo: %v", err)
	}
	if updated.WorkState != model.TodoWorkStateWaiting || !updated.WorkStateAt.Equal(waitingAt) {
		t.Fatalf("updated todo work = state:%q at:%v, want waiting at %v", updated.WorkState, updated.WorkStateAt, waitingAt)
	}
}

func TestEmbeddedSessionActivityUpdatesTodoPinnedToWorktreeSession(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	rootPath := filepath.Join(t.TempDir(), "demo")
	worktreePath := filepath.Join(t.TempDir(), "demo--feat-pinned-todo")
	if err := os.MkdirAll(rootPath, 0o755); err != nil {
		t.Fatalf("mkdir root: %v", err)
	}
	if err := os.MkdirAll(worktreePath, 0o755); err != nil {
		t.Fatalf("mkdir worktree: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:             rootPath,
		Name:             "demo",
		Status:           model.StatusIdle,
		PresentOnDisk:    true,
		InScope:          true,
		WorktreeRootPath: rootPath,
		WorktreeKind:     model.WorktreeKindMain,
		UpdatedAt:        now,
	}); err != nil {
		t.Fatalf("seed root project: %v", err)
	}
	todo, err := st.AddTodo(ctx, rootPath, "Track work in a dedicated worktree")
	if err != nil {
		t.Fatalf("add todo: %v", err)
	}
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:                 worktreePath,
		Name:                 "demo--feat-pinned-todo",
		Status:               model.StatusIdle,
		PresentOnDisk:        true,
		InScope:              true,
		WorktreeRootPath:     rootPath,
		WorktreeKind:         model.WorktreeKindLinked,
		WorktreeOriginTodoID: todo.ID,
		UpdatedAt:            now,
	}); err != nil {
		t.Fatalf("seed worktree project: %v", err)
	}

	svc := New(config.Default(), st, events.NewBus(), nil)
	if err := svc.MarkTodoWorkStarted(ctx, worktreePath, todo.ID, model.SessionSourceCodex, "thread-worktree", now); err != nil {
		t.Fatalf("MarkTodoWorkStarted() error = %v", err)
	}
	started, err := st.GetTodo(ctx, todo.ID)
	if err != nil {
		t.Fatalf("get started todo: %v", err)
	}
	if started.WorkProjectPath != worktreePath || started.WorkSessionID != "codex:thread-worktree" {
		t.Fatalf("started todo work = project:%q session:%q, want %q/codex:thread-worktree", started.WorkProjectPath, started.WorkSessionID, worktreePath)
	}

	waitingAt := now.Add(2 * time.Minute)
	if err := svc.RecordEmbeddedSessionActivity(ctx, EmbeddedSessionActivity{
		ProjectPath:          worktreePath,
		Source:               model.SessionSourceCodex,
		SessionID:            "thread-worktree",
		Format:               "modern",
		LastActivityAt:       waitingAt,
		LatestTurnStartedAt:  now,
		LatestTurnStateKnown: true,
		LatestTurnCompleted:  false,
		WorkState:            model.TodoWorkStateWaiting,
	}); err != nil {
		t.Fatalf("RecordEmbeddedSessionActivity() error = %v", err)
	}
	updated, err := st.GetTodo(ctx, todo.ID)
	if err != nil {
		t.Fatalf("get updated todo: %v", err)
	}
	if updated.WorkState != model.TodoWorkStateWaiting || !updated.WorkStateAt.Equal(waitingAt) {
		t.Fatalf("updated todo work = state:%q at:%v, want waiting at %v", updated.WorkState, updated.WorkStateAt, waitingAt)
	}
}

func TestRecordEmbeddedSessionActivityMarksTurnSettledAtSameLastEvent(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	now := time.Now().UTC().Truncate(time.Minute)
	projectPath := filepath.Join(t.TempDir(), "demo")
	if err := os.MkdirAll(projectPath, 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}

	session := model.NormalizeSessionEvidenceIdentity(model.SessionEvidence{
		Source:               model.SessionSourceCodex,
		SessionID:            "codex:thread-demo",
		RawSessionID:         "thread-demo",
		ProjectPath:          projectPath,
		DetectedProjectPath:  projectPath,
		Format:               "modern",
		StartedAt:            now.Add(-10 * time.Minute),
		LastEventAt:          now,
		LatestTurnStartedAt:  now.Add(-5 * time.Minute),
		LatestTurnStateKnown: true,
		LatestTurnCompleted:  false,
	})
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:           projectPath,
		Name:           "demo",
		LastActivity:   now,
		Status:         model.StatusActive,
		AttentionScore: 20,
		PresentOnDisk:  true,
		InScope:        true,
		Sessions:       []model.SessionEvidence{session},
		CreatedAt:      now.Add(-2 * time.Hour),
		UpdatedAt:      now,
	}); err != nil {
		t.Fatalf("seed project: %v", err)
	}

	svc := New(config.Default(), st, events.NewBus(), nil)
	if err := svc.RecordEmbeddedSessionActivity(ctx, EmbeddedSessionActivity{
		ProjectPath:          projectPath,
		Source:               model.SessionSourceCodex,
		SessionID:            "thread-demo",
		Format:               "modern",
		LastActivityAt:       now,
		LatestTurnStartedAt:  now.Add(-5 * time.Minute),
		LatestTurnStateKnown: true,
		LatestTurnCompleted:  true,
	}); err != nil {
		t.Fatalf("RecordEmbeddedSessionActivity() error = %v", err)
	}

	detail, err := st.GetProjectDetail(ctx, projectPath, 20)
	if err != nil {
		t.Fatalf("get detail: %v", err)
	}
	if len(detail.Sessions) == 0 {
		t.Fatalf("expected stored session")
	}
	if !detail.Sessions[0].LatestTurnStateKnown || !detail.Sessions[0].LatestTurnCompleted {
		t.Fatalf("turn state = known:%t completed:%t, want settled turn", detail.Sessions[0].LatestTurnStateKnown, detail.Sessions[0].LatestTurnCompleted)
	}
}

func TestRefreshProjectStatusPreservesEmbeddedActivityRecordedDuringGitMetadataRead(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	projectPath := filepath.Join(root, "project")
	if err := os.MkdirAll(filepath.Join(projectPath, ".git"), 0o755); err != nil {
		t.Fatalf("create git marker: %v", err)
	}
	st, err := store.Open(filepath.Join(root, "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	seededAt := time.Now().Add(-time.Hour)
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:          projectPath,
		Name:          "project",
		PresentOnDisk: true,
		InScope:       true,
		CreatedAt:     seededAt,
		UpdatedAt:     seededAt,
	}); err != nil {
		t.Fatalf("seed project: %v", err)
	}

	svc := New(config.Default(), st, events.NewBus(), nil)
	svc.gitWorktreeInfoReader = func(context.Context, string) (scanner.GitWorktreeInfo, error) {
		return scanner.GitWorktreeInfo{}, nil
	}
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	svc.gitRepoStatusReader = func(context.Context, string) (scanner.GitRepoStatus, error) {
		once.Do(func() { close(started) })
		<-release
		return scanner.GitRepoStatus{Branch: "master"}, nil
	}

	refreshDone := make(chan error, 1)
	go func() {
		refreshDone <- svc.RefreshProjectStatus(ctx, projectPath)
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("refresh never reached git status read")
	}

	activityAt := time.Now()
	activityDone := make(chan error, 1)
	go func() {
		activityDone <- svc.RecordEmbeddedSessionActivity(ctx, EmbeddedSessionActivity{
			ProjectPath:          projectPath,
			Source:               model.SessionSourceCodex,
			SessionID:            "live-session",
			Format:               "modern",
			StartedAt:            activityAt.Add(-time.Minute),
			LastActivityAt:       activityAt,
			LatestTurnStartedAt:  activityAt,
			LatestTurnStateKnown: true,
			LatestTurnCompleted:  false,
		})
	}()

	select {
	case err := <-activityDone:
		if err != nil {
			t.Fatalf("record embedded activity: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("embedded activity blocked behind slow refresh metadata read")
	}

	select {
	case err := <-refreshDone:
		t.Fatalf("refresh completed before git metadata read was released: %v", err)
	case <-time.After(150 * time.Millisecond):
	}

	close(release)
	select {
	case err := <-refreshDone:
		if err != nil {
			t.Fatalf("refresh project status: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("refresh did not finish")
	}
	detail, err := st.GetProjectDetail(ctx, projectPath, 20)
	if err != nil {
		t.Fatalf("get project detail: %v", err)
	}
	if len(detail.Sessions) != 1 {
		t.Fatalf("sessions = %#v, want recorded live session preserved", detail.Sessions)
	}
	if detail.Sessions[0].SessionID != "codex:live-session" {
		t.Fatalf("session id = %q, want codex:live-session", detail.Sessions[0].SessionID)
	}
	if detail.Summary.RepoBranch != "master" {
		t.Fatalf("repo branch = %q, want master", detail.Summary.RepoBranch)
	}
}

func TestRefreshProjectStatusPreservesPinToggledDuringGitMetadataRead(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	projectPath := filepath.Join(root, "project")
	if err := os.MkdirAll(filepath.Join(projectPath, ".git"), 0o755); err != nil {
		t.Fatalf("create git marker: %v", err)
	}
	st, err := store.Open(filepath.Join(root, "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	seededAt := time.Now().Add(-time.Hour)
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:          projectPath,
		Name:          "project",
		PresentOnDisk: true,
		InScope:       true,
		CreatedAt:     seededAt,
		UpdatedAt:     seededAt,
	}); err != nil {
		t.Fatalf("seed project: %v", err)
	}

	svc := New(config.Default(), st, events.NewBus(), nil)
	svc.gitWorktreeInfoReader = func(context.Context, string) (scanner.GitWorktreeInfo, error) {
		return scanner.GitWorktreeInfo{}, nil
	}
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	svc.gitRepoStatusReader = func(context.Context, string) (scanner.GitRepoStatus, error) {
		once.Do(func() { close(started) })
		<-release
		return scanner.GitRepoStatus{Branch: "master"}, nil
	}

	refreshDone := make(chan error, 1)
	go func() {
		refreshDone <- svc.RefreshProjectStatus(ctx, projectPath)
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("refresh never reached git status read")
	}

	toggleDone := make(chan error, 1)
	go func() {
		toggleDone <- svc.TogglePin(ctx, projectPath)
	}()

	select {
	case err := <-toggleDone:
		if err != nil {
			t.Fatalf("toggle pin: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("toggle pin blocked behind slow refresh metadata read")
	}

	detail, err := st.GetProjectDetail(ctx, projectPath, 20)
	if err != nil {
		t.Fatalf("get project detail after toggle: %v", err)
	}
	if !detail.Summary.Pinned {
		t.Fatal("project should be pinned while refresh metadata read is still blocked")
	}

	select {
	case err := <-refreshDone:
		t.Fatalf("refresh completed before git metadata read was released: %v", err)
	case <-time.After(150 * time.Millisecond):
	}

	close(release)
	select {
	case err := <-refreshDone:
		if err != nil {
			t.Fatalf("refresh project status: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("refresh did not finish")
	}
	detail, err = st.GetProjectDetail(ctx, projectPath, 20)
	if err != nil {
		t.Fatalf("get final project detail: %v", err)
	}
	if !detail.Summary.Pinned {
		t.Fatal("project should be pinned after toggle")
	}
	if detail.Summary.RepoBranch != "master" {
		t.Fatalf("repo branch = %q, want master", detail.Summary.RepoBranch)
	}
}

func TestScanPreservesEmbeddedActivityRecordedDuringScan(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	projectPath := filepath.Join(root, "project")
	if err := os.MkdirAll(projectPath, 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}
	st, err := store.Open(filepath.Join(root, "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	seededAt := time.Now().Add(-time.Hour)
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:          projectPath,
		Name:          "project",
		PresentOnDisk: true,
		InScope:       true,
		CreatedAt:     seededAt,
		UpdatedAt:     seededAt,
	}); err != nil {
		t.Fatalf("seed project: %v", err)
	}

	started := make(chan struct{}, 1)
	release := make(chan struct{})
	cfg := config.Default()
	cfg.IncludePaths = nil
	svc := &Service{
		cfg:       cfg,
		store:     st,
		bus:       events.NewBus(),
		detectors: []detectors.Detector{blockingDetector{started: started, release: release}},
	}

	scanDone := make(chan error, 1)
	go func() {
		_, err := svc.ScanWithOptions(ctx, ScanOptions{})
		scanDone <- err
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("scan did not reach blocking detector")
	}

	activityAt := time.Now()
	if err := svc.RecordEmbeddedSessionActivity(ctx, EmbeddedSessionActivity{
		ProjectPath:          projectPath,
		Source:               model.SessionSourceCodex,
		SessionID:            "scan-live-session",
		Format:               "modern",
		StartedAt:            activityAt.Add(-time.Minute),
		LastActivityAt:       activityAt,
		LatestTurnStartedAt:  activityAt,
		LatestTurnStateKnown: true,
		LatestTurnCompleted:  false,
	}); err != nil {
		t.Fatalf("record embedded activity: %v", err)
	}

	close(release)
	select {
	case err := <-scanDone:
		if err != nil {
			t.Fatalf("scan: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("scan did not finish")
	}

	detail, err := st.GetProjectDetail(ctx, projectPath, 20)
	if err != nil {
		t.Fatalf("get detail: %v", err)
	}
	if len(detail.Sessions) != 1 {
		t.Fatalf("sessions = %#v, want live session preserved", detail.Sessions)
	}
	if detail.Sessions[0].SessionID != "codex:scan-live-session" {
		t.Fatalf("session id = %q, want codex:scan-live-session", detail.Sessions[0].SessionID)
	}
}

func TestRecordEmbeddedSessionActivityQueuesClassificationForOpenCodeLiveSession(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	now := time.Now().UTC().Truncate(time.Minute)
	projectPath := filepath.Join(t.TempDir(), "demo")
	if err := os.MkdirAll(projectPath, 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}

	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:           projectPath,
		Name:           "demo",
		LastActivity:   now.Add(-10 * time.Minute),
		Status:         model.StatusIdle,
		AttentionScore: 5,
		PresentOnDisk:  true,
		InScope:        true,
		CreatedAt:      now.Add(-2 * time.Hour),
		UpdatedAt:      now.Add(-10 * time.Minute),
	}); err != nil {
		t.Fatalf("seed project: %v", err)
	}

	cfg := config.Default()
	cfg.OpenCodeHome = filepath.Join(t.TempDir(), "opencode-home")
	if err := os.MkdirAll(cfg.OpenCodeHome, 0o755); err != nil {
		t.Fatalf("mkdir opencode home: %v", err)
	}

	classifier := &recordingClassifier{}
	svc := New(cfg, st, events.NewBus(), nil)
	svc.SetSessionClassifier(classifier)

	if err := svc.RecordEmbeddedSessionActivity(ctx, EmbeddedSessionActivity{
		ProjectPath:          projectPath,
		Source:               model.SessionSourceOpenCode,
		SessionID:            "ses-demo",
		Format:               "opencode_db",
		LastActivityAt:       now,
		LatestTurnStartedAt:  now.Add(-2 * time.Minute),
		LatestTurnStateKnown: true,
		LatestTurnCompleted:  true,
	}); err != nil {
		t.Fatalf("RecordEmbeddedSessionActivity() error = %v", err)
	}

	if classifier.normalCalls != 1 {
		t.Fatalf("QueueProject() calls = %d, want 1", classifier.normalCalls)
	}
	if classifier.notifyCalls != 1 {
		t.Fatalf("Notify() calls = %d, want 1", classifier.notifyCalls)
	}
	if len(classifier.lastState.Sessions) != 1 {
		t.Fatalf("queued state sessions = %#v, want one session", classifier.lastState.Sessions)
	}
	wantSessionFile := filepath.Join(cfg.OpenCodeHome, "opencode.db") + "#session:ses-demo"
	if got := classifier.lastState.Sessions[0].SessionFile; got != wantSessionFile {
		t.Fatalf("queued session file = %q, want %q", got, wantSessionFile)
	}

	detail, err := st.GetProjectDetail(ctx, projectPath, 20)
	if err != nil {
		t.Fatalf("get detail: %v", err)
	}
	if len(detail.Sessions) != 1 {
		t.Fatalf("stored sessions = %#v, want one session", detail.Sessions)
	}
	if got := detail.Sessions[0].SessionFile; got != wantSessionFile {
		t.Fatalf("stored session file = %q, want %q", got, wantSessionFile)
	}
}

func TestRecordEmbeddedSessionActivityQueuesClassificationForCodexLiveSession(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	now := time.Date(2026, 4, 17, 15, 12, 53, 0, time.UTC)
	projectPath := filepath.Join(t.TempDir(), "demo")
	if err := os.MkdirAll(projectPath, 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}

	cfg := config.Default()
	cfg.CodexHome = filepath.Join(t.TempDir(), ".codex")
	sessionID := "019d9c00-851d-7033-8291-b0c6c7525753"
	sessionDir := filepath.Join(cfg.CodexHome, "sessions", "2026", "04", "17")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatalf("mkdir codex session dir: %v", err)
	}
	fixture := filepath.Clean(filepath.Join("..", "..", "testdata", "codex_footprint", "sessions", "2026", "03", "05", "rollout-modern.jsonl"))
	fixtureData, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	wantSessionFile := filepath.Join(sessionDir, "rollout-2026-04-17T23-12-53-"+sessionID+".jsonl")
	if err := os.WriteFile(wantSessionFile, fixtureData, 0o644); err != nil {
		t.Fatalf("write codex session file: %v", err)
	}

	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:           projectPath,
		Name:           "demo",
		LastActivity:   now.Add(-10 * time.Minute),
		Status:         model.StatusIdle,
		AttentionScore: 5,
		PresentOnDisk:  true,
		InScope:        true,
		CreatedAt:      now.Add(-2 * time.Hour),
		UpdatedAt:      now.Add(-10 * time.Minute),
	}); err != nil {
		t.Fatalf("seed project: %v", err)
	}

	classifier := &recordingClassifier{}
	svc := New(cfg, st, events.NewBus(), nil)
	svc.SetSessionClassifier(classifier)

	if err := svc.RecordEmbeddedSessionActivity(ctx, EmbeddedSessionActivity{
		ProjectPath:          projectPath,
		Source:               model.SessionSourceCodex,
		SessionID:            sessionID,
		Format:               "modern",
		LastActivityAt:       now,
		LatestTurnStartedAt:  now.Add(-2 * time.Minute),
		LatestTurnStateKnown: true,
		LatestTurnCompleted:  true,
	}); err != nil {
		t.Fatalf("RecordEmbeddedSessionActivity() error = %v", err)
	}

	if classifier.normalCalls != 1 {
		t.Fatalf("QueueProject() calls = %d, want 1", classifier.normalCalls)
	}
	if classifier.notifyCalls != 1 {
		t.Fatalf("Notify() calls = %d, want 1", classifier.notifyCalls)
	}
	if len(classifier.lastState.Sessions) != 1 {
		t.Fatalf("queued state sessions = %#v, want one session", classifier.lastState.Sessions)
	}
	if got := classifier.lastState.Sessions[0].SessionFile; got != wantSessionFile {
		t.Fatalf("queued session file = %q, want %q", got, wantSessionFile)
	}

	detail, err := st.GetProjectDetail(ctx, projectPath, 20)
	if err != nil {
		t.Fatalf("get detail: %v", err)
	}
	if len(detail.Sessions) != 1 {
		t.Fatalf("stored sessions = %#v, want one session", detail.Sessions)
	}
	if got := detail.Sessions[0].SessionFile; got != wantSessionFile {
		t.Fatalf("stored session file = %q, want %q", got, wantSessionFile)
	}
}

func TestResolveEmbeddedSessionFileFindsLCAgentSessionFile(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	now := time.Date(2026, 5, 30, 2, 3, 4, 0, time.UTC)
	sessionID := "lca_resolve"
	sessionDir := filepath.Join(cfg.DataDir, "lcagent", "sessions", "2026", "05", "30")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatalf("mkdir lcagent session dir: %v", err)
	}
	want := filepath.Join(sessionDir, sessionID+".jsonl")
	if err := os.WriteFile(want, []byte(`{"type":"session_meta","id":"lca_resolve"}`+"\n"), 0o644); err != nil {
		t.Fatalf("write lcagent session file: %v", err)
	}

	if got := embeddedActivityDefaultFormat(model.SessionSourceLCAgent); got != "lcagent_jsonl" {
		t.Fatalf("embeddedActivityDefaultFormat(LCAgent) = %q, want lcagent_jsonl", got)
	}
	got := resolveEmbeddedSessionFile(model.SessionSourceLCAgent, "lcagent:"+sessionID, "", now, now, cfg)
	if got != want {
		t.Fatalf("resolveEmbeddedSessionFile() = %q, want %q", got, want)
	}
}

func TestRefreshProjectStatusBackfillsCodexSessionFileFromCodexHome(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	now := time.Date(2026, 4, 17, 15, 12, 53, 0, time.UTC)
	projectPath := filepath.Join(t.TempDir(), "demo")
	if err := os.MkdirAll(projectPath, 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}

	cfg := config.Default()
	cfg.CodexHome = filepath.Join(t.TempDir(), ".codex")
	sessionID := "019d9c00-851d-7033-8291-b0c6c7525753"
	sessionDir := filepath.Join(cfg.CodexHome, "sessions", "2026", "04", "17")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatalf("mkdir codex session dir: %v", err)
	}
	fixture := filepath.Clean(filepath.Join("..", "..", "testdata", "codex_footprint", "sessions", "2026", "03", "05", "rollout-modern.jsonl"))
	fixtureData, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	wantSessionFile := filepath.Join(sessionDir, "rollout-2026-04-17T23-12-53-"+sessionID+".jsonl")
	if err := os.WriteFile(wantSessionFile, fixtureData, 0o644); err != nil {
		t.Fatalf("write codex session file: %v", err)
	}

	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:           projectPath,
		Name:           "demo",
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
		t.Fatalf("seed project: %v", err)
	}

	classifier := &recordingClassifier{}
	svc := New(cfg, st, events.NewBus(), nil)
	svc.SetSessionClassifier(classifier)
	if err := svc.RefreshProjectStatus(ctx, projectPath); err != nil {
		t.Fatalf("RefreshProjectStatus() error = %v", err)
	}
	if classifier.normalCalls != 1 {
		t.Fatalf("QueueProject() calls = %d, want 1", classifier.normalCalls)
	}
	if classifier.notifyCalls != 1 {
		t.Fatalf("Notify() calls = %d, want 1", classifier.notifyCalls)
	}
	if len(classifier.lastState.Sessions) != 1 {
		t.Fatalf("queued state sessions = %#v, want one session", classifier.lastState.Sessions)
	}
	if got := classifier.lastState.Sessions[0].SessionFile; got != wantSessionFile {
		t.Fatalf("queued session file = %q, want %q", got, wantSessionFile)
	}
	if strings.TrimSpace(classifier.lastState.Sessions[0].SnapshotHash) == "" {
		t.Fatalf("expected queued session snapshot hash after refresh")
	}

	detail, err := st.GetProjectDetail(ctx, projectPath, 20)
	if err != nil {
		t.Fatalf("get detail: %v", err)
	}
	if len(detail.Sessions) != 1 {
		t.Fatalf("stored sessions = %#v, want one session", detail.Sessions)
	}
	if got := detail.Sessions[0].SessionFile; got != wantSessionFile {
		t.Fatalf("stored session file = %q, want %q", got, wantSessionFile)
	}
	if strings.TrimSpace(detail.Sessions[0].SnapshotHash) == "" {
		t.Fatalf("expected stored session snapshot hash after refresh")
	}
}

func TestRefreshProjectStatusBackfillsCodexSessionFileFromOverlayCodexHome(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	now := time.Date(2026, 4, 17, 15, 12, 53, 0, time.UTC)
	projectPath := filepath.Join(t.TempDir(), "demo")
	if err := os.MkdirAll(projectPath, 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}

	sourceHome := filepath.Join(t.TempDir(), ".codex")
	sessionID := "019d9c00-851d-7033-8291-b0c6c7525753"
	sessionDir := filepath.Join(sourceHome, "sessions", "2026", "04", "17")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatalf("mkdir codex session dir: %v", err)
	}
	fixture := filepath.Clean(filepath.Join("..", "..", "testdata", "codex_footprint", "sessions", "2026", "03", "05", "rollout-modern.jsonl"))
	fixtureData, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	wantSessionFile := filepath.Join(sessionDir, "rollout-2026-04-17T23-12-53-"+sessionID+".jsonl")
	if err := os.WriteFile(wantSessionFile, fixtureData, 0o644); err != nil {
		t.Fatalf("write codex session file: %v", err)
	}

	overlayHome := filepath.Join(t.TempDir(), "internal-workspaces", "lcroom-codex-home-9999")
	if err := os.MkdirAll(overlayHome, 0o755); err != nil {
		t.Fatalf("mkdir overlay home: %v", err)
	}
	if err := os.Symlink(filepath.Join(sourceHome, "sessions"), filepath.Join(overlayHome, "sessions")); err != nil {
		t.Fatalf("symlink sessions: %v", err)
	}

	cfg := config.Default()
	cfg.CodexHome = overlayHome

	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:           projectPath,
		Name:           "demo",
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
		t.Fatalf("seed project: %v", err)
	}

	classifier := &recordingClassifier{}
	svc := New(cfg, st, events.NewBus(), nil)
	svc.SetSessionClassifier(classifier)
	if err := svc.RefreshProjectStatus(ctx, projectPath); err != nil {
		t.Fatalf("RefreshProjectStatus() error = %v", err)
	}
	if len(classifier.lastState.Sessions) != 1 {
		t.Fatalf("queued state sessions = %#v, want one session", classifier.lastState.Sessions)
	}
	if got := classifier.lastState.Sessions[0].SessionFile; got != wantSessionFile {
		t.Fatalf("queued session file = %q, want %q", got, wantSessionFile)
	}

	detail, err := st.GetProjectDetail(ctx, projectPath, 20)
	if err != nil {
		t.Fatalf("get detail: %v", err)
	}
	if len(detail.Sessions) != 1 {
		t.Fatalf("stored sessions = %#v, want one session", detail.Sessions)
	}
	if got := detail.Sessions[0].SessionFile; got != wantSessionFile {
		t.Fatalf("stored session file = %q, want %q", got, wantSessionFile)
	}
}

func TestRefreshProjectStatusWithOptionsForceRetriesFailedClassifications(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	now := time.Date(2026, 4, 17, 15, 12, 53, 0, time.UTC)
	projectPath := filepath.Join(t.TempDir(), "demo")
	if err := os.MkdirAll(projectPath, 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}

	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:           projectPath,
		Name:           "demo",
		LastActivity:   now,
		Status:         model.StatusIdle,
		AttentionScore: 5,
		PresentOnDisk:  true,
		InScope:        true,
		CreatedAt:      now.Add(-2 * time.Hour),
		UpdatedAt:      now,
		Sessions: []model.SessionEvidence{{
			Source:               model.SessionSourceCodex,
			SessionID:            "codex:ses_force_refresh",
			RawSessionID:         "ses_force_refresh",
			ProjectPath:          projectPath,
			DetectedProjectPath:  projectPath,
			SessionFile:          filepath.Join(projectPath, "session.jsonl"),
			Format:               "modern",
			SnapshotHash:         "hash-force-refresh",
			LastEventAt:          now,
			LatestTurnStateKnown: true,
			LatestTurnCompleted:  false,
		}},
	}); err != nil {
		t.Fatalf("seed project: %v", err)
	}

	classifier := &recordingClassifier{}
	svc := New(config.Default(), st, events.NewBus(), nil)
	svc.SetSessionClassifier(classifier)
	svc.gitFingerprintReader = nil
	svc.gitRepoStatusReader = nil

	if err := svc.RefreshProjectStatusWithOptions(ctx, projectPath, ScanOptions{ForceRetryFailedClassifications: true}); err != nil {
		t.Fatalf("RefreshProjectStatusWithOptions() error = %v", err)
	}
	if classifier.forcedCalls != 1 {
		t.Fatalf("forcedCalls = %d, want 1", classifier.forcedCalls)
	}
	if classifier.normalCalls != 0 {
		t.Fatalf("normalCalls = %d, want 0", classifier.normalCalls)
	}
	if classifier.notifyCalls != 1 {
		t.Fatalf("notifyCalls = %d, want 1", classifier.notifyCalls)
	}
}
