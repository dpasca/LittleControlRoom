package attention

import (
	"time"

	"lcroom/internal/model"
)

func AssessmentUnread(summary model.ProjectSummary) bool {
	unreadAt := AssessmentUnreadAt(summary)
	if unreadAt.IsZero() {
		return false
	}
	return summary.LastSessionSeenAt.IsZero() || summary.LastSessionSeenAt.Before(unreadAt)
}

func AssessmentUnreadAt(summary model.ProjectSummary) time.Time {
	if summary.LatestSessionClassification != model.ClassificationCompleted {
		return time.Time{}
	}
	if summary.LatestTurnStateKnown && summary.LatestTurnCompleted && !summary.LatestSessionLastEventAt.IsZero() {
		return summary.LatestSessionLastEventAt
	}
	if !summary.LatestSessionClassificationUpdatedAt.IsZero() {
		return summary.LatestSessionClassificationUpdatedAt
	}
	return time.Time{}
}
