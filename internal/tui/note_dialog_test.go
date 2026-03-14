package tui

import (
	"testing"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
)

func TestNoteCopyScopeTextUsesCursorPosition(t *testing.T) {
	editor := newNoteTextarea("alpha\nbeta line\n\ngamma")
	moveNoteEditorCursor(&editor, 1, 4)

	tests := []struct {
		name  string
		scope int
		want  string
	}{
		{name: "whole", scope: noteCopyScopeWhole, want: "alpha\nbeta line\n\ngamma"},
		{name: "line", scope: noteCopyScopeCurrentLine, want: "beta line"},
		{name: "paragraph", scope: noteCopyScopeCurrentParagraph, want: "alpha\nbeta line"},
		{name: "start to cursor", scope: noteCopyScopeStartToCursor, want: "alpha\nbeta"},
		{name: "cursor to end", scope: noteCopyScopeCursorToEnd, want: " line\n\ngamma"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := noteCopyScopeText(editor, tt.scope); got != tt.want {
				t.Fatalf("noteCopyScopeText(%s) = %q, want %q", tt.name, got, tt.want)
			}
		})
	}
}

func TestNoteDialogCtrlYCopiesWholeNote(t *testing.T) {
	previousWriter := clipboardTextWriter
	defer func() { clipboardTextWriter = previousWriter }()

	var copied string
	clipboardTextWriter = func(text string) error {
		copied = text
		return nil
	}

	m := Model{
		noteDialog: &noteDialogState{
			ProjectPath:  "/tmp/demo",
			ProjectName:  "demo",
			OriginalNote: "prompt draft",
			Editor:       newNoteTextarea("prompt draft"),
			Selected:     noteDialogFocusEditor,
		},
	}

	updated, cmd := m.updateNoteDialogMode(tea.KeyMsg{Type: tea.KeyCtrlY})
	got := updated.(Model)
	if got.noteDialog == nil {
		t.Fatalf("note dialog should remain open after copying")
	}
	if got.noteCopyDialog != nil {
		t.Fatalf("ctrl+y should not open the copy menu")
	}
	if got.status != "Note copied to clipboard" {
		t.Fatalf("status = %q, want note copied status", got.status)
	}
	if copied != "prompt draft" {
		t.Fatalf("copied text = %q, want full note", copied)
	}
	if cmd != nil {
		t.Fatalf("ctrl+y should not return a command")
	}
}

func TestNoteDialogCopyActionOpensCopyMenu(t *testing.T) {
	m := Model{
		noteDialog: &noteDialogState{
			ProjectPath:  "/tmp/demo",
			ProjectName:  "demo",
			OriginalNote: "prompt draft",
			Editor:       newNoteTextarea("prompt draft"),
			Selected:     noteDialogFocusCopy,
		},
	}

	updated, cmd := m.updateNoteDialogMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if got.noteCopyDialog == nil {
		t.Fatalf("copy action should open the note copy menu")
	}
	if got.noteCopyDialog.Selected != noteCopyScopeWhole {
		t.Fatalf("default copy selection = %d, want whole note", got.noteCopyDialog.Selected)
	}
	if got.status != "Choose which note text to copy" {
		t.Fatalf("status = %q, want copy menu status", got.status)
	}
	if cmd != nil {
		t.Fatalf("opening the copy menu should not return a command")
	}
}

func TestNoteCopyDialogCopiesSelectionAndClosesMenu(t *testing.T) {
	previousWriter := clipboardTextWriter
	defer func() { clipboardTextWriter = previousWriter }()

	var copied string
	clipboardTextWriter = func(text string) error {
		copied = text
		return nil
	}

	editor := newNoteTextarea("alpha\nbeta\n\ngamma")
	moveNoteEditorCursor(&editor, 1, 2)

	m := Model{
		noteDialog: &noteDialogState{
			ProjectPath:  "/tmp/demo",
			ProjectName:  "demo",
			OriginalNote: "alpha\nbeta\n\ngamma",
			Editor:       editor,
			Selected:     noteDialogFocusCopy,
		},
		noteCopyDialog: &noteCopyDialogState{Selected: noteCopyScopeStartToCursor},
	}

	updated, cmd := m.updateNoteCopyDialogMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if got.noteCopyDialog != nil {
		t.Fatalf("copy menu should close after copying")
	}
	if got.noteDialog == nil {
		t.Fatalf("note dialog should remain open after copying")
	}
	if got.status != "Note text before the cursor copied to clipboard" {
		t.Fatalf("status = %q, want start-to-cursor copy status", got.status)
	}
	if copied != "alpha\nbe" {
		t.Fatalf("copied text = %q, want text before cursor", copied)
	}
	if cmd != nil {
		t.Fatalf("copying should not return a command when focus stays on actions")
	}
}

func moveNoteEditorCursor(editor *textarea.Model, line, col int) {
	for editor.Line() > line {
		editor.CursorUp()
	}
	for editor.Line() < line {
		editor.CursorDown()
	}
	editor.CursorStart()
	editor.SetCursor(col)
}
