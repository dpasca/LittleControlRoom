package service

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"lcroom/internal/config"
	"lcroom/internal/control"
	"lcroom/internal/events"
	"lcroom/internal/store"
)

func TestControlOperationRelayClaimsAndPublishesProposal(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "relay.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	bus := events.NewBus()
	svc := New(config.Default(), st, bus, nil)
	eventCh, unsubscribe := bus.Subscribe(8)
	defer unsubscribe()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		svc.StartControlOperationRelay(ctx)
	}()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("control operation relay did not stop")
		}
	}()

	operationID := "lcrop_relay"
	args, err := json.Marshal(control.TodoAddInput{
		RequestID:   operationID,
		ProjectPath: t.TempDir(),
		Text:        "Verify the proposal relay",
	})
	if err != nil {
		t.Fatal(err)
	}
	created, err := st.CreateControlOperation(context.Background(), control.Operation{
		ID:         operationID,
		Capability: control.CapabilityTodoAdd,
		Invocation: control.Invocation{
			RequestID:  operationID,
			Capability: control.CapabilityTodoAdd,
			Args:       args,
		},
		Status:      control.OperationProposed,
		Source:      "test",
		Provider:    "codex",
		SessionKey:  "session",
		ProjectPath: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}

	select {
	case event := <-eventCh:
		if event.Type != events.ControlProposed || event.Payload["operation_id"] != created.ID {
			t.Fatalf("relay event = %#v", event)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("control proposal was not relayed")
	}
	claimed, err := st.GetControlOperation(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if claimed.Status != control.OperationWaitingForConfirmation {
		t.Fatalf("claimed status = %q, want waiting_for_confirmation", claimed.Status)
	}
}

func TestControlOperationRelayRetriesRecoveredSendUntilMailboxAcknowledges(t *testing.T) {
	svc, st := newEngineerDispatchTestService(t)
	op := createDispatchTestOperation(t, st, "lcrop_recover_buffered_send", dispatchCallerPath, "codex", dispatchCallerKey,
		control.CapabilityEngineerSendPrompt, control.EngineerSendPromptInput{ProjectPath: dispatchWorkerPath, Provider: control.ProviderCodex, SessionMode: control.SessionModeResumeOrNew, Prompt: "Recover queued work"})
	if _, err := st.UpdateControlOperationStatus(t.Context(), op.ID, control.OperationRunning, nil, nil); err != nil {
		t.Fatal(err)
	}
	bus := events.NewBus()
	svc = New(svc.cfg, st, bus, nil)
	ch, unsubscribe := bus.Subscribe(1)
	defer unsubscribe()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); svc.StartControlOperationRelay(ctx) }()
	defer func() { cancel(); <-done }()
	select {
	case event := <-ch:
		if event.Type != events.ControlProposed || event.Payload["operation_id"] != op.ID {
			t.Fatalf("expected recovered send: %+v", event)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("relay did not publish recovery")
	}
	// The UI has not acknowledged recovery. A startup burst overflows its
	// event buffer; delivery must still be retried rather than forgotten.
	bus.Publish(events.Event{Type: events.ScanCompleted})
	bus.Publish(events.Event{Type: events.ScanCompleted})
	select {
	case event := <-ch:
		if event.Type != events.EventsDropped {
			t.Fatalf("expected a dropped startup event, got %+v", event)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("relay did not publish recovery")
	}
	select {
	case event := <-ch:
		if event.Type != events.ControlProposed || event.Payload["operation_id"] != op.ID {
			t.Fatalf("lost recovery was not retried: %+v", event)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("confirmed send was silently lost after startup overflow")
	}
	if _, err := st.CreateEngineerMessage(t.Context(), control.EngineerMessage{OperationID: op.ID, ProjectPath: dispatchWorkerPath, Provider: control.ProviderCodex, SessionMode: control.SessionModeResumeOrNew, Prompt: "Recover queued work"}); err != nil {
		t.Fatal(err)
	}
}
