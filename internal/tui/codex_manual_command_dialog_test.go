package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"lcroom/internal/codexapp"
)

const testManualCommandProjectPath = "/tmp/manual-command-demo"

func testManualCommandRequest() *codexapp.ToolInputRequest {
	return &codexapp.ToolInputRequest{
		ID:       "manual-command-1",
		ThreadID: "lca-thread-1",
		ManualCommand: &codexapp.ManualCommandRequest{
			QuestionID:     "command_status",
			Prompt:         "Run this command in your terminal, then report what happened.",
			Command:        "./bin/yt-dlp -U",
			CWD:            "/tmp/YouClip2",
			Reason:         "The binary is outside the task workspace.",
			CompletedLabel: "Ran it",
			DeclinedLabel:  "Didn't run it",
		},
		Questions: []codexapp.ToolInputQuestion{{
			Header:   "User command",
			ID:       "command_status",
			Question: "Run this command in your terminal, then report what happened.",
			IsOther:  true,
			Options: []codexapp.ToolInputOption{
				{Label: "Ran it"},
				{Label: "Didn't run it"},
			},
		}},
	}
}

func testManualCommandSnapshot() codexapp.Snapshot {
	return codexapp.Snapshot{
		Provider:         codexapp.ProviderLCAgent,
		Started:          true,
		Busy:             true,
		Phase:            codexapp.SessionPhaseRunning,
		Status:           "Waiting for you to run a command",
		PendingToolInput: testManualCommandRequest(),
	}
}

func testManualCommandModel(t *testing.T) (Model, *fakeCodexSession) {
	t.Helper()
	snapshot := testManualCommandSnapshot()
	session := &fakeCodexSession{
		projectPath: testManualCommandProjectPath,
		snapshot:    snapshot,
	}
	manager := codexapp.NewManagerWithFactory(func(codexapp.LaunchRequest, func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: testManualCommandProjectPath,
		Provider:    codexapp.ProviderLCAgent,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}
	return Model{
		codexManager:        manager,
		codexVisibleProject: testManualCommandProjectPath,
		codexHiddenProject:  testManualCommandProjectPath,
		codexInput:          newCodexTextarea(),
		codexToolAnswers:    make(map[string]codexToolAnswerState),
		codexViewport:       viewport.New(0, 0),
		codexSnapshots:      map[string]codexapp.Snapshot{testManualCommandProjectPath: snapshot},
		width:               100,
		height:              30,
	}, session
}

func TestLCAgentManualCommandRendersDedicatedWarningDialog(t *testing.T) {
	snapshot := testManualCommandSnapshot()
	m := Model{
		codexVisibleProject: testManualCommandProjectPath,
		codexInput:          newCodexTextarea(),
		codexViewport:       viewport.New(0, 0),
		codexSnapshots:      map[string]codexapp.Snapshot{testManualCommandProjectPath: snapshot},
		width:               100,
		height:              30,
	}

	raw := m.renderCodexManualCommandDialogContent(snapshot, *snapshot.PendingToolInput, *snapshot.PendingToolInput.ManualCommand, 76)
	rendered := ansi.Strip(raw)
	normalized := strings.Join(strings.Fields(rendered), " ")
	for _, want := range []string{
		"Manual terminal action required",
		"LCAgent is paused because it cannot perform this action",
		"Why it stopped",
		"The binary is outside the task workspace.",
		"Working directory: /tmp/YouClip2",
		"./bin/yt-dlp -U",
		"This dialog does not execute or approve the command.",
		"C copy command",
		"R I ran it — verify",
		"S skip",
		"O report another outcome",
		"Esc hide pane",
	} {
		if !strings.Contains(normalized, want) {
			t.Fatalf("manual command dialog missing %q:\n%s", want, rendered)
		}
	}
	if !strings.Contains(raw, detailWarningStyle.Render("Manual terminal action required")) {
		t.Fatalf("manual command title is not warning-colored: %q", raw)
	}
	if count := strings.Count(rendered, "./bin/yt-dlp -U"); count != 1 {
		t.Fatalf("manual command rendered %d times, want once:\n%s", count, rendered)
	}
	for _, line := range strings.Split(raw, "\n") {
		if got := lipgloss.Width(line); got > 76 {
			t.Fatalf("dialog line width = %d, want <= 76: %q", got, ansi.Strip(line))
		}
	}

	lower := ansi.Strip(strings.Join(m.codexLowerBlocks(snapshot, 100), "\n"))
	for _, unwanted := range []string{"Structured input", "1-9 choose", "./bin/yt-dlp -U", "> "} {
		if strings.Contains(lower, unwanted) {
			t.Fatalf("manual command background should not contain %q: %q", unwanted, lower)
		}
	}
}

func TestLCAgentManualCommandReportedRunUsesExplicitResponse(t *testing.T) {
	m, session := testManualCommandModel(t)
	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	got := updated.(Model)
	if cmd == nil {
		t.Fatal("reported-run action should send a response")
	}
	if got.status != "Reporting the command as run; LCAgent must verify the result..." {
		t.Fatalf("status = %q", got.status)
	}
	msg := cmd()
	action, ok := msg.(codexActionMsg)
	if !ok {
		t.Fatalf("response command returned %T", msg)
	}
	if action.status != "Manual command response sent to LCAgent" {
		t.Fatalf("action status = %q", action.status)
	}
	if len(session.toolAnswers) != 1 || len(session.toolAnswers[0]["command_status"]) != 1 ||
		session.toolAnswers[0]["command_status"][0] != "Ran it" {
		t.Fatalf("manual command answers = %#v", session.toolAnswers)
	}
}

func TestLCAgentManualCommandSkipUsesDeclinedResponse(t *testing.T) {
	m, session := testManualCommandModel(t)
	_, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	if cmd == nil {
		t.Fatal("skip action should send a response")
	}
	_ = cmd()
	if len(session.toolAnswers) != 1 || len(session.toolAnswers[0]["command_status"]) != 1 ||
		session.toolAnswers[0]["command_status"][0] != "Didn't run it" {
		t.Fatalf("manual command answers = %#v", session.toolAnswers)
	}
}

func TestLCAgentManualCommandCanReportAnotherOutcome(t *testing.T) {
	m, session := testManualCommandModel(t)
	updated, _ := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	got := updated.(Model)
	if got.codexManualCommandOutcome == nil {
		t.Fatal("report-another-outcome action did not open the outcome editor")
	}

	updated, _ = got.updateCodexMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("The updater failed with exit 1")})
	got = updated.(Model)
	updated, cmd := got.updateCodexMode(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if cmd == nil {
		t.Fatal("custom outcome should send a response")
	}
	_ = cmd()
	if got.codexManualCommandOutcome != nil {
		t.Fatal("outcome editor remained open after submission")
	}
	if len(session.toolAnswers) != 1 || len(session.toolAnswers[0]["command_status"]) != 1 ||
		session.toolAnswers[0]["command_status"][0] != "The updater failed with exit 1" {
		t.Fatalf("manual command answers = %#v", session.toolAnswers)
	}
}

func TestLCAgentManualCommandCopiesOnlyTheExactCommand(t *testing.T) {
	previousWriter := clipboardTextWriter
	var copied string
	clipboardTextWriter = func(text string) error {
		copied = text
		return nil
	}
	t.Cleanup(func() { clipboardTextWriter = previousWriter })

	m, _ := testManualCommandModel(t)
	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	got := updated.(Model)
	if cmd == nil || !got.codexManualCommandCopyBusy {
		t.Fatalf("copy action = cmd %v, busy %t", cmd != nil, got.codexManualCommandCopyBusy)
	}
	msg, ok := cmd().(codexManualCommandCopyMsg)
	if !ok {
		t.Fatalf("copy command returned unexpected message")
	}
	if copied != "./bin/yt-dlp -U" {
		t.Fatalf("copied text = %q", copied)
	}
	updated, _ = got.applyCodexManualCommandCopyMsg(msg)
	got = updated.(Model)
	if got.codexManualCommandCopyBusy || got.status != "Copied manual command to clipboard" {
		t.Fatalf("copy result = busy %t, status %q", got.codexManualCommandCopyBusy, got.status)
	}
}

func TestLCAgentManualCommandOutcomeEscapeReturnsToMainDialog(t *testing.T) {
	m, _ := testManualCommandModel(t)
	updated, _ := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	got := updated.(Model)
	updated, cmd := got.updateCodexMode(tea.KeyMsg{Type: tea.KeyEsc})
	got = updated.(Model)
	if cmd != nil || got.codexManualCommandOutcome != nil {
		t.Fatalf("escape should close only the outcome editor, cmd=%v state=%#v", cmd, got.codexManualCommandOutcome)
	}
	if got.codexVisibleProject != testManualCommandProjectPath {
		t.Fatalf("escape from outcome editor hid the engineer pane")
	}
}
