package service

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"lcroom/internal/appfs"
	"lcroom/internal/control"
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
	if input.CategoryID != "" {
		if err := s.store.SetResourceCategory(ctx, model.CategoryResourceAgentTask, task.ID, input.CategoryID); err != nil {
			_ = s.store.DeleteAgentTask(ctx, task.ID)
			if createdWorkspace != "" {
				_ = os.RemoveAll(createdWorkspace)
			}
			return model.AgentTask{}, err
		}
		task, err = s.store.GetAgentTask(ctx, task.ID)
		if err != nil {
			return model.AgentTask{}, err
		}
	}
	return task, nil
}

func (s *Service) ListOpenAgentTasks(ctx context.Context, limit int) ([]model.AgentTask, error) {
	_, _ = s.PurgeExpiredAgentTasks(ctx, time.Now())
	tasks, err := s.store.ListAgentTasks(ctx, model.AgentTaskFilter{
		Statuses: []model.AgentTaskStatus{
			model.AgentTaskStatusActive,
			model.AgentTaskStatusWaiting,
		},
		Limit: limit,
	})
	if err != nil {
		return nil, err
	}
	for i, task := range tasks {
		updated, _ := s.QueueAgentTaskResultCallback(ctx, task.ID)
		if strings.TrimSpace(updated.ID) != "" {
			tasks[i] = updated
		}
	}
	return tasks, nil
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
	status := model.AgentTaskStatusActive
	empty := ""
	zeroTime := time.Time{}
	return s.store.UpdateAgentTask(ctx, model.UpdateAgentTaskInput{
		ID:                  taskID,
		Status:              &status,
		Provider:            &provider,
		SessionID:           &sessionID,
		ResultMessageID:     &empty,
		ResultReadyAt:       &zeroTime,
		ResultDeliveredAt:   &zeroTime,
		ResultDeliveryError: &empty,
		ResultConsumedAt:    &zeroTime,
		ResultConsumedBy:    &empty,
		Resources:           resources,
		ReplaceResources:    true,
		Touch:               true,
	})
}

func (s *Service) CompleteAgentTask(ctx context.Context, taskID, summary string) (model.AgentTask, error) {
	status := model.AgentTaskStatusCompleted
	completedAt := time.Now()
	summary = strings.TrimSpace(summary)
	input := model.UpdateAgentTaskInput{
		ID:          taskID,
		Status:      &status,
		Summary:     &summary,
		CompletedAt: &completedAt,
		Touch:       true,
	}
	if task, err := s.store.GetAgentTask(ctx, taskID); err == nil && !task.ResultReadyAt.IsZero() && task.ResultConsumedAt.IsZero() {
		consumedBy := "operator"
		input.ResultConsumedAt = &completedAt
		input.ResultConsumedBy = &consumedBy
	}
	return s.store.UpdateAgentTask(ctx, input)
}

func (s *Service) MarkAgentTaskReadyForReview(ctx context.Context, taskID, summary string) (model.AgentTask, error) {
	status := model.AgentTaskStatusWaiting
	readyAt := time.Now()
	summary = strings.TrimSpace(summary)
	return s.store.UpdateAgentTask(ctx, model.UpdateAgentTaskInput{
		ID:            taskID,
		Status:        &status,
		Summary:       &summary,
		ResultReadyAt: &readyAt,
		Touch:         true,
	})
}

func (s *Service) QueueAgentTaskResultCallback(ctx context.Context, taskID string) (model.AgentTask, error) {
	task, err := s.store.GetAgentTask(ctx, taskID)
	if err != nil {
		return model.AgentTask{}, err
	}
	provider := agentTaskResultCallbackProvider(task.OriginProvider)
	projectPath := firstNonEmpty(task.OriginWorktreePath, task.OriginProjectPath)
	if task.ResultReadyAt.IsZero() || !task.ResultConsumedAt.IsZero() || strings.TrimSpace(task.ResultMessageID) != "" ||
		provider == "" || strings.TrimSpace(projectPath) == "" || strings.TrimSpace(task.OriginSessionID) == "" {
		return task, nil
	}
	message := control.EngineerMessage{
		OperationID:     fmt.Sprintf("agent-task-result:%s:%d", strings.TrimSpace(task.ID), task.ResultReadyAt.Unix()),
		AgentTaskID:     task.ID,
		ProjectPath:     projectPath,
		Provider:        provider,
		SessionMode:     control.SessionModeResumeOrNew,
		TargetSessionID: task.OriginSessionID,
		Prompt:          agentTaskResultCallbackPrompt(task),
		State:           control.EngineerMessageQueued,
	}
	if _, err := s.store.CreateEngineerMessage(ctx, message); err != nil {
		deliveryError := err.Error()
		updated, updateErr := s.store.UpdateAgentTask(ctx, model.UpdateAgentTaskInput{
			ID:                  task.ID,
			ResultDeliveryError: &deliveryError,
			Touch:               true,
		})
		if updateErr != nil {
			return task, fmt.Errorf("queue result callback: %v; persist callback failure: %w", err, updateErr)
		}
		return updated, fmt.Errorf("queue result callback: %w", err)
	}
	if strings.TrimSpace(task.ResultDeliveryError) != "" {
		empty := ""
		return s.store.UpdateAgentTask(ctx, model.UpdateAgentTaskInput{
			ID:                  task.ID,
			ResultDeliveryError: &empty,
			Touch:               true,
		})
	}
	return s.store.GetAgentTask(ctx, task.ID)
}

func agentTaskResultCallbackProvider(source model.SessionSource) control.Provider {
	switch model.NormalizeSessionSource(source) {
	case model.SessionSourceCodex:
		return control.ProviderCodex
	case model.SessionSourceOpenCode:
		return control.ProviderOpenCode
	case model.SessionSourceClaudeCode:
		return control.ProviderClaudeCode
	case model.SessionSourceLCAgent:
		return control.ProviderLCAgent
	default:
		return ""
	}
}

func agentTaskResultCallbackPrompt(task model.AgentTask) string {
	lines := []string{
		"A delegated Little Control Room task you created is ready for review.",
		"Task ID: " + strings.TrimSpace(task.ID),
		"Title: " + strings.TrimSpace(task.Title),
	}
	if summary := strings.TrimSpace(task.Summary); summary != "" {
		lines = append(lines, "", "Worker result:", summary)
	}
	lines = append(lines,
		"",
		"Inspect the durable task with work.agent_task_get before relying on this summary. Verify and integrate the result in the originating project/worktree. If the result is accepted, propose agent_task.close with status archived and close_session=true; that records consumption before deferred workspace cleanup. If more work is needed, use agent_task.continue instead.",
	)
	return strings.Join(lines, "\n")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func (s *Service) ConsumeAgentTaskResult(ctx context.Context, taskID, consumedBy string) (model.AgentTask, error) {
	task, err := s.store.GetAgentTask(ctx, taskID)
	if err != nil {
		return model.AgentTask{}, err
	}
	if task.ResultReadyAt.IsZero() || !task.ResultConsumedAt.IsZero() {
		return task, nil
	}
	consumedAt := time.Now()
	consumedBy = strings.TrimSpace(consumedBy)
	if consumedBy == "" {
		consumedBy = "operator"
	}
	return s.store.UpdateAgentTask(ctx, model.UpdateAgentTaskInput{
		ID:               taskID,
		ResultConsumedAt: &consumedAt,
		ResultConsumedBy: &consumedBy,
		Touch:            true,
	})
}

func (s *Service) ArchiveAgentTask(ctx context.Context, taskID string) (model.AgentTask, error) {
	if _, err := s.ConsumeAgentTaskResult(ctx, taskID, "operator"); err != nil {
		return model.AgentTask{}, err
	}
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
