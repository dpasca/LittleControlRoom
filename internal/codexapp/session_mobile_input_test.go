package codexapp

import (
	"errors"
	"testing"
)

func TestDescribeSessionInputUsesConservativeBusyModes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		snapshot  Snapshot
		available bool
		mode      SessionInputMode
	}{
		{name: "idle", snapshot: Snapshot{Provider: ProviderCodex, Started: true}, available: true, mode: SessionInputSend},
		{name: "codex steer", snapshot: Snapshot{Provider: ProviderCodex, Started: true, Busy: true, Phase: SessionPhaseRunning, ActiveTurnID: "turn-1"}, available: true, mode: SessionInputSteer},
		{name: "lcagent queue", snapshot: Snapshot{Provider: ProviderLCAgent, Started: true, Busy: true, Phase: SessionPhaseRunning}, available: true, mode: SessionInputQueue},
		{name: "claude busy", snapshot: Snapshot{Provider: ProviderClaudeCode, Started: true, Busy: true, Phase: SessionPhaseRunning}},
		{name: "external", snapshot: Snapshot{Provider: ProviderCodex, Started: true, BusyExternal: true}},
		{name: "approval", snapshot: Snapshot{Provider: ProviderCodex, Started: true, PendingApproval: &ApprovalRequest{}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DescribeSessionInput(tt.snapshot)
			if got.Available != tt.available || got.Mode != tt.mode {
				t.Fatalf("DescribeSessionInput() = %#v, want available=%t mode=%q", got, tt.available, tt.mode)
			}
		})
	}
}

func TestManagerSubmitSessionInputTargetsExpectedLiveThread(t *testing.T) {
	t.Parallel()
	projectPath := "/tmp/mobile-input"
	session := &fakeSession{
		projectPath: projectPath,
		snapshot: Snapshot{
			Provider:     ProviderCodex,
			ThreadID:     "thread-1",
			Started:      true,
			Busy:         true,
			Phase:        SessionPhaseRunning,
			ActiveTurnID: "turn-1",
		},
	}
	manager := &Manager{sessions: map[string]Session{projectPath: session}}

	result, err := manager.SubmitSessionInput(projectPath, "thread-1", Submission{Text: "Please continue from the phone."})
	if err != nil {
		t.Fatalf("SubmitSessionInput() error = %v", err)
	}
	if result.Mode != SessionInputSteer || result.ThreadID != "thread-1" {
		t.Fatalf("result = %#v, want steer for thread-1", result)
	}
	if got, want := session.submitted, []string{"Please continue from the phone."}; len(got) != 1 || got[0] != want[0] {
		t.Fatalf("submitted = %#v, want %#v", got, want)
	}

	_, err = manager.SubmitSessionInput(projectPath, "thread-old", Submission{Text: "Wrong thread"})
	if !errors.Is(err, ErrSessionChanged) {
		t.Fatalf("stale SubmitSessionInput() error = %v, want ErrSessionChanged", err)
	}
	if len(session.submitted) != 1 {
		t.Fatalf("stale submission reached session: %#v", session.submitted)
	}
}
