package attention

import (
	"fmt"
	"strings"
	"time"

	"lcroom/internal/model"
)

type Input struct {
	Path                       string
	Now                        time.Time
	LastActivity               time.Time
	RepoDirty                  bool
	Pinned                     bool
	SnoozedUntil               *time.Time
	ErrorCount                 int
	LatestSessionStart         time.Time
	LatestTurnKnown            bool
	LatestTurnComplete         bool
	LatestSessionCategoryKnown bool
	LatestSessionCategory      model.SessionCategory
	HasActivity                bool
	ActiveThreshold            time.Duration
	StuckThreshold             time.Duration
}

type Output struct {
	Status  model.ProjectStatus
	Score   int
	Reasons []model.AttentionReason
}

const completedAttentionWindow = 48 * time.Hour
const activeAttentionWeight = 50
const genericStuckAttentionWeight = 40
const blockedAttentionWeight = 40
const inProgressAttentionWeight = 32
const needsFollowUpAttentionWeight = 28
const waitingForUserAttentionWeight = 22

type classifiedIdleOutcome struct {
	Status model.ProjectStatus
	Reason *model.AttentionReason
}

func Score(in Input) Output {
	out := Output{
		Status:  model.StatusIdle,
		Score:   0,
		Reasons: []model.AttentionReason{},
	}

	if !in.HasActivity || in.LastActivity.IsZero() {
		out.Status = model.StatusIdle
		out.Score += 10
		out.Reasons = append(out.Reasons, model.AttentionReason{Code: "no_activity", Text: "No Codex/OpenCode activity detected yet", Weight: 10})
	} else {
		idleFor := in.Now.Sub(in.LastActivity)
		switch {
		case idleFor <= in.ActiveThreshold:
			out.Status = model.StatusActive
			out.Score += activeAttentionWeight
			out.Reasons = append(out.Reasons, model.AttentionReason{
				Code:   "active",
				Text:   fmt.Sprintf("Recent activity within %s", in.ActiveThreshold.Round(time.Minute)),
				Weight: activeAttentionWeight,
			})
		case idleFor <= in.StuckThreshold:
			out.Status = model.StatusIdle
			w := 20
			out.Score += w
			out.Reasons = append(out.Reasons, model.AttentionReason{Code: "idle", Text: fmt.Sprintf("Idle for %s", formatAttentionDuration(idleFor)), Weight: w})
		default:
			if handled, outcome := classifiedIdleReason(in, idleFor); handled {
				out.Status = outcome.Status
				if outcome.Reason != nil {
					out.Score += outcome.Reason.Weight
					out.Reasons = append(out.Reasons, *outcome.Reason)
				}
				break
			}
			if in.LatestTurnKnown && in.LatestTurnComplete && idleFor <= completedAttentionWindow {
				out.Status = model.StatusIdle
				w := 18
				out.Score += w
				out.Reasons = append(out.Reasons, model.AttentionReason{Code: "recently_completed", Text: fmt.Sprintf("Last turn completed; idle for %s", formatAttentionDuration(idleFor)), Weight: w})
				break
			}
			out.Status = model.StatusPossiblyStuck
			w := genericStuckAttentionWeight
			out.Score += w
			out.Reasons = append(out.Reasons, model.AttentionReason{Code: "possibly_stuck", Text: fmt.Sprintf("No activity for %s", formatAttentionDuration(idleFor)), Weight: w})
		}
	}

	if in.RepoDirty {
		w := 15
		out.Score += w
		out.Reasons = append(out.Reasons, model.AttentionReason{
			Code:   "repo_dirty",
			Text:   "Git worktree has uncommitted changes",
			Weight: w,
		})
	}

	if !in.LatestSessionStart.IsZero() && in.Now.Sub(in.LatestSessionStart) > 2*time.Hour && in.StatusHint() == model.StatusActive && !latestSessionCompleted(in) {
		w := 8
		out.Score += w
		out.Reasons = append(out.Reasons, model.AttentionReason{Code: "long_running", Text: "Long-running active session", Weight: w})
	}

	if in.Pinned {
		w := 30
		out.Score += w
		out.Reasons = append(out.Reasons, model.AttentionReason{Code: "pinned", Text: "Pinned by user", Weight: w})
	}

	if in.SnoozedUntil != nil && in.SnoozedUntil.After(in.Now) {
		w := -100
		out.Score += w
		out.Reasons = append(out.Reasons, model.AttentionReason{Code: "snoozed", Text: fmt.Sprintf("Snoozed until %s", in.SnoozedUntil.Format(time.RFC3339)), Weight: w})
	}

	if out.Score < 0 {
		out.Score = 0
	}

	return out
}

func (in Input) StatusHint() model.ProjectStatus {
	if !in.HasActivity || in.LastActivity.IsZero() {
		return model.StatusIdle
	}
	idleFor := in.Now.Sub(in.LastActivity)
	if idleFor <= in.ActiveThreshold {
		return model.StatusActive
	}
	if idleFor <= in.StuckThreshold {
		return model.StatusIdle
	}
	return model.StatusPossiblyStuck
}

func formatAttentionDuration(d time.Duration) string {
	rounded := d.Round(time.Minute)
	if rounded < 0 {
		rounded = 0
	}

	totalMinutes := int64(rounded / time.Minute)
	days := totalMinutes / (24 * 60)
	hours := (totalMinutes % (24 * 60)) / 60
	minutes := totalMinutes % 60

	parts := make([]string, 0, 3)
	if days > 0 {
		parts = append(parts, fmt.Sprintf("%dd", days))
		parts = append(parts, fmt.Sprintf("%dh", hours))
		parts = append(parts, fmt.Sprintf("%dm", minutes))
		return strings.Join(parts, " ")
	}
	if hours > 0 {
		parts = append(parts, fmt.Sprintf("%dh", hours))
		parts = append(parts, fmt.Sprintf("%dm", minutes))
		return strings.Join(parts, " ")
	}
	return fmt.Sprintf("%dm", minutes)
}

func classifiedIdleReason(in Input, idleFor time.Duration) (bool, classifiedIdleOutcome) {
	if !in.LatestSessionCategoryKnown {
		return false, classifiedIdleOutcome{}
	}

	switch in.LatestSessionCategory {
	case model.SessionCategoryCompleted:
		if idleFor > completedAttentionWindow {
			return true, classifiedIdleOutcome{Status: model.StatusIdle}
		}
		return true, classifiedIdleOutcome{
			Status: model.StatusIdle,
			Reason: &model.AttentionReason{
				Code:   "session_completed",
				Text:   fmt.Sprintf("Recently completed; idle for %s", formatAttentionDuration(idleFor)),
				Weight: 15,
			},
		}
	case model.SessionCategoryBlocked:
		return true, classifiedIdleOutcome{
			Status: model.StatusPossiblyStuck,
			Reason: &model.AttentionReason{
				Code:   "blocked",
				Text:   fmt.Sprintf("Latest session is blocked; idle for %s", formatAttentionDuration(idleFor)),
				Weight: blockedAttentionWeight,
			},
		}
	case model.SessionCategoryWaitingForUser:
		return true, classifiedIdleOutcome{
			Status: model.StatusIdle,
			Reason: &model.AttentionReason{
				Code:   "waiting_for_user",
				Text:   fmt.Sprintf("Latest session is waiting for user input; idle for %s", formatAttentionDuration(idleFor)),
				Weight: waitingForUserAttentionWeight,
			},
		}
	case model.SessionCategoryNeedsFollowUp:
		return true, classifiedIdleOutcome{
			Status: model.StatusPossiblyStuck,
			Reason: &model.AttentionReason{
				Code:   "needs_follow_up",
				Text:   fmt.Sprintf("Latest session needs follow-up; idle for %s", formatAttentionDuration(idleFor)),
				Weight: needsFollowUpAttentionWeight,
			},
		}
	case model.SessionCategoryInProgress:
		return true, classifiedIdleOutcome{
			Status: model.StatusPossiblyStuck,
			Reason: &model.AttentionReason{
				Code:   "in_progress",
				Text:   fmt.Sprintf("Latest session was still in progress; idle for %s", formatAttentionDuration(idleFor)),
				Weight: inProgressAttentionWeight,
			},
		}
	default:
		return false, classifiedIdleOutcome{}
	}
}

func latestSessionCompleted(in Input) bool {
	return in.LatestSessionCategoryKnown && in.LatestSessionCategory == model.SessionCategoryCompleted
}
