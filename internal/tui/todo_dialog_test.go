package tui

import (
	"testing"

	"lcroom/internal/model"
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

func TestNewTodoTextInputAllowsLongPrompts(t *testing.T) {
	t.Parallel()

	input := newTodoTextInput("")
	if input.CharLimit < 10000 {
		t.Fatalf("newTodoTextInput CharLimit = %d, want at least 10000", input.CharLimit)
	}
}
