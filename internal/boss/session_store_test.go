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
	if session.SessionID == "" || !strings.HasSuffix(session.Path, ".md") {
		t.Fatalf("session = %#v, want markdown-backed session", session)
	}

	if err := store.appendMessage(context.Background(), session.SessionID, ChatMessage{
		Role:    "user",
		Content: "Which project needs \"attention\" first?\nPlease keep this grep-friendly.",
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
	if loaded.Title != "Which project needs \"attention\" first? Please keep this grep-friendly." {
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
	text := string(data)
	if !strings.Contains(text, "## User @ ") || !strings.Contains(text, "Which project needs \"attention\" first?") || strings.Contains(text, `\"attention\"`) {
		t.Fatalf("session file should be a grep-friendly markdown transcript, got %q", text)
	}
	if !strings.Contains(text, "## Assistant @ ") || !strings.Contains(text, "Alpha needs the first look.") {
		t.Fatalf("session file missing assistant transcript, got %q", text)
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

func TestBossSessionStoreLoadLatestPrefersNonEmptySession(t *testing.T) {
	t.Parallel()

	store := newBossSessionStore(t.TempDir())
	base := time.Date(2026, 4, 27, 9, 0, 0, 0, time.UTC)
	older, err := store.createSession(context.Background(), base)
	if err != nil {
		t.Fatalf("create older session: %v", err)
	}
	if err := store.appendMessage(context.Background(), older.SessionID, ChatMessage{Role: "user", Content: "Keep this conversation alive", At: base.Add(time.Minute)}); err != nil {
		t.Fatalf("append older message: %v", err)
	}
	if _, err := store.createSession(context.Background(), base.Add(time.Hour)); err != nil {
		t.Fatalf("create newer empty session: %v", err)
	}

	session, messages, created, err := store.loadLatestOrCreate(context.Background(), base.Add(2*time.Hour))
	if err != nil {
		t.Fatalf("loadLatestOrCreate() error = %v", err)
	}
	if created {
		t.Fatalf("loadLatestOrCreate() created a new session, want existing non-empty session")
	}
	if session.SessionID != older.SessionID {
		t.Fatalf("loaded session = %q, want older non-empty %q", session.SessionID, older.SessionID)
	}
	if len(messages) != 1 || messages[0].Content != "Keep this conversation alive" {
		t.Fatalf("messages = %#v, want older transcript", messages)
	}
}

func TestBossSessionStoreAppendCreatesReadableMarkdownFile(t *testing.T) {
	t.Parallel()

	store := newBossSessionStore(t.TempDir())
	now := time.Date(2026, 4, 27, 10, 0, 0, 0, time.UTC)
	if err := store.appendMessage(context.Background(), "boss_manual_session", ChatMessage{Role: "user", Content: "Find the old launch notes", At: now}); err != nil {
		t.Fatalf("appendMessage() error = %v", err)
	}
	path, err := store.sessionPath("boss_manual_session")
	if err != nil {
		t.Fatalf("sessionPath() error = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(session) error = %v", err)
	}
	text := string(data)
	for _, want := range []string{"# Boss Chat Session", "Session: boss_manual_session", "## User @ ", "Find the old launch notes"} {
		if !strings.Contains(text, want) {
			t.Fatalf("session markdown missing %q:\n%s", want, text)
		}
	}
}

func TestBossSessionStoreSearchesMarkdownTurns(t *testing.T) {
	t.Parallel()

	store := newBossSessionStore(t.TempDir())
	now := time.Date(2026, 4, 27, 10, 0, 0, 0, time.UTC)
	session, err := store.createSession(context.Background(), now)
	if err != nil {
		t.Fatalf("createSession() error = %v", err)
	}
	if err := store.appendMessage(context.Background(), session.SessionID, ChatMessage{
		Role:    "user",
		Content: "Remember the launch notes?\nThey mention <xml> and \"quotes\".",
		At:      now.Add(time.Minute),
	}); err != nil {
		t.Fatalf("appendMessage() error = %v", err)
	}

	matches, err := store.searchSessions(context.Background(), "LAUNCH", 4)
	if err != nil {
		t.Fatalf("searchSessions() error = %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("matches len = %d, want 1", len(matches))
	}
	if matches[0].Turn.Role != "user" || !strings.Contains(matches[0].Snippet, "<xml>") || !strings.Contains(matches[0].Snippet, `"quotes"`) {
		t.Fatalf("match = %#v, want raw grep-friendly snippet", matches[0])
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
