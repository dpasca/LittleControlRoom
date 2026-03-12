package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lcroom/internal/brand"
	"lcroom/internal/codexapp"
	"lcroom/internal/codexcli"
	"lcroom/internal/commands"
	"lcroom/internal/config"
	"lcroom/internal/model"
	"lcroom/internal/service"

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

func (s *fakeCodexSession) ProjectPath() string {
	return s.projectPath
}

func (s *fakeCodexSession) Snapshot() codexapp.Snapshot {
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

func TestProjectDisplayStatusUsesReadableAssessmentCategory(t *testing.T) {
	project := model.ProjectSummary{
		Status:                          model.StatusIdle,
		PresentOnDisk:                   true,
		LatestSessionClassification:     model.ClassificationCompleted,
		LatestSessionClassificationType: model.SessionCategoryNeedsFollowUp,
	}
	if got := projectDisplayStatus(project); got != "needs follow-up" {
		t.Fatalf("projectDisplayStatus() = %q, want %q", got, "needs follow-up")
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
	if got := projectAttentionLabel(project); got != "!  9" {
		t.Fatalf("projectAttentionLabel() = %q, want %q", got, "!  9")
	}

	project.Pinned = true
	if got := projectAttentionLabel(project); got != "!  9" {
		t.Fatalf("projectAttentionLabel() should ignore pinned rows in the list label, got %q", got)
	}

	project = model.ProjectSummary{AttentionScore: 100, RepoSyncStatus: model.RepoSyncAhead}
	if got := projectAttentionLabel(project); got != "! 10" {
		t.Fatalf("projectAttentionLabel() = %q, want %q", got, "! 10")
	}

	project = model.ProjectSummary{AttentionScore: 0}
	if got := projectAttentionLabel(project); got != "   0" {
		t.Fatalf("projectAttentionLabel() = %q, want %q", got, "   0")
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
	if got := compactUsageLabel(model.LLMSessionUsage{}); got != "tok off" {
		t.Fatalf("compactUsageLabel(disabled) = %q, want %q", got, "tok off")
	}

	usage := model.LLMSessionUsage{
		Enabled: true,
		Totals: model.LLMUsage{
			InputTokens:  345,
			OutputTokens: 538,
		},
	}
	if got := compactUsageLabel(usage); got != "tok 345/538" {
		t.Fatalf("compactUsageLabel(enabled) = %q, want %q", got, "tok 345/538")
	}
}

func TestLLMHelpLines(t *testing.T) {
	usage := model.LLMSessionUsage{
		Enabled:   true,
		Model:     "gpt-5-mini-2025-08-07",
		Running:   1,
		Started:   3,
		Completed: 2,
		Failed:    1,
		Totals: model.LLMUsage{
			InputTokens:       1200,
			OutputTokens:      538,
			TotalTokens:       1738,
			CachedInputTokens: 45,
			ReasoningTokens:   12,
		},
	}

	lines := llmHelpLines(usage, 1)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "state  busy x1 /") {
		t.Fatalf("help lines missing running state: %q", joined)
	}
	if !strings.Contains(joined, "model  gpt-5-mini-2025-08-07") {
		t.Fatalf("help lines missing model: %q", joined)
	}
	if !strings.Contains(joined, "tok    in=1.2k out=538 total=1.7k") {
		t.Fatalf("help lines missing token totals: %q", joined)
	}
	if !strings.Contains(joined, "extra  cache=45 reason=12") {
		t.Fatalf("help lines missing extra totals: %q", joined)
	}
}

func TestSessionCategoryLabel(t *testing.T) {
	if got := sessionCategoryLabel(model.SessionCategoryNeedsFollowUp); got != "needs follow-up" {
		t.Fatalf("sessionCategoryLabel(needs_follow_up) = %q, want %q", got, "needs follow-up")
	}
	if got := sessionCategoryLabel(model.SessionCategoryWaitingForUser); got != "waiting for user" {
		t.Fatalf("sessionCategoryLabel(waiting_for_user) = %q, want %q", got, "waiting for user")
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
	if got := projectAssessmentText(project); got != "running" {
		t.Fatalf("projectAssessmentText(running) = %q, want %q", got, "running")
	}

	project = model.ProjectSummary{
		LatestSessionClassification:               model.ClassificationRunning,
		LatestSessionClassificationStage:          model.ClassificationStageWaitingForModel,
		LatestSessionClassificationStageStartedAt: time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC),
	}
	if got := projectAssessmentTextAt(project, time.Date(2026, 3, 10, 12, 0, 37, 0, time.UTC)); got != "waiting for model 00:37" {
		t.Fatalf("projectAssessmentTextAt(waiting_for_model) = %q, want %q", got, "waiting for model 00:37")
	}

	project = model.ProjectSummary{
		LatestSessionFormat: "modern",
	}
	if got := projectAssessmentText(project); got != "not assessed yet" {
		t.Fatalf("projectAssessmentText(unassessed) = %q, want %q", got, "not assessed yet")
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
	if !strings.Contains(rendered, "RUN") {
		t.Fatalf("renderProjectList() missing RUN header: %q", rendered)
	}
	if !strings.Contains(rendered, "ASSESSMENT") {
		t.Fatalf("renderProjectList() missing assessment header: %q", rendered)
	}
	if !strings.Contains(rendered, "A concrete next step still remains.") {
		t.Fatalf("renderProjectList() missing latest assessment summary: %q", rendered)
	}
}

func TestProjectRunLabelUsesLiveBusyTimer(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Started:   true,
			Busy:      true,
			BusySince: time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC),
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

	got, running := m.projectRunLabel(project, time.Date(2026, 3, 9, 12, 0, 37, 0, time.UTC))
	if !running {
		t.Fatalf("projectRunLabel() running = false, want true")
	}
	if got != "00:37" {
		t.Fatalf("projectRunLabel() = %q, want %q", got, "00:37")
	}
}

func TestProjectRunLabelFallsBackToLatestTurnStartWhenLiveBusyTimerMissing(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Started: true,
			Busy:    true,
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
		LatestTurnStartedAt: time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC),
	}

	got, running := m.projectRunLabel(project, time.Date(2026, 3, 9, 12, 0, 37, 0, time.UTC))
	if !running {
		t.Fatalf("projectRunLabel() running = false, want true")
	}
	if got != "00:37" {
		t.Fatalf("projectRunLabel() = %q, want %q", got, "00:37")
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
	if !strings.Contains(lines[1], "!  9") {
		t.Fatalf("renderProjectList() should show repo warnings in ATTN, got %q", lines[1])
	}
	if strings.Contains(lines[1], "demo project !") {
		t.Fatalf("renderProjectList() should keep the project name free of suffix markers, got %q", lines[1])
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
	if !strings.Contains(rendered, "Repo: dirty worktree  Remote: ahead by 2") {
		t.Fatalf("View() should keep repo and remote on one compact row: %q", rendered)
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
	if !strings.Contains(rendered, "needs follow-up") {
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
	if !strings.Contains(rendered, "waiting for user") {
		t.Fatalf("renderDetailContent() missing assessment-based label: %q", rendered)
	}
	if !strings.Contains(rendered, "Activity:") || !strings.Contains(rendered, "idle") {
		t.Fatalf("renderDetailContent() missing separate activity field: %q", rendered)
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
	if !strings.Contains(rendered, "Assessment: waiting for model 00:37") {
		t.Fatalf("renderDetailContent() missing assessment stage timing: %q", rendered)
	}
	if !strings.Contains(rendered, "- assessment waiting for model 00:37") {
		t.Fatalf("renderDetailContent() missing session summary stage timing: %q", rendered)
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

func TestRefreshKeyDoesNothing(t *testing.T) {
	m := Model{}

	updated, cmd := m.updateNormalMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("refresh key should no longer return a scan command")
	}
	if got.status != "" {
		t.Fatalf("status = %q, want empty status after removed shortcut", got.status)
	}
	if got.loading {
		t.Fatalf("loading = true, want false after removed shortcut")
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
	if !strings.Contains(rendered, "/new [prompt]") || !strings.Contains(rendered, "/model") || !strings.Contains(rendered, "/status") {
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
	if got.codexInput.Value() != "/model" {
		t.Fatalf("codex input = %q, want /model after second tab", got.codexInput.Value())
	}

	updated, cmd = got.updateCodexMode(tea.KeyMsg{Type: tea.KeyTab})
	got = updated.(Model)
	if cmd != nil {
		t.Fatalf("tab cycling should not queue a command")
	}
	if got.codexInput.Value() != "/status" {
		t.Fatalf("codex input = %q, want /status after third tab", got.codexInput.Value())
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
	if !strings.Contains(rendered, "Codex Sessions") || !strings.Contains(rendered, "demo") {
		t.Fatalf("picker overlay should render the session list: %q", rendered)
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
	if len(got.settingsFields) != 7 {
		t.Fatalf("settings field count = %d, want 7", len(got.settingsFields))
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
	if !strings.Contains(rendered, "│ AT") || !strings.Contains(rendered, "│ Pa") {
		t.Fatalf("View() should preserve background list and detail context under the settings modal: %q", rendered)
	}
}

func TestSettingsModalRendersColoredActionLegend(t *testing.T) {
	m := Model{
		settingsMode:   true,
		settingsFields: newSettingsFields(config.EditableSettingsFromAppConfig(config.Default())),
		width:          100,
		height:         24,
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
	if !strings.Contains(saved.status, "Name filters and Codex launch mode apply now") {
		t.Fatalf("status = %q, want immediate-apply notice", saved.status)
	}

	configPath := filepath.Join(home, ".little-control-room", "config.toml")
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read saved config: %v", err)
	}
	text := string(raw)
	if !strings.Contains(text, "include_paths = [") || !strings.Contains(text, "exclude_paths = [") || !strings.Contains(text, "exclude_project_patterns = [") || !strings.Contains(text, "codex_launch_preset = \"full-auto\"") || !strings.Contains(text, "interval = \"45s\"") {
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
	if !strings.Contains(got.status, "Name filters and Codex launch mode apply now") {
		t.Fatalf("status = %q, want immediate-apply notice", got.status)
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
	if !strings.Contains(rendered, "Commit Preview for demo (master)") {
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
	if !strings.Contains(rendered, "ATT") || !strings.Contains(rendered, "Attention reasons") {
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
	if !strings.Contains(rendered, "Nothing To Commit") || !strings.Contains(rendered, "quickgame_30") {
		t.Fatalf("rendered dialog should identify the project and empty-commit state: %q", rendered)
	}
	if !strings.Contains(rendered, "ahead of upstream by 4 commit(s)") {
		t.Fatalf("rendered dialog should show ahead status: %q", rendered)
	}
	if !strings.Contains(rendered, "push 4 existing commits") {
		t.Fatalf("rendered dialog should offer pushing existing commits: %q", rendered)
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

func TestRenderHelpPanelOmitsForgetHint(t *testing.T) {
	m := Model{}

	rendered := ansi.Strip(m.renderHelpPanel(80, 20))
	if strings.Contains(rendered, "f   forget missing") {
		t.Fatalf("renderHelpPanel() should not advertise forget while it is unavailable: %q", rendered)
	}
	if strings.Contains(rendered, "r   rescan + retry failed AI") {
		t.Fatalf("renderHelpPanel() should not advertise the removed refresh key: %q", rendered)
	}
	if !strings.Contains(rendered, "/refresh /settings") {
		t.Fatalf("renderHelpPanel() should continue to advertise refresh via slash command: %q", rendered)
	}
}
