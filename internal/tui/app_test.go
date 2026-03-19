package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lcroom/internal/aibackend"
	"lcroom/internal/brand"
	"lcroom/internal/codexapp"
	"lcroom/internal/codexcli"
	"lcroom/internal/commands"
	"lcroom/internal/config"
	"lcroom/internal/events"
	"lcroom/internal/model"
	"lcroom/internal/projectrun"
	"lcroom/internal/scanner"
	"lcroom/internal/service"
	"lcroom/internal/store"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

type fakeCodexSession struct {
	projectPath   string
	snapshot      codexapp.Snapshot
	snapshotCalls int
	submitted     []string
	submissions   []codexapp.Submission
	decisions     []codexapp.ApprovalDecision
	toolAnswers   []map[string][]string
	elicitations  []fakeElicitationResponse
	statusCalls   int
	interrupted   bool
	refreshCalls  int
	refreshBusyFn func(*fakeCodexSession) error
	models        []codexapp.ModelOption
	modelStages   []struct {
		Model     string
		Reasoning string
	}
}

type fakeElicitationResponse struct {
	decision codexapp.ElicitationDecision
	content  json.RawMessage
}

type usageSnapshotClassifier struct {
	usage model.LLMSessionUsage
}

func (c *usageSnapshotClassifier) QueueProject(context.Context, model.ProjectState) (bool, error) {
	return true, nil
}

func (c *usageSnapshotClassifier) Notify()               {}
func (c *usageSnapshotClassifier) Start(context.Context) {}
func (c *usageSnapshotClassifier) UsageSnapshot() model.LLMSessionUsage {
	return c.usage
}

func (s *fakeCodexSession) ProjectPath() string {
	return s.projectPath
}

func (s *fakeCodexSession) Snapshot() codexapp.Snapshot {
	s.snapshotCalls++
	snapshot := s.snapshot
	snapshot.ProjectPath = s.projectPath
	return snapshot
}

func (s *fakeCodexSession) Submit(prompt string) error {
	s.submitted = append(s.submitted, prompt)
	return nil
}

func (s *fakeCodexSession) SubmitInput(input codexapp.Submission) error {
	s.submissions = append(s.submissions, input)
	s.submitted = append(s.submitted, input.TranscriptText())
	return nil
}

func (s *fakeCodexSession) ShowStatus() error {
	s.statusCalls++
	s.snapshot.Entries = append(s.snapshot.Entries, codexapp.TranscriptEntry{
		Kind: codexapp.TranscriptStatus,
		Text: strings.Join([]string{
			"Embedded Codex status",
			"thread: " + s.snapshot.ThreadID,
			"model: gpt-5.4",
			"reasoning effort: high",
			"usage window: limit=Codex; window=5h; left=85; resetsAt=1773027840",
		}, "\n"),
	})
	return nil
}

func (s *fakeCodexSession) ListModels() ([]codexapp.ModelOption, error) {
	if len(s.models) == 0 {
		return []codexapp.ModelOption{{
			ID:          "gpt-5",
			Model:       "gpt-5",
			DisplayName: "GPT-5",
			Description: "Default embedded Codex model",
			IsDefault:   true,
			SupportedReasoningEfforts: []codexapp.ReasoningEffortOption{
				{ReasoningEffort: "medium", Description: "Balanced"},
				{ReasoningEffort: "high", Description: "More deliberate"},
			},
			DefaultReasoningEffort: "medium",
		}}, nil
	}
	return append([]codexapp.ModelOption(nil), s.models...), nil
}

func (s *fakeCodexSession) StageModelOverride(model, reasoningEffort string) error {
	s.modelStages = append(s.modelStages, struct {
		Model     string
		Reasoning string
	}{
		Model:     model,
		Reasoning: reasoningEffort,
	})
	s.snapshot.PendingModel = model
	s.snapshot.PendingReasoning = reasoningEffort
	return nil
}

func (s *fakeCodexSession) Interrupt() error {
	s.interrupted = true
	return nil
}

func (s *fakeCodexSession) RespondApproval(decision codexapp.ApprovalDecision) error {
	s.decisions = append(s.decisions, decision)
	return nil
}

func (s *fakeCodexSession) RespondToolInput(answers map[string][]string) error {
	s.toolAnswers = append(s.toolAnswers, answers)
	return nil
}

func (s *fakeCodexSession) RespondElicitation(decision codexapp.ElicitationDecision, content json.RawMessage) error {
	s.elicitations = append(s.elicitations, fakeElicitationResponse{
		decision: decision,
		content:  append(json.RawMessage(nil), content...),
	})
	return nil
}

func (s *fakeCodexSession) Close() error {
	s.snapshot.Closed = true
	return nil
}

func (s *fakeCodexSession) RefreshBusyElsewhere() error {
	s.refreshCalls++
	if s.refreshBusyFn != nil {
		return s.refreshBusyFn(s)
	}
	return nil
}

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

	if got := codexFooterStatus(snapshot, time.Now()); got != "Rechecking turn status" {
		t.Fatalf("codexFooterStatus() = %q, want %q", got, "Rechecking turn status")
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
	if got := projectAttentionLabel(project); got != "!  95" {
		t.Fatalf("projectAttentionLabel() = %q, want %q", got, "!  95")
	}

	project.Pinned = true
	if got := projectAttentionLabel(project); got != "!  95" {
		t.Fatalf("projectAttentionLabel() should ignore pinned rows in the list label, got %q", got)
	}

	project = model.ProjectSummary{AttentionScore: 100, RepoSyncStatus: model.RepoSyncAhead}
	if got := projectAttentionLabel(project); got != "! 100" {
		t.Fatalf("projectAttentionLabel() = %q, want %q", got, "! 100")
	}

	project = model.ProjectSummary{AttentionScore: 0}
	if got := projectAttentionLabel(project); got != "    0" {
		t.Fatalf("projectAttentionLabel() = %q, want %q", got, "    0")
	}
}

func TestFilterProjects(t *testing.T) {
	projects := []model.ProjectSummary{
		{Name: "visible", LastActivity: time.Date(2026, 3, 6, 9, 0, 0, 0, time.UTC)},
		{Name: "hidden"},
	}

	filtered := filterProjects(projects, visibilityAIFolders, nil)
	if len(filtered) != 1 || filtered[0].Name != "visible" {
		t.Fatalf("filterProjects(AI folders) = %#v, want only visible project", filtered)
	}

	all := filterProjects(projects, visibilityAllFolders, nil)
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

	filtered := filterProjects(projects, visibilityAllFolders, []string{"client-*", "*control*"})
	if len(filtered) != 1 || filtered[0].Name != "visible-demo" {
		t.Fatalf("filterProjects() with name excludes = %#v, want only visible-demo", filtered)
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

func TestRenderFooterListOmitsMoveAndForgetHints(t *testing.T) {
	m := Model{focusedPane: focusProjects}

	rendered := ansi.Strip(m.renderFooter(160))
	if strings.Contains(rendered, "↑/↓ move") {
		t.Fatalf("renderFooter() should not advertise obvious arrow movement in the main list footer: %q", rendered)
	}
	if strings.Contains(rendered, "f forget") {
		t.Fatalf("renderFooter() should not advertise forget in the main list footer: %q", rendered)
	}
	if strings.Contains(rendered, "r refresh") {
		t.Fatalf("renderFooter() should not advertise refresh in the main list footer: %q", rendered)
	}
}

func TestRenderFooterDetailOmitsScrollHint(t *testing.T) {
	m := Model{focusedPane: focusDetail}

	rendered := ansi.Strip(m.renderFooter(160))
	if strings.Contains(rendered, "↑/↓ scroll") {
		t.Fatalf("renderFooter() should not advertise detail scrolling in the footer: %q", rendered)
	}
}

func TestCompactUsageLabel(t *testing.T) {
	if got := compactUsageLabel(model.LLMSessionUsage{}); got != "cost off" {
		t.Fatalf("compactUsageLabel(disabled) = %q, want %q", got, "cost off")
	}

	usage := model.LLMSessionUsage{
		Enabled: true,
		Model:   "gpt-5-mini",
		Totals: model.LLMUsage{
			InputTokens:  345,
			OutputTokens: 538,
		},
	}
	if got := compactUsageLabel(usage); got != "cost $0.0012" {
		t.Fatalf("compactUsageLabel(enabled) = %q, want %q", got, "cost $0.0012")
	}
}

func TestCompactLocalUsageLabel(t *testing.T) {
	if got := compactLocalUsageLabel("Codex", model.LLMSessionUsage{}); got != "Codex ready" {
		t.Fatalf("compactLocalUsageLabel(ready) = %q, want %q", got, "Codex ready")
	}

	usage := model.LLMSessionUsage{Started: 1, Completed: 1}
	if got := compactLocalUsageLabel("Codex", usage); got != "Codex 1 call" {
		t.Fatalf("compactLocalUsageLabel(one call) = %q, want %q", got, "Codex 1 call")
	}
}

func TestFooterUsageLabelShowsLocalBackendActivity(t *testing.T) {
	m := Model{
		setupChecked: true,
		setupSnapshot: aibackend.Snapshot{
			Selected: config.AIBackendCodex,
			Codex: aibackend.Status{
				Backend:       config.AIBackendCodex,
				Label:         "Codex",
				Installed:     true,
				Authenticated: true,
				Ready:         true,
				Detail:        "Logged in with ChatGPT.",
			},
		},
	}

	if got := m.footerUsageLabel(); got != "Codex ready" {
		t.Fatalf("footerUsageLabel() = %q, want %q", got, "Codex ready")
	}
}

func TestFooterUsageLabelShowsUnavailableBackend(t *testing.T) {
	m := Model{
		setupChecked: true,
		setupSnapshot: aibackend.Snapshot{
			Selected: config.AIBackendOpenAIAPI,
			OpenAIAPI: aibackend.Status{
				Backend: config.AIBackendOpenAIAPI,
				Label:   "OpenAI API key",
				Detail:  "No saved OpenAI API key.",
			},
		},
	}

	if got := m.footerUsageLabel(); got != "AI unavailable" {
		t.Fatalf("footerUsageLabel() = %q, want AI unavailable", got)
	}
}

func TestRenderTopStatusLineShowsUnavailableBackendNotice(t *testing.T) {
	m := Model{
		status:       "Ready",
		setupChecked: true,
		setupSnapshot: aibackend.Snapshot{
			Selected: config.AIBackendOpenAIAPI,
			OpenAIAPI: aibackend.Status{
				Backend: config.AIBackendOpenAIAPI,
				Label:   "OpenAI API key",
				Detail:  "No saved OpenAI API key.",
			},
		},
	}

	rendered := ansi.Strip(m.renderTopStatusLine(160))
	if !strings.Contains(rendered, "AI unavailable (use /setup)") {
		t.Fatalf("top status line missing backend warning: %q", rendered)
	}
	if strings.Contains(rendered, "OpenAI API key") {
		t.Fatalf("top status line should keep the warning generic, got %q", rendered)
	}
}

func TestRenderAIBackendStatusNoticeUsesWarningBadge(t *testing.T) {
	t.Parallel()

	prevProfile := lipgloss.ColorProfile()
	prevDarkBackground := lipgloss.HasDarkBackground()
	lipgloss.SetColorProfile(termenv.ANSI256)
	lipgloss.SetHasDarkBackground(true)
	t.Cleanup(func() {
		lipgloss.SetColorProfile(prevProfile)
		lipgloss.SetHasDarkBackground(prevDarkBackground)
	})

	m := Model{
		setupChecked: true,
		setupSnapshot: aibackend.Snapshot{
			Selected: config.AIBackendOpenAIAPI,
			OpenAIAPI: aibackend.Status{
				Backend: config.AIBackendOpenAIAPI,
				Label:   "OpenAI API key",
				Detail:  "No saved OpenAI API key.",
			},
		},
	}

	rendered := m.renderAIBackendStatusNotice()
	if got := strings.TrimSpace(ansi.Strip(rendered)); got != "AI unavailable (use /setup)" {
		t.Fatalf("renderAIBackendStatusNotice() = %q, want %q", got, "AI unavailable (use /setup)")
	}
	if !strings.Contains(rendered, "\x1b[") {
		t.Fatalf("renderAIBackendStatusNotice() should render a styled badge, got %q", rendered)
	}
}

func TestSetupSnapshotUnavailableKeepsExistingStatus(t *testing.T) {
	m := Model{
		status: "Ready",
	}

	updated, cmd := m.Update(setupSnapshotMsg{
		snapshot: aibackend.Snapshot{
			Selected: config.AIBackendOpenAIAPI,
			OpenAIAPI: aibackend.Status{
				Backend: config.AIBackendOpenAIAPI,
				Label:   "OpenAI API key",
				Detail:  "No saved OpenAI API key.",
			},
		},
	})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("setupSnapshotMsg should not return a command")
	}
	if got.status != "Ready" {
		t.Fatalf("status = %q, want existing status to be preserved", got.status)
	}
}

func TestRenderFooterPulsesWhenUsageIncreases(t *testing.T) {
	t.Parallel()

	prevProfile := lipgloss.ColorProfile()
	prevDarkBackground := lipgloss.HasDarkBackground()
	lipgloss.SetColorProfile(termenv.ANSI256)
	lipgloss.SetHasDarkBackground(true)
	t.Cleanup(func() {
		lipgloss.SetColorProfile(prevProfile)
		lipgloss.SetHasDarkBackground(prevDarkBackground)
	})

	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	classifier := &usageSnapshotClassifier{
		usage: model.LLMSessionUsage{
			Enabled: true,
			Model:   "gpt-5-mini",
			Totals: model.LLMUsage{
				InputTokens:      345_000,
				OutputTokens:     538_000,
				EstimatedCostUSD: 1.16225,
			},
		},
	}
	svc := service.New(config.Default(), st, events.NewBus(), nil)
	svc.SetSessionClassifier(classifier)

	now := time.Date(2026, 3, 17, 12, 0, 0, 0, time.UTC)
	m := New(ctx, svc)
	m.nowFn = func() time.Time { return now }
	m.refreshUsagePulse()

	steady := m.renderFooter(160)
	if !strings.Contains(ansi.Strip(steady), "cost $1.16") {
		t.Fatalf("steady footer missing cost label: %q", steady)
	}

	classifier.usage.Totals.InputTokens = 360_000
	classifier.usage.Totals.OutputTokens = 544_000
	classifier.usage.Totals.EstimatedCostUSD = 1.178
	m.spinnerFrame = 0
	m.refreshUsagePulse()
	pulseA := m.renderFooter(160)

	m.spinnerFrame = 1
	pulseB := m.renderFooter(160)

	if ansi.Strip(pulseA) != ansi.Strip(pulseB) {
		t.Fatalf("usage pulse should keep the same visible text: %q vs %q", ansi.Strip(pulseA), ansi.Strip(pulseB))
	}
	if pulseA == pulseB {
		t.Fatalf("usage pulse should animate across spinner frames")
	}
	if pulseA == ansi.Strip(pulseA) {
		t.Fatalf("usage pulse should use ANSI styling: %q", pulseA)
	}

	now = now.Add(usagePulseDuration + 50*time.Millisecond)
	settled := m.renderFooter(160)
	if settled == pulseA || settled == pulseB {
		t.Fatalf("usage pulse should expire after the pulse window")
	}
}

func TestSessionCategoryLabel(t *testing.T) {
	if got := sessionCategoryLabel(model.SessionCategoryNeedsFollowUp); got != "followup" {
		t.Fatalf("sessionCategoryLabel(needs_follow_up) = %q, want %q", got, "followup")
	}
	if got := sessionCategoryLabel(model.SessionCategoryWaitingForUser); got != "waiting" {
		t.Fatalf("sessionCategoryLabel(waiting_for_user) = %q, want %q", got, "waiting")
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

func TestProjectAssessmentTextUsesFallbackStates(t *testing.T) {
	project := model.ProjectSummary{
		LatestSessionClassification: model.ClassificationRunning,
	}
	if got := projectAssessmentText(project); got != "-" {
		t.Fatalf("projectAssessmentText(running) = %q, want %q", got, "-")
	}

	project = model.ProjectSummary{
		LatestSessionClassification:               model.ClassificationRunning,
		LatestSessionClassificationStage:          model.ClassificationStageWaitingForModel,
		LatestSessionClassificationStageStartedAt: time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC),
		LatestCompletedSessionClassificationType:  model.SessionCategoryNeedsFollowUp,
		LatestCompletedSessionSummary:             "A concrete next step still remains.",
	}
	if got := projectAssessmentTextAt(project, time.Date(2026, 3, 10, 12, 0, 37, 0, time.UTC)); got != "A concrete next step still remains." {
		t.Fatalf("projectAssessmentTextAt(refreshing) = %q, want completed summary", got)
	}

	project = model.ProjectSummary{
		LatestSessionFormat: "modern",
	}
	if got := projectAssessmentText(project); got != "not assessed yet" {
		t.Fatalf("projectAssessmentText(unassessed) = %q, want %q", got, "not assessed yet")
	}
}

func TestProjectListStatusUsesLastCompletedAssessmentWhileRefreshRuns(t *testing.T) {
	project := model.ProjectSummary{
		Status:                                   model.StatusIdle,
		PresentOnDisk:                            true,
		LatestSessionFormat:                      "modern",
		LatestSessionClassification:              model.ClassificationRunning,
		LatestSessionClassificationStage:         model.ClassificationStageWaitingForModel,
		LatestCompletedSessionClassificationType: model.SessionCategoryWaitingForUser,
		LatestCompletedSessionSummary:            "Waiting on a design decision before coding resumes.",
	}

	if got := projectListStatus(project); got != "waiting" {
		t.Fatalf("projectListStatus(refreshing) = %q, want %q", got, "waiting")
	}
	if got := projectAssessmentText(project); got != project.LatestCompletedSessionSummary {
		t.Fatalf("projectAssessmentText(refreshing) = %q, want completed summary", got)
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

func TestRenderProjectListHighlightsSelectedRow(t *testing.T) {
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
	if len(lines) != 3 {
		t.Fatalf("renderProjectList() expected header plus two rows, got %q", rendered)
	}
	if strings.Contains(lines[1], "\x1b[48;5;236m") {
		t.Fatalf("renderProjectList() should not highlight unselected rows: %q", lines[1])
	}
	if !strings.Contains(lines[2], "\x1b[48;5;236m") {
		t.Fatalf("renderProjectList() should apply a background highlight to the selected row: %q", lines[2])
	}
	if got := strings.Count(lines[2], "\x1b[48;5;236m"); got < 4 {
		t.Fatalf("renderProjectList() should carry the selected-row background across styled cells, got %d matches in %q", got, lines[2])
	}
	if stripped := ansi.Strip(lines[2]); !strings.Contains(stripped, "selected") {
		t.Fatalf("renderProjectList() should preserve the selected row text, got %q", stripped)
	}
	if got := ansi.StringWidth(ansi.Strip(lines[2])); got > 80 {
		t.Fatalf("renderProjectList() selected row width = %d, want <= 80: %q", got, ansi.Strip(lines[2]))
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
	if len(lines) < 2 {
		t.Fatalf("renderProjectList() expected header plus one row, got %q", rendered)
	}
	if !strings.Contains(lines[1], "!  95") {
		t.Fatalf("renderProjectList() should show repo warnings in ATTN, got %q", lines[1])
	}
	if strings.Contains(lines[1], "demo project !") {
		t.Fatalf("renderProjectList() should keep the project name free of suffix markers, got %q", lines[1])
	}
}

func TestRenderProjectListShowsNoteIndicator(t *testing.T) {
	m := Model{
		projects: []model.ProjectSummary{{
			Name:          "alpha",
			Path:          "/tmp/alpha",
			Status:        model.StatusIdle,
			PresentOnDisk: true,
			Note:          "Follow up on the handoff",
		}},
		selected:   0,
		sortMode:   sortByAttention,
		visibility: visibilityAIFolders,
	}

	rendered := ansi.Strip(m.renderProjectList(80, 6))
	lines := strings.Split(rendered, "\n")
	if len(lines) < 2 {
		t.Fatalf("renderProjectList() expected header plus one row, got %q", rendered)
	}
	if !strings.Contains(lines[0], "AGENT") || !strings.Contains(lines[0], "N RUN") {
		t.Fatalf("renderProjectList() missing agent/note/run headers, got %q", lines[0])
	}
	fields := strings.Fields(lines[1])
	foundIndicator := false
	for _, field := range fields {
		if field == "*" {
			foundIndicator = true
			break
		}
	}
	if !foundIndicator {
		t.Fatalf("renderProjectList() should show the note indicator in the row, got %q", lines[1])
	}
}

func TestRenderDetailContentKeepsRuntimeInSeparatePane(t *testing.T) {
	m := Model{
		projects: []model.ProjectSummary{{
			Name:          "demo",
			Path:          "/tmp/demo",
			Status:        model.StatusIdle,
			PresentOnDisk: true,
			RunCommand:    "pnpm dev",
		}},
		selected: 0,
	}

	rendered := ansi.Strip(m.renderDetailContent(72))
	if strings.Contains(rendered, "Run cmd:") || strings.Contains(rendered, "Runtime:") {
		t.Fatalf("renderDetailContent() should leave runtime fields to the runtime pane: %q", rendered)
	}
}

func TestRuntimePaneShowsRuntimeOutputAndActions(t *testing.T) {
	dir := t.TempDir()
	manager := projectrun.NewManager()
	defer func() { _ = manager.CloseAll() }()

	_, err := manager.Start(projectrun.StartRequest{
		ProjectPath: dir,
		Command:     "printf 'ready on http://127.0.0.1:4310/\\nwarming up\\n'; sleep 2",
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	waitForRuntimeSnapshot(t, manager, dir, func(snapshot projectrun.Snapshot) bool {
		return len(snapshot.RecentOutput) >= 2 && len(snapshot.AnnouncedURLs) >= 1
	})

	m := Model{
		width:  100,
		height: 28,
		projects: []model.ProjectSummary{{
			Name:          "demo",
			Path:          dir,
			PresentOnDisk: true,
			RunCommand:    "pnpm dev",
		}},
		selected:       0,
		runtimeManager: manager,
	}
	if cmd := m.openRuntimeInspectorForSelection(); cmd != nil {
		t.Fatalf("openRuntimeInspectorForSelection() should not return a command, got %T", cmd)
	}

	rendered := ansi.Strip(m.View())
	if !strings.Contains(rendered, "Runtime - demo") {
		t.Fatalf("View() should show the runtime pane title: %q", rendered)
	}
	if !strings.Contains(rendered, "ready on http://127.0.0.1:4310/") || !strings.Contains(rendered, "warming up") {
		t.Fatalf("View() should show runtime output in the runtime pane: %q", rendered)
	}
	if !strings.Contains(rendered, "Open URL") || !strings.Contains(rendered, "Restart") || !strings.Contains(rendered, "Stop") {
		t.Fatalf("View() should show runtime pane actions: %q", rendered)
	}
	if !strings.Contains(rendered, "Focus: runtime") {
		t.Fatalf("View() should show runtime focus in the footer: %q", rendered)
	}
}

func TestRenderRuntimePaneShowsControlRoomFlairWhenEmpty(t *testing.T) {
	m := Model{
		projects: []model.ProjectSummary{{
			Name:          "demo",
			Path:          "/tmp/demo",
			Status:        model.StatusIdle,
			PresentOnDisk: true,
		}},
		allProjects: []model.ProjectSummary{{
			Name:          "demo",
			Path:          "/tmp/demo",
			Status:        model.StatusIdle,
			PresentOnDisk: true,
		}},
		selected: 0,
	}

	rendered := m.renderRuntimePanel(41, 10)
	stripped := ansi.Strip(rendered)
	if !strings.Contains(stripped, "Control Room - demo") {
		t.Fatalf("renderRuntimePanel() should show the control room header: %q", stripped)
	}
	if !strings.Contains(stripped, "Standby. Use /run or /run-edit.") {
		t.Fatalf("renderRuntimePanel() should show the wake-room hint: %q", stripped)
	}
	if strings.Contains(stripped, "Output") {
		t.Fatalf("renderRuntimePanel() should use the dedicated flair layout instead of the generic output box: %q", stripped)
	}
	if !strings.Contains(rendered, "\x1b[38;2;") {
		t.Fatalf("renderRuntimePanel() should use truecolor pixel styling for the idle scene: %q", rendered)
	}
	if !strings.Contains(rendered, "\u2580") && !strings.Contains(rendered, "\u2584") && !strings.Contains(rendered, "\u2588") {
		t.Fatalf("renderRuntimePanel() should include pixel block glyphs in the idle scene: %q", rendered)
	}
}

func TestRenderRuntimePaneControlRoomFlairAnimates(t *testing.T) {
	base := Model{
		projects: []model.ProjectSummary{{
			Name:          "demo",
			Path:          "/tmp/demo",
			Status:        model.StatusIdle,
			PresentOnDisk: true,
		}},
		allProjects: []model.ProjectSummary{{
			Name:          "demo",
			Path:          "/tmp/demo",
			Status:        model.StatusIdle,
			PresentOnDisk: true,
		}},
		selected: 0,
	}

	renderedA := base.renderRuntimePanel(41, 10)
	base.spinnerFrame = 12
	renderedB := base.renderRuntimePanel(41, 10)

	if renderedA == renderedB {
		t.Fatalf("renderRuntimePanel() should animate the control room scene across spinner frames")
	}
	linesA := strings.Split(ansi.Strip(renderedA), "\n")
	linesB := strings.Split(ansi.Strip(renderedB), "\n")
	if len(linesA) != len(linesB) {
		t.Fatalf("control room render line count changed across frames: %d vs %d", len(linesA), len(linesB))
	}
	if linesA[0] != linesB[0] {
		t.Fatalf("control room header should stay stable while animating: %q vs %q", linesA[0], linesB[0])
	}
	if linesA[len(linesA)-1] != linesB[len(linesB)-1] {
		t.Fatalf("control room footer should stay stable while animating: %q vs %q", linesA[len(linesA)-1], linesB[len(linesB)-1])
	}
}

func TestRenderRuntimePaneFallsBackToTextWhenTooNarrowForFlair(t *testing.T) {
	m := Model{
		projects: []model.ProjectSummary{{
			Name:          "demo",
			Path:          "/tmp/demo",
			Status:        model.StatusIdle,
			PresentOnDisk: true,
		}},
		allProjects: []model.ProjectSummary{{
			Name:          "demo",
			Path:          "/tmp/demo",
			Status:        model.StatusIdle,
			PresentOnDisk: true,
		}},
		selected: 0,
	}

	rendered := ansi.Strip(m.renderRuntimePanel(22, 8))
	if !strings.Contains(rendered, "Runtime - demo") {
		t.Fatalf("renderRuntimePanel() should fall back to the standard runtime summary when the pane is too narrow: %q", rendered)
	}
	if !strings.Contains(rendered, "Use /run, /start, or /") {
		t.Fatalf("renderRuntimePanel() should keep the original empty-runtime guidance when flair is unavailable: %q", rendered)
	}
	if strings.Contains(rendered, "Control Room - demo") {
		t.Fatalf("renderRuntimePanel() should not force the control room flair into a cramped pane: %q", rendered)
	}
}

func waitForRuntimeSnapshot(t *testing.T, manager *projectrun.Manager, projectPath string, ready func(projectrun.Snapshot) bool) projectrun.Snapshot {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := projectrun.WaitUntilRunning(ctx, manager, projectPath); err != nil {
		t.Fatalf("WaitUntilRunning() error = %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		snapshot, err := manager.Snapshot(projectPath)
		if err != nil {
			t.Fatalf("Snapshot() error = %v", err)
		}
		if ready(snapshot) {
			return snapshot
		}
		time.Sleep(50 * time.Millisecond)
	}

	snapshot, err := manager.Snapshot(projectPath)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	t.Fatalf("runtime snapshot never reached expected state: %+v", snapshot)
	return projectrun.Snapshot{}
}

func waitForRuntimeStopped(t *testing.T, manager *projectrun.Manager, projectPath string) projectrun.Snapshot {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		snapshot, err := manager.Snapshot(projectPath)
		if err != nil {
			t.Fatalf("Snapshot() error = %v", err)
		}
		if !snapshot.Running {
			return snapshot
		}
		time.Sleep(50 * time.Millisecond)
	}

	snapshot, err := manager.Snapshot(projectPath)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	t.Fatalf("runtime did not stop: %+v", snapshot)
	return projectrun.Snapshot{}
}

func TestRenderDetailContentShowsNotesSection(t *testing.T) {
	m := Model{
		projects: []model.ProjectSummary{{
			Name:          "demo",
			Path:          "/tmp/demo",
			Status:        model.StatusIdle,
			PresentOnDisk: true,
			Note:          "Line one\nLine two",
		}},
		selected: 0,
	}

	rendered := ansi.Strip(m.renderDetailContent(60))
	if !strings.Contains(rendered, "Notes") {
		t.Fatalf("renderDetailContent() should include a Notes section: %q", rendered)
	}
	if !strings.Contains(rendered, "Line one") || !strings.Contains(rendered, "Line two") {
		t.Fatalf("renderDetailContent() should render multiline notes: %q", rendered)
	}
}

func TestViewStacksListAndDetailVertically(t *testing.T) {
	m := Model{
		projects: []model.ProjectSummary{{
			Name:                             "demo",
			Path:                             "/tmp/demo",
			Status:                           model.StatusIdle,
			PresentOnDisk:                    true,
			RepoDirty:                        true,
			RepoSyncStatus:                   model.RepoSyncAhead,
			RepoAheadCount:                   2,
			LatestSessionClassification:      model.ClassificationCompleted,
			LatestSessionClassificationType:  model.SessionCategoryCompleted,
			LatestSessionSummary:             "Work appears complete for now.",
			LatestSessionFormat:              "modern",
			LatestSessionDetectedProjectPath: "/tmp/demo",
		}},
		selected: 0,
		detail: model.ProjectDetail{
			LatestSessionClassification: &model.SessionClassification{
				Status:   model.ClassificationCompleted,
				Category: model.SessionCategoryCompleted,
				Summary:  "Work appears complete for now.",
			},
		},
		width:  100,
		height: 24,
	}

	rendered := m.View()
	if !strings.Contains(rendered, "╯\n╭") {
		t.Fatalf("View() should stack list and detail panes vertically: %q", rendered)
	}
	if got := len(strings.Split(rendered, "\n")); got != m.height {
		t.Fatalf("View() line count = %d, want terminal height %d; render was %q", got, m.height, rendered)
	}
	if !strings.Contains(rendered, "Repo: dirty worktree") || !strings.Contains(rendered, "Remote: ahead by 2") {
		t.Fatalf("View() should keep both repo and remote details visible in the detail pane: %q", rendered)
	}
	if !strings.Contains(rendered, "Runtime - demo") && !strings.Contains(rendered, "Control Room - demo") {
		t.Fatalf("View() should render the runtime pane beside the detail pane: %q", rendered)
	}
	for _, line := range strings.Split(ansi.Strip(rendered), "\n") {
		if ansi.StringWidth(line) > m.width {
			t.Fatalf("View() line width = %d, want <= %d: %q", ansi.StringWidth(line), m.width, line)
		}
		if strings.HasPrefix(line, "╭") && !strings.HasSuffix(strings.TrimRight(line, " "), "╮") {
			t.Fatalf("View() top border should keep its right edge visible: %q", line)
		}
		if strings.HasPrefix(line, "╰") && !strings.HasSuffix(strings.TrimRight(line, " "), "╯") {
			t.Fatalf("View() bottom border should keep its right edge visible: %q", line)
		}
	}
}

func TestRenderDetailAssessmentOmitsConfidence(t *testing.T) {
	m := Model{
		projects: []model.ProjectSummary{{
			Path:                            "/tmp/demo",
			Name:                            "demo",
			Status:                          model.StatusIdle,
			PresentOnDisk:                   true,
			LatestSessionClassification:     model.ClassificationCompleted,
			LatestSessionClassificationType: model.SessionCategoryNeedsFollowUp,
		}},
		selected: 0,
		detail: model.ProjectDetail{
			LatestSessionClassification: &model.SessionClassification{
				Status:     model.ClassificationCompleted,
				Category:   model.SessionCategoryNeedsFollowUp,
				Confidence: 0.91,
				Summary:    "A concrete next step still remains.",
			},
		},
	}

	rendered := m.renderDetailContent(80)
	if !strings.Contains(rendered, "followup") {
		t.Fatalf("renderDetailContent() missing formatted category label: %q", rendered)
	}
	if strings.Contains(rendered, "91%") {
		t.Fatalf("renderDetailContent() still shows confidence: %q", rendered)
	}
	if strings.Contains(rendered, "- needs follow-up") {
		t.Fatalf("renderDetailContent() still repeats assessment category in summary section: %q", rendered)
	}
}

func TestRenderDetailSimplifiesStateAndAttention(t *testing.T) {
	m := Model{
		projects: []model.ProjectSummary{{
			Path:                            "/tmp/demo",
			Name:                            "demo",
			Status:                          model.StatusIdle,
			PresentOnDisk:                   true,
			LatestSessionClassification:     model.ClassificationCompleted,
			LatestSessionClassificationType: model.SessionCategoryWaitingForUser,
		}},
		selected: 0,
	}

	rendered := m.renderDetailContent(80)
	if strings.Contains(rendered, "State:") {
		t.Fatalf("renderDetailContent() still shows State label: %q", rendered)
	}
	if !strings.Contains(rendered, "Assessment:") {
		t.Fatalf("renderDetailContent() missing Assessment label: %q", rendered)
	}
	if !strings.Contains(rendered, "waiting") {
		t.Fatalf("renderDetailContent() missing assessment-based label: %q", rendered)
	}
	if !strings.Contains(rendered, "Status:") || !strings.Contains(rendered, "idle") {
		t.Fatalf("renderDetailContent() missing separate status field: %q", rendered)
	}
	if strings.Contains(rendered, "Attention status:") {
		t.Fatalf("renderDetailContent() still shows separate attention status line: %q", rendered)
	}
	if !strings.Contains(rendered, "Attention:") {
		t.Fatalf("renderDetailContent() missing attention score field: %q", rendered)
	}
}

func TestRenderDetailShowsAssessmentStageTiming(t *testing.T) {
	m := Model{
		nowFn: func() time.Time {
			return time.Date(2026, 3, 10, 12, 0, 37, 0, time.UTC)
		},
		projects: []model.ProjectSummary{{
			Path:                             "/tmp/demo",
			Name:                             "demo",
			Status:                           model.StatusActive,
			PresentOnDisk:                    true,
			LatestSessionFormat:              "modern",
			LatestSessionClassification:      model.ClassificationRunning,
			LatestSessionClassificationStage: model.ClassificationStageWaitingForModel,
			LatestSessionClassificationStageStartedAt: time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC),
			LatestCompletedSessionClassificationType:  model.SessionCategoryWaitingForUser,
			LatestCompletedSessionSummary:             "Waiting on a design decision before coding resumes.",
		}},
		selected: 0,
		detail: model.ProjectDetail{
			LatestSessionClassification: &model.SessionClassification{
				Status:         model.ClassificationRunning,
				Stage:          model.ClassificationStageWaitingForModel,
				StageStartedAt: time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC),
			},
		},
	}

	rendered := ansi.Strip(m.renderDetailContent(100))
	if !strings.Contains(rendered, "Assessment: waiting") {
		t.Fatalf("renderDetailContent() missing visible assessment label: %q", rendered)
	}
	if !strings.Contains(rendered, "- Waiting on a design decision before coding resumes.") {
		t.Fatalf("renderDetailContent() missing visible session summary: %q", rendered)
	}
}

func TestRenderDetailWrapsLongSessionSummary(t *testing.T) {
	m := Model{
		projects: []model.ProjectSummary{{
			Path:                            "/tmp/demo",
			Name:                            "demo",
			Status:                          model.StatusIdle,
			PresentOnDisk:                   true,
			LatestSessionClassification:     model.ClassificationCompleted,
			LatestSessionClassificationType: model.SessionCategoryNeedsFollowUp,
			LatestSessionSummary:            "This is a deliberately long session summary that should wrap inside the detail pane instead of clipping off the edge.",
		}},
		selected: 0,
		detail: model.ProjectDetail{
			LatestSessionClassification: &model.SessionClassification{
				Status:   model.ClassificationCompleted,
				Category: model.SessionCategoryNeedsFollowUp,
				Summary:  "This is a deliberately long session summary that should wrap inside the detail pane instead of clipping off the edge.",
			},
		},
	}

	rendered := ansi.Strip(m.renderDetailContent(40))
	if !strings.Contains(rendered, "- This is a deliberately long session") {
		t.Fatalf("renderDetailContent() missing wrapped summary start: %q", rendered)
	}
	if !strings.Contains(rendered, "  detail pane instead of clipping off") {
		t.Fatalf("renderDetailContent() missing wrapped summary continuation: %q", rendered)
	}
	for _, line := range strings.Split(rendered, "\n") {
		if ansi.StringWidth(line) > 40 {
			t.Fatalf("wrapped detail line width = %d, want <= 40: %q", ansi.StringWidth(line), line)
		}
	}
}

func TestRenderDetailMissingProjectOmitsForgetHint(t *testing.T) {
	m := Model{
		projects: []model.ProjectSummary{{
			Path:                            "/tmp/demo",
			Name:                            "demo",
			Status:                          model.StatusIdle,
			PresentOnDisk:                   false,
			LatestSessionClassification:     model.ClassificationCompleted,
			LatestSessionClassificationType: model.SessionCategoryCompleted,
		}},
		selected: 0,
	}

	rendered := ansi.Strip(m.renderDetailContent(80))
	if strings.Contains(rendered, "press f to forget") {
		t.Fatalf("renderDetailContent() should not show the stale forget hint for missing folders: %q", rendered)
	}
}

func TestRenderDetailMergesLastActivityAndSource(t *testing.T) {
	lastActivity := time.Date(2026, 3, 7, 12, 34, 56, 0, time.UTC)
	m := Model{
		projects: []model.ProjectSummary{{
			Path:                            "/tmp/demo",
			Name:                            "demo",
			Status:                          model.StatusActive,
			PresentOnDisk:                   true,
			LastActivity:                    lastActivity,
			LatestSessionFormat:             "modern",
			LatestSessionClassification:     model.ClassificationCompleted,
			LatestSessionClassificationType: model.SessionCategoryInProgress,
		}},
		selected: 0,
	}

	rendered := ansi.Strip(m.renderDetailContent(80))
	if !strings.Contains(rendered, "Last activity: 2026-03-07T12:34:56Z  Codex") {
		t.Fatalf("renderDetailContent() should keep source inline with last activity: %q", rendered)
	}
	if strings.Contains(rendered, "Latest source:") {
		t.Fatalf("renderDetailContent() still shows separate latest source field: %q", rendered)
	}
	if strings.Contains(rendered, "Extras:") {
		t.Fatalf("renderDetailContent() still shows extras field: %q", rendered)
	}
}

func TestRenderDetailShowsRecentMoveMetadata(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	m := Model{
		projects: []model.ProjectSummary{{
			Path:                             "/new",
			Name:                             "demo",
			Status:                           model.StatusActive,
			PresentOnDisk:                    true,
			MovedFromPath:                    "/old",
			MovedAt:                          now.Add(-2 * time.Hour),
			LatestSessionDetectedProjectPath: "/old",
		}},
		selected: 0,
	}

	rendered := ansi.Strip(m.renderDetailContent(100))
	if !strings.Contains(rendered, "Moved from: /old") {
		t.Fatalf("renderDetailContent() should show recent move origin: %q", rendered)
	}
	if !strings.Contains(rendered, "Moved at:") {
		t.Fatalf("renderDetailContent() should show recent move timestamp: %q", rendered)
	}
}

func TestRenderDetailOmitsStaleMoveMetadata(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	m := Model{
		projects: []model.ProjectSummary{{
			Path:                             "/new",
			Name:                             "demo",
			Status:                           model.StatusActive,
			PresentOnDisk:                    true,
			MovedFromPath:                    "/old",
			MovedAt:                          now.Add(-48 * time.Hour),
			LatestSessionDetectedProjectPath: "/old",
		}},
		selected: 0,
	}

	rendered := ansi.Strip(m.renderDetailContent(100))
	if strings.Contains(rendered, "Moved from: /old") {
		t.Fatalf("renderDetailContent() should hide stale move origin: %q", rendered)
	}
	if strings.Contains(rendered, "Moved at:") {
		t.Fatalf("renderDetailContent() should hide stale move timestamp: %q", rendered)
	}
}

func TestViewWithLongDetailRowsRespectsHeight(t *testing.T) {
	movedAt := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Second)
	oldPath := "/workspaces/repos/BatonDeck"
	m := Model{
		projects: []model.ProjectSummary{{
			Name:                             "demo",
			Path:                             "/workspaces/repos/LittleControlRoom",
			Status:                           model.StatusActive,
			PresentOnDisk:                    true,
			LastActivity:                     time.Date(2026, 3, 10, 17, 26, 9, 0, time.FixedZone("JST", 9*60*60)),
			LatestSessionClassification:      model.ClassificationRunning,
			LatestSessionClassificationType:  model.SessionCategoryInProgress,
			LatestSessionSummary:             "Investigation in progress.",
			LatestSessionFormat:              "modern",
			LatestSessionDetectedProjectPath: oldPath,
			MovedFromPath:                    oldPath,
			MovedAt:                          movedAt,
		}},
		selected:   0,
		status:     "Loaded 1 projects (attention, AI folders)",
		sortMode:   sortByAttention,
		visibility: visibilityAIFolders,
		width:      80,
		height:     24,
		detail: model.ProjectDetail{
			LatestSessionClassification: &model.SessionClassification{
				Status:   model.ClassificationRunning,
				Category: model.SessionCategoryInProgress,
				Summary:  "Investigation in progress.",
			},
		},
	}
	m.syncDetailViewport(false)

	rendered := ansi.Strip(m.View())
	lines := strings.Split(rendered, "\n")
	if got := len(lines); got != m.height {
		t.Fatalf("View() line count = %d, want terminal height %d; render was %q", got, m.height, rendered)
	}
	if !strings.Contains(lines[0], brand.Name) {
		t.Fatalf("View() should keep the app title visible: %q", lines[0])
	}
	if strings.Contains(lines[0], brand.Subtitle) {
		t.Fatalf("View() should omit the old subtitle from the top line: %q", lines[0])
	}
	if !strings.Contains(lines[0], "Loaded 1 projects") {
		t.Fatalf("View() should merge the status into the top line: %q", lines[0])
	}
	if len(lines) > 1 && strings.Contains(lines[1], "Loaded 1 projects") {
		t.Fatalf("View() should not render a separate second status line anymore: %q", lines[1])
	}
	for _, line := range lines {
		if ansi.StringWidth(line) > m.width {
			t.Fatalf("View() line width = %d, want <= %d: %q", ansi.StringWidth(line), m.width, line)
		}
	}
}

func TestProjectsMsgClearsInitialLoadingStatusAfterStartupLoad(t *testing.T) {
	m := Model{
		loading:    true,
		status:     initialProjectsStatus,
		sortMode:   sortByAttention,
		visibility: visibilityAIFolders,
	}

	updated, _ := m.Update(projectsMsg{
		projects: []model.ProjectSummary{{
			Name:                "demo",
			Path:                "/tmp/demo",
			PresentOnDisk:       true,
			LatestSessionFormat: "modern",
		}},
	})
	got := updated.(Model)

	want := loadedProjectsStatus(1, sortByAttention, visibilityAIFolders)
	if got.status != want {
		t.Fatalf("status = %q, want %q", got.status, want)
	}
	if got.loading {
		t.Fatalf("loading should be false after projects load")
	}
}

func TestSplitBodyHeightsGrowFocusedPane(t *testing.T) {
	listListHeight, listDetailHeight := splitBodyHeights(20, focusProjects)
	if listListHeight <= listDetailHeight {
		t.Fatalf("projects focus should favor list pane: list=%d detail=%d", listListHeight, listDetailHeight)
	}

	detailListHeight, detailDetailHeight := splitBodyHeights(20, focusDetail)
	if detailDetailHeight <= detailListHeight {
		t.Fatalf("detail focus should favor detail pane: list=%d detail=%d", detailListHeight, detailDetailHeight)
	}

	runtimeListHeight, runtimeBottomHeight := splitBodyHeights(20, focusRuntime)
	if runtimeBottomHeight <= runtimeListHeight {
		t.Fatalf("runtime focus should favor bottom panes: list=%d bottom=%d", runtimeListHeight, runtimeBottomHeight)
	}
}

func TestTabSwitchesFocusAndEscReturnsToList(t *testing.T) {
	m := Model{
		focusedPane: focusProjects,
		width:       100,
		height:      24,
	}
	m.syncDetailViewport(false)

	updated, _ := m.updateNormalMode(tea.KeyMsg{Type: tea.KeyTab})
	got := updated.(Model)
	if got.focusedPane != focusDetail {
		t.Fatalf("tab should move focus to detail, got %s", got.focusedPane)
	}

	updated, _ = got.updateNormalMode(tea.KeyMsg{Type: tea.KeyTab})
	got = updated.(Model)
	if got.focusedPane != focusRuntime {
		t.Fatalf("second tab should move focus to runtime, got %s", got.focusedPane)
	}

	updated, _ = got.updateNormalMode(tea.KeyMsg{Type: tea.KeyShiftTab})
	got = updated.(Model)
	if got.focusedPane != focusDetail {
		t.Fatalf("shift+tab should move focus back to detail, got %s", got.focusedPane)
	}

	updated, _ = got.updateNormalMode(tea.KeyMsg{Type: tea.KeyEsc})
	got = updated.(Model)
	if got.focusedPane != focusProjects {
		t.Fatalf("esc should return focus to list, got %s", got.focusedPane)
	}
}

func TestSlashOpensCommandMode(t *testing.T) {
	m := Model{
		commandInput: textinput.New(),
		width:        100,
		height:       24,
	}

	updated, _ := m.updateNormalMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	got := updated.(Model)
	if !got.commandMode {
		t.Fatalf("slash should open command mode")
	}
	if got.commandInput.Value() != "/" {
		t.Fatalf("command input = %q, want /", got.commandInput.Value())
	}
	rendered := got.View()
	if !strings.Contains(rendered, "Command Palette") {
		t.Fatalf("View() missing command palette: %q", rendered)
	}
	if !strings.Contains(rendered, "Suggestions") {
		t.Fatalf("View() missing command suggestions section: %q", rendered)
	}
}

func TestRuntimeCommandFocusesRuntimePane(t *testing.T) {
	m := Model{
		projects: []model.ProjectSummary{{
			Name:          "demo",
			Path:          "/tmp/demo",
			PresentOnDisk: true,
			RunCommand:    "pnpm dev",
		}},
		selected:    0,
		width:       100,
		height:      24,
		focusedPane: focusProjects,
	}

	updated, cmd := m.dispatchCommand(commands.Invocation{Kind: commands.KindRuntime})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("/runtime should focus locally without an async command")
	}
	if got.focusedPane != focusRuntime {
		t.Fatalf("/runtime should focus the runtime pane, got %s", got.focusedPane)
	}
	if got.status != "Focus: runtime pane" {
		t.Fatalf("status = %q, want runtime focus status", got.status)
	}
}

func TestQuitKeyStopsManagedRuntimes(t *testing.T) {
	dir := t.TempDir()
	manager := projectrun.NewManager()
	defer func() { _ = manager.CloseAll() }()

	_, err := manager.Start(projectrun.StartRequest{
		ProjectPath: dir,
		Command:     "sleep 30",
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	waitForRuntimeSnapshot(t, manager, dir, func(snapshot projectrun.Snapshot) bool {
		return snapshot.Running
	})

	m := Model{runtimeManager: manager}
	updated, cmd := m.updateNormalMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if cmd == nil {
		t.Fatalf("quit key should return tea.Quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("quit key command should emit tea.QuitMsg")
	}
	_ = updated.(Model)

	snapshot := waitForRuntimeStopped(t, manager, dir)
	if snapshot.Running {
		t.Fatalf("runtime should be stopped after quit: %+v", snapshot)
	}
}

func TestForgetKeyDoesNothing(t *testing.T) {
	m := Model{
		projects: []model.ProjectSummary{{
			Path:          "/tmp/missing",
			Name:          "missing",
			PresentOnDisk: false,
		}},
		selected: 0,
	}

	updated, cmd := m.updateNormalMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("forget key should no longer return a forget command")
	}
	if got.status != "" {
		t.Fatalf("status = %q, want empty status after removed shortcut", got.status)
	}
}

func TestEnterLaunchesCodexFromFocusedProjectList(t *testing.T) {
	m := Model{
		projects: []model.ProjectSummary{
			{
				Path:                "/tmp/demo",
				Name:                "demo",
				PresentOnDisk:       true,
				LatestSessionID:     "cx_summary",
				LatestSessionFormat: "modern",
			},
		},
		selected:    0,
		focusedPane: focusProjects,
		width:       100,
		height:      24,
	}

	updated, cmd := m.updateNormalMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("enter should return an embedded Codex open command when the list is focused")
	}
	if got.status != "Opening embedded Codex session..." {
		t.Fatalf("status = %q, want embedded open notice", got.status)
	}
}

func TestEnterRestoresHiddenLiveCodexSessionFromFocusedProjectList(t *testing.T) {
	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider: codexapp.ProviderCodex,
				Started:  true,
				ThreadID: "thread-live",
				Status:   "Codex session ready",
			},
		}, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Preset:      codexcli.PresetYolo,
		ResumeID:    "thread-live",
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	m := Model{
		codexManager: manager,
		projects: []model.ProjectSummary{
			{
				Path:                "/tmp/demo",
				Name:                "demo",
				PresentOnDisk:       true,
				LatestSessionID:     "thread-stale",
				LatestSessionFormat: "modern",
			},
		},
		selected:           0,
		focusedPane:        focusProjects,
		codexHiddenProject: "/tmp/demo",
		detail: model.ProjectDetail{
			Summary: model.ProjectSummary{Path: "/tmp/demo"},
			Sessions: []model.SessionEvidence{
				{SessionID: "thread-stale", Format: "modern"},
			},
		},
		codexInput:    newCodexTextarea(),
		codexDrafts:   make(map[string]codexDraft),
		codexViewport: viewport.New(0, 0),
		width:         100,
		height:        24,
	}

	updated, cmd := m.updateNormalMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("enter should restore the hidden embedded Codex session")
	}
	if got.codexVisibleProject != "/tmp/demo" {
		t.Fatalf("codexVisibleProject = %q, want /tmp/demo", got.codexVisibleProject)
	}
	if got.codexPendingOpen != nil {
		t.Fatalf("codexPendingOpen = %#v, want nil when restoring a live session", got.codexPendingOpen)
	}
	if got.status != "Embedded Codex session reopened. Alt+Up hides it." {
		t.Fatalf("status = %q, want live restore notice", got.status)
	}
	if len(requests) != 1 {
		t.Fatalf("launch requests = %d, want 1 because reopening should not launch again", len(requests))
	}
}

func TestEnterDoesNotLaunchCodexFromDetailPane(t *testing.T) {
	m := Model{
		projects: []model.ProjectSummary{
			{
				Path:          "/tmp/demo",
				Name:          "demo",
				PresentOnDisk: true,
			},
		},
		selected:    0,
		focusedPane: focusDetail,
		width:       100,
		height:      24,
		status:      "Focus: detail pane",
	}

	updated, cmd := m.updateNormalMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("enter should not launch Codex when the detail pane is focused")
	}
	if got.status != "Focus: detail pane" {
		t.Fatalf("status = %q, want unchanged detail focus status", got.status)
	}
}

func TestF2DoesNothingInMainView(t *testing.T) {
	m := Model{
		codexHiddenProject: "/tmp/demo",
		codexInput:         newCodexTextarea(),
		codexViewport:      viewport.New(0, 0),
		width:              100,
		height:             24,
		status:             "unchanged",
	}

	updated, cmd := m.updateNormalMode(tea.KeyMsg{Type: tea.KeyF2})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("f2 in the main view should not queue a command")
	}
	if got.codexVisibleProject != "" {
		t.Fatalf("codexVisibleProject = %q, want hidden", got.codexVisibleProject)
	}
	if got.status != "unchanged" {
		t.Fatalf("status = %q, want unchanged", got.status)
	}
}

func TestAltUpDoesNotRestoreHiddenCodexSessionFromMainView(t *testing.T) {
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
		codexInput:         newCodexTextarea(),
		codexViewport:      viewport.New(0, 0),
		width:              100,
		height:             24,
	}

	updated, cmd := m.updateNormalMode(tea.KeyMsg{Type: tea.KeyUp, Alt: true})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("alt+up in the main view should not queue a command")
	}
	if got.codexVisibleProject != "" {
		t.Fatalf("codexVisibleProject = %q, want hidden", got.codexVisibleProject)
	}
}

func TestRefreshBusyElsewhereCmdRechecksVisibleSession(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Started:      true,
			Preset:       codexcli.PresetYolo,
			Busy:         true,
			BusyExternal: true,
			ThreadID:     "019cccc3abcdef",
			Status:       "Busy elsewhere",
		},
		refreshBusyFn: func(s *fakeCodexSession) error {
			s.snapshot.Busy = false
			s.snapshot.BusyExternal = false
			s.snapshot.Status = "Embedded controls are live again."
			s.snapshot.LastSystemNotice = "Embedded Codex session 019cccc3 is no longer active in another Codex process. Embedded controls are live again."
			return nil
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
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          newCodexTextarea(),
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              24,
	}

	cmd := m.refreshBusyElsewhereCmd("/tmp/demo")
	if cmd == nil {
		t.Fatalf("refreshBusyElsewhereCmd() should return a refresh command")
	}
	msg := cmd()
	update, ok := msg.(codexUpdateMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want codexUpdateMsg", msg)
	}
	if update.projectPath != "/tmp/demo" {
		t.Fatalf("project path = %q, want /tmp/demo", update.projectPath)
	}
	if session.refreshCalls != 1 {
		t.Fatalf("refresh calls = %d, want 1", session.refreshCalls)
	}
	if session.snapshot.BusyExternal {
		t.Fatalf("session should no longer be busy externally after refresh")
	}
}

func TestVisibleCodexEnterSubmitsPrompt(t *testing.T) {
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

	input := newCodexTextarea()
	input.SetValue("summarize this repo")

	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          input,
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              24,
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("enter should submit the visible Codex prompt")
	}
	if got.codexInput.Value() != "" {
		t.Fatalf("codex input should clear after submit, got %q", got.codexInput.Value())
	}
	if got.status != "Sending prompt to Codex..." {
		t.Fatalf("status = %q, want sending notice", got.status)
	}
}

func TestVisibleCodexSlashSuggestionsRender(t *testing.T) {
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

	input := newCodexTextarea()
	input.SetValue("/")

	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          input,
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              24,
	}
	m.syncCodexViewport(true)

	rendered := ansi.Strip(m.renderCodexView())
	if !strings.Contains(rendered, "Embedded Slash Commands") {
		t.Fatalf("rendered view should show embedded slash commands: %q", rendered)
	}
	if !strings.Contains(rendered, "/new [prompt]") || !strings.Contains(rendered, "/resume [session-id]") || !strings.Contains(rendered, "/model") || !strings.Contains(rendered, "/status") {
		t.Fatalf("rendered view should list embedded slash suggestions: %q", rendered)
	}
	if !strings.Contains(rendered, "Enter run  Ctrl+C close  Alt+Up hide") {
		t.Fatalf("rendered view should advertise slash command handling in the footer: %q", rendered)
	}
}

func TestVisibleCodexSlashTabCyclesSuggestions(t *testing.T) {
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

	input := newCodexTextarea()
	input.SetValue("/")

	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          input,
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              24,
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyTab})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("tab completion should not queue a command")
	}
	if got.codexInput.Value() != "/new" {
		t.Fatalf("codex input = %q, want /new after first tab", got.codexInput.Value())
	}

	updated, cmd = got.updateCodexMode(tea.KeyMsg{Type: tea.KeyTab})
	got = updated.(Model)
	if cmd != nil {
		t.Fatalf("tab cycling should not queue a command")
	}
	if got.codexInput.Value() != "/resume" {
		t.Fatalf("codex input = %q, want /resume after second tab", got.codexInput.Value())
	}

	updated, cmd = got.updateCodexMode(tea.KeyMsg{Type: tea.KeyTab})
	got = updated.(Model)
	if cmd != nil {
		t.Fatalf("tab cycling should not queue a command")
	}
	if got.codexInput.Value() != "/model" {
		t.Fatalf("codex input = %q, want /model after third tab", got.codexInput.Value())
	}

	updated, cmd = got.updateCodexMode(tea.KeyMsg{Type: tea.KeyTab})
	got = updated.(Model)
	if cmd != nil {
		t.Fatalf("tab cycling should not queue a command")
	}
	if got.codexInput.Value() != "/status" {
		t.Fatalf("codex input = %q, want /status after fourth tab", got.codexInput.Value())
	}

	updated, cmd = got.updateCodexMode(tea.KeyMsg{Type: tea.KeyShiftTab})
	got = updated.(Model)
	if cmd != nil {
		t.Fatalf("shift+tab cycling should not queue a command")
	}
	if got.codexInput.Value() != "/model" {
		t.Fatalf("codex input = %q, want /model after shift+tab", got.codexInput.Value())
	}
}

func TestVisibleCodexSlashStatusRunsLocally(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Started:  true,
			Preset:   codexcli.PresetYolo,
			Status:   "Codex session ready",
			ThreadID: "thread_demo",
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

	input := newCodexTextarea()
	input.SetValue("/st")

	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          input,
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              24,
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("enter should run the embedded /status command")
	}
	if got.codexInput.Value() != "" {
		t.Fatalf("codex input should clear after /status, got %q", got.codexInput.Value())
	}
	if got.status != "Reading embedded Codex status..." {
		t.Fatalf("status = %q, want reading status notice", got.status)
	}

	msg := cmd()
	action, ok := msg.(codexActionMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want codexActionMsg", msg)
	}
	if action.err != nil {
		t.Fatalf("/status returned error = %v", action.err)
	}
	if session.statusCalls != 1 {
		t.Fatalf("status calls = %d, want 1", session.statusCalls)
	}
	if len(session.submissions) != 0 {
		t.Fatalf("/status should not submit a Codex prompt, submissions = %d", len(session.submissions))
	}
	rendered := ansi.Strip(got.renderCodexView())
	if !strings.Contains(rendered, "Status") || !strings.Contains(rendered, "85% left") {
		t.Fatalf("rendered view should include the local /status transcript block: %q", rendered)
	}
}

func TestVisibleCodexSlashModelOpensPickerAndStagesSelection(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Started:         true,
			Preset:          codexcli.PresetYolo,
			Status:          "Codex session ready",
			ThreadID:        "thread_demo",
			Model:           "gpt-5",
			ReasoningEffort: "medium",
		},
		models: []codexapp.ModelOption{
			{
				ID:          "gpt-5",
				Model:       "gpt-5",
				DisplayName: "GPT-5",
				Description: "Balanced default",
				IsDefault:   true,
				SupportedReasoningEfforts: []codexapp.ReasoningEffortOption{
					{ReasoningEffort: "medium", Description: "Balanced"},
					{ReasoningEffort: "high", Description: "More deliberate"},
				},
				DefaultReasoningEffort: "medium",
			},
			{
				ID:          "gpt-5-codex",
				Model:       "gpt-5-codex",
				DisplayName: "GPT-5 Codex",
				Description: "Specialized coding model",
				SupportedReasoningEfforts: []codexapp.ReasoningEffortOption{
					{ReasoningEffort: "low", Description: "Fast"},
					{ReasoningEffort: "medium", Description: "Balanced"},
					{ReasoningEffort: "high", Description: "Thorough"},
				},
				DefaultReasoningEffort: "medium",
			},
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

	input := newCodexTextarea()
	input.SetValue("/model")

	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          input,
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              28,
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("enter should open the embedded /model picker")
	}
	if got.codexInput.Value() != "" {
		t.Fatalf("codex input should clear after /model, got %q", got.codexInput.Value())
	}
	if !got.codexModelPickerVisible() || !got.codexModelPicker.Loading {
		t.Fatalf("model picker should enter loading state")
	}

	msg := cmd()
	listMsg, ok := msg.(codexModelListMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want codexModelListMsg", msg)
	}
	updated, _ = got.Update(listMsg)
	got = updated.(Model)
	if !got.codexModelPickerVisible() || got.codexModelPicker.Loading {
		t.Fatalf("model picker should be visible with loaded models")
	}
	if got.codexModelPicker.Focus != codexModelPickerFocusModels {
		t.Fatalf("initial picker focus = %q, want models", got.codexModelPicker.Focus)
	}

	updated, _ = got.updateCodexModelPickerMode(tea.KeyMsg{Type: tea.KeyDown})
	got = updated.(Model)
	updated, _ = got.updateCodexModelPickerMode(tea.KeyMsg{Type: tea.KeyTab})
	got = updated.(Model)
	if got.codexModelPicker.Focus != codexModelPickerFocusEfforts {
		t.Fatalf("picker focus after first tab = %q, want efforts", got.codexModelPicker.Focus)
	}
	updated, _ = got.updateCodexModelPickerMode(tea.KeyMsg{Type: tea.KeyTab})
	got = updated.(Model)
	if got.codexModelPicker.Focus != codexModelPickerFocusModels {
		t.Fatalf("picker focus after second tab = %q, want models", got.codexModelPicker.Focus)
	}
	updated, _ = got.updateCodexModelPickerMode(tea.KeyMsg{Type: tea.KeyTab})
	got = updated.(Model)
	if got.codexModelPicker.Focus != codexModelPickerFocusEfforts {
		t.Fatalf("picker focus after third tab = %q, want efforts", got.codexModelPicker.Focus)
	}
	updated, cmd = got.updateCodexModelPickerMode(tea.KeyMsg{Type: tea.KeyDown})
	got = updated.(Model)
	updated, cmd = got.updateCodexModelPickerMode(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if cmd == nil {
		t.Fatalf("enter should apply the selected model choice")
	}

	msg = cmd()
	action, ok := msg.(codexActionMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want codexActionMsg", msg)
	}
	if action.err != nil {
		t.Fatalf("/model returned error = %v", action.err)
	}
	if len(session.modelStages) != 1 {
		t.Fatalf("model stages = %d, want 1", len(session.modelStages))
	}
	if session.modelStages[0].Model != "gpt-5-codex" || session.modelStages[0].Reasoning != "high" {
		t.Fatalf("staged model = %#v, want gpt-5-codex + high", session.modelStages[0])
	}
}

func TestVisibleCodexSlashResumeOpensPickerAndLoadsChoices(t *testing.T) {
	modernFixture := filepath.Clean(filepath.Join("..", "..", "testdata", "codex_footprint", "sessions", "2026", "03", "05", "rollout-modern.jsonl"))
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Started:  true,
			Preset:   codexcli.PresetYolo,
			Status:   "Codex session ready",
			ThreadID: "thread-demo",
			Entries: []codexapp.TranscriptEntry{
				{Kind: codexapp.TranscriptUser, Text: "Current task title"},
				{Kind: codexapp.TranscriptAgent, Text: "Current session summary."},
			},
			LastActivityAt: time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC),
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

	input := newCodexTextarea()
	input.SetValue("/resume")

	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          input,
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              28,
		detail: model.ProjectDetail{
			Summary: model.ProjectSummary{
				Path:                 "/tmp/demo",
				Name:                 "demo",
				PresentOnDisk:        true,
				LatestSessionID:      "thread-demo",
				LatestSessionFormat:  "modern",
				LatestSessionSummary: "Work appears complete for now.",
			},
			Sessions: []model.SessionEvidence{
				{
					SessionID:   "thread-demo",
					Format:      "modern",
					SessionFile: modernFixture,
					LastEventAt: time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC),
				},
				{
					SessionID:   "thread-old",
					Format:      "modern",
					SessionFile: modernFixture,
					LastEventAt: time.Date(2026, 3, 8, 11, 0, 0, 0, time.UTC),
				},
			},
		},
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("enter should open the embedded /resume picker")
	}
	if !got.codexPickerVisible || !got.codexPickerLoading || got.codexPickerKind != codexPickerKindResume {
		t.Fatalf("resume picker should enter loading state")
	}
	if got.status != "Loading Codex sessions for this project..." {
		t.Fatalf("status = %q, want loading embedded sessions notice", got.status)
	}
	if got.codexInput.Value() != "" {
		t.Fatalf("codex input should clear after /resume, got %q", got.codexInput.Value())
	}

	msg := cmd()
	listMsg, ok := msg.(codexResumeChoicesMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want codexResumeChoicesMsg", msg)
	}
	if listMsg.err != nil {
		t.Fatalf("/resume returned error = %v", listMsg.err)
	}

	updated, _ = got.Update(listMsg)
	got = updated.(Model)
	if !got.codexPickerVisible || got.codexPickerLoading || got.codexPickerKind != codexPickerKindResume {
		t.Fatalf("resume picker should remain visible with loaded choices")
	}
	if len(got.codexPickerChoices) != 2 {
		t.Fatalf("resume picker choices = %d, want 2", len(got.codexPickerChoices))
	}
	if !got.codexPickerChoices[0].Current {
		t.Fatalf("first resume choice should be marked current")
	}
	if got.codexPickerChoices[0].Title != "Current task title" {
		t.Fatalf("current choice title = %q, want live title", got.codexPickerChoices[0].Title)
	}
	if got.codexPickerChoices[0].Summary != "Work appears complete for now." {
		t.Fatalf("current choice summary = %q, want latest summary", got.codexPickerChoices[0].Summary)
	}

	rendered := ansi.Strip(got.View())
	for _, want := range []string{"Resume Codex Session", "CURRENT", "Current task title", "Work appears complete for now."} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("resume picker render missing %q: %q", want, rendered)
		}
	}
}

func TestBuildCodexResumeChoicesSkipsForkedSubagentSessions(t *testing.T) {
	parentFixture := filepath.Join(t.TempDir(), "parent.jsonl")
	if err := os.WriteFile(parentFixture, []byte(strings.Join([]string{
		`{"timestamp":"2026-03-14T06:27:12Z","type":"session_meta","payload":{"id":"thread-parent","cwd":"/tmp/demo"}}`,
		`{"timestamp":"2026-03-14T06:27:13Z","type":"event_msg","payload":{"type":"user_message","message":"Top-level conversation"}}`,
		`{"timestamp":"2026-03-14T06:27:14Z","type":"event_msg","payload":{"type":"agent_message","message":"Parent summary"}}`,
	}, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("write parent fixture: %v", err)
	}

	childFixture := filepath.Join(t.TempDir(), "child.jsonl")
	if err := os.WriteFile(childFixture, []byte(strings.Join([]string{
		`{"timestamp":"2026-03-14T09:32:01Z","type":"session_meta","payload":{"id":"thread-child","forked_from_id":"thread-parent","cwd":"/tmp/demo","agent_role":"explorer","source":{"subagent":{"thread_spawn":{"parent_thread_id":"thread-parent"}}}}}`,
		`{"timestamp":"2026-03-14T09:32:02Z","type":"event_msg","payload":{"type":"user_message","message":"Top-level conversation"}}`,
		`{"timestamp":"2026-03-14T09:32:03Z","type":"event_msg","payload":{"type":"agent_message","message":"Child summary"}}`,
	}, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("write child fixture: %v", err)
	}

	choices := buildCodexResumeChoices(context.Background(), model.ProjectDetail{
		Summary: model.ProjectSummary{
			Path:          "/tmp/demo",
			Name:          "demo",
			PresentOnDisk: true,
		},
		Sessions: []model.SessionEvidence{
			{
				SessionID:   "thread-parent",
				Format:      "modern",
				SessionFile: parentFixture,
				LastEventAt: time.Date(2026, 3, 14, 6, 27, 14, 0, time.UTC),
			},
			{
				SessionID:   "thread-child",
				Format:      "modern",
				SessionFile: childFixture,
				LastEventAt: time.Date(2026, 3, 14, 9, 32, 3, 0, time.UTC),
			},
		},
	}, codexapp.ProviderCodex)

	if len(choices) != 1 {
		t.Fatalf("resume picker choices = %d, want 1 after hiding forked subagent session", len(choices))
	}
	if choices[0].SessionID != "thread-parent" {
		t.Fatalf("remaining choice session id = %q, want thread-parent", choices[0].SessionID)
	}
}

func TestRenderCodexPickerRowUsesCompactSavedBadgeAndTitleInResumeMode(t *testing.T) {
	m := Model{codexPickerKind: codexPickerKindResume}
	row := ansi.Strip(m.renderCodexPickerRow(codexSessionChoice{
		Provider:     codexapp.ProviderCodex,
		SessionID:    "thread-old",
		LastActivity: time.Date(2026, 3, 13, 19, 56, 0, 0, time.UTC),
		Title:        "# AGENTS.md instructions for /Users/davide/dev/repos/FractalMech",
		Summary:      "Feature added: wheel plays a blip on character change, build passes; tuning offers optional next steps.",
	}, false, 96))

	if !strings.Contains(row, "CX     SAVE") {
		t.Fatalf("resume picker row should use the compact saved badge rail: %q", row)
	}
	if strings.Contains(row, "LAST") {
		t.Fatalf("resume picker row should not label every saved session as last: %q", row)
	}
	if !strings.Contains(row, "# AGENTS.md instructions") {
		t.Fatalf("resume picker row should surface the title in the list: %q", row)
	}
	if strings.Contains(row, "Feature added: wheel plays a blip") {
		t.Fatalf("resume picker row should no longer use the summary as the primary list preview: %q", row)
	}
}

func TestRenderCodexPickerRowMarksLatestSavedSessionInResumeMode(t *testing.T) {
	m := Model{codexPickerKind: codexPickerKindResume}
	row := ansi.Strip(m.renderCodexPickerRow(codexSessionChoice{
		Provider:     codexapp.ProviderCodex,
		SessionID:    "thread-latest",
		LastActivity: time.Date(2026, 3, 13, 19, 56, 0, 0, time.UTC),
		Summary:      "Latest saved session summary.",
		Latest:       true,
	}, false, 96))

	if !strings.Contains(row, "CX     LAST") {
		t.Fatalf("resume picker row should use the compact latest badge rail: %q", row)
	}
	if strings.Contains(row, "SAVE") {
		t.Fatalf("latest saved session should use the latest badge instead of saved: %q", row)
	}
}

func TestRenderCodexPickerRowAlignsResumeMetadataColumns(t *testing.T) {
	m := Model{codexPickerKind: codexPickerKindResume}
	at := time.Date(2026, 3, 13, 19, 56, 0, 0, time.UTC)
	ts := formatPickerActivity(at)

	shortRow := ansi.Strip(m.renderCodexPickerRow(codexSessionChoice{
		Provider:     codexapp.ProviderCodex,
		SessionID:    "thread-short",
		LastActivity: at,
		Summary:      "Short label",
	}, false, 96))

	longRow := ansi.Strip(m.renderCodexPickerRow(codexSessionChoice{
		Provider:     codexapp.ProviderCodex,
		SessionID:    "thread-longer",
		LastActivity: at,
		Summary:      "Selection highlight implemented, tests and validation passed after the footer refresh.",
	}, false, 96))

	shortIndex := strings.Index(shortRow, ts)
	longIndex := strings.Index(longRow, ts)
	if shortIndex < 0 || longIndex < 0 {
		t.Fatalf("expected both rows to include the activity timestamp %q: short=%q long=%q", ts, shortRow, longRow)
	}
	if shortIndex != longIndex {
		t.Fatalf("timestamp columns should align: short=%d long=%d shortRow=%q longRow=%q", shortIndex, longIndex, shortRow, longRow)
	}
}

func TestRenderCodexPickerRowKeepsCompactBadgeColumnAligned(t *testing.T) {
	m := Model{codexPickerKind: codexPickerKindResume}
	at := time.Date(2026, 3, 13, 19, 56, 0, 0, time.UTC)
	ts := formatPickerActivity(at)

	savedRow := ansi.Strip(m.renderCodexPickerRow(codexSessionChoice{
		Provider:     codexapp.ProviderCodex,
		SessionID:    "thread-saved",
		LastActivity: at,
		Title:        "Saved session title",
	}, false, 96))

	currentLiveRow := ansi.Strip(m.renderCodexPickerRow(codexSessionChoice{
		Provider:     codexapp.ProviderCodex,
		SessionID:    "thread-live",
		LastActivity: at,
		Title:        "Current live session title",
		Current:      true,
		Live:         true,
	}, false, 96))

	savedIndex := strings.Index(savedRow, ts)
	liveIndex := strings.Index(currentLiveRow, ts)
	if savedIndex < 0 || liveIndex < 0 {
		t.Fatalf("expected both rows to include the activity timestamp %q: saved=%q live=%q", ts, savedRow, currentLiveRow)
	}
	if savedIndex != liveIndex {
		t.Fatalf("compact badge rail should keep timestamp columns aligned: saved=%d live=%d savedRow=%q liveRow=%q", savedIndex, liveIndex, savedRow, currentLiveRow)
	}
	if !strings.Contains(currentLiveRow, "CX CUR LIVE") {
		t.Fatalf("current live row should use the compact current/live badge rail: %q", currentLiveRow)
	}
}

func TestCodexPickerWindowUsesAvailableTerminalHeight(t *testing.T) {
	m := Model{
		codexPickerKind:     codexPickerKindResume,
		codexPickerSelected: 0,
		codexPickerChoices: []codexSessionChoice{
			{Title: "First", Summary: "Summary"},
		},
	}

	start, end := m.codexPickerWindow(20, 30)
	if start != 0 {
		t.Fatalf("start = %d, want 0", start)
	}
	if visible := end - start; visible <= 5 {
		t.Fatalf("visible rows = %d, want more than the old fixed window", visible)
	}
}

func TestVisibleCodexSlashResumeIDOpensRequestedSession(t *testing.T) {
	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Started:  true,
				Preset:   req.Preset,
				Status:   "Codex session ready",
				ThreadID: req.ResumeID,
			},
		}, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Preset:      codexcli.PresetYolo,
		ResumeID:    "thread-demo",
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	input := newCodexTextarea()
	input.SetValue("/resume thread-old")

	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          input,
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              24,
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("enter should resume the requested embedded session")
	}
	if got.codexPendingOpen == nil {
		t.Fatalf("codexPendingOpen should be set while the requested session opens")
	}
	if !strings.Contains(got.status, "Opening embedded Codex session") || !strings.Contains(got.status, "thread-o") {
		t.Fatalf("status = %q, want requested session open notice", got.status)
	}

	msg := cmd()
	opened, ok := msg.(codexSessionOpenedMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want codexSessionOpenedMsg", msg)
	}
	if opened.err != nil {
		t.Fatalf("/resume thread-old returned error = %v", opened.err)
	}
	if len(requests) != 2 {
		t.Fatalf("launch requests = %d, want 2", len(requests))
	}
	if requests[1].ResumeID != "thread-old" {
		t.Fatalf("resume id = %q, want %q", requests[1].ResumeID, "thread-old")
	}
}

func TestVisibleCodexSlashSessionAliasOpensRequestedOpenCodeSession(t *testing.T) {
	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider: codexapp.ProviderOpenCode,
				Started:  true,
				Status:   "OpenCode session ready",
				ThreadID: req.ResumeID,
			},
		}, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Provider:    codexapp.ProviderOpenCode,
		ResumeID:    "ses-current",
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	input := newCodexTextarea()
	input.SetValue("/session ses-old")

	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          input,
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              24,
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("enter should resume the requested embedded OpenCode session")
	}
	if got.codexPendingOpen == nil || got.codexPendingOpen.provider != codexapp.ProviderOpenCode {
		t.Fatalf("codexPendingOpen = %#v, want pending OpenCode session", got.codexPendingOpen)
	}
	if !strings.Contains(got.status, "Opening embedded OpenCode session") || !strings.Contains(got.status, "ses-old") {
		t.Fatalf("status = %q, want requested OpenCode session open notice", got.status)
	}

	msg := cmd()
	opened, ok := msg.(codexSessionOpenedMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want codexSessionOpenedMsg", msg)
	}
	if opened.err != nil {
		t.Fatalf("/session ses-old returned error = %v", opened.err)
	}
	if len(requests) != 2 {
		t.Fatalf("launch requests = %d, want 2", len(requests))
	}
	if requests[1].Provider != codexapp.ProviderOpenCode {
		t.Fatalf("provider = %q, want %q", requests[1].Provider, codexapp.ProviderOpenCode)
	}
	if requests[1].ResumeID != "ses-old" {
		t.Fatalf("resume id = %q, want %q", requests[1].ResumeID, "ses-old")
	}
}

func TestVisibleCodexSlashNewStartsFreshSession(t *testing.T) {
	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Started:  true,
				Preset:   req.Preset,
				ThreadID: fmt.Sprintf("thread_%d", len(requests)),
				Status:   "Codex session ready",
			},
		}, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Preset:      codexcli.PresetYolo,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	input := newCodexTextarea()
	input.SetValue("/new continue in the new thread")

	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          input,
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              24,
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("enter should run the embedded /new command")
	}
	if got.codexInput.Value() != "" {
		t.Fatalf("codex input should clear after /new, got %q", got.codexInput.Value())
	}
	if got.status != "Starting a fresh embedded Codex session..." {
		t.Fatalf("status = %q, want fresh-session notice", got.status)
	}
	if got.codexPendingOpen == nil || got.codexPendingOpen.projectPath != "/tmp/demo" {
		t.Fatalf("codexPendingOpen = %#v, want pending open for /tmp/demo", got.codexPendingOpen)
	}
	rendered := ansi.Strip(got.renderCodexView())
	if !strings.Contains(rendered, "Opening embedded Codex session...") {
		t.Fatalf("rendered view should show opening state, got %q", rendered)
	}

	msg := cmd()
	opened, ok := msg.(codexSessionOpenedMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want codexSessionOpenedMsg", msg)
	}
	if opened.err != nil {
		t.Fatalf("/new returned error = %v", opened.err)
	}
	if len(requests) != 2 {
		t.Fatalf("launch requests = %d, want 2", len(requests))
	}
	if !requests[1].ForceNew {
		t.Fatalf("second launch request should force a fresh session")
	}
	if requests[1].Prompt != "continue in the new thread" {
		t.Fatalf("second launch prompt = %q, want inline /new prompt", requests[1].Prompt)
	}
}

func TestVisibleOpenCodeSlashNewFailureKeepsClosedSessionVisible(t *testing.T) {
	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		if len(requests) == 1 {
			return &fakeCodexSession{
				projectPath: req.ProjectPath,
				snapshot: codexapp.Snapshot{
					Provider: codexapp.ProviderOpenCode,
					Started:  true,
					Status:   "OpenCode session ready",
					ThreadID: "ses-current",
				},
			}, nil
		}
		return nil, fmt.Errorf("opencode create failed")
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Provider:    codexapp.ProviderOpenCode,
		ResumeID:    "ses-current",
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	input := newCodexTextarea()
	input.SetValue("/new")

	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          input,
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              24,
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("enter should run the embedded OpenCode /new command")
	}
	if got.codexPendingOpen == nil || got.codexPendingOpen.provider != codexapp.ProviderOpenCode {
		t.Fatalf("codexPendingOpen = %#v, want pending OpenCode session", got.codexPendingOpen)
	}

	msg := cmd()
	opened, ok := msg.(codexSessionOpenedMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want codexSessionOpenedMsg", msg)
	}
	if opened.err == nil || opened.err.Error() != "opencode create failed" {
		t.Fatalf("/new returned error = %v, want opencode create failed", opened.err)
	}

	updated, _ = got.Update(opened)
	got = updated.(Model)
	if got.codexPendingOpen != nil {
		t.Fatalf("codexPendingOpen = %#v, want nil after handling the failed open", got.codexPendingOpen)
	}
	if got.codexVisibleProject != "/tmp/demo" {
		t.Fatalf("codexVisibleProject = %q, want the closed OpenCode session to remain visible", got.codexVisibleProject)
	}
	snapshot, ok := got.currentCodexSnapshot()
	if !ok {
		t.Fatalf("currentCodexSnapshot() unavailable after a failed replacement")
	}
	if !snapshot.Closed {
		t.Fatalf("snapshot.Closed = false, want the previous OpenCode session to remain as a closed placeholder")
	}
	if got.status != "Embedded session open failed" {
		t.Fatalf("status = %q, want embedded-session-open-failed notice", got.status)
	}
	if got.err == nil || got.err.Error() != "opencode create failed" {
		t.Fatalf("got.err = %v, want opencode create failed", got.err)
	}
	rendered := ansi.Strip(got.renderCodexView())
	if !strings.Contains(rendered, "OpenCode session closed.") {
		t.Fatalf("rendered view should keep showing the closed OpenCode session, got %q", rendered)
	}
	if len(requests) != 2 {
		t.Fatalf("launch requests = %d, want 2", len(requests))
	}
	if !requests[1].ForceNew {
		t.Fatalf("second launch request should force a fresh OpenCode session")
	}
}

func TestVisibleCodexSlashNewWarnsWhenActiveSessionIsReopenedReadOnly(t *testing.T) {
	const threadID = "019cccc3abcd"

	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		snapshot := codexapp.Snapshot{
			Provider: codexapp.ProviderCodex,
			Started:  true,
			Preset:   req.Preset,
			Status:   "Codex session ready",
			ThreadID: threadID,
		}
		if len(requests) > 1 {
			snapshot.BusyExternal = true
		}
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot:    snapshot,
		}, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Preset:      codexcli.PresetYolo,
		ResumeID:    threadID,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	input := newCodexTextarea()
	input.SetValue("/new")

	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          input,
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              24,
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("enter should run the embedded /new command")
	}

	msg := cmd()
	opened, ok := msg.(codexSessionOpenedMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want codexSessionOpenedMsg", msg)
	}
	if opened.err != nil {
		t.Fatalf("/new returned error = %v", opened.err)
	}
	wantStatus := "Could not start a fresh embedded Codex session because session 019cccc3 is already active in another process. Showing that session read-only instead."
	if opened.status != wantStatus {
		t.Fatalf("opened.status = %q, want %q", opened.status, wantStatus)
	}

	updated, _ = got.Update(opened)
	got = updated.(Model)
	if got.status != wantStatus {
		t.Fatalf("status = %q, want %q", got.status, wantStatus)
	}
	if len(requests) != 2 {
		t.Fatalf("launch requests = %d, want 2", len(requests))
	}
	if !requests[1].ForceNew {
		t.Fatalf("second launch request should force a fresh session")
	}
}

func TestLaunchCodexForSelectionShowsOpeningStateInsteadOfPreviousSession(t *testing.T) {
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Started: true,
				Preset:  req.Preset,
				Status:  "Codex session ready",
			},
		}, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/previous",
		Preset:      codexcli.PresetYolo,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/previous",
		codexHiddenProject:  "/tmp/previous",
		codexInput:          newCodexTextarea(),
		codexDrafts:         make(map[string]codexDraft),
		codexViewport:       viewport.New(0, 0),
		projects: []model.ProjectSummary{{
			Name:          "next",
			Path:          "/tmp/next",
			PresentOnDisk: true,
		}},
		selected: 0,
		width:    100,
		height:   24,
	}

	updated, cmd := m.launchCodexForSelection(true, "")
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("launchCodexForSelection() should return an open command")
	}
	if got.codexPendingOpen == nil || got.codexPendingOpen.projectPath != "/tmp/next" {
		t.Fatalf("codexPendingOpen = %#v, want pending open for /tmp/next", got.codexPendingOpen)
	}
	if got.codexVisibleProject != "/tmp/previous" {
		t.Fatalf("codexVisibleProject = %q, want previous session to remain stored while opening", got.codexVisibleProject)
	}
	rendered := ansi.Strip(got.renderCodexView())
	if !strings.Contains(rendered, "Project: /tmp/next") {
		t.Fatalf("rendered opening view should mention the requested project, got %q", rendered)
	}
	if strings.Contains(rendered, "/tmp/previous") {
		t.Fatalf("rendered opening view should not keep showing the previous session, got %q", rendered)
	}
}

func TestLaunchCodexForSelectionForceNewRetriesWhenPreviousThreadReopensFirst(t *testing.T) {
	const previousThreadID = "019cccc3abcd"
	const newThreadID = "019dddd4efgh"

	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		threadID := previousThreadID
		if len(requests) > 1 {
			threadID = newThreadID
		}
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider: codexapp.ProviderCodex,
				Started:  true,
				Preset:   req.Preset,
				Status:   "Codex session ready",
				ThreadID: threadID,
			},
		}, nil
	})

	m := Model{
		codexManager: manager,
		projects: []model.ProjectSummary{{
			Path:                "/tmp/demo",
			Name:                "demo",
			PresentOnDisk:       true,
			LatestSessionID:     previousThreadID,
			LatestSessionFormat: "modern",
		}},
	}

	updated, cmd := m.launchCodexForSelection(true, "")
	if cmd == nil {
		t.Fatalf("launchCodexForSelection() should return an open command")
	}

	msg := cmd()
	opened, ok := msg.(codexSessionOpenedMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want codexSessionOpenedMsg", msg)
	}
	if opened.err != nil {
		t.Fatalf("/codex-new returned error = %v", opened.err)
	}
	if opened.status != "Embedded Codex session opened. Alt+Up hides it." {
		t.Fatalf("opened.status = %q, want normal opened status after retry", opened.status)
	}
	if len(requests) != 2 {
		t.Fatalf("launch requests = %d, want 2 after the automatic fresh-session retry", len(requests))
	}

	updated, _ = updated.(Model).Update(opened)
	got := updated.(Model)
	snapshot, ok := got.currentCodexSnapshot()
	if !ok {
		t.Fatalf("currentCodexSnapshot() unavailable after handling the opened session")
	}
	if snapshot.ThreadID != newThreadID {
		t.Fatalf("thread id = %q, want retried fresh thread %q", snapshot.ThreadID, newThreadID)
	}
}

func TestLaunchCodexForSelectionForceNewWarnsWhenActiveSessionIsReopenedReadOnly(t *testing.T) {
	const threadID = "019cccc3abcd"

	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider:     codexapp.ProviderCodex,
				Started:      true,
				Preset:       req.Preset,
				Status:       "Codex session ready",
				ThreadID:     threadID,
				BusyExternal: true,
			},
		}, nil
	})

	m := Model{
		codexManager: manager,
		projects: []model.ProjectSummary{{
			Path:                "/tmp/demo",
			Name:                "demo",
			PresentOnDisk:       true,
			LatestSessionID:     threadID,
			LatestSessionFormat: "modern",
		}},
	}

	updated, cmd := m.launchCodexForSelection(true, "")
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("launchCodexForSelection() should return an open command")
	}

	msg := cmd()
	opened, ok := msg.(codexSessionOpenedMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want codexSessionOpenedMsg", msg)
	}
	if opened.err != nil {
		t.Fatalf("/codex-new returned error = %v", opened.err)
	}
	wantStatus := "Could not start a fresh embedded Codex session because session 019cccc3 is already active in another process. Showing that session read-only instead."
	if opened.status != wantStatus {
		t.Fatalf("opened.status = %q, want %q", opened.status, wantStatus)
	}

	updated, _ = got.Update(opened)
	got = updated.(Model)
	if got.status != wantStatus {
		t.Fatalf("status = %q, want %q", got.status, wantStatus)
	}
}

func TestVisibleCodexAltEnterInsertsNewline(t *testing.T) {
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

	input := newCodexTextarea()
	input.SetValue("line 1")

	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          input,
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              24,
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyEnter, Alt: true})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("alt+enter should not send a command")
	}
	if got.codexInput.Value() != "line 1\n" {
		t.Fatalf("codex input = %q, want trailing newline", got.codexInput.Value())
	}
}

func TestVisibleCodexCtrlVAttachesClipboardImage(t *testing.T) {
	previousExporter := clipboardImageExporter
	clipboardImageExporter = func() (string, error) {
		return "/tmp/clipboard.png", nil
	}
	t.Cleanup(func() {
		clipboardImageExporter = previousExporter
	})

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
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          newCodexTextarea(),
		codexDrafts:         make(map[string]codexDraft),
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              24,
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyCtrlV})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("ctrl+v image attach should not queue a command")
	}
	attachments := got.currentCodexAttachments()
	if len(attachments) != 1 {
		t.Fatalf("attachments = %d, want 1", len(attachments))
	}
	if attachments[0].Path != "/tmp/clipboard.png" {
		t.Fatalf("attachment path = %q, want /tmp/clipboard.png", attachments[0].Path)
	}
	if got.codexInput.Value() != "[Image #1] " {
		t.Fatalf("composer = %q, want inline image marker", got.codexInput.Value())
	}
	if got.status != "Attached [Image #1]" {
		t.Fatalf("status = %q, want attachment notice", got.status)
	}
	rendered := ansi.Strip(got.renderCodexView())
	if !strings.Contains(rendered, "[Image #1]") {
		t.Fatalf("rendered view should show an inline image marker: %q", rendered)
	}
}

func TestVisibleCodexBackspaceRemovesInlineImageMarker(t *testing.T) {
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
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          newCodexTextarea(),
		codexDrafts:         make(map[string]codexDraft),
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              24,
	}

	previousExporter := clipboardImageExporter
	clipboardImageExporter = func() (string, error) {
		return "/tmp/clipboard.png", nil
	}
	t.Cleanup(func() {
		clipboardImageExporter = previousExporter
	})

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyCtrlV})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("ctrl+v image attach should not queue a command")
	}

	updated, cmd = got.updateCodexMode(tea.KeyMsg{Type: tea.KeyBackspace})
	got = updated.(Model)
	if cmd != nil {
		t.Fatalf("backspace marker removal should not queue a command")
	}
	if got.codexInput.Value() != "" {
		t.Fatalf("composer = %q, want marker removed", got.codexInput.Value())
	}
	attachments := got.currentCodexAttachments()
	if len(attachments) != 0 {
		t.Fatalf("attachments = %d, want 0", len(attachments))
	}
	if got.status != "Removed [Image #1]" {
		t.Fatalf("status = %q, want inline marker removal notice", got.status)
	}
}

func TestVisibleCodexCtrlVPastesLargeTextAsPlaceholder(t *testing.T) {
	previousExporter := clipboardImageExporter
	clipboardImageExporter = func() (string, error) {
		return "", errClipboardHasNoImage
	}
	t.Cleanup(func() {
		clipboardImageExporter = previousExporter
	})

	previousReader := clipboardTextReader
	clipboardTextReader = func() (string, error) {
		return strings.Repeat("a", 1200), nil
	}
	t.Cleanup(func() {
		clipboardTextReader = previousReader
	})

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
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          newCodexTextarea(),
		codexDrafts:         make(map[string]codexDraft),
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              24,
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyCtrlV})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("ctrl+v large text paste should not queue a command")
	}
	if got.codexInput.Value() != "[Paste #1: 1200 characters] " {
		t.Fatalf("composer = %q, want large paste placeholder", got.codexInput.Value())
	}
	pastedTexts := got.currentCodexPastedTexts()
	if len(pastedTexts) != 1 {
		t.Fatalf("pasted texts = %d, want 1", len(pastedTexts))
	}
	if pastedTexts[0].Text != strings.Repeat("a", 1200) {
		t.Fatalf("stored pasted text length = %d, want 1200", len([]rune(pastedTexts[0].Text)))
	}
	if got.status != "Pasted [1200 characters] as a placeholder" {
		t.Fatalf("status = %q, want placeholder notice", got.status)
	}
}

func TestVisibleCodexBracketedPasteUsesLargeTextPlaceholder(t *testing.T) {
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

	longText := strings.Repeat("b", 800)
	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          newCodexTextarea(),
		codexDrafts:         make(map[string]codexDraft),
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              24,
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(longText), Paste: true})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("bracketed large paste should not queue a command")
	}
	if got.codexInput.Value() != "[Paste #1: 800 characters] " {
		t.Fatalf("composer = %q, want bracketed paste placeholder", got.codexInput.Value())
	}
}

func TestVisibleCodexBackspaceRemovesLargePastePlaceholder(t *testing.T) {
	previousExporter := clipboardImageExporter
	clipboardImageExporter = func() (string, error) {
		return "", errClipboardHasNoImage
	}
	t.Cleanup(func() {
		clipboardImageExporter = previousExporter
	})

	previousReader := clipboardTextReader
	clipboardTextReader = func() (string, error) {
		return strings.Repeat("x", 900), nil
	}
	t.Cleanup(func() {
		clipboardTextReader = previousReader
	})

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
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          newCodexTextarea(),
		codexDrafts:         make(map[string]codexDraft),
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              24,
	}

	updated, _ := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyCtrlV})
	got := updated.(Model)
	updated, cmd := got.updateCodexMode(tea.KeyMsg{Type: tea.KeyBackspace})
	got = updated.(Model)
	if cmd != nil {
		t.Fatalf("backspace large placeholder removal should not queue a command")
	}
	if got.codexInput.Value() != "" {
		t.Fatalf("composer = %q, want placeholder removed", got.codexInput.Value())
	}
	if len(got.currentCodexPastedTexts()) != 0 {
		t.Fatalf("pasted texts = %d, want 0", len(got.currentCodexPastedTexts()))
	}
	if got.status != "Removed [900 characters] placeholder" {
		t.Fatalf("status = %q, want placeholder removal notice", got.status)
	}
}

func TestVisibleCodexSubmissionStripsInlineImageMarker(t *testing.T) {
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

	input := newCodexTextarea()
	input.SetValue("[Image #1] describe this")
	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          input,
		codexDrafts: map[string]codexDraft{
			"/tmp/demo": {
				Attachments: []codexapp.Attachment{
					{Kind: codexapp.AttachmentLocalImage, Path: "/tmp/one.png"},
				},
			},
		},
		codexViewport: viewport.New(0, 0),
		width:         100,
		height:        24,
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("enter should queue a submission command")
	}
	msg := cmd()
	action, ok := msg.(codexActionMsg)
	if !ok {
		t.Fatalf("cmd() = %T, want codexActionMsg", msg)
	}
	if action.err != nil {
		t.Fatalf("submission returned error = %v", action.err)
	}
	if len(session.submissions) != 1 {
		t.Fatalf("submissions = %d, want 1", len(session.submissions))
	}
	submission := session.submissions[0]
	if submission.Text != "describe this" {
		t.Fatalf("submission text = %q, want stripped prompt", submission.Text)
	}
	if len(submission.Attachments) != 1 || submission.Attachments[0].Path != "/tmp/one.png" {
		t.Fatalf("submission attachments = %#v, want one image", submission.Attachments)
	}
	if got.codexInput.Value() != "" {
		t.Fatalf("composer should clear after submit, got %q", got.codexInput.Value())
	}
}

func TestVisibleCodexSubmissionExpandsLargePastePlaceholder(t *testing.T) {
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

	hidden := strings.Repeat("p", 700)
	token := "[Paste #1: 700 characters]"
	input := newCodexTextarea()
	input.SetValue(token + " summarize this")
	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          input,
		codexDrafts: map[string]codexDraft{
			"/tmp/demo": {
				PastedTexts: []codexPastedText{{
					Token: token,
					Text:  hidden,
				}},
			},
		},
		codexViewport: viewport.New(0, 0),
		width:         100,
		height:        24,
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("enter should queue a submission command")
	}
	msg := cmd()
	action, ok := msg.(codexActionMsg)
	if !ok {
		t.Fatalf("cmd() = %T, want codexActionMsg", msg)
	}
	if action.err != nil {
		t.Fatalf("submission returned error = %v", action.err)
	}
	if len(session.submissions) != 1 {
		t.Fatalf("submissions = %d, want 1", len(session.submissions))
	}
	submission := session.submissions[0]
	if submission.Text != hidden+" summarize this" {
		t.Fatalf("submission text length = %d, want expanded hidden paste", len([]rune(submission.Text)))
	}
	if got.codexInput.Value() != "" {
		t.Fatalf("composer should clear after submit, got %q", got.codexInput.Value())
	}
}

func TestRenderCodexTranscriptCollapsesLargeUserPaste(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Started: true,
			Preset:  codexcli.PresetYolo,
			Status:  "Codex session ready",
			Entries: []codexapp.TranscriptEntry{
				{Kind: codexapp.TranscriptUser, Text: strings.Repeat("z", 650)},
			},
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
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          newCodexTextarea(),
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              24,
	}

	rendered := ansi.Strip(m.renderCodexView())
	if !strings.Contains(rendered, "[650 characters]") {
		t.Fatalf("rendered transcript should collapse the large user message: %q", rendered)
	}
	if strings.Contains(rendered, strings.Repeat("z", 80)) {
		t.Fatalf("rendered transcript should not include the full pasted text")
	}
}

func TestVisibleCodexCtrlCClosesIdleSession(t *testing.T) {
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
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          newCodexTextarea(),
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              24,
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyCtrlC})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("ctrl+c should close an idle embedded Codex session")
	}
	if got.status != "Closing embedded Codex session..." {
		t.Fatalf("status = %q, want closing notice", got.status)
	}

	msg := cmd()
	action, ok := msg.(codexActionMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want codexActionMsg", msg)
	}
	if !action.closed {
		t.Fatalf("close action should mark the session as closed")
	}
	if !session.snapshot.Closed {
		t.Fatalf("ctrl+c should close the backing session")
	}
}

func TestVisibleCodexCtrlCDoesNotInterruptExternalBusySession(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Started:      true,
			Preset:       codexcli.PresetYolo,
			Busy:         true,
			BusyExternal: true,
			ActiveTurnID: "turn-live",
			Status:       "Busy in another Codex process",
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
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          newCodexTextarea(),
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              24,
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyCtrlC})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("ctrl+c should not interrupt an external busy session")
	}
	if session.interrupted {
		t.Fatalf("session should not be interrupted")
	}
	if !strings.Contains(strings.ToLower(got.status), "another process") {
		t.Fatalf("status = %q, want clear busy-elsewhere message", got.status)
	}
}

func TestVisibleCodexEnterDoesNotSteerExternalBusySession(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Started:      true,
			Preset:       codexcli.PresetYolo,
			Busy:         true,
			BusyExternal: true,
			ActiveTurnID: "turn-live",
			Status:       "Busy in another Codex process",
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

	input := newCodexTextarea()
	input.SetValue("please continue")

	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          input,
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              24,
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("enter should not steer an external busy session")
	}
	if len(session.submissions) != 0 {
		t.Fatalf("submissions = %d, want 0", len(session.submissions))
	}
	if got.codexInput.Value() != "please continue" {
		t.Fatalf("composer = %q, want draft preserved", got.codexInput.Value())
	}
	if !strings.Contains(strings.ToLower(got.status), "another process") {
		t.Fatalf("status = %q, want clear busy-elsewhere message", got.status)
	}
}

func TestVisibleCodexAltUpHidesSession(t *testing.T) {
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
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          newCodexTextarea(),
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              24,
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyUp, Alt: true})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("alt+up hide should not queue a command")
	}
	if got.codexVisibleProject != "" {
		t.Fatalf("codexVisibleProject = %q, want hidden", got.codexVisibleProject)
	}
	if got.status != "Embedded Codex session hidden." {
		t.Fatalf("status = %q, want hide notice", got.status)
	}
}

func TestVisibleCodexEscHidesSession(t *testing.T) {
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
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          newCodexTextarea(),
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              24,
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyEsc})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("esc hide should not queue a command")
	}
	if got.codexVisibleProject != "" {
		t.Fatalf("codexVisibleProject = %q, want hidden", got.codexVisibleProject)
	}
	if got.status != "Embedded Codex session hidden." {
		t.Fatalf("status = %q, want hide notice", got.status)
	}
}

func TestClosedCodexUpdateTriggersScanOnlyOnce(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Started: true,
			Preset:  codexcli.PresetYolo,
			Closed:  true,
			Status:  "Codex app-server exited",
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
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          newCodexTextarea(),
		codexDrafts:         make(map[string]codexDraft),
		codexClosedHandled:  make(map[string]struct{}),
		codexToolAnswers:    make(map[string]codexToolAnswerState),
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              24,
	}

	updated, _ := m.Update(codexUpdateMsg{projectPath: "/tmp/demo"})
	got := updated.(Model)
	if !got.loading {
		t.Fatalf("first closed-session update should queue a refresh")
	}
	if got.status != "Codex app-server exited" {
		t.Fatalf("status = %q, want closed-session status", got.status)
	}
	if _, ok := got.codexClosedHandled["/tmp/demo"]; !ok {
		t.Fatalf("closed session should be marked as handled")
	}

	got.loading = false
	updated, _ = got.Update(codexUpdateMsg{projectPath: "/tmp/demo"})
	got = updated.(Model)
	if got.loading {
		t.Fatalf("duplicate closed-session updates should not queue another refresh")
	}
}

func TestVisibleCodexF3CyclesLiveSessions(t *testing.T) {
	sessionA := &fakeCodexSession{
		projectPath: "/tmp/a",
		snapshot: codexapp.Snapshot{
			Started:        true,
			Preset:         codexcli.PresetYolo,
			Status:         "Codex session ready",
			LastActivityAt: time.Date(2026, 3, 8, 10, 0, 0, 0, time.UTC),
		},
	}
	sessionB := &fakeCodexSession{
		projectPath: "/tmp/b",
		snapshot: codexapp.Snapshot{
			Started:        true,
			Preset:         codexcli.PresetYolo,
			Status:         "Codex session ready",
			LastActivityAt: time.Date(2026, 3, 8, 10, 5, 0, 0, time.UTC),
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		switch req.ProjectPath {
		case "/tmp/a":
			return sessionA, nil
		case "/tmp/b":
			return sessionB, nil
		default:
			return nil, fmt.Errorf("unexpected project %s", req.ProjectPath)
		}
	})
	for _, projectPath := range []string{"/tmp/a", "/tmp/b"} {
		if _, _, err := manager.Open(codexapp.LaunchRequest{
			ProjectPath: projectPath,
			Preset:      codexcli.PresetYolo,
		}); err != nil {
			t.Fatalf("manager.Open(%q) error = %v", projectPath, err)
		}
	}

	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/a",
		codexHiddenProject:  "/tmp/a",
		codexInput:          newCodexTextarea(),
		codexDrafts:         make(map[string]codexDraft),
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              24,
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyF3})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("f3 should focus the switched Codex composer")
	}
	if got.codexVisibleProject != "/tmp/b" {
		t.Fatalf("codexVisibleProject = %q, want /tmp/b", got.codexVisibleProject)
	}
	if got.status != "Switched to the next embedded Codex session" {
		t.Fatalf("status = %q, want switch notice", got.status)
	}
}

func TestVisibleCodexAltBracketCyclesLiveSessions(t *testing.T) {
	sessionA := &fakeCodexSession{
		projectPath: "/tmp/a",
		snapshot: codexapp.Snapshot{
			Started:        true,
			Preset:         codexcli.PresetYolo,
			Status:         "Codex session ready",
			LastActivityAt: time.Date(2026, 3, 8, 10, 0, 0, 0, time.UTC),
		},
	}
	sessionB := &fakeCodexSession{
		projectPath: "/tmp/b",
		snapshot: codexapp.Snapshot{
			Started:        true,
			Preset:         codexcli.PresetYolo,
			Status:         "Codex session ready",
			LastActivityAt: time.Date(2026, 3, 8, 10, 5, 0, 0, time.UTC),
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		switch req.ProjectPath {
		case "/tmp/a":
			return sessionA, nil
		case "/tmp/b":
			return sessionB, nil
		default:
			return nil, fmt.Errorf("unexpected project %s", req.ProjectPath)
		}
	})
	for _, projectPath := range []string{"/tmp/a", "/tmp/b"} {
		if _, _, err := manager.Open(codexapp.LaunchRequest{
			ProjectPath: projectPath,
			Preset:      codexcli.PresetYolo,
		}); err != nil {
			t.Fatalf("manager.Open(%q) error = %v", projectPath, err)
		}
	}

	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/a",
		codexHiddenProject:  "/tmp/a",
		codexInput:          newCodexTextarea(),
		codexDrafts:         make(map[string]codexDraft),
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              24,
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{']'}, Alt: true})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("alt+] should focus the switched Codex composer")
	}
	if got.codexVisibleProject != "/tmp/b" {
		t.Fatalf("codexVisibleProject = %q, want /tmp/b", got.codexVisibleProject)
	}
	if got.status != "Switched to the next embedded Codex session" {
		t.Fatalf("status = %q, want next-session notice", got.status)
	}

	updated, cmd = got.updateCodexMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'['}, Alt: true})
	got = updated.(Model)
	if cmd == nil {
		t.Fatalf("alt+[ should focus the switched Codex composer")
	}
	if got.codexVisibleProject != "/tmp/a" {
		t.Fatalf("codexVisibleProject = %q, want /tmp/a", got.codexVisibleProject)
	}
	if got.status != "Switched to the previous embedded Codex session" {
		t.Fatalf("status = %q, want previous-session notice", got.status)
	}
}

func TestVisibleCodexAltUpReturnsToLastEmbeddedProjectSelection(t *testing.T) {
	sessionA := &fakeCodexSession{
		projectPath: "/tmp/a",
		snapshot: codexapp.Snapshot{
			Started:        true,
			Preset:         codexcli.PresetYolo,
			Status:         "Codex session ready",
			LastActivityAt: time.Date(2026, 3, 8, 10, 0, 0, 0, time.UTC),
		},
	}
	sessionB := &fakeCodexSession{
		projectPath: "/tmp/b",
		snapshot: codexapp.Snapshot{
			Started:        true,
			Preset:         codexcli.PresetYolo,
			Status:         "Codex session ready",
			LastActivityAt: time.Date(2026, 3, 8, 10, 5, 0, 0, time.UTC),
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		switch req.ProjectPath {
		case "/tmp/a":
			return sessionA, nil
		case "/tmp/b":
			return sessionB, nil
		default:
			return nil, fmt.Errorf("unexpected project %s", req.ProjectPath)
		}
	})
	for _, projectPath := range []string{"/tmp/a", "/tmp/b"} {
		if _, _, err := manager.Open(codexapp.LaunchRequest{
			ProjectPath: projectPath,
			Preset:      codexcli.PresetYolo,
		}); err != nil {
			t.Fatalf("manager.Open(%q) error = %v", projectPath, err)
		}
	}

	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/a",
		codexHiddenProject:  "/tmp/a",
		codexInput:          newCodexTextarea(),
		codexDrafts:         make(map[string]codexDraft),
		codexViewport:       viewport.New(0, 0),
		projects: []model.ProjectSummary{
			{Path: "/tmp/a", Name: "a", PresentOnDisk: true},
			{Path: "/tmp/b", Name: "b", PresentOnDisk: true},
		},
		selected: 0,
		width:    100,
		height:   24,
	}

	updated, cycleCmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{']'}, Alt: true})
	got := updated.(Model)
	if cycleCmd == nil {
		t.Fatalf("alt+] should queue the session switch work")
	}
	if got.codexVisibleProject != "/tmp/b" {
		t.Fatalf("codexVisibleProject = %q, want /tmp/b", got.codexVisibleProject)
	}
	if project, ok := got.selectedProject(); !ok || project.Path != "/tmp/b" {
		t.Fatalf("selected project after alt+] = %#v, want /tmp/b", project)
	}

	updated, hideCmd := got.updateCodexMode(tea.KeyMsg{Type: tea.KeyUp, Alt: true})
	got = updated.(Model)
	if hideCmd == nil {
		t.Fatalf("alt+up should refresh the main list detail for the last embedded project")
	}
	if got.codexVisibleProject != "" {
		t.Fatalf("codexVisibleProject = %q, want hidden", got.codexVisibleProject)
	}
	if got.codexHiddenProject != "/tmp/b" {
		t.Fatalf("codexHiddenProject = %q, want /tmp/b", got.codexHiddenProject)
	}
	if project, ok := got.selectedProject(); !ok || project.Path != "/tmp/b" {
		t.Fatalf("selected project after alt+up = %#v, want /tmp/b", project)
	}
}

func TestAltDownOpensCodexSessionPicker(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Started:        true,
			Preset:         codexcli.PresetYolo,
			ThreadID:       "thread-demo",
			LastActivityAt: time.Date(2026, 3, 8, 12, 0, 0, 0, time.UTC),
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
		codexManager: manager,
		allProjects: []model.ProjectSummary{
			{
				Path:                "/tmp/demo",
				Name:                "demo",
				PresentOnDisk:       true,
				LatestSessionID:     "thread-demo",
				LatestSessionFormat: "modern",
				LastActivity:        time.Date(2026, 3, 8, 12, 0, 0, 0, time.UTC),
			},
		},
		codexInput:    newCodexTextarea(),
		codexViewport: viewport.New(0, 0),
		width:         100,
		height:        24,
	}

	updated, cmd := m.updateNormalMode(tea.KeyMsg{Type: tea.KeyDown, Alt: true})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("alt+down picker should not queue a command")
	}
	if !got.codexPickerVisible {
		t.Fatalf("codex picker should be visible")
	}
	rendered := ansi.Strip(got.View())
	if !strings.Contains(rendered, "Embedded Sessions") || !strings.Contains(rendered, "Source: Codex") || !strings.Contains(rendered, "demo") {
		t.Fatalf("picker overlay should render the session list: %q", rendered)
	}
}

func TestOpenCodexSessionChoiceLaunchesOpenCodeResume(t *testing.T) {
	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider: codexapp.ProviderOpenCode,
				Started:  true,
				ThreadID: req.ResumeID,
				Status:   "OpenCode session ready",
			},
		}, nil
	})

	m := Model{
		codexManager:  manager,
		codexInput:    newCodexTextarea(),
		codexViewport: viewport.New(0, 0),
		width:         100,
		height:        24,
	}

	updated, cmd := m.openCodexSessionChoice(codexSessionChoice{
		ProjectPath:  "/tmp/demo",
		ProjectName:  "demo",
		SessionID:    "ses_open",
		Provider:     codexapp.ProviderOpenCode,
		LastActivity: time.Date(2026, 3, 8, 12, 0, 0, 0, time.UTC),
	})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("openCodexSessionChoice() should return an open command")
	}
	if got.codexPendingOpen == nil || got.codexPendingOpen.provider != codexapp.ProviderOpenCode {
		t.Fatalf("codexPendingOpen = %#v, want pending OpenCode session", got.codexPendingOpen)
	}

	msg := cmd()
	opened, ok := msg.(codexSessionOpenedMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want codexSessionOpenedMsg", msg)
	}
	if opened.err != nil {
		t.Fatalf("open command returned error = %v", opened.err)
	}
	if len(requests) != 1 {
		t.Fatalf("launch requests = %d, want 1", len(requests))
	}
	if requests[0].Provider != codexapp.ProviderOpenCode {
		t.Fatalf("provider = %q, want %q", requests[0].Provider, codexapp.ProviderOpenCode)
	}
	if requests[0].ResumeID != "ses_open" {
		t.Fatalf("resume id = %q, want %q", requests[0].ResumeID, "ses_open")
	}
	if requests[0].Preset != "" {
		t.Fatalf("preset = %q, want empty for OpenCode", requests[0].Preset)
	}
}

func TestNormalModeEnterOpensPreferredOpenCodeSession(t *testing.T) {
	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider: codexapp.ProviderOpenCode,
				Started:  true,
				ThreadID: req.ResumeID,
				Status:   "OpenCode session ready",
			},
		}, nil
	})

	project := model.ProjectSummary{
		Path:                "/tmp/demo",
		Name:                "demo",
		PresentOnDisk:       true,
		LatestSessionID:     "latest-open",
		LatestSessionFormat: "opencode_db",
	}
	m := Model{
		codexManager: manager,
		projects:     []model.ProjectSummary{project},
		selected:     0,
		focusedPane:  focusProjects,
		detail: model.ProjectDetail{
			Summary: model.ProjectSummary{Path: "/tmp/demo"},
			Sessions: []model.SessionEvidence{
				{SessionID: "ses_open", Format: "opencode_db"},
				{SessionID: "cx_old", Format: "modern"},
			},
		},
		codexInput:    newCodexTextarea(),
		codexViewport: viewport.New(0, 0),
		width:         100,
		height:        24,
	}

	updated, cmd := m.updateNormalMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("enter should return an open command")
	}
	if got.codexPendingOpen == nil || got.codexPendingOpen.provider != codexapp.ProviderOpenCode {
		t.Fatalf("codexPendingOpen = %#v, want pending OpenCode session", got.codexPendingOpen)
	}

	msg := cmd()
	opened, ok := msg.(codexSessionOpenedMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want codexSessionOpenedMsg", msg)
	}
	if opened.err != nil {
		t.Fatalf("open command returned error = %v", opened.err)
	}
	if len(requests) != 1 {
		t.Fatalf("launch requests = %d, want 1", len(requests))
	}
	if requests[0].Provider != codexapp.ProviderOpenCode {
		t.Fatalf("provider = %q, want %q", requests[0].Provider, codexapp.ProviderOpenCode)
	}
	if requests[0].ResumeID != "ses_open" {
		t.Fatalf("resume id = %q, want %q", requests[0].ResumeID, "ses_open")
	}
}

func TestPendingToolInputEnterSendsStructuredAnswer(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Started: true,
			Preset:  codexcli.PresetYolo,
			Status:  "Waiting for structured user input",
			PendingToolInput: &codexapp.ToolInputRequest{
				ID: "req_1",
				Questions: []codexapp.ToolInputQuestion{
					{
						ID:       "answer",
						Header:   "Reason",
						Question: "Why should we do this?",
					},
				},
			},
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

	input := newCodexTextarea()
	input.SetValue("Because it removes friction.")

	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          input,
		codexToolAnswers:    make(map[string]codexToolAnswerState),
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              24,
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("enter should submit the structured answer")
	}
	if got.status != "Sending structured input..." {
		t.Fatalf("status = %q, want sending structured input notice", got.status)
	}

	msg := cmd()
	action, ok := msg.(codexActionMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want codexActionMsg", msg)
	}
	if action.status != "Structured input sent to Codex" {
		t.Fatalf("action status = %q, want structured input notice", action.status)
	}
	if len(session.toolAnswers) != 1 {
		t.Fatalf("tool answers = %d, want 1", len(session.toolAnswers))
	}
	if got := session.toolAnswers[0]["answer"]; len(got) != 1 || got[0] != "Because it removes friction." {
		t.Fatalf("tool answer = %#v, want submitted text", got)
	}
}

func TestVisibleCodexViewShowsBannerAndYoloWarning(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Started:         true,
			Preset:          codexcli.PresetYolo,
			Status:          "Codex session ready",
			Model:           "gpt-5-codex",
			ReasoningEffort: "high",
			Transcript:      "Codex: hello",
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
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          newCodexTextarea(),
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              24,
	}
	m.syncCodexViewport(true)

	rendered := m.View()
	if !strings.Contains(rendered, "Codex | demo") {
		t.Fatalf("embedded Codex view should use a compact Codex banner: %q", rendered)
	}
	if !strings.Contains(rendered, "YOLO MODE") {
		t.Fatalf("embedded Codex view should show YOLO warning: %q", rendered)
	}
	if !strings.Contains(rendered, "hello") {
		t.Fatalf("embedded Codex view should render transcript: %q", rendered)
	}
	if strings.Contains(rendered, "Codex: hello") {
		t.Fatalf("embedded Codex view should not repeat the sender label in fallback transcript rendering: %q", rendered)
	}
	lines := strings.Split(ansi.Strip(rendered), "\n")
	if len(lines) == 0 {
		t.Fatalf("rendered view should have lines")
	}
	if len(lines) != m.height {
		t.Fatalf("embedded Codex view line count = %d, want %d; render was %q", len(lines), m.height, ansi.Strip(rendered))
	}
	if strings.Contains(lines[0], "Little Control Room - Control Center for AI Tasks") {
		t.Fatalf("embedded Codex banner should omit the full app title: %q", lines[0])
	}
	if !strings.Contains(lines[0], "YOLO MODE") {
		t.Fatalf("YOLO warning should live on the top banner: %q", lines[0])
	}
	if !strings.HasSuffix(strings.TrimRight(lines[0], " "), "YOLO MODE") {
		t.Fatalf("YOLO warning should be overlaid at the banner's right edge: %q", lines[0])
	}
	if !strings.Contains(lines[0], " YOLO MODE") {
		t.Fatalf("YOLO warning should keep a small spacer from the banner text: %q", lines[0])
	}
	if strings.Count(ansi.Strip(rendered), "YOLO MODE") != 1 {
		t.Fatalf("YOLO warning should appear exactly once: %q", ansi.Strip(rendered))
	}
	if !strings.Contains(lines[0], "Alt+Down picker") || !strings.Contains(lines[0], "Alt+L blocks") {
		t.Fatalf("embedded Codex view should keep picker/session shortcuts on the banner line: %q", rendered)
	}
	if len(lines) > 1 && strings.Contains(lines[1], "Alt+Down picker") {
		t.Fatalf("embedded Codex view should not spend a separate row on banner shortcuts: %q", rendered)
	}
	for _, line := range lines {
		if ansi.StringWidth(line) > m.width {
			t.Fatalf("embedded Codex view line width = %d, want <= %d: %q", ansi.StringWidth(line), m.width, line)
		}
	}
}

func TestRenderCodexSessionMetaShowsModelReasoningContextAndPending(t *testing.T) {
	rendered := ansi.Strip((Model{}).renderCodexSessionMeta(codexapp.Snapshot{
		Model:            "gpt-5-codex",
		ReasoningEffort:  "high",
		PendingModel:     "gpt-5",
		PendingReasoning: "medium",
		TokenUsage: &codexapp.TokenUsageSnapshot{
			Last: codexapp.TokenUsageBreakdown{
				InputTokens:           10000,
				OutputTokens:          2345,
				ReasoningOutputTokens: 345,
				TotalTokens:           12345,
			},
			Total: codexapp.TokenUsageBreakdown{
				TotalTokens: 12345,
			},
			ModelContextWindow: 200000,
		},
	}, 140))

	for _, want := range []string{"Model", "gpt-5-codex", "Reasoning", "high", "Context", "94% left", "188,000 tok", "Next", "gpt-5 / medium"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("renderCodexSessionMeta() missing %q: %q", want, rendered)
		}
	}
}

func TestVisibleCodexViewShowsBusyElsewhereWarningBlock(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Started:          true,
			Preset:           codexcli.PresetYolo,
			Busy:             true,
			BusyExternal:     true,
			ThreadID:         "019cccc3abcdef",
			LastSystemNotice: "Resumed embedded Codex session 019cccc3. It is already active in another Codex process, so embedded controls are read-only until it finishes.",
			Entries: []codexapp.TranscriptEntry{
				{Kind: codexapp.TranscriptAgent, Text: "Still waiting on the other run."},
			},
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
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          newCodexTextarea(),
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              24,
	}
	m.syncCodexViewport(true)

	rendered := ansi.Strip(m.renderCodexView())
	if !strings.Contains(rendered, "Read-only") {
		t.Fatalf("busy-elsewhere view should show a read-only warning block: %q", rendered)
	}
	if !strings.Contains(rendered, "Resumed embedded Codex session 019cccc3") {
		t.Fatalf("busy-elsewhere view should surface the resume warning prominently: %q", rendered)
	}
}

func TestVisibleCodexViewUsesPromptInsteadOfPlaceholder(t *testing.T) {
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
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          newCodexTextarea(),
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              24,
	}
	m.syncCodexViewport(true)

	rendered := ansi.Strip(m.renderCodexView())
	if strings.Contains(rendered, "Message Codex") {
		t.Fatalf("embedded Codex composer should not render the old placeholder: %q", rendered)
	}
	if !strings.Contains(rendered, "> ") {
		t.Fatalf("embedded Codex composer should render a prompt marker when empty: %q", rendered)
	}
}

func TestVisibleCodexViewUsesStructuredTranscriptEntries(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Started: true,
			Preset:  codexcli.PresetYolo,
			Status:  "Codex session ready",
			Entries: []codexapp.TranscriptEntry{
				{Kind: codexapp.TranscriptUser, Text: "summarize this repo"},
				{Kind: codexapp.TranscriptAgent, Text: "Here is a quick summary."},
				{Kind: codexapp.TranscriptCommand, Text: "$ git status --short\n# cwd: /tmp/demo\n M README.md\n[command completed, exit 0]"},
			},
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
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          newCodexTextarea(),
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              24,
	}
	m.syncCodexViewport(true)

	rendered := ansi.Strip(m.View())
	if !strings.Contains(rendered, "summarize this repo") {
		t.Fatalf("structured transcript should show a user block: %q", rendered)
	}
	if !strings.Contains(rendered, "Here is a quick summary.") {
		t.Fatalf("structured transcript should show an agent block: %q", rendered)
	}
	if !strings.Contains(rendered, "Command") || !strings.Contains(rendered, "$ git status --short") {
		t.Fatalf("structured transcript should render command blocks: %q", rendered)
	}
}

func TestVisibleCodexViewStripsTerminalEscapeSequencesFromCommandOutput(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Started: true,
			Preset:  codexcli.PresetYolo,
			Status:  "Codex session ready",
			Entries: []codexapp.TranscriptEntry{
				{
					Kind: codexapp.TranscriptCommand,
					Text: "$ make tui\n\x1b[?1049h\x1b[2J\x1b[HLittle Control Room\n\x1b[?1049l[command completed, exit 0]",
				},
			},
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
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          newCodexTextarea(),
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              24,
	}
	m.syncCodexViewport(true)

	rendered := m.renderCodexView()
	if strings.Contains(rendered, "\x1b[?1049h") || strings.Contains(rendered, "\x1b[2J") {
		t.Fatalf("embedded Codex view should strip nested terminal control sequences from transcript output: %q", rendered)
	}
	stripped := ansi.Strip(rendered)
	if !strings.Contains(stripped, "Little Control Room") {
		t.Fatalf("embedded Codex view should preserve readable command output after stripping terminal escapes: %q", stripped)
	}
}

func TestRenderCodexTranscriptEntriesWrapLongAgentMessagesWithoutSenderLabels(t *testing.T) {
	snapshot := codexapp.Snapshot{
		Entries: []codexapp.TranscriptEntry{
			{
				Kind: codexapp.TranscriptAgent,
				Text: "This is a deliberately long Codex reply that should wrap across multiple lines inside the embedded transcript pane instead of being truncated off the edge.",
			},
		},
	}

	rendered := ansi.Strip((Model{}).renderCodexTranscriptEntries(snapshot, 28))
	if strings.Contains(rendered, "Codex:") {
		t.Fatalf("structured transcript should not repeat the agent sender label: %q", rendered)
	}

	lines := strings.Split(rendered, "\n")
	if len(lines) < 2 {
		t.Fatalf("long agent message should wrap to multiple lines: %q", rendered)
	}
	for _, line := range lines {
		if ansi.StringWidth(line) > 28 {
			t.Fatalf("wrapped line width = %d, want <= 28: %q", ansi.StringWidth(line), line)
		}
	}
}

func TestRenderCodexTranscriptEntryHighlightsUserEchoBlock(t *testing.T) {
	prevProfile := lipgloss.ColorProfile()
	prevDarkBackground := lipgloss.HasDarkBackground()
	lipgloss.SetColorProfile(termenv.ANSI256)
	lipgloss.SetHasDarkBackground(true)
	t.Cleanup(func() {
		lipgloss.SetColorProfile(prevProfile)
		lipgloss.SetHasDarkBackground(prevDarkBackground)
	})

	userRendered := renderCodexTranscriptEntry(codexapp.TranscriptEntry{
		Kind: codexapp.TranscriptUser,
		Text: "summarize this repo",
	}, 36, false)
	if !strings.Contains(userRendered, "48;5;"+string(codexComposerShellColor)) {
		t.Fatalf("user transcript entry should reuse the composer background color: %q", userRendered)
	}

	agentRendered := renderCodexTranscriptEntry(codexapp.TranscriptEntry{
		Kind: codexapp.TranscriptAgent,
		Text: "Here is a quick summary.",
	}, 36, false)
	if strings.Contains(agentRendered, "48;5;"+string(codexComposerShellColor)) {
		t.Fatalf("agent transcript entry should not inherit the user echo background: %q", agentRendered)
	}
}

func TestRenderCodexTranscriptEntriesParsesLegacyTranscriptWithoutSenderLabels(t *testing.T) {
	snapshot := codexapp.Snapshot{
		Transcript: "You: summarize this repo\n\nCodex: Here is a deliberately long answer that should wrap inside the pane without repeating the sender name on screen.",
	}

	rendered := ansi.Strip((Model{}).renderCodexTranscriptEntries(snapshot, 30))
	normalized := strings.Join(strings.Fields(rendered), " ")
	if !strings.Contains(normalized, "summarize this repo") || !strings.Contains(normalized, "Here is a deliberately long answer") {
		t.Fatalf("legacy transcript fallback should still render the conversation text: %q", rendered)
	}
	if strings.Contains(rendered, "You:") || strings.Contains(rendered, "Codex:") {
		t.Fatalf("legacy transcript fallback should hide sender labels: %q", rendered)
	}
	for _, line := range strings.Split(rendered, "\n") {
		if ansi.StringWidth(line) > 30 {
			t.Fatalf("legacy wrapped line width = %d, want <= 30: %q", ansi.StringWidth(line), line)
		}
	}
}

func TestRenderCodexTranscriptEntriesRendersEmbeddedStatusCard(t *testing.T) {
	snapshot := codexapp.Snapshot{
		Entries: []codexapp.TranscriptEntry{
			{
				Kind: codexapp.TranscriptStatus,
				Text: strings.Join([]string{
					"Embedded Codex status",
					"model: gpt-5.4",
					"model provider: openai",
					"reasoning effort: high",
					"service tier: auto",
					"cwd: /tmp/demo",
					"total tokens: 12345",
					"model context window: 200000",
					"context tokens: 12000",
					"context used percent: 6",
					"last turn tokens: 4321",
					"usage window: limit=Codex; plan=Pro; window=5h; left=85; resetsAt=1773027840",
					"usage window: limit=Codex; plan=Pro; window=weekly; left=88; resetsAt=1773200640",
					"usage window: limit=GPT-5.3-Codex-Spark; window=5h; left=100; resetsAt=1773027840",
				}, "\n"),
			},
		},
	}

	rendered := ansi.Strip((Model{}).renderCodexTranscriptEntries(snapshot, 80))
	checks := []string{
		"Status",
		"Model:",
		"gpt-5.4",
		"Reasoning:",
		"high",
		"Usage left",
		"Codex (Pro)",
		"5h limit",
		"85% left",
		"Weekly limit",
		"88% left",
		"Context:",
		"12,000 tokens",
		"Last turn:",
		"4,321 tokens",
		"resets ",
	}
	for _, want := range checks {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered embedded status card should include %q: %q", want, rendered)
		}
	}
}

func TestRenderCodexTranscriptEntriesRendersOpenCodeStatusCard(t *testing.T) {
	snapshot := codexapp.Snapshot{
		Entries: []codexapp.TranscriptEntry{
			{
				Kind: codexapp.TranscriptStatus,
				Text: strings.Join([]string{
					"Embedded OpenCode status",
					"model: gpt-5.4",
					"model provider: openai",
					"reasoning effort: high",
					"agent: build",
					"cwd: /tmp/demo",
					"total tokens: 12345",
					"last turn tokens: 4321",
				}, "\n"),
			},
		},
	}

	rendered := ansi.Strip((Model{}).renderCodexTranscriptEntries(snapshot, 80))
	for _, want := range []string{"Status", "Model:", "gpt-5.4", "Reasoning:", "high", "Agent:", "build", "Last turn:", "4,321 tokens"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered OpenCode status card should include %q: %q", want, rendered)
		}
	}
}

func TestRenderCodexTranscriptEntriesRendersLocalMarkdownLinksAsTerminalHyperlinks(t *testing.T) {
	snapshot := codexapp.Snapshot{
		Entries: []codexapp.TranscriptEntry{
			{
				Kind: codexapp.TranscriptAgent,
				Text: "See [README](/tmp/demo/README.md).",
			},
		},
	}

	rendered := (Model{}).renderCodexTranscriptEntries(snapshot, 80)
	if !strings.Contains(rendered, ansi.SetHyperlink("file:///tmp/demo/README.md")) {
		t.Fatalf("rendered transcript should include a clickable file hyperlink escape sequence: %q", rendered)
	}
	stripped := ansi.Strip(rendered)
	if strings.Contains(stripped, "[README](/tmp/demo/README.md)") {
		t.Fatalf("rendered transcript should hide markdown link syntax once rendered: %q", stripped)
	}
	if !strings.Contains(stripped, "See README.") {
		t.Fatalf("rendered transcript should preserve the local link label in the visible text: %q", stripped)
	}
}

func TestRenderCodexTranscriptEntriesKeepsHTTPSMarkdownLinksClickable(t *testing.T) {
	snapshot := codexapp.Snapshot{
		Entries: []codexapp.TranscriptEntry{
			{
				Kind: codexapp.TranscriptAgent,
				Text: "See [docs](https://example.com/docs).",
			},
		},
	}

	rendered := (Model{}).renderCodexTranscriptEntries(snapshot, 80)
	if !strings.Contains(rendered, ansi.SetHyperlink("https://example.com/docs")) {
		t.Fatalf("rendered transcript should include an https hyperlink escape sequence: %q", rendered)
	}

	stripped := ansi.Strip(rendered)
	if strings.Contains(stripped, "[docs](https://example.com/docs)") {
		t.Fatalf("rendered transcript should hide markdown link syntax once rendered: %q", stripped)
	}
	if !strings.Contains(stripped, "See docs.") {
		t.Fatalf("rendered transcript should preserve the external link label in the visible text: %q", stripped)
	}
}

func TestRenderCodexTranscriptEntriesHighlightsFencedCodeBlocks(t *testing.T) {
	prevProfile := lipgloss.ColorProfile()
	prevDarkBackground := lipgloss.HasDarkBackground()
	lipgloss.SetColorProfile(termenv.ANSI256)
	lipgloss.SetHasDarkBackground(true)
	t.Cleanup(func() {
		lipgloss.SetColorProfile(prevProfile)
		lipgloss.SetHasDarkBackground(prevDarkBackground)
	})

	goSnapshot := codexapp.Snapshot{
		Entries: []codexapp.TranscriptEntry{{
			Kind: codexapp.TranscriptAgent,
			Text: "Use this helper:\n```go\nfunc main() {\n    if err != nil {\n        return err\n    }\n}\n```",
		}},
	}
	textSnapshot := codexapp.Snapshot{
		Entries: []codexapp.TranscriptEntry{{
			Kind: codexapp.TranscriptAgent,
			Text: "Use this helper:\n```text\nfunc main() {\n    if err != nil {\n        return err\n    }\n}\n```",
		}},
	}

	goRendered := (Model{}).renderCodexTranscriptEntries(goSnapshot, 80)
	textRendered := (Model{}).renderCodexTranscriptEntries(textSnapshot, 80)

	for _, rendered := range []string{goRendered, textRendered} {
		stripped := ansi.Strip(rendered)
		if !strings.Contains(stripped, "func main() {") || !strings.Contains(stripped, "return err") {
			t.Fatalf("fenced-code rendering should preserve the visible code text: %q", stripped)
		}
	}
	if !strings.Contains(goRendered, "\x1b[") {
		t.Fatalf("Go fenced block should include ANSI styling: %q", goRendered)
	}
	if goRendered == textRendered {
		t.Fatalf("language-tagged fenced block should render differently from plain-text fenced block")
	}
}

func TestSourceStyleDimsNonLiveOpenCodeBadge(t *testing.T) {
	prevProfile := lipgloss.ColorProfile()
	prevDarkBackground := lipgloss.HasDarkBackground()
	lipgloss.SetColorProfile(termenv.ANSI256)
	lipgloss.SetHasDarkBackground(true)
	t.Cleanup(func() {
		lipgloss.SetColorProfile(prevProfile)
		lipgloss.SetHasDarkBackground(prevDarkBackground)
	})

	nonLive := sourceStyle("opencode_db", false).Render("OC")
	live := sourceStyle("opencode_db", true).Render("OC")

	if ansi.Strip(nonLive) != "OC" || ansi.Strip(live) != "OC" {
		t.Fatalf("source badge text should stay visible: non-live=%q live=%q", ansi.Strip(nonLive), ansi.Strip(live))
	}
	if nonLive == live {
		t.Fatalf("non-live OpenCode badge should render differently from a live badge: non-live=%q live=%q", nonLive, live)
	}
}

func TestVisibleCodexViewHidesSessionApprovalShortcutForFileChanges(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Started: true,
			Preset:  codexcli.PresetSafe,
			Status:  "Waiting for file change approval",
			PendingApproval: &codexapp.ApprovalRequest{
				Kind:      codexapp.ApprovalFileChange,
				GrantRoot: "/tmp/demo",
			},
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Preset:      codexcli.PresetSafe,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          newCodexTextarea(),
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              24,
	}
	m.syncCodexViewport(true)

	rendered := m.View()
	if strings.Contains(rendered, "A session") {
		t.Fatalf("file change approval should not advertise session-wide approval: %q", rendered)
	}
	if !strings.Contains(rendered, "a accept  d decline  c cancel  Alt+Up hide") {
		t.Fatalf("file change approval footer missing expected keys: %q", rendered)
	}
}

func TestStoreCodexSnapshotOnlyInvalidatesTranscriptRevisionWhenTranscriptChanges(t *testing.T) {
	m := Model{}
	projectPath := "/tmp/demo"
	base := codexapp.Snapshot{
		Provider: codexapp.ProviderCodex,
		Entries: []codexapp.TranscriptEntry{{
			Kind: codexapp.TranscriptAgent,
			Text: "First reply",
		}},
	}

	m.storeCodexSnapshot(projectPath, base)
	if got := m.codexTranscriptRevision(projectPath); got != 1 {
		t.Fatalf("initial transcript revision = %d, want 1", got)
	}

	statusOnly := base
	statusOnly.Status = "Codex is working..."
	m.storeCodexSnapshot(projectPath, statusOnly)
	if got := m.codexTranscriptRevision(projectPath); got != 1 {
		t.Fatalf("status-only update should not bump transcript revision, got %d", got)
	}

	changed := base
	changed.Entries = []codexapp.TranscriptEntry{{
		Kind: codexapp.TranscriptAgent,
		Text: "Updated reply",
	}}
	m.storeCodexSnapshot(projectPath, changed)
	if got := m.codexTranscriptRevision(projectPath); got != 2 {
		t.Fatalf("transcript update should bump transcript revision, got %d", got)
	}
}

func TestVisibleCodexViewUsesCachedSnapshotWhileTyping(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Started: true,
			Preset:  codexcli.PresetYolo,
			Status:  "Codex session ready",
			Entries: []codexapp.TranscriptEntry{{
				Kind: codexapp.TranscriptAgent,
				Text: "Existing reply",
			}},
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

	input := newCodexTextarea()
	input.SetValue("hello")
	input.Focus()

	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          input,
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              24,
	}
	if _, ok := m.refreshCodexSnapshot("/tmp/demo"); !ok {
		t.Fatalf("refreshCodexSnapshot() failed")
	}
	m.syncCodexViewport(true)

	callsAfterSync := session.snapshotCalls
	if callsAfterSync == 0 {
		t.Fatalf("expected the initial snapshot refresh to read the session")
	}

	rendered := ansi.Strip(m.View())
	if !strings.Contains(rendered, "Existing reply") {
		t.Fatalf("View() missing transcript content: %q", rendered)
	}
	if session.snapshotCalls != callsAfterSync {
		t.Fatalf("View() should reuse the cached snapshot after sync; snapshot calls = %d, want %d", session.snapshotCalls, callsAfterSync)
	}

	updated, _ := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("!")})
	got := updated.(Model)
	_ = got.View()
	if session.snapshotCalls != callsAfterSync {
		t.Fatalf("typing should not reread the session snapshot; snapshot calls = %d, want %d", session.snapshotCalls, callsAfterSync)
	}
	if got.codexInput.Value() != "hello!" {
		t.Fatalf("codex input = %q, want appended text", got.codexInput.Value())
	}
}

func TestSelectedProjectCodexSessionIDPrefersDetailCodexSession(t *testing.T) {
	project := model.ProjectSummary{
		Path:                "/tmp/demo",
		Name:                "demo",
		PresentOnDisk:       true,
		LatestSessionID:     "latest-opencode",
		LatestSessionFormat: "opencode_db",
	}
	m := Model{
		projects: []model.ProjectSummary{project},
		selected: 0,
		detail: model.ProjectDetail{
			Summary: model.ProjectSummary{Path: "/tmp/demo"},
			Sessions: []model.SessionEvidence{
				{SessionID: "op_1", Format: "opencode_db"},
				{SessionID: "cx_2", Format: "modern"},
				{SessionID: "cx_1", Format: "legacy"},
			},
		},
	}

	got := m.selectedProjectCodexSessionID(project)
	if got != "cx_2" {
		t.Fatalf("selectedProjectCodexSessionID() = %q, want %q", got, "cx_2")
	}
}

func TestSelectedProjectSessionIDPrefersDetailOpenCodeSession(t *testing.T) {
	project := model.ProjectSummary{
		Path:                "/tmp/demo",
		Name:                "demo",
		PresentOnDisk:       true,
		LatestSessionID:     "cx_summary",
		LatestSessionFormat: "modern",
	}
	m := Model{
		projects: []model.ProjectSummary{project},
		selected: 0,
		detail: model.ProjectDetail{
			Summary: model.ProjectSummary{Path: "/tmp/demo"},
			Sessions: []model.SessionEvidence{
				{SessionID: "cx_2", Format: "modern"},
				{SessionID: "op_3", Format: "opencode_db"},
			},
		},
	}

	got := m.selectedProjectSessionID(project, codexapp.ProviderOpenCode)
	if got != "op_3" {
		t.Fatalf("selectedProjectSessionID() = %q, want %q", got, "op_3")
	}
}

func TestSelectedProjectSessionIDPrefersLiveEmbeddedSession(t *testing.T) {
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider: codexapp.ProviderCodex,
				Started:  true,
				ThreadID: "thread-live",
				Status:   "Codex session ready",
			},
		}, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Preset:      codexcli.PresetYolo,
		ResumeID:    "thread-live",
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	project := model.ProjectSummary{
		Path:                "/tmp/demo",
		Name:                "demo",
		PresentOnDisk:       true,
		LatestSessionID:     "cx_summary",
		LatestSessionFormat: "modern",
	}
	m := Model{
		codexManager: manager,
		projects:     []model.ProjectSummary{project},
		selected:     0,
		detail: model.ProjectDetail{
			Summary: model.ProjectSummary{Path: "/tmp/demo"},
			Sessions: []model.SessionEvidence{
				{SessionID: "cx_2", Format: "modern"},
			},
		},
	}

	got := m.selectedProjectSessionID(project, codexapp.ProviderCodex)
	if got != "thread-live" {
		t.Fatalf("selectedProjectSessionID() = %q, want %q", got, "thread-live")
	}
}

func TestRenderCodexFooterPrioritizesSendCloseHideAndDefersDenseBlocks(t *testing.T) {
	rendered := ansi.Strip((Model{}).renderCodexFooter(codexapp.Snapshot{
		Started: true,
		Status:  "Codex session ready",
	}, 140))

	enterIndex := strings.Index(rendered, "Enter send")
	closeIndex := strings.Index(rendered, "Ctrl+C close")
	hideIndex := strings.Index(rendered, "Alt+Up hide")
	if enterIndex < 0 || closeIndex < 0 || hideIndex < 0 {
		t.Fatalf("renderCodexFooter() missing expected footer actions: %q", rendered)
	}
	if !(enterIndex < closeIndex && closeIndex < hideIndex) {
		t.Fatalf("renderCodexFooter() order = %q, want Enter send before Ctrl+C close before Alt+Up hide", rendered)
	}
	for _, hidden := range []string{"Alt+Down picker", "Alt+[ prev", "Alt+] next", "Alt+L blocks"} {
		if strings.Contains(rendered, hidden) {
			t.Fatalf("renderCodexFooter() should promote %q out of the footer: %q", hidden, rendered)
		}
	}
}

func TestRenderCodexFooterAnimatesBusyStatus(t *testing.T) {
	prevProfile := lipgloss.ColorProfile()
	prevDarkBackground := lipgloss.HasDarkBackground()
	lipgloss.SetColorProfile(termenv.ANSI256)
	lipgloss.SetHasDarkBackground(true)
	t.Cleanup(func() {
		lipgloss.SetColorProfile(prevProfile)
		lipgloss.SetHasDarkBackground(prevDarkBackground)
	})

	now := time.Date(2026, 3, 13, 15, 4, 5, 0, time.UTC)
	snapshot := codexapp.Snapshot{
		Busy:      true,
		BusySince: now.Add(-12 * time.Minute),
		Status:    "Codex is working...",
	}

	base := Model{
		nowFn: func() time.Time { return now },
	}
	renderedA := base.renderCodexFooter(snapshot, 140)
	base.spinnerFrame = 6
	renderedB := base.renderCodexFooter(snapshot, 140)

	if stripped := ansi.Strip(renderedA); !strings.Contains(stripped, "Working 12:00") {
		t.Fatalf("renderCodexFooter() missing busy status text: %q", stripped)
	}
	if ansi.Strip(renderedA) != ansi.Strip(renderedB) {
		t.Fatalf("busy footer should keep the same visible text while animating: %q vs %q", ansi.Strip(renderedA), ansi.Strip(renderedB))
	}
	if renderedA == renderedB {
		t.Fatalf("busy footer should animate across spinner frames")
	}
	if !strings.Contains(renderedA, "\x1b[") {
		t.Fatalf("busy footer should include ANSI styling while active: %q", renderedA)
	}
	statusSegment := strings.SplitN(renderedA, "  ", 2)[0]
	for _, legacy := range []string{"38;5;81", "38;5;117", "38;5;153", "38;5;178", "38;5;214", "38;5;221"} {
		if strings.Contains(statusSegment, legacy) {
			t.Fatalf("busy footer should use the neutral gray ramp instead of legacy colorful code %q: %q", legacy, statusSegment)
		}
	}
}

func TestCodexBusyGradientWrapsContinuously(t *testing.T) {
	phase := codexBusyGradientPhase(17)
	start := codexBusyGradientGrayLevel(0, phase)
	end := codexBusyGradientGrayLevel(1, phase)

	if math.Abs(float64(start-end)) > 0.0001 {
		t.Fatalf("wrapped busy gradient should match at the seam: start=%d end=%d phase=%v", start, end, phase)
	}
}

func TestSpinnerTickKeepsHighResolutionAnimationFrames(t *testing.T) {
	base := Model{spinnerFrame: len(spinnerFrames) - 1}

	nextModel, _ := base.Update(spinnerTickMsg{})
	next, ok := nextModel.(Model)
	if !ok {
		t.Fatalf("Update() returned %T, want tui.Model", nextModel)
	}
	if next.spinnerFrame != len(spinnerFrames) {
		t.Fatalf("spinnerFrame = %d, want %d so gradients are not limited to spinner glyph count", next.spinnerFrame, len(spinnerFrames))
	}
}

func TestRenderCodexBannerPromotesPickerPrevNextAndBlocks(t *testing.T) {
	rendered := ansi.Strip((Model{}).renderCodexBanner(codexapp.Snapshot{
		Started:     true,
		Status:      "Codex session ready",
		ProjectPath: "/tmp/demo",
	}, 140))

	for _, expected := range []string{"Codex | demo", "Alt+Down picker", "Alt+[ prev", "Alt+] next", "Alt+L blocks"} {
		if !strings.Contains(rendered, expected) {
			t.Fatalf("renderCodexBanner() missing %q: %q", expected, rendered)
		}
	}
}

func TestSelectedProjectCodexSessionIDFallsBackToSummary(t *testing.T) {
	project := model.ProjectSummary{
		Path:                "/tmp/demo",
		Name:                "demo",
		PresentOnDisk:       true,
		LatestSessionID:     "cx_summary",
		LatestSessionFormat: "modern",
	}
	m := Model{
		projects: []model.ProjectSummary{project},
		selected: 0,
	}

	got := m.selectedProjectCodexSessionID(project)
	if got != "cx_summary" {
		t.Fatalf("selectedProjectCodexSessionID() = %q, want %q", got, "cx_summary")
	}
}

func TestCommandTabCompletesSelectedSuggestion(t *testing.T) {
	input := textinput.New()
	input.SetValue("/sort r")

	m := Model{
		commandMode:  true,
		commandInput: input,
		width:        100,
		height:       24,
	}
	m.syncCommandSelection()

	updated, _ := m.updateCommandMode(tea.KeyMsg{Type: tea.KeyTab})
	got := updated.(Model)
	if got.commandInput.Value() != "/sort recent" {
		t.Fatalf("command input = %q, want /sort recent", got.commandInput.Value())
	}
}

func TestCommandEnterUsesAutocompleteSuggestion(t *testing.T) {
	input := textinput.New()
	input.SetValue("/focus d")

	m := Model{
		commandMode:  true,
		commandInput: input,
		focusedPane:  focusProjects,
		width:        100,
		height:       24,
	}
	m.syncCommandSelection()

	updated, _ := m.updateCommandMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if got.commandMode {
		t.Fatalf("command mode should close after executing a valid command")
	}
	if got.focusedPane != focusDetail {
		t.Fatalf("focusedPane = %s, want %s", got.focusedPane, focusDetail)
	}
}

func TestCommandEnterViewAllChangesVisibility(t *testing.T) {
	input := textinput.New()
	input.SetValue("/view all")

	m := Model{
		allProjects: []model.ProjectSummary{
			{Path: "/tmp/ai", Name: "ai", LastActivity: time.Date(2026, 3, 7, 10, 0, 0, 0, time.UTC)},
			{Path: "/tmp/plain", Name: "plain"},
		},
		projects: []model.ProjectSummary{
			{Path: "/tmp/ai", Name: "ai", LastActivity: time.Date(2026, 3, 7, 10, 0, 0, 0, time.UTC)},
		},
		visibility:   visibilityAIFolders,
		commandMode:  true,
		commandInput: input,
		width:        100,
		height:       24,
	}
	m.syncCommandSelection()

	updated, _ := m.updateCommandMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if got.visibility != visibilityAllFolders {
		t.Fatalf("visibility = %s, want %s", got.visibility, visibilityAllFolders)
	}
	if len(got.projects) != 2 {
		t.Fatalf("visible project count = %d, want 2", len(got.projects))
	}
}

func TestCommandEnterOpensSettingsMode(t *testing.T) {
	input := textinput.New()
	input.SetValue("/settings")

	m := Model{
		commandMode:  true,
		commandInput: input,
		width:        100,
		height:       24,
	}
	m.syncCommandSelection()

	updated, _ := m.updateCommandMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if !got.settingsMode {
		t.Fatalf("settings mode should open after /settings")
	}
	if got.commandMode {
		t.Fatalf("command mode should close after /settings")
	}
	if len(got.settingsFields) != 8 {
		t.Fatalf("settings field count = %d, want 8", len(got.settingsFields))
	}
}

func TestStartupUnconfiguredAIBackendOpensSetupMode(t *testing.T) {
	m := Model{
		width:  100,
		height: 24,
	}

	updated, cmd := m.Update(setupSnapshotMsg{
		openOnStartup: true,
		snapshot: aibackend.Snapshot{
			Selected: config.AIBackendUnset,
		},
	})
	got := updated.(Model)
	if !got.setupMode {
		t.Fatalf("setup mode should open when startup detects no configured backend")
	}
	if got.status != "Choose how Little Control Room should run AI summaries, classifications, and commit help." {
		t.Fatalf("status = %q, want startup setup explanation", got.status)
	}
	if cmd == nil {
		t.Fatalf("opening setup should return a refresh command")
	}
}

func TestOpenSetupModePrefersReadyBackendOverUnavailableCurrentBackend(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.AIBackend = config.AIBackendOpenAIAPI

	m := Model{
		settingsBaseline: &settings,
		setupSnapshot: aibackend.Snapshot{
			Selected: config.AIBackendOpenAIAPI,
			OpenAIAPI: aibackend.Status{
				Backend: config.AIBackendOpenAIAPI,
				Label:   "OpenAI API key",
				Detail:  "No saved OpenAI API key.",
			},
			Codex: aibackend.Status{
				Backend:       config.AIBackendCodex,
				Label:         "Codex",
				Installed:     true,
				Authenticated: true,
				Ready:         true,
				Detail:        "Logged in with ChatGPT.",
			},
		},
	}

	_ = m.openSetupMode()
	if got := m.setupSelectedBackend(); got != config.AIBackendCodex {
		t.Fatalf("setupSelectedBackend() = %s, want %s", got, config.AIBackendCodex)
	}
}

func TestRenderSetupOptionRowDistinguishesActiveAndReadyBackends(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.AIBackend = config.AIBackendOpenAIAPI

	m := Model{
		settingsBaseline: &settings,
		setupSnapshot: aibackend.Snapshot{
			Selected: config.AIBackendOpenAIAPI,
			OpenAIAPI: aibackend.Status{
				Backend: config.AIBackendOpenAIAPI,
				Label:   "OpenAI API key",
				Detail:  "No saved OpenAI API key.",
			},
			Codex: aibackend.Status{
				Backend:       config.AIBackendCodex,
				Label:         "Codex",
				Installed:     true,
				Authenticated: true,
				Ready:         true,
				Detail:        "Logged in with ChatGPT.",
			},
		},
	}

	activeRow := ansi.Strip(m.renderSetupOptionRow(config.AIBackendOpenAIAPI, false, 88))
	if !strings.Contains(activeRow, "active") {
		t.Fatalf("active backend row should say active, got %q", activeRow)
	}

	readyRow := ansi.Strip(m.renderSetupOptionRow(config.AIBackendCodex, false, 88))
	if !strings.Contains(readyRow, "ready") {
		t.Fatalf("ready backend row should say ready, got %q", readyRow)
	}
	if strings.Contains(readyRow, "active") {
		t.Fatalf("ready backend row should not look active, got %q", readyRow)
	}
}

func TestCommandEnterOpensNoteDialog(t *testing.T) {
	input := textinput.New()
	input.SetValue("/note")

	m := Model{
		projects: []model.ProjectSummary{{
			Name:          "demo",
			Path:          "/tmp/demo",
			PresentOnDisk: true,
			Note:          "Remember the release checklist",
		}},
		selected:     0,
		commandMode:  true,
		commandInput: input,
		width:        100,
		height:       24,
	}
	m.syncCommandSelection()

	updated, cmd := m.updateCommandMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if got.commandMode {
		t.Fatalf("command mode should close after /note")
	}
	if got.noteDialog == nil {
		t.Fatalf("note dialog should open after /note")
	}
	if got.noteDialog.ProjectPath != "/tmp/demo" {
		t.Fatalf("note dialog project path = %q, want /tmp/demo", got.noteDialog.ProjectPath)
	}
	if got.noteDialog.Editor.Value() != "Remember the release checklist" {
		t.Fatalf("note dialog value = %q, want saved note", got.noteDialog.Editor.Value())
	}
	if cmd == nil {
		t.Fatalf("/note should return a focus command for the editor")
	}
}

func TestCommandEnterOpensRunCommandDialogWhenUnset(t *testing.T) {
	tests := []struct {
		name    string
		command string
	}{
		{name: "run", command: "/run"},
		{name: "start alias", command: "/start"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := textinput.New()
			input.SetValue(tt.command)

			m := Model{
				projects: []model.ProjectSummary{{
					Name:          "demo",
					Path:          "/tmp/demo",
					PresentOnDisk: true,
				}},
				selected:     0,
				commandMode:  true,
				commandInput: input,
				width:        100,
				height:       24,
			}
			m.syncCommandSelection()

			updated, cmd := m.updateCommandMode(tea.KeyMsg{Type: tea.KeyEnter})
			got := updated.(Model)
			if got.commandMode {
				t.Fatalf("command mode should close after %s", tt.command)
			}
			if got.runCommandDialog == nil {
				t.Fatalf("run command dialog should open after %s when no command is saved", tt.command)
			}
			if got.runCommandDialog.ProjectPath != "/tmp/demo" {
				t.Fatalf("run command dialog project path = %q, want /tmp/demo", got.runCommandDialog.ProjectPath)
			}
			if cmd == nil {
				t.Fatalf("%s should return a focus command for the input", tt.command)
			}
		})
	}
}

func TestRestartCommandQueuesRuntimeRestart(t *testing.T) {
	m := Model{
		projects: []model.ProjectSummary{{
			Name:          "demo",
			Path:          "/tmp/demo",
			PresentOnDisk: true,
			RunCommand:    "pnpm dev",
		}},
		selected: 0,
	}

	updated, cmd := m.dispatchCommand(commands.Invocation{Kind: commands.KindRestart})
	got := updated.(Model)
	if got.status != "Restarting runtime..." {
		t.Fatalf("status = %q, want restarting status", got.status)
	}
	if cmd == nil {
		t.Fatalf("dispatchCommand(/restart) should return a restart command")
	}
}

func TestRestartCommandRequiresSavedOrActiveCommand(t *testing.T) {
	m := Model{
		projects: []model.ProjectSummary{{
			Name:          "demo",
			Path:          "/tmp/demo",
			PresentOnDisk: true,
		}},
		selected: 0,
	}

	updated, cmd := m.dispatchCommand(commands.Invocation{Kind: commands.KindRestart})
	got := updated.(Model)
	if got.status != "Runtime command is not set" {
		t.Fatalf("status = %q, want missing runtime command status", got.status)
	}
	if cmd != nil {
		t.Fatalf("dispatchCommand(/restart) should fail locally when no runtime command exists")
	}
}

func TestDispatchNoteClearOpensConfirmationDialog(t *testing.T) {
	m := Model{
		projects: []model.ProjectSummary{{
			Name:          "demo",
			Path:          "/tmp/demo",
			PresentOnDisk: true,
			Note:          "Saved note",
		}},
		selected: 0,
	}

	updated, cmd := m.dispatchCommand(commands.Invocation{Kind: commands.KindNote, Clear: true})
	got := updated.(Model)
	if got.noteClearConfirm == nil {
		t.Fatalf("/note clear should open a confirmation dialog")
	}
	if got.noteClearConfirm.ProjectPath != "/tmp/demo" {
		t.Fatalf("confirmation project path = %q, want /tmp/demo", got.noteClearConfirm.ProjectPath)
	}
	if got.noteClearConfirm.Selected != noteClearConfirmFocusCancel {
		t.Fatalf("default confirmation selection = %d, want cancel", got.noteClearConfirm.Selected)
	}
	if cmd != nil {
		t.Fatalf("/note clear should not clear immediately")
	}
}

func TestNoteDialogSaveActionClosesDialogAndReturnsCommand(t *testing.T) {
	m := Model{
		noteDialog: &noteDialogState{
			ProjectPath:  "/tmp/demo",
			ProjectName:  "demo",
			OriginalNote: "",
			Editor:       newNoteTextarea("Capture the next handoff"),
			Selected:     noteDialogFocusSave,
		},
	}

	updated, cmd := m.updateNoteDialogMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if got.noteDialog != nil {
		t.Fatalf("save should close the note dialog")
	}
	if got.status != "Saving note..." {
		t.Fatalf("status = %q, want saving note status", got.status)
	}
	if cmd == nil {
		t.Fatalf("save should return a note command")
	}
}

func TestNoteClearConfirmationConfirmClosesDialogsAndReturnsCommand(t *testing.T) {
	m := Model{
		noteDialog: &noteDialogState{
			ProjectPath:  "/tmp/demo",
			ProjectName:  "demo",
			OriginalNote: "Saved note",
			Editor:       newNoteTextarea("Saved note"),
			Selected:     noteDialogFocusClear,
		},
		noteClearConfirm: &noteClearConfirmState{
			ProjectPath: "/tmp/demo",
			ProjectName: "demo",
			Selected:    noteClearConfirmFocusConfirm,
		},
	}

	updated, cmd := m.updateNoteClearConfirmMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if got.noteClearConfirm != nil {
		t.Fatalf("confirm should close the confirmation dialog")
	}
	if got.noteDialog != nil {
		t.Fatalf("confirm should close the note dialog before clearing")
	}
	if got.status != "Clearing note..." {
		t.Fatalf("status = %q, want clearing note status", got.status)
	}
	if cmd == nil {
		t.Fatalf("confirm should return a clear-note command")
	}
}

func TestNoteClearConfirmationCancelKeepsNoteDialogOpen(t *testing.T) {
	m := Model{
		noteDialog: &noteDialogState{
			ProjectPath:  "/tmp/demo",
			ProjectName:  "demo",
			OriginalNote: "Saved note",
			Editor:       newNoteTextarea("Saved note"),
			Selected:     noteDialogFocusClear,
		},
		noteClearConfirm: &noteClearConfirmState{
			ProjectPath: "/tmp/demo",
			ProjectName: "demo",
			Selected:    noteClearConfirmFocusCancel,
		},
	}

	updated, cmd := m.updateNoteClearConfirmMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if got.noteClearConfirm != nil {
		t.Fatalf("cancel should close the confirmation dialog")
	}
	if got.noteDialog == nil {
		t.Fatalf("cancel should leave the note dialog open")
	}
	if got.status != "Note clear canceled" {
		t.Fatalf("status = %q, want note clear canceled", got.status)
	}
	if cmd != nil {
		t.Fatalf("cancel should not return a command")
	}
}

func TestDetailFocusScrollsViewportWithoutChangingSelection(t *testing.T) {
	m := Model{
		projects: []model.ProjectSummary{{
			Path:                            "/tmp/demo",
			Name:                            "demo",
			Status:                          model.StatusIdle,
			PresentOnDisk:                   true,
			LatestSessionClassification:     model.ClassificationCompleted,
			LatestSessionClassificationType: model.SessionCategoryNeedsFollowUp,
		}},
		selected:    0,
		focusedPane: focusDetail,
		width:       100,
		height:      18,
		detail: model.ProjectDetail{
			Reasons: []model.AttentionReason{
				{Text: "one"},
				{Text: "two"},
				{Text: "three"},
				{Text: "four"},
				{Text: "five"},
				{Text: "six"},
				{Text: "seven"},
				{Text: "eight"},
			},
			LatestSessionClassification: &model.SessionClassification{
				Status:   model.ClassificationCompleted,
				Category: model.SessionCategoryNeedsFollowUp,
				Summary:  "A concrete next step still remains.",
			},
		},
	}
	m.syncDetailViewport(false)

	before := m.detailViewport.YOffset
	updated, _ := m.updateNormalMode(tea.KeyMsg{Type: tea.KeyPgDown})
	got := updated.(Model)
	if got.selected != 0 {
		t.Fatalf("detail scrolling should not change selected project, got %d", got.selected)
	}
	if got.detailViewport.YOffset <= before {
		t.Fatalf("detail scrolling should advance viewport offset, before=%d after=%d", before, got.detailViewport.YOffset)
	}
}

func TestViewWithCommandModeRespectsHeight(t *testing.T) {
	input := textinput.New()
	input.SetValue("/")

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
		selected:     0,
		commandMode:  true,
		commandInput: input,
		width:        100,
		height:       24,
	}
	m.syncCommandSelection()
	m.syncDetailViewport(false)

	rendered := m.View()
	if got := len(strings.Split(rendered, "\n")); got != m.height {
		t.Fatalf("View() line count = %d, want terminal height %d; render was %q", got, m.height, rendered)
	}
	if !strings.Contains(rendered, "Command Palette") {
		t.Fatalf("View() missing command palette: %q", rendered)
	}
	if !strings.Contains(rendered, "Selected project: demo") {
		t.Fatalf("View() missing selected project context: %q", rendered)
	}
	if !strings.Contains(rendered, "ATTN") || !strings.Contains(rendered, "Path:") {
		t.Fatalf("View() should preserve background list and detail context under the command palette: %q", rendered)
	}
}

func TestCommandPaletteRendersColoredActionLegend(t *testing.T) {
	input := textinput.New()
	input.SetValue("/")

	m := Model{
		commandMode:  true,
		commandInput: input,
		width:        100,
		height:       24,
	}
	m.syncCommandSelection()

	rendered := ansi.Strip(m.renderCommandPaletteContent(72))
	if !strings.Contains(rendered, "Enter") || !strings.Contains(rendered, "run") {
		t.Fatalf("command palette should render Enter run action: %q", rendered)
	}
	if !strings.Contains(rendered, "Tab") || !strings.Contains(rendered, "complete") {
		t.Fatalf("command palette should render Tab complete action: %q", rendered)
	}
	if !strings.Contains(rendered, "Up/Down") || !strings.Contains(rendered, "choose") {
		t.Fatalf("command palette should render Up/Down choose action: %q", rendered)
	}
	if !strings.Contains(rendered, "Esc") || !strings.Contains(rendered, "cancel") {
		t.Fatalf("command palette should render Esc cancel action: %q", rendered)
	}
}

func TestViewWithSettingsModeRespectsHeight(t *testing.T) {
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
		selected:       0,
		settingsMode:   true,
		settingsFields: newSettingsFields(config.EditableSettingsFromAppConfig(config.Default())),
		width:          100,
		height:         24,
	}
	m.syncDetailViewport(false)
	_ = m.setSettingsSelection(0)

	rendered := ansi.Strip(m.View())
	if got := len(strings.Split(rendered, "\n")); got != m.height {
		t.Fatalf("View() line count = %d, want terminal height %d; render was %q", got, m.height, rendered)
	}
	if !strings.Contains(rendered, "Settings") {
		t.Fatalf("View() missing settings modal: %q", rendered)
	}
	if !strings.Contains(rendered, "Config:") {
		t.Fatalf("View() missing config path context: %q", rendered)
	}
	if !strings.Contains(rendered, "│  A") || !strings.Contains(rendered, "│ Pa") {
		t.Fatalf("View() should preserve background list and detail context under the settings modal: %q", rendered)
	}
}

func TestSettingsModalRendersColoredActionLegend(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.OpenAIAPIKey = "sk-test-example"

	m := Model{
		settingsMode:     true,
		settingsFields:   newSettingsFields(settings),
		settingsBaseline: &settings,
		width:            100,
		height:           24,
	}
	_ = m.setSettingsSelection(0)

	rendered := ansi.Strip(m.renderSettingsContent(72, 18))
	if !strings.Contains(rendered, "Enter") || !strings.Contains(rendered, "save") {
		t.Fatalf("settings modal should render Enter save action: %q", rendered)
	}
	if !strings.Contains(rendered, "Tab") || !strings.Contains(rendered, "next") {
		t.Fatalf("settings modal should render Tab next action: %q", rendered)
	}
	if !strings.Contains(rendered, "Up/Down") || !strings.Contains(rendered, "choose") {
		t.Fatalf("settings modal should render Up/Down choose action: %q", rendered)
	}
	if !strings.Contains(rendered, "Esc") || !strings.Contains(rendered, "cancel") {
		t.Fatalf("settings modal should render Esc cancel action: %q", rendered)
	}
}

func TestSettingsModalShowsEscCancel(t *testing.T) {
	m := Model{
		settingsMode:   true,
		settingsFields: newSettingsFields(config.EditableSettingsFromAppConfig(config.Default())),
		width:          100,
		height:         24,
	}
	_ = m.setSettingsSelection(0)

	rendered := ansi.Strip(m.renderSettingsContent(72, 18))
	if !strings.Contains(rendered, "Esc") || !strings.Contains(rendered, "cancel") {
		t.Fatalf("settings modal should render Esc cancel action: %q", rendered)
	}
}

func TestSettingsModalShowsSelectedHintAndWindowsLowerFields(t *testing.T) {
	m := Model{
		settingsMode:   true,
		settingsFields: newSettingsFields(config.EditableSettingsFromAppConfig(config.Default())),
		width:          100,
		height:         24,
	}
	_ = m.setSettingsSelection(settingsFieldInterval)

	rendered := ansi.Strip(m.renderSettingsContent(72, 12))
	if !strings.Contains(rendered, "Scan interval") {
		t.Fatalf("settings modal should keep the selected lower field visible: %q", rendered)
	}
	if strings.Contains(rendered, "Include paths") {
		t.Fatalf("settings modal should window away upper fields in a short modal: %q", rendered)
	}
	if !strings.Contains(rendered, "Background refresh interval. Example: 60s") {
		t.Fatalf("settings modal should show the selected field hint: %q", rendered)
	}
	if strings.Contains(rendered, "Optional comma-separated path prefixes to keep in scope") {
		t.Fatalf("settings modal should not render every field hint inline anymore: %q", rendered)
	}
	if !strings.Contains(rendered, "↑ ") {
		t.Fatalf("settings modal should show an above-window indicator when earlier fields are hidden: %q", rendered)
	}
}

func TestSettingsEnterSavesConfigAndClosesModal(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	m := Model{
		settingsMode:   true,
		settingsFields: newSettingsFields(config.EditableSettingsFromAppConfig(config.Default())),
		width:          100,
		height:         24,
	}
	_ = m.setSettingsSelection(0)

	m.settingsFields[settingsFieldOpenAIAPIKey].input.SetValue("sk-test-example")
	m.settingsFields[settingsFieldIncludePaths].input.SetValue("/tmp/a,/tmp/b")
	m.settingsFields[settingsFieldExcludePaths].input.SetValue("/tmp/skip")
	m.settingsFields[settingsFieldExcludeProjectPatterns].input.SetValue("quickgame_*,secret-demo")
	m.settingsFields[settingsFieldCodexLaunchPreset].input.SetValue("full-auto")
	m.settingsFields[settingsFieldActiveThreshold].input.SetValue("15m")
	m.settingsFields[settingsFieldStuckThreshold].input.SetValue("3h")
	m.settingsFields[settingsFieldInterval].input.SetValue("45s")

	updated, cmd := m.updateSettingsMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("expected save command")
	}
	if got.status != "Saving settings..." {
		t.Fatalf("status = %q, want saving message", got.status)
	}

	msg := cmd()
	finalModel, _ := got.Update(msg)
	saved := finalModel.(Model)
	if saved.settingsMode {
		t.Fatalf("settings mode should close after a successful save")
	}
	if !strings.Contains(saved.status, "Filters, API key, and Codex launch mode apply now") {
		t.Fatalf("status = %q, want immediate-apply notice", saved.status)
	}

	configPath := filepath.Join(home, ".little-control-room", "config.toml")
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read saved config: %v", err)
	}
	text := string(raw)
	if !strings.Contains(text, "openai_api_key = \"sk-test-example\"") || !strings.Contains(text, "include_paths = [") || !strings.Contains(text, "exclude_paths = [") || !strings.Contains(text, "exclude_project_patterns = [") || !strings.Contains(text, "codex_launch_preset = \"full-auto\"") || !strings.Contains(text, "interval = \"45s\"") {
		t.Fatalf("saved config missing edited values: %q", text)
	}
}

func TestSettingsEnterShowsValidationError(t *testing.T) {
	m := Model{
		settingsMode:   true,
		settingsFields: newSettingsFields(config.EditableSettingsFromAppConfig(config.Default())),
		width:          100,
		height:         24,
	}
	_ = m.setSettingsSelection(0)
	m.settingsFields[settingsFieldOpenAIAPIKey].input.SetValue("sk-test-example")
	m.settingsFields[settingsFieldActiveThreshold].input.SetValue("20m")
	m.settingsFields[settingsFieldStuckThreshold].input.SetValue("10m")

	updated, cmd := m.updateSettingsMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("expected no save command when validation fails")
	}
	if got.status != "stuck-threshold must be greater than active-threshold" {
		t.Fatalf("status = %q, want validation message", got.status)
	}
	if !got.settingsMode {
		t.Fatalf("settings mode should stay open after validation failure")
	}
}

func TestSettingsEscCancelsWithoutQuitting(t *testing.T) {
	m := Model{
		settingsMode:   true,
		settingsFields: newSettingsFields(config.EditableSettingsFromAppConfig(config.Default())),
		width:          100,
		height:         24,
	}
	_ = m.setSettingsSelection(0)

	updated, cmd := m.updateSettingsMode(tea.KeyMsg{Type: tea.KeyEsc})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("esc should not return a command")
	}
	if got.settingsMode {
		t.Fatalf("settings mode should close after escape")
	}
	if got.status != "Settings edit canceled" {
		t.Fatalf("status = %q, want canceled message", got.status)
	}
}

func TestSettingsAPIKeyHintShowsMaskedSuffix(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.OpenAIAPIKey = "sk-live-12345"

	m := Model{
		settingsMode:   true,
		settingsFields: newSettingsFields(settings),
		width:          100,
		height:         24,
	}
	_ = m.setSettingsSelection(settingsFieldOpenAIAPIKey)

	rendered := ansi.Strip(m.renderSettingsContent(72, 18))
	if !strings.Contains(rendered, "Stored key ends with ...12345.") {
		t.Fatalf("settings modal should show a masked api key suffix hint: %q", rendered)
	}
	if strings.Contains(rendered, "sk-live-12345") {
		t.Fatalf("settings modal should not show the full api key: %q", rendered)
	}
}

func TestSettingsSavedMsgAppliesProjectNameFilterImmediately(t *testing.T) {
	m := Model{
		settingsMode: true,
		allProjects: []model.ProjectSummary{
			{Path: "/tmp/client-demo-03", Name: "client-demo-03", LastActivity: time.Date(2026, 3, 6, 9, 0, 0, 0, time.UTC)},
			{Path: "/tmp/visible-demo", Name: "visible-demo", LastActivity: time.Date(2026, 3, 6, 10, 0, 0, 0, time.UTC)},
		},
		sortMode:   sortByAttention,
		visibility: visibilityAllFolders,
	}
	m.rebuildProjectList("")

	updated, cmd := m.Update(settingsSavedMsg{
		path: "/tmp/config.toml",
		settings: config.EditableSettings{
			ExcludeProjectPatterns: []string{"client-*"},
			CodexLaunchPreset:      codexcli.PresetSafe,
		},
	})
	got := updated.(Model)
	if got.settingsMode {
		t.Fatalf("settings mode should close after settingsSavedMsg")
	}
	if cmd == nil {
		t.Fatalf("settingsSavedMsg should refresh detail for the remaining visible project")
	}
	if len(got.projects) != 1 || got.projects[0].Name != "visible-demo" {
		t.Fatalf("visible projects after settingsSavedMsg = %#v, want only visible-demo", got.projects)
	}
	if !strings.Contains(got.status, "Filters, API key, and Codex launch mode apply now") {
		t.Fatalf("status = %q, want immediate-apply notice", got.status)
	}
}

func TestDispatchIgnoreCommandStoresIgnoredNameAndHidesProject(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	now := time.Date(2026, 3, 17, 9, 0, 0, 0, time.UTC)
	for _, state := range []model.ProjectState{
		{
			Path:           "/tmp/projects_control_center",
			Name:           "projects_control_center",
			AttentionScore: 20,
			InScope:        true,
			UpdatedAt:      now,
		},
		{
			Path:           "/tmp/worktrees/a1/projects_control_center",
			Name:           "projects_control_center",
			AttentionScore: 15,
			InScope:        true,
			UpdatedAt:      now,
		},
		{
			Path:           "/tmp/visible-demo",
			Name:           "visible-demo",
			AttentionScore: 10,
			InScope:        true,
			UpdatedAt:      now,
		},
	} {
		if err := st.UpsertProjectState(ctx, state); err != nil {
			t.Fatalf("upsert project %s: %v", state.Path, err)
		}
	}

	svc := service.New(config.Default(), st, events.NewBus(), nil)
	projects, err := st.ListProjects(ctx, false)
	if err != nil {
		t.Fatalf("list projects: %v", err)
	}

	m := Model{
		ctx:         ctx,
		svc:         svc,
		allProjects: projects,
		sortMode:    sortByAttention,
		visibility:  visibilityAllFolders,
	}
	m.rebuildProjectList("")
	if len(m.projects) != 3 || m.projects[0].Name != "projects_control_center" {
		t.Fatalf("initial projects = %#v, want ignored candidate first", m.projects)
	}

	updated, cmd := m.dispatchCommand(commands.Invocation{Kind: commands.KindIgnore})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("dispatchCommand(/ignore) should return an ignore command")
	}

	actionMsg := cmd()
	afterAction, reloadCmd := got.Update(actionMsg)
	reloaded := afterAction.(Model)
	if reloadCmd == nil {
		t.Fatalf("ignore action should trigger a project reload")
	}
	projectsMsg := reloadCmd()
	finalModel, _ := reloaded.Update(projectsMsg)
	saved := finalModel.(Model)
	if len(saved.projects) != 1 || saved.projects[0].Name != "visible-demo" {
		t.Fatalf("visible projects after /ignore = %#v, want only visible-demo", saved.projects)
	}
	if saved.status != `Ignored "projects_control_center"` {
		t.Fatalf("status = %q, want ignore confirmation", saved.status)
	}

	ignored, err := st.ListIgnoredProjectNames(ctx)
	if err != nil {
		t.Fatalf("list ignored names: %v", err)
	}
	if len(ignored) != 1 || ignored[0].Name != "projects_control_center" {
		t.Fatalf("ignored names = %#v, want projects_control_center", ignored)
	}
}

func TestIgnoredPickerListsAndRestoresIgnoredNames(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	if err := st.SetIgnoredProjectName(ctx, "projects_control_center", true); err != nil {
		t.Fatalf("seed ignored project name: %v", err)
	}

	svc := service.New(config.Default(), st, events.NewBus(), nil)
	m := Model{ctx: ctx, svc: svc}

	updated, cmd := m.dispatchCommand(commands.Invocation{Kind: commands.KindIgnored})
	got := updated.(Model)
	if !got.ignoredPickerVisible || !got.ignoredPickerLoading {
		t.Fatalf("/ignored should open the ignored picker in loading state")
	}
	if cmd == nil {
		t.Fatalf("/ignored should load ignored project names")
	}

	loadedModel, _ := got.Update(cmd())
	loaded := loadedModel.(Model)
	if !loaded.ignoredPickerVisible || loaded.ignoredPickerLoading {
		t.Fatalf("ignored picker should be visible after loading")
	}
	if len(loaded.ignoredPickerItems) != 1 || loaded.ignoredPickerItems[0].Name != "projects_control_center" {
		t.Fatalf("ignored picker items = %#v, want projects_control_center", loaded.ignoredPickerItems)
	}

	nextModel, unignoreCmd := loaded.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next := nextModel.(Model)
	if unignoreCmd == nil {
		t.Fatalf("enter in ignored picker should trigger restore")
	}
	if !next.ignoredPickerLoading {
		t.Fatalf("ignored picker should return to loading while restoring")
	}

	restoredModel, _ := next.Update(unignoreCmd())
	restored := restoredModel.(Model)
	if restored.status != `Restored "projects_control_center"` {
		t.Fatalf("status = %q, want restore confirmation", restored.status)
	}

	ignored, err := st.ListIgnoredProjectNames(ctx)
	if err != nil {
		t.Fatalf("list ignored names after restore: %v", err)
	}
	if len(ignored) != 0 {
		t.Fatalf("ignored names after restore = %#v, want none", ignored)
	}
}

func TestViewWithCommitPreviewRespectsHeight(t *testing.T) {
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
		selected: 0,
		detail: model.ProjectDetail{
			LatestSessionClassification: &model.SessionClassification{
				Status:   model.ClassificationCompleted,
				Category: model.SessionCategoryCompleted,
				Summary:  "Work appears complete for now.",
			},
		},
		commitPreview: &service.CommitPreview{
			Intent:      service.GitActionFinish,
			ProjectName: "demo",
			ProjectPath: "/tmp/demo",
			Branch:      "master",
			StageMode:   service.GitStageAllChanges,
			Message:     "Ship current repo changes",
			Included: []service.CommitFile{
				{Code: "M", Summary: "README.md"},
				{Code: "??", Summary: "notes.txt"},
			},
			DiffSummary: "2 files changed, 3 insertions(+), 1 deletion(-)",
			CanPush:     true,
		},
		width:  100,
		height: 24,
	}
	m.syncDetailViewport(false)

	rendered := ansi.Strip(m.View())
	if got := len(strings.Split(rendered, "\n")); got != m.height {
		t.Fatalf("View() line count = %d, want terminal height %d; render was %q", got, m.height, rendered)
	}
	if !strings.Contains(rendered, "Commit Preview - demo (master)") {
		t.Fatalf("View() missing commit preview: %q", rendered)
	}
	if !strings.Contains(rendered, "Changes") || !strings.Contains(rendered, "Ship current repo changes") {
		t.Fatalf("View() missing commit preview details: %q", rendered)
	}
	if strings.Contains(rendered, "Source:") || strings.Contains(rendered, "Diff stat") || strings.Contains(rendered, "Included changes") {
		t.Fatalf("View() should use the simplified commit preview labels: %q", rendered)
	}
	if strings.Contains(rendered, "Push:") {
		t.Fatalf("View() should not render a push row anymore: %q", rendered)
	}
	if !strings.Contains(rendered, "2 files changed, 3 insertions(+), 1 deletion(-)") {
		t.Fatalf("View() missing merged change summary: %q", rendered)
	}
	if strings.Contains(rendered, "Selected project: demo") {
		t.Fatalf("View() should fold selected project into the commit preview title: %q", rendered)
	}
	if strings.Contains(rendered, "Branch: master") {
		t.Fatalf("View() should fold branch metadata into the commit preview title: %q", rendered)
	}
	if !strings.Contains(rendered, "Enter") || !strings.Contains(rendered, "commit") || !strings.Contains(rendered, "Alt+Enter") || !strings.Contains(rendered, "commit & push") || !strings.Contains(rendered, "Esc") || !strings.Contains(rendered, "cancel") {
		t.Fatalf("View() missing commit dialog actions: %q", rendered)
	}
	lines := strings.Split(rendered, "\n")
	messageLabelLine := -1
	messageLine := -1
	stageLine := -1
	for i, line := range lines {
		switch {
		case strings.Contains(line, "Message"):
			messageLabelLine = i
		case strings.Contains(line, "Ship current repo changes"):
			messageLine = i
		case strings.Contains(line, "Stage: stage all current changes"):
			stageLine = i
		}
	}
	if messageLabelLine < 0 || messageLine < 0 || stageLine < 0 {
		t.Fatalf("View() missing separated commit message block: %q", rendered)
	}
	if !(messageLabelLine < messageLine && messageLine < stageLine) {
		t.Fatalf("View() should show the commit message block before stage metadata: %q", rendered)
	}
	blankVisible := strings.TrimSpace(strings.Map(func(r rune) rune {
		switch r {
		case '│', '╭', '╮', '╰', '╯', '─':
			return -1
		default:
			return r
		}
	}, lines[messageLine+1]))
	if stageLine-messageLine < 2 || blankVisible != "" {
		t.Fatalf("View() should leave a blank line after the commit message: %q", rendered)
	}
	if !strings.Contains(rendered, "Status: idle") {
		t.Fatalf("View() should preserve the detail-pane context under the commit preview: %q", rendered)
	}
	if !strings.Contains(rendered, "Output") && !strings.Contains(rendered, "Standby. Use /run or /run-edit.") {
		t.Fatalf("View() should preserve background list and detail context under the commit preview: %q", rendered)
	}
}

func TestViewWithCommitPreviewShowsRecommendedUntrackedFiles(t *testing.T) {
	m := Model{
		commitPreview: &service.CommitPreview{
			Intent:      service.GitActionCommit,
			ProjectName: "demo",
			ProjectPath: "/tmp/demo",
			Branch:      "master",
			StageMode:   service.GitStageStagedOnly,
			Message:     "Update repo",
			Included: []service.CommitFile{
				{Code: "M", Summary: "README.md"},
				{Code: "??", Summary: "notes.txt"},
			},
			SelectedUntracked: []service.CommitFile{
				{Code: "??", Summary: "notes.txt"},
			},
			Excluded: []service.CommitFile{
				{Code: "??", Summary: "scratch.txt"},
			},
			DiffSummary: "2 files changed, 3 insertions(+), 1 deletion(-)",
			CanPush:     false,
			Warnings: []string{
				"Will also stage 1 AI-recommended untracked file before commit.",
			},
		},
		width:  100,
		height: 24,
	}

	rendered := ansi.Strip(m.renderCommitPreviewContent(72))
	if !strings.Contains(rendered, "Stage: commit staged changes plus 1 recommended untracked file") {
		t.Fatalf("renderCommitPreviewContent() should mention recommended untracked staging: %q", rendered)
	}
	if !strings.Contains(rendered, "notes.txt") || !strings.Contains(rendered, "scratch.txt") {
		t.Fatalf("renderCommitPreviewContent() should show included and left-out files: %q", rendered)
	}
	if !strings.Contains(rendered, "Will also stage 1 AI-recommended untracked file before commit.") {
		t.Fatalf("renderCommitPreviewContent() should show the AI staging warning: %q", rendered)
	}
	if !strings.Contains(rendered, "Alt+Enter") || !strings.Contains(rendered, "push unavailable") {
		t.Fatalf("renderCommitPreviewContent() should show disabled push action when push is unavailable: %q", rendered)
	}
}

func TestRenderCommitPreviewContentShowsLoadingPlaceholder(t *testing.T) {
	m := Model{
		commitPreview: &service.CommitPreview{
			Intent:        service.GitActionCommit,
			ProjectName:   "demo",
			ProjectPath:   "/tmp/demo",
			StageMode:     service.GitStageStagedOnly,
			Message:       "Generating commit message...",
			LatestSummary: "Recent AI/backend setup work",
		},
		commitPreviewRefreshing: true,
		width:                   100,
		height:                  24,
	}

	rendered := ansi.Strip(m.renderCommitPreviewContent(72))
	if !strings.Contains(rendered, "Generating commit message...") {
		t.Fatalf("renderCommitPreviewContent() should show the loading message placeholder: %q", rendered)
	}
	if !strings.Contains(rendered, "Inspecting repo changes...") {
		t.Fatalf("renderCommitPreviewContent() should show the loading changes placeholder: %q", rendered)
	}
	if !strings.Contains(rendered, "Building commit preview...") {
		t.Fatalf("renderCommitPreviewContent() should show the loading footer hint: %q", rendered)
	}
	if strings.Contains(rendered, "Enter commit") {
		t.Fatalf("renderCommitPreviewContent() should hide commit actions while loading: %q", rendered)
	}
}

func TestCommitPreviewEnterCommitsWithoutPush(t *testing.T) {
	m := Model{
		commitPreview: &service.CommitPreview{
			ProjectPath: "/tmp/demo",
			Message:     "Update repo",
			CanPush:     true,
		},
	}

	updated, cmd := m.updateCommitPreviewMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if !got.commitApplying {
		t.Fatalf("enter should start applying the commit")
	}
	if got.status != "Committing..." {
		t.Fatalf("status = %q, want %q", got.status, "Committing...")
	}
	if cmd == nil {
		t.Fatalf("enter should return an apply command")
	}
}

func TestCommitPreviewRefreshingBlocksCommitActions(t *testing.T) {
	m := Model{
		commitPreview: &service.CommitPreview{
			ProjectPath: "/tmp/demo",
			Message:     "Generating commit message...",
		},
		commitPreviewRefreshing: true,
	}

	updated, cmd := m.updateCommitPreviewMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if got.commitApplying {
		t.Fatalf("enter should stay blocked while the preview is still refreshing")
	}
	if cmd != nil {
		t.Fatalf("enter should not return an apply command while refreshing")
	}
}

func TestCommitPreviewAltEnterCommitsAndPushes(t *testing.T) {
	m := Model{
		commitPreview: &service.CommitPreview{
			ProjectPath: "/tmp/demo",
			Message:     "Update repo",
			CanPush:     true,
		},
	}

	updated, cmd := m.updateCommitPreviewMode(tea.KeyMsg{Type: tea.KeyEnter, Alt: true})
	got := updated.(Model)
	if !got.commitApplying {
		t.Fatalf("alt+enter should start applying the commit")
	}
	if got.status != "Committing and pushing..." {
		t.Fatalf("status = %q, want %q", got.status, "Committing and pushing...")
	}
	if cmd == nil {
		t.Fatalf("alt+enter should return an apply command")
	}
}

func TestCommitPreviewAltEnterStaysBlockedWithoutPush(t *testing.T) {
	m := Model{
		commitPreview: &service.CommitPreview{
			ProjectPath: "/tmp/demo",
			Message:     "Update repo",
			CanPush:     false,
		},
	}

	updated, cmd := m.updateCommitPreviewMode(tea.KeyMsg{Type: tea.KeyEnter, Alt: true})
	got := updated.(Model)
	if got.commitApplying {
		t.Fatalf("alt+enter should not start applying when push is unavailable")
	}
	if got.status != "Commit & push is unavailable for this repo" {
		t.Fatalf("status = %q, want %q", got.status, "Commit & push is unavailable for this repo")
	}
	if cmd != nil {
		t.Fatalf("alt+enter should not return an apply command when push is unavailable")
	}
}

func TestCommitPreviewDOpensDiffView(t *testing.T) {
	m := Model{
		commitPreview: &service.CommitPreview{
			ProjectName: "demo",
			ProjectPath: "/tmp/demo",
			Message:     "Update repo",
		},
		width:  100,
		height: 24,
	}

	updated, cmd := m.updateCommitPreviewMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	got := updated.(Model)
	if got.commitPreview != nil {
		t.Fatalf("pressing d should close the commit preview and open diff mode")
	}
	if got.diffView == nil {
		t.Fatalf("pressing d should open diff mode")
	}
	if got.status != "Preparing diff view..." {
		t.Fatalf("status = %q, want diff preparing status", got.status)
	}
	if cmd == nil {
		t.Fatalf("pressing d should return a diff load command")
	}
}

func TestCommitPreviewNoChangesOpensGitStatusDialog(t *testing.T) {
	m := Model{}

	updated, cmd := m.Update(commitPreviewMsg{
		err: service.NoChangesToCommitError{
			ProjectPath: "/tmp/quickgame_30",
			ProjectName: "quickgame_30",
			Branch:      "master",
			Ahead:       4,
			CanPush:     true,
		},
	})
	got := updated.(Model)

	if cmd != nil {
		t.Fatalf("no-changes dialog should not immediately return another command")
	}
	if got.gitStatusDialog == nil {
		t.Fatalf("expected no-changes commit result to open the git status dialog")
	}
	if got.err != nil {
		t.Fatalf("no-changes dialog should clear the generic error, got %v", got.err)
	}
	if got.status != "Nothing new to commit. Enter push 4 existing commits, Esc cancel" {
		t.Fatalf("status = %q", got.status)
	}

	rendered := ansi.Strip(got.renderGitStatusDialogContent(72))
	if !strings.Contains(rendered, "Nothing To Commit - quickgame_30 (master)") {
		t.Fatalf("rendered dialog should identify the project and empty-commit state: %q", rendered)
	}
	if !strings.Contains(rendered, "ahead of upstream by 4 commit(s)") {
		t.Fatalf("rendered dialog should show ahead status: %q", rendered)
	}
	if strings.Contains(rendered, "Branch: master") {
		t.Fatalf("rendered dialog should fold branch metadata into the title: %q", rendered)
	}
	if !strings.Contains(rendered, "push 4 existing commits") {
		t.Fatalf("rendered dialog should offer pushing existing commits: %q", rendered)
	}
}

func TestCommitPreviewSubmoduleAttentionOpensGitStatusDialog(t *testing.T) {
	m := Model{}

	updated, cmd := m.Update(commitPreviewMsg{
		err: service.SubmoduleAttentionError{
			ProjectPath: "/tmp/fractalmech",
			ProjectName: "FractalMech",
			Branch:      "master",
			Submodules:  []string{"assets_src"},
		},
		intent:  service.GitActionCommit,
		message: "Update parent after assets refresh",
	})
	got := updated.(Model)

	if cmd != nil {
		t.Fatalf("submodule-attention dialog should not immediately return another command")
	}
	if got.gitStatusDialog == nil {
		t.Fatalf("expected submodule-attention result to open the git status dialog")
	}
	if got.err != nil {
		t.Fatalf("submodule-attention dialog should clear the generic error, got %v", got.err)
	}
	if got.status != "Submodule needs attention. Enter resolve & continue, Esc close" {
		t.Fatalf("status = %q", got.status)
	}

	rendered := ansi.Strip(got.renderGitStatusDialogContent(72))
	if !strings.Contains(rendered, "Submodule Attention - FractalMech (master)") {
		t.Fatalf("rendered dialog should identify the project and submodule state: %q", rendered)
	}
	if strings.Contains(rendered, "Branch: master") {
		t.Fatalf("rendered dialog should fold branch metadata into the title: %q", rendered)
	}
	if !strings.Contains(rendered, "assets_src") || !strings.Contains(rendered, "resolve & continue") {
		t.Fatalf("rendered dialog should explain the submodule follow-up: %q", rendered)
	}
}

func TestGitStatusDialogEnterPushesExistingCommits(t *testing.T) {
	m := Model{
		gitStatusDialog: &gitStatusDialog{
			ProjectPath: "/tmp/demo",
			CanPush:     true,
			Ahead:       4,
		},
	}

	updated, cmd := m.updateGitStatusDialogMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if !got.gitStatusApplying {
		t.Fatalf("enter should start pushing when the dialog offers a push")
	}
	if got.status != "Pushing existing commits..." {
		t.Fatalf("status = %q, want %q", got.status, "Pushing existing commits...")
	}
	if cmd == nil {
		t.Fatalf("enter should return a push command when the dialog offers a push")
	}
}

func TestGitStatusDialogEnterClosesWhenPushUnavailable(t *testing.T) {
	m := Model{
		gitStatusDialog: &gitStatusDialog{
			ProjectPath: "/tmp/demo",
			CanPush:     false,
		},
	}

	updated, cmd := m.updateGitStatusDialogMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if got.gitStatusDialog != nil {
		t.Fatalf("enter should close the dialog when no push action is available")
	}
	if got.status != "No changes to commit" {
		t.Fatalf("status = %q, want %q", got.status, "No changes to commit")
	}
	if cmd != nil {
		t.Fatalf("enter should not return a command when the dialog only closes")
	}
}

func TestGitStatusDialogEnterClosesWithCustomDismissStatus(t *testing.T) {
	m := Model{
		gitStatusDialog: &gitStatusDialog{
			ProjectPath:   "/tmp/demo",
			CanPush:       false,
			DismissStatus: "Submodule changes still need attention",
		},
	}

	updated, cmd := m.updateGitStatusDialogMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if got.gitStatusDialog != nil {
		t.Fatalf("enter should close the dialog when no push action is available")
	}
	if got.status != "Submodule changes still need attention" {
		t.Fatalf("status = %q, want custom dismiss status", got.status)
	}
	if cmd != nil {
		t.Fatalf("enter should not return a command when the dialog only closes")
	}
}

func TestGitStatusDialogEnterResolvesSubmodulesWhenAvailable(t *testing.T) {
	m := Model{
		gitStatusDialog: &gitStatusDialog{
			ProjectPath:       "/tmp/demo",
			ResolveSubmodules: true,
			CommitIntent:      service.GitActionCommit,
			CommitMessage:     "Update parent",
			DismissStatus:     "Submodule changes still need attention",
			ReadyStatus:       "Submodule needs attention. Enter resolve & continue, Esc close",
		},
	}

	updated, cmd := m.updateGitStatusDialogMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if !got.gitStatusApplying {
		t.Fatalf("enter should start resolving submodules when the dialog offers it")
	}
	if got.status != "Resolving submodule commits..." {
		t.Fatalf("status = %q, want resolving status", got.status)
	}
	if cmd == nil {
		t.Fatalf("enter should return a resolve command when the dialog offers it")
	}
}

func TestCommandPaletteScrollsSelectedSuggestionIntoView(t *testing.T) {
	input := textinput.New()
	input.SetValue("/")

	selectedIndex := 0
	suggestions := commands.Suggestions("/")
	for i, suggestion := range suggestions {
		if suggestion.Display == "/sessions on|off|toggle" {
			selectedIndex = i
			break
		}
	}

	m := Model{
		commandMode:     true,
		commandInput:    input,
		commandSelected: selectedIndex,
		width:           100,
		height:          24,
	}
	m.syncCommandSelection()

	rendered := m.renderCommandPaletteContent(70)
	if !strings.Contains(rendered, "/sessions on|off|toggle") {
		t.Fatalf("rendered palette should include the selected suggestion once it scrolls into view: %q", rendered)
	}
	if !strings.Contains(rendered, "↑ ") {
		t.Fatalf("rendered palette should show that earlier suggestions exist: %q", rendered)
	}
	if !strings.Contains(rendered, "↓ ") {
		t.Fatalf("rendered palette should show that later suggestions exist: %q", rendered)
	}
	if strings.Contains(rendered, "/help  Open the help panel") {
		t.Fatalf("rendered palette should scroll past the initial suggestions when selection moves later: %q", rendered)
	}
}

func TestHelpPanelLinesStayMinimal(t *testing.T) {
	lines := helpPanelLines()
	joined := ansi.Strip(strings.Join(lines, "\n"))

	if len(lines) > 20 {
		t.Fatalf("helpPanelLines() should stay compact, got %d lines: %q", len(lines), joined)
	}
	if !strings.Contains(joined, "slash-command palette") {
		t.Fatalf("helpPanelLines() should explain the slash-command palette: %q", joined)
	}
	if !strings.Contains(joined, "/codex, /opencode, /settings, /commit, /diff, or /run") {
		t.Fatalf("helpPanelLines() should include concrete slash-command examples: %q", joined)
	}
	if !strings.Contains(joined, "interrupt busy session") {
		t.Fatalf("helpPanelLines() should keep the session interrupt hint: %q", joined)
	}
	if !strings.Contains(joined, "n  note") || !strings.Contains(joined, "o/v  sort/view") || !strings.Contains(joined, "p  pin") || !strings.Contains(joined, "Ctrl+V  image") {
		t.Fatalf("helpPanelLines() should show the reordered quick actions: %q", joined)
	}
	if !strings.Contains(joined, "AGENT") || !strings.Contains(joined, "RUN") {
		t.Fatalf("helpPanelLines() missing colored legend content: %q", joined)
	}
	if strings.Contains(joined, "/settings /new-project /open /run-edit") {
		t.Fatalf("helpPanelLines() should not grow back into a slash-command inventory: %q", joined)
	}
	if strings.Contains(joined, "Runtime pane shows command, ports, URL, logs") {
		t.Fatalf("helpPanelLines() should omit verbose runtime detail: %q", joined)
	}
}

func TestRenderHelpPanelOmitsVerboseLegacyHints(t *testing.T) {
	m := Model{}

	rendered := ansi.Strip(m.renderHelpPanel(80))
	if strings.Contains(rendered, "f   forget missing") {
		t.Fatalf("renderHelpPanel() should not advertise forget while it is unavailable: %q", rendered)
	}
	if strings.Contains(rendered, "Ctrl+Y note copy when notes open") {
		t.Fatalf("renderHelpPanel() should not advertise the old verbose note-copy hint: %q", rendered)
	}
	if strings.Contains(rendered, "Runtime pane shows command, ports, URL, logs") {
		t.Fatalf("renderHelpPanel() should not include verbose runtime prose: %q", rendered)
	}
	if !strings.Contains(rendered, "slash-command palette") {
		t.Fatalf("renderHelpPanel() should explain the slash-command palette: %q", rendered)
	}
	if !strings.Contains(rendered, "Ctrl+V") || !strings.Contains(rendered, "image") {
		t.Fatalf("renderHelpPanel() should keep the paste hint: %q", rendered)
	}
}

func TestViewWithHelpOverlayPreservesBackground(t *testing.T) {
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
		selected: 0,
		showHelp: true,
		width:    100,
		height:   24,
	}
	m.syncDetailViewport(false)

	rendered := ansi.Strip(m.View())
	if got := len(strings.Split(rendered, "\n")); got != m.height {
		t.Fatalf("View() line count = %d, want terminal height %d; render was %q", got, m.height, rendered)
	}
	if !strings.Contains(rendered, "Help") || !strings.Contains(rendered, "slash-command palette") {
		t.Fatalf("View() should show the help overlay content: %q", rendered)
	}
	if !strings.Contains(rendered, "ATTN") || !strings.Contains(rendered, "Path:") || !strings.Contains(rendered, "Focus: list") {
		t.Fatalf("View() should preserve the dashboard behind the help overlay: %q", rendered)
	}
}

func TestSectionToggleDevKeysAreNoLongerBound(t *testing.T) {
	m := Model{
		showSessions: true,
		showEvents:   true,
	}

	updated, cmd := m.updateNormalMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("x should no longer trigger a command")
	}
	if !got.showSessions || !got.showEvents {
		t.Fatalf("x should no longer toggle section visibility")
	}

	updated, cmd = got.updateNormalMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	got = updated.(Model)
	if cmd != nil {
		t.Fatalf("e should no longer trigger a command")
	}
	if !got.showSessions || !got.showEvents {
		t.Fatalf("e should no longer toggle section visibility")
	}
}

func TestDispatchDiffCommandOpensDiffView(t *testing.T) {
	m := Model{
		projects: []model.ProjectSummary{{
			Name:          "demo",
			Path:          "/tmp/demo",
			PresentOnDisk: true,
		}},
		selected: 0,
		width:    100,
		height:   24,
	}

	updated, cmd := m.dispatchCommand(commands.Invocation{Kind: commands.KindDiff})
	got := updated.(Model)
	if got.diffView == nil {
		t.Fatalf("dispatchCommand(/diff) should open the diff view")
	}
	if got.status != "Preparing diff view..." {
		t.Fatalf("status = %q, want preparing diff status", got.status)
	}
	if cmd == nil {
		t.Fatalf("dispatchCommand(/diff) should return a load command")
	}
}

func TestDispatchCommitCommandOpensLoadingPreviewImmediately(t *testing.T) {
	m := Model{
		projects: []model.ProjectSummary{{
			Name:                 "demo",
			Path:                 "/tmp/demo",
			PresentOnDisk:        true,
			LatestSessionSummary: "Recent backend setup cleanup",
		}},
		selected: 0,
		width:    100,
		height:   24,
	}

	updated, cmd := m.dispatchCommand(commands.Invocation{Kind: commands.KindCommit})
	got := updated.(Model)
	if got.commitPreview == nil {
		t.Fatalf("dispatchCommand(/commit) should open the commit preview shell immediately")
	}
	if !got.commitPreviewRefreshing {
		t.Fatalf("dispatchCommand(/commit) should mark the preview as refreshing")
	}
	if got.commitPreview.Message != "Generating commit message..." {
		t.Fatalf("loading preview message = %q, want generating placeholder", got.commitPreview.Message)
	}
	if got.commitPreview.ProjectName != "demo" {
		t.Fatalf("loading preview should use the selected project name, got %q", got.commitPreview.ProjectName)
	}
	if got.status != "Preparing commit preview..." {
		t.Fatalf("status = %q, want preparing commit preview", got.status)
	}
	if cmd == nil {
		t.Fatalf("dispatchCommand(/commit) should still return the async preview command")
	}
}

func TestDispatchFinishCommandPreservesProvidedMessageWhileLoading(t *testing.T) {
	m := Model{
		projects: []model.ProjectSummary{{
			Name:          "demo",
			Path:          "/tmp/demo",
			PresentOnDisk: true,
		}},
		selected: 0,
	}

	updated, cmd := m.dispatchCommand(commands.Invocation{
		Kind:    commands.KindFinish,
		Message: "Ship current repo changes",
	})
	got := updated.(Model)
	if got.commitPreview == nil {
		t.Fatalf("dispatchCommand(/finish) should open the commit preview shell immediately")
	}
	if got.commitPreview.Message != "Ship current repo changes" {
		t.Fatalf("loading preview should keep the provided message, got %q", got.commitPreview.Message)
	}
	if got.status != "Preparing finish preview..." {
		t.Fatalf("status = %q, want preparing finish preview", got.status)
	}
	if cmd == nil {
		t.Fatalf("dispatchCommand(/finish) should still return the async preview command")
	}
}

func TestViewWithDiffScreenUsesFullBody(t *testing.T) {
	diffState := newDiffViewState("/tmp/demo", "demo")
	diffState.loading = false
	diffState.preview = &service.DiffPreview{
		ProjectName: "demo",
		ProjectPath: "/tmp/demo",
		Branch:      "master",
		Summary:     "3 files changed, 12 insertions(+), 1 deletion(-)",
		Files: []service.DiffFilePreview{
			{
				Path:      "pixel.png",
				Summary:   "pixel.png",
				Code:      "M",
				Kind:      scanner.GitChangeModified,
				Unstaged:  true,
				Body:      "Binary image change rendered as ANSI preview.",
				IsImage:   true,
				OldImage:  mustTestPNG(color.RGBA{R: 220, G: 32, B: 32, A: 255}),
				NewImage:  mustTestPNG(color.RGBA{R: 32, G: 120, B: 220, A: 255}),
				Untracked: false,
			},
			{
				Path:      "README.md",
				Summary:   "README.md",
				Code:      "M",
				Kind:      scanner.GitChangeModified,
				Staged:    true,
				Body:      "# Staged\n\ndiff --git a/README.md b/README.md\n+diff screen\n",
				IsImage:   false,
				OldImage:  nil,
				NewImage:  nil,
				Untracked: false,
			},
		},
	}

	m := Model{
		diffView: diffState,
		width:    100,
		height:   24,
		status:   "Preparing diff view...",
	}
	m.syncDiffView(true)

	rendered := ansi.Strip(m.View())
	if got := len(strings.Split(rendered, "\n")); got != m.height {
		t.Fatalf("View() line count = %d, want terminal height %d; render was %q", got, m.height, rendered)
	}
	if !strings.Contains(rendered, "Files") || !strings.Contains(rendered, "README.md") {
		t.Fatalf("View() should render the diff file list: %q", rendered)
	}
	if !strings.Contains(rendered, "Staged (1)") || !strings.Contains(rendered, "Unstaged (1)") {
		t.Fatalf("View() should render staged and unstaged file sections: %q", rendered)
	}
	if !strings.Contains(rendered, "HEAD image") || !strings.Contains(rendered, "Working tree image") {
		t.Fatalf("View() should render image diff labels: %q", rendered)
	}
	if !strings.Contains(rendered, "Alt+Up") || !strings.Contains(rendered, "stage") || !strings.Contains(rendered, "unified") {
		t.Fatalf("View() should render the highlighted diff footer legend: %q", rendered)
	}
	if strings.Contains(rendered, "ATTN  ASSESS") || strings.Contains(rendered, "Attention reasons") {
		t.Fatalf("View() should replace the normal list/detail body when diff is open: %q", rendered)
	}
}

func TestRenderDiffFileListSeparatesStagedAndUnstagedSections(t *testing.T) {
	diffState := newDiffViewState("/tmp/demo", "demo")
	diffState.loading = false
	diffState.preview = &service.DiffPreview{
		Files: []service.DiffFilePreview{
			{
				Path:     "unstaged.txt",
				Summary:  "unstaged.txt",
				Code:     "M",
				Kind:     scanner.GitChangeModified,
				Unstaged: true,
			},
			{
				Path:    "staged.txt",
				Summary: "staged.txt",
				Code:    "M",
				Kind:    scanner.GitChangeModified,
				Staged:  true,
			},
		},
	}

	m := Model{
		diffView: diffState,
		width:    100,
		height:   20,
	}
	m.syncDiffView(true)

	rendered := ansi.Strip(m.renderDiffFileList(28, 10))
	stagedHeader := strings.Index(rendered, "Staged (1)")
	stagedFile := strings.Index(rendered, "staged.txt")
	unstagedHeader := strings.Index(rendered, "Unstaged (1)")
	unstagedFile := strings.Index(rendered, "unstaged.txt")
	if stagedHeader == -1 || stagedFile == -1 || unstagedHeader == -1 || unstagedFile == -1 {
		t.Fatalf("renderDiffFileList() should include both grouped sections and files: %q", rendered)
	}
	if !(stagedHeader < stagedFile && stagedFile < unstagedHeader && unstagedHeader < unstagedFile) {
		t.Fatalf("renderDiffFileList() should place staged files before the unstaged section: %q", rendered)
	}
}

func TestRenderDiffFileRowSelectedUsesCompactCodeSpacing(t *testing.T) {
	rendered := ansi.Strip(renderDiffFileRow(service.DiffFilePreview{
		Path:     "README.md",
		Summary:  "README.md",
		Code:     "M",
		Kind:     scanner.GitChangeModified,
		Unstaged: true,
	}, true, 28))
	if strings.Contains(rendered, "M   changed") {
		t.Fatalf("selected diff row should not add extra padding before the state label: %q", rendered)
	}
	if !strings.Contains(rendered, "M changed") {
		t.Fatalf("selected diff row should keep the compact code-to-state spacing: %q", rendered)
	}
}

func TestRenderDiffEntryBodyUsesSideBySideColumns(t *testing.T) {
	rendered := ansi.Strip(renderDiffEntryBody(service.DiffFilePreview{
		Path:    "README.md",
		Summary: "README.md",
		Code:    "M",
		Kind:    scanner.GitChangeModified,
		Body: strings.TrimSpace(`# Unstaged

diff --git a/README.md b/README.md
--- a/README.md
+++ b/README.md
@@ -1,3 +1,3 @@
-old title
+new title
 shared line
`),
	}, 84, diffRenderModeSideBySide))

	for _, want := range []string{
		"Unstaged",
		"Before",
		"After",
		"--- a/README.md",
		"+++ b/README.md",
		"@@ -1,3 +1,3 @@",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("renderDiffEntryBody() missing %q in side-by-side output: %q", want, rendered)
		}
	}

	foundPair := false
	for _, line := range strings.Split(rendered, "\n") {
		if strings.Contains(line, "-old title") && strings.Contains(line, "+new title") {
			foundPair = true
			break
		}
	}
	if !foundPair {
		t.Fatalf("renderDiffEntryBody() should place removed and added lines on the same visual row: %q", rendered)
	}
}

func TestRenderDiffEntryBodyCanUseUnifiedMode(t *testing.T) {
	rendered := ansi.Strip(renderDiffEntryBody(service.DiffFilePreview{
		Path:    "README.md",
		Summary: "README.md",
		Code:    "M",
		Kind:    scanner.GitChangeModified,
		Body: strings.TrimSpace(`# Unstaged

diff --git a/README.md b/README.md
--- a/README.md
+++ b/README.md
@@ -1,3 +1,3 @@
-old title
+new title
 shared line
`),
	}, 84, diffRenderModeUnified))

	for _, want := range []string{
		"diff --git a/README.md b/README.md",
		"@@ -1,3 +1,3 @@",
		"-old title",
		"+new title",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("unified diff output missing %q: %q", want, rendered)
		}
	}
	if strings.Contains(rendered, "Before") || strings.Contains(rendered, "After") {
		t.Fatalf("unified diff output should not render side-by-side column headers: %q", rendered)
	}
}

func TestSyntaxHighlightLexerUsesFilenameHint(t *testing.T) {
	lexer := syntaxHighlightLexer("", "main.go", "if err != nil {\n    return err\n}")
	if lexer == nil {
		t.Fatalf("expected a lexer for Go source")
	}
	if got := strings.ToLower(lexer.Config().Name); !strings.Contains(got, "go") {
		t.Fatalf("lexer name = %q, want a Go lexer", lexer.Config().Name)
	}
}

func TestDiffModeMovesSelectionAndScrollsContent(t *testing.T) {
	diffState := newDiffViewState("/tmp/demo", "demo")
	diffState.loading = false
	diffState.preview = &service.DiffPreview{
		Files: []service.DiffFilePreview{
			{
				Path:     "README.md",
				Summary:  "README.md",
				Code:     "M",
				Kind:     scanner.GitChangeModified,
				Unstaged: true,
				Body:     "# Unstaged\n\n" + strings.Repeat("+line\n", 8),
			},
			{
				Path:      "notes.txt",
				Summary:   "notes.txt",
				Code:      "??",
				Kind:      scanner.GitChangeUntracked,
				Untracked: true,
				Body:      "# Untracked\n\n" + strings.Repeat("+note\n", 40),
			},
		},
	}

	m := Model{
		diffView:     diffState,
		commandInput: textinput.New(),
		width:        100,
		height:       24,
	}
	m.syncDiffView(true)

	updated, _ := m.updateDiffMode(tea.KeyMsg{Type: tea.KeyDown})
	got := updated.(Model)
	if got.diffView.selected != 1 {
		t.Fatalf("selected index = %d, want 1", got.diffView.selected)
	}

	updated, _ = got.updateDiffMode(tea.KeyMsg{Type: tea.KeyTab})
	got = updated.(Model)
	if got.diffView.focus != diffFocusContent {
		t.Fatalf("focus = %s, want content", got.diffView.focus)
	}

	updated, _ = got.updateDiffMode(tea.KeyMsg{Type: tea.KeyDown})
	got = updated.(Model)
	if got.diffView.selected != 1 {
		t.Fatalf("down in content focus should keep file selection, got %d", got.diffView.selected)
	}
	if got.diffView.contentViewport.YOffset == 0 {
		t.Fatalf("down in content focus should scroll the diff viewport")
	}
}

func TestDiffViewCachesRenderedEntriesAcrossSelectionChanges(t *testing.T) {
	diffState := newDiffViewState("/tmp/demo", "demo")
	diffState.loading = false
	diffState.preview = &service.DiffPreview{
		Files: []service.DiffFilePreview{
			{
				Path:     "main.go",
				Summary:  "main.go",
				Code:     "M",
				Kind:     scanner.GitChangeModified,
				Unstaged: true,
				Body: strings.TrimSpace(`# Unstaged

diff --git a/main.go b/main.go
@@ -1,3 +1,4 @@
-func main() {}
+func main() {
+    println("hello")
+}
 `),
			},
			{
				Path:     "diff_view.go",
				Summary:  "diff_view.go",
				Code:     "M",
				Kind:     scanner.GitChangeModified,
				Unstaged: true,
				Body: strings.TrimSpace(`# Unstaged

diff --git a/diff_view.go b/diff_view.go
@@ -10,3 +10,5 @@
-return "before"
+if mode == diffRenderModeUnified {
+    return "after"
+}
 `),
			},
		},
	}

	m := Model{
		diffView:     diffState,
		commandInput: textinput.New(),
		width:        100,
		height:       24,
	}
	m.syncDiffView(true)

	cacheKey0 := diffRenderCacheKey{
		FileIndex: 0,
		Width:     m.diffView.contentViewport.Width,
		Mode:      diffRenderModeSideBySide,
	}
	firstRendered := m.diffView.renderedContent
	if got := len(m.diffView.renderCache); got != 1 {
		t.Fatalf("initial render cache size = %d, want 1", got)
	}
	if cached := m.diffView.renderCache[cacheKey0]; cached != firstRendered {
		t.Fatalf("cached render for first file did not match rendered content")
	}

	m.moveDiffSelectionTo(1)
	if got := len(m.diffView.renderCache); got != 2 {
		t.Fatalf("cache size after rendering second file = %d, want 2", got)
	}

	m.moveDiffSelectionTo(0)
	if got := len(m.diffView.renderCache); got != 2 {
		t.Fatalf("cache size after revisiting first file = %d, want 2", got)
	}
	if m.diffView.renderedContent != firstRendered {
		t.Fatalf("revisiting a file should reuse the cached render")
	}
}

func TestDiffModeMTogglesRenderMode(t *testing.T) {
	diffState := newDiffViewState("/tmp/demo", "demo")
	diffState.loading = false
	diffState.focus = diffFocusContent
	diffState.preview = &service.DiffPreview{
		Files: []service.DiffFilePreview{{
			Path:     "README.md",
			Summary:  "README.md",
			Code:     "M",
			Kind:     scanner.GitChangeModified,
			Unstaged: true,
			Body: strings.TrimSpace(`# Unstaged

diff --git a/README.md b/README.md
--- a/README.md
+++ b/README.md
@@ -1,3 +1,3 @@
-old title
+new title
 shared line
`),
		}},
	}

	m := Model{
		diffView:     diffState,
		commandInput: textinput.New(),
		width:        100,
		height:       24,
	}
	m.syncDiffView(true)

	if m.diffView.mode != diffRenderModeSideBySide {
		t.Fatalf("default diff mode = %s, want side-by-side", m.diffView.mode)
	}
	if !strings.Contains(ansi.Strip(m.diffView.renderedContent), "Before") {
		t.Fatalf("default diff renderer should start in side-by-side mode: %q", ansi.Strip(m.diffView.renderedContent))
	}

	updated, _ := m.updateDiffMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	got := updated.(Model)
	if got.diffView.mode != diffRenderModeUnified {
		t.Fatalf("toggling mode should switch to unified, got %s", got.diffView.mode)
	}
	if !strings.Contains(got.status, "unified") {
		t.Fatalf("status should mention unified mode after toggling: %q", got.status)
	}
	unified := ansi.Strip(got.diffView.renderedContent)
	if strings.Contains(unified, "Before") || strings.Contains(unified, "After") {
		t.Fatalf("unified mode should not show side-by-side column headers: %q", unified)
	}
	if !strings.Contains(unified, "diff --git a/README.md b/README.md") {
		t.Fatalf("unified mode should keep the regular patch text: %q", unified)
	}

	updated, _ = got.updateDiffMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	got = updated.(Model)
	if got.diffView.mode != diffRenderModeSideBySide {
		t.Fatalf("second toggle should switch back to side-by-side, got %s", got.diffView.mode)
	}
	if !strings.Contains(ansi.Strip(got.diffView.renderedContent), "Before") {
		t.Fatalf("side-by-side mode should restore the paired columns: %q", ansi.Strip(got.diffView.renderedContent))
	}
}

func TestSyntaxHighlightBlockUsesLanguageHint(t *testing.T) {
	prevProfile := lipgloss.ColorProfile()
	prevDarkBackground := lipgloss.HasDarkBackground()
	lipgloss.SetColorProfile(termenv.ANSI256)
	lipgloss.SetHasDarkBackground(true)
	t.Cleanup(func() {
		lipgloss.SetColorProfile(prevProfile)
		lipgloss.SetHasDarkBackground(prevDarkBackground)
	})

	code := "func main() {\n    if err != nil {\n        return err\n    }\n}\n"
	goRendered := syntaxHighlightBlock(code, "go", "", syntaxHighlightOptions{})
	textRendered := syntaxHighlightBlock(code, "text", "", syntaxHighlightOptions{})

	if ansi.Strip(goRendered) != code || ansi.Strip(textRendered) != code {
		t.Fatalf("syntax highlighting should preserve the visible code text")
	}
	if !strings.Contains(goRendered, "\x1b[") {
		t.Fatalf("language hint should produce ANSI styling: %q", goRendered)
	}
	if goRendered == textRendered {
		t.Fatalf("language hint should render differently from plain text")
	}
	if syntaxHighlightLexer("text", "", code) != nil {
		t.Fatalf("explicit text hint should skip syntax lexing")
	}
}

func TestDiffModeAltUpReturnsToMainList(t *testing.T) {
	diffState := newDiffViewState("/tmp/demo", "demo")
	diffState.loading = false
	diffState.preview = &service.DiffPreview{
		Files: []service.DiffFilePreview{{
			Path:     "README.md",
			Summary:  "README.md",
			Code:     "M",
			Kind:     scanner.GitChangeModified,
			Unstaged: true,
			Body:     "# Unstaged\n\n+line\n",
		}},
	}

	m := Model{
		diffView:     diffState,
		commandInput: textinput.New(),
		width:        100,
		height:       24,
	}
	m.syncDiffView(true)

	updated, cmd := m.updateDiffMode(tea.KeyMsg{Type: tea.KeyUp, Alt: true})
	got := updated.(Model)
	if got.diffView != nil {
		t.Fatalf("alt+up should close the diff view and return to the main list")
	}
	if got.status != "Focus: project list" {
		t.Fatalf("status = %q, want focus-list status", got.status)
	}
	if cmd != nil {
		t.Fatalf("alt+up should not return another command")
	}
}

func TestDiffModeEscReturnsCachedCommitPreviewWhenStateMatches(t *testing.T) {
	ctx := context.Background()
	projectPath, svc := newCommitPreviewReturnTestRepo(t, ctx)

	preview, err := svc.PrepareCommit(ctx, projectPath, service.GitActionCommit, "")
	if err != nil {
		t.Fatalf("prepare commit: %v", err)
	}
	preview.Message = "Cached preview should survive"

	diffState := newDiffViewState(projectPath, "repo")
	diffState.loading = false
	diffState.returnToCommitPreview = &commitPreviewReturnState{
		preview:         preview,
		messageOverride: "",
	}

	m := Model{
		ctx:      ctx,
		svc:      svc,
		diffView: diffState,
		width:    100,
		height:   24,
	}

	updated, cmd := m.updateDiffMode(tea.KeyMsg{Type: tea.KeyEsc})
	got := updated.(Model)
	if got.diffView != nil {
		t.Fatalf("esc should close the diff view before restoring commit preview")
	}
	if got.commitPreview == nil {
		t.Fatalf("esc should restore the cached commit preview shell")
	}
	if !got.commitPreviewRefreshing {
		t.Fatalf("esc should mark the commit preview as refreshing while the hash check runs")
	}
	if got.status != "Refreshing commit preview..." {
		t.Fatalf("status = %q, want refreshing commit preview", got.status)
	}
	if cmd == nil {
		t.Fatalf("esc should return a resume command")
	}

	msg := cmd()
	previewMsg, ok := msg.(commitPreviewMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want commitPreviewMsg", msg)
	}
	if previewMsg.err != nil {
		t.Fatalf("resume commit preview returned error: %v", previewMsg.err)
	}
	if previewMsg.preview.Message != "Cached preview should survive" {
		t.Fatalf("resume should reuse the cached preview when the hash matches, got message %q", previewMsg.preview.Message)
	}

	updated, _ = got.Update(previewMsg)
	got = updated.(Model)
	if got.commitPreviewRefreshing {
		t.Fatalf("commit preview should stop refreshing once the preview is ready")
	}
	if got.commitPreview == nil || got.commitPreview.Message != "Cached preview should survive" {
		t.Fatalf("cached commit preview should be restored, got %#v", got.commitPreview)
	}
}

func TestDiffModeEscRefreshesCommitPreviewWhenStateChanges(t *testing.T) {
	ctx := context.Background()
	projectPath, svc := newCommitPreviewReturnTestRepo(t, ctx)

	preview, err := svc.PrepareCommit(ctx, projectPath, service.GitActionCommit, "")
	if err != nil {
		t.Fatalf("prepare commit: %v", err)
	}
	preview.Message = "Cached preview should be replaced"

	diffState := newDiffViewState(projectPath, "repo")
	diffState.loading = false
	diffState.returnToCommitPreview = &commitPreviewReturnState{
		preview:         preview,
		messageOverride: "",
	}

	if err := os.WriteFile(filepath.Join(projectPath, "notes.txt"), []byte("keep this too\n"), 0o644); err != nil {
		t.Fatalf("write notes.txt: %v", err)
	}
	runTUITestGit(t, projectPath, "add", "notes.txt")

	m := Model{
		ctx:      ctx,
		svc:      svc,
		diffView: diffState,
		width:    100,
		height:   24,
	}

	updated, cmd := m.updateDiffMode(tea.KeyMsg{Type: tea.KeyEsc})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("esc should return a resume command")
	}

	msg := cmd()
	previewMsg, ok := msg.(commitPreviewMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want commitPreviewMsg", msg)
	}
	if previewMsg.err != nil {
		t.Fatalf("resume commit preview returned error: %v", previewMsg.err)
	}
	if previewMsg.preview.Message == "Cached preview should be replaced" {
		t.Fatalf("resume should rebuild the commit preview after git state changes")
	}
	if len(previewMsg.preview.Included) != 2 {
		t.Fatalf("refreshed preview should include both staged files, got %#v", previewMsg.preview.Included)
	}

	updated, _ = got.Update(previewMsg)
	got = updated.(Model)
	if got.commitPreview == nil {
		t.Fatalf("refreshed commit preview should be restored")
	}
	if got.commitPreview.Message == "Cached preview should be replaced" {
		t.Fatalf("visible commit preview should come from a refresh, got cached message %q", got.commitPreview.Message)
	}
}

func TestDiffModeDashStartsStageToggle(t *testing.T) {
	diffState := newDiffViewState("/tmp/demo", "demo")
	diffState.loading = false
	diffState.preview = &service.DiffPreview{
		Files: []service.DiffFilePreview{{
			Path:     "README.md",
			Summary:  "README.md",
			Code:     "M",
			Kind:     scanner.GitChangeModified,
			Unstaged: true,
			Body:     "# Unstaged\n\n+line\n",
		}},
	}

	m := Model{
		diffView:     diffState,
		commandInput: textinput.New(),
		width:        100,
		height:       24,
	}
	m.syncDiffView(true)

	updated, cmd := m.updateDiffMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'-'}})
	got := updated.(Model)
	if !got.diffView.loading {
		t.Fatalf("pressing - should put the diff view into loading mode")
	}
	if got.status != "Staging selected file..." {
		t.Fatalf("status = %q, want staging status", got.status)
	}
	if cmd == nil {
		t.Fatalf("pressing - should return a toggle command")
	}
}

func TestSlashCommandModeTakesPriorityOverDiffView(t *testing.T) {
	diffState := newDiffViewState("/tmp/demo", "demo")
	diffState.loading = false
	diffState.preview = &service.DiffPreview{
		Files: []service.DiffFilePreview{{
			Path:     "README.md",
			Summary:  "README.md",
			Code:     "M",
			Kind:     scanner.GitChangeModified,
			Unstaged: true,
			Body:     "# Unstaged\n\n+line\n",
		}},
	}

	m := Model{
		diffView:     diffState,
		commandInput: textinput.New(),
		width:        100,
		height:       24,
	}
	m.syncDiffView(true)

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	got := updated.(Model)
	if !got.commandMode {
		t.Fatalf("pressing / in diff mode should open command mode")
	}
	if got.diffView == nil {
		t.Fatalf("opening command mode from diff should keep the diff view active until a command replaces it")
	}
	if cmd == nil {
		t.Fatalf("opening command mode should return a blink command")
	}

	got.commandInput.SetValue("/help")
	updated, _ = got.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if !got.showHelp {
		t.Fatalf("command mode should take priority over diff key handling once opened")
	}
}

func TestCommitPreviewMsgClosesDiffView(t *testing.T) {
	diffState := newDiffViewState("/tmp/demo", "demo")
	diffState.loading = false
	diffState.preview = &service.DiffPreview{
		Files: []service.DiffFilePreview{{
			Path:     "README.md",
			Summary:  "README.md",
			Code:     "M",
			Kind:     scanner.GitChangeModified,
			Unstaged: true,
			Body:     "# Unstaged\n\n+line\n",
		}},
	}

	m := Model{
		diffView: diffState,
		width:    100,
		height:   24,
	}

	updated, _ := m.Update(commitPreviewMsg{
		preview: service.CommitPreview{
			Intent:      service.GitActionCommit,
			ProjectPath: "/tmp/demo",
			ProjectName: "demo",
			Branch:      "master",
			StageMode:   service.GitStageAllChanges,
			DiffStat:    "1 file changed",
			DiffSummary: "README.md",
			Message:     "demo commit",
		},
		projectPath: "/tmp/demo",
		intent:      service.GitActionCommit,
	})
	got := updated.(Model)
	if got.diffView != nil {
		t.Fatalf("commit preview should replace the diff view once ready")
	}
	if got.commitPreview == nil {
		t.Fatalf("commit preview should be stored when the preview message arrives")
	}
}

func mustTestPNG(fill color.RGBA) []byte {
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			img.SetRGBA(x, y, fill)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

func newCommitPreviewReturnTestRepo(t *testing.T, ctx context.Context) (string, *service.Service) {
	t.Helper()

	projectPath := filepath.Join(t.TempDir(), "repo")
	runTUITestGit(t, "", "init", projectPath)
	runTUITestGit(t, projectPath, "config", "user.name", "Little Control Room Tests")
	runTUITestGit(t, projectPath, "config", "user.email", "tests@example.com")

	if err := os.WriteFile(filepath.Join(projectPath, "README.md"), []byte("hello\nbase\n"), 0o644); err != nil {
		t.Fatalf("write README.md: %v", err)
	}
	runTUITestGit(t, projectPath, "add", "README.md")
	runTUITestGit(t, projectPath, "commit", "-m", "initial commit")

	if err := os.WriteFile(filepath.Join(projectPath, "README.md"), []byte("hello\npreview\n"), 0o644); err != nil {
		t.Fatalf("update README.md: %v", err)
	}
	runTUITestGit(t, projectPath, "add", "README.md")

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})

	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:          projectPath,
		Name:          "repo",
		PresentOnDisk: true,
		InScope:       true,
		UpdatedAt:     time.Now().UTC(),
	}); err != nil {
		t.Fatalf("upsert project: %v", err)
	}

	return projectPath, service.New(config.Default(), st, events.NewBus(), nil)
}

func runTUITestGit(t *testing.T, dir string, args ...string) string {
	t.Helper()

	cmd := exec.Command("git", args...)
	if strings.TrimSpace(dir) != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out))
}
