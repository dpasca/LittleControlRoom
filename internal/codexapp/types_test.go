package codexapp

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"lcroom/internal/codexcli"
)

type fakeSession struct {
	projectPath    string
	snapshot       Snapshot
	submitted      []string
	closed         bool
	waitClosedFn   func(*fakeSession, time.Duration) bool
	refreshCalls   int
	refreshFn      func(*fakeSession) error
	reconcileCalls int
	reconcileFn    func(*fakeSession) error
}

func (s *fakeSession) ProjectPath() string {
	return s.projectPath
}

func (s *fakeSession) Snapshot() Snapshot {
	snapshot := s.snapshot
	snapshot.ProjectPath = s.projectPath
	snapshot.Closed = s.closed || snapshot.Closed
	return snapshot
}

func (s *fakeSession) Submit(prompt string) error {
	s.submitted = append(s.submitted, prompt)
	return nil
}

func (s *fakeSession) SubmitInput(input Submission) error {
	s.submitted = append(s.submitted, input.TranscriptText())
	return nil
}

func (s *fakeSession) ShowStatus() error {
	return nil
}

func (s *fakeSession) ListModels() ([]ModelOption, error) {
	return nil, nil
}

func (s *fakeSession) StageModelOverride(model, reasoningEffort string) error {
	s.snapshot.PendingModel = model
	s.snapshot.PendingReasoning = reasoningEffort
	return nil
}

func (s *fakeSession) Interrupt() error {
	return nil
}

func (s *fakeSession) RespondApproval(decision ApprovalDecision) error {
	return nil
}

func (s *fakeSession) RespondToolInput(answers map[string][]string) error {
	return nil
}

func (s *fakeSession) RespondElicitation(decision ElicitationDecision, content json.RawMessage) error {
	return nil
}

func (s *fakeSession) Close() error {
	s.closed = true
	return nil
}

func (s *fakeSession) WaitClosed(timeout time.Duration) bool {
	if s.waitClosedFn != nil {
		return s.waitClosedFn(s, timeout)
	}
	return s.closed
}

func (s *fakeSession) RefreshBusyElsewhere() error {
	s.refreshCalls++
	if s.refreshFn != nil {
		return s.refreshFn(s)
	}
	return nil
}

func (s *fakeSession) ReconcileBusyState() error {
	s.reconcileCalls++
	if s.reconcileFn != nil {
		return s.reconcileFn(s)
	}
	return nil
}

func TestTokenUsageSnapshotEstimatedContextUsesLastTurnPromptAndVisibleOutput(t *testing.T) {
	usage := &TokenUsageSnapshot{
		Last: TokenUsageBreakdown{
			InputTokens:           10000,
			OutputTokens:          2345,
			ReasoningOutputTokens: 345,
			TotalTokens:           12345,
		},
		Total: TokenUsageBreakdown{
			InputTokens: 999999,
			TotalTokens: 999999,
		},
		ModelContextWindow: 200000,
	}

	if got := usage.EstimatedContextTokens(); got != 12000 {
		t.Fatalf("EstimatedContextTokens() = %d, want 12000", got)
	}
	if got := usage.ContextLeftTokens(); got != 188000 {
		t.Fatalf("ContextLeftTokens() = %d, want 188000", got)
	}
	if got := usage.ContextLeftPercent(); got != 94 {
		t.Fatalf("ContextLeftPercent() = %d, want 94", got)
	}
}

func TestTokenUsageSnapshotEstimatedContextFallsBackWhenLastTurnMissing(t *testing.T) {
	usage := &TokenUsageSnapshot{
		Total: TokenUsageBreakdown{
			InputTokens:           12000,
			OutputTokens:          2500,
			ReasoningOutputTokens: 500,
		},
		ModelContextWindow: 200000,
	}

	if got := usage.EstimatedContextTokens(); got != 14000 {
		t.Fatalf("EstimatedContextTokens() = %d, want 14000", got)
	}
	if got := usage.ContextLeftPercent(); got != 93 {
		t.Fatalf("ContextLeftPercent() = %d, want 93", got)
	}
}

func TestManagerOpenReusesExistingSessionAndSubmitsPrompt(t *testing.T) {
	var created []*fakeSession

	manager := NewManagerWithFactory(func(req LaunchRequest, notify func()) (Session, error) {
		session := &fakeSession{
			projectPath: req.ProjectPath,
			snapshot: Snapshot{
				Started: true,
				Preset:  req.Preset,
			},
		}
		created = append(created, session)
		return session, nil
	})

	first, reused, err := manager.Open(LaunchRequest{
		ProjectPath: "/tmp/demo",
		Preset:      codexcli.PresetYolo,
	})
	if err != nil {
		t.Fatalf("first Open() error = %v", err)
	}
	if reused {
		t.Fatalf("first Open() reused = true, want false")
	}

	second, reused, err := manager.Open(LaunchRequest{
		ProjectPath: "/tmp/demo",
		Preset:      codexcli.PresetYolo,
		Prompt:      "continue",
	})
	if err != nil {
		t.Fatalf("second Open() error = %v", err)
	}
	if !reused {
		t.Fatalf("second Open() reused = false, want true")
	}
	if first != second {
		t.Fatalf("Open() should reuse existing session")
	}
	if len(created) != 1 {
		t.Fatalf("factory create count = %d, want 1", len(created))
	}
	if got := created[0].submitted; len(got) != 1 || got[0] != "continue" {
		t.Fatalf("submitted prompts = %#v, want [\"continue\"]", got)
	}
}

func TestManagerOpenRefreshesBusyElsewhereSessionBeforeSubmittingPrompt(t *testing.T) {
	session := &fakeSession{
		projectPath: "/tmp/demo",
		snapshot: Snapshot{
			Started:      true,
			Preset:       codexcli.PresetYolo,
			Busy:         true,
			BusyExternal: true,
		},
		refreshFn: func(s *fakeSession) error {
			s.snapshot.Busy = false
			s.snapshot.BusyExternal = false
			s.snapshot.Status = "Embedded controls are live again"
			return nil
		},
	}

	manager := NewManagerWithFactory(func(req LaunchRequest, notify func()) (Session, error) {
		return session, nil
	})

	if _, _, err := manager.Open(LaunchRequest{
		ProjectPath: "/tmp/demo",
		Preset:      codexcli.PresetYolo,
	}); err != nil {
		t.Fatalf("first Open() error = %v", err)
	}

	if _, reused, err := manager.Open(LaunchRequest{
		ProjectPath: "/tmp/demo",
		Preset:      codexcli.PresetYolo,
		Prompt:      "continue",
	}); err != nil {
		t.Fatalf("second Open() error = %v", err)
	} else if !reused {
		t.Fatalf("second Open() reused = false, want true")
	}

	if session.refreshCalls != 1 {
		t.Fatalf("refresh calls = %d, want 1", session.refreshCalls)
	}
	if got := session.submitted; len(got) != 1 || got[0] != "continue" {
		t.Fatalf("submitted prompts = %#v, want [\"continue\"]", got)
	}
}

func TestManagerOpenReusesExistingSessionAppliesPendingModelOverride(t *testing.T) {
	session := &fakeSession{
		projectPath: "/tmp/demo",
		snapshot: Snapshot{
			Started:         true,
			Preset:          codexcli.PresetYolo,
			Model:           "gpt-5",
			ReasoningEffort: "medium",
		},
	}

	manager := NewManagerWithFactory(func(req LaunchRequest, notify func()) (Session, error) {
		return session, nil
	})

	if _, _, err := manager.Open(LaunchRequest{
		ProjectPath: "/tmp/demo",
		Preset:      codexcli.PresetYolo,
	}); err != nil {
		t.Fatalf("first Open() error = %v", err)
	}

	if _, reused, err := manager.Open(LaunchRequest{
		ProjectPath:      "/tmp/demo",
		Preset:           codexcli.PresetYolo,
		PendingModel:     "gpt-5-codex",
		PendingReasoning: "high",
		Prompt:           "continue",
	}); err != nil {
		t.Fatalf("second Open() error = %v", err)
	} else if !reused {
		t.Fatalf("second Open() reused = false, want true")
	}

	if session.snapshot.PendingModel != "gpt-5-codex" || session.snapshot.PendingReasoning != "high" {
		t.Fatalf("pending override = (%q, %q), want (gpt-5-codex, high)", session.snapshot.PendingModel, session.snapshot.PendingReasoning)
	}
	if got := session.submitted; len(got) != 1 || got[0] != "continue" {
		t.Fatalf("submitted prompts = %#v, want [\"continue\"]", got)
	}
}

func TestManagerOpenForceNewReplacesExistingSession(t *testing.T) {
	var created []*fakeSession

	manager := NewManagerWithFactory(func(req LaunchRequest, notify func()) (Session, error) {
		session := &fakeSession{
			projectPath: req.ProjectPath,
			snapshot: Snapshot{
				Started: true,
				Preset:  req.Preset,
			},
		}
		created = append(created, session)
		return session, nil
	})

	first, _, err := manager.Open(LaunchRequest{
		ProjectPath: "/tmp/demo",
		Preset:      codexcli.PresetYolo,
	})
	if err != nil {
		t.Fatalf("first Open() error = %v", err)
	}

	second, reused, err := manager.Open(LaunchRequest{
		ProjectPath: "/tmp/demo",
		Preset:      codexcli.PresetYolo,
		ForceNew:    true,
	})
	if err != nil {
		t.Fatalf("second Open() error = %v", err)
	}
	if reused {
		t.Fatalf("second Open() reused = true, want false")
	}
	if first == second {
		t.Fatalf("Open() should replace the existing session on ForceNew")
	}
	if len(created) != 2 {
		t.Fatalf("factory create count = %d, want 2", len(created))
	}
	if !created[0].closed {
		t.Fatalf("original session should be closed when replaced")
	}
}

func TestManagerOpenFailedReplacementKeepsClosedExistingSession(t *testing.T) {
	createCalls := 0

	manager := NewManagerWithFactory(func(req LaunchRequest, notify func()) (Session, error) {
		createCalls++
		if createCalls == 1 {
			return &fakeSession{
				projectPath: req.ProjectPath,
				snapshot: Snapshot{
					Started: true,
					Preset:  req.Preset,
				},
			}, nil
		}
		return nil, fmt.Errorf("replacement failed")
	})

	first, _, err := manager.Open(LaunchRequest{
		ProjectPath: "/tmp/demo",
		Preset:      codexcli.PresetYolo,
	})
	if err != nil {
		t.Fatalf("first Open() error = %v", err)
	}

	second, reused, err := manager.Open(LaunchRequest{
		ProjectPath: "/tmp/demo",
		Preset:      codexcli.PresetYolo,
		ForceNew:    true,
	})
	if err == nil {
		t.Fatalf("second Open() error = nil, want replacement failure")
	}
	if reused {
		t.Fatalf("second Open() reused = true, want false")
	}
	if second != nil {
		t.Fatalf("second Open() session = %#v, want nil on failure", second)
	}

	stored, ok := manager.Session("/tmp/demo")
	if !ok {
		t.Fatalf("manager.Session() lost the previous session after a failed replacement")
	}
	if stored != first {
		t.Fatalf("manager.Session() = %#v, want original session %#v", stored, first)
	}
	if !stored.Snapshot().Closed {
		t.Fatalf("stored snapshot should remain closed after the failed replacement")
	}
}

func TestManagerOpenReplacesSessionWhenResumeIDChanges(t *testing.T) {
	var created []*fakeSession

	manager := NewManagerWithFactory(func(req LaunchRequest, notify func()) (Session, error) {
		session := &fakeSession{
			projectPath: req.ProjectPath,
			snapshot: Snapshot{
				Started:  true,
				Preset:   req.Preset,
				ThreadID: req.ResumeID,
			},
		}
		created = append(created, session)
		return session, nil
	})

	first, reused, err := manager.Open(LaunchRequest{
		ProjectPath: "/tmp/demo",
		Preset:      codexcli.PresetYolo,
		ResumeID:    "thread_a",
	})
	if err != nil {
		t.Fatalf("first Open() error = %v", err)
	}
	if reused {
		t.Fatalf("first Open() reused = true, want false")
	}

	second, reused, err := manager.Open(LaunchRequest{
		ProjectPath: "/tmp/demo",
		Preset:      codexcli.PresetYolo,
		ResumeID:    "thread_b",
	})
	if err != nil {
		t.Fatalf("second Open() error = %v", err)
	}
	if reused {
		t.Fatalf("second Open() reused = true, want false")
	}
	if first == second {
		t.Fatalf("Open() should replace the existing session when the resume target changes")
	}
	if len(created) != 2 {
		t.Fatalf("factory create count = %d, want 2", len(created))
	}
	if !created[0].closed {
		t.Fatalf("original session should be closed when the resume target changes")
	}
	if got := created[1].snapshot.ThreadID; got != "thread_b" {
		t.Fatalf("replacement thread id = %q, want %q", got, "thread_b")
	}
}

func TestManagerOpenReplacesSessionWhenProviderChanges(t *testing.T) {
	var created []*fakeSession

	manager := NewManagerWithFactory(func(req LaunchRequest, notify func()) (Session, error) {
		session := &fakeSession{
			projectPath: req.ProjectPath,
			snapshot: Snapshot{
				Provider: ProviderCodex,
				Started:  true,
				Preset:   req.Preset,
			},
		}
		if req.Provider.Normalized() == ProviderOpenCode {
			session.snapshot.Provider = ProviderOpenCode
			session.snapshot.Preset = ""
		}
		created = append(created, session)
		return session, nil
	})

	first, reused, err := manager.Open(LaunchRequest{
		ProjectPath: "/tmp/demo",
		Provider:    ProviderCodex,
		Preset:      codexcli.PresetYolo,
	})
	if err != nil {
		t.Fatalf("first Open() error = %v", err)
	}
	if reused {
		t.Fatalf("first Open() reused = true, want false")
	}

	second, reused, err := manager.Open(LaunchRequest{
		ProjectPath: "/tmp/demo",
		Provider:    ProviderOpenCode,
		ResumeID:    "ses_open",
	})
	if err != nil {
		t.Fatalf("second Open() error = %v", err)
	}
	if reused {
		t.Fatalf("second Open() reused = true, want false")
	}
	if first == second {
		t.Fatalf("Open() should replace the existing session when the provider changes")
	}
	if len(created) != 2 {
		t.Fatalf("factory create count = %d, want 2", len(created))
	}
	if !created[0].closed {
		t.Fatalf("original session should be closed when the provider changes")
	}
	if got := created[1].snapshot.Provider; got != ProviderOpenCode {
		t.Fatalf("replacement provider = %q, want %q", got, ProviderOpenCode)
	}
}

func TestManagerOpenForceNewWaitsForExistingSessionShutdown(t *testing.T) {
	release := make(chan struct{})
	created := make(chan struct{}, 1)

	manager := NewManagerWithFactory(func(req LaunchRequest, notify func()) (Session, error) {
		session := &fakeSession{
			projectPath: req.ProjectPath,
			snapshot: Snapshot{
				Started: true,
				Preset:  req.Preset,
			},
		}
		select {
		case created <- struct{}{}:
		default:
		}
		return session, nil
	})

	first, _, err := manager.Open(LaunchRequest{
		ProjectPath: "/tmp/demo",
		Preset:      codexcli.PresetYolo,
	})
	if err != nil {
		t.Fatalf("first Open() error = %v", err)
	}
	session := first.(*fakeSession)
	session.waitClosedFn = func(_ *fakeSession, timeout time.Duration) bool {
		select {
		case <-release:
			return true
		case <-time.After(timeout):
			return false
		}
	}

	result := make(chan error, 1)
	go func() {
		_, _, err := manager.Open(LaunchRequest{
			ProjectPath: "/tmp/demo",
			Preset:      codexcli.PresetYolo,
			ForceNew:    true,
		})
		result <- err
	}()

	select {
	case <-created:
		// Consume the initial session creation event from the first open.
	case <-time.After(100 * time.Millisecond):
		t.Fatalf("first session creation was not recorded")
	}

	select {
	case <-created:
		t.Fatalf("replacement session should not start before the old session finishes shutting down")
	case <-time.After(100 * time.Millisecond):
	}

	close(release)

	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("second Open() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatalf("second Open() did not finish after allowing shutdown")
	}

	select {
	case <-created:
	case <-time.After(100 * time.Millisecond):
		t.Fatalf("replacement session was never created")
	}
}

func TestManagerOpenReplacesClosedSession(t *testing.T) {
	var created []*fakeSession

	manager := NewManagerWithFactory(func(req LaunchRequest, notify func()) (Session, error) {
		session := &fakeSession{
			projectPath: req.ProjectPath,
			snapshot: Snapshot{
				Started: true,
				Preset:  req.Preset,
			},
		}
		created = append(created, session)
		return session, nil
	})

	first, _, err := manager.Open(LaunchRequest{
		ProjectPath: "/tmp/demo",
		Preset:      codexcli.PresetYolo,
	})
	if err != nil {
		t.Fatalf("first Open() error = %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}

	second, reused, err := manager.Open(LaunchRequest{
		ProjectPath: "/tmp/demo",
		Preset:      codexcli.PresetYolo,
	})
	if err != nil {
		t.Fatalf("second Open() error = %v", err)
	}
	if reused {
		t.Fatalf("second Open() reused = true, want false")
	}
	if first == second {
		t.Fatalf("Open() should replace a closed session")
	}
	if len(created) != 2 {
		t.Fatalf("factory create count = %d, want 2", len(created))
	}
}

func TestManagerReapIdleSessionsClosesInactiveSession(t *testing.T) {
	session := &fakeSession{
		projectPath: "/tmp/demo",
		snapshot: Snapshot{
			Started:        true,
			Preset:         codexcli.PresetYolo,
			LastActivityAt: time.Now().Add(-2 * time.Hour),
		},
	}
	manager := NewManagerWithFactory(func(req LaunchRequest, notify func()) (Session, error) {
		return session, nil
	})
	manager.idleTimeout = time.Hour

	if _, _, err := manager.Open(LaunchRequest{
		ProjectPath: "/tmp/demo",
		Preset:      codexcli.PresetYolo,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	manager.reapIdleSessions(time.Now())
	if !session.closed {
		t.Fatalf("idle session should be closed by the reaper")
	}
}

func TestManagerReconcileBusySessionsRechecksStaleBusySession(t *testing.T) {
	session := &fakeSession{
		projectPath: "/tmp/demo",
		snapshot: Snapshot{
			Started:        true,
			Preset:         codexcli.PresetYolo,
			Busy:           true,
			Phase:          SessionPhaseFinishing,
			LastActivityAt: time.Now().Add(-2 * time.Minute),
		},
	}
	manager := NewManagerWithFactory(func(req LaunchRequest, notify func()) (Session, error) {
		return session, nil
	})
	manager.busyReconcileAfter = 30 * time.Second

	if _, _, err := manager.Open(LaunchRequest{
		ProjectPath: "/tmp/demo",
		Preset:      codexcli.PresetYolo,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	manager.reconcileBusySessions(time.Now())
	if session.reconcileCalls != 1 {
		t.Fatalf("reconcile calls = %d, want 1", session.reconcileCalls)
	}
}

func TestManagerReconcileBusySessionsUsesBusyActivityTimestamp(t *testing.T) {
	session := &fakeSession{
		projectPath: "/tmp/demo",
		snapshot: Snapshot{
			Started:            true,
			Preset:             codexcli.PresetYolo,
			Busy:               true,
			Phase:              SessionPhaseFinishing,
			LastActivityAt:     time.Now(),
			LastBusyActivityAt: time.Now().Add(-2 * time.Minute),
		},
	}
	manager := NewManagerWithFactory(func(req LaunchRequest, notify func()) (Session, error) {
		return session, nil
	})
	manager.busyReconcileAfter = 30 * time.Second

	if _, _, err := manager.Open(LaunchRequest{
		ProjectPath: "/tmp/demo",
		Preset:      codexcli.PresetYolo,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	manager.reconcileBusySessions(time.Now())
	if session.reconcileCalls != 1 {
		t.Fatalf("reconcile calls = %d, want 1 when only generic activity is fresh", session.reconcileCalls)
	}
}

func TestManagerCoalescesDuplicateUpdatesUntilAck(t *testing.T) {
	manager := NewManagerWithFactory(func(req LaunchRequest, notify func()) (Session, error) {
		return &fakeSession{
			projectPath: req.ProjectPath,
			snapshot: Snapshot{
				Started: true,
				Preset:  req.Preset,
			},
		}, nil
	})

	if _, _, err := manager.Open(LaunchRequest{
		ProjectPath: "/tmp/demo",
		Preset:      codexcli.PresetYolo,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	first := <-manager.Updates()
	if first != "/tmp/demo" {
		t.Fatalf("first update = %q, want /tmp/demo", first)
	}

	manager.notify("/tmp/demo")
	manager.notify("/tmp/demo")

	select {
	case duplicate := <-manager.Updates():
		t.Fatalf("duplicate update leaked before ack: %q", duplicate)
	default:
	}

	manager.AckUpdate("/tmp/demo")
	manager.notify("/tmp/demo")

	select {
	case next := <-manager.Updates():
		if next != "/tmp/demo" {
			t.Fatalf("next update = %q, want /tmp/demo", next)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatalf("expected an update after ack")
	}
}

func TestManagerReplaysDeferredUpdateAfterAck(t *testing.T) {
	manager := NewManagerWithFactory(func(req LaunchRequest, notify func()) (Session, error) {
		return &fakeSession{
			projectPath: req.ProjectPath,
			snapshot: Snapshot{
				Started: true,
				Preset:  req.Preset,
			},
		}, nil
	})

	if _, _, err := manager.Open(LaunchRequest{
		ProjectPath: "/tmp/demo",
		Preset:      codexcli.PresetYolo,
	}); err != nil {
		t.Fatalf("manager.Open() error = %v", err)
	}

	first := <-manager.Updates()
	if first != "/tmp/demo" {
		t.Fatalf("first update = %q, want /tmp/demo", first)
	}

	manager.notify("/tmp/demo")
	manager.notify("/tmp/demo")
	manager.AckUpdate("/tmp/demo")

	select {
	case next := <-manager.Updates():
		if next != "/tmp/demo" {
			t.Fatalf("next update = %q, want /tmp/demo", next)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatalf("expected a deferred update after ack")
	}
}

func TestPresetMappings(t *testing.T) {
	tests := []struct {
		name         string
		preset       codexcli.Preset
		wantApproval string
		wantSandbox  string
	}{
		{
			name:         "yolo",
			preset:       codexcli.PresetYolo,
			wantApproval: "never",
			wantSandbox:  "danger-full-access",
		},
		{
			name:         "full-auto",
			preset:       codexcli.PresetFullAuto,
			wantApproval: "on-request",
			wantSandbox:  "workspace-write",
		},
		{
			name:         "safe",
			preset:       codexcli.PresetSafe,
			wantApproval: "on-request",
			wantSandbox:  "read-only",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := approvalPolicyForPreset(tt.preset); got != tt.wantApproval {
				t.Fatalf("approvalPolicyForPreset(%q) = %q, want %q", tt.preset, got, tt.wantApproval)
			}
			if got := sandboxModeForPreset(tt.preset); got != tt.wantSandbox {
				t.Fatalf("sandboxModeForPreset(%q) = %q, want %q", tt.preset, got, tt.wantSandbox)
			}
		})
	}
}

func TestApprovalRequestAllowsDecision(t *testing.T) {
	commandApproval := ApprovalRequest{Kind: ApprovalCommandExecution}
	if !commandApproval.AllowsDecision(DecisionAcceptForSession) {
		t.Fatalf("command approval should allow accept-for-session")
	}

	fileApproval := ApprovalRequest{Kind: ApprovalFileChange}
	if fileApproval.AllowsDecision(DecisionAcceptForSession) {
		t.Fatalf("file change approval should not allow accept-for-session")
	}
	if !fileApproval.AllowsDecision(DecisionAccept) {
		t.Fatalf("file change approval should allow a normal accept")
	}
}
