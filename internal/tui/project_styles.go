package tui

import (
	"fmt"
	"github.com/charmbracelet/lipgloss"
	"lcroom/internal/attention"
	"lcroom/internal/model"
	"lcroom/internal/sessionclassify"
	"lcroom/internal/uisurface"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func formatSnoozeDuration(d time.Duration) string {
	switch d {
	case time.Hour:
		return "1 hour"
	case 4 * time.Hour:
		return "4 hours"
	case 24 * time.Hour:
		return "24 hours"
	}
	if d%(24*time.Hour) == 0 {
		days := int(d / (24 * time.Hour))
		if days == 1 {
			return "1 day"
		}
		return fmt.Sprintf("%d days", days)
	}
	return d.String()
}

func (m Model) renderHelpPanel(bodyW int) string {
	panelWidth := min(bodyW, min(max(58, bodyW-12), 80))
	panelInnerWidth := max(30, panelWidth-4)
	contentLines := []string{commandPaletteTitleStyle.Render("Help")}
	contentLines = append(contentLines, helpPanelLines()...)
	content := lipgloss.NewStyle().
		Width(panelInnerWidth).
		Render(strings.Join(contentLines, "\n"))
	return lipgloss.NewStyle().
		Width(panelWidth).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("214")).
		Padding(0, 1, 1, 1).
		Background(lipgloss.Color("234")).
		Foreground(lipgloss.Color("252")).
		Render(content)
}

func (m Model) renderHelpPanelOverlay(body string, bodyW, bodyH int) string {
	panel := m.renderHelpPanel(bodyW)
	panelWidth := lipgloss.Width(panel)
	panelHeight := lipgloss.Height(panel)
	left := max(0, (bodyW-panelWidth)/2)
	top := max(0, (bodyH-panelHeight)/5)
	return overlayBlock(body, panel, bodyW, bodyH, left, top)
}

func (m Model) classificationTag(project model.ProjectSummary) string {
	switch project.LatestSessionClassification {
	case model.ClassificationCompleted:
		return "OK"
	case model.ClassificationPending:
		return "Q"
	case model.ClassificationRunning:
		return spinnerFrames[m.spinnerFrame%len(spinnerFrames)]
	case model.ClassificationFailed:
		return "ERR"
	default:
		if project.LatestSessionFormat == "" {
			return "--"
		}
		return ".."
	}
}

func projectDisplayStatus(project model.ProjectSummary) string {
	return projectActivityStatus(project)
}

func recentlyMoved(movedAt time.Time) bool {
	if movedAt.IsZero() {
		return false
	}
	age := time.Since(movedAt)
	return age >= -time.Minute && age <= recentMoveWindow
}

func moveStatusActive(movedAt time.Time, currentPath, latestDetectedPath string) bool {
	if !recentlyMoved(movedAt) {
		return false
	}
	if latestDetectedPath == "" {
		return true
	}
	return filepath.Clean(latestDetectedPath) != filepath.Clean(currentPath)
}

func statusDisplayStyle(project model.ProjectSummary, now time.Time, stuckThreshold time.Duration) lipgloss.Style {
	if projectMissing(project) || moveStatusActive(project.MovedAt, project.Path, project.LatestSessionDetectedProjectPath) {
		return activityDisplayStyle(project)
	}
	if _, _, ok := visibleAssessmentStatusLabelAt(project, now, stuckThreshold); ok {
		return assessmentDisplayStyle(project, now, stuckThreshold)
	}
	if project.LatestSessionFormat != "" {
		return detailMutedStyle
	}
	return activityDisplayStyle(project)
}

func assessmentFlashStyle(style lipgloss.Style) lipgloss.Style {
	return style.Foreground(lipgloss.Color("16")).Background(lipgloss.Color("186")).Bold(true)
}

func (m Model) projectListAssessmentStatusStyle(project model.ProjectSummary) lipgloss.Style {
	style := statusDisplayStyle(project, m.currentTime(), m.assessmentStallThreshold())
	if m.assessmentFlashActive(project.Path) {
		return assessmentFlashStyle(style)
	}
	return style
}

func (m Model) projectListAssessmentSummaryStyle(project model.ProjectSummary) lipgloss.Style {
	style := projectAssessmentStyle(project, m.currentTime(), m.assessmentStallThreshold())
	if m.assessmentFlashActive(project.Path) {
		return assessmentFlashStyle(style)
	}
	return style
}

func effectiveAssessmentForProject(project model.ProjectSummary, now time.Time, stuckThreshold time.Duration) sessionclassify.EffectiveAssessment {
	return uisurface.EffectiveAssessmentForProject(project, now, stuckThreshold)
}

func projectAssessmentUnread(project model.ProjectSummary, now time.Time, stuckThreshold time.Duration) bool {
	return !projectAssessmentUnreadAt(project, now, stuckThreshold).IsZero() && attention.AssessmentUnread(project)
}

func projectAssessmentUnreadAt(project model.ProjectSummary, now time.Time, stuckThreshold time.Duration) time.Time {
	if _, _, ok := visibleAssessmentStatusLabelAt(project, now, stuckThreshold); !ok {
		return time.Time{}
	}
	return attention.AssessmentUnreadAt(project)
}

func assessmentStatusLabel(project model.ProjectSummary, compact bool) (string, model.SessionCategory, bool) {
	return assessmentStatusLabelAt(project, compact, time.Time{}, 0)
}

func assessmentStatusLabelAt(project model.ProjectSummary, compact bool, now time.Time, stuckThreshold time.Duration) (string, model.SessionCategory, bool) {
	effective := effectiveAssessmentForProject(project, now, stuckThreshold)
	if effective.Status != model.ClassificationCompleted {
		return "", model.SessionCategoryUnknown, false
	}
	return assessmentStatusLabelForCategory(effective.Category, compact)
}

func assessmentStatusLabelForCategory(category model.SessionCategory, compact bool) (string, model.SessionCategory, bool) {
	_ = compact
	label, ok := uisurface.AssessmentCompactLabel(category)
	if !ok {
		return "", model.SessionCategoryUnknown, false
	}
	return label, category, true
}

func assessmentCategoryHasLabel(category model.SessionCategory) bool {
	_, _, ok := assessmentStatusLabelForCategory(category, false)
	return ok
}

func latestCompletedAssessmentStatusLabel(project model.ProjectSummary, compact bool) (string, model.SessionCategory, bool) {
	if project.LatestCompletedSessionClassificationType == "" || project.LatestCompletedSessionClassificationType == model.SessionCategoryUnknown {
		return "", model.SessionCategoryUnknown, false
	}
	return assessmentStatusLabelForCategory(project.LatestCompletedSessionClassificationType, compact)
}

func visibleAssessmentStatusLabel(project model.ProjectSummary) (string, model.SessionCategory, bool) {
	return visibleAssessmentStatusLabelAt(project, time.Time{}, 0)
}

func visibleAssessmentStatusLabelAt(project model.ProjectSummary, now time.Time, stuckThreshold time.Duration) (string, model.SessionCategory, bool) {
	if label, category, ok := assessmentStatusLabelAt(project, false, now, stuckThreshold); ok {
		return label, category, true
	}
	if project.LatestSessionClassification == model.ClassificationFailed && strings.TrimSpace(project.LatestSessionSummary) != "" {
		if label, category, ok := assessmentStatusLabelForCategory(project.LatestSessionClassificationType, false); ok {
			return label, category, true
		}
	}
	if projectAssessmentRefreshing(project) {
		if strings.TrimSpace(project.LatestSessionSummary) != "" {
			if label, category, ok := assessmentStatusLabelForCategory(project.LatestSessionClassificationType, false); ok {
				return label, category, true
			}
		}
		return "", model.SessionCategoryUnknown, false
	}
	if projectAssessmentFailed(project) {
		return "", model.SessionCategoryUnknown, false
	}
	return latestCompletedAssessmentStatusLabel(project, false)
}

func projectAssessmentRefreshing(project model.ProjectSummary) bool {
	switch project.LatestSessionClassification {
	case model.ClassificationPending, model.ClassificationRunning:
		return true
	default:
		return false
	}
}

func projectAssessmentFailed(project model.ProjectSummary) bool {
	return project.LatestSessionClassification == model.ClassificationFailed
}

func projectAssessmentUsesLatestSummary(project model.ProjectSummary) bool {
	switch project.LatestSessionClassification {
	case model.ClassificationCompleted, model.ClassificationFailed:
		return true
	default:
		return projectAssessmentRefreshing(project)
	}
}

func classificationProgressCompactLabel(stage model.SessionClassificationStage) string {
	switch stage {
	case model.ClassificationStagePreparingSnapshot:
		return "snapshot"
	case model.ClassificationStageWaitingForModel:
		return "model"
	default:
		return "running"
	}
}

func attentionStatusLabel(status model.ProjectStatus) string {
	switch status {
	case model.StatusPossiblyStuck:
		return "stuck"
	default:
		return string(status)
	}
}

func statusStyle(status model.ProjectStatus) lipgloss.Style {
	switch status {
	case model.StatusActive:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true)
	case model.StatusPossiblyStuck:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Bold(true)
	default:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	}
}

func classificationStyle(status model.SessionClassificationStatus) lipgloss.Style {
	switch status {
	case model.ClassificationCompleted:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true)
	case model.ClassificationPending:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true)
	case model.ClassificationRunning:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("81")).Bold(true)
	case model.ClassificationFailed:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Bold(true)
	default:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	}
}

func classificationCategoryStyle(category model.SessionCategory) lipgloss.Style {
	switch category {
	case model.SessionCategoryCompleted:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true)
	case model.SessionCategoryBlocked:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Bold(true)
	case model.SessionCategoryWaitingForUser:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true)
	case model.SessionCategoryNeedsFollowUp:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("81")).Bold(true)
	case model.SessionCategoryInProgress:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("229")).Bold(true)
	default:
		return detailMutedStyle
	}
}

func projectRunStyle(state projectRunState) lipgloss.Style {
	switch state {
	case projectRunActive:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("81")).Bold(true)
	case projectRunError:
		return detailDangerStyle
	default:
		return detailMutedStyle
	}
}

func sessionCategoryLabel(category model.SessionCategory) string {
	switch category {
	case model.SessionCategoryCompleted:
		return "done"
	case model.SessionCategoryBlocked:
		return "blocked"
	case model.SessionCategoryWaitingForUser:
		return "waiting"
	case model.SessionCategoryNeedsFollowUp:
		return "followup"
	case model.SessionCategoryInProgress:
		return "working"
	case model.SessionCategoryUnknown:
		return "unknown"
	default:
		label := strings.ReplaceAll(string(category), "_", " ")
		if strings.TrimSpace(label) == "" {
			return "unknown"
		}
		return label
	}
}

func sourceStyle(format string, live bool) lipgloss.Style {
	return sourceStyleForTag(sourceTag(format), live)
}

func sourceStyleForTag(tag string, live bool) lipgloss.Style {
	switch tag {
	case "CX":
		style := lipgloss.NewStyle().Foreground(lipgloss.Color("81")).Bold(true)
		if !live {
			style = style.Foreground(lipgloss.Color("66")).Faint(true)
		}
		return style
	case "OC":
		style := lipgloss.NewStyle().Foreground(lipgloss.Color("208")).Bold(true)
		if !live {
			style = style.Foreground(lipgloss.Color("172")).Faint(true)
		}
		return style
	case "CC":
		style := lipgloss.NewStyle().Foreground(lipgloss.Color("215")).Bold(true)
		if !live {
			style = style.Foreground(lipgloss.Color("137")).Faint(true)
		}
		return style
	case "LA":
		style := lipgloss.NewStyle().Foreground(lipgloss.Color("120")).Bold(true)
		if !live {
			style = style.Foreground(lipgloss.Color("71")).Faint(true)
		}
		return style
	default:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	}
}

func sourceTag(format string) string {
	switch format {
	case "modern", "legacy":
		return "CX"
	case "opencode_db":
		return "OC"
	case "claude_code":
		return "CC"
	case "lcagent_jsonl":
		return "LA"
	default:
		return "--"
	}
}

func sourceLabel(format string) string {
	if label := uisurface.SourceLabel(format); label != "" {
		return label
	}
	return "None"
}

func (m *Model) sortProjects(projects []model.ProjectSummary) {
	attentionScores := make(map[string]int, len(projects))
	attentionScoreFor := func(project model.ProjectSummary) int {
		if score, ok := attentionScores[project.Path]; ok {
			return score
		}
		score := m.projectAttentionScore(project)
		attentionScores[project.Path] = score
		return score
	}

	switch m.sortMode {
	case sortByRecent:
		sort.SliceStable(projects, func(i, j int) bool {
			li := projects[i].LastActivity
			lj := projects[j].LastActivity
			if li.IsZero() != lj.IsZero() {
				return !li.IsZero()
			}
			if !li.Equal(lj) {
				return li.After(lj)
			}
			scoreI := attentionScoreFor(projects[i])
			scoreJ := attentionScoreFor(projects[j])
			if scoreI != scoreJ {
				return scoreI > scoreJ
			}
			return projects[i].Name < projects[j].Name
		})
	default:
		sort.SliceStable(projects, func(i, j int) bool {
			scoreI := attentionScoreFor(projects[i])
			scoreJ := attentionScoreFor(projects[j])
			if scoreI != scoreJ {
				return scoreI > scoreJ
			}
			li := projects[i].LastActivity
			lj := projects[j].LastActivity
			if li.IsZero() != lj.IsZero() {
				return !li.IsZero()
			}
			if !li.Equal(lj) {
				return li.After(lj)
			}
			return projects[i].Name < projects[j].Name
		})
	}
}

func (m *Model) indexByPath(path string) int {
	for i, p := range m.projects {
		if p.Path == path {
			return i
		}
	}
	return -1
}

func (m *Model) rowsVisible() int {
	layout := m.bodyLayout()
	inner := layout.listPaneHeight - 2 // border
	if inner < 3 {
		inner = 3
	}
	rows := inner - 1 // list header
	if rows < 1 {
		return 1
	}
	return rows
}

func (m *Model) ensureSelectionVisible() {
	if len(m.projects) == 0 {
		m.selected = 0
		m.offset = 0
		return
	}
	if m.selected < 0 {
		m.selected = 0
	}
	if m.selected >= len(m.projects) {
		m.selected = len(m.projects) - 1
	}
	visible := m.rowsVisible()
	maxOffset := max(0, len(m.projects)-visible)
	if m.offset > maxOffset {
		m.offset = maxOffset
	}
	if m.selected < m.offset {
		m.offset = m.selected
	}
	if m.selected >= m.offset+visible {
		m.offset = m.selected - visible + 1
	}
	if m.offset < 0 {
		m.offset = 0
	}
	if m.offset > maxOffset {
		m.offset = maxOffset
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
