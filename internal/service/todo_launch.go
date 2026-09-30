package service

import (
	"context"
	"lcroom/internal/control"
	"lcroom/internal/model"
)

func (s *Service) AddTodoForLaunch(ctx context.Context, input control.TodoCreateWorktreeAndStartEngineerInput) (model.TodoItem, error) {
	item, err := s.store.AddTodoForLaunch(ctx, input)
	if err != nil {
		return model.TodoItem{}, err
	}
	s.queueSavedTodoWorktreeSuggestion(ctx, input.ProjectPath, item.ID)
	s.refreshProjectStatusAsync(input.ProjectPath)
	return item, nil
}
