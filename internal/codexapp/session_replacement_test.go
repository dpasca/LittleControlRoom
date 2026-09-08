package codexapp

import (
	"errors"
	"testing"
)

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
