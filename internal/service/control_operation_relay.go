package service

import (
	"context"
	"time"

	"lcroom/internal/control"
	"lcroom/internal/events"
)

const controlOperationRelayInterval = 250 * time.Millisecond

// StartControlOperationRelay bridges confirmable operations proposed by an
// isolated MCP process into the live TUI. SQLite is the durable cross-process
// queue; all reads and claims happen in this background loop.
func (s *Service) StartControlOperationRelay(ctx context.Context) {
	if s == nil || s.store == nil || s.bus == nil {
		return
	}
	var pending []control.Operation
	recovered := false
	ticker := time.NewTicker(controlOperationRelayInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if !recovered {
			// Retry startup reads after a transient store failure. A waiting
			// confirmation belongs to the previous UI process and is requeued.
			if err := s.store.RequeueWaitingControlOperations(ctx); err != nil {
				continue
			}
			var err error
			pending, err = s.store.ListUnqueuedEngineerOperations(ctx)
			if err != nil {
				continue
			}
			recovered = true
		}
		if len(pending) > 0 {
			operation := pending[0]
			current, err := s.store.GetControlOperation(ctx, operation.ID)
			if err != nil {
				continue
			}
			_, queued, err := s.store.FindEngineerMessageByOperation(ctx, operation.ID)
			if err != nil {
				continue
			}
			if queued || current.Status.Terminal() {
				pending = pending[1:]
				continue
			}
			// The event bus may overflow during startup. Keep retrying until
			// the mailbox insert (or a terminal refusal) acknowledges recovery.
			s.bus.Publish(events.Event{Type: events.ControlProposed, At: operation.UpdatedAt, ProjectPath: operation.ProjectPath,
				Payload: map[string]string{"operation_id": operation.ID, "provider": operation.Provider, "capability": string(operation.Capability)}})
			continue
		}
		operation, found, err := s.store.ClaimNextControlOperation(ctx)
		if err == nil && found {
			s.bus.Publish(events.Event{
				Type:        events.ControlProposed,
				At:          operation.UpdatedAt,
				ProjectPath: operation.ProjectPath,
				Payload: map[string]string{
					"operation_id": operation.ID,
					"provider":     operation.Provider,
					"capability":   string(operation.Capability),
				},
			})
		}
	}
}
