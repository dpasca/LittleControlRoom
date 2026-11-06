package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lcroom/internal/codexapp"
	"lcroom/internal/projectrun"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestGracefulQuitWaitsForRuntimeCleanupWithoutSecondConfirmation(t *testing.T) {
	dir := t.TempDir()
	// A normal SIGTERM cleanup can exceed the old two-second best-effort
	// CloseAll deadline. One confirmed quit should wait for its completion.
	script := "trap 'sleep 3; exit 0' TERM\necho READY\nwhile :; do sleep 0.1; done\n"
	if err := os.WriteFile(filepath.Join(dir, "slow-stop.sh"), []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	manager := projectrun.NewManager()
	defer manager.CloseAll()
	if _, err := manager.Start(projectrun.StartRequest{ProjectPath: dir, Command: "sh ./slow-stop.sh"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	for {
		snapshot, err := manager.Snapshot(dir)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(strings.Join(snapshot.RecentOutput, "\n"), "READY") {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("runtime did not install its cleanup handler")
		case <-time.After(10 * time.Millisecond):
		}
	}
	m := Model{runtimeManager: manager, appDataDirPath: t.TempDir()}
	updated, cmd := m.beginGracefulQuit()
	quitting := updated.(Model)
	if _, repeat := quitting.beginGracefulQuit(); repeat != nil {
		t.Fatal("second activation stacked another shutdown")
	}
	finished := cmd().(gracefulQuitFinishedMsg)
	if finished.err != nil {
		t.Fatalf("one confirmed quit paused during normal runtime cleanup: %v", finished.err)
	}
	snapshot, err := manager.Snapshot(dir)
	if err != nil || snapshot.Running {
		t.Fatalf("quit left runtime active: %+v %v", snapshot, err)
	}
	_, quitCmd := quitting.applyGracefulQuitFinishedMsg(finished)
	if _, ok := quitCmd().(tea.QuitMsg); !ok {
		t.Fatal("cleanup completed without quitting")
	}
}

func TestQuitKeyOpensConfirmationWithStaySelected(t *testing.T) {
	m := Model{width: 100, height: 24}

	updated, cmd := m.updateNormalMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("q should not begin shutdown before confirmation")
	}
	if got.quitConfirm == nil {
		t.Fatalf("q should open the quit confirmation")
	}
	if got.quitConfirm.Selected != quitConfirmFocusStay {
		t.Fatalf("default quit confirmation selection = %d, want stay", got.quitConfirm.Selected)
	}
	if got.gracefulQuitInFlight {
		t.Fatalf("graceful shutdown should not start while confirmation is open")
	}

	rendered := ansi.Strip(got.View())
	for _, want := range []string{
		"Quit Little Control Room?",
		"In-flight engineer turns will be saved",
		"managed runtimes will be stopped",
		"Quit",
		"Stay",
		"Enter confirm",
		"Esc stay",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("quit confirmation missing %q:\n%s", want, rendered)
		}
	}

	updated, cmd = got.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if cmd != nil || got.quitConfirm != nil || got.gracefulQuitInFlight {
		t.Fatalf("Enter on the default stay choice should close the dialog without quitting")
	}
	if got.status != "Quit canceled" {
		t.Fatalf("status = %q, want quit canceled", got.status)
	}
}

func TestControlCOpensQuitConfirmationWithStaySelected(t *testing.T) {
	m := Model{appDataDirPath: t.TempDir()}

	updated, cmd := m.updateNormalMode(tea.KeyMsg{Type: tea.KeyCtrlC})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("ctrl+c should not begin shutdown before confirmation")
	}
	if got.quitConfirm == nil {
		t.Fatalf("ctrl+c should open the quit confirmation")
	}
	if got.quitConfirm.Selected != quitConfirmFocusStay {
		t.Fatalf("default quit confirmation selection = %d, want stay", got.quitConfirm.Selected)
	}
	if got.gracefulQuitInFlight {
		t.Fatalf("graceful shutdown should not start while confirmation is open")
	}
}

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
	if got.quitConfirm == nil || got.gracefulQuitInFlight || cmd != nil {
		t.Fatalf("q should wait for explicit quit confirmation")
	}
	updated, _ = got.Update(tea.KeyMsg{Type: tea.KeyLeft})
	got = updated.(Model)
	if got.quitConfirm == nil || got.quitConfirm.Selected != quitConfirmFocusQuit {
		t.Fatalf("left should select quit")
	}
	updated, cmd = got.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if !got.gracefulQuitInFlight || got.quitConfirm != nil || cmd == nil {
		t.Fatalf("confirming quit should begin asynchronous graceful shutdown")
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
