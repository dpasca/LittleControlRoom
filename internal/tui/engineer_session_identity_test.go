package tui

import (
	"lcroom/internal/codexapp"
	"testing"
)

func TestIdleCallerIdentityAnnouncementPersistsOffUIThread(t *testing.T) {
	for _, provider := range []codexapp.Provider{codexapp.ProviderCodex, codexapp.ProviderClaudeCode, codexapp.ProviderOpenCode, codexapp.ProviderLCAgent} {
		t.Run(string(provider), func(t *testing.T) {
			svc := newControlTestService(t)
			const path = "/caller/worktree"
			m := Model{ctx: t.Context(), svc: svc}
			before := codexapp.Snapshot{Provider: provider, ProjectPath: path, ControlSessionKey: "host-key"}
			after := before
			after.ThreadID = "provider-thread"
			if !shouldPersistEmbeddedSessionTransitionAfterCodexSnapshot(true, before, after) {
				t.Fatal("idle identity announcement was dropped")
			}
			cmd := m.recordEmbeddedSessionTransitionCmd(path, after)
			if cmd == nil {
				t.Fatal("no async identity record command")
			}
			source := embeddedSessionSource(provider)
			got, err := svc.Store().ResolveEngineerSession(t.Context(), path, source, "host-key")
			if err != nil || got != "" {
				t.Fatalf("update path performed database write: %q, %v", got, err)
			}
			msg, ok := cmd().(embeddedSessionActivityRecordedMsg)
			if !ok || msg.err != nil {
				t.Fatalf("record result = %#v", msg)
			}
			got, err = svc.Store().ResolveEngineerSession(t.Context(), path, source, "host-key")
			if err != nil || got != after.ThreadID {
				t.Fatalf("binding = %q, %v", got, err)
			}
			if shouldPersistEmbeddedSessionTransitionAfterCodexSnapshot(true, after, after) {
				t.Fatal("unchanged idle snapshot scheduled another write")
			}
		})
	}
}
