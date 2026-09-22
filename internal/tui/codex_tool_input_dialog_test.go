package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"lcroom/internal/codexapp"
)

func testToolInputQuestionRequest() *codexapp.ToolInputRequest {
	return &codexapp.ToolInputRequest{
		ID: "req_questions",
		Questions: []codexapp.ToolInputQuestion{{
			ID:       "question-1",
			Header:   "Next milestone",
			Question: "Which path next, now that both are unblocked?",
			IsOther:  true,
			Options: []codexapp.ToolInputOption{
				{
					Label:       "Transport and MCP adapter",
					Description: "Loopback endpoint plus a Python MCP adapter, advertising menus only and making no cockpit claim.",
				},
				{
					Label:       "HUD and avionics content",
					Description: "Plan numbering order, so the cockpit becomes readable before anything else lands.",
				},
			},
		}},
	}
}

func newToolInputDialogModel(t *testing.T, request *codexapp.ToolInputRequest) (Model, *fakeCodexSession) {
	t.Helper()
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Provider:         codexapp.ProviderClaudeCode,
			Started:          true,
			Busy:             true,
			Phase:            codexapp.SessionPhaseRunning,
			ProjectPath:      "/tmp/demo",
			Status:           "Claude Code is waiting for your answer",
			PendingToolInput: request,
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Provider:    codexapp.ProviderClaudeCode,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}
	input := newCodexTextarea()
	input.Focus()
	return Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          input,
		codexToolAnswers:    make(map[string]codexToolAnswerState),
		codexViewport:       viewport.New(0, 0),
		width:               120,
		height:              32,
	}, session
}

func TestToolInputDialogDocksBelowTranscript(t *testing.T) {
	m, _ := newToolInputDialogModel(t, testToolInputQuestionRequest())
	snapshot, ok := m.currentCodexSnapshot()
	if !ok {
		t.Fatal("snapshot should be available")
	}

	lower := ansi.Strip(strings.Join(m.codexLowerBlocks(snapshot, m.width), "\n"))
	if strings.Contains(lower, "Structured input:") {
		t.Fatalf("lower blocks should no longer render the plain structured input block: %q", lower)
	}
	if !strings.Contains(lower, "╭") {
		t.Fatalf("the question should render as a docked dialog panel: %q", lower)
	}

	rendered := ansi.Strip(m.View())
	for _, want := range []string{
		"Claude Code needs your answer",
		"Next milestone",
		"Which path next, now that both are unblocked?",
		"> 1 Transport and MCP adapter",
		"advertising menus only",
		"  2 HUD and avionics content",
		"3 Type a different answer",
		"Enter  choose",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("structured input dialog missing %q:\n%s", want, rendered)
		}
	}
}

func TestToolInputDialogArrowSelectionChoosesHighlightedOption(t *testing.T) {
	m, session := newToolInputDialogModel(t, testToolInputQuestionRequest())

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyDown})
	if cmd == nil {
		t.Fatal("moving the highlight should keep the composer focus command")
	}
	m = updated.(Model)
	if got := m.codexToolAnswers["/tmp/demo"].OptionIndex; got != 1 {
		t.Fatalf("highlighted row = %d, want 1", got)
	}
	if rendered := ansi.Strip(m.View()); !strings.Contains(rendered, "> 2 HUD and avionics content") {
		t.Fatalf("second option should be highlighted:\n%s", rendered)
	}

	updated, cmd = m.updateCodexMode(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter should submit the highlighted option")
	}
	m = updated.(Model)
	if action := toolInputSubmitResult(t, cmd()); action.err != nil {
		t.Fatalf("submitting the highlighted option failed: %v", action.err)
	}
	if len(session.toolAnswers) != 1 {
		t.Fatalf("tool answers = %#v", session.toolAnswers)
	}
	if got := session.toolAnswers[0]["question-1"]; len(got) != 1 || got[0] != "HUD and avionics content" {
		t.Fatalf("submitted answer = %#v, want the highlighted option", got)
	}
}

func TestToolInputDialogTypingSwitchesToFreeTextRow(t *testing.T) {
	m, session := newToolInputDialogModel(t, testToolInputQuestionRequest())

	updated, _ := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	m = updated.(Model)
	if got := m.codexInput.Value(); got != "n" {
		t.Fatalf("composer value = %q, want the typed rune", got)
	}
	if got := m.codexToolAnswers["/tmp/demo"].OptionIndex; got != 2 {
		t.Fatalf("highlighted row = %d, want the free-text row", got)
	}
	if rendered := ansi.Strip(m.View()); !strings.Contains(rendered, "Your own answer") {
		t.Fatalf("free-text row should reveal the composer:\n%s", rendered)
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter should send the typed answer")
	}
	_ = updated.(Model)
	if action := toolInputSubmitResult(t, cmd()); action.err != nil {
		t.Fatalf("typed answer submit failed: %v", action.err)
	}
	if got := session.toolAnswers[0]["question-1"]; len(got) != 1 || got[0] != "n" {
		t.Fatalf("submitted answer = %#v, want the typed text", got)
	}
}

func TestToolInputDialogEscapeReturnsToOptionsBeforeHidingPane(t *testing.T) {
	m, _ := newToolInputDialogModel(t, testToolInputQuestionRequest())

	updated, _ := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("3")})
	m = updated.(Model)
	if got := m.codexToolAnswers["/tmp/demo"].OptionIndex; got != 2 {
		t.Fatalf("highlighted row = %d, want the free-text row", got)
	}

	updated, _ = m.updateCodexMode(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	if got := m.codexToolAnswers["/tmp/demo"].OptionIndex; got != 0 {
		t.Fatalf("Esc should return to the option list, highlighted row = %d", got)
	}
	if m.codexVisibleProject != "/tmp/demo" {
		t.Fatal("Esc should not hide the pane while the free-text row is active")
	}

	updated, _ = m.updateCodexMode(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(Model)
	if m.codexVisibleProject != "" {
		t.Fatalf("Esc on the option list should hide the pane, visible project = %q", m.codexVisibleProject)
	}
}

func TestToolInputDialogKeepsSelectedQuestionAfterTab(t *testing.T) {
	request := &codexapp.ToolInputRequest{
		ID: "req_two",
		Questions: []codexapp.ToolInputQuestion{
			{ID: "first", Question: "First question?", IsOther: true},
			{ID: "second", Question: "Second question?", IsOther: true},
		},
	}
	m, _ := newToolInputDialogModel(t, request)

	updated, _ := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyTab})
	m = updated.(Model)
	if got := m.codexToolAnswers["/tmp/demo"].QuestionIndex; got != 1 {
		t.Fatalf("question index = %d, want 1", got)
	}
	rendered := ansi.Strip(m.View())
	for _, want := range []string{"Question 2 of 2", "Second question?"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("dialog missing %q after Tab:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, "First question?") {
		t.Fatalf("Tab should move away from the first question:\n%s", rendered)
	}
}

func TestToolInputDialogIgnoresRepeatSubmitWhileSending(t *testing.T) {
	m, session := newToolInputDialogModel(t, testToolInputQuestionRequest())

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("1")})
	if cmd == nil {
		t.Fatal("choosing an option should submit the answer")
	}
	m = updated.(Model)
	if m.codexToolInputSubmitting != "req_questions" {
		t.Fatalf("submitting request = %q, want the pending request", m.codexToolInputSubmitting)
	}
	if rendered := ansi.Strip(m.View()); !strings.Contains(rendered, "Sending your answer to Claude Code") {
		t.Fatalf("dialog should report the in-flight answer:\n%s", rendered)
	}

	if _, repeat := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyEnter}); repeat != nil {
		t.Fatal("a repeat Enter should be ignored while the answer is in flight")
	}
	if _, repeat := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("2")}); repeat != nil {
		t.Fatal("a repeat option choice should be ignored while the answer is in flight")
	}

	submitted, ok := cmd().(codexToolInputSubmittedMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want codexToolInputSubmittedMsg", submitted)
	}
	if len(session.toolAnswers) != 1 {
		t.Fatalf("tool answers = %#v, want exactly one submission", session.toolAnswers)
	}
	updated, _ = m.Update(submitted)
	m = updated.(Model)
	if m.codexToolInputSubmitting != "" || m.codexToolInputSubmitProject != "" {
		t.Fatalf("in-flight guard should clear, got %q/%q", m.codexToolInputSubmitting, m.codexToolInputSubmitProject)
	}
}

func TestToolInputDialogShortensDescriptionsToFitShortPanes(t *testing.T) {
	m, _ := newToolInputDialogModel(t, testToolInputQuestionRequest())
	snapshot, ok := m.currentCodexSnapshot()
	if !ok {
		t.Fatal("snapshot should be available")
	}

	m.height = 44
	tall := ansi.Strip(m.renderCodexToolInputDialogBlock(snapshot, 120))
	if !strings.Contains(tall, "Plan numbering order") {
		t.Fatalf("tall pane should show every option description:\n%s", tall)
	}

	m.height = 18
	short := ansi.Strip(m.renderCodexToolInputDialogBlock(snapshot, 120))
	if strings.Contains(short, "Plan numbering order") {
		t.Fatalf("short pane should drop unselected descriptions:\n%s", short)
	}
	for _, want := range []string{"Transport and MCP adapter", "HUD and avionics content", "Enter  choose"} {
		if !strings.Contains(short, want) {
			t.Fatalf("short pane dialog missing %q:\n%s", want, short)
		}
	}
}

func TestToolInputDialogWrapsLongTypedAnswer(t *testing.T) {
	m, session := newToolInputDialogModel(t, testToolInputQuestionRequest())
	text := "Neither of those yet: first widen menu coverage to the remaining pages, " +
		"then add the reveal operation for offscreen catalog rows, and only then " +
		"take the per-frame performance measurement I promised earlier."
	for _, r := range text {
		updated, _ := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = updated.(Model)
	}

	rendered := ansi.Strip(m.View())
	for _, want := range []string{"Neither of those yet", "performance measurement I", "earlier."} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("wrapped answer missing %q:\n%s", want, rendered)
		}
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter should send the wrapped answer")
	}
	_ = updated.(Model)
	if action := toolInputSubmitResult(t, cmd()); action.err != nil {
		t.Fatalf("wrapped answer submit failed: %v", action.err)
	}
	if got := session.toolAnswers[0]["question-1"]; len(got) != 1 || got[0] != text {
		t.Fatalf("submitted answer = %#v, want the full typed text", got)
	}
}

func TestToolInputDialogQuestionNavigationDoesNotWrapOrDropAnswers(t *testing.T) {
	request := &codexapp.ToolInputRequest{
		ID: "req_two",
		Questions: []codexapp.ToolInputQuestion{
			{ID: "first", Question: "First question?", IsOther: true, Options: []codexapp.ToolInputOption{{Label: "Alpha"}}},
			{ID: "second", Question: "Second question?", IsOther: true, Options: []codexapp.ToolInputOption{{Label: "Beta"}}},
		},
	}
	m, session := newToolInputDialogModel(t, request)

	updated, _ := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("1")})
	m = updated.(Model)
	if len(session.toolAnswers) != 0 {
		t.Fatalf("nothing should be sent while a question is unanswered: %#v", session.toolAnswers)
	}
	if got := m.codexToolAnswers["/tmp/demo"].QuestionIndex; got != 1 {
		t.Fatalf("question index = %d, want the next unanswered question", got)
	}
	rendered := ansi.Strip(m.View())
	for _, want := range []string{"Question 2 of 2 · 1 answered", "shift+Tab  previous question"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("dialog missing %q:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, "Tab  next question") {
		t.Fatalf("the last question should not offer a forward move:\n%s", rendered)
	}

	// Tab on the last question must stay put rather than wrapping to the first.
	updated, _ = m.updateCodexMode(tea.KeyMsg{Type: tea.KeyTab})
	m = updated.(Model)
	if got := m.codexToolAnswers["/tmp/demo"].QuestionIndex; got != 1 {
		t.Fatalf("Tab wrapped to question %d", got)
	}

	updated, _ = m.updateCodexMode(tea.KeyMsg{Type: tea.KeyShiftTab})
	m = updated.(Model)
	if got := m.codexToolAnswers["/tmp/demo"].Answers["first"]; len(got) != 1 || got[0] != "Alpha" {
		t.Fatalf("answer for the first question = %#v, want it preserved", got)
	}

	updated, _ = m.updateCodexMode(tea.KeyMsg{Type: tea.KeyTab})
	m = updated.(Model)
	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("1")})
	if cmd == nil {
		t.Fatal("answering the last open question should submit")
	}
	_ = updated.(Model)
	if action := toolInputSubmitResult(t, cmd()); action.err != nil {
		t.Fatalf("submit failed: %v", action.err)
	}
	answers := session.toolAnswers[0]
	if got := answers["first"]; len(got) != 1 || got[0] != "Alpha" {
		t.Fatalf("submitted first answer = %#v", got)
	}
	if got := answers["second"]; len(got) != 1 || got[0] != "Beta" {
		t.Fatalf("submitted second answer = %#v", got)
	}
}

func TestToolInputDialogMinimizeRevealsTranscript(t *testing.T) {
	m, _ := newToolInputDialogModel(t, testToolInputQuestionRequest())
	transcriptLines := ansi.Strip(m.View())

	updated, _ := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("m"), Alt: true})
	m = updated.(Model)
	minimized := ansi.Strip(m.View())
	if strings.Contains(minimized, "Transport and MCP adapter") {
		t.Fatalf("minimized dialog should not render options:\n%s", minimized)
	}
	for _, want := range []string{"Waiting for your answer:", "alt+m answer"} {
		if !strings.Contains(minimized, want) {
			t.Fatalf("minimized bar missing %q:\n%s", want, minimized)
		}
	}
	if countRenderedLines(minimized) != countRenderedLines(transcriptLines) {
		t.Fatal("pane height should stay stable while minimized")
	}

	// Answer keys must not reach the question while it is parked.
	updated, _ = m.updateCodexMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("1")})
	m = updated.(Model)
	if len(m.codexToolAnswers) != 0 {
		t.Fatalf("minimized dialog should ignore option keys: %#v", m.codexToolAnswers)
	}

	updated, _ = m.updateCodexMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("m"), Alt: true})
	m = updated.(Model)
	if restored := ansi.Strip(m.View()); !strings.Contains(restored, "Transport and MCP adapter") {
		t.Fatalf("alt+m should reopen the dialog:\n%s", restored)
	}
}

func countRenderedLines(view string) int {
	return strings.Count(view, "\n") + 1
}
