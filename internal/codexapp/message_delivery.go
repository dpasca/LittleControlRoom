package codexapp

import (
	"fmt"
	"time"
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
