package viewportnav

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/viewport"
)

func TestPageStepUsesEightyPercentWithOverlap(t *testing.T) {
	t.Parallel()

	tests := []struct {
		height int
		want   int
	}{
		{height: -1, want: 0},
		{height: 0, want: 0},
		{height: 1, want: 1},
		{height: 2, want: 1},
		{height: 3, want: 2},
		{height: 4, want: 3},
		{height: 5, want: 4},
		{height: 10, want: 8},
		{height: 20, want: 16},
	}
	for _, tt := range tests {
		if got := PageStep(tt.height); got != tt.want {
			t.Fatalf("PageStep(%d) = %d, want %d", tt.height, got, tt.want)
		}
	}
}

func TestPageUpDownScrollByPageStep(t *testing.T) {
	t.Parallel()

	var lines []string
	for i := 0; i < 40; i++ {
		lines = append(lines, fmt.Sprintf("line %02d", i))
	}
	vp := viewport.New(40, 10)
	vp.SetContent(strings.Join(lines, "\n"))

	PageDown(&vp)
	if got, want := vp.YOffset, 8; got != want {
		t.Fatalf("PageDown offset = %d, want %d", got, want)
	}

	PageUp(&vp)
	if got := vp.YOffset; got != 0 {
		t.Fatalf("PageUp offset = %d, want 0", got)
	}
}
