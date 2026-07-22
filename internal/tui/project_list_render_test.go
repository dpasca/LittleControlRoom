package tui

import (
	"context"
	"fmt"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"lcroom/internal/codexapp"
	"lcroom/internal/codexcli"
	"lcroom/internal/commands"
	"lcroom/internal/config"
	"lcroom/internal/events"
	"lcroom/internal/model"
	"lcroom/internal/procinspect"
	"lcroom/internal/projectrun"
	"lcroom/internal/service"
	"lcroom/internal/store"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSessionCategoryLabel(t *testing.T) {
	if got := sessionCategoryLabel(model.SessionCategoryNeedsFollowUp); got != "followup" {
		t.Fatalf("sessionCategoryLabel(needs_follow_up) = %q, want %q", got, "followup")
	}
	if got := sessionCategoryLabel(model.SessionCategoryWaitingForUser); got != "waiting" {
		t.Fatalf("sessionCategoryLabel(waiting_for_user) = %q, want %q", got, "waiting")
	}
}

func TestShowCodexProjectMarksSessionSeenLocally(t *testing.T) {
	seenAt := time.Date(2026, 4, 3, 11, 0, 0, 0, time.UTC)
	project := model.ProjectSummary{
		Path:                            "/tmp/demo",
		LatestSessionClassification:     model.ClassificationCompleted,
		LatestSessionClassificationType: model.SessionCategoryCompleted,
		LatestSessionFormat:             "modern",
		LatestSessionLastEventAt:        seenAt.Add(-5 * time.Minute),
		LatestTurnStateKnown:            true,
		LatestTurnCompleted:             true,
	}

	updatedModel, _ := (Model{
		nowFn:       func() time.Time { return seenAt },
		allProjects: []model.ProjectSummary{project},
		projects:    []model.ProjectSummary{project},
		detail:      model.ProjectDetail{Summary: project},
		codexInput:  newCodexTextarea(),
	}).showCodexProject(project.Path, "")

	got := updatedModel.(Model)
	if !got.allProjects[0].LastSessionSeenAt.Equal(seenAt) {
		t.Fatalf("allProjects seen_at = %v, want %v", got.allProjects[0].LastSessionSeenAt, seenAt)
	}
	if !got.projects[0].LastSessionSeenAt.Equal(seenAt) {
		t.Fatalf("projects seen_at = %v, want %v", got.projects[0].LastSessionSeenAt, seenAt)
	}
	if !got.detail.Summary.LastSessionSeenAt.Equal(seenAt) {
		t.Fatalf("detail seen_at = %v, want %v", got.detail.Summary.LastSessionSeenAt, seenAt)
	}
}

func TestPruneCodexSessionVisibilityKeepsClosedPlaceholderVisible(t *testing.T) {
	m := Model{
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          newCodexTextarea(),
		codexSnapshots: map[string]codexapp.Snapshot{
			"/tmp/demo": {
				Provider:    codexapp.ProviderOpenCode,
				ProjectPath: "/tmp/demo",
				Closed:      true,
				ThreadID:    "thread-demo",
				Status:      "OpenCode session closed",
			},
		},
	}

	m.pruneCodexSessionVisibility()

	if m.codexVisibleProject != "/tmp/demo" {
		t.Fatalf("codexVisibleProject = %q, want /tmp/demo", m.codexVisibleProject)
	}
	if m.codexHiddenProject != "/tmp/demo" {
		t.Fatalf("codexHiddenProject = %q, want /tmp/demo", m.codexHiddenProject)
	}
	snapshot, ok := m.currentCodexSnapshot()
	if !ok {
		t.Fatalf("currentCodexSnapshot() unavailable after pruning a closed placeholder")
	}
	if !snapshot.Closed {
		t.Fatalf("snapshot.Closed = false, want true")
	}
}

func TestCodexSessionOpenedMarksSessionSeenLocally(t *testing.T) {
	seenAt := time.Date(2026, 4, 4, 9, 0, 0, 0, time.UTC)
	project := model.ProjectSummary{
		Path:                            "/tmp/demo",
		LatestSessionClassification:     model.ClassificationCompleted,
		LatestSessionClassificationType: model.SessionCategoryCompleted,
		LatestSessionFormat:             "modern",
		LatestSessionLastEventAt:        seenAt.Add(-5 * time.Minute),
		LatestTurnStateKnown:            true,
		LatestTurnCompleted:             true,
	}
	session := &fakeCodexSession{
		projectPath: project.Path,
		snapshot: codexapp.Snapshot{
			Provider:    codexapp.ProviderOpenCode,
			ProjectPath: project.Path,
			ThreadID:    "ses-opened",
			Started:     true,
			Status:      "OpenCode session ready",
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		Provider:    codexapp.ProviderOpenCode,
		ProjectPath: project.Path,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	updatedModel, _ := (Model{
		nowFn:        func() time.Time { return seenAt },
		allProjects:  []model.ProjectSummary{project},
		projects:     []model.ProjectSummary{project},
		detail:       model.ProjectDetail{Summary: project},
		codexInput:   newCodexTextarea(),
		codexManager: manager,
		codexPendingOpen: &codexPendingOpenState{
			projectPath: project.Path,
			provider:    codexapp.ProviderOpenCode,
		},
		codexViewport: viewport.New(0, 0),
		width:         100,
		height:        24,
	}).update(codexSessionOpenedMsg{
		projectPath: project.Path,
		status:      "OpenCode session ready",
	})

	got := updatedModel.(Model)
	if !got.allProjects[0].LastSessionSeenAt.Equal(seenAt) {
		t.Fatalf("allProjects seen_at = %v, want %v", got.allProjects[0].LastSessionSeenAt, seenAt)
	}
	if !got.projects[0].LastSessionSeenAt.Equal(seenAt) {
		t.Fatalf("projects seen_at = %v, want %v", got.projects[0].LastSessionSeenAt, seenAt)
	}
	if !got.detail.Summary.LastSessionSeenAt.Equal(seenAt) {
		t.Fatalf("detail seen_at = %v, want %v", got.detail.Summary.LastSessionSeenAt, seenAt)
	}
}

func TestClassificationCategoryStyle(t *testing.T) {
	if got := fmt.Sprint(classificationCategoryStyle(model.SessionCategoryCompleted).GetForeground()); got != "42" {
		t.Fatalf("completed foreground = %q, want %q", got, "42")
	}
	if got := fmt.Sprint(classificationCategoryStyle(model.SessionCategoryNeedsFollowUp).GetForeground()); got != "81" {
		t.Fatalf("needs_follow_up foreground = %q, want %q", got, "81")
	}
	if got := fmt.Sprint(classificationCategoryStyle(model.SessionCategoryBlocked).GetForeground()); got != "203" {
		t.Fatalf("blocked foreground = %q, want %q", got, "203")
	}
}

func TestProjectAssessmentTextUsesLatestSummary(t *testing.T) {
	project := model.ProjectSummary{
		LatestSessionClassification:     model.ClassificationCompleted,
		LatestSessionClassificationType: model.SessionCategoryWaitingForUser,
		LatestSessionSummary:            "Waiting on a design decision before coding resumes.",
	}

	if got := projectAssessmentText(project); got != project.LatestSessionSummary {
		t.Fatalf("projectAssessmentText() = %q, want %q", got, project.LatestSessionSummary)
	}
}

func TestProjectAssessmentDisplayTextPrefersPendingGitSummary(t *testing.T) {
	project := model.ProjectSummary{
		Path:                            "/tmp/demo",
		LatestSessionClassification:     model.ClassificationCompleted,
		LatestSessionClassificationType: model.SessionCategoryCompleted,
		LatestSessionSummary:            "Work appears complete for now.",
	}
	m := Model{
		pendingGitSummaries: map[string]string{
			project.Path: "Committing...",
		},
	}

	if got := m.projectAssessmentDisplayTextAt(project, time.Time{}, 0); got != "Committing..." {
		t.Fatalf("projectAssessmentDisplayTextAt() = %q, want %q", got, "Committing...")
	}
}

func TestRepoCombinedDetailValuePrefersPendingGitOperation(t *testing.T) {
	project := model.ProjectSummary{
		Path:           "/tmp/demo",
		RepoDirty:      true,
		RepoBranch:     "master",
		RepoSyncStatus: model.RepoSyncAhead,
		RepoAheadCount: 2,
	}
	m := Model{
		pendingGitSummaries: map[string]string{
			project.Path: "Committing...",
		},
	}

	rendered := ansi.Strip(m.repoCombinedDetailValue(project))
	if !strings.Contains(rendered, "Committing...") {
		t.Fatalf("repoCombinedDetailValue() = %q, want pending git label", rendered)
	}
	if strings.Contains(rendered, "dirty") {
		t.Fatalf("repoCombinedDetailValue() should hide dirty while commit is pending: %q", rendered)
	}
	if strings.Contains(rendered, "ahead 2") {
		t.Fatalf("repoCombinedDetailValue() should hide stale remote sync while git op is pending: %q", rendered)
	}
}

func TestRepoCombinedDetailValueShowsSubmoduleAttention(t *testing.T) {
	project := model.ProjectSummary{
		Path:                       "/tmp/demo",
		RepoBranch:                 "master",
		RepoSubmoduleDirtyCount:    1,
		RepoSubmoduleUnpushedCount: 2,
	}

	rendered := ansi.Strip((Model{}).repoCombinedDetailValue(project))
	if !strings.Contains(rendered, "clean") {
		t.Fatalf("repoCombinedDetailValue() = %q, want clean parent repo state", rendered)
	}
	if !strings.Contains(rendered, "submodules 1 dirty, 2 unpushed") {
		t.Fatalf("repoCombinedDetailValue() = %q, want submodule attention summary", rendered)
	}
}

func TestProjectAssessmentTextAtUsesDerivedStalledSummary(t *testing.T) {
	project := model.ProjectSummary{
		LatestSessionClassification:     model.ClassificationCompleted,
		LatestSessionClassificationType: model.SessionCategoryInProgress,
		LatestSessionSummary:            "Checking the latest tool outputs.",
		LatestSessionLastEventAt:        time.Date(2026, 3, 29, 6, 54, 44, 0, time.UTC),
		LatestTurnStateKnown:            true,
		LatestTurnCompleted:             false,
	}

	got := projectAssessmentTextAt(project, time.Date(2026, 3, 29, 8, 0, 0, 0, time.UTC), 30*time.Minute)
	if !strings.Contains(got, "likely stalled or disconnected") {
		t.Fatalf("projectAssessmentTextAt() = %q, want derived stalled/disconnected summary", got)
	}
}

func TestProjectAssessmentTextUsesFallbackStates(t *testing.T) {
	project := model.ProjectSummary{
		LatestSessionClassification: model.ClassificationRunning,
	}
	if got := projectAssessmentText(project); got != "assessment running" {
		t.Fatalf("projectAssessmentText(running) = %q, want %q", got, "assessment running")
	}

	project = model.ProjectSummary{
		LatestSessionClassification:               model.ClassificationRunning,
		LatestSessionClassificationStage:          model.ClassificationStageWaitingForModel,
		LatestSessionClassificationStageStartedAt: time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC),
		LatestCompletedSessionClassificationType:  model.SessionCategoryNeedsFollowUp,
		LatestCompletedSessionSummary:             "A concrete next step still remains.",
	}
	if got := projectAssessmentTextAt(project, time.Date(2026, 3, 10, 12, 0, 37, 0, time.UTC), 0); got != "assessment waiting for model 00:37" {
		t.Fatalf("projectAssessmentTextAt(refreshing) = %q, want assessment progress", got)
	}

	project = model.ProjectSummary{
		PresentOnDisk:                            true,
		LatestSessionClassification:              model.ClassificationFailed,
		LatestCompletedSessionClassificationType: model.SessionCategoryNeedsFollowUp,
		LatestCompletedSessionSummary:            "A concrete next step still remains.",
	}
	if got := projectAssessmentText(project); got != "assessment failed" {
		t.Fatalf("projectAssessmentText(failed) = %q, want assessment failed", got)
	}
	if got := projectListStatus(project); got != "failed" {
		t.Fatalf("projectListStatus(failed) = %q, want failed", got)
	}

	project = model.ProjectSummary{
		LatestSessionFormat: "modern",
	}
	if got := projectAssessmentText(project); got != "not assessed yet" {
		t.Fatalf("projectAssessmentText(unassessed) = %q, want %q", got, "not assessed yet")
	}

	project = model.ProjectSummary{
		LatestSessionFormat: "modern",
		PresentOnDisk:       true,
		RepoDirty:           true,
	}
	if got := projectAssessmentText(project); got != "dirty worktree" {
		t.Fatalf("projectAssessmentText(unassessed dirty repo) = %q, want %q", got, "dirty worktree")
	}
}

func TestProjectAssessmentTextPrefersCurrentSummaryDuringRefresh(t *testing.T) {
	project := model.ProjectSummary{
		PresentOnDisk:                            true,
		LatestSessionClassification:              model.ClassificationRunning,
		LatestSessionClassificationStage:         model.ClassificationStageWaitingForModel,
		LatestSessionClassificationType:          model.SessionCategoryNeedsFollowUp,
		LatestSessionSummary:                     "Current session summary.",
		LatestCompletedSessionClassificationType: model.SessionCategoryCompleted,
		LatestCompletedSessionSummary:            "Older session summary.",
	}
	if got := projectAssessmentText(project); got != "Current session summary." {
		t.Fatalf("projectAssessmentText(refreshing with current summary) = %q, want current session summary", got)
	}
	if got := projectListStatus(project); got != "followup" {
		t.Fatalf("projectListStatus(refreshing with current summary) = %q, want current assessment label", got)
	}
}

func TestProjectListStatusShowsProgressWhileRefreshRunsWithoutCurrentSummary(t *testing.T) {
	project := model.ProjectSummary{
		Status:                                   model.StatusIdle,
		PresentOnDisk:                            true,
		LatestSessionFormat:                      "modern",
		LatestSessionClassification:              model.ClassificationRunning,
		LatestSessionClassificationStage:         model.ClassificationStageWaitingForModel,
		LatestCompletedSessionClassificationType: model.SessionCategoryWaitingForUser,
		LatestCompletedSessionSummary:            "Waiting on a design decision before coding resumes.",
	}

	if got := projectListStatus(project); got != "model" {
		t.Fatalf("projectListStatus(refreshing) = %q, want %q", got, "model")
	}
	if got := projectAssessmentText(project); got != "assessment waiting for model" {
		t.Fatalf("projectAssessmentText(refreshing) = %q, want assessment progress", got)
	}
}

func TestProjectSummaryMsgKeepsLatestAssessmentDisplayDuringRefresh(t *testing.T) {
	previous := model.ProjectSummary{
		Path:                            "/tmp/demo",
		Name:                            "demo",
		PresentOnDisk:                   true,
		InScope:                         true,
		LatestSessionID:                 "codex:ses_current",
		LatestSessionFormat:             "modern",
		LatestSessionClassification:     model.ClassificationCompleted,
		LatestSessionClassificationType: model.SessionCategoryWaitingForUser,
		LatestSessionSummary:            "Current session is waiting on review.",
	}
	refreshing := model.ProjectSummary{
		Path:                                     previous.Path,
		Name:                                     previous.Name,
		PresentOnDisk:                            true,
		InScope:                                  true,
		LatestSessionID:                          previous.LatestSessionID,
		LatestSessionFormat:                      previous.LatestSessionFormat,
		LatestSessionClassification:              model.ClassificationRunning,
		LatestSessionClassificationStage:         model.ClassificationStageWaitingForModel,
		LatestCompletedSessionClassificationType: model.SessionCategoryNeedsFollowUp,
		LatestCompletedSessionSummary:            "Older session needs follow-up.",
	}
	m := Model{
		allProjects: []model.ProjectSummary{previous},
		projects:    []model.ProjectSummary{previous},
		sortMode:    sortByAttention,
		visibility:  visibilityAllFolders,
	}

	updated, _ := m.Update(projectSummaryMsg{path: previous.Path, summary: refreshing, found: true})
	got := updated.(Model)
	project, ok := got.selectedProject()
	if !ok {
		t.Fatal("expected selected project after summary update")
	}
	if project.LatestSessionSummary != previous.LatestSessionSummary {
		t.Fatalf("latest summary = %q, want preserved current summary", project.LatestSessionSummary)
	}
	if project.LatestSessionClassificationType != previous.LatestSessionClassificationType {
		t.Fatalf("latest assessment type = %s, want preserved current assessment type", project.LatestSessionClassificationType)
	}
	if gotText := projectAssessmentText(project); gotText != previous.LatestSessionSummary {
		t.Fatalf("projectAssessmentText() = %q, want preserved current summary", gotText)
	}
	if gotStatus := projectListStatus(project); gotStatus != "waiting" {
		t.Fatalf("projectListStatus() = %q, want preserved current assessment label", gotStatus)
	}

	refreshing.LatestSessionClassificationStage = model.ClassificationStagePreparingSnapshot
	updatedAgain, _ := got.Update(projectSummaryMsg{path: previous.Path, summary: refreshing, found: true})
	gotAgain := updatedAgain.(Model)
	projectAgain, ok := gotAgain.selectedProject()
	if !ok {
		t.Fatal("expected selected project after repeated summary update")
	}
	if projectAgain.LatestSessionSummary != previous.LatestSessionSummary {
		t.Fatalf("repeated latest summary = %q, want preserved current summary", projectAgain.LatestSessionSummary)
	}
}

func TestProjectListStatusAtShowsBlockedForStaleInProgressTurn(t *testing.T) {
	project := model.ProjectSummary{
		Status:                           model.StatusPossiblyStuck,
		PresentOnDisk:                    true,
		LatestSessionClassification:      model.ClassificationCompleted,
		LatestSessionClassificationType:  model.SessionCategoryInProgress,
		LatestSessionFormat:              "modern",
		LatestSessionLastEventAt:         time.Date(2026, 3, 29, 6, 54, 44, 0, time.UTC),
		LatestTurnStateKnown:             true,
		LatestTurnCompleted:              false,
		LatestSessionDetectedProjectPath: "/tmp/demo",
	}

	if got := projectListStatusAt(project, time.Date(2026, 3, 29, 8, 0, 0, 0, time.UTC), 30*time.Minute); got != "blocked" {
		t.Fatalf("projectListStatusAt() = %q, want %q", got, "blocked")
	}
}

func TestRenderProjectListIncludesAssessmentColumn(t *testing.T) {
	m := Model{
		projects: []model.ProjectSummary{{
			Name:                             "demo",
			Path:                             "/tmp/demo",
			Status:                           model.StatusIdle,
			PresentOnDisk:                    true,
			LatestSessionClassification:      model.ClassificationCompleted,
			LatestSessionClassificationType:  model.SessionCategoryNeedsFollowUp,
			LatestSessionSummary:             "A concrete next step still remains.",
			LatestSessionFormat:              "modern",
			LatestSessionDetectedProjectPath: "/tmp/demo",
		}},
		sortMode:   sortByAttention,
		visibility: visibilityAIFolders,
	}

	rendered := m.renderProjectList(120, 8)
	if !strings.Contains(rendered, "AGENT") || !strings.Contains(rendered, "RUN") {
		t.Fatalf("renderProjectList() missing agent/run headers: %q", rendered)
	}
	if !strings.Contains(rendered, "ASSESS") || !strings.Contains(rendered, "SUMMARY") {
		t.Fatalf("renderProjectList() missing assessment/summary headers: %q", rendered)
	}
	if !strings.Contains(rendered, "A concrete next step still remains.") {
		t.Fatalf("renderProjectList() missing latest assessment summary: %q", rendered)
	}
}

func TestRenderProjectListPrefersPendingGitSummary(t *testing.T) {
	m := Model{
		projects: []model.ProjectSummary{{
			Name:                             "demo",
			Path:                             "/tmp/demo",
			Status:                           model.StatusIdle,
			PresentOnDisk:                    true,
			LatestSessionClassification:      model.ClassificationCompleted,
			LatestSessionClassificationType:  model.SessionCategoryCompleted,
			LatestSessionSummary:             "Work appears complete for now.",
			LatestSessionFormat:              "modern",
			LatestSessionDetectedProjectPath: "/tmp/demo",
		}},
		pendingGitSummaries: map[string]string{
			"/tmp/demo": "Committing...",
		},
		sortMode:   sortByAttention,
		visibility: visibilityAIFolders,
	}

	rendered := ansi.Strip(m.renderProjectList(120, 8))
	if !strings.Contains(rendered, "Committing...") {
		t.Fatalf("renderProjectList() missing pending git summary: %q", rendered)
	}
	if strings.Contains(rendered, "Work appears complete for now.") {
		t.Fatalf("renderProjectList() should hide the stored summary while commit is pending: %q", rendered)
	}
}

func TestProjectAgentDisplayUsesLiveBusyTimer(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Provider:  codexapp.ProviderCodex,
			Started:   true,
			Busy:      true,
			BusySince: time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC),
			ThreadID:  "thread-live",
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Preset:      codexcli.PresetYolo,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	m := Model{codexManager: manager}
	project := model.ProjectSummary{
		Path:                "/tmp/demo",
		PresentOnDisk:       true,
		LatestSessionFormat: "modern",
	}

	label, tag, live := m.projectAgentDisplay(project, time.Date(2026, 3, 9, 12, 0, 37, 0, time.UTC))
	if !live {
		t.Fatalf("projectAgentDisplay() live = false, want true")
	}
	if tag != "CX" {
		t.Fatalf("projectAgentDisplay() tag = %q, want %q", tag, "CX")
	}
	if label != "CX 00:37" {
		t.Fatalf("projectAgentDisplay() label = %q, want %q", label, "CX 00:37")
	}
}

func TestCompletedTurnStateSuppressesStaleLiveTimer(t *testing.T) {
	startedAt := time.Date(2026, 7, 22, 13, 56, 31, 14_000_000, time.UTC)
	snapshot := codexapp.Snapshot{
		Busy:                 true,
		BusyExternal:         true,
		ActiveTurnID:         "turn-completed",
		BusySince:            startedAt.Add(30 * time.Minute),
		LatestTurnStartedAt:  startedAt,
		LatestTurnStateKnown: true,
		LatestTurnCompleted:  true,
		Phase:                codexapp.SessionPhaseExternal,
	}

	if gotStartedAt, active := embeddedSnapshotActiveStartedAt(snapshot, model.ProjectSummary{}); active || !gotStartedAt.IsZero() {
		t.Fatalf("completed snapshot reported active timer: active=%t started=%v", active, gotStartedAt)
	}
}

func TestProjectAgentDisplayUsesConflictResolverTimer(t *testing.T) {
	projectPath := "/tmp/demo"
	startedAt := time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC)
	m := Model{
		mergeConflictResolvers: map[string]mergeConflictResolverState{
			projectPath: {
				OwnerProjectPath:   projectPath,
				SessionProjectPath: projectPath,
				Provider:           codexapp.ProviderCodex,
				Phase:              mergeConflictResolverRunning,
				StartedAt:          startedAt,
			},
		},
	}
	project := model.ProjectSummary{
		Path:          projectPath,
		PresentOnDisk: true,
	}

	label, tag, live := m.projectAgentDisplay(project, startedAt.Add(37*time.Second))
	if !live {
		t.Fatalf("projectAgentDisplay() live = false, want true")
	}
	if tag != "CX" {
		t.Fatalf("projectAgentDisplay() tag = %q, want %q", tag, "CX")
	}
	if label != "CX 00:37" {
		t.Fatalf("projectAgentDisplay() label = %q, want %q", label, "CX 00:37")
	}
}

func TestProjectAgentDisplayUsesLiveActiveTurnTimer(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Provider:     codexapp.ProviderOpenCode,
			Started:      true,
			ActiveTurnID: "turn-live",
			BusySince:    time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC),
			ThreadID:     "thread-live",
			Goal: &codexapp.ThreadGoal{
				Objective: "Wiring active row summaries into the project list.",
				Status:    codexapp.ThreadGoalStatusActive,
			},
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		Provider:    codexapp.ProviderOpenCode,
		ProjectPath: "/tmp/demo",
		Preset:      codexcli.PresetYolo,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	m := Model{codexManager: manager}
	project := model.ProjectSummary{
		Path:                "/tmp/demo",
		PresentOnDisk:       true,
		LatestSessionFormat: "opencode_db",
	}

	label, tag, live := m.projectAgentDisplay(project, time.Date(2026, 3, 9, 12, 0, 37, 0, time.UTC))
	if !live {
		t.Fatalf("projectAgentDisplay() live = false, want true")
	}
	if tag != "OC" {
		t.Fatalf("projectAgentDisplay() tag = %q, want %q", tag, "OC")
	}
	if label != "OC 00:37" {
		t.Fatalf("projectAgentDisplay() label = %q, want %q", label, "OC 00:37")
	}
}

func TestProjectAgentDisplayUsesLiveLCAgentTimer(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Provider:     codexapp.ProviderLCAgent,
			Started:      true,
			ActiveTurnID: "turn-live",
			BusySince:    time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC),
			ThreadID:     "lca-live",
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		Provider:    codexapp.ProviderLCAgent,
		ProjectPath: "/tmp/demo",
		Preset:      codexcli.PresetYolo,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	m := Model{codexManager: manager}
	project := model.ProjectSummary{
		Path:                "/tmp/demo",
		PresentOnDisk:       true,
		LatestSessionFormat: "lcagent_jsonl",
	}

	label, tag, live := m.projectAgentDisplay(project, time.Date(2026, 3, 9, 12, 0, 37, 0, time.UTC))
	if !live {
		t.Fatalf("projectAgentDisplay() live = false, want true")
	}
	if tag != "LA" {
		t.Fatalf("projectAgentDisplay() tag = %q, want %q", tag, "LA")
	}
	if label != "LA 00:37" {
		t.Fatalf("projectAgentDisplay() label = %q, want %q", label, "LA 00:37")
	}
}

func TestRenderProjectListShowsWorkingAssessmentForLiveEmbeddedTurn(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Provider:     codexapp.ProviderOpenCode,
			Started:      true,
			ActiveTurnID: "turn-live",
			BusySince:    time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC),
			ThreadID:     "thread-live",
			Goal: &codexapp.ThreadGoal{
				Objective: "Wiring active row summaries into the project list.",
				Status:    codexapp.ThreadGoalStatusActive,
			},
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		Provider:    codexapp.ProviderOpenCode,
		ProjectPath: "/tmp/demo",
		Preset:      codexcli.PresetYolo,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	project := model.ProjectSummary{
		Name:                            "demo",
		Path:                            "/tmp/demo",
		Status:                          model.StatusIdle,
		PresentOnDisk:                   true,
		LatestSessionFormat:             "opencode_db",
		LatestSessionClassification:     model.ClassificationCompleted,
		LatestSessionClassificationType: model.SessionCategoryCompleted,
		LatestSessionSummary:            "Previous turn looked done.",
	}
	m := Model{
		projects:     []model.ProjectSummary{project},
		codexManager: manager,
		nowFn: func() time.Time {
			return time.Date(2026, 3, 9, 12, 0, 37, 0, time.UTC)
		},
	}

	rendered := ansi.Strip(m.renderProjectList(120, 4))
	if !strings.Contains(rendered, "working") || !strings.Contains(rendered, "Wiring active row summaries") {
		t.Fatalf("renderProjectList() should show active embedded work in assessment columns: %q", rendered)
	}
	if strings.Contains(rendered, "OpenCode:") {
		t.Fatalf("renderProjectList() should keep provider labels out of active summaries: %q", rendered)
	}
	if strings.Contains(rendered, "done") || strings.Contains(rendered, "Previous turn looked done.") {
		t.Fatalf("renderProjectList() should not show stale completed assessment for active embedded work: %q", rendered)
	}
}

func TestProjectAgentDisplayShowsStalledLiveSession(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Provider: codexapp.ProviderCodex,
			Started:  true,
			Busy:     true,
			Phase:    codexapp.SessionPhaseStalled,
			Status:   "Embedded Codex session seems stuck or disconnected. Use /reconnect.",
			ThreadID: "thread-live",
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Preset:      codexcli.PresetYolo,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	m := Model{codexManager: manager}
	project := model.ProjectSummary{
		Path:                "/tmp/demo",
		PresentOnDisk:       true,
		LatestSessionFormat: "modern",
	}

	label, tag, live := m.projectAgentDisplay(project, time.Date(2026, 3, 9, 12, 0, 37, 0, time.UTC))
	if !live {
		t.Fatalf("projectAgentDisplay() live = false, want true")
	}
	if tag != "CX" {
		t.Fatalf("projectAgentDisplay() tag = %q, want %q", tag, "CX")
	}
	if label != "CX stalled" {
		t.Fatalf("projectAgentDisplay() label = %q, want %q", label, "CX stalled")
	}
}

func TestProjectAgentDisplayHidesPersistedTurnTimerBeforeStartupScan(t *testing.T) {
	m := Model{}
	project := model.ProjectSummary{
		Path:                 "/tmp/demo",
		PresentOnDisk:        true,
		LatestSessionFormat:  "claude_code",
		LatestTurnStateKnown: true,
		LatestTurnCompleted:  false,
		LatestTurnStartedAt:  time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC),
	}

	label, tag, live := m.projectAgentDisplay(project, time.Date(2026, 3, 9, 12, 5, 0, 0, time.UTC))
	if live {
		t.Fatalf("projectAgentDisplay() live = true, want false before startup scan")
	}
	if tag != "CC" {
		t.Fatalf("projectAgentDisplay() tag = %q, want %q", tag, "CC")
	}
	if label != "CC" {
		t.Fatalf("projectAgentDisplay() label = %q, want %q", label, "CC")
	}
}

func TestProjectAgentDisplayShowsPersistedTurnTimerAfterStartupScan(t *testing.T) {
	m := Model{startupScanCompleted: true}
	project := model.ProjectSummary{
		Path:                 "/tmp/demo",
		PresentOnDisk:        true,
		LatestSessionFormat:  "claude_code",
		LatestTurnStateKnown: true,
		LatestTurnCompleted:  false,
		LatestTurnStartedAt:  time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC),
	}

	label, tag, live := m.projectAgentDisplay(project, time.Date(2026, 3, 9, 12, 5, 0, 0, time.UTC))
	if !live {
		t.Fatalf("projectAgentDisplay() live = false, want true after startup scan")
	}
	if tag != "CC" {
		t.Fatalf("projectAgentDisplay() tag = %q, want %q", tag, "CC")
	}
	if label != "CC 05:00" {
		t.Fatalf("projectAgentDisplay() label = %q, want %q", label, "CC 05:00")
	}
}

func TestProjectAgentDisplayHidesStaleUnfinishedTurnTimer(t *testing.T) {
	m := Model{startupScanCompleted: true}
	now := time.Date(2026, 3, 9, 12, 0, 37, 0, time.UTC)
	project := model.ProjectSummary{
		Path:                     "/tmp/demo",
		PresentOnDisk:            true,
		LatestSessionFormat:      "modern",
		LatestSessionLastEventAt: now.Add(-95 * time.Minute),
		LatestTurnStartedAt:      now.Add(-96 * time.Minute),
		LatestTurnStateKnown:     true,
		LatestTurnCompleted:      false,
	}

	label, tag, live := m.projectAgentDisplay(project, now)
	if live {
		t.Fatalf("projectAgentDisplay() live = true, want false")
	}
	if tag != "CX" {
		t.Fatalf("projectAgentDisplay() tag = %q, want %q", tag, "CX")
	}
	if label != "CX" {
		t.Fatalf("projectAgentDisplay() label = %q, want %q", label, "CX")
	}
}

func TestProjectAgentDisplayHidesWaitingForUserTurnTimer(t *testing.T) {
	m := Model{startupScanCompleted: true}
	now := time.Date(2026, 3, 9, 12, 0, 37, 0, time.UTC)
	project := model.ProjectSummary{
		Path:                            "/tmp/demo",
		PresentOnDisk:                   true,
		LatestSessionFormat:             "modern",
		LatestSessionLastEventAt:        now.Add(-2 * time.Minute),
		LatestTurnStartedAt:             now.Add(-3 * time.Minute),
		LatestTurnStateKnown:            true,
		LatestTurnCompleted:             false,
		LatestSessionClassification:     model.ClassificationCompleted,
		LatestSessionClassificationType: model.SessionCategoryWaitingForUser,
		LatestSessionSummary:            "Waiting for a user decision before editing the scorer.",
	}

	label, tag, live := m.projectAgentDisplay(project, now)
	if live {
		t.Fatalf("projectAgentDisplay() live = true, want false")
	}
	if tag != "CX" {
		t.Fatalf("projectAgentDisplay() tag = %q, want %q", tag, "CX")
	}
	if label != "CX" {
		t.Fatalf("projectAgentDisplay() label = %q, want %q", label, "CX")
	}
}

func TestProjectRunSummaryIncludesCommandAndPort(t *testing.T) {
	got, state := projectRunSummary(projectrun.Snapshot{
		Running: true,
		Command: "pnpm dev",
		Ports:   []int{3000},
	}, "")
	if state != projectRunActive {
		t.Fatalf("projectRunSummary() state = %v, want %v", state, projectRunActive)
	}
	if got != "pnpm@3000" {
		t.Fatalf("projectRunSummary() = %q, want %q", got, "pnpm@3000")
	}
}

func TestProjectRunSummaryLabelsNodeScript(t *testing.T) {
	got, state := projectRunSummary(projectrun.Snapshot{
		Running: true,
		Command: "/opt/homebrew/bin/node tune.mjs",
		Ports:   []int{9877},
	}, "")
	if state != projectRunActive {
		t.Fatalf("projectRunSummary() state = %v, want %v", state, projectRunActive)
	}
	if got != "tune.mjs@9877" {
		t.Fatalf("projectRunSummary() = %q, want %q", got, "tune.mjs@9877")
	}
}

func TestProjectRunSummaryUsesCommandAfterNestedCdPrefix(t *testing.T) {
	got, state := projectRunSummary(projectrun.Snapshot{
		Running: true,
		Command: "cd src && pnpm dev",
		Ports:   []int{3000},
	}, "")
	if state != projectRunActive {
		t.Fatalf("projectRunSummary() state = %v, want %v", state, projectRunActive)
	}
	if got != "pnpm@3000" {
		t.Fatalf("projectRunSummary() = %q, want %q", got, "pnpm@3000")
	}
}

func TestProjectLocalInstanceRunSummaryShowsExtraListeners(t *testing.T) {
	project := model.ProjectSummary{
		Name:          "okmain",
		Path:          "/tmp/okmain",
		Status:        model.StatusIdle,
		PresentOnDisk: true,
	}
	m := Model{
		projects: []model.ProjectSummary{project},
		processReports: map[string]procinspect.ProjectReport{
			project.Path: {
				ProjectPath: project.Path,
				Instances: []procinspect.ProjectInstance{
					{Process: procinspect.Process{PID: 101, PGID: 101, Command: "node tune.mjs", Ports: []int{9878}}, ProjectPath: project.Path},
					{Process: procinspect.Process{PID: 102, PGID: 102, Command: "node tune.mjs", Ports: []int{9877}}, ProjectPath: project.Path},
					{Process: procinspect.Process{PID: 103, PGID: 103, Command: "node tune.mjs", Ports: []int{9879}}, ProjectPath: project.Path},
				},
			},
		},
	}

	got, ok := m.projectLocalInstanceRunSummary(project.Path)
	if !ok {
		t.Fatalf("projectLocalInstanceRunSummary() ok = false, want true")
	}
	if got != "tune.mjs@9877+2" {
		t.Fatalf("projectLocalInstanceRunSummary() = %q, want %q", got, "tune.mjs@9877+2")
	}
}

func TestProjectRunSummaryShowsConflictInRunColumn(t *testing.T) {
	got, state := projectRunSummary(projectrun.Snapshot{
		Running:       true,
		Command:       "./bin/dev",
		Ports:         []int{3000},
		ConflictPorts: []int{3000},
	}, "")
	if state != projectRunError {
		t.Fatalf("projectRunSummary() state = %v, want %v", state, projectRunError)
	}
	if got != "dev!3000" {
		t.Fatalf("projectRunSummary() = %q, want %q", got, "dev!3000")
	}
}

func TestProjectRunSummaryUsesSavedCommandWhenIdle(t *testing.T) {
	got, state := projectRunSummary(projectrun.Snapshot{}, "pnpm dev")
	if state != projectRunIdle {
		t.Fatalf("projectRunSummary() state = %v, want %v", state, projectRunIdle)
	}
	if got != "pnpm" {
		t.Fatalf("projectRunSummary() = %q, want %q", got, "pnpm")
	}
}

func TestProjectRunSummaryUsesSavedCommandForStoppedManagedSnapshot(t *testing.T) {
	got, state := projectRunSummary(projectrun.Snapshot{
		Command:       "npm run dev",
		ExitCodeKnown: true,
		ExitCode:      1,
		LastError:     "exit status 1",
	}, "pnpm dev")
	if state != projectRunError {
		t.Fatalf("projectRunSummary() state = %v, want %v", state, projectRunError)
	}
	if got != "pnpm err" {
		t.Fatalf("projectRunSummary() = %q, want %q", got, "pnpm err")
	}
}

func TestRenderProjectListMarksAndHighlightsSelectedRow(t *testing.T) {
	prevProfile := lipgloss.ColorProfile()
	prevDarkBackground := lipgloss.HasDarkBackground()
	lipgloss.SetColorProfile(termenv.ANSI256)
	lipgloss.SetHasDarkBackground(true)
	t.Cleanup(func() {
		lipgloss.SetColorProfile(prevProfile)
		lipgloss.SetHasDarkBackground(prevDarkBackground)
	})

	m := Model{
		projects: []model.ProjectSummary{
			{
				Name:                             "first",
				Path:                             "/tmp/first",
				Status:                           model.StatusIdle,
				PresentOnDisk:                    true,
				LastActivity:                     time.Date(2026, 3, 7, 9, 0, 0, 0, time.UTC),
				LatestSessionFormat:              "modern",
				LatestSessionDetectedProjectPath: "/tmp/first",
			},
			{
				Name:                             "selected",
				Path:                             "/tmp/selected",
				Status:                           model.StatusActive,
				PresentOnDisk:                    true,
				LastActivity:                     time.Date(2026, 3, 7, 10, 0, 0, 0, time.UTC),
				LatestSessionClassification:      model.ClassificationCompleted,
				LatestSessionClassificationType:  model.SessionCategoryNeedsFollowUp,
				LatestSessionSummary:             "Follow up by checking the selected row stays on one line in the list.",
				LatestSessionFormat:              "modern",
				LatestSessionDetectedProjectPath: "/tmp/selected",
			},
		},
		selected:   1,
		sortMode:   sortByAttention,
		visibility: visibilityAIFolders,
	}

	rendered := m.renderProjectList(80, 8)
	lines := strings.Split(rendered, "\n")
	if len(lines) != 4 {
		t.Fatalf("renderProjectList() expected tabs, header, and two rows, got %q", rendered)
	}
	firstLine := lines[2]
	selectedLine := lines[3]
	firstRow := ansi.Strip(firstLine)
	selectedRow := ansi.Strip(selectedLine)
	if strings.HasPrefix(firstRow, ">") {
		t.Fatalf("renderProjectList() should not mark unselected rows: %q", firstRow)
	}
	if !strings.HasPrefix(selectedRow, "> ") {
		t.Fatalf("renderProjectList() should mark the selected row in the gutter, got %q", selectedRow)
	}
	if !strings.Contains(selectedRow, "selected") {
		t.Fatalf("renderProjectList() should preserve the selected row text, got %q", selectedRow)
	}
	if got := ansi.StringWidth(selectedRow); got > 80 {
		t.Fatalf("renderProjectList() selected row width = %d, want <= 80: %q", got, selectedRow)
	}
	if strings.Contains(firstLine, "\x1b[48;5;236m") {
		t.Fatalf("renderProjectList() should not highlight unselected rows: %q", firstLine)
	}
	if !strings.Contains(selectedLine, "\x1b[48;5;236m") {
		t.Fatalf("renderProjectList() should apply a background highlight to the selected row: %q", selectedLine)
	}
	if got := strings.Count(selectedLine, "\x1b[48;5;236m"); got < 4 {
		t.Fatalf("renderProjectList() should carry the selected-row background across styled cells, got %d matches in %q", got, selectedLine)
	}
}

func TestRenderProjectListScrollsSelectedProjectName(t *testing.T) {
	const width = 80
	projectW, _ := projectListColumnWidths(width - projectListSelectionGutterWidth)
	longName := "alpha-bravo-charlie-delta-echo-selected-project"
	m := Model{
		projects: []model.ProjectSummary{{
			Name:          longName,
			Path:          "/tmp/long-project",
			Status:        model.StatusIdle,
			PresentOnDisk: true,
		}},
		selected:      0,
		marqueeOffset: projectW + 2,
		sortMode:      sortByAttention,
		visibility:    visibilityAIFolders,
	}

	rendered := ansi.Strip(m.renderProjectList(width, 6))
	lines := strings.Split(rendered, "\n")
	if len(lines) < 3 {
		t.Fatalf("renderProjectList() expected tabs, header, and one row, got %q", rendered)
	}
	row := lines[2]
	expected := marqueeScrollText(longName, projectW, m.marqueeOffset)
	if !strings.Contains(row, expected) {
		t.Fatalf("renderProjectList() should show the scrolled project name window %q in row %q", expected, row)
	}
	if initial := truncateText(longName, projectW); strings.Contains(row, initial) {
		t.Fatalf("renderProjectList() should not keep the initial truncated project name once scrolled, got %q", row)
	}
	if got := ansi.StringWidth(row); got > width {
		t.Fatalf("renderProjectList() row width = %d, want <= %d: %q", got, width, row)
	}
}

func TestRenderProjectListKeepsWorktreePrefixFixedWhileScrollingName(t *testing.T) {
	const width = 120
	projectW, _ := projectListColumnWidths(width - projectListSelectionGutterWidth)
	rootPath := "/tmp/repo"
	longBranch := "feature/alpha-bravo-charlie-delta-echo-worktree-lane"
	prefix := "  ↳ "
	m := Model{
		projects: []model.ProjectSummary{
			{
				Name:             "repo",
				Path:             rootPath,
				Status:           model.StatusIdle,
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindMain,
			},
			{
				Name:             "repo--alpha-bravo-charlie-delta-echo-worktree-lane",
				Path:             "/tmp/repo--alpha-bravo-charlie-delta-echo-worktree-lane",
				Status:           model.StatusIdle,
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindLinked,
				RepoBranch:       longBranch,
			},
		},
		projectRows: []projectListRow{
			{Kind: projectListRowRepo, ProjectPath: rootPath, RootPath: rootPath, LinkedCount: 1},
			{Kind: projectListRowWorktree, ProjectPath: "/tmp/repo--alpha-bravo-charlie-delta-echo-worktree-lane", RootPath: rootPath},
		},
		selected:      1,
		marqueeOffset: projectW + 6,
		sortMode:      sortByAttention,
		visibility:    visibilityAllFolders,
	}

	rendered := ansi.Strip(m.renderProjectList(width, 8))
	lines := strings.Split(rendered, "\n")
	if len(lines) < 4 {
		t.Fatalf("renderProjectList() expected tabs, header, root, and child rows, got %q", rendered)
	}
	row := lines[3]
	expected := prefix + marqueeScrollText(longBranch, projectW-ansi.StringWidth(prefix), m.marqueeOffset)
	if !strings.Contains(row, expected) {
		t.Fatalf("renderProjectList() should keep the worktree prefix fixed and scroll only the branch label %q in row %q", expected, row)
	}
	legacy := marqueeScrollText(prefix+longBranch, projectW, m.marqueeOffset)
	if strings.Contains(row, legacy) {
		t.Fatalf("renderProjectList() should not scroll the worktree prefix with the branch label, got legacy window %q in row %q", legacy, row)
	}
	if got := ansi.StringWidth(row); got > width {
		t.Fatalf("renderProjectList() row width = %d, want <= %d: %q", got, width, row)
	}
}

func TestRenderProjectListScrollsRepoNameWithoutDisclosurePrefix(t *testing.T) {
	const width = 120
	projectW, _ := projectListColumnWidths(width - projectListSelectionGutterWidth)
	longName := "repo-alpha-bravo-charlie-delta-echo-worktree-family"
	m := Model{
		projects: []model.ProjectSummary{{
			Name:             longName,
			Path:             "/tmp/repo-alpha-bravo-charlie-delta-echo-worktree-family",
			Status:           model.StatusIdle,
			PresentOnDisk:    true,
			WorktreeRootPath: "/tmp/repo-alpha-bravo-charlie-delta-echo-worktree-family",
			WorktreeKind:     model.WorktreeKindMain,
		}},
		projectRows: []projectListRow{{
			Kind:        projectListRowRepo,
			ProjectPath: "/tmp/repo-alpha-bravo-charlie-delta-echo-worktree-family",
			RootPath:    "/tmp/repo-alpha-bravo-charlie-delta-echo-worktree-family",
			LinkedCount: 1,
		}},
		selected:      0,
		marqueeOffset: projectW + 6,
		sortMode:      sortByAttention,
		visibility:    visibilityAllFolders,
	}

	rendered := ansi.Strip(m.renderProjectList(width, 6))
	lines := strings.Split(rendered, "\n")
	if len(lines) < 3 {
		t.Fatalf("renderProjectList() expected tabs, header, and one row, got %q", rendered)
	}
	row := lines[2]
	expected := marqueeScrollText(longName, projectW, m.marqueeOffset)
	if !strings.Contains(row, expected) {
		t.Fatalf("renderProjectList() should scroll the full repo label %q in row %q", expected, row)
	}
	if strings.ContainsAny(row, "▸▾") {
		t.Fatalf("renderProjectList() should not prefix repo roots with a disclosure marker, got %q", row)
	}
}

func TestMoveSelectionFlashesSelectedRowBriefly(t *testing.T) {
	prevProfile := lipgloss.ColorProfile()
	prevDarkBackground := lipgloss.HasDarkBackground()
	lipgloss.SetColorProfile(termenv.ANSI256)
	lipgloss.SetHasDarkBackground(true)
	t.Cleanup(func() {
		lipgloss.SetColorProfile(prevProfile)
		lipgloss.SetHasDarkBackground(prevDarkBackground)
	})

	now := time.Date(2026, 3, 7, 10, 0, 0, 0, time.UTC)
	m := Model{
		nowFn: func() time.Time { return now },
		projects: []model.ProjectSummary{
			{
				Name:          "first",
				Path:          "/tmp/first",
				Status:        model.StatusIdle,
				PresentOnDisk: true,
			},
			{
				Name:          "selected",
				Path:          "/tmp/selected",
				Status:        model.StatusActive,
				PresentOnDisk: true,
			},
		},
		selected:   0,
		sortMode:   sortByAttention,
		visibility: visibilityAIFolders,
	}

	if m.selectionFlashActive() {
		t.Fatalf("selection flash should start inactive")
	}
	_ = m.moveSelectionTo(1)
	if !m.selectionFlashActive() {
		t.Fatalf("moveSelectionTo() should flash the selected row")
	}
	flashing := m.renderProjectList(80, 8)

	now = now.Add(projectListSelectionFlashDuration + time.Nanosecond)
	m.pruneTransientHighlights(now)
	if m.selectionFlashActive() {
		t.Fatalf("selection flash should expire after %v", projectListSelectionFlashDuration)
	}
	settled := m.renderProjectList(80, 8)
	if ansi.Strip(flashing) != ansi.Strip(settled) {
		t.Fatalf("selection flash should keep visible row text: %q vs %q", ansi.Strip(flashing), ansi.Strip(settled))
	}
	if flashing == settled {
		t.Fatalf("selection flash should change the selected row styling")
	}
}

func TestRenderProjectListShowsRepoWarningInAttentionColumn(t *testing.T) {
	m := Model{
		projects: []model.ProjectSummary{{
			Name:           "demo project",
			Path:           "/tmp/demo",
			Status:         model.StatusIdle,
			PresentOnDisk:  true,
			AttentionScore: 95,
			RepoDirty:      true,
		}},
		selected:   0,
		sortMode:   sortByAttention,
		visibility: visibilityAIFolders,
	}

	rendered := ansi.Strip(m.renderProjectList(60, 6))
	lines := strings.Split(rendered, "\n")
	if len(lines) < 3 {
		t.Fatalf("renderProjectList() expected tabs, header, and one row, got %q", rendered)
	}
	if !strings.Contains(lines[2], "!   95") {
		t.Fatalf("renderProjectList() should show repo warnings in ATTN, got %q", lines[2])
	}
	if strings.Contains(lines[2], "demo project !") {
		t.Fatalf("renderProjectList() should keep the project name free of suffix markers, got %q", lines[2])
	}
}

func TestRenderProjectListShowsTODOCount(t *testing.T) {
	m := Model{
		projects: []model.ProjectSummary{{
			Name:           "alpha",
			Path:           "/tmp/alpha",
			Status:         model.StatusIdle,
			PresentOnDisk:  true,
			OpenTODOCount:  3,
			TotalTODOCount: 5,
		}},
		selected:   0,
		sortMode:   sortByAttention,
		visibility: visibilityAIFolders,
	}

	rendered := ansi.Strip(m.renderProjectList(80, 6))
	lines := strings.Split(rendered, "\n")
	if len(lines) < 3 {
		t.Fatalf("renderProjectList() expected tabs, header, and one row, got %q", rendered)
	}
	if !strings.Contains(lines[1], "AGENT") || !strings.Contains(lines[1], "TODO RUN") {
		t.Fatalf("renderProjectList() missing agent/todo/run headers, got %q", lines[1])
	}
	if !strings.Contains(lines[2], " 3 ") {
		t.Fatalf("renderProjectList() should show the open TODO count in the row, got %q", lines[2])
	}
}

func TestRenderProjectListShowsProcessWarningInRunColumn(t *testing.T) {
	project := model.ProjectSummary{
		Name:          "alpha",
		Path:          "/tmp/alpha",
		Status:        model.StatusIdle,
		PresentOnDisk: true,
	}
	m := Model{
		projects: []model.ProjectSummary{project},
		selected: 0,
		processReports: map[string]procinspect.ProjectReport{
			project.Path: {
				ProjectPath: project.Path,
				Findings: []procinspect.Finding{{
					Process:     procinspect.Process{PID: 49995, PPID: 1, CPU: 98.5, Ports: []int{9229}},
					ProjectPath: project.Path,
				}},
			},
		},
	}

	rendered := ansi.Strip(m.renderProjectList(90, 6))
	lines := strings.Split(rendered, "\n")
	if len(lines) < 3 {
		t.Fatalf("renderProjectList() expected tabs, header, and one row, got %q", rendered)
	}
	if !strings.Contains(lines[2], "HOT!") {
		t.Fatalf("renderProjectList() should flag suspicious hot PIDs in RUN, got %q", lines[2])
	}
}

func TestRenderProjectListShowsLocalInstanceInRunColumn(t *testing.T) {
	project := model.ProjectSummary{
		Name:          "portfolio",
		Path:          "/tmp/portfolio",
		Status:        model.StatusIdle,
		PresentOnDisk: true,
		RunCommand:    "pnpm dev",
	}
	m := Model{
		projects: []model.ProjectSummary{project},
		selected: 0,
		processReports: map[string]procinspect.ProjectReport{
			project.Path: {
				ProjectPath: project.Path,
				Instances: []procinspect.ProjectInstance{{
					Process:     procinspect.Process{PID: 4017, PGID: 4017, Command: "vite --host 127.0.0.1", Ports: []int{4017}},
					ProjectPath: project.Path,
				}},
			},
		},
	}

	rendered := ansi.Strip(m.renderProjectList(90, 6))
	lines := strings.Split(rendered, "\n")
	if len(lines) < 3 {
		t.Fatalf("renderProjectList() expected tabs, header, and one row, got %q", rendered)
	}
	if !strings.Contains(lines[2], "vite@4017") {
		t.Fatalf("renderProjectList() should show local listener in RUN, got %q", lines[2])
	}
}

func TestRenderProjectListKeepsScratchTasksInlineAndKeepsRepoWarningOffTheirRows(t *testing.T) {
	m := Model{
		projects: []model.ProjectSummary{
			{
				Name:          "alpha",
				Path:          "/tmp/alpha",
				Status:        model.StatusIdle,
				PresentOnDisk: true,
			},
			{
				Name:           "answer Sarah email",
				Path:           "/tmp/tasks/2026-04-14-answer-sarah-email",
				Kind:           model.ProjectKindScratchTask,
				Status:         model.StatusIdle,
				PresentOnDisk:  true,
				AttentionScore: 20,
				RepoDirty:      true,
				ManuallyAdded:  true,
			},
		},
		selected:   1,
		sortMode:   sortByAttention,
		visibility: visibilityAIFolders,
	}

	rendered := ansi.Strip(m.renderProjectList(120, 8))
	if strings.Contains(rendered, "Projects") || strings.Contains(rendered, "Scratch Tasks") {
		t.Fatalf("renderProjectList() should keep scratch tasks in the same list without kind headers, got %q", rendered)
	}
	lines := strings.Split(rendered, "\n")
	scratchLine := ""
	for _, line := range lines {
		if strings.Contains(line, "[T]") {
			scratchLine = line
			break
		}
	}
	if scratchLine == "" {
		t.Fatalf("renderProjectList() missing scratch task row, got %q", rendered)
	}
	if strings.Contains(scratchLine, "!") {
		t.Fatalf("scratch task rows should not show repo warning markers, got %q", scratchLine)
	}
}

func TestRebuildProjectListIncludesOpenAgentTasksWithDetails(t *testing.T) {
	now := time.Date(2026, 5, 3, 2, 30, 0, 0, time.UTC)
	task := model.AgentTask{
		ID:            "agt_cursor_access",
		Title:         "Revoke Cursor GitHub access",
		Status:        model.AgentTaskStatusWaiting,
		Summary:       "Waiting for browser confirmation in GitHub settings",
		Capabilities:  []string{"browser.control", "github.inspect"},
		Provider:      model.SessionSourceCodex,
		SessionID:     "thread-agent-123456789",
		WorkspacePath: "/tmp/lcroom-agent-task-cursor",
		LastTouchedAt: now,
		Resources: []model.AgentTaskResource{{
			Kind:      model.AgentTaskResourceEngineerSession,
			Provider:  model.SessionSourceCodex,
			SessionID: "thread-agent-123456789",
			Label:     "current engineer session",
		}},
	}
	m := Model{
		nowFn: func() time.Time { return now },
		allProjects: []model.ProjectSummary{{
			Name:           "alpha",
			Path:           "/tmp/alpha",
			Status:         model.StatusIdle,
			PresentOnDisk:  true,
			ManuallyAdded:  true,
			AttentionScore: 10,
		}},
		openAgentTasks: []model.AgentTask{task},
		sortMode:       sortByAttention,
		visibility:     visibilityAIFolders,
	}

	m.rebuildProjectList(task.WorkspacePath)
	if len(m.projects) != 2 {
		t.Fatalf("visible projects = %#v, want regular project plus agent task", m.projects)
	}
	if selected, ok := m.selectedProject(); !ok || selected.Path != task.WorkspacePath || selected.Kind != model.ProjectKindAgentTask {
		t.Fatalf("selected project = %#v, want agent task row", selected)
	}
	if cmd := m.requestProjectDetailViewCmd(task.WorkspacePath); cmd != nil {
		t.Fatalf("agent task detail should render from cached task state without loading project detail")
	}

	rendered := ansi.Strip(m.renderProjectList(150, 8))
	if strings.Contains(rendered, "Agent Tasks") {
		t.Fatalf("agent tasks should stay inline with projects, got %q", rendered)
	}
	if !strings.Contains(rendered, "[A] Revoke Cursor") ||
		!strings.Contains(rendered, "review") ||
		!strings.Contains(rendered, "Waiting for browser confirmation") {
		t.Fatalf("renderProjectList() missing agent task row details, got %q", rendered)
	}

	detail := ansi.Strip(m.renderDetailContent(110))
	for _, want := range []string{"agent task", "agt_cursor_access", "browser.control", "Codex thread-a", "Press Enter to open"} {
		if !strings.Contains(detail, want) {
			t.Fatalf("renderDetailContent() missing %q in %q", want, detail)
		}
	}
}

func TestProjectsMsgThreadsOpenAgentTasksIntoClassicList(t *testing.T) {
	task := model.AgentTask{
		ID:            "agt_loaded",
		Title:         "Loaded background task",
		Status:        model.AgentTaskStatusActive,
		WorkspacePath: "/tmp/lcroom-agent-task-loaded",
		LastTouchedAt: time.Date(2026, 5, 3, 2, 40, 0, 0, time.UTC),
	}
	m := Model{
		sortMode:   sortByAttention,
		visibility: visibilityAIFolders,
	}

	updated, _ := m.Update(projectsMsg{openAgentTasks: []model.AgentTask{task}})
	got := updated.(Model)
	if len(got.projects) != 1 {
		t.Fatalf("visible projects = %#v, want one agent task", got.projects)
	}
	if got.projects[0].Kind != model.ProjectKindAgentTask || got.projects[0].Path != task.WorkspacePath {
		t.Fatalf("project row = %#v, want agent task synthetic summary", got.projects[0])
	}
	if _, ok := got.agentTaskForProjectPath(task.WorkspacePath); !ok {
		t.Fatalf("open agent task cache missing %q", task.WorkspacePath)
	}
}

func TestAgentTaskSelectionOpensTrackedEngineerSession(t *testing.T) {
	task := model.AgentTask{
		ID:            "agt_open_engineer",
		Title:         "Inspect delegated work",
		Status:        model.AgentTaskStatusActive,
		Provider:      model.SessionSourceCodex,
		SessionID:     "thread-agent-existing",
		WorkspacePath: "/tmp/lcroom-agent-task-open",
		LastTouchedAt: time.Date(2026, 5, 3, 2, 35, 0, 0, time.UTC),
		Resources: []model.AgentTaskResource{{
			Kind:      model.AgentTaskResourceEngineerSession,
			Provider:  model.SessionSourceCodex,
			SessionID: "thread-agent-existing",
		}},
	}
	project, err := projectSummaryForAgentTask(task)
	if err != nil {
		t.Fatalf("projectSummaryForAgentTask() error = %v", err)
	}
	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider:       req.Provider,
				ThreadID:       firstNonEmptyTrimmed(req.ResumeID, "thread-agent-new"),
				Started:        true,
				LastActivityAt: time.Now(),
			},
		}, nil
	})
	m := Model{
		projects:       []model.ProjectSummary{project},
		openAgentTasks: []model.AgentTask{task},
		selected:       0,
		codexManager:   manager,
	}

	updated, cmd := m.launchEmbeddedForSelection(codexapp.ProviderCodex, false, "")
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("launchEmbeddedForSelection() cmd = nil, want launch command")
	}
	msgs := collectCmdMsgs(cmd)
	var opened codexSessionOpenedMsg
	for _, msg := range msgs {
		if typed, ok := msg.(codexSessionOpenedMsg); ok {
			opened = typed
			break
		}
	}
	if opened.projectPath == "" || opened.err != nil {
		t.Fatalf("open messages = %#v, want successful codexSessionOpenedMsg", msgs)
	}
	if len(requests) != 1 {
		t.Fatalf("launch requests = %d, want 1", len(requests))
	}
	if requests[0].ProjectPath != task.WorkspacePath {
		t.Fatalf("request ProjectPath = %q, want %q", requests[0].ProjectPath, task.WorkspacePath)
	}
	if requests[0].ResumeID != "thread-agent-existing" {
		t.Fatalf("request ResumeID = %q, want tracked engineer session", requests[0].ResumeID)
	}
	if got.codexPendingOpen == nil || got.codexPendingOpen.projectPath != task.WorkspacePath {
		t.Fatalf("pending open = %#v, want agent task workspace", got.codexPendingOpen)
	}
}

func TestAgentTaskRemoveHotkeyOpensAgentTaskActionDialog(t *testing.T) {
	task := model.AgentTask{
		ID:            "agt_archive_hotkey",
		Title:         "Review highlighted task",
		Status:        model.AgentTaskStatusWaiting,
		WorkspacePath: "/tmp/lcroom-agent-task-hotkey",
	}
	project, err := projectSummaryForAgentTask(task)
	if err != nil {
		t.Fatalf("projectSummaryForAgentTask() error = %v", err)
	}
	m := Model{
		focusedPane:    focusProjects,
		projects:       []model.ProjectSummary{project},
		openAgentTasks: []model.AgentTask{task},
		selected:       0,
	}

	footer := ansi.Strip(m.renderFooter(160))
	if !strings.Contains(footer, "x archive") {
		t.Fatalf("renderFooter() should advertise agent task archiving, got %q", footer)
	}

	updated, cmd := m.updateNormalMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("agent task remove hotkey should open a dialog before running any command")
	}
	if got.agentTaskAction == nil {
		t.Fatalf("agent task remove hotkey should open the agent task action dialog")
	}
	if got.worktreeRemoveConfirm != nil {
		t.Fatalf("agent task remove hotkey should not open the linked-worktree removal dialog")
	}
	if got.agentTaskAction.Selected != agentTaskActionFocusKeep {
		t.Fatalf("default agent task action selection = %d, want keep", got.agentTaskAction.Selected)
	}
	rendered := ansi.Strip(got.renderAgentTaskActionOverlay("", 100, 24))
	if strings.Contains(rendered, "linked worktree") || !strings.Contains(rendered, "Archive") || !strings.Contains(rendered, "Keep") {
		t.Fatalf("agent task action overlay should offer archive/keep without worktree copy, got %q", rendered)
	}
}

func TestDispatchRemoveCommandOpensAgentTaskActionDialog(t *testing.T) {
	t.Parallel()

	task := model.AgentTask{
		ID:            "agt_archive_command",
		Title:         "Review delegated task",
		Status:        model.AgentTaskStatusWaiting,
		WorkspacePath: "/tmp/lcroom-agent-task-command",
	}
	project, err := projectSummaryForAgentTask(task)
	if err != nil {
		t.Fatalf("projectSummaryForAgentTask() error = %v", err)
	}
	m := Model{
		projects:       []model.ProjectSummary{project},
		openAgentTasks: []model.AgentTask{task},
		selected:       0,
	}

	updated, cmd := m.dispatchCommand(commands.Invocation{Kind: commands.KindRemove, Canonical: "/remove"})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("dispatchCommand(/remove) should open a dialog before running any command")
	}
	if got.agentTaskAction == nil {
		t.Fatalf("dispatchCommand(/remove) should open the agent task action dialog")
	}
}

func TestAgentTaskActionArchiveQueuesArchiveCommand(t *testing.T) {
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

	task, err := svc.CreateAgentTask(ctx, model.CreateAgentTaskInput{
		Title: "Archive delegated task",
		Kind:  model.AgentTaskKindAgent,
	})
	if err != nil {
		t.Fatalf("CreateAgentTask() error = %v", err)
	}
	project, err := projectSummaryForAgentTask(task)
	if err != nil {
		t.Fatalf("projectSummaryForAgentTask() error = %v", err)
	}
	neighbor := model.ProjectSummary{Name: "neighbor", Path: "/tmp/neighbor", PresentOnDisk: true}
	m := Model{
		ctx:            ctx,
		svc:            svc,
		allProjects:    []model.ProjectSummary{neighbor},
		projects:       []model.ProjectSummary{project, neighbor},
		openAgentTasks: []model.AgentTask{task},
		selected:       0,
		visibility:     visibilityAllFolders,
		agentTaskAction: &agentTaskActionConfirmState{
			TaskID:      task.ID,
			ProjectPath: task.WorkspacePath,
			TaskTitle:   task.Title,
			Selected:    agentTaskActionFocusKeep,
		},
	}

	updated, _ := m.updateAgentTaskActionConfirmMode(tea.KeyMsg{Type: tea.KeyTab})
	got := updated.(Model)
	if got.agentTaskAction.Selected != agentTaskActionFocusArchive {
		t.Fatalf("first tab should move focus to archive, got %d", got.agentTaskAction.Selected)
	}

	updated, cmd := got.updateAgentTaskActionConfirmMode(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if got.status != "Archiving agent task..." {
		t.Fatalf("status = %q, want archive progress", got.status)
	}
	if cmd == nil {
		t.Fatalf("archive action should queue a command")
	}
	rawMsg := cmd()
	msg, ok := rawMsg.(agentTaskActionMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want agentTaskActionMsg", rawMsg)
	}
	if msg.err != nil {
		t.Fatalf("agentTaskActionMsg.err = %v, want nil", msg.err)
	}
	if msg.status != "Agent task archived" {
		t.Fatalf("agentTaskActionMsg.status = %q, want archive status", msg.status)
	}
	if msg.selectPath != "/tmp/neighbor" {
		t.Fatalf("agentTaskActionMsg.selectPath = %q, want neighbor fallback", msg.selectPath)
	}

	after, reloadCmd := got.Update(msg)
	saved := after.(Model)
	if reloadCmd == nil {
		t.Fatalf("successful agent task archive should queue a list reload")
	}
	if saved.agentTaskAction != nil {
		t.Fatalf("successful agent task archive should close the dialog")
	}
	if len(saved.openAgentTasks) != 0 {
		t.Fatalf("archived agent task should leave open task cache, got %#v", saved.openAgentTasks)
	}
	if len(saved.projects) != 1 || saved.projects[0].Path != "/tmp/neighbor" {
		t.Fatalf("visible projects after archive = %#v, want neighbor only", saved.projects)
	}

	openTasks, err := svc.ListOpenAgentTasks(ctx, 5)
	if err != nil {
		t.Fatalf("ListOpenAgentTasks() error = %v", err)
	}
	if len(openTasks) != 0 {
		t.Fatalf("archived agent task should leave open task list, got %#v", openTasks)
	}
}

func TestOpenAgentTaskActionWithBusySessionShowsAttentionDialog(t *testing.T) {
	task := model.AgentTask{
		ID:            "agt_archive_busy",
		Title:         "Wait for engineer",
		Status:        model.AgentTaskStatusActive,
		WorkspacePath: "/tmp/lcroom-agent-task-busy",
	}
	project, err := projectSummaryForAgentTask(task)
	if err != nil {
		t.Fatalf("projectSummaryForAgentTask() error = %v", err)
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider: req.Provider.Normalized(),
				Started:  true,
				ThreadID: "thread-agent-busy",
				Busy:     true,
				Status:   req.Provider.Label() + " session busy",
			},
		}, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: task.WorkspacePath,
		Provider:    codexapp.ProviderCodex,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}
	m := Model{
		codexManager:   manager,
		projects:       []model.ProjectSummary{project},
		openAgentTasks: []model.AgentTask{task},
		selected:       0,
	}

	cmd := m.openAgentTaskActionConfirmForSelection()
	if cmd != nil {
		t.Fatalf("blocked archive should not schedule a command")
	}
	if m.agentTaskAction != nil {
		t.Fatalf("agent task action dialog should stay closed while the session is busy")
	}
	if m.attentionDialog == nil {
		t.Fatalf("blocked archive should show the attention dialog")
	}
	if m.attentionDialog.Title != "Archive blocked" {
		t.Fatalf("attention dialog title = %q, want archive blocked", m.attentionDialog.Title)
	}
	if strings.Contains(m.status, "linked worktree") {
		t.Fatalf("status should not mention linked worktrees, got %q", m.status)
	}
}

func TestScratchTaskHotkeyOpensTaskActionDialog(t *testing.T) {
	m := Model{
		projects: []model.ProjectSummary{{
			Name:          "answer Sarah email",
			Path:          "/tmp/tasks/answer-sarah-email",
			Kind:          model.ProjectKindScratchTask,
			PresentOnDisk: true,
		}},
		selected: 0,
	}

	updated, cmd := m.updateNormalMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("task action hotkey should open a dialog before running any command")
	}
	if got.scratchTaskAction == nil {
		t.Fatalf("scratch task hotkey should open the task action dialog")
	}
	if got.scratchTaskAction.Selected != scratchTaskActionFocusKeep {
		t.Fatalf("default task action selection = %d, want keep", got.scratchTaskAction.Selected)
	}
	rendered := ansi.Strip(got.renderScratchTaskActionOverlay("", 100, 24))
	if !strings.Contains(rendered, "Archive") || !strings.Contains(rendered, "Delete") {
		t.Fatalf("task action overlay should offer archive and delete, got %q", rendered)
	}
}

func TestDispatchTaskActionsCommandOpensScratchTaskActionDialog(t *testing.T) {
	t.Parallel()

	m := Model{
		projects: []model.ProjectSummary{{
			Name:          "answer Sarah email",
			Path:          "/tmp/tasks/answer-sarah-email",
			Kind:          model.ProjectKindScratchTask,
			PresentOnDisk: true,
		}},
		selected: 0,
	}

	updated, cmd := m.dispatchCommand(commands.Invocation{Kind: commands.KindTaskActions, Canonical: "/task-actions"})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("dispatchCommand(/task-actions) should open a dialog before running any command")
	}
	if got.scratchTaskAction == nil {
		t.Fatalf("dispatchCommand(/task-actions) should open the task action dialog")
	}
	if got.scratchTaskAction.Selected != scratchTaskActionFocusKeep {
		t.Fatalf("default task action selection = %d, want keep", got.scratchTaskAction.Selected)
	}
}

func TestDispatchArchiveCommandArchivesScratchTask(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	cfg := config.Default()
	cfg.ScratchRoot = filepath.Join(t.TempDir(), "tasks")
	svc := service.New(cfg, st, events.NewBus(), nil)
	result, err := svc.CreateScratchTask(ctx, service.CreateScratchTaskRequest{Title: "Archive this task"})
	if err != nil {
		t.Fatalf("CreateScratchTask() error = %v", err)
	}

	m := Model{
		ctx: ctx,
		svc: svc,
		projects: []model.ProjectSummary{
			{
				Name:          "Archive this task",
				Path:          result.TaskPath,
				Kind:          model.ProjectKindScratchTask,
				PresentOnDisk: true,
			},
			{
				Name:          "neighbor",
				Path:          "/tmp/neighbor",
				PresentOnDisk: true,
			},
		},
		selected: 0,
	}

	updated, cmd := m.dispatchCommand(commands.Invocation{Kind: commands.KindArchive, Canonical: "/archive"})
	got := updated.(Model)
	if got.status != "Archiving task..." {
		t.Fatalf("status = %q, want archive progress", got.status)
	}
	if cmd == nil {
		t.Fatalf("/archive should queue a scratch task archive command")
	}
	rawMsg := cmd()
	msg, ok := rawMsg.(scratchTaskActionMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want scratchTaskActionMsg", rawMsg)
	}
	if msg.err != nil {
		t.Fatalf("scratchTaskActionMsg.err = %v, want nil", msg.err)
	}
	if msg.status != "Scratch task archived" {
		t.Fatalf("scratchTaskActionMsg.status = %q, want archive status", msg.status)
	}
	if msg.selectPath != "/tmp/neighbor" {
		t.Fatalf("scratchTaskActionMsg.selectPath = %q, want neighbor fallback", msg.selectPath)
	}
}

func TestDispatchRemoveCommandOpensScratchTaskActionDialog(t *testing.T) {
	t.Parallel()

	m := Model{
		projects: []model.ProjectSummary{{
			Name:          "answer Sarah email",
			Path:          "/tmp/tasks/answer-sarah-email",
			Kind:          model.ProjectKindScratchTask,
			PresentOnDisk: true,
		}},
		selected: 0,
	}

	updated, cmd := m.dispatchCommand(commands.Invocation{Kind: commands.KindRemove, Canonical: "/remove"})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("dispatchCommand(/remove) should open a dialog before running any command")
	}
	if got.scratchTaskAction == nil {
		t.Fatalf("dispatchCommand(/remove) should open the task action dialog")
	}
	if got.scratchTaskAction.Selected != scratchTaskActionFocusKeep {
		t.Fatalf("default task action selection = %d, want keep", got.scratchTaskAction.Selected)
	}
}

func TestScratchTaskActionArchiveQueuesArchiveCommand(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	cfg := config.Default()
	cfg.ScratchRoot = filepath.Join(t.TempDir(), "tasks")
	svc := service.New(cfg, st, events.NewBus(), nil)
	result, err := svc.CreateScratchTask(ctx, service.CreateScratchTaskRequest{Title: "Archive this task"})
	if err != nil {
		t.Fatalf("CreateScratchTask() error = %v", err)
	}

	m := Model{
		ctx: ctx,
		svc: svc,
		projects: []model.ProjectSummary{
			{
				Name:          "Archive this task",
				Path:          result.TaskPath,
				Kind:          model.ProjectKindScratchTask,
				PresentOnDisk: true,
			},
			{
				Name:          "neighbor",
				Path:          "/tmp/neighbor",
				PresentOnDisk: true,
			},
		},
		selected: 0,
		scratchTaskAction: &scratchTaskActionConfirmState{
			ProjectPath:   result.TaskPath,
			ProjectName:   "Archive this task",
			PresentOnDisk: true,
			Selected:      scratchTaskActionFocusKeep,
		},
	}

	updated, _ := m.updateScratchTaskActionConfirmMode(tea.KeyMsg{Type: tea.KeyTab})
	got := updated.(Model)
	if got.scratchTaskAction.Selected != scratchTaskActionFocusArchive {
		t.Fatalf("first tab should move focus to archive, got %d", got.scratchTaskAction.Selected)
	}

	updated, cmd := got.updateScratchTaskActionConfirmMode(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if got.status != "Archiving task..." {
		t.Fatalf("status = %q, want archive progress", got.status)
	}
	if cmd == nil {
		t.Fatalf("archive action should queue a command")
	}
	rawMsg := cmd()
	msg, ok := rawMsg.(scratchTaskActionMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want scratchTaskActionMsg", rawMsg)
	}
	if msg.err != nil {
		t.Fatalf("scratchTaskActionMsg.err = %v, want nil", msg.err)
	}
	if msg.status != "Scratch task archived" {
		t.Fatalf("scratchTaskActionMsg.status = %q, want archive status", msg.status)
	}
	if msg.selectPath != "/tmp/neighbor" {
		t.Fatalf("scratchTaskActionMsg.selectPath = %q, want neighbor fallback", msg.selectPath)
	}
}

func TestRenderProjectListAlwaysShowsLinkedWorktreesUnderRepoRow(t *testing.T) {
	rootPath := "/tmp/repo"
	m := Model{
		allProjects: []model.ProjectSummary{
			{
				Name:                          "repo",
				Path:                          rootPath,
				Status:                        model.StatusIdle,
				PresentOnDisk:                 true,
				WorktreeRootPath:              rootPath,
				WorktreeKind:                  model.WorktreeKindMain,
				RepoBranch:                    "master",
				LatestCompletedSessionSummary: "Keep root summary",
			},
			{
				Name:             "repo--feat-parallel-lane",
				Path:             "/tmp/repo--feat-parallel-lane",
				Status:           model.StatusActive,
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindLinked,
				RepoBranch:       "feat/parallel-lane",
				RepoDirty:        true,
			},
		},
		sortMode:   sortByAttention,
		visibility: visibilityAllFolders,
	}

	m.rebuildProjectList(rootPath)
	rendered := ansi.Strip(m.renderProjectList(140, 8))
	lines := strings.Split(rendered, "\n")
	if len(lines) != 4 {
		t.Fatalf("renderProjectList() expected tabs, header, root, and linked worktree rows, got %q", rendered)
	}
	if strings.ContainsAny(lines[2], "▸▾") {
		t.Fatalf("renderProjectList() should not show a disclosure marker on the repo root, got %q", lines[2])
	}
	if !strings.Contains(lines[2], "Keep root summary") {
		t.Fatalf("renderProjectList() should keep the root repo assessment text, got %q", lines[2])
	}
	if !strings.Contains(lines[2], "[1 linked, 1 to integrate, 1 active]") {
		t.Fatalf("renderProjectList() should show a compact linked-worktree badge, got %q", lines[2])
	}
	if strings.Contains(lines[2], "2 worktrees") {
		t.Fatalf("renderProjectList() should not describe the root repo as a generic worktree, got %q", lines[2])
	}
	if !strings.Contains(lines[3], "↳ feat/parallel") {
		t.Fatalf("renderProjectList() should always show the indented linked worktree row, got %q", lines[3])
	}
}

func TestRenderProjectListShowsOrphanedWorktreeBadgeOnRootRow(t *testing.T) {
	rootPath := "/tmp/repo"
	m := Model{
		allProjects: []model.ProjectSummary{
			{
				Name:                          "repo",
				Path:                          rootPath,
				Status:                        model.StatusIdle,
				PresentOnDisk:                 true,
				WorktreeRootPath:              rootPath,
				WorktreeKind:                  model.WorktreeKindMain,
				RepoBranch:                    "master",
				LatestCompletedSessionSummary: "Keep root summary",
			},
		},
		orphanedWorktreesByRoot: map[string][]model.ProjectSummary{
			rootPath: {{
				Name:             "repo--stale-lane",
				Path:             "/tmp/repo--stale-lane",
				Status:           model.StatusIdle,
				PresentOnDisk:    true,
				Forgotten:        true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindLinked,
				RepoBranch:       "todo/stale-lane",
			}},
		},
		sortMode:   sortByAttention,
		visibility: visibilityAllFolders,
	}

	m.rebuildProjectList(rootPath)
	rendered := ansi.Strip(m.renderProjectList(160, 8))
	lines := strings.Split(rendered, "\n")
	if len(lines) != 3 {
		t.Fatalf("renderProjectList() expected tabs, header, and one root row, got %q", rendered)
	}
	if !strings.Contains(lines[2], "Keep root summary") {
		t.Fatalf("renderProjectList() should keep the root summary text, got %q", lines[2])
	}
	if !strings.Contains(lines[2], "[1 orphaned]") {
		t.Fatalf("renderProjectList() should show an orphaned-checkout badge on the root row, got %q", lines[2])
	}
}

func TestRebuildProjectListUsesMostRecentWorktreeActivityForRootRow(t *testing.T) {
	rootPath := "/tmp/repo"
	rootLast := time.Date(2026, 3, 7, 9, 0, 0, 0, time.UTC)
	worktreeLast := time.Date(2026, 3, 7, 10, 0, 0, 0, time.UTC)
	m := Model{
		allProjects: []model.ProjectSummary{
			{
				Name:             "repo",
				Path:             rootPath,
				LastActivity:     rootLast,
				Status:           model.StatusIdle,
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindMain,
			},
			{
				Name:             "repo--feat-parallel-lane",
				Path:             "/tmp/repo--feat-parallel-lane",
				LastActivity:     worktreeLast,
				Status:           model.StatusIdle,
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindLinked,
			},
		},
		sortMode:   sortByAttention,
		visibility: visibilityAllFolders,
	}

	m.rebuildProjectList(rootPath)

	if len(m.projects) != 2 {
		t.Fatalf("rebuildProjectList() grouped projects = %#v, want root and linked worktree rows", m.projects)
	}
	if got := m.projects[0].LastActivity; !got.Equal(worktreeLast) {
		t.Fatalf("root row LastActivity = %v, want %v from the linked worktree", got, worktreeLast)
	}
}

func TestRenderProjectListShowsWorktreeChildrenWithoutRootDisclosure(t *testing.T) {
	rootPath := "/tmp/repo"
	m := Model{
		allProjects: []model.ProjectSummary{
			{
				Name:             "repo",
				Path:             rootPath,
				Status:           model.StatusIdle,
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindMain,
				RepoBranch:       "master",
			},
			{
				Name:             "repo--feat-parallel-lane",
				Path:             "/tmp/repo--feat-parallel-lane",
				Status:           model.StatusActive,
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindLinked,
				RepoBranch:       "feat/parallel-lane",
			},
		},
		sortMode:   sortByAttention,
		visibility: visibilityAllFolders,
	}

	m.rebuildProjectList(rootPath)
	rendered := ansi.Strip(m.renderProjectList(160, 8))
	lines := strings.Split(rendered, "\n")
	if len(lines) != 4 {
		t.Fatalf("renderProjectList() expected tabs, header, root, and child rows, got %q", rendered)
	}
	if strings.ContainsAny(lines[2], "▸▾") {
		t.Fatalf("renderProjectList() should omit disclosure markers from the repo root, got %q", lines[2])
	}
	if !strings.Contains(lines[3], "  ↳ feat/parallel-lane") {
		t.Fatalf("renderProjectList() should render the child worktree branch label, got %q", lines[3])
	}
}

func TestRenderProjectListSurfacesCleanUnmergedWorktree(t *testing.T) {
	rootPath := "/tmp/repo"
	childPath := "/tmp/repo--feat-parallel-lane"
	m := Model{
		allProjects: []model.ProjectSummary{
			{
				Name:             "repo",
				Path:             rootPath,
				Status:           model.StatusIdle,
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindMain,
				RepoBranch:       "master",
			},
			{
				Name:                 "repo--feat-parallel-lane",
				Path:                 childPath,
				Status:               model.StatusIdle,
				PresentOnDisk:        true,
				WorktreeRootPath:     rootPath,
				WorktreeKind:         model.WorktreeKindLinked,
				WorktreeParentBranch: "master",
				WorktreeMergeStatus:  model.WorktreeMergeStatusNotMerged,
				RepoBranch:           "feat/parallel-lane",
			},
		},
		sortMode:   sortByAttention,
		visibility: visibilityAllFolders,
	}

	m.rebuildProjectList(rootPath)
	rendered := ansi.Strip(m.renderProjectList(160, 8))
	lines := strings.Split(rendered, "\n")
	if len(lines) != 4 {
		t.Fatalf("renderProjectList() should show a clean unmerged worktree, got %q", rendered)
	}
	if strings.ContainsAny(lines[2], "▸▾") || !strings.Contains(lines[2], "M") {
		t.Fatalf("renderProjectList() should mark the root row when a linked worktree needs merging, got %q", lines[2])
	}
	if !strings.Contains(lines[2], "[1 linked, 1 to integrate]") {
		t.Fatalf("renderProjectList() should count linked worktrees pending integration in the root badge, got %q", lines[2])
	}
	if !strings.Contains(lines[3], "↳ feat/parallel-lane") || !strings.Contains(lines[3], "M") {
		t.Fatalf("renderProjectList() should mark the linked worktree row when it needs merging, got %q", lines[3])
	}
	if !strings.Contains(lines[3], "ready to merge into master") {
		t.Fatalf("renderProjectList() should show merge status in the linked worktree summary, got %q", lines[3])
	}
}

func TestWorktreeVisibilityKeysDoNotHideRows(t *testing.T) {
	rootPath := "/tmp/repo"
	childPath := "/tmp/repo--feat-parallel-lane"
	m := Model{
		focusedPane: focusProjects,
		allProjects: []model.ProjectSummary{
			{
				Name:             "repo",
				Path:             rootPath,
				Status:           model.StatusIdle,
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindMain,
				RepoBranch:       "master",
			},
			{
				Name:                 "repo--feat-parallel-lane",
				Path:                 childPath,
				Status:               model.StatusIdle,
				PresentOnDisk:        true,
				WorktreeRootPath:     rootPath,
				WorktreeKind:         model.WorktreeKindLinked,
				WorktreeParentBranch: "master",
				WorktreeMergeStatus:  model.WorktreeMergeStatusNotMerged,
				RepoBranch:           "feat/parallel-lane",
			},
		},
		sortMode:   sortByAttention,
		visibility: visibilityAllFolders,
	}

	m.rebuildProjectList(rootPath)
	keys := []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune("w")},
		{Type: tea.KeyRunes, Runes: []rune("h")},
		{Type: tea.KeyLeft},
		{Type: tea.KeyRunes, Runes: []rune("l")},
		{Type: tea.KeyRight},
	}
	for _, key := range keys {
		next, cmd := m.updateNormalMode(key)
		if cmd != nil {
			t.Fatalf("updateNormalMode(%q) returned an unexpected command", key.String())
		}
		m = next.(Model)
		if len(m.projects) != 2 || m.projects[0].Path != rootPath || m.projects[1].Path != childPath {
			t.Fatalf("updateNormalMode(%q) hid worktree rows: %#v", key.String(), m.projects)
		}
	}
}

func TestRenderProjectListKeepsVisibleWorktreeFamilyWhenChildMatchesPrivacyPattern(t *testing.T) {
	rootPath := "/tmp/LittleControlRoom"
	childPath := "/tmp/LittleControlRoom--make-test-failures"
	m := Model{
		allProjects: []model.ProjectSummary{
			{
				Name:             "LittleControlRoom",
				Path:             rootPath,
				Status:           model.StatusIdle,
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindMain,
				RepoBranch:       "feat/worktree-ux",
			},
			{
				Name:             "LittleControlRoom--make-test-failures",
				Path:             childPath,
				Status:           model.StatusIdle,
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindLinked,
				RepoBranch:       "tests/make-test-failures",
			},
		},
		sortMode:        sortByAttention,
		visibility:      visibilityAllFolders,
		privacyMode:     true,
		privacyPatterns: []string{"*test*"},
	}

	m.rebuildProjectList(rootPath)
	rendered := ansi.Strip(m.renderProjectList(140, 8))
	lines := strings.Split(rendered, "\n")
	if len(lines) != 4 {
		t.Fatalf("renderProjectList() expected tabs, header, root, and linked worktree rows, got %q", rendered)
	}
	if !strings.Contains(lines[2], "[1 linked]") {
		t.Fatalf("renderProjectList() should keep linked lanes visible under a visible root, got %q", lines[2])
	}
	if !strings.Contains(lines[3], "↳ tests/") {
		t.Fatalf("renderProjectList() should always show the privacy-matched linked lane under its visible root, got %q", lines[3])
	}
}
