package codexapp

import (
	"errors"
	"testing"
	"time"
)

type delayedStopTestSession struct {
	*fakeSession
	exited bool
}

func (s *delayedStopTestSession) WaitClosed(time.Duration) bool { return s.exited }

func TestConfirmedStopRetainsOwnershipUntilWorkerExits(t *testing.T) {
	worker := &delayedStopTestSession{fakeSession: &fakeSession{snapshot: Snapshot{Started: true, Busy: true}}}
	manager := NewManagerWithFactory(func(req LaunchRequest, _ func()) (Session, error) {
		worker.projectPath = req.ProjectPath
		return worker, nil
	})
	path := t.TempDir()
	if _, _, err := manager.Open(LaunchRequest{ProjectPath: path}); err != nil {
		t.Fatal(err)
	}
	canceled := false
	verify := func(Snapshot) error { return nil }
	cancel := func() error { canceled = true; return nil }
	if err := manager.StopProjectSession(path, verify, cancel, time.Millisecond); err == nil {
		t.Fatal("stop succeeded before process exit")
	}
	if _, ok := manager.Session(path); !ok || canceled {
		t.Fatal("incomplete stop removed ownership or canceled the run")
	}
	worker.exited = true
	if err := manager.StopProjectSession(path, verify, cancel, time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if _, ok := manager.Session(path); ok || !canceled {
		t.Fatal("completed stop did not cancel and remove session")
	}
}

func TestConfirmedStopChecksWorkerIdentityBeforeInterrupting(t *testing.T) {
	worker := &fakeSession{snapshot: Snapshot{Started: true, Busy: true}}
	manager := NewManagerWithFactory(func(req LaunchRequest, _ func()) (Session, error) {
		worker.projectPath = req.ProjectPath
		return worker, nil
	})
	defer manager.CloseAll()
	path := t.TempDir()
	if _, _, err := manager.Open(LaunchRequest{ProjectPath: path}); err != nil {
		t.Fatal(err)
	}
	changed := errors.New("different worker")
	err := manager.StopProjectSession(path, func(Snapshot) error { return changed }, func() error { t.Fatal("canceled a different worker"); return nil }, time.Millisecond)
	if !errors.Is(err, changed) || worker.Snapshot().Closed || worker.interrupted {
		t.Fatalf("identity check bypassed: %v", err)
	}
}

func TestLCAgentClosedPaneDoesNotConfirmRunningProcessExit(t *testing.T) {
	session := &lcagentSession{closed: true, busy: true}
	if session.WaitClosed(time.Millisecond) {
		t.Fatal("closed pane reported a running worker as stopped")
	}
	session.finishRun("interrupted", false, nil)
	if !session.WaitClosed(time.Second) {
		t.Fatal("process completion did not release the stop wait")
	}
}
