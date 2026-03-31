package tui

import "testing"

func TestNormalizeTodoTextPreservesBlankLines(t *testing.T) {
	t.Parallel()

	raw := "line one\r\n\r\nline two\r\n   \nline three"
	got := normalizeTodoText(raw)
	want := "line one\n\nline two\n   \nline three"
	if got != want {
		t.Fatalf("normalizeTodoText(%q) = %q, want %q", raw, got, want)
	}
}

func TestNewTodoTextInputAllowsLongPrompts(t *testing.T) {
	t.Parallel()

	input := newTodoTextInput("")
	if input.CharLimit < 10000 {
		t.Fatalf("newTodoTextInput CharLimit = %d, want at least 10000", input.CharLimit)
	}
}
