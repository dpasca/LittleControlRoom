package events

import (
	"testing"
	"time"
)

func TestPublishOverflowSignalsDroppedEvents(t *testing.T) {
	bus := NewBus()
	ch, unsub := bus.Subscribe(1)
	defer unsub()

	at := time.Date(2026, 4, 23, 9, 0, 0, 0, time.UTC)
	bus.Publish(Event{Type: ProjectChanged, At: at, ProjectPath: "/tmp/a"})
	bus.Publish(Event{Type: ActionApplied, At: at.Add(time.Second), ProjectPath: "/tmp/b"})

	select {
	case evt := <-ch:
		if evt.Type != EventsDropped {
			t.Fatalf("event type = %s, want %s after subscriber overflow", evt.Type, EventsDropped)
		}
		if evt.ProjectPath != "" {
			t.Fatalf("overflow event project path = %q, want empty for conservative reload", evt.ProjectPath)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for overflow event")
	}

	bus.Publish(Event{Type: ScanCompleted, At: at.Add(2 * time.Second)})
	select {
	case evt := <-ch:
		if evt.Type != ScanCompleted {
			t.Fatalf("event type after overflow drain = %s, want %s", evt.Type, ScanCompleted)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for post-overflow event")
	}
}

func TestPublishOverflowDoesNotPenalizeCurrentSubscribers(t *testing.T) {
	bus := NewBus()
	slowCh, slowUnsub := bus.Subscribe(1)
	defer slowUnsub()
	currentCh, currentUnsub := bus.Subscribe(1)
	defer currentUnsub()

	first := Event{Type: ProjectChanged, At: time.Date(2026, 4, 23, 9, 0, 0, 0, time.UTC), ProjectPath: "/tmp/a"}
	second := Event{Type: ActionApplied, At: first.At.Add(time.Second), ProjectPath: "/tmp/b"}
	bus.Publish(first)

	select {
	case evt := <-currentCh:
		if evt.Type != first.Type {
			t.Fatalf("current subscriber first event type = %s, want %s", evt.Type, first.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for current subscriber first event")
	}

	bus.Publish(second)

	select {
	case evt := <-slowCh:
		if evt.Type != EventsDropped {
			t.Fatalf("slow subscriber event type = %s, want %s", evt.Type, EventsDropped)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for slow subscriber overflow event")
	}
	select {
	case evt := <-currentCh:
		if evt.Type != second.Type {
			t.Fatalf("current subscriber second event type = %s, want %s", evt.Type, second.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for current subscriber second event")
	}
}
