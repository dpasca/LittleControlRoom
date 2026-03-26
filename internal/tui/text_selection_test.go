package tui

import (
	"testing"
)

func TestTextSelection_normalized(t *testing.T) {
	// Forward selection
	sel := textSelection{anchorRow: 1, anchorCol: 5, currentRow: 3, currentCol: 10}
	sr, sc, er, ec := sel.normalized()
	if sr != 1 || sc != 5 || er != 3 || ec != 10 {
		t.Fatalf("forward: got (%d,%d)-(%d,%d), want (1,5)-(3,10)", sr, sc, er, ec)
	}

	// Backward selection
	sel = textSelection{anchorRow: 3, anchorCol: 10, currentRow: 1, currentCol: 5}
	sr, sc, er, ec = sel.normalized()
	if sr != 1 || sc != 5 || er != 3 || ec != 10 {
		t.Fatalf("backward: got (%d,%d)-(%d,%d), want (1,5)-(3,10)", sr, sc, er, ec)
	}

	// Same row, backward
	sel = textSelection{anchorRow: 2, anchorCol: 15, currentRow: 2, currentCol: 3}
	sr, sc, er, ec = sel.normalized()
	if sr != 2 || sc != 3 || er != 2 || ec != 15 {
		t.Fatalf("same-row backward: got (%d,%d)-(%d,%d), want (2,3)-(2,15)", sr, sc, er, ec)
	}
}

func TestTextSelection_hasRange(t *testing.T) {
	sel := textSelection{anchorRow: 1, anchorCol: 5, currentRow: 1, currentCol: 5}
	if sel.hasRange() {
		t.Fatal("same point should not have range")
	}
	sel.currentCol = 6
	if !sel.hasRange() {
		t.Fatal("different col should have range")
	}
}

func TestTextSelection_extractText(t *testing.T) {
	content := "Hello, World!\nSecond line here\nThird line"

	// Single line selection
	sel := textSelection{anchorRow: 0, anchorCol: 7, currentRow: 0, currentCol: 12}
	got := sel.extractText(content)
	if got != "World" {
		t.Fatalf("single line: got %q, want %q", got, "World")
	}

	// Multi-line selection
	sel = textSelection{anchorRow: 0, anchorCol: 7, currentRow: 1, currentCol: 6}
	got = sel.extractText(content)
	if got != "World!\nSecond" {
		t.Fatalf("multi line: got %q, want %q", got, "World!\nSecond")
	}

	// Full line
	sel = textSelection{anchorRow: 2, anchorCol: 0, currentRow: 2, currentCol: 10}
	got = sel.extractText(content)
	if got != "Third line" {
		t.Fatalf("full line: got %q, want %q", got, "Third line")
	}
}

func TestTextSelection_extractText_withANSI(t *testing.T) {
	// Content with ANSI styling
	content := "\x1b[1mBold text\x1b[0m and plain\nLine two"
	sel := textSelection{anchorRow: 0, anchorCol: 0, currentRow: 0, currentCol: 9}
	got := sel.extractText(content)
	if got != "Bold text" {
		t.Fatalf("ansi: got %q, want %q", got, "Bold text")
	}
}

func TestOverlaySelectionHighlight(t *testing.T) {
	input := "Line zero\nLine one\nLine two"
	sel := textSelection{anchorRow: 1, anchorCol: 5, currentRow: 1, currentCol: 8}

	result := overlaySelectionHighlight(input, sel, 0)

	// The selection should highlight "one" with the selection style.
	if result == input {
		t.Fatal("overlay should have modified the output")
	}
	// Check that the highlight escape is present (bright yellow bg).
	if !containsSubstring(result, selectionHighlightStart) {
		t.Fatalf("expected highlight escape in output, got: %q", result)
	}
}

func TestCleanCopiedText(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "strip leading space",
			in:   " Hello\n World",
			want: "Hello World",
		},
		{
			name: "join soft-wrapped lines",
			// Lines without trailing spaces are full-width (soft-wrapped).
			in: "Hello\nWorld",
			want: "Hello World",
		},
		{
			name: "keep line break on short lines",
			// Trailing space signals the line was shorter than viewport width.
			in:   "Hello   \nWorld",
			want: "Hello\nWorld",
		},
		{
			name: "preserve blank line separators",
			in:   " First paragraph\n\n Second paragraph",
			want: "First paragraph\n\nSecond paragraph",
		},
		{
			name: "strip trailing whitespace",
			in:   " Hello   \n World   ",
			want: "Hello\nWorld",
		},
		{
			name: "full pipeline",
			// Two paragraphs separated by blank line. First paragraph has
			// a soft-wrapped line (no trailing space).
			in:   " This is a long line that was\n soft-wrapped by the viewport\n\n " + "Second paragraph here.   ",
			want: "This is a long line that was soft-wrapped by the viewport\n\nSecond paragraph here.",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cleanCopiedText(tt.in)
			if got != tt.want {
				t.Errorf("cleanCopiedText(%q)\n got: %q\nwant: %q", tt.in, got, tt.want)
			}
		})
	}
}

func containsSubstring(s, sub string) bool {
	return len(s) >= len(sub) && findSubstring(s, sub)
}

func findSubstring(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
