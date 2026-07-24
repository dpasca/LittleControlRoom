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
