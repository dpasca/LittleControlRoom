package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"lcroom/internal/codexapp"
	"lcroom/internal/model"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

type suspendedTurnCommandProbe struct {
	cmd       tea.Cmd
	remaining int
}

func (p *suspendedTurnCommandProbe) Init() tea.Cmd { return p.cmd }

func (p *suspendedTurnCommandProbe) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if _, ok := msg.(codexSessionOpenedMsg); !ok {
		return p, nil
	}
	p.remaining--
	if p.remaining <= 0 {
		return p, tea.Quit
	}
	return p, nil
}

func (p *suspendedTurnCommandProbe) View() string { return "" }

func TestBuildRestartIntentResumeChoicesKeepsAllCapturedIntentsAndIgnoresArtifacts(t *testing.T) {
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

	choices := buildRestartIntentResumeChoices(projects, intents)
	if len(choices) != len(intents) {
		t.Fatalf("choices len = %d, want all %d captured intents even above display limit", len(choices), len(intents))
	}
	for _, choice := range choices {
		if !choice.CapturedOnQuit || choice.ActiveTurnID == "" {
			t.Fatalf("captured choice lost restart metadata: %#v", choice)
		}
	}
	if choices := buildRestartIntentResumeChoices(projects, nil); len(choices) != 0 {
		t.Fatalf("artifact-only unfinished project produced restart choices: %#v", choices)
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

func TestBuildRestartIntentResumeChoicesUsesProjectMetadataWithoutPromotingArtifacts(t *testing.T) {
	now := time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC)
	projects := []model.ProjectSummary{{
		Path:                     "/tmp/saved",
		Name:                     "saved display name",
		LatestSessionSummary:     "saved summary",
		LatestSessionLastEventAt: now,
	}, {
		Path:                     "/tmp/artifact-only",
		Name:                     "artifact only",
		PresentOnDisk:            true,
		InScope:                  true,
		LatestSessionID:          "codex:false-positive",
		LatestSessionFormat:      "modern",
		LatestSessionLastEventAt: now.Add(time.Minute),
		LatestTurnStateKnown:     true,
		LatestTurnCompleted:      false,
	}}
	intents := []codexapp.RestartIntent{{
		Provider:     codexapp.ProviderCodex,
		ProjectPath:  "/tmp/saved",
		SessionID:    "saved-thread",
		ActiveTurnID: "saved-turn",
		CapturedAt:   now.Add(-time.Minute),
	}}

	choices := buildRestartIntentResumeChoices(projects, intents)
	if len(choices) != 1 {
		t.Fatalf("choices len = %d, want only the journaled session: %#v", len(choices), choices)
	}
	choice := choices[0]
	if choice.ProjectPath != "/tmp/saved" || choice.ProjectName != "saved display name" || choice.Summary != "saved summary" || choice.SessionID != "saved-thread" || !choice.CapturedOnQuit {
		t.Fatalf("journaled choice = %#v", choice)
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
		{ProjectPath: "/tmp/b", ProjectName: "b", Provider: codexapp.ProviderClaudeCode, SessionID: "cc-b", ActiveTurnID: "turn-b", CapturedOnQuit: true},
	}})
	if cmd != nil {
		t.Fatalf("choices msg should not launch until the user confirms")
	}
	got := updated.(Model)
	if got.suspendedTurnDialog == nil {
		t.Fatalf("suspended turn dialog not shown")
	}
	dialogText := strings.Join(strings.Fields(ansi.Strip(got.renderSuspendedTurnResumeDialogContent(80))), " ")
	if !strings.Contains(dialogText, "only shows sessions recorded in its graceful-shutdown journal") || !strings.Contains(dialogText, "top bar and Agent column show warmup progress") || !strings.Contains(dialogText, "wait for recovery to finish") {
		t.Fatalf("restore dialog should set warmup expectations, got %q", dialogText)
	}

	updated, cmd = got.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if got.suspendedTurnDialog != nil {
		t.Fatalf("dialog should close after confirm")
	}
	if got.restartWarmup == nil || got.restartWarmup.Total != 2 || len(got.restartWarmup.PendingByPath) != 2 {
		t.Fatalf("restart warmup = %#v, want two pending sessions", got.restartWarmup)
	}
	if !strings.Contains(got.status, "warming up 2 engineer sessions one at a time") || !strings.Contains(got.status, "wait before opening them manually") {
		t.Fatalf("warmup status = %q", got.status)
	}
	topStatus := ansi.Strip(got.renderTopStatusLine(220))
	if !strings.Contains(topStatus, "RESTART 0/2") || !strings.Contains(topStatus, "warming up engineer sessions one at a time") {
		t.Fatalf("top status should persist restart warmup progress, got %q", topStatus)
	}
	if compactTopStatus := ansi.Strip(got.renderTopStatusLine(80)); !strings.Contains(compactTopStatus, "RESTART 0/2") {
		t.Fatalf("compact top status should prioritize restart warmup over navigation hints, got %q", compactTopStatus)
	}
	project := model.ProjectSummary{Path: "/tmp/a", LatestSessionFormat: "modern"}
	if label, tag, live := got.projectAgentDisplay(project, time.Now()); label != "CX warmup" || tag != "CX" || !live {
		t.Fatalf("warming project agent display = (%q, %q, %v), want (CX warmup, CX, true)", label, tag, live)
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
	if !requests[1].ContinueInterruptedTurn || requests[1].InterruptedTurnID != "turn-b" || requests[1].Prompt != suspendedTurnContinuationPrompt {
		t.Fatalf("captured second request did not start an explicit continuation: %#v", requests[1])
	}
	for i, msg := range msgs {
		opened, ok := msg.(codexSessionOpenedMsg)
		if !ok || !opened.restartWarmup {
			t.Fatalf("restore message %d = %#v, want warmup-tagged open", i, msg)
		}
	}
	if got.codexVisibleProject != "" {
		t.Fatalf("confirmed startup resumes should stay hidden, visible project = %q", got.codexVisibleProject)
	}
	updated, _ = got.Update(msgs[0])
	got = updated.(Model)
	if got.restartWarmup == nil || got.restartWarmup.Succeeded != 1 || len(got.restartWarmup.PendingByPath) != 1 {
		t.Fatalf("restart warmup after first open = %#v", got.restartWarmup)
	}
	if topStatus = ansi.Strip(got.renderTopStatusLine(220)); !strings.Contains(topStatus, "RESTART 1/2") {
		t.Fatalf("top status after first open = %q, want RESTART 1/2", topStatus)
	}
	updated, _ = got.Update(msgs[1])
	got = updated.(Model)
	if got.restartWarmup != nil {
		t.Fatalf("restart warmup should clear after all opens, got %#v", got.restartWarmup)
	}
	if got.status != "Restart recovery complete: restored 2 engineer sessions." {
		t.Fatalf("completed warmup status = %q", got.status)
	}
	if got.codexVisibleProject != "" {
		t.Fatalf("completed startup resumes should stay hidden, visible project = %q", got.codexVisibleProject)
	}
	if got.codexInput.Focused() {
		t.Fatalf("completed startup resumes should not focus the embedded composer")
	}
}

func TestRestartWarmupBlocksDuplicateManualOpen(t *testing.T) {
	m := Model{}
	m.beginRestartWarmup([]restartWarmupEntry{{
		ProjectPath: "/tmp/demo",
		ProjectName: "demo",
		Provider:    codexapp.ProviderCodex,
	}})

	updated, cmd := m.launchEmbeddedForProjectWithOptions(model.ProjectSummary{
		Path:          "/tmp/demo",
		Name:          "demo",
		PresentOnDisk: true,
	}, codexapp.ProviderCodex, embeddedLaunchOptions{reveal: true})
	got := normalizeUpdateModel(updated)
	if cmd != nil {
		t.Fatal("manual open during restart warmup should not launch another helper")
	}
	if !strings.Contains(got.status, "still warming up") || !strings.Contains(got.status, "wait before opening it manually") {
		t.Fatalf("manual-open warmup hint = %q", got.status)
	}
	if got.codexPendingOpen != nil {
		t.Fatalf("manual-open block should not create pending open: %#v", got.codexPendingOpen)
	}
}

func TestRestartWarmupFailureKeepsSavedRetryHint(t *testing.T) {
	m := Model{}
	m.beginRestartWarmup([]restartWarmupEntry{{
		ProjectPath:    "/tmp/demo",
		ProjectName:    "demo",
		Provider:       codexapp.ProviderCodex,
		CapturedOnQuit: true,
	}})
	m.settleRestartWarmup("/tmp/demo", false)

	if m.restartWarmup != nil {
		t.Fatalf("failed warmup should settle the progress state: %#v", m.restartWarmup)
	}
	if !strings.Contains(m.status, "0 restored; attention needed for 1") || !strings.Contains(m.status, "Saved continuations remain available for retry") {
		t.Fatalf("failed warmup status = %q", m.status)
	}
}

func TestSuspendedTurnRestoreStartsProviderHelpersInOrder(t *testing.T) {
	entered := make(chan string, 2)
	release := make(chan struct{}, 2)
	manager := codexapp.NewManagerWithFactory(func(req codexapp.LaunchRequest, notify func()) (codexapp.Session, error) {
		entered <- req.ProjectPath
		<-release
		return &fakeCodexSession{
			projectPath: req.ProjectPath,
			snapshot: codexapp.Snapshot{
				Provider:    req.Provider.Normalized(),
				ProjectPath: req.ProjectPath,
				ThreadID:    req.ResumeID,
				Started:     true,
			},
		}, nil
	})
	t.Cleanup(func() { _ = manager.CloseAll() })

	m := Model{
		codexManager: manager,
		codexInput:   newCodexTextarea(),
		width:        100,
		height:       24,
	}
	_, cmd := m.resumeSuspendedTurnChoices([]suspendedTurnResumeChoice{
		{ProjectPath: "/tmp/first", Provider: codexapp.ProviderCodex, SessionID: "thread-first", ActiveTurnID: "turn-first", CapturedOnQuit: true},
		{ProjectPath: "/tmp/second", Provider: codexapp.ProviderCodex, SessionID: "thread-second", ActiveTurnID: "turn-second", CapturedOnQuit: true},
	})
	if cmd == nil {
		t.Fatal("restore command = nil")
	}

	program := tea.NewProgram(
		&suspendedTurnCommandProbe{cmd: cmd, remaining: 2},
		tea.WithInput(nil),
		tea.WithoutRenderer(),
	)
	done := make(chan error, 1)
	go func() {
		_, err := program.Run()
		done <- err
	}()
	t.Cleanup(program.Kill)

	select {
	case projectPath := <-entered:
		if projectPath != "/tmp/first" {
			t.Fatalf("first provider startup = %q, want /tmp/first", projectPath)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("first provider startup did not begin")
	}

	select {
	case projectPath := <-entered:
		t.Fatalf("provider startups overlapped; %q began before the first settled", projectPath)
	case <-time.After(150 * time.Millisecond):
	}

	release <- struct{}{}
	select {
	case projectPath := <-entered:
		if projectPath != "/tmp/second" {
			t.Fatalf("second provider startup = %q, want /tmp/second", projectPath)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second provider startup did not begin after the first settled")
	}
	release <- struct{}{}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Bubble Tea command probe failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("restore command sequence did not finish")
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

func TestProjectsMsgDoesNotPromoteArtifactOnlyTurnIntoRestartDialog(t *testing.T) {
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

	choices := buildRestartIntentResumeChoices([]model.ProjectSummary{project}, nil)
	updated, _ = got.Update(suspendedTurnResumeChoicesMsg{choices: choices})
	got = updated.(Model)
	if !got.suspendedTurnChecked {
		t.Fatal("restart intent check should settle even when the journal is empty")
	}
	if got.suspendedTurnDialog != nil {
		t.Fatalf("artifact-only unfinished turn produced restart dialog: %#v", got.suspendedTurnDialog)
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
