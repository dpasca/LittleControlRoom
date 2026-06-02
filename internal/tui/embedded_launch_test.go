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
	"lcroom/internal/service"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEmbeddedModelPreferenceLoadsFromSavedSettingsOnStartup(t *testing.T) {
	cfg := config.Default()
	cfg.ConfigPath = filepath.Join(t.TempDir(), "config.toml")
	cfg.EmbeddedCodexModel = "gpt-5.4"
	cfg.EmbeddedCodexReasoning = "high"
	cfg.EmbeddedClaudeModel = "sonnet"
	cfg.EmbeddedClaudeReasoning = "max"
	cfg.EmbeddedOpenCodeModel = "openai/gpt-5.4"
	cfg.EmbeddedOpenCodeReasoning = "medium"

	svc := service.New(cfg, nil, events.NewBus(), nil)
	m := New(context.Background(), svc)

	var requests []codexapp.LaunchRequest
	m.codexManager = codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider: req.Provider.Normalized(),
				Started:  true,
				Status:   req.Provider.Label() + " session ready",
			},
		}, nil
	})
	m.projects = []model.ProjectSummary{{
		Path:          "/tmp/demo",
		Name:          "demo",
		PresentOnDisk: true,
	}}

	updated, cmd := m.launchEmbeddedForSelection(codexapp.ProviderCodex, true, "")
	m = updated.(Model)
	if cmd == nil {
		t.Fatalf("launchEmbeddedForSelection(codex) should return an open command")
	}
	if msg := cmd(); msg == nil {
		t.Fatalf("codex open command should return a message")
	}

	updated, cmd = m.launchEmbeddedForSelection(codexapp.ProviderOpenCode, true, "")
	m = updated.(Model)
	if cmd == nil {
		t.Fatalf("launchEmbeddedForSelection(opencode) should return an open command")
	}
	if msg := cmd(); msg == nil {
		t.Fatalf("opencode open command should return a message")
	}

	updated, cmd = m.launchEmbeddedForSelection(codexapp.ProviderClaudeCode, true, "")
	m = updated.(Model)
	if cmd == nil {
		t.Fatalf("launchEmbeddedForSelection(claude) should return an open command")
	}
	if msg := cmd(); msg == nil {
		t.Fatalf("claude open command should return a message")
	}

	if len(requests) != 3 {
		t.Fatalf("request count = %d, want 3", len(requests))
	}
	if requests[0].PendingModel != "gpt-5.4" || requests[0].PendingReasoning != "high" {
		t.Fatalf("codex request = %#v, want saved startup preference", requests[0])
	}
	if requests[1].PendingModel != "openai/gpt-5.4" || requests[1].PendingReasoning != "medium" {
		t.Fatalf("opencode request = %#v, want saved startup preference", requests[1])
	}
	if requests[2].PendingModel != "sonnet" || requests[2].PendingReasoning != "max" {
		t.Fatalf("claude request = %#v, want saved startup preference", requests[2])
	}
}

func TestVisibleCodexSlashSessionAliasOpensRequestedOpenCodeSession(t *testing.T) {
	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider: codexapp.ProviderOpenCode,
				Started:  true,
				Status:   "OpenCode session ready",
				ThreadID: req.ResumeID,
			},
		}, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Provider:    codexapp.ProviderOpenCode,
		ResumeID:    "ses-current",
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	input := newCodexTextarea()
	input.SetValue("/session ses-old")

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
		t.Fatalf("enter should resume the requested embedded OpenCode session")
	}
	if got.codexPendingOpen == nil || got.codexPendingOpen.provider != codexapp.ProviderOpenCode {
		t.Fatalf("codexPendingOpen = %#v, want pending OpenCode session", got.codexPendingOpen)
	}
	if !strings.Contains(got.status, "Opening embedded OpenCode session") || !strings.Contains(got.status, "ses-old") {
		t.Fatalf("status = %q, want requested OpenCode session open notice", got.status)
	}

	msg := cmd()
	opened, ok := msg.(codexSessionOpenedMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want codexSessionOpenedMsg", msg)
	}
	if opened.err != nil {
		t.Fatalf("/session ses-old returned error = %v", opened.err)
	}
	if len(requests) != 2 {
		t.Fatalf("launch requests = %d, want 2", len(requests))
	}
	if requests[1].Provider != codexapp.ProviderOpenCode {
		t.Fatalf("provider = %q, want %q", requests[1].Provider, codexapp.ProviderOpenCode)
	}
	if requests[1].ResumeID != "ses-old" {
		t.Fatalf("resume id = %q, want %q", requests[1].ResumeID, "ses-old")
	}
}

func TestVisibleCodexSlashNewStartsFreshSession(t *testing.T) {
	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Started:  true,
				Preset:   req.Preset,
				ThreadID: fmt.Sprintf("thread_%d", len(requests)),
				Status:   "Codex session ready",
			},
		}, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Preset:      codexcli.PresetYolo,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	input := newCodexTextarea()
	input.SetValue("/new continue in the new thread")

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
		t.Fatalf("enter should run the embedded /new command")
	}
	if got.codexInput.Value() != "" {
		t.Fatalf("codex input should clear after /new, got %q", got.codexInput.Value())
	}
	if got.status != "Starting a new embedded Codex session..." {
		t.Fatalf("status = %q, want fresh-session notice", got.status)
	}
	if got.codexPendingOpen == nil || got.codexPendingOpen.projectPath != "/tmp/demo" {
		t.Fatalf("codexPendingOpen = %#v, want pending open for /tmp/demo", got.codexPendingOpen)
	}
	rendered := ansi.Strip(got.renderCodexView())
	if !strings.Contains(rendered, "Starting a new embedded Codex session...") {
		t.Fatalf("rendered view should show opening state, got %q", rendered)
	}

	msg := cmd()
	opened, ok := msg.(codexSessionOpenedMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want codexSessionOpenedMsg", msg)
	}
	if opened.err != nil {
		t.Fatalf("/new returned error = %v", opened.err)
	}
	if len(requests) != 2 {
		t.Fatalf("launch requests = %d, want 2", len(requests))
	}
	if !requests[1].ForceNew {
		t.Fatalf("second launch request should force a fresh session")
	}
	if requests[1].Prompt != "continue in the new thread" {
		t.Fatalf("second launch prompt = %q, want inline /new prompt", requests[1].Prompt)
	}
}

func TestVisibleOpenCodeSlashNewStartsFreshSession(t *testing.T) {
	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		threadID := "ses-old1"
		if len(requests) > 1 {
			threadID = "ses-new1"
		}
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider: codexapp.ProviderOpenCode,
				Started:  true,
				Status:   "OpenCode session ready",
				ThreadID: threadID,
			},
		}, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Provider:    codexapp.ProviderOpenCode,
		ResumeID:    "ses-old1",
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	input := newCodexTextarea()
	input.SetValue("/new")

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
		t.Fatalf("enter should run the embedded OpenCode /new command")
	}
	if got.codexPendingOpen == nil || got.codexPendingOpen.provider != codexapp.ProviderOpenCode {
		t.Fatalf("codexPendingOpen = %#v, want pending OpenCode session", got.codexPendingOpen)
	}
	if got.status != "Starting a new embedded OpenCode session..." {
		t.Fatalf("status = %q, want fresh OpenCode session notice", got.status)
	}

	msg := cmd()
	opened, ok := msg.(codexSessionOpenedMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want codexSessionOpenedMsg", msg)
	}
	if opened.err != nil {
		t.Fatalf("/new returned error = %v", opened.err)
	}
	if opened.status != "Fresh embedded OpenCode session ses-new1 opened. Alt+Up hides it." {
		t.Fatalf("opened.status = %q, want fresh OpenCode session confirmation", opened.status)
	}
	if len(requests) != 2 {
		t.Fatalf("launch requests = %d, want 2", len(requests))
	}
	if !requests[1].ForceNew {
		t.Fatalf("second launch request should force a fresh OpenCode session")
	}

	updated, _ = got.Update(opened)
	got = updated.(Model)
	snapshot, ok := got.currentCodexSnapshot()
	if !ok {
		t.Fatalf("currentCodexSnapshot() unavailable after handling the opened session")
	}
	if snapshot.ThreadID != "ses-new1" {
		t.Fatalf("thread id = %q, want %q", snapshot.ThreadID, "ses-new1")
	}
}

func TestCodexSessionOpenedMsgSeedsSnapshotCache(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Provider: codexapp.ProviderCodex,
			Started:  true,
			Status:   "Codex session ready",
			ThreadID: "thread-demo",
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})

	m := Model{
		codexManager:  manager,
		codexInput:    newCodexTextarea(),
		codexViewport: viewport.New(0, 0),
		width:         100,
		height:        24,
	}

	cmd := m.openCodexSessionCmd(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Preset:      codexcli.PresetYolo,
	})
	if cmd == nil {
		t.Fatalf("openCodexSessionCmd() returned nil")
	}
	msg := cmd()
	opened, ok := msg.(codexSessionOpenedMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want codexSessionOpenedMsg", msg)
	}
	if opened.err != nil {
		t.Fatalf("opened.err = %v, want nil", opened.err)
	}
	if opened.snapshot.ThreadID != "thread-demo" {
		t.Fatalf("opened.snapshot.ThreadID = %q, want %q", opened.snapshot.ThreadID, "thread-demo")
	}

	callsAfterOpen := session.snapshotCalls
	if callsAfterOpen == 0 {
		t.Fatalf("expected open command to snapshot the session off the UI thread")
	}

	updated, _ := m.update(opened)
	got := updated.(Model)
	if session.snapshotCalls != callsAfterOpen {
		t.Fatalf("handling codexSessionOpenedMsg should reuse the opened snapshot; snapshot calls = %d, want %d", session.snapshotCalls, callsAfterOpen)
	}
	snapshot, ok := got.currentCodexSnapshot()
	if !ok {
		t.Fatalf("currentCodexSnapshot() unavailable after handling the opened session")
	}
	if snapshot.ThreadID != "thread-demo" {
		t.Fatalf("current thread id = %q, want %q", snapshot.ThreadID, "thread-demo")
	}
	if session.snapshotCalls != callsAfterOpen {
		t.Fatalf("currentCodexSnapshot() should reuse the cached opened snapshot; snapshot calls = %d, want %d", session.snapshotCalls, callsAfterOpen)
	}
}

func TestVisibleOpenCodeSlashNewFailureKeepsClosedSessionVisible(t *testing.T) {
	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		if len(requests) == 1 {
			return &fakeCodexSession{
				projectPath: req.ProjectPath,
				snapshot: codexapp.Snapshot{
					Provider: codexapp.ProviderOpenCode,
					Started:  true,
					Status:   "OpenCode session ready",
					ThreadID: "ses-current",
				},
			}, nil
		}
		return nil, fmt.Errorf("opencode create failed")
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Provider:    codexapp.ProviderOpenCode,
		ResumeID:    "ses-current",
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	input := newCodexTextarea()
	input.SetValue("/new")

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
		t.Fatalf("enter should run the embedded OpenCode /new command")
	}
	if got.codexPendingOpen == nil || got.codexPendingOpen.provider != codexapp.ProviderOpenCode {
		t.Fatalf("codexPendingOpen = %#v, want pending OpenCode session", got.codexPendingOpen)
	}

	msg := cmd()
	opened, ok := msg.(codexSessionOpenedMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want codexSessionOpenedMsg", msg)
	}
	if opened.err == nil || opened.err.Error() != "opencode create failed" {
		t.Fatalf("/new returned error = %v, want opencode create failed", opened.err)
	}

	updated, _ = got.Update(opened)
	got = updated.(Model)
	if got.codexPendingOpen != nil {
		t.Fatalf("codexPendingOpen = %#v, want nil after handling the failed open", got.codexPendingOpen)
	}
	if got.codexVisibleProject != "/tmp/demo" {
		t.Fatalf("codexVisibleProject = %q, want the closed OpenCode session to remain visible", got.codexVisibleProject)
	}
	snapshot, ok := got.currentCodexSnapshot()
	if !ok {
		t.Fatalf("currentCodexSnapshot() unavailable after a failed replacement")
	}
	if !snapshot.Closed {
		t.Fatalf("snapshot.Closed = false, want the previous OpenCode session to remain as a closed placeholder")
	}
	if got.status != "Embedded session open failed (use /errors)" {
		t.Fatalf("status = %q, want embedded-session-open-failed notice", got.status)
	}
	if got.err != nil {
		t.Fatalf("got.err = %v, want nil after logging", got.err)
	}
	if len(got.errorLogEntries) == 0 || got.errorLogEntries[0].Message != "opencode create failed" {
		t.Fatalf("latest error log entry = %#v, want opencode create failed", got.errorLogEntries)
	}
	rendered := ansi.Strip(got.renderCodexView())
	if !strings.Contains(rendered, "OpenCode session closed.") {
		t.Fatalf("rendered view should keep showing the closed OpenCode session, got %q", rendered)
	}
	if len(requests) != 2 {
		t.Fatalf("launch requests = %d, want 2", len(requests))
	}
	if !requests[1].ForceNew {
		t.Fatalf("second launch request should force a fresh OpenCode session")
	}
}

func TestLaunchOpenCodeForSelectionFailureKeepsErrorPlaceholderVisible(t *testing.T) {
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return nil, fmt.Errorf("opencode create failed")
	})

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

	updated, cmd := m.launchOpenCodeForSelection(false, "")
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("launchOpenCodeForSelection() should return an open command")
	}
	if got.codexPendingOpen == nil || got.codexPendingOpen.provider != codexapp.ProviderOpenCode {
		t.Fatalf("codexPendingOpen = %#v, want pending OpenCode session", got.codexPendingOpen)
	}

	msg := cmd()
	opened, ok := msg.(codexSessionOpenedMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want codexSessionOpenedMsg", msg)
	}
	if opened.err == nil || opened.err.Error() != "opencode create failed" {
		t.Fatalf("open returned error = %v, want opencode create failed", opened.err)
	}

	updated, _ = got.Update(opened)
	got = updated.(Model)
	if got.codexVisibleProject != "/tmp/demo" {
		t.Fatalf("codexVisibleProject = %q, want failed OpenCode project to stay visible", got.codexVisibleProject)
	}
	if got.codexHiddenProject != "/tmp/demo" {
		t.Fatalf("codexHiddenProject = %q, want failed OpenCode project to stay restorable", got.codexHiddenProject)
	}
	snapshot, ok := got.currentCodexSnapshot()
	if !ok {
		t.Fatalf("currentCodexSnapshot() unavailable after failed open")
	}
	if snapshot.Provider != codexapp.ProviderOpenCode {
		t.Fatalf("snapshot.Provider = %q, want %q", snapshot.Provider, codexapp.ProviderOpenCode)
	}
	if !snapshot.Closed {
		t.Fatalf("snapshot.Closed = false, want closed placeholder after failed open")
	}
	if snapshot.LastError != "opencode create failed" {
		t.Fatalf("snapshot.LastError = %q, want opencode create failed", snapshot.LastError)
	}
	if got.status != "Embedded session open failed (use /errors)" {
		t.Fatalf("status = %q, want embedded-session-open-failed notice", got.status)
	}
	if got.err != nil {
		t.Fatalf("got.err = %v, want nil after logging", got.err)
	}
	if len(got.errorLogEntries) == 0 || got.errorLogEntries[0].Message != "opencode create failed" {
		t.Fatalf("latest error log entry = %#v, want opencode create failed", got.errorLogEntries)
	}
	rendered := ansi.Strip(got.renderCodexView())
	if !strings.Contains(rendered, "OpenCode session closed.") {
		t.Fatalf("rendered view should keep showing the failed OpenCode placeholder, got %q", rendered)
	}
	if !strings.Contains(rendered, "opencode create failed") {
		t.Fatalf("rendered view should show the OpenCode startup error, got %q", rendered)
	}
}

func TestVisibleCodexSlashNewWarnsWhenActiveSessionIsReopenedReadOnly(t *testing.T) {
	const threadID = "019cccc3abcd"

	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		snapshot := codexapp.Snapshot{
			Provider: codexapp.ProviderCodex,
			Started:  true,
			Preset:   req.Preset,
			Status:   "Codex session ready",
			ThreadID: threadID,
		}
		if len(requests) > 1 {
			snapshot.BusyExternal = true
		}
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot:    snapshot,
		}, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Preset:      codexcli.PresetYolo,
		ResumeID:    threadID,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	input := newCodexTextarea()
	input.SetValue("/new")

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
		t.Fatalf("enter should run the embedded /new command")
	}

	msg := cmd()
	opened, ok := msg.(codexSessionOpenedMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want codexSessionOpenedMsg", msg)
	}
	if opened.err != nil {
		t.Fatalf("/new returned error = %v", opened.err)
	}
	wantStatus := "Could not start a fresh embedded Codex session because session 019cccc3 is already active in another process. Showing that session read-only instead."
	if opened.status != wantStatus {
		t.Fatalf("opened.status = %q, want %q", opened.status, wantStatus)
	}

	updated, _ = got.Update(opened)
	got = updated.(Model)
	if got.status != wantStatus {
		t.Fatalf("status = %q, want %q", got.status, wantStatus)
	}
	if len(requests) != 2 {
		t.Fatalf("launch requests = %d, want 2", len(requests))
	}
	if !requests[1].ForceNew {
		t.Fatalf("second launch request should force a fresh session")
	}
}

func TestLaunchCodexForSelectionShowsOpeningStateInsteadOfPreviousSession(t *testing.T) {
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Started: true,
				Preset:  req.Preset,
				Status:  "Codex session ready",
			},
		}, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/previous",
		Preset:      codexcli.PresetYolo,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/previous",
		codexHiddenProject:  "/tmp/previous",
		codexInput:          newCodexTextarea(),
		codexDrafts:         make(map[string]codexDraft),
		codexViewport:       viewport.New(0, 0),
		projects: []model.ProjectSummary{{
			Name:          "next",
			Path:          "/tmp/next",
			PresentOnDisk: true,
		}},
		selected: 0,
		width:    100,
		height:   24,
	}

	updated, cmd := m.launchCodexForSelection(true, "")
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("launchCodexForSelection() should return an open command")
	}
	if got.codexPendingOpen == nil || got.codexPendingOpen.projectPath != "/tmp/next" {
		t.Fatalf("codexPendingOpen = %#v, want pending open for /tmp/next", got.codexPendingOpen)
	}
	if got.codexVisibleProject != "/tmp/previous" {
		t.Fatalf("codexVisibleProject = %q, want previous session to remain stored while opening", got.codexVisibleProject)
	}
	rendered := ansi.Strip(got.renderCodexView())
	if !strings.Contains(rendered, "Project: /tmp/next") {
		t.Fatalf("rendered opening view should mention the requested project, got %q", rendered)
	}
	if strings.Contains(rendered, "/tmp/previous") {
		t.Fatalf("rendered opening view should not keep showing the previous session, got %q", rendered)
	}
}

func TestLaunchEmbeddedForSelectionBlocksWhileAnotherEmbeddedProviderIsActive(t *testing.T) {
	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider: req.Provider.Normalized(),
				Started:  true,
				Busy:     req.Provider.Normalized() == codexapp.ProviderClaudeCode,
				Status:   req.Provider.Label() + " session ready",
			},
		}, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Provider:    codexapp.ProviderClaudeCode,
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
		selected: 0,
	}

	updated, cmd := m.launchEmbeddedForSelection(codexapp.ProviderCodex, true, "")
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("launchEmbeddedForSelection() cmd = %#v, want nil when another embedded provider is active", cmd)
	}
	wantStatus := "This project already has an active embedded Claude Code session. Finish or close it before starting Codex here."
	if got.status != wantStatus {
		t.Fatalf("status = %q, want %q", got.status, wantStatus)
	}
	if got.attentionDialog == nil {
		t.Fatalf("launchEmbeddedForSelection() should show an attention dialog when another embedded provider is active")
	}
	if got.attentionDialog.Title != "Launch blocked" {
		t.Fatalf("attention dialog title = %q, want launch blocked", got.attentionDialog.Title)
	}
	if got.attentionDialog.PrimaryProvider != codexapp.ProviderClaudeCode {
		t.Fatalf("attention dialog provider = %q, want Claude Code", got.attentionDialog.PrimaryProvider)
	}
	if got.attentionDialog.PrimaryLabel != "Open Claude Code" {
		t.Fatalf("attention dialog primary label = %q, want open action", got.attentionDialog.PrimaryLabel)
	}
	rendered := ansi.Strip(got.renderAttentionDialogContent(72))
	if !strings.Contains(rendered, "This project already has an active embedded Claude Code session.") ||
		!strings.Contains(rendered, "Finish") ||
		!strings.Contains(rendered, "Open Claude Code") {
		t.Fatalf("attention dialog should surface the blocked launch and open action, got %q", rendered)
	}
	if len(requests) != 1 {
		t.Fatalf("launch requests = %d, want only the original Claude open", len(requests))
	}
}

func TestLaunchEmbeddedForSelectionBlocksWhileAnotherEmbeddedProviderIsOpen(t *testing.T) {
	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider: req.Provider.Normalized(),
				Started:  true,
				ThreadID: "thread-codex",
				Status:   req.Provider.Label() + " session ready",
			},
		}, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Provider:    codexapp.ProviderCodex,
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
		selected: 0,
	}

	updated, cmd := m.launchEmbeddedForSelection(codexapp.ProviderLCAgent, false, "")
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("launchEmbeddedForSelection() cmd = %#v, want nil when another embedded provider is open", cmd)
	}
	wantStatus := "This project already has an open embedded Codex session. Close it before starting LCAgent here."
	if got.status != wantStatus {
		t.Fatalf("status = %q, want %q", got.status, wantStatus)
	}
	if got.attentionDialog == nil {
		t.Fatalf("launchEmbeddedForSelection() should show an attention dialog when another embedded provider is open")
	}
	if got.attentionDialog.PrimaryProvider != codexapp.ProviderCodex {
		t.Fatalf("attention dialog provider = %q, want Codex", got.attentionDialog.PrimaryProvider)
	}
	if got.attentionDialog.PrimaryLabel != "Open Codex" {
		t.Fatalf("attention dialog primary label = %q, want open action", got.attentionDialog.PrimaryLabel)
	}
	if len(requests) != 1 {
		t.Fatalf("launch requests = %d, want only the original Codex open", len(requests))
	}
}

func TestLaunchEmbeddedForSelectionBlocksOpenDifferentEmbeddedProviderPairs(t *testing.T) {
	providers := []codexapp.Provider{
		codexapp.ProviderCodex,
		codexapp.ProviderOpenCode,
		codexapp.ProviderClaudeCode,
		codexapp.ProviderLCAgent,
	}
	for _, liveProvider := range providers {
		for _, requestedProvider := range providers {
			if liveProvider == requestedProvider {
				continue
			}
			t.Run(liveProvider.Label()+" to "+requestedProvider.Label(), func(t *testing.T) {
				var requests []codexapp.LaunchRequest
				manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
					requests = append(requests, req)
					return &fakeCodexSession{
						projectPath: req.ProjectPath,
						snapshot: codexapp.Snapshot{
							Provider: req.Provider.Normalized(),
							Started:  true,
							ThreadID: "thread-" + string(req.Provider.Normalized()),
							Status:   req.Provider.Label() + " session ready",
						},
					}, nil
				})
				if _, _, err := manager.Open(codexapp.LaunchRequest{
					ProjectPath: "/tmp/demo",
					Provider:    liveProvider,
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
					selected: 0,
				}

				updated, cmd := m.launchEmbeddedForSelection(requestedProvider, false, "")
				got := updated.(Model)
				if cmd != nil {
					t.Fatalf("launchEmbeddedForSelection() cmd = %#v, want nil", cmd)
				}
				wantStatus := fmt.Sprintf("This project already has an open embedded %s session. Close it before starting %s here.", liveProvider.Label(), requestedProvider.Label())
				if got.status != wantStatus {
					t.Fatalf("status = %q, want %q", got.status, wantStatus)
				}
				if got.attentionDialog == nil {
					t.Fatalf("launchEmbeddedForSelection() should show an attention dialog")
				}
				if got.attentionDialog.PrimaryProvider != liveProvider {
					t.Fatalf("attention dialog provider = %q, want %q", got.attentionDialog.PrimaryProvider, liveProvider)
				}
				if got.attentionDialog.PrimaryLabel != "Open "+liveProvider.Label() {
					t.Fatalf("attention dialog primary label = %q, want open action", got.attentionDialog.PrimaryLabel)
				}
				if len(requests) != 1 {
					t.Fatalf("launch requests = %d, want only the original provider open", len(requests))
				}
			})
		}
	}
}

func TestLaunchEmbeddedForSelectionBlocksActiveDifferentEmbeddedProviderPairs(t *testing.T) {
	providers := []codexapp.Provider{
		codexapp.ProviderCodex,
		codexapp.ProviderOpenCode,
		codexapp.ProviderClaudeCode,
		codexapp.ProviderLCAgent,
	}
	for _, liveProvider := range providers {
		for _, requestedProvider := range providers {
			if liveProvider == requestedProvider {
				continue
			}
			t.Run(liveProvider.Label()+" to "+requestedProvider.Label(), func(t *testing.T) {
				var requests []codexapp.LaunchRequest
				manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
					requests = append(requests, req)
					return &fakeCodexSession{
						projectPath: req.ProjectPath,
						snapshot: codexapp.Snapshot{
							Provider: req.Provider.Normalized(),
							Started:  true,
							Busy:     true,
							ThreadID: "thread-" + string(req.Provider.Normalized()),
							Status:   req.Provider.Label() + " session ready",
						},
					}, nil
				})
				if _, _, err := manager.Open(codexapp.LaunchRequest{
					ProjectPath: "/tmp/demo",
					Provider:    liveProvider,
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
					selected: 0,
				}

				updated, cmd := m.launchEmbeddedForSelection(requestedProvider, true, "")
				got := updated.(Model)
				if cmd != nil {
					t.Fatalf("launchEmbeddedForSelection() cmd = %#v, want nil", cmd)
				}
				wantStatus := fmt.Sprintf("This project already has an active embedded %s session. Finish or close it before starting %s here.", liveProvider.Label(), requestedProvider.Label())
				if got.status != wantStatus {
					t.Fatalf("status = %q, want %q", got.status, wantStatus)
				}
				if got.attentionDialog == nil {
					t.Fatalf("launchEmbeddedForSelection() should show an attention dialog")
				}
				if got.attentionDialog.PrimaryProvider != liveProvider {
					t.Fatalf("attention dialog provider = %q, want %q", got.attentionDialog.PrimaryProvider, liveProvider)
				}
				if got.attentionDialog.PrimaryLabel != "Open "+liveProvider.Label() {
					t.Fatalf("attention dialog primary label = %q, want open action", got.attentionDialog.PrimaryLabel)
				}
				if len(requests) != 1 {
					t.Fatalf("launch requests = %d, want only the original provider open", len(requests))
				}
			})
		}
	}
}

func TestLaunchEmbeddedForSelectionBlocksWhileAnotherProviderSessionIsUnfinished(t *testing.T) {
	now := time.Date(2026, 3, 30, 20, 30, 0, 0, time.UTC)
	m := Model{
		nowFn: func() time.Time { return now },
		projects: []model.ProjectSummary{{
			Path:                     "/tmp/demo",
			Name:                     "demo",
			PresentOnDisk:            true,
			LatestSessionID:          "cc-old",
			LatestSessionFormat:      "claude_code",
			LatestSessionLastEventAt: now.Add(-10 * time.Minute),
			LatestTurnStateKnown:     true,
			LatestTurnCompleted:      false,
		}},
		selected: 0,
	}

	updated, cmd := m.launchEmbeddedForSelection(codexapp.ProviderCodex, true, "")
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("launchEmbeddedForSelection() cmd = %#v, want nil when another provider session is still unfinished", cmd)
	}
	wantStatus := "This project already has an unfinished Claude Code session. Finish or close it before starting Codex here."
	if got.status != wantStatus {
		t.Fatalf("status = %q, want %q", got.status, wantStatus)
	}
	if got.attentionDialog == nil {
		t.Fatalf("launchEmbeddedForSelection() should show an attention dialog for unfinished external sessions")
	}
	if got.attentionDialog.PrimaryProvider != codexapp.ProviderClaudeCode {
		t.Fatalf("attention dialog provider = %q, want Claude Code", got.attentionDialog.PrimaryProvider)
	}
	if got.attentionDialog.PrimaryLabel != "Resume Claude Code" {
		t.Fatalf("attention dialog primary label = %q, want resume action", got.attentionDialog.PrimaryLabel)
	}
}

func TestAttentionDialogEnterOpensExistingEmbeddedSession(t *testing.T) {
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
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
		Provider:    codexapp.ProviderClaudeCode,
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
		selected: 0,
		attentionDialog: &attentionDialogState{
			Title:           "Launch blocked",
			ProjectName:     "demo",
			ProjectPath:     "/tmp/demo",
			Message:         "This project already has an active embedded Claude Code session. Finish or close it before starting Codex here.",
			PrimaryLabel:    "Open Claude Code",
			PrimaryProvider: codexapp.ProviderClaudeCode,
			Selected:        attentionDialogFocusPrimary,
		},
	}

	updated, cmd := m.updateAttentionDialogMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("attention dialog enter on the primary action should return an open command")
	}
	if got.attentionDialog != nil {
		t.Fatalf("attention dialog should close after taking the primary action")
	}
	if got.codexVisibleProject != "/tmp/demo" {
		t.Fatalf("codexVisibleProject = %q, want /tmp/demo", got.codexVisibleProject)
	}
}

func TestLaunchEmbeddedForSelectionAllowsStaleUnfinishedSessionOutsideProtectionWindow(t *testing.T) {
	now := time.Date(2026, 3, 30, 20, 30, 0, 0, time.UTC)
	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider: req.Provider.Normalized(),
				Started:  true,
				Preset:   req.Preset,
				Status:   req.Provider.Label() + " session ready",
			},
		}, nil
	})

	m := Model{
		nowFn:        func() time.Time { return now },
		codexManager: manager,
		projects: []model.ProjectSummary{{
			Path:                     "/tmp/demo",
			Name:                     "demo",
			PresentOnDisk:            true,
			LatestSessionFormat:      "claude_code",
			LatestSessionLastEventAt: now.Add(-6 * time.Hour),
			LatestTurnStateKnown:     true,
			LatestTurnCompleted:      false,
		}},
		selected: 0,
	}

	updated, cmd := m.launchEmbeddedForSelection(codexapp.ProviderCodex, true, "")
	if cmd == nil {
		t.Fatalf("launchEmbeddedForSelection() should allow stale unfinished sessions outside the protection window")
	}
	msg := cmd()
	opened, ok := msg.(codexSessionOpenedMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want codexSessionOpenedMsg", msg)
	}
	if opened.err != nil {
		t.Fatalf("opened.err = %v, want nil", opened.err)
	}
	if len(requests) != 1 {
		t.Fatalf("launch requests = %d, want 1 fresh Codex open", len(requests))
	}
	if requests[0].Provider != codexapp.ProviderCodex {
		t.Fatalf("provider = %q, want %q", requests[0].Provider, codexapp.ProviderCodex)
	}
	_ = updated
}

func TestLaunchCodexForSelectionForceNewRetriesWhenPreviousThreadReopensFirst(t *testing.T) {
	const previousThreadID = "019cccc3abcd"
	const newThreadID = "019dddd4efgh"

	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		threadID := previousThreadID
		if len(requests) > 1 {
			threadID = newThreadID
		}
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider: codexapp.ProviderCodex,
				Started:  true,
				Preset:   req.Preset,
				Status:   "Codex session ready",
				ThreadID: threadID,
			},
		}, nil
	})

	m := Model{
		codexManager: manager,
		projects: []model.ProjectSummary{{
			Path:                "/tmp/demo",
			Name:                "demo",
			PresentOnDisk:       true,
			LatestSessionID:     previousThreadID,
			LatestSessionFormat: "modern",
		}},
	}

	updated, cmd := m.launchCodexForSelection(true, "")
	if cmd == nil {
		t.Fatalf("launchCodexForSelection() should return an open command")
	}

	msg := cmd()
	opened, ok := msg.(codexSessionOpenedMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want codexSessionOpenedMsg", msg)
	}
	if opened.err != nil {
		t.Fatalf("/codex-new returned error = %v", opened.err)
	}
	if opened.status != "Fresh embedded Codex session 019dddd4 opened. Alt+Up hides it." {
		t.Fatalf("opened.status = %q, want normal opened status after retry", opened.status)
	}
	if len(requests) != 2 {
		t.Fatalf("launch requests = %d, want 2 after the automatic fresh-session retry", len(requests))
	}

	updated, _ = updated.(Model).Update(opened)
	got := updated.(Model)
	snapshot, ok := got.currentCodexSnapshot()
	if !ok {
		t.Fatalf("currentCodexSnapshot() unavailable after handling the opened session")
	}
	if snapshot.ThreadID != newThreadID {
		t.Fatalf("thread id = %q, want retried fresh thread %q", snapshot.ThreadID, newThreadID)
	}
}

func TestLaunchCodexForSelectionForceNewRetriesWhenCodexRejectsFreshThread(t *testing.T) {
	const freshThreadID = "019fresh4efgh"
	const prompt = "continue in the new thread"

	var (
		requests []codexapp.LaunchRequest
		created  []*fakeCodexSession
	)
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		if len(requests) == 1 {
			return nil, &codexapp.ForceNewSessionReusedError{ThreadID: "019stale3abcd"}
		}
		session := &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider: codexapp.ProviderCodex,
				Started:  true,
				Preset:   req.Preset,
				Status:   "Codex session ready",
				ThreadID: freshThreadID,
			},
		}
		created = append(created, session)
		return session, nil
	})

	m := Model{
		codexManager: manager,
		projects: []model.ProjectSummary{{
			Path:          "/tmp/demo",
			Name:          "demo",
			PresentOnDisk: true,
		}},
	}

	updated, cmd := m.launchCodexForSelection(true, prompt)
	if cmd == nil {
		t.Fatalf("launchCodexForSelection() should return an open command")
	}

	msg := cmd()
	opened, ok := msg.(codexSessionOpenedMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want codexSessionOpenedMsg", msg)
	}
	if opened.err != nil {
		t.Fatalf("/codex-new returned error = %v", opened.err)
	}
	if opened.status != "Prompt sent to fresh embedded Codex session 019fresh. Alt+Up hides it." {
		t.Fatalf("opened.status = %q, want prompt-sent status after retry", opened.status)
	}
	if len(requests) != 2 {
		t.Fatalf("launch requests = %d, want 2 after retryable fresh-thread failure", len(requests))
	}
	if !requests[0].ForceNew || !requests[1].ForceNew {
		t.Fatalf("launch requests should keep ForceNew enabled across retries: %#v", requests)
	}
	if requests[1].Prompt != prompt {
		t.Fatalf("second launch prompt = %q, want the original inline prompt after retry", requests[1].Prompt)
	}
	if len(created) != 1 {
		t.Fatalf("created sessions = %d, want 1 successful fresh session", len(created))
	}

	updated, _ = updated.(Model).Update(opened)
	got := updated.(Model)
	snapshot, ok := got.currentCodexSnapshot()
	if !ok {
		t.Fatalf("currentCodexSnapshot() unavailable after handling the opened session")
	}
	if snapshot.ThreadID != freshThreadID {
		t.Fatalf("thread id = %q, want retried fresh thread %q", snapshot.ThreadID, freshThreadID)
	}
}

func TestLaunchOpenCodeForSelectionForceNewRetriesWhenOpenCodeRejectsFreshSession(t *testing.T) {
	const freshSessionID = "ses_fresh4efgh"
	const prompt = "continue in the fresh OpenCode session"

	var (
		requests []codexapp.LaunchRequest
		created  []*fakeCodexSession
	)
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		if len(requests) == 1 {
			return nil, &codexapp.ForceNewSessionReusedError{Provider: codexapp.ProviderOpenCode, ThreadID: "ses_stale3abcd"}
		}
		session := &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider: codexapp.ProviderOpenCode,
				Started:  true,
				Preset:   req.Preset,
				Status:   "OpenCode session ready",
				ThreadID: freshSessionID,
			},
		}
		created = append(created, session)
		return session, nil
	})

	m := Model{
		codexManager: manager,
		projects: []model.ProjectSummary{{
			Path:                "/tmp/demo",
			Name:                "demo",
			PresentOnDisk:       true,
			LatestSessionFormat: "opencode_db",
		}},
	}

	updated, cmd := m.launchOpenCodeForSelection(true, prompt)
	if cmd == nil {
		t.Fatalf("launchOpenCodeForSelection() should return an open command")
	}

	msg := cmd()
	opened, ok := msg.(codexSessionOpenedMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want codexSessionOpenedMsg", msg)
	}
	if opened.err != nil {
		t.Fatalf("/opencode-new returned error = %v", opened.err)
	}
	if opened.status != "Prompt sent to fresh embedded OpenCode session ses_fres. Alt+Up hides it." {
		t.Fatalf("opened.status = %q, want prompt-sent status after retry", opened.status)
	}
	if len(requests) != 2 {
		t.Fatalf("launch requests = %d, want 2 after retryable fresh-session failure", len(requests))
	}
	if !requests[0].ForceNew || !requests[1].ForceNew {
		t.Fatalf("launch requests should keep ForceNew enabled across retries: %#v", requests)
	}
	if requests[1].Prompt != prompt {
		t.Fatalf("second launch prompt = %q, want the original inline prompt after retry", requests[1].Prompt)
	}
	if len(created) != 1 {
		t.Fatalf("created sessions = %d, want 1 successful fresh session", len(created))
	}

	updated, _ = updated.(Model).Update(opened)
	got := updated.(Model)
	snapshot, ok := got.currentCodexSnapshot()
	if !ok {
		t.Fatalf("currentCodexSnapshot() unavailable after handling the opened session")
	}
	if snapshot.ThreadID != freshSessionID {
		t.Fatalf("thread id = %q, want retried fresh session %q", snapshot.ThreadID, freshSessionID)
	}
}

func TestLaunchOpenCodeForSelectionForceNewRetriesWhenOpenCodeReturnsKnownReusedSession(t *testing.T) {
	const staleSessionID = "ses_stale3abcd"
	const freshSessionID = "ses_fresh4efgh"
	const prompt = "continue with a third-force-new attempt"

	var (
		requests []codexapp.LaunchRequest
		created  []*fakeCodexSession
	)
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		switch len(requests) {
		case 1:
			return nil, &codexapp.ForceNewSessionReusedError{Provider: codexapp.ProviderOpenCode, ThreadID: staleSessionID}
		case 2:
			session := &fakeCodexSession{
				projectPath: req.ProjectPath,
				snapshot: codexapp.Snapshot{
					Provider: codexapp.ProviderOpenCode,
					Started:  true,
					Preset:   req.Preset,
					Status:   "OpenCode session ready",
					ThreadID: staleSessionID,
				},
			}
			created = append(created, session)
			return session, nil
		default:
			session := &fakeCodexSession{
				projectPath: req.ProjectPath,
				snapshot: codexapp.Snapshot{
					Provider: codexapp.ProviderOpenCode,
					Started:  true,
					Preset:   req.Preset,
					Status:   "OpenCode session ready",
					ThreadID: freshSessionID,
				},
			}
			created = append(created, session)
			return session, nil
		}
	})

	m := Model{
		codexManager: manager,
		projects: []model.ProjectSummary{{
			Path:                "/tmp/demo",
			Name:                "demo",
			PresentOnDisk:       true,
			LatestSessionFormat: "opencode_db",
		}},
	}

	updated, cmd := m.launchOpenCodeForSelection(true, prompt)
	if cmd == nil {
		t.Fatalf("launchOpenCodeForSelection() should return an open command")
	}

	msg := cmd()
	opened, ok := msg.(codexSessionOpenedMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want codexSessionOpenedMsg", msg)
	}
	if opened.err != nil {
		t.Fatalf("/opencode-new returned error = %v", opened.err)
	}
	if opened.status != "Prompt sent to fresh embedded OpenCode session ses_fres. Alt+Up hides it." {
		t.Fatalf("opened.status = %q, want prompt-sent status after stale-session retry", opened.status)
	}
	if len(requests) != 3 {
		t.Fatalf("launch requests = %d, want 3 when stale thread is not previous or resume", len(requests))
	}
	if requests[0].Prompt != prompt || requests[1].Prompt != prompt || requests[2].Prompt != prompt {
		t.Fatalf("launch prompts changed across retries: %#v", []string{requests[0].Prompt, requests[1].Prompt, requests[2].Prompt})
	}
	if !requests[0].ForceNew || !requests[1].ForceNew || !requests[2].ForceNew {
		t.Fatalf("launch requests should keep ForceNew enabled across retries: %#v", requests)
	}
	if len(created) != 2 {
		t.Fatalf("created sessions = %d, want 2 successful attempts (reused then fresh)", len(created))
	}

	updated, _ = updated.(Model).Update(opened)
	got := updated.(Model)
	snapshot, ok := got.currentCodexSnapshot()
	if !ok {
		t.Fatalf("currentCodexSnapshot() unavailable after handling the opened session")
	}
	if snapshot.ThreadID != freshSessionID {
		t.Fatalf("thread id = %q, want retried fresh session %q", snapshot.ThreadID, freshSessionID)
	}
}

func TestLaunchCodexForSelectionForceNewWarnsWhenActiveSessionIsReopenedReadOnly(t *testing.T) {
	const threadID = "019cccc3abcd"

	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider:     codexapp.ProviderCodex,
				Started:      true,
				Preset:       req.Preset,
				Status:       "Codex session ready",
				ThreadID:     threadID,
				BusyExternal: true,
			},
		}, nil
	})

	m := Model{
		codexManager: manager,
		projects: []model.ProjectSummary{{
			Path:                "/tmp/demo",
			Name:                "demo",
			PresentOnDisk:       true,
			LatestSessionID:     threadID,
			LatestSessionFormat: "modern",
		}},
	}

	updated, cmd := m.launchCodexForSelection(true, "")
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("launchCodexForSelection() should return an open command")
	}

	msg := cmd()
	opened, ok := msg.(codexSessionOpenedMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want codexSessionOpenedMsg", msg)
	}
	if opened.err != nil {
		t.Fatalf("/codex-new returned error = %v", opened.err)
	}
	wantStatus := "Could not start a fresh embedded Codex session because session 019cccc3 is already active in another process. Showing that session read-only instead."
	if opened.status != wantStatus {
		t.Fatalf("opened.status = %q, want %q", opened.status, wantStatus)
	}

	updated, _ = got.Update(opened)
	got = updated.(Model)
	if got.status != wantStatus {
		t.Fatalf("status = %q, want %q", got.status, wantStatus)
	}
}
