package tui

import (
	"testing"

	"lcroom/internal/codexapp"

	tea "github.com/charmbracelet/bubbletea"
)

func TestGracefulQuitCapturesAndInterruptsOwnedEmbeddedTurn(t *testing.T) {
	dataDir := t.TempDir()
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Provider:     codexapp.ProviderCodex,
			ProjectPath:  "/tmp/demo",
			ThreadID:     "thread-demo",
			ActiveTurnID: "turn-demo",
			Started:      true,
			Busy:         true,
			Phase:        codexapp.SessionPhaseRunning,
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		Provider:    codexapp.ProviderCodex,
		ProjectPath: session.projectPath,
		ResumeID:    session.snapshot.ThreadID,
	}); err != nil {
		t.Fatal(err)
	}
	m := Model{codexManager: manager, appDataDirPath: dataDir}

	updated, cmd := m.updateNormalMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	got := updated.(Model)
	if !got.gracefulQuitInFlight || cmd == nil {
		t.Fatalf("q should begin asynchronous graceful shutdown")
	}
	msg, ok := cmd().(gracefulQuitFinishedMsg)
	if !ok || msg.err != nil || msg.captured != 1 {
		t.Fatalf("graceful shutdown message = %#v", msg)
	}
	if !session.interrupted || !session.snapshot.Closed {
		t.Fatalf("session interrupted=%t closed=%t, want both true", session.interrupted, session.snapshot.Closed)
	}
	intents, err := codexapp.ReadRestartIntents(dataDir)
	if err != nil || len(intents) != 1 || intents[0].ActiveTurnID != "turn-demo" {
		t.Fatalf("saved restart intents = %#v, err=%v", intents, err)
	}

	updated, quitCmd := got.Update(msg)
	if quitCmd == nil {
		t.Fatalf("successful graceful shutdown should quit")
	}
	if _, ok := quitCmd().(tea.QuitMsg); !ok {
		t.Fatalf("successful graceful shutdown command should emit tea.QuitMsg")
	}
	_ = updated.(Model)
}
