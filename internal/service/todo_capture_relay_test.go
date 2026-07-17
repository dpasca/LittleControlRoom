package service

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"lcroom/internal/config"
	"lcroom/internal/events"
	"lcroom/internal/model"
	"lcroom/internal/store"
	"lcroom/internal/todocapture"
)

func TestTodoCaptureRelayPublishesOnlyExternalCreatedMutations(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "relay.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	bus := events.NewBus()
	svc := New(config.Default(), st, bus, nil)
	svc.refreshProjectStatusFn = func(context.Context, string) error { return nil }
	eventCh, unsubscribe := bus.Subscribe(8)
	defer unsubscribe()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		svc.StartTodoCaptureRelay(ctx)
	}()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("TODO capture relay did not stop")
		}
	}()
	// Let the relay establish its initial cursor before inserting the event.
	time.Sleep(2 * todoCaptureRelayInterval)

	projectPath := filepath.Join(t.TempDir(), "project")
	addCaptureEvent(t, st, todocapture.ExternalEvent{
		Action:        "add_todo",
		Provider:      "claude_code",
		ProjectPath:   projectPath,
		Disposition:   todocapture.DispositionCreated,
		CaptureKind:   todocapture.CaptureExplicitRequest,
		RequestedPath: projectPath,
	})
	deadline := time.After(2 * time.Second)
	for {
		select {
		case event := <-eventCh:
			if event.Type != events.ActionApplied {
				continue
			}
			if event.ProjectPath != projectPath || event.Payload["action"] != "add_todo" || event.Payload["provider"] != "claude_code" {
				t.Fatalf("relayed event = %#v", event)
			}
			goto observed
		case <-deadline:
			t.Fatal("external created TODO was not relayed")
		}
	}

observed:
	addCaptureEvent(t, st, todocapture.ExternalEvent{
		Action:             "add_todo",
		Provider:           "lcagent",
		ProjectPath:        projectPath,
		Disposition:        todocapture.DispositionCreated,
		CaptureKind:        todocapture.CaptureExplicitRequest,
		RequestedPath:      projectPath,
		PublishedInProcess: true,
	})
	timer := time.NewTimer(3 * todoCaptureRelayInterval)
	defer timer.Stop()
	for {
		select {
		case event := <-eventCh:
			if event.Type == events.ActionApplied {
				t.Fatalf("in-process capture was relayed twice: %#v", event)
			}
		case <-timer.C:
			return
		}
	}
}

func addCaptureEvent(t *testing.T, st *store.Store, captured todocapture.ExternalEvent) {
	t.Helper()
	payload, err := json.Marshal(captured)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddEvent(context.Background(), model.StoredEvent{
		At:          time.Now(),
		ProjectPath: captured.ProjectPath,
		Type:        todocapture.ExternalEventType,
		Payload:     string(payload),
	}); err != nil {
		t.Fatal(err)
	}
}
