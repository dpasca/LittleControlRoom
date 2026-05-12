package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	bossui "lcroom/internal/boss"
	"lcroom/internal/bossrun"
	"lcroom/internal/codexapp"
	"lcroom/internal/control"
	"lcroom/internal/model"

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
	if proposal.Run.Kind == bossrun.GoalKindLCAgentTask {
		return m.executeBossLCAgentGoalRun(proposal)
	}
	m.status = "Running goal: " + proposal.Run.Title
	return m, m.executeBossGoalRunCmd(proposal)
}

func (m Model) executeBossGoalRunCmd(proposal bossrun.GoalProposal) tea.Cmd {
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
		result, err := svc.ExecuteBossGoalRun(ctx, proposal)
		return bossui.GoalRunResultMsg{Result: result, Err: err, AnnounceInChat: true}
	}
}

func (m Model) executeBossLCAgentGoalRun(proposal bossrun.GoalProposal) (tea.Model, tea.Cmd) {
	result := bossrun.GoalResult{
		RunID: proposal.Run.ID,
		Kind:  proposal.Run.Kind,
	}
	if m.svc == nil || m.svc.Store() == nil {
		err := errors.New("service unavailable")
		m.status = "Goal run failed: " + err.Error()
		return m, bossGoalResultCmd(result, err)
	}
	ctx := m.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	record, err := m.svc.Store().CreateGoalRun(ctx, proposal)
	if err != nil {
		m.status = "Goal run failed: " + err.Error()
		return m, bossGoalResultCmd(result, err)
	}
	proposal = record.Proposal
	result.RunID = proposal.Run.ID
	result.Kind = proposal.Run.Kind
	if _, err := m.svc.Store().UpdateGoalRunStatus(ctx, proposal.Run.ID, bossrun.GoalStatusRunning); err != nil {
		m.status = "Goal run failed: " + err.Error()
		return m, m.completeBossGoalRunCmd(result, err)
	}

	createStep := bossGoalPlanStep(proposal.Plan, bossrun.PlanStepDelegate, control.CapabilityAgentTaskCreate)
	task, err := m.svc.CreateAgentTask(ctx, model.CreateAgentTaskInput{
		Title:        proposal.Run.Title,
		Kind:         model.AgentTaskKindAgent,
		Capabilities: lcagentGoalTaskCapabilities(proposal),
		Resources:    agentTaskResourcesFromControl(proposal.Authority.Resources),
	})
	createTrace := bossrun.TraceEntry{
		StepID:     bossGoalStepID(createStep, "create-lcagent-task"),
		Capability: control.CapabilityAgentTaskCreate,
		Status:     "completed",
		Summary:    "Created LCAgent goal task.",
		At:         time.Now(),
	}
	if err != nil {
		createTrace.Status = "failed"
		createTrace.Summary = err.Error()
		result.FailedTasks = append(result.FailedTasks, bossrun.TaskFailure{Error: err.Error()})
		result.Trace = append(result.Trace, createTrace)
		_ = m.svc.Store().AppendGoalRunTrace(ctx, proposal.Run.ID, createTrace)
		m.status = "Goal run failed: " + err.Error()
		return m, m.completeBossGoalRunCmd(result, err)
	}
	createTrace.ResourceID = task.ID
	result.CreatedTaskIDs = append(result.CreatedTaskIDs, task.ID)
	result.Trace = append(result.Trace, createTrace)
	if traceErr := m.svc.Store().AppendGoalRunTrace(ctx, proposal.Run.ID, createTrace); traceErr != nil {
		err := fmt.Errorf("record LCAgent task creation trace: %w", traceErr)
		result.FailedTasks = append(result.FailedTasks, bossrun.TaskFailure{TaskID: task.ID, Error: err.Error()})
		m.status = "Goal run failed: " + err.Error()
		return m, m.completeBossGoalRunCmd(result, err)
	}

	m.upsertOpenAgentTask(task)
	project, err := projectSummaryForAgentTask(task)
	if err != nil {
		result.FailedTasks = append(result.FailedTasks, bossrun.TaskFailure{TaskID: task.ID, Error: err.Error()})
		m.status = "Goal run failed: " + err.Error()
		return m, m.completeBossGoalRunCmd(result, err)
	}
	prompt := m.agentTaskLaunchPromptWithRuntimeContext(task, lcagentGoalTaskPrompt(proposal))
	updated, launchCmd := m.launchEmbeddedForProjectWithOptions(project, codexapp.ProviderLCAgent, embeddedLaunchOptions{
		forceNew: true,
		prompt:   prompt,
		reveal:   false,
	})
	m = normalizeUpdateModel(updated)
	if launchCmd == nil {
		err := errors.New(firstNonEmptyTrimmed(m.status, "LCAgent goal task launch did not start"))
		result.FailedTasks = append(result.FailedTasks, bossrun.TaskFailure{TaskID: task.ID, Error: err.Error()})
		m.status = "Goal run failed: " + err.Error()
		return m, m.completeBossGoalRunCmd(result, err)
	}
	launchStep := bossGoalPlanStep(proposal.Plan, bossrun.PlanStepAct, control.CapabilityAgentTaskCreate)
	trackedCmd := m.agentTaskLaunchTrackingCmd(task, launchCmd, bossAgentTaskHandoffStatus(task))
	m.status = "Starting LCAgent goal task: " + task.Title
	return m, m.bossLCAgentGoalLaunchCmd(proposal, task, launchStep, result.Trace, trackedCmd)
}

func (m Model) bossLCAgentGoalLaunchCmd(proposal bossrun.GoalProposal, task model.AgentTask, launchStep bossrun.PlanStep, priorTrace []bossrun.TraceEntry, launchCmd tea.Cmd) tea.Cmd {
	svc := m.svc
	ctx := m.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		msg := launchCmd()
		result := bossrun.GoalResult{
			RunID:          proposal.Run.ID,
			Kind:           proposal.Run.Kind,
			CreatedTaskIDs: []string{task.ID},
			Trace:          append([]bossrun.TraceEntry(nil), priorTrace...),
		}
		var runErr error
		launchTrace := bossrun.TraceEntry{
			StepID:     bossGoalStepID(launchStep, "launch-lcagent"),
			Capability: control.CapabilityAgentTaskCreate,
			ResourceID: task.ID,
			Status:     "completed",
			Summary:    "Launched LCAgent session for goal task.",
			At:         time.Now(),
		}
		opened, ok := msg.(codexSessionOpenedMsg)
		switch {
		case !ok:
			runErr = errors.New("LCAgent launch did not return a session result")
		case opened.err != nil:
			runErr = opened.err
		default:
			result.Verified = true
			if sessionID := strings.TrimSpace(opened.snapshot.ThreadID); sessionID != "" {
				launchTrace.Summary = "Launched LCAgent session " + sessionID + " for goal task."
			}
		}
		if runErr != nil {
			launchTrace.Status = "failed"
			launchTrace.Summary = runErr.Error()
			result.FailedTasks = append(result.FailedTasks, bossrun.TaskFailure{TaskID: task.ID, Error: runErr.Error()})
		}
		result.Trace = append(result.Trace, launchTrace)
		if svc != nil && svc.Store() != nil {
			if traceErr := svc.Store().AppendGoalRunTrace(ctx, proposal.Run.ID, launchTrace); traceErr != nil {
				wrapped := fmt.Errorf("record LCAgent launch trace: %w", traceErr)
				runErr = errors.Join(runErr, wrapped)
				result.FailedTasks = append(result.FailedTasks, bossrun.TaskFailure{TaskID: task.ID, Error: wrapped.Error()})
			}
			record, completeErr := svc.Store().CompleteGoalRun(ctx, result, runErr)
			if completeErr != nil {
				runErr = errors.Join(runErr, completeErr)
			} else {
				result = record.Result
			}
		}
		result.Summary = bossrun.FormatGoalResult(result)
		goalMsg := bossui.GoalRunResultMsg{Result: result, Err: runErr, AnnounceInChat: true}
		if msg == nil {
			return goalMsg
		}
		return tea.BatchMsg{
			func() tea.Msg { return msg },
			func() tea.Msg { return goalMsg },
		}
	}
}

func (m Model) completeBossGoalRunCmd(result bossrun.GoalResult, runErr error) tea.Cmd {
	svc := m.svc
	ctx := m.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		if svc != nil && svc.Store() != nil && strings.TrimSpace(result.RunID) != "" {
			record, completeErr := svc.Store().CompleteGoalRun(ctx, result, runErr)
			if completeErr != nil {
				runErr = errors.Join(runErr, completeErr)
			} else {
				result = record.Result
			}
		}
		result.Summary = bossrun.FormatGoalResult(result)
		return bossui.GoalRunResultMsg{Result: result, Err: runErr, AnnounceInChat: true}
	}
}

func bossGoalPlanStep(plan bossrun.Plan, kind bossrun.PlanStepKind, capability control.CapabilityName) bossrun.PlanStep {
	for _, step := range plan.Steps {
		if step.Kind != kind {
			continue
		}
		if capability != "" && step.Capability != capability {
			continue
		}
		return step
	}
	return bossrun.PlanStep{Kind: kind, Capability: capability}
}

func bossGoalStepID(step bossrun.PlanStep, fallback string) string {
	if id := strings.TrimSpace(step.ID); id != "" {
		return id
	}
	return fallback
}

func lcagentGoalTaskCapabilities(proposal bossrun.GoalProposal) []string {
	capabilities := []string{"lcagent.low_autonomy", "repo.read", "repo.edit", "test.run"}
	if proposal.Run.Kind != "" {
		capabilities = append(capabilities, "goal."+proposal.Run.Kind)
	}
	return capabilities
}

func lcagentGoalTaskPrompt(proposal bossrun.GoalProposal) string {
	lines := []string{
		"Boss goal run:",
		"ID: " + strings.TrimSpace(proposal.Run.ID),
		"Kind: " + strings.TrimSpace(proposal.Run.Kind),
		"Title: " + strings.TrimSpace(proposal.Run.Title),
		"",
		"Objective:",
		strings.TrimSpace(proposal.Run.Objective),
		"",
		"Success criteria:",
		strings.TrimSpace(proposal.Run.SuccessCriteria),
		"",
		"Authority:",
		"- Run inside LCAgent low-autonomy edit-and-verify mode.",
		"- Stay within the approved resources and forbidden side effects below.",
	}
	if len(proposal.Authority.Resources) > 0 {
		lines = append(lines, "", "Approved resources:")
		for _, resource := range proposal.Authority.Resources {
			if line := lcagentGoalResourceLine(resource); line != "" {
				lines = append(lines, "- "+line)
			}
		}
	}
	if len(proposal.Authority.ForbiddenSideEffects) > 0 {
		lines = append(lines, "", "Forbidden side effects:")
		for _, sideEffect := range proposal.Authority.ForbiddenSideEffects {
			if sideEffect = strings.TrimSpace(sideEffect); sideEffect != "" {
				lines = append(lines, "- "+sideEffect)
			}
		}
	}
	lines = append(lines,
		"",
		"Verification:",
		"- Run the narrowest relevant checks you can safely run under LCAgent policy.",
		"- In the final response, list changed files and verification results explicitly.",
	)
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func lcagentGoalResourceLine(resource control.ResourceRef) string {
	kind := strings.TrimSpace(string(resource.Kind))
	value := firstNonEmptyTrimmed(resource.Label, resource.ID, resource.ProjectPath, resource.Path, resource.SessionID)
	switch {
	case value != "" && kind != "":
		return kind + ": " + value
	case value != "":
		return value
	case kind != "":
		return kind
	default:
		return ""
	}
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
