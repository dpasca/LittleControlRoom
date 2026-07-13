package boss

import (
	"fmt"
	"strings"
	"time"

	"lcroom/internal/model"
	"lcroom/internal/uistyle"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

const (
	bossDeskActiveLimit   = 4
	bossDeskDecisionLimit = 4
	bossDeskTodoLimit     = 4
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
	title := uistyle.SidebarTitleStyle.Render(fitLine("Boss Desk", bossPanelInnerWidth(width)))
	return m.renderRawPanelStyledTitle(title, m.bossSidebarContent(width, height), width, height)
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
	nowLimit, decisionLimit, todoLimit, watchingLimit := bossSidebarSectionLimits(height)

	if rows := m.bossDeskChatStatsRows(width); len(rows) > 0 {
		lines = appendDeskSection(lines, "Chat", rows, width)
	}
	if rows := takeDeskRows(m.bossDeskNowRows(width, now), nowLimit); len(rows) > 0 {
		lines = appendDeskSection(lines, "Now", rows, width)
	}
	needsUser := append([]string{}, m.bossDeskNeedsUserRows(width, now)...)
	needsUser = append(needsUser, m.bossDeskReadyRows(width, now)...)
	if rows := takeDeskRows(needsUser, decisionLimit); len(rows) > 0 {
		lines = appendDeskSection(lines, "Needs You", rows, width)
	}
	if rows := takeDeskRows(m.bossDeskTodoRows(width, now), todoLimit); len(rows) > 0 {
		lines = appendDeskSection(lines, "TODOs", rows, width)
	}
	if next := m.bossDeskNextLine(width, now); next != "" {
		lines = appendDeskSection(lines, "Next", []string{next}, width)
	}
	if rows := takeDeskRows(m.bossDeskWatchingRows(width, now), watchingLimit); len(rows) > 0 {
		lines = appendDeskSection(lines, "Watching", rows, width)
	}
	return lines
}

func (m Model) bossDeskChatStatsRows(width int) []string {
	rows := []string{}
	modelName := strings.TrimSpace(m.lastAssistantModel)
	if modelName == "" && m.assistant != nil {
		modelName = strings.TrimSpace(m.assistant.model)
	}
	if modelName != "" {
		rows = append(rows, bossDeskFieldRow("Model", modelName, width))
		rows = append(rows, bossDeskFieldRow("Reasoning", bossAssistantReasoningEffort, width))
	}
	if contextRows := m.bossDeskContextRows(width); len(contextRows) > 0 {
		rows = append(rows, contextRows...)
	}
	if m.haveLastAssistantUsage {
		if tokens := formatBossLLMUsageTokens(m.lastAssistantUsage); tokens != "" {
			rows = append(rows, bossDeskFieldRow("Tokens", tokens, width))
		}
	} else if usage := m.bossChatUsage(); usage.Running > 0 {
		rows = append(rows, bossDeskFieldRow("Tokens", "measuring", width))
	}
	return rows
}

func (m Model) bossDeskContextRows(width int) []string {
	report := m.contextReport()
	if report.MessageCount == 0 && report.FlowEventCount == 0 {
		return nil
	}
	rows := []string{}
	mode := bossDeskContextModeLabel(report.ContextMode, report.TotalMessages, report.VisibleMessages, report.SummaryMessages)
	if mode != "" {
		rows = append(rows, bossDeskFieldRow("Context", mode, width))
	}
	if detail := bossDeskContextCountLabel(report.TotalMessages, report.VisibleMessages, report.SummaryMessages); detail != "" {
		rows = append(rows, bossDeskMutedDetailRow(detail, width))
	}
	if report.ApproxChars > 0 {
		rows = append(rows, bossDeskMutedDetailRow("~"+uistyle.FormatTokenCount(int64(report.ApproxChars))+" chars", width))
	}
	return rows
}

func bossDeskContextModeLabel(mode string, total, visible, summarized int) string {
	switch strings.TrimSpace(mode) {
	case "compacted":
		return "compacted"
	case "clipped":
		return "recent chat"
	case "exact":
		if summarized > 0 || total > visible {
			return "recent chat"
		}
		return "full chat"
	default:
		if summarized > 0 {
			return "compacted"
		}
		if total > visible {
			return "recent chat"
		}
		if total > 0 || visible > 0 {
			return "full chat"
		}
		return ""
	}
}

func bossDeskContextCountLabel(total, visible, summarized int) string {
	switch {
	case visible > 0 && summarized > 0:
		return fmt.Sprintf("%d recent + %d summarized", visible, summarized)
	case visible > 0 && total > visible:
		return fmt.Sprintf("%d recent of %d messages", visible, total)
	case visible == 1:
		return "1 message"
	case visible > 1:
		return fmt.Sprintf("%d messages", visible)
	default:
		return ""
	}
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

func bossSidebarSectionLimits(height int) (nowLimit, decisionLimit, todoLimit, watchingLimit int) {
	switch {
	case height <= 6:
		return 1, 1, 0, 1
	case height <= 9:
		return 1, 2, 1, 1
	default:
		return bossDeskActiveLimit, bossDeskDecisionLimit, bossDeskTodoLimit, bossDeskWatchingLimit
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
	lines = append(lines, renderBossDeskSectionHeader(title, width))
	lines = append(lines, rows...)
	return lines
}

func renderBossDeskSectionHeader(title string, width int) string {
	title = strings.TrimSpace(title)
	if title == "" {
		title = "Section"
	}
	return bossDeskSectionStyle.Render(fitLine(" "+title, width))
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
				rows = append(rows, m.bossDeskAttentionRow(attentionItemForEngineerActivity(activity), bossAssessmentWaitingStyle, "quiet", text, width))
			}
			continue
		}
		label, style := bossEngineerActivityCell(activity, now)
		title := strings.TrimSpace(firstNonEmpty(activity.Title, activity.ProjectPath, activity.TaskID, "engineer work"))
		item := attentionItemForEngineerActivity(activity)
		text := bossSummaryTextStyle.Render("Working on ")
		if item.Kind == AttentionItemProject {
			text += bossProjectIdentityStyle(item.ProjectPath, bossProjectNameStyle).Render(title)
			rows = append(rows, m.bossDeskAttentionStyledRow(item, style, label, text, width))
			continue
		}
		rows = append(rows, m.bossDeskAttentionRow(item, style, label, "Working on "+title, width))
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
			detail = agentTaskDecisionQuestion()
		}
		rows = append(rows, m.bossDeskAttentionRow(attentionItemForAgentTask(task), bossAssessmentWaitingStyle, "review", bossDeskTextWithDetail(title, detail), width))
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
		rows = append(rows, m.bossDeskAttentionRow(attentionItemForAgentTask(task), bossAssessmentDoneStyle, "close", bossDeskTextWithDetail(title, detail), width))
	}
	return rows
}

func (m Model) bossDeskTodoRows(width int, now time.Time) []string {
	rows := make([]string, 0, minInt(len(m.snapshot.OpenTodos), bossDeskTodoLimit))
	for _, todo := range m.snapshot.OpenTodos {
		if len(rows) >= bossDeskTodoLimit {
			break
		}
		text := m.bossDeskTodoStyledText(todo)
		if strings.TrimSpace(ansi.Strip(text)) == "" {
			continue
		}
		rows = append(rows, bossDeskStyledRowWithHotkey("", bossAssessmentFollowupStyle, bossDeskTodoLabel(todo), text, width))
	}
	return rows
}

func bossDeskTodoLabel(todo TodoBrief) string {
	if todo.ID > 0 {
		return fmt.Sprintf("#%d", todo.ID)
	}
	return "-"
}

func (m Model) bossDeskTodoStyledText(todo TodoBrief) string {
	project := strings.TrimSpace(todo.ProjectName)
	if project == "" {
		project = strings.TrimSpace(todo.ProjectPath)
	}
	text := cleanHandoffSummary(firstNonEmpty(todo.Label, todo.Text))
	if activity := m.bossDeskTodoActivity(todo); activity != "" {
		if text != "" {
			text += " · " + activity
		} else {
			text = activity
		}
	}
	switch {
	case project == "":
		return bossSummaryTextStyle.Render(text)
	case text == "":
		return bossProjectIdentityStyle(firstNonEmpty(todo.ProjectPath, project), bossProjectNameStyle).Render(project)
	default:
		return bossProjectIdentityStyle(firstNonEmpty(todo.ProjectPath, project), bossProjectNameStyle).Render(project) + bossSummaryTextStyle.Render(" - "+text)
	}
}

func (m Model) bossDeskTodoActivity(todo TodoBrief) string {
	providerLabel := bossTodoWorkProviderLabel(todo.WorkProvider)
	if strings.TrimSpace(todo.WorkSessionID) != "" {
		if activity, ok := m.liveEngineerActivityForTodo(todo); ok {
			status := strings.TrimSpace(activity.Status)
			if status == "" {
				status = "working"
			}
			if providerLabel != "" {
				return status + " " + providerLabel
			}
			return status
		}
	}
	state := model.NormalizeTodoWorkState(todo.WorkState)
	if state == "" || state == model.TodoWorkStateIdle {
		if providerLabel != "" && strings.TrimSpace(todo.WorkSessionID) != "" {
			return "resume " + providerLabel
		}
		return ""
	}
	if providerLabel != "" && strings.TrimSpace(todo.WorkSessionID) != "" {
		return "stale " + providerLabel
	}
	return string(state)
}

func bossTodoWorkProviderLabel(provider model.SessionSource) string {
	switch model.NormalizeSessionSource(provider) {
	case model.SessionSourceCodex:
		return "Codex"
	case model.SessionSourceOpenCode:
		return "OpenCode"
	case model.SessionSourceClaudeCode:
		return "Claude"
	case model.SessionSourceLCAgent:
		return "LCAgent"
	}
	return ""
}

func (m Model) liveEngineerActivityForTodo(todo TodoBrief) (ViewEngineerActivity, bool) {
	for _, activity := range m.activeEngineerActivities() {
		if todo.ID > 0 && activity.TodoID == todo.ID {
			return activity, true
		}
		if bossTodoMatchesEngineerActivity(todo, activity) {
			return activity, true
		}
	}
	return ViewEngineerActivity{}, false
}

func bossTodoMatchesEngineerActivity(todo TodoBrief, activity ViewEngineerActivity) bool {
	provider := model.NormalizeSessionSource(todo.WorkProvider)
	if provider != "" && model.NormalizeSessionSource(activity.Provider) != provider {
		return false
	}
	if !bossSessionIDsMatch(todo.WorkSessionID, activity.SessionID) {
		return false
	}
	workProjectPath := strings.TrimSpace(todo.WorkProjectPath)
	if workProjectPath == "" {
		workProjectPath = strings.TrimSpace(todo.ProjectPath)
	}
	activityProjectPath := strings.TrimSpace(activity.ProjectPath)
	if workProjectPath == "" || activityProjectPath == "" {
		return true
	}
	return workProjectPath == activityProjectPath
}

func bossSessionIDsMatch(expected, actual string) bool {
	expected = strings.TrimSpace(expected)
	actual = strings.TrimSpace(actual)
	if expected == "" || actual == "" {
		return false
	}
	if expected == actual {
		return true
	}
	_, expectedRaw := model.ParseCanonicalSessionID(expected)
	_, actualRaw := model.ParseCanonicalSessionID(actual)
	return expectedRaw != "" && expectedRaw == actualRaw
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
		rows = append(rows, m.bossDeskProjectRow(project, style, label, title, summary, width))
	}
	return rows
}

func (m Model) bossDeskNextLine(width int, now time.Time) string {
	for _, task := range m.snapshot.OpenAgentTasks {
		if model.NormalizeAgentTaskStatus(task.Status) == model.AgentTaskStatusWaiting {
			return m.bossDeskAttentionRow(attentionItemForAgentTask(task), bossAssessmentWaitingStyle, "next", "Review "+compactAgentTaskTitle(task), width)
		}
	}
	for _, activity := range m.activeEngineerActivities() {
		title := strings.TrimSpace(firstNonEmpty(activity.Title, activity.ProjectPath, activity.TaskID, "engineer work"))
		item := attentionItemForEngineerActivity(activity)
		if item.Kind == AttentionItemProject {
			text := bossSummaryTextStyle.Render("Let work finish on ") + bossProjectIdentityStyle(item.ProjectPath, bossProjectNameStyle).Render(title)
			return m.bossDeskAttentionStyledRow(item, bossAssessmentWorkingStyle, "next", text, width)
		}
		return m.bossDeskAttentionRow(item, bossAssessmentWorkingStyle, "next", "Let work finish on "+title, width)
	}
	for _, task := range m.snapshot.OpenAgentTasks {
		if model.NormalizeAgentTaskStatus(task.Status) == model.AgentTaskStatusCompleted {
			return m.bossDeskAttentionRow(attentionItemForAgentTask(task), bossAssessmentDoneStyle, "next", "Close "+compactAgentTaskTitle(task), width)
		}
	}
	for _, notice := range m.viewContext.SystemNotices {
		if strings.TrimSpace(notice.Severity) == "warning" && strings.TrimSpace(notice.Summary) != "" {
			return bossDeskRow(bossAssessmentWaitingStyle, "next", "Handle "+notice.Summary, width)
		}
	}
	if len(m.snapshot.HotProjects) > 0 {
		project := m.snapshot.HotProjects[0]
		text := bossSummaryTextStyle.Render("Check ") + bossProjectIdentityStyle(bossProjectBriefIdentity(project), bossProjectNameStyle).Render(compactProjectName(project))
		return m.bossDeskAttentionStyledRow(attentionItemForProject(project), bossAssessmentFollowupStyle, "next", text, width)
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
	status := strings.TrimSpace(activity.Status)
	switch status {
	case "", "working":
		return "Work started on " + title
	case "finishing", "rechecking":
		return "Work on " + title + " is " + status
	default:
		if fallback = strings.TrimSpace(fallback); fallback != "" {
			return fallback
		}
		return "Work on " + title + " is " + status
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
	return bossDeskRowWithHotkey("", labelStyle, label, text, width)
}

func bossDeskFieldRow(label, text string, width int) string {
	label = strings.TrimSpace(label)
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	prefix := label
	if prefix != "" {
		prefix += " "
	}
	textWidth := maxInt(1, width-ansi.StringWidth(prefix))
	return fitStyledLine(bossMutedStyle.Render(prefix)+bossSummaryTextStyle.Render(fitLine(text, textWidth)), width)
}

func bossDeskMutedDetailRow(text string, width int) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	return bossMutedStyle.Render(fitLine("  "+text, width))
}

func (m Model) bossDeskAttentionRow(item AttentionItem, labelStyle lipgloss.Style, label, text string, width int) string {
	return bossDeskRowWithHotkey(m.attentionHotkeyLabel(item), labelStyle, label, text, width)
}

func (m Model) bossDeskAttentionStyledRow(item AttentionItem, labelStyle lipgloss.Style, label, styledText string, width int) string {
	return bossDeskStyledRowWithHotkey(m.attentionHotkeyLabel(item), labelStyle, label, styledText, width)
}

func (m Model) bossDeskProjectRow(project ProjectBrief, labelStyle lipgloss.Style, label, title, summary string, width int) string {
	styled := bossProjectIdentityStyle(bossProjectBriefIdentity(project), bossProjectNameStyle).Render(strings.TrimSpace(title))
	summary = strings.TrimSpace(summary)
	if summary != "" && summary != "-" {
		styled += bossSummaryTextStyle.Render(" - " + summary)
	}
	return m.bossDeskAttentionStyledRow(attentionItemForProject(project), labelStyle, label, styled, width)
}

func bossDeskRowWithHotkey(hotkey string, labelStyle lipgloss.Style, label, text string, width int) string {
	return bossDeskRenderRow(hotkey, labelStyle, label, bossSummaryTextStyle.Render(strings.TrimSpace(text)), width)
}

func bossDeskStyledRowWithHotkey(hotkey string, labelStyle lipgloss.Style, label, styledText string, width int) string {
	return bossDeskRenderRow(hotkey, labelStyle, label, styledText, width)
}

func bossDeskRenderRow(hotkey string, labelStyle lipgloss.Style, label, styledText string, width int) string {
	labelW := 8
	if width < 36 {
		labelW = 6
	}
	label = strings.TrimSpace(label)
	if label == "" {
		label = "-"
	}
	hotkey = strings.TrimSpace(hotkey)
	hotkeyW := 0
	if hotkey != "" {
		hotkeyW = 5
	}
	textW := maxInt(8, width-hotkeyW-labelW-2)
	if hotkey == "" {
		textW = maxInt(8, width-labelW-1)
	}
	parts := []string{}
	if hotkey != "" {
		parts = append(parts, bossHotkeyStyle.Width(hotkeyW).Render(fitLine(hotkey, hotkeyW)), " ")
	}
	left := labelStyle.Width(labelW).Render(fitLine(label, labelW))
	right := fitStyledLine(styledText, textW)
	parts = append(parts, left, " ", right)
	return fitStyledLine(lipgloss.JoinHorizontal(lipgloss.Top, parts...), width)
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

var bossDeskSectionStyle = uistyle.SidebarSectionHeaderStyle
