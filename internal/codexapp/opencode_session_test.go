package codexapp

import "testing"

func TestNewOpenCodeHTTPClientHasNoGlobalTimeout(t *testing.T) {
	client := newOpenCodeHTTPClient()
	if client == nil {
		t.Fatalf("newOpenCodeHTTPClient() returned nil")
	}
	if client.Timeout != 0 {
		t.Fatalf("client timeout = %s, want 0 so the SSE stream can stay open", client.Timeout)
	}
}

func TestOpenCodeSessionStatusIdleMarksTurnCompleted(t *testing.T) {
	session := newTestOpenCodeSession()

	session.handleEventData(`{"type":"session.status","properties":{"sessionID":"ses_test","status":{"type":"busy"}}}`)
	session.handleEventData(`{"type":"session.status","properties":{"sessionID":"ses_test","status":{"type":"idle"}}}`)

	snapshot := session.Snapshot()
	if snapshot.Busy {
		t.Fatalf("snapshot.Busy = true, want false")
	}
	if snapshot.BusyExternal {
		t.Fatalf("snapshot.BusyExternal = true, want false")
	}
	if snapshot.ActiveTurnID != "" {
		t.Fatalf("snapshot.ActiveTurnID = %q, want empty", snapshot.ActiveTurnID)
	}
	if snapshot.Status != "Turn completed" {
		t.Fatalf("snapshot.Status = %q, want %q", snapshot.Status, "Turn completed")
	}
	if snapshot.LastSystemNotice != "Turn completed" {
		t.Fatalf("snapshot.LastSystemNotice = %q, want %q", snapshot.LastSystemNotice, "Turn completed")
	}
}

func TestOpenCodeSessionIdleEventMarksTurnCompleted(t *testing.T) {
	session := newTestOpenCodeSession()

	session.handleEventData(`{"type":"session.status","properties":{"sessionID":"ses_test","status":{"type":"busy"}}}`)
	session.handleEventData(`{"type":"session.idle","properties":{"sessionID":"ses_test"}}`)

	snapshot := session.Snapshot()
	if snapshot.Busy {
		t.Fatalf("snapshot.Busy = true, want false")
	}
	if snapshot.Status != "Turn completed" {
		t.Fatalf("snapshot.Status = %q, want %q", snapshot.Status, "Turn completed")
	}
	if snapshot.LastSystemNotice != "Turn completed" {
		t.Fatalf("snapshot.LastSystemNotice = %q, want %q", snapshot.LastSystemNotice, "Turn completed")
	}
}

func TestOpenCodeSessionIdleAfterExternalBusyMarksSessionReady(t *testing.T) {
	session := newTestOpenCodeSession()
	session.busy = true
	session.busyExternal = true
	session.activeTurnID = "ses_test"
	session.status = "OpenCode is active in another process"

	session.handleEventData(`{"type":"session.status","properties":{"sessionID":"ses_test","status":{"type":"idle"}}}`)

	snapshot := session.Snapshot()
	if snapshot.Busy {
		t.Fatalf("snapshot.Busy = true, want false")
	}
	if snapshot.BusyExternal {
		t.Fatalf("snapshot.BusyExternal = true, want false")
	}
	if snapshot.ActiveTurnID != "" {
		t.Fatalf("snapshot.ActiveTurnID = %q, want empty", snapshot.ActiveTurnID)
	}
	if snapshot.Status != "OpenCode session ready" {
		t.Fatalf("snapshot.Status = %q, want %q", snapshot.Status, "OpenCode session ready")
	}
	if snapshot.LastSystemNotice != "OpenCode session ready" {
		t.Fatalf("snapshot.LastSystemNotice = %q, want %q", snapshot.LastSystemNotice, "OpenCode session ready")
	}
}

func newTestOpenCodeSession() *openCodeSession {
	return &openCodeSession{
		sessionID:         "ses_test",
		http:              newOpenCodeHTTPClient(),
		notify:            func() {},
		entryIndex:        make(map[string]int),
		messageRole:       make(map[string]string),
		partKind:          make(map[string]TranscriptKind),
		partType:          make(map[string]string),
		modelOptionsByKey: make(map[string]ModelOption),
	}
}
