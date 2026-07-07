package tui

import (
	"errors"
	"fmt"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"lcroom/internal/codexapp"
	"lcroom/internal/config"
	"lcroom/internal/fuzzyfilter"
	"lcroom/internal/model"
	"lcroom/internal/projectrun"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	liveEngineerSidebarSummarySentenceLimit = 2
	liveEngineerSidebarSummaryCharLimit     = 360
)

func shortID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

func formatListActivityTime(now, activity time.Time) string {
	if activity.IsZero() {
		return "Never"
	}
	if now.IsZero() {
		now = time.Now()
	}

	loc := now.Location()
	if loc == nil {
		loc = time.Local
	}
	nowLocal := now.In(loc)
	activityLocal := activity.In(loc)
	if activityLocal.After(nowLocal) {
		return activityLocal.Format("2006-01-02 15:04")
	}

	diffDays := calendarDayDiff(nowLocal, activityLocal)
	switch {
	case diffDays <= 0:
		return activityLocal.Format("15:04")
	case diffDays == 1:
		return "Yesterday"
	case diffDays < 7:
		return formatRelativeUnit(diffDays, "day")
	case diffDays < 28:
		return formatRelativeUnit(diffDays/7, "week")
	default:
		return formatRelativeUnit(max(1, wholeMonthsBetween(nowLocal, activityLocal)), "month")
	}
}

func calendarDayDiff(now, activity time.Time) int {
	return dayIndex(now) - dayIndex(activity)
}

func dayIndex(t time.Time) int {
	y, m, d := t.Date()
	return int(time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Unix() / (24 * 60 * 60))
}

func wholeMonthsBetween(now, activity time.Time) int {
	months := (now.Year()-activity.Year())*12 + int(now.Month()-activity.Month())
	if now.Day() < activity.Day() {
		months--
	}
	return months
}

func formatRelativeUnit(n int, unit string) string {
	if n <= 1 {
		return fmt.Sprintf("1 %s", unit)
	}
	return fmt.Sprintf("%d %ss", n, unit)
}

func projectHasGitInfo(project model.ProjectSummary) bool {
	if !projectUsesRepoUI(project) {
		return false
	}
	return project.RepoBranch != "" || project.RepoDirty || project.RepoConflict || projectHasSubmoduleAttention(project) || project.WorktreeKind != model.WorktreeKindNone
}

func projectHasRepoWarning(project model.ProjectSummary) bool {
	if !projectUsesRepoUI(project) {
		return false
	}
	return project.RepoConflict || project.RepoDirty || projectHasSubmoduleAttention(project) || (projectShowsRemoteSyncStatus(project) && repoSyncWarning(project.RepoSyncStatus))
}

func projectHasSubmoduleAttention(project model.ProjectSummary) bool {
	return project.RepoSubmoduleDirtyCount > 0 || project.RepoSubmoduleUnpushedCount > 0
}

func appendDetailFields(lines []string, width int, fields ...string) []string {
	if len(fields) == 0 {
		return lines
	}
	if width < 72 {
		return append(lines, fields...)
	}
	for i := 0; i < len(fields); i += 2 {
		if i+1 < len(fields) {
			lines = append(lines, fields[i]+"  "+fields[i+1])
			continue
		}
		lines = append(lines, fields[i])
	}
	return lines
}

func projectAssessmentText(project model.ProjectSummary) string {
	return projectAssessmentTextAt(project, time.Time{}, 0)
}

func (m Model) projectAssessmentDisplayTextAt(project model.ProjectSummary, now time.Time, stuckThreshold time.Duration) string {
	if pending := m.pendingGitSummary(project.Path); pending != "" {
		return pending
	}
	return projectAssessmentTextAt(project, now, stuckThreshold)
}

func projectAssessmentTextAt(project model.ProjectSummary, now time.Time, stuckThreshold time.Duration) string {
	effective := effectiveAssessmentForProject(project, now, stuckThreshold)
	if strings.TrimSpace(effective.Summary) != "" && projectAssessmentUsesLatestSummary(project) {
		return effective.Summary
	}
	if projectAssessmentRefreshing(project) {
		if progress := classificationProgressText(project.LatestSessionClassification, project.LatestSessionClassificationStage, project.LatestSessionClassificationStageStartedAt, project.LatestSessionClassificationUpdatedAt, now, true); progress != "" {
			return progress
		}
	}
	if projectAssessmentFailed(project) {
		return "assessment failed"
	}
	if strings.TrimSpace(project.LatestCompletedSessionSummary) != "" {
		return project.LatestCompletedSessionSummary
	}
	if label, _, ok := visibleAssessmentStatusLabelAt(project, now, stuckThreshold); ok {
		return label
	}
	if fallback := projectAssessmentRepoFallback(project); fallback != "" {
		return fallback
	}
	if project.LatestSessionFormat != "" {
		return "not assessed yet"
	}
	return "-"
}

func projectAssessmentRepoFallback(project model.ProjectSummary) string {
	if !projectUsesRepoUI(project) {
		return ""
	}
	if project.RepoConflict {
		return "unmerged files"
	}
	if project.RepoDirty {
		return "dirty worktree"
	}
	if projectHasSubmoduleAttention(project) {
		return repoSubmoduleAttentionPlainText(project)
	}
	if worktreeNeedsMergeBack(project) {
		return worktreeMergeStatusSummary(project)
	}
	return ""
}

func projectAssessmentStyle(project model.ProjectSummary, now time.Time, stuckThreshold time.Duration) lipgloss.Style {
	if _, _, ok := visibleAssessmentStatusLabelAt(project, now, stuckThreshold); ok {
		if projectAssessmentRefreshing(project) {
			return detailMutedStyle
		}
		if projectAssessmentUnread(project, now, stuckThreshold) {
			return detailValueStyle
		}
		return detailMutedStyle
	}
	return detailMutedStyle
}

type projectRunState uint8

const (
	projectRunIdle projectRunState = iota
	projectRunActive
	projectRunError
)

func projectTODOCountLabel(count int) string {
	if count <= 0 {
		return ""
	}
	return strconv.Itoa(count)
}

func projectListColumnWidths(totalWidth int) (int, int) {
	const baseWidth = 56

	if totalWidth < baseWidth+22 {
		return 10, 10
	}

	remaining := totalWidth - baseWidth
	projectWidth := min(28, max(16, remaining/4))
	assessmentWidth := remaining - projectWidth - 2
	if assessmentWidth < 18 {
		projectWidth = max(14, remaining-20)
		assessmentWidth = remaining - projectWidth - 2
	}
	if assessmentWidth < 10 {
		projectWidth = max(10, remaining/3)
		assessmentWidth = max(10, remaining-projectWidth-2)
	}
	return projectWidth, assessmentWidth
}

func renderProjectListHeader(projectW, assessmentW int) string {
	return lipgloss.JoinHorizontal(
		lipgloss.Top,
		lipgloss.NewStyle().Width(projectListSelectionGutterWidth).Render(""),
		lipgloss.NewStyle().Width(5).Align(lipgloss.Right).Render("ATTN"),
		"  ",
		lipgloss.NewStyle().Width(8).Render("ASSESS"),
		" ",
		lipgloss.NewStyle().Width(10).Render("LAST"),
		" ",
		lipgloss.NewStyle().Width(projectListAgentWidth).Align(lipgloss.Left).Render("AGENT"),
		" ",
		lipgloss.NewStyle().Width(projectListTODOWidth).Align(lipgloss.Right).Render("TODO"),
		" ",
		lipgloss.NewStyle().Width(projectListRunWidth).Align(lipgloss.Left).Render("RUN"),
		"  ",
		lipgloss.NewStyle().Width(projectW).Render("PROJECT"),
		"  ",
		lipgloss.NewStyle().Width(assessmentW).Render("SUMMARY"),
	)
}

func (m Model) projectTurnLiveWindow() time.Duration {
	activeThreshold := m.currentSettingsBaseline().ActiveThreshold
	if activeThreshold > 0 {
		return activeThreshold
	}
	return config.Default().ActiveThreshold
}

func (m Model) projectUnfinishedTurnLooksLive(project model.ProjectSummary, now time.Time) bool {
	if !project.LatestTurnStateKnown || project.LatestTurnCompleted {
		return false
	}
	if m.suspendedTurnDismissed(project) {
		return false
	}

	if project.LatestSessionClassification == model.ClassificationCompleted {
		effective := effectiveAssessmentForProject(project, now, m.assessmentStallThreshold())
		switch effective.Category {
		case model.SessionCategoryCompleted,
			model.SessionCategoryBlocked,
			model.SessionCategoryWaitingForUser,
			model.SessionCategoryNeedsFollowUp:
			return false
		}
	}

	if now.IsZero() {
		return true
	}
	lastEventAt := project.LatestSessionLastEventAt
	if lastEventAt.IsZero() {
		lastEventAt = project.LastActivity
	}
	if lastEventAt.IsZero() {
		return true
	}
	return now.Sub(lastEventAt) <= m.projectTurnLiveWindow()
}

func (m Model) projectAgentDisplay(project model.ProjectSummary, now time.Time) (string, string, bool) {
	if snapshot, ok := m.liveCodexSnapshot(project.Path); ok {
		tag := embeddedProvider(snapshot).SourceTag()
		label := tag
		if snapshot.Phase == codexapp.SessionPhaseStalled {
			label += " stalled"
		} else if startedAt, active := embeddedSnapshotActiveStartedAt(snapshot, project); active {
			if !startedAt.IsZero() && !now.IsZero() {
				label += " " + formatRunningDuration(now.Sub(startedAt))
			}
		}
		return label, tag, true
	}

	provider := providerForSessionFormat(project.LatestSessionFormat)
	if provider == "" {
		return "", "", false
	}
	tag := provider.SourceTag()
	if project.LatestTurnStateKnown && !project.LatestTurnCompleted {
		if !m.startupScanCompleted {
			return tag, tag, false
		}
	}
	if m.projectUnfinishedTurnLooksLive(project, now) {
		label := tag
		if !project.LatestTurnStartedAt.IsZero() && !now.IsZero() {
			label += " " + formatRunningDuration(now.Sub(project.LatestTurnStartedAt))
		}
		return label, tag, true
	}
	return tag, tag, false
}

func (m Model) projectLiveEngineerAssessmentSummary(project model.ProjectSummary, now time.Time) (string, bool) {
	if snapshot, ok := m.liveCodexSnapshot(project.Path); ok {
		startedAt, active := embeddedSnapshotActiveStartedAt(snapshot, project)
		if !active {
			return "", false
		}
		return formatLiveEngineerSummary(liveEngineerActiveSummaryDetail(snapshot, project), startedAt, now), true
	}

	provider := providerForSessionFormat(project.LatestSessionFormat)
	if provider == "" {
		return "", false
	}
	if project.LatestTurnStateKnown && !project.LatestTurnCompleted && !m.startupScanCompleted {
		return "", false
	}
	if !m.projectUnfinishedTurnLooksLive(project, now) {
		return "", false
	}
	if detail := liveEngineerProjectInProgressSummary(project); detail != "" {
		return formatLiveEngineerSummary(detail, project.LatestTurnStartedAt, now), true
	}
	return formatLiveEngineerSummary("Work in progress", project.LatestTurnStartedAt, now), true
}

func formatLiveEngineerSummary(detail string, startedAt, now time.Time) string {
	summary := strings.TrimSpace(detail)
	if !startedAt.IsZero() && !now.IsZero() {
		timer := formatRunningDuration(now.Sub(startedAt))
		if strings.TrimSpace(summary) == "" {
			return timer
		}
		summary += " (" + timer + ")"
	}
	return strings.TrimSpace(summary)
}

func liveEngineerProjectInProgressSummary(project model.ProjectSummary) string {
	if project.LatestSessionClassificationType != model.SessionCategoryInProgress {
		return ""
	}
	return liveEngineerStatusDetail(project.LatestSessionSummary)
}

func liveEngineerActiveSummaryDetail(snapshot codexapp.Snapshot, project model.ProjectSummary) string {
	if detail := liveEngineerSnapshotDetail(snapshot); detail != "" {
		return detail
	}
	if detail := liveEngineerProjectInProgressSummary(project); detail != "" {
		return detail
	}
	return "Work in progress"
}

func liveEngineerSnapshotDetail(snapshot codexapp.Snapshot) string {
	if snapshot.PendingApproval != nil {
		return liveEngineerCleanSummary("Waiting for approval: " + snapshot.PendingApproval.Summary())
	}
	if snapshot.PendingToolInput != nil {
		return liveEngineerCleanSummary("Waiting for input: " + snapshot.PendingToolInput.Summary())
	}
	if snapshot.PendingElicitation != nil {
		return liveEngineerCleanSummary("Waiting for input: " + snapshot.PendingElicitation.Summary())
	}
	if snapshot.Goal != nil && snapshot.Goal.Status == codexapp.ThreadGoalStatusActive {
		if objective := liveEngineerCleanSummary(snapshot.Goal.Objective); objective != "" {
			return objective
		}
	}
	if detail := liveEngineerTranscriptDetail(snapshot.Entries); detail != "" {
		return detail
	}
	if detail := liveEngineerStatusDetail(snapshot.LastSystemNotice); detail != "" {
		return detail
	}
	return liveEngineerStatusDetail(snapshot.Status)
}

func liveEngineerTranscriptDetail(entries []codexapp.TranscriptEntry) string {
	for i := len(entries) - 1; i >= 0; i-- {
		entry := entries[i]
		rawText := strings.TrimSpace(firstNonEmptyString(entry.DisplayText, entry.Text))
		if rawText == "" {
			continue
		}
		switch entry.Kind {
		case codexapp.TranscriptAgent, codexapp.TranscriptPlan:
			if summary := liveEngineerCompactSummary(rawText); summary != "" {
				return summary
			}
		case codexapp.TranscriptStatus, codexapp.TranscriptSystem:
			text := liveEngineerCleanSummary(rawText)
			if detail := liveEngineerStatusDetail(text); detail != "" {
				return detail
			}
		}
	}
	return ""
}

func liveEngineerCompactSummary(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	summary := engineerNoticeSummaryText(text, liveEngineerSidebarSummarySentenceLimit)
	summary = cleanEngineerNoticeSummary(compactEngineerNoticeText(summary, liveEngineerSidebarSummaryCharLimit))
	if !engineerNoticeHasUsefulDetail(summary) {
		return ""
	}
	return summary
}

func liveEngineerStatusDetail(status string) string {
	status = liveEngineerCleanSummary(status)
	if status == "" || liveEngineerGenericStatus(status) {
		return ""
	}
	return status
}

func liveEngineerCleanSummary(text string) string {
	text = sanitizeCodexRenderedText(text)
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	return singleLineStatusText(text)
}

func liveEngineerGenericStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "working", "codex is working...", "opencode is working...", "codex ready", "opencode ready", "codex turn completed", "turn completed", "conversation history compacted":
		return true
	default:
		return false
	}
}

func embeddedSnapshotActiveStartedAt(snapshot codexapp.Snapshot, project model.ProjectSummary) (time.Time, bool) {
	active := snapshot.Busy || snapshot.BusyExternal || strings.TrimSpace(snapshot.ActiveTurnID) != ""
	switch snapshot.Phase {
	case codexapp.SessionPhaseRunning, codexapp.SessionPhaseFinishing, codexapp.SessionPhaseExternal:
		active = true
	}
	if !active {
		return time.Time{}, false
	}
	startedAt := snapshot.BusySince
	if startedAt.IsZero() {
		startedAt = project.LatestTurnStartedAt
	}
	return startedAt, true
}

func projectRunSummary(snapshot projectrun.Snapshot, savedCommand string) (string, projectRunState) {
	command := effectiveRuntimeCommand(savedCommand, snapshot)
	label := projectRunCommandLabel(command)
	port := projectRunPortSummary(snapshot)
	if snapshot.Running {
		if label == "" {
			label = "run"
		}
		if port != "" {
			if len(snapshot.ConflictPorts) > 0 {
				return label + "!" + port, projectRunError
			}
			return label + "@" + port, projectRunActive
		}
		return label, projectRunActive
	}
	if strings.TrimSpace(snapshot.LastError) != "" {
		if label == "" {
			return "err", projectRunError
		}
		return label + " err", projectRunError
	}
	if snapshot.ExitCodeKnown && snapshot.ExitCode != 0 {
		if label == "" {
			return "err", projectRunError
		}
		return label + " err", projectRunError
	}
	if label != "" {
		return label, projectRunIdle
	}
	return "", projectRunIdle
}

func (m Model) projectLocalInstanceRunSummary(projectPath string) (string, bool) {
	snapshots := m.projectLocalInstanceSnapshots(projectPath)
	if len(snapshots) == 0 {
		return "", false
	}
	snapshot := snapshots[0]
	label, state := projectRunSummary(snapshot, "")
	if state == projectRunActive && len(snapshots) > 1 && strings.TrimSpace(label) != "" {
		label += fmt.Sprintf("+%d", len(snapshots)-1)
	}
	return label, state == projectRunActive && strings.TrimSpace(label) != ""
}

func projectRunPortSummary(snapshot projectrun.Snapshot) string {
	if len(snapshot.Ports) == 0 {
		return ""
	}
	switch len(snapshot.Ports) {
	case 1:
		return strconv.Itoa(snapshot.Ports[0])
	default:
		return fmt.Sprintf("%dp", len(snapshot.Ports))
	}
}

func projectRunCommandLabel(command string) string {
	tokens := strings.Fields(strings.TrimSpace(command))
	for i := 0; i < len(tokens); i++ {
		token := trimRunToken(tokens[i])
		if token == "" {
			continue
		}
		if token == "cd" {
			i++
			for i < len(tokens) {
				next := trimRunToken(tokens[i])
				if next == "&&" || next == ";" {
					break
				}
				i++
			}
			continue
		}
		if isShellEnvAssignment(token) {
			continue
		}
		tokenBase := filepath.Base(token)
		switch tokenBase {
		case "env", "command", "nohup", "time":
			continue
		case "sudo":
			for i+1 < len(tokens) {
				next := trimRunToken(tokens[i+1])
				if !strings.HasPrefix(next, "-") {
					break
				}
				i++
			}
			continue
		case "npx":
			if i+1 < len(tokens) {
				next := trimRunToken(tokens[i+1])
				if next != "" {
					return filepath.Base(next)
				}
			}
			return "npx"
		case "node", "nodejs", "bun", "deno":
			if script := commandInterpreterTargetLabel(tokens[i+1:]); script != "" {
				return script
			}
			return tokenBase
		case "python", "python2", "python3", "ruby", "perl", "php":
			if script := commandInterpreterTargetLabel(tokens[i+1:]); script != "" {
				return script
			}
			return tokenBase
		default:
			return tokenBase
		}
	}
	return ""
}

func commandInterpreterTargetLabel(tokens []string) string {
	for i := 0; i < len(tokens); i++ {
		token := trimRunToken(tokens[i])
		if token == "" {
			continue
		}
		if token == "-m" && i+1 < len(tokens) {
			module := trimRunToken(tokens[i+1])
			if module != "" {
				return filepath.Base(module)
			}
			continue
		}
		if strings.HasPrefix(token, "-") {
			continue
		}
		return filepath.Base(token)
	}
	return ""
}

func trimRunToken(token string) string {
	return strings.TrimSpace(strings.Trim(token, `"'`))
}

func isShellEnvAssignment(token string) bool {
	idx := strings.IndexByte(token, '=')
	if idx <= 0 {
		return false
	}
	key := token[:idx]
	for i, r := range key {
		if r == '_' || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
			continue
		}
		if i > 0 && r >= '0' && r <= '9' {
			continue
		}
		return false
	}
	return true
}

func truncateText(text string, width int) string {
	if width <= 0 {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= width {
		return text
	}
	if width <= 3 {
		return string(runes[:width])
	}
	return string(runes[:width-3]) + "..."
}

// projectListNameCellText keeps hierarchy/disclosure markers fixed while the
// selected project's label scrolls inside the remaining cell width.
func projectListNameCellText(prefix, label string, width int, selected bool, offset int) string {
	if width <= 0 {
		return ""
	}
	prefixWidth := ansi.StringWidth(prefix)
	if prefixWidth >= width {
		return truncateText(prefix, width)
	}
	text := prefix + label
	if !selected || ansi.StringWidth(text) <= width {
		return truncateText(text, width)
	}
	return prefix + marqueeScrollText(label, width-prefixWidth, offset)
}

// marqueeScrollText returns a width-wide window into text that scrolls
// right-to-left, wrapping around.  When text fits in width it is returned
// as-is.  The caller passes an ever-increasing offset; the helper normalises
// it so the animation loops smoothly.  The text repeats with 4 spaces
// between copies so the area is always filled.
func marqueeScrollText(text string, width int, offset int) string {
	if width <= 0 {
		return ""
	}
	runes := []rune(text)
	n := len(runes)
	if n <= width {
		if n < width {
			return text + strings.Repeat(" ", width-n)
		}
		return text
	}
	// Build a repeating ribbon: text + 4 spaces, repeated.
	const spacer = "    "
	cycle := n + len([]rune(spacer))
	// Enough repeats to cover any width-wide window at any position.
	needed := width + cycle
	repeatCount := (needed / cycle) + 1
	ribbon := make([]rune, 0, repeatCount*cycle)
	spaceRunes := []rune(spacer)
	for i := 0; i < repeatCount; i++ {
		ribbon = append(ribbon, runes...)
		ribbon = append(ribbon, spaceRunes...)
	}
	pos := ((offset % cycle) + cycle) % cycle // always non-negative
	return string(ribbon[pos : pos+width])
}

func singleLineStatusText(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, " | ")
}

func fitStyledWidth(text string, width int) string {
	if width <= 0 {
		return text
	}
	text = ansi.Truncate(text, width, "")
	if padding := width - ansi.StringWidth(ansi.Strip(text)); padding > 0 {
		text += strings.Repeat(" ", padding)
	}
	return text
}

func fitPaneContent(content string, width, height int) string {
	if height <= 0 {
		return ""
	}
	content = strings.ReplaceAll(content, "\r\n", "\n")
	lines := strings.Split(content, "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	fitted := make([]string, 0, height)
	for _, line := range lines {
		fitted = append(fitted, fitStyledWidth(line, width))
	}
	blank := strings.Repeat(" ", max(0, width))
	for len(fitted) < height {
		fitted = append(fitted, blank)
	}
	return strings.Join(fitted, "\n")
}

func renderWrappedDetailBullet(style lipgloss.Style, width int, text string) string {
	if width <= 0 {
		return style.Render("- " + text)
	}
	wrapped := lipgloss.NewStyle().Width(max(1, width-2)).Render(text)
	lines := strings.Split(strings.ReplaceAll(wrapped, "\r\n", "\n"), "\n")
	for i := range lines {
		prefix := "- "
		if i > 0 {
			prefix = "  "
		}
		lines[i] = style.Render(prefix + lines[i])
	}
	return strings.Join(lines, "\n")
}

func renderWrappedDialogTextLines(style lipgloss.Style, width int, text string) []string {
	normalized := strings.ReplaceAll(text, "\r\n", "\n")
	rawLines := strings.Split(normalized, "\n")
	out := make([]string, 0, len(rawLines))
	for _, raw := range rawLines {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			out = append(out, "")
			continue
		}
		if strings.HasPrefix(trimmed, "- ") {
			out = append(out, strings.Split(renderWrappedDetailBullet(style, width, strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))), "\n")...)
			continue
		}
		wrapped := lipgloss.NewStyle().Width(max(1, width)).Render(trimmed)
		for _, line := range strings.Split(strings.ReplaceAll(wrapped, "\r\n", "\n"), "\n") {
			out = append(out, style.Render(line))
		}
	}
	return out
}

func clampDialogContent(content string, maxLines, tailLines int, overflowLine string) string {
	if maxLines <= 0 {
		return ""
	}
	content = strings.ReplaceAll(content, "\r\n", "\n")
	lines := strings.Split(content, "\n")
	if len(lines) <= maxLines {
		return content
	}
	if overflowLine == "" {
		overflowLine = "..."
	}
	if maxLines == 1 {
		return overflowLine
	}
	if tailLines < 0 {
		tailLines = 0
	}
	if tailLines > maxLines-1 {
		tailLines = maxLines - 1
	}
	headLines := maxLines - tailLines - 1
	if headLines < 0 {
		headLines = 0
		tailLines = maxLines - 1
	}

	clamped := make([]string, 0, maxLines)
	clamped = append(clamped, lines[:headLines]...)
	clamped = append(clamped, overflowLine)
	if tailLines > 0 {
		clamped = append(clamped, lines[len(lines)-tailLines:]...)
	}
	return strings.Join(clamped, "\n")
}

func dialogOverflowHintLine(width int, text string) string {
	return commandPaletteHintStyle.Render(truncateText(strings.TrimSpace(text), max(1, width)))
}

func repoSyncWarning(status model.RepoSyncStatus) bool {
	switch status {
	case model.RepoSyncAhead, model.RepoSyncBehind, model.RepoSyncDiverged, model.RepoSyncNoUpstream:
		return true
	default:
		return false
	}
}

func repoSyncDetailLine(project model.ProjectSummary) string {
	value := repoSyncDetailValue(project)
	if value == "" {
		return ""
	}
	return "Remote: " + value
}

func (m Model) repoCombinedDetailValue(project model.ProjectSummary) string {
	var parts []string
	if op, ok := m.pendingGitOperation(project.Path); ok {
		parts = append(parts, detailValueStyle.Render(op.summaryText()))
	} else if project.RepoConflict {
		parts = append(parts, detailConflictStyle.Render("conflict"))
	} else if project.RepoDirty {
		parts = append(parts, detailWarningStyle.Render("dirty"))
	} else {
		parts = append(parts, detailMutedStyle.Render("clean"))
	}
	if projectHasSubmoduleAttention(project) && m.pendingGitSummary(project.Path) == "" {
		parts = append(parts, detailWarningStyle.Render(repoSubmoduleAttentionPlainText(project)))
	}
	if projectShowsRemoteSyncStatus(project) && m.pendingGitSummary(project.Path) == "" {
		switch project.RepoSyncStatus {
		case model.RepoSyncNoRemote:
			parts = append(parts, detailMutedStyle.Render("no remote"))
		case model.RepoSyncNoUpstream:
			parts = append(parts, repoSyncDetailStyle(project.RepoSyncStatus).Render("no upstream"))
		case model.RepoSyncSynced:
			parts = append(parts, repoSyncDetailStyle(project.RepoSyncStatus).Render("synced"))
		case model.RepoSyncAhead:
			parts = append(parts, repoSyncDetailStyle(project.RepoSyncStatus).Render(fmt.Sprintf("ahead %d", project.RepoAheadCount)))
		case model.RepoSyncBehind:
			parts = append(parts, repoSyncDetailStyle(project.RepoSyncStatus).Render(fmt.Sprintf("behind %d", project.RepoBehindCount)))
		case model.RepoSyncDiverged:
			parts = append(parts, repoSyncDetailStyle(project.RepoSyncStatus).Render(fmt.Sprintf("diverged +%d/-%d", project.RepoAheadCount, project.RepoBehindCount)))
		}
	}
	value := strings.Join(parts, ", ")
	if branch := strings.TrimSpace(project.RepoBranch); branch != "" {
		branchValue := detailValueStyle.Render("(" + branch + ")")
		if value == "" {
			return branchValue
		}
		return value + " " + branchValue
	}
	return value
}

func repoDirtyDetailValue(project model.ProjectSummary) string {
	if project.RepoConflict {
		return detailConflictStyle.Render("unmerged files")
	}
	if project.RepoDirty {
		return detailWarningStyle.Render("dirty worktree")
	}
	return detailMutedStyle.Render("clean")
}

func repoSubmoduleAttentionDetailValue(project model.ProjectSummary) string {
	return detailWarningStyle.Render(repoSubmoduleAttentionPlainText(project) + ". Use /commit to resolve submodule changes or push existing submodule commits.")
}

func repoSubmoduleAttentionPlainText(project model.ProjectSummary) string {
	dirtyCount := project.RepoSubmoduleDirtyCount
	unpushedCount := project.RepoSubmoduleUnpushedCount
	switch {
	case dirtyCount > 0 && unpushedCount > 0:
		return fmt.Sprintf("submodules %d dirty, %d unpushed", dirtyCount, unpushedCount)
	case dirtyCount > 0:
		return fmt.Sprintf("submodules %d dirty", dirtyCount)
	default:
		return fmt.Sprintf("submodules %d unpushed", unpushedCount)
	}
}

func repoConflictDetailValue(project model.ProjectSummary) string {
	location := "repo"
	if project.WorktreeKind == model.WorktreeKindLinked {
		location = "worktree"
	}
	return detailConflictStyle.Render("Unmerged files are present in this " + location + ". Use /resolve to ask a fresh engineer session for help, or resolve/abort the in-progress Git operation manually.")
}

func worktreeMergeStatusDetailValue(project model.ProjectSummary) string {
	targetBranch := strings.TrimSpace(project.WorktreeParentBranch)
	if project.RepoDirty {
		switch project.WorktreeMergeStatus {
		case model.WorktreeMergeStatusMerged:
			return detailWarningStyle.Render(worktreeNothingToMergeText() + "; local changes")
		case model.WorktreeMergeStatusMergeInProgress:
			return detailWarningStyle.Render(worktreeMergingText(targetBranch) + "; local changes")
		default:
			return detailWarningStyle.Render(worktreeCommitBeforeMergeText(targetBranch))
		}
	}
	switch project.WorktreeMergeStatus {
	case model.WorktreeMergeStatusMerged:
		return detailValueStyle.Render(worktreeNothingToMergeText())
	case model.WorktreeMergeStatusMergeInProgress:
		return detailWarningStyle.Render(worktreeMergingText(targetBranch))
	case model.WorktreeMergeStatusNotMerged:
		return detailWarningStyle.Render(worktreeReadyToMergeText(targetBranch))
	default:
		if targetBranch != "" {
			return detailMutedStyle.Render("unavailable for " + targetBranch)
		}
		return detailMutedStyle.Render("unavailable")
	}
}

func repoSyncDetailValue(project model.ProjectSummary) string {
	if !projectShowsRemoteSyncStatus(project) {
		return ""
	}
	switch project.RepoSyncStatus {
	case model.RepoSyncNoRemote:
		return detailMutedStyle.Render("none")
	case model.RepoSyncNoUpstream:
		return repoSyncDetailStyle(project.RepoSyncStatus).Render("has remote, no upstream tracking branch")
	case model.RepoSyncSynced:
		return repoSyncDetailStyle(project.RepoSyncStatus).Render("synced")
	case model.RepoSyncAhead:
		return repoSyncDetailStyle(project.RepoSyncStatus).Render(fmt.Sprintf("ahead by %d", project.RepoAheadCount))
	case model.RepoSyncBehind:
		return repoSyncDetailStyle(project.RepoSyncStatus).Render(fmt.Sprintf("behind by %d", project.RepoBehindCount))
	case model.RepoSyncDiverged:
		return repoSyncDetailStyle(project.RepoSyncStatus).Render(fmt.Sprintf("diverged (+%d/-%d)", project.RepoAheadCount, project.RepoBehindCount))
	default:
		return ""
	}
}

func projectShowsRemoteSyncStatus(project model.ProjectSummary) bool {
	if !projectUsesRepoUI(project) {
		return false
	}
	return project.WorktreeKind != model.WorktreeKindLinked
}

func repoSyncDetailStyle(status model.RepoSyncStatus) lipgloss.Style {
	switch status {
	case model.RepoSyncSynced:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true)
	case model.RepoSyncAhead, model.RepoSyncBehind, model.RepoSyncNoUpstream:
		return detailWarningStyle
	case model.RepoSyncDiverged:
		return detailDangerStyle
	case model.RepoSyncNoRemote:
		return detailMutedStyle
	default:
		return detailValueStyle
	}
}

func projectConflictIndicatorStyle(spinnerFrame int) lipgloss.Style {
	color := lipgloss.Color("141")
	if spinnerFrame%2 == 0 {
		color = lipgloss.Color("177")
	}
	return lipgloss.NewStyle().Foreground(color).Bold(true)
}

func detailField(label, value string) string {
	return detailLabelStyle.Render(label+":") + " " + value
}

func renderWrappedDetailField(label string, style lipgloss.Style, width int, text string) string {
	prefixPlain := label + ": "
	labelRendered := detailLabelStyle.Render(label + ":")
	if width <= len(prefixPlain) {
		return labelRendered + " " + style.Render(text)
	}
	wrapped := lipgloss.NewStyle().Width(max(1, width-len(prefixPlain))).Render(text)
	lines := strings.Split(strings.ReplaceAll(wrapped, "\r\n", "\n"), "\n")
	for i := range lines {
		if i == 0 {
			lines[i] = labelRendered + " " + style.Render(lines[i])
			continue
		}
		lines[i] = strings.Repeat(" ", len(prefixPlain)) + style.Render(lines[i])
	}
	return strings.Join(lines, "\n")
}

func detailReasonLine(reason model.AttentionReason) string {
	weightStyle := detailMutedStyle
	if reason.Weight > 0 {
		weightStyle = detailWarningStyle
	}
	if reason.Weight < 0 {
		weightStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true)
	}
	return detailValueStyle.Render("- ") + weightStyle.Render(fmt.Sprintf("[%+d]", reason.Weight)) + detailValueStyle.Render(" "+reason.Text)
}

func assessmentDisplayStyle(project model.ProjectSummary, now time.Time, stuckThreshold time.Duration) lipgloss.Style {
	if _, category, ok := visibleAssessmentStatusLabelAt(project, now, stuckThreshold); ok {
		if projectAssessmentRefreshing(project) {
			return detailMutedStyle
		}
		style := classificationCategoryStyle(category)
		if projectAssessmentUnread(project, now, stuckThreshold) {
			return style
		}
		return style.Bold(false).Faint(true)
	}
	return detailMutedStyle
}

func projectAssessmentLabelAt(project model.ProjectSummary, now time.Time) string {
	return projectAssessmentLabelWithThreshold(project, now, 0)
}

func projectAssessmentLabelWithThreshold(project model.ProjectSummary, now time.Time, stuckThreshold time.Duration) string {
	if label, _, ok := visibleAssessmentStatusLabelAt(project, now, stuckThreshold); ok {
		return label
	}
	if projectAssessmentRefreshing(project) {
		if label := classificationProgressText(project.LatestSessionClassification, project.LatestSessionClassificationStage, project.LatestSessionClassificationStageStartedAt, project.LatestSessionClassificationUpdatedAt, now, false); label != "" {
			return label
		}
		return "assessment"
	}
	if projectAssessmentFailed(project) {
		return "failed"
	}
	if project.LatestSessionFormat != "" {
		return "not assessed yet"
	}
	return "not assessed"
}

func projectListStatus(project model.ProjectSummary) string {
	return projectListStatusAt(project, time.Time{}, 0)
}

func projectListStatusAt(project model.ProjectSummary, now time.Time, stuckThreshold time.Duration) string {
	if projectMissing(project) {
		return "missing"
	}
	if moveStatusActive(project.MovedAt, project.Path, project.LatestSessionDetectedProjectPath) {
		return "moved"
	}
	if label, _, ok := visibleAssessmentStatusLabelAt(project, now, stuckThreshold); ok {
		return label
	}
	if projectAssessmentRefreshing(project) {
		if project.LatestSessionClassification == model.ClassificationPending {
			return "queued"
		}
		return classificationProgressCompactLabel(project.LatestSessionClassificationStage)
	}
	if projectAssessmentFailed(project) {
		return "failed"
	}
	if project.LatestSessionFormat != "" {
		return "new"
	}
	return projectActivityStatus(project)
}

func projectUnreadIndicator(project model.ProjectSummary, now time.Time, stuckThreshold time.Duration) string {
	if !projectAssessmentUnread(project, now, stuckThreshold) {
		return " "
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color("229")).Bold(true).Render("u")
}

func classificationProgressText(status model.SessionClassificationStatus, stage model.SessionClassificationStage, stageStartedAt, updatedAt, now time.Time, includeAssessmentPrefix bool) string {
	label := classificationProgressStageLabel(status, stage)
	if label == "" {
		if includeAssessmentPrefix {
			return "assessment"
		}
		return ""
	}
	if includeAssessmentPrefix {
		label = "assessment " + label
	}
	startedAt := classificationProgressStartedAt(stageStartedAt, updatedAt)
	if startedAt.IsZero() || now.IsZero() {
		return label
	}
	return label + " " + formatRunningDuration(now.Sub(startedAt))
}

func classificationProgressStageLabel(status model.SessionClassificationStatus, stage model.SessionClassificationStage) string {
	switch status {
	case model.ClassificationPending:
		return "queued"
	case model.ClassificationRunning:
		switch stage {
		case model.ClassificationStagePreparingSnapshot:
			return "preparing snapshot"
		case model.ClassificationStageWaitingForModel:
			return "waiting for model"
		default:
			return "running"
		}
	default:
		return ""
	}
}

func classificationProgressStartedAt(stageStartedAt, updatedAt time.Time) time.Time {
	if !stageStartedAt.IsZero() {
		return stageStartedAt
	}
	return updatedAt
}

func classificationFailureText(classification *model.SessionClassification) string {
	if classification == nil {
		return "assessment failed"
	}
	label := "assessment failed"
	if stageLabel := classificationProgressStageLabel(model.ClassificationRunning, classification.Stage); stageLabel != "" {
		label += " during " + stageLabel
	}
	if strings.TrimSpace(classification.LastError) == "" {
		return label
	}
	return label + ": " + classification.LastError
}

func (m *Model) appendBackgroundErrorLogEntry(status string, err error, projectPath string) errorLogAppendResult {
	if err == nil {
		return errorLogAppendResult{Status: errorSummaryText(status)}
	}
	result := m.appendErrorLogEntry(status, err, projectPath)
	if m.errorLogVisible {
		return result
	}
	m.status = errorStatusWithHint(result.Status)
	return result
}

func classificationUpdateStatus(payload map[string]string) string {
	switch strings.TrimSpace(payload["error_kind"]) {
	case "timeout":
		return "Assessment timed out"
	case "connection_failed":
		return "Assessment connection failed"
	case "open_file_limit":
		return "Assessment hit open-file limit"
	case "rate_limited":
		return "Assessment rate limited"
	case "service_unavailable":
		return "Assessment service unavailable"
	case "backend_unavailable":
		return "Assessment backend unavailable"
	default:
		return "Assessment failed"
	}
}

func classificationUpdateError(payload map[string]string) error {
	errText := strings.TrimSpace(payload["error"])
	if errText == "" {
		return nil
	}
	err := errors.New(errText)
	if diagnosis := strings.TrimSpace(payload["error_diagnosis"]); diagnosis != "" {
		err = fmt.Errorf("%s: %w", diagnosis, err)
	}
	if modelName := strings.TrimSpace(payload["model"]); modelName != "" {
		err = fmt.Errorf("model %s: %w", modelName, err)
	}
	if stage := humanizeStatusToken(payload["stage"]); stage != "" {
		err = fmt.Errorf("classification stage %s: %w", stage, err)
	}
	return err
}

func todoSuggestionEventError(payload map[string]string) error {
	errText := strings.TrimSpace(payload["error"])
	if errText == "" {
		return nil
	}
	err := errors.New(errText)
	if modelName := strings.TrimSpace(payload["model"]); modelName != "" {
		err = fmt.Errorf("model %s: %w", modelName, err)
	}
	return err
}

func humanizeStatusToken(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	value = strings.ReplaceAll(value, "_", " ")
	value = strings.ReplaceAll(value, "-", " ")
	return strings.Join(strings.Fields(value), " ")
}

func activityDisplayStyle(project model.ProjectSummary) lipgloss.Style {
	if projectMissing(project) {
		return lipgloss.NewStyle().Foreground(lipgloss.Color("178")).Bold(true)
	}
	if moveStatusActive(project.MovedAt, project.Path, project.LatestSessionDetectedProjectPath) {
		return lipgloss.NewStyle().Foreground(lipgloss.Color("229")).Bold(true)
	}
	return statusStyle(project.Status)
}

func projectActivityStatus(project model.ProjectSummary) string {
	if projectMissing(project) {
		return "missing"
	}
	if moveStatusActive(project.MovedAt, project.Path, project.LatestSessionDetectedProjectPath) {
		return "moved"
	}
	return attentionStatusLabel(project.Status)
}

func shouldShowProjectActivity(project model.ProjectSummary) bool {
	if projectMissing(project) || moveStatusActive(project.MovedAt, project.Path, project.LatestSessionDetectedProjectPath) {
		return true
	}
	return project.Status != model.StatusIdle
}

func visibilityLabel(mode projectVisibilityMode) string {
	switch mode {
	case visibilityAllFolders:
		return "All folders"
	default:
		return "AI folders"
	}
}

func visibilityShortLabel(mode projectVisibilityMode) string {
	switch mode {
	case visibilityAllFolders:
		return "all"
	default:
		return "AI"
	}
}

func projectArchiveLabel(mode projectArchiveMode) string {
	switch mode {
	case projectArchiveArchived:
		return "Archived"
	case projectArchiveCategory:
		return "Category"
	default:
		return "Main"
	}
}

func (m Model) currentProjectTabLabel() string {
	if m.archiveMode == projectArchiveCategory {
		if category, ok := m.projectCategoryByID(m.selectedCategoryID); ok {
			if m.privacyMode && category.Private {
				return "********"
			}
			return category.Name
		}
		return "Main"
	}
	return projectArchiveLabel(m.archiveMode)
}

func projectHasAIMetadata(project model.ProjectSummary) bool {
	return project.ManuallyAdded || !project.LastActivity.IsZero() || project.LatestSessionFormat != "" || project.LatestSessionClassification != ""
}

func projectMissing(project model.ProjectSummary) bool {
	return !project.PresentOnDisk
}

func filterProjects(projects []model.ProjectSummary, mode projectVisibilityMode, excludeProjectPatterns []string, projectFilter string) []model.ProjectSummary {
	if mode == visibilityAllFolders {
		return filterProjectsByFilter(filterProjectsByName(projects, excludeProjectPatterns), projectFilter)
	}
	filtered := make([]model.ProjectSummary, 0, len(projects))
	for _, project := range projects {
		if projectHasAIMetadata(project) {
			filtered = append(filtered, project)
		}
	}
	return filterProjectsByFilter(filterProjectsByName(filtered, excludeProjectPatterns), projectFilter)
}

func expandVisibleWorktreeFamilies(filtered, sorted []model.ProjectSummary) []model.ProjectSummary {
	if len(filtered) == 0 {
		return nil
	}

	includePaths := make(map[string]struct{}, len(filtered))
	visibleRoots := map[string]struct{}{}
	for _, project := range filtered {
		path := filepath.Clean(strings.TrimSpace(project.Path))
		if path == "" || path == "." {
			continue
		}
		includePaths[path] = struct{}{}
		if projectIsWorktreeRoot(project) {
			visibleRoots[projectWorktreeRootPath(project)] = struct{}{}
		}
	}
	if len(visibleRoots) == 0 {
		return append([]model.ProjectSummary(nil), filtered...)
	}

	out := make([]model.ProjectSummary, 0, len(sorted))
	for _, project := range sorted {
		path := filepath.Clean(strings.TrimSpace(project.Path))
		if path == "" || path == "." {
			continue
		}
		if _, ok := includePaths[path]; ok {
			out = append(out, project)
			continue
		}
		if !projectParticipatesInWorktreeFamily(project) {
			continue
		}
		if _, ok := visibleRoots[projectWorktreeRootPath(project)]; ok {
			out = append(out, project)
		}
	}
	return out
}

func filterProjectsByPrivacy(projects []model.ProjectSummary) []model.ProjectSummary {
	if len(projects) == 0 {
		return projects
	}
	filtered := make([]model.ProjectSummary, 0, len(projects))
	for _, project := range projects {
		if !project.CategoryPrivate {
			filtered = append(filtered, project)
		}
	}
	return filtered
}

func (m Model) projectsVisibleForPrivacy(projects []model.ProjectSummary) []model.ProjectSummary {
	if !m.privacyMode {
		return projects
	}
	return filterProjectsByPrivacy(projects)
}

func filterProjectsByName(projects []model.ProjectSummary, excludeProjectPatterns []string) []model.ProjectSummary {
	if len(projects) == 0 {
		return nil
	}
	if len(excludeProjectPatterns) == 0 {
		return append([]model.ProjectSummary(nil), projects...)
	}
	filtered := make([]model.ProjectSummary, 0, len(projects))
	for _, project := range projects {
		if projectMatchesExcludedName(project, excludeProjectPatterns) {
			continue
		}
		filtered = append(filtered, project)
	}
	return filtered
}

func filterProjectsByFilter(projects []model.ProjectSummary, projectFilter string) []model.ProjectSummary {
	projectFilter = strings.TrimSpace(projectFilter)
	if len(projects) == 0 {
		return nil
	}
	if projectFilter == "" {
		return append([]model.ProjectSummary(nil), projects...)
	}
	filtered := make([]model.ProjectSummary, 0, len(projects))
	for _, project := range projects {
		if !projectMatchesFilter(project, projectFilter) {
			continue
		}
		filtered = append(filtered, project)
	}
	return filtered
}

func projectMatchesExcludedName(project model.ProjectSummary, excludeProjectPatterns []string) bool {
	if config.ProjectNameExcluded(project.Name, excludeProjectPatterns) {
		return true
	}
	base := filepath.Base(filepath.Clean(project.Path))
	if strings.EqualFold(strings.TrimSpace(base), strings.TrimSpace(project.Name)) {
		return false
	}
	return config.ProjectNameExcluded(base, excludeProjectPatterns)
}

func projectMatchesFilter(project model.ProjectSummary, projectFilter string) bool {
	projectFilter = strings.TrimSpace(projectFilter)
	if projectFilter == "" {
		return true
	}

	return fuzzyfilter.Match(projectFilter,
		project.Name,
		filepath.Base(filepath.Clean(project.Path)),
	)
}
