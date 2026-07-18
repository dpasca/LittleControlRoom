package tui

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"lcroom/internal/model"
	"lcroom/internal/uisurface"

	"github.com/charmbracelet/lipgloss"
)

type projectDetailSurface = uisurface.ProjectDetailSurface
type projectDetailSurfaceBlockKind = uisurface.DetailBlockKind

const (
	projectDetailSurfaceField        = uisurface.DetailBlockField
	projectDetailSurfaceWrappedField = uisurface.DetailBlockWrappedField
	projectDetailSurfaceFieldGroup   = uisurface.DetailBlockFieldGroup
	projectDetailSurfaceText         = uisurface.DetailBlockText
	projectDetailSurfaceSection      = uisurface.DetailBlockSection
	projectDetailSurfaceBullet       = uisurface.DetailBlockBullet
)

type projectDetailSurfaceTone = uisurface.Tone

const (
	projectDetailToneValue    = uisurface.ToneValue
	projectDetailToneMuted    = uisurface.ToneMuted
	projectDetailToneWarning  = uisurface.ToneWarning
	projectDetailToneDanger   = uisurface.ToneDanger
	projectDetailToneConflict = uisurface.ToneConflict
)

type projectDetailSurfaceFieldValue = uisurface.DetailFieldValue
type projectDetailSurfaceBlock = uisurface.DetailBlock

func projectDetailFieldValue(label, text string, tone projectDetailSurfaceTone) projectDetailSurfaceFieldValue {
	return uisurface.FieldValue(label, text, tone)
}

func (m Model) buildProjectDetailSurface(p model.ProjectSummary, d model.ProjectDetail) projectDetailSurface {
	now := m.currentTime()
	stuckThreshold := m.assessmentStallThreshold()
	surface := uisurface.BuildProjectDetailOverview(p, uisurface.BuildOptions{
		Now:            now,
		StuckThreshold: stuckThreshold,
	})
	for blockIndex := range surface.Blocks {
		block := &surface.Blocks[blockIndex]
		if block.Label == "Summary" {
			block.Text = m.projectAssessmentDisplayTextAt(p, now, stuckThreshold)
			block.Tone = projectDetailToneValue
			if projectAssessmentRefreshing(p) {
				block.Tone = projectDetailToneMuted
			}
			if strings.TrimSpace(block.Text) == "" || block.Text == "-" {
				block.Text = "not assessed yet"
				block.Tone = projectDetailToneMuted
			}
		}
		for fieldIndex := range block.Fields {
			field := &block.Fields[fieldIndex]
			switch field.Label {
			case "Assessment":
				field.Text = projectAssessmentLabelWithThreshold(p, now, stuckThreshold)
				field.Tone = projectDetailAssessmentTone(p, now, stuckThreshold)
				field.RenderedText = assessmentDisplayStyle(p, now, stuckThreshold).Render(field.Text)
			case "Activity":
				field.Text = projectActivityStatus(p)
				field.Tone = projectDetailActivityTone(p)
				field.RenderedText = activityDisplayStyle(p).Render(field.Text)
			}
		}
	}
	if model.NormalizeProjectKind(p.Kind) == model.ProjectKindScratchTask {
		surface.Field("Kind", "scratch task", projectDetailToneValue)
		surface.Text("Press d or use /remove to archive or delete this task.", projectDetailToneMuted)
	}

	if browserAttention, ok := m.projectPendingBrowserAttention(p.Path); ok {
		surface.WrappedField("Browser", browserAttentionDetailSummary(browserAttention), projectDetailToneWarning)
	}
	if summary := m.projectProcessWarningSummary(p.Path); summary != "" {
		surface.WrappedField("Processes", summary, projectDetailToneWarning)
	}
	if summary := m.projectLocalInstanceSummary(p.Path); summary != "" {
		surface.WrappedField("Local instance", summary, projectDetailToneValue)
	}
	if projectMissing(p) {
		surface.Text("Folder: missing on disk", projectDetailToneWarning)
		if p.WorktreeKind == model.WorktreeKindLinked {
			surface.Text("Use /remove to clean up this missing linked worktree. x and /wt remove still work too.", projectDetailToneMuted)
		} else {
			surface.Text("Use /remove to take this missing folder off the dashboard.", projectDetailToneMuted)
		}
	}
	if resolver, ok := m.mergeConflictResolverForProject(p.Path); ok {
		surface.WrappedField("Resolver", resolver.detailText(m.currentTime()), mergeConflictResolverDetailTone(resolver))
	}

	surface.RenderedField("Last activity", m.projectDetailLastActivityText(p), projectDetailLastActivityTone(p), m.projectDetailLastActivityRenderedText(p))
	if p.MovedFromPath != "" && moveStatusActive(p.MovedAt, p.Path, p.LatestSessionDetectedProjectPath) {
		movedFields := []projectDetailSurfaceFieldValue{
			projectDetailFieldValue("Moved from", p.MovedFromPath, projectDetailToneValue),
		}
		if !p.MovedAt.IsZero() {
			movedFields = append(movedFields, projectDetailFieldValue("Moved at", p.MovedAt.Format(time.RFC3339), projectDetailToneValue))
		}
		surface.FieldGroup(movedFields...)
	}

	if projectHasGitInfo(p) {
		surface.RenderedField("Repo", m.repoCombinedDetailText(p), m.repoCombinedDetailTone(p), m.repoCombinedDetailValue(p))
		if p.RepoConflict {
			surface.WrappedField("Conflict", m.repoConflictDetailText(p), projectDetailToneConflict)
		}
		if projectHasSubmoduleAttention(p) {
			surface.RenderedField("Submodules", repoSubmoduleAttentionDetailText(p), projectDetailToneWarning, repoSubmoduleAttentionDetailValue(p))
		}
	}
	if projectUsesRepoUI(p) && p.WorktreeKind == model.WorktreeKindLinked {
		mergeBackText := "parent branch unavailable"
		mergeBackTone := projectDetailToneMuted
		targetBranch := strings.TrimSpace(p.WorktreeParentBranch)
		sourceBranch := strings.TrimSpace(p.RepoBranch)
		switch {
		case targetBranch == "":
		case sourceBranch != "" && sourceBranch != targetBranch:
			mergeBackText = sourceBranch + " -> " + targetBranch
			mergeBackTone = projectDetailToneValue
		default:
			mergeBackText = targetBranch
			mergeBackTone = projectDetailToneValue
		}
		surface.Field("Merge back", mergeBackText, mergeBackTone)
		surface.RenderedField("Integration status", worktreeIntegrationStatusDetailText(p), worktreeIntegrationStatusDetailTone(p), worktreeIntegrationStatusDetailValue(p))
	}

	rootPath := projectWorktreeRootPath(p)
	family := m.worktreeFamily(rootPath)
	orphanedFamily := m.orphanedWorktreeFamily(rootPath)
	orphanedCount := len(orphanedFamily)
	if projectUsesRepoUI(p) && (len(family) > 1 || p.WorktreeKind == model.WorktreeKindLinked || orphanedCount > 0) {
		activeCount, dirtyCount := m.worktreeActivityCounts(family)
		pendingIntegrationCount := worktreePendingIntegrationCount(family)
		surface.Field("Worktrees", worktreeGroupSummary(family, activeCount, dirtyCount, pendingIntegrationCount, orphanedCount), projectDetailToneValue)
		if projectIsWorktreeRoot(p) {
			surface.Section("Worktree lanes")
			family = append([]model.ProjectSummary(nil), family...)
			sort.SliceStable(family, func(i, j int) bool {
				leftRoot := projectIsWorktreeRoot(family[i])
				rightRoot := projectIsWorktreeRoot(family[j])
				if leftRoot != rightRoot {
					return leftRoot
				}
				if !family[i].LastActivity.Equal(family[j].LastActivity) {
					return family[i].LastActivity.After(family[j].LastActivity)
				}
				return strings.ToLower(family[i].Path) < strings.ToLower(family[j].Path)
			})
			for _, member := range family {
				text, tone := m.worktreeLaneDetailText(p, member)
				surface.Bullet(text, tone)
			}
		}
		if orphanedCount > 0 {
			surface.Section("Worktree warnings")
			surface.Bullet(fmt.Sprintf("%d orphaned checkout(s) still exist on disk. Git no longer tracks them as live worktrees. Remove the leftover folder when you no longer need its files.", orphanedCount), projectDetailToneWarning)
			for _, orphan := range orphanedFamily {
				surface.Bullet(orphanedWorktreeDetailText(orphan), projectDetailToneWarning)
				surface.Bullet(m.displayPathWithHomeTilde(orphan.Path), projectDetailToneMuted)
			}
		}
		if hints := m.worktreeActionHints(p, family); len(hints) > 0 {
			surface.Section("Worktree actions")
			for _, hint := range hints {
				surface.Bullet(hint, projectDetailToneValue)
			}
		}
	}

	if p.SnoozedUntil != nil {
		surface.Field("Snoozed until", p.SnoozedUntil.Format(time.RFC3339), projectDetailToneValue)
	}
	todoProject := p
	if rootPath := projectWorktreeRootPath(p); rootPath != "" && filepath.Clean(rootPath) != filepath.Clean(p.Path) {
		if rootProject, ok := m.projectSummaryByPath(rootPath); ok {
			todoProject = rootProject
		}
	}
	todoText := "none · press t or /todo"
	todoTone := projectDetailToneMuted
	todoRenderedText := detailMutedStyle.Render(todoText)
	if todoProject.TotalTODOCount > 0 {
		todoCountText := fmt.Sprintf("%d open, %d total", todoProject.OpenTODOCount, todoProject.TotalTODOCount)
		todoText = todoCountText + " · press t or /todo"
		todoTone = projectDetailToneValue
		todoRenderedText = detailValueStyle.Render(todoCountText) + detailMutedStyle.Render(" · press t or /todo")
		if filepath.Clean(todoProject.Path) != filepath.Clean(p.Path) {
			todoText += " · repo-scoped"
			todoRenderedText += detailMutedStyle.Render(" · repo-scoped")
		}
	}
	surface.RenderedField("TODOs", todoText, todoTone, todoRenderedText)

	surface.Section("Attention reasons")
	reasons := uisurface.DetailAttentionReasons(m.projectAttentionReasons(p, d.Reasons))
	if len(reasons) == 0 {
		surface.Text("- none", projectDetailToneMuted)
	} else {
		for _, reason := range reasons {
			surface.RenderedText(projectDetailReasonText(reason), projectDetailReasonTone(reason), detailReasonLine(reason))
		}
	}

	if m.showSessions {
		surface.Section("Sessions")
		if len(d.Sessions) == 0 {
			surface.Text("- none", projectDetailToneMuted)
		} else {
			limit := min(6, len(d.Sessions))
			for i := 0; i < limit; i++ {
				s := d.Sessions[i]
				surface.Text(fmt.Sprintf("- %s | %s | errors=%d", shortID(s.SessionID), s.LastEventAt.Format("01-02 15:04"), s.ErrorCount), projectDetailToneValue)
			}
		}
	}

	if m.showEvents {
		surface.Section("Recent events")
		if len(d.RecentEvents) == 0 {
			surface.Text("- none", projectDetailToneMuted)
		} else {
			limit := min(8, len(d.RecentEvents))
			for i := 0; i < limit; i++ {
				e := d.RecentEvents[i]
				surface.Text(fmt.Sprintf("- %s %s", e.At.Format("01-02 15:04"), e.Payload), projectDetailToneValue)
			}
		}
	}

	return surface
}

func renderProjectDetailSurface(surface projectDetailSurface, width int) string {
	lines := make([]string, 0, len(surface.Blocks))
	for _, block := range surface.Blocks {
		switch block.Kind {
		case projectDetailSurfaceField:
			lines = append(lines, renderProjectDetailSurfaceField(block.Label, block.Text, block.Tone, block.RenderedText))
		case projectDetailSurfaceWrappedField:
			lines = append(lines, renderWrappedDetailField(block.Label, projectDetailSurfaceStyle(block.Tone), width, block.Text))
		case projectDetailSurfaceFieldGroup:
			fields := make([]string, 0, len(block.Fields))
			for _, field := range block.Fields {
				fields = append(fields, renderProjectDetailSurfaceField(field.Label, field.Text, field.Tone, field.RenderedText))
			}
			lines = appendDetailFields(lines, width, fields...)
		case projectDetailSurfaceText:
			if block.RenderedText != "" {
				lines = append(lines, block.RenderedText)
			} else {
				lines = append(lines, projectDetailSurfaceStyle(block.Tone).Render(block.Text))
			}
		case projectDetailSurfaceSection:
			lines = append(lines, detailSectionStyle.Render(block.Text))
		case projectDetailSurfaceBullet:
			lines = append(lines, renderWrappedDetailBullet(projectDetailSurfaceStyle(block.Tone), width, block.Text))
		}
	}
	content := strings.Join(lines, "\n")
	return fitPaneContent(content, width, len(strings.Split(content, "\n")))
}

func renderProjectDetailSurfaceField(label, text string, tone projectDetailSurfaceTone, renderedText string) string {
	if renderedText != "" {
		return detailField(label, renderedText)
	}
	return detailField(label, projectDetailSurfaceStyle(tone).Render(text))
}

func projectDetailSurfaceStyle(tone projectDetailSurfaceTone) lipgloss.Style {
	switch tone {
	case projectDetailToneMuted:
		return detailMutedStyle
	case projectDetailToneWarning:
		return detailWarningStyle
	case projectDetailToneDanger:
		return detailDangerStyle
	case projectDetailToneConflict:
		return detailConflictStyle
	default:
		return detailValueStyle
	}
}

func projectDetailAssessmentTone(project model.ProjectSummary, now time.Time, stuckThreshold time.Duration) projectDetailSurfaceTone {
	if projectAssessmentRefreshing(project) {
		return projectDetailToneMuted
	}
	if _, category, ok := visibleAssessmentStatusLabelAt(project, now, stuckThreshold); ok {
		switch category {
		case model.SessionCategoryNeedsFollowUp, model.SessionCategoryWaitingForUser:
			return projectDetailToneWarning
		case model.SessionCategoryBlocked:
			return projectDetailToneDanger
		default:
			return projectDetailToneValue
		}
	}
	return projectDetailToneMuted
}

func projectDetailActivityTone(project model.ProjectSummary) projectDetailSurfaceTone {
	if projectMissing(project) {
		return projectDetailToneWarning
	}
	if moveStatusActive(project.MovedAt, project.Path, project.LatestSessionDetectedProjectPath) {
		return projectDetailToneWarning
	}
	if project.Status == model.StatusPossiblyStuck {
		return projectDetailToneWarning
	}
	if project.Status == model.StatusActive {
		return projectDetailToneValue
	}
	return projectDetailToneMuted
}

func projectDetailLastActivityTone(project model.ProjectSummary) projectDetailSurfaceTone {
	if project.LastActivity.IsZero() {
		return projectDetailToneMuted
	}
	return projectDetailToneValue
}

func (m Model) projectDetailLastActivityText(project model.ProjectSummary) string {
	text := "never"
	if !project.LastActivity.IsZero() {
		text = project.LastActivity.Format(time.RFC3339)
	}
	if project.LatestSessionFormat != "" || !project.LastActivity.IsZero() {
		lastSource := "None"
		if project.LatestSessionFormat != "" {
			lastSource = sourceLabel(project.LatestSessionFormat)
		}
		text += "  " + lastSource
	}
	return text
}

func (m Model) projectDetailLastActivityRenderedText(project model.ProjectSummary) string {
	text := detailMutedStyle.Render("never")
	if !project.LastActivity.IsZero() {
		text = detailValueStyle.Render(project.LastActivity.Format(time.RFC3339))
	}
	if project.LatestSessionFormat != "" || !project.LastActivity.IsZero() {
		lastSource := detailMutedStyle.Render("None")
		if project.LatestSessionFormat != "" {
			lastSource = sourceStyle(project.LatestSessionFormat, m.projectHasLiveCodexSession(project.Path)).Render(sourceLabel(project.LatestSessionFormat))
		}
		text += "  " + lastSource
	}
	return text
}

func (m Model) repoCombinedDetailText(project model.ProjectSummary) string {
	var parts []string
	if op, ok := m.pendingGitOperation(project.Path); ok {
		parts = append(parts, op.summaryText())
	} else if project.RepoConflict {
		parts = append(parts, "conflict")
	} else if project.RepoDirty {
		parts = append(parts, "dirty")
	} else {
		parts = append(parts, "clean")
	}
	if resolver, ok := m.mergeConflictResolverForProject(project.Path); ok {
		if label := resolver.repoLabel(); label != "" {
			parts = append(parts, label)
		}
	}
	if projectHasSubmoduleAttention(project) && m.pendingGitSummary(project.Path) == "" {
		parts = append(parts, repoSubmoduleAttentionPlainText(project))
	}
	if projectShowsRemoteSyncStatus(project) && m.pendingGitSummary(project.Path) == "" {
		switch project.RepoSyncStatus {
		case model.RepoSyncNoRemote:
			parts = append(parts, "no remote")
		case model.RepoSyncNoUpstream:
			parts = append(parts, "no upstream")
		case model.RepoSyncSynced:
			parts = append(parts, "synced")
		case model.RepoSyncAhead:
			parts = append(parts, fmt.Sprintf("ahead %d", project.RepoAheadCount))
		case model.RepoSyncBehind:
			parts = append(parts, fmt.Sprintf("behind %d", project.RepoBehindCount))
		case model.RepoSyncDiverged:
			parts = append(parts, fmt.Sprintf("diverged +%d/-%d", project.RepoAheadCount, project.RepoBehindCount))
		}
	}
	text := strings.Join(parts, ", ")
	if branch := strings.TrimSpace(project.RepoBranch); branch != "" {
		if text == "" {
			return "(" + branch + ")"
		}
		return text + " (" + branch + ")"
	}
	return text
}

func (m Model) repoCombinedDetailTone(project model.ProjectSummary) projectDetailSurfaceTone {
	if resolver, ok := m.mergeConflictResolverForProject(project.Path); ok {
		switch resolver.Phase {
		case mergeConflictResolverFailed, mergeConflictResolverConflictsRemain:
			return projectDetailToneConflict
		case mergeConflictResolverNeedsAttention, mergeConflictResolverRefreshFailed:
			return projectDetailToneWarning
		}
	}
	if project.RepoConflict {
		return projectDetailToneConflict
	}
	if project.RepoDirty || projectHasSubmoduleAttention(project) {
		return projectDetailToneWarning
	}
	if projectShowsRemoteSyncStatus(project) && repoSyncWarning(project.RepoSyncStatus) {
		if project.RepoSyncStatus == model.RepoSyncDiverged {
			return projectDetailToneDanger
		}
		return projectDetailToneWarning
	}
	return projectDetailToneMuted
}

func repoSubmoduleAttentionDetailText(project model.ProjectSummary) string {
	return repoSubmoduleAttentionPlainText(project) + ". Use /commit to resolve submodule changes or push existing submodule commits."
}

func mergeConflictResolverDetailTone(state mergeConflictResolverState) projectDetailSurfaceTone {
	switch state.Phase {
	case mergeConflictResolverStarting, mergeConflictResolverRunning, mergeConflictResolverChecking, mergeConflictResolverResolved:
		return projectDetailToneValue
	case mergeConflictResolverNeedsAttention, mergeConflictResolverRefreshFailed:
		return projectDetailToneWarning
	case mergeConflictResolverFailed, mergeConflictResolverConflictsRemain:
		return projectDetailToneConflict
	default:
		return projectDetailToneMuted
	}
}

func (m Model) repoConflictDetailText(project model.ProjectSummary) string {
	location := "repo"
	if project.WorktreeKind == model.WorktreeKindLinked {
		location = "worktree"
	}
	if resolver, ok := m.mergeConflictResolverForProject(project.Path); ok {
		switch resolver.Phase {
		case mergeConflictResolverStarting, mergeConflictResolverRunning:
			return "A background " + resolver.provider().Label() + " resolver is working on the unmerged files in this " + location + ". Run /resolve again to see its latest status."
		case mergeConflictResolverChecking:
			return "The background resolver finished and Little Control Room is refreshing this " + location + "'s Git status."
		case mergeConflictResolverNeedsAttention:
			return "The background resolver paused for input. Open its saved session from /sessions to continue."
		case mergeConflictResolverRefreshFailed:
			return "The background resolver finished, but Little Control Room could not refresh this " + location + "'s Git status. Review the Resolver field before deciding whether to retry."
		case mergeConflictResolverFailed:
			return "The background resolver failed. Review the Resolver field and its saved session before retrying /resolve."
		case mergeConflictResolverConflictsRemain:
			return "The background resolver finished, but Git still reports unmerged files in this " + location + ". Review its saved session or run /resolve to retry."
		}
	}
	return "Unmerged files are present in this " + location + ". Use /resolve to start a background conflict resolver, or resolve/abort the in-progress Git operation manually."
}

func worktreeIntegrationStatusDetailText(project model.ProjectSummary) string {
	return worktreeIntegrationStatusSummary(project)
}

func worktreeIntegrationStatusDetailTone(project model.ProjectSummary) projectDetailSurfaceTone {
	if project.RepoConflict {
		return projectDetailToneConflict
	}
	if project.RepoDirty {
		return projectDetailToneWarning
	}
	switch project.WorktreeMergeStatus {
	case model.WorktreeMergeStatusMergeInProgress:
		return projectDetailToneWarning
	case model.WorktreeMergeStatusNotMerged:
		return projectDetailToneWarning
	case model.WorktreeMergeStatusUnknown:
		return projectDetailToneMuted
	default:
		return projectDetailToneValue
	}
}

func (m Model) worktreeLaneDetailText(current, member model.ProjectSummary) (string, projectDetailSurfaceTone) {
	label := projectWorktreeLabel(member)
	if projectIsWorktreeRoot(member) {
		label = "root: " + label
	}
	statusParts := []string{}
	tone := projectDetailToneValue
	op, gitOperationPending := m.pendingGitOperation(member.Path)
	if gitOperationPending {
		statusParts = append(statusParts, op.shortLabel())
	} else if member.RepoConflict {
		statusParts = append(statusParts, "conflict")
		tone = projectDetailToneConflict
	} else if member.RepoDirty {
		statusParts = append(statusParts, "dirty")
		tone = projectDetailToneWarning
	} else {
		statusParts = append(statusParts, "clean")
	}
	if member.Status != model.StatusIdle {
		statusParts = append(statusParts, string(member.Status))
	}
	if m.projectHasLiveCodexSession(member.Path) {
		statusParts = append(statusParts, "agent")
	}
	if resolver, ok := m.mergeConflictResolverForProject(member.Path); ok {
		if label := resolver.repoLabel(); label != "" {
			statusParts = append(statusParts, label)
		}
		switch resolver.Phase {
		case mergeConflictResolverNeedsAttention, mergeConflictResolverRefreshFailed:
			tone = projectDetailToneWarning
		case mergeConflictResolverFailed, mergeConflictResolverConflictsRemain:
			tone = projectDetailToneConflict
		}
	}
	if snapshot := m.projectRuntimeSnapshot(member.Path); snapshot.Running {
		statusParts = append(statusParts, "runtime")
	}
	if member.WorktreeKind == model.WorktreeKindLinked && !gitOperationPending && !member.RepoConflict {
		if integration := worktreeLaneIntegrationText(member); integration != "" {
			statusParts = append(statusParts, integration)
		}
	}
	if filepath.Clean(member.Path) == filepath.Clean(current.Path) {
		statusParts = append(statusParts, "current")
	}
	return label + " · " + strings.Join(statusParts, ", "), tone
}

func orphanedWorktreeDetailText(orphan model.ProjectSummary) string {
	statusParts := []string{"orphaned"}
	if orphan.RepoDirty {
		statusParts = append(statusParts, "dirty")
	}
	if integration := worktreeLaneIntegrationText(orphan); integration != "" {
		statusParts = append(statusParts, integration)
	}
	return projectWorktreeLabel(orphan) + " · " + strings.Join(statusParts, ", ")
}

func worktreeLaneIntegrationText(project model.ProjectSummary) string {
	switch {
	case project.WorktreeMergeStatus == model.WorktreeMergeStatusMergeInProgress:
		return "merging"
	case project.RepoDirty:
		return "needs commit + merge"
	case project.WorktreeMergeStatus == model.WorktreeMergeStatusNotMerged:
		return "needs merge"
	case project.WorktreeMergeStatus == model.WorktreeMergeStatusMerged:
		return "no changes to integrate"
	default:
		return ""
	}
}

func projectDetailReasonText(reason model.AttentionReason) string {
	return fmt.Sprintf("- [%+d] %s", reason.Weight, reason.Text)
}

func projectDetailReasonTone(reason model.AttentionReason) projectDetailSurfaceTone {
	if reason.Weight > 0 {
		return projectDetailToneWarning
	}
	if reason.Weight < 0 {
		return projectDetailToneValue
	}
	return projectDetailToneValue
}
