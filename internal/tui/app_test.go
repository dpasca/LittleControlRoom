package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	"lcroom/internal/attention"
	bossui "lcroom/internal/boss"
	"lcroom/internal/brand"
	"lcroom/internal/browserctl"
	"lcroom/internal/codexapp"
	"lcroom/internal/codexcli"
	"lcroom/internal/commands"
	"lcroom/internal/config"
	"lcroom/internal/events"
	"lcroom/internal/model"
	"lcroom/internal/procinspect"
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
	projectPath      string
	snapshot         codexapp.Snapshot
	snapshotCalls    int
	trySnapshotCalls int
	trySnapshotFn    func(*fakeCodexSession) (codexapp.Snapshot, bool)
	submitted        []string
	submissions      []codexapp.Submission
	decisions        []codexapp.ApprovalDecision
	toolAnswers      []map[string][]string
	elicitations     []fakeElicitationResponse
	statusCalls      int
	compactCalls     int
	reviewCalls      int
	interrupted      bool
	refreshCalls     int
	refreshBusyFn    func(*fakeCodexSession) error
	compactFn        func(*fakeCodexSession) error
	reviewFn         func(*fakeCodexSession) error
	models           []codexapp.ModelOption
	modelStages      []struct {
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

func (s *fakeCodexSession) StateSnapshot() codexapp.Snapshot {
	snapshot := s.snapshot
	snapshot.ProjectPath = s.projectPath
	return snapshot
}

func (s *fakeCodexSession) TrySnapshot() (codexapp.Snapshot, bool) {
	s.trySnapshotCalls++
	if s.trySnapshotFn != nil {
		return s.trySnapshotFn(s)
	}
	return s.Snapshot(), true
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

func (s *fakeCodexSession) Compact() error {
	s.compactCalls++
	if s.compactFn != nil {
		return s.compactFn(s)
	}
	return nil
}

func (s *fakeCodexSession) Review() error {
	s.reviewCalls++
	if s.reviewFn != nil {
		return s.reviewFn(s)
	}
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

func collectCmdMsgs(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	var msg tea.Msg
	func() {
		defer func() {
			if recover() != nil {
				msg = nil
			}
		}()
		msg = cmd()
	}()
	if msg == nil {
		return nil
	}
	switch v := msg.(type) {
	case tea.BatchMsg:
		var out []tea.Msg
		for _, child := range v {
			out = append(out, collectCmdMsgs(child)...)
		}
		return out
	default:
		return []tea.Msg{msg}
	}
}

func drainCmdMsgs(m Model, cmd tea.Cmd) Model {
	queue := collectCmdMsgs(cmd)
	for len(queue) > 0 {
		msg := queue[0]
		queue = queue[1:]
		updated, next := m.Update(msg)
		m = updated.(Model)
		queue = append(queue, collectCmdMsgs(next)...)
	}
	return m
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

	m := Model{codexManager: manager}
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

func TestRenderFooterShowsWorktreeHintsForRepoFamily(t *testing.T) {
	rootPath := "/tmp/repo"
	m := Model{
		focusedPane: focusProjects,
		allProjects: []model.ProjectSummary{
			{
				Name:             "repo",
				Path:             rootPath,
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindMain,
			},
			{
				Name:             "repo--feat-parallel-lane",
				Path:             "/tmp/repo--feat-parallel-lane",
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindLinked,
				RepoBranch:       "feat/parallel-lane",
			},
		},
		visibility: visibilityAllFolders,
		sortMode:   sortByAttention,
	}
	m.rebuildProjectList(rootPath)

	rendered := ansi.Strip(m.renderFooter(160))
	if !strings.Contains(rendered, "w lanes") {
		t.Fatalf("renderFooter() should advertise worktree lane toggling, got %q", rendered)
	}
	if strings.Contains(rendered, "P prune") {
		t.Fatalf("renderFooter() should not advertise a Prune hotkey, got %q", rendered)
	}
}

func TestRenderFooterShowsRemoveHintForLinkedWorktree(t *testing.T) {
	rootPath := "/tmp/repo"
	childPath := "/tmp/repo--feat-parallel-lane"
	m := Model{
		focusedPane: focusProjects,
		allProjects: []model.ProjectSummary{
			{
				Name:             "repo",
				Path:             rootPath,
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindMain,
			},
			{
				Name:             "repo--feat-parallel-lane",
				Path:             childPath,
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindLinked,
				RepoBranch:       "feat/parallel-lane",
			},
		},
		visibility: visibilityAllFolders,
		sortMode:   sortByAttention,
	}
	m.rebuildProjectList(childPath)
	if len(m.projects) > 1 {
		m.selected = 1
	}

	rendered := ansi.Strip(m.renderFooter(160))
	if !strings.Contains(rendered, "w lanes") {
		t.Fatalf("renderFooter() should keep lane toggling available on a linked worktree row, got %q", rendered)
	}
	if !strings.Contains(rendered, "x remove") {
		t.Fatalf("renderFooter() should advertise linked worktree removal when it is allowed, got %q", rendered)
	}
}

func TestRenderFooterShowsRemoveHintForLinkedWorktreeWithActiveSession(t *testing.T) {
	rootPath := "/tmp/repo"
	childPath := "/tmp/repo--feat-parallel-lane"
	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider: req.Provider.Normalized(),
				Started:  true,
				Status:   req.Provider.Label() + " session ready",
			},
		}, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: childPath,
		Provider:    codexapp.ProviderClaudeCode,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	m := Model{
		codexManager: manager,
		focusedPane:  focusProjects,
		allProjects: []model.ProjectSummary{
			{
				Name:             "repo",
				Path:             rootPath,
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindMain,
			},
			{
				Name:             "repo--feat-parallel-lane",
				Path:             childPath,
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindLinked,
				RepoBranch:       "feat/parallel-lane",
			},
		},
		visibility: visibilityAllFolders,
		sortMode:   sortByAttention,
	}
	m.rebuildProjectList(childPath)
	if len(m.projects) > 1 {
		m.selected = 1
	}

	rendered := ansi.Strip(m.renderFooter(160))
	if !strings.Contains(rendered, "x remove") {
		t.Fatalf("renderFooter() should still advertise linked worktree removal with an active session, got %q", rendered)
	}
}

func TestRenderFooterShowsMergeHintForLinkedWorktreeWithParentBranch(t *testing.T) {
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
				PresentOnDisk:        true,
				WorktreeRootPath:     rootPath,
				WorktreeKind:         model.WorktreeKindLinked,
				WorktreeParentBranch: "master",
				RepoBranch:           "feat/parallel-lane",
			},
		},
		visibility: visibilityAllFolders,
		sortMode:   sortByAttention,
	}
	m.rebuildProjectList(childPath)

	rendered := ansi.Strip(m.renderFooter(160))
	if !strings.Contains(rendered, "M merge") {
		t.Fatalf("renderFooter() should advertise linked worktree merge-back when a parent branch is known, got %q", rendered)
	}
}

func TestRenderFooterShowsCommitMergeHintForDirtyLinkedWorktree(t *testing.T) {
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
				PresentOnDisk:        true,
				WorktreeRootPath:     rootPath,
				WorktreeKind:         model.WorktreeKindLinked,
				WorktreeParentBranch: "master",
				RepoBranch:           "feat/parallel-lane",
				RepoDirty:            true,
			},
		},
		visibility: visibilityAllFolders,
		sortMode:   sortByAttention,
	}
	m.rebuildProjectList(childPath)

	rendered := ansi.Strip(m.renderFooter(160))
	if !strings.Contains(rendered, "M commit+merge") {
		t.Fatalf("renderFooter() should advertise commit+merge for dirty linked worktrees, got %q", rendered)
	}
}

func TestRenderFooterHidesMergeHintDuringCommitInFlight(t *testing.T) {
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
				PresentOnDisk:        true,
				WorktreeRootPath:     rootPath,
				WorktreeKind:         model.WorktreeKindLinked,
				WorktreeParentBranch: "master",
				RepoBranch:           "feat/parallel-lane",
			},
		},
		visibility: visibilityAllFolders,
		sortMode:   sortByAttention,
		pendingGitSummaries: map[string]string{
			childPath: "Committing...",
		},
	}
	m.rebuildProjectList(childPath)

	rendered := ansi.Strip(m.renderFooter(160))
	if strings.Contains(rendered, "M merge") {
		t.Fatalf("renderFooter() should hide merge action while commit is in flight")
	}
}

func TestRenderDetailContentShowsWorktreeActions(t *testing.T) {
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
				PresentOnDisk:        true,
				WorktreeRootPath:     rootPath,
				WorktreeKind:         model.WorktreeKindLinked,
				WorktreeParentBranch: "master",
				RepoBranch:           "feat/parallel-lane",
			},
		},
		visibility: visibilityAllFolders,
		sortMode:   sortByAttention,
	}
	m.rebuildProjectList(childPath)

	rendered := ansi.Strip(m.renderDetailContent(100))
	if !strings.Contains(rendered, "Worktree actions") {
		t.Fatalf("renderDetailContent() should include a worktree actions section, got %q", rendered)
	}
	if !strings.Contains(rendered, "M or /wt merge") || !strings.Contains(rendered, "x or /wt remove") {
		t.Fatalf("renderDetailContent() should list worktree hotkeys and slash commands, got %q", rendered)
	}
	if strings.Contains(rendered, "/wt prune") {
		t.Fatalf("renderDetailContent() should skip prune hint for linked worktree selection, got %q", rendered)
	}
}

func TestRenderDetailContentForLinkedWorktreeSkipsPruneHint(t *testing.T) {
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
				PresentOnDisk:        true,
				WorktreeRootPath:     rootPath,
				WorktreeKind:         model.WorktreeKindLinked,
				WorktreeParentBranch: "master",
				RepoBranch:           "feat/parallel-lane",
			},
		},
		visibility: visibilityAllFolders,
		sortMode:   sortByAttention,
	}
	m.rebuildProjectList(childPath)

	rendered := ansi.Strip(m.renderDetailContent(100))
	if strings.Contains(rendered, "/wt prune") {
		t.Fatalf("renderDetailContent() should not include prune for linked worktree selection, got %q", rendered)
	}
}

func TestRenderDetailContentForRootProjectShowsPruneSlashCommand(t *testing.T) {
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
			},
			{
				Name:             "repo--feat-parallel-lane",
				Path:             "/tmp/repo--feat-parallel-lane",
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindLinked,
				RepoBranch:       "feat/parallel-lane",
			},
		},
		visibility: visibilityAllFolders,
		sortMode:   sortByAttention,
	}
	m.rebuildProjectList(rootPath)

	rendered := ansi.Strip(m.renderDetailContent(100))
	if !strings.Contains(rendered, "Worktree actions") {
		t.Fatalf("renderDetailContent() should include a worktree actions section, got %q", rendered)
	}
	if strings.Contains(rendered, "M or /wt merge") || strings.Contains(rendered, "x or /wt remove") {
		t.Fatalf("renderDetailContent() should only show root-level worktree actions, got %q", rendered)
	}
	if !strings.Contains(rendered, "/wt prune") {
		t.Fatalf("renderDetailContent() should include /wt prune for the worktree root, got %q", rendered)
	}
}

func TestRenderDetailContentShowsWorktreeMergeStatus(t *testing.T) {
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
				PresentOnDisk:        true,
				WorktreeRootPath:     rootPath,
				WorktreeKind:         model.WorktreeKindLinked,
				WorktreeParentBranch: "master",
				WorktreeMergeStatus:  model.WorktreeMergeStatusNotMerged,
				RepoBranch:           "feat/parallel-lane",
			},
		},
		visibility: visibilityAllFolders,
		sortMode:   sortByAttention,
	}
	m.rebuildProjectList(childPath)

	rendered := ansi.Strip(m.renderDetailContent(100))
	if !strings.Contains(rendered, "Merge status:") {
		t.Fatalf("renderDetailContent() should show a merge status field for linked worktrees, got %q", rendered)
	}
	if !strings.Contains(rendered, "ready to merge into master") {
		t.Fatalf("renderDetailContent() should show the linked worktree merge status, got %q", rendered)
	}
	if !strings.Contains(rendered, "needs merge") {
		t.Fatalf("renderDetailContent() should include worktree lane merge status in the family list, got %q", rendered)
	}
}

func TestRenderDetailContentShowsWorktreeMergeInProgress(t *testing.T) {
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
				PresentOnDisk:        true,
				WorktreeRootPath:     rootPath,
				WorktreeKind:         model.WorktreeKindLinked,
				WorktreeParentBranch: "master",
				WorktreeMergeStatus:  model.WorktreeMergeStatusMergeInProgress,
				RepoBranch:           "feat/parallel-lane",
			},
		},
		visibility: visibilityAllFolders,
		sortMode:   sortByAttention,
	}
	m.rebuildProjectList(childPath)

	rendered := ansi.Strip(m.renderDetailContent(100))
	if !strings.Contains(rendered, "merging into master") {
		t.Fatalf("renderDetailContent() should show in-progress merge status, got %q", rendered)
	}
	if strings.Contains(rendered, "ready to merge into master") {
		t.Fatalf("renderDetailContent() should not show in-progress merges as unmerged, got %q", rendered)
	}
	if worktreeNeedsMergeBack(m.allProjects[1]) {
		t.Fatalf("merge-in-progress worktree should not be counted as still needing merge-back")
	}
}

func TestRenderDetailContentPrioritizesDirtyWorktreeMergeReadiness(t *testing.T) {
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
				PresentOnDisk:        true,
				WorktreeRootPath:     rootPath,
				WorktreeKind:         model.WorktreeKindLinked,
				WorktreeParentBranch: "master",
				WorktreeMergeStatus:  model.WorktreeMergeStatusNotMerged,
				RepoBranch:           "feat/parallel-lane",
				RepoDirty:            true,
			},
		},
		visibility: visibilityAllFolders,
		sortMode:   sortByAttention,
	}
	m.rebuildProjectList(childPath)

	rendered := ansi.Strip(m.renderDetailContent(100))
	if !strings.Contains(rendered, "commit changes before merging into master") {
		t.Fatalf("renderDetailContent() should put dirty merge readiness first, got %q", rendered)
	}
	if strings.Contains(rendered, "Merge status: ready to merge into master") {
		t.Fatalf("renderDetailContent() should not present dirty worktrees as merge-ready, got %q", rendered)
	}
	if !strings.Contains(rendered, "M or /wt merge (commit dirty changes first)") {
		t.Fatalf("renderDetailContent() should describe the commit+merge action for dirty worktrees, got %q", rendered)
	}
}

func TestRenderDetailContentDoesNotImplyDirtyIntegratedWorktreeWasMerged(t *testing.T) {
	rootPath := "/tmp/repo"
	childPath := "/tmp/repo--todo-reconnect-exhaustion"
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
				Name:                 "repo--todo-reconnect-exhaustion",
				Path:                 childPath,
				PresentOnDisk:        true,
				WorktreeRootPath:     rootPath,
				WorktreeKind:         model.WorktreeKindLinked,
				WorktreeParentBranch: "master",
				WorktreeMergeStatus:  model.WorktreeMergeStatusMerged,
				RepoBranch:           "todo/reconnect-exhaustion",
				RepoDirty:            true,
			},
		},
		visibility: visibilityAllFolders,
		sortMode:   sortByAttention,
	}
	m.rebuildProjectList(childPath)

	rendered := ansi.Strip(m.renderDetailContent(100))
	if !strings.Contains(rendered, "nothing to merge; local changes") {
		t.Fatalf("renderDetailContent() should describe branch ancestry without implying a merge happened, got %q", rendered)
	}
	if strings.Contains(rendered, "merged into master") {
		t.Fatalf("renderDetailContent() should not imply the dirty worktree was merged into master, got %q", rendered)
	}
}

func TestRenderDetailContentShowsRepoCentricWorktreeSummary(t *testing.T) {
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
				Name:             "repo--feat-parallel-lane",
				Path:             childPath,
				Status:           model.StatusActive,
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindLinked,
				RepoBranch:       "feat/parallel-lane",
				RepoDirty:        true,
			},
		},
		visibility: visibilityAllFolders,
		sortMode:   sortByAttention,
	}
	m.rebuildProjectList(rootPath)

	rendered := ansi.Strip(m.renderDetailContent(100))
	if !strings.Contains(rendered, "Worktrees:") {
		t.Fatalf("renderDetailContent() should show worktree family details for the repo root, got %q", rendered)
	}
	if !strings.Contains(rendered, "root + 1 linked, 1 active, 1 dirty") {
		t.Fatalf("renderDetailContent() should describe the family without counting the root as a generic worktree, got %q", rendered)
	}
}

func TestRenderDetailContentShowsOrphanedWorktreeWarning(t *testing.T) {
	rootPath := "/tmp/repo"
	orphanPath := "/tmp/repo--stale-lane"
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
		},
		orphanedWorktreesByRoot: map[string][]model.ProjectSummary{
			rootPath: {{
				Name:                "repo--stale-lane",
				Path:                orphanPath,
				Status:              model.StatusIdle,
				PresentOnDisk:       true,
				Forgotten:           true,
				WorktreeRootPath:    rootPath,
				WorktreeKind:        model.WorktreeKindLinked,
				WorktreeMergeStatus: model.WorktreeMergeStatusMerged,
				RepoBranch:          "todo/stale-lane",
			}},
		},
		visibility: visibilityAllFolders,
		sortMode:   sortByAttention,
	}
	m.rebuildProjectList(rootPath)

	rendered := ansi.Strip(m.renderDetailContent(120))
	if !strings.Contains(rendered, "Worktree warnings") {
		t.Fatalf("renderDetailContent() should add an orphaned-worktree warning section, got %q", rendered)
	}
	if !strings.Contains(rendered, "1 orphaned checkout(s) still exist on disk") {
		t.Fatalf("renderDetailContent() should explain the orphaned checkout state, got %q", rendered)
	}
	if !strings.Contains(rendered, "todo/stale-lane · orphaned, nothing to merge") {
		t.Fatalf("renderDetailContent() should list the orphaned checkout branch and status, got %q", rendered)
	}
	if !strings.Contains(rendered, orphanPath) {
		t.Fatalf("renderDetailContent() should show the orphaned checkout path, got %q", rendered)
	}
}

func TestRenderDetailContentShowsSessionSummaryBeforeWorktreeInfo(t *testing.T) {
	rootPath := "/tmp/repo"
	childPath := "/tmp/repo--feat-parallel-lane"
	m := Model{
		allProjects: []model.ProjectSummary{
			{
				Name:                             "repo",
				Path:                             rootPath,
				Status:                           model.StatusIdle,
				PresentOnDisk:                    true,
				WorktreeRootPath:                 rootPath,
				WorktreeKind:                     model.WorktreeKindMain,
				RepoBranch:                       "master",
				LatestSessionClassification:      model.ClassificationCompleted,
				LatestSessionClassificationType:  model.SessionCategoryNeedsFollowUp,
				LatestSessionSummary:             "Follow the root plan before merging any lane.",
				LatestSessionFormat:              "modern",
				LatestSessionDetectedProjectPath: rootPath,
			},
			{
				Name:             "repo--feat-parallel-lane",
				Path:             childPath,
				Status:           model.StatusActive,
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindLinked,
				RepoBranch:       "feat/parallel-lane",
				RepoDirty:        true,
			},
		},
		visibility: visibilityAllFolders,
		sortMode:   sortByAttention,
		detail: model.ProjectDetail{
			LatestSessionClassification: &model.SessionClassification{
				Status:   model.ClassificationCompleted,
				Category: model.SessionCategoryNeedsFollowUp,
				Summary:  "Follow the root plan before merging any lane.",
			},
		},
	}
	m.rebuildProjectList(rootPath)

	rendered := ansi.Strip(m.renderDetailContent(100))
	summaryIndex := strings.Index(rendered, "Summary:")
	pathIndex := strings.Index(rendered, "Path:")
	worktreesIndex := strings.Index(rendered, "Worktrees:")
	if summaryIndex < 0 || pathIndex < 0 || worktreesIndex < 0 {
		t.Fatalf("renderDetailContent() missing summary or worktree sections: %q", rendered)
	}
	if summaryIndex > pathIndex || summaryIndex > worktreesIndex {
		t.Fatalf("renderDetailContent() should show the summary at the top before path and worktree info: %q", rendered)
	}
}

func TestRenderDetailContentSkipsWorktreeLaneSectionForLinkedSelection(t *testing.T) {
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
				Status:               model.StatusActive,
				PresentOnDisk:        true,
				WorktreeRootPath:     rootPath,
				WorktreeKind:         model.WorktreeKindLinked,
				WorktreeParentBranch: "master",
				WorktreeMergeStatus:  model.WorktreeMergeStatusNotMerged,
				RepoBranch:           "feat/parallel-lane",
				RepoDirty:            true,
			},
		},
		visibility: visibilityAllFolders,
		sortMode:   sortByAttention,
	}
	m.rebuildProjectList(childPath)

	rendered := ansi.Strip(m.renderDetailContent(100))
	if strings.Contains(rendered, "Worktree lanes") {
		t.Fatalf("renderDetailContent() should keep the family lane list on the root project only, got %q", rendered)
	}
	if !strings.Contains(rendered, "Worktree actions") {
		t.Fatalf("renderDetailContent() should keep linked-worktree actions available, got %q", rendered)
	}
}

func TestUpdateNormalModeMOpensWorktreeMergeConfirm(t *testing.T) {
	rootPath := "/tmp/repo"
	childPath := "/tmp/repo--feat-parallel-lane"
	m := Model{
		focusedPane: focusProjects,
		allProjects: []model.ProjectSummary{
			{
				Name:             "repo",
				Path:             rootPath,
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindMain,
				RepoBranch:       "master",
			},
			{
				Name:                 "repo--feat-parallel-lane",
				Path:                 childPath,
				PresentOnDisk:        true,
				WorktreeRootPath:     rootPath,
				WorktreeKind:         model.WorktreeKindLinked,
				WorktreeParentBranch: "master",
				RepoBranch:           "feat/parallel-lane",
			},
		},
		visibility: visibilityAllFolders,
		sortMode:   sortByAttention,
		width:      120,
		height:     28,
	}
	m.rebuildProjectList(childPath)

	updated, cmd := m.updateNormalMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'M'}})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("merge confirm should open without scheduling a command")
	}
	if got.worktreeMergeConfirm == nil {
		t.Fatalf("M should open the worktree merge-back confirmation dialog")
	}
	if got.worktreeMergeConfirm.TargetBranch != "master" {
		t.Fatalf("merge confirm target branch = %q, want master", got.worktreeMergeConfirm.TargetBranch)
	}
}

func TestUpdateNormalModeMBlockedWhenCommitIsInFlight(t *testing.T) {
	rootPath := "/tmp/repo"
	childPath := "/tmp/repo--feat-parallel-lane"
	workingPath := "/tmp/repo--feat-parallel-lane-wip"
	m := Model{
		focusedPane: focusProjects,
		allProjects: []model.ProjectSummary{
			{
				Name:             "repo",
				Path:             rootPath,
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindMain,
				RepoBranch:       "master",
			},
			{
				Name:                 "repo--feat-parallel-lane",
				Path:                 childPath,
				PresentOnDisk:        true,
				WorktreeRootPath:     rootPath,
				WorktreeKind:         model.WorktreeKindLinked,
				WorktreeParentBranch: "master",
				RepoBranch:           "feat/parallel-lane",
			},
			{
				Name:                 "repo--feat-parallel-lane-wip",
				Path:                 workingPath,
				PresentOnDisk:        true,
				WorktreeRootPath:     rootPath,
				WorktreeKind:         model.WorktreeKindLinked,
				WorktreeParentBranch: "master",
				RepoBranch:           "feat/parallel-lane-wip",
			},
		},
		visibility: visibilityAllFolders,
		sortMode:   sortByAttention,
		width:      120,
		height:     28,
		pendingGitSummaries: map[string]string{
			workingPath: "Committing...",
		},
	}
	m.rebuildProjectList(childPath)

	updated, cmd := m.updateNormalMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'M'}})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("commit-in-flight should block merge confirm without scheduling work")
	}
	if got.worktreeMergeConfirm != nil {
		t.Fatalf("merge confirm should remain closed while a commit is running")
	}
	if got.status != "A commit is still in progress. Finish it before merging this worktree back." {
		t.Fatalf("status = %q, want commit in-flight gate message", got.status)
	}
}

func TestUpdateNormalModeMShowsBlockedMergeWhenWorktreesDirty(t *testing.T) {
	rootPath := "/tmp/repo"
	childPath := "/tmp/repo--feat-parallel-lane"
	m := Model{
		focusedPane: focusProjects,
		allProjects: []model.ProjectSummary{
			{
				Name:             "repo",
				Path:             rootPath,
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindMain,
				RepoBranch:       "master",
				RepoDirty:        true,
			},
			{
				Name:                 "repo--feat-parallel-lane",
				Path:                 childPath,
				PresentOnDisk:        true,
				WorktreeRootPath:     rootPath,
				WorktreeKind:         model.WorktreeKindLinked,
				WorktreeParentBranch: "master",
				RepoBranch:           "feat/parallel-lane",
				RepoDirty:            true,
			},
		},
		visibility: visibilityAllFolders,
		sortMode:   sortByAttention,
		width:      120,
		height:     28,
	}
	m.rebuildProjectList(childPath)

	updated, cmd := m.updateNormalMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'M'}})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("blocked merge confirm should open without scheduling work")
	}
	if got.worktreeMergeConfirm == nil {
		t.Fatalf("M should still open the merge-back dialog in blocked state")
	}
	if worktreeMergeConfirmReady(got.worktreeMergeConfirm) {
		t.Fatalf("merge confirm should start blocked when source/root are dirty")
	}
	if !got.worktreeMergeConfirm.SourceDirty {
		t.Fatalf("blocked merge should offer a commit when the source worktree is dirty")
	}
	if !strings.Contains(worktreeMergeConfirmBlockReason(got.worktreeMergeConfirm), "The root checkout is dirty") {
		t.Fatalf("blocked reason = %q, want root dirty reason", worktreeMergeConfirmBlockReason(got.worktreeMergeConfirm))
	}
	rendered := ansi.Strip(got.renderWorktreeMergeConfirmOverlay("", 100, 24))
	if !strings.Contains(rendered, "Merge blocked") || !strings.Contains(rendered, "The root checkout is dirty") || !strings.Contains(rendered, "Commit worktree changes first") {
		t.Fatalf("blocked merge overlay should explain why merge is unavailable, got %q", rendered)
	}
}

func TestOpenWorktreeMergeConfirmRefreshesLiveRootDirtyStatus(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	rootDir := t.TempDir()
	rootPath := filepath.Join(rootDir, "repo")
	worktreePath := filepath.Join(rootDir, "repo--feat-live-dirty")

	runTUITestGit(t, "", "init", rootPath)
	runTUITestGit(t, rootPath, "config", "user.name", "Little Control Room Tests")
	runTUITestGit(t, rootPath, "config", "user.email", "tests@example.com")
	if err := os.WriteFile(filepath.Join(rootPath, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write README.md: %v", err)
	}
	runTUITestGit(t, rootPath, "add", "README.md")
	runTUITestGit(t, rootPath, "commit", "-m", "initial commit")
	runTUITestGit(t, rootPath, "worktree", "add", "-b", "feat/live-dirty", worktreePath)
	if err := os.WriteFile(filepath.Join(rootPath, "DIRTY.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatalf("write DIRTY.txt: %v", err)
	}

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	now := time.Now().UTC()
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:             rootPath,
		Name:             "repo",
		PresentOnDisk:    true,
		InScope:          true,
		WorktreeRootPath: rootPath,
		WorktreeKind:     model.WorktreeKindMain,
		RepoBranch:       "master",
		UpdatedAt:        now,
	}); err != nil {
		t.Fatalf("seed root project state: %v", err)
	}
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:                 worktreePath,
		Name:                 "repo--feat-live-dirty",
		PresentOnDisk:        true,
		InScope:              true,
		WorktreeRootPath:     rootPath,
		WorktreeKind:         model.WorktreeKindLinked,
		WorktreeParentBranch: "master",
		RepoBranch:           "feat/live-dirty",
		UpdatedAt:            now,
	}); err != nil {
		t.Fatalf("seed linked worktree state: %v", err)
	}

	svc := service.New(config.Default(), st, events.NewBus(), nil)
	m := Model{
		ctx: ctx,
		svc: svc,
		allProjects: []model.ProjectSummary{
			{
				Name:             "repo",
				Path:             rootPath,
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindMain,
				RepoBranch:       "master",
			},
			{
				Name:                 "repo--feat-live-dirty",
				Path:                 worktreePath,
				PresentOnDisk:        true,
				WorktreeRootPath:     rootPath,
				WorktreeKind:         model.WorktreeKindLinked,
				WorktreeParentBranch: "master",
				RepoBranch:           "feat/live-dirty",
			},
		},
		visibility: visibilityAllFolders,
		sortMode:   sortByAttention,
		width:      120,
		height:     28,
	}
	m.rebuildProjectList(worktreePath)

	cmd := m.openWorktreeMergeConfirmForSelection()
	if cmd == nil {
		t.Fatal("opening the merge confirmation should refresh live git status when the service is available")
	}
	if m.worktreeMergeConfirm == nil {
		t.Fatal("merge confirmation should open before the async refresh completes")
	}
	if !m.worktreeMergeConfirm.Busy {
		t.Fatal("merge confirmation should stay busy while live git status is loading")
	}
	if got := m.status; got != "Checking live worktree status..." {
		t.Fatalf("status = %q, want live-status refresh message", got)
	}

	m = drainCmdMsgs(m, cmd)

	if m.worktreeMergeConfirm == nil {
		t.Fatal("merge confirmation should stay open after the live status refresh")
	}
	if m.worktreeMergeConfirm.Busy {
		t.Fatal("merge confirmation should stop waiting once both worktree statuses refresh")
	}
	if worktreeMergeConfirmReady(m.worktreeMergeConfirm) {
		t.Fatalf("merge confirmation should block once the live root checkout refresh reports dirtiness: %#v", m.worktreeMergeConfirm)
	}
	if !strings.Contains(worktreeMergeConfirmBlockReason(m.worktreeMergeConfirm), "The root checkout is dirty") {
		t.Fatalf("blocked reason = %q, want refreshed root-dirty warning", worktreeMergeConfirmBlockReason(m.worktreeMergeConfirm))
	}
	if got := m.status; got != "The root checkout is dirty. Commit or discard changes before merging back." {
		t.Fatalf("status = %q, want refreshed root-dirty status", got)
	}
	rootSummary, ok := m.projectSummaryByPath(rootPath)
	if !ok {
		t.Fatalf("root project summary for %q missing after refresh", rootPath)
	}
	if !rootSummary.RepoDirty {
		t.Fatalf("root summary should refresh to dirty after the live status check: %#v", rootSummary)
	}

	rendered := ansi.Strip(m.renderWorktreeMergeConfirmOverlay("", 100, 24))
	if !strings.Contains(rendered, "Merge blocked") || !strings.Contains(rendered, "The root checkout is dirty") {
		t.Fatalf("rendered merge dialog should show the refreshed root-dirty warning, got %q", rendered)
	}
}

func TestBusyWorktreeMergeRefreshEscCancelsDialog(t *testing.T) {
	t.Parallel()

	m := Model{
		worktreeMergeConfirm: &worktreeMergeConfirmState{
			ProjectPath:    "/tmp/repo--feat-live-dirty",
			RootPath:       "/tmp/repo",
			BranchName:     "feat/live-dirty",
			TargetBranch:   "master",
			PendingRefresh: worktreeMergeConfirmPendingRefreshSet("/tmp/repo--feat-live-dirty", "/tmp/repo"),
			Busy:           true,
			BusyMessage:    "Checking live git status for this worktree and its root checkout.",
		},
	}

	updated, cmd := m.updateWorktreeMergeConfirmMode(tea.KeyMsg{Type: tea.KeyEsc})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("esc during merge readiness refresh should not schedule work")
	}
	if got.worktreeMergeConfirm != nil {
		t.Fatalf("esc during merge readiness refresh should close the dialog")
	}
	if got.status != "Worktree merge-back canceled" {
		t.Fatalf("status = %q, want cancel status", got.status)
	}
}

func TestRenderBusyWorktreeMergeRefreshShowsEscHint(t *testing.T) {
	t.Parallel()

	m := Model{
		worktreeMergeConfirm: &worktreeMergeConfirmState{
			ProjectPath:    "/tmp/repo--feat-live-dirty",
			RootPath:       "/tmp/repo",
			BranchName:     "feat/live-dirty",
			TargetBranch:   "master",
			PendingRefresh: worktreeMergeConfirmPendingRefreshSet("/tmp/repo--feat-live-dirty", "/tmp/repo"),
			Busy:           true,
			BusyMessage:    "Checking live git status for this worktree and its root checkout.",
		},
	}

	rendered := ansi.Strip(m.renderWorktreeMergeConfirmOverlay("", 100, 24))
	if !strings.Contains(rendered, "Esc") || !strings.Contains(rendered, "close") {
		t.Fatalf("rendered busy merge dialog should advertise esc close while refreshing, got %q", rendered)
	}
}

func TestRenderWorktreeMergeConfirmClampsLongErrorMessage(t *testing.T) {
	t.Parallel()

	longLine := "This is a deliberately long merge error line that should wrap inside the dialog instead of falling off the bottom of the screen."
	m := Model{
		worktreeMergeConfirm: &worktreeMergeConfirmState{
			ProjectPath:  "/tmp/repo--feat-live-dirty",
			RootPath:     "/tmp/repo",
			BranchName:   "feat/live-dirty",
			TargetBranch: "master",
			Selected:     worktreeMergeConfirmKeepIndex(&worktreeMergeConfirmState{}),
			ErrorMessage: strings.TrimSpace(strings.Repeat(longLine+"\n", 16)),
		},
	}
	m.worktreeMergeConfirm.Selected = worktreeMergeConfirmKeepIndex(m.worktreeMergeConfirm)

	rendered := ansi.Strip(m.renderWorktreeMergeConfirmOverlay("", 72, 18))
	if !strings.Contains(rendered, "... more error details in /errors.") {
		t.Fatalf("long merge error should show an overflow hint instead of clipping silently, got %q", rendered)
	}
	if !strings.Contains(rendered, "Keep") {
		t.Fatalf("long merge error should keep the dialog actions visible, got %q", rendered)
	}
}

func TestOpenWorktreeMergeConfirmAutoClosesCompletedSession(t *testing.T) {
	rootPath := "/tmp/repo"
	childPath := "/tmp/repo--feat-parallel-lane"
	seenAt := time.Date(2026, 4, 4, 10, 0, 0, 0, time.UTC)
	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider: req.Provider.Normalized(),
				Started:  true,
				ThreadID: "thread-live",
				Status:   "Completed in 12s",
			},
		}, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: childPath,
		Provider:    codexapp.ProviderCodex,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	m := Model{
		codexManager:        manager,
		codexVisibleProject: childPath,
		nowFn:               func() time.Time { return seenAt },
		allProjects: []model.ProjectSummary{
			{
				Name:             "repo",
				Path:             rootPath,
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindMain,
				RepoBranch:       "master",
			},
			{
				Name:                            "repo--feat-parallel-lane",
				Path:                            childPath,
				PresentOnDisk:                   true,
				WorktreeRootPath:                rootPath,
				WorktreeKind:                    model.WorktreeKindLinked,
				WorktreeParentBranch:            "master",
				RepoBranch:                      "feat/parallel-lane",
				LatestSessionClassification:     model.ClassificationCompleted,
				LatestSessionClassificationType: model.SessionCategoryCompleted,
				LatestSessionFormat:             "modern",
				LatestSessionLastEventAt:        seenAt.Add(-2 * time.Minute),
				LatestTurnStateKnown:            true,
				LatestTurnCompleted:             true,
			},
		},
		visibility: visibilityAllFolders,
		sortMode:   sortByAttention,
	}
	m.rebuildProjectList(childPath)

	cmd := m.openWorktreeMergeConfirmForSelection()
	if cmd != nil {
		t.Fatalf("auto-closing a completed session should not require background work without a service")
	}
	if m.attentionDialog != nil {
		t.Fatalf("auto-close path should not show the attention dialog")
	}
	if m.worktreeMergeConfirm == nil {
		t.Fatalf("merge confirmation should open after auto-closing the completed session")
	}
	if m.status != "Confirm worktree merge-back" {
		t.Fatalf("status = %q, want merge confirmation", m.status)
	}
	if m.codexVisibleProject != "" {
		t.Fatalf("visible project should be cleared after auto-close, got %q", m.codexVisibleProject)
	}
	if _, ok := manager.Session(childPath); ok {
		t.Fatalf("completed session should be closed before opening merge confirm")
	}
	if !m.allProjects[1].LastSessionSeenAt.Equal(seenAt) {
		t.Fatalf("allProjects seen_at = %v, want %v", m.allProjects[1].LastSessionSeenAt, seenAt)
	}
	if !m.projects[1].LastSessionSeenAt.Equal(seenAt) {
		t.Fatalf("projects seen_at = %v, want %v", m.projects[1].LastSessionSeenAt, seenAt)
	}
}

func TestOpenWorktreeMergeConfirmAutoClosesSettledIdleSession(t *testing.T) {
	rootPath := "/tmp/repo"
	childPath := "/tmp/repo--feat-parallel-lane"
	seenAt := time.Date(2026, 4, 4, 11, 0, 0, 0, time.UTC)
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider: req.Provider.Normalized(),
				Started:  true,
				ThreadID: "thread-live",
				Phase:    codexapp.SessionPhaseIdle,
				Status:   "Recovered idle after status check",
			},
		}, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: childPath,
		Provider:    codexapp.ProviderCodex,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	m := Model{
		codexManager:        manager,
		codexVisibleProject: childPath,
		nowFn:               func() time.Time { return seenAt },
		allProjects: []model.ProjectSummary{
			{
				Name:             "repo",
				Path:             rootPath,
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindMain,
				RepoBranch:       "master",
			},
			{
				Name:                            "repo--feat-parallel-lane",
				Path:                            childPath,
				PresentOnDisk:                   true,
				WorktreeRootPath:                rootPath,
				WorktreeKind:                    model.WorktreeKindLinked,
				WorktreeParentBranch:            "master",
				RepoBranch:                      "feat/parallel-lane",
				LatestSessionClassification:     model.ClassificationCompleted,
				LatestSessionClassificationType: model.SessionCategoryCompleted,
				LatestSessionFormat:             "modern",
				LatestSessionLastEventAt:        seenAt.Add(-2 * time.Minute),
				LatestTurnStateKnown:            true,
				LatestTurnCompleted:             true,
			},
		},
		visibility: visibilityAllFolders,
		sortMode:   sortByAttention,
	}
	m.rebuildProjectList(childPath)

	cmd := m.openWorktreeMergeConfirmForSelection()
	if cmd != nil {
		t.Fatalf("auto-closing a settled idle session should not require background work without a service")
	}
	if m.attentionDialog != nil {
		t.Fatalf("settled idle auto-close path should not show the attention dialog")
	}
	if m.worktreeMergeConfirm == nil {
		t.Fatalf("merge confirmation should open after auto-closing the settled idle session")
	}
	if m.status != "Confirm worktree merge-back" {
		t.Fatalf("status = %q, want merge confirmation", m.status)
	}
	if m.codexVisibleProject != "" {
		t.Fatalf("visible project should be cleared after auto-close, got %q", m.codexVisibleProject)
	}
	if _, ok := manager.Session(childPath); ok {
		t.Fatalf("settled idle session should be closed before opening merge confirm")
	}
	if !m.allProjects[1].LastSessionSeenAt.Equal(seenAt) {
		t.Fatalf("allProjects seen_at = %v, want %v", m.allProjects[1].LastSessionSeenAt, seenAt)
	}
	if !m.projects[1].LastSessionSeenAt.Equal(seenAt) {
		t.Fatalf("projects seen_at = %v, want %v", m.projects[1].LastSessionSeenAt, seenAt)
	}
}

func TestOpenWorktreeMergeConfirmIncludesActiveRuntimeShutdown(t *testing.T) {
	rootPath := "/tmp/repo"
	childPath := "/tmp/repo--feat-runtime"
	m := Model{
		runtimeSnapshots: map[string]projectrun.Snapshot{
			childPath: {ProjectPath: childPath, Running: true},
		},
		allProjects: []model.ProjectSummary{
			{
				Name:             "repo",
				Path:             rootPath,
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindMain,
				RepoBranch:       "master",
			},
			{
				Name:                 "repo--feat-runtime",
				Path:                 childPath,
				PresentOnDisk:        true,
				WorktreeRootPath:     rootPath,
				WorktreeKind:         model.WorktreeKindLinked,
				WorktreeParentBranch: "master",
				WorktreeOriginTodoID: 42,
				RepoBranch:           "feat/runtime",
			},
		},
		visibility: visibilityAllFolders,
		sortMode:   sortByAttention,
	}
	m.rebuildProjectList(childPath)

	cmd := m.openWorktreeMergeConfirmForSelection()
	if cmd != nil {
		t.Fatalf("merge confirmation with a running runtime should not need background work")
	}
	if m.worktreeMergeConfirm == nil {
		t.Fatalf("merge confirmation should open even when a runtime is active")
	}
	if !m.worktreeMergeConfirm.RuntimeRunning || !m.worktreeMergeConfirm.StopRuntime {
		t.Fatalf("merge confirmation should default to stopping the active runtime, got %#v", m.worktreeMergeConfirm)
	}
	if !m.worktreeMergeConfirm.MarkTodoDone || !m.worktreeMergeConfirm.RemoveNow {
		t.Fatalf("merge confirmation should default to follow-up cleanup actions, got %#v", m.worktreeMergeConfirm)
	}
	if !worktreeMergeConfirmReady(m.worktreeMergeConfirm) {
		t.Fatalf("merge confirmation should stay runnable when runtime shutdown is selected")
	}
	if m.status != "Confirm worktree merge-back" {
		t.Fatalf("status = %q, want merge confirmation", m.status)
	}
}

func TestWorktreeMergePlanStopsRuntimeBeforeRunningGitActions(t *testing.T) {
	projectPath := t.TempDir()
	runtimeManager := projectrun.NewManager()
	defer runtimeManager.CloseAll()

	snapshot, err := runtimeManager.Start(projectrun.StartRequest{
		ProjectPath: projectPath,
		Command:     "sleep 30",
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if !snapshot.Running {
		t.Fatalf("runtime should be running after start, got %+v", snapshot)
	}

	m := Model{
		runtimeManager: runtimeManager,
		allProjects: []model.ProjectSummary{{
			Name:                 "repo--feat-runtime",
			Path:                 projectPath,
			PresentOnDisk:        true,
			WorktreeRootPath:     "/tmp/repo",
			WorktreeKind:         model.WorktreeKindLinked,
			WorktreeParentBranch: "master",
			RepoBranch:           "feat/runtime",
		}},
		worktreeMergeConfirm: &worktreeMergeConfirmState{
			ProjectPath:    projectPath,
			RootPath:       "/tmp/repo",
			BranchName:     "feat/runtime",
			TargetBranch:   "master",
			RuntimeRunning: true,
			StopRuntime:    true,
			RemoveNow:      true,
		},
	}
	m.worktreeMergeConfirm.Selected = worktreeMergeConfirmApplyIndex(m.worktreeMergeConfirm)

	updated, cmd := m.updateWorktreeMergeConfirmMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("applying the merge plan should queue background work")
	}
	if got.worktreeMergeConfirm != nil {
		t.Fatalf("merge plan should dismiss the dialog while it runs")
	}
	if got.pendingGitSummary(projectPath) != worktreeMergePendingSummary {
		t.Fatalf("pending git summary = %q, want merge summary", got.pendingGitSummary(projectPath))
	}

	msg := cmd()
	action, ok := msg.(worktreeActionMsg)
	if !ok {
		t.Fatalf("merge plan command returned %T, want worktreeActionMsg", msg)
	}
	if action.err == nil || !strings.Contains(action.err.Error(), "service unavailable") {
		t.Fatalf("merge plan should reach the service boundary after stopping the runtime, got %#v", action)
	}
	stopped, err := runtimeManager.Snapshot(projectPath)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if stopped.Running {
		t.Fatalf("runtime should be stopped before the merge action returns, got %+v", stopped)
	}
}

func TestBlockedWorktreeMergeEnterOnApplyKeepsDialogOpen(t *testing.T) {
	m := Model{
		allProjects: []model.ProjectSummary{{
			Name:                 "repo--feat-parallel-lane",
			Path:                 "/tmp/repo--feat-parallel-lane",
			PresentOnDisk:        true,
			WorktreeRootPath:     "/tmp/repo",
			WorktreeKind:         model.WorktreeKindLinked,
			WorktreeParentBranch: "master",
			RepoBranch:           "feat/parallel-lane",
			RepoDirty:            true,
		}},
		worktreeMergeConfirm: &worktreeMergeConfirmState{
			ProjectPath:       "/tmp/repo--feat-parallel-lane",
			RootPath:          "/tmp/repo",
			BranchName:        "feat/parallel-lane",
			TargetBranch:      "master",
			SourceDirty:       true,
			CommitBeforeMerge: false,
			RemoveNow:         true,
		},
	}
	m.worktreeMergeConfirm.Selected = worktreeMergeConfirmApplyIndex(m.worktreeMergeConfirm)

	updated, cmd := m.updateWorktreeMergeConfirmMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("blocked merge should not schedule a merge command")
	}
	if got.worktreeMergeConfirm == nil {
		t.Fatalf("blocked merge should keep the dialog open")
	}
	if got.status != "This worktree is dirty. Leave commit checked or clean it manually before merging back." {
		t.Fatalf("status = %q, want blocked merge reason", got.status)
	}
}

func TestBlockedWorktreeMergeEnterOnKeepCancels(t *testing.T) {
	m := Model{
		allProjects: []model.ProjectSummary{{
			Name:                 "repo--feat-parallel-lane",
			Path:                 "/tmp/repo--feat-parallel-lane",
			PresentOnDisk:        true,
			WorktreeRootPath:     "/tmp/repo",
			WorktreeKind:         model.WorktreeKindLinked,
			WorktreeParentBranch: "master",
			RepoBranch:           "feat/parallel-lane",
			RepoDirty:            true,
		}},
		worktreeMergeConfirm: &worktreeMergeConfirmState{
			ProjectPath:  "/tmp/repo--feat-parallel-lane",
			RootPath:     "/tmp/repo",
			BranchName:   "feat/parallel-lane",
			TargetBranch: "master",
			SourceDirty:  true,
			RemoveNow:    true,
		},
	}
	m.worktreeMergeConfirm.Selected = worktreeMergeConfirmKeepIndex(m.worktreeMergeConfirm)

	updated, cmd := m.updateWorktreeMergeConfirmMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("blocked keep should not schedule a merge command")
	}
	if got.worktreeMergeConfirm != nil {
		t.Fatalf("keep should close the blocked merge dialog")
	}
	if got.status != "Worktree merge-back canceled" {
		t.Fatalf("status = %q, want cancel status", got.status)
	}
}

func TestDirtyWorktreeMergeEnterOnApplyQueuesCommitAndMergePlan(t *testing.T) {
	projectPath := "/tmp/repo--feat-parallel-lane"
	m := Model{
		allProjects: []model.ProjectSummary{
			{
				Name:                 "repo--feat-parallel-lane",
				Path:                 projectPath,
				PresentOnDisk:        true,
				WorktreeRootPath:     "/tmp/repo",
				WorktreeKind:         model.WorktreeKindLinked,
				WorktreeParentBranch: "master",
				RepoBranch:           "feat/parallel-lane",
				RepoDirty:            true,
				LatestSessionSummary: "Updated the merge-back flow.",
			},
		},
		worktreeMergeConfirm: &worktreeMergeConfirmState{
			ProjectPath:       projectPath,
			RootPath:          "/tmp/repo",
			BranchName:        "feat/parallel-lane",
			TargetBranch:      "master",
			SourceDirty:       true,
			CommitBeforeMerge: true,
			HasLinkedTodo:     true,
			MarkTodoDone:      true,
			RemoveNow:         true,
		},
	}
	m.worktreeMergeConfirm.Selected = worktreeMergeConfirmApplyIndex(m.worktreeMergeConfirm)

	updated, cmd := m.updateWorktreeMergeConfirmMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("apply should queue the merge plan in the background")
	}
	if got.worktreeMergeConfirm != nil {
		t.Fatalf("commit & merge should dismiss the merge dialog immediately")
	}
	if got.commitPreview != nil {
		t.Fatalf("commit & merge should not open the commit preview")
	}
	if got.pendingGitSummary(projectPath) != worktreeCommitMergePendingSummary {
		t.Fatalf("pending git summary = %q, want commit-and-merge summary", got.pendingGitSummary(projectPath))
	}
	if got.status != worktreeCommitMergePendingSummary {
		t.Fatalf("status = %q, want async merge status", got.status)
	}
}

func TestWorktreeMergeEnterDismissesDialogWhileRunning(t *testing.T) {
	m := Model{
		allProjects: []model.ProjectSummary{{
			Name:                 "repo--feat-parallel-lane",
			Path:                 "/tmp/repo--feat-parallel-lane",
			PresentOnDisk:        true,
			WorktreeRootPath:     "/tmp/repo",
			WorktreeKind:         model.WorktreeKindLinked,
			WorktreeParentBranch: "master",
			RepoBranch:           "feat/parallel-lane",
		}},
		worktreeMergeConfirm: &worktreeMergeConfirmState{
			ProjectPath:  "/tmp/repo--feat-parallel-lane",
			RootPath:     "/tmp/repo",
			BranchName:   "feat/parallel-lane",
			TargetBranch: "master",
			RemoveNow:    true,
		},
		spinnerFrame: 2,
	}
	m.worktreeMergeConfirm.Selected = worktreeMergeConfirmApplyIndex(m.worktreeMergeConfirm)

	updated, cmd := m.updateWorktreeMergeConfirmMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("ready merge should queue a merge command")
	}
	if got.worktreeMergeConfirm != nil {
		t.Fatalf("merge dialog should close once the background work starts")
	}
	if got.pendingGitSummary("/tmp/repo--feat-parallel-lane") != worktreeMergePendingSummary {
		t.Fatalf("pending git summary = %q, want merge summary", got.pendingGitSummary("/tmp/repo--feat-parallel-lane"))
	}
	if got.status != worktreeMergePendingSummary {
		t.Fatalf("status = %q, want merge progress message", got.status)
	}
}

func TestDispatchCommandWorktreeMergeOpensConfirm(t *testing.T) {
	rootPath := "/tmp/repo"
	childPath := "/tmp/repo--feat-parallel-lane"
	m := Model{
		allProjects: []model.ProjectSummary{
			{
				Name:             "repo",
				Path:             rootPath,
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindMain,
				RepoBranch:       "master",
			},
			{
				Name:                 "repo--feat-parallel-lane",
				Path:                 childPath,
				PresentOnDisk:        true,
				WorktreeRootPath:     rootPath,
				WorktreeKind:         model.WorktreeKindLinked,
				WorktreeParentBranch: "master",
				RepoBranch:           "feat/parallel-lane",
			},
		},
		visibility: visibilityAllFolders,
		sortMode:   sortByAttention,
	}
	m.rebuildProjectList(childPath)

	updated, cmd := m.dispatchCommand(commands.Invocation{Kind: commands.KindWorktreeMerge})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("dispatchCommand(/wt merge) should open the confirmation dialog without scheduling work")
	}
	if got.worktreeMergeConfirm == nil {
		t.Fatalf("dispatchCommand(/wt merge) should open the merge confirmation dialog")
	}
}

func TestDispatchCommandWorktreeMergeBlockedWhenCommitIsInFlight(t *testing.T) {
	rootPath := "/tmp/repo"
	childPath := "/tmp/repo--feat-parallel-lane"
	m := Model{
		allProjects: []model.ProjectSummary{
			{
				Name:             "repo",
				Path:             rootPath,
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindMain,
				RepoBranch:       "master",
			},
			{
				Name:                 "repo--feat-parallel-lane",
				Path:                 childPath,
				PresentOnDisk:        true,
				WorktreeRootPath:     rootPath,
				WorktreeKind:         model.WorktreeKindLinked,
				WorktreeParentBranch: "master",
				RepoBranch:           "feat/parallel-lane",
			},
		},
		visibility: visibilityAllFolders,
		sortMode:   sortByAttention,
		pendingGitSummaries: map[string]string{
			childPath: "Committing...",
		},
	}
	m.rebuildProjectList(childPath)

	updated, cmd := m.dispatchCommand(commands.Invocation{Kind: commands.KindWorktreeMerge})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("commit-in-flight should block /wt merge without scheduling work")
	}
	if got.worktreeMergeConfirm != nil {
		t.Fatalf("merge confirmation should remain closed while a commit is running")
	}
	if got.status != "A commit is still in progress. Finish it before merging this worktree back." {
		t.Fatalf("status = %q, want commit in-flight gate message", got.status)
	}
}

func TestDispatchCommandWorktreeRemoveOpensConfirm(t *testing.T) {
	rootPath := "/tmp/repo"
	childPath := "/tmp/repo--feat-parallel-lane"
	m := Model{
		allProjects: []model.ProjectSummary{
			{
				Name:             "repo",
				Path:             rootPath,
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindMain,
			},
			{
				Name:             "repo--feat-parallel-lane",
				Path:             childPath,
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindLinked,
				RepoBranch:       "feat/parallel-lane",
			},
		},
		visibility: visibilityAllFolders,
		sortMode:   sortByAttention,
	}
	m.rebuildProjectList(childPath)

	updated, cmd := m.dispatchCommand(commands.Invocation{Kind: commands.KindWorktreeRemove})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("dispatchCommand(/wt remove) should open the confirmation dialog without scheduling work")
	}
	if got.worktreeRemoveConfirm == nil {
		t.Fatalf("dispatchCommand(/wt remove) should open the remove confirmation dialog")
	}
}

func TestLinkedWorktreeRemoveHotkeyStillOpensConfirm(t *testing.T) {
	rootPath := "/tmp/repo"
	childPath := "/tmp/repo--feat-parallel-lane"
	m := Model{
		allProjects: []model.ProjectSummary{
			{
				Name:             "repo",
				Path:             rootPath,
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindMain,
			},
			{
				Name:             "repo--feat-parallel-lane",
				Path:             childPath,
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindLinked,
				RepoBranch:       "feat/parallel-lane",
			},
		},
		visibility: visibilityAllFolders,
		sortMode:   sortByAttention,
	}
	m.rebuildProjectList(childPath)

	updated, cmd := m.updateNormalMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("x on linked worktree should open the confirmation dialog without scheduling work")
	}
	if got.worktreeRemoveConfirm == nil {
		t.Fatalf("x on linked worktree should open the remove confirmation dialog")
	}
}

func TestDispatchCommandRemoveOpensWorktreeRemoveConfirm(t *testing.T) {
	rootPath := "/tmp/repo"
	childPath := "/tmp/repo--feat-parallel-lane"
	m := Model{
		allProjects: []model.ProjectSummary{
			{
				Name:             "repo",
				Path:             rootPath,
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindMain,
			},
			{
				Name:             "repo--feat-parallel-lane",
				Path:             childPath,
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindLinked,
				RepoBranch:       "feat/parallel-lane",
			},
		},
		visibility: visibilityAllFolders,
		sortMode:   sortByAttention,
	}
	m.rebuildProjectList(childPath)

	updated, cmd := m.dispatchCommand(commands.Invocation{Kind: commands.KindRemove, Canonical: "/remove"})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("dispatchCommand(/remove) should open the confirmation dialog without scheduling work")
	}
	if got.worktreeRemoveConfirm == nil {
		t.Fatalf("dispatchCommand(/remove) should open the remove confirmation dialog for linked worktrees")
	}
}

func TestOpenWorktreeMergeConfirmWithLiveSessionShowsAttentionDialog(t *testing.T) {
	rootPath := "/tmp/repo"
	childPath := "/tmp/repo--feat-parallel-lane"
	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider: req.Provider.Normalized(),
				Started:  true,
				ThreadID: "thread-live",
				Status:   req.Provider.Label() + " session ready",
			},
		}, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: childPath,
		Provider:    codexapp.ProviderClaudeCode,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	m := Model{
		codexManager: manager,
		allProjects: []model.ProjectSummary{
			{
				Name:             "repo",
				Path:             rootPath,
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindMain,
				RepoBranch:       "master",
			},
			{
				Name:                 "repo--feat-parallel-lane",
				Path:                 childPath,
				PresentOnDisk:        true,
				WorktreeRootPath:     rootPath,
				WorktreeKind:         model.WorktreeKindLinked,
				WorktreeParentBranch: "master",
				RepoBranch:           "feat/parallel-lane",
			},
		},
		visibility: visibilityAllFolders,
		sortMode:   sortByAttention,
	}
	m.rebuildProjectList(childPath)

	cmd := m.openWorktreeMergeConfirmForSelection()
	if cmd != nil {
		t.Fatalf("blocked merge should not schedule a command")
	}
	if m.worktreeMergeConfirm != nil {
		t.Fatalf("merge confirmation dialog should stay closed when the session warning modal is shown")
	}
	if m.attentionDialog == nil {
		t.Fatalf("blocked merge should show the attention dialog")
	}
	if m.attentionDialog.Title != "Merge blocked" {
		t.Fatalf("attention dialog title = %q, want merge blocked", m.attentionDialog.Title)
	}
	if m.attentionDialog.PrimaryLabel != "Open Claude Code" {
		t.Fatalf("attention dialog primary label = %q, want open action", m.attentionDialog.PrimaryLabel)
	}
	if m.status != "Close the embedded agent session before merging this worktree back." {
		t.Fatalf("status = %q, want merge block warning", m.status)
	}
}

func TestOpenWorktreeRemoveConfirmWithLiveSessionShowsAttentionDialog(t *testing.T) {
	rootPath := "/tmp/repo"
	childPath := "/tmp/repo--feat-parallel-lane"
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider: req.Provider.Normalized(),
				Started:  true,
				ThreadID: "thread-live",
				Status:   req.Provider.Label() + " session ready",
			},
		}, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: childPath,
		Provider:    codexapp.ProviderClaudeCode,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	m := Model{
		codexManager: manager,
		allProjects: []model.ProjectSummary{
			{
				Name:             "repo",
				Path:             rootPath,
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindMain,
			},
			{
				Name:             "repo--feat-parallel-lane",
				Path:             childPath,
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindLinked,
				RepoBranch:       "feat/parallel-lane",
			},
		},
		visibility: visibilityAllFolders,
		sortMode:   sortByAttention,
	}
	m.rebuildProjectList(childPath)

	cmd := m.openWorktreeRemoveConfirmForSelection()
	if cmd != nil {
		t.Fatalf("blocked removal should not schedule a command")
	}
	if m.worktreeRemoveConfirm != nil {
		t.Fatalf("remove confirmation dialog should stay closed when the session warning modal is shown")
	}
	if m.attentionDialog == nil {
		t.Fatalf("blocked removal should show the attention dialog")
	}
	if m.attentionDialog.Title != "Remove blocked" {
		t.Fatalf("attention dialog title = %q, want remove blocked", m.attentionDialog.Title)
	}
	if m.attentionDialog.PrimaryLabel != "Open Claude Code" {
		t.Fatalf("attention dialog primary label = %q, want open action", m.attentionDialog.PrimaryLabel)
	}
	if m.status != "Close the embedded agent session before removing this worktree." {
		t.Fatalf("status = %q, want removal block warning", m.status)
	}
}

func TestRenderWorktreeRemoveConfirmShowsMergeSafetyCopy(t *testing.T) {
	m := Model{
		worktreeRemoveConfirm: &worktreeRemoveConfirmState{
			ProjectPath:  "/tmp/repo--feat-parallel-lane",
			RootPath:     "/tmp/repo",
			ProjectName:  "repo--feat-parallel-lane",
			BranchName:   "feat/parallel-lane",
			TargetBranch: "master",
			MergeStatus:  model.WorktreeMergeStatusNotMerged,
			Selected:     worktreeRemoveConfirmKeepIndex(nil),
		},
	}

	rendered := ansi.Strip(m.renderWorktreeRemoveConfirmOverlay("body", 90, 24))
	if !strings.Contains(rendered, "Pending merge") {
		t.Fatalf("remove confirm should call out unmerged worktrees, got %q", rendered)
	}
	if !strings.Contains(rendered, "still has commits to merge into master") {
		t.Fatalf("remove confirm should explain the merge target, got %q", rendered)
	}
	if !strings.Contains(rendered, "branch ref stays in the repo") {
		t.Fatalf("remove confirm should explain that only the checkout is removed, got %q", rendered)
	}
}

func TestWorktreeRemoveEnterDismissesDialogWhileRunning(t *testing.T) {
	m := Model{
		worktreeRemoveConfirm: &worktreeRemoveConfirmState{
			ProjectPath: "/tmp/repo--feat-parallel-lane",
			RootPath:    "/tmp/repo",
			BranchName:  "feat/parallel-lane",
			Selected:    worktreeRemoveConfirmRemoveIndex(nil),
		},
		spinnerFrame: 2,
	}

	updated, cmd := m.updateWorktreeRemoveConfirmMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("remove confirm should queue a removal command")
	}
	if got.worktreeRemoveConfirm != nil {
		t.Fatalf("remove dialog should close once the background work starts")
	}
	if got.pendingGitSummary("/tmp/repo--feat-parallel-lane") != worktreeRemovePendingSummary {
		t.Fatalf("pending git summary = %q, want remove summary", got.pendingGitSummary("/tmp/repo--feat-parallel-lane"))
	}
	if got.status != worktreeRemovePendingSummary {
		t.Fatalf("status = %q, want removal progress message", got.status)
	}
}

func TestDispatchCommandWorktreePruneQueuesCommand(t *testing.T) {
	rootPath := "/tmp/repo"
	m := Model{
		allProjects: []model.ProjectSummary{
			{
				Name:             "repo",
				Path:             rootPath,
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindMain,
			},
			{
				Name:             "repo--feat-parallel-lane",
				Path:             "/tmp/repo--feat-parallel-lane",
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindLinked,
				RepoBranch:       "feat/parallel-lane",
			},
		},
		visibility: visibilityAllFolders,
		sortMode:   sortByAttention,
	}
	m.rebuildProjectList(rootPath)

	updated, cmd := m.dispatchCommand(commands.Invocation{Kind: commands.KindWorktreePrune})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("dispatchCommand(/wt prune) should queue a prune command")
	}
	if got.status != "Pruning stale git worktrees..." {
		t.Fatalf("status = %q, want pruning status", got.status)
	}
}

func TestWorktreeActionMsgMergeCompletionDoesNotOpenFollowUpPrompt(t *testing.T) {
	rootPath := "/tmp/repo"
	childPath := "/tmp/repo--feat-parallel-lane"
	status := "Merged feat/parallel-lane into master"

	updated, cmd := Model{}.Update(worktreeActionMsg{
		projectPath: childPath,
		selectPath:  rootPath,
		status:      status,
	})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("merge completion should queue a refresh command batch")
	}
	if got.worktreePostMerge != nil {
		t.Fatalf("merge completion should stay in the single merge dialog flow")
	}
	if got.preferredSelectPath != rootPath {
		t.Fatalf("preferred select path = %q, want %q", got.preferredSelectPath, rootPath)
	}
	if got.status != status {
		t.Fatalf("status = %q, want %q", got.status, status)
	}
}

func TestWorktreeActionMsgRemoveHidesWorktreeImmediately(t *testing.T) {
	rootPath := "/tmp/repo"
	childPath := "/tmp/repo--feat-parallel-lane"

	root := model.ProjectSummary{
		Name:             "repo",
		Path:             rootPath,
		Status:           model.StatusIdle,
		PresentOnDisk:    true,
		WorktreeRootPath: rootPath,
		WorktreeKind:     model.WorktreeKindMain,
		RepoBranch:       "master",
	}
	child := model.ProjectSummary{
		Name:             "repo--feat-parallel-lane",
		Path:             childPath,
		Status:           model.StatusIdle,
		PresentOnDisk:    true,
		WorktreeRootPath: rootPath,
		WorktreeKind:     model.WorktreeKindLinked,
		RepoBranch:       "feat/parallel-lane",
	}
	m := Model{
		allProjects: []model.ProjectSummary{root, child},
		detail: model.ProjectDetail{
			Summary: child,
		},
		sortMode:   sortByAttention,
		visibility: visibilityAllFolders,
	}
	m.rebuildProjectList(childPath)

	updated, cmd := m.Update(worktreeActionMsg{
		projectPath:        childPath,
		removedProjectPath: childPath,
		selectPath:         rootPath,
		status:             "Worktree removed",
	})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("worktree removal completion should queue a refresh command batch")
	}
	if got.preferredSelectPath != rootPath {
		t.Fatalf("preferred select path = %q, want %q", got.preferredSelectPath, rootPath)
	}
	if len(got.projects) != 1 || got.projects[0].Path != rootPath {
		t.Fatalf("visible projects after local removal = %#v, want only the repo root", got.projects)
	}
	if _, ok := got.projectSummaryByPath(childPath); ok {
		t.Fatalf("removed worktree %q should be hidden from local project state", childPath)
	}
	if got.detail.Summary.Path != rootPath {
		t.Fatalf("detail path = %q, want immediate fallback to root %q", got.detail.Summary.Path, rootPath)
	}
}

func TestWorktreeActionMsgErrorLogsAsyncMergeFailure(t *testing.T) {
	errText := "merge conflict while merging feat/parallel-lane into master at /tmp/repo\nResolve or abort the merge in the root checkout before retrying.\nConflicted files:\n- README.md\n- STATUS.md"
	m := Model{
		pendingGitSummaries: map[string]string{
			"/tmp/repo--feat-parallel-lane": worktreeMergePendingSummary,
		},
	}

	updated, cmd := m.Update(worktreeActionMsg{
		projectPath:            "/tmp/repo--feat-parallel-lane",
		clearPendingGitSummary: true,
		err:                    errors.New(errText),
	})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("merge error should not queue follow-up work")
	}
	if got.worktreeMergeConfirm != nil {
		t.Fatalf("merge error should not reopen the merge dialog")
	}
	if got.pendingGitSummary("/tmp/repo--feat-parallel-lane") != "" {
		t.Fatalf("pending git summary = %q, want cleared", got.pendingGitSummary("/tmp/repo--feat-parallel-lane"))
	}
	if got.status != "Worktree action failed (use /errors)" {
		t.Fatalf("status = %q, want error hint", got.status)
	}
	if len(got.errorLogEntries) != 1 {
		t.Fatalf("error log count = %d, want 1", len(got.errorLogEntries))
	}
	if got.errorLogEntries[0].Message != errText {
		t.Fatalf("error log message = %q, want %q", got.errorLogEntries[0].Message, errText)
	}
}

func TestWorktreePostMergeEnterRemoveQueuesRemoval(t *testing.T) {
	rootPath := "/tmp/repo"
	childPath := "/tmp/repo--feat-parallel-lane"

	m := Model{
		worktreePostMerge: &worktreePostMergeState{
			ProjectPath:  childPath,
			RootPath:     rootPath,
			BranchName:   "feat/parallel-lane",
			TargetBranch: "master",
			Status:       "Merged feat/parallel-lane into master",
			RemoveNow:    true,
			Selected:     worktreePostMergeFocusRemove,
		},
	}

	updated, cmd := m.updateWorktreePostMergeMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("removing the merged worktree should queue a removal command")
	}
	if got.worktreePostMerge != nil {
		t.Fatalf("post-merge remove should dismiss the prompt immediately")
	}
	if got.pendingGitSummary(childPath) != worktreePostMergeRemoveSummary {
		t.Fatalf("pending git summary = %q, want merged-worktree remove summary", got.pendingGitSummary(childPath))
	}
	if got.status != worktreePostMergeRemoveSummary {
		t.Fatalf("status = %q, want removal progress", got.status)
	}
}

func TestWorktreePostMergeEnterRemoveDismissesDialogWhileRunning(t *testing.T) {
	rootPath := "/tmp/repo"
	childPath := "/tmp/repo--feat-parallel-lane"

	m := Model{
		worktreePostMerge: &worktreePostMergeState{
			ProjectPath:  childPath,
			RootPath:     rootPath,
			BranchName:   "feat/parallel-lane",
			TargetBranch: "master",
			Status:       "Merged feat/parallel-lane into master",
			RemoveNow:    true,
			Selected:     worktreePostMergeFocusRemove,
		},
		spinnerFrame: 2,
	}

	updated, cmd := m.updateWorktreePostMergeMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("post-merge remove should queue a removal command")
	}
	if got.worktreePostMerge != nil {
		t.Fatalf("post-merge dialog should close while removal runs")
	}
	if got.pendingGitSummary(childPath) != worktreePostMergeRemoveSummary {
		t.Fatalf("pending git summary = %q, want merged-worktree remove summary", got.pendingGitSummary(childPath))
	}
}

func TestWorktreePostMergeEnterDoneKeepsWorktreeQueuesTodoUpdate(t *testing.T) {
	rootPath := "/tmp/repo"
	childPath := "/tmp/repo--feat-parallel-lane"

	m := Model{
		worktreePostMerge: &worktreePostMergeState{
			ProjectPath:  childPath,
			RootPath:     rootPath,
			BranchName:   "feat/parallel-lane",
			TargetBranch: "master",
			TodoID:       7,
			TodoText:     "Finish the parallel lane work",
			TodoPath:     rootPath,
			MarkTodoDone: true,
			Status:       "Merged feat/parallel-lane into master",
			Selected:     worktreePostMergeFocusTodo,
		},
	}

	updated, cmd := m.updateWorktreePostMergeMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("marking the linked todo done should queue an update command")
	}
	if got.status != "Marking linked TODO done..." {
		t.Fatalf("status = %q, want todo progress", got.status)
	}
	if got.worktreePostMerge != nil {
		t.Fatalf("post-merge dialog should dismiss while updating the todo")
	}
}

func TestWorktreePostMergeSpaceTogglesSelectedOption(t *testing.T) {
	m := Model{
		worktreePostMerge: &worktreePostMergeState{
			ProjectPath:  "/tmp/repo--feat-parallel-lane",
			RootPath:     "/tmp/repo",
			BranchName:   "feat/parallel-lane",
			TargetBranch: "master",
			TodoID:       7,
			TodoText:     "Finish the parallel lane work",
			TodoPath:     "/tmp/repo",
			Status:       "Merged feat/parallel-lane into master",
			Selected:     worktreePostMergeFocusTodo,
		},
	}

	updated, cmd := m.updateWorktreePostMergeMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("toggling a post-merge option should not queue background work")
	}
	if got.worktreePostMerge == nil || !got.worktreePostMerge.MarkTodoDone {
		t.Fatalf("space should toggle the selected linked todo checkbox")
	}

	got.worktreePostMerge.Selected = worktreePostMergeFocusRemove
	updated, cmd = got.updateWorktreePostMergeMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	got = updated.(Model)
	if cmd != nil {
		t.Fatalf("toggling the remove option should not queue background work")
	}
	if got.worktreePostMerge == nil || !got.worktreePostMerge.RemoveNow {
		t.Fatalf("space should toggle the remove-worktree checkbox")
	}
}

func TestRenderWorktreePostMergeOverlayShowsSeparateCleanupChoices(t *testing.T) {
	rendered := ansi.Strip(Model{
		worktreePostMerge: &worktreePostMergeState{
			ProjectPath:  "/tmp/repo--feat-parallel-lane",
			RootPath:     "/tmp/repo",
			BranchName:   "feat/parallel-lane",
			TargetBranch: "master",
			TodoID:       7,
			TodoText:     "Finish the parallel lane work",
			TodoPath:     "/tmp/repo",
			Status:       "Merged feat/parallel-lane into master",
			Selected:     worktreePostMergeFocusTodo,
		},
	}.renderWorktreePostMergeOverlay("", 72, 24))

	for _, want := range []string{
		"Choose what to clean up now.",
		"The linked TODO and",
		"merged worktree are separate actions.",
		"Linked TODO",
		"Mark the originating TODO complete",
		"[ ] Finish the parallel lane work",
		"Worktree cleanup",
		"Remove this merged checkout now or keep it around",
		"for later. Removing it only deletes the checkout.",
		"[ ] Remove merged worktree now",
		"Enter  apply",
		"Esc",
		"later",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("post-merge overlay should render the full wrapped prompt copy, missing %q in %q", want, rendered)
		}
	}
}

func TestRenderWorktreePostMergeOverlayWithoutTodoShowsCleanupSection(t *testing.T) {
	rendered := ansi.Strip(Model{
		worktreePostMerge: &worktreePostMergeState{
			ProjectPath:  "/tmp/repo--feat-parallel-lane",
			RootPath:     "/tmp/repo",
			BranchName:   "feat/parallel-lane",
			TargetBranch: "master",
			Status:       "Merged feat/parallel-lane into master",
			Selected:     worktreePostMergeFocusRemove,
		},
	}.renderWorktreePostMergeOverlay("", 72, 24))

	for _, want := range []string{
		"Choose whether to remove this merged worktree now or",
		"keep it for later.",
		"Worktree cleanup",
		"Removing it only deletes the checkout.",
		"[ ] Remove merged worktree now",
		"Enter  apply",
		"Esc",
		"later",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("post-merge overlay without todo should show the cleanup section, missing %q in %q", want, rendered)
		}
	}
}

func TestWorktreeActionMsgErrorLogsAsyncPostMergeFailure(t *testing.T) {
	childPath := "/tmp/repo--feat-parallel-lane"
	errText := "linked TODO was marked done, but removing the worktree failed: remove git worktree"

	m := Model{
		pendingGitSummaries: map[string]string{
			childPath: worktreePostMergeRemoveSummary,
		},
	}

	updated, cmd := m.Update(worktreeActionMsg{
		projectPath:            childPath,
		clearPendingGitSummary: true,
		err:                    errors.New(errText),
	})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("post-merge error should not queue follow-up work")
	}
	if got.worktreePostMerge != nil {
		t.Fatalf("post-merge error should not reopen the prompt")
	}
	if got.pendingGitSummary(childPath) != "" {
		t.Fatalf("pending git summary = %q, want cleared", got.pendingGitSummary(childPath))
	}
	if got.status != "Worktree action failed (use /errors)" {
		t.Fatalf("status = %q, want error hint", got.status)
	}
	if len(got.errorLogEntries) != 1 {
		t.Fatalf("error log count = %d, want 1", len(got.errorLogEntries))
	}
	if got.errorLogEntries[0].Message != errText {
		t.Fatalf("error log message = %q, want %q", got.errorLogEntries[0].Message, errText)
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

func TestAIStatsShowsCostOnlyForOpenAIAPI(t *testing.T) {
	if aiStatsShowsCost(config.AIBackendCodex) {
		t.Fatalf("aiStatsShowsCost(codex) = true, want false")
	}
	if !aiStatsShowsCost(config.AIBackendOpenAIAPI) {
		t.Fatalf("aiStatsShowsCost(openai_api) = false, want true")
	}
}

func TestAIStatsCostValue(t *testing.T) {
	usage := model.LLMSessionUsage{
		Enabled: true,
		Model:   "gpt-5-mini",
		Totals: model.LLMUsage{
			InputTokens:  345,
			OutputTokens: 538,
		},
	}

	if got := ansi.Strip(aiStatsCostValue(usage)); got != "$0.0012" {
		t.Fatalf("aiStatsCostValue() = %q, want %q", got, "$0.0012")
	}
}

func TestAIStatsBillingValueMarksLocalProviderMode(t *testing.T) {
	if got := ansi.Strip(aiStatsBillingValue(config.AIBackendOpenCode)); got != "local provider mode" {
		t.Fatalf("aiStatsBillingValue(opencode) = %q, want %q", got, "local provider mode")
	}
}

func TestAIStatsBillingNoticeClarifiesLocalBackends(t *testing.T) {
	got := aiStatsBillingNotice(config.AIBackendClaude)
	if !strings.Contains(got, "local provider path") {
		t.Fatalf("local backend notice should mention local provider billing semantics, got %q", got)
	}
	if !strings.Contains(got, "estimated API-key spend") {
		t.Fatalf("local backend notice should explain how to see API-key cost, got %q", got)
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

func TestFooterUsageLabelUsesConfiguredLocalBackendBeforeSetupCheck(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.AIBackend = config.AIBackendCodex

	m := Model{settingsBaseline: &settings}
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

func TestRenderTopStatusLinePulsesActionRequiredWarning(t *testing.T) {
	prevProfile := lipgloss.ColorProfile()
	prevDarkBackground := lipgloss.HasDarkBackground()
	lipgloss.SetColorProfile(termenv.ANSI256)
	lipgloss.SetHasDarkBackground(true)
	t.Cleanup(func() {
		lipgloss.SetColorProfile(prevProfile)
		lipgloss.SetHasDarkBackground(prevDarkBackground)
	})

	m := Model{status: "Stop the runtime before merging this worktree back"}

	warnA := m.renderTopStatusLine(160)
	m.spinnerFrame = 1
	warnB := m.renderTopStatusLine(160)

	if ansi.Strip(warnA) != ansi.Strip(warnB) {
		t.Fatalf("warning pulse should preserve banner text, got %q vs %q", ansi.Strip(warnA), ansi.Strip(warnB))
	}
	if warnA == warnB {
		t.Fatalf("action-required warning should animate across spinner frames")
	}
}

func TestRenderTopStatusLinePulsesErrorsAsDanger(t *testing.T) {
	prevProfile := lipgloss.ColorProfile()
	prevDarkBackground := lipgloss.HasDarkBackground()
	lipgloss.SetColorProfile(termenv.ANSI256)
	lipgloss.SetHasDarkBackground(true)
	t.Cleanup(func() {
		lipgloss.SetColorProfile(prevProfile)
		lipgloss.SetHasDarkBackground(prevDarkBackground)
	})

	m := Model{
		status: "Scan failed",
		err:    errors.New("boom"),
	}

	errA := m.renderTopStatusLine(160)
	m.spinnerFrame = 1
	errB := m.renderTopStatusLine(160)

	if ansi.Strip(errA) != ansi.Strip(errB) {
		t.Fatalf("danger pulse should preserve banner text, got %q vs %q", ansi.Strip(errA), ansi.Strip(errB))
	}
	if errA == errB {
		t.Fatalf("error banner should animate across spinner frames")
	}
}

func TestRenderTopStatusLineKeepsRecoveryProgressNeutral(t *testing.T) {
	prevProfile := lipgloss.ColorProfile()
	prevDarkBackground := lipgloss.HasDarkBackground()
	lipgloss.SetColorProfile(termenv.ANSI256)
	lipgloss.SetHasDarkBackground(true)
	t.Cleanup(func() {
		lipgloss.SetColorProfile(prevProfile)
		lipgloss.SetHasDarkBackground(prevDarkBackground)
	})

	m := Model{status: "Scanning and retrying failed assessments..."}

	statusA := m.renderTopStatusLine(160)
	m.spinnerFrame = 1
	statusB := m.renderTopStatusLine(160)

	if ansi.Strip(statusA) != ansi.Strip(statusB) {
		t.Fatalf("recovery progress should preserve banner text, got %q vs %q", ansi.Strip(statusA), ansi.Strip(statusB))
	}
	if statusA != statusB {
		t.Fatalf("recovery progress should not animate like a warning or error")
	}
}

func TestRenderTopStatusLineKeepsClipboardConfirmationNeutral(t *testing.T) {
	prevProfile := lipgloss.ColorProfile()
	prevDarkBackground := lipgloss.HasDarkBackground()
	lipgloss.SetColorProfile(termenv.ANSI256)
	lipgloss.SetHasDarkBackground(true)
	t.Cleanup(func() {
		lipgloss.SetColorProfile(prevProfile)
		lipgloss.SetHasDarkBackground(prevDarkBackground)
	})

	m := Model{status: "Copied error details to clipboard"}

	statusA := m.renderTopStatusLine(160)
	m.spinnerFrame = 1
	statusB := m.renderTopStatusLine(160)

	if ansi.Strip(statusA) != ansi.Strip(statusB) {
		t.Fatalf("clipboard confirmation should preserve banner text, got %q vs %q", ansi.Strip(statusA), ansi.Strip(statusB))
	}
	if statusA != statusB {
		t.Fatalf("clipboard confirmation should not animate like a danger banner")
	}
}

func TestRenderTopStatusLineShowsNavigationHintsInsteadOfAICounts(t *testing.T) {
	m := Model{status: "Ready"}

	rendered := ansi.Strip(m.renderTopStatusLine(160))
	if !strings.Contains(rendered, "f filter") || !strings.Contains(rendered, "/ command") || !strings.Contains(rendered, "b boss") {
		t.Fatalf("top status line should surface navigation hints, got %q", rendered)
	}
	if strings.Contains(rendered, "Tab switch") {
		t.Fatalf("top status line should leave pane switching to the footer, got %q", rendered)
	}
	if strings.Contains(rendered, "OK=") || strings.Contains(rendered, "RUN=") || strings.Contains(rendered, "ERR=") {
		t.Fatalf("top status line should no longer include AI classification counters, got %q", rendered)
	}
}

func TestRenderTopStatusLineShowsCPUUsageAtRight(t *testing.T) {
	m := Model{
		status: "Ready",
		cpuSnapshot: procinspect.CPUSnapshot{
			TotalCPU:  132.6,
			ScannedAt: time.Date(2026, 4, 3, 11, 0, 0, 0, time.UTC),
			Processes: []procinspect.CPUProcess{{
				Process: procinspect.Process{PID: 42, CPU: 82.3, Command: "/opt/homebrew/bin/node server.js"},
			}},
		},
	}

	rendered := strings.TrimRight(ansi.Strip(m.renderTopStatusLine(80)), " ")
	if !strings.Contains(rendered, "Ready") {
		t.Fatalf("top status line missing left status: %q", rendered)
	}
	if !strings.HasSuffix(rendered, "CPU 133% node 82%") {
		t.Fatalf("top status line should pin CPU summary to the right, got %q", rendered)
	}
}

func TestCompactFooterBaseSplitsGlobalActionsFromTopStatus(t *testing.T) {
	rendered := ansi.Strip(compactFooterBase(160, focusProjects, 0, 0, false, "Session", nil))
	if strings.Contains(rendered, "f filter") || strings.Contains(rendered, "/ command") {
		t.Fatalf("normal footer should not repeat top global actions, got %q", rendered)
	}
	if !strings.Contains(rendered, "Tab switch") {
		t.Fatalf("normal footer should keep pane switching guidance, got %q", rendered)
	}
}

func TestRenderTopStatusLineShowsMergeConflictBadge(t *testing.T) {
	m := Model{
		status: "Ready",
		projects: []model.ProjectSummary{{
			Path:          "/tmp/demo",
			Name:          "demo",
			PresentOnDisk: true,
			RepoConflict:  true,
		}},
		selected: 0,
	}

	rendered := ansi.Strip(m.renderTopStatusLine(180))
	if !strings.Contains(rendered, "MERGE CONFLICT") {
		t.Fatalf("top status line missing merge conflict badge: %q", rendered)
	}
	if !strings.Contains(rendered, "selected repo has unmerged files") {
		t.Fatalf("top status line missing merge conflict summary: %q", rendered)
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

func TestRenderFooterShowsSeparateAssessmentAlertWhenClassificationErrorsExist(t *testing.T) {
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
			Selected: config.AIBackendCodex,
			Codex: aibackend.Status{
				Backend:       config.AIBackendCodex,
				Label:         "Codex",
				Installed:     true,
				Authenticated: true,
				Ready:         true,
			},
		},
		allProjects: []model.ProjectSummary{{
			Name:                        "demo",
			Path:                        "/tmp/demo",
			LatestSessionClassification: model.ClassificationFailed,
		}},
	}

	m.spinnerFrame = 0
	renderedA := m.renderFooter(160)
	m.spinnerFrame = 1
	renderedB := m.renderFooter(160)

	if ansi.Strip(renderedA) != ansi.Strip(renderedB) {
		t.Fatalf("assessment footer should keep the same visible text: %q vs %q", ansi.Strip(renderedA), ansi.Strip(renderedB))
	}
	if renderedA != renderedB {
		t.Fatalf("assessment footer should stay visually stable across spinner frames")
	}
	if renderedA == ansi.Strip(renderedA) {
		t.Fatalf("assessment footer should use ANSI styling: %q", renderedA)
	}
	if !strings.Contains(ansi.Strip(renderedA), "Codex ready") {
		t.Fatalf("footer should keep the backend status visible, got %q", ansi.Strip(renderedA))
	}
	if !strings.Contains(ansi.Strip(renderedA), "1 assessment error") {
		t.Fatalf("footer should surface assessment failures separately, got %q", ansi.Strip(renderedA))
	}
}

func TestRenderFooterHidesAssessmentAlertWhileErrorLogIsOpen(t *testing.T) {
	prevProfile := lipgloss.ColorProfile()
	prevDarkBackground := lipgloss.HasDarkBackground()
	lipgloss.SetColorProfile(termenv.ANSI256)
	lipgloss.SetHasDarkBackground(true)
	t.Cleanup(func() {
		lipgloss.SetColorProfile(prevProfile)
		lipgloss.SetHasDarkBackground(prevDarkBackground)
	})

	m := Model{
		errorLogVisible: true,
		setupChecked:    true,
		setupSnapshot: aibackend.Snapshot{
			Selected: config.AIBackendCodex,
			Codex: aibackend.Status{
				Backend:       config.AIBackendCodex,
				Label:         "Codex",
				Installed:     true,
				Authenticated: true,
				Ready:         true,
			},
		},
		allProjects: []model.ProjectSummary{{
			Name:                        "demo",
			Path:                        "/tmp/demo",
			LatestSessionClassification: model.ClassificationFailed,
		}},
	}

	rendered := ansi.Strip(m.renderFooter(160))
	if !strings.Contains(rendered, "Error log:") {
		t.Fatalf("footer should switch to error log guidance, got %q", rendered)
	}
	if strings.Contains(rendered, "assessment error") {
		t.Fatalf("footer should hide assessment alert while error log is open, got %q", rendered)
	}
}

func TestRenderFooterShowsBrowserAttentionAlert(t *testing.T) {
	m := Model{
		codexSnapshots: map[string]codexapp.Snapshot{
			"/tmp/demo": {
				Provider: codexapp.ProviderCodex,
				BrowserActivity: browserctl.SessionActivity{
					Policy:     settingsAutomaticPlaywrightPolicy,
					State:      browserctl.SessionActivityStateWaitingForUser,
					ServerName: "playwright",
					ToolName:   "browser_navigate",
				},
				ManagedBrowserSessionKey: "managed-demo",
			},
		},
	}

	rendered := ansi.Strip(m.renderFooter(120))
	if !strings.Contains(rendered, "1 browser wait") {
		t.Fatalf("footer should surface browser attention waits, got %q", rendered)
	}
}

func TestRenderFooterShowsProcessWarningSystemNotice(t *testing.T) {
	m := Model{
		projects:    []model.ProjectSummary{{Path: "/tmp/demo", Name: "demo"}},
		selected:    0,
		allProjects: []model.ProjectSummary{{Path: "/tmp/demo", Name: "demo"}},
		processReports: map[string]procinspect.ProjectReport{
			"/tmp/demo": {
				ProjectPath: "/tmp/demo",
				Findings: []procinspect.Finding{{
					Process: procinspect.Process{PID: 49995, CPU: 98.5},
					Reasons: []string{"orphaned under PID 1", "high CPU 98.5%"},
				}},
			},
		},
	}

	rendered := ansi.Strip(m.renderFooter(220))
	for _, want := range []string{"PIDs 1", "hot1"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("footer missing process warning %q: %q", want, rendered)
		}
	}
	if strings.Contains(rendered, "suspicious project process") || strings.Contains(rendered, "Processes:") {
		t.Fatalf("footer process warning should stay compact, got %q", rendered)
	}
}

func TestProcessWarningStatusIsCompact(t *testing.T) {
	status := processWarningStatus(processWarningStats{Total: 6, HighCPU: 2, PortListeners: 1})
	if status != "PIDs 6 hot2 port1; /cpu" {
		t.Fatalf("processWarningStatus() = %q, want compact CPU status", status)
	}
}

func TestGlobalProcessFindingsIncludeAllProjects(t *testing.T) {
	m := Model{
		allProjects: []model.ProjectSummary{
			{Path: "/tmp/selected", Name: "selected"},
			{Path: "/tmp/other", Name: "other"},
		},
		processReports: map[string]procinspect.ProjectReport{
			"/tmp/selected": {
				ProjectPath: "/tmp/selected",
				Findings: []procinspect.Finding{{
					Process:     procinspect.Process{PID: 1, CPU: 1.2},
					ProjectPath: "/tmp/selected",
					Reasons:     []string{"orphaned under PID 1"},
				}},
			},
			"/tmp/other": {
				ProjectPath: "/tmp/other",
				Findings: []procinspect.Finding{{
					Process:     procinspect.Process{PID: 2, CPU: 99.0},
					ProjectPath: "/tmp/other",
					Reasons:     []string{"high CPU 99.0%"},
				}},
			},
		},
	}

	findings, _ := m.globalProcessFindings()
	if len(findings) != 2 {
		t.Fatalf("global findings len = %d, want 2", len(findings))
	}
	if findings[0].ProjectPath != "/tmp/other" || findings[0].PID != 2 {
		t.Fatalf("first finding = project %q PID %d, want /tmp/other PID 2", findings[0].ProjectPath, findings[0].PID)
	}
}

func TestFooterSupplementSegmentsPrioritizeAssessmentBeforeUsage(t *testing.T) {
	segments := footerSupplementSegments("filter", "1 assessment error", "OpenCode 139 calls")
	if len(segments) != 3 {
		t.Fatalf("segment count = %d, want 3", len(segments))
	}
	if got := ansi.Strip(segments[0]); got != "filter" {
		t.Fatalf("segment 0 = %q, want filter", got)
	}
	if got := ansi.Strip(segments[1]); got != "1 assessment error" {
		t.Fatalf("segment 1 = %q, want assessment alert", got)
	}
	if got := ansi.Strip(segments[2]); got != "OpenCode 139 calls" {
		t.Fatalf("segment 2 = %q, want usage label", got)
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
	if !strings.Contains(lines[1], "!   95") {
		t.Fatalf("renderProjectList() should show repo warnings in ATTN, got %q", lines[1])
	}
	if strings.Contains(lines[1], "demo project !") {
		t.Fatalf("renderProjectList() should keep the project name free of suffix markers, got %q", lines[1])
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
	if len(lines) < 2 {
		t.Fatalf("renderProjectList() expected header plus one row, got %q", rendered)
	}
	if !strings.Contains(lines[0], "AGENT") || !strings.Contains(lines[0], "TODO RUN") {
		t.Fatalf("renderProjectList() missing agent/todo/run headers, got %q", lines[0])
	}
	if !strings.Contains(lines[1], " 3 ") {
		t.Fatalf("renderProjectList() should show the open TODO count in the row, got %q", lines[1])
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
	if len(lines) < 2 {
		t.Fatalf("renderProjectList() expected header plus one row, got %q", rendered)
	}
	if !strings.Contains(lines[1], "HOT!") {
		t.Fatalf("renderProjectList() should flag suspicious hot PIDs in RUN, got %q", lines[1])
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

func TestRenderProjectListCollapsesLinkedWorktreesUnderRepoRow(t *testing.T) {
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
		worktreeExpanded: map[string]bool{rootPath: false},
		sortMode:         sortByAttention,
		visibility:       visibilityAllFolders,
	}

	m.rebuildProjectList(rootPath)
	rendered := ansi.Strip(m.renderProjectList(140, 8))
	lines := strings.Split(rendered, "\n")
	if len(lines) != 2 {
		t.Fatalf("renderProjectList() expected header plus one grouped row, got %q", rendered)
	}
	if !strings.Contains(lines[1], "▸ repo") {
		t.Fatalf("renderProjectList() should show a collapsed disclosure row, got %q", lines[1])
	}
	if !strings.Contains(lines[1], "Keep root summary") {
		t.Fatalf("renderProjectList() should keep the root repo assessment text, got %q", lines[1])
	}
	if !strings.Contains(lines[1], "[1 linked, 1 active]") {
		t.Fatalf("renderProjectList() should show a compact linked-worktree badge, got %q", lines[1])
	}
	if strings.Contains(lines[1], "2 worktrees") {
		t.Fatalf("renderProjectList() should not describe the root repo as a generic worktree, got %q", lines[1])
	}
	if strings.Contains(lines[1], "feat/parallel-lane") {
		t.Fatalf("renderProjectList() should keep child worktree rows hidden while collapsed, got %q", lines[1])
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
	if len(lines) != 2 {
		t.Fatalf("renderProjectList() expected header plus one root row, got %q", rendered)
	}
	if !strings.Contains(lines[1], "Keep root summary") {
		t.Fatalf("renderProjectList() should keep the root summary text, got %q", lines[1])
	}
	if !strings.Contains(lines[1], "[1 orphaned]") {
		t.Fatalf("renderProjectList() should show an orphaned-checkout badge on the root row, got %q", lines[1])
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
		worktreeExpanded: map[string]bool{rootPath: false},
		sortMode:         sortByAttention,
		visibility:       visibilityAllFolders,
	}

	m.rebuildProjectList(rootPath)

	if len(m.projects) != 1 {
		t.Fatalf("rebuildProjectList() grouped projects = %#v, want a single root row", m.projects)
	}
	if got := m.projects[0].LastActivity; !got.Equal(worktreeLast) {
		t.Fatalf("root row LastActivity = %v, want %v from the linked worktree", got, worktreeLast)
	}
}

func TestRenderProjectListShowsExpandedWorktreeChildren(t *testing.T) {
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
		worktreeExpanded: map[string]bool{rootPath: true},
		sortMode:         sortByAttention,
		visibility:       visibilityAllFolders,
	}

	m.rebuildProjectList(rootPath)
	rendered := ansi.Strip(m.renderProjectList(160, 8))
	lines := strings.Split(rendered, "\n")
	if len(lines) != 3 {
		t.Fatalf("renderProjectList() expected header plus root and child rows, got %q", rendered)
	}
	if !strings.Contains(lines[1], "▾ repo") {
		t.Fatalf("renderProjectList() should show an expanded disclosure row, got %q", lines[1])
	}
	if !strings.Contains(lines[2], "↳ feat/parallel-lane") {
		t.Fatalf("renderProjectList() should render the child worktree branch label, got %q", lines[2])
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
	if len(lines) != 3 {
		t.Fatalf("renderProjectList() should auto-expand a clean unmerged worktree, got %q", rendered)
	}
	if !strings.Contains(lines[1], "▾ repo") || !strings.Contains(lines[1], "M") {
		t.Fatalf("renderProjectList() should mark the root row when a linked worktree needs merging, got %q", lines[1])
	}
	if !strings.Contains(lines[1], "[1 linked, 1 needs merge]") {
		t.Fatalf("renderProjectList() should count unmerged linked worktrees in the root badge, got %q", lines[1])
	}
	if !strings.Contains(lines[2], "↳ feat/parallel-lane") || !strings.Contains(lines[2], "M") {
		t.Fatalf("renderProjectList() should mark the linked worktree row when it needs merging, got %q", lines[2])
	}
	if !strings.Contains(lines[2], "ready to merge into master") {
		t.Fatalf("renderProjectList() should show merge status in the linked worktree summary, got %q", lines[2])
	}
}

func TestRenderProjectListShowsUnmergedBadgeWhenWorktreeGroupCollapsed(t *testing.T) {
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
				Name:                 "repo--feat-parallel-lane",
				Path:                 "/tmp/repo--feat-parallel-lane",
				Status:               model.StatusIdle,
				PresentOnDisk:        true,
				WorktreeRootPath:     rootPath,
				WorktreeKind:         model.WorktreeKindLinked,
				WorktreeParentBranch: "master",
				WorktreeMergeStatus:  model.WorktreeMergeStatusNotMerged,
				RepoBranch:           "feat/parallel-lane",
			},
		},
		worktreeExpanded: map[string]bool{rootPath: false},
		sortMode:         sortByAttention,
		visibility:       visibilityAllFolders,
	}

	m.rebuildProjectList(rootPath)
	rendered := ansi.Strip(m.renderProjectList(160, 8))
	lines := strings.Split(rendered, "\n")
	if len(lines) != 2 {
		t.Fatalf("renderProjectList() expected header plus collapsed root row, got %q", rendered)
	}
	if !strings.Contains(lines[1], "▸ repo") || !strings.Contains(lines[1], "M") {
		t.Fatalf("renderProjectList() should mark collapsed root rows with unmerged linked work, got %q", lines[1])
	}
	if !strings.Contains(lines[1], "[1 linked, 1 needs merge]") {
		t.Fatalf("renderProjectList() should keep unmerged linked work visible while collapsed, got %q", lines[1])
	}
	if strings.Contains(lines[1], "feat/parallel-lane") {
		t.Fatalf("renderProjectList() should keep child rows hidden while collapsed, got %q", lines[1])
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
		worktreeExpanded: map[string]bool{rootPath: false},
		sortMode:         sortByAttention,
		visibility:       visibilityAllFolders,
		privacyMode:      true,
		privacyPatterns:  []string{"*test*"},
	}

	m.rebuildProjectList(rootPath)
	rendered := ansi.Strip(m.renderProjectList(140, 8))
	lines := strings.Split(rendered, "\n")
	if len(lines) != 2 {
		t.Fatalf("renderProjectList() expected header plus grouped root row, got %q", rendered)
	}
	if !strings.Contains(lines[1], "[1 linked]") {
		t.Fatalf("renderProjectList() should keep linked lanes visible under a visible root, got %q", lines[1])
	}
}

func TestToggleSelectedWorktreeGroupStillWorksWhenChildMatchesPrivacyPattern(t *testing.T) {
	rootPath := "/tmp/LittleControlRoom"
	childPath := "/tmp/LittleControlRoom--make-test-failures"
	m := Model{
		focusedPane: focusProjects,
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
	cmd := m.toggleSelectedWorktreeGroup()
	if m.status != "Worktrees expanded" {
		t.Fatalf("status = %q, want worktrees expanded", m.status)
	}
	if len(m.projectRows) != 2 || m.projectRows[1].Kind != projectListRowWorktree {
		t.Fatalf("projectRows = %#v, want expanded root + child worktree rows", m.projectRows)
	}
	if cmd == nil {
		t.Fatalf("toggleSelectedWorktreeGroup() should still request a detail refresh after expansion")
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
		allProjects: []model.ProjectSummary{{
			Name:          "demo",
			Path:          dir,
			PresentOnDisk: true,
			RunCommand:    "pnpm dev",
		}},
		selected:         0,
		visibility:       visibilityAllFolders,
		runtimeManager:   manager,
		runtimeSnapshots: make(map[string]projectrun.Snapshot),
	}
	cmd := m.openRuntimeInspectorForSelection()
	if cmd == nil {
		t.Fatalf("openRuntimeInspectorForSelection() should queue a runtime cache refresh")
	}
	updated, followup := m.update(cmd())
	m = updated.(Model)
	if followup != nil {
		t.Fatalf("runtime pane refresh should not queue a follow-up without an in-flight refresh")
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

func TestRenderDetailContentShowsTODOSection(t *testing.T) {
	m := Model{
		projects: []model.ProjectSummary{{
			Name:           "demo",
			Path:           "/tmp/demo",
			Status:         model.StatusIdle,
			PresentOnDisk:  true,
			OpenTODOCount:  2,
			TotalTODOCount: 2,
		}},
		detail: model.ProjectDetail{
			Summary: model.ProjectSummary{Path: "/tmp/demo", OpenTODOCount: 2, TotalTODOCount: 2},
			Todos: []model.TodoItem{
				{ID: 1, ProjectPath: "/tmp/demo", Text: "Line one"},
				{ID: 2, ProjectPath: "/tmp/demo", Text: "Line two"},
			},
		},
		selected: 0,
	}

	rendered := ansi.Strip(m.renderDetailContent(60))
	if !strings.Contains(rendered, "TODO") {
		t.Fatalf("renderDetailContent() should include a TODO section: %q", rendered)
	}
	if !strings.Contains(rendered, "[ ] Line one") || !strings.Contains(rendered, "[ ] Line two") {
		t.Fatalf("renderDetailContent() should render open TODO items: %q", rendered)
	}
}

func TestViewStacksListAndDetailVertically(t *testing.T) {
	m := Model{
		projects: []model.ProjectSummary{{
			Name:                             "demo",
			Path:                             "/tmp/demo",
			Status:                           model.StatusIdle,
			PresentOnDisk:                    true,
			RepoBranch:                       "master",
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
	if !strings.Contains(rendered, "Repo: dirty, ahead 2 (master)") {
		t.Fatalf("View() should show combined repo status in the detail pane: %q", rendered)
	}
	if strings.Contains(rendered, "Branch: master") {
		t.Fatalf("View() should fold the current branch into the repo line, got %q", rendered)
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
	if strings.Contains(rendered, "(idle)") {
		t.Fatalf("renderDetailContent() should no longer combine assessment with parenthetical activity: %q", rendered)
	}
	if strings.Contains(rendered, "Activity:") {
		t.Fatalf("renderDetailContent() should hide idle activity noise: %q", rendered)
	}
	if strings.Contains(rendered, "Status:") {
		t.Fatalf("renderDetailContent() should not show a generic Status field: %q", rendered)
	}
	if strings.Contains(rendered, "Attention status:") {
		t.Fatalf("renderDetailContent() still shows separate attention status line: %q", rendered)
	}
	if !strings.Contains(rendered, "Attention:") {
		t.Fatalf("renderDetailContent() missing attention score field: %q", rendered)
	}
}

func TestRenderDetailShowsActivityWhenItAddsSignal(t *testing.T) {
	m := Model{
		projects: []model.ProjectSummary{{
			Path:                            "/tmp/demo",
			Name:                            "demo",
			Status:                          model.StatusPossiblyStuck,
			PresentOnDisk:                   true,
			LatestSessionClassification:     model.ClassificationCompleted,
			LatestSessionClassificationType: model.SessionCategoryInProgress,
		}},
		selected: 0,
	}

	rendered := m.renderDetailContent(80)
	if !strings.Contains(rendered, "Activity:") {
		t.Fatalf("renderDetailContent() should show non-idle activity: %q", rendered)
	}
	if !strings.Contains(rendered, "stuck") {
		t.Fatalf("renderDetailContent() missing non-idle activity value: %q", rendered)
	}
	foundCombinedRow := false
	for _, line := range strings.Split(ansi.Strip(rendered), "\n") {
		if strings.Contains(line, "Assessment:") && strings.Contains(line, "Activity:") {
			foundCombinedRow = true
			break
		}
	}
	if !foundCombinedRow {
		t.Fatalf("renderDetailContent() should place assessment and activity on the same row when there is room: %q", rendered)
	}
}

func TestRenderDetailContentShowsRepoConflict(t *testing.T) {
	m := Model{
		projects: []model.ProjectSummary{{
			Path:          "/tmp/repo",
			Name:          "repo",
			Status:        model.StatusIdle,
			PresentOnDisk: true,
			RepoConflict:  true,
			RepoDirty:     true,
			RepoBranch:    "feat/worktree-ux",
		}},
		selected: 0,
	}

	rendered := ansi.Strip(m.renderDetailContent(100))
	if !strings.Contains(rendered, "Conflict:") {
		t.Fatalf("renderDetailContent() missing conflict field: %q", rendered)
	}
	if !strings.Contains(rendered, "Unmerged files are present") {
		t.Fatalf("renderDetailContent() missing conflict explanation: %q", rendered)
	}
	if !strings.Contains(rendered, "Repo: conflict") {
		t.Fatalf("renderDetailContent() should surface repo conflict state: %q", rendered)
	}
}

func TestRenderDetailContentOmitsRemoteSyncForLinkedWorktree(t *testing.T) {
	m := Model{
		projects: []model.ProjectSummary{{
			Path:           "/tmp/repo--lane",
			Name:           "repo--lane",
			Status:         model.StatusIdle,
			PresentOnDisk:  true,
			WorktreeKind:   model.WorktreeKindLinked,
			RepoBranch:     "feat/worktree-ux",
			RepoSyncStatus: model.RepoSyncNoUpstream,
		}},
		selected: 0,
	}

	rendered := ansi.Strip(m.renderDetailContent(100))
	if strings.Contains(rendered, "no upstream") {
		t.Fatalf("renderDetailContent() should omit remote sync copy for linked worktrees: %q", rendered)
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
	if !strings.Contains(rendered, "Assessment: waiting for model 00:37") {
		t.Fatalf("renderDetailContent() missing assessment progress label: %q", rendered)
	}
	if !strings.Contains(rendered, "Summary: assessment waiting for model 00:37") {
		t.Fatalf("renderDetailContent() missing assessment progress summary: %q", rendered)
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
	if !strings.Contains(rendered, "Summary: This is a deliberately long") {
		t.Fatalf("renderDetailContent() missing wrapped summary start: %q", rendered)
	}
	if !strings.Contains(rendered, "         wrap inside the detail pane") {
		t.Fatalf("renderDetailContent() missing wrapped summary continuation: %q", rendered)
	}
	for _, line := range strings.Split(rendered, "\n") {
		if ansi.StringWidth(line) > 40 {
			t.Fatalf("wrapped detail line width = %d, want <= 40: %q", ansi.StringWidth(line), line)
		}
	}
}

func TestRenderDetailMissingProjectShowsForgetHint(t *testing.T) {
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
	if !strings.Contains(rendered, "Use /remove to take this missing folder off the dashboard.") {
		t.Fatalf("renderDetailContent() missing /remove guidance for missing folders: %q", rendered)
	}
}

func TestRenderDetailShowsMissingLinkedWorktreeGuidance(t *testing.T) {
	m := Model{
		projects: []model.ProjectSummary{{
			Path:                            "/tmp/demo--feature",
			Name:                            "demo--feature",
			Status:                          model.StatusIdle,
			PresentOnDisk:                   false,
			WorktreeKind:                    model.WorktreeKindLinked,
			WorktreeRootPath:                "/tmp/demo",
			LatestSessionClassification:     model.ClassificationCompleted,
			LatestSessionClassificationType: model.SessionCategoryCompleted,
		}},
		selected: 0,
	}

	rendered := ansi.Strip(m.renderDetailContent(120))
	if !strings.Contains(rendered, "Use /remove to clean up this missing linked worktree.") || !strings.Contains(rendered, "x and /wt remove still work too.") {
		t.Fatalf("renderDetailContent() missing linked worktree guidance for missing folders: %q", rendered)
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

func TestCPUCommandOpensProcessInspector(t *testing.T) {
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

	updated, cmd := m.dispatchCommand(commands.Invocation{Kind: commands.KindCPU})
	got := updated.(Model)
	if got.cpuDialog == nil {
		t.Fatalf("/cpu should open the CPU inspector")
	}
	if !got.cpuDialog.Loading {
		t.Fatalf("dialog should start in loading state")
	}
	if cmd == nil {
		t.Fatalf("/cpu should queue an async CPU scan")
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

func TestFKeyOpensProjectFilterDialog(t *testing.T) {
	m := Model{
		projects: []model.ProjectSummary{{
			Path:          "/tmp/missing",
			Name:          "missing",
			PresentOnDisk: false,
		}},
		width:    100,
		height:   24,
		selected: 0,
	}

	updated, cmd := m.updateNormalMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	got := updated.(Model)
	if got.projectFilterDialog == nil {
		t.Fatalf("f key should open the project filter dialog")
	}
	if got.status != "Project filter open. Type to narrow, Enter keep, Esc close" {
		t.Fatalf("status = %q, want filter dialog status", got.status)
	}
	if cmd == nil {
		t.Fatalf("f key should return a focus command")
	}
}

func TestLowercaseBKeyOpensBossMode(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.BossChatBackend = config.AIBackendOpenAIAPI
	settings.OpenAIAPIKey = "sk-test-example"

	m := Model{
		settingsBaseline: &settings,
		width:            100,
		height:           24,
	}

	updated, cmd := m.updateNormalMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}})
	got := updated.(Model)
	if !got.bossMode {
		t.Fatalf("lowercase b key should open boss mode")
	}
	if got.bossSetupPrompt != nil {
		t.Fatalf("configured lowercase b key should not show setup prompt")
	}
	if cmd == nil {
		t.Fatalf("lowercase b key should return the boss init command")
	}
}

func TestSKeyNoLongerSnoozesProject(t *testing.T) {
	m := Model{
		projects: []model.ProjectSummary{{
			Path:          "/tmp/demo",
			Name:          "demo",
			PresentOnDisk: true,
		}},
		selected: 0,
		width:    100,
		height:   24,
	}

	updated, cmd := m.updateNormalMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	if cmd != nil {
		t.Fatalf("lowercase s should no longer enqueue a snooze command")
	}
	got := updated.(Model)
	if got.status != "" {
		t.Fatalf("lowercase s should not change status, got %q", got.status)
	}
}

func TestSUppercaseNoLongerClearsSnooze(t *testing.T) {
	m := Model{
		projects: []model.ProjectSummary{{Path: "/tmp/demo", Name: "demo", PresentOnDisk: true}},
		selected: 0,
		width:    100,
		height:   24,
	}

	updated, cmd := m.updateNormalMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'S'}})
	if cmd != nil {
		t.Fatalf("uppercase S should no longer enqueue a clear-snooze command")
	}
	got := updated.(Model)
	if got.status != "" {
		t.Fatalf("uppercase S should not change status, got %q", got.status)
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
	if got.status != "Embedded Codex session reopened. Esc hides it." {
		t.Fatalf("status = %q, want live restore notice", got.status)
	}
	if len(requests) != 1 {
		t.Fatalf("launch requests = %d, want 1 because reopening should not launch again", len(requests))
	}
}

func TestEnterReopensClosedEmbeddedSessionByLaunchingAgain(t *testing.T) {
	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider: req.Provider.Normalized(),
				Started:  true,
				ThreadID: req.ResumeID,
				Status:   req.Provider.Label() + " session ready",
			},
		}, nil
	})
	session, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Provider:    codexapp.ProviderCodex,
		Preset:      codexcli.PresetYolo,
		ResumeID:    "thread-closed",
	})
	if err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("session.Close() error = %v", err)
	}

	m := Model{
		codexManager: manager,
		projects: []model.ProjectSummary{{
			Path:                "/tmp/demo",
			Name:                "demo",
			PresentOnDisk:       true,
			LatestSessionID:     "thread-closed",
			LatestSessionFormat: "modern",
		}},
		selected:      0,
		focusedPane:   focusProjects,
		codexInput:    newCodexTextarea(),
		codexDrafts:   make(map[string]codexDraft),
		codexViewport: viewport.New(0, 0),
		width:         100,
		height:        24,
	}

	updated, cmd := m.updateNormalMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("enter should launch a replacement for a closed embedded session")
	}
	if got.codexPendingOpen == nil || got.codexPendingOpen.projectPath != "/tmp/demo" {
		t.Fatalf("codexPendingOpen = %#v, want pending replacement open", got.codexPendingOpen)
	}
	if got.codexVisibleProject == "/tmp/demo" {
		t.Fatalf("codexVisibleProject = %q, want closed session not to be merely reopened", got.codexVisibleProject)
	}
	if got.status != "Opening embedded Codex session..." {
		t.Fatalf("status = %q, want embedded open notice", got.status)
	}

	msg := cmd()
	opened, ok := msg.(codexSessionOpenedMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want codexSessionOpenedMsg", msg)
	}
	if opened.err != nil {
		t.Fatalf("replacement open returned error = %v", opened.err)
	}
	if len(requests) != 2 {
		t.Fatalf("launch requests = %d, want original plus replacement open", len(requests))
	}
	if requests[1].ResumeID != "thread-closed" {
		t.Fatalf("replacement resume id = %q, want thread-closed", requests[1].ResumeID)
	}
}

func TestShowCodexProjectQueuesDeferredSnapshotWhenRevealSnapshotIsContended(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Provider: codexapp.ProviderCodex,
			Started:  true,
			ThreadID: "thread-demo",
			Status:   "Codex session ready",
		},
		trySnapshotFn: func(*fakeCodexSession) (codexapp.Snapshot, bool) {
			return codexapp.Snapshot{}, false
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
		projects: []model.ProjectSummary{{
			Path:          "/tmp/demo",
			Name:          "demo",
			PresentOnDisk: true,
		}},
		selected:      0,
		codexInput:    newCodexTextarea(),
		codexDrafts:   make(map[string]codexDraft),
		codexViewport: viewport.New(0, 0),
		width:         100,
		height:        24,
	}

	updated, cmd := m.showCodexProject("/tmp/demo", "Embedded session restored")
	got := updated.(Model)
	if got.codexVisibleProject != "/tmp/demo" {
		t.Fatalf("codexVisibleProject = %q, want /tmp/demo", got.codexVisibleProject)
	}
	if got.codexHiddenProject != "/tmp/demo" {
		t.Fatalf("codexHiddenProject = %q, want /tmp/demo", got.codexHiddenProject)
	}
	if session.trySnapshotCalls != 1 {
		t.Fatalf("reveal should attempt exactly one non-blocking snapshot refresh, got %d", session.trySnapshotCalls)
	}
	if session.snapshotCalls != 0 {
		t.Fatalf("showCodexProject() should not take a blocking snapshot on the UI thread; snapshot calls = %d", session.snapshotCalls)
	}
	if cmd == nil {
		t.Fatalf("showCodexProject() should queue follow-up commands")
	}

	msgs := collectCmdMsgs(cmd)
	var deferred codexDeferredSnapshotMsg
	foundDeferred := false
	for _, msg := range msgs {
		if candidate, ok := msg.(codexDeferredSnapshotMsg); ok {
			deferred = candidate
			foundDeferred = true
			break
		}
	}
	if !foundDeferred {
		t.Fatalf("showCodexProject() should queue a deferred snapshot when reveal-time TrySnapshot is contended, got %#v", msgs)
	}
	if deferred.projectPath != "/tmp/demo" {
		t.Fatalf("deferred project path = %q, want /tmp/demo", deferred.projectPath)
	}
	if deferred.snapshot.ThreadID != "thread-demo" {
		t.Fatalf("deferred snapshot thread id = %q, want %q", deferred.snapshot.ThreadID, "thread-demo")
	}
	if session.snapshotCalls != 1 {
		t.Fatalf("deferred snapshot command should perform one blocking snapshot off the UI thread; snapshot calls = %d", session.snapshotCalls)
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

func TestSyncDetailViewportSkipsHiddenDashboardWhileCodexVisible(t *testing.T) {
	m := Model{
		projects: []model.ProjectSummary{{
			Path:                          "/tmp/demo",
			Name:                          "demo",
			PresentOnDisk:                 true,
			LatestCompletedSessionSummary: "fresh hidden summary",
		}},
		selected:            0,
		codexVisibleProject: "/tmp/demo",
		detail: model.ProjectDetail{
			Summary: model.ProjectSummary{Path: "/tmp/demo"},
		},
		runtimeSnapshots: map[string]projectrun.Snapshot{
			"/tmp/demo": {
				ProjectPath:  "/tmp/demo",
				RecentOutput: []string{"fresh runtime output"},
			},
		},
		detailViewport:  viewport.New(20, 5),
		runtimeViewport: viewport.New(20, 5),
		width:           100,
		height:          24,
	}
	m.detailViewport.SetContent("stale detail cache")
	m.runtimeViewport.SetContent("stale runtime cache")

	m.syncDetailViewport(false)

	if got := ansi.Strip(m.detailViewport.View()); !strings.Contains(got, "stale detail cache") {
		t.Fatalf("hidden detail sync should keep the cached detail viewport content, got %q", got)
	} else if strings.Contains(got, "fresh hidden summary") {
		t.Fatalf("hidden detail sync should not eagerly rebuild dashboard content while Codex is visible, got %q", got)
	}
	if got := ansi.Strip(m.runtimeViewport.View()); !strings.Contains(got, "stale runtime cache") {
		t.Fatalf("hidden detail sync should leave the cached runtime viewport content alone, got %q", got)
	} else if strings.Contains(got, "fresh runtime output") {
		t.Fatalf("hidden detail sync should not eagerly rebuild the runtime viewport while Codex is visible, got %q", got)
	}
}

func TestHideCodexSessionResyncsDashboardPanes(t *testing.T) {
	seenAt := time.Date(2026, 4, 4, 12, 0, 0, 0, time.UTC)
	m := Model{
		projects: []model.ProjectSummary{{
			Path:                            "/tmp/demo",
			Name:                            "demo",
			PresentOnDisk:                   true,
			LatestCompletedSessionSummary:   "fresh summary after hide",
			LatestSessionClassification:     model.ClassificationCompleted,
			LatestSessionClassificationType: model.SessionCategoryCompleted,
			LatestSessionFormat:             "modern",
			LatestSessionLastEventAt:        seenAt.Add(-2 * time.Minute),
			LatestTurnStateKnown:            true,
			LatestTurnCompleted:             true,
		}},
		selected:            0,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexSnapshots: map[string]codexapp.Snapshot{
			"/tmp/demo": {
				ProjectPath: "/tmp/demo",
				Started:     true,
				Status:      "Codex session ready",
			},
		},
		codexInput: newCodexTextarea(),
		detail: model.ProjectDetail{
			Summary: model.ProjectSummary{
				Path:                            "/tmp/demo",
				LatestSessionClassification:     model.ClassificationCompleted,
				LatestSessionClassificationType: model.SessionCategoryCompleted,
				LatestSessionFormat:             "modern",
				LatestSessionLastEventAt:        seenAt.Add(-2 * time.Minute),
				LatestTurnStateKnown:            true,
				LatestTurnCompleted:             true,
			},
		},
		runtimeSnapshots: map[string]projectrun.Snapshot{
			"/tmp/demo": {
				ProjectPath:  "/tmp/demo",
				RecentOutput: []string{"runtime after hide"},
			},
		},
		detailViewport:  viewport.New(20, 5),
		runtimeViewport: viewport.New(20, 5),
		width:           100,
		height:          24,
		nowFn:           func() time.Time { return seenAt },
	}
	m.detailViewport.SetContent("old detail cache")
	m.runtimeViewport.SetContent("old runtime cache")

	updated, _ := m.hideCodexSession()
	got := updated.(Model)

	if got.codexVisibleProject != "" {
		t.Fatalf("codexVisibleProject = %q, want hidden", got.codexVisibleProject)
	}
	if got := ansi.Strip(got.detailViewport.View()); !strings.Contains(got, "fresh summary after hide") {
		t.Fatalf("hiding Codex should resync the detail viewport before returning to the dashboard, got %q", got)
	}
	if got := ansi.Strip(got.runtimeViewport.View()); !strings.Contains(got, "runtime after hide") {
		t.Fatalf("hiding Codex should resync the runtime viewport before returning to the dashboard, got %q", got)
	}
	if !got.projects[0].LastSessionSeenAt.Equal(seenAt) {
		t.Fatalf("projects seen_at = %v, want %v", got.projects[0].LastSessionSeenAt, seenAt)
	}
	if !got.detail.Summary.LastSessionSeenAt.Equal(seenAt) {
		t.Fatalf("detail seen_at = %v, want %v", got.detail.Summary.LastSessionSeenAt, seenAt)
	}
}

func TestHideCodexSessionRefreshesProjectStatusWhenIdle(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	projectPath := filepath.Join(t.TempDir(), "repo")
	runTUITestGit(t, "", "init", projectPath)
	runTUITestGit(t, projectPath, "config", "user.name", "Little Control Room Tests")
	runTUITestGit(t, projectPath, "config", "user.email", "tests@example.com")
	if err := os.WriteFile(filepath.Join(projectPath, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write README.md: %v", err)
	}
	runTUITestGit(t, projectPath, "add", "README.md")
	runTUITestGit(t, projectPath, "commit", "-m", "initial commit")

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:          projectPath,
		Name:          "repo",
		PresentOnDisk: true,
		InScope:       true,
		RepoBranch:    "master",
		RepoDirty:     false,
		UpdatedAt:     time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed clean project state: %v", err)
	}

	if err := os.WriteFile(filepath.Join(projectPath, "README.md"), []byte("hello\nchanged\n"), 0o644); err != nil {
		t.Fatalf("update README.md: %v", err)
	}

	svc := service.New(config.Default(), st, events.NewBus(), nil)
	m := Model{
		ctx: ctx,
		svc: svc,
		projects: []model.ProjectSummary{{
			Path:          projectPath,
			Name:          "repo",
			PresentOnDisk: true,
			RepoBranch:    "master",
		}},
		allProjects: []model.ProjectSummary{{
			Path:          projectPath,
			Name:          "repo",
			PresentOnDisk: true,
			RepoBranch:    "master",
		}},
		selected:            0,
		codexVisibleProject: projectPath,
		codexHiddenProject:  projectPath,
		codexSnapshots: map[string]codexapp.Snapshot{
			projectPath: {
				ProjectPath: projectPath,
				Started:     true,
				Status:      "Codex session ready",
			},
		},
		codexInput:      newCodexTextarea(),
		detailViewport:  viewport.New(20, 5),
		runtimeViewport: viewport.New(20, 5),
		width:           100,
		height:          24,
		detail: model.ProjectDetail{
			Summary: model.ProjectSummary{
				Path:          projectPath,
				Name:          "repo",
				PresentOnDisk: true,
				RepoBranch:    "master",
			},
		},
	}

	updated, cmd := m.hideCodexSession()
	got := updated.(Model)
	if cmd == nil {
		t.Fatal("hideCodexSession() should queue follow-up work")
	}

	got = drainCmdMsgs(got, cmd)

	if got.detail.Summary.RepoDirty != true {
		t.Fatalf("detail repo dirty = %t, want refreshed dirty state", got.detail.Summary.RepoDirty)
	}

	detail, err := st.GetProjectDetail(ctx, projectPath, 20)
	if err != nil {
		t.Fatalf("get detail after hide refresh: %v", err)
	}
	if !detail.Summary.RepoDirty {
		t.Fatalf("store detail should reflect dirty repo after hide refresh, got %#v", detail.Summary)
	}
}

func TestRenderDetailViewportUsesSyncedCache(t *testing.T) {
	m := Model{
		projects: []model.ProjectSummary{{
			Path:                          "/tmp/demo",
			Name:                          "demo",
			PresentOnDisk:                 true,
			LatestCompletedSessionSummary: "cached detail summary",
		}},
		selected: 0,
		detail: model.ProjectDetail{
			Summary: model.ProjectSummary{Path: "/tmp/demo"},
		},
		detailViewport: viewport.New(20, 5),
		width:          100,
		height:         24,
	}

	m.syncDetailViewport(false)
	m.projects[0].LatestCompletedSessionSummary = "uncached detail summary"

	layout := m.bodyLayout()
	rendered := ansi.Strip(m.renderDetailViewport(layout.detailContentWidth, max(1, layout.bottomPaneHeight-2)))
	if !strings.Contains(rendered, "cached detail summary") {
		t.Fatalf("renderDetailViewport() should use the synced detail cache until the next explicit sync, got %q", rendered)
	}
	if strings.Contains(rendered, "uncached detail summary") {
		t.Fatalf("renderDetailViewport() should not rebuild detail content on every render, got %q", rendered)
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
		trySnapshotFn: func(*fakeCodexSession) (codexapp.Snapshot, bool) {
			return codexapp.Snapshot{}, false
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
		codexSnapshots: map[string]codexapp.Snapshot{
			"/tmp/demo": session.snapshot,
		},
		codexInput:    newCodexTextarea(),
		codexViewport: viewport.New(0, 0),
		width:         100,
		height:        24,
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
	if session.snapshotCalls != 0 {
		t.Fatalf("refreshBusyElsewhereCmd() should avoid blocking Snapshot(); snapshot calls = %d", session.snapshotCalls)
	}
	if session.snapshot.BusyExternal {
		t.Fatalf("session should no longer be busy externally after refresh")
	}
}

func TestRefreshBusyElsewhereCmdSkipsLiveLookupWithoutCachedBusyFlag(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Started:      true,
			Busy:         true,
			BusyExternal: true,
			ThreadID:     "thread-demo",
		},
		trySnapshotFn: func(*fakeCodexSession) (codexapp.Snapshot, bool) {
			return codexapp.Snapshot{}, false
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

	cmd := m.refreshBusyElsewhereCmd("/tmp/demo")
	if cmd != nil {
		t.Fatalf("refreshBusyElsewhereCmd() = %#v, want nil without a cached busy-external snapshot", cmd)
	}
	if session.trySnapshotCalls != 0 || session.snapshotCalls != 0 {
		t.Fatalf("refreshBusyElsewhereCmd() should not probe the live session on cache miss; TrySnapshot/Snapshot calls = %d/%d", session.trySnapshotCalls, session.snapshotCalls)
	}
	if session.refreshCalls != 0 {
		t.Fatalf("refresh calls = %d, want 0", session.refreshCalls)
	}
}

func TestLiveCodexSnapshotsUseCachedMapInsteadOfManagerSnapshots(t *testing.T) {
	sessionA := &fakeCodexSession{
		projectPath: "/tmp/demo-a",
		snapshot: codexapp.Snapshot{
			Started:        true,
			ProjectPath:    "/tmp/demo-a",
			ThreadID:       "thread-a",
			LastActivityAt: time.Date(2026, 4, 4, 12, 0, 0, 0, time.UTC),
		},
	}
	sessionB := &fakeCodexSession{
		projectPath: "/tmp/demo-b",
		snapshot: codexapp.Snapshot{
			Started:        true,
			ProjectPath:    "/tmp/demo-b",
			ThreadID:       "thread-b",
			LastActivityAt: time.Date(2026, 4, 4, 12, 5, 0, 0, time.UTC),
		},
	}
	sessions := map[string]*fakeCodexSession{
		sessionA.projectPath: sessionA,
		sessionB.projectPath: sessionB,
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		session, ok := sessions[req.ProjectPath]
		if !ok {
			return nil, fmt.Errorf("unexpected project %q", req.ProjectPath)
		}
		return session, nil
	})
	for _, projectPath := range []string{sessionA.projectPath, sessionB.projectPath} {
		if _, _, err := manager.Open(codexapp.LaunchRequest{ProjectPath: projectPath}); err != nil {
			t.Fatalf("manager.Open(%q) error = %v", projectPath, err)
		}
	}
	sessionA.snapshotCalls = 0
	sessionB.snapshotCalls = 0

	m := Model{
		codexManager: manager,
		codexSnapshots: map[string]codexapp.Snapshot{
			sessionA.projectPath: sessionA.snapshot,
			sessionB.projectPath: sessionB.snapshot,
		},
	}

	snapshots := m.liveCodexSnapshots()
	if len(snapshots) != 2 {
		t.Fatalf("liveCodexSnapshots() len = %d, want 2", len(snapshots))
	}
	if snapshots[0].ProjectPath != sessionB.projectPath || snapshots[1].ProjectPath != sessionA.projectPath {
		t.Fatalf("liveCodexSnapshots() order = [%q, %q], want most recent cached session first", snapshots[0].ProjectPath, snapshots[1].ProjectPath)
	}
	if sessionA.snapshotCalls != 0 || sessionB.snapshotCalls != 0 {
		t.Fatalf("liveCodexSnapshots() should not consult manager session snapshots; calls = %d/%d", sessionA.snapshotCalls, sessionB.snapshotCalls)
	}
}

func TestSubmitVisibleCodexCmdDefersSessionLookupUntilRun(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Started:     true,
			Provider:    codexapp.ProviderCodex,
			ProjectPath: "/tmp/demo",
			ThreadID:    "thread-demo",
			Status:      "Codex session ready",
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})

	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexSnapshots: map[string]codexapp.Snapshot{
			"/tmp/demo": session.snapshot,
		},
	}

	cmd := m.submitVisibleCodexCmd(codexDraft{Text: "summarize this repo"})
	if cmd == nil {
		t.Fatalf("submitVisibleCodexCmd() = nil, want deferred command")
	}

	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Provider:    codexapp.ProviderCodex,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	msg := cmd()
	action, ok := msg.(codexActionMsg)
	if !ok {
		t.Fatalf("submitVisibleCodexCmd() message = %T, want codexActionMsg", msg)
	}
	if action.err != nil {
		t.Fatalf("submitVisibleCodexCmd() err = %v, want nil", action.err)
	}
	if len(session.submissions) != 1 {
		t.Fatalf("submitted inputs = %d, want 1", len(session.submissions))
	}
	if got := session.submissions[0].Text; got != "summarize this repo" {
		t.Fatalf("submitted text = %q, want summarize this repo", got)
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

func TestVisibleCodexEnterRefreshesStaleResumedSnapshotBeforeBlockingSubmit(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Started:  true,
			Preset:   codexcli.PresetYolo,
			ThreadID: "ses_resume",
			Status:   "Codex session ready",
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
		codexSnapshots: map[string]codexapp.Snapshot{
			"/tmp/demo": {
				Started:      true,
				Preset:       codexcli.PresetYolo,
				ThreadID:     "ses_resume",
				Busy:         true,
				Phase:        codexapp.SessionPhaseReconciling,
				ActiveTurnID: "turn_old",
				Status:       "Rechecking turn state...",
			},
		},
		width:  100,
		height: 24,
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if session.trySnapshotCalls == 0 {
		t.Fatalf("enter should refresh the live snapshot before blocking submit")
	}
	if cmd == nil {
		t.Fatalf("enter should submit when the refreshed resumed snapshot is idle")
	}
	if got.codexInput.Value() != "" {
		t.Fatalf("codex input should clear after submit, got %q", got.codexInput.Value())
	}
	if got.status != "Sending prompt to Codex..." {
		t.Fatalf("status = %q, want sending notice after refreshed idle snapshot", got.status)
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
	if !strings.Contains(rendered, "Enter run  Ctrl+C close  Esc hide") {
		t.Fatalf("rendered view should advertise slash command handling in the footer: %q", rendered)
	}
}

func TestVisibleCodexSlashSuggestsHostTaskActions(t *testing.T) {
	input := newCodexTextarea()
	input.SetValue("/task")

	m := Model{
		codexVisibleProject: "/tmp/demo",
		codexInput:          input,
	}

	suggestions := m.codexSlashSuggestions()
	if len(suggestions) != 1 {
		t.Fatalf("codexSlashSuggestions(/task) returned %d suggestions, want 1", len(suggestions))
	}
	if suggestions[0].Insert != "/task-actions" {
		t.Fatalf("codexSlashSuggestions(/task)[0].Insert = %q, want /task-actions", suggestions[0].Insert)
	}
}

func TestVisibleCodexSlashTaskActionsFallsBackToHostCommand(t *testing.T) {
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
	input.SetValue("/task-actions")

	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          input,
		codexViewport:       viewport.New(0, 0),
		projects: []model.ProjectSummary{{
			Name:          "answer Sarah email",
			Path:          "/tmp/tasks/answer-sarah-email",
			Kind:          model.ProjectKindScratchTask,
			PresentOnDisk: true,
		}},
		selected: 0,
		width:    100,
		height:   24,
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("embedded /task-actions should open the host dialog without queuing work")
	}
	if got.scratchTaskAction == nil {
		t.Fatalf("embedded /task-actions should open the scratch task action dialog")
	}
	if got.codexInput.Value() != "" {
		t.Fatalf("codex input should clear after host command fallback, got %q", got.codexInput.Value())
	}
	rendered := ansi.Strip(got.View())
	if !strings.Contains(rendered, "Task Actions") || !strings.Contains(rendered, "Archive") || !strings.Contains(rendered, "Delete") {
		t.Fatalf("host task action dialog should render over visible Codex, got %q", rendered)
	}
}

func TestScratchTaskActionDialogTakesInputPriorityOverVisibleCodex(t *testing.T) {
	m := Model{
		codexVisibleProject: "/tmp/demo",
		scratchTaskAction: &scratchTaskActionConfirmState{
			ProjectPath:   "/tmp/tasks/answer-sarah-email",
			ProjectName:   "answer Sarah email",
			PresentOnDisk: true,
			Selected:      scratchTaskActionFocusKeep,
		},
	}

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("closing task actions over visible Codex should not queue work")
	}
	if got.scratchTaskAction != nil {
		t.Fatalf("Esc should close the scratch task action dialog before reaching visible Codex")
	}
	if got.codexVisibleProject != "/tmp/demo" {
		t.Fatalf("codexVisibleProject = %q, want visible pane to remain open", got.codexVisibleProject)
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
		t.Fatalf("codex input = %q, want /model after shift+tab from /status", got.codexInput.Value())
	}

	updated, cmd = got.updateCodexMode(tea.KeyMsg{Type: tea.KeyTab})
	got = updated.(Model)
	if cmd != nil {
		t.Fatalf("tab cycling should not queue a command")
	}
	if got.codexInput.Value() != "/status" {
		t.Fatalf("codex input = %q, want /status after tab from /model", got.codexInput.Value())
	}

	updated, cmd = got.updateCodexMode(tea.KeyMsg{Type: tea.KeyTab})
	got = updated.(Model)
	if cmd != nil {
		t.Fatalf("tab cycling should not queue a command")
	}
	if got.codexInput.Value() != "/reconnect" {
		t.Fatalf("codex input = %q, want /reconnect after fifth tab", got.codexInput.Value())
	}
}

func TestVisibleCodexSlashBossOpensBossMode(t *testing.T) {
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

	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.BossChatBackend = config.AIBackendOpenAIAPI
	settings.OpenAIAPIKey = "sk-test-example"

	input := newCodexTextarea()
	input.SetValue("/boss")

	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          input,
		codexViewport:       viewport.New(0, 0),
		settingsBaseline:    &settings,
		width:               100,
		height:              24,
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if !got.bossMode {
		t.Fatalf("embedded /boss should open boss mode")
	}
	if got.bossSetupPrompt != nil {
		t.Fatalf("configured embedded /boss should not show setup prompt")
	}
	if got.codexInput.Value() != "" {
		t.Fatalf("codex input should clear after /boss, got %q", got.codexInput.Value())
	}
	if cmd == nil {
		t.Fatalf("embedded /boss should return the boss init command")
	}
}

func TestVisibleOpenCodeSlashReconnectReopensSameSession(t *testing.T) {
	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider: codexapp.ProviderOpenCode,
				Started:  true,
				Status:   "OpenCode session ready",
				ThreadID: "ses-old1",
			},
		}, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Provider:    codexapp.ProviderOpenCode,
		ResumeID:    "ses-old1",
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	input := newCodexTextarea()
	input.SetValue("/reconnect")

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
		t.Fatalf("enter should run the embedded /reconnect command")
	}
	if got.codexInput.Value() != "" {
		t.Fatalf("codex input should clear after /reconnect, got %q", got.codexInput.Value())
	}
	if got.status != "Reconnecting embedded OpenCode session..." {
		t.Fatalf("status = %q, want reconnect notice", got.status)
	}
	if got.codexPendingOpen == nil || got.codexPendingOpen.provider != codexapp.ProviderOpenCode {
		t.Fatalf("codexPendingOpen = %#v, want pending OpenCode reconnect", got.codexPendingOpen)
	}

	msg := cmd()
	opened, ok := msg.(codexSessionOpenedMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want codexSessionOpenedMsg", msg)
	}
	if opened.err != nil {
		t.Fatalf("/reconnect returned error = %v", opened.err)
	}
	if opened.status != "Reconnected embedded OpenCode session ses-old1. Esc hides it." {
		t.Fatalf("opened.status = %q, want reconnect confirmation", opened.status)
	}
	if len(requests) != 2 {
		t.Fatalf("launch requests = %d, want 2", len(requests))
	}
	if requests[1].Provider != codexapp.ProviderOpenCode {
		t.Fatalf("second launch provider = %q, want %q", requests[1].Provider, codexapp.ProviderOpenCode)
	}
	if requests[1].ResumeID != "ses-old1" {
		t.Fatalf("second launch resume id = %q, want %q", requests[1].ResumeID, "ses-old1")
	}
	if requests[1].ForceNew {
		t.Fatalf("second launch request should not force a fresh session")
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

func TestVisibleCodexSlashReviewRunsLocally(t *testing.T) {
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
	input.SetValue("/rev")

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
		t.Fatalf("enter should run the embedded /review command")
	}
	if got.codexInput.Value() != "" {
		t.Fatalf("codex input should clear after /review, got %q", got.codexInput.Value())
	}
	if got.status != "Starting embedded Codex review..." {
		t.Fatalf("status = %q, want review start notice", got.status)
	}

	msg := cmd()
	action, ok := msg.(codexActionMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want codexActionMsg", msg)
	}
	if action.err != nil {
		t.Fatalf("/review returned error = %v", action.err)
	}
	if session.reviewCalls != 1 {
		t.Fatalf("review calls = %d, want 1", session.reviewCalls)
	}
	if len(session.submissions) != 0 {
		t.Fatalf("/review should not submit a Codex prompt, submissions = %d", len(session.submissions))
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
	if got.codexModelPicker.Focus != codexModelPickerFocusFilter {
		t.Fatalf("initial picker focus = %q, want filter", got.codexModelPicker.Focus)
	}

	updated, _ = got.updateCodexModelPickerMode(tea.KeyMsg{Type: tea.KeyTab})
	got = updated.(Model)
	if got.codexModelPicker.Focus != codexModelPickerFocusModels {
		t.Fatalf("picker focus after first tab = %q, want models", got.codexModelPicker.Focus)
	}
	updated, _ = got.updateCodexModelPickerMode(tea.KeyMsg{Type: tea.KeyDown})
	got = updated.(Model)
	if got.codexModelPicker.ModelIndex != 1 {
		t.Fatalf("model index after down = %d, want 1", got.codexModelPicker.ModelIndex)
	}
	updated, _ = got.updateCodexModelPickerMode(tea.KeyMsg{Type: tea.KeyTab})
	got = updated.(Model)
	if got.codexModelPicker.Focus != codexModelPickerFocusEfforts {
		t.Fatalf("picker focus after second tab = %q, want efforts", got.codexModelPicker.Focus)
	}
	updated, _ = got.updateCodexModelPickerMode(tea.KeyMsg{Type: tea.KeyDown})
	got = updated.(Model)
	updated, _ = got.updateCodexModelPickerMode(tea.KeyMsg{Type: tea.KeyDown})
	got = updated.(Model)
	if got.codexModelPicker.EffortIndex != 2 {
		t.Fatalf("effort index after two downs = %d, want 2 (high)", got.codexModelPicker.EffortIndex)
	}
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

func TestVisibleCodexSlashModelKeepsRecentSelectionWhenChoosingReasoning(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			ThreadID:         "thread-demo",
			Started:          true,
			Preset:           codexcli.PresetYolo,
			Status:           "Codex ready",
			Model:            "gpt-5",
			ReasoningEffort:  "medium",
			PendingModel:     "",
			PendingReasoning: "",
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
		recentCodexModels:   []string{"gpt-5-codex"},
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("enter should open the embedded /model picker")
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
	if len(got.codexModelPicker.RecentModels) != 1 || got.codexModelPicker.RecentModels[0].Model != "gpt-5-codex" {
		t.Fatalf("recent models = %#v, want only gpt-5-codex", got.codexModelPicker.RecentModels)
	}

	updated, _ = got.updateCodexModelPickerMode(tea.KeyMsg{Type: tea.KeyTab})
	got = updated.(Model)
	if got.codexModelPicker.Focus != codexModelPickerFocusRecent {
		t.Fatalf("picker focus after first tab = %q, want recent", got.codexModelPicker.Focus)
	}
	modelOption, ok := got.currentCodexModelOption()
	if !ok || modelOption.Model != "gpt-5-codex" {
		t.Fatalf("selected model after focusing recent = %#v, want gpt-5-codex", modelOption)
	}

	updated, _ = got.updateCodexModelPickerMode(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if got.codexModelPicker.Focus != codexModelPickerFocusEfforts {
		t.Fatalf("picker focus after enter on recent = %q, want efforts", got.codexModelPicker.Focus)
	}
	modelOption, ok = got.currentCodexModelOption()
	if !ok || modelOption.Model != "gpt-5-codex" {
		t.Fatalf("selected model after moving to efforts = %#v, want gpt-5-codex", modelOption)
	}

	updated, _ = got.updateCodexModelPickerMode(tea.KeyMsg{Type: tea.KeyDown})
	got = updated.(Model)
	updated, _ = got.updateCodexModelPickerMode(tea.KeyMsg{Type: tea.KeyDown})
	got = updated.(Model)
	if got.codexModelPicker.EffortIndex != 2 {
		t.Fatalf("effort index after two downs = %d, want 2 (high)", got.codexModelPicker.EffortIndex)
	}

	updated, cmd = got.updateCodexModelPickerMode(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if cmd == nil {
		t.Fatalf("enter should apply the selected recent model choice")
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

func TestVisibleCodexSlashModelArrowDownEntersRecentModels(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			ThreadID:        "thread-demo",
			Started:         true,
			Preset:          codexcli.PresetYolo,
			Status:          "Codex ready",
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
		recentCodexModels:   []string{"gpt-5", "gpt-5-codex"},
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("enter should open the embedded /model picker")
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
	if got.codexModelPicker.Focus != codexModelPickerFocusFilter {
		t.Fatalf("initial picker focus = %q, want filter", got.codexModelPicker.Focus)
	}

	updated, _ = got.updateCodexModelPickerMode(tea.KeyMsg{Type: tea.KeyDown})
	got = updated.(Model)
	if got.codexModelPicker.Focus != codexModelPickerFocusRecent {
		t.Fatalf("picker focus after first down = %q, want recent", got.codexModelPicker.Focus)
	}
	modelOption, ok := got.currentCodexModelOption()
	if !ok || modelOption.Model != "gpt-5" {
		t.Fatalf("selected model after entering recent = %#v, want gpt-5", modelOption)
	}

	updated, _ = got.updateCodexModelPickerMode(tea.KeyMsg{Type: tea.KeyDown})
	got = updated.(Model)
	if got.codexModelPicker.RecentIndex != 1 {
		t.Fatalf("recent index after second down = %d, want 1", got.codexModelPicker.RecentIndex)
	}
	modelOption, ok = got.currentCodexModelOption()
	if !ok || modelOption.Model != "gpt-5-codex" {
		t.Fatalf("selected model after moving within recent = %#v, want gpt-5-codex", modelOption)
	}
}

func TestRenderCodexModelPickerMarksRecentFocus(t *testing.T) {
	models := []codexapp.ModelOption{
		{
			ID:                     "gpt-5",
			Model:                  "gpt-5",
			DisplayName:            "GPT-5",
			DefaultReasoningEffort: "medium",
		},
		{
			ID:                     "gpt-5-codex",
			Model:                  "gpt-5-codex",
			DisplayName:            "GPT-5 Codex",
			DefaultReasoningEffort: "medium",
		},
	}
	m := Model{
		codexModelPicker: &codexModelPickerState{
			Models:         append([]codexapp.ModelOption(nil), models...),
			FilteredModels: append([]codexapp.ModelOption(nil), models...),
			RecentModels:   []codexapp.ModelOption{models[1]},
			SelectedModel:  "gpt-5-codex",
			ModelIndex:     1,
			RecentIndex:    0,
			Focus:          codexModelPickerFocusRecent,
		},
	}

	rendered := ansi.Strip(m.renderCodexModelPickerContent(80, 24))
	if !strings.Contains(rendered, "> GPT-5 Codex") {
		t.Fatalf("rendered picker should mark the focused recent row with >: %q", rendered)
	}
	if !strings.Contains(rendered, "* GPT-5 Codex") {
		t.Fatalf("rendered picker should keep the model list row as secondary selection: %q", rendered)
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

func TestAddPickerProjectHintFallsBackToPathBase(t *testing.T) {
	m := Model{codexPickerKind: codexPickerKindGlobal}
	row := ansi.Strip(m.renderCodexPickerRow(codexSessionChoice{
		Provider:     codexapp.ProviderOpenCode,
		SessionID:    "thread-old",
		ProjectName:  "Demo App",
		LastActivity: time.Date(2026, 3, 13, 19, 56, 0, 0, time.UTC),
		Summary:      "OpenCode session restored.",
		ProjectPath:  "/tmp/demo",
	}, false, 100))
	if !strings.Contains(row, "[Demo App]") {
		t.Fatalf("project hint should be shown on global picker rows: %q", row)
	}

	row = ansi.Strip(m.renderCodexPickerRow(codexSessionChoice{
		Provider:     codexapp.ProviderOpenCode,
		SessionID:    "thread-old",
		LastActivity: time.Date(2026, 3, 13, 19, 56, 0, 0, time.UTC),
		Summary:      "OpenCode session restored.",
		ProjectPath:  "/tmp/demo",
	}, false, 100))
	if !strings.Contains(row, "[demo]") {
		t.Fatalf("project hint should show path base when name is missing: %q", row)
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

func TestEmbeddedModelPreferencePersistsAcrossFutureSessionsPerProvider(t *testing.T) {
	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider: req.Provider.Normalized(),
				Started:  true,
				Preset:   req.Preset,
				Status:   req.Provider.Label() + " session ready",
			},
		}, nil
	})

	m := Model{
		codexManager: manager,
		projects: []model.ProjectSummary{{
			Path:          "/tmp/demo",
			Name:          "demo",
			PresentOnDisk: true,
		}},
		selected:      0,
		codexInput:    newCodexTextarea(),
		codexDrafts:   make(map[string]codexDraft),
		codexViewport: viewport.New(0, 0),
		width:         100,
		height:        24,
	}

	updated, _ := m.Update(codexActionMsg{
		projectPath: "/tmp/demo",
		status:      "Embedded model set to gpt-5.4 with high reasoning for the next prompt",
		provider:    codexapp.ProviderCodex,
		model:       "gpt-5.4",
		reasoning:   "high",
	})
	m = updated.(Model)

	updated, cmd := m.launchEmbeddedForSelection(codexapp.ProviderCodex, true, "")
	m = updated.(Model)
	if cmd == nil {
		t.Fatalf("launchEmbeddedForSelection(codex) should return an open command")
	}
	msg := cmd()
	if opened, ok := msg.(codexSessionOpenedMsg); !ok || opened.err != nil {
		t.Fatalf("codex open msg = %#v, want successful codexSessionOpenedMsg", msg)
	}

	updated, _ = m.Update(codexActionMsg{
		projectPath: "/tmp/demo",
		status:      "Embedded model set to openai/gpt-5.4 with medium reasoning for the next prompt",
		provider:    codexapp.ProviderOpenCode,
		model:       "openai/gpt-5.4",
		reasoning:   "medium",
	})
	m = updated.(Model)

	updated, cmd = m.launchEmbeddedForSelection(codexapp.ProviderOpenCode, true, "")
	m = updated.(Model)
	if cmd == nil {
		t.Fatalf("launchEmbeddedForSelection(opencode) should return an open command")
	}
	msg = cmd()
	if opened, ok := msg.(codexSessionOpenedMsg); !ok || opened.err != nil {
		t.Fatalf("opencode open msg = %#v, want successful codexSessionOpenedMsg", msg)
	}

	updated, _ = m.Update(codexActionMsg{
		projectPath: "/tmp/demo",
		status:      "Embedded model set to sonnet with max reasoning for the next prompt",
		provider:    codexapp.ProviderClaudeCode,
		model:       "sonnet",
		reasoning:   "max",
	})
	m = updated.(Model)

	updated, cmd = m.launchEmbeddedForSelection(codexapp.ProviderClaudeCode, true, "")
	m = updated.(Model)
	if cmd == nil {
		t.Fatalf("launchEmbeddedForSelection(claude) should return an open command")
	}
	msg = cmd()
	if opened, ok := msg.(codexSessionOpenedMsg); !ok || opened.err != nil {
		t.Fatalf("claude open msg = %#v, want successful codexSessionOpenedMsg", msg)
	}

	if len(requests) != 3 {
		t.Fatalf("request count = %d, want 3", len(requests))
	}
	if requests[0].Provider != codexapp.ProviderCodex || requests[0].PendingModel != "gpt-5.4" || requests[0].PendingReasoning != "high" {
		t.Fatalf("codex request = %#v, want persisted Codex model preference", requests[0])
	}
	if requests[1].Provider != codexapp.ProviderOpenCode || requests[1].PendingModel != "openai/gpt-5.4" || requests[1].PendingReasoning != "medium" {
		t.Fatalf("opencode request = %#v, want persisted OpenCode model preference", requests[1])
	}
	if requests[2].Provider != codexapp.ProviderClaudeCode || requests[2].PendingModel != "sonnet" || requests[2].PendingReasoning != "max" {
		t.Fatalf("claude request = %#v, want persisted Claude model preference", requests[2])
	}
}

func TestTodoDialogEnterStartsFreshPreferredProviderWithDraft(t *testing.T) {
	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider: req.Provider.Normalized(),
				ThreadID: "ses-todo",
				Started:  true,
				Preset:   req.Preset,
				Status:   req.Provider.Label() + " session ready",
			},
		}, nil
	})

	todoText := "Change font color to red when there's an error\n\nStack:\n- check logger context\n"
	m := Model{
		codexManager: manager,
		projects: []model.ProjectSummary{{
			Path:                "/tmp/demo",
			Name:                "demo",
			PresentOnDisk:       true,
			LatestSessionFormat: "opencode_db",
		}},
		detail: model.ProjectDetail{
			Summary: model.ProjectSummary{Path: "/tmp/demo"},
			Todos: []model.TodoItem{{
				ID:          7,
				ProjectPath: "/tmp/demo",
				Text:        todoText,
			}},
		},
		selected:      0,
		todoDialog:    &todoDialogState{ProjectPath: "/tmp/demo", ProjectName: "demo"},
		codexInput:    newCodexTextarea(),
		codexDrafts:   make(map[string]codexDraft),
		codexViewport: viewport.New(0, 0),
		width:         100,
		height:        24,
	}

	updated, _ := m.updateTodoDialogMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if got.todoDialog == nil {
		t.Fatalf("todo dialog should still be open after Enter (shows copy dialog)")
	}
	if got.todoCopyDialog == nil {
		t.Fatalf("todo copy dialog should open after Enter")
	}
	if got.todoCopyDialog.RunMode != todoCopyModeNewWorktree {
		t.Fatalf("copy dialog run mode = %d, want %d by default", got.todoCopyDialog.RunMode, todoCopyModeNewWorktree)
	}

	updated, _ = got.updateTodoCopyDialogMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}})
	got = updated.(Model)
	if got.todoCopyDialog.RunMode != todoCopyModeHere {
		t.Fatalf("copy dialog run mode = %d, want %d after w", got.todoCopyDialog.RunMode, todoCopyModeHere)
	}

	updated, cmd := got.updateTodoCopyDialogMode(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if got.todoDialog != nil {
		t.Fatalf("todo dialog should close when starting the selected TODO")
	}
	if got.codexPendingOpen == nil || got.codexPendingOpen.provider != codexapp.ProviderOpenCode {
		t.Fatalf("codexPendingOpen = %#v, want pending OpenCode session", got.codexPendingOpen)
	}
	if got.codexDrafts["/tmp/demo"].Text != todoText {
		t.Fatalf("draft text = %q, want selected TODO text", got.codexDrafts["/tmp/demo"].Text)
	}
	if cmd == nil {
		t.Fatalf("starting a TODO should return an open command")
	}

	msg := cmd()
	opened, ok := msg.(codexSessionOpenedMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want codexSessionOpenedMsg", msg)
	}
	if opened.err != nil {
		t.Fatalf("todo launch returned error = %v", opened.err)
	}
	if len(requests) != 1 {
		t.Fatalf("request count = %d, want 1", len(requests))
	}
	if requests[0].Provider != codexapp.ProviderOpenCode || !requests[0].ForceNew || strings.TrimSpace(requests[0].Prompt) != "" {
		t.Fatalf("launch request = %#v, want fresh OpenCode launch without auto-sent prompt", requests[0])
	}

	updated, cmd = got.Update(opened)
	got = updated.(Model)
	if got.codexVisibleProject != "/tmp/demo" {
		t.Fatalf("codexVisibleProject = %q, want /tmp/demo", got.codexVisibleProject)
	}
	if got.codexInput.Value() != todoText {
		t.Fatalf("composer text = %q, want selected TODO draft", got.codexInput.Value())
	}
	if got.status != "Fresh OpenCode session ready with TODO draft. Edit and press Enter to send." {
		t.Fatalf("status = %q, want todo draft ready status", got.status)
	}
	if cmd == nil {
		t.Fatalf("opening the embedded session should return a focus command")
	}
}

func TestTodoDialogModelToggleOpensPickerBeforeDraft(t *testing.T) {
	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider: req.Provider.Normalized(),
				ThreadID: "ses-model-toggle",
				Started:  true,
				Preset:   req.Preset,
				Status:   req.Provider.Label() + " session ready",
			},
			models: []codexapp.ModelOption{{
				ID:          "gpt-5",
				Model:       "gpt-5",
				DisplayName: "GPT-5",
				IsDefault:   true,
			}},
		}, nil
	})

	m := Model{
		codexManager: manager,
		projects: []model.ProjectSummary{{
			Path:                "/tmp/demo",
			Name:                "demo",
			PresentOnDisk:       true,
			LatestSessionFormat: "codex",
		}},
		detail: model.ProjectDetail{
			Summary: model.ProjectSummary{Path: "/tmp/demo"},
			Todos: []model.TodoItem{{
				ID:          11,
				ProjectPath: "/tmp/demo",
				Text:        "Check model toggle behavior",
			}},
		},
		selected:      0,
		todoDialog:    &todoDialogState{ProjectPath: "/tmp/demo", ProjectName: "demo"},
		codexInput:    newCodexTextarea(),
		codexDrafts:   make(map[string]codexDraft),
		codexViewport: viewport.New(0, 0),
		width:         100,
		height:        24,
	}

	updated, _ := m.updateTodoDialogMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if got.todoCopyDialog == nil {
		t.Fatalf("todo copy dialog should open after Enter")
	}
	updated, _ = got.updateTodoCopyDialogMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}})
	got = updated.(Model)
	if got.todoCopyDialog.RunMode != todoCopyModeHere {
		t.Fatalf("copy dialog run mode = %d, want %d after w", got.todoCopyDialog.RunMode, todoCopyModeHere)
	}
	updated, _ = got.updateTodoCopyDialogMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	got = updated.(Model)
	if !got.todoCopyDialog.OpenModelFirst {
		t.Fatalf("copy dialog should enable model toggle after m")
	}

	updated, cmd := got.updateTodoCopyDialogMode(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if cmd == nil {
		t.Fatalf("starting a TODO should return an open command")
	}

	msg := cmd()
	opened, ok := msg.(codexSessionOpenedMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want codexSessionOpenedMsg", msg)
	}
	if opened.err != nil {
		t.Fatalf("todo launch returned error = %v", opened.err)
	}
	if len(requests) != 1 {
		t.Fatalf("request count = %d, want 1", len(requests))
	}

	updated, cmd = got.Update(opened)
	got = updated.(Model)
	if got.codexModelPicker == nil || !got.codexModelPicker.Loading {
		t.Fatalf("model picker should enter loading state when m is enabled")
	}
	if got.status != "Pick a model, then send the TODO draft." {
		t.Fatalf("status = %q, want model picker guidance", got.status)
	}
	if cmd == nil {
		t.Fatalf("opening the embedded session should return a model picker command")
	}
}

func TestTodoModelPickerApplyRefocusesComposerAndCapturesSettleLatencyImmediately(t *testing.T) {
	now := time.Date(2026, time.April, 2, 17, 30, 0, 0, time.UTC)
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Provider:        codexapp.ProviderOpenCode,
			ThreadID:        "ses-model-focus",
			Started:         true,
			Preset:          codexcli.PresetYolo,
			Status:          "OpenCode ready",
			Model:           "openai/gpt-4.1",
			ReasoningEffort: "",
		},
		models: []codexapp.ModelOption{{
			ID:          "openai/gpt-5.4",
			Model:       "openai/gpt-5.4",
			DisplayName: "GPT-5.4",
			Description: "Fast enough",
			IsDefault:   true,
		}},
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

	m := Model{
		codexManager: manager,
		codexInput:   newCodexTextarea(),
		codexDrafts: map[string]codexDraft{
			"/tmp/demo": {Text: "Line one\nLine two"},
		},
		codexViewport: viewport.New(0, 0),
		width:         100,
		height:        28,
		nowFn:         func() time.Time { return now },
		todoLaunchDrafts: map[string]todoLaunchDraftState{
			"/tmp/demo": {
				projectPath:    "/tmp/demo",
				provider:       codexapp.ProviderOpenCode,
				openModelFirst: true,
			},
		},
	}

	updated, cmd := m.update(codexSessionOpenedMsg{
		projectPath: "/tmp/demo",
		status:      "OpenCode session ready",
	})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("session open should return the model picker command")
	}
	if got.codexInput.Focused() {
		t.Fatalf("composer should not be focused while the model picker is taking over")
	}
	if got.codexInput.Value() != "Line one\nLine two" {
		t.Fatalf("composer text = %q, want preserved multiline TODO draft", got.codexInput.Value())
	}

	msg := cmd()
	listMsg, ok := msg.(codexModelListMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want codexModelListMsg", msg)
	}
	updated, _ = got.update(listMsg)
	got = updated.(Model)
	if got.codexModelPicker == nil || got.codexModelPicker.Loading {
		t.Fatalf("model picker should be loaded")
	}

	updated, _ = got.updateCodexModelPickerMode(tea.KeyMsg{Type: tea.KeyTab})
	got = updated.(Model)
	if got.codexModelPicker.Focus != codexModelPickerFocusModels {
		t.Fatalf("picker focus = %q, want models", got.codexModelPicker.Focus)
	}
	session.trySnapshotCalls = 0
	session.snapshotCalls = 0

	updated, cmd = got.updateCodexModelPickerMode(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if cmd == nil {
		t.Fatalf("enter should apply the selected model")
	}

	msg = cmd()
	action, ok := msg.(codexActionMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want codexActionMsg", msg)
	}
	if !action.awaitSettle {
		t.Fatalf("model apply should wait for a snapshot settle event")
	}

	updated, cmd = got.update(action)
	got = updated.(Model)
	if cmd == nil {
		t.Fatalf("successful model apply should save preferences and refocus the composer")
	}
	if !got.codexInput.Focused() {
		t.Fatalf("composer should regain focus after applying the model picker selection")
	}
	if session.trySnapshotCalls != 0 {
		t.Fatalf("model apply should not probe the live session snapshot on the UI thread; TrySnapshot calls = %d", session.trySnapshotCalls)
	}
	if _, ok := got.modelSettlePending["/tmp/demo"]; ok {
		t.Fatalf("model settle should complete immediately after the staged snapshot refresh")
	}
	if len(got.aiLatencyInFlightSnapshot()) != 0 {
		t.Fatalf("in-flight latency ops = %#v, want none after the immediate settle refresh", got.aiLatencyInFlightSnapshot())
	}

	found := false
	for _, sample := range got.aiLatencyRecent {
		if sample.Name != "Model settle" {
			continue
		}
		found = true
		if sample.ProjectPath != "/tmp/demo" {
			t.Fatalf("model settle project = %q, want /tmp/demo", sample.ProjectPath)
		}
		if sample.Duration != 0 {
			t.Fatalf("model settle duration = %v, want immediate completion", sample.Duration)
		}
	}
	if !found {
		t.Fatalf("recent latency samples = %#v, want a completed model settle sample", got.aiLatencyRecent)
	}
}

func TestCodexUpdateAfterModelApplyDefersLiveSnapshotRefresh(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Provider:        codexapp.ProviderCodex,
			ThreadID:        "thread-demo",
			Started:         true,
			Status:          "Codex ready",
			Model:           "gpt-5",
			ReasoningEffort: "medium",
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
	if _, ok, _ := m.refreshCodexSnapshot("/tmp/demo"); !ok {
		t.Fatalf("refreshCodexSnapshot() failed")
	}
	session.trySnapshotCalls = 0
	session.snapshotCalls = 0

	updated, _ := m.Update(codexActionMsg{
		projectPath: "/tmp/demo",
		status:      "Embedded model set to gpt-5.4 with high reasoning for the next prompt",
		provider:    codexapp.ProviderCodex,
		model:       "gpt-5.4",
		reasoning:   "high",
		awaitSettle: true,
	})
	got := updated.(Model)
	if session.trySnapshotCalls != 0 || session.snapshotCalls != 0 {
		t.Fatalf("model apply should avoid live snapshot reads on the UI thread; TrySnapshot/Snapshot calls = %d/%d", session.trySnapshotCalls, session.snapshotCalls)
	}

	updated, _ = got.Update(codexUpdateMsg{projectPath: "/tmp/demo"})
	got = updated.(Model)
	if session.trySnapshotCalls != 0 || session.snapshotCalls != 0 {
		t.Fatalf("the first update after model apply should defer live snapshot refresh off the UI thread; TrySnapshot/Snapshot calls = %d/%d", session.trySnapshotCalls, session.snapshotCalls)
	}
	if _, ok := got.codexSkipNextLiveRefresh["/tmp/demo"]; ok {
		t.Fatalf("codexUpdateMsg should consume the deferred-refresh hint")
	}
}

func TestCodexUpdateBusyToIdleSettlesTurnAndRefreshesProjectStatus(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	projectPath := filepath.Join(t.TempDir(), "repo")
	runTUITestGit(t, "", "init", projectPath)
	runTUITestGit(t, projectPath, "config", "user.name", "Little Control Room Tests")
	runTUITestGit(t, projectPath, "config", "user.email", "tests@example.com")
	if err := os.WriteFile(filepath.Join(projectPath, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write README.md: %v", err)
	}
	runTUITestGit(t, projectPath, "add", "README.md")
	runTUITestGit(t, projectPath, "commit", "-m", "initial commit")

	now := time.Now().UTC().Truncate(time.Second)
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	sessionEvidence := model.NormalizeSessionEvidenceIdentity(model.SessionEvidence{
		Source:               model.SessionSourceCodex,
		SessionID:            "codex:thread-demo",
		RawSessionID:         "thread-demo",
		ProjectPath:          projectPath,
		DetectedProjectPath:  projectPath,
		Format:               "modern",
		StartedAt:            now.Add(-10 * time.Minute),
		LastEventAt:          now.Add(-time.Minute),
		LatestTurnStartedAt:  now.Add(-2 * time.Minute),
		LatestTurnStateKnown: true,
		LatestTurnCompleted:  false,
	})
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:          projectPath,
		Name:          "repo",
		LastActivity:  now.Add(-time.Minute),
		Status:        model.StatusActive,
		PresentOnDisk: true,
		InScope:       true,
		RepoBranch:    "master",
		RepoDirty:     false,
		Sessions:      []model.SessionEvidence{sessionEvidence},
		CreatedAt:     now.Add(-2 * time.Hour),
		UpdatedAt:     now.Add(-time.Minute),
	}); err != nil {
		t.Fatalf("seed project state: %v", err)
	}

	if err := os.WriteFile(filepath.Join(projectPath, "README.md"), []byte("hello\nchanged\n"), 0o644); err != nil {
		t.Fatalf("update README.md: %v", err)
	}

	session := &fakeCodexSession{
		projectPath: projectPath,
		snapshot: codexapp.Snapshot{
			Provider:       codexapp.ProviderCodex,
			ProjectPath:    projectPath,
			ThreadID:       "thread-demo",
			Started:        true,
			Busy:           false,
			BusySince:      now.Add(-2 * time.Minute),
			LastActivityAt: now,
			Status:         "Codex ready",
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: projectPath,
		Preset:      codexcli.PresetYolo,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	svc := service.New(config.Default(), st, events.NewBus(), nil)
	m := Model{
		ctx:          ctx,
		svc:          svc,
		codexManager: manager,
		projects: []model.ProjectSummary{{
			Path:          projectPath,
			Name:          "repo",
			PresentOnDisk: true,
			RepoBranch:    "master",
		}},
		allProjects: []model.ProjectSummary{{
			Path:          projectPath,
			Name:          "repo",
			PresentOnDisk: true,
			RepoBranch:    "master",
		}},
		selected: 0,
		detail: model.ProjectDetail{
			Summary: model.ProjectSummary{
				Path:          projectPath,
				Name:          "repo",
				PresentOnDisk: true,
				RepoBranch:    "master",
			},
		},
		codexSnapshots: map[string]codexapp.Snapshot{
			projectPath: {
				Provider:           codexapp.ProviderCodex,
				ProjectPath:        projectPath,
				ThreadID:           "thread-demo",
				Started:            true,
				Busy:               true,
				BusySince:          now.Add(-2 * time.Minute),
				LastBusyActivityAt: now.Add(-30 * time.Second),
				LastActivityAt:     now.Add(-30 * time.Second),
			},
		},
		codexInput:      newCodexTextarea(),
		detailViewport:  viewport.New(20, 5),
		runtimeViewport: viewport.New(20, 5),
		width:           100,
		height:          24,
	}

	updated, cmd := m.Update(codexUpdateMsg{projectPath: projectPath})
	got := updated.(Model)
	if cmd == nil {
		t.Fatal("busy-to-idle update should queue follow-up work")
	}
	raw := cmd()
	if raw == nil {
		t.Fatal("busy-to-idle update should return follow-up messages")
	}
	if batch, ok := raw.(tea.BatchMsg); ok {
		found := false
		for i := len(batch) - 1; i >= 0; i-- {
			if batch[i] == nil {
				continue
			}
			msg := batch[i]()
			refreshMsg, ok := msg.(projectStatusRefreshedMsg)
			if !ok {
				continue
			}
			updated, followUp := got.Update(refreshMsg)
			got = updated.(Model)
			got = drainCmdMsgs(got, followUp)
			found = true
			break
		}
		if !found {
			t.Fatalf("busy-to-idle batch should include projectStatusRefreshedMsg, got %#v", raw)
		}
	} else {
		updated, followUp := got.Update(raw)
		got = updated.(Model)
		got = drainCmdMsgs(got, followUp)
	}

	if !got.detail.Summary.RepoDirty {
		t.Fatalf("detail repo dirty = %t, want refreshed dirty state", got.detail.Summary.RepoDirty)
	}
	if !got.detail.Summary.LatestTurnStateKnown || !got.detail.Summary.LatestTurnCompleted {
		t.Fatalf("detail turn state = known:%t completed:%t, want settled turn", got.detail.Summary.LatestTurnStateKnown, got.detail.Summary.LatestTurnCompleted)
	}

	detail, err := st.GetProjectDetail(ctx, projectPath, 20)
	if err != nil {
		t.Fatalf("get detail after settle refresh: %v", err)
	}
	if !detail.Summary.RepoDirty {
		t.Fatalf("store detail should reflect dirty repo after settle refresh, got %#v", detail.Summary)
	}
	if len(detail.Sessions) == 0 || !detail.Sessions[0].LatestTurnStateKnown || !detail.Sessions[0].LatestTurnCompleted {
		t.Fatalf("stored session turn state = %#v, want settled turn", detail.Sessions)
	}
}

func TestSyncCodexViewportRecordsSharedStageLatencies(t *testing.T) {
	projectPath := "/tmp/demo"
	snapshot := codexapp.Snapshot{
		Provider: codexapp.ProviderOpenCode,
		Closed:   true,
		Entries: []codexapp.TranscriptEntry{{
			Kind: codexapp.TranscriptAgent,
			Text: "hello",
		}},
	}

	now := time.Date(2026, time.April, 3, 11, 0, 0, 0, time.UTC)
	ticks := 0
	m := Model{
		codexVisibleProject: projectPath,
		codexHiddenProject:  projectPath,
		codexInput:          newCodexTextarea(),
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              24,
		nowFn: func() time.Time {
			value := now.Add(time.Duration(ticks) * 200 * time.Millisecond)
			ticks++
			return value
		},
	}
	m.storeCodexSnapshot(projectPath, snapshot)

	m.syncCodexViewport(true)

	gotNames := map[string]bool{}
	for _, sample := range m.aiLatencyRecent {
		gotNames[sample.Name] = true
	}
	for _, want := range []string{
		"Embedded lower blocks",
		"Embedded transcript render",
		"Embedded viewport content",
		"Embedded viewport sync",
	} {
		if !gotNames[want] {
			t.Fatalf("syncCodexViewport() missing latency sample %q, got %#v", want, m.aiLatencyRecent)
		}
	}
}

func TestCodexUpdateStatusOnlyPreservesViewportOffset(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Provider: codexapp.ProviderCodex,
			Started:  true,
			Status:   "Codex session ready",
			Entries: []codexapp.TranscriptEntry{{
				Kind: codexapp.TranscriptAgent,
				Text: strings.Join([]string{
					"line 01", "line 02", "line 03", "line 04", "line 05", "line 06", "line 07", "line 08",
					"line 09", "line 10", "line 11", "line 12", "line 13", "line 14", "line 15", "line 16",
					"line 17", "line 18", "line 19", "line 20", "line 21", "line 22", "line 23", "line 24",
					"line 25", "line 26", "line 27", "line 28", "line 29", "line 30", "line 31", "line 32",
				}, "\n"),
			}},
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{ProjectPath: "/tmp/demo"}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          newCodexTextarea(),
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              18,
	}
	if _, ok, _ := m.refreshCodexSnapshot("/tmp/demo"); !ok {
		t.Fatalf("refreshCodexSnapshot() failed")
	}
	m.syncCodexViewport(true)
	m.codexViewport.SetYOffset(1)

	session.snapshot.Status = "Codex status changed only"

	updated, _ := m.Update(codexUpdateMsg{projectPath: "/tmp/demo"})
	got := updated.(Model)
	if got.codexViewport.YOffset != 1 {
		t.Fatalf("status-only codex update should preserve viewport offset, got %d", got.codexViewport.YOffset)
	}
}

func TestCodexPageKeysScrollTranscriptByEightyPercent(t *testing.T) {
	vp := viewport.New(80, 10)
	vp.SetContent(testViewportLines(40))
	m := Model{
		codexVisibleProject: "/tmp/demo",
		codexSnapshots: map[string]codexapp.Snapshot{
			"/tmp/demo": {Provider: codexapp.ProviderCodex, ProjectPath: "/tmp/demo", Started: true},
		},
		codexViewport: vp,
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyPgDown})
	if cmd != nil {
		t.Fatalf("Page Down should not return a command")
	}
	got := updated.(Model)
	if got.codexViewport.YOffset != 8 {
		t.Fatalf("Page Down offset = %d, want 8", got.codexViewport.YOffset)
	}

	updated, cmd = got.updateCodexMode(tea.KeyMsg{Type: tea.KeyPgUp})
	if cmd != nil {
		t.Fatalf("Page Up should not return a command")
	}
	got = updated.(Model)
	if got.codexViewport.YOffset != 0 {
		t.Fatalf("Page Up offset = %d, want 0", got.codexViewport.YOffset)
	}
}

func testViewportLines(count int) string {
	lines := make([]string, count)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %02d", i)
	}
	return strings.Join(lines, "\n")
}

func TestCodexUpdateStatusOnlyBrowserPanelKeepsBottomAnchored(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Provider: codexapp.ProviderCodex,
			Started:  true,
			Status:   "Codex session ready",
			Entries: []codexapp.TranscriptEntry{{
				Kind: codexapp.TranscriptAgent,
				Text: strings.Join([]string{
					"line 01", "line 02", "line 03", "line 04", "line 05", "line 06", "line 07", "line 08",
					"line 09", "line 10", "line 11", "line 12", "line 13", "line 14", "line 15", "line 16",
					"line 17", "line 18", "line 19", "line 20", "line 21", "line 22", "line 23", "line 24",
					"line 25", "line 26", "line 27", "line 28", "line 29", "line 30", "line 31", "line 32",
				}, "\n"),
			}},
			BrowserActivity:          browserctl.SessionActivity{Policy: settingsAutomaticPlaywrightPolicy},
			ManagedBrowserSessionKey: "managed-demo",
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{ProjectPath: "/tmp/demo"}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	settings := config.EditableSettings{PlaywrightPolicy: settingsAutomaticPlaywrightPolicy}
	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          newCodexTextarea(),
		codexViewport:       viewport.New(0, 0),
		settingsBaseline:    &settings,
		width:               48,
		height:              20,
	}
	if _, ok, _ := m.refreshCodexSnapshot("/tmp/demo"); !ok {
		t.Fatalf("refreshCodexSnapshot() failed")
	}
	m.syncCodexViewport(true)
	if !m.codexViewport.AtBottom() {
		t.Fatalf("initial sync should start at the bottom")
	}
	beforeLowerBlocks := m.codexLowerBlocks(session.snapshot, m.width)
	beforeYOffset := m.codexViewport.YOffset
	beforeHeight := m.codexViewport.Height
	beforeLowerHeight := countRenderedBlockLines(beforeLowerBlocks)

	session.snapshot.Status = "Codex status changed only"
	session.snapshot.ManagedBrowserSessionKey = ""

	updated, _ := m.Update(codexUpdateMsg{projectPath: "/tmp/demo"})
	got := updated.(Model)
	afterSnapshot, ok := got.currentCodexSnapshot()
	if !ok {
		t.Fatalf("currentCodexSnapshot() unavailable after update")
	}
	afterLowerBlocks := got.codexLowerBlocks(afterSnapshot, got.width)
	afterLowerHeight := countRenderedBlockLines(afterLowerBlocks)
	if afterLowerHeight <= beforeLowerHeight {
		t.Fatalf("test fixture should add browser panel rows, before=%d after=%d before=%q after=%q", beforeLowerHeight, afterLowerHeight, strings.Join(beforeLowerBlocks, "\n---\n"), strings.Join(afterLowerBlocks, "\n---\n"))
	}
	if got.codexViewport.Height >= beforeHeight {
		t.Fatalf("browser panel should reduce transcript height, before=%d after=%d", beforeHeight, got.codexViewport.Height)
	}
	if !got.codexViewport.AtBottom() {
		maxOffset := max(0, got.codexViewport.TotalLineCount()-got.codexViewport.Height)
		t.Fatalf("status-only browser update should keep transcript pinned to bottom, offset=%d max=%d", got.codexViewport.YOffset, maxOffset)
	}
	if got.codexViewport.YOffset <= beforeYOffset {
		t.Fatalf("browser panel should advance viewport offset to preserve the bottom, before=%d after=%d", beforeYOffset, got.codexViewport.YOffset)
	}
}

func TestTodoDialogCopyDialogIncludesClaudeAndDefaultsToClaudeProvider(t *testing.T) {
	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider: req.Provider.Normalized(),
				ThreadID: "ses-claude",
				Started:  true,
				Preset:   req.Preset,
				Status:   req.Provider.Label() + " session ready",
			},
		}, nil
	})

	m := Model{
		codexManager: manager,
		projects: []model.ProjectSummary{{
			Path:                "/tmp/demo",
			Name:                "demo",
			PresentOnDisk:       true,
			LatestSessionFormat: "claude_code",
		}},
		detail: model.ProjectDetail{
			Summary: model.ProjectSummary{Path: "/tmp/demo"},
			Todos: []model.TodoItem{{
				ID:          9,
				ProjectPath: "/tmp/demo",
				Text:        "Investigate the TODO dialog launch provider list",
				WorktreeSuggestion: &model.TodoWorktreeSuggestion{
					Status:         model.TodoWorktreeSuggestionReady,
					BranchName:     "feat/todo-worktree-launch",
					WorktreeSuffix: "feat-todo-worktree-launch",
				},
			}},
		},
		selected:      0,
		todoDialog:    &todoDialogState{ProjectPath: "/tmp/demo", ProjectName: "demo"},
		codexInput:    newCodexTextarea(),
		codexDrafts:   make(map[string]codexDraft),
		codexViewport: viewport.New(0, 0),
		width:         100,
		height:        24,
	}

	updated, _ := m.updateTodoDialogMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if got.todoCopyDialog == nil {
		t.Fatalf("todo copy dialog should open after Enter")
	}
	if got.todoCopyDialog.Provider != codexapp.ProviderClaudeCode {
		t.Fatalf("copy dialog provider = %q, want %q", got.todoCopyDialog.Provider, codexapp.ProviderClaudeCode)
	}
	if got.todoCopyDialog.RunMode != todoCopyModeNewWorktree {
		t.Fatalf("copy dialog run mode = %d, want %d", got.todoCopyDialog.RunMode, todoCopyModeNewWorktree)
	}

	rendered := ansi.Strip(got.renderTodoCopyDialogOverlay("", 100, 24))
	if !strings.Contains(rendered, "Run in") {
		t.Fatalf("rendered copy dialog = %q, want run mode section", rendered)
	}
	if !strings.Contains(rendered, "Dedicated worktree") {
		t.Fatalf("rendered copy dialog = %q, want dedicated worktree option", rendered)
	}
	if !strings.Contains(rendered, "Claude Code") {
		t.Fatalf("rendered copy dialog = %q, want Claude Code provider option", rendered)
	}
	if !strings.Contains(rendered, "Run in  [w]") {
		t.Fatalf("rendered copy dialog = %q, want dedicated worktree hotkey hint", rendered)
	}
	if !strings.Contains(rendered, "Branch: feat/todo-worktree-launch") {
		t.Fatalf("rendered copy dialog = %q, want ready worktree branch details by default", rendered)
	}
	if !strings.Contains(rendered, "Agent  [a]") {
		t.Fatalf("rendered copy dialog = %q, want agent hotkey hint", rendered)
	}
	if !strings.Contains(rendered, "Options") {
		t.Fatalf("rendered copy dialog = %q, want options column", rendered)
	}
	if strings.Contains(rendered, "Options  [m]") {
		t.Fatalf("rendered copy dialog = %q, should not show options hotkey badge", rendered)
	}
	if !strings.Contains(rendered, "change model") {
		t.Fatalf("rendered copy dialog = %q, want model toggle row", rendered)
	}
	lines := strings.Split(rendered, "\n")
	foundEnterLine := false
	for _, line := range lines {
		if strings.Contains(line, "change model") && strings.Contains(line, "Enter") && strings.Contains(line, "start") {
			t.Fatalf("rendered copy dialog should keep Enter on its own action row, got %q", line)
		}
		if strings.Contains(line, "Enter") && strings.Contains(line, "start") {
			foundEnterLine = true
			if !strings.Contains(line, "Esc") || !strings.Contains(line, "cancel") {
				t.Fatalf("rendered copy dialog Enter row should also include Esc cancel, got %q", line)
			}
		}
	}
	if !foundEnterLine {
		t.Fatalf("rendered copy dialog should include a dedicated Enter/Esc action row, got %q", rendered)
	}

	updated, _ = got.updateTodoCopyDialogMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}})
	got = updated.(Model)
	if got.todoCopyDialog.RunMode != todoCopyModeHere {
		t.Fatalf("copy dialog run mode = %d, want %d after w", got.todoCopyDialog.RunMode, todoCopyModeHere)
	}

	updated, _ = got.updateTodoCopyDialogMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}})
	got = updated.(Model)
	rendered = ansi.Strip(got.renderTodoCopyDialogOverlay("", 100, 24))
	if !strings.Contains(rendered, "Branch: feat/todo-worktree-launch") {
		t.Fatalf("rendered copy dialog = %q, want ready worktree branch details", rendered)
	}
	if !strings.Contains(rendered, "Source: cached AI suggestion") {
		t.Fatalf("rendered copy dialog = %q, want worktree suggestion source details", rendered)
	}
	if !strings.Contains(rendered, "Path: /tmp/demo--feat-todo-worktree-launch") {
		t.Fatalf("rendered copy dialog = %q, want suggested worktree path details", rendered)
	}

	updated, _ = got.updateTodoCopyDialogMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}})
	got = updated.(Model)
	if got.todoCopyDialog.RunMode != todoCopyModeHere {
		t.Fatalf("copy dialog run mode = %d, want %d before Enter", got.todoCopyDialog.RunMode, todoCopyModeHere)
	}

	updated, cmd := got.updateTodoCopyDialogMode(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if got.codexPendingOpen == nil || got.codexPendingOpen.provider != codexapp.ProviderClaudeCode {
		t.Fatalf("codexPendingOpen = %#v, want pending Claude Code session", got.codexPendingOpen)
	}
	if cmd == nil {
		t.Fatalf("starting the Claude TODO flow should return an open command")
	}

	msg := cmd()
	opened, ok := msg.(codexSessionOpenedMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want codexSessionOpenedMsg", msg)
	}
	if opened.err != nil {
		t.Fatalf("todo launch returned error = %v", opened.err)
	}
	if len(requests) != 1 {
		t.Fatalf("request count = %d, want 1", len(requests))
	}
	if requests[0].Provider != codexapp.ProviderClaudeCode || !requests[0].ForceNew {
		t.Fatalf("launch request = %#v, want fresh Claude Code launch", requests[0])
	}
}

func TestTodoDialogDedicatedWorktreeEnsuresMissingWorktreeSuggestion(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	projectPath := filepath.Join(root, "repo")
	runTUITestGit(t, "", "init", projectPath)

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})

	cfg := config.Default()
	cfg.AIBackend = config.AIBackendOpenAIAPI
	cfg.OpenAIAPIKey = "test-key"
	svc := service.New(cfg, st, events.NewBus(), nil)
	if _, err := svc.CreateOrAttachProject(ctx, service.CreateOrAttachProjectRequest{
		ParentPath: root,
		Name:       "repo",
	}); err != nil {
		t.Fatalf("track project: %v", err)
	}

	item, err := st.AddTodo(ctx, projectPath, "Launch this TODO in a new worktree")
	if err != nil {
		t.Fatalf("add todo: %v", err)
	}
	detail, err := st.GetProjectDetail(ctx, projectPath, 10)
	if err != nil {
		t.Fatalf("get project detail: %v", err)
	}

	m := Model{
		ctx: ctx,
		svc: svc,
		projects: []model.ProjectSummary{{
			Path:          projectPath,
			Name:          "repo",
			PresentOnDisk: true,
		}},
		detail:     detail,
		selected:   0,
		todoDialog: &todoDialogState{ProjectPath: projectPath, ProjectName: "repo"},
		width:      100,
		height:     24,
	}

	updated, cmd := m.updateTodoDialogMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if got.todoCopyDialog == nil {
		t.Fatalf("todo copy dialog should open after Enter")
	}
	if got.todoCopyDialog.RunMode != todoCopyModeNewWorktree {
		t.Fatalf("copy dialog run mode = %d, want %d by default", got.todoCopyDialog.RunMode, todoCopyModeNewWorktree)
	}
	if cmd == nil {
		t.Fatalf("opening the TODO launcher should ensure a missing worktree suggestion")
	}
	msg, ok := cmd().(todoActionMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want todoActionMsg", msg)
	}
	if msg.err != nil {
		t.Fatalf("todoActionMsg.err = %v, want nil", msg.err)
	}
	if msg.status != "Preparing worktree suggestion..." {
		t.Fatalf("todoActionMsg.status = %q, want preparing status", msg.status)
	}

	suggestion, err := st.GetTodoWorktreeSuggestion(ctx, item.ID)
	if err != nil {
		t.Fatalf("get todo worktree suggestion: %v", err)
	}
	if suggestion.Status != model.TodoWorktreeSuggestionQueued {
		t.Fatalf("suggestion.Status = %q, want %q", suggestion.Status, model.TodoWorktreeSuggestionQueued)
	}
}

func TestTodoDialogDedicatedWorktreeRetriesFailedWorktreeSuggestion(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	projectPath := filepath.Join(root, "repo")
	runTUITestGit(t, "", "init", projectPath)

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})

	cfg := config.Default()
	cfg.AIBackend = config.AIBackendOpenAIAPI
	cfg.OpenAIAPIKey = "test-key"
	svc := service.New(cfg, st, events.NewBus(), nil)
	if _, err := svc.CreateOrAttachProject(ctx, service.CreateOrAttachProjectRequest{
		ParentPath: root,
		Name:       "repo",
	}); err != nil {
		t.Fatalf("track project: %v", err)
	}

	item, err := st.AddTodo(ctx, projectPath, "Launch this TODO in a new worktree")
	if err != nil {
		t.Fatalf("add todo: %v", err)
	}
	queueTodoWorktreeSuggestionForTest(t, ctx, st, item.ID)
	suggestion, err := st.ClaimNextQueuedTodoWorktreeSuggestion(ctx, 0, 0)
	if err != nil {
		t.Fatalf("claim todo worktree suggestion: %v", err)
	}
	if failed, err := st.FailTodoWorktreeSuggestion(ctx, suggestion, "todo worktree suggestion missing branch_name"); err != nil {
		t.Fatalf("fail todo worktree suggestion: %v", err)
	} else if !failed {
		t.Fatalf("expected todo worktree suggestion to fail")
	}
	detail, err := st.GetProjectDetail(ctx, projectPath, 10)
	if err != nil {
		t.Fatalf("get project detail: %v", err)
	}

	m := Model{
		ctx: ctx,
		svc: svc,
		projects: []model.ProjectSummary{{
			Path:          projectPath,
			Name:          "repo",
			PresentOnDisk: true,
		}},
		detail:     detail,
		selected:   0,
		todoDialog: &todoDialogState{ProjectPath: projectPath, ProjectName: "repo"},
		width:      100,
		height:     24,
	}

	updated, cmd := m.updateTodoDialogMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if got.todoCopyDialog == nil {
		t.Fatalf("todo copy dialog should open after Enter")
	}
	if got.todoCopyDialog.RunMode != todoCopyModeNewWorktree {
		t.Fatalf("copy dialog run mode = %d, want %d by default", got.todoCopyDialog.RunMode, todoCopyModeNewWorktree)
	}
	if cmd == nil {
		t.Fatalf("opening the TODO launcher should retry a failed worktree suggestion")
	}
	msg, ok := cmd().(todoActionMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want todoActionMsg", msg)
	}
	if msg.err != nil {
		t.Fatalf("todoActionMsg.err = %v, want nil", msg.err)
	}
	if msg.status != "Preparing worktree suggestion..." {
		t.Fatalf("todoActionMsg.status = %q, want preparing status", msg.status)
	}

	retried, err := st.GetTodoWorktreeSuggestion(ctx, item.ID)
	if err != nil {
		t.Fatalf("get todo worktree suggestion: %v", err)
	}
	if retried.Status != model.TodoWorktreeSuggestionQueued {
		t.Fatalf("retried.Status = %q, want %q", retried.Status, model.TodoWorktreeSuggestionQueued)
	}
}

func TestTodoDialogCanStartSelectedTodoInNewWorktree(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	projectPath := filepath.Join(root, "repo")
	runTUITestGit(t, "", "init", projectPath)
	runTUITestGit(t, projectPath, "config", "user.name", "Little Control Room Tests")
	runTUITestGit(t, projectPath, "config", "user.email", "tests@example.com")
	if err := os.WriteFile(filepath.Join(projectPath, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write README.md: %v", err)
	}
	runTUITestGit(t, projectPath, "add", "README.md")
	runTUITestGit(t, projectPath, "commit", "-m", "initial commit")

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})

	svc := service.New(config.Default(), st, events.NewBus(), nil)
	if _, err := svc.CreateOrAttachProject(ctx, service.CreateOrAttachProjectRequest{
		ParentPath: root,
		Name:       "repo",
	}); err != nil {
		t.Fatalf("track project: %v", err)
	}

	todoText := "Launch this TODO in a new worktree\n\nUse log output:\nline 1\nline 2\n"
	item, err := svc.AddTodo(ctx, projectPath, todoText)
	if err != nil {
		t.Fatalf("add todo: %v", err)
	}
	queueTodoWorktreeSuggestionForTest(t, ctx, st, item.ID)
	suggestion, err := st.ClaimNextQueuedTodoWorktreeSuggestion(ctx, 0, 0)
	if err != nil {
		t.Fatalf("claim todo worktree suggestion: %v", err)
	}
	suggestion.BranchName = "feat/new-worktree-launch"
	suggestion.WorktreeSuffix = "feat-new-worktree-launch"
	suggestion.Kind = "feature"
	suggestion.Reason = "Implements new worktree launch."
	suggestion.Confidence = 0.91
	suggestion.Model = "test"
	if completed, err := st.CompleteTodoWorktreeSuggestion(ctx, suggestion); err != nil {
		t.Fatalf("complete todo worktree suggestion: %v", err)
	} else if !completed {
		t.Fatalf("expected todo worktree suggestion to complete")
	}

	detail, err := st.GetProjectDetail(ctx, projectPath, 10)
	if err != nil {
		t.Fatalf("get project detail: %v", err)
	}

	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider: req.Provider.Normalized(),
				ThreadID: "ses-worktree",
				Started:  true,
				Preset:   req.Preset,
				Status:   req.Provider.Label() + " session ready",
			},
		}, nil
	})

	m := Model{
		ctx:          ctx,
		svc:          svc,
		codexManager: manager,
		projects: []model.ProjectSummary{{
			Path:          projectPath,
			Name:          "repo",
			PresentOnDisk: true,
		}},
		detail:        detail,
		selected:      0,
		todoDialog:    &todoDialogState{ProjectPath: projectPath, ProjectName: "repo"},
		codexInput:    newCodexTextarea(),
		codexDrafts:   make(map[string]codexDraft),
		codexViewport: viewport.New(0, 0),
		width:         100,
		height:        24,
	}

	updated, _ := m.updateTodoDialogMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	updated, cmd := got.updateTodoCopyDialogMode(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if cmd == nil {
		t.Fatalf("starting the worktree TODO flow should return a command")
	}
	if got.todoDialog != nil {
		t.Fatalf("todo dialog should close as soon as the dedicated worktree launch starts")
	}
	if got.todoCopyDialog != nil {
		t.Fatalf("todo copy dialog should dismiss as soon as the dedicated worktree launch starts")
	}
	if got.status != "Starting TODO in dedicated worktree..." {
		t.Fatalf("status = %q, want immediate background-start message", got.status)
	}
	msg := cmd()
	launchMsg, ok := msg.(todoWorktreeLaunchMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want todoWorktreeLaunchMsg", msg)
	}
	if launchMsg.err != nil {
		t.Fatalf("worktree launch returned error = %v", launchMsg.err)
	}
	if launchMsg.todoText != todoText {
		t.Fatalf("todoWorktreeLaunchMsg.todoText = %q, want %q", launchMsg.todoText, todoText)
	}
	expectedPath := filepath.Join(root, "repo--feat-new-worktree-launch")
	if launchMsg.projectPath != expectedPath {
		t.Fatalf("worktree launch path = %q, want %q", launchMsg.projectPath, expectedPath)
	}

	updated, cmd = got.Update(launchMsg)
	got = updated.(Model)
	if got.codexPendingOpen == nil || got.codexPendingOpen.projectPath != expectedPath {
		t.Fatalf("codexPendingOpen = %#v, want pending open for %q", got.codexPendingOpen, expectedPath)
	}
	if got.codexPendingOpen.provider != codexapp.ProviderCodex {
		t.Fatalf("pending provider = %q, want %q", got.codexPendingOpen.provider, codexapp.ProviderCodex)
	}
	if !got.codexPendingOpen.newSession {
		t.Fatalf("codexPendingOpen.newSession = false, want true for dedicated worktree launch")
	}
	if got.codexVisible() {
		t.Fatalf("background worktree launch should stay hidden while the session is still opening")
	}
	draft, ok := got.todoLaunchDraftFor(expectedPath)
	if !ok {
		t.Fatalf("todoLaunchDraftFor(%q) missing", expectedPath)
	}
	if !draft.autoSubmit {
		t.Fatalf("todoLaunchDraftFor(%q) = %#v, want auto-submit enabled for background launch", expectedPath, draft)
	}
	if cmd == nil {
		t.Fatalf("handling todoWorktreeLaunchMsg should return an open command")
	}
	msgs := collectCmdMsgs(cmd)
	var opened codexSessionOpenedMsg
	foundOpen := false
	for _, msg := range msgs {
		if candidate, ok := msg.(codexSessionOpenedMsg); ok {
			opened = candidate
			foundOpen = true
			break
		}
	}
	if !foundOpen {
		t.Fatalf("cmd messages did not include codexSessionOpenedMsg: %#v", msgs)
	}
	if opened.err != nil {
		t.Fatalf("embedded session open returned error = %v", opened.err)
	}
	if len(requests) != 1 {
		t.Fatalf("request count = %d, want 1", len(requests))
	}
	if requests[0].ProjectPath != expectedPath {
		t.Fatalf("launch project path = %q, want %q", requests[0].ProjectPath, expectedPath)
	}
	if requests[0].Provider != codexapp.ProviderCodex || !requests[0].ForceNew {
		t.Fatalf("launch request = %#v, want a fresh Codex session", requests[0])
	}
	if requests[0].Prompt != todoText {
		t.Fatalf("launch prompt = %q, want %q", requests[0].Prompt, todoText)
	}

	updated, cmd = got.Update(opened)
	got = updated.(Model)
	if got.codexVisibleProject != "" {
		t.Fatalf("codexVisibleProject = %q, want background launch to stay hidden", got.codexVisibleProject)
	}
	if got.codexHiddenProject != expectedPath {
		t.Fatalf("codexHiddenProject = %q, want %q", got.codexHiddenProject, expectedPath)
	}
	if got.codexInput.Focused() {
		t.Fatalf("composer should not be focused for background worktree launches")
	}
	if got.status != opened.status {
		t.Fatalf("status = %q, want %q", got.status, opened.status)
	}
	if _, ok := got.todoLaunchDraftFor(expectedPath); ok {
		t.Fatalf("todoLaunchDraftFor(%q) should clear after the background session opens", expectedPath)
	}
	_ = cmd
}

func TestNormalModeEnterRevealsPendingTodoWorktreeLaunch(t *testing.T) {
	projectPath := "/tmp/root--feat-background-todo"
	m := Model{
		projects: []model.ProjectSummary{{
			Path:          projectPath,
			Name:          "root--feat-background-todo",
			PresentOnDisk: true,
		}},
		selected:    0,
		focusedPane: focusProjects,
		codexPendingOpen: &codexPendingOpenState{
			projectPath:      projectPath,
			provider:         codexapp.ProviderCodex,
			showWhilePending: false,
			newSession:       true,
		},
		width:  100,
		height: 24,
	}

	updated, cmd := m.updateNormalMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("enter should reveal the pending TODO launch, not queue a second open command")
	}
	if !got.codexVisible() {
		t.Fatalf("pending TODO launch should become visible after Enter")
	}
	if got.codexPendingOpen == nil || !got.codexPendingOpen.showWhilePending {
		t.Fatalf("codexPendingOpen = %#v, want visible pending open", got.codexPendingOpen)
	}
	rendered := ansi.Strip(got.renderCodexView())
	if !strings.Contains(rendered, "Starting a new embedded Codex session") || !strings.Contains(rendered, projectPath) {
		t.Fatalf("rendered pending view should show the starting session, got %q", rendered)
	}
	if strings.Contains(rendered, "previous embedded session") {
		t.Fatalf("rendered pending view should not imply a previous embedded session is settling, got %q", rendered)
	}
	got.spinnerFrame++
	animated := ansi.Strip(got.renderCodexView())
	if rendered == animated {
		t.Fatalf("rendered pending view should animate across spinner frames, got %q", rendered)
	}
}

func TestTodoWorktreeLaunchWithModelPickerKeepsPromptUnsentUntilModelChoice(t *testing.T) {
	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider: req.Provider.Normalized(),
				ThreadID: "ses-worktree-model",
				Started:  true,
				Preset:   req.Preset,
				Status:   req.Provider.Label() + " session ready",
			},
		}, nil
	})

	m := Model{
		codexManager:  manager,
		codexInput:    newCodexTextarea(),
		codexDrafts:   make(map[string]codexDraft),
		codexViewport: viewport.New(0, 0),
		projects: []model.ProjectSummary{{
			Path:          "/tmp/root",
			Name:          "root",
			PresentOnDisk: true,
		}},
		selected: 0,
		width:    100,
		height:   24,
	}

	updated, cmd := m.Update(todoWorktreeLaunchMsg{
		projectPath:    "/tmp/root--feat-model-pick",
		todoText:       "Review the TODO before sending it",
		provider:       codexapp.ProviderOpenCode,
		openModelFirst: true,
	})
	got := updated.(Model)
	draft, ok := got.todoLaunchDraftFor("/tmp/root--feat-model-pick")
	if !ok || !draft.openModelFirst {
		t.Fatalf("todoLaunchDraftFor(%q) = %#v, want open-model-first launch state", "/tmp/root--feat-model-pick", draft)
	}
	if !got.codexVisible() {
		t.Fatalf("model-picker launches should stay visible while the session is opening")
	}
	if got.codexDrafts["/tmp/root--feat-model-pick"].Text != "Review the TODO before sending it" {
		t.Fatalf("draft text = %q, want TODO text restored for the picker path", got.codexDrafts["/tmp/root--feat-model-pick"].Text)
	}
	if cmd == nil {
		t.Fatalf("worktree launch should return an embedded open command")
	}

	msgs := collectCmdMsgs(cmd)
	var opened codexSessionOpenedMsg
	foundOpen := false
	for _, msg := range msgs {
		if candidate, ok := msg.(codexSessionOpenedMsg); ok {
			opened = candidate
			foundOpen = true
			break
		}
	}
	if !foundOpen {
		t.Fatalf("cmd messages did not include codexSessionOpenedMsg: %#v", msgs)
	}
	if opened.err != nil {
		t.Fatalf("embedded open returned error = %v", opened.err)
	}
	if len(requests) != 1 {
		t.Fatalf("request count = %d, want 1", len(requests))
	}
	if requests[0].Provider != codexapp.ProviderOpenCode {
		t.Fatalf("provider = %q, want %q", requests[0].Provider, codexapp.ProviderOpenCode)
	}
	if requests[0].Prompt != "" {
		t.Fatalf("prompt = %q, want the TODO to stay unsent until after model selection", requests[0].Prompt)
	}

	updated, cmd = got.Update(opened)
	got = updated.(Model)
	if got.codexVisibleProject != "/tmp/root--feat-model-pick" {
		t.Fatalf("codexVisibleProject = %q, want the worktree session shown for model selection", got.codexVisibleProject)
	}
	if got.codexInput.Focused() {
		t.Fatalf("composer should not be focused while the model picker is opening")
	}
	if got.status != "Pick a model, then send the TODO draft." {
		t.Fatalf("status = %q, want model picker prompt", got.status)
	}
	if cmd == nil {
		t.Fatalf("session open should return the model picker command")
	}
}

func TestTodoWorktreeLaunchWithModelPickerKeepsPerProjectLaunchStateAcrossOverlap(t *testing.T) {
	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		threadID := "ses-" + filepath.Base(req.ProjectPath)
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider: req.Provider.Normalized(),
				ThreadID: threadID,
				Started:  true,
				Preset:   req.Preset,
				Status:   req.Provider.Label() + " session ready",
			},
		}, nil
	})

	m := Model{
		codexManager:  manager,
		codexInput:    newCodexTextarea(),
		codexDrafts:   make(map[string]codexDraft),
		codexViewport: viewport.New(0, 0),
		projects: []model.ProjectSummary{{
			Path:          "/tmp/root",
			Name:          "root",
			PresentOnDisk: true,
		}},
		selected: 0,
		width:    100,
		height:   24,
	}

	updated, cmd := m.Update(todoWorktreeLaunchMsg{
		projectPath:    "/tmp/root--feat-a",
		todoText:       "TODO A",
		provider:       codexapp.ProviderOpenCode,
		openModelFirst: true,
	})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("first worktree launch should return an embedded open command")
	}
	msgs := collectCmdMsgs(cmd)
	if len(msgs) == 0 {
		t.Fatalf("first launch returned no command messages")
	}
	openedA, ok := msgs[0].(codexSessionOpenedMsg)
	if !ok {
		t.Fatalf("first cmd message = %T, want codexSessionOpenedMsg", msgs[0])
	}

	updated, cmd = got.Update(todoWorktreeLaunchMsg{
		projectPath:    "/tmp/root--feat-b",
		todoText:       "TODO B",
		provider:       codexapp.ProviderOpenCode,
		openModelFirst: true,
	})
	got = updated.(Model)
	if cmd == nil {
		t.Fatalf("second worktree launch should return an embedded open command")
	}
	msgs = collectCmdMsgs(cmd)
	if len(msgs) == 0 {
		t.Fatalf("second launch returned no command messages")
	}
	openedB, ok := msgs[0].(codexSessionOpenedMsg)
	if !ok {
		t.Fatalf("second cmd message = %T, want codexSessionOpenedMsg", msgs[0])
	}

	updated, cmd = got.Update(openedA)
	got = updated.(Model)
	if got.codexVisibleProject != "/tmp/root--feat-a" {
		t.Fatalf("codexVisibleProject = %q, want %q after the first session opens", got.codexVisibleProject, "/tmp/root--feat-a")
	}
	if got.status != "Pick a model, then send the TODO draft." {
		t.Fatalf("status = %q, want model picker guidance for the first overlapping launch", got.status)
	}
	if cmd == nil {
		t.Fatalf("first overlapping session open should still return the model picker command")
	}
	msg := cmd()
	listA, ok := msg.(codexModelListMsg)
	if !ok {
		t.Fatalf("first model-picker cmd returned %T, want codexModelListMsg", msg)
	}
	if listA.projectPath != "/tmp/root--feat-a" {
		t.Fatalf("first model-picker project = %q, want %q", listA.projectPath, "/tmp/root--feat-a")
	}

	updated, cmd = got.Update(openedB)
	got = updated.(Model)
	if got.codexVisibleProject != "/tmp/root--feat-b" {
		t.Fatalf("codexVisibleProject = %q, want %q after the second session opens", got.codexVisibleProject, "/tmp/root--feat-b")
	}
	if got.status != "Pick a model, then send the TODO draft." {
		t.Fatalf("status = %q, want model picker guidance for the second overlapping launch", got.status)
	}
	if cmd == nil {
		t.Fatalf("second overlapping session open should return the model picker command")
	}
	msg = cmd()
	listB, ok := msg.(codexModelListMsg)
	if !ok {
		t.Fatalf("second model-picker cmd returned %T, want codexModelListMsg", msg)
	}
	if listB.projectPath != "/tmp/root--feat-b" {
		t.Fatalf("second model-picker project = %q, want %q", listB.projectPath, "/tmp/root--feat-b")
	}
	if len(requests) != 2 {
		t.Fatalf("request count = %d, want 2", len(requests))
	}
}

func TestTodoCopyDialogEnterStartsImmediatelyWhileWorktreeSuggestionIsQueued(t *testing.T) {
	t.Parallel()

	m := Model{
		detail: model.ProjectDetail{
			Summary: model.ProjectSummary{Path: "/tmp/demo"},
			Todos: []model.TodoItem{{
				ID:          7,
				ProjectPath: "/tmp/demo",
				Text:        "Launch this TODO in a new worktree",
				WorktreeSuggestion: &model.TodoWorktreeSuggestion{
					Status: model.TodoWorktreeSuggestionQueued,
				},
			}},
		},
		todoDialog: &todoDialogState{ProjectPath: "/tmp/demo", ProjectName: "demo"},
		todoCopyDialog: &todoCopyDialogState{
			ProjectPath: "/tmp/demo",
			ProjectName: "demo",
			TodoID:      7,
			TodoText:    "Launch this TODO in a new worktree",
			RunMode:     todoCopyModeNewWorktree,
			Provider:    codexapp.ProviderCodex,
		},
		width:        100,
		height:       24,
		spinnerFrame: 2,
	}

	updated, cmd := m.updateTodoCopyDialogMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("queued worktree suggestion should still start the background launch")
	}
	if got.todoDialog != nil || got.todoCopyDialog != nil {
		t.Fatalf("dialogs should dismiss immediately, got todo=%#v copy=%#v", got.todoDialog, got.todoCopyDialog)
	}
	if got.status != "Starting TODO in dedicated worktree..." {
		t.Fatalf("status = %q, want immediate background-start message", got.status)
	}
	msg := cmd()
	launchMsg, ok := msg.(todoWorktreeLaunchMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want todoWorktreeLaunchMsg", msg)
	}
	if launchMsg.err == nil || launchMsg.err.Error() != "service unavailable" {
		t.Fatalf("launch error = %v, want service unavailable from the stubbed model", launchMsg.err)
	}
}

func TestUpdateTodoCopyDialogHandlesPointerModelReturn(t *testing.T) {
	t.Parallel()

	m := Model{
		todoCopyDialog: &todoCopyDialogState{
			ProjectPath: "/tmp/demo",
			ProjectName: "demo",
			TodoID:      7,
			TodoText:    "Launch this TODO in a new worktree",
			RunMode:     todoCopyModeNewWorktree,
			Provider:    codexapp.ProviderCodex,
		},
		width:  100,
		height: 24,
	}

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("missing TODO selection should not start a command")
	}
	if got.status != "No TODO selected" {
		t.Fatalf("status = %q, want %q", got.status, "No TODO selected")
	}
	if got.todoCopyDialog == nil {
		t.Fatalf("todo copy dialog should stay open after a blocked launch")
	}
}

func TestTodoCopyDialogSubmittingEscCancelsPendingLaunch(t *testing.T) {
	t.Parallel()

	canceled := false
	m := Model{
		todoCopyDialog: &todoCopyDialogState{
			ProjectPath: "/tmp/demo",
			ProjectName: "demo",
			TodoID:      7,
			TodoText:    "Launch this TODO in a new worktree",
			RunMode:     todoCopyModeNewWorktree,
			Provider:    codexapp.ProviderClaudeCode,
			LaunchID:    11,
			Submitting:  true,
		},
		todoPendingLaunch: &todoPendingLaunchState{
			ID:     11,
			Cancel: func() { canceled = true },
		},
	}

	updated, cmd := m.updateTodoCopyDialogMode(tea.KeyMsg{Type: tea.KeyEsc})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("canceling a pending TODO launch should not queue another command")
	}
	if !canceled {
		t.Fatalf("cancel should be called for the pending TODO launch")
	}
	if got.todoCopyDialog != nil {
		t.Fatalf("todo copy dialog should close after canceling a pending launch")
	}
	if got.todoPendingLaunch == nil || !got.todoPendingLaunch.Canceled {
		t.Fatalf("pending TODO launch should stay tracked as canceled until its completion message arrives")
	}
	if got.status != "Canceling TODO start..." {
		t.Fatalf("status = %q, want canceling status", got.status)
	}
}

func TestTodoCopyDialogSubmittingCtrlCCancelsPendingLaunch(t *testing.T) {
	t.Parallel()

	canceled := false
	m := Model{
		todoCopyDialog: &todoCopyDialogState{
			ProjectPath: "/tmp/demo",
			ProjectName: "demo",
			TodoID:      7,
			TodoText:    "Launch this TODO in a new worktree",
			RunMode:     todoCopyModeNewWorktree,
			Provider:    codexapp.ProviderClaudeCode,
			LaunchID:    12,
			Submitting:  true,
		},
		todoPendingLaunch: &todoPendingLaunchState{
			ID:     12,
			Cancel: func() { canceled = true },
		},
	}

	updated, cmd := m.updateTodoCopyDialogMode(tea.KeyMsg{Type: tea.KeyCtrlC})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("ctrl+c cancel should not queue another command")
	}
	if !canceled {
		t.Fatalf("ctrl+c should cancel the pending TODO launch")
	}
	if got.todoCopyDialog != nil {
		t.Fatalf("todo copy dialog should close after ctrl+c cancel")
	}
	if got.todoPendingLaunch == nil || !got.todoPendingLaunch.Canceled {
		t.Fatalf("pending TODO launch should remain marked canceled after ctrl+c")
	}
	if got.status != "Canceling TODO start..." {
		t.Fatalf("status = %q, want canceling status", got.status)
	}
}

func TestTodoWorktreeLaunchCanceledSkipsErrorReporting(t *testing.T) {
	t.Parallel()

	m := Model{
		todoPendingLaunch: &todoPendingLaunchState{
			ID:       15,
			Canceled: true,
		},
	}

	updated, cmd := m.Update(todoWorktreeLaunchMsg{
		launchID:    15,
		projectPath: "/tmp/demo",
		err:         context.Canceled,
	})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("canceled todo launch should not queue a follow-up command")
	}
	if got.todoPendingLaunch != nil {
		t.Fatalf("pending TODO launch should clear once the canceled result arrives")
	}
	if got.status != "TODO start canceled" {
		t.Fatalf("status = %q, want canceled status", got.status)
	}
	if len(got.errorLogEntries) != 0 {
		t.Fatalf("canceled todo launch should not add error log entries, got %#v", got.errorLogEntries)
	}
}

func TestTodoWorktreeLaunchErrorAfterBackgroundStartLeavesDialogsClosed(t *testing.T) {
	t.Parallel()

	m := Model{
		todoPendingLaunch: &todoPendingLaunchState{ID: 9},
	}

	updated, cmd := m.Update(todoWorktreeLaunchMsg{
		launchID: 9,
		err:      fmt.Errorf("create worktree failed"),
	})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("todo worktree launch error should not return a follow-up command")
	}
	if got.todoCopyDialog != nil {
		t.Fatalf("todo copy dialog should stay dismissed after the background launch fails")
	}
	if got.todoDialog != nil {
		t.Fatalf("todo dialog should stay dismissed after the background launch fails")
	}
	if got.status != "TODO launch failed (use /errors)" {
		t.Fatalf("status = %q, want launch error", got.status)
	}
	if len(got.errorLogEntries) == 0 || got.errorLogEntries[0].Message != "create worktree failed" {
		t.Fatalf("latest error log entry = %#v, want create worktree failed", got.errorLogEntries)
	}
}

func TestTodoDialogCanStartSelectedTodoInExistingWorktree(t *testing.T) {
	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider: req.Provider.Normalized(),
				ThreadID: "ses-existing-worktree",
				Started:  true,
				Preset:   req.Preset,
				Status:   req.Provider.Label() + " session ready",
			},
		}, nil
	})

	rootPath := "/tmp/repo"
	worktreePath := "/tmp/repo--feat-reuse-lane"
	m := Model{
		codexManager: manager,
		allProjects: []model.ProjectSummary{
			{
				Name:             "repo",
				Path:             rootPath,
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindMain,
				RepoBranch:       "master",
			},
			{
				Name:             "repo--feat-reuse-lane",
				Path:             worktreePath,
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindLinked,
				RepoBranch:       "feat/reuse-lane",
				RepoDirty:        true,
			},
		},
		detail: model.ProjectDetail{
			Summary: model.ProjectSummary{Path: rootPath},
			Todos: []model.TodoItem{{
				ID:          41,
				ProjectPath: rootPath,
				Text:        "Reuse an existing worktree for this TODO",
			}},
		},
		selected:      0,
		todoDialog:    &todoDialogState{ProjectPath: rootPath, ProjectName: "repo"},
		codexInput:    newCodexTextarea(),
		codexDrafts:   make(map[string]codexDraft),
		codexViewport: viewport.New(0, 0),
		width:         100,
		height:        24,
		sortMode:      sortByAttention,
		visibility:    visibilityAllFolders,
	}
	m.rebuildProjectList(rootPath)

	updated, _ := m.updateTodoDialogMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if got.todoCopyDialog == nil || got.todoCopyDialog.Provider != codexapp.ProviderCodex {
		t.Fatalf("todoCopyDialog = %#v, want Codex selected", got.todoCopyDialog)
	}
	rendered := ansi.Strip(got.renderTodoCopyDialogOverlay("", 100, 24))
	if !strings.Contains(rendered, "x for 1 existing worktree(s)") {
		t.Fatalf("rendered copy dialog = %q, want existing-worktree hint", rendered)
	}

	updated, cmd := got.updateTodoCopyDialogMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	got = updated.(Model)
	if cmd != nil {
		t.Fatalf("opening the existing worktree picker should not launch immediately")
	}
	if got.todoExistingWorktree == nil {
		t.Fatalf("existing worktree picker should open")
	}
	rendered = ansi.Strip(got.renderTodoExistingWorktreeOverlay("", 100, 24))
	if !strings.Contains(rendered, "feat/reuse-lane") {
		t.Fatalf("rendered existing worktree picker = %q, want branch label", rendered)
	}

	updated, cmd = got.updateTodoExistingWorktreeMode(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if got.codexPendingOpen == nil || got.codexPendingOpen.projectPath != worktreePath {
		t.Fatalf("codexPendingOpen = %#v, want pending open for %q", got.codexPendingOpen, worktreePath)
	}
	if cmd == nil {
		t.Fatalf("starting in an existing worktree should return an open command")
	}
	msg := cmd()
	opened, ok := msg.(codexSessionOpenedMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want codexSessionOpenedMsg", msg)
	}
	if opened.err != nil {
		t.Fatalf("existing worktree launch returned error = %v", opened.err)
	}
	if len(requests) != 1 {
		t.Fatalf("request count = %d, want 1", len(requests))
	}
	if requests[0].ProjectPath != worktreePath || requests[0].Provider != codexapp.ProviderCodex {
		t.Fatalf("launch request = %#v, want Codex launch in %q", requests[0], worktreePath)
	}
}

func TestCloseTodoDialogRequestsSelectedWorktreeDetailAfterRootTodoView(t *testing.T) {
	rootPath := "/tmp/repo"
	worktreePath := "/tmp/repo--todo-fix"
	m := Model{
		projects: []model.ProjectSummary{{
			Path:             worktreePath,
			Name:             "repo--todo-fix",
			WorktreeRootPath: rootPath,
			WorktreeKind:     model.WorktreeKindLinked,
		}},
		selected: 0,
		todoDialog: &todoDialogState{
			ProjectPath: rootPath,
			ProjectName: "repo",
		},
	}

	cmd := m.closeTodoDialog("TODO list closed")

	if m.todoDialog != nil {
		t.Fatalf("todo dialog should close, got %#v", m.todoDialog)
	}
	if cmd == nil {
		t.Fatal("closing a root TODO dialog from a linked worktree should refresh the selected detail view")
	}
}

func TestTodoDialogCopyDialogHotkeysChangeRunModeAndProvider(t *testing.T) {
	m := Model{
		projects: []model.ProjectSummary{{
			Path:                "/tmp/demo",
			Name:                "demo",
			PresentOnDisk:       true,
			LatestSessionFormat: "codex",
		}},
		detail: model.ProjectDetail{
			Summary: model.ProjectSummary{Path: "/tmp/demo"},
			Todos: []model.TodoItem{{
				ID:          9,
				ProjectPath: "/tmp/demo",
				Text:        "Try the TODO launcher hotkeys",
			}},
		},
		selected:      0,
		todoDialog:    &todoDialogState{ProjectPath: "/tmp/demo", ProjectName: "demo"},
		codexInput:    newCodexTextarea(),
		codexDrafts:   make(map[string]codexDraft),
		codexViewport: viewport.New(0, 0),
		width:         100,
		height:        24,
	}

	updated, _ := m.updateTodoDialogMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if got.todoCopyDialog == nil {
		t.Fatalf("todo copy dialog should open after Enter")
	}
	if got.todoCopyDialog.RunMode != todoCopyModeNewWorktree {
		t.Fatalf("copy dialog run mode = %d, want %d", got.todoCopyDialog.RunMode, todoCopyModeNewWorktree)
	}
	if got.todoCopyDialog.Provider != codexapp.ProviderCodex {
		t.Fatalf("copy dialog provider = %q, want %q", got.todoCopyDialog.Provider, codexapp.ProviderCodex)
	}

	updated, _ = got.updateTodoCopyDialogMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'w'}})
	got = updated.(Model)
	if got.todoCopyDialog.RunMode != todoCopyModeHere {
		t.Fatalf("copy dialog run mode = %d, want %d after w", got.todoCopyDialog.RunMode, todoCopyModeHere)
	}

	updated, _ = got.updateTodoCopyDialogMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	got = updated.(Model)
	if got.todoCopyDialog.Provider != codexapp.ProviderOpenCode {
		t.Fatalf("copy dialog provider = %q, want %q after a", got.todoCopyDialog.Provider, codexapp.ProviderOpenCode)
	}

	updated, _ = got.updateTodoCopyDialogMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	got = updated.(Model)
	if !got.todoCopyDialog.OpenModelFirst {
		t.Fatalf("copy dialog should enable model toggle after m")
	}

	updated, _ = got.updateTodoCopyDialogMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'A'}})
	got = updated.(Model)
	if got.todoCopyDialog.Provider != codexapp.ProviderCodex {
		t.Fatalf("copy dialog provider = %q, want %q after A", got.todoCopyDialog.Provider, codexapp.ProviderCodex)
	}
}

func TestTodoDialogPurgeHotkeyOpensConfirmForCompletedItems(t *testing.T) {
	m := Model{
		detail: model.ProjectDetail{
			Summary: model.ProjectSummary{Path: "/tmp/demo"},
			Todos: []model.TodoItem{
				{
					ID:          1,
					ProjectPath: "/tmp/demo",
					Text:        "Keep this open",
				},
				{
					ID:          2,
					ProjectPath: "/tmp/demo",
					Text:        "Purge this done item",
					Done:        true,
				},
			},
		},
		todoDialog: &todoDialogState{ProjectPath: "/tmp/demo", ProjectName: "demo"},
		width:      100,
		height:     24,
	}

	updated, _ := m.updateTodoDialogMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	got := updated.(Model)
	if got.todoDeleteConfirm == nil {
		t.Fatalf("purge hotkey should open a confirmation dialog")
	}
	if got.todoDeleteConfirm.TodoID != 0 {
		t.Fatalf("purge confirmation todo id = %d, want bulk mode", got.todoDeleteConfirm.TodoID)
	}
	if got.todoDeleteConfirm.DoneCount != 1 {
		t.Fatalf("purge confirmation done count = %d, want 1", got.todoDeleteConfirm.DoneCount)
	}
	if got.todoDeleteConfirm.Selected != todoDeleteConfirmFocusKeep {
		t.Fatalf("default purge confirmation selection = %d, want keep", got.todoDeleteConfirm.Selected)
	}
	if got.status != "Confirm completed TODO purge" {
		t.Fatalf("status = %q, want purge confirmation", got.status)
	}

	rendered := ansi.Strip(got.renderTodoDeleteConfirmOverlay("", 100, 24))
	if !strings.Contains(rendered, "Purge Completed TODOs") {
		t.Fatalf("rendered purge confirmation should explain the bulk action, got %q", rendered)
	}
}

func TestTodoDialogPurgeHotkeyReportsWhenNothingIsCompleted(t *testing.T) {
	m := Model{
		detail: model.ProjectDetail{
			Summary: model.ProjectSummary{Path: "/tmp/demo"},
			Todos: []model.TodoItem{{
				ID:          1,
				ProjectPath: "/tmp/demo",
				Text:        "Still in progress",
			}},
		},
		todoDialog: &todoDialogState{ProjectPath: "/tmp/demo", ProjectName: "demo"},
	}

	updated, cmd := m.updateTodoDialogMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("purge hotkey should not start a command when nothing is completed")
	}
	if got.todoDeleteConfirm != nil {
		t.Fatalf("purge confirmation should stay closed when there is nothing to purge")
	}
	if got.status != "No completed TODOs to purge" {
		t.Fatalf("status = %q, want no-completed message", got.status)
	}
}

func TestTodoDeleteConfirmPurgeQueuesBulkRemoval(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	projectPath := filepath.Join(root, "repo")
	runTUITestGit(t, "", "init", projectPath)
	runTUITestGit(t, projectPath, "config", "user.name", "Little Control Room Tests")
	runTUITestGit(t, projectPath, "config", "user.email", "tests@example.com")

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	svc := service.New(config.Default(), st, events.NewBus(), nil)
	if _, err := svc.CreateOrAttachProject(ctx, service.CreateOrAttachProjectRequest{
		ParentPath: root,
		Name:       "repo",
	}); err != nil {
		t.Fatalf("track root project: %v", err)
	}

	if _, err := svc.AddTodo(ctx, projectPath, "Keep this open"); err != nil {
		t.Fatalf("add open todo: %v", err)
	}
	doneItem, err := svc.AddTodo(ctx, projectPath, "Purge this done item")
	if err != nil {
		t.Fatalf("add done todo: %v", err)
	}
	if err := svc.ToggleTodoDone(ctx, projectPath, doneItem.ID, true); err != nil {
		t.Fatalf("mark done todo: %v", err)
	}

	detail, err := st.GetProjectDetail(ctx, projectPath, 0)
	if err != nil {
		t.Fatalf("get project detail: %v", err)
	}

	m := Model{
		ctx:    ctx,
		svc:    svc,
		detail: detail,
		todoDialog: &todoDialogState{
			ProjectPath: projectPath,
			ProjectName: "repo",
		},
	}

	updated, _ := m.updateTodoDialogMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	got := updated.(Model)
	if got.todoDeleteConfirm == nil {
		t.Fatalf("purge hotkey should open the confirmation dialog")
	}

	updated, _ = got.updateTodoDeleteConfirmMode(tea.KeyMsg{Type: tea.KeyTab})
	got = updated.(Model)
	if got.todoDeleteConfirm.Selected != todoDeleteConfirmFocusDelete {
		t.Fatalf("purge confirmation should move focus to purge")
	}

	updated, cmd := got.updateTodoDeleteConfirmMode(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if got.status != "Purging completed TODOs..." {
		t.Fatalf("status = %q, want purge progress", got.status)
	}
	if cmd == nil {
		t.Fatalf("purge confirmation should queue a removal command")
	}
	rawMsg := cmd()
	msg, ok := rawMsg.(todoActionMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want todoActionMsg", rawMsg)
	}
	if msg.err != nil {
		t.Fatalf("todoActionMsg.err = %v, want nil", msg.err)
	}
	if msg.status != "Purged 1 completed TODO" {
		t.Fatalf("todoActionMsg.status = %q, want singular purge status", msg.status)
	}
}

func TestTodoEditorSaveDismissesImmediatelyAndClearsBusyOnSuccess(t *testing.T) {
	t.Parallel()

	const projectPath = "/tmp/demo"
	const todoText = "Codex may say model is at capacity. Switch to a higher model with lower reasoning?"

	m := Model{
		todoDialog: &todoDialogState{
			ProjectPath: projectPath,
			ProjectName: "demo",
		},
		todoEditor: &todoEditorState{
			ProjectPath: projectPath,
			ProjectName: "demo",
			Input:       newTodoTextInput(todoText),
		},
	}

	updated, cmd := m.updateTodoEditorMode(tea.KeyMsg{Type: tea.KeyCtrlS})
	got := updated.(Model)
	if cmd == nil {
		t.Fatal("ctrl+s should queue a save command")
	}
	if got.todoEditor != nil {
		t.Fatalf("todo editor should dismiss immediately after save, got %#v", got.todoEditor)
	}
	if got.todoPendingSave == nil {
		t.Fatal("todo save should remain tracked while the command is in flight")
	}
	if got.todoPendingSave.Text != todoText {
		t.Fatalf("pending todo text = %q, want %q", got.todoPendingSave.Text, todoText)
	}
	if got.todoDialog == nil || !got.todoDialog.Busy {
		t.Fatalf("todo dialog should mark itself busy while the save is in flight, got %#v", got.todoDialog)
	}
	if got.status != "Adding TODO..." {
		t.Fatalf("status = %q, want add progress", got.status)
	}

	updatedModel, followUp := got.Update(todoActionMsg{projectPath: projectPath, status: "TODO added"})
	got = updatedModel.(Model)
	if got.todoPendingSave != nil {
		t.Fatalf("pending todo save should clear after success, got %#v", got.todoPendingSave)
	}
	if got.todoDialog == nil || got.todoDialog.Busy {
		t.Fatalf("todo dialog busy flag should clear after save success, got %#v", got.todoDialog)
	}
	if got.todoEditor != nil {
		t.Fatalf("todo editor should stay dismissed after a successful save, got %#v", got.todoEditor)
	}
	if got.status != "TODO added" {
		t.Fatalf("status = %q, want success status", got.status)
	}
	if followUp == nil {
		t.Fatal("successful save should refresh project state")
	}
}

func TestTodoEditorSaveRestoresDraftOnFailure(t *testing.T) {
	t.Parallel()

	const projectPath = "/tmp/demo"
	const todoText = "Save should restore this draft after an error"

	m := Model{
		todoDialog: &todoDialogState{
			ProjectPath: projectPath,
			ProjectName: "demo",
		},
		todoEditor: &todoEditorState{
			ProjectPath: projectPath,
			ProjectName: "demo",
			Input:       newTodoTextInput(todoText),
		},
	}

	updated, cmd := m.updateTodoEditorMode(tea.KeyMsg{Type: tea.KeyCtrlS})
	got := updated.(Model)
	if cmd == nil {
		t.Fatal("ctrl+s should queue a save command")
	}

	rawMsg := cmd()
	action, ok := rawMsg.(todoActionMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want todoActionMsg", rawMsg)
	}
	if action.err == nil || action.err.Error() != "service unavailable" {
		t.Fatalf("todoActionMsg.err = %v, want service unavailable", action.err)
	}

	updatedModel, focusCmd := got.Update(action)
	got = updatedModel.(Model)
	if got.todoPendingSave != nil {
		t.Fatalf("pending todo save should clear after failure recovery, got %#v", got.todoPendingSave)
	}
	if got.todoDialog == nil || got.todoDialog.Busy {
		t.Fatalf("todo dialog busy flag should clear after save failure, got %#v", got.todoDialog)
	}
	if got.todoEditor == nil {
		t.Fatal("todo editor should reopen after save failure")
	}
	if got.todoEditor.Input.Value() != todoText {
		t.Fatalf("reopened todo text = %q, want %q", got.todoEditor.Input.Value(), todoText)
	}
	if got.todoEditor.Submitting {
		t.Fatalf("reopened todo editor should not stay submitting")
	}
	if focusCmd == nil {
		t.Fatal("reopening the todo editor after failure should refocus the input")
	}
	if !strings.Contains(got.status, "TODO action failed") {
		t.Fatalf("status = %q, want todo action failure hint", got.status)
	}
}

func TestTodoDialogSpaceBlocksRepeatWhileToggleIsInFlight(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	projectPath := filepath.Join(root, "repo")
	runTUITestGit(t, "", "init", projectPath)
	runTUITestGit(t, projectPath, "config", "user.name", "Little Control Room Tests")
	runTUITestGit(t, projectPath, "config", "user.email", "tests@example.com")

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	svc := service.New(config.Default(), st, events.NewBus(), nil)
	if _, err := svc.CreateOrAttachProject(ctx, service.CreateOrAttachProjectRequest{
		ParentPath: root,
		Name:       "repo",
	}); err != nil {
		t.Fatalf("track root project: %v", err)
	}

	if _, err := svc.AddTodo(ctx, projectPath, "Toggle this item"); err != nil {
		t.Fatalf("add todo: %v", err)
	}

	detail, err := st.GetProjectDetail(ctx, projectPath, 0)
	if err != nil {
		t.Fatalf("get project detail: %v", err)
	}

	m := Model{
		ctx:    ctx,
		svc:    svc,
		detail: detail,
		projects: []model.ProjectSummary{{
			Path: projectPath,
			Name: "repo",
		}},
		todoDialog: &todoDialogState{
			ProjectPath: projectPath,
			ProjectName: "repo",
		},
	}

	updated, cmd := m.updateTodoDialogMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	got := updated.(Model)
	if got.todoDialog == nil || !got.todoDialog.Busy {
		t.Fatalf("todo dialog should mark itself busy while the toggle command is in flight, got %#v", got.todoDialog)
	}
	if got.status != "Updating TODO..." {
		t.Fatalf("status = %q, want toggle progress", got.status)
	}
	if cmd == nil {
		t.Fatal("space toggle should queue a command")
	}

	updated, repeatCmd := got.updateTodoDialogMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	got = updated.(Model)
	if repeatCmd != nil {
		t.Fatalf("repeat toggle while busy should not queue another command")
	}
	if got.status != "TODO update already in progress" {
		t.Fatalf("status = %q, want busy warning", got.status)
	}

	rawMsg := cmd()
	action, ok := rawMsg.(todoActionMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want todoActionMsg", rawMsg)
	}
	updatedModel, followUp := got.Update(action)
	got = updatedModel.(Model)
	if got.todoDialog == nil || got.todoDialog.Busy {
		t.Fatalf("todo dialog busy flag should clear after the toggle completes, got %#v", got.todoDialog)
	}
	if followUp == nil {
		t.Fatal("completed toggle should refresh project state")
	}
}

func TestLaunchClaudeForSelectionUsesClaudeProvider(t *testing.T) {
	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider: req.Provider.Normalized(),
				ThreadID: "cc-demo",
				Started:  true,
				Preset:   req.Preset,
				Status:   req.Provider.Label() + " session ready",
			},
		}, nil
	})

	m := Model{
		codexManager: manager,
		projects: []model.ProjectSummary{{
			Path:          "/tmp/demo",
			Name:          "demo",
			PresentOnDisk: true,
		}},
		selected:      0,
		codexInput:    newCodexTextarea(),
		codexDrafts:   make(map[string]codexDraft),
		codexViewport: viewport.New(0, 0),
		width:         100,
		height:        24,
	}

	updated, cmd := m.launchClaudeForSelection(true, "continue with the current task")
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("launchClaudeForSelection should return an open command")
	}
	if got.codexPendingOpen == nil || got.codexPendingOpen.provider != codexapp.ProviderClaudeCode {
		t.Fatalf("codexPendingOpen = %#v, want pending Claude launch", got.codexPendingOpen)
	}

	msg := cmd()
	opened, ok := msg.(codexSessionOpenedMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want codexSessionOpenedMsg", msg)
	}
	if opened.err != nil {
		t.Fatalf("launchClaudeForSelection returned error = %v", opened.err)
	}

	if len(requests) != 1 {
		t.Fatalf("request count = %d, want 1", len(requests))
	}
	if requests[0].Provider != codexapp.ProviderClaudeCode {
		t.Fatalf("provider = %q, want %q", requests[0].Provider, codexapp.ProviderClaudeCode)
	}
	if !requests[0].ForceNew {
		t.Fatalf("ForceNew = false, want true")
	}
	if requests[0].Prompt != "continue with the current task" {
		t.Fatalf("prompt = %q, want Claude prompt", requests[0].Prompt)
	}
}

func TestTodoDialogSelectedRowHasNoExtraLeadingSpace(t *testing.T) {
	m := Model{
		detail: model.ProjectDetail{
			Summary: model.ProjectSummary{Path: "/tmp/demo"},
			Todos: []model.TodoItem{{
				ID:          7,
				ProjectPath: "/tmp/demo",
				Text:        "Fix spacing on selected TODO row",
			}},
		},
		todoDialog: &todoDialogState{ProjectPath: "/tmp/demo", ProjectName: "demo", Selected: 0},
		width:      100,
		height:     24,
	}

	rendered := ansi.Strip(m.renderTodoDialogOverlay("", 100, 24))
	selectedRow := ""
	for _, line := range strings.Split(rendered, "\n") {
		if strings.Contains(line, "[ ] Fix spacing on selected TODO row") {
			selectedRow = strings.TrimLeft(line, " \t")
			break
		}
	}
	if selectedRow == "" {
		t.Fatalf("selected TODO row not found, got %q", rendered)
	}
	if !strings.HasPrefix(selectedRow, "│ [") {
		t.Fatalf("expected selected TODO row to start with a single leading space after panel border, got %q", selectedRow)
	}
	if strings.HasPrefix(selectedRow, "│  [") {
		t.Fatalf("expected no extra leading space before selected TODO marker, got %q", selectedRow)
	}
}

func TestTodoDialogShowsWorktreeSuggestionState(t *testing.T) {
	prevProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	defer lipgloss.SetColorProfile(prevProfile)

	m := Model{
		detail: model.ProjectDetail{
			Summary: model.ProjectSummary{Path: "/tmp/demo"},
			Todos: []model.TodoItem{
				{
					ID:          7,
					ProjectPath: "/tmp/demo",
					Text:        "Fix spacing on selected TODO row",
					WorktreeSuggestion: &model.TodoWorktreeSuggestion{
						Status:     model.TodoWorktreeSuggestionReady,
						BranchName: "fix/todo-dialog-spacing",
					},
				},
				{
					ID:          8,
					ProjectPath: "/tmp/demo",
					Text:        "Write launch dialog spec",
					WorktreeSuggestion: &model.TodoWorktreeSuggestion{
						Status: model.TodoWorktreeSuggestionQueued,
					},
				},
			},
		},
		todoDialog: &todoDialogState{ProjectPath: "/tmp/demo", ProjectName: "demo", Selected: 0},
		width:      100,
		height:     24,
	}

	rendered := m.renderTodoDialogOverlay("", 100, 24)
	if !strings.Contains(ansi.Strip(rendered), "fix/todo-dialog-spacing") {
		t.Fatalf("rendered TODO dialog should show ready branch suggestion, got %q", ansi.Strip(rendered))
	}
	if !strings.Contains(rendered, "38;5;244") {
		t.Fatalf("rendered TODO dialog should keep suggestion-only worktree labels muted, got %q", rendered)
	}
	if strings.Contains(ansi.Strip(rendered), "preparing suggestion...") {
		t.Fatalf("rendered TODO dialog should hide queued suggestion state, got %q", ansi.Strip(rendered))
	}
}

func TestTodoDialogPageNavigationRevealsItems(t *testing.T) {
	todos := make([]model.TodoItem, 40)
	for i := 0; i < 40; i++ {
		todos[i] = model.TodoItem{
			ID:          int64(i + 1),
			ProjectPath: "/tmp/demo",
			Text:        fmt.Sprintf("Todo %d", i),
		}
	}
	m := Model{
		detail: model.ProjectDetail{
			Summary: model.ProjectSummary{Path: "/tmp/demo"},
			Todos:   todos,
		},
		todoDialog: &todoDialogState{ProjectPath: "/tmp/demo", ProjectName: "demo"},
		width:      100,
		height:     24,
	}

	updated, _ := m.updateTodoDialogMode(tea.KeyMsg{Type: tea.KeyPgDown})
	got := updated.(Model)
	if got.todoDialog == nil {
		t.Fatalf("todo dialog should stay open after Page Down")
	}
	if got.todoDialog.Selected != 14 {
		t.Fatalf("todo dialog selected = %d, want %d", got.todoDialog.Selected, 14)
	}

	rendered := ansi.Strip(got.renderTodoDialogOverlay("", 100, 24))
	if !strings.Contains(rendered, "Todo 14") {
		t.Fatalf("rendered TODO dialog should include newly selected item, got %q", rendered)
	}
}

func TestTodoDialogMouseWheelScrollRevealsHiddenItems(t *testing.T) {
	todos := make([]model.TodoItem, 40)
	for i := 0; i < 40; i++ {
		todos[i] = model.TodoItem{
			ID:          int64(i + 1),
			ProjectPath: "/tmp/demo",
			Text:        fmt.Sprintf("Todo %d", i),
		}
	}
	m := Model{
		detail: model.ProjectDetail{
			Summary: model.ProjectSummary{Path: "/tmp/demo"},
			Todos:   todos,
		},
		todoDialog: &todoDialogState{ProjectPath: "/tmp/demo", ProjectName: "demo"},
		width:      100,
		height:     24,
	}

	msg := tea.MouseMsg{
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonWheelDown,
	}
	current := m
	for i := 0; i < 25; i++ {
		updated, _ := current.Update(msg)
		next, ok := updated.(Model)
		if !ok {
			t.Fatalf("updated model = %T, want Model", updated)
		}
		current = next
	}

	got := current
	if got.todoDialog.Selected <= 0 {
		t.Fatalf("todo dialog should move selection on wheel scroll, selected = %d", got.todoDialog.Selected)
	}
	if got.todoDialog.Selected != 25 {
		t.Fatalf("todo dialog selected = %d, want %d", got.todoDialog.Selected, 25)
	}

	rendered := ansi.Strip(got.renderTodoDialogOverlay("", 100, 24))
	if !strings.Contains(rendered, "Todo 25") {
		t.Fatalf("rendered TODO dialog should include the scrolled-into selection, got %q", rendered)
	}
}

func TestTodoDialogHomeAndEndJumpToExtremes(t *testing.T) {
	todos := make([]model.TodoItem, 24)
	for i := 0; i < 24; i++ {
		todos[i] = model.TodoItem{
			ID:          int64(i + 1),
			ProjectPath: "/tmp/demo",
			Text:        fmt.Sprintf("Todo %d", i),
		}
	}
	m := Model{
		detail: model.ProjectDetail{
			Summary: model.ProjectSummary{Path: "/tmp/demo"},
			Todos:   todos,
		},
		todoDialog: &todoDialogState{ProjectPath: "/tmp/demo", ProjectName: "demo", Selected: 10},
		width:      100,
		height:     24,
	}

	updated, _ := m.updateTodoDialogMode(tea.KeyMsg{Type: tea.KeyHome})
	got := updated.(Model)
	if got.todoDialog.Selected != 0 {
		t.Fatalf("home should jump to first TODO, selected = %d", got.todoDialog.Selected)
	}

	updated, _ = got.updateTodoDialogMode(tea.KeyMsg{Type: tea.KeyEnd})
	got = updated.(Model)
	if got.todoDialog.Selected != len(todos)-1 {
		t.Fatalf("end should jump to last TODO, selected = %d", got.todoDialog.Selected)
	}

	rendered := ansi.Strip(got.renderTodoDialogOverlay("", 100, 24))
	if !strings.Contains(rendered, "Todo 23") {
		t.Fatalf("rendered TODO dialog should include last item after End, got %q", rendered)
	}
}

func TestTodoDialogHighlightsActiveLinkedWorktreeState(t *testing.T) {
	prevProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	defer lipgloss.SetColorProfile(prevProfile)

	m := Model{
		allProjects: []model.ProjectSummary{
			{
				Path:             "/tmp/demo",
				PresentOnDisk:    true,
				WorktreeRootPath: "/tmp/demo",
				WorktreeKind:     model.WorktreeKindMain,
				RepoBranch:       "master",
			},
			{
				Path:                 "/tmp/demo--fix-todo-dialog-spacing",
				PresentOnDisk:        true,
				WorktreeRootPath:     "/tmp/demo",
				WorktreeKind:         model.WorktreeKindLinked,
				WorktreeOriginTodoID: 7,
				RepoBranch:           "fix/todo-dialog-spacing",
			},
		},
		detail: model.ProjectDetail{
			Summary: model.ProjectSummary{Path: "/tmp/demo"},
			Todos: []model.TodoItem{
				{
					ID:          7,
					ProjectPath: "/tmp/demo",
					Text:        "Fix spacing on selected TODO row",
					WorktreeSuggestion: &model.TodoWorktreeSuggestion{
						Status:     model.TodoWorktreeSuggestionReady,
						BranchName: "fix/todo-dialog-spacing",
					},
				},
			},
		},
		todoDialog: &todoDialogState{ProjectPath: "/tmp/demo", ProjectName: "demo", Selected: 0},
		width:      100,
		height:     24,
	}

	rendered := m.renderTodoDialogOverlay("", 100, 24)
	if !strings.Contains(ansi.Strip(rendered), "fix/todo-dialog-spacing") {
		t.Fatalf("rendered TODO dialog should show linked worktree label, got %q", ansi.Strip(rendered))
	}
	if !strings.Contains(rendered, "38;5;42") {
		t.Fatalf("rendered TODO dialog should highlight active linked worktree labels, got %q", rendered)
	}
}

func TestTodoCopyDialogShowsRetryGuidanceForFailedWorktreeSuggestion(t *testing.T) {
	m := Model{
		detail: model.ProjectDetail{
			Summary: model.ProjectSummary{Path: "/tmp/demo"},
			Todos: []model.TodoItem{{
				ID:          7,
				ProjectPath: "/tmp/demo",
				Text:        "Fix spacing on selected TODO row",
				WorktreeSuggestion: &model.TodoWorktreeSuggestion{
					Status: model.TodoWorktreeSuggestionFailed,
				},
			}},
		},
		todoDialog: &todoDialogState{ProjectPath: "/tmp/demo", ProjectName: "demo"},
		todoCopyDialog: &todoCopyDialogState{
			ProjectPath: "/tmp/demo",
			ProjectName: "demo",
			TodoID:      7,
			TodoText:    "Fix spacing on selected TODO row",
			RunMode:     todoCopyModeNewWorktree,
			Provider:    codexapp.ProviderCodex,
		},
		width:  100,
		height: 24,
	}

	rendered := ansi.Strip(m.renderTodoCopyDialogOverlay("", 100, 24))
	if !strings.Contains(rendered, "Worktree suggestion is unavailable right now.") {
		t.Fatalf("rendered copy dialog should show the failed suggestion status, got %q", rendered)
	}
	if !strings.Contains(rendered, "Press Enter to launch with an automatic name, or e to enter names now.") {
		t.Fatalf("rendered copy dialog should explain the next recovery step, got %q", rendered)
	}
}

func TestEmbeddedModelPreferenceLoadsFromSavedSettingsOnStartup(t *testing.T) {
	cfg := config.Default()
	cfg.ConfigPath = filepath.Join(t.TempDir(), "config.toml")
	cfg.EmbeddedCodexModel = "gpt-5.4"
	cfg.EmbeddedCodexReasoning = "high"
	cfg.EmbeddedClaudeModel = "sonnet"
	cfg.EmbeddedClaudeReasoning = "max"
	cfg.EmbeddedOpenCodeModel = "openai/gpt-5.4"
	cfg.EmbeddedOpenCodeReasoning = "medium"

	svc := service.New(cfg, nil, events.NewBus(), nil)
	m := New(context.Background(), svc)

	var requests []codexapp.LaunchRequest
	m.codexManager = codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider: req.Provider.Normalized(),
				Started:  true,
				Status:   req.Provider.Label() + " session ready",
			},
		}, nil
	})
	m.projects = []model.ProjectSummary{{
		Path:          "/tmp/demo",
		Name:          "demo",
		PresentOnDisk: true,
	}}

	updated, cmd := m.launchEmbeddedForSelection(codexapp.ProviderCodex, true, "")
	m = updated.(Model)
	if cmd == nil {
		t.Fatalf("launchEmbeddedForSelection(codex) should return an open command")
	}
	if msg := cmd(); msg == nil {
		t.Fatalf("codex open command should return a message")
	}

	updated, cmd = m.launchEmbeddedForSelection(codexapp.ProviderOpenCode, true, "")
	m = updated.(Model)
	if cmd == nil {
		t.Fatalf("launchEmbeddedForSelection(opencode) should return an open command")
	}
	if msg := cmd(); msg == nil {
		t.Fatalf("opencode open command should return a message")
	}

	updated, cmd = m.launchEmbeddedForSelection(codexapp.ProviderClaudeCode, true, "")
	m = updated.(Model)
	if cmd == nil {
		t.Fatalf("launchEmbeddedForSelection(claude) should return an open command")
	}
	if msg := cmd(); msg == nil {
		t.Fatalf("claude open command should return a message")
	}

	if len(requests) != 3 {
		t.Fatalf("request count = %d, want 3", len(requests))
	}
	if requests[0].PendingModel != "gpt-5.4" || requests[0].PendingReasoning != "high" {
		t.Fatalf("codex request = %#v, want saved startup preference", requests[0])
	}
	if requests[1].PendingModel != "openai/gpt-5.4" || requests[1].PendingReasoning != "medium" {
		t.Fatalf("opencode request = %#v, want saved startup preference", requests[1])
	}
	if requests[2].PendingModel != "sonnet" || requests[2].PendingReasoning != "max" {
		t.Fatalf("claude request = %#v, want saved startup preference", requests[2])
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
	if got.status != "Starting a new embedded Codex session..." {
		t.Fatalf("status = %q, want fresh-session notice", got.status)
	}
	if got.codexPendingOpen == nil || got.codexPendingOpen.projectPath != "/tmp/demo" {
		t.Fatalf("codexPendingOpen = %#v, want pending open for /tmp/demo", got.codexPendingOpen)
	}
	rendered := ansi.Strip(got.renderCodexView())
	if !strings.Contains(rendered, "Starting a new embedded Codex session...") {
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

func TestVisibleOpenCodeSlashNewStartsFreshSession(t *testing.T) {
	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		threadID := "ses-old1"
		if len(requests) > 1 {
			threadID = "ses-new1"
		}
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider: codexapp.ProviderOpenCode,
				Started:  true,
				Status:   "OpenCode session ready",
				ThreadID: threadID,
			},
		}, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Provider:    codexapp.ProviderOpenCode,
		ResumeID:    "ses-old1",
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
	if got.status != "Starting a new embedded OpenCode session..." {
		t.Fatalf("status = %q, want fresh OpenCode session notice", got.status)
	}

	msg := cmd()
	opened, ok := msg.(codexSessionOpenedMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want codexSessionOpenedMsg", msg)
	}
	if opened.err != nil {
		t.Fatalf("/new returned error = %v", opened.err)
	}
	if opened.status != "Fresh embedded OpenCode session ses-new1 opened. Esc hides it." {
		t.Fatalf("opened.status = %q, want fresh OpenCode session confirmation", opened.status)
	}
	if len(requests) != 2 {
		t.Fatalf("launch requests = %d, want 2", len(requests))
	}
	if !requests[1].ForceNew {
		t.Fatalf("second launch request should force a fresh OpenCode session")
	}

	updated, _ = got.Update(opened)
	got = updated.(Model)
	snapshot, ok := got.currentCodexSnapshot()
	if !ok {
		t.Fatalf("currentCodexSnapshot() unavailable after handling the opened session")
	}
	if snapshot.ThreadID != "ses-new1" {
		t.Fatalf("thread id = %q, want %q", snapshot.ThreadID, "ses-new1")
	}
}

func TestCodexSessionOpenedMsgSeedsSnapshotCache(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Provider: codexapp.ProviderCodex,
			Started:  true,
			Status:   "Codex session ready",
			ThreadID: "thread-demo",
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})

	m := Model{
		codexManager:  manager,
		codexInput:    newCodexTextarea(),
		codexViewport: viewport.New(0, 0),
		width:         100,
		height:        24,
	}

	cmd := m.openCodexSessionCmd(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Preset:      codexcli.PresetYolo,
	})
	if cmd == nil {
		t.Fatalf("openCodexSessionCmd() returned nil")
	}
	msg := cmd()
	opened, ok := msg.(codexSessionOpenedMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want codexSessionOpenedMsg", msg)
	}
	if opened.err != nil {
		t.Fatalf("opened.err = %v, want nil", opened.err)
	}
	if opened.snapshot.ThreadID != "thread-demo" {
		t.Fatalf("opened.snapshot.ThreadID = %q, want %q", opened.snapshot.ThreadID, "thread-demo")
	}

	callsAfterOpen := session.snapshotCalls
	if callsAfterOpen == 0 {
		t.Fatalf("expected open command to snapshot the session off the UI thread")
	}

	updated, _ := m.update(opened)
	got := updated.(Model)
	if session.snapshotCalls != callsAfterOpen {
		t.Fatalf("handling codexSessionOpenedMsg should reuse the opened snapshot; snapshot calls = %d, want %d", session.snapshotCalls, callsAfterOpen)
	}
	snapshot, ok := got.currentCodexSnapshot()
	if !ok {
		t.Fatalf("currentCodexSnapshot() unavailable after handling the opened session")
	}
	if snapshot.ThreadID != "thread-demo" {
		t.Fatalf("current thread id = %q, want %q", snapshot.ThreadID, "thread-demo")
	}
	if session.snapshotCalls != callsAfterOpen {
		t.Fatalf("currentCodexSnapshot() should reuse the cached opened snapshot; snapshot calls = %d, want %d", session.snapshotCalls, callsAfterOpen)
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
	if got.status != "Embedded session open failed (use /errors)" {
		t.Fatalf("status = %q, want embedded-session-open-failed notice", got.status)
	}
	if got.err != nil {
		t.Fatalf("got.err = %v, want nil after logging", got.err)
	}
	if len(got.errorLogEntries) == 0 || got.errorLogEntries[0].Message != "opencode create failed" {
		t.Fatalf("latest error log entry = %#v, want opencode create failed", got.errorLogEntries)
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

func TestLaunchOpenCodeForSelectionFailureKeepsErrorPlaceholderVisible(t *testing.T) {
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return nil, fmt.Errorf("opencode create failed")
	})

	m := Model{
		codexManager: manager,
		projects: []model.ProjectSummary{{
			Path:          "/tmp/demo",
			Name:          "demo",
			PresentOnDisk: true,
		}},
		selected:      0,
		codexInput:    newCodexTextarea(),
		codexDrafts:   make(map[string]codexDraft),
		codexViewport: viewport.New(0, 0),
		width:         100,
		height:        24,
	}

	updated, cmd := m.launchOpenCodeForSelection(false, "")
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("launchOpenCodeForSelection() should return an open command")
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
		t.Fatalf("open returned error = %v, want opencode create failed", opened.err)
	}

	updated, _ = got.Update(opened)
	got = updated.(Model)
	if got.codexVisibleProject != "/tmp/demo" {
		t.Fatalf("codexVisibleProject = %q, want failed OpenCode project to stay visible", got.codexVisibleProject)
	}
	if got.codexHiddenProject != "/tmp/demo" {
		t.Fatalf("codexHiddenProject = %q, want failed OpenCode project to stay restorable", got.codexHiddenProject)
	}
	snapshot, ok := got.currentCodexSnapshot()
	if !ok {
		t.Fatalf("currentCodexSnapshot() unavailable after failed open")
	}
	if snapshot.Provider != codexapp.ProviderOpenCode {
		t.Fatalf("snapshot.Provider = %q, want %q", snapshot.Provider, codexapp.ProviderOpenCode)
	}
	if !snapshot.Closed {
		t.Fatalf("snapshot.Closed = false, want closed placeholder after failed open")
	}
	if snapshot.LastError != "opencode create failed" {
		t.Fatalf("snapshot.LastError = %q, want opencode create failed", snapshot.LastError)
	}
	if got.status != "Embedded session open failed (use /errors)" {
		t.Fatalf("status = %q, want embedded-session-open-failed notice", got.status)
	}
	if got.err != nil {
		t.Fatalf("got.err = %v, want nil after logging", got.err)
	}
	if len(got.errorLogEntries) == 0 || got.errorLogEntries[0].Message != "opencode create failed" {
		t.Fatalf("latest error log entry = %#v, want opencode create failed", got.errorLogEntries)
	}
	rendered := ansi.Strip(got.renderCodexView())
	if !strings.Contains(rendered, "OpenCode session closed.") {
		t.Fatalf("rendered view should keep showing the failed OpenCode placeholder, got %q", rendered)
	}
	if !strings.Contains(rendered, "opencode create failed") {
		t.Fatalf("rendered view should show the OpenCode startup error, got %q", rendered)
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

func TestLaunchEmbeddedForSelectionBlocksWhileAnotherEmbeddedProviderIsActive(t *testing.T) {
	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider: req.Provider.Normalized(),
				Started:  true,
				Busy:     req.Provider.Normalized() == codexapp.ProviderClaudeCode,
				Status:   req.Provider.Label() + " session ready",
			},
		}, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Provider:    codexapp.ProviderClaudeCode,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	m := Model{
		codexManager: manager,
		projects: []model.ProjectSummary{{
			Path:          "/tmp/demo",
			Name:          "demo",
			PresentOnDisk: true,
		}},
		selected: 0,
	}

	updated, cmd := m.launchEmbeddedForSelection(codexapp.ProviderCodex, true, "")
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("launchEmbeddedForSelection() cmd = %#v, want nil when another embedded provider is active", cmd)
	}
	wantStatus := "This project already has an active embedded Claude Code session. Finish or close it before starting Codex here."
	if got.status != wantStatus {
		t.Fatalf("status = %q, want %q", got.status, wantStatus)
	}
	if got.attentionDialog == nil {
		t.Fatalf("launchEmbeddedForSelection() should show an attention dialog when another embedded provider is active")
	}
	if got.attentionDialog.Title != "Launch blocked" {
		t.Fatalf("attention dialog title = %q, want launch blocked", got.attentionDialog.Title)
	}
	if got.attentionDialog.PrimaryProvider != codexapp.ProviderClaudeCode {
		t.Fatalf("attention dialog provider = %q, want Claude Code", got.attentionDialog.PrimaryProvider)
	}
	if got.attentionDialog.PrimaryLabel != "Open Claude Code" {
		t.Fatalf("attention dialog primary label = %q, want open action", got.attentionDialog.PrimaryLabel)
	}
	rendered := ansi.Strip(got.renderAttentionDialogContent(72))
	if !strings.Contains(rendered, "This project already has an active embedded Claude Code session.") ||
		!strings.Contains(rendered, "Finish") ||
		!strings.Contains(rendered, "Open Claude Code") {
		t.Fatalf("attention dialog should surface the blocked launch and open action, got %q", rendered)
	}
	if len(requests) != 1 {
		t.Fatalf("launch requests = %d, want only the original Claude open", len(requests))
	}
}

func TestLaunchEmbeddedForSelectionBlocksWhileAnotherProviderSessionIsUnfinished(t *testing.T) {
	now := time.Date(2026, 3, 30, 20, 30, 0, 0, time.UTC)
	m := Model{
		nowFn: func() time.Time { return now },
		projects: []model.ProjectSummary{{
			Path:                     "/tmp/demo",
			Name:                     "demo",
			PresentOnDisk:            true,
			LatestSessionID:          "cc-old",
			LatestSessionFormat:      "claude_code",
			LatestSessionLastEventAt: now.Add(-10 * time.Minute),
			LatestTurnStateKnown:     true,
			LatestTurnCompleted:      false,
		}},
		selected: 0,
	}

	updated, cmd := m.launchEmbeddedForSelection(codexapp.ProviderCodex, true, "")
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("launchEmbeddedForSelection() cmd = %#v, want nil when another provider session is still unfinished", cmd)
	}
	wantStatus := "This project already has an unfinished Claude Code session. Finish or close it before starting Codex here."
	if got.status != wantStatus {
		t.Fatalf("status = %q, want %q", got.status, wantStatus)
	}
	if got.attentionDialog == nil {
		t.Fatalf("launchEmbeddedForSelection() should show an attention dialog for unfinished external sessions")
	}
	if got.attentionDialog.PrimaryProvider != codexapp.ProviderClaudeCode {
		t.Fatalf("attention dialog provider = %q, want Claude Code", got.attentionDialog.PrimaryProvider)
	}
	if got.attentionDialog.PrimaryLabel != "Resume Claude Code" {
		t.Fatalf("attention dialog primary label = %q, want resume action", got.attentionDialog.PrimaryLabel)
	}
}

func TestAttentionDialogEnterOpensExistingEmbeddedSession(t *testing.T) {
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider: req.Provider.Normalized(),
				Started:  true,
				ThreadID: "thread-live",
				Status:   req.Provider.Label() + " session ready",
			},
		}, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Provider:    codexapp.ProviderClaudeCode,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	m := Model{
		codexManager: manager,
		projects: []model.ProjectSummary{{
			Path:          "/tmp/demo",
			Name:          "demo",
			PresentOnDisk: true,
		}},
		selected: 0,
		attentionDialog: &attentionDialogState{
			Title:           "Launch blocked",
			ProjectName:     "demo",
			ProjectPath:     "/tmp/demo",
			Message:         "This project already has an active embedded Claude Code session. Finish or close it before starting Codex here.",
			PrimaryLabel:    "Open Claude Code",
			PrimaryProvider: codexapp.ProviderClaudeCode,
			Selected:        attentionDialogFocusPrimary,
		},
	}

	updated, cmd := m.updateAttentionDialogMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("attention dialog enter on the primary action should return an open command")
	}
	if got.attentionDialog != nil {
		t.Fatalf("attention dialog should close after taking the primary action")
	}
	if got.codexVisibleProject != "/tmp/demo" {
		t.Fatalf("codexVisibleProject = %q, want /tmp/demo", got.codexVisibleProject)
	}
}

func TestLaunchEmbeddedForSelectionAllowsStaleUnfinishedSessionOutsideProtectionWindow(t *testing.T) {
	now := time.Date(2026, 3, 30, 20, 30, 0, 0, time.UTC)
	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider: req.Provider.Normalized(),
				Started:  true,
				Preset:   req.Preset,
				Status:   req.Provider.Label() + " session ready",
			},
		}, nil
	})

	m := Model{
		nowFn:        func() time.Time { return now },
		codexManager: manager,
		projects: []model.ProjectSummary{{
			Path:                     "/tmp/demo",
			Name:                     "demo",
			PresentOnDisk:            true,
			LatestSessionFormat:      "claude_code",
			LatestSessionLastEventAt: now.Add(-6 * time.Hour),
			LatestTurnStateKnown:     true,
			LatestTurnCompleted:      false,
		}},
		selected: 0,
	}

	updated, cmd := m.launchEmbeddedForSelection(codexapp.ProviderCodex, true, "")
	if cmd == nil {
		t.Fatalf("launchEmbeddedForSelection() should allow stale unfinished sessions outside the protection window")
	}
	msg := cmd()
	opened, ok := msg.(codexSessionOpenedMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want codexSessionOpenedMsg", msg)
	}
	if opened.err != nil {
		t.Fatalf("opened.err = %v, want nil", opened.err)
	}
	if len(requests) != 1 {
		t.Fatalf("launch requests = %d, want 1 fresh Codex open", len(requests))
	}
	if requests[0].Provider != codexapp.ProviderCodex {
		t.Fatalf("provider = %q, want %q", requests[0].Provider, codexapp.ProviderCodex)
	}
	_ = updated
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
	if opened.status != "Fresh embedded Codex session 019dddd4 opened. Esc hides it." {
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

func TestLaunchCodexForSelectionForceNewRetriesWhenCodexRejectsFreshThread(t *testing.T) {
	const freshThreadID = "019fresh4efgh"
	const prompt = "continue in the new thread"

	var (
		requests []codexapp.LaunchRequest
		created  []*fakeCodexSession
	)
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		if len(requests) == 1 {
			return nil, &codexapp.ForceNewSessionReusedError{ThreadID: "019stale3abcd"}
		}
		session := &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider: codexapp.ProviderCodex,
				Started:  true,
				Preset:   req.Preset,
				Status:   "Codex session ready",
				ThreadID: freshThreadID,
			},
		}
		created = append(created, session)
		return session, nil
	})

	m := Model{
		codexManager: manager,
		projects: []model.ProjectSummary{{
			Path:          "/tmp/demo",
			Name:          "demo",
			PresentOnDisk: true,
		}},
	}

	updated, cmd := m.launchCodexForSelection(true, prompt)
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
	if opened.status != "Prompt sent to fresh embedded Codex session 019fresh. Esc hides it." {
		t.Fatalf("opened.status = %q, want prompt-sent status after retry", opened.status)
	}
	if len(requests) != 2 {
		t.Fatalf("launch requests = %d, want 2 after retryable fresh-thread failure", len(requests))
	}
	if !requests[0].ForceNew || !requests[1].ForceNew {
		t.Fatalf("launch requests should keep ForceNew enabled across retries: %#v", requests)
	}
	if requests[1].Prompt != prompt {
		t.Fatalf("second launch prompt = %q, want the original inline prompt after retry", requests[1].Prompt)
	}
	if len(created) != 1 {
		t.Fatalf("created sessions = %d, want 1 successful fresh session", len(created))
	}

	updated, _ = updated.(Model).Update(opened)
	got := updated.(Model)
	snapshot, ok := got.currentCodexSnapshot()
	if !ok {
		t.Fatalf("currentCodexSnapshot() unavailable after handling the opened session")
	}
	if snapshot.ThreadID != freshThreadID {
		t.Fatalf("thread id = %q, want retried fresh thread %q", snapshot.ThreadID, freshThreadID)
	}
}

func TestLaunchOpenCodeForSelectionForceNewRetriesWhenOpenCodeRejectsFreshSession(t *testing.T) {
	const freshSessionID = "ses_fresh4efgh"
	const prompt = "continue in the fresh OpenCode session"

	var (
		requests []codexapp.LaunchRequest
		created  []*fakeCodexSession
	)
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		if len(requests) == 1 {
			return nil, &codexapp.ForceNewSessionReusedError{Provider: codexapp.ProviderOpenCode, ThreadID: "ses_stale3abcd"}
		}
		session := &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider: codexapp.ProviderOpenCode,
				Started:  true,
				Preset:   req.Preset,
				Status:   "OpenCode session ready",
				ThreadID: freshSessionID,
			},
		}
		created = append(created, session)
		return session, nil
	})

	m := Model{
		codexManager: manager,
		projects: []model.ProjectSummary{{
			Path:                "/tmp/demo",
			Name:                "demo",
			PresentOnDisk:       true,
			LatestSessionFormat: "opencode_db",
		}},
	}

	updated, cmd := m.launchOpenCodeForSelection(true, prompt)
	if cmd == nil {
		t.Fatalf("launchOpenCodeForSelection() should return an open command")
	}

	msg := cmd()
	opened, ok := msg.(codexSessionOpenedMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want codexSessionOpenedMsg", msg)
	}
	if opened.err != nil {
		t.Fatalf("/opencode-new returned error = %v", opened.err)
	}
	if opened.status != "Prompt sent to fresh embedded OpenCode session ses_fres. Esc hides it." {
		t.Fatalf("opened.status = %q, want prompt-sent status after retry", opened.status)
	}
	if len(requests) != 2 {
		t.Fatalf("launch requests = %d, want 2 after retryable fresh-session failure", len(requests))
	}
	if !requests[0].ForceNew || !requests[1].ForceNew {
		t.Fatalf("launch requests should keep ForceNew enabled across retries: %#v", requests)
	}
	if requests[1].Prompt != prompt {
		t.Fatalf("second launch prompt = %q, want the original inline prompt after retry", requests[1].Prompt)
	}
	if len(created) != 1 {
		t.Fatalf("created sessions = %d, want 1 successful fresh session", len(created))
	}

	updated, _ = updated.(Model).Update(opened)
	got := updated.(Model)
	snapshot, ok := got.currentCodexSnapshot()
	if !ok {
		t.Fatalf("currentCodexSnapshot() unavailable after handling the opened session")
	}
	if snapshot.ThreadID != freshSessionID {
		t.Fatalf("thread id = %q, want retried fresh session %q", snapshot.ThreadID, freshSessionID)
	}
}

func TestLaunchOpenCodeForSelectionForceNewRetriesWhenOpenCodeReturnsKnownReusedSession(t *testing.T) {
	const staleSessionID = "ses_stale3abcd"
	const freshSessionID = "ses_fresh4efgh"
	const prompt = "continue with a third-force-new attempt"

	var (
		requests []codexapp.LaunchRequest
		created  []*fakeCodexSession
	)
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		switch len(requests) {
		case 1:
			return nil, &codexapp.ForceNewSessionReusedError{Provider: codexapp.ProviderOpenCode, ThreadID: staleSessionID}
		case 2:
			session := &fakeCodexSession{
				projectPath: req.ProjectPath,
				snapshot: codexapp.Snapshot{
					Provider: codexapp.ProviderOpenCode,
					Started:  true,
					Preset:   req.Preset,
					Status:   "OpenCode session ready",
					ThreadID: staleSessionID,
				},
			}
			created = append(created, session)
			return session, nil
		default:
			session := &fakeCodexSession{
				projectPath: req.ProjectPath,
				snapshot: codexapp.Snapshot{
					Provider: codexapp.ProviderOpenCode,
					Started:  true,
					Preset:   req.Preset,
					Status:   "OpenCode session ready",
					ThreadID: freshSessionID,
				},
			}
			created = append(created, session)
			return session, nil
		}
	})

	m := Model{
		codexManager: manager,
		projects: []model.ProjectSummary{{
			Path:                "/tmp/demo",
			Name:                "demo",
			PresentOnDisk:       true,
			LatestSessionFormat: "opencode_db",
		}},
	}

	updated, cmd := m.launchOpenCodeForSelection(true, prompt)
	if cmd == nil {
		t.Fatalf("launchOpenCodeForSelection() should return an open command")
	}

	msg := cmd()
	opened, ok := msg.(codexSessionOpenedMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want codexSessionOpenedMsg", msg)
	}
	if opened.err != nil {
		t.Fatalf("/opencode-new returned error = %v", opened.err)
	}
	if opened.status != "Prompt sent to fresh embedded OpenCode session ses_fres. Esc hides it." {
		t.Fatalf("opened.status = %q, want prompt-sent status after stale-session retry", opened.status)
	}
	if len(requests) != 3 {
		t.Fatalf("launch requests = %d, want 3 when stale thread is not previous or resume", len(requests))
	}
	if requests[0].Prompt != prompt || requests[1].Prompt != prompt || requests[2].Prompt != prompt {
		t.Fatalf("launch prompts changed across retries: %#v", []string{requests[0].Prompt, requests[1].Prompt, requests[2].Prompt})
	}
	if !requests[0].ForceNew || !requests[1].ForceNew || !requests[2].ForceNew {
		t.Fatalf("launch requests should keep ForceNew enabled across retries: %#v", requests)
	}
	if len(created) != 2 {
		t.Fatalf("created sessions = %d, want 2 successful attempts (reused then fresh)", len(created))
	}

	updated, _ = updated.(Model).Update(opened)
	got := updated.(Model)
	snapshot, ok := got.currentCodexSnapshot()
	if !ok {
		t.Fatalf("currentCodexSnapshot() unavailable after handling the opened session")
	}
	if snapshot.ThreadID != freshSessionID {
		t.Fatalf("thread id = %q, want retried fresh session %q", snapshot.ThreadID, freshSessionID)
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
	if got.codexInput.Value() != "[Paste #1: 1 line] " {
		t.Fatalf("composer = %q, want large paste placeholder", got.codexInput.Value())
	}
	pastedTexts := got.currentCodexPastedTexts()
	if len(pastedTexts) != 1 {
		t.Fatalf("pasted texts = %d, want 1", len(pastedTexts))
	}
	if pastedTexts[0].Text != strings.Repeat("a", 1200) {
		t.Fatalf("stored pasted text length = %d, want 1200", len([]rune(pastedTexts[0].Text)))
	}
	if got.status != "Pasted [1 line pasted] as a placeholder" {
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
	if got.codexInput.Value() != "[Paste #1: 1 line] " {
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
	if got.status != "Removed [1 line pasted] placeholder" {
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
	token := "[Paste #1: 1 line]"
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
	if submission.DisplayText != "[1 line pasted] summarize this" {
		t.Fatalf("submission display text = %q, want collapsed paste placeholder", submission.DisplayText)
	}
	if got.codexInput.Value() != "" {
		t.Fatalf("composer should clear after submit, got %q", got.codexInput.Value())
	}
}

func TestRenderCodexTranscriptShowsFullTypedText(t *testing.T) {
	longText := strings.Repeat("z", 650)
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Started: true,
			Preset:  codexcli.PresetYolo,
			Status:  "Codex session ready",
			Entries: []codexapp.TranscriptEntry{
				{Kind: codexapp.TranscriptUser, Text: longText},
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
	if strings.Contains(rendered, "[650 characters]") {
		t.Fatalf("rendered transcript should NOT collapse typed text: %q", rendered)
	}
	if !strings.Contains(rendered, strings.Repeat("z", 80)) {
		t.Fatalf("rendered transcript should include the full typed text")
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

func TestVisibleCodexCtrlCMarksClosedSessionSeenAndPersistsIt(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	seenAt := time.Date(2026, 4, 20, 10, 30, 0, 0, time.UTC)
	projectPath := filepath.Join(t.TempDir(), "demo")

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:          projectPath,
		Name:          "demo",
		PresentOnDisk: true,
		InScope:       true,
		UpdatedAt:     seenAt.Add(-time.Minute),
	}); err != nil {
		t.Fatalf("seed project state: %v", err)
	}

	svc := service.New(config.Default(), st, events.NewBus(), nil)
	project := model.ProjectSummary{
		Path:                            projectPath,
		Name:                            "demo",
		PresentOnDisk:                   true,
		LatestSessionClassification:     model.ClassificationCompleted,
		LatestSessionClassificationType: model.SessionCategoryCompleted,
		LatestSessionFormat:             "modern",
		LatestSessionLastEventAt:        seenAt.Add(-5 * time.Minute),
		LatestTurnStateKnown:            true,
		LatestTurnCompleted:             true,
	}

	session := &fakeCodexSession{
		projectPath: projectPath,
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
		ProjectPath: projectPath,
		Preset:      codexcli.PresetYolo,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	m := Model{
		ctx:                 ctx,
		svc:                 svc,
		nowFn:               func() time.Time { return seenAt },
		allProjects:         []model.ProjectSummary{project},
		projects:            []model.ProjectSummary{project},
		detail:              model.ProjectDetail{Summary: project},
		selected:            0,
		codexManager:        manager,
		codexVisibleProject: projectPath,
		codexHiddenProject:  projectPath,
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

	msg := cmd()
	action, ok := msg.(codexActionMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want codexActionMsg", msg)
	}
	if !action.closed {
		t.Fatalf("close action should mark the session as closed")
	}

	updated, followUp := got.Update(action)
	got = updated.(Model)
	if !got.projects[0].LastSessionSeenAt.Equal(seenAt) {
		t.Fatalf("projects seen_at = %v, want %v", got.projects[0].LastSessionSeenAt, seenAt)
	}
	if !got.detail.Summary.LastSessionSeenAt.Equal(seenAt) {
		t.Fatalf("detail seen_at = %v, want %v", got.detail.Summary.LastSessionSeenAt, seenAt)
	}
	if followUp == nil {
		t.Fatalf("close action should queue a seen-state write")
	}

	var seenMsg projectSessionSeenMsg
	foundSeenMsg := false
	for _, followUpMsg := range collectCmdMsgs(followUp) {
		candidate, ok := followUpMsg.(projectSessionSeenMsg)
		if !ok {
			continue
		}
		seenMsg = candidate
		foundSeenMsg = true
		break
	}
	if !foundSeenMsg {
		t.Fatalf("follow-up work should include projectSessionSeenMsg")
	}
	if seenMsg.err != nil {
		t.Fatalf("mark seen follow-up error = %v, want nil", seenMsg.err)
	}

	summary, err := st.GetProjectSummary(ctx, projectPath, false)
	if err != nil {
		t.Fatalf("GetProjectSummary() error = %v", err)
	}
	if !summary.LastSessionSeenAt.Equal(seenAt) {
		t.Fatalf("stored seen_at = %v, want %v", summary.LastSessionSeenAt, seenAt)
	}

	updated, refreshCmd := got.Update(seenMsg)
	got = updated.(Model)
	if refreshCmd == nil {
		t.Fatalf("marking the closed session seen should queue a refresh")
	}
	if got.codexVisibleProject != "" {
		t.Fatalf("codexVisibleProject = %q, want closed", got.codexVisibleProject)
	}
}

func TestCodexUpdateMissingIdleSessionDoesNotResurfaceUnread(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	projectPath := filepath.Join(t.TempDir(), "demo")
	baseEventAt := time.Date(2026, 4, 20, 10, 0, 0, 0, time.UTC)
	seenAt := baseEventAt.Add(5 * time.Minute)

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	session := model.NormalizeSessionEvidenceIdentity(model.SessionEvidence{
		Source:               model.SessionSourceOpenCode,
		SessionID:            "ses-open",
		RawSessionID:         "ses-open",
		ProjectPath:          projectPath,
		DetectedProjectPath:  projectPath,
		SessionFile:          filepath.Join(t.TempDir(), "opencode.db") + "#session:ses-open",
		Format:               "opencode_db",
		SnapshotHash:         "snapshot-open",
		StartedAt:            baseEventAt.Add(-10 * time.Minute),
		LastEventAt:          baseEventAt,
		LatestTurnStateKnown: true,
		LatestTurnCompleted:  true,
	})
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:           projectPath,
		Name:           "demo",
		LastActivity:   baseEventAt,
		Status:         model.StatusIdle,
		AttentionScore: 0,
		PresentOnDisk:  true,
		InScope:        true,
		UpdatedAt:      seenAt.Add(-time.Minute),
		Sessions:       []model.SessionEvidence{session},
	}); err != nil {
		t.Fatalf("seed project state: %v", err)
	}

	classification := model.SessionClassification{
		SessionID:         session.SessionID,
		Source:            session.Source,
		RawSessionID:      session.RawSessionID,
		ProjectPath:       projectPath,
		SessionFile:       session.SessionFile,
		SessionFormat:     session.Format,
		SnapshotHash:      session.SnapshotHash,
		Model:             "gpt-5.4-mini",
		ClassifierVersion: "v1",
		SourceUpdatedAt:   baseEventAt,
	}
	if queued, err := st.QueueSessionClassification(ctx, classification, time.Minute); err != nil || !queued {
		t.Fatalf("queue classification: queued=%v err=%v", queued, err)
	}
	claimed, err := st.ClaimNextPendingSessionClassification(ctx, time.Minute)
	if err != nil {
		t.Fatalf("claim classification: %v", err)
	}
	claimed.Category = model.SessionCategoryCompleted
	claimed.Summary = "Done."
	claimed.Confidence = 0.92
	if err := st.CompleteSessionClassification(ctx, claimed); err != nil {
		t.Fatalf("complete classification: %v", err)
	}
	if err := st.SetProjectSessionSeenAt(ctx, projectPath, seenAt); err != nil {
		t.Fatalf("set seen at: %v", err)
	}

	before, err := st.GetProjectSummary(ctx, projectPath, false)
	if err != nil {
		t.Fatalf("summary before update: %v", err)
	}
	if !before.LastSessionSeenAt.Equal(seenAt) {
		t.Fatalf("seen_at before update = %v, want %v", before.LastSessionSeenAt, seenAt)
	}
	if !before.LatestSessionLastEventAt.Equal(baseEventAt) {
		t.Fatalf("last event before update = %v, want %v", before.LatestSessionLastEventAt, baseEventAt)
	}
	if attention.AssessmentUnread(before) {
		t.Fatalf("project should start read, got %#v", before)
	}

	svc := service.New(config.Default(), st, events.NewBus(), nil)
	m := Model{
		ctx: ctx,
		svc: svc,
		codexSnapshots: map[string]codexapp.Snapshot{
			projectPath: {
				Provider:       codexapp.ProviderOpenCode,
				ProjectPath:    projectPath,
				ThreadID:       "ses-open",
				Started:        true,
				Busy:           false,
				LastActivityAt: seenAt.Add(2 * time.Minute),
				Status:         "OpenCode session ready",
			},
		},
		codexInput:    newCodexTextarea(),
		codexViewport: viewport.New(0, 0),
		width:         100,
		height:        24,
	}

	updated, cmd := m.Update(codexUpdateMsg{projectPath: projectPath})
	got := updated.(Model)
	if _, ok := got.codexSnapshots[projectPath]; ok {
		t.Fatalf("missing session update should drop the stale cached snapshot")
	}

	msgs := collectCmdMsgs(cmd)
	for _, msg := range msgs {
		if refreshMsg, ok := msg.(projectStatusRefreshedMsg); ok && refreshMsg.err != nil {
			t.Fatalf("projectStatusRefreshedMsg err = %v", refreshMsg.err)
		}
	}

	after, err := st.GetProjectSummary(ctx, projectPath, false)
	if err != nil {
		t.Fatalf("summary after update: %v", err)
	}
	if !after.LastSessionSeenAt.Equal(seenAt) {
		t.Fatalf("seen_at after update = %v, want %v", after.LastSessionSeenAt, seenAt)
	}
	if !after.LatestSessionLastEventAt.Equal(baseEventAt) {
		t.Fatalf("last event after update = %v, want %v", after.LatestSessionLastEventAt, baseEventAt)
	}
	if attention.AssessmentUnread(after) {
		t.Fatalf("idle missing session should stay read, got %#v", after)
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

func TestVisibleCodexCtrlCDoesNotInterruptCompactingSession(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Started: true,
			Preset:  codexcli.PresetYolo,
			Phase:   codexapp.SessionPhaseReconciling,
			Status:  "Compacting conversation history...",
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
		t.Fatalf("ctrl+c should not interrupt a compacting session")
	}
	if session.interrupted {
		t.Fatalf("session should not be interrupted while compacting")
	}
	if !strings.Contains(strings.ToLower(got.status), "compacting conversation history") {
		t.Fatalf("status = %q, want compacting guidance", got.status)
	}
}

func TestVisibleCodexCtrlCInterruptsStalledBusySession(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Started:      true,
			Preset:       codexcli.PresetYolo,
			Busy:         true,
			Phase:        codexapp.SessionPhaseStalled,
			ActiveTurnID: "turn-live",
			Status:       "Embedded Codex session seems stuck or disconnected. Use /reconnect.",
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
		t.Fatalf("ctrl+c should interrupt a stalled busy embedded Codex session")
	}
	if got.status != "Interrupting stuck Codex turn..." {
		t.Fatalf("status = %q, want stuck interrupt notice", got.status)
	}

	msg := cmd()
	action, ok := msg.(codexActionMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want codexActionMsg", msg)
	}
	if action.err != nil {
		t.Fatalf("interrupt action error = %v, want nil", action.err)
	}
	if !session.interrupted {
		t.Fatalf("session should be interrupted")
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

func TestVisibleCodexEnterDoesNotSubmitWhileCompacting(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Started: true,
			Preset:  codexcli.PresetYolo,
			Phase:   codexapp.SessionPhaseReconciling,
			Status:  "Compacting conversation history...",
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
		t.Fatalf("enter should not submit while compaction is running")
	}
	if len(session.submissions) != 0 {
		t.Fatalf("submissions = %d, want 0", len(session.submissions))
	}
	if got.codexInput.Value() != "please continue" {
		t.Fatalf("composer = %q, want draft preserved", got.codexInput.Value())
	}
	if !strings.Contains(strings.ToLower(got.status), "compacting conversation history") {
		t.Fatalf("status = %q, want compacting guidance", got.status)
	}
}

func TestVisibleCodexCompactSlashUsesStartAndCompletionMessages(t *testing.T) {
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
	input.SetValue("/compact")

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
		t.Fatalf("enter should queue compaction")
	}
	if got.status != "Starting embedded Codex conversation compaction..." {
		t.Fatalf("status = %q, want explicit compaction start message", got.status)
	}
	if session.compactCalls != 0 {
		t.Fatalf("compact calls = %d before cmd runs, want 0", session.compactCalls)
	}

	msg := cmd()
	action, ok := msg.(codexActionMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want codexActionMsg", msg)
	}
	if action.err != nil {
		t.Fatalf("compaction returned error = %v", action.err)
	}
	if action.status != "Embedded Codex conversation compaction completed" {
		t.Fatalf("action status = %q, want explicit compaction completion message", action.status)
	}
	if session.compactCalls != 1 {
		t.Fatalf("compact calls = %d, want 1", session.compactCalls)
	}
}

func TestVisibleCodexAltUpDoesNotHideSession(t *testing.T) {
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
		t.Fatalf("alt+up should not queue a command")
	}
	if got.codexVisibleProject != "/tmp/demo" {
		t.Fatalf("codexVisibleProject = %q, want still visible", got.codexVisibleProject)
	}
	if got.status != "" {
		t.Fatalf("status = %q, want unchanged", got.status)
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

func TestVisibleCodexEscCancelsInputSelectionBeforeHiding(t *testing.T) {
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
	input.SetValue("select me")
	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          input,
		codexViewport:       viewport.New(0, 0),
		width:               100,
		height:              24,
	}
	m.startCodexInputSelection()

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyEsc})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("esc selection cancel should not queue a command")
	}
	if got.codexVisibleProject != "/tmp/demo" {
		t.Fatalf("codexVisibleProject = %q, want still visible", got.codexVisibleProject)
	}
	if got.codexInputSelectionActive() {
		t.Fatalf("selection should be canceled before hiding")
	}
	if got.status != "Text selection canceled" {
		t.Fatalf("status = %q, want selection cancel notice", got.status)
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
		codexSnapshots: map[string]codexapp.Snapshot{
			"/tmp/a": sessionA.snapshot,
			"/tmp/b": sessionB.snapshot,
		},
		codexInput:    newCodexTextarea(),
		codexDrafts:   make(map[string]codexDraft),
		codexViewport: viewport.New(0, 0),
		width:         100,
		height:        24,
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
		codexSnapshots: map[string]codexapp.Snapshot{
			"/tmp/a": sessionA.snapshot,
			"/tmp/b": sessionB.snapshot,
		},
		codexInput:    newCodexTextarea(),
		codexDrafts:   make(map[string]codexDraft),
		codexViewport: viewport.New(0, 0),
		width:         100,
		height:        24,
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

func TestVisibleCodexAltLCyclesDenseBlockModes(t *testing.T) {
	m := Model{
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexSnapshots: map[string]codexapp.Snapshot{
			"/tmp/demo": {
				Started: true,
				Status:  "Codex session ready",
				Entries: []codexapp.TranscriptEntry{{
					Kind: codexapp.TranscriptCommand,
					Text: "$ demo\nline 1\nline 2\nline 3\nline 4\nline 5\nline 6",
				}},
			},
		},
		codexInput:    newCodexTextarea(),
		codexViewport: viewport.New(0, 0),
		width:         100,
		height:        24,
	}

	for _, want := range []struct {
		mode   codexDenseBlockMode
		status string
	}{
		{codexDenseBlockPreview, "Showing short transcript block previews"},
		{codexDenseBlockFull, "Showing full transcript blocks"},
		{codexDenseBlockSummary, "Hiding transcript block output"},
	} {
		updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'l'}, Alt: true})
		if cmd != nil {
			t.Fatalf("alt+l should not return an async command")
		}
		got := updated.(Model)
		if got.codexDenseBlockMode != want.mode {
			t.Fatalf("codexDenseBlockMode = %v, want %v", got.codexDenseBlockMode, want.mode)
		}
		if got.status != want.status {
			t.Fatalf("status = %q, want %q", got.status, want.status)
		}
		m = got
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
		codexSnapshots: map[string]codexapp.Snapshot{
			"/tmp/a": sessionA.snapshot,
			"/tmp/b": sessionB.snapshot,
		},
		codexInput:    newCodexTextarea(),
		codexDrafts:   make(map[string]codexDraft),
		codexViewport: viewport.New(0, 0),
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

	updated, hideCmd := got.updateCodexMode(tea.KeyMsg{Type: tea.KeyEsc})
	got = updated.(Model)
	if hideCmd == nil && !got.detailReloadQueued["/tmp/b"] {
		t.Fatalf("esc should refresh or queue a detail refresh for the last embedded project")
	}
	if got.codexVisibleProject != "" {
		t.Fatalf("codexVisibleProject = %q, want hidden", got.codexVisibleProject)
	}
	if got.codexHiddenProject != "/tmp/b" {
		t.Fatalf("codexHiddenProject = %q, want /tmp/b", got.codexHiddenProject)
	}
	if project, ok := got.selectedProject(); !ok || project.Path != "/tmp/b" {
		t.Fatalf("selected project after esc = %#v, want /tmp/b", project)
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
	if requests[0].Preset != codexcli.PresetYolo {
		t.Fatalf("preset = %q, want %q for OpenCode", requests[0].Preset, codexcli.PresetYolo)
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

func TestNormalModeEnterPrefersLiveEmbeddedProviderOverStoredLatestProvider(t *testing.T) {
	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider: req.Provider.Normalized(),
				Started:  true,
				ThreadID: "thread-live",
				Status:   req.Provider.Label() + " session ready",
			},
		}, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Provider:    codexapp.ProviderCodex,
		Preset:      codexcli.PresetYolo,
		ResumeID:    "thread-live",
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	project := model.ProjectSummary{
		Path:                "/tmp/demo",
		Name:                "demo",
		PresentOnDisk:       true,
		LatestSessionID:     "cc-old",
		LatestSessionFormat: "claude_code",
	}
	m := Model{
		codexManager:  manager,
		projects:      []model.ProjectSummary{project},
		selected:      0,
		focusedPane:   focusProjects,
		codexInput:    newCodexTextarea(),
		codexDrafts:   make(map[string]codexDraft),
		codexViewport: viewport.New(0, 0),
		width:         100,
		height:        24,
	}

	updated, cmd := m.updateNormalMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("enter should return a show-session command")
	}
	if got.codexVisibleProject != "/tmp/demo" {
		t.Fatalf("codexVisibleProject = %q, want /tmp/demo", got.codexVisibleProject)
	}
	if got.status != "Embedded Codex session reopened. Esc hides it." {
		t.Fatalf("status = %q, want live Codex reopen status", got.status)
	}
	if len(requests) != 1 {
		t.Fatalf("launch requests = %d, want only the original live Codex open", len(requests))
	}
}

func TestNormalModeEnterReusesLiveEmbeddedSessionWhenSnapshotIsContended(t *testing.T) {
	var requests []codexapp.LaunchRequest
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Provider: codexapp.ProviderCodex,
			Started:  true,
			ThreadID: "thread-live",
			Status:   "Codex session ready",
		},
		trySnapshotFn: func(*fakeCodexSession) (codexapp.Snapshot, bool) {
			return codexapp.Snapshot{}, false
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/demo",
		Provider:    codexapp.ProviderCodex,
		Preset:      codexcli.PresetYolo,
		ResumeID:    "thread-live",
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	m := Model{
		codexManager: manager,
		projects: []model.ProjectSummary{{
			Path:                "/tmp/demo",
			Name:                "demo",
			PresentOnDisk:       true,
			LatestSessionID:     "thread-stale",
			LatestSessionFormat: "modern",
		}},
		selected:       0,
		focusedPane:    focusProjects,
		codexInput:     newCodexTextarea(),
		codexDrafts:    make(map[string]codexDraft),
		codexSnapshots: make(map[string]codexapp.Snapshot),
		codexViewport:  viewport.New(0, 0),
		width:          100,
		height:         24,
	}

	updated, cmd := m.updateNormalMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if got.codexVisibleProject != "/tmp/demo" {
		t.Fatalf("codexVisibleProject = %q, want /tmp/demo", got.codexVisibleProject)
	}
	if got.status != "Embedded Codex session reopened. Esc hides it." {
		t.Fatalf("status = %q, want live Codex reopen status", got.status)
	}
	if len(requests) != 1 {
		t.Fatalf("launch requests = %d, want no replacement open after the original session", len(requests))
	}
	if cmd == nil {
		t.Fatalf("showing a contended live session should queue a deferred snapshot command")
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

func TestVisibleCodexURLBasedElicitationCanOpenBrowser(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Started:  true,
			ThreadID: "thread-demo",
			Preset:   codexcli.PresetYolo,
			Status:   "Browser needs attention",
			BrowserActivity: browserctl.SessionActivity{
				Policy:     settingsAutomaticPlaywrightPolicy,
				State:      browserctl.SessionActivityStateWaitingForUser,
				ServerName: "playwright",
				ToolName:   "browser_navigate",
			},
			ManagedBrowserSessionKey: "managed-demo",
			PendingElicitation: &codexapp.ElicitationRequest{
				ID:   "elicitation_1",
				Mode: codexapp.ElicitationModeURL,
				URL:  "https://example.test/login",
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

	previousSessionRevealer := managedBrowserSessionRevealer
	defer func() {
		managedBrowserSessionRevealer = previousSessionRevealer
	}()

	revealedSessionKey := ""
	managedBrowserSessionRevealer = func(_ string, sessionKey string) (browserctl.ManagedPlaywrightState, error) {
		revealedSessionKey = sessionKey
		return browserctl.ManagedPlaywrightState{SessionKey: sessionKey, BrowserPID: 123, RevealSupported: true}, nil
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

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("o")})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("o should queue the browser-open command")
	}
	if got.status != "Showing the managed browser window..." {
		t.Fatalf("status = %q, want managed browser reveal notice", got.status)
	}

	msg := cmd()
	openMsg, ok := msg.(browserOpenMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want browserOpenMsg", msg)
	}
	if openMsg.err != nil {
		t.Fatalf("browserOpenMsg.err = %v, want nil", openMsg.err)
	}
	if openMsg.status != "Managed browser window is ready. Finish the browser flow there, then press Enter when you are ready to continue." {
		t.Fatalf("browserOpenMsg.status = %q, want managed browser handoff status", openMsg.status)
	}
	if revealedSessionKey != "managed-demo" {
		t.Fatalf("revealed session key = %q, want managed-demo", revealedSessionKey)
	}
}

func TestVisibleCodexCanOpenCurrentBackgroundBrowserPage(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Started:                  true,
			ThreadID:                 "thread-demo",
			Preset:                   codexcli.PresetYolo,
			Status:                   "Codex session ready",
			BrowserActivity:          browserctl.SessionActivity{Policy: settingsAutomaticPlaywrightPolicy},
			ManagedBrowserSessionKey: "managed-demo",
			CurrentBrowserPageURL:    "https://chartboost.us.auth0.com/u/login?state=demo",
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

	previousSessionRevealer := managedBrowserSessionRevealer
	defer func() {
		managedBrowserSessionRevealer = previousSessionRevealer
	}()

	revealedSessionKey := ""
	managedBrowserSessionRevealer = func(_ string, sessionKey string) (browserctl.ManagedPlaywrightState, error) {
		revealedSessionKey = sessionKey
		return browserctl.ManagedPlaywrightState{SessionKey: sessionKey, BrowserPID: 123, RevealSupported: true}, nil
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

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyCtrlO})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("ctrl+o should queue the current-page browser-open command")
	}
	if got.status != "Showing the managed browser window..." {
		t.Fatalf("status = %q, want managed browser reveal notice", got.status)
	}

	msg := cmd()
	openMsg, ok := msg.(browserOpenMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want browserOpenMsg", msg)
	}
	if openMsg.err != nil {
		t.Fatalf("browserOpenMsg.err = %v, want nil", openMsg.err)
	}
	if openMsg.status != "Managed browser window is ready. Continue there, then return here when you want Codex to keep going." {
		t.Fatalf("browserOpenMsg.status = %q, want current page success status", openMsg.status)
	}
	if revealedSessionKey != "managed-demo" {
		t.Fatalf("revealed session key = %q, want managed-demo", revealedSessionKey)
	}

	updated, followupCmd := got.Update(openMsg)
	got = updated.(Model)
	if followupCmd != nil {
		t.Fatalf("browser-open followup should not queue more work")
	}
	renderedBlocks := ansi.Strip(got.renderCodexBrowserPanel(session.snapshot, 120))
	if strings.Contains(renderedBlocks, "Press Ctrl+O to reveal the managed browser window for this same session.") {
		t.Fatalf("renderCodexBrowserPanel() kept stale Ctrl+O reveal hint after successful reveal: %q", renderedBlocks)
	}
	if !strings.Contains(renderedBlocks, "Managed browser page: https://chartboost.us.auth0.com/u/login?state=demo") {
		t.Fatalf("renderCodexBrowserPanel() missing managed browser page label after reveal: %q", renderedBlocks)
	}
	footer := ansi.Strip(got.renderCodexFooter(session.snapshot, 160))
	if !strings.Contains(footer, "Ctrl+O focus browser") {
		t.Fatalf("renderCodexFooter() should downgrade Ctrl+O to focus browser after reveal: %q", footer)
	}
}

func TestVisibleCodexURLBasedElicitationBlocksWhenInteractiveLeaseOwnedElsewhere(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Started:  true,
			ThreadID: "thread-demo",
			Preset:   codexcli.PresetYolo,
			Status:   "Browser needs attention",
			BrowserActivity: browserctl.SessionActivity{
				Policy:     settingsAutomaticPlaywrightPolicy,
				State:      browserctl.SessionActivityStateWaitingForUser,
				ServerName: "playwright",
				ToolName:   "browser_navigate",
			},
			ManagedBrowserSessionKey: "managed-demo",
			PendingElicitation: &codexapp.ElicitationRequest{
				ID:   "elicitation_1",
				Mode: codexapp.ElicitationModeURL,
				URL:  "https://example.test/login",
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

	controller := browserctl.NewController()
	ownerObservation := browserctl.Observation{
		Ref: browserctl.SessionRef{
			Provider:    "codex",
			ProjectPath: "/tmp/owner-demo",
			SessionID:   "thread-owner",
		},
		Policy:   settingsAutomaticPlaywrightPolicy,
		Activity: browserctl.SessionActivity{Policy: settingsAutomaticPlaywrightPolicy, State: browserctl.SessionActivityStateWaitingForUser, ServerName: "playwright", ToolName: "browser_navigate"},
		LoginURL: "https://example.test/owner",
	}
	controller.Observe(ownerObservation)
	controller.AcquireInteractive(ownerObservation.Ref)

	m := Model{
		codexManager:         manager,
		codexVisibleProject:  "/tmp/demo",
		codexHiddenProject:   "/tmp/demo",
		codexInput:           newCodexTextarea(),
		codexViewport:        viewport.New(0, 0),
		browserController:    controller,
		browserLeaseSnapshot: controller.Snapshot(),
		width:                100,
		height:               24,
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("o")})
	got := updated.(Model)
	if cmd != nil {
		for _, msg := range collectCmdMsgs(cmd) {
			if _, ok := msg.(browserOpenMsg); ok {
				t.Fatalf("blocked browser open should not queue a browser-open command")
			}
		}
	}
	if !strings.Contains(got.status, "Interactive browser is already reserved by Codex / owner-demo") {
		t.Fatalf("status = %q, want blocked browser ownership status", got.status)
	}
}

func TestVisibleCodexURLBasedElicitationHintsOpenBrowser(t *testing.T) {
	m := Model{
		codexVisibleProject: "/tmp/demo",
		codexSnapshots: map[string]codexapp.Snapshot{
			"/tmp/demo": {
				Provider:                 codexapp.ProviderCodex,
				ManagedBrowserSessionKey: "managed-demo",
				BrowserActivity: browserctl.SessionActivity{
					Policy:     settingsAutomaticPlaywrightPolicy,
					State:      browserctl.SessionActivityStateWaitingForUser,
					ServerName: "playwright",
				},
			},
		},
	}

	request := codexapp.ElicitationRequest{
		ID:      "elicitation_1",
		Mode:    codexapp.ElicitationModeURL,
		Message: "Please log in",
		URL:     "https://example.test/login",
	}

	renderedBlocks := strings.Join(m.renderCodexElicitationBlocks(request, 120), "\n")
	if !strings.Contains(renderedBlocks, "Press O to reveal the managed browser window, then finish the login flow and press Enter when you are done.") {
		t.Fatalf("renderCodexElicitationBlocks() missing managed login hint: %q", renderedBlocks)
	}

	footer := ansi.Strip(m.renderCodexFooter(codexapp.Snapshot{
		Provider:                 codexapp.ProviderCodex,
		Started:                  true,
		Status:                   "Browser needs attention",
		ManagedBrowserSessionKey: "managed-demo",
		BrowserActivity: browserctl.SessionActivity{
			Policy:     settingsAutomaticPlaywrightPolicy,
			State:      browserctl.SessionActivityStateWaitingForUser,
			ServerName: "playwright",
		},
		PendingElicitation: &request,
	}, 160))
	for _, want := range []string{"O show browser", "Enter done/accept", "d decline", "c cancel"} {
		if !strings.Contains(footer, want) {
			t.Fatalf("renderCodexFooter() missing %q for managed browser login: %q", want, footer)
		}
	}
}

func TestVisibleCodexCurrentBackgroundBrowserPageHintsOpenPage(t *testing.T) {
	snapshot := codexapp.Snapshot{
		Provider:                 codexapp.ProviderCodex,
		Started:                  true,
		Status:                   "Codex session ready",
		BrowserActivity:          browserctl.SessionActivity{Policy: settingsAutomaticPlaywrightPolicy},
		ManagedBrowserSessionKey: "managed-demo",
		CurrentBrowserPageURL:    "https://chartboost.us.auth0.com/u/login?state=demo",
	}

	m := Model{codexVisibleProject: "/tmp/demo"}
	renderedBlocks := ansi.Strip(m.renderCodexBrowserPanel(snapshot, 120))
	if !strings.Contains(renderedBlocks, "Background browser page: https://chartboost.us.auth0.com/u/login?state=demo") {
		t.Fatalf("renderCodexBrowserPanel() missing current background page: %q", renderedBlocks)
	}
	if !strings.Contains(renderedBlocks, "Press Ctrl+O to reveal the managed browser window for this same session.") {
		t.Fatalf("renderCodexBrowserPanel() missing Ctrl+O reveal hint: %q", renderedBlocks)
	}

	footer := ansi.Strip(m.renderCodexFooter(snapshot, 160))
	if !strings.Contains(footer, "Ctrl+O show browser") {
		t.Fatalf("renderCodexFooter() missing Ctrl+O show browser action: %q", footer)
	}
}

func TestVisibleCodexCurrentBackgroundBrowserPageUsesVisibleBrowserCopyWhenCachedVisible(t *testing.T) {
	snapshot := codexapp.Snapshot{
		Provider:                 codexapp.ProviderCodex,
		Started:                  true,
		Status:                   "Codex session ready",
		BrowserActivity:          browserctl.SessionActivity{Policy: settingsAutomaticPlaywrightPolicy},
		ManagedBrowserSessionKey: "managed-demo",
		CurrentBrowserPageURL:    "https://chartboost.us.auth0.com/u/login?state=demo",
	}

	m := Model{
		codexVisibleProject: "/tmp/demo",
		managedBrowserStates: map[string]browserctl.ManagedPlaywrightState{
			"managed-demo": {
				SessionKey: "managed-demo",
				BrowserPID: 123,
				Hidden:     false,
			},
		},
	}
	renderedBlocks := ansi.Strip(m.renderCodexBrowserPanel(snapshot, 120))
	if strings.Contains(renderedBlocks, "Press Ctrl+O to reveal the managed browser window for this same session.") {
		t.Fatalf("renderCodexBrowserPanel() should hide stale Ctrl+O reveal hint when browser is already visible: %q", renderedBlocks)
	}
	if !strings.Contains(renderedBlocks, "Managed browser page: https://chartboost.us.auth0.com/u/login?state=demo") {
		t.Fatalf("renderCodexBrowserPanel() missing managed browser page label: %q", renderedBlocks)
	}

	footer := ansi.Strip(m.renderCodexFooter(snapshot, 160))
	if !strings.Contains(footer, "Ctrl+O focus browser") {
		t.Fatalf("renderCodexFooter() should show focus-browser action when browser is already visible: %q", footer)
	}
}

func TestVisibleCodexBrowserPanelShowsReconnectHintForChangedBrowserSettings(t *testing.T) {
	settings := config.EditableSettings{PlaywrightPolicy: settingsAlwaysShowPlaywrightPolicy}
	snapshot := codexapp.Snapshot{
		Provider:                 codexapp.ProviderCodex,
		Started:                  true,
		Status:                   "Codex session ready",
		ManagedBrowserSessionKey: "managed-demo",
		BrowserActivity:          browserctl.SessionActivity{Policy: settingsAutomaticPlaywrightPolicy},
	}

	m := Model{
		codexVisibleProject: "/tmp/demo",
		settingsBaseline:    &settings,
	}

	rendered := ansi.Strip(m.renderCodexBrowserPanel(snapshot, 140))
	for _, want := range []string{
		"Session browser setting: Only when needed.",
		"Current browser setting: Always show.",
		"Use /reconnect to reopen this thread with the current browser behavior, or /codex-new for a fresh session.",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("renderCodexBrowserPanel() missing %q: %q", want, rendered)
		}
	}

	footer := ansi.Strip(m.renderCodexFooter(snapshot, 180))
	for _, want := range []string{"/reconnect apply browser", "/codex-new fresh"} {
		if !strings.Contains(footer, want) {
			t.Fatalf("renderCodexFooter() missing %q for browser policy mismatch: %q", want, footer)
		}
	}
}

func TestVisibleCodexBrowserPanelShowsReconnectHintWhenManagedBrowserNotAttached(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Provider:              codexapp.ProviderCodex,
			Started:               true,
			ThreadID:              "thread-demo",
			Preset:                codexcli.PresetYolo,
			Status:                "Codex session ready",
			BrowserActivity:       browserctl.SessionActivity{Policy: settingsAutomaticPlaywrightPolicy},
			CurrentBrowserPageURL: "https://chartboost.us.auth0.com/u/login?state=demo",
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

	settings := config.EditableSettings{PlaywrightPolicy: settingsAutomaticPlaywrightPolicy}
	m := Model{
		codexManager:        manager,
		codexVisibleProject: "/tmp/demo",
		codexHiddenProject:  "/tmp/demo",
		codexInput:          newCodexTextarea(),
		codexViewport:       viewport.New(0, 0),
		settingsBaseline:    &settings,
		width:               100,
		height:              24,
	}

	rendered := ansi.Strip(m.renderCodexBrowserPanel(session.snapshot, 140))
	for _, want := range []string{
		"Managed browser controls are not attached to this session yet.",
		"Current browser setting: Only when needed.",
		"Use /reconnect to reopen this thread with the current browser behavior, or /codex-new for a fresh session.",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("renderCodexBrowserPanel() missing %q: %q", want, rendered)
		}
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyCtrlO})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("ctrl+o should not queue a browser-open command when managed browser is not attached")
	}
	if !strings.Contains(got.status, "Use /reconnect to reopen this thread with the current browser behavior") {
		t.Fatalf("status = %q, want reconnect guidance", got.status)
	}
}

func TestVisibleOpenCodeCurrentBackgroundBrowserPageHintsOpenPage(t *testing.T) {
	snapshot := codexapp.Snapshot{
		Provider:                 codexapp.ProviderOpenCode,
		Started:                  true,
		Status:                   "OpenCode session ready",
		BrowserActivity:          browserctl.SessionActivity{Policy: settingsAutomaticPlaywrightPolicy},
		ManagedBrowserSessionKey: "managed-demo",
		CurrentBrowserPageURL:    "https://example.com/",
	}

	m := Model{codexVisibleProject: "/tmp/demo"}
	renderedBlocks := ansi.Strip(m.renderCodexBrowserPanel(snapshot, 120))
	if !strings.Contains(renderedBlocks, "Background browser page: https://example.com/") {
		t.Fatalf("renderCodexBrowserPanel() missing current background page for OpenCode: %q", renderedBlocks)
	}
	if !strings.Contains(renderedBlocks, "Press Ctrl+O to reveal the managed browser window for this same session.") {
		t.Fatalf("renderCodexBrowserPanel() missing Ctrl+O reveal hint for OpenCode: %q", renderedBlocks)
	}

	footer := ansi.Strip(m.renderCodexFooter(snapshot, 160))
	if !strings.Contains(footer, "Ctrl+O show browser") {
		t.Fatalf("renderCodexFooter() missing Ctrl+O show browser action for OpenCode: %q", footer)
	}
}

func TestVisibleOpenCodeBrowserPanelShowsReconnectHintWhenManagedBrowserNotAttached(t *testing.T) {
	settings := config.EditableSettings{PlaywrightPolicy: settingsAutomaticPlaywrightPolicy}
	snapshot := codexapp.Snapshot{
		Provider:              codexapp.ProviderOpenCode,
		Started:               true,
		Status:                "OpenCode session ready",
		BrowserActivity:       browserctl.SessionActivity{Policy: settingsAutomaticPlaywrightPolicy},
		CurrentBrowserPageURL: "https://example.com/",
	}

	m := Model{
		codexVisibleProject: "/tmp/demo",
		settingsBaseline:    &settings,
	}

	rendered := ansi.Strip(m.renderCodexBrowserPanel(snapshot, 140))
	for _, want := range []string{
		"Managed browser controls are not attached to this session yet.",
		"Current browser setting: Only when needed.",
		"Use /reconnect to reopen this thread with the current browser behavior, or /opencode-new for a fresh session.",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("renderCodexBrowserPanel() missing %q for OpenCode: %q", want, rendered)
		}
	}

	footer := ansi.Strip(m.renderCodexFooter(snapshot, 180))
	for _, want := range []string{"/reconnect apply browser", "/opencode-new fresh"} {
		if !strings.Contains(footer, want) {
			t.Fatalf("renderCodexFooter() missing %q for OpenCode browser policy mismatch: %q", want, footer)
		}
	}
}

func TestVisibleOpenCodePendingToolInputKeepsShowBrowserAction(t *testing.T) {
	snapshot := codexapp.Snapshot{
		Provider:                 codexapp.ProviderOpenCode,
		Started:                  true,
		Status:                   "Browser needs attention",
		ManagedBrowserSessionKey: "managed-demo",
		BrowserActivity: browserctl.SessionActivity{
			Policy:     settingsAutomaticPlaywrightPolicy,
			State:      browserctl.SessionActivityStateWaitingForUser,
			ServerName: "playwright",
			ToolName:   "browser_navigate",
		},
		CurrentBrowserPageURL: "https://example.com/",
		PendingToolInput: &codexapp.ToolInputRequest{
			ID: "question_1",
			Questions: []codexapp.ToolInputQuestion{{
				ID:       "answer",
				Question: "Finish the sign-in flow and confirm when ready.",
			}},
		},
	}

	m := Model{codexVisibleProject: "/tmp/demo"}
	footer := ansi.Strip(m.renderCodexFooter(snapshot, 180))
	for _, want := range []string{"Enter answer", "Ctrl+O show browser"} {
		if !strings.Contains(footer, want) {
			t.Fatalf("renderCodexFooter() missing %q for OpenCode pending browser question: %q", want, footer)
		}
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

func TestVisibleOpenCodeViewShowsBannerAndYoloWarning(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Provider:         codexapp.ProviderOpenCode,
			Started:          true,
			Preset:           codexcli.PresetYolo,
			Status:           "OpenCode session ready",
			Model:            "openai/gpt-5.4",
			ReasoningEffort:  "high",
			Transcript:       "OpenCode: hello",
			LastSystemNotice: "Started a new embedded OpenCode session ses_demo.",
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
	if !strings.Contains(rendered, "OpenCode | demo") {
		t.Fatalf("embedded OpenCode view should use a compact OpenCode banner: %q", rendered)
	}
	if !strings.Contains(rendered, "YOLO MODE") {
		t.Fatalf("embedded OpenCode view should show YOLO warning: %q", rendered)
	}
}

func TestRenderCodexSessionMetaShowsModelReasoningContextAndPending(t *testing.T) {
	rendered := ansi.Strip((Model{}).renderCodexSessionMeta(codexapp.Snapshot{
		Model:            "gpt-5-codex",
		ReasoningEffort:  "high",
		PendingModel:     "gpt-5",
		PendingReasoning: "medium",
		Entries: []codexapp.TranscriptEntry{
			{Kind: codexapp.TranscriptUser, Text: "Continue from the last breakpoint"},
		},
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

func TestRenderCodexSessionMetaTreatsFreshPendingModelAsCurrent(t *testing.T) {
	rendered := ansi.Strip((Model{}).renderCodexSessionMeta(codexapp.Snapshot{
		Model:            "gpt-5-codex",
		ReasoningEffort:  "medium",
		PendingModel:     "gpt-5.4",
		PendingReasoning: "high",
		Entries: []codexapp.TranscriptEntry{
			{Kind: codexapp.TranscriptSystem, Text: "Started a new embedded Codex session 019demo."},
		},
	}, 140))

	for _, want := range []string{"Model", "gpt-5.4", "Reasoning", "high"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("renderCodexSessionMeta() missing %q: %q", want, rendered)
		}
	}
	for _, unwanted := range []string{"Next", "gpt-5-codex", "medium"} {
		if strings.Contains(rendered, unwanted) {
			t.Fatalf("renderCodexSessionMeta() should not include %q for a fresh session: %q", unwanted, rendered)
		}
	}
}

func TestRenderCodexSessionMetaSkipsNextWhenPendingHasBeenAppliedBeforeOpen(t *testing.T) {
	rendered := ansi.Strip((Model{}).renderCodexSessionMeta(codexapp.Snapshot{
		Model:            "openai/gpt-5",
		ReasoningEffort:  "high",
		PendingModel:     "",
		PendingReasoning: "",
	}, 140))

	for _, want := range []string{"Model", "openai/gpt-5", "Reasoning", "high"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("renderCodexSessionMeta() missing %q: %q", want, rendered)
		}
	}
	for _, unwanted := range []string{"Next", "gpt-5 /"} {
		if strings.Contains(rendered, unwanted) {
			t.Fatalf("renderCodexSessionMeta() should not include %q: %q", unwanted, rendered)
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

func TestVisibleCodexViewShowsCompactingStateInsteadOfBusyElsewhere(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/demo",
		snapshot: codexapp.Snapshot{
			Started: true,
			Preset:  codexcli.PresetYolo,
			Phase:   codexapp.SessionPhaseReconciling,
			Busy:    true,
			Status:  "Compacting conversation history...",
			Entries: []codexapp.TranscriptEntry{
				{Kind: codexapp.TranscriptSystem, Text: "Compacting conversation history..."},
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
	if strings.Contains(rendered, "Read-only") {
		t.Fatalf("compacting view should not show a read-only warning: %q", rendered)
	}
	if strings.Contains(rendered, "Working elsewhere") {
		t.Fatalf("compacting view should not look like an external busy session: %q", rendered)
	}
	if !strings.Contains(rendered, "Compacting conversation") {
		t.Fatalf("compacting view should show an explicit compaction footer: %q", rendered)
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
		codexDenseBlockMode: codexDenseBlockPreview,
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
	}, 36, codexDenseBlockSummary)
	if !strings.Contains(userRendered, "48;5;"+string(codexComposerShellColor)) {
		t.Fatalf("user transcript entry should reuse the composer background color: %q", userRendered)
	}

	agentRendered := renderCodexTranscriptEntry(codexapp.TranscriptEntry{
		Kind: codexapp.TranscriptAgent,
		Text: "Here is a quick summary.",
	}, 36, codexDenseBlockSummary)
	if strings.Contains(agentRendered, "48;5;"+string(codexComposerShellColor)) {
		t.Fatalf("agent transcript entry should not inherit the user echo background: %q", agentRendered)
	}
}

func TestRenderCodexTranscriptEntryCompactsToolCallsToSingleLine(t *testing.T) {
	rendered := ansi.Strip(renderCodexTranscriptEntry(codexapp.TranscriptEntry{
		Kind: codexapp.TranscriptTool,
		Text: "Tool call completed: read README.md\nusing rg --files",
	}, 90, codexDenseBlockSummary))

	// New structured rendering shows tool name bold + summary
	if !strings.Contains(rendered, "call") {
		t.Fatalf("tool transcript entry should show tool name: %q", rendered)
	}
	if !strings.Contains(rendered, "read README.md") {
		t.Fatalf("tool transcript entry should show summary: %q", rendered)
	}
	if strings.Count(rendered, "\n") > 1 {
		t.Fatalf("tool transcript entry should render compactly: %q", rendered)
	}
}

func TestRenderCodexTranscriptEntriesKeepsConsecutiveToolCallsDense(t *testing.T) {
	snapshot := codexapp.Snapshot{
		Entries: []codexapp.TranscriptEntry{
			{Kind: codexapp.TranscriptTool, Text: "Tool call completed: read README.md"},
			{Kind: codexapp.TranscriptTool, Text: "Tool call completed: scan internal/tui"},
		},
	}

	rendered := ansi.Strip((Model{}).renderCodexTranscriptEntries(snapshot, 90))
	if strings.Contains(rendered, "\n\n") {
		t.Fatalf("consecutive tool transcript entries should not be separated by a blank line: %q", rendered)
	}
	if strings.Count(rendered, "\n") != 1 {
		t.Fatalf("consecutive tool transcript entries should render as one line each: %q", rendered)
	}
}

func TestRenderCodexTranscriptEntriesCollapsesLongOpenCodeToolRuns(t *testing.T) {
	snapshot := codexapp.Snapshot{
		Provider: codexapp.ProviderOpenCode,
		Entries: []codexapp.TranscriptEntry{
			{Kind: codexapp.TranscriptTool, Text: "Tool bash completed: read STATUS.md"},
			{Kind: codexapp.TranscriptTool, Text: "Tool bash completed: inspect codex_pane.go"},
			{Kind: codexapp.TranscriptTool, Text: "Tool bash completed: inspect app_test.go"},
			{Kind: codexapp.TranscriptTool, Text: "Tool bash completed: inspect opencode_session.go"},
			{Kind: codexapp.TranscriptTool, Text: "Tool bash completed: inspect opencode_session_test.go"},
			{Kind: codexapp.TranscriptTool, Text: "Tool bash completed: prepare patch"},
		},
	}

	rendered := ansi.Strip((Model{}).renderCodexTranscriptEntries(snapshot, 120))
	// Collapsed run should show the common tool name once, not repeat "Tool bash completed:" per entry
	if !strings.Contains(rendered, "bash") {
		t.Fatalf("collapsed OpenCode tool run should show the common tool name: %q", rendered)
	}
	if !strings.Contains(rendered, "+3 more tool updates") {
		t.Fatalf("collapsed OpenCode tool run should mention omitted updates: %q", rendered)
	}
	if strings.Contains(rendered, "inspect opencode_session.go") {
		t.Fatalf("collapsed OpenCode tool run should omit later repetitive updates: %q", rendered)
	}
	// Should NOT repeat "Tool bash completed:" for each entry
	if strings.Count(rendered, "bash completed") > 0 {
		t.Fatalf("collapsed OpenCode tool run should strip redundant prefixes: %q", rendered)
	}
}

func TestRenderCodexTranscriptEntriesKeepsLongOpenCodeAgentCodeBlocks(t *testing.T) {
	codeLines := make([]string, 0, 95)
	for i := 1; i <= 95; i++ {
		codeLines = append(codeLines, fmt.Sprintf("    line_%d", i))
	}
	snapshot := codexapp.Snapshot{
		Provider: codexapp.ProviderOpenCode,
		Entries: []codexapp.TranscriptEntry{{
			Kind: codexapp.TranscriptAgent,
			Text: "```go\n" + strings.Join(codeLines, "\n") + "\n```",
		}},
	}

	rendered := ansi.Strip((Model{}).renderCodexTranscriptEntries(snapshot, 120))
	if strings.Contains(rendered, "Assistant answer includes a long code block") {
		t.Fatalf("long assistant code block should not collapse: %q", rendered)
	}
	if !strings.Contains(rendered, "line_95") {
		t.Fatalf("long assistant code block should preserve the final line: %q", rendered)
	}
}

func TestRenderCodexTranscriptEntriesKeepsLongOpenCodeAgentCodeBlocksWithoutFence(t *testing.T) {
	codeLines := make([]string, 0, 120)
	for i := 1; i <= 120; i++ {
		codeLines = append(codeLines, "if (i == "+fmt.Sprintf("%d", i)+") { const shake = scene.cameras.main.shake(0, 0); }")
	}
	snapshot := codexapp.Snapshot{
		Provider: codexapp.ProviderOpenCode,
		Entries: []codexapp.TranscriptEntry{{
			Kind: codexapp.TranscriptAgent,
			Text: strings.Join(codeLines, "\n"),
		}},
	}

	rendered := ansi.Strip((Model{}).renderCodexTranscriptEntries(snapshot, 120))
	if strings.Contains(rendered, "Assistant answer includes a long code block") {
		t.Fatalf("long non-fenced OpenCode code block should not collapse: %q", rendered)
	}
	if !strings.Contains(rendered, "i == 120") {
		t.Fatalf("long non-fenced assistant code should preserve the final line: %q", rendered)
	}
}

func TestRenderCodexTranscriptEntriesKeepsLongOpenCodeConsecutiveAgentChunks(t *testing.T) {
	chunk := make([]string, 0, 16)
	for i := 1; i <= 16; i++ {
		chunk = append(chunk, fmt.Sprintf("if (cond_%d) { const shake = scene.cameras.main.shake(0, 0); }", i))
	}
	entries := make([]codexapp.TranscriptEntry, 0, 20)
	for i := 0; i < 10; i++ {
		entries = append(entries, codexapp.TranscriptEntry{Kind: codexapp.TranscriptAgent, Text: strings.Join(chunk, "\n")})
	}
	snapshot := codexapp.Snapshot{
		Provider: codexapp.ProviderOpenCode,
		Entries:  entries,
	}

	rendered := ansi.Strip((Model{}).renderCodexTranscriptEntries(snapshot, 120))
	if strings.Contains(rendered, "Assistant answer includes a long code block") {
		t.Fatalf("consecutive OpenCode agent chunks should not collapse to summary: %q", rendered)
	}
	if count := strings.Count(rendered, "cond_"); count != 160 {
		t.Fatalf("assistant chunks should preserve all code lines, got %d cond_ markers: %q", count, rendered)
	}
}

func TestRenderCodexTranscriptEntriesKeepsLongOpenCodeAgentCodeBlocksWithDenseMode(t *testing.T) {
	codeLines := make([]string, 0, 95)
	for i := 1; i <= 95; i++ {
		codeLines = append(codeLines, fmt.Sprintf("    line_%d", i))
	}
	snapshot := codexapp.Snapshot{
		Provider: codexapp.ProviderOpenCode,
		Entries: []codexapp.TranscriptEntry{{
			Kind: codexapp.TranscriptAgent,
			Text: "```go\n" + strings.Join(codeLines, "\n") + "\n```",
		}},
	}
	m := Model{codexDenseBlockMode: codexDenseBlockFull}
	rendered := ansi.Strip(m.renderCodexTranscriptEntries(snapshot, 120))
	if strings.Contains(rendered, "Assistant answer includes a long code block") {
		t.Fatalf("dense-expanded OpenCode should keep the full assistant output: %q", rendered)
	}
	if !strings.Contains(rendered, "line_95") {
		t.Fatalf("expanded output should include the final line: %q", rendered)
	}
}

func TestRenderCodexTranscriptEntriesKeepsCodexAgentCodeBlocksUncollapsed(t *testing.T) {
	codeLines := make([]string, 0, 95)
	for i := 1; i <= 95; i++ {
		codeLines = append(codeLines, fmt.Sprintf("    line_%d", i))
	}
	snapshot := codexapp.Snapshot{
		Provider: codexapp.ProviderCodex,
		Entries: []codexapp.TranscriptEntry{{
			Kind: codexapp.TranscriptAgent,
			Text: "```go\n" + strings.Join(codeLines, "\n") + "\n```",
		}},
	}

	rendered := ansi.Strip((Model{}).renderCodexTranscriptEntries(snapshot, 120))
	if strings.Contains(rendered, "Assistant answer includes a long code block") {
		t.Fatalf("Codex should not use OpenCode-only code collapse behavior: %q", rendered)
	}
	if !strings.Contains(rendered, "line_95") {
		t.Fatalf("Codex transcript should keep full code line text: %q", rendered)
	}
}

func TestRenderCodexTranscriptEntriesKeepsCodexToolRunsUncollapsed(t *testing.T) {
	snapshot := codexapp.Snapshot{
		Provider: codexapp.ProviderCodex,
		Entries: []codexapp.TranscriptEntry{
			{Kind: codexapp.TranscriptTool, Text: "Tool call completed: read STATUS.md"},
			{Kind: codexapp.TranscriptTool, Text: "Tool call completed: inspect codex_pane.go"},
			{Kind: codexapp.TranscriptTool, Text: "Tool call completed: inspect app_test.go"},
			{Kind: codexapp.TranscriptTool, Text: "Tool call completed: inspect opencode_session.go"},
			{Kind: codexapp.TranscriptTool, Text: "Tool call completed: inspect opencode_session_test.go"},
			{Kind: codexapp.TranscriptTool, Text: "Tool call completed: prepare patch"},
		},
	}

	rendered := ansi.Strip((Model{}).renderCodexTranscriptEntries(snapshot, 120))
	if strings.Contains(rendered, "Tool activity:") {
		t.Fatalf("Codex tool runs should keep the existing dense rendering: %q", rendered)
	}
	if !strings.Contains(rendered, "inspect opencode_session.go") {
		t.Fatalf("Codex tool runs should still show individual updates: %q", rendered)
	}
}

func TestRenderCodexToolLineShowsToolNameAndSummary(t *testing.T) {
	rendered := ansi.Strip(renderCodexToolLine("Tool bash completed: Run focused service tests", 80))
	if !strings.Contains(rendered, "bash") {
		t.Fatalf("tool line should show tool name: %q", rendered)
	}
	if !strings.Contains(rendered, "Run focused service tests") {
		t.Fatalf("tool line should show summary: %q", rendered)
	}
	// "completed" status should be suppressed (noise)
	if strings.Contains(rendered, "completed") {
		t.Fatalf("tool line should suppress 'completed' status: %q", rendered)
	}
}

func TestRenderCodexToolLineShowsNonCompletedStatus(t *testing.T) {
	rendered := ansi.Strip(renderCodexToolLine("Tool bash running: long operation", 80))
	if !strings.Contains(rendered, "running") {
		t.Fatalf("tool line should show non-completed status: %q", rendered)
	}
}

func TestRenderCodexToolLineParsesWebSearch(t *testing.T) {
	rendered := ansi.Strip(renderCodexToolLine("Web search: golang concurrency patterns", 80))
	if !strings.Contains(rendered, "search") {
		t.Fatalf("web search should show tool name: %q", rendered)
	}
	if !strings.Contains(rendered, "golang concurrency patterns") {
		t.Fatalf("web search should show query: %q", rendered)
	}
}

func TestRenderCodexToolLineParsesMCPTool(t *testing.T) {
	rendered := ansi.Strip(renderCodexToolLine("MCP tool myserver/query [completed]", 80))
	if !strings.Contains(rendered, "myserver/query") {
		t.Fatalf("MCP tool should show server/tool name: %q", rendered)
	}
}

func TestRenderCodexBodyRendersNumberedList(t *testing.T) {
	body := "Steps:\n1. First item\n2. Second item\n3. Third item"
	rendered := ansi.Strip(renderCodexBody(body, lipgloss.Color("252"), 80))
	if !strings.Contains(rendered, "1.") || !strings.Contains(rendered, "First item") {
		t.Fatalf("numbered list should render items: %q", rendered)
	}
	if !strings.Contains(rendered, "3.") || !strings.Contains(rendered, "Third item") {
		t.Fatalf("numbered list should render all items: %q", rendered)
	}
}

func TestRenderCodexBodyRendersHorizontalRule(t *testing.T) {
	body := "Above\n---\nBelow"
	rendered := ansi.Strip(renderCodexBody(body, lipgloss.Color("252"), 80))
	if !strings.Contains(rendered, "─") {
		t.Fatalf("horizontal rule should render as box-drawing line: %q", rendered)
	}
	if !strings.Contains(rendered, "Above") || !strings.Contains(rendered, "Below") {
		t.Fatalf("content around horizontal rule should be preserved: %q", rendered)
	}
}

func TestRenderCodexDenseBlockHidesSuccessfulExitInCollapsedMode(t *testing.T) {
	body := "$ git status\n# cwd: /tmp/demo\n M README.md\n[command completed, exit 0]"
	rendered := ansi.Strip(renderCodexDenseBlock("Command", body, lipgloss.Color("111"), 80, codexDenseBlockPreview))
	if strings.Contains(rendered, "exit 0") {
		t.Fatalf("collapsed command block should hide successful exit: %q", rendered)
	}
	if strings.Contains(rendered, "# cwd:") {
		t.Fatalf("collapsed command block should hide cwd comment: %q", rendered)
	}
	if !strings.Contains(rendered, "git status") {
		t.Fatalf("collapsed command block should keep the command: %q", rendered)
	}
	if !strings.Contains(rendered, "README.md") {
		t.Fatalf("collapsed command block should keep command output: %q", rendered)
	}
}

func TestRenderCodexDenseBlockHidesOutputByDefault(t *testing.T) {
	body := "$ git status\n# cwd: /tmp/demo\n M README.md\n?? notes.txt\n[command completed, exit 0]"
	rendered := ansi.Strip(renderCodexDenseBlock("Command", body, lipgloss.Color("111"), 80, codexDenseBlockSummary))
	if !strings.Contains(rendered, "$ git status") {
		t.Fatalf("summary command block should keep the command: %q", rendered)
	}
	for _, hidden := range []string{"README.md", "notes.txt", "exit 0", "# cwd:"} {
		if strings.Contains(rendered, hidden) {
			t.Fatalf("summary command block should hide %q: %q", hidden, rendered)
		}
	}
	if !strings.Contains(rendered, "2 lines hidden") || !strings.Contains(rendered, "Alt+L previews") {
		t.Fatalf("summary command block should mention hidden previewable lines: %q", rendered)
	}
}

func TestRenderCodexDenseBlockPreviewShowsFiveOutputLines(t *testing.T) {
	outputLines := make([]string, 0, 7)
	for i := 1; i <= 7; i++ {
		outputLines = append(outputLines, fmt.Sprintf("output line %d", i))
	}
	body := "$ demo\n" + strings.Join(outputLines, "\n") + "\n[command completed, exit 0]"
	rendered := ansi.Strip(renderCodexDenseBlock("Command", body, lipgloss.Color("111"), 80, codexDenseBlockPreview))
	if !strings.Contains(rendered, "output line 5") {
		t.Fatalf("preview command block should show the fifth output line: %q", rendered)
	}
	if strings.Contains(rendered, "output line 6") {
		t.Fatalf("preview command block should hide output past five lines: %q", rendered)
	}
	if !strings.Contains(rendered, "2 lines hidden") || !strings.Contains(rendered, "Alt+L expands") {
		t.Fatalf("preview command block should mention remaining hidden lines: %q", rendered)
	}
}

func TestRenderCodexDenseBlockKeepsFailedExitInCollapsedMode(t *testing.T) {
	body := "$ make test\nerror: tests failed\n[command completed, exit 1]"
	rendered := ansi.Strip(renderCodexDenseBlock("Command", body, lipgloss.Color("111"), 80, codexDenseBlockSummary))
	if !strings.Contains(rendered, "exit 1") {
		t.Fatalf("collapsed command block should keep non-zero exit: %q", rendered)
	}
}

func TestRenderCodexDenseBlockShowsAllInExpandedMode(t *testing.T) {
	body := "$ git status\n# cwd: /tmp/demo\n M README.md\n[command completed, exit 0]"
	rendered := ansi.Strip(renderCodexDenseBlock("Command", body, lipgloss.Color("111"), 80, codexDenseBlockFull))
	if !strings.Contains(rendered, "exit 0") {
		t.Fatalf("expanded command block should show exit status: %q", rendered)
	}
	if !strings.Contains(rendered, "# cwd:") {
		t.Fatalf("expanded command block should show cwd comment: %q", rendered)
	}
}

func TestCodexTranscriptEntrySeparatorTightensToolCommandTransitions(t *testing.T) {
	// tool→command should be tight
	sep := codexTranscriptEntrySeparator(codexapp.TranscriptTool, codexapp.TranscriptCommand)
	if sep != "\n" {
		t.Fatalf("tool→command should use tight separator, got %q", sep)
	}
	// command→tool should be tight
	sep = codexTranscriptEntrySeparator(codexapp.TranscriptCommand, codexapp.TranscriptTool)
	if sep != "\n" {
		t.Fatalf("command→tool should use tight separator, got %q", sep)
	}
	// agent→tool should still be double
	sep = codexTranscriptEntrySeparator(codexapp.TranscriptAgent, codexapp.TranscriptTool)
	if sep != "\n\n" {
		t.Fatalf("agent→tool should use standard separator, got %q", sep)
	}
}

func TestReasoningIndicatorShownWhenHidden(t *testing.T) {
	snapshot := codexapp.Snapshot{
		Provider: codexapp.ProviderOpenCode,
		Entries: []codexapp.TranscriptEntry{
			{Kind: codexapp.TranscriptAgent, Text: "Let me think about this..."},
			{Kind: codexapp.TranscriptReasoning, Text: "Step 1: analyze the problem\nStep 2: consider options\nStep 3: pick the best one"},
			{Kind: codexapp.TranscriptAgent, Text: "Here is my answer."},
		},
	}

	rendered := ansi.Strip((Model{hideReasoningSections: true}).renderCodexTranscriptEntries(snapshot, 90))
	if !strings.Contains(rendered, "Thinking") {
		t.Fatalf("hidden reasoning should show compact indicator: %q", rendered)
	}
	if !strings.Contains(rendered, "3 lines") {
		t.Fatalf("reasoning indicator should show line count: %q", rendered)
	}
	// Should NOT show the actual reasoning text
	if strings.Contains(rendered, "Step 1") {
		t.Fatalf("hidden reasoning should not show reasoning content: %q", rendered)
	}
	// Agent messages should still be visible
	if !strings.Contains(rendered, "Here is my answer") {
		t.Fatalf("agent messages should still be visible around reasoning indicator: %q", rendered)
	}
}

func TestReasoningIndicatorMergesConsecutiveEntries(t *testing.T) {
	snapshot := codexapp.Snapshot{
		Provider: codexapp.ProviderOpenCode,
		Entries: []codexapp.TranscriptEntry{
			{Kind: codexapp.TranscriptReasoning, Text: "First thought\nSecond thought"},
			{Kind: codexapp.TranscriptReasoning, Text: "Third thought\nFourth thought\nFifth thought"},
		},
	}

	rendered := ansi.Strip((Model{hideReasoningSections: true}).renderCodexTranscriptEntries(snapshot, 90))
	if !strings.Contains(rendered, "5 lines") {
		t.Fatalf("consecutive reasoning entries should merge into one indicator with total lines: %q", rendered)
	}
	// Should only have one "Thinking" indicator, not two
	if strings.Count(rendered, "Thinking") != 1 {
		t.Fatalf("should have exactly one thinking indicator for consecutive reasoning: %q", rendered)
	}
}

func TestReasoningShownFullyWhenNotHidden(t *testing.T) {
	snapshot := codexapp.Snapshot{
		Provider: codexapp.ProviderOpenCode,
		Entries: []codexapp.TranscriptEntry{
			{Kind: codexapp.TranscriptReasoning, Text: "Detailed reasoning step here"},
		},
	}

	rendered := ansi.Strip((Model{hideReasoningSections: false}).renderCodexTranscriptEntries(snapshot, 90))
	if strings.Contains(rendered, "Thinking") {
		t.Fatalf("non-hidden reasoning should not show compact indicator: %q", rendered)
	}
	if !strings.Contains(rendered, "Detailed reasoning step here") {
		t.Fatalf("non-hidden reasoning should show full content: %q", rendered)
	}
}

func TestReasoningExpandedWithAltL(t *testing.T) {
	snapshot := codexapp.Snapshot{
		Provider: codexapp.ProviderOpenCode,
		Entries: []codexapp.TranscriptEntry{
			{Kind: codexapp.TranscriptReasoning, Text: "Detailed reasoning step here"},
		},
	}

	// hideReasoningSections=true but full block mode (Alt+L twice) should show full reasoning
	rendered := ansi.Strip((Model{hideReasoningSections: true, codexDenseBlockMode: codexDenseBlockFull}).renderCodexTranscriptEntries(snapshot, 90))
	if strings.Contains(rendered, "Thinking") {
		t.Fatalf("Alt+L should bypass reasoning hiding and show full content: %q", rendered)
	}
	if !strings.Contains(rendered, "Detailed reasoning step here") {
		t.Fatalf("Alt+L should reveal full reasoning content: %q", rendered)
	}
}

func TestReasoningIndicatorSingularLine(t *testing.T) {
	snapshot := codexapp.Snapshot{
		Provider: codexapp.ProviderOpenCode,
		Entries: []codexapp.TranscriptEntry{
			{Kind: codexapp.TranscriptReasoning, Text: "One line of thought"},
		},
	}

	rendered := ansi.Strip((Model{hideReasoningSections: true}).renderCodexTranscriptEntries(snapshot, 90))
	if !strings.Contains(rendered, "1 line,") {
		t.Fatalf("single-line reasoning should use singular 'line': %q", rendered)
	}
	if strings.Contains(rendered, "1 lines") {
		t.Fatalf("should not say '1 lines': %q", rendered)
	}
}

func TestDefaultConfigHidesReasoningSections(t *testing.T) {
	cfg := config.Default()
	if !cfg.HideReasoningSections {
		t.Fatal("HideReasoningSections should default to true")
	}
}

func TestRenderCodexBodyRendersBoldAndItalic(t *testing.T) {
	rendered := ansi.Strip(renderCodexBody("This is **bold** and *italic* text.", lipgloss.Color("252"), 80))
	if !strings.Contains(rendered, "bold") {
		t.Fatalf("bold text should be preserved: %q", rendered)
	}
	if !strings.Contains(rendered, "italic") {
		t.Fatalf("italic text should be preserved: %q", rendered)
	}
	// The ** and * delimiters should be stripped
	if strings.Contains(rendered, "**") {
		t.Fatalf("bold markers should be stripped: %q", rendered)
	}
}

func TestRenderCodexBodyRendersInlineCodeWithoutBackticks(t *testing.T) {
	body := "This workflow is scheduled twice a day (`0 0` and `0 1`) and maps to `02:00 Europe/Rome`."
	rendered := ansi.Strip(renderCodexBody(body, lipgloss.Color("252"), 52))
	normalized := strings.Join(strings.Fields(rendered), " ")

	for _, want := range []string{"0 0", "0 1", "02:00 Europe/Rome"} {
		if !strings.Contains(normalized, want) {
			t.Fatalf("inline code content should be preserved, missing %q in %q", want, rendered)
		}
	}
	if strings.Contains(rendered, "`") {
		t.Fatalf("inline code markers should be stripped: %q", rendered)
	}
	for _, line := range strings.Split(rendered, "\n") {
		if ansi.StringWidth(line) > 52 {
			t.Fatalf("wrapped line width = %d, want <= 52: %q", ansi.StringWidth(line), line)
		}
	}
}

func TestRenderCodexBodyRendersMarkdownTable(t *testing.T) {
	table := "| Name | Value | Status |\n| --- | --- | --- |\n| foo | 42 | ok |\n| bar | 99 | err |"
	rendered := ansi.Strip(renderCodexBody(table, lipgloss.Color("252"), 80))
	if !strings.Contains(rendered, "Name") || !strings.Contains(rendered, "foo") {
		t.Fatalf("table should render cell contents: %q", rendered)
	}
	if !strings.Contains(rendered, "│") {
		t.Fatalf("table should use box-drawing separators: %q", rendered)
	}
	if !strings.Contains(rendered, "─") {
		t.Fatalf("table should render horizontal separator: %q", rendered)
	}
}

func TestRenderCodexTranscriptEntriesKeepsMassiveAgentOutput(t *testing.T) {
	lines := make([]string, 1200)
	for i := range lines {
		lines[i] = fmt.Sprintf("This is output line %d with some content.", i)
	}
	snapshot := codexapp.Snapshot{
		Provider: codexapp.ProviderOpenCode,
		Entries: []codexapp.TranscriptEntry{{
			Kind: codexapp.TranscriptAgent,
			Text: strings.Join(lines, "\n"),
		}},
	}

	rendered := ansi.Strip((Model{}).renderCodexTranscriptEntries(snapshot, 120))
	if strings.Contains(rendered, "Long output truncated") {
		t.Fatalf("assistant output should not be bottom-clipped: %q", rendered)
	}
	if !strings.Contains(rendered, "This is output line 1199 with some content.") {
		t.Fatalf("assistant output should preserve the final line: %q", rendered)
	}
}

func TestRenderCodexTranscriptEntriesKeepsLongReadableMarkdownAgentOutput(t *testing.T) {
	lines := []string{
		"That is probably much more model-native than a custom graph API with bespoke object types. LLMs already understand:",
		"",
		"```bash",
		"ls",
		"cat",
		"grep",
		"find",
		"mkdir",
		"cp",
		"mv",
		"touch",
		"sed",
		"jq",
		"git",
		"```",
		"",
	}
	for i := len(lines); i < 217; i++ {
		lines = append(lines, fmt.Sprintf("Readable explanation line %d.", i))
	}
	snapshot := codexapp.Snapshot{
		Provider: codexapp.ProviderOpenCode,
		Entries: []codexapp.TranscriptEntry{{
			Kind: codexapp.TranscriptAgent,
			Text: strings.Join(lines, "\n"),
		}},
	}

	rendered := ansi.Strip((Model{}).renderCodexTranscriptEntries(snapshot, 120))
	if strings.Contains(rendered, "Long output truncated") {
		t.Fatalf("readable assistant Markdown should not be treated as dense output: %q", rendered)
	}
	if !strings.Contains(rendered, "Readable explanation line 216.") {
		t.Fatalf("readable assistant Markdown should preserve the final line: %q", rendered)
	}
}

func TestRenderCodexTranscriptEntriesCollapsesMassiveReasoningOutput(t *testing.T) {
	lines := make([]string, 150)
	for i := range lines {
		lines[i] = fmt.Sprintf("Thinking step %d...", i)
	}
	snapshot := codexapp.Snapshot{
		Provider: codexapp.ProviderOpenCode,
		Entries: []codexapp.TranscriptEntry{{
			Kind: codexapp.TranscriptReasoning,
			Text: strings.Join(lines, "\n"),
		}},
	}

	rendered := ansi.Strip((Model{}).renderCodexTranscriptEntries(snapshot, 120))
	if !strings.Contains(rendered, "Long reasoning truncated") {
		t.Fatalf("massive reasoning output should be truncated: %q", rendered)
	}
}

func TestRenderCodexTranscriptEntriesCollapsesRepetitiveContent(t *testing.T) {
	// Simulate GLM-5 style repetitive output
	lines := make([]string, 0, 100)
	lines = append(lines, "Here is the solution:")
	block := []string{
		"Step 1: Initialize the variable",
		"Step 2: Loop through items",
		"Step 3: Process each item",
		"Step 4: Return the result",
		"Step 5: Clean up resources",
		"Step 6: Log completion",
	}
	for i := 0; i < 10; i++ {
		lines = append(lines, block...)
	}
	snapshot := codexapp.Snapshot{
		Provider: codexapp.ProviderOpenCode,
		Entries: []codexapp.TranscriptEntry{{
			Kind: codexapp.TranscriptAgent,
			Text: strings.Join(lines, "\n"),
		}},
	}

	rendered := ansi.Strip((Model{}).renderCodexTranscriptEntries(snapshot, 120))
	if !strings.Contains(rendered, "Repetitive") {
		t.Fatalf("repetitive content should be detected and collapsed: %q", rendered)
	}
	if !strings.Contains(rendered, "similar blocks omitted") {
		t.Fatalf("repetitive collapse should mention omitted blocks: %q", rendered)
	}
}

func TestRenderCodexTranscriptEntriesExpandsMassiveOutputWithDenseMode(t *testing.T) {
	lines := make([]string, 250)
	for i := range lines {
		lines[i] = fmt.Sprintf("This is output line %d with some content.", i)
	}
	snapshot := codexapp.Snapshot{
		Provider: codexapp.ProviderOpenCode,
		Entries: []codexapp.TranscriptEntry{{
			Kind: codexapp.TranscriptAgent,
			Text: strings.Join(lines, "\n"),
		}},
	}

	rendered := ansi.Strip((Model{codexDenseBlockMode: codexDenseBlockFull}).renderCodexTranscriptEntries(snapshot, 120))
	if strings.Contains(rendered, "Long output truncated") {
		t.Fatalf("dense-expanded mode should show full output: %q", rendered[:200])
	}
	if !strings.Contains(rendered, "output line 249") {
		t.Fatalf("dense-expanded mode should include all lines: %q", rendered[len(rendered)-200:])
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

func TestRenderCodexTranscriptEntriesRendersLocalMarkdownLinksAsArtifacts(t *testing.T) {
	snapshot := codexapp.Snapshot{
		Entries: []codexapp.TranscriptEntry{
			{
				Kind: codexapp.TranscriptAgent,
				Text: "See [README](/tmp/demo/README.md).",
			},
		},
	}

	rendered := (Model{}).renderCodexTranscriptEntries(snapshot, 80)
	if strings.Contains(rendered, ansi.SetHyperlink("/tmp/demo/README.md")) {
		t.Fatalf("rendered transcript should not rely on terminal hyperlinks for local markdown artifacts: %q", rendered)
	}
	if strings.Contains(rendered, "file://") {
		t.Fatalf("rendered transcript should not use file URLs for local paths: %q", rendered)
	}
	stripped := ansi.Strip(rendered)
	if strings.Contains(stripped, "[README](/tmp/demo/README.md)") {
		t.Fatalf("rendered transcript should hide markdown link syntax once rendered: %q", stripped)
	}
	if !strings.Contains(stripped, "See README (README.md) Alt+O.") {
		t.Fatalf("rendered transcript should preserve the local markdown artifact in the visible text: %q", stripped)
	}
}

func TestCodexLinkExtractionIgnoresHiddenDenseCommandOutput(t *testing.T) {
	hiddenOutput := strings.Repeat("[not a markdown link\n", 2000) + "[hidden](https://hidden.example/docs)"
	entry := codexapp.TranscriptEntry{
		Kind: codexapp.TranscriptCommand,
		Text: "$ generate-large-output\n" + hiddenOutput,
	}

	if scanText, ok := codexTranscriptEntryLinkScanText(entry, codexDenseBlockSummary); ok || scanText != "" {
		t.Fatalf("command entries should not participate in link scanning: text=%q ok=%t", scanText, ok)
	}
	if targets := codexOpenTargetsFromTranscriptEntryForBlockMode(entry, codexDenseBlockSummary); len(targets) != 0 {
		t.Fatalf("hidden command links should not be discoverable in summary mode: %#v", targets)
	}
}

func TestCodexProgressiveLinkScanFindsHiddenDenseCommandOutput(t *testing.T) {
	projectPath := "/tmp/demo"
	hiddenOutput := strings.Repeat("[not a markdown link\n", 2000) + "[hidden](https://hidden.example/docs)"
	snapshot := codexapp.Snapshot{
		ProjectPath: projectPath,
		Entries: []codexapp.TranscriptEntry{
			{
				Kind: codexapp.TranscriptCommand,
				Text: "$ generate-large-output\n" + hiddenOutput,
			},
		},
	}
	m := Model{
		codexVisibleProject: projectPath,
		codexViewport:       viewport.New(80, 4),
	}
	m.storeCodexSnapshot(projectPath, snapshot)
	cmd := m.maybeStartCodexArtifactLinkScan(projectPath, snapshot)
	if cmd == nil {
		t.Fatalf("progressive link scan should start for transcript entries")
	}
	got := drainCmdMsgs(m, cmd)
	targets := got.cachedProgressiveCodexOpenTargets(snapshot)
	if len(targets) != 1 {
		t.Fatalf("progressive targets = %#v, want one hidden URL", targets)
	}
	if targets[0].Kind != "url" || targets[0].Path != "https://hidden.example/docs" {
		t.Fatalf("hidden target = %#v, want hidden URL", targets[0])
	}

	updated, cmd := got.openCodexArtifactPicker(snapshot)
	if cmd != nil {
		t.Fatalf("cached hidden target should open picker without a command, got %T", cmd)
	}
	got = normalizeUpdateModel(updated)
	if got.codexArtifactPicker == nil || len(got.codexArtifactPicker.Targets) != 1 {
		t.Fatalf("hidden link picker state = %#v, want one progressive target", got.codexArtifactPicker)
	}
}

func TestCodexMarkdownLinkParserBoundsMalformedBrackets(t *testing.T) {
	longLabel := "[" + strings.Repeat("x", codexMarkdownLinkLabelScanLimit+1) + "](https://example.com/docs)"
	if _, _, _, ok := parseCodexMarkdownLink(longLabel); ok {
		t.Fatalf("over-limit markdown labels should not parse")
	}

	label, target, consumed, ok := parseCodexMarkdownLink("[docs](https://example.com/docs)")
	if !ok {
		t.Fatalf("valid markdown link did not parse")
	}
	if label != "docs" || target != "https://example.com/docs" || consumed != len("[docs](https://example.com/docs)") {
		t.Fatalf("parsed link = (%q, %q, %d), want docs/example/full length", label, target, consumed)
	}
}

func TestRenderCodexTranscriptEntriesUnwrapsAngleBracketLocalMarkdownLinks(t *testing.T) {
	snapshot := codexapp.Snapshot{
		Entries: []codexapp.TranscriptEntry{
			{
				Kind: codexapp.TranscriptAgent,
				Text: "Open [notes](</tmp/lcroom mockups/notes.md>).",
			},
		},
	}

	rendered := (Model{}).renderCodexTranscriptEntries(snapshot, 80)
	if strings.Contains(rendered, ansi.SetHyperlink("/tmp/lcroom mockups/notes.md")) {
		t.Fatalf("rendered transcript should not rely on terminal hyperlinks for local markdown artifacts: %q", rendered)
	}
	if strings.Contains(rendered, "file://") {
		t.Fatalf("rendered transcript should not use file URLs for local paths: %q", rendered)
	}
	if strings.Contains(rendered, "%3C") || strings.Contains(rendered, "%3E") || strings.Contains(rendered, "%20") {
		t.Fatalf("rendered transcript should not include encoded markdown target angle brackets: %q", rendered)
	}
	stripped := ansi.Strip(rendered)
	if strings.Contains(stripped, "[notes](</tmp/lcroom mockups/notes.md>)") {
		t.Fatalf("rendered transcript should hide markdown link syntax once rendered: %q", stripped)
	}
	if !strings.Contains(stripped, "Open notes (notes.md) Alt+O.") {
		t.Fatalf("rendered transcript should preserve the compact local artifact label in the visible text: %q", stripped)
	}
}

func TestRenderCodexTranscriptEntriesKeepsLocalLineSuffixInRawPathHyperlink(t *testing.T) {
	snapshot := codexapp.Snapshot{
		Entries: []codexapp.TranscriptEntry{
			{
				Kind: codexapp.TranscriptAgent,
				Text: "Changed [manager.go](/tmp/demo/manager.go:107).",
			},
		},
	}

	rendered := (Model{}).renderCodexTranscriptEntries(snapshot, 80)
	if strings.Contains(rendered, ansi.SetHyperlink("/tmp/demo/manager.go:107")) {
		t.Fatalf("rendered transcript should not use line-suffixed local paths as terminal hyperlink targets: %q", rendered)
	}
	if !strings.Contains(rendered, ansi.SetHyperlink("/tmp/demo/manager.go")) {
		t.Fatalf("rendered transcript should use the openable local file as the terminal hyperlink target: %q", rendered)
	}
	if strings.Contains(rendered, "file://") {
		t.Fatalf("rendered transcript should not use file URLs for local paths: %q", rendered)
	}
	stripped := ansi.Strip(rendered)
	if !strings.Contains(stripped, "Changed manager.go.") {
		t.Fatalf("rendered transcript should preserve the compact local link label in the visible text: %q", stripped)
	}
}

func TestCodexLinkPickerOpensLineSuffixedLocalMarkdownLinksAsFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sourcefile.go")
	if err := os.WriteFile(path, []byte("package demo\n"), 0o600); err != nil {
		t.Fatalf("write source file: %v", err)
	}
	snapshot := codexapp.Snapshot{
		ProjectPath: "/tmp/demo",
		Entries: []codexapp.TranscriptEntry{
			{
				Kind: codexapp.TranscriptAgent,
				Text: "Changed [sourcefile.go](" + path + ":232).",
			},
		},
	}

	opened := ""
	oldOpener := externalPathOpener
	externalPathOpener = func(path string) error {
		opened = path
		return nil
	}
	t.Cleanup(func() { externalPathOpener = oldOpener })

	m := Model{
		codexVisibleProject: "/tmp/demo",
		codexSnapshots: map[string]codexapp.Snapshot{
			"/tmp/demo": snapshot,
		},
		codexViewport: viewport.New(80, 4),
	}
	rendered := m.renderAndCacheCodexTranscript("/tmp/demo", snapshot, 80)
	m.codexViewport.SetContent(rendered)

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}, Alt: true})
	if cmd != nil {
		t.Fatalf("Alt+O should open the link picker without a command, got %T", cmd)
	}
	got := normalizeUpdateModel(updated)
	if got.codexArtifactPicker == nil || len(got.codexArtifactPicker.Targets) != 1 {
		t.Fatalf("link picker state = %#v, want one source target", got.codexArtifactPicker)
	}
	target := got.codexArtifactPicker.Targets[0]
	if target.Kind != "source" || target.Path != path {
		t.Fatalf("source target = %#v, want kind source path %q", target, path)
	}

	updated, cmd = got.updateCodexArtifactPickerMode(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatalf("Enter on source link should queue open command")
	}
	if msg := cmd(); msg == nil {
		t.Fatalf("source open command returned nil")
	}
	if opened != path {
		t.Fatalf("source picker opened %q, want clean file path %q", opened, path)
	}
	_ = updated
}

func TestRenderCodexTranscriptEntriesRendersFileURLMarkdownLinksAsRawPathHyperlinks(t *testing.T) {
	snapshot := codexapp.Snapshot{
		Entries: []codexapp.TranscriptEntry{
			{
				Kind: codexapp.TranscriptAgent,
				Text: "Open [notes](file://localhost/tmp/demo/notes.txt).",
			},
		},
	}

	rendered := (Model{}).renderCodexTranscriptEntries(snapshot, 80)
	if !strings.Contains(rendered, ansi.SetHyperlink("/tmp/demo/notes.txt")) {
		t.Fatalf("rendered transcript should convert local file URLs into raw path hyperlink targets: %q", rendered)
	}
	if strings.Contains(rendered, "file://") {
		t.Fatalf("rendered transcript should not preserve file URLs for local paths: %q", rendered)
	}
	stripped := ansi.Strip(rendered)
	if !strings.Contains(stripped, "Open notes.") {
		t.Fatalf("rendered transcript should preserve the compact local link label in the visible text: %q", stripped)
	}
}

func TestRenderCodexTranscriptEntriesDoesNotUseTerminalHyperlinksForLocalImageMarkdownLinks(t *testing.T) {
	path := "/tmp/demo/image.png"
	snapshot := codexapp.Snapshot{
		Entries: []codexapp.TranscriptEntry{
			{
				Kind: codexapp.TranscriptAgent,
				Text: "Open [image](file://localhost/tmp/demo/image.png).",
			},
		},
	}

	rendered := (Model{}).renderCodexTranscriptEntries(snapshot, 80)
	if strings.Contains(rendered, ansi.SetHyperlink(path)) {
		t.Fatalf("local image markdown links should not rely on terminal hyperlinks: %q", rendered)
	}
	stripped := ansi.Strip(rendered)
	if strings.Contains(stripped, path) {
		t.Fatalf("rendered transcript should not expose long raw image paths that terminals split: %q", stripped)
	}
	if !strings.Contains(stripped, "Open image (image.png) Alt+O.") {
		t.Fatalf("rendered transcript should show the image label and filename: %q", stripped)
	}
}

func TestRenderCodexTranscriptEntriesAdvertisesOpenShortcutBesideEachLocalArtifactLink(t *testing.T) {
	snapshot := codexapp.Snapshot{
		Entries: []codexapp.TranscriptEntry{
			{
				Kind: codexapp.TranscriptAgent,
				Text: "New preview: [boss-cabin-game.png](/tmp/demo/boss-cabin-game.png)\nAll previews: [index.html](/tmp/demo/index.html)",
			},
		},
	}

	rendered := (Model{}).renderCodexTranscriptEntries(snapshot, 80)
	stripped := ansi.Strip(rendered)
	for _, want := range []string{
		"New preview: boss-cabin-game.png Alt+O",
		"All previews: index.html Alt+O",
	} {
		if !strings.Contains(stripped, want) {
			t.Fatalf("rendered transcript missing %q: %q", want, stripped)
		}
	}
	if count := strings.Count(stripped, "Alt+O"); count != 2 {
		t.Fatalf("local artifact shortcut hint count = %d, want 2 in transcript: %q", count, stripped)
	}
}

func TestCodexArtifactPickerOpensFolderNamedReadmeLinksAsDirectory(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "LittleControlRoom-art-lab")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	readme := filepath.Join(workspace, "README.md")
	if err := os.WriteFile(readme, []byte("# Art lab\n"), 0o600); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	snapshot := codexapp.Snapshot{
		ProjectPath: "/tmp/demo",
		Entries: []codexapp.TranscriptEntry{
			{
				Kind: codexapp.TranscriptAgent,
				Text: "Created a sibling art workspace here:\n[LittleControlRoom-art-lab](" + readme + ")",
			},
		},
	}

	rendered := (Model{}).renderCodexTranscriptEntries(snapshot, 80)
	if strings.Contains(rendered, ansi.SetHyperlink(readme)) {
		t.Fatalf("folder README links should not rely on terminal hyperlinks: %q", rendered)
	}
	stripped := ansi.Strip(rendered)
	if strings.Contains(stripped, "README.md") {
		t.Fatalf("folder README link should render as the workspace directory, not the README target: %q", stripped)
	}
	if !strings.Contains(stripped, "LittleControlRoom-art-lab Alt+O") {
		t.Fatalf("folder README link should advertise the artifact picker: %q", stripped)
	}

	opened := ""
	oldOpener := externalPathOpener
	externalPathOpener = func(path string) error {
		opened = path
		return nil
	}
	t.Cleanup(func() { externalPathOpener = oldOpener })

	m := Model{
		codexVisibleProject: "/tmp/demo",
		codexSnapshots: map[string]codexapp.Snapshot{
			"/tmp/demo": snapshot,
		},
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}, Alt: true})
	if cmd != nil {
		t.Fatalf("directory artifact should not queue a preview command, got %T", cmd)
	}
	got := normalizeUpdateModel(updated)
	if got.codexArtifactPicker == nil || len(got.codexArtifactPicker.Targets) != 1 {
		t.Fatalf("directory artifact picker state = %#v, want one target", got.codexArtifactPicker)
	}
	target := got.codexArtifactPicker.Targets[0]
	if target.Kind != "dir" || target.Path != workspace {
		t.Fatalf("directory artifact target = %#v, want kind dir path %q", target, workspace)
	}
	overlay := ansi.Strip(got.renderCodexArtifactPicker(80, 24))
	for _, want := range []string{"Open Links", "DIR", "LittleControlRoom-art-lab"} {
		if !strings.Contains(overlay, want) {
			t.Fatalf("directory artifact picker missing %q: %q", want, overlay)
		}
	}

	updated, cmd = got.updateCodexArtifactPickerMode(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatalf("Enter on directory artifact should queue open command")
	}
	if msg := cmd(); msg == nil {
		t.Fatalf("directory open command returned nil")
	}
	if opened != workspace {
		t.Fatalf("directory picker opened %q, want %q", opened, workspace)
	}
	_ = updated
}

func TestRenderCodexTranscriptEntriesRendersGeneratedImagePreview(t *testing.T) {
	imageBytes := mustTestPNG(color.RGBA{R: 40, G: 180, B: 220, A: 255})
	path := "/tmp/demo/generated_images/ig_demo.png"
	snapshot := codexapp.Snapshot{
		Entries: []codexapp.TranscriptEntry{
			{
				Kind: codexapp.TranscriptTool,
				Text: "Generated image\n" + path,
				GeneratedImage: &codexapp.GeneratedImageArtifact{
					ID:          "ig_demo",
					Path:        path,
					Width:       4,
					Height:      4,
					ByteSize:    int64(len(imageBytes)),
					PreviewData: imageBytes,
				},
			},
		},
	}

	rendered := (Model{}).renderCodexTranscriptEntries(snapshot, 80)
	if strings.Contains(rendered, ansi.SetHyperlink(path)) {
		t.Fatalf("generated image block should not rely on terminal hyperlinks for local image opening: %q", rendered)
	}
	if !strings.Contains(rendered, "\x1b[38;2;") {
		t.Fatalf("generated image block should include an ANSI image preview: %q", rendered)
	}
	stripped := ansi.Strip(rendered)
	if strings.Contains(stripped, path) {
		t.Fatalf("generated image block should not expose long raw paths that terminals split: %q", stripped)
	}
	for _, want := range []string{"Generated image", "4x4", "File: ig_demo.png", "Alt+O artifact picker"} {
		if !strings.Contains(stripped, want) {
			t.Fatalf("generated image block missing %q: %q", want, stripped)
		}
	}
}

func TestRenderCodexTranscriptEntriesAdvertisesOpenShortcutOnlyOnLatestGeneratedImage(t *testing.T) {
	firstPath := "/tmp/demo/generated_images/first.png"
	secondPath := "/tmp/demo/generated_images/second.png"
	snapshot := codexapp.Snapshot{
		Entries: []codexapp.TranscriptEntry{
			{
				Kind: codexapp.TranscriptTool,
				GeneratedImage: &codexapp.GeneratedImageArtifact{
					Path:  firstPath,
					Width: 4,
				},
			},
			{
				Kind: codexapp.TranscriptTool,
				GeneratedImage: &codexapp.GeneratedImageArtifact{
					Path:  secondPath,
					Width: 4,
				},
			},
		},
	}

	stripped := ansi.Strip((Model{}).renderCodexTranscriptEntries(snapshot, 80))
	if strings.Count(stripped, "Alt+O artifact picker") != 1 {
		t.Fatalf("latest artifact shortcut hint count = %d, transcript: %q", strings.Count(stripped, "Alt+O artifact picker"), stripped)
	}
	firstHint := strings.Index(stripped, "File: first.png")
	secondHint := strings.Index(stripped, "File: second.png")
	openHint := strings.Index(stripped, "Alt+O artifact picker")
	if firstHint < 0 || secondHint < 0 || openHint < 0 || openHint < secondHint || openHint < firstHint {
		t.Fatalf("shortcut hint should be attached to the latest generated image: %q", stripped)
	}
}

func TestGeneratedImageOpenActionsUseSystemOpen(t *testing.T) {
	imageBytes := mustTestPNG(color.RGBA{R: 40, G: 180, B: 220, A: 255})
	path := filepath.Join(t.TempDir(), "ig_demo.png")
	if err := os.WriteFile(path, imageBytes, 0o600); err != nil {
		t.Fatalf("write image: %v", err)
	}
	snapshot := codexapp.Snapshot{
		ProjectPath: "/tmp/demo",
		Entries: []codexapp.TranscriptEntry{
			{
				Kind: codexapp.TranscriptTool,
				Text: "Generated image\n" + path,
				GeneratedImage: &codexapp.GeneratedImageArtifact{
					ID:          "ig_demo",
					Path:        path,
					Width:       4,
					Height:      4,
					ByteSize:    int64(len(imageBytes)),
					PreviewData: imageBytes,
				},
			},
		},
	}

	opened := ""
	oldOpener := externalPathOpener
	externalPathOpener = func(path string) error {
		opened = path
		return nil
	}
	t.Cleanup(func() { externalPathOpener = oldOpener })

	m := Model{
		codexVisibleProject: "/tmp/demo",
		codexSnapshots: map[string]codexapp.Snapshot{
			"/tmp/demo": snapshot,
		},
		codexViewport: viewport.New(80, 20),
	}
	rendered := m.renderAndCacheCodexTranscript("/tmp/demo", snapshot, 80)
	m.codexViewport.SetContent(rendered)

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}, Alt: true})
	if cmd != nil {
		t.Fatalf("Alt+O should open the artifact picker without a command, got %T", cmd)
	}
	got := normalizeUpdateModel(updated)
	if got.codexArtifactPicker == nil {
		t.Fatalf("Alt+O should show the artifact picker")
	}
	if got.status != "Link picker open" {
		t.Fatalf("status = %q, want picker status", got.status)
	}
	updated, cmd = got.updateCodexArtifactPickerMode(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatalf("Enter from artifact picker should queue open command")
	}
	if msg := cmd(); msg == nil {
		t.Fatalf("artifact picker command returned nil message")
	}
	if opened != path {
		t.Fatalf("artifact picker opened %q, want %q", opened, path)
	}
}

func TestCodexArtifactPickerOpensSelectedImageTargets(t *testing.T) {
	dir := t.TempDir()
	firstPath := filepath.Join(dir, "mockup.png")
	secondPath := filepath.Join(dir, "generated.png")
	imageBytes := mustTestPNG(color.RGBA{R: 40, G: 180, B: 220, A: 255})
	for _, path := range []string{firstPath, secondPath} {
		if err := os.WriteFile(path, imageBytes, 0o600); err != nil {
			t.Fatalf("write image: %v", err)
		}
	}
	snapshot := codexapp.Snapshot{
		ProjectPath: "/tmp/demo",
		Entries: []codexapp.TranscriptEntry{
			{
				Kind: codexapp.TranscriptAgent,
				Text: "See [GPT Image Mockup](" + firstPath + ").",
			},
			{
				Kind: codexapp.TranscriptTool,
				Text: "Generated image\n" + secondPath,
				GeneratedImage: &codexapp.GeneratedImageArtifact{
					ID:          "ig_demo",
					Path:        secondPath,
					Width:       4,
					Height:      4,
					ByteSize:    int64(len(imageBytes)),
					PreviewData: imageBytes,
				},
			},
		},
	}

	opened := ""
	oldOpener := externalPathOpener
	externalPathOpener = func(path string) error {
		opened = path
		return nil
	}
	t.Cleanup(func() { externalPathOpener = oldOpener })

	m := Model{
		codexVisibleProject: "/tmp/demo",
		codexSnapshots: map[string]codexapp.Snapshot{
			"/tmp/demo": snapshot,
		},
		codexViewport: viewport.New(80, 20),
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}, Alt: true})
	if cmd != nil {
		t.Fatalf("Alt+O should open the artifact picker without a command, got %T", cmd)
	}
	got := normalizeUpdateModel(updated)
	if got.codexArtifactPicker == nil {
		t.Fatalf("Alt+O should show the artifact picker")
	}
	if got.codexArtifactPicker.Selected != 1 {
		t.Fatalf("picker selected = %d, want latest image index 1", got.codexArtifactPicker.Selected)
	}
	overlay := ansi.Strip(got.renderCodexArtifactPicker(80, 24))
	for _, want := range []string{"Open Links", "GPT Image Mockup", "generated.png", "Enter/Alt+O", "open", "Esc", "close"} {
		if !strings.Contains(overlay, want) {
			t.Fatalf("artifact picker missing %q: %q", want, overlay)
		}
	}
	if !strings.Contains(got.renderCodexArtifactPicker(80, 24), "\x1b[38;2;") {
		t.Fatalf("artifact picker should render the selected generated image preview")
	}

	updated, cmd = got.updateCodexArtifactPickerMode(tea.KeyMsg{Type: tea.KeyUp})
	if cmd == nil {
		t.Fatalf("moving to a path-only image should queue a preview load")
	}
	rawPreviewMsg := cmd()
	previewMsg, ok := rawPreviewMsg.(codexArtifactPreviewMsg)
	if !ok {
		t.Fatalf("preview command returned %T, want codexArtifactPreviewMsg", rawPreviewMsg)
	}
	got = normalizeUpdateModel(updated)
	if got.codexArtifactPicker == nil || got.codexArtifactPicker.Selected != 0 {
		t.Fatalf("picker selected after up = %#v, want index 0", got.codexArtifactPicker)
	}
	updated, cmd = got.Update(previewMsg)
	if cmd != nil {
		t.Fatalf("applying preview should not queue a command, got %T", cmd)
	}
	got = normalizeUpdateModel(updated)
	if !strings.Contains(got.renderCodexArtifactPicker(80, 24), "\x1b[38;2;") {
		t.Fatalf("artifact picker should render loaded markdown image preview")
	}

	updated, cmd = got.updateCodexArtifactPickerMode(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatalf("Enter on selected image should queue an open command")
	}
	if msg := cmd(); msg == nil {
		t.Fatalf("artifact open command returned nil message")
	}
	if opened != firstPath {
		t.Fatalf("Enter opened %q, want selected path %q", opened, firstPath)
	}
	got = normalizeUpdateModel(updated)
	if got.codexArtifactPicker != nil {
		t.Fatalf("picker should close after opening an image")
	}
}

func TestCodexArtifactPickerLoadsPreviewForPathOnlyImage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mockup.png")
	imageBytes := mustTestPNG(color.RGBA{R: 40, G: 180, B: 220, A: 255})
	if err := os.WriteFile(path, imageBytes, 0o600); err != nil {
		t.Fatalf("write image: %v", err)
	}
	snapshot := codexapp.Snapshot{
		ProjectPath: "/tmp/demo",
		Entries: []codexapp.TranscriptEntry{
			{
				Kind: codexapp.TranscriptAgent,
				Text: "See [GPT Image Mockup](" + path + ").",
			},
		},
	}
	m := Model{
		codexVisibleProject: "/tmp/demo",
		codexSnapshots: map[string]codexapp.Snapshot{
			"/tmp/demo": snapshot,
		},
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}, Alt: true})
	if cmd == nil {
		t.Fatalf("Alt+O should queue a preview load for a path-only image")
	}
	got := normalizeUpdateModel(updated)
	if got.codexArtifactPicker == nil {
		t.Fatalf("Alt+O should open artifact picker")
	}
	if preview := ansi.Strip(got.renderCodexArtifactPicker(80, 24)); !strings.Contains(preview, "Loading preview") {
		t.Fatalf("picker should show loading preview while path image loads: %q", preview)
	}

	rawPreviewMsg := cmd()
	msg, ok := rawPreviewMsg.(codexArtifactPreviewMsg)
	if !ok {
		t.Fatalf("preview command returned %T, want codexArtifactPreviewMsg", rawPreviewMsg)
	}
	updated, cmd = got.Update(msg)
	if cmd != nil {
		t.Fatalf("preview message should not queue command, got %T", cmd)
	}
	got = normalizeUpdateModel(updated)
	if !strings.Contains(got.renderCodexArtifactPicker(80, 24), "\x1b[38;2;") {
		t.Fatalf("picker should render loaded path image preview")
	}
}

func TestCodexArtifactPickerListsPDFMarkdownLinksWithoutPreview(t *testing.T) {
	path := filepath.Join(t.TempDir(), "brief.pdf")
	if err := os.WriteFile(path, []byte("%PDF-1.7\n"), 0o600); err != nil {
		t.Fatalf("write pdf: %v", err)
	}
	snapshot := codexapp.Snapshot{
		ProjectPath: "/tmp/demo",
		Entries: []codexapp.TranscriptEntry{
			{
				Kind: codexapp.TranscriptAgent,
				Text: "Open [brief](" + path + ").",
			},
		},
	}

	opened := ""
	oldOpener := externalPathOpener
	externalPathOpener = func(path string) error {
		opened = path
		return nil
	}
	t.Cleanup(func() { externalPathOpener = oldOpener })

	m := Model{
		codexVisibleProject: "/tmp/demo",
		codexSnapshots: map[string]codexapp.Snapshot{
			"/tmp/demo": snapshot,
		},
	}

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}, Alt: true})
	if cmd != nil {
		t.Fatalf("PDF artifact should not queue a preview command, got %T", cmd)
	}
	got := normalizeUpdateModel(updated)
	if got.codexArtifactPicker == nil || len(got.codexArtifactPicker.Targets) != 1 {
		t.Fatalf("PDF artifact picker state = %#v, want one target", got.codexArtifactPicker)
	}
	target := got.codexArtifactPicker.Targets[0]
	if target.Kind != "pdf" || target.Path != path {
		t.Fatalf("PDF artifact target = %#v, want kind pdf path %q", target, path)
	}
	overlay := ansi.Strip(got.renderCodexArtifactPicker(80, 24))
	for _, want := range []string{"Open Links", "PDF", "brief (brief.pdf)", filepath.Base(path)} {
		if !strings.Contains(overlay, want) {
			t.Fatalf("PDF artifact picker missing %q: %q", want, overlay)
		}
	}
	if strings.Contains(overlay, "Preview") {
		t.Fatalf("PDF artifact picker should not render a preview section: %q", overlay)
	}

	updated, cmd = got.updateCodexArtifactPickerMode(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatalf("Enter on PDF artifact should queue open command")
	}
	if msg := cmd(); msg == nil {
		t.Fatalf("PDF open command returned nil")
	}
	if opened != path {
		t.Fatalf("PDF picker opened %q, want %q", opened, path)
	}
	_ = updated
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

func TestCodexLinkPickerListsOnlyVisibleTranscriptLinks(t *testing.T) {
	snapshot := codexapp.Snapshot{
		ProjectPath: "/tmp/demo",
		Entries: []codexapp.TranscriptEntry{
			{
				Kind: codexapp.TranscriptAgent,
				Text: "Hidden [old docs](https://hidden.example/docs).",
			},
			{
				Kind: codexapp.TranscriptAgent,
				Text: "Visible [new docs](https://visible.example/docs).",
			},
		},
	}

	m := Model{
		codexVisibleProject: "/tmp/demo",
		codexSnapshots: map[string]codexapp.Snapshot{
			"/tmp/demo": snapshot,
		},
		codexViewport: viewport.New(80, 1),
	}
	rendered := m.renderAndCacheCodexTranscript("/tmp/demo", snapshot, 80)
	m.codexViewport.SetContent(rendered)
	m.codexViewport.SetYOffset(2)

	updated, cmd := m.updateCodexMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}, Alt: true})
	if cmd != nil {
		t.Fatalf("Alt+O should open the link picker without a command, got %T", cmd)
	}
	got := normalizeUpdateModel(updated)
	if got.codexArtifactPicker == nil || len(got.codexArtifactPicker.Targets) != 1 {
		t.Fatalf("visible link picker state = %#v, want exactly one visible link", got.codexArtifactPicker)
	}
	target := got.codexArtifactPicker.Targets[0]
	if target.Kind != "url" || target.Path != "https://visible.example/docs" {
		t.Fatalf("visible link target = %#v, want visible URL", target)
	}

	openedURL := ""
	oldBrowserOpener := externalBrowserOpener
	externalBrowserOpener = func(rawURL string) error {
		openedURL = rawURL
		return nil
	}
	t.Cleanup(func() { externalBrowserOpener = oldBrowserOpener })

	updated, cmd = got.updateCodexArtifactPickerMode(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatalf("Enter on visible URL should queue open command")
	}
	if msg := cmd(); msg == nil {
		t.Fatalf("visible URL open command returned nil")
	}
	if openedURL != "https://visible.example/docs" {
		t.Fatalf("visible URL opened %q, want visible URL", openedURL)
	}
	_ = updated
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
	if !strings.Contains(rendered, "a accept  d decline  c cancel  Esc hide") {
		t.Fatalf("file change approval footer missing expected keys: %q", rendered)
	}
}

func TestStoreCodexSnapshotOnlyInvalidatesTranscriptRevisionWhenTranscriptChanges(t *testing.T) {
	m := Model{}
	projectPath := "/tmp/demo"
	base := codexapp.Snapshot{
		Provider:           codexapp.ProviderCodex,
		TranscriptRevision: 1,
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
	changed.TranscriptRevision = 2
	changed.Entries = []codexapp.TranscriptEntry{{
		Kind: codexapp.TranscriptAgent,
		Text: "Updated reply",
	}}
	m.storeCodexSnapshot(projectPath, changed)
	if got := m.codexTranscriptRevision(projectPath); got != 2 {
		t.Fatalf("transcript update should bump transcript revision, got %d", got)
	}
}

func TestStoreCodexSnapshotIgnoresNoticeOnlyChangesWhenTranscriptHasEntries(t *testing.T) {
	m := Model{}
	projectPath := "/tmp/demo"
	base := codexapp.Snapshot{
		Provider:           codexapp.ProviderClaudeCode,
		TranscriptRevision: 7,
		Entries: []codexapp.TranscriptEntry{{
			Kind: codexapp.TranscriptAgent,
			Text: "Existing reply",
		}},
	}

	m.storeCodexSnapshot(projectPath, base)
	if got := m.codexTranscriptRevision(projectPath); got != 1 {
		t.Fatalf("initial transcript revision = %d, want 1", got)
	}

	noticeOnly := base
	noticeOnly.LastSystemNotice = "Claude Code will use opus, effort high on the next prompt."
	m.storeCodexSnapshot(projectPath, noticeOnly)
	if got := m.codexTranscriptRevision(projectPath); got != 1 {
		t.Fatalf("notice-only update with transcript entries should not bump transcript revision, got %d", got)
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
	if _, ok, _ := m.refreshCodexSnapshot("/tmp/demo"); !ok {
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

func TestRenderCodexViewDoesNotRenderTranscriptOnCacheMiss(t *testing.T) {
	projectPath := "/tmp/demo"
	m := Model{
		codexVisibleProject: projectPath,
		codexHiddenProject:  projectPath,
		codexInput:          newCodexTextarea(),
		codexViewport:       viewport.New(0, 0),
		width:               80,
		height:              20,
	}
	entries := make([]codexapp.TranscriptEntry, 0, codexCacheMissEntryLimit+1)
	for i := 0; i <= codexCacheMissEntryLimit; i++ {
		entries = append(entries, codexapp.TranscriptEntry{
			Kind: codexapp.TranscriptAgent,
			Text: fmt.Sprintf("expensive transcript content %02d should not render from View", i),
		})
	}
	m.storeCodexSnapshot(projectPath, codexapp.Snapshot{
		Provider: codexapp.ProviderCodex,
		Started:  true,
		Entries:  entries,
	})

	rendered := ansi.Strip(m.renderCodexView())
	if strings.Contains(rendered, "expensive transcript content") {
		t.Fatalf("renderCodexView() should not render transcript content on cache miss: %q", rendered)
	}
	if !strings.Contains(rendered, "Transcript is updating") {
		t.Fatalf("renderCodexView() should show a small cache-miss placeholder: %q", rendered)
	}
}

func TestRenderCodexTranscriptEntriesLimitsLiveTail(t *testing.T) {
	entries := make([]codexapp.TranscriptEntry, 0, codexTranscriptLiveEntryLimit+24)
	for i := 0; i < codexTranscriptLiveEntryLimit+24; i++ {
		entries = append(entries, codexapp.TranscriptEntry{
			Kind: codexapp.TranscriptAgent,
			Text: fmt.Sprintf("old reply %03d", i),
		})
	}
	entries = append(entries, codexapp.TranscriptEntry{
		Kind: codexapp.TranscriptAgent,
		Text: "latest reply survives the live-view cap",
	})

	rendered := ansi.Strip((Model{}).renderCodexTranscriptEntries(codexapp.Snapshot{Entries: entries}, 80))
	if !strings.Contains(rendered, "Older transcript hidden from live view") {
		t.Fatalf("rendered transcript should include an omission marker: %q", rendered)
	}
	if strings.Contains(rendered, "old reply 000") {
		t.Fatalf("rendered transcript should omit the oldest entries: %q", rendered)
	}
	if !strings.Contains(rendered, "latest reply survives the live-view cap") {
		t.Fatalf("rendered transcript should keep the latest entry: %q", rendered)
	}
}

func TestRenderCodexTranscriptEntriesBudgetsDenseCommandByRenderedSummary(t *testing.T) {
	noisyOutput := make([]string, 0, codexTranscriptLiveLineLimit+16)
	noisyOutput = append(noisyOutput, "$ make test")
	for i := 0; i < codexTranscriptLiveLineLimit+15; i++ {
		noisyOutput = append(noisyOutput, fmt.Sprintf("noisy validation line %04d", i))
	}
	entries := []codexapp.TranscriptEntry{
		{Kind: codexapp.TranscriptAgent, Text: "important design explanation remains visible"},
		{Kind: codexapp.TranscriptCommand, Text: strings.Join(noisyOutput, "\n")},
		{Kind: codexapp.TranscriptAgent, Text: "final closeout"},
	}

	rendered := ansi.Strip((Model{}).renderCodexTranscriptEntries(codexapp.Snapshot{Entries: entries}, 80))
	if strings.Contains(rendered, "Older transcript hidden from live view") {
		t.Fatalf("dense command output should not consume the live history budget: %q", rendered)
	}
	if !strings.Contains(rendered, "important design explanation remains visible") {
		t.Fatalf("rendered transcript should keep the earlier explanation: %q", rendered)
	}
}

func TestCodexViewportLoadsFullHistoryWhenScrolledToHiddenMarker(t *testing.T) {
	projectPath := "/tmp/demo"
	entries := make([]codexapp.TranscriptEntry, 0, codexTranscriptLiveEntryLimit+2)
	for i := 0; i < codexTranscriptLiveEntryLimit+2; i++ {
		entries = append(entries, codexapp.TranscriptEntry{
			Kind: codexapp.TranscriptAgent,
			Text: fmt.Sprintf("reply %03d", i),
		})
	}
	m := Model{
		codexVisibleProject: projectPath,
		codexHiddenProject:  projectPath,
		codexInput:          newCodexTextarea(),
		codexViewport:       viewport.New(0, 0),
		width:               80,
		height:              16,
	}
	m.storeCodexSnapshot(projectPath, codexapp.Snapshot{
		Provider:    codexapp.ProviderCodex,
		ProjectPath: projectPath,
		Started:     true,
		Entries:     entries,
	})
	m.syncCodexViewport(true)

	if strings.Contains(ansi.Strip(m.codexViewport.View()), "reply 000") {
		t.Fatalf("tail-limited viewport should not start with the oldest reply")
	}
	m.codexViewport.GotoTop()
	if !m.maybeLoadFullCodexHistoryAtViewportTop() {
		t.Fatal("expected viewport top to load full transcript history")
	}
	if !m.codexTranscriptFullHistoryLoaded(projectPath) {
		t.Fatal("full transcript history should be marked loaded for the project")
	}
	rendered := ansi.Strip(m.codexViewport.View())
	if !strings.Contains(rendered, "reply 000") {
		t.Fatalf("full history viewport should show the oldest reply after expansion: %q", rendered)
	}
}

func TestRenderCodexFooterPrioritizesSendCloseHideAndDefersDenseBlocks(t *testing.T) {
	rendered := ansi.Strip((Model{}).renderCodexFooter(codexapp.Snapshot{
		Started: true,
		Status:  "Codex session ready",
	}, 140))

	enterIndex := strings.Index(rendered, "Enter send")
	closeIndex := strings.Index(rendered, "Ctrl+C close")
	hideIndex := strings.Index(rendered, "Esc hide")
	if enterIndex < 0 || closeIndex < 0 || hideIndex < 0 {
		t.Fatalf("renderCodexFooter() missing expected footer actions: %q", rendered)
	}
	if !(enterIndex < closeIndex && closeIndex < hideIndex) {
		t.Fatalf("renderCodexFooter() order = %q, want Enter send before Ctrl+C close before Esc hide", rendered)
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

func TestSpinnerTickRecordsUIStallLatency(t *testing.T) {
	now := time.Date(2026, time.April, 3, 10, 0, 0, 0, time.UTC)
	base := Model{
		codexVisibleProject: "/tmp/demo",
		lastSpinnerTickAt:   now,
		nowFn:               func() time.Time { return now },
	}

	now = now.Add(10 * time.Second)
	nextModel, _ := base.Update(spinnerTickMsg{})
	next, ok := nextModel.(Model)
	if !ok {
		t.Fatalf("Update() returned %T, want tui.Model", nextModel)
	}

	found := false
	for _, sample := range next.aiLatencyRecent {
		if sample.Name != "UI stall" {
			continue
		}
		found = true
		if sample.ProjectPath != "/tmp/demo" {
			t.Fatalf("UI stall project = %q, want /tmp/demo", sample.ProjectPath)
		}
		if sample.Duration != 10*time.Second-spinnerTickInterval {
			t.Fatalf("UI stall duration = %v, want %v", sample.Duration, 10*time.Second-spinnerTickInterval)
		}
		if sample.Result != "event loop blocked" {
			t.Fatalf("UI stall result = %q, want event loop blocked", sample.Result)
		}
	}
	if !found {
		t.Fatalf("spinner stall should record a UI stall sample, got %#v", next.aiLatencyRecent)
	}
}

func TestSpinnerTickDoesNotResyncRuntimeViewport(t *testing.T) {
	base := Model{
		projects: []model.ProjectSummary{{
			Path:          "/tmp/demo",
			Name:          "demo",
			PresentOnDisk: true,
		}},
		selected:        0,
		spinnerFrame:    0,
		runtimeViewport: viewport.New(20, 5),
		runtimeSnapshots: map[string]projectrun.Snapshot{
			"/tmp/demo": {
				ProjectPath:  "/tmp/demo",
				RecentOutput: []string{"fresh runtime output"},
			},
		},
		width:  100,
		height: 24,
	}
	base.runtimeViewport.SetContent("stale runtime cache")

	nextModel, _ := base.Update(spinnerTickMsg{})
	next, ok := nextModel.(Model)
	if !ok {
		t.Fatalf("Update() returned %T, want tui.Model", nextModel)
	}

	rendered := ansi.Strip(next.runtimeViewport.View())
	if !strings.Contains(rendered, "stale runtime cache") {
		t.Fatalf("spinnerTick should not resync runtime output on the UI thread, got %q", rendered)
	}
	if strings.Contains(rendered, "fresh runtime output") {
		t.Fatalf("spinnerTick should leave runtime viewport refreshes to runtime snapshot updates, got %q", rendered)
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

func TestCommandEnterUsesHighlightedSuggestionOverValidPrefix(t *testing.T) {
	input := textinput.New()
	input.SetValue("/open")

	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider: req.Provider.Normalized(),
				Started:  true,
				Status:   req.Provider.Label() + " session ready",
			},
		}, nil
	})

	m := Model{
		commandMode:  true,
		commandInput: input,
		codexManager: manager,
		projects: []model.ProjectSummary{{
			Path:          "/tmp/demo",
			Name:          "demo",
			PresentOnDisk: true,
		}},
		selected:      0,
		codexInput:    newCodexTextarea(),
		codexDrafts:   make(map[string]codexDraft),
		codexViewport: viewport.New(0, 0),
		width:         100,
		height:        24,
	}
	m.syncCommandSelection()
	m.moveCommandSelection(1)

	updated, cmd := m.updateCommandMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("enter should launch the highlighted /opencode suggestion")
	}
	if got.commandMode {
		t.Fatalf("command mode should close after executing the highlighted suggestion")
	}
	if got.codexPendingOpen == nil || got.codexPendingOpen.provider != codexapp.ProviderOpenCode {
		t.Fatalf("codexPendingOpen = %#v, want pending OpenCode launch", got.codexPendingOpen)
	}

	msg := cmd()
	opened, ok := msg.(codexSessionOpenedMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want codexSessionOpenedMsg", msg)
	}
	if opened.err != nil {
		t.Fatalf("highlighted /opencode returned error = %v", opened.err)
	}
	if len(requests) != 1 || requests[0].Provider != codexapp.ProviderOpenCode {
		t.Fatalf("launch requests = %#v, want one OpenCode launch", requests)
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
	if len(got.settingsFields) != 20 {
		t.Fatalf("settings field count = %d, want 20", len(got.settingsFields))
	}
}

func TestCommandEnterOpensSkillsDialog(t *testing.T) {
	input := textinput.New()
	input.SetValue("/skills")

	m := Model{
		commandMode:  true,
		commandInput: input,
		width:        100,
		height:       24,
	}
	m.syncCommandSelection()

	updated, cmd := m.updateCommandMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if got.skillsDialog == nil || !got.skillsDialog.Loading {
		t.Fatalf("skills dialog should open loading after /skills, got %#v", got.skillsDialog)
	}
	if got.commandMode {
		t.Fatalf("command mode should close after /skills")
	}
	if cmd == nil {
		t.Fatalf("/skills should return a skills inventory load command")
	}
}

func TestCommandEnterOpensBossMode(t *testing.T) {
	input := textinput.New()
	input.SetValue("/boss")
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.BossChatBackend = config.AIBackendOpenAIAPI
	settings.OpenAIAPIKey = "sk-test-example"

	m := Model{
		commandMode:      true,
		commandInput:     input,
		settingsBaseline: &settings,
		width:            100,
		height:           24,
	}
	m.syncCommandSelection()

	updated, cmd := m.updateCommandMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if !got.bossMode {
		t.Fatalf("boss mode should open after /boss")
	}
	if got.commandMode {
		t.Fatalf("command mode should close after /boss")
	}
	if cmd == nil {
		t.Fatalf("/boss should return the embedded boss init command")
	}
	rendered := ansi.Strip(got.View())
	if !strings.Contains(rendered, "Boss Chat") || !strings.Contains(rendered, "Boss Desk") {
		t.Fatalf("boss view missing expected panels: %q", rendered)
	}
	if strings.Contains(rendered, "Jump") || strings.Contains(rendered, "Situation") || strings.Contains(rendered, "Notes") {
		t.Fatalf("boss view should not render the old side panels: %q", rendered)
	}
	if strings.Contains(rendered, "Little Room") {
		t.Fatalf("boss view should use the shared TUI panel language, got: %q", rendered)
	}
	lines := strings.Split(rendered, "\n")
	if len(lines) != got.height {
		t.Fatalf("boss view line count = %d, want terminal height %d: %q", len(lines), got.height, rendered)
	}
	if len(lines) < 2 || !strings.Contains(lines[0], "Boss Mode") {
		t.Fatalf("boss view should use a boss-specific top status line: %q", rendered)
	}
	if !strings.HasPrefix(lines[1], "╭") {
		t.Fatalf("boss frames should start below the boss top status line: %q", rendered)
	}
	if strings.Contains(lines[0], brand.Name) {
		t.Fatalf("boss view should not show the classic app title in the top bar: %q", rendered)
	}
	if !strings.Contains(lines[len(lines)-1], "Enter") || !strings.Contains(lines[len(lines)-1], "Alt+Enter") || !strings.Contains(lines[len(lines)-1], "Esc") {
		t.Fatalf("boss footer should show boss chat actions: %q", rendered)
	}
	if strings.Contains(lines[len(lines)-1], "Ctrl+J") {
		t.Fatalf("boss footer should advertise Alt+Enter newline, not Ctrl+J: %q", rendered)
	}
	if strings.Contains(lines[len(lines)-1], "q quit") {
		t.Fatalf("boss footer should not show the classic q quit action: %q", rendered)
	}
	lastBodyLine := ""
	for i := len(lines) - 2; i >= 1; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			lastBodyLine = lines[i]
			break
		}
	}
	if lastBodyLine == "" || !strings.Contains(lastBodyLine, "╰") {
		t.Fatalf("boss footer should consume only one row and leave frame content above it: %q", rendered)
	}
	for _, line := range strings.Split(got.View(), "\n") {
		if gotWidth := ansi.StringWidth(ansi.Strip(line)); gotWidth > got.width {
			t.Fatalf("boss view line width = %d, want <= %d: %q", gotWidth, got.width, ansi.Strip(line))
		}
	}
}

func TestCommandEnterOpensBossModeWithMouseCapture(t *testing.T) {
	input := textinput.New()
	input.SetValue("/boss")
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.BossChatBackend = config.AIBackendOpenAIAPI
	settings.OpenAIAPIKey = "sk-test-example"

	m := Model{
		commandMode:      true,
		commandInput:     input,
		settingsBaseline: &settings,
		width:            100,
		height:           24,
	}
	m.syncCommandSelection()

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if !got.bossMode {
		t.Fatalf("boss mode should open after /boss")
	}
	if !got.mouseEnabled {
		t.Fatalf("boss mode should enable mouse capture for scoped chat selection")
	}

	updated, _ = got.Update(bossui.ExitMsg{})
	got = updated.(Model)
	if got.mouseEnabled {
		t.Fatalf("closing boss mode should release mouse capture")
	}
}

func TestCommandEnterBossUnconfiguredShowsSetupPrompt(t *testing.T) {
	input := textinput.New()
	input.SetValue("/boss")

	m := Model{
		commandMode:  true,
		commandInput: input,
		width:        100,
		height:       24,
	}
	m.syncCommandSelection()

	updated, cmd := m.updateCommandMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("unconfigured /boss should not start async boss work")
	}
	if got.bossMode {
		t.Fatalf("unconfigured /boss should not open boss mode")
	}
	if got.bossSetupPrompt == nil {
		t.Fatalf("unconfigured /boss should open the setup prompt")
	}
	if got.commandMode {
		t.Fatalf("command mode should close after /boss")
	}
	if strings.Contains(got.bossSetupPrompt.Reason, "saved OpenAI API key") {
		t.Fatalf("default boss setup prompt reason should not imply OpenAI-only setup: %q", got.bossSetupPrompt.Reason)
	}
	if strings.Contains(got.bossSetupPrompt.Reason, "OpenAI API") || strings.Contains(got.bossSetupPrompt.Reason, "MLX") || strings.Contains(got.bossSetupPrompt.Reason, "Ollama") {
		t.Fatalf("default boss setup prompt reason should stay provider-agnostic: %q", got.bossSetupPrompt.Reason)
	}
	if !strings.Contains(got.bossSetupPrompt.Reason, "/setup") || !strings.Contains(got.bossSetupPrompt.Reason, "boss chat backend") {
		t.Fatalf("boss setup prompt reason = %q, want setup guidance", got.bossSetupPrompt.Reason)
	}
	rendered := ansi.Strip(got.View())
	for _, want := range []string{"Boss Chat Setup", "Open /setup", "Cancel"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("boss setup prompt missing %q: %q", want, rendered)
		}
	}
}

func TestBossSetupPromptEnterOpensSetupFocusedOnBossChat(t *testing.T) {
	m := Model{
		bossSetupPrompt: &bossSetupPromptState{Selected: bossSetupPromptOpenSetup},
		width:           100,
		height:          24,
	}

	updated, cmd := m.updateBossSetupPromptMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("opening setup from boss prompt should refresh provider status")
	}
	if got.bossSetupPrompt != nil {
		t.Fatalf("boss setup prompt should close")
	}
	if !got.setupMode {
		t.Fatalf("setup mode should open")
	}
	if got.setupFocusedRole != setupRoleBossChat {
		t.Fatalf("setup focused role = %v, want boss chat", got.setupFocusedRole)
	}
	if got.setupSelectedBossBackend() != config.AIBackendOpenAIAPI {
		t.Fatalf("selected boss backend = %s, want openai_api", got.setupSelectedBossBackend())
	}
}

func TestBossModeEscReturnsToClassicTUI(t *testing.T) {
	m := Model{
		bossMode:  true,
		bossModel: bossui.NewEmbedded(context.Background(), nil),
		width:     100,
		height:    24,
	}

	updated, cmd := m.updateBossModeMessage(tea.KeyMsg{Type: tea.KeyEsc})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("boss esc should return an exit command")
	}
	msg := cmd()
	if _, ok := msg.(bossui.ExitMsg); !ok {
		t.Fatalf("cmd() returned %T, want boss.ExitMsg", msg)
	}

	updated, _ = got.Update(msg)
	got = updated.(Model)
	if got.bossMode {
		t.Fatalf("boss mode should hide after exit message")
	}
	if got.status != "Boss mode hidden" {
		t.Fatalf("status = %q, want Boss mode hidden", got.status)
	}
}

func TestBossModeAltUpDoesNotReturnToClassicTUI(t *testing.T) {
	m := Model{
		bossMode:  true,
		bossModel: bossui.NewEmbedded(context.Background(), nil),
		width:     100,
		height:    24,
	}

	updated, cmd := m.updateBossModeMessage(tea.KeyMsg{Type: tea.KeyUp, Alt: true})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("boss alt+up should not return an exit command")
	}
	if !got.bossMode {
		t.Fatalf("boss mode should stay visible after alt+up")
	}
}

func TestBossModeFooterDoesNotCoverTerminalFrames(t *testing.T) {
	for _, height := range []int{52, 45, 18, 13} {
		m := Model{
			bossMode:  true,
			bossModel: bossui.NewEmbedded(context.Background(), nil),
			width:     180,
			height:    height,
		}

		updated, _ := m.updateBossModeWindowSize()
		got := updated.(Model)
		rendered := ansi.Strip(got.View())
		lines := strings.Split(rendered, "\n")
		if len(lines) != got.height {
			t.Fatalf("boss view line count = %d, want terminal height %d:\n%s", len(lines), got.height, rendered)
		}
		if !strings.Contains(lines[len(lines)-1], "Enter") {
			t.Fatalf("boss footer should be the final row:\n%s", rendered)
		}
		lastBodyLine := ""
		for i := len(lines) - 2; i >= 1; i-- {
			if strings.TrimSpace(lines[i]) != "" {
				lastBodyLine = lines[i]
				break
			}
		}
		if !strings.HasPrefix(lastBodyLine, "╰") {
			t.Fatalf("boss footer should not cover the bottom frame row at height %d:\n%s", height, rendered)
		}
	}
}

func TestBossModeForwardsTypingToChatInput(t *testing.T) {
	m := Model{
		bossMode:  true,
		bossModel: bossui.NewEmbedded(context.Background(), nil),
		width:     100,
		height:    24,
	}

	updated, _ := m.updateBossModeMessage(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h', 'i'}})
	got := updated.(Model)
	rendered := ansi.Strip(got.View())
	if !strings.Contains(rendered, "hi") {
		t.Fatalf("boss view should show typed input, got %q", rendered)
	}
}

func TestBossModeRoutesSessionLoadBeforeEnterSubmit(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dataDir := t.TempDir()
	st, err := store.Open(filepath.Join(dataDir, "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	cfg := config.Default()
	cfg.DataDir = dataDir
	cfg.DBPath = filepath.Join(dataDir, "little-control-room.sqlite")
	svc := service.New(cfg, st, events.NewBus(), nil)
	m := New(ctx, svc)
	m.width = 100
	m.height = 24

	updated, initCmd := m.openBossMode()
	got := updated.(Model)
	for _, msg := range collectCmdMsgs(initCmd) {
		if _, ok := msg.(bossui.TickMsg); ok {
			continue
		}
		if !bossui.IsMessage(msg) {
			continue
		}
		updated, _ = got.Update(msg)
		got = updated.(Model)
	}

	updated, _ = got.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("hello boss")})
	got = updated.(Model)
	updated, cmd := got.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if cmd == nil {
		t.Fatalf("enter should submit after session load; boss status = %q", got.bossModel.StatusText())
	}
	if strings.Contains(got.bossModel.StatusText(), "session is still loading") {
		t.Fatalf("boss status = %q, want submitted chat", got.bossModel.StatusText())
	}
}

func TestDispatchBossOffClosesBossMode(t *testing.T) {
	m := Model{
		bossMode:  true,
		bossModel: bossui.NewEmbedded(context.Background(), nil),
		width:     100,
		height:    24,
	}

	updated, cmd := m.dispatchCommand(commands.Invocation{Kind: commands.KindBoss, Toggle: commands.ToggleOff})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("/boss off should not return async work")
	}
	if got.bossMode {
		t.Fatalf("/boss off should hide boss mode")
	}
}

func TestDispatchCommandRefreshAlsoRefreshesSelectedProject(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	projectPath := filepath.Join(t.TempDir(), "demo")
	if err := os.MkdirAll(projectPath, 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:           projectPath,
		Name:           "demo",
		Status:         model.StatusIdle,
		AttentionScore: 5,
		PresentOnDisk:  true,
		InScope:        true,
		UpdatedAt:      time.Now(),
	}); err != nil {
		t.Fatalf("seed project: %v", err)
	}

	svc := service.New(config.Default(), st, events.NewBus(), nil)
	svc.SetSessionClassifier(&usageSnapshotClassifier{})

	m := Model{
		ctx:         ctx,
		svc:         svc,
		allProjects: []model.ProjectSummary{{Path: projectPath, Name: "demo", PresentOnDisk: true}},
		visibility:  visibilityAllFolders,
		sortMode:    sortByAttention,
	}
	m.rebuildProjectList(projectPath)

	updated, cmd := m.dispatchCommand(commands.Invocation{Kind: commands.KindRefresh})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("dispatchCommand(/refresh) should queue refresh work")
	}
	if !got.scanInFlight {
		t.Fatalf("dispatchCommand(/refresh) should mark scan in flight")
	}
	if got.status != "Scanning and retrying failed assessments..." {
		t.Fatalf("status = %q, want refresh status", got.status)
	}

	msgs := collectCmdMsgs(cmd)
	foundProjectRefresh := false
	foundScan := false
	for _, msg := range msgs {
		switch typed := msg.(type) {
		case projectStatusRefreshedMsg:
			if typed.projectPath == projectPath && typed.err == nil {
				foundProjectRefresh = true
			}
		case scanMsg:
			if typed.err == nil {
				foundScan = true
			}
		}
	}
	if !foundProjectRefresh {
		t.Fatalf("expected projectStatusRefreshedMsg for selected project, got %#v", msgs)
	}
	if !foundScan {
		t.Fatalf("expected scanMsg from /refresh, got %#v", msgs)
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
	if got.status != "Choose AI roles for project reports and boss chat." {
		t.Fatalf("status = %q, want startup setup explanation", got.status)
	}
	if cmd == nil {
		t.Fatalf("opening setup should return a refresh command")
	}
}

func TestStartupSetupSnapshotCmdSkippedWhenBackendConfigured(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.AIBackend = config.AIBackendCodex

	m := Model{settingsBaseline: &settings}
	if cmd := m.startupSetupSnapshotCmd(); cmd != nil {
		t.Fatalf("startupSetupSnapshotCmd() should skip configured backends")
	}
}

func TestStartupSetupSnapshotCmdRunsWhenBackendUnset(t *testing.T) {
	m := Model{}
	if cmd := m.startupSetupSnapshotCmd(); cmd == nil {
		t.Fatalf("startupSetupSnapshotCmd() should run when no backend is configured")
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

func TestRenderSetupOptionRowShowsLocalModelSelection(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.AIBackend = config.AIBackendMLX
	settings.MLXModel = "mlx-community/Qwen3.5-9B-MLX-4bit"

	m := Model{
		settingsBaseline: &settings,
		setupSnapshot: aibackend.Snapshot{
			Selected: config.AIBackendMLX,
			MLX: aibackend.Status{
				Backend:     config.AIBackendMLX,
				Label:       "MLX",
				Ready:       true,
				Endpoint:    "http://127.0.0.1:8080/v1",
				Models:      []string{"mlx-community/Qwen3.5-9B-MLX-4bit", "mlx-community/Qwen3.5-4B-MLX-4bit"},
				ActiveModel: "mlx-community/Qwen3.5-9B-MLX-4bit",
			},
		},
	}

	row := ansi.Strip(m.renderSetupOptionRow(config.AIBackendMLX, true, 120))
	if !strings.Contains(row, "using mlx-community/Qwen3.5-9B-MLX-4bit") {
		t.Fatalf("setup row = %q, want configured local model detail", row)
	}
}

func TestOpenSetupModeCanPreferReadyClaudeBackend(t *testing.T) {
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
			Claude: aibackend.Status{
				Backend:       config.AIBackendClaude,
				Label:         "Claude Code",
				Installed:     true,
				Authenticated: true,
				Ready:         true,
				Detail:        "Claude Code ready via claude.ai (max)",
			},
		},
	}

	_ = m.openSetupMode()
	if got := m.setupSelectedBackend(); got != config.AIBackendClaude {
		t.Fatalf("setupSelectedBackend() = %s, want %s", got, config.AIBackendClaude)
	}
}

func TestSetupLoadingBlocksRepeatRefresh(t *testing.T) {
	m := Model{
		setupMode:    true,
		setupLoading: true,
		status:       "Refreshing AI backend checks...",
	}

	updated, cmd := m.updateSetupMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("setup refresh should not queue another command while loading")
	}
	if !got.setupLoading {
		t.Fatalf("setup loading should remain true while the existing refresh is in flight")
	}
	if got.status != "Refreshing AI backend checks..." {
		t.Fatalf("status = %q, want existing refresh status", got.status)
	}
}

func TestSetupEnterMarksSavingAndBlocksRepeatEnter(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	m := Model{
		setupMode:     true,
		setupSelected: mSetupSelectionForTest(config.AIBackendDisabled),
		setupSnapshot: aibackend.Snapshot{
			Selected: config.AIBackendDisabled,
		},
	}

	updated, cmd := m.updateSetupMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("setup enter should queue a save command")
	}
	if !got.setupSaving {
		t.Fatalf("setup enter should mark saving in progress")
	}
	if got.status != "Saving AI setup..." {
		t.Fatalf("status = %q, want saving message", got.status)
	}

	updated, cmd = got.updateSetupMode(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if cmd != nil {
		t.Fatalf("setup enter should not queue another save while saving")
	}
	if !got.setupSaving {
		t.Fatalf("setup saving flag should stay true until the save completes")
	}
}

func TestSetupTabFocusesBossChatRole(t *testing.T) {
	m := Model{
		setupMode:         true,
		setupFocusedRole:  setupRoleProjectReports,
		setupBossSelected: 0,
	}

	updated, cmd := m.updateSetupMode(tea.KeyMsg{Type: tea.KeyTab})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("switching setup roles should not queue a command")
	}
	if got.setupFocusedRole != setupRoleBossChat {
		t.Fatalf("setup focused role = %v, want boss chat", got.setupFocusedRole)
	}

	updated, _ = got.updateSetupMode(tea.KeyMsg{Type: tea.KeyDown})
	got = updated.(Model)
	if got.setupSelectedBossBackend() != config.AIBackendOpenAIAPI {
		t.Fatalf("boss chat selected backend = %s, want openai_api", got.setupSelectedBossBackend())
	}
}

func TestSetupBossChatDisabledSavesSeparately(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.AIBackend = config.AIBackendCodex

	m := Model{
		setupMode:         true,
		settingsBaseline:  &settings,
		setupFocusedRole:  setupRoleBossChat,
		setupBossSelected: mSetupBossSelectionForTest(config.AIBackendDisabled),
	}

	updated, cmd := m.updateSetupMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("choosing disabled boss chat should queue a save command")
	}
	if !got.setupSaving {
		t.Fatalf("choosing disabled boss chat should mark setup saving")
	}
	if got.currentSettingsBaseline().AIBackend != config.AIBackendCodex {
		t.Fatalf("boss chat selection should not change project reports backend")
	}
}

func TestSetupOpenAIKeyEditsInlineInsteadOfOpeningSettings(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.AIBackend = config.AIBackendCodex
	settings.OpenAIAPIKey = ""

	m := Model{
		setupMode:        true,
		settingsBaseline: &settings,
		settingsFields:   newSettingsFields(settings),
		setupFocusedRole: setupRoleProjectReports,
		setupSelected:    mSetupSelectionForTest(config.AIBackendOpenAIAPI),
	}

	updated, cmd := m.updateSetupMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("entering OpenAI setup should focus the inline config field")
	}
	if !got.setupConfigMode {
		t.Fatalf("OpenAI setup should enter inline setup config mode")
	}
	if got.settingsMode {
		t.Fatalf("OpenAI setup should not open /settings")
	}
	if got.setupSelectedConfigFieldIndex() != settingsFieldOpenAIAPIKey {
		t.Fatalf("focused setup field = %d, want OpenAI API key", got.setupSelectedConfigFieldIndex())
	}
}

func TestSetupInlineConfigSaveUsesEditedFields(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.AIBackend = config.AIBackendCodex

	m := Model{
		setupMode:           true,
		setupConfigMode:     true,
		settingsBaseline:    &settings,
		settingsFields:      newSettingsFields(settings),
		setupFocusedRole:    setupRoleBossChat,
		setupBossSelected:   mSetupBossSelectionForTest(config.AIBackendOpenAIAPI),
		setupConfigSelected: 0,
	}
	m.settingsFields[settingsFieldOpenAIAPIKey].input.SetValue("sk-inline-test")

	updated, cmd := m.updateSetupMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("saving inline setup fields should queue a save command")
	}
	if !got.setupSaving {
		t.Fatalf("saving inline setup fields should mark setup saving")
	}
	msg := cmd()
	saved, ok := msg.(setupSavedMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want setupSavedMsg", msg)
	}
	if saved.err != nil {
		t.Fatalf("setup save returned error: %v", saved.err)
	}
	if saved.settings.OpenAIAPIKey != "sk-inline-test" {
		t.Fatalf("saved OpenAI key = %q, want edited field", saved.settings.OpenAIAPIKey)
	}
	if saved.settings.BossChatBackend != config.AIBackendOpenAIAPI {
		t.Fatalf("saved boss backend = %s, want openai_api", saved.settings.BossChatBackend)
	}
}

func TestRenderSetupHintExplainsClaudeHaikuDefault(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.AIBackend = config.AIBackendClaude

	m := Model{
		settingsBaseline: &settings,
		setupSelected:    mSetupSelectionForTest(config.AIBackendClaude),
		setupSnapshot: aibackend.Snapshot{
			Selected: config.AIBackendClaude,
			Claude: aibackend.Status{
				Backend:       config.AIBackendClaude,
				Label:         "Claude Code",
				Installed:     true,
				Authenticated: true,
				Ready:         true,
				Detail:        "Claude Code ready via claude.ai (max)",
			},
		},
	}

	hint := ansi.Strip(m.renderSetupHint(96))
	if !strings.Contains(hint, "Haiku") {
		t.Fatalf("renderSetupHint() = %q, want Haiku guidance", hint)
	}
}

func TestRenderSetupHintExplainsLocalModelPicker(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.AIBackend = config.AIBackendMLX

	m := Model{
		settingsBaseline: &settings,
		setupSelected:    mSetupSelectionForTest(config.AIBackendMLX),
		setupSnapshot: aibackend.Snapshot{
			Selected: config.AIBackendMLX,
			MLX: aibackend.Status{
				Backend:     config.AIBackendMLX,
				Label:       "MLX",
				Ready:       true,
				Endpoint:    "http://127.0.0.1:8080/v1",
				Models:      []string{"mlx-community/Qwen3.5-9B-MLX-4bit"},
				ActiveModel: "mlx-community/Qwen3.5-9B-MLX-4bit",
			},
		},
	}

	hint := ansi.Strip(m.renderSetupHint(120))
	if !strings.Contains(hint, "Press m to pin a discovered") || !strings.Contains(hint, "model or e to edit endpoint") {
		t.Fatalf("renderSetupHint() = %q, want local model picker guidance", hint)
	}
	if !strings.Contains(hint, "Qwen3.5-9B-MLX-4bit") {
		t.Fatalf("renderSetupHint() = %q, want current discovered model", hint)
	}
}

func TestSetupLocalModelPickerUpdatesBaseline(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.AIBackend = config.AIBackendMLX

	m := Model{
		setupMode:        true,
		settingsBaseline: &settings,
		setupSelected:    mSetupSelectionForTest(config.AIBackendMLX),
		setupSnapshot: aibackend.Snapshot{
			Selected: config.AIBackendMLX,
			MLX: aibackend.Status{
				Backend:     config.AIBackendMLX,
				Label:       "MLX",
				Ready:       true,
				Endpoint:    "http://127.0.0.1:8080/v1",
				Models:      []string{"mlx-community/Qwen3.5-9B-MLX-4bit", "mlx-community/Qwen3.5-4B-MLX-4bit"},
				ActiveModel: "mlx-community/Qwen3.5-9B-MLX-4bit",
			},
		},
	}

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("m")})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("opening the local model picker should not return a command")
	}
	if !got.localModelPickerVisible {
		t.Fatalf("local model picker should be visible after pressing m in setup")
	}

	updated, _ = got.Update(tea.KeyMsg{Type: tea.KeyDown})
	got = updated.(Model)
	updated, _ = got.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if got.localModelPickerVisible {
		t.Fatalf("local model picker should close after choosing a model")
	}
	if got.currentSettingsBaseline().MLXModel != "mlx-community/Qwen3.5-9B-MLX-4bit" {
		t.Fatalf("saved local model = %q, want first discovered model after selecting row 1", got.currentSettingsBaseline().MLXModel)
	}
}

func mSetupSelectionForTest(backend config.AIBackend) int {
	for i, option := range setupBackendOptions {
		if option == backend {
			return i
		}
	}
	return 0
}

func mSetupBossSelectionForTest(backend config.AIBackend) int {
	for i, option := range setupBossChatOptions {
		if option == backend {
			return i
		}
	}
	return 0
}

func queueTodoWorktreeSuggestionForTest(t *testing.T, ctx context.Context, st *store.Store, todoID int64) {
	t.Helper()
	queued, err := st.QueueTodoWorktreeSuggestion(ctx, todoID)
	if err != nil {
		t.Fatalf("queue todo worktree suggestion: %v", err)
	}
	if queued {
		return
	}
	suggestion, err := st.GetTodoWorktreeSuggestion(ctx, todoID)
	if err != nil {
		t.Fatalf("get existing todo worktree suggestion: %v", err)
	}
	if suggestion.Status != model.TodoWorktreeSuggestionQueued {
		t.Fatalf("existing todo worktree suggestion status = %q, want %q", suggestion.Status, model.TodoWorktreeSuggestionQueued)
	}
}

func TestSettingsBossChatBackendPickerUpdatesField(t *testing.T) {
	m := Model{
		settingsMode:   true,
		settingsFields: newSettingsFields(config.EditableSettingsFromAppConfig(config.Default())),
		width:          100,
		height:         24,
	}
	_ = m.setSettingsSelection(settingsFieldBossChatBackend)

	updated, cmd := m.updateSettingsMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("opening boss chat picker should not queue a command")
	}
	if !got.settingsBossChatPickerVisible {
		t.Fatalf("boss chat picker should open")
	}

	updated, _ = got.updateSettingsBossChatBackendPickerMode(tea.KeyMsg{Type: tea.KeyDown})
	got = updated.(Model)
	updated, _ = got.updateSettingsBossChatBackendPickerMode(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if got.settingsBossChatPickerVisible {
		t.Fatalf("boss chat picker should close after choosing")
	}
	if got.settingsFieldValue(settingsFieldBossChatBackend) != string(config.AIBackendOpenAIAPI) {
		t.Fatalf("boss chat backend field = %q, want openai_api", got.settingsFieldValue(settingsFieldBossChatBackend))
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

func TestOpenRunCommandDialogLoadsSuggestionAsync(t *testing.T) {
	t.Parallel()

	projectPath := t.TempDir()
	if err := os.MkdirAll(filepath.Join(projectPath, "bin"), 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectPath, "bin", "dev"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write bin/dev: %v", err)
	}

	m := Model{}
	cmd := m.openRunCommandDialog(model.ProjectSummary{
		Name:          "demo",
		Path:          projectPath,
		PresentOnDisk: true,
	}, true)
	if m.runCommandDialog == nil {
		t.Fatal("openRunCommandDialog() should open the dialog")
	}
	if !m.runCommandDialog.SuggestionPending {
		t.Fatal("openRunCommandDialog() should mark suggestion loading pending when the command is empty")
	}

	for _, msg := range collectCmdMsgs(cmd) {
		updated, _ := m.Update(msg)
		m = updated.(Model)
	}

	if got := m.runCommandDialog.Input.Value(); got != "./bin/dev" {
		t.Fatalf("suggested command = %q, want %q", got, "./bin/dev")
	}
	if got := m.runCommandDialog.SuggestionReason; got != "Found bin/dev in the project root." {
		t.Fatalf("suggestion reason = %q, want bin/dev hint", got)
	}
	if m.runCommandDialog.SuggestionPending {
		t.Fatal("suggestion pending should clear after the async lookup returns")
	}
}

func TestRunCommandSuggestionDoesNotOverwriteTypedInput(t *testing.T) {
	t.Parallel()

	projectPath := t.TempDir()
	if err := os.MkdirAll(filepath.Join(projectPath, "bin"), 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectPath, "bin", "dev"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write bin/dev: %v", err)
	}

	m := Model{}
	cmd := m.openRunCommandDialog(model.ProjectSummary{
		Name:          "demo",
		Path:          projectPath,
		PresentOnDisk: true,
	}, true)
	if m.runCommandDialog == nil {
		t.Fatal("openRunCommandDialog() should open the dialog")
	}
	m.runCommandDialog.Input.SetValue("npm run local")

	for _, msg := range collectCmdMsgs(cmd) {
		updated, _ := m.Update(msg)
		m = updated.(Model)
	}

	if got := m.runCommandDialog.Input.Value(); got != "npm run local" {
		t.Fatalf("typed command = %q, want %q", got, "npm run local")
	}
	if got := m.runCommandDialog.SuggestionReason; got != "" {
		t.Fatalf("suggestion reason = %q, want empty when the user has already typed a command", got)
	}
	if m.runCommandDialog.SuggestionPending {
		t.Fatal("suggestion pending should clear even when the suggestion is ignored")
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

func TestCommandPaletteShowsWorktreeCommandHintForLinkedWorktree(t *testing.T) {
	input := textinput.New()
	input.SetValue("/")

	rootPath := "/tmp/repo"
	childPath := "/tmp/repo--feat-parallel-lane"
	m := Model{
		allProjects: []model.ProjectSummary{
			{
				Name:             "repo",
				Path:             rootPath,
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindMain,
				RepoBranch:       "master",
			},
			{
				Name:                 "repo--feat-parallel-lane",
				Path:                 childPath,
				PresentOnDisk:        true,
				WorktreeRootPath:     rootPath,
				WorktreeKind:         model.WorktreeKindLinked,
				WorktreeParentBranch: "master",
				RepoBranch:           "feat/parallel-lane",
				RepoDirty:            true,
			},
		},
		visibility:   visibilityAllFolders,
		sortMode:     sortByAttention,
		commandMode:  true,
		commandInput: input,
		width:        100,
		height:       24,
	}
	m.rebuildProjectList(childPath)
	m.syncCommandSelection()

	rendered := ansi.Strip(m.renderCommandPaletteContent(72))
	if !strings.Contains(rendered, "Worktrees: try /wt lanes, /wt merge, /wt remove.") {
		t.Fatalf("command palette should hint the worktree slash commands, got %q", rendered)
	}
}

func TestRenderCommandPaletteContentForLinkedWorktreeSkipsPruneHint(t *testing.T) {
	input := textinput.New()
	input.SetValue("/")

	rootPath := "/tmp/repo"
	childPath := "/tmp/repo--feat-parallel-lane"
	m := Model{
		allProjects: []model.ProjectSummary{
			{
				Name:             "repo",
				Path:             rootPath,
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindMain,
				RepoBranch:       "master",
			},
			{
				Name:                 "repo--feat-parallel-lane",
				Path:                 childPath,
				PresentOnDisk:        true,
				WorktreeRootPath:     rootPath,
				WorktreeKind:         model.WorktreeKindLinked,
				WorktreeParentBranch: "master",
				RepoBranch:           "feat/parallel-lane",
			},
		},
		visibility:   visibilityAllFolders,
		sortMode:     sortByAttention,
		commandMode:  true,
		commandInput: input,
		width:        100,
		height:       24,
	}
	m.rebuildProjectList(childPath)
	m.syncCommandSelection()

	rendered := ansi.Strip(m.renderCommandPaletteContent(72))
	if strings.Contains(strings.ToLower(rendered), "prune") {
		t.Fatalf("command palette should not include prune for linked worktree selection, got %q", rendered)
	}
}

func TestCommandPaletteShowsWorktreePruneCommandForRepoRoot(t *testing.T) {
	input := textinput.New()
	input.SetValue("/")

	rootPath := "/tmp/repo"
	m := Model{
		allProjects: []model.ProjectSummary{
			{
				Name:             "repo",
				Path:             rootPath,
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindMain,
				RepoBranch:       "master",
			},
			{
				Name:                 "repo--feat-parallel-lane",
				Path:                 "/tmp/repo--feat-parallel-lane",
				PresentOnDisk:        true,
				WorktreeRootPath:     rootPath,
				WorktreeKind:         model.WorktreeKindLinked,
				WorktreeParentBranch: "master",
				RepoBranch:           "feat/parallel-lane",
			},
		},
		visibility:   visibilityAllFolders,
		sortMode:     sortByAttention,
		commandMode:  true,
		commandInput: input,
		width:        100,
		height:       24,
	}
	m.rebuildProjectList(rootPath)
	m.syncCommandSelection()

	rendered := ansi.Strip(m.renderCommandPaletteContent(72))
	if !strings.Contains(rendered, "Worktrees: try /wt lanes, /wt prune.") {
		t.Fatalf("command palette should include prune for repo-root selection, got %q", rendered)
	}
}

func TestRenderDialogPanelRestoresBackgroundAfterStyledResets(t *testing.T) {
	prevProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	defer lipgloss.SetColorProfile(prevProfile)

	panel := renderDialogPanel(30, 26, detailSectionStyle.Render("TODO")+"  "+detailValueStyle.Render("demo"))
	if !strings.Contains(panel, dialogPanelFillReset) {
		t.Fatalf("dialog panel should reapply its background color after nested style resets: %q", panel)
	}
	if !strings.Contains(ansi.Strip(panel), "TODO  demo") {
		t.Fatalf("dialog panel should preserve the rendered content, got %q", ansi.Strip(panel))
	}
}

func TestTodoDialogLegendUsesDistinctActionTones(t *testing.T) {
	prevProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	defer lipgloss.SetColorProfile(prevProfile)

	rendered := todoDialogLegendLine()
	for _, bgCode := range []string{"48;5;42", "48;5;81", "48;5;214", "48;5;160"} {
		if !strings.Contains(rendered, bgCode) {
			t.Fatalf("todo dialog legend should include tone %s, got %q", bgCode, rendered)
		}
	}
	stripped := ansi.Strip(rendered)
	if !strings.Contains(stripped, "\n") {
		t.Fatalf("todo dialog legend should split Enter/Esc onto a second line, got %q", stripped)
	}
	lines := strings.Split(stripped, "\n")
	if len(lines) != 2 {
		t.Fatalf("todo dialog legend line count = %d, want 2; got %q", len(lines), stripped)
	}
	if (strings.Contains(lines[0], "Enter") && strings.Contains(lines[0], "start")) || (strings.Contains(lines[0], "Esc") && strings.Contains(lines[0], "close")) {
		t.Fatalf("todo dialog legend first line should keep Enter/Esc separate, got %q", lines[0])
	}
	if !strings.Contains(lines[1], "Enter") || !strings.Contains(lines[1], "start") || !strings.Contains(lines[1], "Esc") || !strings.Contains(lines[1], "close") {
		t.Fatalf("todo dialog legend second line should contain Enter/Esc, got %q", lines[1])
	}
}

func TestLocalBackendModelPickerLegendUsesDistinctActionTones(t *testing.T) {
	prevProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	defer lipgloss.SetColorProfile(prevProfile)

	m := Model{
		localModelPickerBackend: config.AIBackendMLX,
		setupSnapshot: aibackend.Snapshot{
			MLX: aibackend.Status{
				Backend:     config.AIBackendMLX,
				Label:       "MLX",
				Ready:       true,
				Endpoint:    "http://127.0.0.1:8080/v1",
				Models:      []string{"mlx-community/Qwen3.5-9B-MLX-4bit"},
				ActiveModel: "mlx-community/Qwen3.5-9B-MLX-4bit",
			},
		},
	}

	rendered := m.renderLocalBackendModelPickerContent(80, 16)
	for _, bgCode := range []string{"48;5;42", "48;5;81", "48;5;214", "48;5;160"} {
		if !strings.Contains(rendered, bgCode) {
			t.Fatalf("local backend model picker legend should include tone %s, got %q", bgCode, rendered)
		}
	}

	stripped := ansi.Strip(rendered)
	if !strings.Contains(stripped, "Up/Down") || !strings.Contains(stripped, "move") {
		t.Fatalf("local backend model picker legend should include navigation guidance, got %q", stripped)
	}
	if !strings.Contains(stripped, "Enter") || !strings.Contains(stripped, "choose") {
		t.Fatalf("local backend model picker legend should include choose guidance, got %q", stripped)
	}
	if !strings.Contains(stripped, "a") || !strings.Contains(stripped, "auto") {
		t.Fatalf("local backend model picker legend should include auto guidance, got %q", stripped)
	}
	if !strings.Contains(stripped, "Esc") || !strings.Contains(stripped, "close") {
		t.Fatalf("local backend model picker legend should include close guidance, got %q", stripped)
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
	if !strings.Contains(rendered, "Page Up/Page Down") || !strings.Contains(rendered, "changes section") {
		t.Fatalf("View() should keep the settings section legend visible at 24 rows: %q", rendered)
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
	if !strings.Contains(rendered, "Page Up/Page Down") || !strings.Contains(rendered, "changes section") {
		t.Fatalf("settings modal should clearly render Page Up/Page Down changes section action: %q", rendered)
	}
	if !strings.Contains(rendered, "Up/Down") || !strings.Contains(rendered, "move") {
		t.Fatalf("settings modal should render Up/Down move action: %q", rendered)
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

func TestInferenceStatusCardsShowProjectAndBossChatSelections(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.AIBackend = config.AIBackendOpenCode
	settings.BossChatBackend = config.AIBackendOpenAIAPI
	settings.OpenAIAPIKey = "sk-test-example"

	m := Model{
		settingsBaseline: &settings,
		setupSnapshot: aibackend.Snapshot{
			OpenCode: aibackend.Status{
				Backend: config.AIBackendOpenCode,
				Label:   config.AIBackendOpenCode.Label(),
				Ready:   true,
				Detail:  "OpenCode ready.",
			},
			OpenAIAPI: aibackend.Status{
				Backend: config.AIBackendOpenAIAPI,
				Label:   config.AIBackendOpenAIAPI.Label(),
				Ready:   true,
				Detail:  "Saved OpenAI API key ready.",
			},
		},
	}

	rendered := ansi.Strip(m.renderInferenceStatusCards(140))
	for _, want := range []string{
		"Project reports",
		"OpenCode",
		"Boss chat",
		"OpenAI API key",
		"project reports stay separate",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("inference cards missing %q: %q", want, rendered)
		}
	}
}

func TestInferenceStatusCardsTreatMissingSnapshotAsSelected(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.AIBackend = config.AIBackendOpenCode
	settings.BossChatBackend = config.AIBackendDisabled

	m := Model{
		settingsBaseline: &settings,
	}

	rendered := ansi.Strip(m.renderInferenceStatusCards(140))
	if !strings.Contains(rendered, "SELECTED") {
		t.Fatalf("inference cards should show stale unknown availability as selected: %q", rendered)
	}
	if strings.Contains(rendered, "INSTALL") {
		t.Fatalf("inference cards should not invent an install warning from an empty snapshot: %q", rendered)
	}
	if !strings.Contains(rendered, "Run /setup to refresh availability") {
		t.Fatalf("inference cards should explain how to refresh stale availability: %q", rendered)
	}
}

func TestSettingsModalShowsSelectedHintAndWindowsLowerFields(t *testing.T) {
	m := Model{
		settingsMode:   true,
		settingsFields: newSettingsFields(config.EditableSettingsFromAppConfig(config.Default())),
		width:          100,
		height:         24,
	}
	_ = m.setSettingsSelection(settingsFieldHideReasoningSections)

	rendered := ansi.Strip(m.renderSettingsContent(72, 12))
	if !strings.Contains(rendered, "Show reasoning") {
		t.Fatalf("settings modal should keep the selected lower field visible: %q", rendered)
	}
	if strings.Contains(rendered, "OpenAI API key    ") {
		t.Fatalf("settings modal should window away upper fields in a short modal: %q", rendered)
	}
	if !strings.Contains(rendered, "Accepted values: true, false.") || !strings.Contains(rendered, "reasoning/thinking sections in the embedded transcript. Default: false.") {
		t.Fatalf("settings modal should show the selected field hint: %q", rendered)
	}
	if strings.Contains(rendered, "Used by OpenAI API backed features") {
		t.Fatalf("settings modal should not render every field hint inline anymore: %q", rendered)
	}
	if !strings.Contains(rendered, "↑ ") {
		t.Fatalf("settings modal should show an above-window indicator when earlier fields are hidden: %q", rendered)
	}
}

func TestSettingsSectionSwitchChangesVisibleFields(t *testing.T) {
	m := Model{
		settingsMode:   true,
		settingsFields: newSettingsFields(config.EditableSettingsFromAppConfig(config.Default())),
		width:          100,
		height:         24,
	}
	_ = m.setSettingsSelection(settingsFieldOpenAIAPIKey)

	updated, cmd := m.updateSettingsMode(tea.KeyMsg{Type: tea.KeyPgDown})
	if cmd == nil {
		t.Fatalf("PgDn should move to the next settings section")
	}
	got := updated.(Model)
	if got.settingsSelected != settingsFieldIncludePaths {
		t.Fatalf("settingsSelected = %d, want include paths field", got.settingsSelected)
	}

	rendered := ansi.Strip(got.renderSettingsContent(72, 18))
	if !strings.Contains(rendered, "Sections:") || !strings.Contains(rendered, "Project Scope") {
		t.Fatalf("settings modal should make the section switcher obvious: %q", rendered)
	}
	if !strings.Contains(rendered, "> Project Scope") {
		t.Fatalf("settings modal should render a dedicated selected section row: %q", rendered)
	}
	foundColumnRow := false
	for _, line := range strings.Split(rendered, "\n") {
		if strings.Contains(line, "Sections:") && strings.Contains(line, "Project Scope section.") {
			foundColumnRow = true
			break
		}
	}
	if !foundColumnRow {
		t.Fatalf("settings modal should place sections in a left column beside section content: %q", rendered)
	}
	if !strings.Contains(rendered, "Project Scope section.") {
		t.Fatalf("settings modal should render the new section hint: %q", rendered)
	}
	if !strings.Contains(rendered, "Include paths") {
		t.Fatalf("settings modal should show scope fields after switching sections: %q", rendered)
	}
	if strings.Contains(rendered, "OpenAI API key    ") {
		t.Fatalf("settings modal should not keep rendering the old section fields: %q", rendered)
	}
}

func TestSettingsLeftRightStayWithFocusedInput(t *testing.T) {
	m := Model{
		settingsMode:   true,
		settingsFields: newSettingsFields(config.EditableSettingsFromAppConfig(config.Default())),
		width:          100,
		height:         24,
	}
	_ = m.setSettingsSelection(settingsFieldMLXModel)
	m.settingsFields[settingsFieldMLXModel].input.SetValue("abcdef")
	m.settingsFields[settingsFieldMLXModel].input.CursorEnd()

	beforeSection := m.activeSettingsSectionIndex()
	beforePos := m.settingsFields[settingsFieldMLXModel].input.Position()

	updated, _ := m.updateSettingsMode(tea.KeyMsg{Type: tea.KeyLeft})
	got := updated.(Model)
	if got.activeSettingsSectionIndex() != beforeSection {
		t.Fatalf("left arrow should not switch sections")
	}
	if got.settingsSelected != settingsFieldMLXModel {
		t.Fatalf("left arrow should keep the same focused field")
	}
	leftPos := got.settingsFields[settingsFieldMLXModel].input.Position()
	if leftPos != beforePos-1 {
		t.Fatalf("left arrow cursor position = %d, want %d", leftPos, beforePos-1)
	}

	updated, _ = got.updateSettingsMode(tea.KeyMsg{Type: tea.KeyRight})
	got = updated.(Model)
	if got.activeSettingsSectionIndex() != beforeSection {
		t.Fatalf("right arrow should not switch sections")
	}
	rightPos := got.settingsFields[settingsFieldMLXModel].input.Position()
	if rightPos != beforePos {
		t.Fatalf("right arrow cursor position = %d, want %d", rightPos, beforePos)
	}
}

func TestSettingsBrowserSectionShowsStatusSummary(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.PlaywrightPolicy = browserctl.Policy{
		ManagementMode:     browserctl.ManagementModeManaged,
		DefaultBrowserMode: browserctl.BrowserModeHeadless,
		LoginMode:          browserctl.LoginModePromote,
		IsolationScope:     browserctl.IsolationScopeTask,
	}

	m := Model{
		settingsMode:     true,
		settingsFields:   newSettingsFields(settings),
		settingsBaseline: &settings,
		width:            100,
		height:           24,
	}
	_ = m.setSettingsSelection(settingsFieldBrowserAutomation)

	rendered := ansi.Strip(m.renderSettingsContent(84, 22))
	for _, want := range []string{
		"Effective:",
		"Ownership:",
		"Live activity:",
		"Provider support:",
		"Codex:",
		"OpenCode:",
		"Claude Code:",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("browser settings status is missing %q: %q", want, rendered)
		}
	}
}

func TestSettingsBrowserAutomationEnterOpensPicker(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())

	m := Model{
		settingsMode:     true,
		settingsFields:   newSettingsFields(settings),
		settingsBaseline: &settings,
		width:            100,
		height:           24,
	}
	_ = m.setSettingsSelection(settingsFieldBrowserAutomation)

	updated, cmd := m.updateSettingsMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("browser automation enter should not save immediately")
	}
	if !got.settingsBrowserPickerVisible {
		t.Fatalf("browser automation enter should open the chooser")
	}
	if got.settingsSaving {
		t.Fatalf("browser automation enter should not start saving")
	}
	if got.status != "Choose when Little Control Room should show browser windows." {
		t.Fatalf("status = %q, want chooser status", got.status)
	}
}

func TestSettingsBrowserAutomationPickerHighlightsSelectionAndCurrentMode(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())

	m := Model{
		settingsMode:     true,
		settingsFields:   newSettingsFields(settings),
		settingsBaseline: &settings,
		width:            100,
		height:           24,
	}
	_ = m.setSettingsSelection(settingsFieldBrowserAutomation)

	updated, _ := m.updateSettingsMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	updated, _ = got.updateSettingsBrowserAutomationPickerMode(tea.KeyMsg{Type: tea.KeyDown})
	got = updated.(Model)

	rendered := ansi.Strip(got.renderSettingsBrowserAutomationPickerContent(56, 18))
	for _, want := range []string{
		"Only when needed  (current)",
		"› Always show",
		"About",
		"Selected: Always show",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("browser automation picker is missing %q: %q", want, rendered)
		}
	}
}

func TestSettingsBrowserAutomationFieldRendersChooserHint(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())

	m := Model{
		settingsMode:     true,
		settingsFields:   newSettingsFields(settings),
		settingsBaseline: &settings,
		width:            100,
		height:           24,
	}
	_ = m.setSettingsSelection(settingsFieldBrowserAutomation)

	rendered := ansi.Strip(m.renderSettingsContent(84, 22))
	for _, want := range []string{
		"Only when needed",
		"Enter to choose",
		"Ctrl+S",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("browser settings field is missing %q: %q", want, rendered)
		}
	}
}

func TestSettingsBrowserSectionShowsLiveBrowserActivity(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.PlaywrightPolicy = browserctl.Policy{
		ManagementMode:     browserctl.ManagementModeManaged,
		DefaultBrowserMode: browserctl.BrowserModeHeadless,
		LoginMode:          browserctl.LoginModePromote,
		IsolationScope:     browserctl.IsolationScopeTask,
	}

	m := Model{
		settingsMode:     true,
		settingsFields:   newSettingsFields(settings),
		settingsBaseline: &settings,
		codexSnapshots: map[string]codexapp.Snapshot{
			"/tmp/demo": {
				Provider: codexapp.ProviderCodex,
				BrowserActivity: browserctl.SessionActivity{
					Policy:     settings.PlaywrightPolicy,
					State:      browserctl.SessionActivityStateWaitingForUser,
					ServerName: "playwright",
					ToolName:   "browser_navigate",
				},
			},
		},
		width:  100,
		height: 24,
	}
	_ = m.setSettingsSelection(settingsFieldBrowserAutomation)

	rendered := ansi.Strip(m.renderSettingsContent(84, 22))
	for _, want := range []string{
		"Codex / demo:",
		"playwright/browser_navigate is waiting for user",
		"input.",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("browser settings live activity is missing %q: %q", want, rendered)
		}
	}
}

func TestSettingsBrowserSectionShowsInteractiveLeaseOwner(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	controller := browserctl.NewController()
	ownerObservation := browserctl.Observation{
		Ref: browserctl.SessionRef{
			Provider:    "codex",
			ProjectPath: "/tmp/owner-demo",
			SessionID:   "thread-owner",
		},
		Policy:   settingsAutomaticPlaywrightPolicy,
		Activity: browserctl.SessionActivity{Policy: settingsAutomaticPlaywrightPolicy, State: browserctl.SessionActivityStateWaitingForUser, ServerName: "playwright", ToolName: "browser_navigate"},
		LoginURL: "https://example.test/owner",
	}
	waitingObservation := browserctl.Observation{
		Ref: browserctl.SessionRef{
			Provider:    "codex",
			ProjectPath: "/tmp/waiting-demo",
			SessionID:   "thread-waiting",
		},
		Policy:   settingsAutomaticPlaywrightPolicy,
		Activity: browserctl.SessionActivity{Policy: settingsAutomaticPlaywrightPolicy, State: browserctl.SessionActivityStateWaitingForUser, ServerName: "playwright", ToolName: "browser_navigate"},
		LoginURL: "https://example.test/waiting",
	}
	controller.Observe(ownerObservation)
	controller.Observe(waitingObservation)
	snapshot := controller.AcquireInteractive(ownerObservation.Ref).Snapshot

	m := Model{
		settingsMode:         true,
		settingsFields:       newSettingsFields(settings),
		settingsBaseline:     &settings,
		browserController:    controller,
		browserLeaseSnapshot: snapshot,
		width:                100,
		height:               24,
	}
	_ = m.setSettingsSelection(settingsFieldBrowserAutomation)

	rendered := ansi.Strip(m.renderSettingsContent(84, 22))
	for _, want := range []string{
		"Interactive browser reserved by Codex / owner-demo",
		"1 managed login flow(s) waiting",
		"Codex / waiting-demo is waiting to open a browser login flow.",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("browser settings lease status is missing %q: %q", want, rendered)
		}
	}
}

func TestBrowserAttentionOverlayRendersAndSkipsQuestionNotify(t *testing.T) {
	m := Model{
		browserAttention: &browserAttentionNotification{
			ProjectPath: "/tmp/demo",
			ProjectName: "demo",
			SessionID:   "thread-demo",
			Provider:    codexapp.ProviderCodex,
			Activity: browserctl.SessionActivity{
				Policy:     settingsAutomaticPlaywrightPolicy,
				State:      browserctl.SessionActivityStateWaitingForUser,
				ServerName: "playwright",
				ToolName:   "browser_navigate",
			},
		},
	}

	rendered := ansi.Strip(m.renderBrowserAttentionContent(72))
	for _, want := range []string{
		"Browser needs attention",
		"demo",
		"playwright/browser_navigate",
		"open session",
		"browser settings",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("browser attention overlay is missing %q: %q", want, rendered)
		}
	}

	waitingSnapshot := codexapp.Snapshot{
		Provider: codexapp.ProviderCodex,
		BrowserActivity: browserctl.SessionActivity{
			Policy:     settingsAutomaticPlaywrightPolicy,
			State:      browserctl.SessionActivityStateWaitingForUser,
			ServerName: "playwright",
		},
		PendingElicitation: &codexapp.ElicitationRequest{ID: "elicitation_1"},
	}
	m.detectQuestionNotification("/tmp/demo", waitingSnapshot)
	if m.questionNotify != nil {
		t.Fatalf("question notification should stay nil for browser attention waits")
	}
}

func TestBrowserAttentionOverlayShowsOpenBrowserForManagedLoginURL(t *testing.T) {
	m := Model{
		browserAttention: &browserAttentionNotification{
			ProjectPath: "/tmp/demo",
			ProjectName: "demo",
			SessionID:   "thread-demo",
			Provider:    codexapp.ProviderCodex,
			Activity: browserctl.SessionActivity{
				Policy:     settingsAutomaticPlaywrightPolicy,
				State:      browserctl.SessionActivityStateWaitingForUser,
				ServerName: "playwright",
				ToolName:   "browser_navigate",
			},
			ManagedBrowserSessionKey: "managed-demo",
			OpenURL:                  "https://example.test/login",
		},
	}

	rendered := ansi.Strip(m.renderBrowserAttentionContent(72))
	for _, want := range []string{
		"show browser",
		"open session",
		"Little Control Room can reveal the managed browser window",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("browser attention overlay is missing %q: %q", want, rendered)
		}
	}
}

func TestBrowserAttentionEnterOpensSession(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	m := Model{
		settingsBaseline: &settings,
		browserAttention: &browserAttentionNotification{
			ProjectPath: "/tmp/demo",
			ProjectName: "demo",
			SessionID:   "thread-demo",
			Provider:    codexapp.ProviderCodex,
			Activity: browserctl.SessionActivity{
				Policy:     settingsAutomaticPlaywrightPolicy,
				State:      browserctl.SessionActivityStateWaitingForUser,
				ServerName: "playwright",
			},
		},
	}

	updated, cmd := m.updateBrowserAttentionMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("browser attention Enter should queue follow-up commands")
	}
	if got.browserAttention != nil {
		t.Fatalf("browser attention should clear after opening the session")
	}
	if got.codexVisibleProject != "/tmp/demo" {
		t.Fatalf("codexVisibleProject = %q, want /tmp/demo", got.codexVisibleProject)
	}
	if got.status != "Codex browser needs your attention" {
		t.Fatalf("status = %q, want browser attention status", got.status)
	}
}

func TestBrowserAttentionEnterOpensBrowserAndSessionForManagedLoginURL(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())

	m := Model{
		settingsBaseline: &settings,
		browserAttention: &browserAttentionNotification{
			ProjectPath: "/tmp/demo",
			ProjectName: "demo",
			SessionID:   "thread-demo",
			Provider:    codexapp.ProviderCodex,
			Activity: browserctl.SessionActivity{
				Policy:     settingsAutomaticPlaywrightPolicy,
				State:      browserctl.SessionActivityStateWaitingForUser,
				ServerName: "playwright",
			},
			ManagedBrowserSessionKey: "managed-demo",
			OpenURL:                  "https://example.test/login",
		},
	}

	previousSessionRevealer := managedBrowserSessionRevealer
	defer func() {
		managedBrowserSessionRevealer = previousSessionRevealer
	}()

	revealedSessionKey := ""
	managedBrowserSessionRevealer = func(_ string, sessionKey string) (browserctl.ManagedPlaywrightState, error) {
		revealedSessionKey = sessionKey
		return browserctl.ManagedPlaywrightState{SessionKey: sessionKey, BrowserPID: 123, RevealSupported: true}, nil
	}

	updated, cmd := m.updateBrowserAttentionMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("browser attention Enter should queue login follow-up commands")
	}
	if got.browserAttention != nil {
		t.Fatalf("browser attention should clear after opening the browser flow")
	}
	if got.codexVisibleProject != "/tmp/demo" {
		t.Fatalf("codexVisibleProject = %q, want /tmp/demo", got.codexVisibleProject)
	}
	if got.status != "Showing the managed browser window and switching to the embedded session..." {
		t.Fatalf("status = %q, want browser-reveal status", got.status)
	}

	msgs := collectCmdMsgs(cmd)
	var openMsg browserOpenMsg
	foundOpenMsg := false
	for _, msg := range msgs {
		if candidate, ok := msg.(browserOpenMsg); ok {
			openMsg = candidate
			foundOpenMsg = true
			break
		}
	}
	if !foundOpenMsg {
		t.Fatalf("expected browserOpenMsg in batched login commands, got %#v", msgs)
	}
	if openMsg.err != nil {
		t.Fatalf("browserOpenMsg.err = %v, want nil", openMsg.err)
	}
	if openMsg.status != "Managed browser window is ready. Finish the browser flow there, then return to the embedded session if more input is needed." {
		t.Fatalf("browserOpenMsg.status = %q, want browser reveal success status", openMsg.status)
	}
	if revealedSessionKey != "managed-demo" {
		t.Fatalf("revealed session key = %q, want managed-demo", revealedSessionKey)
	}
}

func TestBrowserAttentionEnterShowsBlockedStatusWhenLeaseOwnedElsewhere(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	controller := browserctl.NewController()
	ownerObservation := browserctl.Observation{
		Ref: browserctl.SessionRef{
			Provider:    "codex",
			ProjectPath: "/tmp/owner-demo",
			SessionID:   "thread-owner",
		},
		Policy:   settingsAutomaticPlaywrightPolicy,
		Activity: browserctl.SessionActivity{Policy: settingsAutomaticPlaywrightPolicy, State: browserctl.SessionActivityStateWaitingForUser, ServerName: "playwright", ToolName: "browser_navigate"},
		LoginURL: "https://example.test/owner",
	}
	controller.Observe(ownerObservation)
	controller.AcquireInteractive(ownerObservation.Ref)

	m := Model{
		settingsBaseline:     &settings,
		browserController:    controller,
		browserLeaseSnapshot: controller.Snapshot(),
		browserAttention: &browserAttentionNotification{
			ProjectPath: "/tmp/demo",
			ProjectName: "demo",
			SessionID:   "thread-demo",
			Provider:    codexapp.ProviderCodex,
			Activity: browserctl.SessionActivity{
				Policy:     settingsAutomaticPlaywrightPolicy,
				State:      browserctl.SessionActivityStateWaitingForUser,
				ServerName: "playwright",
			},
			ManagedBrowserSessionKey: "managed-demo",
			OpenURL:                  "https://example.test/login",
		},
	}

	updated, cmd := m.updateBrowserAttentionMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("blocked browser attention should still reveal the embedded session")
	}
	if got.codexVisibleProject != "/tmp/demo" {
		t.Fatalf("codexVisibleProject = %q, want /tmp/demo", got.codexVisibleProject)
	}
	if !strings.Contains(got.status, "Interactive browser is already reserved by Codex / owner-demo") {
		t.Fatalf("status = %q, want blocked browser ownership status", got.status)
	}
}

func TestOpenManagedBrowserLoginReleasesLeaseOnBrowserOpenFailure(t *testing.T) {
	controller := browserctl.NewController()
	observation := browserctl.Observation{
		Ref: browserctl.SessionRef{
			Provider:    "codex",
			ProjectPath: "/tmp/demo",
			SessionID:   "thread-demo",
		},
		Policy:   settingsAutomaticPlaywrightPolicy,
		Activity: browserctl.SessionActivity{Policy: settingsAutomaticPlaywrightPolicy, State: browserctl.SessionActivityStateWaitingForUser, ServerName: "playwright", ToolName: "browser_navigate"},
		LoginURL: "https://example.test/login",
	}
	controller.Observe(observation)

	previousSessionRevealer := managedBrowserSessionRevealer
	defer func() {
		managedBrowserSessionRevealer = previousSessionRevealer
	}()
	managedBrowserSessionRevealer = func(_ string, sessionKey string) (browserctl.ManagedPlaywrightState, error) {
		return browserctl.ManagedPlaywrightState{}, errors.New("boom")
	}

	m := Model{
		browserController:    controller,
		browserLeaseSnapshot: controller.Snapshot(),
	}

	updated, cmd := m.openManagedBrowserLogin(
		"/tmp/demo",
		codexapp.ProviderCodex,
		"thread-demo",
		"managed-demo",
		browserctl.SessionActivity{
			Policy:     settingsAutomaticPlaywrightPolicy,
			State:      browserctl.SessionActivityStateWaitingForUser,
			ServerName: "playwright",
			ToolName:   "browser_navigate",
		},
		"https://example.test/login",
		"Showing the managed browser window...",
		"Managed browser window is ready.",
	)
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("openManagedBrowserLogin should queue a browser-open command")
	}
	if got.browserLeaseSnapshot.Interactive == nil {
		t.Fatalf("interactive lease should be held while browser open is pending")
	}

	msg := cmd()
	openMsg, ok := msg.(browserOpenMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want browserOpenMsg", msg)
	}
	if openMsg.err == nil {
		t.Fatalf("browserOpenMsg.err = nil, want open failure")
	}
	if !openMsg.browserLeaseSnapshotSet {
		t.Fatalf("browser open failure should return an updated lease snapshot")
	}
	if openMsg.browserLeaseSnapshot.Interactive != nil {
		t.Fatalf("interactive lease should be released after open failure, got %#v", openMsg.browserLeaseSnapshot.Interactive)
	}
	if len(openMsg.browserLeaseSnapshot.Waiting) != 1 {
		t.Fatalf("waiting leases = %d, want 1 after open failure", len(openMsg.browserLeaseSnapshot.Waiting))
	}
}

func TestBrowserAttentionBrowserSettingsShortcutOpensBrowserSection(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	m := Model{
		settingsBaseline: &settings,
		browserAttention: &browserAttentionNotification{
			ProjectPath: "/tmp/demo",
			ProjectName: "demo",
			Provider:    codexapp.ProviderCodex,
			Activity: browserctl.SessionActivity{
				Policy:     settingsAutomaticPlaywrightPolicy,
				State:      browserctl.SessionActivityStateWaitingForUser,
				ServerName: "playwright",
			},
		},
	}

	updated, cmd := m.updateBrowserAttentionMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b")})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("browser attention b should open browser settings")
	}
	if got.browserAttention != nil {
		t.Fatalf("browser attention should clear after opening browser settings")
	}
	if !got.settingsMode {
		t.Fatalf("settings mode should open from browser attention")
	}
	if got.settingsSelected != settingsFieldBrowserAutomation {
		t.Fatalf("settingsSelected = %d, want browser automation field", got.settingsSelected)
	}
	if got.activeSettingsSection().id != settingsSectionBrowser {
		t.Fatalf("active settings section = %q, want browser", got.activeSettingsSection().id)
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
	if !got.settingsSaving {
		t.Fatalf("settings enter should mark saving in progress")
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
	if !strings.Contains(saved.status, "Filters, API keys, local endpoint/model overrides, Codex launch mode, and browser automation policy are applying in the background now") {
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

func TestSettingsBrowserAutomationMapsToManagedPolicy(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	m := Model{
		settingsMode:   true,
		settingsFields: newSettingsFields(config.EditableSettingsFromAppConfig(config.Default())),
		width:          100,
		height:         24,
	}
	_ = m.setSettingsSelection(settingsFieldBrowserAutomation)
	updated, cmd := m.updateSettingsMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("browser automation enter should not queue a save command")
	}
	if !got.settingsBrowserPickerVisible {
		t.Fatalf("browser automation enter should open the chooser")
	}

	updated, cmd = got.updateSettingsBrowserAutomationPickerMode(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if cmd != nil {
		t.Fatalf("browser automation choice apply should not save immediately")
	}
	if got.settingsBrowserPickerVisible {
		t.Fatalf("browser automation chooser should close after choosing")
	}
	if got.settingsFields[settingsFieldBrowserAutomation].input.Value() != "only-when-needed" {
		t.Fatalf("browser automation value = %q, want only-when-needed", got.settingsFields[settingsFieldBrowserAutomation].input.Value())
	}

	updated, cmd = got.updateSettingsMode(tea.KeyMsg{Type: tea.KeyCtrlS})
	got = updated.(Model)
	if cmd == nil {
		t.Fatalf("expected save command after choosing browser automation")
	}
	if !got.settingsSaving {
		t.Fatalf("settings ctrl+s should mark saving in progress")
	}

	msg := cmd()
	finalModel, _ := got.Update(msg)
	saved := finalModel.(Model)
	if saved.settingsMode {
		t.Fatalf("settings mode should close after a successful save")
	}

	configPath := filepath.Join(home, ".little-control-room", "config.toml")
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read saved config: %v", err)
	}
	text := string(raw)
	for _, want := range []string{
		"playwright_management_mode = \"managed\"",
		"playwright_default_browser_mode = \"headless\"",
		"playwright_login_mode = \"promote\"",
		"playwright_isolation_scope = \"task\"",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("saved config missing %q: %q", want, text)
		}
	}
}

func TestSettingsSavingBlocksRepeatEnter(t *testing.T) {
	m := Model{
		settingsMode:   true,
		settingsSaving: true,
		settingsFields: newSettingsFields(config.EditableSettingsFromAppConfig(config.Default())),
		status:         "Saving settings...",
		width:          100,
		height:         24,
	}
	_ = m.setSettingsSelection(0)

	updated, cmd := m.updateSettingsMode(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("settings enter should not queue another save while saving")
	}
	if !got.settingsSaving {
		t.Fatalf("settings saving flag should stay true until the save completes")
	}
	if got.status != "Saving settings..." {
		t.Fatalf("status = %q, want existing saving message", got.status)
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

func TestSettingsSavePreservesEmbeddedModelPreferences(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfg := config.Default()
	cfg.ConfigPath = filepath.Join(home, ".little-control-room", "config.toml")
	cfg.EmbeddedCodexModel = "gpt-5.4"
	cfg.EmbeddedCodexReasoning = "high"
	cfg.EmbeddedClaudeModel = "sonnet"
	cfg.EmbeddedClaudeReasoning = "max"
	cfg.EmbeddedOpenCodeModel = "openai/gpt-5.4"
	cfg.EmbeddedOpenCodeReasoning = "medium"

	svc := service.New(cfg, nil, events.NewBus(), nil)
	m := New(context.Background(), svc)
	m.settingsMode = true
	m.settingsFields = newSettingsFields(config.EditableSettingsFromAppConfig(cfg))
	m.width = 100
	m.height = 24
	_ = m.setSettingsSelection(settingsFieldOpenAIAPIKey)
	m.settingsFields[settingsFieldOpenAIAPIKey].input.SetValue("sk-test-example")

	updated, cmd := m.updateSettingsMode(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatalf("expected save command from settings enter")
	}
	msg := cmd()
	savedMsg, ok := msg.(settingsSavedMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want settingsSavedMsg", msg)
	}
	if savedMsg.err != nil {
		t.Fatalf("settings save returned error = %v", savedMsg.err)
	}
	got := updated.(Model)
	updated, _ = got.Update(savedMsg)
	got = updated.(Model)

	raw, err := os.ReadFile(cfg.ConfigPath)
	if err != nil {
		t.Fatalf("read saved config: %v", err)
	}
	text := string(raw)
	for _, want := range []string{
		"embedded_codex_model = \"gpt-5.4\"",
		"embedded_codex_reasoning_effort = \"high\"",
		"embedded_claude_model = \"sonnet\"",
		"embedded_claude_reasoning_effort = \"max\"",
		"embedded_opencode_model = \"openai/gpt-5.4\"",
		"embedded_opencode_reasoning_effort = \"medium\"",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("saved config missing %q: %q", want, text)
		}
	}
	if got.currentSettingsBaseline().EmbeddedCodexModel != "gpt-5.4" {
		t.Fatalf("baseline embedded codex model = %q, want gpt-5.4", got.currentSettingsBaseline().EmbeddedCodexModel)
	}
	if got.currentSettingsBaseline().EmbeddedClaudeModel != "sonnet" {
		t.Fatalf("baseline embedded claude model = %q, want sonnet", got.currentSettingsBaseline().EmbeddedClaudeModel)
	}
}

func TestCodexActionMsgPersistsEmbeddedModelPreferencesToConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfg := config.Default()
	cfg.ConfigPath = filepath.Join(home, ".little-control-room", "config.toml")
	svc := service.New(cfg, nil, events.NewBus(), nil)
	m := New(context.Background(), svc)

	updated, cmd := m.Update(codexActionMsg{
		projectPath: "/tmp/demo",
		status:      "Embedded model set to gpt-5.4 with high reasoning for the next prompt",
		provider:    codexapp.ProviderCodex,
		model:       "gpt-5.4",
		reasoning:   "high",
	})
	if cmd == nil {
		t.Fatalf("codexActionMsg should trigger a config save command")
	}
	got := updated.(Model)
	if got.status != "Embedded model set to gpt-5.4 with high reasoning for the next prompt" {
		t.Fatalf("status = %q, want model update message", got.status)
	}

	msg := cmd()
	savedMsg, ok := msg.(embeddedModelPreferencesSavedMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want embeddedModelPreferencesSavedMsg", msg)
	}
	if savedMsg.err != nil {
		t.Fatalf("embedded model preference save returned error = %v", savedMsg.err)
	}

	updated, _ = got.Update(savedMsg)
	got = updated.(Model)
	raw, err := os.ReadFile(cfg.ConfigPath)
	if err != nil {
		t.Fatalf("read saved config: %v", err)
	}
	text := string(raw)
	if !strings.Contains(text, "embedded_codex_model = \"gpt-5.4\"") || !strings.Contains(text, "embedded_codex_reasoning_effort = \"high\"") {
		t.Fatalf("saved config missing codex model preference: %q", text)
	}
	if got.currentSettingsBaseline().EmbeddedCodexModel != "gpt-5.4" || got.currentSettingsBaseline().EmbeddedCodexReasoning != "high" {
		t.Fatalf("settings baseline = %#v, want saved codex model preference", got.currentSettingsBaseline())
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
	if !strings.Contains(rendered, "Stored key ends with") || !strings.Contains(rendered, "...12345.") {
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
	if !strings.Contains(got.status, "Filters, API keys, local endpoint/model overrides, Codex launch mode, and browser automation policy are applying in the background now") {
		t.Fatalf("status = %q, want immediate-apply notice", got.status)
	}
}

func TestApplyEditableSettingsCmdReturnsCompletionMsg(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	svc := service.New(config.Default(), st, events.NewBus(), nil)
	m := Model{svc: svc}

	cmd := m.applyEditableSettingsCmd(config.EditableSettingsFromAppConfig(config.Default()))
	if cmd == nil {
		t.Fatal("applyEditableSettingsCmd() should return a command when the service is available")
	}
	msg := cmd()
	if _, ok := msg.(editableSettingsAppliedMsg); !ok {
		t.Fatalf("cmd() returned %T, want editableSettingsAppliedMsg", msg)
	}
}

func TestBusClassificationFailureAddsErrorLogEntry(t *testing.T) {
	m := Model{
		nowFn: func() time.Time {
			return time.Date(2026, 4, 5, 12, 0, 0, 0, time.UTC)
		},
		allProjects: []model.ProjectSummary{{
			Name: "demo",
			Path: "/tmp/demo",
		}},
	}

	updated, cmd := m.Update(busMsg(events.Event{
		Type:        events.ClassificationUpdated,
		At:          time.Date(2026, 4, 5, 11, 59, 0, 0, time.UTC),
		ProjectPath: "/tmp/demo",
		Payload: map[string]string{
			"status": "failed",
			"stage":  "waiting_for_model",
			"model":  "mlx-community/Qwen3.5-9B-MLX-4bit",
			"error":  "connection refused",
		},
	}))
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("classification update should continue waiting on the bus")
	}
	if len(got.errorLogEntries) != 1 {
		t.Fatalf("error log count = %d, want 1", len(got.errorLogEntries))
	}
	if got.status != "Assessment failed (use /errors)" {
		t.Fatalf("status = %q, want assessment failure hint", got.status)
	}
	entry := got.errorLogEntries[0]
	if entry.Status != "Assessment failed" {
		t.Fatalf("error log status = %q, want %q", entry.Status, "Assessment failed")
	}
	if entry.Message != "classification stage waiting for model: model mlx-community/Qwen3.5-9B-MLX-4bit: connection refused" {
		t.Fatalf("error log message = %q", entry.Message)
	}
	if entry.RootCause != "connection refused" {
		t.Fatalf("error log root cause = %q, want %q", entry.RootCause, "connection refused")
	}
	if len(entry.Context) != 2 || entry.Context[0] != "classification stage waiting for model" || entry.Context[1] != "model mlx-community/Qwen3.5-9B-MLX-4bit" {
		t.Fatalf("error log context = %#v", entry.Context)
	}
}

func TestBusClassificationTimeoutUsesSpecificAssessmentStatus(t *testing.T) {
	m := Model{
		nowFn: func() time.Time {
			return time.Date(2026, 4, 20, 22, 32, 5, 0, time.FixedZone("CST", 8*60*60))
		},
		allProjects: []model.ProjectSummary{{
			Name: "LittleControlRoom--todo-we-v-ebeen-working-a-lot-on-this-project",
			Path: "/tmp/demo",
		}},
	}

	updated, cmd := m.Update(busMsg(events.Event{
		Type:        events.ClassificationUpdated,
		At:          time.Date(2026, 4, 20, 22, 32, 5, 0, time.FixedZone("CST", 8*60*60)),
		ProjectPath: "/tmp/demo",
		Payload: map[string]string{
			"status":          "failed",
			"stage":           "waiting_for_model",
			"model":           "gpt-5.4-mini",
			"error":           "context deadline exceeded",
			"error_kind":      "timeout",
			"error_diagnosis": "request timed out while contacting the model; network connectivity or provider availability may be degraded",
		},
	}))
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("classification timeout should continue waiting on the bus")
	}
	if len(got.errorLogEntries) != 1 {
		t.Fatalf("error log count = %d, want 1", len(got.errorLogEntries))
	}
	if got.status != "Assessment timed out (use /errors)" {
		t.Fatalf("status = %q, want timeout-specific assessment hint", got.status)
	}
	entry := got.errorLogEntries[0]
	if entry.Status != "Assessment timed out" {
		t.Fatalf("error log status = %q, want %q", entry.Status, "Assessment timed out")
	}
	if entry.RootCause != "context deadline exceeded" {
		t.Fatalf("error log root cause = %q, want %q", entry.RootCause, "context deadline exceeded")
	}
	if len(entry.Context) != 3 {
		t.Fatalf("error log context = %#v, want 3 lines", entry.Context)
	}
	if entry.Context[0] != "classification stage waiting for model" {
		t.Fatalf("context[0] = %q, want stage", entry.Context[0])
	}
	if entry.Context[1] != "model gpt-5.4-mini" {
		t.Fatalf("context[1] = %q, want model", entry.Context[1])
	}
	if entry.Context[2] != "request timed out while contacting the model; network connectivity or provider availability may be degraded" {
		t.Fatalf("context[2] = %q, want diagnosis", entry.Context[2])
	}
}

func TestBusClassificationConnectionFailureUsesSpecificAssessmentStatus(t *testing.T) {
	m := Model{
		nowFn: func() time.Time {
			return time.Date(2026, 5, 2, 15, 25, 47, 0, time.FixedZone("JST", 9*60*60))
		},
		allProjects: []model.ProjectSummary{{
			Name: "LittleControlRoom",
			Path: "/tmp/demo",
		}},
	}

	updated, cmd := m.Update(busMsg(events.Event{
		Type:        events.ClassificationUpdated,
		At:          time.Date(2026, 5, 2, 15, 25, 47, 0, time.FixedZone("JST", 9*60*60)),
		ProjectPath: "/tmp/demo",
		Payload: map[string]string{
			"status":          "failed",
			"stage":           "waiting_for_model",
			"model":           "gpt-5.4-mini",
			"error":           "Reconnecting... 5/5",
			"error_kind":      "connection_failed",
			"error_diagnosis": "could not reach the model; network connectivity or provider availability may be degraded",
		},
	}))
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("classification connection failure should continue waiting on the bus")
	}
	if got.status != "Assessment connection failed (use /errors)" {
		t.Fatalf("status = %q, want connection-specific assessment hint", got.status)
	}
	if len(got.errorLogEntries) != 1 {
		t.Fatalf("error log count = %d, want 1", len(got.errorLogEntries))
	}
	entry := got.errorLogEntries[0]
	if entry.Status != "Assessment connection failed" {
		t.Fatalf("error log status = %q, want %q", entry.Status, "Assessment connection failed")
	}
	if entry.RootCause != "Reconnecting... 5/5" {
		t.Fatalf("error log root cause = %q, want reconnect detail", entry.RootCause)
	}
	if len(entry.Context) != 3 {
		t.Fatalf("error log context = %#v, want 3 lines", entry.Context)
	}
	if entry.Context[0] != "classification stage waiting for model" {
		t.Fatalf("context[0] = %q, want stage", entry.Context[0])
	}
	if entry.Context[1] != "model gpt-5.4-mini" {
		t.Fatalf("context[1] = %q, want model", entry.Context[1])
	}
	if entry.Context[2] != "could not reach the model; network connectivity or provider availability may be degraded" {
		t.Fatalf("context[2] = %q, want diagnosis", entry.Context[2])
	}
}

func TestBusClassificationOpenFileLimitUsesSpecificAssessmentStatus(t *testing.T) {
	m := Model{
		nowFn: func() time.Time {
			return time.Date(2026, 4, 22, 17, 38, 58, 0, time.FixedZone("CST", 8*60*60))
		},
		allProjects: []model.ProjectSummary{{
			Name: "LittleControlRoom",
			Path: "/tmp/demo",
		}},
	}

	updated, cmd := m.Update(busMsg(events.Event{
		Type:        events.ClassificationUpdated,
		At:          time.Date(2026, 4, 22, 17, 38, 58, 0, time.FixedZone("CST", 8*60*60)),
		ProjectPath: "/tmp/demo",
		Payload: map[string]string{
			"status":          "failed",
			"stage":           "waiting_for_model",
			"model":           "gpt-5.4-mini",
			"error":           "error creating thread: Fatal error: Failed to initialize session: Too many open files (os error 24)",
			"error_kind":      "open_file_limit",
			"error_diagnosis": "local open-file limit was reached while assessing the latest session; too many helper processes or open files may already be active",
		},
	}))
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("classification open-file-limit failure should continue waiting on the bus")
	}
	if len(got.errorLogEntries) != 1 {
		t.Fatalf("error log count = %d, want 1", len(got.errorLogEntries))
	}
	if got.status != "Assessment hit open-file limit (use /errors)" {
		t.Fatalf("status = %q, want open-file-limit assessment hint", got.status)
	}
	entry := got.errorLogEntries[0]
	if entry.Status != "Assessment hit open-file limit" {
		t.Fatalf("error log status = %q, want %q", entry.Status, "Assessment hit open-file limit")
	}
	if entry.RootCause != "error creating thread: Fatal error: Failed to initialize session: Too many open files (os error 24)" {
		t.Fatalf("error log root cause = %q", entry.RootCause)
	}
	if len(entry.Context) != 3 {
		t.Fatalf("error log context = %#v, want 3 lines", entry.Context)
	}
	if entry.Context[0] != "classification stage waiting for model" {
		t.Fatalf("context[0] = %q, want stage", entry.Context[0])
	}
	if entry.Context[1] != "model gpt-5.4-mini" {
		t.Fatalf("context[1] = %q, want model", entry.Context[1])
	}
	if entry.Context[2] != "local open-file limit was reached while assessing the latest session; too many helper processes or open files may already be active" {
		t.Fatalf("context[2] = %q, want diagnosis", entry.Context[2])
	}
}

func TestBusClassificationBackendUnavailableUsesSpecificAssessmentStatus(t *testing.T) {
	m := Model{
		nowFn: func() time.Time {
			return time.Date(2026, 4, 29, 12, 0, 0, 0, time.UTC)
		},
		allProjects: []model.ProjectSummary{{
			Name: "LittleControlRoom",
			Path: "/tmp/demo",
		}},
	}

	updated, cmd := m.Update(busMsg(events.Event{
		Type:        events.ClassificationUpdated,
		At:          time.Date(2026, 4, 29, 11, 59, 0, 0, time.UTC),
		ProjectPath: "/tmp/demo",
		Payload: map[string]string{
			"status":          "failed",
			"error":           "session classifier unavailable: Codex assessment backend is not ready",
			"error_kind":      "backend_unavailable",
			"error_diagnosis": "AI assessment backend is not configured or not ready; open /setup to select a working backend",
		},
	}))
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("classification backend-unavailable failure should continue waiting on the bus")
	}
	if got.status != "Assessment backend unavailable (use /errors)" {
		t.Fatalf("status = %q, want backend-unavailable assessment hint", got.status)
	}
	if len(got.errorLogEntries) != 1 {
		t.Fatalf("error log count = %d, want 1", len(got.errorLogEntries))
	}
	if got.errorLogEntries[0].Status != "Assessment backend unavailable" {
		t.Fatalf("error log status = %q", got.errorLogEntries[0].Status)
	}
}

func TestBusTodoSuggestionFailureAddsErrorLogEntry(t *testing.T) {
	m := Model{
		nowFn: func() time.Time {
			return time.Date(2026, 4, 5, 12, 0, 0, 0, time.UTC)
		},
		allProjects: []model.ProjectSummary{{
			Name: "demo",
			Path: "/tmp/demo",
		}},
	}

	updated, cmd := m.Update(busMsg(events.Event{
		Type:        events.ActionApplied,
		At:          time.Date(2026, 4, 5, 11, 59, 0, 0, time.UTC),
		ProjectPath: "/tmp/demo",
		Payload: map[string]string{
			"action": "todo_worktree_suggestion_failed",
			"model":  "mlx-community/Qwen3.5-9B-MLX-4bit",
			"error":  "EOF while reading response body",
		},
	}))
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("todo suggestion failure should continue waiting on the bus")
	}
	if len(got.errorLogEntries) != 1 {
		t.Fatalf("error log count = %d, want 1", len(got.errorLogEntries))
	}
	if got.status != "TODO worktree suggestion failed (use /errors)" {
		t.Fatalf("status = %q, want TODO suggestion failure hint", got.status)
	}
	entry := got.errorLogEntries[0]
	if entry.Status != "TODO worktree suggestion failed" {
		t.Fatalf("error log status = %q, want %q", entry.Status, "TODO worktree suggestion failed")
	}
	if entry.RootCause != "EOF while reading response body" {
		t.Fatalf("error log root cause = %q", entry.RootCause)
	}
	if len(entry.Context) != 1 || entry.Context[0] != "model mlx-community/Qwen3.5-9B-MLX-4bit" {
		t.Fatalf("error log context = %#v", entry.Context)
	}
}

func TestActionChangesProjectStructure(t *testing.T) {
	for _, action := range []string{"forget_project", "remove_worktree", "scratch_task_archived", "scratch_task_deleted"} {
		if !actionChangesProjectStructure(action) {
			t.Fatalf("actionChangesProjectStructure(%q) = false, want true", action)
		}
	}
	for _, action := range []string{"toggle_pin", "git_push", "todo_worktree_suggestion_failed", ""} {
		if actionChangesProjectStructure(action) {
			t.Fatalf("actionChangesProjectStructure(%q) = true, want false", action)
		}
	}
}

func TestBusRemoveWorktreeRefreshTargetsRootDetail(t *testing.T) {
	rootPath := "/tmp/repo"
	childPath := "/tmp/repo--feat-parallel-lane"
	m := Model{
		allProjects: []model.ProjectSummary{
			{
				Name:             "repo",
				Path:             rootPath,
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindMain,
			},
			{
				Name:             "repo--feat-parallel-lane",
				Path:             childPath,
				PresentOnDisk:    true,
				WorktreeRootPath: rootPath,
				WorktreeKind:     model.WorktreeKindLinked,
			},
		},
		detail: model.ProjectDetail{
			Summary: model.ProjectSummary{
				Name: childPath,
				Path: childPath,
			},
		},
		sortMode:   sortByAttention,
		visibility: visibilityAllFolders,
	}
	m.rebuildProjectList(childPath)

	updated, cmd := m.Update(busMsg(events.Event{
		Type:        events.ActionApplied,
		ProjectPath: childPath,
		Payload: map[string]string{
			"action":    "remove_worktree",
			"root_path": rootPath,
		},
	}))
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("remove-worktree bus event should keep waiting on the bus and queue a refresh")
	}
	if !got.projectsReloadInFlight {
		t.Fatalf("remove-worktree bus event should queue a project list reload")
	}
	if !got.detailReloadInFlight[rootPath] {
		t.Fatalf("detail reload should target root path %q, got %#v", rootPath, got.detailReloadInFlight)
	}
	if got.detailReloadInFlight[childPath] {
		t.Fatalf("detail reload should not target removed worktree path %q", childPath)
	}
}

func TestDispatchRemoveCommandStoresIgnoredPathAndHidesOnlySelectedProject(t *testing.T) {
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
			PresentOnDisk:  true,
			InScope:        true,
			UpdatedAt:      now,
		},
		{
			Path:           "/tmp/worktrees/a1/projects_control_center",
			Name:           "projects_control_center",
			AttentionScore: 15,
			PresentOnDisk:  true,
			InScope:        true,
			UpdatedAt:      now,
		},
		{
			Path:           "/tmp/visible-demo",
			Name:           "visible-demo",
			AttentionScore: 10,
			PresentOnDisk:  true,
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
		t.Fatalf("initial projects = %#v, want removable candidate first", m.projects)
	}

	updated, cmd := m.dispatchCommand(commands.Invocation{Kind: commands.KindRemove, Canonical: "/remove"})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("dispatchCommand(/remove) should open confirmation before scheduling work")
	}
	if got.projectRemoveConfirm == nil {
		t.Fatalf("dispatchCommand(/remove) should open the project removal confirmation")
	}
	if got.projectRemoveConfirm.Selected != projectRemoveConfirmFocusKeep {
		t.Fatalf("default removal confirmation selection = %d, want keep", got.projectRemoveConfirm.Selected)
	}
	rendered := ansi.Strip(got.renderProjectRemoveConfirmOverlay("", 100, 24))
	if !strings.Contains(rendered, "only this exact project path") || !strings.Contains(rendered, "does not delete files") {
		t.Fatalf("project removal confirmation should explain files are kept, got %q", rendered)
	}

	updated, _ = got.updateProjectRemoveConfirmMode(tea.KeyMsg{Type: tea.KeyTab})
	got = updated.(Model)
	if got.projectRemoveConfirm.Selected != projectRemoveConfirmFocusRemove {
		t.Fatalf("tab should move project removal focus to remove")
	}

	updated, cmd = got.updateProjectRemoveConfirmMode(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if cmd == nil {
		t.Fatalf("project removal confirmation should return a removal command")
	}

	rawMsg := cmd()
	afterAction, reloadCmd := got.Update(rawMsg)
	reloaded := afterAction.(Model)
	if reloadCmd == nil {
		t.Fatalf("remove action should trigger a project reload")
	}
	projectsMsg := reloadCmd()
	finalModel, _ := reloaded.Update(projectsMsg)
	saved := finalModel.(Model)
	if len(saved.projects) != 2 || saved.projects[0].Path != "/tmp/worktrees/a1/projects_control_center" || saved.projects[1].Name != "visible-demo" {
		t.Fatalf("visible projects after /remove = %#v, want same-name worktree plus visible-demo", saved.projects)
	}
	if saved.status != `Removed "projects_control_center" from list` {
		t.Fatalf("status = %q, want removal confirmation", saved.status)
	}

	ignoredNames, err := st.ListIgnoredProjectNames(ctx)
	if err != nil {
		t.Fatalf("list ignored names: %v", err)
	}
	if len(ignoredNames) != 0 {
		t.Fatalf("ignored names = %#v, want none for path-specific remove", ignoredNames)
	}

	ignored, err := st.ListIgnoredProjects(ctx)
	if err != nil {
		t.Fatalf("list ignored projects: %v", err)
	}
	if len(ignored) != 1 || ignored[0].Scope != model.ProjectIgnoreScopePath || ignored[0].Path != "/tmp/projects_control_center" {
		t.Fatalf("ignored projects = %#v, want exact path /tmp/projects_control_center", ignored)
	}
}

func TestDispatchRemoveCommandForMissingProjectMarksItForgotten(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	now := time.Date(2026, 3, 17, 9, 0, 0, 0, time.UTC)
	missingPath := filepath.Join(t.TempDir(), "missing-demo")
	visiblePath := filepath.Join(t.TempDir(), "visible-demo")
	for _, state := range []model.ProjectState{
		{
			Path:           missingPath,
			Name:           "missing-demo",
			AttentionScore: 20,
			PresentOnDisk:  false,
			InScope:        true,
			UpdatedAt:      now,
		},
		{
			Path:           visiblePath,
			Name:           "visible-demo",
			AttentionScore: 10,
			PresentOnDisk:  true,
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
	if len(m.projects) != 2 || m.projects[0].Name != "missing-demo" {
		t.Fatalf("initial projects = %#v, want missing project first", m.projects)
	}

	updated, cmd := m.dispatchCommand(commands.Invocation{Kind: commands.KindRemove, Canonical: "/remove"})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("dispatchCommand(/remove) should open confirmation before removing missing projects")
	}
	if got.projectRemoveConfirm == nil {
		t.Fatalf("dispatchCommand(/remove) should open the project removal confirmation")
	}
	if got.projectRemoveConfirm.Selected != projectRemoveConfirmFocusKeep {
		t.Fatalf("default removal confirmation selection = %d, want keep", got.projectRemoveConfirm.Selected)
	}
	rendered := ansi.Strip(got.renderProjectRemoveConfirmOverlay("", 100, 24))
	if !strings.Contains(rendered, "stale dashboard entry") {
		t.Fatalf("missing project removal confirmation should explain stale entry removal, got %q", rendered)
	}

	updated, _ = got.updateProjectRemoveConfirmMode(tea.KeyMsg{Type: tea.KeyTab})
	got = updated.(Model)
	if got.projectRemoveConfirm.Selected != projectRemoveConfirmFocusRemove {
		t.Fatalf("tab should move project removal focus to remove")
	}

	updated, cmd = got.updateProjectRemoveConfirmMode(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if cmd == nil {
		t.Fatalf("project removal confirmation should return a removal command")
	}

	rawMsg := cmd()
	afterAction, reloadCmd := got.Update(rawMsg)
	reloaded := afterAction.(Model)
	if reloadCmd == nil {
		t.Fatalf("remove action should trigger a project reload")
	}
	if reloaded.status != "Removed from list" {
		t.Fatalf("status = %q, want missing-project removal confirmation", reloaded.status)
	}

	visibleProjects, err := st.ListProjects(ctx, false)
	if err != nil {
		t.Fatalf("list visible projects after removal: %v", err)
	}
	if len(visibleProjects) != 1 || visibleProjects[0].Path != visiblePath {
		t.Fatalf("visible projects after removing missing project = %#v, want only visible-demo", visibleProjects)
	}

	ignored, err := st.ListIgnoredProjectNames(ctx)
	if err != nil {
		t.Fatalf("list ignored names: %v", err)
	}
	if len(ignored) != 0 {
		t.Fatalf("ignored names after missing-project removal = %#v, want none", ignored)
	}

	detail, err := st.GetProjectDetail(ctx, missingPath, 1)
	if err != nil {
		t.Fatalf("get missing project detail: %v", err)
	}
	if !detail.Summary.Forgotten {
		t.Fatalf("missing project summary = %#v, want forgotten=true", detail.Summary)
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

func TestIgnoredPickerListsAndRestoresIgnoredPaths(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	projectPath := "/tmp/projects_control_center"
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:          projectPath,
		Name:          "projects_control_center",
		PresentOnDisk: true,
		InScope:       true,
		UpdatedAt:     time.Date(2026, 3, 17, 9, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("upsert project: %v", err)
	}
	if err := st.SetIgnoredProjectPath(ctx, projectPath, true); err != nil {
		t.Fatalf("seed ignored project path: %v", err)
	}

	svc := service.New(config.Default(), st, events.NewBus(), nil)
	m := Model{ctx: ctx, svc: svc}

	updated, cmd := m.dispatchCommand(commands.Invocation{Kind: commands.KindIgnored})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("/ignored should load ignored projects")
	}

	loadedModel, _ := got.Update(cmd())
	loaded := loadedModel.(Model)
	if len(loaded.ignoredPickerItems) != 1 || loaded.ignoredPickerItems[0].Scope != model.ProjectIgnoreScopePath || loaded.ignoredPickerItems[0].Path != projectPath {
		t.Fatalf("ignored picker items = %#v, want exact path", loaded.ignoredPickerItems)
	}

	nextModel, restoreCmd := loaded.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next := nextModel.(Model)
	if restoreCmd == nil {
		t.Fatalf("enter in ignored picker should restore the path")
	}
	if !next.ignoredPickerLoading {
		t.Fatalf("ignored picker should return to loading while restoring")
	}

	restoredModel, _ := next.Update(restoreCmd())
	restored := restoredModel.(Model)
	if restored.status != `Restored "/tmp/projects_control_center"` {
		t.Fatalf("status = %q, want restore confirmation", restored.status)
	}

	ignored, err := st.ListIgnoredProjects(ctx)
	if err != nil {
		t.Fatalf("list ignored projects after restore: %v", err)
	}
	if len(ignored) != 0 {
		t.Fatalf("ignored projects after restore = %#v, want none", ignored)
	}
}

func TestViewWithCommitPreviewRespectsHeight(t *testing.T) {
	m := Model{
		projects: []model.ProjectSummary{{
			Name:                             "demo",
			Path:                             "/tmp/demo",
			Status:                           model.StatusIdle,
			PresentOnDisk:                    true,
			RepoBranch:                       "master",
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
	messageLine := -1
	stageLine := -1
	for i, line := range lines {
		switch {
		case strings.Contains(line, "Message:") && strings.Contains(line, "Ship current repo changes"):
			messageLine = i
		case strings.Contains(line, "Stage: stage all current changes"):
			stageLine = i
		}
	}
	if messageLine < 0 || stageLine < 0 {
		t.Fatalf("View() missing inline commit message or stage line: %q", rendered)
	}
	if !(messageLine < stageLine) {
		t.Fatalf("View() should show the commit message before stage metadata: %q", rendered)
	}
	if stageLine != messageLine+1 {
		t.Fatalf("View() should show stage immediately after the inline commit message line: %q", rendered)
	}
	if !strings.Contains(rendered, "Repo: clean") {
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

	rendered := ansi.Strip(m.renderCommitPreviewContent(72, 50))
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

	rendered := ansi.Strip(m.renderCommitPreviewContent(72, 50))
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
	if got.pendingGitSummary("/tmp/demo") != "Committing..." {
		t.Fatalf("pending git summary = %q, want %q", got.pendingGitSummary("/tmp/demo"), "Committing...")
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

func TestCommitPreviewRefreshingAllowsEscCancel(t *testing.T) {
	m := Model{
		commitPreview: &service.CommitPreview{
			ProjectPath: "/tmp/demo",
			Message:     "Generating commit message...",
		},
		commitPreviewRefreshing: true,
		commitPreviewRequestID:  3,
		pendingGitSummaries: map[string]string{
			"/tmp/demo": "Preparing commit preview...",
		},
	}

	updated, cmd := m.updateCommitPreviewMode(tea.KeyMsg{Type: tea.KeyEsc})
	got := updated.(Model)
	if got.commitPreview != nil {
		t.Fatalf("Esc should close a refreshing commit preview")
	}
	if got.commitPreviewRefreshing {
		t.Fatalf("Esc should clear refreshing state")
	}
	if got.commitPreviewRequestID != 4 {
		t.Fatalf("request id = %d, want old async result invalidated", got.commitPreviewRequestID)
	}
	if got.pendingGitSummary("/tmp/demo") != "" {
		t.Fatalf("pending git summary should clear when canceling refresh")
	}
	if got.status != "Commit preview canceled" {
		t.Fatalf("status = %q, want cancel status", got.status)
	}
	if cmd != nil {
		t.Fatalf("Esc cancel should not return a command")
	}
}

func TestCommitPreviewMsgIgnoresStaleCanceledRequest(t *testing.T) {
	m := Model{
		commitPreviewRequestID: 4,
	}

	updated, cmd := m.Update(commitPreviewMsg{
		requestID:   3,
		projectPath: "/tmp/demo",
		preview: service.CommitPreview{
			ProjectPath: "/tmp/demo",
			Message:     "Late result",
		},
	})
	got := updated.(Model)
	if got.commitPreview != nil {
		t.Fatalf("stale commit preview result should not reopen the dialog")
	}
	if cmd != nil {
		t.Fatalf("stale commit preview result should not return a command")
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
	if got.pendingGitSummary("/tmp/demo") != "Committing and pushing..." {
		t.Fatalf("pending git summary = %q, want %q", got.pendingGitSummary("/tmp/demo"), "Committing and pushing...")
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

func TestDispatchCommandPushMarksPendingGitOperation(t *testing.T) {
	m := Model{
		projects: []model.ProjectSummary{{
			Name:          "demo",
			Path:          "/tmp/demo",
			PresentOnDisk: true,
		}},
		selected: 0,
	}

	updated, cmd := m.dispatchCommand(commands.Invocation{Kind: commands.KindPush})
	got := updated.(Model)
	if got.pendingGitSummary("/tmp/demo") != "Pushing..." {
		t.Fatalf("pending git summary = %q, want push marker", got.pendingGitSummary("/tmp/demo"))
	}
	if got.status != "Pushing..." {
		t.Fatalf("status = %q, want push-in-flight status", got.status)
	}
	if cmd == nil {
		t.Fatalf("/push should schedule async work")
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

func TestCommitPreviewNoChangesRefreshesProjectStatus(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	projectPath := filepath.Join(t.TempDir(), "repo")
	runTUITestGit(t, "", "init", projectPath)
	runTUITestGit(t, projectPath, "config", "user.name", "Little Control Room Tests")
	runTUITestGit(t, projectPath, "config", "user.email", "tests@example.com")
	if err := os.WriteFile(filepath.Join(projectPath, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write README.md: %v", err)
	}
	runTUITestGit(t, projectPath, "add", "README.md")
	runTUITestGit(t, projectPath, "commit", "-m", "initial commit")

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:          projectPath,
		Name:          "repo",
		PresentOnDisk: true,
		InScope:       true,
		RepoBranch:    "master",
		RepoDirty:     true,
		UpdatedAt:     time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed stale project state: %v", err)
	}

	svc := service.New(config.Default(), st, events.NewBus(), nil)
	m := Model{
		ctx: ctx,
		svc: svc,
		projects: []model.ProjectSummary{{
			Path: projectPath,
			Name: "repo",
		}},
		allProjects: []model.ProjectSummary{{
			Path: projectPath,
			Name: "repo",
		}},
		selected: 0,
		detail: model.ProjectDetail{
			Summary: model.ProjectSummary{
				Path: projectPath,
				Name: "repo",
			},
		},
	}

	updated, cmd := m.Update(commitPreviewMsg{
		err: service.NoChangesToCommitError{
			ProjectPath: projectPath,
			ProjectName: "repo",
			Branch:      "master",
		},
	})
	got := updated.(Model)

	if cmd == nil {
		t.Fatal("no-changes commit result should refresh project status")
	}
	msg := cmd()
	refreshMsg, ok := msg.(projectStatusRefreshedMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want projectStatusRefreshedMsg", msg)
	}
	if refreshMsg.err != nil {
		t.Fatalf("refresh err = %v", refreshMsg.err)
	}

	detail, err := st.GetProjectDetail(ctx, projectPath, 20)
	if err != nil {
		t.Fatalf("get detail after refresh: %v", err)
	}
	if detail.Summary.RepoDirty {
		t.Fatalf("expected refresh to clear stale dirty state")
	}

	updated, cmd = got.Update(refreshMsg)
	got = updated.(Model)
	if cmd == nil {
		t.Fatal("refresh completion should invalidate project data")
	}
	if !got.summaryReloadInFlight[projectPath] {
		t.Fatalf("summary reload should start for %q", projectPath)
	}
	if !got.detailReloadInFlight[projectPath] {
		t.Fatalf("detail reload should start for visible project %q", projectPath)
	}
}

func TestPrepareCommitPreviewCmdRefreshesStaleProjectStatusBeforeNoChangesDialog(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	projectPath := filepath.Join(t.TempDir(), "repo")
	runTUITestGit(t, "", "init", projectPath)
	runTUITestGit(t, projectPath, "config", "user.name", "Little Control Room Tests")
	runTUITestGit(t, projectPath, "config", "user.email", "tests@example.com")
	if err := os.WriteFile(filepath.Join(projectPath, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write README.md: %v", err)
	}
	runTUITestGit(t, projectPath, "add", "README.md")
	runTUITestGit(t, projectPath, "commit", "-m", "initial commit")

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:          projectPath,
		Name:          "repo",
		PresentOnDisk: true,
		InScope:       true,
		RepoBranch:    "master",
		RepoDirty:     true,
		UpdatedAt:     time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed stale project state: %v", err)
	}

	svc := service.New(config.Default(), st, events.NewBus(), nil)
	m := Model{
		ctx: ctx,
		svc: svc,
		projects: []model.ProjectSummary{{
			Path: projectPath,
			Name: "repo",
		}},
		allProjects: []model.ProjectSummary{{
			Path: projectPath,
			Name: "repo",
		}},
		selected: 0,
		detail: model.ProjectDetail{
			Summary: model.ProjectSummary{
				Path: projectPath,
				Name: "repo",
			},
		},
	}

	cmd := m.prepareCommitPreviewCmd(projectPath, service.GitActionCommit, "")
	if cmd == nil {
		t.Fatal("prepareCommitPreviewCmd() should return a command")
	}
	raw := cmd()
	msg, ok := raw.(commitPreviewMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want commitPreviewMsg", raw)
	}
	if msg.err == nil {
		t.Fatal("expected no-changes commit preview error")
	}
	if !msg.refreshedProjectState {
		t.Fatal("expected no-changes preview to refresh stale project state")
	}

	detail, err := st.GetProjectDetail(ctx, projectPath, 20)
	if err != nil {
		t.Fatalf("get detail after prepare refresh: %v", err)
	}
	if detail.Summary.RepoDirty {
		t.Fatalf("expected prepare command to clear stale dirty state")
	}

	updated, reloadCmd := m.Update(msg)
	got := updated.(Model)
	if got.gitStatusDialog == nil {
		t.Fatalf("expected no-changes commit result to open the git status dialog")
	}
	if reloadCmd == nil {
		t.Fatal("no-changes commit result should invalidate project data immediately")
	}
	if !got.summaryReloadInFlight[projectPath] {
		t.Fatalf("summary reload should start for %q", projectPath)
	}
	if !got.detailReloadInFlight[projectPath] {
		t.Fatalf("detail reload should start for visible project %q", projectPath)
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
	if got.pendingGitSummary("/tmp/demo") != "Pushing existing commits..." {
		t.Fatalf("pending git summary = %q, want push-in-flight marker", got.pendingGitSummary("/tmp/demo"))
	}
	if got.status != "Pushing existing commits..." {
		t.Fatalf("status = %q, want %q", got.status, "Pushing existing commits...")
	}
	if cmd == nil {
		t.Fatalf("enter should return a push command when the dialog offers a push")
	}
}

func TestPushCmdRefreshesProjectStatusAndTargetsProjectReload(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	remotePath := filepath.Join(root, "origin.git")
	projectPath := filepath.Join(root, "repo")
	runTUITestGit(t, "", "init", "--bare", remotePath)
	runTUITestGit(t, "", "init", projectPath)
	runTUITestGit(t, projectPath, "config", "user.name", "Little Control Room Tests")
	runTUITestGit(t, projectPath, "config", "user.email", "tests@example.com")
	if err := os.WriteFile(filepath.Join(projectPath, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write README.md: %v", err)
	}
	runTUITestGit(t, projectPath, "add", "README.md")
	runTUITestGit(t, projectPath, "commit", "-m", "initial commit")
	runTUITestGit(t, projectPath, "remote", "add", "origin", remotePath)
	runTUITestGit(t, projectPath, "push", "-u", "origin", "master")

	if err := os.WriteFile(filepath.Join(projectPath, "README.md"), []byte("hello\nship\n"), 0o644); err != nil {
		t.Fatalf("update README.md: %v", err)
	}
	runTUITestGit(t, projectPath, "add", "README.md")
	runTUITestGit(t, projectPath, "commit", "-m", "ship")

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:           projectPath,
		Name:           "repo",
		PresentOnDisk:  true,
		InScope:        true,
		RepoBranch:     "master",
		RepoSyncStatus: model.RepoSyncAhead,
		RepoAheadCount: 1,
		UpdatedAt:      time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed stale project state: %v", err)
	}

	svc := service.New(config.Default(), st, events.NewBus(), nil)
	m := Model{
		ctx: ctx,
		svc: svc,
		projects: []model.ProjectSummary{{
			Path: projectPath,
			Name: "repo",
		}},
		allProjects: []model.ProjectSummary{{
			Path: projectPath,
			Name: "repo",
		}},
		selected: 0,
		detail: model.ProjectDetail{
			Summary: model.ProjectSummary{
				Path: projectPath,
				Name: "repo",
			},
		},
	}

	cmd := m.pushCmd(projectPath)
	if cmd == nil {
		t.Fatal("pushCmd() should return a command")
	}
	raw := cmd()
	msg, ok := raw.(actionMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want actionMsg", raw)
	}
	if msg.err != nil {
		t.Fatalf("push action err = %v", msg.err)
	}
	if msg.status != "Pushed latest commits" {
		t.Fatalf("status = %q, want push success message", msg.status)
	}
	if msg.refresh.kind != projectInvalidationProjectData || msg.refresh.projectPath != projectPath {
		t.Fatalf("refresh = %#v, want targeted project-data invalidation for %q", msg.refresh, projectPath)
	}

	detail, err := st.GetProjectDetail(ctx, projectPath, 20)
	if err != nil {
		t.Fatalf("get detail after push refresh: %v", err)
	}
	if detail.Summary.RepoAheadCount != 0 {
		t.Fatalf("expected ahead count to clear after push refresh, got %#v", detail.Summary)
	}
	if detail.Summary.RepoSyncStatus != model.RepoSyncSynced {
		t.Fatalf("expected synced repo status after push refresh, got %#v", detail.Summary)
	}

	updated, reloadCmd := m.Update(msg)
	got := updated.(Model)
	if reloadCmd == nil {
		t.Fatal("push action should invalidate project data")
	}
	if !got.summaryReloadInFlight[projectPath] {
		t.Fatalf("summary reload should start for %q", projectPath)
	}
	if !got.detailReloadInFlight[projectPath] {
		t.Fatalf("detail reload should start for visible project %q", projectPath)
	}
}

func TestApplyCommitPreviewCmdRefreshesProjectStatusAndTargetsProjectReload(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	projectPath := filepath.Join(root, "repo")
	runTUITestGit(t, "", "init", projectPath)
	runTUITestGit(t, projectPath, "config", "user.name", "Little Control Room Tests")
	runTUITestGit(t, projectPath, "config", "user.email", "tests@example.com")
	if err := os.WriteFile(filepath.Join(projectPath, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write README.md: %v", err)
	}
	runTUITestGit(t, projectPath, "add", "README.md")
	runTUITestGit(t, projectPath, "commit", "-m", "initial commit")
	if err := os.WriteFile(filepath.Join(projectPath, "README.md"), []byte("hello\nship\n"), 0o644); err != nil {
		t.Fatalf("update README.md: %v", err)
	}

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:          projectPath,
		Name:          "repo",
		PresentOnDisk: true,
		InScope:       true,
		RepoBranch:    "master",
		RepoDirty:     true,
		UpdatedAt:     time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed stale project state: %v", err)
	}

	svc := service.New(config.Default(), st, events.NewBus(), nil)
	m := Model{
		ctx: ctx,
		svc: svc,
		projects: []model.ProjectSummary{{
			Path: projectPath,
			Name: "repo",
		}},
		allProjects: []model.ProjectSummary{{
			Path: projectPath,
			Name: "repo",
		}},
		selected: 0,
		detail: model.ProjectDetail{
			Summary: model.ProjectSummary{
				Path: projectPath,
				Name: "repo",
			},
		},
	}

	cmd := m.applyCommitPreviewCmd(service.CommitPreview{
		ProjectPath: projectPath,
		ProjectName: "repo",
		Branch:      "master",
		StageMode:   service.GitStageAllChanges,
		Message:     "ship",
	}, false)
	if cmd == nil {
		t.Fatal("applyCommitPreviewCmd() should return a command")
	}
	raw := cmd()
	msg, ok := raw.(actionMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want actionMsg", raw)
	}
	if msg.err != nil {
		t.Fatalf("commit action err = %v", msg.err)
	}
	if msg.status == "" {
		t.Fatal("status should describe commit result")
	}
	if msg.refresh.kind != projectInvalidationProjectData || msg.refresh.projectPath != projectPath {
		t.Fatalf("refresh = %#v, want targeted project-data invalidation for %q", msg.refresh, projectPath)
	}

	detail, err := st.GetProjectDetail(ctx, projectPath, 20)
	if err != nil {
		t.Fatalf("get detail after commit refresh: %v", err)
	}
	if detail.Summary.RepoDirty {
		t.Fatalf("expected clean repo status after commit refresh, got %#v", detail.Summary)
	}

	updated, reloadCmd := m.Update(msg)
	got := updated.(Model)
	if reloadCmd == nil {
		t.Fatal("commit action should invalidate project data")
	}
	if !got.summaryReloadInFlight[projectPath] {
		t.Fatalf("summary reload should start for %q", projectPath)
	}
	if !got.detailReloadInFlight[projectPath] {
		t.Fatalf("detail reload should start for visible project %q", projectPath)
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
	if !strings.Contains(joined, "/wt merge|remove|prune") {
		t.Fatalf("helpPanelLines() should include concrete worktree slash-command examples: %q", joined)
	}
	if !strings.Contains(joined, "/setup, /ai, /perf, /errors, /codex, /todo") || !strings.Contains(joined, "/commit, /diff, or /run") {
		t.Fatalf("helpPanelLines() should include concrete slash-command examples: %q", joined)
	}
	if !strings.Contains(joined, "interrupt busy session") {
		t.Fatalf("helpPanelLines() should keep the session interrupt hint: %q", joined)
	}
	if !strings.Contains(joined, "b  boss") || !strings.Contains(joined, "t  todo") || !strings.Contains(joined, "o/v  sort/view") || !strings.Contains(joined, "p  pin") || !strings.Contains(joined, "Ctrl+V  image") {
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
	if !strings.Contains(rendered, "ATTN") || !strings.Contains(rendered, "Summary") || !strings.Contains(rendered, "Path:") {
		t.Fatalf("View() should preserve the dashboard behind the help overlay: %q", rendered)
	}
}

func TestDispatchAICommandOpensAIStatsDialog(t *testing.T) {
	m := Model{}

	updated, cmd := m.dispatchCommand(commands.Invocation{Kind: commands.KindAIStats})
	got := updated.(Model)
	if !got.showAIStats {
		t.Fatalf("dispatchCommand(/ai) should open the AI stats dialog")
	}
	if got.status != "AI stats open. Press Esc to close" {
		t.Fatalf("status = %q, want AI stats open status", got.status)
	}
	if cmd != nil {
		t.Fatalf("dispatchCommand(/ai) should not return an async command")
	}
}

func TestDispatchPerfCommandOpensPerfDialog(t *testing.T) {
	m := Model{}

	updated, cmd := m.dispatchCommand(commands.Invocation{Kind: commands.KindPerf})
	got := updated.(Model)
	if !got.showPerf {
		t.Fatalf("dispatchCommand(/perf) should open the performance dialog")
	}
	if got.status != "Performance open. Press c to copy or Esc to close" {
		t.Fatalf("status = %q, want performance open status", got.status)
	}
	if cmd != nil {
		t.Fatalf("dispatchCommand(/perf) should not return an async command")
	}
}

func TestRenderAIStatsOverlayPreservesBackground(t *testing.T) {
	m := Model{
		projects: []model.ProjectSummary{{
			Name:                             "demo",
			Path:                             "/tmp/demo",
			Status:                           model.StatusIdle,
			PresentOnDisk:                    true,
			LatestSessionClassification:      model.ClassificationFailed,
			LatestSessionClassificationType:  model.SessionCategoryBlocked,
			LatestSessionSummary:             "Classifier failed on the latest session.",
			LatestSessionFormat:              "modern",
			LatestSessionDetectedProjectPath: "/tmp/demo",
		}},
		allProjects: []model.ProjectSummary{{
			Name:                        "demo",
			Path:                        "/tmp/demo",
			PresentOnDisk:               true,
			LatestSessionClassification: model.ClassificationFailed,
		}},
		showAIStats: true,
		width:       100,
		height:      24,
	}
	m.syncDetailViewport(false)

	rendered := ansi.Strip(m.View())
	if !strings.Contains(rendered, "AI Stats") || !strings.Contains(rendered, "Errors") || !strings.Contains(rendered, "demo") {
		t.Fatalf("View() should show the AI stats overlay content: %q", rendered)
	}
	if !strings.Contains(rendered, "Little Control Room") || !strings.Contains(rendered, "╭────╭") {
		t.Fatalf("View() should keep the dashboard visible around the AI stats overlay: %q", rendered)
	}
}

func TestRenderPerfOverlayPreservesBackground(t *testing.T) {
	m := Model{
		projects: []model.ProjectSummary{{
			Name:                             "demo",
			Path:                             "/tmp/demo",
			Status:                           model.StatusIdle,
			PresentOnDisk:                    true,
			RepoBranch:                       "master",
			LatestSessionClassification:      model.ClassificationCompleted,
			LatestSessionClassificationType:  model.SessionCategoryCompleted,
			LatestSessionSummary:             "Work appears complete for now.",
			LatestSessionFormat:              "modern",
			LatestSessionDetectedProjectPath: "/tmp/demo",
		}},
		showPerf: true,
		width:    100,
		height:   24,
		aiLatencyRecent: []aiLatencySample{{
			Name:        "Model apply",
			ProjectPath: "/tmp/demo",
			Detail:      "Codex gpt-5.4 high",
			Result:      "ok",
			Duration:    420 * time.Millisecond,
		}},
	}
	m.syncDetailViewport(false)

	rendered := ansi.Strip(m.View())
	if !strings.Contains(rendered, "Performance") || !strings.Contains(rendered, "Latency") || !strings.Contains(rendered, "Model apply") {
		t.Fatalf("View() should show the performance overlay content: %q", rendered)
	}
	if !strings.Contains(rendered, "Little Control Room") || !strings.Contains(rendered, "Repo: clean") || !strings.Contains(rendered, "Attention: 0") {
		t.Fatalf("View() should keep the dashboard visible around the performance overlay: %q", rendered)
	}
}

func TestRenderAIStatsContentHidesLocalBackendCost(t *testing.T) {
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
				InputTokens:  345,
				OutputTokens: 538,
			},
		},
	}

	cfg := config.Default()
	cfg.AIBackend = config.AIBackendCodex
	svc := service.New(cfg, st, events.NewBus(), nil)
	svc.SetSessionClassifier(classifier)

	m := New(ctx, svc)
	m.setupChecked = true
	m.setupSnapshot = aibackend.Snapshot{
		Selected: config.AIBackendCodex,
		Codex: aibackend.Status{
			Backend:       config.AIBackendCodex,
			Label:         "Codex",
			Installed:     true,
			Authenticated: true,
			Ready:         true,
			Detail:        "Logged in with ChatGPT.",
		},
	}

	rendered := ansi.Strip(m.renderAIStatsContent(76))
	if strings.Contains(rendered, "Cost:") {
		t.Fatalf("renderAIStatsContent() should hide cost for local backends: %q", rendered)
	}
	if !strings.Contains(rendered, "Billing: local provider mode") {
		t.Fatalf("renderAIStatsContent() should show local provider billing mode: %q", rendered)
	}
	if !strings.Contains(rendered, "Codex is running through its local provider path here") {
		t.Fatalf("renderAIStatsContent() should explain local backend billing semantics: %q", rendered)
	}
	if strings.Contains(rendered, "Latency") {
		t.Fatalf("renderAIStatsContent() should keep performance details out of AI stats: %q", rendered)
	}
}

func TestRenderPerfContentShowsLatencySection(t *testing.T) {
	now := time.Date(2026, time.April, 2, 17, 30, 0, 0, time.UTC)
	m := Model{
		aiLatencyInFlight: map[int64]aiLatencyOp{
			1: {
				ID:          1,
				Name:        "Embedded open",
				ProjectPath: "/tmp/demo",
				Detail:      "Codex",
				StartedAt:   now.Add(-3200 * time.Millisecond),
			},
		},
		aiLatencyRecent: []aiLatencySample{
			{
				Name:        "Model apply",
				ProjectPath: "/tmp/demo",
				Detail:      "Codex gpt-5.4 high",
				Result:      "ok",
				Duration:    420 * time.Millisecond,
			},
			{
				Name:        "Embedded viewport",
				ProjectPath: "/tmp/demo",
				Detail:      "Codex",
				Result:      "ok",
				Duration:    2300 * time.Millisecond,
			},
		},
		nowFn: func() time.Time { return now },
	}

	rendered := ansi.Strip(m.renderPerfContent(76))
	for _, want := range []string{
		"Performance",
		"Latency",
		"In flight: 1 operation(s)",
		"Embedded open",
		"Model apply",
		"Embedded viewport",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("renderPerfContent() missing %q: %q", want, rendered)
		}
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

func TestActionMsgClearsPendingGitSummary(t *testing.T) {
	m := Model{
		pendingGitSummaries: map[string]string{
			"/tmp/demo": "Committing...",
		},
		commitPreview: &service.CommitPreview{
			ProjectPath: "/tmp/demo",
			Message:     "Update repo",
		},
		commitApplying: true,
	}

	updated, cmd := m.Update(actionMsg{
		projectPath:            "/tmp/demo",
		status:                 "Committed abc12345",
		clearPendingGitSummary: true,
	})
	got := updated.(Model)
	// On success, the pending summary stays alive until the next project
	// list refresh so the spinner keeps animating instead of flashing "!".
	if got.pendingGitSummary("/tmp/demo") == "" {
		t.Fatalf("pending git summary should survive until project refresh")
	}
	if !got.pendingGitSummaryExpireNext["/tmp/demo"] {
		t.Fatalf("pending git summary should be marked for expiry on next refresh")
	}
	if got.status != "Committed abc12345" {
		t.Fatalf("status = %q, want %q", got.status, "Committed abc12345")
	}
	if cmd == nil {
		t.Fatalf("actionMsg should trigger follow-up refresh commands")
	}

	// Simulate project list refresh — pending summary should now be cleared.
	updated2, _ := got.Update(projectsMsg{})
	got2 := updated2.(Model)
	if got2.pendingGitSummary("/tmp/demo") != "" {
		t.Fatalf("pending git summary = %q, want cleared after project refresh", got2.pendingGitSummary("/tmp/demo"))
	}
}

func TestProjectSummaryMsgClearsExpiredPendingGitSummary(t *testing.T) {
	m := Model{
		pendingGitSummaries: map[string]string{
			"/tmp/demo": "Pushing...",
		},
		pendingGitSummaryExpireNext: map[string]bool{
			"/tmp/demo": true,
		},
		projects: []model.ProjectSummary{{
			Path: "/tmp/demo",
			Name: "demo",
		}},
		allProjects: []model.ProjectSummary{{
			Path: "/tmp/demo",
			Name: "demo",
		}},
		detail: model.ProjectDetail{
			Summary: model.ProjectSummary{
				Path: "/tmp/demo",
				Name: "demo",
			},
		},
	}

	updated, _ := m.Update(projectSummaryMsg{
		path:  "/tmp/demo",
		found: true,
		summary: model.ProjectSummary{
			Path: "/tmp/demo",
			Name: "demo",
		},
	})
	got := updated.(Model)
	if got.pendingGitSummary("/tmp/demo") != "" {
		t.Fatalf("pending git summary = %q, want cleared after targeted summary refresh", got.pendingGitSummary("/tmp/demo"))
	}
	if got.pendingGitSummaryExpireNext != nil {
		if got.pendingGitSummaryExpireNext["/tmp/demo"] {
			t.Fatalf("pending git summary expiry should be consumed for refreshed project")
		}
	}
}

func TestActionMsgErrorClearsPendingGitSummaryImmediately(t *testing.T) {
	m := Model{
		pendingGitSummaries: map[string]string{
			"/tmp/demo": "Committing...",
		},
		commitPreview: &service.CommitPreview{
			ProjectPath: "/tmp/demo",
			Message:     "Update repo",
		},
		commitApplying: true,
	}

	updated, _ := m.Update(actionMsg{
		projectPath:            "/tmp/demo",
		status:                 "Commit failed",
		clearPendingGitSummary: true,
		err:                    fmt.Errorf("git error"),
	})
	got := updated.(Model)
	if got.pendingGitSummary("/tmp/demo") != "" {
		t.Fatalf("pending git summary = %q, want cleared on error", got.pendingGitSummary("/tmp/demo"))
	}
}

func TestTogglePinCmdReturnsTargetedProjectRefresh(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	projectPath := "/tmp/pin-demo"
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:           projectPath,
		Name:           "pin-demo",
		Status:         model.StatusIdle,
		AttentionScore: 10,
		InScope:        true,
		UpdatedAt:      time.Now().UTC(),
	}); err != nil {
		t.Fatalf("upsert project: %v", err)
	}

	svc := service.New(config.Default(), st, events.NewBus(), nil)
	m := Model{ctx: ctx, svc: svc}

	cmd := m.togglePinCmd(projectPath)
	if cmd == nil {
		t.Fatal("togglePinCmd() should return a command")
	}
	raw := cmd()
	msg, ok := raw.(actionMsg)
	if !ok {
		t.Fatalf("cmd() message type = %T, want actionMsg", raw)
	}
	if msg.projectPath != projectPath {
		t.Fatalf("projectPath = %q, want %q", msg.projectPath, projectPath)
	}
	if msg.refresh.kind != projectInvalidationProjectData || msg.refresh.projectPath != projectPath {
		t.Fatalf("refresh = %#v, want targeted project-data invalidation for %q", msg.refresh, projectPath)
	}
}

func TestSnoozeCmdReturnsTargetedProjectRefresh(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	projectPath := "/tmp/snooze-demo"
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:           projectPath,
		Name:           "snooze-demo",
		Status:         model.StatusIdle,
		AttentionScore: 10,
		InScope:        true,
		UpdatedAt:      time.Now().UTC(),
	}); err != nil {
		t.Fatalf("upsert project: %v", err)
	}

	svc := service.New(config.Default(), st, events.NewBus(), nil)
	m := Model{ctx: ctx, svc: svc}

	cmd := m.snoozeCmd(projectPath, time.Hour)
	if cmd == nil {
		t.Fatal("snoozeCmd() should return a command")
	}
	raw := cmd()
	msg, ok := raw.(actionMsg)
	if !ok {
		t.Fatalf("cmd() message type = %T, want actionMsg", raw)
	}
	if msg.projectPath != projectPath {
		t.Fatalf("projectPath = %q, want %q", msg.projectPath, projectPath)
	}
	if msg.refresh.kind != projectInvalidationProjectData || msg.refresh.projectPath != projectPath {
		t.Fatalf("refresh = %#v, want targeted project-data invalidation for %q", msg.refresh, projectPath)
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

func TestDiffPreviewMsgNoChangesKeepsDiffScreenOpen(t *testing.T) {
	m := Model{
		diffView: newDiffViewState("/tmp/demo", "demo"),
		width:    100,
		height:   24,
	}

	updated, cmd := m.Update(diffPreviewMsg{
		err: service.NoDiffChangesError{
			ProjectPath: "/tmp/demo",
			ProjectName: "demo",
			Branch:      "master",
		},
	})
	got := updated.(Model)

	if cmd != nil {
		t.Fatalf("no-diff result should not queue another command")
	}
	if got.diffView == nil {
		t.Fatalf("no-diff result should keep the diff screen open")
	}
	if got.diffView.loading {
		t.Fatalf("no-diff result should stop loading")
	}
	if got.diffView.preview == nil {
		t.Fatalf("no-diff result should keep preview metadata for the empty state")
	}
	if got.status != "Worktree clean. Esc close" {
		t.Fatalf("status = %q, want clean-worktree status", got.status)
	}

	rendered := ansi.Strip(got.View())
	if !strings.Contains(rendered, "Clean worktree") {
		t.Fatalf("clean diff screen should explain the empty state: %q", rendered)
	}
	if !strings.Contains(rendered, "demo has no staged, unstaged, or untracked changes") {
		t.Fatalf("clean diff screen should show the no-diff warning in the content pane: %q", rendered)
	}
	if strings.Contains(rendered, "Enter/Tab") || strings.Contains(rendered, "unified") {
		t.Fatalf("clean diff screen should not show interactive diff controls: %q", rendered)
	}
}

func TestDiffPreviewMsgNoChangesRefreshesProjectStatus(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	projectPath := filepath.Join(t.TempDir(), "repo")
	runTUITestGit(t, "", "init", projectPath)
	runTUITestGit(t, projectPath, "config", "user.name", "Little Control Room Tests")
	runTUITestGit(t, projectPath, "config", "user.email", "tests@example.com")
	if err := os.WriteFile(filepath.Join(projectPath, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write README.md: %v", err)
	}
	runTUITestGit(t, projectPath, "add", "README.md")
	runTUITestGit(t, projectPath, "commit", "-m", "initial commit")

	st, err := store.Open(filepath.Join(t.TempDir(), "little-control-room.sqlite"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	if err := st.UpsertProjectState(ctx, model.ProjectState{
		Path:          projectPath,
		Name:          "repo",
		PresentOnDisk: true,
		InScope:       true,
		RepoBranch:    "master",
		RepoDirty:     true,
		UpdatedAt:     time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed stale project state: %v", err)
	}

	svc := service.New(config.Default(), st, events.NewBus(), nil)
	m := Model{
		ctx: ctx,
		svc: svc,
		projects: []model.ProjectSummary{{
			Path: projectPath,
			Name: "repo",
		}},
		allProjects: []model.ProjectSummary{{
			Path: projectPath,
			Name: "repo",
		}},
		selected: 0,
		detail: model.ProjectDetail{
			Summary: model.ProjectSummary{
				Path: projectPath,
				Name: "repo",
			},
		},
		diffView: newDiffViewState(projectPath, "repo"),
	}

	updated, cmd := m.Update(diffPreviewMsg{
		err: service.NoDiffChangesError{
			ProjectPath: projectPath,
			ProjectName: "repo",
			Branch:      "master",
		},
	})
	got := updated.(Model)

	if cmd == nil {
		t.Fatal("no-diff result should refresh project status")
	}
	msg := cmd()
	refreshMsg, ok := msg.(projectStatusRefreshedMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want projectStatusRefreshedMsg", msg)
	}
	if refreshMsg.err != nil {
		t.Fatalf("refresh err = %v", refreshMsg.err)
	}

	detail, err := st.GetProjectDetail(ctx, projectPath, 20)
	if err != nil {
		t.Fatalf("get detail after refresh: %v", err)
	}
	if detail.Summary.RepoDirty {
		t.Fatalf("expected refresh to clear stale dirty state")
	}

	updated, cmd = got.Update(refreshMsg)
	got = updated.(Model)
	if cmd == nil {
		t.Fatal("refresh completion should invalidate project data")
	}
	if !got.summaryReloadInFlight[projectPath] {
		t.Fatalf("summary reload should start for %q", projectPath)
	}
	if !got.detailReloadInFlight[projectPath] {
		t.Fatalf("detail reload should start for visible project %q", projectPath)
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
	if strings.Contains(rendered, "M   modified") {
		t.Fatalf("selected diff row should not add extra padding before the state label: %q", rendered)
	}
	if !strings.Contains(rendered, "M modified") {
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
	// Continuous scroll renders all files up front, so both should be cached.
	if got := len(m.diffView.renderCache); got != 2 {
		t.Fatalf("initial render cache size = %d, want 2 (continuous scroll caches all files)", got)
	}
	if _, ok := m.diffView.renderCache[cacheKey0]; !ok {
		t.Fatalf("first file should be in the render cache")
	}

	firstContinuous := m.diffView.continuousContent
	m.moveDiffSelectionTo(1)
	if got := len(m.diffView.renderCache); got != 2 {
		t.Fatalf("cache size after selecting second file = %d, want 2", got)
	}

	m.moveDiffSelectionTo(0)
	if got := len(m.diffView.renderCache); got != 2 {
		t.Fatalf("cache size after revisiting first file = %d, want 2", got)
	}
	if m.diffView.continuousContent != firstContinuous {
		t.Fatalf("revisiting a file should reuse the cached continuous content")
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
	if !strings.Contains(ansi.Strip(m.diffView.continuousContent), "Before") {
		t.Fatalf("default diff renderer should start in side-by-side mode: %q", ansi.Strip(m.diffView.continuousContent))
	}

	updated, _ := m.updateDiffMode(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	got := updated.(Model)
	if got.diffView.mode != diffRenderModeUnified {
		t.Fatalf("toggling mode should switch to unified, got %s", got.diffView.mode)
	}
	if !strings.Contains(got.status, "unified") {
		t.Fatalf("status should mention unified mode after toggling: %q", got.status)
	}
	unified := ansi.Strip(got.diffView.continuousContent)
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
	if !strings.Contains(ansi.Strip(got.diffView.continuousContent), "Before") {
		t.Fatalf("side-by-side mode should restore the paired columns: %q", ansi.Strip(got.diffView.continuousContent))
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

func TestCommitPreviewMsgLogsCommitMessageFallbackError(t *testing.T) {
	m := Model{
		nowFn: func() time.Time {
			return time.Date(2026, 4, 6, 10, 0, 0, 0, time.UTC)
		},
		allProjects: []model.ProjectSummary{{
			Name: "demo",
			Path: "/tmp/demo",
		}},
		width:  100,
		height: 24,
	}

	updated, _ := m.Update(commitPreviewMsg{
		preview: service.CommitPreview{
			Intent:             service.GitActionCommit,
			ProjectPath:        "/tmp/demo",
			ProjectName:        "demo",
			Branch:             "master",
			StageMode:          service.GitStageAllChanges,
			StateHash:          "state-1",
			Message:            "Update demo",
			CommitMessageError: "model mlx-community/Qwen3.5-35B-A3B-4bit: EOF",
		},
		projectPath: "/tmp/demo",
		intent:      service.GitActionCommit,
	})
	got := updated.(Model)
	if got.commitPreview == nil {
		t.Fatalf("commit preview should be stored when the preview message arrives")
	}
	if len(got.errorLogEntries) != 1 {
		t.Fatalf("error log count = %d, want 1", len(got.errorLogEntries))
	}
	entry := got.errorLogEntries[0]
	if entry.Status != "Commit message fallback used" {
		t.Fatalf("error log status = %q", entry.Status)
	}
	if entry.Message != "model mlx-community/Qwen3.5-35B-A3B-4bit: EOF" {
		t.Fatalf("error log message = %q", entry.Message)
	}
	if entry.RootCause != "EOF" {
		t.Fatalf("error log root cause = %q", entry.RootCause)
	}
	if !strings.Contains(got.status, "AI fallback") || !strings.Contains(got.status, "/errors") {
		t.Fatalf("status = %q, want AI fallback hint with /errors", got.status)
	}
}

func TestRenderCommitPreviewContentShowsAIFallbackStatusInline(t *testing.T) {
	m := Model{
		commitPreview: &service.CommitPreview{
			Intent:             service.GitActionCommit,
			ProjectName:        "demo",
			ProjectPath:        "/tmp/demo",
			Branch:             "master",
			StageMode:          service.GitStageStagedOnly,
			Message:            "Update demo",
			CommitMessageError: "commit assistant not configured for selected AI backend",
		},
		width:  100,
		height: 24,
	}

	rendered := ansi.Strip(m.renderCommitPreviewContent(72, 8))
	if !strings.Contains(rendered, "AI: Fallback subject used; /errors has details") {
		t.Fatalf("renderCommitPreviewContent() should show inline AI fallback guidance: %q", rendered)
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
