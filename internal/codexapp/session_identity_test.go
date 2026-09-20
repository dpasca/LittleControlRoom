package codexapp

import (
	"encoding/json"
	"testing"
)

func TestProviderAnnouncementsKeepControlAndResumeIdentitiesDistinct(t *testing.T) {
	check := func(t *testing.T, snapshot Snapshot, key, id string) {
		t.Helper()
		if snapshot.ControlSessionKey != key || snapshot.ThreadID != id {
			t.Fatalf("identities = control %q, provider %q; want %q, %q", snapshot.ControlSessionKey, snapshot.ThreadID, key, id)
		}
	}
	t.Run("codex", func(t *testing.T) {
		s := &appServerSession{controlSessionKey: "host-key", notify: func() {}}
		s.handleNotification("thread/started", json.RawMessage(`{"thread":{"id":"provider-thread"}}`))
		check(t, s.StateSnapshot(), "host-key", "provider-thread")
	})
	t.Run("claude", func(t *testing.T) {
		s := &claudeCodeSession{controlSessionKey: "host-key", notify: func() {}, claudeHome: t.TempDir(), projectPath: "/caller"}
		s.handleClaudeStdoutLine(`{"type":"system","subtype":"init","session_id":"provider-thread"}`)
		check(t, s.StateSnapshot(), "host-key", "provider-thread")
	})
	t.Run("opencode", func(t *testing.T) {
		s := &openCodeSession{controlSessionKey: "host-key", sessionID: "provider-thread"}
		check(t, s.StateSnapshot(), "host-key", "provider-thread")
	})
	t.Run("lcagent", func(t *testing.T) {
		s := &lcagentSession{notify: func() {}}
		s.handleEvent([]byte(`{"type":"session_meta","id":"run-one","thread_id":"stable-thread"}`))
		check(t, s.StateSnapshot(), "stable-thread", "stable-thread")
		s.handleEvent([]byte(`{"type":"session_meta","id":"run-two","thread_id":"stable-thread"}`))
		check(t, s.StateSnapshot(), "stable-thread", "stable-thread")
	})
}
