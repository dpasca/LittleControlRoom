package codexapp

import (
	"fmt"
	"strings"
	"time"

	"lcroom/internal/claudeartifact"
)

const MessageQueueWarningAfter = 2 * time.Minute

const ParentSilenceWarningAfter = 5 * time.Minute

func (s Snapshot) ParentActivitySummary(now time.Time) (string, bool) {
	if s.ParentStatus == "" {
		return "", false
	}
	if s.ParentActivityAt.IsZero() {
		return s.ParentStatus, false
	}
	age := max(time.Duration(0), now.Sub(s.ParentActivityAt))
	if s.ParentTurnActive && age >= ParentSilenceWarningAfter {
		return "No parent activity for " + age.Round(time.Second).String() + " · check the active tool or worker", true
	}
	return s.ParentStatus + " · last parent activity " + age.Round(time.Second).String() + " ago", false
}

// OwnsRunningWork reports work the live provider stream still runs after its
// transcript may read as a completed turn: a parent turn the stream reports
// as running, or background tasks awaiting their terminal notification.
// Claude's transcript has no complete lifecycle for some tasks, such as
// Monitor, and a parent turn woken by a task notification keeps the last
// reloaded state.
func (s Snapshot) OwnsRunningWork() bool {
	if !s.Busy {
		return false
	}
	if s.ParentTurnActive {
		return true
	}
	for _, task := range s.BackgroundTasks {
		if !strings.EqualFold(strings.TrimSpace(task.Status), "unresolved") {
			return true
		}
	}
	return false
}

// ActiveSince is the single running-time origin shared by every surface: the
// earliest known start of the current busy span. BusySince covers queued
// follow-ups and parent turns woken by background tasks; an artifact-derived
// turn start can predate it after LCR reopens an already-running session.
func (s Snapshot) ActiveSince() time.Time {
	switch {
	case s.BusySince.IsZero():
		return s.LatestTurnStartedAt
	case s.LatestTurnStartedAt.IsZero() || s.BusySince.Before(s.LatestTurnStartedAt):
		return s.BusySince
	default:
		return s.LatestTurnStartedAt
	}
}

// BackgroundWait names the background work an idle parent is waiting on, so a
// long busy span is not mistaken for active model work. The label counts the
// work; detail describes it when there is exactly one task. ok is false while
// the parent itself is running.
func (s Snapshot) BackgroundWait() (label, detail string, ok bool) {
	if !s.ParentAwaitingBackgroundTasks {
		return "", "", false
	}
	running := make([]BackgroundTaskSnapshot, 0, len(s.BackgroundTasks))
	for _, task := range s.BackgroundTasks {
		if !strings.EqualFold(strings.TrimSpace(task.Status), "unresolved") {
			running = append(running, task)
		}
	}
	if len(running) == 0 {
		return "", "", false
	}
	noun := backgroundTaskNoun(running[0])
	for _, task := range running[1:] {
		if backgroundTaskNoun(task) != noun {
			noun = "task"
			break
		}
	}
	if len(running) > 1 {
		return fmt.Sprintf("Waiting on %d background %ss", len(running), noun), "", true
	}
	return "Waiting on background " + noun, backgroundTaskLabel(running[0]), true
}

func (s Snapshot) BackgroundWaitSummary() string {
	label, detail, ok := s.BackgroundWait()
	if !ok || detail == "" {
		return label
	}
	return label + ": " + detail
}

func backgroundTaskNoun(task BackgroundTaskSnapshot) string {
	switch strings.TrimSpace(task.Source) {
	case claudeartifact.AsyncTaskSourceBackgroundShell:
		return "command"
	case claudeartifact.AsyncTaskSourceAgent:
		return "agent"
	case claudeartifact.AsyncTaskSourceWorkflow:
		return "workflow"
	}
	switch strings.TrimSpace(task.Tool) {
	case "Bash":
		return "command"
	case "Agent", "Task":
		return "agent"
	}
	return "task"
}

func backgroundTaskLabel(task BackgroundTaskSnapshot) string {
	label := strings.Join(strings.Fields(firstNonEmptyTrimmed(task.Description, task.Summary, task.Command)), " ")
	if runes := []rune(label); len(runes) > 60 {
		label = string(runes[:57]) + "..."
	}
	return label
}

// MessageDeliverySnapshot is a transport receipt, not a claim that the model
// understood or followed an instruction. Only provider events advance it.
type MessageDeliverySnapshot struct {
	ID          string
	Preview     string
	State       string
	SubmittedAt time.Time
	UpdatedAt   time.Time
}

func (d MessageDeliverySnapshot) Pending() bool {
	switch d.State {
	case "sending", "sent", "received", "queued", "started":
		return true
	default:
		return false
	}
}

func (d MessageDeliverySnapshot) Waiting() bool {
	return d.Pending() && d.State != "started"
}

func (s Snapshot) MessageDeliverySummary(now time.Time) (string, bool) {
	waiting := 0
	allQueued := true
	var oldest time.Time
	for _, d := range s.MessageDeliveries {
		if !d.Waiting() {
			continue
		}
		waiting++
		allQueued = allQueued && d.State == "queued"
		if oldest.IsZero() || d.SubmittedAt.Before(oldest) {
			oldest = d.SubmittedAt
		}
	}
	if waiting > 0 {
		age := max(time.Duration(0), now.Sub(oldest))
		label := "1 message waiting"
		unstarted := "parent has not started it"
		if waiting > 1 {
			label = fmt.Sprintf("%d messages waiting", waiting)
			unstarted = "parent has not started them"
		}
		if !allQueued {
			unstarted = "processing not yet confirmed"
		}
		if !oldest.IsZero() {
			label += " · " + age.Round(time.Second).String()
		}
		return label + " · " + unstarted, !oldest.IsZero() && age >= MessageQueueWarningAfter
	}
	if len(s.MessageDeliveries) == 0 {
		return "", false
	}
	d := s.MessageDeliveries[len(s.MessageDeliveries)-1]
	switch d.State {
	case "started":
		return "Latest message: processing", false
	case "completed":
		return "Latest message: completed", false
	case "unknown":
		return "Latest message: delivery unconfirmed after session ended", true
	case "cancelled", "discarded", "refused", "failed", "interrupted":
		return "Latest message: " + d.State, true
	default:
		return "", false
	}
}
