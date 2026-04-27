package tui

import (
	"testing"

	"lcroom/internal/codexapp"
	"lcroom/internal/codexcli"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
)

func TestVisibleCodexAltCCopiesFullMultilineComposerInput(t *testing.T) {
	var copied string
	previousWriter := clipboardTextWriter
	clipboardTextWriter = func(text string) error {
		copied = text
		return nil
	}
	t.Cleanup(func() {
		clipboardTextWriter = previousWriter
	})

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
	input.SetHeight(3)
	input.SetValue("line 1\nline 2\nline 3\nline 4\nline 5")
	input.Focus()

	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          input,
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              24,
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}, Alt: true})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("alt+c copy should not queue a command")
	}
	if copied != input.Value() {
		t.Fatalf("copied = %q, want full composer input %q", copied, input.Value())
	}
	if got.codexInput.Value() != input.Value() {
		t.Fatalf("composer value changed to %q, want %q", got.codexInput.Value(), input.Value())
	}
	if got.status != "Copied full composer input to clipboard" {
		t.Fatalf("status = %q, want copy confirmation", got.status)
	}
}
