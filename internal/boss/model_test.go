package boss

import (
	"context"
	"strings"
	"testing"

	"lcroom/internal/model"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestModelViewRendersBossPanels(t *testing.T) {
	t.Parallel()

	m := New(context.Background(), nil)
	m.width = 100
	m.height = 30
	m.stateLoaded = true
	m.snapshot = StateSnapshot{
		TotalProjects:  1,
		ActiveProjects: 1,
		HotProjects: []ProjectBrief{{
			Name:           "Alpha",
			Status:         model.StatusActive,
			AttentionScore: 12,
		}},
	}
	m.syncLayout(true)

	view := m.View()
	for _, want := range []string{"Boss Chat", "Situation", "Attention", "Notes", "Alpha"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q:\n%s", want, view)
		}
	}
	for _, legacy := range []string{"Little Room", "On My Desk", "Notebook"} {
		if strings.Contains(view, legacy) {
			t.Fatalf("view still contains themed panel %q:\n%s", legacy, view)
		}
	}
	if strings.Contains(ansi.Strip(view), "Ask what needs attention") {
		t.Fatalf("view should not render the old input placeholder:\n%s", view)
	}
}

func TestModelInputAcceptsTypingImmediately(t *testing.T) {
	t.Parallel()

	m := New(context.Background(), nil)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h', 'i'}})
	got := updated.(Model)
	if got.input.Value() != "hi" {
		t.Fatalf("input value = %q, want typed text", got.input.Value())
	}
}

func TestModelQTypesIntoInput(t *testing.T) {
	t.Parallel()

	m := New(context.Background(), nil)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	got := updated.(Model)
	if got.input.Value() != "q" {
		t.Fatalf("input value = %q, want q to type into the chat input", got.input.Value())
	}
}

func TestEmbeddedModelRendersBodyForHostShell(t *testing.T) {
	t.Parallel()

	m := NewEmbedded(context.Background(), nil)
	m.width = 160
	m.height = 42
	m.stateLoaded = true
	m.syncLayout(true)

	rendered := ansi.Strip(m.View())
	lines := strings.Split(rendered, "\n")
	if len(lines) == 0 || !strings.HasPrefix(lines[0], "╭") {
		t.Fatalf("embedded boss body should start directly with frames for the host shell:\n%s", rendered)
	}
	if lineCount := len(lines); lineCount > m.height {
		layout := m.layout()
		t.Fatalf("embedded boss view rendered %d lines, want at most %d; layout=%+v chat=%d situation=%d attention=%d notes=%d",
			lineCount,
			m.height,
			layout,
			renderedLineCount(m.renderChat(layout)),
			renderedLineCount(m.renderSituation(layout.sideWidth, layout.topHeight)),
			renderedLineCount(m.renderPanel("Attention", AttentionText(m.snapshot, m.now()), layout.deskWidth, layout.bottomHeight)),
			renderedLineCount(m.renderPanel("Notes", NotesText(m.snapshot), layout.notebookWidth, layout.bottomHeight)))
	}
	layout := m.layout()
	if !strings.HasSuffix(lines[0], "╮") {
		t.Fatalf("top row should end with the right-hand frame, got %q", lines[0])
	}
	if layout.topHeight < len(lines) && !strings.HasSuffix(lines[layout.topHeight], "╮") {
		t.Fatalf("bottom row should end with the right-hand frame, got %q", lines[layout.topHeight])
	}
	for _, line := range strings.Split(m.View(), "\n") {
		if got := ansi.StringWidth(ansi.Strip(line)); got > m.width {
			t.Fatalf("embedded boss line width = %d, want <= %d: %q", got, m.width, ansi.Strip(line))
		}
	}
	if strings.Contains(m.View(), "\x1b[48;5;0m") {
		t.Fatalf("boss panels should not use ANSI palette black because themed palettes can render it gray")
	}
}

func TestModelTranscriptRendersMarkdown(t *testing.T) {
	t.Parallel()

	m := New(context.Background(), nil)
	m.messages = []ChatMessage{{
		Role:    "assistant",
		Content: "## Plan\n- **Ship** the `boss` chat polish",
	}, {
		Role:    "user",
		Content: "Can we use `markdown`?",
	}}

	rendered := ansi.Strip(m.renderTranscript(72))
	for _, want := range []string{"Plan", "Ship", "boss", "You>", "markdown"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered transcript missing %q:\n%s", want, rendered)
		}
	}
	for _, marker := range []string{"Assistant:", "You:", "##", "**", "`"} {
		if strings.Contains(rendered, marker) {
			t.Fatalf("rendered transcript still contains markdown marker %q:\n%s", marker, rendered)
		}
	}
}

func renderedLineCount(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(ansi.Strip(s), "\n") + 1
}
