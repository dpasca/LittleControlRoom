package codexapp

import (
	"errors"
	"testing"
	"time"
)

func TestControlContinuationGuardRejectsChangedInputForEveryProvider(t *testing.T) {
	for _, provider := range []Provider{ProviderCodex, ProviderClaudeCode, ProviderOpenCode, ProviderLCAgent} {
		for _, reason := range []string{"new input", "stop", "reopened same thread"} {
			t.Run(string(provider)+"/"+reason, func(t *testing.T) {
				expected := ControlInputState{Revision: 7, SubmittedAt: time.Now().Add(-time.Minute)}
				current := expected
				switch reason {
				case "new input":
					current.Revision++
				case "stop":
					current.stop()
				case "reopened same thread":
					current.SubmittedAt = time.Now()
				}
				var session Session
				switch provider {
				case ProviderCodex:
					session = &appServerSession{controlInput: current}
				case ProviderClaudeCode:
					session = &claudeCodeSession{controlInput: current}
				case ProviderOpenCode:
					session = &openCodeSession{controlInput: current}
				case ProviderLCAgent:
					session = &lcagentSession{controlInput: current}
				}
				if err := session.SubmitInput(Submission{Text: "resume", RequireIdle: true, ExpectedControlInput: &expected}); !errors.Is(err, ErrSessionChanged) {
					t.Fatalf("stale continuation reached provider: %v", err)
				}
			})
		}
	}
}

func TestControlInputStopDoesNotPoisonLaterUserWork(t *testing.T) {
	var state ControlInputState
	if err := state.accept(Submission{Text: "first"}); err != nil {
		t.Fatal(err)
	}
	old := state
	state.stop()
	if err := state.accept(Submission{Text: "late callback", ExpectedControlInput: &old}); !errors.Is(err, ErrSessionChanged) {
		t.Fatal(err)
	}
	if err := state.accept(Submission{Text: "new task"}); err != nil || state.Stopped {
		t.Fatal(state, err)
	}
	newInput := state
	if err := state.accept(Submission{Text: "valid continuation", ExpectedControlInput: &newInput}); err != nil {
		t.Fatal(err)
	}
	if err := state.accept(Submission{Text: "duplicate continuation", ExpectedControlInput: &newInput}); !errors.Is(err, ErrSessionChanged) {
		t.Fatal(err)
	}
}
