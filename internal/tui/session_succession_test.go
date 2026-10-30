package tui

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lcroom/internal/codexapp"
	"lcroom/internal/codexcli"
	"lcroom/internal/config"
	"lcroom/internal/control"
	"lcroom/internal/events"
	"lcroom/internal/model"
	"lcroom/internal/service"
	"lcroom/internal/store"

	"github.com/charmbracelet/bubbles/viewport"
)

func handoffSuccessionFixture(t *testing.T, freshThreadID string) (Model, *store.Store, *service.Service, *[]codexapp.LaunchRequest, codexapp.Snapshot) {
	t.Helper()
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	st, err := store.Open(filepath.Join(cfg.DataDir, "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	svc := service.New(cfg, st, events.NewBus(), nil)
	projectPath := filepath.Clean(t.TempDir())
	sourceSnapshot := codexapp.Snapshot{
		Provider:          codexapp.ProviderCodex,
		Started:           true,
		ThreadID:          "thread-organizer-old",
		ControlSessionKey: "old-control-key",
		Preset:            codexcli.PresetYolo,
		Entries:           []codexapp.TranscriptEntry{{Kind: codexapp.TranscriptUser, Text: "Coordinate the TFA worktrees."}},
	}
	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, _ func()) (codexapp.Session, error) {
		requests = append(requests, req)
		if len(requests) == 1 {
			return &fakeCodexSession{projectPath: req.ProjectPath, snapshot: sourceSnapshot}, nil
		}
		return &fakeCodexSession{projectPath: req.ProjectPath, snapshot: codexapp.Snapshot{
			Provider: req.Provider, Started: true, ThreadID: freshThreadID, ControlSessionKey: req.TodoCaptureSessionKey, Preset: req.Preset,
		}}, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{ProjectPath: projectPath, Provider: codexapp.ProviderCodex, ResumeID: "thread-organizer-old", Preset: codexcli.PresetYolo}); err != nil {
		t.Fatal(err)
	}
	m := Model{
		svc:                 svc,
		codexManager:        manager,
		codexVisibleProject: projectPath,
		codexHiddenProject:  projectPath,
		codexInput:          newCodexTextarea(),
		codexViewport:       viewport.New(0, 0),
		appDataDirPath:      cfg.DataDir,
		nowFn:               func() time.Time { return time.Date(2026, time.October, 24, 22, 0, 0, 0, time.UTC) },
		width:               100,
		height:              24,
	}
	return m, st, svc, &requests, sourceSnapshot
}

func queueReportForOldOrganizer(t *testing.T, st *store.Store, projectPath string) control.EngineerMessage {
	t.Helper()
	message, err := st.CreateEngineerMessage(context.Background(), control.EngineerMessage{
		OperationID: "engineer-dispatch-reply:1:1", ProjectPath: projectPath, Provider: control.ProviderCodex,
		SessionMode: control.SessionModeResumeOrNew, TargetSessionID: "thread-organizer-old", Prompt: "Overnight Vulkan queue finished.",
	})
	if err != nil {
		t.Fatal(err)
	}
	return message
}

func TestHandoffForwardsOldSessionReportsToFreshSession(t *testing.T) {
	m, st, _, requests, source := handoffSuccessionFixture(t, "thread-organizer-new")
	report := queueReportForOldOrganizer(t, st, m.codexVisibleProject)

	opened, ok := m.handoffVisibleCodexSessionToSelectionCmd(source, "", codexapp.ProviderCodex, codexapp.ModelOption{}, "", false)().(codexSessionOpenedMsg)
	if !ok || opened.err != nil {
		t.Fatalf("handoff result = %#v", opened)
	}
	if len(*requests) != 2 || (*requests)[1].TodoCaptureSessionKey == "" || (*requests)[1].TodoCaptureSessionKey == "old-control-key" {
		t.Fatalf("fresh launch should carry its own pre-assigned control key: %#v", *requests)
	}
	if !strings.Contains(opened.status, "addressed to the previous session now come here") {
		t.Fatalf("handoff status = %q", opened.status)
	}
	forwarded, err := st.GetEngineerMessage(context.Background(), report.ID)
	if err != nil || forwarded.TargetSessionID != "thread-organizer-new" || forwarded.RequestedTargetSessionID != "thread-organizer-old" {
		t.Fatalf("report after handoff = %#v err=%v", forwarded, err)
	}
}

func TestHandoffCompletesWhenSuccessorAnnouncesIdentityLater(t *testing.T) {
	m, st, svc, requests, source := handoffSuccessionFixture(t, "")
	report := queueReportForOldOrganizer(t, st, m.codexVisibleProject)

	opened, ok := m.handoffVisibleCodexSessionToSelectionCmd(source, "", codexapp.ProviderCodex, codexapp.ModelOption{}, "", false)().(codexSessionOpenedMsg)
	if !ok || opened.err != nil {
		t.Fatalf("handoff result = %#v", opened)
	}
	if !strings.Contains(opened.status, "will move here once this session starts") {
		t.Fatalf("handoff status = %q", opened.status)
	}
	if queued, _ := st.ListQueuedEngineerMessages(context.Background(), 10); len(queued) != 0 {
		t.Fatalf("report deliverable to the replaced session before the successor started: %#v", queued)
	}
	if err := svc.RecordEmbeddedSessionIdentity(context.Background(), service.EmbeddedSessionActivity{
		ProjectPath: m.codexVisibleProject, Source: model.SessionSourceCodex,
		ControlSessionKey: (*requests)[1].TodoCaptureSessionKey, SessionID: "thread-organizer-new",
	}); err != nil {
		t.Fatal(err)
	}
	forwarded, err := st.GetEngineerMessage(context.Background(), report.ID)
	if err != nil || forwarded.TargetSessionID != "thread-organizer-new" {
		t.Fatalf("report after identity = %#v err=%v", forwarded, err)
	}
}

func TestHandoffRefusedForSharedTaskWorker(t *testing.T) {
	m := Model{openAgentTasks: []model.AgentTask{{ID: "agt_perf", WorkspacePath: "/tmp/task-worker"}}}
	if reason := m.handoffBlockedReason("/tmp/task-worker"); !strings.Contains(reason, "delegated task's worker session") {
		t.Fatalf("task worker handoff reason = %q", reason)
	}
	if reason := m.handoffBlockedReason("/tmp/organizer"); reason != "" {
		t.Fatalf("organizer handoff blocked: %q", reason)
	}
}

func TestHandoffAllowsIdleIsolatedTaskWorker(t *testing.T) {
	const path = "/tmp/task-worker"
	m := Model{openAgentTasks: []model.AgentTask{{ID: "agt_isolated", WorkspacePath: path, Repository: model.AgentTaskRepository{Isolated: true}}},
		codexSnapshots: map[string]codexapp.Snapshot{path: {Started: true, Provider: codexapp.ProviderCodex}},
	}
	if reason := m.handoffBlockedReason(path); reason != "" {
		t.Fatalf("idle isolated task blocked: %s", reason)
	}
	m.codexSnapshots[path] = codexapp.Snapshot{Started: true, Busy: true, Provider: codexapp.ProviderCodex}
	if reason := m.handoffBlockedReason(path); reason == "" {
		t.Fatal("busy isolated task handoff allowed")
	}
}
