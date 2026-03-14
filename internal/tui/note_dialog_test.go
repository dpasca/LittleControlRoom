package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

func TestNoteCopyScopeTextWholeNote(t *testing.T) {
	editor := newNoteTextarea("alpha\nbeta line\n\ngamma")
	if got := noteCopyScopeText(editor, noteCopyScopeWhole); got != "alpha\nbeta line\n\ngamma" {
		t.Fatalf("noteCopyScopeText(whole) = %q, want full note", got)
	}
}

func TestNoteSelectedTextUsesSortedEndpoints(t *testing.T) {
	editor := newNoteTextarea("alpha\nbeta line\n\ngamma")
	if got := noteSelectedText(editor, 3, 2, 1, 1); got != "eta line\n\nga" {
		t.Fatalf("noteSelectedText(reversed) = %q, want normalized range", got)
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
	if got.status != "Choose whole note or selected text" {
		t.Fatalf("status = %q, want copy menu status", got.status)
	}
	if cmd != nil {
		t.Fatalf("opening the copy menu should not return a command")
	}
}

func TestNoteCopyDialogSelectedTextStartsSelectionMode(t *testing.T) {
	m := Model{
		noteDialog: &noteDialogState{
			ProjectPath:  "/tmp/demo",
			ProjectName:  "demo",
			OriginalNote: "alpha\nbeta",
			Editor:       newNoteTextarea("alpha\nbeta"),
			Selected:     noteDialogFocusCopy,
		},
		noteCopyDialog: &noteCopyDialogState{Selected: noteCopyScopeSelectedText},
	}

	updated, cmd := m.updateNoteCopyDialogMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if got.noteCopyDialog != nil {
		t.Fatalf("copy menu should close when selection mode starts")
	}
	if got.noteDialog == nil || got.noteDialog.Selection == nil {
		t.Fatalf("selection mode should start after choosing selected text")
	}
	if got.noteDialog.Selected != noteDialogFocusEditor {
		t.Fatalf("selection mode should return focus to the editor")
	}
	if got.status != "Selection mode: move to the start and press Space" {
		t.Fatalf("status = %q, want selection mode status", got.status)
	}
	if cmd == nil {
		t.Fatalf("starting selection mode should refocus the editor")
	}
}

func TestNoteTextSelectionCopiesRangeOnSecondSpace(t *testing.T) {
	previousWriter := clipboardTextWriter
	defer func() { clipboardTextWriter = previousWriter }()

	var copied string
	clipboardTextWriter = func(text string) error {
		copied = text
		return nil
	}

	editor := newNoteTextarea("alpha\nbeta\n\ngamma")
	moveNoteEditorCursor(&editor, 0, 1)

	m := Model{
		noteDialog: &noteDialogState{
			ProjectPath:  "/tmp/demo",
			ProjectName:  "demo",
			OriginalNote: "alpha\nbeta\n\ngamma",
			Editor:       editor,
			Selected:     noteDialogFocusEditor,
			Selection:    &noteTextSelectionState{},
		},
	}

	updated, cmd := m.updateNoteDialogMode(tea.KeyMsg{Type: tea.KeySpace})
	got := updated.(Model)
	if got.noteDialog == nil || got.noteDialog.Selection == nil || !got.noteDialog.Selection.AnchorSet {
		t.Fatalf("first space should set the selection anchor")
	}
	if got.status != "Selection start set. Move to the end and press Space again." {
		t.Fatalf("status = %q, want selection start status", got.status)
	}
	if cmd != nil {
		t.Fatalf("setting the anchor should not return a command")
	}

	moveNoteEditorCursor(&got.noteDialog.Editor, 1, 2)
	updated, cmd = got.updateNoteDialogMode(tea.KeyMsg{Type: tea.KeySpace})
	got = updated.(Model)
	if got.noteDialog == nil {
		t.Fatalf("note dialog should remain open after copying the selection")
	}
	if got.noteDialog.Selection != nil {
		t.Fatalf("selection mode should end after copying")
	}
	if got.status != "Selected note text copied to clipboard" {
		t.Fatalf("status = %q, want selected text copied status", got.status)
	}
	if copied != "lpha\nbe" {
		t.Fatalf("copied text = %q, want selected range", copied)
	}
	if cmd == nil {
		t.Fatalf("finishing selection mode should restore editor focus")
	}
}

func TestRenderNoteDialogContentHighlightsActiveSelection(t *testing.T) {
	previousProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	defer lipgloss.SetColorProfile(previousProfile)

	editor := newNoteTextarea("alpha\nbeta")
	moveNoteEditorCursor(&editor, 1, 2)

	m := Model{
		noteDialog: &noteDialogState{
			ProjectPath:  "/tmp/demo",
			ProjectName:  "demo",
			OriginalNote: "alpha\nbeta",
			Editor:       editor,
			Selected:     noteDialogFocusEditor,
			Selection: &noteTextSelectionState{
				AnchorSet:  true,
				AnchorLine: 0,
				AnchorCol:  1,
			},
		},
	}

	rendered := m.renderNoteDialogContent(40, 6)
	if !strings.Contains(rendered, "48;5;60") {
		t.Fatalf("rendered note dialog should include selection highlight styling, got %q", rendered)
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
