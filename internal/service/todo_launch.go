package service

import (
	"context"
	"fmt"
	"lcroom/internal/control"
	"lcroom/internal/model"
	"lcroom/internal/todocontrol"
)

func (s *Service) AddTodoForLaunch(ctx context.Context, input control.TodoCreateWorktreeAndStartEngineerInput) (model.TodoItem, error) {
	if s == nil || s.store == nil {
		return model.TodoItem{}, fmt.Errorf("service unavailable")
	}
	var err error
	input, err = control.NormalizeTodoCreateWorktreeAndStartEngineerInput(input)
	if err != nil {
		return model.TodoItem{}, err
	}
	plan, err := todocontrol.InspectLaunch(ctx, s.store, input)
	if err != nil {
		return model.TodoItem{}, err
	}
	if plan.Todo.LaunchRequestID == input.RequestID && input.RequestID != "" {
		return plan.Todo, nil
	}
	item, err := s.store.AddTodoForLaunchOwned(ctx, input, plan.OwnerPath)
	if err != nil {
		return model.TodoItem{}, err
	}
	s.queueSavedTodoWorktreeSuggestion(ctx, item.ProjectPath, item.ID)
	s.refreshProjectStatusAsync(item.ProjectPath)
	return item, nil
}
