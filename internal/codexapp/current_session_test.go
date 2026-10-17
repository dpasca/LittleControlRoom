package codexapp

import (
	"errors"
	"testing"
)

func TestCurrentSessionDeliveryDoesNotLaunchOrReplace(t *testing.T) {
	for _, tc := range []struct {
		name     string
		snapshot *Snapshot
	}{
		{"missing", nil},
		{"replaced", &Snapshot{Provider: ProviderCodex, ThreadID: "replacement", Started: true}},
		{"closed", &Snapshot{Provider: ProviderCodex, ThreadID: "expected", Started: true, Closed: true}},
		{"provider changed", &Snapshot{Provider: ProviderClaudeCode, ThreadID: "expected", Started: true}},
		{"matching", &Snapshot{Provider: ProviderCodex, ThreadID: "expected", Started: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			created := 0
			live := &fakeSession{projectPath: "/project"}
			if tc.snapshot != nil {
				live.snapshot = *tc.snapshot
			}
			manager := NewManagerWithFactory(func(LaunchRequest, func()) (Session, error) { created++; return live, nil })
			if tc.snapshot != nil {
				if _, _, err := manager.Open(LaunchRequest{ProjectPath: "/project", Provider: tc.snapshot.Provider}); err != nil {
					t.Fatal(err)
				}
			}
			before := created
			_, _, err := manager.Open(LaunchRequest{ProjectPath: "/project", Provider: ProviderCodex, ResumeID: "expected", RequireResumeID: true, RequireLiveSession: true, Prompt: "Checkpoint"})
			matching := tc.name == "matching"
			if matching && err != nil {
				t.Fatal(err)
			}
			if !matching && !errors.Is(err, ErrCurrentSessionUnavailable) {
				t.Fatalf("error=%v", err)
			}
			if created != before || live.closed || (len(live.submitted) != 0 && !matching) {
				t.Fatalf("delivery changed a conversation: created=%d closed=%v submitted=%v", created, live.closed, live.submitted)
			}
			if matching && len(live.submitted) != 1 {
				t.Fatal("matching current session did not receive the message")
			}
		})
	}
}
