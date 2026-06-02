package tui

import (
	"context"
	"fmt"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"lcroom/internal/codexapp"
	"lcroom/internal/codexcli"
	"lcroom/internal/config"
	"lcroom/internal/events"
	"lcroom/internal/model"
	"lcroom/internal/projectrun"
	"lcroom/internal/service"
	"lcroom/internal/store"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFKeyOpensProjectFilterDialog(t *testing.T) {
	m := Model{
		projects: []model.ProjectSummary{{
			Path:          "/tmp/missing",
			Name:          "missing",
			PresentOnDisk: false,
		}},
		width:    100,
		height:   24,
		selected: 0,
	}

	updated, cmd := m.updateNormalMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	got := updated.(Model)
	if got.projectFilterDialog == nil {
		t.Fatalf("f key should open the project filter dialog")
	}
	if got.status != "Project filter open. Type to narrow, Enter keep, Esc close" {
		t.Fatalf("status = %q, want filter dialog status", got.status)
	}
	if cmd == nil {
		t.Fatalf("f key should return a focus command")
	}
}

func TestLowercaseBKeyOpensBossMode(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.BossChatBackend = config.AIBackendOpenAIAPI
	settings.OpenAIAPIKey = "sk-test-example"

	m := Model{
		settingsBaseline: &settings,
		width:            100,
		height:           24,
	}

	updated, cmd := m.updateNormalMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}})
	got := updated.(Model)
	if !got.bossMode {
		t.Fatalf("lowercase b key should open boss mode")
	}
	if got.bossSetupPrompt != nil {
		t.Fatalf("configured lowercase b key should not show setup prompt")
	}
	if cmd == nil {
		t.Fatalf("lowercase b key should return the boss init command")
	}
}

func TestSKeyNoLongerSnoozesProject(t *testing.T) {
	m := Model{
		projects: []model.ProjectSummary{{
			Path:          "/tmp/demo",
			Name:          "demo",
			PresentOnDisk: true,
		}},
		selected: 0,
		width:    100,
		height:   24,
	}

	updated, cmd := m.updateNormalMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	if cmd != nil {
		t.Fatalf("lowercase s should no longer enqueue a snooze command")
	}
	got := updated.(Model)
	if got.status != "" {
		t.Fatalf("lowercase s should not change status, got %q", got.status)
	}
}

func TestSUppercaseNoLongerClearsSnooze(t *testing.T) {
	m := Model{
		projects: []model.ProjectSummary{{Path: "/tmp/demo", Name: "demo", PresentOnDisk: true}},
		selected: 0,
		width:    100,
		height:   24,
	}

	updated, cmd := m.updateNormalMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'S'}})
	if cmd != nil {
		t.Fatalf("uppercase S should no longer enqueue a clear-snooze command")
	}
	got := updated.(Model)
	if got.status != "" {
		t.Fatalf("uppercase S should not change status, got %q", got.status)
	}
}

func TestEnterLaunchesCodexFromFocusedProjectList(t *testing.T) {
	m := Model{
		projects: []model.ProjectSummary{
			{
				Path:                "/tmp/demo",
				Name:                "demo",
				PresentOnDisk:       true,
				LatestSessionID:     "cx_summary",
				LatestSessionFormat: "modern",
			},
		},
		selected:    0,
		focusedPane: focusProjects,
		width:       100,
		height:      24,
	}

	updated, cmd := m.updateNormalMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("enter should return an embedded Codex open command when the list is focused")
	}
	if got.status != "Opening embedded Codex session..." {
		t.Fatalf("status = %q, want embedded open notice", got.status)
	}
}

func TestEnterRestoresHiddenLiveCodexSessionFromFocusedProjectList(t *testing.T) {
	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
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

	m := Model{
		codexManager: manager,
		projects: []model.ProjectSummary{
			{
				Path:                "/tmp/demo",
				Name:                "demo",
				PresentOnDisk:       true,
				LatestSessionID:     "thread-stale",
				LatestSessionFormat: "modern",
			},
		},
		selected:           0,
		focusedPane:        focusProjects,
		codexHiddenProject: "/tmp/demo",
		detail: model.ProjectDetail{
			Summary: model.ProjectSummary{Path: "/tmp/demo"},
			Sessions: []model.SessionEvidence{
				{SessionID: "thread-stale", Format: "modern"},
			},
		},
		codexInput:    newCodexTextarea(),
		codexDrafts:   make(map[string]codexDraft),
		codexViewport: viewport.New(0, 0),
		width:         100,
		height:        24,
	}

	updated, cmd := m.updateNormalMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("enter should restore the hidden embedded Codex session")
	}
	if got.codexVisibleProject != "/tmp/demo" {
		t.Fatalf("codexVisibleProject = %q, want /tmp/demo", got.codexVisibleProject)
	}
	if got.codexPendingOpen != nil {
		t.Fatalf("codexPendingOpen = %#v, want nil when restoring a live session", got.codexPendingOpen)
	}
	if got.status != "Embedded Codex session reopened. Alt+Up hides it." {
		t.Fatalf("status = %q, want live restore notice", got.status)
	}
	if len(requests) != 1 {
		t.Fatalf("launch requests = %d, want 1 because reopening should not launch again", len(requests))
	}
}

func TestEnterReopensClosedEmbeddedSessionByLaunchingAgain(t *testing.T) {
	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider: req.Provider.Normalized(),
				Started:  true,
				ThreadID: req.ResumeID,
				Status:   req.Provider.Label() + " session ready",
			},
		}, nil
	})
	session, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Provider:    codexapp.ProviderCodex,
		Preset:      codexcli.PresetYolo,
		ResumeID:    "thread-closed",
	})
	if err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("session.Close() error = %v", err)
	}

	m := Model{
		codexManager: manager,
		projects: []model.ProjectSummary{{
			Path:                "/tmp/demo",
			Name:                "demo",
			PresentOnDisk:       true,
			LatestSessionID:     "thread-closed",
			LatestSessionFormat: "modern",
		}},
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
		t.Fatalf("enter should launch a replacement for a closed embedded session")
	}
	if got.codexPendingOpen == nil || got.codexPendingOpen.projectPath != "/tmp/demo" {
		t.Fatalf("codexPendingOpen = %#v, want pending replacement open", got.codexPendingOpen)
	}
	if got.codexVisibleProject == "/tmp/demo" {
		t.Fatalf("codexVisibleProject = %q, want closed session not to be merely reopened", got.codexVisibleProject)
	}
	if got.status != "Opening embedded Codex session..." {
		t.Fatalf("status = %q, want embedded open notice", got.status)
	}

	msg := cmd()
	opened, ok := msg.(codexSessionOpenedMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want codexSessionOpenedMsg", msg)
	}
	if opened.err != nil {
		t.Fatalf("replacement open returned error = %v", opened.err)
	}
	if len(requests) != 2 {
		t.Fatalf("launch requests = %d, want original plus replacement open", len(requests))
	}
	if requests[1].ResumeID != "thread-closed" {
		t.Fatalf("replacement resume id = %q, want thread-closed", requests[1].ResumeID)
	}
}

func TestShowCodexProjectQueuesDeferredSnapshotWhenRevealSnapshotIsContended(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Provider: codexapp.ProviderCodex,
			Started:  true,
			ThreadID: "thread-demo",
			Status:   "Codex session ready",
		},
		trySnapshotFn: func(*fakeCodexSession) (codexapp.Snapshot, bool) {
			return codexapp.Snapshot{}, false
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
		projects: []model.ProjectSummary{{
			Path:          "/tmp/demo",
			Name:          "demo",
			PresentOnDisk: true,
		}},
		selected:      0,
		codexInput:    newCodexTextarea(),
		codexDrafts:   make(map[string]codexDraft),
		codexViewport: viewport.New(0, 0),
		width:         100,
		height:        24,
	}

	updated, cmd := m.showCodexProject("/tmp/demo", "Embedded session restored")
	got := updated.(Model)
	if got.codexVisibleProject != "/tmp/demo" {
		t.Fatalf("codexVisibleProject = %q, want /tmp/demo", got.codexVisibleProject)
	}
	if got.codexHiddenProject != "/tmp/demo" {
		t.Fatalf("codexHiddenProject = %q, want /tmp/demo", got.codexHiddenProject)
	}
	if session.trySnapshotCalls != 1 {
		t.Fatalf("reveal should attempt exactly one non-blocking snapshot refresh, got %d", session.trySnapshotCalls)
	}
	if session.snapshotCalls != 0 {
		t.Fatalf("showCodexProject() should not take a blocking snapshot on the UI thread; snapshot calls = %d", session.snapshotCalls)
	}
	if cmd == nil {
		t.Fatalf("showCodexProject() should queue follow-up commands")
	}

	msgs := collectCmdMsgs(cmd)
	var deferred codexDeferredSnapshotMsg
	foundDeferred := false
	for _, msg := range msgs {
		if candidate, ok := msg.(codexDeferredSnapshotMsg); ok {
			deferred = candidate
			foundDeferred = true
			break
		}
	}
	if !foundDeferred {
		t.Fatalf("showCodexProject() should queue a deferred snapshot when reveal-time TrySnapshot is contended, got %#v", msgs)
	}
	if deferred.projectPath != "/tmp/demo" {
		t.Fatalf("deferred project path = %q, want /tmp/demo", deferred.projectPath)
	}
	if deferred.snapshot.ThreadID != "thread-demo" {
		t.Fatalf("deferred snapshot thread id = %q, want %q", deferred.snapshot.ThreadID, "thread-demo")
	}
	if session.snapshotCalls != 1 {
		t.Fatalf("deferred snapshot command should perform one blocking snapshot off the UI thread; snapshot calls = %d", session.snapshotCalls)
	}
}

func TestEnterDoesNotLaunchCodexFromDetailPane(t *testing.T) {
	m := Model{
		projects: []model.ProjectSummary{
			{
				Path:          "/tmp/demo",
				Name:          "demo",
				PresentOnDisk: true,
			},
		},
		selected:    0,
		focusedPane: focusDetail,
		width:       100,
		height:      24,
		status:      "Focus: detail pane",
	}

	updated, cmd := m.updateNormalMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("enter should not launch Codex when the detail pane is focused")
	}
	if got.status != "Focus: detail pane" {
		t.Fatalf("status = %q, want unchanged detail focus status", got.status)
	}
}

func TestSyncDetailViewportSkipsHiddenDashboardWhileCodexVisible(t *testing.T) {
	m := Model{
		projects: []model.ProjectSummary{{
			Path:                          "/tmp/demo",
			Name:                          "demo",
			PresentOnDisk:                 true,
			LatestCompletedSessionSummary: "fresh hidden summary",
		}},
		selected:            0,
		codexVisibleProject: "/tmp/demo",
		detail: model.ProjectDetail{
			Summary: model.ProjectSummary{Path: "/tmp/demo"},
		},
		runtimeSnapshots: map[string]projectrun.Snapshot{
			"/tmp/demo": {
				ProjectPath:  "/tmp/demo",
				RecentOutput: []string{"fresh runtime output"},
			},
		},
		detailViewport:  viewport.New(20, 5),
		runtimeViewport: viewport.New(20, 5),
		width:           100,
		height:          24,
	}
	m.detailViewport.SetContent("stale detail cache")
	m.runtimeViewport.SetContent("stale runtime cache")

	m.syncDetailViewport(false)

	if got := ansi.Strip(m.detailViewport.View()); !strings.Contains(got, "stale detail cache") {
		t.Fatalf("hidden detail sync should keep the cached detail viewport content, got %q", got)
	} else if strings.Contains(got, "fresh hidden summary") {
		t.Fatalf("hidden detail sync should not eagerly rebuild dashboard content while Codex is visible, got %q", got)
	}
	if got := ansi.Strip(m.runtimeViewport.View()); !strings.Contains(got, "stale runtime cache") {
		t.Fatalf("hidden detail sync should leave the cached runtime viewport content alone, got %q", got)
	} else if strings.Contains(got, "fresh runtime output") {
		t.Fatalf("hidden detail sync should not eagerly rebuild the runtime viewport while Codex is visible, got %q", got)
	}
}

func TestHideCodexSessionResyncsDashboardPanes(t *testing.T) {
	seenAt := time.Date(2026, 4, 4, 12, 0, 0, 0, time.UTC)
	m := Model{
		projects: []model.ProjectSummary{{
			Path:                            "/tmp/demo",
			Name:                            "demo",
			PresentOnDisk:                   true,
			LatestCompletedSessionSummary:   "fresh summary after hide",
			LatestSessionClassification:     model.ClassificationCompleted,
			LatestSessionClassificationType: model.SessionCategoryCompleted,
			LatestSessionFormat:             "modern",
			LatestSessionLastEventAt:        seenAt.Add(-2 * time.Minute),
			LatestTurnStateKnown:            true,
			LatestTurnCompleted:             true,
		}},
		selected:            0,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexSnapshots: map[string]codexapp.Snapshot{
			"/tmp/demo": {
				ProjectPath: "/tmp/demo",
				Started:     true,
				Status:      "Codex session ready",
			},
		},
		codexInput: newCodexTextarea(),
		detail: model.ProjectDetail{
			Summary: model.ProjectSummary{
				Path:                            "/tmp/demo",
				LatestSessionClassification:     model.ClassificationCompleted,
				LatestSessionClassificationType: model.SessionCategoryCompleted,
				LatestSessionFormat:             "modern",
				LatestSessionLastEventAt:        seenAt.Add(-2 * time.Minute),
				LatestTurnStateKnown:            true,
				LatestTurnCompleted:             true,
			},
		},
		runtimeSnapshots: map[string]projectrun.Snapshot{
			"/tmp/demo": {
				ProjectPath:  "/tmp/demo",
				RecentOutput: []string{"runtime after hide"},
			},
		},
		detailViewport:  viewport.New(20, 5),
		runtimeViewport: viewport.New(20, 5),
		width:           100,
		height:          24,
		nowFn:           func() time.Time { return seenAt },
	}
	m.detailViewport.SetContent("old detail cache")
	m.runtimeViewport.SetContent("old runtime cache")

	updated, _ := m.hideCodexSession()
	got := updated.(Model)

	if got.codexVisibleProject != "" {
		t.Fatalf("codexVisibleProject = %q, want hidden", got.codexVisibleProject)
	}
	if got := ansi.Strip(got.detailViewport.View()); !strings.Contains(got, "fresh summary after hide") {
		t.Fatalf("hiding Codex should resync the detail viewport before returning to the dashboard, got %q", got)
	}
	if got := ansi.Strip(got.runtimeViewport.View()); !strings.Contains(got, "runtime after hide") {
		t.Fatalf("hiding Codex should resync the runtime viewport before returning to the dashboard, got %q", got)
	}
	if !got.projects[0].LastSessionSeenAt.Equal(seenAt) {
		t.Fatalf("projects seen_at = %v, want %v", got.projects[0].LastSessionSeenAt, seenAt)
	}
	if !got.detail.Summary.LastSessionSeenAt.Equal(seenAt) {
		t.Fatalf("detail seen_at = %v, want %v", got.detail.Summary.LastSessionSeenAt, seenAt)
	}
}

func TestHideCodexSessionRefreshesProjectStatusWhenIdle(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	projectPath := filepath.Join(root, "repo")
	runTUITestGit(t, "", "init", projectPath)
	runTUITestGit(t, projectPath, "config", "user.name", "Little Control Room Tests")
	runTUITestGit(t, projectPath, "config", "user.email", "tests@example.com")
	if err := os.WriteFile(filepath.Join(projectPath, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write README.md: %v", err)
	}
	runTUITestGit(t, projectPath, "add", "README.md")
	runTUITestGit(t, projectPath, "commit", "-m", "initial commit")

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
		RepoBranch:    "master",
		RepoDirty:     false,
		UpdatedAt:     time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed clean project state: %v", err)
	}

	if err := os.WriteFile(filepath.Join(projectPath, "README.md"), []byte("hello\nchanged\n"), 0o644); err != nil {
		t.Fatalf("update README.md: %v", err)
	}

	svc := service.New(config.Default(), st, events.NewBus(), nil)
	m := Model{
		ctx: ctx,
		svc: svc,
		projects: []model.ProjectSummary{{
			Path:          projectPath,
			Name:          "repo",
			PresentOnDisk: true,
			RepoBranch:    "master",
		}},
		allProjects: []model.ProjectSummary{{
			Path:          projectPath,
			Name:          "repo",
			PresentOnDisk: true,
			RepoBranch:    "master",
		}},
		selected:            0,
		codexVisibleProject: projectPath,
		codexHiddenProject:  projectPath,
		codexSnapshots: map[string]codexapp.Snapshot{
			projectPath: {
				ProjectPath: projectPath,
				Started:     true,
				Status:      "Codex session ready",
			},
		},
		codexInput:      newCodexTextarea(),
		detailViewport:  viewport.New(20, 5),
		runtimeViewport: viewport.New(20, 5),
		width:           100,
		height:          24,
		detail: model.ProjectDetail{
			Summary: model.ProjectSummary{
				Path:          projectPath,
				Name:          "repo",
				PresentOnDisk: true,
				RepoBranch:    "master",
			},
		},
	}

	updated, cmd := m.hideCodexSession()
	got := updated.(Model)
	if cmd == nil {
		t.Fatal("hideCodexSession() should queue follow-up work")
	}

	got = drainCmdMsgs(got, cmd)

	if got.detail.Summary.RepoDirty != true {
		t.Fatalf("detail repo dirty = %t, want refreshed dirty state", got.detail.Summary.RepoDirty)
	}

	detail, err := st.GetProjectDetail(ctx, projectPath, 20)
	if err != nil {
		t.Fatalf("get detail after hide refresh: %v", err)
	}
	if !detail.Summary.RepoDirty {
		t.Fatalf("store detail should reflect dirty repo after hide refresh, got %#v", detail.Summary)
	}
}

func TestRenderDetailViewportUsesSyncedCache(t *testing.T) {
	m := Model{
		projects: []model.ProjectSummary{{
			Path:                          "/tmp/demo",
			Name:                          "demo",
			PresentOnDisk:                 true,
			LatestCompletedSessionSummary: "cached detail summary",
		}},
		selected: 0,
		detail: model.ProjectDetail{
			Summary: model.ProjectSummary{Path: "/tmp/demo"},
		},
		detailViewport: viewport.New(20, 5),
		width:          100,
		height:         24,
	}

	m.syncDetailViewport(false)
	m.projects[0].LatestCompletedSessionSummary = "uncached detail summary"

	layout := m.bodyLayout()
	rendered := ansi.Strip(m.renderDetailViewport(layout.detailContentWidth, max(1, layout.bottomPaneHeight-2)))
	if !strings.Contains(rendered, "cached detail summary") {
		t.Fatalf("renderDetailViewport() should use the synced detail cache until the next explicit sync, got %q", rendered)
	}
	if strings.Contains(rendered, "uncached detail summary") {
		t.Fatalf("renderDetailViewport() should not rebuild detail content on every render, got %q", rendered)
	}
}

func TestF2DoesNothingInMainView(t *testing.T) {
	m := Model{
		codexHiddenProject: "/tmp/demo",
		codexInput:         newCodexTextarea(),
		codexViewport:      viewport.New(0, 0),
		width:              100,
		height:             24,
		status:             "unchanged",
	}

	updated, cmd := m.updateNormalMode(tea.KeyMsg{Type: tea.KeyF2})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("f2 in the main view should not queue a command")
	}
	if got.codexVisibleProject != "" {
		t.Fatalf("codexVisibleProject = %q, want hidden", got.codexVisibleProject)
	}
	if got.status != "unchanged" {
		t.Fatalf("status = %q, want unchanged", got.status)
	}
}

func TestAltUpDoesNotRestoreHiddenCodexSessionFromMainView(t *testing.T) {
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
		codexManager:       manager,
		codexHiddenProject: "/tmp/demo",
		codexInput:         newCodexTextarea(),
		codexViewport:      viewport.New(0, 0),
		width:              100,
		height:             24,
	}

	updated, cmd := m.updateNormalMode(tea.KeyMsg{Type: tea.KeyUp, Alt: true})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("alt+up in the main view should not queue a command")
	}
	if got.codexVisibleProject != "" {
		t.Fatalf("codexVisibleProject = %q, want hidden", got.codexVisibleProject)
	}
}

func TestRefreshBusyElsewhereCmdRechecksVisibleSession(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Started:      true,
			Preset:       codexcli.PresetYolo,
			Busy:         true,
			BusyExternal: true,
			ThreadID:     "019cccc3abcdef",
			Status:       "Busy elsewhere",
		},
		trySnapshotFn: func(*fakeCodexSession) (codexapp.Snapshot, bool) {
			return codexapp.Snapshot{}, false
		},
		refreshBusyFn: func(s *fakeCodexSession) error {
			s.snapshot.Busy = false
			s.snapshot.BusyExternal = false
			s.snapshot.Status = "Embedded controls are live again."
			s.snapshot.LastSystemNotice = "Embedded Codex session 019cccc3 is no longer active in another Codex process. Embedded controls are live again."
			return nil
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
		codexSnapshots: map[string]codexapp.Snapshot{
			"/tmp/demo": session.snapshot,
		},
		codexInput:    newCodexTextarea(),
		codexViewport: viewport.New(0, 0),
		width:         100,
		height:        24,
	}

	cmd := m.refreshBusyElsewhereCmd("/tmp/demo")
	if cmd == nil {
		t.Fatalf("refreshBusyElsewhereCmd() should return a refresh command")
	}
	msg := cmd()
	update, ok := msg.(codexUpdateMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want codexUpdateMsg", msg)
	}
	if update.projectPath != "/tmp/demo" {
		t.Fatalf("project path = %q, want /tmp/demo", update.projectPath)
	}
	if session.refreshCalls != 1 {
		t.Fatalf("refresh calls = %d, want 1", session.refreshCalls)
	}
	if session.snapshotCalls != 0 {
		t.Fatalf("refreshBusyElsewhereCmd() should avoid blocking Snapshot(); snapshot calls = %d", session.snapshotCalls)
	}
	if session.snapshot.BusyExternal {
		t.Fatalf("session should no longer be busy externally after refresh")
	}
}

func TestRefreshBusyElsewhereCmdSkipsLiveLookupWithoutCachedBusyFlag(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Started:      true,
			Busy:         true,
			BusyExternal: true,
			ThreadID:     "thread-demo",
		},
		trySnapshotFn: func(*fakeCodexSession) (codexapp.Snapshot, bool) {
			return codexapp.Snapshot{}, false
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

	m := Model{codexManager: manager}

	cmd := m.refreshBusyElsewhereCmd("/tmp/demo")
	if cmd != nil {
		t.Fatalf("refreshBusyElsewhereCmd() = %#v, want nil without a cached busy-external snapshot", cmd)
	}
	if session.trySnapshotCalls != 0 || session.snapshotCalls != 0 {
		t.Fatalf("refreshBusyElsewhereCmd() should not probe the live session on cache miss; TrySnapshot/Snapshot calls = %d/%d", session.trySnapshotCalls, session.snapshotCalls)
	}
	if session.refreshCalls != 0 {
		t.Fatalf("refresh calls = %d, want 0", session.refreshCalls)
	}
}

func TestLiveCodexSnapshotsUseCachedMapInsteadOfManagerSnapshots(t *testing.T) {
	sessionA := &fakeCodexSession{
		projectPath: "/tmp/demo-a",
		snapshot: codexapp.Snapshot{
			Started:        true,
			ProjectPath:    "/tmp/demo-a",
			ThreadID:       "thread-a",
			LastActivityAt: time.Date(2026, 4, 4, 12, 0, 0, 0, time.UTC),
		},
	}
	sessionB := &fakeCodexSession{
		projectPath: "/tmp/demo-b",
		snapshot: codexapp.Snapshot{
			Started:        true,
			ProjectPath:    "/tmp/demo-b",
			ThreadID:       "thread-b",
			LastActivityAt: time.Date(2026, 4, 4, 12, 5, 0, 0, time.UTC),
		},
	}
	sessions := map[string]*fakeCodexSession{
		sessionA.projectPath: sessionA,
		sessionB.projectPath: sessionB,
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		session, ok := sessions[req.ProjectPath]
		if !ok {
			return nil, fmt.Errorf("unexpected project %q", req.ProjectPath)
		}
		return session, nil
	})
	for _, projectPath := range []string{sessionA.projectPath, sessionB.projectPath} {
		if _, _, err := manager.Open(codexapp.LaunchRequest{ProjectPath: projectPath}); err != nil {
			t.Fatalf("manager.Open(%q) error = %v", projectPath, err)
		}
	}
	sessionA.snapshotCalls = 0
	sessionB.snapshotCalls = 0

	m := Model{
		codexManager: manager,
		codexSnapshots: map[string]codexapp.Snapshot{
			sessionA.projectPath: sessionA.snapshot,
			sessionB.projectPath: sessionB.snapshot,
		},
	}

	snapshots := m.liveCodexSnapshots()
	if len(snapshots) != 2 {
		t.Fatalf("liveCodexSnapshots() len = %d, want 2", len(snapshots))
	}
	if snapshots[0].ProjectPath != sessionB.projectPath || snapshots[1].ProjectPath != sessionA.projectPath {
		t.Fatalf("liveCodexSnapshots() order = [%q, %q], want most recent cached session first", snapshots[0].ProjectPath, snapshots[1].ProjectPath)
	}
	if sessionA.snapshotCalls != 0 || sessionB.snapshotCalls != 0 {
		t.Fatalf("liveCodexSnapshots() should not consult manager session snapshots; calls = %d/%d", sessionA.snapshotCalls, sessionB.snapshotCalls)
	}
}

func TestSubmitVisibleCodexCmdDefersSessionLookupUntilRun(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Started:     true,
			Provider:    codexapp.ProviderCodex,
			ProjectPath: "/tmp/demo",
			ThreadID:    "thread-demo",
			Status:      "Codex session ready",
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})

	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexSnapshots: map[string]codexapp.Snapshot{
			"/tmp/demo": session.snapshot,
		},
	}

	cmd := m.submitVisibleCodexCmd(codexDraft{Text: "summarize this repo"})
	if cmd == nil {
		t.Fatalf("submitVisibleCodexCmd() = nil, want deferred command")
	}

	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Provider:    codexapp.ProviderCodex,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	msg := cmd()
	action, ok := msg.(codexActionMsg)
	if !ok {
		t.Fatalf("submitVisibleCodexCmd() message = %T, want codexActionMsg", msg)
	}
	if action.err != nil {
		t.Fatalf("submitVisibleCodexCmd() err = %v, want nil", action.err)
	}
	if len(session.submissions) != 1 {
		t.Fatalf("submitted inputs = %d, want 1", len(session.submissions))
	}
	if got := session.submissions[0].Text; got != "summarize this repo" {
		t.Fatalf("submitted text = %q, want summarize this repo", got)
	}
}

func TestVisibleCodexEnterSubmitsPrompt(t *testing.T) {
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
	input.SetValue("summarize this repo")

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
		t.Fatalf("enter should submit the visible Codex prompt")
	}
	if got.codexInput.Value() != "" {
		t.Fatalf("codex input should clear after submit, got %q", got.codexInput.Value())
	}
	if got.status != "Sending prompt to Codex..." {
		t.Fatalf("status = %q, want sending notice", got.status)
	}
}

func TestVisibleCodexEnterRefreshesStaleResumedSnapshotBeforeBlockingSubmit(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Started:  true,
			Preset:   codexcli.PresetYolo,
			ThreadID: "ses_resume",
			Status:   "Codex session ready",
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
		codexSnapshots: map[string]codexapp.Snapshot{
			"/tmp/demo": {
				Started:      true,
				Preset:       codexcli.PresetYolo,
				ThreadID:     "ses_resume",
				Busy:         true,
				Phase:        codexapp.SessionPhaseReconciling,
				ActiveTurnID: "turn_old",
				Status:       "Rechecking turn state...",
			},
		},
		width:  100,
		height: 24,
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if session.trySnapshotCalls == 0 {
		t.Fatalf("enter should refresh the live snapshot before blocking submit")
	}
	if cmd == nil {
		t.Fatalf("enter should submit when the refreshed resumed snapshot is idle")
	}
	if got.codexInput.Value() != "" {
		t.Fatalf("codex input should clear after submit, got %q", got.codexInput.Value())
	}
	if got.status != "Sending prompt to Codex..." {
		t.Fatalf("status = %q, want sending notice after refreshed idle snapshot", got.status)
	}
}
