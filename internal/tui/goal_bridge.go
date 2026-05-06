package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	bossui "lcroom/internal/boss"
	"lcroom/internal/bossrun"
	"lcroom/internal/control"
	"lcroom/internal/model"
	"lcroom/internal/service"

	tea "github.com/charmbracelet/bubbletea"
)

func (m Model) executeBossGoalRun(msg bossui.GoalRunConfirmedMsg) (tea.Model, tea.Cmd) {
	proposal, err := bossrun.NormalizeGoalProposal(msg.Proposal)
	if err != nil {
		m.status = "Goal run invalid: " + err.Error()
		return m, bossGoalResultCmd(bossrun.GoalResult{
			RunID: msg.Proposal.Run.ID,
			Kind:  msg.Proposal.Run.Kind,
		}, err)
	}
	switch proposal.Run.Kind {
	case bossrun.GoalKindAgentTaskCleanup:
		m.status = "Running goal: " + proposal.Run.Title
		return m, m.executeAgentTaskCleanupGoalCmd(proposal)
	default:
		err := fmt.Errorf("unsupported goal kind: %s", proposal.Run.Kind)
		m.status = "Goal run unsupported: " + proposal.Run.Kind
		return m, bossGoalResultCmd(bossrun.GoalResult{RunID: proposal.Run.ID, Kind: proposal.Run.Kind}, err)
	}
}

func (m Model) executeAgentTaskCleanupGoalCmd(proposal bossrun.GoalProposal) tea.Cmd {
	svc := m.svc
	parent := m.ctx
	return func() tea.Msg {
		result := bossrun.GoalResult{
			RunID:         proposal.Run.ID,
			Kind:          proposal.Run.Kind,
			KeptTaskIDs:   bossrun.AgentTaskResourceIDs(proposal.KeepResources),
			ReviewTaskIDs: bossrun.AgentTaskResourceIDs(proposal.ReviewResources),
		}
		if svc == nil {
			return bossui.GoalRunResultMsg{Result: result, Err: errors.New("service unavailable"), AnnounceInChat: true}
		}
		ctx := parent
		if ctx == nil {
			ctx = context.Background()
		}
		record, err := svc.Store().CreateGoalRun(ctx, proposal)
		if err != nil {
			return bossui.GoalRunResultMsg{Result: result, Err: err, AnnounceInChat: true}
		}
		proposal = record.Proposal
		result.RunID = proposal.Run.ID
		result.Kind = proposal.Run.Kind
		if _, err := svc.Store().UpdateGoalRunStatus(ctx, proposal.Run.ID, bossrun.GoalStatusRunning); err != nil {
			return bossui.GoalRunResultMsg{Result: result, Err: err, AnnounceInChat: true}
		}
		targetIDs := bossrun.AgentTaskResourceIDs(proposal.Authority.Resources)
		for _, taskID := range targetIDs {
			_, err := svc.ArchiveAgentTask(ctx, taskID)
			entry := bossrun.TraceEntry{
				StepID:     "archive-agent-tasks",
				Capability: control.CapabilityAgentTaskClose,
				ResourceID: taskID,
				At:         time.Now(),
			}
			if err != nil {
				entry.Status = "failed"
				entry.Summary = err.Error()
				result.FailedTasks = append(result.FailedTasks, bossrun.TaskFailure{TaskID: taskID, Error: err.Error()})
			} else {
				entry.Status = "completed"
				entry.Summary = "Archived agent task record."
				result.ArchivedTaskIDs = append(result.ArchivedTaskIDs, taskID)
			}
			result.Trace = append(result.Trace, entry)
			if traceErr := svc.Store().AppendGoalRunTrace(ctx, proposal.Run.ID, entry); traceErr != nil {
				result.FailedTasks = append(result.FailedTasks, bossrun.TaskFailure{TaskID: taskID, Error: "record trace: " + traceErr.Error()})
			}
		}
		verified, verifyErr := verifyAgentTaskCleanupGoal(ctx, svc, targetIDs, result.FailedTasks)
		result.Verified = verified
		if verifyErr != nil {
			entry := bossrun.TraceEntry{
				StepID:  "verify-active-set",
				Status:  "failed",
				Summary: verifyErr.Error(),
				At:      time.Now(),
			}
			result.FailedTasks = append(result.FailedTasks, bossrun.TaskFailure{Error: verifyErr.Error()})
			result.Trace = append(result.Trace, entry)
			if traceErr := svc.Store().AppendGoalRunTrace(ctx, proposal.Run.ID, entry); traceErr != nil {
				result.FailedTasks = append(result.FailedTasks, bossrun.TaskFailure{Error: "record verification trace: " + traceErr.Error()})
			}
		} else {
			entry := bossrun.TraceEntry{
				StepID:  "verify-active-set",
				Status:  "completed",
				Summary: "Refreshed open agent-task state.",
				At:      time.Now(),
			}
			result.Trace = append(result.Trace, entry)
			if traceErr := svc.Store().AppendGoalRunTrace(ctx, proposal.Run.ID, entry); traceErr != nil {
				result.FailedTasks = append(result.FailedTasks, bossrun.TaskFailure{Error: "record verification trace: " + traceErr.Error()})
			}
		}
		result.Summary = bossrun.FormatGoalResult(result)
		var runErr error
		if len(result.FailedTasks) > 0 {
			runErr = errors.New("one or more goal steps need review")
		}
		record, completeErr := svc.Store().CompleteGoalRun(ctx, result, runErr)
		if completeErr != nil {
			runErr = errors.Join(runErr, completeErr)
		} else {
			result = record.Result
		}
		return bossui.GoalRunResultMsg{Result: result, Err: runErr, AnnounceInChat: true}
	}
}

func verifyAgentTaskCleanupGoal(ctx context.Context, svc *service.Service, targetIDs []string, failures []bossrun.TaskFailure) (bool, error) {
	if svc == nil {
		return false, errors.New("service unavailable")
	}
	failed := map[string]struct{}{}
	for _, failure := range failures {
		if id := strings.TrimSpace(failure.TaskID); id != "" {
			failed[id] = struct{}{}
		}
	}
	openTasks, err := svc.Store().ListAgentTasks(ctx, model.AgentTaskFilter{
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

func bossGoalResultCmd(result bossrun.GoalResult, err error) tea.Cmd {
	return func() tea.Msg {
		result.Summary = bossrun.FormatGoalResult(result)
		return bossui.GoalRunResultMsg{
			Result:         result,
			Err:            err,
			AnnounceInChat: true,
		}
	}
}

func (m Model) applyBossGoalRunResultToHost(msg bossui.GoalRunResultMsg) Model {
	if len(msg.Result.ArchivedTaskIDs) == 0 {
		return m
	}
	selectedPath := ""
	if selected, ok := m.selectedProject(); ok {
		selectedPath = selected.Path
	}
	for _, taskID := range msg.Result.ArchivedTaskIDs {
		m.openAgentTasks = removeAgentTask(m.openAgentTasks, taskID)
	}
	m.rebuildProjectList(selectedPath)
	return m
}
