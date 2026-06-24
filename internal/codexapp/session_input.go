package codexapp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

func (s *appServerSession) Submit(prompt string) error {
	return s.SubmitInput(Submission{Text: prompt})
}

func (s *appServerSession) SubmitInput(input Submission) error {
	input = normalizeSubmission(input)
	if input.Empty() {
		return nil
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return fmt.Errorf("codex session is closed")
	}
	if s.compacting {
		s.mu.Unlock()
		return fmt.Errorf("cannot send a prompt while conversation compaction is in progress")
	}
	threadID := s.threadID
	activeTurnID := s.activeTurnID
	busy := s.busy
	busyExternal := s.busyExternal
	refreshBusyBeforeSteer := busy && activeTurnID != "" && s.shouldRefreshBusyBeforeSteerLocked(time.Now())
	pendingModel := strings.TrimSpace(s.pendingModel)
	pendingReasoning := strings.TrimSpace(s.pendingReasoning)
	currentModel := strings.TrimSpace(s.model)
	currentReasoning := strings.TrimSpace(s.reasoningEffort)
	goalToPause := cloneThreadGoal(s.goal)
	pauseGoalForManualPrompt := busy && activeTurnID != "" && threadGoalShouldPauseOnManualPrompt(goalToPause)
	goalToResume := cloneThreadGoal(s.goal)
	if busy || !threadGoalShouldReactivateOnManualPrompt(goalToResume) {
		goalToResume = nil
	}
	if busyExternal {
		s.mu.Unlock()
		return fmt.Errorf("this Codex session is already busy in another process; Little Control Room is read-only until it finishes")
	}
	s.touchLocked()
	s.appendEntryWithDisplayLocked("", TranscriptUser, input.TranscriptText(), input.TranscriptDisplayText())
	switch {
	case pauseGoalForManualPrompt:
		s.status = "Pausing embedded Codex goal..."
	case !busy:
		s.status = "Sending prompt to Codex..."
	}
	s.mu.Unlock()
	s.notify()

	modelInput := augmentSubmissionWithRuntimeContext(input, s.runtimeManager, s.projectPath)

	if goalToResume != nil {
		goalCtx, goalCancel := context.WithTimeout(context.Background(), rpcTimeout)
		err := s.reactivateThreadGoal(goalCtx, threadID, goalToResume)
		goalCancel()
		if err != nil {
			s.appendSystemError(err)
			return err
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
	defer cancel()
	if err := s.ensurePlaywrightMCPReady(ctx); err != nil {
		s.appendSystemError(err)
		return err
	}

	if pauseGoalForManualPrompt {
		if err := s.pauseGoalForManualPrompt(ctx, threadID, activeTurnID, goalToPause); err != nil {
			s.appendSystemError(err)
			return err
		}
		if err := s.waitForThreadIdleAfterInterrupt(ctx, threadID); err != nil {
			s.appendSystemError(err)
			return err
		}
		return s.startTurnWithInput(ctx, threadID, modelInput, pendingModel, pendingReasoning, currentModel, currentReasoning)
	}

	if busy && activeTurnID != "" {
		return s.submitBusyInput(ctx, threadID, activeTurnID, modelInput, pendingModel, pendingReasoning, currentModel, currentReasoning, refreshBusyBeforeSteer)
	}

	return s.startTurnWithInput(ctx, threadID, modelInput, pendingModel, pendingReasoning, currentModel, currentReasoning)
}

func (s *appServerSession) submitBusyInput(ctx context.Context, threadID, activeTurnID string, input Submission, pendingModel, pendingReasoning, currentModel, currentReasoning string, refreshBusyBeforeSteer bool) error {
	if refreshBusyBeforeSteer {
		recoveredTurnID, threadIdle, recoveryErr := s.recoverSteerTarget(ctx, threadID)
		if recoveryErr == nil {
			switch {
			case threadIdle:
				return s.startTurnWithInput(ctx, threadID, input, pendingModel, pendingReasoning, currentModel, currentReasoning)
			case recoveredTurnID != "":
				activeTurnID = recoveredTurnID
			}
		} else if isBusyTurnLikelyStuckError(recoveryErr) {
			return recoveryErr
		}
	}

	turnID, err := s.steerTurn(ctx, threadID, activeTurnID, input)
	if err == nil {
		s.recordSteerSubmission(firstNonEmpty(turnID, activeTurnID))
		return nil
	}
	if !isRecoverableSteerError(err) {
		s.appendSystemError(err)
		return err
	}

	recoveredTurnID, threadIdle, recoveryErr := s.recoverSteerTarget(ctx, threadID)
	if recoveryErr != nil {
		if isBusyTurnLikelyStuckError(recoveryErr) {
			return recoveryErr
		}
		combined := fmt.Errorf("%s (and failed to refresh thread state: %v)", err.Error(), recoveryErr)
		s.appendSystemError(combined)
		return combined
	}

	switch {
	case threadIdle:
		return s.startTurnWithInput(ctx, threadID, input, pendingModel, pendingReasoning, currentModel, currentReasoning)
	case recoveredTurnID != "":
		turnID, err = s.steerTurn(ctx, threadID, recoveredTurnID, input)
		if err == nil {
			s.recordSteerSubmission(firstNonEmpty(turnID, recoveredTurnID))
			return nil
		}
	}

	s.appendSystemError(err)
	return err
}

func (s *appServerSession) ensurePlaywrightMCPReady(parent context.Context) error {
	if s == nil || !s.playwrightMCPExpected {
		return nil
	}

	s.mu.Lock()
	if s.playwrightMCPReady {
		s.mu.Unlock()
		return nil
	}
	s.status = "Waiting for Playwright tools..."
	s.mu.Unlock()
	s.notify()

	ctx := parent
	if _, hasDeadline := parent.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(parent, playwrightMCPReadyTimeout)
		defer cancel()
	}

	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()

	for {
		ready, err := s.readPlaywrightMCPReady(ctx)
		if err == nil && ready {
			s.mu.Lock()
			s.playwrightMCPReady = true
			s.mu.Unlock()
			return nil
		}

		s.mu.Lock()
		status := s.mcpServerStartup["playwright"]
		s.mu.Unlock()
		if status == mcpServerStartupStateFailed || status == mcpServerStartupStateCancelled {
			s.appendSystemNotice("Playwright MCP server did not become ready. Browser tools may need a retry.")
			return nil
		}

		select {
		case <-ctx.Done():
			s.appendSystemNotice("Playwright tools are still starting. The first browser request may need a retry.")
			return nil
		case <-ticker.C:
		}
	}
}

func (s *appServerSession) readPlaywrightMCPReady(ctx context.Context) (bool, error) {
	result, err := s.call(ctx, "mcpServerStatus/list", mcpServerStatusListParams{Detail: "toolsAndAuthOnly"})
	if err != nil {
		return false, err
	}
	var response mcpServerStatusListResponse
	if err := json.Unmarshal(result, &response); err != nil {
		return false, err
	}
	for _, entry := range response.Data {
		if strings.TrimSpace(entry.Name) != "playwright" {
			continue
		}
		return len(entry.Tools) > 0, nil
	}
	return false, nil
}

func (s *appServerSession) startTurnWithInput(ctx context.Context, threadID string, input Submission, pendingModel, pendingReasoning, currentModel, currentReasoning string) error {
	params := turnStartParams{
		ThreadID: threadID,
		Input:    encodeSubmissionInput(input),
	}
	if pendingModel != "" {
		params.Model = pendingModel
	}
	if pendingReasoning != "" {
		params.Effort = pendingReasoning
	}
	result, err := s.call(ctx, "turn/start", params)
	if err != nil {
		s.appendSystemError(err)
		return err
	}
	var response turnStartResponse
	_ = json.Unmarshal(result, &response)
	s.mu.Lock()
	if pendingModel != "" {
		s.model = pendingModel
		s.pendingModel = ""
	} else {
		s.model = currentModel
	}
	if pendingReasoning != "" {
		s.reasoningEffort = pendingReasoning
		s.pendingReasoning = ""
	} else {
		s.reasoningEffort = currentReasoning
	}
	s.setBusyLocked(response.Turn.ID, false)
	s.status = "Codex is working..."
	s.mu.Unlock()
	s.notify()
	return nil
}

func (s *appServerSession) steerTurn(ctx context.Context, threadID, expectedTurnID string, input Submission) (string, error) {
	result, err := s.call(ctx, "turn/steer", turnSteerParams{
		ThreadID:       threadID,
		ExpectedTurnID: expectedTurnID,
		Input:          encodeSubmissionInput(input),
	})
	if err != nil {
		return "", err
	}
	var response turnSteerResponse
	_ = json.Unmarshal(result, &response)
	return strings.TrimSpace(response.TurnID), nil
}

func (s *appServerSession) recordSteerSubmission(turnID string) {
	turnID = strings.TrimSpace(turnID)
	s.mu.Lock()
	if turnID != "" {
		s.setBusyLocked(turnID, false)
	} else {
		s.busy = true
		s.busyExternal = false
		s.reconciling = false
	}
	s.status = "Sent follow-up to Codex"
	s.mu.Unlock()
	s.notify()
}

func (s *appServerSession) recoverSteerTarget(ctx context.Context, threadID string) (string, bool, error) {
	thread, err := s.readThreadState(ctx, threadID)
	if err != nil {
		return "", false, err
	}

	recoveredTurnID := activeTurnIDFromThread(thread)
	threadIdle := recoveredTurnID == ""
	status := effectiveThreadStatus(thread)
	now := time.Now()
	turnLikelyStuck := false

	s.mu.Lock()
	s.touchLocked()
	switch {
	case threadIdle:
		s.syncThreadStatusLocked(thread.ID, status, true)
	case recoveredTurnID != "":
		if s.activeTurnLooksStuckLocked(recoveredTurnID, now) {
			s.setBusyStalledLocked()
			turnLikelyStuck = true
		} else {
			s.restoreBusyLocked(recoveredTurnID, false)
			s.status = "Codex is working..."
			if s.lastSystemNotice == codexReconnectSuggestion {
				s.lastSystemNotice = ""
			}
		}
	default:
		s.syncThreadStatusLocked(thread.ID, status, true)
	}
	s.mu.Unlock()
	s.notify()

	if turnLikelyStuck {
		return "", false, errBusyTurnLikelyStuck
	}
	return recoveredTurnID, threadIdle, nil
}
