package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"lcroom/internal/browserctl"
	"lcroom/internal/codexapp"
	"lcroom/internal/codexcli"
	"lcroom/internal/model"
	"lcroom/internal/procinspect"
	"lcroom/internal/projectrun"
	"lcroom/internal/service"
	"strings"
	"testing"
	"time"
)

func TestFormatListActivityTime(t *testing.T) {
	loc := time.FixedZone("JST", 9*60*60)
	now := time.Date(2026, 3, 6, 11, 32, 0, 0, loc)

	tests := []struct {
		name     string
		activity time.Time
		want     string
	}{
		{
			name:     "zero time",
			activity: time.Time{},
			want:     "Never",
		},
		{
			name:     "same day uses clock time",
			activity: time.Date(2026, 3, 6, 9, 5, 0, 0, loc),
			want:     "09:05",
		},
		{
			name:     "previous calendar day uses yesterday",
			activity: time.Date(2026, 3, 5, 23, 59, 0, 0, loc),
			want:     "Yesterday",
		},
		{
			name:     "recent days use day count",
			activity: time.Date(2026, 3, 4, 18, 0, 0, 0, loc),
			want:     "2 days",
		},
		{
			name:     "weeks use week count",
			activity: time.Date(2026, 2, 20, 10, 0, 0, 0, loc),
			want:     "2 weeks",
		},
		{
			name:     "older activity uses months",
			activity: time.Date(2026, 1, 31, 10, 0, 0, 0, loc),
			want:     "1 month",
		},
		{
			name:     "future timestamps keep full time",
			activity: time.Date(2026, 3, 7, 8, 45, 0, 0, loc),
			want:     "2026-03-07 08:45",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatListActivityTime(now, tt.activity); got != tt.want {
				t.Fatalf("formatListActivityTime() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFormatRunningDuration(t *testing.T) {
	tests := []struct {
		name string
		in   time.Duration
		want string
	}{
		{name: "seconds", in: 45 * time.Second, want: "00:45"},
		{name: "minutes and seconds", in: 12*time.Minute + 4*time.Second, want: "12:04"},
		{name: "hours minutes seconds", in: time.Hour + 3*time.Minute + 5*time.Second, want: "1:03:05"},
		{name: "days and hours", in: 26 * time.Hour, want: "1d 02h"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatRunningDuration(tt.in); got != tt.want {
				t.Fatalf("formatRunningDuration(%s) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestCodexFooterStatusShowsBusyTimer(t *testing.T) {
	now := time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC)
	snapshot := codexapp.Snapshot{
		Busy:      true,
		BusySince: now.Add(-12 * time.Minute),
		Status:    "Codex is working...",
	}

	if got := codexFooterStatus(snapshot, now); got != "Working 12:00" {
		t.Fatalf("codexFooterStatus() = %q, want %q", got, "Working 12:00")
	}
}

func TestCodexFooterStatusShowsTimerWhileBusyExternal(t *testing.T) {
	now := time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC)
	snapshot := codexapp.Snapshot{
		Busy:         true,
		BusyExternal: true,
		BusySince:    now.Add(-12 * time.Minute),
		Status:       "Embedded Codex session 019cccc3 is already active in another Codex process.",
	}

	if got := codexFooterStatus(snapshot, now); got != "Working 12:00" {
		t.Fatalf("codexFooterStatus() = %q, want %q", got, "Working 12:00")
	}
}

func TestCodexFooterStatusShowsFinishingState(t *testing.T) {
	now := time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC)
	snapshot := codexapp.Snapshot{
		Phase:     codexapp.SessionPhaseFinishing,
		Busy:      true,
		BusySince: now.Add(-12 * time.Minute),
		Status:    "Turn completed",
	}

	if got := codexFooterStatus(snapshot, now); got != "Finishing 12:00" {
		t.Fatalf("codexFooterStatus() = %q, want %q", got, "Finishing 12:00")
	}
}

func TestCodexFooterStatusShowsReconcilingState(t *testing.T) {
	snapshot := codexapp.Snapshot{
		Phase:  codexapp.SessionPhaseReconciling,
		Busy:   true,
		Status: "Codex is working...",
	}

	if got := codexFooterStatus(snapshot, time.Now()); got != "Rechecking turn; /reconnect if stuck" {
		t.Fatalf("codexFooterStatus() = %q, want %q", got, "Rechecking turn; /reconnect if stuck")
	}
}

func TestCodexFooterStatusShowsCompactingState(t *testing.T) {
	snapshot := codexapp.Snapshot{
		Phase:  codexapp.SessionPhaseReconciling,
		Status: "Compacting conversation history...",
	}

	if got := codexFooterStatus(snapshot, time.Now()); got != "Compacting conversation" {
		t.Fatalf("codexFooterStatus() = %q, want %q", got, "Compacting conversation")
	}
}

func TestCodexFooterStatusShowsStalledState(t *testing.T) {
	snapshot := codexapp.Snapshot{
		Phase:  codexapp.SessionPhaseStalled,
		Busy:   true,
		Status: "Embedded Codex session seems stuck or disconnected. Use /reconnect.",
	}

	if got := codexFooterStatus(snapshot, time.Now()); got != "Stalled; use /reconnect" {
		t.Fatalf("codexFooterStatus() = %q, want %q", got, "Stalled; use /reconnect")
	}
}

func TestCodexFooterStatusNormalizesLegacyCompletedCopy(t *testing.T) {
	snapshot := codexapp.Snapshot{Status: "Codex turn completed"}

	if got := codexFooterStatus(snapshot, time.Now()); got != "Turn completed" {
		t.Fatalf("codexFooterStatus() = %q, want %q", got, "Turn completed")
	}
}

func TestPickerSummaryForFinishingLiveSnapshot(t *testing.T) {
	snapshot := codexapp.Snapshot{
		Phase:  codexapp.SessionPhaseFinishing,
		Busy:   true,
		Status: "Turn completed",
	}

	if got := pickerSummaryForLiveSnapshot(snapshot); got != "Finishing: waiting for trailing output" {
		t.Fatalf("pickerSummaryForLiveSnapshot() = %q, want finishing summary", got)
	}
}

func TestPickerSummaryForCompactingLiveSnapshot(t *testing.T) {
	snapshot := codexapp.Snapshot{
		Phase:  codexapp.SessionPhaseReconciling,
		Status: "Compacting conversation history...",
	}

	if got := pickerSummaryForLiveSnapshot(snapshot); got != "Compacting: waiting for conversation history to settle" {
		t.Fatalf("pickerSummaryForLiveSnapshot() = %q, want compacting summary", got)
	}
}

func TestPickerSummaryForStalledLiveSnapshot(t *testing.T) {
	snapshot := codexapp.Snapshot{
		Phase:  codexapp.SessionPhaseStalled,
		Busy:   true,
		Status: "Embedded Codex session seems stuck or disconnected. Use /reconnect.",
	}

	if got := pickerSummaryForLiveSnapshot(snapshot); got != "Live now: embedded helper looks stuck; use /reconnect" {
		t.Fatalf("pickerSummaryForLiveSnapshot() = %q, want stalled summary", got)
	}
}

func TestProjectListStatus(t *testing.T) {
	project := model.ProjectSummary{Status: model.StatusPossiblyStuck, PresentOnDisk: true}
	if got := projectListStatus(project); got != "stuck" {
		t.Fatalf("projectListStatus() = %q, want %q", got, "stuck")
	}
}

func TestProjectListStatusUsesAssessmentCategory(t *testing.T) {
	project := model.ProjectSummary{
		Status:                          model.StatusIdle,
		PresentOnDisk:                   true,
		LatestSessionClassification:     model.ClassificationCompleted,
		LatestSessionClassificationType: model.SessionCategoryWaitingForUser,
	}
	if got := projectListStatus(project); got != "waiting" {
		t.Fatalf("projectListStatus() = %q, want %q", got, "waiting")
	}
}

func TestAssessmentFlashHighlightsListCells(t *testing.T) {
	prevProfile := lipgloss.ColorProfile()
	prevDarkBackground := lipgloss.HasDarkBackground()
	lipgloss.SetColorProfile(termenv.ANSI256)
	lipgloss.SetHasDarkBackground(true)
	t.Cleanup(func() {
		lipgloss.SetColorProfile(prevProfile)
		lipgloss.SetHasDarkBackground(prevDarkBackground)
	})

	now := time.Date(2026, 3, 17, 12, 0, 0, 0, time.UTC)
	project := model.ProjectSummary{
		Path:                             "/tmp/demo",
		Status:                           model.StatusIdle,
		PresentOnDisk:                    true,
		LatestSessionClassification:      model.ClassificationCompleted,
		LatestSessionClassificationType:  model.SessionCategoryNeedsFollowUp,
		LatestSessionSummary:             "A concrete next step still remains.",
		LatestSessionFormat:              "modern",
		LatestSessionDetectedProjectPath: "/tmp/demo",
	}

	plain := Model{nowFn: func() time.Time { return now }}
	flashing := Model{
		nowFn:                func() time.Time { return now },
		assessmentFlashUntil: map[string]time.Time{project.Path: now.Add(assessmentFlashDuration)},
	}

	plainRendered := plain.projectListAssessmentStatusStyle(project).Render(projectListStatus(project))
	flashRendered := flashing.projectListAssessmentStatusStyle(project).Render(projectListStatus(project))
	if ansi.Strip(plainRendered) != ansi.Strip(flashRendered) {
		t.Fatalf("assessment flash should keep the same visible text: %q vs %q", ansi.Strip(plainRendered), ansi.Strip(flashRendered))
	}
	if plainRendered == flashRendered {
		t.Fatalf("assessment flash should change the ANSI styling")
	}
}

func TestProjectAssessmentUnreadTracksSeenTimestamp(t *testing.T) {
	project := model.ProjectSummary{
		LatestSessionClassification:     model.ClassificationCompleted,
		LatestSessionClassificationType: model.SessionCategoryCompleted,
		LatestSessionSummary:            "Work appears complete for now.",
		LatestSessionFormat:             "modern",
		LatestSessionLastEventAt:        time.Date(2026, 4, 3, 10, 0, 0, 0, time.UTC),
		LatestTurnStateKnown:            true,
		LatestTurnCompleted:             true,
	}

	if !projectAssessmentUnread(project, time.Time{}, 0) {
		t.Fatalf("expected assessment to be unread before it has been seen")
	}

	project.LastSessionSeenAt = project.LatestSessionLastEventAt
	if projectAssessmentUnread(project, time.Time{}, 0) {
		t.Fatalf("expected assessment to be read once seen_at reaches the completed turn")
	}
}

func TestAssessmentDisplayStyleDimsReadAssessments(t *testing.T) {
	prevProfile := lipgloss.ColorProfile()
	prevDarkBackground := lipgloss.HasDarkBackground()
	lipgloss.SetColorProfile(termenv.ANSI256)
	lipgloss.SetHasDarkBackground(true)
	t.Cleanup(func() {
		lipgloss.SetColorProfile(prevProfile)
		lipgloss.SetHasDarkBackground(prevDarkBackground)
	})

	project := model.ProjectSummary{
		LatestSessionClassification:     model.ClassificationCompleted,
		LatestSessionClassificationType: model.SessionCategoryWaitingForUser,
		LatestSessionSummary:            "Waiting on your review.",
		LatestSessionFormat:             "modern",
		LatestSessionLastEventAt:        time.Date(2026, 4, 3, 10, 0, 0, 0, time.UTC),
		LatestTurnStateKnown:            true,
		LatestTurnCompleted:             true,
	}

	unreadRendered := assessmentDisplayStyle(project, time.Time{}, 0).Render(projectAssessmentLabelWithThreshold(project, time.Time{}, 0))

	project.LastSessionSeenAt = project.LatestSessionLastEventAt
	readRendered := assessmentDisplayStyle(project, time.Time{}, 0).Render(projectAssessmentLabelWithThreshold(project, time.Time{}, 0))

	if ansi.Strip(unreadRendered) != ansi.Strip(readRendered) {
		t.Fatalf("read/unread assessment text should stay the same: %q vs %q", ansi.Strip(unreadRendered), ansi.Strip(readRendered))
	}
	if unreadRendered == readRendered {
		t.Fatalf("expected unread assessment styling to differ from the read styling")
	}
}

func TestApprovalPulseHighlightsProjectListRow(t *testing.T) {
	prevProfile := lipgloss.ColorProfile()
	prevDarkBackground := lipgloss.HasDarkBackground()
	lipgloss.SetColorProfile(termenv.ANSI256)
	lipgloss.SetHasDarkBackground(true)
	t.Cleanup(func() {
		lipgloss.SetColorProfile(prevProfile)
		lipgloss.SetHasDarkBackground(prevDarkBackground)
	})

	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Provider: codexapp.ProviderOpenCode,
			Started:  true,
			Status:   "Waiting for approval",
			PendingApproval: &codexapp.ApprovalRequest{
				Kind:    codexapp.ApprovalCommandExecution,
				Command: "git commit -m test",
			},
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Provider:    codexapp.ProviderOpenCode,
		Preset:      codexcli.PresetYolo,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	project := model.ProjectSummary{
		Path:                             "/tmp/demo",
		Name:                             "demo",
		Status:                           model.StatusIdle,
		PresentOnDisk:                    true,
		LatestSessionClassification:      model.ClassificationCompleted,
		LatestSessionClassificationType:  model.SessionCategoryWaitingForUser,
		LatestSessionSummary:             "Approval needed",
		LatestSessionFormat:              "opencode_db",
		LatestSessionDetectedProjectPath: "/tmp/demo",
	}

	plain := Model{
		nowFn:        func() time.Time { return time.Date(2026, 3, 17, 12, 0, 0, 0, time.UTC) },
		projects:     []model.ProjectSummary{project},
		codexManager: manager,
		width:        120,
		height:       10,
		spinnerFrame: 1,
	}
	flashing := plain
	flashing.spinnerFrame = 0

	plainRendered := plain.renderProjectList(120, 6)
	flashRendered := flashing.renderProjectList(120, 6)
	if ansi.Strip(plainRendered) != ansi.Strip(flashRendered) {
		t.Fatalf("approval pulse should keep the same visible text: %q vs %q", ansi.Strip(plainRendered), ansi.Strip(flashRendered))
	}
	if plainRendered == flashRendered {
		t.Fatalf("approval pulse should change the ANSI styling")
	}
}

func TestBrowserAttentionHighlightsProjectListRow(t *testing.T) {
	prevProfile := lipgloss.ColorProfile()
	prevDarkBackground := lipgloss.HasDarkBackground()
	lipgloss.SetColorProfile(termenv.ANSI256)
	lipgloss.SetHasDarkBackground(true)
	t.Cleanup(func() {
		lipgloss.SetColorProfile(prevProfile)
		lipgloss.SetHasDarkBackground(prevDarkBackground)
	})

	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Provider: codexapp.ProviderCodex,
			Started:  true,
			Status:   "Waiting for browser input",
			BrowserActivity: browserctl.SessionActivity{
				Policy:     settingsAutomaticPlaywrightPolicy,
				State:      browserctl.SessionActivityStateWaitingForUser,
				ServerName: "playwright",
				ToolName:   "browser_navigate",
			},
			ManagedBrowserSessionKey: "managed-demo",
			CurrentBrowserPageURL:    "https://example.test/login",
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Provider:    codexapp.ProviderCodex,
		Preset:      codexcli.PresetYolo,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	project := model.ProjectSummary{
		Path:                             "/tmp/demo",
		Name:                             "demo",
		Status:                           model.StatusIdle,
		PresentOnDisk:                    true,
		LatestSessionClassification:      model.ClassificationCompleted,
		LatestSessionClassificationType:  model.SessionCategoryWaitingForUser,
		LatestSessionSummary:             "Waiting",
		LatestSessionFormat:              "modern",
		LatestSessionDetectedProjectPath: "/tmp/demo",
	}

	plain := Model{
		nowFn:        func() time.Time { return time.Date(2026, 3, 17, 12, 0, 0, 0, time.UTC) },
		projects:     []model.ProjectSummary{project},
		codexManager: manager,
		managedBrowserStates: map[string]browserctl.ManagedPlaywrightState{
			"managed-demo": {SessionKey: "managed-demo", BrowserPID: 123, UpdatedAt: time.Date(2026, 3, 17, 12, 0, 0, 0, time.UTC)},
		},
		width:        140,
		height:       10,
		spinnerFrame: 1,
	}
	flashing := plain
	flashing.spinnerFrame = 0

	plainRendered := plain.renderProjectList(140, 6)
	flashRendered := flashing.renderProjectList(140, 6)
	stripped := ansi.Strip(plainRendered)
	if !strings.Contains(stripped, "browser") {
		t.Fatalf("browser attention row should show browser status: %q", stripped)
	}
	if !strings.Contains(stripped, "browser: playwright/browser_navigate") {
		t.Fatalf("browser attention row should name the browser source: %q", stripped)
	}
	if ansi.Strip(plainRendered) != ansi.Strip(flashRendered) {
		t.Fatalf("browser pulse should keep the same visible text: %q vs %q", ansi.Strip(plainRendered), ansi.Strip(flashRendered))
	}
	if plainRendered == flashRendered {
		t.Fatalf("browser pulse should change the ANSI styling")
	}
}

func TestProjectTabsMarkActionableCategoryAttention(t *testing.T) {
	m := Model{
		allProjects: []model.ProjectSummary{
			{
				Path:                             "/tmp/main-done",
				Name:                             "main-done",
				PresentOnDisk:                    true,
				LatestSessionClassification:      model.ClassificationCompleted,
				LatestSessionClassificationType:  model.SessionCategoryCompleted,
				LatestSessionSummary:             "Done but unread.",
				LatestSessionFormat:              "modern",
				LatestSessionLastEventAt:         time.Date(2026, 4, 3, 10, 0, 0, 0, time.UTC),
				LatestSessionDetectedProjectPath: "/tmp/main-done",
				LatestTurnStateKnown:             true,
				LatestTurnCompleted:              true,
			},
			{
				Path:                             "/tmp/client-followup",
				Name:                             "client-followup",
				CategoryID:                       "cat_client",
				CategoryName:                     "Client",
				PresentOnDisk:                    true,
				LatestSessionClassification:      model.ClassificationCompleted,
				LatestSessionClassificationType:  model.SessionCategoryNeedsFollowUp,
				LatestSessionSummary:             "A concrete follow-up remains.",
				LatestSessionFormat:              "modern",
				LatestSessionDetectedProjectPath: "/tmp/client-followup",
			},
		},
		projectCategories: []model.ProjectCategory{
			{ID: "cat_client", Name: "Client"},
			{ID: "cat_ops", Name: "Ops"},
		},
		openAgentTasks: []model.AgentTask{{
			ID:         "task_waiting",
			Title:      "Needs review",
			Status:     model.AgentTaskStatusWaiting,
			CategoryID: "cat_ops",
		}},
	}

	rendered := ansi.Strip(m.renderProjectArchiveTabs(120))
	for _, want := range []string{"[Main 1]", " Client* 1 ", " Ops* 1 ", " Archived 0 "} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("renderProjectArchiveTabs() missing %q in %q", want, rendered)
		}
	}
	if strings.Contains(rendered, "Main*") {
		t.Fatalf("completed unread project should not mark Main tab: %q", rendered)
	}
}

func TestProjectTabsDoNotMarkArchivedAttention(t *testing.T) {
	m := Model{
		archivedProjects: []model.ProjectSummary{{
			Path:                             "/tmp/archived-followup",
			Name:                             "archived-followup",
			Archived:                         true,
			PresentOnDisk:                    true,
			LatestSessionClassification:      model.ClassificationCompleted,
			LatestSessionClassificationType:  model.SessionCategoryNeedsFollowUp,
			LatestSessionSummary:             "A concrete follow-up remains.",
			LatestSessionFormat:              "modern",
			LatestSessionDetectedProjectPath: "/tmp/archived-followup",
		}},
	}

	rendered := ansi.Strip(m.renderProjectArchiveTabs(120))
	if !strings.Contains(rendered, " Archived 1 ") {
		t.Fatalf("renderProjectArchiveTabs() should count archived projects, got %q", rendered)
	}
	if strings.Contains(rendered, "Archived*") {
		t.Fatalf("archived tab should not show attention marker: %q", rendered)
	}
}

func TestProjectTabsMarkLiveBrowserAttention(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/browser-demo",
		snapshot: codexapp.Snapshot{
			Provider: codexapp.ProviderCodex,
			Started:  true,
			BrowserActivity: browserctl.SessionActivity{
				Policy:     settingsAutomaticPlaywrightPolicy,
				State:      browserctl.SessionActivityStateWaitingForUser,
				ServerName: "playwright",
				ToolName:   "browser_navigate",
			},
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/browser-demo",
		Provider:    codexapp.ProviderCodex,
		Preset:      codexcli.PresetYolo,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	m := Model{
		allProjects: []model.ProjectSummary{{
			Path:          "/tmp/browser-demo",
			Name:          "browser-demo",
			CategoryID:    "cat_client",
			CategoryName:  "Client",
			PresentOnDisk: true,
		}},
		projectCategories: []model.ProjectCategory{{ID: "cat_client", Name: "Client"}},
		codexManager:      manager,
	}

	rendered := ansi.Strip(m.renderProjectArchiveTabs(120))
	if !strings.Contains(rendered, " Client* 1 ") {
		t.Fatalf("live browser wait should mark category tab, got %q", rendered)
	}
}

func TestProjectDisplayStatusClearsMovedWhenLatestSessionIsInNewPath(t *testing.T) {
	now := time.Now().UTC()
	project := model.ProjectSummary{
		Path:                             "/new",
		Status:                           model.StatusIdle,
		PresentOnDisk:                    true,
		MovedAt:                          now,
		LatestSessionDetectedProjectPath: "/new",
	}
	if got := projectDisplayStatus(project); got != "idle" {
		t.Fatalf("projectDisplayStatus() = %q, want %q", got, "idle")
	}

	project.LatestSessionDetectedProjectPath = "/old"
	if got := projectDisplayStatus(project); got != "moved" {
		t.Fatalf("projectDisplayStatus() = %q, want %q", got, "moved")
	}
}

func TestProjectDisplayStatusUsesProjectStatusEvenWithAssessment(t *testing.T) {
	project := model.ProjectSummary{
		Status:                          model.StatusIdle,
		PresentOnDisk:                   true,
		LatestSessionClassification:     model.ClassificationCompleted,
		LatestSessionClassificationType: model.SessionCategoryNeedsFollowUp,
	}
	if got := projectDisplayStatus(project); got != "idle" {
		t.Fatalf("projectDisplayStatus() = %q, want %q", got, "idle")
	}
}

func TestAssessmentStatusLabelUsesInProgressName(t *testing.T) {
	project := model.ProjectSummary{
		LatestSessionClassification:     model.ClassificationCompleted,
		LatestSessionClassificationType: model.SessionCategoryInProgress,
	}
	if got, _, ok := assessmentStatusLabel(project, true); !ok || got != "working" {
		t.Fatalf("assessmentStatusLabel(compact) = (%q, %v), want (%q, true)", got, ok, "working")
	}
}

func TestAssessmentStatusLabelAtMarksStaleInProgressBlocked(t *testing.T) {
	project := model.ProjectSummary{
		LatestSessionClassification:     model.ClassificationCompleted,
		LatestSessionClassificationType: model.SessionCategoryInProgress,
		LatestSessionLastEventAt:        time.Date(2026, 3, 29, 6, 54, 44, 0, time.UTC),
		LatestTurnStateKnown:            true,
		LatestTurnCompleted:             false,
	}

	if got, _, ok := assessmentStatusLabelAt(project, true, time.Date(2026, 3, 29, 8, 0, 0, 0, time.UTC), 30*time.Minute); !ok || got != "blocked" {
		t.Fatalf("assessmentStatusLabelAt(compact) = (%q, %v), want (%q, true)", got, ok, "blocked")
	}
}

func TestProjectDisplayStatusShowsMissingWhenFolderGone(t *testing.T) {
	project := model.ProjectSummary{
		Path:          "/gone",
		Status:        model.StatusPossiblyStuck,
		PresentOnDisk: false,
	}
	if got := projectDisplayStatus(project); got != "missing" {
		t.Fatalf("projectDisplayStatus() = %q, want %q", got, "missing")
	}
	if got := projectListStatus(project); got != "missing" {
		t.Fatalf("projectListStatus() = %q, want %q", got, "missing")
	}
}

func TestProjectAttentionLabel(t *testing.T) {
	project := model.ProjectSummary{AttentionScore: 95, RepoDirty: true}
	if got := projectAttentionLabel(project); got != "  95" {
		t.Fatalf("projectAttentionLabel() = %q, want %q", got, "  95")
	}

	project.Pinned = true
	if got := projectAttentionLabel(project); got != "  95" {
		t.Fatalf("projectAttentionLabel() should ignore pinned rows in the list label, got %q", got)
	}

	project = model.ProjectSummary{AttentionScore: 100, RepoSyncStatus: model.RepoSyncAhead}
	if got := projectAttentionLabel(project); got != " 100" {
		t.Fatalf("projectAttentionLabel() = %q, want %q", got, " 100")
	}

	project = model.ProjectSummary{AttentionScore: 0}
	if got := projectAttentionLabel(project); got != "   0" {
		t.Fatalf("projectAttentionLabel() = %q, want %q", got, "   0")
	}
}

func TestProjectRepoWarningIndicator(t *testing.T) {
	prevProfile := lipgloss.ColorProfile()
	prevDarkBackground := lipgloss.HasDarkBackground()
	lipgloss.SetColorProfile(termenv.ANSI256)
	lipgloss.SetHasDarkBackground(true)
	t.Cleanup(func() {
		lipgloss.SetColorProfile(prevProfile)
		lipgloss.SetHasDarkBackground(prevDarkBackground)
	})

	m := Model{}

	conflictPulseA := m.projectRepoWarningIndicator(model.ProjectSummary{RepoConflict: true, RepoDirty: true}, 0)
	conflictPulseB := m.projectRepoWarningIndicator(model.ProjectSummary{RepoConflict: true, RepoDirty: true}, 1)
	if !strings.Contains(conflictPulseA, "!") || !strings.Contains(conflictPulseB, "!") {
		t.Fatalf("conflict warning should contain '!', got %q / %q", conflictPulseA, conflictPulseB)
	}
	if conflictPulseA == conflictPulseB {
		t.Fatalf("conflict warning should animate across spinner frames")
	}

	// Dirty worktree → styled "!"
	dirtyIndicator := m.projectRepoWarningIndicator(model.ProjectSummary{RepoDirty: true}, 0)
	if !strings.Contains(dirtyIndicator, "!") {
		t.Fatalf("dirty worktree should contain '!', got %q", dirtyIndicator)
	}

	// Sync-only warning → styled "!"
	syncIndicator := m.projectRepoWarningIndicator(model.ProjectSummary{RepoSyncStatus: model.RepoSyncAhead}, 0)
	if !strings.Contains(syncIndicator, "!") {
		t.Fatalf("sync warning should contain '!', got %q", syncIndicator)
	}

	submoduleIndicator := m.projectRepoWarningIndicator(model.ProjectSummary{RepoSubmoduleUnpushedCount: 1}, 0)
	if !strings.Contains(submoduleIndicator, "!") {
		t.Fatalf("submodule warning should contain '!', got %q", submoduleIndicator)
	}

	linkedSyncIndicator := m.projectRepoWarningIndicator(model.ProjectSummary{
		WorktreeKind:   model.WorktreeKindLinked,
		RepoSyncStatus: model.RepoSyncAhead,
	}, 0)
	if linkedSyncIndicator != " " {
		t.Fatalf("linked worktree sync-only state should not warn, got %q", linkedSyncIndicator)
	}

	unmergedIndicator := m.projectRepoWarningIndicator(model.ProjectSummary{
		WorktreeKind:        model.WorktreeKindLinked,
		WorktreeMergeStatus: model.WorktreeMergeStatusNotMerged,
	}, 0)
	if !strings.Contains(unmergedIndicator, "M") {
		t.Fatalf("unmerged linked worktree should use a merge marker, got %q", unmergedIndicator)
	}

	rootPath := "/tmp/repo"
	rootUnmergedIndicator := (Model{
		allProjects: []model.ProjectSummary{
			{
				Path:             rootPath,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindMain,
			},
			{
				Path:                "/tmp/repo--lane",
				WorktreeRootPath:    rootPath,
				WorktreeKind:        model.WorktreeKindLinked,
				WorktreeMergeStatus: model.WorktreeMergeStatusNotMerged,
			},
		},
	}).projectRepoWarningIndicator(model.ProjectSummary{
		Path:             rootPath,
		WorktreeRootPath: rootPath,
		WorktreeKind:     model.WorktreeKindMain,
	}, 0)
	if !strings.Contains(rootUnmergedIndicator, "M") {
		t.Fatalf("root row with unmerged linked worktree should use a merge marker, got %q", rootUnmergedIndicator)
	}

	// Dirty + sync → same as dirty-only (dirty takes priority)
	bothIndicator := m.projectRepoWarningIndicator(model.ProjectSummary{RepoDirty: true, RepoSyncStatus: model.RepoSyncBehind}, 0)
	if bothIndicator != dirtyIndicator {
		t.Fatalf("dirty+sync should match dirty-only indicator, got %q vs %q", bothIndicator, dirtyIndicator)
	}

	pendingIndicator := (Model{
		pendingGitSummaries: map[string]string{
			"/tmp/demo": "Committing...",
		},
	}).projectRepoWarningIndicator(model.ProjectSummary{Path: "/tmp/demo", RepoDirty: true}, 0)
	if !strings.Contains(pendingIndicator, "|") {
		t.Fatalf("pending git op should render spinner indicator, got %q", pendingIndicator)
	}
	if strings.Contains(pendingIndicator, "!") {
		t.Fatalf("pending git op should not render dirty warning indicator, got %q", pendingIndicator)
	}

	orphanedIndicator := (Model{
		orphanedWorktreesByRoot: map[string][]model.ProjectSummary{
			"/tmp/repo": {{
				Path:             "/tmp/repo--stale-lane",
				PresentOnDisk:    true,
				Forgotten:        true,
				WorktreeRootPath: "/tmp/repo",
				WorktreeKind:     model.WorktreeKindLinked,
			}},
		},
	}).projectRepoWarningIndicator(model.ProjectSummary{
		Path:             "/tmp/repo",
		PresentOnDisk:    true,
		WorktreeRootPath: "/tmp/repo",
		WorktreeKind:     model.WorktreeKindMain,
	}, 0)
	if !strings.Contains(orphanedIndicator, "~") {
		t.Fatalf("orphaned linked checkout should use a distinct warning marker on the root row, got %q", orphanedIndicator)
	}

	// No warning → space
	if got := m.projectRepoWarningIndicator(model.ProjectSummary{}, 0); got != " " {
		t.Fatalf("no warning should return space, got %q", got)
	}
}

func TestBuildProjectRowsDoesNotTreatRepoSubdirectoryAsLinkedWorktree(t *testing.T) {
	rootPath := "/tmp/repo"
	derivedPath := "/tmp/repo/runs/001-demo"
	linkedPath := "/tmp/repo--feature"
	m := Model{
		allProjects: []model.ProjectSummary{
			{
				Path:             rootPath,
				Name:             "repo",
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindMain,
				Status:           model.StatusIdle,
			},
			{
				Path:             derivedPath,
				Name:             "001-demo",
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindMain,
				Status:           model.StatusIdle,
			},
			{
				Path:             linkedPath,
				Name:             "repo--feature",
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindLinked,
				Status:           model.StatusIdle,
			},
		},
	}

	rows, meta := m.buildProjectRows(m.allProjects, "")
	if len(rows) != 2 || len(meta) != 2 {
		t.Fatalf("rows/meta lengths = %d/%d, want 2/2; rows=%#v meta=%#v", len(rows), len(meta), rows, meta)
	}
	if rows[0].Path != rootPath || meta[0].Kind != projectListRowRepo || meta[0].LinkedCount != 1 {
		t.Fatalf("root row/meta = %#v/%#v, want repo row with exactly one linked worktree", rows[0], meta[0])
	}
	if rows[1].Path != derivedPath || meta[1].Kind != projectListRowStandalone {
		t.Fatalf("derived row/meta = %#v/%#v, want standalone subdirectory row", rows[1], meta[1])
	}

	family := m.worktreeFamily(rootPath)
	if len(family) != 2 {
		t.Fatalf("worktree family = %#v, want root + linked only", family)
	}
	for _, project := range family {
		if project.Path == derivedPath {
			t.Fatalf("derived subdirectory should not appear in worktree family: %#v", family)
		}
	}
}

func TestProjectAttentionScoreAddsRunningRuntimeWeight(t *testing.T) {
	now := time.Date(2026, 3, 20, 10, 0, 0, 0, time.UTC)
	project := model.ProjectSummary{
		Name:           "demo",
		Path:           "/tmp/demo",
		AttentionScore: 7,
	}
	m := Model{
		nowFn: func() time.Time { return now },
		runtimeSnapshots: map[string]projectrun.Snapshot{
			project.Path: {
				ProjectPath: project.Path,
				Running:     true,
				StartedAt:   now.Add(-5 * time.Minute),
				Ports:       []int{3000},
			},
		},
	}

	if got := m.projectAttentionScore(project); got != project.AttentionScore+runtimeRunningAttentionWeight {
		t.Fatalf("projectAttentionScore() = %d, want %d", got, project.AttentionScore+runtimeRunningAttentionWeight)
	}

	reasons := m.projectAttentionReasons(project, nil)
	if len(reasons) != 1 {
		t.Fatalf("projectAttentionReasons() len = %d, want 1", len(reasons))
	}
	if reasons[0].Code != "runtime_running" {
		t.Fatalf("reason code = %q, want runtime_running", reasons[0].Code)
	}
	if reasons[0].Weight != runtimeRunningAttentionWeight {
		t.Fatalf("reason weight = %d, want %d", reasons[0].Weight, runtimeRunningAttentionWeight)
	}
	if !strings.Contains(reasons[0].Text, "Managed runtime running for 05:00") {
		t.Fatalf("reason text = %q, want running duration", reasons[0].Text)
	}
	if !strings.Contains(reasons[0].Text, "3000") {
		t.Fatalf("reason text = %q, want port summary", reasons[0].Text)
	}
}

func TestProjectAttentionScoreAddsProcessWarningWeight(t *testing.T) {
	project := model.ProjectSummary{
		Name:           "demo",
		Path:           "/tmp/demo",
		AttentionScore: 7,
	}
	m := Model{
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

	if got := m.projectAttentionScore(project); got != project.AttentionScore+processHotCPUAttentionWeight {
		t.Fatalf("projectAttentionScore() = %d, want hot process boost", got)
	}
	reasons := m.projectAttentionReasons(project, nil)
	if len(reasons) != 1 {
		t.Fatalf("projectAttentionReasons() len = %d, want 1", len(reasons))
	}
	if reasons[0].Code != "process_suspicious" {
		t.Fatalf("reason code = %q, want process_suspicious", reasons[0].Code)
	}
	if reasons[0].Weight != processHotCPUAttentionWeight {
		t.Fatalf("reason weight = %d, want %d", reasons[0].Weight, processHotCPUAttentionWeight)
	}
	if !strings.Contains(reasons[0].Text, "hot CPU") || !strings.Contains(reasons[0].Text, "orphaned") {
		t.Fatalf("reason text should summarize process risk, got %q", reasons[0].Text)
	}
}

func TestProjectAttentionScoreUsesBrowserAttentionBeforeGenericQuestion(t *testing.T) {
	project := model.ProjectSummary{
		Name:           "demo",
		Path:           "/tmp/demo",
		AttentionScore: 7,
	}
	session := &fakeCodexSession{
		projectPath: project.Path,
		snapshot: codexapp.Snapshot{
			Provider: codexapp.ProviderCodex,
			Started:  true,
			BrowserActivity: browserctl.SessionActivity{
				Policy:     settingsAutomaticPlaywrightPolicy,
				State:      browserctl.SessionActivityStateWaitingForUser,
				ServerName: "playwright",
				ToolName:   "browser_navigate",
			},
			ManagedBrowserSessionKey: "managed-demo",
			CurrentBrowserPageURL:    "https://example.test/login",
			PendingElicitation: &codexapp.ElicitationRequest{
				ID:         "elicitation-demo",
				ServerName: "playwright",
				Mode:       codexapp.ElicitationModeURL,
				Message:    "Finish the browser login.",
				URL:        "https://example.test/login",
			},
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: project.Path,
		Provider:    codexapp.ProviderCodex,
		Preset:      codexcli.PresetYolo,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	m := Model{
		nowFn:        func() time.Time { return time.Date(2026, 3, 17, 12, 0, 0, 0, time.UTC) },
		codexManager: manager,
		managedBrowserStates: map[string]browserctl.ManagedPlaywrightState{
			"managed-demo": {SessionKey: "managed-demo", BrowserPID: 123, UpdatedAt: time.Date(2026, 3, 17, 12, 0, 0, 0, time.UTC)},
		},
	}
	if got := m.projectAttentionScore(project); got != project.AttentionScore+embeddedBrowserAttentionWeight {
		t.Fatalf("projectAttentionScore() = %d, want browser-only attention boost", got)
	}

	reasons := m.projectAttentionReasons(project, nil)
	if len(reasons) != 1 {
		t.Fatalf("projectAttentionReasons() len = %d, want 1", len(reasons))
	}
	if reasons[0].Code != "embedded_browser_attention" {
		t.Fatalf("reason code = %q, want embedded_browser_attention", reasons[0].Code)
	}
	if reasons[0].Weight != embeddedBrowserAttentionWeight {
		t.Fatalf("reason weight = %d, want %d", reasons[0].Weight, embeddedBrowserAttentionWeight)
	}
	if !strings.Contains(reasons[0].Text, "Codex browser needs attention") {
		t.Fatalf("reason text = %q, want browser attention copy", reasons[0].Text)
	}
}

func TestProjectAttentionIgnoresStaleManagedBrowserWaitAfterRestart(t *testing.T) {
	project := model.ProjectSummary{
		Name:           "demo",
		Path:           "/tmp/demo",
		AttentionScore: 7,
	}
	snapshot := codexapp.Snapshot{
		Provider:                 codexapp.ProviderLCAgent,
		Started:                  true,
		Busy:                     true,
		Status:                   "Browser waiting for user input",
		ManagedBrowserSessionKey: "managed-demo",
		CurrentBrowserPageURL:    "https://accounts.google.com/",
		BrowserActivity: browserctl.SessionActivity{
			Policy:     settingsAutomaticPlaywrightPolicy,
			State:      browserctl.SessionActivityStateWaitingForUser,
			ServerName: "playwright",
			ToolName:   "browser_wait_for_user",
		},
	}
	session := &fakeCodexSession{projectPath: project.Path, snapshot: snapshot}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: project.Path,
		Provider:    codexapp.ProviderLCAgent,
		Preset:      codexcli.PresetYolo,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	m := Model{
		codexManager: manager,
		codexSnapshots: map[string]codexapp.Snapshot{
			project.Path: snapshot,
		},
	}
	if _, ok := m.projectPendingBrowserAttention(project.Path); ok {
		t.Fatal("stale managed browser wait should not surface project browser attention")
	}
	if got := m.projectAttentionScore(project); got != project.AttentionScore {
		t.Fatalf("projectAttentionScore() = %d, want stale browser wait ignored", got)
	}
	if got := m.footerBrowserAttentionLabel(); got != "" {
		t.Fatalf("footerBrowserAttentionLabel() = %q, want no stale browser wait", got)
	}
}

func TestSortProjectsUsesRunningRuntimeAttentionBoost(t *testing.T) {
	running := model.ProjectSummary{
		Name:           "running",
		Path:           "/tmp/running",
		AttentionScore: 1,
	}
	idle := model.ProjectSummary{
		Name:           "idle",
		Path:           "/tmp/idle",
		AttentionScore: 9,
	}
	projects := []model.ProjectSummary{idle, running}
	m := Model{
		sortMode: sortByAttention,
		runtimeSnapshots: map[string]projectrun.Snapshot{
			running.Path: {
				ProjectPath: running.Path,
				Running:     true,
			},
		},
	}

	m.sortProjects(projects)
	if projects[0].Path != running.Path {
		t.Fatalf("first project path = %q, want %q after runtime boost", projects[0].Path, running.Path)
	}
}

func TestProjectRuntimeSnapshotUsesAsyncCacheOnly(t *testing.T) {
	projectPath := t.TempDir()
	manager := projectrun.NewManager()
	defer func() {
		if err := manager.CloseAll(); err != nil {
			t.Fatalf("CloseAll() error = %v", err)
		}
	}()

	started, err := manager.Start(projectrun.StartRequest{
		ProjectPath: projectPath,
		Command:     "sleep 5",
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if !started.Running {
		t.Fatalf("Start() snapshot should report running runtime")
	}

	m := Model{
		runtimeManager:   manager,
		runtimeSnapshots: make(map[string]projectrun.Snapshot),
	}

	if snapshot := m.projectRuntimeSnapshot(projectPath); snapshot.Running {
		t.Fatalf("projectRuntimeSnapshot() should not consult the live runtime manager on cache miss")
	}

	cmd := m.loadRuntimeSnapshotsCmd()
	if cmd == nil {
		t.Fatalf("loadRuntimeSnapshotsCmd() returned nil")
	}
	updated, followup := m.update(cmd())
	got := updated.(Model)
	if followup != nil {
		t.Fatalf("runtime snapshot refresh should not queue a follow-up without an in-flight refresh")
	}
	if snapshot := got.projectRuntimeSnapshot(projectPath); !snapshot.Running {
		t.Fatalf("projectRuntimeSnapshot() should return the asynchronously refreshed cache entry")
	}
}

func TestRuntimeSnapshotsMsgRebuildsAttentionSortedProjectsWhenRunningStateChanges(t *testing.T) {
	idle := model.ProjectSummary{
		Name:           "idle",
		Path:           "/tmp/runtime-idle",
		AttentionScore: 9,
	}
	running := model.ProjectSummary{
		Name:           "running",
		Path:           "/tmp/runtime-running",
		AttentionScore: 1,
	}

	m := Model{
		allProjects: []model.ProjectSummary{idle, running},
		sortMode:    sortByAttention,
		visibility:  visibilityAllFolders,
		runtimeSnapshots: map[string]projectrun.Snapshot{
			running.Path: {ProjectPath: running.Path},
		},
	}
	m.rebuildProjectList(idle.Path)
	if len(m.projects) != 2 || m.projects[0].Path != idle.Path {
		t.Fatalf("initial project order = %#v, want idle first before runtime refresh", m.projects)
	}

	updated, followup := m.update(runtimeSnapshotsMsg{
		snapshots: []projectrun.Snapshot{
			{ProjectPath: running.Path, Running: true},
		},
	})
	got := updated.(Model)
	if followup != nil {
		t.Fatalf("runtime snapshot update should not queue a follow-up without an in-flight refresh")
	}
	if len(got.projects) != 2 || got.projects[0].Path != running.Path {
		t.Fatalf("project order after runtime refresh = %#v, want running project first", got.projects)
	}
}

func TestProcessScanMsgRebuildsAttentionSortedProjectsWhenWarningsChange(t *testing.T) {
	idle := model.ProjectSummary{
		Name:           "idle",
		Path:           "/tmp/process-idle",
		AttentionScore: 20,
	}
	hot := model.ProjectSummary{
		Name:           "hot",
		Path:           "/tmp/process-hot",
		AttentionScore: 1,
	}
	m := Model{
		allProjects: []model.ProjectSummary{idle, hot},
		sortMode:    sortByAttention,
		visibility:  visibilityAllFolders,
	}
	m.rebuildProjectList(idle.Path)
	if len(m.projects) != 2 || m.projects[0].Path != idle.Path {
		t.Fatalf("initial project order = %#v, want idle first before process scan", m.projects)
	}

	_ = m.applyProcessScanMsg(processScanMsg{
		reports: []procinspect.ProjectReport{{
			ProjectPath: hot.Path,
			Findings: []procinspect.Finding{{
				Process:     procinspect.Process{PID: 49995, PPID: 1, CPU: 99},
				ProjectPath: hot.Path,
			}},
		}},
	})

	if len(m.projects) != 2 || m.projects[0].Path != hot.Path {
		t.Fatalf("project order after process scan = %#v, want hot process project first", m.projects)
	}
}

func TestRenderDetailContentShowsRuntimeAttentionReason(t *testing.T) {
	now := time.Date(2026, 3, 20, 10, 0, 0, 0, time.UTC)
	project := model.ProjectSummary{
		Name:           "demo",
		Path:           "/tmp/demo",
		AttentionScore: 4,
		PresentOnDisk:  true,
	}
	m := Model{
		nowFn: func() time.Time { return now },
		projects: []model.ProjectSummary{
			project,
		},
		allProjects: []model.ProjectSummary{
			project,
		},
		selected: 0,
		detail: model.ProjectDetail{
			Summary: model.ProjectSummary{Path: project.Path},
		},
		runtimeSnapshots: map[string]projectrun.Snapshot{
			project.Path: {
				ProjectPath: project.Path,
				Running:     true,
				StartedAt:   now.Add(-5 * time.Minute),
				Ports:       []int{3000},
			},
		},
	}

	rendered := ansi.Strip(m.renderDetailContent(100))
	if !strings.Contains(rendered, "Attention: 14") {
		t.Fatalf("renderDetailContent() missing boosted attention score: %q", rendered)
	}
	if !strings.Contains(rendered, "[+10] Managed runtime running for 05:00 on 3000") {
		t.Fatalf("renderDetailContent() missing runtime attention reason: %q", rendered)
	}
}

func TestRenderDetailContentShowsBrowserAttention(t *testing.T) {
	project := model.ProjectSummary{
		Name:           "demo",
		Path:           "/tmp/demo",
		AttentionScore: 4,
		PresentOnDisk:  true,
	}
	session := &fakeCodexSession{
		projectPath: project.Path,
		snapshot: codexapp.Snapshot{
			Provider: codexapp.ProviderOpenCode,
			Started:  true,
			BrowserActivity: browserctl.SessionActivity{
				Policy:     settingsAutomaticPlaywrightPolicy,
				State:      browserctl.SessionActivityStateWaitingForUser,
				ServerName: "playwright",
				ToolName:   "browser_navigate",
			},
			ManagedBrowserSessionKey: "managed-demo",
			CurrentBrowserPageURL:    "https://example.test/login",
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: project.Path,
		Provider:    codexapp.ProviderOpenCode,
		Preset:      codexcli.PresetYolo,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	m := Model{
		projects: []model.ProjectSummary{
			project,
		},
		allProjects: []model.ProjectSummary{
			project,
		},
		selected: 0,
		detail: model.ProjectDetail{
			Summary: model.ProjectSummary{Path: project.Path},
		},
		codexManager: manager,
		nowFn:        func() time.Time { return time.Date(2026, 3, 17, 12, 0, 0, 0, time.UTC) },
		managedBrowserStates: map[string]browserctl.ManagedPlaywrightState{
			"managed-demo": {SessionKey: "managed-demo", BrowserPID: 123, UpdatedAt: time.Date(2026, 3, 17, 12, 0, 0, 0, time.UTC)},
		},
	}

	rendered := ansi.Strip(m.renderDetailContent(100))
	if !strings.Contains(rendered, "Browser: playwright/browser_navigate is waiting for user input.") {
		t.Fatalf("renderDetailContent() missing browser attention field: %q", rendered)
	}
	if !strings.Contains(rendered, "Attention: 119") {
		t.Fatalf("renderDetailContent() missing browser attention score: %q", rendered)
	}
	if !strings.Contains(rendered, "[+115] OpenCode browser needs attention") {
		t.Fatalf("renderDetailContent() missing browser attention reason: %q", rendered)
	}
}

func TestFilterProjects(t *testing.T) {
	projects := []model.ProjectSummary{
		{Name: "visible", LastActivity: time.Date(2026, 3, 6, 9, 0, 0, 0, time.UTC)},
		{Name: "hidden"},
	}

	filtered := filterProjects(projects, visibilityAIFolders, nil, "")
	if len(filtered) != 1 || filtered[0].Name != "visible" {
		t.Fatalf("filterProjects(AI folders) = %#v, want only visible project", filtered)
	}

	all := filterProjects(projects, visibilityAllFolders, nil, "")
	if len(all) != 2 {
		t.Fatalf("filterProjects(All folders) len = %d, want 2", len(all))
	}
}

func TestFilterProjectsExcludesConfiguredNamePatterns(t *testing.T) {
	projects := []model.ProjectSummary{
		{Name: "client-demo-03", Path: "/tmp/client-demo-03", LastActivity: time.Date(2026, 3, 6, 9, 0, 0, 0, time.UTC)},
		{Name: "LittleControlRoom", Path: "/tmp/LittleControlRoom", LastActivity: time.Date(2026, 3, 6, 9, 30, 0, 0, time.UTC)},
		{Name: "visible-demo", Path: "/tmp/visible-demo", LastActivity: time.Date(2026, 3, 6, 10, 0, 0, 0, time.UTC)},
	}

	filtered := filterProjects(projects, visibilityAllFolders, []string{"client-*", "*control*"}, "")
	if len(filtered) != 1 || filtered[0].Name != "visible-demo" {
		t.Fatalf("filterProjects() with name excludes = %#v, want only visible-demo", filtered)
	}
}

func TestFilterProjectsByTransientProjectFilter(t *testing.T) {
	projects := []model.ProjectSummary{
		{Name: "LittleControlRoom", Path: "/tmp/LittleControlRoom", LastActivity: time.Date(2026, 3, 6, 9, 0, 0, 0, time.UTC)},
		{Name: "helper-tools", Path: "/tmp/helper-tools", LastActivity: time.Date(2026, 3, 6, 10, 0, 0, 0, time.UTC)},
		{Name: "archive", Path: "/tmp/archive", LastActivity: time.Date(2026, 3, 6, 11, 0, 0, 0, time.UTC)},
	}

	filtered := filterProjects(projects, visibilityAllFolders, nil, "control")
	if len(filtered) != 1 || filtered[0].Name != "LittleControlRoom" {
		t.Fatalf("filterProjects() with transient filter = %#v, want only LittleControlRoom", filtered)
	}

	filtered = filterProjects(projects, visibilityAllFolders, nil, "helper")
	if len(filtered) != 1 || filtered[0].Path != "/tmp/helper-tools" {
		t.Fatalf("filterProjects() with helper filter = %#v, want only /tmp/helper-tools", filtered)
	}

	filtered = filterProjects(projects, visibilityAllFolders, nil, "lcr")
	if len(filtered) != 1 || filtered[0].Name != "LittleControlRoom" {
		t.Fatalf("filterProjects() with fuzzy lcr filter = %#v, want only LittleControlRoom", filtered)
	}
}

func TestRebuildProjectListResetsSelectionWhenHidden(t *testing.T) {
	m := Model{
		allProjects: []model.ProjectSummary{
			{Path: "/visible", Name: "visible", LastActivity: time.Date(2026, 3, 6, 9, 0, 0, 0, time.UTC)},
			{Path: "/hidden", Name: "hidden"},
		},
		sortMode:   sortByAttention,
		visibility: visibilityAIFolders,
		selected:   1,
	}

	m.rebuildProjectList("/hidden")

	if len(m.projects) != 1 {
		t.Fatalf("visible project count = %d, want 1", len(m.projects))
	}
	if m.selected != 0 {
		t.Fatalf("selected index = %d, want 0", m.selected)
	}
	if m.projects[0].Path != "/visible" {
		t.Fatalf("selected project = %q, want %q", m.projects[0].Path, "/visible")
	}
}

func TestFormatTokenCount(t *testing.T) {
	tests := []struct {
		value int64
		want  string
	}{
		{value: 0, want: "0"},
		{value: 999, want: "999"},
		{value: 1200, want: "1.2k"},
		{value: 12500, want: "12k"},
		{value: 1250000, want: "1.2M"},
	}

	for _, tt := range tests {
		if got := formatTokenCount(tt.value); got != tt.want {
			t.Fatalf("formatTokenCount(%d) = %q, want %q", tt.value, got, tt.want)
		}
	}
}

func TestCodexSnapshotTokenUsageLabel(t *testing.T) {
	snapshot := codexapp.Snapshot{
		TokenUsage: &codexapp.TokenUsageSnapshot{
			Total: codexapp.TokenUsageBreakdown{
				InputTokens:           12_345,
				OutputTokens:          6_789,
				CachedInputTokens:     2_000,
				ReasoningOutputTokens: 123,
				TotalTokens:           19_134,
			},
		},
	}
	if got := codexSnapshotTokenUsageLabel(snapshot); got != "i12k c16% r123 o6.8k" {
		t.Fatalf("codexSnapshotTokenUsageLabel() = %q", got)
	}
}

func TestScanCompleteStatusIncludesQueuedClassifications(t *testing.T) {
	report := service.ScanReport{
		UpdatedProjects:       []string{"/tmp/demo"},
		QueuedClassifications: 2,
	}

	got := scanCompleteStatus(report)
	if got != "Scan complete: 1 updated, 2 classifications queued" {
		t.Fatalf("scanCompleteStatus() = %q", got)
	}
}

func TestFitFooterWidth(t *testing.T) {
	if got := fitFooterWidth("abcdefghij", 7); got != "abcd..." {
		t.Fatalf("fitFooterWidth() = %q, want %q", got, "abcd...")
	}
	if got := fitFooterWidth("abc", 2); got != "ab" {
		t.Fatalf("fitFooterWidth() narrow = %q, want %q", got, "ab")
	}
}

func TestRenderFooterColorsKeyHints(t *testing.T) {
	prevProfile := lipgloss.ColorProfile()
	prevDarkBackground := lipgloss.HasDarkBackground()
	lipgloss.SetColorProfile(termenv.ANSI256)
	lipgloss.SetHasDarkBackground(true)
	t.Cleanup(func() {
		lipgloss.SetColorProfile(prevProfile)
		lipgloss.SetHasDarkBackground(prevDarkBackground)
	})

	m := Model{focusedPane: focusProjects}

	rendered := m.renderFooter(160)
	if rendered == ansi.Strip(rendered) {
		t.Fatalf("renderFooter() should apply ANSI styling to key hints: %q", rendered)
	}
	stripped := ansi.Strip(rendered)
	if !strings.Contains(stripped, "Enter Codex") || !strings.Contains(stripped, "q quit") {
		t.Fatalf("renderFooter() missing expected key hints: %q", stripped)
	}
}

func TestRenderFooterHiddenCodexPrefersEnterOverAltUpRestore(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Started: true,
			Preset:  codexcli.PresetYolo,
			Status:  "Codex session ready",
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

	m := Model{
		codexManager:       manager,
		codexHiddenProject: "/tmp/demo",
		focusedPane:        focusProjects,
	}

	rendered := ansi.Strip(m.renderFooter(160))
	if !strings.Contains(rendered, "Enter Codex") {
		t.Fatalf("renderFooter() should keep Enter as the main Codex action: %q", rendered)
	}
	if strings.Contains(rendered, "Alt+Up restore") {
		t.Fatalf("renderFooter() should not advertise Alt+Up restore in the main view: %q", rendered)
	}
}

func TestRenderFooterListOmitsMoveAndRemoveHints(t *testing.T) {
	m := Model{focusedPane: focusProjects}

	rendered := ansi.Strip(m.renderFooter(160))
	if strings.Contains(rendered, "↑/↓ move") {
		t.Fatalf("renderFooter() should not advertise obvious arrow movement in the main list footer: %q", rendered)
	}
	if strings.Contains(rendered, "/remove remove") {
		t.Fatalf("renderFooter() should not advertise remove without a missing project selected: %q", rendered)
	}
	if strings.Contains(rendered, "r refresh") {
		t.Fatalf("renderFooter() should not advertise refresh in the main list footer: %q", rendered)
	}
}

func TestRenderFooterShowsResolveHintForMergeConflict(t *testing.T) {
	m := Model{
		focusedPane: focusProjects,
		projects: []model.ProjectSummary{{
			Name:          "demo",
			Path:          "/tmp/demo",
			PresentOnDisk: true,
			RepoConflict:  true,
		}},
		selected: 0,
	}

	rendered := ansi.Strip(m.renderFooter(160))
	if !strings.Contains(rendered, "/resolve resolve") {
		t.Fatalf("renderFooter() missing /resolve action for merge conflict: %q", rendered)
	}
}

func TestRenderFooterShowsRemoveHintForMissingProject(t *testing.T) {
	m := Model{
		focusedPane: focusProjects,
		projects: []model.ProjectSummary{{
			Name:          "demo",
			Path:          "/tmp/demo",
			PresentOnDisk: false,
		}},
		selected: 0,
	}

	rendered := ansi.Strip(m.renderFooter(160))
	if !strings.Contains(rendered, "/remove remove") {
		t.Fatalf("renderFooter() missing /remove action for missing project: %q", rendered)
	}
}

func TestProjectRemoveHotkeyDoesNotOpenRegularProjectRemoval(t *testing.T) {
	m := Model{
		projects: []model.ProjectSummary{{
			Name:          "demo",
			Path:          "/tmp/demo",
			PresentOnDisk: true,
		}},
		selected: 0,
	}

	updated, cmd := m.updateNormalMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("x on a regular project should not schedule work")
	}
	if got.projectRemoveConfirm != nil {
		t.Fatalf("x on a regular project should not open the project removal dialog")
	}
	if got.status != "Select an agent task or linked worktree to archive or remove it" {
		t.Fatalf("status = %q, want scoped x action guidance", got.status)
	}
}

func TestRenderFooterShowsScratchTaskActionHint(t *testing.T) {
	m := Model{
		focusedPane: focusProjects,
		projects: []model.ProjectSummary{{
			Name:          "answer Sarah",
			Path:          "/tmp/tasks/answer-sarah",
			Kind:          model.ProjectKindScratchTask,
			PresentOnDisk: true,
		}},
		selected: 0,
	}

	rendered := ansi.Strip(m.renderFooter(160))
	if !strings.Contains(rendered, "d task") {
		t.Fatalf("renderFooter() should advertise the scratch task action shortcut, got %q", rendered)
	}
}

func TestRenderFooterDetailOmitsScrollHint(t *testing.T) {
	m := Model{focusedPane: focusDetail}

	rendered := ansi.Strip(m.renderFooter(160))
	if strings.Contains(rendered, "↑/↓ scroll") {
		t.Fatalf("renderFooter() should not advertise detail scrolling in the footer: %q", rendered)
	}
}
