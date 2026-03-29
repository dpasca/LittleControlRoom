package sessionclassify

import (
	"fmt"
	"strings"
	"time"

	"lcroom/internal/model"
)

type EffectiveAssessmentInput struct {
	Status               model.SessionClassificationStatus
	Category             model.SessionCategory
	Summary              string
	LastEventAt          time.Time
	LatestTurnStateKnown bool
	LatestTurnCompleted  bool
	Now                  time.Time
	StuckThreshold       time.Duration
}

type EffectiveAssessment struct {
	Status   model.SessionClassificationStatus
	Category model.SessionCategory
	Summary  string
	Derived  bool
}

func EffectiveAssessmentStallThreshold(activeThreshold, stuckThreshold time.Duration) time.Duration {
	threshold := activeThreshold
	if threshold < 30*time.Minute {
		threshold = 30 * time.Minute
	}
	if stuckThreshold > 0 && (threshold <= 0 || stuckThreshold < threshold) {
		threshold = stuckThreshold
	}
	return threshold
}

// DeriveEffectiveAssessment keeps the stored classifier result as the source of
// truth, but upgrades stale completed in-progress turns into a blocked view when
// the latest structured workflow evidence shows the turn never actually
// finished.
func DeriveEffectiveAssessment(in EffectiveAssessmentInput) EffectiveAssessment {
	out := EffectiveAssessment{
		Status:   in.Status,
		Category: in.Category,
		Summary:  strings.TrimSpace(in.Summary),
	}

	if in.Status != model.ClassificationCompleted || in.Category != model.SessionCategoryInProgress {
		return out
	}
	if !in.LatestTurnStateKnown || in.LatestTurnCompleted {
		return out
	}
	if in.LastEventAt.IsZero() || in.Now.IsZero() || in.StuckThreshold <= 0 {
		return out
	}

	idleFor := in.Now.Sub(in.LastEventAt)
	if idleFor < in.StuckThreshold {
		return out
	}

	out.Category = model.SessionCategoryBlocked
	out.Summary = fmt.Sprintf("Last turn never completed; idle %s, likely stalled or disconnected.", formatAssessmentDuration(idleFor))
	out.Derived = true
	return out
}

func formatAssessmentDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	rounded := d.Round(time.Minute)
	if rounded < time.Minute {
		return "<1m"
	}

	totalMinutes := int64(rounded / time.Minute)
	days := totalMinutes / (24 * 60)
	hours := (totalMinutes % (24 * 60)) / 60
	minutes := totalMinutes % 60

	switch {
	case days > 0:
		return fmt.Sprintf("%dd %dh", days, hours)
	case hours > 0:
		return fmt.Sprintf("%dh %dm", hours, minutes)
	default:
		return fmt.Sprintf("%dm", minutes)
	}
}
