package service

import (
	"context"
	"fmt"
	"lcroom/internal/events"
	"lcroom/internal/model"
	"path/filepath"
	"strings"
	"time"
)

func (s *Service) ListProjectCategories(ctx context.Context) ([]model.ProjectCategory, error) {
	return s.store.ListProjectCategories(ctx)
}

func (s *Service) CreateProjectCategory(ctx context.Context, name string) (model.ProjectCategory, error) {
	category, err := s.store.CreateProjectCategory(ctx, name)
	if err != nil {
		return model.ProjectCategory{}, err
	}
	s.publishCategoryAction(ctx, "", "project_category_created", category)
	return category, nil
}

func (s *Service) DeleteProjectCategory(ctx context.Context, name string) (model.ProjectCategory, error) {
	category, err := s.store.DeleteProjectCategory(ctx, name)
	if err != nil {
		return model.ProjectCategory{}, err
	}
	s.publishCategoryAction(ctx, "", "project_category_deleted", category)
	return category, nil
}

func (s *Service) MoveProjectToCategory(ctx context.Context, projectPath, categoryName string) (model.ProjectCategory, error) {
	projectPath = filepath.Clean(strings.TrimSpace(projectPath))
	if projectPath == "" || projectPath == "." {
		return model.ProjectCategory{}, fmt.Errorf("project path is required")
	}
	unlockProjectState, err := s.lockProjectStateMutationContext(ctx, projectPath)
	if err != nil {
		return model.ProjectCategory{}, err
	}
	defer unlockProjectState()

	projects, err := s.store.GetProjectSummaryMap(ctx)
	if err != nil {
		return model.ProjectCategory{}, err
	}
	project := projects[projectPath]
	if project.Path == "" {
		return model.ProjectCategory{}, fmt.Errorf("project not found: %s", projectPath)
	}

	var category model.ProjectCategory
	categoryName = strings.TrimSpace(categoryName)
	if categoryName != "" {
		category, err = s.store.GetProjectCategoryByName(ctx, categoryName)
		if err != nil {
			return model.ProjectCategory{}, err
		}
	}
	if err := s.store.SetResourceCategory(ctx, model.CategoryResourceProject, projectPath, category.ID); err != nil {
		return model.ProjectCategory{}, err
	}
	if project.Archived {
		if err := s.store.SetProjectArchived(ctx, projectPath, false); err != nil {
			return model.ProjectCategory{}, err
		}
	}
	s.publishCategoryAction(ctx, projectPath, "project_category_changed", category)
	return category, nil
}

func (s *Service) MoveAgentTaskToCategory(ctx context.Context, taskID, categoryName string) (model.ProjectCategory, error) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return model.ProjectCategory{}, fmt.Errorf("agent task id is required")
	}
	task, err := s.store.GetAgentTask(ctx, taskID)
	if err != nil {
		return model.ProjectCategory{}, err
	}
	var category model.ProjectCategory
	categoryName = strings.TrimSpace(categoryName)
	if categoryName != "" {
		category, err = s.store.GetProjectCategoryByName(ctx, categoryName)
		if err != nil {
			return model.ProjectCategory{}, err
		}
	}
	if err := s.store.SetResourceCategory(ctx, model.CategoryResourceAgentTask, taskID, category.ID); err != nil {
		return model.ProjectCategory{}, err
	}
	s.publishCategoryAction(ctx, task.WorkspacePath, "agent_task_category_changed", category)
	return category, nil
}

func (s *Service) publishCategoryAction(ctx context.Context, projectPath, action string, category model.ProjectCategory) {
	now := time.Now()
	payload := map[string]string{
		"action":      action,
		"category_id": strings.TrimSpace(category.ID),
		"category":    strings.TrimSpace(category.Name),
	}
	s.bus.Publish(events.Event{Type: events.ActionApplied, At: now, ProjectPath: projectPath, Payload: payload})
	_ = s.store.AddEvent(ctx, model.StoredEvent{At: now, ProjectPath: projectPath, Type: string(events.ActionApplied), Payload: action})
}
