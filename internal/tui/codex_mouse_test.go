package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
)

func TestVisibleCodexMouseWheelScrollsTranscriptOnly(t *testing.T) {
	lines := make([]string, 0, 20)
	for i := 0; i < 20; i++ {
		lines = append(lines, fmt.Sprintf("line %02d", i))
	}
	transcript := viewport.New(80, 5)
	transcript.SetContent(strings.Join(lines, "\n"))
	transcript.GotoBottom()
	before := transcript.YOffset

	m := Model{
		codexVisibleProject: "/tmp/a",
		codexHiddenProject:  "/tmp/a",
		codexViewport:       transcript,
		width:               100,
		height:              24,
	}

	updated, cmd := m.Update(tea.MouseMsg{
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonWheelUp,
		X:      20,
		Y:      8,
	})
	got := normalizeUpdateModel(updated)
	if cmd != nil {
		t.Fatalf("wheel scroll should not queue a command")
	}
	if got.codexVisibleProject != "/tmp/a" {
		t.Fatalf("codexVisibleProject = %q, want /tmp/a", got.codexVisibleProject)
	}
	if got.codexViewport.YOffset >= before {
		t.Fatalf("wheel up should move transcript upward, before=%d after=%d", before, got.codexViewport.YOffset)
	}

	afterVertical := got.codexViewport.YOffset
	updated, cmd = got.Update(tea.MouseMsg{
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonWheelRight,
		X:      20,
		Y:      8,
	})
	got = normalizeUpdateModel(updated)
	if cmd != nil {
		t.Fatalf("horizontal wheel should not queue a command")
	}
	if got.codexVisibleProject != "/tmp/a" {
		t.Fatalf("codexVisibleProject after horizontal wheel = %q, want /tmp/a", got.codexVisibleProject)
	}
	if got.codexViewport.YOffset != afterVertical {
		t.Fatalf("horizontal wheel should stay in the embedded session without moving transcript offset: before=%d after=%d", afterVertical, got.codexViewport.YOffset)
	}
}
