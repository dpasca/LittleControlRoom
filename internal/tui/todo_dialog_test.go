package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"lcroom/internal/model"
	"lcroom/internal/viewportnav"
)

func TestNormalizeTodoTextPreservesBlankLines(t *testing.T) {
	t.Parallel()

	raw := "line one\r\n\r\nline two\r\n   \nline three"
	got := normalizeTodoText(raw)
	want := "line one\n\nline two\n   \nline three"
	if got != want {
		t.Fatalf("normalizeTodoText(%q) = %q, want %q", raw, got, want)
	}
}

func TestTodoPreviewTextStripsNewlinesForSingleLinePreview(t *testing.T) {
	t.Parallel()

	raw := "line one\r\n\r\nline two\n   line three\tline four"
	got := todoPreviewText(raw)
	want := "line one line two line three line four"
	if got != want {
		t.Fatalf("todoPreviewText(%q) = %q, want %q", raw, got, want)
	}
}

func TestTodoDialogItemLineUsesSingleLinePreview(t *testing.T) {
	t.Parallel()

	m := Model{}
	line := m.todoDialogItemLine(model.TodoItem{
		Text: "Fix spacing\non selected TODO row",
	}, "[ ]", 80)
	if line != "[ ] Fix spacing on selected TODO row" {
		t.Fatalf("todoDialogItemLine() = %q, want single-line preview", line)
	}
}

func TestTodoDialogItemLineShowsWorkStateHint(t *testing.T) {
	t.Parallel()

	m := Model{}
	line := m.todoDialogItemLine(model.TodoItem{
		Text:         "Fix Boss handoff",
		WorkProvider: model.SessionSourceCodex,
		WorkState:    model.TodoWorkStateWaiting,
	}, "[ ]", 80)
	if !strings.Contains(line, "waiting Codex") {
		t.Fatalf("todoDialogItemLine() = %q, want waiting Codex hint", line)
	}
}

func TestTodoDialogItemLineMarksMissingPinnedSessionStale(t *testing.T) {
	t.Parallel()

	m := Model{}
	line := m.todoDialogItemLine(model.TodoItem{
		Text:          "Finish the LCAgent lane",
		WorkProvider:  model.SessionSourceLCAgent,
		WorkSessionID: "lcagent:run-1",
		WorkState:     model.TodoWorkStateWorking,
	}, "[ ]", 80)
	if !strings.Contains(line, "stale LCAgent") {
		t.Fatalf("todoDialogItemLine() = %q, want stale LCAgent hint", line)
	}
}

func TestNewTodoTextInputAllowsLongPrompts(t *testing.T) {
	t.Parallel()

	input := newTodoTextInput("")
	if input.CharLimit < 10000 {
		t.Fatalf("newTodoTextInput CharLimit = %d, want at least 10000", input.CharLimit)
	}
}

func newTodoEditorModelWithLines(t *testing.T, count int) Model {
	t.Helper()

	lines := make([]string, 0, count)
	for i := 0; i < count; i++ {
		lines = append(lines, fmt.Sprintf("line %03d", i))
	}
	m := Model{
		todoDialog: &todoDialogState{ProjectPath: "/tmp/demo", ProjectName: "demo"},
		todoEditor: &todoEditorState{
			ProjectPath: "/tmp/demo",
			ProjectName: "demo",
			Input:       newTodoTextInput(strings.Join(lines, "\n")),
		},
	}
	m.todoEditor.Input.Focus()
	return m
}

func TestTodoEditorEnterInsertsNewlineInLongValue(t *testing.T) {
	t.Parallel()

	// Pasting more rows than the bubbles textarea default cap used to make
	// Enter a silent no-op.
	m := newTodoEditorModelWithLines(t, 150)
	before := m.todoEditor.Input.Value()

	updated, _ := m.updateTodoEditorMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if got.todoEditor == nil {
		t.Fatal("enter should keep the todo editor open")
	}
	if want := before + "\n"; got.todoEditor.Input.Value() != want {
		t.Fatalf("enter on a 150-line value did not insert a newline: value length = %d, want %d",
			len(got.todoEditor.Input.Value()), len(want))
	}
}

func TestTodoEditorPageKeysMoveThroughLongValue(t *testing.T) {
	t.Parallel()

	m := newTodoEditorModelWithLines(t, 150)
	step := viewportnav.PageStep(m.todoEditor.Input.Height())
	if step < 2 {
		t.Fatalf("page step = %d, want a multi-line page for a %d-row editor", step, m.todoEditor.Input.Height())
	}
	bottomRow := m.todoEditor.Input.Line()

	updated, _ := m.updateTodoEditorMode(tea.KeyMsg{Type: tea.KeyPgUp})
	got := updated.(Model)
	if row := got.todoEditor.Input.Line(); row != bottomRow-step {
		t.Fatalf("row after pgup = %d, want %d", row, bottomRow-step)
	}

	updated, _ = got.updateTodoEditorMode(tea.KeyMsg{Type: tea.KeyPgDown})
	got = updated.(Model)
	if row := got.todoEditor.Input.Line(); row != bottomRow {
		t.Fatalf("row after pgdown = %d, want %d", row, bottomRow)
	}
	if value := got.todoEditor.Input.Value(); !strings.HasSuffix(value, "line 149") {
		t.Fatal("page navigation should not modify the todo text")
	}
}
