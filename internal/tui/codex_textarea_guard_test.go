package tui

import (
	"testing"
	"time"

	"lcroom/internal/codexapp"
	"lcroom/internal/codexcli"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
)

func TestVisibleCodexWordBackwardAtEmptyLineStartDoesNotHang(t *testing.T) {
	cases := []struct {
		name string
		msg  tea.KeyMsg
	}{
		{
			name: "alt+b",
			msg:  tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}, Alt: true},
		},
		{
			name: "alt+left",
			msg:  tea.KeyMsg{Type: tea.KeyLeft, Alt: true},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

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
			input.SetValue("first line\n")
			input.Focus()
			before := input.Value()

			m := Model{
				codexManager:        manager,
				codexVisibleProject: "/tmp/demo",
				codexHiddenProject:  "/tmp/demo",
				codexInput:          input,
				codexViewport:       viewport.New(0, 0),
				width:               100,
				height:              24,
			}

			done := make(chan Model, 1)
			go func() {
				updated, _ := m.updateCodexMode(tc.msg)
				done <- updated.(Model)
			}()

			select {
			case got := <-done:
				if got.codexInput.Value() != before {
					t.Fatalf("composer value changed to %q, want %q", got.codexInput.Value(), before)
				}
			case <-time.After(250 * time.Millisecond):
				t.Fatalf("%s should not hang at the start of an empty line", tc.name)
			}
		})
	}
}

func TestVisibleCodexWordBackwardStillMovesWhenSafe(t *testing.T) {
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
	input.SetValue("first second")
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

	updated, _ := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}, Alt: true})
	got := updated.(Model)

	rows, row, col, ok := codexTextareaState(&got.codexInput)
	if !ok {
		t.Fatal("could not read textarea state")
	}
	if row != 0 {
		t.Fatalf("row = %d, want 0", row)
	}
	if col != 6 {
		t.Fatalf("col = %d, want 6", col)
	}
	if string(rows[row]) != "first second" {
		t.Fatalf("row text = %q, want %q", string(rows[row]), "first second")
	}
}
