package boss

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"io"
	"strings"
	"testing"
	"time"
)

const juanHistoryProject = "social_manager--youtube-discovery-optimization"
const juanHistoryDraft = "https://mail.example.test/juan-draft"

func newJuanHistoryFixture(t *testing.T) (*bossSessionStore, bossChatSession, *QueryExecutor) {
	t.Helper()
	ctx := context.Background()
	store := newBossSessionStoreNamed(t.TempDir(), helpChatSessionsDirName)
	now := time.Date(2026, 9, 14, 6, 32, 0, 0, time.UTC)
	session, err := store.createSession(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	messages := []ChatMessage{
		{Role: "user", Content: "Where is the task where we were replying to an email?"},
		{Role: "assistant", Content: "I found it. The task is saved as the project:\n\n**" + juanHistoryProject + "**\n\nIts session contains the discussion:\n\n- You wanted help replying to Juan.\n- The draft begins Hi Juan.\n- It covers trading-strategy monitoring, warnings, and overfitting.\n- It asks for a concrete example."},
		{Role: "assistant", Kind: ChatMessageKindLog, Content: "Work on another-project is ready for review."},
		{Role: "assistant", Kind: ChatMessageKindLog, Content: "Work on " + juanHistoryProject + " is ready for review: Saved an unsent draft in [Juan's thread](" + juanHistoryDraft + ")."},
	}
	for i, message := range messages {
		message.At = now.Add(time.Duration(i) * time.Minute)
		if err := store.appendMessage(ctx, session.SessionID, message); err != nil {
			t.Fatal(err)
		}
	}
	return store, session, newQueryExecutorWithBossSessions(nil, store)
}

func TestChatHistoryJuanSearchAndReadRecoverProjectAndDraft(t *testing.T) {
	t.Parallel()
	_, session, executor := newJuanHistoryFixture(t)
	ctx := context.Background()
	for _, query := range []string{"Juan", "trading", "overfitting"} {
		matches, err := executor.searchChatSessions(ctx, query, 6)
		if err != nil {
			t.Fatal(err)
		}
		if len(matches) != 1 || !strings.Contains(matches[0].Snippet, juanHistoryProject) {
			t.Fatalf("%q lost the project above the match: %#v", query, matches)
		}
		encoded := formatBossSessionSearchXML(query, matches, time.Now())
		for _, want := range []string{`source="help-chat-sessions"`, `index="2"`, `truncated="false"`, "read_chat_session"} {
			if !strings.Contains(encoded, want) {
				t.Errorf("search missing %q: %s", want, encoded)
			}
		}
	}
	req := chatSessionReadRequest{Source: helpChatSessionsDirName, SessionID: session.SessionID}
	plain, _, err := executor.readChatSession(ctx, req, ViewContext{})
	if err != nil {
		t.Fatal(err)
	}
	if len(plain.Turns) != 2 {
		t.Fatalf("default read included operational events: %#v", plain.Turns)
	}
	req.IncludeEvents = true
	exchange, receipt, err := executor.readChatSession(ctx, req, ViewContext{})
	if err != nil {
		t.Fatal(err)
	}
	if len(exchange.Turns) != 4 || exchange.Next != nil {
		t.Fatalf("incomplete exchange: %#v", exchange)
	}
	if !strings.Contains(exchange.Turns[1].Content, juanHistoryProject) || !strings.Contains(exchange.Turns[3].Content, juanHistoryDraft) || exchange.Turns[3].Kind != ChatMessageKindLog {
		t.Fatalf("lost project/draft evidence or event provenance: %#v", exchange)
	}
	for _, want := range []string{session.SessionID, session.Path, "2026-09-14", "messages 1–4", helpChatSessionsDirName} {
		if !strings.Contains(receipt, want) {
			t.Errorf("citation missing %q: %s", want, receipt)
		}
	}
}

func TestChatHistoryReadPagesLongUnicodeMessagesWithoutLoss(t *testing.T) {
	t.Parallel()
	store, session, executor := newJuanHistoryFixture(t)
	content := strings.Repeat("日本語🦊 long history. ", 180)
	if err := store.appendMessage(context.Background(), session.SessionID, ChatMessage{Role: "assistant", Content: content}); err != nil {
		t.Fatal(err)
	}
	req := chatSessionReadRequest{Source: helpChatSessionsDirName, SessionID: session.SessionID, StartTurn: 5, MaxChars: 256}
	var collected strings.Builder
	for pages := 0; ; pages++ {
		if pages > 100 {
			t.Fatal("continuation did not advance")
		}
		result, _, err := executor.readChatSession(context.Background(), req, ViewContext{})
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Turns) != 1 || len([]rune(result.Turns[0].Content)) > 256 {
			t.Fatalf("unbounded page: %#v", result)
		}
		collected.WriteString(result.Turns[0].Content)
		if result.Next == nil {
			break
		}
		if !result.Turns[0].Truncated || result.Next.StartTurn != 5 || result.Next.StartChar <= req.StartChar {
			t.Fatalf("invalid continuation: %#v", result)
		}
		req = *result.Next
	}
	if collected.String() != strings.TrimSpace(content) {
		t.Fatal("pagination lost or duplicated text")
	}
}

func TestChatHistoryReadContinuationPreservesEventMode(t *testing.T) {
	t.Parallel()
	_, session, executor := newJuanHistoryFixture(t)
	req := chatSessionReadRequest{Source: helpChatSessionsDirName, SessionID: session.SessionID, Limit: 2, IncludeEvents: true}
	first, _, err := executor.readChatSession(context.Background(), req, ViewContext{})
	if err != nil {
		t.Fatal(err)
	}
	if first.Next == nil || first.Next.StartTurn != 3 || !first.Next.IncludeEvents {
		t.Fatalf("bad continuation: %#v", first)
	}
	second, _, err := executor.readChatSession(context.Background(), *first.Next, ViewContext{})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Turns) != 2 || second.Turns[0].Index != 3 || second.Next != nil {
		t.Fatalf("bad second page: %#v", second)
	}
}

func TestChatHistoryReadRejectsInvalidTargetsAndPrivacy(t *testing.T) {
	t.Parallel()
	_, session, executor := newJuanHistoryFixture(t)
	base := chatSessionReadRequest{Source: helpChatSessionsDirName, SessionID: session.SessionID}
	for name, change := range map[string]func(*chatSessionReadRequest){
		"path traversal":   func(r *chatSessionReadRequest) { r.SessionID = "../escape" },
		"arbitrary source": func(r *chatSessionReadRequest) { r.Source = "/tmp" },
		"missing source":   func(r *chatSessionReadRequest) { r.Source = bossSessionsDirName },
		"out of range":     func(r *chatSessionReadRequest) { r.StartTurn = 999 },
		"invalid offset":   func(r *chatSessionReadRequest) { r.StartChar = 99999 },
		"unbounded limit":  func(r *chatSessionReadRequest) { r.Limit = 13 },
		"unbounded text":   func(r *chatSessionReadRequest) { r.MaxChars = 12001 },
	} {
		t.Run(name, func(t *testing.T) {
			req := base
			change(&req)
			if _, _, err := executor.readChatSession(context.Background(), req, ViewContext{}); err == nil {
				t.Fatal("accepted invalid read")
			}
		})
	}
	if _, _, err := executor.readChatSession(context.Background(), base, ViewContext{PrivacyMode: true}); err == nil {
		t.Fatal("read leaked mixed private history")
	}
	for _, action := range []bossAction{{Kind: bossActionSearchBossSessions, Query: "Juan"}, {Kind: bossActionContextCommand, Command: `ctx search boss "Juan"`}} {
		if _, err := executor.Execute(context.Background(), action, StateSnapshot{}, ViewContext{PrivacyMode: true}); err == nil {
			t.Fatal("search leaked mixed private history")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := executor.readChatSession(ctx, base, ViewContext{}); err == nil {
		t.Fatal("ignored cancellation")
	}
}

func TestChatHistorySearchRemainsParseableAtMaximumResultCount(t *testing.T) {
	t.Parallel()
	store, session, executor := newJuanHistoryFixture(t)
	for i := 0; i < 16; i++ {
		if err := store.appendMessage(context.Background(), session.SessionID, ChatMessage{Role: "user", Content: "needle " + strings.Repeat("x", 1600)}); err != nil {
			t.Fatal(err)
		}
	}
	result, err := executor.Execute(context.Background(), bossAction{Kind: bossActionSearchBossSessions, Query: "needle", Limit: 16}, StateSnapshot{}, ViewContext{})
	if err != nil {
		t.Fatal(err)
	}
	decoder := xml.NewDecoder(strings.NewReader(result.Text))
	for {
		_, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("search truncated its source envelope: %v", err)
		}
	}
	if strings.Count(result.Text, `truncated="true"`) != 16 {
		t.Fatal("long previews were not labelled")
	}
}

func TestChatHistoryReadLegacySourceIsExplicit(t *testing.T) {
	t.Parallel()
	store, session, executor := newJuanHistoryFixture(t)
	legacy := newBossSessionStoreNamed(t.TempDir(), bossSessionsDirName)
	if err := legacy.appendMessage(context.Background(), session.SessionID, ChatMessage{Role: "user", Content: "legacy evidence"}); err != nil {
		t.Fatal(err)
	}
	executor.bossSessions = append(executor.bossSessions, legacy)
	result, _, err := executor.readChatSession(context.Background(), chatSessionReadRequest{Source: bossSessionsDirName, SessionID: session.SessionID}, ViewContext{})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Turns) != 1 || result.Turns[0].Content != "legacy evidence" || strings.Contains(result.Path, store.dir) {
		t.Fatalf("read wrong source: %#v", result)
	}
	tool := (&Assistant{query: executor}).helpChatReadSessionTool(AssistantRequest{})
	args, _ := json.Marshal(chatSessionReadRequest{Source: bossSessionsDirName, SessionID: session.SessionID})
	if _, err := tool.Run(context.Background(), args); err != nil {
		t.Fatal(err)
	}
}
