package tui

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lcroom/internal/control"
	"lcroom/internal/model"
)

func boardWatchTestProject(t *testing.T, m Model) (project, outbox string) {
	t.Helper()
	project = t.TempDir()
	if err := m.svc.Store().UpsertProjectState(context.Background(), model.ProjectState{Path: project, Name: "tracker", PresentOnDisk: true, InScope: true}); err != nil {
		t.Fatal(err)
	}
	return project, filepath.Join(project, "review-events.jsonl")
}

func boardWatchInvocation(t *testing.T, capability control.CapabilityName, args any) control.Invocation {
	t.Helper()
	data, _ := json.Marshal(args)
	inv, err := control.ValidateInvocation(control.Invocation{Capability: capability, Args: data})
	if err != nil {
		t.Fatal(err)
	}
	return inv
}

func TestConfirmedBoardWatchRegistrationAndUnregistration(t *testing.T) {
	m, st := wakeupsTestModel(t)
	project, outbox := boardWatchTestProject(t, m)
	input := control.EngineerBoardWatchRegisterInput{BoardID: "board-1", ProjectPath: project, Provider: control.ProviderClaudeCode, TargetSessionID: "director-1", OutboxPath: outbox}
	inv := boardWatchInvocation(t, control.CapabilityEngineerBoardWatchRegister, input)

	msg := m.executeBoardWatchControl(inv).cmd().(boardWatchControlMsg)
	if msg.err != nil || !strings.Contains(msg.status, "registered") {
		t.Fatalf("register = %#v", msg)
	}
	if status, err := bossControlExecutionStatus(inv, msg); err != nil || !strings.Contains(status, "will wake the director while Little Control Room runs") {
		t.Fatalf("chat status = %q err=%v", status, err)
	}
	again := m.executeBoardWatchControl(inv).cmd().(boardWatchControlMsg)
	if again.err != nil || !strings.Contains(again.status, "already registered") {
		t.Fatalf("repeated confirmation = %#v", again)
	}
	watches, _ := st.ListBoardWatches(context.Background(), project, false)
	if len(watches) != 1 {
		t.Fatalf("watches = %#v", watches)
	}

	// The host re-validates at execution: an outbox outside the project is refused.
	bad := input
	bad.BoardID = "board-2"
	bad.OutboxPath = filepath.Join(t.TempDir(), "escape.jsonl")
	refused := m.executeBoardWatchControl(boardWatchInvocation(t, control.CapabilityEngineerBoardWatchRegister, bad)).cmd().(boardWatchControlMsg)
	if refused.err == nil || !strings.Contains(refused.status, "not registered") {
		t.Fatalf("outside outbox = %#v", refused)
	}

	stopped := m.executeBoardWatchControl(boardWatchInvocation(t, control.CapabilityEngineerBoardWatchUnregister, control.EngineerBoardWatchUnregisterInput{WatchID: watches[0].ID})).cmd().(boardWatchControlMsg)
	if stopped.err != nil || !strings.Contains(stopped.status, "unregistered") {
		t.Fatalf("unregister = %#v", stopped)
	}
	if left, _ := st.ListBoardWatches(context.Background(), project, false); len(left) != 0 {
		t.Fatalf("watch still listed: %#v", left)
	}
}

func TestMailboxPollQueuesWakeFromRegisteredOutboxAfterRestart(t *testing.T) {
	m, st := wakeupsTestModel(t)
	project, outbox := boardWatchTestProject(t, m)
	prepared := m.executeBoardWatchControl(boardWatchInvocation(t, control.CapabilityEngineerBoardWatchRegister,
		control.EngineerBoardWatchRegisterInput{BoardID: "board-1", ProjectPath: project, Provider: control.ProviderClaudeCode, TargetSessionID: "director-1", OutboxPath: outbox})).cmd().(boardWatchControlMsg)
	if prepared.err != nil {
		t.Fatal(prepared.err)
	}
	// The owner clicked while LCR was not running: the line is only in the file.
	event := control.BoardReviewEvent{Schema: control.BoardReviewEventSchema, Kind: control.BoardReviewRequested, EventID: "ev-1", BoardID: "board-1", ItemID: "LP-203", CreatedAt: time.Now().Add(-time.Hour)}
	line, _ := json.Marshal(event)
	if err := os.WriteFile(outbox, append(line, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}

	// The first poll after startup recovers and reads the outbox in the same pass.
	loaded := m.loadEngineerMessagesCmd(true)().(engineerMessagesLoadedMsg)
	if loaded.err != nil || loaded.boardWatchErr != nil || len(loaded.messages) != 1 {
		t.Fatalf("loaded = %#v", loaded)
	}
	wake := loaded.messages[0]
	if wake.TargetSessionID != "director-1" || !strings.Contains(wake.Prompt, "ev-1") || wake.ExpiresAt.Before(event.CreatedAt.Add(23*time.Hour)) {
		t.Fatalf("wake = %#v", wake)
	}
	// A later poll (or another restart) does not queue it again.
	next := m.loadEngineerMessagesCmd(true)().(engineerMessagesLoadedMsg)
	if len(next.messages) != 1 || next.messages[0].ID != wake.ID {
		t.Fatalf("second poll = %#v", next.messages)
	}
	queued, _ := st.ListQueuedEngineerMessages(context.Background(), 10)
	if len(queued) != 1 {
		t.Fatalf("queued = %d", len(queued))
	}
}
