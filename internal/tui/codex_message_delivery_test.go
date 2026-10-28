package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"lcroom/internal/codexapp"
)

func TestClaudeMessageDeliveryRemainsVisibleWithActiveWorker(t *testing.T) {
	now := time.Now()
	s := codexapp.Snapshot{Provider: codexapp.ProviderClaudeCode, Busy: true, Started: true,
		Phase: codexapp.SessionPhaseRunning, BackgroundInputSupported: true,
		ParentStatus: "Parent idle · background tasks still reported", ParentActivityAt: now.Add(-time.Minute),
		BackgroundTasks:   []codexapp.BackgroundTaskSnapshot{{ID: "worker"}},
		MessageDeliveries: []codexapp.MessageDeliverySnapshot{{ID: "message", Preview: "Steer the phone test", State: "queued", SubmittedAt: now.Add(-3 * time.Minute)}},
	}
	m := Model{codexInput: newCodexTextarea()}
	text := ansi.Strip(strings.Join(m.codexLowerBlocks(s, 120), "\n"))
	for _, want := range []string{"Parent idle", "last parent activity", "message waiting", "parent has not started", "Steer the phone test"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q:\n%s", want, text)
		}
	}
	if count := strings.Count(text, "message waiting"); count != 1 {
		t.Fatalf("message delivery summary appears %d times:\n%s", count, text)
	}
	if footer := codexFooterStatus(s, now); footer != "Working" {
		t.Fatalf("footer should show session activity: %q", footer)
	}
	for _, width := range []int{35, 60} {
		for _, row := range m.renderCodexMessageDelivery(s, width) {
			if ansi.StringWidth(row) > width {
				t.Fatalf("overflow: %q", row)
			}
		}
	}
	if !codexSnapshotCanSubmitBusyInput(s) {
		t.Fatal("worker blocked composer")
	}
	if availability := codexapp.DescribeSessionInput(s); !availability.Available || availability.Mode != codexapp.SessionInputQueue {
		t.Fatalf("mobile input: %#v", availability)
	}
	cached := overlayCodexSnapshotState(codexapp.Snapshot{}, s)
	s.MessageDeliveries[0].State = "started"
	if cached.MessageDeliveries[0].State != "queued" || cached.ParentStatus != s.ParentStatus || !cached.BackgroundInputSupported {
		t.Fatal("cached receipt state lost or aliased")
	}
}

func TestFailedPromptShowsErrorAlongsideRestoredDraft(t *testing.T) {
	const path = "/tmp/worker"
	snapshot := codexapp.Snapshot{Provider: codexapp.ProviderClaudeCode, ProjectPath: path,
		Started: true, Busy: true, Phase: codexapp.SessionPhaseRunning, BackgroundInputSupported: true,
		BackgroundTasks: []codexapp.BackgroundTaskSnapshot{{ID: "ios-exit", Status: "running"}},
	}
	session := &fakeCodexSession{projectPath: path, snapshot: snapshot}
	manager := codexapp.NewManagerWithFactory(func(codexapp.LaunchRequest, func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{ProjectPath: path, Provider: codexapp.ProviderClaudeCode}); err != nil {
		t.Fatal(err)
	}
	m := Model{codexManager: manager, codexVisibleProject: path, codexInput: newCodexTextarea(),
		codexSnapshots: map[string]codexapp.Snapshot{path: snapshot},
	}
	const reason = "finish the current task turn before beginning another result run"
	updated, _ := m.applyCodexActionMsg(codexActionMsg{
		projectPath: path, restoreDraft: codexDraft{Text: "let me test the game"}, err: errors.New(reason),
	})
	m = updated.(Model)
	if m.codexInput.Value() != "let me test the game" {
		t.Fatal("failed submission lost the draft")
	}
	text := ansi.Strip(strings.Join(m.codexLowerBlocks(snapshot, 80), "\n"))
	if !strings.Contains(strings.Join(strings.Fields(text), " "), "Message not sent: "+reason) {
		t.Fatalf("send failure invisible in embedded pane: %s", text)
	}
	m.codexVisibleProject = "/tmp/other"
	if text := ansi.Strip(strings.Join(m.codexLowerBlocks(snapshot, 80), "\n")); strings.Contains(text, "Message not sent:") {
		t.Fatal("send error leaked into another project")
	}
	m.codexVisibleProject = path
	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(Model)
	if cmd == nil || m.codexSubmitErrors[path] != "" {
		t.Fatal("retry did not clear the previous error and submit the draft")
	}
	if action := cmd().(codexActionMsg); action.err != nil || len(session.submitted) != 1 || session.submitted[0] != "let me test the game" {
		t.Fatalf("retry failed to deliver the draft: %+v %v", action, session.submitted)
	}
}
