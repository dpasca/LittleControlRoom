package boss

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"lcroom/internal/config"
	"lcroom/internal/control"
	"lcroom/internal/lcagent/modeladapter"
	"lcroom/internal/model"

	tea "github.com/charmbracelet/bubbletea"
)

func historyToolCompletion(name string, args any) modeladapter.Completion {
	raw, _ := json.Marshal(args)
	return modeladapter.Completion{
		Model: "test-model",
		Message: modeladapter.Message{Role: "assistant", ToolCalls: []modeladapter.ToolCall{{
			ID: name, Type: "function", Function: modeladapter.FunctionCall{Name: name, Arguments: raw},
		}}},
		UsageSummary: model.LLMUsage{InputTokens: 10, OutputTokens: 2, TotalTokens: 12},
	}
}

func TestHelpChatHistoryEvidenceAndCitationSurviveFollowupAndReload(t *testing.T) {
	t.Parallel()
	store, historical, executor := newJuanHistoryFixture(t)
	current, err := store.createSession(context.Background(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	client := &scriptedHelpChatModel{model: "test-model", completions: []modeladapter.Completion{
		historyToolCompletion("search_chat_sessions", map[string]any{"query": "Juan"}),
		historyToolCompletion("read_chat_session", chatSessionReadRequest{Source: helpChatSessionsDirName, SessionID: historical.SessionID, IncludeEvents: true}),
		{Model: "test-model", Message: modeladapter.Message{Role: "assistant", Content: "The old exchange names " + juanHistoryProject + " and records an unsent draft at " + juanHistoryDraft + ". Its current project location is unverified."}},
	}}
	assistant := &Assistant{agentModel: client, agentProvider: "openrouter", query: executor, model: "test-model", backend: config.AIBackendOpenRouter}
	response, err := assistant.Reply(context.Background(), AssistantRequest{HelpChat: true, SessionID: current.SessionID, Messages: []ChatMessage{{Role: "user", Content: "Where was the email work with Juan?"}}})
	if err != nil {
		t.Fatal(err)
	}
	evidence := client.requests[2][len(client.requests[2])-1].Content
	// Check what the model actually receives, independently of its scripted answer.
	for _, want := range []string{juanHistoryProject, juanHistoryDraft, `"kind":"log"`, `"source":"help-chat-sessions"`} {
		if !strings.Contains(evidence, want) {
			t.Errorf("synthesis missing source evidence %q: %s", want, evidence)
		}
	}
	if !strings.Contains(response.Content, "History source:") || !strings.Contains(response.Content, historical.SessionID) {
		t.Fatalf("missing host citation: %s", response.Content)
	}
	ui := NewEmbeddedHelp(context.Background(), nil)
	ui.sessionStore, ui.sessionID, ui.sessionLoaded = store, current.SessionID, true
	_, save := ui.applyAssistantReply(response, nil, StateSnapshot{}, nil, false)
	runBossTestCmd(save)
	_, saved, err := store.loadSession(context.Background(), current.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	client.completions = []modeladapter.Completion{{Model: "test-model", Message: modeladapter.Message{Role: "assistant", Content: "It came from your saved September 14 Chat."}}}
	// A fresh assistant instance, as after restart, gets the original citation
	// through the ordinary transcript; there is no in-memory retrieval cache.
	restarted := &Assistant{agentModel: client, agentProvider: "openrouter", query: executor, model: "test-model", backend: config.AIBackendOpenRouter}
	saved = append(saved, ChatMessage{Role: "user", Content: "Where did you find this?"})
	if _, err := restarted.Reply(context.Background(), AssistantRequest{HelpChat: true, SessionID: current.SessionID, Messages: saved}); err != nil {
		t.Fatal(err)
	}
	request := client.requests[len(client.requests)-1]
	var priorAnswer string
	for _, message := range request {
		if message.Role == "assistant" {
			priorAnswer += message.Content
		}
	}
	for _, want := range []string{historical.SessionID, historical.Path, "2026-09-14", "messages 1–4"} {
		if !strings.Contains(priorAnswer, want) {
			t.Errorf("follow-up lost %q: %s", want, priorAnswer)
		}
	}
	if len(client.requests) != 4 {
		t.Fatal("follow-up needed another search")
	}
}

func TestHelpChatTimeoutPreservesSourcesUsageAndStage(t *testing.T) {
	t.Parallel()
	_, historical, executor := newJuanHistoryFixture(t)
	client := &scriptedHelpChatModel{model: "test-model", err: context.DeadlineExceeded, completions: []modeladapter.Completion{
		historyToolCompletion("read_chat_session", chatSessionReadRequest{Source: helpChatSessionsDirName, SessionID: historical.SessionID, IncludeEvents: true}),
	}}
	assistant := &Assistant{agentModel: client, agentProvider: "openrouter", query: executor, model: "test-model", backend: config.AIBackendOpenRouter}
	response, err := assistant.Reply(context.Background(), AssistantRequest{HelpChat: true, Messages: []ChatMessage{{Role: "user", Content: "Find that old email work."}}})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v", err)
	}
	var failure chatReplyError
	if !errors.As(err, &failure) || failure.Stage != "waiting for the chat model (turn 2)" {
		t.Fatalf("lost stage: %v", err)
	}
	if response.Usage.InputTokens != 10 || !strings.Contains(response.Content, historical.SessionID) {
		t.Fatalf("lost partial response: %#v", response)
	}
	m := NewEmbeddedHelp(context.Background(), nil)
	now := time.Now()
	m.nowFn = func() time.Time { return now }
	m.assistantStartedAt = now.Add(-2 * time.Minute)
	m.haveLastAssistantUsage, m.lastAssistantUsage = true, model.LLMUsage{InputTokens: 172000}
	updated, _ := m.applyAssistantReply(response, err, StateSnapshot{}, nil, false)
	got := updated.(Model)
	text := got.messages[len(got.messages)-1].Content
	for _, want := range []string{"Chat timed out after 2m0s", "waiting for the chat model (turn 2)", historical.SessionID} {
		if !strings.Contains(text, want) {
			t.Errorf("timeout lost %q: %s", want, text)
		}
	}
	if strings.Contains(text, "could not reach") || got.lastAssistantUsage.InputTokens != 10 {
		t.Fatalf("misleading failure: %s, usage %#v", text, got.lastAssistantUsage)
	}
}

func TestHelpChatFailureWithoutUsageClearsPreviousTurnTokens(t *testing.T) {
	t.Parallel()
	m := NewEmbeddedHelp(context.Background(), nil)
	m.haveLastAssistantUsage, m.lastAssistantUsage = true, model.LLMUsage{InputTokens: 172000}
	updated, _ := m.applyAssistantReply(AssistantResponse{}, context.DeadlineExceeded, StateSnapshot{}, nil, false)
	got := updated.(Model)
	if got.haveLastAssistantUsage || strings.Contains(got.UsageText(), "172k") {
		t.Fatalf("stale usage: %s", got.UsageText())
	}
}

func TestAssistantStreamDeadlineKeepsFinalEnvelope(t *testing.T) {
	t.Parallel()
	// Expiry of the request budget is data in the result, not cancellation of
	// the UI consumer. Delivery must retain that result even under backpressure.
	host, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := make(chan assistantStreamEnvelope, 1)
	events <- assistantStreamEnvelope{}
	done := make(chan struct{})
	want := assistantStreamEnvelope{done: true, err: chatReplyError{Stage: "reading saved Chat history", Err: context.DeadlineExceeded}, response: AssistantResponse{Content: "History source: retained"}}
	go func() { sendAssistantStreamReply(host, events, want); close(done) }()
	<-events
	got := <-events
	<-done
	if !got.done || !errors.Is(got.err, context.DeadlineExceeded) || got.response.Content != want.response.Content {
		t.Fatalf("lost final result: %#v", got)
	}
}

func TestHelpChatWorktreeReceiptsStayInLogAcrossReload(t *testing.T) {
	t.Parallel()
	store, session, executor := newJuanHistoryFixture(t)
	m := NewEmbeddedHelp(context.Background(), nil)
	m.sessionStore, m.sessionID, m.sessionLoaded = store, session.SessionID, true
	const receipt = "Worktree removed; branch and conversation history preserved"
	updated, save := m.Update(ControlInvocationResultMsg{
		Invocation: control.Invocation{Capability: control.CapabilityWorktreeRemove},
		Status:     receipt, AnnounceInChat: true,
	})
	runBossTestCmd(save)
	got := updated.(Model)
	if strings.Contains(got.renderTranscript(100), receipt) {
		t.Fatal("worktree event appeared in Chat")
	}
	_, messages, err := store.loadSession(context.Background(), session.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if messages[len(messages)-1].Kind != ChatMessageKindLog {
		t.Fatal("worktree event was saved as conversation")
	}
	matches, err := executor.searchChatSessions(context.Background(), "Worktree removed", 6)
	if err != nil || len(matches) != 0 {
		t.Fatalf("worktree event leaked into recall: %#v, %v", matches, err)
	}
}

func TestHelpChatSessionsCommandOpensHistoryWithoutModelCall(t *testing.T) {
	t.Parallel()
	store, historical, _ := newJuanHistoryFixture(t)
	m := NewEmbeddedHelp(context.Background(), nil)
	m.sessionStore, m.sessionLoaded = store, true
	m.input.SetValue("/sessions")
	updated, list := m.submit()
	got := updated.(Model)
	if got.sending || !got.sessionPickerVisible || !got.sessionPickerLoading || list == nil || len(got.messages) != 0 {
		t.Fatalf("history was sent to the model: %#v", got)
	}
	updated, _ = got.Update(list())
	got = updated.(Model)
	if len(got.sessionPickerSessions) != 1 || got.sessionPickerLoading {
		t.Fatal("history did not load")
	}
	updated, read := got.updateBossSessionPicker(tea.KeyMsg{Type: tea.KeyEnter})
	got = updated.(Model)
	if read == nil || got.sessionPickerVisible {
		t.Fatal("selection did not start loading")
	}
	updated, _ = got.Update(read())
	got = updated.(Model)
	if got.sessionID != historical.SessionID || len(got.messages) != 4 {
		t.Fatal("did not open original history")
	}
	// Direct source references open the same history without sending a prompt.
	got.input.SetValue("/sessions " + historical.SessionID)
	updated, read = got.submit()
	got = updated.(Model)
	if read == nil || got.sending || got.sessionLoaded {
		t.Fatal("direct history command did not queue a local read")
	}
}

func TestHelpChatSessionsCommandDoesNotSwitchWhileBusy(t *testing.T) {
	t.Parallel()
	store, historical, _ := newJuanHistoryFixture(t)
	m := NewEmbeddedHelp(context.Background(), nil)
	m.sessionStore, m.sessionID, m.sessionLoaded, m.sending = store, historical.SessionID, true, true
	m.input.SetValue("/sessions")
	updated, cmd := m.submit()
	got := updated.(Model)
	if cmd != nil || !got.sending || got.sessionPickerVisible || got.sessionID != historical.SessionID {
		t.Fatal("history interrupted the current reply")
	}
	if !strings.Contains(got.status, "Chat is busy") {
		t.Fatalf("missing busy state: %s", got.status)
	}
}
