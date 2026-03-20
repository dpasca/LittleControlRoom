package tui

import (
	"fmt"

	"lcroom/internal/model"
)

const runtimeRunningAttentionWeight = 10

func (m Model) projectAttentionScore(project model.ProjectSummary) int {
	score := project.AttentionScore
	if snapshot := m.projectRuntimeSnapshot(project.Path); snapshot.Running {
		score += runtimeRunningAttentionWeight
	}
	return score
}

func (m Model) projectAttentionReasons(project model.ProjectSummary, base []model.AttentionReason) []model.AttentionReason {
	reasons := append([]model.AttentionReason(nil), base...)
	if reason := m.projectRuntimeAttentionReason(project.Path); reason != nil {
		reasons = append(reasons, *reason)
	}
	return reasons
}

func (m Model) projectRuntimeAttentionReason(projectPath string) *model.AttentionReason {
	snapshot := m.projectRuntimeSnapshot(projectPath)
	if !snapshot.Running {
		return nil
	}

	text := "Managed runtime is running"
	if startedAt := snapshot.StartedAt; !startedAt.IsZero() {
		text = fmt.Sprintf("Managed runtime running for %s", formatRunningDuration(m.currentTime().Sub(startedAt)))
	}
	if len(snapshot.Ports) > 0 {
		text += fmt.Sprintf(" on %s", joinPorts(snapshot.Ports))
	}

	return &model.AttentionReason{
		Code:   "runtime_running",
		Text:   text,
		Weight: runtimeRunningAttentionWeight,
	}
}

func projectAttentionLabelForScore(project model.ProjectSummary, score int) string {
	label := fmt.Sprintf("%4d", score)
	if projectHasRepoWarning(project) {
		return "!" + label
	}
	return " " + label
}

func projectAttentionLabel(project model.ProjectSummary) string {
	return projectAttentionLabelForScore(project, project.AttentionScore)
}
