package codexapp

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"time"
)

type claudeModelControl struct {
	requestID string
	model     string
	effort    string
	result    chan error
}

func (s *claudeCodeSession) canApplyClaudeModelToRetainedStreamLocked() bool {
	return (s.pendingModel != "" || s.pendingReasoning != "") &&
		s.cmd != nil && s.stdin != nil && s.managedStream && s.streamState == "idle" &&
		s.claudeBrowserOwnsStreamLocked() && s.pendingSubmissions == 0 && s.runningBackgroundTaskCountLocked() == 0
}

// Keep the browser's owning process alive and acknowledge each change before
// sending a prompt. An effort rejection leaves a confirmed model change intact.
func (s *claudeCodeSession) applyClaudeModelToRetainedStream(cmd *exec.Cmd, model, effort string) error {
	if model != "" {
		err := s.sendClaudeModelControl(cmd, model, "", map[string]any{"subtype": "set_model", "model": model})
		if err != nil {
			return err
		}
	}
	if effort != "" {
		return s.sendClaudeModelControl(cmd, "", effort, map[string]any{
			"subtype":  "apply_flag_settings",
			"settings": map[string]string{"effortLevel": effort},
		})
	}
	return nil
}

func (s *claudeCodeSession) sendClaudeModelControl(cmd *exec.Cmd, model, effort string, request map[string]any) error {
	change := &claudeModelControl{
		requestID: "lcr-model-" + newClaudeMessageID(),
		model:     model,
		effort:    effort,
		result:    make(chan error, 1),
	}
	payload, err := json.Marshal(map[string]any{
		"type": "control_request", "request_id": change.requestID, "request": request,
	})
	if err != nil {
		return err
	}
	s.mu.Lock()
	if s.closed || s.cmd != cmd || s.stdin == nil || s.interruptPending ||
		(model != "" && s.pendingModel != model) || (effort != "" && s.pendingReasoning != effort) {
		s.mu.Unlock()
		return fmt.Errorf("%w: Claude Code stream or model selection changed before it could be applied", ErrSessionChanged)
	}
	if effort != "" && s.reasoningEffort == effort {
		s.pendingReasoning = ""
		s.mu.Unlock()
		return nil
	}
	s.modelControl = change
	stdin := s.stdin
	s.mu.Unlock()

	if _, err := stdin.Write(append(payload, '\n')); err != nil {
		s.mu.Lock()
		if s.modelControl == change {
			s.completeClaudeModelControlLocked(fmt.Errorf("send Claude Code model selection: %w", err))
		}
		s.mu.Unlock()
	}
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	select {
	case err := <-change.result:
		return err
	case <-timer.C:
		s.mu.Lock()
		pending := s.modelControl == change
		s.mu.Unlock()
		if !pending {
			return <-change.result
		}
		return fmt.Errorf("Claude Code has not confirmed the model selection; wait or use /reconnect before retrying")
	}
}

func (s *claudeCodeSession) acknowledgeClaudeModelControlLocked(change *claudeModelControl, reportedModel string) {
	if change.model != "" {
		s.modelChoice, s.modelChoiceSet = change.model, true
		s.model = concreteClaudeModel(reportedModel)
		if s.pendingModel == change.model {
			s.pendingModel = ""
		}
	}
	if change.effort != "" {
		s.reasoningEffort = change.effort
		if s.pendingReasoning == change.effort {
			s.pendingReasoning = ""
		}
	}
	if s.pendingModel == "" && s.pendingReasoning == "" {
		s.lastSystemNotice = ""
	}
	s.completeClaudeModelControlLocked(nil)
}

func (s *claudeCodeSession) completeClaudeModelControlLocked(err error) {
	if change := s.modelControl; change != nil {
		s.modelControl = nil
		change.result <- err
	}
}
