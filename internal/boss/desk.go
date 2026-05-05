package boss

import (
	"fmt"
	"strings"
	"time"

	"lcroom/internal/model"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

const (
	bossDeskActiveLimit   = 4
	bossDeskDecisionLimit = 4
	bossDeskWatchingLimit = 5
	bossLogLimit          = 7
	maxBossDeskEvents     = 16
	bossDeskStateMinRows  = 2
)

type bossDeskEvent struct {
	At      time.Time
	Kind    string
	Label   string
	Summary string
}

func (m Model) renderBossSidebar(width, height int) string {
	return m.renderRawPanel("Boss Desk", m.bossSidebarContent(width, height), width, height)
}

func (m Model) renderBossLog(width, height int) string {
	return m.renderRawPanel("Boss Log", m.bossLogContent(width, height), width, height)
}

func (m Model) deskContent(width, height int) string {
	return m.bossSidebarContent(width, height)
}

func (m Model) bossSidebarContent(width, height int) string {
	innerWidth := bossPanelInnerWidth(width)
	bodyHeight := maxInt(1, height-4)
	lines := m.bossSidebarBodyLines(innerWidth, bodyHeight)
	if len(lines) == 0 {
		return bossMutedStyle.Render(fitLine("Nothing on the desk yet.", innerWidth))
	}
	return strings.Join(lines, "\n")
}

func (m Model) bossSidebarBodyLines(width, height int) []string {
	avatarLines := m.bossSidebarCompanionLines(width, maxInt(0, height-bossDeskStateMinRows-1))
	reservedAvatarHeight := 0
	if len(avatarLines) > 0 && height >= len(avatarLines)+bossDeskStateMinRows+1 {
		reservedAvatarHeight = len(avatarLines) + 1
	}
	stateHeight := maxInt(1, height-reservedAvatarHeight)
	lines := m.bossSidebarLines(width, stateHeight)
	if len(lines) > stateHeight {
		lines = lines[:stateHeight]
	}
	if reservedAvatarHeight == 0 {
		return lines
	}
	for len(lines)+len(avatarLines) < height {
		lines = append(lines, "")
	}
	return append(lines, avatarLines...)
}

func (m Model) bossSidebarLines(width, height int) []string {
	width = maxInt(24, width)
	now := m.now()
	var lines []string
	nowLimit, decisionLimit, watchingLimit := bossSidebarSectionLimits(height)

	if rows := takeDeskRows(m.bossDeskNowRows(width, now), nowLimit); len(rows) > 0 {
		lines = appendDeskSection(lines, "Now", rows, width)
	}
	needsUser := append([]string{}, m.bossDeskNeedsUserRows(width, now)...)
	needsUser = append(needsUser, m.bossDeskReadyRows(width, now)...)
	if rows := takeDeskRows(needsUser, decisionLimit); len(rows) > 0 {
		lines = appendDeskSection(lines, "Needs You", rows, width)
	}
	if next := m.bossDeskNextLine(width, now); next != "" {
		lines = appendDeskSection(lines, "Next", []string{next}, width)
	}
	if rows := takeDeskRows(m.bossDeskWatchingRows(width, now), watchingLimit); len(rows) > 0 {
		lines = appendDeskSection(lines, "Watching", rows, width)
	}
	return lines
}

func (m Model) bossLogContent(width, height int) string {
	innerWidth := bossPanelInnerWidth(width)
	bodyHeight := maxInt(1, height-4)
	rows := m.bossLogRows(innerWidth, bodyHeight)
	if len(rows) == 0 {
		return bossMutedStyle.Render(fitLine("No Boss actions yet.", innerWidth))
	}
	return strings.Join(rows, "\n")
}

func (m Model) bossLogRows(width int, limit int) []string {
	limit = clampInt(limit, 1, bossLogLimit)
	events := m.deskEvents
	if len(events) == 0 {
		return nil
	}
	start := maxInt(0, len(events)-limit)
	now := m.now()
	rows := make([]string, 0, len(events)-start)
	for _, event := range events[start:] {
		summary := strings.TrimSpace(event.Summary)
		if summary == "" {
			continue
		}
		label := bossLogEventTimeLabel(event.At, now)
		if label == "" {
			label = firstNonEmpty(strings.TrimSpace(event.Label), "event")
		}
		rows = append(rows, bossDeskRow(bossDeskEventStyle(event.Label), label, summary, width))
	}
	return rows
}

func (m Model) bossSidebarCompanionLines(width, maxRows int) []string {
	if width < 14 || maxRows <= 0 {
		return nil
	}
	sprite := renderBossCompanionSprite(m.bossCompanionMood(), m.spinnerFrame, width)
	rows := sprite.renderHalfRows()
	if len(rows) == 0 {
		return nil
	}
	if len(rows) > maxRows {
		return nil
	}
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		rowWidth := ansi.StringWidth(ansi.Strip(row))
		if rowWidth > width {
			return nil
		}
		leftPad := maxInt(0, (width-rowWidth)/2)
		rightPad := maxInt(0, width-leftPad-rowWidth)
		out = append(out, fitStyledLine(bossCompanionPanelSpaces(leftPad)+row+bossCompanionPanelSpaces(rightPad), width))
	}
	return out
}

func bossSidebarSectionLimits(height int) (nowLimit, decisionLimit, watchingLimit int) {
	switch {
	case height <= 6:
		return 1, 1, 1
	case height <= 9:
		return 1, 2, 1
	default:
		return bossDeskActiveLimit, bossDeskDecisionLimit, bossDeskWatchingLimit
	}
}

func takeDeskRows(rows []string, limit int) []string {
	if limit <= 0 || len(rows) == 0 {
		return nil
	}
	if len(rows) <= limit {
		return rows
	}
	return rows[:limit]
}

func appendDeskSection(lines []string, title string, rows []string, width int) []string {
	if len(rows) == 0 {
		return lines
	}
	lines = append(lines, bossDeskSectionStyle.Render(fitLine(title, width)))
	lines = append(lines, rows...)
	return lines
}

func (m Model) bossDeskNowRows(width int, now time.Time) []string {
	activities := m.activeEngineerActivities()
	rows := make([]string, 0, minInt(len(activities), bossDeskActiveLimit))
	for _, activity := range activities {
		if len(rows) >= bossDeskActiveLimit {
			break
		}
		if quietFor := supervisorActivityQuietFor(activity, now); quietFor >= bossSupervisorQuietAfter {
			if text := supervisorActivityLine(activity, now); text != "" {
				rows = append(rows, bossDeskRow(bossAssessmentWaitingStyle, "quiet", text, width))
			}
			continue
		}
		label, style := bossEngineerActivityCell(activity, now)
		title := strings.TrimSpace(firstNonEmpty(activity.Title, activity.ProjectPath, activity.TaskID, "engineer work"))
		name := strings.TrimSpace(firstNonEmpty(activity.EngineerName, "Engineer"))
		text := name + " on " + title
		rows = append(rows, bossDeskRow(style, label, text, width))
	}
	return rows
}

func (m Model) bossDeskNeedsUserRows(width int, now time.Time) []string {
	rows := make([]string, 0, bossDeskDecisionLimit)
	for _, task := range m.snapshot.OpenAgentTasks {
		if len(rows) >= bossDeskDecisionLimit {
			break
		}
		if model.NormalizeAgentTaskStatus(task.Status) != model.AgentTaskStatusWaiting {
			continue
		}
		title := compactAgentTaskTitle(task)
		detail := strings.TrimSpace(task.Summary)
		if detail == "" {
			detail = agentTaskDecisionQuestion(task.EngineerName)
		}
		rows = append(rows, bossDeskRow(bossAssessmentWaitingStyle, "review", bossDeskTextWithDetail(title, detail), width))
	}
	for _, notice := range m.bossDeskSystemNotices() {
		if len(rows) >= bossDeskDecisionLimit {
			break
		}
		if strings.TrimSpace(notice.Severity) != "warning" {
			continue
		}
		code := strings.TrimSpace(notice.Code)
		if code == "" {
			code = "notice"
		}
		rows = append(rows, bossDeskRow(bossAssessmentWaitingStyle, deskNoticeLabel(code), notice.Summary, width))
	}
	return rows
}

func (m Model) bossDeskReadyRows(width int, now time.Time) []string {
	rows := make([]string, 0, bossDeskDecisionLimit)
	for _, task := range m.snapshot.OpenAgentTasks {
		if len(rows) >= bossDeskDecisionLimit {
			break
		}
		if model.NormalizeAgentTaskStatus(task.Status) != model.AgentTaskStatusCompleted {
			continue
		}
		title := compactAgentTaskTitle(task)
		detail := strings.TrimSpace(task.Summary)
		if detail == "" {
			detail = "ready to close"
		}
		rows = append(rows, bossDeskRow(bossAssessmentDoneStyle, "close", bossDeskTextWithDetail(title, detail), width))
	}
	return rows
}

func (m Model) bossDeskWatchingRows(width int, now time.Time) []string {
	rows := make([]string, 0, bossDeskWatchingLimit)
	for _, notice := range m.bossDeskSystemNotices() {
		if len(rows) >= bossDeskWatchingLimit {
			break
		}
		if strings.TrimSpace(notice.Severity) == "warning" {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(notice.Code), "control_") || strings.TrimSpace(notice.Code) == "host_update" {
			continue
		}
		code := strings.TrimSpace(notice.Code)
		if code == "" {
			code = "notice"
		}
		rows = append(rows, bossDeskRow(bossMutedStyle, deskNoticeLabel(code), notice.Summary, width))
	}
	for _, project := range m.snapshot.HotProjects {
		if len(rows) >= bossDeskWatchingLimit {
			break
		}
		label, style := bossAssessmentCell(project)
		if project.AttentionScore > 0 && label == "-" {
			label = fmt.Sprintf("%d", project.AttentionScore)
		}
		title := compactProjectName(project)
		summary := bossProjectSummaryText(project, now)
		text := title
		if strings.TrimSpace(summary) != "" && summary != "-" {
			text += " - " + summary
		}
		rows = append(rows, bossDeskRow(style, label, text, width))
	}
	return rows
}

func (m Model) bossDeskNextLine(width int, now time.Time) string {
	for _, task := range m.snapshot.OpenAgentTasks {
		if model.NormalizeAgentTaskStatus(task.Status) == model.AgentTaskStatusWaiting {
			return bossDeskRow(bossAssessmentWaitingStyle, "next", "Review "+compactAgentTaskTitle(task), width)
		}
	}
	for _, activity := range m.activeEngineerActivities() {
		title := strings.TrimSpace(firstNonEmpty(activity.Title, activity.ProjectPath, activity.TaskID, "engineer work"))
		name := strings.TrimSpace(firstNonEmpty(activity.EngineerName, "Engineer"))
		return bossDeskRow(bossAssessmentWorkingStyle, "next", "Let "+name+" finish "+title, width)
	}
	for _, task := range m.snapshot.OpenAgentTasks {
		if model.NormalizeAgentTaskStatus(task.Status) == model.AgentTaskStatusCompleted {
			return bossDeskRow(bossAssessmentDoneStyle, "next", "Close "+compactAgentTaskTitle(task), width)
		}
	}
	for _, notice := range m.viewContext.SystemNotices {
		if strings.TrimSpace(notice.Severity) == "warning" && strings.TrimSpace(notice.Summary) != "" {
			return bossDeskRow(bossAssessmentWaitingStyle, "next", "Handle "+notice.Summary, width)
		}
	}
	if len(m.snapshot.HotProjects) > 0 {
		return bossDeskRow(bossAssessmentFollowupStyle, "next", "Check "+compactProjectName(m.snapshot.HotProjects[0]), width)
	}
	return bossDeskRow(bossMutedStyle, "next", "No obvious move right now", width)
}

func (m Model) bossDeskSystemNotices() []ViewSystemNotice {
	if len(m.operationalNotices) == 0 {
		return append([]ViewSystemNotice(nil), m.viewContext.SystemNotices...)
	}
	out := make([]ViewSystemNotice, 0, len(m.viewContext.SystemNotices)+len(m.operationalNotices))
	out = append(out, m.viewContext.SystemNotices...)
	out = append(out, m.operationalNotices...)
	return out
}

func (m *Model) appendDeskEvent(kind, label, summary string) {
	summary = cleanHandoffSummary(summary)
	if summary == "" {
		return
	}
	kind = strings.TrimSpace(kind)
	label = strings.TrimSpace(label)
	if label == "" {
		label = "event"
	}
	now := m.now()
	if len(m.deskEvents) > 0 {
		last := m.deskEvents[len(m.deskEvents)-1]
		if last.Kind == kind && last.Label == label && last.Summary == summary {
			return
		}
	}
	m.deskEvents = append(m.deskEvents, bossDeskEvent{
		At:      now,
		Kind:    kind,
		Label:   label,
		Summary: summary,
	})
	if len(m.deskEvents) > maxBossDeskEvents {
		m.deskEvents = append([]bossDeskEvent(nil), m.deskEvents[len(m.deskEvents)-maxBossDeskEvents:]...)
	}
}

func bossDeskActivityEventSummary(activity ViewEngineerActivity, fallback string) string {
	title := strings.TrimSpace(firstNonEmpty(activity.Title, activity.ProjectPath, activity.TaskID, "engineer work"))
	name := strings.TrimSpace(firstNonEmpty(activity.EngineerName, "Engineer"))
	status := strings.TrimSpace(activity.Status)
	switch status {
	case "", "working":
		return name + " started " + title
	case "finishing", "rechecking":
		return name + " is " + status + " " + title
	default:
		if fallback = strings.TrimSpace(fallback); fallback != "" {
			return fallback
		}
		return name + " is " + status + " on " + title
	}
}

func bossDeskTextWithDetail(title, detail string) string {
	title = strings.TrimSpace(title)
	detail = cleanHandoffSummary(detail)
	switch {
	case title == "":
		return detail
	case detail == "":
		return title
	default:
		return title + " - " + detail
	}
}

func bossLogEventTimeLabel(at, now time.Time) string {
	if at.IsZero() {
		return ""
	}
	if now.IsZero() {
		now = time.Now()
	}
	if sameLocalDate(at, now) {
		return at.Format("15:04")
	}
	return at.Format("01-02")
}

func sameLocalDate(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}

func bossDeskEventStyle(label string) lipgloss.Style {
	switch strings.TrimSpace(label) {
	case "failed", "error":
		return bossAssessmentFailedStyle
	case "review", "waiting":
		return bossAssessmentWaitingStyle
	case "done", "close", "completed":
		return bossAssessmentDoneStyle
	case "start", "working":
		return bossAssessmentWorkingStyle
	default:
		return bossMutedStyle
	}
}

func bossDeskRow(labelStyle lipgloss.Style, label, text string, width int) string {
	labelW := 8
	if width < 36 {
		labelW = 6
	}
	label = strings.TrimSpace(label)
	if label == "" {
		label = "-"
	}
	textW := maxInt(8, width-labelW-1)
	left := labelStyle.Width(labelW).Render(fitLine(label, labelW))
	right := bossSummaryTextStyle.Render(fitLine(strings.TrimSpace(text), textW))
	return fitStyledLine(lipgloss.JoinHorizontal(lipgloss.Top, left, " ", right), width)
}

func deskNoticeLabel(code string) string {
	switch strings.TrimSpace(code) {
	case "browser_waiting":
		return "browser"
	case "engineer_input_waiting":
		return "input"
	case "process_suspicious":
		return "cpu"
	case "control_completed":
		return "done"
	case "control_failed":
		return "failed"
	default:
		return "notice"
	}
}

var bossDeskSectionStyle = lipgloss.NewStyle().
	Foreground(bossPanelAccent).
	Background(bossPanelBackground).
	Bold(true)
