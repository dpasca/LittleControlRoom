package boss

import (
	"strings"
	"time"
)

const (
	bossSupervisorItemLimit              = 5
	bossSupervisorQuietAfter             = 10 * time.Minute
	bossSupervisorStateRefreshEveryTicks = 17
)

type bossSupervisorItem struct {
	Text string
}

func (m Model) renderSupervisorBrief(width int) string {
	items := m.supervisorItems(m.now())
	if len(items) == 0 {
		return ""
	}
	lines := make([]string, 0, len(items))
	for _, item := range items {
		text := strings.TrimSpace(item.Text)
		if text == "" {
			continue
		}
		lines = append(lines, text)
	}
	if len(lines) == 0 {
		return ""
	}
	for i, line := range lines {
		lines[i] = renderBossHandoffMessage(line, width)
	}
	return strings.Join(lines, "\n")
}

func (m Model) supervisorItems(now time.Time) []bossSupervisorItem {
	if now.IsZero() {
		now = time.Now()
	}
	items := make([]bossSupervisorItem, 0, bossSupervisorItemLimit)
	for _, activity := range m.activeEngineerActivities() {
		if len(items) >= bossSupervisorItemLimit {
			return items
		}
		if text := supervisorActivityLine(activity, now); text != "" {
			items = append(items, bossSupervisorItem{Text: text})
		}
	}
	return items
}

func supervisorActivityLine(activity ViewEngineerActivity, now time.Time) string {
	title := strings.TrimSpace(firstNonEmpty(activity.Title, activity.ProjectPath, activity.TaskID, "engineer session"))
	name := strings.TrimSpace(firstNonEmpty(activity.EngineerName, "Engineer"))
	status := strings.TrimSpace(activity.Status)
	if status == "" {
		status = "working"
	}
	if quietFor := supervisorActivityQuietFor(activity, now); quietFor >= bossSupervisorQuietAfter {
		return name + " has gone quiet on " + title + " for " + bossRunningDuration(quietFor)
	}
	elapsed := bossEngineerActivityElapsedText(activity, now)
	switch status {
	case "stalled":
		return supervisorActivitySentence(name, "is stalled on", title, elapsed)
	case "waiting":
		return supervisorActivitySentence(name, "is waiting on", title, elapsed)
	case "working elsewhere":
		return supervisorActivitySentence(name, "is working elsewhere from", title, elapsed)
	default:
		if status != "working" {
			return supervisorActivitySentence(name, "is "+status+" on", title, elapsed)
		}
		return supervisorActivitySentence(name, "is working on", title, elapsed)
	}
}

func supervisorActivitySentence(name, action, title, elapsed string) string {
	line := strings.TrimSpace(name + " " + action + " " + title)
	if elapsed != "" {
		line += " for " + elapsed
	}
	return line
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

func agentTaskDecisionQuestion(engineerName string) string {
	engineerName = strings.TrimSpace(engineerName)
	if engineerName == "" || strings.EqualFold(engineerName, "Engineer") {
		return "ready to close or continue"
	}
	return engineerName + " is ready to close or continue"
}

func (m Model) shouldRefreshSupervisorState() bool {
	if m.svc == nil || m.svc.Store() == nil {
		return false
	}
	return m.spinnerFrame > 0 && m.spinnerFrame%bossSupervisorStateRefreshEveryTicks == 0
}
