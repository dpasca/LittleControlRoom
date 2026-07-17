package service

import (
	"context"
	"time"

	"lcroom/internal/events"
	"lcroom/internal/todocapture"
)

const todoCaptureRelayInterval = 250 * time.Millisecond

// StartTodoCaptureRelay republishes mutations made by the isolated stdio MCP
// process onto the in-process bus. SQLite is the durable cross-process handoff;
// the TUI never blocks on it because relay reads run in this background loop.
func (s *Service) StartTodoCaptureRelay(ctx context.Context) {
	if s == nil || s.store == nil {
		return
	}
	var cursor int64
	for {
		var err error
		cursor, err = s.store.LatestEventIDByType(ctx, todocapture.ExternalEventType)
		if err == nil {
			break
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(todoCaptureRelayInterval):
		}
	}
	ticker := time.NewTicker(todoCaptureRelayInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		for {
			batch, err := s.store.ListEventsAfterIDByType(ctx, todocapture.ExternalEventType, cursor, 100)
			if err != nil || len(batch) == 0 {
				break
			}
			for _, stored := range batch {
				cursor = stored.ID
				captured, err := todocapture.ParseExternalEvent(stored.Payload)
				if err != nil || captured.Disposition != todocapture.DispositionCreated || captured.PublishedInProcess {
					continue
				}
				s.refreshProjectStatusAsync(captured.ProjectPath)
				s.bus.Publish(events.Event{
					Type:        events.ActionApplied,
					At:          stored.At,
					ProjectPath: captured.ProjectPath,
					Payload: map[string]string{
						"action":   "add_todo",
						"provider": captured.Provider,
					},
				})
			}
			if len(batch) < 100 {
				break
			}
		}
	}
}
