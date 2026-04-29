package tui

import (
	"testing"
	"time"

	"lcroom/internal/codexapp"
	"lcroom/internal/codexcli"
	"lcroom/internal/model"
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

func TestShouldRecordEmbeddedSessionActivityAfterBusyProgress(t *testing.T) {
	now := time.Date(2026, 4, 10, 10, 57, 0, 0, time.UTC)
	prev := codexapp.Snapshot{
		Started:            true,
		Busy:               true,
		LastBusyActivityAt: now.Add(-time.Minute),
	}
	next := codexapp.Snapshot{
		Started:            true,
		Busy:               true,
		LastBusyActivityAt: now,
	}
	if !shouldRecordEmbeddedSessionActivityAfterCodexSnapshot(true, prev, next) {
		t.Fatal("busy progress should record embedded activity")
	}
	if shouldRecordEmbeddedSessionActivityAfterCodexSnapshot(true, next, next) {
		t.Fatal("unchanged busy activity should not record another heartbeat")
	}
	if shouldRecordEmbeddedSessionActivityAfterCodexSnapshot(true, next, codexapp.Snapshot{
		Started:            true,
		Busy:               false,
		LastBusyActivityAt: now.Add(time.Minute),
	}) {
		t.Fatal("idle snapshots should use the existing settle refresh path")
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
