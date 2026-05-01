package tui

import (
	"strings"
	"testing"

	"lcroom/internal/codexapp"
	"lcroom/internal/codexcli"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
)

func TestVisibleCodexAltCOpensDialogAndCopiesFullMultilineComposerInput(t *testing.T) {
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
		t.Fatalf("alt+c dialog should not queue a command")
	}
	if got.codexInputCopyDialog == nil {
		t.Fatalf("alt+c should open the input copy dialog")
	}
	if copied != "" {
		t.Fatalf("alt+c should not copy before a dialog choice, copied %q", copied)
	}

	updated, cmd = got.updateCodexMode(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if cmd != nil {
		t.Fatalf("copy all should not queue a command")
	}
	if copied != input.Value() {
		t.Fatalf("copied = %q, want full composer input %q", copied, input.Value())
	}
	if got.codexInputCopyDialog != nil {
		t.Fatalf("copy dialog should close after choosing copy all")
	}
	if got.codexInput.Value() != input.Value() {
		t.Fatalf("composer value changed to %q, want %q", got.codexInput.Value(), input.Value())
	}
	if got.status != "Copied full composer input to clipboard" {
		t.Fatalf("status = %q, want copy confirmation", got.status)
	}
}

func TestVisibleCodexAltCDialogCanStartSelectionMode(t *testing.T) {
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
	input.SetValue("line 1\nline 2")
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

	updated, _ := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}, Alt: true})
	got := updated.(Model)
	updated, _ = got.updateCodexMode(tea.KeyMsg{Type: tea.KeyTab})
	got = updated.(Model)
	updated, cmd := got.updateCodexMode(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if cmd != nil {
		t.Fatalf("starting selection should not queue a command")
	}
	if got.codexInputCopyDialog != nil {
		t.Fatalf("copy dialog should close after choosing selection")
	}
	if got.codexInputSelection == nil {
		t.Fatalf("selection mode should be active")
	}
	if got.status != "Selection mode: move to the start and press Space" {
		t.Fatalf("status = %q, want selection instructions", got.status)
	}
}

func TestVisibleCodexAltCCopyAllExpandsLargePastePlaceholder(t *testing.T) {
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

	hidden := strings.Join([]string{"alpha", "beta", "gamma"}, "\n")
	token := "[Paste #1: 3 lines]"
	input := newCodexTextarea()
	input.SetValue(token + " summarize this")
	input.Focus()

	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          input,
		codexDrafts: map[string]codexDraft{
			"/tmp/demo": {
				PastedTexts: []codexPastedText{{
					Token: token,
					Text:  hidden,
				}},
			},
		},
		codexViewport: viewport.New(0, 0),
		width:         100,
		height:        24,
	}

	updated, _ := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}, Alt: true})
	got := updated.(Model)
	updated, cmd := got.updateCodexMode(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if cmd != nil {
		t.Fatalf("copy all should not queue a command")
	}
	if want := hidden + " summarize this"; copied != want {
		t.Fatalf("copied = %q, want expanded paste %q", copied, want)
	}
	if got.codexInput.Value() != token+" summarize this" {
		t.Fatalf("composer value changed to %q", got.codexInput.Value())
	}
	if got.status != "Copied full composer input to clipboard" {
		t.Fatalf("status = %q, want copy confirmation", got.status)
	}
}

func TestVisibleCodexAltCDialogCanCopyVisibleOutput(t *testing.T) {
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

	vp := viewport.New(80, 2)
	vp.SetContent("older\nvisible one\nvisible two\nnewer")
	vp.SetYOffset(1)
	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          newCodexTextarea(),
		codexViewport:       vp,
		width:               100,
		height:              24,
	}

	updated, _ := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}, Alt: true})
	got := updated.(Model)
	updated, cmd := got.updateCodexMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	got = updated.(Model)
	if cmd != nil {
		t.Fatalf("copy visible output should not queue a command")
	}
	if copied != "visible one\nvisible two" {
		t.Fatalf("copied visible output = %q", copied)
	}
	if got.codexInputCopyDialog != nil {
		t.Fatalf("copy dialog should close after copying output")
	}
	if got.status != "Copied visible output to clipboard" {
		t.Fatalf("status = %q, want output copy confirmation", got.status)
	}
}
