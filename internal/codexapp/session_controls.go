package codexapp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

func (s *appServerSession) Compact() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return fmt.Errorf("codex session is closed")
	}
	if s.compacting {
		s.mu.Unlock()
		return fmt.Errorf("conversation compaction is already in progress")
	}
	if s.busy {
		s.mu.Unlock()
		return fmt.Errorf("cannot compact while a turn is in progress")
	}
	threadID := strings.TrimSpace(s.threadID)
	if threadID == "" {
		s.mu.Unlock()
		return fmt.Errorf("no active thread to compact")
	}
	s.touchLocked()
	s.compacting = true
	s.status = "Compacting conversation history..."
	s.mu.Unlock()
	s.notify()

	startCtx, startCancel := context.WithTimeout(context.Background(), rpcTimeout)
	defer startCancel()

	_, err := s.call(startCtx, "thread/compact/start", threadCompactStartParams{
		ThreadID: threadID,
	})
	if err != nil {
		s.mu.Lock()
		s.compacting = false
		s.mu.Unlock()
		s.appendSystemError(err)
		return err
	}
	waitCtx, waitCancel := context.WithTimeout(context.Background(), compactionWaitTimeout)
	defer waitCancel()
	return s.waitForCompactionCompletion(waitCtx, threadID)
}

func (s *appServerSession) Review() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return fmt.Errorf("codex session is closed")
	}
	if s.compacting {
		s.mu.Unlock()
		return fmt.Errorf("cannot start a review while conversation compaction is in progress")
	}
	if s.busy {
		s.mu.Unlock()
		return fmt.Errorf("cannot start a review while a turn is in progress")
	}
	threadID := strings.TrimSpace(s.threadID)
	if threadID == "" {
		s.mu.Unlock()
		return fmt.Errorf("no active thread to review")
	}
	s.touchLocked()
	s.status = "Starting Codex review..."
	s.mu.Unlock()
	s.notify()

	ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
	defer cancel()

	result, err := s.call(ctx, "review/start", reviewStartParams{
		ThreadID: threadID,
		Target:   reviewTarget{Type: "uncommittedChanges"},
		Delivery: "inline",
	})
	if err != nil {
		s.appendSystemError(err)
		return err
	}
	var response reviewStartResponse
	_ = json.Unmarshal(result, &response)

	s.mu.Lock()
	if reviewThreadID := strings.TrimSpace(response.ReviewThreadID); reviewThreadID != "" {
		s.threadID = reviewThreadID
	}
	s.setBusyLocked(response.Turn.ID, false)
	s.status = "Codex is reviewing uncommitted changes..."
	s.mu.Unlock()
	s.notify()
	return nil
}

func (s *appServerSession) ListModels() ([]ModelOption, error) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, fmt.Errorf("codex session is closed")
	}
	s.touchLocked()
	s.mu.Unlock()

	const pageSize = 100
	includeHidden := false
	limit := pageSize
	var cursor *string
	models := []ModelOption{}

	for {
		ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
		result, err := s.call(ctx, "model/list", modelListParams{
			Cursor:        cursor,
			Limit:         &limit,
			IncludeHidden: &includeHidden,
		})
		cancel()
		if err != nil {
			return nil, err
		}
		var response modelListResponse
		if err := json.Unmarshal(result, &response); err != nil {
			return nil, err
		}
		for _, entry := range response.Data {
			models = append(models, ModelOption{
				ID:                        strings.TrimSpace(entry.ID),
				Model:                     strings.TrimSpace(entry.Model),
				DisplayName:               strings.TrimSpace(entry.DisplayName),
				Description:               strings.TrimSpace(entry.Description),
				Hidden:                    entry.Hidden,
				SupportedReasoningEfforts: exportedReasoningEffortOptions(entry.SupportedReasoningEfforts),
				DefaultReasoningEffort:    strings.TrimSpace(entry.DefaultReasoningEffort),
				SupportsPersonality:       entry.SupportsPersonality,
				IsDefault:                 entry.IsDefault,
			})
		}
		if response.NextCursor == nil || strings.TrimSpace(*response.NextCursor) == "" {
			return models, nil
		}
		next := strings.TrimSpace(*response.NextCursor)
		cursor = &next
	}
}

func exportedReasoningEffortOptions(in []reasoningEffortOptionEntry) []ReasoningEffortOption {
	if len(in) == 0 {
		return nil
	}
	out := make([]ReasoningEffortOption, 0, len(in))
	for _, option := range in {
		effort := strings.TrimSpace(option.ReasoningEffort)
		if effort == "" {
			continue
		}
		out = append(out, ReasoningEffortOption{
			ReasoningEffort: effort,
			Description:     strings.TrimSpace(option.Description),
		})
	}
	return out
}

func (s *appServerSession) StageModelOverride(model, reasoningEffort string) error {
	model = strings.TrimSpace(model)
	reasoningEffort = strings.TrimSpace(reasoningEffort)

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return fmt.Errorf("codex session is closed")
	}

	currentModel := strings.TrimSpace(s.model)
	currentReasoning := strings.TrimSpace(s.reasoningEffort)
	if model == "" {
		model = currentModel
	}
	if reasoningEffort == "" {
		reasoningEffort = currentReasoning
	}

	s.pendingModel, s.pendingReasoning = stagedModelOverride(currentModel, currentReasoning, model, reasoningEffort)
	s.touchLocked()
	s.mu.Unlock()
	s.notify()
	return nil
}

func stagedModelOverride(currentModel, currentReasoning, requestedModel, requestedReasoning string) (string, string) {
	currentModel = strings.TrimSpace(currentModel)
	currentReasoning = strings.TrimSpace(currentReasoning)
	requestedModel = strings.TrimSpace(requestedModel)
	requestedReasoning = strings.TrimSpace(requestedReasoning)

	if requestedModel == "" && requestedReasoning == "" {
		return "", ""
	}
	if requestedModel == "" {
		requestedModel = currentModel
	}
	if requestedReasoning == "" {
		requestedReasoning = currentReasoning
	}
	if strings.EqualFold(requestedModel, currentModel) && strings.EqualFold(requestedReasoning, currentReasoning) {
		return "", ""
	}
	return requestedModel, requestedReasoning
}

func (s *appServerSession) Interrupt() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return fmt.Errorf("codex session is closed")
	}
	threadID := s.threadID
	turnID := s.activeTurnID
	busyExternal := s.busyExternal
	if busyExternal {
		s.mu.Unlock()
		return fmt.Errorf("this Codex session is already busy in another process; interrupt it there or hide it here")
	}
	if threadID == "" || turnID == "" {
		s.mu.Unlock()
		return fmt.Errorf("no active Codex turn to interrupt")
	}
	s.touchLocked()
	s.status = "Interrupting Codex turn..."
	s.mu.Unlock()
	s.notify()

	ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
	defer cancel()
	if _, err := s.call(ctx, "turn/interrupt", turnInterruptParams{
		ThreadID: threadID,
		TurnID:   turnID,
	}); err != nil {
		s.appendSystemError(err)
		return err
	}
	return nil
}

func (s *appServerSession) RespondApproval(decision ApprovalDecision) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return fmt.Errorf("codex session is closed")
	}
	approval := cloneApprovalRequest(s.pendingApproval)
	if approval == nil {
		s.mu.Unlock()
		return fmt.Errorf("no pending approval")
	}
	if !approval.AllowsDecision(decision) {
		s.mu.Unlock()
		return fmt.Errorf("approval decision %q is not supported for %s approvals", decision, approval.Kind)
	}
	s.touchLocked()
	s.status = "Sending approval decision..."
	s.mu.Unlock()
	s.notify()

	response := map[string]any{
		"decision": string(decision),
	}
	if err := s.send(rpcResponse{
		ID:     decodeRequestID(approval.ID),
		Result: response,
	}); err != nil {
		s.appendSystemError(err)
		return err
	}

	s.mu.Lock()
	s.lastSystemNotice = "Approval decision sent"
	s.status = "Approval decision sent"
	s.mu.Unlock()
	s.notify()
	return nil
}

func (s *appServerSession) RespondToolInput(answers map[string][]string) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return fmt.Errorf("codex session is closed")
	}
	request := cloneToolInputRequest(s.pendingToolInput)
	if request == nil {
		s.mu.Unlock()
		return fmt.Errorf("no pending tool input request")
	}
	s.touchLocked()
	s.status = "Sending tool input..."
	s.mu.Unlock()
	s.notify()

	payload := map[string]any{
		"answers": map[string]any{},
	}
	answerMap := payload["answers"].(map[string]any)
	for questionID, values := range answers {
		trimmed := make([]string, 0, len(values))
		for _, value := range values {
			if text := strings.TrimSpace(value); text != "" {
				trimmed = append(trimmed, text)
			}
		}
		answerMap[questionID] = map[string]any{
			"answers": trimmed,
		}
	}

	if err := s.send(rpcResponse{
		ID:     decodeRequestID(request.ID),
		Result: payload,
	}); err != nil {
		s.appendSystemError(err)
		return err
	}

	s.mu.Lock()
	s.lastSystemNotice = "Tool input sent"
	s.status = "Tool input sent"
	s.mu.Unlock()
	s.notify()
	return nil
}

func (s *appServerSession) RespondElicitation(decision ElicitationDecision, content json.RawMessage) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return fmt.Errorf("codex session is closed")
	}
	request := cloneElicitationRequest(s.pendingElicitation)
	if request == nil {
		s.mu.Unlock()
		return fmt.Errorf("no pending MCP elicitation")
	}
	s.touchLocked()
	s.status = "Sending MCP response..."
	s.mu.Unlock()
	s.notify()

	payload := map[string]any{
		"action": string(decision),
	}
	if len(content) > 0 && string(content) != "null" {
		var decoded any
		if err := json.Unmarshal(content, &decoded); err != nil {
			return fmt.Errorf("decode elicitation content: %w", err)
		}
		payload["content"] = decoded
	}

	if err := s.send(rpcResponse{
		ID:     decodeRequestID(request.ID),
		Result: payload,
	}); err != nil {
		s.appendSystemError(err)
		return err
	}

	s.mu.Lock()
	s.lastSystemNotice = "MCP input sent"
	s.status = "MCP input sent"
	s.mu.Unlock()
	s.notify()
	return nil
}

func (s *appServerSession) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.clearActiveStateLocked()
	s.status = "Codex session closed"
	cmd := s.cmd
	stdin := s.stdin
	s.mu.Unlock()

	if stdin != nil {
		_ = stdin.Close()
	}
	if cmd != nil && cmd.Process != nil {
		_ = terminateAppServerCommand(cmd)
	} else {
		s.closeExitCh()
	}
	s.failPending("session closed")
	s.notify()
	return nil
}

func (s *appServerSession) CloseDueToInactivity() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	if s.busy || s.pendingApproval != nil {
		s.touchLocked()
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.clearActiveStateLocked()
	s.lastSystemNotice = idleShutdownNotice
	s.status = idleShutdownNotice
	s.appendEntryLocked("", TranscriptSystem, idleShutdownNotice)
	cmd := s.cmd
	stdin := s.stdin
	s.mu.Unlock()

	if stdin != nil {
		_ = stdin.Close()
	}
	if cmd != nil && cmd.Process != nil {
		_ = terminateAppServerCommand(cmd)
	} else {
		s.closeExitCh()
	}
	s.failPending("session closed")
	s.notify()
	return nil
}

func (s *appServerSession) WaitClosed(timeout time.Duration) bool {
	if s == nil {
		return true
	}
	s.mu.Lock()
	closed := s.closed
	cmd := s.cmd
	exitCh := s.exitCh
	s.mu.Unlock()
	if cmd == nil || exitCh == nil {
		return true
	}
	if !closed {
		return false
	}
	if timeout <= 0 {
		<-exitCh
		return true
	}
	select {
	case <-exitCh:
		return true
	case <-time.After(timeout):
		return false
	}
}
