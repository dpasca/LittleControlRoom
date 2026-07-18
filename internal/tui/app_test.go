package tui

import (
	"context"
	"encoding/json"
	"errors"
	tea "github.com/charmbracelet/bubbletea"
	"lcroom/internal/brand"
	"lcroom/internal/codexapp"
	"lcroom/internal/config"
	"lcroom/internal/events"
	"lcroom/internal/model"
	"lcroom/internal/service"
	"lcroom/internal/sessionclassify"
	"lcroom/internal/store"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type fakeCodexSession struct {
	projectPath           string
	snapshot              codexapp.Snapshot
	snapshotCalls         int
	trySnapshotCalls      int
	stateSnapshotCalls    int
	tryStateSnapshotCalls int
	trySnapshotFn         func(*fakeCodexSession) (codexapp.Snapshot, bool)
	tryStateSnapshotFn    func(*fakeCodexSession) (codexapp.Snapshot, bool)
	submitted             []string
	submissions           []codexapp.Submission
	decisions             []codexapp.ApprovalDecision
	toolAnswers           []map[string][]string
	elicitations          []fakeElicitationResponse
	statusCalls           int
	permissionCalls       int
	permissionLevels      []string
	showGoalCalls         int
	clearGoalCalls        int
	goalSetObjective      string
	goalSetBudget         *int64
	compactCalls          int
	reviewCalls           int
	interrupted           bool
	refreshCalls          int
	refreshBusyFn         func(*fakeCodexSession) error
	compactFn             func(*fakeCodexSession) error
	reviewFn              func(*fakeCodexSession) error
	models                []codexapp.ModelOption
	modelStages           []struct {
		Model     string
		Reasoning string
	}
	modelProviderStages []struct {
		Provider  string
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

func TestNewCachesStableSettingsForUI(t *testing.T) {
	cfg := config.Default()
	cfg.ConfigPath = filepath.Join(t.TempDir(), "config.toml")
	cfg.DataDir = filepath.Join(t.TempDir(), "data")
	cfg.DBPath = filepath.Join(cfg.DataDir, "custom.sqlite")
	cfg.CodexHome = filepath.Join(t.TempDir(), "codex-home")
	cfg.ActiveThreshold = 17 * time.Second
	cfg.StuckThreshold = 43 * time.Second
	cfg.ExcludeProjectPatterns = []string{"vendor"}

	m := New(context.Background(), service.New(cfg, nil, events.NewBus(), nil))

	if m.settingsBaseline == nil {
		t.Fatal("settings baseline was not cached")
	}
	if got := m.currentConfigPath(); got != cfg.ConfigPath {
		t.Fatalf("config path = %q, want %q", got, cfg.ConfigPath)
	}
	if got := m.appDataDir(); got != cfg.DataDir {
		t.Fatalf("app data dir = %q, want %q", got, cfg.DataDir)
	}
	if got := m.embeddedLaunchDBPath(); got != cfg.DBPath {
		t.Fatalf("app DB path = %q, want %q", got, cfg.DBPath)
	}
	if got := m.codexHome(); got != cfg.CodexHome {
		t.Fatalf("codex home = %q, want %q", got, cfg.CodexHome)
	}
	if got := m.assessmentStallThreshold(); got != sessionclassify.EffectiveAssessmentStallThreshold(cfg.ActiveThreshold, cfg.StuckThreshold) {
		t.Fatalf("assessment stall threshold = %s", got)
	}
	if got := strings.Join(m.excludeProjectPatterns, ","); got != "vendor" {
		t.Fatalf("exclude patterns = %q", got)
	}
	if m.sortMode != sortByRecent {
		t.Fatalf("default sort mode = %q, want %q", m.sortMode, sortByRecent)
	}
}

func TestNewWithCodexManagerUsesSharedLiveSessionManager(t *testing.T) {
	manager := codexapp.NewManager()
	m := NewWithCodexManager(
		context.Background(),
		service.New(config.Default(), nil, events.NewBus(), nil),
		manager,
	)

	if m.codexManager != manager {
		t.Fatal("TUI should use the live session manager shared with the mobile server")
	}
}

func TestBareModelConfigSaveDoesNotWriteDefaultUserConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cmd := Model{}.savePrivacyModeCmd(true)
	if cmd == nil {
		t.Fatal("savePrivacyModeCmd() returned nil")
	}
	msg := cmd()
	saved, ok := msg.(privacyModeSavedMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want privacyModeSavedMsg", msg)
	}
	if saved.err == nil {
		t.Fatal("bare model save should fail without an initialized config path")
	}
	defaultPath := filepath.Join(home, brand.DataDirName, brand.ConfigFileName)
	if _, err := os.Stat(defaultPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("default config path was touched; stat err = %v", err)
	}
}

func TestSavePrivacyModeCmdAppliesToSharedService(t *testing.T) {
	cfg := config.Default()
	cfg.ConfigPath = filepath.Join(t.TempDir(), "config.toml")
	svc := service.New(cfg, nil, events.NewBus(), nil)
	m := New(context.Background(), svc)

	msg, ok := m.savePrivacyModeCmd(true)().(privacyModeSavedMsg)
	if !ok {
		t.Fatal("savePrivacyModeCmd() did not return privacyModeSavedMsg")
	}
	if msg.err != nil {
		t.Fatalf("save privacy mode: %v", msg.err)
	}
	if !svc.Config().PrivacyMode {
		t.Fatal("privacy mode was not applied to the shared service")
	}
}

func TestSavePrivacyModeCmdReportsLivePolicyApplyFailure(t *testing.T) {
	cfg := config.Default()
	cfg.ConfigPath = filepath.Join(t.TempDir(), "config.toml")
	st, err := store.Open(filepath.Join(t.TempDir(), "closed.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	svc := service.New(cfg, st, events.NewBus(), nil)
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	settings := config.EditableSettingsFromAppConfig(cfg)
	m := Model{
		svc:                svc,
		settingsBaseline:   &settings,
		settingsConfigPath: cfg.ConfigPath,
	}

	msg, ok := m.savePrivacyModeCmd(true)().(privacyModeSavedMsg)
	if !ok {
		t.Fatal("savePrivacyModeCmd() did not return privacyModeSavedMsg")
	}
	if msg.err != nil {
		t.Fatalf("config save unexpectedly failed: %v", msg.err)
	}
	if msg.applyErr == nil {
		t.Fatal("live TODO policy apply failure was not reported")
	}
}

func TestAssessmentStallThresholdUsesCachedSettingsWithoutService(t *testing.T) {
	settings := config.EditableSettingsFromAppConfig(config.Default())
	settings.ActiveThreshold = 9 * time.Second
	settings.StuckThreshold = 31 * time.Second

	got := (Model{settingsBaseline: &settings}).assessmentStallThreshold()
	want := sessionclassify.EffectiveAssessmentStallThreshold(settings.ActiveThreshold, settings.StuckThreshold)
	if got != want {
		t.Fatalf("assessment stall threshold = %s, want %s", got, want)
	}
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
	s.stateSnapshotCalls++
	snapshot := s.snapshot
	snapshot.ProjectPath = s.projectPath
	return snapshot
}

func (s *fakeCodexSession) TryStateSnapshot() (codexapp.Snapshot, bool) {
	s.tryStateSnapshotCalls++
	if s.tryStateSnapshotFn != nil {
		return s.tryStateSnapshotFn(s)
	}
	return s.StateSnapshot(), true
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

func (s *fakeCodexSession) ShowPermissions() error {
	s.permissionCalls++
	s.snapshot.Entries = append(s.snapshot.Entries, codexapp.TranscriptEntry{
		Kind: codexapp.TranscriptStatus,
		Text: "Embedded LCAgent permissions\ncurrent: Low\nMedium: command execution no longer uses the Low allowlist.",
	})
	return nil
}

func (s *fakeCodexSession) SetPermissionLevel(level string) error {
	s.permissionLevels = append(s.permissionLevels, strings.TrimSpace(level))
	s.snapshot.PermissionLevel = strings.TrimSpace(level)
	return nil
}

func (s *fakeCodexSession) ShowGoal() error {
	s.showGoalCalls++
	text := "Embedded Codex goal\nstatus: none"
	if s.snapshot.Goal != nil {
		text = strings.Join([]string{
			"Embedded Codex goal",
			"objective: " + s.snapshot.Goal.Objective,
			"status: " + string(s.snapshot.Goal.Status),
		}, "\n")
	}
	s.snapshot.Entries = append(s.snapshot.Entries, codexapp.TranscriptEntry{
		Kind: codexapp.TranscriptStatus,
		Text: text,
	})
	return nil
}

func (s *fakeCodexSession) SetGoal(objective string, tokenBudget *int64) error {
	s.goalSetObjective = strings.TrimSpace(objective)
	if tokenBudget == nil {
		s.goalSetBudget = nil
	} else {
		copied := *tokenBudget
		s.goalSetBudget = &copied
	}
	s.snapshot.Goal = &codexapp.ThreadGoal{
		ThreadID:    s.snapshot.ThreadID,
		Objective:   s.goalSetObjective,
		Status:      codexapp.ThreadGoalStatusActive,
		TokenBudget: s.goalSetBudget,
	}
	return nil
}

func (s *fakeCodexSession) PauseGoal() error {
	if s.snapshot.Goal != nil {
		s.snapshot.Goal.Status = codexapp.ThreadGoalStatusPaused
	}
	return nil
}

func (s *fakeCodexSession) ResumeGoal() error {
	if s.snapshot.Goal != nil {
		s.snapshot.Goal.Status = codexapp.ThreadGoalStatusActive
	}
	return nil
}

func (s *fakeCodexSession) ClearGoal() error {
	s.clearGoalCalls++
	s.snapshot.Goal = nil
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

func (s *fakeCodexSession) StageModelProviderOverride(provider, model, reasoningEffort string) error {
	s.modelProviderStages = append(s.modelProviderStages, struct {
		Provider  string
		Model     string
		Reasoning string
	}{
		Provider:  provider,
		Model:     model,
		Reasoning: reasoningEffort,
	})
	s.snapshot.ModelProvider = provider
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
		// tea.Sequence returns an unexported named []tea.Cmd message. Reflect
		// over that shape so tests exercise its children in the same order as
		// Bubble Tea without depending on the private type name.
		value := reflect.ValueOf(msg)
		cmdType := reflect.TypeOf((tea.Cmd)(nil))
		if value.IsValid() && value.Kind() == reflect.Slice && value.Type().Elem() == cmdType {
			var out []tea.Msg
			for i := 0; i < value.Len(); i++ {
				child, _ := value.Index(i).Interface().(tea.Cmd)
				out = append(out, collectCmdMsgs(child)...)
			}
			return out
		}
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
