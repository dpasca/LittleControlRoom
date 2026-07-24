package service

import (
	"context"
	"time"

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
	// A waiting confirmation belongs to the previous UI process. Requeue it so
	// the new host can present it again after a graceful or unexpected restart.
	if err := s.store.RequeueWaitingControlOperations(ctx); err != nil {
		return
	}
	ticker := time.NewTicker(controlOperationRelayInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
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
