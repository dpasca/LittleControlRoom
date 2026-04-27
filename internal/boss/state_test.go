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

func TestBuildStateBriefKeepsRoutineRepoStateAsReferenceMetadata(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_800_000_000, 0)
	snapshot := StateSnapshot{
		TotalProjects:  1,
		ActiveProjects: 1,
		DirtyProjects:  1,
		HotProjects: []ProjectBrief{{
			Name:           "FCX",
			Path:           "/tmp/okmain",
			Status:         model.StatusActive,
			RepoBranch:     "okmain",
			RepoDirty:      true,
			RepoSyncStatus: model.RepoSyncAhead,
			RepoAheadCount: 3,
			LatestSummary:  "Notification visibility fix around the Other tab is ready for validation.",
		}},
	}

	brief := BuildStateBrief(snapshot, now)
	lines := strings.Split(brief, "\n")
	operational := ""
	reference := ""
	for _, line := range lines {
		if strings.HasPrefix(line, "- FCX") {
			operational = line
		}
		if strings.Contains(line, "Reference metadata") {
			reference = line
		}
	}
	if operational == "" || reference == "" {
		t.Fatalf("brief missing operational/reference split:\n%s", brief)
	}
	if !strings.Contains(operational, "Notification visibility fix") {
		t.Fatalf("operational line should lead with work substance:\n%s", operational)
	}
	for _, noisy := range []string{"dirty", "ahead +3", "okmain"} {
		if strings.Contains(operational, noisy) {
			t.Fatalf("operational line should not include routine repo metadata %q:\n%s", noisy, operational)
		}
	}
	for _, want := range []string{"path=/tmp/okmain", "branch=okmain", "repo=dirty, ahead +3"} {
		if !strings.Contains(reference, want) {
			t.Fatalf("reference metadata missing %q:\n%s", want, reference)
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
