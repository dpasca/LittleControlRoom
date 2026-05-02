package service

import (
	"context"
	"os"
	"strings"
	"time"

	"lcroom/internal/appfs"
	"lcroom/internal/model"
)

const (
	agentTaskWorkspacePrefix = "lcroom-agent-task-"
	archivedAgentTaskTTL     = 30 * 24 * time.Hour
)

func (s *Service) CreateAgentTask(ctx context.Context, input model.CreateAgentTaskInput) (model.AgentTask, error) {
	input.Kind = model.NormalizeAgentTaskKind(input.Kind)
	createdWorkspace := ""
	if input.WorkspacePath == "" && agentTaskNeedsWorkspace(input.Kind) {
		workspace, err := appfs.CreateInternalWorkspace(s.cfg.DataDir, agentTaskWorkspacePrefix)
		if err != nil {
			return model.AgentTask{}, err
		}
		input.WorkspacePath = workspace
		createdWorkspace = workspace
	}
	task, err := s.store.CreateAgentTask(ctx, input)
	if err != nil {
		if createdWorkspace != "" {
			_ = os.RemoveAll(createdWorkspace)
		}
		return model.AgentTask{}, err
	}
	return task, nil
}

func (s *Service) ListOpenAgentTasks(ctx context.Context, limit int) ([]model.AgentTask, error) {
	_, _ = s.PurgeExpiredAgentTasks(ctx, time.Now())
	return s.store.ListAgentTasks(ctx, model.AgentTaskFilter{
		Statuses: []model.AgentTaskStatus{
			model.AgentTaskStatusActive,
			model.AgentTaskStatusWaiting,
		},
		Limit: limit,
	})
}

func (s *Service) PurgeExpiredAgentTasks(ctx context.Context, now time.Time) (int, error) {
	tasks, err := s.store.ListExpiredAgentTasks(ctx, now)
	if err != nil {
		return 0, err
	}
	managedRoots := []string{appfs.InternalWorkspaceRoot(s.cfg.DataDir)}
	purged := 0
	for _, task := range tasks {
		workspace := strings.TrimSpace(task.WorkspacePath)
		if workspace != "" && appfs.IsManagedInternalPath(workspace, managedRoots) {
			if err := os.RemoveAll(workspace); err != nil {
				return purged, err
			}
		}
		if err := s.store.DeleteAgentTask(ctx, task.ID); err != nil {
			return purged, err
		}
		purged++
	}
	return purged, nil
}

func (s *Service) GetAgentTask(ctx context.Context, taskID string) (model.AgentTask, error) {
	return s.store.GetAgentTask(ctx, taskID)
}

func (s *Service) AttachAgentTaskEngineerSession(ctx context.Context, taskID string, provider model.SessionSource, sessionID string) (model.AgentTask, error) {
	task, err := s.store.GetAgentTask(ctx, taskID)
	if err != nil {
		return model.AgentTask{}, err
	}
	provider = model.NormalizeSessionSource(provider)
	sessionID = strings.TrimSpace(sessionID)
	resources := append([]model.AgentTaskResource(nil), task.Resources...)
	replaced := false
	for i, resource := range resources {
		if model.NormalizeAgentTaskResourceKind(resource.Kind) != model.AgentTaskResourceEngineerSession {
			continue
		}
		if model.NormalizeSessionSource(resource.Provider) == provider {
			resources[i].SessionID = sessionID
			resources[i].Provider = provider
			replaced = true
			break
		}
	}
	if !replaced && sessionID != "" {
		resources = append(resources, model.AgentTaskResource{
			Kind:      model.AgentTaskResourceEngineerSession,
			Provider:  provider,
			SessionID: sessionID,
			Label:     "current engineer session",
		})
	}
	return s.store.UpdateAgentTask(ctx, model.UpdateAgentTaskInput{
		ID:               taskID,
		Provider:         &provider,
		SessionID:        &sessionID,
		Resources:        resources,
		ReplaceResources: true,
		Touch:            true,
	})
}

func (s *Service) CompleteAgentTask(ctx context.Context, taskID, summary string) (model.AgentTask, error) {
	status := model.AgentTaskStatusCompleted
	completedAt := time.Now()
	summary = strings.TrimSpace(summary)
	return s.store.UpdateAgentTask(ctx, model.UpdateAgentTaskInput{
		ID:          taskID,
		Status:      &status,
		Summary:     &summary,
		CompletedAt: &completedAt,
		Touch:       true,
	})
}

func (s *Service) ArchiveAgentTask(ctx context.Context, taskID string) (model.AgentTask, error) {
	status := model.AgentTaskStatusArchived
	archivedAt := time.Now()
	expiresAt := archivedAt.Add(archivedAgentTaskTTL)
	return s.store.UpdateAgentTask(ctx, model.UpdateAgentTaskInput{
		ID:         taskID,
		Status:     &status,
		ArchivedAt: &archivedAt,
		ExpiresAt:  &expiresAt,
		Touch:      true,
	})
}

func agentTaskNeedsWorkspace(kind model.AgentTaskKind) bool {
	switch model.NormalizeAgentTaskKind(kind) {
	case model.AgentTaskKindAgent, model.AgentTaskKindSubagent:
		return true
	default:
		return false
	}
}
