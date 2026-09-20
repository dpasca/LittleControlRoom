package service

import (
	"context"
	"errors"
	"path/filepath"

	"lcroom/internal/model"
)

func (s *Service) settleStructuredTask(ctx context.Context, task model.AgentTask) (model.AgentTask, error) {
	s.repositoryMu.Lock()
	defer s.repositoryMu.Unlock()
	if !task.Workflow.Enabled {
		return task, nil
	}
	if task.Workflow.Phase != "working" && task.Workflow.Phase != "submitted" && task.Workflow.Phase != "canceled" {
		return task, nil
	}
	if !task.Workflow.Handoff {
		if s.repositoryEngineers == nil {
			return task, errors.New("worker state unavailable; structured result remains pending")
		}
		live := false
		for _, snapshot := range s.repositoryEngineers() {
			if snapshot.ProjectPath != task.WorkspacePath {
				continue
			}
			if snapshot.Busy || snapshot.BusyExternal || snapshot.ActiveTurnID != "" || snapshot.PendingApproval != nil || snapshot.PendingToolInput != nil {
				return task, errors.New("worker is still active; structured result remains pending")
			}
			if snapshot.ThreadID == task.SessionID && task.SessionID != "" {
				live = true
			}
		}
		if !live {
			return task, errors.New("worker is unavailable; reopen its transcript to verify the handoff")
		}
	}
	if err := s.releaseTaskRepositoryLocked(ctx, task.ID); err != nil {
		return task, err
	}
	current, err := s.store.GetAgentTask(ctx, task.ID)
	if err != nil {
		return task, err
	}
	return s.store.FinalizeStructuredTask(ctx, current)
}

func (s *Service) GetAgentTaskResult(ctx context.Context, taskID string, revision int64) (map[string]any, error) {
	return s.store.GetAgentTaskResult(ctx, taskID, revision)
}

// Called only by explicit host stop/close actions. Late claims remain inspectable
// but cannot wake the worker or its original caller after a stop.
func (s *Service) StopStructuredTasksForEngineer(ctx context.Context, path string, provider model.SessionSource, key, sessionID string) error {
	s.repositoryMu.Lock()
	defer s.repositoryMu.Unlock()
	tasks, err := s.store.ListAgentTasks(ctx, model.AgentTaskFilter{})
	if err != nil {
		return err
	}
	for _, task := range tasks {
		if !task.Workflow.Enabled {
			continue
		}
		worker := filepath.Clean(task.WorkspacePath) == filepath.Clean(path) && task.Provider == provider
		origin := firstNonEmpty(task.OriginWorktreePath, task.OriginProjectPath)
		caller := filepath.Clean(origin) == filepath.Clean(path) && task.OriginProvider == provider && ((key != "" && key == task.OriginSessionKey) || (sessionID != "" && sessionID == task.OriginSessionID))
		if worker || caller {
			if err := s.store.StopStructuredTask(ctx, task, caller && !worker); err != nil {
				return err
			}
		}
	}
	return nil
}
