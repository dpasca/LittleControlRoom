package boss

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lcroom/internal/config"
	"lcroom/internal/events"
	"lcroom/internal/model"
	"lcroom/internal/service"
	"lcroom/internal/store"
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

func TestBuildStateBriefIncludesOpenAgentTasks(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_800_000_000, 0)
	snapshot := StateSnapshot{
		TotalProjects: 1,
		OpenAgentTasks: []AgentTaskBrief{{
			ID:            "agt_processes",
			Title:         "Investigate runaway dev processes",
			Kind:          model.AgentTaskKindAgent,
			Status:        model.AgentTaskStatusActive,
			Summary:       "Node debug listener is still hot; Python process needs a cwd check.",
			Capabilities:  []string{"process.inspect", "process.terminate"},
			Provider:      model.SessionSourceCodex,
			SessionID:     "codex:ses-1",
			LastTouchedAt: now.Add(-8 * time.Minute),
			Resources: []model.AgentTaskResource{
				{Kind: model.AgentTaskResourceProcess, PID: 49995, Label: "ts-node-dev"},
				{Kind: model.AgentTaskResourcePort, Port: 9229, Label: "debug listener"},
				{Kind: model.AgentTaskResourceEngineerSession, Provider: model.SessionSourceCodex, SessionID: "codex:ses-1"},
			},
		}},
	}

	brief := BuildStateBrief(snapshot, now)
	for _, want := range []string{
		"Open delegated agent tasks (separate from project TODOs):",
		"Investigate runaway dev processes",
		"agt_processes",
		"agent/active",
		"touched 8m ago",
		"process.inspect, process.terminate",
		"pid 49995 ts-node-dev",
		"port 9229 debug listener",
		"codex session codex:ses-1",
	} {
		if !strings.Contains(brief, want) {
			t.Fatalf("brief missing %q:\n%s", want, brief)
		}
	}
}

func TestLoadStateSnapshotIncludesOpenAgentTasksWithPrivacyFiltering(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	cfg.DBPath = filepath.Join(cfg.DataDir, "little-control-room.sqlite")
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	svc := service.New(cfg, st, events.NewBus(), nil)

	publicTitle := "Follow up on temp process investigation"
	if _, err := svc.CreateAgentTask(ctx, model.CreateAgentTaskInput{
		Title: publicTitle,
		Kind:  model.AgentTaskKindAgent,
	}); err != nil {
		t.Fatalf("CreateAgentTask() error = %v", err)
	}
	if _, err := svc.CreateAgentTask(ctx, model.CreateAgentTaskInput{
		Title: "Private delegated cleanup",
		Kind:  model.AgentTaskKindAgent,
	}); err != nil {
		t.Fatalf("CreateAgentTask(private) error = %v", err)
	}

	snapshot, err := LoadStateSnapshot(ctx, svc, time.Unix(1_800_000_000, 0))
	if err != nil {
		t.Fatalf("LoadStateSnapshot() error = %v", err)
	}
	if len(snapshot.OpenAgentTasks) != 2 {
		t.Fatalf("open agent tasks = %#v, want both created tasks", snapshot.OpenAgentTasks)
	}

	privateSnapshot, err := LoadStateSnapshot(ctx, svc, time.Unix(1_800_000_000, 0), StateSnapshotOptions{
		PrivacyMode:     true,
		PrivacyPatterns: []string{"*private*"},
	})
	if err != nil {
		t.Fatalf("LoadStateSnapshot() with privacy error = %v", err)
	}
	if len(privateSnapshot.OpenAgentTasks) != 1 || privateSnapshot.OpenAgentTasks[0].Title != publicTitle {
		t.Fatalf("privacy mode should include only non-private agent tasks, got %#v", privateSnapshot.OpenAgentTasks)
	}
}

func TestPanelTextsStayCompact(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_800_000_000, 0)
	snapshot := StateSnapshot{
		PendingClassifications: 1,
		HotProjects: []ProjectBrief{
			{Name: "Alpha", Status: model.StatusIdle, AttentionScore: 20, LastActivity: now.Add(-time.Hour), RepoDirty: true, LatestSummary: "Ready for review."},
			{Name: "Beta", Status: model.StatusActive, AttentionScore: 10},
		},
	}
	attention := AttentionText(snapshot, now)
	if !strings.Contains(attention, "idle 20") || !strings.Contains(attention, "dirty") || !strings.Contains(attention, "Ready for review.") {
		t.Fatalf("unexpected attention text:\n%s", attention)
	}
}

func TestAttentionTextIncludesAgentTasksBeforeProjects(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_800_000_000, 0)
	snapshot := StateSnapshot{
		OpenAgentTasks: []AgentTaskBrief{{
			ID:        "agt_cursor",
			Title:     "Revoke Cursor GitHub access",
			Status:    model.AgentTaskStatusActive,
			Provider:  model.SessionSourceCodex,
			SessionID: "thread-agent-1",
		}},
		HotProjects: []ProjectBrief{{
			Name:           "Alpha",
			Status:         model.StatusIdle,
			AttentionScore: 20,
			LatestSummary:  "Ready for review.",
		}},
	}

	attention := AttentionTextWithLimit(snapshot, now, 2)
	lines := strings.Split(attention, "\n")
	if len(lines) != 2 {
		t.Fatalf("attention lines = %d, want 2:\n%s", len(lines), attention)
	}
	if !strings.Contains(lines[0], "agent | Revoke Cursor GitHub access") || !strings.Contains(lines[0], "codex thread-agent-1") {
		t.Fatalf("first attention line should describe agent task:\n%s", attention)
	}
	if !strings.Contains(lines[1], "Ready for review.") {
		t.Fatalf("second attention line should keep project item:\n%s", attention)
	}
}

func TestAttentionTextLabelsWaitingAgentTasksAsReview(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_800_000_000, 0)
	snapshot := StateSnapshot{
		OpenAgentTasks: []AgentTaskBrief{{
			ID:      "agt_diff",
			Title:   "Diff duplicate Codex skills",
			Status:  model.AgentTaskStatusWaiting,
			Summary: "Found one canonical skill and one stale duplicate.",
		}},
	}

	attention := AttentionText(snapshot, now)
	if !strings.Contains(attention, "review | Diff duplicate Codex skills") ||
		!strings.Contains(attention, "Found one canonical skill") {
		t.Fatalf("waiting agent task should read as a review handoff:\n%s", attention)
	}
}

func TestAttentionTextShowsWaitingAgentTaskDecisionWhenSummaryMissing(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_800_000_000, 0)
	snapshot := StateSnapshot{
		OpenAgentTasks: []AgentTaskBrief{{
			ID:            "agt_diff",
			Title:         "Diff duplicate Codex skills",
			EngineerName:  "Dennis",
			Status:        model.AgentTaskStatusWaiting,
			Provider:      model.SessionSourceCodex,
			SessionID:     "thread-agent-1",
			LastTouchedAt: now.Add(-time.Hour),
		}},
	}

	attention := AttentionText(snapshot, now)
	for _, want := range []string{"review | Diff duplicate Codex skills", "Dennis", "ready to close or continue", "touched 1h ago"} {
		if !strings.Contains(attention, want) {
			t.Fatalf("waiting agent task should show the needed decision %q:\n%s", want, attention)
		}
	}
}

func TestAttentionTextCleansWaitingAgentTaskSummaryPunctuation(t *testing.T) {
	t.Parallel()

	snapshot := StateSnapshot{
		OpenAgentTasks: []AgentTaskBrief{{
			ID:           "agt_diff",
			Title:        "Diff duplicate Codex skills",
			EngineerName: "Dennis",
			Status:       model.AgentTaskStatusWaiting,
			Summary:      "Confirmed: the removed imagegen copy was the user-local directory:",
		}},
	}

	attention := AttentionText(snapshot, time.Unix(1_800_000_000, 0))
	if !strings.Contains(attention, "Confirmed: the removed imagegen copy was the user-local directory.") {
		t.Fatalf("waiting agent summary should end naturally:\n%s", attention)
	}
	if strings.Contains(attention, "directory:") {
		t.Fatalf("waiting agent summary kept dangling colon:\n%s", attention)
	}
}

func TestSelectRecentAttentionProjectsPrefersPresentRecentProjects(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_800_000_000, 0)
	projects := []model.ProjectSummary{
		{Name: "Missing newest", LastActivity: now, AttentionScore: 99, PresentOnDisk: false},
		{Name: "Present older", LastActivity: now.Add(-time.Hour), AttentionScore: 10, PresentOnDisk: true},
		{Name: "Present newest", LastActivity: now.Add(-time.Minute), AttentionScore: 5, PresentOnDisk: true},
	}

	selected := selectRecentAttentionProjects(projects, 2)
	if len(selected) != 2 {
		t.Fatalf("selected len = %d, want 2", len(selected))
	}
	if selected[0].Name != "Present newest" || selected[1].Name != "Present older" {
		t.Fatalf("selected order = %#v, want present projects by recent activity", selected)
	}
}

func TestSelectRecentAttentionProjectsSkipsCachedPresentPathsMissingOnDisk(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_800_000_000, 0)
	root := t.TempDir()
	presentOlder := filepath.Join(root, "present-older")
	presentNewest := filepath.Join(root, "present-newest")
	if err := os.Mkdir(presentOlder, 0o755); err != nil {
		t.Fatalf("mkdir present older: %v", err)
	}
	if err := os.Mkdir(presentNewest, 0o755); err != nil {
		t.Fatalf("mkdir present newest: %v", err)
	}
	projects := []model.ProjectSummary{
		{Name: "Missing newest", Path: filepath.Join(root, "missing-newest"), LastActivity: now, AttentionScore: 99, PresentOnDisk: true},
		{Name: "Present older", Path: presentOlder, LastActivity: now.Add(-time.Hour), AttentionScore: 10, PresentOnDisk: true},
		{Name: "Present newest", Path: presentNewest, LastActivity: now.Add(-time.Minute), AttentionScore: 5, PresentOnDisk: true},
	}

	selected := selectRecentAttentionProjectsWithPresence(projects, 2, projectCurrentlyPresent)
	if len(selected) != 2 {
		t.Fatalf("selected len = %d, want 2", len(selected))
	}
	if selected[0].Name != "Present newest" || selected[1].Name != "Present older" {
		t.Fatalf("selected order = %#v, want currently present projects by recent activity", selected)
	}
}

func TestFilterProjectSummariesByPrivacyHidesMatchingProjectNames(t *testing.T) {
	t.Parallel()

	projects := []model.ProjectSummary{
		{Name: "PublicApp", Path: "/tmp/public"},
		{Name: "SecretClient", Path: "/tmp/secret"},
		{Name: "AnotherPublicApp", Path: "/tmp/public-2"},
	}

	filtered := filterProjectSummariesByPrivacy(projects, []string{"*secret*"})
	if len(filtered) != 2 {
		t.Fatalf("filtered len = %d, want 2: %#v", len(filtered), filtered)
	}
	for _, project := range filtered {
		if project.Name == "SecretClient" {
			t.Fatalf("private project should not be visible in boss state: %#v", filtered)
		}
	}
}
