package attention

import (
	"testing"
	"time"

	"lcroom/internal/model"
)

func TestScoreIgnoresNonZeroCommandExits(t *testing.T) {
	now := time.Date(2026, 3, 5, 16, 0, 0, 0, time.UTC)
	in := Input{
		Now:             now,
		HasActivity:     true,
		LastActivity:    now.Add(-6 * time.Hour),
		ActiveThreshold: 20 * time.Minute,
		StuckThreshold:  4 * time.Hour,
		ErrorCount:      2,
	}

	out := Score(in)
	if out.Status != "idle" {
		t.Fatalf("status = %s, want idle", out.Status)
	}
	if out.Score != 9 {
		t.Fatalf("score = %d, want 9", out.Score)
	}
	for _, reason := range out.Reasons {
		if reason.Code == "error_markers" {
			t.Fatalf("did not expect error_markers reason, got %#v", out.Reasons)
		}
	}
}

func TestScoreSnoozedDropsAttention(t *testing.T) {
	now := time.Now()
	until := now.Add(30 * time.Minute)
	in := Input{
		Now:             now,
		HasActivity:     true,
		LastActivity:    now.Add(-2 * time.Hour),
		ActiveThreshold: 20 * time.Minute,
		StuckThreshold:  4 * time.Hour,
		SnoozedUntil:    &until,
	}

	out := Score(in)
	if out.Score != 0 {
		t.Fatalf("score = %d, want 0 when snoozed", out.Score)
	}
}

func TestScoreActiveAddsAttentionReason(t *testing.T) {
	now := time.Date(2026, 3, 7, 0, 0, 0, 0, time.UTC)
	in := Input{
		Now:             now,
		HasActivity:     true,
		LastActivity:    now.Add(-10 * time.Minute),
		ActiveThreshold: 20 * time.Minute,
		StuckThreshold:  4 * time.Hour,
	}

	out := Score(in)
	if out.Status != "active" {
		t.Fatalf("status = %s, want active", out.Status)
	}
	if out.Score != activeAttentionWeight {
		t.Fatalf("score = %d, want %d", out.Score, activeAttentionWeight)
	}
	if len(out.Reasons) != 1 || out.Reasons[0].Code != "active" {
		t.Fatalf("expected active reason, got %#v", out.Reasons)
	}
}

func TestScoreRecentIdleAddsFreshnessBonus(t *testing.T) {
	now := time.Date(2026, 3, 7, 0, 0, 0, 0, time.UTC)
	in := Input{
		Now:             now,
		HasActivity:     true,
		LastActivity:    now.Add(-27 * time.Minute),
		ActiveThreshold: 20 * time.Minute,
		StuckThreshold:  4 * time.Hour,
	}

	out := Score(in)
	if out.Status != model.StatusIdle {
		t.Fatalf("status = %s, want idle", out.Status)
	}
	if out.Score != 29 {
		t.Fatalf("score = %d, want 29", out.Score)
	}
	if len(out.Reasons) != 2 || out.Reasons[0].Code != "idle" || out.Reasons[1].Code != "recent_activity" {
		t.Fatalf("expected idle + recent_activity reasons, got %#v", out.Reasons)
	}
}

func TestScoreRecentlyCompletedDowngradesStuck(t *testing.T) {
	now := time.Date(2026, 3, 6, 0, 0, 0, 0, time.UTC)
	in := Input{
		Now:                now,
		HasActivity:        true,
		LastActivity:       now.Add(-9 * time.Hour),
		ActiveThreshold:    20 * time.Minute,
		StuckThreshold:     4 * time.Hour,
		LatestTurnKnown:    true,
		LatestTurnComplete: true,
	}

	out := Score(in)
	if out.Status != "idle" {
		t.Fatalf("status = %s, want idle", out.Status)
	}
	if out.Score != 26 {
		t.Fatalf("score = %d, want 26", out.Score)
	}
	if len(out.Reasons) != 2 || out.Reasons[0].Code != "recently_completed" || out.Reasons[1].Code != "recent_activity" {
		t.Fatalf("expected recently_completed + recent_activity reasons, got %#v", out.Reasons)
	}
}

func TestScoreCompletedButOldIsIdle(t *testing.T) {
	now := time.Date(2026, 3, 6, 0, 0, 0, 0, time.UTC)
	in := Input{
		Now:                now,
		HasActivity:        true,
		LastActivity:       now.Add(-72 * time.Hour),
		ActiveThreshold:    20 * time.Minute,
		StuckThreshold:     4 * time.Hour,
		LatestTurnKnown:    true,
		LatestTurnComplete: true,
	}

	out := Score(in)
	if out.Status != "idle" {
		t.Fatalf("status = %s, want idle", out.Status)
	}
}

func TestScoreCompletedClassificationDowngradesStuck(t *testing.T) {
	now := time.Date(2026, 3, 6, 0, 0, 0, 0, time.UTC)
	in := Input{
		Now:                        now,
		HasActivity:                true,
		LastActivity:               now.Add(-72 * time.Hour),
		ActiveThreshold:            20 * time.Minute,
		StuckThreshold:             4 * time.Hour,
		LatestSessionCategoryKnown: true,
		LatestSessionCategory:      model.SessionCategoryCompleted,
	}

	out := Score(in)
	if out.Status != "idle" {
		t.Fatalf("status = %s, want idle", out.Status)
	}
	if out.Score != 0 {
		t.Fatalf("score = %d, want 0", out.Score)
	}
	if len(out.Reasons) != 0 {
		t.Fatalf("expected no attention reasons for stale completed work, got %#v", out.Reasons)
	}
}

func TestScoreRecentCompletedClassificationKeepsAttention(t *testing.T) {
	now := time.Date(2026, 3, 6, 0, 0, 0, 0, time.UTC)
	in := Input{
		Now:                        now,
		HasActivity:                true,
		LastActivity:               now.Add(-36 * time.Hour),
		ActiveThreshold:            20 * time.Minute,
		StuckThreshold:             4 * time.Hour,
		LatestSessionCategoryKnown: true,
		LatestSessionCategory:      model.SessionCategoryCompleted,
	}

	out := Score(in)
	if out.Status != "idle" {
		t.Fatalf("status = %s, want idle", out.Status)
	}
	if out.Score != 15 {
		t.Fatalf("score = %d, want 15", out.Score)
	}
	if len(out.Reasons) != 2 || out.Reasons[0].Code != "session_completed" || out.Reasons[1].Code != "recent_activity" {
		t.Fatalf("expected session_completed + recent_activity reasons, got %#v", out.Reasons)
	}
}

func TestScoreWaitingForUserDowngradesStuck(t *testing.T) {
	now := time.Date(2026, 3, 6, 0, 0, 0, 0, time.UTC)
	in := Input{
		Now:                        now,
		HasActivity:                true,
		LastActivity:               now.Add(-9 * time.Hour),
		ActiveThreshold:            20 * time.Minute,
		StuckThreshold:             4 * time.Hour,
		LatestSessionCategoryKnown: true,
		LatestSessionCategory:      model.SessionCategoryWaitingForUser,
	}

	out := Score(in)
	if out.Status != "idle" {
		t.Fatalf("status = %s, want idle", out.Status)
	}
	if out.Score != waitingForUserAttentionWeight+8 {
		t.Fatalf("score = %d, want %d", out.Score, waitingForUserAttentionWeight+8)
	}
	if len(out.Reasons) != 2 || out.Reasons[0].Code != "waiting_for_user" || out.Reasons[1].Code != "recent_activity" {
		t.Fatalf("expected waiting_for_user + recent_activity reasons, got %#v", out.Reasons)
	}
}

func TestScoreNeedsFollowUpUsesSpecificReason(t *testing.T) {
	now := time.Date(2026, 3, 6, 0, 0, 0, 0, time.UTC)
	in := Input{
		Now:                        now,
		HasActivity:                true,
		LastActivity:               now.Add(-9 * time.Hour),
		ActiveThreshold:            20 * time.Minute,
		StuckThreshold:             4 * time.Hour,
		LatestSessionCategoryKnown: true,
		LatestSessionCategory:      model.SessionCategoryNeedsFollowUp,
	}

	out := Score(in)
	if out.Status != model.StatusPossiblyStuck {
		t.Fatalf("status = %s, want possibly_stuck", out.Status)
	}
	if out.Score != needsFollowUpAttentionWeight+8 {
		t.Fatalf("score = %d, want %d", out.Score, needsFollowUpAttentionWeight+8)
	}
	if len(out.Reasons) != 2 || out.Reasons[0].Code != "needs_follow_up" || out.Reasons[1].Code != "recent_activity" {
		t.Fatalf("expected needs_follow_up + recent_activity reasons, got %#v", out.Reasons)
	}
}

func TestScoreInProgressUsesSpecificReason(t *testing.T) {
	now := time.Date(2026, 3, 6, 0, 0, 0, 0, time.UTC)
	in := Input{
		Now:                        now,
		HasActivity:                true,
		LastActivity:               now.Add(-9 * time.Hour),
		ActiveThreshold:            20 * time.Minute,
		StuckThreshold:             4 * time.Hour,
		LatestSessionCategoryKnown: true,
		LatestSessionCategory:      model.SessionCategoryInProgress,
	}

	out := Score(in)
	if out.Status != model.StatusPossiblyStuck {
		t.Fatalf("status = %s, want possibly_stuck", out.Status)
	}
	if out.Score != inProgressAttentionWeight+8 {
		t.Fatalf("score = %d, want %d", out.Score, inProgressAttentionWeight+8)
	}
	if len(out.Reasons) != 2 || out.Reasons[0].Code != "in_progress" || out.Reasons[1].Code != "recent_activity" {
		t.Fatalf("expected in_progress + recent_activity reasons, got %#v", out.Reasons)
	}
}

func TestScoreBlockedUsesSpecificReason(t *testing.T) {
	now := time.Date(2026, 3, 6, 0, 0, 0, 0, time.UTC)
	in := Input{
		Now:                        now,
		HasActivity:                true,
		LastActivity:               now.Add(-9 * time.Hour),
		ActiveThreshold:            20 * time.Minute,
		StuckThreshold:             4 * time.Hour,
		LatestSessionCategoryKnown: true,
		LatestSessionCategory:      model.SessionCategoryBlocked,
	}

	out := Score(in)
	if out.Status != model.StatusPossiblyStuck {
		t.Fatalf("status = %s, want possibly_stuck", out.Status)
	}
	if out.Score != blockedAttentionWeight+8 {
		t.Fatalf("score = %d, want %d", out.Score, blockedAttentionWeight+8)
	}
	if len(out.Reasons) != 2 || out.Reasons[0].Code != "blocked" || out.Reasons[1].Code != "recent_activity" {
		t.Fatalf("expected blocked + recent_activity reasons, got %#v", out.Reasons)
	}
}

func TestScoreCompletedRecencyTapersOverTime(t *testing.T) {
	now := time.Date(2026, 3, 6, 0, 0, 0, 0, time.UTC)
	inputs := []time.Duration{24 * time.Hour, 48 * time.Hour, 60 * time.Hour}
	scores := make([]int, 0, len(inputs))

	for _, age := range inputs {
		out := Score(Input{
			Now:                        now,
			HasActivity:                true,
			LastActivity:               now.Add(-age),
			ActiveThreshold:            20 * time.Minute,
			StuckThreshold:             4 * time.Hour,
			LatestSessionCategoryKnown: true,
			LatestSessionCategory:      model.SessionCategoryCompleted,
		})
		scores = append(scores, out.Score)
	}

	if !(scores[0] > scores[1] && scores[1] > scores[2]) {
		t.Fatalf("expected completed scores to taper over time, got %v", scores)
	}
}

func TestScoreCompletedAssessmentSuppressesLongRunning(t *testing.T) {
	now := time.Date(2026, 3, 7, 0, 0, 0, 0, time.UTC)
	in := Input{
		Now:                        now,
		HasActivity:                true,
		LastActivity:               now.Add(-10 * time.Minute),
		LatestSessionStart:         now.Add(-3 * time.Hour),
		LatestSessionCategoryKnown: true,
		LatestSessionCategory:      model.SessionCategoryCompleted,
		ActiveThreshold:            20 * time.Minute,
		StuckThreshold:             4 * time.Hour,
	}

	out := Score(in)
	if out.Score != activeAttentionWeight {
		t.Fatalf("score = %d, want %d", out.Score, activeAttentionWeight)
	}
	for _, reason := range out.Reasons {
		if reason.Code == "long_running" {
			t.Fatalf("did not expect long_running reason, got %#v", out.Reasons)
		}
	}
}

func TestScoreReasonUsesDaysHoursMinutes(t *testing.T) {
	now := time.Date(2026, 3, 6, 0, 0, 0, 0, time.UTC)
	in := Input{
		Now:             now,
		HasActivity:     true,
		LastActivity:    now.Add(-(2*time.Hour + 12*time.Minute + 20*time.Second)),
		ActiveThreshold: 20 * time.Minute,
		StuckThreshold:  4 * time.Hour,
	}

	out := Score(in)
	if len(out.Reasons) == 0 {
		t.Fatalf("expected at least one reason")
	}
	if got, want := out.Reasons[0].Text, "Idle for 2h 12m"; got != want {
		t.Fatalf("reason text = %q, want %q", got, want)
	}
}

func TestFormatAttentionDuration(t *testing.T) {
	tests := []struct {
		name string
		in   time.Duration
		want string
	}{
		{
			name: "minutes only",
			in:   12*time.Minute + 29*time.Second,
			want: "12m",
		},
		{
			name: "hours and minutes",
			in:   3*time.Hour + 5*time.Minute + 31*time.Second,
			want: "3h 6m",
		},
		{
			name: "days hours minutes",
			in:   27*time.Hour + 1*time.Minute,
			want: "1d 3h 1m",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatAttentionDuration(tt.in); got != tt.want {
				t.Fatalf("formatAttentionDuration(%s) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestScoreRepoDirtyAddsAttentionReason(t *testing.T) {
	now := time.Date(2026, 3, 6, 0, 0, 0, 0, time.UTC)
	in := Input{
		Now:             now,
		RepoDirty:       true,
		HasActivity:     true,
		LastActivity:    now.Add(-30 * time.Minute),
		ActiveThreshold: 20 * time.Minute,
		StuckThreshold:  4 * time.Hour,
	}

	out := Score(in)
	if out.Score != 44 {
		t.Fatalf("score = %d, want 44", out.Score)
	}
	found := false
	for _, reason := range out.Reasons {
		if reason.Code == "repo_dirty" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected repo_dirty reason, got %#v", out.Reasons)
	}
}

func TestScoreOpenTodosAddsAttentionReason(t *testing.T) {
	now := time.Date(2026, 3, 6, 0, 0, 0, 0, time.UTC)
	in := Input{
		Now:             now,
		HasActivity:     true,
		LastActivity:    now.Add(-30 * time.Minute),
		ActiveThreshold: 20 * time.Minute,
		StuckThreshold:  4 * time.Hour,
		OpenTodoCount:   3,
	}

	out := Score(in)
	found := false
	for _, reason := range out.Reasons {
		if reason.Code == "has_open_todos" {
			found = true
			if reason.Weight != openTodosAttentionWeight {
				t.Fatalf("weight = %d, want %d", reason.Weight, openTodosAttentionWeight)
			}
			if reason.Text != "3 open TODO items" {
				t.Fatalf("text = %q, want %q", reason.Text, "3 open TODO items")
			}
			break
		}
	}
	if !found {
		t.Fatalf("expected has_open_todos reason, got %#v", out.Reasons)
	}
}

func TestScoreOpenTodosSingularItem(t *testing.T) {
	now := time.Date(2026, 3, 6, 0, 0, 0, 0, time.UTC)
	in := Input{
		Now:             now,
		HasActivity:     true,
		LastActivity:    now.Add(-30 * time.Minute),
		ActiveThreshold: 20 * time.Minute,
		StuckThreshold:  4 * time.Hour,
		OpenTodoCount:   1,
	}

	out := Score(in)
	found := false
	for _, reason := range out.Reasons {
		if reason.Code == "has_open_todos" {
			found = true
			if reason.Text != "1 open TODO item" {
				t.Fatalf("text = %q, want %q", reason.Text, "1 open TODO item")
			}
			break
		}
	}
	if !found {
		t.Fatalf("expected has_open_todos reason, got %#v", out.Reasons)
	}
}

func TestScoreZeroTodosNoReason(t *testing.T) {
	now := time.Date(2026, 3, 6, 0, 0, 0, 0, time.UTC)
	in := Input{
		Now:             now,
		HasActivity:     true,
		LastActivity:    now.Add(-30 * time.Minute),
		ActiveThreshold: 20 * time.Minute,
		StuckThreshold:  4 * time.Hour,
		OpenTodoCount:   0,
	}

	out := Score(in)
	for _, reason := range out.Reasons {
		if reason.Code == "has_open_todos" {
			t.Fatalf("did not expect has_open_todos reason, got %#v", out.Reasons)
		}
	}
}

func TestScoreUnreadAddsAttentionReason(t *testing.T) {
	now := time.Date(2026, 3, 6, 0, 0, 0, 0, time.UTC)
	in := Input{
		Now:             now,
		Unread:          true,
		HasActivity:     true,
		LastActivity:    now.Add(-30 * time.Minute),
		ActiveThreshold: 20 * time.Minute,
		StuckThreshold:  4 * time.Hour,
	}

	out := Score(in)
	if out.Score != 41 {
		t.Fatalf("score = %d, want 41", out.Score)
	}
	found := false
	for _, reason := range out.Reasons {
		if reason.Code == "unread" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected unread reason, got %#v", out.Reasons)
	}
}
