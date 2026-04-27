package boss

import (
	"context"
	"strings"
	"testing"
	"time"

	"lcroom/internal/config"
	"lcroom/internal/service"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestBossSlashTabCyclesSuggestions(t *testing.T) {
	t.Parallel()

	m := NewEmbedded(context.Background(), nil)
	m.input.SetValue("/")

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	got := updated.(Model)
	if cmd != nil {
		t.Fatalf("tab completion should not queue a command")
	}
	if got.input.Value() != "/new" {
		t.Fatalf("input = %q, want /new", got.input.Value())
	}

	updated, cmd = got.Update(tea.KeyMsg{Type: tea.KeyTab})
	got = updated.(Model)
	if cmd != nil {
		t.Fatalf("tab cycling should not queue a command")
	}
	if got.input.Value() != "/sessions" {
		t.Fatalf("input = %q, want /sessions", got.input.Value())
	}
}

func TestBossSlashNewCreatesFreshFileSession(t *testing.T) {
	t.Parallel()

	svc := newBossSessionTestService(t)
	m := NewEmbedded(context.Background(), svc)
	loadedMsg := m.loadLatestBossSessionCmd()().(bossSessionLoadedMsg)
	updated, _ := m.Update(loadedMsg)
	m = updated.(Model)
	firstSessionID := m.sessionID
	m.messages = []ChatMessage{{Role: "user", Content: "old chat", At: time.Now()}}
	m.input.SetValue("/new")

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("/new should create a fresh session")
	}
	msg := cmd()
	loaded, ok := msg.(bossSessionLoadedMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want bossSessionLoadedMsg", msg)
	}
	updated, _ = got.Update(loaded)
	got = updated.(Model)
	if got.sessionID == "" || got.sessionID == firstSessionID {
		t.Fatalf("session id = %q, want new id different from %q", got.sessionID, firstSessionID)
	}
	if len(got.messages) != 0 {
		t.Fatalf("messages len = %d, want fresh transcript", len(got.messages))
	}
}

func TestBossLoadsLatestFileSessionOnOpen(t *testing.T) {
	t.Parallel()

	svc := newBossSessionTestService(t)
	store := newBossSessionStore(svc.Config().DataDir)
	now := time.Date(2026, 4, 27, 9, 0, 0, 0, time.UTC)
	session, err := store.createSession(context.Background(), now)
	if err != nil {
		t.Fatalf("createSession() error = %v", err)
	}
	if err := store.appendMessage(context.Background(), session.SessionID, ChatMessage{Role: "user", Content: "What is hot?", At: now.Add(time.Minute)}); err != nil {
		t.Fatalf("append user: %v", err)
	}
	if err := store.appendMessage(context.Background(), session.SessionID, ChatMessage{Role: "assistant", Content: "Alpha is hot.", At: now.Add(2 * time.Minute)}); err != nil {
		t.Fatalf("append assistant: %v", err)
	}

	m := NewEmbedded(context.Background(), svc)
	loadedMsg := m.loadLatestBossSessionCmd()().(bossSessionLoadedMsg)
	updated, _ := m.Update(loadedMsg)
	got := updated.(Model)
	if got.sessionID != session.SessionID {
		t.Fatalf("loaded session id = %q, want %q", got.sessionID, session.SessionID)
	}
	if len(got.messages) != 2 || got.messages[1].Content != "Alpha is hot." {
		t.Fatalf("messages = %#v, want persisted transcript", got.messages)
	}
	if got.sessionTitle != "What is hot?" {
		t.Fatalf("session title = %q", got.sessionTitle)
	}
}

func TestBossSlashSessionsOpensPickerAndLoadsSelection(t *testing.T) {
	t.Parallel()

	svc := newBossSessionTestService(t)
	store := newBossSessionStore(svc.Config().DataDir)
	now := time.Date(2026, 4, 27, 9, 0, 0, 0, time.UTC)
	older, err := store.createSession(context.Background(), now)
	if err != nil {
		t.Fatalf("createSession() error = %v", err)
	}
	if err := store.appendMessage(context.Background(), older.SessionID, ChatMessage{Role: "user", Content: "Older topic", At: now}); err != nil {
		t.Fatalf("append message: %v", err)
	}
	newer, err := store.createSession(context.Background(), now.Add(time.Hour))
	if err != nil {
		t.Fatalf("createSession() second error = %v", err)
	}
	if err := store.appendMessage(context.Background(), newer.SessionID, ChatMessage{Role: "user", Content: "Newer topic", At: now.Add(time.Hour)}); err != nil {
		t.Fatalf("append second message: %v", err)
	}

	m := NewEmbedded(context.Background(), svc)
	m.sessionLoaded = true
	m.sessionID = newer.SessionID
	m.input.SetValue("/sessions")
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := updated.(Model)
	if cmd == nil {
		t.Fatalf("/sessions should load the saved session list")
	}
	if !got.sessionPickerVisible || !got.sessionPickerLoading {
		t.Fatalf("/sessions should open a loading picker: visible=%v loading=%v", got.sessionPickerVisible, got.sessionPickerLoading)
	}
	msg := cmd()
	listed, ok := msg.(bossSessionsListedMsg)
	if !ok {
		t.Fatalf("cmd() returned %T, want bossSessionsListedMsg", msg)
	}
	updated, _ = got.Update(listed)
	got = updated.(Model)
	if !got.sessionPickerVisible || got.sessionPickerLoading {
		t.Fatalf("listed sessions should keep picker open and stop loading: visible=%v loading=%v", got.sessionPickerVisible, got.sessionPickerLoading)
	}
	if len(got.messages) != 0 {
		t.Fatalf("messages len = %d, want picker without transcript noise", len(got.messages))
	}
	rendered := ansi.Strip(got.View())
	if !strings.Contains(rendered, "Boss Sessions") || !strings.Contains(rendered, "Newer topic") {
		t.Fatalf("picker view missing saved sessions:\n%s", rendered)
	}

	updated, cmd = got.Update(tea.KeyMsg{Type: tea.KeyDown})
	got = updated.(Model)
	if cmd != nil {
		t.Fatalf("down in picker should not queue a command")
	}
	if got.sessionPickerSelected != 1 {
		t.Fatalf("sessionPickerSelected = %d, want 1", got.sessionPickerSelected)
	}
	updated, cmd = got.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if cmd == nil {
		t.Fatalf("enter in picker should load selected session")
	}
	if got.sessionPickerVisible {
		t.Fatalf("picker should close before loading selected session")
	}
	loadMsg := cmd()
	loaded, ok := loadMsg.(bossSessionLoadedMsg)
	if !ok {
		t.Fatalf("load cmd returned %T, want bossSessionLoadedMsg", loadMsg)
	}
	updated, _ = got.Update(loaded)
	got = updated.(Model)
	if got.sessionID != older.SessionID {
		t.Fatalf("loaded session id = %q, want selected older session %q", got.sessionID, older.SessionID)
	}
	if len(got.messages) != 1 || got.messages[0].Content != "Older topic" {
		t.Fatalf("messages = %#v, want selected session transcript", got.messages)
	}
}

func TestBossSlashSuggestionsKeepInputVisibleInShortEmbeddedView(t *testing.T) {
	t.Parallel()

	m := NewEmbedded(context.Background(), nil)
	m.width = 120
	m.height = 16
	m.input.SetValue("/")
	m.syncLayout(false)

	rendered := ansi.Strip(m.View())
	if !strings.Contains(rendered, "Boss Slash Commands") {
		t.Fatalf("slash suggestions should render when there is room:\n%s", rendered)
	}
	if !strings.Contains(rendered, "> /") {
		t.Fatalf("slash input should remain visible with suggestions:\n%s", rendered)
	}
}

func newBossSessionTestService(t *testing.T) *service.Service {
	t.Helper()
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	cfg.DBPath = cfg.DataDir + "/little-control-room.sqlite"
	return service.New(cfg, nil, nil, nil)
}
