package pasteplaceholder

import (
	"strings"
	"testing"
)

func TestStrip(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "single line placeholder", in: "[1 line pasted]", want: ""},
		{name: "multi line placeholder", in: "[12 lines pasted]", want: ""},
		{name: "keeps text around placeholder", in: "[2 lines pasted] summarize this", want: "summarize this"},
		{name: "leaves other bracketed text", in: "[2 lines kept] summarize this", want: "[2 lines kept] summarize this"},
		{name: "rejects plural one line", in: "[1 lines pasted] summarize this", want: "[1 lines pasted] summarize this"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := strings.Join(strings.Fields(Strip(tt.in)), " ")
			if got != tt.want {
				t.Fatalf("Strip(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
