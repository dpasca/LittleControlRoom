package service

import (
	"context"
	"errors"
	"path/filepath"

	"lcroom/internal/codexapp"
	"lcroom/internal/model"
)

func (s *Service) settleStructuredTask(ctx context.Context, task model.AgentTask) (model.AgentTask, error) {
	return s.settleStructuredTaskWithCompletion(ctx, task, nil)
}

// SettleAgentTaskEngineerTurn carries the host's terminal observation across an
// asynchronous handoff. Closing an already-finished pane must not discard that
// evidence. This is a host-only path, not worker-supplied completion evidence.
func (s *Service) SettleAgentTaskEngineerTurn(ctx context.Context, task model.AgentTask, completion codexapp.Snapshot) (model.AgentTask, error) {
	return s.settleStructuredTaskWithCompletion(ctx, task, &completion)
}

func (s *Service) settleStructuredTaskWithCompletion(ctx context.Context, expected model.AgentTask, completion *codexapp.Snapshot) (model.AgentTask, error) {
	s.repositoryMu.Lock()
	defer s.repositoryMu.Unlock()
	// Identity persistence, result submission and another admitted turn may all
	// have advanced since the UI or task list captured expected.
	task, err := s.store.GetAgentTask(ctx, expected.ID)
	if err != nil {
		return task, err
	}
	if task.Workflow.RunID != expected.Workflow.RunID || task.Workflow.WorkerKey != expected.Workflow.WorkerKey {
		return task, errors.New("task run changed; reload the task before retrying the handoff")
	}
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
		otherWorker := false
		var worker *codexapp.Snapshot
		for _, snapshot := range s.repositoryEngineers() {
			if filepath.Clean(snapshot.ProjectPath) != filepath.Clean(task.WorkspacePath) {
				continue
			}
			if agentTaskEngineerActive(snapshot) {
				return task, errors.New("worker is still active; structured result remains pending")
			}
			if agentTaskWorkerMatches(task, snapshot) {
				worker = &snapshot
			} else {
				otherWorker = true
			}
		}
		// A saved result alone does not prove completion. Only the exact run's
		// host-observed terminal state can replace a missing registry entry.
		if completion != nil {
			if !agentTaskWorkerMatches(task, *completion) {
				return task, errors.New("completion belongs to another worker; reload the task before retrying the handoff")
			}
			if worker == nil && !otherWorker && completion.Started && completion.LatestTurnStateKnown &&
				completion.LatestTurnCompleted && !agentTaskEngineerActive(*completion) {
				worker = completion
			}
		}
		if worker == nil {
			return task, errors.New("worker is unavailable; reopen its transcript to verify the handoff")
		}
		if task.SessionID == "" {
			// The host control key binds a late provider identity without ever
			// choosing the newest or an unrelated session in the workspace.
			if err := s.store.RecordAgentTaskEngineerSession(ctx, task.ID, task.Provider, worker.ThreadID); err != nil {
				return task, err
			}
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

func agentTaskWorkerMatches(task model.AgentTask, snapshot codexapp.Snapshot) bool {
	if filepath.Clean(snapshot.ProjectPath) != filepath.Clean(task.WorkspacePath) ||
		model.SessionSource(snapshot.Provider.Normalized()) != task.Provider || snapshot.ThreadID == "" {
		return false
	}
	if task.SessionID != "" {
		return snapshot.ThreadID == task.SessionID
	}
	return task.Workflow.WorkerKey != "" && snapshot.ControlSessionKey == task.Workflow.WorkerKey
}

func agentTaskEngineerActive(snapshot codexapp.Snapshot) bool {
	if snapshot.Busy || snapshot.BusyExternal || snapshot.Compacting || snapshot.ActiveTurnID != "" ||
		snapshot.PendingApproval != nil || snapshot.PendingToolInput != nil || snapshot.PendingElicitation != nil ||
		len(snapshot.BackgroundTasks) > 0 || (snapshot.Goal != nil && snapshot.Goal.Status == codexapp.ThreadGoalStatusActive) {
		return true
	}
	return snapshot.Phase != "" && snapshot.Phase != codexapp.SessionPhaseIdle && snapshot.Phase != codexapp.SessionPhaseClosed
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
