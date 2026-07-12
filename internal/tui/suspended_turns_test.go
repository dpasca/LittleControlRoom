package tui

import (
	"fmt"
	"testing"
	"time"

	"lcroom/internal/codexapp"
	"lcroom/internal/model"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
)

func TestMergeSuspendedTurnChoicesKeepsAllCapturedIntentsAheadOfFallbackLimit(t *testing.T) {
	now := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)
	intents := make([]codexapp.RestartIntent, 0, 10)
	for i := 0; i < 10; i++ {
		intents = append(intents, codexapp.RestartIntent{
			Provider:     codexapp.ProviderCodex,
			ProjectPath:  fmt.Sprintf("/tmp/captured-%02d", i),
			SessionID:    fmt.Sprintf("thread-%02d", i),
			ActiveTurnID: fmt.Sprintf("turn-%02d", i),
			CapturedAt:   now.Add(time.Duration(i) * time.Minute),
		})
	}
	projects := []model.ProjectSummary{{
		Path:                     "/tmp/fallback",
		Name:                     "fallback",
		PresentOnDisk:            true,
		InScope:                  true,
		LatestSessionID:          "codex:fallback",
		LatestSessionFormat:      "modern",
		LatestSessionLastEventAt: now,
		LatestTurnStateKnown:     true,
		LatestTurnCompleted:      false,
	}}

	choices := mergeSuspendedTurnResumeChoices(projects, intents, suspendedTurnResumeChoiceLimit)
	if len(choices) != len(intents) {
		t.Fatalf("choices len = %d, want all %d captured intents even above display limit", len(choices), len(intents))
	}
	for _, choice := range choices {
		if !choice.CapturedOnQuit || choice.ActiveTurnID == "" {
			t.Fatalf("captured choice lost restart metadata: %#v", choice)
		}
	}
}

func TestSuspendedTurnSkipKeepsCapturedRestartIntentForLater(t *testing.T) {
	dataDir := t.TempDir()
	intent := codexapp.RestartIntent{
		Provider:     codexapp.ProviderLCAgent,
		ProjectPath:  "/tmp/demo",
		SessionID:    "thread-demo",
		ActiveTurnID: "run-demo",
		CapturedAt:   time.Now(),
	}
	if err := codexapp.WriteRestartIntents(dataDir, []codexapp.RestartIntent{intent}); err != nil {
		t.Fatal(err)
	}
	m := Model{
		appDataDirPath: dataDir,
		suspendedTurnDialog: &suspendedTurnResumeDialogState{
			Choices: []suspendedTurnResumeChoice{{
				ProjectPath:    intent.ProjectPath,
				Provider:       intent.Provider,
				SessionID:      intent.SessionID,
				CapturedOnQuit: true,
			}},
			Selected: suspendedTurnResumeSelectionResume,
		},
	}

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	got := updated.(Model)
	if got.suspendedTurnDialog != nil || cmd != nil {
		t.Fatalf("skip should close dialog without consuming captured intent")
	}
	loaded, err := codexapp.ReadRestartIntents(dataDir)
	if err != nil || len(loaded) != 1 || loaded[0].Key() != intent.Key() {
		t.Fatalf("restart intents after skip = %#v, err=%v", loaded, err)
	}
}

func TestBuildSuspendedTurnResumeChoicesFiltersAndSorts(t *testing.T) {
	now := time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC)
	projects := []model.ProjectSummary{
		{
			Path:                     "/tmp/completed",
			Name:                     "completed",
			PresentOnDisk:            true,
			InScope:                  true,
			LatestSessionID:          "codex:done",
			LatestSessionFormat:      "modern",
			LatestSessionLastEventAt: now.Add(-time.Minute),
			LatestTurnStateKnown:     true,
			LatestTurnCompleted:      true,
		},
		{
			Path:                     "/tmp/missing",
			Name:                     "missing",
			InScope:                  true,
			LatestSessionID:          "codex:missing",
			LatestSessionFormat:      "modern",
			LatestSessionLastEventAt: now,
			LatestTurnStateKnown:     true,
		},
		{
			Path:                     "/tmp/old",
			Name:                     "old",
			PresentOnDisk:            true,
			InScope:                  true,
			LatestSessionID:          "codex:old",
			LatestSessionFormat:      "modern",
			LatestSessionLastEventAt: now.Add(-2 * time.Hour),
			LatestTurnStateKnown:     true,
			LatestTurnCompleted:      false,
		},
		{
			Path:                     "/tmp/new",
			Name:                     "new",
			PresentOnDisk:            true,
			InScope:                  true,
			LatestSessionID:          "opencode:new",
			LatestSessionFormat:      "opencode_db",
			LatestSessionLastEventAt: now.Add(-5 * time.Minute),
			LatestTurnStateKnown:     true,
			LatestTurnCompleted:      false,
		},
	}

	choices := buildSuspendedTurnResumeChoices(projects, 10)
	if len(choices) != 2 {
		t.Fatalf("choices len = %d, want 2: %#v", len(choices), choices)
	}
	if choices[0].ProjectPath != "/tmp/new" || choices[0].Provider != codexapp.ProviderOpenCode || choices[0].SessionID != "new" {
		t.Fatalf("first choice = %#v, want newest OpenCode raw session", choices[0])
	}
	if choices[1].ProjectPath != "/tmp/old" || choices[1].Provider != codexapp.ProviderCodex || choices[1].SessionID != "old" {
		t.Fatalf("second choice = %#v, want older Codex raw session", choices[1])
	}
}

func TestSuspendedTurnResumeDialogEnterOpensChoicesInBackground(t *testing.T) {
	var requests []codexapp.LaunchRequest
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		requests = append(requests, req)
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider:       req.Provider.Normalized(),
				ProjectPath:    req.ProjectPath,
				ThreadID:       req.ResumeID,
				Started:        true,
				LastActivityAt: time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC),
			},
		}, nil
	})
	m := Model{
		codexManager: manager,
		codexInput:   newCodexTextarea(),
		width:        100,
		height:       24,
	}

	updated, cmd := m.Update(suspendedTurnResumeChoicesMsg{choices: []suspendedTurnResumeChoice{
		{ProjectPath: "/tmp/a", ProjectName: "a", Provider: codexapp.ProviderCodex, SessionID: "cx-a", ActiveTurnID: "turn-a", CapturedOnQuit: true},
		{ProjectPath: "/tmp/b", ProjectName: "b", Provider: codexapp.ProviderClaudeCode, SessionID: "cc-b"},
	}})
	if cmd != nil {
		t.Fatalf("choices msg should not launch until the user confirms")
	}
	got := updated.(Model)
	if got.suspendedTurnDialog == nil {
		t.Fatalf("suspended turn dialog not shown")
	}

	updated, cmd = got.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if got.suspendedTurnDialog != nil {
		t.Fatalf("dialog should close after confirm")
	}
	msgs := collectCmdMsgs(cmd)
	if len(msgs) != 2 {
		t.Fatalf("resume command messages = %d, want 2: %#v", len(msgs), msgs)
	}
	if len(requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(requests))
	}
	if requests[0].ProjectPath != "/tmp/a" || requests[0].Provider != codexapp.ProviderCodex || requests[0].ResumeID != "cx-a" {
		t.Fatalf("first request = %#v", requests[0])
	}
	if !requests[0].ContinueInterruptedTurn || requests[0].InterruptedTurnID != "turn-a" || requests[0].Prompt != suspendedTurnContinuationPrompt {
		t.Fatalf("captured first request did not start an explicit continuation: %#v", requests[0])
	}
	if requests[1].ProjectPath != "/tmp/b" || requests[1].Provider != codexapp.ProviderClaudeCode || requests[1].ResumeID != "cc-b" {
		t.Fatalf("second request = %#v", requests[1])
	}
	if requests[1].ContinueInterruptedTurn || requests[1].Prompt != "" {
		t.Fatalf("artifact-only second request should reopen without injecting a prompt: %#v", requests[1])
	}
	if got.codexVisibleProject != "" {
		t.Fatalf("confirmed startup resumes should stay hidden, visible project = %q", got.codexVisibleProject)
	}
	for _, msg := range msgs {
		updated, _ = got.Update(msg)
		got = updated.(Model)
	}
	if got.codexVisibleProject != "" {
		t.Fatalf("completed startup resumes should stay hidden, visible project = %q", got.codexVisibleProject)
	}
	if got.codexInput.Focused() {
		t.Fatalf("completed startup resumes should not focus the embedded composer")
	}
}

func TestSuspendedTurnHiddenPendingQuestionUpdateStaysHidden(t *testing.T) {
	session := &fakeCodexSession{
		projectPath: "/tmp/a",
		snapshot: codexapp.Snapshot{
			Provider:    codexapp.ProviderCodex,
			ProjectPath: "/tmp/a",
			ThreadID:    "cx-a",
			Started:     true,
			Status:      "Waiting for input",
			PendingToolInput: &codexapp.ToolInputRequest{
				ID: "tool-1",
				Questions: []codexapp.ToolInputQuestion{{
					ID:       "continue",
					Question: "Continue?",
				}},
			},
			LastActivityAt: time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC),
		},
	}
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		return session, nil
	})
	if _, _, err := manager.Open(codexapp.LaunchRequest{
		ProjectPath: "/tmp/a",
		Provider:    codexapp.ProviderCodex,
		ResumeID:    "cx-a",
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	m := Model{
		codexManager: manager,
		codexPendingOpen: &codexPendingOpenState{
			projectPath: "/tmp/a",
			provider:    codexapp.ProviderCodex,
			hideOnOpen:  true,
		},
		codexInput:    newCodexTextarea(),
		codexViewport: viewport.New(0, 0),
		width:         100,
		height:        24,
	}

	updated, _ := m.applyCodexUpdateMsg(codexUpdateMsg{projectPath: "/tmp/a"})
	got := updated.(Model)
	if got.codexPendingOpen != nil {
		t.Fatalf("codexPendingOpen = %#v, want cleared once hidden startup resume settles", got.codexPendingOpen)
	}
	if got.codexVisibleProject != "" {
		t.Fatalf("hidden startup resume should not reveal on pending input, visible project = %q", got.codexVisibleProject)
	}
	if got.codexInput.Focused() {
		t.Fatalf("hidden startup resume should not focus the embedded composer")
	}
	if got.questionNotify == nil || got.questionNotify.ProjectPath != "/tmp/a" {
		t.Fatalf("question notification = %#v, want hidden pending input notification", got.questionNotify)
	}
}

func TestProjectsMsgWaitsForProviderNeutralSuspendedTurnLoad(t *testing.T) {
	now := time.Date(2026, 5, 21, 12, 5, 0, 0, time.UTC)
	project := model.ProjectSummary{
		Path:                     "/tmp/roman",
		Name:                     "roman",
		PresentOnDisk:            true,
		InScope:                  true,
		LatestSessionSource:      model.SessionSourceLCAgent,
		LatestSessionFormat:      "lcagent_jsonl",
		LatestSessionID:          "lca_roman",
		LatestSessionLastEventAt: now.Add(-2 * time.Minute),
		LatestTurnStartedAt:      now.Add(-65 * time.Minute),
		LatestTurnStateKnown:     true,
		LatestTurnCompleted:      false,
	}
	m := Model{}

	updated, _ := m.Update(projectsMsg{projects: []model.ProjectSummary{project}})
	got := updated.(Model)
	if got.startupScanCompleted {
		t.Fatalf("startup scan should not be marked complete")
	}
	if got.suspendedTurnChecked {
		t.Fatalf("projects cache should not race the provider-neutral restart intent load")
	}
	if got.suspendedTurnDialog != nil {
		t.Fatalf("projects cache should not open the dialog before restart intents load")
	}

	choices := buildSuspendedTurnResumeChoices([]model.ProjectSummary{project}, suspendedTurnResumeChoiceLimit)
	updated, _ = got.Update(suspendedTurnResumeChoicesMsg{choices: choices})
	got = updated.(Model)
	if got.suspendedTurnDialog == nil || len(got.suspendedTurnDialog.Choices) != 1 || got.suspendedTurnDialog.Choices[0].SessionID != "lca_roman" {
		t.Fatalf("loaded suspended turn dialog = %#v, want lca_roman", got.suspendedTurnDialog)
	}
}

func TestSuspendedTurnResumeDialogEscSuppressesPersistedTimer(t *testing.T) {
	now := time.Date(2026, 5, 21, 12, 5, 0, 0, time.UTC)
	choice := suspendedTurnResumeChoice{
		ProjectPath: "/tmp/roman",
		ProjectName: "roman",
		Provider:    codexapp.ProviderLCAgent,
		SessionID:   "lca_roman",
	}
	m := Model{
		startupScanCompleted: true,
		suspendedTurnDialog: &suspendedTurnResumeDialogState{
			Choices:  []suspendedTurnResumeChoice{choice},
			Selected: suspendedTurnResumeSelectionResume,
		},
	}

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd != nil {
		t.Fatalf("esc should not launch resume command")
	}
	got := updated.(Model)
	if got.suspendedTurnDialog != nil {
		t.Fatalf("dialog should close after esc")
	}

	project := model.ProjectSummary{
		Path:                     choice.ProjectPath,
		PresentOnDisk:            true,
		LatestSessionSource:      model.SessionSourceLCAgent,
		LatestSessionFormat:      "lcagent_jsonl",
		LatestSessionID:          choice.SessionID,
		LatestSessionLastEventAt: now.Add(-2 * time.Minute),
		LatestTurnStartedAt:      now.Add(-65 * time.Minute),
		LatestTurnStateKnown:     true,
		LatestTurnCompleted:      false,
	}
	label, tag, live := got.projectAgentDisplay(project, now)
	if live {
		t.Fatalf("projectAgentDisplay() live = true, want false after skipped startup resume")
	}
	if label != "LA" || tag != "LA" {
		t.Fatalf("projectAgentDisplay() label/tag = %q/%q, want LA/LA", label, tag)
	}
}
