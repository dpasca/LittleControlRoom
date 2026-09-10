package tui

import (
	"lcroom/internal/codexapp"
	"lcroom/internal/codexslash"
	"lcroom/internal/projectrun"
	"strings"
	"testing"
)

func TestImageReviewToggleReconnectsOnlyExactSession(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")
	dir := t.TempDir()
	var launches []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, _ func()) (codexapp.Session, error) {
		launches = append(launches, req)
		return &fakeCodexSession{projectPath: req.ProjectPath, snapshot: codexapp.Snapshot{Provider: codexapp.ProviderCodex, ProjectPath: req.ProjectPath, ThreadID: "thread-review", Started: true, ImageReviewEnabled: req.ImageReviewEnabled}}, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{ProjectPath: dir, Provider: codexapp.ProviderCodex}); err != nil {
		t.Fatal(err)
	}
	runtime := projectrun.NewManager()
	defer runtime.CloseAll()
	m := Model{codexManager: manager, runtimeManager: runtime, codexVisibleProject: dir}
	for _, enabled := range []bool{true, false} {
		cmd := m.reconnectVisibleCodexSessionWithImageReviewCmd(&enabled)
		if cmd == nil {
			t.Fatal("missing reconnect command")
		}
		msg := cmd().(codexSessionOpenedMsg)
		if msg.err != nil {
			t.Fatal(msg.err)
		}
		last := launches[len(launches)-1]
		if !last.RequireResumeID || last.ResumeID != "thread-review" || last.ImageReviewEnabled != enabled || last.ForceNew {
			t.Fatalf("wrong reconnect target or setting: id=%s enabled=%v", last.ResumeID, last.ImageReviewEnabled)
		}
		if msg.snapshot.ImageReviewEnabled != enabled {
			t.Fatal("snapshot lost review state")
		}
		if enabled {
			reconnected := m.reconnectVisibleCodexSessionCmd()().(codexSessionOpenedMsg)
			last = launches[len(launches)-1]
			if reconnected.err != nil || !last.RequireResumeID || !last.ImageReviewEnabled {
				t.Fatal("ordinary reconnect must preserve opt-in only on the exact thread")
			}
		}
	}
	if launches[0].ImageReviewEnabled {
		t.Fatal("ordinary session unexpectedly enabled")
	}
}

func TestImageReviewMissingKeyLeavesSessionOpen(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	dir := t.TempDir()
	count := 0
	session := &fakeCodexSession{projectPath: dir, snapshot: codexapp.Snapshot{Provider: codexapp.ProviderCodex, ProjectPath: dir, ThreadID: "thread-review", Started: true}}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, _ func()) (codexapp.Session, error) { count++; return session, nil })
	if _, _, err := manager.Open(codexapp.LaunchRequest{ProjectPath: dir, Provider: codexapp.ProviderCodex}); err != nil {
		t.Fatal(err)
	}
	runtime := projectrun.NewManager()
	defer runtime.CloseAll()
	m := Model{codexManager: manager, runtimeManager: runtime, codexVisibleProject: dir}
	enabled := true
	msg := m.reconnectVisibleCodexSessionWithImageReviewCmd(&enabled)().(codexSessionOpenedMsg)
	if msg.err == nil || !strings.Contains(msg.err.Error(), "OPENAI_API_KEY") || count != 1 {
		t.Fatalf("missing key should fail before replacement: count=%d err=%v", count, msg.err)
	}
	if existing, ok := manager.Session(dir); !ok || existing != session || existing.Snapshot().Closed {
		t.Fatal("original session was closed")
	}
}

func TestImageReviewSlashParsing(t *testing.T) {
	for _, mode := range []string{"", "on", "off"} {
		inv, err := codexslash.Parse(strings.TrimSpace("/image-review " + mode))
		if err != nil || inv.Kind != codexslash.KindImageReview || inv.ImageReviewMode != mode {
			t.Fatalf("parse %q: %+v %v", mode, inv, err)
		}
	}
	if _, err := codexslash.Parse("/image-review automatic"); err == nil {
		t.Fatal("unsupported mode accepted")
	}
}

func TestImageReviewRejectsBusyAndOtherProviders(t *testing.T) {
	for _, snapshot := range []codexapp.Snapshot{
		{Provider: codexapp.ProviderCodex, ThreadID: "test", Busy: true},
		{Provider: codexapp.ProviderCodex, ThreadID: "test", BusyExternal: true},
		{Provider: codexapp.ProviderOpenCode, ThreadID: "test"},
	} {
		m := Model{}
		_, cmd := m.setVisibleImageReview(snapshot, "on")
		if cmd != nil {
			t.Fatalf("must not replace busy/unsupported session: %+v", snapshot)
		}
	}
}
