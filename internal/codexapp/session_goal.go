package codexapp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

func (s *appServerSession) ShowStatus() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return fmt.Errorf("codex session is closed")
	}
	threadID := strings.TrimSpace(s.threadID)
	projectPath := s.projectPath
	currentCWD := firstNonEmpty(strings.TrimSpace(s.currentCWD), projectPath)
	model := strings.TrimSpace(s.model)
	modelProvider := strings.TrimSpace(s.modelProvider)
	reasoningEffort := strings.TrimSpace(s.reasoningEffort)
	serviceTier := strings.TrimSpace(s.serviceTier)
	approvalPolicy := append(json.RawMessage(nil), s.approvalPolicy...)
	sandboxPolicy := append(json.RawMessage(nil), s.sandboxPolicy...)
	tokenUsage := cloneThreadTokenUsage(s.tokenUsage)
	rateLimits := cloneRateLimitSnapshot(s.rateLimits)
	rateLimitsByID := cloneRateLimitSnapshotMap(s.rateLimitsByID)
	s.touchLocked()
	s.status = "Reading embedded Codex status..."
	s.mu.Unlock()
	s.notify()

	if refreshed, byID, err := s.readRateLimits(); err == nil {
		rateLimits = cloneRateLimitSnapshot(refreshed)
		rateLimitsByID = cloneRateLimitSnapshotMap(byID)
		s.mu.Lock()
		s.rateLimits = cloneRateLimitSnapshot(refreshed)
		s.rateLimitsByID = cloneRateLimitSnapshotMap(byID)
		s.mu.Unlock()
	} else if rateLimits == nil && len(rateLimitsByID) == 0 {
		s.mu.Lock()
		s.lastSystemNotice = "Embedded Codex status could not refresh rate limits: " + err.Error()
		s.mu.Unlock()
	}

	statusText := buildEmbeddedStatusText(
		threadID,
		projectPath,
		currentCWD,
		model,
		modelProvider,
		reasoningEffort,
		serviceTier,
		approvalPolicy,
		sandboxPolicy,
		tokenUsage,
		rateLimits,
		rateLimitsByID,
	)

	s.mu.Lock()
	s.touchLocked()
	s.appendEntryLocked("", TranscriptStatus, statusText)
	s.lastSystemNotice = "Displayed embedded Codex status"
	s.status = "Displayed embedded Codex status"
	s.mu.Unlock()
	s.notify()
	return nil
}

func (s *appServerSession) ShowGoal() error {
	goal, err := s.readAndStoreGoal()
	if err != nil {
		return err
	}

	statusText := buildEmbeddedGoalStatusText(goal)
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return fmt.Errorf("codex session is closed")
	}
	s.touchLocked()
	s.appendEntryLocked("", TranscriptStatus, statusText)
	s.lastSystemNotice = "Displayed embedded Codex goal"
	s.status = "Displayed embedded Codex goal"
	s.mu.Unlock()
	s.notify()
	return nil
}

func (s *appServerSession) SetGoal(objective string, tokenBudget *int64) error {
	objective = strings.TrimSpace(objective)
	if objective == "" {
		return fmt.Errorf("goal objective required")
	}
	if tokenBudget != nil && *tokenBudget <= 0 {
		return fmt.Errorf("goal token budget must be greater than zero")
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return fmt.Errorf("codex session is closed")
	}
	if s.compacting {
		s.mu.Unlock()
		return fmt.Errorf("cannot update the goal while conversation compaction is in progress")
	}
	if s.busy {
		s.mu.Unlock()
		return fmt.Errorf("cannot update the goal while a turn is in progress")
	}
	threadID := strings.TrimSpace(s.threadID)
	if threadID == "" {
		s.mu.Unlock()
		return fmt.Errorf("no active thread for goal")
	}
	s.touchLocked()
	s.status = "Setting embedded Codex goal..."
	s.mu.Unlock()
	s.notify()

	ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
	defer cancel()

	goal, err := s.setThreadGoalReplacingStale(ctx, threadID, objective, tokenBudget)
	if err != nil {
		s.appendSystemError(err)
		return err
	}
	if goal == nil {
		goal = &ThreadGoal{
			ThreadID:    threadID,
			Objective:   objective,
			Status:      ThreadGoalStatusActive,
			TokenBudget: cloneInt64Ptr(tokenBudget),
		}
	}

	s.mu.Lock()
	s.goal = cloneThreadGoal(goal)
	s.touchLocked()
	s.appendEntryLocked("", TranscriptStatus, buildEmbeddedGoalSetText(goal, objective))
	s.lastSystemNotice = "Set embedded Codex goal"
	s.status = "Set embedded Codex goal"
	s.mu.Unlock()
	s.notify()
	return nil
}

func (s *appServerSession) reactivateThreadGoal(ctx context.Context, threadID string, previous *ThreadGoal) error {
	if previous == nil {
		return nil
	}
	objective := strings.TrimSpace(previous.Objective)
	if objective == "" {
		return nil
	}
	goal, err := s.setThreadGoalReplacingStale(ctx, threadID, objective, previous.TokenBudget)
	if err != nil {
		return err
	}
	if goal == nil {
		goal = &ThreadGoal{
			ThreadID:    threadID,
			Objective:   objective,
			Status:      ThreadGoalStatusActive,
			TokenBudget: cloneInt64Ptr(previous.TokenBudget),
		}
	}
	s.mu.Lock()
	if current := strings.TrimSpace(s.threadID); current == "" || strings.TrimSpace(threadID) == "" || current == strings.TrimSpace(threadID) {
		s.goal = cloneThreadGoal(goal)
		s.touchLocked()
		s.lastSystemNotice = "Embedded Codex goal resumed"
		s.status = "Embedded Codex goal resumed"
	}
	s.mu.Unlock()
	s.notify()
	return nil
}

func (s *appServerSession) PauseGoal() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return fmt.Errorf("codex session is closed")
	}
	if s.compacting {
		s.mu.Unlock()
		return fmt.Errorf("cannot pause the goal while conversation compaction is in progress")
	}
	if s.busyExternal {
		s.mu.Unlock()
		return fmt.Errorf("cannot pause the goal while this Codex session is busy in another process")
	}
	threadID := strings.TrimSpace(s.threadID)
	turnID := strings.TrimSpace(s.activeTurnID)
	goal := cloneThreadGoal(s.goal)
	if threadID == "" {
		s.mu.Unlock()
		return fmt.Errorf("no active thread for goal")
	}
	if goal == nil || strings.TrimSpace(goal.Objective) == "" {
		s.mu.Unlock()
		return fmt.Errorf("no embedded Codex goal to pause")
	}
	s.touchLocked()
	s.status = "Pausing embedded Codex goal..."
	s.mu.Unlock()
	s.notify()

	ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
	defer cancel()
	if err := s.pauseGoalForManualPrompt(ctx, threadID, turnID, goal); err != nil {
		s.appendSystemError(err)
		return err
	}
	return nil
}

func (s *appServerSession) ResumeGoal() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return fmt.Errorf("codex session is closed")
	}
	if s.compacting {
		s.mu.Unlock()
		return fmt.Errorf("cannot resume the goal while conversation compaction is in progress")
	}
	if s.busy {
		s.mu.Unlock()
		return fmt.Errorf("cannot resume the goal while a turn is in progress")
	}
	threadID := strings.TrimSpace(s.threadID)
	goal := cloneThreadGoal(s.goal)
	if threadID == "" {
		s.mu.Unlock()
		return fmt.Errorf("no active thread for goal")
	}
	s.touchLocked()
	s.status = "Resuming embedded Codex goal..."
	s.mu.Unlock()
	s.notify()

	ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
	defer cancel()
	if goal == nil {
		var err error
		goal, err = s.readThreadGoal(ctx, threadID)
		if err != nil {
			s.appendSystemError(err)
			return err
		}
	}
	if goal == nil || strings.TrimSpace(goal.Objective) == "" {
		err := fmt.Errorf("no embedded Codex goal to resume")
		s.appendSystemError(err)
		return err
	}
	updated, err := s.setThreadGoalStatus(ctx, threadID, ThreadGoalStatusActive, goal)
	if err != nil {
		s.appendSystemError(err)
		return err
	}
	if updated == nil {
		updated = &ThreadGoal{
			ThreadID:    threadID,
			Objective:   strings.TrimSpace(goal.Objective),
			Status:      ThreadGoalStatusActive,
			TokenBudget: cloneInt64Ptr(goal.TokenBudget),
		}
	}

	s.mu.Lock()
	s.goal = cloneThreadGoal(updated)
	s.touchLocked()
	s.appendEntryLocked("", TranscriptStatus, "Embedded Codex goal resumed")
	s.lastSystemNotice = "Embedded Codex goal resumed"
	s.status = "Embedded Codex goal resumed"
	s.mu.Unlock()
	s.notify()
	return nil
}

func (s *appServerSession) setThreadGoalReplacingStale(ctx context.Context, threadID, objective string, tokenBudget *int64) (*ThreadGoal, error) {
	goal, err := s.setThreadGoal(ctx, threadID, objective, tokenBudget)
	if err != nil {
		return nil, err
	}
	if !threadGoalSetResponseStale(goal, objective, tokenBudget) {
		return goal, nil
	}
	if _, err := s.clearThreadGoal(ctx, threadID); err != nil {
		return nil, err
	}
	goal, err = s.setThreadGoal(ctx, threadID, objective, tokenBudget)
	if err != nil {
		return nil, err
	}
	if threadGoalSetResponseStale(goal, objective, tokenBudget) {
		return nil, fmt.Errorf("embedded Codex goal did not update; Codex returned %s", threadGoalSummary(goal))
	}
	return goal, nil
}

func (s *appServerSession) ClearGoal() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return fmt.Errorf("codex session is closed")
	}
	if s.compacting {
		s.mu.Unlock()
		return fmt.Errorf("cannot clear the goal while conversation compaction is in progress")
	}
	if s.busyExternal {
		s.mu.Unlock()
		return fmt.Errorf("cannot clear the goal while this Codex session is busy in another process")
	}
	threadID := strings.TrimSpace(s.threadID)
	if threadID == "" {
		s.mu.Unlock()
		return fmt.Errorf("no active thread for goal")
	}
	busy := s.busy
	turnID := strings.TrimSpace(s.activeTurnID)
	s.touchLocked()
	if busy {
		s.status = "Stopping embedded Codex goal..."
	} else {
		s.status = "Clearing embedded Codex goal..."
	}
	s.mu.Unlock()
	s.notify()

	ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
	defer cancel()

	var interruptErr error
	if busy && turnID == "" {
		thread, err := s.readThreadState(ctx, threadID)
		if err != nil {
			interruptErr = err
		} else {
			turnID = activeTurnIDFromThread(thread)
			status := effectiveThreadStatus(thread)
			s.mu.Lock()
			s.syncThreadStatusLocked(threadID, status, true)
			s.mu.Unlock()
			s.notify()
		}
	}
	clearCtx := ctx
	var clearCancel context.CancelFunc
	if ctx.Err() != nil {
		clearCtx, clearCancel = context.WithTimeout(context.Background(), rpcTimeout)
		defer clearCancel()
	}
	response, err := s.clearThreadGoal(clearCtx, threadID)
	if err != nil {
		if interruptErr != nil {
			err = fmt.Errorf("checking active turn failed: %v; clearing embedded Codex goal failed: %w", interruptErr, err)
		}
		s.appendSystemError(err)
		return err
	}
	if busy {
		if turnID != "" {
			if _, err := s.call(ctx, "turn/interrupt", turnInterruptParams{
				ThreadID: threadID,
				TurnID:   turnID,
			}); err != nil {
				interruptErr = err
			} else if err := s.waitForThreadIdleAfterInterrupt(ctx, threadID); err != nil {
				interruptErr = err
			}
		}
	}

	s.mu.Lock()
	s.goal = nil
	s.touchLocked()
	text := "Embedded Codex goal cleared"
	if busy {
		text = "Embedded Codex goal stopped"
	}
	if !response.Cleared {
		text = "No embedded Codex goal was active"
	}
	if interruptErr != nil && response.Cleared {
		text += "; turn interrupt failed"
	}
	s.appendEntryLocked("", TranscriptStatus, text)
	s.lastSystemNotice = text
	s.status = text
	s.mu.Unlock()
	s.notify()
	return nil
}

func (s *appServerSession) setThreadGoal(ctx context.Context, threadID, objective string, tokenBudget *int64) (*ThreadGoal, error) {
	result, err := s.call(ctx, "thread/goal/set", threadGoalSetParams{
		ThreadID:    threadID,
		Objective:   objective,
		Status:      ThreadGoalStatusActive,
		TokenBudget: cloneInt64Ptr(tokenBudget),
	})
	if err != nil {
		return nil, err
	}
	var response threadGoalResponse
	if err := json.Unmarshal(result, &response); err != nil {
		return nil, err
	}
	return exportedThreadGoal(response.Goal), nil
}

func (s *appServerSession) setThreadGoalStatus(ctx context.Context, threadID string, status ThreadGoalStatus, previous *ThreadGoal) (*ThreadGoal, error) {
	params := threadGoalSetParams{
		ThreadID: strings.TrimSpace(threadID),
		Status:   status,
	}
	if previous != nil {
		params.Objective = strings.TrimSpace(previous.Objective)
		params.TokenBudget = cloneInt64Ptr(previous.TokenBudget)
	}
	result, err := s.call(ctx, "thread/goal/set", params)
	if err != nil {
		return nil, err
	}
	var response threadGoalResponse
	if err := json.Unmarshal(result, &response); err != nil {
		return nil, err
	}
	return exportedThreadGoal(response.Goal), nil
}

func (s *appServerSession) pauseGoalForManualPrompt(ctx context.Context, threadID, turnID string, goal *ThreadGoal) error {
	threadID = strings.TrimSpace(threadID)
	turnID = strings.TrimSpace(turnID)
	if threadID == "" {
		return fmt.Errorf("no active thread for goal")
	}
	var interruptErr error
	if turnID != "" {
		_, interruptErr = s.call(ctx, "turn/interrupt", turnInterruptParams{
			ThreadID: threadID,
			TurnID:   turnID,
		})
	}
	updated, err := s.setThreadGoalStatus(ctx, threadID, ThreadGoalStatusPaused, goal)
	if err != nil {
		if interruptErr != nil {
			return fmt.Errorf("interrupting active turn failed: %v; pausing embedded Codex goal failed: %w", interruptErr, err)
		}
		return err
	}
	if updated == nil && goal != nil {
		updated = cloneThreadGoal(goal)
		updated.Status = ThreadGoalStatusPaused
	}

	s.mu.Lock()
	if current := strings.TrimSpace(s.threadID); current == "" || current == threadID {
		s.goal = cloneThreadGoal(updated)
		s.touchLocked()
		text := "Embedded Codex goal paused"
		if interruptErr != nil {
			text += "; turn interrupt failed"
		}
		s.appendEntryLocked("", TranscriptStatus, text)
		s.lastSystemNotice = text
		s.status = text
	}
	s.mu.Unlock()
	s.notify()
	if interruptErr != nil {
		return interruptErr
	}
	return nil
}

func (s *appServerSession) waitForThreadIdleAfterInterrupt(ctx context.Context, threadID string) error {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil
	}
	for {
		thread, err := s.readThreadState(ctx, threadID)
		if err != nil {
			return err
		}
		status := effectiveThreadStatus(thread)
		s.mu.Lock()
		s.syncThreadStatusLocked(threadID, status, true)
		s.mu.Unlock()
		s.notify()
		if activeTurnIDFromThread(thread) == "" {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (s *appServerSession) clearThreadGoal(ctx context.Context, threadID string) (threadGoalClearResponse, error) {
	result, err := s.call(ctx, "thread/goal/clear", threadGoalClearParams{ThreadID: threadID})
	if err != nil {
		return threadGoalClearResponse{}, err
	}
	var response threadGoalClearResponse
	if err := json.Unmarshal(result, &response); err != nil {
		return threadGoalClearResponse{}, err
	}
	return response, nil
}

func (s *appServerSession) readAndStoreGoal() (*ThreadGoal, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, fmt.Errorf("codex session is closed")
	}
	threadID := strings.TrimSpace(s.threadID)
	if threadID == "" {
		s.mu.Unlock()
		return nil, fmt.Errorf("no active thread for goal")
	}
	s.touchLocked()
	s.status = "Reading embedded Codex goal..."
	s.mu.Unlock()
	s.notify()

	ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
	defer cancel()
	goal, err := s.readThreadGoal(ctx, threadID)
	if err != nil {
		s.appendSystemError(err)
		return nil, err
	}

	s.mu.Lock()
	s.goal = cloneThreadGoal(goal)
	s.mu.Unlock()
	s.notify()
	return goal, nil
}

func (s *appServerSession) refreshGoalState(ctx context.Context, threadID string) error {
	goal, err := s.readThreadGoal(ctx, threadID)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.goal = cloneThreadGoal(goal)
	s.mu.Unlock()
	return nil
}

func (s *appServerSession) readThreadGoal(ctx context.Context, threadID string) (*ThreadGoal, error) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil, fmt.Errorf("thread id required")
	}
	result, err := s.call(ctx, "thread/goal/get", threadGoalGetParams{ThreadID: threadID})
	if err != nil {
		return nil, err
	}
	var response threadGoalResponse
	if err := json.Unmarshal(result, &response); err != nil {
		return nil, err
	}
	return exportedThreadGoal(response.Goal), nil
}
