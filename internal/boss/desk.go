package boss

import (
	"fmt"
	"strings"
	"time"

	"lcroom/internal/model"

	"github.com/charmbracelet/lipgloss"
)

const (
	bossDeskActiveLimit   = 4
	bossDeskDecisionLimit = 4
	bossDeskWatchingLimit = 5
)

func (m Model) renderBossDesk(width, height int) string {
	return m.renderRawPanel("Boss Desk", m.deskContent(width, height), width, height)
}

func (m Model) deskContent(width, height int) string {
	innerWidth := bossPanelInnerWidth(width)
	lines := m.bossDeskLines(innerWidth, height)
	if len(lines) == 0 {
		return bossMutedStyle.Render(fitLine("Nothing on the desk yet.", innerWidth))
	}
	return strings.Join(lines, "\n")
}

func (m Model) bossDeskLines(width, height int) []string {
	width = maxInt(24, width)
	now := m.now()
	var lines []string

	if rows := m.bossDeskNowRows(width, now); len(rows) > 0 {
		lines = appendDeskSection(lines, "Now", rows, width)
	}
	if rows := m.bossDeskNeedsUserRows(width, now); len(rows) > 0 {
		lines = appendDeskSection(lines, "Needs You", rows, width)
	}
	if rows := m.bossDeskReadyRows(width, now); len(rows) > 0 {
		lines = appendDeskSection(lines, "Ready To Close", rows, width)
	}
	if rows := m.bossDeskWatchingRows(width, now); len(rows) > 0 {
		lines = appendDeskSection(lines, "Watching", rows, width)
	}
	if next := m.bossDeskNextLine(width, now); next != "" {
		lines = appendDeskSection(lines, "Next", []string{next}, width)
	}
	return lines
}

func appendDeskSection(lines []string, title string, rows []string, width int) []string {
	if len(rows) == 0 {
		return lines
	}
	if len(lines) > 0 {
		lines = append(lines, "")
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
		rows = append(rows, bossDeskRow(bossAssessmentWaitingStyle, "review", title+" - "+cleanHandoffSummary(detail), width))
	}
	for _, notice := range m.viewContext.SystemNotices {
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
		rows = append(rows, bossDeskRow(bossAssessmentDoneStyle, "close", title+" - "+cleanHandoffSummary(detail), width))
	}
	return rows
}

func (m Model) bossDeskWatchingRows(width int, now time.Time) []string {
	rows := make([]string, 0, bossDeskWatchingLimit)
	for _, notice := range m.viewContext.SystemNotices {
		if len(rows) >= bossDeskWatchingLimit {
			break
		}
		if strings.TrimSpace(notice.Severity) == "warning" {
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
		return "pids"
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
