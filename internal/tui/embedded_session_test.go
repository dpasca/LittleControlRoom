package tui

import (
	"testing"
	"time"

	"lcroom/internal/codexapp"
	"lcroom/internal/codexcli"
	"lcroom/internal/model"

	"github.com/charmbracelet/bubbles/viewport"
)

func TestEmbeddedSnapshotHelpersUseCachedStateWithoutTranscriptSnapshot(t *testing.T) {
	approval := &codexapp.ApprovalRequest{
		ID:       "approval-1",
		Kind:     codexapp.ApprovalCommandExecution,
		Command:  "make test",
		ThreadID: "thread-demo",
	}
	toolInput := &codexapp.ToolInputRequest{
		ID:       "tool-1",
		ThreadID: "thread-demo",
		Questions: []codexapp.ToolInputQuestion{{
			ID:       "choice",
			Question: "Select the next action",
		}},
	}
	cached := codexapp.Snapshot{
		Started:          true,
		Provider:         codexapp.ProviderOpenCode,
		ThreadID:         "thread-demo",
		PendingApproval:  approval,
		PendingToolInput: toolInput,
		LastActivityAt:   time.Date(2026, 4, 4, 10, 30, 0, 0, time.UTC),
	}
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot:    cached,
		trySnapshotFn: func(*fakeCodexSession) (codexapp.Snapshot, bool) {
			return codexapp.Snapshot{}, false
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Provider:    codexapp.ProviderOpenCode,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	m := Model{
		codexManager: manager,
		codexSnapshots: map[string]codexapp.Snapshot{
			"/tmp/demo": cached,
		},
	}

	gotApproval, provider, ok := m.projectPendingEmbeddedApproval("/tmp/demo")
	if !ok || gotApproval != approval {
		t.Fatalf("projectPendingEmbeddedApproval() = (%v, %v), want cached approval", gotApproval, ok)
	}
	if provider != codexapp.ProviderOpenCode {
		t.Fatalf("approval provider = %q, want %q", provider, codexapp.ProviderOpenCode)
	}

	question, provider, ok := m.projectPendingEmbeddedQuestion("/tmp/demo")
	if !ok {
		t.Fatalf("projectPendingEmbeddedQuestion() = false, want true")
	}
	if question != "Select the next action" {
		t.Fatalf("question summary = %q, want cached summary", question)
	}
	if provider != codexapp.ProviderOpenCode {
		t.Fatalf("question provider = %q, want %q", provider, codexapp.ProviderOpenCode)
	}

	if !m.projectHasLiveCodexSession("/tmp/demo") {
		t.Fatalf("projectHasLiveCodexSession() = false, want true")
	}
	if snapshot, ok := m.liveCodexSnapshot("/tmp/demo"); !ok || snapshot.ThreadID != "thread-demo" {
		t.Fatalf("liveCodexSnapshot() = (%+v, %v), want cached live snapshot", snapshot, ok)
	}
	if snapshot, ok := m.liveEmbeddedSnapshotForProject("/tmp/demo", codexapp.ProviderOpenCode); !ok || snapshot.ThreadID != "thread-demo" {
		t.Fatalf("liveEmbeddedSnapshotForProject() = (%+v, %v), want cached embedded snapshot", snapshot, ok)
	}
	if snapshot, ok := m.codexSnapshotForProject("/tmp/demo"); !ok || snapshot.ThreadID != "thread-demo" {
		t.Fatalf("codexSnapshotForProject() = (%+v, %v), want cached snapshot", snapshot, ok)
	}
	if session.snapshotCalls != 0 || session.trySnapshotCalls != 0 {
		t.Fatalf("helpers should avoid full transcript snapshots; Snapshot/TrySnapshot calls = %d/%d", session.snapshotCalls, session.trySnapshotCalls)
	}
}

func TestCachedLiveCodexSnapshotCarriesGoalFromLightState(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Started:  true,
			Provider: codexapp.ProviderCodex,
			ThreadID: "thread-demo",
			Goal: &codexapp.ThreadGoal{
				ThreadID:   "thread-demo",
				Objective:  "finish the branch",
				Status:     codexapp.ThreadGoalStatusActive,
				TokensUsed: 42,
			},
			LastActivityAt: time.Date(2026, 4, 4, 10, 30, 0, 0, time.UTC),
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Provider:    codexapp.ProviderCodex,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	m := Model{
		codexManager: manager,
		codexSnapshots: map[string]codexapp.Snapshot{
			"/tmp/demo": {
				Started:        true,
				Provider:       codexapp.ProviderCodex,
				ThreadID:       "thread-demo",
				LastActivityAt: time.Date(2026, 4, 4, 10, 29, 0, 0, time.UTC),
			},
		},
	}

	snapshot, ok := m.liveCodexSnapshot("/tmp/demo")
	if !ok {
		t.Fatalf("liveCodexSnapshot() = false, want live snapshot")
	}
	if snapshot.Goal == nil {
		t.Fatalf("liveCodexSnapshot().Goal = nil, want goal from lightweight session state")
	}
	if snapshot.Goal.Objective != "finish the branch" {
		t.Fatalf("goal objective = %q, want finish the branch", snapshot.Goal.Objective)
	}
	if session.snapshotCalls != 0 || session.trySnapshotCalls != 0 {
		t.Fatalf("helper should use lightweight session state; Snapshot/TrySnapshot calls = %d/%d", session.snapshotCalls, session.trySnapshotCalls)
	}
}

func TestEmbeddedSnapshotHelpersIgnoreStaleOpenCacheWithoutBackingSession(t *testing.T) {
	m := Model{
		codexSnapshots: map[string]codexapp.Snapshot{
			"/tmp/demo": {
				Started:        true,
				Provider:       codexapp.ProviderClaudeCode,
				ProjectPath:    "/tmp/demo",
				ThreadID:       "ses-demo",
				Status:         "Claude Code session ready",
				LastActivityAt: time.Date(2026, 4, 4, 10, 30, 0, 0, time.UTC),
			},
		},
	}

	if m.projectHasLiveCodexSession("/tmp/demo") {
		t.Fatalf("projectHasLiveCodexSession() = true, want false for stale open cache without a backing session")
	}
	if snapshot, ok := m.liveCodexSnapshot("/tmp/demo"); ok {
		t.Fatalf("liveCodexSnapshot() = (%+v, true), want false for stale open cache", snapshot)
	}
	if snapshot, ok := m.liveEmbeddedSnapshotForProject("/tmp/demo", codexapp.ProviderClaudeCode); ok {
		t.Fatalf("liveEmbeddedSnapshotForProject() = (%+v, true), want false for stale open cache", snapshot)
	}
	if snapshot, ok := m.codexSnapshotForProject("/tmp/demo"); ok {
		t.Fatalf("codexSnapshotForProject() = (%+v, true), want false for stale open cache", snapshot)
	}
}

func TestEmbeddedSnapshotHelpersUseLightStateToRejectClosedLiveSession(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Started:        true,
			Closed:         true,
			Provider:       codexapp.ProviderClaudeCode,
			ProjectPath:    "/tmp/demo",
			ThreadID:       "ses-demo",
			Status:         "Claude Code session closed",
			LastActivityAt: time.Date(2026, 4, 4, 10, 35, 0, 0, time.UTC),
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Provider:    codexapp.ProviderClaudeCode,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	m := Model{
		codexManager: manager,
		codexSnapshots: map[string]codexapp.Snapshot{
			"/tmp/demo": {
				Started:        true,
				Provider:       codexapp.ProviderClaudeCode,
				ProjectPath:    "/tmp/demo",
				ThreadID:       "ses-demo",
				Status:         "Claude Code session ready",
				LastActivityAt: time.Date(2026, 4, 4, 10, 30, 0, 0, time.UTC),
			},
		},
	}

	if m.projectHasLiveCodexSession("/tmp/demo") {
		t.Fatalf("projectHasLiveCodexSession() = true, want false after the live session reports closed")
	}
	if snapshot, ok := m.liveCodexSnapshot("/tmp/demo"); ok {
		t.Fatalf("liveCodexSnapshot() = (%+v, true), want false after the live session reports closed", snapshot)
	}
	if snapshot, ok := m.codexSnapshotForProject("/tmp/demo"); ok {
		t.Fatalf("codexSnapshotForProject() = (%+v, true), want false after the live session reports closed", snapshot)
	}
	if session.snapshotCalls != 0 || session.trySnapshotCalls != 0 {
		t.Fatalf("helpers should consult lightweight session state, not full snapshots; Snapshot/TrySnapshot calls = %d/%d", session.snapshotCalls, session.trySnapshotCalls)
	}
}

func TestShouldRefreshProjectStatusAfterCodexSnapshotWhenTurnSettles(t *testing.T) {
	if !shouldRefreshProjectStatusAfterCodexSnapshot(
		codexapp.Snapshot{Busy: true},
		codexapp.Snapshot{Busy: false},
	) {
		t.Fatal("busy-to-idle transition should refresh project status")
	}
	if shouldRefreshProjectStatusAfterCodexSnapshot(
		codexapp.Snapshot{Busy: false},
		codexapp.Snapshot{Busy: false},
	) {
		t.Fatal("idle snapshots should not refresh project status")
	}
	if shouldRefreshProjectStatusAfterCodexSnapshot(
		codexapp.Snapshot{Busy: true},
		codexapp.Snapshot{Busy: true},
	) {
		t.Fatal("still-busy snapshots should not refresh project status")
	}
	if shouldRefreshProjectStatusAfterCodexSnapshot(
		codexapp.Snapshot{Busy: true},
		codexapp.Snapshot{Busy: false, Closed: true},
	) {
		t.Fatal("closed snapshots should rely on the scan refresh path instead")
	}
}

func TestShouldPersistEmbeddedSessionTransitionKeepsStreamingPulsesInMemory(t *testing.T) {
	now := time.Date(2026, 4, 10, 10, 57, 0, 0, time.UTC)
	prev := codexapp.Snapshot{
		Provider:           codexapp.ProviderCodex,
		ThreadID:           "thread-demo",
		Started:            true,
		Busy:               true,
		BusySince:          now.Add(-5 * time.Minute),
		ActiveTurnID:       "turn-demo",
		LastBusyActivityAt: now.Add(-time.Minute),
	}
	next := prev
	next.LastBusyActivityAt = now
	if shouldPersistEmbeddedSessionTransitionAfterCodexSnapshot(true, prev, next) {
		t.Fatal("streaming activity pulse should stay in the in-memory session snapshot")
	}
	if !shouldPersistEmbeddedSessionTransitionAfterCodexSnapshot(false, codexapp.Snapshot{}, next) {
		t.Fatal("first live snapshot should persist the session start transition")
	}
	waiting := next
	waiting.PendingApproval = &codexapp.ApprovalRequest{ID: "approval-demo", Kind: codexapp.ApprovalCommandExecution}
	if !shouldPersistEmbeddedSessionTransitionAfterCodexSnapshot(true, next, waiting) {
		t.Fatal("approval transition should be persisted")
	}
	if !shouldPersistEmbeddedSessionTransitionAfterCodexSnapshot(true, waiting, next) {
		t.Fatal("return from waiting to working should be persisted")
	}
	newTurn := next
	newTurn.ActiveTurnID = "turn-next"
	newTurn.BusySince = now
	if !shouldPersistEmbeddedSessionTransitionAfterCodexSnapshot(true, next, newTurn) {
		t.Fatal("new active turn should be persisted")
	}
	idle := next
	idle.Busy = false
	idle.LastBusyActivityAt = now.Add(time.Minute)
	if shouldPersistEmbeddedSessionTransitionAfterCodexSnapshot(true, next, idle) {
		t.Fatal("idle snapshots should use the existing settle refresh path")
	}
}

func TestCodexUpdateDefersAckForVisibleStreamingTranscript(t *testing.T) {
	projectPath := "/tmp/demo"
	now := time.Date(2026, 6, 18, 9, 30, 0, 0, time.UTC)
	prev := codexapp.Snapshot{
		Provider:                 codexapp.ProviderLCAgent,
		ProjectPath:              projectPath,
		ThreadID:                 "thread-demo",
		Started:                  true,
		Busy:                     true,
		Phase:                    codexapp.SessionPhaseRunning,
		ActiveTurnID:             "turn-demo",
		Status:                   "LCAgent is working...",
		LastBusyActivityAt:       now,
		TranscriptRevision:       1,
		ManagedBrowserSessionKey: "browser-demo",
		Entries: []codexapp.TranscriptEntry{{
			Kind: codexapp.TranscriptAgent,
			Text: "streaming",
		}},
	}
	next := prev
	next.LastBusyActivityAt = now.Add(10 * time.Millisecond)
	next.TranscriptRevision = 2
	next.Entries = []codexapp.TranscriptEntry{{
		Kind: codexapp.TranscriptAgent,
		Text: "streaming token",
	}}

	session, manager, notifySession := openFakeManagedCodexSession(t, projectPath, next)
	requireManagerUpdate(t, manager, projectPath)
	m := Model{
		codexManager:        manager,
		codexVisibleProject: projectPath,
		codexHiddenProject:  projectPath,
		codexInput:          newCodexTextarea(),
		codexViewport:       viewport.New(80, 20),
		width:               100,
		height:              24,
	}
	m.storeCodexSnapshot(projectPath, prev)

	updated, _ := m.applyCodexUpdateMsg(codexUpdateMsg{projectPath: projectPath})
	got := normalizeUpdateModel(updated)
	if _, ok := got.codexUpdateAckSeq[projectPath]; !ok {
		t.Fatalf("visible streaming transcript update should defer manager ack")
	}
	if session.trySnapshotCalls == 0 {
		t.Fatalf("applyCodexUpdateMsg() should refresh the session snapshot")
	}

	notifySession()
	assertNoManagerUpdate(t, manager)

	seq := got.codexUpdateAckSeq[projectPath]
	updated, _ = got.applyCodexUpdateAckMsg(codexUpdateAckMsg{projectPath: projectPath, seq: seq})
	got = normalizeUpdateModel(updated)
	if _, ok := got.codexUpdateAckSeq[projectPath]; ok {
		t.Fatalf("deferred ack state should clear after ack message")
	}
	requireManagerUpdate(t, manager, projectPath)
}

func TestCodexUpdateThrottlesBackgroundStreamingWithoutCopyingTranscript(t *testing.T) {
	projectPath := "/tmp/demo"
	now := time.Date(2026, 6, 18, 9, 30, 0, 0, time.UTC)
	prev := codexapp.Snapshot{
		Provider:           codexapp.ProviderCodex,
		ProjectPath:        projectPath,
		ThreadID:           "thread-demo",
		Started:            true,
		Busy:               true,
		Phase:              codexapp.SessionPhaseRunning,
		ActiveTurnID:       "turn-demo",
		Status:             "Codex is working...",
		LastBusyActivityAt: now,
		TranscriptRevision: 1,
		Entries: []codexapp.TranscriptEntry{{
			Kind: codexapp.TranscriptAgent,
			Text: "cached transcript",
		}},
	}
	next := prev
	next.LastBusyActivityAt = now.Add(10 * time.Millisecond)
	next.TranscriptRevision = 2
	next.Entries = []codexapp.TranscriptEntry{{
		Kind: codexapp.TranscriptAgent,
		Text: "The updated session summary is ready for the main project row.",
	}}
	next.ActivityPreview = []codexapp.TranscriptEntry{{
		Kind: codexapp.TranscriptAgent,
		Text: "The updated session summary is ready for the main project row.",
	}}

	session, manager, notifySession := openFakeManagedCodexSession(t, projectPath, next)
	session.tryStateSnapshotFn = func(*fakeCodexSession) (codexapp.Snapshot, bool) {
		state := next
		state.Entries = nil
		state.Transcript = ""
		return state, true
	}
	requireManagerUpdate(t, manager, projectPath)
	m := Model{
		codexManager:       manager,
		codexHiddenProject: projectPath,
		codexInput:         newCodexTextarea(),
		codexViewport:      viewport.New(80, 20),
		width:              100,
		height:             24,
	}
	m.storeCodexSnapshot(projectPath, prev)

	updated, _ := m.applyCodexUpdateMsg(codexUpdateMsg{projectPath: projectPath})
	got := normalizeUpdateModel(updated)
	if _, ok := got.codexUpdateAckSeq[projectPath]; !ok {
		t.Fatalf("background streaming update should defer manager ack")
	}
	if session.tryStateSnapshotCalls == 0 {
		t.Fatalf("background update should use the lightweight state snapshot")
	}
	if session.trySnapshotCalls != 0 {
		t.Fatalf("background update copied the full transcript %d time(s)", session.trySnapshotCalls)
	}
	if cached, ok := got.codexCachedSnapshot(projectPath); !ok || len(cached.Entries) != 1 || cached.Entries[0].Text != "cached transcript" {
		t.Fatalf("background state refresh should preserve cached transcript, got %#v", cached.Entries)
	}
	if cached, ok := got.codexCachedSnapshot(projectPath); !ok || liveEngineerSnapshotDetail(cached) != "The updated session summary is ready for the main project row." {
		t.Fatalf("background state refresh should update the main-row activity preview, got %#v", cached.ActivityPreview)
	}
	if delay := got.codexStreamingUpdateAckDelay(projectPath); delay != codexBackgroundStreamingUpdateAckDelay {
		t.Fatalf("background ack delay = %s, want %s", delay, codexBackgroundStreamingUpdateAckDelay)
	}

	notifySession()
	assertNoManagerUpdate(t, manager)

	seq := got.codexUpdateAckSeq[projectPath]
	updated, _ = got.applyCodexUpdateAckMsg(codexUpdateAckMsg{projectPath: projectPath, seq: seq})
	got = normalizeUpdateModel(updated)
	if _, ok := got.codexUpdateAckSeq[projectPath]; ok {
		t.Fatalf("deferred background ack state should clear after ack message")
	}
	requireManagerUpdate(t, manager, projectPath)
}

func TestCodexUpdateSettlesVisiblePendingOpenWithFullTranscript(t *testing.T) {
	projectPath := "/tmp/resumed"
	full := codexapp.Snapshot{
		Provider:           codexapp.ProviderCodex,
		ProjectPath:        projectPath,
		ThreadID:           "thread-resumed",
		Started:            true,
		TranscriptRevision: 7,
		Entries: []codexapp.TranscriptEntry{
			{Kind: codexapp.TranscriptUser, Text: "implement playthrough mode"},
			{Kind: codexapp.TranscriptAgent, Text: "Implemented and verified the Android build."},
		},
	}
	session, manager, _ := openFakeManagedCodexSession(t, projectPath, full)
	requireManagerUpdate(t, manager, projectPath)
	// Match production StateSnapshot behavior: it carries live state but not
	// the potentially large transcript payload.
	session.tryStateSnapshotFn = func(*fakeCodexSession) (codexapp.Snapshot, bool) {
		state := full
		state.Entries = nil
		state.Transcript = ""
		return state, true
	}

	m := Model{
		codexManager:  manager,
		codexInput:    newCodexTextarea(),
		codexViewport: viewport.New(80, 20),
		width:         100,
		height:        24,
	}
	m.beginCodexPendingOpen(projectPath, codexapp.ProviderCodex)

	updated, _ := m.applyCodexUpdateMsg(codexUpdateMsg{projectPath: projectPath})
	got := normalizeUpdateModel(updated)
	if got.codexPendingOpen != nil {
		t.Fatal("ready snapshot should settle the visible pending open")
	}
	if got.codexVisibleProject != projectPath {
		t.Fatalf("visible project = %q, want %q", got.codexVisibleProject, projectPath)
	}
	if session.trySnapshotCalls == 0 {
		t.Fatal("visible pending open should fetch the full transcript snapshot")
	}
	cached, ok := got.codexCachedSnapshot(projectPath)
	if !ok || len(cached.Entries) != len(full.Entries) {
		t.Fatalf("settled pending transcript = %#v, want %#v", cached.Entries, full.Entries)
	}
}

func TestCodexUpdateAcksImmediatelyForPendingApproval(t *testing.T) {
	projectPath := "/tmp/demo"
	prev := codexapp.Snapshot{
		Provider:           codexapp.ProviderLCAgent,
		ProjectPath:        projectPath,
		ThreadID:           "thread-demo",
		Started:            true,
		Busy:               true,
		Phase:              codexapp.SessionPhaseRunning,
		ActiveTurnID:       "turn-demo",
		Status:             "LCAgent is working...",
		TranscriptRevision: 1,
		Entries: []codexapp.TranscriptEntry{{
			Kind: codexapp.TranscriptAgent,
			Text: "before approval",
		}},
	}
	next := prev
	next.Status = "Waiting for command approval"
	next.TranscriptRevision = 2
	next.PendingApproval = &codexapp.ApprovalRequest{
		ID:      "approval-demo",
		Kind:    codexapp.ApprovalCommandExecution,
		Command: "make test",
	}
	next.Entries = []codexapp.TranscriptEntry{{
		Kind: codexapp.TranscriptStatus,
		Text: "LCAgent requested command approval: make test",
	}}

	for _, tc := range []struct {
		name    string
		visible bool
	}{
		{name: "visible", visible: true},
		{name: "background", visible: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, manager, notifySession := openFakeManagedCodexSession(t, projectPath, next)
			requireManagerUpdate(t, manager, projectPath)
			m := Model{
				codexManager:       manager,
				codexHiddenProject: projectPath,
				codexInput:         newCodexTextarea(),
				codexViewport:      viewport.New(80, 20),
				width:              100,
				height:             24,
			}
			if tc.visible {
				m.codexVisibleProject = projectPath
			}
			m.storeCodexSnapshot(projectPath, prev)

			updated, _ := m.applyCodexUpdateMsg(codexUpdateMsg{projectPath: projectPath})
			got := normalizeUpdateModel(updated)
			if _, ok := got.codexUpdateAckSeq[projectPath]; ok {
				t.Fatalf("pending approval update should ack immediately")
			}

			notifySession()
			requireManagerUpdate(t, manager, projectPath)
		})
	}
}

func TestEmbeddedSessionActivityFromSnapshotUsesBusyActivity(t *testing.T) {
	now := time.Date(2026, 4, 10, 10, 57, 0, 0, time.UTC)
	activity, ok := embeddedSessionActivityFromSnapshot("/tmp/demo", codexapp.Snapshot{
		Provider:           codexapp.ProviderCodex,
		ProjectPath:        "/tmp/other",
		ThreadID:           "thread-demo",
		Started:            true,
		Busy:               true,
		BusySince:          now.Add(-5 * time.Minute),
		LastActivityAt:     now.Add(time.Minute),
		LastBusyActivityAt: now,
	})
	if !ok {
		t.Fatal("expected snapshot to produce embedded activity")
	}
	if activity.ProjectPath != "/tmp/demo" {
		t.Fatalf("project path = %q, want explicit path", activity.ProjectPath)
	}
	if activity.Source != model.SessionSourceCodex {
		t.Fatalf("source = %q, want codex", activity.Source)
	}
	if activity.SessionID != "thread-demo" {
		t.Fatalf("session id = %q", activity.SessionID)
	}
	if !activity.LastActivityAt.Equal(now) {
		t.Fatalf("last activity = %v, want busy activity %v", activity.LastActivityAt, now)
	}
	if !activity.LatestTurnStateKnown || activity.LatestTurnCompleted {
		t.Fatalf("turn state = known:%t completed:%t, want live incomplete", activity.LatestTurnStateKnown, activity.LatestTurnCompleted)
	}
}

func TestTodoWorkStateTreatsApprovalAsWaiting(t *testing.T) {
	snapshot := codexapp.Snapshot{
		Started:         true,
		Busy:            true,
		PendingApproval: &codexapp.ApprovalRequest{ID: "approval-demo", Kind: codexapp.ApprovalCommandExecution},
	}
	if got := todoWorkStateFromEmbeddedSnapshot(snapshot, false); got != model.TodoWorkStateWaiting {
		t.Fatalf("todo work state = %q, want waiting", got)
	}
}

func openFakeManagedCodexSession(t *testing.T, projectPath string, snapshot codexapp.Snapshot) (*fakeCodexSession, *codexapp.Manager, func()) {
	t.Helper()
	session := &fakeCodexSession{
		projectPath: projectPath,
		snapshot:    snapshot,
	}
	var notifySession func()
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		notifySession = notify
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: projectPath,
		Provider:    snapshot.Provider,
		Preset:      codexcli.PresetYolo,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}
	if notifySession == nil {
		t.Fatalf("manager factory did not receive notify callback")
	}
	return session, manager, notifySession
}

func requireManagerUpdate(t *testing.T, manager *codexapp.Manager, want string) {
	t.Helper()
	select {
	case got := <-manager.Updates():
		if got != want {
			t.Fatalf("manager update = %q, want %q", got, want)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatalf("timed out waiting for manager update %q", want)
	}
}

func assertNoManagerUpdate(t *testing.T, manager *codexapp.Manager) {
	t.Helper()
	select {
	case got := <-manager.Updates():
		t.Fatalf("unexpected manager update before deferred ack: %q", got)
	default:
	}
}

func TestEmbeddedSessionSettledActivityFromSnapshotMarksTurnCompleted(t *testing.T) {
	now := time.Date(2026, 4, 10, 10, 57, 0, 0, time.UTC)
	activity, ok := embeddedSessionSettledActivityFromSnapshot("/tmp/demo", codexapp.Snapshot{
		Provider:       codexapp.ProviderCodex,
		ProjectPath:    "/tmp/other",
		ThreadID:       "thread-demo",
		Started:        true,
		BusySince:      now.Add(-5 * time.Minute),
		LastActivityAt: now,
	})
	if !ok {
		t.Fatal("expected settled snapshot to produce embedded activity")
	}
	if activity.ProjectPath != "/tmp/demo" {
		t.Fatalf("project path = %q, want explicit path", activity.ProjectPath)
	}
	if !activity.LatestTurnStateKnown || !activity.LatestTurnCompleted {
		t.Fatalf("turn state = known:%t completed:%t, want settled turn", activity.LatestTurnStateKnown, activity.LatestTurnCompleted)
	}
	if !activity.LastActivityAt.Equal(now) {
		t.Fatalf("last activity = %v, want %v", activity.LastActivityAt, now)
	}
}

func TestSelectedProjectCodexSessionIDPrefersDetailCodexSession(t *testing.T) {
	project := model.ProjectSummary{
		Path:                "/tmp/demo",
		Name:                "demo",
		PresentOnDisk:       true,
		LatestSessionID:     "latest-opencode",
		LatestSessionFormat: "opencode_db",
	}
	m := Model{
		projects: []model.ProjectSummary{project},
		selected: 0,
		detail: model.ProjectDetail{
			Summary: model.ProjectSummary{Path: "/tmp/demo"},
			Sessions: []model.SessionEvidence{
				{SessionID: "op_1", Format: "opencode_db"},
				{SessionID: "cx_2", Format: "modern"},
				{SessionID: "cx_1", Format: "legacy"},
			},
		},
	}

	got := m.selectedProjectCodexSessionID(project)
	if got != "cx_2" {
		t.Fatalf("selectedProjectCodexSessionID() = %q, want %q", got, "cx_2")
	}
}

func TestSelectedProjectSessionIDPrefersDetailOpenCodeSession(t *testing.T) {
	project := model.ProjectSummary{
		Path:                "/tmp/demo",
		Name:                "demo",
		PresentOnDisk:       true,
		LatestSessionID:     "cx_summary",
		LatestSessionFormat: "modern",
	}
	m := Model{
		projects: []model.ProjectSummary{project},
		selected: 0,
		detail: model.ProjectDetail{
			Summary: model.ProjectSummary{Path: "/tmp/demo"},
			Sessions: []model.SessionEvidence{
				{SessionID: "cx_2", Format: "modern"},
				{SessionID: "op_3", Format: "opencode_db"},
			},
		},
	}

	got := m.selectedProjectSessionID(project, codexapp.ProviderOpenCode)
	if got != "op_3" {
		t.Fatalf("selectedProjectSessionID() = %q, want %q", got, "op_3")
	}
}

func TestSelectedProjectSessionIDPrefersLiveEmbeddedSession(t *testing.T) {
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider: codexapp.ProviderCodex,
				Started:  true,
				ThreadID: "thread-live",
				Status:   "Codex session ready",
			},
		}, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Preset:      codexcli.PresetYolo,
		ResumeID:    "thread-live",
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	project := model.ProjectSummary{
		Path:                "/tmp/demo",
		Name:                "demo",
		PresentOnDisk:       true,
		LatestSessionID:     "cx_summary",
		LatestSessionFormat: "modern",
	}
	m := Model{
		codexManager: manager,
		projects:     []model.ProjectSummary{project},
		selected:     0,
		detail: model.ProjectDetail{
			Summary: model.ProjectSummary{Path: "/tmp/demo"},
			Sessions: []model.SessionEvidence{
				{SessionID: "cx_2", Format: "modern"},
			},
		},
	}

	got := m.selectedProjectSessionID(project, codexapp.ProviderCodex)
	if got != "thread-live" {
		t.Fatalf("selectedProjectSessionID() = %q, want %q", got, "thread-live")
	}
}

func TestSelectedProjectCodexSessionIDFallsBackToSummary(t *testing.T) {
	project := model.ProjectSummary{
		Path:                "/tmp/demo",
		Name:                "demo",
		PresentOnDisk:       true,
		LatestSessionID:     "cx_summary",
		LatestSessionFormat: "modern",
	}
	m := Model{
		projects: []model.ProjectSummary{project},
		selected: 0,
	}

	got := m.selectedProjectCodexSessionID(project)
	if got != "cx_summary" {
		t.Fatalf("selectedProjectCodexSessionID() = %q, want %q", got, "cx_summary")
	}
}

func TestPreferredEmbeddedProviderUsesOneShotOverrideBeforeStoredLatest(t *testing.T) {
	project := model.ProjectSummary{
		Path:                "/tmp/demo",
		Name:                "demo",
		LatestSessionFormat: "modern",
	}
	m := Model{}
	m.setEmbeddedLaunchProviderOverride(project.Path, codexapp.ProviderOpenCode)

	if got := m.preferredEmbeddedProviderForProject(project); got != codexapp.ProviderOpenCode {
		t.Fatalf("preferred provider = %q, want OpenCode override", got)
	}
}

func TestPreferredEmbeddedProviderUsesPersistedPreferredSourceWhenNoSession(t *testing.T) {
	project := model.ProjectSummary{
		Path:                   "/tmp/demo",
		Name:                   "demo",
		PreferredSessionSource: model.SessionSourceLCAgent,
	}
	m := Model{}

	if got := m.preferredEmbeddedProviderForProject(project); got != codexapp.ProviderLCAgent {
		t.Fatalf("preferred provider = %q, want LCAgent", got)
	}
}

func TestPreferredEmbeddedProviderKeepsLiveSessionBeforeOneShotOverride(t *testing.T) {
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider: codexapp.ProviderCodex,
				Started:  true,
				ThreadID: "thread-live",
				Status:   "Codex session ready",
			},
		}, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Provider:    codexapp.ProviderCodex,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	project := model.ProjectSummary{Path: "/tmp/demo", Name: "demo"}
	m := Model{codexManager: manager}
	m.setEmbeddedLaunchProviderOverride(project.Path, codexapp.ProviderOpenCode)

	if got := m.preferredEmbeddedProviderForProject(project); got != codexapp.ProviderCodex {
		t.Fatalf("preferred provider = %q, want live Codex provider", got)
	}
}

func TestDefaultNewItemProviderUsesLatestScannedEmbeddedProvider(t *testing.T) {
	now := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	m := Model{
		projects: []model.ProjectSummary{
			{
				Path:                     "/tmp/codex",
				LatestSessionFormat:      "modern",
				LatestSessionLastEventAt: now.Add(-2 * time.Hour),
			},
			{
				Path:                     "/tmp/opencode",
				LatestSessionFormat:      "opencode_db",
				LatestSessionLastEventAt: now.Add(-time.Hour),
			},
		},
	}

	provider, label := m.defaultEmbeddedProviderForNewItem()
	if provider != codexapp.ProviderOpenCode {
		t.Fatalf("default provider = %q, want latest scanned OpenCode", provider)
	}
	if label != "last used" {
		t.Fatalf("default label = %q, want last used", label)
	}
}
