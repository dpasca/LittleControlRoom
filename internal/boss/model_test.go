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
	if !strings.HasSuffix(strings.TrimRight(lines[0], " "), "╮") {
		t.Fatalf("top row should end with the right-hand frame, got %q", lines[0])
	}
	if layout.topHeight > 0 && !strings.HasPrefix(lines[layout.topHeight-1], "╰") {
		t.Fatalf("top row should keep its bottom border visible, got %q", lines[layout.topHeight-1])
	}
	bottomStart := layout.topHeight + layout.middleGapHeight
	if layout.bottomHeight > 0 && bottomStart < len(lines) && !strings.HasSuffix(strings.TrimRight(lines[bottomStart], " "), "╮") {
		t.Fatalf("bottom row should end with the right-hand frame, got %q", lines[bottomStart])
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

func TestEmbeddedModelHonorsShortHostHeight(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		width  int
		height int
	}{
		{name: "wide", width: 180, height: 16},
		{name: "wide-very-short", width: 180, height: 11},
		{name: "narrow", width: 70, height: 16},
		{name: "narrow-very-short", width: 70, height: 11},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			m := NewEmbedded(context.Background(), nil)
			m.width = tc.width
			m.height = tc.height
			m.stateLoaded = true
			m.syncLayout(true)

			rendered := ansi.Strip(m.View())
			lines := strings.Split(rendered, "\n")
			if len(lines) != m.height {
				t.Fatalf("embedded boss view line count = %d, want %d:\n%s", len(lines), m.height, rendered)
			}
			if !strings.Contains(lines[len(lines)-1], "╰") {
				t.Fatalf("short embedded boss view should keep the bottom panel border visible:\n%s", rendered)
			}
			layout := m.layout()
			if layout.topHeight+layout.middleGapHeight+layout.bottomHeight > layout.height {
				t.Fatalf("short embedded layout heights = top %d + gap %d + bottom %d, want <= %d", layout.topHeight, layout.middleGapHeight, layout.bottomHeight, layout.height)
			}
		})
	}
}

func TestEmbeddedModelKeepsTopRowContentSizedOnTallHosts(t *testing.T) {
	t.Parallel()

	previousTopHeight := 0
	for _, height := range []int{30, 44, 61} {
		m := NewEmbedded(context.Background(), nil)
		m.width = 180
		m.height = height
		m.stateLoaded = true
		m.snapshot = StateSnapshot{
			TotalProjects:          148,
			ActiveProjects:         2,
			PossiblyStuckProjects:  2,
			DirtyProjects:          33,
			PendingClassifications: 2,
		}
		m.status = "Boss chat via gpt-5.4-mini"

		layout := m.layout()
		renderedHeight := layout.topHeight + layout.middleGapHeight + layout.bottomHeight
		if renderedHeight >= layout.height {
			t.Fatalf("content-sized layout should leave terminal slack below the panels on tall hosts, got rendered height %d terminal height %d", renderedHeight, layout.height)
		}
		if previousTopHeight > 0 && layout.topHeight != previousTopHeight {
			t.Fatalf("height %d top panel height = %d, want content-sized top row to stay stable at %d", height, layout.topHeight, previousTopHeight)
		}
		if previousTopHeight > 0 && layout.bottomHeight <= 0 {
			t.Fatalf("height %d bottom panels should remain visible, got bottom height %d", height, layout.bottomHeight)
		}
		if height > 30 && layout.middleGapHeight != 1 {
			t.Fatalf("height %d should use one separator row between panel bands, got gap %d", height, layout.middleGapHeight)
		}
		previousTopHeight = layout.topHeight
	}
}

func TestEmbeddedModelExpandsTopRowForLongerConversation(t *testing.T) {
	t.Parallel()

	m := NewEmbedded(context.Background(), nil)
	m.width = 180
	m.height = 52
	baseTopHeight := m.layout().topHeight

	m.messages = append(m.messages,
		ChatMessage{Role: "user", Content: "Give me a compact risk summary for the stuck projects, the active projects, and which dirty repos are probably safe to ignore for now."},
		ChatMessage{Role: "assistant", Content: "The stuck work looks concentrated in a few repos. I would review the highest-attention items first, then separate harmless dirty working trees from projects that are blocking merges or release work."},
		ChatMessage{Role: "user", Content: "Also call out what can safely wait until tomorrow and what needs action before I context switch away from this machine."},
	)

	layout := m.layout()
	if layout.topHeight <= baseTopHeight {
		t.Fatalf("longer conversation should grow the top row, got base %d current %d", baseTopHeight, layout.topHeight)
	}
	if layout.bottomHeight < 7 {
		t.Fatalf("longer conversation should still leave room for lower panels, got bottom height %d", layout.bottomHeight)
	}
	if layout.topHeight+layout.middleGapHeight+layout.bottomHeight > layout.height {
		t.Fatalf("longer conversation layout heights = top %d + gap %d + bottom %d, want <= %d", layout.topHeight, layout.middleGapHeight, layout.bottomHeight, layout.height)
	}
}

func TestEmbeddedModelFlowsMediumWidthPanelsFromTop(t *testing.T) {
	t.Parallel()

	m := NewEmbedded(context.Background(), nil)
	m.width = 100
	m.height = 44
	m.stateLoaded = true
	m.snapshot = StateSnapshot{
		TotalProjects:         148,
		ActiveProjects:        1,
		PossiblyStuckProjects: 2,
		DirtyProjects:         32,
		HotProjects: []ProjectBrief{
			{Name: "LittleControlRoom", Status: model.StatusActive, AttentionScore: 140, RepoBranch: "master", RepoDirty: true, RepoAheadCount: 10},
			{Name: "social_manager", Status: model.StatusPossiblyStuck, AttentionScore: 92, RepoBranch: "master", RepoDirty: true},
			{Name: "crypto", Status: model.StatusIdle, AttentionScore: 72, RepoBranch: "feature/tui-trader-mvp", RepoDirty: true, RepoAheadCount: 3},
			{Name: "okmain", Status: model.StatusIdle, AttentionScore: 70, RepoBranch: "master_mobnext", RepoAheadCount: 3},
		},
	}
	m.status = "Boss chat via gpt-5.4-mini"
	m.syncLayout(true)

	layout := m.layout()
	if layout.middleGapHeight != 1 {
		t.Fatalf("medium-width layout should use one separator row, got %d", layout.middleGapHeight)
	}
	if renderedHeight := layout.topHeight + layout.middleGapHeight + layout.bottomHeight; renderedHeight >= layout.height {
		t.Fatalf("medium-width panels should flow from the top and leave slack below, got rendered height %d terminal height %d", renderedHeight, layout.height)
	}

	rendered := ansi.Strip(m.View())
	lines := strings.Split(rendered, "\n")
	bottomBorderLine := layout.topHeight + layout.middleGapHeight + layout.bottomHeight - 1
	if bottomBorderLine >= len(lines) {
		t.Fatalf("bottom border row %d outside rendered view with %d lines:\n%s", bottomBorderLine, len(lines), rendered)
	}
	if !strings.HasPrefix(lines[bottomBorderLine], "╰") {
		t.Fatalf("bottom panels should keep their bottom border visible, got %q", lines[bottomBorderLine])
	}
	for i := bottomBorderLine + 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) != "" {
			t.Fatalf("rows after content-sized panels should be blank, line %d = %q", i, lines[i])
		}
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

func TestPanelUsesFullAllocatedWidthAndKeepsBottomBorder(t *testing.T) {
	t.Parallel()

	m := NewEmbedded(context.Background(), nil)
	rendered := ansi.Strip(m.renderPanel("Attention", "alpha beta gamma delta epsilon zeta eta theta", 33, 8))
	lines := strings.Split(rendered, "\n")
	if len(lines) != 8 {
		t.Fatalf("panel rendered %d lines, want 8:\n%s", len(lines), rendered)
	}
	if got := ansi.StringWidth(strings.TrimRight(lines[0], " ")); got != 33 {
		t.Fatalf("panel visible width = %d, want 33: %q", got, lines[0])
	}
	if !strings.HasPrefix(lines[len(lines)-1], "╰") {
		t.Fatalf("panel should keep bottom border visible:\n%s", rendered)
	}
}

func renderedLineCount(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(ansi.Strip(s), "\n") + 1
}
