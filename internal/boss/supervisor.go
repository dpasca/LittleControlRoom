package boss

import (
	"fmt"
	"strings"
	"time"

	"lcroom/internal/model"
)

const (
	bossSupervisorItemLimit              = 5
	bossSupervisorQuietAfter             = 10 * time.Minute
	bossSupervisorStateRefreshEveryTicks = 17
)

type bossSupervisorItem struct {
	Text  string
	State bossSupervisorItemState
}

type bossSupervisorItemState string

const (
	bossSupervisorItemActive  bossSupervisorItemState = "active"
	bossSupervisorItemWaiting bossSupervisorItemState = "waiting"
	bossSupervisorItemOpen    bossSupervisorItemState = "open"
)

func (m Model) renderSupervisorBrief(width int) string {
	items := m.supervisorItems(m.now())
	if len(items) == 0 {
		return ""
	}
	lines := []string{supervisorHeader(items, m.spinnerFrame)}
	for _, item := range items {
		text := strings.TrimSpace(item.Text)
		if text == "" {
			continue
		}
		lines = append(lines, "  "+text)
	}
	for i, line := range lines {
		lines[i] = bossToolCallStyle.Render(fitLine(line, width))
	}
	return strings.Join(lines, "\n")
}

func (m Model) supervisorItems(now time.Time) []bossSupervisorItem {
	if now.IsZero() {
		now = time.Now()
	}
	items := make([]bossSupervisorItem, 0, bossSupervisorItemLimit)
	activeTaskIDs := map[string]struct{}{}
	for _, activity := range m.activeEngineerActivities() {
		if len(items) >= bossSupervisorItemLimit {
			return items
		}
		if taskID := strings.TrimSpace(activity.TaskID); taskID != "" {
			activeTaskIDs[taskID] = struct{}{}
		}
		if text := supervisorActivityLine(activity, now); text != "" {
			items = append(items, bossSupervisorItem{Text: text, State: bossSupervisorItemActive})
		}
	}
	for _, task := range m.snapshot.OpenAgentTasks {
		if len(items) >= bossSupervisorItemLimit {
			return items
		}
		if _, active := activeTaskIDs[strings.TrimSpace(task.ID)]; active {
			continue
		}
		if text := supervisorTaskLine(task, now); text != "" {
			items = append(items, bossSupervisorItem{Text: text, State: supervisorTaskItemState(task)})
		}
	}
	return items
}

func supervisorHeader(items []bossSupervisorItem, spinnerFrame int) string {
	if supervisorItemsIncludeState(items, bossSupervisorItemActive) {
		return "Supervisor: tracking work " + spinnerDots(spinnerFrame)
	}
	if supervisorItemsIncludeState(items, bossSupervisorItemWaiting) {
		return "Supervisor: needs your call"
	}
	return "Supervisor: open handoffs"
}

func supervisorItemsIncludeState(items []bossSupervisorItem, state bossSupervisorItemState) bool {
	for _, item := range items {
		if item.State == state {
			return true
		}
	}
	return false
}

func supervisorTaskItemState(task AgentTaskBrief) bossSupervisorItemState {
	if model.NormalizeAgentTaskStatus(task.Status) == model.AgentTaskStatusWaiting {
		return bossSupervisorItemWaiting
	}
	return bossSupervisorItemOpen
}

func supervisorActivityLine(activity ViewEngineerActivity, now time.Time) string {
	title := strings.TrimSpace(firstNonEmpty(activity.Title, activity.ProjectPath, activity.TaskID, "engineer session"))
	name := strings.TrimSpace(firstNonEmpty(activity.EngineerName, "Engineer"))
	status := supervisorActivityStatus(activity, now)
	if status == "" {
		return ""
	}
	provider := strings.TrimSpace(string(model.NormalizeSessionSource(activity.Provider)))
	if provider != "" {
		status = provider + " " + status
	}
	return name + " on " + title + " - " + status
}

func supervisorActivityStatus(activity ViewEngineerActivity, now time.Time) string {
	status := strings.TrimSpace(activity.Status)
	if status == "" {
		status = "working"
	}
	switch status {
	case "stalled", "waiting", "working elsewhere":
		if elapsed := bossEngineerActivityElapsedText(activity, now); elapsed != "" {
			return status + " " + elapsed
		}
		return status
	}
	if quietFor := supervisorActivityQuietFor(activity, now); quietFor >= bossSupervisorQuietAfter {
		return "quiet " + bossRunningDuration(quietFor)
	}
	if elapsed := bossEngineerActivityElapsedText(activity, now); elapsed != "" {
		return status + " " + elapsed
	}
	return status
}

func supervisorActivityQuietFor(activity ViewEngineerActivity, now time.Time) time.Duration {
	if now.IsZero() {
		now = time.Now()
	}
	last := activity.LastEventAt
	if last.IsZero() {
		last = activity.StartedAt
	}
	if last.IsZero() {
		return 0
	}
	d := now.Sub(last)
	if d < 0 {
		return 0
	}
	return d
}

func supervisorTaskLine(task AgentTaskBrief, now time.Time) string {
	title := compactAgentTaskTitle(task)
	name := strings.TrimSpace(firstNonEmpty(task.EngineerName, EngineerNameForKey("agent_task", task.ID)))
	detail := supervisorTaskDetail(task, now)
	switch model.NormalizeAgentTaskStatus(task.Status) {
	case model.AgentTaskStatusWaiting:
		return supervisorJoinLine(name+" finished "+title+". "+agentTaskDecisionQuestion(name), detail)
	case model.AgentTaskStatusActive:
		return supervisorJoinLine(name+" has "+title+" open", detail)
	default:
		return ""
	}
}

func supervisorTaskDetail(task AgentTaskBrief, now time.Time) string {
	if summary := strings.TrimSpace(task.Summary); summary != "" {
		return clipText(summary, 140)
	}
	if model.NormalizeAgentTaskStatus(task.Status) == model.AgentTaskStatusActive {
		return "no live engineer session right now"
	}
	if !task.LastTouchedAt.IsZero() {
		return "touched " + relativeAge(now, task.LastTouchedAt)
	}
	return ""
}

func supervisorJoinLine(head, detail string) string {
	head = strings.TrimSpace(head)
	detail = strings.TrimSpace(detail)
	if head == "" {
		return detail
	}
	if detail == "" {
		return head
	}
	return fmt.Sprintf("%s - %s", head, detail)
}

func agentTaskDecisionQuestion(engineerName string) string {
	engineerName = strings.TrimSpace(engineerName)
	if engineerName == "" || strings.EqualFold(engineerName, "Engineer") {
		return "Should I close it, or send the engineer back in?"
	}
	return "Should I close it, or send " + engineerName + " back in?"
}

func (m Model) shouldRefreshSupervisorState() bool {
	if m.svc == nil || m.svc.Store() == nil {
		return false
	}
	return m.spinnerFrame > 0 && m.spinnerFrame%bossSupervisorStateRefreshEveryTicks == 0
}
