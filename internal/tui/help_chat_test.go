package tui

import (
	"context"
	"strings"
	"testing"

	bossui "lcroom/internal/boss"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

func TestUnconfiguredHelpChatOpensSetupPrompt(t *testing.T) {
	t.Parallel()

	m := Model{width: 100, height: 24}
	updated, cmd := m.openHelpChatModeOrSetupPrompt()
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("unconfigured Chat should not start async work")
	}
	if got.helpChatMode {
		t.Fatalf("unconfigured Chat should stay closed")
	}
	if got.bossSetupPrompt == nil {
		t.Fatalf("unconfigured Chat should open its setup prompt")
	}
	if !strings.Contains(got.bossSetupPrompt.Reason, "chat backend") {
		t.Fatalf("setup prompt reason = %q, want provider-neutral chat guidance", got.bossSetupPrompt.Reason)
	}
	rendered := ansi.Strip(got.View())
	for _, want := range []string{"Chat Setup", "Open setup", "Cancel"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("Chat setup prompt missing %q: %q", want, rendered)
		}
	}
}

func TestHelpChatSetupPromptOpensFocusedSetup(t *testing.T) {
	t.Parallel()

	m := Model{
		bossSetupPrompt: &bossSetupPromptState{Selected: bossSetupPromptOpenSetup},
		width:           100,
		height:          24,
	}
	updated, cmd := m.updateBossSetupPromptMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("opening setup from the Chat prompt should return a focus command")
	}
	if got.bossSetupPrompt != nil || !got.settingsMode {
		t.Fatalf("Chat setup prompt should close into settings")
	}
	if got.settingsSelected != settingsFieldBossChatBackend {
		t.Fatalf("settings selected = %d, want Chat backend field", got.settingsSelected)
	}
}

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
		t.Fatalf("chat should initially show the bottom of a long transcript")
	}

	updated, _ = help.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	m.helpChatModel = normalizeBossModel(updated)
	pageUpView := m.renderHelpChatOverlay(base, bodyWidth, bodyHeight)
	assertHelpChatOverlayBorder(t, pageUpView, bodyHeight, geom)
	if pageUpView == bottomView {
		t.Fatalf("Page Up should change the visible help transcript")
	}
}

func TestHelpChatFooterOffersStopAndSteerWhileResponding(t *testing.T) {
	t.Parallel()

	help := bossui.NewEmbeddedHelp(context.Background(), nil)
	updated, _ := help.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("mistaken request")})
	help = normalizeBossModel(updated)
	updated, _ = help.Update(tea.KeyMsg{Type: tea.KeyEnter})
	help = normalizeBossModel(updated)

	m := Model{helpChatModel: help}
	footer := ansi.Strip(m.renderHelpChatFooter(100))
	for _, want := range []string{"Enter steer", "Ctrl+C stop", "Esc hide"} {
		if !strings.Contains(footer, want) {
			t.Fatalf("responding Chat footer missing %q: %q", want, footer)
		}
	}

	updated, _ = help.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	help = normalizeBossModel(updated)
}

func TestHelpChatOverlayPaintsEveryInteriorCell(t *testing.T) {
	prevProfile := lipgloss.ColorProfile()
	prevDarkBackground := lipgloss.HasDarkBackground()
	lipgloss.SetColorProfile(termenv.ANSI256)
	lipgloss.SetHasDarkBackground(true)
	defer func() {
		lipgloss.SetColorProfile(prevProfile)
		lipgloss.SetHasDarkBackground(prevDarkBackground)
	}()

	const (
		bodyWidth  = 112
		bodyHeight = 38
	)
	geom := helpChatOverlayGeometryForSize(bodyWidth, bodyHeight)
	help := bossui.NewEmbeddedHelp(context.Background(), nil)
	updated, _ := help.Update(tea.WindowSizeMsg{Width: geom.chatWidth, Height: geom.chatHeight})
	help = normalizeBossModel(updated)
	updated, _ = help.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("A **styled** question with enough text to exercise the transcript background.")})
	help = normalizeBossModel(updated)
	updated, _ = help.Update(tea.KeyMsg{Type: tea.KeyEnter})
	help = normalizeBossModel(updated)

	m := Model{helpChatModel: help}
	rendered := m.renderHelpChatOverlay(fitPaneContent("", bodyWidth, bodyHeight), bodyWidth, bodyHeight)
	lines := parseTerminalANSI(rendered)
	if len(lines) != bodyHeight {
		t.Fatalf("parsed help overlay height = %d, want %d", len(lines), bodyHeight)
	}
	for row := geom.top + 1; row < geom.top+geom.panelHeight-1; row++ {
		assertTerminalLineHasExplicitBackground(t, lines[row], row, geom.left+1, geom.left+geom.panelWidth-1)
	}
}

func assertTerminalLineHasExplicitBackground(t *testing.T, line terminalLine, row, left, right int) {
	t.Helper()

	column := 0
	covered := left
	for _, run := range line {
		runWidth := ansi.StringWidth(run.text)
		runLeft := column
		runRight := column + runWidth
		column = runRight
		if runRight <= left || runLeft >= right {
			continue
		}
		covered = max(covered, min(runRight, right))
		hasExplicitBackground := run.style.hasBG || (run.style.reverse && run.style.hasFG)
		if !hasExplicitBackground {
			t.Fatalf("help overlay row %d columns %d..%d use the terminal default background in %q; runs=%#v", row, max(runLeft, left), min(runRight, right), run.text, line)
		}
	}
	if covered < right {
		t.Fatalf("help overlay row %d only renders through column %d, want interior through %d", row, covered, right)
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
		t.Fatalf("chat top-left border = %q, want ╭", got)
	}
	if got := ansi.Strip(ansi.Cut(top, geom.left+geom.panelWidth-1, geom.left+geom.panelWidth)); got != "╮" {
		t.Fatalf("chat top-right border = %q, want ╮", got)
	}
	if got := ansi.Strip(ansi.Cut(bottom, geom.left, geom.left+1)); got != "╰" {
		t.Fatalf("chat bottom-left border at configured height = %q, want ╰", got)
	}
	if got := ansi.Strip(ansi.Cut(bottom, geom.left+geom.panelWidth-1, geom.left+geom.panelWidth)); got != "╯" {
		t.Fatalf("chat bottom-right border at configured size = %q, want ╯", got)
	}
	for row := geom.top + 1; row < geom.top+geom.panelHeight-1; row++ {
		if got := ansi.Strip(ansi.Cut(lines[row], geom.left, geom.left+1)); got != "│" {
			t.Fatalf("chat left border row %d = %q, want │", row-geom.top, got)
		}
		if got := ansi.Strip(ansi.Cut(lines[row], geom.left+geom.panelWidth-1, geom.left+geom.panelWidth)); got != "│" {
			t.Fatalf("chat right border row %d = %q, want │", row-geom.top, got)
		}
	}
}
