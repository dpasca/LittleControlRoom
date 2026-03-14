package tui

import (
	"testing"

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
