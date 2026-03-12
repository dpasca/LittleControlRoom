package tui

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"lcroom/internal/commands"
	"lcroom/internal/config"
	"lcroom/internal/events"
	"lcroom/internal/service"
	"lcroom/internal/store"

	tea "github.com/charmbracelet/bubbletea"
)

func TestDispatchNewProjectCommandOpensDialogWithRecentPathDefault(t *testing.T) {
	m := Model{
		width:                   100,
		height:                  28,
		homeDirFn:               func() (string, error) { return "/Users/tester", nil },
		newProjectRecentParents: []string{"/tmp/work"},
	}

	updated, cmd := m.dispatchCommand(commands.Invocation{Kind: commands.KindNewProject})
	got := updated.(Model)
	if got.newProjectDialog == nil {
		t.Fatalf("expected new project dialog to open")
	}
	if got.newProjectDialog.PathInput.Value() != "/tmp/work" {
		t.Fatalf("default path = %q, want recent path", got.newProjectDialog.PathInput.Value())
	}
	if cmd == nil {
		t.Fatalf("opening the dialog should focus the first field")
	}

	rendered := got.renderNewProjectContent(72)
	if !strings.Contains(rendered, "New Project") || !strings.Contains(rendered, "/tmp/work") {
		t.Fatalf("rendered dialog missing title or default path: %q", rendered)
	}
}

func TestNewProjectDialogCreatesProjectAndSelectsIt(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	svc := service.New(config.Default(), st, events.NewBus(), nil)
	parent := t.TempDir()

	m := New(ctx, svc)
	m.width = 100
	m.height = 28
	m.homeDirFn = func() (string, error) { return parent, nil }

	updated, _ := m.dispatchCommand(commands.Invocation{Kind: commands.KindNewProject})
	got := updated.(Model)
	if got.newProjectDialog == nil {
		t.Fatalf("expected dialog to open")
	}
	got.newProjectDialog.PathInput.SetValue(parent)
	got.newProjectDialog.NameInput.SetValue("demo")
	got.newProjectDialog.CreateGitRepo = false

	updated, cmd := got.updateNewProjectMode(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if cmd == nil {
		t.Fatalf("enter should submit the new project dialog")
	}
	if got.status != "Creating project..." {
		t.Fatalf("status = %q, want creating notice", got.status)
	}

	msg := cmd()
	updated, loadCmd := got.Update(msg)
	got = updated.(Model)
	if got.newProjectDialog != nil {
		t.Fatalf("dialog should close after a successful create")
	}
	if loadCmd == nil {
		t.Fatalf("successful create should reload the project list")
	}

	msg = loadCmd()
	updated, _ = got.Update(msg)
	got = updated.(Model)
	selected, ok := got.selectedProject()
	if !ok {
		t.Fatalf("expected the created project to be selected")
	}
	wantPath := filepath.Join(parent, "demo")
	if selected.Path != wantPath {
		t.Fatalf("selected path = %q, want %q", selected.Path, wantPath)
	}
}

func TestNewProjectDialogAltDigitAppliesRecentPath(t *testing.T) {
	m := Model{
		width:                   100,
		height:                  28,
		homeDirFn:               func() (string, error) { return "/Users/tester", nil },
		newProjectRecentParents: []string{"/tmp/one", "/tmp/two", "/tmp/three"},
	}
	updated, _ := m.dispatchCommand(commands.Invocation{Kind: commands.KindNewProject})
	got := updated.(Model)

	updated, cmd := got.updateNewProjectMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}, Alt: true})
	got = updated.(Model)
	if cmd != nil {
		t.Fatalf("alt+digit should not return an async command")
	}
	if got.newProjectDialog.PathInput.Value() != "/tmp/two" {
		t.Fatalf("path input = %q, want /tmp/two", got.newProjectDialog.PathInput.Value())
	}
}
