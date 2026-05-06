package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"lcroom/internal/bossrun"
	"lcroom/internal/control"
	"lcroom/internal/model"
)

func (s *Service) ExecuteBossGoalRun(ctx context.Context, proposal bossrun.GoalProposal) (bossrun.GoalResult, error) {
	normalized, err := bossrun.NormalizeGoalProposal(proposal)
	if err != nil {
		return bossrun.GoalResult{RunID: proposal.Run.ID, Kind: proposal.Run.Kind}, err
	}
	if s == nil || s.store == nil {
		return bossrun.GoalResult{
			RunID:         normalized.Run.ID,
			Kind:          normalized.Run.Kind,
			KeptTaskIDs:   bossrun.AgentTaskResourceIDs(normalized.KeepResources),
			ReviewTaskIDs: bossrun.AgentTaskResourceIDs(normalized.ReviewResources),
		}, errors.New("service unavailable")
	}
	switch normalized.Run.Kind {
	case bossrun.GoalKindAgentTaskCleanup:
		return s.executeAgentTaskCleanupGoalRun(ctx, normalized)
	default:
		return bossrun.GoalResult{RunID: normalized.Run.ID, Kind: normalized.Run.Kind}, fmt.Errorf("unsupported goal kind: %s", normalized.Run.Kind)
	}
}

func (s *Service) executeAgentTaskCleanupGoalRun(ctx context.Context, proposal bossrun.GoalProposal) (bossrun.GoalResult, error) {
	result := bossrun.GoalResult{
		RunID:         proposal.Run.ID,
		Kind:          proposal.Run.Kind,
		KeptTaskIDs:   bossrun.AgentTaskResourceIDs(proposal.KeepResources),
		ReviewTaskIDs: bossrun.AgentTaskResourceIDs(proposal.ReviewResources),
	}
	record, err := s.store.CreateGoalRun(ctx, proposal)
	if err != nil {
		return result, err
	}
	proposal = record.Proposal
	result.RunID = proposal.Run.ID
	result.Kind = proposal.Run.Kind
	if _, err := s.store.UpdateGoalRunStatus(ctx, proposal.Run.ID, bossrun.GoalStatusRunning); err != nil {
		return result, err
	}

	archiveStep := goalRunPlanStep(proposal.Plan, bossrun.PlanStepAct, control.CapabilityAgentTaskClose)
	targetIDs := bossrun.AgentTaskResourceIDs(proposal.Authority.Resources)
	if len(targetIDs) == 0 {
		targetIDs = bossrun.AgentTaskResourceIDs(archiveStep.Resources)
	}
	for _, taskID := range targetIDs {
		entry := s.executeAgentTaskCloseGoalStep(ctx, proposal.Run.ID, archiveStep.ID, taskID)
		result.Trace = append(result.Trace, entry)
		if entry.Status == "failed" {
			result.FailedTasks = append(result.FailedTasks, bossrun.TaskFailure{TaskID: taskID, Error: entry.Summary})
		} else {
			result.ArchivedTaskIDs = append(result.ArchivedTaskIDs, taskID)
		}
	}

	verifyStep := goalRunPlanStep(proposal.Plan, bossrun.PlanStepVerify, "")
	verified, verifyErr := s.verifyAgentTaskCleanupGoal(ctx, targetIDs, result.FailedTasks)
	result.Verified = verified
	verifyEntry := bossrun.TraceEntry{
		StepID:  goalRunStepID(verifyStep, "verify-active-set"),
		Status:  "completed",
		Summary: "Refreshed open agent-task state.",
		At:      time.Now(),
	}
	if verifyErr != nil {
		verifyEntry.Status = "failed"
		verifyEntry.Summary = verifyErr.Error()
		result.FailedTasks = append(result.FailedTasks, bossrun.TaskFailure{Error: verifyErr.Error()})
	}
	result.Trace = append(result.Trace, verifyEntry)
	if traceErr := s.store.AppendGoalRunTrace(ctx, proposal.Run.ID, verifyEntry); traceErr != nil {
		result.FailedTasks = append(result.FailedTasks, bossrun.TaskFailure{Error: "record verification trace: " + traceErr.Error()})
	}

	result.Summary = bossrun.FormatGoalResult(result)
	var runErr error
	if len(result.FailedTasks) > 0 {
		runErr = errors.New("one or more goal steps need review")
	}
	record, completeErr := s.store.CompleteGoalRun(ctx, result, runErr)
	if completeErr != nil {
		runErr = errors.Join(runErr, completeErr)
	} else {
		result = record.Result
	}
	return result, runErr
}

func (s *Service) executeAgentTaskCloseGoalStep(ctx context.Context, runID, stepID, taskID string) bossrun.TraceEntry {
	entry := bossrun.TraceEntry{
		StepID:     strings.TrimSpace(stepID),
		Capability: control.CapabilityAgentTaskClose,
		ResourceID: strings.TrimSpace(taskID),
		At:         time.Now(),
	}
	if entry.StepID == "" {
		entry.StepID = "archive-agent-tasks"
	}
	_, err := s.ArchiveAgentTask(ctx, taskID)
	if err != nil {
		entry.Status = "failed"
		entry.Summary = err.Error()
	} else {
		entry.Status = "completed"
		entry.Summary = "Archived agent task record."
	}
	if traceErr := s.store.AppendGoalRunTrace(ctx, runID, entry); traceErr != nil && entry.Status != "failed" {
		entry.Status = "failed"
		entry.Summary = "record trace: " + traceErr.Error()
	}
	return entry
}

func (s *Service) verifyAgentTaskCleanupGoal(ctx context.Context, targetIDs []string, failures []bossrun.TaskFailure) (bool, error) {
	failed := map[string]struct{}{}
	for _, failure := range failures {
		if id := strings.TrimSpace(failure.TaskID); id != "" {
			failed[id] = struct{}{}
		}
	}
	openTasks, err := s.store.ListAgentTasks(ctx, model.AgentTaskFilter{
		Statuses: []model.AgentTaskStatus{model.AgentTaskStatusActive, model.AgentTaskStatusWaiting},
		Limit:    1000,
	})
	if err != nil {
		return false, err
	}
	open := map[string]struct{}{}
	for _, task := range openTasks {
		open[strings.TrimSpace(task.ID)] = struct{}{}
	}
	for _, taskID := range targetIDs {
		taskID = strings.TrimSpace(taskID)
		if taskID == "" {
			continue
		}
		if _, ok := failed[taskID]; ok {
			return false, nil
		}
		if _, ok := open[taskID]; ok {
			return false, nil
		}
	}
	return true, nil
}

func goalRunPlanStep(plan bossrun.Plan, kind bossrun.PlanStepKind, capability ...control.CapabilityName) bossrun.PlanStep {
	var want control.CapabilityName
	if len(capability) > 0 {
		want = capability[0]
	}
	for _, step := range plan.Steps {
		if step.Kind != kind {
			continue
		}
		if want != "" && step.Capability != want {
			continue
		}
		return step
	}
	return bossrun.PlanStep{Kind: kind, Capability: want}
}

func goalRunStepID(step bossrun.PlanStep, fallback string) string {
	if id := strings.TrimSpace(step.ID); id != "" {
		return id
	}
	return fallback
}
