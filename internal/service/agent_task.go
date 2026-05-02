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
	return s.store.ListAgentTasks(ctx, model.AgentTaskFilter{
		Statuses: []model.AgentTaskStatus{
			model.AgentTaskStatusActive,
			model.AgentTaskStatusWaiting,
		},
		Limit: limit,
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
	case model.AgentTaskKindEphemeral, model.AgentTaskKindSystemOps:
		return true
	default:
		return false
	}
}
