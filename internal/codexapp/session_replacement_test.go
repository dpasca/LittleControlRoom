package codexapp

import (
	"errors"
	"testing"
)

func TestEmptySessionRecoveryRefusesChangedSession(t *testing.T) {
	for _, change := range []string{"prompt", "busy", "replacement"} {
		t.Run(change, func(t *testing.T) {
			manager := NewManagerWithFactory(func(req LaunchRequest, notify func()) (Session, error) {
				return &fakeSession{projectPath: req.ProjectPath, snapshot: Snapshot{
					Provider: ProviderClaudeCode, ThreadID: "startup", Started: true, EmptyConversation: true,
				}}, nil
			})
			first, _, err := manager.Open(LaunchRequest{ProjectPath: "/tmp/demo", Provider: ProviderClaudeCode})
			if err != nil {
				t.Fatal(err)
			}
			session := first.(*fakeSession)
			switch change {
			case "prompt":
				session.snapshot.EmptyConversation = false
			case "busy":
				session.snapshot.Busy = true
			case "replacement":
				session.snapshot.ThreadID = "different"
			}
			_, _, err = manager.Open(LaunchRequest{
				ProjectPath: "/tmp/demo", Provider: ProviderCodex, ResumeID: "saved-work", ReplaceEmptySessionID: "startup",
			})
			if !errors.Is(err, ErrSessionChanged) || session.closed {
				t.Fatalf("changed session must remain open: error=%v closed=%v", err, session.closed)
			}
		})
	}
}

func TestBusySessionReplacementRequiresExactConfirmation(t *testing.T) {
	for _, provider := range []Provider{ProviderCodex, ProviderOpenCode, ProviderClaudeCode, ProviderLCAgent} {
		t.Run(string(provider), func(t *testing.T) {
			created := 0
			manager := NewManagerWithFactory(func(req LaunchRequest, notify func()) (Session, error) {
				created++
				return &fakeSession{projectPath: req.ProjectPath, snapshot: Snapshot{Provider: provider, ThreadID: "original", Busy: true, Started: true}}, nil
			})
			req := LaunchRequest{ProjectPath: "/tmp/demo", Provider: provider}
			first, _, err := manager.Open(req)
			if err != nil {
				t.Fatal(err)
			}
			req.ForceNew = true
			_, _, err = manager.Open(req)
			var busy *BusySessionReplacementError
			if !errors.As(err, &busy) || busy.SessionID != "original" {
				t.Fatalf("expected busy refusal, got %v", err)
			}
			if first.(*fakeSession).closed || created != 1 {
				t.Fatal("refusal changed the running session")
			}
			req.ConfirmedReplacementSessionID = "different"
			_, _, err = manager.Open(req)
			if !errors.Is(err, ErrSessionChanged) || first.(*fakeSession).closed {
				t.Fatalf("stale confirmation must fail without closing: %v", err)
			}
			req.ConfirmedReplacementSessionID = "original"
			_, _, err = manager.Open(req)
			if err != nil {
				t.Fatal(err)
			}
			if !first.(*fakeSession).closed || created != 2 {
				t.Fatal("confirmed replacement did not run")
			}
		})
	}
}
