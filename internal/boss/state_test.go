package boss

import (
	"strings"
	"testing"
	"time"

	"lcroom/internal/model"
)

func TestBuildStateBriefSummarizesHotProjects(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_800_000_000, 0)
	snapshot := StateSnapshot{
		TotalProjects:          3,
		ActiveProjects:         1,
		PossiblyStuckProjects:  1,
		DirtyProjects:          2,
		ConflictProjects:       1,
		PendingClassifications: 2,
		HotProjects: []ProjectBrief{{
			Name:                 "Alpha",
			Status:               model.StatusPossiblyStuck,
			AttentionScore:       42,
			LastActivity:         now.Add(-2 * time.Hour),
			RepoBranch:           "feature/boss",
			RepoDirty:            true,
			RepoConflict:         true,
			RepoSyncStatus:       model.RepoSyncDiverged,
			RepoAheadCount:       1,
			RepoBehindCount:      2,
			OpenTODOCount:        3,
			LatestSummary:        "Waiting on a design decision before continuing implementation.",
			ClassificationStatus: model.ClassificationCompleted,
			Reasons: []model.AttentionReason{{
				Text: "Latest session is waiting for the user",
			}},
		}},
	}

	brief := BuildStateBrief(snapshot, now)
	for _, want := range []string{
		"Visible projects: 3",
		"AI assessment queue: 2 pending/running.",
		"Alpha",
		"possibly_stuck",
		"diverged +1/-2",
		"Latest session is waiting for the user",
	} {
		if !strings.Contains(brief, want) {
			t.Fatalf("brief missing %q:\n%s", want, brief)
		}
	}
}

func TestPanelTextsStayCompact(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_800_000_000, 0)
	snapshot := StateSnapshot{
		PendingClassifications: 1,
		HotProjects: []ProjectBrief{
			{Name: "Alpha", Status: model.StatusIdle, AttentionScore: 20, LastActivity: now.Add(-time.Hour)},
			{Name: "Beta", Status: model.StatusActive, AttentionScore: 10},
		},
	}
	attention := AttentionText(snapshot, now)
	if !strings.Contains(attention, "Alpha") || !strings.Contains(attention, "1h ago") {
		t.Fatalf("unexpected attention text:\n%s", attention)
	}
	notes := NotesText(snapshot)
	if !strings.Contains(notes, "Panels are stationary") || !strings.Contains(notes, "Assessment queue: 1") {
		t.Fatalf("unexpected notes text:\n%s", notes)
	}
}
