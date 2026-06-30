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

func (s *Service) SetProjectCategoryPrivate(ctx context.Context, name string, private bool) (model.ProjectCategory, error) {
	category, err := s.store.SetProjectCategoryPrivate(ctx, name, private)
	if err != nil {
		return model.ProjectCategory{}, err
	}
	action := "project_category_marked_public"
	if category.Private {
		action = "project_category_marked_private"
	}
	s.publishCategoryAction(ctx, "", action, category)
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

func (s *Service) MoveResourcesToCategory(ctx context.Context, resources []model.CategoryResourceRef, categoryName string) (model.ProjectCategory, int, error) {
	if len(resources) == 0 {
		return model.ProjectCategory{}, 0, fmt.Errorf("no items selected")
	}
	var category model.ProjectCategory
	var err error
	categoryName = strings.TrimSpace(categoryName)
	if categoryName != "" {
		category, err = s.store.GetProjectCategoryByName(ctx, categoryName)
		if err != nil {
			return model.ProjectCategory{}, 0, err
		}
	}
	moved := 0
	seen := map[model.CategoryResourceRef]struct{}{}
	projectSummaries := map[string]model.ProjectSummary{}
	for _, resource := range resources {
		resource.Kind = model.NormalizeCategoryResourceKind(resource.Kind)
		resource.ID = strings.TrimSpace(resource.ID)
		if resource.Kind == "" || resource.ID == "" {
			continue
		}
		if resource.Kind == model.CategoryResourceProject {
			resource.ID = filepath.Clean(resource.ID)
			if resource.ID == "." {
				continue
			}
		}
		if _, ok := seen[resource]; ok {
			continue
		}
		seen[resource] = struct{}{}
		switch resource.Kind {
		case model.CategoryResourceProject:
			if len(projectSummaries) == 0 {
				projectSummaries, err = s.store.GetProjectSummaryMap(ctx)
				if err != nil {
					return model.ProjectCategory{}, moved, err
				}
			}
			project := projectSummaries[resource.ID]
			if strings.TrimSpace(project.Path) == "" {
				return model.ProjectCategory{}, moved, fmt.Errorf("project not found: %s", resource.ID)
			}
			if err := s.store.SetResourceCategory(ctx, model.CategoryResourceProject, resource.ID, category.ID); err != nil {
				return model.ProjectCategory{}, moved, err
			}
			if project.Archived {
				if err := s.store.SetProjectArchived(ctx, resource.ID, false); err != nil {
					return model.ProjectCategory{}, moved, err
				}
			}
			s.publishCategoryAction(ctx, resource.ID, "project_category_changed", category)
		case model.CategoryResourceAgentTask:
			task, err := s.store.GetAgentTask(ctx, resource.ID)
			if err != nil {
				return model.ProjectCategory{}, moved, err
			}
			if err := s.store.SetResourceCategory(ctx, model.CategoryResourceAgentTask, resource.ID, category.ID); err != nil {
				return model.ProjectCategory{}, moved, err
			}
			s.publishCategoryAction(ctx, task.WorkspacePath, "agent_task_category_changed", category)
		}
		moved++
	}
	if moved == 0 {
		return model.ProjectCategory{}, 0, fmt.Errorf("no valid items selected")
	}
	return category, moved, nil
}

func (s *Service) publishCategoryAction(ctx context.Context, projectPath, action string, category model.ProjectCategory) {
	now := time.Now()
	private := "false"
	if category.Private {
		private = "true"
	}
	payload := map[string]string{
		"action":           action,
		"category_id":      strings.TrimSpace(category.ID),
		"category":         strings.TrimSpace(category.Name),
		"category_private": private,
	}
	s.bus.Publish(events.Event{Type: events.ActionApplied, At: now, ProjectPath: projectPath, Payload: payload})
	_ = s.store.AddEvent(ctx, model.StoredEvent{At: now, ProjectPath: projectPath, Type: string(events.ActionApplied), Payload: action})
}
