package tui

import (
	"context"
	"fmt"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"lcroom/internal/attention"
	"lcroom/internal/browserctl"
	"lcroom/internal/codexapp"
	"lcroom/internal/codexcli"
	"lcroom/internal/config"
	"lcroom/internal/events"
	"lcroom/internal/model"
	"lcroom/internal/service"
	"lcroom/internal/store"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestVisibleCodexCtrlCClosesIdleSession(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Started: true,
			Preset:  codexcli.PresetYolo,
			Status:  "Codex session ready",
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Preset:      codexcli.PresetYolo,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          newCodexTextarea(),
		codexViewport:       viewport.New(0, 0),
		managedBrowserStates: map[string]browserctl.ManagedPlaywrightState{
			"managed-demo": {SessionKey: "managed-demo", BrowserPID: 123, Hidden: true},
		},
		width:  100,
		height: 24,
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyCtrlC})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("ctrl+c should close an idle embedded Codex session")
	}
	if got.status != "Closing embedded Codex session..." {
		t.Fatalf("status = %q, want closing notice", got.status)
	}

	msg := cmd()
	action, ok := msg.(codexActionMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want codexActionMsg", msg)
	}
	if !action.closed {
		t.Fatalf("close action should mark the session as closed")
	}
	if !session.snapshot.Closed {
		t.Fatalf("ctrl+c should close the backing session")
	}
}

func TestVisibleCodexCtrlCMarksClosedSessionSeenAndPersistsIt(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	seenAt := time.Date(2026, 4, 20, 10, 30, 0, 0, time.UTC)
	projectPath := filepath.Join(t.TempDir(), "demo")

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:          projectPath,
		Name:          "demo",
		PresentOnDisk: true,
		InScope:       true,
		UpdatedAt:     seenAt.Add(-time.Minute),
	}); err != nil {
		t.Fatalf("seed project state: %v", err)
	}

	svc := service.New(config.Default(), st, events.NewBus(), nil)
	project := model.ProjectSummary{
		Path:                            projectPath,
		Name:                            "demo",
		PresentOnDisk:                   true,
		LatestSessionClassification:     model.ClassificationCompleted,
		LatestSessionClassificationType: model.SessionCategoryCompleted,
		LatestSessionFormat:             "modern",
		LatestSessionLastEventAt:        seenAt.Add(-5 * time.Minute),
		LatestTurnStateKnown:            true,
		LatestTurnCompleted:             true,
	}

	session := &fakeCodexSession{
		projectPath: projectPath,
		snapshot: codexapp.Snapshot{
			Started: true,
			Preset:  codexcli.PresetYolo,
			Status:  "Codex session ready",
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: projectPath,
		Preset:      codexcli.PresetYolo,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	m := Model{
		ctx:                 ctx,
		svc:                 svc,
		nowFn:               func() time.Time { return seenAt },
		allProjects:         []model.ProjectSummary{project},
		projects:            []model.ProjectSummary{project},
		detail:              model.ProjectDetail{Summary: project},
		selected:            0,
		codexManager:        manager,
		codexVisibleProject: projectPath,
		codexHiddenProject:  projectPath,
		codexInput:          newCodexTextarea(),
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              24,
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyCtrlC})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("ctrl+c should close an idle embedded Codex session")
	}

	msg := cmd()
	action, ok := msg.(codexActionMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want codexActionMsg", msg)
	}
	if !action.closed {
		t.Fatalf("close action should mark the session as closed")
	}

	updated, followUp := got.Update(action)
	got = updated.(Model)
	if !got.projects[0].LastSessionSeenAt.Equal(seenAt) {
		t.Fatalf("projects seen_at = %v, want %v", got.projects[0].LastSessionSeenAt, seenAt)
	}
	if !got.detail.Summary.LastSessionSeenAt.Equal(seenAt) {
		t.Fatalf("detail seen_at = %v, want %v", got.detail.Summary.LastSessionSeenAt, seenAt)
	}
	if followUp == nil {
		t.Fatalf("close action should queue a seen-state write")
	}

	var seenMsg projectSessionSeenMsg
	foundSeenMsg := false
	for _, followUpMsg := range collectCmdMsgs(followUp) {
		candidate, ok := followUpMsg.(projectSessionSeenMsg)
		if !ok {
			continue
		}
		seenMsg = candidate
		foundSeenMsg = true
		break
	}
	if !foundSeenMsg {
		t.Fatalf("follow-up work should include projectSessionSeenMsg")
	}
	if seenMsg.err != nil {
		t.Fatalf("mark seen follow-up error = %v, want nil", seenMsg.err)
	}

	summary, err := st.GetProjectSummary(ctx, projectPath, false)
	if err != nil {
		t.Fatalf("GetProjectSummary() error = %v", err)
	}
	if !summary.LastSessionSeenAt.Equal(seenAt) {
		t.Fatalf("stored seen_at = %v, want %v", summary.LastSessionSeenAt, seenAt)
	}

	updated, refreshCmd := got.Update(seenMsg)
	got = updated.(Model)
	if refreshCmd == nil {
		t.Fatalf("marking the closed session seen should queue a refresh")
	}
	if got.codexVisibleProject != "" {
		t.Fatalf("codexVisibleProject = %q, want closed", got.codexVisibleProject)
	}
}

func TestCodexUpdateMissingIdleSessionDoesNotResurfaceUnread(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	projectPath := filepath.Join(t.TempDir(), "demo")
	baseEventAt := time.Date(2026, 4, 20, 10, 0, 0, 0, time.UTC)
	seenAt := baseEventAt.Add(5 * time.Minute)

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	session := model.NormalizeSessionEvidenceIdentity(model.SessionEvidence{
		Source:               model.SessionSourceOpenCode,
		SessionID:            "ses-open",
		RawSessionID:         "ses-open",
		ProjectPath:          projectPath,
		DetectedProjectPath:  projectPath,
		SessionFile:          filepath.Join(t.TempDir(), "opencode.db") + "#session:ses-open",
		Format:               "opencode_db",
		SnapshotHash:         "snapshot-open",
		StartedAt:            baseEventAt.Add(-10 * time.Minute),
		LastEventAt:          baseEventAt,
		LatestTurnStateKnown: true,
		LatestTurnCompleted:  true,
	})
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:           projectPath,
		Name:           "demo",
		LastActivity:   baseEventAt,
		Status:         model.StatusIdle,
		AttentionScore: 0,
		PresentOnDisk:  true,
		InScope:        true,
		UpdatedAt:      seenAt.Add(-time.Minute),
		Sessions:       []model.SessionEvidence{session},
	}); err != nil {
		t.Fatalf("seed project state: %v", err)
	}

	classification := model.SessionClassification{
		SessionID:         session.SessionID,
		Source:            session.Source,
		RawSessionID:      session.RawSessionID,
		ProjectPath:       projectPath,
		SessionFile:       session.SessionFile,
		SessionFormat:     session.Format,
		SnapshotHash:      session.SnapshotHash,
		Model:             "gpt-5.4-mini",
		ClassifierVersion: "v1",
		SourceUpdatedAt:   baseEventAt,
	}
	if queued, err := st.QueueSessionClassification(ctx, classification, time.Minute); err != nil || !queued {
		t.Fatalf("queue classification: queued=%v err=%v", queued, err)
	}
	claimed, err := st.ClaimNextPendingSessionClassification(ctx, time.Minute)
	if err != nil {
		t.Fatalf("claim classification: %v", err)
	}
	claimed.Category = model.SessionCategoryCompleted
	claimed.Summary = "Done."
	claimed.Confidence = 0.92
	if err := st.CompleteSessionClassification(ctx, claimed); err != nil {
		t.Fatalf("complete classification: %v", err)
	}
	if err := st.SetProjectSessionSeenAt(ctx, projectPath, seenAt); err != nil {
		t.Fatalf("set seen at: %v", err)
	}

	before, err := st.GetProjectSummary(ctx, projectPath, false)
	if err != nil {
		t.Fatalf("summary before update: %v", err)
	}
	if !before.LastSessionSeenAt.Equal(seenAt) {
		t.Fatalf("seen_at before update = %v, want %v", before.LastSessionSeenAt, seenAt)
	}
	if !before.LatestSessionLastEventAt.Equal(baseEventAt) {
		t.Fatalf("last event before update = %v, want %v", before.LatestSessionLastEventAt, baseEventAt)
	}
	if attention.AssessmentUnread(before) {
		t.Fatalf("project should start read, got %#v", before)
	}

	svc := service.New(config.Default(), st, events.NewBus(), nil)
	m := Model{
		ctx: ctx,
		svc: svc,
		codexSnapshots: map[string]codexapp.Snapshot{
			projectPath: {
				Provider:       codexapp.ProviderOpenCode,
				ProjectPath:    projectPath,
				ThreadID:       "ses-open",
				Started:        true,
				Busy:           false,
				LastActivityAt: seenAt.Add(2 * time.Minute),
				Status:         "OpenCode session ready",
			},
		},
		codexInput:    newCodexTextarea(),
		codexViewport: viewport.New(0, 0),
		width:         100,
		height:        24,
	}

	updated, cmd := m.Update(codexUpdateMsg{projectPath: projectPath})
	got := updated.(Model)
	if _, ok := got.codexSnapshots[projectPath]; ok {
		t.Fatalf("missing session update should drop the stale cached snapshot")
	}

	msgs := collectCmdMsgs(cmd)
	for _, msg := range msgs {
		if refreshMsg, ok := msg.(projectStatusRefreshedMsg); ok && refreshMsg.err != nil {
			t.Fatalf("projectStatusRefreshedMsg err = %v", refreshMsg.err)
		}
	}

	after, err := st.GetProjectSummary(ctx, projectPath, false)
	if err != nil {
		t.Fatalf("summary after update: %v", err)
	}
	if !after.LastSessionSeenAt.Equal(seenAt) {
		t.Fatalf("seen_at after update = %v, want %v", after.LastSessionSeenAt, seenAt)
	}
	if !after.LatestSessionLastEventAt.Equal(baseEventAt) {
		t.Fatalf("last event after update = %v, want %v", after.LatestSessionLastEventAt, baseEventAt)
	}
	if attention.AssessmentUnread(after) {
		t.Fatalf("idle missing session should stay read, got %#v", after)
	}
}

func TestVisibleCodexCtrlCDoesNotInterruptExternalBusySession(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Started:      true,
			Preset:       codexcli.PresetYolo,
			Busy:         true,
			BusyExternal: true,
			ActiveTurnID: "turn-live",
			Status:       "Busy in another Codex process",
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Preset:      codexcli.PresetYolo,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          newCodexTextarea(),
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              24,
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyCtrlC})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("ctrl+c should not interrupt an external busy session")
	}
	if session.interrupted {
		t.Fatalf("session should not be interrupted")
	}
	if !strings.Contains(strings.ToLower(got.status), "another process") {
		t.Fatalf("status = %q, want clear busy-elsewhere message", got.status)
	}
}

func TestVisibleCodexCtrlCDoesNotInterruptCompactingSession(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Started: true,
			Preset:  codexcli.PresetYolo,
			Phase:   codexapp.SessionPhaseReconciling,
			Status:  "Compacting conversation history...",
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Preset:      codexcli.PresetYolo,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          newCodexTextarea(),
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              24,
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyCtrlC})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("ctrl+c should not interrupt a compacting session")
	}
	if session.interrupted {
		t.Fatalf("session should not be interrupted while compacting")
	}
	if !strings.Contains(strings.ToLower(got.status), "compacting conversation history") {
		t.Fatalf("status = %q, want compacting guidance", got.status)
	}
}

func TestVisibleCodexCtrlCInterruptsStalledBusySession(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Started:      true,
			Preset:       codexcli.PresetYolo,
			Busy:         true,
			Phase:        codexapp.SessionPhaseStalled,
			ActiveTurnID: "turn-live",
			Status:       "Embedded Codex session seems stuck or disconnected. Use /reconnect.",
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Preset:      codexcli.PresetYolo,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          newCodexTextarea(),
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              24,
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyCtrlC})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("ctrl+c should interrupt a stalled busy embedded Codex session")
	}
	if got.status != "Interrupting stuck Codex turn..." {
		t.Fatalf("status = %q, want stuck interrupt notice", got.status)
	}

	msg := cmd()
	action, ok := msg.(codexActionMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want codexActionMsg", msg)
	}
	if action.err != nil {
		t.Fatalf("interrupt action error = %v, want nil", action.err)
	}
	if !session.interrupted {
		t.Fatalf("session should be interrupted")
	}
}

func TestVisibleLCAgentCtrlCInterruptsBusySession(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Provider: codexapp.ProviderLCAgent,
			Started:  true,
			Busy:     true,
			Phase:    codexapp.SessionPhaseRunning,
			Status:   "LCAgent is working...",
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Provider:    codexapp.ProviderLCAgent,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          newCodexTextarea(),
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              24,
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyCtrlC})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("ctrl+c should interrupt a busy LCAgent session")
	}
	if got.status != "Interrupting LCAgent turn..." {
		t.Fatalf("status = %q, want LCAgent interrupt notice", got.status)
	}

	msg := cmd()
	action, ok := msg.(codexActionMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want codexActionMsg", msg)
	}
	if action.err != nil {
		t.Fatalf("interrupt action error = %v, want nil", action.err)
	}
	if !session.interrupted {
		t.Fatalf("session should be interrupted")
	}
}

func TestVisibleCodexEnterDoesNotSteerExternalBusySession(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Started:      true,
			Preset:       codexcli.PresetYolo,
			Busy:         true,
			BusyExternal: true,
			ActiveTurnID: "turn-live",
			Status:       "Busy in another Codex process",
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Preset:      codexcli.PresetYolo,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	input := newCodexTextarea()
	input.SetValue("please continue")

	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          input,
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              24,
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("enter should not steer an external busy session")
	}
	if len(session.submissions) != 0 {
		t.Fatalf("submissions = %d, want 0", len(session.submissions))
	}
	if got.codexInput.Value() != "please continue" {
		t.Fatalf("composer = %q, want draft preserved", got.codexInput.Value())
	}
	if !strings.Contains(strings.ToLower(got.status), "another process") {
		t.Fatalf("status = %q, want clear busy-elsewhere message", got.status)
	}
}

func TestVisibleLCAgentEnterQueuesInputWhileBusy(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Provider: codexapp.ProviderLCAgent,
			Started:  true,
			Busy:     true,
			Phase:    codexapp.SessionPhaseRunning,
			Status:   "LCAgent is working...",
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Provider:    codexapp.ProviderLCAgent,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	input := newCodexTextarea()
	input.SetValue("please continue")

	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          input,
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              24,
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("enter should queue a busy LCAgent submission command")
	}
	_ = cmd()
	if len(session.submissions) != 1 || session.submissions[0].TranscriptText() != "please continue" {
		t.Fatalf("submissions = %#v, want queued LCAgent input", session.submissions)
	}
	if got.codexInput.Value() != "" {
		t.Fatalf("composer = %q, want draft cleared after queueing", got.codexInput.Value())
	}
	if !strings.Contains(got.status, "Queueing steer for LCAgent") {
		t.Fatalf("status = %q, want queued LCAgent guidance", got.status)
	}
}

func TestVisibleCodexEnterDoesNotSubmitWhileCompacting(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Started: true,
			Preset:  codexcli.PresetYolo,
			Phase:   codexapp.SessionPhaseReconciling,
			Status:  "Compacting conversation history...",
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Preset:      codexcli.PresetYolo,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	input := newCodexTextarea()
	input.SetValue("please continue")

	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          input,
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              24,
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("enter should not submit while compaction is running")
	}
	if len(session.submissions) != 0 {
		t.Fatalf("submissions = %d, want 0", len(session.submissions))
	}
	if got.codexInput.Value() != "please continue" {
		t.Fatalf("composer = %q, want draft preserved", got.codexInput.Value())
	}
	if !strings.Contains(strings.ToLower(got.status), "compacting conversation history") {
		t.Fatalf("status = %q, want compacting guidance", got.status)
	}
}

func TestVisibleCodexCompactSlashUsesStartAndCompletionMessages(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Started: true,
			Preset:  codexcli.PresetYolo,
			Status:  "Codex session ready",
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Preset:      codexcli.PresetYolo,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	input := newCodexTextarea()
	input.SetValue("/compact")

	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          input,
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              24,
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("enter should queue compaction")
	}
	if got.status != "Starting embedded Codex conversation compaction..." {
		t.Fatalf("status = %q, want explicit compaction start message", got.status)
	}
	if session.compactCalls != 0 {
		t.Fatalf("compact calls = %d before cmd runs, want 0", session.compactCalls)
	}

	msg := cmd()
	action, ok := msg.(codexActionMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want codexActionMsg", msg)
	}
	if action.err != nil {
		t.Fatalf("compaction returned error = %v", action.err)
	}
	if action.status != "Embedded Codex conversation compaction completed" {
		t.Fatalf("action status = %q, want explicit compaction completion message", action.status)
	}
	if session.compactCalls != 1 {
		t.Fatalf("compact calls = %d, want 1", session.compactCalls)
	}
}

func TestVisibleCodexAltUpHidesSession(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Started: true,
			Preset:  codexcli.PresetYolo,
			Status:  "Codex session ready",
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Preset:      codexcli.PresetYolo,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          newCodexTextarea(),
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              24,
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyUp, Alt: true})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("alt+up hide should not queue a command")
	}
	if got.codexVisibleProject != "" {
		t.Fatalf("codexVisibleProject = %q, want hidden", got.codexVisibleProject)
	}
	if got.status != "Embedded Codex session hidden." {
		t.Fatalf("status = %q, want hide notice", got.status)
	}
}

func TestVisibleCodexAltUpHidesWithoutBlockingOnContendedLiveState(t *testing.T) {
	projectPath := "/tmp/demo"
	lastActivity := time.Date(2026, 4, 4, 10, 30, 0, 0, time.UTC)
	cached := codexapp.Snapshot{
		ProjectPath:        projectPath,
		Provider:           codexapp.ProviderCodex,
		Started:            true,
		Busy:               true,
		Phase:              codexapp.SessionPhaseRunning,
		ThreadID:           "thread-demo",
		LastActivityAt:     lastActivity,
		LastBusyActivityAt: lastActivity,
		Status:             "Codex is working",
	}
	session := &fakeCodexSession{
		projectPath: projectPath,
		snapshot:    cached,
		trySnapshotFn: func(*fakeCodexSession) (codexapp.Snapshot, bool) {
			return codexapp.Snapshot{}, false
		},
		tryStateSnapshotFn: func(*fakeCodexSession) (codexapp.Snapshot, bool) {
			return codexapp.Snapshot{}, false
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: projectPath,
		Preset:      codexcli.PresetYolo,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	project := model.ProjectSummary{
		Path:                projectPath,
		Name:                "demo",
		PresentOnDisk:       true,
		LastActivity:        lastActivity,
		LatestSessionFormat: "modern",
	}
	m := Model{
		codexManager:        manager,
		codexVisibleProject: projectPath,
		codexHiddenProject:  projectPath,
		codexSnapshots: map[string]codexapp.Snapshot{
			projectPath: cached,
		},
		allProjects:    []model.ProjectSummary{project},
		projects:       []model.ProjectSummary{project},
		detail:         model.ProjectDetail{Summary: project},
		codexInput:     newCodexTextarea(),
		codexViewport:  viewport.New(0, 0),
		detailViewport: viewport.New(0, 0),
		width:          100,
		height:         24,
	}

	updated, _ := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyUp, Alt: true})
	got := updated.(Model)
	if got.codexVisibleProject != "" {
		t.Fatalf("codexVisibleProject = %q, want hidden", got.codexVisibleProject)
	}
	if session.trySnapshotCalls == 0 {
		t.Fatalf("alt+up should try the non-blocking full snapshot before using the cache")
	}
	if session.tryStateSnapshotCalls == 0 {
		t.Fatalf("dashboard rebuild should try non-blocking state before using the cache")
	}
	if session.snapshotCalls != 0 {
		t.Fatalf("alt+up should not call blocking Snapshot(); calls = %d", session.snapshotCalls)
	}
	if session.stateSnapshotCalls != 0 {
		t.Fatalf("alt+up should not call blocking StateSnapshot(); calls = %d", session.stateSnapshotCalls)
	}
}

func TestVisibleClosedLCAgentAltUpHidesWithStaleInputSelection(t *testing.T) {
	m := Model{
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexSnapshots: map[string]codexapp.Snapshot{
			"/tmp/demo": {
				Provider: codexapp.ProviderLCAgent,
				Started:  true,
				Closed:   true,
				Status:   "Closed embedded LCAgent session after 1 hour of inactivity.",
			},
		},
		codexInput:          newCodexTextarea(),
		codexInputSelection: &codexInputSelectionState{},
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              24,
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyUp, Alt: true})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("alt+up hide should not queue a command")
	}
	if got.codexVisibleProject != "" {
		t.Fatalf("codexVisibleProject = %q, want hidden", got.codexVisibleProject)
	}
	if got.codexInputSelection != nil {
		t.Fatalf("codex input selection should be cleared when hiding")
	}
	if got.status != "Embedded LCAgent session hidden." {
		t.Fatalf("status = %q, want LCAgent hide notice", got.status)
	}
}

func TestVisibleCodexEscHidesSession(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Started: true,
			Preset:  codexcli.PresetYolo,
			Status:  "Codex session ready",
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Preset:      codexcli.PresetYolo,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          newCodexTextarea(),
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              24,
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyEsc})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("esc hide should not queue a command")
	}
	if got.codexVisibleProject != "" {
		t.Fatalf("codexVisibleProject = %q, want hidden", got.codexVisibleProject)
	}
	if got.status != "Embedded Codex session hidden." {
		t.Fatalf("status = %q, want hide notice", got.status)
	}
}

func TestVisibleCodexEscCancelsInputSelectionBeforeHiding(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Started: true,
			Preset:  codexcli.PresetYolo,
			Status:  "Codex session ready",
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Preset:      codexcli.PresetYolo,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	input := newCodexTextarea()
	input.SetValue("select me")
	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          input,
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              24,
	}
	m.startCodexInputSelection()

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyEsc})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("esc selection cancel should not queue a command")
	}
	if got.codexVisibleProject != "/tmp/demo" {
		t.Fatalf("codexVisibleProject = %q, want still visible", got.codexVisibleProject)
	}
	if got.codexInputSelectionActive() {
		t.Fatalf("selection should be canceled before hiding")
	}
	if got.status != "Text selection canceled" {
		t.Fatalf("status = %q, want selection cancel notice", got.status)
	}
}

func TestClosedCodexUpdateTriggersScanOnlyOnce(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Started: true,
			Preset:  codexcli.PresetYolo,
			Closed:  true,
			Status:  "Codex app-server exited",
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Preset:      codexcli.PresetYolo,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          newCodexTextarea(),
		codexDrafts:         make(map[string]codexDraft),
		codexClosedHandled:  make(map[string]struct{}),
		codexToolAnswers:    make(map[string]codexToolAnswerState),
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              24,
	}

	updated, _ := m.Update(codexUpdateMsg{projectPath: "/tmp/demo"})
	got := updated.(Model)
	if !got.loading {
		t.Fatalf("first closed-session update should queue a refresh")
	}
	if got.status != "Codex app-server exited" {
		t.Fatalf("status = %q, want closed-session status", got.status)
	}
	if _, ok := got.codexClosedHandled["/tmp/demo"]; !ok {
		t.Fatalf("closed session should be marked as handled")
	}

	got.loading = false
	updated, _ = got.Update(codexUpdateMsg{projectPath: "/tmp/demo"})
	got = updated.(Model)
	if got.loading {
		t.Fatalf("duplicate closed-session updates should not queue another refresh")
	}
}

func TestVisibleCodexF3CyclesLiveSessions(t *testing.T) {
	sessionA := &fakeCodexSession{
		projectPath: "/tmp/a",
		snapshot: codexapp.Snapshot{
			Started:        true,
			Preset:         codexcli.PresetYolo,
			Status:         "Codex session ready",
			LastActivityAt: time.Date(2026, 3, 8, 10, 0, 0, 0, time.UTC),
		},
	}
	sessionB := &fakeCodexSession{
		projectPath: "/tmp/b",
		snapshot: codexapp.Snapshot{
			Started:        true,
			Preset:         codexcli.PresetYolo,
			Status:         "Codex session ready",
			LastActivityAt: time.Date(2026, 3, 8, 10, 5, 0, 0, time.UTC),
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		switch req.ProjectPath {
		case "/tmp/a":
			return sessionA, nil
		case "/tmp/b":
			return sessionB, nil
		default:
			return nil, fmt.Errorf("unexpected project %s", req.ProjectPath)
		}
	})
	for _, projectPath := range []string{"/tmp/a", "/tmp/b"} {
		if _, _, err := manager.Open(codexapp.LaunchRequest{
			ProjectPath: projectPath,
			Preset:      codexcli.PresetYolo,
		}); err != nil {
			t.Fatalf("manager.Open(%q) error = %v", projectPath, err)
		}
	}

	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/a",
		codexHiddenProject:  "/tmp/a",
		codexSnapshots: map[string]codexapp.Snapshot{
			"/tmp/a": sessionA.snapshot,
			"/tmp/b": sessionB.snapshot,
		},
		codexInput:    newCodexTextarea(),
		codexDrafts:   make(map[string]codexDraft),
		codexViewport: viewport.New(0, 0),
		width:         100,
		height:        24,
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyF3})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("f3 should focus the switched Codex composer")
	}
	if got.codexVisibleProject != "/tmp/b" {
		t.Fatalf("codexVisibleProject = %q, want /tmp/b", got.codexVisibleProject)
	}
	if got.status != "Switched to the next embedded Codex session" {
		t.Fatalf("status = %q, want switch notice", got.status)
	}
}

func TestVisibleCodexAltBracketCyclesLiveSessions(t *testing.T) {
	sessionA := &fakeCodexSession{
		projectPath: "/tmp/a",
		snapshot: codexapp.Snapshot{
			Started:        true,
			Preset:         codexcli.PresetYolo,
			Status:         "Codex session ready",
			LastActivityAt: time.Date(2026, 3, 8, 10, 0, 0, 0, time.UTC),
		},
	}
	sessionB := &fakeCodexSession{
		projectPath: "/tmp/b",
		snapshot: codexapp.Snapshot{
			Started:        true,
			Preset:         codexcli.PresetYolo,
			Status:         "Codex session ready",
			LastActivityAt: time.Date(2026, 3, 8, 10, 5, 0, 0, time.UTC),
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		switch req.ProjectPath {
		case "/tmp/a":
			return sessionA, nil
		case "/tmp/b":
			return sessionB, nil
		default:
			return nil, fmt.Errorf("unexpected project %s", req.ProjectPath)
		}
	})
	for _, projectPath := range []string{"/tmp/a", "/tmp/b"} {
		if _, _, err := manager.Open(codexapp.LaunchRequest{
			ProjectPath: projectPath,
			Preset:      codexcli.PresetYolo,
		}); err != nil {
			t.Fatalf("manager.Open(%q) error = %v", projectPath, err)
		}
	}

	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/a",
		codexHiddenProject:  "/tmp/a",
		codexSnapshots: map[string]codexapp.Snapshot{
			"/tmp/a": sessionA.snapshot,
			"/tmp/b": sessionB.snapshot,
		},
		codexInput:    newCodexTextarea(),
		codexDrafts:   make(map[string]codexDraft),
		codexViewport: viewport.New(0, 0),
		width:         100,
		height:        24,
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{']'}, Alt: true})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("alt+] should focus the switched Codex composer")
	}
	if got.codexVisibleProject != "/tmp/b" {
		t.Fatalf("codexVisibleProject = %q, want /tmp/b", got.codexVisibleProject)
	}
	if got.status != "Switched to the next embedded Codex session" {
		t.Fatalf("status = %q, want next-session notice", got.status)
	}

	updated, cmd = got.updateCodexMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'['}, Alt: true})
	got = updated.(Model)
	if cmd == nil {
		t.Fatalf("alt+[ should focus the switched Codex composer")
	}
	if got.codexVisibleProject != "/tmp/a" {
		t.Fatalf("codexVisibleProject = %q, want /tmp/a", got.codexVisibleProject)
	}
	if got.status != "Switched to the previous embedded Codex session" {
		t.Fatalf("status = %q, want previous-session notice", got.status)
	}
}

func TestVisibleCodexAltLCyclesDenseBlockModes(t *testing.T) {
	m := Model{
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexSnapshots: map[string]codexapp.Snapshot{
			"/tmp/demo": {
				Started: true,
				Status:  "Codex session ready",
				Entries: []codexapp.TranscriptEntry{{
					Kind: codexapp.TranscriptCommand,
					Text: "$ demo\nline 1\nline 2\nline 3\nline 4\nline 5\nline 6",
				}},
			},
		},
		codexInput:    newCodexTextarea(),
		codexViewport: viewport.New(0, 0),
		width:         100,
		height:        24,
	}

	for _, want := range []struct {
		mode   codexDenseBlockMode
		status string
	}{
		{codexDenseBlockPreview, "Showing short transcript block previews"},
		{codexDenseBlockFull, "Showing full transcript blocks"},
		{codexDenseBlockSummary, "Hiding transcript block output"},
	} {
		updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'l'}, Alt: true})
		if cmd != nil {
			t.Fatalf("alt+l should not return an async command")
		}
		got := updated.(Model)
		if got.codexDenseBlockMode != want.mode {
			t.Fatalf("codexDenseBlockMode = %v, want %v", got.codexDenseBlockMode, want.mode)
		}
		if got.status != want.status {
			t.Fatalf("status = %q, want %q", got.status, want.status)
		}
		m = got
	}
}

func TestVisibleCodexAltUpReturnsToLastEmbeddedProjectSelection(t *testing.T) {
	sessionA := &fakeCodexSession{
		projectPath: "/tmp/a",
		snapshot: codexapp.Snapshot{
			Started:        true,
			Preset:         codexcli.PresetYolo,
			Status:         "Codex session ready",
			LastActivityAt: time.Date(2026, 3, 8, 10, 0, 0, 0, time.UTC),
		},
	}
	sessionB := &fakeCodexSession{
		projectPath: "/tmp/b",
		snapshot: codexapp.Snapshot{
			Started:        true,
			Preset:         codexcli.PresetYolo,
			Status:         "Codex session ready",
			LastActivityAt: time.Date(2026, 3, 8, 10, 5, 0, 0, time.UTC),
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		switch req.ProjectPath {
		case "/tmp/a":
			return sessionA, nil
		case "/tmp/b":
			return sessionB, nil
		default:
			return nil, fmt.Errorf("unexpected project %s", req.ProjectPath)
		}
	})
	for _, projectPath := range []string{"/tmp/a", "/tmp/b"} {
		if _, _, err := manager.Open(codexapp.LaunchRequest{
			ProjectPath: projectPath,
			Preset:      codexcli.PresetYolo,
		}); err != nil {
			t.Fatalf("manager.Open(%q) error = %v", projectPath, err)
		}
	}

	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/a",
		codexHiddenProject:  "/tmp/a",
		codexSnapshots: map[string]codexapp.Snapshot{
			"/tmp/a": sessionA.snapshot,
			"/tmp/b": sessionB.snapshot,
		},
		codexInput:    newCodexTextarea(),
		codexDrafts:   make(map[string]codexDraft),
		codexViewport: viewport.New(0, 0),
		projects: []model.ProjectSummary{
			{Path: "/tmp/a", Name: "a", PresentOnDisk: true},
			{Path: "/tmp/b", Name: "b", PresentOnDisk: true},
		},
		selected: 0,
		width:    100,
		height:   24,
	}

	updated, cycleCmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{']'}, Alt: true})
	got := updated.(Model)
	if cycleCmd == nil {
		t.Fatalf("alt+] should queue the session switch work")
	}
	if got.codexVisibleProject != "/tmp/b" {
		t.Fatalf("codexVisibleProject = %q, want /tmp/b", got.codexVisibleProject)
	}
	if project, ok := got.selectedProject(); !ok || project.Path != "/tmp/b" {
		t.Fatalf("selected project after alt+] = %#v, want /tmp/b", project)
	}

	updated, hideCmd := got.updateCodexMode(tea.KeyMsg{Type: tea.KeyEsc})
	got = updated.(Model)
	if hideCmd == nil && !got.detailReloadQueued["/tmp/b"] {
		t.Fatalf("esc should refresh or queue a detail refresh for the last embedded project")
	}
	if got.codexVisibleProject != "" {
		t.Fatalf("codexVisibleProject = %q, want hidden", got.codexVisibleProject)
	}
	if got.codexHiddenProject != "/tmp/b" {
		t.Fatalf("codexHiddenProject = %q, want /tmp/b", got.codexHiddenProject)
	}
	if project, ok := got.selectedProject(); !ok || project.Path != "/tmp/b" {
		t.Fatalf("selected project after esc = %#v, want /tmp/b", project)
	}
}

func TestAltDownNoLongerOpensCodexSessionPicker(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Started:        true,
			Preset:         codexcli.PresetYolo,
			ThreadID:       "thread-demo",
			LastActivityAt: time.Date(2026, 3, 8, 12, 0, 0, 0, time.UTC),
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Preset:      codexcli.PresetYolo,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	m := Model{
		codexManager: manager,
		allProjects: []model.ProjectSummary{
			{
				Path:                "/tmp/demo",
				Name:                "demo",
				PresentOnDisk:       true,
				LatestSessionID:     "thread-demo",
				LatestSessionFormat: "modern",
				LastActivity:        time.Date(2026, 3, 8, 12, 0, 0, 0, time.UTC),
			},
		},
		codexInput:    newCodexTextarea(),
		codexViewport: viewport.New(0, 0),
		width:         100,
		height:        24,
	}

	updated, cmd := m.updateNormalMode(tea.KeyMsg{Type: tea.KeyDown, Alt: true})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("alt+down should not queue a command")
	}
	if got.codexPickerVisible {
		t.Fatalf("alt+down should no longer open the obsolete embedded session picker")
	}
	if got.status == "Embedded session picker open" {
		t.Fatalf("alt+down should not report picker-open status")
	}
}

func TestOpenCodexSessionChoiceLaunchesOpenCodeResume(t *testing.T) {
	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider: codexapp.ProviderOpenCode,
				Started:  true,
				ThreadID: req.ResumeID,
				Status:   "OpenCode session ready",
			},
		}, nil
	})

	m := Model{
		codexManager:  manager,
		codexInput:    newCodexTextarea(),
		codexViewport: viewport.New(0, 0),
		width:         100,
		height:        24,
	}

	updated, cmd := m.openCodexSessionChoice(codexSessionChoice{
		ProjectPath:  "/tmp/demo",
		ProjectName:  "demo",
		SessionID:    "ses_open",
		Provider:     codexapp.ProviderOpenCode,
		LastActivity: time.Date(2026, 3, 8, 12, 0, 0, 0, time.UTC),
	})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("openCodexSessionChoice() should return an open command")
	}
	if got.codexPendingOpen == nil || got.codexPendingOpen.provider != codexapp.ProviderOpenCode {
		t.Fatalf("codexPendingOpen = %#v, want pending OpenCode session", got.codexPendingOpen)
	}

	msg := cmd()
	opened, ok := msg.(codexSessionOpenedMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want codexSessionOpenedMsg", msg)
	}
	if opened.err != nil {
		t.Fatalf("open command returned error = %v", opened.err)
	}
	if len(requests) != 1 {
		t.Fatalf("launch requests = %d, want 1", len(requests))
	}
	if requests[0].Provider != codexapp.ProviderOpenCode {
		t.Fatalf("provider = %q, want %q", requests[0].Provider, codexapp.ProviderOpenCode)
	}
	if requests[0].ResumeID != "ses_open" {
		t.Fatalf("resume id = %q, want %q", requests[0].ResumeID, "ses_open")
	}
	if requests[0].Preset != codexcli.PresetYolo {
		t.Fatalf("preset = %q, want %q for OpenCode", requests[0].Preset, codexcli.PresetYolo)
	}
}

func TestNormalModeEnterOpensPreferredOpenCodeSession(t *testing.T) {
	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider: codexapp.ProviderOpenCode,
				Started:  true,
				ThreadID: req.ResumeID,
				Status:   "OpenCode session ready",
			},
		}, nil
	})

	project := model.ProjectSummary{
		Path:                "/tmp/demo",
		Name:                "demo",
		PresentOnDisk:       true,
		LatestSessionID:     "latest-open",
		LatestSessionFormat: "opencode_db",
	}
	m := Model{
		codexManager: manager,
		projects:     []model.ProjectSummary{project},
		selected:     0,
		focusedPane:  focusProjects,
		detail: model.ProjectDetail{
			Summary: model.ProjectSummary{Path: "/tmp/demo"},
			Sessions: []model.SessionEvidence{
				{SessionID: "ses_open", Format: "opencode_db"},
				{SessionID: "cx_old", Format: "modern"},
			},
		},
		codexInput:    newCodexTextarea(),
		codexViewport: viewport.New(0, 0),
		width:         100,
		height:        24,
	}

	updated, cmd := m.updateNormalMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("enter should return an open command")
	}
	if got.codexPendingOpen == nil || got.codexPendingOpen.provider != codexapp.ProviderOpenCode {
		t.Fatalf("codexPendingOpen = %#v, want pending OpenCode session", got.codexPendingOpen)
	}

	msg := cmd()
	opened, ok := msg.(codexSessionOpenedMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want codexSessionOpenedMsg", msg)
	}
	if opened.err != nil {
		t.Fatalf("open command returned error = %v", opened.err)
	}
	if len(requests) != 1 {
		t.Fatalf("launch requests = %d, want 1", len(requests))
	}
	if requests[0].Provider != codexapp.ProviderOpenCode {
		t.Fatalf("provider = %q, want %q", requests[0].Provider, codexapp.ProviderOpenCode)
	}
	if requests[0].ResumeID != "ses_open" {
		t.Fatalf("resume id = %q, want %q", requests[0].ResumeID, "ses_open")
	}
}

func TestNormalModeEnterPrefersLiveEmbeddedProviderOverStoredLatestProvider(t *testing.T) {
	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider: req.Provider.Normalized(),
				Started:  true,
				ThreadID: "thread-live",
				Status:   req.Provider.Label() + " session ready",
			},
		}, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Provider:    codexapp.ProviderCodex,
		Preset:      codexcli.PresetYolo,
		ResumeID:    "thread-live",
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	project := model.ProjectSummary{
		Path:                "/tmp/demo",
		Name:                "demo",
		PresentOnDisk:       true,
		LatestSessionID:     "cc-old",
		LatestSessionFormat: "claude_code",
	}
	m := Model{
		codexManager:  manager,
		projects:      []model.ProjectSummary{project},
		selected:      0,
		focusedPane:   focusProjects,
		codexInput:    newCodexTextarea(),
		codexDrafts:   make(map[string]codexDraft),
		codexViewport: viewport.New(0, 0),
		width:         100,
		height:        24,
	}

	updated, cmd := m.updateNormalMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("enter should return a show-session command")
	}
	if got.codexVisibleProject != "/tmp/demo" {
		t.Fatalf("codexVisibleProject = %q, want /tmp/demo", got.codexVisibleProject)
	}
	if got.status != "Embedded Codex session reopened. Alt+Up hides it." {
		t.Fatalf("status = %q, want live Codex reopen status", got.status)
	}
	if len(requests) != 1 {
		t.Fatalf("launch requests = %d, want only the original live Codex open", len(requests))
	}
}

func TestNormalModeEnterReusesLiveEmbeddedSessionWhenSnapshotIsContended(t *testing.T) {
	var requests []codexapp.LaunchRequest
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Provider: codexapp.ProviderCodex,
			Started:  true,
			ThreadID: "thread-live",
			Status:   "Codex session ready",
		},
		trySnapshotFn: func(*fakeCodexSession) (codexapp.Snapshot, bool) {
			return codexapp.Snapshot{}, false
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Provider:    codexapp.ProviderCodex,
		Preset:      codexcli.PresetYolo,
		ResumeID:    "thread-live",
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	m := Model{
		codexManager: manager,
		projects: []model.ProjectSummary{{
			Path:                "/tmp/demo",
			Name:                "demo",
			PresentOnDisk:       true,
			LatestSessionID:     "thread-stale",
			LatestSessionFormat: "modern",
		}},
		selected:       0,
		focusedPane:    focusProjects,
		codexInput:     newCodexTextarea(),
		codexDrafts:    make(map[string]codexDraft),
		codexSnapshots: make(map[string]codexapp.Snapshot),
		codexViewport:  viewport.New(0, 0),
		width:          100,
		height:         24,
	}

	updated, cmd := m.updateNormalMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if got.codexVisibleProject != "/tmp/demo" {
		t.Fatalf("codexVisibleProject = %q, want /tmp/demo", got.codexVisibleProject)
	}
	if got.status != "Embedded Codex session reopened. Alt+Up hides it." {
		t.Fatalf("status = %q, want live Codex reopen status", got.status)
	}
	if len(requests) != 1 {
		t.Fatalf("launch requests = %d, want no replacement open after the original session", len(requests))
	}
	if cmd == nil {
		t.Fatalf("showing a contended live session should queue a deferred snapshot command")
	}
}
