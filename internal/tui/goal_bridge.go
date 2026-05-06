package tui

import (
	"context"
	"errors"

	bossui "lcroom/internal/boss"
	"lcroom/internal/bossrun"

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
