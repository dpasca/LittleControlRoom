package boss

import (
	"errors"
	"fmt"
	"strings"

	"lcroom/internal/bossrun"
	"lcroom/internal/control"
	"lcroom/internal/uistyle"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type GoalRunConfirmedMsg struct {
	Proposal bossrun.GoalProposal
}

type GoalRunResultMsg struct {
	Result         bossrun.GoalResult
	Err            error
	AnnounceInChat bool
}

type goalProposalError struct {
	err error
}

func (e goalProposalError) Error() string {
	if e.err == nil {
		return "goal proposal failed"
	}
	return "goal proposal failed: " + e.err.Error()
}

func (e goalProposalError) Unwrap() error {
	return e.err
}

func wrapGoalProposalError(err error) error {
	if err == nil {
		return nil
	}
	return goalProposalError{err: err}
}

func goalProposalFromBossAction(action bossAction) (bossrun.GoalProposal, string, error) {
	capabilities := make([]control.CapabilityName, 0, len(action.GoalAllowedCapabilities))
	for _, capability := range action.GoalAllowedCapabilities {
		capabilities = append(capabilities, control.CapabilityName(strings.TrimSpace(capability)))
	}
	resources := append([]control.ResourceRef(nil), action.GoalResources...)
	if len(resources) == 0 {
		resources = append([]control.ResourceRef(nil), action.Resources...)
	}
	proposal := bossrun.GoalProposal{
		Run: bossrun.GoalRun{
			Kind:            strings.TrimSpace(action.GoalKind),
			Title:           strings.TrimSpace(action.GoalTitle),
			Objective:       strings.TrimSpace(action.GoalObjective),
			SuccessCriteria: strings.TrimSpace(action.GoalSuccessCriteria),
		},
		Plan: bossrun.Plan{
			Version: 1,
			Steps:   append([]bossrun.PlanStep(nil), action.GoalPlanSteps...),
		},
		Authority: bossrun.AuthorityGrant{
			Summary:              strings.TrimSpace(action.GoalTitle),
			AllowedCapabilities:  capabilities,
			Resources:            resources,
			ForbiddenSideEffects: append([]string(nil), action.GoalForbiddenSideEffects...),
			MaxRisk:              control.RiskLevel(strings.TrimSpace(action.GoalMaxRisk)),
		},
		Preview:          strings.TrimSpace(action.GoalPreview),
		ArchiveResources: resources,
		KeepResources:    append([]control.ResourceRef(nil), action.GoalKeepResources...),
		ReviewResources:  append([]control.ResourceRef(nil), action.GoalReviewResources...),
	}
	normalized, err := bossrun.NormalizeGoalProposal(proposal)
	if err != nil {
		return bossrun.GoalProposal{}, "", err
	}
	return normalized, normalized.Preview, nil
}

func (m Model) GoalConfirmationActive() bool {
	return m.pendingGoal != nil
}

func (m Model) updateGoalConfirmation(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.pendingGoal == nil {
		return m, nil
	}
	switch msg.String() {
	case "enter":
		if !m.embedded {
			m.status = "Goal runs need the main TUI host"
			return m, nil
		}
		proposal := bossrun.CloneGoalProposal(*m.pendingGoal)
		m.pendingGoal = nil
		m.status = "Starting goal run..."
		return m, func() tea.Msg {
			return GoalRunConfirmedMsg{Proposal: proposal}
		}
	case "esc", "ctrl+c":
		m.pendingGoal = nil
		m.status = "Goal run canceled"
		m = m.recordOperationalNotice("goal_canceled", "notice", "The user canceled a pending goal run.")
		m.appendDeskEvent("goal", "cancel", "The pending goal run was canceled.")
		m.syncLayout(false)
		return m, nil
	default:
		m.status = "Confirm goal run with Enter, or Esc to cancel"
		return m, nil
	}
}

func (m Model) applyGoalRunResult(msg GoalRunResultMsg) (tea.Model, tea.Cmd) {
	m.pendingGoal = nil
	content := goalResultContent(msg)
	var cmds []tea.Cmd
	if msg.Err != nil {
		m.status = operationalStatusLine(content, "Goal run failed")
		m = m.recordOperationalNotice("goal_failed", "error", content)
		m.appendDeskEvent("goal", "failed", content)
	} else {
		m.status = operationalStatusLine(content, "Goal run completed")
		m = m.recordOperationalNotice("goal_completed", "notice", content)
		m.appendDeskEvent("goal", "done", content)
	}
	if msg.AnnounceInChat {
		if saved, ok := m.appendAssistantNoticeMessage(content); ok {
			cmds = append(cmds, m.saveBossChatMessageCmd(saved))
		}
	}
	m.syncLayout(msg.AnnounceInChat)
	if m.svc != nil {
		cmds = append(cmds, m.loadStateCmd())
	}
	return m, tea.Batch(cmds...)
}

func goalResultContent(msg GoalRunResultMsg) string {
	status := strings.TrimSpace(bossrun.FormatGoalResult(msg.Result))
	if msg.Err != nil {
		errText := strings.TrimSpace(msg.Err.Error())
		switch {
		case status == "":
			status = errText
		case errText != "" && status != errText:
			status += ": " + errText
		}
		return "I could not complete that goal run: " + status
	}
	if status == "" {
		status = "Goal run completed."
	}
	return status
}

func (m Model) renderGoalConfirmationOverlay(body string, bodyW, bodyH int) string {
	panel := m.renderGoalConfirmationDialog(bodyW, bodyH)
	panelW := lipgloss.Width(panel)
	panelH := lipgloss.Height(panel)
	left := maxInt(0, (bodyW-panelW)/2)
	top := maxInt(0, minInt((bodyH-panelH)/3, bodyH-panelH))
	return overlayBossBlock(body, panel, bodyW, bodyH, left, top)
}

func (m Model) renderGoalConfirmationDialog(bodyW, bodyH int) string {
	panelW := minInt(bodyW, minInt(maxInt(62, bodyW-10), 92))
	panelInnerW := maxInt(32, panelW-4)
	content := m.renderGoalConfirmationContent(panelInnerW)
	panelH := maxInt(14, countBlockLines(content)+4)
	if bodyH > 0 {
		panelH = minInt(panelH, maxInt(10, bodyH-2))
	}
	return renderBossControlPanel("Confirm Goal Run", content, panelW, panelH)
}

func (m Model) renderGoalConfirmationContent(width int) string {
	if m.pendingGoal == nil {
		return ""
	}
	proposal := *m.pendingGoal
	targets := bossrun.AgentTaskResourceIDs(proposal.Authority.Resources)
	capabilities := make([]string, 0, len(proposal.Authority.AllowedCapabilities))
	for _, capability := range proposal.Authority.AllowedCapabilities {
		capabilities = append(capabilities, string(capability))
	}
	lines := []string{
		bossControlNoticeStyle.Render(fitLine("Scoped goal: execute multiple primitive actions after one approval", width)),
		"",
		renderBossControlDetail("Goal", proposal.Run.Title, width),
		renderBossControlDetail("Targets", fmt.Sprintf("%d agent tasks", len(targets)), width),
		renderBossControlDetail("Risk", string(proposal.Authority.MaxRisk), width),
		renderBossControlDetail("Allowed", strings.Join(capabilities, ", "), width),
		"",
		bossControlSectionStyle.Render(fitLine("Objective", width)),
		renderBossControlPromptBox(proposal.Run.Objective, width),
		"",
		bossControlSectionStyle.Render(fitLine("Targets", width)),
		renderBossGoalResourceBox(proposal.Authority.Resources, width),
		"",
		bossControlSectionStyle.Render(fitLine("Guardrails", width)),
		renderBossControlPromptBox(strings.Join(proposal.Authority.ForbiddenSideEffects, "\n"), width),
		"",
		strings.Join([]string{
			renderBossControlAction("Enter", "run", uistyle.DialogActionPrimary),
			renderBossControlAction("Esc", "cancel", uistyle.DialogActionCancel),
		}, "   "),
	}
	return strings.Join(lines, "\n")
}

func renderBossGoalResourceBox(resources []control.ResourceRef, width int) string {
	lines := make([]string, 0, len(resources))
	for _, resource := range resources {
		if resource.Kind != control.ResourceAgentTask {
			continue
		}
		id := strings.TrimSpace(resource.ID)
		label := strings.TrimSpace(resource.Label)
		switch {
		case id != "" && label != "":
			lines = append(lines, label+" ("+id+")")
		case id != "":
			lines = append(lines, id)
		case label != "":
			lines = append(lines, label)
		}
	}
	if len(lines) == 0 {
		return renderBossControlPromptBox("(no agent tasks)", width)
	}
	if len(lines) > 7 {
		lines = append(lines[:6], fmt.Sprintf("... %d more", len(lines)-6))
	}
	for i, line := range lines {
		lines[i] = "- " + line
	}
	return renderBossControlPromptBox(strings.Join(lines, "\n"), width)
}

func goalProposalDetail(err error) string {
	if err == nil {
		return ""
	}
	var proposalErr goalProposalError
	if errors.As(err, &proposalErr) && proposalErr.Unwrap() != nil {
		return proposalErr.Unwrap().Error()
	}
	return err.Error()
}
