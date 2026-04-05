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
	CreatedAt                  time.Time
	RepoDirty                  bool
	Pinned                     bool
	Unread                     bool
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
	OpenTodoCount              int
}

type Output struct {
	Status  model.ProjectStatus
	Score   int
	Reasons []model.AttentionReason
}

const recentAttentionWindow = 72 * time.Hour
const newProjectAttentionWindow = 48 * time.Hour
const newProjectAttentionWeight = 45
const noActivityBaseWeight = 10
const activeAttentionWeight = 50
const blockedAttentionWeight = 40
const inProgressAttentionWeight = 32
const needsFollowUpAttentionWeight = 28
const openTodosAttentionWeight = 25
const waitingForUserAttentionWeight = 22
const recentCompletionAttentionWeight = 20
const recentActivityBonusWeight = 10
const unreadAttentionWeight = 12

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
		if w, ok := newProjectWeight(in); ok {
			out.Score += w
			out.Reasons = append(out.Reasons, model.AttentionReason{
				Code:   "new_project",
				Text:   fmt.Sprintf("New project awaiting first agent session (added %s ago)", formatAttentionDuration(in.Now.Sub(in.CreatedAt))),
				Weight: w,
			})
		} else {
			out.Score += noActivityBaseWeight
			out.Reasons = append(out.Reasons, model.AttentionReason{Code: "no_activity", Text: "No agent activity detected yet", Weight: noActivityBaseWeight})
		}
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
		default:
			if handled, outcome := classifiedIdleReason(in, idleFor); handled {
				out.Status = outcome.Status
				if outcome.Reason != nil {
					out.Score += outcome.Reason.Weight
					out.Reasons = append(out.Reasons, *outcome.Reason)
				}
				break
			}
			if in.LatestTurnKnown && in.LatestTurnComplete {
				out.Status = model.StatusIdle
				if reason := recentCompletionReason("recently_completed", "Last turn completed", idleFor, in.StuckThreshold); reason != nil {
					out.Score += reason.Weight
					out.Reasons = append(out.Reasons, *reason)
					break
				}
			}
			out.Status = model.StatusIdle
			if idleFor <= in.StuckThreshold {
				w := 20
				out.Score += w
				out.Reasons = append(out.Reasons, model.AttentionReason{Code: "idle", Text: fmt.Sprintf("Idle for %s", formatAttentionDuration(idleFor)), Weight: w})
			}
		}
	}

	if reason := recentActivityReason(in); reason != nil {
		out.Score += reason.Weight
		out.Reasons = append(out.Reasons, *reason)
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

	if in.Unread {
		w := unreadAttentionWeight
		out.Score += w
		out.Reasons = append(out.Reasons, model.AttentionReason{Code: "unread", Text: "Latest assessment is unread", Weight: w})
	}

	if in.OpenTodoCount > 0 {
		w := openTodosAttentionWeight
		out.Score += w
		item := "item"
		if in.OpenTodoCount > 1 {
			item = "items"
		}
		out.Reasons = append(out.Reasons, model.AttentionReason{
			Code:   "has_open_todos",
			Text:   fmt.Sprintf("%d open TODO %s", in.OpenTodoCount, item),
			Weight: w,
		})
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
		if idleFor > recentAttentionWindow {
			return true, classifiedIdleOutcome{Status: model.StatusIdle}
		}
		return true, classifiedIdleOutcome{
			Status: model.StatusIdle,
			Reason: recentCompletionReason("session_completed", "Recently completed", idleFor, in.StuckThreshold),
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

func recentCompletionReason(code, text string, idleFor, floor time.Duration) *model.AttentionReason {
	weight := taperedAttentionWeight(idleFor, floor, recentAttentionWindow, recentCompletionAttentionWeight)
	if weight == 0 {
		return nil
	}
	return &model.AttentionReason{
		Code:   code,
		Text:   fmt.Sprintf("%s; idle for %s", text, formatAttentionDuration(idleFor)),
		Weight: weight,
	}
}

func recentActivityReason(in Input) *model.AttentionReason {
	if !in.HasActivity || in.LastActivity.IsZero() {
		return nil
	}
	idleFor := in.Now.Sub(in.LastActivity)
	if idleFor <= in.ActiveThreshold {
		return nil
	}
	weight := taperedAttentionWeight(idleFor, in.ActiveThreshold, recentAttentionWindow, recentActivityBonusWeight)
	if weight == 0 {
		return nil
	}
	return &model.AttentionReason{
		Code:   "recent_activity",
		Text:   fmt.Sprintf("Recent activity %s ago", formatAttentionDuration(idleFor)),
		Weight: weight,
	}
}

func newProjectWeight(in Input) (int, bool) {
	if in.CreatedAt.IsZero() {
		return 0, false
	}
	age := in.Now.Sub(in.CreatedAt)
	if age >= newProjectAttentionWindow {
		return 0, false
	}
	w := taperedAttentionWeight(age, 0, newProjectAttentionWindow, newProjectAttentionWeight)
	if w < noActivityBaseWeight {
		w = noActivityBaseWeight
	}
	return w, true
}

func taperedAttentionWeight(age, floor, ceiling time.Duration, maxWeight int) int {
	if maxWeight <= 0 || ceiling <= floor {
		return 0
	}
	if age <= floor {
		return maxWeight
	}
	if age >= ceiling {
		return 0
	}

	window := int64(ceiling - floor)
	remaining := int64(ceiling - age)
	weight := int((remaining * int64(maxWeight)) / window)
	if weight < 1 {
		return 1
	}
	if weight > maxWeight {
		return maxWeight
	}
	return weight
}
