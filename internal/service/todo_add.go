package service

import (
	"context"
	"fmt"

	"lcroom/internal/control"
	"lcroom/internal/model"
	"lcroom/internal/todocontrol"
)

// AddTodoForControl rechecks the reviewed owner immediately before persistence.
// The requested checkout remains in the invocation for permissions/provenance.
func (s *Service) AddTodoForControl(ctx context.Context, input control.TodoAddInput) (model.TodoItem, error) {
	if s == nil || s.store == nil {
		return model.TodoItem{}, fmt.Errorf("service unavailable")
	}
	input, err := control.NormalizeTodoAddInput(input)
	if err != nil {
		return model.TodoItem{}, err
	}
	plan, err := todocontrol.InspectAdd(ctx, s.store, input)
	if err != nil {
		return model.TodoItem{}, err
	}
	return s.addTodoOwned(ctx, plan.OwnerPath, input.Text, nil)
}
