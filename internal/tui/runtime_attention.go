package tui

import (
	"fmt"

	"lcroom/internal/codexapp"
	"lcroom/internal/model"
)

const (
	runtimeRunningAttentionWeight   = 10
	embeddedApprovalAttentionWeight = 120
	embeddedQuestionAttentionWeight = 100
)

func (m Model) projectAttentionScore(project model.ProjectSummary) int {
	score := project.AttentionScore
	if snapshot := m.projectRuntimeSnapshot(project.Path); snapshot.Running {
		score += runtimeRunningAttentionWeight
	}
	if _, _, ok := m.projectPendingEmbeddedApproval(project.Path); ok {
		score += embeddedApprovalAttentionWeight
	}
	if _, _, ok := m.projectPendingEmbeddedQuestion(project.Path); ok {
		score += embeddedQuestionAttentionWeight
	}
	return score
}

func (m Model) projectAttentionReasons(project model.ProjectSummary, base []model.AttentionReason) []model.AttentionReason {
	reasons := append([]model.AttentionReason(nil), base...)
	if reason := m.projectRuntimeAttentionReason(project.Path); reason != nil {
		reasons = append(reasons, *reason)
	}
	if reason := m.projectEmbeddedApprovalAttentionReason(project.Path); reason != nil {
		reasons = append(reasons, *reason)
	}
	if reason := m.projectEmbeddedQuestionAttentionReason(project.Path); reason != nil {
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

func (m Model) projectEmbeddedApprovalAttentionReason(projectPath string) *model.AttentionReason {
	request, provider, ok := m.projectPendingEmbeddedApproval(projectPath)
	if !ok {
		return nil
	}

	text := provider.Label() + " is waiting for approval"
	switch request.Kind {
	case codexapp.ApprovalFileChange:
		if request.GrantRoot != "" {
			text = fmt.Sprintf("%s is waiting for file-change approval under %s", provider.Label(), request.GrantRoot)
		} else {
			text = provider.Label() + " is waiting for file-change approval"
		}
	case codexapp.ApprovalCommandExecution:
		if request.Command != "" {
			text = fmt.Sprintf("%s is waiting for command approval: %s", provider.Label(), request.Command)
		}
	}

	return &model.AttentionReason{
		Code:   "embedded_approval_pending",
		Text:   text,
		Weight: embeddedApprovalAttentionWeight,
	}
}

func (m Model) projectEmbeddedQuestionAttentionReason(projectPath string) *model.AttentionReason {
	summary, provider, ok := m.projectPendingEmbeddedQuestion(projectPath)
	if !ok {
		return nil
	}

	text := fmt.Sprintf("%s is asking: %s", provider.Label(), summary)

	return &model.AttentionReason{
		Code:   "embedded_question_pending",
		Text:   text,
		Weight: embeddedQuestionAttentionWeight,
	}
}

func projectAttentionLabelForScore(score int) string {
	return fmt.Sprintf("%4d", score)
}

func projectAttentionLabel(project model.ProjectSummary) string {
	return projectAttentionLabelForScore(project.AttentionScore)
}

// projectRepoWarningIndicator returns a styled "!" for dirty/unsynced repos.
// Dirty worktree → red (danger), sync-only → orange (warning), neither → space.
func projectRepoWarningIndicator(project model.ProjectSummary) string {
	if project.RepoDirty {
		return detailDangerStyle.Render("!")
	}
	if repoSyncWarning(project.RepoSyncStatus) {
		return detailWarningStyle.Render("!")
	}
	return " "
}
