package tui

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lcroom/internal/browserctl"
	"lcroom/internal/codexapp"
	"lcroom/internal/commands"
	"lcroom/internal/model"
	"lcroom/internal/projectrun"
	"lcroom/internal/service"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestDispatchCleanOpensStaleWorktreeAudit(t *testing.T) {
	updated, cmd := (Model{}).dispatchCommand(commands.Invocation{Kind: commands.KindClean})
	got := updated.(Model)
	if cmd == nil || got.staleWorktreeCleanup == nil || !got.staleWorktreeCleanup.Loading {
		t.Fatalf("/clean state = %#v, cmd=%v", got.staleWorktreeCleanup, cmd)
	}
	if got.codexCleanup != nil {
		t.Fatal("/clean must not open permanent Codex storage cleanup")
	}
}

func TestStaleWorktreeAuditPreselectsEligibleAndExcludesLiveUnsafeSessions(t *testing.T) {
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	idle := staleWorktreeCleanupTestCandidate("/tmp/demo--idle", "feature/idle", now.Add(-48*time.Hour))
	active := staleWorktreeCleanupTestCandidate("/tmp/demo--active", "feature/active", now.Add(-72*time.Hour))
	recentIdle := staleWorktreeCleanupTestCandidate("/tmp/demo--recent-idle", "feature/recent-idle", now.Add(-72*time.Hour))
	m := Model{
		nowFn:                        func() time.Time { return now },
		renderCachedSessionStateOnly: true,
		codexSnapshots: map[string]codexapp.Snapshot{
			idle.ProjectPath: {
				Provider: codexapp.ProviderCodex,
				Started:  true,
				Phase:    codexapp.SessionPhaseIdle,
			},
			active.ProjectPath: {
				Provider: codexapp.ProviderOpenCode,
				Started:  true,
				Busy:     true,
				Phase:    codexapp.SessionPhaseRunning,
			},
			recentIdle.ProjectPath: {
				Provider:       codexapp.ProviderClaudeCode,
				Started:        true,
				Phase:          codexapp.SessionPhaseIdle,
				LastActivityAt: now.Add(-time.Hour),
			},
		},
		staleWorktreeCleanup: &staleWorktreeCleanupDialogState{
			Chosen:  make(map[string]bool),
			Loading: true,
		},
	}
	updated, cmd := m.applyStaleWorktreeCleanupAudit(staleWorktreeCleanupAuditMsg{audit: service.StaleWorktreeCleanupAudit{
		AuditedAt:              now,
		ScannedLinkedWorktrees: 3,
		Candidates:             []service.StaleWorktreeCleanupCandidate{idle, active, recentIdle},
	}})
	if cmd != nil {
		t.Fatal("applying stale audit should not start removal")
	}
	got := updated.(Model)
	if len(got.staleWorktreeCleanup.Audit.Candidates) != 1 || got.staleWorktreeCleanup.Audit.Candidates[0].ProjectPath != idle.ProjectPath {
		t.Fatalf("filtered candidates = %#v, want only idle session", got.staleWorktreeCleanup.Audit.Candidates)
	}
	if !got.staleWorktreeCleanup.Chosen[idle.ProjectPath] || got.staleWorktreeCleanup.LiveExcluded != 2 {
		t.Fatalf("dialog selection = %#v, live excluded = %d", got.staleWorktreeCleanup.Chosen, got.staleWorktreeCleanup.LiveExcluded)
	}
	rendered := ansi.Strip(got.renderStaleWorktreeCleanupOverlay("", 120, 36))
	for _, want := range []string{"Clean stale worktrees", "[x] feature/idle", "idle Codex open", "Branches and AI conversation history are preserved"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("stale cleanup preview missing %q:\n%s", want, rendered)
		}
	}

	updated, cmd = got.updateStaleWorktreeCleanupMode(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if cmd == nil || !got.staleWorktreeCleanup.Removing || len(got.staleWorktreeCleanup.Queue) != 1 {
		t.Fatalf("Enter should start checked cleanup: %#v cmd=%v", got.staleWorktreeCleanup, cmd)
	}
}

func TestStaleWorktreeCuesShowInRowDetailFamilyAndFooter(t *testing.T) {
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	root := model.ProjectSummary{
		Path:             "/tmp/demo",
		Name:             "demo",
		PresentOnDisk:    true,
		WorktreeRootPath: "/tmp/demo",
		WorktreeKind:     model.WorktreeKindMain,
		RepoBranch:       "master",
	}
	child := staleWorktreeCleanupTestSummary(now)
	m := Model{
		nowFn:                        func() time.Time { return now },
		allProjects:                  []model.ProjectSummary{root, child},
		visibility:                   visibilityAllFolders,
		sortMode:                     sortByAttention,
		renderCachedSessionStateOnly: true,
		codexSnapshots: map[string]codexapp.Snapshot{
			child.Path: {
				Provider: codexapp.ProviderCodex,
				Started:  true,
				Phase:    codexapp.SessionPhaseIdle,
			},
		},
	}
	m.rebuildProjectList(child.Path)
	rendered := ansi.Strip(m.renderProjectList(120, 8))
	for _, want := range []string{"stale", "idle Codex open", "[1 linked, 1 stale]"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("project list missing %q: %s", want, rendered)
		}
	}

	detail := strings.Join(strings.Fields(ansi.Strip(renderProjectDetailSurface(m.buildProjectDetailSurface(child, model.ProjectDetail{}), 100))), " ")
	if !strings.Contains(detail, "Cleanup: stale — merged, clean, idle 2d; idle Codex session will be closed") {
		t.Fatalf("project detail missing stale cleanup explanation: %s", detail)
	}

	actions := m.worktreeFooterActions(120)
	footer := ""
	for _, action := range actions {
		footer += " " + ansi.Strip(action.render())
	}
	if !strings.Contains(footer, "/clean clean stale") {
		t.Fatalf("footer actions missing /clean hint: %q", footer)
	}
}

func TestStaleWorktreeCleanupShowsExternalGitActivity(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	project := model.ProjectSummary{
		Path:                 "/tmp/demo--external",
		Name:                 "demo--external",
		PresentOnDisk:        true,
		WorktreeRootPath:     "/tmp/demo",
		WorktreeKind:         model.WorktreeKindLinked,
		WorktreeParentBranch: "master",
		WorktreeMergeStatus:  model.WorktreeMergeStatusMerged,
		RepoBranch:           "feature/external",
		LastActivity:         now.Add(-48 * time.Hour),
	}
	m := Model{nowFn: func() time.Time { return now }, renderCachedSessionStateOnly: true}
	candidate, _, ok := m.staleWorktreeCleanupCandidate(project)
	if !ok || !candidate.NoRecordedSession {
		t.Fatalf("external candidate = %#v, %t", candidate, ok)
	}
	for _, text := range []string{
		m.projectDetailLastActivityText(project),
		ansi.Strip(m.projectDetailLastActivityRenderedText(project)),
	} {
		if !strings.Contains(text, "Git") || strings.Contains(strings.ToLower(text), "never") {
			t.Fatalf("last activity = %q, want Git date", text)
		}
	}
	m.staleWorktreeCleanup = &staleWorktreeCleanupDialogState{Loading: true}
	updated, _ := m.applyStaleWorktreeCleanupAudit(staleWorktreeCleanupAuditMsg{audit: service.StaleWorktreeCleanupAudit{
		AuditedAt: now, ScannedLinkedWorktrees: 1, Candidates: []service.StaleWorktreeCleanupCandidate{candidate},
	}})
	m = updated.(Model)
	if !m.staleWorktreeCleanup.Chosen[project.Path] {
		t.Fatal("external worktree should be selected for review")
	}
	rendered := ansi.Strip(m.renderStaleWorktreeCleanupOverlay("", 120, 40))
	for _, want := range []string{"[x] feature/external", "no recorded session", "age from Git activity"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("cleanup missing %q:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, "idle Codex") {
		t.Fatalf("cleanup invented an open engineer session:\n%s", rendered)
	}
	// External/sessionless candidates still obey the host's live-session guard.
	m.codexSnapshots = map[string]codexapp.Snapshot{project.Path: {
		Provider: codexapp.ProviderCodex, Started: true, Busy: true, Phase: codexapp.SessionPhaseRunning,
	}}
	if _, _, ok := m.staleWorktreeCleanupCandidate(project); ok {
		t.Fatal("external worktree with a busy engineer must not be eligible")
	}
}

func TestStaleWorktreeCleanupSessionBlockReasonCoversLiveProviderWork(t *testing.T) {
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name     string
		snapshot codexapp.Snapshot
		want     string
	}{
		{
			name: "active goal",
			snapshot: codexapp.Snapshot{
				Provider: codexapp.ProviderCodex,
				Started:  true,
				Phase:    codexapp.SessionPhaseIdle,
				Goal:     &codexapp.ThreadGoal{Status: codexapp.ThreadGoalStatusActive},
			},
			want: "active goal",
		},
		{
			name: "background work",
			snapshot: codexapp.Snapshot{
				Provider:        codexapp.ProviderClaudeCode,
				Started:         true,
				Phase:           codexapp.SessionPhaseIdle,
				BackgroundTasks: []codexapp.BackgroundTaskSnapshot{{ID: "task-1"}},
			},
			want: "background work",
		},
		{
			name: "browser wait",
			snapshot: codexapp.Snapshot{
				Provider:        codexapp.ProviderOpenCode,
				Started:         true,
				Phase:           codexapp.SessionPhaseIdle,
				BrowserActivity: browserctl.SessionActivity{State: browserctl.SessionActivityStateWaitingForUser},
			},
			want: "waiting for input",
		},
		{
			name: "old idle session",
			snapshot: codexapp.Snapshot{
				Provider:       codexapp.ProviderCodex,
				Started:        true,
				Phase:          codexapp.SessionPhaseIdle,
				LastActivityAt: now.Add(-48 * time.Hour),
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reason := staleWorktreeCleanupSessionBlockReason(test.snapshot, now)
			if test.want == "" && reason != "" {
				t.Fatalf("block reason = %q, want none", reason)
			}
			if test.want != "" && !strings.Contains(reason, test.want) {
				t.Fatalf("block reason = %q, want substring %q", reason, test.want)
			}
		})
	}
}

func TestStaleWorktreeCleanupContinuesAfterSkippedAndFailedItems(t *testing.T) {
	first := staleWorktreeCleanupTestCandidate("/tmp/demo--first", "feature/first", time.Now().Add(-48*time.Hour))
	second := staleWorktreeCleanupTestCandidate("/tmp/demo--second", "feature/second", time.Now().Add(-72*time.Hour))
	m := Model{
		staleWorktreeCleanup: &staleWorktreeCleanupDialogState{
			Removing: true,
			Queue:    []service.StaleWorktreeCleanupCandidate{first, second},
		},
	}

	updated, cmd := m.applyStaleWorktreeCleanupRemove(staleWorktreeCleanupRemoveMsg{result: staleWorktreeCleanupResult{
		Candidate:     first,
		SkippedReason: "a runtime became active",
	}})
	got := updated.(Model)
	if cmd == nil || !got.staleWorktreeCleanup.Removing || got.staleWorktreeCleanup.QueueIndex != 1 {
		t.Fatalf("first result did not continue batch: %#v cmd=%v", got.staleWorktreeCleanup, cmd)
	}

	updated, cmd = got.applyStaleWorktreeCleanupRemove(staleWorktreeCleanupRemoveMsg{result: staleWorktreeCleanupResult{
		Candidate: second,
		Err:       fmt.Errorf("remove failed"),
	}})
	got = updated.(Model)
	removed, skipped, failed := staleWorktreeCleanupResultCounts(got.staleWorktreeCleanup.Results)
	if got.staleWorktreeCleanup.Removing || !got.staleWorktreeCleanup.Finished || removed != 0 || skipped != 1 || failed != 1 {
		t.Fatalf("final batch state = %#v, counts=(%d,%d,%d)", got.staleWorktreeCleanup, removed, skipped, failed)
	}
	if cmd == nil || !strings.Contains(got.status, "0 removed, 1 skipped, 1 failed") {
		t.Fatalf("final command/status = %v / %q", cmd, got.status)
	}
	if severity := topStatusSeverityForMessage(got.status, got.err); severity != topStatusSeverityDanger {
		t.Fatalf("failed cleanup top status severity = %v, want danger", severity)
	}
}

func TestStaleWorktreeCleanupSuccessfulRemovalUsesSteadyGreenStatus(t *testing.T) {
	withANSI256DarkBackground(t)

	candidate := staleWorktreeCleanupTestCandidate("/tmp/demo--removed", "feature/removed", time.Now().Add(-48*time.Hour))
	m := Model{
		staleWorktreeCleanup: &staleWorktreeCleanupDialogState{
			Removing: true,
			Queue:    []service.StaleWorktreeCleanupCandidate{candidate},
		},
	}

	updated, cmd := m.applyStaleWorktreeCleanupRemove(staleWorktreeCleanupRemoveMsg{result: staleWorktreeCleanupResult{
		Candidate: candidate,
		Finalize: service.FinalizeMergedWorktreeResult{
			WorktreeRemoved: true,
		},
	}})
	got := updated.(Model)
	if cmd == nil || got.status != "Stale worktree cleanup finished successfully: 1 removed, 0 skipped" {
		t.Fatalf("final command/status = %v / %q", cmd, got.status)
	}
	if severity := topStatusSeverityForMessage(got.status, got.err); severity != topStatusSeveritySuccess {
		t.Fatalf("top status severity = %v, want success", severity)
	}

	statusA := got.renderTopStatusMessage(got.status, got.status)
	got.spinnerFrame = 1
	statusB := got.renderTopStatusMessage(got.status, got.status)
	if statusA != statusB {
		t.Fatalf("successful cleanup status should not flash, got %q vs %q", statusA, statusB)
	}
	if want := topStatusSuccessBadgeStyle.Render(got.status); statusA != want {
		t.Fatalf("successful cleanup status did not use green success style: got %q, want %q", statusA, want)
	}
}

func TestStaleWorktreeCleanupRechecksLiveStateAfterGitRevalidation(t *testing.T) {
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	candidate := staleWorktreeCleanupTestCandidate("/tmp/demo--became-active", "feature/became-active", now.Add(-48*time.Hour))
	m := Model{
		nowFn: func() time.Time { return now },
		runtimeSnapshots: map[string]projectrun.Snapshot{
			candidate.ProjectPath: {
				ProjectPath: candidate.ProjectPath,
				External:    true,
				Running:     true,
			},
		},
		staleWorktreeCleanup: &staleWorktreeCleanupDialogState{
			Removing: true,
			Queue:    []service.StaleWorktreeCleanupCandidate{candidate},
		},
	}

	updated, cmd := m.applyStaleWorktreeCleanupRevalidate(staleWorktreeCleanupRevalidateMsg{candidate: candidate})
	got := updated.(Model)
	if !got.staleWorktreeCleanup.Finished || got.staleWorktreeCleanup.Removing {
		t.Fatalf("cleanup did not finish after current live-state skip: %#v", got.staleWorktreeCleanup)
	}
	if len(got.staleWorktreeCleanup.Results) != 1 || !strings.Contains(got.staleWorktreeCleanup.Results[0].SkippedReason, "external") {
		t.Fatalf("cleanup result = %#v, want external-runtime skip", got.staleWorktreeCleanup.Results)
	}
	if got.staleWorktreeCleanup.Results[0].Finalize.WorktreeRemoved {
		t.Fatal("worktree was removed after a session became active")
	}
	if cmd == nil {
		t.Fatal("finished cleanup should still invalidate the project list")
	}
}

func staleWorktreeCleanupTestCandidate(path, branch string, lastActivity time.Time) service.StaleWorktreeCleanupCandidate {
	return service.StaleWorktreeCleanupCandidate{
		ProjectPath:     path,
		ProjectName:     filepath.Base(path),
		RootProjectPath: "/tmp/demo",
		Branch:          branch,
		ParentBranch:    "master",
		LastActivity:    lastActivity,
	}
}

func staleWorktreeCleanupTestSummary(now time.Time) model.ProjectSummary {
	lastActivity := now.Add(-48 * time.Hour)
	return model.ProjectSummary{
		Path:                            "/tmp/demo--stale",
		Name:                            "demo--stale",
		Status:                          model.StatusIdle,
		PresentOnDisk:                   true,
		WorktreeRootPath:                "/tmp/demo",
		WorktreeKind:                    model.WorktreeKindLinked,
		WorktreeParentBranch:            "master",
		WorktreeMergeStatus:             model.WorktreeMergeStatusMerged,
		RepoBranch:                      "feature/stale",
		LastActivity:                    lastActivity,
		LatestSessionFormat:             "modern",
		LatestSessionLastEventAt:        lastActivity,
		LatestTurnStateKnown:            true,
		LatestTurnCompleted:             true,
		LatestSessionClassification:     model.ClassificationCompleted,
		LatestSessionClassificationType: model.SessionCategoryCompleted,
		LatestSessionSummary:            "Work is complete.",
	}
}

func TestRenderStaleWorktreeCleanupResultsWrapsLongDetails(t *testing.T) {
	longError := "linked TODO was completed, but removing the worktree failed: symlink /tmp/demo--stale/.git: file exists and cannot be replaced safely"
	dialog := &staleWorktreeCleanupDialogState{Results: []staleWorktreeCleanupResult{
		{Candidate: staleWorktreeCleanupTestCandidate("/tmp/demo--stale", "feature/stale", time.Now().Add(-48*time.Hour)), Err: fmt.Errorf("%s", longError)},
		{Candidate: staleWorktreeCleanupTestCandidate("/tmp/demo--recent", "ui/recent", time.Now()), SkippedReason: "the embedded Codex session was used within the last 24 hours"},
	}}

	rendered := ansi.Strip(renderStaleWorktreeCleanupResults(dialog, 100, 40))
	if strings.Contains(rendered, "…") {
		t.Fatalf("expected no truncation in report, got:\n%s", rendered)
	}
	joined := strings.Join(strings.Fields(rendered), " ")
	if !strings.Contains(joined, longError) {
		t.Fatalf("expected full failure detail in report, got:\n%s", rendered)
	}
	if !strings.Contains(joined, "skipped: the embedded Codex session was used within the last 24 hours") {
		t.Fatalf("expected full skip reason in report, got:\n%s", rendered)
	}
	for _, line := range strings.Split(rendered, "\n") {
		if width := ansi.StringWidth(line); width > 100 {
			t.Fatalf("line exceeds dialog width (%d): %q", width, line)
		}
	}
}
