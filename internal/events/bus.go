package events

import (
	"sync"
	"time"
)

type Type string

const (
	ProjectChanged        Type = "project_changed"
	ProjectMoved          Type = "project_moved"
	ScanCompleted         Type = "scan_completed"
	ActionApplied         Type = "action_applied"
	ClassificationUpdated Type = "classification_updated"
	EventsDropped         Type = "events_dropped"
)

type Event struct {
	Type        Type
	At          time.Time
	ProjectPath string
	Payload     map[string]string
}

type Bus struct {
	mu     sync.RWMutex
	nextID int
	subs   map[int]chan Event
}

func NewBus() *Bus {
	return &Bus{subs: map[int]chan Event{}}
}

func (b *Bus) Subscribe(buffer int) (<-chan Event, func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	id := b.nextID
	b.nextID++
	ch := make(chan Event, buffer)
	b.subs[id] = ch
	return ch, func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if existing, ok := b.subs[id]; ok {
			delete(b.subs, id)
			close(existing)
		}
	}
}

func (b *Bus) Publish(evt Event) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, ch := range b.subs {
		publishNonBlocking(ch, evt)
	}
}

func publishNonBlocking(ch chan Event, evt Event) {
	select {
	case ch <- evt:
		return
	default:
	}

	overflow := Event{Type: EventsDropped, At: evt.At}
	if overflow.At.IsZero() {
		overflow.At = time.Now()
	}
	select {
	case <-ch:
	default:
	}
	select {
	case ch <- overflow:
	default:
	}
}
