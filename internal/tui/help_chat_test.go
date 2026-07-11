package tui

import (
	"context"
	"strings"
	"testing"

	bossui "lcroom/internal/boss"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestHelpChatLongTranscriptStaysInsideScrollableOverlay(t *testing.T) {
	t.Parallel()

	const (
		bodyWidth  = 240
		bodyHeight = 52
	)
	geom := helpChatOverlayGeometryForSize(bodyWidth, bodyHeight)
	help := bossui.NewEmbeddedHelp(context.Background(), nil)
	updated, _ := help.Update(tea.WindowSizeMsg{Width: geom.chatWidth, Height: geom.chatHeight})
	help = normalizeBossModel(updated)

	paragraphs := make([]string, 0, 48)
	for i := 0; i < 48; i++ {
		paragraphs = append(paragraphs, "Help transcript text with enough words to exercise wrapping while staying owned by the transcript viewport.")
	}
	paragraphs[0] = "START OF HELP TRANSCRIPT"
	paragraphs[len(paragraphs)-1] = "END OF HELP TRANSCRIPT"
	longQuestion := strings.Join(paragraphs, " ")
	updated, _ = help.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(longQuestion)})
	help = normalizeBossModel(updated)
	updated, _ = help.Update(tea.KeyMsg{Type: tea.KeyEnter})
	help = normalizeBossModel(updated)

	m := Model{helpChatModel: help}
	base := fitPaneContent("", bodyWidth, bodyHeight)
	bottomView := m.renderHelpChatOverlay(base, bodyWidth, bodyHeight)
	assertHelpChatOverlayBorder(t, bottomView, bodyHeight, geom)
	if !strings.Contains(ansi.Strip(bottomView), "END OF HELP TRANSCRIPT") {
		t.Fatalf("help chat should initially show the bottom of a long transcript")
	}

	updated, _ = help.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	m.helpChatModel = normalizeBossModel(updated)
	pageUpView := m.renderHelpChatOverlay(base, bodyWidth, bodyHeight)
	assertHelpChatOverlayBorder(t, pageUpView, bodyHeight, geom)
	if pageUpView == bottomView {
		t.Fatalf("Page Up should change the visible help transcript")
	}
}

func assertHelpChatOverlayBorder(t *testing.T, rendered string, bodyHeight int, geom helpChatOverlayGeometry) {
	t.Helper()

	lines := strings.Split(rendered, "\n")
	if got := len(lines); got != bodyHeight {
		t.Fatalf("rendered body height = %d, want %d", got, bodyHeight)
	}
	top := lines[geom.top]
	bottom := lines[geom.top+geom.panelHeight-1]
	if got := ansi.Strip(ansi.Cut(top, geom.left, geom.left+1)); got != "╭" {
		t.Fatalf("help chat top-left border = %q, want ╭", got)
	}
	if got := ansi.Strip(ansi.Cut(top, geom.left+geom.panelWidth-1, geom.left+geom.panelWidth)); got != "╮" {
		t.Fatalf("help chat top-right border = %q, want ╮", got)
	}
	if got := ansi.Strip(ansi.Cut(bottom, geom.left, geom.left+1)); got != "╰" {
		t.Fatalf("help chat bottom-left border at configured height = %q, want ╰", got)
	}
	if got := ansi.Strip(ansi.Cut(bottom, geom.left+geom.panelWidth-1, geom.left+geom.panelWidth)); got != "╯" {
		t.Fatalf("help chat bottom-right border at configured size = %q, want ╯", got)
	}
	for row := geom.top + 1; row < geom.top+geom.panelHeight-1; row++ {
		if got := ansi.Strip(ansi.Cut(lines[row], geom.left, geom.left+1)); got != "│" {
			t.Fatalf("help chat left border row %d = %q, want │", row-geom.top, got)
		}
		if got := ansi.Strip(ansi.Cut(lines[row], geom.left+geom.panelWidth-1, geom.left+geom.panelWidth)); got != "│" {
			t.Fatalf("help chat right border row %d = %q, want │", row-geom.top, got)
		}
	}
}
