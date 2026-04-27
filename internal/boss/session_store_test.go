package boss

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestBossSessionStorePersistsTranscriptFiles(t *testing.T) {
	t.Parallel()

	store := newBossSessionStore(t.TempDir())
	now := time.Date(2026, 4, 27, 9, 0, 0, 0, time.UTC)
	session, err := store.createSession(context.Background(), now)
	if err != nil {
		t.Fatalf("createSession() error = %v", err)
	}
	if session.SessionID == "" || !strings.HasSuffix(session.Path, ".jsonl") {
		t.Fatalf("session = %#v, want jsonl-backed session", session)
	}

	if err := store.appendMessage(context.Background(), session.SessionID, ChatMessage{
		Role:    "user",
		Content: "Which project needs attention first?",
		At:      now.Add(time.Minute),
	}); err != nil {
		t.Fatalf("appendMessage(user) error = %v", err)
	}
	if err := store.appendMessage(context.Background(), session.SessionID, ChatMessage{
		Role:    "assistant",
		Content: "Alpha needs the first look.",
		At:      now.Add(2 * time.Minute),
	}); err != nil {
		t.Fatalf("appendMessage(assistant) error = %v", err)
	}

	loaded, messages, err := store.loadSession(context.Background(), session.SessionID)
	if err != nil {
		t.Fatalf("loadSession() error = %v", err)
	}
	if loaded.Title != "Which project needs attention first?" {
		t.Fatalf("title = %q", loaded.Title)
	}
	if loaded.MessageCount != 2 {
		t.Fatalf("message count = %d, want 2", loaded.MessageCount)
	}
	if len(messages) != 2 || messages[0].Role != "user" || messages[1].Role != "assistant" {
		t.Fatalf("messages = %#v, want user then assistant", messages)
	}
	data, err := os.ReadFile(session.Path)
	if err != nil {
		t.Fatalf("ReadFile(session) error = %v", err)
	}
	if !strings.Contains(string(data), `"type":"message"`) || !strings.Contains(string(data), "Alpha needs") {
		t.Fatalf("session file should be inspectable JSONL, got %q", string(data))
	}
}

func TestBossSessionStoreListsMostRecentFirst(t *testing.T) {
	t.Parallel()

	store := newBossSessionStore(t.TempDir())
	base := time.Date(2026, 4, 27, 9, 0, 0, 0, time.UTC)
	oldSession, err := store.createSession(context.Background(), base)
	if err != nil {
		t.Fatalf("create old session: %v", err)
	}
	newSession, err := store.createSession(context.Background(), base.Add(time.Hour))
	if err != nil {
		t.Fatalf("create new session: %v", err)
	}

	sessions, err := store.listSessions(context.Background(), 10)
	if err != nil {
		t.Fatalf("listSessions() error = %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("sessions len = %d, want 2", len(sessions))
	}
	if sessions[0].SessionID != newSession.SessionID || sessions[1].SessionID != oldSession.SessionID {
		t.Fatalf("sessions order = %#v, want newest first", sessions)
	}
}

func TestBossSessionStoreLoadLatestCreatesFirstSession(t *testing.T) {
	t.Parallel()

	store := newBossSessionStore(t.TempDir())
	session, messages, created, err := store.loadLatestOrCreate(context.Background(), time.Date(2026, 4, 27, 9, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("loadLatestOrCreate() error = %v", err)
	}
	if !created || session.SessionID == "" || len(messages) != 0 {
		t.Fatalf("loadLatestOrCreate() = (%#v, %d messages, %v), want fresh empty session", session, len(messages), created)
	}
}

func TestBossSessionStoreRejectsInvalidSessionID(t *testing.T) {
	t.Parallel()

	store := newBossSessionStore(t.TempDir())
	if _, _, err := store.loadSession(context.Background(), "../escape"); err == nil {
		t.Fatalf("loadSession() should reject path-like session ids")
	}
}

func TestBossSessionTitleClipsLongMessage(t *testing.T) {
	t.Parallel()

	got := bossSessionTitleFromMessage(strings.Repeat("a", 100))
	if len([]rune(got)) != 72 || !strings.HasSuffix(got, "...") {
		t.Fatalf("title = %q, want 72 runes with ellipsis", got)
	}
}
