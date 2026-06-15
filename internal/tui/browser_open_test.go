package tui

import (
	"testing"
	"time"

	"lcroom/internal/browserctl"
	"lcroom/internal/commands"
	"lcroom/internal/model"
)

func TestOpenProjectDirInBrowserUsesDirectoryFileURL(t *testing.T) {
	dir := t.TempDir()

	previousOpener := externalBrowserOpener
	defer func() { externalBrowserOpener = previousOpener }()

	called := ""
	externalBrowserOpener = func(rawURL string) error {
		called = rawURL
		return nil
	}

	if err := openProjectDirInBrowser(dir); err != nil {
		t.Fatalf("openProjectDirInBrowser() error = %v", err)
	}
	if called != directoryFileURL(dir) {
		t.Fatalf("opened URL = %q, want %q", called, directoryFileURL(dir))
	}
}

func TestOpenProjectDirInTerminalUsesProjectDirectory(t *testing.T) {
	dir := t.TempDir()

	previousOpener := externalTerminalOpener
	defer func() { externalTerminalOpener = previousOpener }()

	called := ""
	externalTerminalOpener = func(path string) error {
		called = path
		return nil
	}

	if err := openProjectDirInTerminal(dir); err != nil {
		t.Fatalf("openProjectDirInTerminal() error = %v", err)
	}
	if called != dir {
		t.Fatalf("opened terminal path = %q, want %q", called, dir)
	}
}

func TestOpenRuntimeURLInBrowserUsesRawURL(t *testing.T) {
	previousOpener := externalBrowserOpener
	defer func() { externalBrowserOpener = previousOpener }()

	called := ""
	externalBrowserOpener = func(rawURL string) error {
		called = rawURL
		return nil
	}

	if err := openRuntimeURLInBrowser("http://127.0.0.1:3000/"); err != nil {
		t.Fatalf("openRuntimeURLInBrowser() error = %v", err)
	}
	if called != "http://127.0.0.1:3000/" {
		t.Fatalf("opened URL = %q, want runtime URL", called)
	}
}

func TestManagedBrowserStateFreshForUIRejectsStaleState(t *testing.T) {
	now := time.Date(2026, time.May, 30, 17, 0, 0, 0, time.UTC)
	fresh := browserctl.ManagedPlaywrightState{
		SessionKey: "managed-demo",
		BrowserPID: 123,
		UpdatedAt:  now.Add(-5 * time.Second),
	}
	if !managedBrowserStateFreshForUI(fresh, now) {
		t.Fatal("fresh managed browser state should be accepted")
	}

	stale := fresh
	stale.UpdatedAt = now.Add(-time.Hour)
	if managedBrowserStateFreshForUI(stale, now) {
		t.Fatal("stale managed browser state should be rejected")
	}

	missingBrowser := fresh
	missingBrowser.BrowserPID = 0
	if managedBrowserStateFreshForUI(missingBrowser, now) {
		t.Fatal("state without a revealable browser should be rejected")
	}
}

func TestDispatchOpenCommandOpensSelectedProjectInBrowser(t *testing.T) {
	dir := t.TempDir()

	previousOpener := externalBrowserOpener
	defer func() { externalBrowserOpener = previousOpener }()

	called := ""
	externalBrowserOpener = func(rawURL string) error {
		called = rawURL
		return nil
	}

	m := Model{
		projects: []model.ProjectSummary{{
			Name:          "demo",
			Path:          dir,
			PresentOnDisk: true,
		}},
		selected: 0,
	}

	updated, cmd := m.dispatchCommand(commands.Invocation{Kind: commands.KindOpen})
	got := updated.(Model)
	if got.status != "Opening project in browser..." {
		t.Fatalf("status = %q, want opening status", got.status)
	}
	if cmd == nil {
		t.Fatalf("dispatchCommand(/open) should return an open command")
	}

	msg := cmd()
	openMsg, ok := msg.(browserOpenMsg)
	if !ok {
		t.Fatalf("cmd() message type = %T, want browserOpenMsg", msg)
	}
	if openMsg.err != nil {
		t.Fatalf("browserOpenMsg.err = %v, want nil", openMsg.err)
	}
	if openMsg.status != "Opened project in browser" {
		t.Fatalf("browserOpenMsg.status = %q, want success status", openMsg.status)
	}
	if called != directoryFileURL(dir) {
		t.Fatalf("opened URL = %q, want %q", called, directoryFileURL(dir))
	}
}

func TestDispatchTerminalCommandOpensSelectedProjectInTerminal(t *testing.T) {
	dir := t.TempDir()

	previousOpener := externalTerminalOpener
	defer func() { externalTerminalOpener = previousOpener }()

	called := ""
	externalTerminalOpener = func(path string) error {
		called = path
		return nil
	}

	m := Model{
		projects: []model.ProjectSummary{{
			Name:          "demo",
			Path:          dir,
			PresentOnDisk: true,
		}},
		selected: 0,
	}

	updated, cmd := m.dispatchCommand(commands.Invocation{Kind: commands.KindTerminal})
	got := updated.(Model)
	if got.status != "Opening project terminal..." {
		t.Fatalf("status = %q, want opening terminal status", got.status)
	}
	if cmd == nil {
		t.Fatalf("dispatchCommand(/terminal) should return an open command")
	}

	msg := cmd()
	openMsg, ok := msg.(browserOpenMsg)
	if !ok {
		t.Fatalf("cmd() message type = %T, want browserOpenMsg", msg)
	}
	if openMsg.err != nil {
		t.Fatalf("browserOpenMsg.err = %v, want nil", openMsg.err)
	}
	if openMsg.status != "Opened project terminal" {
		t.Fatalf("browserOpenMsg.status = %q, want terminal success status", openMsg.status)
	}
	if called != dir {
		t.Fatalf("opened terminal path = %q, want %q", called, dir)
	}
}
