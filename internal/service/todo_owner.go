package service

import (
	"context"
	"encoding/json"
	"fmt"

	"lcroom/internal/control"
	"lcroom/internal/todocontrol"
)

// RepairTodoOwner is the supported guarded repair boundary. It never touches
// checkout files, provider sessions, origin TODOs or historical launch requests.
func (s *Service) RepairTodoOwner(ctx context.Context, input control.TodoRepairOwnerInput) (bool, error) {
	if s == nil || s.store == nil {
		return false, fmt.Errorf("service unavailable")
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return false, err
	}
	inv, err := control.ValidateInvocation(control.Invocation{Capability: control.CapabilityTodoRepairOwner, RequestID: input.RequestID, Args: raw})
	if err != nil {
		return false, err
	}
	if err := json.Unmarshal(inv.Args, &input); err != nil {
		return false, err
	}
	input, _, err = todocontrol.InspectRepair(ctx, s.store, input)
	if err != nil {
		return false, err
	}
	changed, err := s.store.RepairTodoOwner(ctx, input)
	if err != nil {
		return false, err
	}
	s.refreshProjectStatusAsync(input.ExpectedOwnerPath)
	s.refreshProjectStatusAsync(input.RepositoryPath)
	return changed, nil
}
